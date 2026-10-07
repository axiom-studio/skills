import { openAudio } from './audio.mjs';
import { isAddressed } from './attention.mjs';
import { ElevenLabsClient } from './elevenlabs.mjs';
import { RealtimeCapture, RealtimeTranscription } from './realtime-transcription.mjs';
import { ReplyInbox } from './reply-inbox.mjs';
import { SpeechPlayback } from './speech-playback.mjs';
import { TranscriptQueue } from './transcript-queue.mjs';
import { realtimeFailureStage, voiceFailureMessage } from './voice-failure.mjs';
import { playSpeechStream } from './voice-latency.mjs';

const PREFERRED_SPEECH_MODELS = ['eleven_flash_v2_5', 'eleven_turbo_v2_5', 'eleven_multilingual_v2'];
const LABEL = /^[^\u0000-\u001f\u007f]{1,100}$/;

function label(value, fallback) {
  if (value === undefined) return fallback;
  if (typeof value !== 'string' || !LABEL.test(value.trim())) throw new Error('Invalid speaker label');
  return value.trim();
}

// Site-agnostic listen/speak capabilities for one live browser.
//
// Listen: page audio (PulseAudio sink monitor) -> ElevenLabs realtime
// transcription -> transcript lines in the Seal Chat; lines addressed to the
// agent become chat utterances, and the agent's replies are spoken back.
// Speak: ElevenLabs PCM stream -> the page's virtual microphone.
//
// The provider key lives only in this object's memory while audio is on. No
// audio, transcript text or provider body is logged; metrics carry only fixed
// stage names and durations.
export class BrowserAudio {
  #route; #conversation; #agentID; #agentLabel; #fetchAPI; #openAudio; #transcriber;
  #audio; #elevenLabs; #speech; #controller = new AbortController();
  #listen; #playback; #speaking = false; #selecting;

  constructor({ route, conversation, agentID, agentLabel = 'Agent', fetchAPI = fetch,
    openAudioStream = openAudio, transcriberFactory = options => new RealtimeTranscription(options) }) {
    if (!route?.commands || !conversation || !agentID) throw new Error('Browser audio is unavailable');
    this.#route = route;
    this.#conversation = conversation;
    this.#agentID = String(agentID);
    this.#agentLabel = label(agentLabel, 'Agent');
    this.#fetchAPI = fetchAPI;
    this.#openAudio = openAudioStream;
    this.#transcriber = transcriberFactory;
    this.#playback = new SpeechPlayback(text => this.#play(text), this.#controller.signal);
  }

