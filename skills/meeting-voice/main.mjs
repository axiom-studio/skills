import { joinMeeting, meetingURL, MeetingJoinError } from './meet.mjs';
import { runWorker } from './worker-lifecycle.mjs';
import { BrowserHandoff } from './browser-handoff.mjs';
import { openBrowserVideo } from './browser-video.mjs';
import { openBrowserRFB } from './browser-rfb.mjs';
import { createBrowserDesktop } from './browser-desktop.mjs';
import { BrowserVideoIPC } from './browser-video-ipc.mjs';
import { BrowserDesktopInput } from './browser-desktop-input.mjs';
import { TranscriptQueue } from './transcript-queue.mjs';
import { ReplyInbox } from './reply-inbox.mjs';
import { RealtimeCapture } from './realtime-transcription.mjs';
import { SpeechPlayback, handleSpeak } from './speech-playback.mjs';
import { playSpeechChunks, playSpeechStream, timedVoiceStage } from './voice-latency.mjs';
import { audioCommands, openAudio, SAMPLE_RATE } from './audio.mjs';
import { CortexConversation, decodeSpeech, ParentSpeechClient, SpeechClient, UtteranceDetector } from './bridge.mjs';

async function main() {
  const config = {
    meetURL: meetingURL(process.env.MEET_URL),
    expiresAt: process.env.MEET_SESSION_EXPIRES_AT,
    profileDir: process.env.GOOGLE_PROFILE_DIR,
    displayName: process.env.MEET_DISPLAY_NAME || 'Axiom Agent',
    cortex: {
      baseURL: process.env.CORTEX_MEET_SESSION_API_URL,
      grant: process.env.CORTEX_MEET_GRANT,
      tenantID: process.env.CORTEX_TENANT_ID,
      agentID: process.env.CORTEX_AGENT_ID,
      conversationID: process.env.CORTEX_CONVERSATION_ID,
      sessionID: process.env.MEET_SESSION_ID,
    },
    speech: {
      baseURL: process.env.AXIOM_SPEECH_API_URL,
      transcriptionToken: process.env.AXIOM_TRANSCRIPTION_TOKEN,
      speechToken: process.env.AXIOM_SPEECH_TOKEN,
      transcriptionModel: process.env.AXIOM_TRANSCRIPTION_MODEL,
      speechModel: process.env.AXIOM_SPEECH_MODEL,
      voice: process.env.AXIOM_SPEECH_VOICE,
    },
  };
  if (!config.profileDir) {
    throw new Error('the bot browser profile is required');
  }
  const conversation = new CortexConversation(config.cortex);
  const controller = new AbortController();
  let display;
  const handoff = process.env.MEET_BROWSER_HANDOFF_ENABLED === 'true' ? new BrowserHandoff({
    tenantID: config.cortex.tenantID, agentID: config.cortex.agentID,
    videoFactory: ({ signal }) => openBrowserVideo({ display: display.display, signal }),
    desktopFactory: ({ signal }) => openBrowserRFB({ display: display.display, signal }),
    inputFactory: ({ control, signal }) => new BrowserDesktopInput({ display: display.display, control, signal }),
    onState: async (status, intervention) => {
      process.send?.({ status, intervention });
      if (status === 'awaiting_user') {
        await conversation.postStatus(`${intervention.summary} Select “Take control” to continue privately, then “Return control” when you are finished.`, controller.signal);
      }
    },
  }) : null;
  const video = handoff ? new BrowserVideoIPC({ handoff, processRef: process, signal: controller.signal }) : null;
  const speech = process.env.MEET_SPEECH_PROVIDER === 'elevenlabs'
    ? new ParentSpeechClient({ processRef: process }) : new SpeechClient(config.speech);
  process.on('message', message => {
    video?.handle(message);
    if (message?.type === 'grant' && typeof message.grant === 'string') conversation.setGrant(message.grant);
    if (message?.type === 'speech-grants') {
      try { speech.setTokens(message); } catch { controller.abort(); }
    }
    // This IPC channel is private to the parent. The parent must authenticate
    // every request and derive the principal before forwarding it here.
    if (message?.type === 'browser-control' && handoff && typeof message.id === 'string') {
      void handoff.handle(message.principal, message.command).then(result => {
        if (result.type === 'frame') result = { ...result, bytes: result.bytes.toString('base64') };
        if (process.connected) process.send?.({ type: 'browser-result', id: message.id, result });
      }, () => {
        if (process.connected) process.send?.({ type: 'browser-result', id: message.id, error: true });
      });
    }
  });
  const commands = audioCommands();
  Object.assign(process.env, commands.browserEnv);
  const speechChunkCharacters = Number(process.env.AXIOM_SPEECH_CHUNK_CHARACTERS) || 3000;

  let meeting;
  let audio;
  let presenceCheck;
  let speaking = false;
  async function playAudioText(text) {
    try {
      const onPlaybackStart = () => { speaking = true; detector.reset(); };
      if (typeof speech.synthesizeStream === 'function') {
        await playSpeechStream(text, {
          synthesizeStream: (chunk, signal) => speech.synthesizeStream(chunk, signal),
          speak: pcm => audio.speak(pcm), signal: controller.signal,
          maximum: speechChunkCharacters, onPlaybackStart,
        });
      } else await playSpeechChunks(text, {
        synthesize: (chunk, signal) => speech.synthesize(chunk, signal), decode: decodeSpeech,
        speak: pcm => audio.speak(pcm), signal: controller.signal,
        maximum: speechChunkCharacters, sampleRate: SAMPLE_RATE,
        onPlaybackStart,
      });
    } finally {
      speaking = false;
      detector.reset();
    }
  }
  const playback = new SpeechPlayback(playAudioText, controller.signal);
  const playText = text => playback.enqueue(text);
  process.on('message', message => {
    void handleSpeak(message, playback, () => Boolean(meeting && audio && !controller.signal.aborted),
      result => { if (process.connected) process.send?.(result); });
  });
  const replyInbox = new ReplyInbox({
    agentID: config.cortex.agentID, signal: controller.signal,
    read: (cursor, signal) => conversation.request(`messages?afterSequence=${cursor}`, 'GET', undefined, signal),
    deliver: async reply => {
      try { await playText(reply); }
      catch (error) {
        await conversation.postStatus('I could not deliver my last reply aloud in the meeting.', controller.signal).catch(() => {});
        throw error;
      }
    },
    onError: stage => transcripts.fail(stage),
  });
  const transcripts = new TranscriptQueue({
    signal: controller.signal,
    transcribe: pcm => timedVoiceStage('transcription', () => speech.transcribe(pcm, controller.signal)),
    postUtterance: text => timedVoiceStage('transcript_post', () => conversation.postUtterance(text, controller.signal)),
    reply: utterance => replyInbox.register(utterance),
    onError: async stage => {
      console.warn(JSON.stringify({ event: 'meeting_voice_pipeline_failed', stage }));
      try {
        await conversation.postStatus('I stopped because the audio processing pipeline could not keep up or failed. The transcript may be incomplete.', controller.signal);
      } finally { controller.abort(); }
    },
  });
  const detector = new UtteranceDetector(pcm => transcripts.enqueue(pcm));
  let realtimeCapture;
  process.on('message', message => {
    if (message?.type !== 'speech-transcript' || !realtimeCapture) return;
    if (message.error) transcripts.fail('realtime_transcription');
    else transcripts.enqueueText(message.text);
  });

  const stop = () => controller.abort();
  const expiresIn = Date.parse(config.expiresAt) - Date.now();
  if (!Number.isFinite(expiresIn) || expiresIn <= 0) throw new Error('meeting session has expired');
  const expiryTimer = setTimeout(stop, expiresIn);
  expiryTimer.unref();
  // The parent owns renewal and revocation. A child orphaned by a parent
  // crash must leave the call immediately, before its bridge grant expires.
  process.once('disconnect', stop);
  process.once('SIGTERM', stop);
  process.once('SIGINT', stop);
  try {
    await conversation.attach(controller.signal);
    if (handoff) display = await createBrowserDesktop({ signal: controller.signal });
    meeting = await joinMeeting({ url: config.meetURL, profileDir: config.profileDir, executablePath: config.executablePath,
      displayName: config.displayName, signal: controller.signal, handoff, display: display?.display,
      handoffBeforeJoin: process.env.MEET_BROWSER_HANDOFF_REQUESTED === 'true',
      onAdmissionRequested: () => {
        process.send?.({ status: 'awaiting_admission' });
        void conversation.postStatus('I requested to join the meeting and am waiting for the host to admit me.',
          controller.signal).catch(error => { console.error('Meet admission update failed:', error.name); });
      } });
    meeting.page.on('close', stop);
    presenceCheck = setInterval(async () => {
      try {
        const present = await meeting.isPresent();
        if (!present) stop();
      } catch { stop(); }
    }, 5000);
    presenceCheck.unref();
    if (process.env.MEET_SPEECH_PROVIDER === 'elevenlabs' && config.speech.transcriptionModel === 'scribe_v2_realtime') {
      realtimeCapture = new RealtimeCapture({ signal: controller.signal,
        send: pcm => speech.request('transcription-audio', { audio: pcm.toString('base64') }, controller.signal),
        onError: stage => transcripts.fail(stage),
      });
      await speech.request('transcription-open', {}, controller.signal);
    }
    audio = openAudio(commands);
    audio.input.once('end', stop);
    audio.input.once('error', stop);
    audio.input.on('data', chunk => {
      if (realtimeCapture) realtimeCapture.feed(speaking ? Buffer.alloc(chunk.length) : chunk);
      else if (!speaking) detector.feed(chunk);
    });
    try {
      await conversation.postStatus('I joined the meeting and am listening. Ask me to leave in this Seal Chat when you are done.', controller.signal);
    } catch (error) {
      console.error('Meet status update failed:', error.name);
    }
    process.send?.({ status: 'active' });
    const greeting = 'Hello, I am the Axiom voice assistant. I am listening and can respond to requests.';
    await playText(greeting);
    await conversation.postStatus(`Bot (spoken): ${greeting}`, controller.signal).catch(() => {});
    if (!controller.signal.aborted) {
      await new Promise(resolve => controller.signal.addEventListener('abort', resolve, { once: true }));
    }
  } catch (error) {
    if (!controller.signal.aborted) {
      try {
        await conversation.postStatus(error instanceof MeetingJoinError ? error.message
          : 'The meeting voice session failed. Check the meeting link, host admission, and audio setup before starting it again.');
      } catch (statusError) {
        console.error('Meet failure update failed:', statusError.name);
      }
      throw error;
    }
  } finally {
    controller.abort();
    video?.close();
    display?.close();
    clearTimeout(expiryTimer);
    if (presenceCheck) clearInterval(presenceCheck);
    audio?.close();
    if (meeting) await meeting.leave();
  }
}

void runWorker(main);
