// Real Chromium: sign-in and card filling on intercepted https sites. Runs
// when a Chromium build is installed (PLAYWRIGHT_CHROMIUM, or Playwright's
// cache); skipped otherwise. Camoufox runs the same page scripts in Firefox.
import assert from 'node:assert/strict';
import { existsSync, readdirSync } from 'node:fs';
import { createRequire } from 'node:module';
import { homedir } from 'node:os';
import { join } from 'node:path';
import { after, before, test } from 'node:test';
import { totp, websiteLogins } from './credentials.mjs';
import { LivePage, POINT_NAME_JS } from './page.mjs';
import { paymentOutcome, SignIn } from './sign-in.mjs';

const require = createRequire(import.meta.resolve('@axiom/live-browser'));
const { chromium } = require('playwright-core');

function chromiumPath() {
  if (process.env.PLAYWRIGHT_CHROMIUM) return process.env.PLAYWRIGHT_CHROMIUM;
  const cache = join(homedir(), '.cache', 'ms-playwright');
  if (!existsSync(cache)) return undefined;
  for (const entry of readdirSync(cache).filter(name => /^chromium-\d+$/.test(name)).sort().reverse()) {
    const path = join(cache, entry, 'chrome-linux64', 'chrome');
    if (existsSync(path)) return path;
  }
  return undefined;
}

const executablePath = chromiumPath();
const skip = executablePath ? false : 'no Chromium build installed';
const SECRET = 'GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ';
const [login] = websiteLogins({ 'website-login-1': { name: 'Shop', website: 'https://shop.example', username: 'kev@example.com', password: 'Hunter2-Secret!' } });
const card = { number: '4242424242424242', month: '07', year: '2029', cvc: '123', name: 'Kev K', postal: '560001' };

let browser;
before(async () => { if (!skip) browser = await chromium.launch({ headless: true, executablePath }); });
after(async () => { await browser?.close(); });

const html = body => `<!doctype html><html><head><meta charset="utf-8"></head><body>${body}</body></html>`;

// Serves pages by URL and records every form post.
async function site(routes) {
  const context = await browser.newContext();
  const page = await context.newPage();
  const posts = [];
  const requests = [];
  await context.route('**/*', async route => {
    const request = route.request();
    const url = new URL(request.url());
    requests.push(request.url());
    if (request.method() === 'POST') posts.push({ url: `${url.origin}${url.pathname}`, body: Object.fromEntries(new URLSearchParams(request.postData() ?? '')) });
    const key = `${request.method()} ${url.origin}${url.pathname}`;
    const reply = routes[key] ?? routes[`${url.origin}${url.pathname}`];
    if (!reply) return route.fulfill({ status: 404, contentType: 'text/html', body: html('Not found') });
    return route.fulfill({ status: 200, contentType: 'text/html', body: html(typeof reply === 'function' ? reply(request) : reply) });
  });
  return { page, posts, requests, live: new LivePage(page), close: () => context.close() };
}

test('identifier-first sign-in, then an SMS code supplied in a later call', { skip }, async () => {
  const s = await site({
    'https://shop.example/signin': `<input type="search" name="q" placeholder="Search">
      <form method="post" action="/signin/password"><label>Email or mobile phone number <input name="email" type="text"></label>
      <button type="submit">Continue</button></form>`,
    'POST https://shop.example/signin/password': `<form method="post" action="/signin/check"><input type="hidden" name="email" value="x">
      <label>Password <input name="password" type="password"></label><button>Sign in</button></form>`,
    'POST https://shop.example/signin/check': `<p>We sent a code to your mobile number ending 42.</p>
      <form method="post" action="/signin/otp"><label>Enter OTP <input name="otp" autocomplete="one-time-code"></label><button>Verify</button></form>`,
    'POST https://shop.example/signin/otp': '<h1>Hello, Kev</h1>',
  });
  try {
    await s.page.goto('https://shop.example/signin');
    const first = await new SignIn(s.live).signIn(login);
    assert.deepEqual(first, { state: 'otp_required', step: 'password', via: 'message' });
    assert.deepEqual(s.posts.map(post => post.url), ['https://shop.example/signin/password', 'https://shop.example/signin/check']);
    assert.equal(s.posts[0].body.email, login.username);
    assert.equal(s.posts[1].body.password, login.password);
    // The user replied in chat; the next call fills the code on the same page.
    const second = await new SignIn(s.live).signIn(login, { oneTimeCode: '482913' });
    assert.equal(second.state, 'submitted');
    assert.equal(s.posts[2].body.otp, '482913');
    assert.match(await s.page.textContent('h1'), /Hello/);
  } finally { await s.close(); }
});

