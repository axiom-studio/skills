import { setTimeout as delay } from 'node:timers/promises';
import { speechChunks } from './bridge.mjs';

// Metrics contain durations and fixed stage names, never speech or provider bodies.
export async function timedVoiceStage(stage, operation, report = console.info, now = performance.now.bind(performance)) {
  const started = now();
  let outcome = 'ok';
  try { return await operation(); }
  catch (error) { outcome = 'error'; throw error; }
  finally { report(JSON.stringify({ event: 'browser_voice_stage', stage, outcome, durationMs: Math.round(now() - started) })); }
}

export async function playSpeechChunks(text, { synthesize, decode, speak, signal, onPlaybackStart = () => {},
  maximum = 3000, sampleRate = 16000, now = performance.now.bind(performance),
  sleep = ms => delay(ms, undefined, { signal }), measure = timedVoiceStage }) {
  if (signal?.aborted) throw new Error('Speech canceled');
  const chunks = speechChunks(text, maximum);
  const prepare = async chunk => {
    const bytes = await measure('synthesis', () => synthesize(chunk, signal));
    return measure('decode', () => decode(bytes));
  };
  // Attach rejection handlers immediately: a speculative next chunk may fail
  // while the current chunk is still playing. Keep at most one lookahead.
  const settled = chunk => prepare(chunk).then(value => ({ value }), error => ({ error }));
  let pending = chunks.length ? settled(chunks[0]) : undefined;
  for (let i = 0; i < chunks.length; i++) {
    if (signal?.aborted) throw new Error('Speech canceled');
    const result = await pending;
    if (result.error) throw result.error;
    if (signal?.aborted) throw new Error('Speech canceled');
    const pcm = result.value;
    pending = i + 1 < chunks.length ? settled(chunks[i + 1]) : undefined;
    onPlaybackStart();
    await measure('playback', async () => {
      const started = now();
      await speak(pcm);
      // pacat backpressure may already have consumed most of this duration.
      // Do not wait the full duration a second time after drain.
      const remaining = Math.max(0, pcm.length / (sampleRate * 2) * 1000 - (now() - started)) + 100;
      await sleep(remaining);
    });
  }
}

// Keep one provider request open and deliver PCM as it arrives. The playback
// horizon includes time already spent in pacat backpressure and network stalls.
export async function playSpeechStream(text, { synthesizeStream, speak, signal, onPlaybackStart = () => {},
  maximum = 3000, now = performance.now.bind(performance),
  sleep = ms => delay(ms, undefined, { signal }), report = console.info }) {
  let horizon = now(), started = false;
  const requestedAt = now();
  for (const chunk of speechChunks(text, maximum)) {
    if (signal?.aborted) throw new Error('Speech canceled');
    for await (const pcm of synthesizeStream(chunk, signal)) {
      if (signal?.aborted) throw new Error('Speech canceled');
      if (!Buffer.isBuffer(pcm) || !pcm.length || pcm.length > 6400 || pcm.length % 2) {
        throw new Error('Invalid PCM stream');
      }
      if (!started) {
        onPlaybackStart(); started = true;
        report(JSON.stringify({ event: 'browser_voice_stage', stage: 'first_pcm', durationMs: Math.round(now() - requestedAt) }));
      }
      horizon = Math.max(horizon, now()) + pcm.length / 32;
      await speak(pcm);
      // Bound local buffering to 200ms without adding a sleep per packet.
      const ahead = horizon - now() - 200;
      if (ahead > 0) await sleep(ahead);
    }
  }
  if (started) await sleep(Math.max(0, horizon - now()) + 100);
}
