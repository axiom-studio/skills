import { siteOf } from '@axiom/live-browser';
import { topOrigin, totp } from './credentials.mjs';
import { elementProblem, exposed, notActionable } from './page.mjs';

// Fills saved sign-ins and the saved payment card into the live page.
// Credential values go only into fields this module recognized, through
// Playwright locators; they never reach results, logs, snapshots or errors.
// Every Playwright failure is replaced by fixed text (a call log can quote
// typed text).

const ORIGIN_CHANGED = ['The page moved to another site, so nothing more was typed',
  'Take a new snapshot. Call this action again only on the site the saved login is for.'];

// Shared by the scanners: collects elements through open shadow roots.
const COLLECT = `const all = []; const visit = root => { for (const e of root.querySelectorAll('*')) {
    if (e.matches(selector)) all.push(e); if (e.shadowRoot) visit(e.shadowRoot); } }; visit(document);
  const shown = e => { const r = e.getBoundingClientRect(); const s = getComputedStyle(e);
    return r.width > 0 && r.height > 0 && s.visibility !== 'hidden' && s.display !== 'none'; };
  const usable = e => shown(e) && !e.disabled && !e.readOnly;
  const describe = e => [e.name, e.id, e.getAttribute('autocomplete'), e.getAttribute('aria-label'), e.getAttribute('placeholder'),
    e.getAttribute('data-testid'), ...(e.labels ? [...e.labels].map(l => l.innerText) : [])].filter(Boolean).join(' ').toLowerCase();`;

// Marks the sign-in fields of the top-level document (never inside frames):
// data-live-signin = username | password | otp | otp-N | submit.
export const SCAN_LOGIN_JS = `() => {
  const selector = 'input,button,[role="button"]';
  ${COLLECT}
  for (const e of globalThis.__liveSignIn || []) e.removeAttribute('data-live-signin');
  const marked = []; globalThis.__liveSignIn = marked;
  const mark = (e, kind) => { e.setAttribute('data-live-signin', kind); marked.push(e); };
  const formOf = e => e && (e.form || e.closest('form'));
  const fields = all.filter(e => e.tagName === 'INPUT' && ['', 'text', 'email', 'tel', 'number', 'password'].includes((e.getAttribute('type') || '').toLowerCase()) && usable(e));
  const EXCLUDE = /search|coupon|promo|voucher|gift|zip|postal|captcha|newsletter|card|cvv|cvc/;
  const OTP = /one-time-code|\\botp\\b|one.?time|verification.?code|security.?code|auth(entication)?.?code|2fa|mfa|totp|passcode|\\bcode\\b/;
  const USER = /username|e-?mail|\\buser|login|account|phone|mobile|identifier|\\bid\\b/;
  const otps = fields.filter(e => !EXCLUDE.test(describe(e)) && ((e.getAttribute('autocomplete') || '').includes('one-time-code') || OTP.test(describe(e))));
  const passwords = fields.filter(e => e.type === 'password' && !/new-password/.test(e.getAttribute('autocomplete') || '') && !otps.includes(e));
  let split = [];
  const singles = fields.filter(e => e.maxLength === 1 && e.type !== 'password');
  if (singles.length >= 4) {
    const groups = new Map();
    for (const e of singles) { const key = formOf(e) || e.parentElement?.parentElement || document.body; groups.set(key, [...(groups.get(key) || []), e]); }
    split = [...groups.values()].find(group => group.length >= 4 && group.length <= 8) || [];
  }
  const password = passwords[0];
  const users = fields.filter(e => e.type !== 'password' && !otps.includes(e) && !split.includes(e) && !EXCLUDE.test(describe(e)) &&
    (/username|email/.test(e.getAttribute('autocomplete') || '') || e.type === 'email' || USER.test(describe(e))));
  const user = password
    ? (users.find(e => formOf(password) && formOf(e) === formOf(password)) || users.filter(e => e.compareDocumentPosition(password) & Node.DOCUMENT_POSITION_FOLLOWING).at(-1))
    : users[0];
  const otp = password || split.length ? undefined : otps[0];
  if (user) mark(user, 'username');
  if (password) mark(password, 'password');
  if (otp) mark(otp, 'otp');
  split.forEach((e, i) => mark(e, 'otp-' + (i + 1)));
  const target = password || user || otp || split[0];
  const named = e => (e.innerText || e.value || e.getAttribute('aria-label') || '').replace(/\\s+/g, ' ').trim().toLowerCase();
  const SUBMIT = /^(sign ?in|log ?in|login|continue|next|submit|verify|confirm|send|done|ok)\\b/;
  const OTHER = /sign ?up|create|register|google|apple|facebook|microsoft|github|forgot|resend|another way|cancel|back/;
  const buttons = all.filter(e => e !== target && shown(e) && !e.disabled && (e.tagName === 'BUTTON' || e.matches('[role="button"]') ||
    (e.tagName === 'INPUT' && ['submit', 'button'].includes(e.type))) && !OTHER.test(named(e)));
  const form = formOf(target);
  const submit = target && ((form && buttons.find(e => formOf(e) === form && (e.type === 'submit' || SUBMIT.test(named(e))))) ||
    buttons.find(e => SUBMIT.test(named(e)) && named(e).length <= 40));
  if (submit) mark(submit, 'submit');
  const text = (document.body?.innerText || '').slice(0, 8000).toLowerCase();
  const via = /authenticator|authentication app|\\btotp\\b|security key app/.test(text) ? 'app'
    : /\\bsms\\b|text message|we('ve| have)? sent|sent (a |an |the )?(code|otp)|to your (phone|mobile|email)|check your (email|phone)/.test(text) ? 'message' : '';
  return { username: Boolean(user), password: Boolean(password), otp: otp ? 1 : split.length, split: split.length > 0, submit: Boolean(submit), via };
}`;

