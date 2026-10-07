import { openAudio, SAMPLE_RATE } from './audio.mjs';
import { isAddressed } from './attention.mjs';
import { decodeSpeech, UtteranceDetector } from './bridge.mjs';
import { ReplyInbox } from './reply-inbox.mjs';
import { availableSpeechModels, SpeechClient } from './speech-gateway.mjs';
import { SpeechPlayback } from './speech-playback.mjs';
import { TranscriptQueue } from './transcript-queue.mjs';
import { voiceFailureMessage } from './voice-failure.mjs';
import { playSpeechChunks, timedVoiceStage } from './voice-latency.mjs';

const LABEL = /^[^\u0000-\u001f\u007f]{1,100}$/;

function label(value, fallback) {
  if (value === undefined) return fallback;
  if (typeof value !== 'string' || !LABEL.test(value.trim())) throw new Error('Invalid speaker label');
  return value.trim();
}

// Site-agnostic listen/speak capabilities for one live browser, through the
// Axiom managed speech gateway with short-lived grants issued by Cortex for
// this browser session.
//
// Listen: page audio (PulseAudio sink monitor) -> utterance detection ->
// gateway transcription -> transcript lines in the Seal Chat; lines that
// address the agent become chat utterances, and its replies are spoken back.
// Speak: gateway speech -> decoded PCM -> the page's virtual microphone.
//
// No audio, transcript text, token or provider body is logged; metrics carry
// only fixed stage names and durations.
export class BrowserAudio {
  #route; #conversation; #agentID; #agentLabel; #grants; #speechBaseURL; #fetchAPI; #openAudio; #decode;
  #audio; #client; #selection; #renewTimer; #controller = new AbortController();
  #listen; #playback; #speaking = false; #selecting;

  // grants: { catalog(): {token}, audio({transcriptionModel, speechModel}): {transcriptionToken, speechToken, expiresAt} }
  constructor({ route, conversation, agentID, agentLabel = 'Agent', grants, speechBaseURL, fetchAPI = fetch,
    openAudioStream = openAudio, decode = decodeSpeech }) {
    if (!route?.commands || !conversation || !agentID || !grants?.catalog || !grants?.audio || !speechBaseURL) {
      throw new Error('Browser audio is unavailable');
    }
    this.#route = route;
    this.#conversation = conversation;
    this.#agentID = String(agentID);
    this.#agentLabel = label(agentLabel, 'Agent');
    this.#grants = grants;
    this.#speechBaseURL = speechBaseURL;
    this.#fetchAPI = fetchAPI;
    this.#openAudio = openAudioStream;
    this.#decode = decode;
    this.#playback = new SpeechPlayback(text => this.#play(text), this.#controller.signal);
  }

