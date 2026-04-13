# Dropbox Skill

Dropbox file storage operations for Atlas agents. This skill provides comprehensive Dropbox integration for uploading, downloading, listing, deleting, sharing, and searching files.

## Features

- **Upload Files**: Upload content to Dropbox with configurable overwrite modes
- **Download Files**: Download file contents from Dropbox
- **List Folders**: List contents of Dropbox folders with pagination support
- **Delete Files/Folders**: Remove files and folders from Dropbox
- **Share Files**: Create shared links for files and folders
- **Search Files**: Search for files by name or content

## Installation

### Prerequisites

- Go 1.25 or later
- Dropbox API access token
- Axiom SDK

### Build from Source

```bash
# Clone the repository
git clone <repository-url>
cd skills.skill-dropbox

# Download dependencies
go mod tidy

# Build for your platform
go build -o skill-dropbox .

# Or build for specific platforms
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o skill-dropbox-linux-amd64 .
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -o skill-dropbox-darwin-arm64 .
```

### Docker Build

```bash
docker build -t skill-dropbox .
```

## Configuration

### Required Configuration

| Field | Description |
|-------|-------------|
| `accessToken` | Dropbox API access token (supports `{{bindings.xxx}}` for secure access) |

### Obtaining a Dropbox Access Token

1. Go to [Dropbox App Console](https://www.dropbox.com/developers/apps)
2. Create a new app or select an existing one
3. Generate an access token in the app settings
4. For production use, implement OAuth 2.0 flow

## Node Types

### dropbox-upload

Upload files to Dropbox.

**Configuration:**
- `accessToken` (required): Dropbox access token
- `path` (required): Destination path in Dropbox (e.g., `/folder/file.txt`)
- `content` (required): File content to upload
- `mode` (optional): Upload mode - `add` (default), `update`, `add_no_overwrite`

**Example:**
```yaml
- type: dropbox-upload
  config:
    accessToken: "{{bindings.dropbox_token}}"
    path: "/documents/report.txt"
    content: "Hello, Dropbox!"
    mode: add
```

**Output:**
```json
{
  "success": true,
  "path": "/documents/report.txt",
  "size": 15,
  "message": "Successfully uploaded to /documents/report.txt"
}
```

### dropbox-download

Download files from Dropbox.

**Configuration:**
- `accessToken` (required): Dropbox access token
- `path` (required): Path to file in Dropbox

**Example:**
```yaml
- type: dropbox-download
  config:
    accessToken: "{{bindings.dropbox_token}}"
    path: "/documents/report.txt"
```

**Output:**
```json
{
  "success": true,
  "path": "/documents/report.txt",
  "content": "File contents here...",
  "size": 1234,
  "message": "Successfully downloaded /documents/report.txt"
}
```

### dropbox-list

List contents of a Dropbox folder.

**Configuration:**
- `accessToken` (required): Dropbox access token
- `path` (optional): Folder path to list (empty for root)
- `recursive` (optional): List recursively (default: false)
- `limit` (optional): Maximum entries to return (default: 100)

**Example:**
```yaml
- type: dropbox-list
  config:
    accessToken: "{{bindings.dropbox_token}}"
    path: "/documents"
    recursive: false
    limit: 50
```

**Output:**
```json
{
  "success": true,
  "path": "/documents",
  "entries": [
    {
      "name": "report.txt",
      "path": "/documents/report.txt",
      "type": "file",
      "size": 1234,
      "modified": "2026-03-21T10:00:00Z"
    },
    {
      "name": "images",
      "path": "/documents/images",
      "type": "folder"
    }
  ],
  "count": 2,
  "hasMore": false,
  "message": "Listed 2 items in /documents"
}
```

### dropbox-delete

Delete files or folders from Dropbox.

**Configuration:**
- `accessToken` (required): Dropbox access token
- `path` (required): Path to file or folder to delete

**Example:**
```yaml
- type: dropbox-delete
  config:
    accessToken: "{{bindings.dropbox_token}}"
    path: "/documents/old-report.txt"
```

**Output:**
```json
{
  "success": true,
  "path": "/documents/old-report.txt",
  "message": "Successfully deleted /documents/old-report.txt"
}
```

### dropbox-share

Create shared links for Dropbox files or folders.

**Configuration:**
- `accessToken` (required): Dropbox access token
- `path` (required): Path to file or folder to share
- `viewerInfo` (optional): Request viewer info (default: false)
- `password` (optional): Password for the shared link
- `expires` (optional): Expiration time in ISO 8601 format

**Example:**
```yaml
- type: dropbox-share
  config:
    accessToken: "{{bindings.dropbox_token}}"
    path: "/documents/report.txt"
    viewerInfo: false
```

**Output:**
```json
{
  "success": true,
  "path": "/documents/report.txt",
  "url": "https://www.dropbox.com/s/abc123/report.txt",
  "id": "id:abc123",
  "name": "report.txt",
  "message": "Successfully created shared link for /documents/report.txt"
}
```

### dropbox-search

Search for files in Dropbox.

**Configuration:**
- `accessToken` (required): Dropbox access token
- `query` (required): Search query string
- `path` (optional): Restrict search to this path
- `maxResults` (optional): Maximum number of results (default: 20)
- `orderBy` (optional): Sort order - `relevance` (default), `modified`, `size`

**Example:**
```yaml
- type: dropbox-search
  config:
    accessToken: "{{bindings.dropbox_token}}"
    query: "report"
    path: "/documents"
    maxResults: 10
    orderBy: "modified"
```

**Output:**
```json
{
  "success": true,
  "query": "report",
  "results": [
    {
      "name": "report.txt",
      "path": "/documents/report.txt",
      "type": "file",
      "size": 1234,
      "modified": "2026-03-21T10:00:00Z",
      "matchScore": 0.95
    }
  ],
  "count": 1,
  "hasMore": false,
  "message": "Found 1 results for 'report'"
}
```

## Security Best Practices

1. **Store access tokens securely**: Use Axiom bindings/secrets to store Dropbox access tokens
2. **Use minimal permissions**: Request only the permissions your workflow needs
3. **Rotate tokens regularly**: Implement token rotation for production environments
4. **Validate paths**: Sanitize user-provided paths to prevent path traversal

## Development

### Running Tests

```bash
go test ./...
```

### Building for Multiple Platforms

```bash
# Linux AMD64
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o skill-dropbox-linux-amd64 .

# Linux ARM64
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o skill-dropbox-linux-arm64 .

# macOS AMD64
CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -o skill-dropbox-darwin-amd64 .

# macOS ARM64
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -o skill-dropbox-darwin-arm64 .
```

## Troubleshooting

### Common Issues

**Authentication Failed**
- Verify your access token is valid and not expired
- Check that the app has the required permissions

**Path Not Found**
- Ensure the path starts with a forward slash `/`
- Verify the file or folder exists in your Dropbox

**Permission Denied**
- Check that your app has the required scope permissions
- For shared folders, ensure you have access

## License

MIT License - see LICENSE file for details.

## Support

For issues and feature requests, please open an issue on the GitHub repository.

**Author:** Axiom Studio  
**Email:** engineering@axiomstudio.ai