// Card fields in one document. "loose" (a payment processor's own frame)
// also accepts bare MM / YY / CVC style names.
export const SCAN_CARD_JS = `(loose) => {
  const selector = 'input,select';
  ${COLLECT}
  for (const e of globalThis.__liveCard || []) e.removeAttribute('data-live-card');
  const marked = []; globalThis.__liveCard = marked;
  const AUTOCOMPLETE = { 'cc-number': 'number', 'cc-exp': 'exp', 'cc-exp-month': 'month', 'cc-exp-year': 'year', 'cc-csc': 'cvc', 'cc-name': 'name', 'postal-code': 'postal' };
  const kindOf = e => {
    const tokens = (e.getAttribute('autocomplete') || '').toLowerCase().split(/\\s+/);
    for (const token of tokens) if (AUTOCOMPLETE[token]) return AUTOCOMPLETE[token];
    const d = describe(e);
    if (/card.?number|cardnumber|cc.?num|credit.?card.?(no|num)|debit.?card.?(no|num)|card.?no\\b/.test(d) || (loose && /\\bnumber\\b|\\bpan\\b|1234 1234/.test(d))) return 'number';
    if (/cvc|cvv|csc|security.?code|card.?code|card.?verification/.test(d)) return 'cvc';
    if (/name.?on.?card|card.?holder|cardholder|cc.?name/.test(d)) return 'name';
    if (/(exp|card|valid).*month|month.*(exp|card)/.test(d) || (loose && /month|^mm$|\\bmm\\b(?!\\s*\\/)/.test(d))) return 'month';
    if (/(exp|card|valid).*year|year.*(exp|card)/.test(d) || (loose && /year|\\byy(yy)?\\b(?!\\s*\\/)/.test(d))) return 'year';
    if (/expir|exp.?date|mm\\s*\\/\\s*yy|valid.?thr/.test(d)) return 'exp';
    if (/postal|zip/.test(d)) return 'postal';
    return undefined;
  };
  const found = {};
  for (const e of all) {
    if (!usable(e) || (e.tagName === 'INPUT' && !['', 'text', 'tel', 'number', 'password'].includes((e.getAttribute('type') || '').toLowerCase()))) continue;
    const kind = kindOf(e);
    if (!kind || found[kind]) continue;
    e.setAttribute('data-live-card', kind); marked.push(e);
    found[kind] = { select: e.tagName === 'SELECT', placeholder: (e.getAttribute('placeholder') || '').toLowerCase().slice(0, 40),
      maxLength: e.maxLength > 0 ? e.maxLength : 0, options: e.tagName === 'SELECT' ? [...e.options].slice(0, 120).map(o => [o.value, o.text.trim()]) : [] };
  }
  return found;
}`;

