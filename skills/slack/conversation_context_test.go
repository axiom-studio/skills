package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/axiom-studio/skills.sdk/executor"
)

func slackContextConfig() map[string]interface{} {
	return map[string]interface{}{
		slackConnectionKey: "xoxb-context-token",
		adapterEnvelopeKey: map[string]interface{}{
			"operation": "context",
			"endpoint":  &conversationEndpoint{ID: "endpoint", Provider: "slack", Address: "C123"},
			"event": &normalizedConversationEvent{
				ExternalConversationID: "C123", ExternalThreadID: "1720000000.000001",
				ExternalMessageID: "1720000100.000001",
			},
		},
	}
}

func decodeSlackContext(t *testing.T, output map[string]interface{}) []slackContextMessage {
	t.Helper()
	encoded, err := json.Marshal(output["messages"])
	if err != nil {
		t.Fatal(err)
	}
	var messages []slackContextMessage
	if err := json.Unmarshal(encoded, &messages); err != nil {
		t.Fatal(err)
	}
	return messages
}

func TestSlackContextReadsOnlyEarlierMessagesFromTheOriginatingThread(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		calls++
		query := request.URL.Query()
		if request.Method != http.MethodGet || request.URL.Path != "/conversations.replies" ||
			request.Header.Get("Authorization") != "Bearer xoxb-context-token" ||
			query.Get("channel") != "C123" || query.Get("ts") != "1720000000.000001" ||
			query.Get("latest") != "1720000100.000001" || query.Get("inclusive") != "false" {
			t.Errorf("unexpected context request: %s %s", request.Method, request.URL)
		}
		_, _ = response.Write([]byte(`{"ok":true,"messages":[
			{"type":"message","ts":"1720000000.000001","user":"U1","text":"*The list*"},
			{"type":"message","ts":"1720000001.000001","thread_ts":"1720000000.000001","bot_id":"B1","text":"A reply"},
			{"ts":"1720000002.000001","thread_ts":"other-thread","user":"U2","text":"Wrong thread"},
			{"ts":"1720000003.000001","thread_ts":"1720000000.000001","channel":"C-other","user":"U2","text":"Wrong channel"},
			{"ts":"1720000004.000001","user":"U2","text":"Unthreaded channel message"},
			{"ts":"1720000100.000001","thread_ts":"1720000000.000001","user":"U2","text":"Current trigger"},
			{"ts":"1720000101.000001","thread_ts":"1720000000.000001","user":"U2","text":"Future message"},
			{"ts":"invalid","thread_ts":"1720000000.000001","user":"U2","text":"Malformed timestamp"},
			{"ts":"1720000005.000001","thread_ts":"1720000000.000001","subtype":"channel_join","user":"U2","text":"A notification"}
		]}`))
	}))
	defer server.Close()
	output, err := newSlackAdapter("", server.URL, server.Client()).delivery(t.Context(), slackContextConfig())
	messages := decodeSlackContext(t, output)
	if err != nil || output["status"] != "complete" || calls != 1 || len(messages) != 2 {
		t.Fatalf("context = %#v, %v; calls = %d", output, err, calls)
	}
	if messages[0].Text != "**The list**" || messages[0].ExternalParticipantID != "U1" ||
		messages[1].ExternalParticipantID != "B1" || messages[1].ExternalMessageID != "1720000001.000001" {
		t.Fatalf("context content = %#v", messages)
	}
	for _, message := range messages {
		if message.ExternalConversationID != "C123" || message.ExternalThreadID != "1720000000.000001" || message.OccurredAt.IsZero() {
			t.Fatalf("missing origin provenance: %#v", message)
		}
	}
}

func TestSlackContextPagesWithinTheThreadAndDeduplicatesMessages(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		calls++
		if calls == 1 {
			_, _ = response.Write([]byte(`{"ok":true,"has_more":true,"response_metadata":{"next_cursor":"next-page"},"messages":[
				{"ts":"1720000000.000001","user":"U1","text":"Root"}
			]}`))
			return
		}
		if request.URL.Query().Get("cursor") != "next-page" || request.URL.Query().Get("channel") != "C123" {
			t.Errorf("unexpected page query: %s", request.URL.RawQuery)
		}
		_, _ = response.Write([]byte(`{"ok":true,"messages":[
			{"ts":"1720000000.000001","user":"U1","text":"Root"},
			{"ts":"1720000002.000001","thread_ts":"1720000000.000001","user":"U2","text":"Earlier reply"}
		]}`))
	}))
	defer server.Close()
	output, err := newSlackAdapter("", server.URL, server.Client()).delivery(t.Context(), slackContextConfig())
	if err != nil || output["status"] != "complete" || len(decodeSlackContext(t, output)) != 2 || calls != 2 {
		t.Fatalf("paged context = %#v, %v; calls = %d", output, err, calls)
	}
}

