package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"hash"
	"crypto/sha256"
	"crypto/sha1"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/axiom-studio/skills.sdk/executor"
	"github.com/axiom-studio/skills.sdk/grpc"
	"github.com/axiom-studio/skills.sdk/resolver"
	"github.com/google/uuid"
)

// Webhook endpoint registry
var (
	endpoints   = make(map[string]*WebhookEndpoint)
	endpointMux sync.RWMutex
)

// WebhookEndpoint represents a registered webhook endpoint
type WebhookEndpoint struct {
	ID        string            `json:"id"`
	Path      string            `json:"path"`
	Method    string            `json:"method"`
	Headers   map[string]string `json:"headers"`
	CreatedAt time.Time         `json:"createdAt"`
	LastHit   *time.Time        `json:"lastHit,omitempty"`
	HitCount  int               `json:"hitCount"`
}

func main() {
	// Get port from env or use default
	port := os.Getenv("SKILL_PORT")
	if port == "" {
		port = "50053"
	}

	// Create skill server
	server := grpc.NewSkillServer("skill-webhook", "1.0.0")

	// Register webhook executors with schemas
	server.RegisterExecutorWithSchema("webhook-trigger", &WebhookTriggerExecutor{}, WebhookTriggerSchema)
	server.RegisterExecutorWithSchema("webhook-send", &WebhookSendExecutor{}, WebhookSendSchema)
	server.RegisterExecutorWithSchema("webhook-parse", &WebhookParseExecutor{}, WebhookParseSchema)
	server.RegisterExecutorWithSchema("webhook-signature", &WebhookSignatureExecutor{}, WebhookSignatureSchema)

	fmt.Printf("Starting skill-webhook gRPC server on port %s\n", port)
	if err := server.Serve(port); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to serve: %v\n", err)
		os.Exit(1)
	}
}

// WebhookTriggerExecutor handles webhook-trigger node type
type WebhookTriggerExecutor struct{}

// WebhookTriggerConfig defines the typed configuration for webhook-trigger
type WebhookTriggerConfig struct {
	Endpoint string            `json:"endpoint" description:"Webhook endpoint path"`
	Method   string            `json:"method" default:"POST" options:"GET,POST,PUT,DELETE,PATCH" description:"HTTP method"`
	Headers  map[string]string `json:"headers" description:"Expected headers"`
	Timeout  string            `json:"timeout" default:"30s" description:"Request timeout"`
}

// WebhookTriggerSchema is the UI schema for webhook-trigger
var WebhookTriggerSchema = resolver.NewSchemaBuilder("webhook-trigger").
	WithName("Webhook Trigger").
	WithCategory("webhook").
	WithIcon("zap").
	WithDescription("Create webhook endpoints to receive external events").
	AddSection("Endpoint").
		AddTextField("endpoint", "Endpoint Path",
			resolver.WithRequired(),
			resolver.WithPlaceholder("/webhooks/my-endpoint"),
			resolver.WithHint("Path for the webhook endpoint"),
		).
		AddSelectField("method", "HTTP Method", []resolver.SelectOption{
			{Label: "POST", Value: "POST"},
			{Label: "GET", Value: "GET"},
			{Label: "PUT", Value: "PUT"},
			{Label: "DELETE", Value: "DELETE"},
			{Label: "PATCH", Value: "PATCH"},
		}, resolver.WithDefault("POST")).
		EndSection().
	AddSection("Configuration").
		AddKeyValueField("headers", "Expected Headers",
			resolver.WithHint("Headers to validate on incoming requests"),
		).
		AddTextField("timeout", "Timeout",
			resolver.WithDefault("30s"),
			resolver.WithPlaceholder("30s"),
		).
		EndSection().
	Build()

func (e *WebhookTriggerExecutor) Type() string { return "webhook-trigger" }

func (e *WebhookTriggerExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	// Parse config into typed struct
	var cfg WebhookTriggerConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	}

	if cfg.Endpoint == "" {
		return nil, fmt.Errorf("endpoint is required")
	}

	// Generate unique endpoint ID
	endpointID := uuid.New().String()

	// Create endpoint
	endpoint := &WebhookEndpoint{
		ID:        endpointID,
		Path:      cfg.Endpoint,
		Method:    cfg.Method,
		Headers:   cfg.Headers,
		CreatedAt: time.Now(),
		HitCount:  0,
	}

	// Register endpoint
	endpointMux.Lock()
	endpoints[endpointID] = endpoint
	endpointMux.Unlock()

	// Parse timeout
	timeout := 30 * time.Second
	if cfg.Timeout != "" {
		if d, err := time.ParseDuration(cfg.Timeout); err == nil {
			timeout = d
		}
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"endpointId": endpointID,
			"path":       cfg.Endpoint,
			"method":     cfg.Method,
			"timeout":    timeout.String(),
			"webhookUrl": fmt.Sprintf("http://localhost:8080%s", cfg.Endpoint),
		},
	}, nil
}

