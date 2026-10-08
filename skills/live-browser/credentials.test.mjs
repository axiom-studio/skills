import assert from 'node:assert/strict';
import { test } from 'node:test';
import { base32, CARD_SLOT, hotp, LOGIN_SLOTS, loginSite, luhn, matchingLogins, paymentCard, topOrigin, totp, websiteLogins, websiteOrigins } from './credentials.mjs';

// RFC 6238 appendix B seeds ("12345678901234567890" etc.) in base32.
const SHA1 = 'GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ';
const SHA256 = base32Encode(Buffer.from('12345678901234567890123456789012'));
const SHA512 = base32Encode(Buffer.from('1234567890123456789012345678901234567890123456789012345678901234'));

function base32Encode(bytes) {
  const alphabet = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567';
  let bits = 0, value = 0, out = '';
  for (const byte of bytes) {
    value = (value << 8) | byte; bits += 8;
    while (bits >= 5) { out += alphabet[(value >>> (bits - 5)) & 31]; bits -= 5; }
  }
  if (bits) out += alphabet[(value << (5 - bits)) & 31];
  return out;
}

test('TOTP matches the RFC 6238 test vectors', () => {
  const vectors = [
    [59, '94287082', '46119246', '90693936'],
    [1111111109, '07081804', '68084774', '25091201'],
    [1111111111, '14050471', '67062674', '99943326'],
    [1234567890, '89005924', '91819424', '93441116'],
    [2000000000, '69279037', '90698825', '38618901'],
    [20000000000, '65353130', '77737706', '47863826'],
  ];
  for (const [seconds, sha1, sha256, sha512] of vectors) {
    assert.equal(totp(`otpauth://totp/x?secret=${SHA1}&digits=8`, seconds * 1000), sha1);
    assert.equal(totp(`otpauth://totp/x?secret=${SHA256}&digits=8&algorithm=SHA256`, seconds * 1000), sha256);
    assert.equal(totp(`otpauth://totp/x?secret=${SHA512}&digits=8&algorithm=SHA512`, seconds * 1000), sha512);
  }
  // A plain base32 secret: 6 digits, 30 seconds, SHA-1; spaces and case are ignored.
  assert.equal(totp('gezd gnbv gy3t qojq gezd gnbv gy3t qojq', 59000), '287082');
  assert.equal(hotp(Buffer.from('12345678901234567890'), 0), '755224', 'RFC 4226 HOTP vector');
  assert.equal(base32('MZXW6YTBOI======').toString(), 'foobar');
  for (const bad of ['', '1189', 'otpauth://hotp/x?secret=GEZD', `otpauth://totp/x?secret=${SHA1}&digits=4`]) {
    assert.throws(() => totp(bad), error => error.message === 'Invalid one-time code secret');
  }
});

