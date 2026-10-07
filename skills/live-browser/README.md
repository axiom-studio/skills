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
| `live-browser-start {url?, intent?, durationMinutes?}` | read | Registers a Cortex browser session and returns `sessionId`. Declares `host:browser:session`, `host:browser:profile` and `host:browser:audio`: the session's registration origin carries the profile and audio permissions for its lifetime. Same run: reused. New run in the same conversation: replaces the previous browser. |
| `live-browser-navigate {sessionId, url, intent?}` | read | |
| `live-browser-snapshot {sessionId, includeScreenshot?, intent?}` | read | Text plus elements with generation-scoped refs `sN:eM` (same shape as the old `camoufox-snapshot`). |
| `live-browser-click {sessionId, target \| generation+x+y, intent}` | write | |
| `live-browser-fill {sessionId, target, value, intent}` | write | Works on inputs, textareas and contenteditable rich-text editors (click to focus, then type). Password, payment and identity fields are not filled; the model must hand off. |
| `live-browser-select {sessionId, target, value, intent}` | write | |
| `live-browser-scroll {sessionId, dx?, dy?}` | read | |
| `live-browser-screenshot {sessionId, fullPage?}` | read | |
| `live-browser-request-handoff {sessionId, reason, summary}` | read | `reason`: payment, submit, login, personal_data, destructive, captcha, other. Returns `requiresHuman: true` with `challenges: ["manual_confirmation"]`. |
| `live-browser-close {sessionId}` | read | Saves shared sign-ins and revokes the session. |
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
  conversation/transcript posts, speech grants and the shared profile.
  `AXIOM_SPEECH_API_URL` (default
  `http://axiomcloud.axiomcd.svc.cluster.local/rest/v1/llm-gateway/v1/`) for
  transcription and speech. `LIVE_BROWSER_MAX_SESSIONS` (default 4).
- Stateless: no volume. Shared sign-ins are loaded on start and merged back on
  hand-back, close and every five minutes. Only first-party state is saved:
  cookies and localStorage of sites (eTLD+1) the browser navigated to at top
  level in that session. Third-party (ad and tracking) cookies are never
  saved, and stored entries for sites outside Cortex's first-party site list
  (left from before this rule) are tombstoned on the next save.

Never log page content, typed values, cookies, storage, tokens or audio.

## Development

The library is installed with `install-links=true` (a copy, so its own
dependencies resolve here). After editing `skills/_lib/live-browser`, run
`npm install` again in this directory.

```bash
npm --prefix skills/_lib/live-browser ci && npm --prefix skills/_lib/live-browser test
npm --prefix skills/live-browser ci && npm --prefix skills/live-browser test
docker build -f skills/live-browser/Dockerfile --build-arg SKILL_NAME=live-browser -t axiomstudio/skill-live-browser:1.0.5 .
```
