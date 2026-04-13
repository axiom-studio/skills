package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/axiom-studio/skills.sdk/executor"
	"github.com/axiom-studio/skills.sdk/grpc"
	"github.com/axiom-studio/skills.sdk/resolver"
	"github.com/xanzy/go-gitlab"
)

const (
	iconGitLab = "git-branch"
)

// GitLab clients cache
var (
	clients   = make(map[string]*gitlab.Client)
	clientMux sync.RWMutex
	baseURLs  = make(map[string]string)
)

func main() {
	// Get port from env or use default
	port := os.Getenv("SKILL_PORT")
	if port == "" {
		port = "50084"
	}

	// Create skill server
	server := grpc.NewSkillServer("skill-gitlab", "1.0.0")

	// Project executors
	server.RegisterExecutorWithSchema("gitlab-project-list", &ProjectListExecutor{}, ProjectListSchema)
	server.RegisterExecutorWithSchema("gitlab-project-get", &ProjectGetExecutor{}, ProjectGetSchema)

	// Pipeline executors
	server.RegisterExecutorWithSchema("gitlab-pipeline-trigger", &PipelineTriggerExecutor{}, PipelineTriggerSchema)
	server.RegisterExecutorWithSchema("gitlab-pipeline-status", &PipelineStatusExecutor{}, PipelineStatusSchema)

	// Job executors
	server.RegisterExecutorWithSchema("gitlab-job-list", &JobListExecutor{}, JobListSchema)
	server.RegisterExecutorWithSchema("gitlab-job-logs", &JobLogsExecutor{}, JobLogsSchema)

	// Merge Request executors
	server.RegisterExecutorWithSchema("gitlab-mr-list", &MRListExecutor{}, MRListSchema)
	server.RegisterExecutorWithSchema("gitlab-mr-create", &MRCreateExecutor{}, MRCreateSchema)
	server.RegisterExecutorWithSchema("gitlab-mr-merge", &MRMergeExecutor{}, MRMergeSchema)

	// Issue executors
	server.RegisterExecutorWithSchema("gitlab-issue-list", &IssueListExecutor{}, IssueListSchema)
	server.RegisterExecutorWithSchema("gitlab-issue-create", &IssueCreateExecutor{}, IssueCreateSchema)

	// Variable executors
	server.RegisterExecutorWithSchema("gitlab-variable-list", &VariableListExecutor{}, VariableListSchema)
	server.RegisterExecutorWithSchema("gitlab-variable-set", &VariableSetExecutor{}, VariableSetSchema)

	// Repository file executor
	server.RegisterExecutorWithSchema("gitlab-repo-file", &RepoFileExecutor{}, RepoFileSchema)

	fmt.Printf("Starting skill-gitlab gRPC server on port %s\n", port)
	if err := server.Serve(port); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to serve: %v\n", err)
		os.Exit(1)
	}
}

// ============================================================================
// GITLAB CLIENT HELPERS
// ============================================================================

