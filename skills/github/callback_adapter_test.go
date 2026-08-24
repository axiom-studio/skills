package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"testing"
)

func TestGitHubCallbackVerifiesAndNormalizesPullRequest(t *testing.T) {
	body := []byte(`{"action":"opened","number":17,"repository":{"name":"cortex","full_name":"axiom-studio/cortex","owner":{"login":"axiom-studio"}},"pull_request":{"number":17,"title":"Fix runner","html_url":"https://github.com/axiom-studio/cortex/pull/17","head":{"ref":"fix","sha":"0123456789012345678901234567890123456789"},"base":{"ref":"main","sha":"1123456789012345678901234567890123456789"},"updated_at":"2026-08-24T12:00:00Z"},"sender":{"login":"kev"}}`)
	secret := "review-hook"
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	signature := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	output, err := normalizeGitHubCallback(map[string]interface{}{
		githubWebhookSecretKey: secret,
		githubCallbackEnvelope: map[string]interface{}{
			"operation":    "callback",
			"registration": map[string]interface{}{"id": "review", "provider": "github", "configuration": map[string]interface{}{"owner": "axiom-studio", "repository": "cortex"}},
			"request": map[string]interface{}{"method": http.MethodPost, "headers": map[string][]string{
				"X-Hub-Signature-256": {signature}, "X-GitHub-Event": {"pull_request"}, "X-GitHub-Delivery": {"delivery-1"},
			}, "body": body},
		},
	})
	if err != nil || output["statusCode"] != http.StatusAccepted {
		t.Fatalf("output=%#v err=%v", output, err)
	}
	events, ok := output["events"].([]githubNormalizedCallbackEvent)
	if !ok || len(events) != 1 || events[0].Type != "github.pull_request.opened" || events[0].Subject != "axiom-studio/cortex#17" {
		t.Fatalf("events=%#v", output["events"])
	}
}

func TestGitHubCallbackRejectsInvalidSignatureAndFiltersRepository(t *testing.T) {
	body := []byte(`{"action":"opened","number":1,"repository":{"name":"other","owner":{"login":"axiom-studio"}},"pull_request":{}}`)
	request := func(signature, secret string) map[string]interface{} {
		return map[string]interface{}{
			githubWebhookSecretKey: secret,
			githubCallbackEnvelope: map[string]interface{}{
				"operation":    "callback",
				"registration": map[string]interface{}{"id": "review", "provider": "github", "configuration": map[string]interface{}{"repository": "cortex"}},
				"request":      map[string]interface{}{"method": http.MethodPost, "headers": map[string][]string{"X-Hub-Signature-256": {signature}, "X-GitHub-Event": {"pull_request"}}, "body": body},
			},
		}
	}
	invalid, err := normalizeGitHubCallback(request("sha256=00", "secret"))
	if err != nil || invalid["statusCode"] != http.StatusUnauthorized {
		t.Fatalf("invalid=%#v err=%v", invalid, err)
	}
	mac := hmac.New(sha256.New, []byte("secret"))
	_, _ = mac.Write(body)
	filtered, err := normalizeGitHubCallback(request("sha256="+hex.EncodeToString(mac.Sum(nil)), "secret"))
	if err != nil || filtered["statusCode"] != http.StatusAccepted {
		t.Fatalf("filtered=%#v err=%v", filtered, err)
	}
	if events, ok := filtered["events"].([]githubNormalizedCallbackEvent); !ok || len(events) != 0 {
		t.Fatalf("filtered events=%#v", filtered["events"])
	}
}
