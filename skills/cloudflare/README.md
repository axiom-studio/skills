# Cloudflare Skill

Cloudflare DNS, Workers, and edge operations for Atlas agents. This skill enables AI agents to manage Cloudflare services including DNS records, Workers, R2 storage, D1 databases, WAF rules, and zone settings.

## Overview

The Cloudflare skill provides comprehensive node types for managing Cloudflare's suite of services. It supports DNS management, Workers deployment, R2 object storage, D1 database operations, WAF configuration, and zone management.

## Node Types

### `cf-dns-list`

List all DNS records for a zone.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| apiToken | string | Yes | Cloudflare API token |
| zoneId | string | Yes | Zone ID |
| type | string | No | Filter by record type (A, AAAA, CNAME, etc.) |
| name | string | No | Filter by record name |

**Output:**

```json
{
  "records": [
    {
      "id": "372e67954025e0ba6aaa6d586b9e0b59",
      "type": "A",
      "name": "example.com",
      "content": "192.0.2.1",
      "proxiable": true,
      "proxied": true,
      "ttl": 1,
      "locked": false
    }
  ],
  "count": 1
}
```

---

### `cf-dns-create`

Create a new DNS record.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| apiToken | string | Yes | Cloudflare API token |
| zoneId | string | Yes | Zone ID |
| type | string | Yes | Record type (A, AAAA, CNAME, MX, TXT, etc.) |
| name | string | Yes | Record name |
| content | string | Yes | Record content/value |
| ttl | integer | No | TTL in seconds (1 = automatic) |
| proxied | boolean | No | Proxy through Cloudflare (default: false) |
| priority | integer | No | Priority (for MX records) |

**Output:**

```json
{
  "success": true,
  "recordId": "372e67954025e0ba6aaa6d586b9e0b59",
  "name": "www.example.com",
  "type": "A",
  "content": "192.0.2.1"
}
```

---

### `cf-dns-update`

Update an existing DNS record.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| apiToken | string | Yes | Cloudflare API token |
| zoneId | string | Yes | Zone ID |
| recordId | string | Yes | Record ID to update |
| type | string | Yes | Record type |
| name | string | Yes | Record name |
| content | string | Yes | Record content/value |
| ttl | integer | No | TTL in seconds |
| proxied | boolean | No | Proxy through Cloudflare |

---

### `cf-dns-delete`

Delete a DNS record.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| apiToken | string | Yes | Cloudflare API token |
| zoneId | string | Yes | Zone ID |
| recordId | string | Yes | Record ID to delete |

---

### `cf-worker-deploy`

Deploy a Cloudflare Worker.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| apiToken | string | Yes | Cloudflare API token |
| accountId | string | Yes | Cloudflare account ID |
| workerName | string | Yes | Worker name |
| script | string | Yes | Worker JavaScript code |
| compatibilityDate | string | No | Compatibility date (default: current) |
| compatibilityFlags | array | No | Compatibility flags |
| bindings | object | No | Environment variable bindings |
| kvNamespaces | object | No | KV namespace bindings |
| durableObjects | object | No | Durable Object bindings |

**Output:**

```json
{
  "success": true,
  "workerName": "my-worker",
  "message": "Worker deployed successfully"
}
```

---

### `cf-worker-list`

List all Workers in an account.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| apiToken | string | Yes | Cloudflare API token |
| accountId | string | Yes | Cloudflare account ID |

---

### `cf-r2-list`

List all R2 buckets.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| apiToken | string | Yes | Cloudflare API token |
| accountId | string | Yes | Cloudflare account ID |

---

### `cf-r2-upload`

Upload an object to R2 storage.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| apiToken | string | Yes | Cloudflare API token |
| accountId | string | Yes | Cloudflare account ID |
| bucket | string | Yes | Bucket name |
| objectName | string | Yes | Object name |
| content | string | Yes | File content |
| contentType | string | No | Content type |

---

### `cf-r2-download`

Download an object from R2 storage.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| apiToken | string | Yes | Cloudflare API token |
| accountId | string | Yes | Cloudflare account ID |
| bucket | string | Yes | Bucket name |
| objectName | string | Yes | Object name |

---

### `cf-d1-list`

List all D1 databases.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| apiToken | string | Yes | Cloudflare API token |
| accountId | string | Yes | Cloudflare account ID |

---

### `cf-d1-query`

Execute a SQL query on a D1 database.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| apiToken | string | Yes | Cloudflare API token |
| accountId | string | Yes | Cloudflare account ID |
| databaseId | string | Yes | Database UUID |
| query | string | Yes | SQL query |

**Output:**

```json
{
  "success": true,
  "results": [
    {"id": 1, "name": "item1"},
    {"id": 2, "name": "item2"}
  ],
  "meta": {
    "changed_db": false,
    "changes": 0,
    "duration": 0.001
  }
}
```

---

### `cf-waf-list`

List WAF rules for a zone.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| apiToken | string | Yes | Cloudflare API token |
| zoneId | string | Yes | Zone ID |

---

### `cf-waf-create-rule`

Create a custom WAF rule.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| apiToken | string | Yes | Cloudflare API token |
| zoneId | string | Yes | Zone ID |
| expression | string | Yes | Firewall rule expression |
| action | string | Yes | Action (block, challenge, allow, log) |
| description | string | No | Rule description |

---

### `cf-zone-list`

List all zones in an account.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| apiToken | string | Yes | Cloudflare API token |
| accountId | string | No | Filter by account ID |
| name | string | No | Filter by zone name |
| status | string | No | Filter by status (active, pending) |

---

## Authentication

All node types require a Cloudflare API token with appropriate permissions.

### Creating an API Token

1. Go to Cloudflare Dashboard > My Profile > API Tokens
2. Create Token with appropriate permissions:
   - **Zone:DNS:Edit** - For DNS operations
   - **Zone:Zone:Read** - For zone listing
   - **Account:Workers:Edit** - For Workers
   - **Account:Storage:Edit** - For R2
   - **Zone:Firewall:Edit** - For WAF

### Getting IDs

- **Zone ID**: Found in Cloudflare Dashboard > Overview > API section
- **Account ID**: Found in Cloudflare Dashboard > Overview

## Usage Examples

### Create DNS Record

```yaml
- type: cf-dns-create
  config:
    apiToken: "{{secrets.cloudflare.apiToken}}"
    zoneId: "{{secrets.cloudflare.zoneId}}"
    type: "A"
    name: "app"
    content: "192.0.2.1"
    ttl: 1
    proxied: true
```

### Deploy Worker

```yaml
- type: cf-worker-deploy
  config:
    apiToken: "{{secrets.cloudflare.apiToken}}"
    accountId: "{{secrets.cloudflare.accountId}}"
    workerName: "api-gateway"
    script: |
      export default {
        async fetch(request, env) {
          return new Response('Hello World!');
        }
      }
    bindings:
      ENVIRONMENT: "production"
```

### Query D1 Database

```yaml
- type: cf-d1-query
  config:
    apiToken: "{{secrets.cloudflare.apiToken}}"
    accountId: "{{secrets.cloudflare.accountId}}"
    databaseId: "abc123-def456"
    query: "SELECT * FROM users WHERE active = true"
```

## License

MIT License - See [LICENSE](LICENSE) for details.
