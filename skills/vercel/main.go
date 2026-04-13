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
	vercelAPIBase   = "https://api.vercel.com"
	vercelAPIVersion = "v9"
	iconVercel      = "cloud"
)

// ============================================================================
// VERCEL API CLIENT
// ============================================================================

// VercelClient handles HTTP communication with Vercel API
type VercelClient struct {
	baseURL    string
	apiToken   string
	httpClient *http.Client
}

// NewVercelClient creates a new Vercel API client
func NewVercelClient(apiToken string) *VercelClient {
	return &VercelClient{
		baseURL: vercelAPIBase,
		apiToken: apiToken,
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

// doRequest performs an HTTP request to the Vercel API
func (c *VercelClient) doRequest(ctx context.Context, method, path string, body interface{}) ([]byte, error) {
	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
		reqBody = bytes.NewReader(data)
	}

	url := fmt.Sprintf("%s/%s", c.baseURL, strings.TrimPrefix(path, "/"))
	req, err := http.NewRequestWithContext(ctx, method, url, reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.apiToken))
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode >= 400 {
		var errResp map[string]interface{}
		if err := json.Unmarshal(respBody, &errResp); err == nil {
			if msg, ok := errResp["error"].(map[string]interface{}); ok {
				if code, ok := msg["code"].(string); ok {
					if reason, ok := msg["reason"].(string); ok {
						return nil, fmt.Errorf("Vercel API error [%s]: %s (HTTP %d)", code, reason, resp.StatusCode)
					}
				}
				if message, ok := msg["message"].(string); ok {
					return nil, fmt.Errorf("Vercel API error: %s (HTTP %d)", message, resp.StatusCode)
				}
			}
		}
		return nil, fmt.Errorf("Vercel API error: HTTP %d - %s", resp.StatusCode, string(respBody))
	}

	return respBody, nil
}

// ============================================================================
// VERCEL API DATA STRUCTURES
// ============================================================================

// VercelProject represents a Vercel project
type VercelProject struct {
	ID             string                 `json:"id"`
	Name           string                 `json:"name"`
	TeamID         string                 `json:"teamId,omitempty"`
	Link           map[string]interface{} `json:"link,omitempty"`
	GitRepository  map[string]interface{} `json:"gitRepository,omitempty"`
	UpdatedAt      int64                  `json:"updatedAt,omitempty"`
	CreatedAt      int64                  `json:"createdAt,omitempty"`
	TargetFramework string                `json:"targetFramework,omitempty"`
}

// VercelDeployment represents a Vercel deployment
type VercelDeployment struct {
	ID             string            `json:"id"`
	UID            string            `json:"uid"`
	URL            string            `json:"url"`
	Alias          []string          `json:"alias,omitempty"`
	ProjectID      string            `json:"projectId"`
	ProjectSettings map[string]interface{} `json:"projectSettings,omitempty"`
	Target         string            `json:"target,omitempty"`
	Ref            string            `json:"ref,omitempty"`
	CommitRef      string            `json:"commitRef,omitempty"`
	CommitMessage  string            `json:"commitMessage,omitempty"`
	CommitAuthorName string          `json:"commitAuthorName,omitempty"`
	State          string            `json:"state"`
	Ready          int               `json:"ready,omitempty"`
	ReadyState     string            `json:"readyState,omitempty"`
	ErrorCode      string            `json:"errorCode,omitempty"`
	ErrorMessage   string            `json:"errorMessage,omitempty"`
	CreatedAt      int64             `json:"createdAt"`
	UpdatedAt      int64             `json:"updatedAt"`
	Build          map[string]interface{} `json:"build,omitempty"`
}

// VercelDomain represents a domain configuration
type VercelDomain struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	TeamID             string `json:"teamId,omitempty"`
	ProjectID          string `json:"projectId,omitempty"`
	Redirect           string `json:"redirect,omitempty"`
	Verified           bool   `json:"verified"`
	Verification       map[string]interface{} `json:"verification,omitempty"`
	ConfiguredBy       string `json:"configuredBy,omitempty"`
	Nameservers        []string `json:"nameservers,omitempty"`
	ServiceType        string `json:"serviceType,omitempty"`
	Creator            string `json:"creator,omitempty"`
	CreatedAt          int64  `json:"createdAt"`
	UpdatedAt          int64  `json:"updatedAt"`
	BoughtAt           int64  `json:"boughtAt,omitempty"`
	ExpiresAt          int64  `json:"expiresAt,omitempty"`
	OrderedAt          int64  `json:"orderedAt,omitempty"`
	RegistrationStatus string `json:"registrationStatus,omitempty"`
}

// VercelEnvVar represents an environment variable
type VercelEnvVar struct {
	ID         string   `json:"id,omitempty"`
	Key        string   `json:"key"`
	Value      string   `json:"value"`
	Target     []string `json:"target"`
	TeamID     string   `json:"teamId,omitempty"`
	ProjectID  string   `json:"projectId,omitempty"`
	SourceType string   `json:"source,omitempty"`
	Type       string   `json:"type,omitempty"`
	UpdatedAt  int64    `json:"updatedAt,omitempty"`
	UpdatedAtBy string  `json:"updatedAtBy,omitempty"`
	CreatedAt  int64    `json:"createdAt,omitempty"`
	CreatedAtBy string  `json:"createdAtBy,omitempty"`
	GitBranch  string   `json:"gitBranch,omitempty"`
}

// ============================================================================
// SCHEMAS
// ============================================================================

