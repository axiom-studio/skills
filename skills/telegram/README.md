# Telegram Skill

Telegram messaging bot operations for Atlas agents. Send messages, manage chats, and handle bot interactions.

## Overview

The Telegram skill provides comprehensive node types for Telegram bot operations. It supports message sending, editing, media uploads, and chat management.

## Node Types

### `telegram-send-message`

Send a text message.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| botToken | string | Yes | Telegram bot token |
| chatId | string | Yes | Chat ID or username |
| text | string | Yes | Message text |
| parseMode | string | No | Parse mode (HTML, Markdown) |
| disableNotification | boolean | No | Disable notifications |
| replyToMessageId | integer | No | Reply to message ID |

**Output:**

```json
{
  "success": true,
  "messageId": 123,
  "chat": {"id": 456, "type": "private"},
  "date": 1705312200
}
```

### `telegram-edit-message`

Edit a message.

### `telegram-delete-message`

Delete a message.

### `telegram-send-photo`

Send a photo.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| botToken | string | Yes | Telegram bot token |
| chatId | string | Yes | Chat ID |
| photo | string | Yes | Photo URL or file ID |
| caption | string | No | Photo caption |

### `telegram-send-document`

Send a document.

### `telegram-get-updates`

Get bot updates.

### `telegram-get-chat`

Get chat information.

### `telegram-set-webhook`

Set webhook URL.

## Authentication

Get bot token from @BotFather on Telegram.

## Usage Examples

```yaml
# Send message
- type: telegram-send-message
  config:
    botToken: "{{secrets.telegram.botToken}}"
    chatId: "@channelname"
    text: "New deployment completed successfully!"
    parseMode: "HTML"

# Send photo
- type: telegram-send-photo
  config:
    botToken: "{{secrets.telegram.botToken}}"
    chatId: "123456"
    photo: "https://example.com/image.png"
    caption: "Deployment dashboard"
```

## License

MIT License - See [LICENSE](LICENSE) for details.