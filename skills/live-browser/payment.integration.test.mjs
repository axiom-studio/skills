// Real Chromium: what follows a pay click on intercepted https sites. A bank's
// one-time code page arrives seconds after the click, behind a "Processing
// your request" page, in the issuer's cross-origin frame or as the issuer's
// own top-level page (dev runs 5560f9e2, f5f1f1f5). Skipped without Chromium.
import assert from 'node:assert/strict';
import { existsSync, mkdtempSync, readdirSync, rmSync } from 'node:fs';
import { createRequire } from 'node:module';
import { homedir, tmpdir } from 'node:os';
import { join } from 'node:path';
import { after, before, test } from 'node:test';
import { PersistentBrowserProfile } from '@axiom/live-browser';
import { LivePage } from './page.mjs';
import { LiveBrowserService } from './service.mjs';
import { SCAN_OTP_JS, SignIn } from './sign-in.mjs';

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
const roots = [];
let browser;
before(async () => { if (!skip) browser = await chromium.launch({ headless: true, executablePath }); });
after(async () => { await browser?.close(); for (const root of roots) rmSync(root, { recursive: true, force: true }); });

const html = body => `<!doctype html><html><head><meta charset="utf-8"></head><body>${body}</body></html>`;

async function site(routes) {
  const context = await browser.newContext();
  const page = await context.newPage();
  const posts = [];
  await context.route('**/*', async route => {
    const request = route.request();
    const url = new URL(request.url());
    if (request.method() === 'POST') posts.push({ url: `${url.origin}${url.pathname}`, body: Object.fromEntries(new URLSearchParams(request.postData() ?? '')) });
    const reply = routes[`${request.method()} ${url.origin}${url.pathname}`] ?? routes[`${url.origin}${url.pathname}`];
    if (!reply) return route.fulfill({ status: 404, contentType: 'text/html', body: html('Not found') });
    return route.fulfill({ status: 200, contentType: 'text/html', body: html(typeof reply === 'function' ? reply(request) : reply) });
  });
  return { context, page, posts, live: new LivePage(page), close: () => context.close() };
}

const CHECKOUT = `<p>Order Total: ₹703.00</p><form method="post" action="/place"><button type="submit">Place your order</button></form>`;
// Amazon-like: the click posts, a processing page redirects, and the payment
// page loads the issuer's challenge frame a moment later.
const PROCESSING = '<p>Processing your request</p><script>setTimeout(() => location.href = "/aips/process-payment", 1200)</script>';
const WRAPPER = (delay = 1500) => `<p>Paying Amazon: ₹703.00</p><p>Complete your payment in 08:55 mins</p><div id="bank"></div>
  <a href="/aips/process-payment">Complete transaction on your bank website</a>
  <script>addEventListener('message', event => { if (event.data === 'paid') location.href = '/thankyou'; });
  setTimeout(() => { const f = document.createElement('iframe'); f.src = 'https://acs.issuer.example/acs/challenge'; f.width = 500; f.height = 300;
    document.getElementById('bank').append(f); }, ${delay})</script>`;
// A typical ACS page: the field's only label is page text.
const ACS = (error = '') => `${error}<p>One Time Password (OTP) has been sent to your mobile number XXXXXX4242</p>
  <form method="post" action="/acs/verify"><input type="password" name="txtOtpPassword" maxlength="6" autocomplete="off">
  <button type="submit">Submit</button><button type="button">Resend OTP</button><button type="button">Cancel</button></form>`;
const VERIFY = request => new URLSearchParams(request.postData() ?? '').get('txtOtpPassword') === '482913'
  ? '<p>Authenticated</p><script>parent.postMessage("paid", "*")</script>'
  : ACS('<p>Incorrect OTP. Please try again.</p>');
const THANKS = '<h1>Order placed, thank you!</h1><p>Order number: 404-4245310-6116300</p>';

