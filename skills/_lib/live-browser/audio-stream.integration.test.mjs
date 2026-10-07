import test from 'node:test';
import assert from 'node:assert/strict';
import { spawn, execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { setTimeout as delay } from 'node:timers/promises';
import { openAudio, audioCommands } from './audio.mjs';
import { playSpeechStream } from './voice-latency.mjs';

const execute = promisify(execFile);
test('streamed PCM reaches a real PulseAudio microphone before provider completion', {
  skip: process.env.AUDIO_INTEGRATION !== '1', timeout: 15000,
}, async () => {
  const directory = await mkdtemp(join(tmpdir(), 'voice-audio-test-'));
  const env = { ...process.env, XDG_RUNTIME_DIR: directory, PULSE_SERVER: `unix:${directory}/pulse/native` };
  const sinkOptions = process.env.AUDIO_BASELINE === '1' ? '' : ' norewinds=1';
  const pulse = spawn('pulseaudio', ['--daemonize=no', '--exit-idle-time=-1',
    `--load=module-null-sink sink_name=axiom_agent_microphone${sinkOptions}`,
    `--load=module-null-sink sink_name=axiom_page_capture${sinkOptions}`,
    '--load=module-remap-source master=axiom_agent_microphone.monitor source_name=axiom_agent_source'], { env, stdio: 'ignore' });
  let audio, monitor, firstSample, release, audioDeadline;
  const audible = new Promise(resolve => { firstSample = resolve; });
  const continueProvider = new Promise(resolve => { release = resolve; });
  const controller = new AbortController();
  try {
    let ready = false;
    for (let attempt = 0; attempt < 100; attempt++) {
      try { await execute('pactl', ['info'], { env, timeout: 1000 }); ready = true; break; }
      catch { await delay(20); }
    }
    assert.equal(ready, true, 'isolated PulseAudio must start');
    audio = openAudio(audioCommands(), (cmd, args, options) => spawn(cmd, args, { ...options, env }));
    monitor = spawn('parec', ['--device=axiom_agent_source', '--latency-msec=20',
      '--format=s16le', '--rate=16000', '--channels=1', '--raw'], { env, stdio: ['ignore', 'pipe', 'ignore'] });
    let carry = Buffer.alloc(0);
    monitor.stdout.on('data', bytes => {
      const pcm = Buffer.concat([carry, bytes]);
      for (let i = 0; i + 1 < pcm.length; i += 2) {
        if (Math.abs(pcm.readInt16LE(i)) > 1000) { firstSample(performance.now()); break; }
      }
      carry = pcm.subarray(pcm.length - pcm.length % 2);
    });
    await delay(100); // Allow native capture to subscribe before the fixture.
    const tone = Buffer.alloc(6400);
    for (let i = 0; i < 3200; i++) tone.writeInt16LE(Math.round(Math.sin(i * 2 * Math.PI * 440 / 16000) * 6000), i * 2);
    let completed = false;
    const started = performance.now();
    const playback = playSpeechStream('synthetic audio fixture', {
      signal: controller.signal,
      synthesizeStream: async function* () {
        yield tone;
        await continueProvider;
        yield tone;
        completed = true;
      },
      speak: pcm => audio.speak(pcm), report: () => {},
    });
    playback.catch(() => {});
    const heardAt = await Promise.race([audible, new Promise((_, reject) => {
      audioDeadline = setTimeout(() => reject(new Error('No native microphone PCM received')), 5000);
    })]);
    clearTimeout(audioDeadline);
    assert.equal(completed, false, 'microphone must receive audio while provider is still blocked');
    console.info(JSON.stringify({ event: 'native_audio_benchmark', firstMicrophoneSampleMs: Math.round(heardAt - started) }));
    if (process.env.AUDIO_BASELINE !== '1') assert.ok(heardAt - started < 500, 'native first audio exceeds 500ms budget');
    release();
    await playback;
  } finally {
    clearTimeout(audioDeadline);
    release(); controller.abort(); audio?.close(); monitor?.kill('SIGTERM'); pulse.kill('SIGTERM');
    await rm(directory, { recursive: true, force: true });
  }
});
