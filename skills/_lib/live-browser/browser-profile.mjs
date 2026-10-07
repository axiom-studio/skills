// Shared, structured browser sign-in state: one default profile per tenant,
// held by Cortex in the credential catalog and granted to a browser session
// whose registration declared host:browser:profile. No lock: every task loads
// the latest state; a save sends only this task's own changes since load and
// Cortex merges them (per key, last writer wins by updatedAt; deletions are
// short-lived tombstones; compare-and-swap with retry on its side).
//
//   cookies: key (domain, path, name)  storage: key (origin, key)  (localStorage)
//
// First-party only: a session saves cookies and localStorage only for the
// sites (registrable domains, eTLD+1) its pages navigated to at top level.
// Third-party (ad and tracking) cookies set while visiting those pages are
// never saved. Cortex keeps the list of first-party sites ("sites"); a stored
// entry whose site is in neither that list nor this session's visits predates
// first-party capture and is deleted (tombstoned) on the next save.
//
// Never log cookie or storage values, or keys.

import { getDomain, getHostname } from 'tldts';

export const PROFILE_SAVE_INTERVAL_MS = 5 * 60 * 1000;
const MAX_ENTRIES = 20000;
const MAX_VALUE = 1 << 20;
const SEEDED = '__axiom_profile_seeded';
const SAME_SITE = new Set(['Strict', 'Lax', 'None']);

const cookieKey = c => `c\t${c.domain}\t${c.path}\t${c.name}`;
const storageKey = s => `s\t${s.origin}\t${s.key}`;
const MAX_SITES = 500;

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

export const entrySite = entry => siteOf(entry.kind === 'cookie' ? entry.domain : entry.origin);

function originOf(value) {
  try {
    const url = new URL(value);
    return ['http:', 'https:'].includes(url.protocol) && url.origin !== 'null' ? url.origin : undefined;
  } catch { return undefined; }
}

function cookieEntry(raw) {
  if (!raw || typeof raw.name !== 'string' || typeof raw.value !== 'string' || typeof raw.domain !== 'string' ||
    !raw.name || raw.name.length > 4096 || raw.value.length > MAX_VALUE || !raw.domain || raw.domain.length > 253) return undefined;
  return { domain: raw.domain.toLowerCase(), path: typeof raw.path === 'string' && raw.path.startsWith('/') ? raw.path : '/',
    name: raw.name, value: raw.value, expires: Number.isFinite(raw.expires) && raw.expires > 0 ? raw.expires : -1,
    httpOnly: raw.httpOnly === true, secure: raw.secure === true, sameSite: SAME_SITE.has(raw.sameSite) ? raw.sameSite : 'Lax' };
}

// Playwright storageState -> keyed entries {kind, ...}.
export function profileFromStorageState(state) {
  const entries = new Map();
  for (const raw of state?.cookies ?? []) {
    const cookie = cookieEntry(raw);
    if (cookie) entries.set(cookieKey(cookie), { kind: 'cookie', ...cookie });
  }
  for (const site of state?.origins ?? []) {
    const origin = originOf(site?.origin);
    if (!origin) continue;
    for (const item of site.localStorage ?? []) {
      if (typeof item?.name !== 'string' || typeof item.value !== 'string' || !item.name || item.name === SEEDED ||
        item.name.length > 4096 || item.value.length > MAX_VALUE) continue;
      entries.set(storageKey({ origin, key: item.name }), { kind: 'storage', origin, key: item.name, value: item.value });
    }
  }
  if (entries.size > MAX_ENTRIES) throw new Error('Browser profile has too many entries');
  return entries;
}

// Cortex profile JSON -> keyed entries.
export function profileFromCortex({ cookies = [], storage = [] } = {}) {
  const entries = new Map();
  for (const raw of cookies) {
    const cookie = cookieEntry({ ...raw, value: raw?.value ?? '' });
    if (cookie && !raw.deleted) entries.set(cookieKey(cookie), { kind: 'cookie', ...cookie });
  }
  for (const raw of storage) {
    const origin = originOf(raw?.origin);
    if (!origin || typeof raw.key !== 'string' || !raw.key || raw.deleted) continue;
    entries.set(storageKey({ origin, key: raw.key }), { kind: 'storage', origin, key: raw.key, value: String(raw.value ?? '') });
  }
  return entries;
}

function same(left, right) {
  if (!left || !right) return false;
  return left.kind === 'cookie'
    ? left.value === right.value && left.expires === right.expires && left.httpOnly === right.httpOnly &&
      left.secure === right.secure && left.sameSite === right.sameSite
    : left.value === right.value;
}

// This task's own changes in Cortex's wire shape.
export function profileChanges(loaded, current, now = Date.now()) {
  const updatedAt = new Date(now).toISOString();
  const changes = { cookies: [], storage: [] };
  const wire = ({ kind, ...entry }, deleted) => {
    if (kind === 'cookie') {
      changes.cookies.push(deleted ? { domain: entry.domain, path: entry.path, name: entry.name, updatedAt, deleted: true }
        : { ...entry, updatedAt });
    } else {
      changes.storage.push(deleted ? { origin: entry.origin, key: entry.key, updatedAt, deleted: true }
        : { origin: entry.origin, key: entry.key, value: entry.value, updatedAt });
    }
  };
  for (const [key, entry] of current) if (!same(loaded.get(key), entry)) wire(entry, false);
  for (const [key, entry] of loaded) if (!current.has(key)) wire(entry, true);
  return changes;
}