func TestSlackContextBoundsPagesAndRetainsTheRootAndRecentReplies(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		calls++
		messages := []map[string]string{}
		if calls == 1 {
			messages = append(messages, map[string]string{"ts": "1720000000.000001", "user": "U1", "text": "Root"})
		}
		for i := 1; i <= 30; i++ {
			second := (calls-1)*30 + i
			messages = append(messages, map[string]string{
				"ts": fmt.Sprintf("17200000%02d.000001", second), "thread_ts": "1720000000.000001",
				"user": "U2", "text": fmt.Sprintf("Reply %d", second),
			})
		}
		_ = json.NewEncoder(response).Encode(map[string]interface{}{
			"ok": true, "has_more": true, "messages": messages,
			"response_metadata": map[string]string{"next_cursor": fmt.Sprintf("page-%d", calls+1)},
		})
	}))
	defer server.Close()
	output, err := newSlackAdapter("", server.URL, server.Client()).delivery(t.Context(), slackContextConfig())
	messages := decodeSlackContext(t, output)
	if err != nil || output["status"] != "partial" || calls != 2 || len(messages) != 50 ||
		messages[0].Text != "Root" || messages[1].Text != "Reply 12" || messages[49].Text != "Reply 60" {
		t.Fatalf("bounded context = %#v, %v; calls = %d", output, err, calls)
	}
}

func TestSlackContextCannotReadAChannelOutsideItsBoundDestination(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		calls++
		t.Error("invalid context must not call Slack")
	}))
	defer server.Close()
	for _, change := range []func(map[string]interface{}){
		func(config map[string]interface{}) { delete(config, slackConnectionKey) },
		func(config map[string]interface{}) {
			config[adapterEnvelopeKey].(map[string]interface{})["event"].(*normalizedConversationEvent).ExternalConversationID = "C-other"
		},
		func(config map[string]interface{}) {
			config[adapterEnvelopeKey].(map[string]interface{})["event"].(*normalizedConversationEvent).ExternalThreadID = ""
		},
		func(config map[string]interface{}) {
			config[adapterEnvelopeKey].(map[string]interface{})["event"].(*normalizedConversationEvent).ExternalMessageID = "1720000100.bad"
		},
	} {
		config := slackContextConfig()
		change(config)
		output, err := newSlackAdapter("", server.URL, server.Client()).delivery(t.Context(), config)
		if err != nil || output["status"] != "unavailable" || len(decodeSlackContext(t, output)) != 0 {
			t.Fatalf("invalid context = %#v, %v", output, err)
		}
	}
	if calls != 0 {
		t.Fatalf("unexpected Slack calls = %d", calls)
	}
}

func TestSlackContextDoesNotFetchHistoryForAnUnthreadedTrigger(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		calls++
		t.Error("a new root message has no earlier thread to fetch")
	}))
	defer server.Close()
	config := slackContextConfig()
	event := config[adapterEnvelopeKey].(map[string]interface{})["event"].(*normalizedConversationEvent)
	event.ExternalThreadID = event.ExternalMessageID
	output, err := newSlackAdapter("", server.URL, server.Client()).delivery(t.Context(), config)
	if err != nil || output["status"] != "complete" || calls != 0 || len(decodeSlackContext(t, output)) != 0 {
		t.Fatalf("unthreaded context = %#v, %v; calls = %d", output, err, calls)
	}
}

func TestSlackContextDoesNotDescribeAMissingThreadRootAsComplete(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		_, _ = response.Write([]byte(`{"ok":true,"messages":[
			{"ts":"1720000001.000001","thread_ts":"1720000000.000001","user":"U2","text":"A reply without the root"}
		]}`))
	}))
	defer server.Close()
	output, err := newSlackAdapter("", server.URL, server.Client()).delivery(t.Context(), slackContextConfig())
	if err != nil || output["status"] != "partial" || len(decodeSlackContext(t, output)) != 1 {
		t.Fatalf("missing root context = %#v, %v", output, err)
	}
}

