package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/axiom-studio/skills.sdk/executor"
	"github.com/axiom-studio/skills.sdk/grpc"
	"github.com/axiom-studio/skills.sdk/resolver"
)

const (
	iconReplicate  = "cpu"
	defaultBaseURL = "https://api.replicate.com/v1"
)

// Replicate client cache
var (
	clients   = make(map[string]*ReplicateClient)
	clientMux sync.RWMutex
)

// ReplicateClient holds the HTTP client and configuration
type ReplicateClient struct {
	HTTPClient *http.Client
	APIKey     string
	BaseURL    string
}

// ReplicatePrediction represents a prediction object
type ReplicatePrediction struct {
	ID             string                 `json:"id"`
	Model          string                 `json:"model"`
	Version        string                 `json:"version"`
	Input          map[string]interface{} `json:"input"`
	Status         string                 `json:"status"`
	CreatedAt      string                 `json:"created_at"`
	StartedAt      string                 `json:"started_at"`
	CompletedAt    string                 `json:"completed_at"`
	Output         interface{}            `json:"output"`
	Error          string                 `json:"error"`
	Logs           string                 `json:"logs"`
	Metrics        map[string]interface{} `json:"metrics"`
	URLs           map[string]string      `json:"urls"`
}

// ReplicateTraining represents a training object
type ReplicateTraining struct {
	ID              string                 `json:"id"`
	Model           string                 `json:"model"`
	Version         string                 `json:"version"`
	Destination     string                 `json:"destination"`
	Input           map[string]interface{} `json:"input"`
	Status          string                 `json:"status"`
	CreatedAt       string                 `json:"created_at"`
	StartedAt       string                 `json:"started_at"`
	CompletedAt     string                 `json:"completed_at"`
	Output          map[string]interface{} `json:"output"`
	Error           string                 `json:"error"`
	Logs            string                 `json:"logs"`
	Metrics         map[string]interface{} `json:"metrics"`
	URLs            map[string]string      `json:"urls"`
}

// ReplicateModel represents a model object
type ReplicateModel struct {
	Owner       string                 `json:"owner"`
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Visibility  string                 `json:"visibility"`
	GithubURL   string                 `json:"github_url"`
	PaperURL    string                 `json:"paper_url"`
	LicenseURL  string                 `json:"license_url"`
	RunCount    int                    `json:"run_count"`
	CoverImage  string                 `json:"cover_image_url"`
	DefaultExample map[string]interface{} `json:"default_example"`
	LatestVersion *ReplicateModelVersion `json:"latest_version"`
}

// ReplicateModelVersion represents a model version
type ReplicateModelVersion struct {
	ID            string   `json:"id"`
	CreatedAt     string   `json:"created_at"`
	CogVersion    string   `json:"cog_version"`
	OpenAPISchema map[string]interface{} `json:"openapi_schema"`
}

// ReplicateListResponse represents a paginated list response
type ReplicateListResponse struct {
	Previous string          `json:"previous"`
	Next     string          `json:"next"`
	Results  json.RawMessage `json:"results"`
}

func main() {
	// Get port from env or use default
	port := os.Getenv("SKILL_PORT")
	if port == "" {
		port = "50099"
	}

	// Create skill server
	server := grpc.NewSkillServer("skill-replicate", "1.0.0")

	// Register executors with schemas
	server.RegisterExecutorWithSchema("replicate-run", &RunExecutor{}, RunSchema)
	server.RegisterExecutorWithSchema("replicate-prediction-status", &PredictionStatusExecutor{}, PredictionStatusSchema)
	server.RegisterExecutorWithSchema("replicate-prediction-cancel", &PredictionCancelExecutor{}, PredictionCancelSchema)
	server.RegisterExecutorWithSchema("replicate-model-list", &ModelListExecutor{}, ModelListSchema)
	server.RegisterExecutorWithSchema("replicate-model-get", &ModelGetExecutor{}, ModelGetSchema)
	server.RegisterExecutorWithSchema("replicate-training-create", &TrainingCreateExecutor{}, TrainingCreateSchema)
	server.RegisterExecutorWithSchema("replicate-training-status", &TrainingStatusExecutor{}, TrainingStatusSchema)

	fmt.Printf("Starting skill-replicate gRPC server on port %s\n", port)
	if err := server.Serve(port); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to serve: %v\n", err)
		os.Exit(1)
	}
}

