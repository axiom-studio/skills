import assert from 'node:assert/strict';
import { test } from 'node:test';
import { detectChallenges, elementProblem, FOCUS_JS, FOCUSED_VALUE_JS, LivePage, navigationURL, SETTLE_JS, SNAPSHOT_JS } from './page.mjs';

// options.focus: what a click focuses ('field', 'editable', 'none');
// options.box: element box (null when hidden); options.focusable: whether the
// target or an editable inside it can be focused directly.
function fakePage(raw, options = {}) {
  const events = [];
  const state = { focus: 'none' };
  const locator = ref => ({
    first() { return this; },
    scrollIntoViewIfNeeded: async () => {},
    boundingBox: async () => (options.box === undefined ? { x: 10, y: 20, width: 100, height: 20 } : options.box),
    click: async () => { if (options.locatorClick) throw options.locatorClick; events.push(['locator-click', ref]); },
    evaluate: async () => {
      events.push(['focus-editable']);
      if (options.focusable === false || /"[23]"/.test(ref)) return false; // links
      state.focus = options.focus ?? 'field';
      return true;
    },
    selectOption: async option => (option.label === 'Economy' ? ['economy'] : []),
  });
  return { events, state, page: {
    url: () => raw.url, title: async () => raw.title, waitForLoadState: async () => {},
    evaluate: async script => {
      if (typeof script === 'string' && script.startsWith('((limits)')) return structuredClone(raw);
      if (script === `(${FOCUS_JS})()`) return state.focus;
      if (script === `(${FOCUSED_VALUE_JS})()`) return state.focus === 'editable' ? `${events.typed ?? ''}\n` : (events.typed ?? '');
      return undefined;
    },
    locator: selector => locator(selector),
    mouse: { click: async (x, y) => { events.push(['click', x, y]); state.focus = options.clickFocus ?? options.focus ?? 'field'; },
      wheel: async (dx, dy) => events.push(['wheel', dx, dy]) },
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

const notActionable = pattern => error => error.notActionable === true && error.expose === true && pattern.test(error.reason) && error.hint.length > 0;

test('element problems are not actionable, browser faults stay errors', async () => {
  const timeout = message => Object.assign(new Error(message), { name: 'TimeoutError' });
  assert.match(elementProblem(timeout('locator.click: Timeout 5000ms exceeded.\n  - element is not visible\n  - retrying')).reason, /not visible/);
  assert.match(elementProblem(new Error('<div class="overlay"> intercepts pointer events')).reason, /covered/);
  assert.match(elementProblem(new Error('Element is not attached to the DOM')).reason, /no longer on the page/);
  assert.match(elementProblem(new Error('Element is not editable')).reason, /not editable/);
  assert.match(elementProblem(timeout('Timeout 5000ms exceeded.')).reason, /Timed out/);
  for (const fault of ['Target page, context or browser has been closed', 'Browser has disconnected', 'Target crashed']) {
    const error = new Error(fault);
    assert.equal(elementProblem(error), error);
  }
  const other = new Error('Protocol error: unknown');
  assert.equal(elementProblem(other), other);

  const { page } = fakePage(raw, { box: null, locatorClick: timeout('locator.click: Timeout 5000ms exceeded.\n  - element is not visible') });
  const live = new LivePage(page);
  await live.snapshot();
  await assert.rejects(live.click({ target: 's1:e2' }), notActionable(/not visible/));
  await live.snapshot();
  await assert.rejects(live.click({ target: 's1:e2' }), notActionable(/stale/));
  await assert.rejects(live.click({ generation: 1, x: 1, y: 1 }), notActionable(/stale/));
  const closed = fakePage(raw, { locatorClick: new Error('Target page, context or browser has been closed'), box: null });
  const gone = new LivePage(closed.page);
  await gone.snapshot();
  await assert.rejects(gone.click({ target: 's1:e2' }), error => error.notActionable !== true && /closed/.test(error.message));
});

test('fill types into a contenteditable comment editor and confirms it kept the text', async () => {
  const editor = { ...raw, elements: [{ ref: 1, role: 'textbox', name: 'Join the conversation', context: 'form', href: '', inViewport: true, bounds: {}, state: {} },
    { ref: 6, role: 'shreddit-composer', name: 'Comment', context: 'form', href: '', inViewport: true, bounds: {}, state: {} }] };
  const { page, events } = fakePage(editor, { focus: 'editable' });
  const live = new LivePage(page);
  await live.snapshot();
  assert.deepEqual(await live.fill({ target: 's1:e1', value: 'Great  pup!' }), { retained: true, editor: 'rich_text' });
  assert.deepEqual(events.filter(e => ['click', 'press', 'type'].includes(e[0])), [['click', 60, 30], ['press', 'ControlOrMeta+A'], ['press', 'Backspace'], ['type', 11]]);
  // A wrapper element that holds an editor is filled too.
  await live.snapshot();
  assert.equal((await live.fill({ target: 's2:e6', value: 'Hi' })).retained, true);
});

test('fill of a hidden or collapsed editor is not actionable unless it can be focused', async () => {
  const collapsed = fakePage(raw, { box: null, focusable: false });
  const live = new LivePage(collapsed.page);
  await live.snapshot();
  await assert.rejects(live.fill({ target: 's1:e1', value: 'x' }), notActionable(/not visible/));
  assert.equal(collapsed.events.filter(e => e[0] === 'type').length, 0);
  // Hidden behind a placeholder but focusable: focus it directly and type.
  const hidden = fakePage(raw, { box: null, focus: 'editable' });
  const focused = new LivePage(hidden.page);
  await focused.snapshot();
  assert.equal((await focused.fill({ target: 's1:e1', value: 'hello' })).retained, true);
  assert.equal(hidden.events.filter(e => e[0] === 'click').length, 0);
  // A click that leaves focus nowhere editable, and a read-only field.
  const nowhere = fakePage(raw, { clickFocus: 'none', focusable: false });
  const noFocus = new LivePage(nowhere.page);
  await noFocus.snapshot();
  await assert.rejects(noFocus.fill({ target: 's1:e1', value: 'x' }), notActionable(/did not take keyboard focus/));
  const readonly = fakePage(raw, { focus: 'readonly' });
  const ro = new LivePage(readonly.page);
  await ro.snapshot();
  await assert.rejects(ro.fill({ target: 's1:e1', value: 'x' }), notActionable(/not editable/));
});
