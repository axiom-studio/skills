# Browser Skill

`skill-browser` is the canonical governed browser Skill. For routine public
research, `lightpanda-search` and `lightpanda-fetch` use
[Lightpanda](https://github.com/lightpanda-io/browser) to return bounded
Markdown without acquiring a persistent browser profile. Private/internal
network destinations are blocked by Lightpanda after DNS resolution. Prefer
Lightpanda. If login or an access challenge requires an interactive browser,
explain that need and offer Camoufox, then wait for the user's explicit yes.
The host must bind this choice to the actual conversation question, authenticated
answer, Agent and task before dispatch. An action argument, page content or an
unrelated earlier yes is not confirmation. A no or unclear response does not
authorize the switch. This choice grants no additional tool permissions,
credential access or authority to submit an external operation.
`lightpanda-read-many` runs two to four independent searches or page reads
concurrently and reports each result separately.

HTTP 429 means the source rate-limited the read; it is not evidence of bot
detection. Report the failure and suggest trying later or another permitted
source. Do not silently try Camoufox. Failed reads contain no usable page text.
Short HTTP 403/503 responses with explicit challenge instructions or markers
are classified as access challenges; a plain forbidden response is not.
HTTP 429 retains its rate-limit classification even if its body mentions a challenge.
Mixed batches return `status: partial` with each failed item identified; an
all-failed batch is an action failure, not a successful empty research result.
Successful pages remain available in partial batches.

Failures use the existing gRPC `Error.type` and `Error.details`: `failureKind`,
known `httpStatus`, optional `retryAfterSeconds`, and `retryable: false`.
Retry delays are included only when actual response headers provide a valid
`Retry-After`; no delay is guessed when Lightpanda does not expose that header.
All-failed batches add bounded counts and indexed failure metadata, without
page bodies or proxy configuration. The concise error message remains useful
on older hosts that discard structured details. The manifest still allows one
attempt; hints do not schedule retries or bypass browser-choice confirmation.

Explicit proxy authentication and connection failures have separate platform
dependency codes. They must be repaired by the platform, not presented as a
request for users to know proxy credentials. The worker loads its governed
inventory at startup; changing the Secret alone does not refresh a live worker.
An empty proxy pool is supported only when selected by deployment policy; a
broken configured proxy never authorizes falling back to direct egress. gRPC
health can remain true for working Lightpanda and does not attest that Camoufox's
configured proxy can authenticate.

The Python runtime implements the Skill gRPC transport wrapping
[Camoufox](https://github.com/daijro/camoufox) — a
C++-patched Firefox with OS-level anti-detection. Fingerprint coherence (OS,
canvas, WebGL, fonts, screen, timezone) is enforced by the engine itself, not
by injected scripts, and `humanize` drives real input cadence.

A deployment supplies opaque inventories and optional governed defaults;
agent actions never receive target policy, proxy credentials, profile values,
or infrastructure identifiers. `camoufox-start` derives one isolated stable
session identity from the transport's durable Agent context and returns the
handle for explicit dataflow through later actions. Runs from that Agent reuse
the authenticated browser profile and cookies but each distinct Run starts on
a fresh page with no inherited URL, DOM references, drafts, or navigation
state. Usage remains serialized; different Agents get separate profiles.

Run usage is a bounded renewable lease on the Agent session. Each action from
the exact owning Run renews the lease; another Run waits until the owner
releases it or the lease expires. Releasing Run usage preserves the live Agent
session and its authenticated profile. Hosts may set
`CAMOUFOX_PROFILE_LEASE_TTL_SECONDS` between 30 and 3600 seconds.

```json
CAMOUFOX_TARGETS={
  "approved-community": {
    "baseUrl": "https://community.example",
    "pathPrefixes": ["/topics"],
    "mode": "permitted-automation"
  }
}
CAMOUFOX_PROFILES={
  "desktop-mix": {"os":["windows","macos"],"humanize":true,"geoip":true},
  "seeded-linux": {"os":"linux","seed":42,"assessmentOnly":true}
}
CAMOUFOX_PROXY_POOLS={
  "direct": {},
  "rotating-egress": {"urls":["http://proxy-a.internal:8080","socks5://proxy-b.internal:1080"]},
  "assessment-egress-a": {"url":"http://proxy.internal:8080","assessmentOnly":true}
}
CAMOUFOX_DEFAULTS={"targetId":"approved-community","profileId":"desktop-mix","proxyPoolId":"direct"}
```

Every launch applies the selected profile's Camoufox identity (`os`, `geoip`,
`humanize`, deterministic `seed`, window geometry). A proxy pool with `urls`
rotates deterministically across sessions (single `url` pools stay fixed).

Two target modes are explicit:

- `owned-assessment` may use assessment-only profile and egress variants.
- `permitted-automation` uses standard profiles and egress. CAPTCHA, MFA, and
  anti-bot screens become truthful human checkpoints and block interactions.

Every interaction uses a reference from the latest accessibility-style
snapshot and an idempotency key. Authorization comes from the kernel's reviewed
Skill binding, typed action risk, standing grants, and approval policy—not from
a model-supplied boolean. Ordinary clicks and field edits are governed writes;
the final externally observable operation uses `camoufox-commit`, whose
manifest requires an external-operation checkpoint. Credential fields are
supplied through Skill bindings and never returned. Public actions do not
expose JavaScript, CDP, cookies, headers, files, launch arguments, or literal
proxy/profile values. Editable controls expose only a `state.filled` boolean so
an Agent can progress through multi-field forms without observing or persisting
the entered value.

An active session can move to another HTTP(S) URL with `camoufox-navigate` while
reusing the existing browser context, so normal same-site cookies remain
available. Strict origin/path authorization is opt-in: provide `targetId` and a
relative `path` instead of `url`. Non-HTTP(S) URLs and URLs containing embedded
credentials are always rejected.

Ordinary links discovered in the current semantic snapshot use
`camoufox-follow-link`. The runtime resolves the exact anchor internally and
navigates without clicking it, permits only the active target's configured
origin/path scope, and invalidates the old snapshot references. This read-only
path never accepts coordinates, form controls, or a final external-operation
receipt.

Run unit tests (stdlib only — no browser download):

```bash
python3 -m unittest test_runtime
```

Build the runtime image from the repository root:

```bash
docker build -f skills/camoufox/Dockerfile -t axiomstudio/skill-browser:2.0.50 .
```