// getGitLabClient returns a GitLab client (cached)
func getGitLabClient(token, baseURL string) (*gitlab.Client, error) {
	if token == "" {
		return nil, fmt.Errorf("GitLab token is required")
	}

	cacheKey := fmt.Sprintf("%s:%s", token, baseURL)

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

	var err error
	if baseURL != "" {
		client, err = gitlab.NewClient(token, gitlab.WithBaseURL(baseURL))
	} else {
		client, err = gitlab.NewClient(token)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to create GitLab client: %w", err)
	}

	clients[cacheKey] = client
	baseURLs[cacheKey] = baseURL
	return client, nil
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

// Helper to get interface slice from config
func getInterfaceSlice(config map[string]interface{}, key string) []interface{} {
	if v, ok := config[key]; ok {
		if arr, ok := v.([]interface{}); ok {
			return arr
		}
	}
	return nil
}

// parseProjectID parses project ID which can be numeric or URL-encoded path
func parseProjectID(projectID string) interface{} {
	// If it's a numeric string, convert to int
	var intVal int
	if _, err := fmt.Sscanf(projectID, "%d", &intVal); err == nil {
		return intVal
	}
	// Otherwise return as string (URL-encoded path)
	return projectID
}

// ============================================================================
// SCHEMAS
// ============================================================================

// ProjectListSchema is the UI schema for gitlab-project-list
var ProjectListSchema = resolver.NewSchemaBuilder("gitlab-project-list").
	WithName("List GitLab Projects").
	WithCategory("action").
	WithIcon(iconGitLab).
	WithDescription("List projects in GitLab").
	AddSection("Connection").
		AddExpressionField("token", "Access Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("glpat-..."),
			resolver.WithHint("GitLab personal access token (supports {{bindings.xxx}})"),
			resolver.WithSensitive(),
		).
		AddExpressionField("baseURL", "Base URL",
			resolver.WithPlaceholder("https://gitlab.com"),
			resolver.WithHint("Optional: Custom GitLab instance URL"),
		).
		EndSection().
	AddSection("Filters").
		AddExpressionField("search", "Search",
			resolver.WithPlaceholder("my-project"),
			resolver.WithHint("Search projects by name"),
		).
		AddExpressionField("visibility", "Visibility",
			resolver.WithPlaceholder("public, internal, private"),
			resolver.WithHint("Filter by visibility level"),
		).
		AddExpressionField("membership", "Only My Projects",
			resolver.WithPlaceholder("true"),
			resolver.WithHint("Limit to projects the user is a member of"),
		).
		EndSection().
	AddSection("Options").
		AddNumberField("perPage", "Per Page",
			resolver.WithDefault(20),
			resolver.WithMinMax(1, 100),
			resolver.WithHint("Number of projects per page"),
		).
		AddNumberField("page", "Page",
			resolver.WithDefault(1),
			resolver.WithMinMax(1, 100),
			resolver.WithHint("Page number"),
		).
		EndSection().
	Build()

// ProjectGetSchema is the UI schema for gitlab-project-get
var ProjectGetSchema = resolver.NewSchemaBuilder("gitlab-project-get").
	WithName("Get GitLab Project").
	WithCategory("action").
	WithIcon(iconGitLab).
	WithDescription("Get details of a specific GitLab project").
	AddSection("Connection").
		AddExpressionField("token", "Access Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("glpat-..."),
			resolver.WithHint("GitLab personal access token"),
			resolver.WithSensitive(),
		).
		AddExpressionField("baseURL", "Base URL",
			resolver.WithPlaceholder("https://gitlab.com"),
			resolver.WithHint("Optional: Custom GitLab instance URL"),
		).
		EndSection().
	AddSection("Project").
		AddExpressionField("projectID", "Project ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("group/project or 123"),
			resolver.WithHint("Project ID or URL-encoded path (e.g., group%2Fproject)"),
		).
		EndSection().
	Build()

// PipelineTriggerSchema is the UI schema for gitlab-pipeline-trigger
var PipelineTriggerSchema = resolver.NewSchemaBuilder("gitlab-pipeline-trigger").
	WithName("Trigger GitLab Pipeline").
	WithCategory("action").
	WithIcon(iconGitLab).
	WithDescription("Trigger a CI/CD pipeline in GitLab").
	AddSection("Connection").
		AddExpressionField("token", "Access Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("glpat-..."),
			resolver.WithHint("GitLab personal access token or trigger token"),
			resolver.WithSensitive(),
		).
		AddExpressionField("baseURL", "Base URL",
			resolver.WithPlaceholder("https://gitlab.com"),
			resolver.WithHint("Optional: Custom GitLab instance URL"),
		).
		EndSection().
	AddSection("Pipeline").
		AddExpressionField("projectID", "Project ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("group/project or 123"),
			resolver.WithHint("Project ID or URL-encoded path"),
		).
		AddExpressionField("ref", "Ref/Branch",
			resolver.WithRequired(),
			resolver.WithPlaceholder("main"),
			resolver.WithHint("Branch, tag, or commit SHA to run pipeline on"),
		).
		AddExpressionField("triggerToken", "Trigger Token",
			resolver.WithPlaceholder("trigger-token"),
			resolver.WithHint("Optional: Pipeline trigger token (for public triggers)"),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Variables").
		AddKeyValueField("variables", "Variables",
			resolver.WithHint("CI/CD variables to pass to the pipeline"),
		).
		EndSection().
	Build()

// PipelineStatusSchema is the UI schema for gitlab-pipeline-status
var PipelineStatusSchema = resolver.NewSchemaBuilder("gitlab-pipeline-status").
	WithName("Get Pipeline Status").
	WithCategory("action").
	WithIcon(iconGitLab).
	WithDescription("Get the status of a GitLab pipeline").
	AddSection("Connection").
		AddExpressionField("token", "Access Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("glpat-..."),
			resolver.WithHint("GitLab personal access token"),
			resolver.WithSensitive(),
		).
		AddExpressionField("baseURL", "Base URL",
			resolver.WithPlaceholder("https://gitlab.com"),
			resolver.WithHint("Optional: Custom GitLab instance URL"),
		).
		EndSection().
	AddSection("Pipeline").
		AddExpressionField("projectID", "Project ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("group/project or 123"),
			resolver.WithHint("Project ID or URL-encoded path"),
		).
		AddExpressionField("pipelineID", "Pipeline ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("12345"),
			resolver.WithHint("Pipeline ID to get status for"),
		).
		EndSection().
	Build()

// JobListSchema is the UI schema for gitlab-job-list
var JobListSchema = resolver.NewSchemaBuilder("gitlab-job-list").
	WithName("List Pipeline Jobs").
	WithCategory("action").
	WithIcon(iconGitLab).
	WithDescription("List jobs in a GitLab pipeline").
	AddSection("Connection").
		AddExpressionField("token", "Access Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("glpat-..."),
			resolver.WithHint("GitLab personal access token"),
			resolver.WithSensitive(),
		).
		AddExpressionField("baseURL", "Base URL",
			resolver.WithPlaceholder("https://gitlab.com"),
			resolver.WithHint("Optional: Custom GitLab instance URL"),
		).
		EndSection().
	AddSection("Pipeline").
		AddExpressionField("projectID", "Project ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("group/project or 123"),
			resolver.WithHint("Project ID or URL-encoded path"),
		).
		AddExpressionField("pipelineID", "Pipeline ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("12345"),
			resolver.WithHint("Pipeline ID to list jobs for"),
		).
		EndSection().
	AddSection("Filters").
		AddExpressionField("scope", "Scope",
			resolver.WithPlaceholder("running, pending, success, failed"),
			resolver.WithHint("Filter jobs by scope (comma-separated)"),
		).
		EndSection().
	Build()

// JobLogsSchema is the UI schema for gitlab-job-logs
var JobLogsSchema = resolver.NewSchemaBuilder("gitlab-job-logs").
	WithName("Get Job Logs").
	WithCategory("action").
	WithIcon(iconGitLab).
	WithDescription("Get logs from a GitLab CI/CD job").
	AddSection("Connection").
		AddExpressionField("token", "Access Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("glpat-..."),
			resolver.WithHint("GitLab personal access token"),
			resolver.WithSensitive(),
		).
		AddExpressionField("baseURL", "Base URL",
			resolver.WithPlaceholder("https://gitlab.com"),
			resolver.WithHint("Optional: Custom GitLab instance URL"),
		).
		EndSection().
	AddSection("Job").
		AddExpressionField("projectID", "Project ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("group/project or 123"),
			resolver.WithHint("Project ID or URL-encoded path"),
		).
		AddExpressionField("jobID", "Job ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("12345"),
			resolver.WithHint("Job ID to get logs for"),
		).
		EndSection().
	Build()

// MRListSchema is the UI schema for gitlab-mr-list
var MRListSchema = resolver.NewSchemaBuilder("gitlab-mr-list").
	WithName("List Merge Requests").
	WithCategory("action").
	WithIcon(iconGitLab).
	WithDescription("List merge requests in a GitLab project").
	AddSection("Connection").
		AddExpressionField("token", "Access Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("glpat-..."),
			resolver.WithHint("GitLab personal access token"),
			resolver.WithSensitive(),
		).
		AddExpressionField("baseURL", "Base URL",
			resolver.WithPlaceholder("https://gitlab.com"),
			resolver.WithHint("Optional: Custom GitLab instance URL"),
		).
		EndSection().
	AddSection("Project").
		AddExpressionField("projectID", "Project ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("group/project or 123"),
			resolver.WithHint("Project ID or URL-encoded path"),
		).
		EndSection().
	AddSection("Filters").
		AddExpressionField("state", "State",
			resolver.WithPlaceholder("opened, merged, closed"),
			resolver.WithHint("Filter by state"),
		).
		AddExpressionField("scope", "Scope",
			resolver.WithPlaceholder("created-by-me, assigned-to-me, all"),
			resolver.WithHint("Filter by scope"),
		).
		AddExpressionField("authorID", "Author ID",
			resolver.WithPlaceholder("123"),
			resolver.WithHint("Filter by author ID"),
		).
		AddExpressionField("reviewerID", "Reviewer ID",
			resolver.WithPlaceholder("123"),
			resolver.WithHint("Filter by reviewer ID"),
		).
		EndSection().
	AddSection("Options").
		AddNumberField("perPage", "Per Page",
			resolver.WithDefault(20),
			resolver.WithMinMax(1, 100),
			resolver.WithHint("Number of MRs per page"),
		).
		EndSection().
	Build()

// MRCreateSchema is the UI schema for gitlab-mr-create
var MRCreateSchema = resolver.NewSchemaBuilder("gitlab-mr-create").
	WithName("Create Merge Request").
	WithCategory("action").
	WithIcon(iconGitLab).
	WithDescription("Create a new merge request in GitLab").
	AddSection("Connection").
		AddExpressionField("token", "Access Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("glpat-..."),
			resolver.WithHint("GitLab personal access token"),
			resolver.WithSensitive(),
		).
		AddExpressionField("baseURL", "Base URL",
			resolver.WithPlaceholder("https://gitlab.com"),
			resolver.WithHint("Optional: Custom GitLab instance URL"),
		).
		EndSection().
	AddSection("Merge Request").
		AddExpressionField("projectID", "Project ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("group/project or 123"),
			resolver.WithHint("Project ID or URL-encoded path"),
		).
		AddExpressionField("title", "Title",
			resolver.WithRequired(),
			resolver.WithPlaceholder("Fix bug in feature X"),
			resolver.WithHint("MR title"),
		).
		AddExpressionField("description", "Description",
			resolver.WithPlaceholder("This MR fixes..."),
			resolver.WithHint("MR description (markdown supported)"),
		).
		AddExpressionField("sourceBranch", "Source Branch",
			resolver.WithRequired(),
			resolver.WithPlaceholder("feature-branch"),
			resolver.WithHint("Branch to merge from"),
		).
		AddExpressionField("targetBranch", "Target Branch",
			resolver.WithRequired(),
			resolver.WithPlaceholder("main"),
			resolver.WithHint("Branch to merge into"),
		).
		EndSection().
	AddSection("Options").
		AddExpressionField("assigneeID", "Assignee ID",
			resolver.WithPlaceholder("123"),
			resolver.WithHint("User ID to assign the MR to"),
		).
		AddExpressionField("reviewerIDs", "Reviewer IDs",
			resolver.WithPlaceholder("123,456"),
			resolver.WithHint("Comma-separated user IDs to set as reviewers"),
		).
		AddExpressionField("labels", "Labels",
			resolver.WithPlaceholder("bug,enhancement"),
			resolver.WithHint("Comma-separated labels"),
		).
		AddToggleField("removeSourceBranch", "Remove Source Branch",
			resolver.WithDefault(false),
			resolver.WithHint("Remove source branch on merge"),
		).
		AddToggleField("squash", "Squash Commits",
			resolver.WithDefault(false),
			resolver.WithHint("Squash commits into a single commit on merge"),
		).
		EndSection().
	Build()

// MRMergeSchema is the UI schema for gitlab-mr-merge
var MRMergeSchema = resolver.NewSchemaBuilder("gitlab-mr-merge").
	WithName("Merge Merge Request").
	WithCategory("action").
	WithIcon(iconGitLab).
	WithDescription("Merge a merge request in GitLab").
	AddSection("Connection").
		AddExpressionField("token", "Access Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("glpat-..."),
			resolver.WithHint("GitLab personal access token"),
			resolver.WithSensitive(),
		).
		AddExpressionField("baseURL", "Base URL",
			resolver.WithPlaceholder("https://gitlab.com"),
			resolver.WithHint("Optional: Custom GitLab instance URL"),
		).
		EndSection().
	AddSection("Merge Request").
		AddExpressionField("projectID", "Project ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("group/project or 123"),
			resolver.WithHint("Project ID or URL-encoded path"),
		).
		AddExpressionField("mrIID", "Merge Request IID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("123"),
			resolver.WithHint("Internal ID of the merge request"),
		).
		EndSection().
	AddSection("Options").
		AddExpressionField("mergeCommitMessage", "Merge Commit Message",
			resolver.WithPlaceholder("Custom merge commit message"),
			resolver.WithHint("Custom merge commit message"),
		).
		AddSelectField("mergeMethod", "Merge Method",
			[]resolver.SelectOption{
				{Label: "Merge", Value: "merge"},
				{Label: "Rebase Merge", Value: "rebase_merge"},
				{Label: "Fast Forward", Value: "ff"},
			},
			resolver.WithDefault("merge"),
			resolver.WithHint("Merge method to use"),
		).
		AddToggleField("shouldRemoveSourceBranch", "Remove Source Branch",
			resolver.WithDefault(false),
			resolver.WithHint("Remove source branch after merge"),
		).
		AddToggleField("squash", "Squash",
			resolver.WithDefault(false),
			resolver.WithHint("Squash commits into a single commit"),
		).
		EndSection().
	Build()

// IssueListSchema is the UI schema for gitlab-issue-list
var IssueListSchema = resolver.NewSchemaBuilder("gitlab-issue-list").
	WithName("List Issues").
	WithCategory("action").
	WithIcon(iconGitLab).
	WithDescription("List issues in a GitLab project").
	AddSection("Connection").
		AddExpressionField("token", "Access Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("glpat-..."),
			resolver.WithHint("GitLab personal access token"),
			resolver.WithSensitive(),
		).
		AddExpressionField("baseURL", "Base URL",
			resolver.WithPlaceholder("https://gitlab.com"),
			resolver.WithHint("Optional: Custom GitLab instance URL"),
		).
		EndSection().
	AddSection("Project").
		AddExpressionField("projectID", "Project ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("group/project or 123"),
			resolver.WithHint("Project ID or URL-encoded path"),
		).
		EndSection().
	AddSection("Filters").
		AddExpressionField("state", "State",
			resolver.WithPlaceholder("opened, closed, all"),
			resolver.WithHint("Filter by state"),
		).
		AddExpressionField("labels", "Labels",
			resolver.WithPlaceholder("bug,enhancement"),
			resolver.WithHint("Comma-separated labels to filter by"),
		).
		AddExpressionField("milestone", "Milestone",
			resolver.WithPlaceholder("v1.0"),
			resolver.WithHint("Filter by milestone title"),
		).
		AddExpressionField("authorID", "Author ID",
			resolver.WithPlaceholder("123"),
			resolver.WithHint("Filter by author ID"),
		).
		AddExpressionField("assigneeID", "Assignee ID",
			resolver.WithPlaceholder("123"),
			resolver.WithHint("Filter by assignee ID"),
		).
		EndSection().
	AddSection("Options").
		AddNumberField("perPage", "Per Page",
			resolver.WithDefault(20),
			resolver.WithMinMax(1, 100),
			resolver.WithHint("Number of issues per page"),
		).
		EndSection().
	Build()

// IssueCreateSchema is the UI schema for gitlab-issue-create
var IssueCreateSchema = resolver.NewSchemaBuilder("gitlab-issue-create").
	WithName("Create Issue").
	WithCategory("action").
	WithIcon(iconGitLab).
	WithDescription("Create a new issue in GitLab").
	AddSection("Connection").
		AddExpressionField("token", "Access Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("glpat-..."),
			resolver.WithHint("GitLab personal access token"),
			resolver.WithSensitive(),
		).
		AddExpressionField("baseURL", "Base URL",
			resolver.WithPlaceholder("https://gitlab.com"),
			resolver.WithHint("Optional: Custom GitLab instance URL"),
		).
		EndSection().
	AddSection("Issue").
		AddExpressionField("projectID", "Project ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("group/project or 123"),
			resolver.WithHint("Project ID or URL-encoded path"),
		).
		AddExpressionField("title", "Title",
			resolver.WithRequired(),
			resolver.WithPlaceholder("Bug: Something is broken"),
			resolver.WithHint("Issue title"),
		).
		AddExpressionField("description", "Description",
			resolver.WithPlaceholder("Steps to reproduce..."),
			resolver.WithHint("Issue description (markdown supported)"),
		).
		EndSection().
	AddSection("Options").
		AddExpressionField("labels", "Labels",
			resolver.WithPlaceholder("bug,critical"),
			resolver.WithHint("Comma-separated labels"),
		).
		AddExpressionField("milestoneID", "Milestone ID",
			resolver.WithPlaceholder("123"),
			resolver.WithHint("Milestone ID to assign"),
		).
		AddExpressionField("assigneeIDs", "Assignee IDs",
			resolver.WithPlaceholder("123,456"),
			resolver.WithHint("Comma-separated user IDs to assign"),
		).
		AddExpressionField("confidential", "Confidential",
			resolver.WithPlaceholder("false"),
			resolver.WithHint("Make issue confidential"),
		).
		EndSection().
	Build()

// VariableListSchema is the UI schema for gitlab-variable-list
var VariableListSchema = resolver.NewSchemaBuilder("gitlab-variable-list").
	WithName("List CI/CD Variables").
	WithCategory("action").
	WithIcon(iconGitLab).
	WithDescription("List CI/CD variables in a GitLab project").
	AddSection("Connection").
		AddExpressionField("token", "Access Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("glpat-..."),
			resolver.WithHint("GitLab personal access token"),
			resolver.WithSensitive(),
		).
		AddExpressionField("baseURL", "Base URL",
			resolver.WithPlaceholder("https://gitlab.com"),
			resolver.WithHint("Optional: Custom GitLab instance URL"),
		).
		EndSection().
	AddSection("Project").
		AddExpressionField("projectID", "Project ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("group/project or 123"),
			resolver.WithHint("Project ID or URL-encoded path"),
		).
		EndSection().
	AddSection("Options").
		AddExpressionField("environmentScope", "Environment Scope",
			resolver.WithPlaceholder("*"),
			resolver.WithHint("Filter by environment scope"),
		).
		EndSection().
	Build()

// VariableSetSchema is the UI schema for gitlab-variable-set
var VariableSetSchema = resolver.NewSchemaBuilder("gitlab-variable-set").
	WithName("Set CI/CD Variable").
	WithCategory("action").
	WithIcon(iconGitLab).
	WithDescription("Set a CI/CD variable in a GitLab project").
	AddSection("Connection").
		AddExpressionField("token", "Access Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("glpat-..."),
			resolver.WithHint("GitLab personal access token"),
			resolver.WithSensitive(),
		).
		AddExpressionField("baseURL", "Base URL",
			resolver.WithPlaceholder("https://gitlab.com"),
			resolver.WithHint("Optional: Custom GitLab instance URL"),
		).
		EndSection().
	AddSection("Project").
		AddExpressionField("projectID", "Project ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("group/project or 123"),
			resolver.WithHint("Project ID or URL-encoded path"),
		).
		EndSection().
	AddSection("Variable").
		AddExpressionField("key", "Key",
			resolver.WithRequired(),
			resolver.WithPlaceholder("MY_VARIABLE"),
			resolver.WithHint("Variable key/name"),
		).
		AddExpressionField("value", "Value",
			resolver.WithRequired(),
			resolver.WithPlaceholder("secret-value"),
			resolver.WithHint("Variable value"),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Options").
		AddExpressionField("variableType", "Type",
			resolver.WithPlaceholder("env_var"),
			resolver.WithHint("Variable type: env_var or file"),
		).
		AddExpressionField("protected", "Protected",
			resolver.WithPlaceholder("false"),
			resolver.WithHint("Restrict to protected branches/tags"),
		).
		AddExpressionField("masked", "Masked",
			resolver.WithPlaceholder("false"),
			resolver.WithHint("Mask variable in job logs"),
		).
		AddExpressionField("environmentScope", "Environment Scope",
			resolver.WithPlaceholder("*"),
			resolver.WithHint("Environment scope"),
		).
		AddExpressionField("description", "Description",
			resolver.WithPlaceholder("My variable description"),
			resolver.WithHint("Variable description"),
		).
		EndSection().
	Build()

// RepoFileSchema is the UI schema for gitlab-repo-file
var RepoFileSchema = resolver.NewSchemaBuilder("gitlab-repo-file").
	WithName("Repository File Operations").
	WithCategory("action").
	WithIcon(iconGitLab).
	WithDescription("Get, create, update, or delete files in a GitLab repository").
	AddSection("Connection").
		AddExpressionField("token", "Access Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("glpat-..."),
			resolver.WithHint("GitLab personal access token"),
			resolver.WithSensitive(),
		).
		AddExpressionField("baseURL", "Base URL",
			resolver.WithPlaceholder("https://gitlab.com"),
			resolver.WithHint("Optional: Custom GitLab instance URL"),
		).
		EndSection().
	AddSection("Repository").
		AddExpressionField("projectID", "Project ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("group/project or 123"),
			resolver.WithHint("Project ID or URL-encoded path"),
		).
		AddExpressionField("filePath", "File Path",
			resolver.WithRequired(),
			resolver.WithPlaceholder("path/to/file.txt"),
			resolver.WithHint("Path to the file in the repository"),
		).
		AddExpressionField("ref", "Ref/Branch",
			resolver.WithPlaceholder("main"),
			resolver.WithHint("Branch, tag, or commit SHA"),
		).
		EndSection().
	AddSection("Action").
		AddSelectField("action", "Action",
			[]resolver.SelectOption{
				{Label: "Get File", Value: "get"},
				{Label: "Create File", Value: "create"},
				{Label: "Update File", Value: "update"},
				{Label: "Delete File", Value: "delete"},
			},
			resolver.WithDefault("get"),
			resolver.WithHint("Action to perform on the file"),
		).
		AddExpressionField("content", "Content",
			resolver.WithPlaceholder("File content..."),
			resolver.WithHint("File content (for create/update actions)"),
		).
		AddExpressionField("commitMessage", "Commit Message",
			resolver.WithPlaceholder("Update file via API"),
			resolver.WithHint("Commit message (for create/update/delete actions)"),
		).
		AddExpressionField("branch", "Branch",
			resolver.WithPlaceholder("main"),
			resolver.WithHint("Branch to commit to (for create/update/delete)"),
		).
		EndSection().
	Build()

// ============================================================================
// PROJECT EXECUTORS
// ============================================================================

// ProjectListExecutor handles gitlab-project-list
type ProjectListExecutor struct{}

func (e *ProjectListExecutor) Type() string { return "gitlab-project-list" }

func (e *ProjectListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	token := resolver.ResolveString(getString(config, "token"))
	baseURL := resolver.ResolveString(getString(config, "baseURL"))

	client, err := getGitLabClient(token, baseURL)
	if err != nil {
		return nil, err
	}

	search := resolver.ResolveString(getString(config, "search"))
	visibility := resolver.ResolveString(getString(config, "visibility"))
	membership := getBool(config, "membership", false)
	perPage := getInt(config, "perPage", 20)
	page := getInt(config, "page", 1)

	opts := &gitlab.ListProjectsOptions{
		ListOptions: gitlab.ListOptions{
			PerPage: perPage,
			Page:    page,
		},
	}

	if search != "" {
		opts.Search = gitlab.Ptr(search)
	}
	if visibility != "" {
		var vis gitlab.VisibilityValue
		switch strings.ToLower(visibility) {
		case "public":
			vis = gitlab.PublicVisibility
		case "internal":
			vis = gitlab.InternalVisibility
		case "private":
			vis = gitlab.PrivateVisibility
		}
		opts.Visibility = &vis
	}
	if membership {
		opts.Membership = gitlab.Ptr(true)
	}

	projects, _, err := client.Projects.ListProjects(opts)
	if err != nil {
		return nil, fmt.Errorf("failed to list projects: %w", err)
	}

	var results []map[string]interface{}
	for _, p := range projects {
		results = append(results, map[string]interface{}{
			"id":                p.ID,
			"name":              p.Name,
			"path":              p.Path,
			"pathWithNamespace": p.PathWithNamespace,
			"description":       p.Description,
			"webURL":            p.WebURL,
			"visibility":        string(p.Visibility),
			"defaultBranch":     p.DefaultBranch,
			"archived":          p.Archived,
			"createdAt":         p.CreatedAt,
			"lastActivityAt":    p.LastActivityAt,
		})
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"projects": results,
			"count":    len(results),
		},
	}, nil
}

// ProjectGetExecutor handles gitlab-project-get
type ProjectGetExecutor struct{}

func (e *ProjectGetExecutor) Type() string { return "gitlab-project-get" }

func (e *ProjectGetExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	token := resolver.ResolveString(getString(config, "token"))
	baseURL := resolver.ResolveString(getString(config, "baseURL"))
	projectID := resolver.ResolveString(getString(config, "projectID"))

	client, err := getGitLabClient(token, baseURL)
	if err != nil {
		return nil, err
	}

	project, _, err := client.Projects.GetProject(parseProjectID(projectID), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get project: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"id":                   project.ID,
			"name":                 project.Name,
			"path":                 project.Path,
			"pathWithNamespace":    project.PathWithNamespace,
			"description":          project.Description,
			"webURL":               project.WebURL,
			"visibility":           string(project.Visibility),
			"defaultBranch":        project.DefaultBranch,
			"archived":             project.Archived,
			"createdAt":            project.CreatedAt,
			"lastActivityAt":       project.LastActivityAt,
			"sshURLToRepo":         project.SSHURLToRepo,
			"httpURLToRepo":        project.HTTPURLToRepo,
			"namespace":            project.Namespace,
			"topics":               project.Topics,
			"starCount":            project.StarCount,
			"forksCount":           project.ForksCount,
			"openIssuesCount":      project.OpenIssuesCount,
			"jobsEnabled":          project.JobsEnabled,
			"mergeRequestsEnabled": project.MergeRequestsEnabled,
		},
	}, nil
}

// ============================================================================
// PIPELINE EXECUTORS
// ============================================================================

// PipelineTriggerExecutor handles gitlab-pipeline-trigger
type PipelineTriggerExecutor struct{}

func (e *PipelineTriggerExecutor) Type() string { return "gitlab-pipeline-trigger" }

func (e *PipelineTriggerExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	token := resolver.ResolveString(getString(config, "token"))
	baseURL := resolver.ResolveString(getString(config, "baseURL"))
	projectID := resolver.ResolveString(getString(config, "projectID"))
	ref := resolver.ResolveString(getString(config, "ref"))

	client, err := getGitLabClient(token, baseURL)
	if err != nil {
		return nil, err
	}

	// Build variables in GitLab's expected format
	var variables []*gitlab.PipelineVariableOptions
	if vars := getMap(config, "variables"); vars != nil {
		for k, v := range vars {
			if s, ok := v.(string); ok {
				variables = append(variables, &gitlab.PipelineVariableOptions{
					Key:   gitlab.Ptr(k),
					Value: gitlab.Ptr(resolver.ResolveString(s)),
				})
			}
		}
	}

	// Use CreatePipeline to trigger a new pipeline
	opts := &gitlab.CreatePipelineOptions{
		Ref: gitlab.Ptr(ref),
	}
	if len(variables) > 0 {
		opts.Variables = &variables
	}
	pipeline, _, err := client.Pipelines.CreatePipeline(parseProjectID(projectID), opts)

	if err != nil {
		return nil, fmt.Errorf("failed to trigger pipeline: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"id":         pipeline.ID,
			"iid":        pipeline.IID,
			"projectID":  pipeline.ProjectID,
			"status":     pipeline.Status,
			"ref":        pipeline.Ref,
			"sha":        pipeline.SHA,
			"webURL":     pipeline.WebURL,
			"createdAt":  pipeline.CreatedAt,
			"updatedAt":  pipeline.UpdatedAt,
			"startedAt":  pipeline.StartedAt,
			"finishedAt": pipeline.FinishedAt,
		},
	}, nil
}

// PipelineStatusExecutor handles gitlab-pipeline-status
type PipelineStatusExecutor struct{}

func (e *PipelineStatusExecutor) Type() string { return "gitlab-pipeline-status" }

func (e *PipelineStatusExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	token := resolver.ResolveString(getString(config, "token"))
	baseURL := resolver.ResolveString(getString(config, "baseURL"))
	projectID := resolver.ResolveString(getString(config, "projectID"))
	pipelineID := getInt(config, "pipelineID", 0)

	client, err := getGitLabClient(token, baseURL)
	if err != nil {
		return nil, err
	}

	pipeline, _, err := client.Pipelines.GetPipeline(parseProjectID(projectID), pipelineID)
	if err != nil {
		return nil, fmt.Errorf("failed to get pipeline: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"id":         pipeline.ID,
			"iid":        pipeline.IID,
			"projectID":  pipeline.ProjectID,
			"status":     pipeline.Status,
			"ref":        pipeline.Ref,
			"sha":        pipeline.SHA,
			"webURL":     pipeline.WebURL,
			"createdAt":  pipeline.CreatedAt,
			"updatedAt":  pipeline.UpdatedAt,
			"startedAt":  pipeline.StartedAt,
			"finishedAt": pipeline.FinishedAt,
			"duration":   pipeline.Duration,
		},
	}, nil
}

// ============================================================================
// JOB EXECUTORS
// ============================================================================

// JobListExecutor handles gitlab-job-list
type JobListExecutor struct{}

func (e *JobListExecutor) Type() string { return "gitlab-job-list" }

func (e *JobListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	token := resolver.ResolveString(getString(config, "token"))
	baseURL := resolver.ResolveString(getString(config, "baseURL"))
	projectID := resolver.ResolveString(getString(config, "projectID"))
	pipelineID := getInt(config, "pipelineID", 0)
	scope := getString(config, "scope")

	client, err := getGitLabClient(token, baseURL)
	if err != nil {
		return nil, err
	}

	opts := &gitlab.ListJobsOptions{}
	if scope != "" {
		scopes := strings.Split(scope, ",")
		var scopeValues []gitlab.BuildStateValue
		for _, s := range scopes {
			s = strings.TrimSpace(s)
			switch strings.ToLower(s) {
			case "running":
				scopeValues = append(scopeValues, gitlab.Running)
			case "pending":
				scopeValues = append(scopeValues, gitlab.Pending)
			case "success":
				scopeValues = append(scopeValues, gitlab.Success)
			case "failed":
				scopeValues = append(scopeValues, gitlab.Failed)
			case "created":
				scopeValues = append(scopeValues, gitlab.Created)
			case "preparing":
				scopeValues = append(scopeValues, gitlab.Preparing)
			case "scheduled":
				scopeValues = append(scopeValues, gitlab.Scheduled)
			case "waiting_for_resource":
				scopeValues = append(scopeValues, gitlab.WaitingForResource)
			case "canceled":
				scopeValues = append(scopeValues, gitlab.Canceled)
			case "skipped":
				scopeValues = append(scopeValues, gitlab.Skipped)
			case "manual":
				scopeValues = append(scopeValues, gitlab.Manual)
			default:
				scopeValues = append(scopeValues, gitlab.BuildStateValue(s))
			}
		}
		opts.Scope = &scopeValues
	}

	jobs, _, err := client.Jobs.ListPipelineJobs(parseProjectID(projectID), pipelineID, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to list jobs: %w", err)
	}

	var results []map[string]interface{}
	for _, job := range jobs {
		jobResult := map[string]interface{}{
			"id":         job.ID,
			"name":       job.Name,
			"status":     job.Status,
			"stage":      job.Stage,
			"ref":        job.Ref,
			"tag":        job.Tag,
			"webURL":     job.WebURL,
			"createdAt":  job.CreatedAt,
			"startedAt":  job.StartedAt,
			"finishedAt": job.FinishedAt,
			"duration":   job.Duration,
			"runner": map[string]interface{}{
				"id":          job.Runner.ID,
				"description": job.Runner.Description,
			},
		}
		results = append(results, jobResult)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"jobs":  results,
			"count": len(results),
		},
	}, nil
}

