package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/axiom-studio/skills.sdk/executor"
	"github.com/axiom-studio/skills.sdk/grpc"
	"github.com/axiom-studio/skills.sdk/resolver"
)

const (
	iconReddit       = "reddit"
	defaultUserAgent = "axiom-atlas-reddit-skill/1.0.0"
	maxResponseBytes = 10 << 20
)

var (
	redditOAuthBase = "https://oauth.reddit.com"
	redditWebBase   = "https://www.reddit.com"
	httpClient      = &http.Client{Timeout: 60 * time.Second}
)

func main() {
	port := os.Getenv("SKILL_PORT")
	if port == "" {
		port = "50111"
	}

	server := grpc.NewSkillServer("skill-reddit", "1.0.0")
	server.RegisterExecutorWithSchema("reddit-health", &HealthExecutor{}, HealthSchema)
	server.RegisterExecutorWithSchema("reddit-authorize-url", &AuthorizeURLExecutor{}, AuthorizeURLSchema)
	server.RegisterExecutorWithSchema("reddit-token", &TokenExecutor{}, TokenSchema)
	server.RegisterExecutorWithSchema("reddit-api-request", &APIRequestExecutor{}, APIRequestSchema)

	fmt.Printf("Starting skill-reddit gRPC server on port %s\n", port)
	if err := server.Serve(port); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to serve: %v\n", err)
		os.Exit(1)
	}
}

func getString(config map[string]interface{}, key string) string {
	v, _ := config[key].(string)
	return v
}

func getInt(config map[string]interface{}, key string, fallback int) int {
	switch v := config[key].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case string:
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

func resolveConfig(step *executor.StepDefinition, res executor.TemplateResolver) map[string]interface{} {
	return res.ResolveMap(step.Config)
}

func stringMap(value interface{}) (map[string]interface{}, error) {
	if value == nil {
		return map[string]interface{}{}, nil
	}
	if m, ok := value.(map[string]interface{}); ok {
		return m, nil
	}
	if m, ok := value.(map[string]string); ok {
		out := make(map[string]interface{}, len(m))
		for k, v := range m {
			out[k] = v
		}
		return out, nil
	}
	if s, ok := value.(string); ok {
		if strings.TrimSpace(s) == "" {
			return map[string]interface{}{}, nil
		}
		var out map[string]interface{}
		if err := json.Unmarshal([]byte(s), &out); err != nil {
			return nil, fmt.Errorf("must be a JSON object: %w", err)
		}
		return out, nil
	}
	return nil, fmt.Errorf("must be an object")
}

func valuesFromMap(value interface{}) (url.Values, error) {
	m, err := stringMap(value)
	if err != nil {
		return nil, err
	}
	values := make(url.Values)
	for key, raw := range m {
		switch v := raw.(type) {
		case []interface{}:
			for _, item := range v {
				values.Add(key, fmt.Sprint(item))
			}
		case []string:
			for _, item := range v {
				values.Add(key, item)
			}
		case nil:
			values.Add(key, "")
		default:
			values.Add(key, fmt.Sprint(v))
		}
	}
	return values, nil
}

func userAgent(config map[string]interface{}, res executor.TemplateResolver) string {
	ua := strings.TrimSpace(res.ResolveString(getString(config, "userAgent")))
	if ua == "" {
		return defaultUserAgent
	}
	return ua
}

func safeAPIURL(path string, query interface{}) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", errors.New("path is required")
	}
	parsed, err := url.Parse(path)
	if err != nil {
		return "", fmt.Errorf("invalid path: %w", err)
	}
	if parsed.IsAbs() || parsed.Host != "" || strings.HasPrefix(path, "//") {
		return "", errors.New("path must be relative to oauth.reddit.com")
	}
	if !strings.HasPrefix(parsed.Path, "/") {
		parsed.Path = "/" + parsed.Path
	}
	additional, err := valuesFromMap(query)
	if err != nil {
		return "", fmt.Errorf("invalid query: %w", err)
	}
	values := parsed.Query()
	for key, entries := range additional {
		for _, entry := range entries {
			values.Add(key, entry)
		}
	}
	parsed.RawQuery = values.Encode()
	return strings.TrimRight(redditOAuthBase, "/") + parsed.RequestURI(), nil
}

