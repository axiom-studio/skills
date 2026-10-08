// The tenant's browser profile: one real Camoufox (Firefox) profile directory
// on the runtime's persistent volume. Everything a normal browser keeps stays
// in it between sessions: first- and third-party cookies, localStorage,
// IndexedDB (including non-extractable CryptoKeys, e.g. WhatsApp Web's),
// Cache Storage, service workers, history, permissions, site settings and
// saved logins. Nothing is exported or uploaded; Firefox itself owns the files
// and flushes them when the browser closes.
//
// Firefox can open a profile only once, so the profile is an exclusive lease:
// one live browser at a time per tenant runtime, later starts wait in FIFO
// order (bounded) for the current browser to close.
//
// The "saved sign-ins" list is the set of top-level sites (eTLD+1) the
// browser has visited, persisted next to the profile. Third-party sites whose
// cookies the browser keeps never appear in it.
//
// Never log URLs, cookie or storage values, or keys.

import { mkdir, readFile, rename, rm, stat, readdir, writeFile, lstat } from 'node:fs/promises';
import { join } from 'node:path';
import { getDomain, getHostname } from 'tldts';

export const PROFILE_ROOT = '/var/lib/axiom-live-browser';
export const PROFILE_WAIT_MS = 120000;
const BROWSER_DIR = 'camoufox';
const SITES_FILE = 'sites.json';
// The launch fingerprint kept with the profile (see camoufox-browser.mjs).
export const IDENTITY_FILE = 'identity.json';
// Everything this runtime keeps on the volume. Forget removes exactly these.
const PROFILE_FILES = [BROWSER_DIR, SITES_FILE, `${SITES_FILE}.tmp`, IDENTITY_FILE, `${IDENTITY_FILE}.tmp`];
const MAX_SITES = 500;
const SITES_WRITE_DELAY_MS = 2000;

// Registrable domain (eTLD+1, private suffixes included, as browsers define a
// "site"); the host itself for IP addresses and single-label hosts.
export function siteOf(value) {
  if (typeof value !== 'string' || !value) return undefined;
  let host;
  if (/^https?:\/\//i.test(value)) {
    try { host = new URL(value).hostname; } catch { return undefined; }
  } else host = getHostname(value.replace(/^\./, ''));
  host = host?.replace(/^\[|\]$/g, '').replace(/\.$/, '').toLowerCase();
  if (!host) return undefined;
  return getDomain(host, { allowPrivateDomains: true }) ?? host;
}

export class BrowserProfileBusyError extends Error {
  constructor() {
    super('The live browser is in use by another task. Try again when that browser is closed.');
    this.name = 'BrowserProfileBusyError';
    this.expose = true;
  }
}

async function directorySize(path) {
  let total = 0;
  let entries;
  try { entries = await readdir(path, { withFileTypes: true }); } catch { return 0; }
  for (const entry of entries) {
    const child = join(path, entry.name);
    if (entry.isDirectory()) total += await directorySize(child);
    else if (entry.isFile()) total += await stat(child).then(info => info.size, () => 0);
  }
  return total;
}

export class PersistentBrowserProfile {
  #root; #now; #sites = new Map(); #updatedAt; #loading; #waiters = []; #holder; #timer; #writing = Promise.resolve();

  constructor({ root = PROFILE_ROOT, now = Date.now } = {}) {
    if (typeof root !== 'string' || !root.startsWith('/')) throw new Error('Browser profile root must be an absolute path');
    this.#root = root;
    this.#now = now;
  }

