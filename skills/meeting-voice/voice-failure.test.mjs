import test from 'node:test';
import assert from 'node:assert/strict';
import { realtimeFailureStage, voiceFailureMessage } from './voice-failure.mjs';

test('known realtime failure categories survive the worker boundary', () => {
  for (const stage of ['startup_timeout', 'connection', 'connection_closed', 'provider', 'invalid_event', 'backlog', 'delivery']) {
    assert.equal(realtimeFailureStage(stage), `realtime_transcription_${stage}`);
  }
});

test('unknown failure data never enters logs or user-facing status', () => {
  for (const value of [undefined, null, {}, 'secret-key and private transcript', 'connection\nsecret']) {
    assert.equal(realtimeFailureStage(value), 'realtime_transcription');
    assert.equal(voiceFailureMessage(value), 'I stopped because the voice pipeline failed. The transcript may be incomplete.');
  }
});

test('only measured backlog is described as inability to keep up', () => {
  assert.match(voiceFailureMessage(realtimeFailureStage('backlog')), /faster than/);
  assert.match(voiceFailureMessage(realtimeFailureStage('connection_closed')), /connection was lost/);
  assert.match(voiceFailureMessage(realtimeFailureStage('provider')), /provider returned an error/);
  assert.match(voiceFailureMessage(realtimeFailureStage('startup_timeout')), /did not start in time/);
  for (const stage of ['delivery', 'invalid_event', undefined]) {
    assert.doesNotMatch(voiceFailureMessage(realtimeFailureStage(stage)), /faster than|could not keep up/);
  }
});
