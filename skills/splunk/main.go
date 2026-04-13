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
	"time"

	"github.com/axiom-studio/skills.sdk/executor"
	"github.com/axiom-studio/skills.sdk/grpc"
)

// SplunkSkill represents the Splunk skill server
type SplunkSkill struct {
	grpc.SkillServer
	client *http.Client
}

// NewSplunkSkill creates a new Splunk skill instance
func NewSplunkSkill() *SplunkSkill {
	return &SplunkSkill{
		client: &http.Client{
			Timeout: 120 * time.Second,
		},
	}
}

// ============================================================================
// HELPER FUNCTIONS
// ============================================================================

// getString safely gets a string from interface config
func getString(config map[string]interface{}, key string) string {
	if v, ok := config[key]; ok {
		switch val := v.(type) {
		case string:
			return val
		case []byte:
			return string(val)
		case float64:
			return fmt.Sprintf("%v", val)
		case int:
			return fmt.Sprintf("%d", val)
		default:
			return fmt.Sprintf("%v", val)
		}
	}
	return ""
}

// getBool safely gets a bool from interface config
func getBool(config map[string]interface{}, key string) bool {
	v := getString(config, key)
	return v == "true" || v == "1" || v == "yes"
}

// getInt safely gets an int from interface config
func getInt(config map[string]interface{}, key string, def int) int {
	if v, ok := config[key]; ok {
		switch val := v.(type) {
		case float64:
			return int(val)
		case int:
			return val
		case string:
			var i int
			fmt.Sscanf(val, "%d", &i)
			return i
		}
	}
	return def
}

// getInterfaceSlice safely gets a slice of interfaces from config
func getInterfaceSlice(config map[string]interface{}, key string) []interface{} {
	if v, ok := config[key]; ok {
		switch val := v.(type) {
		case []interface{}:
			return val
		case []map[string]interface{}:
			result := make([]interface{}, len(val))
			for i, item := range val {
				result[i] = item
			}
			return result
		}
	}
	return nil
}

// doRequest performs an HTTP request to the Splunk API
func (s *SplunkSkill) doRequest(ctx context.Context, method, server, path, authToken string, params url.Values, body interface{}) ([]byte, error) {
	var reqBody io.Reader
	var contentType string

	if body != nil {
		switch b := body.(type) {
		case url.Values:
			reqBody = strings.NewReader(b.Encode())
			contentType = "application/x-www-form-urlencoded"
		case []byte:
			reqBody = bytes.NewReader(b)
			contentType = "application/json"
		default:
			jsonBody, err := json.Marshal(body)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal request body: %w", err)
			}
			reqBody = bytes.NewReader(jsonBody)
			contentType = "application/json"
		}
	}

	// Build URL with query params if provided
	fullURL := fmt.Sprintf("%s/%s", strings.TrimSuffix(server, "/"), strings.TrimPrefix(path, "/"))
	if params != nil && len(params) > 0 {
		fullURL += "?" + params.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, method, fullURL, reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set authentication - Splunk supports both Bearer token and Basic auth
	if authToken != "" {
		// Check if it's a bearer token or basic auth credentials
		if strings.Contains(authToken, ":") {
			// Basic auth format: username:password
			req.SetBasicAuth(strings.Split(authToken, ":")[0], strings.Split(authToken, ":")[1])
		} else {
			// Bearer token
			req.Header.Set("Authorization", "Bearer "+authToken)
		}
	}

	// Set content type
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	// Accept JSON response
	req.Header.Set("Accept", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	// Check for error status codes
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("splunk API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	return respBody, nil
}

// makeOutput creates an output map
func makeOutput(data map[string]interface{}) map[string]interface{} {
	return data
}

// ============================================================================
// SPLUNK SEARCH EXECUTOR
// ============================================================================

// SearchExecutor handles splunk-search
type SearchExecutor struct {
	client *http.Client
}

func (e *SearchExecutor) Type() string {
	return "splunk-search"
}

func (e *SearchExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	server := resolver.ResolveString(getString(config, "server"))
	authToken := resolver.ResolveString(getString(config, "authToken"))
	search := resolver.ResolveString(getString(config, "search"))
	earliest := resolver.ResolveString(getString(config, "earliest"))
	latest := resolver.ResolveString(getString(config, "latest"))
	index := resolver.ResolveString(getString(config, "index"))
	maxCount := resolver.ResolveString(getString(config, "maxCount"))
	outputMode := resolver.ResolveString(getString(config, "outputMode"))

	if search == "" {
		return nil, fmt.Errorf("search query is required")
	}

	// Build search query with time range and index
	searchQuery := search
	if index != "" {
		searchQuery = fmt.Sprintf("index=%s %s", index, searchQuery)
	}

	// Default time range
	if earliest == "" {
		earliest = "-24h"
	}
	if latest == "" {
		latest = "now"
	}

	// Default max count
	if maxCount == "" {
		maxCount = "100"
	}

	// Default output mode
	if outputMode == "" {
		outputMode = "json"
	}

	// Create search job
	params := url.Values{}
	params.Set("search", searchQuery)
	params.Set("earliest_time", earliest)
	params.Set("latest_time", latest)
	params.Set("max_count", maxCount)
	params.Set("output_mode", outputMode)

	skill := &SplunkSkill{client: e.client}
	respBody, err := skill.doRequest(ctx, "POST", server, "/services/search/jobs", authToken, params, nil)
	if err != nil {
		return nil, err
	}

	// Parse response to get SID (Search ID)
	var jobResult map[string]interface{}
	if err := json.Unmarshal(respBody, &jobResult); err != nil {
		return nil, fmt.Errorf("failed to parse job response: %w", err)
	}

	sid, ok := jobResult["sid"].(string)
	if !ok {
		return nil, fmt.Errorf("no search ID returned")
	}

	// Wait for search to complete
	maxWait := 30 * time.Second
	startTime := time.Now()
	for {
		if time.Since(startTime) > maxWait {
			break
		}

		statusBody, err := skill.doRequest(ctx, "GET", server, fmt.Sprintf("/services/search/jobs/%s", sid), authToken, url.Values{"output_mode": []string{"json"}}, nil)
		if err != nil {
			return nil, err
		}

		var statusResult map[string]interface{}
		if err := json.Unmarshal(statusBody, &statusResult); err != nil {
			return nil, fmt.Errorf("failed to parse status: %w", err)
		}

		entry, ok := statusResult["entry"].([]interface{})
		if !ok || len(entry) == 0 {
			break
		}

		content, ok := entry[0].(map[string]interface{})["content"].(map[string]interface{})
		if !ok {
			break
		}

		dispatchState, _ := content["dispatchState"].(string)
		isDone, _ := content["isDone"].(bool)

		if isDone || dispatchState == "DONE" {
			break
		}

		time.Sleep(500 * time.Millisecond)
	}

	// Get search results
	resultsBody, err := skill.doRequest(ctx, "GET", server, fmt.Sprintf("/services/search/jobs/%s/results", sid), authToken, url.Values{"output_mode": []string{"json"}}, nil)
	if err != nil {
		return nil, err
	}

	var resultsResult map[string]interface{}
	if err := json.Unmarshal(resultsBody, &resultsResult); err != nil {
		return nil, fmt.Errorf("failed to parse results: %w", err)
	}

	// Extract results
	var results []map[string]interface{}
	if r, ok := resultsResult["results"].([]map[string]interface{}); ok {
		results = r
	}

	output := map[string]interface{}{
		"result": results,
		"sid":    sid,
		"count":  len(results),
	}

	// Add messages if present
	if messages, ok := resultsResult["messages"].([]interface{}); ok && len(messages) > 0 {
		output["messages"] = messages
	}

	return &executor.StepResult{Output: output}, nil
}

// ============================================================================
// SPLUNK SAVED SEARCH EXECUTORS
// ============================================================================

// SavedSearchGetExecutor handles splunk-saved-search
type SavedSearchGetExecutor struct {
	client *http.Client
}

func (e *SavedSearchGetExecutor) Type() string {
	return "splunk-saved-search"
}

func (e *SavedSearchGetExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	server := resolver.ResolveString(getString(config, "server"))
	authToken := resolver.ResolveString(getString(config, "authToken"))
	searchName := resolver.ResolveString(getString(config, "searchName"))

	if searchName == "" {
		return nil, fmt.Errorf("searchName is required")
	}

	skill := &SplunkSkill{client: e.client}
	respBody, err := skill.doRequest(ctx, "GET", server, fmt.Sprintf("/services/saved/searches/%s", url.PathEscape(searchName)), authToken, url.Values{"output_mode": []string{"json"}}, nil)
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Extract entry data
	var entry map[string]interface{}
	if entries, ok := result["entry"].([]interface{}); ok && len(entries) > 0 {
		if ent, ok := entries[0].(map[string]interface{}); ok {
			entry = ent
		}
	}

	output := map[string]interface{}{
		"result": entry,
	}

	if entry != nil {
		if content, ok := entry["content"].(map[string]interface{}); ok {
			if search, ok := content["search"].(string); ok {
				output["search"] = search
			}
			if cron, ok := content["cron_schedule"].(string); ok {
				output["cronSchedule"] = cron
			}
		}
	}

	return &executor.StepResult{Output: output}, nil
}

// SavedSearchListExecutor handles splunk-saved-search-list
type SavedSearchListExecutor struct {
	client *http.Client
}

func (e *SavedSearchListExecutor) Type() string {
	return "splunk-saved-search-list"
}

func (e *SavedSearchListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	server := resolver.ResolveString(getString(config, "server"))
	authToken := resolver.ResolveString(getString(config, "authToken"))
	app := resolver.ResolveString(getString(config, "app"))
	searchFilter := resolver.ResolveString(getString(config, "searchFilter"))

	params := url.Values{"output_mode": []string{"json"}}
	if app != "" {
		params.Set("app", app)
	}
	if searchFilter != "" {
		params.Set("search", searchFilter)
	}

	skill := &SplunkSkill{client: e.client}
	respBody, err := skill.doRequest(ctx, "GET", server, "/services/saved/searches", authToken, params, nil)
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	var entries []interface{}
	if e, ok := result["entry"].([]interface{}); ok {
		entries = e
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"result": entries,
			"count":  len(entries),
		},
	}, nil
}

// SavedSearchCreateExecutor handles splunk-saved-search-create
type SavedSearchCreateExecutor struct {
	client *http.Client
}

func (e *SavedSearchCreateExecutor) Type() string {
	return "splunk-saved-search-create"
}

func (e *SavedSearchCreateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	server := resolver.ResolveString(getString(config, "server"))
	authToken := resolver.ResolveString(getString(config, "authToken"))
	name := resolver.ResolveString(getString(config, "name"))
	search := resolver.ResolveString(getString(config, "search"))
	app := resolver.ResolveString(getString(config, "app"))
	cronSchedule := resolver.ResolveString(getString(config, "cronSchedule"))
	earliestTime := resolver.ResolveString(getString(config, "earliestTime"))
	latestTime := resolver.ResolveString(getString(config, "latestTime"))
	realtime := resolver.ResolveString(getString(config, "realtime"))

	if name == "" {
		return nil, fmt.Errorf("name is required")
	}
	if search == "" {
		return nil, fmt.Errorf("search query is required")
	}

	body := url.Values{}
	body.Set("name", name)
	body.Set("search", search)

	if app != "" {
		body.Set("app", app)
	}
	if cronSchedule != "" {
		body.Set("cron_schedule", cronSchedule)
	}
	if earliestTime != "" {
		body.Set("earliest_time", earliestTime)
	}
	if latestTime != "" {
		body.Set("latest_time", latestTime)
	}
	if realtime != "" {
		body.Set("realtime_schedule", realtime)
	}

	skill := &SplunkSkill{client: e.client}
	respBody, err := skill.doRequest(ctx, "POST", server, "/services/saved/searches", authToken, nil, body)
	if err != nil {
		return nil, err
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"result":  string(respBody),
			"name":    name,
			"success": true,
		},
	}, nil
}

