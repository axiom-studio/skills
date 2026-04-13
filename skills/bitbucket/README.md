# Bitbucket Skill

Bitbucket repository operations for Atlas agents. Manage repositories, pull requests, pipelines, and commits.

## Overview

The Bitbucket skill provides comprehensive node types for Bitbucket Git operations. It supports repository management, pull request workflows, pipeline triggers, and commit history.

## Node Types

### `bitbucket-repo-list`

List repositories.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| username | string | Yes | Bitbucket username |
| appPassword | string | Yes | App password |
| workspace | string | No | Workspace slug |
| role | string | No | Filter by role (member, admin) |

**Output:**

```json
{
  "repositories": [
    {
      "uuid": "{abc-123}",
      "name": "my-repo",
      "full_name": "workspace/my-repo",
      "is_private": true,
      "created_on": "2024-01-15T10:30:00.000Z"
    }
  ]
}
```

### `bitbucket-repo-get`

Get repository details.

### `bitbucket-pr-list`

List pull requests.

### `bitbucket-pr-create`

Create a pull request.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| username | string | Yes | Bitbucket username |
| appPassword | string | Yes | App password |
| workspace | string | Yes | Workspace slug |
| repoSlug | string | Yes | Repository slug |
| title | string | Yes | PR title |
| sourceBranch | string | Yes | Source branch |
| destinationBranch | string | Yes | Destination branch |
| description | string | No | PR description |

### `bitbucket-pr-merge`

Merge a pull request.

### `bitbucket-pipeline-trigger`

Trigger a pipeline.

### `bitbucket-pipeline-status`

Get pipeline status.

### `bitbucket-commit-list`

List commits.

### `bitbucket-branch-list`

List branches.

## Authentication

Create app password at Bitbucket Settings > App passwords.

**Required permissions:**
- `Repositories: Read` - Read repos
- `Repositories: Write` - Write repos
- `Pull requests: Read/Write` - PR operations
- `Pipelines: Read/Write` - Pipeline operations

## Usage Examples

```yaml
# Create PR
- type: bitbucket-pr-create
  config:
    username: "{{secrets.bitbucket.username}}"
    appPassword: "{{secrets.bitbucket.appPassword}}"
    workspace: "myworkspace"
    repoSlug: "my-repo"
    title: "Add new feature"
    sourceBranch: "feature/new"
    destinationBranch: "main"
    description: "Implements new feature X"

# Trigger pipeline
- type: bitbucket-pipeline-trigger
  config:
    username: "{{secrets.bitbucket.username}}"
    appPassword: "{{secrets.bitbucket.appPassword}}"
    workspace: "myworkspace"
    repoSlug: "my-repo"
    branch: "main"
```

## License

MIT License - See [LICENSE](LICENSE) for details.