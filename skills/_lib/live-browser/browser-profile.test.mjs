import assert from 'node:assert/strict';
import { test } from 'node:test';
import { applyProfileChanges, BrowserProfileStore, profileDiff, profileFromStorageState, profileOrigins, TOMBSTONE_TTL_MS } from './browser-profile.mjs';

const cookie = (name, value, domain = '.google.com') => ({ name, value, domain, path: '/', expires: -1, httpOnly: true, secure: true, sameSite: 'Lax' });

// In-memory Cortex profile routes with compare-and-swap on the revision.
function cortex() {
  const shared = { revision: 0, entries: [] };
  const calls = [];
  let interfere;
  const api = {
    async request(path, options) {
      calls.push(path.split('/').at(-1));
      assert.equal(options.grant, 'runtime-grant');
      if (path.endsWith('/load')) return structuredClone(shared);
      if (path.endsWith('/save')) {
        if (interfere) { const run = interfere; interfere = undefined; await run(); }
        if (options.body.baseRevision !== shared.revision) throw Object.assign(new Error('conflict'), { status: 409 });
        shared.entries = structuredClone(options.body.entries);
        shared.revision++;
        return { revision: shared.revision };
      }
      throw new Error('unexpected route');
    },
  };
  return { api, shared, calls, interfereOnce: run => { interfere = run; } };
}

// A fake persistent context: storageState reflects what the "page" holds.
function browser(initial = { cookies: [], origins: [] }) {
  const state = structuredClone(initial);
  const context = {
    seeded: undefined,
    async addCookies(cookies) { for (const c of cookies) { state.cookies = state.cookies.filter(x => x.name !== c.name || x.domain !== c.domain); state.cookies.push({ ...c }); } },
    async addInitScript(script) {
      assert.equal(typeof script, 'string');
      const storage = JSON.parse(script.slice(script.lastIndexOf(')(') + 2, -1));
      context.seeded = storage;
      for (const [origin, items] of Object.entries(storage)) {
        state.origins.push({ origin, localStorage: items.map(([name, value]) => ({ name, value })) });
      }
    },
    async storageState() { return structuredClone(state); },
  };
  return { context, state };
}

function task(host, time) {
  const b = browser();
  const store = new BrowserProfileStore({ api: host.api, session: { sessionId: 's1', grant: 'runtime-grant', tenantId: '7' }, now: () => time.value });
  return { ...b, store };
}

const values = shared => Object.fromEntries(shared.entries.map(e => [e.kind === 'cookie' ? e.name : `${e.origin}|${e.key}`, e.deleted ? 'DELETED' : e.value]));

test('a first task starts new and saves cookies and localStorage as keyed entries', async () => {
  const host = cortex();
  const time = { value: 1000 };
  const a = task(host, time);
  assert.deepEqual(await a.store.load(a.context), { state: 'new', origins: [] });
  a.state.cookies.push(cookie('SID', 'secret-1'));
  a.state.origins.push({ origin: 'https://mail.example.com', localStorage: [{ name: 'token', value: 'abc' }] });
  assert.deepEqual(await a.store.save(a.context), { state: 'shared', origins: ['google.com', 'mail.example.com'] });
  assert.deepEqual(values(host.shared), { SID: 'secret-1', 'https://mail.example.com|token': 'abc' });
  assert.ok(host.shared.entries.every(entry => entry.updatedAt === 1000));
  const before = host.calls.length;
  await a.store.save(a.context);
  assert.equal(host.calls.length, before, 'an unchanged profile is not saved again');
  const b = task(host, time);
  assert.deepEqual(await b.store.load(b.context), { state: 'shared', origins: ['google.com', 'mail.example.com'] });
  assert.equal(b.state.cookies[0].value, 'secret-1');
  assert.deepEqual(b.context.seeded, { 'https://mail.example.com': [['token', 'abc']] });
});

test('two concurrent tasks changing different keys both survive', async () => {
  const host = cortex();
  const time = { value: 1000 };
  const a = task(host, time), b = task(host, time);
  await a.store.load(a.context); await b.store.load(b.context);
  a.state.cookies.push(cookie('GOOGLE', 'a'));
  b.state.cookies.push(cookie('GITHUB', 'b', 'github.com'));
  time.value = 2000; await a.store.save(a.context);
  time.value = 3000; await b.store.save(b.context);
  assert.deepEqual(values(host.shared), { GOOGLE: 'a', GITHUB: 'b' });
});

