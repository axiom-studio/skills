package main

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type telegramMessageSource struct {
	Provider               string    `json:"provider"`
	WorkspaceID            string    `json:"workspaceId,omitempty"`
	ChannelID              string    `json:"channelId"`
	ChannelName            string    `json:"channelName,omitempty"`
	ChannelType            string    `json:"channelType,omitempty"`
	ThreadID               string    `json:"threadId,omitempty"`
	MessageID              string    `json:"messageId"`
	ParticipantID          string    `json:"participantId"`
	ParticipantDisplayName string    `json:"participantDisplayName,omitempty"`
	OccurredAt             time.Time `json:"occurredAt,omitempty"`
}

type telegramUpdate struct {
	UpdateID    int64           `json:"update_id"`
	Message     telegramMessage `json:"message"`
	ChannelPost telegramMessage `json:"channel_post"`
}
type telegramMessage struct {
	MessageID           int64                `json:"message_id"`
	MessageThreadID     int64                `json:"message_thread_id"`
	DirectMessagesTopic *telegramDirectTopic `json:"direct_messages_topic"`
	Date                int64                `json:"date"`
	Text                string               `json:"text"`
	Caption             string               `json:"caption"`
	From                telegramUser         `json:"from"`
	SenderChat          *telegramChat        `json:"sender_chat"`
	Chat                telegramChat         `json:"chat"`
	ReplyTo             *telegramMessage     `json:"reply_to_message"`
	Entities            []telegramEntity     `json:"entities"`
	CaptionEntities     []telegramEntity     `json:"caption_entities"`
	Photo               []telegramMedia      `json:"photo"`
	Document            *telegramMedia       `json:"document"`
	Animation           *telegramMedia       `json:"animation"`
	Audio               *telegramMedia       `json:"audio"`
	Voice               *telegramMedia       `json:"voice"`
	Video               *telegramMedia       `json:"video"`
	VideoNote           *telegramMedia       `json:"video_note"`
	Sticker             *telegramMedia       `json:"sticker"`
	MediaGroupID        string               `json:"media_group_id"`
	HasProtectedContent bool                 `json:"has_protected_content"`
}
type telegramDirectTopic struct {
	TopicID int64 `json:"topic_id"`
}
type telegramUser struct {
	ID                      int64  `json:"id"`
	IsBot                   bool   `json:"is_bot"`
	Username                string `json:"username"`
	FirstName               string `json:"first_name"`
	LastName                string `json:"last_name"`
	CanJoinGroups           bool   `json:"can_join_groups,omitempty"`
	CanReadAllGroupMessages bool   `json:"can_read_all_group_messages,omitempty"`
}
type telegramChat struct {
	IsDirectMessages bool   `json:"is_direct_messages,omitempty"`
	ID               int64  `json:"id"`
	Type             string `json:"type"`
	Title            string `json:"title"`
	Username         string `json:"username"`
	FirstName        string `json:"first_name"`
	LastName         string `json:"last_name"`
}
type telegramEntity struct {
	Type   string        `json:"type"`
	Offset int           `json:"offset"`
	Length int           `json:"length"`
	User   *telegramUser `json:"user"`
}
type telegramMedia struct {
	FileID       string `json:"file_id"`
	FileUniqueID string `json:"file_unique_id"`
	FileSize     int64  `json:"file_size"`
	FileName     string `json:"file_name"`
	MediaType    string `json:"mime_type"`
	Width        int64  `json:"width"`
	Height       int64  `json:"height"`
	IsAnimated   bool   `json:"is_animated"`
	IsVideo      bool   `json:"is_video"`
}

func telegramLabel(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 160 || strings.ContainsAny(value, "\r\n\x00") {
		return ""
	}
	return value
}
func firstTelegramText(message telegramMessage) string {
	if strings.TrimSpace(message.Text) != "" {
		return message.Text
	}
	return message.Caption
}
func telegramConfigString(configuration map[string]interface{}, name string) string {
	value, _ := configuration[name].(string)
	return strings.TrimSpace(value)
}

