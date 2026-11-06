"""Operator-configured proxies: parsing, tenant pinning, failover, and the --http-proxy flag."""

import hashlib
import json
import socket
import threading
import unittest
from unittest import mock

from proxy_pool import ProxyListError, ProxyPool, parse_proxy, probe_proxy, tenant_proxies
from runtime import BrowserActionFailure, BrowserConfig, LightpandaRuntime
from server import secret_values
from skill_config import Secret

POOL = "31.59.20.176:6754:u1:p1,http://u2:p2@64.137.96.74:6641,198.23.239.134:6540:u3:p3,191.96.254.138:6185"


def config(proxies):
    return BrowserConfig(proxies=Secret(proxies))


class ProxyListTest(unittest.TestCase):
    def test_entries_parse_from_provider_lines_and_urls(self):
        proxy = parse_proxy("31.59.20.176:6754:hsk:p:ss")
        self.assertEqual((proxy.server, proxy.username, proxy.password), ("http://31.59.20.176:6754", "hsk", "p:ss"))
        proxy = parse_proxy("http://u%40x:pw@64.137.96.74:6641")
        self.assertEqual(proxy.url(), "http://u%40x:pw@64.137.96.74:6641")
        self.assertEqual(parse_proxy("proxy.example:8080").url(), "http://proxy.example:8080")
        with self.assertRaisesRegex(ProxyListError, "invalid"):
            parse_proxy("not-a-proxy")
        self.assertNotIn("p:ss", repr(parse_proxy("31.59.20.176:6754:hsk:p:ss")))

    def test_socks_entries_are_skipped_and_an_empty_list_connects_directly(self):
        self.assertEqual(tenant_proxies("socks5://proxy:1080", "42"), [])
        self.assertEqual(tenant_proxies(" \n , ", "42"), [])
        self.assertIsNone(ProxyPool(probe=lambda _p: self.fail("must not probe")).choose("", "42"))

    def test_tenant_order_matches_the_live_browser_rendezvous_hash(self):
        entries = POOL.split(",")
        score = lambda entry: int.from_bytes(hashlib.sha256(f"42\n{entry}".encode()).digest()[:8], "big")
        expected = [parse_proxy(e) for e in sorted(entries, key=score, reverse=True)]
        self.assertEqual(tenant_proxies(POOL, "42"), expected)

    def test_a_tenant_keeps_its_proxy_when_other_entries_change(self):
        entries = POOL.split(",")
        for tenant in range(1, 40):
            chosen = tenant_proxies(POOL, str(tenant))[0]
            others = [e for e in entries if parse_proxy(e) != chosen]
            fewer = ",".join(e for e in entries if e != others[0])
            self.assertEqual(tenant_proxies(fewer, str(tenant))[0], chosen)


class ProxyPoolTest(unittest.TestCase):
    def test_unreachable_preferred_proxy_moves_to_the_next_and_back_after_the_ttl(self):
        now = [0.0]
        down = set()
        pool = ProxyPool(probe=lambda proxy: proxy not in down, clock=lambda: now[0], ttl=60)
        first, second = tenant_proxies(POOL, "42")[:2]
        self.assertEqual(pool.choose(POOL, "42"), first)
        down.add(first)
        self.assertEqual(pool.choose(POOL, "42"), first, "the choice is cached")
        now[0] = 61
        self.assertEqual(pool.choose(POOL, "42"), second)
        down.clear()
        now[0] = 122
        self.assertEqual(pool.choose(POOL, "42"), first)

    def test_no_reachable_proxy_fails_the_read_clearly(self):
        runtime = LightpandaRuntime(run=lambda *a, **k: self.fail("must not read"),
                                    proxies=ProxyPool(probe=lambda _p: False))
        with self.assertRaises(BrowserActionFailure) as caught:
            runtime.execute("lightpanda-fetch", {"url": "https://example.com"}, config(POOL), tenant="42")
        self.assertEqual(caught.exception.code, "source_unavailable")
        self.assertIn("No browser proxy is reachable", str(caught.exception))

    def test_an_invalid_list_fails_without_echoing_the_entry(self):
        runtime = LightpandaRuntime(proxies=ProxyPool(probe=lambda _p: True))
        with self.assertRaises(BrowserActionFailure) as caught:
            runtime.execute("lightpanda-fetch", {"url": "https://example.com"}, config("secret-entry"), tenant="42")
        self.assertNotIn("secret-entry", str(caught.exception))

    def test_config_check_reports_reachable_proxies(self):
        entries = POOL.split(",")
        pool = ProxyPool(probe=lambda proxy: proxy != parse_proxy(entries[0]))
        self.assertIsNone(pool.check(""))
        self.assertEqual(pool.check(POOL)["status"], "warn")
        self.assertEqual(ProxyPool(probe=lambda _p: True).check(POOL)["status"], "ok")
        self.assertEqual(ProxyPool(probe=lambda _p: False).check(POOL)["status"], "fail")
        self.assertEqual(ProxyPool(probe=lambda _p: True).check("socks5://p:1")["status"], "fail")
        runtime = LightpandaRuntime(run=lambda *a, **k: self.fail("must not read"),
                                    proxies=ProxyPool(probe=lambda _p: False))
        result = runtime.check_config(config(POOL))
        self.assertEqual((result["status"], [row["id"] for row in result["checks"]]), ("fail", ["proxies"]))


