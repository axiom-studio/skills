# DigitalOcean Skill

DigitalOcean cloud infrastructure operations for Atlas agents. Manage Droplets, Kubernetes, Spaces, App Platform, Databases, and Load Balancers.

## Overview

The DigitalOcean skill provides comprehensive node types for managing DigitalOcean cloud resources. It supports Droplet lifecycle management, Kubernetes cluster operations, Spaces object storage, App Platform deployments, managed databases, and load balancer configuration.

## Node Types

### `do-droplet-list`

List all Droplets in your account.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| apiToken | string | Yes | DigitalOcean API token |
| tag | string | No | Filter by tag name |
| region | string | No | Filter by region slug |

**Output:**

```json
{
  "droplets": [
    {
      "id": 3164444,
      "name": "example.com",
      "memory": 1024,
      "vcpus": 1,
      "disk": 25,
      "region": {"slug": "nyc1", "name": "New York 1"},
      "image": {"slug": "ubuntu-22-04-x64"},
      "status": "active",
      "networks": {
        "v4": [{"ip_address": "192.0.2.1", "type": "public"}]
      },
      "tags": ["web", "production"]
    }
  ],
  "count": 1
}
```

---

### `do-droplet-create`

Create a new Droplet.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| apiToken | string | Yes | DigitalOcean API token |
| name | string | Yes | Droplet hostname |
| region | string | Yes | Region slug (nyc1, sfo2, ams3, etc.) |
| size | string | Yes | Size slug (s-1vcpu-1gb, s-2vcpu-4gb, etc.) |
| image | string | Yes | Image slug or ID (ubuntu-22-04-x64, docker-20-04, etc.) |
| sshKeys | array | No | SSH key IDs or fingerprints |
| userData | string | No | Cloud-init user data script |
| tags | array | No | Tags to apply |
| monitoring | boolean | No | Enable monitoring (default: true) |
| backups | boolean | No | Enable automated backups |
| ipv6 | boolean | No | Enable IPv6 |

**Output:**

```json
{
  "success": true,
  "dropletId": 3164445,
  "name": "web-server-01",
  "status": "new",
  "message": "Droplet creation initiated"
}
```

---

### `do-droplet-delete`

Delete a Droplet.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| apiToken | string | Yes | DigitalOcean API token |
| dropletId | integer | Yes | Droplet ID to delete |

---

### `do-droplet-power`

Power on/off, reboot, or shutdown a Droplet.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| apiToken | string | Yes | DigitalOcean API token |
| dropletId | integer | Yes | Droplet ID |
| action | string | Yes | Action: power_on, power_off, reboot, shutdown |

---

### `do-kubernetes-list`

List all Kubernetes clusters.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| apiToken | string | Yes | DigitalOcean API token |

**Output:**

```json
{
  "clusters": [
    {
      "id": "bd5f5959-5e1e-4205-a714-a914373942af",
      "name": "prod-cluster",
      "region": "nyc1",
      "version": "1.28.2-do.0",
      "node_pools": [
        {
          "name": "default",
          "count": 3,
          "nodes": [...]
        }
      ],
      "status": {"state": "running"}
    }
  ]
}
```

---

### `do-kubernetes-create`

Create a Kubernetes cluster.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| apiToken | string | Yes | DigitalOcean API token |
| name | string | Yes | Cluster name |
| region | string | Yes | Region slug |
| version | string | Yes | Kubernetes version |
| nodePools | array | Yes | Node pool configurations |
| autoUpgrade | boolean | No | Enable auto-upgrades |
| ha | boolean | No | Enable high availability |

---

### `do-space-list`

List all Spaces (object storage buckets).

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| apiToken | string | Yes | DigitalOcean API token |

---

### `do-space-upload`

Upload a file to Spaces.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| apiToken | string | Yes | DigitalOcean API token |
| space | string | Yes | Space name |
| key | string | Yes | Object key |
| content | string | Yes | File content |
| contentType | string | No | Content type |
| acl | string | No | Access control (private, public-read) |

---

### `do-space-download`

Download a file from Spaces.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| apiToken | string | Yes | DigitalOcean API token |
| space | string | Yes | Space name |
| key | string | Yes | Object key |

---

### `do-app-list`

List all App Platform apps.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| apiToken | string | Yes | DigitalOcean API token |

---

### `do-app-deploy`

Deploy an app to App Platform.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| apiToken | string | Yes | DigitalOcean API token |
| spec | object | Yes | App spec (services, databases, etc.) |

---

### `do-database-list`

List all managed databases.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| apiToken | string | Yes | DigitalOcean API token |

---

### `do-loadbalancer-list`

List all load balancers.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| apiToken | string | Yes | DigitalOcean API token |

---

## Authentication

Get your API token from DigitalOcean Control Panel > API > Generate New Token.

**Required scopes:**
- `read` - For listing and viewing resources
- `write` - For creating, modifying, and deleting resources

### Creating an API Token

1. Log in to DigitalOcean Control Panel
2. Navigate to API > Tokens/Keys
3. Click "Generate New Token"
4. Give it a name and select scopes
5. Copy the token immediately (shown only once)

## Usage Examples

### Create and Manage Droplets

```yaml
# Create a Droplet
- type: do-droplet-create
  config:
    apiToken: "{{secrets.digitalocean.apiToken}}"
    name: "web-server-01"
    region: "nyc1"
    size: "s-2vcpu-4gb"
    image: "ubuntu-22-04-x64"
    sshKeys: ["289794"]
    tags: ["web", "production"]
    userData: |
      #!/bin/bash
      apt update && apt install -y nginx

# List Droplets
- type: do-droplet-list
  config:
    apiToken: "{{secrets.digitalocean.apiToken}}"
    tag: "production"

# Power off a Droplet
- type: do-droplet-power
  config:
    apiToken: "{{secrets.digitalocean.apiToken}}"
    dropletId: 3164444
    action: "power_off"
```

### Manage Kubernetes

```yaml
# List clusters
- type: do-kubernetes-list
  config:
    apiToken: "{{secrets.digitalocean.apiToken}}"

# Create cluster
- type: do-kubernetes-create
  config:
    apiToken: "{{secrets.digitalocean.apiToken}}"
    name: "prod-cluster"
    region: "nyc1"
    version: "1.28.2-do.0"
    nodePools:
      - name: "workers"
        size: "s-2vcpu-4gb"
        count: 3
```

### Upload to Spaces

```yaml
- type: do-space-upload
  config:
    apiToken: "{{secrets.digitalocean.apiToken}}"
    space: "my-bucket"
    key: "reports/monthly.json"
    content: '{"status": "completed"}'
    contentType: "application/json"
```

## Error Handling

All node types return structured error responses:

```json
{
  "error": "Droplet not found",
  "id": "not_found",
  "message": "The requested resource could not be found."
}
```

## License

MIT License - See [LICENSE](LICENSE) for details.