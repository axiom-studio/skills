// Run: node skills/meeting-voice/latency-benchmark.mjs
// Provider/network timings are synthetic. Decode timings use real local FFmpeg.
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { playSpeechChunks, playSpeechStream } from './voice-latency.mjs';
import { decodeSpeech, pcmWav, UtteranceDetector } from './bridge.mjs';

const cwd = fileURLToPath(new URL('.', import.meta.url));
const baseline = '2727bb2f';
const source = file => execFileSync('git', ['show', `${baseline}:skills/meeting-voice/${file}`], { cwd, encoding: 'utf8' });
const oldBridge = await import(`data:text/javascript;base64,${Buffer.from(source('bridge.mjs')).toString('base64')}`);
const main = source('main.mjs');
const oldPlayback = main.slice(main.indexOf('  async function playAudioText'), main.indexOf('  const playback ='));
assert.ok(oldPlayback.includes('+ 500'), 'baseline playback contract changed');

class Clock {
  time = 0;
  timers = [];
  now = () => this.time;
  sleep = ms => new Promise(resolve => this.timers.push({ at: this.time + ms, resolve }));
  async run(operation) {
    let done = false, failure;
    operation().then(() => { done = true; }, error => { done = true; failure = error; });
    for (let steps = 0; !done; steps++) {
      assert.ok(steps < 10000, 'virtual benchmark deadlock');
      // Drain promise continuations before advancing the deterministic clock.
      for (let i = 0; i < 100; i++) await Promise.resolve();
      if (done) break;
      this.timers.sort((a, b) => a.at - b.at);
      const next = this.timers.shift();
      assert.ok(next, 'operation blocked outside virtual clock');
      this.time = next.at;
      next.resolve();
    }
    if (failure) throw failure;
  }
}

async function simulate(kind, scenario) {
  const clock = new Clock();
  let firstAudio, requests = 0, chars = 0, playedMs = 0;
  const chunks = [];
  const synthesize = async text => {
    requests++; chars += text.length; chunks.push(text);
    await clock.sleep(scenario.fixedTTS + text.length * scenario.perCharTTS);
    return text;
  };
  const decode = async text => {
    await clock.sleep(25);
    return Buffer.alloc(text.length * 40 * 32); // 40ms speech per character, 16kHz PCM.
  };
  const speak = async pcm => {
    firstAudio ??= clock.now();
    const duration = pcm.length / 32;
    playedMs += duration;
    await clock.sleep(duration * scenario.drainFraction);
  };
  const signal = new AbortController().signal;
  await clock.run(async () => {
    if (kind === 'before') {
      const playback = new Function('speechChunks', 'speech', 'controller', 'decodeSpeech', 'audio', 'delay', 'SAMPLE_RATE', 'detector',
        `let speaking = false; const speechChunkCharacters = 3000; ${oldPlayback}; return playAudioText;`)(
        oldBridge.speechChunks, { synthesize }, { signal }, decode, { speak }, clock.sleep, 16000, { reset() {} });
      await playback(scenario.text);
    } else if (kind === 'streaming') {
      await playSpeechStream(scenario.text, {
        synthesizeStream: async function* (text) {
          requests++; chars += text.length; chunks.push(text);
          // Hypothesis, not provider measurement: fixed TTFB cost, then
          // generation proportional to packet text. Backpressure is retained.
          await clock.sleep(scenario.fixedTTS);
          let remaining = text.length * 40 * 32;
          while (remaining) {
            const bytes = Math.min(6400, remaining);
            await clock.sleep(bytes / (40 * 32) * scenario.perCharTTS);
            yield Buffer.alloc(bytes);
            remaining -= bytes;
          }
        },
        speak, signal, now: clock.now, sleep: clock.sleep, report: () => {},
      });
    } else {
      await playSpeechChunks(scenario.text, { synthesize, decode, speak, signal,
        now: clock.now, sleep: clock.sleep, measure: (_, operation) => operation() });
    }
  });
  assert.equal(chunks.join(' '), scenario.text, 'no dropped/reordered words');
  return { firstAudioMs: firstAudio, finishedMs: clock.now(), requests, submittedCharacters: chars, audioMs: playedMs };
}

const rows = [];
for (const scenario of [
  { name: 'short reply, fast provider', text: 'Hello Vishnu, how are you doing?', fixedTTS: 200, perCharTTS: 2, drainFraction: 0.8 },
  { name: 'long reply, fast provider', text: Array(30).fill('Here is the next step.').join(' '), fixedTTS: 200, perCharTTS: 2, drainFraction: 0.8 },
  { name: 'long reply, slow provider', text: Array(30).fill('Here is the next step.').join(' '), fixedTTS: 12000, perCharTTS: 2, drainFraction: 0.8 },
  { name: 'long reply, immediate audio writes', text: Array(30).fill('Here is the next step.').join(' '), fixedTTS: 200, perCharTTS: 2, drainFraction: 0 },
  { name: 'long reply, slow provider and immediate writes', text: Array(30).fill('Here is the next step.').join(' '), fixedTTS: 12000, perCharTTS: 2, drainFraction: 0 },
]) {
  const before = await simulate('before', scenario), after = await simulate('after', scenario);
  const streaming = await simulate('streaming', scenario);
  assert.ok(after.firstAudioMs <= before.firstAudioMs);
  assert.ok(after.finishedMs <= before.finishedMs, 'buffered fallback must not regress');
  assert.equal(streaming.requests, before.requests, 'streaming must not amplify provider requests');
  rows.push({ scenario: scenario.name, before, bufferedFallback: after, streaming });
}

function endpoint(Detector) {
  let frames = 0, emittedAt;
  const detector = new Detector(() => { emittedAt ??= frames * 20; });
  const voice = Buffer.alloc(640);
  for (let i = 0; i < 640; i += 2) voice.writeInt16LE(1000, i);
  for (; frames < 50;) { frames++; detector.feed(voice); }
  for (; frames < 100 && emittedAt === undefined;) { frames++; detector.feed(Buffer.alloc(640)); }
  return emittedAt - 1000;
}
const vad = { beforeSilenceMs: endpoint(oldBridge.UtteranceDetector), afterSilenceMs: endpoint(UtteranceDetector) };
assert.equal(vad.beforeSilenceMs, 700);
assert.equal(vad.afterSilenceMs, 500);

const decodeResults = [];
for (const seconds of [1, 10]) {
  const wav = pcmWav(Buffer.alloc(32000 * seconds));
  const times = [];
  await decodeSpeech(wav); // Warm up process/filesystem paths.
  for (let i = 0; i < 20; i++) {
    const start = performance.now();
    const pcm = await decodeSpeech(wav);
    times.push(performance.now() - start);
    assert.equal(pcm.length, seconds * 32000);
  }
  times.sort((a, b) => a - b);
  decodeResults.push({ audioSeconds: seconds, samples: times.length, p50Ms: Math.round(times[9]), p95Ms: Math.round(times[18]) });
}
console.log(JSON.stringify({ baseline, simulation: 'Deterministic synthetic TTS/decode/playback delays, not live provider latency', rows, vad,
  realLocalDecode: decodeResults, node: process.version }, null, 2));