test('logins match on scheme + registrable domain; logins need a username, password and website', () => {
  assert.deepEqual(websiteOrigins('https://www.amazon.in, https://amazon.in/ap/signin  http://localhost:8080 ftp://x *.amazon.in amazon.in'),
    ['https://www.amazon.in', 'https://amazon.in', 'http://localhost:8080']);
  assert.equal(topOrigin('https://www.amazon.in/gp/cart?x=1'), 'https://www.amazon.in');
  assert.equal(topOrigin('about:blank'), undefined);
  const bindings = {
    'website-login-1': JSON.stringify({ username: 'kev@example.com', password: 'hunter2', website: 'https://www.amazon.in, https://amazon.in', name: 'Amazon India' }),
    'website-login-2': { username: 'kev', password: 'pw2', website: 'https://www.flipkart.com', totpSecret: SHA1 },
    'website-login-3': JSON.stringify({ username: 'nobody', password: 'pw3' }),
    'website-login-4': '{not json hunter2',
    'website-login-9': JSON.stringify({ username: 'x', password: 'y', website: 'https://www.amazon.in' }),
    CORTEX_HOST_INVOCATIONS: '{"host:browser":"t"}',
  };
  const logins = websiteLogins(bindings);
  assert.deepEqual(logins.map(login => [login.slot, login.label, login.origins]), [
    ['website-login-1', 'Amazon India', ['https://www.amazon.in', 'https://amazon.in']],
    ['website-login-2', 'website-login-2', ['https://www.flipkart.com']]]);
  assert.equal(logins[1].totpSecret, SHA1);
  assert.deepEqual(matchingLogins(logins, 'https://www.amazon.in').map(login => login.slot), ['website-login-1']);
  assert.deepEqual(matchingLogins(logins, 'https://evil-amazon.in'), []);
  assert.deepEqual(matchingLogins(logins, 'https://smile.amazon.in').map(login => login.slot), ['website-login-1'], 'any subdomain of the registrable domain');
  assert.deepEqual(matchingLogins(logins, 'https://flipkart.com').map(login => login.slot), ['website-login-2'], 'the bare registrable domain');
  assert.deepEqual(matchingLogins(logins, 'https://www.amazon.in.evil.example'), [], 'never another registrable domain');
  assert.deepEqual(matchingLogins(logins, 'https://amazon.com'), []);
  assert.deepEqual(matchingLogins(logins, 'https://in'), []);
  assert.deepEqual(matchingLogins(logins, 'http://www.amazon.in'), [], 'the scheme must match');
  assert.deepEqual(matchingLogins(logins, 'https://www.amazon.in', 'Amazon India').length, 1);
  assert.deepEqual(matchingLogins(logins, 'https://www.amazon.in', 'website-login-2'), [], 'a named login must also match the origin');
  assert.equal(LOGIN_SLOTS.length, 8);
});

test('a login site is the scheme plus the registrable domain, private suffixes included', () => {
  assert.equal(loginSite('https://www.amazon.in/ap/signin'), 'https://amazon.in');
  assert.equal(loginSite('https://accounts.shop.co.uk:8443/x'), 'https://shop.co.uk');
  assert.equal(loginSite('http://amazon.in'), 'http://amazon.in');
  assert.equal(loginSite('https://alice.github.io'), 'https://alice.github.io', 'github.io is a private suffix');
  assert.notEqual(loginSite('https://alice.github.io'), loginSite('https://bob.github.io'));
  assert.equal(loginSite('http://localhost:8080'), 'http://localhost');
  assert.equal(loginSite('http://192.168.1.4:3000/'), 'http://192.168.1.4');
  assert.equal(loginSite('about:blank'), undefined);
  const [github] = websiteLogins({ 'website-login-1': { username: 'a', password: 'b', website: 'https://alice.github.io' } });
  assert.deepEqual(matchingLogins([github], 'https://bob.github.io'), [], 'never across private-suffix sites');
});

test('the payment card is validated and a spend cap refusal carries no card values', () => {
  const card = paymentCard({ [CARD_SLOT]: JSON.stringify({ cardholderName: 'Kev K', number: '4242 4242 4242 4242', expiryMonth: '7', expiryYear: '29',
    cvc: '123', billingPostalCode: '560001', spendCap: '50000', spendCapCurrency: 'INR' }) });
  assert.deepEqual({ ...card }, { number: '4242424242424242', month: '07', year: '2029', cvc: '123', name: 'Kev K', postal: '560001' });
  assert.equal(luhn('4242424242424241'), false);
  assert.equal(paymentCard({ [CARD_SLOT]: { number: '4242424242424241', expiryMonth: 7, expiryYear: 2029 } }), undefined, 'Luhn');
  assert.equal(paymentCard({ [CARD_SLOT]: { number: '4242424242424242', expiryMonth: 13, expiryYear: 2029 } }), undefined);
  assert.equal(paymentCard({}), undefined);
  assert.deepEqual(paymentCard({ [CARD_SLOT]: { error: 'spend_cap_exceeded', remaining: '1200.00', cap: '5000', currency: 'INR', number: '4242424242424242' } }),
    { refused: 'spend_cap_exceeded', remaining: '1200.00', cap: '5000', currency: 'INR' });
});
