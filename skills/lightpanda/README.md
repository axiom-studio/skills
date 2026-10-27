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
docker build -f skills/lightpanda/Dockerfile --build-arg SKILL_NAME=lightpanda -t axiomstudio/skill-browser:3.2.0 .
```

Version 3.0.0 removed every `camoufox-*` action, the Camoufox engine, its
profile storage and its target/profile/proxy inventory (`CAMOUFOX_*`
environment variables are no longer read).

Version 3.0.1 declares `tenancy: shared`: the host runs one Browser Skill
runtime for every tenant. Each action is a separate Lightpanda process with no
cookies, profile or cache carried between requests.

Version 3.0.2 sends `lightpanda-search` to Yahoo Search. Brave, DuckDuckGo,
Google, Mojeek, Ecosia and Yandex answer Lightpanda with bot challenges, and
Bing returns unrelated results to it.

Version 3.1.0 lets the host choose the search provider. Sentinel injects the
host-owned config `searchProvider` (`{"kind": "yahoo"|"tavily", "fallback":
"yahoo"|null}`) and, for Tavily, the credential binding `searchApiKey`; neither
is in any action schema, and without them search uses Yahoo. Tavily searches
call `POST https://api.tavily.com/search` and return the same
`{url, httpStatus, text, truncated}` shape, with `text` a Markdown list of
titles, URLs and snippets. A missing key fails as
`search_provider_unconfigured`, a rejected key (401/403) as
`search_provider_authentication_failed`, a 429 as `source_rate_limited` and
432/433 as `search_provider_quota_exceeded`. With `fallback` set to `yahoo`,
any Tavily failure answers from Yahoo instead; the result `url` (the Tavily
endpoint or the Yahoo search URL) shows which one served. A short
captcha or bot-check body is now `source_access_challenge` even on HTTP 429,
and a 429 whose `Retry-After` is 10 seconds or less is retried once.

Version 3.2.0 reads the search provider from the Skill's operator config, the
standard `config` section in `skill.yaml` (`searchProvider`: `yahoo`|`tavily`,
default `yahoo`; `tavilyApiKey`, a secret required when the provider is
Tavily; `fallbackToYahoo`, default `false`). Operators set it globally or per
tenant in HelloSeal; the platform attaches the calling tenant's effective
values to every call in `ExecutionContext.config`, and `skill_config.py`
decodes them with declared defaults. The host-injected `searchProvider` config
and `searchApiKey` binding are gone. A value of the wrong type or outside the
enum fails the call with error type `config`. The Health RPC reports the
config schema and, when asked, checks the given values: a reachability probe
for Yahoo and an authenticated `GET https://api.tavily.com/usage` (no search
credits) for a Tavily key.
