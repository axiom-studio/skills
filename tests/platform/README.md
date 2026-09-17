# Integration platform tests

This module tests the generic skills against the OpenSeal dependency used by
Cortex without adding OpenSeal to the production skill runtime dependencies.
Run commands from this directory.

## Local contract regression tests

```bash
go test -mod=mod -race -p 2 ./...
```

The default run skips external-service tests. It validates persisted bindings,
revision conflicts, tenant and Agent isolation, input injection rejection, and
read/write restrictions.

## Live end-to-end examples

```bash
INTEGRATION_LIVE_E2E=1 go test -mod=mod -p 2 -run TestLiveExamplesE2E -v -count=1
```

Requires Go, outbound HTTPS, and localhost TCP access. No service credentials are
needed. The test builds the actual API and MCP executables from this worktree,
starts them on temporary gRPC ports, and removes the processes/databases afterward.
External outages, rate limits, and changed remote contracts fail the opt-in test;
it does not silently skip a failing service or weaken the production network policy.

Each example uses the checked-in canonical Skill manifest and follows:

1. Author a test profile from the linked official documentation.
2. Invoke compilation through a durable OpenSeal action and gRPC.
3. Propose `openseal.skills.upsert_binding`, resolve the local test approval, and
   execute it through the real management dispatcher and action worker.
4. Reload the catalog from SQLite and resolve the exact binding/revision.
5. Dispatch the learned operation through OpenSeal's tool dispatcher, a test host
   implementing Cortex's configuration merge boundary, and the gRPC runtime.
6. Call the real public service with production HTTPS/DNS checks and assert the
   returned data and schema.

| Example | Learning source | Verified behavior |
|---|---|---|
| GitHub repository metadata | [Repository API](https://docs.github.com/en/rest/repos/repos#get-a-repository) | Path substitution, HTTP 200, `golang/go` identity and branch |
| Open-Meteo current weather | [Forecast API](https://open-meteo.com/en/docs) | Numeric query parameters, fixed query options, timestamp and temperature |
| Cloudflare documentation MCP | [Official server catalog](https://developers.cloudflare.com/agents/model-context-protocol/cloudflare/servers-for-cloudflare/) | Connection bootstrap, live discovery, schema/hash capture, governed binding update to revision 2, actual tool call and structured documentation results |

API fixture profiles live only under `testdata`; the runtime contains no provider
specific handlers. MCP tools and schemas are learned from live discovery during the
test rather than hardcoded in its connection profile.

## Verified run

On 2026-09-17, all three examples passed in 9.84 seconds:

- GitHub: HTTP 200, repository `golang/go`, default branch `master`.
- Open-Meteo: HTTP 200, temperature 24.5 °C at `2026-09-17T11:15` for the requested
  Bengaluru coordinates. This is a recorded test observation, not a current forecast.
- Cloudflare MCP: two tools discovered, binding advanced to revision 2, seven
  search results; the first linked to `https://developers.cloudflare.com/kv/get-started/`.

A repeat with `-race` also passed all three examples in 11.07 seconds. It explicitly
verified approval checkpoints and persisted approval attribution; the MCP query
returned six results on that run.

The result count, branch, and weather values are observations, not brittle expected
values in the tests. Tests assert durable invariants and required response structure.

## Coverage boundary

This is a local OpenSeal + actual gRPC binaries + live upstream-services test. It
does not install images into a deployed Cortex/Atlas cluster or test Studio UI,
production credential leases, authenticated provider writes, or OAuth onboarding.
Those are separate deployment tests requiring a target environment and authorized
credentials. The test approval principal belongs only to the isolated local store.