// SavedSearchUpdateExecutor handles splunk-saved-search-update
type SavedSearchUpdateExecutor struct {
	client *http.Client
}

func (e *SavedSearchUpdateExecutor) Type() string {
	return "splunk-saved-search-update"
}

func (e *SavedSearchUpdateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	server := resolver.ResolveString(getString(config, "server"))
	authToken := resolver.ResolveString(getString(config, "authToken"))
	searchName := resolver.ResolveString(getString(config, "searchName"))
	app := resolver.ResolveString(getString(config, "app"))

	if searchName == "" {
		return nil, fmt.Errorf("searchName is required")
	}

	body := url.Values{}

	if search := resolver.ResolveString(getString(config, "search")); search != "" {
		body.Set("search", search)
	}
	if cronSchedule := resolver.ResolveString(getString(config, "cronSchedule")); cronSchedule != "" {
		body.Set("cron_schedule", cronSchedule)
	}
	if earliestTime := resolver.ResolveString(getString(config, "earliestTime")); earliestTime != "" {
		body.Set("earliest_time", earliestTime)
	}
	if latestTime := resolver.ResolveString(getString(config, "latestTime")); latestTime != "" {
		body.Set("latest_time", latestTime)
	}
	if realtime := resolver.ResolveString(getString(config, "realtime")); realtime != "" {
		body.Set("realtime_schedule", realtime)
	}
	if description := resolver.ResolveString(getString(config, "description")); description != "" {
		body.Set("description", description)
	}

	if len(body) == 0 {
		return nil, fmt.Errorf("no fields to update")
	}

	path := fmt.Sprintf("/services/saved/searches/%s", url.PathEscape(searchName))
	if app != "" {
		path += "?app=" + url.QueryEscape(app)
	}

	skill := &SplunkSkill{client: e.client}
	respBody, err := skill.doRequest(ctx, "POST", server, path, authToken, nil, body)
	if err != nil {
		return nil, err
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"result":  string(respBody),
			"success": true,
		},
	}, nil
}

// SavedSearchDeleteExecutor handles splunk-saved-search-delete
type SavedSearchDeleteExecutor struct {
	client *http.Client
}

func (e *SavedSearchDeleteExecutor) Type() string {
	return "splunk-saved-search-delete"
}

func (e *SavedSearchDeleteExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	server := resolver.ResolveString(getString(config, "server"))
	authToken := resolver.ResolveString(getString(config, "authToken"))
	searchName := resolver.ResolveString(getString(config, "searchName"))

	if searchName == "" {
		return nil, fmt.Errorf("searchName is required")
	}

	skill := &SplunkSkill{client: e.client}
	_, err := skill.doRequest(ctx, "DELETE", server, fmt.Sprintf("/services/saved/searches/%s", url.PathEscape(searchName)), authToken, nil, nil)
	if err != nil {
		return nil, err
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success": true,
		},
	}, nil
}

// SavedSearchRunExecutor handles splunk-saved-search-run
type SavedSearchRunExecutor struct {
	client *http.Client
}

func (e *SavedSearchRunExecutor) Type() string {
	return "splunk-saved-search-run"
}

