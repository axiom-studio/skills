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
  page.goto = async url => { page.current = url; return { status: () => 200 }; };
  page.waitForLoadState = async () => {};
  page.evaluate = async () => {};
  page.mouse = { click: async () => {}, wheel: async () => {} };
  return page;
}

function harness({ agentId = 'agent-1', conversationId = 'conv-1', detect } = {}) {
  const calls = [];
  let sessions = 0, requests = 0;
  const profile = { revision: 0, entries: [] };
  const api = {
    async register({ invocation, durationMinutes }) {
      calls.push(['register', invocation, durationMinutes]);
      sessions++;
      return { sessionId: `b-${sessions}`, grant: `grant-${sessions}`, expiresAt: later(), tenantId: '7', agentId, conversationId };
    },
    async revoke({ sessionId, grant }) { calls.push(['revoke', sessionId, grant]); },
    async request(path, options) {
      calls.push([path.split('/').at(-1), path.split('/')[1]]);
      assert.match(options.grant, /^grant-/);
      if (path.endsWith('/load')) return structuredClone(profile);
      if (path.endsWith('/save')) { profile.entries = options.body.entries; profile.revision++; return { revision: profile.revision }; }
      throw new Error('unexpected');
    },
    async audioGrant() { calls.push(['audioGrant']); return { grant: 'audio', grantExpiresAt: later(), expiresAt: later(), conversationId: 'other-conv' }; },
    audioURL: id => `http://cortex/browser/v1/sessions/${id}/audio/`,
  };
  const pages = [];
  const contexts = [];
  const deps = {
    createDesktop: async () => ({ display: ':42', close: () => calls.push(['desktop-close']) }),
    createAudioRoute: async id => ({ sink: `lb_${id}_capture`, source: `lb_${id}_source`, commands: {}, close: async () => calls.push(['route-close']) }),
    makeTemp: async () => '/tmp/live-browser-test', removeTemp: async dir => calls.push(['rm', dir]),
    launch: async (dir, options) => {
      calls.push(['launch', dir, options.display, options.audio.sink]);
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
  };
  const authorize = async ({ command, sessionGrant }) => {
    assert.match(sessionGrant, /^grant-/);
    return { userID: 'human-1', tenantID: '7', agentID: agentId, requestID: `r-${++requests}`, expiresAt: new Date(Date.now() + 15000).toISOString(), command };
  };
  const service = new LiveBrowserService({ api, authorize, deps, humanWaitMs: 200, profileSaveIntervalMs: 3600000 });
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
  assert.ok(h.calls.some(call => call[0] === 'load'), 'the shared profile is loaded on start');
  const status = await h.control('b-1', { type: 'status' });
  assert.deepEqual({ ...status, expiresAt: undefined }, { sessionId: 'b-1', status: 'automating', url: 'https://flights.example.com/',
    title: 'Flights', step: 'Open the flight search', profile: { state: 'new', origins: [] }, audio: { listening: false }, expiresAt: undefined });
  // The same run reuses its browser.
  const again = await h.run('live-browser-start', { url: 'https://flights.example.com/results' });
  assert.equal(again.sessionId, 'b-1');
  assert.equal(h.calls.filter(call => call[0] === 'register').length, 1);
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
  await h.run('live-browser-start', {});
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
  for (let i = 0; i < 20 && !h.calls.some(call => call[0] === 'save'); i++) await new Promise(resolve => setTimeout(resolve, 10));
  assert.ok(h.calls.some(call => call[0] === 'save'), 'hand-back saves the profile');
  assert.equal(h.profile.entries[0].name, 'SID');
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
  assert.ok(order.indexOf('context-close') > order.lastIndexOf('load'));
  assert.ok(order.includes('revoke') && order.includes('route-close') && order.includes('desktop-close') && order.includes('rm'));
  await assert.rejects(h.control('b-1', { type: 'status' }), /could not be completed/);
});

test('audio requires the host:browser:audio grant for this exact conversation', async () => {
  const h = harness();
  await h.run('live-browser-start', {});
  await assert.rejects(h.service.execute('live-browser-listen', { runID: 'run-1', agentID: 'agent-1',
    input: { sessionId: 'b-1', state: 'on' }, bindings: {} }), /host:browser:audio/);
  await assert.rejects(h.run('live-browser-listen', { sessionId: 'b-1', state: 'on' }), /does not match this conversation/);
  assert.deepEqual(await h.run('live-browser-listen', { sessionId: 'b-1', state: 'off' }), { sessionId: 'b-1', listening: false });
  await h.service.closeAll();
});
