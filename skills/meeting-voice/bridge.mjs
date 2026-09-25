import { spawn } from 'node:child_process';
import { randomUUID } from 'node:crypto';

const SAMPLE_RATE = 16000;
const FRAME_BYTES = 640; // 20 ms, mono signed 16-bit PCM.

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

export function pcmWav(pcm, sampleRate = SAMPLE_RATE) {
  if (!Buffer.isBuffer(pcm) || pcm.length % 2 !== 0) throw new Error('16-bit PCM is required');
  const wav = Buffer.alloc(44 + pcm.length);
  wav.write('RIFF', 0);
  wav.writeUInt32LE(36 + pcm.length, 4);
  wav.write('WAVEfmt ', 8);
  wav.writeUInt32LE(16, 16);
  wav.writeUInt16LE(1, 20);
  wav.writeUInt16LE(1, 22);
  wav.writeUInt32LE(sampleRate, 24);
  wav.writeUInt32LE(sampleRate * 2, 28);
  wav.writeUInt16LE(2, 32);
  wav.writeUInt16LE(16, 34);
  wav.write('data', 36);
  wav.writeUInt32LE(pcm.length, 40);
  pcm.copy(wav, 44);
  return wav;
}

export class UtteranceDetector {
  constructor(onUtterance, { threshold = 650, silenceMs = 700, minMs = 300, maxMs = 20000 } = {}) {
    this.onUtterance = onUtterance;
    this.threshold = threshold;
    this.silenceFrames = Math.ceil(silenceMs / 20);
    this.minFrames = Math.ceil(minMs / 20);
    this.maxFrames = Math.ceil(maxMs / 20);
    this.pending = Buffer.alloc(0);
    this.frames = [];
    this.silent = 0;
  }

  feed(chunk) {
    this.pending = Buffer.concat([this.pending, chunk]);
    while (this.pending.length >= FRAME_BYTES) {
      const frame = this.pending.subarray(0, FRAME_BYTES);
      this.pending = this.pending.subarray(FRAME_BYTES);
      let energy = 0;
      for (let i = 0; i < frame.length; i += 2) energy += Math.abs(frame.readInt16LE(i));
      const active = energy / (frame.length / 2) >= this.threshold;
      if (active || this.frames.length) {
        this.frames.push(Buffer.from(frame));
        this.silent = active ? 0 : this.silent + 1;
      }
      if (this.frames.length >= this.maxFrames || this.silent >= this.silenceFrames) {
        const complete = this.frames.length >= this.minFrames;
        const utterance = complete ? Buffer.concat(this.frames) : undefined;
        this.frames = [];
        this.silent = 0;
        if (utterance) this.onUtterance(utterance);
      }
    }
  }

  reset() {
    this.pending = Buffer.alloc(0);
    this.frames = [];
    this.silent = 0;
  }
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
    form.set('file', new Blob([pcmWav(pcm)], { type: 'audio/wav' }), 'meeting.wav');
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

// The browser worker asks its parent to perform ElevenLabs calls. The Vault
// credential remains in the parent and is never included in worker arguments,
// environment variables, or its persistent Chromium profile.
export class ParentSpeechClient {
  constructor({ processRef = process }) {
    this.processRef = processRef;
    this.pending = new Map();
    processRef.on('message', message => {
      if (message?.type !== 'speech-result') return;
      const request = this.pending.get(message.id);
      if (!request) return;
      this.pending.delete(message.id);
      clearTimeout(request.timer);
      if (message.error) request.reject(new Error('voice service request failed'));
      else request.resolve(message);
    });
  }

  request(operation, value, signal) {
    if (signal?.aborted) return Promise.reject(new Error('voice request cancelled'));
    const id = randomUUID();
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => {
        this.pending.delete(id);
        reject(new Error('voice service request timed out'));
      }, 45000);
      this.pending.set(id, { resolve, reject, timer });
      const abort = () => {
        if (this.pending.delete(id)) {
          clearTimeout(timer);
          reject(new Error('voice request cancelled'));
        }
      };
      signal?.addEventListener('abort', abort, { once: true });
      const settle = this.pending.get(id);
      settle.resolve = result => { signal?.removeEventListener('abort', abort); resolve(result); };
      settle.reject = error => { signal?.removeEventListener('abort', abort); reject(error); };
      this.processRef.send?.({ type: 'speech-request', id, operation, ...value }, error => {
        if (error && this.pending.delete(id)) {
          clearTimeout(timer);
          settle.reject(new Error('voice service is unavailable'));
        }
      });
    });
  }

  async transcribe(pcm, signal) {
    if (!Buffer.isBuffer(pcm) || pcm.length > 640000) throw new Error('meeting utterance is too large');
    const result = await this.request('transcribe', { audio: pcm.toString('base64') }, signal);
    return typeof result.text === 'string' ? result.text : '';
  }

  async synthesize(text, signal) {
    const result = await this.request('synthesize', { text }, signal);
    if (typeof result.audio !== 'string') throw new Error('voice service response is invalid');
    return Buffer.from(result.audio, 'base64');
  }
}

