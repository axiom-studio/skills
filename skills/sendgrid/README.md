# SendGrid Skill

SendGrid email marketing operations for Atlas agents. This skill provides comprehensive integration with SendGrid's API for managing emails, templates, contacts, contact lists, and marketing campaigns.

## Features

- **Send Emails** (`sg-send`): Send transactional and marketing emails via SendGrid
- **Manage Templates** (`sg-template`): Create, read, update, and delete email templates
- **Manage Contacts** (`sg-contact`): Manage individual contacts in SendGrid Marketing Campaigns
- **Manage Contact Lists** (`sg-list`): Create and manage contact lists for campaigns
- **Manage Campaigns** (`sg-campaign`): Create, schedule, and send email campaigns

## Installation

### Using Docker

```bash
docker build -t skill-sendgrid .
docker run -p 50053:50053 skill-sendgrid
```

### Building Locally

```bash
go build -o skill-sendgrid .
./skill-sendgrid
```

## Configuration

### Required Dependencies

- SendGrid API Key (store securely using `{{secrets.sendgrid_api_key}}`)

### Node Types

#### sg-send - Send Email

Send emails via SendGrid with support for:
- Single or multiple recipients
- CC and BCC recipients
- Plain text or HTML content
- Dynamic templates with template data

**Configuration Fields:**
- `apiKey`: SendGrid API key (required)
- `fromEmail`: Sender email address (required)
- `fromName`: Sender name
- `toEmails`: Recipient email addresses (required)
- `ccEmails`: CC email addresses
- `bccEmails`: BCC email addresses
- `subject`: Email subject (required)
- `content`: Email body content (required if not using template)
- `contentType`: Content type (text/plain or text/html)
- `templateId`: Dynamic template ID (optional)
- `templateData`: Data for dynamic template substitution

#### sg-template - Manage Templates

Manage SendGrid email templates with actions:
- `list`: List all templates
- `get`: Get a specific template
- `create`: Create a new template
- `update`: Update an existing template
- `delete`: Delete a template

**Configuration Fields:**
- `apiKey`: SendGrid API key (required)
- `action`: Action to perform (required)
- `templateId`: Template ID (required for get/update/delete)
- `name`: Template name (required for create/update)
- `content`: Template HTML content (for create/update)
- `subject`: Template subject (for create/update)

#### sg-contact - Manage Contacts

Manage contacts in SendGrid Marketing Campaigns with actions:
- `list`: List all contacts
- `get`: Get a specific contact
- `create`: Create a new contact
- `update`: Update an existing contact
- `delete`: Delete a contact
- `search`: Search contacts with SQL-like query

**Configuration Fields:**
- `apiKey`: SendGrid API key (required)
- `action`: Action to perform (required)
- `contactId`: Contact ID (required for get/update/delete)
- `email`: Contact email address (required for create)
- `firstName`: Contact first name
- `lastName`: Contact last name
- `phone`: Contact phone number
- `customFields`: Custom field values
- `query`: Search query (required for search action)

#### sg-list - Manage Contact Lists

Manage contact lists with actions:
- `list`: List all contact lists
- `get`: Get a specific list
- `create`: Create a new list
- `update`: Update an existing list
- `delete`: Delete a list

**Configuration Fields:**
- `apiKey`: SendGrid API key (required)
- `action`: Action to perform (required)
- `listId`: List ID (required for get/update/delete)
- `name`: List name (required for create/update)
- `contactIds`: Contact IDs to add to list

#### sg-campaign - Manage Campaigns

Manage email campaigns with actions:
- `list`: List all campaigns
- `get`: Get a specific campaign
- `create`: Create a new campaign
- `update`: Update an existing campaign
- `delete`: Delete a campaign
- `schedule`: Schedule a campaign for later sending
- `send`: Send a campaign immediately

**Configuration Fields:**
- `apiKey`: SendGrid API key (required)
- `action`: Action to perform (required)
- `campaignId`: Campaign ID (required for get/update/delete/schedule/send)
- `name`: Campaign name (required for create)
- `subject`: Campaign subject (required for create)
- `fromEmail`: Sender email (required for create)
- `fromName`: Sender name
- `listIds`: Contact list IDs to send to (required for create)
- `templateId`: Dynamic template ID
- `customUnsubscribeUrl`: Custom unsubscribe URL
- `sendTime`: Scheduled send time in ISO 8601 format (required for schedule)

## Usage Examples

### Send a Simple Email

```yaml
nodeType: sg-send
config:
  apiKey: "{{secrets.sendgrid_api_key}}"
  fromEmail: "noreply@example.com"
  fromName: "Example Corp"
  toEmails:
    - "user@example.com"
  subject: "Welcome to Our Service"
  content: "Thank you for signing up!"
  contentType: "text/plain"
```

### Send Using a Dynamic Template

```yaml
nodeType: sg-send
config:
  apiKey: "{{secrets.sendgrid_api_key}}"
  fromEmail: "noreply@example.com"
  toEmails:
    - "user@example.com"
  subject: "Your Order Confirmation"
  templateId: "d-abc123"
  templateData:
    customer_name: "John Doe"
    order_number: "12345"
    total: "$99.99"
```

### Create a Contact

```yaml
nodeType: sg-contact
config:
  apiKey: "{{secrets.sendgrid_api_key}}"
  action: "create"
  email: "john@example.com"
  firstName: "John"
  lastName: "Doe"
  phone: "+1-555-123-4567"
```

### Create and Send a Campaign

```yaml
# Step 1: Create campaign
- nodeType: sg-campaign
  config:
    apiKey: "{{secrets.sendgrid_api_key}}"
    action: "create"
    name: "Monthly Newsletter"
    subject: "March Newsletter"
    fromEmail: "newsletter@example.com"
    fromName: "Example Corp"
    listIds:
      - "list-id-123"
    templateId: "d-template-id"

# Step 2: Send campaign
- nodeType: sg-campaign
  config:
    apiKey: "{{secrets.sendgrid_api_key}}"
    action: "send"
    campaignId: "{{outputs.previous_node.campaign_id}}"
```

## Security

- Always store your SendGrid API key as a secret using `{{secrets.sendgrid_api_key}}`
- Never hardcode API keys in workflow definitions
- Use the minimum required permissions for your API key

## API Reference

This skill uses the SendGrid Web API v3:
- [SendGrid API Documentation](https://docs.sendgrid.com/api-reference/)
- [Marketing Campaigns API](https://docs.sendgrid.com/api-reference/marketing-campaigns-apis/)

## License

MIT License - See LICENSE file for details.

## Support

For issues or questions, please contact engineering@axiomstudio.ai
