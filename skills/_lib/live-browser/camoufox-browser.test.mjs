import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, mkdir, readlink, rm, symlink, writeFile, readdir } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { createServer } from 'node:net';
import { launchCamoufox, probeProxy, proxiesFromEnv, reachableProxy } from './camoufox-browser.mjs';

const proxyFromEnv = env => proxiesFromEnv(env)[0];

async function profileRoot() {
  const dir = await mkdtemp(join(tmpdir(), 'camoufox-test-'));
  return { dir, cleanup: () => rm(dir, { recursive: true, force: true }) };
}

// Simulates camoufox-js: a new random fingerprint on every call.
function preparer(executablePath = '/opt/camoufox/camoufox', offset = 0) {
  let generation = offset;
  const seen = [];
  return { seen, prepareOptions: async options => {
    seen.push(options);
    generation++;
    return { env: { ...options.env, CAMOU_CONFIG_1: `{"seed":${generation},`, CAMOU_CONFIG_2: `"g":${generation}}`,
      ...(generation % 10 === 1 ? { CAMOU_CONFIG_3: '' } : {}) }, executablePath, firefoxUserPrefs: { ...options.firefox_user_prefs, 'webgl.enable-webgl2': generation % 2 === 1 } };
  } };
}

test('Camoufox launches the persistent profile on the volume with real audio and no Chromium flags', async () => {
  const { dir, cleanup } = await profileRoot();
  try {
    const { seen, prepareOptions } = preparer();
    const expected = {};
    const result = await launchCamoufox(dir, { display: ':17', audio: { sink: 'lb_1_capture', source: 'lb_1_source' } }, {
      prepareOptions,
      browserType: { launchPersistentContext: async (profile, options) => {
        assert.equal(profile, join(dir, 'camoufox'));
        assert.equal(options.viewport, null);
        assert.equal(options.acceptDownloads, false);
        assert.equal(options.env.DISPLAY, ':17');
        assert.equal(options.env.PULSE_SINK, 'lb_1_capture');
        assert.equal(options.env.PULSE_SOURCE, 'lb_1_source');
        assert.equal(options.args, undefined);
        assert.equal(options.permissions, undefined);
        return expected;
      } },
    });
    assert.equal(result, expected);
    const [configured] = seen;
    assert.equal(configured.headless, false);
    assert.equal(configured.humanize, false);
    assert.equal(configured.block_webrtc, false);
    assert.equal(configured.firefox_user_prefs['media.navigator.streams.fake'], false);
    assert.equal(configured.firefox_user_prefs['permissions.default.microphone'], 1);
    assert.equal(configured.firefox_user_prefs['permissions.default.camera'], 2);
    assert.deepEqual(configured.exclude_addons, ['UBO']);
  } finally { await cleanup(); }
});

test('stale Firefox locks from a crashed browser or another pod are removed before launch', async () => {
  const { dir, cleanup } = await profileRoot();
  try {
    const browserDir = join(dir, 'camoufox');
    await mkdir(browserDir);
    await symlink('10.42.0.17:+4242', join(browserDir, 'lock'));
    await writeFile(join(browserDir, '.parentlock'), '');
    await writeFile(join(browserDir, 'cookies.sqlite'), 'kept');
    assert.equal(await readlink(join(browserDir, 'lock')), '10.42.0.17:+4242');
    await launchCamoufox(dir, {}, { ...preparer(), browserType: { launchPersistentContext: async () => {
      assert.deepEqual((await readdir(browserDir)).sort(), ['cookies.sqlite']);
      return {};
    } } });
  } finally { await cleanup(); }
});

