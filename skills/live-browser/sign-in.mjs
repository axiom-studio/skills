import { siteOf } from '@axiom/live-browser';
import { loginMatches, topOrigin, totp } from './credentials.mjs';
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

const TOTAL_LABEL = /\b(order total|grand total|total amount|amount payable|amount due|total payable|total due|you pay|to pay|total|amount)\b/i;
const SYMBOLS = [[/₹|\bRs\.?|\bINR\b/i, 'INR'], [/€|\bEUR\b/i, 'EUR'], [/£|\bGBP\b/i, 'GBP'], [/¥|\bJPY\b/i, 'JPY'], [/\bUSD\b|US\$/i, 'USD'], [/\bCAD\b|CA\$/i, 'CAD'], [/\bAUD\b|A\$/i, 'AUD'], [/\$/, 'USD']];
const AMOUNT = /(₹|Rs\.?|€|£|¥|US\$|CA\$|A\$|\$|\b[A-Z]{3}\b)?\s*([0-9]{1,3}(?:[,\s][0-9]{2,3})+(?:\.[0-9]{1,2})?|[0-9]+(?:\.[0-9]{1,2})?)/;
// Stored value spent on the order besides the charged method: a site balance,
// wallet, gift card or store credit applied as a negative summary line.
const STORED_VALUE = /\b(pay balance|wallet|gift ?cards?|store credit|account credit|account balance|reward points|points|balance|credits?(?!\s*card))\b/i;
const NOT_PAYMENT = /sub.?total|savings|saved|refund|discount|promo|coupon|voucher|cashback|offer|free|shipping|delivery|fee|tax|items?\b/i;

function lineAmount(lines, index, label) {
  const line = lines[index];
  const rest = line.slice(label.index + label[0].length);
  let match = AMOUNT.exec(rest);
  let before = match ? rest.slice(0, match.index) : '';
  if (!match && lines[index + 1]) {
    match = AMOUNT.exec(lines[index + 1]);
    before = match ? lines[index + 1].slice(0, match.index) : '';
  }
  if (!match) return undefined;
  const amount = Number(match[2].replace(/[,\s]/g, ''));
  if (!Number.isFinite(amount)) return undefined;
  const currency = match[1] ? SYMBOLS.find(([pattern]) => pattern.test(match[1]))?.[1] ?? (/^[A-Z]{3}$/.test(match[1]) ? match[1] : undefined) : undefined;
  return { amount, currency, symbol: Boolean(match[1]), negative: /[-−–]\s*$/.test(before) };
}

// Best effort: the order summary shown on the page. due is the order total the
// page still charges (it may be 0 when a balance covers it); applied lists the
// stored value (balances, wallets, gift cards) subtracted before that total;
// spend, their sum, is what the order costs across all payment methods.
export function orderSummary(text) {
  const lines = String(text ?? '').slice(0, 200000).split(/\n+/).map(line => line.trim()).filter(Boolean);
  let due;
  const applied = [];
  lines.forEach((line, index) => {
    const stored = STORED_VALUE.exec(line);
    if (stored && !NOT_PAYMENT.test(line)) {
      const value = lineAmount(lines, index, stored);
      if (value?.symbol && value.negative && value.amount > 0) {
        applied.push({ method: line.replace(/[:\s]+$/, '').replace(/\s*[-−–]?\s*(₹|Rs\.?|€|£|¥|US\$|CA\$|A\$|\$|\b[A-Z]{3}\b)\s*[0-9][0-9,.\s]*$/, '').slice(0, 80),
          amount: value.amount, ...(value.currency ? { currency: value.currency } : {}), index });
      }
      return;
    }
    const label = TOTAL_LABEL.exec(line);
    if (!label || /sub.?total|savings|saved|refund|discount|items?\s*total|before/i.test(line)) return;
    const value = lineAmount(lines, index, label);
    if (!value || value.negative || value.amount < 0) return;
    const rank = /order total|grand total|amount payable|total payable|you pay|to pay|amount due|total due/i.test(label[0]) ? 2 : 1;
    if (!due || rank > due.rank || (rank === due.rank && index > due.index)) due = { amount: value.amount, currency: value.currency, rank, index };
  });
  if (!due) return undefined;
  const used = applied.filter(entry => entry.index < due.index && (!entry.currency || !due.currency || entry.currency === due.currency));
  const cents = value => Math.round(value * 100);
  const spend = (cents(due.amount) + used.reduce((sum, entry) => sum + cents(entry.amount), 0)) / 100;
  return { due: due.amount, spend, ...(due.currency ? { currency: due.currency } : {}),
    applied: used.map(({ method, amount, currency }) => ({ method, amount, ...(currency ? { currency } : {}) })) };
}

