import assert from 'node:assert/strict';
import { test } from 'node:test';
import grpc from '@grpc/grpc-js';
import protoLoader from '@grpc/proto-loader';
import { fileURLToPath } from 'node:url';
import { handlers } from './server.mjs';

function invoke(handler, request) {
  return new Promise((resolve, reject) => handler({ request }, (error, result) => error ? reject(error) : resolve(result)));
}

test('hosted Skill action derives authority from Run context and receives URL from config', async () => {
  const calls = [];
  const service = { start: async input => { calls.push(input); return { sessionId: 'session-1', status: 'joining' }; } };
  const reply = await invoke(handlers(service).Execute, {
    node_type: 'meet-start', context: { run_id: 'run-1', agent_id: 'agent-1' },
    config: { url: Buffer.from(JSON.stringify('https://meet.google.com/abc-defg-hij')) },
    bindings: { CORTEX_MEET_ISSUER_TOKEN: Buffer.from(JSON.stringify('bot-secret')),
      CORTEX_MEET_INVOCATION: Buffer.from(JSON.stringify('signed-invocation')),
      AXIOM_SPEECH_TOKEN: Buffer.from(JSON.stringify('speech-secret')) },
    input: { conversationId: Buffer.from(JSON.stringify('foreign-chat')) },
  });
  assert.deepEqual(calls, [{ runID: 'run-1', agentID: 'agent-1', url: 'https://meet.google.com/abc-defg-hij',
    issuerToken: 'bot-secret', invocationToken: 'signed-invocation', speechToken: 'speech-secret', transcriptionModel: undefined,
    speechModel: undefined, voice: undefined, durationMinutes: undefined }]);
  assert.equal(JSON.parse(reply.output.status.toString()), 'joining');
  assert.equal(JSON.parse(reply.output.sessionId.toString()), 'session-1');
});

test('hosted Skill reports action failures without a false success output', async () => {
  const reply = await invoke(handlers({ stop: async () => { throw new Error('not authorized'); } }).Execute,
    { node_type: 'meet-stop', context: { run_id: 'run-1', agent_id: 'agent-1' }, config: {} });
  assert.equal(reply.error.message, 'not authorized');
  assert.equal(reply.output, undefined);
});

test('meet-models sends bound tenant speech authority through the hosted contract', async () => {
  const calls = [];
  const reply = await invoke(handlers({ models: async input => {
    calls.push(input);
    return { transcriptionModels: [], speechModels: [] };
  } }).Execute, {
    node_type: 'meet-models', context: { run_id: 'run-1', agent_id: 'agent-1' }, config: {},
    bindings: { CORTEX_MEET_ISSUER_TOKEN: Buffer.from(JSON.stringify('bot-secret')),
      AXIOM_SPEECH_TOKEN: Buffer.from(JSON.stringify('speech-secret')) },
  });
  assert.deepEqual(calls, [{ runID: 'run-1', agentID: 'agent-1', issuerToken: 'bot-secret', invocationToken: undefined,
    speechToken: 'speech-secret' }]);
  assert.deepEqual(JSON.parse(reply.output.speechModels.toString()), []);
});

test('meeting actions receive the selected ElevenLabs Vault binding', async () => {
  const calls = [];
  const service = { models: async input => { calls.push(input); return { transcriptionModels: [], speechModels: [] }; } };
  await invoke(handlers(service).Execute, {
    node_type: 'meet-models', context: { run_id: 'run-1', agent_id: 'agent-1' }, config: {},
    bindings: { CORTEX_MEET_INVOCATION: Buffer.from(JSON.stringify('signed-invocation')),
      api_key: Buffer.from(JSON.stringify('vault-secret')) },
  });
  assert.equal(calls[0].elevenLabsAPIKey, 'vault-secret');
  assert.equal(calls[0].invocationToken, 'signed-invocation');
});

for (const action of ['meet-models', 'meet-start']) {
  for (const [label, keys, expected] of [
    ['current Vault field', { api_key: 'current-key' }, 'current-key'],
    ['legacy installed binding', { elevenlabs_api: 'legacy-key' }, 'legacy-key'],
    ['current field takes precedence', { api_key: 'current-key', elevenlabs_api: 'legacy-key' }, 'current-key'],
  ]) {
    test(`${action} forwards ${label} only to the service`, async () => {
      const calls = [];
      const capture = async input => { calls.push(input); return { status: 'joining' }; };
      const result = await invoke(handlers({ models: capture, start: capture }).Execute, {
        node_type: action,
        context: { run_id: 'test-run', agent_id: 'test-agent' },
        config: { url: Buffer.from(JSON.stringify('https://meet.google.com/abc-defg-hij')) },
        bindings: Object.fromEntries(Object.entries(keys).map(([key, value]) => [key, Buffer.from(JSON.stringify(value))])),
      });
      assert.equal(calls.length, 1);
      assert.equal(calls[0].elevenLabsAPIKey, expected);
      assert.equal(calls[0].agentID, 'test-agent');
      const output = Object.values(result.output).map(value => value.toString()).join('');
      assert.equal(output.includes(expected), false);
    });
  }
}

test('hosted Skill serves the standard gRPC Execute contract', async () => {
  const definition = protoLoader.loadSync(fileURLToPath(new URL('./skill.proto', import.meta.url)), { keepCase: true });
  const protocol = grpc.loadPackageDefinition(definition).axiom.skill.v1;
  const server = new grpc.Server();
  server.addService(protocol.SkillService.service, handlers({
    status: async () => ({ status: 'none' }),
  }));
  const port = await new Promise((resolve, reject) => server.bindAsync('127.0.0.1:0', grpc.ServerCredentials.createInsecure(),
    (error, bound) => error ? reject(error) : resolve(bound)));
  const client = new protocol.SkillService(`127.0.0.1:${port}`, grpc.credentials.createInsecure());
  try {
    const result = await new Promise((resolve, reject) => client.Execute({
      node_type: 'meet-status', context: { run_id: 'run-1', agent_id: 'agent-1' }, config: {},
    }, (error, value) => error ? reject(error) : resolve(value)));
    assert.equal(JSON.parse(result.output.status.toString()), 'none');
  } finally {
    client.close();
    await new Promise(resolve => server.tryShutdown(resolve));
  }
});
