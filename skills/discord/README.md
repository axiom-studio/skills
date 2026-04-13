# Discord Skill for Axiom Atlas

Discord messaging operations for Atlas agents. Send messages, embeds, reactions, manage channels, users, and webhooks.

## Features

- **Send Messages**: Send text messages to Discord channels with TTS support
- **Send Embeds**: Create rich embed messages with titles, descriptions, fields, images, and more
- **Add Reactions**: React to messages with unicode or custom emojis
- **Channel Management**: Create, list, get, update, and delete channels
- **User Management**: Get user info, kick, ban, timeout, and manage roles
- **Webhook Support**: Send messages via Discord webhooks

## Installation

### From Source

```bash
# Clone the repository
git clone <repository-url>
cd skills.skill-discord

# Build for your platform
go build -o skill-discord .

# Or build for all platforms
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o skill-discord-linux-amd64 .
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o skill-discord-linux-arm64 .
CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -o skill-discord-darwin-amd64 .
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -o skill-discord-darwin-arm64 .
```

### Using Docker

```bash
docker build -t axiom-studio/skills.skill-discord .
docker run -p 50053:50053 axiom-studio/skills.skill-discord
```

## Configuration

### Required Permissions

The Discord bot token requires the following permissions:
- **Send Messages**: Send messages to channels
- **Embed Links**: Send embed messages
- **Add Reactions**: Add reactions to messages
- **Manage Channels**: Create, update, delete channels
- **Kick Members**: Kick users from server
- **Ban Members**: Ban/unban users
- **Moderate Members**: Timeout users
- **Manage Roles**: Add/remove user roles
- **Manage Webhooks**: Create and manage webhooks

### Secrets

Store your Discord bot token securely using Atlas secrets:

```yaml
# In your Atlas workflow
{{secrets.discord_token}}
```

## Node Types

### discord-send

Send a text message to a Discord channel.

**Configuration:**
- `token`: Discord bot token (required)
- `channelId`: Channel ID to send message to (required)
- `content`: Message content (required)
- `tts`: Send as text-to-speech (optional, default: false)
- `replyTo`: Message ID to reply to (optional)
- `mentionUser`: Mention user when replying (optional, default: false)

**Output:**
- `messageId`: ID of the sent message
- `channelId`: Channel where message was sent
- `content`: Message content
- `timestamp`: Message timestamp
- `success`: Boolean indicating success

### discord-embed

Send a rich embed message to Discord.

**Configuration:**
- `token`: Discord bot token (required)
- `channelId`: Channel ID (required)
- `content`: Message content above embed (optional)
- `title`: Embed title (required)
- `description`: Embed description (required)
- `color`: Embed color in hex format (optional, default: #5865F2)
- `url`: Embed title URL (optional)
- `authorName`: Author name (optional)
- `authorURL`: Author URL (optional)
- `authorIcon`: Author icon URL (optional)
- `footerText`: Footer text (optional)
- `footerIcon`: Footer icon URL (optional)
- `thumbnail`: Thumbnail image URL (optional)
- `image`: Main image URL (optional)
- `fields`: Array of embed fields (optional)
- `timestamp`: Include current timestamp (optional, default: true)

**Output:**
- `messageId`: ID of the sent message
- `channelId`: Channel where message was sent
- `title`: Embed title
- `timestamp`: Message timestamp
- `success`: Boolean indicating success

### discord-react

Add a reaction emoji to a Discord message.

**Configuration:**
- `token`: Discord bot token (required)
- `channelId`: Channel ID containing the message (required)
- `messageId`: Message ID to react to (required)
- `emoji`: Emoji to react with (required)
- `emojiType`: Type of emoji - "unicode" or "custom" (optional, default: unicode)
- `emojiName`: Custom emoji name (required for custom emojis)

**Output:**
- `messageId`: Message that was reacted to
- `channelId`: Channel ID
- `emoji`: Emoji that was added
- `success`: Boolean indicating success

### discord-channel

Manage Discord channels (create, list, get, delete, update).

**Configuration:**
- `token`: Discord bot token (required)
- `guildId`: Server/Guild ID (required)
- `action`: Action to perform - "create", "list", "get", "delete", "update" (required)
- `channelId`: Channel ID (required for get/delete/update)
- `name`: Channel name (required for create)
- `channelType`: Channel type - "text", "voice", "category", "announcement" (optional, default: text)
- `topic`: Channel topic (optional)
- `nsfw`: Mark as NSFW (optional, default: false)
- `reason`: Audit log reason (optional)

**Output:**
- `action`: Action performed
- `channel`: Channel data
- `success`: Boolean indicating success

### discord-user

Manage Discord users (get info, kick, ban, timeout, roles).

**Configuration:**
- `token`: Discord bot token (required)
- `guildId`: Server/Guild ID (required)
- `action`: Action to perform - "get", "kick", "ban", "unban", "timeout", "get-role", "add-role", "remove-role" (required)
- `userId`: User ID (required)
- `roleId`: Role ID (required for role operations)
- `reason`: Audit log reason (optional)
- `duration`: Timeout duration in seconds (optional)
- `deleteDays`: Days of messages to delete on ban (optional, 0-7)

**Output:**
- `action`: Action performed
- `user`: User data
- `success`: Boolean indicating success

### discord-webhook

Send a message via Discord webhook.

**Configuration:**
- `webhookUrl`: Discord webhook URL (required)
- `content`: Message content (required)
- `username`: Override webhook username (optional)
- `avatarUrl`: Override webhook avatar URL (optional)
- `tts`: Send as text-to-speech (optional, default: false)
- `threadId`: Send to specific thread (optional)
- `wait`: Wait for server confirmation (optional, default: false)

**Output:**
- `messageId`: ID of the sent message (if wait=true)
- `content`: Message content
- `timestamp`: Message timestamp (if wait=true)
- `success`: Boolean indicating success

## Example Workflows

### Send Welcome Message

```yaml
nodes:
  - type: discord-send
    config:
      token: "{{secrets.discord_token}}"
      channelId: "123456789012345678"
      content: "Welcome to the server! Please read the rules."
```

### Send Rich Embed

```yaml
nodes:
  - type: discord-embed
    config:
      token: "{{secrets.discord_token}}"
      channelId: "123456789012345678"
      title: "Server Update"
      description: "We've updated our community guidelines!"
      color: "#5865F2"
      fields:
        - name: "What Changed"
          value: "New rules added"
          inline: true
        - name: "When"
          value: "Effective immediately"
          inline: true
      footerText: "Atlas Bot"
      timestamp: true
```

### Moderate User

```yaml
nodes:
  - type: discord-user
    config:
      token: "{{secrets.discord_token}}"
      guildId: "987654321098765432"
      action: "timeout"
      userId: "111222333444555666"
      duration: 3600
      reason: "Violated community guidelines"
```

### Create Channel

```yaml
nodes:
  - type: discord-channel
    config:
      token: "{{secrets.discord_token}}"
      guildId: "987654321098765432"
      action: "create"
      name: "new-discussion"
      channelType: "text"
      topic: "General discussion channel"
```

## Development

### Building

```bash
# Build for current platform
go build -o skill-discord .

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
export SKILL_PORT=50053

# Run the skill
go run main.go
```

## License

MIT License - see LICENSE file for details.

## Support

For issues and feature requests, please open an issue on GitHub.

**Author:** Axiom Studio  
**Email:** engineering@axiomstudio.ai
