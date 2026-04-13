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
	"strings"
	"sync"
	"time"

	"github.com/axiom-studio/skills.sdk/executor"
	"github.com/axiom-studio/skills.sdk/grpc"
	"github.com/axiom-studio/skills.sdk/resolver"
)

const (
	iconLangSmith = "search"
	defaultAPIURL = "https://api.smith.langchain.com"
)

// LangSmith client cache
var (
	clients   = make(map[string]*LangSmithClient)
	clientMux sync.RWMutex
)

// LangSmithClient holds the HTTP client and configuration
type LangSmithClient struct {
	apiKey    string
	apiURL    string
	httpClient *http.Client
}

// NewLangSmithClient creates a new LangSmith API client
func NewLangSmithClient(apiKey, apiURL string) *LangSmithClient {
	if apiURL == "" {
		apiURL = defaultAPIURL
	}
	return &LangSmithClient{
		apiKey: apiKey,
		apiURL: apiURL,
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

// getLangSmithClient returns a cached LangSmith client
func getLangSmithClient(apiKey, apiURL string) *LangSmithClient {
	cacheKey := fmt.Sprintf("%s:%s", apiKey, apiURL)

	clientMux.RLock()
	client, ok := clients[cacheKey]
	clientMux.RUnlock()

	if ok {
		return client
	}

	clientMux.Lock()
	defer clientMux.Unlock()

	// Double check
	if client, ok := clients[cacheKey]; ok {
		return client
	}

	client = NewLangSmithClient(apiKey, apiURL)
	clients[cacheKey] = client
	return client
}

// doRequest performs an HTTP request to the LangSmith API
func (c *LangSmithClient) doRequest(ctx context.Context, method, path string, body interface{}) ([]byte, error) {
	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
		reqBody = bytes.NewReader(data)
	}

	reqURL := c.apiURL + path
	req, err := http.NewRequestWithContext(ctx, method, reqURL, reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	return respBody, nil
}

func main() {
	port := os.Getenv("SKILL_PORT")
	if port == "" {
		port = "50100"
	}

	server := grpc.NewSkillServer("skill-langsmith", "1.0.0")

	// Register all executors with schemas
	server.RegisterExecutorWithSchema("langsmith-trace-list", &TraceListExecutor{}, TraceListSchema)
	server.RegisterExecutorWithSchema("langsmith-trace-get", &TraceGetExecutor{}, TraceGetSchema)
	server.RegisterExecutorWithSchema("langsmith-project-create", &ProjectCreateExecutor{}, ProjectCreateSchema)
	server.RegisterExecutorWithSchema("langsmith-project-list", &ProjectListExecutor{}, ProjectListSchema)
	server.RegisterExecutorWithSchema("langsmith-dataset-create", &DatasetCreateExecutor{}, DatasetCreateSchema)
	server.RegisterExecutorWithSchema("langsmith-dataset-list", &DatasetListExecutor{}, DatasetListSchema)
	server.RegisterExecutorWithSchema("langsmith-run-eval", &RunEvalExecutor{}, RunEvalSchema)
	server.RegisterExecutorWithSchema("langsmith-feedback-create", &FeedbackCreateExecutor{}, FeedbackCreateSchema)
	server.RegisterExecutorWithSchema("langsmith-feedback-list", &FeedbackListExecutor{}, FeedbackListSchema)

	fmt.Printf("Starting skill-langsmith gRPC server on port %s\n", port)
	if err := server.Serve(port); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to serve: %v\n", err)
		os.Exit(1)
	}
}

// ============================================================================
// CONFIG HELPERS
// ============================================================================

func getString(config map[string]interface{}, key string) string {
	if v, ok := config[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

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

func getBool(config map[string]interface{}, key string, def bool) bool {
	if v, ok := config[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return def
}

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
			return strings.Split(arr, ",")
		}
	}
	return nil
}

func getMap(config map[string]interface{}, key string) map[string]interface{} {
	if v, ok := config[key]; ok {
		if m, ok := v.(map[string]interface{}); ok {
			return m
		}
	}
	return nil
}

func getInterfaceSlice(config map[string]interface{}, key string) []interface{} {
	if v, ok := config[key]; ok {
		if arr, ok := v.([]interface{}); ok {
			return arr
		}
	}
	return nil
}

// ============================================================================
// SCHEMAS
// ============================================================================

// TraceListSchema is the UI schema for langsmith-trace-list
var TraceListSchema = resolver.NewSchemaBuilder("langsmith-trace-list").
	WithName("List Traces").
	WithCategory("action").
	WithIcon(iconLangSmith).
	WithDescription("List traces/runs from LangSmith").
	AddSection("Connection").
		AddExpressionField("apiKey", "API Key",
			resolver.WithRequired(),
			resolver.WithPlaceholder("lsv2_pt_..."),
			resolver.WithHint("LangSmith API key (supports {{bindings.xxx}})"),
			resolver.WithSensitive(),
		).
		AddExpressionField("apiURL", "API URL",
			resolver.WithDefault(defaultAPIURL),
			resolver.WithPlaceholder(defaultAPIURL),
			resolver.WithHint("LangSmith API URL (optional, defaults to api.smith.langchain.com)"),
		).
		EndSection().
	AddSection("Filters").
		AddExpressionField("projectId", "Project ID",
			resolver.WithHint("Filter by project ID or name"),
		).
		AddExpressionField("traceId", "Trace ID",
			resolver.WithHint("Filter by specific trace/run ID"),
		).
		AddExpressionField("name", "Name",
			resolver.WithHint("Filter by run name"),
		).
		AddSelectField("runType", "Run Type",
			[]resolver.SelectOption{
				{Label: "All", Value: ""},
				{Label: "LLM", Value: "llm"},
				{Label: "Chain", Value: "chain"},
				{Label: "Tool", Value: "tool"},
				{Label: "Retriever", Value: "retriever"},
				{Label: "Embedding", Value: "embedding"},
				{Label: "Prompt", Value: "prompt"},
			},
			resolver.WithDefault(""),
			resolver.WithHint("Filter by run type"),
		).
		AddExpressionField("projectId", "Project ID",
			resolver.WithHint("Filter by project ID"),
		).
		EndSection().
	AddSection("Options").
		AddNumberField("limit", "Limit",
			resolver.WithDefault(100),
			resolver.WithMinMax(1, 10000),
			resolver.WithHint("Maximum number of traces to return"),
		).
		AddNumberField("offset", "Offset",
			resolver.WithDefault(0),
			resolver.WithMinMax(0, 1000000),
			resolver.WithHint("Number of traces to skip"),
		).
		EndSection().
	Build()

// TraceGetSchema is the UI schema for langsmith-trace-get
var TraceGetSchema = resolver.NewSchemaBuilder("langsmith-trace-get").
	WithName("Get Trace").
	WithCategory("action").
	WithIcon(iconLangSmith).
	WithDescription("Get detailed information about a specific trace/run").
	AddSection("Connection").
		AddExpressionField("apiKey", "API Key",
			resolver.WithRequired(),
			resolver.WithPlaceholder("lsv2_pt_..."),
			resolver.WithHint("LangSmith API key"),
			resolver.WithSensitive(),
		).
		AddExpressionField("apiURL", "API URL",
			resolver.WithDefault(defaultAPIURL),
			resolver.WithHint("LangSmith API URL"),
		).
		EndSection().
	AddSection("Trace").
		AddExpressionField("traceId", "Trace ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("00000000-0000-0000-0000-000000000000"),
			resolver.WithHint("ID of the trace/run to retrieve"),
		).
		EndSection().
	Build()

// ProjectCreateSchema is the UI schema for langsmith-project-create
var ProjectCreateSchema = resolver.NewSchemaBuilder("langsmith-project-create").
	WithName("Create Project").
	WithCategory("action").
	WithIcon(iconLangSmith).
	WithDescription("Create a new LangSmith project").
	AddSection("Connection").
		AddExpressionField("apiKey", "API Key",
			resolver.WithRequired(),
			resolver.WithSensitive(),
		).
		AddExpressionField("apiURL", "API URL",
			resolver.WithDefault(defaultAPIURL),
		).
		EndSection().
	AddSection("Project").
		AddExpressionField("projectName", "Project Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-project"),
			resolver.WithHint("Name for the new project"),
		).
		AddTextareaField("description", "Description",
			resolver.WithRows(3),
			resolver.WithPlaceholder("Project description..."),
			resolver.WithHint("Optional project description"),
		).
		EndSection().
	AddSection("Options").
		AddExpressionField("referenceDatasetId", "Reference Dataset ID",
			resolver.WithHint("Optional reference dataset ID for evaluation projects"),
		).
		EndSection().
	Build()