test('the pay click is watched through the processing page until the issuer frame asks for the code', { skip }, async () => {
  const s = await site({
    'https://www.amazon.in/checkout': CHECKOUT,
    'POST https://www.amazon.in/place': PROCESSING,
    'https://www.amazon.in/aips/process-payment': WRAPPER(),
    'https://acs.issuer.example/acs/challenge': ACS(),
    'POST https://acs.issuer.example/acs/verify': VERIFY,
    'https://www.amazon.in/thankyou': THANKS,
  });
  try {
    await s.page.goto('https://www.amazon.in/checkout');
    const sign = new SignIn(s.live);
    const baseline = await sign.pay({ origin: 'https://www.amazon.in' });
    const started = Date.now();
    const outcome = await sign.awaitPaymentOutcome(baseline, { watchMs: 20000, pollMs: 250 });
    assert.equal(outcome.state, 'otp_required');
    assert.ok(Date.now() - started > 1500, 'the code page came after the processing page');
    assert.equal(new URL(s.page.url()).pathname, '/aips/process-payment');
    // A wrong code: the bank shows a fresh code field.
    const wrong = await sign.awaitPaymentOutcome(await sign.submitPaymentCode('000000', ['https://www.amazon.in']), { watchMs: 10000, pollMs: 200 });
    assert.equal(wrong.state, 'otp_required');
    assert.equal(wrong.sent, false);
    assert.equal(s.posts.at(-1).body.txtOtpPassword, '000000');
    const done = await sign.awaitPaymentOutcome(await sign.submitPaymentCode('482913', ['https://www.amazon.in']), { watchMs: 15000, pollMs: 200 });
    assert.equal(done.state, 'confirmed');
    assert.equal(done.orderReference, '404-4245310-6116300');
    assert.equal(s.posts.at(-1).url, 'https://acs.issuer.example/acs/verify');
  } finally { await s.close(); }
});

test('the issuer\'s own top-level page with an unlabelled numeric field', { skip }, async () => {
  const s = await site({
    'https://shop.example/checkout': CHECKOUT,
    'POST https://shop.example/place': '<p>Redirecting to your bank. Please wait.</p><script>setTimeout(() => location.href = "https://secure.issuer.example/3ds/otp", 1000)</script>',
    'https://secure.issuer.example/3ds/otp': `<p>Enter OTP sent to +91 XXXXX42</p><form method="post" action="/3ds/verify">
      <input type="text" name="q" placeholder="Search help" style="display:none"><input type="tel" inputmode="numeric" maxlength="6">
      <button type="submit">Verify</button></form>`,
    'POST https://secure.issuer.example/3ds/verify': '<script>location.href = "https://shop.example/thanks"</script>',
    'https://shop.example/thanks': '<h1>Thank you for your order</h1><p>Order number: ORD-778812</p>',
  });
  try {
    await s.page.goto('https://shop.example/checkout');
    const sign = new SignIn(s.live);
    const outcome = await sign.awaitPaymentOutcome(await sign.pay({ origin: 'https://shop.example' }), { watchMs: 15000, pollMs: 250 });
    assert.equal(outcome.state, 'otp_required');
    assert.equal(new URL(s.page.url()).origin, 'https://secure.issuer.example');
    await assert.rejects(sign.submitPaymentCode('482913', ['https://shop.example']), error => error.notActionable === true, 'only on the payment\'s pages');
    const done = await sign.awaitPaymentOutcome(await sign.submitPaymentCode('482913', ['https://shop.example', 'https://secure.issuer.example']), { watchMs: 15000, pollMs: 200 });
    assert.equal(done.state, 'confirmed');
    assert.equal(done.orderReference, 'ORD-778812');
  } finally { await s.close(); }
});

