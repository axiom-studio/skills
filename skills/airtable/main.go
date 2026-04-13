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

// Airtable API configuration
const (
	AirtableAPIBase   = "https://api.airtable.com/v0"
	DefaultMaxRecords = 100
	MaxRecordsLimit   = 100
	HTTPTimeout       = 30 * time.Second
)

// AirtableError represents an error response from the Airtable API
type AirtableError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// AirtableErrorResponse represents the error response structure
type AirtableErrorResponse struct {
	Error AirtableError `json:"error"`
}

// AirtableClient cache
var (
	clients     = make(map[string]*http.Client)
	clientMutex sync.RWMutex
)

// AirtableRecord represents a single Airtable record
type AirtableRecord struct {
	ID          string                 `json:"id,omitempty"`
	CreatedTime string                 `json:"createdTime,omitempty"`
	Fields      map[string]interface{} `json:"fields"`
}

// AirtableResponse represents the API response for list operations
type AirtableResponse struct {
	Records []AirtableRecord `json:"records"`
	Offset  string           `json:"offset,omitempty"`
	Error   *AirtableError   `json:"error,omitempty"`
}

// AirtableCreateResponse represents the API response for create operations
type AirtableCreateResponse struct {
	Records []AirtableRecord `json:"records"`
	Error   *AirtableError   `json:"error,omitempty"`
}

// AirtableDeleteResponse represents the API response for delete operations
type AirtableDeleteResponse struct {
	Records []struct {
		ID      string `json:"id"`
		Deleted bool   `json:"deleted"`
	} `json:"records"`
	Error *AirtableError `json:"error,omitempty"`
}

func main() {
	// Get port from env or use default
	port := os.Getenv("SKILL_PORT")
	if port == "" {
		port = "50052"
	}

	// Create skill server
	server := grpc.NewSkillServer("skill-airtable", "1.0.0")

	// Register Airtable executors with schemas
	server.RegisterExecutorWithSchema("airtable-list", &AirtableListExecutor{}, AirtableListSchema)
	server.RegisterExecutorWithSchema("airtable-create", &AirtableCreateExecutor{}, AirtableCreateSchema)
	server.RegisterExecutorWithSchema("airtable-update", &AirtableUpdateExecutor{}, AirtableUpdateSchema)
	server.RegisterExecutorWithSchema("airtable-delete", &AirtableDeleteExecutor{}, AirtableDeleteSchema)
	server.RegisterExecutorWithSchema("airtable-search", &AirtableSearchExecutor{}, AirtableSearchSchema)

	fmt.Printf("Starting skill-airtable gRPC server on port %s\n", port)
	if err := server.Serve(port); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to serve: %v\n", err)
		os.Exit(1)
	}
}

// getHTTPClient returns or creates an HTTP client (cached)
func getHTTPClient() *http.Client {
	clientMutex.RLock()
	client, ok := clients["default"]
	clientMutex.RUnlock()

	if ok {
		return client
	}

	clientMutex.Lock()
	defer clientMutex.Unlock()

	// Double check
	if client, ok := clients["default"]; ok {
		return client
	}

	client = &http.Client{
		Timeout: HTTPTimeout,
	}
	clients["default"] = client
	return client
}

// doRequest performs an HTTP request to the Airtable API with proper authentication
func doRequest(ctx context.Context, method, url string, apiKey string, body interface{}) (*http.Response, error) {
	var reqBody io.Reader
	if body != nil {
		jsonData, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
		reqBody = bytes.NewBuffer(jsonData)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set authentication header with Bearer token
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", apiKey))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	httpClient := getHTTPClient()
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}

	return resp, nil
}

// parseAPIError extracts error message from Airtable API response
func parseAPIError(resp *http.Response) error {
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("API request failed with status %d and failed to read body: %w", resp.StatusCode, err)
	}

	// Try to parse as Airtable error response
	var errorResp AirtableErrorResponse
	if err := json.Unmarshal(body, &errorResp); err == nil && errorResp.Error.Type != "" {
		return fmt.Errorf("Airtable API error (%s): %s", errorResp.Error.Type, errorResp.Error.Message)
	}

	return fmt.Errorf("Airtable API request failed with status %d: %s", resp.StatusCode, string(body))
}

// buildURLWithParams builds a URL with query parameters
func buildURLWithParams(baseURL string, params url.Values) string {
	if len(params) == 0 {
		return baseURL
	}
	return fmt.Sprintf("%s?%s", baseURL, params.Encode())
}