test('a saved authenticator secret answers the code page; split code boxes work', { skip }, async () => {
  const boxes = Array.from({ length: 6 }, (_, i) => `<input name="d${i}" maxlength="1" inputmode="numeric" aria-label="Digit ${i + 1}">`).join('');
  const s = await site({
    'https://shop.example/login': `<form method="post" action="/login"><input name="user" autocomplete="username" placeholder="Username">
      <input name="pass" type="password" autocomplete="current-password"><input type="submit" value="Log in"></form>`,
    'POST https://shop.example/login': `<p>Open your authenticator app and enter the code.</p>
      <form method="post" action="/mfa"><div>${boxes}</div><button type="submit">Verify</button></form>`,
    'POST https://shop.example/mfa': '<h1>Signed in</h1>',
  });
  try {
    await s.page.goto('https://shop.example/login');
    const now = Date.now();
    const result = await new SignIn(s.live).signIn({ ...login, totpSecret: SECRET }, { now: () => now });
    assert.equal(result.state, 'submitted');
    assert.equal(result.step, 'one_time_code');
    const posted = s.posts.find(post => post.url.endsWith('/mfa')).body;
    assert.equal(Array.from({ length: 6 }, (_, i) => posted[`d${i}`]).join(''), totp(SECRET, now));
  } finally { await s.close(); }
});

test('a redirect to another site mid sign-in stops before any keystroke there', { skip }, async () => {
  const s = await site({
    'https://shop.example/signin': `<form method="post" action="/x"><input name="email" type="email" placeholder="Email">
      <input name="password" type="password" onfocus="location.href='https://evil.example/capture'"><button>Sign in</button></form>`,
    'https://evil.example/capture': '<form><input name="email" type="email"><input name="password" type="password"></form>',
  });
  try {
    await s.page.goto('https://shop.example/signin');
    await assert.rejects(new SignIn(s.live).signIn(login), error => error.notActionable === true && /another site/.test(error.message) &&
      !error.message.includes(login.password));
    assert.equal(new URL(s.page.url()).origin, 'https://evil.example');
    assert.equal(await s.page.inputValue('input[name="password"]'), '', 'nothing typed on the other site');
    assert.equal(await s.page.inputValue('input[name="email"]'), '');
    assert.ok(!s.requests.some(url => url.includes(encodeURIComponent(login.password)) || url.includes(login.password)));
    // Starting on a site the login is not for fails closed.
    await assert.rejects(new SignIn(s.live).signIn(login), error => error.notActionable === true);
  } finally { await s.close(); }
});

test('a login saved for the registrable domain signs in across its subdomains, never over http', { skip }, async () => {
  const s = await site({
    'https://www.shop.example/signin': `<form method="post" action="https://accounts.shop.example/signin/password"><label>Email <input name="email" type="email"></label>
      <button type="submit">Continue</button></form>`,
    'POST https://accounts.shop.example/signin/password': `<form method="post" action="/signin/check"><label>Password <input name="password" type="password"></label>
      <button>Sign in</button></form>`,
    'POST https://accounts.shop.example/signin/check': '<h1>Hello, Kev</h1>',
    'http://www.shop.example/signin': '<form method="post" action="/x"><input name="email" type="email"><input name="password" type="password"><button>Sign in</button></form>',
  });
  try {
    await s.page.goto('https://www.shop.example/signin');
    const result = await new SignIn(s.live).signIn(login);
    assert.equal(result.state, 'submitted');
    assert.deepEqual(s.posts.map(post => post.url), ['https://accounts.shop.example/signin/password', 'https://accounts.shop.example/signin/check']);
    assert.equal(s.posts[0].body.email, login.username);
    assert.equal(s.posts[1].body.password, login.password);
    await s.page.goto('http://www.shop.example/signin');
    await assert.rejects(new SignIn(s.live).signIn(login), error => error.notActionable === true);
    assert.equal(await s.page.inputValue('input[name="password"]'), '', 'an https login never fills on http');
  } finally { await s.close(); }
});

