package main

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/axiom-studio/skills.sdk/executor"
)

const (
	telegramIngressNodeType  = "telegram.conversation.ingress"
	telegramDeliveryNodeType = "telegram.conversation.deliver"
	telegramAdapterEnvelope  = "_opensealConversationAdapterRequest"
	telegramSecretHeader     = "X-Telegram-Bot-Api-Secret-Token"
	maxTelegramAdapterBytes  = 1 << 20
)

type telegramConversationAdapter struct {
	baseURL string
	client  *http.Client
}

func newTelegramConversationAdapter(baseURL string, client *http.Client) *telegramConversationAdapter {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = TelegramAPIBase
	}
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	return &telegramConversationAdapter{baseURL: strings.TrimRight(baseURL, "/"), client: client}
}

type telegramIngressExecutor struct{ adapter *telegramConversationAdapter }

func (e *telegramIngressExecutor) Type() string { return telegramIngressNodeType }

func (e *telegramIngressExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := telegramAdapterConfig(step.Config, resolver)
	defer clearTelegramAdapterConfig(config)
	output, err := e.adapter.ingress(ctx, config)
	if err != nil {
		return nil, err
	}
	return &executor.StepResult{Output: output}, nil
}

type telegramDeliveryExecutor struct{ adapter *telegramConversationAdapter }

func (e *telegramDeliveryExecutor) Type() string { return telegramDeliveryNodeType }

func (e *telegramDeliveryExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := telegramAdapterConfig(step.Config, resolver)
	defer clearTelegramAdapterConfig(config)
	output, err := e.adapter.delivery(ctx, config)
	if err != nil {
		return nil, err
	}
	return &executor.StepResult{Output: output}, nil
}

func telegramAdapterConfig(config map[string]interface{}, resolver executor.TemplateResolver) map[string]interface{} {
	resolved := make(map[string]interface{}, len(config)+1)
	for key, value := range config {
		resolved[key] = value
	}
	if bindings, ok := resolver.(executor.BindingResolver); ok {
		if value, ok := bindings.GetBinding(telegramCredentialKey).(string); ok && strings.TrimSpace(value) != "" {
			resolved[telegramCredentialKey] = strings.TrimSpace(value)
		}
	}
	return resolved
}

func clearTelegramAdapterConfig(config map[string]interface{}) {
	if _, ok := config[telegramCredentialKey]; ok {
		config[telegramCredentialKey] = ""
		delete(config, telegramCredentialKey)
	}
}

type telegramAdapterRequest struct {
	Operation string                        `json:"operation"`
	Endpoint  *telegramConversationEndpoint `json:"endpoint,omitempty"`
	Gateway   *telegramIngressGateway       `json:"gateway,omitempty"`
	Request   *telegramIngressRequest       `json:"request,omitempty"`
	Delivery  *telegramConversationDelivery `json:"delivery,omitempty"`
	Message   *telegramConversationMessage  `json:"message,omitempty"`
}

type telegramConversationEndpoint struct {
	ID            string                 `json:"id"`
	Provider      string                 `json:"provider"`
	Address       string                 `json:"address"`
	Configuration map[string]interface{} `json:"configuration,omitempty"`
}

type telegramConversationScope struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

type telegramIngressGateway struct {
	Scope        telegramConversationScope `json:"scope"`
	DeploymentID string                    `json:"deploymentId"`
	Provider     string                    `json:"provider"`
}

type telegramIngressRequest struct {
	Scope      telegramConversationScope `json:"scope"`
	EndpointID string                    `json:"endpointId"`
	Method     string                    `json:"method"`
	Headers    map[string][]string       `json:"headers,omitempty"`
	Body       []byte                    `json:"body"`
}

type telegramConversationDelivery struct {
	ID                string                 `json:"id"`
	Operation         string                 `json:"operation"`
	ExternalThreadID  string                 `json:"externalThreadId,omitempty"`
	ProviderMessageID string                 `json:"providerMessageId,omitempty"`
	Parameters        map[string]interface{} `json:"parameters,omitempty"`
}

