# Meeting voice Skill (in development)

This first-party hosted Skill runs as a long-lived, tenant-scoped service. An
Agent created in Seal Chat can call `meet-models`, `meet-start`, `meet-status`,
and `meet-stop`.
`meet-start` accepts direct Google Meet, Zoom, and Microsoft Teams links. The service resolves the
calling Agent Run to its Seal Chat conversation through Cortex, verifies the
tenant and Agent owner, and keeps the browser and audio process alive after the
chat turn finishes. The bot speaks replies in the meeting; Seal Chat records
the participant utterances and spoken replies as its transcript.
Start and stop additionally verify that the Run's triggering message came
from an active human tenant member. API-token users and meeting transcript
observations cannot control the call.
The worker posts visible joined and failure updates in that Seal Chat without
starting an extra Agent turn. After the browser exits, the service reports the
final outcome through a narrow controller route, since the browser's chat grant
has already been revoked. It speaks only a channel-visible reply from this
Agent that names the particular meeting utterance as its reply target.

Each Agent has a separate persistent Chromium profile under `/profile`. The
service holds an exclusive lock on the profile workspace, and one Agent can
join only one meeting at a time. A credential-free state snapshot on the
retained volume lets Seal Chat report an interrupted call as failed after a
service restart. The next authenticated status or start action revokes the
interrupted durable session and posts its final failed status in the old Seal
Chat; a new start then opens a replacement call. The snapshot contains no
bearer or control grant.
PulseAudio captures the meeting output and
provides a separate virtual microphone for synthesized speech.
The ordinary Browser Skill also retains Agent profiles between Runs, but its
Run usage lease does not keep a participant and audio devices connected for
an entire call. This service owns that continuous browser process and its
meeting-specific session lifetime.
Every session has a selected lifetime of 15 to 480 minutes (240 by default),
and the service stops the worker when that deadline arrives. If Chromium does
not exit after a stop request, the service force stops its worker after ten
seconds so the meeting participant cannot linger indefinitely.
The container audio smoke check sends a short tone through each path and
verifies that it reaches only its intended capture device.

The Skill runtime is not yet a complete product deployment. Publishing the OCI
image, installing the Skill, and
verifying a live meeting remain required. Sentinel
can now attest to a persisted running Meet action and pass that short-lived
invocation through Atlas's secret Skill bindings. The Skill uses it for the
calling chat and initial grant. A separate signed control grant remains in the
Skill parent for renewal, revocation, and final outcome; the browser never
receives it. Sentinel
issues a five-minute grant for one Agent conversation and renews it while the
bounded session is active. The signed control grant stays in the Skill service process;
the Chromium worker receives only the scoped grant. Sentinel also keeps a
durable session record and checks it on every bridge request; stopping the
worker revokes the record immediately across Sentinel replicas. Automatic
meeting rejoin after a restart remains unimplemented.

The `meet-models` and `meet-start` actions bind an `elevenlabs_api` credential
selected from the tenant's Vault. The Skill parent uses that key to list
available voices, transcribe meeting utterances with Scribe v2, and synthesize
replies with the selected ElevenLabs speech model and voice. The browser worker
requests these operations over IPC and never receives the reusable API key.
The older Cloud gateway speech path remains available for existing 0.1.0
installations.

## Service configuration

Atlas supplies the short-lived meeting invocation and the selected ElevenLabs
Vault binding through the hosted gRPC secret bindings channel. The ElevenLabs
key stays in the parent process. Never pass meeting account passwords or
browser cookies in an Agent action or command argument.

| Variable | Purpose |
| --- | --- |
| `GOOGLE_PROFILES_DIR` | Persistent volume root, default `/profile` |
| `CHROMIUM_PATH` | Chromium executable, default `/usr/bin/chromium` |
| `MEET_DISPLAY_NAME` | Visible guest name, default `Axiom Agent` |
| `CORTEX_MEET_API_URL` | Sentinel API ending in `/orchestrator/agent/meet/v1/`; set by Axiom hosting |
| `CORTEX_MEET_INVOCATION` | Short-lived per-action assertion supplied by Atlas as a secret Skill binding |
| `CORTEX_TENANT_ID` | Tenant owning this Skill deployment; set by Axiom hosting |
| `AXIOM_SPEECH_API_URL` | Base URL ending in `/rest/v1/llm-gateway/v1/`; set by Axiom hosting |
| `AXIOM_TRANSCRIPTION_MODEL` | Optional deployment default; otherwise select a model from `meet-models` in Seal Chat |
| `AXIOM_SPEECH_MODEL` | Optional deployment default; otherwise select a model from `meet-models` in Seal Chat |
| `AXIOM_SPEECH_VOICE` | Optional deployment default; otherwise select a voice from `meet-models` in Seal Chat |

For signed-in entry, the relevant meeting account profile must be prepared
through a controlled interactive browser session. A fresh profile can request
guest entry when the host permits it. Zoom browser join depends on the host's
browser join setting, and Teams can require sign-in or lobby admission. The
worker does not automate sign-in or bypass admission. Live meetings are needed
to verify join and two-way audio on each platform. Selectors currently expect
English controls.

## Local checks

From `skills/meeting-voice`:

```bash
npm ci
npm test
```

From the `skills` repository root:

```bash
docker build -f skills/meeting-voice/Dockerfile -t axiomstudio/skill-meeting-voice:0.2.1 .
docker run --rm -e MEET_AUDIO_SMOKE=1 axiomstudio/skill-meeting-voice:0.2.1
docker run --rm -e MEET_BROWSER_SMOKE=1 axiomstudio/skill-meeting-voice:0.2.1
```

The browser smoke serves an offline fake Meet page to the real packaged
Chromium. It checks guest entry controls, microphone access, and profile
retention across browser restarts; it does not join a Google meeting.
