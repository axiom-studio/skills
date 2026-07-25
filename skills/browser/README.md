# Browser Skill

Generic, governed Chromium automation for OpenSeal and Atlas agents. The skill
executes deterministic browser operations; planning and interpretation remain
outside the skill.

The Reddit API skill remains unchanged and is preferred when Reddit's API
supports the operation. This browser skill is a fallback for UI-only workflows.
It contains no Reddit-specific code.

## Public actions

| Action | Classification | Purpose |
| --- | --- | --- |
| `browser-health` | read | Report engine, workspace, proxy, and cleanup readiness. |
| `browser-open` | read | Open an exact validated HTTP(S) URL in an isolated session. |
| `browser-snapshot` | read | Return a bounded accessibility tree and generation-scoped references. |
| `browser-read` | read | Return bounded rendered body text. |
| `browser-click` | external | Click an exact current snapshot reference. |
| `browser-fill` | external | Clear and fill an exact current reference, including safe credential filling. |
| `browser-type` | external | Type non-secret literal text into an exact current reference. |
| `browser-select` | external | Select an exact non-secret option. |
| `browser-wait` | read | Wait up to 30 seconds for load, exact text, or a duration. |
| `browser-screenshot` | read | Return a bounded PNG when the session is not secret-tainted. |
| `browser-session-status` | read | Return bounded non-secret session state. |
| `browser-close` | internal write | Explicitly stop the browser and preserve only configured profile state. |

Every action except `browser-health` requires `sessionId`. Names match
`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`; slashes, dot segments, whitespace, and
absolute paths are rejected. An optional `profileName` persists authenticated
browser state under the configured workspace. A profile can have only one
active session at a time.

Snapshot references have the form `s<generation>:e<element>`. Interaction
actions accept only a reference from the latest snapshot. Navigation or any
mutation invalidates existing references and requires a new snapshot.

All four interaction actions require:

- an exact snapshot `target`;
- a concrete `intent`;
- `writeAuthorized: true`; and
- a caller-provided `idempotencyKey`.

Successful action receipts are stored in the session workspace. Repeating the
same key and arguments returns the original receipt with `duplicate: true`;
reusing a key with different arguments fails. Interaction actions have one
attempt and are never automatically retried.

## Credentials

Do not put passwords, cookies, tokens, authorization headers, or other secrets
in `value`. Bind an OpenSeal credential of kind `browser-secret` and call
`browser-fill` with only its non-secret `credentialField` name. The runtime
resolves the binding out of band. The skill sends entered text to
`agent-browser` as JSON over stdin, never as a process argument, and never
returns it.

Controls with secret-like accessible names reject literal values. Returned
text, errors, and snapshots are scrubbed against resolved binding values;
password-like element values are omitted. Screenshots are disabled after a
credential fill for the rest of that page session because arbitrary pixels
cannot be proven free of reflected secrets. Cookie jars and Web Storage are
never public actions or outputs.

## Network policy

Only `http` and `https` are accepted. URL credentials are rejected. By default,
only ports 80 and 443 are allowed, and every dial rejects loopback, private,
link-local, multicast, unspecified, carrier-grade NAT, documentation/reserved,
Kubernetes service, and common cloud metadata destinations.

All Chromium traffic passes through a loopback proxy owned by the skill. The
proxy resolves each requested host and dials the validated IP directly. It
therefore revalidates redirects, subresources, and the address actually used
for each connection, preventing DNS rebinding between policy validation and
network I/O. `BROWSER_ALLOWED_HOSTS` optionally narrows destinations to
comma-separated exact hosts or `*.example.com` suffixes.

`BROWSER_ALLOWED_PORTS` may add explicitly trusted ports. Setting
`BROWSER_ALLOW_PRIVATE_NETWORKS=true` is a deployment-level escape hatch for a
trusted private browser environment; it is intentionally unavailable as an
action input. Treat that setting as privileged network policy.

Callers cannot supply browser executable paths, proxy settings, headers,
cookies, extensions, JavaScript, CDP commands, launch flags, shell commands, or
arbitrary CLI arguments.

## Challenges and human checkpoints

Snapshots and reads identify likely login, consent, CAPTCHA, MFA, and anti-bot
screens. CAPTCHA, MFA, and anti-bot detection marks `requiresHuman: true` and
blocks interaction actions. The skill never attempts to evade challenges,
access controls, source rules, or rate limits. Consent controls still require
an exact target, intent, authorization, and idempotency key.

## Reddit fallback flow

For an explicitly approved UI fallback:

1. Open `https://www.reddit.com/r/vibecoding/new` with a named authenticated
   profile and a hostname allowlist covering Reddit.
2. Snapshot or read the listing, then click an exact thread reference.
3. Snapshot/read the thread and comments.
4. Snapshot the comment form and call `browser-fill` with the exact approved
   comment and a unique idempotency key.
5. Take a new snapshot, click the exact submit reference once with a different
   idempotency key, and wait for the exact comment text.
6. Read or snapshot again and verify exact equality before reporting success.

Never test write behavior against live Reddit. The test suite uses a local form
fixture and verifies one submission.

## Runtime and packaging

The custom Dockerfile is automatically selected by the repository Makefile. It
pins `agent-browser` 0.27.2 and the Playwright 1.61.0 Noble runtime image, which
contains Chromium and required libraries. Browser downloads happen at image
build time, not per action. The service runs as `pwuser` under `tini`.

Mount a persistent volume at `BROWSER_WORKSPACE` (default
`/var/lib/openseal-browser`). All session metadata, daemon files, screenshots
during capture, cache, and named profiles remain beneath that directory with
owner-only permissions. Idle sessions close after `BROWSER_IDLE_TIMEOUT`
(default `15m`); explicit close is preferred.

Recommended baseline:

- 1 CPU, with 2 CPUs for concurrent or script-heavy pages;
- 1 GiB memory minimum and 2 GiB recommended;
- a writable persistent volume sized for named profiles;
- container init enabled (already supplied by the image);
- Chromium-compatible shared memory and a seccomp policy allowing its user
  namespace sandbox.

The `browser-health` action and container health check report readiness. Keep
session concurrency bounded at the Atlas policy layer.

## Engine decision and limitations

Vercel `agent-browser` is used because it supplies isolated daemon sessions,
accessible snapshots, stable element references, and reliable Chromium child
cleanup. Its unrestricted CLI is not exposed.

The engine's own domain-allowlist mode is not the security boundary: current
`agent-browser` documentation states that allowed-domain enforcement requires a
fresh controllable context and is incompatible with profile/session startup or
state restore. Persistent authenticated profiles are a requirement here, so
the skill instead enforces allowlists and SSRF policy at its validating proxy.

Operational limitations:

- sessions are single-page, single-active-profile workflows;
- screenshots are PNG only and capped at 4 MiB;
- snapshots cap at 200 KiB and reads at 100,000 characters;
- ordinary interactions time out at 30 seconds;
- profile contents are sensitive operational state and require encrypted,
  access-controlled storage supplied by the deployment;
- challenge detection is conservative pattern matching and does not replace a
  human review checkpoint.
