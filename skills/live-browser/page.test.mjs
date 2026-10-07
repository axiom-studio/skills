import assert from 'node:assert/strict';
import { test } from 'node:test';
import { detectChallenges, LivePage, navigationURL, SETTLE_JS, SNAPSHOT_JS } from './page.mjs';

function fakePage(raw) {
  const events = [];
  const locator = ref => ({
    first() { return this; },
    scrollIntoViewIfNeeded: async () => {},
    boundingBox: async () => ({ x: 10, y: 20, width: 100, height: 20 }),
    click: async () => events.push(['locator-click', ref]),
    evaluate: async () => events.typed ?? '',
    selectOption: async option => (option.label === 'Economy' ? ['economy'] : []),
  });
  return { events, page: {
    url: () => raw.url, title: async () => raw.title, waitForLoadState: async () => {},
    evaluate: async script => (typeof script === 'string' && script.startsWith('((limits)') ? structuredClone(raw) : undefined),
    locator: selector => locator(selector),
    mouse: { click: async (x, y) => events.push(['click', x, y]), wheel: async (dx, dy) => events.push(['wheel', dx, dy]) },
    keyboard: { press: async key => events.push(['press', key]), type: async text => { events.push(['type', text.length]); events.typed = text; } },
    screenshot: async () => Buffer.from('jpeg'), viewportSize: () => ({ width: 1280, height: 800 }),
  } };
}

const raw = { url: 'https://flights.example.com/search', title: 'Search', text: 'Find flights',
  elements: [
    { ref: 1, role: 'textbox', name: 'From', context: 'form', href: '', inViewport: true, bounds: {}, state: {} },
    { ref: 2, role: 'link', name: 'Deals', context: 'nav', href: 'https://flights.example.com/deals', inViewport: true, bounds: {}, state: {} },
    { ref: 3, role: 'link', name: 'Partner', context: 'nav', href: 'https://partner.example.org/', inViewport: false, bounds: {}, state: {} },
    { ref: 4, role: 'textbox', name: 'Card number', context: 'form', href: '', inViewport: true, bounds: {}, state: { autocomplete: 'cc-number' } },
    { ref: 5, role: 'combobox', name: 'Cabin', context: 'form', href: '', inViewport: true, bounds: {}, state: {} },
  ] };

test('browser-side scripts are valid functions', () => {
  for (const script of [SNAPSHOT_JS, SETTLE_JS]) assert.equal(typeof new Function(`return ${script}`)(), 'function');
});

test('snapshots use generation-scoped references and the established output shape', async () => {
  const { page } = fakePage(raw);
  const live = new LivePage(page);
  const snapshot = await live.snapshot({ includeScreenshot: true });
  assert.equal(snapshot.generation, 1);
  assert.match(snapshot.observationDigest, /^sha256:[0-9a-f]{64}$/);
  assert.deepEqual(snapshot.elements.map(e => e.ref), ['s1:e1', 's1:e2', 's1:e3', 's1:e4', 's1:e5']);
  assert.deepEqual(snapshot.elements[1], { ref: 's1:e2', role: 'link', name: 'Deals', context: 'nav', inViewport: true, bounds: {}, state: {},
    destinationScope: 'same_origin', destinationPath: '/deals' });
  assert.equal(snapshot.elements[2].destinationScope, 'external_origin');
  assert.equal('href' in snapshot.elements[1], false);
  assert.deepEqual(snapshot.challenges, []);
  assert.equal(snapshot.modelMedia.mediaType, 'image/jpeg');
  assert.equal((await live.snapshot()).generation, 2);
  await assert.rejects(live.click({ target: 's1:e2' }), /stale/);
});

test('fill types into fields but refuses payment, password and identity fields', async () => {
  const { page, events } = fakePage(raw);
  const live = new LivePage(page);
  await live.snapshot();
  assert.deepEqual(await live.fill({ target: 's1:e1', value: 'Mumbai' }), { retained: true });
  assert.deepEqual(events.filter(e => e[0] === 'type'), [['type', 6]]);
  await live.snapshot();
  await assert.rejects(live.fill({ target: 's2:e4', value: '4111111111111111' }), error => error.expose && /request-handoff/.test(error.message));
  await assert.rejects(live.fill({ target: 's2:e2', value: 'x' }), /not an editable field/);
  assert.equal(events.filter(e => e[0] === 'type').length, 1);
});

test('select, coordinate click and scroll are bounded', async () => {
  const { page, events } = fakePage(raw);
  const live = new LivePage(page);
  await live.snapshot();
  assert.deepEqual(await live.select({ target: 's1:e5', value: 'Economy' }), { selected: 1 });
  await live.snapshot();
  await assert.rejects(live.select({ target: 's2:e5', value: 'Lounge' }), /Option not found/);
  await live.click({ generation: 2, x: 40, y: 50 });
  await assert.rejects(live.click({ generation: 1, x: 1, y: 1 }), /stale/);
  await live.scroll({ dy: 600 });
  await assert.rejects(live.scroll({ dy: 1e6 }), /within/);
  assert.deepEqual(events.filter(e => e[0] !== 'press'), [['click', 40, 50], ['wheel', 0, 600]]);
});

test('navigation accepts only HTTP(S) URLs without credentials; challenges are typed', () => {
  assert.equal(navigationURL('https://example.com/a'), 'https://example.com/a');
  for (const value of ['javascript:alert(1)', 'file:///etc/passwd', 'https://user:pw@example.com/', 'not a url']) {
    assert.throws(() => navigationURL(value), /HTTP/);
  }
  assert.deepEqual(detectChallenges('Please verify you are human'), ['captcha']);
  assert.deepEqual(detectChallenges('Enter your verification code'), ['mfa']);
  assert.deepEqual(detectChallenges('Checking your browser before accessing'), ['anti_bot']);
  assert.deepEqual(detectChallenges('Our Cloudflare outage report and 2FA guide'), []);
});
