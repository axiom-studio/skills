package main

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/axiom-studio/skills.sdk/executor"
)

// slackFileServer serves files.info for one file and its private download.
func slackFileServer(t *testing.T, info map[string]interface{}, content string) (*httptest.Server, *int) {
	t.Helper()
	downloads := 0
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer xoxb-files" {
			t.Errorf("missing bot token on %s", r.URL.Path)
		}
		switch r.URL.Path {
		case "/files.info":
			if file, ok := info["file"].(map[string]interface{}); ok {
				file["url_private_download"] = server.URL + "/private"
			}
			json.NewEncoder(w).Encode(info)
		case "/private":
			downloads++
			io.WriteString(w, content)
		default:
			t.Errorf("unexpected Slack call %s", r.URL.Path)
		}
	}))
	t.Cleanup(server.Close)
	previous := slackBaseURLOverride
	slackBaseURLOverride = server.URL
	t.Cleanup(func() { slackBaseURLOverride = previous })
	return server, &downloads
}

func readSlackFile(t *testing.T, channel, fileID string) (*executor.StepResult, error) {
	t.Helper()
	return (&SlackReadFileExecutor{}).Execute(t.Context(), &executor.StepDefinition{Config: map[string]interface{}{"channel": channel, "fileId": fileID}},
		slackBindingResolver{bindings: map[string]interface{}{slackBotTokenCredential: "xoxb-files"}})
}

func sharedFile(id, mediaType string, size int, channel string) map[string]interface{} {
	return map[string]interface{}{"ok": true, "file": map[string]interface{}{
		"id": id, "name": "shot", "mimetype": mediaType, "size": size,
		"shares": map[string]interface{}{"public": map[string]interface{}{channel: []map[string]string{{"ts": "1.000001"}}}},
	}}
}

func TestSlackReadMessagesListsMessageFiles(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "messages": []map[string]interface{}{
			{"text": "see screenshot", "ts": "2.000001", "files": []map[string]interface{}{
				{"id": "F1", "name": "shot.png", "mimetype": "image/png", "size": 42, "url_private_download": "https://files.slack.com/secret"},
			}},
			{"text": "plain", "ts": "1.000001"},
		}})
	}))
	defer server.Close()
	previous := slackBaseURLOverride
	slackBaseURLOverride = server.URL
	t.Cleanup(func() { slackBaseURLOverride = previous })
	result, err := (&SlackReadMessagesExecutor{}).Execute(t.Context(), &executor.StepDefinition{Config: map[string]interface{}{"channel": "C1"}},
		slackBindingResolver{bindings: map[string]interface{}{slackBotTokenCredential: "xoxb-files"}})
	if err != nil {
		t.Fatal(err)
	}
	messages := result.Output["messages"].([]map[string]interface{})
	files, _ := messages[0]["files"].([]conversationFile)
	if len(files) != 1 || files[0] != (conversationFile{ID: "F1", Name: "shot.png", MediaType: "image/png", SizeBytes: 42}) {
		t.Fatalf("message files = %#v", messages[0]["files"])
	}
	if _, ok := messages[1]["files"]; ok {
		t.Fatalf("message without files lists files: %#v", messages[1])
	}
	raw, _ := json.Marshal(result.Output)
	if strings.Contains(string(raw), "secret") {
		t.Fatal("private file URL leaked into read output")
	}
}

func TestSlackReadFileSuppliesSharedPNGToModel(t *testing.T) {
	slackFileServer(t, sharedFile("F1", "image/png", 4, "C1"), "\x89PNG")
	result, err := readSlackFile(t, "C1", "F1")
	if err != nil {
		t.Fatal(err)
	}
	media, _ := result.Output["modelMedia"].(map[string]interface{})
	if result.Output["status"] != "supplied" || result.Output["channel"] != "C1" || media["mediaType"] != "image/png" ||
		media["detail"] != "auto" || media["contentBase64"] != base64.StdEncoding.EncodeToString([]byte("\x89PNG")) {
		t.Fatalf("read file = %#v", result.Output)
	}
	if file := result.Output["file"].(conversationFile); file.ID != "F1" || file.SizeBytes != 4 {
		t.Fatalf("file = %#v", file)
	}
}

func TestSlackReadFileRefusesFileNotSharedInChannel(t *testing.T) {
	_, downloads := slackFileServer(t, sharedFile("F1", "image/png", 4, "C2"), "\x89PNG")
	_, err := readSlackFile(t, "C1", "F1")
	if err == nil || err.Error() != "file F1 is not shared in channel C1" || *downloads != 0 {
		t.Fatalf("err = %v, downloads = %d", err, *downloads)
	}
}

func TestSlackReadFileAcceptsLegacyChannelList(t *testing.T) {
	slackFileServer(t, map[string]interface{}{"ok": true, "file": map[string]interface{}{
		"id": "F1", "name": "shot.jpg", "mimetype": "image/jpeg", "size": 3, "groups": []string{"G1"},
	}}, "jpg")
	result, err := readSlackFile(t, "G1", "F1")
	if err != nil || result.Output["status"] != "supplied" {
		t.Fatalf("read = %#v, %v", result, err)
	}
}

func TestSlackReadFileDoesNotDownloadNonImages(t *testing.T) {
	for mediaType, want := range map[string]string{"application/pdf": "not_an_image", "image/gif": "unsupported_image"} {
		_, downloads := slackFileServer(t, sharedFile("F1", mediaType, 4, "C1"), "data")
		result, err := readSlackFile(t, "C1", "F1")
		if err != nil || result.Output["status"] != want || result.Output["modelMedia"] != nil || *downloads != 0 {
			t.Fatalf("%s: read = %#v, err = %v, downloads = %d", mediaType, result, err, *downloads)
		}
	}
}

func TestSlackReadFileMissingScopeAsksForFilesRead(t *testing.T) {
	slackFileServer(t, map[string]interface{}{"ok": false, "error": "missing_scope"}, "")
	_, err := readSlackFile(t, "C1", "F1")
	if err == nil || !strings.Contains(err.Error(), "files:read") || !strings.Contains(err.Error(), "reinstall") {
		t.Fatalf("err = %v", err)
	}
}

func TestSlackReadFileReportsSizeLimitWithoutData(t *testing.T) {
	_, downloads := slackFileServer(t, sharedFile("F1", "image/png", maximumSlackFileBytes+1, "C1"), "")
	result, err := readSlackFile(t, "C1", "F1")
	if err != nil || result.Output["status"] != "size_limit" || result.Output["modelMedia"] != nil || *downloads != 0 {
		t.Fatalf("read = %#v, err = %v", result, err)
	}
}

func TestSlackReadFileRejectsMalformedFileID(t *testing.T) {
	if _, err := readSlackFile(t, "C1", "../files.info"); err == nil {
		t.Fatal("malformed file ID accepted")
	}
}
