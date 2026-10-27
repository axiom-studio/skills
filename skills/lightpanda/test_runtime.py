import json
import os
import subprocess
import threading
import unittest
from datetime import datetime, timezone
from unittest import mock

import yaml

import urllib.error

from runtime import (TAVILY_URL, VERSION, BrowserActionFailure, BrowserConfig, LightpandaRuntime,
                     navigation_url, source_retry_after)
from skill_config import Secret


class ManifestTest(unittest.TestCase):
    def test_manifest_is_lightpanda_only_and_keeps_the_browser_identity(self):
        with open(os.path.join(os.path.dirname(__file__), "skill.yaml"), "r", encoding="utf-8") as stream:
            definition = yaml.safe_load(stream)["definition"]
        self.assertEqual(definition["id"], "skill-browser")
        self.assertEqual(definition["version"], VERSION)
        self.assertEqual(definition["source"]["identity"], "https://github.com/axiom-studio/skills::skill-browser")
        self.assertEqual(definition["installers"][0]["package"], f"axiomstudio/skill-browser:{VERSION}")
        self.assertEqual(sorted(definition["actions"]), ["lightpanda-fetch", "lightpanda-read-many", "lightpanda-search"])
        self.assertEqual(sorted(definition["prompt"]["allowedTools"]), sorted(definition["actions"]))
        self.assertNotIn("storage", definition["requirements"])
        self.assertEqual(definition["requirements"]["tenancy"], "shared")
        for requirement in ("environment", "configuration", "storage"):
            self.assertNotIn(requirement, definition["requirements"])
        for name, action in definition["actions"].items():
            self.assertEqual((action["risk"], action["sideEffect"]), ("read", "read"))
            self.assertNotIn("sessionId", action["inputSchema"]["properties"])
            self.assertNotIn("browser:session", action["permissions"])
        serialized = json.dumps(definition).lower()
        self.assertNotIn("camoufox", serialized)
        with open(os.path.join(os.path.dirname(__file__), "pyproject.toml"), encoding="utf-8") as stream:
            self.assertIn(f'version = "{VERSION}"', stream.read())

    def test_unknown_and_removed_actions_are_rejected(self):
        for action in ("camoufox-start", "camoufox-health", "unknown"):
            with self.assertRaisesRegex(ValueError, "unsupported"):
                LightpandaRuntime().execute(action, {})
        self.assertEqual(navigation_url("https://example.com/a"), "https://example.com/a")


