# Generic API and MCP contracts

These are provider-neutral learning skills backed by a shared Go runtime. The agent
reads authoritative API documentation or MCP discovery output and constructs a
profile. The compiler validates it and deterministically creates named OpenSeal
blocks. No service-specific endpoint or workflow is compiled into the executors.

## Pipeline and trust boundary

```
documentation / MCP discovery
        → agent-authored candidate profile + source evidence
        → offline validation / deterministic compilation
        → fixture and authorized live checks
        → governed upsert_binding persists contract + opaque credential reference
        → named, schema-checked calls
```

The compiler is not an LLM or an automatic OpenAPI importer. Documentation fetching,
source interpretation, and profile construction use the agent's available tools and
the skills' prompt modules. OpenAPI is useful evidence; normalize only supported
features without losing assertions. Human-readable documentation also works.

Profiles are trusted deployment configuration, not per-call input. A proposed
profile does not grant authority. The provisioning host must review/apply existing
policy to the destination, operations, effects, resource scope, and credential
binding. It must keep the generated manifest paired with the exact profile hash.
The runtime does not implement user approval, credential issuance, or tenant access
control. Do not expose its gRPC port directly to untrusted callers: like other
repository skills, it assumes an authorized host supplies bindings and calls.

## Profile v1

Both transports use JSON with these fields (unknown fields are rejected):

| Field | Meaning |
|---|---|
| `version` | Integer `1` |
| `id` | Lowercase stable integration name; letters, digits, hyphens |
| `transport` | `api` or `mcp` |
| `endpoint` | Fixed HTTPS origin for API, complete HTTPS URL for MCP; port 443 only, no userinfo/query/fragment |
| `sources` | Authoritative documentation/discovery URLs; archive evidence separately |
| `credential` | Optional `{binding, field, header, prefix}`; contains references, never secret values |
| `fixedQuery` | Optional fixed, nonsecret account/scope parameters; operations cannot override them |
| `operations` | Up to 100 named operation contracts; empty for MCP discovery bootstrap |

Credential headers support `Authorization`, `Api-Key`, and custom `X-*` headers.
Examples of prefixes are `Bearer `, `Token `, or the empty string. The named binding
must contain an object whose specified field holds the secret string. The binding
name also serves as the generated manifest's credential kind. An already-issued
OAuth access token works as a bearer credential; acquiring/refreshing it remains
the host credential broker's responsibility. Compound authentication needs an
adapter extension. Never place secrets in `fixedQuery` or schema constants.

Each operation has `name`, `description`, `effect` (`read` or `write`), `inputSchema`,
and optional `outputSchema`. Use bounded schemas and explicit success-envelope
constraints when the service returns HTTP 200 for application errors. The runtime
does not inject schema defaults. Supply defaults in arguments. Mutating HTTP methods
must be classified as writes; authors must also classify mutating GETs as writes.

API fields:

| Field | Meaning |
|---|---|
| `method` | GET, HEAD, POST, PUT, PATCH, DELETE |
| `path` | Absolute path, e.g. `/v2/records/{id}`; cannot change origin |
| `pathParams` | Map path placeholder → top-level argument property |
| `queryParams` | Map query key → top-level argument property; scalar values only |
| `bodyParam` | Optional top-level argument property containing the complete body |
| `bodyEncoding` | `json` (default), `form` (scalar object), or `text` (string) |
| `responseEncoding` | `json` (default) or `text`; empty JSON response becomes null |

API output is `{status, data, profileHash, operation}`. `outputSchema`, if supplied,
checks `data` before credential redaction. A schema mismatch after a write does not
undo that write. Nested bodies retain their JSON structure. Path parameters are
escaped and reject separators/traversal; full URLs are not valid path identifiers.
Arrays/objects in query parameters, multipart uploads, streaming bodies, signed
requests, and arbitrary dynamic headers are unsupported.

MCP fields:

| Field | Meaning |
|---|---|
| `tool` | Exact remote tool name |
| `toolHash` | SHA-256 hash returned by discovery for the complete tool definition |

