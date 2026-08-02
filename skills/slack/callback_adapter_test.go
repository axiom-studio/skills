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
		ApprovalID: "approval-1", ApprovalRevision: 4, ActionCallID: "call-1", InvocationDigest: strings.Repeat("a", 64), ExpiresAt: now.Add(time.Hour),
	})
	payload, _ := json.Marshal(map[string]interface{}{
		"type": "block_actions", "api_app_id": "A123", "action_ts": "1720000000.2",
		"team": map[string]string{"id": "T123"}, "user": map[string]string{"id": "U123", "username": "alice"},
		"channel": map[string]string{"id": "C123"}, "container": map[string]string{"message_ts": "1720000000.1"},
		"message": map[string]interface{}{
			"text": "Review this action",
			"blocks": []map[string]interface{}{
				{"type": "section", "text": map[string]string{"type": "mrkdwn", "text": "*Review this action*"}},
				{"type": "actions", "elements": []map[string]string{{"type": "button", "text": "Approve"}}},
			},
		},
		"actions": []map[string]string{{"action_id": "openseal_approval_approve", "value": string(value), "action_ts": "1720000000.2"}},
	})
	body := []byte(url.Values{"payload": []string{string(payload)}}.Encode())
	config := callbackConfig(now, body, map[string]interface{}{
		// A legacy channel hint must not become a second routing authority. The
		// reviewed callback subscription owns the destination and may move.
		"teamId": "T123", "appId": "A123", "channelId": "C-OLD",
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
	responseBody, ok := output["body"].([]byte)
	if !ok {
		t.Fatalf("response body = %#v", output["body"])
	}
	var response struct {
		ReplaceOriginal bool   `json:"replace_original"`
		Text            string `json:"text"`
		Blocks          []struct {
			Type string `json:"type"`
		} `json:"blocks"`
	}
	if json.Unmarshal(responseBody, &response) != nil || !response.ReplaceOriginal ||
		response.Text != "Approved by <@U123>" || len(response.Blocks) != 2 ||
		response.Blocks[0].Type != "section" || response.Blocks[1].Type != "context" {
		t.Fatalf("decision response = %s", responseBody)
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

func TestSlackCallbackReplacesLateApprovalWithExpiredCard(t *testing.T) {
	now := time.Unix(1_720_000_000, 0).UTC()
	adapter := newSlackAdapter("signing-secret", "", nil)
	adapter.now = func() time.Time { return now }
	value, _ := json.Marshal(slackApprovalValue{
		ApprovalID: "approval-1", ApprovalRevision: 4, ActionCallID: "call-1",
		InvocationDigest: strings.Repeat("a", 64), ExpiresAt: now.Add(-time.Second),
	})
	payload, _ := json.Marshal(map[string]interface{}{
		"type": "block_actions", "api_app_id": "A123", "action_ts": "1720000000.2",
		"team": map[string]string{"id": "T123"}, "user": map[string]string{"id": "U123", "username": "alice"},
		"channel": map[string]string{"id": "C123"}, "container": map[string]string{"message_ts": "1720000000.1"},
		"message": map[string]interface{}{"blocks": []map[string]interface{}{
			{"type": "section", "text": map[string]string{"type": "mrkdwn", "text": "*Review this action*"}},
			{"type": "actions", "elements": []map[string]string{{"type": "button", "text": "Approve"}}},
		}},
		"actions": []map[string]string{{"action_id": "openseal_approval_approve", "value": string(value), "action_ts": "1720000000.2"}},
	})
	body := []byte(url.Values{"payload": []string{string(payload)}}.Encode())
	output, err := adapter.callback(context.Background(), callbackConfig(now, body, map[string]interface{}{
		"teamId": "T123", "appId": "A123", "channelId": "C123",
		"approvalPrincipals": map[string]interface{}{"U123": map[string]interface{}{"type": "role", "id": "operator"}},
	}))
	if err != nil || output["statusCode"] != http.StatusOK {
		t.Fatalf("callback = %#v, %v", output, err)
	}
	responseBody := output["body"].([]byte)
	var response struct {
		Text   string `json:"text"`
		Blocks []struct {
			Type string `json:"type"`
		} `json:"blocks"`
	}
	if json.Unmarshal(responseBody, &response) != nil || response.Text != "Expired by <@U123>" {
		t.Fatalf("expired response = %s", responseBody)
	}
	for _, block := range response.Blocks {
		if block.Type == "actions" {
			t.Fatalf("expired response retained action buttons: %s", responseBody)
		}
	}
	encoded, _ := json.Marshal(output["events"])
	var events []normalizedCallbackEvent
	if json.Unmarshal(encoded, &events) != nil || len(events) != 1 || events[0].Attributes["decision"] != "approve" {
		t.Fatalf("expiry event = %s", encoded)
	}
}

func TestSlackCallbackExpiresLegacyCardWithoutReplayingApproval(t *testing.T) {
	now := time.Unix(1_720_000_000, 0).UTC()
	value, _ := json.Marshal(slackApprovalValue{
		ApprovalID: "approval-legacy", ApprovalRevision: 1, ActionCallID: "call-legacy",
		InvocationDigest: strings.Repeat("b", 64),
	})
	payload, _ := json.Marshal(map[string]interface{}{
		"type": "block_actions", "api_app_id": "A123", "action_ts": "1720000000.2",
		"team": map[string]string{"id": "T123"}, "user": map[string]string{"id": "U123", "username": "alice"},
		"channel": map[string]string{"id": "C123"}, "container": map[string]string{"message_ts": "1720000000.1"},
		"message": map[string]interface{}{"blocks": []map[string]interface{}{
			{"type": "section", "text": map[string]string{"type": "mrkdwn", "text": "*Review this action*"}},
			{"type": "actions", "elements": []map[string]string{{"type": "button", "text": "Approve"}}},
		}},
		"actions": []map[string]string{{"action_id": "openseal_approval_approve", "value": string(value), "action_ts": "1720000000.2"}},
	})
	body := []byte(url.Values{"payload": []string{string(payload)}}.Encode())
	adapter := newSlackAdapter("signing-secret", "", nil)
	adapter.now = func() time.Time { return now }
	output, err := adapter.callback(context.Background(), callbackConfig(now, body, map[string]interface{}{
		"teamId": "T123", "appId": "A123",
		"approvalPrincipals": map[string]interface{}{"U123": map[string]interface{}{"type": "role", "id": "operator"}},
	}))
	if err != nil || output["statusCode"] != http.StatusOK {
		t.Fatalf("legacy callback = %#v, %v", output, err)
	}
	responseBody := output["body"].([]byte)
	if !strings.Contains(string(responseBody), "Expired") {
		t.Fatalf("legacy response = %s", responseBody)
	}
	encoded, _ := json.Marshal(output["events"])
	var events []normalizedCallbackEvent
	if json.Unmarshal(encoded, &events) != nil || len(events) != 0 {
		t.Fatalf("legacy callback replayed events = %s", encoded)
	}
}

func TestSlackCallbackExecutorReadsEphemeralSigningSecretFromBindingChannel(t *testing.T) {
	now := time.Unix(1_720_000_000, 0).UTC()
	value, _ := json.Marshal(slackApprovalValue{
		ApprovalID: "approval-1", ApprovalRevision: 4, ActionCallID: "call-1", InvocationDigest: strings.Repeat("a", 64), ExpiresAt: now.Add(time.Hour),
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