type telegramConversationMessage struct {
	ID        string    `json:"id"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"createdAt"`
}

type telegramNormalizedEvent struct {
	ID                     string                 `json:"id"`
	Type                   string                 `json:"type"`
	ExternalConversationID string                 `json:"externalConversationId"`
	ExternalThreadID       string                 `json:"externalThreadId,omitempty"`
	ExternalMessageID      string                 `json:"externalMessageId,omitempty"`
	ExternalParticipantID  string                 `json:"externalParticipantId,omitempty"`
	ParticipantIsBot       bool                   `json:"participantIsBot,omitempty"`
	Text                   string                 `json:"text,omitempty"`
	MentionsEndpoint       bool                   `json:"mentionsEndpoint,omitempty"`
	Direct                 bool                   `json:"direct,omitempty"`
	OrderingKey            string                 `json:"orderingKey"`
	OccurredAt             time.Time              `json:"occurredAt"`
	Attributes             map[string]interface{} `json:"attributes,omitempty"`
}

type telegramGatewayEvent struct {
	InstallationID string                  `json:"installationId"`
	ApplicationID  string                  `json:"applicationId,omitempty"`
	Address        string                  `json:"address"`
	Event          telegramNormalizedEvent `json:"event"`
}

func decodeTelegramAdapterRequest(config map[string]interface{}) (*telegramAdapterRequest, error) {
	raw, ok := config[telegramAdapterEnvelope]
	if !ok {
		return nil, errors.New("conversation adapter request is required")
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil, errors.New("conversation adapter request is invalid")
	}
	var request telegramAdapterRequest
	if json.Unmarshal(encoded, &request) != nil {
		return nil, errors.New("conversation adapter request is invalid")
	}
	return &request, nil
}

type telegramUpdate struct {
	UpdateID int64           `json:"update_id"`
	Message  telegramMessage `json:"message"`
}

type telegramMessage struct {
	MessageID       int64        `json:"message_id"`
	MessageThreadID int64        `json:"message_thread_id"`
	Date            int64        `json:"date"`
	Text            string       `json:"text"`
	Caption         string       `json:"caption"`
	From            telegramUser `json:"from"`
	Chat            telegramChat `json:"chat"`
}

type telegramUser struct {
	ID       int64  `json:"id"`
	IsBot    bool   `json:"is_bot"`
	Username string `json:"username"`
}

type telegramChat struct {
	ID       int64  `json:"id"`
	Type     string `json:"type"`
	Title    string `json:"title"`
	Username string `json:"username"`
}

func (a *telegramConversationAdapter) ingress(_ context.Context, config map[string]interface{}) (map[string]interface{}, error) {
	request, err := decodeTelegramAdapterRequest(config)
	if err != nil || request.Request == nil ||
		(request.Operation != "ingress" && request.Operation != "gateway_ingress") ||
		(request.Operation == "ingress" && request.Endpoint == nil) ||
		(request.Operation == "gateway_ingress" && request.Gateway == nil) {
		return nil, errors.New("Telegram ingress request is invalid")
	}
	botToken, _ := config[telegramCredentialKey].(string)
	if !verifyTelegramWebhook(request.Request.Headers, botToken) {
		return map[string]interface{}{"statusCode": http.StatusUnauthorized, "contentType": "text/plain", "body": "invalid Telegram webhook secret"}, nil
	}
	var update telegramUpdate
	if json.Unmarshal(request.Request.Body, &update) != nil || update.UpdateID < 1 {
		return map[string]interface{}{"statusCode": http.StatusBadRequest, "contentType": "text/plain", "body": "invalid Telegram update"}, nil
	}
	event, accepted := normalizeTelegramUpdate(update, request.Endpoint)
	if !accepted {
		return map[string]interface{}{"statusCode": http.StatusOK, "contentType": "application/json", "body": `{"ok":true}`, "events": []interface{}{}}, nil
	}
	if request.Operation == "gateway_ingress" {
		installationID := telegramBotID(botToken)
		if installationID == "" {
			return map[string]interface{}{"statusCode": http.StatusUnauthorized}, nil
		}
		return map[string]interface{}{
			"statusCode": http.StatusOK, "contentType": "application/json", "body": `{"ok":true}`,
			"events": []telegramGatewayEvent{{InstallationID: installationID, ApplicationID: installationID, Address: event.ExternalConversationID, Event: event}},
		}, nil
	}
	return map[string]interface{}{
		"statusCode": http.StatusOK, "contentType": "application/json", "body": `{"ok":true}`,
		"events": []telegramNormalizedEvent{event},
	}, nil
}