// ============================================================================
// REPLICATE CLIENT HELPERS
// ============================================================================

// getReplicateClient returns a Replicate client (cached)
func getReplicateClient(apiKey, baseURL string) (*ReplicateClient, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("Replicate API key is required")
	}

	cacheKey := fmt.Sprintf("%s:%s", apiKey, baseURL)

	clientMux.RLock()
	client, ok := clients[cacheKey]
	clientMux.RUnlock()

	if ok {
		return client, nil
	}

	clientMux.Lock()
	defer clientMux.Unlock()

	// Double check
	if client, ok := clients[cacheKey]; ok {
		return client, nil
	}

	if baseURL == "" {
		baseURL = defaultBaseURL
	}

	client = &ReplicateClient{
		HTTPClient: &http.Client{
			Timeout: 5 * time.Minute,
		},
		APIKey:  apiKey,
		BaseURL: baseURL,
	}
	clients[cacheKey] = client
	return client, nil
}

// doRequest performs an HTTP request to the Replicate API
func (c *ReplicateClient) doRequest(ctx context.Context, method, path string, body interface{}) ([]byte, error) {
	var reqBody io.Reader
	if body != nil {
		jsonBody, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
		reqBody = bytes.NewReader(jsonBody)
	}

	url := c.BaseURL + path
	req, err := http.NewRequestWithContext(ctx, method, url, reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "skill-replicate/1.0.0")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode >= 400 {
		var errResp map[string]interface{}
		if err := json.Unmarshal(respBody, &errResp); err == nil {
			if detail, ok := errResp["detail"].(string); ok {
				return nil, fmt.Errorf("Replicate API error (%d): %s", resp.StatusCode, detail)
			}
		}
		return nil, fmt.Errorf("Replicate API error (%d): %s", resp.StatusCode, string(respBody))
	}

	return respBody, nil
}

// ============================================================================
// CONFIG HELPERS
// ============================================================================