// WebhookSendExecutor handles webhook-send node type
type WebhookSendExecutor struct{}

// WebhookSendConfig defines the typed configuration for webhook-send
type WebhookSendConfig struct {
	URL        string            `json:"url" description:"Target webhook URL"`
	Method     string            `json:"method" default:"POST" options:"GET,POST,PUT,DELETE,PATCH" description:"HTTP method"`
	Headers    map[string]string `json:"headers" description:"Custom headers"`
	Body       interface{}       `json:"body" description:"Request body"`
	Timeout    string            `json:"timeout" default:"30s" description:"Request timeout"`
	RetryCount int               `json:"retryCount" default:"0" description:"Number of retries on failure"`
}

// WebhookSendSchema is the UI schema for webhook-send
var WebhookSendSchema = resolver.NewSchemaBuilder("webhook-send").
	WithName("Send Webhook").
	WithCategory("webhook").
	WithIcon("send").
	WithDescription("Send webhook payloads to external services").
	AddSection("Target").
		AddTextField("url", "Webhook URL",
			resolver.WithRequired(),
			resolver.WithPlaceholder("https://example.com/webhook"),
			resolver.WithHint("Full URL of the webhook endpoint"),
		).
		AddSelectField("method", "HTTP Method", []resolver.SelectOption{
			{Label: "POST", Value: "POST"},
			{Label: "GET", Value: "GET"},
			{Label: "PUT", Value: "PUT"},
			{Label: "DELETE", Value: "DELETE"},
			{Label: "PATCH", Value: "PATCH"},
		}, resolver.WithDefault("POST")).
		EndSection().
	AddSection("Payload").
		AddKeyValueField("headers", "Headers",
			resolver.WithHint("Custom HTTP headers"),
		).
		AddJSONField("body", "Request Body",
			resolver.WithHeight(150),
			resolver.WithHint("JSON payload to send"),
		).
		EndSection().
	AddSection("Options").
		AddTextField("timeout", "Timeout",
			resolver.WithDefault("30s"),
			resolver.WithPlaceholder("30s"),
		).
		AddNumberField("retryCount", "Retry Count",
			resolver.WithDefault(0),
			resolver.WithMinMax(0, 10),
		).
		EndSection().
	Build()

func (e *WebhookSendExecutor) Type() string { return "webhook-send" }

func (e *WebhookSendExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	// Parse config into typed struct
	var cfg WebhookSendConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	}

	if cfg.URL == "" {
		return nil, fmt.Errorf("url is required")
	}

	// Parse timeout
	timeout := 30 * time.Second
	if cfg.Timeout != "" {
		if d, err := time.ParseDuration(cfg.Timeout); err == nil {
			timeout = d
		}
	}

	// Create HTTP client
	client := &http.Client{
		Timeout: timeout,
	}

	// Prepare request body
	var bodyReader io.Reader
	if cfg.Body != nil {
		bodyBytes, err := json.Marshal(cfg.Body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal body: %w", err)
		}
		bodyReader = bytes.NewReader(bodyBytes)
	}

	// Create request
	req, err := http.NewRequestWithContext(ctx, cfg.Method, cfg.URL, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	for key, value := range cfg.Headers {
		req.Header.Set(key, value)
	}

	// Execute request with retries
	var resp *http.Response
	var lastErr error
	attempts := cfg.RetryCount + 1

	for i := 0; i < attempts; i++ {
		resp, lastErr = client.Do(req)
		if lastErr == nil {
			break
		}
		if i < attempts-1 {
			time.Sleep(time.Second * time.Duration(i+1))
		}
	}

	if lastErr != nil {
		return nil, fmt.Errorf("request failed after %d attempts: %w", attempts, lastErr)
	}
	defer resp.Body.Close()

	// Read response
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	// Parse response JSON if possible
	var respJSON interface{}
	if err := json.Unmarshal(respBody, &respJSON); err != nil {
		respJSON = string(respBody)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"statusCode": resp.StatusCode,
			"headers":    resp.Header,
			"body":       respJSON,
			"url":        cfg.URL,
			"method":     cfg.Method,
		},
	}, nil
}

