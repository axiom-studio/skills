import assert from 'node:assert/strict';
import { test } from 'node:test';
import { ElevenLabsClient } from './elevenlabs.mjs';

test('ElevenLabs speech uses the bound Vault key and current transcription and synthesis contracts', async () => {
  const calls = [];
  const fetchAPI = async (url, options) => {
    calls.push({ url: String(url), options });
    if (String(url).endsWith('/v1/models')) return { ok: true, json: async () => [
      { model_id: 'eleven_flash_v2_5', name: 'Flash', can_do_text_to_speech: true,
        maximum_text_length_per_request: 1200 },
      { model_id: 'unsupported', can_do_text_to_speech: false },
    ] };
    if (String(url).includes('/v2/voices')) return { ok: true, json: async () => ({
      voices: [{ voice_id: 'voice123456', name: 'Team voice' }], has_more: false,
    }) };
    if (String(url).endsWith('/v1/speech-to-text')) return { ok: true, json: async () => ({ text: '  Hello team. ' }) };
    return { ok: true, arrayBuffer: async () => Uint8Array.from([1, 2, 3]).buffer };
  };
  const client = new ElevenLabsClient({ apiKey: 'vault-secret', fetchAPI });
  assert.deepEqual(await client.models(), {
    transcriptionModels: [{ id: 'scribe_v2', name: 'Scribe v2', voices: [] },
      { id: 'scribe_v2_realtime', name: 'Scribe v2 Realtime', voices: [] }],
    speechModels: [{ id: 'eleven_flash_v2_5', name: 'Flash', voices: ['voice123456'], maxCharacters: 1200 }],
    voiceOptions: [{ id: 'voice123456', name: 'Team voice' }],
  });
  assert.equal(await client.transcribe(Buffer.alloc(320), 'scribe_v2'), 'Hello team.');
  assert.deepEqual(await client.synthesize('Yes.', 'eleven_flash_v2_5', 'voice123456'), Buffer.from([1, 2, 3]));
  assert.ok(calls.every(call => call.options.headers['xi-api-key'] === 'vault-secret'));
  assert.equal(calls[2].options.body.get('model_id'), 'scribe_v2');
  assert.equal(calls[2].options.body.get('file').name, 'audio.wav');
  assert.deepEqual(JSON.parse(calls[3].options.body), { text: 'Yes.', model_id: 'eleven_flash_v2_5' });
  assert.match(calls[3].url, /\/v1\/text-to-speech\/voice123456$/);
});

test('provider failures do not expose response bodies or Vault credentials', async () => {
  const client = new ElevenLabsClient({ apiKey: 'vault-secret', fetchAPI: async () => ({
    ok: false, status: 403, text: async () => 'vault-secret',
  }) });
  await assert.rejects(client.models(), error => error.message === 'ElevenLabs voice request failed (HTTP 403)');
});

test('provider errors expose only fixed actionable categories, never provider text', async () => {
  for (const [status, expected] of [
    ['invalid_api_key', /key was rejected/],
    ['missing_permissions', /lacks permission/],
    ['quota_exceeded', /insufficient quota/],
    ['voice_not_found', /voice is unavailable/],
    ['vault-secret', /^ElevenLabs voice request failed \(HTTP 401\)$/],
    ['__proto__', /^ElevenLabs voice request failed \(HTTP 401\)$/],
  ]) {
    const client = new ElevenLabsClient({ apiKey: 'vault-secret', fetchAPI: async () => ({
      ok: false, status: 401,
      json: async () => ({ detail: { status, message: 'vault-secret and private meeting content' } }),
    }) });
    await assert.rejects(client.models(), error => {
      assert.match(error.message, expected);
      assert.doesNotMatch(error.message, /vault-secret|private meeting|__proto__/);
      return true;
    });
  }
});
