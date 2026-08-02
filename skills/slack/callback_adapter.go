package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/axiom-studio/skills.sdk/executor"
)

const (
	slackCallbackNodeType = "slack.callback.ingress"
	callbackEnvelopeKey   = "_opensealCallbackRequest"
)

type slackCallbackExecutor struct{ adapter *slackAdapter }

func (e *slackCallbackExecutor) Type() string { return slackCallbackNodeType }

func (e *slackCallbackExecutor) Execute(
	ctx context.Context,
	step *executor.StepDefinition,
	resolver executor.TemplateResolver,
) (*executor.StepResult, error) {
	config := slackAdapterConfig(step.Config, resolver, slackSigningSecretKey)
	defer clearSlackAdapterConfig(config, slackSigningSecretKey)
	output, err := e.adapter.callback(ctx, config)
	if err != nil {
		return nil, err
	}
	return &executor.StepResult{Output: output}, nil
}

type callbackAdapterEnvelope struct {
	Operation    string                 `json:"operation"`
	Registration *callbackRegistration  `json:"registration"`
	Request      *callbackPublicRequest `json:"request"`
}

type callbackRegistration struct {
	ID            string                 `json:"id"`
	Provider      string                 `json:"provider"`
	Configuration map[string]interface{} `json:"configuration,omitempty"`
}

type callbackPublicRequest struct {
	Method  string              `json:"method"`
	Headers map[string][]string `json:"headers,omitempty"`
	Body    []byte              `json:"body"`
}

type normalizedCallbackEvent struct {
	ID         string                 `json:"id"`
	Type       string                 `json:"type"`
	Source     string                 `json:"source"`
	Subject    string                 `json:"subject,omitempty"`
	OccurredAt time.Time              `json:"occurredAt"`
	Attributes map[string]interface{} `json:"attributes,omitempty"`
	Payload    map[string]interface{} `json:"payload,omitempty"`
}

func decodeCallbackEnvelope(config map[string]interface{}) (*callbackAdapterEnvelope, error) {
	raw, ok := config[callbackEnvelopeKey]
	if !ok {
		return nil, errors.New("callback adapter request is required")
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil, errors.New("callback adapter request is invalid")
	}
	var envelope callbackAdapterEnvelope
	if json.Unmarshal(encoded, &envelope) != nil || envelope.Operation != "callback" ||
		envelope.Registration == nil || envelope.Request == nil || envelope.Request.Method != http.MethodPost ||
		envelope.Registration.Provider != "slack" {
		return nil, errors.New("callback adapter request is invalid")
	}
	return &envelope, nil
}

func (a *slackAdapter) callback(_ context.Context, config map[string]interface{}) (map[string]interface{}, error) {
	envelope, err := decodeCallbackEnvelope(config)
	if err != nil {
		return nil, err
	}
	signingSecret := a.signingSecret
	if resolved, ok := config[slackSigningSecretKey].(string); ok && strings.TrimSpace(resolved) != "" {
		signingSecret = strings.TrimSpace(resolved)
	}
	request := &conversationIngressRequest{
		Method: envelope.Request.Method, Headers: envelope.Request.Headers, Body: envelope.Request.Body,
	}
	if !a.verifySlackRequest(request, signingSecret) {
		return map[string]interface{}{
			"statusCode": http.StatusUnauthorized, "contentType": "text/plain", "body": []byte("invalid Slack signature"),
		}, nil
	}
	if !strings.Contains(strings.ToLower(firstHeader(envelope.Request.Headers, "Content-Type")), "application/x-www-form-urlencoded") {
		return map[string]interface{}{"statusCode": http.StatusUnsupportedMediaType}, nil
	}
	return normalizeSlackApprovalCallback(envelope, a.now().UTC())
}