// The final pay / place-order control of a checkout. Clicking it is
// live-browser-pay's job (always approved by the user), never a plain click.
// Payment-method choices ("Pay later", "Pay on delivery") and steps before the
// final review ("Continue", "Proceed to checkout", "Use this payment method")
// are not final.
export const PAY_BUTTON = /^(?!\s*pay\s+(later|on delivery|in\s+[0-9]|by emi|monthly|after)\b)\s*(place (your |my |the )?order(\s+(and pay|now))?|pay(\s+(now|securely|online|and (place|book)( (your |the )?order)?))?|pay\s+with\s+\S.{0,80}|pay\s*(₹|rs\.?\s*|[$€£¥]|[A-Z]{3}\s*)?\s*[0-9][0-9,.]*(\s+(now|securely))?|buy now|complete (my |your |the )?(purchase|order|payment|booking|checkout)|confirm and pay|confirm (your |my |the )?(order|purchase|payment|booking)|submit (your |my )?(order|payment)|proceed to pay(ment)?|make (a )?payment|book (now|and pay)|checkout and pay|place booking|purchase( now)?)\s*$/i;

// A snapshot element that is a final pay or place-order button (not a link).
export function finalPayElement(element) {
  return element?.role === 'button' && element.state?.disabled !== true && PAY_BUTTON.test(String(element.name ?? '').replace(/\s+/g, ' '));
}

const CHECKOUT_URL = /checkout|payment|\/pay(\/|\b)|\/buy\/|placeorder|place-order|order-?review|\/cart\b|\/basket\b|\/booking/i;
const CHECKOUT_TEXT = /\b(order total|order summary|payment method|place your order|billing address|amount payable|review your order|card number|payment options|total payable|grand total)\b/i;

// Whether the page looks like a checkout (URL or visible text).
export function checkoutPage(url, text) {
  let path = '';
  try { const parsed = new URL(url); path = `${parsed.hostname}${parsed.pathname}`; } catch { /* not a URL */ }
  return CHECKOUT_URL.test(path) || CHECKOUT_TEXT.test(String(text ?? '').slice(0, 200000));
}

// Marks the page's final pay buttons (top document); returns how many.
export const SCAN_PAY_JS = `(source) => {
  const pattern = new RegExp(source, 'i');
  const selector = 'button,input[type="submit"],input[type="button"],[role="button"]';
  ${COLLECT}
  for (const e of globalThis.__livePay || []) e.removeAttribute('data-live-pay');
  const named = e => (e.getAttribute('aria-label') || e.innerText || e.value || e.getAttribute('title') || '').replace(/\\s+/g, ' ').trim();
  const found = all.filter(e => shown(e) && !e.disabled && e.getAttribute('aria-disabled') !== 'true' && pattern.test(named(e)));
  globalThis.__livePay = found;
  // Several copies of one button (top and bottom of the page) count once.
  const names = new Set(found.map(e => named(e).toLowerCase()));
  if (names.size === 1) found[0].setAttribute('data-live-pay', '1');
  return names.size;
}`;

const BODY_TEXT = 'document.body ? document.body.innerText.slice(0, 200000) : ""';