// ProjectListSchema is the UI schema for langsmith-project-list
var ProjectListSchema = resolver.NewSchemaBuilder("langsmith-project-list").
	WithName("List Projects").
	WithCategory("action").
	WithIcon(iconLangSmith).
	WithDescription("List all LangSmith projects").
	AddSection("Connection").
		AddExpressionField("apiKey", "API Key",
			resolver.WithRequired(),
			resolver.WithSensitive(),
		).
		AddExpressionField("apiURL", "API URL",
			resolver.WithDefault(defaultAPIURL),
		).
		EndSection().
	AddSection("Options").
		AddNumberField("limit", "Limit",
			resolver.WithDefault(100),
			resolver.WithMinMax(1, 10000),
		).
		AddExpressionField("nameContains", "Name Contains",
			resolver.WithHint("Filter projects by name substring"),
		).
		EndSection().
	Build()

// DatasetCreateSchema is the UI schema for langsmith-dataset-create
var DatasetCreateSchema = resolver.NewSchemaBuilder("langsmith-dataset-create").
	WithName("Create Dataset").
	WithCategory("action").
	WithIcon(iconLangSmith).
	WithDescription("Create a new LangSmith dataset").
	AddSection("Connection").
		AddExpressionField("apiKey", "API Key",
			resolver.WithRequired(),
			resolver.WithSensitive(),
		).
		AddExpressionField("apiURL", "API URL",
			resolver.WithDefault(defaultAPIURL),
		).
		EndSection().
	AddSection("Dataset").
		AddExpressionField("datasetName", "Dataset Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-dataset"),
			resolver.WithHint("Name for the new dataset"),
		).
		AddTextareaField("description", "Description",
			resolver.WithRows(3),
			resolver.WithHint("Optional dataset description"),
		).
		EndSection().
	AddSection("Options").
		AddExpressionField("dataType", "Data Type",
			resolver.WithHint("Dataset data type: kv, llm, or chat"),
		).
		EndSection().
	Build()

