# Postman Skill

Postman API testing operations for Atlas agents. Run collections, manage environments, and monitor APIs.

## Overview

The Postman skill provides comprehensive node types for Postman API testing. It supports collection runs, environment management, monitor execution, and API results retrieval.

## Node Types

### `postman-collection-run`

Run a Postman collection.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| apiKey | string | Yes | Postman API key |
| collectionId | string | Yes | Collection UID |
| environmentId | string | No | Environment UID |
| folder | string | No | Specific folder to run |
| delay | integer | No | Request delay (ms) |
| timeout | integer | No | Request timeout (ms) |
| iterationCount | integer | No | Number of iterations |

**Output:**

```json
{
  "success": true,
  "runId": "abc123",
  "stats": {
    "iterations": {"total": 1, "completed": 1},
    "items": {"total": 10, "completed": 10},
    "requests": {"total": 10, "completed": 10, "failed": 0},
    "tests": {"total": 50, "passed": 50, "failed": 0},
    "assertions": {"total": 50, "passed": 50, "failed": 0}
  },
  "failures": []
}
```

### `postman-collection-list`

List collections.

### `postman-collection-get`

Get collection details.

### `postman-environment-list`

List environments.

### `postman-environment-get`

Get environment details.

### `postman-monitor-run`

Run a monitor.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| apiKey | string | Yes | Postman API key |
| monitorId | string | Yes | Monitor UID |

### `postman-monitor-list`

List monitors.

### `postman-api-list`

List APIs.

## Authentication

Get API key from Postman Settings > API keys.

## Usage Examples

```yaml
# Run collection
- type: postman-collection-run
  config:
    apiKey: "{{secrets.postman.apiKey}}"
    collectionId: "abc123-def456"
    environmentId: "xyz789"
    iterationCount: 1

# Run monitor
- type: postman-monitor-run
  config:
    apiKey: "{{secrets.postman.apiKey}}"
    monitorId: "monitor123"
```

## License

MIT License - See [LICENSE](LICENSE) for details.