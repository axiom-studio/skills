# Calendly Skill

Calendly scheduling operations for Atlas agents. Manage events, bookings, and availability.

## Overview

The Calendly skill provides comprehensive node types for Calendly scheduling automation. It supports event management, booking operations, availability queries, and webhook configuration.

## Node Types

### `calendly-event-list`

List scheduled events.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| apiKey | string | Yes | Calendly API token |
| user | string | No | Filter by user URI |
| status | string | No | Filter by status |
| count | integer | No | Max results |

**Output:**

```json
{
  "events": [
    {
      "uri": "https://api.calendly.com/scheduled_events/abc123",
      "name": "Team Meeting",
      "status": "active",
      "start_time": "2024-01-20T10:00:00Z",
      "invitee": {"email": "user@example.com"}
    }
  ]
}
```

### `calendly-event-get`

Get event details.

### `calendly-event-create`

Create a scheduled event.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| apiKey | string | Yes | Calendly API token |
| eventTypeId | string | Yes | Event type URI |
| inviteeEmail | string | Yes | Invitee email |
| inviteeName | string | Yes | Invitee name |
| startTime | string | Yes | Start time (ISO 8601) |
| timezone | string | No | Timezone |

### `calendly-cancel`

Cancel an event.

### `calendly-reschedule`

Reschedule an event.

### `calendly-availability`

Check availability.

### `calendly-user-get`

Get user details.

### `calendly-webhook-create`

Create a webhook subscription.

## Authentication

Get API token from Calendly Settings > Integrations > API & Webhooks.

## Usage Examples

```yaml
# List events
- type: calendly-event-list
  config:
    apiKey: "{{secrets.calendly.apiKey}}"
    status: "active"

# Create event
- type: calendly-event-create
  config:
    apiKey: "{{secrets.calendly.apiKey}}"
    eventTypeId: "https://api.calendly.com/event_types/abc"
    inviteeEmail: "client@example.com"
    inviteeName: "John Client"
    startTime: "2024-01-20T14:00:00Z"
```

## License

MIT License - See [LICENSE](LICENSE) for details.