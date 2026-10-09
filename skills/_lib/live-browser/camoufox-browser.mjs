import { firefox } from 'playwright-core';
import { launchOptions } from 'camoufox-js';
import { mkdir, readFile, rename, rm, writeFile } from 'node:fs/promises';
import { dirname, join } from 'node:path';
import { createHash } from 'node:crypto';
import { IDENTITY_FILE } from './browser-profile.mjs';

const SINK = /^[a-z][a-z0-9_]{0,62}$/;
const CONFIG_ENV = /^CAMOU_CONFIG_[0-9]+$/;
// Firefox's profile locks: the `lock` symlink names the host and PID that
// hold the profile, `.parentlock` is the fcntl lock file. After a crash or a
// pod restart they name a process that no longer exists (on another host),
// and Firefox would refuse the profile as "already in use".
export const PROFILE_LOCK_FILES = ['lock', '.parentlock', 'parent.lock'];

// Only the exclusive profile lease holder may call this: no browser of this
// runtime has the profile open, and the volume has a single writer pod.
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

// Upstream proxies for page traffic, shared by every tenant's runtime: hosting
// injects the list from one platform Secret. Each entry is a proxy URL
// (http://user:pass@host:port) or a provider list line (host:port:user:pass);
// entries are separated by newlines or commas. A tenant always uses the same
// entry, so sites see one stable IP for its sign-ins; rendezvous hashing
// keeps every other tenant on its proxy when the list changes. The list stays
// out of the browser process environment.
const PROXY_ENV = 'LIVE_BROWSER_PROXIES';
const TENANT_ENV = 'CORTEX_TENANT_ID';

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

export function proxyFromEnv(env = process.env) {
  const entries = (env[PROXY_ENV] ?? '').split(/[\n,]/).map(entry => entry.trim()).filter(Boolean);
  if (!entries.length) return undefined;
  const tenant = (env[TENANT_ENV] ?? '').trim();
  const score = entry => createHash('sha256').update(`${tenant}\n${entry}`).digest().readBigUInt64BE();
  const [chosen] = entries.map(entry => ({ entry, score: score(entry) })).sort((a, b) => (a.score < b.score ? 1 : a.score > b.score ? -1 : 0));
  return parseProxy(chosen.entry);
}

// Launches Camoufox on the persistent profile <profileRoot>/camoufox. Audio
// routing (PULSE_SINK/PULSE_SOURCE) is per browser so a page never hears or
// speaks into another browser's audio.
export async function launchCamoufox(profileRoot, { display, audio } = {}, {
  prepareOptions = launchOptions, browserType = firefox,
} = {}) {
  if (!profileRoot || (display !== undefined && !/^:[0-9]{1,5}$/.test(display)) ||
    (audio !== undefined && (!SINK.test(audio?.sink ?? '') || !SINK.test(audio?.source ?? '')))) {
    throw new Error('Browser profile or display is unavailable');
  }
  const proxy = proxyFromEnv();
  const env = Object.fromEntries(Object.entries(process.env).filter(([key]) => key !== PROXY_ENV));
  const prepared = await prepareOptions({
    ...(proxy ? { proxy } : {}),
    os: 'linux', headless: !display, window: [1280, 800],
    humanize: false, block_webrtc: false, geoip: false,
    // No runtime extension downloads or third-party network setup calls.
    exclude_addons: ['UBO'],
    env: { ...env, ...(display ? { DISPLAY: display, MOZ_ENABLE_WAYLAND: '0' } : {}),
      ...(audio ? { PULSE_SINK: audio.sink, PULSE_SOURCE: audio.source } : {}) },
    firefox_user_prefs: {
      'media.navigator.streams.fake': false,
      'permissions.default.microphone': 1,
      'permissions.default.camera': 2,
      'media.autoplay.default': 0,
    },
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
