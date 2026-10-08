import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { test } from 'node:test';
import { browserAuthorizer, profileAuthorizer } from './browser-authorizer.mjs';
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

test('session-less profile commands go to the profile handler with their exact proof', async () => {
  const seen = [];
  const service = {
    controlBrowser: async () => { throw new Error('not a session command'); },
    controlProfile: async input => { seen.push(input); return { state: 'shared', origins: ['whatsapp.com'], updatedAt: null, sizeBytes: 1 }; },
  };
  const reply = await invoke(browserHandlers(service).Control, { value: Buffer.from(JSON.stringify({
    agentID: 'agent-1', sessionID: '', authorization: 'human-proof', commandJSON: '{"type":"profileStatus"}',
  })) });
  assert.deepEqual(JSON.parse(reply.value).origins, ['whatsapp.com']);
  assert.deepEqual(seen, [{ agentID: 'agent-1', authorization: { token: 'human-proof', commandJSON: '{"type":"profileStatus"}' }, command: { type: 'profileStatus' } }]);
});

test('profile authorization is verified by Cortex without a session grant and bound to the runtime tenant', async () => {
  const commandJSON = '{"type":"forgetProfile"}';
  const expiresAt = new Date(Date.now() + 15000).toISOString();
  let reply = { userID: '23', tenantID: '7', agentID: 'agent-1', requestID: 'request-1', expiresAt };
  const authorize = profileAuthorizer({ baseURL: 'https://host.example/agent/browser/v1/', fetchAPI: async (url, options) => {
    assert.equal(url.href, 'https://host.example/agent/browser/v1/profile/authorize');
    assert.equal(options.headers.Authorization, undefined);
    assert.equal(options.headers['X-Tenant-ID'], '7');
    assert.deepEqual(JSON.parse(options.body), { authorization: 'human-proof', commandDigest: createHash('sha256').update(commandJSON).digest('hex') });
    return { ok: true, text: async () => JSON.stringify({ result: reply }) };
  } });
  const input = { tenantID: '7', authorization: { token: 'human-proof', commandJSON }, command: { type: 'forgetProfile' } };
  assert.equal((await authorize(input)).userID, '23');
  await assert.rejects(authorize({ ...input, command: { type: 'profileStatus' } }), /authorization failed/, 'proof for another command');
  await assert.rejects(authorize({ ...input, command: { type: 'claim' }, authorization: { token: 't', commandJSON: '{"type":"claim"}' } }), /authorization failed/);
  await assert.rejects(authorize({ ...input, tenantID: undefined }), /authorization failed/, 'no tenant configured');
  reply = { ...reply, tenantID: '8' };
  await assert.rejects(authorize(input), /authorization failed/, 'a proof for another tenant');
});
