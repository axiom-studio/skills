# Veiled Browser Skill

`skill-veiled-browser` is a native JavaScript implementation of the canonical
Skill gRPC transport. It runs pinned VeilBrowser 1.3.1 with system Chromium and
does not require a Go wrapper.

The default product journey is an authorized browser-security assessment of a
platform-owned target. A deployment supplies opaque inventories; agent actions
select identifiers and never receive raw target policy, proxy credentials, or
fingerprint payloads.

```json
VEILED_BROWSER_TARGETS={
  "checkout-fixture": {
    "baseUrl": "https://anti-bot.internal.example",
    "pathPrefixes": ["/assessment"],
    "mode": "owned-assessment"
  },
  "approved-community": {
    "baseUrl": "https://community.example",
    "pathPrefixes": ["/topics"],
    "mode": "permitted-automation"
  }
}
VEILED_BROWSER_PROFILES={
  "standard": {},
  "windows-assessment": {"preset":"windows-chrome","assessmentOnly":true}
}
VEILED_BROWSER_PROXY_POOLS={
  "direct": {},
  "assessment-egress-a": {"url":"http://proxy.internal:8080","assessmentOnly":true}
}
VEILED_BROWSER_CHALLENGES={
  "checkbox-v1": {
    "targetId":"checkout-fixture",
    "kind":"synthetic",
    "accessibleName":"Authorized test challenge"
  }
}
```

Two target modes are explicit:

- `owned-assessment` may use assessment-only profile and egress variants plus
  configured synthetic challenges.
- `permitted-automation` uses standard profiles and egress. CAPTCHA, MFA, and
  anti-bot screens become truthful human checkpoints and block interactions.

Every interaction uses a reference from the latest accessibility snapshot,
explicit write authorization, and an idempotency key. The final external
operation uses `veiled-browser-commit`, whose manifest requires a governed
external-operation checkpoint. Credential fields are supplied through Skill
bindings and never returned. Public actions do not expose JavaScript, CDP,
cookies, headers, files, launch arguments, or literal proxy/fingerprint values.

Run unit tests with Node 24:

```bash
npm ci --ignore-scripts
npm test
```

Build the pinned runtime image from the repository root:

```bash
docker build -f skills/veiled-browser/Dockerfile -t axiomstudio/skill-veiled-browser:1.0.0 .
```
