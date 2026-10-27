"""Lightpanda page-reading runtime for the canonical browser Skill.

Reads public pages and search results as bounded Markdown with Lightpanda. It
holds no browser session, profile, cookies or credentials. Interactive
browsing lives in the separate live-browser Skill.
"""

from __future__ import annotations

import json
import os
import re
import subprocess
import time
import urllib.error
import urllib.request
from concurrent.futures import ThreadPoolExecutor
from datetime import datetime, timezone
from email.utils import parsedate_to_datetime
from urllib.parse import quote_plus, urlparse

VERSION = "3.1.0"
SKILL_ID = "skill-browser"
MAX_LIGHTPANDA_TEXT = 24 * 1024
MAX_LIGHTPANDA_BATCH_TEXT = 6 * 1024
SEARCH_URL = "https://search.yahoo.com/search?p="
TAVILY_URL = "https://api.tavily.com/search"
TAVILY_TIMEOUT_SECONDS = 10
TAVILY_MAX_RESULTS = 8
MAX_TAVILY_RESPONSE_BYTES = 1024 * 1024
# A source's own Retry-After at or below this is waited out once in-action.
MAX_INLINE_RETRY_AFTER_SECONDS = 10
# Host-owned config key (sentinel injects it; the action schemas never expose it).
SEARCH_PROVIDER_CONFIG_KEY = "searchProvider"
SEARCH_API_KEY_BINDING = "searchApiKey"
SEARCH_PROVIDERS = ("yahoo", "tavily")
SEARCH_FALLBACKS = ("yahoo", None)
API_KEY = re.compile(r"[\x21-\x7e]{1,512}")

CAPTCHA = re.compile(r"\b(captcha|recaptcha|hcaptcha|verify you are human)\b", re.I)
# Challenge language or provider markers, never a vendor name by itself.
ANTI_BOT = re.compile(
    r"\b(access denied|unusual traffic|bot detection|security check|js_challenge|"
    r"checking your browser|performing security verification|cloudflare ray id)\b|"
    r"(?:/cdn-cgi/challenge-platform/|cf-chl-)",
    re.I,
)
ERROR_PAGE_CHALLENGE = re.compile(
    r"\b(bot detection|security check|js_challenge|checking your browser|"
    r"performing security verification|cloudflare ray id)\b|"
    r"(?:/cdn-cgi/challenge-platform/|cf-chl-)",
    re.I,
)


class BrowserActionFailure(RuntimeError):
    """A classified failure with bounded metadata, never an upstream error body."""

    def __init__(self, code, message, *, http_status=None, retry_after_seconds=None,
                 failures=None):
        super().__init__(message)
        self.code = code
        self.http_status = http_status
        self.retry_after_seconds = retry_after_seconds
        self.failures = failures

    def failure_details(self):
        # The manifest permits one attempt. A retry hint informs the user; it
        # does not authorize retries or switching to another browser.
        details = {"failureKind": self.code, "retryable": "false"}
        if self.http_status is not None:
            details["httpStatus"] = str(self.http_status)
        if self.retry_after_seconds is not None:
            details["retryAfterSeconds"] = str(self.retry_after_seconds)
        if self.failures is not None:
            details.update({"failedCount": str(len(self.failures)),
                            "totalCount": str(len(self.failures)),
                            "failures": json.dumps(self.failures, separators=(",", ":"))})
        return details


def navigation_url(value):
    if not isinstance(value, str) or len(value) > 2048:
        raise ValueError("navigation URL is invalid")
    parsed = urlparse(value)
    if parsed.scheme not in ("http", "https") or not parsed.hostname or parsed.username or parsed.password:
        raise ValueError("navigation URL must be an HTTP(S) URL without embedded credentials")
    return value


class SearchSettings:
    """The host-resolved search provider for one action, with its credential."""

    __slots__ = ("kind", "fallback", "api_key")

    def __init__(self, kind="yahoo", fallback=None, api_key=None):
        self.kind = kind
        self.fallback = fallback
        self.api_key = api_key

    def __repr__(self):
        # Never render the credential.
        return f"SearchSettings(kind={self.kind!r}, fallback={self.fallback!r})"


def search_settings(raw, bindings):
    """Validate the host-injected searchProvider; absent means Yahoo."""
    if raw is None:
        return SearchSettings()
    if (not isinstance(raw, dict) or "kind" not in raw or not set(raw) <= {"kind", "fallback"}
            or raw["kind"] not in SEARCH_PROVIDERS or raw.get("fallback") not in SEARCH_FALLBACKS):
        raise ValueError("searchProvider must be {kind: yahoo|tavily, fallback: yahoo|null}")
    key = None
    if raw["kind"] == "tavily":
        candidate = (bindings or {}).get(SEARCH_API_KEY_BINDING)
        if isinstance(candidate, str) and API_KEY.fullmatch(candidate.strip()):
            key = candidate.strip()
    return SearchSettings(raw["kind"], raw.get("fallback"), key)