// JobLogsExecutor handles gitlab-job-logs
type JobLogsExecutor struct{}

func (e *JobLogsExecutor) Type() string { return "gitlab-job-logs" }

func (e *JobLogsExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	token := resolver.ResolveString(getString(config, "token"))
	baseURL := resolver.ResolveString(getString(config, "baseURL"))
	projectID := resolver.ResolveString(getString(config, "projectID"))
	jobID := getInt(config, "jobID", 0)

	client, err := getGitLabClient(token, baseURL)
	if err != nil {
		return nil, err
	}

	// Get job logs - the API returns a bytes.Reader
	logs, _, err := client.Jobs.GetTraceFile(parseProjectID(projectID), jobID)
	if err != nil {
		return nil, fmt.Errorf("failed to get job logs: %w", err)
	}

	// Read logs from the response body
	logBytes := make([]byte, logs.Len())
	_, err = logs.Read(logBytes)
	if err != nil && err.Error() != "EOF" {
		// Read may return EOF which is expected
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"jobID":     jobID,
			"logs":      string(logBytes),
			"logsBytes": len(logBytes),
		},
	}, nil
}

// ============================================================================
// MERGE REQUEST EXECUTORS
// ============================================================================

// MRListExecutor handles gitlab-mr-list
type MRListExecutor struct{}

