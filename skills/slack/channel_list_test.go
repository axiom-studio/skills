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
		if request.Header.Get("Authorization") != "Bearer xoxb-authorized" ||
			request.URL.Query().Get("cursor") != "cursor-1" || request.URL.Query().Get("limit") != "50" {
			t.Fatalf("request = authorization %q query %q", request.Header.Get("Authorization"), request.URL.RawQuery)
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
		slackBotTokenCredential: "xoxb-authorized", "cursor": "cursor-1", "limit": 50, "query": "feedback",
	}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	channels, ok := result.Output["channels"].([]map[string]interface{})
	if !ok || len(channels) != 1 || channels[0]["id"] != "C2" || channels[0]["name"] != "product-feedback" ||
		channels[0]["description"] != "Customer feedback" || result.Output["nextCursor"] != "cursor-2" {
		t.Fatalf("result = %#v", result.Output)
	}
}
