package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSlackFileOnlyIngressKeepsOriginWithoutPrivateURLs(t *testing.T) {
	now := time.Unix(1720000000, 0)
	adapter := newSlackAdapter("signing-secret", "", nil)
	adapter.now = func() time.Time { return now }
	body := []byte(`{"type":"event_callback","team_id":"T1","event_id":"Ev1","event":{"type":"message","subtype":"file_share","channel":"C1","user":"U1","ts":"1720000000.1","files":[{"id":"F1","name":"photo.png","mimetype":"image/png","size":12,"url_private":"https://files.slack.com/private-secret"}]}}`)
	output, err := adapter.ingress(t.Context(), ingressConfig(now, body, &conversationEndpoint{Provider: "slack", Address: "C1"}))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(output["events"])
	var events []normalizedConversationEvent
	json.Unmarshal(raw, &events)
	if len(events) != 1 || len(events[0].Attachments) != 1 || events[0].Attachments[0].MediaType != "image/png" || events[0].ExternalParticipantID != "U1" || events[0].ExternalThreadID != "1720000000.1" || strings.Contains(string(raw), "private-secret") {
		t.Fatalf("event = %s", raw)
	}
}
func TestSlackDownloadsOnlyVerifiedFilesAndNeverLeaksBotToken(t *testing.T) {
	var server *httptest.Server
	calls := 0
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer bot-secret" {
			t.Error("missing private-file authentication")
		}
		if r.URL.Path == "/files.info" {
			json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "file": map[string]interface{}{"id": "F1", "mimetype": "text/plain", "size": 5, "url_private_download": server.URL + "/private"}})
			return
		}
		io.WriteString(w, "hello")
	}))
	defer server.Close()
	adapter := newSlackAdapter("", server.URL, server.Client())
	file := conversationFile{ID: "F1", Name: "note.txt", MediaType: "text/plain", SizeBytes: 5}
	envelope := &adapterEnvelope{Endpoint: &conversationEndpoint{Address: "C1"}, Event: &normalizedConversationEvent{ExternalConversationID: "C1", Attachments: []conversationFile{file}}, Attachment: &file}
	result, err := adapter.readAttachment(t.Context(), "bot-secret", envelope)
	if err != nil || result["status"] != "supplied" || string(result["data"].([]byte)) != "hello" {
		t.Fatalf("download = %#v %v", result, err)
	}
	raw, _ := json.Marshal(result)
	if strings.Contains(string(raw), "bot-secret") || strings.Contains(string(raw), server.URL) {
		t.Fatal("private transport leaked")
	}
	forged := file
	forged.ID = "F-other"
	envelope.Attachment = &forged
	if _, err := adapter.readAttachment(t.Context(), "bot-secret", envelope); err == nil || calls != 2 {
		t.Fatal("unverified file fetched")
	}
	for _, link := range []string{"http://files.slack.com/private", "https://slack.com.attacker.invalid/private", "https://attacker.invalid/private", "https://user:secret@files.slack.com/private"} {
		if adapter.allowedFileURL(link) {
			t.Fatalf("unsafe URL allowed: %s", link)
		}
	}
}
func TestSlackUploadsCheckpointBeforeShareAndRecoversLostAcknowledgement(t *testing.T) {
	var server *httptest.Server
	allocated, uploaded, completed := 0, 0, 0
	shared := false
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/files.getUploadURLExternal":
			allocated++
			if r.Header.Get("Authorization") != "Bearer bot-secret" {
				t.Error("missing API authentication")
			}
			json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "upload_url": server.URL + "/upload", "file_id": "F-out"})
		case "/upload":
			uploaded++
			body, _ := io.ReadAll(r.Body)
			if string(body) != "image-bytes" || r.Header.Get("Authorization") != "" {
				t.Error("wrong upload bytes or credential leaked")
			}
			io.WriteString(w, "ok")
		case "/files.info":
			shares := map[string]interface{}{}
			if shared {
				shares = map[string]interface{}{"private": map[string]interface{}{"C-origin": []map[string]string{{"ts": "1720000001.1", "thread_ts": "1720000000.1"}}}}
			}
			json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "file": map[string]interface{}{"id": "F-out", "shares": shares}})
		case "/files.completeUploadExternal":
			completed++
			var body map[string]interface{}
			json.NewDecoder(r.Body).Decode(&body)
			if body["channel_id"] != "C-origin" || body["thread_ts"] != "1720000000.1" || body["initial_comment"] != "Here's your picture." {
				t.Errorf("wrong origin %#v", body)
			}
			shared = true
			// Slack completed the side effect, but the HTTP acknowledgement was lost.
			w.WriteHeader(http.StatusBadGateway)
		default:
			t.Errorf("unexpected API %s", r.URL.Path)
		}
	}))
	defer server.Close()
	adapter := newSlackAdapter("", server.URL, server.Client())
	envelope := &adapterEnvelope{Endpoint: &conversationEndpoint{Address: "C-origin"}, Delivery: &conversationDelivery{ID: "delivery1", Operation: "message.send", ExternalThreadID: "1720000000.1"}, Message: &conversationMessage{Content: "Here's your picture."}, Attachments: []conversationFileContent{{ID: "artifact:1", Name: "picture.png", MediaType: "image/png", Data: []byte("image-bytes")}}}
	result, err := adapter.deliverFiles(t.Context(), "bot-secret", envelope)
	if err != nil || result["outcome"] != "retry" || completed != 0 || uploaded != 1 {
		t.Fatalf("stage=%#v %v", result, err)
	}
	// JSON roundtrip models the durable outbox checkpoint, not in-memory state.
	raw, _ := json.Marshal(result["progress"])
	json.Unmarshal(raw, &envelope.Delivery.Progress)
	if strings.Contains(string(raw), server.URL) || strings.Contains(string(raw), "image-bytes") || strings.Contains(string(raw), "bot-secret") {
		t.Fatal("ephemeral payload persisted")
	}
	result, err = adapter.deliverFiles(t.Context(), "bot-secret", envelope)
	if err != nil || result["outcome"] != "retry" || completed != 1 {
		t.Fatalf("ambiguous=%#v %v", result, err)
	}
	result, err = adapter.lookupFileDelivery(t.Context(), "bot-secret", envelope)
	if err != nil || result["status"] != "found" {
		t.Fatalf("lookup=%#v %v", result, err)
	}
	result, err = adapter.deliverFiles(t.Context(), "bot-secret", envelope)
	if err != nil || result["outcome"] != "delivered" || allocated != 1 || uploaded != 1 || completed != 1 {
		t.Fatalf("replay duplicated upload: %#v %v counts=%d/%d/%d", result, err, allocated, uploaded, completed)
	}
	envelope.Endpoint.Address = "C-other"
	if _, err = adapter.lookupFileDelivery(t.Context(), "bot-secret", envelope); err == nil {
		t.Fatal("accepted checkpoint from different channel")
	}
}
func TestSlackFilePermissionFailuresAreExplicit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"ok":false,"error":"missing_scope"}`)
	}))
	defer server.Close()
	adapter := newSlackAdapter("", server.URL, server.Client())
	file := conversationFile{ID: "F1", Name: "note.txt"}
	result, err := adapter.readAttachment(t.Context(), "bot", &adapterEnvelope{Endpoint: &conversationEndpoint{Address: "C1"}, Event: &normalizedConversationEvent{ExternalConversationID: "C1", Attachments: []conversationFile{file}}, Attachment: &file})
	if err != nil || result["status"] != "missing_scope" {
		t.Fatalf("read = %#v %v", result, err)
	}
	result, err = adapter.deliverFiles(t.Context(), "bot", &adapterEnvelope{Endpoint: &conversationEndpoint{Address: "C1"}, Delivery: &conversationDelivery{ID: "d1"}, Message: &conversationMessage{}, Attachments: []conversationFileContent{{ID: "a1", Name: "file.txt", Data: []byte("hello")}}})
	if err != nil || result["outcome"] != "failed" || result["errorCode"] != "missing_scope" {
		t.Fatalf("write = %#v %v", result, err)
	}
}
