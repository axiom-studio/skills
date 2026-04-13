package main

import (
	"bytes"
	"context"
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
	iconAirbyte = "database"
)

// AirbyteConfig holds Airbyte API configuration
type AirbyteConfig struct {
	APIToken    string
	WorkspaceID string
	BaseURL     string
}

// HTTPClient wraps http.Client with Airbyte-specific methods
type AirbyteHTTPClient struct {
	client    *http.Client
	baseURL   string
	apiToken  string
	userAgent string
}

// NewAirbyteHTTPClient creates a new Airbyte HTTP client
func NewAirbyteHTTPClient(baseURL, apiToken string) *AirbyteHTTPClient {
	return &AirbyteHTTPClient{
		client: &http.Client{
			Timeout: 120 * time.Second,
		},
		baseURL:   strings.TrimSuffix(baseURL, "/"),
		apiToken:  apiToken,
		userAgent: "skill-airbyte/1.0.0",
	}
}

// doRequest performs an HTTP request to the Airbyte API
func (c *AirbyteHTTPClient) doRequest(ctx context.Context, method, path string, body interface{}) ([]byte, error) {
	var reqBody io.Reader
	if body != nil {
		jsonData, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
		reqBody = bytes.NewReader(jsonData)
	}

	url := c.baseURL + path
	req, err := http.NewRequestWithContext(ctx, method, url, reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Authorization", "Bearer "+c.apiToken)

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
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

// Get performs a GET request
func (c *AirbyteHTTPClient) Get(ctx context.Context, path string) ([]byte, error) {
	return c.doRequest(ctx, http.MethodGet, path, nil)
}

// Post performs a POST request
func (c *AirbyteHTTPClient) Post(ctx context.Context, path string, body interface{}) ([]byte, error) {
	return c.doRequest(ctx, http.MethodPost, path, body)
}

// Delete performs a DELETE request
func (c *AirbyteHTTPClient) Delete(ctx context.Context, path string) ([]byte, error) {
	return c.doRequest(ctx, http.MethodDelete, path, nil)
}

// ============================================================================
// API RESPONSE STRUCTURES
// ============================================================================

// Source represents an Airbyte source
type Source struct {
	ID                    string                 `json:"sourceId"`
	Name                  string                 `json:"name"`
	SourceDefinitionID    string                 `json:"sourceDefinitionId"`
	WorkspaceID           string                 `json:"workspaceId"`
	ConnectionConfiguration map[string]interface{} `json:"connectionConfiguration,omitempty"`
	CreatedAt             string                 `json:"createdAt,omitempty"`
	UpdatedAt             string                 `json:"updatedAt,omitempty"`
}

// SourceDefinition represents an available source definition
type SourceDefinition struct {
	ID               string   `json:"sourceDefinitionId"`
	Name             string   `json:"name"`
	DockerRepository string   `json:"dockerRepository"`
	DockerImageTag   string   `json:"dockerImageTag"`
	Spec             Spec     `json:"spec,omitempty"`
}

// Destination represents an Airbyte destination
type Destination struct {
	ID                      string                 `json:"destinationId"`
	Name                    string                 `json:"name"`
	DestinationDefinitionID string                 `json:"destinationDefinitionId"`
	WorkspaceID             string                 `json:"workspaceId"`
	ConnectionConfiguration map[string]interface{} `json:"connectionConfiguration,omitempty"`
	CreatedAt               string                 `json:"createdAt,omitempty"`
	UpdatedAt               string                 `json:"updatedAt,omitempty"`
}

// DestinationDefinition represents an available destination definition
type DestinationDefinition struct {
	ID               string `json:"destinationDefinitionId"`
	Name             string `json:"name"`
	DockerRepository string `json:"dockerRepository"`
	DockerImageTag   string `json:"dockerImageTag"`
	Spec             Spec   `json:"spec,omitempty"`
}

// Spec represents connector specification
type Spec struct {
	ConnectionSpecification map[string]interface{} `json:"connectionSpecification,omitempty"`
	DocumentationURL        string                 `json:"documentationUrl,omitempty"`
	ChangeLogURL            string                 `json:"changelogUrl,omitempty"`
}

// Connection represents an Airbyte connection
type Connection struct {
	ID               string                 `json:"connectionId"`
	Name             string                 `json:"name,omitempty"`
	SourceID         string                 `json:"sourceId"`
	DestinationID    string                 `json:"destinationId"`
	SyncSchedule     *SyncSchedule          `json:"syncSchedule,omitempty"`
	Configurations   []StreamConfiguration  `json:"configurations,omitempty"`
	Status           string                 `json:"status,omitempty"`
	CreatedAt        string                 `json:"createdAt,omitempty"`
	UpdatedAt        string                 `json:"updatedAt,omitempty"`
}

// SyncSchedule represents a sync schedule
type SyncSchedule struct {
	Units    int64  `json:"units,omitempty"`
	Timezone string `json:"timezone,omitempty"`
}

// StreamConfiguration represents stream-level configuration
type StreamConfiguration struct {
	Name      string   `json:"name"`
	Enabled   bool     `json:"enabled"`
	CursorField []string `json:"cursorField,omitempty"`
	PrimaryKey  []string `json:"primaryKey,omitempty"`
}

// Job represents an Airbyte sync job
type Job struct {
	ID         string     `json:"jobId"`
	ConfigType string     `json:"configType"`
	ConfigID   string     `json:"configId"`
	Status     string     `json:"status"`
	StartTime  int64      `json:"startTime,omitempty"`
	EndTime    int64      `json:"endTime,omitempty"`
	UpdatedAt  int64      `json:"updatedAt,omitempty"`
}

// JobAttempt represents a job attempt
type JobAttempt struct {
	ID        int64  `json:"id"`
	Status    string `json:"status"`
	StartTime int64  `json:"startTime,omitempty"`
	EndTime   int64  `json:"endTime,omitempty"`
}

// JobInfo represents detailed job information
type JobInfo struct {
	Job     Job        `json:"job"`
	Attempts []JobAttempt `json:"attempts,omitempty"`
}

// Workspace represents an Airbyte workspace
type Workspace struct {
	ID        string `json:"workspaceId"`
	Name      string `json:"name"`
	Slug      string `json:"slug,omitempty"`
}

// ============================================================================
// API REQUEST STRUCTURES
// ============================================================================

// CreateSourceRequest represents a request to create a source
type CreateSourceRequest struct {
	Name                  string                 `json:"name"`
	SourceDefinitionID    string                 `json:"sourceDefinitionId"`
	WorkspaceID           string                 `json:"workspaceId"`
	ConnectionConfiguration map[string]interface{} `json:"connectionConfiguration"`
}

// CreateDestinationRequest represents a request to create a destination
type CreateDestinationRequest struct {
	Name                      string                 `json:"name"`
	DestinationDefinitionID   string                 `json:"destinationDefinitionId"`
	WorkspaceID               string                 `json:"workspaceId"`
	ConnectionConfiguration   map[string]interface{} `json:"connectionConfiguration"`
}

// CreateConnectionRequest represents a request to create a connection
type CreateConnectionRequest struct {
	Name            string                `json:"name,omitempty"`
	SourceID        string                `json:"sourceId"`
	DestinationID   string                `json:"destinationId"`
	SyncSchedule    *SyncSchedule         `json:"syncSchedule,omitempty"`
	Configurations  []StreamConfiguration `json:"configurations,omitempty"`
	Status          string                `json:"status,omitempty"`
}

// TriggerSyncRequest represents a request to trigger a sync
type TriggerSyncRequest struct {
	ConnectionID string `json:"connectionId"`
}

// ListJobsRequest represents a request to list jobs
type ListJobsRequest struct {
	ConnectionID string `json:"connectionId,omitempty"`
	WorkspaceID  string `json:"workspaceId,omitempty"`
	Status       string `json:"status,omitempty"`
	Limit        int    `json:"limit,omitempty"`
	Offset       int    `json:"offset,omitempty"`
}

// ============================================================================
// MAIN FUNCTION
// ============================================================================

func main() {
	// Get port from env or use default
	port := os.Getenv("SKILL_PORT")
	if port == "" {
		port = "50101"
	}

	// Create skill server
	server := grpc.NewSkillServer("skill-airbyte", "1.0.0")

	// Register executors with schemas
	server.RegisterExecutorWithSchema("airbyte-source-list", &SourceListExecutor{}, SourceListSchema)
	server.RegisterExecutorWithSchema("airbyte-source-create", &SourceCreateExecutor{}, SourceCreateSchema)
	server.RegisterExecutorWithSchema("airbyte-destination-list", &DestinationListExecutor{}, DestinationListSchema)
	server.RegisterExecutorWithSchema("airbyte-destination-create", &DestinationCreateExecutor{}, DestinationCreateSchema)
	server.RegisterExecutorWithSchema("airbyte-connection-list", &ConnectionListExecutor{}, ConnectionListSchema)
	server.RegisterExecutorWithSchema("airbyte-connection-create", &ConnectionCreateExecutor{}, ConnectionCreateSchema)
	server.RegisterExecutorWithSchema("airbyte-connection-sync", &ConnectionSyncExecutor{}, ConnectionSyncSchema)
	server.RegisterExecutorWithSchema("airbyte-job-status", &JobStatusExecutor{}, JobStatusSchema)
	server.RegisterExecutorWithSchema("airbyte-job-list", &JobListExecutor{}, JobListSchema)

	fmt.Printf("Starting skill-airbyte gRPC server on port %s\n", port)
	if err := server.Serve(port); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to serve: %v\n", err)
		os.Exit(1)
	}
}

// ============================================================================
// CONFIG HELPERS
// ============================================================================

// getString safely gets a string from a map
func getString(config map[string]interface{}, key string) string {
	if v, ok := config[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// getInt safely gets an int from a map
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

// getBool safely gets a bool from a map
func getBool(config map[string]interface{}, key string, def bool) bool {
	if v, ok := config[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return def
}

// getMap safely gets a map from a map
func getMap(config map[string]interface{}, key string) map[string]interface{} {
	if v, ok := config[key]; ok {
		if m, ok := v.(map[string]interface{}); ok {
			return m
		}
	}
	return nil
}

// getInterfaceSlice safely gets a slice of interfaces from a map
func getInterfaceSlice(config map[string]interface{}, key string) []interface{} {
	if v, ok := config[key]; ok {
		if arr, ok := v.([]interface{}); ok {
			return arr
		}
	}
	return nil
}

// parseAirbyteConfig extracts Airbyte configuration from config map
func parseAirbyteConfig(config map[string]interface{}) AirbyteConfig {
	baseURL := getString(config, "baseURL")
	if baseURL == "" {
		baseURL = "http://localhost:8000/api/v1"
	}
	
	return AirbyteConfig{
		APIToken:    getString(config, "apiToken"),
		WorkspaceID: getString(config, "workspaceId"),
		BaseURL:     baseURL,
	}
}

// ============================================================================
// SOURCE LIST EXECUTOR
// ============================================================================

// SourceListExecutor handles airbyte-source-list node type
type SourceListExecutor struct{}

func (e *SourceListExecutor) Type() string { return "airbyte-source-list" }

func (e *SourceListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)
	airbyteCfg := parseAirbyteConfig(config)

	if airbyteCfg.APIToken == "" {
		return nil, fmt.Errorf("apiToken is required")
	}
	if airbyteCfg.WorkspaceID == "" {
		return nil, fmt.Errorf("workspaceId is required")
	}

	client := NewAirbyteHTTPClient(airbyteCfg.BaseURL, airbyteCfg.APIToken)

	// Call Airbyte API to list sources
	respBody, err := client.Post(ctx, "/sources/list", map[string]string{
		"workspaceId": airbyteCfg.WorkspaceID,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list sources: %w", err)
	}

	var result struct {
		Sources []Source `json:"sources"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Convert to output format
	sources := make([]map[string]interface{}, 0, len(result.Sources))
	for _, src := range result.Sources {
		sources = append(sources, map[string]interface{}{
			"id":                    src.ID,
			"name":                  src.Name,
			"sourceDefinitionId":    src.SourceDefinitionID,
			"workspaceId":           src.WorkspaceID,
			"createdAt":             src.CreatedAt,
			"updatedAt":             src.UpdatedAt,
		})
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success": true,
			"sources": sources,
			"count":   len(sources),
		},
	}, nil
}

// ============================================================================
// SOURCE CREATE EXECUTOR
// ============================================================================

// SourceCreateExecutor handles airbyte-source-create node type
type SourceCreateExecutor struct{}

func (e *SourceCreateExecutor) Type() string { return "airbyte-source-create" }

func (e *SourceCreateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)
	airbyteCfg := parseAirbyteConfig(config)

	if airbyteCfg.APIToken == "" {
		return nil, fmt.Errorf("apiToken is required")
	}
	if airbyteCfg.WorkspaceID == "" {
		return nil, fmt.Errorf("workspaceId is required")
	}

	name := getString(config, "name")
	if name == "" {
		return nil, fmt.Errorf("name is required")
	}

	sourceDefinitionID := getString(config, "sourceDefinitionId")
	if sourceDefinitionID == "" {
		return nil, fmt.Errorf("sourceDefinitionId is required")
	}

	connectionConfig := getMap(config, "connectionConfiguration")
	if connectionConfig == nil {
		connectionConfig = make(map[string]interface{})
	}

	client := NewAirbyteHTTPClient(airbyteCfg.BaseURL, airbyteCfg.APIToken)

	req := CreateSourceRequest{
		Name:                  name,
		SourceDefinitionID:    sourceDefinitionID,
		WorkspaceID:           airbyteCfg.WorkspaceID,
		ConnectionConfiguration: connectionConfig,
	}

	respBody, err := client.Post(ctx, "/sources/create", req)
	if err != nil {
		return nil, fmt.Errorf("failed to create source: %w", err)
	}

	var result Source
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success": true,
			"source": map[string]interface{}{
				"id":                 result.ID,
				"name":               result.Name,
				"sourceDefinitionId": result.SourceDefinitionID,
				"workspaceId":        result.WorkspaceID,
				"createdAt":          result.CreatedAt,
			},
			"sourceId": result.ID,
		},
	}, nil
}

// ============================================================================
// DESTINATION LIST EXECUTOR
// ============================================================================

// DestinationListExecutor handles airbyte-destination-list node type
type DestinationListExecutor struct{}

func (e *DestinationListExecutor) Type() string { return "airbyte-destination-list" }

func (e *DestinationListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)
	airbyteCfg := parseAirbyteConfig(config)

	if airbyteCfg.APIToken == "" {
		return nil, fmt.Errorf("apiToken is required")
	}
	if airbyteCfg.WorkspaceID == "" {
		return nil, fmt.Errorf("workspaceId is required")
	}

	client := NewAirbyteHTTPClient(airbyteCfg.BaseURL, airbyteCfg.APIToken)

	// Call Airbyte API to list destinations
	respBody, err := client.Post(ctx, "/destinations/list", map[string]string{
		"workspaceId": airbyteCfg.WorkspaceID,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list destinations: %w", err)
	}

	var result struct {
		Destinations []Destination `json:"destinations"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Convert to output format
	destinations := make([]map[string]interface{}, 0, len(result.Destinations))
	for _, dst := range result.Destinations {
		destinations = append(destinations, map[string]interface{}{
			"id":                      dst.ID,
			"name":                    dst.Name,
			"destinationDefinitionId": dst.DestinationDefinitionID,
			"workspaceId":             dst.WorkspaceID,
			"createdAt":               dst.CreatedAt,
			"updatedAt":               dst.UpdatedAt,
		})
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":      true,
			"destinations": destinations,
			"count":        len(destinations),
		},
	}, nil
}

// ============================================================================
// DESTINATION CREATE EXECUTOR
// ============================================================================

// DestinationCreateExecutor handles airbyte-destination-create node type
type DestinationCreateExecutor struct{}

func (e *DestinationCreateExecutor) Type() string { return "airbyte-destination-create" }

func (e *DestinationCreateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)
	airbyteCfg := parseAirbyteConfig(config)

	if airbyteCfg.APIToken == "" {
		return nil, fmt.Errorf("apiToken is required")
	}
	if airbyteCfg.WorkspaceID == "" {
		return nil, fmt.Errorf("workspaceId is required")
	}

	name := getString(config, "name")
	if name == "" {
		return nil, fmt.Errorf("name is required")
	}

	destinationDefinitionID := getString(config, "destinationDefinitionId")
	if destinationDefinitionID == "" {
		return nil, fmt.Errorf("destinationDefinitionId is required")
	}

	connectionConfig := getMap(config, "connectionConfiguration")
	if connectionConfig == nil {
		connectionConfig = make(map[string]interface{})
	}

	client := NewAirbyteHTTPClient(airbyteCfg.BaseURL, airbyteCfg.APIToken)

	req := CreateDestinationRequest{
		Name:                    name,
		DestinationDefinitionID: destinationDefinitionID,
		WorkspaceID:             airbyteCfg.WorkspaceID,
		ConnectionConfiguration: connectionConfig,
	}

	respBody, err := client.Post(ctx, "/destinations/create", req)
	if err != nil {
		return nil, fmt.Errorf("failed to create destination: %w", err)
	}

	var result Destination
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success": true,
			"destination": map[string]interface{}{
				"id":                      result.ID,
				"name":                    result.Name,
				"destinationDefinitionId": result.DestinationDefinitionID,
				"workspaceId":             result.WorkspaceID,
				"createdAt":               result.CreatedAt,
			},
			"destinationId": result.ID,
		},
	}, nil
}

// ============================================================================
// CONNECTION LIST EXECUTOR
// ============================================================================

// ConnectionListExecutor handles airbyte-connection-list node type
type ConnectionListExecutor struct{}

func (e *ConnectionListExecutor) Type() string { return "airbyte-connection-list" }

func (e *ConnectionListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)
	airbyteCfg := parseAirbyteConfig(config)

	if airbyteCfg.APIToken == "" {
		return nil, fmt.Errorf("apiToken is required")
	}
	if airbyteCfg.WorkspaceID == "" {
		return nil, fmt.Errorf("workspaceId is required")
	}

	client := NewAirbyteHTTPClient(airbyteCfg.BaseURL, airbyteCfg.APIToken)

	// Call Airbyte API to list connections
	respBody, err := client.Post(ctx, "/connections/list", map[string]string{
		"workspaceId": airbyteCfg.WorkspaceID,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list connections: %w", err)
	}

	var result struct {
		Connections []Connection `json:"connections"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Convert to output format
	connections := make([]map[string]interface{}, 0, len(result.Connections))
	for _, conn := range result.Connections {
		connMap := map[string]interface{}{
			"id":            conn.ID,
			"name":          conn.Name,
			"sourceId":      conn.SourceID,
			"destinationId": conn.DestinationID,
			"status":        conn.Status,
			"createdAt":     conn.CreatedAt,
			"updatedAt":     conn.UpdatedAt,
		}
		if conn.SyncSchedule != nil {
			connMap["syncSchedule"] = map[string]interface{}{
				"units":    conn.SyncSchedule.Units,
				"timezone": conn.SyncSchedule.Timezone,
			}
		}
		connections = append(connections, connMap)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":     true,
			"connections": connections,
			"count":       len(connections),
		},
	}, nil
}

// ============================================================================
// CONNECTION CREATE EXECUTOR
// ============================================================================

// ConnectionCreateExecutor handles airbyte-connection-create node type
type ConnectionCreateExecutor struct{}

func (e *ConnectionCreateExecutor) Type() string { return "airbyte-connection-create" }

func (e *ConnectionCreateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)
	airbyteCfg := parseAirbyteConfig(config)

	if airbyteCfg.APIToken == "" {
		return nil, fmt.Errorf("apiToken is required")
	}
	if airbyteCfg.WorkspaceID == "" {
		return nil, fmt.Errorf("workspaceId is required")
	}

	sourceID := getString(config, "sourceId")
	if sourceID == "" {
		return nil, fmt.Errorf("sourceId is required")
	}

	destinationID := getString(config, "destinationId")
	if destinationID == "" {
		return nil, fmt.Errorf("destinationId is required")
	}

	client := NewAirbyteHTTPClient(airbyteCfg.BaseURL, airbyteCfg.APIToken)

	// Build sync schedule if provided
	var syncSchedule *SyncSchedule
	if scheduleCfg := getMap(config, "syncSchedule"); scheduleCfg != nil {
		timezone := getString(scheduleCfg, "timezone")
		if timezone == "" {
			timezone = "UTC"
		}
		syncSchedule = &SyncSchedule{
			Units:    int64(getInt(scheduleCfg, "units", 60)),
			Timezone: timezone,
		}
	}

	// Build stream configurations if provided
	var configurations []StreamConfiguration
	if configsRaw := getInterfaceSlice(config, "configurations"); configsRaw != nil {
		for _, cfgRaw := range configsRaw {
			if cfgMap, ok := cfgRaw.(map[string]interface{}); ok {
				configurations = append(configurations, StreamConfiguration{
					Name:    getString(cfgMap, "name"),
					Enabled: getBool(cfgMap, "enabled", true),
				})
			}
		}
	}

	req := CreateConnectionRequest{
		Name:           getString(config, "name"),
		SourceID:       sourceID,
		DestinationID:  destinationID,
		SyncSchedule:   syncSchedule,
		Configurations: configurations,
		Status:         "active",
	}

	respBody, err := client.Post(ctx, "/connections/create", req)
	if err != nil {
		return nil, fmt.Errorf("failed to create connection: %w", err)
	}

	var result Connection
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success": true,
			"connection": map[string]interface{}{
				"id":            result.ID,
				"name":          result.Name,
				"sourceId":      result.SourceID,
				"destinationId": result.DestinationID,
				"status":        result.Status,
				"createdAt":     result.CreatedAt,
			},
			"connectionId": result.ID,
		},
	}, nil
}

// ============================================================================
// CONNECTION SYNC EXECUTOR
// ============================================================================

// ConnectionSyncExecutor handles airbyte-connection-sync node type
type ConnectionSyncExecutor struct{}

func (e *ConnectionSyncExecutor) Type() string { return "airbyte-connection-sync" }

func (e *ConnectionSyncExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)
	airbyteCfg := parseAirbyteConfig(config)

	if airbyteCfg.APIToken == "" {
		return nil, fmt.Errorf("apiToken is required")
	}

	connectionID := getString(config, "connectionId")
	if connectionID == "" {
		return nil, fmt.Errorf("connectionId is required")
	}

	client := NewAirbyteHTTPClient(airbyteCfg.BaseURL, airbyteCfg.APIToken)

	req := TriggerSyncRequest{
		ConnectionID: connectionID,
	}

	respBody, err := client.Post(ctx, "/connections/sync", req)
	if err != nil {
		return nil, fmt.Errorf("failed to trigger sync: %w", err)
	}

	var result struct {
		ConnectionID string `json:"connectionId"`
		Job          Job    `json:"job"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success": true,
			"jobId":   result.Job.ID,
			"status":  result.Job.Status,
			"message": "Sync job triggered successfully",
			"job": map[string]interface{}{
				"id":         result.Job.ID,
				"configType": result.Job.ConfigType,
				"configId":   result.Job.ConfigID,
				"status":     result.Job.Status,
				"startTime":  result.Job.StartTime,
			},
		},
	}, nil
}