// DatasetListSchema is the UI schema for langsmith-dataset-list
var DatasetListSchema = resolver.NewSchemaBuilder("langsmith-dataset-list").
	WithName("List Datasets").
	WithCategory("action").
	WithIcon(iconLangSmith).
	WithDescription("List all LangSmith datasets").
	AddSection("Connection").
		AddExpressionField("apiKey", "API Key",
			resolver.WithRequired(),
			resolver.WithSensitive(),
		).
		AddExpressionField("apiURL", "API URL",
			resolver.WithDefault(defaultAPIURL),
		).
		EndSection().
	AddSection("Options").
		AddNumberField("limit", "Limit",
			resolver.WithDefault(100),
			resolver.WithMinMax(1, 10000),
		).
		AddExpressionField("nameContains", "Name Contains",
			resolver.WithHint("Filter datasets by name substring"),
		).
		EndSection().
	Build()

// RunEvalSchema is the UI schema for langsmith-run-eval
var RunEvalSchema = resolver.NewSchemaBuilder("langsmith-run-eval").
	WithName("Run Evaluation").
	WithCategory("action").
	WithIcon(iconLangSmith).
	WithDescription("Run an evaluation on a dataset").
	AddSection("Connection").
		AddExpressionField("apiKey", "API Key",
			resolver.WithRequired(),
			resolver.WithSensitive(),
		).
		AddExpressionField("apiURL", "API URL",
			resolver.WithDefault(defaultAPIURL),
		).
		EndSection().
	AddSection("Evaluation").
		AddExpressionField("datasetId", "Dataset ID",
			resolver.WithRequired(),
			resolver.WithHint("ID of the dataset to evaluate against"),
		).
		AddExpressionField("evaluator", "Evaluator",
			resolver.WithRequired(),
			resolver.WithHint("Evaluator configuration (name or config object)"),
		).
		AddJSONField("llmOrChain", "LLM/Chain Config",
			resolver.WithHeight(150),
			resolver.WithHint("LLM or chain configuration to evaluate"),
		).
		EndSection().
	AddSection("Options").
		AddExpressionField("experimentName", "Experiment Name",
			resolver.WithHint("Optional name for the experiment"),
		).
		AddNumberField("maxConcurrency", "Max Concurrency",
			resolver.WithDefault(4),
			resolver.WithMinMax(1, 100),
			resolver.WithHint("Maximum concurrent evaluation requests"),
		).
		AddNumberField("repetitionCount", "Repetition Count",
			resolver.WithDefault(1),
			resolver.WithMinMax(1, 10),
			resolver.WithHint("Number of times to repeat the evaluation"),
		).
		EndSection().
	Build()