// ============================================================================
// AIRTABLE LIST EXECUTOR
// ============================================================================

// AirtableListExecutor handles airtable-list node type
type AirtableListExecutor struct{}

// AirtableListConfig defines the typed configuration for airtable-list
type AirtableListConfig struct {
	APIKey          string       `json:"apiKey" description:"Airtable API key"`
	BaseID          string       `json:"baseId" description:"Airtable base ID"`
	Table           string       `json:"table" description:"Table name to list records from"`
	MaxRecords      int          `json:"maxRecords" default:"100" description:"Maximum number of records to return"`
	FilterByFormula string       `json:"filterByFormula" description:"Airtable formula to filter records"`
	Sort            []SortOption `json:"sort" description:"Sort options"`
	View            string       `json:"view" description:"View ID to use for listing"`
	Offset          string       `json:"offset" description:"Pagination offset from previous request"`
}

// SortOption defines sorting configuration
type SortOption struct {
	Field     string `json:"field"`
	Direction string `json:"direction,omitempty"`
}

// AirtableListSchema is the UI schema for airtable-list
var AirtableListSchema = resolver.NewSchemaBuilder("airtable-list").
	WithName("Airtable List Records").
	WithCategory("airtable").
	WithIcon("table").
	WithDescription("List records from an Airtable table").
	AddSection("Connection").
		AddExpressionField("apiKey", "API Key",
			resolver.WithRequired(),
			resolver.WithPlaceholder("patXXXXXXXXXXXXXX"),
			resolver.WithHint("Use {{secrets.airtable_api_key}} for secure access"),
			resolver.WithSensitive(),
		).
		AddTextField("baseId", "Base ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("appXXXXXXXXXXXXXX"),
		).
		AddTextField("table", "Table Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("Users"),
		).
		EndSection().
	AddSection("Options").
		AddNumberField("maxRecords", "Max Records",
			resolver.WithDefault(100),
			resolver.WithMinMax(1, 100),
		).
		AddTextField("filterByFormula", "Filter Formula",
			resolver.WithPlaceholder("{Status} = 'Active'"),
			resolver.WithHint("Airtable formula syntax for filtering"),
		).
		AddTextField("view", "View ID",
			resolver.WithPlaceholder("viwXXXXXXXXXXXXXX"),
			resolver.WithHint("Optional view ID to use for listing"),
		).
		AddTextField("offset", "Offset",
			resolver.WithPlaceholder("Optional pagination offset"),
			resolver.WithHint("Offset from previous response for pagination"),
		).
		EndSection().
	Build()

func (e *AirtableListExecutor) Type() string { return "airtable-list" }

func (e *AirtableListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	// Parse config into typed struct
	var cfg AirtableListConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	} else {
		return nil, fmt.Errorf("invalid resolver type")
	}

	// Validate required fields
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("apiKey is required")
	}
	if cfg.BaseID == "" {
		return nil, fmt.Errorf("baseId is required")
	}
	if cfg.Table == "" {
		return nil, fmt.Errorf("table is required")
	}

	// Build URL
	baseURL := fmt.Sprintf("%s/%s/%s", AirtableAPIBase, cfg.BaseID, cfg.Table)

	// Build query parameters
	params := url.Values{}
	if cfg.MaxRecords > 0 && cfg.MaxRecords <= MaxRecordsLimit {
		params.Set("maxRecords", fmt.Sprintf("%d", cfg.MaxRecords))
	}
	if cfg.FilterByFormula != "" {
		params.Set("filterByFormula", cfg.FilterByFormula)
	}
	if cfg.View != "" {
		params.Set("view", cfg.View)
	}
	if cfg.Offset != "" {
		params.Set("offset", cfg.Offset)
	}

	// Add sort parameters
	for i, sort := range cfg.Sort {
		if sort.Field != "" {
			params.Add(fmt.Sprintf("sort[%d][field]", i), sort.Field)
			if sort.Direction != "" {
				params.Add(fmt.Sprintf("sort[%d][direction]", i), sort.Direction)
			}
		}
	}

	fullURL := buildURLWithParams(baseURL, params)

	// Make API request
	resp, err := doRequest(ctx, "GET", fullURL, cfg.APIKey, nil)
	if err != nil {
		return nil, err
	}

	// Check for error response
	if resp.StatusCode != http.StatusOK {
		return nil, parseAPIError(resp)
	}
	defer resp.Body.Close()

	// Parse response
	var result AirtableResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	// Check for error in response body
	if result.Error != nil {
		return nil, fmt.Errorf("Airtable API error (%s): %s", result.Error.Type, result.Error.Message)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"records": result.Records,
			"count":   len(result.Records),
			"offset":  result.Offset,
			"hasMore": result.Offset != "",
		},
	}, nil
}

