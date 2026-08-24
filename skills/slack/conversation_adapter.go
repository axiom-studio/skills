package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/axiom-studio/skills.sdk/executor"
)

const (
	slackIngressNodeType  = "slack.conversation.ingress"
	slackDeliveryNodeType = "slack.conversation.deliver"
	adapterEnvelopeKey    = "_opensealConversationAdapterRequest"
	slackConnectionKey    = "slack_bot_token"
	slackSigningSecretKey = "slack_signing_secret"
	maxSlackResponseBytes = 1 << 20
)

type slackAdapter struct {
	signingSecret string
	baseURL       string
	client        *http.Client
	now           func() time.Time
}

func newSlackAdapter(signingSecret, baseURL string, client *http.Client) *slackAdapter {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = "https://slack.com/api"
	}
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &slackAdapter{
		signingSecret: strings.TrimSpace(signingSecret),
		baseURL:       strings.TrimRight(baseURL, "/"),
		client:        client,
		now:           time.Now,
	}
}

type slackIngressExecutor struct{ adapter *slackAdapter }

func (e *slackIngressExecutor) Type() string { return slackIngressNodeType }

func (e *slackIngressExecutor) Execute(
	ctx context.Context,
	step *executor.StepDefinition,
	resolver executor.TemplateResolver,
) (*executor.StepResult, error) {
	config := slackAdapterConfig(step.Config, resolver, slackSigningSecretKey)
	defer clearSlackAdapterConfig(config, slackSigningSecretKey)
	output, err := e.adapter.ingress(ctx, config)
	if err != nil {
		return nil, err
	}
	return &executor.StepResult{Output: output}, nil
}

type slackDeliveryExecutor struct{ adapter *slackAdapter }

func (e *slackDeliveryExecutor) Type() string { return slackDeliveryNodeType }

func (e *slackDeliveryExecutor) Execute(
	ctx context.Context,
	step *executor.StepDefinition,
	resolver executor.TemplateResolver,
) (*executor.StepResult, error) {
	config := slackAdapterConfig(step.Config, resolver, slackConnectionKey)
	defer clearSlackAdapterConfig(config, slackConnectionKey)
	output, err := e.adapter.delivery(ctx, config)
	if err != nil {
		return nil, err
	}
	return &executor.StepResult{Output: output}, nil
}

func slackAdapterConfig(config map[string]interface{}, resolver executor.TemplateResolver, credentials ...string) map[string]interface{} {
	resolved := make(map[string]interface{}, len(config)+len(credentials))
	for key, value := range config {
		resolved[key] = value
	}
	bindings, _ := resolver.(executor.BindingResolver)
	if bindings == nil {
		return resolved
	}
	for _, credential := range credentials {
		if value, ok := bindings.GetBinding(credential).(string); ok && strings.TrimSpace(value) != "" {
			resolved[credential] = value
		}
	}
	return resolved
}

func clearSlackAdapterConfig(config map[string]interface{}, credentials ...string) {
	for _, credential := range credentials {
		if _, ok := config[credential]; ok {
			config[credential] = ""
			delete(config, credential)
		}
	}
}

type adapterEnvelope struct {
	Operation string                      `json:"operation"`
	Endpoint  *conversationEndpoint       `json:"endpoint"`
	Gateway   *conversationIngressGateway `json:"gateway,omitempty"`
	Request   *conversationIngressRequest `json:"request,omitempty"`
	Delivery  *conversationDelivery       `json:"delivery,omitempty"`
	Message   *conversationMessage        `json:"message,omitempty"`
}

// These DTOs implement the versioned JSON wire protocol declared by the
// Skill manifest. Provider Skills intentionally depend only on that protocol:
// unknown kernel fields are ignored and no host implementation package is
// imported.
type conversationEndpoint struct {
	ID            string                 `json:"id"`
	Provider      string                 `json:"provider"`
	Address       string                 `json:"address"`
	Configuration map[string]interface{} `json:"configuration,omitempty"`
}

type conversationScope struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

type conversationIngressGateway struct {
	Scope        conversationScope `json:"scope"`
	DeploymentID string            `json:"deploymentId"`
	Provider     string            `json:"provider"`
}

