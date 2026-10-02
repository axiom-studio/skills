import { spawn } from 'node:child_process';
import { once } from 'node:events';

export const SAMPLE_RATE = 16000;

export function audioCommands({ captureSink = 'axiom_meet_capture', microphoneSink = 'axiom_bot_microphone' } = {}) {
  for (const name of [captureSink, microphoneSink]) {
    if (!/^[a-z][a-z0-9_]{0,62}$/.test(name)) throw new Error('invalid PulseAudio sink name');
  }
  return {
    capture: ['parec', ['--device', `${captureSink}.monitor`, '--latency-msec=20', '--format=s16le', `--rate=${SAMPLE_RATE}`, '--channels=1', '--raw']],
    playback: ['pacat', ['--device', microphoneSink, '--latency-msec=20', '--format=s16le', `--rate=${SAMPLE_RATE}`, '--channels=1', '--raw']],
    chromeEnv: { PULSE_SINK: captureSink, PULSE_SOURCE: 'axiom_bot_source' },
  };
}

export function openAudio(commands = audioCommands(), spawnProcess = spawn) {
  const capture = spawnProcess(...commands.capture, { stdio: ['ignore', 'pipe', 'pipe'] });
  const playback = spawnProcess(...commands.playback, { stdio: ['pipe', 'ignore', 'pipe'] });
  let closed = false;
  const close = () => {
    if (closed) return;
    closed = true;
    capture.kill('SIGTERM');
    playback.stdin.end();
    playback.kill('SIGTERM');
  };
  capture.once('error', close);
  playback.once('error', close);
  capture.once('exit', close);
  playback.once('exit', close);
  return {
    input: capture.stdout,
    async speak(pcm) {
      if (closed) throw new Error('meeting audio is closed');
      if (!Buffer.isBuffer(pcm) || pcm.length % 2 !== 0) throw new Error('speech must be 16-bit PCM');
      if (!playback.stdin.write(pcm)) await once(playback.stdin, 'drain');
    },
    close,
  };
}