func (e *MRListExecutor) Type() string { return "gitlab-mr-list" }

func (e *MRListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	token := resolver.ResolveString(getString(config, "token"))
	baseURL := resolver.ResolveString(getString(config, "baseURL"))
	projectID := resolver.ResolveString(getString(config, "projectID"))
	state := resolver.ResolveString(getString(config, "state"))
	scope := resolver.ResolveString(getString(config, "scope"))
	authorID := getInt(config, "authorID", 0)
	reviewerID := getInt(config, "reviewerID", 0)
	perPage := getInt(config, "perPage", 20)

	client, err := getGitLabClient(token, baseURL)
	if err != nil {
		return nil, err
	}

	opts := &gitlab.ListProjectMergeRequestsOptions{
		ListOptions: gitlab.ListOptions{
			PerPage: perPage,
		},
	}

	if state != "" {
		opts.State = gitlab.Ptr(state)
	}
	if scope != "" {
		opts.Scope = gitlab.Ptr(scope)
	}
	if authorID > 0 {
		opts.AuthorID = gitlab.Ptr(authorID)
	}
	if reviewerID > 0 {
		// Note: Reviewer filtering may not be supported in all GitLab versions
		_ = reviewerID
	}

	mrs, _, err := client.MergeRequests.ListProjectMergeRequests(parseProjectID(projectID), opts)
	if err != nil {
		return nil, fmt.Errorf("failed to list merge requests: %w", err)
	}

	var results []map[string]interface{}
	for _, mr := range mrs {
		results = append(results, map[string]interface{}{
			"id":            mr.ID,
			"iid":           mr.IID,
			"title":         mr.Title,
			"description":   mr.Description,
			"state":         mr.State,
			"webURL":        mr.WebURL,
			"sourceBranch":  mr.SourceBranch,
			"targetBranch":  mr.TargetBranch,
			"author": map[string]interface{}{
				"id":       mr.Author.ID,
				"name":     mr.Author.Name,
				"username": mr.Author.Username,
			},
			"createdAt":   mr.CreatedAt,
			"updatedAt":   mr.UpdatedAt,
			"mergedAt":    mr.MergedAt,
			"closedAt":    mr.ClosedAt,
			"mergeStatus": mr.MergeStatus,
			"sha":         mr.SHA,
		})
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"mergeRequests": results,
			"count":         len(results),
		},
	}, nil
}

