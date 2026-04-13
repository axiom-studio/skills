# Grafana Skill

Grafana visualization operations for Atlas agents. Manage dashboards, datasources, annotations, and alert rules.

## Overview

The Grafana skill provides comprehensive node types for Grafana visualization and monitoring. It supports dashboard management, datasource configuration, annotations, and alert rule operations.

## Node Types

### `grafana-dashboard-list`

List all dashboards.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| url | string | Yes | Grafana URL |
| apiKey | string | Yes | Grafana API key |
| folderId | integer | No | Filter by folder |
| tag | array | No | Filter by tags |

**Output:**

```json
{
  "dashboards": [
    {
      "id": 1,
      "uid": "abc123",
      "title": "System Metrics",
      "uri": "/d/abc123/system-metrics",
      "folderTitle": "Infrastructure"
    }
  ]
}
```

### `grafana-dashboard-create`

Create a new dashboard.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| url | string | Yes | Grafana URL |
| apiKey | string | Yes | Grafana API key |
| title | string | Yes | Dashboard title |
| panels | array | No | Panel configurations |
| templating | object | No | Template variables |
| time | object | No | Default time range |
| refresh | string | No | Auto-refresh interval |

**Output:**

```json
{
  "success": true,
  "id": 123,
  "uid": "new-dash-uid",
  "url": "/d/new-dash-uid/system-metrics",
  "message": "Dashboard created"
}
```

### `grafana-dashboard-update`

Update a dashboard.

### `grafana-dashboard-delete`

Delete a dashboard.

### `grafana-datasource-list`

List datasources.

### `grafana-datasource-create`

Create a datasource.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| url | string | Yes | Grafana URL |
| apiKey | string | Yes | Grafana API key |
| name | string | Yes | Datasource name |
| type | string | Yes | Type (prometheus, influxdb, etc.) |
| url | string | Yes | Datasource URL |
| access | string | No | Access mode (proxy, direct) |
| jsonData | object | No | Type-specific config |
| secureJsonData | object | No | Sensitive config |

### `grafana-annotation-create`

Create an annotation.

### `grafana-alert-rule-list`

List alert rules.

### `grafana-alert-rule-create`

Create an alert rule.

## Authentication

Create API key at Grafana > Configuration > API Keys.

**Required permissions:**
- `Admin` - Full access
- `Editor` - Create/edit dashboards
- `Viewer` - Read-only access

## Usage Examples

```yaml
# Create dashboard
- type: grafana-dashboard-create
  config:
    url: "https://grafana.example.com"
    apiKey: "{{secrets.grafana.apiKey}}"
    title: "Application Metrics"
    panels:
      - title: "Request Rate"
        type: "graph"
        targets:
          - expr: "rate(http_requests_total[5m])"
    refresh: "30s"

# Create datasource
- type: grafana-datasource-create
  config:
    url: "https://grafana.example.com"
    apiKey: "{{secrets.grafana.apiKey}}"
    name: "Prometheus"
    type: "prometheus"
    url: "http://prometheus:9090"
    access: "proxy"
```

## License

MIT License - See [LICENSE](LICENSE) for details.