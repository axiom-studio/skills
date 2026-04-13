# RabbitMQ Skill

RabbitMQ message broker operations for Atlas agents. Manage queues, exchanges, bindings, and message publishing/consuming.

## Overview

The RabbitMQ skill provides comprehensive node types for RabbitMQ messaging. It supports queue management, exchange operations, message publishing/consuming, and binding configuration.

## Node Types

### `rabbitmq-publish`

Publish a message to an exchange.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| url | string | Yes | RabbitMQ URL (amqp://host:port) |
| username | string | Yes | Username |
| password | string | Yes | Password |
| exchange | string | Yes | Exchange name |
| routingKey | string | Yes | Routing key |
| message | string | Yes | Message body |
| contentType | string | No | Content type |
| deliveryMode | integer | No | 1=transient, 2=persistent |
| priority | integer | No | Message priority |

**Output:**

```json
{
  "success": true,
  "exchange": "orders",
  "routingKey": "orders.new",
  "messageId": "msg-abc123"
}
```

### `rabbitmq-consume`

Consume messages from a queue.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| url | string | Yes | RabbitMQ URL |
| username | string | Yes | Username |
| password | string | Yes | Password |
| queue | string | Yes | Queue name |
| maxMessages | integer | No | Max messages to consume |
| ack | boolean | No | Auto-acknowledge |
| timeout | string | No | Consume timeout |

### `rabbitmq-queue-create`

Create a queue.

### `rabbitmq-queue-list`

List queues.

### `rabbitmq-queue-delete`

Delete a queue.

### `rabbitmq-queue-purge`

Purge messages from a queue.

### `rabbitmq-exchange-create`

Create an exchange.

### `rabbitmq-exchange-list`

List exchanges.

### `rabbitmq-binding-create`

Create a binding.

### `rabbitmq-binding-list`

List bindings.

## Usage Examples

```yaml
# Create queue
- type: rabbitmq-queue-create
  config:
    url: "amqp://localhost:5672"
    username: "guest"
    password: "{{secrets.rabbitmq.password}}"
    name: "orders"
    durable: true

# Publish message
- type: rabbitmq-publish
  config:
    url: "amqp://localhost:5672"
    username: "guest"
    password: "{{secrets.rabbitmq.password}}"
    exchange: "orders-exchange"
    routingKey: "orders.new"
    message: '{"orderId": "123"}'
    contentType: "application/json"

# Consume messages
- type: rabbitmq-consume
  config:
    url: "amqp://localhost:5672"
    username: "guest"
    password: "{{secrets.rabbitmq.password}}"
    queue: "orders"
    maxMessages: 10
```

## License

MIT License - See [LICENSE](LICENSE) for details.