// Helper to get string from config
func getString(config map[string]interface{}, key string) string {
	if v, ok := config[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// Helper to get int from config
func getInt(config map[string]interface{}, key string, def int) int {
	if v, ok := config[key]; ok {
		switch n := v.(type) {
		case float64:
			return int(n)
		case int:
			return n
		}
	}
	return def
}

// Helper to get float from config
func getFloat(config map[string]interface{}, key string, def float64) float64 {
	if v, ok := config[key]; ok {
		switch n := v.(type) {
		case float64:
			return n
		case int:
			return float64(n)
		}
	}
	return def
}

// Helper to get bool from config
func getBool(config map[string]interface{}, key string, def bool) bool {
	if v, ok := config[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return def
}

// Helper to get string slice from config
func getStringSlice(config map[string]interface{}, key string) []string {
	if v, ok := config[key]; ok {
		switch arr := v.(type) {
		case []interface{}:
			result := make([]string, 0, len(arr))
			for _, item := range arr {
				if s, ok := item.(string); ok {
					result = append(result, s)
				}
			}
			return result
		case []string:
			return arr
		case string:
			return splitString(arr)
		}
	}
	return nil
}

// Helper to get map from config
func getMap(config map[string]interface{}, key string) map[string]interface{} {
	if v, ok := config[key]; ok {
		if m, ok := v.(map[string]interface{}); ok {
			return m
		}
	}
	return nil
}

// Helper to get interface slice from config
func getInterfaceSlice(config map[string]interface{}, key string) []interface{} {
	if v, ok := config[key]; ok {
		if arr, ok := v.([]interface{}); ok {
			return arr
		}
	}
	return nil
}

// Helper to split string by comma or newline
func splitString(s string) []string {
	if s == "" {
		return nil
	}
	// Try comma first
	result := []string{}
	for _, part := range splitByComma(s) {
		trimmed := trimString(part)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func splitByComma(s string) []string {
	result := []string{}
	current := ""
	for _, r := range s {
		if r == ',' {
			result = append(result, current)
			current = ""
		} else {
			current += string(r)
		}
	}
	result = append(result, current)
	return result
}

func trimString(s string) string {
	result := ""
	started := false
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			if started {
				result += string(r)
			}
		} else {
			started = true
			result += string(r)
		}
	}
	// Trim trailing whitespace
	trimmed := ""
	for i := len(result) - 1; i >= 0; i-- {
		r := result[i]
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			continue
		}
		trimmed = result[:i+1]
		break
	}
	return trimmed
}

// ============================================================================
// SCHEMAS
// ============================================================================

// RunSchema is the UI schema for replicate-run
var RunSchema = resolver.NewSchemaBuilder("replicate-run").
	WithName("Run Model").
	WithCategory("action").
	WithIcon(iconReplicate).
	WithDescription("Run a Replicate model and get the prediction result").
	AddSection("Connection").
		AddExpressionField("apiKey", "API Key",
			resolver.WithRequired(),
			resolver.WithPlaceholder("r8_..."),
			resolver.WithHint("Replicate API key (supports {{bindings.xxx}})"),
			resolver.WithSensitive(),
		).
		AddExpressionField("baseURL", "Base URL",
			resolver.WithPlaceholder("https://api.replicate.com/v1"),
			resolver.WithHint("Optional: Custom base URL for Replicate API"),
		).
		EndSection().
	AddSection("Model").
		AddExpressionField("model", "Model",
			resolver.WithRequired(),
			resolver.WithPlaceholder("meta/llama-2-70b-chat"),
			resolver.WithHint("Model identifier in format owner/name or model version ID"),
		).
		AddExpressionField("version", "Version",
			resolver.WithPlaceholder("Model version ID"),
			resolver.WithHint("Optional: Specific model version ID (overrides model)"),
		).
		EndSection().
	AddSection("Input").
		AddJSONField("input", "Input",
			resolver.WithRequired(),
			resolver.WithHeight(200),
			resolver.WithHint(`Model input parameters as JSON object: {"prompt": "Hello", "max_tokens": 100}`),
		).
		EndSection().
	AddSection("Parameters").
		AddSelectField("webhook", "Webhook",
			[]resolver.SelectOption{},
			resolver.WithPlaceholder("https://your-server.com/webhook"),
			resolver.WithHint("Optional webhook URL for async notifications"),
		).
		AddSelectField("webhookEventsFilter", "Webhook Events",
			[]resolver.SelectOption{
				{Label: "Start", Value: "start"},
				{Label: "Output", Value: "output"},
				{Label: "Logs", Value: "logs"},
				{Label: "Completed", Value: "completed"},
			},
			resolver.WithHint("Events to trigger webhook (comma-separated)"),
		).
		AddToggleField("stream", "Stream",
			resolver.WithDefault(false),
			resolver.WithHint("Enable streaming output"),
		).
		EndSection().
	AddSection("Polling").
		AddToggleField("wait", "Wait for Completion",
			resolver.WithDefault(true),
			resolver.WithHint("Wait for prediction to complete before returning"),
		).
		AddNumberField("pollInterval", "Poll Interval (seconds)",
			resolver.WithDefault(1),
			resolver.WithHint("How often to poll for status when waiting"),
		).
		AddNumberField("pollTimeout", "Poll Timeout (seconds)",
			resolver.WithDefault(300),
			resolver.WithHint("Maximum time to wait for completion"),
		).
		EndSection().
	Build()

// PredictionStatusSchema is the UI schema for replicate-prediction-status
var PredictionStatusSchema = resolver.NewSchemaBuilder("replicate-prediction-status").
	WithName("Get Prediction Status").
	WithCategory("action").
	WithIcon(iconReplicate).
	WithDescription("Get the status of a Replicate prediction").
	AddSection("Connection").
		AddExpressionField("apiKey", "API Key",
			resolver.WithRequired(),
			resolver.WithPlaceholder("r8_..."),
			resolver.WithHint("Replicate API key (supports {{bindings.xxx}})"),
			resolver.WithSensitive(),
		).
		AddExpressionField("baseURL", "Base URL",
			resolver.WithPlaceholder("https://api.replicate.com/v1"),
			resolver.WithHint("Optional: Custom base URL for Replicate API"),
		).
		EndSection().
	AddSection("Prediction").
		AddExpressionField("predictionId", "Prediction ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("Prediction ID to check"),
			resolver.WithHint("ID of the prediction to get status for"),
		).
		EndSection().
	Build()

// PredictionCancelSchema is the UI schema for replicate-prediction-cancel
var PredictionCancelSchema = resolver.NewSchemaBuilder("replicate-prediction-cancel").
	WithName("Cancel Prediction").
	WithCategory("action").
	WithIcon(iconReplicate).
	WithDescription("Cancel a running Replicate prediction").
	AddSection("Connection").
		AddExpressionField("apiKey", "API Key",
			resolver.WithRequired(),
			resolver.WithPlaceholder("r8_..."),
			resolver.WithHint("Replicate API key (supports {{bindings.xxx}})"),
			resolver.WithSensitive(),
		).
		AddExpressionField("baseURL", "Base URL",
			resolver.WithPlaceholder("https://api.replicate.com/v1"),
			resolver.WithHint("Optional: Custom base URL for Replicate API"),
		).
		EndSection().
	AddSection("Prediction").
		AddExpressionField("predictionId", "Prediction ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("Prediction ID to cancel"),
			resolver.WithHint("ID of the prediction to cancel"),
		).
		EndSection().
	Build()

// ModelListSchema is the UI schema for replicate-model-list
var ModelListSchema = resolver.NewSchemaBuilder("replicate-model-list").
	WithName("List Models").
	WithCategory("query").
	WithIcon(iconReplicate).
	WithDescription("List available Replicate models").
	AddSection("Connection").
		AddExpressionField("apiKey", "API Key",
			resolver.WithRequired(),
			resolver.WithPlaceholder("r8_..."),
			resolver.WithHint("Replicate API key (supports {{bindings.xxx}})"),
			resolver.WithSensitive(),
		).
		AddExpressionField("baseURL", "Base URL",
			resolver.WithPlaceholder("https://api.replicate.com/v1"),
			resolver.WithHint("Optional: Custom base URL for Replicate API"),
		).
		EndSection().
	AddSection("Filtering").
		AddExpressionField("cursor", "Cursor",
			resolver.WithPlaceholder("Pagination cursor"),
			resolver.WithHint("Optional cursor for pagination"),
		).
		EndSection().
	Build()

// ModelGetSchema is the UI schema for replicate-model-get
var ModelGetSchema = resolver.NewSchemaBuilder("replicate-model-get").
	WithName("Get Model").
	WithCategory("query").
	WithIcon(iconReplicate).
	WithDescription("Get details of a specific Replicate model").
	AddSection("Connection").
		AddExpressionField("apiKey", "API Key",
			resolver.WithRequired(),
			resolver.WithPlaceholder("r8_..."),
			resolver.WithHint("Replicate API key (supports {{bindings.xxx}})"),
			resolver.WithSensitive(),
		).
		AddExpressionField("baseURL", "Base URL",
			resolver.WithPlaceholder("https://api.replicate.com/v1"),
			resolver.WithHint("Optional: Custom base URL for Replicate API"),
		).
		EndSection().
	AddSection("Model").
		AddExpressionField("model", "Model",
			resolver.WithRequired(),
			resolver.WithPlaceholder("owner/name"),
			resolver.WithHint("Model identifier in format owner/name"),
		).
		EndSection().
	Build()

// TrainingCreateSchema is the UI schema for replicate-training-create
var TrainingCreateSchema = resolver.NewSchemaBuilder("replicate-training-create").
	WithName("Create Training").
	WithCategory("action").
	WithIcon(iconReplicate).
	WithDescription("Create a new training job on Replicate").
	AddSection("Connection").
		AddExpressionField("apiKey", "API Key",
			resolver.WithRequired(),
			resolver.WithPlaceholder("r8_..."),
			resolver.WithHint("Replicate API key (supports {{bindings.xxx}})"),
			resolver.WithSensitive(),
		).
		AddExpressionField("baseURL", "Base URL",
			resolver.WithPlaceholder("https://api.replicate.com/v1"),
			resolver.WithHint("Optional: Custom base URL for Replicate API"),
		).
		EndSection().
	AddSection("Model").
		AddExpressionField("model", "Model",
			resolver.WithRequired(),
			resolver.WithPlaceholder("owner/name"),
			resolver.WithHint("Model identifier in format owner/name"),
		).
		AddExpressionField("version", "Version",
			resolver.WithRequired(),
			resolver.WithPlaceholder("Model version ID"),
			resolver.WithHint("Model version ID to train"),
		).
		EndSection().
	AddSection("Destination").
		AddExpressionField("destination", "Destination",
			resolver.WithRequired(),
			resolver.WithPlaceholder("owner/new-model-name"),
			resolver.WithHint("Destination model for the trained weights"),
		).
		EndSection().
	AddSection("Input").
		AddJSONField("input", "Input",
			resolver.WithRequired(),
			resolver.WithHeight(200),
			resolver.WithHint(`Training input parameters as JSON object`),
		).
		EndSection().
	AddSection("Parameters").
		AddExpressionField("webhook", "Webhook",
			resolver.WithPlaceholder("https://your-server.com/webhook"),
			resolver.WithHint("Optional webhook URL for async notifications"),
		).
		EndSection().
	Build()

// TrainingStatusSchema is the UI schema for replicate-training-status
var TrainingStatusSchema = resolver.NewSchemaBuilder("replicate-training-status").
	WithName("Get Training Status").
	WithCategory("action").
	WithIcon(iconReplicate).
	WithDescription("Get the status of a Replicate training job").
	AddSection("Connection").
		AddExpressionField("apiKey", "API Key",
			resolver.WithRequired(),
			resolver.WithPlaceholder("r8_..."),
			resolver.WithHint("Replicate API key (supports {{bindings.xxx}})"),
			resolver.WithSensitive(),
		).
		AddExpressionField("baseURL", "Base URL",
			resolver.WithPlaceholder("https://api.replicate.com/v1"),
			resolver.WithHint("Optional: Custom base URL for Replicate API"),
		).
		EndSection().
	AddSection("Training").
		AddExpressionField("trainingId", "Training ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("Training ID to check"),
			resolver.WithHint("ID of the training job to get status for"),
		).
		EndSection().
	Build()

// ============================================================================
// EXECUTORS
// ============================================================================

// RunExecutor handles replicate-run
type RunExecutor struct{}

// Type returns the executor type
func (e *RunExecutor) Type() string {
	return "replicate-run"
}

// Execute runs a model prediction
func (e *RunExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	// Get connection parameters
	apiKey := resolver.ResolveString(getString(config, "apiKey"))
	baseURL := resolver.ResolveString(getString(config, "baseURL"))

	client, err := getReplicateClient(apiKey, baseURL)
	if err != nil {
		return nil, err
	}

	// Get model
	model := resolver.ResolveString(getString(config, "model"))
	if model == "" {
		return nil, fmt.Errorf("model is required")
	}

	// Get version (optional)
	version := resolver.ResolveString(getString(config, "version"))

	// Get input
	inputRaw := getMap(config, "input")
	if inputRaw == nil {
		return nil, fmt.Errorf("input is required")
	}

	// Build prediction request
	req := map[string]interface{}{
		"input": inputRaw,
	}

	if version != "" {
		req["version"] = version
	} else {
		req["model"] = model
	}

	// Add webhook if provided
	webhook := resolver.ResolveString(getString(config, "webhook"))
	if webhook != "" {
		req["webhook"] = webhook
	}

	// Add webhook events filter
	webhookEvents := getStringSlice(config, "webhookEventsFilter")
	if len(webhookEvents) > 0 {
		req["webhook_events_filter"] = webhookEvents
	}

	// Add stream flag
	if getBool(config, "stream", false) {
		req["stream"] = true
	}

	// Create prediction
	respBody, err := client.doRequest(ctx, "POST", "/predictions", req)
	if err != nil {
		return nil, fmt.Errorf("failed to create prediction: %w", err)
	}

	var prediction ReplicatePrediction
	if err := json.Unmarshal(respBody, &prediction); err != nil {
		return nil, fmt.Errorf("failed to parse prediction response: %w", err)
	}

	// Check if we should wait for completion
	wait := getBool(config, "wait", true)
	if wait {
		pollInterval := getInt(config, "pollInterval", 1)
		pollTimeout := getInt(config, "pollTimeout", 300)

		pred, err := waitForPrediction(ctx, client, prediction.ID, pollInterval, pollTimeout)
		if err != nil {
			return nil, err
		}
		prediction = *pred
	}

	// Build result
	result := buildPredictionResult(&prediction)

	return &executor.StepResult{
		Output: result,
	}, nil
}

// waitForPrediction polls for prediction completion
func waitForPrediction(ctx context.Context, client *ReplicateClient, predictionID string, pollInterval, pollTimeout int) (*ReplicatePrediction, error) {
	timeout := time.After(time.Duration(pollTimeout) * time.Second)
	ticker := time.NewTicker(time.Duration(pollInterval) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("context cancelled while waiting for prediction")
		case <-timeout:
			return nil, fmt.Errorf("timeout waiting for prediction to complete")
		case <-ticker.C:
			respBody, err := client.doRequest(ctx, "GET", "/predictions/"+predictionID, nil)
			if err != nil {
				return nil, fmt.Errorf("failed to get prediction status: %w", err)
			}

			var prediction ReplicatePrediction
			if err := json.Unmarshal(respBody, &prediction); err != nil {
				return nil, fmt.Errorf("failed to parse prediction response: %w", err)
			}

			// Check if prediction is complete
			if prediction.Status == "succeeded" || prediction.Status == "failed" || prediction.Status == "canceled" {
				return &prediction, nil
			}
		}
	}
}

// buildPredictionResult builds the result map from a prediction
func buildPredictionResult(prediction *ReplicatePrediction) map[string]interface{} {
	result := map[string]interface{}{
		"id":        prediction.ID,
		"model":     prediction.Model,
		"version":   prediction.Version,
		"status":    prediction.Status,
		"createdAt": prediction.CreatedAt,
		"output":    prediction.Output,
	}

	if prediction.StartedAt != "" {
		result["startedAt"] = prediction.StartedAt
	}
	if prediction.CompletedAt != "" {
		result["completedAt"] = prediction.CompletedAt
	}
	if prediction.Error != "" {
		result["error"] = prediction.Error
	}
	if prediction.Logs != "" {
		result["logs"] = prediction.Logs
	}
	if prediction.Metrics != nil {
		result["metrics"] = prediction.Metrics
	}
	if prediction.URLs != nil {
		result["urls"] = prediction.URLs
	}

	return result
}

// PredictionStatusExecutor handles replicate-prediction-status
type PredictionStatusExecutor struct{}

// Type returns the executor type
func (e *PredictionStatusExecutor) Type() string {
	return "replicate-prediction-status"
}

// Execute gets the status of a prediction
func (e *PredictionStatusExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	// Get connection parameters
	apiKey := resolver.ResolveString(getString(config, "apiKey"))
	baseURL := resolver.ResolveString(getString(config, "baseURL"))

	client, err := getReplicateClient(apiKey, baseURL)
	if err != nil {
		return nil, err
	}

	// Get prediction ID
	predictionID := resolver.ResolveString(getString(config, "predictionId"))
	if predictionID == "" {
		return nil, fmt.Errorf("predictionId is required")
	}

	// Get prediction
	respBody, err := client.doRequest(ctx, "GET", "/predictions/"+predictionID, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get prediction: %w", err)
	}

	var prediction ReplicatePrediction
	if err := json.Unmarshal(respBody, &prediction); err != nil {
		return nil, fmt.Errorf("failed to parse prediction response: %w", err)
	}

	result := buildPredictionResult(&prediction)

	return &executor.StepResult{
		Output: result,
	}, nil
}

// PredictionCancelExecutor handles replicate-prediction-cancel
type PredictionCancelExecutor struct{}

// Type returns the executor type
func (e *PredictionCancelExecutor) Type() string {
	return "replicate-prediction-cancel"
}

// Execute cancels a prediction
func (e *PredictionCancelExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	// Get connection parameters
	apiKey := resolver.ResolveString(getString(config, "apiKey"))
	baseURL := resolver.ResolveString(getString(config, "baseURL"))

	client, err := getReplicateClient(apiKey, baseURL)
	if err != nil {
		return nil, err
	}

	// Get prediction ID
	predictionID := resolver.ResolveString(getString(config, "predictionId"))
	if predictionID == "" {
		return nil, fmt.Errorf("predictionId is required")
	}

	// Cancel prediction
	respBody, err := client.doRequest(ctx, "POST", "/predictions/"+predictionID+"/cancel", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to cancel prediction: %w", err)
	}

	var prediction ReplicatePrediction
	if err := json.Unmarshal(respBody, &prediction); err != nil {
		return nil, fmt.Errorf("failed to parse prediction response: %w", err)
	}

	result := buildPredictionResult(&prediction)

	return &executor.StepResult{
		Output: result,
	}, nil
}

// ModelListExecutor handles replicate-model-list
type ModelListExecutor struct{}

// Type returns the executor type
func (e *ModelListExecutor) Type() string {
	return "replicate-model-list"
}

// Execute lists available models
func (e *ModelListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	// Get connection parameters
	apiKey := resolver.ResolveString(getString(config, "apiKey"))
	baseURL := resolver.ResolveString(getString(config, "baseURL"))

	client, err := getReplicateClient(apiKey, baseURL)
	if err != nil {
		return nil, err
	}

	// Build path with optional cursor
	path := "/models"
	cursor := resolver.ResolveString(getString(config, "cursor"))
	if cursor != "" {
		path = cursor
	}

	// Get models
	respBody, err := client.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list models: %w", err)
	}

	var listResp ReplicateListResponse
	if err := json.Unmarshal(respBody, &listResp); err != nil {
		return nil, fmt.Errorf("failed to parse models response: %w", err)
	}

	// Parse results
	var models []ReplicateModel
	if err := json.Unmarshal(listResp.Results, &models); err != nil {
		return nil, fmt.Errorf("failed to parse model results: %w", err)
	}

	// Build result
	modelList := make([]map[string]interface{}, 0, len(models))
	for _, model := range models {
		modelMap := map[string]interface{}{
			"owner":       model.Owner,
			"name":        model.Name,
			"description": model.Description,
			"visibility":  model.Visibility,
			"runCount":    model.RunCount,
		}
		if model.GithubURL != "" {
			modelMap["githubUrl"] = model.GithubURL
		}
		if model.PaperURL != "" {
			modelMap["paperUrl"] = model.PaperURL
		}
		if model.LicenseURL != "" {
			modelMap["licenseUrl"] = model.LicenseURL
		}
		if model.CoverImage != "" {
			modelMap["coverImageUrl"] = model.CoverImage
		}
		if model.LatestVersion != nil {
			modelMap["latestVersion"] = map[string]interface{}{
				"id":         model.LatestVersion.ID,
				"createdAt":  model.LatestVersion.CreatedAt,
				"cogVersion": model.LatestVersion.CogVersion,
			}
		}
		modelList = append(modelList, modelMap)
	}

	result := map[string]interface{}{
		"models": modelList,
	}
	if listResp.Next != "" {
		result["nextCursor"] = listResp.Next
	}
	if listResp.Previous != "" {
		result["previousCursor"] = listResp.Previous
	}

	return &executor.StepResult{
		Output: result,
	}, nil
}

