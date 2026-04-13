# Zendesk Skill

Zendesk support ticket operations for Atlas agents. This skill enables automated ticket management, user operations, and macro execution within the Zendesk support platform.

## Features

- **Ticket Management**: Create, update, and search tickets
- **User Operations**: Get, create, update, and search users
- **Macro Operations**: List, get, and apply macros
- **View Operations**: List views and retrieve tickets from views

## Installation

### Prerequisites

- Go 1.25 or later
- Zendesk account with API access
- Zendesk API token

### Building

```bash
# Build for current platform
go build -o skill-zendesk .

# Build for Linux AMD64
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o skill-zendesk-linux-amd64 .

# Build for Linux ARM64
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o skill-zendesk-linux-arm64 .

# Build for macOS AMD64
CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -o skill-zendesk-darwin-amd64 .

# Build for macOS ARM64
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -o skill-zendesk-darwin-arm64 .
```

### Docker

```bash
# Build Docker image
docker build -t skill-zendesk .

# Run the skill
docker run -p 50053:50053 skill-zendesk
```

## Configuration

### Authentication

All operations require Zendesk API credentials:

- **Subdomain**: Your Zendesk subdomain (e.g., `company` for `company.zendesk.com`)
- **Email**: The API email address
- **API Token**: Your Zendesk API token

### Getting API Token

1. Log in to your Zendesk admin panel
2. Go to Admin > Channels > API
3. Enable Token Access
4. Click "Add API token"
5. Copy and securely store the token

## Node Types

### zendesk-ticket-create

Create a new support ticket in Zendesk.

**Configuration:**
- `subdomain`: Zendesk subdomain
- `email`: API email
- `apiToken`: API token
- `subject`: Ticket subject
- `description`: Ticket description
- `priority`: low, normal, high, urgent
- `status`: new, open, pending, hold, solved, closed
- `type`: question, incident, problem, task
- `requesterId`: Existing user ID (optional)
- `requester`: New requester details {name, email}
- `tags`: Array of tags
- `customFields`: Array of {id, value} objects

**Output:**
```json
{
  "ticket": {
    "id": 12345,
    "subject": "Issue with login",
    "status": "new",
    ...
  }
}
```

### zendesk-ticket-update

Update an existing Zendesk ticket.

**Configuration:**
- `subdomain`: Zendesk subdomain
- `email`: API email
- `apiToken`: API token
- `ticketId`: Ticket ID to update
- `subject`: New subject (optional)
- `description`: New description (optional)
- `comment`: Add a comment (optional)
- `priority`: New priority
- `status`: New status
- `type`: New type
- `assigneeId`: Assign to user ID
- `tags`: Replace existing tags
- `customFields`: Custom field values

**Output:**
```json
{
  "ticket": {
    "id": 12345,
    "updated_at": "2024-01-15T10:30:00Z",
    ...
  }
}
```

### zendesk-ticket-search

Search for tickets using Zendesk search syntax.

**Configuration:**
- `subdomain`: Zendesk subdomain
- `email`: API email
- `apiToken`: API token
- `query`: Search query (Zendesk syntax)
- `status`: Filter by status
- `type`: Filter by type
- `priority`: Filter by priority
- `assignee`: Filter by assignee email/ID
- `requester`: Filter by requester email/ID
- `tags`: Filter by tags
- `page`: Page number (default: 1)
- `perPage`: Results per page (max: 100)

**Search Query Examples:**
- `status:open type:question`
- `priority:urgent created>2024-01-01`
- `tags:bug status<closed`

**Output:**
```json
{
  "results": [...],
  "count": 25,
  "next_page": "...",
  "previous_page": "..."
}
```

### zendesk-user

Manage Zendesk users (get, create, update, search, list).

**Configuration:**
- `subdomain`: Zendesk subdomain
- `email`: API email
- `apiToken`: API token
- `operation`: get, create, update, search, list
- `userId`: User ID for get/update
- `name`: User name for create/update
- `userEmail`: User email for create/update/search
- `role`: end-user, agent, admin
- `phone`: Phone number
- `notes`: User notes
- `tags`: User tags

**Output:**
```json
{
  "user": {
    "id": 12345,
    "name": "John Doe",
    "email": "john@example.com",
    ...
  }
}
```

### zendesk-macro

List, get, and apply Zendesk macros.

**Configuration:**
- `subdomain`: Zendesk subdomain
- `email`: API email
- `apiToken`: API token
- `operation`: list, get, apply
- `macroId`: Macro ID for get/apply
- `ticketId`: Ticket ID for apply operation

**Output:**
```json
{
  "macro": {
    "id": 123,
    "title": "Close and Tag",
    "actions": [...],
    ...
  }
}
```

### zendesk-view

List views and get tickets from views.

**Configuration:**
- `subdomain`: Zendesk subdomain
- `email`: API email
- `apiToken`: API token
- `operation`: list, get, tickets
- `viewId`: View ID for get/tickets
- `page`: Page number for tickets

**Output:**
```json
{
  "view": {
    "id": 456,
    "title": "Unassigned Tickets",
    ...
  },
  "tickets": [...]
}
```

## Example Workflows

### Auto-assign High Priority Tickets

```yaml
nodes:
  - id: search-high-priority
    type: zendesk-ticket-search
    config:
      subdomain: mycompany
      email: bot@mycompany.com
      apiToken: "{{secrets.zendesk_token}}"
      priority: urgent
      status: new
  
  - id: assign-tickets
    type: zendesk-ticket-update
    config:
      subdomain: mycompany
      email: bot@mycompany.com
      apiToken: "{{secrets.zendesk_token}}"
      ticketId: "{{bindings.search-high-priority.output.results[0].ticket.id}}"
      assigneeId: 98765
      status: open
```

### Create Ticket from Form Submission

```yaml
nodes:
  - id: create-ticket
    type: zendesk-ticket-create
    config:
      subdomain: mycompany
      email: bot@mycompany.com
      apiToken: "{{secrets.zendesk_token}}"
      subject: "{{bindings.form.subject}}"
      description: "{{bindings.form.description}}"
      priority: normal
      requester:
        name: "{{bindings.form.name}}"
        email: "{{bindings.form.email}}"
      tags:
        - web-form
        - "{{bindings.form.category}}"
```

### Apply Macro to Solved Ticket

```yaml
nodes:
  - id: apply-macro
    type: zendesk-macro
    config:
      subdomain: mycompany
      email: bot@mycompany.com
      apiToken: "{{secrets.zendesk_token}}"
      operation: apply
      macroId: 12345
      ticketId: "{{bindings.ticket_id}}"
```

## Security

- Store API tokens securely using the secrets manager
- Use minimal required permissions for API tokens
- Rotate tokens periodically
- Monitor API usage in Zendesk admin panel

## Error Handling

The skill returns detailed error messages for:
- Authentication failures
- Invalid ticket/user IDs
- API rate limiting
- Network errors

Check the `error` field in the output for troubleshooting.

## Rate Limits

Zendesk API has rate limits:
- Standard: 200 requests/minute
- Professional: 400 requests/minute
- Enterprise: 700 requests/minute

Implement retry logic with exponential backoff for rate limit errors.

## License

MIT

## Support

For issues and feature requests, please open an issue in the repository.
