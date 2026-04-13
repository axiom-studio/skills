# Google Drive Skill

Google Drive file storage operations for Atlas agents. Manage files, folders, sharing, and permissions.

## Overview

The Google Drive skill provides comprehensive node types for Google Drive operations. It supports file upload/download, folder management, sharing configuration, and search capabilities.

## Node Types

### `gdrive-list`

List files and folders.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| credentials | string | Yes | Service account JSON |
| parentId | string | No | Parent folder ID |
| query | string | No | Search query |
| pageSize | integer | No | Max results (default: 100) |

**Output:**

```json
{
  "files": [
    {
      "id": "1abc123",
      "name": "Report.pdf",
      "mimeType": "application/pdf",
      "size": "1024000",
      "createdTime": "2024-01-15T10:30:00.000Z",
      "modifiedTime": "2024-01-15T12:00:00.000Z"
    }
  ]
}
```

### `gdrive-upload`

Upload a file.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| credentials | string | Yes | Service account JSON |
| name | string | Yes | File name |
| content | string | Yes | File content (base64) |
| mimeType | string | No | MIME type |
| parentId | string | No | Parent folder ID |

### `gdrive-download`

Download a file.

### `gdrive-delete`

Delete a file or folder.

### `gdrive-share`

Share a file or folder.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| credentials | string | Yes | Service account JSON |
| fileId | string | Yes | File ID |
| email | string | Yes | User email |
| role | string | Yes | Role (reader, writer, commenter) |
| type | string | No | Type (user, group, domain, anyone) |

### `gdrive-search`

Search for files.

### `gdrive-folder-create`

Create a folder.

### `gdrive-permission-list`

List permissions.

## Authentication

Use Google Cloud service account with Drive API enabled.

**Required scopes:**
- `https://www.googleapis.com/auth/drive`

## Usage Examples

```yaml
# Upload file
- type: gdrive-upload
  config:
    credentials: "{{secrets.google.credentials}}"
    name: "report.pdf"
    content: "base64encodedcontent"
    mimeType: "application/pdf"
    parentId: "folder123"

# Share file
- type: gdrive-share
  config:
    credentials: "{{secrets.google.credentials}}"
    fileId: "file123"
    email: "user@example.com"
    role: "reader"
```

## License

MIT License - See [LICENSE](LICENSE) for details.