// MRCreateExecutor handles gitlab-mr-create
type MRCreateExecutor struct{}

func (e *MRCreateExecutor) Type() string { return "gitlab-mr-create" }

func (e *MRCreateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	token := resolver.ResolveString(getString(config, "token"))
	baseURL := resolver.ResolveString(getString(config, "baseURL"))
	projectID := resolver.ResolveString(getString(config, "projectID"))
	title := resolver.ResolveString(getString(config, "title"))
	description := resolver.ResolveString(getString(config, "description"))
	sourceBranch := resolver.ResolveString(getString(config, "sourceBranch"))
	targetBranch := resolver.ResolveString(getString(config, "targetBranch"))
	assigneeID := getInt(config, "assigneeID", 0)
	reviewerIDs := getStringSlice(config, "reviewerIDs")
	labels := getStringSlice(config, "labels")
	removeSourceBranch := getBool(config, "removeSourceBranch", false)
	squash := getBool(config, "squash", false)

	client, err := getGitLabClient(token, baseURL)
	if err != nil {
		return nil, err
	}

	opts := &gitlab.CreateMergeRequestOptions{
		Title:              gitlab.Ptr(title),
		Description:        gitlab.Ptr(description),
		SourceBranch:       gitlab.Ptr(sourceBranch),
		TargetBranch:       gitlab.Ptr(targetBranch),
		RemoveSourceBranch: gitlab.Ptr(removeSourceBranch),
		Squash:             gitlab.Ptr(squash),
	}

	if assigneeID > 0 {
		opts.AssigneeID = gitlab.Ptr(assigneeID)
	}
	if len(reviewerIDs) > 0 {
		var ids []int
		for _, id := range reviewerIDs {
			var intID int
			if _, err := fmt.Sscanf(id, "%d", &intID); err == nil {
				ids = append(ids, intID)
			}
		}
		if len(ids) > 0 {
			opts.ReviewerIDs = &ids
		}
	}
	if len(labels) > 0 {
		var labelsSlice gitlab.LabelOptions
		for _, l := range labels {
			labelsSlice = append(labelsSlice, l)
		}
		opts.Labels = &labelsSlice
	}

	mr, _, err := client.MergeRequests.CreateMergeRequest(parseProjectID(projectID), opts)
	if err != nil {
		return nil, fmt.Errorf("failed to create merge request: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"id":           mr.ID,
			"iid":          mr.IID,
			"title":        mr.Title,
			"description":  mr.Description,
			"state":        mr.State,
			"webURL":       mr.WebURL,
			"sourceBranch": mr.SourceBranch,
			"targetBranch": mr.TargetBranch,
			"createdAt":    mr.CreatedAt,
			"updatedAt":    mr.UpdatedAt,
			"mergeStatus":  mr.MergeStatus,
		},
	}, nil
}

