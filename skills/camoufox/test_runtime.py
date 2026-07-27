import base64
import os
import tempfile
import unittest
from datetime import datetime, timezone

from runtime import CamoufoxRuntime, exact_path, load_inventory, resolve_proxy


def inventory():
    return {
        "targets": {
            "owned": {"baseUrl": "http://fixture.internal:8080", "pathPrefixes": ["/assessment"], "mode": "owned-assessment"},
            "forum": {"baseUrl": "https://forum.example", "pathPrefixes": ["/community"], "mode": "permitted-automation"},
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
    }


class FakeHandle:
    def __init__(self, state, options):
        state["launch"] = options
        self.state = state

    def goto(self, url, timeout_ms=30000):
        self.state["url"] = url
        return {"url": url, "status": self.state.get("status", 200)}

    def snapshot(self):
        if self.state.get("solved"):
            return {"url": self.state["url"], "title": "Accepted", "text": "Assessment accepted", "elements": []}
        result = {
            "url": self.state["url"],
            "title": "Page",
            "text": self.state.get("text", "Welcome"),
            "viewport": {"width": 1280, "height": 720},
            "elements": [
                {"ref": 1, "role": self.state.get("element_role", "textbox"), "name": self.state.get("element_name", "Comment")}
            ],
        }
        if self.state.get("model_media"):
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
    return CamoufoxRuntime(
        inventory=inventory() if inv is _UNSET else inv,
        workspace=workspace,
        browser_factory=fake_factory(state),
        now=lambda: datetime(2026, 7, 27, tzinfo=timezone.utc),
    ), state


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


class RuntimeTest(unittest.TestCase):
    def test_health_reports_configuration_state(self):
        service, _ = make_runtime(inv=None)
        self.assertEqual(service.execute("camoufox-health")["status"], "needs_configuration")
        with self.assertRaisesRegex(ValueError, "not configured"):
            service.execute("camoufox-start", {"sessionId": "unconfigured"})
        ready, _ = make_runtime()
        health = ready.execute("camoufox-health")
        self.assertEqual(health["status"], "ready")
        self.assertEqual(health["authorizedTargets"], 2)

    def test_target_scope_cannot_escape_origin_or_path(self):
        target = inventory()["targets"]["owned"]
        self.assertEqual(exact_path(target, "/assessment/start"), "http://fixture.internal:8080/assessment/start")
        for value in ["https://other.example/assessment", "//other.example/assessment", "/admin", "relative"]:
            with self.assertRaises(ValueError, msg=value):
                exact_path(target, value)

    def test_start_applies_profile_options_and_rotated_proxy(self):
        service, state = make_runtime()
        service.execute(
            "camoufox-start",
            {"sessionId": "pool-1", "targetId": "forum", "path": "/community", "profileId": "standard", "proxyPoolId": "pool"},
        )
        launch = state["launch"]
        self.assertEqual(launch["proxy"], resolve_proxy(inventory()["proxy_pools"]["pool"], "pool", "pool-1"))
        self.assertEqual(launch["profile_options"], {"os": ["windows", "macos"], "humanize": True, "geoip": True})
        self.assertEqual(launch["headless"], "virtual")

    def test_assessment_only_identity_blocked_on_third_party(self):
        service, _ = make_runtime()
        with self.assertRaisesRegex(ValueError, "assessment-only"):
            service.execute(
                "camoufox-start",
                {"sessionId": "bad-profile", "targetId": "forum", "path": "/community", "profileId": "seeded", "proxyPoolId": "direct"},
            )
        with self.assertRaisesRegex(ValueError, "assessment-only"):
            service.execute(
                "camoufox-start",
                {"sessionId": "bad-proxy", "targetId": "forum", "path": "/community", "profileId": "standard", "proxyPoolId": "rotating"},
            )

    def test_challenge_requires_human_and_blocks_interaction(self):
        service, _ = make_runtime({"text": "Security check: verify you are human with CAPTCHA"})
        service.execute(
            "camoufox-start",
            {"sessionId": "forum-1", "targetId": "forum", "path": "/community", "profileId": "standard", "proxyPoolId": "direct"},
        )
        snapshot = service.execute("camoufox-snapshot", {"sessionId": "forum-1"})
        self.assertTrue(snapshot["requiresHuman"])
        self.assertIn("captcha", snapshot["challenges"])
        with self.assertRaisesRegex(ValueError, "human completion"):
            service.execute(
                "camoufox-click",
                {"sessionId": "forum-1", "target": snapshot["elements"][0]["ref"], "writeAuthorized": True, "idempotencyKey": "challenge-click-1"},
            )
        self.assertEqual(service.execute("camoufox-report", {"sessionId": "forum-1"})["outcome"], "challenged")

    def test_click_is_idempotent_and_refs_go_stale(self):
        service, state = make_runtime()
        service.execute(
            "camoufox-start",
            {"sessionId": "owned-1", "targetId": "owned", "path": "/assessment", "profileId": "seeded", "proxyPoolId": "rotating"},
        )
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
        service.execute(
            "camoufox-start",
            {"sessionId": "visual-1", "targetId": "owned", "path": "/assessment", "profileId": "seeded", "proxyPoolId": "rotating"},
        )
        snapshot = service.execute("camoufox-snapshot", {"sessionId": "visual-1"})
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

    def test_coordinate_click_rejects_stale_generation_and_out_of_bounds_point(self):
        service, _ = make_runtime()
        service.execute(
            "camoufox-start",
            {"sessionId": "visual-stale", "targetId": "owned", "path": "/assessment", "profileId": "seeded", "proxyPoolId": "rotating"},
        )
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
        service.execute(
            "camoufox-start",
            {"sessionId": "sel-1", "targetId": "owned", "path": "/assessment", "profileId": "seeded", "proxyPoolId": "rotating"},
        )
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

    def test_screenshot_returns_bounded_png_evidence(self):
        service, state = make_runtime()
        service.execute(
            "camoufox-start",
            {"sessionId": "shot-1", "targetId": "owned", "path": "/assessment", "profileId": "seeded", "proxyPoolId": "rotating"},
        )
        result = service.execute("camoufox-screenshot", {"sessionId": "shot-1", "fullPage": True})
        self.assertEqual(result["mediaType"], "image/png")
        self.assertTrue(state["screenshot_full_page"])
        self.assertGreater(result["bytes"], 0)
        self.assertEqual(base64.b64decode(result["contentBase64"])[:8], b"\x89PNG\r\n\x1a\n")

    def test_credential_values_stay_out_of_outputs(self):
        service, state = make_runtime({"text": "Login form", "element_name": "Password", "reflect_fill": True})
        service.execute(
            "camoufox-start",
            {"sessionId": "login-1", "targetId": "forum", "path": "/community/login", "profileId": "standard", "proxyPoolId": "direct"},
        )
        snapshot = service.execute("camoufox-snapshot", {"sessionId": "login-1"})
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
        self.assertNotIn("correct horse", json.dumps(service.execute("camoufox-report", {"sessionId": "login-1"})))

    def test_close_preserves_profile_and_removes_session(self):
        service, state = make_runtime()
        service.execute(
            "camoufox-start",
            {"sessionId": "close-1", "targetId": "owned", "path": "/assessment", "profileId": "seeded", "proxyPoolId": "rotating"},
        )
        result = service.execute("camoufox-close", {"sessionId": "close-1"})
        self.assertTrue(result["closed"])
        self.assertTrue(state["closed"])
        with self.assertRaisesRegex(ValueError, "not found"):
            service.execute("camoufox-snapshot", {"sessionId": "close-1"})


if __name__ == "__main__":
    unittest.main()