class LightpandaTest(unittest.TestCase):
    def test_lightpanda_reads_public_pages_without_a_browser_session(self):
        service = LightpandaRuntime()
        result = {"url": "https://example.com/", "http_status": 200, "content": "# Example Domain", "error": None}
        completed = subprocess.CompletedProcess([], 0, json.dumps(result), "")
        with mock.patch("runtime.subprocess.run", return_value=completed) as execute:
            page = service.execute("lightpanda-fetch", {"url": "https://example.com"})
        self.assertEqual(page, {"url": "https://example.com/", "httpStatus": 200, "text": "# Example Domain", "truncated": False})
        command = execute.call_args.args[0]
        self.assertIn("--block-private-networks", command)
        self.assertIn("--terminate-ms", command)
        with mock.patch("runtime.subprocess.run") as execute:
            with self.assertRaisesRegex(ValueError, "without embedded credentials"):
                service.execute("lightpanda-fetch", {"url": "https://user:secret@example.com"})
            execute.assert_not_called()

    def test_lightpanda_search_encodes_query(self):
        service = LightpandaRuntime()
        result = {"url": "https://search.yahoo.com/search?p=capital+of+Tanzania", "http_status": 200, "content": "Dodoma", "error": None}
        with mock.patch("runtime.subprocess.run", return_value=subprocess.CompletedProcess([], 0, json.dumps(result), "")) as execute:
            page = service.execute("lightpanda-search", {"query": "capital of Tanzania"})
        self.assertEqual(page["text"], "Dodoma")
        self.assertEqual(execute.call_args.args[0][2], result["url"])

    def test_lightpanda_does_not_report_blocked_or_empty_pages_as_reads(self):
        service = LightpandaRuntime()
        cases = [
            (403, "Access Denied", "HTTP 403"),
            (203, "HHS Vulnerability Disclosure", "HTTP 203"),
            (200, "  \n", "empty page"),
            (200, "Performing security verification", "access challenge"),
        ]
        for status, content, reason in cases:
            with self.subTest(status=status, content=content):
                result = {"url": "https://example.com/article", "http_status": status, "content": content, "error": None}
                completed = subprocess.CompletedProcess([], 0, json.dumps(result), "")
                with mock.patch("runtime.subprocess.run", return_value=completed):
                    with self.assertRaisesRegex(RuntimeError, reason):
                        service.execute("lightpanda-fetch", {"url": result["url"]})

    def test_rate_limit_is_typed_failure_without_page_content_or_browser_fallback(self):
        service = LightpandaRuntime()
        result = {"http_status": 429, "content": "Too many requests: secret page body",
                  "headers": {"Retry-After": "30"}, "error": "raw upstream diagnostic"}
        with mock.patch("runtime.subprocess.run", return_value=subprocess.CompletedProcess([], 1, json.dumps(result), "")) as execute:
            with self.assertRaises(BrowserActionFailure) as caught:
                service.execute("lightpanda-search", {"query": "current research"})
        self.assertEqual(execute.call_count, 1)
        self.assertEqual(caught.exception.code, "source_rate_limited")
        self.assertEqual(caught.exception.failure_details(), {
            "failureKind": "source_rate_limited", "httpStatus": "429",
            "retryAfterSeconds": "30", "retryable": "false",
        })
        self.assertIn("Try again after 30 seconds", str(caught.exception))
        self.assertNotIn("live browser", str(caught.exception))
        self.assertNotIn("secret", str(caught.exception))

    def test_retry_delay_is_optional_bounded_actual_header_metadata(self):
        now = datetime(2026, 10, 4, 20, 0, tzinfo=timezone.utc)
        self.assertEqual(source_retry_after({"headers": {"retry-after": "Sun, 04 Oct 2026 20:00:45 GMT"}}, now), 45)
        for value in [None, "", "-1", "86401", "999999999", "secret", "30\r\nAuthorization: secret", ["30"], True]:
            with self.subTest(value=value):
                self.assertIsNone(source_retry_after({"headers": {"Retry-After": value}}, now))
        self.assertIsNone(source_retry_after({"retry_after": 30, "content": "Retry-After: 30"}, now))
        self.assertIsNone(source_retry_after({"headers": {"retry-after": "30", "Retry-After": "40"}}, now))

    def test_non_rate_limit_failures_keep_distinct_classification(self):
        service = LightpandaRuntime()
        for payload, code in [
            ({"http_status": 403, "content": "Access Denied"}, "source_http_error"),
            ({"http_status": 403, "content": "Forbidden: authentication required"}, "source_http_error"),
            ({"http_status": 403, "content": "Checking your browser; private-page-value"}, "source_access_challenge"),
            ({"http_status": 503, "content": "Performing security verification"}, "source_access_challenge"),
            ({"http_status": 503, "content": "Service temporarily unavailable"}, "source_http_error"),
            ({"http_status": 407, "content": "Proxy Authentication Required"}, "source_http_error"),
            ({"http_status": 200, "content": "Performing security verification"}, "source_access_challenge"),
            ({"http_status": 200, "content": "  "}, "source_empty_response"),
            ({"http_status": True, "content": "page"}, "source_invalid_response"),
            ([], "source_invalid_response"),
        ]:
            with self.subTest(code=code, payload=payload):
                with mock.patch("runtime.subprocess.run", return_value=subprocess.CompletedProcess([], 0, json.dumps(payload), "")):
                    with self.assertRaises(BrowserActionFailure) as caught:
                        service.execute("lightpanda-fetch", {"url": "https://example.com/"})
                self.assertEqual(caught.exception.code, code)
                self.assertNotIn("content", caught.exception.failure_details())
                self.assertNotIn("private-page-value", str(caught.exception))
                self.assertNotIn("private-page-value", json.dumps(caught.exception.failure_details()))

    def test_error_status_challenge_detection_does_not_scan_article_sized_bodies(self):
        service = LightpandaRuntime()
        payload = {"http_status": 403, "content": "Checking your browser " + "a" * 1024}
        with mock.patch("runtime.subprocess.run", return_value=subprocess.CompletedProcess([], 0, json.dumps(payload), "")):
            with self.assertRaises(BrowserActionFailure) as caught:
                service.execute("lightpanda-fetch", {"url": "https://example.com/"})
        self.assertEqual(caught.exception.code, "source_http_error")

    def test_all_rate_limited_batch_is_an_action_failure(self):
        service = LightpandaRuntime()
        payload = {"http_status": 429, "content": "This is not evidence"}
        with mock.patch("runtime.subprocess.run", return_value=subprocess.CompletedProcess([], 0, json.dumps(payload), "")):
            with self.assertRaises(BrowserActionFailure) as caught:
                service.lightpanda_read_many({"reads": [{"kind": "query", "value": "first"}, {"kind": "query", "value": "second"}]})
        details = caught.exception.failure_details()
        self.assertEqual(caught.exception.code, "source_rate_limited")
        self.assertEqual((details["failedCount"], details["totalCount"]), ("2", "2"))
        self.assertEqual(json.loads(details["failures"]), [
            {"index": 0, "failureKind": "source_rate_limited", "httpStatus": 429},
            {"index": 1, "failureKind": "source_rate_limited", "httpStatus": 429},
        ])
        self.assertNotIn("retryAfterSeconds", details)
        self.assertNotIn("This is not evidence", json.dumps(details))

    def test_all_failed_batch_preserves_distinct_source_causes(self):
        service = LightpandaRuntime()
        def fetch(command, **_kwargs):
            payload = {"http_status": 429 if "limited" in command[2] else 403, "content": "not evidence"}
            return subprocess.CompletedProcess([], 0, json.dumps(payload), "")
        with mock.patch("runtime.subprocess.run", side_effect=fetch):
            with self.assertRaises(BrowserActionFailure) as caught:
                service.lightpanda_read_many({"reads": [
                    {"kind": "url", "value": "https://example.com/limited"},
                    {"kind": "url", "value": "https://example.com/denied"},
                ]})
        self.assertEqual(caught.exception.code, "source_reads_failed")
        failures = json.loads(caught.exception.failure_details()["failures"])
        self.assertEqual([f["failureKind"] for f in failures], ["source_rate_limited", "source_http_error"])
        self.assertNotIn("httpStatus", caught.exception.failure_details())

    def test_lightpanda_read_many_runs_concurrently_and_keeps_partial_results(self):
        service = LightpandaRuntime()
        started = threading.Barrier(2)

        def fetch(command, **_kwargs):
            started.wait(timeout=5)
            url = command[2]
            blocked = "blocked" in url
            result = {
                "url": url, "http_status": 403 if blocked else 200,
                "content": "Access Denied" if blocked else "# Useful page\n" + "x" * 7000,
                "error": None,
            }
            return subprocess.CompletedProcess(command, 0, json.dumps(result), "")

        with mock.patch("runtime.subprocess.run", side_effect=fetch) as execute:
            result = service.execute("lightpanda-read-many", {"reads": [
                {"kind": "url", "value": "https://example.com/useful"},
                {"kind": "url", "value": "https://example.com/blocked"},
            ]})
        self.assertEqual(execute.call_count, 2)
        self.assertEqual((result["status"], result["succeededCount"], result["failedCount"]), ("partial", 1, 1))
        good, blocked = result["results"]
        self.assertEqual((good["status"], good["httpStatus"], len(good["text"]), good["truncated"]),
                         ("succeeded", 200, 6144, True))
        self.assertEqual((blocked["status"], blocked["kind"]), ("failed", "url"))
        self.assertIn("HTTP 403", blocked["error"])
        self.assertEqual((blocked["failureKind"], blocked["httpStatus"], blocked["retryable"]), ("source_http_error", 403, False))
        self.assertNotIn("text", blocked)

    def test_lightpanda_read_many_validates_every_input_before_starting(self):
        service = LightpandaRuntime()
        with mock.patch("runtime.subprocess.run") as execute:
            with self.assertRaises(ValueError):
                service.execute("lightpanda-read-many", {"reads": [
                    {"kind": "url", "value": "https://example.com"},
                    {"kind": "url", "value": "https://user:secret@example.com"},
                ]})
            execute.assert_not_called()




