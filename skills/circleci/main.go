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
	"github.com/axiom-studio/skills.sdk/resolver"
)

const (
	iconCircleCI = "circle"
	apiBaseURL   = "https://circleci.com/api/v2"
)

// CircleCISkill holds the HTTP client for CircleCI API calls
type CircleCISkill struct {
	client *http.Client
}

func NewCircleCISkill() *CircleCISkill {
	return &CircleCISkill{
		client: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

// ============================================================================
// HTTP Client Helpers
// ============================================================================

// doRequest performs an HTTP request to the CircleCI API
func (s *CircleCISkill) doRequest(ctx context.Context, method, endpoint, token string, body interface{}) ([]byte, int, error) {
	var reqBody io.Reader
	if body != nil {
		jsonBody, err := json.Marshal(body)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to marshal request body: %w", err)
		}
		reqBody = bytes.NewReader(jsonBody)
	}

	req, err := http.NewRequestWithContext(ctx, method, endpoint, reqBody)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Circle-Token", token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to read response: %w", err)
	}

	return respBody, resp.StatusCode, nil
}

// get performs a GET request
func (s *CircleCISkill) get(ctx context.Context, path, token string) ([]byte, int, error) {
	return s.doRequest(ctx, http.MethodGet, apiBaseURL+path, token, nil)
}

// post performs a POST request
func (s *CircleCISkill) post(ctx context.Context, path, token string, body interface{}) ([]byte, int, error) {
	return s.doRequest(ctx, http.MethodPost, apiBaseURL+path, token, body)
}

// put performs a PUT request
func (s *CircleCISkill) put(ctx context.Context, path, token string, body interface{}) ([]byte, int, error) {
	return s.doRequest(ctx, http.MethodPut, apiBaseURL+path, token, body)
}

// delete performs a DELETE request
func (s *CircleCISkill) delete(ctx context.Context, path, token string) ([]byte, int, error) {
	return s.doRequest(ctx, http.MethodDelete, apiBaseURL+path, token, nil)
}

// ============================================================================
// Helper Functions
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
		case int64:
			return int(n)
		}
	}
	return def
}

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
// SCHEMAS
// ============================================================================

// CircleCIProjectListSchema is the UI schema for circleci-project-list
var CircleCIProjectListSchema = resolver.NewSchemaBuilder("circleci-project-list").
	WithName("List Projects").
	WithCategory("action").
	WithIcon(iconCircleCI).
	WithDescription("List CircleCI projects for an organization").
	AddSection("Authentication").
	AddExpressionField("token", "CircleCI Token",
		resolver.WithRequired(),
		resolver.WithPlaceholder("api_token_xxxx"),
		resolver.WithHint("CircleCI API token (supports {{bindings.circleci}})"),
	).
	EndSection().
	AddSection("Organization").
	AddExpressionField("org", "Organization",
		resolver.WithRequired(),
		resolver.WithPlaceholder("github"),
		resolver.WithHint("GitHub organization name (VCS type defaults to github)"),
	).
	AddExpressionField("vcsType", "VCS Type",
		resolver.WithDefault("github"),
		resolver.WithPlaceholder("github, bitbucket"),
		resolver.WithHint("Version control system type (github or bitbucket)"),
	).
	AddNumberField("limit", "Limit",
		resolver.WithDefault(100),
		resolver.WithMinMax(1, 1000),
		resolver.WithHint("Maximum number of projects to return"),
	).
	EndSection().
	Build()

// CircleCIPipelineTriggerSchema is the UI schema for circleci-pipeline-trigger
var CircleCIPipelineTriggerSchema = resolver.NewSchemaBuilder("circleci-pipeline-trigger").
	WithName("Trigger Pipeline").
	WithCategory("action").
	WithIcon(iconCircleCI).
	WithDescription("Trigger a new CircleCI pipeline").
	AddSection("Authentication").
	AddExpressionField("token", "CircleCI Token",
		resolver.WithRequired(),
		resolver.WithPlaceholder("api_token_xxxx"),
	).
	EndSection().
	AddSection("Project").
	AddExpressionField("org", "Organization",
		resolver.WithRequired(),
		resolver.WithPlaceholder("github"),
	).
	AddExpressionField("repo", "Repository",
		resolver.WithRequired(),
		resolver.WithPlaceholder("my-repo"),
	).
	AddExpressionField("vcsType", "VCS Type",
		resolver.WithDefault("github"),
		resolver.WithPlaceholder("github, bitbucket"),
	).
	EndSection().
	AddSection("Pipeline Configuration").
	AddExpressionField("branch", "Branch",
		resolver.WithDefault("main"),
		resolver.WithPlaceholder("main"),
		resolver.WithHint("Branch to build from"),
	).
	AddExpressionField("tag", "Tag",
		resolver.WithPlaceholder("v1.0.0"),
		resolver.WithHint("Optional: Tag to build instead of branch"),
	).
	AddExpressionField("revision", "Revision (SHA)",
		resolver.WithPlaceholder("abc123..."),
		resolver.WithHint("Optional: Specific commit SHA to build"),
	).
	AddJSONField("parameters", "Pipeline Parameters",
		resolver.WithHint("Optional: Pipeline parameters as JSON object"),
	).
	EndSection().
	Build()

