# Netlify Skill

Netlify deployment platform operations for Atlas agents. Manage sites, deployments, DNS, and environment variables.

## Overview

The Netlify skill provides comprehensive node types for Netlify platform operations. It supports site management, deployment operations, DNS configuration, and environment variable management.

## Node Types

### `netlify-deploy`

Create a new deployment.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| apiToken | string | Yes | Netlify API token |
| siteId | string | Yes | Site ID |
| dir | string | No | Directory to deploy |
| functions | string | No | Functions directory |
| draft | boolean | No | Create draft deployment |
| message | string | No | Deployment message |

**Output:**

```json
{
  "success": true,
  "deploymentId": "abc123",
  "deployUrl": "https://abc123--mysite.netlify.app",
  "adminUrl": "https://app.netlify.com/sites/mysite/deploys/abc123",
  "state": "building"
}
```

### `netlify-site-list`

List sites.

### `netlify-site-create`

Create a new site.

### `netlify-site-get`

Get site details.

### `netlify-deploy-list`

List deployments.

### `netlify-dns-records`

Manage DNS records.

### `netlify-env-set`

Set environment variable.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| apiToken | string | Yes | Netlify API token |
| siteId | string | Yes | Site ID |
| key | string | Yes | Variable key |
| value | string | Yes | Variable value |
| context | string | No | Context (production, dev, branch) |

### `netlify-env-list`

List environment variables.

### `netlify-build-hook-create`

Create a build hook.

## Authentication

Get API token from Netlify User Settings > Applications > Personal access tokens.

## Usage Examples

```yaml
# Deploy site
- type: netlify-deploy
  config:
    apiToken: "{{secrets.netlify.apiToken}}"
    siteId: "abc-123"
    dir: "./dist"
    message: "Production deployment v1.2.3"

# Set env var
- type: netlify-env-set
  config:
    apiToken: "{{secrets.netlify.apiToken}}"
    siteId: "abc-123"
    key: "API_URL"
    value: "https://api.example.com"
    context: "production"
```

## License

MIT License - See [LICENSE](LICENSE) for details.