func (e *SavedSearchRunExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	server := resolver.ResolveString(getString(config, "server"))
	authToken := resolver.ResolveString(getString(config, "authToken"))
	searchName := resolver.ResolveString(getString(config, "searchName"))
	app := resolver.ResolveString(getString(config, "app"))
	earliest := resolver.ResolveString(getString(config, "earliest"))
	latest := resolver.ResolveString(getString(config, "latest"))

	if searchName == "" {
		return nil, fmt.Errorf("searchName is required")
	}

	params := url.Values{"output_mode": []string{"json"}}
	if app != "" {
		params.Set("app", app)
	}
	if earliest != "" {
		params.Set("earliest_time", earliest)
	}
	if latest != "" {
		params.Set("latest_time", latest)
	}

	path := fmt.Sprintf("/services/saved/searches/%s/dispatch", url.PathEscape(searchName))

	skill := &SplunkSkill{client: e.client}
	respBody, err := skill.doRequest(ctx, "POST", server, path, authToken, params, nil)
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	sid, _ := result["sid"].(string)

	return &executor.StepResult{
		Output: map[string]interface{}{
			"result": string(respBody),
			"sid":    sid,
		},
	}, nil
}

// ============================================================================
// SPLUNK SEARCH JOB EXECUTORS
// ============================================================================

// SearchJobCreateExecutor handles splunk-search-job-create
type SearchJobCreateExecutor struct {
	client *http.Client
}

func (e *SearchJobCreateExecutor) Type() string {
	return "splunk-search-job-create"
}

func (e *SearchJobCreateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	server := resolver.ResolveString(getString(config, "server"))
	authToken := resolver.ResolveString(getString(config, "authToken"))
	search := resolver.ResolveString(getString(config, "search"))
	earliest := resolver.ResolveString(getString(config, "earliest"))
	latest := resolver.ResolveString(getString(config, "latest"))
	index := resolver.ResolveString(getString(config, "index"))
	maxCount := resolver.ResolveString(getString(config, "maxCount"))
	execMode := resolver.ResolveString(getString(config, "execMode"))
	app := resolver.ResolveString(getString(config, "app"))

	if search == "" {
		return nil, fmt.Errorf("search query is required")
	}

	searchQuery := search
	if index != "" {
		searchQuery = fmt.Sprintf("index=%s %s", index, searchQuery)
	}

	if earliest == "" {
		earliest = "-24h"
	}
	if latest == "" {
		latest = "now"
	}

	body := url.Values{}
	body.Set("search", searchQuery)
	body.Set("earliest_time", earliest)
	body.Set("latest_time", latest)

	if maxCount != "" {
		body.Set("max_count", maxCount)
	}
	if execMode != "" {
		body.Set("exec_mode", execMode)
	}
	if app != "" {
		body.Set("app", app)
	}

	skill := &SplunkSkill{client: e.client}
	respBody, err := skill.doRequest(ctx, "POST", server, "/services/search/jobs", authToken, nil, body)
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	sid, _ := result["sid"].(string)

	return &executor.StepResult{
		Output: map[string]interface{}{
			"result": string(respBody),
			"sid":    sid,
		},
	}, nil
}

// SearchJobStatusExecutor handles splunk-search-job-status
type SearchJobStatusExecutor struct {
	client *http.Client
}

func (e *SearchJobStatusExecutor) Type() string {
	return "splunk-search-job-status"
}

func (e *SearchJobStatusExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	server := resolver.ResolveString(getString(config, "server"))
	authToken := resolver.ResolveString(getString(config, "authToken"))
	sid := resolver.ResolveString(getString(config, "sid"))

	if sid == "" {
		return nil, fmt.Errorf("sid (search ID) is required")
	}

	skill := &SplunkSkill{client: e.client}
	respBody, err := skill.doRequest(ctx, "GET", server, fmt.Sprintf("/services/search/jobs/%s", sid), authToken, url.Values{"output_mode": []string{"json"}}, nil)
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	var content map[string]interface{}
	if entries, ok := result["entry"].([]interface{}); ok && len(entries) > 0 {
		if ent, ok := entries[0].(map[string]interface{}); ok {
			if c, ok := ent["content"].(map[string]interface{}); ok {
				content = c
			}
		}
	}

	output := map[string]interface{}{
		"result": string(respBody),
	}

	if content != nil {
		if dispatchState, ok := content["dispatchState"].(string); ok {
			output["dispatchState"] = dispatchState
		}
		if isDone, ok := content["isDone"].(bool); ok {
			output["isDone"] = isDone
		}
		if isFailed, ok := content["isFailed"].(bool); ok {
			output["isFailed"] = isFailed
		}
		if runDuration, ok := content["runDuration"].(float64); ok {
			output["runDuration"] = runDuration
		}
		if eventCount, ok := content["eventCount"].(float64); ok {
			output["eventCount"] = int(eventCount)
		}
		if resultCount, ok := content["resultCount"].(float64); ok {
			output["resultCount"] = int(resultCount)
		}
	}

	return &executor.StepResult{Output: output}, nil
}

// SearchJobResultsExecutor handles splunk-search-job-results
type SearchJobResultsExecutor struct {
	client *http.Client
}

func (e *SearchJobResultsExecutor) Type() string {
	return "splunk-search-job-results"
}

func (e *SearchJobResultsExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	server := resolver.ResolveString(getString(config, "server"))
	authToken := resolver.ResolveString(getString(config, "authToken"))
	sid := resolver.ResolveString(getString(config, "sid"))
	offset := resolver.ResolveString(getString(config, "offset"))
	count := resolver.ResolveString(getString(config, "count"))
	outputMode := resolver.ResolveString(getString(config, "outputMode"))

	if sid == "" {
		return nil, fmt.Errorf("sid (search ID) is required")
	}

	params := url.Values{"output_mode": []string{"json"}}
	if outputMode != "" {
		params.Set("output_mode", outputMode)
	}
	if offset != "" {
		params.Set("offset", offset)
	}
	if count != "" {
		params.Set("count", count)
	}

	skill := &SplunkSkill{client: e.client}
	respBody, err := skill.doRequest(ctx, "GET", server, fmt.Sprintf("/services/search/jobs/%s/results", sid), authToken, params, nil)
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	var results []map[string]interface{}
	if r, ok := result["results"].([]map[string]interface{}); ok {
		results = r
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"result": results,
			"count":  len(results),
		},
	}, nil
}

// SearchJobCancelExecutor handles splunk-search-job-cancel
type SearchJobCancelExecutor struct {
	client *http.Client
}

func (e *SearchJobCancelExecutor) Type() string {
	return "splunk-search-job-cancel"
}

func (e *SearchJobCancelExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	server := resolver.ResolveString(getString(config, "server"))
	authToken := resolver.ResolveString(getString(config, "authToken"))
	sid := resolver.ResolveString(getString(config, "sid"))

	if sid == "" {
		return nil, fmt.Errorf("sid (search ID) is required")
	}

	skill := &SplunkSkill{client: e.client}
	_, err := skill.doRequest(ctx, "DELETE", server, fmt.Sprintf("/services/search/jobs/%s", sid), authToken, nil, nil)
	if err != nil {
		return nil, err
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success": true,
			"sid":     sid,
		},
	}, nil
}

// SearchJobWaitExecutor handles splunk-search-job-wait
type SearchJobWaitExecutor struct {
	client *http.Client
}

func (e *SearchJobWaitExecutor) Type() string {
	return "splunk-search-job-wait"
}

