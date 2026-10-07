import { pcmWav } from './bridge.mjs';

// Axiom managed speech gateway (AXIOM_SPEECH_API_URL). Tokens are short-lived
// grants issued by Cortex per browser session; they are sent only as Bearer
// headers to this gateway and never logged.

function requireText(value, name) {
  if (!value || !String(value).trim()) throw new Error(`${name} is required`);
  return String(value).trim();
}

function endpoint(base, path) {
  const url = new URL(base);
  if (!['http:', 'https:'].includes(url.protocol)) throw new Error('HTTP endpoint is required');
  return new URL(path, url.href.endsWith('/') ? url.href : `${url.href}/`).toString();
}

async function checked(response) {
  if (!response.ok) throw new Error(`voice service returned HTTP ${response.status}`);
  return response;
}

export class SpeechClient {
  constructor({ baseURL, token, transcriptionToken, speechToken, transcriptionModel, speechModel, voice, fetchAPI = fetch }) {
    this.baseURL = requireText(baseURL, 'speech endpoint');
    this.setTokens({ transcriptionToken: transcriptionToken ?? token, speechToken: speechToken ?? token });
    this.transcriptionModel = requireText(transcriptionModel, 'transcription model');
    this.speechModel = requireText(speechModel, 'speech model');
    this.voice = requireText(voice, 'voice');
    this.fetchAPI = fetchAPI;
  }

  setTokens({ transcriptionToken, speechToken }) {
    const transcription = requireText(transcriptionToken, 'transcription token');
    const speaking = requireText(speechToken, 'speech token');
    this.transcriptionToken = transcription;
    this.speechToken = speaking;
  }

  async transcribe(pcm, signal) {
    const form = new FormData();
    form.set('model', this.transcriptionModel);
    form.set('file', new Blob([pcmWav(pcm)], { type: 'audio/wav' }), 'audio.wav');
    const response = await checked(await this.fetchAPI(endpoint(this.baseURL, 'audio/transcriptions'), {
      method: 'POST', headers: { Authorization: `Bearer ${this.transcriptionToken}` }, body: form, signal,
    }));
    const result = await response.json();
    return typeof result.text === 'string' ? result.text.trim() : '';
  }

  async synthesize(text, signal) {
    const response = await checked(await this.fetchAPI(endpoint(this.baseURL, 'audio/speech'), {
      method: 'POST',
      headers: { Authorization: `Bearer ${this.speechToken}`, 'Content-Type': 'application/json' },
      body: JSON.stringify({ model: this.speechModel, voice: this.voice, input: text }), signal,
    }));
    return Buffer.from(await response.arrayBuffer());
  }
}

export async function availableSpeechModels({ baseURL, token, fetchAPI = fetch }) {
  requireText(baseURL, 'speech endpoint');
  requireText(token, 'speech token');
  const list = async surface => {
    const endpointURL = new URL(endpoint(baseURL, 'models'));
    let url = new URL(endpointURL);
    url.searchParams.set('surface', surface);
    url.searchParams.set('limit', '500');
    const models = [];
    for (let page = 0; page < 10; page++) {
      const response = await checked(await fetchAPI(url, {
        headers: { Authorization: `Bearer ${token}` },
      }));
      const body = await response.json();
      if (!Array.isArray(body.data)) throw new Error('speech model catalog is invalid');
      models.push(...body.data);
      if (!body.links?.next) {
        url = null;
        break;
      }
      const next = new URL(body.links.next, url);
      if (next.origin !== endpointURL.origin || next.pathname !== endpointURL.pathname ||
          next.searchParams.get('surface') !== surface) {
        throw new Error('speech model catalog continuation is invalid');
      }
      url = next;
    }
    if (url) throw new Error('speech model catalog exceeds the supported page limit');
    return models.filter(model => Array.isArray(model.surfaces) && model.surfaces.includes(surface))
      .map(model => ({ id: model.model_ref || model.id, name: model.display_name || model.id,
        voices: Array.isArray(model.supported_voices) ? model.supported_voices : [] }))
      .filter(model => typeof model.id === 'string' && model.id.trim());
  };
  return { transcriptionModels: await list('audio.transcriptions'), speechModels: await list('audio.speech') };
}

