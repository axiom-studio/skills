// Shared, structured browser sign-in state (one default profile per tenant,
// held by Cortex in the credential catalog and granted to this browser
// session). No lock: every task loads the latest state, and a save merges only
// this task's own changes onto the latest state.
//
//   cookies: key (domain, path, name) -> {value, expires, httpOnly, secure, sameSite}
//   storage: key (origin, key)        -> {value}            (localStorage)
//   every entry has updatedAt (ms); deletions are tombstones {deleted: true}.
//
// Merge rule: per key, last writer wins by updatedAt; tombstones expire after
// TOMBSTONE_TTL_MS. Cortex stores an internal revision used only for
// compare-and-swap; a conflicting save reloads the latest state and re-applies
// the same diff. Never log cookie or storage values, or keys.

export const PROFILE_SAVE_INTERVAL_MS = 5 * 60 * 1000;
export const TOMBSTONE_TTL_MS = 7 * 24 * 60 * 60 * 1000;
export const PROFILE_MAX_ENTRIES = 20000;
const MAX_VALUE = 16384;
const SEEDED = '__axiom_profile_seeded';
const DOMAIN = /^(?=.{1,253}$)([a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$/;
const SAME_SITE = new Set(['Strict', 'Lax', 'None']);

const cookieKey = c => `c\t${c.domain}\t${c.path}\t${c.name}`;
const storageKey = s => `s\t${s.origin}\t${s.key}`;

function originOf(value) {
  try {
    const url = new URL(value);
    return ['http:', 'https:'].includes(url.protocol) && url.origin !== 'null' ? url.origin : undefined;
  } catch { return undefined; }
}

function cookieEntry(raw) {
  if (!raw || typeof raw.name !== 'string' || typeof raw.value !== 'string' || typeof raw.domain !== 'string' ||
    !raw.name || raw.name.length > 1024 || raw.value.length > MAX_VALUE || !raw.domain || raw.domain.length > 255) return undefined;
  return { domain: raw.domain.toLowerCase(), path: typeof raw.path === 'string' && raw.path.startsWith('/') ? raw.path : '/',
    name: raw.name, value: raw.value, expires: Number.isFinite(raw.expires) ? raw.expires : -1,
    httpOnly: raw.httpOnly === true, secure: raw.secure === true, sameSite: SAME_SITE.has(raw.sameSite) ? raw.sameSite : 'Lax' };
}

// Converts Playwright storageState into keyed, comparable entries.
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
        item.name.length > 1024 || item.value.length > MAX_VALUE) continue;
      entries.set(storageKey({ origin, key: item.name }), { kind: 'storage', origin, key: item.name, value: item.value });
    }
  }
  if (entries.size > PROFILE_MAX_ENTRIES) throw new Error('Browser profile has too many entries');
  return entries;
}

function same(left, right) {
  if (!left || !right || left.deleted || right.deleted) return false;
  return left.kind === 'cookie'
    ? left.value === right.value && left.expires === right.expires && left.httpOnly === right.httpOnly &&
      left.secure === right.secure && left.sameSite === right.sameSite
    : left.value === right.value;
}

function strip({ updatedAt, deleted, ...entry }) { return entry; }

// This task's own changes: sets for new or changed entries, tombstones for
// entries that existed at load time and are gone now.
export function profileDiff(loaded, current, now = Date.now()) {
  const changes = [];
  for (const [key, entry] of current) {
    if (!same(loaded.get(key), entry)) changes.push({ ...strip(entry), updatedAt: now });
  }
  for (const [key, entry] of loaded) {
    if (!entry.deleted && !current.has(key)) {
      const { value, expires, httpOnly, secure, sameSite, ...identity } = strip(entry);
      changes.push({ ...identity, deleted: true, updatedAt: now });
    }
  }
  return changes;
}

const keyOf = entry => entry.kind === 'cookie' ? cookieKey(entry) : storageKey(entry);

// Applies changes to a saved entry list: per-key last writer wins.
export function applyProfileChanges(entries, changes, now = Date.now()) {
  const merged = new Map();
  for (const entry of entries ?? []) merged.set(keyOf(entry), entry);
  for (const change of changes) {
    const key = keyOf(change);
    const existing = merged.get(key);
    if (!existing || change.updatedAt >= existing.updatedAt) merged.set(key, change);
  }
  for (const [key, entry] of merged) {
    if (entry.deleted && now - entry.updatedAt > TOMBSTONE_TTL_MS) merged.delete(key);
  }
  if (merged.size > PROFILE_MAX_ENTRIES) throw new Error('Browser profile has too many entries');
  return [...merged.values()];
}

