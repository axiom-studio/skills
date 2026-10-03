package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/axiom-studio/skills.sdk/executor"
	"gopkg.in/yaml.v3"
)

func TestTelegramFileOnlyIdentityAndTopicNormalization(t *testing.T) {
	for _, test := range []struct {
		name, body, thread, participant string
		direct, mentioned               bool
		kind                            string
	}{
		{name: "private photo", body: `{"update_id":1,"message":{"message_id":2,"date":1700000000,"from":{"id":9007199254740991,"first_name":"Kev","username":"kev"},"chat":{"id":9007199254740991,"type":"private"},"photo":[{"file_id":"small","width":10,"height":10,"file_size":12},{"file_id":"large","width":100,"height":100,"file_size":24}]}}`, thread: "reply:2", participant: "9007199254740991", direct: true, mentioned: true, kind: "user"},
		{name: "group reply", body: `{"update_id":2,"message":{"message_id":9,"text":"thanks","from":{"id":99,"first_name":"Kev"},"chat":{"id":-1001234567890,"type":"supergroup","title":"Lifting"},"reply_to_message":{"message_id":7,"from":{"id":123456789,"is_bot":true},"text":"hello"}}}`, thread: "reply:7", participant: "99", mentioned: true, kind: "user"},
		{name: "forum topic", body: `{"update_id":3,"message":{"message_id":10,"message_thread_id":77,"caption":"@OpenSealBot read this","document":{"file_id":"pdf","file_name":"plan.pdf","mime_type":"application/pdf","file_size":100},"from":{"id":99},"chat":{"id":-1001234567890,"type":"supergroup"},"reply_to_message":{"message_id":5}}}`, thread: "topic:77", participant: "99", mentioned: true, kind: "user"},
		{name: "private forum", body: `{"update_id":4,"message":{"message_id":10,"message_thread_id":42,"text":"hello","from":{"id":99},"chat":{"id":99,"type":"private"}}}`, thread: "topic:42", participant: "99", direct: true, mentioned: true, kind: "user"},
		{name: "channel sender", body: `{"update_id":5,"channel_post":{"message_id":5,"text":"news","from":{"id":1087968824,"is_bot":true,"first_name":"Anonymous"},"sender_chat":{"id":-100777,"type":"channel","title":"News"},"chat":{"id":-100777,"type":"channel","title":"News"}}}`, thread: "reply:5", participant: "chat:-100777", kind: "chat"},
		{name: "direct message topic", body: `{"update_id":6,"message":{"message_id":8,"text":"hello","direct_messages_topic":{"topic_id":23},"from":{"id":99},"chat":{"id":-10088,"type":"supergroup"}}}`, thread: "direct-topic:23", participant: "99", kind: "user"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var update telegramUpdate
			if err := json.Unmarshal([]byte(test.body), &update); err != nil {
				t.Fatal(err)
			}
			event, ok := normalizeTelegramUpdate(update, &telegramConversationEndpoint{Provider: "telegram", Address: "*", Configuration: map[string]interface{}{"botId": "123456789", "botUsername": "OpenSealBot"}})
			if !ok || event.ExternalThreadID != test.thread || event.ExternalParticipantID != test.participant || event.Direct != test.direct || event.MentionsEndpoint != test.mentioned || event.Attributes["participantKind"] != test.kind {
				t.Fatalf("normalized event=%#v", event)
			}
			if test.name == "private photo" && (len(event.Attachments) != 1 || event.Attachments[0].ID != "large") {
				t.Fatal(event.Attachments)
			}
			if test.kind == "chat" && (event.ParticipantIsBot || event.Attributes["participantDisplayName"] != "News") {
				t.Fatal(event)
			}
		})
	}
}
func TestTelegramDMContinuityKeepsChatAndSeparateTurnRoots(t *testing.T) {
	endpoint := &telegramConversationEndpoint{Provider: "telegram", Address: "99"}
	for _, id := range []int64{1, 2} {
		event, ok := normalizeTelegramUpdate(telegramUpdate{UpdateID: id, Message: telegramMessage{MessageID: id, Text: "hello", From: telegramUser{ID: 99}, Chat: telegramChat{ID: 99, Type: "private"}}}, endpoint)
		if !ok || event.ExternalConversationID != "99" || event.ExternalThreadID != []string{"reply:1", "reply:2"}[id-1] {
			t.Fatal(event)
		}
	}
}
func TestTelegramMentionDoesNotMatchAnotherBot(t *testing.T) {
	endpoint := &telegramConversationEndpoint{Provider: "telegram", Address: "99", Configuration: map[string]interface{}{"botUsername": "OpenSealBot", "botId": "123456789"}}
	for _, text := range []string{"@OpenSealBotOther hi", "@otherbot hi"} {
		event, _ := normalizeTelegramUpdate(telegramUpdate{UpdateID: 1, Message: telegramMessage{MessageID: 1, Text: text, From: telegramUser{ID: 99}, Chat: telegramChat{ID: 99, Type: "group"}}}, endpoint)
		if event.MentionsEndpoint {
			t.Fatal(event)
		}
	}
}
func TestTelegramContextDoesNotPretendToFetchHistory(t *testing.T) {
	request := &telegramAdapterRequest{Endpoint: &telegramConversationEndpoint{Provider: "telegram", Address: "*", InstallationWide: true}, Event: &telegramNormalizedEvent{ExternalConversationID: "99", ExternalParticipantID: "99", ExternalMessageID: "8", ExternalThreadID: "topic:4", Attributes: map[string]interface{}{"botId": "123456789", "participantDisplayName": "Kev", "chatType": "private", "replyContext": map[string]interface{}{"externalMessageId": "7", "text": "parent"}}}}
	output := telegramReadContext(request)
	if output["status"] != "partial" || output["errorCode"] != "provider_history_unavailable" {
		t.Fatal(output)
	}
	source := output["source"].(map[string]interface{})
	if source["participantDisplayName"] != "Kev" || source["workspaceId"] != "123456789" {
		t.Fatal(source)
	}
}
func TestTelegramDeliveryTargetDistinguishesTopicAndReply(t *testing.T) {
	for _, test := range []struct {
		thread, key string
		value       int64
	}{{"topic:77", "message_thread_id", 77}, {"direct-topic:23", "direct_messages_topic_id", 23}, {"reply:5", "reply_parameters", 5}, {"5", "reply_parameters", 5}} {
		t.Run(test.thread, func(t *testing.T) {
			request := &telegramAdapterRequest{Endpoint: &telegramConversationEndpoint{Provider: "telegram", Address: "*", InstallationWide: true}, Delivery: &telegramConversationDelivery{ExternalConversationID: "-1001234567890", ExternalThreadID: test.thread}}
			params, err := telegramDeliveryTarget(request)
			if err != nil || params["chat_id"] != "-1001234567890" {
				t.Fatal(params, err)
			}
			if test.key == "reply_parameters" {
				if params[test.key].(map[string]interface{})["message_id"] != test.value {
					t.Fatal(params)
				}
			} else if params[test.key] != test.value || params["reply_parameters"] != nil {
				t.Fatal(params)
			}
		})
	}
	if _, err := telegramDeliveryTarget(&telegramAdapterRequest{Endpoint: &telegramConversationEndpoint{Address: "99"}, Delivery: &telegramConversationDelivery{ExternalConversationID: "100"}}); err == nil {
		t.Fatal("cross-endpoint destination accepted")
	}
}