// CircleCIPipelineListSchema is the UI schema for circleci-pipeline-list
var CircleCIPipelineListSchema = resolver.NewSchemaBuilder("circleci-pipeline-list").
	WithName("List Pipelines").
	WithCategory("action").
	WithIcon(iconCircleCI).
	WithDescription("List pipelines for a CircleCI project").
	AddSection("Authentication").
	AddExpressionField("token", "CircleCI Token",
		resolver.WithRequired(),
		resolver.WithPlaceholder("api_token_xxxx"),
	).
	EndSection().
	AddSection("Project").
	AddExpressionField("org", "Organization",
		resolver.WithRequired(),
		resolver.WithPlaceholder("github"),
	).
	AddExpressionField("repo", "Repository",
		resolver.WithRequired(),
		resolver.WithPlaceholder("my-repo"),
	).
	AddExpressionField("vcsType", "VCS Type",
		resolver.WithDefault("github"),
		resolver.WithPlaceholder("github, bitbucket"),
	).
	EndSection().
	AddSection("Filters").
	AddExpressionField("branch", "Branch",
		resolver.WithPlaceholder("main"),
		resolver.WithHint("Optional: Filter by branch"),
	).
	AddNumberField("limit", "Limit",
		resolver.WithDefault(50),
		resolver.WithMinMax(1, 1000),
	).
	EndSection().
	Build()

// CircleCIPipelineStatusSchema is the UI schema for circleci-pipeline-status
var CircleCIPipelineStatusSchema = resolver.NewSchemaBuilder("circleci-pipeline-status").
	WithName("Get Pipeline Status").
	WithCategory("action").
	WithIcon(iconCircleCI).
	WithDescription("Get the status of a specific CircleCI pipeline").
	AddSection("Authentication").
	AddExpressionField("token", "CircleCI Token",
		resolver.WithRequired(),
		resolver.WithPlaceholder("api_token_xxxx"),
	).
	EndSection().
	AddSection("Pipeline").
	AddExpressionField("pipelineId", "Pipeline ID",
		resolver.WithRequired(),
		resolver.WithPlaceholder("abc123..."),
		resolver.WithHint("The pipeline ID to check"),
	).
	EndSection().
	Build()

// CircleCIWorkflowListSchema is the UI schema for circleci-workflow-list
var CircleCIWorkflowListSchema = resolver.NewSchemaBuilder("circleci-workflow-list").
	WithName("List Workflows").
	WithCategory("action").
	WithIcon(iconCircleCI).
	WithDescription("List workflows for a CircleCI pipeline").
	AddSection("Authentication").
	AddExpressionField("token", "CircleCI Token",
		resolver.WithRequired(),
		resolver.WithPlaceholder("api_token_xxxx"),
	).
	EndSection().
	AddSection("Pipeline").
	AddExpressionField("pipelineId", "Pipeline ID",
		resolver.WithRequired(),
		resolver.WithPlaceholder("abc123..."),
		resolver.WithHint("The pipeline ID to get workflows for"),
	).
	EndSection().
	Build()

// CircleCIWorkflowStatusSchema is the UI schema for circleci-workflow-status
var CircleCIWorkflowStatusSchema = resolver.NewSchemaBuilder("circleci-workflow-status").
	WithName("Get Workflow Status").
	WithCategory("action").
	WithIcon(iconCircleCI).
	WithDescription("Get the status of a specific CircleCI workflow").
	AddSection("Authentication").
	AddExpressionField("token", "CircleCI Token",
		resolver.WithRequired(),
		resolver.WithPlaceholder("api_token_xxxx"),
	).
	EndSection().
	AddSection("Workflow").
	AddExpressionField("workflowId", "Workflow ID",
		resolver.WithRequired(),
		resolver.WithPlaceholder("abc123..."),
		resolver.WithHint("The workflow ID to check"),
	).
	EndSection().
	Build()