// A one-time code field (3-D Secure or a wallet's OTP) and its submit
// button in one document, plus whether it asks to approve in an app. Banks'
// 3-D Secure (ACS) pages name the field otpValue, txtOtp, otp_input, ... or
// only label it in nearby text ("Enter OTP", "One Time Password", "OTP sent
// to"); some split the code into one box per character.
// data-live-otp = code | code-N | submit; sent: the field already got a code
// (data-live-otp-sent, set before submitting it).
export const SCAN_OTP_JS = `() => {
  const selector = 'input,button,[role="button"]';
  ${COLLECT}
  for (const e of globalThis.__liveOtp || []) e.removeAttribute('data-live-otp');
  const marked = []; globalThis.__liveOtp = marked;
  const mark = (e, kind) => { e.setAttribute('data-live-otp', kind); marked.push(e); };
  const text = (document.body?.innerText || '').slice(0, 8000).toLowerCase();
  const fields = all.filter(e => e.tagName === 'INPUT' && ['', 'text', 'tel', 'number', 'password'].includes((e.getAttribute('type') || '').toLowerCase()) && usable(e));
  // An explicit one-time code field (autocomplete one-time-code, OTP or
  // "one time password" in its name or label) wins even when its label
  // mentions the card or phone the code went to; a generic "code" field
  // must not look like any other kind of field.
  const NEVER = /cvv|cvc|card.?num|expir|search|coupon|promo|voucher|gift|zip|postal|captcha/;
  const OTHER = /card|\\bpin\\b|mobile|phone|e-?mail|user|amount/;
  const kindOf = e => (e.getAttribute('autocomplete') || '').toLowerCase();
  const notCode = e => NEVER.test(describe(e)) || /email|username|tel|cc-|address|name/.test(kindOf(e));
  const OTP = /(^|[^a-z])otp|otp($|[^a-z]|val|code|input|field|box|text|num|pass)|one.?time/;
  const CODE = /verification.?code|auth(entication)?.?code|passcode|\\bcode\\b/;
  const explicit = e => kindOf(e).includes('one-time-code') || OTP.test(describe(e));
  const generic = e => CODE.test(describe(e)) && !OTHER.test(describe(e));
  const asked = /\\botp\\b|one.?time.?(password|pass ?code|code)|verification code|authentication code|security code (has been )?sent|enter (the )?(\\d-digit )?code|code (has been |was )?sent|sent (a |an |the )?(\\d-digit )?(code|otp)/.test(text);
  const short = e => (e.maxLength >= 4 && e.maxLength <= 8) || /numeric|decimal/.test(e.getAttribute('inputmode') || '') ||
    /\\[0-9\\]|\\\\d/.test(e.getAttribute('pattern') || '');
  const candidates = fields.filter(e => !notCode(e));
  const singles = candidates.filter(e => e.maxLength === 1);
  let split = [];
  if (singles.length >= 4) {
    const groups = new Map();
    for (const e of singles) { const key = e.form || e.closest('form') || e.parentElement?.parentElement || document.body; groups.set(key, [...(groups.get(key) || []), e]); }
    split = [...groups.values()].find(group => group.length >= 4 && group.length <= 8) || [];
  }
  const shortOnes = candidates.filter(short);
  const code = split.length ? undefined
    : candidates.find(explicit) || candidates.find(generic) ||
      (asked && candidates.length === 1 ? candidates[0] : undefined) || (asked && shortOnes.length === 1 ? shortOnes[0] : undefined);
  let submit;
  const first = code || split[0];
  if (first) {
    if (code) mark(code, 'code');
    split.forEach((e, i) => mark(e, 'code-' + (i + 1)));
    const named = e => (e.innerText || e.value || e.getAttribute('aria-label') || '').replace(/\\s+/g, ' ').trim().toLowerCase();
    const buttons = all.filter(e => e !== first && shown(e) && !e.disabled && (e.tagName === 'BUTTON' || e.matches('[role="button"]') ||
      (e.tagName === 'INPUT' && ['submit', 'button'].includes(e.type))) && !/resend|cancel|back|another|help|change/.test(named(e)));
    const form = first.form || first.closest('form');
    submit = (form && buttons.find(e => (e.form || e.closest('form')) === form && (e.type === 'submit' || /^(submit|verify|confirm|continue|proceed|pay|ok|done|authenticate|authori[sz]e)/.test(named(e))))) ||
      buttons.find(e => /^(submit|verify|confirm|continue|proceed|pay|ok|done|authenticate|authori[sz]e)/.test(named(e)) && named(e).length <= 40);
    if (submit) mark(submit, 'submit');
  }
  const app = /(approve|confirm|authori[sz]e) (it |this |the )?(payment|transaction|purchase|request)? ?(in|on|using|with|from) (your|the) .{0,40}app|open (your|the) .{0,40}app|notification (has been )?sent to your (phone|device|mobile)|waiting for (your )?approval|check your (phone|mobile app)/.test(text);
  return { code: Boolean(first), split: split.length, submit: Boolean(submit), app, sent: Boolean(first && first.hasAttribute('data-live-otp-sent')) };
}`;