// FeedbackCreateSchema is the UI schema for langsmith-feedback-create
var FeedbackCreateSchema = resolver.NewSchemaBuilder("langsmith-feedback-create").
	WithName("Create Feedback").
	WithCategory("action").
	WithIcon(iconLangSmith).
	WithDescription("Create feedback for a run/trace").
	AddSection("Connection").
		AddExpressionField("apiKey", "API Key",
			resolver.WithRequired(),
			resolver.WithSensitive(),
		).
		AddExpressionField("apiURL", "API URL",
			resolver.WithDefault(defaultAPIURL),
		).
		EndSection().
	AddSection("Feedback").
		AddExpressionField("runId", "Run ID",
			resolver.WithRequired(),
			resolver.WithHint("ID of the run to provide feedback for"),
		).
		AddExpressionField("key", "Feedback Key",
			resolver.WithRequired(),
			resolver.WithPlaceholder("quality"),
			resolver.WithHint("Key/name for the feedback (e.g., quality, helpfulness)"),
		).
		AddExpressionField("score", "Score",
			resolver.WithHint("Numeric score value (0-1 or custom scale)"),
		).
		AddSelectField("scoreType", "Score Type",
			[]resolver.SelectOption{
				{Label: "Continuous", Value: "continuous"},
				{Label: "Binary", Value: "binary"},
				{Label: "Categorical", Value: "categorical"},
			},
			resolver.WithDefault("continuous"),
			resolver.WithHint("Type of score"),
		).
		AddTextareaField("comment", "Comment",
			resolver.WithRows(3),
			resolver.WithHint("Optional feedback comment"),
		).
		EndSection().
	Build()

// FeedbackListSchema is the UI schema for langsmith-feedback-list
var FeedbackListSchema = resolver.NewSchemaBuilder("langsmith-feedback-list").
	WithName("List Feedback").
	WithCategory("action").
	WithIcon(iconLangSmith).
	WithDescription("List feedback for runs").
	AddSection("Connection").
		AddExpressionField("apiKey", "API Key",
			resolver.WithRequired(),
			resolver.WithSensitive(),
		).
		AddExpressionField("apiURL", "API URL",
			resolver.WithDefault(defaultAPIURL),
		).
		EndSection().
	AddSection("Filters").
		AddExpressionField("runId", "Run ID",
			resolver.WithHint("Filter by run ID"),
		).
		AddExpressionField("key", "Feedback Key",
			resolver.WithHint("Filter by feedback key"),
		).
		AddExpressionField("source", "Source",
			resolver.WithHint("Filter by feedback source (api, model, user)"),
		).
		EndSection().
	AddSection("Options").
		AddNumberField("limit", "Limit",
			resolver.WithDefault(100),
			resolver.WithMinMax(1, 10000),
		).
		EndSection().
	Build()

// ============================================================================
// TRACE LIST EXECUTOR
// ============================================================================

// TraceListExecutor handles langsmith-trace-list
type TraceListExecutor struct{}

func (e *TraceListExecutor) Type() string { return "langsmith-trace-list" }

func (e *TraceListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	apiKey := resolver.ResolveString(getString(config, "apiKey"))
	if apiKey == "" {
		return nil, fmt.Errorf("LangSmith API key is required")
	}

	apiURL := resolver.ResolveString(getString(config, "apiURL"))
	if apiURL == "" {
		apiURL = defaultAPIURL
	}

	client := getLangSmithClient(apiKey, apiURL)

	// Build query parameters
	queryParams := url.Values{}

	if limit := getInt(config, "limit", 100); limit > 0 {
		queryParams.Set("limit", fmt.Sprintf("%d", limit))
	}
	if offset := getInt(config, "offset", 0); offset > 0 {
		queryParams.Set("offset", fmt.Sprintf("%d", offset))
	}

	projectId := resolver.ResolveString(getString(config, "projectId"))
	if projectId != "" {
		queryParams.Set("project", projectId)
	}

	traceId := resolver.ResolveString(getString(config, "traceId"))
	if traceId != "" {
		queryParams.Set("id", traceId)
	}

	name := resolver.ResolveString(getString(config, "name"))
	if name != "" {
		queryParams.Set("name", name)
	}

	runType := resolver.ResolveString(getString(config, "runType"))
	if runType != "" {
		queryParams.Set("run_type", runType)
	}

	path := "/api/runs?" + queryParams.Encode()

	respBody, err := client.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list traces: %w", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Format traces for output
	traces := formatTraces(result)

	return &executor.StepResult{
		Output: map[string]interface{}{
			"traces": traces,
			"total":  len(traces),
		},
	}, nil
}