func verifyTelegramWebhook(headers map[string][]string, botToken string) bool {
	provided := telegramHeader(headers, telegramSecretHeader)
	expected := telegramWebhookSecret(botToken)
	return provided != "" && subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) == 1
}

func telegramHeader(headers map[string][]string, wanted string) string {
	for name, values := range headers {
		if strings.EqualFold(name, wanted) && len(values) > 0 {
			return strings.TrimSpace(values[0])
		}
	}
	return ""
}

func normalizeTelegramUpdate(update telegramUpdate, endpoint *telegramConversationEndpoint) (telegramNormalizedEvent, bool) {
	message := update.Message
	if message.MessageID < 1 || message.Chat.ID == 0 || strings.TrimSpace(firstTelegramText(message)) == "" {
		return telegramNormalizedEvent{}, false
	}
	chatID := strconv.FormatInt(message.Chat.ID, 10)
	if endpoint != nil {
		if endpoint.Provider != "telegram" || (strings.TrimSpace(endpoint.Address) != "" && strings.TrimSpace(endpoint.Address) != chatID) {
			return telegramNormalizedEvent{}, false
		}
	}
	messageID := strconv.FormatInt(message.MessageID, 10)
	threadID := messageID
	if message.MessageThreadID > 0 {
		threadID = strconv.FormatInt(message.MessageThreadID, 10)
	}
	direct := message.Chat.Type == "private"
	text := strings.TrimSpace(firstTelegramText(message))
	botUsername := ""
	if endpoint != nil {
		botUsername, _ = endpoint.Configuration["botUsername"].(string)
		botUsername = strings.TrimPrefix(strings.TrimSpace(botUsername), "@")
	}
	mentioned := direct || (botUsername != "" && strings.Contains(strings.ToLower(text), "@"+strings.ToLower(botUsername)))
	if botUsername != "" {
		text = strings.TrimSpace(strings.ReplaceAll(text, "@"+botUsername, ""))
	}
	occurredAt := time.Unix(message.Date, 0).UTC()
	if message.Date <= 0 {
		occurredAt = time.Now().UTC()
	}
	return telegramNormalizedEvent{
		ID: "telegram:update:" + strconv.FormatInt(update.UpdateID, 10), Type: "conversation.message.received",
		ExternalConversationID: chatID, ExternalThreadID: threadID, ExternalMessageID: messageID,
		ExternalParticipantID: strconv.FormatInt(message.From.ID, 10), ParticipantIsBot: message.From.IsBot,
		Text: text, MentionsEndpoint: mentioned, Direct: direct, OrderingKey: chatID + ":" + threadID,
		OccurredAt: occurredAt,
		Attributes: map[string]interface{}{"chatType": message.Chat.Type, "chatTitle": message.Chat.Title, "chatUsername": message.Chat.Username, "participantUsername": message.From.Username},
	}, true
}

func firstTelegramText(message telegramMessage) string {
	if strings.TrimSpace(message.Text) != "" {
		return message.Text
	}
	return message.Caption
}

func telegramBotID(botToken string) string {
	id, _, found := strings.Cut(strings.TrimSpace(botToken), ":")
	if !found {
		return ""
	}
	if _, err := strconv.ParseInt(id, 10, 64); err != nil {
		return ""
	}
	return id
}

