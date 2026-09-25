import assert from 'node:assert/strict';
import { test } from 'node:test';
import { audioCommands, SAMPLE_RATE } from './audio.mjs';

test('separates meeting capture from bot microphone output', () => {
  const commands = audioCommands();
  assert.equal(SAMPLE_RATE, 16000);
  assert.ok(commands.capture[1].includes('axiom_meet_capture.monitor'));
  assert.ok(commands.playback[1].includes('axiom_bot_microphone'));
  assert.ok(commands.capture[1].includes('--latency-msec=20'));
  assert.ok(commands.playback[1].includes('--latency-msec=20'));
  assert.equal(commands.chromeEnv.PULSE_SOURCE, 'axiom_bot_source');
  assert.notEqual(commands.chromeEnv.PULSE_SINK, commands.chromeEnv.PULSE_SOURCE);
});

test('rejects sink names that could alter process arguments', () => {
  assert.throws(() => audioCommands({ captureSink: '--device=secret' }));
});
