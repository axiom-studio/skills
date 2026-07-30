package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/axiom-studio/skills.sdk/executor"
)

func TestSlackCallbackVerifiesSignatureAndNormalizesApprovalDecision(t *testing.T) {
	now := time.Unix(1_720_000_000, 0).UTC()
	adapter := newSlackAdapter("signing-secret", "", nil)
	adapter.now = func() time.Time { return now }
	value, _ := json.Marshal(slackApprovalValue{
		ApprovalID: "approval-1", ApprovalRevision: 4, ActionCallID: "call-1", InvocationDigest: strings.Repeat("a", 64),
	})
	payload, _ := json.Marshal(map[string]interface{}{
		"type": "block_actions", "api_app_id": "A123", "action_ts": "1720000000.2",
		"team": map[string]string{"id": "T123"}, "user": map[string]string{"id": "U123", "username": "alice"},
		"channel": map[string]string{"id": "C123"}, "container": map[string]string{"message_ts": "1720000000.1"},
		"actions": []map[string]string{{"action_id": "openseal_approval_approve", "value": string(value), "action_ts": "1720000000.2"}},
	})
	body := []byte(url.Values{"payload": []string{string(payload)}}.Encode())
	config := callbackConfig(now, body, map[string]interface{}{
		"teamId": "T123", "appId": "A123", "channelId": "C123",
		"approvalPrincipals": map[string]interface{}{"U123": map[string]interface{}{"type": "role", "id": "operator"}},
	})

	output, err := adapter.callback(context.Background(), config)

	if err != nil || output["statusCode"] != http.StatusOK {
		t.Fatalf("callback = %#v, %v", output, err)
	}
	encoded, _ := json.Marshal(output["events"])
	var events []normalizedCallbackEvent
	if json.Unmarshal(encoded, &events) != nil || len(events) != 1 {
		t.Fatalf("events = %s", encoded)
	}
	event := events[0]
	if event.Type != "approval.decided" || event.Source != "slack" || event.Subject != "approval-1" ||
		event.Attributes["actionCallId"] != "call-1" || event.Attributes["invocationDigest"] != strings.Repeat("a", 64) ||
		event.Attributes["principalType"] != "role" || event.Attributes["principalId"] != "operator" {
		t.Fatalf("decision = %#v", event)
	}

	config[callbackEnvelopeKey].(map[string]interface{})["registration"].(*callbackRegistration).Configuration["approvalPrincipals"] = map[string]interface{}{}
	denied, err := adapter.callback(context.Background(), config)
	if err != nil || denied["statusCode"] != http.StatusForbidden {
		t.Fatalf("unauthorized = %#v, %v", denied, err)
	}
}

func TestSlackCallbackRejectsUnsignedProviderRequest(t *testing.T) {
	now := time.Unix(1_720_000_000, 0).UTC()
	adapter := newSlackAdapter("signing-secret", "", nil)
	adapter.now = func() time.Time { return now }
	config := callbackConfig(now, []byte("payload=invalid"), nil)
	request := config[callbackEnvelopeKey].(map[string]interface{})["request"].(*callbackPublicRequest)
	request.Headers["X-Slack-Signature"] = []string{"v0=forged"}

	output, err := adapter.callback(context.Background(), config)

	body, bodyOK := output["body"].([]byte)
	if err != nil || output["statusCode"] != http.StatusUnauthorized || output["events"] != nil ||
		!bodyOK || string(body) != "invalid Slack signature" {
		t.Fatalf("unsigned callback = %#v, %v", output, err)
	}
}

func TestSlackCallbackExecutorReadsEphemeralSigningSecretFromBindingChannel(t *testing.T) {
	now := time.Unix(1_720_000_000, 0).UTC()
	value, _ := json.Marshal(slackApprovalValue{
		ApprovalID: "approval-1", ApprovalRevision: 4, ActionCallID: "call-1", InvocationDigest: strings.Repeat("a", 64),
	})
	payload, _ := json.Marshal(map[string]interface{}{
		"type": "block_actions", "api_app_id": "A123", "action_ts": "1720000000.2",
		"team": map[string]string{"id": "T123"}, "user": map[string]string{"id": "U123", "username": "alice"},
		"channel": map[string]string{"id": "C123"}, "container": map[string]string{"message_ts": "1720000000.1"},
		"actions": []map[string]string{{"action_id": "openseal_approval_approve", "value": string(value), "action_ts": "1720000000.2"}},
	})
	body := []byte(url.Values{"payload": []string{string(payload)}}.Encode())
	config := callbackConfig(now, body, map[string]interface{}{
		"teamId": "T123", "appId": "A123", "channelId": "C123",
		"approvalPrincipals": map[string]interface{}{"U123": map[string]interface{}{"type": "role", "id": "operator"}},
	})
	step := &executor.StepDefinition{Config: config}
	adapter := newSlackAdapter("", "", nil)
	adapter.now = func() time.Time { return now }

	result, err := (&slackCallbackExecutor{adapter: adapter}).Execute(t.Context(), step, slackBindingResolver{
		bindings: map[string]interface{}{slackSigningSecretKey: "signing-secret"},
	})

	if err != nil || result.Output["statusCode"] != http.StatusOK {
		t.Fatalf("callback = %#v, %v", result, err)
	}
	if _, leaked := step.Config[slackSigningSecretKey]; leaked {
		t.Fatal("ephemeral Slack signing secret leaked into durable step config")
	}
}

func callbackConfig(timestamp time.Time, body []byte, configuration map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{
		callbackEnvelopeKey: map[string]interface{}{
			"operation":    "callback",
			"registration": &callbackRegistration{ID: "slack-approval", Provider: "slack", Configuration: configuration},
			"request": &callbackPublicRequest{
				Method: http.MethodPost,
				Headers: map[string][]string{
					"Content-Type":              {"application/x-www-form-urlencoded"},
					"X-Slack-Request-Timestamp": {strconv.FormatInt(timestamp.Unix(), 10)},
					"X-Slack-Signature":         {signedSlackRequest("signing-secret", timestamp.Unix(), body)},
				},
				Body: body,
			},
		},
	}
}
