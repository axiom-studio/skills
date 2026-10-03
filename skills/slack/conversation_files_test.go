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
			if r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" || r.ParseForm() != nil || r.PostForm.Get("filename") != "picture.png" || r.PostForm.Get("length") != "11" {
				t.Fatal("upload URL allocation must use encoded filename and byte length")
			}
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
			if !shared {
				io.WriteString(w, `{"ok":false,"error":"file_not_found"}`)
				return
			}
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
	raw, _ = json.Marshal(result["progress"])
	json.Unmarshal(raw, &envelope.Delivery.Progress)
	checkpoint, err := uploadCheckpoint(envelope)
	if err != nil || !checkpoint.CompletionAttempted {
		t.Fatalf("ambiguous completion was not checkpointed: %#v %v", checkpoint, err)
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

func TestSlackCompletesCheckpointedUploadBeforeFileIsReadable(t *testing.T) {
	for _, lookupError := range []string{"file_not_found", "missing_scope"} {
		t.Run(lookupError, func(t *testing.T) {
			allocated, uploaded, completed := 0, 0, 0
			var server *httptest.Server
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/files.getUploadURLExternal":
					allocated++
					json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "upload_url": server.URL + "/upload", "file_id": "F-pending"})
				case "/upload":
					uploaded++
					io.WriteString(w, "ok")
				case "/files.info":
					json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": lookupError})
				case "/files.completeUploadExternal":
					completed++
					var body struct {
						Files   []map[string]string `json:"files"`
						Channel string              `json:"channel_id"`
						Thread  string              `json:"thread_ts"`
						Comment string              `json:"initial_comment"`
					}
					if json.NewDecoder(r.Body).Decode(&body) != nil || len(body.Files) != 1 || body.Files[0]["id"] != "F-pending" || body.Files[0]["title"] != "picture.png" || body.Channel != "D-origin" || body.Thread != "1720000000.1" || body.Comment != "Here's your *picture*." {
						t.Errorf("completion drifted from checkpointed origin: %#v", body)
					}
					io.WriteString(w, `{"ok":true,"files":[{"id":"F-pending"}]}`)
				default:
					t.Errorf("unexpected API %s", r.URL.Path)
				}
			}))
			defer server.Close()
			adapter := newSlackAdapter("", server.URL, server.Client())
			envelope := slackFileDeliveryTestEnvelope()
			result, err := adapter.deliverFiles(t.Context(), "bot-secret", envelope)
			if err != nil || result["errorCode"] != "file_upload_ready" || completed != 0 {
				t.Fatalf("upload must persist before sharing: %#v %v", result, err)
			}
			persistSlackFileTestProgress(t, envelope, result)
			// The delivery worker also looks up acknowledgement on a fresh lease.
			lookup, err := adapter.lookupFileDelivery(t.Context(), "bot-secret", envelope)
			if err != nil || lookup["status"] != "unknown" {
				t.Fatalf("uncompleted file unexpectedly readable: %#v %v", lookup, err)
			}
			result, err = adapter.deliverFiles(t.Context(), "bot-secret", envelope)
			if err != nil || result["outcome"] != "delivered" || result["providerMessageId"] != "file:F-pending" || allocated != 1 || uploaded != 1 || completed != 1 {
				t.Fatalf("pending file was not completed: %#v %v counts=%d/%d/%d", result, err, allocated, uploaded, completed)
			}
		})
	}
}

