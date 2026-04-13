# HubSpot Skill

HubSpot CRM skill for Axiom Atlas agents. Provides comprehensive HubSpot CRM operations including contacts, deals, companies, tickets, and engagements.

## Features

- **Contact Management**: Create, update, and search contacts
- **Deal Management**: Create and update sales deals
- **Company Management**: Create companies
- **Ticket Management**: Create support tickets
- **Engagements**: Create notes, calls, emails, tasks, and meetings

## Installation

### From Docker Hub

```bash
docker pull ghcr.io/axiom-studio/skills.skill-hubspot:latest
```

### Build from Source

```bash
git clone https://github.com/axiom-studio/skills.skill-hubspot.git
cd skills.skill-hubspot
docker build -t skills.skill-hubspot .
```

## Configuration

### Required Secrets

- `HUBSPOT_API_KEY`: Your HubSpot API key or private app access token

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `SKILL_PORT` | `50053` | gRPC server port |

## Node Types

### hs-contact-create

Create a new contact in HubSpot CRM.

**Configuration:**
- `apiKey`: HubSpot API key (supports `{{secrets.xxx}}`)
- `email`: Contact email address (required)
- `firstName`: Contact first name
- `lastName`: Contact last name
- `phone`: Contact phone number
- `company`: Contact company name
- `properties`: Additional custom properties

**Output:**
- `id`: Created contact ID
- `properties`: Contact properties
- `createdAt`: Creation timestamp
- `updatedAt`: Update timestamp

### hs-contact-update

Update an existing contact in HubSpot CRM.

**Configuration:**
- `apiKey`: HubSpot API key
- `contactId`: Contact ID or email (required)
- `email`: New email address
- `firstName`: New first name
- `lastName`: New last name
- `phone`: New phone number
- `company`: New company name
- `properties`: Additional properties to update

**Output:**
- `id`: Updated contact ID
- `properties`: Updated contact properties
- `updatedAt`: Update timestamp

### hs-contact-search

Search for contacts in HubSpot CRM.

**Configuration:**
- `apiKey`: HubSpot API key
- `query`: Search query string (required)
- `properties`: Properties to return
- `limit`: Maximum results (default: 100)

**Output:**
- `contacts`: Array of contact objects
- `count`: Number of results returned
- `total`: Total matching results

### hs-deal-create

Create a new deal in HubSpot CRM.

**Configuration:**
- `apiKey`: HubSpot API key
- `dealName`: Name of the deal (required)
- `amount`: Deal amount
- `currency`: Currency code (default: USD)
- `stage`: Deal stage (e.g., `appointmentscheduled`, `qualifiedtobuy`)
- `closeDate`: Expected close date (YYYY-MM-DD)
- `companyId`: Associated company ID
- `contactId`: Associated contact ID
- `dealPipeline`: Deal pipeline ID
- `properties`: Additional custom properties

**Output:**
- `id`: Created deal ID
- `properties`: Deal properties
- `createdAt`: Creation timestamp
- `updatedAt`: Update timestamp

### hs-deal-update

Update an existing deal in HubSpot CRM.

**Configuration:**
- `apiKey`: HubSpot API key
- `dealId`: Deal ID to update (required)
- `dealName`: New deal name
- `amount`: New deal amount
- `stage`: New deal stage
- `closeDate`: New close date
- `properties`: Additional properties to update

**Output:**
- `id`: Updated deal ID
- `properties`: Updated deal properties
- `updatedAt`: Update timestamp

### hs-company-create

Create a new company in HubSpot CRM.

**Configuration:**
- `apiKey`: HubSpot API key
- `name`: Company name (required)
- `domain`: Company website domain
- `industry`: Company industry
- `annualRevenue`: Company annual revenue
- `numberOfEmployees`: Number of employees
- `phone`: Company phone number
- `address`: Company street address
- `city`: Company city
- `state`: Company state
- `zip`: Company zip code
- `country`: Company country
- `properties`: Additional custom properties

**Output:**
- `id`: Created company ID
- `properties`: Company properties
- `createdAt`: Creation timestamp
- `updatedAt`: Update timestamp

### hs-ticket-create

Create a new support ticket in HubSpot CRM.

