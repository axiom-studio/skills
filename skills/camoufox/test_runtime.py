import base64
import json
import os
import sqlite3
import tempfile
import threading
import unittest
from datetime import datetime, timedelta, timezone

import yaml

from runtime import (
    SNAPSHOT_JS,
    BrowserOperationTimeout,
    BrowserWorker,
    CamoufoxHandle,
    CamoufoxRuntime,
    agent_session_id,
    exact_path,
    load_inventory,
    navigation_url,
    observation_digest,
    resolve_proxy,
    run_digest,
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

    def fresh_page(self):
        self.state["fresh_pages"] = self.state.get("fresh_pages", 0) + 1
        self.state["url"] = "about:blank"
        self.state.pop("text", None)
        self.state.pop("elements", None)
        self.state.pop("fill", None)
        self.state.pop("click", None)
        self.state.pop("solved", None)

    def snapshot(self, include_model_media=False):
        if self.state.get("click") and self.state.get("delayed_progress_snapshots"):
            self.state["post_click_snapshots"] = self.state.get("post_click_snapshots", 0) + 1
            if self.state["post_click_snapshots"] >= self.state["delayed_progress_snapshots"]:
                self.state["solved"] = True
        if self.state.get("solved"):
            return {
                "url": self.state["url"],
                "title": "Accepted",
                "text": self.state.get("accepted_text", "Assessment accepted"),
                "elements": self.state.get("accepted_elements", []),
            }
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

    def click(self, marker, exact=False):
        self.state["click"] = marker
        self.state["exact_click"] = exact
        if not self.state.get("no_progress") and not self.state.get("delayed_progress_snapshots"):
            self.state["solved"] = True

    def click_point(self, x, y):
        self.state["click_point"] = (x, y)
        if not self.state.get("no_progress"):
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
        context={"agentId": run_id, "runId": run_id},
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

    def test_snapshot_uses_remaining_capacity_for_nearby_off_viewport_controls(self):
        self.assertIn("viewportDistance", SNAPSHOT_JS)
        self.assertIn("focusedForm", SNAPSHOT_JS)
        self.assertIn("focusAffinity", SNAPSHOT_JS)
        self.assertIn(".filter(({element}) => visible(element))", SNAPSHOT_JS)
        self.assertNotIn("visible(e) && inViewport(e)", SNAPSHOT_JS)

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

    def test_external_commit_activates_exact_locator_once(self):
        calls = []

        class Locator:
            first = None

            def __init__(self):
                self.first = self

            def evaluate(self, expression, timeout):
                calls.append(("scroll", expression, timeout))

            def bounding_box(self):
                return {"x": 10, "y": 20, "width": 30, "height": 40}

        class Mouse:
            @staticmethod
            def move(x, y):
                calls.append(("move", x, y))

            @staticmethod
            def click(x, y):
                calls.append(("click", x, y))

        class Page:
            mouse = Mouse()

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
        handle.click("21", exact=True)

        self.assertEqual(calls, [
            ("locator", '[data-camoufox-ref="21"]'),
            ("scroll", "element => element.scrollIntoView({block: 'center', inline: 'nearest'})", 5000),
            ("move", 25.0, 40.0),
            ("click", 25.0, 40.0),
        ])

    def test_fill_replaces_exact_control_without_pointer_stability_gate(self):
        calls = []

        class Locator:
            first = None

            def __init__(self):
                self.first = self

            def evaluate(self, script, *args, timeout):
                calls.append(("evaluate", script, args, timeout))
                if "matches" in script:
                    return True
                if "scrollIntoView" in script:
                    return None
                # Simulate a controlled editor replacing the original node.
                return ""

            def bounding_box(self):
                return {"x": 10, "y": 20, "width": 30, "height": 40}

        class Mouse:
            @staticmethod
            def move(x, y):
                calls.append(("move", x, y))

            @staticmethod
            def click(x, y):
                calls.append(("click", x, y))

        class Keyboard:
            @staticmethod
            def press(key):
                calls.append(("press", key))

            @staticmethod
            def type(value, delay):
                calls.append(("type", value, delay))

        class Page:
            mouse = Mouse()
            keyboard = Keyboard()

            @staticmethod
            def locator(selector):
                calls.append(("locator", selector))
                return Locator()

            @staticmethod
            def evaluate(script, value):
                calls.append(("live-readback", script, value))
                return value == "replacement"

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
            ("evaluate", "(element, selector) => element.matches(selector)", ('input, textarea, [contenteditable="true"], [role="textbox"]',), 5000),
            ("evaluate", "element => element.scrollIntoView({block: 'center', inline: 'nearest'})", (), 5000),
            ("move", 25.0, 40.0),
            ("click", 25.0, 40.0),
            ("press", "Control+A"),
            ("press", "Backspace"),
            ("type", "replacement", 8),
            ("evaluate", "element => ('value' in element ? element.value : (element.innerText || element.textContent || ''))", (), 5000),
            ("live-readback", "expected => Array.from(document.querySelectorAll('input, textarea, [contenteditable=\"true\"], [role=\"textbox\"]')).some(element => (('value' in element ? element.value : (element.innerText || element.textContent || '')) === expected))", "replacement"),
        ])