const TOTAL_LABEL = /\b(order total|grand total|total amount|amount payable|amount due|total payable|total due|you pay|to pay|total)\b/i;
const SYMBOLS = [[/₹|\bRs\.?|\bINR\b/i, 'INR'], [/€|\bEUR\b/i, 'EUR'], [/£|\bGBP\b/i, 'GBP'], [/¥|\bJPY\b/i, 'JPY'], [/\bUSD\b|US\$/i, 'USD'], [/\bCAD\b|CA\$/i, 'CAD'], [/\bAUD\b|A\$/i, 'AUD'], [/\$/, 'USD']];
const AMOUNT = /(₹|Rs\.?|€|£|¥|US\$|CA\$|A\$|\$|\b[A-Z]{3}\b)?\s*([0-9]{1,3}(?:[,\s][0-9]{2,3})+(?:\.[0-9]{1,2})?|[0-9]+(?:\.[0-9]{1,2})?)/;

// Best effort: the order total shown on the page, as {amount, currency?}.
export function pageTotal(text) {
  const lines = String(text ?? '').slice(0, 200000).split(/\n+/).map(line => line.trim()).filter(Boolean);
  let best;
  lines.forEach((line, index) => {
    const label = TOTAL_LABEL.exec(line);
    if (!label || /sub.?total|savings|discount|items?\s*total|before/i.test(line)) return;
    const rest = line.slice(label.index + label[0].length);
    const match = AMOUNT.exec(rest) ?? (lines[index + 1] ? AMOUNT.exec(lines[index + 1]) : null);
    if (!match) return;
    const amount = Number(match[2].replace(/[,\s]/g, ''));
    if (!Number.isFinite(amount) || amount <= 0) return;
    const symbol = match[1] ? SYMBOLS.find(([pattern]) => pattern.test(match[1]))?.[1] ?? (/^[A-Z]{3}$/.test(match[1]) ? match[1] : undefined) : undefined;
    const rank = /order total|grand total|amount payable|total payable|you pay|to pay|amount due|total due/i.test(label[0]) ? 2 : 1;
    if (!best || rank > best.rank || (rank === best.rank && index > best.index)) best = { amount, currency: symbol, rank, index };
  });
  return best ? { amount: best.amount, ...(best.currency ? { currency: best.currency } : {}) } : undefined;
}

// Known card processors' frame hosts (exact host or a subdomain).
export const PROCESSOR_HOSTS = Object.freeze(['stripe.com', 'stripe.network', 'razorpay.com', 'adyen.com', 'adyenpayments.com',
  'braintreegateway.com', 'braintree-api.com', 'paypal.com', 'checkout.com', 'squareup.com', 'squarecdn.com', 'pci.shopifyinc.com',
  'shopifycs.com', 'recurly.com', 'authorize.net', 'worldpay.com', 'cybersource.com', 'payu.in', 'payu.com', 'cashfree.com',
  'paytm.in', 'juspay.in', 'ccavenue.com', 'billdesk.com', 'mollie.com', 'klarna.com', 'chargebee.com', 'spreedly.com', 'vgs.io',
  'verygoodsecurity.com', 'basistheory.com', 'evervault.com']);

function processorHost(host) {
  return PROCESSOR_HOSTS.some(suffix => host === suffix || host.endsWith(`.${suffix}`));
}

