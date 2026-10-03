package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/axiom-studio/skills.sdk/executor"
	"gopkg.in/yaml.v3"
)

func TestSlackSearchMessagesKeepsChannelScopeAndContinuesEmptyPages(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests++
		if request.URL.Path != "/conversations.history" || request.Method != http.MethodGet ||
			request.Header.Get("Authorization") != "Bearer xoxb-search" || request.URL.Query().Get("channel") != "C123" ||
			request.URL.Query().Get("limit") != "50" || request.URL.Query().Get("oldest") != "100.000001" {
			t.Errorf("unexpected scoped read: path=%s query=%s", request.URL.Path, request.URL.RawQuery)
		}
		if request.URL.Query().Has("query") || request.URL.Query().Has("token") {
			t.Error("search text or token was forwarded as Slack API query authority")
		}
		if request.URL.Query().Get("cursor") == "" {
			_ = json.NewEncoder(response).Encode(map[string]interface{}{
				"ok": true, "messages": []SlackMessage{{Text: "Unrelated update", Timestamp: "150.000001"}},
				"has_more": true, "response_metadata": map[string]interface{}{"next_cursor": "next-page"},
			})
			return
		}
		if request.URL.Query().Get("cursor") != "next-page" {
			t.Error("did not preserve the opaque pagination cursor")
		}
		_ = json.NewEncoder(response).Encode(map[string]interface{}{
			"ok": true, "messages": []SlackMessage{
				{Text: "The PROJECT decision is ready", Timestamp: "120.000001", User: "U123", ThreadTs: "120.000001", ReplyCount: 2},
				{Text: "Project status only", Timestamp: "110.000001"},
			}, "is_limited": true,
		})
	}))
	defer server.Close()
	previousBaseURL := slackBaseURLOverride
	slackBaseURLOverride = server.URL
	t.Cleanup(func() { slackBaseURLOverride = previousBaseURL })
	bindings := slackBindingResolver{bindings: map[string]interface{}{slackBotTokenCredential: "xoxb-search"}}
	config := map[string]interface{}{"channel": "C123", "query": "project DECISION", "limit": 50, "oldest": "100.000001"}
	first, err := (&SlackSearchMessagesExecutor{}).Execute(t.Context(), &executor.StepDefinition{Config: config}, bindings)
	if err != nil {
		t.Fatal(err)
	}
	if first.Output["count"] != 0 || first.Output["scannedCount"] != 1 || first.Output["hasMore"] != true || first.Output["nextCursor"] != "next-page" {
		t.Fatalf("empty bounded page lost continuation: %#v", first.Output)
	}
	config["cursor"] = first.Output["nextCursor"]
	second, err := (&SlackSearchMessagesExecutor{}).Execute(t.Context(), &executor.StepDefinition{Config: config}, bindings)
	if err != nil {
		t.Fatal(err)
	}
	messages := second.Output["messages"].([]map[string]interface{})
	if len(messages) != 1 || messages[0]["timestamp"] != "120.000001" || second.Output["scannedCount"] != 2 ||
		second.Output["hasMore"] != false || second.Output["coverage"] != "channel_history_page" ||
		second.Output["includesThreadReplies"] != false || second.Output["providerHistoryLimited"] != true || requests != 2 {
		t.Fatalf("bounded search results = %#v (requests=%d)", second.Output, requests)
	}
	serialized, _ := json.Marshal(second.Output)
	if strings.Contains(string(serialized), "xoxb-search") {
		t.Fatal("search output leaked a connection credential")
	}
}

func TestSlackSearchModifiersAreLiteralAndCannotWidenScope(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/conversations.history" || request.URL.Query().Get("channel") != "D123" || request.URL.Query().Has("query") {
			t.Error("search modifiers changed the authorized channel or API operation")
		}
		_ = json.NewEncoder(response).Encode(map[string]interface{}{
			"ok": true, "messages": []SlackMessage{
				{Text: "Secret from elsewhere", Timestamp: "2.000001"},
				{Text: "I wrote in:COTHER is:dm", Timestamp: "1.000001"},
			}, "has_more": true,
		})
	}))
	defer server.Close()
	previousBaseURL := slackBaseURLOverride
	slackBaseURLOverride = server.URL
	t.Cleanup(func() { slackBaseURLOverride = previousBaseURL })
	result, err := (&SlackSearchMessagesExecutor{}).Execute(t.Context(), &executor.StepDefinition{Config: map[string]interface{}{
		"channel": "D123", "query": "in:COTHER is:dm",
	}}, slackBindingResolver{bindings: map[string]interface{}{slackBotTokenCredential: "xoxb-search"}})
	if err != nil {
		t.Fatal(err)
	}
	messages := result.Output["messages"].([]map[string]interface{})
	if len(messages) != 1 || messages[0]["timestamp"] != "1.000001" || result.Output["nextLatest"] != "1.000001" {
		t.Fatalf("literal modifier search = %#v", result.Output)
	}
}