type telegramTestTransport func(*http.Request) (*http.Response, error)

func (f telegramTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestTelegramUnknownDeliveryFailsWithoutRetryOrCredentialLeak(t *testing.T) {
	attempts := 0
	adapter := newTelegramConversationAdapter("https://api.telegram.org/bot", &http.Client{Transport: telegramTestTransport(func(r *http.Request) (*http.Response, error) {
		attempts++
		return nil, errors.New("failed https://api.telegram.org/bot" + testTelegramBotToken + "/sendMessage")
	})})
	output, err := adapter.delivery(t.Context(), telegramDeliveryConfig("message.send", ""))
	encoded, _ := json.Marshal(output)
	if err != nil || output["outcome"] != "failed" || output["errorCode"] != "delivery_unconfirmed" || attempts != 1 || strings.Contains(string(encoded), testTelegramBotToken) {
		t.Fatalf("output=%s err=%v attempts=%d", encoded, err, attempts)
	}
	for _, body := range []string{`{"ok":true}`, `{"ok":true,"result":{"message_id":4,"chat":{"id":100}}}`} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, body) }))
		output, err = newTelegramConversationAdapter(server.URL+"/bot", server.Client()).delivery(t.Context(), telegramDeliveryConfig("message.send", ""))
		server.Close()
		if err != nil || output["outcome"] != "failed" {
			t.Fatal(output, err)
		}
	}
}
func TestTelegramGetMeDiscoveryIsBotIdentityNotChatEnumeration(t *testing.T) {
	calls := 0
	previousClient, previousBase := httpClient, telegramAPIBaseOverride
	t.Cleanup(func() { httpClient = previousClient; telegramAPIBaseOverride = previousBase })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if !strings.HasSuffix(r.URL.Path, "/getMe") {
			t.Error("unexpected chat or updates enumeration")
		}
		_, _ = io.WriteString(w, `{"ok":true,"result":{"id":123456789,"is_bot":true,"username":"opensealbot","first_name":"Seal"}}`)
	}))
	defer server.Close()
	httpClient = server.Client()
	telegramAPIBaseOverride = server.URL + "/bot"
	result, err := (&GetMeExecutor{}).Execute(t.Context(), &executor.StepDefinition{}, telegramBindingResolver{bindings: map[string]interface{}{telegramCredentialKey: testTelegramBotToken}})
	if err != nil || calls != 1 || !reflect.DeepEqual(result.Output["items"], []interface{}{}) || result.Output["connection"].(map[string]interface{})["installationId"] != "123456789" {
		t.Fatal(result, err, calls)
	}
	requireTelegramActionOutputSchema(t, "telegram-get-me", result.Output)
}
func TestTelegramAgentCannotConsumeManagedUpdates(t *testing.T) {
	if result, err := (&GetUpdatesExecutor{}).Execute(context.Background(), nil, nil); err == nil || result != nil {
		t.Fatal("agent consumed managed stream")
	}
}
func TestTelegramWebhookManifestDoesNotExposeManagedUpdateStream(t *testing.T) {
	data, _ := os.ReadFile("skill.yaml")
	var m struct {
		Definition struct {
			Actions map[string]interface{} `yaml:"actions"`
			Prompt  struct {
				AllowedTools []string `yaml:"allowedTools"`
			} `yaml:"prompt"`
			CallbackAdapters map[string]interface{} `yaml:"callbackAdapters"`
		} `yaml:"definition"`
	}
	if err := yaml.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	if len(m.Definition.CallbackAdapters) != 0 {
		t.Fatal("polling callback adapter shipped in webhook mode")
	}
	if _, ok := m.Definition.Actions["telegram-get-updates"]; ok {
		t.Fatal("agent polling exposed")
	}
	for _, tool := range m.Definition.Prompt.AllowedTools {
		if tool == "telegram-get-updates" || tool == "telegram-set-webhook" {
			t.Fatal("managed stream action exposed")
		}
	}
}

