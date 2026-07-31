"""Governed Camoufox browser runtime.

Stdlib-only at import time so unit tests run without Camoufox, Playwright, or
grpc installed. The real engine is imported lazily inside the default browser
factory, which always executes on a dedicated worker thread because the
Playwright sync API is bound to the thread that created it.
"""

from __future__ import annotations

import base64
import fcntl
import hashlib
import json
import os
import queue
import re
import shutil
import sqlite3
import threading
from datetime import datetime, timedelta, timezone
from urllib.parse import urljoin, urlparse

ID = re.compile(r"^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$")
SECRET_CONTROL = re.compile(
    r"(password|passcode|secret|token|api.?key|authorization|cookie|private.?key)", re.I
)
USERNAME_CONTROL = re.compile(r"\b(user(?:name)?|email|login|account)\b", re.I)
CHALLENGES = {
    "captcha": re.compile(r"\b(captcha|recaptcha|hcaptcha|verify you are human)\b", re.I),
    "mfa": re.compile(r"\b(multi[ -]?factor|two[ -]?factor|2fa|authenticator|verification code)\b", re.I),
    "anti_bot": re.compile(r"\b(access denied|unusual traffic|bot detection|security check|cloudflare|js_challenge)\b", re.I),
}

MODES = ("owned-assessment", "permitted-automation")
OS_VALUES = {"windows", "macos", "linux"}
PROFILE_KEYS = {"os", "geoip", "humanize", "seed", "window", "block_webrtc", "block_images"}
PROXY_SCHEMES = ("http", "https", "socks5")
DEFAULT_KEYS = {
    "targetId": "targets",
    "profileId": "profiles",
    "proxyPoolId": "proxy_pools",
}
MAX_ELEMENTS = 180
MAX_TEXT = 48 * 1024
MAX_SCREENSHOT = 5 * 1024 * 1024
MAX_MODEL_SCREENSHOT = 1 * 1024 * 1024
VERSION = "2.0.26"
COMMIT_OBSERVATION_ATTEMPTS = 4
# A lease spans model planning as well as browser I/O. Hosted model turns can
# legitimately take several minutes, so the default must not reclaim a live
# Run's browser while that Run is still deciding its next bounded action.
DEFAULT_PROFILE_LEASE_TTL_SECONDS = 900
WORKER_TIMEOUT_SECONDS = {
    "launch": 45,
    "navigate": 40,
    "snapshot": 20,
    "click": 20,
    "fill": 10,
    "select": 10,
    "scroll": 10,
    "screenshot": 30,
    "close": 10,
}


class BrowserOperationTimeout(TimeoutError):
    """A browser-engine call exceeded its local action boundary."""

SNAPSHOT_JS = r"""
() => {
  const sel = 'a,button,input,select,textarea,summary,[role="button"],[role="link"],[role="checkbox"],[role="radio"],[role="textbox"],[role="combobox"],[role="tab"],[role="menuitem"],[contenteditable="true"]';
  const candidates = [];
  const visit = (root) => {
    for (const element of root.querySelectorAll('*')) {
      if (element.matches(sel)) candidates.push(element);
      if (element.shadowRoot) visit(element.shadowRoot);
    }
  };
  visit(document);
  const visible = (e) => {
    const r = e.getBoundingClientRect();
    const s = getComputedStyle(e);
    return r.width > 0 && r.height > 0 && s.visibility !== 'hidden' && s.display !== 'none';
  };
  const inViewport = (e) => {
    const r = e.getBoundingClientRect();
    return r.bottom > 0 && r.right > 0 && r.top < innerHeight && r.left < innerWidth;
  };
  const viewportDistance = (e) => {
    const r = e.getBoundingClientRect();
    const dx = r.right < 0 ? -r.right : (r.left > innerWidth ? r.left - innerWidth : 0);
    const dy = r.bottom < 0 ? -r.bottom : (r.top > innerHeight ? r.top - innerHeight : 0);
    return Math.hypot(dx, dy);
  };
  const focused = document.activeElement;
  const focusedForm = focused?.closest?.('form');
  const focusAffinity = (e) => {
    if (!focused || focused === document.body) return 2;
    if (e === focused) return 0;
    if (focusedForm && focusedForm.contains(e)) return 0;
    const target = focused.getBoundingClientRect();
    const candidate = e.getBoundingClientRect();
    const dx = Math.max(0, target.left - candidate.right, candidate.left - target.right);
    const dy = Math.max(0, target.top - candidate.bottom, candidate.top - target.bottom);
    return Math.hypot(dx, dy) <= Math.max(innerWidth, innerHeight) ? 1 : 2;
  };
  const labelledBy = (e) => (e.getAttribute('aria-labelledby') || '').split(/\s+/)
    .map((id) => e.getRootNode().getElementById?.(id)?.innerText || '').filter(Boolean).join(' ');
  const name = (e) => (e.getAttribute('aria-label') || labelledBy(e) || e.innerText ||
    e.getAttribute('alt') || e.getAttribute('title') || e.getAttribute('placeholder') ||
    e.getAttribute('name') || e.getAttribute('autocomplete') || e.getAttribute('type') || '')
    .replace(/\s+/g, ' ').trim().slice(0, 240);
  const role = (e) => e.getAttribute('role') || ({A: 'link', BUTTON: 'button', TEXTAREA: 'textbox',
    SELECT: 'combobox', SUMMARY: 'button'}[e.tagName]) ||
    (e.tagName === 'INPUT' ? ({checkbox: 'checkbox', radio: 'radio'}[e.type] || 'textbox') :
      (e.isContentEditable ? 'textbox' : e.tagName.toLowerCase()));
  const landmark = (e) => {
    const parent = e.closest('dialog,[role="dialog"],form,article,nav,main,aside,header,footer,section');
    if (!parent) return '';
    const kind = parent.getAttribute('role') || parent.tagName.toLowerCase();
    const label = name(parent);
    return label ? `${kind}: ${label}`.slice(0, 240) : kind;
  };
  for (const e of (globalThis.__opensealCamoufoxRefs || [])) {
    e.removeAttribute?.('data-camoufox-ref');
  }
  // Keep the focused field's form and nearby actions first, then current-
  // viewport controls, then the nearest visible controls outside it. Forms
  // commonly place their submit button immediately below a textarea; keeping
  // that interaction neighborhood together makes the compact observation
  // usable even when the full page exceeds the durable result envelope. The
  // original DOM order breaks equal-distance ties and keeps refs deterministic.
  const els = [...new Set(candidates)]
    .map((element, index) => ({element, index}))
    .filter(({element}) => visible(element))
    .sort((left, right) =>
      focusAffinity(left.element) - focusAffinity(right.element) ||
      viewportDistance(left.element) - viewportDistance(right.element) || left.index - right.index)
    .slice(0, %d)
    .map(({element}) => element);
  globalThis.__opensealCamoufoxRefs = els;
  els.forEach((e, i) => e.setAttribute('data-camoufox-ref', String(i + 1)));
  const lines = [];
  const seen = new Set();
  let textLength = 0;
  const collectText = (root) => {
    const walker = document.createTreeWalker(root, NodeFilter.SHOW_ELEMENT | NodeFilter.SHOW_TEXT);
    while (walker.nextNode() && textLength < %d) {
      const node = walker.currentNode;
      if (node.nodeType === Node.ELEMENT_NODE && node.shadowRoot) collectText(node.shadowRoot);
      if (node.nodeType !== Node.TEXT_NODE) continue;
      const parent = node.parentElement;
      if (!parent || !visible(parent) || !inViewport(parent)) continue;
      const line = (node.textContent || '').replace(/\s+/g, ' ').trim();
      if (!line || seen.has(line)) continue;
      seen.add(line);
      lines.push(line);
      textLength += line.length + 1;
    }
  };
  collectText(document.body || document.documentElement);
  return {
    url: location.href,
    title: document.title,
    text: lines.join('\n').slice(0, %d),
    elements: els.map((e, i) => {
      const r = e.getBoundingClientRect();
      const state = {};
      for (const key of ['disabled', 'checked', 'selected', 'required', 'readOnly']) {
        if (key in e && e[key] === true) state[key === 'readOnly' ? 'readonly' : key] = true;
      }
      for (const attr of ['aria-expanded', 'aria-pressed', 'aria-current', 'autocomplete', 'type']) {
        const value = e.getAttribute(attr);
        if (value) state[attr.replace('aria-', '')] = value;
      }
      const elementRole = role(e);
      const inputType = String(e.getAttribute('type') || '').toLowerCase();
      const editable = e.tagName === 'TEXTAREA' || e.tagName === 'SELECT' || e.isContentEditable ||
        elementRole === 'textbox' || elementRole === 'searchbox' || elementRole === 'combobox' ||
        (e.tagName === 'INPUT' && !['button','checkbox','file','hidden','image','radio','reset','submit'].includes(inputType));
      if (editable) {
        const current = e.isContentEditable ? e.textContent : e.value;
        state.filled = String(current || '').length > 0;
      }
      const href = e.tagName === 'A' && e.href ? e.href : '';
      return {ref: i + 1, role: elementRole, name: name(e), context: landmark(e), href, inViewport: inViewport(e),
        bounds: {x: Math.round(r.x), y: Math.round(r.y), width: Math.round(r.width), height: Math.round(r.height)}, state};
    }),
  };
}
""" % (MAX_ELEMENTS, MAX_TEXT, MAX_TEXT)

