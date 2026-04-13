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

	"github.com/axiom-studio/skills.sdk/executor"
	"github.com/axiom-studio/skills.sdk/grpc"
	"github.com/axiom-studio/skills.sdk/resolver"
)

// Linear API endpoints
const (
	LinearAPIURL = "https://api.linear.app/graphql"
)

func main() {
	// Get port from env or use default
	port := os.Getenv("SKILL_PORT")
	if port == "" {
		port = "50054"
	}

	// Create skill server
	server := grpc.NewSkillServer("skill-linear", "1.0.0")

	// Register Linear executors with schemas
	server.RegisterExecutorWithSchema("linear-issue-create", &LinearIssueCreateExecutor{}, LinearIssueCreateSchema)
	server.RegisterExecutorWithSchema("linear-issue-update", &LinearIssueUpdateExecutor{}, LinearIssueUpdateSchema)
	server.RegisterExecutorWithSchema("linear-issue-search", &LinearIssueSearchExecutor{}, LinearIssueSearchSchema)
	server.RegisterExecutorWithSchema("linear-comment", &LinearCommentExecutor{}, LinearCommentSchema)
	server.RegisterExecutorWithSchema("linear-cycle", &LinearCycleExecutor{}, LinearCycleSchema)
	server.RegisterExecutorWithSchema("linear-project", &LinearProjectExecutor{}, LinearProjectSchema)

	fmt.Printf("Starting skill-linear gRPC server on port %s\n", port)
	if err := server.Serve(port); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to serve: %v\n", err)
		os.Exit(1)
	}
}

// getLinearClient creates HTTP client with Linear API authentication
func getLinearClient(apiToken string) *http.Client {
	return &http.Client{
		Transport: &authTransport{
			apiToken: apiToken,
			base:     http.DefaultTransport,
		},
	}
}

// authTransport adds Linear API authentication to requests
type authTransport struct {
	apiToken string
	base     http.RoundTripper
}

func (t *authTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.Header.Set("Authorization", t.apiToken)
	req.Header.Set("Content-Type", "application/json")
	return t.base.RoundTrip(req)
}

// executeGraphQL executes a GraphQL query against Linear API
func executeGraphQL(ctx context.Context, client *http.Client, query string, variables map[string]interface{}) ([]byte, error) {
	body := map[string]interface{}{
		"query":     query,
		"variables": variables,
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", LinearAPIURL, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", client.Transport.(*authTransport).apiToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API error (%d): %s", resp.StatusCode, string(respBody))
	}

	return respBody, nil
}

// LinearIssueCreateExecutor handles linear-issue-create node type
type LinearIssueCreateExecutor struct{}

// LinearIssueCreateConfig defines the typed configuration for linear-issue-create
type LinearIssueCreateConfig struct {
	ApiToken    string   `json:"apiToken" description:"Linear API token"`
	TeamId      string   `json:"teamId" description:"Linear team ID"`
	Title       string   `json:"title" description:"Issue title"`
	Description string   `json:"description" description:"Issue description (Markdown)"`
	Priority    int      `json:"priority" default:"0" options:"-4:Urgent,-3:High,-2:Medium,-1:Low,0:None" description:"Issue priority (-4 to 0)"`
	AssigneeId  string   `json:"assigneeId" description:"User ID to assign the issue to"`
	StateId     string   `json:"stateId" description:"Issue state ID"`
	Labels      []string `json:"labels" description:"Label IDs to apply"`
	ProjectId   string   `json:"projectId" description:"Project ID to associate"`
	CycleId     string   `json:"cycleId" description:"Cycle ID to associate"`
}

