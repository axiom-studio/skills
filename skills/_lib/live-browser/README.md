# @axiom/live-browser

Shared Node runtime for Skills that run a live, watchable browser. It is not a
Skill itself (no manifest or image); Skills depend on it with
`"@axiom/live-browser": "file:../_lib/live-browser"` plus `install-links=true`
in their `.npmrc`, and their Dockerfile copies it to the same relative path
(the Docker build context is the repository root).

| Module | Purpose |
| --- | --- |
| `camoufox-browser.mjs` | Camoufox launch on a private display with per-browser PulseAudio devices. |
| `browser-video.mjs`, `browser-desktop.mjs` | Private Xvfb display (1280x800), ffmpeg x11grab → WebM/VP8, window manager. |
| `browser-rfb.mjs`, `browser-desktop-input.mjs`, `browser-human-view.mjs` | Lease-holder desktop (x11vnc over a Unix socket) and human input. |
| `browser-control.mjs` | Lease/queue: automation vs. human, optional pause-on-expiry. |
| `browser-handoff.mjs` | Status, claim (also while automating), input, resume, cancel, lease video and lease-free `watch`. |
| `browser-intervention.mjs` | Handoff reasons and summaries; DOM detection of codes and challenges. |
| `browser-authorizer.mjs` | Verifies each human command with Cortex (`sessions/{id}/authorize`). |
| `browser-session.mjs` | Cortex browser-session client: register/revoke (`host:browser:session`), audio grants (`host:browser:audio`). |
| `browser-profile.mjs` | Shared sign-in state: structured cookies/localStorage, per-key last-writer-wins merge with tombstones and compare-and-swap. |
| `browser-grpc.mjs`, `browser-video-rpc.mjs`, `browser-control.proto` | `axiom.browser.v1.BrowserControlService`. |
| `browser-audio.mjs` and `audio`, `realtime-transcription`, `transcript-queue`, `attention`, `reply-inbox`, `bridge`, `elevenlabs`, `speech-playback`, `voice-latency`, `voice-failure` | Listen (page audio → transcript → addressed utterances) and speak (ElevenLabs → virtual microphone). |

Never log page content, typed input, cookies, storage values, tokens, keys or
audio. Errors crossing a module boundary are fixed strings.

```bash
npm ci && npm test   # BROWSER_VIDEO_INTEGRATION=1 / AUDIO_INTEGRATION=1 enable native tests
```
