"""Upstream proxies for Lightpanda page reads, set in the Skill's operator config.

The ``proxies`` setting is a list of proxy URLs (``http://user:pass@host:port``)
or provider lines (``host:port:user:pass``), separated by newlines or commas:
the same format and tenant pinning as the live browser. Each tenant prefers
one entry by rendezvous hash of (tenant, entry), so sites see a stable IP and
changing the list only moves the tenants of entries that were added or removed.
When the preferred proxy does not open a tunnel, the tenant uses the next one
in its own order. Lightpanda speaks only HTTP(S) proxies; SOCKS entries are
skipped.
"""

from __future__ import annotations

import base64
import hashlib
import re
import socket
import ssl
import threading
import time
from urllib.parse import quote, unquote, urlsplit

PROBE_TARGET = "example.com:443"
PROBE_TIMEOUT_SECONDS = 5
# A tenant's reachable proxy is re-checked after this long, so a recovered
# preferred proxy takes the tenant back.
CHOICE_TTL_SECONDS = 60
_SCHEME = re.compile(r"^[a-z][a-z0-9+.-]*://", re.IGNORECASE)


class ProxyListError(ValueError):
    """An entry of the proxy list is invalid. Never includes the entry."""


class Proxy:
    __slots__ = ("scheme", "host", "port", "username", "password")

    def __init__(self, scheme, host, port, username=None, password=None):
        self.scheme, self.host, self.port = scheme, host, port
        self.username, self.password = username, password

    @property
    def server(self):
        return f"{self.scheme}://{self.host}:{self.port}"

    def url(self):
        """The proxy URL with credentials, for Lightpanda's --http-proxy."""
        if self.username is None:
            return self.server
        auth = f"{quote(self.username, safe='')}:{quote(self.password or '', safe='')}"
        return f"{self.scheme}://{auth}@{self.host}:{self.port}"

    def __eq__(self, other):
        return isinstance(other, Proxy) and self.url() == other.url()

    def __hash__(self):
        return hash(self.url())

    def __repr__(self):
        # Never render the credentials.
        return f"Proxy({self.server!r})"


def parse_proxy(entry):
    if _SCHEME.match(entry):
        try:
            parts = urlsplit(entry)
            port = parts.port
        except ValueError:
            raise ProxyListError("Browser proxy list entry is invalid") from None
        if not parts.hostname:
            raise ProxyListError("Browser proxy list entry is invalid")
        scheme = parts.scheme.lower()
        port = port or (443 if scheme == "https" else 80)
        username = unquote(parts.username) if parts.username else None
        password = unquote(parts.password or "") if parts.username else None
        return Proxy(scheme, parts.hostname, port, username, password)
    host, _, rest = entry.partition(":")
    port, _, credentials = rest.partition(":")
    if not host or not re.fullmatch(r"[0-9]{1,5}", port):
        raise ProxyListError("Browser proxy list entry is invalid")
    username, _, password = credentials.partition(":")
    return Proxy("http", host, int(port), username or None, password if username else None)


def proxy_entries(value):
    return [entry.strip() for entry in re.split(r"[\n,]", value or "") if entry.strip()]


def tenant_proxies(value, tenant):
    """The tenant's usable (HTTP/HTTPS) proxies, most preferred first."""
    tenant = (tenant or "").strip()

    def score(entry):
        return int.from_bytes(hashlib.sha256(f"{tenant}\n{entry}".encode("utf-8")).digest()[:8], "big")

    ordered = sorted(proxy_entries(value), key=score, reverse=True)
    return [proxy for proxy in map(parse_proxy, ordered) if proxy.scheme in ("http", "https")]


def probe_proxy(proxy, timeout=PROBE_TIMEOUT_SECONDS):
    """True when the proxy opens an (authenticated) CONNECT tunnel in time."""
    auth = ""
    if proxy.username is not None:
        token = base64.b64encode(f"{proxy.username}:{proxy.password or ''}".encode("utf-8")).decode("ascii")
        auth = f"Proxy-Authorization: Basic {token}\r\n"
    try:
        with socket.create_connection((proxy.host, proxy.port), timeout=timeout) as raw:
            sock = ssl.create_default_context().wrap_socket(raw, server_hostname=proxy.host) \
                if proxy.scheme == "https" else raw
            sock.settimeout(timeout)
            sock.sendall(f"CONNECT {PROBE_TARGET} HTTP/1.1\r\nHost: {PROBE_TARGET}\r\n{auth}\r\n".encode("latin-1"))
            reply = b""
            while b"\r\n" not in reply and len(reply) <= 1024:
                chunk = sock.recv(1024)
                if not chunk:
                    return False
                reply += chunk
            return re.match(rb"HTTP/1\.[01] 200 ", reply) is not None
    except (OSError, ValueError):
        return False


class ProxyPool:
    """Chooses each tenant's reachable proxy, caching the choice briefly."""

    def __init__(self, probe=None, clock=None, ttl=CHOICE_TTL_SECONDS):
        self._probe = probe or probe_proxy
        self._clock = clock or time.monotonic
        self._ttl = ttl
        self._lock = threading.Lock()
        self._choices = {}  # (tenant, list digest) -> (proxy, expires)

    def choose(self, value, tenant):
        """The tenant's proxy, None without a list; ProxyUnreachable when none answers."""
        proxies = tenant_proxies(value, tenant)
        if not proxies:
            return None
        key = (tenant or "", hashlib.sha256((value or "").encode("utf-8")).hexdigest())
        now = self._clock()
        with self._lock:
            cached = self._choices.get(key)
            if cached and cached[1] > now:
                return cached[0]
        for proxy in proxies:
            if self._probe(proxy):
                with self._lock:
                    for stale in [k for k in self._choices if k[0] == key[0] and k != key]:
                        del self._choices[stale]
                    self._choices[key] = (proxy, now + self._ttl)
                return proxy
        raise ProxyUnreachable()

    def check(self, value):
        """Config-check row for the proxy list (None without a list)."""
        row = {"id": "proxies", "label": "Proxies"}
        try:
            proxies = tenant_proxies(value, "")
        except ProxyListError as exc:
            return {**row, "status": "fail", "detail": str(exc)}
        if not proxy_entries(value):
            return None
        if not proxies:
            return {**row, "status": "fail", "detail": "No HTTP(S) proxy is listed; Lightpanda cannot use SOCKS proxies."}
        reachable = sum(1 for proxy in proxies if self._probe(proxy))
        total = len(proxies)
        if reachable == 0:
            return {**row, "status": "fail", "detail": f"None of the {total} proxies opened a tunnel."}
        if reachable < total:
            return {**row, "status": "warn", "detail": f"{reachable} of {total} proxies opened a tunnel."}
        return {**row, "status": "ok", "detail": f"All {total} proxies opened a tunnel."}


class ProxyUnreachable(Exception):
    pass
