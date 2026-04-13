# Intercom Skill

Intercom customer messaging operations for Atlas agents. Manage conversations, users, messages, and tags.

## Overview

The Intercom skill provides comprehensive node types for Intercom customer messaging. It supports conversation management, user operations, message sending, and tag configuration.

## Node Types

### `intercom-conversation-list`

List conversations.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| apiToken | string | Yes | Intercom API token |
| type | string | No | Filter by type (open, closed, unassigned) |
| page | integer | No | Page number |
| perPage | integer | No | Results per page |

**Output:**

```json
{
  "conversations": [
    {
      "id": "123",
      "created_at": 1705312200,
      "updated_at": 1705312500,
      "state": "open",
      "user": {"id": "abc", "email": "user@example.com"},
      "assignee": {"id": "xyz", "name": "Support Agent"}
    }
  ],
  "total_count": 1
}
```

### `intercom-conversation-get`

Get conversation details.

### `intercom-conversation-reply`

Reply to a conversation.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| apiToken | string | Yes | Intercom API token |
| conversationId | string | Yes | Conversation ID |
| messageType | string | No | Message type (comment, note) |
| body | string | Yes | Message body |
| attachmentUrls | array | No | Attachment URLs |

### `intercom-user-list`

List users.

### `intercom-user-create`

Create a user.

### `intercom-message-send`

Send a message.

### `intercom-tag-create`

Create a tag.

### `intercom-tag-apply`

Apply tag to conversation/user.

## Authentication

Get API token from Intercom Settings > API keys.

## Usage Examples

```yaml
# Reply to conversation
- type: intercom-conversation-reply
  config:
    apiToken: "{{secrets.intercom.apiToken}}"
    conversationId: "123"
    messageType: "comment"
    body: "Thank you for contacting us. We'll look into this issue."

# Create user
- type: intercom-user-create
  config:
    apiToken: "{{secrets.intercom.apiToken}}"
    email: "user@example.com"
    name: "John Doe"
```

## License

MIT License - See [LICENSE](LICENSE) for details.