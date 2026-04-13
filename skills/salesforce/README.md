# Salesforce Skill

Salesforce CRM operations for Atlas agents - accounts, contacts, opportunities, leads, cases.

## Overview

This skill provides gRPC-based executors for interacting with Salesforce CRM. It supports all major Salesforce objects and operations including querying, creating, updating, and deleting records.

## Features

- **SOQL Queries**: Execute complex SOQL queries against any Salesforce object
- **Record Management**: Create, update, and delete records
- **Object Metadata**: Describe objects and retrieve field definitions
- **Query Builder**: Visual query builder mode for constructing SOQL queries
- **Connection Caching**: Efficient connection management with OAuth 2.0 support

## Supported Objects

- Account
- Contact
- Opportunity
- Lead
- Case
- Custom Objects (via API name)

## Node Types

| Node Type | Description |
|-----------|-------------|
| `sf-query` | Execute SOQL queries |
| `sf-create` | Create new records |
| `sf-update` | Update existing records |
| `sf-delete` | Delete records |
| `sf-describe` | Get object metadata |
| `sf-soql` | Execute SOQL with query builder mode |

## Configuration

### Connection Settings

All executors require the following connection parameters:

- **Instance URL**: Your Salesforce instance URL (e.g., `https://mydomain.my.salesforce.com`)
- **Consumer Key**: Salesforce Connected App Consumer Key
- **Consumer Secret**: Salesforce Connected App Consumer Secret
- **Access Token** (Optional): Pre-generated access token for direct authentication

### Setting Up Salesforce Connected App

1. Navigate to **Setup** > **App Manager**
2. Click **New Connected App**
3. Fill in the required information:
   - Connected App Name
   - API Name
   - Contact Email
4. Enable OAuth Settings:
   - Callback URL: `https://localhost/callback` (or your actual callback)
   - Selected OAuth Scopes: `Full access (full)`, `Perform requests on your behalf at any time (refresh_token, offline_access)`
5. Save and note the **Consumer Key** and **Consumer Secret**

## Usage Examples

### Query Accounts

```yaml
nodeType: sf-query
config:
  instanceURL: https://mydomain.my.salesforce.com
  consumerKey: 3MVG9...
  consumerSecret: A1B2C3D4E5F6...
  query: SELECT Id, Name, Type, Industry FROM Account WHERE Type = 'Customer' LIMIT 10
```

### Create Contact

```yaml
nodeType: sf-create
config:
  instanceURL: https://mydomain.my.salesforce.com
  consumerKey: 3MVG9...
  consumerSecret: A1B2C3D4E5F6...
  objectType: Contact
  data:
    FirstName: John
    LastName: Doe
    Email: john.doe@example.com
    Phone: 555-123-4567
```

### Update Opportunity

```yaml
nodeType: sf-update
config:
  instanceURL: https://mydomain.my.salesforce.com
  consumerKey: 3MVG9...
  consumerSecret: A1B2C3D4E5F6...
  objectType: Opportunity
  recordId: 006XXXXXXXXXXXX
  data:
    StageName: Closed Won
    CloseDate: 2026-03-21
```

### Delete Lead

```yaml
nodeType: sf-delete
config:
  instanceURL: https://mydomain.my.salesforce.com
  consumerKey: 3MVG9...
  consumerSecret: A1B2C3D4E5F6...
  objectType: Lead
  recordId: 00QXXXXXXXXXXXX
```

### Describe Account Object

```yaml
nodeType: sf-describe
config:
  instanceURL: https://mydomain.my.salesforce.com
  consumerKey: 3MVG9...
  consumerSecret: A1B2C3D4E5F6...
  objectType: Account
```

### SOQL with Query Builder

```yaml
nodeType: sf-soql
config:
  instanceURL: https://mydomain.my.salesforce.com
  consumerKey: 3MVG9...
  consumerSecret: A1B2C3D4E5F6...
  mode: builder
  objectType: Opportunity
  fields: Id, Name, Amount, StageName, CloseDate
  whereClause: StageName = 'Prospecting' AND Amount > 10000
  limit: 50
```

## Output Format

### Query Results

```json
{
  "records": [
    {
      "Id": "001XXXXXXXXXXXX",
      "Name": "Acme Corporation",
      "Type": "Customer",
      "Industry": "Technology"
    }
  ],
  "totalSize": 1,
  "done": true,
  "nextURL": ""
}
```

### Create Result

```json
{
  "id": "001XXXXXXXXXXXX",
  "success": true,
  "object": "Account"
}
```

### Update/Delete Result

```json
{
  "success": true,
  "recordId": "001XXXXXXXXXXXX",
  "object": "Account"
}
```

### Describe Result

```json
{
  "name": "Account",
  "label": "Account",
  "keyPrefix": "001",
  "createable": true,
  "updateable": true,
  "deletable": true,
  "fields": [
    {
      "name": "Name",
      "label": "Account Name",
      "type": "string",
      "length": 255,
      "required": true,
      "creatable": true,
      "updateable": true,
      "calculated": false
    }
  ],
  "fieldCount": 45
}
```

## Building

### Local Build

```bash
go mod tidy
CGO_ENABLED=0 go build -o skill-salesforce .
```

### Docker Build

```bash
docker build -t skill-salesforce .
```

### Cross-Platform Build

```bash
# Linux AMD64
GOOS=linux GOARCH=amd64 go build -o skill-salesforce-linux-amd64 .

# Linux ARM64
GOOS=linux GOARCH=arm64 go build -o skill-salesforce-linux-arm64 .

# macOS AMD64
GOOS=darwin GOARCH=amd64 go build -o skill-salesforce-darwin-amd64 .

# macOS ARM64
GOOS=darwin GOARCH=arm64 go build -o skill-salesforce-darwin-arm64 .
```

## Running

```bash
# Default port 50053
./skill-salesforce

# Custom port
SKILL_PORT=50060 ./skill-salesforce
```

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `SKILL_PORT` | 50053 | gRPC server port |

## Security Considerations

- Store Consumer Secret and Access Tokens securely using Atlas secrets management
- Use the minimum required OAuth scopes for your Connected App
- Consider using named credentials or binding references for sensitive values
- Implement proper error handling for API rate limits

## Rate Limits

Salesforce API has rate limits based on your org edition. The skill does not implement automatic retry logic, so consider:

- Implementing exponential backoff in your workflows
- Monitoring API usage in Salesforce Setup
- Using bulk APIs for large data operations

## Troubleshooting

### Authentication Errors

- Verify Consumer Key and Secret are correct
- Ensure Connected App has proper OAuth scopes
- Check that the user has API access enabled

### Query Errors

- Validate SOQL syntax
- Check field names match object schema
- Verify object permissions

### Connection Issues

- Confirm Instance URL is correct (include https://)
- Check network connectivity to Salesforce
- Verify firewall/proxy settings

## License

MIT

## Author

Axiom Studio <engineering@axiomstudio.ai>
