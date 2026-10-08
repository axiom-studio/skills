import { createHmac } from 'node:crypto';

// Saved website logins and the payment card arrive only through gRPC
// bindings (the host's trusted channel), never through model input. Nothing
// here logs, returns or throws a credential value: malformed bindings are
// skipped silently and every error message is fixed text.

// Manifest credential slots. Several website logins per tenant (Amazon,
// Flipkart, ...): the host binds up to this many http_basic_auth references.
export const LOGIN_SLOTS = Object.freeze(Array.from({ length: 8 }, (_, index) => `website-login-${index + 1}`));
export const CARD_SLOT = 'payment-card';

const MAX_FIELD = 4096;

function value(binding) {
  if (binding === undefined || binding === null || binding === '') return undefined;
  if (typeof binding === 'object' && !Array.isArray(binding)) return binding;
  if (typeof binding !== 'string' || binding.length > 65536) return undefined;
  try {
    const parsed = JSON.parse(binding);
    return parsed && typeof parsed === 'object' && !Array.isArray(parsed) ? parsed : undefined;
  } catch { return undefined; }
}

function field(object, ...names) {
  for (const name of names) {
    const raw = object[name];
    if (typeof raw === 'number' && Number.isFinite(raw)) return String(raw);
    if (typeof raw === 'string' && raw.length && raw.length <= MAX_FIELD) return raw;
  }
  return undefined;
}

// A non-secret label the model may use to pick a login: the vault credential
// name when the host supplies one, else the slot.
function label(object, slot) {
  const name = field(object, 'name', 'displayName');
  return name ? name.replace(/[\u0000-\u001f\u007f]+/g, ' ').trim().slice(0, 200) || slot : slot;
}

// "https://www.amazon.in, https://amazon.in" -> exact origins. An entry that
// is not an absolute http(s) URL without credentials is ignored; no suffix or
// wildcard matching ever happens.
export function websiteOrigins(website) {
  if (typeof website !== 'string') return [];
  const origins = new Set();
  for (const entry of website.split(/[\s,]+/)) {
    if (!entry) continue;
    try {
      const url = new URL(entry);
      if (['http:', 'https:'].includes(url.protocol) && url.hostname && !url.username && !url.password) origins.add(url.origin);
    } catch { /* not an origin */ }
  }
  return [...origins];
}

export function topOrigin(url) {
  try {
    const parsed = new URL(url);
    return ['http:', 'https:'].includes(parsed.protocol) ? parsed.origin : undefined;
  } catch { return undefined; }
}

// Bound website logins usable by the live browser: username, password and at
// least one website origin are required.
export function websiteLogins(bindings = {}) {
  const logins = [];
  for (const slot of LOGIN_SLOTS) {
    const object = value(bindings[slot]);
    if (!object) continue;
    const username = field(object, 'username');
    const password = field(object, 'password');
    const origins = websiteOrigins(field(object, 'website', 'websites'));
    if (!username || !password || !origins.length) continue;
    const totpSecret = field(object, 'totpSecret', 'totp_secret');
    logins.push(Object.freeze({ slot, label: label(object, slot), origins, username, password, ...(totpSecret ? { totpSecret } : {}) }));
  }
  return logins;
}

// Logins for exactly this top-level origin; a named login must match both the
// name (label or slot) and the origin.
export function matchingLogins(logins, origin, credential) {
  if (!origin) return [];
  return logins.filter(login => login.origins.includes(origin) &&
    (credential === undefined || login.slot === credential || login.label === credential));
}

const DECIMAL = /^(0|[1-9][0-9]{0,11})(\.[0-9]{1,4})?$/;
const CURRENCY = /^[A-Z]{3}$/;

