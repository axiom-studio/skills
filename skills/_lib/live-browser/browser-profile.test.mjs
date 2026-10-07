import assert from 'node:assert/strict';
import { test } from 'node:test';
import { BrowserProfileStore, profileChanges, profileFromStorageState } from './browser-profile.mjs';

const cookie = (name, value, domain = '.google.com') => ({ name, value, domain, path: '/', expires: -1, httpOnly: true, secure: true, sameSite: 'Lax' });

// In-memory model of Cortex's profile routes: the server merges per key,
// last writer wins by updatedAt, deletions are tombstones.
function cortex() {
  const state = new Map();
  const calls = [];
  let forgotten = false;
  const key = entry => entry.name !== undefined ? `c|${entry.domain}|${entry.path}|${entry.name}` : `s|${entry.origin}|${entry.key}`;
  const api = {
    async profileGrant(session) {
      calls.push(['grant', session.grant]);
      return { profile: 'default', state: state.size ? 'shared' : 'new', origins: [], grant: 'profile-grant', expiresAt: new Date(Date.now() + 600000).toISOString() };
    },
    async loadProfile(_session, grant) {
      calls.push(['load', grant]);
      const live = [...state.values()].filter(entry => !entry.deleted);
      return { cookies: live.filter(e => e.name !== undefined), storage: live.filter(e => e.name === undefined), loadedAt: new Date().toISOString() };
    },
    async saveProfileChanges(_session, grant, changes) {
      calls.push(['changes', grant, structuredClone(changes)]);
      if (forgotten) throw Object.assign(new Error('gone'), { status: 410 });
      for (const entry of [...changes.cookies, ...changes.storage]) {
        const current = state.get(key(entry));
        if (!current || Date.parse(entry.updatedAt) >= Date.parse(current.updatedAt)) state.set(key(entry), structuredClone(entry));
      }
      const origins = [...new Set([...state.values()].filter(e => !e.deleted).map(e => (e.domain ?? new URL(e.origin).hostname).replace(/^\./, '')))].sort();
      return { origins };
    },
  };
  return { api, state, calls, forget: () => { forgotten = true; state.clear(); } };
}

function browser() {
  const state = { cookies: [], origins: [] };
  const context = {
    seeded: undefined,
    async addCookies(cookies) { state.cookies.push(...structuredClone(cookies)); },
    async addInitScript(script) {
      assert.equal(typeof script, 'string');
      context.seeded = JSON.parse(script.slice(script.lastIndexOf(')(') + 2, -1));
      for (const [origin, items] of Object.entries(context.seeded)) state.origins.push({ origin, localStorage: items.map(([name, value]) => ({ name, value })) });
    },
    async storageState() { return structuredClone(state); },
  };
  return { context, state };
}

function task(host, time) {
  const b = browser();
  const store = new BrowserProfileStore({ api: host.api, session: { sessionId: 's1', grant: 'session-grant', tenantId: '7' }, now: () => time.value });
  return { ...b, store };
}

const values = host => Object.fromEntries([...host.state.values()].map(e => [e.name ?? `${e.origin}|${e.key}`, e.deleted ? 'DELETED' : e.value]));

test('a first task starts new and sends only its changes in the Cortex wire shape', async () => {
  const host = cortex();
  const time = { value: Date.parse('2026-10-07T10:00:00Z') };
  const a = task(host, time);
  assert.deepEqual(await a.store.load(a.context), { state: 'new', origins: [] });
  assert.deepEqual(host.calls.slice(0, 2), [['grant', 'session-grant'], ['load', 'profile-grant']]);
  a.state.cookies.push(cookie('SID', 'secret-1'));
  a.state.origins.push({ origin: 'https://mail.example.com', localStorage: [{ name: 'token', value: 'abc' }] });
  assert.deepEqual(await a.store.save(a.context), { state: 'shared', origins: ['google.com', 'mail.example.com'] });
  const sent = host.calls.at(-1)[2];
  assert.deepEqual(sent, {
    cookies: [{ domain: '.google.com', path: '/', name: 'SID', value: 'secret-1', expires: -1, httpOnly: true, secure: true, sameSite: 'Lax', updatedAt: '2026-10-07T10:00:00.000Z' }],
    storage: [{ origin: 'https://mail.example.com', key: 'token', value: 'abc', updatedAt: '2026-10-07T10:00:00.000Z' }],
  });
  const count = host.calls.length;
  await a.store.save(a.context);
  assert.equal(host.calls.length, count, 'an unchanged profile is not sent again');
  const b = task(host, time);
  assert.deepEqual(await b.store.load(b.context), { state: 'shared', origins: [] });
  assert.equal(b.state.cookies[0].value, 'secret-1');
  assert.deepEqual(b.context.seeded, { 'https://mail.example.com': [['token', 'abc']] });
});

