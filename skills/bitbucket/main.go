package main

import (
	"context"
	"encoding/base64"
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
	iconBitbucket    = "git-branch"
	bitbucketAPIBase = "https://api.bitbucket.org/2.0"
)

// HTTP client with reasonable defaults
var httpClient = &http.Client{
	Timeout: 60 * time.Second,
	Transport: &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,
	},
}

// Bitbucket clients cache (stores base64 auth headers)
var (
	authCache   = make(map[string]string)
	authCacheMux sync.RWMutex
)

func main() {
	// Get port from env or use default
	port := os.Getenv("SKILL_PORT")
	if port == "" {
		port = "50125"
	}

	// Create skill server
	server := grpc.NewSkillServer("skill-bitbucket", "1.0.0")

	// Repository executors
	server.RegisterExecutorWithSchema("bitbucket-repo-list", &RepoListExecutor{}, RepoListSchema)
	server.RegisterExecutorWithSchema("bitbucket-repo-get", &RepoGetExecutor{}, RepoGetSchema)

	// Pull Request executors
	server.RegisterExecutorWithSchema("bitbucket-pr-list", &PRListExecutor{}, PRListSchema)
	server.RegisterExecutorWithSchema("bitbucket-pr-create", &PRCreateExecutor{}, PRCreateSchema)
	server.RegisterExecutorWithSchema("bitbucket-pr-merge", &PRMergeExecutor{}, PRMergeSchema)

	// Pipeline executors
	server.RegisterExecutorWithSchema("bitbucket-pipeline-trigger", &PipelineTriggerExecutor{}, PipelineTriggerSchema)
	server.RegisterExecutorWithSchema("bitbucket-pipeline-status", &PipelineStatusExecutor{}, PipelineStatusSchema)

	// Commit executors
	server.RegisterExecutorWithSchema("bitbucket-commit-list", &CommitListExecutor{}, CommitListSchema)

	// Branch executors
	server.RegisterExecutorWithSchema("bitbucket-branch-list", &BranchListExecutor{}, BranchListSchema)

	fmt.Printf("Starting skill-bitbucket gRPC server on port %s\n", port)
	if err := server.Serve(port); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to serve: %v\n", err)
		os.Exit(1)
	}
}

// ============================================================================
// BITBUCKET CLIENT HELPERS
// ============================================================================

// getAuthHeader returns the Basic auth header value (cached)
func getAuthHeader(username, appPassword string) string {
	if username == "" || appPassword == "" {
		return ""
	}

	cacheKey := fmt.Sprintf("%s:%s", username, appPassword)

	authCacheMux.RLock()
	auth, ok := authCache[cacheKey]
	authCacheMux.RUnlock()

	if ok {
		return auth
	}

	authCacheMux.Lock()
	defer authCacheMux.Unlock()

	// Double check
	if auth, ok := authCache[cacheKey]; ok {
		return auth
	}

	// Create Basic auth header
	credentials := fmt.Sprintf("%s:%s", username, appPassword)
	auth = "Basic " + base64.StdEncoding.EncodeToString([]byte(credentials))
	authCache[cacheKey] = auth
	return auth
}

// bitbucketRequest makes an HTTP request to the Bitbucket API
func bitbucketRequest(ctx context.Context, authHeader, method, endpoint string, body interface{}) ([]byte, error) {
	if authHeader == "" {
		return nil, fmt.Errorf("Bitbucket authentication is required (username and app password)")
	}

	urlStr := bitbucketAPIBase + endpoint
	var req *http.Request
	var err error

	if body != nil {
		jsonData, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
		req, err = http.NewRequestWithContext(ctx, method, urlStr, strings.NewReader(string(jsonData)))
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
	} else {
		req, err = http.NewRequestWithContext(ctx, method, urlStr, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
		}
	}

	req.Header.Set("Authorization", authHeader)
	req.Header.Set("Accept", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("Bitbucket API error (%d): %s", resp.StatusCode, string(respBody))
	}

	return respBody, nil
}