func normalizeSlackApprovalCallback(envelope *callbackAdapterEnvelope, now time.Time) (map[string]interface{}, error) {
	form, err := url.ParseQuery(string(envelope.Request.Body))
	if err != nil || strings.TrimSpace(form.Get("payload")) == "" {
		return map[string]interface{}{"statusCode": http.StatusBadRequest}, nil
	}
	var payload slackInteraction
	if json.Unmarshal([]byte(form.Get("payload")), &payload) != nil || payload.Type != "block_actions" || len(payload.Actions) != 1 {
		return map[string]interface{}{"statusCode": http.StatusBadRequest}, nil
	}
	configuration := envelope.Registration.Configuration
	if expected := stringConfiguration(configuration, "teamId"); expected != "" && expected != payload.Team.ID {
		return map[string]interface{}{
			"statusCode": http.StatusConflict, "contentType": "text/plain",
			"body": []byte("Slack workspace does not match the reviewed callback installation"),
		}, nil
	}
	if expected := stringConfiguration(configuration, "appId"); expected != "" && expected != payload.APIAppID {
		return map[string]interface{}{
			"statusCode": http.StatusConflict, "contentType": "text/plain",
			"body": []byte("Slack app does not match the reviewed callback installation"),
		}, nil
	}
	action := payload.Actions[0]
	decision := strings.TrimPrefix(strings.TrimSpace(action.ActionID), "openseal_approval_")
	if decision != "approve" && decision != "reject" && decision != "request_changes" {
		return map[string]interface{}{"statusCode": http.StatusBadRequest}, nil
	}
	var reviewed slackApprovalValue
	if json.Unmarshal([]byte(action.Value), &reviewed) != nil || reviewed.ApprovalID == "" ||
		reviewed.ApprovalRevision < 1 || reviewed.ActionCallID == "" || reviewed.InvocationDigest == "" || reviewed.ExpiresAt.IsZero() {
		return map[string]interface{}{"statusCode": http.StatusBadRequest}, nil
	}
	principal, ok := slackApprovalPrincipal(configuration, payload.User.ID)
	if !ok {
		return map[string]interface{}{
			"statusCode": http.StatusForbidden, "contentType": "text/plain",
			"body": []byte("Slack user is not authorized to decide this approval"),
		}, nil
	}
	eventID := "slack:approval:" + payload.Team.ID + ":" + strings.TrimSpace(action.ActionTS) + ":" + payload.User.ID
	occurredAt := slackTimestamp(firstNonEmpty(action.ActionTS, payload.ActionTS))
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}
	event := normalizedCallbackEvent{
		ID: eventID, Type: "approval.decided", Source: "slack", Subject: reviewed.ApprovalID, OccurredAt: occurredAt,
		Attributes: map[string]interface{}{
			"approvalId": reviewed.ApprovalID, "approvalRevision": reviewed.ApprovalRevision,
			"actionCallId": reviewed.ActionCallID, "invocationDigest": reviewed.InvocationDigest,
			"decision": decision, "principalType": principal["type"], "principalId": principal["id"],
			"providerUserId": payload.User.ID, "expiresAt": reviewed.ExpiresAt.UTC(),
		},
		Payload: map[string]interface{}{
			"provider": "slack", "teamId": payload.Team.ID, "channelId": payload.Channel.ID,
			"messageId": payload.Container.MessageTS, "threadId": payload.Container.ThreadTS,
		},
	}
	responseBody, err := slackApprovalDecisionResponse(payload, decision, occurredAt, !now.Before(reviewed.ExpiresAt))
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{
		"statusCode": http.StatusOK, "contentType": "application/json", "body": responseBody,
		"events": []normalizedCallbackEvent{event},
	}, nil
}

func slackApprovalDecisionResponse(payload slackInteraction, decision string, occurredAt time.Time, expired bool) ([]byte, error) {
	label := map[string]string{
		"approve":         "Approved",
		"reject":          "Rejected",
		"request_changes": "Changes requested",
	}[decision]
	if expired {
		label = "Expired"
	}
	actor := strings.TrimSpace(payload.User.Username)
	if payload.User.ID != "" {
		actor = "<@" + payload.User.ID + ">"
	}
	if actor == "" {
		actor = "an authorized approver"
	}
	text := fmt.Sprintf("%s by %s", label, actor)

	blocks := make([]json.RawMessage, 0, len(payload.Message.Blocks)+1)
	for _, block := range payload.Message.Blocks {
		var header struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(block, &header) != nil || header.Type == "actions" {
			continue
		}
		blocks = append(blocks, block)
	}
	if len(blocks) == 0 {
		section, err := json.Marshal(map[string]interface{}{
			"type": "section",
			"text": map[string]string{"type": "mrkdwn", "text": "*" + label + "*"},
		})
		if err != nil {
			return nil, err
		}
		blocks = append(blocks, section)
	}
	contextBlock, err := json.Marshal(map[string]interface{}{
		"type": "context",
		"elements": []map[string]string{{
			"type": "mrkdwn",
			"text": fmt.Sprintf("*%s* by %s · %s", label, actor, occurredAt.UTC().Format(time.RFC3339)),
		}},
	})
	if err != nil {
		return nil, err
	}
	blocks = append(blocks, contextBlock)
	return json.Marshal(map[string]interface{}{
		"replace_original": true,
		"text":             text,
		"blocks":           blocks,
	})
}