// Seeds saved localStorage once per tab and origin without overwriting values
// the page has already written in this browser.
const SEED_SCRIPT = `(data) => {
  try {
    const items = data[location.origin];
    if (!items || sessionStorage.getItem(${JSON.stringify(SEEDED)})) return;
    for (const [key, value] of items) if (localStorage.getItem(key) === null) localStorage.setItem(key, value);
    sessionStorage.setItem(${JSON.stringify(SEEDED)}, '1');
  } catch {}
}`;

export class BrowserProfileStore {
  #api; #session; #grant; #loaded = new Map(); #state = 'new'; #origins = []; #saving; #now; #disabled;
  // Sites this session navigated to at top level; first-party sites Cortex
  // already holds (undefined: a profile saved before first-party capture).
  #visited = new Set(); #sites;

  constructor({ api, session, now = Date.now }) {
    if (!api?.profileGrant || !session?.sessionId || !session.grant) throw new Error('Browser profile store is unavailable');
    this.#api = api;
    this.#session = session;
    this.#now = now;
  }

  get status() { return { state: this.#state, origins: this.#origins }; }

  get visitedSites() { return [...this.#visited].sort(); }

  // Records a top-level navigation (agent or human, including sign-in
  // redirects and popups). Only http(s) pages count.
  visit(url) {
    if (typeof url !== 'string' || !/^https?:/i.test(url) || this.#visited.size >= MAX_SITES) return;
    const site = siteOf(url);
    if (site) this.#visited.add(site);
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

  async #profileGrant() {
    if (!this.#grant || Date.parse(this.#grant.expiresAt) - this.#now() < 30000) {
      this.#grant = await this.#api.profileGrant(this.#session);
      this.#state = this.#grant.state;
      this.#origins = this.#grant.origins;
    }
    return this.#grant.grant;
  }

  // Loads the latest shared state into a fresh browser context.
  async load(context) {
    const grant = await this.#profileGrant();
    const profile = await this.#api.loadProfile(this.#session, grant);
    const entries = profileFromCortex(profile);
    this.#sites = Array.isArray(profile?.sites) ? new Set(profile.sites.map(siteOf).filter(Boolean)) : undefined;
    const nowSeconds = this.#now() / 1000;
    const cookies = [];
    const storage = {};
    for (const { kind, ...entry } of entries.values()) {
      if (kind === 'cookie' && (entry.expires === -1 || entry.expires > nowSeconds)) cookies.push(entry);
      if (kind === 'storage') (storage[entry.origin] ??= []).push([entry.key, entry.value]);
    }
    if (cookies.length) await context.addCookies(cookies);
    // Playwright cannot pass arguments to a string script; inline the data as JSON.
    if (Object.keys(storage).length) await context.addInitScript(`(${SEED_SCRIPT})(${JSON.stringify(storage)})`);
    this.#loaded = entries;
    return this.status;
  }

  // Sends only the changes since load (or the last successful save).
  async save(context) {
    if (this.#disabled) return this.status;
    this.#saving = (this.#saving ?? Promise.resolve()).catch(() => {}).then(async () => {
      if (this.#disabled) return;
      const visited = new Set(this.#visited);
      const known = new Set([...(this.#sites ?? []), ...visited]);
      // Only this session's first-party state is current. Saved entries for
      // other known first-party sites are left alone; saved entries for any
      // other site are third-party leftovers and are tombstoned.
      const current = new Map([...profileFromStorageState(await context.storageState())].filter(([, entry]) => visited.has(entrySite(entry))));
      const untouched = new Map([...this.#loaded].filter(([, entry]) => !visited.has(entrySite(entry)) && known.has(entrySite(entry))));
      const base = new Map([...this.#loaded].filter(([key]) => !untouched.has(key)));
      const changes = profileChanges(base, current, this.#now());
      const sites = [...visited].filter(site => !this.#sites?.has(site)).sort();
      if (!changes.cookies.length && !changes.storage.length && !sites.length) return;
      try {
        const { origins } = await this.#api.saveProfileChanges(this.#session, await this.#profileGrant(), { ...changes, sites });
        this.#loaded = new Map([...untouched, ...current]);
        this.#sites = known;
        this.#state = 'shared';
        this.#origins = origins;
      } catch (error) {
        // Forgotten sign-ins must not be re-uploaded from this browser.
        if (error?.status === 410) { this.#disabled = true; this.#state = 'new'; this.#origins = []; return; }
        throw new Error(error?.status === 409 ? 'Browser profile is busy; it will be saved later' : 'Browser profile could not be saved');
      }
    });
    await this.#saving;
    return this.status;
  }
}
