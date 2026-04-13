# Confluence Skill

Confluence wiki operations for Atlas agents. This skill enables automated interactions with Atlassian Confluence, including page management, space operations, attachment handling, comments, and search functionality.

## Features

- **Page Management**: Create, read, update, delete, and list Confluence pages
- **Space Operations**: List and get Confluence spaces
- **Attachment Management**: Upload, list, and delete page attachments
- **Comments**: Create and list comments on pages
- **Search**: Full-text search using Confluence Query Language (CQL)

## Installation

### Prerequisites

- Go 1.21 or later
- Confluence Cloud instance (Server/Data Center may require API adjustments)
- Confluence API token or Personal Access Token (PAT)

### Building

```bash
# Build for current platform
go build -o skill-confluence .

# Build for Linux AMD64
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o skill-confluence-linux-amd64 .

# Build for Linux ARM64
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o skill-confluence-linux-arm64 .

# Build for macOS AMD64
CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -o skill-confluence-darwin-amd64 .

# Build for macOS ARM64
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -o skill-confluence-darwin-arm64 .
```

### Docker

```bash
# Build Docker image
docker build -t axiom-studio/skill-confluence:latest .

# Run the skill
docker run -p 50053:50053 axiom-studio/skill-confluence:latest
```

## Configuration

### Required Credentials