test('two concurrent tasks changing different keys both survive', async () => {
  const host = cortex();
  const time = { value: 1000000 };
  const a = task(host, time), b = task(host, time);
  await a.store.load(a.context); await b.store.load(b.context);
  a.state.cookies.push(cookie('GOOGLE', 'a'));
  b.state.cookies.push(cookie('GITHUB', 'b', 'github.com'));
  time.value += 1000; await a.store.save(a.context);
  time.value += 1000; await b.store.save(b.context);
  assert.deepEqual(values(host), { GOOGLE: 'a', GITHUB: 'b' });
  assert.equal(host.calls.at(-1)[2].cookies.length, 1, 'b sends only its own change');
});

test('the same key changed by two tasks keeps the newest write', async () => {
  const host = cortex();
  const time = { value: 1000000 };
  const seed = task(host, time);
  await seed.store.load(seed.context);
  seed.state.cookies.push(cookie('SID', 'old'));
  await seed.store.save(seed.context);
  const a = task(host, time), b = task(host, time);
  await a.store.load(a.context); await b.store.load(b.context);
  a.state.cookies[0].value = 'from-a';
  b.state.cookies[0].value = 'from-b';
  time.value = 5000000; await b.store.save(b.context);
  time.value = 3000000; await a.store.save(a.context);
  assert.deepEqual(values(host), { SID: 'from-b' });
});

test('a delete versus an update resolves by time', async () => {
  const host = cortex();
  const time = { value: 1000000 };
  const seed = task(host, time);
  await seed.store.load(seed.context);
  seed.state.cookies.push(cookie('SID', 'v1'), cookie('PREF', 'p1'));
  await seed.store.save(seed.context);
  const a = task(host, time), b = task(host, time);
  await a.store.load(a.context); await b.store.load(b.context);
  a.state.cookies = a.state.cookies.filter(c => c.name !== 'SID');
  b.state.cookies.find(c => c.name === 'SID').value = 'v2';
  time.value = 2000000; await b.store.save(b.context);
  time.value = 3000000; await a.store.save(a.context);
  assert.deepEqual(values(host), { SID: 'DELETED', PREF: 'p1' });
  assert.deepEqual(host.calls.at(-1)[2].cookies, [{ domain: '.google.com', path: '/', name: 'SID', updatedAt: new Date(3000000).toISOString(), deleted: true }]);
  b.state.cookies.find(c => c.name === 'SID').value = 'v3';
  time.value = 4000000; await b.store.save(b.context);
  assert.deepEqual(values(host), { SID: 'v3', PREF: 'p1' });
  const c = task(host, time);
  [...host.state.values()].find(e => e.name === 'SID').deleted = true;
  await c.store.load(c.context);
  assert.deepEqual(c.state.cookies.map(x => x.name), ['PREF']);
});

test('a forgotten profile (410) is never re-uploaded; a busy one (409) reports and retries later', async () => {
  const host = cortex();
  const time = { value: 1000000 };
  const a = task(host, time);
  await a.store.load(a.context);
  a.state.cookies.push(cookie('SID', 'x'));
  host.forget();
  assert.deepEqual(await a.store.save(a.context), { state: 'new', origins: [] });
  const count = host.calls.length;
  a.state.cookies.push(cookie('MORE', 'y'));
  await a.store.save(a.context);
  assert.equal(host.calls.length, count);
  const busy = cortex();
  busy.api.saveProfileChanges = async () => { throw Object.assign(new Error('busy'), { status: 409 }); };
  const b = task(busy, time);
  await b.store.load(b.context);
  b.state.cookies.push(cookie('SID', 'x'));
  await assert.rejects(b.store.save(b.context), /busy/);
});

test('session cookies and the seeding marker are normalized out of diffs', () => {
  const loaded = profileFromStorageState({ cookies: [{ ...cookie('A', '1'), expires: 0 }] });
  const current = profileFromStorageState({ cookies: [cookie('A', '1')],
    origins: [{ origin: 'https://x.example.com', localStorage: [{ name: '__axiom_profile_seeded', value: '1' }] }] });
  assert.deepEqual(profileChanges(loaded, current, 0), { cookies: [], storage: [] });
  assert.equal(profileFromStorageState({ origins: [{ origin: 'javascript:alert(1)', localStorage: [{ name: 'a', value: 'b' }] }] }).size, 0);
});