func (e *SearchJobWaitExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	server := resolver.ResolveString(getString(config, "server"))
	authToken := resolver.ResolveString(getString(config, "authToken"))
	sid := resolver.ResolveString(getString(config, "sid"))
	timeout := resolver.ResolveString(getString(config, "timeout"))

	if sid == "" {
		return nil, fmt.Errorf("sid (search ID) is required")
	}

	maxWait := 60 * time.Second
	if timeout != "" {
		if t, err := time.ParseDuration(timeout); err == nil {
			maxWait = t
		}
	}

	startTime := time.Now()
	for {
		if time.Since(startTime) > maxWait {
			return nil, fmt.Errorf("timeout waiting for search job %s to complete", sid)
		}

		skill := &SplunkSkill{client: e.client}
		respBody, err := skill.doRequest(ctx, "GET", server, fmt.Sprintf("/services/search/jobs/%s", sid), authToken, url.Values{"output_mode": []string{"json"}}, nil)
		if err != nil {
			return nil, err
		}

		var result map[string]interface{}
		if err := json.Unmarshal(respBody, &result); err != nil {
			return nil, fmt.Errorf("failed to parse status: %w", err)
		}

		var content map[string]interface{}
		if entries, ok := result["entry"].([]interface{}); ok && len(entries) > 0 {
			if ent, ok := entries[0].(map[string]interface{}); ok {
				if c, ok := ent["content"].(map[string]interface{}); ok {
					content = c
				}
			}
		}

		if content != nil {
			isDone, _ := content["isDone"].(bool)
			isFailed, _ := content["isFailed"].(bool)
			dispatchState, _ := content["dispatchState"].(string)

			if isDone || dispatchState == "DONE" {
				output := map[string]interface{}{
					"result":        string(respBody),
					"isDone":        true,
					"dispatchState": dispatchState,
				}
				if runDuration, ok := content["runDuration"].(float64); ok {
					output["runDuration"] = runDuration
				}
				return &executor.StepResult{Output: output}, nil
			}

			if isFailed {
				var messages string
				if msgs, ok := content["messages"].([]interface{}); ok && len(msgs) > 0 {
					messagesJSON, _ := json.Marshal(msgs)
					messages = string(messagesJSON)
				}
				return nil, fmt.Errorf("search job failed: %s", messages)
			}
		}

		time.Sleep(500 * time.Millisecond)
	}
}

// ============================================================================
// SPLUNK INDEX EXECUTORS
// ============================================================================

// IndexListExecutor handles splunk-index-list
type IndexListExecutor struct {
	client *http.Client
}

func (e *IndexListExecutor) Type() string {
	return "splunk-index-list"
}

func (e *IndexListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	server := resolver.ResolveString(getString(config, "server"))
	authToken := resolver.ResolveString(getString(config, "authToken"))
	app := resolver.ResolveString(getString(config, "app"))

	params := url.Values{"output_mode": []string{"json"}}
	if app != "" {
		params.Set("app", app)
	}

	skill := &SplunkSkill{client: e.client}
	respBody, err := skill.doRequest(ctx, "GET", server, "/services/data/indexes", authToken, params, nil)
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	var entries []interface{}
	if e, ok := result["entry"].([]interface{}); ok {
		entries = e
	}

	var indexNames []string
	for _, entry := range entries {
		if ent, ok := entry.(map[string]interface{}); ok {
			if name, ok := ent["name"].(string); ok {
				indexNames = append(indexNames, name)
			}
		}
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"result": entries,
			"count":  len(entries),
			"names":  indexNames,
		},
	}, nil
}

// IndexGetExecutor handles splunk-index-get
type IndexGetExecutor struct {
	client *http.Client
}

func (e *IndexGetExecutor) Type() string {
	return "splunk-index-get"
}

func (e *IndexGetExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	server := resolver.ResolveString(getString(config, "server"))
	authToken := resolver.ResolveString(getString(config, "authToken"))
	indexName := resolver.ResolveString(getString(config, "indexName"))

	if indexName == "" {
		return nil, fmt.Errorf("indexName is required")
	}

	skill := &SplunkSkill{client: e.client}
	respBody, err := skill.doRequest(ctx, "GET", server, fmt.Sprintf("/services/data/indexes/%s", url.PathEscape(indexName)), authToken, url.Values{"output_mode": []string{"json"}}, nil)
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	var entry map[string]interface{}
	if entries, ok := result["entry"].([]interface{}); ok && len(entries) > 0 {
		if ent, ok := entries[0].(map[string]interface{}); ok {
			entry = ent
		}
	}

	output := map[string]interface{}{
		"result": entry,
	}

	if entry != nil {
		if content, ok := entry["content"].(map[string]interface{}); ok {
			if totalEventCount, ok := content["totalEventCount"].(float64); ok {
				output["totalEventCount"] = int(totalEventCount)
			}
			if isInternal, ok := content["isInternal"].(bool); ok {
				output["isInternal"] = isInternal
			}
			if isReady, ok := content["isReady"].(bool); ok {
				output["isReady"] = isReady
			}
		}
	}

	return &executor.StepResult{Output: output}, nil
}

// IndexCreateExecutor handles splunk-index-create
type IndexCreateExecutor struct {
	client *http.Client
}

func (e *IndexCreateExecutor) Type() string {
	return "splunk-index-create"
}

func (e *IndexCreateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	server := resolver.ResolveString(getString(config, "server"))
	authToken := resolver.ResolveString(getString(config, "authToken"))
	indexName := resolver.ResolveString(getString(config, "indexName"))
	dataType := resolver.ResolveString(getString(config, "dataType"))
	homePath := resolver.ResolveString(getString(config, "homePath"))
	coldPath := resolver.ResolveString(getString(config, "coldPath"))
	thawedPath := resolver.ResolveString(getString(config, "thawedPath"))
	maxTotalDataSizeMB := resolver.ResolveString(getString(config, "maxTotalDataSizeMB"))

	if indexName == "" {
		return nil, fmt.Errorf("indexName is required")
	}

	body := url.Values{}
	body.Set("name", indexName)

	if dataType != "" {
		body.Set("datatype", dataType)
	}
	if homePath != "" {
		body.Set("homePath", homePath)
	}
	if coldPath != "" {
		body.Set("coldPath", coldPath)
	}
	if thawedPath != "" {
		body.Set("thawedPath", thawedPath)
	}
	if maxTotalDataSizeMB != "" {
		body.Set("maxTotalDataSizeMB", maxTotalDataSizeMB)
	}

	skill := &SplunkSkill{client: e.client}
	respBody, err := skill.doRequest(ctx, "POST", server, "/services/data/indexes", authToken, nil, body)
	if err != nil {
		return nil, err
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"result":  string(respBody),
			"name":    indexName,
			"success": true,
		},
	}, nil
}

// IndexUpdateExecutor handles splunk-index-update
type IndexUpdateExecutor struct {
	client *http.Client
}

func (e *IndexUpdateExecutor) Type() string {
	return "splunk-index-update"
}

func (e *IndexUpdateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	server := resolver.ResolveString(getString(config, "server"))
	authToken := resolver.ResolveString(getString(config, "authToken"))
	indexName := resolver.ResolveString(getString(config, "indexName"))

	if indexName == "" {
		return nil, fmt.Errorf("indexName is required")
	}

	body := url.Values{}

	if homePath := resolver.ResolveString(getString(config, "homePath")); homePath != "" {
		body.Set("homePath", homePath)
	}
	if coldPath := resolver.ResolveString(getString(config, "coldPath")); coldPath != "" {
		body.Set("coldPath", coldPath)
	}
	if thawedPath := resolver.ResolveString(getString(config, "thawedPath")); thawedPath != "" {
		body.Set("thawedPath", thawedPath)
	}
	if maxTotalDataSizeMB := resolver.ResolveString(getString(config, "maxTotalDataSizeMB")); maxTotalDataSizeMB != "" {
		body.Set("maxTotalDataSizeMB", maxTotalDataSizeMB)
	}
	if frozenTimePeriodInSecs := resolver.ResolveString(getString(config, "frozenTimePeriodInSecs")); frozenTimePeriodInSecs != "" {
		body.Set("frozenTimePeriodInSecs", frozenTimePeriodInSecs)
	}

	if len(body) == 0 {
		return nil, fmt.Errorf("no fields to update")
	}

	skill := &SplunkSkill{client: e.client}
	respBody, err := skill.doRequest(ctx, "POST", server, fmt.Sprintf("/services/data/indexes/%s", url.PathEscape(indexName)), authToken, nil, body)
	if err != nil {
		return nil, err
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"result":  string(respBody),
			"success": true,
		},
	}, nil
}