type conversationIngressRequest struct {
	Scope      conversationScope   `json:"scope"`
	EndpointID string              `json:"endpointId"`
	Method     string              `json:"method"`
	Headers    map[string][]string `json:"headers,omitempty"`
	Body       []byte              `json:"body"`
}

type conversationDelivery struct {
	ID                string                 `json:"id"`
	Operation         string                 `json:"operation"`
	ExternalThreadID  string                 `json:"externalThreadId,omitempty"`
	ProviderMessageID string                 `json:"providerMessageId,omitempty"`
	Parameters        map[string]interface{} `json:"parameters,omitempty"`
}

type conversationMessage struct {
	ID        string    `json:"id"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"createdAt"`
}

type normalizedConversationEvent struct {
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

type conversationGatewayEvent struct {
	InstallationID string                      `json:"installationId"`
	ApplicationID  string                      `json:"applicationId,omitempty"`
	Address        string                      `json:"address"`
	Event          normalizedConversationEvent `json:"event"`
}

func decodeAdapterEnvelope(config map[string]interface{}) (*adapterEnvelope, error) {
	raw, ok := config[adapterEnvelopeKey]
	if !ok {
		return nil, errors.New("conversation adapter request is required")
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil, errors.New("conversation adapter request is invalid")
	}
	var envelope adapterEnvelope
	if err := json.Unmarshal(encoded, &envelope); err != nil {
		return nil, errors.New("conversation adapter request is invalid")
	}
	return &envelope, nil
}

type slackEventsEnvelope struct {
	Type           string               `json:"type"`
	Challenge      string               `json:"challenge"`
	TeamID         string               `json:"team_id"`
	APIAppID       string               `json:"api_app_id"`
	EventID        string               `json:"event_id"`
	EventTime      int64                `json:"event_time"`
	Authorizations []slackAuthorization `json:"authorizations"`
	Event          slackEvent           `json:"event"`
}

type slackAuthorization struct {
	UserID string `json:"user_id"`
	TeamID string `json:"team_id"`
	IsBot  bool   `json:"is_bot"`
}

type slackEvent struct {
	Type        string `json:"type"`
	Subtype     string `json:"subtype"`
	User        string `json:"user"`
	BotID       string `json:"bot_id"`
	Text        string `json:"text"`
	Channel     string `json:"channel"`
	ChannelType string `json:"channel_type"`
	Timestamp   string `json:"ts"`
	ThreadTS    string `json:"thread_ts"`
}

type slackInteraction struct {
	Type      string `json:"type"`
	APIAppID  string `json:"api_app_id"`
	ActionTS  string `json:"action_ts"`
	TriggerID string `json:"trigger_id"`
	Team      struct {
		ID string `json:"id"`
	} `json:"team"`
	User struct {
		ID       string `json:"id"`
		Username string `json:"username"`
	} `json:"user"`
	Channel struct {
		ID string `json:"id"`
	} `json:"channel"`
	Container struct {
		MessageTS string `json:"message_ts"`
		ThreadTS  string `json:"thread_ts"`
	} `json:"container"`
	Message struct {
		Text   string            `json:"text"`
		Blocks []json.RawMessage `json:"blocks"`
	} `json:"message"`
	Actions []struct {
		ActionID string `json:"action_id"`
		Value    string `json:"value"`
		ActionTS string `json:"action_ts"`
	} `json:"actions"`
	View struct {
		ID              string `json:"id"`
		CallbackID      string `json:"callback_id"`
		PrivateMetadata string `json:"private_metadata"`
		State           struct {
			Values map[string]map[string]struct {
				Type  string `json:"type"`
				Value string `json:"value"`
			} `json:"values"`
		} `json:"state"`
	} `json:"view"`
}

type slackApprovalValue struct {
	ApprovalID       string    `json:"approvalId"`
	ApprovalRevision int64     `json:"approvalRevision"`
	ActionCallID     string    `json:"actionCallId"`
	InvocationDigest string    `json:"invocationDigest"`
	ExpiresAt        time.Time `json:"expiresAt"`
	DestinationID    string    `json:"destinationId,omitempty"`
}

func (a *slackAdapter) ingress(_ context.Context, config map[string]interface{}) (map[string]interface{}, error) {
	envelope, err := decodeAdapterEnvelope(config)
	if err != nil || envelope.Request == nil ||
		(envelope.Operation != "ingress" && envelope.Operation != "gateway_ingress") ||
		(envelope.Operation == "ingress" && envelope.Endpoint == nil) ||
		(envelope.Operation == "gateway_ingress" && envelope.Gateway == nil) {
		return nil, errors.New("Slack ingress request is invalid")
	}
	signingSecret := a.signingSecret
	if resolved, ok := config[slackSigningSecretKey].(string); ok && strings.TrimSpace(resolved) != "" {
		signingSecret = strings.TrimSpace(resolved)
	}
	if !a.verifySlackRequest(envelope.Request, signingSecret) {
		return map[string]interface{}{
			"statusCode": http.StatusUnauthorized, "contentType": "text/plain",
			"body": []byte("invalid Slack signature"),
		}, nil
	}
	if strings.Contains(strings.ToLower(firstHeader(envelope.Request.Headers, "Content-Type")), "application/x-www-form-urlencoded") {
		return normalizeSlackInteraction(envelope)
	}
	var payload slackEventsEnvelope
	if err := json.Unmarshal(envelope.Request.Body, &payload); err != nil {
		return map[string]interface{}{
			"statusCode": http.StatusBadRequest, "contentType": "text/plain",
			"body": []byte("invalid Slack event"),
		}, nil
	}
	if payload.Type == "url_verification" {
		if strings.TrimSpace(payload.Challenge) == "" {
			return map[string]interface{}{"statusCode": http.StatusBadRequest}, nil
		}
		body, _ := json.Marshal(map[string]string{"challenge": payload.Challenge})
		return map[string]interface{}{
			"statusCode": http.StatusOK, "contentType": "application/json", "body": string(body),
		}, nil
	}
	if payload.Type != "event_callback" || strings.TrimSpace(payload.EventID) == "" {
		return map[string]interface{}{"statusCode": http.StatusOK, "events": []interface{}{}}, nil
	}
	if envelope.Operation == "ingress" && !slackEndpointMatches(envelope.Endpoint, payload) {
		return map[string]interface{}{"statusCode": http.StatusOK, "events": []interface{}{}}, nil
	}
	event, accepted := normalizeSlackEvent(payload)
	if !accepted {
		return map[string]interface{}{"statusCode": http.StatusOK, "events": []interface{}{}}, nil
	}
	if envelope.Operation == "gateway_ingress" {
		teamID := strings.TrimSpace(payload.TeamID)
		if teamID == "" {
			for _, authorization := range payload.Authorizations {
				if strings.TrimSpace(authorization.TeamID) != "" {
					teamID = strings.TrimSpace(authorization.TeamID)
					break
				}
			}
		}
		if teamID == "" {
			return map[string]interface{}{"statusCode": http.StatusBadRequest}, nil
		}
		return map[string]interface{}{
			"statusCode": http.StatusOK, "contentType": "application/json", "body": `{"ok":true}`,
			"events": []conversationGatewayEvent{{
				InstallationID: teamID, ApplicationID: strings.TrimSpace(payload.APIAppID),
				Address: strings.TrimSpace(payload.Event.Channel), Event: event,
			}},
		}, nil
	}
	return map[string]interface{}{
		"statusCode": http.StatusOK, "contentType": "application/json", "body": `{"ok":true}`,
		"events": []normalizedConversationEvent{event},
	}, nil
}

func normalizeSlackInteraction(envelope *adapterEnvelope) (map[string]interface{}, error) {
	if envelope.Operation != "ingress" || envelope.Endpoint == nil {
		return map[string]interface{}{"statusCode": http.StatusBadRequest}, nil
	}
	form, err := url.ParseQuery(string(envelope.Request.Body))
	if err != nil || strings.TrimSpace(form.Get("payload")) == "" {
		return map[string]interface{}{"statusCode": http.StatusBadRequest}, nil
	}
	var payload slackInteraction
	if json.Unmarshal([]byte(form.Get("payload")), &payload) != nil || payload.Type != "block_actions" || len(payload.Actions) != 1 {
		return map[string]interface{}{"statusCode": http.StatusBadRequest}, nil
	}
	if envelope.Endpoint.Address != payload.Channel.ID ||
		(stringConfiguration(envelope.Endpoint.Configuration, "teamId") != "" && stringConfiguration(envelope.Endpoint.Configuration, "teamId") != payload.Team.ID) ||
		(stringConfiguration(envelope.Endpoint.Configuration, "appId") != "" && stringConfiguration(envelope.Endpoint.Configuration, "appId") != payload.APIAppID) {
		return map[string]interface{}{"statusCode": http.StatusOK, "events": []interface{}{}}, nil
	}
	action := payload.Actions[0]
	decision := strings.TrimPrefix(strings.TrimSpace(action.ActionID), "openseal_approval_")
	if decision != "approve" && decision != "reject" && decision != "request_changes" {
		return map[string]interface{}{"statusCode": http.StatusBadRequest}, nil
	}
	var reviewed slackApprovalValue
	if json.Unmarshal([]byte(action.Value), &reviewed) != nil || reviewed.ApprovalID == "" || reviewed.ApprovalRevision < 1 || reviewed.ActionCallID == "" || reviewed.InvocationDigest == "" {
		return map[string]interface{}{"statusCode": http.StatusBadRequest}, nil
	}
	principal, ok := slackApprovalPrincipal(envelope.Endpoint.Configuration, payload.User.ID)
	if !ok {
		return map[string]interface{}{"statusCode": http.StatusForbidden, "contentType": "text/plain", "body": []byte("Slack user is not authorized to decide this approval")}, nil
	}
	eventID := "slack:approval:" + payload.Team.ID + ":" + strings.TrimSpace(action.ActionTS) + ":" + payload.User.ID
	event := normalizedConversationEvent{
		ID: eventID, Type: "conversation.approval.decided", ExternalConversationID: payload.Channel.ID,
		ExternalThreadID: payload.Container.ThreadTS, ExternalMessageID: payload.Container.MessageTS,
		ExternalParticipantID: payload.User.ID, OrderingKey: payload.Channel.ID + ":" + payload.Container.MessageTS,
		OccurredAt: slackTimestamp(firstNonEmpty(action.ActionTS, payload.ActionTS)),
		Attributes: map[string]interface{}{
			"approvalId": reviewed.ApprovalID, "approvalRevision": reviewed.ApprovalRevision,
			"actionCallId": reviewed.ActionCallID, "invocationDigest": reviewed.InvocationDigest,
			"decision": decision, "principalType": principal["type"], "principalId": principal["id"],
			"providerUserId": payload.User.ID,
		},
	}
	if event.OccurredAt.IsZero() {
		event.OccurredAt = time.Now().UTC()
	}
	return map[string]interface{}{"statusCode": http.StatusOK, "contentType": "application/json", "body": `{"ok":true}`, "events": []normalizedConversationEvent{event}}, nil
}

func slackApprovalPrincipal(config map[string]interface{}, userID string) (map[string]string, bool) {
	raw, ok := config["approvalPrincipals"].(map[string]interface{})
	if !ok {
		return nil, false
	}
	entry, ok := raw[userID].(map[string]interface{})
	if !ok {
		return nil, false
	}
	typeName, _ := entry["type"].(string)
	id, _ := entry["id"].(string)
	if strings.TrimSpace(typeName) == "" || strings.TrimSpace(id) == "" {
		return nil, false
	}
	return map[string]string{"type": strings.TrimSpace(typeName), "id": strings.TrimSpace(id)}, true
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func (a *slackAdapter) verifySlackRequest(request *conversationIngressRequest, signingSecret string) bool {
	if request == nil || signingSecret == "" {
		return false
	}
	timestamp := firstHeader(request.Headers, "X-Slack-Request-Timestamp")
	signature := firstHeader(request.Headers, "X-Slack-Signature")
	seconds, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil || signature == "" {
		return false
	}
	occurred := time.Unix(seconds, 0)
	if delta := a.now().UTC().Sub(occurred); delta > 5*time.Minute || delta < -5*time.Minute {
		return false
	}
	mac := hmac.New(sha256.New, []byte(signingSecret))
	_, _ = mac.Write([]byte("v0:" + timestamp + ":"))
	_, _ = mac.Write(request.Body)
	expected := "v0=" + hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(signature))
}

func firstHeader(headers map[string][]string, wanted string) string {
	for name, values := range headers {
		if strings.EqualFold(name, wanted) && len(values) > 0 {
			return strings.TrimSpace(values[0])
		}
	}
	return ""
}

func slackEndpointMatches(endpoint *conversationEndpoint, payload slackEventsEnvelope) bool {
	if endpoint == nil || endpoint.Provider != "slack" {
		return false
	}
	if endpoint.Address != "" && endpoint.Address != payload.Event.Channel {
		return false
	}
	if teamID := stringConfiguration(endpoint.Configuration, "teamId"); teamID != "" && teamID != payload.TeamID {
		return false
	}
	if appID := stringConfiguration(endpoint.Configuration, "appId"); appID != "" && appID != payload.APIAppID {
		return false
	}
	return true
}

func normalizeSlackEvent(payload slackEventsEnvelope) (normalizedConversationEvent, bool) {
	source := payload.Event
	if source.Type != "message" && source.Type != "app_mention" {
		return normalizedConversationEvent{}, false
	}
	switch source.Subtype {
	case "", "bot_message":
	default:
		return normalizedConversationEvent{}, false
	}
	if strings.TrimSpace(source.Channel) == "" || strings.TrimSpace(source.Timestamp) == "" {
		return normalizedConversationEvent{}, false
	}
	botUserID := ""
	for _, authorization := range payload.Authorizations {
		if authorization.IsBot && authorization.UserID != "" {
			botUserID = authorization.UserID
			break
		}
	}
	mention := ""
	if botUserID != "" {
		mention = "<@" + botUserID + ">"
	}
	text := strings.TrimSpace(source.Text)
	mentionsEndpoint := source.Type == "app_mention" || (mention != "" && strings.Contains(text, mention))
	if mention != "" {
		text = strings.TrimSpace(strings.ReplaceAll(text, mention, ""))
	}
	threadID := strings.TrimSpace(source.ThreadTS)
	if threadID == "" {
		threadID = strings.TrimSpace(source.Timestamp)
	}
	occurredAt := slackTimestamp(source.Timestamp)
	if occurredAt.IsZero() && payload.EventTime > 0 {
		occurredAt = time.Unix(payload.EventTime, 0).UTC()
	}
	participantID := strings.TrimSpace(source.User)
	if participantID == "" {
		participantID = strings.TrimSpace(source.BotID)
	}
	eventID := "slack:message:" + payload.TeamID + ":" + source.Channel + ":" + source.Timestamp
	if strings.TrimSpace(payload.TeamID) == "" {
		eventID = "slack:event:" + payload.EventID
	}
	return normalizedConversationEvent{
		ID: eventID, Type: "conversation.message.received",
		ExternalConversationID: source.Channel, ExternalThreadID: threadID,
		ExternalMessageID: source.Timestamp, ExternalParticipantID: participantID,
		ParticipantIsBot: source.BotID != "" || source.Subtype == "bot_message",
		Text:             text, MentionsEndpoint: mentionsEndpoint, Direct: source.ChannelType == "im",
		OrderingKey: source.Channel + ":" + source.Timestamp, OccurredAt: occurredAt,
		Attributes: map[string]interface{}{
			"teamId": payload.TeamID, "appId": payload.APIAppID, "channelType": source.ChannelType,
			"providerEventId": payload.EventID,
		},
	}, true
}

func slackTimestamp(value string) time.Time {
	secondsText, fractionText, found := strings.Cut(strings.TrimSpace(value), ".")
	seconds, err := strconv.ParseInt(secondsText, 10, 64)
	if err != nil {
		return time.Time{}
	}
	nanoseconds := int64(0)
	if found {
		fractionText += strings.Repeat("0", 9)
		nanoseconds, _ = strconv.ParseInt(fractionText[:9], 10, 64)
	}
	return time.Unix(seconds, nanoseconds).UTC()
}

func stringConfiguration(config map[string]interface{}, key string) string {
	value, _ := config[key].(string)
	return strings.TrimSpace(value)
}

type slackDeliveryResponse struct {
	OK        bool           `json:"ok"`
	Error     string         `json:"error"`
	Channel   string         `json:"channel"`
	Timestamp string         `json:"ts"`
	Messages  []slackMessage `json:"messages"`
}

type slackMessage struct {
	Timestamp string        `json:"ts"`
	Metadata  slackMetadata `json:"metadata"`
}

type slackMetadata struct {
	EventType    string            `json:"event_type"`
	EventPayload map[string]string `json:"event_payload"`
}

func (a *slackAdapter) delivery(ctx context.Context, config map[string]interface{}) (map[string]interface{}, error) {
	envelope, err := decodeAdapterEnvelope(config)
	if err != nil || envelope.Delivery == nil || envelope.Message == nil ||
		(envelope.Operation != "lookup" && envelope.Operation != "deliver") {
		return nil, errors.New("Slack delivery request is invalid")
	}
	token, _ := config[slackConnectionKey].(string)
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("Slack OAuth connection is unavailable")
	}
	if envelope.Operation == "lookup" {
		return a.lookup(ctx, token, envelope)
	}
	return a.deliver(ctx, token, envelope)
}