test('the profile keeps one fingerprint across launches until the Camoufox build changes', async () => {
  const { dir, cleanup } = await profileRoot();
  try {
    const launches = [];
    const browserType = { launchPersistentContext: async (_profile, options) => { launches.push(options); return {}; } };
    const first = preparer();
    await launchCamoufox(dir, {}, { prepareOptions: first.prepareOptions, browserType });
    await launchCamoufox(dir, {}, { prepareOptions: first.prepareOptions, browserType });
    const config = options => Object.fromEntries(Object.entries(options.env).filter(([key]) => key.startsWith('CAMOU_CONFIG_')));
    assert.deepEqual(config(launches[1]), config(launches[0]), 'the second launch reuses the first fingerprint exactly');
    assert.deepEqual(config(launches[0]), { CAMOU_CONFIG_1: '{"seed":1,', CAMOU_CONFIG_2: '"g":1}', CAMOU_CONFIG_3: '' });
    assert.equal(launches[1].firefoxUserPrefs['webgl.enable-webgl2'], true);
    // A new browser build gets a new, then stable, fingerprint.
    const upgraded = preparer('/opt/camoufox-next/camoufox', 10);
    await launchCamoufox(dir, {}, { prepareOptions: upgraded.prepareOptions, browserType });
    assert.deepEqual(config(launches[2]), { CAMOU_CONFIG_1: '{"seed":11,', CAMOU_CONFIG_2: '"g":11}', CAMOU_CONFIG_3: '' });
    await launchCamoufox(dir, {}, { prepareOptions: upgraded.prepareOptions, browserType });
    assert.deepEqual(config(launches[3]), config(launches[2]));
  } finally { await cleanup(); }
});

test('invalid private display or missing profile fails before browser preparation', async () => {
  const options = { prepareOptions: () => { throw new Error('must not run'); } };
  await assert.rejects(launchCamoufox('', {}, options), /profile or display/);
  await assert.rejects(launchCamoufox('/profile', { display: 'remote:0' }, options), /profile or display/);
  await assert.rejects(launchCamoufox('/profile', { audio: { sink: 'x;rm', source: 'ok' } }, options), /profile or display/);
});

const POOL = ['31.59.20.176:6754:user:p:ss', '45.38.107.97:6014:user:p:ss', 'http://u%40x:pw@64.137.96.74:6641', '198.23.243.226:6361:user:p:ss'].join('\n');

test('each tenant browses through its own stable proxy from the shared list, which stays out of the browser environment', async () => {
  const { dir, cleanup } = await profileRoot();
  const saved = { ...process.env };
  try {
    Object.assign(process.env, { LIVE_BROWSER_PROXIES: POOL, CORTEX_TENANT_ID: '42' });
    const { seen, prepareOptions } = preparer();
    let launched;
    await launchCamoufox(dir, {}, { prepareOptions, probe: async () => true, browserType: { launchPersistentContext: async (_profile, options) => {
      launched = options;
      return {};
    } } });
    assert.deepEqual(seen[0].proxy, proxyFromEnv({ LIVE_BROWSER_PROXIES: POOL, CORTEX_TENANT_ID: '42' }));
    assert.equal(seen[0].env.LIVE_BROWSER_PROXIES, undefined);
    assert.equal(launched.env.LIVE_BROWSER_PROXIES, undefined);
  } finally {
    for (const key of Object.keys(process.env)) if (!(key in saved)) delete process.env[key];
    Object.assign(process.env, saved);
    await cleanup();
  }
});

test('a tenant keeps its proxy across launches and when other entries are added or removed', () => {
  const entries = POOL.split('\n');
  const pick = (list, tenant) => proxyFromEnv({ LIVE_BROWSER_PROXIES: list.join(','), CORTEX_TENANT_ID: String(tenant) });
  const servers = new Set();
  for (let tenant = 1; tenant <= 200; tenant++) {
    const chosen = pick(entries, tenant);
    servers.add(chosen.server);
    assert.deepEqual(pick(entries, tenant), chosen);
    const added = pick([...entries, '191.96.254.138:6185:user:p:ss'], tenant);
    assert.ok(added.server === chosen.server || added.server === 'http://191.96.254.138:6185', 'an added proxy only takes tenants, never reshuffles them');
    const removed = entries.find(entry => !entry.includes(chosen.server.split('//')[1]));
    assert.deepEqual(pick(entries.filter(entry => entry !== removed), tenant), chosen);
  }
  assert.equal(servers.size, entries.length, 'tenants spread across the whole list');
});

