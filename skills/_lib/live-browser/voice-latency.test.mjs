import test from 'node:test';
import assert from 'node:assert/strict';
import { playSpeechChunks, timedVoiceStage } from './voice-latency.mjs';

test('playback only waits for audio duration not already consumed by backpressure', async () => {
  let clock = 0;
  const sleeps = [], events = [];
  await playSpeechChunks('Hello.', {
    synthesize: async () => { events.push('synthesize'); return Buffer.alloc(1); },
    decode: async () => Buffer.alloc(32000),
    onPlaybackStart: () => events.push('start'),
    speak: async () => { events.push('speak'); clock += 800; },
    now: () => clock, sleep: async ms => sleeps.push(ms),
    measure: (_, operation) => operation(),
  });
  assert.deepEqual(sleeps, [300]);
  assert.deepEqual(events, ['synthesize', 'start', 'speak']);
});

test('short chunks preserve text and prefetch at most one chunk during playback', async () => {
  const text = Array(80).fill('Hello friend.').join(' ');
  const chunks = [];
  let played = 0;
  await playSpeechChunks(text, {
    maximum: 240,
    synthesize: async chunk => {
      chunks.push(chunk);
      assert.ok(chunks.length <= played + 2);
      assert.ok(chunk.length <= 240);
      return Buffer.alloc(1);
    },
    decode: async () => Buffer.alloc(320),
    speak: async () => { played++; }, sleep: async () => {},
    measure: (_, operation) => operation(),
  });
  assert.equal(chunks.join(' '), text);
  assert.equal(played, chunks.length);
});

test('abort during synthesis prevents playback', async () => {
  const controller = new AbortController();
  await assert.rejects(playSpeechChunks('Hello.', {
    signal: controller.signal,
    synthesize: async () => { controller.abort(); return Buffer.alloc(1); },
    decode: async () => Buffer.alloc(320),
    speak: () => assert.fail('must not speak'),
    measure: (_, operation) => operation(),
  }), /canceled/);
});

test('already canceled speech does not spend a synthesis request', async () => {
  const controller = new AbortController();
  controller.abort();
  await assert.rejects(playSpeechChunks('Hello.', {
    signal: controller.signal, synthesize: () => assert.fail('must not synthesize'),
  }), /canceled/);
});

test('stage metrics exclude provider errors and speech text', async () => {
  const logs = [];
  let clock = 1;
  await assert.rejects(timedVoiceStage('transcription', async () => {
    clock = 26; throw new Error('private provider response');
  }, value => logs.push(JSON.parse(value)), () => clock));
  assert.deepEqual(logs, [{ event: 'browser_voice_stage', stage: 'transcription', outcome: 'error', durationMs: 25 }]);
});
