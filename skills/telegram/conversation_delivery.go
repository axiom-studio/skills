package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
)

// Thread/topic IDs are opaque to the host. Keep provider topics distinct from
// ordinary message replies; passing a topic ID as reply_to_message_id is wrong.
func telegramDeliveryTarget(request *telegramAdapterRequest) (map[string]interface{}, error) {
	chat := strings.TrimSpace(request.Endpoint.Address)
	if request.Endpoint.InstallationWide && chat == "*" {
		chat = strings.TrimSpace(request.Delivery.ExternalConversationID)
	} else if request.Delivery.ExternalConversationID != "" && request.Delivery.ExternalConversationID != chat {
		return nil, errors.New("Telegram destination does not match its reviewed endpoint")
	}
	if id, err := strconv.ParseInt(chat, 10, 64); err != nil || id == 0 {
		return nil, errors.New("Telegram destination is invalid")
	}
	parameters := map[string]interface{}{"chat_id": chat}
	thread := strings.TrimSpace(request.Delivery.ExternalThreadID)
	if thread == "" {
		return parameters, nil
	}
	kind, value, found := strings.Cut(thread, ":")
	if !found {
		kind = "reply"
		value = thread
	} //1.1.0 persisted thread compatibility
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id < 1 {
		return nil, errors.New("Telegram reply or topic target is invalid")
	}
	switch kind {
	case "reply":
		parameters["reply_parameters"] = map[string]interface{}{"message_id": id}
	case "topic":
		parameters["message_thread_id"] = id
	case "direct-topic":
		parameters["direct_messages_topic_id"] = id
	default:
		return nil, errors.New("Telegram reply or topic target is invalid")
	}
	return parameters, nil
}
func telegramTextChunks(text string) []string {
	text = strings.TrimSpace(text)
	if text == "" || text == "Files saved by the completed action." {
		return nil
	}
	chunks := make([]string, 0)
	current := make([]rune, 0)
	units := 0
	for _, r := range text {
		width := 1
		if utf16.IsSurrogate(r) || r > 0xffff {
			width = 2
		}
		if units+width > 4096 {
			chunks = append(chunks, string(current))
			current = current[:0]
			units = 0
		}
		current = append(current, r)
		units += width
	}
	if len(current) > 0 {
		chunks = append(chunks, string(current))
	}
	return chunks
}
func telegramAcknowledgement(response TelegramResponse, expectedChat string) (string, error) {
	var message struct {
		MessageID int64        `json:"message_id"`
		Chat      telegramChat `json:"chat"`
	}
	if json.Unmarshal(response.Result, &message) != nil || message.MessageID < 1 || message.Chat.ID == 0 {
		return "", errors.New("Telegram did not return a valid message receipt")
	}
	// Host deliveries use an exact numeric chat. An action may use @username;
	// its returned numeric chat becomes authoritative for subsequent operations.
	if !strings.HasPrefix(expectedChat, "@") && strconv.FormatInt(message.Chat.ID, 10) != expectedChat {
		return "", errors.New("Telegram returned a different destination receipt")
	}
	return strconv.FormatInt(message.MessageID, 10), nil
}
func telegramDeliveryResponse(response TelegramResponse, status int, err error) map[string]interface{} {
	if err != nil || status >= 500 {
		return telegramFailedDelivery("delivery_unconfirmed", "Telegram delivery could not be confirmed. It will not be repeated automatically because a message may already have been sent.")
	}
	if !response.OK {
		if status == 429 || response.ErrorCode == 429 {
			delay := time.Duration(response.Parameters.RetryAfter) * time.Second
			if delay <= 0 {
				delay = time.Second
			}
			return telegramRetryDelivery("rate_limited", "Telegram asked the Skill to retry later.", delay)
		}
		if response.Parameters.MigrateToChatID != 0 {
			return telegramFailedDelivery("chat_migrated", "This Telegram group migrated. Reconnect its new chat ID before sending again.")
		}
		return telegramFailedDelivery("telegram_rejected", "Telegram rejected the delivery. Check the bot's access to this chat.")
	}
	return nil
}
func (a *telegramConversationAdapter) deliverTelegram(ctx context.Context, token string, request *telegramAdapterRequest) (map[string]interface{}, error) {
	parameters, err := telegramDeliveryTarget(request)
	if err != nil {
		return telegramFailedDelivery("invalid_destination", err.Error()), nil
	}
	request.Endpoint.Address = parameters["chat_id"].(string) // Effective copy decoded from the host envelope, never the durable endpoint.
	if review, ok := request.Delivery.Parameters["reviewRequest"].(map[string]interface{}); ok {
		label, _ := review["label"].(string)
		link, _ := review["url"].(string)
		parsed, parseErr := url.Parse(link)
		if (label != "Review approval" && label != "Complete setup") || parseErr != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || strings.Contains(link, token) {
			return telegramFailedDelivery("invalid_review_link", "The Telegram web review link is invalid."), nil
		}
		for key := range parsed.Query() {
			switch strings.ToLower(key) {
			case "token", "access_token", "api_key", "secret", "password":
				return telegramFailedDelivery("invalid_review_link", "The Telegram web review link contains credential parameters."), nil
			}
		}
		parameters["reply_markup"] = map[string]interface{}{"inline_keyboard": [][]map[string]string{{{"text": label, "url": link}}}}
	}
	operation := request.Delivery.Operation
	if operation == "typing.set" {
		if request.Operation == "lookup" {
			return map[string]interface{}{"status": "unknown"}, nil
		}
		state, _ := request.Delivery.Parameters["state"].(string)
		statusText, hasStatus := request.Delivery.Parameters["status"].(string)
		if active, exists := request.Delivery.Parameters["active"]; (exists && active == false) || state == "active" || state == "suspended" || (state == "" && hasStatus && strings.TrimSpace(statusText) == "") {
			return map[string]interface{}{"outcome": "delivered", "providerMessageId": request.Endpoint.Address + ":typing:" + request.Delivery.ID, "summary": "Telegram typing is no longer renewed."}, nil
		}
		delete(parameters, "reply_parameters")
		delete(parameters, "direct_messages_topic_id")
		parameters["action"] = "typing"
		response, status, err := a.telegramJSON(ctx, token, "sendChatAction", parameters)
		if failure := telegramDeliveryResponse(response, status, err); failure != nil {
			return failure, nil
		}
		return map[string]interface{}{"outcome": "delivered", "providerMessageId": request.Endpoint.Address + ":typing:" + request.Delivery.ID, "summary": "Telegram typing updated."}, nil
	}
	if operation == "message.update" {
		if request.Operation == "lookup" {
			return map[string]interface{}{"status": "unknown"}, nil
		}
		id := strings.TrimSpace(request.Delivery.ProviderMessageID)
		if id == "" {
			id, _ = request.Delivery.Parameters["providerMessageId"].(string)
		}
		id = telegramRawMessageID(id, request.Endpoint.Address)
		parsed, err := strconv.ParseInt(id, 10, 64)
		if err != nil || parsed < 1 {
			return telegramFailedDelivery("invalid_update", "The Telegram message update target is unavailable."), nil
		}
		if len(telegramTextChunks(request.Message.Content)) != 1 {
			return telegramFailedDelivery("invalid_update", "A Telegram message update must contain at most 4096 characters."), nil
		}
		delete(parameters, "reply_parameters")
		delete(parameters, "message_thread_id")
		delete(parameters, "direct_messages_topic_id")
		parameters["message_id"] = parsed
		parameters["text"] = request.Message.Content
		response, status, err := a.telegramJSON(ctx, token, "editMessageText", parameters)
		if failure := telegramDeliveryResponse(response, status, err); failure != nil {
			return failure, nil
		}
		receipt, err := telegramAcknowledgement(response, request.Endpoint.Address)
		if err != nil {
			return telegramFailedDelivery("delivery_unconfirmed", err.Error()), nil
		}
		return map[string]interface{}{"outcome": "delivered", "providerMessageId": request.Endpoint.Address + ":" + receipt, "summary": "Telegram accepted the update."}, nil
	}
	if operation != "message.send" {
		return telegramFailedDelivery("unsupported_operation", "Telegram does not support this delivery operation."), nil
	}
	if len(request.Attachments) > maximumTelegramAttachments {
		return telegramFailedDelivery("attachment_limit", "The Telegram delivery contains too many files."), nil
	}
	for _, file := range request.Attachments {
		if file.ID == "" || file.Name == "" || len(file.Data) == 0 || len(file.Data) > maximumTelegramFileBytes {
			return telegramFailedDelivery("attachment_unavailable", "A Telegram reply attachment is unavailable or exceeds the file size limit."), nil
		}
	}
	texts := telegramTextChunks(request.Message.Content)
	if len(texts) == 0 && len(request.Attachments) == 0 {
		return telegramFailedDelivery("empty_message", "The Telegram reply has no text or files."), nil
	}
	checkpoint, err := telegramCheckpoint(request)
	if err != nil {
		return telegramFailedDelivery("checkpoint_conflict", err.Error()), nil
	}
	receipt := func() string {
		if len(checkpoint.Files) > 0 {
			return checkpoint.Files[len(checkpoint.Files)-1].MessageID
		}
		if len(checkpoint.TextReceipts) > 0 {
			return checkpoint.TextReceipts[len(checkpoint.TextReceipts)-1]
		}
		return ""
	}
	complete := func() bool {
		return len(checkpoint.TextReceipts) == len(texts) && len(checkpoint.Files) == len(request.Attachments)
	}
	if request.Operation == "lookup" {
		if complete() {
			return map[string]interface{}{"status": "found", "providerMessageId": request.Endpoint.Address + ":" + receipt()}, nil
		}
		return map[string]interface{}{"status": "unknown"}, nil
	}
	if !complete() && (request.Delivery.Attempt < 1 || (request.Delivery.Attempt > 1 && checkpoint.SafeRetryAfterAttempt != request.Delivery.Attempt-1)) {
		return telegramProgress(telegramFailedDelivery("delivery_unconfirmed", "A previous Telegram delivery attempt could not be confirmed. It will not be repeated automatically."), checkpoint), nil
	}
	checkpoint.SafeRetryAfterAttempt = 0
	for i := len(checkpoint.TextReceipts); i < len(texts); i++ {
		parameters["text"] = texts[i]
		response, status, err := a.telegramJSON(ctx, token, "sendMessage", parameters)
		delete(parameters, "text")
		if failure := telegramDeliveryResponse(response, status, err); failure != nil {
			if failure["outcome"] == "retry" {
				checkpoint.SafeRetryAfterAttempt = request.Delivery.Attempt
			}
			return telegramProgress(failure, checkpoint), nil
		}
		id, err := telegramAcknowledgement(response, request.Endpoint.Address)
		if err != nil {
			return telegramProgress(telegramFailedDelivery("delivery_unconfirmed", err.Error()), checkpoint), nil
		}
		checkpoint.TextReceipts = append(checkpoint.TextReceipts, id)
	}
	for i := len(checkpoint.Files); i < len(request.Attachments); i++ {
		file := request.Attachments[i]
		method, field := "sendDocument", "document"
		if file.MediaType == "image/png" || file.MediaType == "image/jpeg" || file.MediaType == "image/webp" {
			method = "sendPhoto"
			field = "photo"
		}
		response, status, err := a.telegramUpload(ctx, token, method, field, parameters, file)
		if failure := telegramDeliveryResponse(response, status, err); failure != nil {
			if failure["outcome"] == "retry" {
				checkpoint.SafeRetryAfterAttempt = request.Delivery.Attempt
			}
			return telegramProgress(failure, checkpoint), nil
		}
		id, err := telegramAcknowledgement(response, request.Endpoint.Address)
		if err != nil {
			return telegramProgress(telegramFailedDelivery("delivery_unconfirmed", err.Error()), checkpoint), nil
		}
		checkpoint.Files = append(checkpoint.Files, telegramUploadReceipt{ArtifactID: file.ID, Digest: telegramDigest(file.Data), Name: file.Name, MessageID: id})
	}
	return telegramProgress(map[string]interface{}{"outcome": "delivered", "providerMessageId": request.Endpoint.Address + ":" + receipt(), "summary": "Telegram accepted the reply."}, checkpoint), nil
}