def search_provider_unconfigured():
    return BrowserActionFailure("search_provider_unconfigured",
                                "Web search is set to Tavily but no usable Tavily API key is configured. "
                                "An operator must set the key in HelloSeal settings.")


def source_retry_after(result, now=None):
    """Use an actual response header when available; never invent a delay."""
    headers = result.get("headers")
    if not isinstance(headers, dict):
        return None
    values = [v for k, v in headers.items() if isinstance(k, str) and k.lower() == "retry-after"]
    if len(values) != 1 or not isinstance(values[0], str) or len(values[0]) > 128:
        return None
    if "\r" in values[0] or "\n" in values[0]:
        return None
    value = values[0].strip()
    if re.fullmatch(r"[0-9]{1,5}", value):
        delay = int(value)
    else:
        try:
            date = parsedate_to_datetime(value)
            if date.tzinfo is None:
                return None
            seconds = (date - (now or datetime.now(timezone.utc))).total_seconds()
            delay = max(0, int(seconds) + (seconds % 1 > 0))
        except (TypeError, ValueError, OverflowError):
            return None
    return delay if 0 <= delay <= 86400 else None


def source_http_failure(status, retry_after_seconds=None):
    if status == 429:
        advice = (f"Try again after {retry_after_seconds} seconds."
                  if retry_after_seconds is not None else "Try again later.")
        return BrowserActionFailure("source_rate_limited",
                                    f"This source returned HTTP 429 (rate limited). {advice}",
                                    http_status=429, retry_after_seconds=retry_after_seconds)
    return BrowserActionFailure("source_http_error", f"This source returned HTTP {status}; the page could not be read.",
                                http_status=status, retry_after_seconds=retry_after_seconds)


def source_has_access_challenge(status, content):
    if not isinstance(content, str) or len(content) >= 1024:
        return False
    if status == 200:
        return bool(ANTI_BOT.search(content))
    if status not in (403, 429, 503):
        return False
    # A bare forbidden/access-denied page is not evidence of bot detection.
    # Error-status pages need an explicit challenge instruction or marker.
    return bool(ERROR_PAGE_CHALLENGE.search(content) or CAPTCHA.search(content))


