import assert from 'node:assert/strict';
import { test } from 'node:test';
import { EventEmitter } from 'node:events';
import { mkdtemp, mkdir, readdir, readFile, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { BrowserProfileBusyError, PersistentBrowserProfile, siteOf } from './browser-profile.mjs';

async function root() {
  const dir = await mkdtemp(join(tmpdir(), 'profile-test-'));
  return { dir, cleanup: () => rm(dir, { recursive: true, force: true }) };
}

function page(url = 'about:blank') {
  const p = new EventEmitter();
  const main = { url: () => p.current };
  p.current = url;
  p.url = () => p.current;
  p.mainFrame = () => main;
  p.go = target => { p.current = target; p.emit('framenavigated', main); };
  p.frame = target => p.emit('framenavigated', { url: () => target });
  return p;
}

test('sites are registrable domains', () => {
  assert.equal(siteOf('https://old.reddit.com/r/x'), 'reddit.com');
  assert.equal(siteOf('.reddit.com'), 'reddit.com');
  assert.equal(siteOf('www.bbc.co.uk'), 'bbc.co.uk');
  assert.equal(siteOf('https://alice.github.io/'), 'alice.github.io');
  assert.equal(siteOf('http://127.0.0.1:8080/'), '127.0.0.1');
  assert.equal(siteOf('localhost'), 'localhost');
  assert.equal(siteOf(''), undefined);
});

test('the saved sign-ins list holds only top-level sites the browser visited and persists on the volume', async () => {
  const { dir, cleanup } = await root();
  try {
    let now = Date.parse('2026-10-09T10:00:00Z');
    const profile = new PersistentBrowserProfile({ root: dir, now: () => now });
    await profile.load();
    assert.deepEqual(await profile.details(), { state: 'none', origins: [], updatedAt: null, sizeBytes: 0 });
    assert.deepEqual(profile.status, { state: 'new', origins: [] });
    const context = new EventEmitter();
    const first = page('https://web.whatsapp.com/');
    context.pages = () => [first];
    profile.track(context);
    first.frame('https://doubleclick.net/ad');                // third-party iframe: never listed
    first.go('https://accounts.google.com/signin');
    const popup = page();
    context.emit('page', popup);
    now += 1000;
    popup.go('https://www.github.com/login');
    profile.visit('file:///etc/passwd');
    profile.visit('about:blank');
    await profile.flush();
    assert.deepEqual(profile.status, { state: 'shared', origins: ['github.com', 'google.com', 'whatsapp.com'] });
    // The browser's own files count towards the size.
    await mkdir(join(dir, 'camoufox'));
    await writeFile(join(dir, 'camoufox', 'cookies.sqlite'), Buffer.alloc(1000));
    const reopened = new PersistentBrowserProfile({ root: dir });
    const details = await reopened.details();
    assert.deepEqual(details.origins, ['github.com', 'google.com', 'whatsapp.com']);
    assert.equal(details.state, 'shared');
    assert.equal(details.updatedAt, '2026-10-09T10:00:01.000Z');
    assert.ok(details.sizeBytes >= 1000);
    assert.doesNotMatch(await readFile(join(dir, 'sites.json'), 'utf8'), /signin|login|doubleclick/, 'only sites, never URLs');
  } finally { await cleanup(); }
});

test('the profile is an exclusive FIFO lease with a bounded wait', async () => {
  const profile = new PersistentBrowserProfile({ root: '/unused' });
  const order = [];
  const releaseA = await profile.acquire(1000);
  assert.equal(profile.busy, true);
  const b = profile.acquire(1000).then(release => { order.push('b'); return release; });
  const c = profile.acquire(1000).then(release => { order.push('c'); return release; });
  await assert.rejects(profile.acquire(20), BrowserProfileBusyError);
  await new Promise(resolve => setTimeout(resolve, 10));
  assert.deepEqual(order, []);
  releaseA();
  releaseA(); // idempotent: never releases someone else's lease
  const releaseB = await b;
  assert.deepEqual(order, ['b']);
  releaseB();
  (await c)();
  assert.deepEqual(order, ['b', 'c']);
  assert.equal(profile.busy, false);
  const held = await profile.acquire(1000);
  const waiting = profile.acquire(10000);
  profile.cancelWaiters();
  await assert.rejects(waiting, /in use by another task/);
  held();
});

test('forget removes the browser profile, the site list and the fingerprint, and nothing else on the volume', async () => {
  const { dir, cleanup } = await root();
  try {
    const profile = new PersistentBrowserProfile({ root: dir });
    await profile.load();
    profile.visit('https://web.whatsapp.com/');
    await profile.flush();
    await mkdir(join(dir, 'camoufox', 'storage', 'default'), { recursive: true });
    await writeFile(join(dir, 'camoufox', 'cookies.sqlite'), 'x');
    await writeFile(join(dir, 'identity.json'), '{}');
    await mkdir(join(dir, 'lost+found'));
    await profile.forget();
    assert.deepEqual(await readdir(dir), ['lost+found']);
    assert.deepEqual(profile.status, { state: 'new', origins: [] });
    assert.equal((await profile.details()).state, 'none');
  } finally { await cleanup(); }
});