// ============================================================================
// JOB STATUS EXECUTOR
// ============================================================================

// JobStatusExecutor handles airbyte-job-status node type
type JobStatusExecutor struct{}

func (e *JobStatusExecutor) Type() string { return "airbyte-job-status" }

func (e *JobStatusExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)
	airbyteCfg := parseAirbyteConfig(config)

	if airbyteCfg.APIToken == "" {
		return nil, fmt.Errorf("apiToken is required")
	}

	jobID := getString(config, "jobId")
	if jobID == "" {
		return nil, fmt.Errorf("jobId is required")
	}

	client := NewAirbyteHTTPClient(airbyteCfg.BaseURL, airbyteCfg.APIToken)

	// Call Airbyte API to get job status
	respBody, err := client.Post(ctx, "/jobs/get", map[string]string{
		"jobId": jobID,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get job status: %w", err)
	}

	var result JobInfo
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	attempts := make([]map[string]interface{}, 0, len(result.Attempts))
	for _, attempt := range result.Attempts {
		attempts = append(attempts, map[string]interface{}{
			"id":        attempt.ID,
			"status":    attempt.Status,
			"startTime": attempt.StartTime,
			"endTime":   attempt.EndTime,
		})
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success": true,
			"job": map[string]interface{}{
				"id":         result.Job.ID,
				"configType": result.Job.ConfigType,
				"configId":   result.Job.ConfigID,
				"status":     result.Job.Status,
				"startTime":  result.Job.StartTime,
				"endTime":    result.Job.EndTime,
				"updatedAt":  result.Job.UpdatedAt,
			},
			"attempts": attempts,
			"jobId":    result.Job.ID,
			"status":   result.Job.Status,
		},
	}, nil
}

