# Microsoft Teams Skill for Axiom Atlas

Microsoft Teams messaging operations for Atlas agents. Send messages, adaptive cards, manage channels, meetings, and users.

## Features

- **Send Messages**: Send messages to Teams channels via incoming webhooks
- **Send Adaptive Cards**: Create and send rich adaptive cards with sections, facts, and actions
- **Channel Management**: Create, list, get, update, and delete Teams channels
- **Meeting Operations**: Create, get, cancel meetings and manage attendees
- **User Operations**: Get user info, send chat messages, install apps for users

## Installation

### From Source

```bash
# Clone the repository
git clone <repository-url>
cd skills.skill-teams

# Build for your platform
go build -o skill-teams .

# Or build for all platforms
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o skill-teams-linux-amd64 .
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o skill-teams-linux-arm64 .
CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -o skill-teams-darwin-amd64 .
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -o skill-teams-darwin-arm64 .
```

### Using Docker

```bash
docker build -t axiom-studio/skills.skill-teams .
docker run -p 50054:50054 axiom-studio/skills.skill-teams
```

## Configuration

### Required Permissions

For webhook-based operations (teams-send, teams-card):
- **Incoming Webhook**: Configure an incoming webhook in your Teams channel

For Graph API operations (teams-channel, teams-meeting, teams-user):
- **Channel.ReadBasic.All**: Read channel information
- **Channel.Create**: Create channels
- **Channel.Update**: Update channels
- **Channel.Delete**: Delete channels
- **Calendars.ReadWrite**: Create and manage meetings
- **User.Read**: Read user information
- **Chat.ReadWrite**: Send chat messages
- **TeamsAppInstallation.ReadWriteForUser**: Install apps for users

### Secrets

Store your credentials securely using Atlas secrets:

```yaml
# In your Atlas workflow
{{secrets.teams_webhook}}
{{secrets.teams_tenant_id}}
{{secrets.teams_client_id}}
{{secrets.teams_client_secret}}
```

### Azure AD App Registration

For Graph API operations, you need to register an application in Azure AD:

1. Go to Azure Portal > Azure Active Directory > App registrations
2. Create a new registration
3. Add API permissions for Microsoft Graph (see Required Permissions above)
4. Create a client secret
5. Note the Application (client) ID and Directory (tenant) ID

## Node Types

### teams-send

Send a message to a Microsoft Teams channel via incoming webhook.

