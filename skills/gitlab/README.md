# GitLab Skill

GitLab repository and CI/CD operations for Atlas agents. Manage projects, pipelines, merge requests, issues, and repository files.

## Overview

The GitLab skill provides comprehensive node types for GitLab DevOps operations. It supports project management, CI/CD pipeline control, merge request workflows, issue tracking, and repository file operations.

## Node Types

### `gitlab-project-list`

List all projects accessible by the authenticated user.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| url | string | No | GitLab URL (default: https://gitlab.com) |
| token | string | Yes | Personal access token |
| owned | boolean | No | Only return projects owned by user |
| membership | boolean | No | Only return projects user is member of |
| search | string | No | Search query for project name |
| perPage | integer | No | Results per page (default: 20) |

**Output:**

```json
{
  "projects": [
    {
      "id": 123,
      "name": "my-project",
      "path_with_namespace": "group/my-project",
      "web_url": "https://gitlab.com/group/my-project",
      "visibility": "private",
      "last_activity_at": "2024-01-15T10:30:00.000Z"
    }
  ],
  "count": 1
}
```

---

### `gitlab-project-get`

Get details of a specific project.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| url | string | No | GitLab URL |
| token | string | Yes | Personal access token |
| projectId | string | Yes | Project ID or path |

---

### `gitlab-pipeline-trigger`

Trigger a new pipeline.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| url | string | No | GitLab URL |
| token | string | Yes | Personal access token |
| projectId | string | Yes | Project ID or path |
| ref | string | Yes | Branch or tag name |
| variables | object | No | Pipeline variables |

**Output:**

```json
{
  "success": true,
  "pipelineId": 456,
  "webUrl": "https://gitlab.com/group/my-project/-/pipelines/456",
  "status": "pending"
}
```

---

### `gitlab-pipeline-status`

Get pipeline status.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| url | string | No | GitLab URL |
| token | string | Yes | Personal access token |
| projectId | string | Yes | Project ID |
| pipelineId | integer | Yes | Pipeline ID |

**Output:**

```json
{
  "id": 456,
  "status": "running",
  "ref": "main",
  "sha": "abc123",
  "webUrl": "https://gitlab.com/..."
}
```

---

### `gitlab-job-list`

List jobs in a pipeline.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| url | string | No | GitLab URL |
| token | string | Yes | Personal access token |
| projectId | string | Yes | Project ID |
| pipelineId | integer | Yes | Pipeline ID |
| scope | array | No | Job scopes (created, pending, running, failed, success) |

---

### `gitlab-job-logs`

Get job trace/logs.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| url | string | No | GitLab URL |
| token | string | Yes | Personal access token |
| projectId | string | Yes | Project ID |
| jobId | integer | Yes | Job ID |

---

### `gitlab-mr-list`

List merge requests.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| url | string | No | GitLab URL |
| token | string | Yes | Personal access token |
| projectId | string | Yes | Project ID |
| state | string | No | MR state (opened, closed, merged, all) |
| labels | array | No | Filter by labels |
| author | string | No | Filter by author username |

---

### `gitlab-mr-create`

Create a merge request.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| url | string | No | GitLab URL |
| token | string | Yes | Personal access token |
| projectId | string | Yes | Project ID |
| sourceBranch | string | Yes | Source branch name |
| targetBranch | string | Yes | Target branch name |
| title | string | Yes | MR title |
| description | string | No | MR description (markdown) |
| assigneeIds | array | No | User IDs to assign |
| reviewerIds | array | No | User IDs for review |
| labels | array | No | Labels to apply |
| removeSourceBranch | boolean | No | Remove source branch after merge |
| squash | boolean | No | Squash commits on merge |

**Output:**

```json
{
  "success": true,
  "mrId": 789,
  "iid": 42,
  "webUrl": "https://gitlab.com/group/my-project/-/merge_requests/42"
}
```

---

### `gitlab-mr-merge`

Merge a merge request.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| url | string | No | GitLab URL |
| token | string | Yes | Personal access token |
| projectId | string | Yes | Project ID |
| mrIid | integer | Yes | MR internal ID |
| squashCommitMessage | string | No | Custom squash message |
| shouldRemoveSourceBranch | boolean | No | Remove source branch |

---

### `gitlab-issue-list`

List issues.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| url | string | No | GitLab URL |
| token | string | Yes | Personal access token |
| projectId | string | Yes | Project ID |
| state | string | No | Issue state (opened, closed, all) |
| labels | array | No | Filter by labels |
| assignee | string | No | Filter by assignee username |

---

### `gitlab-issue-create`

Create an issue.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| url | string | No | GitLab URL |
| token | string | Yes | Personal access token |
| projectId | string | Yes | Project ID |
| title | string | Yes | Issue title |
| description | string | No | Issue description |
| labels | array | No | Labels |
| assigneeIds | array | No | Assignee user IDs |
| dueDate | string | No | Due date (YYYY-MM-DD) |

---

### `gitlab-variable-list`

List CI/CD variables.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| url | string | No | GitLab URL |
| token | string | Yes | Personal access token |
| projectId | string | Yes | Project ID |

---

### `gitlab-variable-set`

Set a CI/CD variable.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| url | string | No | GitLab URL |
| token | string | Yes | Personal access token |
| projectId | string | Yes | Project ID |
| key | string | Yes | Variable key |
| value | string | Yes | Variable value |
| protected | boolean | No | Only for protected branches |
| masked | boolean | No | Mask in logs |
| environmentScope | string | No | Environment scope (default: *) |

---

### `gitlab-repo-file`

Get repository file content.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| url | string | No | GitLab URL |
| token | string | Yes | Personal access token |
| projectId | string | Yes | Project ID |
| filePath | string | Yes | File path |
| ref | string | Yes | Branch or commit SHA |

**Output:**

```json
{
  "content": "file content here...",
  "encoding": "text",
  "size": 1024
}
```

---

## Authentication

Create a Personal Access Token at GitLab > Settings > Access Tokens.

**Required scopes:**
- `api` - Full API access
- `read_repository` - Read repository
- `write_repository` - Write repository

### Creating a Token

1. Go to GitLab > Settings > Access Tokens
2. Add a name and expiration date
3. Select scopes: `api`, `read_repository`, `write_repository`
4. Click "Create personal access token"
5. Copy the token immediately

## Usage Examples

### Trigger Pipeline and Monitor

```yaml
# Trigger pipeline
- type: gitlab-pipeline-trigger
  config:
    url: "https://gitlab.com"
    token: "{{secrets.gitlab.token}}"
    projectId: "group/my-project"
    ref: "main"
    variables:
      ENVIRONMENT: "production"
      DEPLOY_VERSION: "1.2.3"

# Check status
- type: gitlab-pipeline-status
  config:
    token: "{{secrets.gitlab.token}}"
    projectId: "group/my-project"
    pipelineId: 456
```

### Create and Merge MR

```yaml
# Create MR
- type: gitlab-mr-create
  config:
    token: "{{secrets.gitlab.token}}"
    projectId: "123"
    sourceBranch: "feature/new-api"
    targetBranch: "main"
    title: "Add new API endpoint"
    description: |
      ## Changes
      - Added /api/v2/users endpoint
      - Updated authentication middleware
      
      ## Testing
      - Unit tests added
      - Integration tests passing
    labels: ["enhancement", "api"]
    removeSourceBranch: true

# Merge MR
- type: gitlab-mr-merge
  config:
    token: "{{secrets.gitlab.token}}"
    projectId: "123"
    mrIid: 42
```

### Set CI/CD Variables

```yaml
- type: gitlab-variable-set
  config:
    token: "{{secrets.gitlab.token}}"
    projectId: "123"
    key: "AWS_ACCESS_KEY_ID"
    value: "{{secrets.aws.accessKeyId}}"
    protected: true
    masked: true
```

## Error Handling

All node types return structured error responses:

```json
{
  "error": "404 Project Not Found",
  "message": "The project could not be found"
}
```

## License

MIT License - See [LICENSE](LICENSE) for details.