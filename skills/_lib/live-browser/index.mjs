// Shared live-browser runtime: Camoufox on a private Xvfb display, a human
// take-over/hand-back lease, view-only and lease video, RFB desktop, host
// authorization, Cortex browser-session registration, and page audio.
export { launchCamoufox, camoufoxBrowser } from './camoufox-browser.mjs';
export { createBrowserDisplay, openBrowserVideo, BROWSER_VIDEO_MIME, BROWSER_VIDEO_SIZE } from './browser-video.mjs';
export { createBrowserDesktop } from './browser-desktop.mjs';
export { openBrowserRFB } from './browser-rfb.mjs';
export { BrowserControl } from './browser-control.mjs';
export { BrowserHandoff, BrowserPausedError } from './browser-handoff.mjs';
export { BrowserDesktopInput } from './browser-desktop-input.mjs';
export { BrowserHumanView } from './browser-human-view.mjs';
export { browserIntervention, detectBrowserIntervention, BROWSER_HANDOFF_REASONS } from './browser-intervention.mjs';
export { browserAuthorizer } from './browser-authorizer.mjs';
export { browserVideoRPC } from './browser-video-rpc.mjs';
export { browserHandlers, addBrowserControlService, loadBrowserControlService, grpc, protoLoader } from './browser-grpc.mjs';
export { BrowserSessionAPI, hostInvocation } from './browser-session.mjs';
export { BrowserAudio } from './browser-audio.mjs';
export { audioCommands, createAudioRoute, openAudio, SAMPLE_RATE } from './audio.mjs';
export { CortexConversation, UtteranceDetector, pcmWav, speechChunks, decodeSpeech } from './bridge.mjs';
export { availableSpeechModels, SpeechClient } from './speech-gateway.mjs';
export { isAddressed, attributeSpeaker } from './attention.mjs';
export { ReplyInbox } from './reply-inbox.mjs';
export { TranscriptQueue } from './transcript-queue.mjs';
export { SpeechPlayback, handleSpeak } from './speech-playback.mjs';
export { playSpeechChunks, playSpeechStream, timedVoiceStage } from './voice-latency.mjs';
export { realtimeFailureStage, realtimeProviderFailureStage, voiceFailureMessage } from './voice-failure.mjs';
export { BrowserProfileStore, PROFILE_SAVE_INTERVAL_MS, profileChanges, profileFromCortex, profileFromStorageState } from './browser-profile.mjs';