// LinearIssueCreateSchema is the UI schema for linear-issue-create
var LinearIssueCreateSchema = resolver.NewSchemaBuilder("linear-issue-create").
	WithName("Create Linear Issue").
	WithCategory("project-management").
	WithIcon("plus-circle").
	WithDescription("Create a new issue in Linear").
	AddSection("Connection").
		AddTextField("apiToken", "API Token",
			resolver.WithRequired(),
			resolver.WithHint("Generate at https://linear.app/settings/api"),
		).
		EndSection().
	AddSection("Issue Details").
		AddTextField("teamId", "Team ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("TEAM123"),
		).
		AddTextField("title", "Title",
			resolver.WithRequired(),
			resolver.WithPlaceholder("Issue title"),
		).
		AddTextareaField("description", "Description",
			resolver.WithRows(5),
			resolver.WithHint("Supports Markdown"),
		).
		EndSection().
	AddSection("Optional Fields").
		AddSelectField("priority", "Priority", []resolver.SelectOption{
			{Label: "None", Value: "0"},
			{Label: "Low", Value: "-1"},
			{Label: "Medium", Value: "-2"},
			{Label: "High", Value: "-3"},
			{Label: "Urgent", Value: "-4"},
		}, resolver.WithDefault("0")).
		AddTextField("assigneeId", "Assignee ID",
			resolver.WithPlaceholder("User ID"),
		).
		AddTextField("stateId", "State ID",
			resolver.WithPlaceholder("State ID"),
		).
		AddTagsField("labels", "Label IDs",
			resolver.WithHint("Comma-separated label IDs"),
		).
		AddTextField("projectId", "Project ID",
			resolver.WithPlaceholder("Project ID"),
		).
		AddTextField("cycleId", "Cycle ID",
			resolver.WithPlaceholder("Cycle ID"),
		).
		EndSection().
	Build()

func (e *LinearIssueCreateExecutor) Type() string { return "linear-issue-create" }

func (e *LinearIssueCreateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg LinearIssueCreateConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	} else {
		return nil, fmt.Errorf("invalid resolver type")
	}

	if cfg.ApiToken == "" {
		return nil, fmt.Errorf("apiToken is required")
	}
	if cfg.TeamId == "" {
		return nil, fmt.Errorf("teamId is required")
	}
	if cfg.Title == "" {
		return nil, fmt.Errorf("title is required")
	}

	client := getLinearClient(cfg.ApiToken)

	// Build GraphQL mutation
	mutation := `
		mutation IssueCreate($input: IssueCreateInput!) {
			issueCreate(input: $input) {
				success
				issue {
					id
					identifier
					title
					description
					priority
					state {
						id
						name
						color
					}
					assignee {
						id
						name
						email
					}
					createdAt
					url
				}
			}
		}
	`

	input := map[string]interface{}{
		"title":       cfg.Title,
		"teamId":      cfg.TeamId,
		"description": cfg.Description,
		"priority":    cfg.Priority,
	}

	if cfg.AssigneeId != "" {
		input["assigneeId"] = cfg.AssigneeId
	}
	if cfg.StateId != "" {
		input["stateId"] = cfg.StateId
	}
	if cfg.ProjectId != "" {
		input["projectId"] = cfg.ProjectId
	}
	if cfg.CycleId != "" {
		input["cycleId"] = cfg.CycleId
	}
	if len(cfg.Labels) > 0 {
		input["labelIds"] = cfg.Labels
	}

	variables := map[string]interface{}{
		"input": input,
	}

	respBody, err := executeGraphQL(ctx, client, mutation, variables)
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	issueData, ok := result["data"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid response format")
	}

	issueCreate, ok := issueData["issueCreate"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid response format")
	}

	if success, ok := issueCreate["success"].(bool); !ok || !success {
		return nil, fmt.Errorf("failed to create issue")
	}

	issue, ok := issueCreate["issue"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid response format")
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"id":          issue["id"],
			"identifier":  issue["identifier"],
			"title":       issue["title"],
			"description": issue["description"],
			"priority":    issue["priority"],
			"state":       issue["state"],
			"assignee":    issue["assignee"],
			"createdAt":   issue["createdAt"],
			"url":         issue["url"],
			"success":     true,
		},
	}, nil
}

// LinearIssueUpdateExecutor handles linear-issue-update node type
type LinearIssueUpdateExecutor struct{}

// LinearIssueUpdateConfig defines the typed configuration for linear-issue-update
type LinearIssueUpdateConfig struct {
	ApiToken    string   `json:"apiToken" description:"Linear API token"`
	IssueId     string   `json:"issueId" description:"Issue ID to update"`
	Title       string   `json:"title" description:"New title"`
	Description string   `json:"description" description:"New description"`
	Priority    *int     `json:"priority" description:"New priority (-4 to 0)"`
	AssigneeId  string   `json:"assigneeId" description:"New assignee ID"`
	StateId     string   `json:"stateId" description:"New state ID"`
	Labels      []string `json:"labels" description:"New label IDs"`
	ProjectId   string   `json:"projectId" description:"New project ID"`
	CycleId     string   `json:"cycleId" description:"New cycle ID"`
}

