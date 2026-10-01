// Google Workspace executes a fixed, reviewed API catalog with an ephemeral
// access token from the host. It never owns refresh tokens or app secrets.
package main

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/mail"
	"net/textproto"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/axiom-studio/skills.sdk/executor"
	skillgrpc "github.com/axiom-studio/skills.sdk/grpc"
)

const skillID = "skill-google-workspace"
const skillVersion = "1.0.0"
const credentialName = "google_access_token"
const maxContentBytes = 2 * 1024 * 1024

//go:embed operations.json
var operationJSON []byte
var client = &http.Client{Timeout: 45 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
var placeholder = regexp.MustCompile(`\{(\+?)([A-Za-z0-9_]+)\}`)

type parameter struct {
	Type     string   `json:"type"`
	Location string   `json:"location"`
	Required bool     `json:"required"`
	Repeated bool     `json:"repeated"`
	Enum     []string `json:"enum,omitempty"`
	Minimum  string   `json:"minimum,omitempty"`
	Maximum  string   `json:"maximum,omitempty"`
	Default  string   `json:"default,omitempty"`
}
type operation struct {
	Name           string               `json:"name"`
	Service        string               `json:"service"`
	Method         string               `json:"method"`
	BaseURL        string               `json:"baseURL"`
	Path           string               `json:"path"`
	Description    string               `json:"description"`
	Params         map[string]parameter `json:"params"`
	Body           bool                 `json:"body"`
	BodyFormat     string               `json:"bodyFormat,omitempty"`
	Scopes         []string             `json:"scopes"`
	Risk           string               `json:"risk"`
	ResponseFormat string               `json:"responseFormat"`
}

func operations() []operation {
	var values []operation
	if err := json.Unmarshal(operationJSON, &values); err != nil {
		panic(err)
	}
	return values
}
func main() {
	if len(os.Args) == 2 && os.Args[1] == "-manifest" {
		if err := writeManifest(os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	server := skillgrpc.NewSkillServer(skillID, skillVersion)
	for _, op := range operations() {
		server.RegisterExecutorWithSchema(op.Name, &workspaceExecutor{op: op}, nodeSchema(op))
	}
	port := strings.TrimSpace(os.Getenv("SKILL_PORT"))
	if port == "" {
		port = "50051"
	}
	if err := server.Serve(port); err != nil {
		fmt.Fprintln(os.Stderr, "Google Workspace Skill server failed")
		os.Exit(1)
	}
}

type workspaceExecutor struct{ op operation }

func (e *workspaceExecutor) Type() string { return e.op.Name }
func (e *workspaceExecutor) Execute(ctx context.Context, step *executor.StepDefinition, bindings executor.TemplateResolver) (*executor.StepResult, error) {
	if step == nil {
		return nil, errors.New("Google action input is required")
	}
	token, _ := step.Config[credentialName].(string)
	if r, ok := bindings.(executor.BindingResolver); ok {
		if value, ok := r.GetBinding(credentialName).(string); ok && value != "" {
			token = value
		}
	}
	if strings.TrimSpace(token) == "" || strings.ContainsAny(token, "\r\n") {
		return nil, errors.New("Connect and select a Google Workspace account before using this action")
	}
	endpoint, body, contentType, err := e.op.request(step.Config)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, e.op.Method, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, errors.New("Invalid Google request")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if len(body) > 0 {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, errors.New("Google request failed; no automatic retry was attempted")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("Google %s request rejected (%d); check account access, consent scopes and enabled APIs", e.op.Service, resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxContentBytes+1))
	if err != nil || len(data) > maxContentBytes {
		return nil, errors.New("Google response exceeds the 2 MiB limit; request a smaller page or fields projection")
	}
	result := map[string]any{}
	if e.op.ResponseFormat == "bytes" {
		result["contentBase64"] = base64.StdEncoding.EncodeToString(data)
		result["mimeType"] = resp.Header.Get("Content-Type")
		result["sizeBytes"] = len(data)
		if strings.HasPrefix(resp.Header.Get("Content-Type"), "text/") && utf8.Valid(data) {
			result["text"] = string(data)
		}
	} else if len(data) > 0 && json.Unmarshal(data, &result) != nil {
		return nil, errors.New("Google returned an invalid JSON response")
	}
	return &executor.StepResult{Output: result}, nil
}
func (op operation) request(config map[string]any) (string, []byte, string, error) {
	properties, _ := inputProperties(op)
	for key := range config {
		if key != credentialName {
			if _, ok := properties[key]; !ok {
				return "", nil, "", fmt.Errorf("Unsupported Google argument %q", key)
			}
		}
	}
	query := url.Values{}
	values := map[string]string{"userId": "me"}
	for key, p := range op.Params {
		value, ok := config[key]
		if !ok || value == nil || value == "" {
			value, ok = defaultParameter(op, key, p)
		}
		if !ok {
			if p.Required {
				return "", nil, "", fmt.Errorf("Google argument %s is required", key)
			}
			continue
		}
		converted, err := parameterValues(value, p)
		if err != nil {
			return "", nil, "", fmt.Errorf("Invalid Google argument %s", key)
		}
		if p.Location == "path" {
			if len(converted) != 1 || converted[0] == "" {
				return "", nil, "", fmt.Errorf("Invalid Google path argument %s", key)
			}
			values[key] = converted[0]
		} else {
			for _, s := range converted {
				query.Add(key, s)
			}
		}
	}
	pathErr := false
	path := placeholder.ReplaceAllStringFunc(op.Path, func(match string) string {
		groups := placeholder.FindStringSubmatch(match)
		value := values[groups[2]]
		if value == "" || len(value) > 1024 || strings.ContainsAny(value, "\x00\r\n") {
			pathErr = true
			return ""
		}
		if groups[1] == "+" {
			if !validResource(op, groups[2], value) {
				pathErr = true
				return ""
			}
			parts := strings.Split(value, "/")
			for i := range parts {
				parts[i] = url.PathEscape(parts[i])
			}
			return strings.Join(parts, "/")
		}
		if value == "." || value == ".." {
			pathErr = true
			return ""
		}
		return url.PathEscape(value)
	})
	if pathErr {
		return "", nil, "", errors.New("Invalid Google resource name")
	}
	var body []byte
	contentType := "application/json"
	var err error
	switch op.BodyFormat {
	case "trash":
		body = []byte(`{"trashed":true}`)
	case "email":
		body, err = emailPayload(config, strings.HasSuffix(op.Name, "draft-email"))
	case "multipart":
		body, contentType, err = uploadPayload(config)
		query.Set("uploadType", "multipart")
	default:
		if op.Body {
			value, ok := config["body"].(map[string]any)
			if !ok {
				return "", nil, "", errors.New("Google request body must be a JSON object")
			}
			if op.Name == "google-drive-update-file" {
				if _, exists := value["trashed"]; exists {
					return "", nil, "", errors.New("Use the dedicated trash-file action to move a file to trash")
				}
			}
			body, err = json.Marshal(value)
			if op.Name == "google-gmail-modify-message" && err == nil {
				var labels struct {
					AddLabelIDs []string `json:"addLabelIds"`
				}
				if json.Unmarshal(body, &labels) != nil {
					return "", nil, "", errors.New("Gmail label IDs must be strings")
				}
				for _, label := range labels.AddLabelIDs {
					if strings.EqualFold(label, "TRASH") {
						return "", nil, "", errors.New("Use the dedicated trash-message action to move a message to trash")
					}
				}
			}
		}
	}
	if err != nil || len(body) > maxContentBytes+65536 {
		if err != nil {
			return "", nil, "", err
		}
		return "", nil, "", errors.New("Google request body is too large")
	}
	if strings.HasSuffix(op.Name, "download-file") {
		query.Set("alt", "media")
	}
	endpoint := op.BaseURL + path
	if encoded := query.Encode(); encoded != "" {
		endpoint += "?" + encoded
	}
	return endpoint, body, contentType, nil
}
func parameterValues(value any, p parameter) ([]string, error) {
	if p.Repeated {
		values, ok := value.([]any)
		if !ok {
			return nil, errors.New("array required")
		}
		if len(values) > 100 {
			return nil, errors.New("too many values")
		}
		p.Repeated = false
		var out []string
		for _, v := range values {
			converted, err := parameterValues(v, p)
			if err != nil {
				return nil, err
			}
			out = append(out, converted...)
		}
		return out, nil
	}
	var text string
	switch p.Type {
	case "integer":
		number, err := strconv.ParseInt(fmt.Sprint(value), 10, 64)
		if err != nil {
			return nil, err
		}
		if p.Minimum != "" {
			minimum, _ := strconv.ParseInt(p.Minimum, 10, 64)
			if number < minimum {
				return nil, errors.New("below minimum")
			}
		}
		if p.Maximum != "" {
			maximum, _ := strconv.ParseInt(p.Maximum, 10, 64)
			if number > maximum {
				return nil, errors.New("above maximum")
			}
		}
		text = strconv.FormatInt(number, 10)
	case "boolean":
		v, ok := value.(bool)
		if !ok {
			return nil, errors.New("boolean required")
		}
		text = strconv.FormatBool(v)
	default:
		v, ok := value.(string)
		if !ok || len(v) > 8192 || strings.ContainsAny(v, "\x00\r\n") {
			return nil, errors.New("invalid string")
		}
		text = v
	}
	if len(p.Enum) > 0 {
		found := false
		for _, v := range p.Enum {
			if v == text {
				found = true
			}
		}
		if !found {
			return nil, errors.New("invalid enum")
		}
	}
	return []string{text}, nil
}
func defaultParameter(op operation, key string, p parameter) (any, bool) {
	if key == "resourceName" && op.Name == "google-people-list-contacts" {
		return "people/me", true
	}
	if key == "personFields" || key == "readMask" {
		return "names,emailAddresses,phoneNumbers", true
	}
	if key == "valueInputOption" {
		return "RAW", true
	}
	if key == "pageSize" || key == "maxResults" {
		limit := int64(50)
		if maximum, err := strconv.ParseInt(p.Maximum, 10, 64); err == nil && maximum < limit {
			limit = maximum
		}
		if op.Name == "google-people-search-contacts" {
			limit = 30
		}
		return limit, true
	}
	if key == "fields" && op.Name == "google-drive-list-files" {
		return "nextPageToken,files(id,name,mimeType,modifiedTime,webViewLink,parents)", true
	}
	if key == "fields" && op.Name == "google-drive-get-file" {
		return "id,name,mimeType,modifiedTime,webViewLink,parents,size", true
	}
	if p.Default != "" {
		switch p.Type {
		case "boolean":
			v, err := strconv.ParseBool(p.Default)
			return v, err == nil
		case "integer":
			v, err := strconv.ParseInt(p.Default, 10, 64)
			return v, err == nil
		}
		return p.Default, true
	}
	return nil, false
}
func validResource(op operation, key, value string) bool {
	parts := strings.Split(value, "/")
	for _, s := range parts {
		if s == "" || s == "." || s == ".." || strings.ContainsAny(s, "?#%\\") {
			return false
		}
	}
	if op.Service == "people" {
		return len(parts) == 2 && parts[0] == "people"
	}
	if op.Service == "slides" {
		return len(parts) == 1
	}
	if op.Service != "meet" {
		return false
	}
	if strings.Contains(op.Name, "space") {
		return len(parts) == 2 && parts[0] == "spaces"
	}
	if strings.HasSuffix(op.Name, "list-transcript-entries") {
		return len(parts) == 4 && parts[0] == "conferenceRecords" && parts[2] == "transcripts"
	}
	return len(parts) == 2 && parts[0] == "conferenceRecords"
}
func emailPayload(config map[string]any, draft bool) ([]byte, error) {
	var headers []string
	for _, key := range []string{"to", "cc", "bcc"} {
		value, _ := config[key].(string)
		if key == "to" && strings.TrimSpace(value) == "" {
			return nil, errors.New("Email recipient is required")
		}
		if value == "" {
			continue
		}
		if strings.ContainsAny(value, "\r\n") {
			return nil, errors.New("Invalid email recipient")
		}
		addresses, err := mail.ParseAddressList(value)
		if err != nil || len(addresses) > 100 {
			return nil, errors.New("Invalid email recipient")
		}
		encoded := make([]string, len(addresses))
		for i, a := range addresses {
			encoded[i] = a.String()
		}
		headers = append(headers, strings.ToUpper(key[:1])+key[1:]+": "+strings.Join(encoded, ", "))
	}
	subject, ok := config["subject"].(string)
	if !ok || len(subject) > 998 || strings.ContainsAny(subject, "\r\n") {
		return nil, errors.New("Invalid email subject")
	}
	text, ok := config["text"].(string)
	if !ok || len(text) > maxContentBytes/2 {
		return nil, errors.New("Email text is required and must fit the content limit")
	}
	headers = append(headers, "Subject: "+mime.QEncoding.Encode("utf-8", subject), "MIME-Version: 1.0", "Content-Type: text/plain; charset=utf-8", "Content-Transfer-Encoding: base64")
	content := base64.StdEncoding.EncodeToString([]byte(text))
	var lines []string
	for len(content) > 76 {
		lines = append(lines, content[:76])
		content = content[76:]
	}
	lines = append(lines, content)
	message := map[string]any{"raw": base64.RawURLEncoding.EncodeToString([]byte(strings.Join(headers, "\r\n") + "\r\n\r\n" + strings.Join(lines, "\r\n")))}
	if thread, ok := config["threadId"].(string); ok {
		message["threadId"] = thread
	}
	if draft {
		return json.Marshal(map[string]any{"message": message})
	}
	return json.Marshal(message)
}
func uploadPayload(config map[string]any) ([]byte, string, error) {
	name, _ := config["name"].(string)
	kind, _ := config["mimeType"].(string)
	encoded, _ := config["contentBase64"].(string)
	if name == "" || len(name) > 256 || strings.ContainsAny(name, "\r\n") {
		return nil, "", errors.New("A valid file name is required")
	}
	if _, _, err := mime.ParseMediaType(kind); err != nil || strings.ContainsAny(kind, "\r\n") {
		return nil, "", errors.New("A valid MIME type is required")
	}
	if len(encoded) > base64.StdEncoding.EncodedLen(maxContentBytes) {
		return nil, "", errors.New("File exceeds the 2 MiB limit")
	}
	content, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, "", errors.New("Invalid base64 file content")
	}
	metadata := map[string]any{"name": name}
	if parent, ok := config["parentId"].(string); ok && parent != "" {
		metadata["parents"] = []string{parent}
	}
	var out bytes.Buffer
	writer := multipart.NewWriter(&out)
	part, _ := writer.CreatePart(textproto.MIMEHeader{"Content-Type": {"application/json; charset=UTF-8"}})
	if err := json.NewEncoder(part).Encode(metadata); err != nil {
		return nil, "", err
	}
	part, _ = writer.CreatePart(textproto.MIMEHeader{"Content-Type": {kind}})
	if _, err := part.Write(content); err != nil {
		return nil, "", err
	}
	if err := writer.Close(); err != nil {
		return nil, "", err
	}
	return out.Bytes(), "multipart/related; boundary=" + writer.Boundary(), nil
}
