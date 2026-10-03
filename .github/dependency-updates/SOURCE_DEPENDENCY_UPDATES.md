# Skills source dependency updates

The dependency workflow prepares a branch under `dependency-updates/`, validates
its exact candidate, and promotes passing dependency changes directly to `main`.
It does not create a pull request or change Skill behavior, permissions,
definitions, or installation versions. Skill definition and image publication
remain an explicit release boundary.

## Configuration

An administrator must install and configure a dedicated GitHub App for source
dependency maintenance. Scope it to the participating repositories and the
private repositories required for internal Go module resolution, including the
Skills SDK and OpenSeal contract-test dependency. Add the App configuration
required by the workflow as repository or organization configuration. Do not
reuse registry publishing secrets or unrelated integration tokens.

Toolchain installation, dependency lifecycle code, tests, and image builds run
on an isolated hosted runner without repository write credentials. The runner
needs Go matching the root `go.mod`, Node 24, npm, Python 3.11 or newer with
`venv`, make, Docker, a C compiler for race tests, and outbound package-registry
access. The workflow verifies official Go and Node toolchain checksums before
candidate execution. App write credentials must never enter the candidate Git
configuration, environment, Docker build arguments, or dependency caches.
If a private module cannot be resolved with the permitted preparation path,
the candidate remains held; tests and vendoring are not bypassed.

## Candidate validation

The trusted `.github/dependency-updates/checks.sh` performs:

1. `npm ci` for the meeting-voice and meet-voice lockfiles.
2. A temporary Python environment installed from the exact Camoufox requirements.
3. The root Go unit suite against the checked-in vendor tree.
4. The isolated `tests/platform` binding and authorization contract tests with
   the race detector and read-only module inputs.
5. `scripts/validate.sh`, which builds Go services, runs Python and Node tests,
   and checks manifest structure.
6. `make docker-build`, building every supported Skill service image, followed
   by a build of the alternate meeting-voice local-transcript Dockerfile.

Every failing preparation, test, or image build holds promotion. Tests use
temporary state and local fixtures; the gate supplies no provider credentials
and enables no production actions. The platform suite's live upstream examples
are explicitly opt-in in existing tests and are not enabled by this source
gate. Deployment and provider integration checks belong to a separate release
validation environment.

The existing validator's `scripts/lib/health.sh` is a placeholder that marks all
gRPC health checks as skipped. Running the validator therefore does not prove
that every service starts or that production integrations work. The additional
unit, contract, and image build checks provide their stated coverage; no skipped
health check is reported as passing.

Go candidates require canonical reconstruction of every existing vendor tree.
A modified or incomplete baseline vendor tree holds Go updates until a normal
source change repairs it. The controller does not discard vendor patches or
replace private SDK packages with local shortcuts. npm, Python, and Docker
updates can be prepared independently. The incomplete `skills/mongodb` module
has no executable entrypoint and is excluded from image discovery by the
existing Makefile. Its module inputs are also outside automatic promotion;
adding its service and dependency-update coverage requires a separate source
change.

`make docker-build` covers the real heavyweight browser and voice image
installation steps. External download failures hold the candidate. This work
is bounded to one candidate on a hosted runner and never runs on the platform
request-serving machines. It builds local images and never calls `docker push`.
Some existing Dockerfiles resolve floating OS packages, browser downloads, or
base-image tags; these gates do not claim fully reproducible releases.

## Promotion and release boundary

Promotion requires passing policy and candidate checks, unchanged dependency
inputs after the checks, and a default branch that still matches the tested
baseline. The controller does not force-push or reuse results from a stale
baseline. Concurrent updates are serialized; failures retain diagnostics.

The Skills repository currently publishes images through manual
`make docker-push`. This source workflow does not publish those images, alter
Skill versions or installer tags, or deploy runtimes. An authorized release
must build and publish verified images under new immutable versions and update
their Skill definitions. Only then can the runtime Skill updater discover and
install the released version. Updating `main` alone does not update an Agent.

## Shared source maintenance contract

This workflow is configured to run daily at 03:17 UTC and on manual dispatch.
It resolves supported dependency updates with pinned Renovate 44.132.2. A
repository-specific allowlist restricts changed paths to dependency manifests,
lockfiles, vendor files, Docker inputs, and declared generated inputs. Changes
to application code, workflow files, permissions, or Skill manifests are held.
Major updates, replacement/rollback proposals, unstable releases, and minor
updates to 0.x dependencies are held. Docker base-image minor upgrades also
require a separate reviewed source change.

Generation, validation, and promotion are separate jobs on isolated
`ubuntu-24.04` GitHub-hosted runners. They never run on the platform node.
There is one candidate per invocation, jobs have explicit timeouts, and
parallel Go compilation is bounded to two workers. No package lookup,
compilation, image build, or polling from this workflow runs inside a request
or conversation handler.

Renovate is deliberately configured with a supported dependency dashboard and
`prCreation: approval`, with all automerge disabled. The dashboard provides
candidate diagnostics; it is not our promotion authority. Do not approve its
PR controls. The dedicated writer App must have no pull-request write
permission, so approval cannot create a PR. The workflow promotes only a
verified source commit directly to the repository's configured default branch.