class SharedRuntimeIsolationTest(unittest.TestCase):
    """One runtime serves every tenant: concurrent reads stay separate."""

    def test_concurrent_tenants_only_receive_their_own_pages(self):
        def fake_run(command, **_):
            url = command[2]
            marker = "marker-tenant-a" if "tenant-a" in url else "marker-tenant-b"
            body = json.dumps({"url": url, "http_status": 200, "content": f"page for {marker}"})
            return subprocess.CompletedProcess(command, 0, stdout=body, stderr="")

        runtime = LightpandaRuntime(binary="/bin/true", run=fake_run)
        failures = []

        def tenant(name, other):
            for index in range(32):
                result = runtime.execute("lightpanda-fetch", {"url": f"https://{name}.example.com/{index}"})
                text = json.dumps(result)
                if f"marker-{other}" in text or f"marker-{name}" not in text:
                    failures.append(text)

        threads = [threading.Thread(target=tenant, args=("tenant-a", "tenant-b")),
                   threading.Thread(target=tenant, args=("tenant-b", "tenant-a"))]
        for thread in threads:
            thread.start()
        for thread in threads:
            thread.join()
        self.assertEqual(failures, [])


def lightpanda_result(url, status=200, content="Yahoo results", headers=None):
    payload = {"url": url, "http_status": status, "content": content, "error": None}
    if headers:
        payload["headers"] = headers
    return subprocess.CompletedProcess([], 0, json.dumps(payload), "")


