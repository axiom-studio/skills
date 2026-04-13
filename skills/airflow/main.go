package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/axiom-studio/skills.sdk/executor"
	"github.com/axiom-studio/skills.sdk/grpc"
)

const (
	defaultAirflowAPIVersion = "v1"
	defaultTimeout           = 60 * time.Second
)

// AirflowSkill holds the HTTP client and configuration
type AirflowSkill struct {
	client *http.Client
}

// NewAirflowSkill creates a new Airflow skill instance
func NewAirflowSkill() *AirflowSkill {
	return &AirflowSkill{
		client: &http.Client{
			Timeout: defaultTimeout,
		},
	}
}

// Type returns the executor type
func (s *AirflowSkill) Type() string {
	return "airflow-generic"
}

// Execute is a placeholder for the interface
func (s *AirflowSkill) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	return &executor.StepResult{Output: map[string]interface{}{}}, nil
}

// ============================================================================
// HTTP CLIENT HELPERS
// ============================================================================

// getAuthHeader builds the appropriate authentication header
func getAuthHeader(config map[string]interface{}) (string, string, error) {
	authType := getString(config, "authType")
	
	switch authType {
	case "basic", "":
		username := getString(config, "username")
		password := getString(config, "password")
		if username == "" || password == "" {
			return "", "", fmt.Errorf("username and password required for basic auth")
		}
		credentials := base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
		return "Authorization", "Basic " + credentials, nil
		
	case "bearer", "token":
		token := getString(config, "token")
		if token == "" {
			return "", "", fmt.Errorf("token required for bearer auth")
		}
		return "Authorization", "Bearer " + token, nil
		
	default:
		return "", "", fmt.Errorf("unsupported auth type: %s", authType)
	}
}