SETTLE_JS = r"""
() => new Promise((resolve) => {
  let done = false;
  let quietTimer;
  let hardTimer;
  let observer;
  const finish = () => {
    if (done) return;
    done = true;
    if (observer) observer.disconnect();
    clearTimeout(quietTimer);
    clearTimeout(hardTimer);
    const fallback = setTimeout(resolve, 100);
    requestAnimationFrame(() => requestAnimationFrame(() => {
      clearTimeout(fallback);
      resolve();
    }));
  };
  if (!document.documentElement) return finish();
  quietTimer = setTimeout(finish, 180);
  hardTimer = setTimeout(finish, 1400);
  observer = new MutationObserver(() => {
    clearTimeout(quietTimer);
    quietTimer = setTimeout(finish, 180);
  });
  observer.observe(document.documentElement, {subtree: true, childList: true, attributes: true, characterData: true});
})
"""


def _configured_map(env, name, required=True):
    raw = (env.get(name) or "").strip()
    if not raw:
        if required:
            raise ValueError(f"{name} is required")
        return {}
    try:
        value = json.loads(raw)
    except json.JSONDecodeError:
        raise ValueError(f"{name} is invalid") from None
    if not isinstance(value, dict):
        raise ValueError(f"{name} must be an object")
    return value


def _validate_profile(pid, profile):
    if not ID.match(pid) or not isinstance(profile, dict):
        raise ValueError(f"profile {pid} is invalid")
    unknown = set(profile) - PROFILE_KEYS - {"assessmentOnly"}
    if unknown:
        raise ValueError(f"profile {pid} sets unsupported keys: {sorted(unknown)}")
    os_value = profile.get("os")
    if isinstance(os_value, str):
        os_value = [os_value]
    if os_value is not None and (
        not isinstance(os_value, list)
        or not os_value
        or any(v not in OS_VALUES for v in os_value)
    ):
        raise ValueError(f"profile {pid} os must be one or more of {sorted(OS_VALUES)}")
    if profile.get("seed") is not None and (not isinstance(profile["seed"], int) or profile["seed"] < 0):
        raise ValueError(f"profile {pid} seed is invalid")
    window = profile.get("window")
    if window is not None and (
        not isinstance(window, list) or len(window) != 2 or any(not isinstance(v, int) or v <= 0 for v in window)
    ):
        raise ValueError(f"profile {pid} window must be [width, height]")


def proxy_urls(proxy):
    if isinstance(proxy.get("urls"), list):
        return proxy["urls"]
    if proxy.get("url"):
        return [proxy["url"]]
    return []


def load_inventory(env=None):
    env = env if env is not None else os.environ
    required = ("CAMOUFOX_TARGETS", "CAMOUFOX_PROFILES", "CAMOUFOX_PROXY_POOLS")
    if all(not (env.get(name) or "").strip() for name in required):
        return None
    inventory = {
        "targets": _configured_map(env, "CAMOUFOX_TARGETS"),
        "profiles": _configured_map(env, "CAMOUFOX_PROFILES"),
        "proxy_pools": _configured_map(env, "CAMOUFOX_PROXY_POOLS"),
        "defaults": _configured_map(env, "CAMOUFOX_DEFAULTS", required=False),
    }
    for tid, target in inventory["targets"].items():
        parsed = urlparse(target.get("baseUrl", ""))
        if (
            not ID.match(tid)
            or parsed.scheme not in ("http", "https")
            or parsed.username
            or parsed.password
        ):
            raise ValueError(f"target {tid} is invalid")
        prefixes = target.get("pathPrefixes")
        if (
            not isinstance(prefixes, list)
            or not prefixes
            or any(not isinstance(p, str) or not p.startswith("/") for p in prefixes)
        ):
            raise ValueError(f"target {tid} requires pathPrefixes")
        if target.get("mode") not in MODES:
            raise ValueError(f"target {tid} requires an explicit mode")
    for pid, profile in inventory["profiles"].items():
        _validate_profile(pid, profile)
    for gid, proxy in inventory["proxy_pools"].items():
        if not ID.match(gid) or not isinstance(proxy, dict):
            raise ValueError(f"proxy pool {gid} is invalid")
        if proxy.get("url") and proxy.get("urls"):
            raise ValueError(f"proxy pool {gid} cannot set url and urls")
        if "urls" in proxy and (not isinstance(proxy["urls"], list) or not proxy["urls"]):
            raise ValueError(f"proxy pool {gid} requires non-empty urls")
        for endpoint in proxy_urls(proxy):
            parsed = urlparse(endpoint)
            if parsed.scheme not in PROXY_SCHEMES or not parsed.netloc:
                raise ValueError(f"proxy pool {gid} is invalid")
    if not inventory["targets"] or not inventory["profiles"] or not inventory["proxy_pools"]:
        raise ValueError("automation inventory must include a target, profile, and proxy pool")
    unknown_defaults = set(inventory["defaults"]) - set(DEFAULT_KEYS)
    if unknown_defaults:
        raise ValueError(f"CAMOUFOX_DEFAULTS sets unsupported keys: {sorted(unknown_defaults)}")
    for key, category in DEFAULT_KEYS.items():
        selected = inventory["defaults"].get(key)
        if selected is not None and (not isinstance(selected, str) or selected not in inventory[category]):
            raise ValueError(f"CAMOUFOX_DEFAULTS {key} is unavailable")
    return inventory


