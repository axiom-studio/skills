# Browser Skill

`skill-browser` is the canonical governed browser Skill. Its Python runtime
implements the Skill gRPC
transport wrapping [Camoufox](https://github.com/daijro/camoufox) — a
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
docker build -f skills/camoufox/Dockerfile -t axiomstudio/skill-browser:2.0.41 .
```