// The whole service on a real page: pay returns clicked before the bank page
// is up, a later snapshot flags the code, and the code goes into the frame.
test('service: a snapshot flags the issuer frame\'s code field and submit-payment-code fills it', { skip }, async () => {
  const s = await site({
    'https://www.amazon.in/checkout': CHECKOUT,
    'POST https://www.amazon.in/place': PROCESSING,
    'https://www.amazon.in/aips/process-payment': WRAPPER(3000),
    'https://acs.issuer.example/acs/challenge': ACS(),
    'POST https://acs.issuer.example/acs/verify': VERIFY,
    'https://www.amazon.in/thankyou': THANKS,
  });
  const root = mkdtempSync(join(tmpdir(), 'live-browser-payment-'));
  roots.push(root);
  const api = {
    register: async () => ({ sessionId: 'b-1', grant: 'g', expiresAt: new Date(Date.now() + 3600000).toISOString(), tenantId: '7', agentId: 'agent-1', conversationId: 'c-1' }),
    extend: async () => ({ expiresAt: new Date(Date.now() + 3600000).toISOString() }),
    revoke: async () => {}, handoffNotice: async () => ({}), sessionURL: id => `http://cortex/${id}/`,
  };
  const deps = {
    createDesktop: async () => ({ display: ':42', close: () => {} }),
    createAudioRoute: async () => ({ sink: 'sink', source: 'source', close: async () => {} }),
    launch: async () => ({ pages: () => [s.page], newPage: async () => s.page, on: () => {}, close: async () => {} }),
    openVideo: () => ({ close() {}, async *[Symbol.asyncIterator]() {} }), openRFB: async () => { throw new Error('none'); },
    detectIntervention: async () => undefined,
  };
  const service = new LiveBrowserService({ api, authorize: async () => ({}), deps, tenantID: '7', closeTimeoutMs: 50,
    profile: new PersistentBrowserProfile({ root }), paymentWatchMs: 1500, paymentPollMs: 250 });
  const bindings = { CORTEX_HOST_INVOCATIONS: JSON.stringify({ 'host:browser': 'invocation' }) };
  const run = (action, input) => service.execute(action, { runID: 'run-1', agentID: 'agent-1', input, bindings });
  try {
    await run('live-browser-start', { url: 'https://www.amazon.in/checkout' });
    const paid = await run('live-browser-pay', { sessionId: 'b-1', amount: '703.00', currency: 'INR', merchant: 'https://www.amazon.in', intent: 'Place the order' });
    assert.equal(paid.status, 'clicked');
    let snapshot;
    for (let attempt = 0; attempt < 40 && !snapshot?.paymentChallenge; attempt++) {
      snapshot = await run('live-browser-snapshot', { sessionId: 'b-1' });
      if (!snapshot.paymentChallenge) await new Promise(resolve => setTimeout(resolve, 250));
    }
    assert.equal(snapshot.paymentChallenge, 'otp_required');
    assert.match(snapshot.message, /Ask the user in chat: "Might have gotten an OTP, please provide".*live-browser-submit-payment-code/);
    assert.equal(snapshot.requiresHuman, false);
    assert.ok(!snapshot.elements.some(element => /otp/i.test(element.name)), 'the code field is in the issuer frame, not in the snapshot');
    const done = await run('live-browser-submit-payment-code', { sessionId: 'b-1', oneTimeCode: '482 913' });
    assert.deepEqual([done.status, done.orderReference], ['confirmed', '404-4245310-6116300']);
    assert.equal(s.posts.at(-1).body.txtOtpPassword, '482913');
  } finally {
    await service.closeAll();
    await s.close();
  }
});

test('bank code fields are recognized; other fields are not', { skip }, async () => {
  const page = await browser.newPage();
  const scan = async body => { await page.setContent(html(body)); return page.evaluate(`(${SCAN_OTP_JS})()`); };
  try {
    for (const body of [
      '<input name="otpValue" type="tel">',
      '<input id="txtOtp" type="password" maxlength="6">',
      '<input autocomplete="one-time-code">',
      '<label>Enter the OTP sent to your mobile for card ending 1001 <input name="x"></label>',
      '<label>One Time Password <input name="pwd" type="password"></label>',
      '<p>An OTP has been sent to your registered mobile</p><input type="text" name="field1">',
      '<p>Enter the 6-digit code sent to your phone</p><input name="a" placeholder="Search"><input name="b" inputmode="numeric" maxlength="6"><input name="c">',
    ]) assert.equal((await scan(body)).code, true, body);
    const split = await scan('<p>Enter OTP</p><form>' + Array.from({ length: 6 }, (_, i) => `<input name="d${i}" maxlength="1">`).join('') + '<button>Verify</button></form>');
    assert.deepEqual([split.code, split.split, split.submit], [true, 6, true]);
    for (const body of [
      '<input name="q" placeholder="Search">',
      '<label>Gift card code <input name="gc"></label>',
      '<label>CVV <input name="cvv" maxlength="3"></label>',
      '<label>Mobile number <input name="m" autocomplete="tel"></label>',
      '<p>Order Total ₹703</p><input name="promo" placeholder="Promo code">',
      '<input name="footprint">',
    ]) assert.equal((await scan(body)).code, false, body);
    assert.equal((await scan('<p>Approve this payment in your HDFC Bank mobile app</p>')).app, true);
  } finally { await page.close(); }
});
