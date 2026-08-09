package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/axiom-studio/skills.sdk/executor"
)

const testTelegramBotToken = "123456789:test-token"

type telegramBindingResolver struct {
	executor.TemplateResolver
	bindings map[string]interface{}
}

func (r telegramBindingResolver) GetBinding(name string) interface{}  { return r.bindings[name] }
func (r telegramBindingResolver) GetBindings() map[string]interface{} { return r.bindings }

func TestTelegramIngressVerifiesWebhookAndNormalizesDirectMessage(t *testing.T) {
	adapter := newTelegramConversationAdapter("", nil)
	config := telegramIngressConfig(`{
		"update_id":42,
		"message":{"message_id":7,"date":1720000000,"text":"hello",
			"from":{"id":99,"is_bot":false,"username":"operator"},
			"chat":{"id":99,"type":"private","username":"operator"}}
	}`, &telegramConversationEndpoint{ID: "endpoint", Provider: "telegram", Address: "99"})

	output, err := adapter.ingress(context.Background(), config)
	if err != nil || output["statusCode"] != http.StatusOK {
		t.Fatalf("ingress = %#v, %v", output, err)
	}
	encoded, _ := json.Marshal(output["events"])
	var events []telegramNormalizedEvent
	if err := json.Unmarshal(encoded, &events); err != nil || len(events) != 1 {
		t.Fatalf("events = %s, %v", encoded, err)
	}
	event := events[0]
	if event.ID != "telegram:update:42" || event.ExternalConversationID != "99" ||
		event.ExternalThreadID != "7" || event.ExternalMessageID != "7" ||
		event.ExternalParticipantID != "99" || event.Text != "hello" ||
		!event.Direct || !event.MentionsEndpoint || event.ParticipantIsBot ||
		event.OccurredAt.Unix() != 1720000000 {
		t.Fatalf("normalized event = %#v", event)
	}
}

func TestTelegramIngressRejectsWrongSecretAndEndpoint(t *testing.T) {
	adapter := newTelegramConversationAdapter("", nil)
	config := telegramIngressConfig(`{"update_id":42,"message":{"message_id":7,"date":1720000000,"text":"hello","from":{"id":99},"chat":{"id":99,"type":"private"}}}`, &telegramConversationEndpoint{ID: "endpoint", Provider: "telegram", Address: "99"})
	request := config[telegramAdapterEnvelope].(*telegramAdapterRequest)
	request.Request.Headers[telegramSecretHeader] = []string{"wrong"}
	output, err := adapter.ingress(context.Background(), config)
	if err != nil || output["statusCode"] != http.StatusUnauthorized {
		t.Fatalf("wrong secret = %#v, %v", output, err)
	}

	config = telegramIngressConfig(`{"update_id":42,"message":{"message_id":7,"date":1720000000,"text":"hello","from":{"id":99},"chat":{"id":100,"type":"private"}}}`, &telegramConversationEndpoint{ID: "endpoint", Provider: "telegram", Address: "99"})
	output, err = adapter.ingress(context.Background(), config)
	if err != nil || output["statusCode"] != http.StatusOK {
		t.Fatalf("wrong endpoint = %#v, %v", output, err)
	}
	encoded, _ := json.Marshal(output["events"])
	if string(encoded) != "[]" {
		t.Fatalf("wrong endpoint events = %s", encoded)
	}
}

func TestTelegramGatewayIngressReturnsBotInstallationRoute(t *testing.T) {
	adapter := newTelegramConversationAdapter("", nil)
	config := telegramIngressConfig(`{"update_id":42,"message":{"message_id":7,"date":1720000000,"text":"hello","from":{"id":99},"chat":{"id":100,"type":"group","title":"Ops"}}}`, nil)
	request := config[telegramAdapterEnvelope].(*telegramAdapterRequest)
	request.Operation = "gateway_ingress"
	request.Gateway = &telegramIngressGateway{Scope: telegramConversationScope{Kind: "tenant", ID: "1"}, DeploymentID: "telegram-gateway", Provider: "telegram"}

	output, err := adapter.ingress(context.Background(), config)
	if err != nil || output["statusCode"] != http.StatusOK {
		t.Fatalf("gateway ingress = %#v, %v", output, err)
	}
	encoded, _ := json.Marshal(output["events"])
	var events []telegramGatewayEvent
	if err := json.Unmarshal(encoded, &events); err != nil || len(events) != 1 ||
		events[0].InstallationID != "123456789" || events[0].ApplicationID != "123456789" ||
		events[0].Address != "100" || events[0].Event.Text != "hello" {
		t.Fatalf("gateway events = %s, %v", encoded, err)
	}
}