func TestSlackContextReportsUnavailableAccessAndPartialFetches(t *testing.T) {
	for _, scenario := range []string{"missing_scope", "not_in_channel", "channel_not_found", "thread_not_found", "ratelimited", "rate_limit", "invalid_json", "unexpected_provider_error", "second_page_failure"} {
		t.Run(scenario, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				calls++
				if scenario == "second_page_failure" && calls == 1 {
					_, _ = response.Write([]byte(`{"ok":true,"has_more":true,"response_metadata":{"next_cursor":"next"},"messages":[
						{"ts":"1720000000.000001","user":"U1","text":"Root"}
					]}`))
					return
				}
				switch scenario {
				case "rate_limit":
					response.WriteHeader(http.StatusTooManyRequests)
				case "invalid_json":
					_, _ = response.Write([]byte("not JSON"))
				case "second_page_failure":
					_, _ = response.Write([]byte(`{"ok":false,"error":"missing_scope","needed":"groups:history"}`))
				case "unexpected_provider_error":
					_, _ = response.Write([]byte(`{"ok":false,"error":"unexpected private provider detail","needed":"private data"}`))
				default:
					_ = json.NewEncoder(response).Encode(map[string]interface{}{"ok": false, "error": scenario})
				}
			}))
			defer server.Close()
			output, err := newSlackAdapter("", server.URL, server.Client()).delivery(t.Context(), slackContextConfig())
			wantStatus, wantCount := "unavailable", 0
			wantCode := scenario
			switch scenario {
			case "rate_limit", "ratelimited":
				wantCode = "rate_limited"
			case "invalid_json", "unexpected_provider_error":
				wantCode = "unavailable"
			case "second_page_failure":
				wantCode = "missing_scope"
			}
			if scenario == "second_page_failure" {
				wantStatus, wantCount = "partial", 1
			}
			if err != nil || output["status"] != wantStatus || output["errorCode"] != wantCode || len(decodeSlackContext(t, output)) != wantCount {
				t.Fatalf("failed context = %#v, %v", output, err)
			}
		})
	}
}

func TestSlackContextExecutorUsesTheEphemeralBotCredential(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer xoxb-ephemeral-token" {
			t.Error("context did not use the bound credential")
		}
		_, _ = response.Write([]byte(`{"ok":true,"messages":[{"ts":"1720000000.000001","user":"U1","text":"Root"}]}`))
	}))
	defer server.Close()
	config := slackContextConfig()
	delete(config, slackConnectionKey)
	step := &executor.StepDefinition{Config: config}
	result, err := (&slackDeliveryExecutor{adapter: newSlackAdapter("", server.URL, server.Client())}).Execute(t.Context(), step, slackBindingResolver{
		bindings: map[string]interface{}{slackConnectionKey: "xoxb-ephemeral-token"},
	})
	if err != nil || result.Output["status"] != "complete" || len(decodeSlackContext(t, result.Output)) != 1 {
		t.Fatalf("executor context = %#v, %v", result, err)
	}
	if _, leaked := step.Config[slackConnectionKey]; leaked {
		t.Fatal("context credential leaked into durable config")
	}
}

func TestSlackContextBoundsTextBytesWithoutBreakingUnicode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		messages := []map[string]string{{"ts": "1720000000.000001", "user": "U1", "text": "Root " + strings.Repeat("🦭", 20_000)}}
		for i := 1; i <= 6; i++ {
			messages = append(messages, map[string]string{
				"ts": fmt.Sprintf("172000000%d.000001", i), "thread_ts": "1720000000.000001", "user": "U2",
				"text": fmt.Sprintf("Reply %d ", i) + strings.Repeat("🦭", 20_000),
			})
		}
		_ = json.NewEncoder(response).Encode(map[string]interface{}{"ok": true, "messages": messages})
	}))
	defer server.Close()
	output, err := newSlackAdapter("", server.URL, server.Client()).delivery(t.Context(), slackContextConfig())
	messages := decodeSlackContext(t, output)
	if err != nil || output["status"] != "partial" || len(messages) != 4 ||
		!strings.HasPrefix(messages[0].Text, "Root ") || !strings.HasPrefix(messages[1].Text, "Reply 4 ") ||
		!strings.HasPrefix(messages[3].Text, "Reply 6 ") {
		t.Fatalf("bounded text context = %v, %v; messages = %d", output["status"], err, len(messages))
	}
	totalBytes := 0
	for _, message := range messages {
		if !utf8.ValidString(message.Text) || len(message.Text) > 64*1024 {
			t.Fatal("context text exceeds the per-message budget or corrupts Unicode")
		}
		totalBytes += len(message.Text)
	}
	if totalBytes > 256*1024 {
		t.Fatalf("context text exceeds the total budget: %d bytes", totalBytes)
	}
}