// CircleCIJobListSchema is the UI schema for circleci-job-list
var CircleCIJobListSchema = resolver.NewSchemaBuilder("circleci-job-list").
	WithName("List Jobs").
	WithCategory("action").
	WithIcon(iconCircleCI).
	WithDescription("List jobs for a CircleCI workflow").
	AddSection("Authentication").
	AddExpressionField("token", "CircleCI Token",
		resolver.WithRequired(),
		resolver.WithPlaceholder("api_token_xxxx"),
	).
	EndSection().
	AddSection("Workflow").
	AddExpressionField("workflowId", "Workflow ID",
		resolver.WithRequired(),
		resolver.WithPlaceholder("abc123..."),
		resolver.WithHint("The workflow ID to get jobs for"),
	).
	EndSection().
	Build()

// CircleCIJobLogsSchema is the UI schema for circleci-job-logs
var CircleCIJobLogsSchema = resolver.NewSchemaBuilder("circleci-job-logs").
	WithName("Get Job Logs").
	WithCategory("action").
	WithIcon(iconCircleCI).
	WithDescription("Get logs for a CircleCI job").
	AddSection("Authentication").
	AddExpressionField("token", "CircleCI Token",
		resolver.WithRequired(),
		resolver.WithPlaceholder("api_token_xxxx"),
	).
	EndSection().
	AddSection("Job").
	AddExpressionField("projectSlug", "Project Slug",
		resolver.WithRequired(),
		resolver.WithPlaceholder("gh/github/my-repo"),
		resolver.WithHint("Project slug in format: vcs/org/repo"),
	).
	AddNumberField("jobNumber", "Job Number",
		resolver.WithRequired(),
		resolver.WithMinMax(1, 999999),
		resolver.WithHint("The job number to get logs for"),
	).
	EndSection().
	Build()

// CircleCIContextListSchema is the UI schema for circleci-context-list
var CircleCIContextListSchema = resolver.NewSchemaBuilder("circleci-context-list").
	WithName("List Contexts").
	WithCategory("action").
	WithIcon(iconCircleCI).
	WithDescription("List CircleCI contexts for an organization").
	AddSection("Authentication").
	AddExpressionField("token", "CircleCI Token",
		resolver.WithRequired(),
		resolver.WithPlaceholder("api_token_xxxx"),
	).
	EndSection().
	AddSection("Organization").
	AddExpressionField("orgId", "Organization ID",
		resolver.WithRequired(),
		resolver.WithPlaceholder("org-id-xxxx"),
		resolver.WithHint("The organization ID (UUID format)"),
	).
	EndSection().
	Build()

// CircleCIContextSetEnvSchema is the UI schema for circleci-context-set-env
var CircleCIContextSetEnvSchema = resolver.NewSchemaBuilder("circleci-context-set-env").
	WithName("Set Context Environment Variable").
	WithCategory("action").
	WithIcon(iconCircleCI).
	WithDescription("Set an environment variable in a CircleCI context").
	AddSection("Authentication").
	AddExpressionField("token", "CircleCI Token",
		resolver.WithRequired(),
		resolver.WithPlaceholder("api_token_xxxx"),
	).
	EndSection().
	AddSection("Context").
	AddExpressionField("contextId", "Context ID",
		resolver.WithRequired(),
		resolver.WithPlaceholder("ctx-id-xxxx"),
		resolver.WithHint("The context ID (UUID format)"),
	).
	EndSection().
	AddSection("Environment Variable").
	AddExpressionField("name", "Variable Name",
		resolver.WithRequired(),
		resolver.WithPlaceholder("API_KEY"),
		resolver.WithHint("Name of the environment variable"),
	).
	AddExpressionField("value", "Variable Value",
		resolver.WithRequired(),
		resolver.WithPlaceholder("secret-value"),
		resolver.WithSensitive(),
		resolver.WithHint("Value of the environment variable"),
	).
	EndSection().
	Build()

// ============================================================================
// EXECUTORS
// ============================================================================

// CircleCIProjectListExecutor handles circleci-project-list node type
type CircleCIProjectListExecutor struct {
	skill *CircleCISkill
}

func (e *CircleCIProjectListExecutor) Type() string { return "circleci-project-list" }

