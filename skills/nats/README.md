# NATS Skill

NATS messaging system operations for Atlas agents. Publish, subscribe, and manage JetStream.

## Overview

The NATS skill provides comprehensive node types for NATS messaging. It supports publishing, subscribing, request-reply patterns, JetStream operations, and KV store management.

## Node Types

### `nats-publish`

Publish a message.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| url | string | Yes | NATS server URL |
| subject | string | Yes | Subject to publish to |
| data | string | Yes | Message data |
| headers | object | No | Message headers |

**Output:**

```json
{
  "success": true,
  "subject": "orders.new",
  "message": "Message published successfully"
}
```

### `nats-subscribe`

Subscribe to a subject.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| url | string | Yes | NATS server URL |
| subject | string | Yes | Subject to subscribe |
| queue | string | No | Queue group name |
| timeout | string | No | Subscribe timeout |
| maxMessages | integer | No | Max messages to receive |

### `nats-request`

Request-reply pattern.

### `nats-jetstream-create`

Create a JetStream stream.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| url | string | Yes | NATS server URL |
| name | string | Yes | Stream name |
| subjects | array | Yes | Subjects to store |
| retention | string | No | Retention policy |
| maxConsumers | integer | No | Max consumers |
| maxMsgs | integer | No | Max messages |

### `nats-jetstream-list`

List JetStream streams.

### `nats-consumer-create`

Create a consumer.

### `nats-consumer-list`

List consumers.

### `nats-stream-info`

Get stream information.

## Authentication

Supports token and NKEYS authentication.

## Usage Examples

```yaml
# Publish message
- type: nats-publish
  config:
    url: "nats://localhost:4222"
    subject: "orders.new"
    data: '{"orderId": "123"}'

# Subscribe
- type: nats-subscribe
  config:
    url: "nats://localhost:4222"
    subject: "orders.*"
    maxMessages: 10
    timeout: "30s"

# Create JetStream stream
- type: nats-jetstream-create
  config:
    url: "nats://localhost:4222"
    name: "ORDERS"
    subjects: ["orders.*"]
    retention: "limits"
    maxMsgs: 100000
```

## License

MIT License - See [LICENSE](LICENSE) for details.