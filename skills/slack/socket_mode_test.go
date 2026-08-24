package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestSlackSocketModeConnectorForwardsSignedInteractionAndAcknowledgesResponse(t *testing.T) {
	fixedNow := time.Date(2026, 8, 24, 5, 0, 0, 0, time.UTC)
	const signingSecret = "socket-signing-secret"
	payload := json.RawMessage(`{"type":"block_actions","team":{"id":"T1"},"user":{"id":"U1"},"actions":[{"action_id":"openseal_approval_approve"}]}`)

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
			AppToken: "xapp-reviewed", SigningSecret: signingSecret,
			CallbackURL: callback.URL, APIBaseURL: slack.URL,
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
		SigningSecret: "secret", CallbackURL: callback.URL,
		HTTPClient: callback.Client(), Now: time.Now,
	}, slackSocketModeEnvelope{EnvelopeID: "envelope", Payload: json.RawMessage(`{"type":"block_actions"}`)})
	if err == nil || result.EnvelopeID != "" {
		t.Fatalf("rejected ingress result = %#v, %v", result, err)
	}
}