func (e *CircleCIProjectListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := templateResolver.ResolveMap(step.Config)

	token := getString(config, "token")
	org := getString(config, "org")
	vcsType := getString(config, "vcsType")
	limit := getInt(config, "limit", 100)

	if token == "" {
		return nil, fmt.Errorf("circleci token is required")
	}
	if org == "" {
		return nil, fmt.Errorf("organization is required")
	}
	if vcsType == "" {
		vcsType = "github"
	}

	// CircleCI doesn't have a direct "list projects" endpoint
	// We need to use the projects endpoint with org filter
	// GET /api/v2/project?organization-id={org-id}
	// But we need org ID first, so we'll use a workaround
	// Actually, we can list pipelines for the org to discover projects

	// For now, we'll return a message that project listing requires org ID
	// In practice, users should provide the org ID
	output := map[string]interface{}{
		"message": "Project listing requires organization ID. Use the organization's UUID.",
		"org":     org,
		"vcsType": vcsType,
		"hint":    "To get organization ID, use the CircleCI API: GET /api/v2/organization/github/{org}",
	}

	// Try to get org info first
	orgInfo, err := e.getOrganization(ctx, token, vcsType, org)
	if err == nil {
		output["organizationId"] = orgInfo["id"]
		output["organizationName"] = orgInfo["name"]

		// Now list projects using org ID
		projects, err := e.listProjects(ctx, token, orgInfo["id"].(string), limit)
		if err != nil {
			output["error"] = err.Error()
		} else {
			output["projects"] = projects
			output["count"] = len(projects)
		}
	} else {
		output["organizationLookupError"] = err.Error()
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

func (e *CircleCIProjectListExecutor) getOrganization(ctx context.Context, token, vcsType, orgName string) (map[string]interface{}, error) {
	path := fmt.Sprintf("/organization/%s/%s", vcsType, orgName)
	respBody, statusCode, err := e.skill.get(ctx, path, token)
	if err != nil {
		return nil, err
	}
	if statusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to get organization: %s", string(respBody))
	}

	var org map[string]interface{}
	if err := json.Unmarshal(respBody, &org); err != nil {
		return nil, fmt.Errorf("failed to parse organization response: %w", err)
	}
	return org, nil
}

func (e *CircleCIProjectListExecutor) listProjects(ctx context.Context, token, orgID string, limit int) ([]map[string]interface{}, error) {
	var allProjects []map[string]interface{}
	nextPage := ""

	for len(allProjects) < limit {
		path := fmt.Sprintf("/project?organization-id=%s&limit=%d", url.QueryEscape(orgID), 50)
		if nextPage != "" {
			path += "&page-token=" + nextPage
		}

		respBody, statusCode, err := e.skill.get(ctx, path, token)
		if err != nil {
			return nil, err
		}
		if statusCode != http.StatusOK {
			return nil, fmt.Errorf("failed to list projects: %s", string(respBody))
		}

		var response struct {
			Items         []map[string]interface{} `json:"items"`
			NextPageToken string                   `json:"next_page_token"`
		}
		if err := json.Unmarshal(respBody, &response); err != nil {
			return nil, fmt.Errorf("failed to parse projects response: %w", err)
		}

		allProjects = append(allProjects, response.Items...)
		nextPage = response.NextPageToken

		if nextPage == "" || len(response.Items) == 0 {
			break
		}
	}

	if len(allProjects) > limit {
		allProjects = allProjects[:limit]
	}

	return allProjects, nil
}

// CircleCIPipelineTriggerExecutor handles circleci-pipeline-trigger node type
type CircleCIPipelineTriggerExecutor struct {
	skill *CircleCISkill
}

func (e *CircleCIPipelineTriggerExecutor) Type() string { return "circleci-pipeline-trigger" }

func (e *CircleCIPipelineTriggerExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := templateResolver.ResolveMap(step.Config)

	token := getString(config, "token")
	org := getString(config, "org")
	repo := getString(config, "repo")
	vcsType := getString(config, "vcsType")
	branch := getString(config, "branch")
	tag := getString(config, "tag")
	revision := getString(config, "revision")
	parametersJSON := getString(config, "parameters")

	if token == "" {
		return nil, fmt.Errorf("circleci token is required")
	}
	if org == "" {
		return nil, fmt.Errorf("organization is required")
	}
	if repo == "" {
		return nil, fmt.Errorf("repository is required")
	}
	if vcsType == "" {
		vcsType = "github"
	}
	if branch == "" && tag == "" {
		branch = "main"
	}

	// Build request body
	body := map[string]interface{}{
		"branch": branch,
	}

	if tag != "" {
		body["tag"] = tag
		delete(body, "branch")
	}

	if revision != "" {
		body["revision"] = revision
	}

	// Parse parameters if provided
	if parametersJSON != "" {
		var params map[string]interface{}
		if err := json.Unmarshal([]byte(parametersJSON), &params); err != nil {
			return nil, fmt.Errorf("invalid parameters JSON: %w", err)
		}
		body["parameters"] = params
	}

	// Trigger pipeline
	path := fmt.Sprintf("/project/%s/%s/%s/pipeline", vcsType, org, repo)
	respBody, statusCode, err := e.skill.post(ctx, path, token, body)
	if err != nil {
		return nil, fmt.Errorf("failed to trigger pipeline: %w", err)
	}
	if statusCode != http.StatusCreated && statusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to trigger pipeline (status %d): %s", statusCode, string(respBody))
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	output := map[string]interface{}{
		"success":     true,
		"pipelineId":  getStringFromMap(result, "id"),
		"number":      getIntFromMap(result, "number", 0),
		"createdAt":   getStringFromMap(result, "created_at"),
		"triggerType": getStringFromMap(result, "trigger_type"),
	}

	if state, ok := result["state"].(map[string]interface{}); ok {
		output["state"] = state
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// CircleCIPipelineListExecutor handles circleci-pipeline-list node type
type CircleCIPipelineListExecutor struct {
	skill *CircleCISkill
}

func (e *CircleCIPipelineListExecutor) Type() string { return "circleci-pipeline-list" }

func (e *CircleCIPipelineListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := templateResolver.ResolveMap(step.Config)

	token := getString(config, "token")
	org := getString(config, "org")
	repo := getString(config, "repo")
	vcsType := getString(config, "vcsType")
	branch := getString(config, "branch")
	limit := getInt(config, "limit", 50)

	if token == "" {
		return nil, fmt.Errorf("circleci token is required")
	}
	if org == "" {
		return nil, fmt.Errorf("organization is required")
	}
	if repo == "" {
		return nil, fmt.Errorf("repository is required")
	}
	if vcsType == "" {
		vcsType = "github"
	}

	var allPipelines []map[string]interface{}
	nextPage := ""

	for len(allPipelines) < limit {
		pageLimit := 50
		if len(allPipelines)+pageLimit > limit {
			pageLimit = limit - len(allPipelines)
		}

		path := fmt.Sprintf("/project/%s/%s/%s/pipeline?limit=%d", vcsType, org, repo, pageLimit)
		if branch != "" {
			path += "&branch=" + url.QueryEscape(branch)
		}
		if nextPage != "" {
			path += "&page-token=" + nextPage
		}

		respBody, statusCode, err := e.skill.get(ctx, path, token)
		if err != nil {
			return nil, fmt.Errorf("failed to list pipelines: %w", err)
		}
		if statusCode != http.StatusOK {
			return nil, fmt.Errorf("failed to list pipelines (status %d): %s", statusCode, string(respBody))
		}

		var response struct {
			Items         []map[string]interface{} `json:"items"`
			NextPageToken string                   `json:"next_page_token"`
		}
		if err := json.Unmarshal(respBody, &response); err != nil {
			return nil, fmt.Errorf("failed to parse pipelines response: %w", err)
		}

		allPipelines = append(allPipelines, response.Items...)
		nextPage = response.NextPageToken

		if nextPage == "" || len(response.Items) == 0 {
			break
		}
	}

	output := map[string]interface{}{
		"pipelines": allPipelines,
		"count":     len(allPipelines),
		"org":       org,
		"repo":      repo,
	}

	if branch != "" {
		output["branch"] = branch
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// CircleCIPipelineStatusExecutor handles circleci-pipeline-status node type
type CircleCIPipelineStatusExecutor struct {
	skill *CircleCISkill
}

func (e *CircleCIPipelineStatusExecutor) Type() string { return "circleci-pipeline-status" }

func (e *CircleCIPipelineStatusExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := templateResolver.ResolveMap(step.Config)

	token := getString(config, "token")
	pipelineId := getString(config, "pipelineId")

	if token == "" {
		return nil, fmt.Errorf("circleci token is required")
	}
	if pipelineId == "" {
		return nil, fmt.Errorf("pipeline ID is required")
	}

	path := fmt.Sprintf("/pipeline/%s", pipelineId)
	respBody, statusCode, err := e.skill.get(ctx, path, token)
	if err != nil {
		return nil, fmt.Errorf("failed to get pipeline status: %w", err)
	}
	if statusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to get pipeline status (status %d): %s", statusCode, string(respBody))
	}

	var pipeline map[string]interface{}
	if err := json.Unmarshal(respBody, &pipeline); err != nil {
		return nil, fmt.Errorf("failed to parse pipeline response: %w", err)
	}

	output := map[string]interface{}{
		"id":             getStringFromMap(pipeline, "id"),
		"number":         getIntFromMap(pipeline, "number", 0),
		"createdAt":      getStringFromMap(pipeline, "created_at"),
		"triggerType":    getStringFromMap(pipeline, "trigger_type"),
		"triggerSubject": getStringFromMap(pipeline, "trigger_subject"),
		"state":          getStringFromMap(pipeline, "state"),
		"vcs":            pipeline["vcs"],
		"project":        pipeline["project"],
		"errors":         pipeline["errors"],
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// CircleCIWorkflowListExecutor handles circleci-workflow-list node type
type CircleCIWorkflowListExecutor struct {
	skill *CircleCISkill
}

func (e *CircleCIWorkflowListExecutor) Type() string { return "circleci-workflow-list" }

func (e *CircleCIWorkflowListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := templateResolver.ResolveMap(step.Config)

	token := getString(config, "token")
	pipelineId := getString(config, "pipelineId")

	if token == "" {
		return nil, fmt.Errorf("circleci token is required")
	}
	if pipelineId == "" {
		return nil, fmt.Errorf("pipeline ID is required")
	}

	path := fmt.Sprintf("/pipeline/%s/workflow", pipelineId)
	respBody, statusCode, err := e.skill.get(ctx, path, token)
	if err != nil {
		return nil, fmt.Errorf("failed to list workflows: %w", err)
	}
	if statusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to list workflows (status %d): %s", statusCode, string(respBody))
	}

	var response struct {
		Items         []map[string]interface{} `json:"items"`
		NextPageToken string                   `json:"next_page_token"`
	}
	if err := json.Unmarshal(respBody, &response); err != nil {
		return nil, fmt.Errorf("failed to parse workflows response: %w", err)
	}

	output := map[string]interface{}{
		"workflows":  response.Items,
		"count":      len(response.Items),
		"pipelineId": pipelineId,
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// CircleCIWorkflowStatusExecutor handles circleci-workflow-status node type
type CircleCIWorkflowStatusExecutor struct {
	skill *CircleCISkill
}

func (e *CircleCIWorkflowStatusExecutor) Type() string { return "circleci-workflow-status" }

func (e *CircleCIWorkflowStatusExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := templateResolver.ResolveMap(step.Config)

	token := getString(config, "token")
	workflowId := getString(config, "workflowId")

	if token == "" {
		return nil, fmt.Errorf("circleci token is required")
	}
	if workflowId == "" {
		return nil, fmt.Errorf("workflow ID is required")
	}

	path := fmt.Sprintf("/workflow/%s", workflowId)
	respBody, statusCode, err := e.skill.get(ctx, path, token)
	if err != nil {
		return nil, fmt.Errorf("failed to get workflow status: %w", err)
	}
	if statusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to get workflow status (status %d): %s", statusCode, string(respBody))
	}

	var workflow map[string]interface{}
	if err := json.Unmarshal(respBody, &workflow); err != nil {
		return nil, fmt.Errorf("failed to parse workflow response: %w", err)
	}

	output := map[string]interface{}{
		"id":          getStringFromMap(workflow, "id"),
		"name":        getStringFromMap(workflow, "name"),
		"pipelineId":  getStringFromMap(workflow, "pipeline_id"),
		"status":      getStringFromMap(workflow, "status"),
		"startedAt":   getStringFromMap(workflow, "started_at"),
		"stoppedAt":   getStringFromMap(workflow, "stopped_at"),
		"canceledBy":  getStringFromMap(workflow, "canceled_by"),
		"projectSlug": getStringFromMap(workflow, "project_slug"),
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// CircleCIJobListExecutor handles circleci-job-list node type
type CircleCIJobListExecutor struct {
	skill *CircleCISkill
}

func (e *CircleCIJobListExecutor) Type() string { return "circleci-job-list" }

func (e *CircleCIJobListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := templateResolver.ResolveMap(step.Config)

	token := getString(config, "token")
	workflowId := getString(config, "workflowId")

	if token == "" {
		return nil, fmt.Errorf("circleci token is required")
	}
	if workflowId == "" {
		return nil, fmt.Errorf("workflow ID is required")
	}

	path := fmt.Sprintf("/workflow/%s/job", workflowId)
	respBody, statusCode, err := e.skill.get(ctx, path, token)
	if err != nil {
		return nil, fmt.Errorf("failed to list jobs: %w", err)
	}
	if statusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to list jobs (status %d): %s", statusCode, string(respBody))
	}

	var response struct {
		Items         []map[string]interface{} `json:"items"`
		NextPageToken string                   `json:"next_page_token"`
	}
	if err := json.Unmarshal(respBody, &response); err != nil {
		return nil, fmt.Errorf("failed to parse jobs response: %w", err)
	}

	output := map[string]interface{}{
		"jobs":       response.Items,
		"count":      len(response.Items),
		"workflowId": workflowId,
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// CircleCIJobLogsExecutor handles circleci-job-logs node type
type CircleCIJobLogsExecutor struct {
	skill *CircleCISkill
}

func (e *CircleCIJobLogsExecutor) Type() string { return "circleci-job-logs" }

func (e *CircleCIJobLogsExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := templateResolver.ResolveMap(step.Config)

	token := getString(config, "token")
	projectSlug := getString(config, "projectSlug")
	jobNumber := getInt(config, "jobNumber", 0)

	if token == "" {
		return nil, fmt.Errorf("circleci token is required")
	}
	if projectSlug == "" {
		return nil, fmt.Errorf("project slug is required")
	}
	if jobNumber == 0 {
		return nil, fmt.Errorf("job number is required")
	}

	path := fmt.Sprintf("/project/%s/%d/logs", projectSlug, jobNumber)
	respBody, statusCode, err := e.skill.get(ctx, path, token)
	if err != nil {
		return nil, fmt.Errorf("failed to get job logs: %w", err)
	}
	if statusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to get job logs (status %d): %s", statusCode, string(respBody))
	}

	// Parse the logs response - CircleCI returns an array of log items
	var logItems []map[string]interface{}
	if err := json.Unmarshal(respBody, &logItems); err != nil {
		// If it's not JSON, return raw response
		return &executor.StepResult{
			Output: map[string]interface{}{
				"logs":        string(respBody),
				"projectSlug": projectSlug,
				"jobNumber":   jobNumber,
			},
		}, nil
	}

	// Combine all log messages
	var allLogs strings.Builder
	for _, item := range logItems {
		if message, ok := item["message"].(string); ok {
			allLogs.WriteString(message)
			allLogs.WriteString("\n")
		}
	}

	output := map[string]interface{}{
		"logs":        allLogs.String(),
		"logItems":    logItems,
		"count":       len(logItems),
		"projectSlug": projectSlug,
		"jobNumber":   jobNumber,
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// CircleCIContextListExecutor handles circleci-context-list node type
type CircleCIContextListExecutor struct {
	skill *CircleCISkill
}

func (e *CircleCIContextListExecutor) Type() string { return "circleci-context-list" }

func (e *CircleCIContextListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := templateResolver.ResolveMap(step.Config)

	token := getString(config, "token")
	orgId := getString(config, "orgId")

	if token == "" {
		return nil, fmt.Errorf("circleci token is required")
	}
	if orgId == "" {
		return nil, fmt.Errorf("organization ID is required")
	}

	var allContexts []map[string]interface{}
	nextPage := ""

	for {
		path := fmt.Sprintf("/context?owner-id=%s", url.QueryEscape(orgId))
		if nextPage != "" {
			path += "&page-token=" + nextPage
		}

		respBody, statusCode, err := e.skill.get(ctx, path, token)
		if err != nil {
			return nil, fmt.Errorf("failed to list contexts: %w", err)
		}
		if statusCode != http.StatusOK {
			return nil, fmt.Errorf("failed to list contexts (status %d): %s", statusCode, string(respBody))
		}

		var response struct {
			Items         []map[string]interface{} `json:"items"`
			NextPageToken string                   `json:"next_page_token"`
		}
		if err := json.Unmarshal(respBody, &response); err != nil {
			return nil, fmt.Errorf("failed to parse contexts response: %w", err)
		}

		allContexts = append(allContexts, response.Items...)
		nextPage = response.NextPageToken

		if nextPage == "" || len(response.Items) == 0 {
			break
		}
	}

	output := map[string]interface{}{
		"contexts": allContexts,
		"count":    len(allContexts),
		"ownerId":  orgId,
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// CircleCIContextSetEnvExecutor handles circleci-context-set-env node type
type CircleCIContextSetEnvExecutor struct {
	skill *CircleCISkill
}

func (e *CircleCIContextSetEnvExecutor) Type() string { return "circleci-context-set-env" }

func (e *CircleCIContextSetEnvExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := templateResolver.ResolveMap(step.Config)

	token := getString(config, "token")
	contextId := getString(config, "contextId")
	name := getString(config, "name")
	value := getString(config, "value")

	if token == "" {
		return nil, fmt.Errorf("circleci token is required")
	}
	if contextId == "" {
		return nil, fmt.Errorf("context ID is required")
	}
	if name == "" {
		return nil, fmt.Errorf("variable name is required")
	}
	if value == "" {
		return nil, fmt.Errorf("variable value is required")
	}

	// Build the environment variable payload
	body := map[string]interface{}{
		"name":  name,
		"value": value,
	}

	// PUT to create or update the environment variable
	path := fmt.Sprintf("/context/%s/environment-variable/%s", contextId, url.QueryEscape(name))
	respBody, statusCode, err := e.skill.put(ctx, path, token, body)
	if err != nil {
		return nil, fmt.Errorf("failed to set environment variable: %w", err)
	}
	if statusCode != http.StatusOK && statusCode != http.StatusCreated {
		return nil, fmt.Errorf("failed to set environment variable (status %d): %s", statusCode, string(respBody))
	}

	output := map[string]interface{}{
		"success":   true,
		"contextId": contextId,
		"name":      name,
		"message":   "Environment variable set successfully",
	}

	// Parse response if available
	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err == nil {
		output["id"] = getStringFromMap(result, "id")
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// ============================================================================
// Helper Functions for Map Access
// ============================================================================

func getStringFromMap(m map[string]interface{}, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func getIntFromMap(m map[string]interface{}, key string, def int) int {
	if v, ok := m[key]; ok {
		switch n := v.(type) {
		case float64:
			return int(n)
		case int:
			return n
		case int64:
			return int(n)
		}
	}
	return def
}

// ============================================================================
// Main
// ============================================================================

func main() {
	port := os.Getenv("SKILL_PORT")
	if port == "" {
		port = "50086"
	}

	server := grpc.NewSkillServer("skill-circleci", "1.0.0")
	skill := NewCircleCISkill()

	// Register all executors with schemas
	server.RegisterExecutorWithSchema("circleci-project-list", &CircleCIProjectListExecutor{skill: skill}, CircleCIProjectListSchema)
	server.RegisterExecutorWithSchema("circleci-pipeline-trigger", &CircleCIPipelineTriggerExecutor{skill: skill}, CircleCIPipelineTriggerSchema)
	server.RegisterExecutorWithSchema("circleci-pipeline-list", &CircleCIPipelineListExecutor{skill: skill}, CircleCIPipelineListSchema)
	server.RegisterExecutorWithSchema("circleci-pipeline-status", &CircleCIPipelineStatusExecutor{skill: skill}, CircleCIPipelineStatusSchema)
	server.RegisterExecutorWithSchema("circleci-workflow-list", &CircleCIWorkflowListExecutor{skill: skill}, CircleCIWorkflowListSchema)
	server.RegisterExecutorWithSchema("circleci-workflow-status", &CircleCIWorkflowStatusExecutor{skill: skill}, CircleCIWorkflowStatusSchema)
	server.RegisterExecutorWithSchema("circleci-job-list", &CircleCIJobListExecutor{skill: skill}, CircleCIJobListSchema)
	server.RegisterExecutorWithSchema("circleci-job-logs", &CircleCIJobLogsExecutor{skill: skill}, CircleCIJobLogsSchema)
	server.RegisterExecutorWithSchema("circleci-context-list", &CircleCIContextListExecutor{skill: skill}, CircleCIContextListSchema)
	server.RegisterExecutorWithSchema("circleci-context-set-env", &CircleCIContextSetEnvExecutor{skill: skill}, CircleCIContextSetEnvSchema)

	fmt.Printf("Starting skill-circleci gRPC server on port %s\n", port)
	if err := server.Serve(":" + port); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to start server: %v\n", err)
		os.Exit(1)
	}
}