func TestTelegramGetMeRejectsWrongOrNonBotInstallation(t *testing.T) {
	for _, body := range []string{`{"ok":true,"result":{"id":99,"is_bot":true,"username":"otherbot"}}`, `{"ok":true,"result":{"id":123456789,"is_bot":false,"username":"human"}}`, `{"ok":false,"error_code":401,"description":"invalid token 123456789:test-token"}`} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, body) }))
		_, err := newTelegramConversationAdapter(server.URL+"/bot", server.Client()).getTelegramIdentity(t.Context(), testTelegramBotToken)
		server.Close()
		if err == nil || strings.Contains(err.Error(), testTelegramBotToken) {
			t.Fatal(err)
		}
	}
}
func TestTelegramDeliveryRejectsAnotherBotInstallationBeforeProvider(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++ }))
	defer server.Close()
	config := telegramDeliveryConfig("message.send", "")
	config[telegramAdapterEnvelope].(*telegramAdapterRequest).Endpoint.InstallationID = "99"
	output, err := newTelegramConversationAdapter(server.URL+"/bot", server.Client()).delivery(t.Context(), config)
	if err == nil || output != nil || calls != 0 {
		t.Fatal(output, err, calls)
	}
}

type telegramLiteralResolver struct{ telegramBindingResolver }

