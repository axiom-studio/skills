# CircleCI Skill

CircleCI pipeline operations for Atlas agents. Manage projects, pipelines, workflows, jobs, and contexts.

## Overview

The CircleCI skill provides comprehensive node types for CircleCI CI/CD automation. It supports pipeline triggering, workflow monitoring, job log retrieval, and context management.

## Node Types

### `circleci-project-list`

List all projects.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| token | string | Yes | CircleCI API token |
| orgSlug | string | No | Organization slug filter |
| limit | integer | No | Max projects to return |

### `circleci-pipeline-trigger`

Trigger a new pipeline.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| token | string | Yes | CircleCI API token |
| projectSlug | string | Yes | Project slug (gh/owner/repo) |
| branch | string | Yes | Branch name |
| parameters | object | No | Pipeline parameters |
| tag | string | No | Git tag |

**Output:**

```json
{
  "success": true,
  "pipelineId": "abc123",
  "pipelineNumber": 42,
  "webUrl": "https://app.circleci.com/pipelines/gh/owner/repo/42"
}
```

### `circleci-pipeline-list`

List pipelines for a project.

### `circleci-pipeline-status`

Get pipeline status.

### `circleci-workflow-list`

List workflows in a pipeline.

### `circleci-workflow-status`

Get workflow status.

### `circleci-job-list`

List jobs in a workflow.

### `circleci-job-logs`

Get job logs.

### `circleci-context-list`

List contexts.

### `circleci-context-set-env`

Set environment variable in context.

## Authentication

Create a token at CircleCI > User Settings > API Tokens.

## Usage Examples

```yaml
# Trigger pipeline
- type: circleci-pipeline-trigger
  config:
    token: "{{secrets.circleci.token}}"
    projectSlug: "gh/myorg/myrepo"
    branch: "main"
    parameters:
      deploy_environment: "production"
```

## License

MIT License - See [LICENSE](LICENSE) for details.