// DeploySchema is the UI schema for vercel-deploy
var DeploySchema = resolver.NewSchemaBuilder("vercel-deploy").
	WithName("Vercel Deploy").
	WithCategory("action").
	WithIcon(iconVercel).
	WithDescription("Create a new deployment on Vercel").
	AddSection("Authentication").
		AddExpressionField("apiToken", "API Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("..."),
			resolver.WithHint("Vercel API token (supports {{bindings.xxx}})"),
			resolver.WithSensitive(),
		).
		AddExpressionField("teamId", "Team ID",
			resolver.WithPlaceholder("team_xxx"),
			resolver.WithHint("Vercel Team ID (optional, for team deployments)"),
		).
		EndSection().
	AddSection("Project").
		AddExpressionField("projectId", "Project ID",
			resolver.WithPlaceholder("prj_xxx"),
			resolver.WithHint("Existing Vercel Project ID"),
		).
		AddExpressionField("projectName", "Project Name",
			resolver.WithPlaceholder("my-project"),
			resolver.WithHint("Name for new project (if projectId not provided)"),
		).
		EndSection().
	AddSection("Source").
		AddSelectField("gitSource", "Git Source",
			[]resolver.SelectOption{
				{Label: "GitHub", Value: "github"},
				{Label: "GitLab", Value: "gitlab"},
				{Label: "Bitbucket", Value: "bitbucket"},
			},
			resolver.WithHint("Git provider for deployment"),
		).
		AddExpressionField("gitRepoUrl", "Git Repository URL",
			resolver.WithPlaceholder("https://github.com/user/repo"),
			resolver.WithHint("Full Git repository URL"),
		).
		AddExpressionField("gitBranch", "Git Branch",
			resolver.WithPlaceholder("main"),
			resolver.WithHint("Branch to deploy"),
		).
		AddExpressionField("gitCommitRef", "Git Commit Ref",
			resolver.WithPlaceholder("abc123"),
			resolver.WithHint("Specific commit to deploy"),
		).
		EndSection().
	AddSection("Deployment Settings").
		AddSelectField("target", "Target",
			[]resolver.SelectOption{
				{Label: "Production", Value: "production"},
				{Label: "Preview", Value: "preview"},
				{Label: "Staging", Value: "staging"},
			},
			resolver.WithDefault("production"),
			resolver.WithHint("Deployment target environment"),
		).
		AddExpressionField("ref", "Git Ref",
			resolver.WithPlaceholder("main"),
			resolver.WithHint("Git reference (branch/tag) to deploy"),
		).
		AddExpressionField("regions", "Regions",
			resolver.WithPlaceholder("iad1,sfo1"),
			resolver.WithHint("Comma-separated list of deployment regions"),
		).
		EndSection().
	AddSection("Advanced").
		AddJSONField("build", "Build Settings",
			resolver.WithHeight(150),
			resolver.WithHint("Custom build configuration"),
		).
		AddJSONField("projectSettings", "Project Settings",
			resolver.WithHeight(150),
			resolver.WithHint("Project-specific settings"),
		).
		AddJSONField("functions", "Functions Config",
			resolver.WithHeight(150),
			resolver.WithHint("Serverless function configuration"),
		).
		EndSection().
	Build()

// DeploymentListSchema is the UI schema for vercel-deployment-list
var DeploymentListSchema = resolver.NewSchemaBuilder("vercel-deployment-list").
	WithName("List Deployments").
	WithCategory("action").
	WithIcon(iconVercel).
	WithDescription("List deployments for a Vercel project").
	AddSection("Authentication").
		AddExpressionField("apiToken", "API Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("..."),
			resolver.WithHint("Vercel API token"),
			resolver.WithSensitive(),
		).
		AddExpressionField("teamId", "Team ID",
			resolver.WithPlaceholder("team_xxx"),
			resolver.WithHint("Vercel Team ID"),
		).
		EndSection().
	AddSection("Filter").
		AddExpressionField("projectId", "Project ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("prj_xxx"),
			resolver.WithHint("Vercel Project ID"),
		).
		AddSelectField("target", "Target",
			[]resolver.SelectOption{
				{Label: "All", Value: ""},
				{Label: "Production", Value: "production"},
				{Label: "Preview", Value: "preview"},
				{Label: "Staging", Value: "staging"},
			},
			resolver.WithHint("Filter by target"),
		).
		AddSelectField("state", "State",
			[]resolver.SelectOption{
				{Label: "All", Value: ""},
				{Label: "Building", Value: "BUILDING"},
				{Label: "Queued", Value: "QUEUED"},
				{Label: "Init", Value: "INIT"},
				{Label: "Ready", Value: "READY"},
				{Label: "Error", Value: "ERROR"},
				{Label: "Canceled", Value: "CANCELED"},
			},
			resolver.WithHint("Filter by deployment state"),
		).
		AddExpressionField("gitBranch", "Git Branch",
			resolver.WithPlaceholder("main"),
			resolver.WithHint("Filter by git branch"),
		).
		EndSection().
	AddSection("Pagination").
		AddNumberField("limit", "Limit",
			resolver.WithDefault(20),
			resolver.WithHint("Maximum number of deployments to return"),
		).
		AddExpressionField("continue", "Continue",
			resolver.WithHint("Pagination cursor for next page"),
		).
		EndSection().
	Build()

// DeploymentGetSchema is the UI schema for vercel-deployment-get
var DeploymentGetSchema = resolver.NewSchemaBuilder("vercel-deployment-get").
	WithName("Get Deployment").
	WithCategory("action").
	WithIcon(iconVercel).
	WithDescription("Get details of a specific Vercel deployment").
	AddSection("Authentication").
		AddExpressionField("apiToken", "API Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("..."),
			resolver.WithHint("Vercel API token"),
			resolver.WithSensitive(),
		).
		AddExpressionField("teamId", "Team ID",
			resolver.WithPlaceholder("team_xxx"),
			resolver.WithHint("Vercel Team ID"),
		).
		EndSection().
	AddSection("Deployment").
		AddExpressionField("deployId", "Deployment ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("dpl_xxx"),
			resolver.WithHint("Vercel Deployment ID"),
		).
		EndSection().
	Build()

// ProjectListSchema is the UI schema for vercel-project-list
var ProjectListSchema = resolver.NewSchemaBuilder("vercel-project-list").
	WithName("List Projects").
	WithCategory("action").
	WithIcon(iconVercel).
	WithDescription("List all Vercel projects").
	AddSection("Authentication").
		AddExpressionField("apiToken", "API Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("..."),
			resolver.WithHint("Vercel API token"),
			resolver.WithSensitive(),
		).
		AddExpressionField("teamId", "Team ID",
			resolver.WithPlaceholder("team_xxx"),
			resolver.WithHint("Vercel Team ID"),
		).
		EndSection().
	AddSection("Pagination").
		AddNumberField("limit", "Limit",
			resolver.WithDefault(50),
			resolver.WithHint("Maximum number of projects to return"),
		).
		EndSection().
	Build()

