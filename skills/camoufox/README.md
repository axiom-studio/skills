# Browser Skill

`skill-browser` is the canonical governed browser Skill. Its Python runtime
implements the Skill gRPC
transport wrapping [Camoufox](https://github.com/daijro/camoufox) — a
C++-patched Firefox with OS-level anti-detection. Fingerprint coherence (OS,
canvas, WebGL, fonts, screen, timezone) is enforced by the engine itself, not
by injected scripts, and `humanize` drives real input cadence.

A deployment supplies opaque inventories; agent actions select identifiers and
never receive raw target policy, proxy credentials, or profile values.

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
```

Every launch applies the selected profile's Camoufox identity (`os`, `geoip`,
`humanize`, deterministic `seed`, window geometry). A proxy pool with `urls`
rotates deterministically across sessions (single `url` pools stay fixed).

Two target modes are explicit:

- `owned-assessment` may use assessment-only profile and egress variants.
- `permitted-automation` uses standard profiles and egress. CAPTCHA, MFA, and
  anti-bot screens become truthful human checkpoints and block interactions.

Every interaction uses a reference from the latest accessibility-style
snapshot, explicit write authorization, and an idempotency key. The final
external operation uses `camoufox-commit`, whose manifest requires a governed
external-operation checkpoint. Credential fields are supplied through Skill
bindings and never returned. Public actions do not expose JavaScript, CDP,
cookies, headers, files, launch arguments, or literal proxy/profile values.

An active session can move to another HTTP(S) URL with `camoufox-navigate` while
reusing the existing browser context, so normal same-site cookies remain
available. Strict origin/path authorization is opt-in: provide `targetId` and a
relative `path` instead of `url`. Non-HTTP(S) URLs and URLs containing embedded
credentials are always rejected.

Run unit tests (stdlib only — no browser download):

```bash
python3 -m unittest test_runtime
```

Build the runtime image from the repository root:

```bash
docker build -f skills/camoufox/Dockerfile -t axiomstudio/skill-browser:2.0.2 .
```
