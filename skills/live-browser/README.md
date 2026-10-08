# Live browser Skill

`skill-live-browser` is the agent's interactive browser: Camoufox (Firefox)
running headful on a private Xvfb display, streamed live to Seal Chat, with
an always-available **Take control** for the user. It is built on the shared
runtime in [`skills/_lib/live-browser`](../_lib/live-browser/README.md).
For reading, searching and comparing pages use the Lightpanda browser Skill
(`skill-browser`).

## Actions

| Action | Risk | Notes |
| --- | --- | --- |
| `live-browser-start {url?, intent?, durationMinutes?}` | read | Registers a Cortex browser session and returns `sessionId`. Declares `host:browser:session` and `host:browser:audio`: the session's registration origin carries the audio permission for its lifetime. Same run: reused. New run in the same conversation: replaces the previous browser. Another conversation's browser open: waits (up to two minutes) for it to close. |
| `live-browser-navigate {sessionId, url, intent?}` | read | |
| `live-browser-snapshot {sessionId, includeScreenshot?, intent?}` | read | Text plus elements with generation-scoped refs `sN:eM` (same shape as the old `camoufox-snapshot`). |
| `live-browser-click {sessionId, target \| generation+x+y, intent}` | write | |
| `live-browser-fill {sessionId, target, value, intent}` | write | Works on inputs, textareas and contenteditable rich-text editors (click to focus, then type). Password, payment and identity fields are not filled; the model must hand off. |
| `live-browser-select {sessionId, target, value, intent}` | write | |
| `live-browser-scroll {sessionId, dx?, dy?}` | read | |
| `live-browser-screenshot {sessionId, fullPage?}` | read | |
| `live-browser-request-handoff {sessionId, reason, summary}` | read | `reason`: payment, submit, login, personal_data, destructive, captcha, other. Returns `requiresHuman: true` with `challenges: ["manual_confirmation"]`. |
| `live-browser-close {sessionId}` | read | Closes Camoufox (the profile is flushed to the volume), frees the browser for the next task and revokes the session. |
| `live-browser-listen {sessionId, state: on\|off, speakerLabel?, displayName?, wakePhrases?, speakReplies?, transcriptionModel?, speechModel?, voice?}` | write | Axiom speech gateway; models default to the agent's catalog. |
| `live-browser-speak {sessionId, text, speechModel?, voice?}` | write | Axiom speech gateway. |

While a human holds control, model actions wait (bounded) and return
`status: paused_by_user` without acting; if the user is still in control the
result has `requiresHuman: true` so the run waits for hand-back. CAPTCHA,
bot-check and verification-code pages trigger an automatic handoff.

An element the model cannot use (hidden or collapsed, covered by an overlay,
stale reference, not editable, option missing, timed out waiting for it)
returns a successful result `{status: "not_actionable", reason, hint, url,
title, browserStatus}` and changes nothing, so the agent can expand, scroll or
re-snapshot instead of failing the run. Browser faults (closed page or
context, crash) and session or permission failures remain errors.

Joining a video call is an ordinary task described in the Skill instructions
(navigate, type the display name, Ask to join, wait for admission, listen);
there is no site-specific code.

## Host interface

- gRPC `axiom.skill.v1.SkillService` and `axiom.browser.v1.BrowserControlService`
  (Control, Video, Desktop) on `SKILL_PORT` (50051).
- `CORTEX_BROWSER_API_URL` (default
  `http://sentinel.axiomcd.svc.cluster.local/orchestrator/agent/browser/v1/`)
  for session registration, human-command authorization, handoff notices,
  conversation/transcript posts, speech grants and profile-command
  authorization. `AXIOM_SPEECH_API_URL` (default
  `http://axiomcloud.axiomcd.svc.cluster.local/rest/v1/llm-gateway/v1/`) for
  transcription and speech. `CORTEX_TENANT_ID`: the tenant this runtime
  serves (set by Cortex's Skill hosting); profile commands for any other
  tenant are refused.
- `BrowserControlService.Control` also takes two session-less profile
  commands, `{"type":"profileStatus"}` (returns `{state, origins, updatedAt,
  sizeBytes}`) and `{"type":"forgetProfile"}` (stops the open browser cleanly,
  deletes the profile and returns `{deleted}`), each with a single-command
  proof that Cortex verifies at `profile/authorize`.

## Browser profile

The runtime is per tenant and keeps the tenant's one real Camoufox profile on
its persistent volume (`browser-profile`, 10Gi, mounted at
`/var/lib/axiom-live-browser`, override with `LIVE_BROWSER_PROFILE_ROOT`).
The browser is launched with Playwright's persistent context on
`<root>/camoufox`, so everything a normal browser keeps survives between
sessions and pod restarts: first- and third-party cookies, localStorage,
IndexedDB (including non-extractable CryptoKeys, as WhatsApp Web uses), Cache
Storage, service workers, history, permissions, site settings and saved
logins. Nothing is exported, uploaded or merged; there is no central copy.

- One browser per profile. Firefox opens a profile only once, and a live
  browser owns its display (live view and take control) and its page audio,
  so sessions are queued: one live browser at a time, and a start from
  another conversation waits in FIFO order (up to two minutes) for it to
  close. A new start in the same conversation still replaces its browser.
- Durability: closing a browser closes Camoufox, which flushes its SQLite
  files; the profile is released only after Firefox has exited. On SIGTERM
  the runtime closes every browser before exiting (bounded to 45 s, inside
  the pod's termination grace period). The image runs under `tini`, which
  reaps orphaned browser processes.
- Stale `lock`/`.parentlock` files left by a crash or another pod are removed
  before launch (the runtime holds the profile exclusively).
- The Camoufox fingerprint generated on first launch is kept in
  `identity.json` and reused, so sites see the same device every time, until
  the Camoufox build changes.
- Saved sign-ins: status reports `profile: {state, origins}`, where origins
  are the top-level sites (eTLD+1) the browser has visited, kept in
  `sites.json`. Third-party sites whose cookies the browser keeps are not
  listed. Forget sign-ins (`forgetProfile`) deletes `camoufox/`, `sites.json`
  and `identity.json`.

Never log page content, typed values, cookies, storage, tokens or audio.

## Development

The library is installed with `install-links=true` (a copy, so its own
dependencies resolve here). After editing `skills/_lib/live-browser`, run
`npm install` again in this directory.

```bash
npm --prefix skills/_lib/live-browser ci && npm --prefix skills/_lib/live-browser test
npm --prefix skills/live-browser ci && npm --prefix skills/live-browser test
docker build -f skills/live-browser/Dockerfile --build-arg SKILL_NAME=live-browser -t axiomstudio/skill-live-browser:1.0.6 .
```
