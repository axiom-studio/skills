# Airtable Skill

Airtable database operations for Atlas agents. This skill enables seamless integration with Airtable bases, allowing you to list, create, update, delete, and search records programmatically.

## Features

- **List Records**: Retrieve records from Airtable tables with optional filtering and sorting
- **Create Records**: Add new records to Airtable tables
- **Update Records**: Modify existing records in Airtable tables
- **Delete Records**: Remove records from Airtable tables
- **Search Records**: Find records matching specific criteria

## Installation

### Using Docker

```bash
docker build -t skill-airtable .
docker run -p 50052:50052 -e AIRTABLE_API_KEY=your_api_key skill-airtable
```

### Building Locally

```bash
go build -o skill-airtable .
./skill-airtable
```

## Configuration

### Environment Variables

| Variable | Description | Required |
|----------|-------------|----------|
| `AIRTABLE_API_KEY` | Your Airtable API key | Yes |
| `SKILL_PORT` | gRPC server port (default: 50052) | No |

### Node Types

#### airtable-list

List records from an Airtable table.

**Configuration:**
- `apiKey`: Airtable API key (or use environment variable)
- `baseId`: The ID of the Airtable base
- `table`: The name of the table to list records from
- `maxRecords`: Maximum number of records to return (optional)
- `filterByFormula`: Airtable formula to filter records (optional)
- `sort`: Array of sort options (optional)
- `view`: View ID to use for listing (optional)

**Output:**
- `records`: Array of record objects
- `count`: Number of records returned

#### airtable-create

Create new records in an Airtable table.

**Configuration:**
- `apiKey`: Airtable API key
- `baseId`: The ID of the Airtable base
- `table`: The name of the table
- `records`: Array of record objects with `fields`

**Output:**
- `createdRecords`: Array of created record objects
- `count`: Number of records created

#### airtable-update

Update existing records in an Airtable table.

**Configuration:**
- `apiKey`: Airtable API key
- `baseId`: The ID of the Airtable base
- `table`: The name of the table
- `records`: Array of record objects with `id` and `fields`

**Output:**
- `updatedRecords`: Array of updated record objects
- `count`: Number of records updated

#### airtable-delete

Delete records from an Airtable table.

**Configuration:**
- `apiKey`: Airtable API key
- `baseId`: The ID of the Airtable base
- `table`: The name of the table
- `recordIds`: Array of record IDs to delete

**Output:**
- `deletedRecords`: Array of deleted record IDs
- `count`: Number of records deleted

#### airtable-search

Search for records matching specific criteria.

**Configuration:**
- `apiKey`: Airtable API key
- `baseId`: The ID of the Airtable base
- `table`: The name of the table
- `field`: Field name to search in
- `value`: Value to search for
- `exactMatch`: Whether to match exactly (default: false)

**Output:**
- `records`: Array of matching record objects
- `count`: Number of records found

## Usage Examples

### List Records

```yaml
nodeType: airtable-list
config:
  apiKey: "{{secrets.airtable_api_key}}"
  baseId: "appXXXXXXXXXXXXXX"
  table: "Users"
  maxRecords: 100
  filterByFormula: "{Status} = 'Active'"
```

### Create Record

```yaml
nodeType: airtable-create
config:
  apiKey: "{{secrets.airtable_api_key}}"
  baseId: "appXXXXXXXXXXXXXX"
  table: "Users"
  records:
    - fields:
        Name: "John Doe"
        Email: "john@example.com"
        Status: "Active"
```

### Update Record

```yaml
nodeType: airtable-update
config:
  apiKey: "{{secrets.airtable_api_key}}"
  baseId: "appXXXXXXXXXXXXXX"
  table: "Users"
  records:
    - id: "recXXXXXXXXXXXXXX"
      fields:
        Status: "Inactive"
```

### Delete Records

```yaml
nodeType: airtable-delete
config:
  apiKey: "{{secrets.airtable_api_key}}"
  baseId: "appXXXXXXXXXXXXXX"
  table: "Users"
  recordIds:
    - "recXXXXXXXXXXXXXX"
    - "recYYYYYYYYYYYYY"
```

### Search Records

```yaml
nodeType: airtable-search
config:
  apiKey: "{{secrets.airtable_api_key}}"
  baseId: "appXXXXXXXXXXXXXX"
  table: "Users"
  field: "Email"
  value: "john@example.com"
  exactMatch: true
```

## Getting Your Airtable Credentials

1. **API Key**: Generate an API key from your Airtable account settings at https://airtable.com/account
2. **Base ID**: Found in the API documentation for your base at https://airtable.com/api
3. **Table Name**: The exact name of the table as shown in your Airtable base

## Security Best Practices

- Always use secrets management (e.g., `{{secrets.airtable_api_key}}`) instead of hardcoding API keys
- Limit API key permissions to only the bases and tables needed
- Rotate API keys regularly
- Use filter formulas to limit data exposure

## Development

```bash
# Install dependencies
go mod tidy

# Build
go build -o skill-airtable .

# Run
./skill-airtable

# Test
go test ./...
```

## License

MIT License - see LICENSE file for details.

## Support

For issues and feature requests, please open an issue on GitHub or contact engineering@axiomstudio.ai.