export function speechChunks(value, maximum = 3000) {
  if (!Number.isInteger(maximum) || maximum < 32) throw new Error('speech chunk limit is invalid');
  let remaining = String(value ?? '').replace(/\s+/g, ' ').trim();
  const chunks = [];
  while (remaining.length > maximum) {
    const sentence = Math.max(remaining.lastIndexOf('. ', maximum), remaining.lastIndexOf('? ', maximum),
      remaining.lastIndexOf('! ', maximum));
    const word = remaining.lastIndexOf(' ', maximum);
    const boundary = sentence >= maximum / 2 ? sentence + 1 : word >= maximum / 2 ? word : maximum;
    chunks.push(remaining.slice(0, boundary).trim());
    remaining = remaining.slice(boundary).trim();
  }
  if (remaining) chunks.push(remaining);
  return chunks;
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

export async function decodeSpeech(audio, spawnProcess = spawn) {
  const ffmpeg = spawnProcess('ffmpeg', [
    '-hide_banner', '-loglevel', 'error', '-i', 'pipe:0', '-f', 's16le', '-ac', '1', '-ar', String(SAMPLE_RATE), 'pipe:1',
  ], { stdio: ['pipe', 'pipe', 'pipe'] });
  const chunks = [];
  let errors = '';
  ffmpeg.stdout.on('data', chunk => chunks.push(chunk));
  ffmpeg.stderr.on('data', chunk => { errors += chunk.toString(); });
  const finished = new Promise((resolve, reject) => {
    ffmpeg.once('error', reject);
    ffmpeg.once('close', code => code === 0 ? resolve() : reject(new Error(`speech decode failed: ${errors.slice(0, 200)}`)));
  });
  ffmpeg.stdin.end(audio);
  await finished;
  return Buffer.concat(chunks);
}

export class CortexConversation {
  constructor({ baseURL, grant, tenantID, agentID, conversationID, sessionID, fetchAPI = fetch }) {
    this.baseURL = requireText(baseURL, 'Cortex endpoint');
    this.grant = requireText(grant, 'meeting session grant');
    this.tenantID = requireText(tenantID, 'tenant ID');
    this.agentID = requireText(agentID, 'agent deployment ID');
    this.sessionID = requireText(sessionID, 'meeting session ID');
    this.fetchAPI = fetchAPI;
    this.conversationID = requireText(conversationID, 'Seal Chat conversation ID');
    this.revision = 0;
    this.sequence = 0;
  }

  async request(path, method = 'GET', body, signal) {
    const response = await this.fetchAPI(endpoint(this.baseURL, path), {
      method,
      headers: {
        Authorization: `Bearer ${this.grant}`,
        'X-Tenant-ID': this.tenantID,
        ...(body ? { 'Content-Type': 'application/json', 'Idempotency-Key': body.idempotencyKey || randomUUID() } : {}),
      },
      ...(body ? { body: JSON.stringify(body) } : {}),
      signal,
    });
    if (!response.ok) {
      const error = new Error(`Cortex returned HTTP ${response.status}`);
      error.status = response.status;
      throw error;
    }
    const envelope = await response.json();
    if (!envelope.result) throw new Error('Cortex returned no result');
    return envelope.result;
  }

  async attach(signal) {
    const conversation = await this.request('conversation', 'GET', undefined, signal);
    if (conversation.id !== this.conversationID || conversation.scope?.kind !== 'tenant' ||
        String(conversation.scope?.id) !== this.tenantID || conversation.owner?.type !== 'agent' ||
        String(conversation.owner?.id) !== this.agentID || conversation.status !== 'active') {
      throw new Error('Seal Chat conversation does not match the active tenant and agent');
    }
    this.revision = conversation.revision;
    this.sequence = conversation.lastSequence;
    return conversation;
  }

  async postUtterance(text, signal) {
    const result = await this.postMessage({
      intent: 'question',
      content: `A meeting participant said: ${text}`,
    }, signal);
    if (!result.run) throw new Error('Seal Chat did not start an Agent reply for the meeting utterance');
    return result.message;
  }

  async postStatus(text, signal) {
    const result = await this.postMessage({
      intent: 'update',
      content: text,
    }, signal);
    return result.message;
  }

  async postMessage(body, signal) {
    const idempotencyKey = randomUUID();
    for (let attempt = 0; attempt < 3; attempt++) {
      await this.attach(signal);
      try {
        const result = await this.request('messages', 'POST', {
          ...body, expectedRevision: this.revision, idempotencyKey,
        }, signal);
        this.revision = result.conversation.revision;
        this.sequence = Math.max(this.sequence, result.message.sequence);
        return result;
      } catch (error) {
        if (error.status !== 409 || attempt === 2) throw error;
      }
    }
  }

  async waitForReply(triggerMessageID, timeoutMs = 90000, signal) {
    requireText(triggerMessageID, 'meeting utterance ID');
    const deadline = Date.now() + timeoutMs;
    while (Date.now() < deadline && !signal?.aborted) {
      const messages = await this.request(`messages?afterSequence=${this.sequence}`, 'GET', undefined, signal);
      for (const message of messages) {
        this.sequence = Math.max(this.sequence, message.sequence);
        if (message.sender?.type === 'agent' && String(message.sender.id) === this.agentID &&
            message.replyToMessageId === triggerMessageID && message.audience?.kind === 'channel' &&
            typeof message.content === 'string' && message.content.trim()) {
          return message.content.trim();
        }
      }
      await new Promise(resolve => setTimeout(resolve, 500));
    }
    return '';
  }

  setGrant(value) {
    this.grant = requireText(value, 'meeting session grant');
  }
}
