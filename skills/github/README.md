# GitHub Skill

Governed GitHub repository, issue, pull-request, and Actions operations for
OpenSeal Agents. The `github_token` credential is supplied by the runtime and
never included in model-visible arguments or outputs. The skill uses explicit,
permissioned operations rather than an unrestricted GitHub API proxy.

Repository and source inspection:

- `github-repository-get`
- `github-repository-content-get`
- `github-branch-list`
- `github-commit-list`
- `github-branch-create` — create a branch from an inspected commit SHA
- `github-commit-create` — add/update/delete files, commit them together and publish without force-pushing

Issues and conversation:

- `github-issue-list`
- `github-issue-get`
- `github-issue-create`
- `github-issue-update`
- `github-issue-comments-list`
- `github-issue-comment-create`

Pull requests:

- `github-pull-request-list`
- `github-pull-request-get`
- `github-pull-request-files`
- `github-pull-request-create`
- `github-pull-request-update`
- `github-pull-request-comments-list`
- `github-pull-request-comment-create`
- `github-pull-request-reviews-list`
- `github-pull-request-review-create`
- `github-pull-request-checks-list`
- `github-pull-request-merge`

GitHub Actions:

- `github-workflow-list`
- `github-workflow-runs-list`
- `github-workflow-dispatch`

Bindings can narrow `owner` and `repository` with argument restrictions; the
connected GitHub account always enforces its own repository permissions. Writes are external side effects. Pull-request merge is marked
destructive and should always use the host's configured approval policy. Pass
the `headSha` returned by `github-pull-request-get` when merging so an Agent
cannot merge a revision it did not inspect.

Repository writing uses the GitHub Git data API, not sandbox Git. Commit publishing
requires `github:contents:write`, an explicit branch and `expectedHeadSha`. It
creates blobs, a complete tree and a single-parent commit, then advances the
branch with `force: false`. Concurrent head changes are rejected. Retries recognize
an already published commit only when its parent, message and resulting tree
match. File contents preserve whitespace and may use UTF-8 or base64; deletion
is explicit. Existing action grants are not silently expanded by upgrading.

Workspace Git is separate framework authority. A connected GitHub account enables
clone, local commit and push by default for repositories it can access. Configure
repository limits or disable workspace Git from that agent's GitHub configuration;
prompt requests use the reviewed `configure_workspace` action. Repository access
does not elevate GitHub token permissions, bypass branch protection or enable
arbitrary workspace commands.