// bitbucketGet makes a GET request with pagination support
func bitbucketGet(ctx context.Context, authHeader, endpoint string, paginated bool) ([]map[string]interface{}, error) {
	var allResults []map[string]interface{}
	nextURL := endpoint

	for nextURL != "" {
		respBody, err := bitbucketRequest(ctx, authHeader, "GET", nextURL, nil)
		if err != nil {
			return nil, err
		}

		var result map[string]interface{}
		if err := json.Unmarshal(respBody, &result); err != nil {
			return nil, fmt.Errorf("failed to parse response: %w", err)
		}

		// Extract values from paginated response
		if paginated {
			if values, ok := result["values"].([]interface{}); ok {
				for _, v := range values {
					if m, ok := v.(map[string]interface{}); ok {
						allResults = append(allResults, m)
					}
				}
			}
			// Check for next page
			if next, ok := result["next"].(string); ok && next != "" {
				// Parse the next URL to get just the path
				parsedURL, err := url.Parse(next)
				if err == nil {
					nextURL = parsedURL.Path
					if parsedURL.RawQuery != "" {
						nextURL += "?" + parsedURL.RawQuery
					}
				} else {
					nextURL = ""
				}
			} else {
				nextURL = ""
			}
		} else {
			allResults = append(allResults, result)
			nextURL = ""
		}
	}

	return allResults, nil
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

// ============================================================================
// SCHEMAS
// ============================================================================

// RepoListSchema is the UI schema for bitbucket-repo-list
var RepoListSchema = resolver.NewSchemaBuilder("bitbucket-repo-list").
	WithName("List Bitbucket Repositories").
	WithCategory("action").
	WithIcon(iconBitbucket).
	WithDescription("List repositories in a Bitbucket workspace").
	AddSection("Connection").
		AddExpressionField("username", "Username",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-username"),
			resolver.WithHint("Bitbucket username (supports {{bindings.xxx}})"),
		).
		AddExpressionField("appPassword", "App Password",
			resolver.WithRequired(),
			resolver.WithPlaceholder("ATBBxxx..."),
			resolver.WithHint("Bitbucket App Password (supports {{bindings.xxx}})"),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Workspace").
		AddExpressionField("workspace", "Workspace",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-workspace"),
			resolver.WithHint("Workspace slug or UUID"),
		).
		EndSection().
	AddSection("Filters").
		AddExpressionField("role", "Role Filter",
			resolver.WithPlaceholder("admin, contributor, member"),
			resolver.WithHint("Filter by user role"),
		).
		AddExpressionField("search", "Search",
			resolver.WithPlaceholder("my-repo"),
			resolver.WithHint("Search repositories by name"),
		).
		EndSection().
	AddSection("Options").
		AddNumberField("limit", "Limit",
			resolver.WithDefault(50),
			resolver.WithMinMax(1, 100),
			resolver.WithHint("Maximum number of repositories to return"),
		).
		EndSection().
	Build()

// RepoGetSchema is the UI schema for bitbucket-repo-get
var RepoGetSchema = resolver.NewSchemaBuilder("bitbucket-repo-get").
	WithName("Get Bitbucket Repository").
	WithCategory("action").
	WithIcon(iconBitbucket).
	WithDescription("Get details of a specific Bitbucket repository").
	AddSection("Connection").
		AddExpressionField("username", "Username",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-username"),
			resolver.WithHint("Bitbucket username"),
		).
		AddExpressionField("appPassword", "App Password",
			resolver.WithRequired(),
			resolver.WithPlaceholder("ATBBxxx..."),
			resolver.WithHint("Bitbucket App Password"),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Repository").
		AddExpressionField("workspace", "Workspace",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-workspace"),
			resolver.WithHint("Workspace slug or UUID"),
		).
		AddExpressionField("repoSlug", "Repository Slug",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-repo"),
			resolver.WithHint("Repository name/slug"),
		).
		EndSection().
	Build()

// PRListSchema is the UI schema for bitbucket-pr-list
var PRListSchema = resolver.NewSchemaBuilder("bitbucket-pr-list").
	WithName("List Pull Requests").
	WithCategory("action").
	WithIcon(iconBitbucket).
	WithDescription("List pull requests in a Bitbucket repository").
	AddSection("Connection").
		AddExpressionField("username", "Username",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-username"),
			resolver.WithHint("Bitbucket username"),
		).
		AddExpressionField("appPassword", "App Password",
			resolver.WithRequired(),
			resolver.WithPlaceholder("ATBBxxx..."),
			resolver.WithHint("Bitbucket App Password"),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Repository").
		AddExpressionField("workspace", "Workspace",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-workspace"),
			resolver.WithHint("Workspace slug or UUID"),
		).
		AddExpressionField("repoSlug", "Repository Slug",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-repo"),
			resolver.WithHint("Repository name/slug"),
		).
		EndSection().
	AddSection("Filters").
		AddExpressionField("state", "State",
			resolver.WithPlaceholder("OPEN, MERGED, DECLINED, SUPERSEDED"),
			resolver.WithHint("Filter by PR state (comma-separated)"),
		).
		AddExpressionField("author", "Author",
			resolver.WithPlaceholder("username"),
			resolver.WithHint("Filter by author username"),
		).
		AddExpressionField("reviewer", "Reviewer",
			resolver.WithPlaceholder("username"),
			resolver.WithHint("Filter by reviewer username"),
		).
		EndSection().
	AddSection("Options").
		AddNumberField("limit", "Limit",
			resolver.WithDefault(50),
			resolver.WithMinMax(1, 100),
			resolver.WithHint("Maximum number of pull requests to return"),
		).
		EndSection().
	Build()

// PRCreateSchema is the UI schema for bitbucket-pr-create
var PRCreateSchema = resolver.NewSchemaBuilder("bitbucket-pr-create").
	WithName("Create Pull Request").
	WithCategory("action").
	WithIcon(iconBitbucket).
	WithDescription("Create a new pull request in Bitbucket").
	AddSection("Connection").
		AddExpressionField("username", "Username",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-username"),
			resolver.WithHint("Bitbucket username"),
		).
		AddExpressionField("appPassword", "App Password",
			resolver.WithRequired(),
			resolver.WithPlaceholder("ATBBxxx..."),
			resolver.WithHint("Bitbucket App Password"),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Repository").
		AddExpressionField("workspace", "Workspace",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-workspace"),
			resolver.WithHint("Workspace slug or UUID"),
		).
		AddExpressionField("repoSlug", "Repository Slug",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-repo"),
			resolver.WithHint("Repository name/slug"),
		).
		EndSection().
	AddSection("Pull Request Details").
		AddExpressionField("title", "Title",
			resolver.WithRequired(),
			resolver.WithPlaceholder("Add new feature"),
			resolver.WithHint("Pull request title"),
		).
		AddTextareaField("description", "Description",
			resolver.WithRows(6),
			resolver.WithPlaceholder("Describe your changes..."),
			resolver.WithHint("Pull request description (supports Markdown)"),
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
	AddSection("Reviewers").
		AddExpressionField("reviewers", "Reviewers",
			resolver.WithPlaceholder("user1,user2"),
			resolver.WithHint("Comma-separated list of reviewer usernames"),
		).
		EndSection().
	Build()

// PRMergeSchema is the UI schema for bitbucket-pr-merge
var PRMergeSchema = resolver.NewSchemaBuilder("bitbucket-pr-merge").
	WithName("Merge Pull Request").
	WithCategory("action").
	WithIcon(iconBitbucket).
	WithDescription("Merge a pull request in Bitbucket").
	AddSection("Connection").
		AddExpressionField("username", "Username",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-username"),
			resolver.WithHint("Bitbucket username"),
		).
		AddExpressionField("appPassword", "App Password",
			resolver.WithRequired(),
			resolver.WithPlaceholder("ATBBxxx..."),
			resolver.WithHint("Bitbucket App Password"),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Repository").
		AddExpressionField("workspace", "Workspace",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-workspace"),
			resolver.WithHint("Workspace slug or UUID"),
		).
		AddExpressionField("repoSlug", "Repository Slug",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-repo"),
			resolver.WithHint("Repository name/slug"),
		).
		AddNumberField("prId", "Pull Request ID",
			resolver.WithRequired(),
			resolver.WithMinMax(1, 999999),
			resolver.WithHint("The pull request ID to merge"),
		).
		EndSection().
	AddSection("Merge Options").
		AddSelectField("mergeStrategy", "Merge Strategy",
			[]resolver.SelectOption{
				{Label: "Merge Commit", Value: "merge_commit"},
				{Label: "Squash", Value: "squash"},
				{Label: "Fast Forward", Value: "fast_forward"},
			},
			resolver.WithDefault("merge_commit"),
			resolver.WithHint("Strategy to use for merging"),
		).
		AddExpressionField("closeSourceBranch", "Close Source Branch",
			resolver.WithPlaceholder("true"),
			resolver.WithHint("Set to 'true' to close the source branch after merge"),
		).
		EndSection().
	Build()

// PipelineTriggerSchema is the UI schema for bitbucket-pipeline-trigger
var PipelineTriggerSchema = resolver.NewSchemaBuilder("bitbucket-pipeline-trigger").
	WithName("Trigger Pipeline").
	WithCategory("action").
	WithIcon(iconBitbucket).
	WithDescription("Trigger a CI/CD pipeline in Bitbucket").
	AddSection("Connection").
		AddExpressionField("username", "Username",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-username"),
			resolver.WithHint("Bitbucket username"),
		).
		AddExpressionField("appPassword", "App Password",
			resolver.WithRequired(),
			resolver.WithPlaceholder("ATBBxxx..."),
			resolver.WithHint("Bitbucket App Password with pipelines:write permission"),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Repository").
		AddExpressionField("workspace", "Workspace",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-workspace"),
			resolver.WithHint("Workspace slug or UUID"),
		).
		AddExpressionField("repoSlug", "Repository Slug",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-repo"),
			resolver.WithHint("Repository name/slug"),
		).
		EndSection().
	AddSection("Pipeline").
		AddExpressionField("branch", "Branch",
			resolver.WithRequired(),
			resolver.WithPlaceholder("main"),
			resolver.WithHint("Branch to run the pipeline on"),
		).
		AddExpressionField("commit", "Commit",
			resolver.WithPlaceholder("HEAD"),
			resolver.WithHint("Specific commit SHA (optional, defaults to HEAD)"),
		).
		EndSection().
	AddSection("Variables").
		AddKeyValueField("variables", "Pipeline Variables",
			resolver.WithHint("Custom variables to pass to the pipeline"),
		).
		EndSection().
	Build()

// PipelineStatusSchema is the UI schema for bitbucket-pipeline-status
var PipelineStatusSchema = resolver.NewSchemaBuilder("bitbucket-pipeline-status").
	WithName("Get Pipeline Status").
	WithCategory("action").
	WithIcon(iconBitbucket).
	WithDescription("Get the status of a Bitbucket pipeline").
	AddSection("Connection").
		AddExpressionField("username", "Username",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-username"),
			resolver.WithHint("Bitbucket username"),
		).
		AddExpressionField("appPassword", "App Password",
			resolver.WithRequired(),
			resolver.WithPlaceholder("ATBBxxx..."),
			resolver.WithHint("Bitbucket App Password"),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Repository").
		AddExpressionField("workspace", "Workspace",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-workspace"),
			resolver.WithHint("Workspace slug or UUID"),
		).
		AddExpressionField("repoSlug", "Repository Slug",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-repo"),
			resolver.WithHint("Repository name/slug"),
		).
		AddExpressionField("pipelineId", "Pipeline ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("{pipeline-uuid}"),
			resolver.WithHint("Pipeline UUID (with or without braces)"),
		).
		EndSection().
	Build()

// CommitListSchema is the UI schema for bitbucket-commit-list
var CommitListSchema = resolver.NewSchemaBuilder("bitbucket-commit-list").
	WithName("List Commits").
	WithCategory("action").
	WithIcon(iconBitbucket).
	WithDescription("List commits in a Bitbucket repository").
	AddSection("Connection").
		AddExpressionField("username", "Username",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-username"),
			resolver.WithHint("Bitbucket username"),
		).
		AddExpressionField("appPassword", "App Password",
			resolver.WithRequired(),
			resolver.WithPlaceholder("ATBBxxx..."),
			resolver.WithHint("Bitbucket App Password"),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Repository").
		AddExpressionField("workspace", "Workspace",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-workspace"),
			resolver.WithHint("Workspace slug or UUID"),
		).
		AddExpressionField("repoSlug", "Repository Slug",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-repo"),
			resolver.WithHint("Repository name/slug"),
		).
		EndSection().
	AddSection("Filters").
		AddExpressionField("branch", "Branch",
			resolver.WithPlaceholder("main"),
			resolver.WithHint("Filter commits by branch"),
		).
		AddExpressionField("author", "Author",
			resolver.WithPlaceholder("username"),
			resolver.WithHint("Filter by author username"),
		).
		EndSection().
	AddSection("Options").
		AddNumberField("limit", "Limit",
			resolver.WithDefault(50),
			resolver.WithMinMax(1, 100),
			resolver.WithHint("Maximum number of commits to return"),
		).
		EndSection().
	Build()

// BranchListSchema is the UI schema for bitbucket-branch-list
var BranchListSchema = resolver.NewSchemaBuilder("bitbucket-branch-list").
	WithName("List Branches").
	WithCategory("action").
	WithIcon(iconBitbucket).
	WithDescription("List branches in a Bitbucket repository").
	AddSection("Connection").
		AddExpressionField("username", "Username",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-username"),
			resolver.WithHint("Bitbucket username"),
		).
		AddExpressionField("appPassword", "App Password",
			resolver.WithRequired(),
			resolver.WithPlaceholder("ATBBxxx..."),
			resolver.WithHint("Bitbucket App Password"),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Repository").
		AddExpressionField("workspace", "Workspace",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-workspace"),
			resolver.WithHint("Workspace slug or UUID"),
		).
		AddExpressionField("repoSlug", "Repository Slug",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-repo"),
			resolver.WithHint("Repository name/slug"),
		).
		EndSection().
	AddSection("Options").
		AddNumberField("limit", "Limit",
			resolver.WithDefault(50),
			resolver.WithMinMax(1, 100),
			resolver.WithHint("Maximum number of branches to return"),
		).
		EndSection().
	Build()

// ============================================================================
// REPOSITORY EXECUTORS
// ============================================================================

// RepoListExecutor handles bitbucket-repo-list node type
type RepoListExecutor struct{}

func (e *RepoListExecutor) Type() string { return "bitbucket-repo-list" }

func (e *RepoListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	username := getString(step.Config, "username")
	appPassword := getString(step.Config, "appPassword")
	workspace := getString(step.Config, "workspace")
	role := getString(step.Config, "role")
	search := getString(step.Config, "search")
	limit := getInt(step.Config, "limit", 50)

	if username == "" || appPassword == "" {
		return nil, fmt.Errorf("username and appPassword are required")
	}
	if workspace == "" {
		return nil, fmt.Errorf("workspace is required")
	}

	authHeader := getAuthHeader(username, appPassword)

	// Build endpoint with filters
	endpoint := fmt.Sprintf("/repositories/%s", workspace)
	params := []string{}
	if role != "" {
		params = append(params, fmt.Sprintf("role=%s", url.QueryEscape(role)))
	}
	if search != "" {
		params = append(params, fmt.Sprintf("q=name~%q", search))
	}
	params = append(params, fmt.Sprintf("pagelen=%d", limit))

	if len(params) > 0 {
		endpoint += "?" + strings.Join(params, "&")
	}

	repos, err := bitbucketGet(ctx, authHeader, endpoint, true)
	if err != nil {
		return nil, fmt.Errorf("failed to list repositories: %w", err)
	}

	// Limit results
	if len(repos) > limit {
		repos = repos[:limit]
	}

	// Format output
	output := []map[string]interface{}{}
	for _, repo := range repos {
		output = append(output, formatRepo(repo))
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"repositories": output,
			"count":        len(output),
		},
	}, nil
}

// RepoGetExecutor handles bitbucket-repo-get node type
type RepoGetExecutor struct{}

func (e *RepoGetExecutor) Type() string { return "bitbucket-repo-get" }

func (e *RepoGetExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	username := getString(step.Config, "username")
	appPassword := getString(step.Config, "appPassword")
	workspace := getString(step.Config, "workspace")
	repoSlug := getString(step.Config, "repoSlug")

	if username == "" || appPassword == "" {
		return nil, fmt.Errorf("username and appPassword are required")
	}
	if workspace == "" {
		return nil, fmt.Errorf("workspace is required")
	}
	if repoSlug == "" {
		return nil, fmt.Errorf("repoSlug is required")
	}

	authHeader := getAuthHeader(username, appPassword)
	endpoint := fmt.Sprintf("/repositories/%s/%s", workspace, repoSlug)

	repos, err := bitbucketGet(ctx, authHeader, endpoint, false)
	if err != nil {
		return nil, fmt.Errorf("failed to get repository: %w", err)
	}

	if len(repos) == 0 {
		return nil, fmt.Errorf("repository not found")
	}

	repo := formatRepo(repos[0])

	return &executor.StepResult{
		Output: map[string]interface{}{
			"repository": repo,
		},
	}, nil
}

// formatRepo formats a repository response
func formatRepo(repo map[string]interface{}) map[string]interface{} {
	result := map[string]interface{}{
		"name":        getStringMapValue(repo, "name"),
		"slug":        getStringMapValue(repo, "slug"),
		"fullName":    getStringMapValue(repo, "full_name"),
		"description": getStringMapValue(repo, "description"),
		"language":    getStringMapValue(repo, "language"),
		"isPrivate":   getBoolMapValue(repo, "is_private"),
		"createdAt":   getStringMapValue(repo, "created_on"),
		"updatedAt":   getStringMapValue(repo, "updated_on"),
	}

	// Extract owner info
	if owner, ok := repo["owner"].(map[string]interface{}); ok {
		result["owner"] = map[string]interface{}{
			"username":    getStringMapValue(owner, "username"),
			"displayName": getStringMapValue(owner, "display_name"),
			"type":        getStringMapValue(owner, "type"),
		}
	}

	// Extract main branch
	if mainBranch, ok := repo["mainbranch"].(map[string]interface{}); ok {
		result["mainBranch"] = getStringMapValue(mainBranch, "name")
	}

	// Extract links
	if links, ok := repo["links"].(map[string]interface{}); ok {
		result["links"] = formatLinks(links)
	}

	return result
}

// formatLinks formats repository links
func formatLinks(links map[string]interface{}) map[string]interface{} {
	result := map[string]interface{}{}
	for _, key := range []string{"html", "clone", "self", "avatar"} {
		if v, ok := links[key]; ok {
			result[key] = v
		}
	}
	return result
}

// ============================================================================
// PULL REQUEST EXECUTORS
// ============================================================================

// PRListExecutor handles bitbucket-pr-list node type
type PRListExecutor struct{}

func (e *PRListExecutor) Type() string { return "bitbucket-pr-list" }

func (e *PRListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	username := getString(step.Config, "username")
	appPassword := getString(step.Config, "appPassword")
	workspace := getString(step.Config, "workspace")
	repoSlug := getString(step.Config, "repoSlug")
	state := getString(step.Config, "state")
	author := getString(step.Config, "author")
	reviewer := getString(step.Config, "reviewer")
	limit := getInt(step.Config, "limit", 50)

	if username == "" || appPassword == "" {
		return nil, fmt.Errorf("username and appPassword are required")
	}
	if workspace == "" || repoSlug == "" {
		return nil, fmt.Errorf("workspace and repoSlug are required")
	}

	authHeader := getAuthHeader(username, appPassword)

	// Build endpoint with filters
	endpoint := fmt.Sprintf("/repositories/%s/%s/pullrequests", workspace, repoSlug)
	params := []string{fmt.Sprintf("pagelen=%d", limit)}

	if state != "" {
		params = append(params, fmt.Sprintf("state=%s", url.QueryEscape(state)))
	}

	// Build query for author/reviewer filters
	queries := []string{}
	if author != "" {
		queries = append(queries, fmt.Sprintf("author.username=%q", author))
	}
	if reviewer != "" {
		queries = append(queries, fmt.Sprintf("reviewers.username=%q", reviewer))
	}
	if len(queries) > 0 {
		params = append(params, "q="+url.QueryEscape(strings.Join(queries, " AND ")))
	}

	endpoint += "?" + strings.Join(params, "&")

	prs, err := bitbucketGet(ctx, authHeader, endpoint, true)
	if err != nil {
		return nil, fmt.Errorf("failed to list pull requests: %w", err)
	}

	// Limit results
	if len(prs) > limit {
		prs = prs[:limit]
	}

	// Format output
	output := []map[string]interface{}{}
	for _, pr := range prs {
		output = append(output, formatPR(pr))
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"pullRequests": output,
			"count":        len(output),
		},
	}, nil
}

// PRCreateExecutor handles bitbucket-pr-create node type
type PRCreateExecutor struct{}

func (e *PRCreateExecutor) Type() string { return "bitbucket-pr-create" }

func (e *PRCreateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	username := getString(step.Config, "username")
	appPassword := getString(step.Config, "appPassword")
	workspace := getString(step.Config, "workspace")
	repoSlug := getString(step.Config, "repoSlug")
	title := getString(step.Config, "title")
	description := getString(step.Config, "description")
	sourceBranch := getString(step.Config, "sourceBranch")
	targetBranch := getString(step.Config, "targetBranch")
	reviewers := getStringSlice(step.Config, "reviewers")

	if username == "" || appPassword == "" {
		return nil, fmt.Errorf("username and appPassword are required")
	}
	if workspace == "" || repoSlug == "" {
		return nil, fmt.Errorf("workspace and repoSlug are required")
	}
	if title == "" {
		return nil, fmt.Errorf("title is required")
	}
	if sourceBranch == "" || targetBranch == "" {
		return nil, fmt.Errorf("sourceBranch and targetBranch are required")
	}

	authHeader := getAuthHeader(username, appPassword)

	// Build request body
	body := map[string]interface{}{
		"title":       title,
		"description": description,
		"source": map[string]interface{}{
			"branch": map[string]interface{}{
				"name": sourceBranch,
			},
		},
		"destination": map[string]interface{}{
			"branch": map[string]interface{}{
				"name": targetBranch,
			},
		},
	}

	// Add reviewers if specified
	if len(reviewers) > 0 {
		reviewerList := []map[string]interface{}{}
		for _, r := range reviewers {
			if r != "" {
				reviewerList = append(reviewerList, map[string]interface{}{
					"username": r,
				})
			}
		}
		body["reviewers"] = reviewerList
	}

	endpoint := fmt.Sprintf("/repositories/%s/%s/pullrequests", workspace, repoSlug)
	respBody, err := bitbucketRequest(ctx, authHeader, "POST", endpoint, body)
	if err != nil {
		return nil, fmt.Errorf("failed to create pull request: %w", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	pr := formatPR(result)

	return &executor.StepResult{
		Output: map[string]interface{}{
			"pullRequest": pr,
			"id":          pr["id"],
			"links":       pr["links"],
		},
	}, nil
}

// PRMergeExecutor handles bitbucket-pr-merge node type
type PRMergeExecutor struct{}

func (e *PRMergeExecutor) Type() string { return "bitbucket-pr-merge" }

func (e *PRMergeExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	username := getString(step.Config, "username")
	appPassword := getString(step.Config, "appPassword")
	workspace := getString(step.Config, "workspace")
	repoSlug := getString(step.Config, "repoSlug")
	prId := getInt(step.Config, "prId", 0)
	mergeStrategy := getString(step.Config, "mergeStrategy")
	closeSourceBranch := getString(step.Config, "closeSourceBranch")

	if username == "" || appPassword == "" {
		return nil, fmt.Errorf("username and appPassword are required")
	}
	if workspace == "" || repoSlug == "" {
		return nil, fmt.Errorf("workspace and repoSlug are required")
	}
	if prId == 0 {
		return nil, fmt.Errorf("prId is required")
	}

	authHeader := getAuthHeader(username, appPassword)

	// Build request body
	body := map[string]interface{}{
		"close_source_branch": closeSourceBranch == "true" || closeSourceBranch == "1",
	}
	if mergeStrategy != "" {
		body["merge_strategy"] = mergeStrategy
	}

	endpoint := fmt.Sprintf("/repositories/%s/%s/pullrequests/%d/merge", workspace, repoSlug, prId)
	respBody, err := bitbucketRequest(ctx, authHeader, "POST", endpoint, body)
	if err != nil {
		return nil, fmt.Errorf("failed to merge pull request: %w", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	pr := formatPR(result)

	return &executor.StepResult{
		Output: map[string]interface{}{
			"pullRequest": pr,
			"merged":      true,
			"state":       pr["state"],
		},
	}, nil
}

// formatPR formats a pull request response
func formatPR(pr map[string]interface{}) map[string]interface{} {
	result := map[string]interface{}{
		"id":          getFloat64MapValue(pr, "id"),
		"title":       getStringMapValue(pr, "title"),
		"description": getStringMapValue(pr, "description"),
		"state":       getStringMapValue(pr, "state"),
		"createdAt":   getStringMapValue(pr, "created_on"),
		"updatedAt":   getStringMapValue(pr, "updated_on"),
	}

	// Extract author
	if author, ok := pr["author"].(map[string]interface{}); ok {
		result["author"] = map[string]interface{}{
			"username":    getStringMapValue(author, "username"),
			"displayName": getStringMapValue(author, "display_name"),
		}
	}

	// Extract source branch
	if source, ok := pr["source"].(map[string]interface{}); ok {
		if branch, ok := source["branch"].(map[string]interface{}); ok {
			result["sourceBranch"] = getStringMapValue(branch, "name")
		}
		if commit, ok := source["commit"].(map[string]interface{}); ok {
			result["sourceCommit"] = getStringMapValue(commit, "hash")
		}
	}

	// Extract destination branch
	if dest, ok := pr["destination"].(map[string]interface{}); ok {
		if branch, ok := dest["branch"].(map[string]interface{}); ok {
			result["targetBranch"] = getStringMapValue(branch, "name")
		}
		if commit, ok := dest["commit"].(map[string]interface{}); ok {
			result["targetCommit"] = getStringMapValue(commit, "hash")
		}
	}

	// Extract reviewers
	if reviewers, ok := pr["reviewers"].([]interface{}); ok {
		reviewerList := []map[string]interface{}{}
		for _, r := range reviewers {
			if rm, ok := r.(map[string]interface{}); ok {
				reviewerList = append(reviewerList, map[string]interface{}{
					"username":    getStringMapValue(rm, "username"),
					"displayName": getStringMapValue(rm, "display_name"),
				})
			}
		}
		result["reviewers"] = reviewerList
	}

	// Extract links
	if links, ok := pr["links"].(map[string]interface{}); ok {
		result["links"] = formatLinks(links)
	}

	return result
}

// ============================================================================
// PIPELINE EXECUTORS
// ============================================================================

// PipelineTriggerExecutor handles bitbucket-pipeline-trigger node type
type PipelineTriggerExecutor struct{}

func (e *PipelineTriggerExecutor) Type() string { return "bitbucket-pipeline-trigger" }

func (e *PipelineTriggerExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	username := getString(step.Config, "username")
	appPassword := getString(step.Config, "appPassword")
	workspace := getString(step.Config, "workspace")
	repoSlug := getString(step.Config, "repoSlug")
	branch := getString(step.Config, "branch")
	commit := getString(step.Config, "commit")
	variables := getMap(step.Config, "variables")

	if username == "" || appPassword == "" {
		return nil, fmt.Errorf("username and appPassword are required")
	}
	if workspace == "" || repoSlug == "" {
		return nil, fmt.Errorf("workspace and repoSlug are required")
	}
	if branch == "" {
		return nil, fmt.Errorf("branch is required")
	}

	authHeader := getAuthHeader(username, appPassword)

	// Build request body
	body := map[string]interface{}{
		"target": map[string]interface{}{
			"ref_type": "branch",
			"ref_name": branch,
			"selector": map[string]interface{}{
				"type": "branches",
			},
		},
	}

	if commit != "" && commit != "HEAD" {
		body["target"].(map[string]interface{})["commit"] = map[string]interface{}{
			"hash": commit,
		}
	}

	// Add variables if specified
	if len(variables) > 0 {
		varList := []map[string]interface{}{}
		for k, v := range variables {
			if vs, ok := v.(string); ok {
				varList = append(varList, map[string]interface{}{
					"key":   k,
					"value": vs,
				})
			}
		}
		if len(varList) > 0 {
			body["variables"] = varList
		}
	}

	endpoint := fmt.Sprintf("/repositories/%s/%s/pipelines/", workspace, repoSlug)
	respBody, err := bitbucketRequest(ctx, authHeader, "POST", endpoint, body)
	if err != nil {
		return nil, fmt.Errorf("failed to trigger pipeline: %w", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	pipeline := formatPipeline(result)

	return &executor.StepResult{
		Output: map[string]interface{}{
			"pipeline": pipeline,
			"id":       pipeline["id"],
			"state":    pipeline["state"],
		},
	}, nil
}

// PipelineStatusExecutor handles bitbucket-pipeline-status node type
type PipelineStatusExecutor struct{}

func (e *PipelineStatusExecutor) Type() string { return "bitbucket-pipeline-status" }

func (e *PipelineStatusExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	username := getString(step.Config, "username")
	appPassword := getString(step.Config, "appPassword")
	workspace := getString(step.Config, "workspace")
	repoSlug := getString(step.Config, "repoSlug")
	pipelineId := getString(step.Config, "pipelineId")

	if username == "" || appPassword == "" {
		return nil, fmt.Errorf("username and appPassword are required")
	}
	if workspace == "" || repoSlug == "" {
		return nil, fmt.Errorf("workspace and repoSlug are required")
	}
	if pipelineId == "" {
		return nil, fmt.Errorf("pipelineId is required")
	}

	// Clean up pipeline ID (remove braces if present)
	pipelineId = strings.Trim(pipelineId, "{}")

	authHeader := getAuthHeader(username, appPassword)
	endpoint := fmt.Sprintf("/repositories/%s/%s/pipelines/%s", workspace, repoSlug, pipelineId)

	pipelines, err := bitbucketGet(ctx, authHeader, endpoint, false)
	if err != nil {
		return nil, fmt.Errorf("failed to get pipeline status: %w", err)
	}

	if len(pipelines) == 0 {
		return nil, fmt.Errorf("pipeline not found")
	}

	pipeline := formatPipeline(pipelines[0])

	return &executor.StepResult{
		Output: map[string]interface{}{
			"pipeline": pipeline,
			"state":    pipeline["state"],
			"status":   pipeline["status"],
		},
	}, nil
}

// formatPipeline formats a pipeline response
func formatPipeline(pipeline map[string]interface{}) map[string]interface{} {
	result := map[string]interface{}{
		"id":        getStringMapValue(pipeline, "uuid"),
		"buildNumber": getFloat64MapValue(pipeline, "build_number"),
		"state":     getStringMapValue(pipeline, "state"),
		"status":    getStringMapValue(pipeline, "state", "name"),
		"createdAt": getStringMapValue(pipeline, "created_on"),
		"completedAt": getStringMapValue(pipeline, "completed_on"),
	}

	// Extract target info
	if target, ok := pipeline["target"].(map[string]interface{}); ok {
		result["target"] = map[string]interface{}{
			"refType": getStringMapValue(target, "ref_type"),
			"refName": getStringMapValue(target, "ref_name"),
		}
		if commit, ok := target["commit"].(map[string]interface{}); ok {
			result["target"].(map[string]interface{})["commit"] = map[string]interface{}{
				"hash": getStringMapValue(commit, "hash"),
			}
		}
	}

	// Extract creator
	if creator, ok := pipeline["creator"].(map[string]interface{}); ok {
		result["creator"] = map[string]interface{}{
			"username":    getStringMapValue(creator, "username"),
			"displayName": getStringMapValue(creator, "display_name"),
		}
	}

	// Extract links
	if links, ok := pipeline["links"].(map[string]interface{}); ok {
		result["links"] = formatLinks(links)
	}

	return result
}

// ============================================================================
// COMMIT EXECUTORS
// ============================================================================

// CommitListExecutor handles bitbucket-commit-list node type
type CommitListExecutor struct{}

func (e *CommitListExecutor) Type() string { return "bitbucket-commit-list" }

func (e *CommitListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	username := getString(step.Config, "username")
	appPassword := getString(step.Config, "appPassword")
	workspace := getString(step.Config, "workspace")
	repoSlug := getString(step.Config, "repoSlug")
	branch := getString(step.Config, "branch")
	author := getString(step.Config, "author")
	limit := getInt(step.Config, "limit", 50)

	if username == "" || appPassword == "" {
		return nil, fmt.Errorf("username and appPassword are required")
	}
	if workspace == "" || repoSlug == "" {
		return nil, fmt.Errorf("workspace and repoSlug are required")
	}

	authHeader := getAuthHeader(username, appPassword)

	// Build endpoint
	endpoint := fmt.Sprintf("/repositories/%s/%s/commits", workspace, repoSlug)
	params := []string{fmt.Sprintf("pagelen=%d", limit)}

	if branch != "" {
		params = append(params, fmt.Sprintf("include=%s", url.QueryEscape(branch)))
	}

	if author != "" {
		params = append(params, fmt.Sprintf("q=author.username=%q", author))
	}

	endpoint += "?" + strings.Join(params, "&")

	commits, err := bitbucketGet(ctx, authHeader, endpoint, true)
	if err != nil {
		return nil, fmt.Errorf("failed to list commits: %w", err)
	}

	// Limit results
	if len(commits) > limit {
		commits = commits[:limit]
	}

	// Format output
	output := []map[string]interface{}{}
	for _, commit := range commits {
		output = append(output, formatCommit(commit))
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"commits": output,
			"count":   len(output),
		},
	}, nil
}

// formatCommit formats a commit response
func formatCommit(commit map[string]interface{}) map[string]interface{} {
	result := map[string]interface{}{
		"hash":      getStringMapValue(commit, "hash"),
		"message":   getStringMapValue(commit, "message"),
		"createdAt": getStringMapValue(commit, "date"),
	}

	// Extract author
	if author, ok := commit["author"].(map[string]interface{}); ok {
		result["author"] = map[string]interface{}{
			"username":    getStringMapValue(author, "username"),
			"displayName": getStringMapValue(author, "display_name"),
			"email":       getStringMapValue(author, "email"),
		}
	}

	// Extract parents
	if parents, ok := commit["parents"].([]interface{}); ok {
		parentHashes := []string{}
		for _, p := range parents {
			if pm, ok := p.(map[string]interface{}); ok {
				parentHashes = append(parentHashes, getStringMapValue(pm, "hash"))
			}
		}
		result["parents"] = parentHashes
	}

	return result
}

// ============================================================================
// BRANCH EXECUTORS
// ============================================================================

// BranchListExecutor handles bitbucket-branch-list node type
type BranchListExecutor struct{}

func (e *BranchListExecutor) Type() string { return "bitbucket-branch-list" }

func (e *BranchListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	username := getString(step.Config, "username")
	appPassword := getString(step.Config, "appPassword")
	workspace := getString(step.Config, "workspace")
	repoSlug := getString(step.Config, "repoSlug")
	limit := getInt(step.Config, "limit", 50)

	if username == "" || appPassword == "" {
		return nil, fmt.Errorf("username and appPassword are required")
	}
	if workspace == "" || repoSlug == "" {
		return nil, fmt.Errorf("workspace and repoSlug are required")
	}

	authHeader := getAuthHeader(username, appPassword)

	// Build endpoint
	endpoint := fmt.Sprintf("/repositories/%s/%s/refs/branches", workspace, repoSlug)
	params := []string{fmt.Sprintf("pagelen=%d", limit)}
	endpoint += "?" + strings.Join(params, "&")

	branches, err := bitbucketGet(ctx, authHeader, endpoint, true)
	if err != nil {
		return nil, fmt.Errorf("failed to list branches: %w", err)
	}

	// Limit results
	if len(branches) > limit {
		branches = branches[:limit]
	}

	// Format output
	output := []map[string]interface{}{}
	for _, branch := range branches {
		output = append(output, formatBranch(branch))
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"branches": output,
			"count":    len(output),
		},
	}, nil
}

// formatBranch formats a branch response
func formatBranch(branch map[string]interface{}) map[string]interface{} {
	result := map[string]interface{}{
		"name":      getStringMapValue(branch, "name"),
		"type":      getStringMapValue(branch, "type"),
		"createdAt": getStringMapValue(branch, "created_on"),
	}

	// Extract target commit
	if target, ok := branch["target"].(map[string]interface{}); ok {
		result["target"] = map[string]interface{}{
			"hash":    getStringMapValue(target, "hash"),
			"message": getStringMapValue(target, "message"),
			"date":    getStringMapValue(target, "date"),
		}
		if author, ok := target["author"].(map[string]interface{}); ok {
			result["target"].(map[string]interface{})["author"] = map[string]interface{}{
				"username":    getStringMapValue(author, "username"),
				"displayName": getStringMapValue(author, "display_name"),
				"email":       getStringMapValue(author, "email"),
			}
		}
	}

	return result
}

// ============================================================================
// HELPER FUNCTIONS
// ============================================================================

// getStringMapValue safely gets a string value from a nested map
func getStringMapValue(m map[string]interface{}, keys ...string) string {
	current := interface{}(m)
	for _, key := range keys {
		if current == nil {
			return ""
		}
		cm, ok := current.(map[string]interface{})
		if !ok {
			return ""
		}
		current = cm[key]
	}
	if current == nil {
		return ""
	}
	if s, ok := current.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", current)
}

// getFloat64MapValue safely gets a float64 value from a nested map
func getFloat64MapValue(m map[string]interface{}, keys ...string) float64 {
	current := interface{}(m)
	for _, key := range keys {
		if current == nil {
			return 0
		}
		cm, ok := current.(map[string]interface{})
		if !ok {
			return 0
		}
		current = cm[key]
	}
	if current == nil {
		return 0
	}
	switch v := current.(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case string:
		var f float64
		fmt.Sscanf(v, "%f", &f)
		return f
	default:
		return 0
	}
}

// getBoolMapValue safely gets a bool value from a nested map
func getBoolMapValue(m map[string]interface{}, keys ...string) bool {
	current := interface{}(m)
	for _, key := range keys {
		if current == nil {
			return false
		}
		cm, ok := current.(map[string]interface{})
		if !ok {
			return false
		}
		current = cm[key]
	}
	if current == nil {
		return false
	}
	if b, ok := current.(bool); ok {
		return b
	}
	return false
}
