import assert from 'node:assert/strict';
import { test } from 'node:test';
import { EventEmitter } from 'node:events';
import { BrowserProfileStore, profileChanges, profileFromStorageState, siteOf } from './browser-profile.mjs';

const cookie = (name, value, domain = '.google.com') => ({ name, value, domain, path: '/', expires: -1, httpOnly: true, secure: true, sameSite: 'Lax' });

// In-memory model of Cortex's profile routes: the server merges per key,
// last writer wins by updatedAt, deletions are tombstones. It records the
// first-party sites sessions report; a legacy profile has no site list.
function cortex({ legacy = false } = {}) {
  const state = new Map();
  let sites = legacy ? undefined : new Set();
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
      return { cookies: live.filter(e => e.name !== undefined), storage: live.filter(e => e.name === undefined),
        ...(sites ? { sites: [...sites].sort() } : {}), loadedAt: new Date().toISOString() };
    },
    async saveProfileChanges(_session, grant, changes) {
      calls.push(['changes', grant, structuredClone(changes)]);
      if (forgotten) throw Object.assign(new Error('gone'), { status: 410 });
      sites = new Set([...(sites ?? []), ...(changes.sites ?? [])]);
      for (const entry of [...changes.cookies, ...changes.storage]) {
        const current = state.get(key(entry));
        if (!current || Date.parse(entry.updatedAt) >= Date.parse(current.updatedAt)) state.set(key(entry), structuredClone(entry));
      }
      const origins = [...new Set([...state.values()].filter(e => !e.deleted).map(e => (e.domain ?? new URL(e.origin).hostname).replace(/^\./, '')))].sort();
      return { origins };
    },
  };
  return { api, state, calls, sites: () => sites && [...sites].sort(), forget: () => { forgotten = true; state.clear(); } };
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

// A task whose pages navigated (top level) to these URLs.
function task(host, time, visits = ['https://accounts.google.com/', 'https://github.com/login', 'https://mail.example.com/']) {
  const b = browser();
  const store = new BrowserProfileStore({ api: host.api, session: { sessionId: 's1', grant: 'session-grant', tenantId: '7' }, now: () => time.value });
  for (const url of visits) store.visit(url);
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
    sites: ['example.com', 'github.com', 'google.com'],
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

test('sites are registrable domains (eTLD+1), private suffixes included', () => {
  assert.equal(siteOf('https://old.reddit.com/r/x'), 'reddit.com');
  assert.equal(siteOf('.reddit.com'), 'reddit.com');
  assert.equal(siteOf('www.bbc.co.uk'), 'bbc.co.uk');
  assert.equal(siteOf('https://alice.github.io/'), 'alice.github.io');
  assert.equal(siteOf('http://127.0.0.1:8080/'), '127.0.0.1');
  assert.equal(siteOf('localhost'), 'localhost');
  assert.equal(siteOf(''), undefined);
});

test('only first-party state of visited sites is saved; tracker cookies are not', async () => {
  const host = cortex();
  const time = { value: Date.parse('2026-10-07T10:00:00Z') };
  const a = task(host, time, ['https://www.reddit.com/r/aww/comments/1']);
  await a.store.load(a.context);
  a.state.cookies.push(cookie('reddit_session', 'r', '.reddit.com'), cookie('token_v2', 't', 'www.reddit.com'),
    cookie('tvid', 'ad', '.1rx.io'), cookie('uid', 'ad', '.33across.com'), cookie('PugT', 'ad', '.360yield.com'));
  a.state.origins.push({ origin: 'https://www.reddit.com', localStorage: [{ name: 'prefs', value: 'p' }] },
    { origin: 'https://ads.360yield.com', localStorage: [{ name: 'id', value: 'ad' }] });
  const saved = await a.store.save(a.context);
  const sent = host.calls.at(-1)[2];
  assert.deepEqual(sent.cookies.map(c => c.domain), ['.reddit.com', 'www.reddit.com']);
  assert.deepEqual(sent.storage.map(e => e.origin), ['https://www.reddit.com']);
  assert.deepEqual(sent.sites, ['reddit.com']);
  assert.deepEqual(saved.origins, ['reddit.com', 'www.reddit.com']);
  assert.deepEqual(host.sites(), ['reddit.com']);
});

test('a legacy profile is cleaned once: third-party entries are tombstoned, first-party ones kept', async () => {
  const host = cortex({ legacy: true });
  const time = { value: 1000000 };
  // Saved before first-party capture: a real sign-in plus tracker leftovers.
  for (const entry of [{ ...cookie('SID', 'g'), updatedAt: new Date(0).toISOString() }, { ...cookie('tvid', 'ad', '.1rx.io'), updatedAt: new Date(0).toISOString() },
    { origin: 'https://ads.360yield.com', key: 'id', value: 'ad', updatedAt: new Date(0).toISOString() }]) {
    host.state.set(entry.name ? `c|${entry.domain}|/|${entry.name}` : `s|${entry.origin}|${entry.key}`, entry);
  }
  const a = task(host, time, ['https://accounts.google.com/']);
  await a.store.load(a.context);
  time.value += 1000;
  await a.store.save(a.context);
  const sent = host.calls.at(-1)[2];
  assert.deepEqual(sent.cookies, [{ domain: '.1rx.io', path: '/', name: 'tvid', updatedAt: new Date(time.value).toISOString(), deleted: true }]);
  assert.deepEqual(sent.storage, [{ origin: 'https://ads.360yield.com', key: 'id', updatedAt: new Date(time.value).toISOString(), deleted: true }]);
  assert.deepEqual(sent.sites, ['google.com']);
  assert.deepEqual(values(host), { SID: 'g', tvid: 'DELETED', 'https://ads.360yield.com|id': 'DELETED' });
  // Later tasks keep known first-party sign-ins of sites they never visit.
  const b = task(host, time, ['https://github.com/']);
  await b.store.load(b.context);
  b.state.cookies.push(cookie('user_session', 'gh', 'github.com'));
  time.value += 1000;
  await b.store.save(b.context);
  assert.deepEqual(host.calls.at(-1)[2].cookies.map(c => c.name), ['user_session']);
  assert.equal(values(host).SID, 'g');
  assert.deepEqual(host.sites(), ['github.com', 'google.com']);
});

test('track records top-level navigations of every page, not iframes', () => {
  const host = cortex();
  const store = new BrowserProfileStore({ api: host.api, session: { sessionId: 's1', grant: 'g', tenantId: '7' } });
  const page = (url) => {
    const p = new EventEmitter();
    const main = { url: () => url };
    p.url = () => url; p.mainFrame = () => main;
    p.navigate = next => { url = next; p.emit('framenavigated', main); };
    p.frame = next => p.emit('framenavigated', { url: () => next });
    return p;
  };
  const context = new EventEmitter();
  const first = page('about:blank');
  context.pages = () => [first];
  store.track(context);
  first.navigate('https://www.reddit.com/r/aww');
  first.frame('https://ads.1rx.io/frame');
  const popup = page('https://accounts.google.com/o/oauth2');
  context.emit('page', popup);
  popup.navigate('https://accounts.google.com/signin');
  assert.deepEqual(store.visitedSites, ['google.com', 'reddit.com']);
});
