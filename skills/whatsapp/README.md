# WhatsApp Skill

WhatsApp Business API operations for Atlas agents. Send messages, manage templates, and handle customer communications.

## Overview

The WhatsApp skill provides comprehensive node types for WhatsApp Business API. It supports message sending, template management, media uploads, and contact operations.

## Node Types

### `whatsapp-send-message`

Send a text message.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| phoneNumberId | string | Yes | WhatsApp phone number ID |
| accessToken | string | Yes | Meta access token |
| to | string | Yes | Recipient phone number |
| message | string | Yes | Message text |

**Output:**

```json
{
  "success": true,
  "messageId": "wamid.abc123",
  "status": "sent"
}
```

### `whatsapp-send-template`

Send a template message.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| phoneNumberId | string | Yes | WhatsApp phone number ID |
| accessToken | string | Yes | Meta access token |
| to | string | Yes | Recipient phone number |
| templateName | string | Yes | Template name |
| language | string | No | Language code (default: en_US) |
| components | array | No | Template components |

### `whatsapp-send-media`

Send media message.

### `whatsapp-mark-read`

Mark messages as read.

### `whatsapp-contact-list`

List contacts.

### `whatsapp-phone-number-list`

List phone numbers.

### `whatsapp-business-profile`

Get business profile.

## Authentication

Get credentials from Meta Developer Portal.

## Usage Examples

```yaml
# Send text message
- type: whatsapp-send-message
  config:
    phoneNumberId: "{{secrets.whatsapp.phoneNumberId}}"
    accessToken: "{{secrets.whatsapp.accessToken}}"
    to: "+1234567890"
    message: "Your order has been shipped!"

# Send template
- type: whatsapp-send-template
  config:
    phoneNumberId: "{{secrets.whatsapp.phoneNumberId}}"
    accessToken: "{{secrets.whatsapp.accessToken}}"
    to: "+1234567890"
    templateName: "order_confirmation"
    language: "en_US"
    components:
      - type: "body"
        parameters:
          - type: "text"
            text: "Order #123"
```

## License

MIT License - See [LICENSE](LICENSE) for details.