// ============================================================================
// AIRTABLE CREATE EXECUTOR
// ============================================================================

// AirtableCreateExecutor handles airtable-create node type
type AirtableCreateExecutor struct{}

// AirtableCreateConfig defines the typed configuration for airtable-create
type AirtableCreateConfig struct {
	APIKey  string                 `json:"apiKey" description:"Airtable API key"`
	BaseID  string                 `json:"baseId" description:"Airtable base ID"`
	Table   string                 `json:"table" description:"Table name to create records in"`
	Records []AirtableCreateRecord `json:"records" description:"Records to create"`
}

// AirtableCreateRecord represents a record to create
type AirtableCreateRecord struct {
	Fields map[string]interface{} `json:"fields"`
}

// AirtableCreateSchema is the UI schema for airtable-create
var AirtableCreateSchema = resolver.NewSchemaBuilder("airtable-create").
	WithName("Airtable Create Records").
	WithCategory("airtable").
	WithIcon("plus-circle").
	WithDescription("Create new records in an Airtable table").
	AddSection("Connection").
		AddExpressionField("apiKey", "API Key",
			resolver.WithRequired(),
			resolver.WithPlaceholder("patXXXXXXXXXXXXXX"),
			resolver.WithHint("Use {{secrets.airtable_api_key}} for secure access"),
			resolver.WithSensitive(),
		).
		AddTextField("baseId", "Base ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("appXXXXXXXXXXXXXX"),
		).
		AddTextField("table", "Table Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("Users"),
		).
		EndSection().
	AddSection("Records").
		AddJSONField("records", "Records",
			resolver.WithRequired(),
			resolver.WithHeight(200),
			resolver.WithHint("Array of objects with 'fields' property, e.g., [{\"fields\": {\"Name\": \"John\"}}]"),
		).
		EndSection().
	Build()

func (e *AirtableCreateExecutor) Type() string { return "airtable-create" }

func (e *AirtableCreateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	// Parse config into typed struct
	var cfg AirtableCreateConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	} else {
		return nil, fmt.Errorf("invalid resolver type")
	}

	// Validate required fields
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("apiKey is required")
	}
	if cfg.BaseID == "" {
		return nil, fmt.Errorf("baseId is required")
	}
	if cfg.Table == "" {
		return nil, fmt.Errorf("table is required")
	}
	if len(cfg.Records) == 0 {
		return nil, fmt.Errorf("records are required")
	}

	// Airtable API limits batch creates to 10 records at a time
	if len(cfg.Records) > 10 {
		return nil, fmt.Errorf("maximum 10 records can be created at once")
	}

	// Build URL
	baseURL := fmt.Sprintf("%s/%s/%s", AirtableAPIBase, cfg.BaseID, cfg.Table)

	// Airtable API expects records in a specific format
	requestBody := map[string]interface{}{
		"records": cfg.Records,
	}

	// Make API request
	resp, err := doRequest(ctx, "POST", baseURL, cfg.APIKey, requestBody)
	if err != nil {
		return nil, err
	}

	// Check for error response
	if resp.StatusCode != http.StatusOK {
		return nil, parseAPIError(resp)
	}
	defer resp.Body.Close()

	// Parse response
	var result AirtableCreateResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	// Check for error in response body
	if result.Error != nil {
		return nil, fmt.Errorf("Airtable API error (%s): %s", result.Error.Type, result.Error.Message)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"createdRecords": result.Records,
			"count":          len(result.Records),
			"ids":            extractRecordIDs(result.Records),
		},
	}, nil
}

// ============================================================================
// AIRTABLE UPDATE EXECUTOR
// ============================================================================

// AirtableUpdateExecutor handles airtable-update node type
type AirtableUpdateExecutor struct{}