// IndexDeleteExecutor handles splunk-index-delete
type IndexDeleteExecutor struct {
	client *http.Client
}

func (e *IndexDeleteExecutor) Type() string {
	return "splunk-index-delete"
}

func (e *IndexDeleteExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	server := resolver.ResolveString(getString(config, "server"))
	authToken := resolver.ResolveString(getString(config, "authToken"))
	indexName := resolver.ResolveString(getString(config, "indexName"))

	if indexName == "" {
		return nil, fmt.Errorf("indexName is required")
	}

	skill := &SplunkSkill{client: e.client}
	_, err := skill.doRequest(ctx, "DELETE", server, fmt.Sprintf("/services/data/indexes/%s", url.PathEscape(indexName)), authToken, nil, nil)
	if err != nil {
		return nil, err
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success": true,
		},
	}, nil
}

// ============================================================================
// SPLUNK ALERT EXECUTORS
// ============================================================================

// AlertCreateExecutor handles splunk-alert-create
type AlertCreateExecutor struct {
	client *http.Client
}

func (e *AlertCreateExecutor) Type() string {
	return "splunk-alert-create"
}

func (e *AlertCreateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	server := resolver.ResolveString(getString(config, "server"))
	authToken := resolver.ResolveString(getString(config, "authToken"))
	name := resolver.ResolveString(getString(config, "name"))
	search := resolver.ResolveString(getString(config, "search"))
	app := resolver.ResolveString(getString(config, "app"))
	alertType := resolver.ResolveString(getString(config, "alertType"))
	alertCondition := resolver.ResolveString(getString(config, "alertCondition"))
	alertThreshold := resolver.ResolveString(getString(config, "alertThreshold"))
	alertCompare := resolver.ResolveString(getString(config, "alertCompare"))
	alertSeverity := resolver.ResolveString(getString(config, "alertSeverity"))
	alertTrack := resolver.ResolveString(getString(config, "alertTrack"))
	actionEmail := resolver.ResolveString(getString(config, "actionEmail"))
	actionEmailTo := resolver.ResolveString(getString(config, "actionEmailTo"))
	actionEmailSubject := resolver.ResolveString(getString(config, "actionEmailSubject"))
	actionEmailMessage := resolver.ResolveString(getString(config, "actionEmailMessage"))
	actionWebhook := resolver.ResolveString(getString(config, "actionWebhook"))
	actionWebhookURL := resolver.ResolveString(getString(config, "actionWebhookURL"))
	cronSchedule := resolver.ResolveString(getString(config, "cronSchedule"))
	earliestTime := resolver.ResolveString(getString(config, "earliestTime"))
	latestTime := resolver.ResolveString(getString(config, "latestTime"))

	if name == "" {
		return nil, fmt.Errorf("name is required")
	}
	if search == "" {
		return nil, fmt.Errorf("search query is required")
	}

	body := url.Values{}
	body.Set("name", name)
	body.Set("search", search)
	body.Set("alert_type", alertType)

	if alertCondition != "" {
		body.Set("alert_condition", alertCondition)
	}
	if alertThreshold != "" {
		body.Set("alert_threshold", alertThreshold)
	}
	if alertCompare != "" {
		body.Set("alert_comparator", alertCompare)
	}
	if alertSeverity != "" {
		body.Set("alert_severity", alertSeverity)
	}
	if alertTrack != "" {
		body.Set("alert_track", alertTrack)
	}

	if actionEmail == "true" {
		body.Set("action.email", "1")
		if actionEmailTo != "" {
			body.Set("action.email.to", actionEmailTo)
		}
		if actionEmailSubject != "" {
			body.Set("action.email.subject", actionEmailSubject)
		}
		if actionEmailMessage != "" {
			body.Set("action.email.message", actionEmailMessage)
		}
	}

	if actionWebhook == "true" {
		body.Set("action.webhook", "1")
		if actionWebhookURL != "" {
			body.Set("action.webhook.webhook_url", actionWebhookURL)
		}
	}

	if cronSchedule != "" {
		body.Set("cron_schedule", cronSchedule)
	}
	if earliestTime != "" {
		body.Set("earliest_time", earliestTime)
	}
	if latestTime != "" {
		body.Set("latest_time", latestTime)
	}
	if app != "" {
		body.Set("app", app)
	}

	skill := &SplunkSkill{client: e.client}
	respBody, err := skill.doRequest(ctx, "POST", server, "/services/saved/searches", authToken, nil, body)
	if err != nil {
		return nil, err
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"result":  string(respBody),
			"name":    name,
			"success": true,
		},
	}, nil
}

// AlertListExecutor handles splunk-alert-list
type AlertListExecutor struct {
	client *http.Client
}

func (e *AlertListExecutor) Type() string {
	return "splunk-alert-list"
}

func (e *AlertListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	server := resolver.ResolveString(getString(config, "server"))
	authToken := resolver.ResolveString(getString(config, "authToken"))
	app := resolver.ResolveString(getString(config, "app"))

	params := url.Values{"output_mode": []string{"json"}}
	if app != "" {
		params.Set("app", app)
	}

	skill := &SplunkSkill{client: e.client}
	respBody, err := skill.doRequest(ctx, "GET", server, "/services/saved/searches", authToken, params, nil)
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	var entries []interface{}
	if e, ok := result["entry"].([]interface{}); ok {
		for _, entry := range e {
			if entryMap, ok := entry.(map[string]interface{}); ok {
				if content, ok := entryMap["content"].(map[string]interface{}); ok {
					if _, hasAlertType := content["alert_type"]; hasAlertType {
						entries = append(entries, entry)
					} else if _, hasEmail := content["action.email"]; hasEmail {
						entries = append(entries, entry)
					}
				}
			}
		}
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"result": entries,
			"count":  len(entries),
		},
	}, nil
}

// AlertGetExecutor handles splunk-alert-get
type AlertGetExecutor struct {
	client *http.Client
}

func (e *AlertGetExecutor) Type() string {
	return "splunk-alert-get"
}

func (e *AlertGetExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	server := resolver.ResolveString(getString(config, "server"))
	authToken := resolver.ResolveString(getString(config, "authToken"))
	alertName := resolver.ResolveString(getString(config, "alertName"))

	if alertName == "" {
		return nil, fmt.Errorf("alertName is required")
	}

	skill := &SplunkSkill{client: e.client}
	respBody, err := skill.doRequest(ctx, "GET", server, fmt.Sprintf("/services/saved/searches/%s", url.PathEscape(alertName)), authToken, url.Values{"output_mode": []string{"json"}}, nil)
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	var entry map[string]interface{}
	if entries, ok := result["entry"].([]interface{}); ok && len(entries) > 0 {
		if ent, ok := entries[0].(map[string]interface{}); ok {
			entry = ent
		}
	}

	output := map[string]interface{}{
		"result": entry,
	}

	if entry != nil {
		if content, ok := entry["content"].(map[string]interface{}); ok {
			if search, ok := content["search"].(string); ok {
				output["search"] = search
			}
			if alertType, ok := content["alert_type"].(string); ok {
				output["alertType"] = alertType
			}
			if alertCondition, ok := content["alert_condition"].(string); ok {
				output["alertCondition"] = alertCondition
			}
		}
	}

	return &executor.StepResult{Output: output}, nil
}

