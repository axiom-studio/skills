package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/axiom-studio/skills.sdk/executor"
)

func slackActionHTTPFixture(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	previousClient := slackHTTPClient
	previousBaseURL := slackBaseURLOverride
	slackHTTPClient = &http.Client{Transport: identityTransport(func(request *http.Request) (*http.Response, error) {
		response := httptest.NewRecorder()
		handler(response, request)
		return response.Result(), nil
	})}
	slackBaseURLOverride = "https://slack.test"
	t.Cleanup(func() {
		slackHTTPClient = previousClient
		slackBaseURLOverride = previousBaseURL
	})
}

func TestSlackSendMessageReturnsProviderConversationReceipt(t *testing.T) {
	for _, test := range []struct {
		name           string
		channel        string
		defaultChannel string
		requestChannel string
		receiptChannel string
		resolvedByName bool
	}{
		{name: "user recipient", channel: "U123", requestChannel: "U123", receiptChannel: "D456"},
		{name: "enterprise user recipient", channel: "W123", requestChannel: "W123", receiptChannel: "D456"},
		{name: "configured user recipient", defaultChannel: "U123", requestChannel: "U123", receiptChannel: "D456"},
		{name: "direct message", channel: "D456", requestChannel: "D456", receiptChannel: "D456"},
		{name: "public channel", channel: "C123", requestChannel: "C123", receiptChannel: "C123"},
		{name: "private group", channel: "G123", requestChannel: "G123", receiptChannel: "G123"},
		{name: "provider canonical channel", channel: "C123", requestChannel: "C123", receiptChannel: "C456"},
		{name: "channel name", channel: "general", requestChannel: "C123", receiptChannel: "C456", resolvedByName: true},
		{name: "channel alias", channel: "#general", requestChannel: "C123", receiptChannel: "C123", resolvedByName: true},
		{name: "name beginning with ID prefix", channel: "General", requestChannel: "C123", receiptChannel: "C123", resolvedByName: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var paths []string
			slackActionHTTPFixture(t, func(response http.ResponseWriter, request *http.Request) {
				paths = append(paths, request.URL.Path)
				if request.Header.Get("Authorization") != "Bearer xoxb-send" {
					t.Error("send did not use the bound connection")
				}
				switch request.URL.Path {
				case "/conversations.list":
					if !test.resolvedByName || request.Method != http.MethodGet {
						t.Error("an exact recipient must not require channel discovery")
					}
					_ = json.NewEncoder(response).Encode(map[string]interface{}{
						"ok": true, "channels": []SlackChannel{{ID: "C123", Name: "general"}},
					})
				case "/chat.postMessage":
					if err := request.ParseForm(); err != nil {
						t.Error(err)
					}
					if request.Method != http.MethodPost || request.Form.Get("channel") != test.requestChannel ||
						request.Form.Get("text") != "Please reply" || request.Form.Get("thread_ts") != "100.000001" {
						t.Errorf("unexpected send request: method=%s form=%v", request.Method, request.Form)
					}
					_ = json.NewEncoder(response).Encode(map[string]interface{}{
						"ok": true, "channel": test.receiptChannel, "ts": "101.000001",
					})
				default:
					t.Errorf("unexpected provider request: %s", request.URL.Path)
					http.Error(response, "unexpected provider request", http.StatusBadRequest)
				}
			})
			result, err := (&SlackSendMessageExecutor{}).Execute(t.Context(), &executor.StepDefinition{Config: map[string]interface{}{
				"channel": test.channel, "default_channel": test.defaultChannel, "message": "Please reply", "threadTs": "100.000001",
			}}, slackBindingResolver{bindings: map[string]interface{}{slackBotTokenCredential: "xoxb-send"}})
			if err != nil {
				t.Fatal(err)
			}
			if result.Output["success"] != true || result.Output["channel"] != test.receiptChannel || result.Output["timestamp"] != "101.000001" {
				t.Fatalf("provider receipt = %#v", result.Output)
			}
			wantPaths := []string{"/chat.postMessage"}
			if test.resolvedByName {
				wantPaths = []string{"/conversations.list", "/chat.postMessage"}
			}
			if !reflect.DeepEqual(paths, wantPaths) {
				t.Fatalf("provider requests = %v, want %v", paths, wantPaths)
			}
		})
	}
}