// ============================================================================
// JOB LIST EXECUTOR
// ============================================================================

// JobListExecutor handles airbyte-job-list node type
type JobListExecutor struct{}

func (e *JobListExecutor) Type() string { return "airbyte-job-list" }

func (e *JobListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)
	airbyteCfg := parseAirbyteConfig(config)

	if airbyteCfg.APIToken == "" {
		return nil, fmt.Errorf("apiToken is required")
	}

	client := NewAirbyteHTTPClient(airbyteCfg.BaseURL, airbyteCfg.APIToken)

	// Build request
	req := ListJobsRequest{
		ConnectionID: getString(config, "connectionId"),
		WorkspaceID:  airbyteCfg.WorkspaceID,
		Status:       getString(config, "status"),
		Limit:        getInt(config, "limit", 50),
		Offset:       getInt(config, "offset", 0),
	}

	respBody, err := client.Post(ctx, "/jobs/list", req)
	if err != nil {
		return nil, fmt.Errorf("failed to list jobs: %w", err)
	}

	var result struct {
		Jobs []Job `json:"jobs"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Convert to output format
	jobs := make([]map[string]interface{}, 0, len(result.Jobs))
	for _, job := range result.Jobs {
		jobs = append(jobs, map[string]interface{}{
			"id":         job.ID,
			"configType": job.ConfigType,
			"configId":   job.ConfigID,
			"status":     job.Status,
			"startTime":  job.StartTime,
			"endTime":    job.EndTime,
			"updatedAt":  job.UpdatedAt,
		})
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success": true,
			"jobs":    jobs,
			"count":   len(jobs),
		},
	}, nil
}

// ============================================================================
// SCHEMAS
// ============================================================================

// SourceListSchema is the UI schema for airbyte-source-list
var SourceListSchema = resolver.NewSchemaBuilder("airbyte-source-list").
	WithName("List Sources").
	WithCategory("action").
	WithIcon(iconAirbyte).
	WithDescription("List all sources in an Airbyte workspace").
	AddSection("Connection").
		AddExpressionField("apiToken", "API Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("Your Airbyte API token"),
			resolver.WithHint("Airbyte API token from Settings > API"),
			resolver.WithSensitive(),
		).
		AddExpressionField("workspaceId", "Workspace ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("workspace-id"),
			resolver.WithHint("Airbyte workspace ID"),
		).
		AddExpressionField("baseURL", "Base URL",
			resolver.WithDefault("http://localhost:8000/api/v1"),
			resolver.WithPlaceholder("http://localhost:8000/api/v1"),
			resolver.WithHint("Airbyte API base URL"),
		).
		EndSection().
	Build()

// SourceCreateSchema is the UI schema for airbyte-source-create
var SourceCreateSchema = resolver.NewSchemaBuilder("airbyte-source-create").
	WithName("Create Source").
	WithCategory("action").
	WithIcon(iconAirbyte).
	WithDescription("Create a new source in Airbyte").
	AddSection("Connection").
		AddExpressionField("apiToken", "API Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("Your Airbyte API token"),
			resolver.WithHint("Airbyte API token from Settings > API"),
			resolver.WithSensitive(),
		).
		AddExpressionField("workspaceId", "Workspace ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("workspace-id"),
			resolver.WithHint("Airbyte workspace ID"),
		).
		AddExpressionField("baseURL", "Base URL",
			resolver.WithDefault("http://localhost:8000/api/v1"),
			resolver.WithPlaceholder("http://localhost:8000/api/v1"),
			resolver.WithHint("Airbyte API base URL"),
		).
		EndSection().
	AddSection("Source Configuration").
		AddExpressionField("name", "Source Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("My Database"),
			resolver.WithHint("Name for the new source"),
		).
		AddExpressionField("sourceDefinitionId", "Source Definition ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("source-definition-id"),
			resolver.WithHint("ID of the source definition (connector) to use"),
		).
		AddJSONField("connectionConfiguration", "Connection Configuration",
			resolver.WithRequired(),
			resolver.WithHeight(200),
			resolver.WithHint("Source-specific configuration (host, port, credentials, etc.)"),
		).
		EndSection().
	Build()

// DestinationListSchema is the UI schema for airbyte-destination-list
var DestinationListSchema = resolver.NewSchemaBuilder("airbyte-destination-list").
	WithName("List Destinations").
	WithCategory("action").
	WithIcon(iconAirbyte).
	WithDescription("List all destinations in an Airbyte workspace").
	AddSection("Connection").
		AddExpressionField("apiToken", "API Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("Your Airbyte API token"),
			resolver.WithHint("Airbyte API token from Settings > API"),
			resolver.WithSensitive(),
		).
		AddExpressionField("workspaceId", "Workspace ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("workspace-id"),
			resolver.WithHint("Airbyte workspace ID"),
		).
		AddExpressionField("baseURL", "Base URL",
			resolver.WithDefault("http://localhost:8000/api/v1"),
			resolver.WithPlaceholder("http://localhost:8000/api/v1"),
			resolver.WithHint("Airbyte API base URL"),
		).
		EndSection().
	Build()

// DestinationCreateSchema is the UI schema for airbyte-destination-create
var DestinationCreateSchema = resolver.NewSchemaBuilder("airbyte-destination-create").
	WithName("Create Destination").
	WithCategory("action").
	WithIcon(iconAirbyte).
	WithDescription("Create a new destination in Airbyte").
	AddSection("Connection").
		AddExpressionField("apiToken", "API Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("Your Airbyte API token"),
			resolver.WithHint("Airbyte API token from Settings > API"),
			resolver.WithSensitive(),
		).
		AddExpressionField("workspaceId", "Workspace ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("workspace-id"),
			resolver.WithHint("Airbyte workspace ID"),
		).
		AddExpressionField("baseURL", "Base URL",
			resolver.WithDefault("http://localhost:8000/api/v1"),
			resolver.WithPlaceholder("http://localhost:8000/api/v1"),
			resolver.WithHint("Airbyte API base URL"),
		).
		EndSection().
	AddSection("Destination Configuration").
		AddExpressionField("name", "Destination Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("My Data Warehouse"),
			resolver.WithHint("Name for the new destination"),
		).
		AddExpressionField("destinationDefinitionId", "Destination Definition ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("destination-definition-id"),
			resolver.WithHint("ID of the destination definition (connector) to use"),
		).
		AddJSONField("connectionConfiguration", "Connection Configuration",
			resolver.WithRequired(),
			resolver.WithHeight(200),
			resolver.WithHint("Destination-specific configuration (host, port, credentials, etc.)"),
		).
		EndSection().
	Build()

// ConnectionListSchema is the UI schema for airbyte-connection-list
var ConnectionListSchema = resolver.NewSchemaBuilder("airbyte-connection-list").
	WithName("List Connections").
	WithCategory("action").
	WithIcon(iconAirbyte).
	WithDescription("List all connections in an Airbyte workspace").
	AddSection("Connection").
		AddExpressionField("apiToken", "API Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("Your Airbyte API token"),
			resolver.WithHint("Airbyte API token from Settings > API"),
			resolver.WithSensitive(),
		).
		AddExpressionField("workspaceId", "Workspace ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("workspace-id"),
			resolver.WithHint("Airbyte workspace ID"),
		).
		AddExpressionField("baseURL", "Base URL",
			resolver.WithDefault("http://localhost:8000/api/v1"),
			resolver.WithPlaceholder("http://localhost:8000/api/v1"),
			resolver.WithHint("Airbyte API base URL"),
		).
		EndSection().
	Build()

// ConnectionCreateSchema is the UI schema for airbyte-connection-create
var ConnectionCreateSchema = resolver.NewSchemaBuilder("airbyte-connection-create").
	WithName("Create Connection").
	WithCategory("action").
	WithIcon(iconAirbyte).
	WithDescription("Create a new connection between a source and destination").
	AddSection("Connection").
		AddExpressionField("apiToken", "API Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("Your Airbyte API token"),
			resolver.WithHint("Airbyte API token from Settings > API"),
			resolver.WithSensitive(),
		).
		AddExpressionField("workspaceId", "Workspace ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("workspace-id"),
			resolver.WithHint("Airbyte workspace ID"),
		).
		AddExpressionField("baseURL", "Base URL",
			resolver.WithDefault("http://localhost:8000/api/v1"),
			resolver.WithPlaceholder("http://localhost:8000/api/v1"),
			resolver.WithHint("Airbyte API base URL"),
		).
		EndSection().
	AddSection("Connection Configuration").
		AddExpressionField("name", "Connection Name",
			resolver.WithPlaceholder("My Data Pipeline"),
			resolver.WithHint("Optional name for the connection"),
		).
		AddExpressionField("sourceId", "Source ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("source-id"),
			resolver.WithHint("ID of the source to connect"),
		).
		AddExpressionField("destinationId", "Destination ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("destination-id"),
			resolver.WithHint("ID of the destination to connect"),
		).
		EndSection().
	AddSection("Sync Schedule").
		AddJSONField("syncSchedule", "Sync Schedule",
			resolver.WithHeight(100),
			resolver.WithHint(`Schedule config: {"units": 60, "timezone": "UTC"} for every 60 minutes`),
		).
		AddJSONField("configurations", "Stream Configurations",
			resolver.WithHeight(150),
			resolver.WithHint(`Stream configs: [{"name": "users", "enabled": true}]`),
		).
		EndSection().
	Build()

// ConnectionSyncSchema is the UI schema for airbyte-connection-sync
var ConnectionSyncSchema = resolver.NewSchemaBuilder("airbyte-connection-sync").
	WithName("Trigger Sync").
	WithCategory("action").
	WithIcon(iconAirbyte).
	WithDescription("Trigger a manual sync for a connection").
	AddSection("Connection").
		AddExpressionField("apiToken", "API Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("Your Airbyte API token"),
			resolver.WithHint("Airbyte API token from Settings > API"),
			resolver.WithSensitive(),
		).
		AddExpressionField("baseURL", "Base URL",
			resolver.WithDefault("http://localhost:8000/api/v1"),
			resolver.WithPlaceholder("http://localhost:8000/api/v1"),
			resolver.WithHint("Airbyte API base URL"),
		).
		EndSection().
	AddSection("Sync").
		AddExpressionField("connectionId", "Connection ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("connection-id"),
			resolver.WithHint("ID of the connection to sync"),
		).
		EndSection().
	Build()

// JobStatusSchema is the UI schema for airbyte-job-status
var JobStatusSchema = resolver.NewSchemaBuilder("airbyte-job-status").
	WithName("Get Job Status").
	WithCategory("action").
	WithIcon(iconAirbyte).
	WithDescription("Get the status of a sync job").
	AddSection("Connection").
		AddExpressionField("apiToken", "API Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("Your Airbyte API token"),
			resolver.WithHint("Airbyte API token from Settings > API"),
			resolver.WithSensitive(),
		).
		AddExpressionField("baseURL", "Base URL",
			resolver.WithDefault("http://localhost:8000/api/v1"),
			resolver.WithPlaceholder("http://localhost:8000/api/v1"),
			resolver.WithHint("Airbyte API base URL"),
		).
		EndSection().
	AddSection("Job").
		AddExpressionField("jobId", "Job ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("job-id"),
			resolver.WithHint("ID of the job to check"),
		).
		EndSection().
	Build()

// JobListSchema is the UI schema for airbyte-job-list
var JobListSchema = resolver.NewSchemaBuilder("airbyte-job-list").
	WithName("List Jobs").
	WithCategory("action").
	WithIcon(iconAirbyte).
	WithDescription("List sync jobs with optional filters").
	AddSection("Connection").
		AddExpressionField("apiToken", "API Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("Your Airbyte API token"),
			resolver.WithHint("Airbyte API token from Settings > API"),
			resolver.WithSensitive(),
		).
		AddExpressionField("workspaceId", "Workspace ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("workspace-id"),
			resolver.WithHint("Airbyte workspace ID"),
		).
		AddExpressionField("baseURL", "Base URL",
			resolver.WithDefault("http://localhost:8000/api/v1"),
			resolver.WithPlaceholder("http://localhost:8000/api/v1"),
			resolver.WithHint("Airbyte API base URL"),
		).
		EndSection().
	AddSection("Filters").
		AddExpressionField("connectionId", "Connection ID",
			resolver.WithPlaceholder("connection-id"),
			resolver.WithHint("Filter by connection ID"),
		).
		AddSelectField("status", "Status",
			[]resolver.SelectOption{
				{Label: "All", Value: ""},
				{Label: "Pending", Value: "pending"},
				{Label: "Running", Value: "running"},
				{Label: "Succeeded", Value: "succeeded"},
				{Label: "Failed", Value: "failed"},
				{Label: "Cancelled", Value: "cancelled"},
			},
			resolver.WithDefault(""),
			resolver.WithHint("Filter by job status"),
		).
		AddNumberField("limit", "Limit",
			resolver.WithDefault(50),
			resolver.WithMinMax(1, 1000),
			resolver.WithHint("Maximum number of jobs to return"),
		).
		AddNumberField("offset", "Offset",
			resolver.WithDefault(0),
			resolver.WithHint("Number of jobs to skip"),
		).
		EndSection().
	Build()
