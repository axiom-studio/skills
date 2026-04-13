package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"

	"github.com/axiom-studio/skills.sdk/executor"
	"github.com/axiom-studio/skills.sdk/grpc"
	"github.com/axiom-studio/skills.sdk/resolver"
)

const (
	PostmanAPIBase = "https://api.getpostman.com"
	iconPostman    = "send"
)

// PostmanClient represents a Postman API client
type PostmanClient struct {
	APIKey string
}

// Client cache
var (
	clients     = make(map[string]*PostmanClient)
	clientMutex sync.RWMutex
)

// ============================================================================
// POSTMAN API DATA STRUCTURES
// ============================================================================

// Collection represents a Postman collection
type Collection struct {
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	UID       string          `json:"uid,omitempty"`
	CreatedAt string          `json:"createdAt,omitempty"`
	UpdatedAt string          `json:"updatedAt,omitempty"`
	Collection json.RawMessage `json:"collection,omitempty"`
}

// CollectionListResponse represents the response from list collections
type CollectionListResponse struct {
	Collections []CollectionSummary `json:"collections"`
}

// CollectionSummary is a summary of a collection
type CollectionSummary struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	UID  string `json:"uid"`
}

// CollectionGetResponse represents the response from get collection
type CollectionGetResponse struct {
	Collection Collection `json:"collection"`
}

// CollectionRunRequest represents a request to run a collection
type CollectionRunRequest struct {
	Collection  json.RawMessage `json:"collection"`
	Environment json.RawMessage `json:"environment,omitempty"`
	Globals     json.RawMessage `json:"globals,omitempty"`
	Options     RunOptions      `json:"options"`
}

// RunOptions represents options for running a collection
type RunOptions struct {
	IterationCount int  `json:"iterationCount,omitempty"`
	Folder         string `json:"folder,omitempty"`
}

// CollectionRunResponse represents the response from running a collection
type CollectionRunResponse struct {
	RunID     string                 `json:"runId,omitempty"`
	Status    string                 `json:"status,omitempty"`
	Results   []TestResult           `json:"results,omitempty"`
	Summary   *RunSummary            `json:"summary,omitempty"`
}

// TestResult represents a single test result
type TestResult struct {
	Request     *RequestInfo  `json:"request,omitempty"`
	Response    *ResponseInfo `json:"response,omitempty"`
	Assertions  []Assertion   `json:"assertions,omitempty"`
	Error       string        `json:"error,omitempty"`
}

// RequestInfo contains request details
type RequestInfo struct {
	Name   string `json:"name,omitempty"`
	Method string `json:"method,omitempty"`
	URL    string `json:"url,omitempty"`
}

// ResponseInfo contains response details
type ResponseInfo struct {
	Status string `json:"status,omitempty"`
	Code   int    `json:"code,omitempty"`
}

// Assertion represents a test assertion
type Assertion struct {
	Assertion string `json:"assertion,omitempty"`
	Skipped   bool   `json:"skipped,omitempty"`
	Error     string `json:"error,omitempty"`
}

// RunSummary contains summary of a collection run
type RunSummary struct {
	Stats       *Stats       `json:"stats,omitempty"`
	Collectio   *Collection  `json:"collection,omitempty"`
}

// Stats contains run statistics
type Stats struct {
	Iterations   int `json:"iterations,omitempty"`
	Items        int `json:"items,omitempty"`
	Scripts      int `json:"scripts,omitempty"`
	PrerequestScripts int `json:"prerequestScripts,omitempty"`
	Assertions   int `json:"assertions,omitempty"`
	Failed       int `json:"failed,omitempty"`
	Passed       int `json:"passed,omitempty"`
}

// Environment represents a Postman environment
type Environment struct {
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	UID       string          `json:"uid,omitempty"`
	CreatedAt string          `json:"createdAt,omitempty"`
	UpdatedAt string          `json:"updatedAt,omitempty"`
	Variables []EnvVariable   `json:"values,omitempty"`
}

// EnvironmentListResponse represents the response from list environments
type EnvironmentListResponse struct {
	Environments []EnvironmentSummary `json:"environments"`
}

// EnvironmentSummary is a summary of an environment
type EnvironmentSummary struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	UID  string `json:"uid"`
}