func (a *telegramConversationAdapter) delivery(ctx context.Context, config map[string]interface{}) (map[string]interface{}, error) {
	request, err := decodeTelegramAdapterRequest(config)
	if err != nil || request.Endpoint == nil || request.Delivery == nil || request.Message == nil ||
		(request.Operation != "lookup" && request.Operation != "deliver") {
		return nil, errors.New("Telegram delivery request is invalid")
	}
	botToken, _ := config[telegramCredentialKey].(string)
	if strings.TrimSpace(botToken) == "" {
		return nil, errors.New("Telegram bot credential is unavailable")
	}
	if request.Operation == "lookup" {
		return map[string]interface{}{"status": "unknown"}, nil
	}
	method := "sendMessage"
	parameters := map[string]interface{}{"chat_id": request.Endpoint.Address, "text": request.Message.Content}
	if request.Delivery.Operation == "message.update" {
		method = "editMessageText"
		messageID := strings.TrimSpace(request.Delivery.ProviderMessageID)
		if messageID == "" {
			messageID, _ = request.Delivery.Parameters["providerMessageId"].(string)
		}
		parsed, parseErr := strconv.ParseInt(strings.TrimSpace(messageID), 10, 64)
		if parseErr != nil || parsed < 1 {
			return telegramFailedDelivery("invalid_update", "The Telegram message update target is unavailable."), nil
		}
		parameters["message_id"] = parsed
	} else if replyID, parseErr := strconv.ParseInt(strings.TrimSpace(request.Delivery.ExternalThreadID), 10, 64); parseErr == nil && replyID > 0 {
		parameters["reply_to_message_id"] = replyID
	}
	response, status, err := a.telegramJSON(ctx, botToken, method, parameters)
	if err != nil {
		return telegramRetryDelivery("telegram_unavailable", "Telegram could not be reached.", 0), nil
	}
	if status >= 500 {
		return telegramRetryDelivery("telegram_unavailable", "Telegram is temporarily unavailable.", 0), nil
	}
	if !response.OK {
		if response.ErrorCode == http.StatusTooManyRequests {
			return telegramRetryDelivery("rate_limited", "Telegram asked the Skill to retry later.", time.Duration(response.Parameters.RetryAfter)*time.Second), nil
		}
		return telegramFailedDelivery("telegram_rejected", "Telegram rejected the message."), nil
	}
	var result struct {
		MessageID int64 `json:"message_id"`
	}
	if json.Unmarshal(response.Result, &result) != nil || result.MessageID < 1 {
		return telegramRetryDelivery("missing_acknowledgement", "Telegram did not acknowledge the message.", 0), nil
	}
	return map[string]interface{}{"outcome": "delivered", "providerMessageId": strconv.FormatInt(result.MessageID, 10), "summary": "Telegram accepted the message."}, nil
}

func (a *telegramConversationAdapter) telegramJSON(ctx context.Context, botToken, method string, parameters map[string]interface{}) (TelegramResponse, int, error) {
	encoded, err := json.Marshal(parameters)
	if err != nil {
		return TelegramResponse{}, 0, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+strings.TrimSpace(botToken)+"/"+method, bytes.NewReader(encoded))
	if err != nil {
		return TelegramResponse{}, 0, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := a.client.Do(request)
	if err != nil {
		return TelegramResponse{}, 0, err
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, maxTelegramAdapterBytes+1))
	if err != nil || len(payload) > maxTelegramAdapterBytes {
		return TelegramResponse{}, response.StatusCode, errors.New("Telegram response is invalid")
	}
	var result TelegramResponse
	if json.Unmarshal(payload, &result) != nil {
		return TelegramResponse{}, response.StatusCode, fmt.Errorf("Telegram response is invalid")
	}
	return result, response.StatusCode, nil
}

func telegramRetryDelivery(code, summary string, retryAfter time.Duration) map[string]interface{} {
	result := map[string]interface{}{"outcome": "retry", "errorCode": code, "summary": summary}
	if retryAfter > 0 {
		result["retryAfterMs"] = retryAfter.Milliseconds()
	}
	return result
}

func telegramFailedDelivery(code, summary string) map[string]interface{} {
	return map[string]interface{}{"outcome": "failed", "errorCode": code, "summary": summary}
}