func TestTelegramDeliverySendsAndUpdatesMessages(t *testing.T) {
	var methods []string
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		methods = append(methods, request.URL.Path)
		var body map[string]interface{}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body["chat_id"] != "99" || body["text"] != "answer" {
			t.Errorf("body = %#v", body)
		}
		if strings.HasSuffix(request.URL.Path, "/editMessageText") && body["message_id"] != float64(77) {
			t.Errorf("update body = %#v", body)
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(response, `{"ok":true,"result":{"message_id":77}}`)
	}))
	defer server.Close()
	adapter := newTelegramConversationAdapter(server.URL+"/bot", server.Client())

	delivered, err := adapter.delivery(context.Background(), telegramDeliveryConfig("message.send", ""))
	if err != nil || delivered["outcome"] != "delivered" || delivered["providerMessageId"] != "77" {
		t.Fatalf("delivery = %#v, %v", delivered, err)
	}
	updated, err := adapter.delivery(context.Background(), telegramDeliveryConfig("message.update", "77"))
	if err != nil || updated["outcome"] != "delivered" {
		t.Fatalf("update = %#v, %v", updated, err)
	}
	want := []string{"/bot" + testTelegramBotToken + "/sendMessage", "/bot" + testTelegramBotToken + "/editMessageText"}
	if strings.Join(methods, ",") != strings.Join(want, ",") {
		t.Fatalf("methods = %v, want %v", methods, want)
	}
}

func TestTelegramAdapterExecutorUsesEphemeralVaultBinding(t *testing.T) {
	adapter := newTelegramConversationAdapter("", nil)
	step := &executor.StepDefinition{Config: telegramIngressConfig(`{"update_id":42,"message":{"message_id":7,"date":1720000000,"text":"hello","from":{"id":99},"chat":{"id":99,"type":"private"}}}`, &telegramConversationEndpoint{Provider: "telegram", Address: "99"})}
	delete(step.Config, telegramCredentialKey)

	result, err := (&telegramIngressExecutor{adapter: adapter}).Execute(t.Context(), step, telegramBindingResolver{bindings: map[string]interface{}{telegramCredentialKey: testTelegramBotToken}})
	if err != nil || result.Output["statusCode"] != http.StatusOK {
		t.Fatalf("executor ingress = %#v, %v", result, err)
	}
	if _, leaked := step.Config[telegramCredentialKey]; leaked {
		t.Fatal("ephemeral Telegram credential leaked into durable step config")
	}
}

func TestTelegramWebhookSecretIsStableAndHeaderSafe(t *testing.T) {
	secret := telegramWebhookSecret(testTelegramBotToken)
	if len(secret) != 64 || secret != telegramWebhookSecret(testTelegramBotToken) || strings.ContainsAny(secret, ":/ ") {
		t.Fatalf("webhook secret = %q", secret)
	}
}

func telegramIngressConfig(body string, endpoint *telegramConversationEndpoint) map[string]interface{} {
	return map[string]interface{}{
		telegramCredentialKey: testTelegramBotToken,
		telegramAdapterEnvelope: &telegramAdapterRequest{
			Operation: "ingress", Endpoint: endpoint,
			Request: &telegramIngressRequest{
				Method: http.MethodPost, Body: []byte(body),
				Headers: map[string][]string{telegramSecretHeader: {telegramWebhookSecret(testTelegramBotToken)}},
			},
		},
	}
}

func telegramDeliveryConfig(operation, providerMessageID string) map[string]interface{} {
	return map[string]interface{}{
		telegramCredentialKey: testTelegramBotToken,
		telegramAdapterEnvelope: &telegramAdapterRequest{
			Operation: "deliver", Endpoint: &telegramConversationEndpoint{Provider: "telegram", Address: "99"},
			Delivery: &telegramConversationDelivery{ID: "delivery-1", Operation: operation, ExternalThreadID: "7", ProviderMessageID: providerMessageID},
			Message:  &telegramConversationMessage{ID: "message-1", Content: "answer", CreatedAt: time.Unix(1720000001, 0).UTC()},
		},
	}
}
