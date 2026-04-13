# Asana Skill

Asana project management operations for Atlas agents. Manage tasks, projects, sections, and stories.

## Overview

The Asana skill provides comprehensive node types for Asana project management. It supports task CRUD operations, project management, section organization, and activity tracking.

## Node Types

### `asana-task-list`

List tasks.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| token | string | Yes | Asana personal access token |
| projectId | string | No | Filter by project |
| assignee | string | No | Filter by assignee |
| completed | boolean | No | Filter by completion |
| limit | integer | No | Max results |

**Output:**

```json
{
  "tasks": [
    {
      "gid": "123456",
      "name": "Complete project proposal",
      "completed": false,
      "dueOn": "2024-01-20",
      "assignee": {"gid": "789", "name": "John Doe"}
    }
  ]
}
```

### `asana-task-create`

Create a task.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| token | string | Yes | Asana personal access token |
| name | string | Yes | Task name |
| notes | string | No | Task description |
| projectId | string | No | Add to project |
| assignee | string | No | Assign to user |
| dueOn | string | No | Due date (YYYY-MM-DD) |
| tags | array | No | Tags to add |

### `asana-task-update`

Update a task.

### `asana-task-delete`

Delete a task.

### `asana-project-list`

List projects.

### `asana-project-get`

Get project details.

### `asana-section-list`

List sections in a project.

### `asana-story-create`

Create a story/comment.

### `asana-subtask-list`

List subtasks.

### `asana-tag-list`

List tags.

## Authentication

Create token at Asana > My Profile Settings > Apps > Manage Developer Apps.

## Usage Examples

```yaml
# Create task
- type: asana-task-create
  config:
    token: "{{secrets.asana.token}}"
    name: "Review pull request"
    notes: "Review the new API implementation"
    projectId: "123456"
    assignee: "789012"
    dueOn: "2024-01-20"

# List incomplete tasks
- type: asana-task-list
  config:
    token: "{{secrets.asana.token}}"
    projectId: "123456"
    completed: false
```

## License

MIT License - See [LICENSE](LICENSE) for details.