**Configuration:**
- `apiKey`: HubSpot API key
- `subject`: Ticket subject (required)
- `content`: Ticket description (required)
- `status`: Ticket status (NEW, IN_PROGRESS, CLOSED)
- `priority`: Ticket priority (LOW, MEDIUM, HIGH, URGENT)
- `contactId`: Associated contact ID
- `companyId`: Associated company ID
- `ownerId`: Assigned owner ID
- `category`: Ticket category
- `properties`: Additional custom properties

**Output:**
- `id`: Created ticket ID
- `properties`: Ticket properties
- `createdAt`: Creation timestamp
- `updatedAt`: Update timestamp

### hs-engagement

Create engagements (notes, calls, emails, tasks, meetings) in HubSpot.

**Configuration:**
- `apiKey`: HubSpot API key
- `engagementType`: Type of engagement (NOTE, CALL, EMAIL, TASK, MEETING)
- `subject`: Engagement subject/title
- `body`: Engagement body/content (required)
- `contactId`: Associated contact ID
- `companyId`: Associated company ID
- `dealId`: Associated deal ID
- `ticketId`: Associated ticket ID
- `ownerId`: Owner ID
- `status`: Engagement status (for tasks)
- `dueDate`: Due date for tasks/meetings
- `properties`: Additional custom properties

**Output:**
- `id`: Created engagement ID
- `type`: Engagement type
- `properties`: Engagement properties
- `createdAt`: Creation timestamp
- `updatedAt`: Update timestamp

## Usage Examples

### Create a Contact

```yaml
- id: create-contact
  type: hs-contact-create
  config:
    apiKey: "{{secrets.hubspot_api_key}}"
    email: "john.doe@example.com"
    firstName: "John"
    lastName: "Doe"
    company: "Acme Inc"
    phone: "+1-555-123-4567"
```

### Search for Contacts

```yaml
- id: search-contacts
  type: hs-contact-search
  config:
    apiKey: "{{secrets.hubspot_api_key}}"
    query: "john@example.com"
    properties:
      - email
      - firstname
      - lastname
      - company
    limit: 10
```

### Create a Deal

```yaml
- id: create-deal
  type: hs-deal-create
  config:
    apiKey: "{{secrets.hubspot_api_key}}"
    dealName: "Enterprise License Deal"
    amount: 50000
    currency: "USD"
    stage: "qualifiedtobuy"
    closeDate: "2026-12-31"
    companyId: "{{outputs.create-company.id}}"
    contactId: "{{outputs.create-contact.id}}"
```

### Create a Support Ticket

```yaml
- id: create-ticket
  type: hs-ticket-create
  config:
    apiKey: "{{secrets.hubspot_api_key}}"
    subject: "Unable to access account"
    content: "Customer reports unable to log in to the platform."
    status: "NEW"
    priority: "HIGH"
    contactId: "{{outputs.search-contacts.contacts[0].id}}"
```

### Create a Follow-up Note

```yaml
- id: create-note
  type: hs-engagement
  config:
    apiKey: "{{secrets.hubspot_api_key}}"
    engagementType: "NOTE"
    subject: "Follow-up call"
    body: "Discussed product features and pricing. Customer interested in enterprise plan."
    contactId: "{{outputs.search-contacts.contacts[0].id}}"
```

## Getting a HubSpot API Key

1. Log in to your HubSpot account
2. Go to Settings > Integrations > Private Apps
3. Click "Create a private app"
4. Configure the required scopes:
   - `crm.objects.contacts.read`
   - `crm.objects.contacts.write`
   - `crm.objects.deals.read`
   - `crm.objects.deals.write`
   - `crm.objects.companies.read`
   - `crm.objects.companies.write`
   - `crm.objects.tickets.read`
   - `crm.objects.tickets.write`
5. Copy the access token and store it as a secret

## Development

### Prerequisites

- Go 1.25+
- Docker (optional)

### Build

```bash
go build -o skill-hubspot .
```

### Run Locally

```bash
export HUBSPOT_API_KEY="your-api-key"
./skill-hubspot
```

### Test

```bash
go test ./...
```

## License

MIT License - see LICENSE file for details.

## Support

For issues and feature requests, please open an issue on GitHub.
