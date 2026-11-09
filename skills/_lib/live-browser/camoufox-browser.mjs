import { firefox } from 'playwright-core';
import { launchOptions } from 'camoufox-js';
import { mkdir, readFile, rename, rm, writeFile } from 'node:fs/promises';
import { dirname, join } from 'node:path';
import { createHash } from 'node:crypto';
import net from 'node:net';
import tls from 'node:tls';
import { IDENTITY_FILE } from './browser-profile.mjs';

const SINK = /^[a-z][a-z0-9_]{0,62}$/;
const CONFIG_ENV = /^CAMOU_CONFIG_[0-9]+$/;
// Firefox's profile locks: the `lock` symlink names the host and PID that
// hold the profile, `.parentlock` is the fcntl lock file. After a crash or a
// pod restart they name a process that no longer exists (on another host),
// and Firefox would refuse the profile as "already in use".
export const PROFILE_LOCK_FILES = ['lock', '.parentlock', 'parent.lock'];

// Only the exclusive profile lease holder may call this: no browser of this
// runtime has the profile open, and the slot holds the only copy in use.
export async function clearStaleLocks(browserDir) {
  for (const name of PROFILE_LOCK_FILES) await rm(join(browserDir, name), { force: true });
}

async function installedRelease(executablePath) {
  try { return (await readFile(join(dirname(executablePath), 'version.json'), 'utf8')).trim(); } catch { return ''; }
}

// A real browser keeps one fingerprint for its profile; sites that bind a
// sign-in to the device would otherwise see a new machine on every launch.
// The generated Camoufox config (CAMOU_CONFIG_n) and its WebGL preference are
// kept next to the profile and reused until the Camoufox build changes.
async function stableIdentity(profileRoot, options) {
  const file = join(profileRoot, IDENTITY_FILE);
  const release = `${options.executablePath ?? ''}\n${await installedRelease(options.executablePath ?? '')}`;
  let saved;
  try { saved = JSON.parse(await readFile(file, 'utf8')); } catch { /* first launch */ }
  const env = Object.fromEntries(Object.entries(options.env ?? {}).filter(([key]) => !CONFIG_ENV.test(key)));
  const prefs = { ...(options.firefoxUserPrefs ?? {}) };
  const valid = saved?.release === release && saved.config && typeof saved.config === 'object' &&
    Object.keys(saved.config).length && Object.entries(saved.config).every(([key, value]) => CONFIG_ENV.test(key) && typeof value === 'string');
  if (valid) {
    if (typeof saved.webgl2 === 'boolean') prefs['webgl.enable-webgl2'] = saved.webgl2;
    return { ...options, env: { ...env, ...saved.config }, firefoxUserPrefs: prefs };
  }
  const config = Object.fromEntries(Object.entries(options.env ?? {}).filter(([key]) => CONFIG_ENV.test(key)));
  if (Object.keys(config).length) {
    await writeFile(`${file}.tmp`, JSON.stringify({ release, config, webgl2: prefs['webgl.enable-webgl2'] }), { mode: 0o600 });
    await rename(`${file}.tmp`, file);
  }
  return options;
}

// Upstream proxies for page traffic: the operator sets the list in the live
// browser Skill's `proxies` config (global, with tenant overrides), which
// arrives with each call and is passed to launch. Each entry is a proxy URL
// (http://user:pass@host:port) or a provider list line (host:port:user:pass);
// entries are separated by newlines or commas. A tenant always prefers the
// same entry, so sites see one stable IP for its sign-ins; rendezvous hashing
// keeps every other tenant on its proxy when the list changes. When the
// preferred proxy does not answer, the tenant moves to the next one in its own
// order rather than timing out on every page.
const PROXY_PROBE_TARGET = 'example.com:443';
const PROXY_PROBE_TIMEOUT_MS = 5000;

function parseProxy(entry) {
  if (/^[a-z][a-z0-9+.-]*:\/\//i.test(entry)) {
    const url = new URL(entry);
    return { server: `${url.protocol}//${url.host}`,
      ...(url.username ? { username: decodeURIComponent(url.username), password: decodeURIComponent(url.password) } : {}) };
  }
  const [host, port, username, ...password] = entry.split(':');
  if (!host || !/^[0-9]{1,5}$/.test(port ?? '')) throw new Error('Browser proxy list entry is invalid');
  return { server: `http://${host}:${port}`, ...(username ? { username, password: password.join(':') } : {}) };
}

// The tenant's proxies from the configured list, most preferred first.
export function tenantProxies(list, tenant) {
  const entries = String(list ?? '').split(/[\n,]/).map(entry => entry.trim()).filter(Boolean);
  tenant = String(tenant ?? '').trim();
  const score = entry => createHash('sha256').update(`${tenant}\n${entry}`).digest().readBigUInt64BE();
  return entries.map(entry => ({ entry, score: score(entry) }))
    .sort((a, b) => (a.score < b.score ? 1 : a.score > b.score ? -1 : 0))
    .map(({ entry }) => parseProxy(entry));
}