const CONFIRMED = /\b(order (has been )?(placed|confirmed|received)|thank you for (your )?(order|purchase|booking)|booking (is )?confirmed|payment (was )?(successful|received|complete)|your order number|order confirmation)\b/i;
const VERIFY = /\b(3-?d ?secure|verified by visa|mastercard (securecode|identity check)|safekey|enter (the )?(otp|one.time password)|otp (has been )?sent|authenticate (this|the|your) (payment|transaction)|bank verification)\b/i;
const REFERENCE = /\b(?:order|booking|confirmation|reference)\s*(?:number|no\.?|id|#)?\s*[:#]?\s*([A-Z0-9][A-Z0-9-]{4,39})\b/i;
const FAILED = /\b(payment (has |was )?(failed|declined|unsuccessful|not successful|could not be (processed|completed))|transaction (has |was )?(failed|declined|unsuccessful|not successful)|(card|payment) (was )?declined|your payment did not go through)\b/i;
// A page between the click and the outcome: the payment is under way.
const PROCESSING = /\b(processing your (request|payment|order)|please wait|do not (refresh|close|press back|go back)|redirecting( you)? to (your |the )?bank|complete your payment in|connecting to (your |the )?bank)\b/i;

// What the page says after the pay click. The summary is page text:
// untrusted data for the model, bounded.
export function paymentOutcome(text) {
  const body = String(text ?? '').slice(0, 200000);
  const lines = body.split(/\n+/).map(line => line.replace(/\s+/g, ' ').trim()).filter(Boolean);
  const reference = lines.map(line => CONFIRMED.test(line) || /order|booking|confirmation/i.test(line) ? REFERENCE.exec(line)?.[1] : undefined)
    .find(value => value && /[0-9]/.test(value));
  return { confirmed: CONFIRMED.test(body), failed: FAILED.test(body), processing: PROCESSING.test(body), verification: VERIFY.test(body),
    ...(reference ? { orderReference: reference } : {}), summary: lines.join('\n').slice(0, 2000) };
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
    if (!this.#origin || !loginMatches(login, this.#origin)) throw notActionable(...ORIGIN_CHANGED);
    let steps = 0, step;
    for (let round = 0; round < 4; round++) {
      // A redirect between steps must stay on the site this login is for.
      const origin = this.origin();
      if (origin !== this.#origin) {
        if (!loginMatches(login, origin)) return { state: 'origin_changed', step };
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

  // Clicks the checkout's final pay button: the snapshot reference when
  // given (it must be a pay button), else the page's one pay button. The
  // origin is re-checked immediately before the click.
  async pay({ target, origin }) {
    let locator, check;
    if (target !== undefined) {
      const { element, locator: found } = this.#live.element(target);
      if (!finalPayElement(element)) {
        throw notActionable('This is not a final pay or place-order button', 'Take a new snapshot and pass the element marked finalPay as target. Do not ask the user to approve again until a snapshot shows one.');
      }
      locator = found;
    } else {
      // A processor's checkout frame (Razorpay, Stripe, ...) sits over the
      // page, so its pay button wins; otherwise the page's own.
      const found = [];
      for (const frame of this.paymentFrames(origin)) {
        const count = await frame.evaluate(`(${SCAN_PAY_JS})(${JSON.stringify(PAY_BUTTON.source)})`).catch(() => 0);
        if (count) found.push({ frame, url: frame.url(), count, main: frame === this.#page.mainFrame() });
      }
      const framed = found.filter(entry => !entry.main);
      const candidates = framed.length ? framed : found;
      const count = candidates.reduce((sum, entry) => sum + entry.count, 0);
      if (count !== 1) {
        throw notActionable(count ? 'Several different pay buttons are on this page' : 'No pay or place-order button on this page',
          'Take a new snapshot and pass the final pay or place-order button as target.');
      }
      const [{ frame, url }] = candidates;
      locator = frame.locator('[data-live-pay="1"]').first();
      check = () => {
        this.#check(origin);
        if (frame.isDetached?.() || frame.url() !== url) throw notActionable(...ORIGIN_CHANGED);
      };
    }
    const baseline = await this.#baseline();
    await guarded(async () => {
      (check ?? (() => this.#check(origin)))();
      await locator.click({ timeout: 10000 });
    });
    return baseline;
  }

  // The page before a click, so the outcome is read from what changed: a
  // checkout's own "order confirmation" or "payment failed" wording is not
  // an outcome.
  async #baseline() {
    const outcome = paymentOutcome(await this.#page.evaluate(BODY_TEXT).catch(() => ''));
    return { url: this.#page.url(), confirmed: outcome.confirmed, failed: outcome.failed };
  }

  // Watches the page after the pay click (or a submitted code) until the
  // outcome is clear, for at most watchMs: the click often leads through a
  // "Processing your request" page and redirects before the bank's code
  // page, whose field is usually in the bank's (any-origin) frame and appears
  // seconds later. Returns as soon as the page is confirmed, failed, asks for
  // a code (a new field: not the one the code was just typed into) or for
  // approval in the bank's app; otherwise the last state seen.
  async awaitPaymentOutcome(baseline, { watchMs = 45000, pollMs = 1000, now = Date.now } = {}) {
    const deadline = now() + watchMs;
    for (;;) {
      await this.#settle();
      const outcome = await this.paymentState(baseline);
      const done = ['confirmed', 'payment_failed', 'approve_in_app'].includes(outcome.state) || (outcome.state === 'otp_required' && !outcome.sent);
      const left = deadline - now();
      if (done || left <= 0) return outcome;
      await new Promise(resolve => setTimeout(resolve, Math.min(pollMs, left)));
    }
  }

  // The top document, same-site frames and known processor frames.
  paymentFrames(origin = this.origin()) {
    const main = this.#page.mainFrame();
    return this.#page.frames().filter(frame => frame === main || (!frame.isDetached?.() && cardFrameAllowed(frame.url(), origin)));
  }

  // The order summary of the page and of each processor frame shown with it.
  async summaries(origin) {
    const main = this.#page.mainFrame();
    const summaries = [];
    for (const frame of this.paymentFrames(origin)) {
      const summary = orderSummary(await frame.evaluate(BODY_TEXT).catch(() => ''));
      if (summary) summaries.push({ ...summary, main: frame === main });
    }
    return summaries;
  }

  // Where a payment stands after the pay click or a code: confirmed, a
  // one-time code asked for in any frame (3-D Secure challenges are bank
  // frames of any origin), approval in the bank's app, or unknown.
  // baseline (from before the click) keeps the checkout's own wording from
  // counting as an outcome. moved: the page is no longer the one clicked on.
  async paymentState(baseline) {
    const outcome = paymentOutcome(await this.#page.evaluate(BODY_TEXT).catch(() => ''));
    const moved = !baseline || this.#page.url() !== baseline.url;
    const result = { ...outcome, moved };
    if (outcome.confirmed && (moved || !baseline.confirmed)) return { ...result, state: 'confirmed' };
    let app = false;
    for (const frame of this.#page.frames()) {
      if (frame.isDetached?.()) continue;
      const scan = await frame.evaluate(`(${SCAN_OTP_JS})()`).catch(() => undefined);
      if (scan?.code) return { ...result, state: 'otp_required', sent: scan.sent === true };
      if (scan?.app) app = true;
    }
    if (app) return { ...result, state: 'approve_in_app' };
    if (outcome.failed && (moved || !baseline.failed)) return { ...result, state: 'payment_failed' };
    return { ...result, state: outcome.verification ? 'payment_verification' : 'clicked' };
  }

  // Types the user's bank code into the payment's code field (any frame of
  // the current page, while its top-level origin is one the payment went
  // through) and submits it.
  // Returns the baseline to watch the outcome from.
  async submitPaymentCode(code, origins) {
    const top = this.origin();
    if (!top || !origins.includes(top)) throw notActionable(...ORIGIN_CHANGED);
    let target;
    for (const frame of this.#page.frames()) {
      if (frame.isDetached?.()) continue;
      const scan = await frame.evaluate(`(${SCAN_OTP_JS})()`).catch(() => undefined);
      if (scan?.code) { target = { frame, url: frame.url(), scan }; break; }
    }
    if (!target) throw notActionable('No payment code field on this page', 'Take a new snapshot; the bank page may have changed or expired.');
    const { frame, url, scan } = target;
    const check = () => {
      this.#check(top);
      if (frame.isDetached?.() || frame.url() !== url) throw notActionable(...ORIGIN_CHANGED);
    };
    const characters = [...code];
    if (scan.split && characters.length !== scan.split) throw notActionable('The code does not fit the code boxes on this page', 'Check the code with the user.');
    const boxes = scan.split ? characters.map((_, index) => `code-${index + 1}`) : ['code'];
    for (const [index, box] of boxes.entries()) {
      await this.#type(frame.locator(`[data-live-otp="${box}"]`).first(), scan.split ? characters[index] : code, check);
    }
    const last = frame.locator(`[data-live-otp="${boxes.at(-1)}"]`).first();
    // A field still showing after this is the one the code went into.
    await guarded(() => frame.locator(`[data-live-otp="${boxes[0]}"]`).first().evaluate(e => e.setAttribute('data-live-otp-sent', '1'), undefined, { timeout: 5000 }));
    const baseline = await this.#baseline();
    await guarded(async () => {
      check();
      if (scan.submit) await frame.locator('[data-live-otp="submit"]').first().click({ timeout: 10000 });
      else await last.press('Enter', { timeout: 5000 });
    });
    return baseline;
  }

  // The card-entry fields of the top document, same-site frames and known
  // processor frames; no card values are involved. entry is true when a card
  // number field is there: a form that asks for new card details. A page
  // listing the site's saved cards or other payment methods has none.
  async cardForm() {
    const top = this.origin();
    if (!top) return { top, frames: [], entry: false };
    const main = this.#page.mainFrame();
    const frames = [];
    for (const frame of this.#page.frames()) {
      if (frame !== main && (frame.isDetached?.() || !cardFrameAllowed(frame.url(), top))) continue;
      const loose = frame !== main && processorHost(new URL(frame.url()).hostname.toLowerCase());
      const found = await frame.evaluate(`(${SCAN_CARD_JS})(${loose})`).catch(() => ({}));
      if (found && Object.keys(found).length) frames.push({ frame, url: frame.url(), found });
    }
    return { top, frames, entry: frames.some(entry => entry.found.number) };
  }

  // Card values into the card fields cardForm found. Returns the kinds filled.
  async fillCard(card, form) {
    const { top, frames, entry } = form ?? await this.cardForm();
    if (!top) throw notActionable('This page is not a website checkout', 'Navigate to the checkout page first.');
    if (!entry) return { filled: [] };
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
