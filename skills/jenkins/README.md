# Jenkins Skill

Jenkins CI/CD server operations for Atlas agents. Manage jobs, builds, nodes, and queue operations.

## Overview

The Jenkins skill provides comprehensive node types for Jenkins automation. It supports job triggering, build monitoring, log retrieval, node management, and queue operations.

## Node Types

### `jenkins-job-list`

List all jobs on the Jenkins server.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| url | string | Yes | Jenkins server URL |
| username | string | Yes | Jenkins username |
| password | string | Yes | API token or password |
| folder | string | No | Folder path to list jobs from |

**Output:**

```json
{
  "jobs": [
    {
      "name": "my-job",
      "url": "http://jenkins/job/my-job",
      "color": "blue",
      "lastBuild": {"number": 42, "result": "SUCCESS"}
    }
  ],
  "count": 1
}
```

---

### `jenkins-job-trigger`

Trigger a Jenkins job/build.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| url | string | Yes | Jenkins server URL |
| username | string | Yes | Jenkins username |
| password | string | Yes | API token or password |
| jobName | string | Yes | Job name |
| parameters | object | No | Build parameters |
| folder | string | No | Folder path |

**Output:**

```json
{
  "success": true,
  "buildNumber": 43,
  "queueId": 123,
  "message": "Build triggered successfully"
}
```

---

### `jenkins-job-status`

Get job/build status.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| url | string | Yes | Jenkins server URL |
| username | string | Yes | Jenkins username |
| password | string | Yes | API token or password |
| jobName | string | Yes | Job name |
| buildNumber | integer | No | Build number (default: last) |

**Output:**

```json
{
  "number": 43,
  "result": "SUCCESS",
  "building": false,
  "duration": 120000,
  "timestamp": 1705312200000
}
```

---

### `jenkins-build-list`

List builds for a job.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| url | string | Yes | Jenkins server URL |
| username | string | Yes | Jenkins username |
| password | string | Yes | API token or password |
| jobName | string | Yes | Job name |
| limit | integer | No | Max builds to return |

---

### `jenkins-build-logs`

Get build console output/logs.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| url | string | Yes | Jenkins server URL |
| username | string | Yes | Jenkins username |
| password | string | Yes | API token or password |
| jobName | string | Yes | Job name |
| buildNumber | integer | No | Build number (default: last) |
| startLine | integer | No | Start line for logs |

**Output:**

```json
{
  "logs": "Started by user admin\nBuilding in workspace /var/jenkins/...\nSUCCESS",
  "buildNumber": 43
}
```

---

### `jenkins-build-abort`

Abort a running build.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| url | string | Yes | Jenkins server URL |
| username | string | Yes | Jenkins username |
| password | string | Yes | API token or password |
| jobName | string | Yes | Job name |
| buildNumber | integer | Yes | Build number to abort |

---

### `jenkins-node-list`

List all Jenkins nodes/agents.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| url | string | Yes | Jenkins server URL |
| username | string | Yes | Jenkins username |
| password | string | Yes | API token or password |

**Output:**

```json
{
  "nodes": [
    {
      "name": "master",
      "displayName": "master",
      "numExecutors": 2,
      "description": "Jenkins master node"
    },
    {
      "name": "agent-1",
      "displayName": "agent-1",
      "numExecutors": 4,
      "description": "Build agent 1"
    }
  ]
}
```

---

### `jenkins-queue-list`

List items in the build queue.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| url | string | Yes | Jenkins server URL |
| username | string | Yes | Jenkins username |
| password | string | Yes | API token or password |

---

### `jenkins-view-list`

List Jenkins views.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| url | string | Yes | Jenkins server URL |
| username | string | Yes | Jenkins username |
| password | string | Yes | API token or password |

---

### `jenkins-credential-list`

List credentials (requires admin).

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| url | string | Yes | Jenkins server URL |
| username | string | Yes | Jenkins username |
| password | string | Yes | API token or password |
| domain | string | No | Credential domain |

---

### `jenkins-plugin-list`

List installed plugins.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| url | string | Yes | Jenkins server URL |
| username | string | Yes | Jenkins username |
| password | string | Yes | API token or password |

---

## Authentication

Create an API token at Jenkins > User > Configure > API Token.

**Required permissions:**
- `Job/Read` - For listing jobs
- `Job/Build` - For triggering builds
- `Job/Workspace` - For accessing workspace
- `Node/Read` - For listing nodes

### Creating API Token

1. Go to Jenkins > User > Your Username > Configure
2. Click "Add new Token"
3. Give it a name
4. Copy the token immediately

## Usage Examples

### Trigger Build and Monitor

```yaml
# Trigger build
- type: jenkins-job-trigger
  config:
    url: "https://jenkins.example.com"
    username: "atlas-bot"
    password: "{{secrets.jenkins.apiToken}}"
    jobName: "deploy-production"
    parameters:
      VERSION: "1.2.3"
      ENVIRONMENT: "production"

# Check status
- type: jenkins-job-status
  config:
    url: "https://jenkins.example.com"
    username: "atlas-bot"
    password: "{{secrets.jenkins.apiToken}}"
    jobName: "deploy-production"
    buildNumber: 43
```

### Get Build Logs

```yaml
- type: jenkins-build-logs
  config:
    url: "https://jenkins.example.com"
    username: "atlas-bot"
    password: "{{secrets.jenkins.apiToken}}"
    jobName: "build-app"
    buildNumber: 42
```

## License

MIT License - See [LICENSE](LICENSE) for details.