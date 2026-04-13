# Trello Skill

Trello board management operations for Atlas agents. Manage boards, cards, lists, labels, and members.

## Overview

The Trello skill provides comprehensive node types for Trello board management. It supports board operations, card CRUD, list management, label operations, and member management.

## Node Types

### `trello-board-list`

List all boards.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| apiKey | string | Yes | Trello API key |
| token | string | Yes | Trello token |
| filter | string | No | Filter (members, all) |

**Output:**

```json
{
  "boards": [
    {
      "id": "abc123",
      "name": "Project Board",
      "desc": "Main project tracking",
      "closed": false,
      "url": "https://trello.com/b/abc123"
    }
  ]
}
```

### `trello-board-get`

Get board details.

### `trello-card-list`

List cards on a board/list.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| apiKey | string | Yes | Trello API key |
| token | string | Yes | Trello token |
| boardId | string | Yes | Board ID |
| listId | string | No | Filter by list |

### `trello-card-create`

Create a card.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| apiKey | string | Yes | Trello API key |
| token | string | Yes | Trello token |
| listId | string | Yes | List ID |
| name | string | Yes | Card name |
| description | string | No | Card description |
| dueDate | string | No | Due date |
| labels | array | No | Label IDs |

### `trello-card-update`

Update a card.

### `trello-card-move`

Move a card to another list.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| apiKey | string | Yes | Trello API key |
| token | string | Yes | Trello token |
| cardId | string | Yes | Card ID |
| listId | string | Yes | Target list ID |
| position | integer | No | Position in list |

### `trello-list-list`

List board lists.

### `trello-label-add`

Add label to card.

### `trello-member-add`

Add member to board.

## Authentication

Get API key and token from Trello Developer page.

## Usage Examples

```yaml
# Create card
- type: trello-card-create
  config:
    apiKey: "{{secrets.trello.apiKey}}"
    token: "{{secrets.trello.token}}"
    listId: "list123"
    name: "Implement feature X"
    description: "Add new feature to the product"
    dueDate: "2024-01-20"

# Move card
- type: trello-card-move
  config:
    apiKey: "{{secrets.trello.apiKey}}"
    token: "{{secrets.trello.token}}"
    cardId: "card456"
    listId: "list789"
    position: 0
```

## License

MIT License - See [LICENSE](LICENSE) for details.