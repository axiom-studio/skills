// Only fixed categories may cross into logs or conversation status. Provider
// error bodies can contain credentials or transcript fragments.
// Provider event types are a fixed protocol enum, never the free-text error.
const providerStages = new Map([
  ['auth_error', 'provider_auth'], ['quota_exceeded', 'provider_quota'],
  ['transcriber_error', 'provider_transcriber'], ['input_error', 'provider_input'],
  ['invalid_request', 'provider_request'], ['error', 'provider'],
  ['commit_throttled', 'provider_rate_limit'], ['rate_limited', 'provider_rate_limit'],
  ['unaccepted_terms', 'provider_terms'], ['queue_overflow', 'provider_backlog'],
  ['resource_exhausted', 'provider_capacity'], ['session_time_limit_exceeded', 'provider_session_limit'],
  ['chunk_size_exceeded', 'provider_chunk_size'], ['insufficient_audio_activity', 'provider_idle'],
]);

export function realtimeProviderFailureStage(eventType) {
  return providerStages.get(eventType) ?? 'provider';
}

const realtimeStages = new Set([
  'startup_timeout', 'connection', 'connection_closed', 'provider',
  'invalid_event', 'backlog', 'delivery',
  ...providerStages.values(),
]);

export function realtimeFailureStage(stage) {
  return realtimeStages.has(stage) ? `realtime_transcription_${stage}` : 'realtime_transcription';
}

export function voiceFailureMessage(stage) {
  if (stage === 'realtime_transcription_provider_idle') {
    return 'I stopped because the transcription provider closed the session for insufficient audio activity. The transcript may be incomplete.';
  }
  if (stage === 'realtime_transcription_provider_session_limit') {
    return 'I stopped because the transcription provider reached its session time limit. The transcript may be incomplete.';
  }
  if (stage === 'realtime_transcription_provider_quota') {
    return 'I stopped because the transcription provider reported that its usage quota was exhausted. The transcript may be incomplete.';
  }
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