// An HTTP(S) proxy is usable when it accepts a tunnel within the timeout.
// Other schemes (SOCKS) are not probed.
export function probeProxy(proxy, { timeoutMs = PROXY_PROBE_TIMEOUT_MS } = {}) {
  const url = new URL(proxy.server);
  if (url.protocol !== 'http:' && url.protocol !== 'https:') return Promise.resolve(true);
  return new Promise(resolve => {
    const secure = url.protocol === 'https:';
    const port = Number(url.port) || (secure ? 443 : 80);
    const socket = secure ? tls.connect({ host: url.hostname, port, servername: url.hostname }) : net.connect(port, url.hostname);
    const done = ok => { socket.destroy(); resolve(ok); };
    const auth = proxy.username === undefined ? '' :
      `Proxy-Authorization: Basic ${Buffer.from(`${proxy.username}:${proxy.password ?? ''}`).toString('base64')}\r\n`;
    socket.once(secure ? 'secureConnect' : 'connect', () =>
      socket.write(`CONNECT ${PROXY_PROBE_TARGET} HTTP/1.1\r\nHost: ${PROXY_PROBE_TARGET}\r\n${auth}\r\n`));
    let reply = '';
    socket.on('data', data => {
      reply += data.toString('latin1');
      const end = reply.indexOf('\r\n');
      if (end >= 0) done(/^HTTP\/1\.[01] 200 /.test(reply.slice(0, end)));
      else if (reply.length > 1024) done(false);
    });
    socket.setTimeout(timeoutMs, () => done(false));
    socket.once('error', () => done(false));
    socket.once('end', () => done(false));
  });
}

// In the shared browser pool the slot's own egress proxy carries all page
// traffic: the pool chains it to the tenant's proxy, so the runtime neither
// sees the configured list nor probes anything.
export function slotProxy(value) {
  if (value === undefined || value === '') return undefined;
  let url;
  try { url = new URL(value); } catch { throw new Error('The slot proxy is invalid'); }
  if (url.protocol !== 'http:' || url.username || url.password || url.pathname !== '/' || url.search || url.hash) {
    throw new Error('The slot proxy is invalid');
  }
  return { server: value };
}

export async function reachableProxy(list, tenant, probe = probeProxy) {
  const proxies = tenantProxies(list, tenant);
  if (!proxies.length) return undefined;
  for (const proxy of proxies) if (await probe(proxy)) return proxy;
  throw new Error('No browser proxy is reachable');
}

// Preferences on top of Camoufox's own.
export const FIREFOX_PREFS = Object.freeze({
  'media.navigator.streams.fake': false,
  'permissions.default.microphone': 1,
  'permissions.default.camera': 2,
  'media.autoplay.default': 0,
  // Camoufox ships with site isolation off; browsers of several people share
  // a pod, so every site gets its own content process.
  'fission.autostart': true,
  'fission.webContentIsolationStrategy': 1,
  // The slot has only a loopback interface and reaches the internet through
  // its proxy: without these WebRTC gathers no candidates, and meetings
  // relay over TURN-over-TLS through the proxy.
  'media.peerconnection.ice.default_address_only': false,
  'media.peerconnection.ice.loopback': true,
  // A bounded disk cache (KiB), so a profile stays small to save and restore.
  'browser.cache.disk.capacity': 262144,
  'browser.cache.disk.smart_size.enabled': false,
  // Page traffic to localhost goes to the proxy too, which refuses it: pages
  // never reach the runtime's own ports in the slot.
  'network.proxy.allow_hijacking_localhost': true,
  'network.proxy.no_proxies_on': '',
});

// Launches Camoufox on the persistent profile <profileRoot>/camoufox. Audio
// routing (PULSE_SINK/PULSE_SOURCE) is per browser so a page never hears or
// speaks into another browser's audio. With a slot proxy, every launch uses
// exactly that proxy and the configured list is ignored.
export async function launchCamoufox(profileRoot, { display, audio, proxies, tenant, slotProxy: fixedProxy } = {}, {
  prepareOptions = launchOptions, browserType = firefox, probe = probeProxy,
} = {}) {
  if (!profileRoot || (display !== undefined && !/^:[0-9]{1,5}$/.test(display)) ||
    (audio !== undefined && (!SINK.test(audio?.sink ?? '') || !SINK.test(audio?.source ?? '')))) {
    throw new Error('Browser profile or display is unavailable');
  }
  const proxy = slotProxy(fixedProxy) ?? await reachableProxy(proxies, tenant, probe);
  const prepared = await prepareOptions({
    ...(proxy ? { proxy } : {}),
    os: 'linux', headless: !display, window: [1280, 800],
    humanize: false, block_webrtc: false, geoip: false,
    // No runtime extension downloads or third-party network setup calls.
    exclude_addons: ['UBO'],
    env: { ...process.env, ...(display ? { DISPLAY: display, MOZ_ENABLE_WAYLAND: '0' } : {}),
      ...(audio ? { PULSE_SINK: audio.sink, PULSE_SOURCE: audio.source } : {}) },
    firefox_user_prefs: { ...FIREFOX_PREFS },
  });
  const browserDir = join(profileRoot, 'camoufox');
  await mkdir(browserDir, { recursive: true, mode: 0o700 });
  const options = await stableIdentity(profileRoot, prepared);
  await clearStaleLocks(browserDir);
  return browserType.launchPersistentContext(browserDir, {
    ...options, viewport: null, acceptDownloads: false,
  });
}

export const camoufoxBrowser = { launchPersistentContext: launchCamoufox };
