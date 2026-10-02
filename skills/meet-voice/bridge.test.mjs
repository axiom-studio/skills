import assert from 'node:assert/strict';
import { test } from 'node:test';
import { availableSpeechModels, CortexConversation, pcmWav, SpeechClient, speechChunks, UtteranceDetector } from './bridge.mjs';

test('WAV framing preserves 16-bit mono audio', () => {
  const pcm = Buffer.from([1, 2, 3, 4]);
  const wav = pcmWav(pcm);
  assert.equal(wav.toString('ascii', 0, 4), 'RIFF');
  assert.equal(wav.readUInt32LE(24), 16000);
  assert.equal(wav.readUInt16LE(22), 1);
  assert.deepEqual(wav.subarray(44), pcm);
});

test('utterance detector emits speech after silence and drops brief noise', () => {
  const utterances = [];
  const detector = new UtteranceDetector(value => utterances.push(value), { silenceMs: 40, minMs: 80 });
  const speech = Buffer.alloc(640);
  for (let i = 0; i < speech.length; i += 2) speech.writeInt16LE(3000, i);
  const silence = Buffer.alloc(640);
  detector.feed(Buffer.concat([speech, silence, silence]));
  assert.equal(utterances.length, 0);
  detector.feed(Buffer.concat([speech, speech, speech, silence, silence]));
  assert.equal(utterances.length, 1);
  assert.equal(utterances[0].length, 5 * 640);
});

test('speech client uses the configured Axiom audio routes', async () => {
  const paths = [];
  const fetchAPI = async (url, options) => {
    paths.push([new URL(url).pathname, options.method, options.headers.Authorization]);
    return paths.length === 1
      ? { ok: true, json: async () => ({ text: 'hello' }) }
      : { ok: true, arrayBuffer: async () => Uint8Array.from([1, 2]).buffer };
  };
  const client = new SpeechClient({
    baseURL: 'https://axiom.example/rest/v1/llm-gateway/v1/', token: 'secret',
    transcriptionModel: 'transcribe', speechModel: 'speak', voice: 'alloy', fetchAPI,
  });
  assert.equal(await client.transcribe(Buffer.alloc(640)), 'hello');
  assert.deepEqual(await client.synthesize('reply'), Buffer.from([1, 2]));
  assert.deepEqual(paths, [
    ['/rest/v1/llm-gateway/v1/audio/transcriptions', 'POST', 'Bearer secret'],
    ['/rest/v1/llm-gateway/v1/audio/speech', 'POST', 'Bearer secret'],
  ]);
});

test('speech client keeps transcription and playback grants separate during renewal', async () => {
  const authorization = [];
  const client = new SpeechClient({ baseURL: 'https://axiom.example/rest/v1/llm-gateway/v1/',
    transcriptionToken: 'transcription-1', speechToken: 'speech-1',
    transcriptionModel: 'transcribe', speechModel: 'speak', voice: 'alloy',
    fetchAPI: async (url, request) => {
      authorization.push([new URL(url).pathname, request.headers.Authorization]);
      return new URL(url).pathname.endsWith('/transcriptions')
        ? { ok: true, json: async () => ({ text: 'hello' }) }
        : { ok: true, arrayBuffer: async () => Uint8Array.from([1, 2]).buffer };
    } });
  await client.transcribe(Buffer.alloc(640));
  await client.synthesize('hello');
  client.setTokens({ transcriptionToken: 'transcription-2', speechToken: 'speech-2' });
  await client.transcribe(Buffer.alloc(640));
  await client.synthesize('hello');
  assert.deepEqual(authorization.map(([, token]) => token), [
    'Bearer transcription-1', 'Bearer speech-1', 'Bearer transcription-2', 'Bearer speech-2',
  ]);
});

test('long Agent replies become bounded speech requests without losing words', () => {
  const words = Array.from({ length: 80 }, (_, index) => `word${index}`);
  const chunks = speechChunks(words.join(' '), 80);
  assert.ok(chunks.length > 1);
  assert.ok(chunks.every(chunk => chunk.length <= 80));
  assert.equal(chunks.join(' '), words.join(' '));
  assert.deepEqual(speechChunks(''), []);
});