// LinearIssueUpdateSchema is the UI schema for linear-issue-update
var LinearIssueUpdateSchema = resolver.NewSchemaBuilder("linear-issue-update").
	WithName("Update Linear Issue").
	WithCategory("project-management").
	WithIcon("edit").
	WithDescription("Update an existing Linear issue").
	AddSection("Connection").
		AddTextField("apiToken", "API Token",
			resolver.WithRequired(),
		).
		EndSection().
	AddSection("Issue").
		AddTextField("issueId", "Issue ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("ISSUE123"),
		).
		EndSection().
	AddSection("Fields to Update").
		AddTextField("title", "Title",
			resolver.WithPlaceholder("Leave empty to keep current"),
		).
		AddTextareaField("description", "Description",
			resolver.WithRows(5),
		).
		AddSelectField("priority", "Priority", []resolver.SelectOption{
			{Label: "None", Value: "0"},
			{Label: "Low", Value: "-1"},
			{Label: "Medium", Value: "-2"},
			{Label: "High", Value: "-3"},
			{Label: "Urgent", Value: "-4"},
		}).
		AddTextField("assigneeId", "Assignee ID").
		AddTextField("stateId", "State ID").
		AddTagsField("labels", "Label IDs").
		AddTextField("projectId", "Project ID").
		AddTextField("cycleId", "Cycle ID").
		EndSection().
	Build()

func (e *LinearIssueUpdateExecutor) Type() string { return "linear-issue-update" }

func (e *LinearIssueUpdateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg LinearIssueUpdateConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	} else {
		return nil, fmt.Errorf("invalid resolver type")
	}

	if cfg.ApiToken == "" {
		return nil, fmt.Errorf("apiToken is required")
	}
	if cfg.IssueId == "" {
		return nil, fmt.Errorf("issueId is required")
	}

	client := getLinearClient(cfg.ApiToken)

	mutation := `
		mutation IssueUpdate($id: String!, $input: IssueUpdateInput!) {
			issueUpdate(id: $id, input: $input) {
				success
				issue {
					id
					identifier
					title
					description
					priority
					state {
						id
						name
						color
					}
					assignee {
						id
						name
						email
					}
					updatedAt
					url
				}
			}
		}
	`

	input := map[string]interface{}{}

	if cfg.Title != "" {
		input["title"] = cfg.Title
	}
	if cfg.Description != "" {
		input["description"] = cfg.Description
	}
	if cfg.Priority != nil {
		input["priority"] = *cfg.Priority
	}
	if cfg.AssigneeId != "" {
		input["assigneeId"] = cfg.AssigneeId
	}
	if cfg.StateId != "" {
		input["stateId"] = cfg.StateId
	}
	if cfg.ProjectId != "" {
		input["projectId"] = cfg.ProjectId
	}
	if cfg.CycleId != "" {
		input["cycleId"] = cfg.CycleId
	}
	if len(cfg.Labels) > 0 {
		input["labelIds"] = cfg.Labels
	}

	variables := map[string]interface{}{
		"id":    cfg.IssueId,
		"input": input,
	}

	respBody, err := executeGraphQL(ctx, client, mutation, variables)
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	issueData, ok := result["data"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid response format")
	}

	issueUpdate, ok := issueData["issueUpdate"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid response format")
	}

	if success, ok := issueUpdate["success"].(bool); !ok || !success {
		return nil, fmt.Errorf("failed to update issue")
	}

	issue, ok := issueUpdate["issue"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid response format")
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"id":          issue["id"],
			"identifier":  issue["identifier"],
			"title":       issue["title"],
			"description": issue["description"],
			"priority":    issue["priority"],
			"state":       issue["state"],
			"assignee":    issue["assignee"],
			"updatedAt":   issue["updatedAt"],
			"url":         issue["url"],
			"success":     true,
		},
	}, nil
}

// LinearIssueSearchExecutor handles linear-issue-search node type
type LinearIssueSearchExecutor struct{}

// LinearIssueSearchConfig defines the typed configuration for linear-issue-search
type LinearIssueSearchConfig struct {
	ApiToken   string `json:"apiToken" description:"Linear API token"`
	TeamId     string `json:"teamId" description:"Team ID to search in"`
	Query      string `json:"query" description:"Search query string"`
	MaxResults int    `json:"maxResults" default:"20" description:"Maximum results to return"`
}