func (a *slackAdapter) deliver(ctx context.Context, token string, envelope *adapterEnvelope) (map[string]interface{}, error) {
	path := "/chat.postMessage"
	body := map[string]interface{}{
		"channel": envelope.Endpoint.Address,
		"text":    envelope.Message.Content,
		"metadata": map[string]interface{}{
			"event_type":    "openseal_conversation_delivery",
			"event_payload": map[string]string{"delivery_id": envelope.Delivery.ID},
		},
	}
	if approval, ok := envelope.Delivery.Parameters["approval"].(map[string]interface{}); ok {
		blocks, err := slackApprovalBlocks(approval, envelope.Endpoint.ID)
		if err != nil {
			return failedDelivery("invalid_approval", "The approval card is invalid."), nil
		}
		body["blocks"] = blocks
	}
	if envelope.Delivery.Operation == "message.update" {
		providerMessageID, _ := envelope.Delivery.Parameters["providerMessageId"].(string)
		providerMessageID = strings.TrimSpace(providerMessageID)
		if providerMessageID == "" {
			return failedDelivery("invalid_update", "The Slack message update target is unavailable."), nil
		}
		path = "/chat.update"
		body["ts"] = providerMessageID
		delete(body, "metadata")
	}
	if threadID := strings.TrimSpace(envelope.Delivery.ExternalThreadID); threadID != "" {
		body["thread_ts"] = threadID
	}
	response, status, retryAfter, err := a.slackJSON(ctx, token, http.MethodPost, path, nil, body)
	if err != nil {
		return retryDelivery("slack_unavailable", "Slack could not be reached.", retryAfter), nil
	}
	if status == http.StatusTooManyRequests {
		return retryDelivery("rate_limited", "Slack asked the Skill to retry later.", retryAfter), nil
	}
	if status < 200 || status >= 300 {
		if status >= 500 {
			return retryDelivery("slack_unavailable", "Slack is temporarily unavailable.", retryAfter), nil
		}
		return failedDelivery("slack_rejected", "Slack rejected the message."), nil
	}
	var result slackDeliveryResponse
	if err := json.Unmarshal(response, &result); err != nil {
		return retryDelivery("invalid_response", "Slack returned an invalid response.", 0), nil
	}
	if !result.OK {
		if transientSlackError(result.Error) {
			return retryDelivery(safeSlackError(result.Error), "Slack asked the Skill to retry the message.", retryAfter), nil
		}
		return failedDelivery(safeSlackError(result.Error), "Slack rejected the message."), nil
	}
	if strings.TrimSpace(result.Timestamp) == "" {
		return retryDelivery("missing_acknowledgement", "Slack did not acknowledge the message.", 0), nil
	}
	return map[string]interface{}{
		"outcome": "delivered", "providerMessageId": result.Timestamp,
		"summary": "Slack accepted the message.",
	}, nil
}