// AlertUpdateExecutor handles splunk-alert-update
type AlertUpdateExecutor struct {
	client *http.Client
}

func (e *AlertUpdateExecutor) Type() string {
	return "splunk-alert-update"
}

func (e *AlertUpdateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	server := resolver.ResolveString(getString(config, "server"))
	authToken := resolver.ResolveString(getString(config, "authToken"))
	alertName := resolver.ResolveString(getString(config, "alertName"))
	app := resolver.ResolveString(getString(config, "app"))

	if alertName == "" {
		return nil, fmt.Errorf("alertName is required")
	}

	body := url.Values{}

	if search := resolver.ResolveString(getString(config, "search")); search != "" {
		body.Set("search", search)
	}
	if alertType := resolver.ResolveString(getString(config, "alertType")); alertType != "" {
		body.Set("alert_type", alertType)
	}
	if alertCondition := resolver.ResolveString(getString(config, "alertCondition")); alertCondition != "" {
		body.Set("alert_condition", alertCondition)
	}
	if alertThreshold := resolver.ResolveString(getString(config, "alertThreshold")); alertThreshold != "" {
		body.Set("alert_threshold", alertThreshold)
	}
	if alertSeverity := resolver.ResolveString(getString(config, "alertSeverity")); alertSeverity != "" {
		body.Set("alert_severity", alertSeverity)
	}
	if actionEmail := resolver.ResolveString(getString(config, "actionEmail")); actionEmail != "" {
		body.Set("action.email", actionEmail)
	}
	if actionEmailTo := resolver.ResolveString(getString(config, "actionEmailTo")); actionEmailTo != "" {
		body.Set("action.email.to", actionEmailTo)
	}
	if cronSchedule := resolver.ResolveString(getString(config, "cronSchedule")); cronSchedule != "" {
		body.Set("cron_schedule", cronSchedule)
	}

	if len(body) == 0 {
		return nil, fmt.Errorf("no fields to update")
	}

	path := fmt.Sprintf("/services/saved/searches/%s", url.PathEscape(alertName))
	if app != "" {
		path += "?app=" + url.QueryEscape(app)
	}

	skill := &SplunkSkill{client: e.client}
	respBody, err := skill.doRequest(ctx, "POST", server, path, authToken, nil, body)
	if err != nil {
		return nil, err
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"result":  string(respBody),
			"success": true,
		},
	}, nil
}

// AlertDeleteExecutor handles splunk-alert-delete
type AlertDeleteExecutor struct {
	client *http.Client
}

func (e *AlertDeleteExecutor) Type() string {
	return "splunk-alert-delete"
}

func (e *AlertDeleteExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	server := resolver.ResolveString(getString(config, "server"))
	authToken := resolver.ResolveString(getString(config, "authToken"))
	alertName := resolver.ResolveString(getString(config, "alertName"))

	if alertName == "" {
		return nil, fmt.Errorf("alertName is required")
	}

	skill := &SplunkSkill{client: e.client}
	_, err := skill.doRequest(ctx, "DELETE", server, fmt.Sprintf("/services/saved/searches/%s", url.PathEscape(alertName)), authToken, nil, nil)
	if err != nil {
		return nil, err
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success": true,
		},
	}, nil
}

// AlertHistoryExecutor handles splunk-alert-history
type AlertHistoryExecutor struct {
	client *http.Client
}

func (e *AlertHistoryExecutor) Type() string {
	return "splunk-alert-history"
}

func (e *AlertHistoryExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	server := resolver.ResolveString(getString(config, "server"))
	authToken := resolver.ResolveString(getString(config, "authToken"))
	alertName := resolver.ResolveString(getString(config, "alertName"))
	count := resolver.ResolveString(getString(config, "count"))

	if alertName == "" {
		return nil, fmt.Errorf("alertName is required")
	}

	params := url.Values{"output_mode": []string{"json"}}
	if count != "" {
		params.Set("count", count)
	}

	skill := &SplunkSkill{client: e.client}
	respBody, err := skill.doRequest(ctx, "GET", server, fmt.Sprintf("/services/alerts/%s/history", url.PathEscape(alertName)), authToken, params, nil)
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"result": result,
		},
	}, nil
}

// ============================================================================
// SPLUNK DASHBOARD EXECUTORS
// ============================================================================

// DashboardListExecutor handles splunk-dashboard-list
type DashboardListExecutor struct {
	client *http.Client
}

func (e *DashboardListExecutor) Type() string {
	return "splunk-dashboard-list"
}

func (e *DashboardListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	server := resolver.ResolveString(getString(config, "server"))
	authToken := resolver.ResolveString(getString(config, "authToken"))
	app := resolver.ResolveString(getString(config, "app"))

	params := url.Values{"output_mode": []string{"json"}}
	if app != "" {
		params.Set("app", app)
	}

	skill := &SplunkSkill{client: e.client}
	respBody, err := skill.doRequest(ctx, "GET", server, "/services/data/views", authToken, params, nil)
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	var entries []interface{}
	if e, ok := result["entry"].([]interface{}); ok {
		entries = e
	}

	var dashboardNames []string
	for _, entry := range entries {
		if ent, ok := entry.(map[string]interface{}); ok {
			if name, ok := ent["name"].(string); ok {
				dashboardNames = append(dashboardNames, name)
			}
		}
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"result": entries,
			"count":  len(entries),
			"names":  dashboardNames,
		},
	}, nil
}

// DashboardGetExecutor handles splunk-dashboard-get
type DashboardGetExecutor struct {
	client *http.Client
}

func (e *DashboardGetExecutor) Type() string {
	return "splunk-dashboard-get"
}

func (e *DashboardGetExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	server := resolver.ResolveString(getString(config, "server"))
	authToken := resolver.ResolveString(getString(config, "authToken"))
	dashboardName := resolver.ResolveString(getString(config, "dashboardName"))
	app := resolver.ResolveString(getString(config, "app"))

	if dashboardName == "" {
		return nil, fmt.Errorf("dashboardName is required")
	}

	params := url.Values{"output_mode": []string{"json"}}
	if app != "" {
		params.Set("app", app)
	}

	skill := &SplunkSkill{client: e.client}
	respBody, err := skill.doRequest(ctx, "GET", server, fmt.Sprintf("/services/data/views/%s", url.PathEscape(dashboardName)), authToken, params, nil)
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	var entry map[string]interface{}
	if entries, ok := result["entry"].([]interface{}); ok && len(entries) > 0 {
		if ent, ok := entries[0].(map[string]interface{}); ok {
			entry = ent
		}
	}

	output := map[string]interface{}{
		"result": entry,
	}

	if entry != nil {
		if content, ok := entry["content"].(map[string]interface{}); ok {
			if eai, ok := content["eai:data"].(string); ok {
				output["xml"] = eai
			}
			if appName, ok := content["eai:appName"].(string); ok {
				output["appName"] = appName
			}
		}
	}

	return &executor.StepResult{Output: output}, nil
}

// DashboardCreateExecutor handles splunk-dashboard-create
type DashboardCreateExecutor struct {
	client *http.Client
}

func (e *DashboardCreateExecutor) Type() string {
	return "splunk-dashboard-create"
}

