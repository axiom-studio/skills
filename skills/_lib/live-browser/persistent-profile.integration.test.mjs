// Real Camoufox: the tenant profile on disk survives a browser restart, a
// crashed browser's stale locks, and keeps its fingerprint. Runs when the
// pinned Camoufox build is installed (CAMOUFOX_INSTALL_DIR or camoufox-js's
// default directory); skipped otherwise.
import test from 'node:test';
import assert from 'node:assert/strict';
import { existsSync } from 'node:fs';
import { mkdtemp, rm, symlink, writeFile } from 'node:fs/promises';
import { createServer } from 'node:http';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { INSTALL_DIR } from 'camoufox-js/dist/pkgman.js';
import { launchCamoufox } from './camoufox-browser.mjs';
import { PersistentBrowserProfile } from './browser-profile.mjs';

const installed = existsSync(join(String(INSTALL_DIR), 'version.json'));

// Writes state with every kind of storage a signed-in web app uses. Values
// are checked inside the page: Camoufox's isolated world cannot return typed
// arrays across Xrays.
const WRITE = async () => {
  document.cookie = 'sid=signed-in; Max-Age=86400; Path=/; SameSite=Lax';
  localStorage.setItem('settings', '{"theme":"dark"}');
  const key = await crypto.subtle.generateKey({ name: 'AES-GCM', length: 256 }, false, ['encrypt', 'decrypt']);
  const iv = new Uint8Array(12);
  const sealed = await crypto.subtle.encrypt({ name: 'AES-GCM', iv }, key, new TextEncoder().encode('relink-not-needed'));
  await new Promise((resolve, reject) => {
    const open = indexedDB.open('auth', 1);
    open.onupgradeneeded = () => open.result.createObjectStore('keys');
    open.onerror = () => reject(open.error);
    open.onsuccess = () => {
      const tx = open.result.transaction('keys', 'readwrite');
      tx.objectStore('keys').put({ key, sealed, blob: new Blob(['blob-bytes']), at: new Date(1700000000000) }, 'device');
      tx.oncomplete = () => { open.result.close(); resolve(); };
      tx.onerror = () => reject(tx.error);
    };
  });
  const cache = await caches.open('app-shell');
  await cache.put('/app.js', new Response('console.log("cached")', { headers: { 'Content-Type': 'text/javascript' } }));
  return navigator.userAgent + '|' + navigator.hardwareConcurrency + '|' + screen.width;
};

const READ = async () => {
  const record = await new Promise((resolve, reject) => {
    const open = indexedDB.open('auth', 1);
    open.onerror = () => reject(open.error);
    open.onsuccess = () => {
      const get = open.result.transaction('keys').objectStore('keys').get('device');
      get.onsuccess = () => resolve(get.result);
      get.onerror = () => reject(get.error);
    };
  });
  const plain = await crypto.subtle.decrypt({ name: 'AES-GCM', iv: new Uint8Array(12) }, record.key, record.sealed);
  const cached = await (await caches.open('app-shell')).match('/app.js');
  return {
    cookie: document.cookie,
    settings: localStorage.getItem('settings'),
    keyExtractable: record.key.extractable,
    decrypted: new TextDecoder().decode(plain),
    blob: await record.blob.text(),
    at: record.at.getTime(),
    cache: cached ? await cached.text() : null,
    fingerprint: navigator.userAgent + '|' + navigator.hardwareConcurrency + '|' + screen.width,
  };
};

test('a real Camoufox profile keeps cookies, storage, IndexedDB keys and Cache Storage across restarts', {
  skip: !installed && 'Camoufox is not installed', timeout: 180000,
}, async () => {
  const root = await mkdtemp(join(tmpdir(), 'live-browser-profile-it-'));
  // The server never sets cookies or storage: everything must come from disk.
  const server = createServer((_req, res) => { res.setHeader('Content-Type', 'text/html'); res.end('<!doctype html><title>app</title>'); });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  const url = `http://127.0.0.1:${server.address().port}/`;
  const profile = new PersistentBrowserProfile({ root });
  let context;
  try {
    await profile.load();
    context = await launchCamoufox(root);
    profile.track(context);
    let page = context.pages()[0] ?? await context.newPage();
    await page.goto(url);
    const fingerprint = await page.evaluate(WRITE);
    await context.close();
    context = undefined;
    await profile.flush();

    // A crash or another pod leaves Firefox's locks behind.
    await rm(join(root, 'camoufox', 'lock'), { force: true });
    await symlink('10.42.9.9:+31337', join(root, 'camoufox', 'lock'));
    await writeFile(join(root, 'camoufox', '.parentlock'), '');

    context = await launchCamoufox(root);
    page = context.pages()[0] ?? await context.newPage();
    await page.goto(url);
    const restored = await page.evaluate(READ);
    assert.deepEqual(restored, { cookie: 'sid=signed-in', settings: '{"theme":"dark"}', keyExtractable: false,
      decrypted: 'relink-not-needed', blob: 'blob-bytes', at: 1700000000000, cache: 'console.log("cached")', fingerprint });
    await context.close();
    context = undefined;
    // History is kept too (places.sqlite, readable once Firefox has exited).
    const { DatabaseSync } = await import('node:sqlite');
    const places = new DatabaseSync(join(root, 'camoufox', 'places.sqlite'), { readOnly: true });
    try {
      const row = places.prepare('SELECT visit_count FROM moz_places WHERE url = ?').get(url);
      assert.ok(row && row.visit_count >= 2, 'both sessions\' visits are in the history');
    } finally { places.close(); }
    assert.deepEqual((await profile.details()).origins, ['127.0.0.1']);
  } finally {
    await context?.close().catch(() => {});
    server.close();
    await rm(root, { recursive: true, force: true });
  }
});