def exact_path(target, requested="/"):
    if not isinstance(requested, str) or not requested.startswith("/"):
        raise ValueError("path must be relative to the authorized origin")
    base = target["baseUrl"].rstrip("/") + "/"
    destination = urljoin(base, requested)
    parsed_base, parsed_dest = urlparse(base), urlparse(destination)
    if (parsed_base.scheme, parsed_base.netloc) != (parsed_dest.scheme, parsed_dest.netloc) or not any(
        parsed_dest.path.startswith(prefix) for prefix in target["pathPrefixes"]
    ):
        raise ValueError("path is outside the authorized target scope")
    return destination


def configured_choice(options, requested, label):
    if requested is not None:
        if not isinstance(requested, str) or not ID.match(requested):
            raise ValueError(f"{label} identifier is invalid")
        if requested not in options:
            raise ValueError(f"authorized {label} is unavailable")
        return requested
    if "default" in options:
        return "default"
    if len(options) == 1:
        return next(iter(options))
    raise ValueError(f"{label} must be selected from the authorized inventory")


def agent_session_id(context):
    agent_id = context.get("agentId") if isinstance(context, dict) else None
    if not isinstance(agent_id, str) or not agent_id.strip():
        raise ValueError("durable Agent context is required to start a browser session")
    agent_id = agent_id.strip()
    if ID.fullmatch(agent_id):
        return agent_id
    return "agent-" + hashlib.sha256(agent_id.encode("utf-8")).hexdigest()[:32]


def run_digest(context):
    run_id = context.get("runId") if isinstance(context, dict) else None
    if not isinstance(run_id, str) or not run_id.strip():
        raise ValueError("durable Run context is required for browser session ownership")
    return "sha256:" + hashlib.sha256(run_id.strip().encode("utf-8")).hexdigest()


def read_json_file(path):
    try:
        with open(path, "r", encoding="utf-8") as handle:
            value = json.load(handle)
    except FileNotFoundError:
        return {}
    except (OSError, ValueError) as exc:
        raise ValueError("browser profile lease metadata is invalid") from exc
    if not isinstance(value, dict):
        raise ValueError("browser profile lease metadata is invalid")
    return value


def write_json_file(path, value):
    temporary = f"{path}.{os.getpid()}.{threading.get_ident()}.tmp"
    with open(temporary, "w", encoding="utf-8") as handle:
        os.chmod(temporary, 0o600)
        json.dump(value, handle, sort_keys=True, separators=(",", ":"))
        handle.flush()
        os.fsync(handle.fileno())
    os.replace(temporary, path)


def persistent_profile_directory(profile_directory):
    data_directory = os.path.join(profile_directory, "data")
    if os.path.isdir(data_directory):
        return data_directory, ""
    candidates = []
    for name in os.listdir(profile_directory):
        candidate = os.path.join(profile_directory, name)
        if name != "data" and os.path.isdir(candidate):
            candidates.append(candidate)
    if candidates:
        source = max(candidates, key=lambda candidate: (profile_cookie_count(candidate), os.path.getmtime(candidate)))
        os.replace(source, data_directory)
        return data_directory, os.path.basename(source)
    os.makedirs(data_directory, mode=0o700, exist_ok=True)
    return data_directory, ""


def agent_profile_directory(workspace, profile_id, session_id):
    profile_root = os.path.join(workspace, "profiles", profile_id)
    directory = os.path.join(profile_root, "agents", session_id)
    data_directory = os.path.join(directory, "data")
    legacy_data = os.path.join(profile_root, "data")
    os.makedirs(profile_root, mode=0o700, exist_ok=True)
    if not os.path.isdir(legacy_data):
        persistent_profile_directory(profile_root)
    if not os.path.isdir(data_directory) and os.path.isdir(legacy_data):
        os.makedirs(directory, mode=0o700, exist_ok=True)
        shutil.copytree(
            legacy_data,
            data_directory,
            ignore=shutil.ignore_patterns("lock", ".parentlock", "parent.lock"),
            ignore_dangling_symlinks=True,
        )
    return directory


def profile_cookie_count(profile_directory):
    cookie_database = ""
    for root, _directories, files in os.walk(profile_directory):
        if "cookies.sqlite" in files:
            cookie_database = os.path.join(root, "cookies.sqlite")
            break
    if not cookie_database:
        return 0
    connection = None
    try:
        connection = sqlite3.connect(f"file:{cookie_database}?mode=ro", uri=True, timeout=0.1)
        row = connection.execute("SELECT COUNT(*) FROM moz_cookies").fetchone()
        return int(row[0]) if row else 0
    except (OSError, sqlite3.Error, TypeError, ValueError):
        return 0
    finally:
        if connection is not None:
            connection.close()


def navigation_url(value):
    if not isinstance(value, str) or len(value) > 2048:
        raise ValueError("navigation URL is invalid")
    parsed = urlparse(value)
    if parsed.scheme not in ("http", "https") or not parsed.hostname or parsed.username or parsed.password:
        raise ValueError("navigation URL must be an HTTP(S) URL without embedded credentials")
    return value


def snapshot_element_state(element):
    """Project only non-secret control state from the browser boundary."""
    raw = element.get("state")
    if not isinstance(raw, dict):
        return {}
    result = {}
    for key in ("disabled", "checked", "selected", "required", "readonly", "filled"):
        if isinstance(raw.get(key), bool):
            result[key] = raw[key]
    for key in ("expanded", "pressed", "current", "autocomplete", "type"):
        value = raw.get(key)
        if isinstance(value, str) and value:
            result[key] = value[:128]
    return result


def credential_control_matches(field, control):
    """Require a current editable control whose semantics match the secret."""
    if not isinstance(control, dict) or control.get("role") not in ("textbox", "combobox"):
        return False
    state = control.get("state") if isinstance(control.get("state"), dict) else {}
    if state.get("disabled") or state.get("readonly"):
        return False
    semantics = " ".join((
        str(control.get("name", "")),
        str(control.get("context", "")),
        str(state.get("type", "")),
        str(state.get("autocomplete", "")),
    ))
    if field == "password":
        return bool(SECRET_CONTROL.search(semantics))
    if field == "username":
        return bool(USERNAME_CONTROL.search(semantics))
    return False