class LightpandaRuntime:
    def __init__(self, binary=None, run=None, urlopen=None, sleep=None):
        self.binary = binary or os.environ.get("LIGHTPANDA_BINARY", "/usr/local/bin/lightpanda")
        self._run = run
        self._urlopen = urlopen or urllib.request.urlopen
        self._sleep = sleep or time.sleep

    def execute(self, action, config=None, bindings=None, context=None):
        # The host-owned provider key is removed before any action reads its
        # model-supplied arguments, so the actions only ever see their schema.
        config = dict(config or {})
        settings = search_settings(config.pop(SEARCH_PROVIDER_CONFIG_KEY, None), bindings)
        handlers = {
            "lightpanda-fetch": lambda: self.lightpanda_fetch(config),
            "lightpanda-search": lambda: self.lightpanda_search(config, settings),
            "lightpanda-read-many": lambda: self.lightpanda_read_many(config, settings),
        }
        if action not in handlers:
            raise ValueError(f"unsupported browser action {action}")
        return handlers[action]()

    def ready(self):
        return os.access(self.binary, os.X_OK)

    def lightpanda_fetch(self, config):
        """Read a public page; private/internal networks are blocked after DNS."""
        url = navigation_url(config.get("url"))
        completed, result = self._lightpanda_read(url)
        status = result.get("http_status")
        if type(status) is int and status == 429 and not source_has_access_challenge(status, result.get("content")):
            delay = source_retry_after(result)
            if delay is not None and delay <= MAX_INLINE_RETRY_AFTER_SECONDS:
                self._sleep(delay)
                completed, result = self._lightpanda_read(url)
        return self._lightpanda_page(url, completed, result)

    def _lightpanda_read(self, url):
        command = [
            self.binary,
            "fetch", url, "--json", "--dump", "markdown", "--strip-mode", "clutter",
            "--strip-mode", "ui", "--dump-max-bytes", str(MAX_LIGHTPANDA_TEXT), "--block-private-networks",
            "--terminate-ms", "12000", "--http-timeout", "8000",
            "--log-level", "error", "--log-filter", "note",
        ]
        run = self._run or subprocess.run
        try:
            completed = run(command, capture_output=True, text=True, timeout=15, check=False)
        except FileNotFoundError as exc:
            raise BrowserActionFailure("source_unavailable", "The page reader is unavailable. The platform must restore it before retrying.") from exc
        except subprocess.TimeoutExpired as exc:
            raise BrowserActionFailure("source_unavailable", "The source did not respond before the page read timed out. Try again later.") from exc
        try:
            result = json.loads(completed.stdout)
        except (json.JSONDecodeError, TypeError) as exc:
            raise BrowserActionFailure("source_invalid_response", "The page reader did not return a valid page result.") from exc
        if not isinstance(result, dict):
            raise BrowserActionFailure("source_invalid_response", "The page reader did not return a valid page result.")
        return completed, result

    def _lightpanda_page(self, url, completed, result):
        status = result.get("http_status")
        # A short challenge body (captcha, bot check) is an access challenge
        # whatever the status; a plain 429 stays rate limiting. Neither
        # authorizes switching to another browser.
        if type(status) is int and source_has_access_challenge(status, result.get("content")):
            raise BrowserActionFailure("source_access_challenge",
                                       f"This source returned an access challenge (HTTP {status}) instead of readable page content.",
                                       http_status=status)
        if type(status) is int and status == 429:
            raise source_http_failure(status, source_retry_after(result))
        if type(status) is int and 100 <= status <= 599 and status != 200:
            raise source_http_failure(status, source_retry_after(result))
        if completed.returncode != 0 or result.get("error"):
            raise BrowserActionFailure("source_unavailable", "The source could not be reached for this page read. Try again later.")
        if status != 200 or type(status) is not int:
            raise BrowserActionFailure("source_invalid_response", "The page reader did not return a valid HTTP status.")
        content = result.get("content")
        if not isinstance(content, str):
            raise BrowserActionFailure("source_invalid_response", "The page reader returned no page content.")
        if not content.strip():
            raise BrowserActionFailure("source_empty_response", "This source returned an empty page. Try another permitted source.")
        return {
            "url": navigation_url(result.get("url", url)),
            "httpStatus": status,
            "text": content[:MAX_LIGHTPANDA_TEXT],
            "truncated": len(content) > MAX_LIGHTPANDA_TEXT or content.endswith("[truncated]"),
        }

    def lightpanda_search(self, config, settings=None):
        settings = settings or SearchSettings()
        query = config.get("query")
        if not isinstance(query, str) or not query.strip() or len(query) > 512:
            raise ValueError("search query must be between 1 and 512 characters")
        query = query.strip()
        if settings.kind == "tavily":
            if settings.api_key is None:
                raise search_provider_unconfigured()
            try:
                return self.tavily_search(query, settings.api_key, retry=settings.fallback != "yahoo")
            except BrowserActionFailure as exc:
                # Fallback covers Tavily being unavailable (rate limit, quota,
                # 5xx, unreachable). A rejected key is an operator error and
                # must surface, not hide behind Yahoo. The Tavily endpoint URL
                # is how callers tell which provider served.
                if settings.fallback != "yahoo" or exc.code == "search_provider_authentication_failed":
                    raise
        return self.lightpanda_fetch({"url": SEARCH_URL + quote_plus(query)})

    def tavily_search(self, query, api_key, *, retry=True):
        """One Tavily Search API call; the key travels only in the header."""
        body = json.dumps({"query": query, "max_results": TAVILY_MAX_RESULTS,
                           "search_depth": "basic"}).encode("utf-8")
        request = urllib.request.Request(TAVILY_URL, data=body, method="POST", headers={
            "Authorization": f"Bearer {api_key}", "Content-Type": "application/json",
            "Accept": "application/json",
        })
        try:
            with self._urlopen(request, timeout=TAVILY_TIMEOUT_SECONDS) as response:
                status = response.status
                raw = response.read(MAX_TAVILY_RESPONSE_BYTES + 1)
        except urllib.error.HTTPError as exc:
            status = exc.code
            retry_after = source_retry_after({"headers": {"Retry-After": exc.headers.get("Retry-After")}}
                                             if exc.headers is not None else {})
            exc.close()
            # With a Yahoo fallback, answering from Yahoo beats waiting.
            if status == 429 and retry and retry_after is not None \
                    and retry_after <= MAX_INLINE_RETRY_AFTER_SECONDS:
                self._sleep(retry_after)
                return self.tavily_search(query, api_key, retry=False)
            raise tavily_http_failure(status, retry_after) from None
        except (urllib.error.URLError, TimeoutError, OSError):
            raise BrowserActionFailure("source_unavailable",
                                       "The Tavily search service could not be reached. Try again later.") from None
        if status != 200:
            raise tavily_http_failure(status, None)
        try:
            if len(raw) > MAX_TAVILY_RESPONSE_BYTES:
                raise ValueError("oversized")
            results = json.loads(raw.decode("utf-8")).get("results")
            if not isinstance(results, list):
                raise ValueError("no results list")
        except (ValueError, AttributeError, UnicodeDecodeError):
            raise BrowserActionFailure("source_invalid_response",
                                       "The Tavily search service did not return valid search results.") from None
        text = tavily_markdown(results)
        return {"url": TAVILY_URL, "httpStatus": 200, "text": text[:MAX_LIGHTPANDA_TEXT],
                "truncated": len(text) > MAX_LIGHTPANDA_TEXT}

    def lightpanda_read_many(self, config, settings=None):
        """Run independent public reads concurrently and retain each result."""
        reads = config.get("reads")
        if not isinstance(reads, list) or not 2 <= len(reads) <= 4:
            raise ValueError("reads must contain two to four public searches or URLs")
        normalized = []
        for item in reads:
            if not isinstance(item, dict) or set(item) != {"kind", "value"}:
                raise ValueError("each read needs a kind and value")
            if item["kind"] == "url":
                normalized.append(("url", navigation_url(item["value"])))
            elif item["kind"] == "query" and isinstance(item["value"], str) and 1 <= len(item["value"].strip()) <= 512:
                normalized.append(("query", item["value"].strip()))
            else:
                raise ValueError("each read needs a valid URL or search query")
        settings = settings or SearchSettings()
        if settings.kind == "tavily" and settings.api_key is None and any(k == "query" for k, _ in normalized):
            raise search_provider_unconfigured()

        def read(item):
            kind, value = item
            try:
                page = self.lightpanda_fetch({"url": value}) if kind == "url" else self.lightpanda_search({"query": value}, settings)
            except BrowserActionFailure as exc:
                failure = {"kind": kind, "input": value, "status": "failed", "error": str(exc)[:240],
                           "failureKind": exc.code, "retryable": False}
                if exc.http_status is not None:
                    failure["httpStatus"] = exc.http_status
                if exc.retry_after_seconds is not None:
                    failure["retryAfterSeconds"] = exc.retry_after_seconds
                return failure
            text = page["text"]
            return {
                "kind": kind, "input": value, "status": "succeeded", "url": page["url"],
                "httpStatus": page["httpStatus"], "text": text[:MAX_LIGHTPANDA_BATCH_TEXT],
                "truncated": page["truncated"] or len(text) > MAX_LIGHTPANDA_BATCH_TEXT,
            }

        with ThreadPoolExecutor(max_workers=len(normalized)) as workers:
            results = list(workers.map(read, normalized))
        failed = [result for result in results if result["status"] == "failed"]
        if len(failed) == len(results):
            failures = [{"index": index, **{k: v for k, v in result.items()
                         if k in ("failureKind", "httpStatus", "retryAfterSeconds")}}
                        for index, result in enumerate(results)]
            if all(result["failureKind"] == "source_rate_limited" for result in failed):
                delays = [result["retryAfterSeconds"] for result in failed if "retryAfterSeconds" in result]
                failure = source_http_failure(429, max(delays) if len(delays) == len(failed) else None)
                failure.failures = failures
                raise failure
            codes = {result["failureKind"] for result in failed}
            if len(codes) == 1 and next(iter(codes)).startswith("search_provider_"):
                failure = BrowserActionFailure(next(iter(codes)), failed[0]["error"], failures=failures)
                raise failure
            raise BrowserActionFailure("source_reads_failed",
                                       "None of the requested pages could be read. Review the individual source failures and try later or use other permitted sources.",
                                       failures=failures)
        return {"status": "partial" if failed else "succeeded", "succeededCount": len(results) - len(failed),
                "failedCount": len(failed), "results": results}