func encodeBody(kind string, body interface{}) (io.Reader, string, error) {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "", "none":
		return nil, "", nil
	case "json":
		if s, ok := body.(string); ok {
			var valid interface{}
			if err := json.Unmarshal([]byte(s), &valid); err != nil {
				return nil, "", fmt.Errorf("invalid JSON body: %w", err)
			}
			return strings.NewReader(s), "application/json", nil
		}
		encoded, err := json.Marshal(body)
		return bytes.NewReader(encoded), "application/json", err
	case "form":
		values, err := valuesFromMap(body)
		if err != nil {
			return nil, "", fmt.Errorf("invalid form body: %w", err)
		}
		return strings.NewReader(values.Encode()), "application/x-www-form-urlencoded", nil
	case "multipart":
		return encodeMultipart(body)
	case "raw":
		s, ok := body.(string)
		if !ok {
			return nil, "", errors.New("raw body must be a string")
		}
		return strings.NewReader(s), "application/octet-stream", nil
	default:
		return nil, "", fmt.Errorf("unsupported body type %q", kind)
	}
}

func encodeMultipart(body interface{}) (io.Reader, string, error) {
	fields, err := stringMap(body)
	if err != nil {
		return nil, "", fmt.Errorf("invalid multipart body: %w", err)
	}
	buffer := &bytes.Buffer{}
	writer := multipart.NewWriter(buffer)
	for name, value := range fields {
		file, isFile := value.(map[string]interface{})
		if !isFile {
			if err := writer.WriteField(name, fmt.Sprint(value)); err != nil {
				return nil, "", err
			}
			continue
		}
		encoded, _ := file["contentBase64"].(string)
		filename, _ := file["filename"].(string)
		if encoded == "" || filename == "" {
			return nil, "", fmt.Errorf("multipart field %q file requires filename and contentBase64", name)
		}
		content, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return nil, "", fmt.Errorf("multipart field %q has invalid base64: %w", name, err)
		}
		part, err := writer.CreateFormFile(name, filename)
		if err != nil {
			return nil, "", err
		}
		if _, err := part.Write(content); err != nil {
			return nil, "", err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, "", err
	}
	return buffer, writer.FormDataContentType(), nil
}

func applyHeaders(req *http.Request, raw interface{}) error {
	headers, err := stringMap(raw)
	if err != nil {
		return fmt.Errorf("invalid headers: %w", err)
	}
	for key, value := range headers {
		canonical := http.CanonicalHeaderKey(key)
		switch canonical {
		case "Authorization", "Cookie", "Host", "Proxy-Authorization", "User-Agent":
			return fmt.Errorf("header %q is managed by the Reddit skill", key)
		}
		if strings.ContainsAny(key, "\r\n") || strings.ContainsAny(fmt.Sprint(value), "\r\n") {
			return fmt.Errorf("header %q contains invalid characters", key)
		}
		req.Header.Set(canonical, fmt.Sprint(value))
	}
	return nil
}

func readResponse(resp *http.Response) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxResponseBytes {
		return nil, fmt.Errorf("Reddit response exceeds %d bytes", maxResponseBytes)
	}
	return body, nil
}

func decodedBody(body []byte, contentType, encoding string) interface{} {
	if encoding == "base64" {
		return base64.StdEncoding.EncodeToString(body)
	}
	if encoding != "text" && (strings.Contains(strings.ToLower(contentType), "json") || json.Valid(body)) {
		var decoded interface{}
		if json.Unmarshal(body, &decoded) == nil {
			return decoded
		}
	}
	return string(body)
}

type HealthExecutor struct{}

