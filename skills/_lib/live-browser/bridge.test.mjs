import assert from 'node:assert/strict';
import { test } from 'node:test';
import { CortexConversation, pcmWav, speechChunks, UtteranceDetector } from './bridge.mjs';

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

test('long Agent replies become bounded speech requests without losing words', () => {
  const words = Array.from({ length: 80 }, (_, index) => `word${index}`);
  const chunks = speechChunks(words.join(' '), 80);
  assert.ok(chunks.length > 1);
  assert.ok(chunks.every(chunk => chunk.length <= 80));
  assert.equal(chunks.join(' '), words.join(' '));
  assert.deepEqual(speechChunks(''), []);
});

test('spoken turn stays in the selected Seal Chat and uses the authenticated bot identity', async () => {
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
    baseURL: 'https://cortex.example/orchestrator/agent/browser/v1/sessions/browser-1/audio/', grant: 'audio-grant',
    tenantID: '7', agentID: '42', conversationID: 'conv-1', sessionID: 'browser-1', fetchAPI,
  });
  await client.attach();
  await client.postUtterance('Please check the deployment');
  const posted = JSON.parse(requests.find(request => new URL(request.url).pathname.endsWith('/messages')).options.body);
  assert.equal(requests.at(-1).options.headers['X-Tenant-ID'], '7');
  assert.equal(requests.at(-1).options.headers.Authorization, 'Bearer audio-grant');
  assert.equal(posted.content, 'Someone speaking in the live browser said (unverified speaker: Unknown speaker): Please check the deployment');
  assert.equal(posted.sender, undefined);
  assert.equal(posted.expectedRevision, 2);
  assert.equal(client.sequence, 6);
  assert.equal(requests.length, 3);
  assert.ok(requests.every(request => request.options.method !== 'POST' || !new URL(request.url).pathname.endsWith('/conversations')));
});

test('transcript retries preserve event identity and serialize concurrent bot and participant appends', async () => {
  const requests = [];
  let attempt = 0;
  const client = new CortexConversation({
    baseURL: 'https://cortex.example/orchestrator/agent/browser/v1/sessions/browser-1/audio/', grant: 'grant',
    tenantID: '7', agentID: '42', conversationID: 'conv-1', sessionID: 'browser-1',
    fetchAPI: async (url, options) => {
      assert.equal(new URL(url).pathname.endsWith('/transcript'), true);
      const body = JSON.parse(options.body); requests.push(body);
      if (attempt++ === 0) throw new Error('lost response');
      return { ok: true, json: async () => ({ result: { id: 'transcript', version: body.expectedVersion + 1 } }) };
    },
  });
  await Promise.all([client.appendTranscript('Hello', 'Kevin'), client.appendTranscript('Hello back', 'Agent')]);
  assert.deepEqual(requests[0], requests[1]);
  assert.equal(requests[2].expectedVersion, 1);
  assert.equal(client.transcriptVersion, 2);
});

test('voice worker reports missing Agent reply without calling Team coordination', async () => {
  const requests = [];
  const client = new CortexConversation({
    baseURL: 'https://cortex.example/orchestrator/agent/browser/v1/sessions/browser-1/audio/', grant: 'audio-grant',
    tenantID: '7', agentID: '42', conversationID: 'conv-1', sessionID: 'browser-1',
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
    baseURL: 'https://cortex.example/orchestrator/agent/browser/v1/sessions/browser-1/audio/', grant: 'audio-grant',
    tenantID: '7', agentID: '42', conversationID: 'conv-1', sessionID: 'browser-1',
    fetchAPI: async () => ({ ok: true, json: async () => ({ result: {
      id: 'conv-1', scope: { kind: 'tenant', id: '7' }, owner: { type: 'agent', id: '99' },
      status: 'active', lastSequence: 0, revision: 1,
    } }) }),
  });
  await assert.rejects(client.attach(), /does not match/);
});

