# Auth0 Skill

Auth0 authentication platform operations for Atlas agents. Manage users, roles, connections, clients, and logs.

## Overview

The Auth0 skill provides comprehensive node types for Auth0 identity management. It supports user CRUD operations, role management, connection configuration, client management, and log retrieval.

## Node Types

### `auth0-user-list`

List all users.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| domain | string | Yes | Auth0 domain |
| clientId | string | Yes | Management API client ID |
| clientSecret | string | Yes | Management API client secret |
| q | string | No | Search query |
| per_page | integer | No | Results per page |
| page | integer | No | Page number |

**Output:**

```json
{
  "users": [
    {
      "user_id": "auth0|123abc",
      "email": "user@example.com",
      "name": "John Doe",
      "created_at": "2024-01-15T10:30:00.000Z"
    }
  ],
  "total": 1
}
```

### `auth0-user-create`

Create a new user.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| domain | string | Yes | Auth0 domain |
| clientId | string | Yes | Management API client ID |
| clientSecret | string | Yes | Management API client secret |
| connection | string | Yes | Connection name |
| email | string | Yes | User email |
| password | string | No | Initial password |
| name | string | No | User name |

### `auth0-user-update`

Update a user.

### `auth0-user-delete`

Delete a user.

### `auth0-role-list`

List all roles.

### `auth0-role-assign`

Assign role to user.

### `auth0-connection-list`

List all connections.

### `auth0-client-list`

List all applications/clients.

### `auth0-rule-list`

List all rules.

### `auth0-log-list`

List audit logs.

## Authentication

Create a Management API application at Auth0 > Applications > APIs > Auth0 Management API.

**Required scopes:**
- `read:users` - Read users
- `create:users` - Create users
- `update:users` - Update users
- `delete:users` - Delete users
- `read:roles` - Read roles
- `update:roles` - Update roles

## Usage Examples

```yaml
# Create user
- type: auth0-user-create
  config:
    domain: "myorg.auth0.com"
    clientId: "{{secrets.auth0.clientId}}"
    clientSecret: "{{secrets.auth0.clientSecret}}"
    connection: "Username-Password-Authentication"
    email: "newuser@example.com"
    password: "securePassword123!"
    name: "New User"

# Assign role
- type: auth0-role-assign
  config:
    domain: "myorg.auth0.com"
    clientId: "{{secrets.auth0.clientId}}"
    clientSecret: "{{secrets.auth0.clientSecret}}"
    userId: "auth0|123abc"
    roleId: "rol_abc123"
```

## License

MIT License - See [LICENSE](LICENSE) for details.