// doRequest performs an HTTP request to the Airflow API
func (s *AirflowSkill) doRequest(ctx context.Context, method, server, path string, config map[string]interface{}, body interface{}) ([]byte, error) {
	// Build URL
	baseURL := strings.TrimSuffix(server, "/")
	fullURL := fmt.Sprintf("%s/api/v1/%s", baseURL, path)

	// Prepare request body
	var reqBody io.Reader
	if body != nil {
		jsonBody, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
		reqBody = bytes.NewReader(jsonBody)
	}

	req, err := http.NewRequestWithContext(ctx, method, fullURL, reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	// Add authentication
	authHeader, authValue, err := getAuthHeader(config)
	if err != nil {
		return nil, err
	}
	req.Header.Set(authHeader, authValue)

	// Execute request
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	// Read response
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	// Check for errors
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("airflow API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	return respBody, nil
}

// ============================================================================
// CONFIG HELPERS
// ============================================================================

// getString safely gets a string from config
func getString(config map[string]interface{}, key string) string {
	if v, ok := config[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// getInt safely gets an int from config
func getInt(config map[string]interface{}, key string, def int) int {
	if v, ok := config[key]; ok {
		switch n := v.(type) {
		case float64:
			return int(n)
		case int:
			return n
		case string:
			if i, err := strconv.Atoi(n); err == nil {
				return i
			}
		}
	}
	return def
}

// getBool safely gets a bool from config
func getBool(config map[string]interface{}, key string, def bool) bool {
	if v, ok := config[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
		if s, ok := v.(string); ok {
			return strings.ToLower(s) == "true" || s == "1"
		}
	}
	return def
}

// ============================================================================
// DAG LIST
// ============================================================================

// DAGListExecutor handles airflow-dag-list
type DAGListExecutor struct {
	*AirflowSkill
}

func (e *DAGListExecutor) Type() string {
	return "airflow-dag-list"
}

func (e *DAGListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, res executor.TemplateResolver) (*executor.StepResult, error) {
	config := res.ResolveMap(step.Config)

	server := getString(config, "server")
	if server == "" {
		return nil, fmt.Errorf("server URL is required")
	}

	// Build query params
	queryParams := url.Values{}
	
	if limit := getInt(config, "limit", 100); limit > 0 {
		queryParams.Set("limit", strconv.Itoa(limit))
	}
	if offset := getInt(config, "offset", 0); offset > 0 {
		queryParams.Set("offset", strconv.Itoa(offset))
	}
	
	// Filter by paused status
	if paused, ok := config["paused"]; ok {
		switch v := paused.(type) {
		case bool:
			queryParams.Set("paused", strconv.FormatBool(v))
		case string:
			if v != "" {
				queryParams.Set("paused", v)
			}
		}
	}

	// Filter by tags
	if tags := getString(config, "tags"); tags != "" {
		queryParams.Set("tags", tags)
	}

	// Filter by DAG ID pattern
	if dagPattern := getString(config, "dagPattern"); dagPattern != "" {
		queryParams.Set("dag_id_pattern", dagPattern)
	}

	path := "dags"
	if len(queryParams) > 0 {
		path += "?" + queryParams.Encode()
	}

	respBody, err := e.doRequest(ctx, "GET", server, path, config, nil)
	if err != nil {
		return nil, err
	}

	// Parse response
	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	output := map[string]interface{}{
		"result": string(respBody),
	}

	// Extract DAG list
	if dags, ok := result["dags"].([]interface{}); ok {
		dagIDs := make([]string, 0, len(dags))
		for _, dag := range dags {
			if dagMap, ok := dag.(map[string]interface{}); ok {
				if dagID, ok := dagMap["dag_id"].(string); ok {
					dagIDs = append(dagIDs, dagID)
				}
			}
		}
		output["dag_ids"] = dagIDs
		output["count"] = len(dagIDs)
	}

	return &executor.StepResult{Output: output}, nil
}

// ============================================================================
// DAG TRIGGER
// ============================================================================

// DAGTriggerExecutor handles airflow-dag-trigger
type DAGTriggerExecutor struct {
	*AirflowSkill
}

func (e *DAGTriggerExecutor) Type() string {
	return "airflow-dag-trigger"
}

func (e *DAGTriggerExecutor) Execute(ctx context.Context, step *executor.StepDefinition, res executor.TemplateResolver) (*executor.StepResult, error) {
	config := res.ResolveMap(step.Config)

	server := getString(config, "server")
	if server == "" {
		return nil, fmt.Errorf("server URL is required")
	}

	dagID := getString(config, "dagId")
	if dagID == "" {
		return nil, fmt.Errorf("dagId is required")
	}

	// Build request body
	body := map[string]interface{}{}

	// Add conf (configuration) if provided
	if confStr := getString(config, "conf"); confStr != "" {
		var conf map[string]interface{}
		if err := json.Unmarshal([]byte(confStr), &conf); err != nil {
			return nil, fmt.Errorf("invalid conf JSON: %w", err)
		}
		body["conf"] = conf
	}

	// Add dagRunId if provided
	if dagRunID := getString(config, "dagRunId"); dagRunID != "" {
		body["dag_run_id"] = dagRunID
	}

	// Add dataIntervalStart
	if dataIntervalStart := getString(config, "dataIntervalStart"); dataIntervalStart != "" {
		body["data_interval_start"] = dataIntervalStart
	}

	// Add dataIntervalEnd
	if dataIntervalEnd := getString(config, "dataIntervalEnd"); dataIntervalEnd != "" {
		body["data_interval_end"] = dataIntervalEnd
	}

	// Add logicalDate
	if logicalDate := getString(config, "logicalDate"); logicalDate != "" {
		body["logical_date"] = logicalDate
	}

	// Add note
	if note := getString(config, "note"); note != "" {
		body["note"] = note
	}

	path := fmt.Sprintf("dags/%s/dagRuns", url.PathEscape(dagID))

	respBody, err := e.doRequest(ctx, "POST", server, path, config, body)
	if err != nil {
		return nil, err
	}

	// Parse response
	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	output := map[string]interface{}{
		"result": string(respBody),
	}

	// Extract key fields
	if dagRunID, ok := result["dag_run_id"].(string); ok {
		output["dagRunId"] = dagRunID
	}
	if state, ok := result["state"].(string); ok {
		output["state"] = state
	}
	if executionDate, ok := result["execution_date"].(string); ok {
		output["executionDate"] = executionDate
	}
	if startDate, ok := result["start_date"].(string); ok {
		output["startDate"] = startDate
	}

	return &executor.StepResult{Output: output}, nil
}

// ============================================================================
// DAG STATUS
// ============================================================================

// DAGStatusExecutor handles airflow-dag-status
type DAGStatusExecutor struct {
	*AirflowSkill
}

func (e *DAGStatusExecutor) Type() string {
	return "airflow-dag-status"
}

func (e *DAGStatusExecutor) Execute(ctx context.Context, step *executor.StepDefinition, res executor.TemplateResolver) (*executor.StepResult, error) {
	config := res.ResolveMap(step.Config)

	server := getString(config, "server")
	if server == "" {
		return nil, fmt.Errorf("server URL is required")
	}

	dagID := getString(config, "dagId")
	if dagID == "" {
		return nil, fmt.Errorf("dagId is required")
	}

	dagRunID := getString(config, "dagRunId")
	if dagRunID == "" {
		return nil, fmt.Errorf("dagRunId is required")
	}

	path := fmt.Sprintf("dags/%s/dagRuns/%s", url.PathEscape(dagID), url.PathEscape(dagRunID))

	respBody, err := e.doRequest(ctx, "GET", server, path, config, nil)
	if err != nil {
		return nil, err
	}

	// Parse response
	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	output := map[string]interface{}{
		"result": string(respBody),
	}

	// Extract key fields
	if state, ok := result["state"].(string); ok {
		output["state"] = state
	}
	if dagRunID, ok := result["dag_run_id"].(string); ok {
		output["dagRunId"] = dagRunID
	}
	if executionDate, ok := result["execution_date"].(string); ok {
		output["executionDate"] = executionDate
	}
	if startDate, ok := result["start_date"].(string); ok {
		output["startDate"] = startDate
	}
	if endDate, ok := result["end_date"].(string); ok {
		output["endDate"] = endDate
	}
	if duration, ok := result["duration"].(float64); ok {
		output["duration"] = duration
	}

	return &executor.StepResult{Output: output}, nil
}

// ============================================================================
// DAG PAUSE
// ============================================================================

// DAGPauseExecutor handles airflow-dag-pause
type DAGPauseExecutor struct {
	*AirflowSkill
}

func (e *DAGPauseExecutor) Type() string {
	return "airflow-dag-pause"
}

func (e *DAGPauseExecutor) Execute(ctx context.Context, step *executor.StepDefinition, res executor.TemplateResolver) (*executor.StepResult, error) {
	config := res.ResolveMap(step.Config)

	server := getString(config, "server")
	if server == "" {
		return nil, fmt.Errorf("server URL is required")
	}

	dagID := getString(config, "dagId")
	if dagID == "" {
		return nil, fmt.Errorf("dagId is required")
	}

	// Pause the DAG
	body := map[string]interface{}{
		"is_paused": true,
	}

	path := fmt.Sprintf("dags/%s", url.PathEscape(dagID))

	respBody, err := e.doRequest(ctx, "PATCH", server, path, config, body)
	if err != nil {
		return nil, err
	}

	// Parse response
	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	output := map[string]interface{}{
		"result":  string(respBody),
		"success": "true",
		"paused":  "true",
	}

	if dagIDResult, ok := result["dag_id"].(string); ok {
		output["dagId"] = dagIDResult
	}

	return &executor.StepResult{Output: output}, nil
}

// ============================================================================
// DAG UNPAUSE
// ============================================================================

// DAGUnpauseExecutor handles airflow-dag-unpause
type DAGUnpauseExecutor struct {
	*AirflowSkill
}

func (e *DAGUnpauseExecutor) Type() string {
	return "airflow-dag-unpause"
}

func (e *DAGUnpauseExecutor) Execute(ctx context.Context, step *executor.StepDefinition, res executor.TemplateResolver) (*executor.StepResult, error) {
	config := res.ResolveMap(step.Config)

	server := getString(config, "server")
	if server == "" {
		return nil, fmt.Errorf("server URL is required")
	}

	dagID := getString(config, "dagId")
	if dagID == "" {
		return nil, fmt.Errorf("dagId is required")
	}

	// Unpause the DAG
	body := map[string]interface{}{
		"is_paused": false,
	}

	path := fmt.Sprintf("dags/%s", url.PathEscape(dagID))

	respBody, err := e.doRequest(ctx, "PATCH", server, path, config, body)
	if err != nil {
		return nil, err
	}

	// Parse response
	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	output := map[string]interface{}{
		"result":   string(respBody),
		"success":  "true",
		"paused":   "false",
		"unpaused": "true",
	}

	if dagIDResult, ok := result["dag_id"].(string); ok {
		output["dagId"] = dagIDResult
	}

	return &executor.StepResult{Output: output}, nil
}

// ============================================================================
// TASK LIST
// ============================================================================

// TaskListExecutor handles airflow-task-list
type TaskListExecutor struct {
	*AirflowSkill
}

func (e *TaskListExecutor) Type() string {
	return "airflow-task-list"
}

func (e *TaskListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, res executor.TemplateResolver) (*executor.StepResult, error) {
	config := res.ResolveMap(step.Config)

	server := getString(config, "server")
	if server == "" {
		return nil, fmt.Errorf("server URL is required")
	}

	dagID := getString(config, "dagId")
	if dagID == "" {
		return nil, fmt.Errorf("dagId is required")
	}

	// Build query params
	queryParams := url.Values{}
	
	if limit := getInt(config, "limit", 100); limit > 0 {
		queryParams.Set("limit", strconv.Itoa(limit))
	}
	if offset := getInt(config, "offset", 0); offset > 0 {
		queryParams.Set("offset", strconv.Itoa(offset))
	}

	path := fmt.Sprintf("dags/%s/tasks", url.PathEscape(dagID))
	if len(queryParams) > 0 {
		path += "?" + queryParams.Encode()
	}

	respBody, err := e.doRequest(ctx, "GET", server, path, config, nil)
	if err != nil {
		return nil, err
	}

	// Parse response
	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	output := map[string]interface{}{
		"result": string(respBody),
	}

	// Extract task IDs
	if tasks, ok := result["tasks"].([]interface{}); ok {
		taskIDs := make([]string, 0, len(tasks))
		for _, task := range tasks {
			if taskMap, ok := task.(map[string]interface{}); ok {
				if taskID, ok := taskMap["task_id"].(string); ok {
					taskIDs = append(taskIDs, taskID)
				}
			}
		}
		output["task_ids"] = taskIDs
		output["count"] = len(taskIDs)
	}

	return &executor.StepResult{Output: output}, nil
}

// ============================================================================
// TASK LOGS
// ============================================================================

// TaskLogsExecutor handles airflow-task-logs
type TaskLogsExecutor struct {
	*AirflowSkill
}

func (e *TaskLogsExecutor) Type() string {
	return "airflow-task-logs"
}

func (e *TaskLogsExecutor) Execute(ctx context.Context, step *executor.StepDefinition, res executor.TemplateResolver) (*executor.StepResult, error) {
	config := res.ResolveMap(step.Config)

	server := getString(config, "server")
	if server == "" {
		return nil, fmt.Errorf("server URL is required")
	}

	dagID := getString(config, "dagId")
	if dagID == "" {
		return nil, fmt.Errorf("dagId is required")
	}

	dagRunID := getString(config, "dagRunId")
	if dagRunID == "" {
		return nil, fmt.Errorf("dagRunId is required")
	}

	taskID := getString(config, "taskId")
	if taskID == "" {
		return nil, fmt.Errorf("taskId is required")
	}

	// Build path
	path := fmt.Sprintf("dags/%s/dagRuns/%s/taskInstances/%s/logs", 
		url.PathEscape(dagID), 
		url.PathEscape(dagRunID), 
		url.PathEscape(taskID))

	// Add optional query parameters
	queryParams := url.Values{}
	
	if tryNumber := getInt(config, "tryNumber", 0); tryNumber > 0 {
		queryParams.Set("try_number", strconv.Itoa(tryNumber))
	}

	if len(queryParams) > 0 {
		path += "?" + queryParams.Encode()
	}

	respBody, err := e.doRequest(ctx, "GET", server, path, config, nil)
	if err != nil {
		return nil, err
	}

	// Parse response
	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	output := map[string]interface{}{
		"result": string(respBody),
	}

	// Extract log content
	if content, ok := result["content"].([]interface{}); ok {
		logLines := make([]string, 0, len(content))
		for _, line := range content {
			if lineStr, ok := line.(string); ok {
				logLines = append(logLines, lineStr)
			}
		}
		output["logs"] = strings.Join(logLines, "\n")
		output["log_lines"] = logLines
	}

	if totalLines, ok := result["total_lines"].(float64); ok {
		output["totalLines"] = int(totalLines)
	}

	return &executor.StepResult{Output: output}, nil
}

// ============================================================================
// RUN LIST
// ============================================================================

// RunListExecutor handles airflow-run-list
type RunListExecutor struct {
	*AirflowSkill
}

func (e *RunListExecutor) Type() string {
	return "airflow-run-list"
}

func (e *RunListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, res executor.TemplateResolver) (*executor.StepResult, error) {
	config := res.ResolveMap(step.Config)

	server := getString(config, "server")
	if server == "" {
		return nil, fmt.Errorf("server URL is required")
	}

	dagID := getString(config, "dagId")
	if dagID == "" {
		return nil, fmt.Errorf("dagId is required")
	}

	// Build path with query parameters
	queryParams := url.Values{}
	
	if limit := getInt(config, "limit", 100); limit > 0 {
		queryParams.Set("limit", strconv.Itoa(limit))
	}
	if offset := getInt(config, "offset", 0); offset > 0 {
		queryParams.Set("offset", strconv.Itoa(offset))
	}

	// Filter by state
	if state := getString(config, "state"); state != "" {
		queryParams.Set("state", state)
	}

	// Filter by execution date
	if executionDate := getString(config, "executionDate"); executionDate != "" {
		queryParams.Set("execution_date", executionDate)
	}

	// Filter by start date range
	if startDateGte := getString(config, "startDateGte"); startDateGte != "" {
		queryParams.Set("start_date_gte", startDateGte)
	}
	if startDateLte := getString(config, "startDateLte"); startDateLte != "" {
		queryParams.Set("start_date_lte", startDateLte)
	}

	// Filter by end date range
	if endDateGte := getString(config, "endDateGte"); endDateGte != "" {
		queryParams.Set("end_date_gte", endDateGte)
	}
	if endDateLte := getString(config, "endDateLte"); endDateLte != "" {
		queryParams.Set("end_date_lte", endDateLte)
	}

	path := fmt.Sprintf("dags/%s/dagRuns", url.PathEscape(dagID))
	if len(queryParams) > 0 {
		path += "?" + queryParams.Encode()
	}

	respBody, err := e.doRequest(ctx, "GET", server, path, config, nil)
	if err != nil {
		return nil, err
	}

	// Parse response
	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	output := map[string]interface{}{
		"result": string(respBody),
	}

	// Extract DAG runs
	if dagRuns, ok := result["dag_runs"].([]interface{}); ok {
		runIDs := make([]string, 0, len(dagRuns))
		runStates := make(map[string]string)
		
		for _, run := range dagRuns {
			if runMap, ok := run.(map[string]interface{}); ok {
				if runID, ok := runMap["dag_run_id"].(string); ok {
					runIDs = append(runIDs, runID)
					if state, ok := runMap["state"].(string); ok {
						runStates[runID] = state
					}
				}
			}
		}
		output["run_ids"] = runIDs
		output["run_states"] = runStates
		output["count"] = len(runIDs)
	}

	return &executor.StepResult{Output: output}, nil
}

// ============================================================================
// CONNECTION LIST
// ============================================================================

// ConnectionListExecutor handles airflow-connection-list
type ConnectionListExecutor struct {
	*AirflowSkill
}

func (e *ConnectionListExecutor) Type() string {
	return "airflow-connection-list"
}

func (e *ConnectionListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, res executor.TemplateResolver) (*executor.StepResult, error) {
	config := res.ResolveMap(step.Config)

	server := getString(config, "server")
	if server == "" {
		return nil, fmt.Errorf("server URL is required")
	}

	// Build query params
	queryParams := url.Values{}
	
	if limit := getInt(config, "limit", 100); limit > 0 {
		queryParams.Set("limit", strconv.Itoa(limit))
	}
	if offset := getInt(config, "offset", 0); offset > 0 {
		queryParams.Set("offset", strconv.Itoa(offset))
	}

	path := "connections"
	if len(queryParams) > 0 {
		path += "?" + queryParams.Encode()
	}

	respBody, err := e.doRequest(ctx, "GET", server, path, config, nil)
	if err != nil {
		return nil, err
	}

	// Parse response
	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	output := map[string]interface{}{
		"result": string(respBody),
	}

	// Extract connection IDs
	if connections, ok := result["connections"].([]interface{}); ok {
		connIDs := make([]string, 0, len(connections))
		connTypes := make(map[string]string)
		
		for _, conn := range connections {
			if connMap, ok := conn.(map[string]interface{}); ok {
				if connID, ok := connMap["connection_id"].(string); ok {
					connIDs = append(connIDs, connID)
					if connType, ok := connMap["conn_type"].(string); ok {
						connTypes[connID] = connType
					}
				}
			}
		}
		output["connection_ids"] = connIDs
		output["connection_types"] = connTypes
		output["count"] = len(connIDs)
	}

	return &executor.StepResult{Output: output}, nil
}

// ============================================================================
// MAIN
// ============================================================================

func main() {
	port := os.Getenv("SKILL_PORT")
	if port == "" {
		port = "50104"
	}

	server := grpc.NewSkillServer("skill-airflow", "1.0.0")

	skill := NewAirflowSkill()

	// Register DAG executors
	server.RegisterExecutor("airflow-dag-list", &DAGListExecutor{skill})
	server.RegisterExecutor("airflow-dag-trigger", &DAGTriggerExecutor{skill})
	server.RegisterExecutor("airflow-dag-status", &DAGStatusExecutor{skill})
	server.RegisterExecutor("airflow-dag-pause", &DAGPauseExecutor{skill})
	server.RegisterExecutor("airflow-dag-unpause", &DAGUnpauseExecutor{skill})

	// Register task executors
	server.RegisterExecutor("airflow-task-list", &TaskListExecutor{skill})
	server.RegisterExecutor("airflow-task-logs", &TaskLogsExecutor{skill})

	// Register run executors
	server.RegisterExecutor("airflow-run-list", &RunListExecutor{skill})

	// Register connection executors
	server.RegisterExecutor("airflow-connection-list", &ConnectionListExecutor{skill})

	fmt.Printf("Starting skill-airflow gRPC server on port %s\n", port)
	if err := server.Serve(port); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to start server: %v\n", err)
		os.Exit(1)
	}
}