// EnvironmentGetResponse represents the response from get environment
type EnvironmentGetResponse struct {
	Environment Environment `json:"environment"`
}

// EnvVariable represents an environment variable
type EnvVariable struct {
	Key     string `json:"key"`
	Value   string `json:"value"`
	Enabled bool   `json:"enabled"`
	Type    string `json:"type,omitempty"`
}

// Monitor represents a Postman monitor
type Monitor struct {
	ID             string          `json:"id"`
	Name           string          `json:"name"`
	CreatedAt      string          `json:"createdAt,omitempty"`
	UpdatedAt      string          `json:"updatedAt,omitempty"`
	Collection     *MonitorCollection `json:"collection,omitempty"`
	Schedule       *MonitorSchedule `json:"schedule,omitempty"`
	Environment    *MonitorEnvironment `json:"environment,omitempty"`
	NextRunAt      string          `json:"nextRunAt,omitempty"`
	LastRunAt      string          `json:"lastRunAt,omitempty"`
	LastRunStatus  string          `json:"lastRunStatus,omitempty"`
}

// MonitorCollection represents the collection being monitored
type MonitorCollection struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	UID  string `json:"uid,omitempty"`
}

// MonitorSchedule represents the monitor schedule
type MonitorSchedule struct {
	Interval int    `json:"interval,omitempty"`
	Unit     string `json:"unit,omitempty"`
	Cron     string `json:"cron,omitempty"`
}

// MonitorEnvironment represents the environment used by monitor
type MonitorEnvironment struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	UID  string `json:"uid,omitempty"`
}

// MonitorListResponse represents the response from list monitors
type MonitorListResponse struct {
	Monitors []Monitor `json:"monitors"`
}

// MonitorRunRequest represents a request to run a monitor
type MonitorRunRequest struct {
	MonitorID   string `json:"monitorId"`
	Environment string `json:"environment,omitempty"`
}

// MonitorRunResponse represents the response from running a monitor
type MonitorRunResponse struct {
	RunID   string      `json:"runId,omitempty"`
	Status  string      `json:"status,omitempty"`
	Results []TestResult `json:"results,omitempty"`
}

// API represents a Postman API definition
type API struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	UID         string     `json:"uid,omitempty"`
	CreatedAt   string     `json:"createdAt,omitempty"`
	UpdatedAt   string     `json:"updatedAt,omitempty"`
	Spec        *APISpec   `json:"spec,omitempty"`
	Environment *APIEnvironment `json:"environment,omitempty"`
}

// APISpec represents the API specification
type APISpec struct {
	Type    string `json:"type,omitempty"`
	Format  string `json:"format,omitempty"`
}

// APIEnvironment represents the API environment
type APIEnvironment struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// APIListResponse represents the response from list APIs
type APIListResponse struct {
	Apis []API `json:"apis"`
}

// PostmanErrorResponse represents an error response from Postman API
type PostmanErrorResponse struct {
	Error struct {
		Name    string `json:"name"`
		Message string `json:"message"`
	} `json:"error"`
}

// ============================================================================
// CLIENT METHODS
// ============================================================================

// getClient returns or creates a Postman client (cached)
func getClient(apiKey string) *PostmanClient {
	clientMutex.RLock()
	client, ok := clients[apiKey]
	clientMutex.RUnlock()

	if ok {
		return client
	}

	clientMutex.Lock()
	defer clientMutex.Unlock()

	// Double check
	if client, ok := clients[apiKey]; ok {
		return client
	}

	client = &PostmanClient{APIKey: apiKey}
	clients[apiKey] = client
	return client
}