// ProjectCreateSchema is the UI schema for vercel-project-create
var ProjectCreateSchema = resolver.NewSchemaBuilder("vercel-project-create").
	WithName("Create Project").
	WithCategory("action").
	WithIcon(iconVercel).
	WithDescription("Create a new Vercel project").
	AddSection("Authentication").
		AddExpressionField("apiToken", "API Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("..."),
			resolver.WithHint("Vercel API token"),
			resolver.WithSensitive(),
		).
		AddExpressionField("teamId", "Team ID",
			resolver.WithPlaceholder("team_xxx"),
			resolver.WithHint("Vercel Team ID"),
		).
		EndSection().
	AddSection("Project").
		AddExpressionField("name", "Project Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-project"),
			resolver.WithHint("Name for the new project"),
		).
		EndSection().
	AddSection("Git Integration").
		AddSelectField("gitSource", "Git Source",
			[]resolver.SelectOption{
				{Label: "GitHub", Value: "github"},
				{Label: "GitLab", Value: "gitlab"},
				{Label: "Bitbucket", Value: "bitbucket"},
			},
			resolver.WithHint("Git provider"),
		).
		AddExpressionField("gitRepoUrl", "Git Repository URL",
			resolver.WithPlaceholder("https://github.com/user/repo"),
			resolver.WithHint("Full Git repository URL"),
		).
		AddExpressionField("gitBranch", "Git Branch",
			resolver.WithPlaceholder("main"),
			resolver.WithHint("Default branch"),
		).
		EndSection().
	AddSection("Build Settings").
		AddExpressionField("buildCommand", "Build Command",
			resolver.WithPlaceholder("npm run build"),
			resolver.WithHint("Command to build the project"),
		).
		AddExpressionField("devCommand", "Dev Command",
			resolver.WithPlaceholder("npm run dev"),
			resolver.WithHint("Command for development"),
		).
		AddExpressionField("installCommand", "Install Command",
			resolver.WithPlaceholder("npm install"),
			resolver.WithHint("Command to install dependencies"),
		).
		AddExpressionField("outputDirectory", "Output Directory",
			resolver.WithPlaceholder("dist"),
			resolver.WithHint("Build output directory"),
		).
		AddExpressionField("targetFramework", "Target Framework",
			resolver.WithPlaceholder("nextjs"),
			resolver.WithHint("Framework preset (nextjs, create-react-app, etc.)"),
		).
		EndSection().
	AddSection("Environment Variables").
		AddKeyValueField("environmentVariables", "Environment Variables",
			resolver.WithHint("Initial environment variables (key-value pairs)"),
		).
		EndSection().
	Build()

// DomainAddSchema is the UI schema for vercel-domain-add
var DomainAddSchema = resolver.NewSchemaBuilder("vercel-domain-add").
	WithName("Add Domain").
	WithCategory("action").
	WithIcon(iconVercel).
	WithDescription("Add a domain to a Vercel project").
	AddSection("Authentication").
		AddExpressionField("apiToken", "API Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("..."),
			resolver.WithHint("Vercel API token"),
			resolver.WithSensitive(),
		).
		AddExpressionField("teamId", "Team ID",
			resolver.WithPlaceholder("team_xxx"),
			resolver.WithHint("Vercel Team ID"),
		).
		EndSection().
	AddSection("Domain").
		AddExpressionField("projectId", "Project ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("prj_xxx"),
			resolver.WithHint("Vercel Project ID"),
		).
		AddExpressionField("domain", "Domain Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("example.com"),
			resolver.WithHint("Domain name to add"),
		).
		AddExpressionField("redirect", "Redirect To",
			resolver.WithPlaceholder("https://example.com"),
			resolver.WithHint("Optional redirect URL"),
		).
		EndSection().
	Build()

// DomainListSchema is the UI schema for vercel-domain-list
var DomainListSchema = resolver.NewSchemaBuilder("vercel-domain-list").
	WithName("List Domains").
	WithCategory("action").
	WithIcon(iconVercel).
	WithDescription("List domains for a Vercel project").
	AddSection("Authentication").
		AddExpressionField("apiToken", "API Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("..."),
			resolver.WithHint("Vercel API token"),
			resolver.WithSensitive(),
		).
		AddExpressionField("teamId", "Team ID",
			resolver.WithPlaceholder("team_xxx"),
			resolver.WithHint("Vercel Team ID"),
		).
		EndSection().
	AddSection("Project").
		AddExpressionField("projectId", "Project ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("prj_xxx"),
			resolver.WithHint("Vercel Project ID"),
		).
		EndSection().
	Build()

// EnvSetSchema is the UI schema for vercel-env-set
var EnvSetSchema = resolver.NewSchemaBuilder("vercel-env-set").
	WithName("Set Environment Variable").
	WithCategory("action").
	WithIcon(iconVercel).
	WithDescription("Set an environment variable on a Vercel project").
	AddSection("Authentication").
		AddExpressionField("apiToken", "API Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("..."),
			resolver.WithHint("Vercel API token"),
			resolver.WithSensitive(),
		).
		AddExpressionField("teamId", "Team ID",
			resolver.WithPlaceholder("team_xxx"),
			resolver.WithHint("Vercel Team ID"),
		).
		EndSection().
	AddSection("Environment Variable").
		AddExpressionField("projectId", "Project ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("prj_xxx"),
			resolver.WithHint("Vercel Project ID"),
		).
		AddExpressionField("key", "Key",
			resolver.WithRequired(),
			resolver.WithPlaceholder("DATABASE_URL"),
			resolver.WithHint("Environment variable name"),
		).
		AddExpressionField("value", "Value",
			resolver.WithRequired(),
			resolver.WithPlaceholder("postgres://..."),
			resolver.WithHint("Environment variable value"),
			resolver.WithSensitive(),
		).
		AddSelectField("target", "Target",
			[]resolver.SelectOption{
				{Label: "All", Value: ""},
				{Label: "Production", Value: "production"},
				{Label: "Preview", Value: "preview"},
				{Label: "Development", Value: "development"},
			},
			resolver.WithHint("Target environment(s)"),
		).
		AddExpressionField("gitBranch", "Git Branch",
			resolver.WithPlaceholder("main"),
			resolver.WithHint("Git branch for preview environment"),
		).
		AddSelectField("type", "Type",
			[]resolver.SelectOption{
				{Label: "Plain", Value: "plain"},
				{Label: "Encrypted", Value: "encrypted"},
				{Label: "System", Value: "system"},
			},
			resolver.WithDefault("plain"),
			resolver.WithHint("Environment variable type"),
		).
		EndSection().
	Build()

