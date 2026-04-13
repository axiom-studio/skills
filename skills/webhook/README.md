# Webhook Skill

Webhook triggers and actions for Atlas agents. This skill provides comprehensive webhook functionality including endpoint creation, payload sending, parsing, and signature verification.

## Features

- **Webhook Trigger**: Create and manage webhook endpoints for receiving external events
- **Webhook Send**: Send webhook payloads to external services
- **Webhook Parse**: Parse and validate incoming webhook payloads
- **Webhook Signature**: Verify webhook signatures for security

## Installation

```bash
# Clone the repository
git clone https://github.com/axiom-studio/skills.skill-webhook.git

# Build the skill
cd skills.skill-webhook
go build -o skill-webhook .

# Or build with Docker
docker build -t skill-webhook .
```

## Usage

### Webhook Trigger (`webhook-trigger`)

Create webhook endpoints to receive external events.

```yaml
nodeType: webhook-trigger
config:
  endpoint: /webhooks/my-endpoint
  method: POST
  headers:
    Content-Type: application/json
  timeout: 30s
```

### Webhook Send (`webhook-send`)

Send webhook payloads to external services.

```yaml
nodeType: webhook-send
config:
  url: https://example.com/webhook
  method: POST
  headers:
    Content-Type: application/json
    X-Custom-Header: value
  body:
    event: user.created
    data:
      userId: "123"
  timeout: 30s
  retryCount: 3
```

### Webhook Parse (`webhook-parse`)

Parse and validate incoming webhook payloads.

```yaml
nodeType: webhook-parse
config:
  payload: "{{inputs.payload}}"
  schema:
    type: object
    properties:
      event:
        type: string
      data:
        type: object
  extractFields:
    - event
    - data
```

### Webhook Signature (`webhook-signature`)

Verify webhook signatures for security.

```yaml
nodeType: webhook-signature
config:
  signature: "{{headers.X-Signature}}"
  secret: "{{secrets.webhook_secret}}"
  algorithm: sha256
  headerName: X-Signature
  timestampHeader: X-Timestamp
  timestampTolerance: 300s
```

## Configuration Options

### webhook-trigger

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| endpoint | string | Yes | Webhook endpoint path |
| method | string | No | HTTP method (default: POST) |
| headers | object | No | Expected headers |
| timeout | string | No | Request timeout (default: 30s) |

### webhook-send

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| url | string | Yes | Target webhook URL |
| method | string | No | HTTP method (default: POST) |
| headers | object | No | Custom headers |
| body | object | No | Request body |
| timeout | string | No | Request timeout |
| retryCount | int | No | Number of retries on failure |

### webhook-parse

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| payload | string | Yes | JSON payload to parse |
| schema | object | No | JSON schema for validation |
| extractFields | array | No | Fields to extract from payload |

### webhook-signature

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| signature | string | Yes | Signature from headers |
| secret | string | Yes | Shared secret for verification |
| algorithm | string | No | Hash algorithm (default: sha256) |
| headerName | string | No | Header containing signature |
| timestampHeader | string | No | Header containing timestamp |
| timestampTolerance | string | No | Allowed timestamp drift |

## Security

- Always use HTTPS for webhook endpoints
- Verify webhook signatures using the `webhook-signature` node
- Store secrets using the secrets manager
- Implement rate limiting for webhook endpoints

## License

MIT License - see LICENSE file for details.

## Support

For issues and feature requests, please open an issue on GitHub.