test('speech options come from the tenant gateway catalog', async () => {
  const requests = [];
  const options = await availableSpeechModels({
    baseURL: 'https://axiom.example/rest/v1/llm-gateway/v1/', token: 'speech-token',
    fetchAPI: async (url, request) => {
      requests.push({ url: new URL(url), authorization: request.headers.Authorization });
      const speech = new URL(url).searchParams.get('surface') === 'audio.speech';
      return { ok: true, json: async () => ({ data: [{ id: speech ? 'tts' : 'whisper',
        model_ref: speech ? 'openai/tts' : 'openai/whisper', display_name: speech ? 'Talk' : 'Listen',
        surfaces: [speech ? 'audio.speech' : 'audio.transcriptions'],
        supported_voices: speech ? ['alloy'] : [] }], links: { next: null } }) };
    },
  });
  assert.deepEqual(options, { transcriptionModels: [{ id: 'openai/whisper', name: 'Listen', voices: [] }],
    speechModels: [{ id: 'openai/tts', name: 'Talk', voices: ['alloy'] }] });
  assert.deepEqual(requests.map(value => value.url.searchParams.get('surface')),
    ['audio.transcriptions', 'audio.speech']);
  assert.ok(requests.every(value => value.authorization === 'Bearer speech-token'));
});

test('speech catalog never follows a credential-bearing link to another origin', async () => {
  let requests = 0;
  await assert.rejects(availableSpeechModels({
    baseURL: 'https://axiom.example/rest/v1/llm-gateway/v1/', token: 'speech-token',
    fetchAPI: async () => {
      requests++;
      return { ok: true, json: async () => ({ data: [], links: { next: 'https://other.example/models' } }) };
    },
  }), /continuation is invalid/);
  assert.equal(requests, 1);
});

test('voice turn stays in the selected Seal Chat and uses the authenticated bot identity', async () => {
  const requests = [];
  const fetchAPI = async (url, options) => {
    requests.push({ url, options });
    const path = new URL(url).pathname;
    const result = path.endsWith('/messages')
      ? { conversation: { revision: 3 }, message: { id: 'message-1', sequence: 6 }, run: { id: 'run-1' } }
      : { id: 'conv-1', scope: { kind: 'tenant', id: '7' }, owner: { type: 'agent', id: '42' }, status: 'active', lastSequence: 5, revision: 2 };
    return { ok: true, json: async () => ({ result }) };
  };
  const client = new CortexConversation({
    baseURL: 'https://cortex.example/orchestrator/agent/meet/v1/sessions/meeting-1/', grant: 'meeting-grant',
    tenantID: '7', agentID: '42', conversationID: 'conv-1', sessionID: 'meeting-1', fetchAPI,
  });
  await client.attach();
  await client.postUtterance('Please check the deployment');
  const posted = JSON.parse(requests.find(request => new URL(request.url).pathname.endsWith('/messages')).options.body);
  assert.equal(requests.at(-1).options.headers['X-Tenant-ID'], '7');
  assert.equal(requests.at(-1).options.headers.Authorization, 'Bearer meeting-grant');
  assert.equal(posted.content, 'A meeting participant said: Please check the deployment');
  assert.equal(posted.sender, undefined);
  assert.equal(posted.expectedRevision, 2);
  assert.equal(client.sequence, 6);
  assert.equal(requests.length, 3);
  assert.ok(requests.every(request => request.options.method !== 'POST' || !new URL(request.url).pathname.endsWith('/conversations')));
});

test('voice worker reports missing Agent reply without calling Team coordination', async () => {
  const requests = [];
  const client = new CortexConversation({
    baseURL: 'https://cortex.example/orchestrator/agent/meet/v1/sessions/meeting-1/', grant: 'meeting-grant',
    tenantID: '7', agentID: '42', conversationID: 'conv-1', sessionID: 'meeting-1',
    fetchAPI: async (url, options) => {
      requests.push(new URL(url).pathname);
      return { ok: true, json: async () => ({ result: options.method === 'POST'
        ? { conversation: { revision: 3 }, message: { id: 'message-1', sequence: 6 } }
        : { id: 'conv-1', scope: { kind: 'tenant', id: '7' }, owner: { type: 'agent', id: '42' }, status: 'active', lastSequence: 5, revision: 2 } }) };
    },
  });
  await assert.rejects(client.postUtterance('hello'), /did not start an Agent reply/);
  assert.ok(requests.every(path => !path.endsWith('/participation-rounds')));
});

