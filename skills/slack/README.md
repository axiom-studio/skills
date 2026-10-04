# Slack Skill

First-class Slack integration for governed Agent and Team conversations,
messaging, channel operations, and signed interactive approval decisions.

The Skill owns request verification, event normalization, durable reply
delivery, acknowledgement lookup, paginated channel discovery, and interactive
callback verification. Hosts use the portable OpenSeal conversation and
callback-adapter contracts; no Slack-specific logic is required in the kernel
or product UI.

## Node Types

- **slack-send-message** — Send messages to channels or threads
- **slack-read-messages** — Read a page of channel history, or a specific thread
- **slack-search-messages** — Search keywords in one page of channel or thread history
- **slack-channel-list** — Search and page through authorized channels
- **slack-add-reaction** — Add a reaction to a message
- **slack-remove-reaction** — Remove a reaction from a message
- **slack-update-message** — Update a message by timestamp
- **slack-delete-message** — Delete a message by timestamp
- **slack-create-channel** — Create channels (including private channels)
- **slack-rename-channel** — Rename a channel
- **slack-archive-channel** — Archive a channel
- **slack-set-channel-topic** — Set a channel topic
- **slack-set-channel-purpose** — Set a channel purpose
- **slack-send-ephemeral-message** — Send an ephemeral message to one user
- **slack-list-users** — List users in the workspace
- **slack.callback.ingress** — Verify signed Slack interactions and emit a
  canonical `approval.decided` callback event
- **slack.callback.socket_mode** — Maintain a pooled Slack Socket Mode
  connection for one exact app-token binding and route interactions to the
  owning Agent callback through the same signed normalizer

## Setup

1. Create a Slack app with the scopes required by the selected actions and
   install it to the workspace.
2. Store the bot token and signing secret as fields of the same Slack Vault
   credential. They are bound opaquely as `slack_bot_token` and
   `slack_signing_secret`; neither is an action input.
3. For HTTP delivery, configure Slack Event Subscriptions with the connection's
   public event URL and Interactivity with its public interaction URL. Use the
   `interactions_http` callback adapter for signed approval interactions. This
   adapter uses the existing `slack.callback.ingress` verifier and requires no
   app token or persistent socket connector.
4. Select an authorized channel by name during Agent or Team authoring. The
   Skill persists the exact channel ID and continues pagination using Slack's
   opaque cursor.
5. For interactive approvals, create an OpenSeal callback registration for the
   selected callback adapter, map eligible Slack user IDs to approval principals,
   and subscribe `approval.decided` to the `approvals` consumer. Registrations
   begin paused and activate only after the exact Skill binding and credentials
   have been reviewed.

Existing Socket Mode connections keep the `interactions` adapter. Enable Socket
Mode on the Slack app and add its `slack_app_token` to the same Vault credential.
The host pools one isolated connector for registrations sharing that exact app
credential and routes approvals to their subscribed Agent endpoints. Switching
to HTTP requires choosing `interactions_http` and configuring both public URLs;
existing Socket Mode registrations are not converted automatically.

Hosts can use the `gateway_verification` conversation ingress operation during
paused HTTP setup. It verifies the usual Slack signature and timestamp, answers
only JSON URL verification challenges, and emits no conversation events. Message
and interaction requests are rejected until the host enables ordinary ingress.

## Thread context and search

The conversations adapter advertises `context_history` so the host can retrieve
the originating thread's root and earlier messages before the agent answers.
That retrieval stays inside the exact incoming channel and thread. Existing
connection credentials are reused; message text cannot select another account.

Each incoming message also carries its Slack user ID and display name, workspace
ID, channel ID and name, channel type, thread ID, message timestamp, and provider.
Name lookups use `users.info` (`users:read`) and `conversations.info` with the
applicable conversation read scope. They run after acknowledgement; missing
permissions retain stable IDs without guessing names. Names describe the source
and never grant permissions or identify the sender as the agent's owner.

The read action accepts `threadTs` to retrieve a root message and its replies.
Both read and search actions accept `cursor`, `oldest`, and `latest`, and return
`hasMore`, `nextCursor`, and `nextLatest` for bounded pagination. Search matches
every whitespace-separated keyword without interpreting Slack search modifiers.
The channel must be an exact Slack channel or DM ID, and the agent uses the
originating conversation by default. Searching another channel requires an
explicit authorized request.

Search returns `coverage`, `scannedCount`, `includesThreadReplies`, and
`providerHistoryLimited`. A single page without matches is not proof of absence.
Channel history does not include replies inside other threads; supply that
thread's `threadTs` to search its replies. Slack retention and plan restrictions
still apply. The bot needs the applicable `channels:history`, `groups:history`,
`im:history`, or `mpim:history` scope and access to the conversation. If Slack
rejects a read, the action returns the provider error instead of an empty result.

This action uses the bot-compatible Conversations API rather than pretending
that `search.messages` accepts bot tokens. Slack's native Real-time Search API
is a separate integration: bot requests require `search:read.public` and a
short-lived `action_token` from the initiating event; native private-channel and
DM search requires user authorization. See the official
[Conversations history reference](https://docs.slack.dev/reference/methods/conversations.history/)
and [Real-time Search guide](https://docs.slack.dev/apis/web-api/real-time-search-api/).

## Conversation attachments

The conversations adapter supports `attachments`. Signed message events retain
provider file IDs, names, MIME types and sizes; private URLs stay out of the inbox.
After acknowledgement, the worker downloads files with the bound bot token and
stores confidential artifacts owned by the originating conversation's agent.

Add **`files:read` and `files:write`** to the Slack app's bot scopes, reinstall the
app, and update the saved bot token if Slack issued a new one. The bot must have
access to the originating conversation. Existing message-event subscriptions
include files shared in messages; a separate `file_created` subscription is not
needed. Missing file permissions are reported explicitly without repeated setup
requests or pretending that the agent read the file.

The host supplies PNG/JPEG images to vision-capable models and extracts content
from text/CSV/JSON, XLSX, PDF with a text layer, and DOCX files. Scanned PDFs need
OCR; unsupported or oversized files are marked unreadable. Each message can have
up to eight files with at most **2 MiB of file data in total** (the current gRPC
transport budget). Extracted text has a separate bounded context budget.

Canonical reply artifacts upload via `files.getUploadURLExternal`, a binary POST,
and `files.completeUploadExternal` into the exact originating channel/thread.
The upload IDs are checkpointed before sharing; acknowledgement recovery checks
those same IDs and the original thread before sending again. URLs, credentials
and file bytes are never stored in outbox progress. Provider uploads are private
and never use public-link sharing or the retired `files.upload` endpoint.