func formatTraces(result map[string]interface{}) []map[string]interface{} {
	var traces []map[string]interface{}

	runsRaw, ok := result["runs"].([]interface{})
	if !ok {
		return traces
	}

	for _, runRaw := range runsRaw {
		run, ok := runRaw.(map[string]interface{})
		if !ok {
			continue
		}

		trace := map[string]interface{}{
			"id":        getSafeString(run, "id"),
			"name":      getSafeString(run, "name"),
			"runType":   getSafeString(run, "run_type"),
			"startTime": getSafeString(run, "start_time"),
			"endTime":   getSafeString(run, "end_time"),
			"status":    getSafeString(run, "status"),
		}

		if errorInfo, ok := run["error"]; ok && errorInfo != nil {
			trace["error"] = errorInfo
		}

		if executionTime, ok := run["execution_time"]; ok {
			trace["executionTime"] = executionTime
		}

		traces = append(traces, trace)
	}

	return traces
}

// ============================================================================
// TRACE GET EXECUTOR
// ============================================================================

// TraceGetExecutor handles langsmith-trace-get
type TraceGetExecutor struct{}

func (e *TraceGetExecutor) Type() string { return "langsmith-trace-get" }

func (e *TraceGetExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	apiKey := resolver.ResolveString(getString(config, "apiKey"))
	if apiKey == "" {
		return nil, fmt.Errorf("LangSmith API key is required")
	}

	apiURL := resolver.ResolveString(getString(config, "apiURL"))
	if apiURL == "" {
		apiURL = defaultAPIURL
	}

	traceId := resolver.ResolveString(getString(config, "traceId"))
	if traceId == "" {
		return nil, fmt.Errorf("trace ID is required")
	}

	client := getLangSmithClient(apiKey, apiURL)

	path := "/api/runs/" + url.PathEscape(traceId)

	respBody, err := client.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get trace: %w", err)
	}

	var trace map[string]interface{}
	if err := json.Unmarshal(respBody, &trace); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Format the trace output
	output := formatTraceDetail(trace)

	return &executor.StepResult{
		Output: output,
	}, nil
}

func formatTraceDetail(trace map[string]interface{}) map[string]interface{} {
	output := map[string]interface{}{
		"id":           getSafeString(trace, "id"),
		"name":         getSafeString(trace, "name"),
		"runType":      getSafeString(trace, "run_type"),
		"startTime":    getSafeString(trace, "start_time"),
		"endTime":      getSafeString(trace, "end_time"),
		"status":       getSafeString(trace, "status"),
		"extra":        trace["extra"],
		"inputs":       trace["inputs"],
		"outputs":      trace["outputs"],
		"error":        trace["error"],
		"executionTime": trace["execution_time"],
		"serialized":   trace["serialized"],
		"events":       trace["events"],
		"tags":         trace["tags"],
		"metadata":     trace["metadata"],
	}

	if parentRunId, ok := trace["parent_run_id"]; ok {
		output["parentRunId"] = parentRunId
	}

	if childRuns, ok := trace["child_runs"].([]interface{}); ok {
		var children []map[string]interface{}
		for _, childRaw := range childRuns {
			if child, ok := childRaw.(map[string]interface{}); ok {
				children = append(children, formatTraceDetail(child))
			}
		}
		output["childRuns"] = children
	}

	return output
}

// ============================================================================
// PROJECT CREATE EXECUTOR
// ============================================================================

// ProjectCreateExecutor handles langsmith-project-create
type ProjectCreateExecutor struct{}

func (e *ProjectCreateExecutor) Type() string { return "langsmith-project-create" }

func (e *ProjectCreateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	apiKey := resolver.ResolveString(getString(config, "apiKey"))
	if apiKey == "" {
		return nil, fmt.Errorf("LangSmith API key is required")
	}

	apiURL := resolver.ResolveString(getString(config, "apiURL"))
	if apiURL == "" {
		apiURL = defaultAPIURL
	}

	projectName := resolver.ResolveString(getString(config, "projectName"))
	if projectName == "" {
		return nil, fmt.Errorf("project name is required")
	}

	client := getLangSmithClient(apiKey, apiURL)

	requestBody := map[string]interface{}{
		"name": projectName,
	}

	description := resolver.ResolveString(getString(config, "description"))
	if description != "" {
		requestBody["description"] = description
	}

	referenceDatasetId := resolver.ResolveString(getString(config, "referenceDatasetId"))
	if referenceDatasetId != "" {
		requestBody["reference_dataset_id"] = referenceDatasetId
	}

	respBody, err := client.doRequest(ctx, http.MethodPost, "/api/projects", requestBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create project: %w", err)
	}

	var project map[string]interface{}
	if err := json.Unmarshal(respBody, &project); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"id":          getSafeString(project, "id"),
			"name":        getSafeString(project, "name"),
			"description": getSafeString(project, "description"),
			"createdAt":   getSafeString(project, "created_at"),
			"updatedAt":   getSafeString(project, "updated_at"),
		},
	}, nil
}

