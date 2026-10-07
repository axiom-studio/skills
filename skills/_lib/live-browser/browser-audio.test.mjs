import assert from 'node:assert/strict';
import { PassThrough } from 'node:stream';
import { test } from 'node:test';
import { BrowserAudio } from './browser-audio.mjs';

const route = { commands: { capture: ['parec', []], playback: ['pacat', []] } };
const GATEWAY = 'https://gateway.example/rest/v1/llm-gateway/v1/';

function fixture({ failGrant = false } = {}) {
  const transcript = [], utterances = [], statuses = [], spoken = [], requests = [], grants = [];
  const input = new PassThrough();
  const conversation = {
    attach: async () => ({}),
    appendTranscript: async (text, speaker) => { transcript.push([speaker, text]); return { version: transcript.length }; },
    postUtterance: async (text, _signal, speaker) => { utterances.push([speaker, text]); return { id: `m-${utterances.length}`, sequence: 10 }; },
    postStatus: async text => { statuses.push(text); },
    request: async () => [{ sequence: 11, replyToMessageId: 'm-1', sender: { type: 'agent', id: 'agent-1' }, audience: { kind: 'channel' }, content: 'Here is the answer.' }],
  };
  const fetchAPI = async (url, options) => {
    const parsed = new URL(url);
    requests.push([parsed.pathname, options.headers.Authorization]);
    if (parsed.pathname.endsWith('/models')) {
      const speech = parsed.searchParams.get('surface') === 'audio.speech';
      return { ok: true, json: async () => ({ data: [{ id: speech ? 'tts' : 'stt', model_ref: speech ? 'openai/tts' : 'openai/whisper',
        surfaces: [speech ? 'audio.speech' : 'audio.transcriptions'], supported_voices: speech ? ['alloy'] : [] }], links: {} }) };
    }
    if (parsed.pathname.endsWith('/audio/transcriptions')) return { ok: true, json: async () => ({ text: 'Ada, can you summarise?' }) };
    if (parsed.pathname.endsWith('/audio/speech')) return { ok: true, arrayBuffer: async () => Uint8Array.from([1, 2, 3]).buffer };
    throw new Error(`unexpected ${parsed.pathname}`);
  };
  const audio = new BrowserAudio({ route, conversation, agentID: 'agent-1', agentLabel: 'Ada', fetchAPI, speechBaseURL: GATEWAY,
    grants: {
      catalog: async () => ({ token: 'catalog-token', expiresAt: new Date(Date.now() + 60000).toISOString() }),
      audio: async selection => {
        grants.push(selection);
        if (failGrant) throw new Error('grant denied');
        return { transcriptionToken: 'stt-token', speechToken: 'tts-token', expiresAt: new Date(Date.now() + 600000).toISOString() };
      },
    },
    decode: async () => Buffer.alloc(320),
    openAudioStream: () => ({ input, speak: async pcm => { spoken.push(pcm.length); }, close: () => input.end() }) });
  return { audio, input, transcript, utterances, statuses, spoken, requests, grants };
}

const settle = async (check, tries = 60) => { for (let i = 0; i < tries && !check(); i++) await new Promise(resolve => setTimeout(resolve, 50)); };

test('listen transcribes through the gateway, routes addressed lines and speaks the reply', async () => {
  const f = fixture();
  assert.deepEqual(await f.audio.listen({ speakerLabel: 'Meeting participant' }), { listening: true, speakerLabel: 'Meeting participant',
    transcriptionModel: 'openai/whisper', speechModel: 'openai/tts', voice: 'alloy' });
  assert.deepEqual(f.grants, [{ transcriptionModel: 'openai/whisper', speechModel: 'openai/tts' }]);
  const speech = Buffer.alloc(640);
  for (let i = 0; i < speech.length; i += 2) speech.writeInt16LE(3000, i);
  for (let i = 0; i < 20; i++) f.input.write(speech);
  for (let i = 0; i < 30; i++) f.input.write(Buffer.alloc(640));
  await settle(() => f.spoken.length > 0 && f.transcript.length >= 2);
  assert.deepEqual(f.transcript[0], ['Meeting participant', 'Ada, can you summarise?']);
  assert.deepEqual(f.utterances, [['Meeting participant', 'Ada, can you summarise?']]);
  assert.ok(f.spoken.length > 0, 'the reply reaches the virtual microphone');
  assert.deepEqual(f.transcript.at(-1), ['Ada', 'Here is the answer.']);
  assert.deepEqual(f.requests.filter(([path]) => path.includes('/audio/')).map(([path, auth]) => [path.split('/').at(-1), auth]),
    [['transcriptions', 'Bearer stt-token'], ['speech', 'Bearer tts-token']]);
  assert.ok(f.requests.filter(([path]) => path.endsWith('/models')).every(([, auth]) => auth === 'Bearer catalog-token'));
  assert.equal(f.audio.stopListening().listening, false);
  f.audio.close();
});

test('explicit speech needs no provider credential and rejects unknown voices', async () => {
  const f = fixture();
  assert.deepEqual(await f.audio.speak('Hello everyone'), { delivery: 'played' });
  assert.ok(f.spoken.length > 0);
  await assert.rejects(f.audio.speak('Hi', { voice: 'nonexistent' }), /unavailable/);
  await assert.rejects(f.audio.speak(''), /1-3000/);
  await assert.rejects(f.audio.listen({ speakerLabel: 'bad\nlabel' }), /speaker label/);
  f.audio.close();
  await assert.rejects(f.audio.speak('Hello'), /closed|expired|canceled/);
});

test('a refused audio grant fails without leaking tokens', async () => {
  const f = fixture({ failGrant: true });
  await assert.rejects(f.audio.listen({}), error => !/token/.test(error.message));
  assert.equal(f.audio.listening, false);
  f.audio.close();
});