// LinearIssueSearchSchema is the UI schema for linear-issue-search
var LinearIssueSearchSchema = resolver.NewSchemaBuilder("linear-issue-search").
	WithName("Search Linear Issues").
	WithCategory("project-management").
	WithIcon("search").
	WithDescription("Search for Linear issues").
	AddSection("Connection").
		AddTextField("apiToken", "API Token",
			resolver.WithRequired(),
		).
		EndSection().
	AddSection("Search").
		AddTextField("teamId", "Team ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("TEAM123"),
		).
		AddTextareaField("query", "Search Query",
			resolver.WithRequired(),
			resolver.WithRows(3),
			resolver.WithPlaceholder("status:done assignee:me"),
			resolver.WithHint("Linear search syntax: https://linear.app/docs/search"),
		).
		AddNumberField("maxResults", "Max Results",
			resolver.WithDefault(20),
			resolver.WithMinMax(1, 100),
		).
		EndSection().
	Build()

func (e *LinearIssueSearchExecutor) Type() string { return "linear-issue-search" }

func (e *LinearIssueSearchExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg LinearIssueSearchConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	} else {
		return nil, fmt.Errorf("invalid resolver type")
	}

	if cfg.ApiToken == "" {
		return nil, fmt.Errorf("apiToken is required")
	}
	if cfg.TeamId == "" {
		return nil, fmt.Errorf("teamId is required")
	}
	if cfg.Query == "" {
		return nil, fmt.Errorf("query is required")
	}

	client := getLinearClient(cfg.ApiToken)

	if cfg.MaxResults <= 0 {
		cfg.MaxResults = 20
	}

	query := `
		query Issues($teamId: String!, $filter: IssueFilter!, $first: Int!) {
			team(id: $teamId) {
				issues(filter: $filter, first: $first) {
					nodes {
						id
						identifier
						title
						description
						priority
						state {
							id
							name
							color
						}
						assignee {
							id
							name
							email
						}
						createdAt
						updatedAt
						url
					}
				}
			}
		}
	`

	filter := map[string]interface{}{
		"search": cfg.Query,
	}

	variables := map[string]interface{}{
		"teamId": cfg.TeamId,
		"filter": filter,
		"first":  cfg.MaxResults,
	}

	respBody, err := executeGraphQL(ctx, client, query, variables)
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Extract issues from nested response
	issues := extractIssuesFromResponse(result)

	return &executor.StepResult{
		Output: map[string]interface{}{
			"issues": issues,
			"count":  len(issues),
		},
	}, nil
}

// extractIssuesFromResponse extracts issue nodes from GraphQL response
func extractIssuesFromResponse(result map[string]interface{}) []map[string]interface{} {
	var issues []map[string]interface{}

	data, ok := result["data"].(map[string]interface{})
	if !ok {
		return issues
	}

	team, ok := data["team"].(map[string]interface{})
	if !ok {
		return issues
	}

	issuesData, ok := team["issues"].(map[string]interface{})
	if !ok {
		return issues
	}

	nodes, ok := issuesData["nodes"].([]interface{})
	if !ok {
		return issues
	}

	for _, node := range nodes {
		if issue, ok := node.(map[string]interface{}); ok {
			issues = append(issues, issue)
		}
	}

	return issues
}

// LinearCommentExecutor handles linear-comment node type
type LinearCommentExecutor struct{}

// LinearCommentConfig defines the typed configuration for linear-comment
type LinearCommentConfig struct {
	ApiToken  string `json:"apiToken" description:"Linear API token"`
	IssueId   string `json:"issueId" description:"Issue ID to comment on"`
	Body      string `json:"body" description:"Comment body (Markdown)"`
	ParentId  string `json:"parentId" description:"Parent comment ID for replies"`
}

// LinearCommentSchema is the UI schema for linear-comment
var LinearCommentSchema = resolver.NewSchemaBuilder("linear-comment").
	WithName("Add Linear Comment").
	WithCategory("project-management").
	WithIcon("message-square").
	WithDescription("Add a comment to a Linear issue").
	AddSection("Connection").
		AddTextField("apiToken", "API Token",
			resolver.WithRequired(),
		).
		EndSection().
	AddSection("Comment").
		AddTextField("issueId", "Issue ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("ISSUE123"),
		).
		AddTextareaField("body", "Comment Body",
			resolver.WithRequired(),
			resolver.WithRows(5),
			resolver.WithHint("Supports Markdown"),
		).
		AddTextField("parentId", "Parent Comment ID",
			resolver.WithPlaceholder("For replying to a comment"),
		).
		EndSection().
	Build()

