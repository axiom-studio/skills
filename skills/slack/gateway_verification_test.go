package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/axiom-studio/skills.sdk/executor"
)

func TestSlackGatewayVerificationOnlyAnswersAuthenticatedURLChallenges(t *testing.T) {
	now := time.Unix(1_720_000_000, 0).UTC()
	var providerCalls atomic.Int64
	provider := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		providerCalls.Add(1)
		response.WriteHeader(http.StatusInternalServerError)
	}))
	defer provider.Close()
	adapter := newSlackAdapter("", provider.URL, provider.Client())
	adapter.now = func() time.Time { return now }
	ingress := &slackIngressExecutor{adapter: adapter}
	challenge := `{"type":"url_verification","challenge":"verify-me"}`
	interaction := url.Values{"payload": {`{"type":"block_actions","actions":[{"action_id":"openseal_review_web_open"}]}`}}.Encode()
	for _, test := range []struct {
		name           string
		body           string
		contentType    string
		timeOffset     time.Duration
		wrongSignature bool
		missingSecret  bool
		status         int
	}{
		{name: "challenge", body: challenge, status: http.StatusOK},
		{name: "empty challenge", body: `{"type":"url_verification","challenge":""}`, status: http.StatusBadRequest},
		{name: "blank challenge", body: `{"type":"url_verification","challenge":"  "}`, status: http.StatusBadRequest},
		{name: "missing challenge", body: `{"type":"url_verification"}`, status: http.StatusBadRequest},
		{name: "message", body: `{"type":"event_callback","team_id":"T123","event_id":"Ev1","event":{"type":"message","user":"U1","text":"hello","channel":"C123","ts":"1720000000.1"}}`, status: http.StatusBadRequest},
		{name: "unknown event", body: `{"type":"app_rate_limited"}`, status: http.StatusBadRequest},
		{name: "interaction", body: interaction, contentType: "application/x-www-form-urlencoded", status: http.StatusBadRequest},
		{name: "challenge as form", body: challenge, contentType: "application/x-www-form-urlencoded", status: http.StatusBadRequest},
		{name: "malformed JSON", body: `{`, status: http.StatusBadRequest},
		{name: "signature checked before payload", body: `{`, wrongSignature: true, status: http.StatusUnauthorized},
		{name: "wrong signature", body: challenge, wrongSignature: true, status: http.StatusUnauthorized},
		{name: "replayed challenge", body: challenge, timeOffset: -10 * time.Minute, status: http.StatusUnauthorized},
		{name: "future challenge", body: challenge, timeOffset: 10 * time.Minute, status: http.StatusUnauthorized},
		{name: "missing credential", body: challenge, missingSecret: true, status: http.StatusUnauthorized},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := ingressConfig(now.Add(test.timeOffset), []byte(test.body), &conversationEndpoint{})
			envelope := config[adapterEnvelopeKey].(map[string]interface{})
			envelope["operation"] = "gateway_verification"
			envelope["gateway"] = &conversationIngressGateway{Provider: "slack"}
			delete(envelope, "endpoint")
			request := envelope["request"].(*conversationIngressRequest)
			contentType := test.contentType
			if contentType == "" {
				contentType = "application/json"
			}
			request.Headers["Content-Type"] = []string{contentType}
			if test.wrongSignature {
				request.Headers["X-Slack-Signature"] = []string{signedSlackRequest("wrong-secret", now.Unix(), request.Body)}
			}
			bindings := map[string]interface{}{}
			if !test.missingSecret {
				bindings[slackSigningSecretKey] = "signing-secret"
			}
			result, err := ingress.Execute(t.Context(), &executor.StepDefinition{Config: config}, slackBindingResolver{bindings: bindings})
			if err != nil || result == nil || result.Output["statusCode"] != test.status {
				t.Fatalf("verification = %#v, %v; want HTTP %d", result, err, test.status)
			}
			encoded, err := json.Marshal(result.Output["events"])
			var events []interface{}
			if err != nil || json.Unmarshal(encoded, &events) != nil || len(events) != 0 {
				t.Fatalf("verification emitted events: %s, %v", encoded, err)
			}
			if test.status == http.StatusOK {
				body, ok := result.Output["body"].(string)
				var response map[string]string
				if !ok || result.Output["contentType"] != "application/json" || json.Unmarshal([]byte(body), &response) != nil || response["challenge"] != "verify-me" {
					t.Fatalf("verification response = %#v", result.Output)
				}
			}
			if _, leaked := config[slackSigningSecretKey]; leaked {
				t.Fatal("verification persisted its signing credential")
			}
		})
	}
	if count := providerCalls.Load(); count != 0 {
		t.Fatalf("verification made %d provider calls", count)
	}
}