test('voice worker refuses a Seal Chat for another agent', async () => {
  const client = new CortexConversation({
    baseURL: 'https://cortex.example/orchestrator/agent/meet/v1/sessions/meeting-1/', grant: 'meeting-grant',
    tenantID: '7', agentID: '42', conversationID: 'conv-1', sessionID: 'meeting-1',
    fetchAPI: async () => ({ ok: true, json: async () => ({ result: {
      id: 'conv-1', scope: { kind: 'tenant', id: '7' }, owner: { type: 'agent', id: '99' },
      status: 'active', lastSequence: 0, revision: 1,
    } }) }),
  });
  await assert.rejects(client.attach(), /does not match/);
});

test('joined meeting status appears in Seal Chat without starting another Agent turn', async () => {
  const requests = [];
  const client = new CortexConversation({
    baseURL: 'https://cortex.example/orchestrator/agent/meet/v1/sessions/meeting-1/', grant: 'meeting-grant',
    tenantID: '7', agentID: '42', conversationID: 'conv-1', sessionID: 'meeting-1',
    fetchAPI: async (url, options) => {
      requests.push({ path: new URL(url).pathname, options });
      return { ok: true, json: async () => ({ result: options.method === 'POST'
        ? { conversation: { revision: 3 }, message: { id: 'status-1', sequence: 6 } }
        : { id: 'conv-1', scope: { kind: 'tenant', id: '7' }, owner: { type: 'agent', id: '42' },
          status: 'active', lastSequence: 5, revision: 2 } }) };
    },
  });
  await client.postStatus('I joined the Google Meet.');
  const body = JSON.parse(requests.at(-1).options.body);
  assert.equal(body.intent, 'update');
  assert.equal(body.requiresResponse, undefined);
  assert.equal(body.content, 'I joined the Google Meet.');
  assert.equal(body.sender, undefined);
  assert.equal(client.sequence, 6);
});

test('meeting observation retries a chat revision conflict with one idempotency key', async () => {
  const posts = [];
  const headers = [];
  let revision = 2;
  const client = new CortexConversation({
    baseURL: 'https://cortex.example/orchestrator/agent/meet/v1/sessions/meeting-1/', grant: 'meeting-grant',
    tenantID: '7', agentID: '42', conversationID: 'conv-1', sessionID: 'meeting-1',
    fetchAPI: async (_url, options) => {
      if (options.method === 'POST') {
        posts.push(JSON.parse(options.body));
        headers.push(options.headers['Idempotency-Key']);
        if (posts.length === 1) { revision = 3; return { ok: false, status: 409 }; }
        return { ok: true, json: async () => ({ result: {
          conversation: { revision: 4 }, message: { id: 'message-1', sequence: 7 }, run: { id: 'run-1' },
        } }) };
      }
      return { ok: true, json: async () => ({ result: {
        id: 'conv-1', scope: { kind: 'tenant', id: '7' }, owner: { type: 'agent', id: '42' },
        status: 'active', lastSequence: revision + 3, revision,
      } }) };
    },
  });
  await client.postUtterance('Please summarize that');
  assert.equal(posts.length, 2);
  assert.deepEqual(posts.map(value => value.expectedRevision), [2, 3]);
  assert.equal(posts[0].idempotencyKey, posts[1].idempotencyKey);
  assert.deepEqual(headers, [posts[0].idempotencyKey, posts[0].idempotencyKey]);
  assert.equal(client.sequence, 7);
});

test('spoken reply matches this meeting utterance and is channel visible', async () => {
  const client = new CortexConversation({
    baseURL: 'https://cortex.example/orchestrator/agent/meet/v1/sessions/meeting-1/', grant: 'meeting-grant',
    tenantID: '7', agentID: '42', conversationID: 'conv-1', sessionID: 'meeting-1',
    fetchAPI: async () => ({ ok: true, json: async () => ({ result: [
      { sequence: 6, sender: { type: 'agent', id: '42' }, replyToMessageId: 'someone-else', audience: { kind: 'channel' }, content: 'Unrelated answer' },
      { sequence: 7, sender: { type: 'agent', id: '42' }, replyToMessageId: 'meeting-1', audience: { kind: 'participants' }, content: 'Private answer' },
      { sequence: 8, sender: { type: 'agent', id: '99' }, replyToMessageId: 'meeting-1', audience: { kind: 'channel' }, content: 'Other Agent' },
      { sequence: 9, sender: { type: 'agent', id: '42' }, replyToMessageId: 'meeting-1', audience: { kind: 'channel' }, content: 'Meeting answer' },
    ] }) }),
  });
  assert.equal(await client.waitForReply('meeting-1', 100), 'Meeting answer');
});