Renovate itself uses `--force-with-lease` when preparing a candidate. We limit
that behavior to a checked-unused `dependency-updates/<run>-<attempt>/`
namespace, unique to the workflow invocation. Namespace reuse or collision
holds generation. Renovate is never permitted to automerge the default branch.
The separate promoter uses plain, non-force Git push. It rejects a candidate
unless it is exactly one regular-file-only commit on the tested baseline, the
exact remote candidate still matches, and the default branch has not advanced.
A concurrent default advance fails Git's fast-forward check. The next run must
resolve and test a fresh candidate; it cannot reuse a stale verdict.

Validation copies the check recipe and policy from the trusted baseline before
checking out the candidate. It receives an independently read-only credential.
Promotion runs in a fresh job with no candidate code execution and checks the
GitHub Actions API for the successful exact validation job and matching
workflow run, attempt, and baseline. Before tests, the trusted validator also reconstructs vendor from the exact
candidate module inputs when Go/vendor files changed and canonical vendor is
required. Tampered vendor bytes are rejected even if module metadata matches.
An unrelated npm or Docker candidate skips this reconstruction and preserves
reviewed legacy vendor patches. Tracked or untracked build input changes
after testing hold promotion. The promoted commit and policy digest appear in
the Actions job summary. After successful promotion finishes and Actions persists its exact receipt
summary, a separate trusted cleanup step verifies that the promoted commit
remains reachable from the current default and removes only this run's
unchanged candidate ref using an explicit expected-SHA deletion lease. A changed ref is preserved; cleanup
failure is diagnostic and never reverses a successful default advancement.
The commit stays reachable on the default branch and its receipt stays in
Actions. Failed or changed candidates remain for audit. Generation holds at
32 remaining reserved refs until an administrator reviews exact receipts and
current SHAs and manually cleans owned failed candidates. No branch is deleted
solely because of its prefix, and failed evidence is not silently discarded.

## Required GitHub configuration

Two new GitHub Apps are required. No existing integration, registry, or
publishing token is borrowed. Configure these repository or organization
variables and secrets:

| Configuration | Kind | Required capability |
| --- | --- | --- |
| `DEPENDENCY_UPDATE_APP_ID` | Actions variable | Dedicated writer App ID |
| `DEPENDENCY_UPDATE_APP_PRIVATE_KEY` | Actions secret | Writer App private key |
| `DEPENDENCY_UPDATE_READ_APP_ID` | Actions variable | Independently read-only App ID |
| `DEPENDENCY_UPDATE_READ_APP_PRIVATE_KEY` | Actions secret | Read-only App private key |

Install the writer App only on the participating maintenance repositories.
Its repository permissions are Contents: write, Issues: write, Pull requests:
read, and Metadata: read. It must have no Pull requests: write permission.
Each generated writer token explicitly requests only those permissions and
only the current repository. Issues access is needed for the supported
dependency dashboard; it grants no platform or credential authority.

Install the read App on the participating repositories and any private
internal module repositories they require. Give it Contents: read and
Metadata: read, with no write permissions. The driver verifies the App's own
permission declaration before minting a read token, so using the writer key
for the read configuration is rejected. The validation job never receives the
writer App key or a token with repository write access.

The promotion job uses this repository's GitHub Actions token with Contents:
write and Actions: read. Branch protection and organizational rules must
permit that exact identity to fast-forward the configured default branch.
The workflow does not bypass protections, request administrative authority,
or force-push. If organizational policy prevents this, promotion remains held.

Package registries and official Go/Node toolchain downloads must be reachable
from hosted runners. Private modules must be readable through the read App.
Existing Dockerfiles that resolve private modules without a supported build
credential path can still fail; such failures hold the candidate rather than
passing credentials as Docker build arguments or bypassing the build.

Until both Apps and the Actions configuration are installed, generation and
candidate validation are explicitly held. A regular default-branch push can
still run its checks without App credentials when existing inputs are already
sufficient. These files do not configure secrets or prove a hosted run.

## Source advancement and release

A dependency commit is a source update. This workflow does not create release
tags, publish images/assets, rewrite Skill definition versions, or deploy
services. Existing tag/manual release validation is a separate boundary.
GitHub Actions token pushes do not trigger follow-on workflows, so correctness
does not depend on another push-triggered job: required candidate tests and
builds run before promotion within this workflow. Authorized release automation
must still publish its checked immutable artifacts through the repository's
existing release process before the runtime Skill updater can discover them.

The generated secret configuration stays at mode `0600` and is explicitly
owned by the pinned official image's non-root UID 12021. The hosted runner
needs non-interactive sudo for that ownership change; both validator and
generator retain the image's non-root user and a read-only configuration mount.
The pinned Renovate CLI configuration validator runs with `--strict` before
generation. Local verification covers the official pinned JSON schema and
offline promotion/credential fixtures; it does not claim that registry access,
private modules, real hosted image builds, or release publication succeeded.
Supported behavior is documented by [Renovate configuration](https://docs.renovatebot.com/configuration-options/),
[Renovate self-hosting](https://docs.renovatebot.com/self-hosted-configuration/),
[GitHub App installation tokens](https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/generating-an-installation-access-token-for-a-github-app),
and [GitHub Actions token behavior](https://docs.github.com/en/actions/concepts/security/github_token).
