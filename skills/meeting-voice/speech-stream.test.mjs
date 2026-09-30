import test from 'node:test';
import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import { SpeechStreamHost } from './speech-stream.mjs';
import { ParentSpeechClient } from './bridge.mjs';
import { ElevenLabsClient } from './elevenlabs.mjs';
import { playSpeechStream } from './voice-latency.mjs';
import { MeetSessionService } from './session.mjs';

test('ElevenLabs PCM streaming emits early, aligns fragmented samples and bounds packets', async () => {
  let reads = 0, calls = 0;
  const client = new ElevenLabsClient({ apiKey: 'private-key', fetchAPI: async (url, options) => {
    calls++;
    assert.equal(new URL(url).searchParams.get('output_format'), 'pcm_16000');
    assert.match(new URL(url).pathname, /\/voice-id\/stream$/);
    assert.deepEqual(JSON.parse(options.body), { text: 'Hello.', model_id: 'selected-model' });
    return { ok: true, body: (async function* () {
      reads++; yield Buffer.from([1, 2, 3]);
      reads++; yield Buffer.alloc(12801, 4);
    })() };
  } });
  const stream = client.synthesizeStream('Hello.', 'selected-model', 'voice-id');
  const first = await stream.next();
  assert.deepEqual(first.value, Buffer.from([1, 2]));
  assert.equal(reads, 1, 'must not wait for full provider response');
  const packets = [first.value];
  for await (const packet of stream) { assert.ok(packet.length <= 6400); assert.equal(packet.length % 2, 0); packets.push(packet); }
  assert.equal(Buffer.concat(packets).length, 12804);
  assert.equal(calls, 1);
});

test('truncated PCM and empty provider streams fail rather than claiming playback', async () => {
  for (const bytes of [Buffer.alloc(0), Buffer.from([1])]) {
    const client = new ElevenLabsClient({ apiKey: 'private-key', fetchAPI: async () => ({
      ok: true, body: (async function* () { yield bytes; })(),
    }) });
    await assert.rejects(async () => {
      for await (const pcm of client.synthesizeStream('Hello', 'model', 'voice')) assert.fail('no complete PCM expected');
    }, /incomplete/);
  }
});

test('private IPC pulls one packet at a time and cancels provider on consumer exit', async () => {
  let pulls = 0, finished = false;
  const host = new SpeechStreamHost(async function* (text, signal) {
    assert.equal(text, 'Hello');
    try {
      for (let i = 0; i < 10; i++) { pulls++; yield Buffer.from([i, 0]); }
    } finally { finished = true; assert.equal(signal.aborted, true); }
  });
  const processRef = new EventEmitter();
  processRef.send = message => {
    void host.handle(message).then(result => processRef.emit('message', { type: 'speech-result', id: message.id, ...result }),
      () => processRef.emit('message', { type: 'speech-result', id: message.id, error: true }));
  };
  const client = new ParentSpeechClient({ processRef });
  for await (const pcm of client.synthesizeStream('Hello')) {
    assert.deepEqual(pcm, Buffer.from([0, 0]));
    assert.equal(pulls, 1);
    break;
  }
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(finished, true);
  assert.equal(host.active, undefined);
  assert.equal(client.pending.size, 0);
});

test('foreign stream IDs cannot cancel or consume active speech', async () => {
  const host = new SpeechStreamHost(async function* () { yield Buffer.alloc(2); yield Buffer.alloc(2); });
  try {
    await host.handle({ operation: 'stream-start', id: 'owned', text: 'Hello' });
    await assert.rejects(host.handle({ operation: 'stream-cancel', streamID: 'foreign' }), /unavailable/);
    await assert.rejects(host.handle({ operation: 'stream-start', id: 'second', text: 'Hello' }), /occupied/);
    assert.equal((await host.handle({ operation: 'stream-next', streamID: 'owned' })).done, false);
  } finally { host.close(); }
});

test('stream playback starts before generation completes and avoids per-packet tail waits', async () => {
  let time = 0, writes = 0, generated = 0;
  const sleeps = [];
  await playSpeechStream('Hello', {
    synthesizeStream: async function* () {
      time += 80;
      for (let i = 0; i < 5; i++) { generated++; yield Buffer.alloc(6400); }
    },
    speak: async () => { writes++; assert.equal(writes, generated); },
    onPlaybackStart: () => { assert.equal(time, 80); assert.equal(generated, 1); },
    now: () => time, sleep: async ms => { sleeps.push(ms); time += ms; }, report: () => {},
  });
  assert.equal(writes, 5);
  assert.equal(time, 1180); // 80ms first PCM + 1s audio + one 100ms tail.
  assert.deepEqual(sleeps, [200, 200, 200, 200, 300]);
});

test('stream playback accounts for network underruns and write backpressure', async () => {
  let time = 0;
  await playSpeechStream('Hello', {
    synthesizeStream: async function* () {
      yield Buffer.alloc(6400);
      time += 1000; // Provider stalled; previous audio has already played.
      yield Buffer.alloc(6400);
    },
    speak: async () => { time += 180; }, now: () => time,
    sleep: async ms => { time += ms; }, report: () => {},
  });
  assert.equal(time, 1480);
});

test('session parent streams the selected model and voice without exposing its credential', async () => {
  const processRef = new EventEmitter();
  const client = new ElevenLabsClient({ apiKey: 'private-vault-key', fetchAPI: async (_, options) => {
    assert.equal(options.headers['xi-api-key'], 'private-vault-key');
    assert.equal(JSON.parse(options.body).model_id, 'tenant-model');
    return { ok: true, body: (async function* () { yield Buffer.alloc(12800); })() };
  } });
  const session = { status: 'active', elevenLabs: client, speechModel: 'tenant-model', voice: 'tenant-voice',
    child: { send: message => {
      assert.ok(!JSON.stringify(message).includes('private-vault-key'));
      processRef.emit('message', message);
    } } };
  processRef.send = message => { void MeetSessionService.prototype.handleElevenLabsRequest.call({}, session, message); };
  const child = new ParentSpeechClient({ processRef });
  const received = [];
  for await (const pcm of child.synthesizeStream('Hello')) received.push(pcm);
  assert.equal(received.length, 2);
  assert.equal(Buffer.concat(received).length, 12800);
  assert.equal(session.speechStream.active, undefined);
  assert.equal(child.pending.size, 0);
  session.status = 'ended';
  await assert.rejects(async () => {
    for await (const pcm of child.synthesizeStream('Hello')) assert.fail('ended session must not speak');
  }, /failed/);
});

test('canceling before first PCM aborts the parent provider request', async () => {
  let providerSignal;
  const host = new SpeechStreamHost(async function* (_, signal) {
    providerSignal = signal;
    await new Promise((resolve, reject) => signal.addEventListener('abort', () => reject(new Error('canceled')), { once: true }));
    yield Buffer.alloc(2);
  });
  const processRef = new EventEmitter();
  processRef.send = message => {
    void host.handle(message).then(result => processRef.emit('message', { type: 'speech-result', id: message.id, ...result }),
      () => processRef.emit('message', { type: 'speech-result', id: message.id, error: true }));
  };
  const child = new ParentSpeechClient({ processRef }), controller = new AbortController();
  const pending = child.synthesizeStream('Hello', controller.signal).next();
  controller.abort();
  await assert.rejects(pending, /cancelled/);
  assert.equal(providerSignal.aborted, true);
  assert.equal(host.active, undefined);
});
