"""Operator config: decode, defaults, cache, secrets, errors and Health."""

import importlib
import json
import os
import pickle
import subprocess
import sys
import tempfile
import threading
import unittest
import urllib.error
from dataclasses import dataclass
from types import SimpleNamespace

import yaml

from runtime import BrowserConfig, LightpandaRuntime, config_spec
from skill_config import (CACHE_SIZE, ConfigError, ConfigSpec, Secret, check_result, setting,
                          struct_to_dict)

KEY = "tvly-secret-key-123"
HERE = os.path.dirname(os.path.abspath(__file__))


def context(tenant="t1", version="", config=None):
    return SimpleNamespace(tenant_id=tenant, config_version=version, config=config)


class SchemaTest(unittest.TestCase):
    def test_schema_matches_the_canonical_manifest(self):
        with open(os.path.join(HERE, "skill.yaml"), encoding="utf-8") as stream:
            declared = yaml.safe_load(stream)["definition"]["config"]
        self.assertEqual(config_spec().schema(), declared)

    def test_schema_wire_form(self):
        schema = json.loads(config_spec().schema_json())
        self.assertEqual(schema["scopes"], ["global", "tenant"])
        self.assertEqual([f["key"] for f in schema["fields"]], ["searchProvider", "tavilyApiKey", "fallbackToYahoo"])
        provider, key, fallback = schema["fields"]
        self.assertEqual((provider["type"], provider["enum"], provider["default"]), ("enum", ["yahoo", "tavily"], "yahoo"))
        self.assertEqual((key["type"], key["requiredWhen"]), ("secret", {"searchProvider": "tavily"}))
        self.assertNotIn("default", key)
        self.assertEqual((fallback["type"], fallback["default"]), ("bool", False))
        self.assertTrue(all(f["summary"] for f in schema["fields"]))

    def test_invalid_declarations_are_rejected(self):
        cases = {
            "bad key": lambda: setting("Bad", label="x", type="string"),
            "secret default": lambda: setting("k", label="x", type="secret", default="v"),
        }
        for name, build in cases.items():
            with self.subTest(name=name):
                with self.assertRaises(ValueError):
                    @dataclass
                    class Cfg:
                        value: str = build()
                    ConfigSpec(Cfg)

        @dataclass
        class BadEnumDefault:
            mode: str = setting("mode", label="Mode", type="enum", enum=("a",), default="b")
        with self.assertRaises(ValueError):
            ConfigSpec(BadEnumDefault)

        @dataclass
        class WhenSecret:
            key: Secret = setting("key", label="Key", type="secret")
            other: str = setting("other", label="Other", type="string", required_when={"key": "x"})
        with self.assertRaises(ValueError):
            ConfigSpec(WhenSecret)

        @dataclass
        class Undeclared:
            plain: str = ""
        with self.assertRaises(ValueError):
            ConfigSpec(Undeclared)


