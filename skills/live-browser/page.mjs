import { createHash } from 'node:crypto';

// Errors written by this module are safe to show the model; any other error
// (for example from Playwright) may quote page content and is replaced.
export function exposed(message, extra = {}) { return Object.assign(new Error(message), { expose: true }, extra); }

// Page observation and agent interaction for one live browser page. Element
// references are generation-scoped ("s<generation>:e<n>"), so an action can
// only target what the model saw in the latest snapshot. Never log page text,
// field values or screenshots.

export const MAX_ELEMENTS = 180;
export const MAX_TEXT = 48 * 1024;
export const MAX_MODEL_SCREENSHOT = 1024 * 1024;
const REF = /^s([1-9][0-9]*):e([1-9][0-9]*)$/;

export const CHALLENGES = Object.freeze({
  captcha: /\b(captcha|recaptcha|hcaptcha|verify you are human)\b/i,
  // Instructions to complete a factor, not topic words such as "2FA".
  mfa: /\b((multi[ -]?factor|two[ -]?factor) authentication (is )?(required|needed)|enter (your )?(verification|security|authentication) code|(verification|security|authentication) code (is )?(required|needed)|2fa (challenge|required|verification)|one[ -]?time (password|passcode|code))\b/i,
  // Challenge language or provider markers, never a vendor name alone.
  anti_bot: /\b(access denied|unusual traffic|bot detection|security check|js_challenge|checking your browser|performing security verification|cloudflare ray id)\b|(?:\/cdn-cgi\/challenge-platform\/|cf-chl-)/i,
});

export function detectChallenges(...evidence) {
  const bounded = evidence.filter(value => typeof value === 'string').map(value => value.slice(0, 4096)).join('\n');
  return Object.entries(CHALLENGES).filter(([, pattern]) => pattern.test(bounded)).map(([kind]) => kind);
}