def target_allows_url(target, value):
    destination = navigation_url(value)
    base = urlparse(target.get("baseUrl", ""))
    parsed = urlparse(destination)
    return (
        (parsed.scheme, parsed.netloc) == (base.scheme, base.netloc)
        and any(parsed.path.startswith(prefix) for prefix in target.get("pathPrefixes", []))
    )


def detect_challenges(*evidence):
    bounded = "\n".join(value[:4096] for value in evidence if isinstance(value, str))
    return [kind for kind, pattern in CHALLENGES.items() if pattern.search(bounded)]


def digest(*parts):
    return "sha256:" + hashlib.sha256("\0".join(str(p) for p in parts).encode()).hexdigest()


def observation_digest(snapshot):
    """Return a content digest for model-observable browser state.

    Snapshot references are deliberately excluded: they are regenerated on
    every observation and do not represent page progress.  The digest also
    never exposes page or credential values; only the hash crosses the action
    boundary.
    """
    elements = []
    for element in (snapshot or {}).get("elements") or []:
        elements.append({
            "role": element.get("role", ""),
            "name": element.get("name", ""),
            "context": element.get("context", ""),
            "href": element.get("href", ""),
            "state": snapshot_element_state(element),
        })
    canonical = {
        "url": (snapshot or {}).get("url", ""),
        "title": (snapshot or {}).get("title", ""),
        "text": (snapshot or {}).get("text", "")[:MAX_TEXT],
        "elements": elements[:MAX_ELEMENTS],
    }
    return "sha256:" + hashlib.sha256(
        json.dumps(canonical, sort_keys=True, separators=(",", ":")).encode("utf-8")
    ).hexdigest()


def snapshot_link_destination(current_url, href):
    """Expose bounded, non-secret context for choosing a navigation link."""
    if not isinstance(href, str) or not href:
        return {}
    try:
        destination = urlparse(navigation_url(href))
        current = urlparse(navigation_url(current_url))
    except ValueError:
        return {}
    same_origin = (destination.scheme, destination.netloc) == (current.scheme, current.netloc)
    result = {"destinationScope": "same_origin" if same_origin else "external_origin"}
    if same_origin:
        # Query strings and fragments can contain credentials or tracking
        # values; the route alone is enough to distinguish local controls.
        result["destinationPath"] = destination.path or "/"
    return result


def stable_seed(value):
    return int.from_bytes(hashlib.sha256(str(value).encode()).digest()[:4], "big")


def resolve_proxy(proxy, pool_id, session_id):
    endpoints = proxy_urls(proxy)
    if not endpoints:
        return None
    if len(endpoints) == 1:
        return endpoints[0]
    return endpoints[stable_seed(f"{pool_id}:{session_id}") % len(endpoints)]


def profile_options(profile):
    return {key: profile[key] for key in PROFILE_KEYS if key in profile}


class BrowserWorker(threading.Thread):
    """Single dedicated thread for Playwright-sync engine calls."""

    def __init__(self):
        super().__init__(daemon=True)
        self._jobs = queue.Queue()
        self._poisoned = False
        self.start()

    def run(self):
        while True:
            job = self._jobs.get()
            if job is None:
                return
            fn, args, kwargs, result = job
            try:
                result["value"] = fn(*args, **kwargs)
            except BaseException as exc:  # surfaced to the caller thread
                result["error"] = exc
            finally:
                result["done"].set()

    def call(self, fn, *args, timeout, operation, **kwargs):
        if self._poisoned:
            raise BrowserOperationTimeout(
                "browser worker is unavailable after a timed-out operation; start a new session"
            )
        result = {"done": threading.Event(), "value": None, "error": None}
        self._jobs.put((fn, args, kwargs, result))
        if not result["done"].wait(timeout=timeout):
            self._poisoned = True
            raise BrowserOperationTimeout(
                f"browser {operation} exceeded its {timeout}-second action timeout; start a new session"
            )
        if result["error"] is not None:
            raise result["error"]
        return result["value"]

    def stop(self):
        self._jobs.put(None)
        self.join(timeout=10)