test('browser audio status appears in Seal Chat without starting another Agent turn', async () => {
  const requests = [];
  const client = new CortexConversation({
    baseURL: 'https://cortex.example/orchestrator/agent/browser/v1/sessions/browser-1/audio/', grant: 'audio-grant',
    tenantID: '7', agentID: '42', conversationID: 'conv-1', sessionID: 'browser-1',
    fetchAPI: async (url, options) => {
      requests.push({ path: new URL(url).pathname, options });
      return { ok: true, json: async () => ({ result: options.method === 'POST'
        ? { conversation: { revision: 3 }, message: { id: 'status-1', sequence: 6 } }
        : { id: 'conv-1', scope: { kind: 'tenant', id: '7' }, owner: { type: 'agent', id: '42' },
          status: 'active', lastSequence: 5, revision: 2 } }) };
    },
  });
  await client.postStatus('I am listening to the page.');
  const body = JSON.parse(requests.at(-1).options.body);
  assert.equal(body.intent, 'update');
  assert.equal(body.requiresResponse, undefined);
  assert.equal(body.content, 'I am listening to the page.');
  assert.equal(body.sender, undefined);
  assert.equal(client.sequence, 6);
});

test('spoken observation retries a chat revision conflict with one idempotency key', async () => {
  const posts = [];
  const headers = [];
  let revision = 2;
  const client = new CortexConversation({
    baseURL: 'https://cortex.example/orchestrator/agent/browser/v1/sessions/browser-1/audio/', grant: 'audio-grant',
    tenantID: '7', agentID: '42', conversationID: 'conv-1', sessionID: 'browser-1',
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

test('spoken reply matches this utterance and is channel visible', async () => {
  const client = new CortexConversation({
    baseURL: 'https://cortex.example/orchestrator/agent/browser/v1/sessions/browser-1/audio/', grant: 'audio-grant',
    tenantID: '7', agentID: '42', conversationID: 'conv-1', sessionID: 'browser-1',
    fetchAPI: async () => ({ ok: true, json: async () => ({ result: [
      { sequence: 6, sender: { type: 'agent', id: '42' }, replyToMessageId: 'someone-else', audience: { kind: 'channel' }, content: 'Unrelated answer' },
      { sequence: 7, sender: { type: 'agent', id: '42' }, replyToMessageId: 'utterance-1', audience: { kind: 'participants' }, content: 'Private answer' },
      { sequence: 8, sender: { type: 'agent', id: '99' }, replyToMessageId: 'utterance-1', audience: { kind: 'channel' }, content: 'Other Agent' },
      { sequence: 9, sender: { type: 'agent', id: '42' }, replyToMessageId: 'utterance-1', audience: { kind: 'channel' }, content: 'Spoken answer' },
    ] }) }),
  });
  assert.equal(await client.waitForReply('utterance-1', 100), 'Spoken answer');
});

test('reply cursor is isolated from concurrent transcript writes', async () => {
  const cursors = [];
  const client = new CortexConversation({
    baseURL: 'https://cortex.example/orchestrator/agent/browser/v1/sessions/browser-1/audio/', grant: 'audio-grant',
    tenantID: '7', agentID: '42', conversationID: 'conv-1', sessionID: 'browser-1',
    fetchAPI: async url => {
      cursors.push(new URL(url).searchParams.get('afterSequence'));
      client.sequence = 100;
      return { ok: true, json: async () => ({ result: cursors.length === 1 ? [] : [
        { sequence: 9, sender: { type: 'agent', id: '42' }, replyToMessageId: 'trigger', audience: { kind: 'channel' }, content: 'Answer' },
      ] }) };
    },
  });
  client.sequence = 50;
  assert.equal(await client.waitForReply('trigger', 2000, undefined, 6), 'Answer');
  assert.deepEqual(cursors, ['6', '6']);
  assert.equal(client.sequence, 100);
});
