# Monday.com Skill

Monday.com work management operations for Atlas agents. Manage boards, items, columns, and groups.

## Overview

The Monday.com skill provides comprehensive node types for Monday.com work management. It supports board operations, item CRUD, column management, and GraphQL queries.

## Node Types

### `monday-board-list`

List all boards.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| apiToken | string | Yes | Monday.com API token |
| boardIds | array | No | Filter by board IDs |
| limit | integer | No | Max results |

**Output:**

```json
{
  "boards": [
    {
      "id": 123,
      "name": "Project Tracker",
      "description": "Main project tracking board",
      "workspace_id": 456,
      "owners": [{"id": 1, "name": "John"}]
    }
  ]
}
```

### `monday-board-get`

Get board details.

### `monday-item-list`

List items on a board.

### `monday-item-create`

Create an item.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| apiToken | string | Yes | Monday.com API token |
| boardId | integer | Yes | Board ID |
| name | string | Yes | Item name |
| groupId | string | No | Group ID |
| columnValues | object | No | Column values |

### `monday-item-update`

Update an item.

### `monday-column-list`

List board columns.

### `monday-group-list`

List board groups.

### `monday-query`

Execute GraphQL query.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| apiToken | string | Yes | Monday.com API token |
| query | string | Yes | GraphQL query |
| variables | object | No | Query variables |

## Authentication

Get API token from Monday.com > Profile > Developer > Personal API Token.

## Usage Examples

```yaml
# Create item
- type: monday-item-create
  config:
    apiToken: "{{secrets.monday.apiToken}}"
    boardId: 123
    name: "New Task"
    columnValues:
      status: {"label": "Working on it"}
      priority: {"label": "High"}

# GraphQL query
- type: monday-query
  config:
    apiToken: "{{secrets.monday.apiToken}}"
    query: "query { boards (limit: 5) { id name } }"
```

## License

MIT License - See [LICENSE](LICENSE) for details.