// ============================================================================
// PROJECT LIST EXECUTOR
// ============================================================================

// ProjectListExecutor handles langsmith-project-list
type ProjectListExecutor struct{}

func (e *ProjectListExecutor) Type() string { return "langsmith-project-list" }

func (e *ProjectListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	apiKey := resolver.ResolveString(getString(config, "apiKey"))
	if apiKey == "" {
		return nil, fmt.Errorf("LangSmith API key is required")
	}

	apiURL := resolver.ResolveString(getString(config, "apiURL"))
	if apiURL == "" {
		apiURL = defaultAPIURL
	}

	client := getLangSmithClient(apiKey, apiURL)

	queryParams := url.Values{}
	if limit := getInt(config, "limit", 100); limit > 0 {
		queryParams.Set("limit", fmt.Sprintf("%d", limit))
	}

	nameContains := resolver.ResolveString(getString(config, "nameContains"))
	if nameContains != "" {
		queryParams.Set("name_contains", nameContains)
	}

	path := "/api/projects?" + queryParams.Encode()

	respBody, err := client.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list projects: %w", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	var projects []map[string]interface{}
	projectsRaw, ok := result["projects"].([]interface{})
	if ok {
		for _, projRaw := range projectsRaw {
			proj, ok := projRaw.(map[string]interface{})
			if !ok {
				continue
			}
			projects = append(projects, map[string]interface{}{
				"id":          getSafeString(proj, "id"),
				"name":        getSafeString(proj, "name"),
				"description": getSafeString(proj, "description"),
				"createdAt":   getSafeString(proj, "created_at"),
				"updatedAt":   getSafeString(proj, "updated_at"),
			})
		}
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"projects": projects,
			"total":    len(projects),
		},
	}, nil
}

// ============================================================================
// DATASET CREATE EXECUTOR
// ============================================================================

// DatasetCreateExecutor handles langsmith-dataset-create
type DatasetCreateExecutor struct{}

func (e *DatasetCreateExecutor) Type() string { return "langsmith-dataset-create" }

func (e *DatasetCreateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	apiKey := resolver.ResolveString(getString(config, "apiKey"))
	if apiKey == "" {
		return nil, fmt.Errorf("LangSmith API key is required")
	}

	apiURL := resolver.ResolveString(getString(config, "apiURL"))
	if apiURL == "" {
		apiURL = defaultAPIURL
	}

	datasetName := resolver.ResolveString(getString(config, "datasetName"))
	if datasetName == "" {
		return nil, fmt.Errorf("dataset name is required")
	}

	client := getLangSmithClient(apiKey, apiURL)

	requestBody := map[string]interface{}{
		"name": datasetName,
	}

	description := resolver.ResolveString(getString(config, "description"))
	if description != "" {
		requestBody["description"] = description
	}

	dataType := resolver.ResolveString(getString(config, "dataType"))
	if dataType != "" {
		requestBody["data_type"] = dataType
	}

	respBody, err := client.doRequest(ctx, http.MethodPost, "/api/datasets", requestBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create dataset: %w", err)
	}

	var dataset map[string]interface{}
	if err := json.Unmarshal(respBody, &dataset); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"id":          getSafeString(dataset, "id"),
			"name":        getSafeString(dataset, "name"),
			"description": getSafeString(dataset, "description"),
			"dataType":    getSafeString(dataset, "data_type"),
			"createdAt":   getSafeString(dataset, "created_at"),
			"updatedAt":   getSafeString(dataset, "updated_at"),
		},
	}, nil
}

// ============================================================================
// DATASET LIST EXECUTOR
// ============================================================================

// DatasetListExecutor handles langsmith-dataset-list
type DatasetListExecutor struct{}

func (e *DatasetListExecutor) Type() string { return "langsmith-dataset-list" }

