package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

type slackSocketTestTransport func(*http.Request) (*http.Response, error)

func (transport slackSocketTestTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func TestSlackSocketCallbackFailurePreservesCancellationWithoutPrivateURL(t *testing.T) {
	const privateURL = "https://callback.invalid/private-ticket"
	for _, cause := range []error{context.DeadlineExceeded, context.Canceled, errors.New("private transport details")} {
		t.Run(cause.Error(), func(t *testing.T) {
			config := slackSocketModeConfig{
				SigningSecret: "signing-secret", Now: time.Now,
				HTTPClient: &http.Client{Transport: slackSocketTestTransport(func(*http.Request) (*http.Response, error) {
					return nil, &url.Error{Op: "Post", URL: privateURL, Err: cause}
				})},
			}
			_, err := forwardSlackSocketPayload(t.Context(), config, privateURL, "application/json", []byte(`{}`))
			if err == nil || strings.Contains(err.Error(), privateURL) || strings.Contains(err.Error(), "private transport details") {
				t.Fatalf("callback failure leaked private transport details: %v", err)
			}
			if (cause == context.DeadlineExceeded || cause == context.Canceled) && !errors.Is(err, cause) {
				t.Fatalf("callback cancellation cause was lost: %v", err)
			}
		})
	}
}

func TestSlackSocketModeConnectorForwardsSignedInteractionAndAcknowledgesResponse(t *testing.T) {
	fixedNow := time.Date(2026, 8, 24, 5, 0, 0, 0, time.UTC)
	const signingSecret = "socket-signing-secret"
	payload := json.RawMessage(`{"type":"block_actions","team":{"id":"T1"},"user":{"id":"U1"},"actions":[{"action_id":"openseal_approval_approve","value":"{\"destinationId\":\"endpoint-1\"}"}]}`)

	callbackCalled := make(chan struct{}, 1)
	callback := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Error(err)
			response.WriteHeader(http.StatusBadRequest)
			return
		}
		timestamp := request.Header.Get("X-Slack-Request-Timestamp")
		mac := hmac.New(sha256.New, []byte(signingSecret))
		_, _ = mac.Write([]byte("v0:" + timestamp + ":" + string(body)))
		wantSignature := "v0=" + hex.EncodeToString(mac.Sum(nil))
		if timestamp != "1787547600" || !hmac.Equal([]byte(request.Header.Get("X-Slack-Signature")), []byte(wantSignature)) {
			t.Errorf("invalid forwarded signature metadata")
		}
		values, err := url.ParseQuery(string(body))
		if err != nil || values.Get("payload") != string(payload) {
			t.Errorf("forwarded body = %q, %v", string(body), err)
		}
		callbackCalled <- struct{}{}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"replace_original":true}`))
	}))
	defer callback.Close()

	acknowledgement := make(chan slackSocketModeAcknowledgement, 1)
	upgrader := websocket.Upgrader{}
	var socketURL string
	slack := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/apps.connections.open":
			if request.Header.Get("Authorization") != "Bearer xapp-reviewed" {
				t.Errorf("unexpected app authorization")
			}
			response.Header().Set("Content-Type", "application/json")
			_, _ = response.Write([]byte(`{"ok":true,"url":"` + socketURL + `"}`))
		case "/socket":
			connection, err := upgrader.Upgrade(response, request, nil)
			if err != nil {
				t.Error(err)
				return
			}
			defer connection.Close()
			if err := connection.WriteJSON(slackSocketModeEnvelope{
				EnvelopeID: "envelope-1", Type: "interactive", AcceptsResponsePayload: true, Payload: payload,
			}); err != nil {
				t.Error(err)
				return
			}
			var value slackSocketModeAcknowledgement
			if err := connection.ReadJSON(&value); err != nil {
				t.Error(err)
				return
			}
			acknowledgement <- value
		default:
			response.WriteHeader(http.StatusNotFound)
		}
	}))
	defer slack.Close()
	socketURL = "ws" + strings.TrimPrefix(slack.URL, "http") + "/socket"

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- runSlackSocketModeConnector(ctx, slackSocketModeConfig{
			AppToken: "xapp-reviewed", SigningSecret: signingSecret, BotToken: "xoxb-reviewed",
			CallbackRoutes: map[string]string{"endpoint-1": callback.URL}, APIBaseURL: slack.URL,
			HTTPClient: slack.Client(), Dialer: websocket.DefaultDialer, Now: func() time.Time { return fixedNow },
		})
	}()

	select {
	case <-callbackCalled:
	case <-time.After(3 * time.Second):
		t.Fatal("signed callback was not forwarded")
	}
	select {
	case value := <-acknowledgement:
		if value.EnvelopeID != "envelope-1" || string(value.Payload) != `{"replace_original":true}` {
			t.Fatalf("acknowledgement = %#v", value)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Socket Mode envelope was not acknowledged")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("connector did not stop with its context")
	}
}

func TestSlackSocketModeConnectorDoesNotAcknowledgeRejectedIngress(t *testing.T) {
	callback := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusBadGateway)
	}))
	defer callback.Close()
	result, err := forwardSlackSocketInteraction(context.Background(), slackSocketModeConfig{
		SigningSecret: "secret", CallbackRoutes: map[string]string{"endpoint-1": callback.URL},
		HTTPClient: callback.Client(), Now: time.Now,
	}, slackSocketModeEnvelope{EnvelopeID: "envelope", Payload: json.RawMessage(`{"type":"block_actions","actions":[{"value":"{\"destinationId\":\"endpoint-1\"}"}]}`)})
	if err == nil || result.EnvelopeID != "" {
		t.Fatalf("rejected ingress result = %#v, %v", result, err)
	}
}

func TestSlackSocketCallbackURLRoutesExactDestinationAndBoundsLegacyFallback(t *testing.T) {
	routes := map[string]string{"endpoint-1": "http://sentinel/one", "endpoint-2": "http://sentinel/two"}
	payload := json.RawMessage(`{"type":"block_actions","actions":[{"value":"{\"destinationId\":\"endpoint-2\"}"}]}`)
	if callbackURL, err := slackSocketCallbackURL(payload, routes); err != nil || callbackURL != routes["endpoint-2"] {
		t.Fatalf("callback route = %q, %v", callbackURL, err)
	}
	legacy := json.RawMessage(`{"type":"block_actions","actions":[{"value":"{}"}]}`)
	if _, err := slackSocketCallbackURL(legacy, routes); err == nil {
		t.Fatal("ambiguous legacy interaction was accepted")
	}
	if callbackURL, err := slackSocketCallbackURL(legacy, map[string]string{"only": "http://sentinel/only"}); err != nil || callbackURL != "http://sentinel/only" {
		t.Fatalf("bounded legacy callback route = %q, %v", callbackURL, err)
	}
	if callbackURL, err := slackSocketCallbackURL(legacy, map[string]string{
		"only": "http://sentinel/only", "conversation_gateway:shared": "http://sentinel/gateway",
	}); err != nil || callbackURL != "http://sentinel/only" {
		t.Fatalf("gateway route affected bounded legacy callback = %q, %v", callbackURL, err)
	}
}

func TestSlackSocketModeForwardsEventsAPIToConversationGateway(t *testing.T) {
	fixedNow := time.Date(2026, 8, 24, 5, 0, 0, 0, time.UTC)
	const signingSecret = "socket-signing-secret"
	payload := json.RawMessage(`{"type":"event_callback","team_id":"T1","api_app_id":"A1","event_id":"Ev1","event":{"type":"app_mention","user":"U1","channel":"C1","ts":"1.1","text":"<@B1> help"}}`)

	called := 0
	statusCalled := 0
	gateway := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/assistant.threads.setStatus" {
			statusCalled++
			if request.Header.Get("Authorization") != "Bearer xoxb-reviewed" {
				t.Error("assistant status omitted the bound bot token")
			}
			response.Header().Set("Content-Type", "application/json")
			_, _ = response.Write([]byte(`{"ok":true}`))
			return
		}
		called++
		body, _ := io.ReadAll(request.Body)
		if request.Header.Get("Content-Type") != "application/json" || string(body) != string(payload) {
			t.Errorf("gateway request = %s %q", request.Header.Get("Content-Type"), string(body))
		}
		timestamp := request.Header.Get("X-Slack-Request-Timestamp")
		mac := hmac.New(sha256.New, []byte(signingSecret))
		_, _ = mac.Write([]byte("v0:" + timestamp + ":" + string(body)))
		if request.Header.Get("X-Slack-Signature") != "v0="+hex.EncodeToString(mac.Sum(nil)) {
			t.Error("gateway signature was not regenerated")
		}
		response.WriteHeader(http.StatusOK)
	}))
	defer gateway.Close()

	err := forwardSlackSocketEvents(context.Background(), slackSocketModeConfig{
		SigningSecret: signingSecret, BotToken: "xoxb-reviewed", APIBaseURL: gateway.URL,
		HTTPClient: gateway.Client(), Now: func() time.Time { return fixedNow },
		CallbackRoutes: map[string]string{"approval": "http://sentinel/approval", "conversation_gateway:shared": gateway.URL},
	}, slackSocketModeEnvelope{EnvelopeID: "events-1", Type: "events_api", Payload: payload})
	if err != nil || called != 1 || statusCalled != 1 {
		t.Fatalf("forward events called=%d status=%d err=%v", called, statusCalled, err)
	}
}

func TestSlackWebReviewNavigationAcknowledgesWithoutCallback(t *testing.T) {
	callback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("navigation must not invoke approval or conversation ingress")
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer callback.Close()
	for _, routes := range []map[string]string{nil, {"one": callback.URL}, {"one": callback.URL, "two": callback.URL}} {
		result, err := forwardSlackSocketInteraction(t.Context(), slackSocketModeConfig{CallbackRoutes: routes}, slackSocketModeEnvelope{
			EnvelopeID: "web-review", AcceptsResponsePayload: true,
			Payload: json.RawMessage(`{"type":"block_actions","actions":[{"action_id":"openseal_review_web_open"}]}`),
		})
		if err != nil || result.EnvelopeID != "web-review" || len(result.Payload) != 0 {
			t.Fatalf("navigation acknowledgement = %#v, %v", result, err)
		}
	}
}
func TestSlackWebReviewFastAckDoesNotAcceptDecisionsOrMalformedActions(t *testing.T) {
	for _, body := range []string{
		`{"type":"block_actions","actions":[{"action_id":"openseal_approval_approve"}]}`,
		`{"type":"view_submission","actions":[{"action_id":"openseal_review_web_open"}]}`,
		`{"type":"block_actions","actions":[{"action_id":"openseal_review_web_open"},{"action_id":"openseal_approval_approve"}]}`,
		`{"type":"block_actions","actions":[{"action_id":"unknown"}]}`,
	} {
		result, err := forwardSlackSocketInteraction(t.Context(), slackSocketModeConfig{}, slackSocketModeEnvelope{EnvelopeID: "bad", Payload: json.RawMessage(body)})
		if err == nil || result.EnvelopeID != "" {
			t.Fatalf("non-navigation bypassed decision routing: %s", body)
		}
	}
}

func TestSlackOriginReviewUsesConversationRouteWithoutBorrowingPolicyCallback(t *testing.T) {
	routes := map[string]string{"endpoint": "http://sentinel/policy", "conversation_endpoint:endpoint": "http://sentinel/origin"}
	payload := json.RawMessage(`{"type":"block_actions","actions":[{"value":"{\"destinationId\":\"endpoint\",\"originReview\":true}"}]}`)
	if route, err := slackSocketCallbackURL(payload, routes); err != nil || route != routes["conversation_endpoint:endpoint"] {
		t.Fatalf("origin route %q %v", route, err)
	}
	delete(routes, "conversation_endpoint:endpoint")
	if _, err := slackSocketCallbackURL(payload, routes); err == nil {
		t.Fatal("origin approval borrowed policy callback")
	}
}
