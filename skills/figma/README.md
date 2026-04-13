# Figma Skill

Figma design platform operations for Atlas agents. Access files, export assets, manage comments, and retrieve styles.

## Overview

The Figma skill provides comprehensive node types for Figma design operations. It supports file access, asset export, comment management, and design system retrieval.

## Node Types

### `figma-file-list`

List files in a project.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| apiToken | string | Yes | Figma API token |
| projectId | string | Yes | Project ID |

**Output:**

```json
{
  "files": [
    {
      "key": "abc123",
      "name": "Design System",
      "thumbnailUrl": "https://...",
      "lastModified": "2024-01-15T10:30:00.000Z"
    }
  ]
}
```

### `figma-file-get`

Get file metadata and structure.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| apiToken | string | Yes | Figma API token |
| fileKey | string | Yes | File key |
| depth | integer | No | Node depth (default: 0) |

### `figma-file-export`

Export frames or components.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| apiToken | string | Yes | Figma API token |
| fileKey | string | Yes | File key |
| nodeId | string | Yes | Node ID to export |
| format | string | No | Export format (png, svg, jpg) |
| scale | number | No | Scale factor |

### `figma-comment-create`

Create a comment.

### `figma-comment-list`

List comments.

### `figma-team-projects`

List team projects.

### `figma-project-files`

List project files.

### `figma-style-list`

List styles in a file.

## Authentication

Get API token from Figma Settings > Account > Personal access tokens.

## Usage Examples

```yaml
# Get file
- type: figma-file-get
  config:
    apiToken: "{{secrets.figma.apiToken}}"
    fileKey: "abc123def456"

# Export frame
- type: figma-file-export
  config:
    apiToken: "{{secrets.figma.apiToken}}"
    fileKey: "abc123"
    nodeId: "1:2"
    format: "png"
    scale: 2

# List project files
- type: figma-project-files
  config:
    apiToken: "{{secrets.figma.apiToken}}"
    projectId: "12345"
```

## License

MIT License - See [LICENSE](LICENSE) for details.