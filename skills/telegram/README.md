# Telegram Skill

Telegram Bot API channel support for OpenSeal agents: direct messages, groups, forum topics and channel posts, with sender/chat context and image/document exchange. Runtime version `1.2.0`; OCI installation reference `axiomstudio/skill-telegram:latest`.

## Connect a bot

Create a bot with [BotFather](https://core.telegram.org/bots/features#botfather), save its token through the connection form, and attach that saved connection to one agent. `telegram-get-me` verifies the bot's real numeric installation ID and username. Telegram does not offer a Bot API list of every accessible chat; discovery returns the bot identity with an empty destination list.

The host registers the canonical public HTTPS conversation gateway as the bot's webhook. Configure the host with `OPENSEAL_PUBLIC_CONVERSATION_INGRESS_BASE_URL` (for example, `https://sealchat.ksz.sh`); the host appends its unguessable canonical gateway route. This ingress must be reachable by Telegram; local or private addresses are refused. Existing unrelated webhook URLs produce a conflict and are never overwritten. It supplies the secret-token header, derived from the saved token, and preserves pending updates. Webhook ingress verifies that header before normalizing an update. The model cannot consume `getUpdates` or reconfigure the channel's webhook.

Start a private chat with the bot, or add it to a group. Group privacy mode controls which messages Telegram supplies: addressed mentions/commands and replies to the bot work with privacy enabled. Administrator status or disabling privacy expands visibility; disabling privacy requires removing and re-adding the bot. Channel posting requires the bot to be a channel administrator with posting permission. The host's conversation policy separately controls mentions, bot senders and reply participation.

Bot-wide routing identifies the exact chat from the verified update. Posting and attachment delivery retain the originating chat and topic. Sender names and chat titles provide context; they grant no connection, destination or approval authority. Anonymous/channel posts use `sender_chat` identity rather than pretending their fallback `from` user is a human.

## Conversation and media behavior

Messages and file-only updates enter the same canonical inbox. Single-photo and document updates are supported. Telegram delivers media albums as separate updates; this release does not coalesce an album into one request, and group mention policy may select only its captioned update. The largest photo variant or document/audio/video/voice/sticker descriptor is retained without exposing a private download URL. The background worker requests `getFile` only for a descriptor in the verified event, checks the returned file ID and path, refuses redirects, and bounds downloads to the host's 2 MiB artifact budget. Oversized and unavailable files retain their descriptor and a truthful unreadable status.

Generated image artifacts use multipart `sendPhoto`; other artifacts use `sendDocument`. Up to eight host-supplied files can accompany a response. The reply text arrives before its generated media, without a filename or download-link boilerplate message. Responses exceeding 4096 UTF-16 code units split into bounded text chunks without breaking Unicode characters.

Provider message IDs are chat-qualified (`<chat ID>:<message ID>`) in canonical inbox/outbox mappings because Telegram message numbers repeat across chats. User-facing sender context and action receipts retain the raw message number.

Ordinary reply roots use `reply:<message ID>`. Forum/private bot topics use `topic:<topic ID>`, and channel direct-message topics use `direct-topic:<topic ID>`. These IDs remain distinct: delivery maps them to `reply_parameters.message_id`, `message_thread_id` and `direct_messages_topic_id`, respectively. Topics are never mistaken for message reply IDs. Legacy numeric reply roots remain readable.

The Bot API does not expose arbitrary message-history reads. Context includes verified sender/chat identity and an available quoted parent; prior canonical conversation history supplies earlier turns. A partial context result does not claim that omitted history is empty or was fetched from Telegram.

## Action tools

| Action | Purpose |
| --- | --- |
| `telegram-get-me` | Verify saved bot identity; return the connection's installation ID and display name. |
| `telegram-get-chat` | Read one explicitly identified chat and return its canonical chat ID. |
| `telegram-send-message` | Send requested text to a specific authorized chat/topic. |
| `telegram-send-photo` | Send a provided Telegram photo file ID or URL. |
| `telegram-send-document` | Send a provided Telegram document file ID or URL. |
| `telegram-edit-message` | Edit an explicitly identified message. |
| `telegram-delete-message` | Delete an explicitly identified message; destructive policy applies. |

Send actions use `chatId`, optional `messageThreadId`, `directMessagesTopicId` and `replyToMessageId`. Successful sends return the provider's canonical `chatId` and `messageId`; use these receipts for subsequent reads and operations instead of guessing a chat from a username. Generated conversation artifacts are delivered by the host and should not be sent again by the model.

## Delivery guarantees

Structured 429 rate limits honor `retry_after`. Canonical delivery attempt numbers fence recovered attempts: only an exact next attempt with an explicit prior 429 receipt may transmit again. A reclaim after an unknown send cannot repeat it. Progress receipts bind completed text/file phases to the delivery, destination, thread and artifact digests; later retries skip acknowledged phases. Unknown post-transmission network outcomes, malformed/missing acknowledgements and ambiguous server failures stop automatic delivery rather than risking duplicate messages. Telegram has no send idempotency key or general message-history acknowledgement lookup; a host crash between provider success and receipt persistence cannot provide exactly-once delivery. This limitation is reported honestly.

Raw network errors, bot-token URLs, private download paths and provider descriptions are not returned to user-visible output or logs. Provider redirects are refused. Bot credentials are resolved from the host's ephemeral vault binding and never copied into durable step configuration.

## Verification

Run `go test -race ./skills/telegram`. Tests cover signed webhook ingress, bot identity, direct-message continuity, group mentions/replies, forum and direct-message topics, channel sender identity, file-only messages, bounded verified downloads, redirect/token protection, multipart artifacts, successful-phase receipts, rate-limit replay and ambiguous delivery failures.

Reference: [Telegram Bot API](https://core.telegram.org/bots/api), [Bot privacy mode](https://core.telegram.org/bots/features#privacy-mode).