def tavily_http_failure(status, retry_after_seconds):
    if status in (401, 403):
        return BrowserActionFailure("search_provider_authentication_failed",
                                    f"The Tavily search service rejected the configured API key (HTTP {status}). "
                                    "An operator must update the key in HelloSeal settings.", http_status=status)
    if status == 429:
        return source_http_failure(429, retry_after_seconds)
    if status in (432, 433):
        return BrowserActionFailure("search_provider_quota_exceeded",
                                    f"The Tavily search plan limit is exhausted (HTTP {status}). Try again later.",
                                    http_status=status)
    return BrowserActionFailure("source_http_error",
                                f"The Tavily search service returned HTTP {status}; no results could be read.",
                                http_status=status)


def _one_line(value, limit):
    return re.sub(r"\s+", " ", value).strip()[:limit] if isinstance(value, str) else ""


def tavily_markdown(results):
    entries = []
    for item in results:
        if not isinstance(item, dict):
            continue
        try:
            url = navigation_url(item.get("url"))
        except ValueError:
            continue
        title = _one_line(item.get("title"), 300) or url
        snippet = _one_line(item.get("content"), 1200)
        entries.append(f"### {title}\n{url}\n{snippet}".rstrip())
    if not entries:
        return "No search results were found for this query."
    return "\n\n".join(entries)