export const SNAPSHOT_JS = `(limits) => {
  const sel = 'a,button,input,select,textarea,summary,[role="button"],[role="link"],[role="checkbox"],[role="radio"],[role="textbox"],[role="combobox"],[role="tab"],[role="menuitem"],[role="option"],[contenteditable="true"]';
  const candidates = [];
  const visit = (root) => {
    for (const element of root.querySelectorAll('*')) {
      if (element.matches(sel)) candidates.push(element);
      if (element.shadowRoot) visit(element.shadowRoot);
    }
  };
  visit(document);
  const visible = (e) => { const r = e.getBoundingClientRect(); const s = getComputedStyle(e);
    return r.width > 0 && r.height > 0 && s.visibility !== 'hidden' && s.display !== 'none'; };
  const inViewport = (e) => { const r = e.getBoundingClientRect();
    return r.bottom > 0 && r.right > 0 && r.top < innerHeight && r.left < innerWidth; };
  const distance = (e) => { const r = e.getBoundingClientRect();
    const dx = r.right < 0 ? -r.right : (r.left > innerWidth ? r.left - innerWidth : 0);
    const dy = r.bottom < 0 ? -r.bottom : (r.top > innerHeight ? r.top - innerHeight : 0);
    return Math.hypot(dx, dy); };
  const labelledBy = (e) => (e.getAttribute('aria-labelledby') || '').split(/\\s+/)
    .map((id) => e.getRootNode().getElementById?.(id)?.innerText || '').filter(Boolean).join(' ');
  const labelFor = (e) => (e.labels && e.labels[0] ? e.labels[0].innerText : '');
  const name = (e) => (e.getAttribute('aria-label') || labelledBy(e) || labelFor(e) || e.innerText ||
    e.getAttribute('alt') || e.getAttribute('title') || e.getAttribute('placeholder') ||
    e.getAttribute('name') || e.getAttribute('type') || '').replace(/\\s+/g, ' ').trim().slice(0, 240);
  const role = (e) => e.getAttribute('role') || ({A: 'link', BUTTON: 'button', TEXTAREA: 'textbox', SELECT: 'combobox', SUMMARY: 'button'}[e.tagName]) ||
    (e.tagName === 'INPUT' ? ({checkbox: 'checkbox', radio: 'radio', button: 'button', submit: 'button', reset: 'button'}[e.type] || 'textbox') :
      (e.isContentEditable ? 'textbox' : e.tagName.toLowerCase()));
  const landmark = (e) => { const parent = e.closest('dialog,[role="dialog"],form,article,nav,main,aside,header,footer,section');
    if (!parent) return ''; const kind = parent.getAttribute('role') || parent.tagName.toLowerCase();
    const label = (parent.getAttribute('aria-label') || '').trim(); return (label ? kind + ': ' + label : kind).slice(0, 240); };
  for (const e of (globalThis.__liveBrowserRefs || [])) e.removeAttribute?.('data-live-ref');
  const els = [...new Set(candidates)].map((element, index) => ({element, index}))
    .filter(({element}) => visible(element))
    .sort((a, b) => distance(a.element) - distance(b.element) || a.index - b.index)
    .slice(0, limits.elements).map(({element}) => element);
  globalThis.__liveBrowserRefs = els;
  els.forEach((e, i) => e.setAttribute('data-live-ref', String(i + 1)));
  const lines = []; const seen = new Set(); let length = 0;
  const collect = (root) => {
    const walker = document.createTreeWalker(root, NodeFilter.SHOW_ELEMENT | NodeFilter.SHOW_TEXT);
    while (walker.nextNode() && length < limits.text) {
      const node = walker.currentNode;
      if (node.nodeType === Node.ELEMENT_NODE && node.shadowRoot) collect(node.shadowRoot);
      if (node.nodeType !== Node.TEXT_NODE) continue;
      const parent = node.parentElement;
      if (!parent || !visible(parent) || !inViewport(parent)) continue;
      const line = (node.textContent || '').replace(/\\s+/g, ' ').trim();
      if (!line || seen.has(line)) continue;
      seen.add(line); lines.push(line); length += line.length + 1;
    }
  };
  collect(document.body || document.documentElement);
  return { url: location.href, title: document.title, text: lines.join('\\n').slice(0, limits.text),
    elements: els.map((e, i) => {
      const r = e.getBoundingClientRect(); const state = {};
      for (const key of ['disabled', 'checked', 'selected', 'required', 'readOnly']) if (key in e && e[key] === true) state[key === 'readOnly' ? 'readonly' : key] = true;
      for (const attr of ['aria-expanded', 'aria-pressed', 'aria-current', 'autocomplete', 'type']) { const v = e.getAttribute(attr); if (v) state[attr.replace('aria-', '')] = v.slice(0, 128); }
      const r0 = role(e);
      if (['textbox', 'searchbox', 'combobox'].includes(r0) || e.tagName === 'TEXTAREA' || e.tagName === 'SELECT' || e.isContentEditable) {
        state.filled = String((e.isContentEditable ? e.textContent : e.value) || '').length > 0;
      }
      return { ref: i + 1, role: r0, name: name(e), context: landmark(e), href: e.tagName === 'A' && e.href ? e.href : '',
        inViewport: inViewport(e), bounds: {x: Math.round(r.x), y: Math.round(r.y), width: Math.round(r.width), height: Math.round(r.height)}, state };
    }) };
}`;

export const SETTLE_JS = `() => new Promise((resolve) => {
  let done = false, quiet, hard, observer;
  const finish = () => { if (done) return; done = true; observer?.disconnect(); clearTimeout(quiet); clearTimeout(hard); resolve(); };
  if (!document.documentElement) return finish();
  quiet = setTimeout(finish, 180); hard = setTimeout(finish, 1400);
  observer = new MutationObserver(() => { clearTimeout(quiet); quiet = setTimeout(finish, 180); });
  observer.observe(document.documentElement, {subtree: true, childList: true, attributes: true, characterData: true});
})`;

// Fields the agent must never fill: the user enters these during a handoff.
const SENSITIVE_FIELD = /(password|passcode|one-time-code|cc-|card|cvc|cvv|security code|iban|ssn|social security)/i;

