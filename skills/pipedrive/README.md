# Pipedrive Skill

Pipedrive CRM operations for Atlas agents.

## Nodes

- `pipedrive-api-request` - Call any JSON Pipedrive API endpoint by method, path, query, and body.
- `pipedrive-search` - Search across deals, persons, organizations, leads, products, files, mail attachments, and projects.
- `pipedrive-list` - List supported entities with optional query filters.
- `pipedrive-get` - Fetch a supported entity by ID.
- `pipedrive-create-record` - Create a supported entity with a raw JSON body.
- `pipedrive-update-record` - Update a supported entity with a raw JSON body.
- `pipedrive-delete-record` - Delete a supported entity by ID.
- `pipedrive-create-person` - Create a person contact.
- `pipedrive-create-organization` - Create an organization contact.
- `pipedrive-create-deal` - Create a deal.
- `pipedrive-update-deal` - Update a deal.
- `pipedrive-create-activity` - Create an activity linked to a deal, person, or organization.

Generic record nodes support common collections including deals, persons, organizations, activities, leads, notes, products, pipelines, stages, projects, users, filters, fields, and currencies where the Pipedrive API exposes collection paths.

## Credentials

Provide a Pipedrive API token through the `apiToken` input or the configured connection binding.

The skill defaults to `https://api.pipedrive.com`. Set `baseURL` to a company-domain API base URL if your account requires it, for example `https://example.pipedrive.com`.
