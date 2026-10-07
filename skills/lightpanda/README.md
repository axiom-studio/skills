# Browser Skill (Lightpanda)

`skill-browser` is the canonical page-reading browser Skill and the Skill that
Cortex's bundled `browser` binding resolves. It uses
[Lightpanda](https://github.com/lightpanda-io/browser) to return bounded
Markdown for public pages and searches. It has no browser session, profile,
cookies, sign-in or interaction: those belong to the separate live browser
Skill (`skills/live-browser`), which the user can watch and take over.

Actions (all `risk: read`):

- `lightpanda-search` searches the public web and returns result Markdown.
- `lightpanda-fetch` reads one public HTTP(S) page.
- `lightpanda-read-many` runs two to four independent searches or page reads
  concurrently and reports each result separately.

Private/internal network destinations are blocked by Lightpanda after DNS
resolution. URLs with embedded credentials and non-HTTP(S) URLs are rejected
before Lightpanda runs.

HTTP 429 means the source rate-limited the read; it is not evidence of bot
detection. Report the failure and suggest trying later or another permitted
source. Failed reads contain no usable page text. Short HTTP 403/503 responses
with explicit challenge instructions or markers are classified as access
challenges; a plain forbidden response is not. HTTP 429 keeps its rate-limit
classification even if its body mentions a challenge. Mixed batches return
`status: partial` with each failed item identified; an all-failed batch is an
action failure, not a successful empty result.

Failures use the gRPC `Error.type` and `Error.details`: `failureKind`, known
`httpStatus`, optional `retryAfterSeconds`, and `retryable: false`. Retry
delays are included only when actual response headers provide a valid
`Retry-After`; no delay is guessed. All-failed batches add bounded counts and
indexed failure metadata, without page bodies.

Run the unit tests (no browser download; PyYAML is the only non-stdlib import):

```bash
python3 -m unittest discover -s skills/lightpanda -p 'test_*.py'
```

Build the image from the repository root:

```bash
docker build -f skills/lightpanda/Dockerfile --build-arg SKILL_NAME=lightpanda -t axiomstudio/skill-browser:3.0.0 .
```

Version 3.0.0 removed every `camoufox-*` action, the Camoufox engine, its
profile storage and its target/profile/proxy inventory (`CAMOUFOX_*`
environment variables are no longer read).