class CamoufoxHandle:
    """Real engine handle; every method runs on the worker thread."""

    def __init__(self, options):
        self._worker = BrowserWorker()
        try:
            self._worker.call(
                self._launch, options, timeout=WORKER_TIMEOUT_SECONDS["launch"], operation="launch"
            )
        except Exception:
            self._worker.stop()
            raise

    def _launch(self, options):
        from camoufox.sync_api import Camoufox

        kwargs = dict(options.get("profile_options") or {})
        if options.get("proxy"):
            kwargs["proxy"] = {"server": options["proxy"]}
        self._ctx = Camoufox(
            headless=options.get("headless", "virtual"),
            user_data_dir=options["user_data_dir"],
            persistent_context=True,
            **kwargs,
        )
        self._browser = self._ctx.__enter__()
        self._page = self._browser.new_page()

    def goto(self, url, timeout_ms=30000):
        def _goto():
            # Long-lived applications commonly keep analytics, streaming, or
            # notification requests open. DOM readiness is the stable browser
            # navigation boundary; snapshot() performs the bounded visual
            # settling needed before an Agent inspects or interacts.
            response = self._page.goto(url, timeout=timeout_ms, wait_until="domcontentloaded")
            return {"url": self._page.url, "status": response.status if response else 0}

        return self._worker.call(
            _goto,
            timeout=max(WORKER_TIMEOUT_SECONDS["navigate"], timeout_ms / 1000 + 5),
            operation="navigation",
        )

    def snapshot(self, include_model_media=False):
        def _snapshot():
            try:
                self._page.wait_for_load_state("domcontentloaded", timeout=3000)
            except Exception:
                pass
            self._page.evaluate(SETTLE_JS)
            result = self._page.evaluate(SNAPSHOT_JS)
            viewport = self._page.evaluate("() => ({width: innerWidth, height: innerHeight})")
            result["viewport"] = viewport
            if include_model_media:
                screenshot = self._page.screenshot(type="jpeg", quality=45, full_page=False, scale="css")
                if len(screenshot) > MAX_MODEL_SCREENSHOT:
                    screenshot = self._page.screenshot(type="jpeg", quality=25, full_page=False, scale="css")
                if len(screenshot) > MAX_MODEL_SCREENSHOT:
                    raise ValueError("model screenshot exceeds the 1MiB visual context limit")
                result["model_media"] = screenshot
            return result

        return self._worker.call(
            _snapshot, timeout=WORKER_TIMEOUT_SECONDS["snapshot"], operation="snapshot"
        )

    def click(self, marker, exact=False):
        def _click():
            locator = self._page.locator(f'[data-camoufox-ref="{marker}"]').first
            if exact:
                # An approved external commit must activate the exact reviewed
                # control once. Scroll the exact semantic target before the
                # actionability gate: reviewed controls may sit just outside
                # the viewport, where Locator.click otherwise waits for
                # visibility without first bringing them into view. The only
                # activation remains the trusted locator click below.
                locator.evaluate(
                    "element => element.scrollIntoView({block: 'center', inline: 'nearest'})",
                    timeout=5000,
                )
                locator.click(timeout=5000)
                return
            try:
                locator.scroll_into_view_if_needed(timeout=5000)
                box = locator.bounding_box()
                if not box:
                    raise ValueError("target has no visible mouse bounds")
                x, y = box["x"] + box["width"] / 2, box["y"] + box["height"] / 2
                self._page.mouse.move(x, y)
                self._page.mouse.click(x, y)
            except Exception:
                # Playwright's trusted locator click is still pointer input and
                # remains a safe fallback for controls with transient bounds.
                try:
                    locator.click(timeout=5000)
                except Exception:
                    # Legacy applications sometimes expose exact, current
                    # script-backed controls that Playwright can locate but
                    # cannot prove stable. Activate only that already-reviewed
                    # semantic reference; arbitrary script input is never
                    # accepted from the caller.
                    locator.evaluate("element => element.click()", timeout=5000)

        self._worker.call(_click, timeout=WORKER_TIMEOUT_SECONDS["click"], operation="click")

    def click_point(self, x, y):
        # Coordinate input is a last-resort visual fallback. Resolve it once in
        # the current document instead of allowing a patched mouse trajectory
        # to block the single browser worker indefinitely.
        self._worker.call(
            lambda: self._page.evaluate(
                "([x, y]) => { const element = document.elementFromPoint(x, y); "
                "if (!element) throw new Error('no element at coordinates'); element.click(); }",
                [x, y],
            ),
            timeout=WORKER_TIMEOUT_SECONDS["click"],
            operation="coordinate click",
        )

    def fill(self, marker, value):
        def _fill():
            field = self._page.locator(f'[data-camoufox-ref="{marker}"]').first
            # Filling a form control does not need pointer stability. Requiring
            # a preliminary click makes dynamic legacy forms wait indefinitely
            # even when the exact current textarea is visible and editable.
            # Playwright's fill action focuses the resolved control, replaces
            # its value, and dispatches the normal input events.
            field.fill(value, timeout=5000)

        self._worker.call(_fill, timeout=WORKER_TIMEOUT_SECONDS["fill"], operation="fill")

    def select(self, marker, value):
        def _select():
            field = self._page.locator(f'[data-camoufox-ref="{marker}"]').first
            try:
                return field.select_option(value=value)
            except Exception:
                return field.select_option(label=value)

        return self._worker.call(
            _select, timeout=WORKER_TIMEOUT_SECONDS["select"], operation="select"
        )

    def scroll(self, dx, dy):
        # Camoufox's humanized pointer patch can keep mouse.wheel() pending on
        # dynamic pages. Scrolling is observational, so use the browser's
        # synchronous viewport primitive and still enforce the worker bound.
        self._worker.call(
            lambda: self._page.evaluate("([dx, dy]) => window.scrollBy(dx, dy)", [dx, dy]),
            timeout=WORKER_TIMEOUT_SECONDS["scroll"],
            operation="scroll",
        )

    def screenshot(self, full_page=False):
        return self._worker.call(
            lambda: self._page.screenshot(full_page=full_page, type="png"),
            timeout=WORKER_TIMEOUT_SECONDS["screenshot"],
            operation="screenshot",
        )

    def close(self):
        try:
            self._worker.call(
                lambda: self._ctx.__exit__(None, None, None),
                timeout=WORKER_TIMEOUT_SECONDS["close"],
                operation="close",
            )
        finally:
            self._worker.stop()


def default_browser_factory(options):
    return CamoufoxHandle(options)