MCP output is `{data, profileHash, operation}`, with the original successful MCP
result in `data`. The local and advertised input schemas are both checked. Advertised
outputSchema and local outputSchema check `structuredContent`. Discovery includes
all pages (max 20 pages / 1,000 tools) and rejects repeated cursors and duplicate
names. Connections are initialized per action; negotiated session/protocol headers
are preserved and sessions are closed best-effort. Supported protocol versions are
2025-03-26, 2025-06-18, and 2025-11-25. POST responses may be JSON or SSE. No package
execution, stdio, legacy SSE transport, OAuth bootstrap, server-initiated requests,
task-required tools, or resumable streams are implemented.

## Schemas and execution limits

The validator supports a reference-free common subset of draft-07 / 2020-12:
objects/properties/patternProperties/additionalProperties, scalar types, arrays with
single-schema items, enum/const, required, numeric/string/collection bounds, pattern,
uniqueItems, contains, allOf/anyOf/oneOf/not, and if/then/else. Unsupported keywords
(including `$ref`, format, nullable, prefixItems, and unevaluatedProperties) fail
compilation. Resolve references and translate semantics deliberately in the learning
phase; never drop a validation assertion to get a profile accepted. This is not a
full OpenAPI or JSON Schema implementation.

Public HTTPS only. DNS results are checked at dial time and the validated address is
used directly. Private, loopback, link-local, and common special networks are blocked.
Redirects and environment proxies are disabled; credentials never follow another
origin. A private TSDB requires a separately governed private-network adapter rather
than a caller-controlled bypass. Authentication failures do not trigger credential
fallback or automatic refresh.

Profiles, arguments, request bodies, and individual responses are capped at 2 MiB.
HTTP requests have a 30-second timeout; an action has a 60-second deadline. There are
no automatic retries, provider idempotency headers, or exactly-once guarantees.
All generated execution actions declare one attempt. Writes require the platform's
durable action idempotency key (`idempotency: required`); this is kernel dispatch
deduplication, not a promise of provider-side exactly-once execution. Reads declare
`idempotency: none`. Writes carry
conservative `production` / `external` policy; read operations use `read` / `read`.
A host must reconcile ambiguous outcomes before deciding to retry writes.

Errors omit raw remote bodies and credentials. Successful outputs redact common
secret keys and literal occurrences of the supplied credential. This is not general
DLP: business data and unrelated secrets may remain sensitive. The host must apply
its normal output-access and logging policy. Profiles must contain no secrets, as
`*-describe` and compilation intentionally return their contents.

## Build, compile, run

From the repository root:

```bash
go test -mod=mod ./internal/integration ./skills/api ./skills/mcp
go build -mod=mod -o /tmp/skill-api ./skills/api
go build -mod=mod -o /tmp/skill-mcp ./skills/mcp
/tmp/skill-api -profile skills/api/examples/records.json -check
/tmp/skill-api -profile skills/api/examples/records.json -compile > /tmp/records-bundle.json
```

The example uses a fictional provider and requires no network or credential for
compilation. It is a format demonstration, not a working public service. For a real
service, learn a new profile from its actual docs.

## Platform activation without per-service deployments

Install the `skill-api` / `skill-mcp` 0.2.0 base manifest and runtime image once using
the existing platform installation flow. With no `INTEGRATION_PROFILE` environment
variable, the service registers compilation, inspection, and effect-specific
execution actions. It does not accept a model-controlled endpoint or profile.

Compilation also returns `bindingPlan`:

- `upsertArguments`: the exact input shape of `openseal.skills.upsert_binding`, with
  the base Skill ID/version, binding ID, expected revision, allowed actions, risk,
  operation/hash restrictions, and host-owned `config.integration`.
- `requiredAccess` and `requiresAccessReference`: identify the generic credential
  slot when the service requires authentication. No credential identity is invented.
- `blocks`: stable operation names, input schemas, base action selection, and fixed
  operation/hash arguments. These are reusable call templates; the platform exposes
  the shared read/write actions, not newly registered per-provider Skill definitions.
- `state: prepared`: compilation has not performed a binding mutation.

