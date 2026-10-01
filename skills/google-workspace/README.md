# Google Workspace

One managed Google account connection for Gmail, Drive, Docs, Sheets, Slides,
Calendar, Contacts (People API), Tasks and Meet. The canonical manifest exposes
65 fixed operations, with separate read, edit, external-effect and destructive
policies. Sending mail and sharing files are distinct actions. Drive trash cannot
be hidden inside a metadata update.

## Connect an account

Configure a Google **Web application** OAuth client on the Sentinel host:

- `GOOGLE_OAUTH_CLIENT_ID`
- `GOOGLE_OAUTH_CLIENT_SECRET` (a server-side secret, never a Skill binding)
- `OAUTH_PUBLIC_CALLBACK_URL` (the public frontend `/oauth/callback` route)

Register that exact redirect URL with the OAuth client. Local development may
use `http://localhost:8084/oauth/callback`; production uses HTTPS. Enable Gmail,
Drive, Docs, Sheets, Slides, Calendar, People, Tasks and Meet APIs in the Google
Cloud project. Configure the consent screen and add test accounts when the app
is in testing. Public access to sensitive/restricted scopes requires Google's
app verification; a user's Workspace administrator can also restrict access.

In OpenSeal, create a **Google Workspace** connection and continue with Google.
The existing tenant-authorized OAuth flow verifies state and PKCE, saves tokens
in Vault, and supplies only an ephemeral access token to an approved action.
Google account identity is verified using OpenID userinfo. Offline access is
required; refresh authority and revocation stay on the host. Select the saved
connection when adding this Skill to an agent. Saving an account alone does not
install the Skill or authorize every agent to use it.

The standalone Workspace profile requests all service scopes. Skill installation
requirements remain action-specific, so a narrower agent selection can request
only the scopes used by its chosen actions. Google does not expose an unlimited
"all Google products" grant. This Skill excludes Google Cloud administration,
YouTube, Maps, domain-wide delegation and Google Chat.

## Actions

- Gmail: messages, threads, labels, attachments, plain-text drafting/sending,
  draft reads/edits/sending, label modifications and trash.
- Drive: file search/metadata, folder/file creation, edits/copies, bounded uploads,
  downloads/exports, permissions and trash.
- Docs, Sheets, Slides: create/read documents and presentations, service-native
  batch edits, and spreadsheet value reads/updates/appends.
- Calendar: calendar discovery and event reads, creation, edits and deletion.
- Contacts: search/list/read/create/edit/delete through the People API.
- Tasks: task lists and task reads/creation/edits/deletion.
- Meet: create/read/edit app-created spaces, conference records, participants,
  recordings, transcripts and transcript entries when the account can access them.
  This does not join calls or record meetings. Artifact availability depends on
  the user's account and the meeting.

Inputs use the documented Google API field names. `body` is a JSON object for the
fixed operation, not an arbitrary HTTP request. IDs and resource names are path
encoded and checked. Gmail is always the connected user's `me` mailbox. Writes
are attempted once; after an ambiguous failure, inspect the resource before
repeating them. The platform's action idempotency does not promise Google's
upstream create/send APIs are exactly-once.

Lists default to bounded pages and preserve Google page tokens. Drive listing
uses a compact fields projection; callers can request another projection.
Responses and file contents are bounded to 2 MiB. Exports/downloads return
`contentBase64`, `mimeType`, `sizeBytes`, and text for UTF-8 text responses.
Sheets value writes default to `RAW`; request `USER_ENTERED` explicitly for
formulas. Plain-text email actions validate recipients and prevent MIME header
injection. No service-account or ambient Google credential fallback exists.

## Build and validation

From the repository root:

```sh
go test -mod=vendor ./skills/google-workspace
go run -mod=vendor ./skills/google-workspace -manifest > skills/google-workspace/skill.yaml
go build -mod=vendor -o /tmp/skill-google-workspace ./skills/google-workspace
openseal skill validate skills/google-workspace/skill.yaml
docker build -f skills/google-workspace/Dockerfile -t axiomstudio/skill-google-workspace:1.0.0 .
```

`generate_operations.py` refreshes the checked-in, selected API catalog from
Google's official Discovery documents. Review catalog changes, regenerate the
manifest, and change the Skill version when a published action contract changes.
The runtime uses the checked-in catalog and does not download it during startup.

References: [OAuth web-server flow](https://developers.google.com/identity/protocols/oauth2/web-server),
[Google API scopes](https://developers.google.com/identity/protocols/oauth2/scopes),
[Meet authorization](https://developers.google.com/workspace/meet/api/guides/authenticate-authorize).
