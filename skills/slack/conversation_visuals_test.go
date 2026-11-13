package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// visualDeliveryConfig is a delivery whose envelope carries the host's
// visuals field in its wire form.
func visualDeliveryConfig(t *testing.T, visuals string) map[string]interface{} {
	t.Helper()
	config := deliveryConfig("deliver")
	if visuals != "" {
		var decoded interface{}
		if err := json.Unmarshal([]byte(visuals), &decoded); err != nil {
			t.Fatal(err)
		}
		config[adapterEnvelopeKey].(map[string]interface{})["visuals"] = decoded
	}
	return config
}

// postedSlackBody serves one Slack method and records its JSON body.
func postedSlackBody(t *testing.T, path string, body *map[string]interface{}) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != path {
			t.Errorf("path = %s", request.URL.Path)
		}
		if err := json.NewDecoder(request.Body).Decode(body); err != nil {
			t.Error(err)
		}
		_, _ = io.WriteString(response, `{"ok":true,"channel":"C123","ts":"1720000001.123"}`)
	}))
}

func TestSlackDeliveryPostsVisualBlocks(t *testing.T) {
	var posted map[string]interface{}
	server := postedSlackBody(t, "/chat.postMessage", &posted)
	defer server.Close()
	adapter := newSlackAdapter("", server.URL, server.Client())

	output, err := adapter.delivery(context.Background(), visualDeliveryConfig(t, "["+testVisualJSON+"]"))

	if err != nil || output["outcome"] != "delivered" {
		t.Fatalf("delivery = %#v, %v", output, err)
	}
	blocks, _ := json.Marshal(posted["blocks"])
	want := `[{"text":{"text":"Agent reply","type":"mrkdwn"},"type":"section"},` +
		`{"text":{"emoji":true,"text":"Quarter review","type":"plain_text"},"type":"header"},` +
		`{"text":{"text":"Next","type":"mrkdwn"},"type":"section"},` +
		`{"elements":[{"action_id":"openseal_card:1","text":{"emoji":true,"text":"Dig in","type":"plain_text"},"type":"button",` +
		`"value":"{\"d\":\"endpoint\",\"a\":\"visual-1\",\"v\":2,\"c\":\"ch\",\"k\":\"chip\",\"e\":\"a\"}"}],"type":"actions"}]`
	if string(blocks) != want || posted["text"] != "Agent reply\n\n*Quarter review*\nNext\n- Dig in" || posted["metadata"] == nil {
		t.Fatalf("Slack post = %#v", posted)
	}
}

func TestSlackDeliveryWithoutVisualsPostsText(t *testing.T) {
	var posted map[string]interface{}
	server := postedSlackBody(t, "/chat.postMessage", &posted)
	defer server.Close()
	adapter := newSlackAdapter("", server.URL, server.Client())

	output, err := adapter.delivery(context.Background(), visualDeliveryConfig(t, ""))

	if _, blocks := posted["blocks"]; err != nil || output["outcome"] != "delivered" || blocks || posted["text"] != "Agent reply" {
		t.Fatalf("Slack post = %#v, %v", posted, err)
	}
}

// The edit that finishes a streamed answer adds the visual in place.
func TestSlackFinishesAStreamedAnswerWithItsVisual(t *testing.T) {
	var updated map[string]interface{}
	server := postedSlackBody(t, "/chat.update", &updated)
	defer server.Close()
	adapter := newSlackAdapter("", server.URL, server.Client())
	config := visualDeliveryConfig(t, "["+testVisualJSON+"]")
	delivery := config[adapterEnvelopeKey].(map[string]interface{})["delivery"].(*conversationDelivery)
	delivery.Operation = "message.update"
	delivery.Parameters = map[string]interface{}{"providerMessageId": "1720000001.123"}

	output, err := adapter.delivery(context.Background(), config)

	blocks, _ := updated["blocks"].([]interface{})
	if err != nil || output["outcome"] != "delivered" || updated["ts"] != "1720000001.123" || len(blocks) != 4 || updated["metadata"] != nil {
		t.Fatalf("Slack update = %#v, %v", updated, err)
	}
}

// A file share cannot carry blocks, so its comment reads the visual as text.
func TestSlackFileMessageReadsTheVisualAsText(t *testing.T) {
	envelope := &adapterEnvelope{Message: &conversationMessage{Content: "Here's the **report**."}}
	if got := fileMessageText(envelope); got != "Here's the *report*." {
		t.Fatalf("text = %q", got)
	}
	envelope.Visuals = []conversationVisual{decodeTestVisual(t, testVisualJSON)}
	if got := fileMessageText(envelope); got != "Here's the *report*.\n\n*Quarter review*\nNext\n- Dig in" {
		t.Fatalf("text = %q", got)
	}
}
