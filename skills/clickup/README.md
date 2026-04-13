# ClickUp Skill

ClickUp productivity platform operations for Atlas agents. Manage tasks, spaces, folders, lists, and time tracking.

## Overview

The ClickUp skill provides comprehensive node types for ClickUp productivity management. It supports task CRUD, space/folder/list operations, and time tracking.

## Node Types

### `clickup-task-list`

List tasks.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| apiToken | string | Yes | ClickUp API token |
| listId | string | No | Filter by list |
| assignee | string | No | Filter by assignee |
| status | string | No | Filter by status |
| limit | integer | No | Max results |

**Output:**

```json
{
  "tasks": [
    {
      "id": "abc123",
      "name": "Complete project",
      "status": {"status": "in progress"},
      "assignees": [{"id": "user1", "username": "john"}],
      "dueDate": "2024-01-20"
    }
  ]
}
```

### `clickup-task-create`

Create a task.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| apiToken | string | Yes | ClickUp API token |
| listId | string | Yes | List ID |
| name | string | Yes | Task name |
| description | string | No | Task description |
| status | string | No | Initial status |
| assignees | array | No | Assignee IDs |
| dueDate | string | No | Due date |
| priority | integer | No | Priority (1-4) |

### `clickup-task-update`

Update a task.

### `clickup-task-delete`

Delete a task.

### `clickup-space-list`

List spaces.

### `clickup-folder-list`

List folders.

### `clickup-list-list`

List lists.

### `clickup-time-track`

Track time on task.

### `clickup-comment-create`

Create a comment.

## Authentication

Get API token from ClickUp Settings > Apps > API Token.

## Usage Examples

```yaml
# Create task
- type: clickup-task-create
  config:
    apiToken: "{{secrets.clickup.apiToken}}"
    listId: "list123"
    name: "Review PR"
    description: "Review the new feature implementation"
    assignees: ["user1"]
    dueDate: "2024-01-20"
    priority: 2

# List tasks
- type: clickup-task-list
  config:
    apiToken: "{{secrets.clickup.apiToken}}"
    listId: "list123"
    status: "open"
```

## License

MIT License - See [LICENSE](LICENSE) for details.