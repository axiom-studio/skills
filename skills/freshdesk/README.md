# Freshdesk Skill

Freshdesk helpdesk operations for Atlas agents. Manage tickets, agents, contacts, and solutions.

## Overview

The Freshdesk skill provides comprehensive node types for Freshdesk helpdesk operations. It supports ticket management, agent operations, contact management, and knowledge base operations.

## Node Types

### `freshdesk-ticket-list`

List tickets.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| apiKey | string | Yes | Freshdesk API key |
| domain | string | Yes | Freshdesk domain |
| status | string | No | Filter by status |
| priority | integer | No | Filter by priority |
| userId | string | No | Filter by requester |

**Output:**

```json
{
  "tickets": [
    {
      "id": 123,
      "subject": "Login issue",
      "description": "User cannot login",
      "status": 2,
      "priority": 1,
      "requester_id": 456,
      "created_at": "2024-01-15T10:30:00.000Z"
    }
  ]
}
```

### `freshdesk-ticket-create`

Create a ticket.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| apiKey | string | Yes | Freshdesk API key |
| domain | string | Yes | Freshdesk domain |
| subject | string | Yes | Ticket subject |
| description | string | Yes | Ticket description |
| email | string | Yes | Requester email |
| status | integer | No | Status (2=Open, 3=Pending, 4=Resolved) |
| priority | integer | No | Priority (1-4) |

### `freshdesk-ticket-update`

Update a ticket.

### `freshdesk-ticket-reply`

Reply to a ticket.

### `freshdesk-agent-list`

List agents.

### `freshdesk-contact-list`

List contacts.

### `freshdesk-solution-create`

Create a solution article.

### `freshdesk-article-list`

List solution articles.

## Authentication

Get API key from Freshdesk Profile Settings.

## Usage Examples

```yaml
# Create ticket
- type: freshdesk-ticket-create
  config:
    apiKey: "{{secrets.freshdesk.apiKey}}"
    domain: "mycompany"
    subject: "Password reset request"
    description: "User needs password reset"
    email: "user@example.com"
    priority: 2

# Reply to ticket
- type: freshdesk-ticket-reply
  config:
    apiKey: "{{secrets.freshdesk.apiKey}}"
    domain: "mycompany"
    ticketId: 123
    body: "We've reset your password. Check your email."
```

## License

MIT License - See [LICENSE](LICENSE) for details.