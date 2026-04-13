package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/axiom-studio/skills.sdk/executor"
	"github.com/axiom-studio/skills.sdk/grpc"
	"github.com/axiom-studio/skills.sdk/resolver"
)

const (
	fivetranAPIBase = "https://api.fivetran.com/v1"
	iconFivetran    = "database"
)

// FivetranConfig holds Fivetran connection configuration
type FivetranConfig struct {
	APIKey    string
	APISecret string
}

// FivetranSkill holds the HTTP client for Fivetran API
type FivetranSkill struct {
	client *http.Client
}

// NewFivetranSkill creates a new Fivetran skill instance
func NewFivetranSkill() *FivetranSkill {
	return &FivetranSkill{
		client: &http.Client{
			Timeout: 120 * time.Second,
		},
	}
}

// ============================================================================
// HTTP CLIENT HELPERS
// ============================================================================

// doRequest performs an HTTP request to the Fivetran API
func (s *FivetranSkill) doRequest(ctx context.Context, method, endpoint string, cfg FivetranConfig, body interface{}) ([]byte, error) {
	var reqBody io.Reader
	if body != nil {
		jsonBody, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
		reqBody = bytes.NewReader(jsonBody)
	}

	url := fmt.Sprintf("%s/%s", strings.TrimSuffix(fivetranAPIBase, "/"), strings.TrimPrefix(endpoint, "/"))

	req, err := http.NewRequestWithContext(ctx, method, url, reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set Basic Auth using API Key and Secret
	auth := base64.StdEncoding.EncodeToString([]byte(cfg.APIKey + ":" + cfg.APISecret))
	req.Header.Set("Authorization", "Basic "+auth)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Axiom-Fivetran-Skill/1.0.0")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Check for error status codes
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("fivetran API error (status %d): %s", resp.StatusCode, string(respBody))
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
			return strings.Split(arr, ",")
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

// parseFivetranConfig extracts Fivetran configuration from config map
func parseFivetranConfig(config map[string]interface{}) FivetranConfig {
	return FivetranConfig{
		APIKey:    getString(config, "apiKey"),
		APISecret: getString(config, "apiSecret"),
	}
}

// ============================================================================
// SCHEMAS
// ============================================================================

// ConnectorListSchema is the UI schema for fivetran-connector-list
var ConnectorListSchema = resolver.NewSchemaBuilder("fivetran-connector-list").
	WithName("List Fivetran Connectors").
	WithCategory("action").
	WithIcon(iconFivetran).
	WithDescription("List all Fivetran connectors").
	AddSection("Fivetran Connection").
		AddExpressionField("apiKey", "API Key",
			resolver.WithPlaceholder("YOUR_API_KEY"),
			resolver.WithRequired(),
			resolver.WithHint("Fivetran API key"),
		).
		AddExpressionField("apiSecret", "API Secret",
			resolver.WithSensitive(),
			resolver.WithRequired(),
			resolver.WithHint("Fivetran API secret"),
		).
		EndSection().
	AddSection("Filters").
		AddExpressionField("groupId", "Group ID",
			resolver.WithPlaceholder("grp_abc123"),
			resolver.WithHint("Filter connectors by group ID"),
		).
		EndSection().
	AddSection("Options").
		AddNumberField("limit", "Limit",
			resolver.WithDefault(100),
			resolver.WithMinMax(1, 1000),
			resolver.WithHint("Maximum number of connectors to return"),
		).
		EndSection().
	Build()

// ConnectorCreateSchema is the UI schema for fivetran-connector-create
var ConnectorCreateSchema = resolver.NewSchemaBuilder("fivetran-connector-create").
	WithName("Create Fivetran Connector").
	WithCategory("action").
	WithIcon(iconFivetran).
	WithDescription("Create a new Fivetran connector").
	AddSection("Fivetran Connection").
		AddExpressionField("apiKey", "API Key",
			resolver.WithPlaceholder("YOUR_API_KEY"),
			resolver.WithRequired(),
		).
		AddExpressionField("apiSecret", "API Secret",
			resolver.WithSensitive(),
			resolver.WithRequired(),
		).
		EndSection().
	AddSection("Connector Configuration").
		AddExpressionField("groupId", "Group ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("grp_abc123"),
			resolver.WithHint("Group ID to add the connector to"),
		).
		AddExpressionField("service", "Service",
			resolver.WithRequired(),
			resolver.WithPlaceholder("postgres"),
			resolver.WithHint("Service name (e.g., postgres, mysql, salesforce)"),
		).
		AddExpressionField("name", "Connector Name",
			resolver.WithPlaceholder("My Connector"),
			resolver.WithHint("Display name for the connector"),
		).
		AddJSONField("config", "Connector Config",
			resolver.WithRequired(),
			resolver.WithHeight(200),
			resolver.WithHint("Service-specific configuration as JSON"),
		).
		EndSection().
	AddSection("Options").
		AddToggleField("paused", "Paused",
			resolver.WithDefault(false),
			resolver.WithHint("Create connector in paused state"),
		).
		AddKeyValueField("tags", "Tags",
			resolver.WithHint("Tags to apply to the connector"),
		).
		EndSection().
	Build()

// ConnectorSyncSchema is the UI schema for fivetran-connector-sync
var ConnectorSyncSchema = resolver.NewSchemaBuilder("fivetran-connector-sync").
	WithName("Trigger Fivetran Sync").
	WithCategory("action").
	WithIcon(iconFivetran).
	WithDescription("Trigger a sync for a Fivetran connector").
	AddSection("Fivetran Connection").
		AddExpressionField("apiKey", "API Key",
			resolver.WithPlaceholder("YOUR_API_KEY"),
			resolver.WithRequired(),
		).
		AddExpressionField("apiSecret", "API Secret",
			resolver.WithSensitive(),
			resolver.WithRequired(),
		).
		EndSection().
	AddSection("Connector").
		AddExpressionField("connectorId", "Connector ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("abc123def456"),
			resolver.WithHint("ID of the connector to sync"),
		).
		EndSection().
	Build()

// ConnectorStatusSchema is the UI schema for fivetran-connector-status
var ConnectorStatusSchema = resolver.NewSchemaBuilder("fivetran-connector-status").
	WithName("Get Connector Status").
	WithCategory("action").
	WithIcon(iconFivetran).
	WithDescription("Get status and details of a Fivetran connector").
	AddSection("Fivetran Connection").
		AddExpressionField("apiKey", "API Key",
			resolver.WithPlaceholder("YOUR_API_KEY"),
			resolver.WithRequired(),
		).
		AddExpressionField("apiSecret", "API Secret",
			resolver.WithSensitive(),
			resolver.WithRequired(),
		).
		EndSection().
	AddSection("Connector").
		AddExpressionField("connectorId", "Connector ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("abc123def456"),
			resolver.WithHint("ID of the connector"),
		).
		EndSection().
	Build()

// DestinationListSchema is the UI schema for fivetran-destination-list
var DestinationListSchema = resolver.NewSchemaBuilder("fivetran-destination-list").
	WithName("List Fivetran Destinations").
	WithCategory("action").
	WithIcon(iconFivetran).
	WithDescription("List all Fivetran destinations").
	AddSection("Fivetran Connection").
		AddExpressionField("apiKey", "API Key",
			resolver.WithPlaceholder("YOUR_API_KEY"),
			resolver.WithRequired(),
		).
		AddExpressionField("apiSecret", "API Secret",
			resolver.WithSensitive(),
			resolver.WithRequired(),
		).
		EndSection().
	AddSection("Options").
		AddNumberField("limit", "Limit",
			resolver.WithDefault(100),
			resolver.WithMinMax(1, 1000),
			resolver.WithHint("Maximum number of destinations to return"),
		).
		EndSection().
	Build()

// SchemaConfigSchema is the UI schema for fivetran-schema-config
var SchemaConfigSchema = resolver.NewSchemaBuilder("fivetran-schema-config").
	WithName("Configure Fivetran Schema").
	WithCategory("action").
	WithIcon(iconFivetran).
	WithDescription("Configure schema settings for a Fivetran connector").
	AddSection("Fivetran Connection").
		AddExpressionField("apiKey", "API Key",
			resolver.WithPlaceholder("YOUR_API_KEY"),
			resolver.WithRequired(),
		).
		AddExpressionField("apiSecret", "API Secret",
			resolver.WithSensitive(),
			resolver.WithRequired(),
		).
		EndSection().
	AddSection("Schema Configuration").
		AddExpressionField("connectorId", "Connector ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("abc123def456"),
			resolver.WithHint("ID of the connector"),
		).
		AddExpressionField("schema", "Schema Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("public"),
			resolver.WithHint("Schema name to configure"),
		).
		AddExpressionField("table", "Table Name",
			resolver.WithPlaceholder(""),
			resolver.WithHint("Table name (leave empty for schema-level config)"),
		).
		EndSection().
	AddSection("Settings").
		AddToggleField("enabled", "Enabled",
			resolver.WithDefault(true),
			resolver.WithHint("Enable or disable the schema/table"),
		).
		AddToggleField("syncMode", "Sync Mode",
			resolver.WithDefault(false),
			resolver.WithHint("Use incremental sync (false = full sync)"),
		).
		AddJSONField("columnConfig", "Column Configuration",
			resolver.WithHeight(150),
			resolver.WithHint("Column-level configuration as JSON"),
		).
		EndSection().
	Build()

// GroupListSchema is the UI schema for fivetran-group-list
var GroupListSchema = resolver.NewSchemaBuilder("fivetran-group-list").
	WithName("List Fivetran Groups").
	WithCategory("action").
	WithIcon(iconFivetran).
	WithDescription("List all Fivetran groups").
	AddSection("Fivetran Connection").
		AddExpressionField("apiKey", "API Key",
			resolver.WithPlaceholder("YOUR_API_KEY"),
			resolver.WithRequired(),
		).
		AddExpressionField("apiSecret", "API Secret",
			resolver.WithSensitive(),
			resolver.WithRequired(),
		).
		EndSection().
	AddSection("Options").
		AddNumberField("limit", "Limit",
			resolver.WithDefault(100),
			resolver.WithMinMax(1, 1000),
			resolver.WithHint("Maximum number of groups to return"),
		).
		EndSection().
	Build()

// ============================================================================
// CONNECTOR LIST EXECUTOR
// ============================================================================

// ConnectorListExecutor handles fivetran-connector-list node type
type ConnectorListExecutor struct {
	*FivetranSkill
}

func (e *ConnectorListExecutor) Type() string { return "fivetran-connector-list" }

func (e *ConnectorListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	cfg := parseFivetranConfig(step.Config)
	if cfg.APIKey == "" || cfg.APISecret == "" {
		return nil, fmt.Errorf("apiKey and apiSecret are required")
	}

	groupID := getString(step.Config, "groupId")
	limit := getInt(step.Config, "limit", 100)

	// Build endpoint
	endpoint := fmt.Sprintf("/connectors?limit=%d", limit)
	if groupID != "" {
		endpoint += fmt.Sprintf("&group_id=%s", groupID)
	}

	respBody, err := e.doRequest(ctx, "GET", endpoint, cfg, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list connectors: %w", err)
	}

	// Parse response
	var response struct {
		Data struct {
			Items []struct {
				ID           string `json:"id"`
				GroupID      string `json:"group_id"`
				Service      string `json:"service"`
				Schema       string `json:"schema"`
				Paused       bool   `json:"paused"`
				PausedReason string `json:"paused_reason"`
				SyncFrequency int   `json:"sync_frequency"`
				SucceededAt  string `json:"succeeded_at"`
				FailedAt     string `json:"failed_at"`
				ServiceVersion string `json:"service_version"`
			} `json:"items"`
			NextCursor string `json:"next_cursor"`
		} `json:"data"`
	}

	if err := json.Unmarshal(respBody, &response); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Build output
	var connectors []map[string]interface{}
	for _, item := range response.Data.Items {
		connector := map[string]interface{}{
			"id":              item.ID,
			"group_id":        item.GroupID,
			"service":         item.Service,
			"schema":          item.Schema,
			"paused":          item.Paused,
			"sync_frequency":  item.SyncFrequency,
			"succeeded_at":    item.SucceededAt,
			"failed_at":       item.FailedAt,
			"service_version": item.ServiceVersion,
		}
		if item.PausedReason != "" {
			connector["paused_reason"] = item.PausedReason
		}
		connectors = append(connectors, connector)
	}

	output := map[string]interface{}{
		"connectors": connectors,
		"total":      len(connectors),
	}
	if response.Data.NextCursor != "" {
		output["next_cursor"] = response.Data.NextCursor
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// ============================================================================
// CONNECTOR CREATE EXECUTOR
// ============================================================================

// ConnectorCreateExecutor handles fivetran-connector-create node type
type ConnectorCreateExecutor struct {
	*FivetranSkill
}

func (e *ConnectorCreateExecutor) Type() string { return "fivetran-connector-create" }

func (e *ConnectorCreateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	cfg := parseFivetranConfig(step.Config)
	if cfg.APIKey == "" || cfg.APISecret == "" {
		return nil, fmt.Errorf("apiKey and apiSecret are required")
	}

	groupID := getString(step.Config, "groupId")
	if groupID == "" {
		return nil, fmt.Errorf("groupId is required")
	}

	service := getString(step.Config, "service")
	if service == "" {
		return nil, fmt.Errorf("service is required")
	}

	config := getMap(step.Config, "config")
	if config == nil {
		return nil, fmt.Errorf("config is required")
	}

	// Build request body
	requestBody := map[string]interface{}{
		"group_id": groupID,
		"service":  service,
		"config":   config,
	}

	if name := getString(step.Config, "name"); name != "" {
		requestBody["name"] = name
	}

	if paused := step.Config["paused"]; paused != nil {
		requestBody["paused"] = paused
	}

	if tags := getMap(step.Config, "tags"); tags != nil && len(tags) > 0 {
		requestBody["tags"] = tags
	}

	respBody, err := e.doRequest(ctx, "POST", "/connectors", cfg, requestBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create connector: %w", err)
	}

	// Parse response
	var response struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Data    struct {
			ID           string `json:"id"`
			GroupID      string `json:"group_id"`
			Service      string `json:"service"`
			Schema       string `json:"schema"`
			Paused       bool   `json:"paused"`
			Name         string `json:"name"`
			CreatedAt    string `json:"created_at"`
			CreatedBy    string `json:"created_by"`
			ServiceVersion string `json:"service_version"`
		} `json:"data"`
	}

	if err := json.Unmarshal(respBody, &response); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	output := map[string]interface{}{
		"success":      true,
		"code":         response.Code,
		"message":      response.Message,
		"connector_id": response.Data.ID,
		"group_id":     response.Data.GroupID,
		"service":      response.Data.Service,
		"name":         response.Data.Name,
		"created_at":   response.Data.CreatedAt,
		"created_by":   response.Data.CreatedBy,
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// ============================================================================
// CONNECTOR SYNC EXECUTOR
// ============================================================================

// ConnectorSyncExecutor handles fivetran-connector-sync node type
type ConnectorSyncExecutor struct {
	*FivetranSkill
}

func (e *ConnectorSyncExecutor) Type() string { return "fivetran-connector-sync" }

func (e *ConnectorSyncExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	cfg := parseFivetranConfig(step.Config)
	if cfg.APIKey == "" || cfg.APISecret == "" {
		return nil, fmt.Errorf("apiKey and apiSecret are required")
	}

	connectorID := getString(step.Config, "connectorId")
	if connectorID == "" {
		return nil, fmt.Errorf("connectorId is required")
	}

	endpoint := fmt.Sprintf("/connectors/%s/force", connectorID)

	respBody, err := e.doRequest(ctx, "POST", endpoint, cfg, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to trigger sync: %w", err)
	}

	// Parse response
	var response struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}

	if err := json.Unmarshal(respBody, &response); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	output := map[string]interface{}{
		"success":    true,
		"code":       response.Code,
		"message":    response.Message,
		"connector_id": connectorID,
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// ============================================================================
// CONNECTOR STATUS EXECUTOR
// ============================================================================

// ConnectorStatusExecutor handles fivetran-connector-status node type
type ConnectorStatusExecutor struct {
	*FivetranSkill
}

func (e *ConnectorStatusExecutor) Type() string { return "fivetran-connector-status" }

func (e *ConnectorStatusExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	cfg := parseFivetranConfig(step.Config)
	if cfg.APIKey == "" || cfg.APISecret == "" {
		return nil, fmt.Errorf("apiKey and apiSecret are required")
	}

	connectorID := getString(step.Config, "connectorId")
	if connectorID == "" {
		return nil, fmt.Errorf("connectorId is required")
	}

	endpoint := fmt.Sprintf("/connectors/%s", connectorID)

	respBody, err := e.doRequest(ctx, "GET", endpoint, cfg, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get connector status: %w", err)
	}

	// Parse response
	var response struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Data    struct {
			ID              string `json:"id"`
			GroupID         string `json:"group_id"`
			Service         string `json:"service"`
			Schema          string `json:"schema"`
			Paused          bool   `json:"paused"`
			PausedReason    string `json:"paused_reason"`
			Name            string `json:"name"`
			SyncFrequency   int    `json:"sync_frequency"`
			SucceededAt     string `json:"succeeded_at"`
			FailedAt        string `json:"failed_at"`
			ServiceVersion  string `json:"service_version"`
			CreatedAt       string `json:"created_at"`
			CreatedBy       string `json:"created_by"`
			SetupState      string `json:"setup_state"`
			Status          struct {
				SetupState     string `json:"setup_state"`
				IsHistoricalSync bool   `json:"is_historical_sync"`
				Syncing        bool   `json:"syncing"`
				Tasks          []struct {
					ID      string `json:"id"`
					Name    string `json:"name"`
					Info    string `json:"info"`
					Status  string `json:"status"`
				} `json:"tasks"`
				Warnings []struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"warnings"`
			} `json:"status"`
		} `json:"data"`
	}

	if err := json.Unmarshal(respBody, &response); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	output := map[string]interface{}{
		"success":         true,
		"connector_id":    response.Data.ID,
		"group_id":        response.Data.GroupID,
		"service":         response.Data.Service,
		"schema":          response.Data.Schema,
		"name":            response.Data.Name,
		"paused":          response.Data.Paused,
		"sync_frequency":  response.Data.SyncFrequency,
		"succeeded_at":    response.Data.SucceededAt,
		"failed_at":       response.Data.FailedAt,
		"setup_state":     response.Data.SetupState,
		"created_at":      response.Data.CreatedAt,
		"created_by":      response.Data.CreatedBy,
		"service_version": response.Data.ServiceVersion,
		"syncing":         response.Data.Status.Syncing,
	}

	if response.Data.PausedReason != "" {
		output["paused_reason"] = response.Data.PausedReason
	}

	// Add status details
	if response.Data.Status.SetupState != "" {
		output["status_setup_state"] = response.Data.Status.SetupState
	}
	output["is_historical_sync"] = response.Data.Status.IsHistoricalSync

	// Add tasks if present
	if len(response.Data.Status.Tasks) > 0 {
		var tasks []map[string]interface{}
		for _, task := range response.Data.Status.Tasks {
			tasks = append(tasks, map[string]interface{}{
				"id":     task.ID,
				"name":   task.Name,
				"info":   task.Info,
				"status": task.Status,
			})
		}
		output["tasks"] = tasks
	}

	// Add warnings if present
	if len(response.Data.Status.Warnings) > 0 {
		var warnings []map[string]interface{}
		for _, warning := range response.Data.Status.Warnings {
			warnings = append(warnings, map[string]interface{}{
				"code":    warning.Code,
				"message": warning.Message,
			})
		}
		output["warnings"] = warnings
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// ============================================================================
// DESTINATION LIST EXECUTOR
// ============================================================================

// DestinationListExecutor handles fivetran-destination-list node type
type DestinationListExecutor struct {
	*FivetranSkill
}

func (e *DestinationListExecutor) Type() string { return "fivetran-destination-list" }

func (e *DestinationListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	cfg := parseFivetranConfig(step.Config)
	if cfg.APIKey == "" || cfg.APISecret == "" {
		return nil, fmt.Errorf("apiKey and apiSecret are required")
	}

	limit := getInt(step.Config, "limit", 100)

	endpoint := fmt.Sprintf("/destinations?limit=%d", limit)

	respBody, err := e.doRequest(ctx, "GET", endpoint, cfg, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list destinations: %w", err)
	}

	// Parse response
	var response struct {
		Data struct {
			Items []struct {
				ID             string `json:"id"`
				GroupID        string `json:"group_id"`
				Service        string `json:"service"`
				Region         string `json:"region"`
				TimeZoneOffset string `json:"time_zone_offset"`
				CreatedAt      string `json:"created_at"`
				CreatedBy      string `json:"created_by"`
			} `json:"items"`
			NextCursor string `json:"next_cursor"`
		} `json:"data"`
	}

	if err := json.Unmarshal(respBody, &response); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Build output
	var destinations []map[string]interface{}
	for _, item := range response.Data.Items {
		destination := map[string]interface{}{
			"id":              item.ID,
			"group_id":        item.GroupID,
			"service":         item.Service,
			"region":          item.Region,
			"time_zone_offset": item.TimeZoneOffset,
			"created_at":      item.CreatedAt,
			"created_by":      item.CreatedBy,
		}
		destinations = append(destinations, destination)
	}

	output := map[string]interface{}{
		"destinations": destinations,
		"total":        len(destinations),
	}
	if response.Data.NextCursor != "" {
		output["next_cursor"] = response.Data.NextCursor
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// ============================================================================
// SCHEMA CONFIG EXECUTOR
// ============================================================================

// SchemaConfigExecutor handles fivetran-schema-config node type
type SchemaConfigExecutor struct {
	*FivetranSkill
}

func (e *SchemaConfigExecutor) Type() string { return "fivetran-schema-config" }

func (e *SchemaConfigExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	cfg := parseFivetranConfig(step.Config)
	if cfg.APIKey == "" || cfg.APISecret == "" {
		return nil, fmt.Errorf("apiKey and apiSecret are required")
	}

	connectorID := getString(step.Config, "connectorId")
	if connectorID == "" {
		return nil, fmt.Errorf("connectorId is required")
	}

	schema := getString(step.Config, "schema")
	if schema == "" {
		return nil, fmt.Errorf("schema is required")
	}

	table := getString(step.Config, "table")

	// Build request body
	requestBody := make(map[string]interface{})

	// Handle enabled field
	if enabled, ok := step.Config["enabled"]; ok {
		if table != "" {
			// Table-level config
			if requestBody["tables"] == nil {
				requestBody["tables"] = map[string]interface{}{}
			}
			tables := requestBody["tables"].(map[string]interface{})
			tables[table] = map[string]interface{}{
				"enabled": enabled,
			}
		} else {
			// Schema-level config
			requestBody["enabled"] = enabled
		}
	}

	// Handle sync mode (incremental vs full)
	if syncMode, ok := step.Config["syncMode"]; ok {
		if table != "" {
			if requestBody["tables"] == nil {
				requestBody["tables"] = map[string]interface{}{}
			}
			tables := requestBody["tables"].(map[string]interface{})
			if tables[table] == nil {
				tables[table] = map[string]interface{}{}
			}
			tableConfig := tables[table].(map[string]interface{})
			// syncMode=true means incremental, false means full
			tableConfig["sync_mode"] = "incremental"
			if syncMode == false {
				tableConfig["sync_mode"] = "full"
			}
		}
	}

	// Handle column configuration
	if columnConfig := getMap(step.Config, "columnConfig"); columnConfig != nil {
		if table != "" {
			if requestBody["tables"] == nil {
				requestBody["tables"] = map[string]interface{}{}
			}
			tables := requestBody["tables"].(map[string]interface{})
			if tables[table] == nil {
				tables[table] = map[string]interface{}{}
			}
			tableConfig := tables[table].(map[string]interface{})
			tableConfig["columns"] = columnConfig
		}
	}

	// Build endpoint
	var endpoint string
	if table != "" {
		endpoint = fmt.Sprintf("/connectors/%s/schemas/%s/tables/%s", connectorID, schema, table)
	} else {
		endpoint = fmt.Sprintf("/connectors/%s/schemas/%s", connectorID, schema)
	}

	respBody, err := e.doRequest(ctx, "PATCH", endpoint, cfg, requestBody)
	if err != nil {
		return nil, fmt.Errorf("failed to configure schema: %w", err)
	}

	// Parse response
	var response struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}

	if err := json.Unmarshal(respBody, &response); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	output := map[string]interface{}{
		"success":     true,
		"code":        response.Code,
		"message":     response.Message,
		"connector_id": connectorID,
		"schema":      schema,
	}
	if table != "" {
		output["table"] = table
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// ============================================================================
// GROUP LIST EXECUTOR
// ============================================================================

// GroupListExecutor handles fivetran-group-list node type
type GroupListExecutor struct {
	*FivetranSkill
}

func (e *GroupListExecutor) Type() string { return "fivetran-group-list" }

func (e *GroupListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	cfg := parseFivetranConfig(step.Config)
	if cfg.APIKey == "" || cfg.APISecret == "" {
		return nil, fmt.Errorf("apiKey and apiSecret are required")
	}

	limit := getInt(step.Config, "limit", 100)

	endpoint := fmt.Sprintf("/groups?limit=%d", limit)

	respBody, err := e.doRequest(ctx, "GET", endpoint, cfg, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list groups: %w", err)
	}

	// Parse response
	var response struct {
		Data struct {
			Items []struct {
				ID          string `json:"id"`
				Name        string `json:"name"`
				CreatedAt   string `json:"created_at"`
				CreatedBy   string `json:"created_by"`
				Plan        string `json:"plan"`
				Region      string `json:"region"`
			} `json:"items"`
			NextCursor string `json:"next_cursor"`
		} `json:"data"`
	}

	if err := json.Unmarshal(respBody, &response); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Build output
	var groups []map[string]interface{}
	for _, item := range response.Data.Items {
		group := map[string]interface{}{
			"id":         item.ID,
			"name":       item.Name,
			"created_at": item.CreatedAt,
			"created_by": item.CreatedBy,
			"plan":       item.Plan,
			"region":     item.Region,
		}
		groups = append(groups, group)
	}

	output := map[string]interface{}{
		"groups": groups,
		"total":  len(groups),
	}
	if response.Data.NextCursor != "" {
		output["next_cursor"] = response.Data.NextCursor
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// ============================================================================
// MAIN
// ============================================================================

func main() {
	port := os.Getenv("SKILL_PORT")
	if port == "" {
		port = "50102"
	}

	server := grpc.NewSkillServer("skill-fivetran", "1.0.0")

	skill := NewFivetranSkill()

	// Register executors with schemas
	server.RegisterExecutorWithSchema("fivetran-connector-list", &ConnectorListExecutor{skill}, ConnectorListSchema)
	server.RegisterExecutorWithSchema("fivetran-connector-create", &ConnectorCreateExecutor{skill}, ConnectorCreateSchema)
	server.RegisterExecutorWithSchema("fivetran-connector-sync", &ConnectorSyncExecutor{skill}, ConnectorSyncSchema)
	server.RegisterExecutorWithSchema("fivetran-connector-status", &ConnectorStatusExecutor{skill}, ConnectorStatusSchema)
	server.RegisterExecutorWithSchema("fivetran-destination-list", &DestinationListExecutor{skill}, DestinationListSchema)
	server.RegisterExecutorWithSchema("fivetran-schema-config", &SchemaConfigExecutor{skill}, SchemaConfigSchema)
	server.RegisterExecutorWithSchema("fivetran-group-list", &GroupListExecutor{skill}, GroupListSchema)

	fmt.Printf("Starting skill-fivetran gRPC server on port %s\n", port)
	if err := server.Serve(port); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to start server: %v\n", err)
		os.Exit(1)
	}
}
