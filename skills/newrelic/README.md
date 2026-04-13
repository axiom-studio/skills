# New Relic Skill

New Relic observability operations for Atlas agents. Query NRQL, manage deployments, alerts, and dashboards.

## Overview

The New Relic skill provides comprehensive node types for New Relic observability platform. It supports NRQL queries, deployment markers, alert policies, dashboard management, and Apdex scores.

## Node Types

### `newrelic-query-nrql`

Execute an NRQL query.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| apiKey | string | Yes | New Relic API key |
| accountId | string | Yes | Account ID |
| query | string | Yes | NRQL query |

**Output:**

```json
{
  "results": [
    {
      "time": "2024-01-15T10:30:00.000Z",
      "average(duration)": 0.125,
      "count()": 1000
    }
  ],
  "metadata": {
    "eventCount": 1000
  }
}
```

### `newrelic-deploy-marker`

Create a deployment marker.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| apiKey | string | Yes | New Relic API key |
| accountId | string | Yes | Account ID |
| applicationId | string | Yes | Application ID |
| revision | string | Yes | Deployment revision |
| description | string | No | Description |
| user | string | No | Deployer name |

### `newrelic-alert-policy-list`

List alert policies.

### `newrelic-alert-policy-create`

Create an alert policy.

### `newrelic-dashboard-list`

List dashboards.

### `newrelic-dashboard-create`

Create a dashboard.

### `newrelic-apdex`

Get Apdex score.

### `newrelic-application-list`

List applications.

## Authentication

Get API key from New Relic Settings > API keys.

**Required permissions:**
- `User key` or `REST API key` - For queries
- `Admin key` - For creating resources

## Usage Examples

```yaml
# Query metrics
- type: newrelic-query-nrql
  config:
    apiKey: "{{secrets.newrelic.apiKey}}"
    accountId: "123456"
    query: "SELECT average(duration), count(*) FROM Transaction WHERE appName = 'MyApp' SINCE 1 hour ago"

# Create deployment marker
- type: newrelic-deploy-marker
  config:
    apiKey: "{{secrets.newrelic.apiKey}}"
    accountId: "123456"
    applicationId: "789012"
    revision: "v1.2.3"
    description: "Production deployment"
    user: "CI/CD Pipeline"
```

## License

MIT License - See [LICENSE](LICENSE) for details.