// EnvListSchema is the UI schema for vercel-env-list
var EnvListSchema = resolver.NewSchemaBuilder("vercel-env-list").
	WithName("List Environment Variables").
	WithCategory("action").
	WithIcon(iconVercel).
	WithDescription("List environment variables for a Vercel project").
	AddSection("Authentication").
		AddExpressionField("apiToken", "API Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("..."),
			resolver.WithHint("Vercel API token"),
			resolver.WithSensitive(),
		).
		AddExpressionField("teamId", "Team ID",
			resolver.WithPlaceholder("team_xxx"),
			resolver.WithHint("Vercel Team ID"),
		).
		EndSection().
	AddSection("Project").
		AddExpressionField("projectId", "Project ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("prj_xxx"),
			resolver.WithHint("Vercel Project ID"),
		).
		EndSection().
	Build()

// ============================================================================
// HELPER FUNCTIONS
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

// ============================================================================
// EXECUTORS
// ============================================================================

// DeployExecutor handles vercel-deploy
type DeployExecutor struct{}

func (e *DeployExecutor) Type() string {
	return "vercel-deploy"
}

func (e *DeployExecutor) Execute(ctx context.Context, step *executor.StepDefinition, tmplResolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := tmplResolver.ResolveMap(step.Config)

	// Get connection parameters
	apiToken := getString(config, "apiToken")
	apiToken = tmplResolver.ResolveString(apiToken)
	teamID := getString(config, "teamId")
	teamID = tmplResolver.ResolveString(teamID)

	if apiToken == "" {
		return nil, fmt.Errorf("Vercel API token is required")
	}

	client := NewVercelClient(apiToken)

	// Build deployment request
	deployReq := make(map[string]interface{})

	// Set project
	projectID := getString(config, "projectId")
	projectID = tmplResolver.ResolveString(projectID)
	projectName := getString(config, "projectName")
	projectName = tmplResolver.ResolveString(projectName)
	if projectID != "" {
		deployReq["projectId"] = projectID
	} else if projectName != "" {
		deployReq["name"] = projectName
	}

	// Set target
	target := getString(config, "target")
	target = tmplResolver.ResolveString(target)
	if target != "" {
		deployReq["target"] = target
	}

	// Set git source
	gitSource := getString(config, "gitSource")
	gitSource = tmplResolver.ResolveString(gitSource)
	if gitSource != "" {
		deployReq["gitSource"] = gitSource
	}
	gitRepoURL := getString(config, "gitRepoUrl")
	gitRepoURL = tmplResolver.ResolveString(gitRepoURL)
	if gitRepoURL != "" {
		deployReq["gitRepoUrl"] = gitRepoURL
	}
	gitBranch := getString(config, "gitBranch")
	gitBranch = tmplResolver.ResolveString(gitBranch)
	if gitBranch != "" {
		deployReq["gitBranch"] = gitBranch
	}
	gitCommitRef := getString(config, "gitCommitRef")
	gitCommitRef = tmplResolver.ResolveString(gitCommitRef)
	if gitCommitRef != "" {
		deployReq["gitCommitRef"] = gitCommitRef
	}
	ref := getString(config, "ref")
	ref = tmplResolver.ResolveString(ref)
	if ref != "" {
		deployReq["ref"] = ref
	}

	// Set regions
	regions := getString(config, "regions")
	regions = tmplResolver.ResolveString(regions)
	if regions != "" {
		regionList := strings.Split(regions, ",")
		for i, r := range regionList {
			regionList[i] = strings.TrimSpace(r)
		}
		deployReq["regions"] = regionList
	}

	// Set build settings
	if build, ok := config["build"]; ok && build != nil {
		deployReq["build"] = build
	}

	// Set project settings
	if projectSettings, ok := config["projectSettings"]; ok && projectSettings != nil {
		deployReq["projectSettings"] = projectSettings
	}

	// Set functions config
	functions := getString(config, "functions")
	functions = tmplResolver.ResolveString(functions)
	if functions != "" {
		var functionsMap map[string]interface{}
		if err := json.Unmarshal([]byte(functions), &functionsMap); err == nil {
			deployReq["functions"] = functionsMap
		}
	}

	// Build URL path
	path := "/v9/deployments"
	if teamID != "" {
		path = fmt.Sprintf("%s?teamId=%s", path, teamID)
	}

	respBody, err := client.doRequest(ctx, "POST", path, deployReq)
	if err != nil {
		return nil, err
	}

	var deployment VercelDeployment
	if err := json.Unmarshal(respBody, &deployment); err != nil {
		return nil, fmt.Errorf("failed to parse deployment response: %w", err)
	}

	result := map[string]interface{}{
		"id":            deployment.ID,
		"uid":           deployment.UID,
		"url":           deployment.URL,
		"projectId":     deployment.ProjectID,
		"target":        deployment.Target,
		"ref":           deployment.Ref,
		"state":         deployment.State,
		"readyState":    deployment.ReadyState,
		"commitMessage": deployment.CommitMessage,
		"commitRef":     deployment.CommitRef,
		"createdAt":     deployment.CreatedAt,
		"updatedAt":     deployment.UpdatedAt,
	}

	if len(deployment.Alias) > 0 {
		result["alias"] = deployment.Alias
	}

	return &executor.StepResult{
		Output: result,
	}, nil
}

