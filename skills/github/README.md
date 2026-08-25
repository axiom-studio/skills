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

Deployments should narrow `owner` and `repository` with binding argument
restrictions. Writes are external side effects. Pull-request merge is marked
destructive and should always use the host's configured approval policy. Pass
the `headSha` returned by `github-pull-request-get` when merging so an Agent
cannot merge a revision it did not inspect.