class DecodeTest(unittest.TestCase):
    def setUp(self):
        self.spec = config_spec()

    def test_missing_config_is_declared_defaults(self):
        for values in (None, {}):
            cfg = self.spec.decode(values)
            self.assertEqual((cfg.search_provider, cfg.tavily_api_key.is_set(), cfg.fallback_to_yahoo),
                             ("yahoo", False, False))

    def test_values_override_defaults_and_null_or_unknown_keys_are_ignored(self):
        cfg = self.spec.decode({"searchProvider": "tavily", "tavilyApiKey": KEY, "fallbackToYahoo": True,
                                "somethingElse": {"x": 1}})
        self.assertEqual((cfg.search_provider, cfg.tavily_api_key.reveal(), cfg.fallback_to_yahoo),
                         ("tavily", KEY, True))
        cfg = self.spec.decode({"searchProvider": None, "fallbackToYahoo": None})
        self.assertEqual((cfg.search_provider, cfg.fallback_to_yahoo), ("yahoo", False))

    def test_protobuf_struct_is_decoded(self):
        from google.protobuf import struct_pb2
        struct = struct_pb2.Struct()
        struct.update({"searchProvider": "tavily", "tavilyApiKey": KEY, "fallbackToYahoo": True})
        self.assertEqual(struct_to_dict(struct)["fallbackToYahoo"], True)
        cfg = self.spec.decode(struct)
        self.assertEqual((cfg.search_provider, cfg.tavily_api_key.reveal()), ("tavily", KEY))

    def test_wrong_values_are_config_errors_naming_every_key(self):
        with self.assertRaises(ConfigError) as caught:
            self.spec.decode({"searchProvider": "bing", "fallbackToYahoo": "true", "tavilyApiKey": 7})
        message = str(caught.exception)
        self.assertEqual(caught.exception.type, "config")
        for key in ("searchProvider", "fallbackToYahoo", "tavilyApiKey"):
            self.assertIn(key, message)

    def test_required_when_is_enforced(self):
        for values in ({"searchProvider": "tavily"}, {"searchProvider": "tavily", "tavilyApiKey": ""}):
            with self.subTest(values=values):
                with self.assertRaisesRegex(ConfigError, "tavilyApiKey: required when searchProvider=tavily"):
                    self.spec.decode(values)
        # Not required for Yahoo.
        self.spec.decode({"searchProvider": "yahoo", "tavilyApiKey": ""})

    def test_errors_never_echo_a_secret_value(self):
        @dataclass(frozen=True)
        class Cfg:
            token: Secret = setting("token", label="Token", type="secret", required=True)
            mode: str = setting("mode", label="Mode", type="enum", enum=("a", "b"), default="a")
        spec = ConfigSpec(Cfg)
        for values in ({"token": {"v": KEY}, "mode": "z"}, {"token": [KEY]}):
            with self.assertRaises(ConfigError) as caught:
                spec.decode(values)
            self.assertNotIn(KEY, str(caught.exception))
        with self.assertRaisesRegex(ConfigError, "token: required"):
            spec.decode({})

    def test_int_and_url_fields(self):
        @dataclass(frozen=True)
        class Cfg:
            limit: int = setting("limit", label="Limit", type="int", default=5, min=1, max=10)
            endpoint: str = setting("endpoint", label="Endpoint", type="url", default="https://a.example")
        spec = ConfigSpec(Cfg)
        self.assertEqual(spec.decode({"limit": 7.0}).limit, 7)
        for values in ({"limit": 7.5}, {"limit": 11.0}, {"limit": True}, {"endpoint": "ftp://x"}):
            with self.subTest(values=values):
                with self.assertRaises(ConfigError):
                    spec.decode(values)

    def test_host_audience_fields_are_never_delivered(self):
        @dataclass(frozen=True)
        class Cfg:
            client: str = setting("clientId", label="Client ID", type="string", audience="host")
            mode: str = setting("mode", label="Mode", type="string", default="x")
        spec = ConfigSpec(Cfg)
        self.assertEqual(spec.decode({"clientId": "leak", "mode": "y"}), Cfg(client="", mode="y"))
        self.assertEqual(spec.schema()["fields"][0]["audience"], "host")


class CacheTest(unittest.TestCase):
    def setUp(self):
        self.spec = config_spec()
        self.decodes = 0
        original = self.spec.decode

        def counting(values):
            self.decodes += 1
            return original(values)
        self.spec.decode = counting

    def test_tenant_and_version_come_from_the_execution_context(self):
        a = self.spec.for_call(context("a", "1", {"searchProvider": "tavily", "tavilyApiKey": KEY}))
        b = self.spec.for_call(context("b", "1", {"searchProvider": "yahoo"}))
        self.assertEqual((a.search_provider, b.search_provider), ("tavily", "yahoo"))
        # A repeated (tenant, version) is served from cache, even if a caller
        # sent different values under the same version.
        self.assertIs(self.spec.for_call(context("a", "1", {})), a)
        self.assertEqual(self.decodes, 2)

    def test_empty_version_is_never_cached(self):
        for _ in range(3):
            self.spec.for_call(context("a", "", {"searchProvider": "yahoo"}))
        self.assertEqual(self.decodes, 3)
        self.assertEqual(len(self.spec._cache), 0)

    def test_new_version_replaces_the_tenants_old_one(self):
        self.spec.for_call(context("a", "1", {}))
        self.spec.for_call(context("b", "1", {}))
        cfg = self.spec.for_call(context("a", "2", {"fallbackToYahoo": True}))
        self.assertTrue(cfg.fallback_to_yahoo)
        self.assertEqual(sorted(self.spec._cache), [("a", "2"), ("b", "1")])

    def test_cache_is_bounded_lru(self):
        self.assertEqual(CACHE_SIZE, 256)
        for i in range(CACHE_SIZE):
            self.spec.for_call(context(f"t{i}", "v", {}))
        self.spec.for_call(context("t0", "v", {}))  # refresh t0
        self.spec.for_call(context("new", "v", {}))
        self.assertEqual(len(self.spec._cache), CACHE_SIZE)
        self.assertIn(("t0", "v"), self.spec._cache)
        self.assertNotIn(("t1", "v"), self.spec._cache)

    def test_invalid_config_is_not_cached(self):
        for _ in range(2):
            with self.assertRaises(ConfigError):
                self.spec.for_call(context("a", "1", {"searchProvider": "bing"}))
        self.assertEqual(self.decodes, 2)

    def test_concurrent_tenants_get_their_own_config(self):
        failures = []

        def worker(tenant, provider):
            values = {"searchProvider": provider, "tavilyApiKey": f"key-{tenant}"}
            for i in range(200):
                cfg = self.spec.for_call(context(tenant, str(i % 5), values))
                if cfg.search_provider != provider or cfg.tavily_api_key.reveal() != f"key-{tenant}":
                    failures.append(tenant)
        threads = [threading.Thread(target=worker, args=(f"t{i}", ("yahoo", "tavily")[i % 2])) for i in range(8)]
        for thread in threads:
            thread.start()
        for thread in threads:
            thread.join()
        self.assertEqual(failures, [])


