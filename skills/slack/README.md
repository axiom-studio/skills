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
- **slack.callback.ingress** — Verify signed Slack interactions and emit a
  canonical `approval.decided` callback event
- **slack.callback.socket_mode** — Maintain an isolated Slack Socket Mode
  connection for one callback registration and feed interactions through the
  same signed callback normalizer

## Setup

1. Create a Slack app with the scopes required by the selected actions and
   install it to the workspace.
2. Bind its bot token through the host Vault. The token is bound opaquely as
   `slack_bot_token`; it is never an action input.
3. Configure `slack_signing_secret` through the host credential surface for
   inbound Events API verification.
4. Select an authorized channel by name during Agent or Team authoring. The
   Skill persists the exact channel ID and continues pagination using Slack's
   opaque cursor.
5. For interactive approvals, create an OpenSeal callback registration for the
   `interactions` adapter, map eligible Slack user IDs to approval principals,
   and subscribe `approval.decided` to the `approvals` consumer. Bind a
   `slack_app_token` when the Slack app uses Socket Mode; the host then
   materializes one isolated connector for the registration. HTTP-mode apps may
   instead use the registration's public callback URL as the Interactivity
   Request URL. Registrations begin paused and activate only after the exact
   Skill binding and credentials have been reviewed.