export function profileOrigins(entries) {
  const domains = new Set();
  for (const entry of entries) {
    if (entry.deleted) continue;
    const domain = entry.kind === 'cookie' ? entry.domain.replace(/^\./, '') : (() => { try { return new URL(entry.origin).hostname; } catch { return ''; } })();
    if (DOMAIN.test(domain)) domains.add(domain);
    if (domains.size >= 200) break;
  }
  return [...domains].sort();
}

function validEntry(entry) {
  if (!entry || typeof entry !== 'object' || !Number.isFinite(entry.updatedAt)) return false;
  if (entry.kind === 'cookie') return typeof entry.domain === 'string' && typeof entry.path === 'string' && typeof entry.name === 'string' &&
    (entry.deleted === true || typeof entry.value === 'string');
  if (entry.kind === 'storage') return Boolean(originOf(entry.origin)) && typeof entry.key === 'string' &&
    (entry.deleted === true || typeof entry.value === 'string');
  return false;
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

// Small, isolated client for the Cortex profile routes (relative to
// CORTEX_BROWSER_API_URL, authenticated by the browser session's runtime grant):
//   POST sessions/{id}/profile/load -> {revision, entries: [...]}   (revision 0: new)
//   POST sessions/{id}/profile/save {baseRevision, entries} -> {revision}
//        409 when baseRevision is stale (the runtime reloads and re-merges).
export class BrowserProfileStore {
  #api; #session; #loaded = new Map(); #origins = []; #revision = 0; #saving; #now;

  constructor({ api, session, now = Date.now }) {
    if (!api?.request || !session?.sessionId || !session.grant) throw new Error('Browser profile store is unavailable');
    this.#api = api;
    this.#session = session;
    this.#now = now;
  }

  get status() { return { state: this.#revision > 0 ? 'shared' : 'new', origins: this.#origins }; }

  #call(path, body) {
    return this.#api.request(`sessions/${encodeURIComponent(this.#session.sessionId)}/profile/${path}`,
      { grant: this.#session.grant, tenantID: this.#session.tenantId, body });
  }

  async #latest() {
    const result = await this.#call('load', {});
    if (!Number.isSafeInteger(result?.revision) || result.revision < 0 || !Array.isArray(result.entries) ||
      result.entries.length > PROFILE_MAX_ENTRIES) throw new Error('Cortex returned an invalid browser profile');
    return { revision: result.revision, entries: result.entries.filter(validEntry) };
  }

  // Loads the latest shared state into a fresh browser context.
  async load(context) {
    const { revision, entries } = await this.#latest();
    const live = entries.filter(entry => !entry.deleted);
    const nowSeconds = this.#now() / 1000;
    const cookies = live.filter(entry => entry.kind === 'cookie' && (entry.expires === -1 || entry.expires > nowSeconds)).map(strip)
      .map(({ kind, ...cookie }) => cookie);
    if (cookies.length) await context.addCookies(cookies);
    const storage = {};
    for (const entry of live) if (entry.kind === 'storage') (storage[entry.origin] ??= []).push([entry.key, entry.value]);
    // Playwright cannot pass arguments to a string script; inline the data as JSON.
    if (Object.keys(storage).length) await context.addInitScript(`(${SEED_SCRIPT})(${JSON.stringify(storage)})`);
    this.#revision = revision;
    this.#loaded = new Map(live.map(entry => [keyOf(entry), entry]));
    this.#origins = profileOrigins(live);
    return this.status;
  }

  // Merges this task's changes onto the latest state, retrying on conflict.
  async save(context, { attempts = 4 } = {}) {
    this.#saving = (this.#saving ?? Promise.resolve()).catch(() => {}).then(async () => {
      const current = profileFromStorageState(await context.storageState());
      const changes = profileDiff(this.#loaded, current, this.#now());
      if (!changes.length) return;
      for (let attempt = 1; ; attempt++) {
        const latest = await this.#latest();
        const entries = applyProfileChanges(latest.entries, changes, this.#now());
        try {
          const saved = await this.#call('save', { baseRevision: latest.revision, entries });
          if (!Number.isSafeInteger(saved?.revision) || saved.revision <= latest.revision) throw new Error('Cortex returned an invalid browser profile revision');
          this.#revision = saved.revision;
          this.#loaded = current;
          this.#origins = profileOrigins(entries);
          return;
        } catch (error) {
          if (error?.status !== 409 || attempt >= attempts) throw new Error('Browser profile could not be saved');
        }
      }
    });
    await this.#saving;
    return this.status;
  }
}