func (e *DatasetListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	apiKey := resolver.ResolveString(getString(config, "apiKey"))
	if apiKey == "" {
		return nil, fmt.Errorf("LangSmith API key is required")
	}

	apiURL := resolver.ResolveString(getString(config, "apiURL"))
	if apiURL == "" {
		apiURL = defaultAPIURL
	}

	client := getLangSmithClient(apiKey, apiURL)

	queryParams := url.Values{}
	if limit := getInt(config, "limit", 100); limit > 0 {
		queryParams.Set("limit", fmt.Sprintf("%d", limit))
	}

	nameContains := resolver.ResolveString(getString(config, "nameContains"))
	if nameContains != "" {
		queryParams.Set("name_contains", nameContains)
	}

	path := "/api/datasets?" + queryParams.Encode()

	respBody, err := client.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list datasets: %w", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	var datasets []map[string]interface{}
	datasetsRaw, ok := result["datasets"].([]interface{})
	if ok {
		for _, dsRaw := range datasetsRaw {
			ds, ok := dsRaw.(map[string]interface{})
			if !ok {
				continue
			}
			datasets = append(datasets, map[string]interface{}{
				"id":          getSafeString(ds, "id"),
				"name":        getSafeString(ds, "name"),
				"description": getSafeString(ds, "description"),
				"dataType":    getSafeString(ds, "data_type"),
				"createdAt":   getSafeString(ds, "created_at"),
				"updatedAt":   getSafeString(ds, "updated_at"),
			})
		}
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"datasets": datasets,
			"total":    len(datasets),
		},
	}, nil
}

// ============================================================================
// RUN EVAL EXECUTOR
// ============================================================================

// RunEvalExecutor handles langsmith-run-eval
type RunEvalExecutor struct{}

func (e *RunEvalExecutor) Type() string { return "langsmith-run-eval" }

func (e *RunEvalExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	apiKey := resolver.ResolveString(getString(config, "apiKey"))
	if apiKey == "" {
		return nil, fmt.Errorf("LangSmith API key is required")
	}

	apiURL := resolver.ResolveString(getString(config, "apiURL"))
	if apiURL == "" {
		apiURL = defaultAPIURL
	}

	datasetId := resolver.ResolveString(getString(config, "datasetId"))
	if datasetId == "" {
		return nil, fmt.Errorf("dataset ID is required")
	}

	evaluator := resolver.ResolveString(getString(config, "evaluator"))
	if evaluator == "" {
		return nil, fmt.Errorf("evaluator is required")
	}

	client := getLangSmithClient(apiKey, apiURL)

	// Parse evaluator - can be a name string or a JSON object
	var evaluatorConfig interface{}
	if err := json.Unmarshal([]byte(evaluator), &evaluatorConfig); err != nil {
		// If not valid JSON, treat as evaluator name
		evaluatorConfig = map[string]interface{}{
			"name": evaluator,
		}
	}

	requestBody := map[string]interface{}{
		"dataset": datasetId,
		"evaluator": evaluatorConfig,
	}

	// Add LLM/Chain config if provided
	llmOrChain := getMap(config, "llmOrChain")
	if llmOrChain != nil && len(llmOrChain) > 0 {
		requestBody["llm_or_chain"] = llmOrChain
	}

	// Add optional parameters
	experimentName := resolver.ResolveString(getString(config, "experimentName"))
	if experimentName != "" {
		requestBody["experiment_name"] = experimentName
	}

	if maxConcurrency := getInt(config, "maxConcurrency", 0); maxConcurrency > 0 {
		requestBody["max_concurrency"] = maxConcurrency
	}

	if repetitionCount := getInt(config, "repetitionCount", 0); repetitionCount > 1 {
		requestBody["repetition_count"] = repetitionCount
	}

	respBody, err := client.doRequest(ctx, http.MethodPost, "/api/tune", requestBody)
	if err != nil {
		return nil, fmt.Errorf("failed to run evaluation: %w", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"id":           getSafeString(result, "id"),
			"status":       getSafeString(result, "status"),
			"experimentId": getSafeString(result, "experiment_id"),
			"datasetId":    getSafeString(result, "dataset_id"),
			"createdAt":    getSafeString(result, "created_at"),
		},
	}, nil
}

// ============================================================================
// FEEDBACK CREATE EXECUTOR
// ============================================================================

// FeedbackCreateExecutor handles langsmith-feedback-create
type FeedbackCreateExecutor struct{}

func (e *FeedbackCreateExecutor) Type() string { return "langsmith-feedback-create" }