// The bound card, or the host's refusal to release it (spend cap). The host
// sends {error: "spend_cap_exceeded", remaining, cap, currency} in place of
// card values when the declared charge does not fit the cap.
export function paymentCard(bindings = {}) {
  const object = value(bindings[CARD_SLOT]);
  if (!object) return undefined;
  if (object.error === 'spend_cap_exceeded') {
    const remaining = field(object, 'remaining', 'spendRemaining');
    const cap = field(object, 'cap', 'spendCap');
    const currency = field(object, 'currency', 'spendCapCurrency');
    return { refused: 'spend_cap_exceeded', ...(remaining && DECIMAL.test(remaining) ? { remaining } : {}),
      ...(cap && DECIMAL.test(cap) ? { cap } : {}), ...(currency && CURRENCY.test(currency) ? { currency } : {}) };
  }
  const number = field(object, 'number', 'cardNumber')?.replace(/[\s-]/g, '');
  const cvc = field(object, 'cvc', 'cvv')?.trim();
  const month = Number(field(object, 'expiryMonth', 'expMonth'));
  let year = Number(field(object, 'expiryYear', 'expYear'));
  if (year >= 0 && year < 100) year += 2000;
  if (!number || !/^[0-9]{12,19}$/.test(number) || !luhn(number) || !Number.isInteger(month) || month < 1 || month > 12 ||
    !Number.isInteger(year) || year < 2000 || year > 2100 || (cvc !== undefined && !/^[0-9]{3,4}$/.test(cvc))) return undefined;
  const name = field(object, 'cardholderName', 'name')?.trim();
  const postal = field(object, 'billingPostalCode', 'postalCode')?.trim();
  return Object.freeze({ number, month: String(month).padStart(2, '0'), year: String(year), ...(cvc ? { cvc } : {}),
    ...(name ? { name } : {}), ...(postal ? { postal } : {}) });
}

export function luhn(digits) {
  let sum = 0;
  for (let index = 0; index < digits.length; index++) {
    let digit = Number(digits[digits.length - 1 - index]);
    if (index % 2 === 1) { digit *= 2; if (digit > 9) digit -= 9; }
    sum += digit;
  }
  return sum % 10 === 0;
}

// RFC 4648 base32 (case-insensitive, spaces and padding ignored).
export function base32(text) {
  const clean = String(text).replace(/[\s=-]/g, '').toUpperCase();
  if (!clean || /[^A-Z2-7]/.test(clean)) throw new Error('Invalid one-time code secret');
  let bits = 0, buffer = 0;
  const bytes = [];
  for (const char of clean) {
    buffer = (buffer << 5) | 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567'.indexOf(char);
    bits += 5;
    if (bits >= 8) { bytes.push((buffer >>> (bits - 8)) & 0xff); bits -= 8; }
  }
  return Buffer.from(bytes);
}

// RFC 6238 TOTP. The secret is base32, or an otpauth://totp URI (secret,
// digits, period and algorithm parameters are honored).
export function totp(secret, now = Date.now()) {
  let key = secret, digits = 6, period = 30, algorithm = 'sha1';
  if (/^otpauth:\/\//i.test(String(secret))) {
    let url;
    try { url = new URL(secret); } catch { throw new Error('Invalid one-time code secret'); }
    if (url.host.toLowerCase() !== 'totp') throw new Error('Invalid one-time code secret');
    key = url.searchParams.get('secret') ?? '';
    digits = Number(url.searchParams.get('digits') ?? 6);
    period = Number(url.searchParams.get('period') ?? 30);
    algorithm = (url.searchParams.get('algorithm') ?? 'SHA1').toLowerCase();
  }
  if (![6, 7, 8].includes(digits) || !Number.isInteger(period) || period < 1 || period > 300 ||
    !['sha1', 'sha256', 'sha512'].includes(algorithm)) throw new Error('Invalid one-time code secret');
  return hotp(base32(key), Math.floor(now / 1000 / period), digits, algorithm);
}

export function hotp(key, counter, digits = 6, algorithm = 'sha1') {
  const message = Buffer.alloc(8);
  message.writeBigUInt64BE(BigInt(counter));
  const digest = createHmac(algorithm, key).update(message).digest();
  const offset = digest[digest.length - 1] & 0x0f;
  const code = (digest.readUInt32BE(offset) & 0x7fffffff) % 10 ** digits;
  return String(code).padStart(digits, '0');
}

export { DECIMAL, CURRENCY };
