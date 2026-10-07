import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import { test } from 'node:test';
import { LiveBrowserService } from './service.mjs';

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
  page.evaluate = async script => (typeof script === 'string' && script.startsWith('((limits)') ? structuredClone(page.snapshot) : 'none');
  page.box = null;
  page.locator = () => ({ first() { return this; }, scrollIntoViewIfNeeded: async () => {}, boundingBox: async () => page.box,
    evaluate: async () => { if (page.fault) throw page.fault; return false; },
    click: async () => { throw page.fault ?? Object.assign(new Error('Timeout 5000ms exceeded.\n - element is not visible'), { name: 'TimeoutError' }); } });
  page.mouse = { click: async () => {}, wheel: async () => {} };
  page.keyboard = { press: async () => {}, type: async () => {} };
  return page;
}

function harness({ agentId = 'agent-1', conversationId = 'conv-1', detect, profileForbidden = false, leaseMs, noticeFails = false, launchGate } = {}) {
  const calls = [];
  let sessions = 0, requests = 0;
  const profile = { cookies: [], storage: [] };
  const api = {
    async register({ invocation, durationMinutes }) {
      calls.push(['register', invocation, durationMinutes]);
      sessions++;
      return { sessionId: `b-${sessions}`, grant: `grant-${sessions}`, expiresAt: later(), tenantId: '7', agentId, conversationId };
    },
    async revoke({ sessionId, grant }) { calls.push(['revoke', sessionId, grant]); },
    async handoffNotice(session, notice) {
      calls.push(['notice', session.grant, notice.handoffId, notice.summary]);
      if (noticeFails) throw Object.assign(new Error('secret summary'), { status: 503 });
      return { posted: true };
    },
    async audioCatalogGrant() { calls.push(['catalog']); return { token: 'c', expiresAt: later() }; },
    async audioGrants() { calls.push(['audio']); return { transcriptionToken: 't', speechToken: 's', expiresAt: later() }; },
    async profileGrant(session) {
      calls.push(['profileGrant', session.grant]);
      if (profileForbidden) throw Object.assign(new Error('forbidden'), { status: 403 });
      return { profile: 'default', state: profile.cookies.length ? 'shared' : 'new', origins: [], grant: 'pg', expiresAt: later() };
    },
    async loadProfile() { calls.push(['loadProfile']); return structuredClone(profile); },
    async saveProfileChanges(_session, grant, changes) {
      calls.push(['saveProfile', grant, structuredClone(changes)]);
      profile.cookies.push(...changes.cookies);
      return { origins: ['example.com'] };
    },
    sessionURL: id => `http://cortex/browser/v1/sessions/${id}/`,
  };
  const pages = [];
  const contexts = [];
  const deps = {
    createDesktop: async () => ({ display: ':42', close: () => calls.push(['desktop-close']) }),
    createAudioRoute: async id => ({ sink: `lb_${id}_capture`, source: `lb_${id}_source`, commands: {}, close: async () => calls.push(['route-close']) }),
    makeTemp: async () => '/tmp/live-browser-test', removeTemp: async dir => calls.push(['rm', dir]),
    launch: async (dir, options) => {
      calls.push(['launch', dir, options.display, options.audio.sink]);
      await launchGate;
      const page = fakePage(); pages.push(page);
      const state = { cookies: [], origins: [] };
      const context = { state, pages: () => [page], newPage: async () => page, addCookies: async c => state.cookies.push(...c),
        addInitScript: async () => {}, storageState: async () => structuredClone(state), close: async () => calls.push(['context-close']) };
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
  const service = new LiveBrowserService({ api, authorize, deps, humanWaitMs: 200, profileSaveIntervalMs: 3600000, ...(leaseMs ? { leaseMs } : {}) });
  const control = (sessionId, command) => service.controlBrowser({ agentID: agentId, sessionID: sessionId,
    authorization: { token: 'proof', commandJSON: JSON.stringify(command) }, command });
  const bindings = { CORTEX_HOST_INVOCATIONS: JSON.stringify({ 'host:browser': 'invocation' }) };
  const run = (action, input, runID = 'run-1') => service.execute(action, { runID, agentID: agentId, input, bindings });
  return { service, calls, pages, contexts, control, run, profile };
}

test('start registers the session, launches on a private display and reports rich status', async () => {
  const h = harness();
  const started = await h.run('live-browser-start', { url: 'https://flights.example.com/', intent: 'Open the flight search' });
  assert.equal(started.sessionId, 'b-1');
  assert.equal(started.status, 'automating');
  assert.equal(started.url, 'https://flights.example.com/');
  assert.deepEqual(h.calls[0], ['register', 'invocation', 120]);
  assert.deepEqual(h.calls.find(call => call[0] === 'launch'), ['launch', '/tmp/live-browser-test', ':42', 'lb_b1_capture']);
  assert.deepEqual(h.calls.filter(call => ['profileGrant', 'loadProfile'].includes(call[0])), [['profileGrant', 'grant-1'], ['loadProfile']], 'the shared profile is loaded on start');
  const status = await h.control('b-1', { type: 'status' });
  assert.deepEqual({ ...status, expiresAt: undefined }, { sessionId: 'b-1', status: 'automating', url: 'https://flights.example.com/',
    title: 'Flights', step: 'Open the flight search', profile: { state: 'new', origins: [] }, audio: { listening: false }, expiresAt: undefined });
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

test('a new start in the same conversation replaces the previous browser', async () => {
  const h = harness();
  await h.run('live-browser-start', {});
  const second = await h.run('live-browser-start', {}, 'run-2');
  assert.equal(second.sessionId, 'b-2');
  assert.ok(h.calls.some(call => call[0] === 'revoke' && call[1] === 'b-1'));
  assert.equal(h.service.size, 1);
  await assert.rejects(h.run('live-browser-snapshot', { sessionId: 'b-1' }), /No live browser session/);
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

test('request-handoff waits for the user; hand-back resumes and saves shared sign-ins', async () => {
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
  h.contexts[0].state.cookies.push({ name: 'SID', value: 'signed-in', domain: '.example.com', path: '/', expires: -1, httpOnly: true, secure: true, sameSite: 'Lax' });
  await h.control('b-1', { type: 'resume', leaseID: lease.id });
  for (let i = 0; i < 20 && !h.calls.some(call => call[0] === 'saveProfile'); i++) await new Promise(resolve => setTimeout(resolve, 10));
  assert.deepEqual(h.calls.find(call => call[0] === 'saveProfile').slice(0, 2), ['saveProfile', 'pg'], 'hand-back saves the profile');
  assert.equal(h.profile.cookies[0].name, 'SID');
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
  assert.ok(order.indexOf('context-close') > order.lastIndexOf('loadProfile'));
  assert.ok(order.includes('revoke') && order.includes('route-close') && order.includes('desktop-close') && order.includes('rm'));
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

test('handoff posts a take-over notice; a session without profile permission runs privately', async () => {
  const h = harness({ profileForbidden: true });
  await h.run('live-browser-start', {});
  await h.run('live-browser-request-handoff', { sessionId: 'b-1', reason: 'submit', summary: 'Confirm the booking.' });
  await new Promise(resolve => setImmediate(resolve));
  assert.deepEqual(h.calls.filter(call => call[0] === 'notice'), [['notice', 'grant-1', 'b-1:h1', 'Confirm the booking.']]);
  assert.deepEqual((await h.control('b-1', { type: 'status' })).profile, { state: 'new', origins: [] });
  await h.service.closeAll();
  assert.equal(h.calls.filter(call => call[0] === 'saveProfile').length, 0);
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

test('the shared profile saves only sites the browser navigated to', async () => {
  const h = harness();
  await h.run('live-browser-start', { url: 'https://www.reddit.com/r/aww' });
  const cookie = (name, domain) => ({ name, value: 'v', domain, path: '/', expires: -1, httpOnly: false, secure: true, sameSite: 'None' });
  h.contexts[0].state.cookies.push(cookie('reddit_session', '.reddit.com'), cookie('tvid', '.1rx.io'), cookie('uid', '.33across.com'));
  h.contexts[0].state.origins.push({ origin: 'https://ads.360yield.com', localStorage: [{ name: 'id', value: 'ad' }] });
  await h.service.closeAll();
  const [, , changes] = h.calls.find(call => call[0] === 'saveProfile');
  assert.deepEqual(changes.cookies.map(c => c.domain), ['.reddit.com']);
  assert.deepEqual(changes.storage, []);
  assert.deepEqual(changes.sites, ['reddit.com']);
});
