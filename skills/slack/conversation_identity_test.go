package main

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type identityTransport func(*http.Request) (*http.Response, error)

func (f identityTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSlackContextIncludesSenderAndChannelIdentity(t *testing.T) {
	for _, missingScopes := range []bool{false, true} {
		t.Run(map[bool]string{false: "names", true: "missing scopes"}[missingScopes], func(t *testing.T) {
			client := &http.Client{Transport: identityTransport(func(r *http.Request) (*http.Response, error) {
				var body string
				if r.Header.Get("Authorization") != "Bearer xoxb-context-token" {
					t.Error("wrong credential")
				}
				if missingScopes {
					return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"ok":false,"error":"missing_scope"}`)), Header: make(http.Header)}, nil
				}
				switch r.URL.Path {
				case "/users.info":
					if r.URL.Query().Get("user") != "U-kev" {
						t.Error("wrong sender lookup")
					}
					body = `{"ok":true,"user":{"id":"U-kev","profile":{"display_name":"Kev","email":"private@example.com"}}}`
				case "/conversations.info":
					if r.URL.Query().Get("channel") != "C123" {
						t.Error("wrong channel lookup")
					}
					body = `{"ok":true,"channel":{"id":"C123","name":"engineering","is_private":true}}`
				case "/conversations.replies":
					body = `{"ok":true,"messages":[]}`
				default:
					t.Errorf("unexpected API %s", r.URL.Path)
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
			})}
			config := slackContextConfig()
			event := config[adapterEnvelopeKey].(map[string]interface{})["event"].(*normalizedConversationEvent)
			event.ExternalParticipantID = "U-kev"
			event.Attributes = map[string]interface{}{"teamId": "T123"}
			output, err := newSlackAdapter("", "https://slack.test", client).delivery(t.Context(), config)
			if err != nil {
				t.Fatal(err)
			}
			encoded, _ := json.Marshal(output["source"])
			var source slackMessageSource
			if err := json.Unmarshal(encoded, &source); err != nil {
				t.Fatal(err)
			}
			if source.ParticipantID != "U-kev" || source.ChannelID != "C123" || source.WorkspaceID != "T123" || source.ThreadID != event.ExternalThreadID {
				t.Fatalf("IDs missing: %#v", source)
			}
			if !missingScopes && (source.ParticipantDisplayName != "Kev" || source.ChannelName != "engineering" || source.ChannelType != "private_channel") {
				t.Fatalf("labels missing: %#v", source)
			}
			if missingScopes && (source.ParticipantDisplayName != "" || source.ChannelName != "") {
				t.Fatal("guessed names")
			}
			if strings.Contains(string(encoded), "private@example.com") {
				t.Fatal("email leaked")
			}
		})
	}
}
