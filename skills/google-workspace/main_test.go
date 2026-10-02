package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/axiom-studio/skills.sdk/executor"
	"gopkg.in/yaml.v3"
)

type workspaceTransport func(*http.Request) (*http.Response, error)

func (f workspaceTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func operationNamed(t *testing.T, name string) operation {
	t.Helper()
	for _, op := range operations() {
		if op.Name == name {
			return op
		}
	}
	t.Fatalf("operation missing: %s", name)
	return operation{}
}
func TestEveryWorkspaceOperationUsesFixedRoutesAndHostToken(t *testing.T) {
	previous := client
	t.Cleanup(func() { client = previous })
	services := map[string]bool{}
	for _, op := range operations() {
		t.Run(op.Name, func(t *testing.T) {
			config := map[string]any{credentialName: "host-token"}
			for name, p := range op.Params {
				if !p.Required {
					continue
				}
				value := "resource-id"
				if strings.Contains(op.Path, "{+"+name+"}") {
					if op.Service == "people" {
						value = "people/contact-id"
					} else if op.Service == "meet" {
						value = "conferenceRecords/record-id"
						if strings.Contains(op.Name, "space") {
							value = "spaces/space-id"
						}
						if strings.HasSuffix(op.Name, "transcript-entries") {
							value = "conferenceRecords/record-id/transcripts/transcript-id"
						}
					}
				}
				if name == "mimeType" {
					value = "text/plain"
				}
				config[name] = value
			}
			if op.Body {
				config["body"] = map[string]any{"title": "Test"}
			}
			if op.BodyFormat == "email" {
				config["to"] = "seal@example.com"
				config["subject"] = "Test"
				config["text"] = "Body"
			}
			if op.BodyFormat == "multipart" {
				config["name"] = "note.txt"
				config["mimeType"] = "text/plain"
				config["contentBase64"] = base64.StdEncoding.EncodeToString([]byte("hello"))
			}
			client = &http.Client{Transport: workspaceTransport(func(r *http.Request) (*http.Response, error) {
				services[op.Service] = true
				if r.Header.Get("Authorization") != "Bearer host-token" || r.Method != op.Method || r.URL.Scheme != "https" || !strings.HasSuffix(r.URL.Host, "googleapis.com") || strings.Contains(r.URL.String(), "host-token") || strings.Contains(r.URL.Path, "{") {
					t.Fatalf("unsafe request: %s %s", r.Method, r.URL)
				}
				text := `{"id":"result"}`
				kind := "application/json"
				if op.ResponseFormat == "bytes" {
					text = "hello"
					kind = "text/plain"
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {kind}}, Body: io.NopCloser(strings.NewReader(text))}, nil
			})}
			result, err := (&workspaceExecutor{op: op}).Execute(context.Background(), &executor.StepDefinition{Config: config}, nil)
			if err != nil || result == nil {
				t.Fatalf("execution failed: %v", err)
			}
		})
	}
	if len(services) != 9 {
		t.Fatalf("covered %d services", len(services))
	}
}
func TestWorkspaceDeniesUnknownParametersResourceTraversalAndMissingCredentials(t *testing.T) {
	op := operationNamed(t, "google-meet-get-space")
	for _, name := range []string{"https://evil.example/token", "spaces/../victim", "spaces/x?key=secret", "conferenceRecords/other", "spaces/x%2fy", "spaces//id"} {
		if _, _, _, err := op.request(map[string]any{"name": name}); err == nil {
			t.Fatalf("accepted resource %q", name)
		}
	}
	if _, _, _, err := op.request(map[string]any{"name": "spaces/id", "url": "https://evil.example"}); err == nil {
		t.Fatal("accepted arbitrary endpoint")
	}
	if _, err := (&workspaceExecutor{op: op}).Execute(context.Background(), &executor.StepDefinition{Config: map[string]any{"name": "spaces/id"}}, nil); err == nil {
		t.Fatal("fell back to server Google credentials")
	}
	op = operationNamed(t, "google-drive-update-file")
	if _, _, _, err := op.request(map[string]any{"fileId": "id", "body": map[string]any{"trashed": true}}); err == nil {
		t.Fatal("trash bypassed destructive action")
	}
	op = operationNamed(t, "google-gmail-modify-message")
	if _, _, _, err := op.request(map[string]any{"id": "id", "body": map[string]any{"addLabelIds": []any{"TRASH"}}}); err == nil {
		t.Fatal("Gmail trash bypassed destructive action")
	}
}
func TestSheetsRangeAndPaginationAreEncoded(t *testing.T) {
	op := operationNamed(t, "google-sheets-update-values")
	endpoint, body, _, err := op.request(map[string]any{"spreadsheetId": "sheet", "range": "'Q1 Sales'!A1:B2", "body": map[string]any{"values": []any{[]any{"hello", 42}}}})
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(endpoint)
	if u.Query().Get("valueInputOption") != "RAW" || !strings.Contains(u.Path, "'Q1 Sales'!A1:B2") || !strings.Contains(string(body), "values") {
		t.Fatalf("invalid Sheets request %s", endpoint)
	}
	op = operationNamed(t, "google-gmail-list-messages")
	endpoint, _, _, err = op.request(map[string]any{"q": "from:seal@example.com is:unread", "pageToken": "opaque + / token", "maxResults": 10})
	if err != nil {
		t.Fatal(err)
	}
	u, _ = url.Parse(endpoint)
	if u.Query().Get("pageToken") != "opaque + / token" || u.Query().Get("maxResults") != "10" || !strings.Contains(u.Path, "/users/me/") {
		t.Fatal("lost mailbox boundary or pagination")
	}
	op = operationNamed(t, "google-people-search-contacts")
	if _, _, _, err = op.request(map[string]any{"query": "Seal"}); err != nil {
		t.Fatalf("invalid bounded contacts search: %v", err)
	}
}
func TestEmailDraftAndUploadPayloads(t *testing.T) {
	config := map[string]any{"to": "Seal <seal@example.com>", "subject": "Hello 🦭", "text": "Line one\nLine two"}
	payload, err := emailPayload(config, true)
	if err != nil {
		t.Fatal(err)
	}
	var draft map[string]any
	json.Unmarshal(payload, &draft)
	raw := draft["message"].(map[string]any)["raw"].(string)
	message, _ := base64.RawURLEncoding.DecodeString(raw)
	if !strings.Contains(string(message), "To: \"Seal\" <seal@example.com>") || !strings.Contains(string(message), "Content-Transfer-Encoding: base64") {
		t.Fatalf("invalid MIME %s", message)
	}
	config["subject"] = "hello\r\nBcc: attacker@example.com"
	if _, err := emailPayload(config, false); err == nil {
		t.Fatal("accepted header injection")
	}
	payload, kind, err := uploadPayload(map[string]any{"name": "note.txt", "mimeType": "text/plain", "contentBase64": base64.StdEncoding.EncodeToString([]byte("hello")), "parentId": "folder"})
	if err != nil {
		t.Fatal(err)
	}
	media, params, _ := mime.ParseMediaType(kind)
	if media != "multipart/related" {
		t.Fatal("wrong Google upload encoding")
	}
	reader := multipart.NewReader(strings.NewReader(string(payload)), params["boundary"])
	part, _ := reader.NextPart()
	metadata, _ := io.ReadAll(part)
	part, _ = reader.NextPart()
	content, _ := io.ReadAll(part)
	if !strings.Contains(string(metadata), "folder") || string(content) != "hello" {
		t.Fatal("lost upload metadata or bytes")
	}
}
func TestManifestMatchesRuntimeActionsAndScopedOAuth(t *testing.T) {
	var out strings.Builder
	if err := writeManifest(&out); err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if err := yaml.Unmarshal([]byte(out.String()), &parsed); err != nil {
		t.Fatal(err)
	}
	definition := parsed["definition"].(map[string]any)
	actions := definition["actions"].(map[string]any)
	if len(actions) != len(operations()) {
		t.Fatal("manifest/runtime drift")
	}
	for _, op := range operations() {
		action := actions[op.Name].(map[string]any)
		credentials := action["credentials"].([]any)
		oauth := credentials[0].(map[string]any)["oauth2"].(map[string]any)
		if oauth["provider"] != "google" || oauth["subject"] != "user" || action["risk"] != op.Risk {
			t.Fatalf("wrong authority for %s", op.Name)
		}
		properties := action["inputSchema"].(map[string]any)["properties"].(map[string]any)
		if _, exists := properties[credentialName]; exists {
			t.Fatal("credential exposed to model")
		}
	}
	// The saved installation manifest must be reproducible from the runtime catalog.
	savedBytes, err := os.ReadFile("skill.yaml")
	saved := string(savedBytes)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(strings.TrimSpace(saved), strings.TrimSpace(out.String())) {
		t.Fatal("regenerate skill.yaml with -manifest")
	}
}
func TestGoogleErrorsAndOversizedResponsesDoNotExposeTokens(t *testing.T) {
	previous := client
	t.Cleanup(func() { client = previous })
	op := operationNamed(t, "google-gmail-list-messages")
	for _, status := range []int{401, 403, 429, 302} {
		client = &http.Client{Transport: workspaceTransport(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader("secret token")), Header: make(http.Header)}, nil
		})}
		_, err := (&workspaceExecutor{op: op}).Execute(context.Background(), &executor.StepDefinition{Config: map[string]any{credentialName: "host-token"}}, nil)
		if err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatalf("unsafe error %v", err)
		}
	}
	client = &http.Client{Transport: workspaceTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(strings.Repeat("x", maxContentBytes+1))), Header: make(http.Header)}, nil
	})}
	if _, err := (&workspaceExecutor{op: op}).Execute(context.Background(), &executor.StepDefinition{Config: map[string]any{credentialName: "host-token"}}, nil); err == nil {
		t.Fatal("accepted unbounded response")
	}
}

func TestGoogleErrorsExposeOnlyReviewedReasons(t *testing.T) {
	for _, tc := range []struct{ body, want string }{
		{`{"error":{"message":"private-content token-secret","details":[{"reason":"SERVICE_DISABLED","metadata":{"consumer":"private-project"}}]}}`, "enable the service API"},
		{`{"error":{"errors":[{"reason":"insufficientPermissions"}]}}`, "required consent scope"},
		{`{"error":{"errors":[{"reason":"domainPolicy"}]}}`, "organization policy"},
		{`{"error":{"errors":[{"reason":"private-unknown"}],"message":"private-content token-secret"}}`, "does not establish"},
		{`not json private-content token-secret`, "does not establish"},
	} {
		err := googleResponseError("gmail", 403, strings.NewReader(tc.body))
		if !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("missing safe guidance: %v", err)
		}
		for _, secret := range []string{"private-content", "token-secret", "private-project", "private-unknown"} {
			if strings.Contains(err.Error(), secret) {
				t.Fatalf("provider content leaked: %v", err)
			}
		}
	}
}