// AirtableUpdateConfig defines the typed configuration for airtable-update
type AirtableUpdateConfig struct {
	APIKey  string                 `json:"apiKey" description:"Airtable API key"`
	BaseID  string                 `json:"baseId" description:"Airtable base ID"`
	Table   string                 `json:"table" description:"Table name to update records in"`
	Records []AirtableUpdateRecord `json:"records" description:"Records to update with ID and fields"`
}

// AirtableUpdateRecord represents a record to update
type AirtableUpdateRecord struct {
	ID     string                 `json:"id"`
	Fields map[string]interface{} `json:"fields"`
}

// AirtableUpdateSchema is the UI schema for airtable-update
var AirtableUpdateSchema = resolver.NewSchemaBuilder("airtable-update").
	WithName("Airtable Update Records").
	WithCategory("airtable").
	WithIcon("edit").
	WithDescription("Update existing records in an Airtable table").
	AddSection("Connection").
		AddExpressionField("apiKey", "API Key",
			resolver.WithRequired(),
			resolver.WithPlaceholder("patXXXXXXXXXXXXXX"),
			resolver.WithHint("Use {{secrets.airtable_api_key}} for secure access"),
			resolver.WithSensitive(),
		).
		AddTextField("baseId", "Base ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("appXXXXXXXXXXXXXX"),
		).
		AddTextField("table", "Table Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("Users"),
		).
		EndSection().
	AddSection("Records").
		AddJSONField("records", "Records",
			resolver.WithRequired(),
			resolver.WithHeight(200),
			resolver.WithHint("Array of objects with 'id' and 'fields' properties, e.g., [{\"id\": \"recXXX\", \"fields\": {\"Name\": \"Jane\"}}]"),
		).
		EndSection().
	Build()

func (e *AirtableUpdateExecutor) Type() string { return "airtable-update" }

func (e *AirtableUpdateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	// Parse config into typed struct
	var cfg AirtableUpdateConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	} else {
		return nil, fmt.Errorf("invalid resolver type")
	}

	// Validate required fields
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("apiKey is required")
	}
	if cfg.BaseID == "" {
		return nil, fmt.Errorf("baseId is required")
	}
	if cfg.Table == "" {
		return nil, fmt.Errorf("table is required")
	}
	if len(cfg.Records) == 0 {
		return nil, fmt.Errorf("records are required")
	}

	// Validate each record has an ID
	for i, record := range cfg.Records {
		if record.ID == "" {
			return nil, fmt.Errorf("record at index %d is missing 'id' field", i)
		}
	}

	// Airtable API limits batch updates to 10 records at a time
	if len(cfg.Records) > 10 {
		return nil, fmt.Errorf("maximum 10 records can be updated at once")
	}

	// Build URL
	baseURL := fmt.Sprintf("%s/%s/%s", AirtableAPIBase, cfg.BaseID, cfg.Table)

	requestBody := map[string]interface{}{
		"records": cfg.Records,
	}

	// Make API request (PATCH for updates)
	resp, err := doRequest(ctx, "PATCH", baseURL, cfg.APIKey, requestBody)
	if err != nil {
		return nil, err
	}

	// Check for error response
	if resp.StatusCode != http.StatusOK {
		return nil, parseAPIError(resp)
	}
	defer resp.Body.Close()

	// Parse response
	var result AirtableCreateResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	// Check for error in response body
	if result.Error != nil {
		return nil, fmt.Errorf("Airtable API error (%s): %s", result.Error.Type, result.Error.Message)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"updatedRecords": result.Records,
			"count":          len(result.Records),
			"ids":            extractRecordIDs(result.Records),
		},
	}, nil
}

// ============================================================================
// AIRTABLE DELETE EXECUTOR
// ============================================================================

// AirtableDeleteExecutor handles airtable-delete node type
type AirtableDeleteExecutor struct{}

// AirtableDeleteConfig defines the typed configuration for airtable-delete
type AirtableDeleteConfig struct {
	APIKey    string   `json:"apiKey" description:"Airtable API key"`
	BaseID    string   `json:"baseId" description:"Airtable base ID"`
	Table     string   `json:"table" description:"Table name to delete records from"`
	RecordIDs []string `json:"recordIds" description:"Array of record IDs to delete"`
}

