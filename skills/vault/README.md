# HashiCorp Vault Skill

HashiCorp Vault secrets management operations for Atlas agents.

## Overview

This skill provides comprehensive HashiCorp Vault operations including KV secrets engine, PKI/certificate management, database credential leasing, transit encryption, and token management.

## Features

- **KV Secrets Engine**: Read, write, delete, and list secrets
- **PKI Engine**: Issue, renew, and revoke certificates
- **Database Engine**: Generate dynamic database credentials
- **Transit Engine**: Encrypt and decrypt data
- **Token Management**: Create, renew, and revoke tokens
- **Health Checks**: Monitor Vault status

## Node Types

### KV Secrets Engine

#### vault-kv-get

Read a secret from KV engine (v1 or v2).

**Configuration:**
- `address`: Vault server address
- `token`: Vault token (or use `roleId`/`secretId` for AppRole)
- `path`: Secret path
- `mount`: KV mount path (default: secret)
- `version`: KV version (1 or 2, default: 2)
- `secretVersion`: Specific version to read (v2 only, optional)

**Output:**
- `data`: Secret data as JSON
- `metadata`: Secret metadata (v2 only)

#### vault-kv-put

Write a secret to KV engine.

**Configuration:**
- `address`: Vault server address
- `token`: Vault token
- `path`: Secret path
- `mount`: KV mount path (default: secret)
- `version`: KV version (1 or 2)
- `data`: Secret data (key-value pairs)

**Output:**
- `version`: Secret version created
- `created`: Creation timestamp

#### vault-kv-delete

Delete a secret from KV engine.

**Configuration:**
- `address`: Vault server address
- `token`: Vault token
- `path`: Secret path
- `mount`: KV mount path
- `version`: KV version
- `versions`: Versions to delete (v2 only, optional)

### Generic Secret Operations

#### vault-read

Read from any Vault path.

**Configuration:**
- `address`: Vault server address
- `token`: Vault token
- `path`: Secret path

**Output:**
- `data`: Response data

#### vault-write

Write to any Vault path.

**Configuration:**
- `address`: Vault server address
- `token`: Vault token
- `path`: Secret path
- `data`: Data to write

#### vault-delete

Delete at any Vault path.

**Configuration:**
- `address`: Vault server address
- `token`: Vault token
- `path`: Path to delete

#### vault-list

List secrets at a path.

**Configuration:**
- `address`: Vault server address
- `token`: Vault token
- `path`: Path to list

**Output:**
- `keys`: List of keys at path

### PKI Engine

#### vault-pki-issue

Issue a new certificate.

**Configuration:**
- `address`: Vault server address
- `token`: Vault token
- `role`: PKI role name
- `mount`: PKI mount path (default: pki)
- `commonName`: Certificate common name
- `altNames`: Subject alternative names (comma-separated)
- `ipSans`: IP SANs (comma-separated)
- `ttl`: Certificate TTL (e.g., "720h")
- `format`: Output format (pem, der, pem_bundle)

**Output:**
- `certificate`: Issued certificate
- `privateKey`: Private key
- `caChain`: CA certificate chain
- `serial`: Certificate serial number
- `expiration`: Expiration timestamp

#### vault-pki-renew

Renew a certificate.

**Configuration:**
- `address`: Vault server address
- `token`: Vault token
- `serial`: Certificate serial number
- `mount`: PKI mount path
- `ttl`: New TTL

**Output:**
- `certificate`: Renewed certificate
- `expiration`: New expiration timestamp

#### vault-pki-revoke

Revoke a certificate.

**Configuration:**
- `address`: Vault server address
- `token`: Vault token
- `serial`: Certificate serial number
- `mount`: PKI mount path

**Output:**
- `revocationTime`: Revocation timestamp

### Database Engine

#### vault-database-creds

Generate dynamic database credentials.

**Configuration:**
- `address`: Vault server address
- `token`: Vault token
- `role`: Database role name
- `mount`: Database mount path (default: database)

**Output:**
- `username`: Generated username
- `password`: Generated password
- `leaseId`: Lease ID
- `leaseDuration`: Lease duration in seconds

### Transit Engine

#### vault-transit-encrypt

Encrypt data using Transit engine.