func slackApprovalBlocks(approval map[string]interface{}, destinationID string) ([]map[string]interface{}, error) {
	encoded, err := json.Marshal(approval)
	if err != nil {
		return nil, err
	}
	var reviewed struct {
		ID               string                 `json:"id"`
		Revision         int64                  `json:"revision"`
		ActionCallID     string                 `json:"actionCallId"`
		InvocationDigest string                 `json:"invocationDigest"`
		Risk             string                 `json:"risk"`
		Summary          string                 `json:"summary"`
		PolicyReason     string                 `json:"policyReason"`
		ProposedAction   map[string]interface{} `json:"proposedAction"`
		ExpiresAt        time.Time              `json:"expiresAt"`
		Status           string                 `json:"status"`
		ActionStatus     string                 `json:"actionStatus"`
		ActionError      string                 `json:"actionError"`
		DecisionBy       map[string]string      `json:"decisionBy"`
		DecisionReason   string                 `json:"decisionReason"`
		ProviderApprover string                 `json:"providerApproverId"`
		DecidedAt        *time.Time             `json:"decidedAt"`
	}
	if json.Unmarshal(encoded, &reviewed) != nil || reviewed.ID == "" || reviewed.Revision < 1 || reviewed.ActionCallID == "" {
		return nil, errors.New("invalid approval")
	}
	if reviewed.Status == "" {
		reviewed.Status = "pending"
	}
	if reviewed.ExpiresAt.IsZero() || (reviewed.Status == "pending" && reviewed.InvocationDigest == "") {
		return nil, errors.New("invalid approval expiry")
	}
	value, _ := json.Marshal(slackApprovalValue{ApprovalID: reviewed.ID, ApprovalRevision: reviewed.Revision, ActionCallID: reviewed.ActionCallID, InvocationDigest: reviewed.InvocationDigest, ExpiresAt: reviewed.ExpiresAt.UTC(), DestinationID: strings.TrimSpace(destinationID)})
	preview, _ := json.MarshalIndent(reviewed.ProposedAction, "", "  ")
	previewText := string(preview)
	if len(previewText) > 1800 {
		previewText = previewText[:1800] + "…"
	}
	header, progress := slackApprovalState(reviewed.Status, reviewed.ActionStatus, reviewed.DecisionReason)
	contextText := slackApprovalContext(reviewed.ID, reviewed.Risk, reviewed.ExpiresAt, reviewed.DecidedAt, reviewed.ProviderApprover, reviewed.DecisionBy)
	detail := "*" + reviewed.Summary + "*"
	if strings.TrimSpace(reviewed.PolicyReason) != "" {
		detail += "\n" + reviewed.PolicyReason
	}
	if progress != "" {
		detail += "\n\n" + progress
	}
	if strings.TrimSpace(reviewed.ActionError) != "" && header == "Proposal failed" {
		detail += "\n" + reviewed.ActionError
	} else if strings.TrimSpace(reviewed.DecisionReason) != "" && reviewed.Status != "approved" {
		detail += "\n" + reviewed.DecisionReason
	}
	blocks := []map[string]interface{}{
		{"type": "header", "text": map[string]interface{}{"type": "plain_text", "text": header}},
		{"type": "section", "text": map[string]interface{}{"type": "mrkdwn", "text": detail}},
		{"type": "section", "text": map[string]interface{}{"type": "mrkdwn", "text": "```" + previewText + "```"}},
		{"type": "context", "elements": []map[string]interface{}{{"type": "mrkdwn", "text": contextText}}},
	}
	if reviewed.Status == "pending" {
		blocks = append(blocks, map[string]interface{}{"type": "actions", "block_id": "openseal_approval_actions", "elements": []map[string]interface{}{
			{"type": "button", "action_id": "openseal_approval_approve", "text": map[string]interface{}{"type": "plain_text", "text": "Approve"}, "style": "primary", "value": string(value)},
			{"type": "button", "action_id": "openseal_approval_reject", "text": map[string]interface{}{"type": "plain_text", "text": "Reject"}, "style": "danger", "value": string(value)},
			{"type": "button", "action_id": "openseal_approval_request_changes", "text": map[string]interface{}{"type": "plain_text", "text": "Request changes"}, "value": string(value)},
		}})
	}
	return blocks, nil
}