// MRMergeExecutor handles gitlab-mr-merge
type MRMergeExecutor struct{}

func (e *MRMergeExecutor) Type() string { return "gitlab-mr-merge" }

func (e *MRMergeExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	token := resolver.ResolveString(getString(config, "token"))
	baseURL := resolver.ResolveString(getString(config, "baseURL"))
	projectID := resolver.ResolveString(getString(config, "projectID"))
	mrIID := getInt(config, "mrIID", 0)
	mergeCommitMessage := resolver.ResolveString(getString(config, "mergeCommitMessage"))
	shouldRemoveSourceBranch := getBool(config, "shouldRemoveSourceBranch", false)
	squash := getBool(config, "squash", false)

	client, err := getGitLabClient(token, baseURL)
	if err != nil {
		return nil, err
	}

	opts := &gitlab.AcceptMergeRequestOptions{}

	if mergeCommitMessage != "" {
		opts.MergeCommitMessage = gitlab.Ptr(mergeCommitMessage)
	}
	opts.ShouldRemoveSourceBranch = gitlab.Ptr(shouldRemoveSourceBranch)
	opts.Squash = gitlab.Ptr(squash)

	result, _, err := client.MergeRequests.AcceptMergeRequest(parseProjectID(projectID), mrIID, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to merge merge request: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"id":           result.ID,
			"iid":          result.IID,
			"title":        result.Title,
			"state":        result.State,
			"merged":       true,
			"mergedAt":     result.MergedAt,
			"mergedBy": map[string]interface{}{
				"id":       result.MergedBy.ID,
				"name":     result.MergedBy.Name,
				"username": result.MergedBy.Username,
			},
			"webURL": result.WebURL,
		},
	}, nil
}

