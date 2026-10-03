package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func telegramAttachmentConfig() map[string]interface{} {
	file := telegramConversationFile{ID: "file1", Name: "plan.pdf", MediaType: "application/pdf", SizeBytes: 4}
	return map[string]interface{}{telegramCredentialKey: testTelegramBotToken, telegramAdapterEnvelope: &telegramAdapterRequest{Operation: "attachment", Endpoint: &telegramConversationEndpoint{Provider: "telegram", Address: "*", InstallationWide: true}, Attachment: &file, Event: &telegramNormalizedEvent{ExternalConversationID: "99", Attachments: []telegramConversationFile{file}}}}
}
func TestTelegramVerifiedAttachmentDownload(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		switch {
		case strings.HasSuffix(r.URL.Path, "/getFile"):
			var body map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["file_id"] != "file1" {
				t.Error(body)
			}
			_, _ = io.WriteString(w, `{"ok":true,"result":{"file_id":"file1","file_size":4,"file_path":"documents/plan.pdf"}}`)
		case strings.Contains(r.URL.Path, "/file/bot"):
			_, _ = io.WriteString(w, "test")
		default:
			t.Error("unexpected method")
		}
	}))
	defer server.Close()
	output, err := newTelegramConversationAdapter(server.URL+"/bot", server.Client()).delivery(t.Context(), telegramAttachmentConfig())
	if err != nil || output["status"] != "supplied" || string(output["data"].([]byte)) != "test" || output["name"] != "plan.pdf" || calls != 2 {
		t.Fatal(output, err, calls)
	}
	encoded, _ := json.Marshal(output)
	if strings.Contains(string(encoded), testTelegramBotToken) || strings.Contains(string(encoded), "file_path") {
		t.Fatal("download URL leaked")
	}
}
func TestTelegramAttachmentCannotSubstituteFileOrOrigin(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = io.WriteString(w, `{"ok":true,"result":{"file_id":"different","file_path":"documents/plan.pdf"}}`)
	}))
	defer server.Close()
	adapter := newTelegramConversationAdapter(server.URL+"/bot", server.Client())
	config := telegramAttachmentConfig()
	request := config[telegramAdapterEnvelope].(*telegramAdapterRequest)
	request.Attachment.ID = "different"
	if _, err := adapter.delivery(t.Context(), config); err == nil || calls != 0 {
		t.Fatal("unverified file reached provider")
	}
	config = telegramAttachmentConfig()
	request = config[telegramAdapterEnvelope].(*telegramAdapterRequest)
	request.Endpoint.Address = "100"
	if _, err := adapter.delivery(t.Context(), config); err == nil || calls != 0 {
		t.Fatal("cross-chat file reached provider")
	}
	if _, err := adapter.delivery(t.Context(), telegramAttachmentConfig()); err == nil || calls != 1 {
		t.Fatal("provider file substitution accepted")
	}
}
func TestTelegramDownloadLimitsAndUntrustedPaths(t *testing.T) {
	for _, test := range []struct{ name, response, download, status string }{
		{"provider size", `{"ok":true,"result":{"file_id":"file1","file_size":2097153,"file_path":"documents/plan.pdf"}}`, "", "size_limit"},
		{"stream size", `{"ok":true,"result":{"file_id":"file1","file_path":"documents/plan.pdf"}}`, strings.Repeat("x", maximumTelegramFileBytes+1), "size_limit"},
		{"traversal", `{"ok":true,"result":{"file_id":"file1","file_path":"../other"}}`, "", "unavailable"},
		{"external URL", `{"ok":true,"result":{"file_id":"file1","file_path":"https://evil.test/steal"}}`, "", "unavailable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/getFile") {
					_, _ = io.WriteString(w, test.response)
				} else {
					_, _ = io.WriteString(w, test.download)
				}
			}))
			defer server.Close()
			output, err := newTelegramConversationAdapter(server.URL+"/bot", server.Client()).delivery(t.Context(), telegramAttachmentConfig())
			if err != nil || output["status"] != test.status || output["data"] != nil {
				t.Fatal(output, err)
			}
		})
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		t.Error("oversized descriptor reached provider")
	}))
	defer server.Close()
	config := telegramAttachmentConfig()
	request := config[telegramAdapterEnvelope].(*telegramAdapterRequest)
	request.Attachment.SizeBytes = maximumTelegramFileBytes + 1
	request.Event.Attachments[0] = *request.Attachment
	output, err := newTelegramConversationAdapter(server.URL+"/bot", server.Client()).delivery(t.Context(), config)
	if err != nil || output["status"] != "size_limit" || requests != 0 {
		t.Fatal(output, err)
	}
}
func TestTelegramTokenBearingFileRedirectIsRefused(t *testing.T) {
	leaked := 0
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked++ }))
	defer other.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/getFile") {
			_, _ = io.WriteString(w, `{"ok":true,"result":{"file_id":"file1","file_path":"documents/plan.pdf"}}`)
		} else {
			http.Redirect(w, r, other.URL+"/"+testTelegramBotToken, http.StatusFound)
		}
	}))
	defer server.Close()
	output, err := newTelegramConversationAdapter(server.URL+"/bot", server.Client()).delivery(t.Context(), telegramAttachmentConfig())
	if leaked != 0 || err == nil || output != nil || strings.Contains(err.Error(), testTelegramBotToken) {
		t.Fatal(output, err, leaked)
	}
}
func TestTelegramArtifactDeliveryReusesSuccessfulPhasesAfterRateLimit(t *testing.T) {
	textCalls, photoCalls, documentCalls := 0, 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/sendMessage"):
			textCalls++
			var body map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["message_thread_id"] != float64(77) || body["reply_parameters"] != nil {
				t.Error(body)
			}
			_, _ = io.WriteString(w, `{"ok":true,"result":{"message_id":10,"chat":{"id":99}}}`)
		case strings.HasSuffix(r.URL.Path, "/sendPhoto"):
			photoCalls++
			if err := r.ParseMultipartForm(4 << 20); err != nil {
				t.Error(err)
			}
			if r.FormValue("message_thread_id") != "77" {
				t.Error(r.Form)
			}
			file, _, err := r.FormFile("photo")
			if err != nil {
				t.Error(err)
			} else {
				data, _ := io.ReadAll(file)
				file.Close()
				if string(data) != "png" {
					t.Error(string(data))
				}
			}
			_, _ = io.WriteString(w, `{"ok":true,"result":{"message_id":11,"chat":{"id":99}}}`)
		case strings.HasSuffix(r.URL.Path, "/sendDocument"):
			documentCalls++
			if documentCalls == 1 {
				w.WriteHeader(429)
				_, _ = io.WriteString(w, `{"ok":false,"error_code":429,"parameters":{"retry_after":3}}`)
			} else {
				_, _ = io.WriteString(w, `{"ok":true,"result":{"message_id":12,"chat":{"id":99}}}`)
			}
		default:
			t.Error("unexpected request")
		}
	}))
	defer server.Close()
	adapter := newTelegramConversationAdapter(server.URL+"/bot", server.Client())
	config := telegramDeliveryConfig("message.send", "")
	request := config[telegramAdapterEnvelope].(*telegramAdapterRequest)
	request.Delivery.ExternalThreadID = "topic:77"
	request.Attachments = []telegramConversationFileContent{{ID: "image", Name: "seal.png", MediaType: "image/png", Data: []byte("png")}, {ID: "pdf", Name: "plan.pdf", MediaType: "application/pdf", Data: []byte("pdf")}}
	output, err := adapter.delivery(t.Context(), config)
	if err != nil || output["outcome"] != "retry" || output["retryAfterMs"] != int64(3000) {
		t.Fatal(output, err)
	}
	request.Delivery.Progress = output["progress"].(map[string]interface{})
	request.Delivery.Attempt = 2
	output, err = adapter.delivery(t.Context(), config)
	if err != nil || output["outcome"] != "delivered" || textCalls != 1 || photoCalls != 1 || documentCalls != 2 || output["providerMessageId"] != "99:12" {
		t.Fatal(output, err, textCalls, photoCalls, documentCalls)
	}
	request.Delivery.Progress = output["progress"].(map[string]interface{})
	request.Operation = "lookup"
	output, err = adapter.delivery(t.Context(), config)
	if err != nil || output["status"] != "found" {
		t.Fatal(output, err)
	}
	request.Operation = "deliver"
	output, err = adapter.delivery(t.Context(), config)
	if err != nil || output["outcome"] != "delivered" || textCalls != 1 || photoCalls != 1 || documentCalls != 2 {
		t.Fatal(output, err)
	}
	request.Attachments[0].Data = []byte("changed")
	output, err = adapter.delivery(t.Context(), config)
	if err != nil || output["errorCode"] != "checkpoint_conflict" {
		t.Fatal(output, err)
	}
}
func TestTelegramLongTextSplitsWithoutBreakingUnicode(t *testing.T) {
	text := strings.Repeat("🙂", 3000)
	chunks := telegramTextChunks(text)
	if len(chunks) != 2 || strings.Join(chunks, "") != text || len([]rune(chunks[0])) != 2048 {
		t.Fatal(len(chunks))
	}
}
func TestTelegramArtifactRejectsOversizeBeforeSendingText(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests++ }))
	defer server.Close()
	config := telegramDeliveryConfig("message.send", "")
	request := config[telegramAdapterEnvelope].(*telegramAdapterRequest)
	request.Attachments = []telegramConversationFileContent{{ID: "pdf", Name: "plan.pdf", Data: make([]byte, maximumTelegramFileBytes+1)}}
	output, err := newTelegramConversationAdapter(server.URL+"/bot", server.Client()).delivery(t.Context(), config)
	if err != nil || output["outcome"] != "failed" || requests != 0 {
		t.Fatal(output, err)
	}
}
func TestTelegramPhotoChoosesLargestVariantWithinHostBudget(t *testing.T) {
	files := normalizedTelegramFiles(telegramMessage{Photo: []telegramMedia{{FileID: "tiny", Width: 10, Height: 10, FileSize: 100}, {FileID: "readable", Width: 300, Height: 300, FileSize: 1000}, {FileID: "oversized", Width: 1000, Height: 1000, FileSize: maximumTelegramFileBytes + 1}}})
	if len(files) != 1 || files[0].ID != "readable" {
		t.Fatal(files)
	}
}
