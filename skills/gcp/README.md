# Google Cloud Platform Skill

Google Cloud Platform operations for Atlas agents. This skill enables AI agents to interact with GCP services including Compute Engine, GKE (Google Kubernetes Engine), Cloud Functions, Pub/Sub, and Cloud Storage.

## Overview

The GCP skill provides comprehensive node types for managing Google Cloud infrastructure. It supports VM instance management, GKE cluster operations, Cloud Functions deployment and invocation, Pub/Sub messaging, and Cloud Storage operations.

## Node Types

### `gcp-instance-list`

List all Compute Engine VM instances.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| projectId | string | Yes | GCP project ID |
| credentials | string | Yes | Service account JSON key |
| zone | string | No | Filter by zone (e.g., us-central1-a) |
| region | string | No | Filter by region |

**Output:**

```json
{
  "instances": [
    {
      "name": "my-instance",
      "id": "123456789",
      "zone": "us-central1-a",
      "machineType": "e2-medium",
      "status": "RUNNING",
      "networkInterfaces": [
        {
          "networkIP": "10.0.0.2",
          "accessConfigs": [{"natIP": "35.192.0.1"}]
        }
      ]
    }
  ],
  "count": 1
}
```

---

### `gcp-instance-start`

Start a stopped Compute Engine instance.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| projectId | string | Yes | GCP project ID |
| credentials | string | Yes | Service account JSON key |
| zone | string | Yes | Zone of the instance |
| instanceName | string | Yes | Instance name |

**Output:**

```json
{
  "success": true,
  "instanceName": "my-instance",
  "status": "PROVISIONING",
  "message": "Instance start operation initiated"
}
```

---

### `gcp-instance-stop`

Stop a running Compute Engine instance.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| projectId | string | Yes | GCP project ID |
| credentials | string | Yes | Service account JSON key |
| zone | string | Yes | Zone of the instance |
| instanceName | string | Yes | Instance name |

---

### `gcp-gke-list`

List all GKE clusters in a project.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| projectId | string | Yes | GCP project ID |
| credentials | string | Yes | Service account JSON key |
| region | string | No | Filter by region |

**Output:**

```json
{
  "clusters": [
    {
      "name": "my-cluster",
      "location": "us-central1",
      "initialNodeCount": 3,
      "currentMasterVersion": "1.28.3-gke.1283000",
      "status": "RUNNING",
      "endpoint": "35.192.0.1"
    }
  ],
  "count": 1
}
```

---

### `gcp-gke-get-credentials`

Get kubeconfig credentials for a GKE cluster.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| projectId | string | Yes | GCP project ID |
| credentials | string | Yes | Service account JSON key |
| region | string | Yes | Cluster region/zone |
| clusterName | string | Yes | Cluster name |

---

### `gcp-function-list`

List all Cloud Functions.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| projectId | string | Yes | GCP project ID |
| credentials | string | Yes | Service account JSON key |
| region | string | No | Filter by region |

---

### `gcp-function-deploy`

Deploy a new Cloud Function or update an existing one.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| projectId | string | Yes | GCP project ID |
| credentials | string | Yes | Service account JSON key |
| region | string | Yes | Deployment region |
| functionName | string | Yes | Function name |
| runtime | string | Yes | Runtime (nodejs18, python311, go121) |
| entryPoint | string | Yes | Function entry point |
| sourceUrl | string | No | Source archive URL |
| sourceDir | string | No | Local source directory |
| triggerHttp | boolean | No | HTTP trigger (default: true) |
| timeout | string | No | Function timeout (default: 60s) |
| memoryMB | integer | No | Memory allocation (default: 256) |
| environmentVariables | object | No | Environment variables |

---

### `gcp-function-invoke`

Invoke a Cloud Function.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| projectId | string | Yes | GCP project ID |
| credentials | string | Yes | Service account JSON key |
| region | string | Yes | Function region |
| functionName | string | Yes | Function name |
| method | string | No | HTTP method (default: POST) |
| body | object | No | Request body |
| headers | object | No | Request headers |