func slackApprovalState(status, actionStatus, decisionReason string) (string, string) {
	switch status {
	case "pending":
		return "Approval required", ""
	case "rejected":
		if strings.Contains(strings.ToLower(decisionReason), "changes requested") {
			return "Changes requested", ":pencil2: Revise the proposal before trying again."
		}
		return "Proposal rejected", ":no_entry: This proposal will not run."
	case "expired":
		return "Approval expired", ":hourglass_flowing_sand: This proposal can no longer be approved."
	case "canceled":
		return "Proposal canceled", ":no_entry_sign: This proposal was canceled."
	case "approved":
		switch actionStatus {
		case "succeeded":
			return "Proposal completed", ":white_check_mark: The approved action completed."
		case "failed", "denied":
			return "Proposal failed", ":x: The approved action did not complete."
		case "canceled", "compensated":
			return "Proposal canceled", ":no_entry_sign: The approved action was canceled."
		default:
			return "Proposal approved", ":white_check_mark: *Going ahead…*"
		}
	default:
		return "Approval updated", ""
	}
}

func slackApprovalContext(
	approvalID, risk string,
	expiresAt time.Time,
	decidedAt *time.Time,
	providerApprover string,
	decisionBy map[string]string,
) string {
	if decidedAt == nil {
		expiresUnix := expiresAt.UTC().Unix()
		return fmt.Sprintf("Risk: %s · Approval: %s · Expires <!date^%d^{relative}|soon> · <!date^%d^{date_short_pretty} at {time}|%s>",
			risk, approvalID, expiresUnix, expiresUnix, expiresAt.UTC().Format("2006-01-02 15:04 UTC"))
	}
	actor := strings.TrimSpace(providerApprover)
	if actor != "" {
		actor = "<@" + actor + ">"
	} else if strings.TrimSpace(decisionBy["id"]) != "" {
		actor = decisionBy["id"]
	} else {
		actor = "an authorized approver"
	}
	decidedUnix := decidedAt.UTC().Unix()
	return fmt.Sprintf("Risk: %s · Approval: %s · Decided by %s <!date^%d^{relative}|recently>", risk, approvalID, actor, decidedUnix)
}

