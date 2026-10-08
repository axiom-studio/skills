import assert from 'node:assert/strict';
import { test } from 'node:test';
import { BrowserSessionAPI, hostInvocation } from './browser-session.mjs';

const later = () => new Date(Date.now() + 600000).toISOString();

test('registration sends only the host invocation and validates the issued session', async () => {
  const requests = [];
  const api = new BrowserSessionAPI({ baseURL: 'http://sentinel.example/orchestrator/agent/browser/v1', fetchAPI: async (url, options) => {
    requests.push({ url: url.href, options });
    return { ok: true, json: async () => ({ result: { sessionId: 'b-1', grant: 'runtime-grant', expiresAt: later(),
      tenantId: '7', agentId: 'agent-1', conversationId: 'conv-1' } }) };
  } });
  const session = await api.register({ invocation: 'host-grant', durationMinutes: 60 });
  assert.equal(session.sessionId, 'b-1');
  assert.equal(session.conversationId, 'conv-1');
  assert.equal(requests[0].url, 'http://sentinel.example/orchestrator/agent/browser/v1/sessions');
  assert.deepEqual(Object.keys(requests[0].options.headers).sort(), ['Content-Type', 'X-Cortex-Host-Invocation']);
  assert.deepEqual(JSON.parse(requests[0].options.body), { durationMinutes: 60 });
  assert.equal(requests[0].options.redirect, 'error');
  await api.revoke({ sessionId: 'b-1', grant: 'runtime-grant', tenantId: '7' });
  assert.equal(requests[1].url, 'http://sentinel.example/orchestrator/agent/browser/v1/sessions/b-1/revoke');
  assert.equal(requests[1].options.headers.Authorization, 'Bearer runtime-grant');
  assert.equal(requests[1].options.headers['X-Tenant-ID'], '7');
  assert.equal(requests[1].options.headers['X-Cortex-Host-Invocation'], undefined);
  await assert.rejects(api.register({ invocation: 'host-grant', durationMinutes: 0 }), /duration/);
});

test('session routes use the runtime grant', async () => {
  const requests = [];
  const later = () => new Date(Date.now() + 600000).toISOString();
  const replies = {
    'handoff-notice': { posted: true },
    'catalog-grant': { token: 'catalog', expiresAt: later() },
    grants: { transcriptionToken: 'stt', speechToken: 'tts', expiresAt: later() },
  };
  const api = new BrowserSessionAPI({ baseURL: 'http://sentinel.example/browser/v1/', fetchAPI: async (url, options) => {
    requests.push({ path: new URL(url).pathname, options });
    return { ok: true, json: async () => ({ result: replies[new URL(url).pathname.split('/').at(-1)] }) };
  } });
  const session = { sessionId: 'b-1', grant: 'runtime-grant', tenantId: '7' };
  await api.handoffNotice(session, { handoffId: 'h-1', summary: 'Pay' });
  assert.deepEqual(await api.audioCatalogGrant(session), { token: 'catalog', expiresAt: replies['catalog-grant'].expiresAt });
  assert.equal((await api.audioGrants(session, { transcriptionModel: 'a', speechModel: 'b' })).speechToken, 'tts');
  assert.deepEqual(requests.map(r => `${r.options.method} ${r.path}`), [
    'POST /browser/v1/sessions/b-1/handoff-notice', 'POST /browser/v1/sessions/b-1/audio/catalog-grant',
    'POST /browser/v1/sessions/b-1/audio/grants']);
  assert.ok(requests.every(r => r.options.headers.Authorization === 'Bearer runtime-grant' && !r.options.headers['X-Cortex-Host-Invocation']));
  assert.deepEqual(JSON.parse(requests[2].options.body), { transcriptionModel: 'a', speechModel: 'b' });
  assert.equal(api.sessionURL('b-1'), 'http://sentinel.example/browser/v1/sessions/b-1/');
});

test('HTTP statuses such as 410 and 409 are surfaced without bodies', async () => {
  const api = new BrowserSessionAPI({ baseURL: 'http://h/b/v1/', fetchAPI: async () => ({ ok: false, status: 410, json: async () => ({ error: 'secret' }) }) });
  await assert.rejects(api.handoffNotice({ sessionId: 'b', grant: 'g' }, { handoffId: 'h', summary: 's' }), error => error.status === 410 && !/secret/.test(error.message));
});

test('a run without a conversation gets the typed refusal the agent can explain', async () => {
  const reply = body => async () => ({ ok: false, status: 409, json: async () => body });
  await assert.rejects(new BrowserSessionAPI({ baseURL: 'http://h/b/v1/',
    fetchAPI: reply({ error: 'ignored server text', errorCode: 'browser_no_conversation' }) }).register({ invocation: 'host-grant', durationMinutes: 5 }),
  error => error.code === 'browser_no_conversation' && error.status === 409 &&
    error.message.startsWith('This task has no conversation to show the live browser in.') && !/ignored/.test(error.message));
  for (const body of [{ errorCode: 'other', error: 'secret' }, { errorCode: 'toString' }, null]) {
    await assert.rejects(new BrowserSessionAPI({ baseURL: 'http://h/b/v1/', fetchAPI: reply(body) }).register({ invocation: 'host-grant', durationMinutes: 5 }),
      error => error.message === 'Cortex browser API refused the request (HTTP 409)');
  }
});

test('Cortex failures and malformed replies never expose tokens', async () => {
  for (const fetchAPI of [
    async () => { throw new Error('connect to host-grant failed'); },
    async () => ({ ok: false, status: 403, json: async () => ({ error: 'host-grant denied' }) }),
    async () => ({ ok: true, json: async () => ({ result: { sessionId: '../x', grant: 'g', expiresAt: later() } }) }),
    async () => ({ ok: true, json: async () => ({ result: { sessionId: 'b', grant: 'g', expiresAt: new Date(0).toISOString(), tenantId: '1', agentId: 'a', conversationId: 'c' } }) }),
  ]) {
    await assert.rejects(new BrowserSessionAPI({ baseURL: 'http://h/b/v1/', fetchAPI }).register({ invocation: 'host-grant', durationMinutes: 5 }),
      error => !error.message.includes('host-grant'));
  }
  assert.throws(() => new BrowserSessionAPI({ baseURL: 'ftp://h/' }), /invalid/);
});

test('host invocation bindings select one audience and hide parser errors', () => {
  const binding = JSON.stringify({ 'host:browser': 'browser-token', 'host:other': 'x' });
  assert.equal(hostInvocation(binding), 'browser-token');
  assert.equal(hostInvocation({ 'host:other': 'x' }), undefined);
  assert.equal(hostInvocation(undefined), undefined);
  assert.throws(() => hostInvocation('{"host:browser": secret'), error => error.message === 'Invalid host invocation binding');
  assert.throws(() => hostInvocation({ 'host:browser': 7 }), /Invalid host invocation/);
});