// DeploymentListExecutor handles vercel-deployment-list
type DeploymentListExecutor struct{}

func (e *DeploymentListExecutor) Type() string {
	return "vercel-deployment-list"
}

func (e *DeploymentListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, tmplResolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := tmplResolver.ResolveMap(step.Config)

	// Get connection parameters
	apiToken := getString(config, "apiToken")
	apiToken = tmplResolver.ResolveString(apiToken)
	teamID := getString(config, "teamId")
	teamID = tmplResolver.ResolveString(teamID)

	if apiToken == "" {
		return nil, fmt.Errorf("Vercel API token is required")
	}

	// Get project ID
	projectID := getString(config, "projectId")
	projectID = tmplResolver.ResolveString(projectID)
	if projectID == "" {
		return nil, fmt.Errorf("projectId is required")
	}

	client := NewVercelClient(apiToken)

	// Build query parameters
	queryParams := []string{fmt.Sprintf("projectId=%s", projectID)}

	if teamID != "" {
		queryParams = append(queryParams, fmt.Sprintf("teamId=%s", teamID))
	}
	target := getString(config, "target")
	target = tmplResolver.ResolveString(target)
	if target != "" {
		queryParams = append(queryParams, fmt.Sprintf("target=%s", target))
	}
	state := getString(config, "state")
	state = tmplResolver.ResolveString(state)
	if state != "" {
		queryParams = append(queryParams, fmt.Sprintf("state=%s", state))
	}
	limit := getInt(config, "limit", 0)
	if limit > 0 {
		queryParams = append(queryParams, fmt.Sprintf("limit=%d", limit))
	}
	continueToken := getString(config, "continue")
	continueToken = tmplResolver.ResolveString(continueToken)
	if continueToken != "" {
		queryParams = append(queryParams, fmt.Sprintf("continue=%s", continueToken))
	}
	uid := getString(config, "uid")
	uid = tmplResolver.ResolveString(uid)
	if uid != "" {
		queryParams = append(queryParams, fmt.Sprintf("uid=%s", uid))
	}
	gitBranch := getString(config, "gitBranch")
	gitBranch = tmplResolver.ResolveString(gitBranch)
	if gitBranch != "" {
		queryParams = append(queryParams, fmt.Sprintf("gitBranch=%s", gitBranch))
	}
	gitCommit := getString(config, "gitCommit")
	gitCommit = tmplResolver.ResolveString(gitCommit)
	if gitCommit != "" {
		queryParams = append(queryParams, fmt.Sprintf("gitCommit=%s", gitCommit))
	}

	path := "/v6/deployments?" + strings.Join(queryParams, "&")

	respBody, err := client.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}

	var response struct {
		Deployments []VercelDeployment `json:"deployments"`
		Next        string             `json:"next,omitempty"`
	}
	if err := json.Unmarshal(respBody, &response); err != nil {
		return nil, fmt.Errorf("failed to parse deployments response: %w", err)
	}

	deployments := make([]map[string]interface{}, 0, len(response.Deployments))
	for _, d := range response.Deployments {
		deployments = append(deployments, map[string]interface{}{
			"id":            d.ID,
			"uid":           d.UID,
			"url":           d.URL,
			"projectId":     d.ProjectID,
			"target":        d.Target,
			"ref":           d.Ref,
			"state":         d.State,
			"readyState":    d.ReadyState,
			"commitMessage": d.CommitMessage,
			"commitRef":     d.CommitRef,
			"createdAt":     d.CreatedAt,
			"updatedAt":     d.UpdatedAt,
		})
	}

	result := map[string]interface{}{
		"deployments": deployments,
		"count":       len(deployments),
	}
	if response.Next != "" {
		result["next"] = response.Next
	}

	return &executor.StepResult{
		Output: result,
	}, nil
}

// DeploymentGetExecutor handles vercel-deployment-get
type DeploymentGetExecutor struct{}

func (e *DeploymentGetExecutor) Type() string {
	return "vercel-deployment-get"
}

func (e *DeploymentGetExecutor) Execute(ctx context.Context, step *executor.StepDefinition, tmplResolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := tmplResolver.ResolveMap(step.Config)

	// Get connection parameters
	apiToken := getString(config, "apiToken")
	apiToken = tmplResolver.ResolveString(apiToken)
	teamID := getString(config, "teamId")
	teamID = tmplResolver.ResolveString(teamID)

	if apiToken == "" {
		return nil, fmt.Errorf("Vercel API token is required")
	}

	// Get deployment ID
	deployID := getString(config, "deployId")
	deployID = tmplResolver.ResolveString(deployID)
	if deployID == "" {
		return nil, fmt.Errorf("deployId is required")
	}

	client := NewVercelClient(apiToken)

	path := fmt.Sprintf("/v13/deployments/%s", deployID)
	if teamID != "" {
		path = fmt.Sprintf("%s?teamId=%s", path, teamID)
	}

	respBody, err := client.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}

	var deployment VercelDeployment
	if err := json.Unmarshal(respBody, &deployment); err != nil {
		return nil, fmt.Errorf("failed to parse deployment response: %w", err)
	}

	result := map[string]interface{}{
		"id":               deployment.ID,
		"uid":              deployment.UID,
		"url":              deployment.URL,
		"projectId":        deployment.ProjectID,
		"target":           deployment.Target,
		"ref":              deployment.Ref,
		"state":            deployment.State,
		"readyState":       deployment.ReadyState,
		"commitMessage":    deployment.CommitMessage,
		"commitRef":        deployment.CommitRef,
		"commitAuthorName": deployment.CommitAuthorName,
		"createdAt":        deployment.CreatedAt,
		"updatedAt":        deployment.UpdatedAt,
		"build":            deployment.Build,
		"projectSettings":  deployment.ProjectSettings,
	}

	if len(deployment.Alias) > 0 {
		result["alias"] = deployment.Alias
	}
	if deployment.ErrorCode != "" {
		result["errorCode"] = deployment.ErrorCode
		result["errorMessage"] = deployment.ErrorMessage
	}

	return &executor.StepResult{
		Output: result,
	}, nil
}

