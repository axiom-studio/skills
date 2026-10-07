import assert from 'node:assert/strict';
import { test } from 'node:test';
import { availableSpeechModels, SpeechClient } from './speech-gateway.mjs';

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