class FakeResponse:
    def __init__(self, body, status=200):
        self.status = status
        self._body = body if isinstance(body, bytes) else json.dumps(body).encode()

    def read(self, limit=-1):
        return self._body if limit < 0 else self._body[:limit]

    def __enter__(self):
        return self

    def __exit__(self, *_):
        return False


def http_error(status, headers=None):
    import email.message
    message = email.message.Message()
    for key, value in (headers or {}).items():
        message[key] = value
    return urllib.error.HTTPError(TAVILY_URL, status, "error", message, None)


KEY = "tvly-secret-key-123"
TAVILY = BrowserConfig(search_provider="tavily", tavily_api_key=Secret(KEY))
TAVILY_FALLBACK = BrowserConfig(search_provider="tavily", tavily_api_key=Secret(KEY), fallback_to_yahoo=True)
# Not decodable from the wire (requiredWhen), but the runtime still refuses it.
TAVILY_NO_KEY = BrowserConfig(search_provider="tavily")
TAVILY_BODY = {"results": [
    {"title": "Dodoma\n- Wikipedia", "url": "https://en.wikipedia.org/wiki/Dodoma", "content": "Dodoma is the\ncapital.", "score": 0.9},
    {"title": "Bad", "url": "javascript:alert(1)", "content": "skip"},
    {"title": "", "url": "https://example.com/tz", "content": "Tanzania facts"},
]}