func (e *DashboardCreateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	server := resolver.ResolveString(getString(config, "server"))
	authToken := resolver.ResolveString(getString(config, "authToken"))
	name := resolver.ResolveString(getString(config, "name"))
	app := resolver.ResolveString(getString(config, "app"))
	dashboardXML := resolver.ResolveString(getString(config, "dashboardXML"))
	label := resolver.ResolveString(getString(config, "label"))
	description := resolver.ResolveString(getString(config, "description"))

	if name == "" {
		return nil, fmt.Errorf("name is required")
	}
	if dashboardXML == "" {
		return nil, fmt.Errorf("dashboardXML is required")
	}

	body := url.Values{}
	body.Set("name", name)
	body.Set("eai:data", dashboardXML)

	if app != "" {
		body.Set("app", app)
	}
	if label != "" {
		body.Set("label", label)
	}
	if description != "" {
		body.Set("description", description)
	}

	skill := &SplunkSkill{client: e.client}
	respBody, err := skill.doRequest(ctx, "POST", server, "/services/data/views", authToken, nil, body)
	if err != nil {
		return nil, err
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"result":  string(respBody),
			"name":    name,
			"success": true,
		},
	}, nil
}

// DashboardUpdateExecutor handles splunk-dashboard-update
type DashboardUpdateExecutor struct {
	client *http.Client
}

func (e *DashboardUpdateExecutor) Type() string {
	return "splunk-dashboard-update"
}

func (e *DashboardUpdateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	server := resolver.ResolveString(getString(config, "server"))
	authToken := resolver.ResolveString(getString(config, "authToken"))
	dashboardName := resolver.ResolveString(getString(config, "dashboardName"))
	app := resolver.ResolveString(getString(config, "app"))
	dashboardXML := resolver.ResolveString(getString(config, "dashboardXML"))
	label := resolver.ResolveString(getString(config, "label"))
	description := resolver.ResolveString(getString(config, "description"))

	if dashboardName == "" {
		return nil, fmt.Errorf("dashboardName is required")
	}

	body := url.Values{}

	if dashboardXML != "" {
		body.Set("eai:data", dashboardXML)
	}
	if label != "" {
		body.Set("label", label)
	}
	if description != "" {
		body.Set("description", description)
	}

	if len(body) == 0 {
		return nil, fmt.Errorf("no fields to update")
	}

	path := fmt.Sprintf("/services/data/views/%s", url.PathEscape(dashboardName))
	if app != "" {
		path += "?app=" + url.QueryEscape(app)
	}

	skill := &SplunkSkill{client: e.client}
	respBody, err := skill.doRequest(ctx, "POST", server, path, authToken, nil, body)
	if err != nil {
		return nil, err
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"result":  string(respBody),
			"success": true,
		},
	}, nil
}

// DashboardDeleteExecutor handles splunk-dashboard-delete
type DashboardDeleteExecutor struct {
	client *http.Client
}

func (e *DashboardDeleteExecutor) Type() string {
	return "splunk-dashboard-delete"
}

func (e *DashboardDeleteExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	server := resolver.ResolveString(getString(config, "server"))
	authToken := resolver.ResolveString(getString(config, "authToken"))
	dashboardName := resolver.ResolveString(getString(config, "dashboardName"))
	app := resolver.ResolveString(getString(config, "app"))

	if dashboardName == "" {
		return nil, fmt.Errorf("dashboardName is required")
	}

	path := fmt.Sprintf("/services/data/views/%s", url.PathEscape(dashboardName))
	if app != "" {
		path += "?app=" + url.QueryEscape(app)
	}

	skill := &SplunkSkill{client: e.client}
	_, err := skill.doRequest(ctx, "DELETE", server, path, authToken, nil, nil)
	if err != nil {
		return nil, err
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success": true,
		},
	}, nil
}

// ============================================================================
// SPLUNK EVENT TYPE EXECUTORS
// ============================================================================

// EventTypeListExecutor handles splunk-event-type-list
type EventTypeListExecutor struct {
	client *http.Client
}

func (e *EventTypeListExecutor) Type() string {
	return "splunk-event-type-list"
}

func (e *EventTypeListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	server := resolver.ResolveString(getString(config, "server"))
	authToken := resolver.ResolveString(getString(config, "authToken"))
	app := resolver.ResolveString(getString(config, "app"))

	params := url.Values{"output_mode": []string{"json"}}
	if app != "" {
		params.Set("app", app)
	}

	skill := &SplunkSkill{client: e.client}
	respBody, err := skill.doRequest(ctx, "GET", server, "/services/data/eventtypes", authToken, params, nil)
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	var entries []interface{}
	if e, ok := result["entry"].([]interface{}); ok {
		entries = e
	}

	var eventTypeNames []string
	for _, entry := range entries {
		if ent, ok := entry.(map[string]interface{}); ok {
			if name, ok := ent["name"].(string); ok {
				eventTypeNames = append(eventTypeNames, name)
			}
		}
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"result": entries,
			"count":  len(entries),
			"names":  eventTypeNames,
		},
	}, nil
}

// EventTypeGetExecutor handles splunk-event-type-get
type EventTypeGetExecutor struct {
	client *http.Client
}

func (e *EventTypeGetExecutor) Type() string {
	return "splunk-event-type-get"
}

func (e *EventTypeGetExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	server := resolver.ResolveString(getString(config, "server"))
	authToken := resolver.ResolveString(getString(config, "authToken"))
	eventTypeName := resolver.ResolveString(getString(config, "eventTypeName"))

	if eventTypeName == "" {
		return nil, fmt.Errorf("eventTypeName is required")
	}

	skill := &SplunkSkill{client: e.client}
	respBody, err := skill.doRequest(ctx, "GET", server, fmt.Sprintf("/services/data/eventtypes/%s", url.PathEscape(eventTypeName)), authToken, url.Values{"output_mode": []string{"json"}}, nil)
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	var entry map[string]interface{}
	if entries, ok := result["entry"].([]interface{}); ok && len(entries) > 0 {
		if ent, ok := entries[0].(map[string]interface{}); ok {
			entry = ent
		}
	}

	output := map[string]interface{}{
		"result": entry,
	}

	if entry != nil {
		if content, ok := entry["content"].(map[string]interface{}); ok {
			if search, ok := content["search"].(string); ok {
				output["search"] = search
			}
			if enabled, ok := content["enabled"].(string); ok {
				output["enabled"] = enabled
			}
			if priority, ok := content["priority"].(string); ok {
				output["priority"] = priority
			}
		}
	}

	return &executor.StepResult{Output: output}, nil
}

// EventTypeCreateExecutor handles splunk-event-type-create
type EventTypeCreateExecutor struct {
	client *http.Client
}

func (e *EventTypeCreateExecutor) Type() string {
	return "splunk-event-type-create"
}

func (e *EventTypeCreateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	server := resolver.ResolveString(getString(config, "server"))
	authToken := resolver.ResolveString(getString(config, "authToken"))
	name := resolver.ResolveString(getString(config, "name"))
	search := resolver.ResolveString(getString(config, "search"))
	app := resolver.ResolveString(getString(config, "app"))
	enabled := resolver.ResolveString(getString(config, "enabled"))
	priority := resolver.ResolveString(getString(config, "priority"))

	if name == "" {
		return nil, fmt.Errorf("name is required")
	}
	if search == "" {
		return nil, fmt.Errorf("search is required")
	}

	body := url.Values{}
	body.Set("name", name)
	body.Set("search", search)

	if app != "" {
		body.Set("app", app)
	}
	if enabled != "" {
		body.Set("enabled", enabled)
	}
	if priority != "" {
		body.Set("priority", priority)
	}

	skill := &SplunkSkill{client: e.client}
	respBody, err := skill.doRequest(ctx, "POST", server, "/services/data/eventtypes", authToken, nil, body)
	if err != nil {
		return nil, err
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"result":  string(respBody),
			"name":    name,
			"success": true,
		},
	}, nil
}

// EventTypeUpdateExecutor handles splunk-event-type-update
type EventTypeUpdateExecutor struct {
	client *http.Client
}

func (e *EventTypeUpdateExecutor) Type() string {
	return "splunk-event-type-update"
}