func (e *LinearCommentExecutor) Type() string { return "linear-comment" }

func (e *LinearCommentExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg LinearCommentConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	} else {
		return nil, fmt.Errorf("invalid resolver type")
	}

	if cfg.ApiToken == "" {
		return nil, fmt.Errorf("apiToken is required")
	}
	if cfg.IssueId == "" {
		return nil, fmt.Errorf("issueId is required")
	}
	if cfg.Body == "" {
		return nil, fmt.Errorf("body is required")
	}

	client := getLinearClient(cfg.ApiToken)

	mutation := `
		mutation CommentCreate($input: CommentCreateInput!) {
			commentCreate(input: $input) {
				success
				comment {
					id
					body
					createdAt
					user {
						id
						name
						email
					}
				}
			}
		}
	`

	input := map[string]interface{}{
		"issueId": cfg.IssueId,
		"body":    cfg.Body,
	}

	if cfg.ParentId != "" {
		input["parentId"] = cfg.ParentId
	}

	variables := map[string]interface{}{
		"input": input,
	}

	respBody, err := executeGraphQL(ctx, client, mutation, variables)
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	data, ok := result["data"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid response format")
	}

	commentCreate, ok := data["commentCreate"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid response format")
	}

	if success, ok := commentCreate["success"].(bool); !ok || !success {
		return nil, fmt.Errorf("failed to create comment")
	}

	comment, ok := commentCreate["comment"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid response format")
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"id":        comment["id"],
			"body":      comment["body"],
			"createdAt": comment["createdAt"],
			"user":      comment["user"],
			"success":   true,
		},
	}, nil
}

// LinearCycleExecutor handles linear-cycle node type
type LinearCycleExecutor struct{}

// LinearCycleConfig defines the typed configuration for linear-cycle
type LinearCycleConfig struct {
	ApiToken  string `json:"apiToken" description:"Linear API token"`
	TeamId    string `json:"teamId" description:"Team ID"`
	Operation string `json:"operation" options:"list,get,getIssues" description:"Cycle operation to perform"`
	CycleId   string `json:"cycleId" description:"Cycle ID (for get/getIssues)"`
}

// LinearCycleSchema is the UI schema for linear-cycle
var LinearCycleSchema = resolver.NewSchemaBuilder("linear-cycle").
	WithName("Linear Cycle Operations").
	WithCategory("project-management").
	WithIcon("calendar").
	WithDescription("Perform cycle operations in Linear").
	AddSection("Connection").
		AddTextField("apiToken", "API Token",
			resolver.WithRequired(),
		).
		EndSection().
	AddSection("Operation").
		AddTextField("teamId", "Team ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("TEAM123"),
		).
		AddSelectField("operation", "Operation", []resolver.SelectOption{
			{Label: "List Cycles", Value: "list"},
			{Label: "Get Cycle", Value: "get"},
			{Label: "Get Cycle Issues", Value: "getIssues"},
		}, resolver.WithRequired()).
		AddTextField("cycleId", "Cycle ID",
			resolver.WithPlaceholder("Required for get/getIssues"),
		).
		EndSection().
	Build()

func (e *LinearCycleExecutor) Type() string { return "linear-cycle" }

func (e *LinearCycleExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg LinearCycleConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	} else {
		return nil, fmt.Errorf("invalid resolver type")
	}

	if cfg.ApiToken == "" {
		return nil, fmt.Errorf("apiToken is required")
	}
	if cfg.TeamId == "" {
		return nil, fmt.Errorf("teamId is required")
	}
	if cfg.Operation == "" {
		return nil, fmt.Errorf("operation is required")
	}

	client := getLinearClient(cfg.ApiToken)

	switch strings.ToLower(cfg.Operation) {
	case "list":
		return e.listCycles(ctx, client, cfg.TeamId)
	case "get":
		if cfg.CycleId == "" {
			return nil, fmt.Errorf("cycleId is required for get operation")
		}
		return e.getCycle(ctx, client, cfg.CycleId)
	case "getissues":
		if cfg.CycleId == "" {
			return nil, fmt.Errorf("cycleId is required for getIssues operation")
		}
		return e.getCycleIssues(ctx, client, cfg.CycleId)
	default:
		return nil, fmt.Errorf("unknown operation: %s", cfg.Operation)
	}
}

