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
  assert.equal(api.audioURL('b-1'), 'http://sentinel.example/orchestrator/agent/browser/v1/sessions/b-1/audio/');
  await assert.rejects(api.register({ invocation: 'host-grant', durationMinutes: 0 }), /duration/);
});

test('audio grants mirror the session grant issuer under the browser session', async () => {
  const requests = [];
  const api = new BrowserSessionAPI({ baseURL: 'http://sentinel.example/browser/v1/', fetchAPI: async (url, options) => {
    requests.push({ path: new URL(url).pathname, options });
    return { ok: true, json: async () => ({ result: { grant: 'audio-grant-2', grantExpiresAt: later(), expiresAt: later(), conversationId: 'conv-1' } }) };
  } });
  const issued = await api.audioGrant({ sessionId: 'b-1', invocation: 'audio-invocation', durationMinutes: 30 });
  assert.equal(issued.grant, 'audio-grant-2');
  assert.equal(requests[0].path, '/browser/v1/sessions/b-1/audio/grants');
  assert.equal(requests[0].options.headers['X-Cortex-Host-Invocation'], 'audio-invocation');
  await api.renewAudioGrant({ sessionId: 'b-1', grant: 'audio-grant', tenantId: '7' });
  assert.equal(requests[1].path, '/browser/v1/sessions/b-1/audio/grants/renew');
  assert.equal(requests[1].options.headers.Authorization, 'Bearer audio-grant');
  await api.revokeAudio({ sessionId: 'b-1', grant: 'audio-grant-2', tenantId: '7' });
  assert.equal(requests[2].path, '/browser/v1/sessions/b-1/audio/revoke');
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