export function navigationURL(value) {
  let url;
  try { url = new URL(value); } catch { throw exposed('URL must be an absolute HTTP(S) URL'); }
  if (!['http:', 'https:'].includes(url.protocol) || !url.hostname || url.username || url.password || value.length > 2048) {
    throw exposed('URL must be an HTTP(S) URL without embedded credentials');
  }
  return url.toString();
}

function destination(current, href) {
  try {
    const target = new URL(href); const here = new URL(current);
    if (!['http:', 'https:'].includes(target.protocol)) return {};
    return target.origin === here.origin
      ? { destinationScope: 'same_origin', destinationPath: (target.pathname || '/').slice(0, 2048) }
      : { destinationScope: 'external_origin' };
  } catch { return {}; }
}

export function observationDigest(raw) {
  const canonical = { url: raw.url ?? '', title: raw.title ?? '', text: (raw.text ?? '').slice(0, MAX_TEXT),
    elements: (raw.elements ?? []).slice(0, MAX_ELEMENTS).map(e => ({ role: e.role, name: e.name, context: e.context, href: e.href ?? '', state: e.state ?? {} })) };
  return `sha256:${createHash('sha256').update(JSON.stringify(canonical)).digest('hex')}`;
}

export class LivePage {
  #page;
  generation = 0;
  #elements = new Map();