func TestSlackAmbiguousFileCompletionOnlyConfirmsExistingUpload(t *testing.T) {
	for _, acknowledgement := range []string{"http_failure", "invalid_json", "internal_error"} {
		t.Run(acknowledgement, func(t *testing.T) {
			completed := 0
			visible := false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/files.info":
					if !visible {
						io.WriteString(w, `{"ok":true,"file":{"id":"F-pending","shares":{}}}`)
						return
					}
					io.WriteString(w, `{"ok":true,"file":{"id":"F-pending","shares":{"private":{"D-origin":[{"ts":"1720000001.1","thread_ts":"1720000000.1"}]}}}}`)
				case "/files.completeUploadExternal":
					completed++
					switch acknowledgement {
					case "http_failure":
						w.WriteHeader(http.StatusBadGateway)
					case "invalid_json":
						io.WriteString(w, "truncated acknowledgement")
					case "internal_error":
						io.WriteString(w, `{"ok":false,"error":"internal_error"}`)
					}
				default:
					t.Errorf("reallocated or reuploaded an existing file: %s", r.URL.Path)
				}
			}))
			defer server.Close()
			adapter := newSlackAdapter("", server.URL, server.Client())
			envelope := slackFileDeliveryTestEnvelope()
			checkpoint, err := uploadCheckpoint(envelope)
			if err != nil {
				t.Fatal(err)
			}
			checkpoint.Files = []slackUploadedFile{{ArtifactID: "artifact:1", Digest: "2c8648d103e3dd7ad87660da0f126a1443b6d21ac1bd3ec000c5e24e2373a90c", FileID: "F-pending", Title: "picture.png"}}
			persistSlackFileTestProgress(t, envelope, fileRetry(checkpoint, "file_upload_ready"))
			result, err := adapter.deliverFiles(t.Context(), "bot-secret", envelope)
			if err != nil || result["errorCode"] != "confirmation_pending" || completed != 1 {
				t.Fatalf("ambiguous completion: %#v %v completions=%d", result, err, completed)
			}
			persistSlackFileTestProgress(t, envelope, result)
			for attempt := 0; attempt < 2; attempt++ {
				result, err = adapter.deliverFiles(t.Context(), "bot-secret", envelope)
				if err != nil || result["errorCode"] != "confirmation_pending" || completed != 1 {
					t.Fatalf("ambiguous completion repeated side effect: %#v %v completions=%d", result, err, completed)
				}
				persistSlackFileTestProgress(t, envelope, result)
			}
			visible = true
			result, err = adapter.deliverFiles(t.Context(), "bot-secret", envelope)
			if err != nil || result["outcome"] != "delivered" || result["providerMessageId"] != "1720000001.1" || completed != 1 {
				t.Fatalf("existing share not acknowledged: %#v %v completions=%d", result, err, completed)
			}
		})
	}
}

func TestSlackFileCompletionRejectionsAreTerminal(t *testing.T) {
	for _, code := range []string{"missing_scope", "not_in_channel", "invalid_auth", "file_not_found", "file_uploads_disabled"} {
		t.Run(code, func(t *testing.T) {
			completed := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/files.info":
					io.WriteString(w, `{"ok":false,"error":"file_not_found"}`)
				case "/files.completeUploadExternal":
					completed++
					json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": code})
				default:
					t.Errorf("reallocated or reuploaded an existing file: %s", r.URL.Path)
				}
			}))
			defer server.Close()
			adapter := newSlackAdapter("", server.URL, server.Client())
			envelope := slackFileDeliveryTestEnvelope()
			checkpoint, _ := uploadCheckpoint(envelope)
			checkpoint.Files = []slackUploadedFile{{ArtifactID: "artifact:1", Digest: "2c8648d103e3dd7ad87660da0f126a1443b6d21ac1bd3ec000c5e24e2373a90c", FileID: "F-pending", Title: "picture.png"}}
			persistSlackFileTestProgress(t, envelope, fileRetry(checkpoint, "file_upload_ready"))
			result, err := adapter.deliverFiles(t.Context(), "bot-secret", envelope)
			if err != nil || result["outcome"] != "failed" || result["errorCode"] != code || completed != 1 {
				t.Fatalf("permanent rejection was hidden or retried: %#v %v completions=%d", result, err, completed)
			}
		})
	}
}