func (*HealthExecutor) Type() string { return "reddit-health" }
func (*HealthExecutor) Execute(context.Context, *executor.StepDefinition, executor.TemplateResolver) (*executor.StepResult, error) {
	return &executor.StepResult{Output: map[string]interface{}{"status": "OK", "apiBase": redditOAuthBase}}, nil
}

type AuthorizeURLExecutor struct{}

func (*AuthorizeURLExecutor) Type() string { return "reddit-authorize-url" }
func (*AuthorizeURLExecutor) Execute(_ context.Context, step *executor.StepDefinition, res executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolveConfig(step, res)
	clientID := res.ResolveString(getString(config, "clientId"))
	redirectURI := res.ResolveString(getString(config, "redirectUri"))
	state := res.ResolveString(getString(config, "state"))
	if clientID == "" || redirectURI == "" || state == "" {
		return nil, errors.New("clientId, redirectUri, and state are required")
	}
	duration := getString(config, "duration")
	if duration == "" {
		duration = "permanent"
	}
	if duration != "temporary" && duration != "permanent" {
		return nil, errors.New("duration must be temporary or permanent")
	}
	scope := getString(config, "scope")
	if scope == "" {
		scope = "identity"
	}
	params := url.Values{
		"client_id":     {clientID},
		"response_type": {"code"},
		"state":         {state},
		"redirect_uri":  {redirectURI},
		"duration":      {duration},
		"scope":         {scope},
	}
	authorizeURL := strings.TrimRight(redditWebBase, "/") + "/api/v1/authorize?" + params.Encode()
	return &executor.StepResult{Output: map[string]interface{}{"authorizeUrl": authorizeURL, "state": state, "duration": duration, "scope": scope}}, nil
}

type TokenExecutor struct{}

