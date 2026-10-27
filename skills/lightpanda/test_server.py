"""Transport contract tests without downloading browser or gRPC dependencies."""

import importlib.util
import json
from pathlib import Path
import sys
from types import SimpleNamespace
import unittest
from unittest import mock

from runtime import BrowserActionFailure, source_http_failure


class Message:
    def __init__(self, **fields):
        self.__dict__.update(fields)


def load_server():
    spec = importlib.util.spec_from_file_location("browser_server_under_test", Path(__file__).with_name("server.py"))
    module = importlib.util.module_from_spec(spec)
    # Only the protobuf construction boundary is substituted. Execute and
    # encode_output are the actual production implementation; imports restore
    # immediately so these fixtures cannot affect other tests.
    modules = {
        "grpc": SimpleNamespace(),
        "skill_pb2": SimpleNamespace(ExecuteResponse=Message, Error=Message),
        "skill_pb2_grpc": SimpleNamespace(SkillServiceServicer=object),
    }
    with mock.patch.dict(sys.modules, modules):
        spec.loader.exec_module(module)
    return module


class BrowserTransportTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.server = load_server()

    def execute(self, *, result=None, error=None, bindings=None, config=None):
        service = self.server.SkillService.__new__(self.server.SkillService)
        service.config = self.server.config_spec()
        service.runtime = mock.Mock()
        service.runtime.execute.return_value = result
        service.runtime.execute.side_effect = error
        request = SimpleNamespace(
            node_type="lightpanda-fetch", config={}, bindings=bindings or {},
            context=SimpleNamespace(run_id="run", agent_id="agent", namespace="tenant",
                                    tenant_id="tenant", config_version="", config=config),
        )
        self.service = service
        return service.Execute(request, mock.Mock())

    def test_rate_limit_is_error_not_success_output(self):
        response = self.execute(error=source_http_failure(429, 45))
        self.assertFalse(hasattr(response, "output"))
        self.assertEqual(response.error.type, "source_rate_limited")
        self.assertEqual(response.error.details, {
            "failureKind": "source_rate_limited", "httpStatus": "429",
            "retryAfterSeconds": "45", "retryable": "false",
        })
        self.assertIn("HTTP 429", response.error.message)
        self.assertIn("45 seconds", response.error.message)
        self.assertNotIn("live browser", response.error.message)

    def test_mixed_batch_keeps_explicit_partial_result(self):
        result = {"status": "partial", "succeededCount": 1, "failedCount": 1, "results": [
            {"kind": "url", "input": "https://example.com/a", "status": "succeeded", "text": "actual evidence"},
            {"kind": "url", "input": "https://example.com/b", "status": "failed", "httpStatus": 429,
             "failureKind": "source_rate_limited", "error": "This source returned HTTP 429 (rate limited). Try again later."},
        ]}
        response = self.execute(result=result)
        self.assertFalse(hasattr(response, "error"))
        self.assertEqual({k: json.loads(v) for k, v in response.output.items()}, result)

    def test_all_failed_batch_keeps_safe_indexed_causes_in_error(self):
        failure = BrowserActionFailure("source_reads_failed", "None of the requested pages could be read.", failures=[
            {"index": 0, "failureKind": "source_rate_limited", "httpStatus": 429},
            {"index": 1, "failureKind": "source_http_error", "httpStatus": 403},
        ])
        response = self.execute(error=failure)
        self.assertFalse(hasattr(response, "output"))
        self.assertEqual(response.error.type, "source_reads_failed")
        self.assertEqual(json.loads(response.error.details["failures"]), failure.failures)

    def test_ordinary_binding_error_redaction_is_preserved(self):
        response = self.execute(error=ValueError("invalid binding private-value"), bindings={"password": b'"private-value"'})
        self.assertEqual(response.error.type, "validation")
        self.assertEqual(response.error.message, "invalid binding [REDACTED]")

    def test_operator_config_reaches_the_runtime_from_the_execution_context(self):
        self.execute(result={"ok": True}, config={"searchProvider": "tavily", "tavilyApiKey": "tvly-abc",
                                                   "fallbackToYahoo": True, "unknownKey": 1})
        cfg = self.service.runtime.execute.call_args.args[2]
        self.assertEqual((cfg.search_provider, cfg.tavily_api_key.reveal(), cfg.fallback_to_yahoo),
                         ("tavily", "tvly-abc", True))
        self.execute(result={"ok": True})
        cfg = self.service.runtime.execute.call_args.args[2]
        self.assertEqual((cfg.search_provider, cfg.tavily_api_key.is_set(), cfg.fallback_to_yahoo),
                         ("yahoo", False, False))

    def test_invalid_operator_config_fails_the_call_with_config_error_type(self):
        for config, needle in [({"searchProvider": "bing"}, "searchProvider"),
                               ({"fallbackToYahoo": "yes"}, "fallbackToYahoo"),
                               ({"tavilyApiKey": 42}, "tavilyApiKey"),
                               ({"searchProvider": "tavily", "tavilyApiKey": ""}, "tavilyApiKey")]:
            with self.subTest(config=config):
                response = self.execute(result={"ok": True}, config=config)
                self.assertEqual(response.error.type, "config")
                self.assertIn(needle, response.error.message)
                self.service.runtime.execute.assert_not_called()

    def test_config_error_never_echoes_a_secret(self):
        response = self.execute(result={"ok": True}, config={"searchProvider": "nope", "tavilyApiKey": ["tvly-leak"]})
        self.assertEqual(response.error.type, "config")
        self.assertNotIn("tvly-leak", response.error.message)

    def test_catalog_and_health_cover_only_lightpanda(self):
        self.assertEqual(self.server.ACTIONS, ["lightpanda-fetch", "lightpanda-search", "lightpanda-read-many"])
        schemas = self.server.load_action_schemas()
        self.assertEqual(sorted(schemas), sorted(self.server.ACTIONS))


if __name__ == "__main__":
    unittest.main()