1. **Base URL**: Your Confluence instance URL (e.g., `https://your-domain.atlassian.net/wiki`)
2. **API Token**: Generate from [Atlassian Account Settings](https://id.atlassian.com/manage-profile/security/api-tokens)
3. **Email**: Your Atlassian account email (required for API token auth, not for PAT)

### Authentication

This skill supports two authentication methods:

1. **API Token + Email**: Use Basic Auth with your email and API token
2. **Personal Access Token (PAT)**: Use Bearer token authentication (email not required)

## Node Types

### confluence-page-list

List pages in a Confluence space with filtering and pagination.

**Parameters:**
- `baseUrl`: Confluence base URL
- `apiToken`: API token for authentication
- `email`: Account email (optional for PAT)
- `spaceKey`: (Optional) Filter by space key
- `parentId`: (Optional) Get child pages of a specific page
- `title`: (Optional) Filter by page title
- `expand`: (Optional) Fields to expand (e.g., `body.storage,version,space`)
- `limit`: Maximum results (default: 25, max: 100)
- `start`: Starting index for pagination (default: 0)
- `orderBy`: Order by field (e.g., `title`, `created`, `modified`)

**Output:**
- `pages`: Array of page objects
- `count`: Number of pages returned
- `size`: Total size from API
- `start`: Start index
- `limit`: Limit used
- `hasNext`: Boolean indicating if more results exist
- `raw`: Full API response

### confluence-page-get

Retrieve Confluence page content by ID or search by title.

**Parameters:**
- `baseUrl`: Confluence base URL
- `apiToken`: API token
- `email`: Account email
- `pageId`: (Optional) Specific page ID for direct lookup
- `spaceKey`: (Optional) Space key for search
- `title`: (Optional) Title for search (requires spaceKey)
- `expand`: (Optional) Fields to expand (e.g., `body.storage,version,space,ancestors`)
- `version`: (Optional) Specific version to retrieve

**Output:**
- `page`: Retrieved page object (or first match from search)
- `pages`: Array of pages (if search returned multiple)
- `count`: Number of results
- `raw`: Full API response

### confluence-page-create

Create a new page in Confluence.

**Parameters:**
- `baseUrl`: Confluence base URL
- `apiToken`: API token
- `email`: Account email
- `spaceKey`: Target space key
- `title`: Page title
- `content`: Page content in Confluence storage format (HTML)
- `parentId`: (Optional) Parent page ID for nested pages

**Output:**
- `id`: Created page ID
- `title`: Page title
- `type`: Content type (page)
- `status`: Page status
- `space`: Space information
- `version`: Version information
- `links`: Page links
- `self`: Human-readable page URL

### confluence-page-update

Update an existing Confluence page.

**Parameters:**
- `baseUrl`: Confluence base URL
- `apiToken`: API token
- `email`: Account email
- `pageId`: ID of page to update
- `title`: (Optional) New title (keeps existing if empty)
- `content`: New page content (HTML in storage format)
- `minorEdit`: Mark as minor edit (default: false)
- `message`: (Optional) Version message

**Output:**
- `id`: Updated page ID
- `title`: Page title
- `type`: Content type
- `status`: Page status
- `version`: New version information
- `links`: Page links
- `self`: Human-readable page URL

### confluence-page-delete

Delete a Confluence page permanently.

**Parameters:**
- `baseUrl`: Confluence base URL
- `apiToken`: API token
- `email`: Account email
- `pageId`: ID of the page to delete

**Output:**
- `success`: Boolean indicating success
- `message`: Success message
- `pageId`: Deleted page ID

### confluence-space-list

List all Confluence spaces with filtering options.

**Parameters:**
- `baseUrl`: Confluence base URL
- `apiToken`: API token
- `email`: Account email
- `spaceType`: Type of spaces (global, personal) - default: global
- `status`: Space status (current, archived) - default: current
- `expand`: (Optional) Fields to expand (permissions, roles, icon)
- `limit`: Maximum results (default: 25, max: 100)
- `start`: Starting index (default: 0)

**Output:**
- `spaces`: Array of space objects
- `count`: Number of spaces returned
- `size`: Total size from API
- `start`: Start index
- `limit`: Limit used
- `hasNext`: Boolean indicating if more results exist
- `raw`: Full API response

### confluence-space-get

Get details of a specific Confluence space by key.

**Parameters:**
- `baseUrl`: Confluence base URL
- `apiToken`: API token
- `email`: Account email
- `spaceKey`: Space key to retrieve
- `expand`: (Optional) Fields to expand (permissions, roles, icon, metadata)

**Output:**
- `key`: Space key
- `name`: Space name
- `type`: Space type
- `status`: Space status
- `permissions`: Space permissions (if expanded)
- `roles`: Space roles (if expanded)
- `icon`: Space icon (if expanded)
- `metadata`: Space metadata (if expanded)
- `raw`: Full API response

### confluence-attachment-upload

Upload a file attachment to a Confluence page.

**Parameters:**
- `baseUrl`: Confluence base URL
- `apiToken`: API token
- `email`: Account email
- `pageId`: Page ID to attach the file to
- `fileName`: Name of the file
- `fileContent`: Base64 encoded file content
- `comment`: (Optional) Attachment comment

**Output:**
- `attachment`: Full attachment object
- `attachmentId`: ID of the uploaded attachment
- `fileName`: Name of the uploaded file
- `downloadLink`: Download URL for the attachment
- `message`: Success message

### confluence-attachment-list

List all attachments on a Confluence page.

**Parameters:**
- `baseUrl`: Confluence base URL
- `apiToken`: API token
- `email`: Account email
- `pageId`: Page ID to list attachments for
- `expand`: (Optional) Fields to expand (version, container)
- `limit`: Maximum results (default: 25, max: 100)

**Output:**
- `attachments`: Array of attachment objects
- `count`: Number of attachments
- `size`: Total size from API
- `raw`: Full API response

### confluence-attachment-delete

Delete an attachment from a Confluence page.

**Parameters:**
- `baseUrl`: Confluence base URL
- `apiToken`: API token
- `email`: Account email
- `attachmentId`: Attachment ID to delete (e.g., att123456)

**Output:**
- `success`: Boolean indicating success
- `message`: Success message
- `attachmentId`: Deleted attachment ID

### confluence-comment-create

Add a comment to a Confluence page.

**Parameters:**
- `baseUrl`: Confluence base URL
- `apiToken`: API token
- `email`: Account email
- `pageId`: Page ID to add comment to
- `content`: Comment content in Confluence storage format (HTML)

**Output:**
- `id`: Comment ID
- `type`: Content type (comment)
- `status`: Comment status
- `container`: Container information
- `body`: Comment body
- `version`: Version information
- `links`: Comment links
- `raw`: Full API response

### confluence-comment-list

List all comments on a Confluence page.

**Parameters:**
- `baseUrl`: Confluence base URL
- `apiToken`: API token
- `email`: Account email
- `pageId`: Page ID to list comments for
- `expand`: (Optional) Fields to expand (body.storage, version, container)
- `limit`: Maximum results (default: 25, max: 100)

**Output:**
- `comments`: Array of comment objects
- `count`: Number of comments
- `size`: Total size from API
- `raw`: Full API response

### confluence-search

Search Confluence content using CQL (Confluence Query Language).

**Parameters:**
- `baseUrl`: Confluence base URL
- `apiToken`: API token
- `email`: Account email
- `query`: CQL search query (e.g., `type=page AND space=DEV AND text ~ 'documentation'`)
- `expand`: (Optional) Fields to expand (body.storage, version, space)
- `limit`: Maximum results (default: 25, max: 100)
- `start`: Starting index (default: 0)
- `includeArchived`: Include archived content (default: false)

**Output:**
- `results`: Array of search result objects
- `count`: Number of results returned
- `size`: Size of this result set
- `start`: Start index
- `limit`: Limit used
- `totalSize`: Total matching results
- `cqlQuery`: The executed CQL query
- `raw`: Full API response

## Content Format

Confluence uses "storage format" for content, which is HTML with specific Confluence macros. Examples:

```html
<!-- Basic heading and paragraph -->
<h1>Page Title</h1>
<p>This is a paragraph.</p>

<!-- Code block -->
<ac:structured-macro ac:name="code">
  <ac:parameter ac:name="language">javascript</ac:parameter>
  <ac:plain-text-body><![CDATA[console.log('Hello');]]></ac:plain-text-body>
</ac:structured-macro>

<!-- Info panel -->
<ac:structured-macro ac:name="info">
  <ac:rich-text-body><p>This is an info message.</p></ac:rich-text-body>
</ac:structured-macro>

<!-- Table of contents -->
<ac:structured-macro ac:name="toc"/>

<!-- Warning panel -->
<ac:structured-macro ac:name="warning">
  <ac:rich-text-body><p>This is a warning!</p></ac:rich-text-body>
</ac:structured-macro>

<!-- User mention -->
<ac:link>
  <ri:user ri:account-id="123456789"/>
</ac:link>
```

## CQL Examples

Here are some common CQL queries for the search node:

```cql
-- Find pages by title
title ~ "API Documentation"

-- Find pages in a specific space
space = "DEV" AND type = page

-- Find pages modified recently
lastModified > "2024-01-01"

-- Find pages created by a specific user
creator = "user@example.com"

-- Complex query
type = page AND space = "DEV" AND text ~ "authentication" AND lastModified > startOfMonth()

-- Find pages with specific label
label = "api-docs"
```

## Example Usage

### List Pages in a Space

```yaml
- type: confluence-page-list
  config:
    baseUrl: https://mycompany.atlassian.net/wiki
    apiToken: {{secrets.confluence_token}}
    email: bot@mycompany.com
    spaceKey: DEV
    expand: body.storage,version
    limit: 50
```

### Create a Page

```yaml
- type: confluence-page-create
  config:
    baseUrl: https://mycompany.atlassian.net/wiki
    apiToken: {{secrets.confluence_token}}
    email: bot@mycompany.com
    spaceKey: DEV
    title: API Documentation
    content: |
      <h1>API Documentation</h1>
      <p>Welcome to our API docs.</p>
```

### Update a Page

```yaml
- type: confluence-page-update
  config:
    baseUrl: https://mycompany.atlassian.net/wiki
    apiToken: {{secrets.confluence_token}}
    email: bot@mycompany.com
    pageId: "12345678"
    content: |
      <h1>Updated Documentation</h1>
      <p>Content has been updated.</p>
    minorEdit: false
    message: "Updated via automation"
```

### Get Page by Title

```yaml
- type: confluence-page-get
  config:
    baseUrl: https://mycompany.atlassian.net/wiki
    apiToken: {{secrets.confluence_token}}
    email: bot@mycompany.com
    spaceKey: DEV
    title: API Documentation
    expand: body.storage,version
```

### Delete a Page

```yaml
- type: confluence-page-delete
  config:
    baseUrl: https://mycompany.atlassian.net/wiki
    apiToken: {{secrets.confluence_token}}
    email: bot@mycompany.com
    pageId: "12345678"
```

### List Spaces

```yaml
- type: confluence-space-list
  config:
    baseUrl: https://mycompany.atlassian.net/wiki
    apiToken: {{secrets.confluence_token}}
    email: bot@mycompany.com
    spaceType: global
    limit: 50
```

### Upload Attachment

```yaml
- type: confluence-attachment-upload
  config:
    baseUrl: https://mycompany.atlassian.net/wiki
    apiToken: {{secrets.confluence_token}}
    email: bot@mycompany.com
    pageId: "12345678"
    fileName: report.pdf
    fileContent: {{base64_encoded_content}}
    comment: "Monthly report"
```

### Add Comment to Page

```yaml
- type: confluence-comment-create
  config:
    baseUrl: https://mycompany.atlassian.net/wiki
    apiToken: {{secrets.confluence_token}}
    email: bot@mycompany.com
    pageId: "12345678"
    content: |
      <p>This page has been updated with the latest information.</p>
```

### Search with CQL

```yaml
- type: confluence-search
  config:
    baseUrl: https://mycompany.atlassian.net/wiki
    apiToken: {{secrets.confluence_token}}
    email: bot@mycompany.com
    query: "type=page AND space=DEV AND text ~ 'API'"
    expand: body.storage,version,space
    limit: 25
```

## Permissions

This skill requires the following Confluence permissions:

- **Read**: View spaces and pages
- **Create**: Create new pages and comments
- **Update**: Edit existing pages
- **Delete**: Remove pages and attachments

## Troubleshooting

### Authentication Errors

- Verify your API token is valid and not expired
- Ensure the email matches your Atlassian account
- Check that the base URL is correct (include `/wiki` for Cloud)
- For PAT authentication, email is not required

### Permission Errors

- Ensure the user has space permissions
- Verify the space key exists
- Check page-level restrictions
- Confirm the user has the required permissions for the operation

### Content Format Issues

- Use Confluence storage format (HTML with macros)
- Escape special characters properly
- Validate HTML structure
- Use CDATA sections for code content

### Rate Limiting

Confluence API has rate limits. If you encounter 429 errors:
- Implement retry logic with exponential backoff
- Reduce the frequency of API calls
- Consider batching operations

## API Reference

For more details on the Confluence REST API, see:
- [Confluence Cloud API Documentation](https://developer.atlassian.com/cloud/confluence/rest/v1/)
- [Confluence Query Language (CQL)](https://developer.atlassian.com/cloud/confluence/advanced-searching-using-cql/)

## License

MIT License - See LICENSE file for details.

## Support

For issues and feature requests, please open a GitHub issue or contact engineering@axiomstudio.ai.
