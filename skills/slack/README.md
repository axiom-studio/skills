# Slack Skill

Integration with Slack for messaging and channel operations.

## Node Types

- **slack-send-message** — Send messages to channels or threads
- **slack-read-messages** — Read recent messages from a channel
- **slack-channel-list** — List available channels
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

1. Create a Slack app and install it to your workspace
2. Copy the Bot User OAuth Token
3. Add the token to your agent's vault as key `token` in a `slack_bot_token` credential
4. Use the skill nodes in your agent workflows
