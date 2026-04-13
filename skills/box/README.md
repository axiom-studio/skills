# Box Skill

Box file storage operations for Atlas agents. Manage files, folders, sharing, and metadata.

## Overview

The Box skill provides comprehensive node types for Box enterprise file storage. It supports file upload/download, folder management, sharing configuration, and metadata operations.

## Node Types

### `box-list`

List files and folders.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| accessToken | string | Yes | Box access token |
| folderId | string | No | Folder ID (default: 0 for root) |
| limit | integer | No | Max results |
| offset | integer | No | Offset for pagination |

**Output:**

```json
{
  "entries": [
    {
      "id": "123456",
      "type": "file",
      "name": "report.pdf",
      "size": 1024000,
      "modified_at": "2024-01-15T10:30:00.000Z"
    }
  ],
  "total_count": 1
}
```

### `box-upload`

Upload a file.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| accessToken | string | Yes | Box access token |
| folderId | string | Yes | Destination folder ID |
| filename | string | Yes | File name |
| content | string | Yes | File content (base64) |

### `box-download`

Download a file.

### `box-delete`

Delete a file or folder.

### `box-share`

Create a shared link.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| accessToken | string | Yes | Box access token |
| fileId | string | Yes | File or folder ID |
| access | string | No | Access level (open, company, collaborators) |
| permissions | object | No | Link permissions |

### `box-folder-create`

Create a folder.

### `box-search`

Search for files.

### `box-metadata-get`

Get file metadata.

## Authentication

Get access token from Box Developer Console.

**Required scopes:**
- `root_readwrite` - Full access
- `item_readwrite` - Item-level access

## Usage Examples

```yaml
# Upload file
- type: box-upload
  config:
    accessToken: "{{secrets.box.accessToken}}"
    folderId: "0"
    filename: "report.pdf"
    content: "base64encodedcontent"

# Create shared link
- type: box-share
  config:
    accessToken: "{{secrets.box.accessToken}}"
    fileId: "123456"
    access: "company"
```

## License

MIT License - See [LICENSE](LICENSE) for details.