**Configuration:**
- `webhookUrl`: Microsoft Teams incoming webhook URL (required)
- `message`: Message content to send (required)
- `themeColor`: Theme color for the message card in hex format (optional, default: #6264A7)
- `summary`: Card summary for accessibility (optional)
- `mentionUsers`: Comma-separated user IDs to mention (optional)

**Output:**
- `webhookUrl`: Webhook URL used
- `message`: Message content sent
- `success`: Boolean indicating success

### teams-card

Send a rich adaptive card to Microsoft Teams.

**Configuration:**
- `webhookUrl`: Microsoft Teams incoming webhook URL (required)
- `title`: Card title (required)
- `text`: Card text content (required)
- `themeColor`: Theme color in hex format (optional, default: #6264A7)
- `summary`: Card summary for accessibility (optional)
- `sections`: Array of section objects with title, text, facts (optional)
- `actions`: Array of action buttons (optional)
- `potentialAction`: Array of potential actions (optional)

**Output:**
- `webhookUrl`: Webhook URL used
- `title`: Card title
- `success`: Boolean indicating success

### teams-channel

Manage Microsoft Teams channels (list, get, create, update, delete).

**Configuration:**
- `tenantId`: Azure AD tenant ID (required)
- `clientId`: Azure AD application client ID (required)
- `clientSecret`: Azure AD application client secret (required)
- `teamId`: Team ID containing channels (required)
- `action`: Action to perform - "list", "get", "create", "update", "delete" (required)
- `channelId`: Channel ID (required for get/update/delete)
- `displayName`: Channel display name (required for create)
- `description`: Channel description (optional)
- `membershipType`: Channel type - "standard", "private", "shared" (optional, default: standard)

**Output:**
- `action`: Action performed
- `channel`: Channel data
- `success`: Boolean indicating success

### teams-meeting

Manage Microsoft Teams meetings (create, get, cancel, attendees).

**Configuration:**
- `tenantId`: Azure AD tenant ID (required)
- `clientId`: Azure AD application client ID (required)
- `clientSecret`: Azure AD application client secret (required)
- `action`: Action to perform - "create", "get", "cancel", "attendees" (required)
- `meetingId`: Meeting ID (required for get/cancel/attendees)
- `subject`: Meeting subject (required for create)
- `startTime`: Meeting start time in ISO 8601 format (required for create)
- `endTime`: Meeting end time in ISO 8601 format (required for create)
- `content`: Meeting content/body (optional)
- `attendees`: Comma-separated attendee emails (optional)
- `isOnline`: Create as online meeting (optional, default: true)

**Output:**
- `action`: Action performed
- `meeting`: Meeting data
- `success`: Boolean indicating success

### teams-user

Manage Microsoft Teams users (get, list, chat, install-app).

**Configuration:**
- `tenantId`: Azure AD tenant ID (required)
- `clientId`: Azure AD application client ID (required)
- `clientSecret`: Azure AD application client secret (required)
- `action`: Action to perform - "get", "list", "chat", "install-app" (required)
- `userId`: User ID or email (required for get/chat/install-app)
- `message`: Chat message content (required for chat)
- `appId`: App ID to install (required for install-app)

**Output:**
- `action`: Action performed
- `user`: User data
- `success`: Boolean indicating success

## Example Workflows

### Send Simple Message

```yaml
nodes:
  - type: teams-send
    config:
      webhookUrl: "{{secrets.teams_webhook}}"
      message: "Hello from Atlas! This is an automated notification."
      themeColor: "#6264A7"
```

### Send Alert Card

```yaml
nodes:
  - type: teams-card
    config:
      webhookUrl: "{{secrets.teams_webhook}}"
      title: "Deployment Alert"
      text: "A new deployment has been completed."
      themeColor: "#FF0000"
      summary: "Deployment completed"
      sections:
        - title: "Deployment Details"
          facts:
            - name: "Environment"
              value: "Production"
            - name: "Version"
              value: "1.2.3"
            - name: "Status"
              value: "Success"
      actions:
        - "@type": "OpenUri"
          name: "View Dashboard"
          target: "https://dashboard.example.com"
```

### Create Channel

```yaml
nodes:
  - type: teams-channel
    config:
      tenantId: "{{secrets.teams_tenant_id}}"
      clientId: "{{secrets.teams_client_id}}"
      clientSecret: "{{secrets.teams_client_secret}}"
      teamId: "team-id-here"
      action: "create"
      displayName: "Project Alpha"
      description: "Discussion channel for Project Alpha"
      membershipType: "standard"
```

### Schedule Meeting

```yaml
nodes:
  - type: teams-meeting
    config:
      tenantId: "{{secrets.teams_tenant_id}}"
      clientId: "{{secrets.teams_client_id}}"
      clientSecret: "{{secrets.teams_client_secret}}"
      action: "create"
      subject: "Weekly Standup"
      startTime: "2024-01-15T10:00:00"
      endTime: "2024-01-15T10:30:00"
      content: "Weekly team standup meeting"
      attendees: "user1@example.com,user2@example.com"
      isOnline: true
```

### Send Direct Chat

```yaml
nodes:
  - type: teams-user
    config:
      tenantId: "{{secrets.teams_tenant_id}}"
      clientId: "{{secrets.teams_client_id}}"
      clientSecret: "{{secrets.teams_client_secret}}"
      action: "chat"
      userId: "user-id-or-email"
      message: "Hi! You have a new task assigned."
```

## Development

### Building

```bash
# Build for current platform
go build -o skill-teams .

# Build for all platforms
make build-all
```

### Testing

```bash
# Run tests
go test ./...

# Run with race detector
go test -race ./...
```

### Running Locally

```bash
# Set environment variables
export SKILL_PORT=50054

# Run the skill
go run main.go
```

## API Reference

### Microsoft Graph API

This skill uses the Microsoft Graph API for most operations:
- Base URL: `https://graph.microsoft.com/v1.0`
- Documentation: https://docs.microsoft.com/en-us/graph/api/overview

### Teams Incoming Webhooks

For simple message sending, use incoming webhooks:
- Documentation: https://docs.microsoft.com/en-us/microsoftteams/platform/webhooks-and-connectors/how-to/add-incoming-webhook

## Troubleshooting

### Common Issues

1. **401 Unauthorized**: Check your client secret and ensure the app registration has correct permissions
2. **403 Forbidden**: Ensure admin consent has been granted for the API permissions
3. **404 Not Found**: Verify the team/channel/user IDs are correct
4. **Webhook failures**: Ensure the webhook URL is valid and the connector is enabled

### Debug Mode

Enable debug logging to see detailed API requests:

```bash
export DEBUG=true
go run main.go
```

## License

MIT License - see LICENSE file for details.

## Support

For issues and feature requests, please open an issue on GitHub.

**Author:** Axiom Studio
**Email:** engineering@axiomstudio.ai
