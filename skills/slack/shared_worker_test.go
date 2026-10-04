package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/axiom-studio/skills.sdk/executor"
)

func TestSlackSharedWorkerSeparatesConcurrentAccountCredentials(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		channel, _ := body["channel"].(string)
		if (channel != "C101" && channel != "C202") || r.Header.Get("Authorization") != "Bearer xoxb-"+channel {
			t.Error("shared worker mixed account credential and destination")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = fmt.Fprintf(w, `{"ok":true,"channel":%q,"ts":"1720000001.123"}`, channel)
	}))
	defer server.Close()
	shared := &slackDeliveryExecutor{adapter: newSlackAdapter("", server.URL, server.Client())}
	var requests sync.WaitGroup
	for _, channel := range []string{"C101", "C202"} {
		requests.Add(1)
		go func() {
			defer requests.Done()
			for range 8 {
				config := deliveryConfig("deliver")
				delete(config, slackConnectionKey)
				config[adapterEnvelopeKey].(map[string]interface{})["endpoint"].(*conversationEndpoint).Address = channel
				result, err := shared.Execute(t.Context(), &executor.StepDefinition{Config: config}, slackBindingResolver{bindings: map[string]interface{}{slackConnectionKey: "xoxb-" + channel}})
				if err != nil || result.Output["outcome"] != "delivered" {
					t.Errorf("shared account delivery = %#v, %v", result, err)
					return
				}
				if _, leaked := config[slackConnectionKey]; leaked {
					t.Error("ephemeral credential entered ordinary config")
				}
			}
		}()
	}
	requests.Wait()
	config := deliveryConfig("deliver")
	delete(config, slackConnectionKey)
	result, err := shared.Execute(t.Context(), &executor.StepDefinition{Config: config}, slackBindingResolver{})
	if err == nil && result.Output["outcome"] == "delivered" {
		t.Fatal("missing credentials reused a previous account")
	}
}
