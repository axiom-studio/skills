# Snyk Skill

Snyk security vulnerability scanning for Atlas agents. Scan code, containers, and infrastructure for vulnerabilities.

## Overview

The Snyk skill provides comprehensive node types for Snyk security scanning. It supports project scanning, vulnerability management, dependency testing, and monitoring.

## Node Types

### `snyk-project-list`

List all Snyk projects.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| apiToken | string | Yes | Snyk API token |
| orgId | string | No | Organization ID |

**Output:**

```json
{
  "projects": [
    {
      "id": "abc123",
      "name": "my-app",
      "type": "npm",
      "created": "2024-01-15T10:30:00.000Z",
      "origin": "github"
    }
  ]
}
```

### `snyk-project-scan`

Trigger a vulnerability scan.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| apiToken | string | Yes | Snyk API token |
| projectId | string | Yes | Project ID |

### `snyk-issue-list`

List vulnerabilities/issues.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| apiToken | string | Yes | Snyk API token |
| projectId | string | Yes | Project ID |
| severity | string | No | Filter by severity |

### `snyk-issue-ignore`

Ignore a vulnerability.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| apiToken | string | Yes | Snyk API token |
| projectId | string | Yes | Project ID |
| issueId | string | Yes | Issue ID |
| reason | string | Yes | Ignore reason |
| expires | string | No | Expiry date |

### `snyk-test-deps`

Test dependencies for vulnerabilities.

### `snyk-monitor`

Monitor project for new vulnerabilities.

### `snyk-severity-list`

List issues by severity.

### `snyk-license-list`

List license violations.

## Authentication

Get API token from Snyk Settings > API Tokens.

## Usage Examples

```yaml
# List projects
- type: snyk-project-list
  config:
    apiToken: "{{secrets.snyk.apiToken}}"

# Scan project
- type: snyk-project-scan
  config:
    apiToken: "{{secrets.snyk.apiToken}}"
    projectId: "abc123"

# List vulnerabilities
- type: snyk-issue-list
  config:
    apiToken: "{{secrets.snyk.apiToken}}"
    projectId: "abc123"
    severity: "high"
```

## License

MIT License - See [LICENSE](LICENSE) for details.