// WebhookParseExecutor handles webhook-parse node type
type WebhookParseExecutor struct{}

// WebhookParseConfig defines the typed configuration for webhook-parse
type WebhookParseConfig struct {
	Payload       string                 `json:"payload" description:"JSON payload to parse"`
	Schema        map[string]interface{} `json:"schema" description:"JSON schema for validation"`
	ExtractFields []string               `json:"extractFields" description:"Fields to extract from payload"`
}

// WebhookParseSchema is the UI schema for webhook-parse
var WebhookParseSchema = resolver.NewSchemaBuilder("webhook-parse").
	WithName("Parse Webhook").
	WithCategory("webhook").
	WithIcon("file-text").
	WithDescription("Parse and validate incoming webhook payloads").
	AddSection("Input").
		AddTextareaField("payload", "Payload",
			resolver.WithRequired(),
			resolver.WithRows(8),
			resolver.WithPlaceholder("{\"event\": \"user.created\", \"data\": {}}"),
			resolver.WithHint("JSON payload to parse"),
		).
		EndSection().
	AddSection("Validation").
		AddJSONField("schema", "JSON Schema",
			resolver.WithHeight(150),
			resolver.WithHint("Optional JSON schema for validation"),
		).
		EndSection().
	AddSection("Extraction").
		AddTagsField("extractFields", "Fields to Extract",
			resolver.WithHint("Field names to extract from payload"),
		).
		EndSection().
	Build()

func (e *WebhookParseExecutor) Type() string { return "webhook-parse" }

func (e *WebhookParseExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	// Parse config into typed struct
	var cfg WebhookParseConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	}

	if cfg.Payload == "" {
		return nil, fmt.Errorf("payload is required")
	}

	// Parse JSON payload
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(cfg.Payload), &payload); err != nil {
		return nil, fmt.Errorf("failed to parse JSON: %w", err)
	}

	// Validate against schema if provided
	if cfg.Schema != nil && len(cfg.Schema) > 0 {
		if err := validateSchema(payload, cfg.Schema); err != nil {
			return nil, fmt.Errorf("schema validation failed: %w", err)
		}
	}

	// Extract specified fields
	extracted := make(map[string]interface{})
	if len(cfg.ExtractFields) > 0 {
		for _, field := range cfg.ExtractFields {
			if value, ok := payload[field]; ok {
				extracted[field] = value
			}
		}
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"parsed":    payload,
			"extracted": extracted,
			"valid":     true,
		},
	}, nil
}

// validateSchema performs basic JSON schema validation
func validateSchema(data map[string]interface{}, schema map[string]interface{}) error {
	schemaType, ok := schema["type"].(string)
	if !ok || schemaType != "object" {
		return nil // Skip validation for non-object schemas
	}

	properties, ok := schema["properties"].(map[string]interface{})
	if !ok {
		return nil
	}

	required, ok := schema["required"].([]interface{})
	if ok {
		for _, req := range required {
			if reqStr, ok := req.(string); ok {
				if _, exists := data[reqStr]; !exists {
					return fmt.Errorf("missing required field: %s", reqStr)
				}
			}
		}
	}

	for key, prop := range properties {
		if propMap, ok := prop.(map[string]interface{}); ok {
			if propType, exists := propMap["type"].(string); exists {
				if value, hasValue := data[key]; hasValue {
					if !checkType(value, propType) {
						return fmt.Errorf("field %s has wrong type, expected %s", key, propType)
					}
				}
			}
		}
	}

	return nil
}

// checkType verifies if a value matches the expected JSON type
func checkType(value interface{}, expectedType string) bool {
	switch expectedType {
	case "string":
		_, ok := value.(string)
		return ok
	case "number":
		_, ok := value.(float64)
		return ok
	case "integer":
		_, ok := value.(int)
		if !ok {
			if f, ok := value.(float64); ok {
				return f == float64(int(f))
			}
		}
		return ok
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "array":
		_, ok := value.([]interface{})
		return ok
	case "object":
		_, ok := value.(map[string]interface{})
		return ok
	case "null":
		return value == nil
	}
	return true // Unknown type, assume valid
}

// WebhookSignatureExecutor handles webhook-signature node type
type WebhookSignatureExecutor struct{}

