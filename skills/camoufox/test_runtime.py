import base64
import json
import os
import sqlite3
import tempfile
import threading
import unittest
from datetime import datetime, timezone

from runtime import (
    SNAPSHOT_JS,
    BrowserOperationTimeout,
    BrowserWorker,
    CamoufoxHandle,
    CamoufoxRuntime,
    exact_path,
    load_inventory,
    navigation_url,
    resolve_proxy,
    snapshot_element_state,
)


def inventory():
    return {
        "targets": {
            "owned": {"baseUrl": "http://fixture.internal:8080", "pathPrefixes": ["/assessment"], "mode": "owned-assessment"},
            "forum": {"baseUrl": "https://forum.example", "pathPrefixes": ["/community"], "mode": "permitted-automation"},
            "forum-old": {"baseUrl": "https://old.forum.example", "pathPrefixes": ["/community"], "mode": "permitted-automation"},
        },
        "profiles": {
            "standard": {"os": ["windows", "macos"], "humanize": True, "geoip": True},
            "seeded": {"os": "linux", "seed": 42, "assessmentOnly": True},
        },
        "proxy_pools": {
            "direct": {},
            "rotating": {"url": "http://proxy.internal:8080", "assessmentOnly": True},
            "pool": {"urls": ["http://proxy-a.internal:8080", "socks5://proxy-b.internal:1080", "http://proxy-c.internal:8080"]},
        },
        "defaults": {},
    }


class FakeHandle:
    def __init__(self, state, options):
        state["launch"] = options
        state.setdefault("launches", []).append(options)
        self.state = state

    def goto(self, url, timeout_ms=30000):
        self.state["url"] = url
        return {"url": url, "status": self.state.get("status", 200)}

    def snapshot(self, include_model_media=False):
        if self.state.get("solved"):
            return {"url": self.state["url"], "title": "Accepted", "text": "Assessment accepted", "elements": []}
        result = {
            "url": self.state["url"],
            "title": "Page",
            "text": self.state.get("text", "Welcome"),
            "viewport": {"width": 1280, "height": 720},
            "elements": self.state.get("elements") or [
                {"ref": 1, "role": self.state.get("element_role", "textbox"), "name": self.state.get("element_name", "Comment")}
            ],
        }
        if include_model_media and self.state.get("model_media"):
            result["model_media"] = self.state["model_media"]
        return result

    def click(self, marker):
        self.state["click"] = marker
        self.state["solved"] = True

    def click_point(self, x, y):
        self.state["click_point"] = (x, y)
        self.state["solved"] = True

    def fill(self, marker, value):
        self.state["fill"] = {"marker": marker, "value": value}
        if self.state.get("reflect_fill"):
            self.state["text"] = f"Echo {value}"

    def select(self, marker, value):
        self.state["select"] = {"marker": marker, "value": value}
        return [value]

    def scroll(self, dx, dy):
        if self.state.get("timeout_scroll"):
            raise BrowserOperationTimeout("browser scroll exceeded its action timeout; start a new session")
        self.state["scroll"] = (dx, dy)

    def screenshot(self, full_page=False):
        self.state["screenshot_full_page"] = full_page
        return base64.b64decode("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==")

    def close(self):
        self.state["closed"] = True


def fake_factory(state):
    return lambda options: FakeHandle(state, options)


_UNSET = object()


def make_runtime(state=None, inv=_UNSET):
    state = state if state is not None else {}
    workspace = tempfile.mkdtemp(prefix="camoufox-")
    state["workspace"] = workspace
    return CamoufoxRuntime(
        inventory=inventory() if inv is _UNSET else inv,
        workspace=workspace,
        browser_factory=fake_factory(state),
        now=lambda: datetime(2026, 7, 27, tzinfo=timezone.utc),
    ), state


def start_session(service, run_id, *, target, profile, proxy_pool, path="/"):
    service.inventory["defaults"] = {
        "targetId": target,
        "profileId": profile,
        "proxyPoolId": proxy_pool,
    }
    return service.execute(
        "camoufox-start",
        {"path": path},
        context={"runId": run_id},
    )


