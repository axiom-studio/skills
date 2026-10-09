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
from concurrent.futures import ThreadPoolExecutor
from datetime import datetime, timezone
from email.utils import parsedate_to_datetime
from urllib.parse import quote_plus, urlparse

VERSION = "3.0.1"
SKILL_ID = "skill-browser"
MAX_LIGHTPANDA_TEXT = 24 * 1024
MAX_LIGHTPANDA_BATCH_TEXT = 6 * 1024
SEARCH_URL = "https://search.brave.com/search?q="

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
    if status not in (403, 503):
        return False
    # A bare forbidden/access-denied page is not evidence of bot detection.
    # Error-status pages need an explicit challenge instruction or marker.
    return bool(ERROR_PAGE_CHALLENGE.search(content) or CAPTCHA.search(content))


class LightpandaRuntime:
    def __init__(self, binary=None, run=None):
        self.binary = binary or os.environ.get("LIGHTPANDA_BINARY", "/usr/local/bin/lightpanda")
        self._run = run

    def execute(self, action, config=None, bindings=None, context=None):
        config = config or {}
        handlers = {
            "lightpanda-fetch": lambda: self.lightpanda_fetch(config),
            "lightpanda-search": lambda: self.lightpanda_search(config),
            "lightpanda-read-many": lambda: self.lightpanda_read_many(config),
        }
        if action not in handlers:
            raise ValueError(f"unsupported browser action {action}")
        return handlers[action]()

    def ready(self):
        return os.access(self.binary, os.X_OK)

    def lightpanda_fetch(self, config):
        """Read a public page; private/internal networks are blocked after DNS."""
        url = navigation_url(config.get("url"))
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
        status = result.get("http_status")
        # Rate limiting remains its own failure even if its response body
        # mentions challenges. Never derive a browser switch from HTTP 429.
        if type(status) is int and status == 429:
            raise source_http_failure(status, source_retry_after(result))
        if type(status) is int and source_has_access_challenge(status, result.get("content")):
            raise BrowserActionFailure("source_access_challenge",
                                       f"This source returned an access challenge (HTTP {status}) instead of readable page content.",
                                       http_status=status)
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

    def lightpanda_search(self, config):
        query = config.get("query")
        if not isinstance(query, str) or not query.strip() or len(query) > 512:
            raise ValueError("search query must be between 1 and 512 characters")
        return self.lightpanda_fetch({"url": SEARCH_URL + quote_plus(query.strip())})

    def lightpanda_read_many(self, config):
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

        def read(item):
            kind, value = item
            try:
                page = self.lightpanda_fetch({"url": value}) if kind == "url" else self.lightpanda_search({"query": value})
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
            raise BrowserActionFailure("source_reads_failed",
                                       "None of the requested pages could be read. Review the individual source failures and try later or use other permitted sources.",
                                       failures=failures)
        return {"status": "partial" if failed else "succeeded", "succeededCount": len(results) - len(failed),
                "failedCount": len(failed), "results": results}