// ModelGetExecutor handles replicate-model-get
type ModelGetExecutor struct{}

// Type returns the executor type
func (e *ModelGetExecutor) Type() string {
	return "replicate-model-get"
}

// Execute gets details of a specific model
func (e *ModelGetExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	// Get connection parameters
	apiKey := resolver.ResolveString(getString(config, "apiKey"))
	baseURL := resolver.ResolveString(getString(config, "baseURL"))

	client, err := getReplicateClient(apiKey, baseURL)
	if err != nil {
		return nil, err
	}

	// Get model
	model := resolver.ResolveString(getString(config, "model"))
	if model == "" {
		return nil, fmt.Errorf("model is required")
	}

	// Get model
	respBody, err := client.doRequest(ctx, "GET", "/models/"+model, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get model: %w", err)
	}

	var modelObj ReplicateModel
	if err := json.Unmarshal(respBody, &modelObj); err != nil {
		return nil, fmt.Errorf("failed to parse model response: %w", err)
	}

	result := map[string]interface{}{
		"owner":       modelObj.Owner,
		"name":        modelObj.Name,
		"description": modelObj.Description,
		"visibility":  modelObj.Visibility,
		"runCount":    modelObj.RunCount,
	}
	if modelObj.GithubURL != "" {
		result["githubUrl"] = modelObj.GithubURL
	}
	if modelObj.PaperURL != "" {
		result["paperUrl"] = modelObj.PaperURL
	}
	if modelObj.LicenseURL != "" {
		result["licenseUrl"] = modelObj.LicenseURL
	}
	if modelObj.CoverImage != "" {
		result["coverImageUrl"] = modelObj.CoverImage
	}
	if modelObj.LatestVersion != nil {
		result["latestVersion"] = map[string]interface{}{
			"id":         modelObj.LatestVersion.ID,
			"createdAt":  modelObj.LatestVersion.CreatedAt,
			"cogVersion": modelObj.LatestVersion.CogVersion,
		}
	}

	return &executor.StepResult{
		Output: result,
	}, nil
}