func (e *LinearCycleExecutor) listCycles(ctx context.Context, client *http.Client, teamId string) (*executor.StepResult, error) {
	query := `
		query TeamCycles($teamId: String!) {
			team(id: $teamId) {
				cycles {
					nodes {
						id
						name
						number
						startsAt
						endsAt
						createdAt
					}
				}
			}
		}
	`

	variables := map[string]interface{}{
		"teamId": teamId,
	}

	respBody, err := executeGraphQL(ctx, client, query, variables)
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	data, ok := result["data"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid response format")
	}

	team, ok := data["team"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid response format")
	}

	cyclesData, ok := team["cycles"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid response format")
	}

	var cycles []map[string]interface{}
	if nodes, ok := cyclesData["nodes"].([]interface{}); ok {
		for _, node := range nodes {
			if cycle, ok := node.(map[string]interface{}); ok {
				cycles = append(cycles, cycle)
			}
		}
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"cycles": cycles,
			"count":  len(cycles),
		},
	}, nil
}

func (e *LinearCycleExecutor) getCycle(ctx context.Context, client *http.Client, cycleId string) (*executor.StepResult, error) {
	query := `
		query Cycle($cycleId: String!) {
			cycle(id: $cycleId) {
				id
				name
				number
				startsAt
				endsAt
				createdAt
				team {
					id
					name
				}
			}
		}
	`

	variables := map[string]interface{}{
		"cycleId": cycleId,
	}

	respBody, err := executeGraphQL(ctx, client, query, variables)
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	data, ok := result["data"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid response format")
	}

	cycle, ok := data["cycle"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("cycle not found")
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"cycle":   cycle,
			"success": true,
		},
	}, nil
}

func (e *LinearCycleExecutor) getCycleIssues(ctx context.Context, client *http.Client, cycleId string) (*executor.StepResult, error) {
	query := `
		query CycleIssues($cycleId: String!) {
			cycle(id: $cycleId) {
				id
				name
				issues {
					nodes {
						id
						identifier
						title
						priority
						state {
							id
							name
							color
						}
						assignee {
							id
							name
						}
					}
				}
			}
		}
	`

	variables := map[string]interface{}{
		"cycleId": cycleId,
	}

	respBody, err := executeGraphQL(ctx, client, query, variables)
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	data, ok := result["data"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid response format")
	}

	cycle, ok := data["cycle"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("cycle not found")
	}

	var issues []map[string]interface{}
	if issuesData, ok := cycle["issues"].(map[string]interface{}); ok {
		if nodes, ok := issuesData["nodes"].([]interface{}); ok {
			for _, node := range nodes {
				if issue, ok := node.(map[string]interface{}); ok {
					issues = append(issues, issue)
				}
			}
		}
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"cycle": map[string]interface{}{
				"id":   cycle["id"],
				"name": cycle["name"],
			},
			"issues": issues,
			"count":  len(issues),
		},
	}, nil
}

// LinearProjectExecutor handles linear-project node type
type LinearProjectExecutor struct{}

// LinearProjectConfig defines the typed configuration for linear-project
type LinearProjectConfig struct {
	ApiToken  string `json:"apiToken" description:"Linear API token"`
	Operation string `json:"operation" options:"list,get,getIssues" description:"Project operation to perform"`
	ProjectId string `json:"projectId" description:"Project ID (for get/getIssues)"`
	TeamId    string `json:"teamId" description:"Team ID (for list operation)"`
}

// LinearProjectSchema is the UI schema for linear-project
var LinearProjectSchema = resolver.NewSchemaBuilder("linear-project").
	WithName("Linear Project Operations").
	WithCategory("project-management").
	WithIcon("folder").
	WithDescription("Perform project operations in Linear").
	AddSection("Connection").
		AddTextField("apiToken", "API Token",
			resolver.WithRequired(),
		).
		EndSection().
	AddSection("Operation").
		AddSelectField("operation", "Operation", []resolver.SelectOption{
			{Label: "List Projects", Value: "list"},
			{Label: "Get Project", Value: "get"},
			{Label: "Get Project Issues", Value: "getIssues"},
		}, resolver.WithRequired()).
		AddTextField("teamId", "Team ID",
			resolver.WithPlaceholder("Required for list operation"),
		).
		AddTextField("projectId", "Project ID",
			resolver.WithPlaceholder("Required for get/getIssues"),
		).
		EndSection().
	Build()

