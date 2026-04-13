# SonarQube Skill

SonarQube code quality analysis for Atlas agents. Scan projects, get issues, check quality gates, and retrieve metrics.

## Overview

The SonarQube skill provides comprehensive node types for SonarQube code quality analysis. It supports project scanning, issue management, quality gate checks, and metrics retrieval.

## Node Types

### `sonar-project-list`

List all projects.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| url | string | Yes | SonarQube URL |
| token | string | Yes | SonarQube token |
| organization | string | No | Organization key |

**Output:**

```json
{
  "projects": [
    {
      "key": "my-project",
      "name": "My Project",
      "qualifier": "TRK",
      "visibility": "private"
    }
  ]
}
```

### `sonar-scan`

Trigger a project scan.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| url | string | Yes | SonarQube URL |
| token | string | Yes | SonarQube token |
| projectKey | string | Yes | Project key |
| branch | string | No | Branch name |

### `sonar-issues-list`

List issues.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| url | string | Yes | SonarQube URL |
| token | string | Yes | SonarQube token |
| projectKey | string | Yes | Project key |
| severities | array | No | Filter by severities |
| types | array | No | Filter by types |
| statuses | array | No | Filter by statuses |

### `sonar-issue-assign`

Assign an issue.

### `sonar-quality-gate`

Get quality gate status.

**Output:**

```json
{
  "projectKey": "my-project",
  "gateStatus": "OK",
  "conditions": [
    {
      "metric": "coverage",
      "status": "OK",
      "value": "85"
    }
  ]
}
```

### `sonar-measures`

Get project measures.

### `sonar-rules-list`

List rules.

### `sonar-branches-list`

List project branches.

## Authentication

Generate token at SonarQube > User Profile > Security.

## Usage Examples

```yaml
# Get quality gate
- type: sonar-quality-gate
  config:
    url: "https://sonarqube.example.com"
    token: "{{secrets.sonarqube.token}}"
    projectKey: "my-project"

# List issues
- type: sonar-issues-list
  config:
    url: "https://sonarqube.example.com"
    token: "{{secrets.sonarqube.token}}"
    projectKey: "my-project"
    severities: ["CRITICAL", "MAJOR"]
```

## License

MIT License - See [LICENSE](LICENSE) for details.