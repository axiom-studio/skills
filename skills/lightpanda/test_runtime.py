import json
import os
import subprocess
import threading
import unittest
from datetime import datetime, timezone
from unittest import mock

import yaml

from runtime import VERSION, BrowserActionFailure, LightpandaRuntime, navigation_url, source_retry_after


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
            page = service.execute("lightpanda-fetch", {"url": "https://example.com"}, context={"runId": "run-1", "agentId": "agent-1"})
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
        result = {"url": "https://search.brave.com/search?q=capital+of+Tanzania", "http_status": 200, "content": "Dodoma", "error": None}
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
        result = {"http_status": 429, "content": "Performing security verification: secret page body",
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
            ]}, context={"runId": "run-1", "agentId": "agent-1"})
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


if __name__ == "__main__":
    unittest.main()