  // Passed to launchCamoufox, which keeps the Firefox profile in <root>/camoufox.
  get root() { return this.#root; }

  get browserDir() { return join(this.#root, BROWSER_DIR); }

  get busy() { return Boolean(this.#holder); }

  // Loads the visited-site list once. A missing or unreadable list is empty.
  load() {
    this.#loading ??= (async () => {
      await mkdir(this.#root, { recursive: true, mode: 0o700 });
      try {
        const saved = JSON.parse(await readFile(join(this.#root, SITES_FILE), 'utf8'));
        for (const [site, at] of Object.entries(saved?.sites ?? {})) {
          if (siteOf(site) === site && Number.isFinite(Date.parse(at)) && this.#sites.size < MAX_SITES) this.#sites.set(site, at);
        }
        if (Number.isFinite(Date.parse(saved?.updatedAt))) this.#updatedAt = saved.updatedAt;
      } catch { /* none yet */ }
    })();
    return this.#loading;
  }

  // Exclusive use of the profile. Resolves with a release function; rejects
  // with BrowserProfileBusyError after timeoutMs in the queue. `first` jumps
  // the queue (forgetting the profile goes before queued browser starts).
  acquire(timeoutMs = PROFILE_WAIT_MS, { first = false } = {}) {
    return new Promise((resolve, reject) => {
      const waiter = {};
      const timer = setTimeout(() => {
        this.#waiters = this.#waiters.filter(entry => entry !== waiter);
        reject(new BrowserProfileBusyError());
      }, timeoutMs);
      timer.unref?.();
      waiter.grant = () => { clearTimeout(timer); resolve(this.#releaser(waiter)); };
      waiter.cancel = () => { clearTimeout(timer); reject(new BrowserProfileBusyError()); };
      if (first) this.#waiters.unshift(waiter);
      else this.#waiters.push(waiter);
      this.#next();
    });
  }

  #next() {
    if (this.#holder || !this.#waiters.length) return;
    this.#holder = this.#waiters.shift();
    this.#holder.grant();
  }

  #releaser(waiter) {
    let released = false;
    return () => {
      if (released) return;
      released = true;
      if (this.#holder === waiter) this.#holder = undefined;
      this.#next();
    };
  }

  // Shutdown: nobody new gets the profile.
  cancelWaiters() {
    const waiters = this.#waiters;
    this.#waiters = [];
    for (const waiter of waiters) waiter.cancel();
  }

  // Records a top-level navigation (agent or human, including sign-in
  // redirects and popups). Only http(s) pages count.
  visit(url) {
    if (typeof url !== 'string' || !/^https?:/i.test(url)) return;
    const site = siteOf(url);
    if (!site || (!this.#sites.has(site) && this.#sites.size >= MAX_SITES)) return;
    const at = new Date(this.#now()).toISOString();
    const known = this.#sites.has(site);
    this.#sites.set(site, at);
    this.#updatedAt = at;
    // New sites are written promptly; revisits ride along with the next write.
    if (!known) this.#schedule();
  }

  // Watches every page of the context for main-frame navigations.
  track(context) {
    const watch = page => {
      try { this.visit(page.url()); } catch { /* not navigated yet */ }
      page.on?.('framenavigated', frame => {
        try { if (frame === page.mainFrame()) this.visit(frame.url()); } catch { /* page closed */ }
      });
    };
    for (const page of context.pages?.() ?? []) watch(page);
    context.on?.('page', watch);
  }

  #schedule() {
    if (this.#timer) return;
    this.#timer = setTimeout(() => { this.#timer = undefined; void this.flush().catch(() => {}); }, SITES_WRITE_DELAY_MS);
    this.#timer.unref?.();
  }

  // Writes the visited-site list atomically (temp file + rename).
  flush() {
    clearTimeout(this.#timer);
    this.#timer = undefined;
    this.#writing = this.#writing.catch(() => {}).then(async () => {
      await mkdir(this.#root, { recursive: true, mode: 0o700 });
      const file = join(this.#root, SITES_FILE);
      const body = JSON.stringify({ version: 1, updatedAt: this.#updatedAt ?? null, sites: Object.fromEntries([...this.#sites].sort()) });
      await writeFile(`${file}.tmp`, body, { mode: 0o600 });
      await rename(`${file}.tmp`, file);
    });
    return this.#writing;
  }

  get origins() { return [...this.#sites.keys()].sort(); }

  // Session status projection (`profile` in browser status).
  get status() { return { state: this.#sites.size ? 'shared' : 'new', origins: this.origins }; }

  // The conversation's "saved sign-ins" view.
  async details() {
    await this.load();
    if (!this.#sites.size && !(await lstat(this.browserDir).then(() => true, () => false))) {
      return { state: 'none', origins: [], updatedAt: null, sizeBytes: 0 };
    }
    return { state: this.#sites.size ? 'shared' : 'none', origins: this.origins, updatedAt: this.#updatedAt ?? null,
      sizeBytes: await directorySize(this.#root) };
  }

  // Deletes everything the browser kept. The caller must hold the lease, so
  // no browser has the profile open.
  async forget() {
    clearTimeout(this.#timer);
    this.#timer = undefined;
    await this.#writing.catch(() => {});
    this.#sites.clear();
    this.#updatedAt = undefined;
    await mkdir(this.#root, { recursive: true, mode: 0o700 });
    for (const name of PROFILE_FILES) await rm(join(this.#root, name), { recursive: true, force: true });
  }
}