test('a same-site redirect during a keystroke batch still stops the fill', { skip }, async () => {
  const s = await site({
    'https://www.shop.example/signin': `<form method="post" action="/x"><input name="email" type="email" placeholder="Email">
      <input name="password" type="password" onfocus="location.href='https://other.shop.example/capture'"><button>Sign in</button></form>`,
    'https://other.shop.example/capture': '<form><input name="email" type="email"><input name="password" type="password"></form>',
  });
  try {
    await s.page.goto('https://www.shop.example/signin');
    await assert.rejects(new SignIn(s.live).signIn(login), error => error.notActionable === true && !error.message.includes(login.password));
    assert.equal(new URL(s.page.url()).origin, 'https://other.shop.example');
    assert.equal(await s.page.inputValue('input[name="password"]'), '');
    assert.ok(!s.requests.some(url => url.includes(encodeURIComponent(login.password)) || url.includes(login.password)));
  } finally { await s.close(); }
});

test('the card goes into the processor frame and card fields only, never an unknown frame', { skip }, async () => {
  const s = await site({
    'https://shop.example/checkout': `<h2>Shipping</h2><label>ZIP code <input name="ship_zip"></label>
      <label>Name on card <input name="cardholder"></label>
      <iframe name="pay" src="https://js.stripe.com/v3/elements-inner-card.html" width="600" height="200"></iframe>
      <iframe name="ad" src="https://ads.example/widget" width="300" height="100"></iframe>
      <p>Order Total:</p><p>₹4,210.00</p>`,
    'https://js.stripe.com/v3/elements-inner-card.html': `<input name="cardnumber" autocomplete="cc-number" placeholder="1234 1234 1234 1234">
      <input name="exp-date" autocomplete="cc-exp" placeholder="MM / YY"><input name="cvc" autocomplete="cc-csc" placeholder="CVC">
      <input name="postal" autocomplete="postal-code" placeholder="ZIP">`,
    'https://ads.example/widget': '<input name="steal" autocomplete="cc-number">',
  });
  try {
    await s.page.goto('https://shop.example/checkout');
    await s.page.waitForFunction(() => document.querySelectorAll('iframe').length === 2);
    await Promise.all(s.page.frames().map(frame => frame.waitForLoadState('domcontentloaded')));
    const result = await new SignIn(s.live).fillCard(card);
    assert.deepEqual(result.filled.sort(), ['cvc', 'exp', 'name', 'number', 'postal']);
    const stripe = s.page.frames().find(frame => frame.url().startsWith('https://js.stripe.com/'));
    assert.equal(await stripe.inputValue('input[name="cardnumber"]'), card.number);
    assert.equal(await stripe.inputValue('input[name="exp-date"]'), '07/29');
    assert.equal(await stripe.inputValue('input[name="cvc"]'), '123');
    assert.equal(await stripe.inputValue('input[name="postal"]'), '560001');
    assert.equal(await s.page.inputValue('input[name="cardholder"]'), 'Kev K');
    assert.equal(await s.page.inputValue('input[name="ship_zip"]'), '', 'a shipping ZIP is not a card field');
    const ad = s.page.frames().find(frame => frame.url().startsWith('https://ads.example/'));
    assert.equal(await ad.inputValue('input[name="steal"]'), '', 'an unknown cross-origin frame never gets the card');
  } finally { await s.close(); }
});

test('a page with only a search box has no sign-in form', { skip }, async () => {
  const s = await site({ 'https://shop.example/': '<form><input type="search" name="q" placeholder="Search Shop"><button>Go</button></form>' });
  try {
    await s.page.goto('https://shop.example/');
    assert.deepEqual(await new SignIn(s.live).signIn(login), { state: 'no_form' });
    assert.equal(await s.page.inputValue('input[name="q"]'), '');
  } finally { await s.close(); }
});

test('no card number field means nothing is filled', { skip }, async () => {
  const s = await site({ 'https://shop.example/pay': '<label>Name on card <input name="cardholder"></label><label>Expiry month <input name="m"></label>' });
  try {
    await s.page.goto('https://shop.example/pay');
    assert.deepEqual(await new SignIn(s.live).fillCard(card), { filled: [] });
    assert.equal(await s.page.inputValue('input[name="cardholder"]'), '');
  } finally { await s.close(); }
});

