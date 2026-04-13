# Linear Skill

Linear issue tracking operations for Atlas agents - issues, comments, cycles, projects.

## Overview

This skill provides comprehensive Linear integration for the Axiom agent platform, enabling automated issue management, team collaboration, and project tracking through a gRPC-based executor system.

## Capabilities

### Issue Management
- **Create Issues** - Create new Linear issues with full field support
- **Update Issues** - Modify existing issues (title, description, assignee, priority, state, etc.)
- **Search Issues** - Search issues using Linear's powerful query syntax

### Collaboration
- **Add Comments** - Post comments to issues with Markdown support

### Agile Operations
- **Cycle Management** - List cycles, get cycle details, retrieve cycle issues
- **Project Operations** - List projects, get project details, retrieve project issues

## Installation

### Prerequisites
- Go 1.25 or later
- Linear workspace with API access
- Linear API token (generate at https://linear.app/settings/api)

### Build from Source

```bash
# Navigate to the skill directory
cd skills.skill-linear

# Download dependencies
go mod tidy

# Build for your platform
CGO_ENABLED=0 go build -o skill-linear .

# Or use the Makefile (if available)
make build
```

### Docker

```bash
# Build the Docker image
docker build -t skill-linear .

# Run the skill
docker run -p 50054:50054 skill-linear
```

## Configuration

### Connection Settings

All executors require the following connection configuration:

| Field | Description | Example |
|-------|-------------|---------|
| `apiToken` | Linear API token | Generated from Linear settings |

### Node Types

| Node Type | Description |
|-----------|-------------|
| `linear-issue-create` | Create a new Linear issue |
| `linear-issue-update` | Update an existing Linear issue |
| `linear-issue-search` | Search issues using Linear query syntax |
| `linear-comment` | Add a comment to an issue |
| `linear-cycle` | Cycle operations (list, get, get issues) |
| `linear-project` | Project operations (list, get, get issues) |

## Usage Examples

### Create an Issue

```yaml
nodeType: linear-issue-create
config:
  apiToken: "{{secrets.linear-api-token}}"
  teamId: TEAM123
  title: Implement user authentication
  description: |
    ## Requirements
    
    Implement OAuth2 authentication for the application.
    
    ### Acceptance Criteria
    - User can log in with Google
    - User can log in with GitHub
    - Session management works correctly
  priority: -2
  assigneeId: USER456
  labelIds:
    - LABEL789
  projectId: PROJECT012
```

### Search Issues

```yaml
nodeType: linear-issue-search
config:
  apiToken: "{{secrets.linear-api-token}}"
  teamId: TEAM123
  query: status:todo assignee:me
  maxResults: 50
```

### Update an Issue

```yaml
nodeType: linear-issue-update
config:
  apiToken: "{{secrets.linear-api-token}}"
  issueId: ISSUE123
  title: Updated title
  description: Updated description with more details
  priority: -3
  stateId: STATE456
```

### Add a Comment

```yaml
nodeType: linear-comment
config:
  apiToken: "{{secrets.linear-api-token}}"
  issueId: ISSUE123
  body: |
    ## Update
    
    This has been implemented and is ready for review.
    
    **Changes:**
    - Added OAuth2 flow
    - Updated session handling
```

### Cycle Operations

```yaml
# List all cycles
nodeType: linear-cycle
config:
  apiToken: "{{secrets.linear-api-token}}"
  teamId: TEAM123
  operation: list

# Get cycle details
nodeType: linear-cycle
config:
  apiToken: "{{secrets.linear-api-token}}"
  operation: get
  cycleId: CYCLE456

# Get cycle issues
nodeType: linear-cycle
config:
  apiToken: "{{secrets.linear-api-token}}"
  operation: getIssues
  cycleId: CYCLE456
```

### Project Operations

```yaml
# List all projects
nodeType: linear-project
config:
  apiToken: "{{secrets.linear-api-token}}"
  teamId: TEAM123
  operation: list

# Get project details
nodeType: linear-project
config:
  apiToken: "{{secrets.linear-api-token}}"
  operation: get
  projectId: PROJECT789

# Get project issues
nodeType: linear-project
config:
  apiToken: "{{secrets.linear-api-token}}"
  operation: getIssues
  projectId: PROJECT789
```

## Output Format

### Issue Create/Update
```json
{
  "id": "ISSUE123",
  "identifier": "TEAM-123",
  "title": "Issue title",
  "description": "Issue description",
  "priority": -2,
  "state": {
    "id": "STATE456",
    "name": "In Progress",
    "color": "#5E6AD2"
  },
  "assignee": {
    "id": "USER789",
    "name": "John Doe",
    "email": "john@example.com"
  },
  "url": "https://linear.app/company/issue/TEAM-123",
  "success": true
}
```

### Issue Search
```json
{
  "issues": [
    {
      "id": "ISSUE123",
      "identifier": "TEAM-123",
      "title": "Issue title",
      "priority": -2,
      "state": {
        "id": "STATE456",
        "name": "In Progress",
        "color": "#5E6AD2"
      },
      "assignee": {
        "id": "USER789",
        "name": "John Doe"
      }
    }
  ],
  "count": 10
}
```

### Comment
```json
{
  "id": "COMMENT123",
  "body": "Comment body",
  "createdAt": "2024-01-15T10:30:00Z",
  "user": {
    "id": "USER789",
    "name": "John Doe",
    "email": "john@example.com"
  },
  "success": true
}
```

### Cycle Operations
```json
{
  "cycles": [
    {
      "id": "CYCLE123",
      "name": "Sprint 1",
      "number": 1,
      "startsAt": "2024-01-01T00:00:00Z",
      "endsAt": "2024-01-14T23:59:59Z"
    }
  ],
  "count": 5
}
```

### Project Operations
```json
{
  "projects": [
    {
      "id": "PROJECT123",
      "name": "Mobile App",
      "description": "Mobile application development",
      "icon": "📱"
    }
  ],
  "count": 3
}
```

## Linear Search Syntax

The search executor supports Linear's powerful query syntax:

| Query | Description |
|-------|-------------|
| `status:todo` | Issues in todo state |
| `status:in progress` | Issues in progress |
| `status:done` | Completed issues |
| `assignee:me` | Issues assigned to you |
| `priority:high` | High priority issues |
| `label:bug` | Issues with bug label |
| `created:this week` | Issues created this week |
| `team:Engineering` | Issues in Engineering team |

For full syntax reference: https://linear.app/docs/search

## Security Best Practices

1. **Use Secrets Management**: Store API tokens using the secrets manager (`{{secrets.linear-api-token}}`)
2. **Principle of Least Privilege**: Use a dedicated service account with minimal required permissions
3. **Rotate Tokens Regularly**: Periodically regenerate API tokens
4. **Audit Access**: Monitor Linear audit logs for automated actions

## Troubleshooting

### Common Issues

**Authentication Failed**
- Verify your API token is valid and not expired
- Check that the token has appropriate permissions
- Ensure the token hasn't been revoked

**Permission Denied**
- Verify the API token has access to the specified team
- Check that the user has appropriate project/cycle permissions
- Ensure the token hasn't expired

**Issue Not Found**
- Verify the issue ID is correct
- Check that the issue exists in the specified team
- Ensure the API token has access to the issue

## License

MIT License - See LICENSE file for details.

## Support

For issues and feature requests, please open an issue on the GitHub repository.