// doRequest performs an HTTP request to the Postman API
func (c *PostmanClient) doRequest(ctx context.Context, method, path string, body interface{}) (*http.Response, error) {
	var reqBody io.Reader
	if body != nil {
		jsonData, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
		reqBody = bytes.NewBuffer(jsonData)
	}

	reqURL := PostmanAPIBase + path
	req, err := http.NewRequestWithContext(ctx, method, reqURL, reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("X-Api-Key", c.APIKey)
	req.Header.Set("Content-Type", "application/json")

	httpClient := &http.Client{
		Timeout: 60 * time.Second,
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	return resp, nil
}

// readResponseBody reads and parses the response body
func readResponseBody(resp *http.Response, result interface{}) error {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode >= 400 {
		var errResp PostmanErrorResponse
		if err := json.Unmarshal(body, &errResp); err == nil && errResp.Error.Message != "" {
			return fmt.Errorf("Postman API error (%d): %s - %s", resp.StatusCode, errResp.Error.Name, errResp.Error.Message)
		}
		return fmt.Errorf("Postman API error (%d): %s", resp.StatusCode, string(body))
	}

	if result != nil {
		if err := json.Unmarshal(body, result); err != nil {
			return fmt.Errorf("failed to decode response: %w", err)
		}
	}

	return nil
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

// splitString splits a string by comma
func splitString(s string) []string {
	if s == "" {
		return nil
	}
	result := []string{}
	for _, part := range splitByComma(s) {
		trimmed := trimSpace(part)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

// splitByComma splits a string by comma (simple implementation)
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
	if current != "" {
		result = append(result, current)
	}
	return result
}

// trimSpace trims spaces from a string
func trimSpace(s string) string {
	start := 0
	end := len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t' || s[start] == '\n' || s[start] == '\r') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\n' || s[end-1] == '\r') {
		end--
	}
	return s[start:end]
}

// ============================================================================
// SCHEMAS
// ============================================================================

// PostmanCollectionListSchema is the UI schema for postman-collection-list
var PostmanCollectionListSchema = resolver.NewSchemaBuilder("postman-collection-list").
	WithName("List Collections").
	WithCategory("action").
	WithIcon(iconPostman).
	WithDescription("List all Postman collections in your workspace").
	AddSection("Connection").
		AddExpressionField("apiKey", "API Key",
			resolver.WithRequired(),
			resolver.WithPlaceholder("PMAK-..."),
			resolver.WithHint("Postman API key (supports {{bindings.xxx}})"),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Options").
		AddTagsField("types", "Collection Types",
			resolver.WithHint("Filter by collection types (e.g., collection, folder)"),
		).
		AddNumberField("limit", "Limit",
			resolver.WithDefault(100),
			resolver.WithMinMax(1, 1000),
			resolver.WithHint("Maximum number of collections to return"),
		).
		EndSection().
	Build()

// PostmanCollectionGetSchema is the UI schema for postman-collection-get
var PostmanCollectionGetSchema = resolver.NewSchemaBuilder("postman-collection-get").
	WithName("Get Collection").
	WithCategory("action").
	WithIcon(iconPostman).
	WithDescription("Get details of a specific Postman collection").
	AddSection("Connection").
		AddExpressionField("apiKey", "API Key",
			resolver.WithRequired(),
			resolver.WithPlaceholder("PMAK-..."),
			resolver.WithHint("Postman API key"),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Collection").
		AddExpressionField("collectionId", "Collection ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("abc123..."),
			resolver.WithHint("ID of the collection to retrieve"),
		).
		EndSection().
	Build()

// PostmanCollectionRunSchema is the UI schema for postman-collection-run
var PostmanCollectionRunSchema = resolver.NewSchemaBuilder("postman-collection-run").
	WithName("Run Collection").
	WithCategory("action").
	WithIcon(iconPostman).
	WithDescription("Run a Postman collection and get test results").
	AddSection("Connection").
		AddExpressionField("apiKey", "API Key",
			resolver.WithRequired(),
			resolver.WithPlaceholder("PMAK-..."),
			resolver.WithHint("Postman API key"),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Collection").
		AddExpressionField("collectionId", "Collection ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("abc123..."),
			resolver.WithHint("ID of the collection to run"),
		).
		AddExpressionField("environmentId", "Environment ID",
			resolver.WithPlaceholder("env123..."),
			resolver.WithHint("Optional: Environment ID to use for the run"),
		).
		EndSection().
	AddSection("Run Options").
		AddNumberField("iterationCount", "Iteration Count",
			resolver.WithDefault(1),
			resolver.WithMinMax(1, 100),
			resolver.WithHint("Number of times to run the collection"),
		).
		AddExpressionField("folder", "Folder",
			resolver.WithPlaceholder("Folder Name"),
			resolver.WithHint("Optional: Run only a specific folder within the collection"),
		).
		EndSection().
	Build()

// PostmanEnvironmentListSchema is the UI schema for postman-environment-list
var PostmanEnvironmentListSchema = resolver.NewSchemaBuilder("postman-environment-list").
	WithName("List Environments").
	WithCategory("action").
	WithIcon(iconPostman).
	WithDescription("List all Postman environments in your workspace").
	AddSection("Connection").
		AddExpressionField("apiKey", "API Key",
			resolver.WithRequired(),
			resolver.WithPlaceholder("PMAK-..."),
			resolver.WithHint("Postman API key"),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Options").
		AddNumberField("limit", "Limit",
			resolver.WithDefault(100),
			resolver.WithMinMax(1, 1000),
			resolver.WithHint("Maximum number of environments to return"),
		).
		EndSection().
	Build()

// PostmanEnvironmentGetSchema is the UI schema for postman-environment-get
var PostmanEnvironmentGetSchema = resolver.NewSchemaBuilder("postman-environment-get").
	WithName("Get Environment").
	WithCategory("action").
	WithIcon(iconPostman).
	WithDescription("Get details of a specific Postman environment including variables").
	AddSection("Connection").
		AddExpressionField("apiKey", "API Key",
			resolver.WithRequired(),
			resolver.WithPlaceholder("PMAK-..."),
			resolver.WithHint("Postman API key"),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Environment").
		AddExpressionField("environmentId", "Environment ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("env123..."),
			resolver.WithHint("ID of the environment to retrieve"),
		).
		EndSection().
	Build()

// PostmanMonitorListSchema is the UI schema for postman-monitor-list
var PostmanMonitorListSchema = resolver.NewSchemaBuilder("postman-monitor-list").
	WithName("List Monitors").
	WithCategory("action").
	WithIcon(iconPostman).
	WithDescription("List all Postman monitors in your workspace").
	AddSection("Connection").
		AddExpressionField("apiKey", "API Key",
			resolver.WithRequired(),
			resolver.WithPlaceholder("PMAK-..."),
			resolver.WithHint("Postman API key"),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Options").
		AddNumberField("limit", "Limit",
			resolver.WithDefault(100),
			resolver.WithMinMax(1, 1000),
			resolver.WithHint("Maximum number of monitors to return"),
		).
		EndSection().
	Build()

// PostmanMonitorRunSchema is the UI schema for postman-monitor-run
var PostmanMonitorRunSchema = resolver.NewSchemaBuilder("postman-monitor-run").
	WithName("Run Monitor").
	WithCategory("action").
	WithIcon(iconPostman).
	WithDescription("Trigger a Postman monitor run and get results").
	AddSection("Connection").
		AddExpressionField("apiKey", "API Key",
			resolver.WithRequired(),
			resolver.WithPlaceholder("PMAK-..."),
			resolver.WithHint("Postman API key"),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Monitor").
		AddExpressionField("monitorId", "Monitor ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("mon123..."),
			resolver.WithHint("ID of the monitor to run"),
		).
		AddExpressionField("environmentId", "Environment ID",
			resolver.WithPlaceholder("env123..."),
			resolver.WithHint("Optional: Override the monitor's default environment"),
		).
		EndSection().
	Build()

// PostmanAPIListSchema is the UI schema for postman-api-list
var PostmanAPIListSchema = resolver.NewSchemaBuilder("postman-api-list").
	WithName("List APIs").
	WithCategory("action").
	WithIcon(iconPostman).
	WithDescription("List all API definitions in your Postman workspace").
	AddSection("Connection").
		AddExpressionField("apiKey", "API Key",
			resolver.WithRequired(),
			resolver.WithPlaceholder("PMAK-..."),
			resolver.WithHint("Postman API key"),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Options").
		AddTagsField("types", "API Types",
			resolver.WithHint("Filter by API types (e.g., openapi, graphql)"),
		).
		AddNumberField("limit", "Limit",
			resolver.WithDefault(100),
			resolver.WithMinMax(1, 1000),
			resolver.WithHint("Maximum number of APIs to return"),
		).
		EndSection().
	Build()

// ============================================================================
// EXECUTORS
// ============================================================================

// PostmanCollectionListExecutor handles postman-collection-list
type PostmanCollectionListExecutor struct{}

func (e *PostmanCollectionListExecutor) Type() string { return "postman-collection-list" }

func (e *PostmanCollectionListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	apiKey := resolver.ResolveString(getString(config, "apiKey"))
	if apiKey == "" {
		return nil, fmt.Errorf("apiKey is required")
	}

	client := getClient(apiKey)

	// Build URL with query parameters
	path := "/collections"
	params := url.Values{}

	types := getStringSlice(config, "types")
	if len(types) > 0 {
		for _, t := range types {
			params.Add("types", t)
		}
	}

	limit := getInt(config, "limit", 100)
	if limit > 0 {
		params.Set("limit", fmt.Sprintf("%d", limit))
	}

	if len(params) > 0 {
		path += "?" + params.Encode()
	}

	resp, err := client.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result CollectionListResponse
	if err := readResponseBody(resp, &result); err != nil {
		return nil, err
	}

	// Build output
	output := map[string]interface{}{
		"collections": result.Collections,
		"count":       len(result.Collections),
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// PostmanCollectionGetExecutor handles postman-collection-get
type PostmanCollectionGetExecutor struct{}

func (e *PostmanCollectionGetExecutor) Type() string { return "postman-collection-get" }

func (e *PostmanCollectionGetExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	apiKey := resolver.ResolveString(getString(config, "apiKey"))
	if apiKey == "" {
		return nil, fmt.Errorf("apiKey is required")
	}

	collectionID := resolver.ResolveString(getString(config, "collectionId"))
	if collectionID == "" {
		return nil, fmt.Errorf("collectionId is required")
	}

	client := getClient(apiKey)
	path := fmt.Sprintf("/collections/%s", collectionID)

	resp, err := client.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result CollectionGetResponse
	if err := readResponseBody(resp, &result); err != nil {
		return nil, err
	}

	// Build output
	output := map[string]interface{}{
		"id":         result.Collection.ID,
		"name":       result.Collection.Name,
		"uid":        result.Collection.UID,
		"createdAt":  result.Collection.CreatedAt,
		"updatedAt":  result.Collection.UpdatedAt,
		"collection": result.Collection.Collection,
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// PostmanCollectionRunExecutor handles postman-collection-run
type PostmanCollectionRunExecutor struct{}

func (e *PostmanCollectionRunExecutor) Type() string { return "postman-collection-run" }

func (e *PostmanCollectionRunExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	apiKey := resolver.ResolveString(getString(config, "apiKey"))
	if apiKey == "" {
		return nil, fmt.Errorf("apiKey is required")
	}

	collectionID := resolver.ResolveString(getString(config, "collectionId"))
	if collectionID == "" {
		return nil, fmt.Errorf("collectionId is required")
	}

	client := getClient(apiKey)

	// First, get the collection details
	collectionPath := fmt.Sprintf("/collections/%s", collectionID)
	collectionResp, err := client.doRequest(ctx, "GET", collectionPath, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get collection: %w", err)
	}
	defer collectionResp.Body.Close()

	var collectionResult CollectionGetResponse
	if err := readResponseBody(collectionResp, &collectionResult); err != nil {
		return nil, fmt.Errorf("failed to parse collection: %w", err)
	}

	// Get environment if specified
	var environmentData json.RawMessage
	environmentID := resolver.ResolveString(getString(config, "environmentId"))
	if environmentID != "" {
		envPath := fmt.Sprintf("/environments/%s", environmentID)
		envResp, err := client.doRequest(ctx, "GET", envPath, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to get environment: %w", err)
		}
		defer envResp.Body.Close()

		var envResult EnvironmentGetResponse
		if err := readResponseBody(envResp, &envResult); err != nil {
			return nil, fmt.Errorf("failed to parse environment: %w", err)
		}

		// Convert environment to Postman format
		envData := map[string]interface{}{
			"id":    envResult.Environment.ID,
			"name":  envResult.Environment.Name,
			"values": envResult.Environment.Variables,
		}
		environmentData, _ = json.Marshal(envData)
	}

	// Build run request
	iterationCount := getInt(config, "iterationCount", 1)
	folder := resolver.ResolveString(getString(config, "folder"))

	runReq := CollectionRunRequest{
		Collection:  collectionResult.Collection.Collection,
		Environment: environmentData,
		Options: RunOptions{
			IterationCount: iterationCount,
			Folder:         folder,
		},
	}

	// Run the collection
	runPath := fmt.Sprintf("/collections/%s/runs", collectionID)
	runResp, err := client.doRequest(ctx, "POST", runPath, runReq)
	if err != nil {
		return nil, fmt.Errorf("failed to run collection: %w", err)
	}
	defer runResp.Body.Close()

	var runResult CollectionRunResponse
	if err := readResponseBody(runResp, &runResult); err != nil {
		return nil, fmt.Errorf("failed to parse run result: %w", err)
	}

	// Build output
	output := map[string]interface{}{
		"runId":   runResult.RunID,
		"status":  runResult.Status,
		"results": runResult.Results,
	}

	if runResult.Summary != nil {
		output["summary"] = runResult.Summary
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// PostmanEnvironmentListExecutor handles postman-environment-list
type PostmanEnvironmentListExecutor struct{}

func (e *PostmanEnvironmentListExecutor) Type() string { return "postman-environment-list" }

func (e *PostmanEnvironmentListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	apiKey := resolver.ResolveString(getString(config, "apiKey"))
	if apiKey == "" {
		return nil, fmt.Errorf("apiKey is required")
	}

	client := getClient(apiKey)

	// Build URL with query parameters
	path := "/environments"
	params := url.Values{}

	limit := getInt(config, "limit", 100)
	if limit > 0 {
		params.Set("limit", fmt.Sprintf("%d", limit))
	}

	if len(params) > 0 {
		path += "?" + params.Encode()
	}

	resp, err := client.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result EnvironmentListResponse
	if err := readResponseBody(resp, &result); err != nil {
		return nil, err
	}

	// Build output
	output := map[string]interface{}{
		"environments": result.Environments,
		"count":        len(result.Environments),
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// PostmanEnvironmentGetExecutor handles postman-environment-get
type PostmanEnvironmentGetExecutor struct{}

func (e *PostmanEnvironmentGetExecutor) Type() string { return "postman-environment-get" }

func (e *PostmanEnvironmentGetExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	apiKey := resolver.ResolveString(getString(config, "apiKey"))
	if apiKey == "" {
		return nil, fmt.Errorf("apiKey is required")
	}

	environmentID := resolver.ResolveString(getString(config, "environmentId"))
	if environmentID == "" {
		return nil, fmt.Errorf("environmentId is required")
	}

	client := getClient(apiKey)
	path := fmt.Sprintf("/environments/%s", environmentID)

	resp, err := client.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result EnvironmentGetResponse
	if err := readResponseBody(resp, &result); err != nil {
		return nil, err
	}

	// Build output
	output := map[string]interface{}{
		"id":        result.Environment.ID,
		"name":      result.Environment.Name,
		"uid":       result.Environment.UID,
		"createdAt": result.Environment.CreatedAt,
		"updatedAt": result.Environment.UpdatedAt,
		"variables": result.Environment.Variables,
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// PostmanMonitorListExecutor handles postman-monitor-list
type PostmanMonitorListExecutor struct{}

func (e *PostmanMonitorListExecutor) Type() string { return "postman-monitor-list" }

func (e *PostmanMonitorListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	apiKey := resolver.ResolveString(getString(config, "apiKey"))
	if apiKey == "" {
		return nil, fmt.Errorf("apiKey is required")
	}

	client := getClient(apiKey)

	// Build URL with query parameters
	path := "/monitors"
	params := url.Values{}

	limit := getInt(config, "limit", 100)
	if limit > 0 {
		params.Set("limit", fmt.Sprintf("%d", limit))
	}

	if len(params) > 0 {
		path += "?" + params.Encode()
	}

	resp, err := client.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result MonitorListResponse
	if err := readResponseBody(resp, &result); err != nil {
		return nil, err
	}

	// Build output
	output := map[string]interface{}{
		"monitors": result.Monitors,
		"count":    len(result.Monitors),
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// PostmanMonitorRunExecutor handles postman-monitor-run
type PostmanMonitorRunExecutor struct{}

func (e *PostmanMonitorRunExecutor) Type() string { return "postman-monitor-run" }

func (e *PostmanMonitorRunExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	apiKey := resolver.ResolveString(getString(config, "apiKey"))
	if apiKey == "" {
		return nil, fmt.Errorf("apiKey is required")
	}

	monitorID := resolver.ResolveString(getString(config, "monitorId"))
	if monitorID == "" {
		return nil, fmt.Errorf("monitorId is required")
	}

	client := getClient(apiKey)

	// Build run request
	runReq := MonitorRunRequest{
		MonitorID: monitorID,
	}

	environmentID := resolver.ResolveString(getString(config, "environmentId"))
	if environmentID != "" {
		runReq.Environment = environmentID
	}

	// Run the monitor
	runPath := fmt.Sprintf("/monitors/%s/run", monitorID)
	runResp, err := client.doRequest(ctx, "POST", runPath, runReq)
	if err != nil {
		return nil, fmt.Errorf("failed to run monitor: %w", err)
	}
	defer runResp.Body.Close()

	var runResult MonitorRunResponse
	if err := readResponseBody(runResp, &runResult); err != nil {
		return nil, fmt.Errorf("failed to parse run result: %w", err)
	}

	// Build output
	output := map[string]interface{}{
		"runId":   runResult.RunID,
		"status":  runResult.Status,
		"results": runResult.Results,
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// PostmanAPIListExecutor handles postman-api-list
type PostmanAPIListExecutor struct{}

func (e *PostmanAPIListExecutor) Type() string { return "postman-api-list" }

func (e *PostmanAPIListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	apiKey := resolver.ResolveString(getString(config, "apiKey"))
	if apiKey == "" {
		return nil, fmt.Errorf("apiKey is required")
	}

	client := getClient(apiKey)

	// Build URL with query parameters
	path := "/apis"
	params := url.Values{}

	types := getStringSlice(config, "types")
	if len(types) > 0 {
		for _, t := range types {
			params.Add("type", t)
		}
	}

	limit := getInt(config, "limit", 100)
	if limit > 0 {
		params.Set("limit", fmt.Sprintf("%d", limit))
	}

	if len(params) > 0 {
		path += "?" + params.Encode()
	}

	resp, err := client.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result APIListResponse
	if err := readResponseBody(resp, &result); err != nil {
		return nil, err
	}

	// Build output
	output := map[string]interface{}{
		"apis":  result.Apis,
		"count": len(result.Apis),
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// ============================================================================
// MAIN
// ============================================================================

func main() {
	// Get port from env or use default
	port := os.Getenv("SKILL_PORT")
	if port == "" {
		port = "50128"
	}

	// Create skill server
	server := grpc.NewSkillServer("skill-postman", "1.0.0")

	// Register executors with schemas
	server.RegisterExecutorWithSchema("postman-collection-list", &PostmanCollectionListExecutor{}, PostmanCollectionListSchema)
	server.RegisterExecutorWithSchema("postman-collection-get", &PostmanCollectionGetExecutor{}, PostmanCollectionGetSchema)
	server.RegisterExecutorWithSchema("postman-collection-run", &PostmanCollectionRunExecutor{}, PostmanCollectionRunSchema)
	server.RegisterExecutorWithSchema("postman-environment-list", &PostmanEnvironmentListExecutor{}, PostmanEnvironmentListSchema)
	server.RegisterExecutorWithSchema("postman-environment-get", &PostmanEnvironmentGetExecutor{}, PostmanEnvironmentGetSchema)
	server.RegisterExecutorWithSchema("postman-monitor-list", &PostmanMonitorListExecutor{}, PostmanMonitorListSchema)
	server.RegisterExecutorWithSchema("postman-monitor-run", &PostmanMonitorRunExecutor{}, PostmanMonitorRunSchema)
	server.RegisterExecutorWithSchema("postman-api-list", &PostmanAPIListExecutor{}, PostmanAPIListSchema)

	fmt.Printf("Starting skill-postman gRPC server on port %s\n", port)
	if err := server.Serve(port); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to serve: %v\n", err)
		os.Exit(1)
	}
}