class InventoryTest(unittest.TestCase):
    def base_env(self):
        return {
            "CAMOUFOX_TARGETS": '{"fixture": {"baseUrl": "http://fixture.internal", "pathPrefixes": ["/"], "mode": "owned-assessment"}}',
            "CAMOUFOX_PROFILES": '{"standard": {"os": "windows"}}',
            "CAMOUFOX_PROXY_POOLS": '{"direct": {}}',
        }

    def test_valid_inventory_loads(self):
        self.assertEqual(len(load_inventory(self.base_env())["targets"]), 1)
        self.assertIsNone(load_inventory({}))

    def test_governed_defaults_select_only_authorized_inventory(self):
        base = self.base_env()
        configured = load_inventory({
            **base,
            "CAMOUFOX_DEFAULTS": '{"targetId":"fixture","profileId":"standard","proxyPoolId":"direct"}',
        })
        self.assertEqual(configured["defaults"], {
            "targetId": "fixture",
            "profileId": "standard",
            "proxyPoolId": "direct",
        })
        for defaults in [
            '{"targetId":"missing"}',
            '{"profileId":"missing"}',
            '{"proxyPoolId":"missing"}',
            '{"javascript":"alert(1)"}',
        ]:
            with self.assertRaisesRegex(ValueError, "CAMOUFOX_DEFAULTS"):
                load_inventory({**base, "CAMOUFOX_DEFAULTS": defaults})

    def test_validation_fails_closed(self):
        base = self.base_env()
        bad_envs = [
            {"CAMOUFOX_TARGETS": base["CAMOUFOX_TARGETS"]},
            {**base, "CAMOUFOX_TARGETS": '{"bad": {"baseUrl": "file:///etc/passwd", "pathPrefixes": ["/"], "mode": "owned-assessment"}}'},
            {**base, "CAMOUFOX_TARGETS": '{"bad": {"baseUrl": "https://example.com", "pathPrefixes": ["/"], "mode": "unknown"}}'},
            {**base, "CAMOUFOX_PROFILES": '{"bad": {"os": "solaris"}}'},
            {**base, "CAMOUFOX_PROFILES": '{"bad": {"seed": -1}}'},
            {**base, "CAMOUFOX_PROFILES": '{"bad": {"window": [0, 1080]}}'},
            {**base, "CAMOUFOX_PROFILES": '{"bad": {"javascript": "alert(1)"}}'},
            {**base, "CAMOUFOX_PROXY_POOLS": '{"bad": {"url": "http://a:1", "urls": ["http://b:2"]}}'},
            {**base, "CAMOUFOX_PROXY_POOLS": '{"bad": {"urls": []}}'},
            {**base, "CAMOUFOX_PROXY_POOLS": '{"bad": {"urls": ["file:///etc/passwd"]}}'},
        ]
        for env in bad_envs:
            with self.assertRaises(ValueError, msg=env):
                load_inventory(env)
        good = {**base, "CAMOUFOX_PROXY_POOLS": '{"pool": {"urls": ["http://a:1", "socks5://b:1080"]}}'}
        self.assertEqual(len(load_inventory(good)["proxy_pools"]), 1)


class BrowserWorkerTest(unittest.TestCase):
    def test_timeout_poisoned_worker_fails_future_calls_without_waiting(self):
        worker = BrowserWorker()
        release = threading.Event()
        try:
            with self.assertRaisesRegex(BrowserOperationTimeout, "test operation exceeded"):
                worker.call(lambda: release.wait(10), timeout=0.01, operation="test operation")
            with self.assertRaisesRegex(BrowserOperationTimeout, "worker is unavailable"):
                worker.call(lambda: None, timeout=1, operation="later operation")
        finally:
            release.set()
            worker.stop()


class ProxyRotationTest(unittest.TestCase):
    def test_single_and_direct_pools(self):
        self.assertEqual(resolve_proxy({"url": "http://one:1"}, "single", "s1"), "http://one:1")
        self.assertIsNone(resolve_proxy({}, "direct", "s1"))

    def test_deterministic_rotation_across_sessions(self):
        pool = {"urls": ["http://a:1", "http://b:2", "http://c:3"]}
        first = resolve_proxy(pool, "pool", "session-a")
        self.assertIn(first, pool["urls"])
        self.assertEqual(resolve_proxy(pool, "pool", "session-a"), first)
        chosen = {resolve_proxy(pool, "pool", f"s-{i}") for i in range(12)}
        self.assertGreater(len(chosen), 1)
        self.assertTrue(chosen <= set(pool["urls"]))


