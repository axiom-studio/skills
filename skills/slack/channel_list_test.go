package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/axiom-studio/skills.sdk/executor"
)

func TestSlackChannelListProjectsSearchableCursorPage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer xoxb-authorized" {
			t.Fatalf("request = authorization %q query %q", request.Header.Get("Authorization"), request.URL.RawQuery)
		}
		if request.URL.Path == "/auth.test" {
			_ = json.NewEncoder(response).Encode(map[string]interface{}{
				"ok": true, "team_id": "T-product", "team": "Product workspace",
			})
			return
		}
		if request.URL.Query().Get("cursor") != "cursor-1" || request.URL.Query().Get("limit") != "50" {
			t.Fatalf("request query = %q", request.URL.RawQuery)
		}
		_ = json.NewEncoder(response).Encode(map[string]interface{}{
			"ok": true,
			"channels": []map[string]interface{}{
				{"id": "C1", "name": "engineering", "purpose": map[string]interface{}{"value": "Build the product"}},
				{"id": "C2", "name": "product-feedback", "purpose": map[string]interface{}{"value": "Customer feedback"}},
			},
			"response_metadata": map[string]interface{}{"next_cursor": "cursor-2"},
		})
	}))
	defer server.Close()

	previousBaseURL := slackBaseURLOverride
	slackBaseURLOverride = server.URL
	t.Cleanup(func() { slackBaseURLOverride = previousBaseURL })
	result, err := (&SlackChannelListExecutor{}).Execute(context.Background(), &executor.StepDefinition{Config: map[string]interface{}{
		"cursor": "cursor-1", "limit": 50, "query": "feedback",
	}}, slackBindingResolver{bindings: map[string]interface{}{slackBotTokenCredential: "xoxb-authorized"}})
	if err != nil {
		t.Fatal(err)
	}
	channels, ok := result.Output["channels"].([]map[string]interface{})
	connection, connectionOK := result.Output["connection"].(map[string]interface{})
	if !ok || len(channels) != 1 || channels[0]["id"] != "C2" || channels[0]["name"] != "product-feedback" ||
		channels[0]["description"] != "Customer feedback" || result.Output["nextCursor"] != "cursor-2" ||
		!connectionOK || connection["installationId"] != "T-product" || connection["displayName"] != "Product workspace" {
		t.Fatalf("result = %#v", result.Output)
	}
}

func TestSlackSendMessageUsesGovernedBindingWithoutConfigSecret(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer xoxb-governed" {
			t.Fatalf("authorization = %q", request.Header.Get("Authorization"))
		}
		if request.URL.Path != "/chat.postMessage" {
			t.Fatalf("path = %q", request.URL.Path)
		}
		if err := request.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if request.Form.Get("channel") != "C-approvals" || request.Form.Get("text") != "Review this action" {
			t.Fatalf("form = %#v", request.Form)
		}
		_ = json.NewEncoder(response).Encode(map[string]interface{}{"ok": true, "ts": "123.456"})
	}))
	defer server.Close()

	previousBaseURL := slackBaseURLOverride
	slackBaseURLOverride = server.URL
	t.Cleanup(func() { slackBaseURLOverride = previousBaseURL })
	config := map[string]interface{}{"channel": "C-approvals", "message": "Review this action"}
	result, err := (&SlackSendMessageExecutor{}).Execute(context.Background(), &executor.StepDefinition{Config: config},
		slackBindingResolver{bindings: map[string]interface{}{slackBotTokenCredential: "xoxb-governed"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Output["success"] != true || result.Output["timestamp"] != "123.456" {
		t.Fatalf("result = %#v", result.Output)
	}
	if _, leaked := config[slackBotTokenCredential]; leaked {
		t.Fatal("governed credential leaked into ordinary action config")
	}
}
