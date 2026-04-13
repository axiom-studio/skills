# AWS S3 Skill

AWS S3 object storage operations for Atlas agents. Manage buckets and objects with full CRUD operations.

## Overview

The S3 skill provides comprehensive node types for AWS S3 storage operations. It supports bucket management, object upload/download, presigned URLs, and lifecycle policies.

## Node Types

### `s3-list-buckets`

List all S3 buckets.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| accessKeyId | string | Yes | AWS access key ID |
| secretAccessKey | string | Yes | AWS secret access key |
| region | string | No | AWS region |

**Output:**

```json
{
  "buckets": [
    {
      "name": "my-bucket",
      "creationDate": "2024-01-15T10:30:00.000Z",
      "region": "us-east-1"
    }
  ],
  "count": 1
}
```

### `s3-list-objects`

List objects in a bucket.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| accessKeyId | string | Yes | AWS access key ID |
| secretAccessKey | string | Yes | AWS secret access key |
| bucket | string | Yes | Bucket name |
| prefix | string | No | Key prefix filter |
| delimiter | string | No | Delimiter for grouping |
| maxKeys | integer | No | Max keys to return |

### `s3-upload`

Upload an object to S3.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| accessKeyId | string | Yes | AWS access key ID |
| secretAccessKey | string | Yes | AWS secret access key |
| bucket | string | Yes | Bucket name |
| key | string | Yes | Object key |
| content | string | Yes | File content (text or base64) |
| contentType | string | No | Content type |
| metadata | object | No | User metadata |
| acl | string | No | ACL (private, public-read, etc.) |

**Output:**

```json
{
  "success": true,
  "bucket": "my-bucket",
  "key": "path/to/object.json",
  "etag": "\"abc123\"",
  "versionId": "version123"
}
```

### `s3-download`

Download an object from S3.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| accessKeyId | string | Yes | AWS access key ID |
| secretAccessKey | string | Yes | AWS secret access key |
| bucket | string | Yes | Bucket name |
| key | string | Yes | Object key |

### `s3-delete`

Delete an object or bucket.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| accessKeyId | string | Yes | AWS access key ID |
| secretAccessKey | string | Yes | AWS secret access key |
| bucket | string | Yes | Bucket name |
| key | string | No | Object key (omit for bucket deletion) |

### `s3-presign`

Generate a presigned URL.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| accessKeyId | string | Yes | AWS access key ID |
| secretAccessKey | string | Yes | AWS secret access key |
| bucket | string | Yes | Bucket name |
| key | string | Yes | Object key |
| operation | string | No | get/put object (default: get) |
| expiresIn | integer | No | URL expiry seconds (default: 3600) |

### `s3-bucket-create`

Create a bucket.

### `s3-bucket-delete`

Delete a bucket.

### `s3-copy-object`

Copy an object.

## Authentication

Use IAM credentials with appropriate S3 permissions.

**Required IAM permissions:**
- `s3:ListAllMyBuckets`
- `s3:ListBucket`
- `s3:GetObject`
- `s3:PutObject`
- `s3:DeleteObject`

## Usage Examples

```yaml
# Upload file
- type: s3-upload
  config:
    accessKeyId: "{{secrets.aws.accessKeyId}}"
    secretAccessKey: "{{secrets.aws.secretAccessKey}}"
    region: "us-east-1"
    bucket: "my-bucket"
    key: "data/report.json"
    content: '{"status": "completed"}'
    contentType: "application/json"

# Generate presigned URL
- type: s3-presign
  config:
    accessKeyId: "{{secrets.aws.accessKeyId}}"
    secretAccessKey: "{{secrets.aws.secretAccessKey}}"
    bucket: "my-bucket"
    key: "private/document.pdf"
    expiresIn: 3600
```

## License

MIT License - See [LICENSE](LICENSE) for details.