func TestSlackReadAndSearchUseExactThreadAndProviderCursor(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/conversations.replies" || request.URL.Query().Get("channel") != "C123" ||
			request.URL.Query().Get("ts") != "100.000001" || request.URL.Query().Get("cursor") != "thread-page" {
			t.Errorf("unexpected thread read: %s?%s", request.URL.Path, request.URL.RawQuery)
		}
		_ = json.NewEncoder(response).Encode(map[string]interface{}{
			"ok": true, "messages": []SlackMessage{
				{Text: "The root list has vacation photos", Timestamp: "100.000001", ReplyCount: 1},
				{Text: "Can you see the list?", Timestamp: "101.000001", ThreadTs: "100.000001"},
			}, "has_more": true, "response_metadata": map[string]interface{}{"next_cursor": "thread-next"},
		})
	}))
	defer server.Close()
	previousBaseURL := slackBaseURLOverride
	slackBaseURLOverride = server.URL
	t.Cleanup(func() { slackBaseURLOverride = previousBaseURL })
	config := map[string]interface{}{"channel": "C123", "threadTs": "100.000001", "cursor": "thread-page", "limit": 10, "query": "vacation"}
	bindings := slackBindingResolver{bindings: map[string]interface{}{slackBotTokenCredential: "xoxb-search"}}
	read, err := (&SlackReadMessagesExecutor{}).Execute(t.Context(), &executor.StepDefinition{Config: config}, bindings)
	if err != nil {
		t.Fatal(err)
	}
	if read.Output["count"] != 2 || read.Output["nextCursor"] != "thread-next" || read.Output["nextLatest"] != "" {
		t.Fatalf("thread read = %#v", read.Output)
	}
	search, err := (&SlackSearchMessagesExecutor{}).Execute(t.Context(), &executor.StepDefinition{Config: config}, bindings)
	if err != nil {
		t.Fatal(err)
	}
	if search.Output["count"] != 1 || search.Output["coverage"] != "thread_history_page" || search.Output["includesThreadReplies"] != true ||
		search.Output["nextCursor"] != "thread-next" || search.Output["nextLatest"] != "" {
		t.Fatalf("thread search = %#v", search.Output)
	}
}

func TestSlackSearchRejectsInvalidScopeAndPropagatesMissingAccess(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests++
		_ = json.NewEncoder(response).Encode(map[string]interface{}{"ok": false, "error": "missing_scope"})
	}))
	defer server.Close()
	previousBaseURL := slackBaseURLOverride
	slackBaseURLOverride = server.URL
	t.Cleanup(func() { slackBaseURLOverride = previousBaseURL })
	bindings := slackBindingResolver{bindings: map[string]interface{}{slackBotTokenCredential: "xoxb-search"}}
	for _, config := range []map[string]interface{}{
		{"channel": "U123", "query": "project"},
		{"channel": "C123,D123", "query": "project"},
		{"channel": "C123", "query": " "},
		{"channel": "C123", "query": strings.Repeat("x", 513)},
		{"channel": "C123", "query": "project", "limit": 201},
		{"channel": "C123", "query": "project", "threadTs": "../other"},
		{"channel": "C123", "query": "project", "oldest": "invalid"},
	} {
		if result, err := (&SlackSearchMessagesExecutor{}).Execute(t.Context(), &executor.StepDefinition{Config: config}, bindings); err == nil || result != nil {
			t.Fatalf("invalid search accepted: %#v", config)
		}
	}
	if requests != 0 {
		t.Fatalf("invalid scope reached Slack: %d requests", requests)
	}
	result, err := (&SlackSearchMessagesExecutor{}).Execute(t.Context(), &executor.StepDefinition{Config: map[string]interface{}{
		"channel": "C123", "query": "project",
	}}, bindings)
	if result != nil || err == nil || !strings.Contains(err.Error(), "missing_scope") || requests != 1 {
		t.Fatalf("missing access was misreported as empty results: result=%#v error=%v requests=%d", result, err, requests)
	}
}

func TestSlackSearchManifestUsesReadAuthorityAndHistoryFeature(t *testing.T) {
	data, err := os.ReadFile("skill.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Definition struct {
			Version string `yaml:"version"`
			Actions map[string]struct {
				Permissions []string             `yaml:"permissions"`
				Risk        string               `yaml:"risk"`
				SideEffect  string               `yaml:"sideEffect"`
				Credentials []manifestCredential `yaml:"credentials"`
			} `yaml:"actions"`
			ConversationAdapters map[string]struct {
				Features []string `yaml:"features"`
			} `yaml:"conversationAdapters"`
			Prompt struct {
				AllowedTools []string `yaml:"allowedTools"`
			} `yaml:"prompt"`
		} `yaml:"definition"`
	}
	if err := yaml.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	identity, err := slackRuntimeIdentityFromManifest(slackSkillManifest)
	if err != nil {
		t.Fatal(err)
	}
	search, ok := manifest.Definition.Actions["slack-search-messages"]
	if !ok || !reflect.DeepEqual(search.Permissions, []string{"slack:read"}) || search.Risk != "read" || search.SideEffect != "read" ||
		len(search.Credentials) != 1 || search.Credentials[0].Name != slackBotTokenCredential || manifest.Definition.Version != identity.Version {
		t.Fatalf("invalid search authority: %#v version=%s", search, manifest.Definition.Version)
	}
	if !containsSlackTestString(manifest.Definition.ConversationAdapters["conversations"].Features, "context_history") ||
		!containsSlackTestString(manifest.Definition.Prompt.AllowedTools, "slack-search-messages") {
		t.Fatal("history context or search is missing from the agent's advertised capabilities")
	}
}

func containsSlackTestString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