test('proxy list entries parse from provider lines and URLs', () => {
  assert.deepEqual(proxyFromEnv({ LIVE_BROWSER_PROXIES: '31.59.20.176:6754:hsk:p:ss' }),
    { server: 'http://31.59.20.176:6754', username: 'hsk', password: 'p:ss' });
  assert.deepEqual(proxyFromEnv({ LIVE_BROWSER_PROXIES: 'http://u%40x:pw@64.137.96.74:6641' }),
    { server: 'http://64.137.96.74:6641', username: 'u@x', password: 'pw' });
  assert.deepEqual(proxyFromEnv({ LIVE_BROWSER_PROXIES: 'socks5://proxy:1080' }), { server: 'socks5://proxy:1080' });
  assert.throws(() => proxyFromEnv({ LIVE_BROWSER_PROXIES: 'not-a-proxy' }), /invalid/);
});

test('without a configured proxy list the browser connects directly', async () => {
  assert.equal(proxyFromEnv({}), undefined);
  assert.equal(proxyFromEnv({ LIVE_BROWSER_PROXIES: ' \n , ' }), undefined);
  assert.equal(await reachableProxy({}, () => { throw new Error('must not probe'); }), undefined);
});

test('a tenant whose proxy does not answer moves to its next proxy, and back once it recovers', async () => {
  const env = { LIVE_BROWSER_PROXIES: POOL, CORTEX_TENANT_ID: '42' };
  const [first, second, third] = proxiesFromEnv(env);
  const probed = [];
  const down = new Set([first.server]);
  const probe = async proxy => { probed.push(proxy.server); return !down.has(proxy.server); };
  assert.deepEqual(await reachableProxy(env, probe), second);
  assert.deepEqual(probed, [first.server, second.server]);
  down.add(second.server);
  assert.deepEqual(await reachableProxy(env, probe), third);
  down.clear();
  assert.deepEqual(await reachableProxy(env, probe), first);
});

test('launch fails clearly when no proxy in the list answers', async () => {
  const { dir, cleanup } = await profileRoot();
  const saved = { ...process.env };
  try {
    Object.assign(process.env, { LIVE_BROWSER_PROXIES: POOL, CORTEX_TENANT_ID: '42' });
    await assert.rejects(launchCamoufox(dir, {}, { probe: async () => false,
      prepareOptions: () => { throw new Error('must not run'); } }), /No browser proxy is reachable/);
  } finally {
    for (const key of Object.keys(process.env)) if (!(key in saved)) delete process.env[key];
    Object.assign(process.env, saved);
    await cleanup();
  }
});

async function fakeProxy(onRequest) {
  const server = createServer(socket => {
    let request = '';
    socket.on('data', data => {
      request += data;
      if (request.includes('\r\n\r\n')) onRequest(request, socket);
    });
  });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  return { server: `http://127.0.0.1:${server.address().port}`, close: () => new Promise(resolve => server.close(resolve)) };
}

test('a proxy is usable only when it opens an authenticated tunnel in time', async () => {
  let seen = '';
  const open = await fakeProxy((request, socket) => { seen = request; socket.end('HTTP/1.1 200 Connection established\r\n\r\n'); });
  const denied = await fakeProxy((_request, socket) => socket.end('HTTP/1.1 407 Proxy Authentication Required\r\n\r\n'));
  const silent = await fakeProxy(() => {});
  try {
    assert.equal(await probeProxy({ server: open.server, username: 'u', password: 'p:w' }), true);
    assert.match(seen, /^CONNECT example\.com:443 HTTP\/1\.1\r\n/);
    assert.match(seen, new RegExp(`Proxy-Authorization: Basic ${Buffer.from('u:p:w').toString('base64')}\r\n`));
    assert.equal(await probeProxy({ server: denied.server }), false);
    assert.equal(await probeProxy({ server: silent.server }, { timeoutMs: 100 }), false);
    assert.equal(await probeProxy({ server: 'http://127.0.0.1:1' }), false);
    assert.equal(await probeProxy({ server: 'socks5://proxy:1080' }), true);
  } finally { await Promise.all([open.close(), denied.close(), silent.close()]); }
});