func normalizeTelegramUpdate(update telegramUpdate, endpoint *telegramConversationEndpoint) (telegramNormalizedEvent, bool) {
	message := update.Message
	kind := "message"
	if message.MessageID == 0 {
		message = update.ChannelPost
		kind = "channel_post"
		if message.SenderChat == nil {
			sender := message.Chat
			message.SenderChat = &sender
		}
	}
	files := normalizedTelegramFiles(message)
	if update.UpdateID < 1 || message.MessageID < 1 || message.Chat.ID == 0 || (strings.TrimSpace(firstTelegramText(message)) == "" && len(files) == 0) {
		return telegramNormalizedEvent{}, false
	}
	chatID := strconv.FormatInt(message.Chat.ID, 10)
	if endpoint != nil && (endpoint.Provider != "telegram" || (strings.TrimSpace(endpoint.Address) != "" && strings.TrimSpace(endpoint.Address) != "*" && strings.TrimSpace(endpoint.Address) != chatID)) {
		return telegramNormalizedEvent{}, false
	}
	switch message.Chat.Type {
	case "private", "group", "supergroup", "channel":
	default:
		return telegramNormalizedEvent{}, false
	}
	messageID := strconv.FormatInt(message.MessageID, 10)
	threadID := "reply:" + messageID
	if message.ReplyTo != nil && message.ReplyTo.MessageID > 0 && (message.ReplyTo.Chat.ID == 0 || message.ReplyTo.Chat.ID == message.Chat.ID) {
		threadID = "reply:" + strconv.FormatInt(message.ReplyTo.MessageID, 10)
	}
	if message.MessageThreadID > 0 {
		threadID = "topic:" + strconv.FormatInt(message.MessageThreadID, 10)
	}
	if message.DirectMessagesTopic != nil && message.DirectMessagesTopic.TopicID > 0 {
		threadID = "direct-topic:" + strconv.FormatInt(message.DirectMessagesTopic.TopicID, 10)
	}
	direct := message.Chat.Type == "private" || message.Chat.IsDirectMessages
	botUsername, botID := "", ""
	if endpoint != nil {
		botUsername = strings.TrimPrefix(telegramConfigString(endpoint.Configuration, "botUsername"), "@")
		botID = telegramConfigString(endpoint.Configuration, "botId")
	}
	text := strings.TrimSpace(firstTelegramText(message))
	mentioned := direct
	if botUsername != "" {
		match := regexp.MustCompile(`(?i)(^|[^a-z0-9_])@` + regexp.QuoteMeta(botUsername) + `($|[^a-z0-9_])`)
		mentioned = mentioned || match.MatchString(text)
		command := regexp.MustCompile(`(?i)(^|[^a-z0-9_])/[a-z0-9_]+@` + regexp.QuoteMeta(botUsername) + `($|[^a-z0-9_])`)
		mentioned = mentioned || command.MatchString(text)
	}
	for _, entity := range append(message.Entities, message.CaptionEntities...) {
		if entity.Type == "text_mention" && entity.User != nil && strconv.FormatInt(entity.User.ID, 10) == botID {
			mentioned = true
		}
	}
	if message.ReplyTo != nil && message.ReplyTo.From.IsBot && botID != "" && strconv.FormatInt(message.ReplyTo.From.ID, 10) == botID {
		mentioned = true
	}
	occurredAt := time.Unix(message.Date, 0).UTC()
	if message.Date <= 0 {
		occurredAt = time.Now().UTC()
	}
	participantID, participantName, participantKind, bot := telegramParticipantIdentity(message)
	attrs := map[string]interface{}{"chatType": message.Chat.Type, "chatTitle": telegramLabel(message.Chat.Title), "chatUsername": telegramLabel(message.Chat.Username), "participantUsername": telegramLabel(message.From.Username), "participantDisplayName": participantName, "participantKind": participantKind, "updateType": kind, "providerEventId": strconv.FormatInt(update.UpdateID, 10)}
	if message.MessageThreadID > 0 {
		attrs["messageThreadId"] = strconv.FormatInt(message.MessageThreadID, 10)
	}
	if message.DirectMessagesTopic != nil && message.DirectMessagesTopic.TopicID > 0 {
		attrs["directMessagesTopicId"] = strconv.FormatInt(message.DirectMessagesTopic.TopicID, 10)
	}
	if message.SenderChat != nil {
		attrs["senderChatId"] = strconv.FormatInt(message.SenderChat.ID, 10)
		attrs["senderChatType"] = message.SenderChat.Type
		delete(attrs, "participantUsername")
	}
	if message.Chat.IsDirectMessages {
		attrs["isDirectMessages"] = true
	}
	if message.MediaGroupID != "" {
		attrs["mediaGroupId"] = message.MediaGroupID
	}
	if message.HasProtectedContent {
		attrs["protectedContent"] = true
	}
	if message.ReplyTo != nil && message.ReplyTo.MessageID > 0 {
		attrs["replyToMessageId"] = strconv.FormatInt(message.ReplyTo.MessageID, 10)
		if message.ReplyTo.Chat.ID == message.Chat.ID || message.ReplyTo.Chat.ID == 0 {
			parent := message.ReplyTo
			parentID, parentName, _, _ := telegramParticipantIdentity(*parent)
			attrs["replyContext"] = map[string]interface{}{"externalMessageId": chatID + ":" + strconv.FormatInt(parent.MessageID, 10), "externalParticipantId": parentID, "participantDisplayName": parentName, "text": firstTelegramText(*parent), "occurredAt": time.Unix(parent.Date, 0).UTC()}
		}
	}
	replyToExternalMessageID := ""
	if message.ReplyTo != nil && message.ReplyTo.MessageID > 0 && (message.ReplyTo.Chat.ID == 0 || message.ReplyTo.Chat.ID == message.Chat.ID) && message.MessageThreadID == 0 && (message.DirectMessagesTopic == nil || message.DirectMessagesTopic.TopicID == 0) {
		replyToExternalMessageID = chatID + ":" + strconv.FormatInt(message.ReplyTo.MessageID, 10)
	}
	source := &telegramMessageSource{Provider: "telegram", WorkspaceID: botID, ChannelID: chatID, ChannelName: telegramLabel(message.Chat.Title), ChannelType: message.Chat.Type, ThreadID: threadID, MessageID: messageID, ParticipantID: participantID, ParticipantDisplayName: participantName, OccurredAt: occurredAt}
	return telegramNormalizedEvent{Source: source, ParticipantDisplayName: participantName, ReplyToExternalMessageID: replyToExternalMessageID, ID: "telegram:update:" + strconv.FormatInt(update.UpdateID, 10), Type: "conversation.message.received", ExternalConversationID: chatID, ExternalThreadID: threadID, ExternalMessageID: chatID + ":" + messageID, ExternalParticipantID: participantID, ParticipantIsBot: bot, Text: text, MentionsEndpoint: mentioned, Direct: direct, OrderingKey: chatID + ":" + threadID, OccurredAt: occurredAt, Attributes: attrs, Attachments: files}, true
}