func (e *EventTypeUpdateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	server := resolver.ResolveString(getString(config, "server"))
	authToken := resolver.ResolveString(getString(config, "authToken"))
	eventTypeName := resolver.ResolveString(getString(config, "eventTypeName"))
	app := resolver.ResolveString(getString(config, "app"))

	if eventTypeName == "" {
		return nil, fmt.Errorf("eventTypeName is required")
	}

	body := url.Values{}

	if search := resolver.ResolveString(getString(config, "search")); search != "" {
		body.Set("search", search)
	}
	if enabled := resolver.ResolveString(getString(config, "enabled")); enabled != "" {
		body.Set("enabled", enabled)
	}
	if priority := resolver.ResolveString(getString(config, "priority")); priority != "" {
		body.Set("priority", priority)
	}

	if len(body) == 0 {
		return nil, fmt.Errorf("no fields to update")
	}

	path := fmt.Sprintf("/services/data/eventtypes/%s", url.PathEscape(eventTypeName))
	if app != "" {
		path += "?app=" + url.QueryEscape(app)
	}

	skill := &SplunkSkill{client: e.client}
	respBody, err := skill.doRequest(ctx, "POST", server, path, authToken, nil, body)
	if err != nil {
		return nil, err
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"result":  string(respBody),
			"success": true,
		},
	}, nil
}

// EventTypeDeleteExecutor handles splunk-event-type-delete
type EventTypeDeleteExecutor struct {
	client *http.Client
}

func (e *EventTypeDeleteExecutor) Type() string {
	return "splunk-event-type-delete"
}

func (e *EventTypeDeleteExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	server := resolver.ResolveString(getString(config, "server"))
	authToken := resolver.ResolveString(getString(config, "authToken"))
	eventTypeName := resolver.ResolveString(getString(config, "eventTypeName"))

	if eventTypeName == "" {
		return nil, fmt.Errorf("eventTypeName is required")
	}

	skill := &SplunkSkill{client: e.client}
	_, err := skill.doRequest(ctx, "DELETE", server, fmt.Sprintf("/services/data/eventtypes/%s", url.PathEscape(eventTypeName)), authToken, nil, nil)
	if err != nil {
		return nil, err
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success": true,
		},
	}, nil
}

// ============================================================================
// SPLUNK INFO EXECUTOR
// ============================================================================

// InfoExecutor handles splunk-info
type InfoExecutor struct {
	client *http.Client
}

func (e *InfoExecutor) Type() string {
	return "splunk-info"
}

func (e *InfoExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	server := resolver.ResolveString(getString(config, "server"))
	authToken := resolver.ResolveString(getString(config, "authToken"))

	skill := &SplunkSkill{client: e.client}
	respBody, err := skill.doRequest(ctx, "GET", server, "/services/server/info", authToken, url.Values{"output_mode": []string{"json"}}, nil)
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	var content map[string]interface{}
	if entries, ok := result["entry"].([]interface{}); ok && len(entries) > 0 {
		if ent, ok := entries[0].(map[string]interface{}); ok {
			if c, ok := ent["content"].(map[string]interface{}); ok {
				content = c
			}
		}
	}

	output := map[string]interface{}{
		"result": result,
	}

	if content != nil {
		if version, ok := content["version"].(string); ok {
			output["version"] = version
		}
		if build, ok := content["build"].(string); ok {
			output["build"] = build
		}
		if serverName, ok := content["serverName"].(string); ok {
			output["serverName"] = serverName
		}
		if timeZone, ok := content["timeZone"].(string); ok {
			output["timeZone"] = timeZone
		}
	}

	return &executor.StepResult{Output: output}, nil
}

// ============================================================================
// MAIN
// ============================================================================

func main() {
	port := os.Getenv("SKILL_PORT")
	if port == "" {
		port = "50122"
	}

	server := grpc.NewSkillServer("skill-splunk", "1.0.0")
	skill := NewSplunkSkill()

	// Register search executors
	server.RegisterExecutor("splunk-search", &SearchExecutor{client: skill.client})

	// Register saved search executors
	server.RegisterExecutor("splunk-saved-search", &SavedSearchGetExecutor{client: skill.client})
	server.RegisterExecutor("splunk-saved-search-list", &SavedSearchListExecutor{client: skill.client})
	server.RegisterExecutor("splunk-saved-search-create", &SavedSearchCreateExecutor{client: skill.client})
	server.RegisterExecutor("splunk-saved-search-update", &SavedSearchUpdateExecutor{client: skill.client})
	server.RegisterExecutor("splunk-saved-search-delete", &SavedSearchDeleteExecutor{client: skill.client})
	server.RegisterExecutor("splunk-saved-search-run", &SavedSearchRunExecutor{client: skill.client})

	// Register search job executors
	server.RegisterExecutor("splunk-search-job-create", &SearchJobCreateExecutor{client: skill.client})
	server.RegisterExecutor("splunk-search-job-status", &SearchJobStatusExecutor{client: skill.client})
	server.RegisterExecutor("splunk-search-job-results", &SearchJobResultsExecutor{client: skill.client})
	server.RegisterExecutor("splunk-search-job-cancel", &SearchJobCancelExecutor{client: skill.client})
	server.RegisterExecutor("splunk-search-job-wait", &SearchJobWaitExecutor{client: skill.client})

	// Register index executors
	server.RegisterExecutor("splunk-index-list", &IndexListExecutor{client: skill.client})
	server.RegisterExecutor("splunk-index-get", &IndexGetExecutor{client: skill.client})
	server.RegisterExecutor("splunk-index-create", &IndexCreateExecutor{client: skill.client})
	server.RegisterExecutor("splunk-index-update", &IndexUpdateExecutor{client: skill.client})
	server.RegisterExecutor("splunk-index-delete", &IndexDeleteExecutor{client: skill.client})

	// Register alert executors
	server.RegisterExecutor("splunk-alert-create", &AlertCreateExecutor{client: skill.client})
	server.RegisterExecutor("splunk-alert-list", &AlertListExecutor{client: skill.client})
	server.RegisterExecutor("splunk-alert-get", &AlertGetExecutor{client: skill.client})
	server.RegisterExecutor("splunk-alert-update", &AlertUpdateExecutor{client: skill.client})
	server.RegisterExecutor("splunk-alert-delete", &AlertDeleteExecutor{client: skill.client})
	server.RegisterExecutor("splunk-alert-history", &AlertHistoryExecutor{client: skill.client})

	// Register dashboard executors
	server.RegisterExecutor("splunk-dashboard-list", &DashboardListExecutor{client: skill.client})
	server.RegisterExecutor("splunk-dashboard-get", &DashboardGetExecutor{client: skill.client})
	server.RegisterExecutor("splunk-dashboard-create", &DashboardCreateExecutor{client: skill.client})
	server.RegisterExecutor("splunk-dashboard-update", &DashboardUpdateExecutor{client: skill.client})
	server.RegisterExecutor("splunk-dashboard-delete", &DashboardDeleteExecutor{client: skill.client})

	// Register event type executors
	server.RegisterExecutor("splunk-event-type-list", &EventTypeListExecutor{client: skill.client})
	server.RegisterExecutor("splunk-event-type-get", &EventTypeGetExecutor{client: skill.client})
	server.RegisterExecutor("splunk-event-type-create", &EventTypeCreateExecutor{client: skill.client})
	server.RegisterExecutor("splunk-event-type-update", &EventTypeUpdateExecutor{client: skill.client})
	server.RegisterExecutor("splunk-event-type-delete", &EventTypeDeleteExecutor{client: skill.client})

	// Register utility executors
	server.RegisterExecutor("splunk-info", &InfoExecutor{client: skill.client})

	fmt.Printf("Starting skill-splunk gRPC server on port %s\n", port)
	if err := server.Serve(port); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to start server: %v\n", err)
		os.Exit(1)
	}
}