class RuntimeTest(unittest.TestCase):
    def test_manifest_uses_kernel_owned_interaction_authority(self):
        manifest_path = os.path.join(os.path.dirname(__file__), "skill.yaml")
        with open(manifest_path, "r", encoding="utf-8") as stream:
            definition = yaml.safe_load(stream)["definition"]
        self.assertEqual(definition["version"], "2.0.37")
        actions = definition["actions"]
        for action in actions.values():
            input_schema = action.get("inputSchema", {})
            if "sessionId" not in input_schema.get("required", []):
                continue
            session_id = input_schema["properties"]["sessionId"]
            self.assertIs(session_id["x-openseal-kernel-resolved"], True)
            self.assertEqual(session_id["x-openseal-kernel-source"], "agent_session_id")
        for name in ("camoufox-click", "camoufox-fill", "camoufox-fill-secret", "camoufox-select"):
            action = actions[name]
            self.assertEqual((action["risk"], action["sideEffect"]), ("write", "write"))
            self.assertNotIn("writeAuthorized", action["inputSchema"]["properties"])
            self.assertNotIn("writeAuthorized", action["inputSchema"]["required"])
        commit = actions["camoufox-commit"]
        for name in (
            "camoufox-start", "camoufox-navigate", "camoufox-snapshot", "camoufox-follow-link",
            "camoufox-click", "camoufox-commit", "camoufox-fill", "camoufox-fill-secret",
            "camoufox-select", "camoufox-scroll", "camoufox-screenshot", "camoufox-report",
        ):
            self.assertEqual(actions[name]["finalizerAction"], "camoufox-close")
        self.assertEqual((commit["risk"], commit["sideEffect"]), ("external", "external"))
        self.assertEqual(commit["externalOperationPolicy"], "required")
        self.assertEqual(commit["inputSchema"]["required"], ["sessionId", "target", "intent", "idempotencyKey", "postcondition"])
        self.assertEqual(commit["inputSchema"]["properties"]["target"]["x-openseal-observationRef"], {
            "roles": ["button"], "requireEnabled": True,
        })
        self.assertEqual(commit["requiredEvidence"], [{"action": "camoufox-fill", "matchingArguments": ["sessionId"]}])
        self.assertIn("Never target a textbox", commit["description"])
        self.assertIn("never submits", actions["camoufox-fill"]["description"])
        self.assertIn("Never pass a textbox reference to camoufox-commit", definition["prompt"]["instructions"])

    def test_health_reports_configuration_state(self):
        service, _ = make_runtime(inv=None)
        self.assertEqual(service.execute("camoufox-health")["status"], "needs_configuration")
        with self.assertRaisesRegex(ValueError, "not configured"):
            service.execute("camoufox-start", {}, context={"agentId": "unconfigured", "runId": "unconfigured"})
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
        result = service.execute(
            "camoufox-start",
            {"path": "/community"},
            context={"agentId": "run-123", "runId": "run-123"},
        )
        self.assertEqual(result["sessionId"], "run-123")
        self.assertNotIn("targetId", result)
        self.assertNotIn("profileId", result)
        self.assertNotIn("proxyPoolId", result)

        repeated = service.execute(
            "camoufox-start",
            {"path": "/community"},
            context={"agentId": "run-123", "runId": "run-123"},
        )
        self.assertEqual(repeated, result)

    def test_start_derives_shared_handle_from_durable_agent_context(self):
        configured = inventory()
        configured["defaults"] = {
            "targetId": "forum",
            "profileId": "standard",
            "proxyPoolId": "direct",
        }
        service, _ = make_runtime(inv=configured)
        long_agent_id = "tenant/workforce/" + "a" * 160
        first_context = {"agentId": long_agent_id, "runId": "run-one"}
        first = service.execute("camoufox-start", {"path": "/community"}, context=first_context)
        self.assertRegex(first["sessionId"], r"^agent-[0-9a-f]{32}$")
        self.assertEqual(
            service.execute("camoufox-start", {"path": "/community"}, context=first_context),
            first,
        )
        self.assertEqual(
            service.execute("camoufox-snapshot", {"sessionId": first["sessionId"]})["sessionId"],
            first["sessionId"],
        )
        service.execute("camoufox-close", {"sessionId": first["sessionId"]}, context=first_context)
        second = service.execute(
            "camoufox-start",
            {"path": "/community"},
            context={"agentId": long_agent_id, "runId": "another-run"},
        )
        self.assertEqual(second["sessionId"], first["sessionId"])
        for field in ["sessionId", "targetId", "profileId", "proxyPoolId"]:
            with self.assertRaisesRegex(ValueError, "host-owned"):
                service.execute(
                    "camoufox-start",
                    {field: "caller-choice"},
                    context={"agentId": "third-run", "runId": "third-run"},
                )

    def test_start_replaces_terminal_session_for_same_durable_run(self):
        service, state = make_runtime()
        first = start_session(service, "terminal-run", target="owned", path="/assessment", profile="seeded", proxy_pool="rotating")
        service.sessions[first["sessionId"]]["terminal_error"] = "browser operation timed out; start a new session"

        replacement = service.execute(
            "camoufox-start",
            {"path": "/assessment"},
            context={"agentId": "terminal-run", "runId": "terminal-run"},
        )

        self.assertEqual(replacement["sessionId"], first["sessionId"])
        self.assertEqual(len(state["launches"]), 2)
        self.assertNotIn("terminal_error", service.sessions[first["sessionId"]])

    def test_start_requires_durable_agent_context(self):
        configured = inventory()
        configured["defaults"] = {
            "targetId": "forum",
            "profileId": "standard",
            "proxyPoolId": "direct",
        }
        service, _ = make_runtime(inv=configured)
        with self.assertRaisesRegex(ValueError, "durable Agent context"):
            service.execute("camoufox-start", {"path": "/community"})

    def test_sessions_and_profiles_are_isolated_by_durable_agent(self):
        service, state = make_runtime()
        service.inventory["defaults"] = {
            "targetId": "owned",
            "profileId": "seeded",
            "proxyPoolId": "rotating",
        }
        first_context = {"agentId": "agent:a", "runId": "run-a"}
        second_context = {"agentId": "agent:b", "runId": "run-b"}
        first = service.execute(
            "camoufox-start",
            {"path": "/assessment"},
            context=first_context,
        )
        with self.assertRaisesRegex(ValueError, "different durable Agent"):
            service.execute(
                "camoufox-snapshot",
                {"sessionId": first["sessionId"]},
                context=second_context,
            )
        second = service.execute(
            "camoufox-start",
            {"path": "/assessment"},
            context=second_context,
        )
        self.assertNotEqual(first["sessionId"], second["sessionId"])
        self.assertNotEqual(
            service.sessions[first["sessionId"]]["lease_path"],
            service.sessions[second["sessionId"]]["lease_path"],
        )
        self.assertEqual(len(state["launches"]), 2)
        service.execute(
            "camoufox-close",
            {"sessionId": first["sessionId"]},
            context=first_context,
        )
        service.execute(
            "camoufox-close",
            {"sessionId": second["sessionId"]},
            context=second_context,
        )

    def test_agent_session_is_reused_across_serialized_run_usage_leases(self):
        configured = inventory()
        configured["defaults"] = {
            "targetId": "owned",
            "profileId": "seeded",
            "proxyPoolId": "rotating",
        }
        state = {}
        clock = {"now": datetime(2026, 7, 27, tzinfo=timezone.utc)}
        service = CamoufoxRuntime(
            inventory=configured,
            workspace=tempfile.mkdtemp(prefix="camoufox-agent-session-"),
            browser_factory=fake_factory(state),
            now=lambda: clock["now"],
            lease_ttl_seconds=30,
        )
        first_context = {"agentId": "agent:shared", "runId": "run-a"}
        second_context = {"agentId": "agent:shared", "runId": "run-b"}
        first = service.execute("camoufox-start", {"path": "/assessment"}, context=first_context)
        first_handle = service.sessions[first["sessionId"]]["handle"]

        with self.assertRaisesRegex(ValueError, "usage is leased by another active Run"):
            service.execute("camoufox-start", {"path": "/assessment"}, context=second_context)
        with self.assertRaisesRegex(ValueError, "usage is leased by another active Run"):
            service.execute(
                "camoufox-snapshot",
                {"sessionId": first["sessionId"]},
                context=second_context,
            )

        released = service.execute(
            "camoufox-close",
            {"sessionId": first["sessionId"]},
            context=first_context,
        )
        self.assertTrue(released["usageReleased"])
        self.assertTrue(released["sessionPreserved"])
        self.assertFalse(released["closed"])

        snapshot = service.execute(
            "camoufox-snapshot",
            {"sessionId": first["sessionId"]},
            context=second_context,
        )
        self.assertEqual(snapshot["sessionId"], first["sessionId"])

        second = service.execute("camoufox-start", {"path": "/assessment"}, context=second_context)
        self.assertEqual(second["sessionId"], first["sessionId"])
        self.assertIs(service.sessions[second["sessionId"]]["handle"], first_handle)
        self.assertEqual(len(state["launches"]), 1)
        self.assertEqual(state["fresh_pages"], 1)
        self.assertEqual(second["url"], "http://fixture.internal:8080/assessment")

        lease_path = service.sessions[second["sessionId"]]["lease_path"]
        with open(lease_path, "r", encoding="utf-8") as handle:
            lease = json.load(handle)
        self.assertEqual(lease["state"], "active")
        self.assertEqual(lease["runDigest"], run_digest(second_context))
        self.assertRegex(lease["agentDigest"], r"^sha256:[0-9a-f]{64}$")

    def test_distinct_run_starts_on_fresh_page_but_same_run_is_idempotent(self):
        service, state = make_runtime()
        service.inventory["defaults"] = {
            "targetId": "owned",
            "profileId": "seeded",
            "proxyPoolId": "rotating",
        }
        first_context = {"agentId": "agent:shared", "runId": "run-a"}
        second_context = {"agentId": "agent:shared", "runId": "run-b"}
        first = service.execute("camoufox-start", {"path": "/assessment/first"}, context=first_context)
        self.assertEqual(
            service.execute("camoufox-start", {"path": "/assessment/ignored"}, context=first_context),
            first,
        )
        self.assertNotIn("fresh_pages", state)

        service.execute("camoufox-close", {"sessionId": first["sessionId"]}, context=first_context)
        second = service.execute("camoufox-start", {"path": "/assessment/second"}, context=second_context)

        self.assertEqual(second["sessionId"], first["sessionId"])
        self.assertEqual(second["url"], "http://fixture.internal:8080/assessment/second")
        self.assertEqual(state["fresh_pages"], 1)
        self.assertEqual(len(state["launches"]), 1)
        session = service.sessions[second["sessionId"]]
        self.assertEqual(session["generation"], 0)
        self.assertEqual(session["receipts"], {})
        self.assertIsNone(session["last_fill"])

    def test_expired_run_usage_lease_reuses_the_agent_session(self):
        configured = inventory()
        configured["defaults"] = {
            "targetId": "owned",
            "profileId": "seeded",
            "proxyPoolId": "rotating",
        }
        state = {}
        clock = {"now": datetime(2026, 7, 27, tzinfo=timezone.utc)}
        service = CamoufoxRuntime(
            inventory=configured,
            workspace=tempfile.mkdtemp(prefix="camoufox-agent-expiry-"),
            browser_factory=fake_factory(state),
            now=lambda: clock["now"],
            lease_ttl_seconds=30,
        )
        first_context = {"agentId": "agent:shared", "runId": "run-a"}
        second_context = {"agentId": "agent:shared", "runId": "run-b"}
        first = service.execute("camoufox-start", {"path": "/assessment"}, context=first_context)
        first_handle = service.sessions[first["sessionId"]]["handle"]

        clock["now"] += timedelta(seconds=31)
        second = service.execute("camoufox-start", {"path": "/assessment"}, context=second_context)

        self.assertEqual(second["sessionId"], first["sessionId"])
        self.assertIs(service.sessions[second["sessionId"]]["handle"], first_handle)
        self.assertEqual(len(state["launches"]), 1)

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
        os.symlink(
            "/nonexistent/firefox-profile-lock",
            os.path.join(profile_directory, "authenticated-session", "lock"),
        )

        start_session(
            service,
            "replacement-run",
            target="owned",
            path="/assessment",
            profile="seeded",
            proxy_pool="rotating",
        )
        data_directory = os.path.join(
            state["workspace"], "profiles", "seeded", "agents", "replacement-run", "data"
        )
        self.assertEqual(state["launch"]["user_data_dir"], data_directory)
        self.assertTrue(os.path.isfile(os.path.join(data_directory, "cookies.sqlite")))
        self.assertFalse(os.path.lexists(os.path.join(data_directory, "lock")))
        database = sqlite3.connect(os.path.join(data_directory, "cookies.sqlite"))
        self.assertEqual(database.execute("SELECT COUNT(*) FROM moz_cookies").fetchone()[0], 3)
        database.close()
        service.execute(
            "camoufox-close",
            {"sessionId": "replacement-run"},
            context={"agentId": "replacement-run", "runId": "replacement-run"},
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
        failed_context = {"agentId": "agent:shared", "runId": "failed-run"}
        replacement_context = {"agentId": "agent:shared", "runId": "replacement-run"}
        with self.assertRaisesRegex(RuntimeError, "launch failed"):
            failing.execute("camoufox-start", {"path": "/assessment"}, context=failed_context)

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
            context=replacement_context,
        )
        self.assertEqual(started["sessionId"], agent_session_id(replacement_context))
        with open(replacement.sessions[started["sessionId"]]["lease_path"], "r", encoding="utf-8") as handle:
            lease = json.load(handle)
        self.assertEqual(lease["state"], "active")
        self.assertEqual(lease["generation"], 2)
        replacement.execute(
            "camoufox-close",
            {"sessionId": started["sessionId"]},
            context=replacement_context,
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
        first_context = {"agentId": "agent:shared", "runId": "first-run"}
        second_context = {"agentId": "agent:shared", "runId": "second-run"}
        started = first.execute("camoufox-start", {"path": "/assessment"}, context=first_context)
        with self.assertRaisesRegex(ValueError, "leased by another active Run"):
            second.execute("camoufox-start", {"path": "/assessment"}, context=second_context)
        first.execute("camoufox-close", {"sessionId": started["sessionId"]}, context=first_context)
        first._release_session(first.sessions[started["sessionId"]], "binding_disabled")
        self.assertEqual(
            second.execute("camoufox-start", {"path": "/assessment"}, context=second_context)[
                "sessionId"
            ],
            started["sessionId"],
        )
        second.execute("camoufox-close", {"sessionId": started["sessionId"]}, context=second_context)

    def test_abandoned_profile_lease_is_reclaimed_without_runtime_restart(self):
        configured = inventory()
        configured["defaults"] = {
            "targetId": "owned",
            "profileId": "seeded",
            "proxyPoolId": "rotating",
        }
        workspace = tempfile.mkdtemp(prefix="camoufox-expiring-profile-")
        state = {}
        clock = {"now": datetime(2026, 7, 27, tzinfo=timezone.utc)}
        service = CamoufoxRuntime(
            inventory=configured,
            workspace=workspace,
            browser_factory=fake_factory(state),
            now=lambda: clock["now"],
            lease_ttl_seconds=30,
        )
        first_context = {"agentId": "agent:shared", "runId": "failed-run"}
        second_context = {"agentId": "agent:shared", "runId": "replacement-run"}
        first = service.execute("camoufox-start", {"path": "/assessment"}, context=first_context)
        lease_path = service.sessions[first["sessionId"]]["lease_path"]
        with open(lease_path, "r", encoding="utf-8") as handle:
            first_lease = json.load(handle)
        self.assertEqual(first_lease["expiresAt"], (clock["now"] + timedelta(seconds=30)).isoformat())

        clock["now"] += timedelta(seconds=31)
        replacement = service.execute(
            "camoufox-start", {"path": "/assessment"}, context=second_context
        )
        self.assertEqual(replacement["sessionId"], first["sessionId"])
        self.assertEqual(len(state["launches"]), 1)
        with open(lease_path, "r", encoding="utf-8") as handle:
            replacement_lease = json.load(handle)
        self.assertEqual(replacement_lease["state"], "active")
        self.assertEqual(replacement_lease["generation"], first_lease["generation"])
        self.assertEqual(replacement_lease["sessionId"], first["sessionId"])
        self.assertEqual(replacement_lease["runDigest"], run_digest(second_context))
        service.execute(
            "camoufox-close",
            {"sessionId": replacement["sessionId"]},
            context=second_context,
        )

    def test_owner_activity_renews_profile_lease(self):
        configured = inventory()
        configured["defaults"] = {"targetId": "owned", "profileId": "seeded", "proxyPoolId": "rotating"}
        workspace = tempfile.mkdtemp(prefix="camoufox-renewing-profile-")
        clock = {"now": datetime(2026, 7, 27, tzinfo=timezone.utc)}
        service = CamoufoxRuntime(
            inventory=configured,
            workspace=workspace,
            browser_factory=fake_factory({}),
            now=lambda: clock["now"],
            lease_ttl_seconds=30,
        )
        active_context = {"agentId": "agent:shared", "runId": "active-run"}
        other_context = {"agentId": "agent:shared", "runId": "other-run"}
        started = service.execute("camoufox-start", {"path": "/assessment"}, context=active_context)
        clock["now"] += timedelta(seconds=20)
        service.execute(
            "camoufox-snapshot",
            {"sessionId": started["sessionId"]},
            context=active_context,
        )
        lease_path = service.sessions[started["sessionId"]]["lease_path"]
        with open(lease_path, "r", encoding="utf-8") as handle:
            renewed = json.load(handle)
        self.assertEqual(renewed["expiresAt"], (clock["now"] + timedelta(seconds=30)).isoformat())
        clock["now"] += timedelta(seconds=20)
        with self.assertRaisesRegex(ValueError, "leased by another active Run"):
            service.execute("camoufox-start", {"path": "/assessment"}, context=other_context)
        service.execute(
            "camoufox-close",
            {"sessionId": started["sessionId"]},
            context=active_context,
        )

    def test_approved_action_reacquires_expired_usage_without_losing_snapshot_refs(self):
        configured = inventory()
        configured["defaults"] = {"targetId": "owned", "profileId": "seeded", "proxyPoolId": "rotating"}
        state = {}
        clock = {"now": datetime(2026, 7, 27, tzinfo=timezone.utc)}
        service = CamoufoxRuntime(
            inventory=configured,
            workspace=tempfile.mkdtemp(prefix="camoufox-approval-wait-"),
            browser_factory=fake_factory(state),
            now=lambda: clock["now"],
            lease_ttl_seconds=30,
        )
        context = {"agentId": "agent:shared", "runId": "approved-run"}
        started = service.execute("camoufox-start", {"path": "/assessment"}, context=context)
        snapshot = service.execute(
            "camoufox-snapshot",
            {"sessionId": started["sessionId"]},
            context=context,
        )
        target = snapshot["elements"][0]["ref"]

        clock["now"] += timedelta(seconds=31)
        result = service.execute(
            "camoufox-fill",
            {
                "sessionId": started["sessionId"],
                "target": target,
                "value": "Approved comment",
                "intent": "Fill the previously approved comment",
                "idempotencyKey": "approved-comment-1",
            },
            context=context,
        )

        self.assertEqual(result["receipt"], "approved-comment-1")
        self.assertEqual(state["fill"]["value"], "Approved comment")
        self.assertEqual(service.sessions[started["sessionId"]]["run_digest"], run_digest(context))
        self.assertEqual(
            service.sessions[started["sessionId"]]["lease_expires_at"],
            clock["now"] + timedelta(seconds=30),
        )

    def test_profile_lease_ttl_is_bounded(self):
        for value in (0, 29, 3601, "invalid"):
            with self.assertRaisesRegex(ValueError, "profile lease TTL"):
                CamoufoxRuntime(inventory=inventory(), workspace=tempfile.mkdtemp(), lease_ttl_seconds=value)

    def test_default_profile_lease_spans_slow_hosted_model_turns(self):
        service = CamoufoxRuntime(
            inventory=inventory(),
            workspace=tempfile.mkdtemp(),
            browser_factory=fake_factory({}),
        )
        self.assertEqual(service.lease_ttl, timedelta(seconds=900))

    def test_start_requires_explicit_choice_when_inventory_is_ambiguous(self):
        service, _ = make_runtime()
        with self.assertRaisesRegex(ValueError, "target must be selected"):
            service.execute(
                "camoufox-start",
                {},
                context={"agentId": "run-123", "runId": "run-123"},
            )

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
        self.assertEqual(snapshot["elements"][0]["destinationScope"], "same_origin")
        self.assertEqual(snapshot["elements"][0]["destinationPath"], "/community/thread/2")
        self.assertNotIn("destinationScope", snapshot["elements"][1])
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
        self.assertEqual(snapshot["elements"][1]["destinationScope"], "same_origin")
        self.assertEqual(snapshot["elements"][1]["destinationPath"], "/account/settings")
        self.assertEqual(snapshot["elements"][2]["destinationScope"], "external_origin")
        self.assertNotIn("destinationPath", snapshot["elements"][2])
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
                {"sessionId": "forum-1", "target": snapshot["elements"][0]["ref"], "idempotencyKey": "challenge-click-1"},
            )
        self.assertEqual(service.execute("camoufox-report", {"sessionId": "forum-1"})["outcome"], "challenged")

    def test_mfa_discussion_content_is_not_an_authentication_challenge(self):
        service, _ = make_runtime({"text": "Does MFA & 2FA collect data info for AI training?"})
        start_session(service, "mfa-discussion", target="forum", path="/community", profile="standard", proxy_pool="direct")
        snapshot = service.execute("camoufox-snapshot", {"sessionId": "mfa-discussion"})
        self.assertEqual(snapshot["challenges"], [])
        self.assertFalse(snapshot["requiresHuman"])

    def test_mfa_completion_ui_requires_human(self):
        service, _ = make_runtime({"text": "Two-factor authentication is required. Enter your verification code."})
        start_session(service, "mfa-challenge", target="forum", path="/community", profile="standard", proxy_pool="direct")
        snapshot = service.execute("camoufox-snapshot", {"sessionId": "mfa-challenge"})
        self.assertEqual(snapshot["challenges"], ["mfa"])
        self.assertTrue(snapshot["requiresHuman"])

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
            "idempotencyKey": "click-owned-1",
        }
        first = service.execute("camoufox-click", request)
        self.assertTrue(first["success"])
        self.assertTrue(first["progress"]["changed"])
        duplicate = service.execute("camoufox-click", request)
        self.assertTrue(duplicate["duplicate"])
        self.assertEqual(duplicate["progress"], first["progress"])
        with self.assertRaisesRegex(ValueError, "stale"):
            service.execute("camoufox-click", {**request, "idempotencyKey": "click-owned-2"})

    def test_click_reports_success_without_observable_progress(self):
        service, _ = make_runtime({"no_progress": True})
        start_session(service, "stagnant-1", target="owned", path="/assessment", profile="seeded", proxy_pool="rotating")
        snapshot = service.execute("camoufox-snapshot", {"sessionId": "stagnant-1"})
        self.assertRegex(snapshot["observationDigest"], r"^sha256:[0-9a-f]{64}$")
        result = service.execute("camoufox-click", {
            "sessionId": "stagnant-1",
            "target": snapshot["elements"][0]["ref"],
            "intent": "Open the community rules",
            "idempotencyKey": "stagnant-click-1",
        })
        self.assertTrue(result["success"])
        self.assertFalse(result["progress"]["changed"])
        self.assertEqual(result["progress"]["beforeDigest"], result["progress"]["afterDigest"])

    def test_commit_rejects_unverified_no_progress_without_persisting_receipt(self):
        service, state = make_runtime({"no_progress": True, "element_role": "button"})
        start_session(service, "stagnant-commit", target="owned", path="/assessment", profile="seeded", proxy_pool="rotating")
        snapshot = service.execute("camoufox-snapshot", {"sessionId": "stagnant-commit"})
        request = {
            "sessionId": "stagnant-commit",
            "target": snapshot["elements"][0]["ref"],
            "intent": "Publish the reviewed response",
            "idempotencyKey": "stagnant-commit-1",
            "postcondition": {"kind": "text_present", "text": "Assessment accepted"},
        }
        with self.assertRaisesRegex(ValueError, "external commit produced no observable progress"):
            service.execute("camoufox-commit", request)
        self.assertNotIn("commit:stagnant-commit-1", service.sessions["stagnant-commit"]["receipts"])
        self.assertEqual(service.sessions["stagnant-commit"]["refs"], {})
        self.assertEqual(state["click"], 1)
        self.assertTrue(state["exact_click"])

    def test_commit_waits_for_delayed_progress_without_clicking_twice(self):
        service, state = make_runtime({"element_role": "button", "delayed_progress_snapshots": 3})
        start_session(service, "delayed-commit", target="owned", path="/assessment", profile="seeded", proxy_pool="rotating")
        snapshot = service.execute("camoufox-snapshot", {"sessionId": "delayed-commit"})
        result = service.execute("camoufox-commit", {
            "sessionId": "delayed-commit",
            "target": snapshot["elements"][0]["ref"],
            "intent": "Publish the reviewed response",
            "idempotencyKey": "delayed-commit-1",
            "postcondition": {"kind": "text_present", "text": "Assessment accepted"},
        })
        self.assertTrue(result["success"])
        self.assertTrue(result["progress"]["changed"])
        self.assertEqual(state["click"], 1)
        self.assertTrue(state["exact_click"])
        self.assertEqual(state["post_click_snapshots"], 3)

    def test_commit_returns_receipt_only_after_observable_progress(self):
        service, _ = make_runtime({"element_role": "button"})
        start_session(service, "commit-progress", target="owned", path="/assessment", profile="seeded", proxy_pool="rotating")
        snapshot = service.execute("camoufox-snapshot", {"sessionId": "commit-progress"})
        result = service.execute("camoufox-commit", {
            "sessionId": "commit-progress",
            "target": snapshot["elements"][0]["ref"],
            "intent": "Publish the reviewed response",
            "idempotencyKey": "commit-progress-1",
            "postcondition": {"kind": "text_present", "text": "Assessment accepted"},
        })
        self.assertTrue(result["success"])
        self.assertTrue(result["progress"]["changed"])
        self.assertEqual(result["receipt"], "commit-progress-1")

    def test_commit_rejects_changed_error_page_when_declared_outcome_is_absent(self):
        service, _ = make_runtime({"element_role": "button", "accepted_text": "The field is required and cannot be empty"})
        start_session(service, "commit-error", target="owned", path="/assessment", profile="seeded", proxy_pool="rotating")
        snapshot = service.execute("camoufox-snapshot", {"sessionId": "commit-error"})
        request = {
            "sessionId": "commit-error",
            "target": snapshot["elements"][0]["ref"],
            "intent": "Publish the reviewed response",
            "idempotencyKey": "commit-error-1",
            "postcondition": {"kind": "text_present", "text": "Published response"},
        }
        with self.assertRaisesRegex(ValueError, "did not produce the expected visible text"):
            service.execute("camoufox-commit", request)
        self.assertNotIn("commit:commit-error-1", service.sessions["commit-error"]["receipts"])

    def test_commit_proves_previously_filled_value_persisted_after_form_reset(self):
        posted = "A durable reviewed response"
        service, state = make_runtime({"reflect_fill": True})
        start_session(service, "commit-filled", target="owned", path="/assessment", profile="seeded", proxy_pool="rotating")
        snapshot = service.execute("camoufox-snapshot", {"sessionId": "commit-filled"})
        service.execute("camoufox-fill", {
            "sessionId": "commit-filled",
            "target": snapshot["elements"][0]["ref"],
            "value": posted,
            "intent": "Prepare the reviewed response",
            "idempotencyKey": "fill-persisted-1",
        })
        state["elements"] = [{"ref": 2, "role": "button", "name": "Publish", "state": {}}]
        state["accepted_text"] = f"Published {posted}"
        snapshot = service.execute("camoufox-snapshot", {"sessionId": "commit-filled"})
        result = service.execute("camoufox-commit", {
            "sessionId": "commit-filled",
            "target": snapshot["elements"][0]["ref"],
            "intent": "Publish the reviewed response",
            "idempotencyKey": "commit-persisted-1",
            "postcondition": {"kind": "filled_value_persisted"},
        })
        self.assertTrue(result["success"])
        self.assertEqual(result["receipt"], "commit-persisted-1")

    def test_commit_rejects_non_button_and_disabled_controls(self):
        for state, message in [
            ({"element_role": "textbox"}, "must be a current button"),
            ({"element_role": "button", "elements": [{"ref": 1, "role": "button", "name": "Submit", "state": {"disabled": True}}]}, "is disabled"),
        ]:
            with self.subTest(message=message):
                service, _ = make_runtime(state)
                start_session(service, "invalid-commit", target="owned", path="/assessment", profile="seeded", proxy_pool="rotating")
                snapshot = service.execute("camoufox-snapshot", {"sessionId": "invalid-commit"})
                with self.assertRaisesRegex(ValueError, message):
                    service.execute("camoufox-commit", {
                        "sessionId": "invalid-commit",
                        "target": snapshot["elements"][0]["ref"],
                        "intent": "Publish the reviewed response",
                        "idempotencyKey": "invalid-commit-1",
                        "postcondition": {"kind": "text_present", "text": "Assessment accepted"},
                    })

    def test_observation_digest_ignores_regenerated_references(self):
        before = {"url": "https://forum.example/community", "title": "Forum", "text": "Rules", "elements": [
            {"ref": "1", "role": "button", "name": "Rules", "state": {"expanded": "false"}},
        ]}
        after = {**before, "elements": [
            {"ref": "74", "role": "button", "name": "Rules", "state": {"expanded": "false"}},
        ]}
        self.assertEqual(observation_digest(before), observation_digest(after))

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
                "intent": "Activate the visually identified control",
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
            "intent": "Activate the visually identified control",
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

    def test_secret_fill_rejects_semantically_wrong_current_textbox(self):
        service, _ = make_runtime({
            "text": "Home",
            "elements": [{
                "ref": 1,
                "role": "textbox",
                "name": "Find anything",
                "state": {"autocomplete": "off", "filled": False},
            }],
        })
        start_session(service, "wrong-secret-target", target="forum", path="/community/login", profile="standard", proxy_pool="direct")
        snapshot = service.execute("camoufox-snapshot", {"sessionId": "wrong-secret-target"})
        request = {
            "sessionId": "wrong-secret-target",
            "target": snapshot["elements"][0]["ref"],
            "credentialField": "username",
            "intent": "Fill the login username",
            "idempotencyKey": "wrong-secret-target-1",
        }
        with self.assertRaisesRegex(ValueError, "not a current username control"):
            service.execute("camoufox-fill-secret", request, {"username": "private-user"})

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

    def test_close_releases_usage_and_preserves_agent_session(self):
        service, state = make_runtime()
        start_session(service, "close-1", target="owned", path="/assessment", profile="seeded", proxy_pool="rotating")
        result = service.execute("camoufox-close", {"sessionId": "close-1"})
        self.assertFalse(result["closed"])
        self.assertTrue(result["usageReleased"])
        self.assertTrue(result["sessionPreserved"])
        repeated = service.execute("camoufox-close", {"sessionId": "close-1"})
        self.assertTrue(repeated["usageReleased"])
        self.assertTrue(repeated["sessionPreserved"])
        self.assertFalse(state.get("closed", False))
        self.assertIn("close-1", service.sessions)
        with self.assertRaisesRegex(ValueError, "usage lease expired"):
            service.execute("camoufox-snapshot", {"sessionId": "close-1"})

    def test_close_treats_an_absent_session_as_already_released(self):
        service, _ = make_runtime()
        result = service.execute("camoufox-close", {"sessionId": "missing-session"})
        self.assertTrue(result["usageReleased"])
        self.assertTrue(result["profilePreserved"])
        self.assertFalse(result["sessionPreserved"])


if __name__ == "__main__":
    unittest.main()