func TestSlackSendMessageRejectsMissingOrMalformedReceipt(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
	}{
		{name: "missing receipt", body: `{"ok":true}`},
		{name: "missing channel", body: `{"ok":true,"ts":"101.000001"}`},
		{name: "missing timestamp", body: `{"ok":true,"channel":"D456"}`},
		{name: "user ID channel", body: `{"ok":true,"channel":"U123","ts":"101.000001"}`},
		{name: "channel alias", body: `{"ok":true,"channel":"#general","ts":"101.000001"}`},
		{name: "malformed channel", body: `{"ok":true,"channel":"D-bad","ts":"101.000001"}`},
		{name: "whitespace channel", body: `{"ok":true,"channel":" D456 ","ts":"101.000001"}`},
		{name: "numeric channel", body: `{"ok":true,"channel":456,"ts":"101.000001"}`},
		{name: "malformed timestamp", body: `{"ok":true,"channel":"D456","ts":"not-a-timestamp"}`},
		{name: "integer timestamp", body: `{"ok":true,"channel":"D456","ts":"101"}`},
		{name: "numeric timestamp", body: `{"ok":true,"channel":"D456","ts":101.000001}`},
		{name: "provider rejection", body: `{"ok":false,"error":"channel_not_found","channel":"D456","ts":"101.000001"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			requests := 0
			slackActionHTTPFixture(t, func(response http.ResponseWriter, request *http.Request) {
				requests++
				if request.URL.Path != "/chat.postMessage" {
					t.Errorf("unexpected provider request: %s", request.URL.Path)
				}
				_, _ = response.Write([]byte(test.body))
			})
			result, err := (&SlackSendMessageExecutor{}).Execute(t.Context(), &executor.StepDefinition{Config: map[string]interface{}{
				"channel": "U123", "message": "Please reply",
			}}, slackBindingResolver{bindings: map[string]interface{}{slackBotTokenCredential: "xoxb-send"}})
			if err == nil || result != nil || requests != 1 {
				t.Fatalf("invalid receipt reported success: result=%#v error=%v requests=%d", result, err, requests)
			}
		})
	}
}

func TestSlackConversationActionsRejectUserRecipientBeforeProviderRequest(t *testing.T) {
	requests := 0
	slackActionHTTPFixture(t, func(response http.ResponseWriter, request *http.Request) {
		requests++
		t.Errorf("user ID reached a conversation operation: %s", request.URL.Path)
		_, _ = response.Write([]byte(`{"ok":true}`))
	})
	for _, action := range []executor.StepExecutor{
		&SlackReadMessagesExecutor{}, &SlackSearchMessagesExecutor{},
		&SlackAddReactionExecutor{}, &SlackRemoveReactionExecutor{},
		&SlackUpdateMessageExecutor{}, &SlackDeleteMessageExecutor{},
		&SlackRenameChannelExecutor{}, &SlackArchiveChannelExecutor{},
		&SlackSetChannelTopicExecutor{}, &SlackSetChannelPurposeExecutor{},
		&SlackSendEphemeralMessageExecutor{},
	} {
		for _, channel := range []string{"U123", "W123", " U123 "} {
			t.Run(action.Type()+"/"+strings.TrimSpace(channel), func(t *testing.T) {
				result, err := action.Execute(t.Context(), &executor.StepDefinition{Config: map[string]interface{}{
					"channel": channel, "query": "reply", "threadTs": "100.000001", "timestamp": "100.000001",
					"text": "updated", "emoji": "thumbsup", "name": "renamed", "topic": "topic", "purpose": "purpose", "user": "U456",
				}}, slackBindingResolver{bindings: map[string]interface{}{slackBotTokenCredential: "xoxb-read"}})
				if err == nil || result != nil {
					t.Fatalf("user recipient accepted by %s: result=%#v error=%v", action.Type(), result, err)
				}
			})
		}
	}
	if requests != 0 {
		t.Fatalf("invalid conversation IDs caused %d provider requests", requests)
	}
}

func TestSlackChannelNameLookupRejectsInvalidProviderConversation(t *testing.T) {
	for _, providerChannel := range []string{"", "U123", "D-bad"} {
		t.Run(providerChannel, func(t *testing.T) {
			requests := 0
			slackActionHTTPFixture(t, func(response http.ResponseWriter, request *http.Request) {
				requests++
				if request.URL.Path != "/conversations.list" {
					t.Errorf("invalid lookup reached another operation: %s", request.URL.Path)
				}
				_ = json.NewEncoder(response).Encode(map[string]interface{}{
					"ok": true, "channels": []SlackChannel{{ID: providerChannel, Name: "general"}},
				})
			})
			result, err := (&SlackReadMessagesExecutor{}).Execute(t.Context(), &executor.StepDefinition{Config: map[string]interface{}{
				"channel": "#general",
			}}, slackBindingResolver{bindings: map[string]interface{}{slackBotTokenCredential: "xoxb-read"}})
			if err == nil || result != nil || requests != 1 {
				t.Fatalf("invalid provider conversation accepted: result=%#v error=%v requests=%d", result, err, requests)
			}
		})
	}
}
