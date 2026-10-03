package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testTelegramWebhookURL = "https://app.example.com/api/orchestrator/conversation-gateway/opaque-route"

func telegramWebhookConfig(operation string) map[string]interface{} {
	return map[string]interface{}{telegramCredentialKey: testTelegramBotToken, telegramAdapterEnvelope: &telegramAdapterRequest{Operation: operation, Gateway: &telegramIngressGateway{Provider: "telegram", InstallationID: "123456789", ApplicationID: "123456789", Scope: telegramConversationScope{Kind: "tenant", ID: "7"}, DeploymentID: "agent-1"}, Webhook: &telegramWebhookRequest{URL: testTelegramWebhookURL, InstallationID: "123456789"}}}
}
func TestTelegramWebhookConfigureAndRemovePreservePendingUpdates(t *testing.T) {
	current := ""
	setCalls, deleteCalls := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		switch {
		case strings.HasSuffix(r.URL.Path, "/getMe"):
			_, _ = io.WriteString(w, `{"ok":true,"result":{"id":123456789,"is_bot":true,"username":"opensealbot"}}`)
		case strings.HasSuffix(r.URL.Path, "/getWebhookInfo"):
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "result": map[string]interface{}{"url": current, "pending_update_count": 3}})
		case strings.HasSuffix(r.URL.Path, "/setWebhook"):
			setCalls++
			if body["url"] != testTelegramWebhookURL || body["secret_token"] != telegramWebhookSecret(testTelegramBotToken) || body["drop_pending_updates"] != false {
				t.Error(body)
			}
			updates, _ := json.Marshal(body["allowed_updates"])
			if string(updates) != `["message","channel_post"]` {
				t.Error(string(updates))
			}
			current = body["url"].(string)
			_, _ = io.WriteString(w, `{"ok":true,"result":true}`)
		case strings.HasSuffix(r.URL.Path, "/deleteWebhook"):
			deleteCalls++
			if body["drop_pending_updates"] != false {
				t.Error(body)
			}
			current = ""
			_, _ = io.WriteString(w, `{"ok":true,"result":true}`)
		default:
			t.Error("unexpected API method")
		}
	}))
	defer server.Close()
	adapter := newTelegramConversationAdapter(server.URL+"/bot", server.Client())
	for _, operation := range []string{"webhook.configure", "webhook.status", "webhook.configure"} {
		output, err := adapter.ingress(t.Context(), telegramWebhookConfig(operation))
		encoded, _ := json.Marshal(output)
		if err != nil || output["status"] != "configured" || output["pendingUpdateCount"] != int64(3) || strings.Contains(string(encoded), testTelegramBotToken) || strings.Contains(string(encoded), telegramWebhookSecret(testTelegramBotToken)) {
			t.Fatal(output, err)
		}
	}
	output, err := adapter.ingress(t.Context(), telegramWebhookConfig("webhook.remove"))
	if err != nil || output["status"] != "removed" || setCalls != 2 || deleteCalls != 1 {
		t.Fatal(output, err, setCalls, deleteCalls)
	}
}
func TestTelegramWebhookRefusesExistingOtherBotReceiver(t *testing.T) {
	mutations := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/getMe"):
			_, _ = io.WriteString(w, `{"ok":true,"result":{"id":123456789,"is_bot":true,"username":"opensealbot"}}`)
		case strings.HasSuffix(r.URL.Path, "/getWebhookInfo"):
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "result": map[string]interface{}{"url": "https://other.example/" + testTelegramBotToken, "pending_update_count": 8, "last_error_message": "failed token " + testTelegramBotToken, "last_error_date": 123}})
		default:
			mutations++
		}
	}))
	defer server.Close()
	adapter := newTelegramConversationAdapter(server.URL+"/bot", server.Client())
	for _, operation := range []string{"webhook.configure", "webhook.status", "webhook.remove"} {
		output, err := adapter.ingress(t.Context(), telegramWebhookConfig(operation))
		encoded, _ := json.Marshal(output)
		if err != nil || output["status"] != "conflict" || output["errorCode"] != "existing_webhook" || mutations != 0 || strings.Contains(string(encoded), testTelegramBotToken) {
			t.Fatal(output, err, mutations)
		}
	}
}
func TestTelegramWebhookRequiresVerifiedBotAndPublicHTTPSGateway(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = io.WriteString(w, `{"ok":true,"result":{"id":123456789,"is_bot":true,"username":"opensealbot"}}`)
	}))
	defer server.Close()
	adapter := newTelegramConversationAdapter(server.URL+"/bot", server.Client())
	for _, target := range []string{"http://public.example/webhook", "https://localhost/webhook", "https://127.0.0.1/webhook", "https://10.0.0.1/webhook", "https://public.example:1234/webhook", "https://user:password@public.example/webhook", "https://public.example/webhook?token=secret", "https://public.example/webhook#fragment"} {
		config := telegramWebhookConfig("webhook.configure")
		config[telegramAdapterEnvelope].(*telegramAdapterRequest).Webhook.URL = target
		if _, err := adapter.ingress(t.Context(), config); err == nil || calls != 0 {
			t.Fatal("invalid gateway reached provider", target, err)
		}
	}
	config := telegramWebhookConfig("webhook.configure")
	config[telegramAdapterEnvelope].(*telegramAdapterRequest).Webhook.InstallationID = "99"
	config[telegramAdapterEnvelope].(*telegramAdapterRequest).Gateway.InstallationID = "99"
	config[telegramAdapterEnvelope].(*telegramAdapterRequest).Gateway.ApplicationID = "99"
	output, err := adapter.ingress(t.Context(), config)
	if err != nil || output["status"] != "conflict" || output["errorCode"] != "bot_installation_mismatch" || calls != 1 {
		t.Fatal(output, err, calls)
	}
}
func TestTelegramWebhookDoesNotClaimSuccessAfterLostConfirmation(t *testing.T) {
	statusCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/getMe"):
			_, _ = io.WriteString(w, `{"ok":true,"result":{"id":123456789,"is_bot":true,"username":"opensealbot"}}`)
		case strings.HasSuffix(r.URL.Path, "/getWebhookInfo"):
			statusCalls++
			if statusCalls == 1 {
				_, _ = io.WriteString(w, `{"ok":true,"result":{"url":""}}`)
			} else {
				w.WriteHeader(500)
				_, _ = io.WriteString(w, `{"ok":false,"error_code":500}`)
			}
		case strings.HasSuffix(r.URL.Path, "/setWebhook"):
			_, _ = io.WriteString(w, `{"ok":true,"result":true}`)
		}
	}))
	defer server.Close()
	output, err := newTelegramConversationAdapter(server.URL+"/bot", server.Client()).ingress(t.Context(), telegramWebhookConfig("webhook.configure"))
	if err != nil || output["status"] != "unavailable" || output["errorCode"] != "webhook_confirmation_unavailable" {
		t.Fatal(output, err)
	}
}
func TestTelegramChatScopedCanonicalMessageIDsAvoidCollision(t *testing.T) {
	var ids []string
	for _, chat := range []int64{99, 100} {
		event, ok := normalizeTelegramUpdate(telegramUpdate{UpdateID: chat, Message: telegramMessage{MessageID: 7, Text: "hello", From: telegramUser{ID: 99}, Chat: telegramChat{ID: chat, Type: "private"}}}, &telegramConversationEndpoint{Provider: "telegram", Address: "*"})
		if !ok {
			t.Fatal(event)
		}
		ids = append(ids, event.ExternalMessageID)
		context := telegramReadContext(&telegramAdapterRequest{Endpoint: &telegramConversationEndpoint{Provider: "telegram", Address: "*"}, Event: &event})
		if context["source"].(map[string]interface{})["messageId"] != "7" {
			t.Fatal(context)
		}
	}
	if ids[0] == ids[1] || ids[0] != "99:7" || ids[1] != "100:7" {
		t.Fatal(ids)
	}
}
func TestTelegramWebhookConfigurePreservesConfirmedDeliveryFailure(t *testing.T) {
	checks := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/getMe"):
			_, _ = io.WriteString(w, `{"ok":true,"result":{"id":123456789,"is_bot":true,"username":"opensealbot"}}`)
		case strings.HasSuffix(r.URL.Path, "/getWebhookInfo"):
			checks++
			result := map[string]interface{}{"url": ""}
			if checks > 1 {
				result["url"] = testTelegramWebhookURL
				result["last_error_date"] = 123
				result["last_error_message"] = "Failed HTTPS response " + testTelegramBotToken
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "result": result})
		case strings.HasSuffix(r.URL.Path, "/setWebhook"):
			_, _ = io.WriteString(w, `{"ok":true,"result":true}`)
		}
	}))
	defer server.Close()
	output, err := newTelegramConversationAdapter(server.URL+"/bot", server.Client()).ingress(t.Context(), telegramWebhookConfig("webhook.configure"))
	encoded, _ := json.Marshal(output)
	if err != nil || output["status"] != "configured" || output["lastErrorDate"] != int64(123) || output["lastErrorMessage"] == nil || strings.Contains(string(encoded), testTelegramBotToken) {
		t.Fatal(output, err)
	}
}
