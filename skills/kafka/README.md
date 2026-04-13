# Apache Kafka Skill

Apache Kafka event streaming operations for Atlas agents. Produce and consume messages, manage topics and consumer groups.

## Overview

The Kafka skill provides comprehensive node types for Apache Kafka event streaming. It supports message production/consumption, topic management, consumer group operations, and partition/offset management.

## Node Types

### `kafka-produce`

Produce messages to a Kafka topic.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| brokers | array | Yes | Broker addresses (host:port) |
| topic | string | Yes | Topic name |
| key | string | No | Message key |
| value | string | Yes | Message value |
| headers | object | No | Message headers |
| partition | integer | No | Target partition |
| ack | string | No | Ack mode (none, leader, all) |
| compression | string | No | Compression (none, gzip, snappy, lz4, zstd) |

**Output:**

```json
{
  "success": true,
  "topic": "orders",
  "partition": 0,
  "offset": 12345,
  "timestamp": 1705312200000
}
```

---

### `kafka-consume`

Consume messages from a Kafka topic.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| brokers | array | Yes | Broker addresses |
| topic | string | Yes | Topic name |
| groupId | string | No | Consumer group ID |
| fromBeginning | boolean | No | Start from beginning |
| maxMessages | integer | No | Max messages to consume |
| timeout | string | No | Poll timeout |
| autoCommit | boolean | No | Auto-commit offsets |

**Output:**

```json
{
  "messages": [
    {
      "topic": "orders",
      "partition": 0,
      "offset": 12345,
      "key": "order-123",
      "value": "{\"orderId\": \"123\"}",
      "headers": {"source": "web"},
      "timestamp": 1705312200000
    }
  ],
  "count": 1
}
```

---

### `kafka-topic-create`

Create a Kafka topic.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| brokers | array | Yes | Broker addresses |
| topic | string | Yes | Topic name |
| partitions | integer | No | Number of partitions |
| replicationFactor | integer | No | Replication factor |
| config | object | No | Topic configuration |

**Output:**

```json
{
  "success": true,
  "topic": "events",
  "partitions": 3,
  "message": "Topic created successfully"
}
```

---

### `kafka-topic-list`

List all topics.

### `kafka-topic-delete`

Delete a topic.

### `kafka-topic-describe`

Get topic details.

### `kafka-consumer-group-list`

List consumer groups.

### `kafka-consumer-group-reset`

Reset consumer group offsets.

### `kafka-partition-list`

List topic partitions.

### `kafka-offset-get`

Get partition offsets.

## Authentication

Supports SASL and TLS authentication.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| saslMechanism | string | No | SASL mechanism |
| saslUsername | string | No | SASL username |
| saslPassword | string | No | SASL password |
| tls | boolean | No | Enable TLS |
| caCert | string | No | CA certificate |

## Usage Examples

```yaml
# Produce message
- type: kafka-produce
  config:
    brokers: ["kafka-1:9092", "kafka-2:9092"]
    topic: "orders"
    key: "order-123"
    value: '{"orderId": "123", "amount": 99.99}'
    headers:
      source: "web-app"

# Consume messages
- type: kafka-consume
  config:
    brokers: ["kafka-1:9092"]
    topic: "orders"
    groupId: "order-processor"
    maxMessages: 10
```

## License

MIT License - See [LICENSE](LICENSE) for details.