import assert from 'node:assert/strict';
import { test } from 'node:test';
import { audioCommands, createAudioRoute, SAMPLE_RATE } from './audio.mjs';

test('separates page capture from the agent microphone', () => {
  const commands = audioCommands();
  assert.equal(SAMPLE_RATE, 16000);
  assert.ok(commands.capture[1].includes('axiom_page_capture.monitor'));
  assert.ok(commands.playback[1].includes('axiom_agent_microphone'));
  assert.ok(commands.capture[1].includes('--latency-msec=20'));
  assert.ok(commands.playback[1].includes('--latency-msec=20'));
  assert.equal(commands.browserEnv.PULSE_SOURCE, 'axiom_agent_source');
  assert.notEqual(commands.browserEnv.PULSE_SINK, commands.browserEnv.PULSE_SOURCE);
});

test('rejects sink names that could alter process arguments', () => {
  assert.throws(() => audioCommands({ captureSink: '--device=secret' }));
  assert.throws(() => audioCommands({ microphoneSource: 'a b' }));
});

test('each browser gets private PulseAudio devices that are unloaded on close', async () => {
  const calls = [];
  let next = 40;
  const run = async (command, args) => { calls.push([command, ...args]); return { stdout: args[0] === 'load-module' ? `${next++}\n` : '' }; };
  const route = await createAudioRoute('s1', { run });
  assert.equal(route.sink, 'lb_s1_capture');
  assert.equal(route.source, 'lb_s1_source');
  assert.ok(route.commands.capture[1].includes('lb_s1_capture.monitor'));
  assert.ok(route.commands.playback[1].includes('lb_s1_mic'));
  assert.ok(calls.every(call => call[0] === 'pactl'));
  assert.ok(calls[0].includes('norewinds=1'));
  await route.close();
  assert.deepEqual(calls.slice(-3).map(call => call.slice(1)), [['unload-module', '42'], ['unload-module', '41'], ['unload-module', '40']]);
  await assert.rejects(createAudioRoute('../x', { run }), /invalid/);
});

test('a failed device load releases already allocated devices', async () => {
  const calls = [];
  let loads = 0;
  const run = async (command, args) => {
    calls.push(args);
    if (args[0] === 'load-module' && ++loads === 2) throw new Error('pactl stderr must stay private');
    return { stdout: '7' };
  };
  await assert.rejects(createAudioRoute('s2', { run }), /^Error: Browser audio is unavailable$/);
  assert.deepEqual(calls.at(-1), ['unload-module', '7']);
});