test('the same key changed by two tasks keeps the newest write', async () => {
  const host = cortex();
  const time = { value: 1000 };
  const seed = task(host, time);
  await seed.store.load(seed.context);
  seed.state.cookies.push(cookie('SID', 'old'));
  await seed.store.save(seed.context);
  const a = task(host, time), b = task(host, time);
  await a.store.load(a.context); await b.store.load(b.context);
  a.state.cookies[0].value = 'from-a';
  b.state.cookies[0].value = 'from-b';
  // b changes later but saves first; a's older change must not overwrite it.
  time.value = 5000; const changesB = b.store.save(b.context);
  await changesB;
  time.value = 3000; await a.store.save(a.context);
  assert.deepEqual(values(host.shared), { SID: 'from-b' });
  time.value = 9000; a.state.cookies[0].value = 'newest';
  await a.store.save(a.context);
  assert.deepEqual(values(host.shared), { SID: 'newest' });
});

test('a delete versus an update resolves by time and keeps a short-lived tombstone', async () => {
  const host = cortex();
  const time = { value: 1000 };
  const seed = task(host, time);
  await seed.store.load(seed.context);
  seed.state.cookies.push(cookie('SID', 'v1'), cookie('PREF', 'p1'));
  await seed.store.save(seed.context);
  const a = task(host, time), b = task(host, time);
  await a.store.load(a.context); await b.store.load(b.context);
  // a signs out (deletes SID) later than b refreshes SID: the delete wins.
  a.state.cookies = a.state.cookies.filter(c => c.name !== 'SID');
  b.state.cookies.find(c => c.name === 'SID').value = 'v2';
  time.value = 2000; await b.store.save(b.context);
  time.value = 3000; await a.store.save(a.context);
  assert.deepEqual(values(host.shared), { SID: 'DELETED', PREF: 'p1' });
  assert.deepEqual(profileOrigins(host.shared.entries), ['google.com']);
  // A newer update resurrects the key over the tombstone.
  b.state.cookies.find(c => c.name === 'SID').value = 'v3';
  time.value = 4000; await b.store.save(b.context);
  assert.deepEqual(values(host.shared), { SID: 'v3', PREF: 'p1' });
  // A fresh task never receives deleted entries.
  const c = task(host, time);
  host.shared.entries.find(e => e.name === 'SID').deleted = true;
  await c.store.load(c.context);
  assert.deepEqual(c.state.cookies.map(x => x.name), ['PREF']);
});

test('a save that loses a compare-and-swap race reloads and re-applies only its own diff', async () => {
  const host = cortex();
  const time = { value: 1000 };
  const a = task(host, time);
  await a.store.load(a.context);
  a.state.cookies.push(cookie('MINE', 'a'));
  host.interfereOnce(async () => {
    host.shared.entries.push({ kind: 'cookie', ...cookie('THEIRS', 't'), updatedAt: 1500 });
    host.shared.revision++;
  });
  time.value = 2000;
  await a.store.save(a.context);
  assert.deepEqual(values(host.shared), { THEIRS: 't', MINE: 'a' });
  assert.deepEqual(host.calls.filter(call => call !== 'load').length, 2);
});

test('tombstones expire and diffs never carry unchanged values', () => {
  const loaded = profileFromStorageState({ cookies: [cookie('A', '1'), cookie('B', '2')] });
  const current = profileFromStorageState({ cookies: [cookie('A', '1'), cookie('C', '3')],
    origins: [{ origin: 'https://x.example.com', localStorage: [{ name: '__axiom_profile_seeded', value: '1' }] }] });
  const diff = profileDiff(loaded, current, TOMBSTONE_TTL_MS);
  assert.deepEqual(diff.map(d => [d.name, d.deleted === true, d.value]), [['C', false, '3'], ['B', true, undefined]]);
  const merged = applyProfileChanges([{ kind: 'cookie', domain: '.g.com', path: '/', name: 'Z', deleted: true, updatedAt: 0 }], diff, TOMBSTONE_TTL_MS + 100);
  assert.deepEqual(merged.map(e => e.name).sort(), ['B', 'C']);
  assert.equal(profileFromStorageState({ origins: [{ origin: 'javascript:alert(1)', localStorage: [{ name: 'a', value: 'b' }] }] }).size, 0);
});