func (a *slackAdapter) lookup(ctx context.Context, token string, envelope *adapterEnvelope) (map[string]interface{}, error) {
	path := "/conversations.history"
	query := url.Values{"channel": {envelope.Endpoint.Address}, "limit": {"100"}, "inclusive": {"true"}}
	if threadID := strings.TrimSpace(envelope.Delivery.ExternalThreadID); threadID != "" {
		path = "/conversations.replies"
		query.Set("ts", threadID)
	}
	response, status, _, err := a.slackJSON(ctx, token, http.MethodGet, path, query, nil)
	if err != nil || status < 200 || status >= 300 {
		return map[string]interface{}{"status": "unknown"}, nil
	}
	var result slackDeliveryResponse
	if json.Unmarshal(response, &result) != nil || !result.OK {
		return map[string]interface{}{"status": "unknown"}, nil
	}
	for _, message := range result.Messages {
		if message.Metadata.EventType == "openseal_conversation_delivery" &&
			message.Metadata.EventPayload["delivery_id"] == envelope.Delivery.ID &&
			strings.TrimSpace(message.Timestamp) != "" {
			return map[string]interface{}{"status": "found", "providerMessageId": message.Timestamp}, nil
		}
	}
	return map[string]interface{}{"status": "not_found"}, nil
}

