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
	config := slackAdapterConfig(step.Config, resolver, slackSigningSecretKey, slackConnectionKey)
	defer clearSlackAdapterConfig(config, slackSigningSecretKey, slackConnectionKey)
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

func (a *slackAdapter) callback(ctx context.Context, config map[string]interface{}) (map[string]interface{}, error) {
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
	return a.normalizeSlackApprovalCallback(ctx, envelope, config, a.now().UTC())
}

const (
	slackApprovalChangesCallback = "openseal_approval_request_changes"
	slackApprovalGuidanceBlock   = "openseal_approval_guidance"
	slackApprovalGuidanceAction  = "guidance"
)

type slackApprovalRevisionMetadata struct {
	Reviewed  slackApprovalValue `json:"reviewed"`
	ChannelID string             `json:"channelId"`
	MessageID string             `json:"messageId"`
	ThreadID  string             `json:"threadId,omitempty"`
}

func (a *slackAdapter) normalizeSlackApprovalCallback(ctx context.Context, envelope *callbackAdapterEnvelope, config map[string]interface{}, now time.Time) (map[string]interface{}, error) {
	form, err := url.ParseQuery(string(envelope.Request.Body))
	if err != nil || strings.TrimSpace(form.Get("payload")) == "" {
		return map[string]interface{}{"statusCode": http.StatusBadRequest}, nil
	}
	var payload slackInteraction
	if json.Unmarshal([]byte(form.Get("payload")), &payload) != nil ||
		(payload.Type != "block_actions" && payload.Type != "view_submission") {
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
	if payload.Type == "view_submission" {
		return normalizeSlackApprovalRevision(payload, configuration, now)
	}
	if len(payload.Actions) != 1 {
		return map[string]interface{}{"statusCode": http.StatusBadRequest}, nil
	}
	action := payload.Actions[0]
	decision := strings.TrimPrefix(strings.TrimSpace(action.ActionID), "openseal_approval_")
	if decision != "approve" && decision != "reject" && decision != "request_changes" {
		return map[string]interface{}{"statusCode": http.StatusBadRequest}, nil
	}
	principal, ok := slackApprovalPrincipal(configuration, payload.User.ID)
	if !ok {
		return map[string]interface{}{
			"statusCode": http.StatusForbidden, "contentType": "text/plain",
			"body": []byte("Slack user is not authorized to decide this approval"),
		}, nil
	}
	var reviewed slackApprovalValue
	legacy := json.Unmarshal([]byte(action.Value), &reviewed) != nil || reviewed.ApprovalID == "" ||
		reviewed.ApprovalRevision < 1 || reviewed.ActionCallID == "" || reviewed.InvocationDigest == "" ||
		reviewed.ExpiresAt.IsZero()
	if legacy {
		responseBody, responseErr := slackApprovalDecisionResponse(payload, decision, now, true)
		if responseErr != nil {
			return nil, responseErr
		}
		return map[string]interface{}{
			"statusCode": http.StatusOK, "contentType": "application/json", "body": responseBody,
			"events": []normalizedCallbackEvent{},
		}, nil
	}
	if decision == "request_changes" {
		if !now.Before(reviewed.ExpiresAt) {
			responseBody, responseErr := slackApprovalDecisionResponse(payload, decision, now, true)
			if responseErr != nil {
				return nil, responseErr
			}
			return map[string]interface{}{"statusCode": http.StatusOK, "contentType": "application/json", "body": responseBody, "events": []normalizedCallbackEvent{}}, nil
		}
		token, _ := config[slackConnectionKey].(string)
		if strings.TrimSpace(token) == "" || strings.TrimSpace(payload.TriggerID) == "" {
			return map[string]interface{}{"statusCode": http.StatusServiceUnavailable, "contentType": "text/plain", "body": []byte("Slack approval guidance is unavailable")}, nil
		}
		metadata, marshalErr := json.Marshal(slackApprovalRevisionMetadata{
			Reviewed: reviewed, ChannelID: payload.Channel.ID,
			MessageID: payload.Container.MessageTS, ThreadID: payload.Container.ThreadTS,
		})
		if marshalErr != nil {
			return nil, marshalErr
		}
		modal := map[string]interface{}{
			"type": "modal", "callback_id": slackApprovalChangesCallback,
			"private_metadata": string(metadata),
			"title":            map[string]string{"type": "plain_text", "text": "Request changes"},
			"submit":           map[string]string{"type": "plain_text", "text": "Send guidance"},
			"close":            map[string]string{"type": "plain_text", "text": "Cancel"},
			"blocks": []map[string]interface{}{{
				"type": "input", "block_id": slackApprovalGuidanceBlock,
				"label": map[string]string{"type": "plain_text", "text": "What should change?"},
				"element": map[string]interface{}{
					"type": "plain_text_input", "action_id": slackApprovalGuidanceAction,
					"multiline": true, "min_length": 1,
					"placeholder": map[string]string{"type": "plain_text", "text": "Describe the exact revision you want."},
				},
			}},
		}
		response, status, _, requestErr := a.slackJSON(ctx, strings.TrimSpace(token), http.MethodPost, "/views.open", nil, map[string]interface{}{"trigger_id": payload.TriggerID, "view": modal})
		if requestErr != nil || status < 200 || status >= 300 {
			return map[string]interface{}{"statusCode": http.StatusBadGateway, "contentType": "text/plain", "body": []byte("Slack could not open reviewer guidance")}, nil
		}
		var opened struct {
			OK bool `json:"ok"`
		}
		if json.Unmarshal(response, &opened) != nil || !opened.OK {
			return map[string]interface{}{"statusCode": http.StatusBadGateway, "contentType": "text/plain", "body": []byte("Slack could not open reviewer guidance")}, nil
		}
		return map[string]interface{}{"statusCode": http.StatusOK, "events": []normalizedCallbackEvent{}}, nil
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

func normalizeSlackApprovalRevision(payload slackInteraction, configuration map[string]interface{}, now time.Time) (map[string]interface{}, error) {
	if payload.View.CallbackID != slackApprovalChangesCallback {
		return map[string]interface{}{"statusCode": http.StatusBadRequest}, nil
	}
	principal, ok := slackApprovalPrincipal(configuration, payload.User.ID)
	if !ok {
		return map[string]interface{}{"statusCode": http.StatusForbidden, "contentType": "text/plain", "body": []byte("Slack user is not authorized to decide this approval")}, nil
	}
	var metadata slackApprovalRevisionMetadata
	if json.Unmarshal([]byte(payload.View.PrivateMetadata), &metadata) != nil || metadata.Reviewed.ApprovalID == "" ||
		metadata.Reviewed.ApprovalRevision < 1 || metadata.Reviewed.ActionCallID == "" ||
		metadata.Reviewed.InvocationDigest == "" || metadata.Reviewed.ExpiresAt.IsZero() {
		return map[string]interface{}{"statusCode": http.StatusBadRequest}, nil
	}
	guidance := strings.TrimSpace(payload.View.State.Values[slackApprovalGuidanceBlock][slackApprovalGuidanceAction].Value)
	if guidance == "" {
		body, err := json.Marshal(map[string]interface{}{
			"response_action": "errors", "errors": map[string]string{slackApprovalGuidanceBlock: "Describe what should change."},
		})
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{"statusCode": http.StatusOK, "contentType": "application/json", "body": body, "events": []normalizedCallbackEvent{}}, nil
	}
	if !now.Before(metadata.Reviewed.ExpiresAt) {
		body, err := json.Marshal(map[string]interface{}{
			"response_action": "errors", "errors": map[string]string{slackApprovalGuidanceBlock: "This approval has expired."},
		})
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{"statusCode": http.StatusOK, "contentType": "application/json", "body": body, "events": []normalizedCallbackEvent{}}, nil
	}
	eventID := "slack:approval:" + payload.Team.ID + ":" + payload.View.ID + ":" + payload.User.ID
	event := normalizedCallbackEvent{
		ID: eventID, Type: "approval.decided", Source: "slack", Subject: metadata.Reviewed.ApprovalID, OccurredAt: now,
		Attributes: map[string]interface{}{
			"approvalId": metadata.Reviewed.ApprovalID, "approvalRevision": metadata.Reviewed.ApprovalRevision,
			"actionCallId": metadata.Reviewed.ActionCallID, "invocationDigest": metadata.Reviewed.InvocationDigest,
			"decision": "request_changes", "reason": guidance,
			"principalType": principal["type"], "principalId": principal["id"],
			"providerUserId": payload.User.ID, "expiresAt": metadata.Reviewed.ExpiresAt.UTC(),
		},
		Payload: map[string]interface{}{
			"provider": "slack", "teamId": payload.Team.ID, "channelId": metadata.ChannelID,
			"messageId": metadata.MessageID, "threadId": metadata.ThreadID,
		},
	}
	body, err := json.Marshal(map[string]interface{}{"response_action": "clear"})
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{"statusCode": http.StatusOK, "contentType": "application/json", "body": body, "events": []normalizedCallbackEvent{event}}, nil
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