func (r telegramLiteralResolver) ResolveMap(input map[string]interface{}) map[string]interface{} {
	return input
}
func (r telegramLiteralResolver) ResolveString(input string) string { return input }
func TestTelegramSendActionCanonicalReceiptAndTopicArguments(t *testing.T) {
	previousClient, previousBase := httpClient, telegramAPIBaseOverride
	t.Cleanup(func() { httpClient = previousClient; telegramAPIBaseOverride = previousBase })
	for _, test := range []struct {
		action executor.StepExecutor
		input  map[string]interface{}
		method string
	}{
		{&SendMessageExecutor{}, map[string]interface{}{"chatId": "@channel", "text": "hello", "messageThreadId": 77, "replyToMessageId": 5}, "sendMessage"},
		{&SendPhotoExecutor{}, map[string]interface{}{"chatId": "@channel", "photo": "file1", "messageThreadId": 77, "replyToMessageId": 5}, "sendPhoto"},
		{&SendDocumentExecutor{}, map[string]interface{}{"chatId": "@channel", "document": "file1", "messageThreadId": 77, "replyToMessageId": 5}, "sendDocument"},
	} {
		t.Run(test.method, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !strings.HasSuffix(r.URL.Path, "/"+test.method) {
					t.Error("wrong method")
				}
				var body map[string]interface{}
				_ = json.NewDecoder(r.Body).Decode(&body)
				if body["chat_id"] != "@channel" {
					t.Error(body)
				}
				topic := body["message_thread_id"]
				if topic != float64(77) && topic != "77" {
					t.Error(body)
				}
				switch reply := body["reply_parameters"].(type) {
				case map[string]interface{}:
					if reply["message_id"] != float64(5) {
						t.Error(body)
					}
				case string:
					var parsed map[string]interface{}
					_ = json.Unmarshal([]byte(reply), &parsed)
					if parsed["message_id"] != float64(5) {
						t.Error(body)
					}
				default:
					t.Error(body)
				}
				_, _ = io.WriteString(w, `{"ok":true,"result":{"message_id":9007199254740991,"chat":{"id":-1001234567890,"type":"channel"}}}`)
			}))
			defer server.Close()
			httpClient = server.Client()
			telegramAPIBaseOverride = server.URL + "/bot"
			result, err := test.action.Execute(t.Context(), &executor.StepDefinition{Config: test.input}, telegramLiteralResolver{telegramBindingResolver{bindings: map[string]interface{}{telegramCredentialKey: testTelegramBotToken}}})
			if err != nil || result.Output["chatId"] != "-1001234567890" || result.Output["messageId"] != "9007199254740991" {
				t.Fatal(result, err)
			}
			requireTelegramActionOutputSchema(t, test.action.Type(), result.Output)
		})
	}
}

func TestTelegramGetChatCanonicalEvidenceMatchesManifest(t *testing.T) {
	previousClient, previousBase := httpClient, telegramAPIBaseOverride
	t.Cleanup(func() { httpClient = previousClient; telegramAPIBaseOverride = previousBase })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/getChat") {
			t.Error("unexpected Telegram identity action")
		}
		_, _ = io.WriteString(w, `{"ok":true,"result":{"id":-1001234567890,"type":"supergroup","title":"Lifting"}}`)
	}))
	defer server.Close()
	httpClient = server.Client()
	telegramAPIBaseOverride = server.URL + "/bot"
	result, err := (&GetChatExecutor{}).Execute(t.Context(), &executor.StepDefinition{Config: map[string]interface{}{"chatId": "-1001234567890"}}, telegramLiteralResolver{telegramBindingResolver{bindings: map[string]interface{}{telegramCredentialKey: testTelegramBotToken}}})
	if err != nil || result.Output["chatId"] != "-1001234567890" {
		t.Fatal(result, err)
	}
	requireTelegramActionOutputSchema(t, "telegram-get-chat", result.Output)
}