// ============================================================================
// ISSUE EXECUTORS
// ============================================================================

// IssueListExecutor handles gitlab-issue-list
type IssueListExecutor struct{}

func (e *IssueListExecutor) Type() string { return "gitlab-issue-list" }

func (e *IssueListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	token := resolver.ResolveString(getString(config, "token"))
	baseURL := resolver.ResolveString(getString(config, "baseURL"))
	projectID := resolver.ResolveString(getString(config, "projectID"))
	state := resolver.ResolveString(getString(config, "state"))
	labels := getStringSlice(config, "labels")
	milestone := resolver.ResolveString(getString(config, "milestone"))
	authorID := getInt(config, "authorID", 0)
	assigneeID := getInt(config, "assigneeID", 0)
	perPage := getInt(config, "perPage", 20)

	client, err := getGitLabClient(token, baseURL)
	if err != nil {
		return nil, err
	}

	opts := &gitlab.ListProjectIssuesOptions{
		ListOptions: gitlab.ListOptions{
			PerPage: perPage,
		},
	}

	if state != "" {
		opts.State = gitlab.Ptr(state)
	}
	if len(labels) > 0 {
		var labelsSlice gitlab.LabelOptions
		for _, l := range labels {
			labelsSlice = append(labelsSlice, l)
		}
		opts.Labels = &labelsSlice
	}
	if milestone != "" {
		opts.Milestone = gitlab.Ptr(milestone)
	}
	if authorID > 0 {
		opts.AuthorID = gitlab.Ptr(authorID)
	}
	if assigneeID > 0 {
		// Note: Assignee filtering by ID may need different approach
		_ = assigneeID
	}

	issues, _, err := client.Issues.ListProjectIssues(parseProjectID(projectID), opts)
	if err != nil {
		return nil, fmt.Errorf("failed to list issues: %w", err)
	}

	var results []map[string]interface{}
	for _, issue := range issues {
		author := map[string]interface{}{
			"id":       issue.Author.ID,
			"name":     issue.Author.Name,
			"username": issue.Author.Username,
		}
		if issue.Assignee != nil {
			author["assignee"] = map[string]interface{}{
				"id":       issue.Assignee.ID,
				"name":     issue.Assignee.Name,
				"username": issue.Assignee.Username,
			}
		}

		results = append(results, map[string]interface{}{
			"id":          issue.ID,
			"iid":         issue.IID,
			"title":       issue.Title,
			"description": issue.Description,
			"state":       issue.State,
			"webURL":      issue.WebURL,
			"author":      author,
			"labels":      issue.Labels,
			"milestone":   issue.Milestone,
			"createdAt":   issue.CreatedAt,
			"updatedAt":   issue.UpdatedAt,
			"closedAt":    issue.ClosedAt,
			"confidential": issue.Confidential,
		})
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"issues": results,
			"count":  len(results),
		},
	}, nil
}

// IssueCreateExecutor handles gitlab-issue-create
type IssueCreateExecutor struct{}

func (e *IssueCreateExecutor) Type() string { return "gitlab-issue-create" }

func (e *IssueCreateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	token := resolver.ResolveString(getString(config, "token"))
	baseURL := resolver.ResolveString(getString(config, "baseURL"))
	projectID := resolver.ResolveString(getString(config, "projectID"))
	title := resolver.ResolveString(getString(config, "title"))
	description := resolver.ResolveString(getString(config, "description"))
	labels := getStringSlice(config, "labels")
	milestoneID := getInt(config, "milestoneID", 0)
	assigneeIDs := getStringSlice(config, "assigneeIDs")
	confidential := getBool(config, "confidential", false)

	client, err := getGitLabClient(token, baseURL)
	if err != nil {
		return nil, err
	}

	opts := &gitlab.CreateIssueOptions{
		Title:        gitlab.Ptr(title),
		Description:  gitlab.Ptr(description),
		Confidential: gitlab.Ptr(confidential),
	}

	if len(labels) > 0 {
		var labelsSlice gitlab.LabelOptions
		for _, l := range labels {
			labelsSlice = append(labelsSlice, l)
		}
		opts.Labels = &labelsSlice
	}
	if milestoneID > 0 {
		opts.MilestoneID = gitlab.Ptr(milestoneID)
	}
	if len(assigneeIDs) > 0 {
		var ids []int
		for _, id := range assigneeIDs {
			var intID int
			if _, err := fmt.Sscanf(id, "%d", &intID); err == nil {
				ids = append(ids, intID)
			}
		}
		if len(ids) > 0 {
			opts.AssigneeIDs = &ids
		}
	}

	issue, _, err := client.Issues.CreateIssue(parseProjectID(projectID), opts)
	if err != nil {
		return nil, fmt.Errorf("failed to create issue: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"id":          issue.ID,
			"iid":         issue.IID,
			"title":       issue.Title,
			"description": issue.Description,
			"state":       issue.State,
			"webURL":      issue.WebURL,
			"labels":      issue.Labels,
			"milestone":   issue.Milestone,
			"createdAt":   issue.CreatedAt,
			"updatedAt":   issue.UpdatedAt,
			"confidential": issue.Confidential,
		},
	}, nil
}

// ============================================================================
// VARIABLE EXECUTORS
// ============================================================================

// VariableListExecutor handles gitlab-variable-list
type VariableListExecutor struct{}

func (e *VariableListExecutor) Type() string { return "gitlab-variable-list" }