class SearchProviderTest(unittest.TestCase):
    def runtime(self, responses=None, lightpanda=None):
        self.requests = []
        self.sleeps = []
        responses = list(responses or [])

        def urlopen(request, timeout):
            self.requests.append((request, timeout))
            item = responses.pop(0)
            if isinstance(item, BaseException):
                raise item
            return item

        self.fetches = []

        def run(command, **_):
            self.fetches.append(command[2])
            return (lightpanda or (lambda url: lightpanda_result(url)))(command[2])

        return LightpandaRuntime(binary="/bin/true", run=run, urlopen=urlopen, sleep=self.sleeps.append)

    def test_absent_provider_searches_yahoo_without_calling_tavily(self):
        service = self.runtime()
        page = service.execute("lightpanda-search", {"query": "capital of Tanzania"})
        self.assertEqual(self.fetches, ["https://search.yahoo.com/search?p=capital+of+Tanzania"])
        self.assertEqual(self.requests, [])
        self.assertEqual(page["text"], "Yahoo results")

    def test_explicit_yahoo_provider_dispatches_to_yahoo(self):
        service = self.runtime()
        service.execute("lightpanda-search", {"query": "q"},
                        BrowserConfig(search_provider="yahoo", tavily_api_key=Secret(KEY), fallback_to_yahoo=True))
        self.assertEqual(len(self.fetches), 1)
        self.assertEqual(self.requests, [])

    def test_search_settings_are_never_read_from_action_arguments(self):
        service = self.runtime()
        seen = {}
        service.lightpanda_fetch = lambda config: seen.update(config) or {"url": "https://a.example/", "httpStatus": 200, "text": "x", "truncated": False}
        service.execute("lightpanda-search", {"query": "q", "searchProvider": {"kind": "tavily", "fallback": None}})
        self.assertEqual(seen, {"url": "https://search.yahoo.com/search?p=q"})
        self.assertEqual(self.requests, [])

    def test_tavily_success_is_formatted_as_bounded_markdown(self):
        service = self.runtime([FakeResponse(TAVILY_BODY)])
        page = service.execute("lightpanda-search", {"query": " capital of Tanzania "}, TAVILY)
        request, timeout = self.requests[0]
        self.assertEqual((request.full_url, request.get_method(), timeout), (TAVILY_URL, "POST", 10))
        self.assertEqual(request.get_header("Authorization"), f"Bearer {KEY}")
        self.assertEqual(json.loads(request.data), {"query": "capital of Tanzania", "max_results": 8, "search_depth": "basic"})
        self.assertEqual(page, {"url": TAVILY_URL, "httpStatus": 200, "truncated": False, "text":
            "### Dodoma - Wikipedia\nhttps://en.wikipedia.org/wiki/Dodoma\nDodoma is the capital.\n\n"
            "### https://example.com/tz\nhttps://example.com/tz\nTanzania facts"})
        self.assertEqual(self.fetches, [])

    def test_tavily_text_is_capped(self):
        body = {"results": [{"title": f"t{i}", "url": f"https://example.com/{i}", "content": "x" * 5000} for i in range(30)]}
        page = self.runtime([FakeResponse(body)]).execute("lightpanda-search", {"query": "q"}, TAVILY)
        self.assertEqual((len(page["text"]), page["truncated"]), (24 * 1024, True))

    def test_tavily_without_usable_key_is_unconfigured(self):
        for key in [None, "", "bad key\r\nX: y", "x" * 513]:
            with self.subTest(key=key):
                service = self.runtime()
                cfg = BrowserConfig(search_provider="tavily", tavily_api_key=Secret(key), fallback_to_yahoo=True)
                with self.assertRaises(BrowserActionFailure) as caught:
                    service.execute("lightpanda-search", {"query": "q"}, cfg)
                self.assertEqual(caught.exception.code, "search_provider_unconfigured")
                self.assertEqual((self.fetches, self.requests), ([], []))

    def test_tavily_error_mapping_without_fallback(self):
        for status, code in [(401, "search_provider_authentication_failed"), (403, "search_provider_authentication_failed"),
                             (429, "source_rate_limited"), (432, "search_provider_quota_exceeded"),
                             (433, "search_provider_quota_exceeded"), (500, "source_http_error"),
                             (503, "source_http_error"), (400, "source_http_error")]:
            with self.subTest(status=status):
                service = self.runtime([http_error(status)])
                with self.assertRaises(BrowserActionFailure) as caught:
                    service.execute("lightpanda-search", {"query": "q"}, TAVILY)
                self.assertEqual((caught.exception.code, caught.exception.http_status), (code, status))
                self.assertEqual(self.fetches, [])
                self.assertNotIn(KEY, str(caught.exception) + json.dumps(caught.exception.failure_details()))

    def test_tavily_unreachable_and_invalid_responses(self):
        for item, code in [(urllib.error.URLError(f"boom {KEY}"), "source_unavailable"),
                           (TimeoutError(), "source_unavailable"),
                           (FakeResponse(b"not json"), "source_invalid_response"),
                           (FakeResponse({"answer": "x"}), "source_invalid_response")]:
            with self.subTest(code=code, item=item):
                with self.assertRaises(BrowserActionFailure) as caught:
                    self.runtime([item]).execute("lightpanda-search", {"query": "q"}, TAVILY)
                self.assertEqual(caught.exception.code, code)
                self.assertNotIn(KEY, str(caught.exception))
                self.assertIsNone(caught.exception.__cause__)

    def test_tavily_provider_failures_fall_back_to_yahoo_when_allowed(self):
        for item in [http_error(400), http_error(429, {"Retry-After": "3"}), http_error(432),
                     http_error(433), http_error(500), http_error(502), urllib.error.URLError("down"),
                     FakeResponse(b"not json")]:
            with self.subTest(item=item):
                service = self.runtime([item])
                page = service.execute("lightpanda-search", {"query": "capital"}, TAVILY_FALLBACK)
                self.assertEqual(self.fetches, ["https://search.yahoo.com/search?p=capital"])
                self.assertEqual(page["text"], "Yahoo results")
                self.assertEqual(self.sleeps, [])

    def test_rejected_tavily_key_is_not_hidden_by_fallback(self):
        for status in (401, 403):
            with self.subTest(status=status):
                service = self.runtime([http_error(status)])
                with self.assertRaises(BrowserActionFailure) as caught:
                    service.execute("lightpanda-search", {"query": "q"}, TAVILY_FALLBACK)
                self.assertEqual(caught.exception.code, "search_provider_authentication_failed")
                self.assertEqual(self.fetches, [])

    def test_tavily_short_retry_after_is_waited_once(self):
        service = self.runtime([http_error(429, {"Retry-After": "3"}), FakeResponse(TAVILY_BODY)])
        page = service.execute("lightpanda-search", {"query": "q"}, TAVILY)
        self.assertEqual((self.sleeps, len(self.requests), page["url"]), ([3], 2, TAVILY_URL))
        service = self.runtime([http_error(429, {"Retry-After": "3"}), http_error(429, {"Retry-After": "3"})])
        with self.assertRaises(BrowserActionFailure) as caught:
            service.execute("lightpanda-search", {"query": "q"}, TAVILY)
        self.assertEqual((caught.exception.code, self.sleeps, len(self.requests)), ("source_rate_limited", [3], 2))

    def test_key_is_redacted_by_the_transport_and_never_in_the_settings_repr(self):
        from runtime import search_settings
        self.assertNotIn(KEY, repr(search_settings(TAVILY)) + repr(TAVILY) + str(TAVILY))
        import test_server
        server = test_server.load_server()
        service = server.SkillService.__new__(server.SkillService)
        service.config = server.config_spec()
        service.runtime = mock.Mock()
        service.runtime.execute.side_effect = RuntimeError(f"upstream echoed {KEY}")
        context = mock.Mock(tenant_id="t1", config_version="", config={"searchProvider": "tavily", "tavilyApiKey": KEY})
        request = mock.Mock(node_type="lightpanda-search", config={"query": b'"q"'}, bindings={}, context=context)
        response = service.Execute(request, mock.Mock())
        self.assertEqual(response.error.message, "upstream echoed [REDACTED]")
        self.assertEqual(service.runtime.execute.call_args.args[2].tavily_api_key.reveal(), KEY)

    def test_read_many_queries_use_the_provider(self):
        service = self.runtime([FakeResponse(TAVILY_BODY), FakeResponse(TAVILY_BODY)])
        result = service.execute("lightpanda-read-many", {"reads": [
            {"kind": "query", "value": "one"}, {"kind": "query", "value": "two"},
            {"kind": "url", "value": "https://example.com/page"}]}, TAVILY)
        self.assertEqual(result["status"], "succeeded")
        self.assertEqual([r["url"] for r in result["results"]], [TAVILY_URL, TAVILY_URL, "https://example.com/page"])
        self.assertEqual(sorted(json.loads(r.data)["query"] for r, _ in self.requests), ["one", "two"])
        self.assertEqual(self.fetches, ["https://example.com/page"])

    def test_read_many_with_unconfigured_tavily_fails_before_starting(self):
        service = self.runtime()
        with self.assertRaises(BrowserActionFailure) as caught:
            service.execute("lightpanda-read-many", {"reads": [
                {"kind": "query", "value": "one"}, {"kind": "url", "value": "https://example.com/"}]}, TAVILY_NO_KEY)
        self.assertEqual(caught.exception.code, "search_provider_unconfigured")
        self.assertEqual(self.fetches, [])
        # Only URL reads need no provider key.
        self.runtime().execute("lightpanda-read-many", {"reads": [
            {"kind": "url", "value": "https://example.com/a"}, {"kind": "url", "value": "https://example.com/b"}]}, TAVILY_NO_KEY)

    def test_read_many_rejected_key_everywhere_is_one_provider_failure(self):
        service = self.runtime([http_error(401), http_error(401)])
        with self.assertRaises(BrowserActionFailure) as caught:
            service.execute("lightpanda-read-many", {"reads": [
                {"kind": "query", "value": "one"}, {"kind": "query", "value": "two"}]}, TAVILY)
        self.assertEqual(caught.exception.code, "search_provider_authentication_failed")
        self.assertEqual(len(json.loads(caught.exception.failure_details()["failures"])), 2)

    def test_read_many_falls_back_per_query(self):
        service = self.runtime([http_error(433), http_error(433)])
        result = service.execute("lightpanda-read-many", {"reads": [
            {"kind": "query", "value": "one"}, {"kind": "query", "value": "two"}]}, TAVILY_FALLBACK)
        self.assertEqual(result["succeededCount"], 2)
        self.assertEqual(sorted(self.fetches), ["https://search.yahoo.com/search?p=one", "https://search.yahoo.com/search?p=two"])