  constructor(page) { this.#page = page; }

  get page() { return this.#page; }

  async settle() {
    await this.#page.waitForLoadState('domcontentloaded', { timeout: 5000 }).catch(() => {});
    // Invoke explicitly: Firefox/Juggler does not call a function-shaped string.
    await this.#page.evaluate(`(${SETTLE_JS})()`).catch(() => {});
  }

  async navigate(url) {
    const target = navigationURL(url);
    let status;
    try {
      const response = await this.#page.goto(target, { waitUntil: 'domcontentloaded', timeout: 30000 });
      status = response?.status();
    } catch { throw exposed('Navigation failed or timed out'); }
    await this.settle();
    this.#elements.clear();
    return { url: this.#page.url(), title: await this.#page.title().catch(() => ''), ...(Number.isInteger(status) ? { httpStatus: status } : {}) };
  }

  async snapshot({ includeScreenshot = false } = {}) {
    await this.settle();
    const raw = await this.#page.evaluate(`(${SNAPSHOT_JS})(${JSON.stringify({ elements: MAX_ELEMENTS, text: MAX_TEXT })})`);
    this.generation++;
    this.#elements = new Map(raw.elements.map(element => [element.ref, element]));
    const elements = raw.elements.map(element => ({
      ref: `s${this.generation}:e${element.ref}`, role: String(element.role).slice(0, 64), name: element.name, context: element.context,
      inViewport: element.inViewport === true, bounds: element.bounds, state: element.state,
      ...(element.href ? destination(raw.url, element.href) : {}),
    }));
    const challenges = detectChallenges(raw.text, raw.url, raw.title);
    const result = { generation: this.generation, observationDigest: observationDigest(raw), url: raw.url, title: raw.title,
      text: raw.text, elements, challenges };
    if (includeScreenshot) result.modelMedia = await this.modelMedia();
    return result;
  }

  async modelMedia() {
    let bytes = await this.#page.screenshot({ type: 'jpeg', quality: 45, fullPage: false, timeout: 10000 });
    if (bytes.length > MAX_MODEL_SCREENSHOT) bytes = await this.#page.screenshot({ type: 'jpeg', quality: 25, fullPage: false, timeout: 10000 });
    if (bytes.length > MAX_MODEL_SCREENSHOT) throw exposed('Screenshot exceeds the 1 MiB model limit');
    const viewport = this.#page.viewportSize?.() ?? await this.#page.evaluate('({ width: innerWidth, height: innerHeight })').catch(() => null);
    return { mediaType: 'image/jpeg', contentBase64: bytes.toString('base64'), detail: 'low',
      width: viewport?.width ?? null, height: viewport?.height ?? null };
  }

  #resolve(target) {
    const match = REF.exec(String(target ?? ''));
    if (!match) throw exposed('Target must be an element reference from the latest snapshot');
    if (Number(match[1]) !== this.generation || !this.#elements.has(Number(match[2]))) {
      throw exposed('Element reference is stale; take a new snapshot');
    }
    return { element: this.#elements.get(Number(match[2])), locator: this.#page.locator(`[data-live-ref="${Number(match[2])}"]`).first() };
  }

  async #center(locator) {
    await locator.scrollIntoViewIfNeeded({ timeout: 5000 }).catch(() => {});
    const box = await locator.boundingBox({ timeout: 5000 });
    if (!box) throw exposed('Target is not visible');
    return { x: box.x + box.width / 2, y: box.y + box.height / 2 };
  }

  async click({ target, generation, x, y }) {
    if (target !== undefined) {
      const { locator } = this.#resolve(target);
      try {
        const point = await this.#center(locator);
        await this.#page.mouse.click(point.x, point.y);
      } catch { await locator.click({ timeout: 5000 }); }
    } else {
      if (generation !== this.generation) throw exposed('Coordinates are stale; take a new snapshot');
      if (![x, y].every(value => Number.isFinite(value) && value >= 0 && value <= 10000)) throw exposed('Invalid coordinates');
      await this.#page.mouse.click(x, y);
    }
    await this.settle();
    this.#elements.clear();
  }

  async fill({ target, value }) {
    if (typeof value !== 'string' || value.length > 20000) throw exposed('Invalid value');
    const { element, locator } = this.#resolve(target);
    const semantics = `${element.name} ${element.state?.type ?? ''} ${element.state?.autocomplete ?? ''}`;
    if (SENSITIVE_FIELD.test(semantics)) {
      throw exposed('This field is for a password, payment or identity value. Call live-browser-request-handoff so the user enters it.');
    }
    if (!['textbox', 'searchbox', 'combobox'].includes(element.role)) throw exposed('Target is not an editable field');
    const point = await this.#center(locator);
    await this.#page.mouse.click(point.x, point.y);
    await this.#page.keyboard.press('ControlOrMeta+A');
    await this.#page.keyboard.press('Backspace');
    if (value) await this.#page.keyboard.type(value, { delay: 8 });
    const actual = await locator.evaluate(e => ('value' in e ? e.value : (e.innerText || e.textContent || '')), undefined, { timeout: 5000 }).catch(() => undefined);
    this.#elements.clear();
    return { retained: actual === value };
  }

  async select({ target, value }) {
    if (typeof value !== 'string' || !value || value.length > 1000) throw exposed('Invalid option');
    const { locator } = this.#resolve(target);
    let selected = await locator.selectOption({ value }, { timeout: 5000 }).catch(() => []);
    if (!selected?.length) selected = await locator.selectOption({ label: value }, { timeout: 5000 }).catch(() => []);
    if (!selected?.length) throw exposed('Option not found; use its value or visible label from the page');
    await this.settle();
    this.#elements.clear();
    return { selected: selected.length };
  }

  async scroll({ dx = 0, dy = 0 }) {
    if (![dx, dy].every(value => Number.isFinite(value) && Math.abs(value) <= 10000)) throw exposed('Scroll delta must be within ±10000 pixels');
    await this.#page.mouse.wheel(dx, dy);
    await this.settle();
  }

  async screenshot({ fullPage = false }) {
    let bytes = await this.#page.screenshot({ type: 'jpeg', quality: 60, fullPage: fullPage === true, timeout: 15000 });
    if (bytes.length > MAX_MODEL_SCREENSHOT) bytes = await this.#page.screenshot({ type: 'jpeg', quality: 30, fullPage: false, timeout: 15000 });
    if (bytes.length > MAX_MODEL_SCREENSHOT) throw exposed('Screenshot exceeds the 1 MiB model limit');
    return { mediaType: 'image/jpeg', contentBase64: bytes.toString('base64'), detail: 'low', width: null, height: null };
  }
}
