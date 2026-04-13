# Fivetran Skill

Fivetran data pipeline operations for Atlas agents. Manage connectors, syncs, and destinations.

## Overview

The Fivetran skill provides comprehensive node types for Fivetran data pipelines. It supports connector management, sync operations, destination configuration, and schema management.

## Node Types

### `fivetran-connector-list`

List all connectors.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| apiKey | string | Yes | Fivetran API key |
| apiSecret | string | Yes | Fivetran API secret |
| groupId | string | No | Filter by group |

**Output:**

```json
{
  "connectors": [
    {
      "id": "abc123",
      "group_id": "grp_456",
      "service": "postgres",
      "name": "Production DB",
      "connected_by": "user@example.com",
      "succeeded_at": "2024-01-15T10:30:00.000Z"
    }
  ]
}
```

### `fivetran-connector-create`

Create a connector.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| apiKey | string | Yes | Fivetran API key |
| apiSecret | string | Yes | Fivetran API secret |
| groupId | string | Yes | Group ID |
| service | string | Yes | Service name |
| config | object | Yes | Connector configuration |
| paused | boolean | No | Pause connector |

### `fivetran-connector-sync`

Trigger a sync.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| apiKey | string | Yes | Fivetran API key |
| apiSecret | string | Yes | Fivetran API secret |
| connectorId | string | Yes | Connector ID |

**Output:**

```json
{
  "success": true,
  "code": "SyncQueued",
  "message": "Sync started for connector abc123"
}
```

### `fivetran-connector-status`

Get connector status.

### `fivetran-destination-list`

List destinations.

### `fivetran-schema-config`

Configure schema.

### `fivetran-group-list`

List groups.

## Authentication

Get API credentials from Fivetran Settings > API Keys.

## Usage Examples

```yaml
# Trigger sync
- type: fivetran-connector-sync
  config:
    apiKey: "{{secrets.fivetran.apiKey}}"
    apiSecret: "{{secrets.fivetran.apiSecret}}"
    connectorId: "abc123"

# List connectors
- type: fivetran-connector-list
  config:
    apiKey: "{{secrets.fivetran.apiKey}}"
    apiSecret: "{{secrets.fivetran.apiSecret}}"
```

## License

MIT License - See [LICENSE](LICENSE) for details.