class BrowserNavigationTest(unittest.TestCase):
    def test_snapshot_clears_previous_dom_markers_before_assigning_current_refs(self):
        clear = "e.removeAttribute?.('data-camoufox-ref')"
        assign = "e.setAttribute('data-camoufox-ref', String(i + 1))"
        self.assertIn(clear, SNAPSHOT_JS)
        self.assertLess(SNAPSHOT_JS.index(clear), SNAPSHOT_JS.index(assign))

    def test_goto_uses_dom_readiness_instead_of_network_quiescence(self):
        calls = []

        class Page:
            url = "https://forum.example/community"

            def goto(self, url, timeout, wait_until):
                calls.append({"url": url, "timeout": timeout, "wait_until": wait_until})
                return type("Response", (), {"status": 200})()

        class Worker:
            @staticmethod
            def call(fn, **_kwargs):
                return fn()

        handle = CamoufoxHandle.__new__(CamoufoxHandle)
        handle._worker = Worker()
        handle._page = Page()

        self.assertEqual(handle.goto("https://forum.example/community", 1234)["status"], 200)
        self.assertEqual(calls, [{
            "url": "https://forum.example/community",
            "timeout": 1234,
            "wait_until": "domcontentloaded",
        }])

    def test_exact_script_backed_control_uses_bounded_dom_fallback(self):
        calls = []

        class Locator:
            first = None

            def __init__(self):
                self.first = self

            def scroll_into_view_if_needed(self, timeout):
                raise TimeoutError("not stable")

            def click(self, timeout):
                calls.append(("trusted", timeout))
                raise TimeoutError("not stable")

            def evaluate(self, expression, timeout):
                calls.append(("exact-dom", expression, timeout))

        class Page:
            @staticmethod
            def locator(selector):
                calls.append(("locator", selector))
                return Locator()

        class Worker:
            @staticmethod
            def call(fn, **_kwargs):
                return fn()

        handle = CamoufoxHandle.__new__(CamoufoxHandle)
        handle._worker = Worker()
        handle._page = Page()
        handle.click(74)

        self.assertEqual(calls[0], ("locator", '[data-camoufox-ref="74"]'))
        self.assertEqual(calls[1], ("trusted", 5000))
        self.assertEqual(calls[2], ("exact-dom", "element => element.click()", 5000))

    def test_fill_replaces_exact_control_without_pointer_stability_gate(self):
        calls = []

        class Locator:
            first = None

            def __init__(self):
                self.first = self

            def fill(self, value, timeout):
                calls.append(("fill", value, timeout))

        class Page:
            @staticmethod
            def locator(selector):
                calls.append(("locator", selector))
                return Locator()

        class Worker:
            @staticmethod
            def call(fn, **_kwargs):
                return fn()

        handle = CamoufoxHandle.__new__(CamoufoxHandle)
        handle._worker = Worker()
        handle._page = Page()
        handle.fill(70, "replacement")

        self.assertEqual(calls, [
            ("locator", '[data-camoufox-ref="70"]'),
            ("fill", "replacement", 5000),
        ])