class ChallengeAndRetryTest(unittest.TestCase):
    def runtime(self, results):
        self.sleeps = []
        self.calls = 0
        results = list(results)

        def run(command, **_):
            self.calls += 1
            return results.pop(0)
        return LightpandaRuntime(binary="/bin/true", run=run, sleep=self.sleeps.append)

    def test_challenge_body_wins_over_rate_limit_status(self):
        for content in ["Performing security verification", "Please complete the CAPTCHA", "Checking your browser"]:
            for status in (429, 403, 503):
                with self.subTest(status=status, content=content):
                    service = self.runtime([lightpanda_result("https://example.com/", status, content, {"Retry-After": "2"})])
                    with self.assertRaises(BrowserActionFailure) as caught:
                        service.execute("lightpanda-fetch", {"url": "https://example.com/"})
                    self.assertEqual((caught.exception.code, caught.exception.http_status), ("source_access_challenge", status))
                    self.assertEqual((self.calls, self.sleeps), (1, []))

    def test_plain_429_stays_rate_limited(self):
        service = self.runtime([lightpanda_result("https://example.com/", 429, "Too Many Requests")])
        with self.assertRaises(BrowserActionFailure) as caught:
            service.execute("lightpanda-fetch", {"url": "https://example.com/"})
        self.assertEqual(caught.exception.code, "source_rate_limited")
        self.assertEqual((self.calls, self.sleeps), (1, []))

    def test_short_retry_after_is_waited_and_retried_once(self):
        service = self.runtime([lightpanda_result("https://example.com/", 429, "slow down", {"Retry-After": "4"}),
                                lightpanda_result("https://example.com/", 200, "# Page")])
        page = service.execute("lightpanda-fetch", {"url": "https://example.com/"})
        self.assertEqual((page["text"], self.calls, self.sleeps), ("# Page", 2, [4]))
        service = self.runtime([lightpanda_result("https://example.com/", 429, "slow down", {"Retry-After": "10"}),
                                lightpanda_result("https://example.com/", 429, "slow down", {"Retry-After": "10"})])
        with self.assertRaises(BrowserActionFailure) as caught:
            service.execute("lightpanda-fetch", {"url": "https://example.com/"})
        self.assertEqual((caught.exception.code, caught.exception.retry_after_seconds), ("source_rate_limited", 10))
        self.assertEqual((self.calls, self.sleeps), (2, [10]))

    def test_long_or_missing_retry_after_is_not_waited(self):
        for headers in [{"Retry-After": "11"}, None]:
            with self.subTest(headers=headers):
                service = self.runtime([lightpanda_result("https://example.com/", 429, "slow down", headers)])
                with self.assertRaises(BrowserActionFailure):
                    service.execute("lightpanda-fetch", {"url": "https://example.com/"})
                self.assertEqual((self.calls, self.sleeps), (1, []))


if __name__ == "__main__":
    unittest.main()