// ProjectListExecutor handles vercel-project-list
type ProjectListExecutor struct{}

func (e *ProjectListExecutor) Type() string {
	return "vercel-project-list"
}

func (e *ProjectListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, tmplResolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := tmplResolver.ResolveMap(step.Config)

	// Get connection parameters
	apiToken := getString(config, "apiToken")
	apiToken = tmplResolver.ResolveString(apiToken)
	teamID := getString(config, "teamId")
	teamID = tmplResolver.ResolveString(teamID)

	if apiToken == "" {
		return nil, fmt.Errorf("Vercel API token is required")
	}

	client := NewVercelClient(apiToken)

	// Build query parameters
	queryParams := []string{}
	if teamID != "" {
		queryParams = append(queryParams, fmt.Sprintf("teamId=%s", teamID))
	}
	limit := getInt(config, "limit", 0)
	if limit > 0 {
		queryParams = append(queryParams, fmt.Sprintf("limit=%d", limit))
	}

	path := "/v9/projects"
	if len(queryParams) > 0 {
		path = path + "?" + strings.Join(queryParams, "&")
	}

	respBody, err := client.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}

	var response struct {
		Projects []VercelProject `json:"projects"`
	}
	if err := json.Unmarshal(respBody, &response); err != nil {
		return nil, fmt.Errorf("failed to parse projects response: %w", err)
	}

	projects := make([]map[string]interface{}, 0, len(response.Projects))
	for _, p := range response.Projects {
		project := map[string]interface{}{
			"id":        p.ID,
			"name":      p.Name,
			"updatedAt": p.UpdatedAt,
			"createdAt": p.CreatedAt,
		}
		if p.TeamID != "" {
			project["teamId"] = p.TeamID
		}
		if p.TargetFramework != "" {
			project["targetFramework"] = p.TargetFramework
		}
		if p.Link != nil {
			project["link"] = p.Link
		}
		if p.GitRepository != nil {
			project["gitRepository"] = p.GitRepository
		}
		projects = append(projects, project)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"projects": projects,
			"count":    len(projects),
		},
	}, nil
}

// ProjectCreateExecutor handles vercel-project-create
type ProjectCreateExecutor struct{}

func (e *ProjectCreateExecutor) Type() string {
	return "vercel-project-create"
}

func (e *ProjectCreateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, tmplResolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := tmplResolver.ResolveMap(step.Config)

	// Get connection parameters
	apiToken := getString(config, "apiToken")
	apiToken = tmplResolver.ResolveString(apiToken)
	teamID := getString(config, "teamId")
	teamID = tmplResolver.ResolveString(teamID)

	if apiToken == "" {
		return nil, fmt.Errorf("Vercel API token is required")
	}

	// Get project name
	name := getString(config, "name")
	name = tmplResolver.ResolveString(name)
	if name == "" {
		return nil, fmt.Errorf("project name is required")
	}

	client := NewVercelClient(apiToken)

	// Build project request
	projectReq := make(map[string]interface{})
	projectReq["name"] = name

	if teamID != "" {
		projectReq["teamId"] = teamID
	}

	// Git integration
	gitSource := getString(config, "gitSource")
	gitSource = tmplResolver.ResolveString(gitSource)
	if gitSource != "" {
		projectReq["gitSource"] = gitSource
	}
	gitRepoURL := getString(config, "gitRepoUrl")
	gitRepoURL = tmplResolver.ResolveString(gitRepoURL)
	if gitRepoURL != "" {
		projectReq["gitRepoUrl"] = gitRepoURL
	}
	gitBranch := getString(config, "gitBranch")
	gitBranch = tmplResolver.ResolveString(gitBranch)
	if gitBranch != "" {
		projectReq["gitBranch"] = gitBranch
	}

	// Build settings
	buildSettings := make(map[string]interface{})
	buildCommand := getString(config, "buildCommand")
	buildCommand = tmplResolver.ResolveString(buildCommand)
	if buildCommand != "" {
		buildSettings["buildCommand"] = buildCommand
	}
	devCommand := getString(config, "devCommand")
	devCommand = tmplResolver.ResolveString(devCommand)
	if devCommand != "" {
		buildSettings["devCommand"] = devCommand
	}
	installCommand := getString(config, "installCommand")
	installCommand = tmplResolver.ResolveString(installCommand)
	if installCommand != "" {
		buildSettings["installCommand"] = installCommand
	}
	outputDirectory := getString(config, "outputDirectory")
	outputDirectory = tmplResolver.ResolveString(outputDirectory)
	if outputDirectory != "" {
		buildSettings["outputDirectory"] = outputDirectory
	}
	targetFramework := getString(config, "targetFramework")
	targetFramework = tmplResolver.ResolveString(targetFramework)
	if targetFramework != "" {
		buildSettings["targetFramework"] = targetFramework
	}
	framework := getString(config, "framework")
	framework = tmplResolver.ResolveString(framework)
	if framework != "" {
		buildSettings["framework"] = framework
	}
	if len(buildSettings) > 0 {
		projectReq["buildSettings"] = buildSettings
	}

	// Environment variables
	if envVarsRaw, ok := config["environmentVariables"]; ok && envVarsRaw != nil {
		if envVarsMap, ok := envVarsRaw.(map[string]interface{}); ok && len(envVarsMap) > 0 {
			envVars := make([]map[string]interface{}, 0, len(envVarsMap))
			for key, value := range envVarsMap {
				if v, ok := value.(string); ok {
					v = tmplResolver.ResolveString(v)
					envVars = append(envVars, map[string]interface{}{
						"key":    key,
						"value":  v,
						"target": []string{"production", "preview", "development"},
					})
				}
			}
			projectReq["environmentVariables"] = envVars
		}
	}

	path := "/v9/projects"
	if teamID != "" {
		path = fmt.Sprintf("%s?teamId=%s", path, teamID)
	}

	respBody, err := client.doRequest(ctx, "POST", path, projectReq)
	if err != nil {
		return nil, err
	}

	var project VercelProject
	if err := json.Unmarshal(respBody, &project); err != nil {
		return nil, fmt.Errorf("failed to parse project response: %w", err)
	}

	result := map[string]interface{}{
		"id":              project.ID,
		"name":            project.Name,
		"targetFramework": project.TargetFramework,
		"createdAt":       project.CreatedAt,
		"updatedAt":       project.UpdatedAt,
	}
	if project.TeamID != "" {
		result["teamId"] = project.TeamID
	}
	if project.Link != nil {
		result["link"] = project.Link
	}
	if project.GitRepository != nil {
		result["gitRepository"] = project.GitRepository
	}

	return &executor.StepResult{
		Output: result,
	}, nil
}