// TrainingCreateExecutor handles replicate-training-create
type TrainingCreateExecutor struct{}

// Type returns the executor type
func (e *TrainingCreateExecutor) Type() string {
	return "replicate-training-create"
}

// Execute creates a new training job
func (e *TrainingCreateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	// Get connection parameters
	apiKey := resolver.ResolveString(getString(config, "apiKey"))
	baseURL := resolver.ResolveString(getString(config, "baseURL"))

	client, err := getReplicateClient(apiKey, baseURL)
	if err != nil {
		return nil, err
	}

	// Get model
	model := resolver.ResolveString(getString(config, "model"))
	if model == "" {
		return nil, fmt.Errorf("model is required")
	}

	// Get version
	version := resolver.ResolveString(getString(config, "version"))
	if version == "" {
		return nil, fmt.Errorf("version is required")
	}

	// Get destination
	destination := resolver.ResolveString(getString(config, "destination"))
	if destination == "" {
		return nil, fmt.Errorf("destination is required")
	}

	// Get input
	inputRaw := getMap(config, "input")
	if inputRaw == nil {
		return nil, fmt.Errorf("input is required")
	}

	// Build training request
	req := map[string]interface{}{
		"version":     version,
		"destination": destination,
		"input":       inputRaw,
	}

	// Add webhook if provided
	webhook := resolver.ResolveString(getString(config, "webhook"))
	if webhook != "" {
		req["webhook"] = webhook
	}

	// Create training
	respBody, err := client.doRequest(ctx, "POST", "/trainings", req)
	if err != nil {
		return nil, fmt.Errorf("failed to create training: %w", err)
	}

	var training ReplicateTraining
	if err := json.Unmarshal(respBody, &training); err != nil {
		return nil, fmt.Errorf("failed to parse training response: %w", err)
	}

	result := buildTrainingResult(&training)

	return &executor.StepResult{
		Output: result,
	}, nil
}