func TestSlackRateLimitedCompletionReusesPendingUpload(t *testing.T) {
	for _, rejection := range []string{"http_429", "http_429_invalid_body", "ratelimited"} {
		t.Run(rejection, func(t *testing.T) {
			completed := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/files.info":
					io.WriteString(w, `{"ok":false,"error":"file_not_found"}`)
				case "/files.completeUploadExternal":
					completed++
					if completed == 1 {
						w.Header().Set("Retry-After", "23")
						if strings.HasPrefix(rejection, "http_429") {
							w.WriteHeader(http.StatusTooManyRequests)
						}
						if rejection == "http_429_invalid_body" {
							io.WriteString(w, strings.Repeat("x", maxSlackResponseBytes+1))
							return
						}
						io.WriteString(w, `{"ok":false,"error":"ratelimited"}`)
						return
					}
					io.WriteString(w, `{"ok":true,"files":[{"id":"F-pending"}]}`)
				default:
					t.Errorf("reallocated or reuploaded an existing file: %s", r.URL.Path)
				}
			}))
			defer server.Close()
			adapter := newSlackAdapter("", server.URL, server.Client())
			envelope := slackFileDeliveryTestEnvelope()
			checkpoint, _ := uploadCheckpoint(envelope)
			checkpoint.Files = []slackUploadedFile{{ArtifactID: "artifact:1", Digest: "2c8648d103e3dd7ad87660da0f126a1443b6d21ac1bd3ec000c5e24e2373a90c", FileID: "F-pending", Title: "picture.png"}}
			persistSlackFileTestProgress(t, envelope, fileRetry(checkpoint, "file_upload_ready"))
			result, err := adapter.deliverFiles(t.Context(), "bot-secret", envelope)
			retryAfterMs := int64(23000)
			if rejection == "http_429_invalid_body" {
				// The shared HTTP helper cannot parse retry metadata from an invalid body.
				retryAfterMs = 1000
			}
			if err != nil || result["outcome"] != "retry" || result["retryAfterMs"] != retryAfterMs || completed != 1 {
				t.Fatalf("rate limit not preserved: %#v %v completions=%d", result, err, completed)
			}
			persistSlackFileTestProgress(t, envelope, result)
			checkpoint, err = uploadCheckpoint(envelope)
			if err != nil || checkpoint.CompletionAttempted {
				t.Fatalf("rejected completion treated as ambiguous: %#v %v", checkpoint, err)
			}
			result, err = adapter.deliverFiles(t.Context(), "bot-secret", envelope)
			if err != nil || result["outcome"] != "delivered" || completed != 2 {
				t.Fatalf("pending upload not reused after rate limit: %#v %v completions=%d", result, err, completed)
			}
		})
	}
}

func slackFileDeliveryTestEnvelope() *adapterEnvelope {
	return &adapterEnvelope{Endpoint: &conversationEndpoint{Address: "D-origin"}, Delivery: &conversationDelivery{ID: "delivery1", Operation: "message.send", ExternalThreadID: "1720000000.1"}, Message: &conversationMessage{Content: "Here's your **picture**."}, Attachments: []conversationFileContent{{ID: "artifact:1", Name: "picture.png", MediaType: "image/png", Data: []byte("image-bytes")}}}
}

func persistSlackFileTestProgress(t *testing.T, envelope *adapterEnvelope, result map[string]interface{}) {
	t.Helper()
	raw, err := json.Marshal(result["progress"])
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &envelope.Delivery.Progress); err != nil {
		t.Fatal(err)
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

func TestSlackUploadRejectionsPreserveProviderCode(t *testing.T) {
	for _, code := range []string{"invalid_arguments", "file_uploads_disabled", "ratelimited"} {
		t.Run(code, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": code})
			}))
			defer server.Close()
			adapter := newSlackAdapter("", server.URL, server.Client())
			result, err := adapter.deliverFiles(t.Context(), "bot", &adapterEnvelope{Endpoint: &conversationEndpoint{Address: "C1"}, Delivery: &conversationDelivery{ID: "d1"}, Message: &conversationMessage{}, Attachments: []conversationFileContent{{ID: "a1", Name: "image.png", Data: []byte("image")}}})
			outcome := "failed"
			if code == "ratelimited" {
				outcome = "retry"
			}
			if err != nil || result["outcome"] != outcome || result["errorCode"] != code {
				t.Fatalf("upload rejection = %#v %v", result, err)
			}
		})
	}
}