// Frames of this page that may receive card values: the top document, frames
// of the same site, and known processors' frames.
export function cardFrameAllowed(frameURL, top) {
  let url;
  try { url = new URL(frameURL); } catch { return false; }
  if (url.protocol !== 'https:' && url.origin !== top) return false;
  if (url.origin === top) return true;
  const topSite = siteOf(top);
  return Boolean(topSite && siteOf(url.href) === topSite) || processorHost(url.hostname.toLowerCase());
}

// Runs Playwright element work; failures become fixed-text errors.
async function guarded(operation) {
  try { return await operation(); } catch (error) {
    const problem = elementProblem(error);
    if (problem?.expose === true) throw problem;
    throw exposed('The browser could not fill the field');
  }
}

export class SignIn {
  #live; #page; #origin; #settle;

  constructor(live) {
    this.#live = live;
    this.#page = live.page;
    this.#settle = () => live.settle();
  }

  // The current top-level origin (scheme, host and port).
  origin() { return topOrigin(this.#page.url()); }

  // Called immediately before every keystroke batch and click.
  #check(expected = this.#origin) {
    const main = this.#page.mainFrame?.()?.url?.() ?? this.#page.url();
    if (!expected || this.origin() !== expected || topOrigin(main) !== expected) throw notActionable(...ORIGIN_CHANGED);
  }

  async scan() {
    return guarded(() => this.#page.evaluate(`(${SCAN_LOGIN_JS})()`));
  }

  async #type(locator, text, check) {
    await guarded(async () => {
      check();
      await locator.click({ timeout: 5000 }).catch(() => locator.focus({ timeout: 5000 }));
      check();
      await locator.fill('', { timeout: 5000 });
      check();
      await locator.pressSequentially(text, { delay: 20, timeout: 30000 });
    });
  }

  async #field(kind, text) {
    await this.#type(this.#page.locator(`[data-live-signin="${kind}"]`).first(), text, () => this.#check());
  }

