# Slack Skill

First-class Slack integration for governed Agent and Team conversations,
messaging, and channel operations.

The Skill owns request verification, event normalization, durable reply
delivery, acknowledgement lookup, and paginated channel discovery. Hosts use
the portable OpenSeal conversation-adapter contract; no Slack-specific logic
is required in the kernel or product UI.

## Node Types

- **slack-send-message** — Send messages to channels or threads
- **slack-read-messages** — Read recent messages from a channel
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

## Setup

1. Create a Slack app with the scopes declared in `skill.yaml` and install it
   to the workspace.
2. Connect the installation through the host's authorized OAuth flow. The bot
   token is bound opaquely as `slack_bot_token`; it is never an action input.
3. Configure `slack_signing_secret` through the host credential surface for
   inbound Events API verification.
4. Select an authorized channel by name during Agent or Team authoring. The
   Skill persists the exact channel ID and continues pagination using Slack's
   opaque cursor.
