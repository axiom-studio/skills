import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { EventEmitter } from 'node:events';
import { mkdtempSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { test } from 'node:test';
import { MeetSessionService } from './session.mjs';

function service(overrides = {}) {
  const children = [];
  const requests = [];
  const profilesDir = overrides.profilesDir ?? mkdtempSync(join(tmpdir(), 'meet-voice-test-'));
  const sessionID = overrides.sessionID ?? randomUUID();
  const fetchAPI = async (url, options) => {
    const path = new URL(url).pathname;
    const body = options.body ? JSON.parse(options.body) : undefined;
    requests.push({ path, token: options.headers.token, invocation: options.headers['X-Cortex-Meet-Invocation'], body });
    if (overrides.deny?.(path, body)) return { ok: false, status: 403 };
    const now = Date.now();
    if (path === '/models') return { ok: true, json: async () => ({ data: [], links: {} }) };
    const result = path.endsWith('/grants')
      && !path.endsWith('/speech/grants')
      ? { sessionId: sessionID, conversationId: 'chat-1', grant: 'signed-meeting-grant',
        controlGrant: overrides.controlGrant,
        grantExpiresAt: new Date(now + 300000).toISOString(),
        sessionExpiresAt: new Date(now + (body.durationMinutes ?? 240) * 60000).toISOString() }
      : path.endsWith('/speech/grants')
        ? { transcriptionToken: 'scoped-transcription', speechToken: 'scoped-speech',
          expiresAt: new Date(now + 300000).toISOString() }
      : path.endsWith('/catalog-grant')
        ? { token: 'catalog-token', expiresAt: new Date(now + 300000).toISOString() }
      : path.endsWith('/grants/renew')
        ? { grant: 'renewed-meeting-grant', grantExpiresAt: new Date(now + 300000).toISOString() }
        : { conversationId: 'chat-1' };
    return { ok: true, json: async () => ({ result: overrides.result?.(path, result, body) ?? result }) };
  };
  const forkProcess = (_file, _args, options) => {
    const child = new EventEmitter();
    child.kill = signal => { child.signal = signal; return true; };
    child.send = message => { child.lastMessage = message; };
    children.push({ child, options });
    return child;
  };
  return { worker: new MeetSessionService({ baseURL: 'https://cortex.example/orchestrator/agent/meet/v1/',
    tenantID: '7', profilesDir, fetchAPI, forkProcess, terminationGraceMs: overrides.terminationGraceMs ?? 10000,
    speechConfig: { AXIOM_SPEECH_API_URL: 'https://speech.example/',
      AXIOM_TRANSCRIPTION_MODEL: 'transcribe', AXIOM_SPEECH_MODEL: 'speak', AXIOM_SPEECH_VOICE: 'voice' } }), children, requests, profilesDir };
}

test('start derives the Seal Chat from the authenticated Run and persists past that turn', async t => {
  const { worker, children, requests, profilesDir } = service();
  t.after(() => rmSync(profilesDir, { recursive: true, force: true }));
  const input = { runID: 'run-1', agentID: 'agent-1', url: 'https://meet.google.com/abc-defg-hij',
    issuerToken: 'bound-bot-token', speechToken: 'bound-speech-token' };
  const started = await worker.start(input);
  assert.equal(started.status, 'joining');
  assert.ok(Math.abs(Date.parse(started.expiresAt) - Date.parse(started.startedAt) - 240 * 60000) < 1000);
  assert.deepEqual(requests[0], { path: '/orchestrator/agent/meet/v1/actions/context', token: 'bound-bot-token', invocation: undefined,
    body: { runId: 'run-1', agentId: 'agent-1', action: 'meet-status' } });
  assert.deepEqual(requests[1], { path: '/orchestrator/agent/meet/v1/grants', token: 'bound-bot-token', invocation: undefined,
    body: { runId: 'run-1', agentId: 'agent-1', durationMinutes: 240 } });
  assert.equal(children[0].options.env.CORTEX_CONVERSATION_ID, 'chat-1');
  assert.equal(children[0].options.env.CORTEX_MEET_GRANT, 'signed-meeting-grant');
  assert.equal(children[0].options.env.CORTEX_MEET_ISSUER_TOKEN, undefined);
  assert.equal(children[0].options.env.CORTEX_MEET_SESSION_API_URL,
    `https://cortex.example/orchestrator/agent/meet/v1/sessions/${started.sessionId}/`);
  assert.equal(children[0].options.env.AXIOM_SPEECH_TOKEN, 'bound-speech-token');
  assert.equal(children[0].options.env.MEET_SESSION_EXPIRES_AT, started.expiresAt);
  assert.equal(children[0].options.env.GOOGLE_PROFILE_DIR, join(profilesDir, 'agent-1'));
  const snapshot = readFileSync(join(profilesDir, '.meet-voice-state.json'), 'utf8');
  assert.equal(snapshot.includes('bound-bot-token') || snapshot.includes('bound-speech-token'), false);
  assert.equal((await worker.start(input)).sessionId, started.sessionId);
  assert.equal(children.length, 1);
  children[0].child.emit('message', { status: 'awaiting_admission' });
  assert.equal((await worker.status(input)).status, 'awaiting_admission');
  children[0].child.emit('message', { status: 'active' });
  assert.equal((await worker.status(input)).status, 'active');
  assert.equal((await worker.stop(input)).status, 'leaving');
  assert.ok(requests.some(({ path, token, body }) => path.endsWith(`/sessions/${started.sessionId}/revoke`) &&
    token === 'bound-bot-token' && body.grant === 'signed-meeting-grant'));
  assert.equal(children[0].child.signal, 'SIGTERM');
  children[0].child.emit('exit', 0);
  assert.equal((await worker.status(input)).status, 'ended');
  assert.ok(requests.some(({ path, token, body }) => path.endsWith(`/sessions/${started.sessionId}/outcome`) &&
    token === 'bound-bot-token' && body.outcome === 'ended'));
});

test('a running Meet action uses its scoped invocation for chat and grant issuance', async t => {
  const { worker, requests, children, profilesDir } = service();
  t.after(() => rmSync(profilesDir, { recursive: true, force: true }));
  await worker.start({ runID: 'run-1', agentID: 'agent-1', url: 'https://meet.google.com/abc-defg-hij',
    issuerToken: 'management-token', invocationToken: 'signed-invocation', speechToken: 'speech' });
  assert.equal(requests[0].body.action, 'meet-start');
  assert.equal(requests[0].invocation, 'signed-invocation');
  assert.equal(requests[1].invocation, 'signed-invocation');
  assert.equal(requests[0].token, undefined);
  assert.equal(requests[1].token, undefined);
  assert.equal(children[0].options.env.CORTEX_MEET_INVOCATION, undefined);
  assert.equal(readFileSync(join(profilesDir, '.meet-voice-state.json'), 'utf8').includes('signed-invocation'), false);
});

test('session control grant renews and closes a long call without an issuer token', async t => {
  const { worker, requests, children, profilesDir } = service({ controlGrant: 'signed-control' });
  t.after(() => rmSync(profilesDir, { recursive: true, force: true }));
  const input = { runID: 'run-1', agentID: 'agent-1', url: 'https://meet.google.com/abc-defg-hij',
    invocationToken: 'signed-invocation', speechToken: 'speech' };
  await worker.start(input);
  const session = worker.sessions.get('agent-1');
  assert.equal(session.controlGrant, 'signed-control');
  assert.equal(session.issuerToken, undefined);
  assert.equal(children[0].options.env.CORTEX_MEET_INVOCATION, undefined);
  assert.equal(children[0].options.env.CORTEX_MEET_CONTROL_GRANT, undefined);
  assert.equal(readFileSync(join(profilesDir, '.meet-voice-state.json'), 'utf8').includes('signed-control'), false);
  await worker.renew(session);
  await worker.stop(input);
  children[0].child.emit('exit', 0);
  await session.outcomePromise;
  for (const request of requests.filter(({ path }) => /grants\/renew|sessions\/.*\/(revoke|outcome)/.test(path))) {
    assert.equal(request.token, undefined);
    assert.equal(request.body.controlGrant, 'signed-control');
  }
});

test('managed speech grants keep separate audio scopes refreshed in the parent', async t => {
  const { worker, requests, children, profilesDir } = service({ controlGrant: 'signed-control' });
  t.after(() => rmSync(profilesDir, { recursive: true, force: true }));
  const input = { runID: 'run-1', agentID: 'agent-1', url: 'https://meet.google.com/abc-defg-hij',
    invocationToken: 'signed-invocation' };
  await worker.start(input);
  const session = worker.sessions.get('agent-1');
  assert.equal(children[0].options.env.AXIOM_TRANSCRIPTION_TOKEN, 'scoped-transcription');
  assert.equal(children[0].options.env.AXIOM_SPEECH_TOKEN, 'scoped-speech');
  assert.equal(readFileSync(join(profilesDir, '.meet-voice-state.json'), 'utf8').includes('scoped-speech'), false);
  assert.ok(requests.some(({ path, body }) => path.endsWith('/speech/grants') &&
    body.controlGrant === 'signed-control' && body.transcriptionModel === 'transcribe' && body.speechModel === 'speak'));
  await worker.renewSpeech(session);
  assert.deepEqual(children[0].child.lastMessage, { type: 'speech-grants',
    transcriptionToken: 'scoped-transcription', speechToken: 'scoped-speech' });
  await worker.stop(input);
  children[0].child.emit('exit', 0);
});

test('start rejects a run whose Seal Chat belongs to another agent', async t => {
  const { worker, children, profilesDir } = service({ deny: path => path.endsWith('/grants') });
  t.after(() => rmSync(profilesDir, { recursive: true, force: true }));
  await assert.rejects(worker.start({ runID: 'run-1', agentID: 'agent-1', url: 'https://meet.google.com/abc-defg-hij', issuerToken: 'bot', speechToken: 'speech' }), /authorization failed/);
  assert.equal(children.length, 0);
});

test('worker launch failure revokes its issued meeting session', async t => {
  const { worker, requests, profilesDir } = service({ controlGrant: 'signed-control' });
  t.after(() => rmSync(profilesDir, { recursive: true, force: true }));
  worker.forkProcess = () => { throw new Error('browser worker could not launch'); };
  await assert.rejects(worker.start({ runID: 'run-1', agentID: 'agent-1',
    url: 'https://meet.google.com/abc-defg-hij', invocationToken: 'signed-invocation' }),
  /browser worker could not launch/);
  assert.equal(worker.sessions.size, 0);
  assert.ok(requests.some(({ path, body }) => path.endsWith('/revoke') &&
    body.controlGrant === 'signed-control' && body.grant === 'signed-meeting-grant'));
});

test('meeting speech cannot start or stop the call', async t => {
  const { worker, children, profilesDir } = service({ deny: (path, body) =>
    path.endsWith('/grants') || body.action === 'meet-stop' });
  t.after(() => rmSync(profilesDir, { recursive: true, force: true }));
  const input = { runID: 'run-1', agentID: 'agent-1', url: 'https://meet.google.com/abc-defg-hij',
    issuerToken: 'bot', speechToken: 'speech' };
  await assert.rejects(worker.start(input), /authorization failed/);
  await assert.rejects(worker.stop(input), /authorization failed/);
  assert.equal(children.length, 0);
});

test('meeting actions require bound credentials', async t => {
  const { worker, children, profilesDir } = service();
  t.after(() => rmSync(profilesDir, { recursive: true, force: true }));
  const input = { runID: 'run-1', agentID: 'agent-1', url: 'https://meet.google.com/abc-defg-hij' };
  await assert.rejects(worker.start(input), /Cortex meeting invocation is required/);
  await assert.rejects(worker.status(input), /Cortex meeting issuer token is required/);
  assert.equal(children.length, 0);
});

test('start rejects a second meeting for an occupied agent profile', async t => {
  const { worker, children, profilesDir } = service();
  t.after(() => rmSync(profilesDir, { recursive: true, force: true }));
  await worker.start({ runID: 'run-1', agentID: 'agent-1', url: 'https://meet.google.com/abc-defg-hij', issuerToken: 'bot', speechToken: 'speech' });
  await assert.rejects(worker.start({ runID: 'run-1', agentID: 'agent-1', url: 'https://meet.google.com/xyz-abcd-efg', issuerToken: 'bot', speechToken: 'speech' }), /active meeting/);
  assert.equal(children.length, 1);
});

test('grant renewal stays in the service process and a failed renewal stops capture', async t => {
  let refuseRenewal = false;
  const { worker, children, profilesDir } = service({ deny: path => refuseRenewal && path.endsWith('/grants/renew') });
  t.after(() => rmSync(profilesDir, { recursive: true, force: true }));
  const input = { runID: 'run-1', agentID: 'agent-1', url: 'https://meet.google.com/abc-defg-hij',
    issuerToken: 'issuer-only-secret', speechToken: 'speech' };
  await worker.start(input);
  const session = worker.sessions.get('agent-1');
  assert.equal(children[0].options.env.CORTEX_MEET_ISSUER_TOKEN, undefined);
  await worker.renew(session);
  assert.deepEqual(children[0].child.lastMessage, { type: 'grant', grant: 'renewed-meeting-grant' });
  refuseRenewal = true;
  await worker.renew(session);
  assert.equal(children[0].child.signal, 'SIGTERM');
  children[0].child.emit('exit', 0);
  assert.equal((await worker.status(input)).status, 'failed');
});

test('restart reports an interrupted call as failed and permits a fresh start', async t => {
  const original = service();
  t.after(() => rmSync(original.profilesDir, { recursive: true, force: true }));
  const input = { runID: 'run-1', agentID: 'agent-1', url: 'https://meet.google.com/abc-defg-hij',
    issuerToken: 'bound-bot-token', speechToken: 'bound-speech-token' };
  const first = await original.worker.start(input);
  original.children[0].child.emit('message', { status: 'active' });
  await original.worker.status(input);
  const restarted = service({ profilesDir: original.profilesDir });
  const state = await restarted.worker.status(input);
  assert.equal(state.sessionId, first.sessionId);
  assert.equal(state.status, 'failed');
  assert.ok(state.endedAt);
  const next = await restarted.worker.start(input);
  assert.notEqual(next.sessionId, first.sessionId);
  assert.equal(restarted.children.length, 1);
  const recovery = restarted.requests.findIndex(({ path }) => path.endsWith('/actions/recover'));
  assert.ok(recovery > 0);
  assert.deepEqual(restarted.requests[recovery].body, {
    runId: 'run-1', agentId: 'agent-1', action: 'meet-status', sessionId: first.sessionId,
    conversationId: 'chat-1', sessionExpiresAt: first.expiresAt,
  });
  assert.equal(restarted.requests.filter(({ path }) => path.endsWith('/actions/recover')).length, 1);
  assert.ok(restarted.requests.some(({ path }) => path === '/orchestrator/agent/meet/v1/grants'));
});

test('restart clears the old lease before a direct new start', async t => {
  const original = service({ controlGrant: 'signed-control' });
  t.after(() => rmSync(original.profilesDir, { recursive: true, force: true }));
  const input = { runID: 'run-1', agentID: 'agent-1', url: 'https://meet.google.com/abc-defg-hij',
    invocationToken: 'signed-invocation', speechToken: 'speech' };
  const first = await original.worker.start(input);
  const restarted = service({ profilesDir: original.profilesDir, controlGrant: 'signed-control' });
  await restarted.worker.start(input);
  const recovery = restarted.requests.findIndex(({ path }) => path.endsWith('/actions/recover'));
  assert.ok(recovery > 0);
  assert.equal(restarted.requests[recovery].body.action, 'meet-start');
  assert.equal(restarted.requests[recovery].body.sessionId, first.sessionId);
  assert.equal(restarted.requests[recovery].invocation, 'signed-invocation');
  assert.equal(restarted.requests[recovery + 1].path, '/orchestrator/agent/meet/v1/grants');
});

test('stop after restart revokes the interrupted session before reporting it stopped', async t => {
  const original = service({ controlGrant: 'signed-control' });
  t.after(() => rmSync(original.profilesDir, { recursive: true, force: true }));
  const input = { runID: 'run-1', agentID: 'agent-1', url: 'https://meet.google.com/abc-defg-hij',
    invocationToken: 'signed-invocation', speechToken: 'speech' };
  const first = await original.worker.start(input);
  const restarted = service({ profilesDir: original.profilesDir });
  assert.equal((await restarted.worker.stop(input)).status, 'failed');
  const recovery = restarted.requests.find(({ path }) => path.endsWith('/actions/recover'));
  assert.equal(recovery.body.action, 'meet-stop');
  assert.equal(recovery.body.sessionId, first.sessionId);
  assert.equal(recovery.invocation, 'signed-invocation');
  assert.equal(restarted.worker.sessions.get('agent-1').recoveryPending, false);
  assert.equal(restarted.requests.some(({ path }) => path.endsWith('/grants')), false);
});

test('restart cannot open a replacement call until the old session is revoked', async t => {
  const original = service();
  t.after(() => rmSync(original.profilesDir, { recursive: true, force: true }));
  const input = { runID: 'run-1', agentID: 'agent-1', url: 'https://meet.google.com/abc-defg-hij',
    issuerToken: 'bound-bot-token', speechToken: 'bound-speech-token' };
  await original.worker.start(input);
  const restarted = service({ profilesDir: original.profilesDir,
    deny: path => path.endsWith('/actions/recover') });
  await assert.rejects(restarted.worker.start(input), /authorization failed/);
  assert.equal(restarted.children.length, 0);
  assert.equal(restarted.worker.sessions.get('agent-1').recoveryPending, true);
  assert.equal(restarted.requests.some(({ path }) => path.endsWith('/grants')), false);
});

test('start accepts speech selections from chat without deployment model variables', async t => {
  const { worker, children, profilesDir } = service();
  t.after(() => rmSync(profilesDir, { recursive: true, force: true }));
  delete worker.speechConfig.AXIOM_TRANSCRIPTION_MODEL;
  delete worker.speechConfig.AXIOM_SPEECH_MODEL;
  delete worker.speechConfig.AXIOM_SPEECH_VOICE;
  await worker.start({ runID: 'run-1', agentID: 'agent-1', url: 'https://meet.google.com/abc-defg-hij',
    issuerToken: 'bot', speechToken: 'speech', transcriptionModel: 'openai/whisper',
    speechModel: 'openai/tts', voice: 'alloy' });
  assert.equal(children[0].options.env.AXIOM_TRANSCRIPTION_MODEL, 'openai/whisper');
  assert.equal(children[0].options.env.AXIOM_SPEECH_MODEL, 'openai/tts');
  assert.equal(children[0].options.env.AXIOM_SPEECH_VOICE, 'alloy');
});

test('worker startup error stays failed even if an exit event follows', async t => {
  const { worker, children, profilesDir, requests } = service();
  t.after(() => rmSync(profilesDir, { recursive: true, force: true }));
  const input = { runID: 'run-1', agentID: 'agent-1', url: 'https://meet.google.com/abc-defg-hij',
    issuerToken: 'bot', speechToken: 'speech' };
  await worker.start(input);
  children[0].child.emit('error', new Error('spawn failed'));
  children[0].child.emit('exit', 0);
  assert.equal((await worker.status(input)).status, 'failed');
  assert.ok(requests.some(({ path, body }) => path.endsWith('/outcome') && body.outcome === 'failed'));
});

test('meeting duration is bounded and reflected in the session lease', async t => {
  const { worker, profilesDir } = service();
  t.after(() => rmSync(profilesDir, { recursive: true, force: true }));
  const input = { runID: 'run-1', agentID: 'agent-1', url: 'https://meet.google.com/abc-defg-hij',
    issuerToken: 'bot', speechToken: 'speech' };
  await assert.rejects(worker.start({ ...input, durationMinutes: 10 }), /between 15 and 480/);
  const session = await worker.start({ ...input, durationMinutes: 30 });
  assert.ok(Math.abs(Date.parse(session.expiresAt) - Date.parse(session.startedAt) - 30 * 60000) < 1000);
});

test('a worker stuck during teardown is force stopped and its watchdog is cleared on exit', async t => {
  const { worker, children, profilesDir } = service({ terminationGraceMs: 10 });
  t.after(() => rmSync(profilesDir, { recursive: true, force: true }));
  const input = { runID: 'run-1', agentID: 'agent-1', url: 'https://meet.google.com/abc-defg-hij',
    issuerToken: 'bot', speechToken: 'speech' };
  await worker.start(input);
  await worker.stop(input);
  assert.equal(children[0].child.signal, 'SIGTERM');
  await new Promise(resolve => setTimeout(resolve, 30));
  assert.equal(children[0].child.signal, 'SIGKILL');
  children[0].child.emit('exit', null);
  assert.equal((await worker.status(input)).status, 'ended');

  await worker.start(input);
  await worker.stop(input);
  children[1].child.emit('exit', 0);
  await new Promise(resolve => setTimeout(resolve, 30));
  assert.equal(children[1].child.signal, 'SIGTERM');
});