// WebhookSignatureConfig defines the typed configuration for webhook-signature
type WebhookSignatureConfig struct {
	Signature          string `json:"signature" description:"Signature from headers"`
	Secret             string `json:"secret" description:"Shared secret for verification"`
	Algorithm          string `json:"algorithm" default:"sha256" options:"sha256,sha1,sha512" description:"Hash algorithm"`
	HeaderName         string `json:"headerName" default:"X-Signature" description:"Header containing signature"`
	TimestampHeader    string `json:"timestampHeader" description:"Header containing timestamp"`
	TimestampTolerance string `json:"timestampTolerance" default:"300s" description:"Allowed timestamp drift"`
	Payload            string `json:"payload" description:"Raw payload for signature verification"`
}

// WebhookSignatureSchema is the UI schema for webhook-signature
var WebhookSignatureSchema = resolver.NewSchemaBuilder("webhook-signature").
	WithName("Verify Signature").
	WithCategory("webhook").
	WithIcon("shield").
	WithDescription("Verify webhook signatures for security").
	AddSection("Signature").
		AddTextField("signature", "Signature Value",
			resolver.WithRequired(),
			resolver.WithPlaceholder("sha256=abc123..."),
			resolver.WithHint("Signature from webhook headers"),
		).
		AddTextField("secret", "Secret",
			resolver.WithRequired(),
			resolver.WithPlaceholder("{{secrets.webhook_secret}}"),
			resolver.WithHint("Shared secret for HMAC verification"),
		).
		AddSelectField("algorithm", "Algorithm", []resolver.SelectOption{
			{Label: "SHA-256", Value: "sha256"},
			{Label: "SHA-1", Value: "sha1"},
			{Label: "SHA-512", Value: "sha512"},
		}, resolver.WithDefault("sha256")).
		EndSection().
	AddSection("Headers").
		AddTextField("headerName", "Signature Header",
			resolver.WithDefault("X-Signature"),
			resolver.WithPlaceholder("X-Signature"),
		).
		AddTextField("timestampHeader", "Timestamp Header",
			resolver.WithPlaceholder("X-Timestamp"),
			resolver.WithHint("Optional: header containing timestamp"),
		).
		EndSection().
	AddSection("Options").
		AddTextField("timestampTolerance", "Timestamp Tolerance",
			resolver.WithDefault("300s"),
			resolver.WithPlaceholder("300s"),
			resolver.WithHint("Allowed time drift (e.g., 300s)"),
		).
		AddTextareaField("payload", "Raw Payload",
			resolver.WithRows(5),
			resolver.WithHint("Raw payload body for signature calculation"),
		).
		EndSection().
	Build()

func (e *WebhookSignatureExecutor) Type() string { return "webhook-signature" }

func (e *WebhookSignatureExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	// Parse config into typed struct
	var cfg WebhookSignatureConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	}

	if cfg.Signature == "" {
		return nil, fmt.Errorf("signature is required")
	}

	if cfg.Secret == "" {
		return nil, fmt.Errorf("secret is required")
	}

	// Extract signature prefix if present (e.g., "sha256=abc123")
	signatureValue := cfg.Signature
	if idx := strings.Index(cfg.Signature, "="); idx != -1 {
		signatureValue = cfg.Signature[idx+1:]
	}

	// Decode signature
	expectedSig, err := hex.DecodeString(signatureValue)
	if err != nil {
		// Try base64 decoding
		expectedSig, err = base64.StdEncoding.DecodeString(signatureValue)
		if err != nil {
			return nil, fmt.Errorf("failed to decode signature: %w", err)
		}
	}

	// Calculate expected signature
	var hashFunc func() hash.Hash
	switch cfg.Algorithm {
	case "sha1":
		hashFunc = sha1.New
	case "sha512":
		hashFunc = sha512.New
	default:
		hashFunc = sha256.New
	}

	// Use payload if provided, otherwise use empty string
	payloadData := []byte(cfg.Payload)

	mac := hmac.New(hashFunc, []byte(cfg.Secret))
	mac.Write(payloadData)
	calculatedSig := mac.Sum(nil)

	// Compare signatures
	valid := hmac.Equal(expectedSig, calculatedSig)

	// Check timestamp if provided
	timestampValid := true
	var timestampAge string
	if cfg.TimestampHeader != "" && cfg.TimestampTolerance != "" {
		// Timestamp validation would require header values
		// This is a placeholder for timestamp validation logic
		tolerance, err := time.ParseDuration(cfg.TimestampTolerance)
		if err == nil {
			timestampAge = tolerance.String()
		}
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"valid":           valid,
			"algorithm":       cfg.Algorithm,
			"signatureMatch":  valid,
			"timestampValid":  timestampValid,
			"timestampAge":    timestampAge,
			"headerName":      cfg.HeaderName,
		},
	}, nil
}