func TestTelegramAddressedCommandMentionsOnlyExactBot(t *testing.T) {
	endpoint := &telegramConversationEndpoint{Provider: "telegram", Address: "99", Configuration: map[string]interface{}{"botUsername": "OpenSealBot", "botId": "123456789"}}
	for _, test := range []struct {
		text      string
		mentioned bool
	}{{"/help@OpenSealBot", true}, {"/help@opensealbot topic", true}, {"/help@OtherBot", false}, {"/help@OpenSealBotOther", false}} {
		event, ok := normalizeTelegramUpdate(telegramUpdate{UpdateID: 1, Message: telegramMessage{MessageID: 1, Text: test.text, From: telegramUser{ID: 99}, Chat: telegramChat{ID: 99, Type: "group"}, Entities: []telegramEntity{{Type: "bot_command", Offset: 0, Length: len(test.text)}}}}, endpoint)
		if !ok || event.MentionsEndpoint != test.mentioned {
			t.Fatal(test, event)
		}
	}
}
func TestTelegramReviewLinkUsesOnlyWebButton(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var body map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		raw, _ := json.Marshal(body["reply_markup"])
		if string(raw) != `{"inline_keyboard":[[{"text":"Review approval","url":"https://app.example.com/approval/123"}]]}` || strings.Contains(string(raw), "callback_data") {
			t.Error(string(raw))
		}
		_, _ = io.WriteString(w, `{"ok":true,"result":{"message_id":11,"chat":{"id":99}}}`)
	}))
	defer server.Close()
	config := telegramDeliveryConfig("message.send", "")
	request := config[telegramAdapterEnvelope].(*telegramAdapterRequest)
	request.Delivery.Parameters = map[string]interface{}{"reviewRequest": map[string]interface{}{"label": "Review approval", "url": "https://app.example.com/approval/123"}}
	output, err := newTelegramConversationAdapter(server.URL+"/bot", server.Client()).delivery(t.Context(), config)
	if err != nil || output["outcome"] != "delivered" || requests != 1 {
		t.Fatal(output, err)
	}
	for _, link := range []string{"javascript:alert(1)", "https://user:password@app.example.com/approval/123", "https://app.example.com/approval/123?access_token=secret"} {
		request.Delivery.Parameters["reviewRequest"].(map[string]interface{})["url"] = link
		output, err = newTelegramConversationAdapter(server.URL+"/bot", server.Client()).delivery(t.Context(), config)
		if err != nil || output["outcome"] != "failed" || requests != 1 {
			t.Fatal(output, err)
		}
	}
}
func TestTelegramDeliveryFencesReclaimedAttemptsWithoutSafeRetryReceipt(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = io.WriteString(w, `{"ok":true,"result":{"message_id":11,"chat":{"id":99}}}`)
	}))
	defer server.Close()
	adapter := newTelegramConversationAdapter(server.URL+"/bot", server.Client())
	config := telegramDeliveryConfig("message.send", "")
	request := config[telegramAdapterEnvelope].(*telegramAdapterRequest)
	for _, attempt := range []int{0, 2, 3} {
		request.Delivery.Attempt = attempt
		output, err := adapter.delivery(t.Context(), config)
		if err != nil || output["outcome"] != "failed" || output["errorCode"] != "delivery_unconfirmed" || calls != 0 {
			t.Fatal(output, err, calls)
		}
	}
	request.Delivery.Attempt = 1
	checkpoint, _ := telegramCheckpoint(request)
	checkpoint.SafeRetryAfterAttempt = 1
	request.Delivery.Progress = map[string]interface{}{"telegramDelivery": checkpoint}
	request.Delivery.Attempt = 3
	output, err := adapter.delivery(t.Context(), config)
	if err != nil || output["outcome"] != "failed" || calls != 0 {
		t.Fatal(output, err, calls)
	}
	request.Delivery.Attempt = 2
	output, err = adapter.delivery(t.Context(), config)
	if err != nil || output["outcome"] != "delivered" || calls != 1 {
		t.Fatal(output, err, calls)
	}
}
func TestTelegramTypingHasScopedAdvisoryReceipt(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if !strings.HasSuffix(r.URL.Path, "/sendChatAction") {
			t.Error("wrong operation")
		}
		_, _ = io.WriteString(w, `{"ok":true,"result":true}`)
	}))
	defer server.Close()
	adapter := newTelegramConversationAdapter(server.URL+"/bot", server.Client())
	config := telegramDeliveryConfig("typing.set", "")
	request := config[telegramAdapterEnvelope].(*telegramAdapterRequest)
	for _, state := range []string{"processing", "active", "suspended"} {
		request.Delivery.Parameters = map[string]interface{}{"state": state}
		output, err := adapter.delivery(t.Context(), config)
		if err != nil || output["outcome"] != "delivered" || output["providerMessageId"] != "99:typing:delivery-1" {
			t.Fatal(output, err)
		}
	}
	if calls != 1 {
		t.Fatal("renewed typing after completion", calls)
	}
}
func TestTelegramOrdinaryReplySourceAndCanonicalParentAreDistinct(t *testing.T) {
	message := telegramMessage{MessageID: 8, Text: "thanks", From: telegramUser{ID: 99, FirstName: "Kev"}, Chat: telegramChat{ID: 99, Type: "private"}, ReplyTo: &telegramMessage{MessageID: 7}}
	event, ok := normalizeTelegramUpdate(telegramUpdate{UpdateID: 1, Message: message}, &telegramConversationEndpoint{Provider: "telegram", Address: "99"})
	if !ok || event.ReplyToExternalMessageID != "99:7" || event.ExternalMessageID != "99:8" || event.Source == nil || event.Source.MessageID != "8" || event.ParticipantDisplayName != "Kev" {
		t.Fatal(event)
	}
	message.MessageThreadID = 77
	event, _ = normalizeTelegramUpdate(telegramUpdate{UpdateID: 2, Message: message}, &telegramConversationEndpoint{Provider: "telegram", Address: "99"})
	if event.ReplyToExternalMessageID != "" || event.ExternalThreadID != "topic:77" {
		t.Fatal(event)
	}
}
func TestTelegramChannelDirectMessageChatBypassesGroupMentionRequirement(t *testing.T) {
	event, ok := normalizeTelegramUpdate(telegramUpdate{UpdateID: 1, Message: telegramMessage{MessageID: 7, Text: "hello", From: telegramUser{ID: 99}, Chat: telegramChat{ID: -100123, Type: "supergroup", IsDirectMessages: true}, DirectMessagesTopic: &telegramDirectTopic{TopicID: 77}}}, &telegramConversationEndpoint{Provider: "telegram", Address: "*"})
	if !ok || !event.Direct || !event.MentionsEndpoint || event.ExternalThreadID != "direct-topic:77" || event.Attributes["isDirectMessages"] != true {
		t.Fatal(event)
	}
}
func TestTelegramTypingClearWithEmptyStatusDoesNotRenew(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++ }))
	defer server.Close()
	config := telegramDeliveryConfig("typing.set", "")
	config[telegramAdapterEnvelope].(*telegramAdapterRequest).Delivery.Parameters = map[string]interface{}{"status": ""}
	output, err := newTelegramConversationAdapter(server.URL+"/bot", server.Client()).delivery(t.Context(), config)
	if err != nil || output["outcome"] != "delivered" || output["providerMessageId"] == nil || calls != 0 {
		t.Fatal(output, err, calls)
	}
}
func TestTelegramQuotedAnonymousSenderAndCrossChatParent(t *testing.T) {
	message := telegramMessage{MessageID: 8, Text: "thanks", From: telegramUser{ID: 99}, Chat: telegramChat{ID: -10099, Type: "supergroup"}, ReplyTo: &telegramMessage{MessageID: 7, Text: "hello", From: telegramUser{ID: 1087968824, FirstName: "Fake"}, SenderChat: &telegramChat{ID: -10099, Title: "Anonymous Ops", Type: "supergroup"}}}
	event, _ := normalizeTelegramUpdate(telegramUpdate{UpdateID: 1, Message: message}, &telegramConversationEndpoint{Provider: "telegram", Address: "*"})
	parent := event.Attributes["replyContext"].(map[string]interface{})
	if parent["externalParticipantId"] != "chat:-10099" || parent["participantDisplayName"] != "Anonymous Ops" {
		t.Fatal(parent)
	}
	message.ReplyTo.Chat.ID = -10088
	event, _ = normalizeTelegramUpdate(telegramUpdate{UpdateID: 2, Message: message}, &telegramConversationEndpoint{Provider: "telegram", Address: "*"})
	if event.ReplyToExternalMessageID != "" || event.Attributes["replyContext"] != nil || event.ExternalThreadID != "reply:8" {
		t.Fatal(event)
	}
}