func (*TokenExecutor) Type() string { return "reddit-token" }
func (*TokenExecutor) Execute(ctx context.Context, step *executor.StepDefinition, res executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolveConfig(step, res)
	clientID := res.ResolveString(getString(config, "clientId"))
	clientSecret := res.ResolveString(getString(config, "clientSecret"))
	grantType := getString(config, "grantType")
	if clientID == "" || grantType == "" {
		return nil, errors.New("clientId and grantType are required")
	}
	form := url.Values{"grant_type": {grantType}}
	switch grantType {
	case "authorization_code":
		form.Set("code", res.ResolveString(getString(config, "code")))
		form.Set("redirect_uri", res.ResolveString(getString(config, "redirectUri")))
	case "refresh_token":
		form.Set("refresh_token", res.ResolveString(getString(config, "refreshToken")))
	case "password":
		form.Set("username", res.ResolveString(getString(config, "username")))
		form.Set("password", res.ResolveString(getString(config, "password")))
	case "client_credentials":
	case "https://oauth.reddit.com/grants/installed_client":
		form.Set("device_id", res.ResolveString(getString(config, "deviceId")))
	default:
		return nil, fmt.Errorf("unsupported grantType %q", grantType)
	}
	for key, values := range form {
		if key != "grant_type" && strings.TrimSpace(values[0]) == "" {
			return nil, fmt.Errorf("%s is required for %s", key, grantType)
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(redditWebBase, "/")+"/api/v1/access_token", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(clientID, clientSecret)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", userAgent(config, res))
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Reddit token request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := readResponse(resp)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("Reddit token request returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var token map[string]interface{}
	if err := json.Unmarshal(body, &token); err != nil {
		return nil, fmt.Errorf("invalid Reddit token response: %w", err)
	}
	if oauthErr, _ := token["error"].(string); oauthErr != "" {
		return nil, fmt.Errorf("Reddit OAuth error: %s", oauthErr)
	}
	return &executor.StepResult{Output: token}, nil
}

type APIRequestExecutor struct{}

func (*APIRequestExecutor) Type() string { return "reddit-api-request" }
func (*APIRequestExecutor) Execute(ctx context.Context, step *executor.StepDefinition, res executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolveConfig(step, res)
	token := res.ResolveString(getString(config, "accessToken"))
	if token == "" {
		return nil, errors.New("accessToken is required")
	}
	method := strings.ToUpper(strings.TrimSpace(getString(config, "method")))
	if method == "" {
		method = http.MethodGet
	}
	allowed := map[string]bool{"GET": true, "POST": true, "PUT": true, "PATCH": true, "DELETE": true, "HEAD": true, "OPTIONS": true}
	if !allowed[method] {
		return nil, fmt.Errorf("unsupported HTTP method %q", method)
	}
	requestURL, err := safeAPIURL(res.ResolveString(getString(config, "path")), config["query"])
	if err != nil {
		return nil, err
	}
	body := config["body"]
	if getString(config, "bodyType") == "raw" {
		body = getString(config, "rawBody")
	}
	bodyReader, contentType, err := encodeBody(getString(config, "bodyType"), body)
	if err != nil {
		return nil, err
	}
	timeout := getInt(config, "timeoutSeconds", 60)
	if timeout < 1 || timeout > 120 {
		return nil, errors.New("timeoutSeconds must be between 1 and 120")
	}
	requestCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, method, requestURL, bodyReader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", userAgent(config, res))
	req.Header.Set("Accept", "application/json")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if err := applyHeaders(req, config["headers"]); err != nil {
		return nil, err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Reddit API request failed: %w", err)
	}
	defer resp.Body.Close()
	responseBody, err := readResponse(resp)
	if err != nil {
		return nil, err
	}
	output := map[string]interface{}{
		"success":     resp.StatusCode >= 200 && resp.StatusCode < 300,
		"status":      resp.Status,
		"statusCode":  resp.StatusCode,
		"body":        decodedBody(responseBody, resp.Header.Get("Content-Type"), getString(config, "responseEncoding")),
		"contentType": resp.Header.Get("Content-Type"),
		"headers":     responseHeaders(resp.Header),
		"rateLimit": map[string]interface{}{
			"used":      resp.Header.Get("X-Ratelimit-Used"),
			"remaining": resp.Header.Get("X-Ratelimit-Remaining"),
			"reset":     resp.Header.Get("X-Ratelimit-Reset"),
		},
		"pagination": paginationFromBody(responseBody),
		"requestId":  firstHeader(resp.Header, "X-Reddit-Request", "X-Request-Id"),
	}
	return &executor.StepResult{Output: output}, nil
}

func responseHeaders(headers http.Header) map[string]interface{} {
	result := make(map[string]interface{}, len(headers))
	for key, values := range headers {
		result[key] = append([]string(nil), values...)
	}
	return result
}

func firstHeader(headers http.Header, names ...string) string {
	for _, name := range names {
		if value := headers.Get(name); value != "" {
			return value
		}
	}
	return ""
}

func paginationFromBody(body []byte) map[string]interface{} {
	result := map[string]interface{}{"after": nil, "before": nil, "dist": nil}
	var listing struct {
		Data struct {
			After  interface{} `json:"after"`
			Before interface{} `json:"before"`
			Dist   interface{} `json:"dist"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &listing) == nil {
		result["after"] = listing.Data.After
		result["before"] = listing.Data.Before
		result["dist"] = listing.Data.Dist
	}
	return result
}

var HealthSchema = resolver.NewSchemaBuilder("reddit-health").
	WithName("Reddit Health Check").WithCategory("action").WithIcon(iconReddit).
	WithDescription("Check whether the Reddit skill is running").Build()

var AuthorizeURLSchema = resolver.NewSchemaBuilder("reddit-authorize-url").
	WithName("Build Reddit Authorization URL").WithCategory("action").WithIcon(iconReddit).
	WithDescription("Build an OAuth authorization URL for a Reddit user grant").
	AddSection("OAuth").
	AddExpressionField("clientId", "Client ID", resolver.WithRequired()).
	AddExpressionField("redirectUri", "Redirect URI", resolver.WithRequired()).
	AddExpressionField("state", "State", resolver.WithRequired(), resolver.WithSensitive()).
	AddSelectField("duration", "Duration", []resolver.SelectOption{{Label: "Permanent", Value: "permanent"}, {Label: "Temporary", Value: "temporary"}}, resolver.WithDefault("permanent")).
	AddExpressionField("scope", "Scopes", resolver.WithDefault("identity"), resolver.WithHint("Space-separated Reddit OAuth scopes; use * only when explicitly justified")).
	EndSection().Build()

var TokenSchema = resolver.NewSchemaBuilder("reddit-token").
	WithName("Get Reddit OAuth Token").WithCategory("action").WithIcon(iconReddit).
	WithDescription("Exchange or refresh Reddit OAuth credentials using any supported grant").
	AddSection("Client").
	AddExpressionField("clientId", "Client ID", resolver.WithRequired()).
	AddExpressionField("clientSecret", "Client Secret", resolver.WithSensitive()).
	AddExpressionField("userAgent", "User Agent", resolver.WithDefault(defaultUserAgent)).
	EndSection().
	AddSection("Grant").
	AddSelectField("grantType", "Grant Type", []resolver.SelectOption{
		{Label: "Authorization code", Value: "authorization_code"},
		{Label: "Refresh token", Value: "refresh_token"},
		{Label: "Script password", Value: "password"},
		{Label: "Client credentials", Value: "client_credentials"},
		{Label: "Installed client", Value: "https://oauth.reddit.com/grants/installed_client"},
	}, resolver.WithRequired()).
	AddExpressionField("code", "Authorization Code", resolver.WithSensitive()).
	AddExpressionField("redirectUri", "Redirect URI").
	AddExpressionField("refreshToken", "Refresh Token", resolver.WithSensitive()).
	AddExpressionField("username", "Reddit Username").
	AddExpressionField("password", "Reddit Password", resolver.WithSensitive()).
	AddExpressionField("deviceId", "Device ID", resolver.WithSensitive()).
	EndSection().Build()

var APIRequestSchema = resolver.NewSchemaBuilder("reddit-api-request").
	WithName("Reddit API Request").WithCategory("action").WithIcon(iconReddit).
	WithDescription("Call any Reddit Data API OAuth endpoint with arbitrary supported method, query, headers, and body encoding").
	AddSection("Authentication").
	AddExpressionField("accessToken", "Access Token", resolver.WithRequired(), resolver.WithSensitive()).
	AddExpressionField("userAgent", "User Agent", resolver.WithDefault(defaultUserAgent)).
	EndSection().
	AddSection("Request").
	AddSelectField("method", "Method", []resolver.SelectOption{{Label: "GET", Value: "GET"}, {Label: "POST", Value: "POST"}, {Label: "PUT", Value: "PUT"}, {Label: "PATCH", Value: "PATCH"}, {Label: "DELETE", Value: "DELETE"}, {Label: "HEAD", Value: "HEAD"}, {Label: "OPTIONS", Value: "OPTIONS"}}, resolver.WithDefault("GET")).
	AddExpressionField("path", "OAuth API Path", resolver.WithRequired(), resolver.WithPlaceholder("/api/v1/me"), resolver.WithHint("Relative path on oauth.reddit.com; existing query parameters are preserved")).
	AddKeyValueField("query", "Query Parameters").
	AddSelectField("bodyType", "Body Type", []resolver.SelectOption{{Label: "None", Value: "none"}, {Label: "JSON", Value: "json"}, {Label: "Form", Value: "form"}, {Label: "Multipart", Value: "multipart"}, {Label: "Raw", Value: "raw"}}, resolver.WithDefault("none")).
	AddJSONField("body", "Body").
	AddTextareaField("rawBody", "Raw Body").
	AddKeyValueField("headers", "Additional Headers").
	AddSelectField("responseEncoding", "Response Encoding", []resolver.SelectOption{{Label: "Auto", Value: "auto"}, {Label: "Text", Value: "text"}, {Label: "Base64", Value: "base64"}}, resolver.WithDefault("auto")).
	AddNumberField("timeoutSeconds", "Timeout", resolver.WithDefault(60), resolver.WithMinMax(1, 120)).
	EndSection().Build()
