import { joinMeeting, meetingURL } from './meet.mjs';
import { audioCommands, openAudio, SAMPLE_RATE } from './audio.mjs';
import { CortexConversation, decodeSpeech, ParentSpeechClient, SpeechClient, speechChunks, UtteranceDetector } from './bridge.mjs';
import { setTimeout as delay } from 'node:timers/promises';

async function main() {
  const config = {
    meetURL: meetingURL(process.env.MEET_URL),
    expiresAt: process.env.MEET_SESSION_EXPIRES_AT,
    profileDir: process.env.GOOGLE_PROFILE_DIR,
    displayName: process.env.MEET_DISPLAY_NAME || 'Axiom Agent',
    executablePath: process.env.CHROMIUM_PATH,
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
  if (!config.profileDir || !config.executablePath) {
    throw new Error('the bot browser profile and Chromium executable are required');
  }
  const conversation = new CortexConversation(config.cortex);
  const speech = process.env.MEET_SPEECH_PROVIDER === 'elevenlabs'
    ? new ParentSpeechClient({ processRef: process }) : new SpeechClient(config.speech);
  process.on('message', message => {
    if (message?.type === 'grant' && typeof message.grant === 'string') conversation.setGrant(message.grant);
    if (message?.type === 'speech-grants') {
      try { speech.setTokens(message); } catch { controller.abort(); }
    }
  });
  const commands = audioCommands();
  Object.assign(process.env, commands.chromeEnv);

  const controller = new AbortController();
  let meeting;
  let audio;
  let presenceCheck;
  let speaking = false;
  let busy = false;
  let queued = Promise.resolve();
  async function playText(text) {
    speaking = true;
    detector.reset();
    try {
      for (const chunk of speechChunks(text)) {
        const encoded = await speech.synthesize(chunk, controller.signal);
        const output = await decodeSpeech(encoded);
        await audio.speak(output);
        await delay(Math.ceil(output.length / (SAMPLE_RATE * 2) * 1000) + 500, undefined, { signal: controller.signal });
      }
    } finally {
      speaking = false;
      detector.reset();
    }
  }
  const detector = new UtteranceDetector(pcm => {
    if (speaking || busy) return;
    busy = true;
    queued = queued.then(async () => {
      try {
        const text = await speech.transcribe(pcm, controller.signal);
        if (!text) return;
        const utterance = await conversation.postUtterance(text, controller.signal);
        const reply = await conversation.waitForReply(utterance.id, 90000, controller.signal);
        if (!reply || controller.signal.aborted) return;
        try {
          await playText(reply);
        } catch (error) {
          await conversation.postStatus('I could not deliver my last reply aloud in the meeting.', controller.signal)
            .catch(() => {});
          throw error;
        }
      } finally {
        speaking = false;
        busy = false;
        detector.reset();
      }
    }).catch(error => { console.error('voice turn failed:', error.name); busy = false; });
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
    meeting = await joinMeeting({ url: config.meetURL, profileDir: config.profileDir, executablePath: config.executablePath,
      displayName: config.displayName, signal: controller.signal,
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
    audio = openAudio(commands);
    audio.input.once('end', stop);
    audio.input.once('error', stop);
    try {
      await conversation.postStatus('I joined the meeting and am listening. Ask me to leave in this Seal Chat when you are done.', controller.signal);
    } catch (error) {
      console.error('Meet status update failed:', error.name);
    }
    audio.input.on('data', chunk => { if (!speaking && !busy) detector.feed(chunk); });
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
        await conversation.postStatus('The meeting voice session failed. Check the meeting link, host admission, and audio setup before starting it again.');
      } catch (statusError) {
        console.error('Meet failure update failed:', statusError.name);
      }
      throw error;
    }
  } finally {
    controller.abort();
    clearTimeout(expiryTimer);
    if (presenceCheck) clearInterval(presenceCheck);
    audio?.close();
    if (meeting) await meeting.leave();
  }
}

main().catch(error => { console.error('Meet voice worker failed:', error.name); process.exitCode = 1; });