func (e *FeedbackCreateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	apiKey := resolver.ResolveString(getString(config, "apiKey"))
	if apiKey == "" {
		return nil, fmt.Errorf("LangSmith API key is required")
	}

	apiURL := resolver.ResolveString(getString(config, "apiURL"))
	if apiURL == "" {
		apiURL = defaultAPIURL
	}

	runId := resolver.ResolveString(getString(config, "runId"))
	if runId == "" {
		return nil, fmt.Errorf("run ID is required")
	}

	key := resolver.ResolveString(getString(config, "key"))
	if key == "" {
		return nil, fmt.Errorf("feedback key is required")
	}

	client := getLangSmithClient(apiKey, apiURL)

	requestBody := map[string]interface{}{
		"run_id": runId,
		"key":    key,
	}

	// Add score if provided
	scoreStr := resolver.ResolveString(getString(config, "score"))
	if scoreStr != "" {
		// Try to parse as number
		if score, err := parseFloat(scoreStr); err == nil {
			requestBody["score"] = score
		} else {
			requestBody["score"] = scoreStr
		}
	}

	// Add score type
	scoreType := resolver.ResolveString(getString(config, "scoreType"))
	if scoreType != "" {
		requestBody["score_type"] = scoreType
	}

	// Add comment if provided
	comment := resolver.ResolveString(getString(config, "comment"))
	if comment != "" {
		requestBody["comment"] = comment
	}

	respBody, err := client.doRequest(ctx, http.MethodPost, "/api/feedback", requestBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create feedback: %w", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"id":        getSafeString(result, "id"),
			"runId":     getSafeString(result, "run_id"),
			"key":       getSafeString(result, "key"),
			"score":     result["score"],
			"comment":   getSafeString(result, "comment"),
			"createdAt": getSafeString(result, "created_at"),
		},
	}, nil
}

// ============================================================================
// FEEDBACK LIST EXECUTOR
// ============================================================================

// FeedbackListExecutor handles langsmith-feedback-list
type FeedbackListExecutor struct{}

func (e *FeedbackListExecutor) Type() string { return "langsmith-feedback-list" }

func (e *FeedbackListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	apiKey := resolver.ResolveString(getString(config, "apiKey"))
	if apiKey == "" {
		return nil, fmt.Errorf("LangSmith API key is required")
	}

	apiURL := resolver.ResolveString(getString(config, "apiURL"))
	if apiURL == "" {
		apiURL = defaultAPIURL
	}

	client := getLangSmithClient(apiKey, apiURL)

	queryParams := url.Values{}
	if limit := getInt(config, "limit", 100); limit > 0 {
		queryParams.Set("limit", fmt.Sprintf("%d", limit))
	}

	runId := resolver.ResolveString(getString(config, "runId"))
	if runId != "" {
		queryParams.Set("run", runId)
	}

	key := resolver.ResolveString(getString(config, "key"))
	if key != "" {
		queryParams.Set("key", key)
	}

	source := resolver.ResolveString(getString(config, "source"))
	if source != "" {
		queryParams.Set("source", source)
	}

	path := "/api/feedback?" + queryParams.Encode()

	respBody, err := client.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list feedback: %w", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	var feedbacks []map[string]interface{}
	feedbacksRaw, ok := result["feedback"].([]interface{})
	if ok {
		for _, fbRaw := range feedbacksRaw {
			fb, ok := fbRaw.(map[string]interface{})
			if !ok {
				continue
			}
			feedbacks = append(feedbacks, map[string]interface{}{
				"id":        getSafeString(fb, "id"),
				"runId":     getSafeString(fb, "run_id"),
				"key":       getSafeString(fb, "key"),
				"score":     fb["score"],
				"value":     fb["value"],
				"comment":   getSafeString(fb, "comment"),
				"source":    getSafeString(fb, "source"),
				"createdAt": getSafeString(fb, "created_at"),
				"updatedAt": getSafeString(fb, "updated_at"),
			})
		}
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"feedback": feedbacks,
			"total":    len(feedbacks),
		},
	}, nil
}

// ============================================================================
// HELPER FUNCTIONS
// ============================================================================

// getSafeString safely extracts a string from a map
func getSafeString(m map[string]interface{}, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
		// Handle UUID or other types that might be returned
		return fmt.Sprintf("%v", v)
	}
	return ""
}

// parseFloat parses a string to float64
func parseFloat(s string) (float64, error) {
	var result float64
	_, err := fmt.Sscanf(s, "%f", &result)
	return result, err
}
