import { pcmWav } from './bridge.mjs';

const API = 'https://api.elevenlabs.io';
const VOICE_ID = /^[A-Za-z0-9_-]{1,100}$/;
const MODEL_ID = /^[A-Za-z0-9_-]{1,100}$/;

export class ElevenLabsClient {
  constructor({ apiKey, fetchAPI = fetch }) {
    if (typeof apiKey !== 'string' || !apiKey.trim()) throw new Error('ElevenLabs Vault credential is required');
    this.apiKey = apiKey.trim();
    this.fetchAPI = fetchAPI;
  }

  async request(path, options = {}) {
    const response = await this.fetchAPI(new URL(path, API), {
      ...options,
      headers: { 'xi-api-key': this.apiKey, ...options.headers },
    });
    // Provider bodies may include request data. Never echo them into an Agent action.
    if (!response.ok) throw new Error(`ElevenLabs voice request failed (HTTP ${response.status})`);
    return response;
  }

  async models() {
    const modelResponse = await this.request('/v1/models');
    const rawModels = await modelResponse.json();
    if (!Array.isArray(rawModels)) throw new Error('ElevenLabs model catalog is invalid');
    const speechModels = rawModels.filter(model => model?.can_do_text_to_speech === true &&
      typeof model.model_id === 'string' && MODEL_ID.test(model.model_id))
      .map(model => ({ id: model.model_id, name: model.name || model.model_id, voices: [] }));
    const voices = [];
    let pageToken;
    for (let page = 0; page < 10; page++) {
      const url = new URL('/v2/voices', API);
      url.searchParams.set('page_size', '100');
      if (pageToken) url.searchParams.set('next_page_token', pageToken);
      const response = await this.request(url);
      const body = await response.json();
      if (!Array.isArray(body?.voices)) throw new Error('ElevenLabs voice catalog is invalid');
      for (const voice of body.voices) {
        if (typeof voice?.voice_id === 'string' && VOICE_ID.test(voice.voice_id)) {
          voices.push({ id: voice.voice_id, name: voice.name || voice.voice_id });
        }
      }
      if (!body.has_more) { pageToken = undefined; break; }
      if (typeof body.next_page_token !== 'string' || !body.next_page_token) {
        throw new Error('ElevenLabs voice catalog continuation is invalid');
      }
      pageToken = body.next_page_token;
    }
    if (pageToken) throw new Error('ElevenLabs voice catalog exceeds the supported page limit');
    for (const model of speechModels) model.voices = voices.map(voice => voice.id);
    return { transcriptionModels: [{ id: 'scribe_v2', name: 'Scribe v2', voices: [] }], speechModels, voiceOptions: voices };
  }

  async transcribe(pcm, model, signal) {
    if (model !== 'scribe_v2') throw new Error('unsupported ElevenLabs transcription model');
    if (!Buffer.isBuffer(pcm) || pcm.length > 640000) throw new Error('meeting utterance is too large');
    const form = new FormData();
    form.set('model_id', model);
    form.set('file', new Blob([pcmWav(pcm)], { type: 'audio/wav' }), 'meeting.wav');
    const response = await this.request('/v1/speech-to-text', { method: 'POST', body: form, signal });
    const body = await response.json();
    if (typeof body?.text !== 'string') throw new Error('ElevenLabs transcription response is invalid');
    return body.text.trim();
  }

  async synthesize(text, model, voice, signal) {
    if (typeof model !== 'string' || !MODEL_ID.test(model) ||
        typeof voice !== 'string' || !VOICE_ID.test(voice) ||
        typeof text !== 'string' || !text.trim() || text.length > 3000) {
      throw new Error('ElevenLabs speech selection is invalid');
    }
    const response = await this.request(`/v1/text-to-speech/${encodeURIComponent(voice)}`, {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ text, model_id: model }), signal,
    });
    const audio = Buffer.from(await response.arrayBuffer());
    if (audio.length > 10000000) throw new Error('ElevenLabs speech response is too large');
    return audio;
  }
}