// AirtableDeleteSchema is the UI schema for airtable-delete
var AirtableDeleteSchema = resolver.NewSchemaBuilder("airtable-delete").
	WithName("Airtable Delete Records").
	WithCategory("airtable").
	WithIcon("trash").
	WithDescription("Delete records from an Airtable table").
	AddSection("Connection").
		AddExpressionField("apiKey", "API Key",
			resolver.WithRequired(),
			resolver.WithPlaceholder("patXXXXXXXXXXXXXX"),
			resolver.WithHint("Use {{secrets.airtable_api_key}} for secure access"),
			resolver.WithSensitive(),
		).
		AddTextField("baseId", "Base ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("appXXXXXXXXXXXXXX"),
		).
		AddTextField("table", "Table Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("Users"),
		).
		EndSection().
	AddSection("Delete").
		AddTagsField("recordIds", "Record IDs",
			resolver.WithRequired(),
			resolver.WithHint("Array of record IDs to delete, e.g., [\"recXXX\", \"recYYY\"]"),
		).
		EndSection().
	Build()

func (e *AirtableDeleteExecutor) Type() string { return "airtable-delete" }

func (e *AirtableDeleteExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	// Parse config into typed struct
	var cfg AirtableDeleteConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	} else {
		return nil, fmt.Errorf("invalid resolver type")
	}

	// Validate required fields
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("apiKey is required")
	}
	if cfg.BaseID == "" {
		return nil, fmt.Errorf("baseId is required")
	}
	if cfg.Table == "" {
		return nil, fmt.Errorf("table is required")
	}
	if len(cfg.RecordIDs) == 0 {
		return nil, fmt.Errorf("recordIds are required")
	}

	// Airtable API limits batch deletes to 10 records at a time
	if len(cfg.RecordIDs) > 10 {
		return nil, fmt.Errorf("maximum 10 records can be deleted at once")
	}

	// Build URL with record IDs as query parameters
	baseURL := fmt.Sprintf("%s/%s/%s", AirtableAPIBase, cfg.BaseID, cfg.Table)
	params := url.Values{}
	for _, id := range cfg.RecordIDs {
		params.Add("records[]", id)
	}
	fullURL := buildURLWithParams(baseURL, params)

	// Make API request
	resp, err := doRequest(ctx, "DELETE", fullURL, cfg.APIKey, nil)
	if err != nil {
		return nil, err
	}

	// Check for error response
	if resp.StatusCode != http.StatusOK {
		return nil, parseAPIError(resp)
	}
	defer resp.Body.Close()

	// Parse response
	var result AirtableDeleteResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	// Check for error in response body
	if result.Error != nil {
		return nil, fmt.Errorf("Airtable API error (%s): %s", result.Error.Type, result.Error.Message)
	}

	// Extract deleted record IDs
	deletedIDs := make([]string, 0, len(result.Records))
	for _, record := range result.Records {
		if record.Deleted {
			deletedIDs = append(deletedIDs, record.ID)
		}
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"deletedRecords": deletedIDs,
			"count":          len(deletedIDs),
			"success":        len(deletedIDs) == len(cfg.RecordIDs),
		},
	}, nil
}

// ============================================================================
// AIRTABLE SEARCH EXECUTOR
// ============================================================================

// AirtableSearchExecutor handles airtable-search node type
type AirtableSearchExecutor struct{}

// AirtableSearchConfig defines the typed configuration for airtable-search
type AirtableSearchConfig struct {
	APIKey       string `json:"apiKey" description:"Airtable API key"`
	BaseID       string `json:"baseId" description:"Airtable base ID"`
	Table        string `json:"table" description:"Table name to search in"`
	Field        string `json:"field" description:"Field name to search in"`
	Value        string `json:"value" description:"Value to search for"`
	ExactMatch   bool   `json:"exactMatch" default:"false" description:"Whether to match exactly"`
	MaxRecords   int    `json:"maxRecords" default:"100" description:"Maximum number of records to return"`
	CaseSensitive bool  `json:"caseSensitive" default:"false" description:"Whether search is case-sensitive"`
}

