# @axiom/live-browser

Shared Node runtime for Skills that run a live, watchable browser. It is not a
Skill itself (no manifest or image); Skills depend on it with
`"@axiom/live-browser": "file:../_lib/live-browser"` plus `install-links=true`
in their `.npmrc`, and their Dockerfile copies it to the same relative path
(the Docker build context is the repository root).

| Module | Purpose |
| --- | --- |
| `camoufox-browser.mjs` | Camoufox persistent-context launch on a private display with per-browser PulseAudio devices, site isolation and loopback WebRTC prefs, the slot proxy (`LIVE_BROWSER_SLOT_PROXY`) or the tenant's proxy list; stale profile lock cleanup and a stable per-profile fingerprint. |
| `playwright-guard.mjs` | Keeps the runtime up when playwright-core's Firefox network code throws on a request that finished without a response (a refused TURN-over-TLS channel); every other uncaught error still ends the process. |
| `browser-video.mjs`, `browser-desktop.mjs` | Private Xvfb display (1280x800), ffmpeg x11grab → WebM/VP8, window manager. |
| `browser-rfb.mjs`, `browser-desktop-input.mjs`, `browser-human-view.mjs` | Lease-holder desktop (x11vnc over a Unix socket) and human input. |
| `browser-control.mjs` | Lease/queue: automation vs. human, optional pause-on-expiry. |
| `browser-handoff.mjs` | Status, claim (also while automating), input, resume, cancel, lease video and lease-free `watch`. |
| `browser-intervention.mjs` | Handoff reasons and summaries; DOM detection of codes and challenges. |
| `browser-authorizer.mjs` | Verifies each human command with Cortex (`sessions/{id}/authorize`); session-less profile commands with `profile/authorize`. |
| `browser-session.mjs` | Cortex browser-session client: register/extend/revoke, handoff notice, audio catalog and speech grants. |
| `browser-profile.mjs` | The owner's Camoufox profile in the runtime's slot (`/tmp/profile`, restored and saved by the browser pool): exclusive FIFO lease (one browser per profile), visited top-level sites (eTLD+1) for the saved sign-ins list, status and forget. |
| `browser-grpc.mjs`, `browser-video-rpc.mjs`, `browser-control.proto` | `axiom.browser.v1.BrowserControlService`. |
| `browser-audio.mjs` and `audio`, `speech-gateway`, `transcript-queue`, `attention`, `reply-inbox`, `bridge`, `speech-playback`, `voice-latency`, `voice-failure` | Listen (page audio → gateway transcription → transcript and addressed utterances) and speak (gateway speech → virtual microphone), with session-bound grants from Cortex. |

Never log page content, typed input, cookies, storage values, tokens, keys or
audio. Errors crossing a module boundary are fixed strings.

```bash
npm ci && npm test   # BROWSER_VIDEO_INTEGRATION=1 / AUDIO_INTEGRATION=1 enable native tests
```
