# Splunk Skill

Splunk log analytics operations for Atlas agents. Search logs, manage saved searches, create alerts, and query indexes.

## Overview

The Splunk skill provides comprehensive node types for Splunk log analytics. It supports search operations, saved search management, alert creation, and index exploration.

## Node Types

### `splunk-search`

Execute a Splunk search query.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| host | string | Yes | Splunk host URL |
| token | string | Yes | Splunk API token |
| search | string | Yes | SPL search query |
| earliest | string | No | Earliest time (e.g., -24h) |
| latest | string | No | Latest time (e.g., now) |
| index | string | No | Index to search |
| maxCount | integer | No | Max results |

**Output:**

```json
{
  "results": [
    {
      "_time": "2024-01-15T10:30:00.000Z",
      "host": "web-server-01",
      "source": "/var/log/app.log",
      "message": "Error: Connection timeout",
      "level": "ERROR"
    }
  ],
  "count": 1
}
```

### `splunk-saved-search`

Run a saved search.

### `splunk-search-job-create`

Create a search job.

### `splunk-search-job-status`

Get search job status.

### `splunk-index-list`

List all indexes.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| host | string | Yes | Splunk host URL |
| token | string | Yes | Splunk API token |

### `splunk-alert-create`

Create an alert.

### `splunk-dashboard-list`

List dashboards.

### `splunk-event-type-list`

List event types.

## Authentication

Get API token from Splunk Settings > Tokens.

**Required capabilities:**
- `search` - Execute searches
- `saved_search` - Access saved searches
- `alert` - Create/manage alerts

## Usage Examples

```yaml
# Search logs
- type: splunk-search
  config:
    host: "https://splunk.example.com:8089"
    token: "{{secrets.splunk.token}}"
    search: "index=main sourcetype=app:error | head 100"
    earliest: "-24h"
    latest: "now"

# List indexes
- type: splunk-index-list
  config:
    host: "https://splunk.example.com:8089"
    token: "{{secrets.splunk.token}}"
```

## License

MIT License - See [LICENSE](LICENSE) for details.