test('pay finds the place-order button, re-checks the origin and reads the confirmation', { skip }, async () => {
  const s = await site({
    'https://shop.example/checkout': `<button>Place your order</button><a href="/help">Payment options</a><p>Order Total: ₹4,910.00</p>
      <form method="post" action="/place"><button type="submit">Place your order</button></form>`,
    'POST https://shop.example/place': '<h1>Thank you, your order has been placed.</h1><p>Order number: 404-5551234-7654321</p>',
  });
  try {
    await s.page.goto('https://shop.example/checkout');
    const box = await s.page.locator('form button').boundingBox();
    assert.equal(await s.page.evaluate(`(${POINT_NAME_JS})(${box.x + 5}, ${box.y + 5})`), 'Place your order');
    await s.page.locator('button').first().evaluate(e => e.remove());
    await new SignIn(s.live).pay({ origin: 'https://shop.example' });
    assert.deepEqual(s.posts.map(post => post.url), ['https://shop.example/place']);
    const outcome = paymentOutcome(await s.page.evaluate('document.body.innerText'));
    assert.equal(outcome.confirmed, true);
    assert.equal(outcome.orderReference, '404-5551234-7654321');
    // A redirect away before the click: nothing is clicked.
    await s.page.goto('https://shop.example/checkout');
    await assert.rejects(new SignIn(s.live).pay({ origin: 'https://other.example' }), error => error.notActionable === true);
    assert.equal(s.posts.length, 1);
  } finally { await s.close(); }
});

test('pay inside a processor frame, then a 3-D Secure code in the bank frame', { skip }, async () => {
  const s = await site({
    'https://shop.example/checkout': `<button>Proceed to pay</button><p>Order Total: ₹4,910.00</p>
      <iframe name="rzp" src="https://api.razorpay.com/v1/checkout/embedded" width="500" height="300"></iframe>
      <iframe name="ad" src="https://ads.example/widget" width="300" height="100"></iframe>`,
    'https://api.razorpay.com/v1/checkout/embedded': `<p>Amount ₹4,910.00</p>
      <form method="post" action="/v1/pay"><button type="submit">Pay ₹4,910</button></form>`,
    'POST https://api.razorpay.com/v1/pay': `<p>Redirecting to your bank</p><iframe name="acs" src="https://acs.bank.example/challenge" width="400" height="200"></iframe>`,
    'https://acs.bank.example/challenge': `<p>Enter the OTP sent to your mobile ending 42</p>
      <form method="post" action="/verify"><input name="otpValue" inputmode="numeric" aria-label="Enter OTP"><button type="submit">Submit</button><button type="button">Resend OTP</button></form>`,
    'POST https://acs.bank.example/verify': '<p>Payment successful. Thank you for your order. Order number: ORD-778812</p>',
    'https://ads.example/widget': '<button>Pay now</button>',
  });
  try {
    await s.page.goto('https://shop.example/checkout');
    await Promise.all(s.page.frames().map(frame => frame.waitForLoadState('domcontentloaded')));
    // A coordinate click on the processor's Pay button is seen through the frame.
    const rzp = s.page.frames().find(frame => frame.url().startsWith('https://api.razorpay.com/'));
    const button = await rzp.locator('button').boundingBox(); // page coordinates
    assert.equal(await s.live.pointName(button.x + 5, button.y + 5), 'Pay ₹4,910');
    const sign = new SignIn(s.live);
    assert.deepEqual((await sign.summaries('https://shop.example')).map(summary => summary.due), [4910, 4910]);
    await sign.pay({ origin: 'https://shop.example' });
    assert.deepEqual(s.posts.map(post => post.url), ['https://api.razorpay.com/v1/pay'], 'the processor button, not the page or ad button');
    const acs = () => s.page.frames().find(frame => frame.url().startsWith('https://acs.bank.example/'));
    while (!acs()) await new Promise(resolve => setTimeout(resolve, 50));
    await acs().waitForLoadState('domcontentloaded');
    assert.equal((await sign.paymentState()).state, 'otp_required');
    await assert.rejects(sign.submitPaymentCode('482913', ['https://other.example']), error => error.notActionable === true);
    await sign.submitPaymentCode('482913', ['https://shop.example']);
    assert.equal(s.posts.at(-1).url, 'https://acs.bank.example/verify');
    assert.equal(s.posts.at(-1).body.otpValue, '482913');
  } finally { await s.close(); }
});
