import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import { mkdtempSync, rmSync } from 'node:fs';
import { readFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { after, test } from 'node:test';
import { PersistentBrowserProfile } from '@axiom/live-browser';
import { LiveBrowserService } from './service.mjs';

const roots = [];
after(() => { for (const root of roots) rmSync(root, { recursive: true, force: true }); });

const later = () => new Date(Date.now() + 3600000).toISOString();

function fakePage() {
  const page = new EventEmitter();
  page.current = 'about:blank';
  page.url = () => page.current;
  page.title = async () => (page.current === 'about:blank' ? '' : 'Flights');
  const main = { url: () => page.current };
  page.mainFrame = () => main;
  page.goto = async url => { page.current = url; page.emit('framenavigated', main); return { status: () => 200 }; };
  page.waitForLoadState = async () => {};
  page.snapshot = { url: 'https://www.reddit.com/r/aww/comments/1', title: 'Cute', text: 'Comments', elements: [
    { ref: 1, role: 'textbox', name: 'Join the conversation', context: 'form', href: '', inViewport: false, bounds: {}, state: {} }] };
  // Sign-in and card scans: queued results of the page scripts.
  page.scans = [];
  page.cardScan = {};
  page.bodyText = '';
  page.typed = [];
  page.evaluate = async script => {
    if (typeof script !== 'string') return 'none';
    if (script.startsWith('((limits)')) return structuredClone(page.snapshot);
    if (script.includes('__liveSignIn')) return page.scans.shift() ?? {};
    if (script.includes('__liveCard')) return structuredClone(page.cardScan);
    if (script.includes('innerText.slice(0, 200000)')) return page.bodyText;
    if (script.includes('__livePay')) return page.payCount ?? 0;
    if (script.includes('__liveOtp')) return page.otpScan ?? {};
    if (script.startsWith('((x, y)')) return page.pointName ?? '';
    return 'none';
  };
  page.box = null;
  page.locator = selector => ({ first() { return this; }, scrollIntoViewIfNeeded: async () => {}, boundingBox: async () => page.box,
    evaluate: async () => { if (page.fault) throw page.fault; return false; },
    click: async () => {
      if (selector.includes('data-live-otp')) { if (selector.includes('submit')) { page.onCode?.(); page.typed.push(['submit-code']); } return; }
      if (/data-live-(signin|card)/.test(selector)) { if (selector.includes('submit')) page.typed.push(['submit']); return; }
      if (selector.includes('data-live-pay') || (page.clickable && selector.includes('data-live-ref'))) {
        page.onPay?.(); page.clicked = [...(page.clicked ?? []), selector]; return;
      }
      throw page.fault ?? Object.assign(new Error('Timeout 5000ms exceeded.\n - element is not visible'), { name: 'TimeoutError' });
    },
    focus: async () => {}, fill: async () => {}, press: async key => page.typed.push(['press', selector, key]),
    pressSequentially: async text => { page.onType?.(selector); page.typed.push([selector, text]); },
    selectOption: async value => page.typed.push([selector, value]) });
  page.mouse = { click: async () => {}, wheel: async () => {} };
  page.keyboard = { press: async () => {}, type: async () => {} };
  page.frames = () => [main];
  main.evaluate = script => page.evaluate(script);
  main.locator = selector => page.locator(selector);
  main.isDetached = () => false;
  page.screenshot = async () => Buffer.from('jpeg');
  page.viewportSize = () => ({ width: 1280, height: 800 });
  return page;
}

function harness({ agentId = 'agent-1', conversationId = 'conv-1', detect, leaseMs, noticeFails = false, launchGate, profileWaitMs, closeGate, authorizeProfile,
  lifetime = {}, expiresInMs = 3600000, extendFails = false } = {}) {
  const calls = [];
  let sessions = 0, requests = 0;
  const conversation = { id: conversationId };
  const root = mkdtempSync(join(tmpdir(), 'live-browser-profile-'));
  roots.push(root);
  const profile = new PersistentBrowserProfile({ root });
  const api = {
    async register({ invocation, durationMinutes }) {
      calls.push(['register', invocation, durationMinutes]);
      sessions++;
      return { sessionId: `b-${sessions}`, grant: `grant-${sessions}`, expiresAt: new Date(Date.now() + expiresInMs).toISOString(), tenantId: '7', agentId, conversationId: conversation.id };
    },
    async extend(session, { durationMinutes }) {
      calls.push(['extend', session.sessionId, session.grant, durationMinutes]);
      if (extendFails) throw Object.assign(new Error('Cortex browser API refused the request (HTTP 410)'), { status: 410 });
      return { expiresAt: new Date(Date.now() + expiresInMs).toISOString() };
    },
    async revoke({ sessionId, grant }) { calls.push(['revoke', sessionId, grant]); },
    async handoffNotice(session, notice) {
      calls.push(['notice', session.grant, notice.handoffId, notice.summary]);
      if (noticeFails) throw Object.assign(new Error('secret summary'), { status: 503 });
      return { posted: true };
    },
    async audioCatalogGrant() { calls.push(['catalog']); return { token: 'c', expiresAt: later() }; },
    async audioGrants() { calls.push(['audio']); return { transcriptionToken: 't', speechToken: 's', expiresAt: later() }; },
    sessionURL: id => `http://cortex/browser/v1/sessions/${id}/`,
  };
  const pages = [];
  const contexts = [];
  const deps = {
    createDesktop: async () => ({ display: ':42', close: () => calls.push(['desktop-close']) }),
    createAudioRoute: async id => ({ sink: `lb_${id}_capture`, source: `lb_${id}_source`, commands: {}, close: async () => calls.push(['route-close']) }),
    launch: async (dir, options) => {
      calls.push(['launch', dir, options.display, options.audio.sink]);
      await launchGate;
      const page = fakePage(); pages.push(page);
      const context = { pages: () => [page], newPage: async () => page,
        close: async () => { calls.push(['context-close']); await closeGate; calls.push(['browser-exited']); } };
      contexts.push(context);
      return context;
    },
    openVideo: ({ signal }) => ({ close: () => {}, async *[Symbol.asyncIterator]() {
      yield Buffer.from('webm');
      await new Promise(resolve => signal.addEventListener('abort', resolve, { once: true }));
    } }),
    openRFB: async () => { throw new Error('no rfb'); },
    detectIntervention: detect ?? (async () => undefined),
    createAudio: options => {
      calls.push(['createAudio', options.speechBaseURL]);
      return { listening: false, setAgentLabel: label => calls.push(['label', label]), stopListening() { this.listening = false; },
        async listen(o) { this.listening = true; await options.grants.catalog(); await options.grants.audio({}); return { listening: true, speakerLabel: o.speakerLabel }; },
        async speak() { return { delivery: 'played' }; }, close() {} };
    },
  };
  const authorize = async ({ command, sessionGrant }) => {
    assert.match(sessionGrant, /^grant-/);
    return { userID: 'human-1', tenantID: '7', agentID: agentId, requestID: `r-${++requests}`, expiresAt: new Date(Date.now() + 15000).toISOString(), command };
  };
  const service = new LiveBrowserService({ api, authorize, deps, profile, humanWaitMs: 200, tenantID: '7', closeTimeoutMs: 50,
    authorizeProfile: authorizeProfile ?? (async ({ command }) => ({ userID: 'human-1', tenantID: '7', agentID: agentId,
      requestID: `p-${++requests}`, expiresAt: new Date(Date.now() + 15000).toISOString(), command })),
    ...(leaseMs ? { leaseMs } : {}), ...(profileWaitMs ? { profileWaitMs } : {}), ...lifetime });
  const control = (sessionId, command) => service.controlBrowser({ agentID: agentId, sessionID: sessionId,
    authorization: { token: 'proof', commandJSON: JSON.stringify(command) }, command });
  const bindings = { CORTEX_HOST_INVOCATIONS: JSON.stringify({ 'host:browser': 'invocation' }) };
  const run = (action, input, runID = 'run-1', extra = {}) => service.execute(action, { runID, agentID: agentId, input, bindings: { ...bindings, ...extra } });
  const profileCommand = command => service.controlProfile({ agentID: agentId,
    authorization: { token: 'proof', commandJSON: JSON.stringify(command) }, command });
  return { service, calls, pages, contexts, control, run, profile, root, conversation, profileCommand };
}

test('start registers the session, launches on a private display and reports rich status', async () => {
  const h = harness();
  const started = await h.run('live-browser-start', { url: 'https://flights.example.com/', intent: 'Open the flight search' });
  assert.equal(started.sessionId, 'b-1');
  assert.equal(started.status, 'automating');
  assert.equal(started.url, 'https://flights.example.com/');
  assert.deepEqual(h.calls[0], ['register', 'invocation', 30], 'a fixed idle window; the model sets no duration');
  assert.deepEqual(h.calls.find(call => call[0] === 'launch'), ['launch', h.root, ':42', 'lb_b1_capture'], 'the persistent profile on the volume');
  const status = await h.control('b-1', { type: 'status' });
  assert.deepEqual({ ...status, expiresAt: undefined }, { sessionId: 'b-1', status: 'automating', url: 'https://flights.example.com/',
    title: 'Flights', step: 'Open the flight search', profile: { state: 'shared', origins: ['example.com'] }, audio: { listening: false }, expiresAt: undefined });
  // The same run reuses its browser.
  const again = await h.run('live-browser-start', { url: 'https://flights.example.com/results' });
  assert.equal(again.sessionId, 'b-1');
  assert.equal(h.calls.filter(call => call[0] === 'register').length, 1);
  await h.service.closeAll();
});

test('status and the live view work while the browser is still launching', async () => {
  let launched;
  const h = harness({ launchGate: new Promise(resolve => { launched = resolve; }) });
  const starting = h.run('live-browser-start', { url: 'https://flights.example.com/' });
  while (!h.calls.some(call => call[0] === 'launch')) await new Promise(resolve => setImmediate(resolve));
  const status = await h.control('b-1', { type: 'status' });
  assert.equal(status.status, 'automating');
  assert.equal(status.url, '');
  const watch = await h.service.videoBrowser({ agentID: 'agent-1', sessionID: 'b-1', authorization: {}, command: { type: 'watch' } });
  const iterator = watch.stream[Symbol.asyncIterator]();
  assert.equal((await iterator.next()).value.toString(), 'webm', 'the display streams before Camoufox is up');
  launched();
  assert.equal((await starting).url, 'https://flights.example.com/');
  watch.close();
  await h.service.closeAll();
});

test('a later turn of the same conversation reattaches to the open browser and its page', async () => {
  const h = harness();
  await h.run('live-browser-start', { url: 'https://www.amazon.in/ap/signin' });
  const second = await h.run('live-browser-start', {}, 'run-2');
  assert.equal(second.sessionId, 'b-1');
  assert.equal(second.url, 'https://www.amazon.in/ap/signin', 'the same page');
  assert.ok(h.calls.some(call => call[0] === 'revoke' && call[1] === 'b-2'), 'the duplicate registration is revoked');
  assert.ok(!h.calls.some(call => call[0] === 'revoke' && call[1] === 'b-1'));
  assert.equal(h.calls.filter(call => call[0] === 'launch').length, 1);
  assert.equal(h.service.size, 1);
  assert.equal((await h.run('live-browser-snapshot', { sessionId: 'b-1' }, 'run-2')).sessionId, 'b-1');
  await h.service.closeAll();
});

test('start requires the host browser session grant and other agents cannot use a session', async () => {
  const h = harness();
  await assert.rejects(h.service.execute('live-browser-start', { runID: 'run-1', agentID: 'agent-1', input: {}, bindings: {} }), /host:browser:session/);
  await h.run('live-browser-start', {});
  await assert.rejects(h.service.execute('live-browser-snapshot', { runID: 'run-1', agentID: 'agent-2', input: { sessionId: 'b-1' }, bindings: {} }), /No live browser session/);
  await assert.rejects(h.service.controlBrowser({ agentID: 'agent-2', sessionID: 'b-1', authorization: {}, command: { type: 'status' } }), /could not be completed/);
  await h.service.closeAll();
});

test('model actions wait while a human holds control and return paused_by_user', async () => {
  const h = harness();
  await h.run('live-browser-start', {});
  const lease = await h.control('b-1', { type: 'claim' });
  assert.equal((await h.control('b-1', { type: 'status' })).status, 'human');
  assert.ok((await h.control('b-1', { type: 'status' })).lease.expiresAt);
  // Still held after the bounded wait: the kernel must wait for hand-back.
  const held = await h.run('live-browser-click', { sessionId: 'b-1', generation: 1, x: 5, y: 5, intent: 'Pick a date' });
  assert.equal(held.status, 'paused_by_user');
  assert.equal(held.requiresHuman, true);
  assert.deepEqual(held.challenge, { kind: 'manual_confirmation' });
  // Handed back during the wait: the action still does not act, and says to re-observe.
  const waiting = h.run('live-browser-navigate', { sessionId: 'b-1', url: 'https://other.example.com/' });
  await new Promise(resolve => setTimeout(resolve, 20));
  await h.control('b-1', { type: 'resume', leaseID: lease.id });
  const resumed = await waiting;
  assert.equal(resumed.status, 'paused_by_user');
  assert.equal(resumed.requiresHuman, false);
  assert.equal(h.pages[0].url(), 'about:blank', 'the paused action was not performed');
  const navigated = await h.run('live-browser-navigate', { sessionId: 'b-1', url: 'https://other.example.com/' });
  assert.equal(navigated.status, 'automating');
  await h.service.closeAll();
});

test('request-handoff waits for the user; hand-back resumes', async () => {
  const h = harness();
  await h.run('live-browser-start', { url: 'https://shop.example.com/' });
  const handoff = await h.run('live-browser-request-handoff', { sessionId: 'b-1', reason: 'payment', summary: 'Pay ₹4,210 for the 9:05 flight.' });
  assert.equal(handoff.status, 'awaiting_user');
  assert.equal(handoff.requiresHuman, true);
  assert.deepEqual(handoff.challenges, ['manual_confirmation']);
  assert.equal(handoff.intervention.summary, 'Pay ₹4,210 for the 9:05 flight.');
  const blocked = await h.run('live-browser-click', { sessionId: 'b-1', generation: 1, x: 5, y: 5, intent: 'Click pay' });
  assert.equal(blocked.status, 'awaiting_user');
  assert.equal(blocked.requiresHuman, false);
  const status = await h.control('b-1', { type: 'status' });
  assert.equal(status.intervention.reason, 'payment');
  const lease = await h.control('b-1', { type: 'claim' });
  await h.control('b-1', { type: 'resume', leaseID: lease.id });
  assert.equal((await h.control('b-1', { type: 'status' })).status, 'automating');
  await h.service.closeAll();
});

test('anti-bot challenges hand off automatically', async () => {
  const h = harness({ detect: async () => 'challenge' });
  const result = await h.run('live-browser-start', { url: 'https://shop.example.com/' });
  assert.equal(result.status, 'awaiting_user');
  assert.equal(result.intervention.reason, 'captcha');
  assert.equal(result.requiresHuman, true);
  await h.service.closeAll();
});

test('watch streams without a lease; lease video needs the owner; close revokes and ends streams', async () => {
  const h = harness();
  await h.run('live-browser-start', {});
  const watch = await h.service.videoBrowser({ agentID: 'agent-1', sessionID: 'b-1', authorization: {}, command: { type: 'watch' } });
  const iterator = watch.stream[Symbol.asyncIterator]();
  assert.equal((await iterator.next()).value.toString(), 'webm');
  await assert.rejects(h.service.videoBrowser({ agentID: 'agent-1', sessionID: 'b-1', authorization: {}, command: { type: 'video', leaseID: 'none' } })
    .then(video => video.stream[Symbol.asyncIterator]().next()), /unavailable/);
  await assert.rejects(h.service.videoBrowser({ agentID: 'agent-1', sessionID: 'b-1', authorization: {}, command: { type: 'watch', leaseID: 'x' } }), /unavailable/);
  await assert.rejects(h.service.videoBrowser({ agentID: 'agent-1', sessionID: 'b-1', authorization: {}, command: { type: 'watch' } }, true), /unavailable/);
  const pending = iterator.next();
  const closed = await h.run('live-browser-close', { sessionId: 'b-1' });
  assert.deepEqual(closed, { sessionId: 'b-1', status: 'none' });
  assert.equal((await pending).done, true);
  const order = h.calls.map(call => call[0]);
  assert.ok(order.indexOf('context-close') < order.indexOf('revoke'), 'Camoufox closes (and flushes the profile) before the session ends');
  assert.ok(order.includes('route-close') && order.includes('desktop-close'));
  assert.equal(h.profile.busy, false, 'the profile is free for the next browser');
  await assert.rejects(h.control('b-1', { type: 'status' }), /could not be completed/);
});

test('listen and speak use the session grant and gateway grants, with no provider credential', async () => {
  const h = harness();
  await h.run('live-browser-start', {});
  assert.deepEqual(await h.service.execute('live-browser-listen', { runID: 'run-1', agentID: 'agent-1',
    input: { sessionId: 'b-1', state: 'on', displayName: 'Ada', speakerLabel: 'Meeting participant' }, bindings: {} }),
  { sessionId: 'b-1', listening: true, speakerLabel: 'Meeting participant' });
  assert.ok(h.calls.some(call => call[0] === 'createAudio' && /llm-gateway/.test(call[1])));
  assert.ok(h.calls.some(call => call[0] === 'catalog') && h.calls.some(call => call[0] === 'audio'));
  assert.deepEqual(await h.service.execute('live-browser-speak', { runID: 'run-1', agentID: 'agent-1', input: { sessionId: 'b-1', text: 'Hello' }, bindings: {} }),
    { sessionId: 'b-1', delivery: 'played' });
  assert.deepEqual(await h.run('live-browser-listen', { sessionId: 'b-1', state: 'off' }), { sessionId: 'b-1', listening: false });
  await h.service.closeAll();
});

test('handoff posts a take-over notice', async () => {
  const h = harness();
  await h.run('live-browser-start', {});
  await h.run('live-browser-request-handoff', { sessionId: 'b-1', reason: 'submit', summary: 'Confirm the booking.' });
  await new Promise(resolve => setImmediate(resolve));
  assert.deepEqual(h.calls.filter(call => call[0] === 'notice'), [['notice', 'grant-1', 'b-1:h1', 'Confirm the booking.']]);
  assert.deepEqual((await h.control('b-1', { type: 'status' })).profile, { state: 'new', origins: [] });
  await h.service.closeAll();
});

test('every entry into awaiting_user posts a best-effort handoff notice', async () => {
  const warnings = [];
  const warn = console.warn;
  console.warn = line => warnings.push(line);
  try {
    const h = harness({ detect: async () => 'challenge', leaseMs: 1000, noticeFails: true });
    const challenged = await h.run('live-browser-start', { url: 'https://shop.example.com/' });
    assert.equal(challenged.status, 'awaiting_user', 'a failing notice never blocks the handoff');
    const lease = await h.control('b-1', { type: 'claim' });
    await h.control('b-1', { type: 'resume', leaseID: lease.id });
    // Lease expiry pauses the agent and also notifies.
    await h.control('b-1', { type: 'claim' });
    await new Promise(resolve => setTimeout(resolve, 1300));
    assert.equal((await h.control('b-1', { type: 'status' })).status, 'awaiting_user');
    const notices = h.calls.filter(call => call[0] === 'notice');
    assert.deepEqual(notices.map(call => call[2]), ['b-1:h1', 'b-1:h2']);
    assert.equal(notices[0][3], 'The website requires a human verification step.');
    assert.match(notices[1][3], /control ended/);
    assert.ok(warnings.length >= 2 && warnings.every(line => !/secret|summary|verification/.test(line)));
    await h.service.closeAll();
  } finally { console.warn = warn; }
});

test('an element the agent cannot use returns not_actionable; the run can recover', async () => {
  const h = harness();
  await h.run('live-browser-start', { url: 'https://www.reddit.com/r/aww/comments/1' });
  const snapshot = await h.run('live-browser-snapshot', { sessionId: 'b-1' });
  const target = snapshot.elements[0].ref;
  const fill = await h.run('live-browser-fill', { sessionId: 'b-1', target, value: 'So cute!', intent: 'Draft a supportive comment' });
  assert.equal(fill.status, 'not_actionable');
  assert.equal(fill.browserStatus, 'automating');
  assert.equal(fill.reason, 'Element is not visible');
  assert.match(fill.hint, /click what expands it/);
  assert.equal(fill.url, 'https://www.reddit.com/r/aww/comments/1');
  assert.equal(fill.title, 'Flights');
  assert.equal(fill.requiresHuman, false);
  for (const key of ['error', 'success', 'isError']) assert.equal(key in fill, false, 'never an explicit failure envelope');
  const click = await h.run('live-browser-click', { sessionId: 'b-1', target, intent: 'Open the comment box' });
  assert.equal(click.status, 'not_actionable');
  const stale = await h.run('live-browser-click', { sessionId: 'b-1', target: 's9:e1', intent: 'Click an old reference' });
  assert.deepEqual([stale.status, stale.reason], ['not_actionable', 'Element reference is stale']);
  // The session still works.
  assert.equal((await h.run('live-browser-snapshot', { sessionId: 'b-1' })).generation, 2);
  // A browser fault is still an error.
  h.pages[0].fault = new Error('Target page, context or browser has been closed');
  const fresh = await h.run('live-browser-snapshot', { sessionId: 'b-1' });
  await assert.rejects(h.run('live-browser-fill', { sessionId: 'b-1', target: fresh.elements[0].ref, value: 'x', intent: 'Type' }), /The browser action failed/);
  await h.service.closeAll();
});

test('sessions of other conversations queue for the one browser profile', async () => {
  const h = harness({ profileWaitMs: 2000 });
  const first = await h.run('live-browser-start', { url: 'https://web.whatsapp.com/' });
  h.conversation.id = 'conv-2';
  let secondDone = false;
  const second = h.run('live-browser-start', { url: 'https://mail.example.com/' }, 'run-2').then(result => { secondDone = true; return result; });
  await new Promise(resolve => setTimeout(resolve, 50));
  assert.equal(secondDone, false, 'waits while the first browser has the profile');
  assert.equal(h.calls.filter(call => call[0] === 'launch').length, 1);
  await h.run('live-browser-close', { sessionId: first.sessionId });
  const started = await second;
  assert.equal(started.sessionId, 'b-2');
  assert.equal(started.status, 'automating');
  const order = h.calls.map(call => call[0]);
  assert.ok(order.indexOf('browser-exited') < order.lastIndexOf('launch'), 'the next browser launches only after Firefox exited');
  assert.deepEqual(h.profile.status.origins, ['example.com', 'whatsapp.com']);
  await h.service.closeAll();
});

test('a start that cannot get the profile in time revokes its session and says the browser is busy', async () => {
  const h = harness({ profileWaitMs: 30 });
  await h.run('live-browser-start', {});
  h.conversation.id = 'conv-2';
  await assert.rejects(h.run('live-browser-start', {}, 'run-2'), /in use by another task/);
  assert.ok(h.calls.some(call => call[0] === 'revoke' && call[1] === 'b-2'));
  assert.equal(h.service.size, 1);
  await h.service.closeAll();
});

test('the profile stays leased until a slow Firefox has really exited', async () => {
  let exit;
  const h = harness({ profileWaitMs: 2000, closeGate: new Promise(resolve => { exit = resolve; }) });
  await h.run('live-browser-start', {});
  await h.run('live-browser-close', { sessionId: 'b-1' });
  assert.ok(h.calls.some(call => call[0] === 'revoke'), 'the session ends after the bounded close wait');
  assert.equal(h.profile.busy, true);
  h.conversation.id = 'conv-2';
  const next = h.run('live-browser-start', {}, 'run-2');
  await new Promise(resolve => setTimeout(resolve, 30));
  assert.equal(h.calls.filter(call => call[0] === 'launch').length, 1);
  exit();
  assert.equal((await next).sessionId, 'b-2');
  await h.service.closeAll();
});

test('closing while Camoufox is still launching closes it once it is up', async () => {
  let launched;
  const h = harness({ launchGate: new Promise(resolve => { launched = resolve; }) });
  const starting = h.run('live-browser-start', {}).then(() => undefined, error => error);
  while (!h.calls.some(call => call[0] === 'launch')) await new Promise(resolve => setImmediate(resolve));
  const closing = h.service.closeAll();
  launched();
  await closing;
  assert.match((await starting)?.message ?? '', /could not start/);
  assert.ok(h.calls.some(call => call[0] === 'context-close'));
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(h.profile.busy, false);
});

test('profile status lists visited top-level sites with no browser open', async () => {
  const h = harness();
  assert.deepEqual(await h.profileCommand({ type: 'profileStatus' }), { state: 'none', origins: [], updatedAt: null, sizeBytes: 0 });
  await h.run('live-browser-start', { url: 'https://web.whatsapp.com/' });
  await h.run('live-browser-navigate', { sessionId: 'b-1', url: 'https://accounts.google.com/' });
  await h.service.closeAll();
  const status = await h.profileCommand({ type: 'profileStatus' });
  assert.equal(status.state, 'shared');
  assert.deepEqual(status.origins, ['google.com', 'whatsapp.com']);
  assert.ok(Date.parse(status.updatedAt) > 0);
  assert.deepEqual(JSON.parse(await readFile(join(h.root, 'sites.json'), 'utf8')).sites['whatsapp.com'] !== undefined, true);
});

test('forget stops the open browser cleanly, then deletes the profile', async () => {
  const h = harness();
  await h.run('live-browser-start', { url: 'https://web.whatsapp.com/' });
  const forgotten = await h.profileCommand({ type: 'forgetProfile' });
  assert.deepEqual(forgotten, { deleted: true });
  const order = h.calls.map(call => call[0]);
  assert.ok(order.indexOf('browser-exited') >= 0 && order.indexOf('browser-exited') < order.indexOf('revoke') + 1);
  assert.equal(h.service.size, 0);
  assert.deepEqual(await h.profileCommand({ type: 'profileStatus' }), { state: 'none', origins: [], updatedAt: null, sizeBytes: 0 });
  assert.deepEqual(await h.profileCommand({ type: 'forgetProfile' }), { deleted: false });
});

test('profile commands need a verified single-use proof for this runtime tenant', async () => {
  const proofs = [];
  const reply = { userID: 'human-1', tenantID: '7', agentID: 'agent-1', requestID: 'p-1', expiresAt: new Date(Date.now() + 15000).toISOString() };
  const h = harness({ authorizeProfile: async input => { proofs.push(input); return { ...reply }; } });
  await h.profileCommand({ type: 'profileStatus' });
  assert.equal(proofs[0].tenantID, '7');
  assert.equal(proofs[0].authorization.commandJSON, '{"type":"profileStatus"}');
  await assert.rejects(h.profileCommand({ type: 'profileStatus' }), /could not be completed/, 'a replayed proof is refused');
  for (const command of [{ type: 'status' }, { type: 'forgetProfile', extra: 1 }, { type: 'claim' }]) {
    await assert.rejects(h.profileCommand(command), /could not be completed/);
  }
  reply.requestID = 'p-2';
  reply.tenantID = '8';
  await assert.rejects(h.profileCommand({ type: 'forgetProfile' }), /could not be completed/, 'another tenant');
  reply.tenantID = '7';
  reply.agentID = 'agent-2';
  await assert.rejects(h.profileCommand({ type: 'forgetProfile' }), /could not be completed/, 'another agent');
  const denied = harness({ authorizeProfile: async () => { throw new Error('denied'); } });
  await assert.rejects(denied.profileCommand({ type: 'forgetProfile' }), /could not be completed/);
  const unconfigured = new LiveBrowserService({ api: {}, authorize: async () => ({}), profile: new PersistentBrowserProfile({ root: h.root }),
    authorizeProfile: async () => reply });
  await assert.rejects(unconfigured.controlProfile({ agentID: 'agent-1', authorization: {}, command: { type: 'profileStatus' } }),
    /could not be completed/, 'no tenant configured: fail closed');
});

const SECRETS = ['kev@example.com', 'Hunter2-Secret!', '4242424242424242', '123'];
const noSecrets = result => { const text = JSON.stringify(result); for (const secret of SECRETS) assert.ok(!text.includes(secret), 'no credential value in the result'); };
const amazonLogin = { 'website-login-1': JSON.stringify({ username: 'kev@example.com', password: 'Hunter2-Secret!', website: 'https://www.amazon.in, https://amazon.in', name: 'Amazon India' }) };
const savedCard = { 'payment-card': JSON.stringify({ cardholderName: 'Kev K', number: '4242424242424242', expiryMonth: '07', expiryYear: '2029', cvc: '123' }) };

test('sign-in without a saved login for this exact site gives the exact in-chat credential request, never a handoff', async () => {
  const h = harness();
  await h.run('live-browser-start', { url: 'https://www.amazon.in/ap/signin' });
  const none = await h.run('live-browser-sign-in', { sessionId: 'b-1' });
  assert.equal(none.status, 'no_matching_login');
  assert.equal(none.origin, 'https://www.amazon.in');
  assert.equal(none.requiresHuman, false);
  assert.match(none.message, /do not use request_setup/);
  assert.match(none.message, /openseal\.skills\.request_credential/);
  assert.deepEqual(none.credentialRequest, { action: 'openseal.skills.request_credential',
    arguments: { kind: 'website_login', website: 'https://amazon.in', reason: 'Add your amazon.in login so I can sign in and continue' } });
  assert.match(none.message, /Do not hand off/);
  await h.run('live-browser-navigate', { sessionId: 'b-1', url: 'https://www.amazon.in.evil.example/ap/signin' });
  const lookalike = await h.run('live-browser-sign-in', { sessionId: 'b-1' }, 'run-1', amazonLogin);
  assert.equal(lookalike.status, 'no_matching_login');
  assert.deepEqual(h.pages[0].typed, [], 'nothing typed');
  assert.equal((await h.control('b-1', { type: 'status' })).status, 'automating');
  await h.service.closeAll();
});

test('sign-in fills the saved login, returns otp_required, and a later turn completes it with the code', async () => {
  const h = harness();
  await h.run('live-browser-start', { url: 'https://www.amazon.in/ap/signin' });
  const page = h.pages[0];
  page.scans.push({ username: true, password: true, otp: 0, submit: true, via: '' }, { otp: 1, split: false, submit: true, via: 'message' });
  const first = await h.run('live-browser-sign-in', { sessionId: 'b-1', intent: 'Sign in to Amazon' }, 'run-1', amazonLogin);
  assert.equal(first.status, 'otp_required');
  assert.equal(first.credential, 'Amazon India');
  assert.equal(first.step, 'password');
  assert.match(first.message, /Might have gotten an OTP, please provide/);
  assert.match(first.message, /Keep using sessionId b-1/);
  noSecrets(first);
  assert.deepEqual(page.typed.filter(entry => entry.length === 2), [['[data-live-signin="username"]', 'kev@example.com'], ['[data-live-signin="password"]', 'Hunter2-Secret!']]);
  // The turn ended; the user's reply starts a new run in the same conversation.
  const reattached = await h.run('live-browser-start', {}, 'run-2');
  assert.equal(reattached.sessionId, 'b-1');
  page.scans.push({ otp: 1, split: false, submit: true, via: 'message' }, {});
  const second = await h.run('live-browser-sign-in', { sessionId: 'b-1', oneTimeCode: '482 913' }, 'run-2', amazonLogin);
  assert.equal(second.status, 'submitted');
  assert.equal(second.step, 'one_time_code');
  assert.deepEqual(page.typed.at(-2), ['[data-live-signin="otp"]', '482913']);
  noSecrets(second);
  await h.service.closeAll();
});

test('a redirect to another site between keystroke batches stops the sign-in', async () => {
  const h = harness();
  await h.run('live-browser-start', { url: 'https://www.amazon.in/ap/signin' });
  const page = h.pages[0];
  page.scans.push({ username: true, password: true, submit: true });
  // The page navigates away as soon as the username is typed.
  page.onType = selector => { if (selector.includes('username')) page.current = 'https://evil.example/capture'; };
  const result = await h.run('live-browser-sign-in', { sessionId: 'b-1' }, 'run-1', amazonLogin);
  assert.equal(result.status, 'not_actionable');
  assert.match(result.reason, /another site/);
  assert.ok(!page.typed.some(entry => entry[0].includes('password')), 'the password was never typed');
  noSecrets(result);
  await h.service.closeAll();
});

test('several logins for one site: choose_login lists names only', async () => {
  const h = harness();
  await h.run('live-browser-start', { url: 'https://www.amazon.in/' });
  const result = await h.run('live-browser-sign-in', { sessionId: 'b-1' }, 'run-1', { ...amazonLogin,
    'website-login-2': JSON.stringify({ username: 'work@example.com', password: 'Other-Secret', website: 'https://www.amazon.in' }) });
  assert.equal(result.status, 'choose_login');
  assert.deepEqual(result.logins, ['Amazon India', 'website-login-2']);
  assert.ok(!JSON.stringify(result).includes('Other-Secret') && !JSON.stringify(result).includes('work@example.com'));
  await h.service.closeAll();
});

test('a one-time code page is not handed off automatically; a CAPTCHA still is', async () => {
  const h = harness({ detect: async () => 'verification' });
  const started = await h.run('live-browser-start', { url: 'https://www.amazon.in/ap/mfa' });
  assert.equal(started.status, 'automating');
  h.pages[0].snapshot = { url: 'https://www.amazon.in/ap/mfa', title: 'Verify', text: 'Enter verification code', elements: [] };
  const snapshot = await h.run('live-browser-snapshot', { sessionId: 'b-1' });
  assert.equal(snapshot.status, 'automating');
  assert.deepEqual(snapshot.challenges, ['mfa']);
  h.pages[0].snapshot = { url: 'https://www.amazon.in/ap/mfa', title: 'Verify', text: 'Please complete the captcha', elements: [] };
  assert.equal((await h.run('live-browser-snapshot', { sessionId: 'b-1' })).status, 'awaiting_user');
  await h.service.closeAll();
});

test('the saved card: in-chat credential request when missing, spend cap and amount checks, filled card is never shown', async () => {
  const h = harness();
  await h.run('live-browser-start', { url: 'https://www.amazon.in/checkout' });
  const page = h.pages[0];
  const pay = (extra, amount = '4210.00') => h.run('live-browser-fill-payment-card', { sessionId: 'b-1', amount, currency: 'INR' }, 'run-1', extra);
  const missing = await pay({});
  assert.equal(missing.status, 'no_payment_card');
  assert.match(missing.message, /openseal\.skills\.request_credential/);
  assert.equal(missing.credentialRequest.action, 'openseal.skills.request_credential');
  assert.equal(missing.credentialRequest.arguments.kind, 'payment_card');
  const capped = await pay({ 'payment-card': JSON.stringify({ error: 'spend_cap_exceeded', remaining: '1000.00', cap: '5000', currency: 'INR' }) });
  assert.deepEqual([capped.status, capped.remaining, capped.cap, capped.capCurrency], ['spend_cap_exceeded', '1000.00', '5000', 'INR']);
  assert.equal(capped.credentialRequest.arguments.kind, 'payment_card');
  assert.match(capped.credentialRequest.arguments.reason, /spend cap/);
  assert.match(capped.message, /After the cap is raised/);
  page.bodyText = 'Items: ₹4,000.00\nOrder Total: ₹4,910.00';
  const mismatch = await pay(savedCard);
  assert.deepEqual([mismatch.status, mismatch.pageTotal, mismatch.pageCurrency], ['amount_mismatch', '4910', 'INR']);
  assert.deepEqual(page.typed, [], 'nothing filled');
  page.cardScan = {};
  const noFields = await pay(savedCard, '4910.00');
  assert.equal(noFields.status, 'not_actionable');
  assert.match(noFields.reason, /No card number field/);
  page.cardScan = { number: { select: false, placeholder: '', maxLength: 0, options: [] }, exp: { select: false, placeholder: 'mm / yy', maxLength: 7, options: [] },
    cvc: { select: false, placeholder: 'cvc', maxLength: 4, options: [] } };
  const filled = await pay(savedCard, '4910.00');
  assert.equal(filled.status, 'card_filled');
  assert.deepEqual(filled.filled, ['number', 'exp', 'cvc']);
  noSecrets(filled);
  assert.deepEqual(page.typed.map(entry => entry[1]), ['4242424242424242', '07/29', '123']);
  const screenshot = await h.run('live-browser-screenshot', { sessionId: 'b-1' });
  assert.equal(screenshot.status, 'not_actionable');
  assert.ok(!('modelMedia' in screenshot));
  const snapshot = await h.run('live-browser-snapshot', { sessionId: 'b-1', includeScreenshot: true });
  assert.equal(snapshot.screenshotWithheld, true);
  assert.ok(!('modelMedia' in snapshot));
  await assert.rejects(pay(savedCard, '0'), /greater than zero/);
  await h.service.closeAll();
});

const pause = ms => new Promise(resolve => setTimeout(resolve, ms));

test('an idle browser closes after the idle window; actions slide it', async () => {
  const h = harness({ lifetime: { idleMs: 200 } });
  await h.run('live-browser-start', {});
  await pause(120);
  await h.run('live-browser-scroll', { sessionId: 'b-1', dy: 10 });
  await pause(120);
  assert.equal(h.service.size, 1, 'the action re-armed the idle window');
  await pause(200);
  assert.equal(h.service.size, 0);
  assert.ok(h.calls.some(call => call[0] === 'revoke' && call[1] === 'b-1'));
});

test('a pending handoff keeps the browser open and extends the Cortex session', async () => {
  const h = harness({ lifetime: { idleMs: 100 } });
  await h.run('live-browser-start', { url: 'https://shop.example.com/' });
  await h.run('live-browser-request-handoff', { sessionId: 'b-1', reason: 'payment', summary: 'Pay.' });
  await pause(450);
  assert.equal(h.service.size, 1, 'still open while waiting for the user');
  const extends_ = h.calls.filter(call => call[0] === 'extend');
  assert.ok(extends_.length >= 2, 'extended on entering awaiting_user and while waiting');
  assert.deepEqual(extends_[0].slice(1), ['b-1', 'grant-1', 1]);
  const lease = await h.control('b-1', { type: 'claim' });
  await h.control('b-1', { type: 'resume', leaseID: lease.id });
  await pause(300);
  assert.equal(h.service.size, 0, 'idle again after hand-back');
});

test('waiting for a one-time code from chat keeps the browser open', async () => {
  const h = harness({ lifetime: { idleMs: 100, replyWaitMs: 400 } });
  await h.run('live-browser-start', { url: 'https://www.amazon.in/ap/signin' });
  h.pages[0].scans.push({ otp: 1, via: 'message', submit: true });
  assert.equal((await h.run('live-browser-sign-in', { sessionId: 'b-1' }, 'run-1', amazonLogin)).status, 'otp_required');
  await pause(250);
  assert.equal(h.service.size, 1);
  assert.ok(h.calls.some(call => call[0] === 'extend'));
  await pause(450);
  assert.equal(h.service.size, 0, 'the reply wait is bounded');
});

test('the Cortex session is extended before it expires and the 8 hour cap closes the browser', async () => {
  const h = harness({ lifetime: { idleMs: 10000, extendMarginMs: 100 }, expiresInMs: 250 });
  await h.run('live-browser-start', {});
  await pause(400);
  assert.equal(h.service.size, 1);
  assert.ok(h.calls.filter(call => call[0] === 'extend').length >= 2, 'refreshed ahead of each expiry');
  const capped = harness({ lifetime: { maxLifetimeMs: 200 } });
  await capped.run('live-browser-start', {});
  await capped.run('live-browser-request-handoff', { sessionId: 'b-1', reason: 'login', summary: 'Sign in.' });
  await pause(350);
  assert.equal(capped.service.size, 0, 'even a pending handoff ends at the cap');
  const failing = harness({ lifetime: { idleMs: 10000, extendMarginMs: 100 }, expiresInMs: 250, extendFails: true });
  await failing.run('live-browser-start', {});
  await pause(400);
  assert.equal(failing.service.size, 0, 'a session Cortex will not extend closes at its expiry');
});

test('a plain click refuses the final pay button on a checkout; live-browser-pay clicks it', async () => {
  const h = harness();
  await h.run('live-browser-start', { url: 'https://www.amazon.in/gp/buy/spc/handlers/display.html' });
  const page = h.pages[0];
  page.snapshot = { url: page.current, title: 'Checkout', text: 'Order Total: ₹4,910.00', elements: [
    { ref: 1, role: 'button', name: 'Place your order', context: 'form', href: '', inViewport: true, bounds: {}, state: {} },
    { ref: 2, role: 'link', name: 'Change delivery address', context: 'form', href: '', inViewport: true, bounds: {}, state: {} }] };
  const snapshot = await h.run('live-browser-snapshot', { sessionId: 'b-1' });
  const refused = await h.run('live-browser-click', { sessionId: 'b-1', target: snapshot.elements[0].ref, intent: 'Place the order' });
  assert.equal(refused.status, 'not_actionable');
  assert.match(refused.hint, /live-browser-pay/);
  page.pointName = 'Pay ₹4,910';
  const byPoint = await h.run('live-browser-click', { sessionId: 'b-1', generation: snapshot.generation, x: 10, y: 10, intent: 'Pay' });
  assert.equal(byPoint.status, 'not_actionable', 'coordinates cannot get around it');
  page.pointName = 'Change delivery address';
  assert.equal((await h.run('live-browser-click', { sessionId: 'b-1', generation: snapshot.generation, x: 10, y: 10, intent: 'Change address' })).status, 'automating');
  assert.equal(page.clicked, undefined, 'nothing pressed the pay button');

  page.bodyText = 'Order Summary\nOrder Total: ₹4,910.00';
  const pay = input => h.run('live-browser-pay', { sessionId: 'b-1', amount: '4910.00', currency: 'INR', merchant: 'https://www.amazon.in', intent: 'Place the order', ...input });
  assert.equal((await pay({ merchant: 'https://www.flipkart.com' })).status, 'merchant_mismatch');
  const mismatch = await pay({ amount: '4900.00' });
  assert.deepEqual([mismatch.status, mismatch.pageTotal], ['amount_mismatch', '4910']);
  page.payCount = 2;
  assert.match((await pay({})).reason, /Several different pay buttons/);
  assert.equal(page.clicked, undefined);
  page.payCount = 1;
  page.onPay = () => { page.current = 'https://www.amazon.in/gp/buy/thankyou'; page.bodyText = 'Thank you, your order has been placed.\nOrder number: 404-5551234-7654321\nArriving Thursday'; };
  const paid = await pay({});
  assert.equal(paid.status, 'confirmed');
  assert.equal(paid.orderReference, '404-5551234-7654321');
  assert.match(paid.summary, /order has been placed/);
  assert.deepEqual(page.clicked, ['[data-live-pay="1"]']);
  await h.service.closeAll();
});

test('pay stays on the site where the card was filled and reports a bank verification step', async () => {
  const h = harness();
  await h.run('live-browser-start', { url: 'https://shop.example/checkout' });
  const page = h.pages[0];
  page.cardScan = { number: { select: false, placeholder: '', maxLength: 0, options: [] } };
  assert.equal((await h.run('live-browser-fill-payment-card', { sessionId: 'b-1', amount: '25.00', currency: 'USD' }, 'run-1', savedCard)).status, 'card_filled');
  // After a card is filled, any pay button is refused for plain clicks, wherever the text says.
  page.snapshot = { url: page.current, title: 'Pay', text: '', elements: [
    { ref: 1, role: 'button', name: 'Pay now', context: '', href: '', inViewport: true, bounds: {}, state: {} }] };
  page.current = 'https://shop.example/review';
  const snapshot = await h.run('live-browser-snapshot', { sessionId: 'b-1' });
  assert.equal((await h.run('live-browser-click', { sessionId: 'b-1', target: snapshot.elements[0].ref, intent: 'Pay' })).status, 'not_actionable');
  page.current = 'https://other.example/review';
  assert.equal((await h.run('live-browser-pay', { sessionId: 'b-1', amount: '25.00', currency: 'USD', intent: 'Pay' })).status, 'merchant_mismatch');
  page.current = 'https://shop.example/review';
  const fresh = await h.run('live-browser-snapshot', { sessionId: 'b-1' });
  page.clickable = true;
  page.onPay = () => { page.current = 'https://acs.bank.example/challenge'; page.bodyText = 'Verified by Visa\nEnter the OTP sent to your phone'; page.otpScan = { code: true, submit: true }; };
  const result = await h.run('live-browser-pay', { sessionId: 'b-1', amount: '25.00', currency: 'USD', target: fresh.elements[0].ref, intent: 'Pay' });
  assert.equal(result.status, 'otp_required');
  assert.match(result.message, /Might have gotten an OTP, please provide/);
  assert.match(result.message, /live-browser-submit-payment-code/);
  assert.equal(result.requiresHuman, false);
  assert.equal((await h.control('b-1', { type: 'status' })).status, 'automating', 'no handoff');
  noSecrets(result);
  // A wrong code: still asked.
  const wrong = await h.run('live-browser-submit-payment-code', { sessionId: 'b-1', oneTimeCode: '000000' }, 'run-2');
  assert.deepEqual([wrong.status, wrong.retry], ['otp_required', true]);
  assert.deepEqual(page.typed.filter(entry => entry[0] === '[data-live-otp="code"]').map(entry => entry[1]), ['000000']);
  page.onCode = () => { page.current = 'https://shop.example/thanks'; page.otpScan = {}; page.bodyText = 'Thank you for your order\nOrder number: A1B2C3D4'; };
  const done = await h.run('live-browser-submit-payment-code', { sessionId: 'b-1', oneTimeCode: '123 456' }, 'run-2');
  assert.deepEqual([done.status, done.orderReference], ['confirmed', 'A1B2C3D4']);
  const late = await h.run('live-browser-submit-payment-code', { sessionId: 'b-1', oneTimeCode: '123456' }, 'run-2');
  assert.match(late.reason, /No payment is waiting/, 'codes only for a pending payment');
  page.current = 'https://shop.example/review';
  page.snapshot.elements[0].name = 'Continue shopping';
  const other = await h.run('live-browser-snapshot', { sessionId: 'b-1' });
  assert.match((await h.run('live-browser-pay', { sessionId: 'b-1', amount: '25.00', currency: 'USD', target: other.elements[0].ref, intent: 'Pay' })).reason,
    /not a final pay/);
  await h.service.closeAll();
});

test('a bank app approval is asked for in chat, and the pay button in a processor frame is preferred', async () => {
  const h = harness();
  await h.run('live-browser-start', { url: 'https://shop.example/checkout' });
  const page = h.pages[0];
  page.bodyText = 'Order Total: $25.00';
  page.payCount = 1;
  page.onPay = () => { page.bodyText = 'Approve this payment in your HDFC Bank mobile app'; page.otpScan = { app: true }; };
  const result = await h.run('live-browser-pay', { sessionId: 'b-1', amount: '25.00', currency: 'USD', intent: 'Pay' });
  assert.equal(result.status, 'approve_in_app');
  assert.match(result.message, /bank app/);
  assert.equal(result.requiresHuman, false);
  await h.service.closeAll();
});