class SecretTest(unittest.TestCase):
    def test_secret_never_renders(self):
        secret = Secret(KEY)
        cfg = BrowserConfig(search_provider="tavily", tavily_api_key=secret)
        rendered = [repr(secret), str(secret), f"{secret}", f"{secret!r}", "%s" % secret, "{}".format(secret),
                    repr(cfg), str(cfg), f"{cfg}"]
        for text in rendered:
            self.assertNotIn(KEY, text)
        self.assertEqual(secret.reveal(), KEY)
        self.assertTrue(secret.is_set())
        self.assertFalse(Secret().is_set())
        self.assertEqual(Secret("").reveal(), "")
        with self.assertRaises(TypeError):
            json.dumps({"k": secret})
        with self.assertRaises(TypeError):
            pickle.dumps(secret)
        with self.assertRaises(AttributeError):
            secret._value = "x"


class FakeResponse:
    def __init__(self, status=200):
        self.status = status

    def read(self, _limit=-1):
        return b"{}"

    def __enter__(self):
        return self

    def __exit__(self, *_):
        return False


def http_error(url, status):
    import email.message
    return urllib.error.HTTPError(url, status, "error", email.message.Message(), None)


YAHOO = "yahoo"


def yahoo_read(status=200, content="Yahoo results for weather", returncode=0):
    def run(command, **_):
        payload = {"url": command[2], "http_status": status, "content": content, "error": None}
        return subprocess.CompletedProcess(command, returncode, json.dumps(payload), "")
    return run


class ConfigCheckTest(unittest.TestCase):
    def runtime(self, responses):
        self.requests = []
        self.reads = []
        yahoo = responses.get(YAHOO, yahoo_read())

        def urlopen(request, timeout):
            self.requests.append(request)
            item = responses[request.full_url]
            if isinstance(item, BaseException):
                raise item
            return item

        def run(command, **kwargs):
            self.reads.append(command[2])
            if isinstance(yahoo, BaseException):
                raise yahoo
            return yahoo(command, **kwargs)
        return LightpandaRuntime(binary="/bin/true", run=run, urlopen=urlopen, sleep=lambda _: None)

    def check(self, values, responses):
        spec = config_spec(check=self.runtime(responses).check_config)
        result = spec.check(values)
        self.assertNotIn(KEY, json.dumps(result))
        return result

    def test_yahoo_is_checked_with_a_real_search(self):
        result = self.check(None, {})
        self.assertEqual(result["status"], "ok")
        self.assertEqual([r["id"] for r in result["checks"]], ["yahoo"])
        self.assertEqual(self.reads, ["https://search.yahoo.com/search?p=weather"])
        self.assertEqual(self.requests, [])
        for name, yahoo, expected in [
            ("server error", yahoo_read(status=500), "fail"),
            ("rate limited", yahoo_read(status=429, content="Too many requests, slow down. " * 50), "warn"),
            ("access challenge", yahoo_read(status=200, content="Our systems have detected unusual traffic from your network."), "fail"),
            ("empty page", yahoo_read(content="  "), "fail"),
            ("reader crashed", yahoo_read(returncode=1), "fail"),
            ("reader missing", FileNotFoundError("lightpanda"), "fail"),
        ]:
            with self.subTest(name):
                result = self.check({}, {YAHOO: yahoo})
                self.assertEqual(result["status"], expected, result)

    def test_tavily_key_is_validated_without_a_search(self):
        tavily = {"searchProvider": "tavily", "tavilyApiKey": KEY}
        result = self.check(tavily, {"https://api.tavily.com/usage": FakeResponse()})
        self.assertEqual((result["status"], [r["id"] for r in result["checks"]]), ("ok", ["tavily"]))
        self.assertEqual(self.requests[0].get_method(), "GET")
        self.assertEqual(self.requests[0].get_header("Authorization"), f"Bearer {KEY}")
        for status, expected in [(401, "fail"), (403, "fail"), (429, "warn"), (432, "warn"), (500, "fail")]:
            with self.subTest(status=status):
                result = self.check(tavily, {"https://api.tavily.com/usage": http_error("https://api.tavily.com/usage", status)})
                self.assertEqual(result["status"], expected)

    def test_tavily_outage_with_yahoo_fallback_is_a_warning_but_a_rejected_key_fails(self):
        values = {"searchProvider": "tavily", "tavilyApiKey": KEY, "fallbackToYahoo": True}
        down = {"https://api.tavily.com/usage": urllib.error.URLError("down")}
        result = self.check(values, down)
        self.assertEqual((result["status"], [r["id"] for r in result["checks"]]), ("warn", ["tavily", "yahoo"]))
        rejected = {"https://api.tavily.com/usage": http_error("https://api.tavily.com/usage", 401)}
        self.assertEqual(self.check(values, rejected)["status"], "fail")

    def test_invalid_values_are_a_failed_check(self):
        result = self.check({"searchProvider": "tavily"}, {})
        self.assertEqual(result["status"], "fail")
        self.assertEqual(result["checks"][0]["id"], "config")
        self.assertEqual(self.requests, [])

    def test_a_crashing_check_is_a_failed_check(self):
        spec = config_spec(check=lambda cfg: 1 / 0)
        self.assertEqual(spec.check(None)["status"], "fail")
        self.assertIsNone(config_spec().check(None))
        self.assertEqual(check_result([{"id": "a", "label": "A", "status": "warn"}])["status"], "warn")


