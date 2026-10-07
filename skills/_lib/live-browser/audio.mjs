import { execFile, spawn } from 'node:child_process';
import { once } from 'node:events';
import { promisify } from 'node:util';

export const SAMPLE_RATE = 16000;
const NAME = /^[a-z][a-z0-9_]{0,62}$/;

// One browser's page audio and virtual microphone. The page plays into
// captureSink (we listen on its monitor); the agent speaks into microphoneSink,
// whose monitor is remapped as the page's default input source.
export function audioCommands({ captureSink = 'axiom_page_capture', microphoneSink = 'axiom_agent_microphone',
  microphoneSource = 'axiom_agent_source' } = {}) {
  for (const name of [captureSink, microphoneSink, microphoneSource]) {
    if (!NAME.test(name)) throw new Error('invalid PulseAudio sink name');
  }
  return {
    capture: ['parec', ['--device', `${captureSink}.monitor`, '--latency-msec=20', '--format=s16le', `--rate=${SAMPLE_RATE}`, '--channels=1', '--raw']],
    playback: ['pacat', ['--device', microphoneSink, '--latency-msec=20', '--format=s16le', `--rate=${SAMPLE_RATE}`, '--channels=1', '--raw']],
    browserEnv: { PULSE_SINK: captureSink, PULSE_SOURCE: microphoneSource },
  };
}

// Allocates private PulseAudio devices for one browser session. Null sinks
// otherwise default to a two-second rewind window; these realtime streams never
// seek, so norewinds bounds latency. Device descriptions carry no user data.
export async function createAudioRoute(id, { run = promisify(execFile) } = {}) {
  if (!/^[a-z0-9]{1,24}$/.test(id)) throw new Error('invalid audio route');
  const names = { captureSink: `lb_${id}_capture`, microphoneSink: `lb_${id}_mic`, microphoneSource: `lb_${id}_source` };
  const modules = [];
  const load = async args => {
    const { stdout } = await run('pactl', ['load-module', ...args], { timeout: 5000, maxBuffer: 1024 });
    const index = String(stdout).trim();
    if (!/^[0-9]{1,9}$/.test(index)) throw new Error();
    modules.push(index);
  };
  const close = async () => {
    for (const index of modules.splice(0).reverse()) {
      await run('pactl', ['unload-module', index], { timeout: 5000, maxBuffer: 1024 }).catch(() => {});
    }
  };
  try {
    await load(['module-null-sink', `sink_name=${names.captureSink}`, 'norewinds=1', 'sink_properties=device.description=LiveBrowserPage']);
    await load(['module-null-sink', `sink_name=${names.microphoneSink}`, 'norewinds=1', 'sink_properties=device.description=LiveBrowserAgentMicrophone']);
    await load(['module-remap-source', `master=${names.microphoneSink}.monitor`, `source_name=${names.microphoneSource}`,
      'source_properties=device.description=LiveBrowserAgentSource']);
  } catch { await close(); throw new Error('Browser audio is unavailable'); }
  const commands = audioCommands(names);
  return { ...names, sink: names.captureSink, source: names.microphoneSource, commands, close };
}

export function openAudio(commands = audioCommands(), spawnProcess = spawn) {
  const capture = spawnProcess(...commands.capture, { stdio: ['ignore', 'pipe', 'ignore'] });
  const playback = spawnProcess(...commands.playback, { stdio: ['pipe', 'ignore', 'ignore'] });
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
  playback.stdin.on('error', close);
  return {
    input: capture.stdout,
    async speak(pcm) {
      if (closed) throw new Error('browser audio is closed');
      if (!Buffer.isBuffer(pcm) || pcm.length % 2 !== 0) throw new Error('speech must be 16-bit PCM');
      if (!playback.stdin.write(pcm)) await once(playback.stdin, 'drain');
    },
    close,
  };
}