**Configuration:**
- `address`: Vault server address
- `token`: Vault token
- `key`: Transit key name
- `mount`: Transit mount path (default: transit)
- `plaintext`: Data to encrypt (base64 encoded)
- `context`: Encryption context (for derived keys)

**Output:**
- `ciphertext`: Encrypted ciphertext
- `keyVersion`: Key version used

#### vault-transit-decrypt

Decrypt data using Transit engine.

**Configuration:**
- `address`: Vault server address
- `token`: Vault token
- `key`: Transit key name
- `mount`: Transit mount path
- `ciphertext`: Ciphertext to decrypt
- `context`: Decryption context (for derived keys)

**Output:**
- `plaintext`: Decrypted data (base64 encoded)

### Token Management

#### vault-token-create

Create a new token.

**Configuration:**
- `address`: Vault server address
- `token`: Vault token (parent token)
- `policies`: List of policies
- `ttl`: Token TTL
- `renewable`: Is token renewable (default: true)
- `displayName`: Token display name
- `maxTtl`: Maximum TTL
- `numUses`: Number of uses (0 = unlimited)

**Output:**
- `token`: Created token
- `tokenAccessor`: Token accessor
- `policies`: Assigned policies
- `ttl`: Token TTL

#### vault-token-renew

Renew a token.

**Configuration:**
- `address`: Vault server address
- `token`: Token to renew
- `increment`: TTL increment

**Output:**
- `token`: Token (may change)
- `ttl`: New TTL

#### vault-token-revoke

Revoke a token.

**Configuration:**
- `address`: Vault server address
- `token`: Token to revoke

### Health

#### vault-health

Check Vault health status.

**Configuration:**
- `address`: Vault server address

**Output:**
- `initialized`: Is Vault initialized
- `sealed`: Is Vault sealed
- `standby`: Is Vault in standby
- `serverTime`: Server timestamp

## Usage Examples

### Read a Secret

```yaml
- id: read-secret
  type: vault-kv-get
  config:
    address: https://vault.example.com
    token: "{{secrets.vault_token}}"
    path: myapp/config
    mount: secret
```

### Write a Secret

```yaml
- id: write-secret
  type: vault-kv-put
  config:
    address: https://vault.example.com
    token: "{{secrets.vault_token}}"
    path: myapp/database
    data:
      username: app_user
      password: "{{secrets.db_password}}"
```

### Issue a Certificate

```yaml
- id: issue-cert
  type: vault-pki-issue
  config:
    address: https://vault.example.com
    token: "{{secrets.vault_token}}"
    role: myapp-role
    commonName: myapp.example.com
    altNames: "api.example.com,www.example.com"
    ttl: "720h"
```

### Get Database Credentials

```yaml
- id: get-db-creds
  type: vault-database-creds
  config:
    address: https://vault.example.com
    token: "{{secrets.vault_token}}"
    role: myapp-postgres
```

### Encrypt Data

```yaml
- id: encrypt-data
  type: vault-transit-encrypt
  config:
    address: https://vault.example.com
    token: "{{secrets.vault_token}}"
    key: myapp-encryption-key
    plaintext: "{{base64Encode inputs.sensitive_data}}"
```

### Create a Child Token

```yaml
- id: create-token
  type: vault-token-create
  config:
    address: https://vault.example.com
    token: "{{secrets.vault_token}}"
    policies:
      - myapp-read-only
    ttl: "1h"
    displayName: "myapp-temp-token"
```

## Authentication Methods

### Token Authentication

```yaml
token: "hvs.xxxxx"
```

### AppRole Authentication

```yaml
roleId: "xxxxx-xxxxx"
secretId: "xxxxx-xxxxx"
```

## Security Considerations

- Never log or expose tokens
- Use short-lived tokens with renewal
- Implement least-privilege policies
- Use AppRole for machine authentication
- Enable audit logging
- Rotate encryption keys regularly

## Building

```bash
go mod tidy
CGO_ENABLED=0 go build -o skill-vault .
```

## Running

```bash
# Default port 50072
./skill-vault

# Custom port
SKILL_PORT=50080 ./skill-vault
```

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `SKILL_PORT` | 50072 | gRPC server port |

## License

MIT