// DomainAddExecutor handles vercel-domain-add
type DomainAddExecutor struct{}

func (e *DomainAddExecutor) Type() string {
	return "vercel-domain-add"
}

func (e *DomainAddExecutor) Execute(ctx context.Context, step *executor.StepDefinition, tmplResolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := tmplResolver.ResolveMap(step.Config)

	// Get connection parameters
	apiToken := getString(config, "apiToken")
	apiToken = tmplResolver.ResolveString(apiToken)
	teamID := getString(config, "teamId")
	teamID = tmplResolver.ResolveString(teamID)

	if apiToken == "" {
		return nil, fmt.Errorf("Vercel API token is required")
	}

	// Get project ID
	projectID := getString(config, "projectId")
	projectID = tmplResolver.ResolveString(projectID)
	if projectID == "" {
		return nil, fmt.Errorf("projectId is required")
	}

	// Get domain
	domain := getString(config, "domain")
	domain = tmplResolver.ResolveString(domain)
	if domain == "" {
		return nil, fmt.Errorf("domain is required")
	}

	client := NewVercelClient(apiToken)

	// Build domain request
	domainReq := make(map[string]interface{})
	domainReq["name"] = domain

	redirect := getString(config, "redirect")
	redirect = tmplResolver.ResolveString(redirect)
	if redirect != "" {
		domainReq["redirect"] = redirect
	}

	path := fmt.Sprintf("/v9/projects/%s/domains", projectID)
	if teamID != "" {
		path = fmt.Sprintf("%s?teamId=%s", path, teamID)
	}

	respBody, err := client.doRequest(ctx, "POST", path, domainReq)
	if err != nil {
		return nil, err
	}

	var domainObj VercelDomain
	if err := json.Unmarshal(respBody, &domainObj); err != nil {
		return nil, fmt.Errorf("failed to parse domain response: %w", err)
	}

	result := map[string]interface{}{
		"id":        domainObj.ID,
		"name":      domainObj.Name,
		"verified":  domainObj.Verified,
		"projectId": domainObj.ProjectID,
		"createdAt": domainObj.CreatedAt,
		"updatedAt": domainObj.UpdatedAt,
	}
	if domainObj.TeamID != "" {
		result["teamId"] = domainObj.TeamID
	}
	if domainObj.Redirect != "" {
		result["redirect"] = domainObj.Redirect
	}
	if domainObj.Verification != nil {
		result["verification"] = domainObj.Verification
	}
	if len(domainObj.Nameservers) > 0 {
		result["nameservers"] = domainObj.Nameservers
	}

	return &executor.StepResult{
		Output: result,
	}, nil
}

// DomainListExecutor handles vercel-domain-list
type DomainListExecutor struct{}

func (e *DomainListExecutor) Type() string {
	return "vercel-domain-list"
}

func (e *DomainListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, tmplResolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := tmplResolver.ResolveMap(step.Config)

	// Get connection parameters
	apiToken := getString(config, "apiToken")
	apiToken = tmplResolver.ResolveString(apiToken)
	teamID := getString(config, "teamId")
	teamID = tmplResolver.ResolveString(teamID)

	if apiToken == "" {
		return nil, fmt.Errorf("Vercel API token is required")
	}

	// Get project ID
	projectID := getString(config, "projectId")
	projectID = tmplResolver.ResolveString(projectID)
	if projectID == "" {
		return nil, fmt.Errorf("projectId is required")
	}

	client := NewVercelClient(apiToken)

	path := fmt.Sprintf("/v9/projects/%s/domains", projectID)
	if teamID != "" {
		path = fmt.Sprintf("%s?teamId=%s", path, teamID)
	}

	respBody, err := client.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}

	var response struct {
		Domains []VercelDomain `json:"domains"`
	}
	if err := json.Unmarshal(respBody, &response); err != nil {
		return nil, fmt.Errorf("failed to parse domains response: %w", err)
	}

	domains := make([]map[string]interface{}, 0, len(response.Domains))
	for _, d := range response.Domains {
		domain := map[string]interface{}{
			"id":        d.ID,
			"name":      d.Name,
			"verified":  d.Verified,
			"projectId": d.ProjectID,
			"createdAt": d.CreatedAt,
			"updatedAt": d.UpdatedAt,
		}
		if d.TeamID != "" {
			domain["teamId"] = d.TeamID
		}
		if d.Redirect != "" {
			domain["redirect"] = d.Redirect
		}
		if d.Verification != nil {
			domain["verification"] = d.Verification
		}
		if d.ConfiguredBy != "" {
			domain["configuredBy"] = d.ConfiguredBy
		}
		if len(d.Nameservers) > 0 {
			domain["nameservers"] = d.Nameservers
		}
		domains = append(domains, domain)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"domains": domains,
			"count":   len(domains),
		},
	}, nil
}

// EnvSetExecutor handles vercel-env-set
type EnvSetExecutor struct{}

func (e *EnvSetExecutor) Type() string {
	return "vercel-env-set"
}