// Telegram exposes one quoted parent, not arbitrary history. Supply the verified
// update's identity/context and let canonical conversation history supply prior
// turns. Never claim a provider history fetch took place.
func telegramReadContext(request *telegramAdapterRequest) map[string]interface{} {
	output := map[string]interface{}{"status": "partial", "messages": []interface{}{}, "errorCode": "provider_history_unavailable"}
	if request.Event == nil || !telegramEndpointMatchesEvent(request.Endpoint, request.Event) {
		output["status"] = "unavailable"
		return output
	}
	event := request.Event
	attrs := event.Attributes
	output["source"] = map[string]interface{}{"provider": "telegram", "workspaceId": telegramConfigString(attrs, "botId"), "channelId": event.ExternalConversationID, "channelName": telegramConfigString(attrs, "chatTitle"), "channelType": telegramConfigString(attrs, "chatType"), "threadId": event.ExternalThreadID, "messageId": telegramRawMessageID(event.ExternalMessageID, event.ExternalConversationID), "participantId": event.ExternalParticipantID, "participantDisplayName": telegramConfigString(attrs, "participantDisplayName"), "occurredAt": event.OccurredAt}
	if parent, ok := attrs["replyContext"]; ok {
		encoded, _ := json.Marshal(parent)
		var message map[string]interface{}
		if json.Unmarshal(encoded, &message) == nil && telegramConfigString(message, "externalMessageId") != "" {
			message["externalConversationId"] = event.ExternalConversationID
			message["externalThreadId"] = event.ExternalThreadID
			output["messages"] = []interface{}{message}
		}
	}
	return output
}
func telegramEndpointMatchesEvent(endpoint *telegramConversationEndpoint, event *telegramNormalizedEvent) bool {
	return endpoint != nil && event != nil && endpoint.Provider == "telegram" && (endpoint.Address == "*" || endpoint.Address == event.ExternalConversationID)
}

func telegramRawMessageID(messageID, chatID string) string {
	if prefix, raw, ok := strings.Cut(messageID, ":"); ok && prefix == chatID {
		return raw
	}
	return messageID
}

func telegramParticipantIdentity(message telegramMessage) (id, name, kind string, bot bool) {
	if message.SenderChat != nil && message.SenderChat.ID != 0 {
		name = telegramLabel(message.SenderChat.Title)
		if name == "" {
			name = telegramLabel(message.SenderChat.Username)
		}
		return "chat:" + strconv.FormatInt(message.SenderChat.ID, 10), name, "chat", false
	}
	if message.From.ID == 0 {
		return "", "", "unknown", false
	}
	name = telegramLabel(strings.TrimSpace(message.From.FirstName + " " + message.From.LastName))
	if name == "" {
		name = telegramLabel(message.From.Username)
	}
	return strconv.FormatInt(message.From.ID, 10), name, "user", message.From.IsBot
}
