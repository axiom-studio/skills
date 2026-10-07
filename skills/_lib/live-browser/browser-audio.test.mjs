import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import { PassThrough } from 'node:stream';
import { test } from 'node:test';
import { setImmediate as tick } from 'node:timers/promises';
import { BrowserAudio } from './browser-audio.mjs';

const route = { commands: { capture: ['parec', []], playback: ['pacat', []] } };

function fixture() {
  const transcript = [], utterances = [], statuses = [], spoken = [];
  const input = new PassThrough();
  let transcriber;
  const conversation = {
    attach: async () => ({}),
    appendTranscript: async (text, speaker) => { transcript.push([speaker, text]); return { version: transcript.length }; },
    postUtterance: async (text, _signal, speaker) => { utterances.push([speaker, text]); return { id: `m-${utterances.length}`, sequence: 10 }; },
    postStatus: async text => { statuses.push(text); },
    request: async () => [{ sequence: 11, replyToMessageId: 'm-1', sender: { type: 'agent', id: 'agent-1' }, audience: { kind: 'channel' }, content: 'Here is the answer.' }],
  };
  const fetchAPI = async url => {
    const path = new URL(url).pathname;
    if (path === '/v1/models') return { ok: true, json: async () => [{ model_id: 'eleven_flash_v2_5', can_do_text_to_speech: true }] };
    if (path === '/v2/voices') return { ok: true, json: async () => ({ voices: [{ voice_id: 'voice1', name: 'Voice' }], has_more: false }) };
    if (path.startsWith('/v1/text-to-speech/voice1/stream')) {
      return { ok: true, body: (async function* () { yield new Uint8Array(640); })() };
    }
    throw new Error(`unexpected ${path}`);
  };
  const audio = new BrowserAudio({ route, conversation, agentID: 'agent-1', agentLabel: 'Ada', fetchAPI,
    openAudioStream: () => ({ input, speak: async pcm => { spoken.push(pcm.length); }, close: () => input.end() }),
    transcriberFactory: options => {
      transcriber = Object.assign(new EventEmitter(), options, { ready: Promise.resolve(), sent: [],
        send: async pcm => { transcriber.sent.push(pcm.length); }, close: () => { transcriber.closed = true; } });
      return transcriber;
    } });
  return { audio, input, transcript, utterances, statuses, spoken, transcriber: () => transcriber };
}

test('listen posts transcripts, routes addressed lines to the agent and speaks its replies', async () => {
  const f = fixture();
  assert.deepEqual(await f.audio.listen({ apiKey: 'vault-key', speakerLabel: 'Meeting participant' }),
    { listening: true, speakerLabel: 'Meeting participant', speechModel: 'eleven_flash_v2_5', voice: 'voice1' });
  f.input.write(Buffer.alloc(6400, 1));
  await tick();
  assert.ok(f.transcriber().sent.length >= 1);
  f.transcriber().onTranscript('The deadline is Friday.');
  f.transcriber().onTranscript('Ada, can you summarise?');
  for (let i = 0; i < 20 && f.spoken.length === 0; i++) await new Promise(resolve => setTimeout(resolve, 50));
  assert.deepEqual(f.transcript.slice(0, 2), [['Meeting participant', 'The deadline is Friday.'], ['Meeting participant', 'Ada, can you summarise?']]);
  assert.deepEqual(f.utterances, [['Meeting participant', 'Ada, can you summarise?']]);
  assert.ok(f.spoken.length > 0, 'the agent reply reaches the virtual microphone');
  for (let i = 0; i < 20 && f.transcript.length < 3; i++) await new Promise(resolve => setTimeout(resolve, 50));
  assert.deepEqual(f.transcript.at(-1), ['Ada', 'Here is the answer.']);
  assert.deepEqual(f.audio.stopListening(), { listening: false, speechModel: 'eleven_flash_v2_5', voice: 'voice1' });
  assert.equal(f.transcriber().closed, true);
  f.audio.close();
});

test('explicit speech plays into the microphone and requires a provider credential', async () => {
  const f = fixture();
  await assert.rejects(f.audio.speak('Hello'), /ElevenLabs Vault credential/);
  assert.deepEqual(await f.audio.speak('Hello everyone', { apiKey: 'vault-key' }), { delivery: 'played' });
  assert.ok(f.spoken.length > 0);
  await assert.rejects(f.audio.speak('', { apiKey: 'vault-key' }), /1-3000/);
  await assert.rejects(f.audio.listen({ apiKey: 'vault-key', speakerLabel: 'bad\nlabel' }), /speaker label/);
  f.audio.close();
  await assert.rejects(f.audio.speak('Hello', { apiKey: 'vault-key' }), /closed|expired|canceled/);
});

test('a transcription failure stops listening and reports only a fixed message', async () => {
  const f = fixture();
  await f.audio.listen({ apiKey: 'vault-key', speakReplies: false });
  f.transcriber().onError('auth_error');
  await tick();
  assert.equal(f.audio.listening, false);
  assert.equal(f.statuses.length, 1);
  assert.doesNotMatch(f.statuses[0], /vault-key/);
  f.audio.close();
});