  get listening() { return Boolean(this.#listen); }

  get state() {
    return { listening: this.listening, ...(this.#listen ? { speakerLabel: this.#listen.speakerLabel } : {}),
      ...(this.#selection ? { transcriptionModel: this.#selection.transcriptionModel, speechModel: this.#selection.speechModel,
        voice: this.#selection.voice } : {}) };
  }

  setAgentLabel(value) { this.#agentLabel = label(value, this.#agentLabel); }

  #open() {
    if (this.#controller.signal.aborted) throw new Error('Browser audio is closed');
    if (!this.#audio) {
      this.#audio = this.#openAudio(this.#route.commands);
      this.#audio.input.on('data', chunk => {
        // Never transcribe the agent's own voice back into the chat.
        if (!this.#speaking) this.#listen?.detector.feed(chunk);
      });
      this.#audio.input.once('end', () => this.#fail('capture_closed'));
    }
    return this.#audio;
  }

  // Chooses models from the tenant's gateway catalog unless given, then
  // obtains session-bound transcription and speech grants.
  async #select({ transcriptionModel, speechModel, voice } = {}) {
    const current = this.#selection;
    if (current && (!transcriptionModel || transcriptionModel === current.transcriptionModel) &&
      (!speechModel || speechModel === current.speechModel) && (!voice || voice === current.voice)) return current;
    this.#selecting ??= (async () => {
      const { token } = await this.#grants.catalog();
      const catalog = await availableSpeechModels({ baseURL: this.#speechBaseURL, token, fetchAPI: this.#fetchAPI });
      const transcription = transcriptionModel ?? current?.transcriptionModel ?? catalog.transcriptionModels[0]?.id;
      const speech = speechModel ? catalog.speechModels.find(model => model.id === speechModel)
        : catalog.speechModels.find(model => model.id === current?.speechModel) ?? catalog.speechModels.find(model => model.voices.length);
      const chosenVoice = voice ?? (speech?.voices.includes(current?.voice) ? current.voice : speech?.voices[0]);
      if (!transcription || !catalog.transcriptionModels.some(model => model.id === transcription) ||
        !speech || !chosenVoice || !speech.voices.includes(chosenVoice)) {
        throw new Error('The selected speech or transcription model or voice is unavailable for this agent');
      }
      const selection = { transcriptionModel: transcription, speechModel: speech.id, voice: chosenVoice };
      const tokens = await this.#grants.audio({ transcriptionModel: selection.transcriptionModel, speechModel: selection.speechModel });
      this.#client = new SpeechClient({ baseURL: this.#speechBaseURL, ...tokens, ...selection, fetchAPI: this.#fetchAPI });
      this.#scheduleRenewal(selection, tokens.expiresAt);
      return selection;
    })().finally(() => { this.#selecting = undefined; });
    this.#selection = await this.#selecting;
    return this.#selection;
  }

  // Grants are short-lived; Cortex reissues them while the session is live.
  #scheduleRenewal(selection, expiresAt) {
    clearTimeout(this.#renewTimer);
    const delay = Math.max(1000, Date.parse(expiresAt) - Date.now() - 60000);
    this.#renewTimer = setTimeout(async () => {
      try {
        const tokens = await this.#grants.audio({ transcriptionModel: selection.transcriptionModel, speechModel: selection.speechModel });
        if (this.#selection !== selection) return;
        this.#client.setTokens(tokens);
        this.#scheduleRenewal(selection, tokens.expiresAt);
      } catch { this.#fail('grant_renewal'); }
    }, delay);
    this.#renewTimer.unref?.();
  }

  async #play(text) {
    const audio = this.#open();
    const client = this.#client;
    if (!client) throw new Error('Speech is unavailable');
    try {
      await playSpeechChunks(text, {
        synthesize: (chunk, signal) => client.synthesize(chunk, signal), decode: bytes => this.#decode(bytes),
        speak: pcm => audio.speak(pcm), signal: this.#controller.signal, sampleRate: SAMPLE_RATE,
        onPlaybackStart: () => { this.#speaking = true; this.#listen?.detector.reset(); },
      });
    } finally { this.#speaking = false; this.#listen?.detector.reset(); }
    await this.#conversation.appendTranscript(text, this.#agentLabel, this.#controller.signal).catch(() => {});
  }

  async listen({ speakerLabel, wakePhrases = [], speakReplies = true, transcriptionModel, speechModel, voice } = {}) {
    const speaker = label(speakerLabel, 'Unknown speaker');
    if (!Array.isArray(wakePhrases) || wakePhrases.length > 8 ||
      wakePhrases.some(value => typeof value !== 'string' || !LABEL.test(value.trim()))) throw new Error('Invalid wake phrases');
    if (typeof speakReplies !== 'boolean') throw new Error('speakReplies must be a boolean');
    await this.#select({ transcriptionModel, speechModel, voice });
    const phrases = [this.#agentLabel, ...wakePhrases.map(value => value.trim())];
    if (this.#listen) {
      Object.assign(this.#listen, { speakerLabel: speaker, phrases, speakReplies });
      return this.state;
    }
    this.#open();
    const controller = new AbortController();
    const signal = AbortSignal.any([controller.signal, this.#controller.signal]);
    const listen = { controller, speakerLabel: speaker, phrases, speakReplies };
    const fail = stage => { if (this.#listen === listen) this.#fail(stage); };
    const replies = new ReplyInbox({ agentID: this.#agentID, signal,
      read: (cursor, readSignal) => this.#conversation.request(`messages?afterSequence=${cursor}`, 'GET', undefined, readSignal),
      deliver: reply => listen.speakReplies ? this.#playback.enqueue(reply) : undefined,
      onError: stage => fail(stage) });
    const transcripts = new TranscriptQueue({ signal,
      transcribe: pcm => timedVoiceStage('transcription', () => this.#client.transcribe(pcm, signal)),
      postUtterance: async text => {
        await this.#conversation.appendTranscript(text, listen.speakerLabel, signal);
        if (!isAddressed(text, listen.phrases)) return null;
        return this.#conversation.postUtterance(text, signal, listen.speakerLabel);
      },
      reply: utterance => replies.register(utterance),
      onError: stage => fail(stage) });
    listen.detector = new UtteranceDetector(pcm => transcripts.enqueue(pcm));
    this.#listen = listen;
    try { await this.#conversation.attach(signal); }
    catch {
      this.stopListening();
      throw new Error('This browser session cannot post to its conversation');
    }
    return this.state;
  }

  stopListening() {
    const listen = this.#listen;
    this.#listen = undefined;
    listen?.controller.abort();
    return this.state;
  }

  #fail(stage) {
    if (!this.#listen) return;
    this.stopListening();
    console.warn(JSON.stringify({ event: 'browser_voice_pipeline_failed', stage }));
    void this.#conversation.postStatus(voiceFailureMessage(stage), this.#controller.signal).catch(() => {});
  }

  // Explicit agent speech. Resolves only after playback into the microphone.
  async speak(text, { speechModel, voice } = {}) {
    if (typeof text !== 'string' || !text.trim() || text.length > 3000) throw new Error('Speech requires 1-3000 characters');
    await this.#select({ speechModel, voice });
    this.#open();
    await this.#playback.enqueue(text.trim(), Date.now() + 120000);
    return { delivery: 'played' };
  }

  close() {
    this.stopListening();
    clearTimeout(this.#renewTimer);
    this.#controller.abort();
    this.#audio?.close();
    this.#audio = undefined;
    this.#client = undefined;
  }
}