// AirtableSearchSchema is the UI schema for airtable-search
var AirtableSearchSchema = resolver.NewSchemaBuilder("airtable-search").
	WithName("Airtable Search Records").
	WithCategory("airtable").
	WithIcon("search").
	WithDescription("Search for records in an Airtable table by field value").
	AddSection("Connection").
		AddExpressionField("apiKey", "API Key",
			resolver.WithRequired(),
			resolver.WithPlaceholder("patXXXXXXXXXXXXXX"),
			resolver.WithHint("Use {{secrets.airtable_api_key}} for secure access"),
			resolver.WithSensitive(),
		).
		AddTextField("baseId", "Base ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("appXXXXXXXXXXXXXX"),
		).
		AddTextField("table", "Table Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("Users"),
		).
		EndSection().
	AddSection("Search").
		AddTextField("field", "Field Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("Name"),
			resolver.WithHint("The field to search in"),
		).
		AddTextField("value", "Search Value",
			resolver.WithRequired(),
			resolver.WithPlaceholder("John"),
			resolver.WithHint("The value to search for"),
		).
		AddToggleField("exactMatch", "Exact Match",
			resolver.WithDefault(false),
			resolver.WithHint("If true, matches exact value only; if false, finds partial matches"),
		).
		AddToggleField("caseSensitive", "Case Sensitive",
			resolver.WithDefault(false),
			resolver.WithHint("If true, search is case-sensitive"),
		).
		AddNumberField("maxRecords", "Max Records",
			resolver.WithDefault(100),
			resolver.WithMinMax(1, 100),
		).
		EndSection().
	Build()

func (e *AirtableSearchExecutor) Type() string { return "airtable-search" }

func (e *AirtableSearchExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	// Parse config into typed struct
	var cfg AirtableSearchConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	} else {
		return nil, fmt.Errorf("invalid resolver type")
	}

	// Validate required fields
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("apiKey is required")
	}
	if cfg.BaseID == "" {
		return nil, fmt.Errorf("baseId is required")
	}
	if cfg.Table == "" {
		return nil, fmt.Errorf("table is required")
	}
	if cfg.Field == "" {
		return nil, fmt.Errorf("field is required")
	}
	if cfg.Value == "" {
		return nil, fmt.Errorf("value is required")
	}

	// Build URL
	baseURL := fmt.Sprintf("%s/%s/%s", AirtableAPIBase, cfg.BaseID, cfg.Table)

	// Build filter formula based on exact match and case sensitivity settings
	var filterFormula string
	escapedValue := escapeFormulaValue(cfg.Value)

	if cfg.ExactMatch {
		if cfg.CaseSensitive {
			filterFormula = fmt.Sprintf("{%s} = '%s'", cfg.Field, escapedValue)
		} else {
			filterFormula = fmt.Sprintf("LOWER({%s}) = LOWER('%s')", cfg.Field, escapedValue)
		}
	} else {
		// Partial match using FIND
		if cfg.CaseSensitive {
			filterFormula = fmt.Sprintf("FIND('%s', {%s})", escapedValue, cfg.Field)
		} else {
			filterFormula = fmt.Sprintf("FIND(LOWER('%s'), LOWER({%s}))", escapedValue, cfg.Field)
		}
	}

	// Build query parameters
	params := url.Values{}
	params.Set("filterByFormula", filterFormula)
	if cfg.MaxRecords > 0 && cfg.MaxRecords <= MaxRecordsLimit {
		params.Set("maxRecords", fmt.Sprintf("%d", cfg.MaxRecords))
	}

	fullURL := buildURLWithParams(baseURL, params)

	// Make API request
	resp, err := doRequest(ctx, "GET", fullURL, cfg.APIKey, nil)
	if err != nil {
		return nil, err
	}

	// Check for error response
	if resp.StatusCode != http.StatusOK {
		return nil, parseAPIError(resp)
	}
	defer resp.Body.Close()

	// Parse response
	var result AirtableResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	// Check for error in response body
	if result.Error != nil {
		return nil, fmt.Errorf("Airtable API error (%s): %s", result.Error.Type, result.Error.Message)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"records":       result.Records,
			"count":         len(result.Records),
			"field":         cfg.Field,
			"value":         cfg.Value,
			"exactMatch":    cfg.ExactMatch,
			"caseSensitive": cfg.CaseSensitive,
		},
	}, nil
}

// ============================================================================
// HELPER FUNCTIONS
// ============================================================================

// extractRecordIDs extracts IDs from a slice of records
func extractRecordIDs(records []AirtableRecord) []string {
	ids := make([]string, len(records))
	for i, record := range records {
		ids[i] = record.ID
	}
	return ids
}

// escapeFormulaValue escapes single quotes in formula values
func escapeFormulaValue(value string) string {
	// Escape single quotes by doubling them for Airtable formulas
	return strings.ReplaceAll(value, "'", "''")
}
