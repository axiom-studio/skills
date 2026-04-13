# Airbyte Skill

Airbyte data integration operations for Atlas agents. Manage sources, destinations, connections, and sync jobs.

## Overview

The Airbyte skill provides comprehensive node types for Airbyte data integration. It supports source/destination configuration, connection management, sync triggering, and job monitoring.

## Node Types

### `airbyte-source-list`

List all sources.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| apiToken | string | Yes | Airbyte API token |
| workspaceId | string | Yes | Workspace ID |

### `airbyte-source-create`

Create a source.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| apiToken | string | Yes | Airbyte API token |
| workspaceId | string | Yes | Workspace ID |
| name | string | Yes | Source name |
| sourceDefinitionId | string | Yes | Source definition ID |
| connectionConfiguration | object | Yes | Source-specific config |

### `airbyte-destination-list`

List destinations.

### `airbyte-destination-create`

Create a destination.

### `airbyte-connection-list`

List connections.

### `airbyte-connection-create`

Create a connection.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| apiToken | string | Yes | Airbyte API token |
| workspaceId | string | Yes | Workspace ID |
| sourceId | string | Yes | Source ID |
| destinationId | string | Yes | Destination ID |
| syncSchedule | object | No | Sync schedule |
| configurations | object | No | Stream configurations |

### `airbyte-connection-sync`

Trigger a sync.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| apiToken | string | Yes | Airbyte API token |
| connectionId | string | Yes | Connection ID |

**Output:**

```json
{
  "success": true,
  "jobId": "job-abc123",
  "status": "pending",
  "message": "Sync job triggered successfully"
}
```

### `airbyte-job-status`

Get job status.

### `airbyte-job-list`

List jobs.

## Authentication

Get API token from Airbyte Settings > API.

## Usage Examples

```yaml
# Create source
- type: airbyte-source-create
  config:
    apiToken: "{{secrets.airbyte.apiToken}}"
    workspaceId: "ws-123"
    name: "Postgres DB"
    sourceDefinitionId: "postgres"
    connectionConfiguration:
      host: "db.example.com"
      port: 5432
      database: "mydb"
      username: "user"
      password: "{{secrets.db.password}}"

# Trigger sync
- type: airbyte-connection-sync
  config:
    apiToken: "{{secrets.airbyte.apiToken}}"
    connectionId: "conn-abc123"
```

## License

MIT License - See [LICENSE](LICENSE) for details.