The non-secret configuration stores a profile plus `access` header/reference
metadata. Access metadata contains no credential value. Before submitting the plan,
the host's existing credential flow supplies an opaque reference under
`accessReferences.integration-access`, with kind `integration-access`. The broker
must authorize that slot/reference for the service and tenant. A credential can be
a raw access string or an object containing the learned field. Tokens stay in the
credential channel and are resolved at execution. Direct gRPC callers are trusted
host infrastructure, not a public API.

Use `expectedRevision: 0` only when creating a binding. For updates, fetch and supply
the current revision and exact installed source identity. The management action
derives tenant/Agent ownership from the current run, applies existing authorization
and approval policy, and persists the contract and reference using the normal
binding store. Existing database lifecycle/audit/revision checks apply. No new tables,
credential store, public endpoint, or provisioning privilege are introduced.

After upsert succeeds, select that exact binding ID/revision and call `api-inspect`
or `mcp-inspect` to verify the hash. Execute a named block through `api-read`,
`api-write`, `mcp-read`, or `mcp-write` using:

```json
{"profileHash":"<compiled hash>","operation":"record-get","arguments":{"recordId":"record-123"}}
```

The platform input schema prohibits `integration` and credential transport keys;
Cortex merges trusted binding configuration only after rejecting collisions.
The runtime reconstructs and validates the bound contract, enforces its hash and
operation effect, then injects the credential. No mutable runtime-global profile is
shared across bindings. For MCP bootstrap, bind a connection profile with no
operations, call `mcp-discover-bound`, then compile the selected tools and update the
binding using its current revision. Invalidate cached block revisions after updates.

The platform rejects secret-like keys in non-secret configuration, including nested
schema property names. A profile that cannot satisfy this rule gets a
`bindingPlanError` instead of a binding plan. Do not serialize/base64 the profile to
bypass that rule. Such APIs need an explicit host configuration-contract extension.

The host still must have the generic runtime installed, the management action
available to the Agent, and an authorized credential reference when required. A
prepared plan does not prove those prerequisites exist. Missing prerequisites must
be reported, not treated as successful activation. OCI images have to be built and
published before deployment; this implementation does not publish them implicitly.

## Standalone mounted-profile mode

The original `manifest`, `profile`, and `profileHash` export remains available for
hosts that provision a service instance per profile. Extract the manifest, mount the
profile read-only, and set `INTEGRATION_PROFILE=/path/to/profile.json` on the matching
runtime image. That instance registers the profile's named operations plus
`api-describe` / `mcp-describe` and optional `mcp-discover`. This path is distinct from
the preferred platform binding plan and is not automatically provisioned.
`SKILL_PORT` defaults to 50051. Every exported action explicitly names its executor
entrypoint; service identity is not used as the action's transport endpoint.

Every standalone named operation accepts:

```json
{"profileHash":"<hash from compile/describe>","arguments":{"recordId":"record-123"}}
```

Hashing uses Go's stable JSON serialization of the typed profile/tool object. It
ignores input object key ordering but preserves array ordering; it is not RFC 8785
canonical JSON. Same normalized profile produces the same manifest and hash.
Hashes are drift checks, not signatures or evidence of trusted authorship. Tool
hashes do not prevent a malicious server changing its implementation while
advertising the same contract.

## Sources

The architecture uses the [OpenAPI specification](https://spec.openapis.org/oas/v3.0.3.html)
as an optional learning input and MCP's official
[lifecycle](https://modelcontextprotocol.io/specification/2025-11-25/basic/lifecycle),
[tools](https://modelcontextprotocol.io/specification/2025-11-25/server/tools), and
[transport](https://modelcontextprotocol.io/specification/2025-11-25/basic/transports)
contracts. No provider-specific implementation is required to create a new profile.

## Platform contract tests

The isolated test module verifies these plans against the OpenSeal version used by
Cortex, including binding validation, durable SQLite reload, revision conflicts,
cross-tenant/cross-Agent denial, credential-key injection denial, and effect
restrictions:

```bash
cd tests/platform
go test -mod=mod -p 2 ./...
```

After updating learning guides or the base contract, regenerate canonical manifests
from the repository root with `go run -mod=mod ./cmd/integration-manifests`.
