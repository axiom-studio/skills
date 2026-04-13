# Zoom Skill

Zoom video conferencing operations for Atlas agents. Manage meetings, recordings, participants, and webinars.

## Overview

The Zoom skill provides comprehensive node types for Zoom video conferencing. It supports meeting management, recording access, participant tracking, and webinar operations.

## Node Types

### `zoom-meeting-create`

Create a new Zoom meeting.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| accountId | string | Yes | Zoom account ID |
| clientId | string | Yes | Zoom client ID |
| clientSecret | string | Yes | Zoom client secret |
| topic | string | Yes | Meeting topic |
| type | integer | No | Meeting type (1=instant, 2=scheduled) |
| startTime | string | No | Start time (ISO 8601) |
| duration | integer | No | Duration in minutes |
| timezone | string | No | Timezone |
| password | string | No | Meeting password |
| settings | object | No | Meeting settings |

**Output:**

```json
{
  "success": true,
  "meetingId": "123456789",
  "joinUrl": "https://zoom.us/j/123456789",
  "password": "abc123",
  "hostKey": "456789"
}
```

### `zoom-meeting-list`

List meetings.

### `zoom-meeting-get`

Get meeting details.

### `zoom-meeting-delete`

Delete a meeting.

### `zoom-recording-list`

List recordings.

### `zoom-participant-list`

List meeting participants.

### `zoom-user-list`

List users.

### `zoom-webinar-create`

Create a webinar.

## Authentication

Create OAuth app at Zoom App Marketplace > Develop > Build App.

**Required scopes:**
- `meeting:write` - Create/update meetings
- `meeting:read` - Read meetings
- `recording:read` - Read recordings
- `user:read` - Read users

## Usage Examples

```yaml
# Create meeting
- type: zoom-meeting-create
  config:
    accountId: "{{secrets.zoom.accountId}}"
    clientId: "{{secrets.zoom.clientId}}"
    clientSecret: "{{secrets.zoom.clientSecret}}"
    topic: "Team Standup"
    type: 2
    startTime: "2024-01-16T10:00:00Z"
    duration: 30
    password: "standup123"
    settings:
      host_video: true
      participant_video: true
      mute_upon_entry: true

# List participants
- type: zoom-participant-list
  config:
    accountId: "{{secrets.zoom.accountId}}"
    clientId: "{{secrets.zoom.clientId}}"
    clientSecret: "{{secrets.zoom.clientSecret}}"
    meetingId: "123456789"
```

## License

MIT License - See [LICENSE](LICENSE) for details.