class CamoufoxRuntime:
    def __init__(self, inventory, workspace, browser_factory=None, now=None, lease_ttl_seconds=None):
        self.inventory = inventory
        self.workspace = workspace
        self.browser_factory = browser_factory or default_browser_factory
        self.now = now or (lambda: datetime.now(timezone.utc))
        configured_ttl = lease_ttl_seconds if lease_ttl_seconds is not None else os.environ.get(
            "CAMOUFOX_PROFILE_LEASE_TTL_SECONDS", DEFAULT_PROFILE_LEASE_TTL_SECONDS
        )
        try:
            self.lease_ttl = timedelta(seconds=int(configured_ttl))
        except (TypeError, ValueError) as exc:
            raise ValueError("Camoufox profile lease TTL must be an integer number of seconds") from exc
        if self.lease_ttl < timedelta(seconds=30) or self.lease_ttl > timedelta(hours=1):
            raise ValueError("Camoufox profile lease TTL must be between 30 and 3600 seconds")
        self.sessions = {}

    def execute(self, action, config=None, bindings=None, context=None):
        config = config or {}
        bindings = bindings or {}
        # The gRPC boundary always supplies an ExecutionContext. Unit tests may
        # call the runtime directly without one, but hosted calls fail closed.
        if context is not None and action not in ("camoufox-health", "camoufox-start"):
            expected_session = agent_session_id(context)
            if config.get("sessionId") != expected_session:
                raise ValueError("automation session belongs to a different durable Agent")
            session = self.sessions.get(expected_session)
            if (
                session is not None
                and session.get("run_digest") != run_digest(context)
                and not (action == "camoufox-close" and not session.get("run_digest"))
            ):
                raise ValueError("browser session usage is leased by another active Run")
        handlers = {
            "camoufox-health": lambda: self.health(),
            "camoufox-start": lambda: self.start(config, context),
            "camoufox-navigate": lambda: self.navigate(config),
            "camoufox-snapshot": lambda: self.snapshot(config, bindings),
            "camoufox-follow-link": lambda: self.follow_link(config),
            "camoufox-click": lambda: self.mutate("click", config),
            "camoufox-commit": lambda: self.mutate("commit", config),
            "camoufox-fill": lambda: self.mutate("fill", config),
            "camoufox-fill-secret": lambda: self.fill_secret(config, bindings),
            "camoufox-select": lambda: self.mutate("select", config),
            "camoufox-scroll": lambda: self.scroll(config),
            "camoufox-screenshot": lambda: self.screenshot(config),
            "camoufox-report": lambda: self.report(config),
            "camoufox-close": lambda: self.close(config),
        }
        if action not in handlers:
            raise ValueError(f"unsupported Camoufox action {action}")
        try:
            return handlers[action]()
        except BrowserOperationTimeout as exc:
            session_id = config.get("sessionId")
            session = self.sessions.get(session_id) if isinstance(session_id, str) else None
            if session is not None:
                session["terminal_error"] = str(exc)
            raise

    def health(self):
        if not self.inventory:
            return {
                "status": "needs_configuration",
                "skillId": "skill-browser",
                "version": VERSION,
                "authorizedTargets": 0,
                "profiles": 0,
                "proxyPools": 0,
            }
        return {
            "status": "ready",
            "skillId": "skill-browser",
            "version": VERSION,
            "authorizedTargets": len(self.inventory["targets"]),
            "profiles": len(self.inventory["profiles"]),
            "proxyPools": len(self.inventory["proxy_pools"]),
        }

    def session(self, session_id, allow_terminal=False):
        if not ID.match(session_id or ""):
            raise ValueError("sessionId is invalid")
        session = self.sessions.get(session_id)
        if not session:
            raise ValueError("automation session not found")
        if session.get("terminal_error") and not allow_terminal:
            raise ValueError(
                f"automation session is unavailable: {session['terminal_error']}"
            )
        if not allow_terminal and self.now() >= session["lease_expires_at"]:
            self._release_usage_lease(session, "usage_lease_expired")
            raise ValueError("browser session usage lease expired; acquire the Agent session again")
        if not allow_terminal:
            self._renew_session_lease(session)
        return session

    def _renew_session_lease(self, session):
        current = read_json_file(session["lease_path"])
        if (
            current.get("state") != "active"
            or current.get("sessionId") != session["id"]
            or current.get("runDigest") != session["run_digest"]
            or current.get("generation") != session["lease_generation"]
        ):
            raise ValueError("browser profile lease changed while the session was active")
        now = self.now()
        expires_at = now + self.lease_ttl
        write_json_file(session["lease_path"], {
            **current,
            "renewedAt": now.isoformat(),
            "expiresAt": expires_at.isoformat(),
        })
        session["lease_expires_at"] = expires_at

    def _release_session(self, session, reason="closed"):
        close_error = None
        try:
            session["handle"].close()
        except Exception as exc:
            close_error = exc
        lease_fd = session["lease_fd"]
        try:
            current = read_json_file(session["lease_path"])
            if (
                current.get("state") not in ("active", "idle")
                or current.get("sessionId") != session["id"]
                or current.get("generation") != session["lease_generation"]
                or (
                    current.get("state") == "active"
                    and current.get("runDigest") != session["run_digest"]
                )
            ):
                raise ValueError("browser profile lease changed while the session was active")
            write_json_file(session["lease_path"], {
                **current,
                "state": "released",
                "releasedAt": self.now().isoformat(),
                "releaseReason": reason,
            })
        finally:
            fcntl.flock(lease_fd, fcntl.LOCK_UN)
            os.close(lease_fd)
            self.sessions.pop(session["id"], None)
        if close_error is not None:
            raise close_error

    def _release_usage_lease(self, session, reason="run_released"):
        current = read_json_file(session["lease_path"])
        if (
            current.get("sessionId") != session["id"]
            or current.get("generation") != session["lease_generation"]
        ):
            raise ValueError("browser profile lease changed while the session was active")
        now = self.now()
        write_json_file(session["lease_path"], {
            **current,
            "state": "idle",
            "lastRunDigest": session.get("run_digest", ""),
            "runDigest": "",
            "releasedAt": now.isoformat(),
            "releaseReason": reason,
            "expiresAt": now.isoformat(),
        })
        session["run_digest"] = ""
        session["lease_expires_at"] = now

    def _claim_usage_lease(self, session, context):
        requested_run = run_digest(context)
        now = self.now()
        current_run = session.get("run_digest", "")
        if current_run == requested_run:
            self._renew_session_lease(session)
            return
        if current_run and now < session["lease_expires_at"]:
            raise ValueError("browser session usage is leased by another active Run")
        current = read_json_file(session["lease_path"])
        if (
            current.get("sessionId") != session["id"]
            or current.get("generation") != session["lease_generation"]
        ):
            raise ValueError("browser profile lease changed while the session was active")
        expires_at = now + self.lease_ttl
        write_json_file(session["lease_path"], {
            **current,
            "state": "active",
            "runDigest": requested_run,
            "acquiredAt": now.isoformat(),
            "renewedAt": now.isoformat(),
            "expiresAt": expires_at.isoformat(),
        })
        session["run_digest"] = requested_run
        session["lease_expires_at"] = expires_at
        session["refs"].clear()
        session["ref_names"].clear()
        session["ref_controls"].clear()
        session["ref_links"].clear()
        session["receipts"].clear()
        session["challenges"] = []
        session["snapshot_text"] = ""
        session["observation_digest"] = ""
        session["viewport"] = {}

    def _reclaim_expired_profile(self, profile_id):
        now = self.now()
        for session in list(self.sessions.values()):
            if (
                session["profile_id"] == profile_id
                and session.get("run_digest")
                and now >= session["lease_expires_at"]
            ):
                self._release_usage_lease(session, "usage_lease_expired")

    def start(self, config, context=None):
        if not self.inventory:
            raise ValueError("Camoufox inventory is not configured")
        forbidden = set(config) & (set(DEFAULT_KEYS) | {"sessionId"})
        if forbidden:
            raise ValueError(f"browser infrastructure fields are host-owned: {sorted(forbidden)}")
        session_id = agent_session_id(context)
        if session_id in self.sessions:
            session = self.sessions[session_id]
            if session.get("terminal_error"):
                self._release_session(session, "terminal_error")
                session = None
            if session is not None:
                self._claim_usage_lease(session, context)
                return {
                    "sessionId": session_id,
                    "url": session["current_url"],
                    "status": "active",
                }
        defaults = self.inventory.get("defaults") or {}
        target_id = configured_choice(self.inventory["targets"], defaults.get("targetId"), "target")
        profile_id = configured_choice(self.inventory["profiles"], defaults.get("profileId"), "browser profile")
        pool_id = configured_choice(self.inventory["proxy_pools"], defaults.get("proxyPoolId"), "proxy pool")
        target = self.inventory["targets"].get(target_id)
        profile = self.inventory["profiles"].get(profile_id)
        proxy = self.inventory["proxy_pools"].get(pool_id)
        if not target:
            raise ValueError("authorized target is unavailable")
        if not profile:
            raise ValueError("browser profile is unavailable")
        if proxy is None:
            raise ValueError("proxy pool is unavailable")
        if target["mode"] == "permitted-automation" and (profile.get("assessmentOnly") or proxy.get("assessmentOnly")):
            raise ValueError("assessment-only identity or egress cannot be used with a third-party automation target")
        destination = exact_path(target, config.get("path", "/"))
        profile_directory = agent_profile_directory(self.workspace, profile_id, session_id)
        os.makedirs(profile_directory, mode=0o700, exist_ok=True)
        self._reclaim_expired_profile(profile_id)
        lease_path = os.path.join(profile_directory, "lease.json")
        lease_lock_path = os.path.join(profile_directory, "lease.lock")
        lease_fd = os.open(lease_lock_path, os.O_RDWR | os.O_CREAT, 0o600)
        try:
            fcntl.flock(lease_fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError as exc:
            os.close(lease_fd)
            raise ValueError("browser profile is leased by another active Run") from exc
        try:
            previous_lease = read_json_file(lease_path)
            generation = int(previous_lease.get("generation") or 0) + 1
            user_data_dir, migrated_from = persistent_profile_directory(profile_directory)
            acquired_at = self.now()
            lease_expires_at = acquired_at + self.lease_ttl
            lease = {
                "generation": generation,
                "sessionId": session_id,
                "agentDigest": "sha256:" + hashlib.sha256(session_id.encode("utf-8")).hexdigest(),
                "runDigest": run_digest(context),
                "state": "active",
                "acquiredAt": acquired_at.isoformat(),
                "renewedAt": acquired_at.isoformat(),
                "expiresAt": lease_expires_at.isoformat(),
            }
            if migrated_from:
                lease["migratedFrom"] = migrated_from
            write_json_file(lease_path, lease)
        except Exception:
            fcntl.flock(lease_fd, fcntl.LOCK_UN)
            os.close(lease_fd)
            raise
        handle = None
        try:
            handle = self.browser_factory(
                {
                    "headless": "virtual",
                    "user_data_dir": user_data_dir,
                    "proxy": resolve_proxy(proxy, pool_id, session_id),
                    "profile_options": profile_options(profile),
                }
            )
            navigation = handle.goto(destination, timeout_ms=30000)
        except Exception:
            if handle is not None:
                try:
                    handle.close()
                except Exception:
                    pass
            try:
                write_json_file(
                    lease_path,
                    {
                        **lease,
                        "state": "released",
                        "releasedAt": self.now().isoformat(),
                        "releaseReason": "launch_failed",
                    },
                )
            finally:
                fcntl.flock(lease_fd, fcntl.LOCK_UN)
                os.close(lease_fd)
            raise
        self.sessions[session_id] = {
            "id": session_id,
            "target_id": target_id,
            "profile_id": profile_id,
            "proxy_pool_id": pool_id,
            "target": target,
            "handle": handle,
            "current_url": navigation.get("url") or destination,
            "status_code": navigation.get("status") or 0,
            "generation": 0,
            "refs": {},
            "ref_names": {},
            "ref_controls": {},
            "ref_links": {},
            "challenges": [],
            "snapshot_text": "",
            "observation_digest": "",
            "viewport": {},
            "mutations": 0,
            "receipts": {},
            "secrets": set(),
            "started_at": self.now(),
            "lease_fd": lease_fd,
            "lease_path": lease_path,
            "lease_generation": generation,
            "run_digest": lease["runDigest"],
            "lease_expires_at": lease_expires_at,
        }
        return {
            "sessionId": session_id,
            "url": destination,
            "status": "active",
        }

    def navigate(self, config):
        if not self.inventory:
            raise ValueError("Camoufox inventory is not configured")
        session = self.session(config.get("sessionId"))
        target_id = config.get("targetId")
        if target_id:
            target = self.inventory["targets"].get(target_id)
            if not target:
                raise ValueError("authorized target is unavailable")
            destination = exact_path(target, config.get("path", "/"))
        else:
            target = {"mode": "permitted-automation"}
            destination = navigation_url(config.get("url"))
        profile = self.inventory["profiles"].get(session["profile_id"])
        proxy = self.inventory["proxy_pools"].get(session["proxy_pool_id"])
        if not profile or proxy is None:
            raise ValueError("session identity configuration is unavailable")
        if target["mode"] == "permitted-automation" and (profile.get("assessmentOnly") or proxy.get("assessmentOnly")):
            raise ValueError("assessment-only identity or egress cannot be used with a third-party automation target")
        navigation = session["handle"].goto(destination, timeout_ms=30000)
        session["target_id"] = target_id or "unrestricted"
        session["target"] = target
        session["current_url"] = navigation.get("url") or destination
        session["status_code"] = navigation.get("status") or 0
        session["refs"].clear()
        session["ref_names"].clear()
        session["ref_controls"].clear()
        session["ref_links"].clear()
        session["challenges"] = []
        session["snapshot_text"] = ""
        session["viewport"] = {}
        return {
            "sessionId": session["id"],
            "url": session["current_url"],
            "statusCode": session["status_code"],
        }

    def snapshot(self, config, bindings=None):
        session = self.session(config.get("sessionId"))
        raw = session["handle"].snapshot(include_model_media=bool(config.get("includeScreenshot")))
        secret_values = [s for s in session["secrets"]] + [
            v for v in (bindings or {}).values() if isinstance(v, str) and len(v) >= 3
        ]

        def redact(value):
            text = str(value if value is not None else "")
            for secret in secret_values:
                text = text.replace(secret, "[REDACTED]")
            return text

        text = redact(raw.get("text", ""))[:MAX_TEXT]
        session["generation"] += 1
        session["refs"].clear()
        session["ref_names"].clear()
        session["ref_controls"].clear()
        session["ref_links"].clear()
        elements = []
        for index, element in enumerate((raw.get("elements") or [])[:MAX_ELEMENTS]):
            ref = f"s{session['generation']}:e{index + 1}"
            session["refs"][ref] = element["ref"]
            session["ref_names"][ref] = element.get("name", "")
            session["ref_controls"][ref] = {
                "role": element.get("role", ""),
                "name": element.get("name", ""),
                "context": element.get("context", ""),
                "state": snapshot_element_state(element),
            }
            href = element.get("href", "")
            if element.get("role") == "link" and isinstance(href, str) and href:
                session["ref_links"][ref] = href
            projected = {
                "ref": ref,
                "role": element.get("role", ""),
                "name": redact(element.get("name", "")),
                "context": redact(element.get("context", "")),
                "inViewport": bool(element.get("inViewport")),
                "bounds": element.get("bounds") or {},
                "state": snapshot_element_state(element),
            }
            if element.get("role") == "link":
                projected.update(snapshot_link_destination(raw.get("url", session["current_url"]), href))
            elements.append(projected)
        session["current_url"] = raw.get("url", session["current_url"])
        session["snapshot_text"] = text
        session["observation_digest"] = observation_digest(raw)
        session["viewport"] = raw.get("viewport") or {}
        session["challenges"] = detect_challenges(text, raw.get("url", ""), raw.get("title", ""))
        result = {
            "sessionId": session["id"],
            "generation": session["generation"],
            "observationDigest": session["observation_digest"],
            "url": raw.get("url", ""),
            "title": raw.get("title", ""),
            "text": text,
            "elements": elements,
            "challenges": session["challenges"],
            "requiresHuman": session["target"]["mode"] == "permitted-automation" and bool(session["challenges"]),
        }
        media = raw.get("model_media")
        if isinstance(media, (bytes, bytearray)):
            result["modelMedia"] = {
                "mediaType": "image/jpeg",
                "contentBase64": base64.b64encode(media).decode(),
                "detail": "low",
                "width": session["viewport"].get("width"),
                "height": session["viewport"].get("height"),
            }
        return result

    def follow_link(self, config):
        session = self.session(config.get("sessionId"))
        if session["target"]["mode"] == "permitted-automation" and session["challenges"]:
            raise ValueError("the destination presented an access challenge; human completion is required")
        target_ref = config.get("target")
        destination = session["ref_links"].get(target_ref)
        if not destination:
            raise ValueError("target is not a current navigable link; take a new snapshot")
        destination = navigation_url(destination)
        target = session["target"]
        if session["target_id"] == "unrestricted":
            current = urlparse(session["current_url"])
            parsed = urlparse(destination)
            if (parsed.scheme, parsed.netloc) != (current.scheme, current.netloc):
                raise ValueError("link destination is outside the active navigation origin")
        elif not target_allows_url(target, destination):
            raise ValueError("link destination is outside the authorized target scope")
        navigation = session["handle"].goto(destination, timeout_ms=30000)
        session["current_url"] = navigation.get("url") or destination
        session["status_code"] = navigation.get("status") or 0
        session["refs"].clear()
        session["ref_names"].clear()
        session["ref_controls"].clear()
        session["ref_links"].clear()
        session["challenges"] = []
        session["snapshot_text"] = ""
        session["viewport"] = {}
        return {
            "sessionId": session["id"],
            "targetId": session["target_id"],
            "url": session["current_url"],
            "statusCode": session["status_code"],
        }

    def mutate(self, operation, config, secret_value=None):
        session = self.session(config.get("sessionId"))
        if session["target"]["mode"] == "permitted-automation" and session["challenges"]:
            raise ValueError("the destination presented an access challenge; human completion is required")
        key = config.get("idempotencyKey")
        if not isinstance(key, str) or len(key) < 8:
            raise ValueError("idempotencyKey is required")
        value = secret_value if secret_value is not None else config.get("value", "")
        fingerprint = digest(operation, config.get("target"), config.get("generation"), config.get("x"), config.get("y"), value)
        receipt_key = f"{operation}:{key}"
        if receipt_key in session["receipts"]:
            receipt = session["receipts"][receipt_key]
            if receipt["fingerprint"] != fingerprint:
                raise ValueError("idempotency key was reused with different arguments")
            return {**receipt["result"], "duplicate": True}
        target_ref = config.get("target")
        marker = session["refs"].get(target_ref) if target_ref else None
        point = target_ref is None and operation in ("click", "commit")
        if operation == "commit":
            control = session["ref_controls"].get(target_ref)
            state = control.get("state") if isinstance(control, dict) else {}
            if not isinstance(control, dict) or control.get("role") != "button":
                raise ValueError("external commit target must be a current button; take a new snapshot")
            if isinstance(state, dict) and state.get("disabled") is True:
                raise ValueError("external commit target is disabled; take a new snapshot")
        if marker is None and not point:
            raise ValueError("target is stale or unknown; take a new snapshot")
        if point:
            generation, x, y = config.get("generation"), config.get("x"), config.get("y")
            viewport = session.get("viewport") or {}
            if generation != session["generation"]:
                raise ValueError("screenshot generation is stale; take a new snapshot")
            if not isinstance(x, (int, float)) or not isinstance(y, (int, float)):
                raise ValueError("screenshot coordinates must be numbers")
            if x < 0 or y < 0 or x >= viewport.get("width", 0) or y >= viewport.get("height", 0):
                raise ValueError("screenshot coordinates are outside the current viewport")
        if (
            operation == "fill"
            and secret_value is None
            and SECRET_CONTROL.search(session["ref_names"].get(target_ref, ""))
        ):
            raise ValueError("literal values cannot fill a secret-like control; use an authorized credential binding")
        if operation in ("click", "commit"):
            if point:
                session["handle"].click_point(x, y)
            else:
                session["handle"].click(marker, exact=operation == "commit")
        elif operation == "select":
            session["handle"].select(marker, value)
        else:
            session["handle"].fill(marker, value)
        before_digest = session.get("observation_digest") or ""
        observation_attempts = COMMIT_OBSERVATION_ATTEMPTS if operation == "commit" else 1
        after_snapshot = None
        after_digest = before_digest
        for _attempt in range(observation_attempts):
            after_snapshot = session["handle"].snapshot(include_model_media=False)
            after_digest = observation_digest(after_snapshot)
            if not before_digest or after_digest != before_digest:
                break
        session["current_url"] = after_snapshot.get("url", session["current_url"])
        session["snapshot_text"] = str(after_snapshot.get("text", ""))[:MAX_TEXT]
        session["observation_digest"] = after_digest
        changed = bool(before_digest and before_digest != after_digest)
        if operation == "commit" and not changed:
            session["refs"].clear()
            session["ref_names"].clear()
            session["ref_controls"].clear()
            session["ref_links"].clear()
            raise ValueError(
                "external commit produced no observable progress; completion is unverified; "
                "take a fresh snapshot and confirm the external state before any retry"
            )
        result = {
            "sessionId": session["id"],
            "success": True,
            "duplicate": False,
            "receipt": key,
            "progress": {
                "changed": changed,
                "beforeDigest": before_digest,
                "afterDigest": after_digest,
            },
        }
        session["receipts"][receipt_key] = {"fingerprint": fingerprint, "result": result}
        session["refs"].clear()
        session["ref_names"].clear()
        session["ref_controls"].clear()
        session["ref_links"].clear()
        session["mutations"] += 1
        return result

    def fill_secret(self, config, bindings):
        field = config.get("credentialField")
        secret = bindings.get(field) if isinstance(field, str) else None
        if not isinstance(secret, str) or not secret:
            raise ValueError("authorized credential field is unavailable")
        session = self.session(config.get("sessionId"))
        receipt_key = f"fill:{config.get('idempotencyKey')}"
        if receipt_key not in session["receipts"] and not credential_control_matches(
            field, session["ref_controls"].get(config.get("target"))
        ):
            raise ValueError(f"target is not a current {field} control; take a new snapshot")
        result = self.mutate("fill", config, secret)
        session["secrets"].add(secret)
        return result

    def scroll(self, config):
        session = self.session(config.get("sessionId"))
        dx, dy = config.get("dx", 0), config.get("dy", 0)
        if not isinstance(dx, (int, float)) or not isinstance(dy, (int, float)):
            raise ValueError("dx and dy must be numbers")
        if abs(dx) > 10000 or abs(dy) > 10000:
            raise ValueError("scroll delta is out of range")
        session["handle"].scroll(dx, dy)
        return {"sessionId": session["id"], "scrolled": True, "dx": dx, "dy": dy}

    def screenshot(self, config):
        session = self.session(config.get("sessionId"))
        png = session["handle"].screenshot(full_page=bool(config.get("fullPage")))
        if len(png) > MAX_SCREENSHOT:
            raise ValueError("screenshot exceeds the 5MiB evidence limit")
        return {
            "sessionId": session["id"],
            "url": session["current_url"],
            "mediaType": "image/png",
            "bytes": len(png),
            "contentBase64": base64.b64encode(png).decode(),
        }

    def report(self, config):
        session = self.session(config.get("sessionId"))
        outcome = "accepted"
        if session["status_code"] in (401, 403, 429):
            outcome = "blocked"
        elif session["challenges"]:
            outcome = "challenged"
        return {
            "sessionId": session["id"],
            "outcome": outcome,
            "httpStatus": session["status_code"],
            "challenges": session["challenges"],
            "mutationCount": session["mutations"],
            "startedAt": session["started_at"].isoformat(),
            "evidence": {"snapshotDigest": digest(session["current_url"], session["snapshot_text"])},
        }

    def close(self, config):
        session = self.session(config.get("sessionId"), allow_terminal=True)
        self._release_usage_lease(session)
        return {
            "sessionId": session["id"],
            "closed": False,
            "usageReleased": True,
            "profilePreserved": True,
            "sessionPreserved": True,
        }
