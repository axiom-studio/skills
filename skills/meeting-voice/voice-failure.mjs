// Only fixed categories may cross into logs or conversation status. Provider
// error bodies can contain credentials or transcript fragments.
const realtimeStages = new Set([
  'startup_timeout', 'connection', 'connection_closed', 'provider',
  'invalid_event', 'backlog', 'delivery',
]);

export function realtimeFailureStage(stage) {
  return realtimeStages.has(stage) ? `realtime_transcription_${stage}` : 'realtime_transcription';
}

export function voiceFailureMessage(stage) {
  if (stage === 'capture_backlog' || stage === 'realtime_capture_backlog' || stage === 'realtime_transcription_backlog') {
    return 'I stopped because audio was arriving faster than it could be processed. The transcript may be incomplete.';
  }
  if (stage === 'realtime_transcription_connection' || stage === 'realtime_transcription_connection_closed') {
    return 'I stopped because the live transcription connection was lost. The transcript may be incomplete.';
  }
  if (stage === 'realtime_transcription_provider') {
    return 'I stopped because the live transcription provider returned an error. The transcript may be incomplete.';
  }
  if (stage === 'realtime_transcription_startup_timeout') {
    return 'I stopped because the live transcription connection did not start in time. The transcript may be incomplete.';
  }
  return 'I stopped because the voice pipeline failed. The transcript may be incomplete.';
}
