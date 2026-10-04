package main

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
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
		client = &http.Client{Timeout: 45 * time.Second}
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
	config[telegramCredentialKey] = ""
	delete(config, telegramCredentialKey)
}

type telegramAdapterRequest struct {
	Webhook     *telegramWebhookRequest           `json:"webhook,omitempty"`
	Operation   string                            `json:"operation"`
	Endpoint    *telegramConversationEndpoint     `json:"endpoint,omitempty"`
	Gateway     *telegramIngressGateway           `json:"gateway,omitempty"`
	Request     *telegramIngressRequest           `json:"request,omitempty"`
	Delivery    *telegramConversationDelivery     `json:"delivery,omitempty"`
	Message     *telegramConversationMessage      `json:"message,omitempty"`
	Event       *telegramNormalizedEvent          `json:"event,omitempty"`
	Attachment  *telegramConversationFile         `json:"attachment,omitempty"`
	Attachments []telegramConversationFileContent `json:"attachments,omitempty"`
}
type telegramConversationEndpoint struct {
	ID               string                 `json:"id"`
	Provider         string                 `json:"provider"`
	Address          string                 `json:"address"`
	InstallationID   string                 `json:"installationId,omitempty"`
	ApplicationID    string                 `json:"applicationId,omitempty"`
	InstallationWide bool                   `json:"installationWide,omitempty"`
	Configuration    map[string]interface{} `json:"configuration,omitempty"`
}
type telegramConversationScope struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}
type telegramIngressGateway struct {
	InstallationID string                    `json:"installationId,omitempty"`
	ApplicationID  string                    `json:"applicationId,omitempty"`
	Scope          telegramConversationScope `json:"scope"`
	DeploymentID   string                    `json:"deploymentId"`
	Provider       string                    `json:"provider"`
}
type telegramIngressRequest struct {
	Scope      telegramConversationScope `json:"scope"`
	EndpointID string                    `json:"endpointId"`
	Method     string                    `json:"method"`
	Headers    map[string][]string       `json:"headers,omitempty"`
	Body       []byte                    `json:"body"`
}
type telegramConversationDelivery struct {
	Attempt                int                    `json:"attempt"`
	ID                     string                 `json:"id"`
	Operation              string                 `json:"operation"`
	ExternalConversationID string                 `json:"externalConversationId,omitempty"`
	ExternalThreadID       string                 `json:"externalThreadId,omitempty"`
	ProviderMessageID      string                 `json:"providerMessageId,omitempty"`
	Parameters             map[string]interface{} `json:"parameters,omitempty"`
	Progress               map[string]interface{} `json:"progress,omitempty"`
}
type telegramConversationMessage struct {
	ID        string    `json:"id"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"createdAt"`
}
type telegramNormalizedEvent struct {
	Source                   *telegramMessageSource     `json:"source,omitempty"`
	ParticipantDisplayName   string                     `json:"participantDisplayName,omitempty"`
	ReplyToExternalMessageID string                     `json:"replyToExternalMessageId,omitempty"`
	ID                       string                     `json:"id"`
	Type                     string                     `json:"type"`
	ExternalConversationID   string                     `json:"externalConversationId"`
	ExternalThreadID         string                     `json:"externalThreadId,omitempty"`
	ExternalMessageID        string                     `json:"externalMessageId,omitempty"`
	ExternalParticipantID    string                     `json:"externalParticipantId,omitempty"`
	ParticipantIsBot         bool                       `json:"participantIsBot,omitempty"`
	Text                     string                     `json:"text,omitempty"`
	MentionsEndpoint         bool                       `json:"mentionsEndpoint,omitempty"`
	Direct                   bool                       `json:"direct,omitempty"`
	OrderingKey              string                     `json:"orderingKey"`
	OccurredAt               time.Time                  `json:"occurredAt"`
	Attributes               map[string]interface{}     `json:"attributes,omitempty"`
	Attachments              []telegramConversationFile `json:"attachments,omitempty"`
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
func (a *telegramConversationAdapter) ingress(ctx context.Context, config map[string]interface{}) (map[string]interface{}, error) {
	request, err := decodeTelegramAdapterRequest(config)
	token, _ := config[telegramCredentialKey].(string)
	if err == nil && (request.Operation == "webhook.configure" || request.Operation == "webhook.status" || request.Operation == "webhook.remove") {
		return a.manageWebhook(ctx, token, request)
	}
	if err != nil || request.Request == nil || (request.Operation != "ingress" && request.Operation != "gateway_ingress") || (request.Operation == "ingress" && request.Endpoint == nil) || (request.Operation == "gateway_ingress" && (request.Gateway == nil || request.Gateway.Provider != "telegram")) {
		return nil, errors.New("Telegram ingress request is invalid")
	}
	if request.Gateway != nil && ((request.Gateway.InstallationID != "" && request.Gateway.InstallationID != telegramBotID(token)) || (request.Gateway.ApplicationID != "" && request.Gateway.ApplicationID != telegramBotID(token))) {
		return map[string]interface{}{"statusCode": http.StatusUnauthorized, "body": "Telegram installation does not match the reviewed gateway"}, nil
	}
	if request.Request.Method != http.MethodPost || !verifyTelegramWebhook(request.Request.Headers, token) {
		return map[string]interface{}{"statusCode": http.StatusUnauthorized, "contentType": "text/plain", "body": "invalid Telegram webhook secret"}, nil
	}
	var update telegramUpdate
	if len(request.Request.Body) > maxTelegramAdapterBytes || json.Unmarshal(request.Request.Body, &update) != nil || update.UpdateID < 1 {
		return map[string]interface{}{"statusCode": http.StatusBadRequest, "contentType": "text/plain", "body": "invalid Telegram update"}, nil
	}
	normalizationEndpoint := request.Endpoint
	if normalizationEndpoint == nil {
		bot, identityErr := a.getTelegramIdentity(ctx, token)
		if identityErr != nil {
			return map[string]interface{}{"statusCode": http.StatusServiceUnavailable, "contentType": "text/plain", "body": "Telegram bot identity is temporarily unavailable"}, nil
		}
		normalizationEndpoint = &telegramConversationEndpoint{Provider: "telegram", Address: "*", InstallationID: strconv.FormatInt(bot.ID, 10), Configuration: map[string]interface{}{"botId": strconv.FormatInt(bot.ID, 10), "botUsername": bot.Username}}
	}
	event, accepted := normalizeTelegramUpdate(update, normalizationEndpoint)
	if accepted {
		event.Attributes["botId"] = telegramBotID(token)
		if event.Source != nil {
			event.Source.WorkspaceID = telegramBotID(token)
		}
	}
	if !accepted {
		return map[string]interface{}{"statusCode": http.StatusOK, "contentType": "application/json", "body": `{"ok":true}`, "events": []interface{}{}}, nil
	}
	if request.Operation == "gateway_ingress" {
		installationID := telegramBotID(token)
		if installationID == "" {
			return map[string]interface{}{"statusCode": http.StatusUnauthorized}, nil
		}
		// Credentials are verified with getMe before the managed connector starts;
		// the installation key is the same numeric Bot API user ID, never a chat ID.
		return map[string]interface{}{"statusCode": http.StatusOK, "contentType": "application/json", "body": `{"ok":true}`, "events": []telegramGatewayEvent{{InstallationID: installationID, ApplicationID: installationID, Address: event.ExternalConversationID, Event: event}}}, nil
	}
	return map[string]interface{}{"statusCode": http.StatusOK, "contentType": "application/json", "body": `{"ok":true}`, "events": []telegramNormalizedEvent{event}}, nil
}
func verifyTelegramWebhook(headers map[string][]string, token string) bool {
	if strings.TrimSpace(token) == "" {
		return false
	}
	provided := telegramHeader(headers, telegramSecretHeader)
	expected := telegramWebhookSecret(token)
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
func telegramBotID(token string) string {
	id, _, found := strings.Cut(strings.TrimSpace(token), ":")
	if !found {
		return ""
	}
	parsed, err := strconv.ParseInt(id, 10, 64)
	if err != nil || parsed < 1 {
		return ""
	}
	return strconv.FormatInt(parsed, 10)
}
func (a *telegramConversationAdapter) delivery(ctx context.Context, config map[string]interface{}) (map[string]interface{}, error) {
	request, err := decodeTelegramAdapterRequest(config)
	if err != nil || request.Endpoint == nil || request.Endpoint.Provider != "telegram" {
		return nil, errors.New("Telegram delivery request is invalid")
	}
	token, _ := config[telegramCredentialKey].(string)
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("Telegram bot credential is unavailable")
	}
	if request.Endpoint.InstallationID != "" && request.Endpoint.InstallationID != telegramBotID(token) {
		return nil, errors.New("Telegram bot does not match the reviewed installation")
	}
	if request.Endpoint.ApplicationID != "" && request.Endpoint.ApplicationID != telegramBotID(token) {
		return nil, errors.New("Telegram bot does not match the reviewed application")
	}
	switch request.Operation {
	case "attachment":
		return a.readAttachment(ctx, token, request)
	case "context":
		return telegramReadContext(request), nil
	case "deliver", "lookup":
		if request.Delivery == nil || request.Message == nil {
			return nil, errors.New("Telegram delivery request is invalid")
		}
		return a.deliverTelegram(ctx, token, request)
	default:
		return nil, errors.New("Telegram delivery operation is invalid")
	}
}

// Provider URLs contain the bot token. Never return raw request/network errors,
// provider descriptions, response bodies, or redirects to logs or user output.
func (a *telegramConversationAdapter) telegramJSON(ctx context.Context, token, method string, parameters map[string]interface{}) (TelegramResponse, int, error) {
	if telegramBotID(token) == "" || strings.ContainsAny(token, "/?# \t\r\n") {
		return TelegramResponse{}, 0, errors.New("Telegram bot credential is invalid")
	}
	encoded, err := json.Marshal(parameters)
	if err != nil {
		return TelegramResponse{}, 0, errors.New("Telegram request is invalid")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+strings.TrimSpace(token)+"/"+method, bytes.NewReader(encoded))
	if err != nil {
		return TelegramResponse{}, 0, errors.New("Telegram request is invalid")
	}
	request.Header.Set("Content-Type", "application/json")
	return a.telegramRequest(request)
}
func (a *telegramConversationAdapter) telegramRequest(request *http.Request) (TelegramResponse, int, error) {
	client := *a.client
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return errors.New("Telegram redirects are refused") }
	response, err := client.Do(request)
	if err != nil {
		return TelegramResponse{}, 0, errors.New("Telegram request outcome could not be confirmed")
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, maxTelegramAdapterBytes+1))
	if err != nil || len(payload) > maxTelegramAdapterBytes {
		return TelegramResponse{}, response.StatusCode, errors.New("Telegram response is invalid")
	}
	var result TelegramResponse
	if json.Unmarshal(payload, &result) != nil {
		return TelegramResponse{}, response.StatusCode, errors.New("Telegram response is invalid")
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