class LightpandaProxyFlagTest(unittest.TestCase):
    def test_every_read_uses_the_tenants_proxy_and_none_without_a_list(self):
        commands = []

        def run(command, **_kwargs):
            commands.append(command)
            return mock.Mock(returncode=0, stdout=json.dumps({"http_status": 200, "content": "# Page\n\nHello"}), stderr="")

        runtime = LightpandaRuntime(run=run, proxies=ProxyPool(probe=lambda _p: True))
        runtime.execute("lightpanda-fetch", {"url": "https://example.com"}, config(POOL), tenant="42")
        runtime.execute("lightpanda-read-many", {"reads": [{"kind": "url", "value": "https://example.com/a"},
                                                           {"kind": "query", "value": "tides"}]}, config(POOL), tenant="42")
        runtime.execute("lightpanda-fetch", {"url": "https://example.com"}, config(""), tenant="42")
        expected = tenant_proxies(POOL, "42")[0].url()
        for command in commands[:3]:
            self.assertEqual(command[command.index("--http-proxy") + 1], expected)
        self.assertNotIn("--http-proxy", commands[3])

    def test_proxy_entries_and_passwords_are_redacted_from_errors(self):
        values = secret_values(config("31.59.20.176:6754:user:hunter22"))
        self.assertIn("31.59.20.176:6754:user:hunter22", values)
        self.assertIn("hunter22", values)


class ProbeTest(unittest.TestCase):
    def fake_proxy(self, reply):
        server = socket.socket()
        server.bind(("127.0.0.1", 0))
        server.listen(1)
        seen = {}

        def serve():
            conn, _ = server.accept()
            with conn:
                seen["request"] = conn.recv(4096).decode("latin-1")
                if reply:
                    conn.sendall(reply)
                else:
                    conn.recv(1)
            server.close()

        threading.Thread(target=serve, daemon=True).start()
        return server.getsockname()[1], seen

    def test_probe_needs_an_authenticated_tunnel_in_time(self):
        port, seen = self.fake_proxy(b"HTTP/1.1 200 Connection established\r\n\r\n")
        self.assertTrue(probe_proxy(parse_proxy(f"127.0.0.1:{port}:u:p:w")))
        self.assertIn("Proxy-Authorization: Basic dTpwOnc=\r\n", seen["request"])
        port, _ = self.fake_proxy(b"HTTP/1.1 407 Proxy Authentication Required\r\n\r\n")
        self.assertFalse(probe_proxy(parse_proxy(f"127.0.0.1:{port}")))
        port, _ = self.fake_proxy(b"")
        self.assertFalse(probe_proxy(parse_proxy(f"127.0.0.1:{port}"), timeout=0.2))
        self.assertFalse(probe_proxy(parse_proxy("127.0.0.1:1")))


if __name__ == "__main__":
    unittest.main()
