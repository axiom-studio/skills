import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { test } from 'node:test';
import { browserAuthorizer } from './browser-authorizer.mjs';
import { browserHandlers } from './browser-grpc.mjs';

const invoke = (handler, request) => new Promise((resolve, reject) => handler({ request },
  (error, result) => error ? reject(error) : resolve(result)));

test('generic browser authorization binds exact input bytes to an independent runtime proof', async () => {
  const commandJSON = '{"type":"input","leaseID":"lease","input":{"type":"text","text":"private-value"}}';
  const authorize = browserAuthorizer({ baseURL: 'https://host.example/agent/browser/v1/', fetchAPI: async (url, options) => {
    assert.equal(url.href, 'https://host.example/agent/browser/v1/sessions/session-1/authorize');
    assert.equal(options.headers.Authorization, 'Bearer runtime-proof');
    const body = JSON.parse(options.body);
    assert.equal(body.authorization, 'human-proof');
    assert.equal(body.commandDigest, createHash('sha256').update(commandJSON).digest('hex'));
    assert.equal(options.body.includes('private-value'), false);
    assert.equal(options.redirect, 'error');
    return { ok: true, text: async () => JSON.stringify({ result: { userID: '23', tenantID: '7', agentID: 'agent-1',
      requestID: 'request-1', expiresAt: new Date(Date.now() + 15000).toISOString() } }) };
  } });
  const input = { tenantID: '7', agentID: 'agent-1', sessionID: 'session-1', sessionGrant: 'runtime-proof',
    authorization: { token: 'human-proof', commandJSON }, command: JSON.parse(commandJSON) };
  assert.equal((await authorize(input)).userID, '23');
  await assert.rejects(authorize({ ...input, command: { type: 'claim' } }), /authorization failed/);
});

test('browser authorization fails closed on host denial, malformed replies, foreign scope and expired proof', async () => {
  for (const reply of [null, {}, { userID: '23', tenantID: 'other', agentID: 'agent-1' },
    { userID: '23', tenantID: '7', agentID: 'agent-1', requestID: 'old', expiresAt: new Date(0).toISOString() }]) {
    const authorize = browserAuthorizer({ baseURL: 'https://host.example/browser/v1/', fetchAPI: async () => ({
      ok: reply !== null, text: async () => JSON.stringify({ result: reply }),
    }) });
    await assert.rejects(authorize({ tenantID: '7', agentID: 'agent-1', sessionID: 'session-1', sessionGrant: 'runtime-proof',
      authorization: { token: 'human-proof', commandJSON: '{"type":"claim"}' }, command: { type: 'claim' } }));
  }
});

test('browser RPC exposes only host transports and never returns raw input errors', async () => {
  let calls = 0;
  const service = { controlBrowser: async input => {
    calls++;
    assert.equal(input.authorization.token, 'human-proof');
    assert.deepEqual(input.command, { type: 'claim' });
    return { id: 'lease' };
  } };
  const reply = await invoke(browserHandlers(service).Control, { value: Buffer.from(JSON.stringify({
    agentID: 'agent-1', sessionID: 'session-1', authorization: 'human-proof', commandJSON: '{"type":"claim"}',
  })) });
  assert.equal(JSON.parse(reply.value).id, 'lease');
  assert.deepEqual(Object.keys(browserHandlers(service)).sort(), ['Control', 'Desktop', 'Video']);
  assert.equal(calls, 1);
  await assert.rejects(invoke(browserHandlers(service).Control, { value: Buffer.from('private-invalid-json') }), error => {
    assert.doesNotMatch(error.message, /private-invalid-json/);
    return true;
  });
});