class RuntimeTest(unittest.TestCase):
    def test_health_reports_configuration_state(self):
        service, _ = make_runtime(inv=None)
        self.assertEqual(service.execute("camoufox-health")["status"], "needs_configuration")
        with self.assertRaisesRegex(ValueError, "not configured"):
            service.execute("camoufox-start", {}, context={"runId": "unconfigured"})
        ready, _ = make_runtime()
        health = ready.execute("camoufox-health")
        self.assertEqual(health["status"], "ready")
        self.assertEqual(health["authorizedTargets"], 3)

    def test_target_scope_cannot_escape_origin_or_path(self):
        target = inventory()["targets"]["owned"]
        self.assertEqual(exact_path(target, "/assessment/start"), "http://fixture.internal:8080/assessment/start")
        for value in ["https://other.example/assessment", "//other.example/assessment", "/admin", "relative"]:
            with self.assertRaises(ValueError, msg=value):
                exact_path(target, value)

    def test_start_applies_profile_options_and_rotated_proxy(self):
        service, state = make_runtime()
        start_session(service, "pool-1", target="forum", path="/community", profile="standard", proxy_pool="pool")
        launch = state["launch"]
        self.assertEqual(launch["proxy"], resolve_proxy(inventory()["proxy_pools"]["pool"], "pool", "pool-1"))
        self.assertEqual(launch["profile_options"], {"os": ["windows", "macos"], "humanize": True, "geoip": True})
        self.assertEqual(launch["headless"], "virtual")

    def test_start_resolves_single_target_and_named_defaults(self):
        configured = inventory()
        configured["targets"] = {"reddit": configured["targets"]["forum"]}
        configured["profiles"]["default"] = configured["profiles"]["standard"]
        configured["proxy_pools"]["default"] = configured["proxy_pools"]["direct"]
        service, _ = make_runtime(inv=configured)
        result = service.execute("camoufox-start", {"path": "/community"}, context={"runId": "run-123"})
        self.assertEqual(result["sessionId"], "run-123")
        self.assertNotIn("targetId", result)
        self.assertNotIn("profileId", result)
        self.assertNotIn("proxyPoolId", result)

        repeated = service.execute("camoufox-start", {"path": "/community"}, context={"runId": "run-123"})
        self.assertEqual(repeated, result)

    def test_start_derives_isolated_handle_from_durable_run_context(self):
        configured = inventory()
        configured["defaults"] = {
            "targetId": "forum",
            "profileId": "standard",
            "proxyPoolId": "direct",
        }
        service, _ = make_runtime(inv=configured)
        long_run_id = "tenant/workforce/" + "r" * 160
        first = service.execute("camoufox-start", {"path": "/community"}, context={"runId": long_run_id})
        self.assertRegex(first["sessionId"], r"^run-[0-9a-f]{32}$")
        self.assertEqual(
            service.execute("camoufox-start", {"path": "/community"}, context={"runId": long_run_id}),
            first,
        )
        self.assertEqual(
            service.execute("camoufox-snapshot", {"sessionId": first["sessionId"]})["sessionId"],
            first["sessionId"],
        )
        service.execute("camoufox-close", {"sessionId": first["sessionId"]})
        second = service.execute("camoufox-start", {"path": "/community"}, context={"runId": "another-run"})
        self.assertNotEqual(second["sessionId"], first["sessionId"])
        for field in ["sessionId", "targetId", "profileId", "proxyPoolId"]:
            with self.assertRaisesRegex(ValueError, "host-owned"):
                service.execute("camoufox-start", {field: "caller-choice"}, context={"runId": "third-run"})

    def test_start_requires_durable_run_context(self):
        configured = inventory()
        configured["defaults"] = {
            "targetId": "forum",
            "profileId": "standard",
            "proxyPoolId": "direct",
        }
        service, _ = make_runtime(inv=configured)
        with self.assertRaisesRegex(ValueError, "durable Run context"):
            service.execute("camoufox-start", {"path": "/community"})

    def test_profile_lease_and_every_hosted_action_are_owned_by_the_durable_run(self):
        service, state = make_runtime()
        first = start_session(
            service,
            "run-a",
            target="owned",
            path="/assessment",
            profile="seeded",
            proxy_pool="rotating",
        )
        with self.assertRaisesRegex(ValueError, "different durable Run"):
            service.execute(
                "camoufox-close",
                {"sessionId": first["sessionId"]},
                context={"runId": "run-b"},
            )
        self.assertEqual(
            service.execute(
                "camoufox-snapshot",
                {"sessionId": first["sessionId"]},
                context={"runId": "run-a"},
            )["sessionId"],
            first["sessionId"],
        )
        with self.assertRaisesRegex(ValueError, "leased by another active Run"):
            start_session(
                service,
                "run-b",
                target="owned",
                path="/assessment",
                profile="seeded",
                proxy_pool="rotating",
            )

        lease_path = os.path.join(state["workspace"], "profiles", "seeded", "lease.json")
        with open(lease_path, "r", encoding="utf-8") as handle:
            active_lease = json.load(handle)
        self.assertEqual(active_lease["state"], "active")
        self.assertEqual(active_lease["sessionId"], "run-a")
        self.assertRegex(active_lease["runDigest"], r"^sha256:[0-9a-f]{64}$")

        first_profile_directory = state["launches"][0]["user_data_dir"]
        service.execute(
            "camoufox-close",
            {"sessionId": first["sessionId"]},
            context={"runId": "run-a"},
        )
        second = start_session(
            service,
            "run-b",
            target="owned",
            path="/assessment",
            profile="seeded",
            proxy_pool="rotating",
        )
        self.assertNotEqual(first["sessionId"], second["sessionId"])
        self.assertEqual(state["launches"][1]["user_data_dir"], first_profile_directory)
        with open(lease_path, "r", encoding="utf-8") as handle:
            replacement_lease = json.load(handle)
        self.assertEqual(replacement_lease["generation"], active_lease["generation"] + 1)
        service.execute("camoufox-close", {"sessionId": "run-b"}, context={"runId": "run-b"})

    def test_first_profile_lease_forward_ports_the_richest_legacy_session_directory(self):
        service, state = make_runtime()
        profile_directory = os.path.join(state["workspace"], "profiles", "seeded")

        def legacy_profile(name, cookie_count, modified):
            directory = os.path.join(profile_directory, name)
            os.makedirs(directory, mode=0o700)
            database = sqlite3.connect(os.path.join(directory, "cookies.sqlite"))
            database.execute("CREATE TABLE moz_cookies (id INTEGER PRIMARY KEY)")
            database.executemany("INSERT INTO moz_cookies DEFAULT VALUES", [()] * cookie_count)
            database.commit()
            database.close()
            os.utime(directory, (modified, modified))

        legacy_profile("authenticated-session", 3, 1)
        legacy_profile("newer-empty-session", 1, 2)

        start_session(
            service,
            "replacement-run",
            target="owned",
            path="/assessment",
            profile="seeded",
            proxy_pool="rotating",
        )
        data_directory = os.path.join(state["workspace"], "profiles", "seeded", "data")
        self.assertEqual(state["launch"]["user_data_dir"], data_directory)
        self.assertTrue(os.path.isfile(os.path.join(data_directory, "cookies.sqlite")))
        database = sqlite3.connect(os.path.join(data_directory, "cookies.sqlite"))
        self.assertEqual(database.execute("SELECT COUNT(*) FROM moz_cookies").fetchone()[0], 3)
        database.close()
        service.execute(
            "camoufox-close",
            {"sessionId": "replacement-run"},
            context={"runId": "replacement-run"},
        )

    def test_failed_browser_launch_releases_the_durable_profile_lease(self):
        configured = inventory()
        configured["defaults"] = {
            "targetId": "owned",
            "profileId": "seeded",
            "proxyPoolId": "rotating",
        }
        workspace = tempfile.mkdtemp(prefix="camoufox-launch-failure-")
        failing = CamoufoxRuntime(
            inventory=configured,
            workspace=workspace,
            browser_factory=lambda _options: (_ for _ in ()).throw(RuntimeError("launch failed")),
            now=lambda: datetime(2026, 7, 27, tzinfo=timezone.utc),
        )
        with self.assertRaisesRegex(RuntimeError, "launch failed"):
            failing.execute("camoufox-start", {"path": "/assessment"}, context={"runId": "failed-run"})

        state = {}
        replacement = CamoufoxRuntime(
            inventory=configured,
            workspace=workspace,
            browser_factory=fake_factory(state),
            now=lambda: datetime(2026, 7, 27, tzinfo=timezone.utc),
        )
        started = replacement.execute(
            "camoufox-start",
            {"path": "/assessment"},
            context={"runId": "replacement-run"},
        )
        self.assertEqual(started["sessionId"], "replacement-run")
        with open(os.path.join(workspace, "profiles", "seeded", "lease.json"), "r", encoding="utf-8") as handle:
            lease = json.load(handle)
        self.assertEqual(lease["state"], "active")
        self.assertEqual(lease["generation"], 2)
        replacement.execute(
            "camoufox-close",
            {"sessionId": "replacement-run"},
            context={"runId": "replacement-run"},
        )

    def test_profile_lease_is_exclusive_across_runtime_instances(self):
        configured = inventory()
        configured["defaults"] = {
            "targetId": "owned",
            "profileId": "seeded",
            "proxyPoolId": "rotating",
        }
        workspace = tempfile.mkdtemp(prefix="camoufox-shared-profile-")
        first = CamoufoxRuntime(
            inventory=configured,
            workspace=workspace,
            browser_factory=fake_factory({}),
        )
        second = CamoufoxRuntime(
            inventory=configured,
            workspace=workspace,
            browser_factory=fake_factory({}),
        )
        first.execute("camoufox-start", {"path": "/assessment"}, context={"runId": "first-run"})
        with self.assertRaisesRegex(ValueError, "leased by another active Run"):
            second.execute("camoufox-start", {"path": "/assessment"}, context={"runId": "second-run"})
        first.execute("camoufox-close", {"sessionId": "first-run"}, context={"runId": "first-run"})
        self.assertEqual(
            second.execute("camoufox-start", {"path": "/assessment"}, context={"runId": "second-run"})[
                "sessionId"
            ],
            "second-run",
        )
        second.execute("camoufox-close", {"sessionId": "second-run"}, context={"runId": "second-run"})

    def test_start_requires_explicit_choice_when_inventory_is_ambiguous(self):
        service, _ = make_runtime()
        with self.assertRaisesRegex(ValueError, "target must be selected"):
            service.execute("camoufox-start", {}, context={"runId": "run-123"})

    def test_navigate_reuses_active_session_for_direct_https_url(self):
        service, state = make_runtime()
        start_session(service, "forum-nav", target="forum", path="/community/start", profile="standard", proxy_pool="direct")
        handle = service.sessions["forum-nav"]["handle"]
        result = service.execute(
            "camoufox-navigate",
            {"sessionId": "forum-nav", "url": "https://old.forum.example/community/thread/1"},
        )
        self.assertIs(service.sessions["forum-nav"]["handle"], handle)
        self.assertEqual(state["url"], "https://old.forum.example/community/thread/1")
        self.assertEqual(result["url"], state["url"])
        self.assertNotIn("targetId", result)
        self.assertEqual(service.sessions["forum-nav"]["target_id"], "unrestricted")

    def test_follow_link_navigates_current_anchor_without_write_authority(self):
        service, state = make_runtime({"elements": [
            {"ref": 1, "role": "link", "name": "Next discussion", "href": "https://forum.example/community/thread/2"},
            {"ref": 2, "role": "button", "name": "Publish"},
        ]})
        start_session(service, "forum-follow", target="forum", path="/community", profile="standard", proxy_pool="direct")
        snapshot = service.execute("camoufox-snapshot", {"sessionId": "forum-follow"})
        result = service.execute(
            "camoufox-follow-link",
            {"sessionId": "forum-follow", "target": snapshot["elements"][0]["ref"], "intent": "Read the next discussion"},
        )
        self.assertEqual(result["url"], "https://forum.example/community/thread/2")
        self.assertEqual(state["url"], result["url"])
        self.assertNotIn("click", state)
        with self.assertRaisesRegex(ValueError, "current navigable link"):
            service.execute(
                "camoufox-follow-link",
                {"sessionId": "forum-follow", "target": snapshot["elements"][0]["ref"], "intent": "Reuse a stale link"},
            )

    def test_follow_link_rejects_controls_and_destinations_outside_active_scope(self):
        service, _ = make_runtime({"elements": [
            {"ref": 1, "role": "button", "name": "Continue"},
            {"ref": 2, "role": "link", "name": "Account", "href": "https://forum.example/account/settings"},
            {"ref": 3, "role": "link", "name": "External", "href": "https://outside.example/community"},
        ]})
        start_session(service, "forum-follow-deny", target="forum", path="/community", profile="standard", proxy_pool="direct")
        snapshot = service.execute("camoufox-snapshot", {"sessionId": "forum-follow-deny"})
        for index, message in [(0, "current navigable link"), (1, "authorized target scope"), (2, "authorized target scope")]:
            with self.assertRaisesRegex(ValueError, message):
                service.execute(
                    "camoufox-follow-link",
                    {"sessionId": "forum-follow-deny", "target": snapshot["elements"][index]["ref"], "intent": "Inspect this destination"},
                )

    def test_navigate_target_guard_is_opt_in_and_fails_closed(self):
        service, state = make_runtime()
        start_session(service, "forum-nav-deny", target="forum", path="/community", profile="standard", proxy_pool="direct")
        original = state["url"]
        with self.assertRaisesRegex(ValueError, "authorized target"):
            service.execute(
                "camoufox-navigate",
                {"sessionId": "forum-nav-deny", "targetId": "missing", "path": "/community"},
            )
        with self.assertRaisesRegex(ValueError, "outside"):
            service.execute(
                "camoufox-navigate",
                {"sessionId": "forum-nav-deny", "targetId": "forum-old", "path": "/account/settings"},
            )
        self.assertEqual(state["url"], original)
        self.assertEqual(service.sessions["forum-nav-deny"]["target_id"], "forum")

    def test_direct_navigation_rejects_unsafe_url_forms(self):
        self.assertEqual(navigation_url("https://old.forum.example/community"), "https://old.forum.example/community")
        for value in ["file:///etc/passwd", "javascript:alert(1)", "https://user:password@example.com/", "//example.com/path", None]:
            with self.assertRaises(ValueError, msg=value):
                navigation_url(value)

    def test_direct_navigation_cannot_export_assessment_identity(self):
        service, _ = make_runtime()
        start_session(service, "assessment-nav", target="owned", path="/assessment", profile="seeded", proxy_pool="rotating")
        with self.assertRaisesRegex(ValueError, "assessment-only"):
            service.execute(
                "camoufox-navigate",
                {"sessionId": "assessment-nav", "url": "https://old.forum.example/community"},
            )

    def test_assessment_only_identity_blocked_on_third_party(self):
        service, _ = make_runtime()
        with self.assertRaisesRegex(ValueError, "assessment-only"):
            start_session(service, "bad-profile", target="forum", path="/community", profile="seeded", proxy_pool="direct")
        with self.assertRaisesRegex(ValueError, "assessment-only"):
            start_session(service, "bad-proxy", target="forum", path="/community", profile="standard", proxy_pool="rotating")

    def test_challenge_requires_human_and_blocks_interaction(self):
        service, _ = make_runtime({"text": "Security check: verify you are human with CAPTCHA"})
        start_session(service, "forum-1", target="forum", path="/community", profile="standard", proxy_pool="direct")
        snapshot = service.execute("camoufox-snapshot", {"sessionId": "forum-1"})
        self.assertTrue(snapshot["requiresHuman"])
        self.assertIn("captcha", snapshot["challenges"])
        with self.assertRaisesRegex(ValueError, "human completion"):
            service.execute(
                "camoufox-click",
                {"sessionId": "forum-1", "target": snapshot["elements"][0]["ref"], "writeAuthorized": True, "idempotencyKey": "challenge-click-1"},
            )
        self.assertEqual(service.execute("camoufox-report", {"sessionId": "forum-1"})["outcome"], "challenged")

    def test_reddit_js_challenge_is_typed_at_snapshot_boundary(self):
        service, state = make_runtime({"text": "File a ticket"})
        start_session(service, "reddit-challenge", target="forum", path="/community", profile="standard", proxy_pool="direct")
        state["url"] = "https://www.reddit.com/r/test/js_challenge"
        snapshot = service.execute("camoufox-snapshot", {"sessionId": "reddit-challenge"})
        self.assertEqual(snapshot["challenges"], ["anti_bot"])
        self.assertTrue(snapshot["requiresHuman"])

    def test_click_is_idempotent_and_refs_go_stale(self):
        service, state = make_runtime()
        start_session(service, "owned-1", target="owned", path="/assessment", profile="seeded", proxy_pool="rotating")
        self.assertEqual(state["launch"]["proxy"], "http://proxy.internal:8080")
        snapshot = service.execute("camoufox-snapshot", {"sessionId": "owned-1"})
        request = {
            "sessionId": "owned-1",
            "target": snapshot["elements"][0]["ref"],
            "writeAuthorized": True,
            "idempotencyKey": "click-owned-1",
        }
        self.assertTrue(service.execute("camoufox-click", request)["success"])
        self.assertTrue(service.execute("camoufox-click", request)["duplicate"])
        with self.assertRaisesRegex(ValueError, "stale"):
            service.execute("camoufox-click", {**request, "idempotencyKey": "click-owned-2"})

    def test_snapshot_attaches_bounded_model_media_and_supports_current_coordinates(self):
        service, state = make_runtime({"model_media": b"jpeg-screen"})
        start_session(service, "visual-1", target="owned", path="/assessment", profile="seeded", proxy_pool="rotating")
        snapshot = service.execute("camoufox-snapshot", {"sessionId": "visual-1", "includeScreenshot": True})
        self.assertEqual(base64.b64decode(snapshot["modelMedia"]["contentBase64"]), b"jpeg-screen")
        self.assertEqual(snapshot["modelMedia"]["width"], 1280)
        result = service.execute(
            "camoufox-click",
            {
                "sessionId": "visual-1", "generation": snapshot["generation"], "x": 640, "y": 360,
                "intent": "Activate the visually identified control", "writeAuthorized": True,
                "idempotencyKey": "visual-click-1",
            },
        )
        self.assertTrue(result["success"])
        self.assertEqual(state["click_point"], (640, 360))

    def test_snapshot_is_text_only_by_default_and_preserves_semantic_target_context(self):
        service, _ = make_runtime({
            "model_media": b"jpeg-screen",
            "elements": [{
                "ref": 1,
                "role": "button",
                "name": "Continue",
                "context": "form: Sign in",
                "inViewport": True,
                "bounds": {"x": 10, "y": 20, "width": 100, "height": 40},
                "state": {"expanded": "false"},
            }],
        })
        start_session(service, "text-1", target="owned", path="/assessment", profile="seeded", proxy_pool="rotating")
        snapshot = service.execute("camoufox-snapshot", {"sessionId": "text-1"})
        self.assertNotIn("modelMedia", snapshot)
        self.assertEqual(snapshot["elements"][0]["context"], "form: Sign in")
        self.assertTrue(snapshot["elements"][0]["inViewport"])
        self.assertEqual(snapshot["elements"][0]["state"], {"expanded": "false"})

    def test_coordinate_click_rejects_stale_generation_and_out_of_bounds_point(self):
        service, _ = make_runtime()
        start_session(service, "visual-stale", target="owned", path="/assessment", profile="seeded", proxy_pool="rotating")
        snapshot = service.execute("camoufox-snapshot", {"sessionId": "visual-stale"})
        base = {
            "sessionId": "visual-stale", "generation": snapshot["generation"], "x": 10, "y": 10,
            "intent": "Activate the visually identified control", "writeAuthorized": True,
        }
        with self.assertRaisesRegex(ValueError, "stale"):
            service.execute("camoufox-click", {**base, "generation": 99, "idempotencyKey": "visual-stale-1"})
        with self.assertRaisesRegex(ValueError, "outside"):
            service.execute("camoufox-click", {**base, "x": 5000, "idempotencyKey": "visual-stale-2"})

    def test_select_and_scroll(self):
        service, state = make_runtime()
        start_session(service, "sel-1", target="owned", path="/assessment", profile="seeded", proxy_pool="rotating")
        snapshot = service.execute("camoufox-snapshot", {"sessionId": "sel-1"})
        result = service.execute(
            "camoufox-select",
            {
                "sessionId": "sel-1",
                "target": snapshot["elements"][0]["ref"],
                "value": "new",
                "writeAuthorized": True,
                "idempotencyKey": "select-sel-1",
            },
        )
        self.assertTrue(result["success"])
        self.assertEqual(state["select"], {"marker": 1, "value": "new"})
        self.assertEqual(service.execute("camoufox-scroll", {"sessionId": "sel-1", "dy": 600})["scrolled"], True)
        self.assertEqual(state["scroll"], (0, 600))
        with self.assertRaisesRegex(ValueError, "out of range"):
            service.execute("camoufox-scroll", {"sessionId": "sel-1", "dy": 50000})

    def test_timed_out_action_invalidates_only_its_session(self):
        service, state = make_runtime({"timeout_scroll": True})
        start_session(service, "stuck-1", target="owned", path="/assessment", profile="seeded", proxy_pool="rotating")
        with self.assertRaisesRegex(BrowserOperationTimeout, "scroll exceeded"):
            service.execute("camoufox-scroll", {"sessionId": "stuck-1", "dy": 600})
        with self.assertRaisesRegex(ValueError, "session is unavailable"):
            service.execute("camoufox-snapshot", {"sessionId": "stuck-1"})
        service.execute("camoufox-close", {"sessionId": "stuck-1"})

        state["timeout_scroll"] = False
        start_session(service, "fresh-1", target="owned", path="/assessment", profile="seeded", proxy_pool="rotating")
        self.assertEqual(
            service.execute("camoufox-scroll", {"sessionId": "fresh-1", "dy": 300})["scrolled"],
            True,
        )

    def test_screenshot_returns_bounded_png_evidence(self):
        service, state = make_runtime()
        start_session(service, "shot-1", target="owned", path="/assessment", profile="seeded", proxy_pool="rotating")
        result = service.execute("camoufox-screenshot", {"sessionId": "shot-1", "fullPage": True})
        self.assertEqual(result["mediaType"], "image/png")
        self.assertTrue(state["screenshot_full_page"])
        self.assertGreater(result["bytes"], 0)
        self.assertEqual(base64.b64decode(result["contentBase64"])[:8], b"\x89PNG\r\n\x1a\n")

    def test_credential_values_stay_out_of_outputs(self):
        service, state = make_runtime({
            "text": "Login form",
            "elements": [{
                "ref": 1,
                "role": "textbox",
                "name": "Password",
                "state": {"filled": False, "value": "must-never-project"},
            }],
            "reflect_fill": True,
        })
        start_session(service, "login-1", target="forum", path="/community/login", profile="standard", proxy_pool="direct")
        snapshot = service.execute("camoufox-snapshot", {"sessionId": "login-1"})
        self.assertEqual(snapshot["elements"][0]["state"], {"filled": False})
        request = {
            "sessionId": "login-1",
            "target": snapshot["elements"][0]["ref"],
            "credentialField": "password",
            "writeAuthorized": True,
            "idempotencyKey": "secret-fill-1",
        }
        with self.assertRaisesRegex(ValueError, "credential binding"):
            service.execute("camoufox-fill", {**request, "value": "literal-secret"})
        import json

        first = service.execute("camoufox-fill-secret", request, {"password": "correct horse battery staple"})
        self.assertNotIn("correct horse", json.dumps(first))
        duplicate = service.execute("camoufox-fill-secret", request, {"password": "correct horse battery staple"})
        self.assertTrue(duplicate["duplicate"])
        redacted = service.execute("camoufox-snapshot", {"sessionId": "login-1"})
        self.assertEqual(redacted["text"], "Echo [REDACTED]")
        self.assertNotIn("value", redacted["elements"][0]["state"])
        self.assertNotIn("correct horse", json.dumps(service.execute("camoufox-report", {"sessionId": "login-1"})))

    def test_snapshot_element_state_exposes_only_boolean_occupancy(self):
        projected = snapshot_element_state({"state": {
            "filled": True,
            "required": True,
            "type": "password",
            "value": "correct horse battery staple",
            "textContent": "correct horse battery staple",
        }})
        self.assertEqual(projected, {"required": True, "filled": True, "type": "password"})
        self.assertNotIn("correct horse", str(projected))

    def test_close_preserves_profile_and_removes_session(self):
        service, state = make_runtime()
        start_session(service, "close-1", target="owned", path="/assessment", profile="seeded", proxy_pool="rotating")
        result = service.execute("camoufox-close", {"sessionId": "close-1"})
        self.assertTrue(result["closed"])
        self.assertTrue(state["closed"])
        with self.assertRaisesRegex(ValueError, "not found"):
            service.execute("camoufox-snapshot", {"sessionId": "close-1"})


if __name__ == "__main__":
    unittest.main()
