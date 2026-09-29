# Meeting voice latency benchmark — 2026-09-30

Comparison: baseline `2727bb2f` versus implementation `d09d082b`.

## Native audio finding and local rollout (0.2.12)

A real PulseAudio test uncovered a delay hidden by the simulated pipeline:
the default null sink starts with a two-second render/rewind window. Setting
`norewinds=1` on the capture and microphone null sinks uses the module's 50ms
window instead. See [PulseAudio 16.1 module source](https://github.com/pulseaudio/pulseaudio/blob/v16.1/src/modules/module-null-sink.c).

Measured from the first immediately available synthetic PCM packet to the first
non-silent sample received through `axiom_bot_source`, including the real
`pacat` → null sink → remapped microphone → `parec` path:

| Configuration | First microphone sample |
|---|---:|
| Default rewind configuration | 1,882ms |
| `norewinds=1`, three independent cold starts | 4ms, 5ms, 4ms |

The test holds the provider iterator open until the microphone receives audio,
proving playback does not await provider completion. A 500ms native-delivery
regression gate is enforced for the optimized configuration. These are local
virtual-microphone measurements, **not** remote participant audibility or
STT/LLM/provider timing. The production-entrypoint smoke also confirmed both
directions deliver audio with zero cross-channel signal.

Run the optional test in the worker image with `AUDIO_INTEGRATION=1`; setting
`AUDIO_BASELINE=1` recreates the old sink configuration. The built 0.2.12 image
passed **163 tests, zero failed/skipped**, with audio and browser integration
enabled. A subsequent mounted-test refinement verified the exact remapped source
as above; production audio configuration was unchanged.

Local k3d rollout: worker Health returns version 0.2.12 and healthy=true, one
available replica; both loaded null-sink modules show `norewinds=1`. The tenant-2
Meet Swift binding converged to 0.2.12, revision 10, with its same five actions.
No active meeting was interrupted and no new meeting was joined. The installed
tenant skill record was backed up before the local catalog version/image update.

## Streaming revision (current worktree)

The revised implementation uses a single ElevenLabs streaming request per
provider-sized text block, requesting `pcm_16000`. PCM passes through bounded,
pull-based private IPC directly to microphone playback. There is no whole-file
decode or per-240-character request. The buffered gateway fallback also respects
the configured provider text limit again, removing the forced-small-chunk
regression. Source: [ElevenLabs streaming API](https://elevenlabs.io/docs/api-reference/text-to-speech/stream).

The current benchmark compares baseline, buffered fallback, and streaming. For
streaming, it assumes the same fixed provider cost before the first packet and
2ms generation per character thereafter, yielding at most 200ms of PCM per
packet. **This is an explicit simulation assumption, not a provider latency
measurement.** The historical table below records the rejected small-chunk
revision, not the current output of the benchmark.

| Scenario | Baseline completion | Buffered fallback | Streaming completion | Streaming first PCM |
|---|---:|---:|---:|---:|
| Short, fast | 3,093ms | 1,669ms | 1,590ms | 210ms |
| Long, fast | 51,711ms | 29,263ms | 27,870ms | 210ms |
| Long, slow | 63,511ms | 41,063ms | 39,670ms | 12,010ms |
| Long, fast, immediate writes | 29,663ms | 29,263ms | 27,870ms | 210ms |
| Long, slow, immediate writes | 41,463ms | 41,063ms | 39,670ms | 12,010ms |

All current scenarios use one provider request and preserve exactly the same
submitted characters/audio duration. The fallback non-regression and streaming
request count are asserted by the executable benchmark. The 12-second slow
provider case deliberately remains slow: local streaming cannot erase upstream
time-to-first-byte.

Additional tests cover early PCM delivery, fragmented PCM sample alignment,
packet bounds, truncated/empty streams, private IPC backpressure, ownership,
provider cancellation before first audio, terminal-session rejection, selected
model/voice preservation, network underruns and write backpressure.

Streaming revision verification: **154 passed, 0 failed, 0 skipped** in the
Node 22 worker container with both browser integration tests enabled and network
disabled. No live provider request or deployment was made in this run.

Run from the repository root:

```sh
node skills/meeting-voice/latency-benchmark.mjs
cd skills/meeting-voice && npm test
```

## Historical small-chunk pipeline simulation

The harness executes the baseline playback function extracted from Git and the
current production helper. Both receive identical synthetic TTS and playback
conditions. These are **not measured ElevenLabs, LLM, or live meeting latencies**.
First audio means the first PCM write, not remote participant audibility.

Assumptions: 40ms audio per character, 25ms decode, TTS fixed cost of 200ms
(fast) or 12,000ms (slow), plus 2ms per character. Backpressure consumes 80%
of audio duration unless immediate writes are specified. Short text is 32
characters; long text is 689. Chunk boundaries remove two spaces from provider
requests in the long fixture; reconstructed words and order are asserted intact.

| Scenario | First PCM before → after | Playback completion before → after | TTS requests |
|---|---:|---:|---:|
| Short, fast provider | 289 → 289ms | 3,093 → 1,669ms | 1 → 1 |
| Long, fast provider | 1,603 → 683ms | 51,711 → 28,463ms | 1 → 3 |
| Long, slow provider | 13,403 → 12,483ms | 63,511 → 46,709ms | 1 → 3 |
| Long, fast provider, immediate writes | 1,603 → 683ms | 29,663 → 28,463ms | 1 → 3 |
| Long, slow provider, immediate writes | 13,403 → 12,483ms | 41,463 → 46,709ms | 1 → 3 |

The final scenario is a **12.7% completion-time regression**. Fixed small chunks
increase request count and can introduce inter-chunk gaps when synthesis takes
longer than playback. The first-audio improvement is not a universal throughput
or cost improvement. This benchmark intentionally reports the adverse case;
there is no release gate asserting that every scenario is faster.

The actual old/new utterance detectors emit a one-second PCM fixture after
700ms/500ms of trailing silence respectively (200ms improvement). This does not
measure transcription accuracy or provider processing time.

## Real local FFmpeg decode

Twenty warm samples per fixture on the host, Node v26.9.0, using real
`decodeSpeech` and in-memory 16kHz mono PCM WAVs:

| Audio duration | p50 | p95 |
|---|---:|---:|
| 1 second | 58ms | 107ms |
| 10 seconds | 52ms | 66ms |

These include process startup and vary with machine load. They do not cover
provider MP3 payloads, pacat delivery, or a remote meeting microphone.

## Test coverage and limits

Host and production Node 22 container: 144 passing, 2 opt-in browser integration
tests skipped in the standard suite. Coverage includes isolated reply cursors,
latest-follow-up retention, bounded prefetch, playback backpressure accounting,
cancellation, and sanitized timing diagnostics.

With `BROWSER_VIDEO_INTEGRATION=1` in the isolated production container:
**146 passed, 0 failed, 0 skipped**, including native browser input and real
VP8 encoding/decoding. The container had networking disabled and used disposable
test profiles, not an active user meeting.

No deployment or external meeting was started for these benchmarks. Live STT
accuracy, TTS continuity, real request overhead/rate limits, and end-to-end
participant-to-bot audibility remain unverified. The previously observed
12–54 second agent-generation delays are outside this pipeline simulation.

## Realtime transcription revision

New ElevenLabs sessions without an explicit transcription selection default to
`scribe_v2_realtime`; an explicit `scribe_v2` still selects the batch path. Both
are exposed in `meet-models`. The key stays in the parent process and is sent as
an authorization header on a fixed-origin WebSocket, never as a child secret or
URL query parameter. Audio is sent in 100ms frames with bounded queues. Only
`committed_transcript` enters the existing canonical chat queue; partial and
timestamp follow-ups do not create duplicate agent turns.

Provider VAD is configured for 500ms of silence. This is a setting, **not a
measured end-to-end guarantee**; the provider documents an initial processing
window and live accuracy/latency must still be tested. Provider errors,
disconnects and queue overflows fail explicitly; no automatic replay/reconnect
can duplicate speech or spend unbounded requests.

The Node 22 container suite passed **162 tests, zero skipped**, including a real
loopback WebSocket PCM/transcript exchange. That test proves transport behavior,
not live ElevenLabs transcription quality. Dependencies are pinned, including
`ws` 8.22.0.

References: [realtime API](https://elevenlabs.io/docs/api-reference/speech-to-text/v-1-speech-to-text-realtime),
[commit events](https://elevenlabs.io/docs/eleven-api/guides/how-to/speech-to-text/realtime/event-reference).

Before claiming near-real-time: measure capture → transcript → agent reply → first audible speech in an
approved live session, with per-stage p50/p95 and interruption/accuracy checks.
