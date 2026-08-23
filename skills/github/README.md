# GitHub Skill

Governed GitHub repository metadata and pull-request operations for OpenSeal
Agents. The `github_token` credential is supplied by the runtime and never
included in model-visible arguments or outputs.

Actions:

- `github-repository-get`
- `github-pull-request-list`
- `github-pull-request-create`

Deployments should narrow `owner` and `repository` with binding argument
restrictions. Creating a pull request is an external side effect and should use
the host's configured approval policy.
