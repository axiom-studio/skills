# SQS Skill

AWS SQS queue operations for Atlas agents. Send, receive, and manage SQS messages and queues.

## Overview

The SQS skill provides comprehensive node types for AWS Simple Queue Service. It supports message operations, queue management, and dead-letter queue handling.

## Node Types

### `sqs-send-message`

Send a message to a queue.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| accessKeyId | string | Yes | AWS access key ID |
| secretAccessKey | string | Yes | AWS secret access key |
| region | string | Yes | AWS region |
| queueUrl | string | Yes | Queue URL |
| messageBody | string | Yes | Message body |
| delaySeconds | integer | No | Delay in seconds |
| messageAttributes | object | No | Message attributes |
| groupId | string | No | FIFO group ID |

**Output:**

```json
{
  "success": true,
  "messageId": "abc123-def456",
  "md5OfMessageBody": "d41d8cd98f00b204e9800998ecf8427e"
}
```

### `sqs-receive-message`

Receive messages from a queue.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| accessKeyId | string | Yes | AWS access key ID |
| secretAccessKey | string | Yes | AWS secret access key |
| region | string | Yes | AWS region |
| queueUrl | string | Yes | Queue URL |
| maxNumberOfMessages | integer | No | Max messages (1-10) |
| waitTimeSeconds | integer | No | Long polling wait time |
| visibilityTimeout | integer | No | Visibility timeout |

### `sqs-delete-message`

Delete a message.

### `sqs-queue-create`

Create a queue.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| accessKeyId | string | Yes | AWS access key ID |
| secretAccessKey | string | Yes | AWS secret access key |
| region | string | Yes | AWS region |
| queueName | string | Yes | Queue name |
| fifo | boolean | No | FIFO queue |
| delaySeconds | integer | No | Default delay |
| visibilityTimeout | integer | No | Default visibility |

### `sqs-queue-list`

List queues.

### `sqs-queue-delete`

Delete a queue.

### `sqs-queue-purge`

Purge all messages.

### `sqs-queue-attributes`

Get queue attributes.

### `sqs-dead-letter-queue`

Configure DLQ.

## Usage Examples

```yaml
# Send message
- type: sqs-send-message
  config:
    accessKeyId: "{{secrets.aws.accessKeyId}}"
    secretAccessKey: "{{secrets.aws.secretAccessKey}}"
    region: "us-east-1"
    queueUrl: "https://sqs.us-east-1.amazonaws.com/123/my-queue"
    messageBody: '{"orderId": "123"}'
    messageAttributes:
      OrderType: {DataType: "String", StringValue: "Premium"}

# Receive messages
- type: sqs-receive-message
  config:
    accessKeyId: "{{secrets.aws.accessKeyId}}"
    secretAccessKey: "{{secrets.aws.secretAccessKey}}"
    region: "us-east-1"
    queueUrl: "https://sqs.us-east-1.amazonaws.com/123/my-queue"
    maxNumberOfMessages: 10
    waitTimeSeconds: 20
```

## License

MIT License - See [LICENSE](LICENSE) for details.