func (a *slackAdapter) slackJSON(
	ctx context.Context,
	token, method, path string,
	query url.Values,
	body interface{},
) ([]byte, int, time.Duration, error) {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, 0, 0, err
		}
		reader = bytes.NewReader(encoded)
	}
	endpoint := a.baseURL + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return nil, 0, 0, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := a.client.Do(request)
	if err != nil {
		return nil, 0, 0, err
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, maxSlackResponseBytes+1))
	if err != nil || len(payload) > maxSlackResponseBytes {
		return nil, response.StatusCode, 0, errors.New("Slack response is invalid")
	}
	retryAfter := time.Duration(0)
	if seconds, parseErr := strconv.Atoi(strings.TrimSpace(response.Header.Get("Retry-After"))); parseErr == nil && seconds > 0 {
		retryAfter = time.Duration(seconds) * time.Second
	}
	return payload, response.StatusCode, retryAfter, nil
}

func retryDelivery(code, summary string, retryAfter time.Duration) map[string]interface{} {
	result := map[string]interface{}{"outcome": "retry", "errorCode": code, "summary": summary}
	if retryAfter > 0 {
		result["retryAfterMs"] = retryAfter.Milliseconds()
	}
	return result
}

func failedDelivery(code, summary string) map[string]interface{} {
	return map[string]interface{}{"outcome": "failed", "errorCode": code, "summary": summary}
}

func transientSlackError(code string) bool {
	switch strings.TrimSpace(code) {
	case "ratelimited", "internal_error", "fatal_error", "request_timeout", "service_unavailable":
		return true
	default:
		return false
	}
}

func safeSlackError(code string) string {
	code = strings.TrimSpace(code)
	if code == "" || len(code) > 128 || strings.ContainsAny(code, "\r\n") {
		return "slack_rejected"
	}
	return code
}
