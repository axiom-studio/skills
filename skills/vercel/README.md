# Vercel Skill

Vercel deployment platform operations for Atlas agents. Manage deployments, projects, domains, and environment variables.

## Overview

The Vercel skill provides comprehensive node types for Vercel platform operations. It supports deployment management, project configuration, domain setup, and environment variable management.

## Node Types

### `vercel-deploy`

Create a new deployment.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| apiToken | string | Yes | Vercel API token |
| projectId | string | No | Project ID (for project deployments) |
| name | string | No | Deployment name |
| files | array | Yes | Files to deploy |
| target | string | No | Target (production, preview) |
| ref | string | No | Git ref (branch/tag) |

**Output:**

```json
{
  "success": true,
  "deploymentId": "dpl_abc123",
  "url": "https://my-app.vercel.app",
  "status": "BUILDING"
}
```

### `vercel-deployment-list`

List deployments.

### `vercel-deployment-get`

Get deployment details.

### `vercel-project-list`

List projects.

### `vercel-project-create`

Create a project.

### `vercel-domain-add`

Add a domain.

### `vercel-domain-list`

List domains.

### `vercel-env-set`

Set environment variable.

### `vercel-env-list`

List environment variables.

## Authentication

Get API token from Vercel Settings > Tokens.

## Usage Examples

```yaml
# Deploy
- type: vercel-deploy
  config:
    apiToken: "{{secrets.vercel.apiToken}}"
    projectId: "prj_abc123"
    target: "production"
    files:
      - path: "index.html"
        data: "<html>Hello</html>"

# Set env var
- type: vercel-env-set
  config:
    apiToken: "{{secrets.vercel.apiToken}}"
    projectId: "prj_abc123"
    key: "DATABASE_URL"
    value: "postgres://..."
    target: "production"
```

## License

MIT License - See [LICENSE](LICENSE) for details.