func (e *EnvSetExecutor) Execute(ctx context.Context, step *executor.StepDefinition, tmplResolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := tmplResolver.ResolveMap(step.Config)

	// Get connection parameters
	apiToken := getString(config, "apiToken")
	apiToken = tmplResolver.ResolveString(apiToken)
	teamID := getString(config, "teamId")
	teamID = tmplResolver.ResolveString(teamID)

	if apiToken == "" {
		return nil, fmt.Errorf("Vercel API token is required")
	}

	// Get project ID
	projectID := getString(config, "projectId")
	projectID = tmplResolver.ResolveString(projectID)
	if projectID == "" {
		return nil, fmt.Errorf("projectId is required")
	}

	// Get key
	key := getString(config, "key")
	key = tmplResolver.ResolveString(key)
	if key == "" {
		return nil, fmt.Errorf("key is required")
	}

	// Get value
	value := getString(config, "value")
	value = tmplResolver.ResolveString(value)
	if value == "" {
		return nil, fmt.Errorf("value is required")
	}

	client := NewVercelClient(apiToken)

	// Build environment variable request
	envReq := make(map[string]interface{})
	envReq["key"] = key
	envReq["value"] = value

	// Set target - default to all environments if not specified
	target := getString(config, "target")
	target = tmplResolver.ResolveString(target)
	var targets []string
	if target != "" {
		targets = []string{target}
	} else {
		targets = []string{"production", "preview", "development"}
	}
	envReq["target"] = targets

	gitBranch := getString(config, "gitBranch")
	gitBranch = tmplResolver.ResolveString(gitBranch)
	if gitBranch != "" {
		envReq["gitBranch"] = gitBranch
	}

	envType := getString(config, "type")
	envType = tmplResolver.ResolveString(envType)
	if envType != "" {
		envReq["type"] = envType
	}

	path := fmt.Sprintf("/v10/projects/%s/env", projectID)
	if teamID != "" {
		path = fmt.Sprintf("%s?teamId=%s", path, teamID)
	}

	respBody, err := client.doRequest(ctx, "POST", path, envReq)
	if err != nil {
		return nil, err
	}

	var envVar VercelEnvVar
	if err := json.Unmarshal(respBody, &envVar); err != nil {
		return nil, fmt.Errorf("failed to parse env var response: %w", err)
	}

	result := map[string]interface{}{
		"id":        envVar.ID,
		"key":       envVar.Key,
		"target":    envVar.Target,
		"projectId": envVar.ProjectID,
		"type":      envVar.Type,
		"createdAt": envVar.CreatedAt,
		"updatedAt": envVar.UpdatedAt,
	}
	if envVar.TeamID != "" {
		result["teamId"] = envVar.TeamID
	}
	if envVar.GitBranch != "" {
		result["gitBranch"] = envVar.GitBranch
	}

	return &executor.StepResult{
		Output: result,
	}, nil
}

// EnvListExecutor handles vercel-env-list
type EnvListExecutor struct{}

func (e *EnvListExecutor) Type() string {
	return "vercel-env-list"
}

func (e *EnvListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, tmplResolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := tmplResolver.ResolveMap(step.Config)

	// Get connection parameters
	apiToken := getString(config, "apiToken")
	apiToken = tmplResolver.ResolveString(apiToken)
	teamID := getString(config, "teamId")
	teamID = tmplResolver.ResolveString(teamID)

	if apiToken == "" {
		return nil, fmt.Errorf("Vercel API token is required")
	}

	// Get project ID
	projectID := getString(config, "projectId")
	projectID = tmplResolver.ResolveString(projectID)
	if projectID == "" {
		return nil, fmt.Errorf("projectId is required")
	}

	client := NewVercelClient(apiToken)

	path := fmt.Sprintf("/v10/projects/%s/env", projectID)
	if teamID != "" {
		path = fmt.Sprintf("%s?teamId=%s", path, teamID)
	}

	respBody, err := client.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}

	var response struct {
		EnvVars []VercelEnvVar `json:"envs"`
	}
	if err := json.Unmarshal(respBody, &response); err != nil {
		return nil, fmt.Errorf("failed to parse env vars response: %w", err)
	}

	envVars := make([]map[string]interface{}, 0, len(response.EnvVars))
	for _, e := range response.EnvVars {
		envVar := map[string]interface{}{
			"id":        e.ID,
			"key":       e.Key,
			"target":    e.Target,
			"projectId": e.ProjectID,
			"type":      e.Type,
			"createdAt": e.CreatedAt,
			"updatedAt": e.UpdatedAt,
		}
		if e.TeamID != "" {
			envVar["teamId"] = e.TeamID
		}
		if e.GitBranch != "" {
			envVar["gitBranch"] = e.GitBranch
		}
		if e.SourceType != "" {
			envVar["sourceType"] = e.SourceType
		}
		envVars = append(envVars, envVar)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"envVars": envVars,
			"count":   len(envVars),
		},
	}, nil
}

// ============================================================================
// MAIN
// ============================================================================

func main() {
	port := os.Getenv("SKILL_PORT")
	if port == "" {
		port = "50126"
	}

	server := grpc.NewSkillServer("skill-vercel", "1.0.0")

	// Register all executors with their schemas
	server.RegisterExecutorWithSchema("vercel-deploy", &DeployExecutor{}, DeploySchema)
	server.RegisterExecutorWithSchema("vercel-deployment-list", &DeploymentListExecutor{}, DeploymentListSchema)
	server.RegisterExecutorWithSchema("vercel-deployment-get", &DeploymentGetExecutor{}, DeploymentGetSchema)
	server.RegisterExecutorWithSchema("vercel-project-list", &ProjectListExecutor{}, ProjectListSchema)
	server.RegisterExecutorWithSchema("vercel-project-create", &ProjectCreateExecutor{}, ProjectCreateSchema)
	server.RegisterExecutorWithSchema("vercel-domain-add", &DomainAddExecutor{}, DomainAddSchema)
	server.RegisterExecutorWithSchema("vercel-domain-list", &DomainListExecutor{}, DomainListSchema)
	server.RegisterExecutorWithSchema("vercel-env-set", &EnvSetExecutor{}, EnvSetSchema)
	server.RegisterExecutorWithSchema("vercel-env-list", &EnvListExecutor{}, EnvListSchema)

	fmt.Printf("Starting skill-vercel gRPC server on port %s\n", port)
	if err := server.Serve(port); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to start server: %v\n", err)
		os.Exit(1)
	}
}
