package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/axiom-studio/skills.sdk/executor"
)

func TestTelegramSharedWorkerSeparatesConcurrentAccounts(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	arrived := make(chan string, 2)
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		botID := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/bot"), ":account-token/getMe")
		if botID != "101" && botID != "202" {
			t.Errorf("unexpected identity request path %q", r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		arrived <- botID
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		_, _ = fmt.Fprintf(w, `{"ok":true,"result":{"id":%s,"is_bot":true,"username":"bot%s"}}`, botID, botID)
	}))
	defer server.Close()
	shared := &telegramIngressExecutor{adapter: newTelegramConversationAdapter(server.URL+"/bot", server.Client())}
	errors := make(chan error, 2)
	for _, account := range []struct{ tenant, bot string }{{"11", "101"}, {"22", "202"}} {
		go func() {
			token := account.bot + ":account-token"
			config := telegramIngressConfig(`{"update_id":42,"message":{"message_id":7,"date":1720000000,"text":"hello","from":{"id":99},"chat":{"id":99,"type":"private"}}}`, nil)
			delete(config, telegramCredentialKey)
			request := config[telegramAdapterEnvelope].(*telegramAdapterRequest)
			request.Operation = "gateway_ingress"
			request.Gateway = &telegramIngressGateway{Scope: telegramConversationScope{Kind: "tenant", ID: account.tenant}, Provider: "telegram", InstallationID: account.bot}
			request.Request.Scope = request.Gateway.Scope
			request.Request.Headers[telegramSecretHeader] = []string{telegramWebhookSecret(token)}
			result, err := shared.Execute(ctx, &executor.StepDefinition{Config: config}, telegramBindingResolver{bindings: map[string]interface{}{telegramCredentialKey: token}})
			if err != nil {
				errors <- err
				return
			}
			events, ok := result.Output["events"].([]telegramGatewayEvent)
			if result.Output["statusCode"] != http.StatusOK || !ok || len(events) != 1 || events[0].InstallationID != account.bot || events[0].Event.Attributes["botId"] != account.bot {
				errors <- fmt.Errorf("tenant %s received another account's event: %#v", account.tenant, result.Output)
				return
			}
			if _, leaked := config[telegramCredentialKey]; leaked {
				errors <- fmt.Errorf("tenant %s credential entered ordinary config", account.tenant)
				return
			}
			errors <- nil
		}()
	}
	seen := make(map[string]bool)
	for len(seen) < 2 {
		select {
		case botID := <-arrived:
			seen[botID] = true
		case <-ctx.Done():
			unblock()
			t.Fatal("one account's identity request blocked the other account")
		}
	}
	unblock()
	for range 2 {
		if err := <-errors; err != nil {
			t.Error(err)
		}
	}
	config := telegramIngressConfig(`{"update_id":43}`, &telegramConversationEndpoint{Provider: "telegram", Address: "99"})
	delete(config, telegramCredentialKey)
	result, err := shared.Execute(ctx, &executor.StepDefinition{Config: config}, telegramBindingResolver{})
	if err != nil || result.Output["statusCode"] != http.StatusUnauthorized {
		t.Fatalf("missing credentials reused a previous account: %#v, %v", result, err)
	}
}

func TestTelegramActionUsesReviewedCredentialOverConfig(t *testing.T) {
	previousClient, previousBase := httpClient, telegramAPIBaseOverride
	t.Cleanup(func() { httpClient = previousClient; telegramAPIBaseOverride = previousBase })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/bot101:reviewed/sendMessage" {
			t.Errorf("ordinary action config changed the reviewed account")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var message map[string]interface{}
		if json.NewDecoder(r.Body).Decode(&message) != nil || message["chat_id"] != "99" {
			t.Error("unexpected message destination")
		}
		_, _ = fmt.Fprint(w, `{"ok":true,"result":{"message_id":7,"chat":{"id":99}}}`)
	}))
	defer server.Close()
	httpClient, telegramAPIBaseOverride = server.Client(), server.URL+"/bot"
	config := map[string]interface{}{"botToken": "202:unreviewed", "chatId": "99", "text": "hello"}
	result, err := (&SendMessageExecutor{}).Execute(t.Context(), &executor.StepDefinition{Config: config}, telegramLiteralResolver{telegramBindingResolver{bindings: map[string]interface{}{telegramCredentialKey: "101:reviewed"}}})
	if err != nil || result == nil {
		t.Fatalf("reviewed send failed: %#v, %v", result, err)
	}
	if config["botToken"] != "202:unreviewed" {
		t.Fatal("execution copied its governed credential into ordinary config")
	}
}