// TrainingStatusExecutor handles replicate-training-status
type TrainingStatusExecutor struct{}

// Type returns the executor type
func (e *TrainingStatusExecutor) Type() string {
	return "replicate-training-status"
}

// Execute gets the status of a training job
func (e *TrainingStatusExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	// Get connection parameters
	apiKey := resolver.ResolveString(getString(config, "apiKey"))
	baseURL := resolver.ResolveString(getString(config, "baseURL"))

	client, err := getReplicateClient(apiKey, baseURL)
	if err != nil {
		return nil, err
	}

	// Get training ID
	trainingID := resolver.ResolveString(getString(config, "trainingId"))
	if trainingID == "" {
		return nil, fmt.Errorf("trainingId is required")
	}

	// Get training
	respBody, err := client.doRequest(ctx, "GET", "/trainings/"+trainingID, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get training: %w", err)
	}

	var training ReplicateTraining
	if err := json.Unmarshal(respBody, &training); err != nil {
		return nil, fmt.Errorf("failed to parse training response: %w", err)
	}

	result := buildTrainingResult(&training)

	return &executor.StepResult{
		Output: result,
	}, nil
}

// buildTrainingResult builds the result map from a training
func buildTrainingResult(training *ReplicateTraining) map[string]interface{} {
	result := map[string]interface{}{
		"id":          training.ID,
		"model":       training.Model,
		"version":     training.Version,
		"destination": training.Destination,
		"status":      training.Status,
		"createdAt":   training.CreatedAt,
		"output":      training.Output,
	}

	if training.StartedAt != "" {
		result["startedAt"] = training.StartedAt
	}
	if training.CompletedAt != "" {
		result["completedAt"] = training.CompletedAt
	}
	if training.Error != "" {
		result["error"] = training.Error
	}
	if training.Logs != "" {
		result["logs"] = training.Logs
	}
	if training.Metrics != nil {
		result["metrics"] = training.Metrics
	}
	if training.URLs != nil {
		result["urls"] = training.URLs
	}

	return result
}