func (e *VariableListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	token := resolver.ResolveString(getString(config, "token"))
	baseURL := resolver.ResolveString(getString(config, "baseURL"))
	projectID := resolver.ResolveString(getString(config, "projectID"))

	client, err := getGitLabClient(token, baseURL)
	if err != nil {
		return nil, err
	}

	opts := &gitlab.ListProjectVariablesOptions{}

	variables, _, err := client.ProjectVariables.ListVariables(parseProjectID(projectID), opts)
	if err != nil {
		return nil, fmt.Errorf("failed to list variables: %w", err)
	}

	var results []map[string]interface{}
	for _, v := range variables {
		results = append(results, map[string]interface{}{
			"key":              v.Key,
			"value":            v.Value,
			"variableType":     string(v.VariableType),
			"protected":        v.Protected,
			"masked":           v.Masked,
			"environmentScope": v.EnvironmentScope,
			"description":      v.Description,
		})
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"variables": results,
			"count":     len(results),
		},
	}, nil
}

// VariableSetExecutor handles gitlab-variable-set
type VariableSetExecutor struct{}

func (e *VariableSetExecutor) Type() string { return "gitlab-variable-set" }

func (e *VariableSetExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	token := resolver.ResolveString(getString(config, "token"))
	baseURL := resolver.ResolveString(getString(config, "baseURL"))
	projectID := resolver.ResolveString(getString(config, "projectID"))
	key := resolver.ResolveString(getString(config, "key"))
	value := resolver.ResolveString(getString(config, "value"))
	variableType := resolver.ResolveString(getString(config, "variableType"))
	protected := getBool(config, "protected", false)
	masked := getBool(config, "masked", false)
	environmentScope := resolver.ResolveString(getString(config, "environmentScope"))
	description := resolver.ResolveString(getString(config, "description"))

	client, err := getGitLabClient(token, baseURL)
	if err != nil {
		return nil, err
	}

	opts := &gitlab.UpdateProjectVariableOptions{
		Value:       gitlab.Ptr(value),
		Protected:   gitlab.Ptr(protected),
		Masked:      gitlab.Ptr(masked),
		Description: gitlab.Ptr(description),
	}

	if variableType != "" {
		var vt gitlab.VariableTypeValue
		switch strings.ToLower(variableType) {
		case "env_var":
			vt = gitlab.EnvVariableType
		case "file":
			vt = gitlab.FileVariableType
		}
		opts.VariableType = &vt
	}
	if environmentScope != "" {
		opts.EnvironmentScope = gitlab.Ptr(environmentScope)
	}

	variable, _, err := client.ProjectVariables.UpdateVariable(parseProjectID(projectID), key, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to update/create variable: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"key":              variable.Key,
			"variableType":     string(variable.VariableType),
			"protected":        variable.Protected,
			"masked":           variable.Masked,
			"environmentScope": variable.EnvironmentScope,
			"description":      variable.Description,
		},
	}, nil
}

// ============================================================================
// REPOSITORY FILE EXECUTOR
// ============================================================================

// RepoFileExecutor handles gitlab-repo-file
type RepoFileExecutor struct{}

func (e *RepoFileExecutor) Type() string { return "gitlab-repo-file" }

func (e *RepoFileExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	token := resolver.ResolveString(getString(config, "token"))
	baseURL := resolver.ResolveString(getString(config, "baseURL"))
	projectID := resolver.ResolveString(getString(config, "projectID"))
	filePath := resolver.ResolveString(getString(config, "filePath"))
	ref := resolver.ResolveString(getString(config, "ref"))
	action := resolver.ResolveString(getString(config, "action"))
	content := resolver.ResolveString(getString(config, "content"))
	commitMessage := resolver.ResolveString(getString(config, "commitMessage"))
	branch := resolver.ResolveString(getString(config, "branch"))

	client, err := getGitLabClient(token, baseURL)
	if err != nil {
		return nil, err
	}

	switch strings.ToLower(action) {
	case "get":
		return e.getFile(client, projectID, filePath, ref)
	case "create":
		return e.createFile(client, projectID, filePath, branch, content, commitMessage)
	case "update":
		return e.updateFile(client, projectID, filePath, branch, content, commitMessage)
	case "delete":
		return e.deleteFile(client, projectID, filePath, branch, commitMessage)
	default:
		return nil, fmt.Errorf("unknown action: %s (valid: get, create, update, delete)", action)
	}
}

func (e *RepoFileExecutor) getFile(client *gitlab.Client, projectID, filePath, ref string) (*executor.StepResult, error) {
	file, _, err := client.RepositoryFiles.GetFile(parseProjectID(projectID), filePath, &gitlab.GetFileOptions{
		Ref: gitlab.Ptr(ref),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get file: %w", err)
	}

	// Decode content
	contentBytes, err := base64.StdEncoding.DecodeString(file.Content)
	if err != nil {
		return nil, fmt.Errorf("failed to decode file content: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"filePath":     file.FilePath,
			"branch":       file.Ref,
			"size":         file.Size,
			"encoding":     file.Encoding,
			"content":      string(contentBytes),
			"lastCommitID": file.LastCommitID,
			"blobID":       file.BlobID,
		},
	}, nil
}

func (e *RepoFileExecutor) createFile(client *gitlab.Client, projectID, filePath, branch, content, commitMessage string) (*executor.StepResult, error) {
	if commitMessage == "" {
		commitMessage = fmt.Sprintf("Create %s", filePath)
	}

	opts := &gitlab.CreateFileOptions{
		Branch:        gitlab.Ptr(branch),
		Content:       gitlab.Ptr(content),
		CommitMessage: gitlab.Ptr(commitMessage),
	}

	_, _, err := client.RepositoryFiles.CreateFile(parseProjectID(projectID), filePath, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to create file: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"filePath":      filePath,
			"branch":        branch,
			"created":       true,
			"commitMessage": commitMessage,
		},
	}, nil
}

func (e *RepoFileExecutor) updateFile(client *gitlab.Client, projectID, filePath, branch, content, commitMessage string) (*executor.StepResult, error) {
	if commitMessage == "" {
		commitMessage = fmt.Sprintf("Update %s", filePath)
	}

	opts := &gitlab.UpdateFileOptions{
		Branch:        gitlab.Ptr(branch),
		Content:       gitlab.Ptr(content),
		CommitMessage: gitlab.Ptr(commitMessage),
	}

	_, _, err := client.RepositoryFiles.UpdateFile(parseProjectID(projectID), filePath, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to update file: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"filePath":      filePath,
			"branch":        branch,
			"updated":       true,
			"commitMessage": commitMessage,
		},
	}, nil
}

func (e *RepoFileExecutor) deleteFile(client *gitlab.Client, projectID, filePath, branch, commitMessage string) (*executor.StepResult, error) {
	if commitMessage == "" {
		commitMessage = fmt.Sprintf("Delete %s", filePath)
	}

	opts := &gitlab.DeleteFileOptions{
		Branch:        gitlab.Ptr(branch),
		CommitMessage: gitlab.Ptr(commitMessage),
	}

	_, err := client.RepositoryFiles.DeleteFile(parseProjectID(projectID), filePath, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to delete file: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"filePath": filePath,
			"branch":   branch,
			"deleted":  true,
		},
	}, nil
}