  async #code(scan, code) {
    if (scan.split) {
      const characters = [...code];
      if (characters.length !== scan.otp) throw notActionable('The code does not fit the code boxes on this page', 'Check the code with the user.');
      for (let index = 0; index < characters.length; index++) await this.#field(`otp-${index + 1}`, characters[index]);
    } else await this.#field('otp', code);
  }

  async #submit(scan, last) {
    await guarded(async () => {
      this.#check();
      if (scan.submit) await this.#page.locator('[data-live-signin="submit"]').first().click({ timeout: 5000 });
      else await this.#page.locator(`[data-live-signin="${last}"]`).first().press('Enter', { timeout: 5000 });
    });
    await this.#settle();
    // Some sign-ins finish with a client-side redirect a moment later.
    await this.#page.waitForTimeout?.(600).catch(() => {});
    await this.#settle();
  }

  // Signs in with one login on the page's current origin. Returns
  // {state: submitted | otp_required | origin_changed | no_form, step}.
  async signIn(login, { oneTimeCode, now = Date.now } = {}) {
    this.#origin = this.origin();
    if (!this.#origin || !login.origins.includes(this.#origin)) throw notActionable(...ORIGIN_CHANGED);
    let steps = 0, step;
    for (let round = 0; round < 4; round++) {
      // A redirect between steps must stay on an origin this login is for.
      const origin = this.origin();
      if (origin !== this.#origin) {
        if (!login.origins.includes(origin)) return { state: 'origin_changed', step };
        this.#origin = origin;
      }
      const scan = await this.scan();
      if (scan.otp && !scan.password) {
        // Still asking after a code was submitted: wrong or expired code.
        if (step === 'one_time_code') return { state: 'otp_required', step, retry: true, via: scan.via };
        let code = oneTimeCode;
        if (code === undefined && login.totpSecret && scan.via !== 'message') code = totp(login.totpSecret, now());
        if (!code) return { state: 'otp_required', step, via: scan.via };
        await this.#code(scan, code);
        await this.#submit(scan, scan.split ? `otp-${scan.otp}` : 'otp');
        oneTimeCode = undefined;
        step = 'one_time_code'; steps++;
        continue;
      }
      if (oneTimeCode !== undefined) return { state: 'no_code_field', step };
      if (scan.password) {
        if (step === 'password') return { state: 'submitted', step, stillOnForm: true };
        if (scan.username) await this.#field('username', login.username);
        await this.#field('password', login.password);
        await this.#submit(scan, 'password');
        step = 'password'; steps++;
        continue;
      }
      if (scan.username) {
        if (step === 'username') return { state: 'submitted', step, stillOnForm: true };
        await this.#field('username', login.username);
        await this.#submit(scan, 'username');
        step = 'username'; steps++;
        continue;
      }
      return steps ? { state: 'submitted', step } : { state: 'no_form' };
    }
    return { state: 'submitted', step };
  }

  // Card values into recognized card fields of the top document, same-site
  // frames and known processor frames. Returns the kinds filled.
  async fillCard(card) {
    const top = this.origin();
    if (!top) throw notActionable('This page is not a website checkout', 'Navigate to the checkout page first.');
    const main = this.#page.mainFrame();
    const frames = [];
    for (const frame of this.#page.frames()) {
      if (frame !== main && (frame.isDetached?.() || !cardFrameAllowed(frame.url(), top))) continue;
      const loose = frame !== main && processorHost(new URL(frame.url()).hostname.toLowerCase());
      const found = await frame.evaluate(`(${SCAN_CARD_JS})(${loose})`).catch(() => ({}));
      if (found && Object.keys(found).length) frames.push({ frame, url: frame.url(), found });
    }
    if (!frames.some(entry => entry.found.number)) return { filled: [] };
    const filled = new Set();
    for (const { frame, url, found } of frames) {
      const own = Object.keys(found);
      const check = () => {
        this.#check(top);
        if (frame.isDetached?.() || frame.url() !== url) throw notActionable(...ORIGIN_CHANGED);
      };
      for (const kind of ['number', 'exp', 'month', 'year', 'cvc', 'name', 'postal']) {
        const field = found[kind];
        if (!field) continue;
        // A postal code only where card fields are: never a shipping form.
        if (kind === 'postal' && (!card.postal || !own.some(other => ['number', 'exp', 'cvc'].includes(other)))) continue;
        const text = cardValue(kind, field, card);
        if (text === undefined) continue;
        const locator = frame.locator(`[data-live-card="${kind}"]`).first();
        if (field.select) {
          await guarded(async () => {
            check();
            for (const option of text) {
              const match = field.options.find(([value, label]) => value === option || label === option);
              if (match) { await locator.selectOption(match[0], { timeout: 5000 }); return; }
            }
            throw notActionable('The card expiry option was not found', 'Take a snapshot and check the expiry fields.');
          });
        } else await this.#type(locator, text, check);
        filled.add(kind);
      }
    }
    await this.#settle();
    return { filled: [...filled] };
  }
}

function cardValue(kind, field, card) {
  const yy = card.year.slice(-2);
  const month = String(Number(card.month));
  switch (kind) {
    case 'number': return card.number;
    case 'cvc': return card.cvc;
    case 'name': return card.name;
    case 'postal': return card.postal;
    case 'exp': return /yyyy/.test(field.placeholder) ? `${card.month}/${card.year}` : `${card.month}/${yy}`;
    case 'month': return field.select ? [card.month, month, `${card.month} - ${monthName(card.month)}`, monthName(card.month), monthName(card.month).slice(0, 3)] : card.month;
    case 'year': return field.select ? [card.year, yy] : (field.maxLength === 2 || (/\byy\b/.test(field.placeholder) && !/yyyy/.test(field.placeholder)) ? yy : card.year);
    default: return undefined;
  }
}

function monthName(month) {
  return ['January', 'February', 'March', 'April', 'May', 'June', 'July', 'August', 'September', 'October', 'November', 'December'][Number(month) - 1];
}