  get listening() { return Boolean(this.#listen); }

  get state() {
    return { listening: this.listening, ...(this.#listen ? { speakerLabel: this.#listen.speakerLabel } : {}),
      ...(this.#speech ? { speechModel: this.#speech.model, voice: this.#speech.voice } : {}) };
  }

  #open() {
    if (this.#controller.signal.aborted) throw new Error('Browser audio is closed');
    if (!this.#audio) {
      this.#audio = this.#openAudio(this.#route.commands);
      this.#audio.input.on('data', chunk => {
        // Never transcribe the agent's own voice back into the chat.
        this.#listen?.capture.feed(this.#speaking ? Buffer.alloc(chunk.length) : chunk);
      });
      this.#audio.input.once('end', () => this.#fail('capture_closed'));
    }
    return this.#audio;
  }

  #provider(apiKey) {
    if (apiKey) {
      const key = String(apiKey).trim();
      if (!this.#elevenLabs || this.#elevenLabs.apiKey !== key) {
        this.#elevenLabs = new ElevenLabsClient({ apiKey: key, fetchAPI: this.#fetchAPI });
        this.#speech = undefined;
      }
    }
    if (!this.#elevenLabs) throw new Error('An ElevenLabs Vault credential is required for browser audio');
    return this.#elevenLabs;
  }

  async #select({ speechModel, voice } = {}) {
    if (this.#speech && (!speechModel || speechModel === this.#speech.model) && (!voice || voice === this.#speech.voice)) return this.#speech;
    this.#selecting ??= (async () => {
      const catalog = await this.#elevenLabs.models();
      const models = catalog.speechModels;
      const model = speechModel ? models.find(item => item.id === speechModel)
        : PREFERRED_SPEECH_MODELS.map(id => models.find(item => item.id === id)).find(Boolean) ?? models[0];
      const chosenVoice = voice ?? catalog.voiceOptions?.[0]?.id;
      if (!model || !chosenVoice || !model.voices.includes(chosenVoice)) {
        throw new Error('The selected ElevenLabs speech model or voice is unavailable');
      }
      return { model: model.id, voice: chosenVoice, maxCharacters: model.maxCharacters ?? 3000 };
    })().finally(() => { this.#selecting = undefined; });
    this.#speech = await this.#selecting;
    return this.#speech;
  }

  async #play(text) {
    const audio = this.#open();
    const speech = this.#speech;
    const elevenLabs = this.#elevenLabs;
    if (!speech || !elevenLabs) throw new Error('Speech is unavailable');
    try {
      await playSpeechStream(text, {
        synthesizeStream: (chunk, signal) => elevenLabs.synthesizeStream(chunk, speech.model, speech.voice, signal),
        speak: pcm => audio.speak(pcm), signal: this.#controller.signal, maximum: speech.maxCharacters,
        onPlaybackStart: () => { this.#speaking = true; },
      });
    } finally { this.#speaking = false; }
    await this.#conversation.appendTranscript(text, this.#agentLabel, this.#controller.signal).catch(() => {});
  }

  async listen({ apiKey, speakerLabel, wakePhrases = [], speakReplies = true, speechModel, voice } = {}) {
    const elevenLabs = this.#provider(apiKey);
    const speaker = label(speakerLabel, 'Unknown speaker');
    if (!Array.isArray(wakePhrases) || wakePhrases.length > 8 ||
      wakePhrases.some(value => typeof value !== 'string' || !LABEL.test(value.trim()))) throw new Error('Invalid wake phrases');
    if (typeof speakReplies !== 'boolean') throw new Error('speakReplies must be a boolean');
    if (speakReplies) await this.#select({ speechModel, voice });
    if (this.#listen) {
      Object.assign(this.#listen, { speakerLabel: speaker, phrases: [this.#agentLabel, ...wakePhrases.map(v => v.trim())], speakReplies });
      return this.state;
    }
    this.#open();
    const controller = new AbortController();
    const signal = AbortSignal.any([controller.signal, this.#controller.signal]);
    const listen = { controller, speakerLabel: speaker, phrases: [this.#agentLabel, ...wakePhrases.map(v => v.trim())], speakReplies };
    const fail = stage => { if (this.#listen === listen) this.#fail(stage); };
    const replies = new ReplyInbox({ agentID: this.#agentID, signal,
      read: (cursor, readSignal) => this.#conversation.request(`messages?afterSequence=${cursor}`, 'GET', undefined, readSignal),
      deliver: reply => listen.speakReplies ? this.#playback.enqueue(reply) : undefined,
      onError: stage => fail(stage) });
    const transcripts = new TranscriptQueue({ signal,
      transcribe: async () => '',
      postUtterance: async text => {
        await this.#conversation.appendTranscript(text, listen.speakerLabel, signal);
        if (!isAddressed(text, listen.phrases)) return null;
        return this.#conversation.postUtterance(text, signal, listen.speakerLabel);
      },
      reply: utterance => replies.register(utterance),
      onError: stage => fail(stage) });
    const transcription = this.#transcriber({ apiKey: elevenLabs.apiKey,
      onTranscript: text => transcripts.enqueueText(text),
      onError: stage => fail(realtimeFailureStage(stage)) });
    listen.transcription = transcription;
    listen.capture = new RealtimeCapture({ signal, send: pcm => transcription.send(pcm), onError: stage => fail(stage) });
    this.#listen = listen;
    try {
      await this.#conversation.attach(signal);
      await transcription.ready;
    } catch (error) {
      this.stopListening();
      throw new Error('Live transcription could not start');
    }
    return this.state;
  }

  stopListening() {
    const listen = this.#listen;
    this.#listen = undefined;
    listen?.controller.abort();
    listen?.transcription?.close();
    return this.state;
  }

  #fail(stage) {
    if (!this.#listen) return;
    this.stopListening();
    console.warn(JSON.stringify({ event: 'browser_voice_pipeline_failed', stage }));
    void this.#conversation.postStatus(voiceFailureMessage(stage), this.#controller.signal).catch(() => {});
  }

  // Explicit agent speech. Resolves only after playback into the microphone.
  async speak(text, { apiKey, speechModel, voice } = {}) {
    if (typeof text !== 'string' || !text.trim() || text.length > 3000) throw new Error('Speech requires 1-3000 characters');
    this.#provider(apiKey);
    await this.#select({ speechModel, voice });
    this.#open();
    await this.#playback.enqueue(text.trim(), Date.now() + 120000);
    return { delivery: 'played' };
  }

  close() {
    this.stopListening();
    this.#controller.abort();
    this.#audio?.close();
    this.#audio = undefined;
    this.#elevenLabs = undefined;
  }
}
