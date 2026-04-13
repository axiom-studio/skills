# Okta Skill

Okta identity management operations for Atlas agents. Manage users, groups, applications, MFA, and sessions.

## Overview

The Okta skill provides comprehensive node types for Okta identity and access management. It supports user lifecycle management, group operations, application assignments, MFA enrollment, and session management.

## Node Types

### `okta-user-list`

List all users.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| orgUrl | string | Yes | Okta organization URL |
| apiKey | string | Yes | Okta API token |
| search | string | No | Search query |
| limit | integer | No | Max results |

**Output:**

```json
{
  "users": [
    {
      "id": "00u123abc",
      "profile": {
        "login": "user@example.com",
        "firstName": "John",
        "lastName": "Doe",
        "email": "user@example.com"
      },
      "status": "ACTIVE"
    }
  ],
  "count": 1
}
```

### `okta-user-create`

Create a new user.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| orgUrl | string | Yes | Okta organization URL |
| apiKey | string | Yes | Okta API token |
| login | string | Yes | User login |
| email | string | Yes | User email |
| firstName | string | Yes | First name |
| lastName | string | Yes | Last name |
| password | string | No | Initial password |
| groups | array | No | Group IDs to add |

### `okta-user-update`

Update a user.

### `okta-user-deactivate`

Deactivate a user.

### `okta-group-list`

List all groups.

### `okta-group-create`

Create a group.

### `okta-group-add-user`

Add user to group.

### `okta-app-list`

List all applications.

### `okta-app-assign-user`

Assign user to application.

### `okta-mfa-enroll`

Enroll user in MFA.

### `okta-session-revoke`

Revoke user session.

## Authentication

Create an API token at Okta Admin > Security > API > Tokens.

**Required permissions:**
- `okta.users.manage` - User operations
- `okta.groups.manage` - Group operations
- `okta.apps.manage` - Application operations

## Usage Examples

```yaml
# Create user
- type: okta-user-create
  config:
    orgUrl: "https://myorg.okta.com"
    apiKey: "{{secrets.okta.apiKey}}"
    login: "newuser@example.com"
    email: "newuser@example.com"
    firstName: "New"
    lastName: "User"

# Add to group
- type: okta-group-add-user
  config:
    orgUrl: "https://myorg.okta.com"
    apiKey: "{{secrets.okta.apiKey}}"
    groupId: "00g123abc"
    userId: "00u456def"
```

## License

MIT License - See [LICENSE](LICENSE) for details.