class HealthTest(unittest.TestCase):
    """Health over the real generated protobuf messages."""

    @classmethod
    def setUpClass(cls):
        try:
            import grpc_tools  # noqa: F401
        except ImportError:
            raise unittest.SkipTest("grpcio-tools is not installed")
        cls.tmp = tempfile.TemporaryDirectory()
        subprocess.run([sys.executable, "-m", "grpc_tools.protoc", f"-I{HERE}", f"--python_out={cls.tmp.name}",
                        os.path.join(HERE, "skill.proto")], check=True)
        sys.path.insert(0, cls.tmp.name)
        sys.modules.pop("skill_pb2", None)
        cls.pb = importlib.import_module("skill_pb2")

    @classmethod
    def tearDownClass(cls):
        sys.path.remove(cls.tmp.name)
        sys.modules.pop("skill_pb2", None)
        cls.tmp.cleanup()

    def test_proto_has_the_sdk_config_fields(self):
        context = self.pb.ExecutionContext.DESCRIPTOR.fields_by_name
        self.assertEqual((context["tenant_id"].number, context["config"].number, context["config_version"].number),
                         (5, 6, 7))
        request = self.pb.HealthRequest.DESCRIPTOR.fields_by_name
        self.assertEqual((request["config_check"].number, request["config_tenant_id"].number,
                          request["config_values"].number), (1, 2, 3))

    def test_execution_context_struct_is_decoded(self):
        ctx = self.pb.ExecutionContext(tenant_id="t1", config_version="v1")
        ctx.config.update({"searchProvider": "tavily", "tavilyApiKey": KEY, "fallbackToYahoo": True})
        cfg = config_spec().for_call(ctx)
        self.assertEqual((cfg.search_provider, cfg.tavily_api_key.reveal(), cfg.fallback_to_yahoo),
                         ("tavily", KEY, True))
        self.assertEqual(config_spec().for_call(self.pb.ExecutionContext(tenant_id="t1")).search_provider, "yahoo")

    def test_health_reports_schema_and_runs_the_check_on_request(self):
        spec = config_spec(check=lambda cfg: check_result([
            {"id": "provider", "label": "Provider", "status": "ok", "detail": cfg.search_provider}]))
        status = spec.health_status(self.pb, self.pb.HealthRequest())
        self.assertEqual(json.loads(status.schema), spec.schema())
        self.assertTrue(status.has_config_check)
        self.assertFalse(status.HasField("config_check"))

        request = self.pb.HealthRequest(config_check=True, config_tenant_id="t1")
        request.config_values.update({"searchProvider": "tavily", "tavilyApiKey": KEY})
        status = spec.health_status(self.pb, request)
        self.assertEqual(status.config_check.status, "ok")
        self.assertEqual([(c.id, c.status, c.detail) for c in status.config_check.checks], [("provider", "ok", "tavily")])


if __name__ == "__main__":
    unittest.main()