func (e *LinearProjectExecutor) Type() string { return "linear-project" }

func (e *LinearProjectExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg LinearProjectConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	} else {
		return nil, fmt.Errorf("invalid resolver type")
	}

	if cfg.ApiToken == "" {
		return nil, fmt.Errorf("apiToken is required")
	}
	if cfg.Operation == "" {
		return nil, fmt.Errorf("operation is required")
	}

	client := getLinearClient(cfg.ApiToken)

	switch strings.ToLower(cfg.Operation) {
	case "list":
		if cfg.TeamId == "" {
			return nil, fmt.Errorf("teamId is required for list operation")
		}
		return e.listProjects(ctx, client, cfg.TeamId)
	case "get":
		if cfg.ProjectId == "" {
			return nil, fmt.Errorf("projectId is required for get operation")
		}
		return e.getProject(ctx, client, cfg.ProjectId)
	case "getissues":
		if cfg.ProjectId == "" {
			return nil, fmt.Errorf("projectId is required for getIssues operation")
		}
		return e.getProjectIssues(ctx, client, cfg.ProjectId)
	default:
		return nil, fmt.Errorf("unknown operation: %s", cfg.Operation)
	}
}

func (e *LinearProjectExecutor) listProjects(ctx context.Context, client *http.Client, teamId string) (*executor.StepResult, error) {
	query := `
		query TeamProjects($teamId: String!) {
			team(id: $teamId) {
				projects {
					nodes {
						id
						name
						description
						icon
						createdAt
						updatedAt
					}
				}
			}
		}
	`

	variables := map[string]interface{}{
		"teamId": teamId,
	}

	respBody, err := executeGraphQL(ctx, client, query, variables)
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	data, ok := result["data"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid response format")
	}

	team, ok := data["team"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid response format")
	}

	projectsData, ok := team["projects"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid response format")
	}

	var projects []map[string]interface{}
	if nodes, ok := projectsData["nodes"].([]interface{}); ok {
		for _, node := range nodes {
			if project, ok := node.(map[string]interface{}); ok {
				projects = append(projects, project)
			}
		}
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"projects": projects,
			"count":    len(projects),
		},
	}, nil
}

func (e *LinearProjectExecutor) getProject(ctx context.Context, client *http.Client, projectId string) (*executor.StepResult, error) {
	query := `
		query Project($projectId: String!) {
			project(id: $projectId) {
				id
				name
				description
				icon
				createdAt
				updatedAt
				team {
					id
					name
				}
			}
		}
	`

	variables := map[string]interface{}{
		"projectId": projectId,
	}

	respBody, err := executeGraphQL(ctx, client, query, variables)
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	data, ok := result["data"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid response format")
	}

	project, ok := data["project"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("project not found")
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"project": project,
			"success": true,
		},
	}, nil
}

func (e *LinearProjectExecutor) getProjectIssues(ctx context.Context, client *http.Client, projectId string) (*executor.StepResult, error) {
	query := `
		query ProjectIssues($projectId: String!) {
			project(id: $projectId) {
				id
				name
				issues {
					nodes {
						id
						identifier
						title
						priority
						state {
							id
							name
							color
						}
						assignee {
							id
							name
						}
					}
				}
			}
		}
	`

	variables := map[string]interface{}{
		"projectId": projectId,
	}

	respBody, err := executeGraphQL(ctx, client, query, variables)
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	data, ok := result["data"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid response format")
	}

	project, ok := data["project"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("project not found")
	}

	var issues []map[string]interface{}
	if issuesData, ok := project["issues"].(map[string]interface{}); ok {
		if nodes, ok := issuesData["nodes"].([]interface{}); ok {
			for _, node := range nodes {
				if issue, ok := node.(map[string]interface{}); ok {
					issues = append(issues, issue)
				}
			}
		}
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"project": map[string]interface{}{
				"id":   project["id"],
				"name": project["name"],
			},
			"issues": issues,
			"count":  len(issues),
		},
	}, nil
}