---

### `gcp-pubsub-publish`

Publish a message to a Pub/Sub topic.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| projectId | string | Yes | GCP project ID |
| credentials | string | Yes | Service account JSON key |
| topic | string | Yes | Topic name |
| message | string | Yes | Message body |
| attributes | object | No | Message attributes |
| orderingKey | string | No | Ordering key for ordered delivery |

---

### `gcp-pubsub-subscribe`

Subscribe and pull messages from a Pub/Sub subscription.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| projectId | string | Yes | GCP project ID |
| credentials | string | Yes | Service account JSON key |
| subscription | string | Yes | Subscription name |
| maxMessages | integer | No | Max messages to pull (default: 10) |
| ack | boolean | No | Auto-acknowledge messages (default: true) |

---

### `gcp-storage-list`

List all Cloud Storage buckets.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| projectId | string | Yes | GCP project ID |
| credentials | string | Yes | Service account JSON key |

---

### `gcp-storage-upload`

Upload a file to Cloud Storage.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| projectId | string | Yes | GCP project ID |
| credentials | string | Yes | Service account JSON key |
| bucket | string | Yes | Bucket name |
| objectName | string | Yes | Destination object name |
| content | string | Yes | File content (text or base64) |
| contentType | string | No | Content type |

---

### `gcp-storage-download`

Download a file from Cloud Storage.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| projectId | string | Yes | GCP project ID |
| credentials | string | Yes | Service account JSON key |
| bucket | string | Yes | Bucket name |
| objectName | string | Yes | Object name to download |

---

## Authentication

All node types require GCP service account authentication:

1. **Project ID** - Your GCP project ID
2. **Credentials** - Service account JSON key file content

### Creating a Service Account

```bash
# Create service account
gcloud iam service-accounts create my-sa --display-name "My Service Account"

# Grant permissions
gcloud projects add-iam-policy-binding my-project \
  --member="serviceAccount:my-sa@my-project.iam.gserviceaccount.com" \
  --role="roles/editor"

# Create key
gcloud iam service-accounts keys create key.json \
  --iam-account=my-sa@my-project.iam.gserviceaccount.com
```

### Required Permissions

The service account needs appropriate IAM roles:
- **Compute Admin** (`roles/compute.admin`) - For VM operations
- **Kubernetes Engine Admin** (`roles/container.admin`) - For GKE operations
- **Cloud Functions Developer** (`roles/cloudfunctions.developer`) - For Functions
- **Pub/Sub Editor** (`roles/pubsub.editor`) - For Pub/Sub
- **Storage Admin** (`roles/storage.admin`) - For Cloud Storage

## Usage Examples

### List and Start VMs

```yaml
# List instances
- type: gcp-instance-list
  config:
    projectId: "{{secrets.gcp.projectId}}"
    credentials: "{{secrets.gcp.credentials}}"

# Start an instance
- type: gcp-instance-start
  config:
    projectId: "{{secrets.gcp.projectId}}"
    credentials: "{{secrets.gcp.credentials}}"
    zone: "us-central1-a"
    instanceName: "my-instance"
```

### Deploy and Invoke Cloud Function

```yaml
# Deploy function
- type: gcp-function-deploy
  config:
    projectId: "{{secrets.gcp.projectId}}"
    credentials: "{{secrets.gcp.credentials}}"
    region: "us-central1"
    functionName: "my-function"
    runtime: "nodejs18"
    entryPoint: "handler"
    sourceUrl: "gs://my-bucket/function-source.zip"

# Invoke function
- type: gcp-function-invoke
  config:
    projectId: "{{secrets.gcp.projectId}}"
    credentials: "{{secrets.gcp.credentials}}"
    region: "us-central1"
    functionName: "my-function"
    body:
      event: "test"
```

## License

MIT License - See [LICENSE](LICENSE) for details.
