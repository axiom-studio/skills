package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/axiom-studio/skills.sdk/executor"
	"github.com/axiom-studio/skills.sdk/grpc"
	"github.com/axiom-studio/skills.sdk/resolver"
)

const (
	iconArgoCD = "git-branch"
)

// ArgoCDClient represents an ArgoCD API client with authentication
type ArgoCDClient struct {
	BaseURL    string
	AuthToken  string
	HTTPClient *http.Client
}

// ArgoCDAuthResponse represents the response from the ArgoCD login endpoint
type ArgoCDAuthResponse struct {
	Token        string `json:"token"`
	DisplayName  string `json:"displayName"`
	LoggedInAt   string `json:"loggedInAt"`
	ExpiresAt    string `json:"expiresAt"`
	IssuedAt     string `json:"issuedAt"`
	NotBefore    string `json:"notBefore"`
	Subject      string `json:"subject"`
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	TokenType    string `json:"tokenType"`
}

// Client cache for connection reuse
var (
	clients   = make(map[string]*ArgoCDClient)
	clientMux sync.RWMutex
)

func main() {
	// Get port from env or use default
	port := os.Getenv("SKILL_PORT")
	if port == "" {
		port = "50073"
	}

	// Create skill server
	server := grpc.NewSkillServer("skill-argocd", "1.0.0")

	// Register Application executors with schemas
	server.RegisterExecutorWithSchema("argocd-app-list", &AppListExecutor{}, AppListSchema)
	server.RegisterExecutorWithSchema("argocd-app-get", &AppGetExecutor{}, AppGetSchema)
	server.RegisterExecutorWithSchema("argocd-app-create", &AppCreateExecutor{}, AppCreateSchema)
	server.RegisterExecutorWithSchema("argocd-app-update", &AppUpdateExecutor{}, AppUpdateSchema)
	server.RegisterExecutorWithSchema("argocd-app-delete", &AppDeleteExecutor{}, AppDeleteSchema)
	server.RegisterExecutorWithSchema("argocd-app-sync", &AppSyncExecutor{}, AppSyncSchema)
	server.RegisterExecutorWithSchema("argocd-app-rollback", &AppRollbackExecutor{}, AppRollbackSchema)
	server.RegisterExecutorWithSchema("argocd-app-refresh", &AppRefreshExecutor{}, AppRefreshSchema)
	server.RegisterExecutorWithSchema("argocd-app-restart", &AppRestartExecutor{}, AppRestartSchema)
	server.RegisterExecutorWithSchema("argocd-app-status", &AppStatusExecutor{}, AppStatusSchema)
	server.RegisterExecutorWithSchema("argocd-app-history", &AppHistoryExecutor{}, AppHistorySchema)
	server.RegisterExecutorWithSchema("argocd-app-diff", &AppDiffExecutor{}, AppDiffSchema)
	server.RegisterExecutorWithSchema("argocd-app-resources", &AppResourcesExecutor{}, AppResourcesSchema)
	server.RegisterExecutorWithSchema("argocd-app-logs", &AppLogsExecutor{}, AppLogsSchema)

	// Register Project executors with schemas
	server.RegisterExecutorWithSchema("argocd-project-list", &ProjectListExecutor{}, ProjectListSchema)
	server.RegisterExecutorWithSchema("argocd-project-get", &ProjectGetExecutor{}, ProjectGetSchema)
	server.RegisterExecutorWithSchema("argocd-project-create", &ProjectCreateExecutor{}, ProjectCreateSchema)

	// Register Repository executors with schemas
	server.RegisterExecutorWithSchema("argocd-repo-add", &RepoAddExecutor{}, RepoAddSchema)
	server.RegisterExecutorWithSchema("argocd-repo-list", &RepoListExecutor{}, RepoListSchema)

	// Register Cluster executors with schemas
	server.RegisterExecutorWithSchema("argocd-cluster-list", &ClusterListExecutor{}, ClusterListSchema)
	server.RegisterExecutorWithSchema("argocd-cluster-create", &ClusterCreateExecutor{}, ClusterCreateSchema)

	// Register System executors with schemas
	server.RegisterExecutorWithSchema("argocd-version", &VersionExecutor{}, VersionSchema)

	fmt.Printf("Starting skill-argocd gRPC server on port %s\n", port)
	if err := server.Serve(port); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to serve: %v\n", err)
		os.Exit(1)
	}
}

// ============================================================================
// ARGOCD CLIENT HELPERS
// ============================================================================

// getArgoCDClient returns an ArgoCD client (cached) with authentication
func getArgoCDClient(serverURL, username, password, authToken, insecure string) (*ArgoCDClient, error) {
	if serverURL == "" {
		return nil, fmt.Errorf("ArgoCD server URL is required")
	}

	// Normalize server URL
	serverURL = strings.TrimSuffix(serverURL, "/")
	if !strings.HasPrefix(serverURL, "http://") && !strings.HasPrefix(serverURL, "https://") {
		serverURL = "https://" + serverURL
	}

	// Create cache key
	cacheKey := fmt.Sprintf("%s:%s:%s", serverURL, username, authToken)

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

	// Create HTTP client with optional insecure TLS
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: insecure == "true"},
	}
	jar, _ := cookiejar.New(nil)
	httpClient := &http.Client{
		Transport: tr,
		Jar:       jar,
		Timeout:   60 * time.Second,
	}

	client = &ArgoCDClient{
		BaseURL:    serverURL,
		HTTPClient: httpClient,
	}

	// Authenticate if credentials provided
	var token string
	if authToken != "" {
		token = authToken
	} else if username != "" && password != "" {
		// Login to get JWT token
		authToken, err := client.login(username, password)
		if err != nil {
			return nil, fmt.Errorf("failed to authenticate with ArgoCD: %w", err)
		}
		token = authToken
	}

	if token == "" {
		return nil, fmt.Errorf("either authToken or username/password must be provided")
	}

	client.AuthToken = token
	clients[cacheKey] = client
	return client, nil
}

// login authenticates with ArgoCD and returns a JWT token
func (c *ArgoCDClient) login(username, password string) (string, error) {
	loginURL := fmt.Sprintf("%s/api/v1/session", c.BaseURL)

	loginReq := map[string]interface{}{
		"username": username,
		"password": password,
	}

	jsonBody, err := json.Marshal(loginReq)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest("POST", loginURL, bytes.NewReader(jsonBody))
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("login failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	var authResp ArgoCDAuthResponse
	if err := json.Unmarshal(respBody, &authResp); err != nil {
		return "", err
	}

	return authResp.Token, nil
}

// doRequest performs an HTTP request to the ArgoCD API
func (c *ArgoCDClient) doRequest(ctx context.Context, method, path string, body interface{}) ([]byte, error) {
	url := fmt.Sprintf("%s/api/v1/%s", c.BaseURL, path)

	var reqBody io.Reader
	if body != nil {
		jsonBody, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
		reqBody = bytes.NewReader(jsonBody)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.AuthToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	// Handle error responses
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("ArgoCD API error (%d): %s", resp.StatusCode, string(respBody))
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
		// Handle string "true"/"false"
		if s, ok := v.(string); ok {
			return strings.ToLower(s) == "true"
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

// parseArgoCDConfig extracts ArgoCD connection configuration from config map
func parseArgoCDConfig(config map[string]interface{}, r *resolver.Resolver) (serverURL, username, password, authToken, insecure string) {
	serverURL = r.ResolveString(getString(config, "server"))
	username = r.ResolveString(getString(config, "username"))
	password = r.ResolveString(getString(config, "password"))
	authToken = r.ResolveString(getString(config, "authToken"))
	insecure = getString(config, "insecure")
	return
}

// ============================================================================
// SCHEMAS
// ============================================================================

// AppListSchema is the UI schema for argocd-app-list
var AppListSchema = resolver.NewSchemaBuilder("argocd-app-list").
	WithName("List Applications").
	WithCategory("action").
	WithIcon(iconArgoCD).
	WithDescription("List all ArgoCD applications").
	AddSection("Connection").
		AddExpressionField("server", "Server URL",
			resolver.WithRequired(),
			resolver.WithPlaceholder("https://argocd.example.com"),
			resolver.WithHint("ArgoCD server URL"),
		).
		AddExpressionField("username", "Username",
			resolver.WithPlaceholder("admin"),
			resolver.WithHint("ArgoCD username (optional if using authToken)"),
		).
		AddExpressionField("password", "Password",
			resolver.WithSensitive(),
			resolver.WithHint("ArgoCD password (optional if using authToken)"),
		).
		AddExpressionField("authToken", "Auth Token",
			resolver.WithSensitive(),
			resolver.WithHint("JWT auth token (optional if using username/password)"),
		).
		AddToggleField("insecure", "Skip TLS Verify",
			resolver.WithDefault(false),
			resolver.WithHint("Skip TLS certificate verification"),
		).
		EndSection().
	AddSection("Options").
		AddExpressionField("project", "Project",
			resolver.WithHint("Filter by project name"),
		).
		AddExpressionField("namePattern", "Name Pattern",
			resolver.WithPlaceholder("my-app-*"),
			resolver.WithHint("Filter by name pattern (wildcards supported)"),
		).
		EndSection().
	Build()

// AppGetSchema is the UI schema for argocd-app-get
var AppGetSchema = resolver.NewSchemaBuilder("argocd-app-get").
	WithName("Get Application").
	WithCategory("action").
	WithIcon(iconArgoCD).
	WithDescription("Get details of a specific ArgoCD application").
	AddSection("Connection").
		AddExpressionField("server", "Server URL",
			resolver.WithRequired(),
			resolver.WithPlaceholder("https://argocd.example.com"),
			resolver.WithHint("ArgoCD server URL"),
		).
		AddExpressionField("username", "Username",
			resolver.WithPlaceholder("admin"),
			resolver.WithHint("ArgoCD username (optional if using authToken)"),
		).
		AddExpressionField("password", "Password",
			resolver.WithSensitive(),
			resolver.WithHint("ArgoCD password (optional if using authToken)"),
		).
		AddExpressionField("authToken", "Auth Token",
			resolver.WithSensitive(),
			resolver.WithHint("JWT auth token (optional if using username/password)"),
		).
		AddToggleField("insecure", "Skip TLS Verify",
			resolver.WithDefault(false),
			resolver.WithHint("Skip TLS certificate verification"),
		).
		EndSection().
	AddSection("Application").
		AddExpressionField("appName", "Application Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-app"),
			resolver.WithHint("Name of the application"),
		).
		AddToggleField("refresh", "Refresh",
			resolver.WithDefault(false),
			resolver.WithHint("Force refresh application data"),
		).
		EndSection().
	Build()

// AppCreateSchema is the UI schema for argocd-app-create
var AppCreateSchema = resolver.NewSchemaBuilder("argocd-app-create").
	WithName("Create Application").
	WithCategory("action").
	WithIcon(iconArgoCD).
	WithDescription("Create a new ArgoCD application").
	AddSection("Connection").
		AddExpressionField("server", "Server URL",
			resolver.WithRequired(),
			resolver.WithPlaceholder("https://argocd.example.com"),
			resolver.WithHint("ArgoCD server URL"),
		).
		AddExpressionField("username", "Username",
			resolver.WithPlaceholder("admin"),
			resolver.WithHint("ArgoCD username (optional if using authToken)"),
		).
		AddExpressionField("password", "Password",
			resolver.WithSensitive(),
			resolver.WithHint("ArgoCD password (optional if using authToken)"),
		).
		AddExpressionField("authToken", "Auth Token",
			resolver.WithSensitive(),
			resolver.WithHint("JWT auth token (optional if using username/password)"),
		).
		AddToggleField("insecure", "Skip TLS Verify",
			resolver.WithDefault(false),
			resolver.WithHint("Skip TLS certificate verification"),
		).
		EndSection().
	AddSection("Application").
		AddExpressionField("name", "Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-app"),
			resolver.WithHint("Application name"),
		).
		AddExpressionField("project", "Project",
			resolver.WithDefault("default"),
			resolver.WithPlaceholder("default"),
			resolver.WithHint("Project name"),
		).
		EndSection().
	AddSection("Source").
		AddExpressionField("repoUrl", "Repository URL",
			resolver.WithRequired(),
			resolver.WithPlaceholder("https://github.com/org/repo.git"),
			resolver.WithHint("Git repository URL"),
		).
		AddExpressionField("path", "Path",
			resolver.WithDefault("."),
			resolver.WithPlaceholder("."),
			resolver.WithHint("Path in the repository"),
		).
		AddExpressionField("revision", "Revision",
			resolver.WithDefault("HEAD"),
			resolver.WithPlaceholder("HEAD"),
			resolver.WithHint("Branch, tag, or commit"),
		).
		AddExpressionField("targetRevision", "Target Revision",
			resolver.WithHint("Alias for revision"),
		).
		EndSection().
	AddSection("Destination").
		AddExpressionField("destServer", "Destination Server",
			resolver.WithDefault("https://kubernetes.default.svc"),
			resolver.WithPlaceholder("https://kubernetes.default.svc"),
			resolver.WithHint("Kubernetes cluster URL"),
		).
		AddExpressionField("destNamespace", "Destination Namespace",
			resolver.WithRequired(),
			resolver.WithPlaceholder("default"),
			resolver.WithHint("Kubernetes namespace"),
		).
		EndSection().
	AddSection("Sync Policy").
		AddSelectField("syncPolicy", "Sync Policy",
			[]resolver.SelectOption{
				{Label: "Manual", Value: "manual"},
				{Label: "Automated", Value: "automated"},
			},
			resolver.WithDefault("manual"),
			resolver.WithHint("Sync policy mode"),
		).
		AddToggleField("prune", "Prune",
			resolver.WithDefault(false),
			resolver.WithHint("Prune resources not in Git"),
		).
		AddToggleField("selfHeal", "Self Heal",
			resolver.WithDefault(false),
			resolver.WithHint("Auto-correct drift"),
		).
		AddToggleField("allowEmpty", "Allow Empty",
			resolver.WithDefault(false),
			resolver.WithHint("Allow empty manifests"),
		).
		EndSection().
	AddSection("Advanced").
		AddJSONField("ignoreDifferences", "Ignore Differences",
			resolver.WithHeight(100),
			resolver.WithHint("Resources differences to ignore"),
		).
		AddTagsField("finalizers", "Finalizers",
			resolver.WithHint("Application finalizers"),
		).
		EndSection().
	Build()

// AppUpdateSchema is the UI schema for argocd-app-update
var AppUpdateSchema = resolver.NewSchemaBuilder("argocd-app-update").
	WithName("Update Application").
	WithCategory("action").
	WithIcon(iconArgoCD).
	WithDescription("Update an existing ArgoCD application").
	AddSection("Connection").
		AddExpressionField("server", "Server URL",
			resolver.WithRequired(),
			resolver.WithPlaceholder("https://argocd.example.com"),
			resolver.WithHint("ArgoCD server URL"),
		).
		AddExpressionField("username", "Username",
			resolver.WithPlaceholder("admin"),
			resolver.WithHint("ArgoCD username (optional if using authToken)"),
		).
		AddExpressionField("password", "Password",
			resolver.WithSensitive(),
			resolver.WithHint("ArgoCD password (optional if using authToken)"),
		).
		AddExpressionField("authToken", "Auth Token",
			resolver.WithSensitive(),
			resolver.WithHint("JWT auth token (optional if using username/password)"),
		).
		AddToggleField("insecure", "Skip TLS Verify",
			resolver.WithDefault(false),
			resolver.WithHint("Skip TLS certificate verification"),
		).
		EndSection().
	AddSection("Application").
		AddExpressionField("name", "Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-app"),
			resolver.WithHint("Application name"),
		).
		EndSection().
	AddSection("Source (Optional)").
		AddExpressionField("repoUrl", "Repository URL",
			resolver.WithHint("New repository URL"),
		).
		AddExpressionField("path", "Path",
			resolver.WithHint("New path in repository"),
		).
		AddExpressionField("revision", "Revision",
			resolver.WithHint("New branch, tag, or commit"),
		).
		EndSection().
	AddSection("Destination (Optional)").
		AddExpressionField("destNamespace", "Destination Namespace",
			resolver.WithHint("New Kubernetes namespace"),
		).
		EndSection().
	AddSection("Sync Policy").
		AddSelectField("syncPolicy", "Sync Policy",
			[]resolver.SelectOption{
				{Label: "Manual", Value: "manual"},
				{Label: "Automated", Value: "automated"},
			},
			resolver.WithHint("Sync policy mode"),
		).
		AddToggleField("prune", "Prune",
			resolver.WithHint("Prune resources not in Git"),
		).
		AddToggleField("selfHeal", "Self Heal",
			resolver.WithHint("Auto-correct drift"),
		).
		EndSection().
	Build()

// AppDeleteSchema is the UI schema for argocd-app-delete
var AppDeleteSchema = resolver.NewSchemaBuilder("argocd-app-delete").
	WithName("Delete Application").
	WithCategory("action").
	WithIcon(iconArgoCD).
	WithDescription("Delete an ArgoCD application").
	AddSection("Connection").
		AddExpressionField("server", "Server URL",
			resolver.WithRequired(),
			resolver.WithPlaceholder("https://argocd.example.com"),
			resolver.WithHint("ArgoCD server URL"),
		).
		AddExpressionField("username", "Username",
			resolver.WithPlaceholder("admin"),
			resolver.WithHint("ArgoCD username (optional if using authToken)"),
		).
		AddExpressionField("password", "Password",
			resolver.WithSensitive(),
			resolver.WithHint("ArgoCD password (optional if using authToken)"),
		).
		AddExpressionField("authToken", "Auth Token",
			resolver.WithSensitive(),
			resolver.WithHint("JWT auth token (optional if using username/password)"),
		).
		AddToggleField("insecure", "Skip TLS Verify",
			resolver.WithDefault(false),
			resolver.WithHint("Skip TLS certificate verification"),
		).
		EndSection().
	AddSection("Application").
		AddExpressionField("appName", "Application Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-app"),
			resolver.WithHint("Name of the application to delete"),
		).
		EndSection().
	AddSection("Options").
		AddToggleField("cascade", "Cascade Delete",
			resolver.WithDefault(true),
			resolver.WithHint("Delete associated resources"),
		).
		EndSection().
	Build()

// AppSyncSchema is the UI schema for argocd-app-sync
var AppSyncSchema = resolver.NewSchemaBuilder("argocd-app-sync").
	WithName("Sync Application").
	WithCategory("action").
	WithIcon(iconArgoCD).
	WithDescription("Sync an ArgoCD application to Git state").
	AddSection("Connection").
		AddExpressionField("server", "Server URL",
			resolver.WithRequired(),
			resolver.WithPlaceholder("https://argocd.example.com"),
			resolver.WithHint("ArgoCD server URL"),
		).
		AddExpressionField("username", "Username",
			resolver.WithPlaceholder("admin"),
			resolver.WithHint("ArgoCD username (optional if using authToken)"),
		).
		AddExpressionField("password", "Password",
			resolver.WithSensitive(),
			resolver.WithHint("ArgoCD password (optional if using authToken)"),
		).
		AddExpressionField("authToken", "Auth Token",
			resolver.WithSensitive(),
			resolver.WithHint("JWT auth token (optional if using username/password)"),
		).
		AddToggleField("insecure", "Skip TLS Verify",
			resolver.WithDefault(false),
			resolver.WithHint("Skip TLS certificate verification"),
		).
		EndSection().
	AddSection("Application").
		AddExpressionField("appName", "Application Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-app"),
			resolver.WithHint("Name of the application to sync"),
		).
		EndSection().
	AddSection("Sync Options").
		AddExpressionField("revision", "Revision",
			resolver.WithHint("Specific revision to sync to"),
		).
		AddToggleField("prune", "Prune",
			resolver.WithDefault(false),
			resolver.WithHint("Prune resources not in Git"),
		).
		AddToggleField("dryRun", "Dry Run",
			resolver.WithDefault(false),
			resolver.WithHint("Preview sync without applying"),
		).
		AddToggleField("force", "Force",
			resolver.WithDefault(false),
			resolver.WithHint("Force sync even if out of sync"),
		).
		AddTagsField("resources", "Resources",
			resolver.WithHint("Specific resources to sync (format: kind:namespace/name)"),
		).
		EndSection().
	Build()

// AppRollbackSchema is the UI schema for argocd-app-rollback
var AppRollbackSchema = resolver.NewSchemaBuilder("argocd-app-rollback").
	WithName("Rollback Application").
	WithCategory("action").
	WithIcon(iconArgoCD).
	WithDescription("Rollback an ArgoCD application to a previous revision").
	AddSection("Connection").
		AddExpressionField("server", "Server URL",
			resolver.WithRequired(),
			resolver.WithPlaceholder("https://argocd.example.com"),
			resolver.WithHint("ArgoCD server URL"),
		).
		AddExpressionField("username", "Username",
			resolver.WithPlaceholder("admin"),
			resolver.WithHint("ArgoCD username (optional if using authToken)"),
		).
		AddExpressionField("password", "Password",
			resolver.WithSensitive(),
			resolver.WithHint("ArgoCD password (optional if using authToken)"),
		).
		AddExpressionField("authToken", "Auth Token",
			resolver.WithSensitive(),
			resolver.WithHint("JWT auth token (optional if using username/password)"),
		).
		AddToggleField("insecure", "Skip TLS Verify",
			resolver.WithDefault(false),
			resolver.WithHint("Skip TLS certificate verification"),
		).
		EndSection().
	AddSection("Application").
		AddExpressionField("appName", "Application Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-app"),
			resolver.WithHint("Name of the application"),
		).
		EndSection().
	AddSection("Rollback").
		AddNumberField("historyId", "History ID",
			resolver.WithRequired(),
			resolver.WithHint("History revision ID to rollback to"),
		).
		AddToggleField("prune", "Prune",
			resolver.WithDefault(false),
			resolver.WithHint("Prune resources not in target revision"),
		).
		AddToggleField("dryRun", "Dry Run",
			resolver.WithDefault(false),
			resolver.WithHint("Preview rollback without applying"),
		).
		EndSection().
	Build()

// AppRefreshSchema is the UI schema for argocd-app-refresh
var AppRefreshSchema = resolver.NewSchemaBuilder("argocd-app-refresh").
	WithName("Refresh Application").
	WithCategory("action").
	WithIcon(iconArgoCD).
	WithDescription("Refresh an ArgoCD application's state from Git").
	AddSection("Connection").
		AddExpressionField("server", "Server URL",
			resolver.WithRequired(),
			resolver.WithPlaceholder("https://argocd.example.com"),
			resolver.WithHint("ArgoCD server URL"),
		).
		AddExpressionField("username", "Username",
			resolver.WithPlaceholder("admin"),
			resolver.WithHint("ArgoCD username (optional if using authToken)"),
		).
		AddExpressionField("password", "Password",
			resolver.WithSensitive(),
			resolver.WithHint("ArgoCD password (optional if using authToken)"),
		).
		AddExpressionField("authToken", "Auth Token",
			resolver.WithSensitive(),
			resolver.WithHint("JWT auth token (optional if using username/password)"),
		).
		AddToggleField("insecure", "Skip TLS Verify",
			resolver.WithDefault(false),
			resolver.WithHint("Skip TLS certificate verification"),
		).
		EndSection().
	AddSection("Application").
		AddExpressionField("appName", "Application Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-app"),
			resolver.WithHint("Name of the application"),
		).
		EndSection().
	AddSection("Options").
		AddToggleField("hard", "Hard Refresh",
			resolver.WithDefault(false),
			resolver.WithHint("Force full refresh including manifest generation"),
		).
		EndSection().
	Build()

// AppRestartSchema is the UI schema for argocd-app-restart
var AppRestartSchema = resolver.NewSchemaBuilder("argocd-app-restart").
	WithName("Restart Application").
	WithCategory("action").
	WithIcon(iconArgoCD).
	WithDescription("Restart an ArgoCD application (hard refresh + sync)").
	AddSection("Connection").
		AddExpressionField("server", "Server URL",
			resolver.WithRequired(),
			resolver.WithPlaceholder("https://argocd.example.com"),
			resolver.WithHint("ArgoCD server URL"),
		).
		AddExpressionField("username", "Username",
			resolver.WithPlaceholder("admin"),
			resolver.WithHint("ArgoCD username (optional if using authToken)"),
		).
		AddExpressionField("password", "Password",
			resolver.WithSensitive(),
			resolver.WithHint("ArgoCD password (optional if using authToken)"),
		).
		AddExpressionField("authToken", "Auth Token",
			resolver.WithSensitive(),
			resolver.WithHint("JWT auth token (optional if using username/password)"),
		).
		AddToggleField("insecure", "Skip TLS Verify",
			resolver.WithDefault(false),
			resolver.WithHint("Skip TLS certificate verification"),
		).
		EndSection().
	AddSection("Application").
		AddExpressionField("appName", "Application Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-app"),
			resolver.WithHint("Name of the application"),
		).
		EndSection().
	AddSection("Options").
		AddToggleField("waitForSync", "Wait for Sync",
			resolver.WithDefault(false),
			resolver.WithHint("Wait for sync to complete"),
		).
		EndSection().
	Build()

// AppStatusSchema is the UI schema for argocd-app-status
var AppStatusSchema = resolver.NewSchemaBuilder("argocd-app-status").
	WithName("Get Application Status").
	WithCategory("action").
	WithIcon(iconArgoCD).
	WithDescription("Get the sync and health status of an ArgoCD application").
	AddSection("Connection").
		AddExpressionField("server", "Server URL",
			resolver.WithRequired(),
			resolver.WithPlaceholder("https://argocd.example.com"),
			resolver.WithHint("ArgoCD server URL"),
		).
		AddExpressionField("username", "Username",
			resolver.WithPlaceholder("admin"),
			resolver.WithHint("ArgoCD username (optional if using authToken)"),
		).
		AddExpressionField("password", "Password",
			resolver.WithSensitive(),
			resolver.WithHint("ArgoCD password (optional if using authToken)"),
		).
		AddExpressionField("authToken", "Auth Token",
			resolver.WithSensitive(),
			resolver.WithHint("JWT auth token (optional if using username/password)"),
		).
		AddToggleField("insecure", "Skip TLS Verify",
			resolver.WithDefault(false),
			resolver.WithHint("Skip TLS certificate verification"),
		).
		EndSection().
	AddSection("Application").
		AddExpressionField("appName", "Application Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-app"),
			resolver.WithHint("Name of the application"),
		).
		EndSection().
	AddSection("Options").
		AddToggleField("refresh", "Refresh",
			resolver.WithDefault(false),
			resolver.WithHint("Force refresh before getting status"),
		).
		EndSection().
	Build()

// AppHistorySchema is the UI schema for argocd-app-history
var AppHistorySchema = resolver.NewSchemaBuilder("argocd-app-history").
	WithName("Get Application History").
	WithCategory("action").
	WithIcon(iconArgoCD).
	WithDescription("Get the deployment history of an ArgoCD application").
	AddSection("Connection").
		AddExpressionField("server", "Server URL",
			resolver.WithRequired(),
			resolver.WithPlaceholder("https://argocd.example.com"),
			resolver.WithHint("ArgoCD server URL"),
		).
		AddExpressionField("username", "Username",
			resolver.WithPlaceholder("admin"),
			resolver.WithHint("ArgoCD username (optional if using authToken)"),
		).
		AddExpressionField("password", "Password",
			resolver.WithSensitive(),
			resolver.WithHint("ArgoCD password (optional if using authToken)"),
		).
		AddExpressionField("authToken", "Auth Token",
			resolver.WithSensitive(),
			resolver.WithHint("JWT auth token (optional if using username/password)"),
		).
		AddToggleField("insecure", "Skip TLS Verify",
			resolver.WithDefault(false),
			resolver.WithHint("Skip TLS certificate verification"),
		).
		EndSection().
	AddSection("Application").
		AddExpressionField("appName", "Application Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-app"),
			resolver.WithHint("Name of the application"),
		).
		EndSection().
	AddSection("Options").
		AddNumberField("limit", "Limit",
			resolver.WithDefault(10),
			resolver.WithHint("Number of history entries to return"),
		).
		EndSection().
	Build()

// AppDiffSchema is the UI schema for argocd-app-diff
var AppDiffSchema = resolver.NewSchemaBuilder("argocd-app-diff").
	WithName("Get Application Diff").
	WithCategory("action").
	WithIcon(iconArgoCD).
	WithDescription("Get the diff between Git and live state for an application").
	AddSection("Connection").
		AddExpressionField("server", "Server URL",
			resolver.WithRequired(),
			resolver.WithPlaceholder("https://argocd.example.com"),
			resolver.WithHint("ArgoCD server URL"),
		).
		AddExpressionField("username", "Username",
			resolver.WithPlaceholder("admin"),
			resolver.WithHint("ArgoCD username (optional if using authToken)"),
		).
		AddExpressionField("password", "Password",
			resolver.WithSensitive(),
			resolver.WithHint("ArgoCD password (optional if using authToken)"),
		).
		AddExpressionField("authToken", "Auth Token",
			resolver.WithSensitive(),
			resolver.WithHint("JWT auth token (optional if using username/password)"),
		).
		AddToggleField("insecure", "Skip TLS Verify",
			resolver.WithDefault(false),
			resolver.WithHint("Skip TLS certificate verification"),
		).
		EndSection().
	AddSection("Application").
		AddExpressionField("appName", "Application Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-app"),
			resolver.WithHint("Name of the application"),
		).
		EndSection().
	AddSection("Options").
		AddToggleField("refresh", "Refresh",
			resolver.WithDefault(false),
			resolver.WithHint("Refresh before getting diff"),
		).
		EndSection().
	Build()

// AppResourcesSchema is the UI schema for argocd-app-resources
var AppResourcesSchema = resolver.NewSchemaBuilder("argocd-app-resources").
	WithName("Get Application Resources").
	WithCategory("action").
	WithIcon(iconArgoCD).
	WithDescription("Get the Kubernetes resources managed by an ArgoCD application").
	AddSection("Connection").
		AddExpressionField("server", "Server URL",
			resolver.WithRequired(),
			resolver.WithPlaceholder("https://argocd.example.com"),
			resolver.WithHint("ArgoCD server URL"),
		).
		AddExpressionField("username", "Username",
			resolver.WithPlaceholder("admin"),
			resolver.WithHint("ArgoCD username (optional if using authToken)"),
		).
		AddExpressionField("password", "Password",
			resolver.WithSensitive(),
			resolver.WithHint("ArgoCD password (optional if using authToken)"),
		).
		AddExpressionField("authToken", "Auth Token",
			resolver.WithSensitive(),
			resolver.WithHint("JWT auth token (optional if using username/password)"),
		).
		AddToggleField("insecure", "Skip TLS Verify",
			resolver.WithDefault(false),
			resolver.WithHint("Skip TLS certificate verification"),
		).
		EndSection().
	AddSection("Application").
		AddExpressionField("appName", "Application Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-app"),
			resolver.WithHint("Name of the application"),
		).
		EndSection().
	Build()

// AppLogsSchema is the UI schema for argocd-app-logs
var AppLogsSchema = resolver.NewSchemaBuilder("argocd-app-logs").
	WithName("Get Application Logs").
	WithCategory("action").
	WithIcon(iconArgoCD).
	WithDescription("Get logs from an ArgoCD application's pods").
	AddSection("Connection").
		AddExpressionField("server", "Server URL",
			resolver.WithRequired(),
			resolver.WithPlaceholder("https://argocd.example.com"),
			resolver.WithHint("ArgoCD server URL"),
		).
		AddExpressionField("username", "Username",
			resolver.WithPlaceholder("admin"),
			resolver.WithHint("ArgoCD username (optional if using authToken)"),
		).
		AddExpressionField("password", "Password",
			resolver.WithSensitive(),
			resolver.WithHint("ArgoCD password (optional if using authToken)"),
		).
		AddExpressionField("authToken", "Auth Token",
			resolver.WithSensitive(),
			resolver.WithHint("JWT auth token (optional if using username/password)"),
		).
		AddToggleField("insecure", "Skip TLS Verify",
			resolver.WithDefault(false),
			resolver.WithHint("Skip TLS certificate verification"),
		).
		EndSection().
	AddSection("Application").
		AddExpressionField("appName", "Application Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-app"),
			resolver.WithHint("Name of the application"),
		).
		EndSection().
	AddSection("Options").
		AddExpressionField("container", "Container",
			resolver.WithHint("Specific container name"),
		).
		AddNumberField("tailLines", "Tail Lines",
			resolver.WithDefault(100),
			resolver.WithHint("Number of log lines to retrieve"),
		).
		AddToggleField("follow", "Follow",
			resolver.WithDefault(false),
			resolver.WithHint("Follow logs (streaming)"),
		).
		EndSection().
	Build()

// ProjectListSchema is the UI schema for argocd-project-list
var ProjectListSchema = resolver.NewSchemaBuilder("argocd-project-list").
	WithName("List Projects").
	WithCategory("action").
	WithIcon(iconArgoCD).
	WithDescription("List all ArgoCD projects").
	AddSection("Connection").
		AddExpressionField("server", "Server URL",
			resolver.WithRequired(),
			resolver.WithPlaceholder("https://argocd.example.com"),
			resolver.WithHint("ArgoCD server URL"),
		).
		AddExpressionField("username", "Username",
			resolver.WithPlaceholder("admin"),
			resolver.WithHint("ArgoCD username (optional if using authToken)"),
		).
		AddExpressionField("password", "Password",
			resolver.WithSensitive(),
			resolver.WithHint("ArgoCD password (optional if using authToken)"),
		).
		AddExpressionField("authToken", "Auth Token",
			resolver.WithSensitive(),
			resolver.WithHint("JWT auth token (optional if using username/password)"),
		).
		AddToggleField("insecure", "Skip TLS Verify",
			resolver.WithDefault(false),
			resolver.WithHint("Skip TLS certificate verification"),
		).
		EndSection().
	Build()

// ProjectGetSchema is the UI schema for argocd-project-get
var ProjectGetSchema = resolver.NewSchemaBuilder("argocd-project-get").
	WithName("Get Project").
	WithCategory("action").
	WithIcon(iconArgoCD).
	WithDescription("Get details of a specific ArgoCD project").
	AddSection("Connection").
		AddExpressionField("server", "Server URL",
			resolver.WithRequired(),
			resolver.WithPlaceholder("https://argocd.example.com"),
			resolver.WithHint("ArgoCD server URL"),
		).
		AddExpressionField("username", "Username",
			resolver.WithPlaceholder("admin"),
			resolver.WithHint("ArgoCD username (optional if using authToken)"),
		).
		AddExpressionField("password", "Password",
			resolver.WithSensitive(),
			resolver.WithHint("ArgoCD password (optional if using authToken)"),
		).
		AddExpressionField("authToken", "Auth Token",
			resolver.WithSensitive(),
			resolver.WithHint("JWT auth token (optional if using username/password)"),
		).
		AddToggleField("insecure", "Skip TLS Verify",
			resolver.WithDefault(false),
			resolver.WithHint("Skip TLS certificate verification"),
		).
		EndSection().
	AddSection("Project").
		AddExpressionField("name", "Project Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-project"),
			resolver.WithHint("Name of the project"),
		).
		EndSection().
	Build()

// ProjectCreateSchema is the UI schema for argocd-project-create
var ProjectCreateSchema = resolver.NewSchemaBuilder("argocd-project-create").
	WithName("Create Project").
	WithCategory("action").
	WithIcon(iconArgoCD).
	WithDescription("Create a new ArgoCD project").
	AddSection("Connection").
		AddExpressionField("server", "Server URL",
			resolver.WithRequired(),
			resolver.WithPlaceholder("https://argocd.example.com"),
			resolver.WithHint("ArgoCD server URL"),
		).
		AddExpressionField("username", "Username",
			resolver.WithPlaceholder("admin"),
			resolver.WithHint("ArgoCD username (optional if using authToken)"),
		).
		AddExpressionField("password", "Password",
			resolver.WithSensitive(),
			resolver.WithHint("ArgoCD password (optional if using authToken)"),
		).
		AddExpressionField("authToken", "Auth Token",
			resolver.WithSensitive(),
			resolver.WithHint("JWT auth token (optional if using username/password)"),
		).
		AddToggleField("insecure", "Skip TLS Verify",
			resolver.WithDefault(false),
			resolver.WithHint("Skip TLS certificate verification"),
		).
		EndSection().
	AddSection("Project").
		AddExpressionField("name", "Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-project"),
			resolver.WithHint("Project name"),
		).
		AddTextareaField("description", "Description",
			resolver.WithRows(3),
			resolver.WithHint("Project description"),
		).
		EndSection().
	AddSection("Destinations").
		AddJSONField("destinations", "Destinations",
			resolver.WithHeight(100),
			resolver.WithHint(`[{"server": "https://kubernetes.default.svc", "namespace": "*"}]`),
		).
		EndSection().
	AddSection("Source Repositories").
		AddTagsField("sourceRepos", "Source Repositories",
			resolver.WithHint("Allowed source repositories"),
		).
		EndSection().
	Build()

// RepoAddSchema is the UI schema for argocd-repo-add
var RepoAddSchema = resolver.NewSchemaBuilder("argocd-repo-add").
	WithName("Add Repository").
	WithCategory("action").
	WithIcon(iconArgoCD).
	WithDescription("Add a repository to ArgoCD").
	AddSection("Connection").
		AddExpressionField("server", "Server URL",
			resolver.WithRequired(),
			resolver.WithPlaceholder("https://argocd.example.com"),
			resolver.WithHint("ArgoCD server URL"),
		).
		AddExpressionField("username", "Username",
			resolver.WithPlaceholder("admin"),
			resolver.WithHint("ArgoCD username (optional if using authToken)"),
		).
		AddExpressionField("password", "Password",
			resolver.WithSensitive(),
			resolver.WithHint("ArgoCD password (optional if using authToken)"),
		).
		AddExpressionField("authToken", "Auth Token",
			resolver.WithSensitive(),
			resolver.WithHint("JWT auth token (optional if using username/password)"),
		).
		AddToggleField("insecure", "Skip TLS Verify",
			resolver.WithDefault(false),
			resolver.WithHint("Skip TLS certificate verification"),
		).
		EndSection().
	AddSection("Repository").
		AddExpressionField("repoUrl", "Repository URL",
			resolver.WithRequired(),
			resolver.WithPlaceholder("https://github.com/org/repo.git"),
			resolver.WithHint("Git repository URL"),
		).
		AddExpressionField("name", "Name",
			resolver.WithHint("Optional name for the repository"),
		).
		EndSection().
	AddSection("Authentication").
		AddExpressionField("gitUsername", "Git Username",
			resolver.WithHint("Git username (for HTTPS repos)"),
		).
		AddExpressionField("gitPassword", "Git Password",
			resolver.WithSensitive(),
			resolver.WithHint("Git password or token (for HTTPS repos)"),
		).
		AddTextareaField("sshPrivateKey", "SSH Private Key",
			resolver.WithRows(6),
			resolver.WithSensitive(),
			resolver.WithHint("SSH private key (for SSH repos)"),
		).
		EndSection().
	AddSection("Options").
		AddToggleField("repoInsecure", "Insecure",
			resolver.WithDefault(false),
			resolver.WithHint("Skip TLS verification"),
		).
		AddToggleField("enableLfs", "Enable LFS",
			resolver.WithDefault(false),
			resolver.WithHint("Enable Git LFS"),
		).
		EndSection().
	Build()

// RepoListSchema is the UI schema for argocd-repo-list
var RepoListSchema = resolver.NewSchemaBuilder("argocd-repo-list").
	WithName("List Repositories").
	WithCategory("action").
	WithIcon(iconArgoCD).
	WithDescription("List all repositories in ArgoCD").
	AddSection("Connection").
		AddExpressionField("server", "Server URL",
			resolver.WithRequired(),
			resolver.WithPlaceholder("https://argocd.example.com"),
			resolver.WithHint("ArgoCD server URL"),
		).
		AddExpressionField("username", "Username",
			resolver.WithPlaceholder("admin"),
			resolver.WithHint("ArgoCD username (optional if using authToken)"),
		).
		AddExpressionField("password", "Password",
			resolver.WithSensitive(),
			resolver.WithHint("ArgoCD password (optional if using authToken)"),
		).
		AddExpressionField("authToken", "Auth Token",
			resolver.WithSensitive(),
			resolver.WithHint("JWT auth token (optional if using username/password)"),
		).
		AddToggleField("insecure", "Skip TLS Verify",
			resolver.WithDefault(false),
			resolver.WithHint("Skip TLS certificate verification"),
		).
		EndSection().
	Build()

// ClusterListSchema is the UI schema for argocd-cluster-list
var ClusterListSchema = resolver.NewSchemaBuilder("argocd-cluster-list").
	WithName("List Clusters").
	WithCategory("action").
	WithIcon(iconArgoCD).
	WithDescription("List all clusters registered in ArgoCD").
	AddSection("Connection").
		AddExpressionField("server", "Server URL",
			resolver.WithRequired(),
			resolver.WithPlaceholder("https://argocd.example.com"),
			resolver.WithHint("ArgoCD server URL"),
		).
		AddExpressionField("username", "Username",
			resolver.WithPlaceholder("admin"),
			resolver.WithHint("ArgoCD username (optional if using authToken)"),
		).
		AddExpressionField("password", "Password",
			resolver.WithSensitive(),
			resolver.WithHint("ArgoCD password (optional if using authToken)"),
		).
		AddExpressionField("authToken", "Auth Token",
			resolver.WithSensitive(),
			resolver.WithHint("JWT auth token (optional if using username/password)"),
		).
		AddToggleField("insecure", "Skip TLS Verify",
			resolver.WithDefault(false),
			resolver.WithHint("Skip TLS certificate verification"),
		).
		EndSection().
	Build()

// ClusterCreateSchema is the UI schema for argocd-cluster-create
var ClusterCreateSchema = resolver.NewSchemaBuilder("argocd-cluster-create").
	WithName("Create Cluster").
	WithCategory("action").
	WithIcon(iconArgoCD).
	WithDescription("Register a new Kubernetes cluster with ArgoCD").
	AddSection("Connection").
		AddExpressionField("server", "Server URL",
			resolver.WithRequired(),
			resolver.WithPlaceholder("https://argocd.example.com"),
			resolver.WithHint("ArgoCD server URL"),
		).
		AddExpressionField("username", "Username",
			resolver.WithPlaceholder("admin"),
			resolver.WithHint("ArgoCD username (optional if using authToken)"),
		).
		AddExpressionField("password", "Password",
			resolver.WithSensitive(),
			resolver.WithHint("ArgoCD password (optional if using authToken)"),
		).
		AddExpressionField("authToken", "Auth Token",
			resolver.WithSensitive(),
			resolver.WithHint("JWT auth token (optional if using username/password)"),
		).
		AddToggleField("insecure", "Skip TLS Verify",
			resolver.WithDefault(false),
			resolver.WithHint("Skip TLS certificate verification"),
		).
		EndSection().
	AddSection("Cluster").
		AddExpressionField("name", "Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-cluster"),
			resolver.WithHint("Cluster name"),
		).
		AddExpressionField("clusterServer", "Server URL",
			resolver.WithRequired(),
			resolver.WithPlaceholder("https://kubernetes.default.svc"),
			resolver.WithHint("Kubernetes API server URL"),
		).
		EndSection().
	AddSection("Authentication").
		AddSelectField("authType", "Auth Type",
			[]resolver.SelectOption{
				{Label: "Service Account Token", Value: "serviceAccountToken"},
				{Label: "Client Certificate", Value: "clientCertificate"},
			},
			resolver.WithDefault("serviceAccountToken"),
			resolver.WithHint("Authentication method"),
		).
		AddTextareaField("bearerToken", "Bearer Token",
			resolver.WithRows(4),
			resolver.WithSensitive(),
			resolver.WithHint("Service account bearer token"),
		).
		AddTextareaField("tlsClientCertData", "Client Certificate",
			resolver.WithRows(6),
			resolver.WithSensitive(),
			resolver.WithHint("TLS client certificate (PEM)"),
		).
		AddTextareaField("tlsClientKeyData", "Client Key",
			resolver.WithRows(6),
			resolver.WithSensitive(),
			resolver.WithHint("TLS client key (PEM)"),
		).
		AddTextareaField("caData", "CA Certificate",
			resolver.WithRows(6),
			resolver.WithSensitive(),
			resolver.WithHint("CA certificate (PEM)"),
		).
		EndSection().
	AddSection("Options").
		AddToggleField("clusterInsecure", "Insecure",
			resolver.WithDefault(false),
			resolver.WithHint("Skip TLS verification"),
		).
		AddTagsField("namespaces", "Namespaces",
			resolver.WithHint("Namespaces to manage (empty = all)"),
		).
		EndSection().
	Build()

// VersionSchema is the UI schema for argocd-version
var VersionSchema = resolver.NewSchemaBuilder("argocd-version").
	WithName("Get Version").
	WithCategory("action").
	WithIcon(iconArgoCD).
	WithDescription("Get ArgoCD server version information").
	AddSection("Connection").
		AddExpressionField("server", "Server URL",
			resolver.WithRequired(),
			resolver.WithPlaceholder("https://argocd.example.com"),
			resolver.WithHint("ArgoCD server URL"),
		).
		AddExpressionField("username", "Username",
			resolver.WithPlaceholder("admin"),
			resolver.WithHint("ArgoCD username (optional if using authToken)"),
		).
		AddExpressionField("password", "Password",
			resolver.WithSensitive(),
			resolver.WithHint("ArgoCD password (optional if using authToken)"),
		).
		AddExpressionField("authToken", "Auth Token",
			resolver.WithSensitive(),
			resolver.WithHint("JWT auth token (optional if using username/password)"),
		).
		AddToggleField("insecure", "Skip TLS Verify",
			resolver.WithDefault(false),
			resolver.WithHint("Skip TLS certificate verification"),
		).
		EndSection().
	Build()

// ============================================================================
// APPLICATION EXECUTORS
// ============================================================================

// AppListExecutor handles argocd-app-list
type AppListExecutor struct{}

func (e *AppListExecutor) Type() string { return "argocd-app-list" }

func (e *AppListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, res executor.TemplateResolver) (*executor.StepResult, error) {
	config := res.ResolveMap(step.Config)

	r := res.(*resolver.Resolver)
	serverURL, username, password, authToken, insecure := parseArgoCDConfig(config, r)

	client, err := getArgoCDClient(serverURL, username, password, authToken, insecure)
	if err != nil {
		return nil, err
	}

	// Build query parameters
	path := "applications"
	params := []string{}

	if project := r.ResolveString(getString(config, "project")); project != "" {
		params = append(params, "project="+url.QueryEscape(project))
	}
	if namePattern := r.ResolveString(getString(config, "namePattern")); namePattern != "" {
		params = append(params, "name="+url.QueryEscape(namePattern))
	}

	if len(params) > 0 {
		path += "?" + strings.Join(params, "&")
	}

	respBody, err := client.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Extract applications list
	applications := []map[string]interface{}{}
	if items, ok := result["items"].([]interface{}); ok {
		for _, item := range items {
			if app, ok := item.(map[string]interface{}); ok {
				appInfo := map[string]interface{}{
					"name":      "",
					"project":   "",
					"sync":      "",
					"health":    "",
					"repoUrl":   "",
					"namespace": "",
				}

				if metadata, ok := app["metadata"].(map[string]interface{}); ok {
					if name, ok := metadata["name"].(string); ok {
						appInfo["name"] = name
					}
					if ns, ok := metadata["namespace"].(string); ok {
						appInfo["namespace"] = ns
					}
				}

				if spec, ok := app["spec"].(map[string]interface{}); ok {
					if project, ok := spec["project"].(string); ok {
						appInfo["project"] = project
					}
					if source, ok := spec["source"].(map[string]interface{}); ok {
						if repoURL, ok := source["repoURL"].(string); ok {
							appInfo["repoUrl"] = repoURL
						}
					}
				}

				if status, ok := app["status"].(map[string]interface{}); ok {
					if sync, ok := status["sync"].(map[string]interface{}); ok {
						if syncStatus, ok := sync["status"].(string); ok {
							appInfo["sync"] = syncStatus
						}
					}
					if health, ok := status["health"].(map[string]interface{}); ok {
						if healthStatus, ok := health["status"].(string); ok {
							appInfo["health"] = healthStatus
						}
					}
				}

				applications = append(applications, appInfo)
			}
		}
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"applications": applications,
			"count":        len(applications),
			"raw":          result,
		},
	}, nil
}

// AppGetExecutor handles argocd-app-get
type AppGetExecutor struct{}

func (e *AppGetExecutor) Type() string { return "argocd-app-get" }

func (e *AppGetExecutor) Execute(ctx context.Context, step *executor.StepDefinition, res executor.TemplateResolver) (*executor.StepResult, error) {
	config := res.ResolveMap(step.Config)

	r := res.(*resolver.Resolver)
	serverURL, username, password, authToken, insecure := parseArgoCDConfig(config, r)

	client, err := getArgoCDClient(serverURL, username, password, authToken, insecure)
	if err != nil {
		return nil, err
	}

	appName := r.ResolveString(getString(config, "appName"))
	if appName == "" {
		return nil, fmt.Errorf("appName is required")
	}

	path := fmt.Sprintf("applications/%s", appName)
	if getBool(config, "refresh", false) {
		path += "?refresh=normal"
	}

	respBody, err := client.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Extract key fields
	output := map[string]interface{}{
		"raw": result,
	}

	if metadata, ok := result["metadata"].(map[string]interface{}); ok {
		output["name"] = metadata["name"]
		output["namespace"] = metadata["namespace"]
		output["uid"] = metadata["uid"]
		if annotations, ok := metadata["annotations"].(map[string]interface{}); ok {
			output["annotations"] = annotations
		}
		if labels, ok := metadata["labels"].(map[string]interface{}); ok {
			output["labels"] = labels
		}
	}

	if spec, ok := result["spec"].(map[string]interface{}); ok {
		output["project"] = spec["project"]
		output["source"] = spec["source"]
		output["destination"] = spec["destination"]
		output["syncPolicy"] = spec["syncPolicy"]
	}

	if status, ok := result["status"].(map[string]interface{}); ok {
		output["status"] = status
		if sync, ok := status["sync"].(map[string]interface{}); ok {
			output["syncStatus"] = sync["status"]
		}
		if health, ok := status["health"].(map[string]interface{}); ok {
			output["healthStatus"] = health["status"]
		}
	}

	return &executor.StepResult{Output: output}, nil
}

// AppCreateExecutor handles argocd-app-create
type AppCreateExecutor struct{}

func (e *AppCreateExecutor) Type() string { return "argocd-app-create" }

func (e *AppCreateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, res executor.TemplateResolver) (*executor.StepResult, error) {
	config := res.ResolveMap(step.Config)

	r := res.(*resolver.Resolver)
	serverURL, username, password, authToken, insecure := parseArgoCDConfig(config, r)

	client, err := getArgoCDClient(serverURL, username, password, authToken, insecure)
	if err != nil {
		return nil, err
	}

	name := r.ResolveString(getString(config, "name"))
	if name == "" {
		return nil, fmt.Errorf("name is required")
	}

	project := r.ResolveString(getString(config, "project"))
	if project == "" {
		project = "default"
	}

	repoURL := r.ResolveString(getString(config, "repoUrl"))
	if repoURL == "" {
		return nil, fmt.Errorf("repoUrl is required")
	}

	path := getString(config, "path")
	if path == "" {
		path = "."
	}

	revision := r.ResolveString(getString(config, "revision"))
	if revision == "" {
		revision = r.ResolveString(getString(config, "targetRevision"))
	}
	if revision == "" {
		revision = "HEAD"
	}

	destServer := r.ResolveString(getString(config, "destServer"))
	if destServer == "" {
		destServer = "https://kubernetes.default.svc"
	}

	destNamespace := r.ResolveString(getString(config, "destNamespace"))
	if destNamespace == "" {
		return nil, fmt.Errorf("destNamespace is required")
	}

	// Build application spec
	app := map[string]interface{}{
		"metadata": map[string]interface{}{
			"name":      name,
			"namespace": "argocd",
		},
		"spec": map[string]interface{}{
			"project": project,
			"source": map[string]interface{}{
				"repoURL":        repoURL,
				"path":           path,
				"targetRevision": revision,
			},
			"destination": map[string]interface{}{
				"server":    destServer,
				"namespace": destNamespace,
			},
		},
	}

	// Add sync policy
	syncPolicy := getString(config, "syncPolicy")
	if syncPolicy == "automated" {
		syncPolicySpec := map[string]interface{}{
			"automated": map[string]interface{}{},
		}
		if getBool(config, "prune", false) {
			syncPolicySpec["automated"].(map[string]interface{})["prune"] = true
		}
		if getBool(config, "selfHeal", false) {
			syncPolicySpec["automated"].(map[string]interface{})["selfHeal"] = true
		}
		if getBool(config, "allowEmpty", false) {
			syncPolicySpec["automated"].(map[string]interface{})["allowEmpty"] = true
		}
		app["spec"].(map[string]interface{})["syncPolicy"] = syncPolicySpec
	} else if getBool(config, "prune", false) {
		// Manual sync with prune option
		app["spec"].(map[string]interface{})["syncPolicy"] = map[string]interface{}{
			"syncOptions": []string{"Prune=true"},
		}
	}

	// Add ignore differences if provided
	if ignoreDifferences := getMap(config, "ignoreDifferences"); ignoreDifferences != nil {
		app["spec"].(map[string]interface{})["ignoreDifferences"] = ignoreDifferences
	}

	// Add finalizers if provided
	if finalizers := getStringSlice(config, "finalizers"); len(finalizers) > 0 {
		app["metadata"].(map[string]interface{})["finalizers"] = finalizers
	}

	respBody, err := client.doRequest(ctx, "POST", "applications", app)
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"name":    name,
			"success": true,
			"raw":     result,
		},
	}, nil
}

// AppUpdateExecutor handles argocd-app-update
type AppUpdateExecutor struct{}

func (e *AppUpdateExecutor) Type() string { return "argocd-app-update" }

func (e *AppUpdateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, res executor.TemplateResolver) (*executor.StepResult, error) {
	config := res.ResolveMap(step.Config)

	r := res.(*resolver.Resolver)
	serverURL, username, password, authToken, insecure := parseArgoCDConfig(config, r)

	client, err := getArgoCDClient(serverURL, username, password, authToken, insecure)
	if err != nil {
		return nil, err
	}

	name := r.ResolveString(getString(config, "name"))
	if name == "" {
		return nil, fmt.Errorf("name is required")
	}

	// Get current application
	respBody, err := client.doRequest(ctx, "GET", fmt.Sprintf("applications/%s", name), nil)
	if err != nil {
		return nil, err
	}

	var app map[string]interface{}
	if err := json.Unmarshal(respBody, &app); err != nil {
		return nil, fmt.Errorf("failed to parse current application: %w", err)
	}

	// Update fields if provided
	spec, ok := app["spec"].(map[string]interface{})
	if !ok {
		spec = make(map[string]interface{})
		app["spec"] = spec
	}

	// Update source
	if repoURL := r.ResolveString(getString(config, "repoUrl")); repoURL != "" {
		source, ok := spec["source"].(map[string]interface{})
		if !ok {
			source = make(map[string]interface{})
			spec["source"] = source
		}
		source["repoURL"] = repoURL
	}

	if path := r.ResolveString(getString(config, "path")); path != "" {
		source, ok := spec["source"].(map[string]interface{})
		if !ok {
			source = make(map[string]interface{})
			spec["source"] = source
		}
		source["path"] = path
	}

	if revision := r.ResolveString(getString(config, "revision")); revision != "" {
		source, ok := spec["source"].(map[string]interface{})
		if !ok {
			source = make(map[string]interface{})
			spec["source"] = source
		}
		source["targetRevision"] = revision
	}

	// Update destination namespace
	if destNamespace := r.ResolveString(getString(config, "destNamespace")); destNamespace != "" {
		destination, ok := spec["destination"].(map[string]interface{})
		if !ok {
			destination = make(map[string]interface{})
			spec["destination"] = destination
		}
		destination["namespace"] = destNamespace
	}

	// Update sync policy
	syncPolicy := getString(config, "syncPolicy")
	if syncPolicy == "automated" {
		currentSyncPolicy, ok := spec["syncPolicy"].(map[string]interface{})
		if !ok {
			currentSyncPolicy = make(map[string]interface{})
			spec["syncPolicy"] = currentSyncPolicy
		}
		automated, ok := currentSyncPolicy["automated"].(map[string]interface{})
		if !ok {
			automated = make(map[string]interface{})
			currentSyncPolicy["automated"] = automated
		}
		if prune, ok := config["prune"]; ok {
			automated["prune"] = prune
		}
		if selfHeal, ok := config["selfHeal"]; ok {
			automated["selfHeal"] = selfHeal
		}
	} else if syncPolicy == "manual" {
		// Remove automated sync
		if currentSyncPolicy, ok := spec["syncPolicy"].(map[string]interface{}); ok {
			delete(currentSyncPolicy, "automated")
		}
	}

	// Handle prune flag for manual sync
	if prune, ok := config["prune"]; ok && prune == true {
		currentSyncPolicy, ok := spec["syncPolicy"].(map[string]interface{})
		if !ok {
			currentSyncPolicy = make(map[string]interface{})
			spec["syncPolicy"] = currentSyncPolicy
		}
		currentSyncPolicy["syncOptions"] = []string{"Prune=true"}
	}

	respBody, err = client.doRequest(ctx, "PUT", fmt.Sprintf("applications/%s", name), app)
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"name":    name,
			"success": true,
			"raw":     result,
		},
	}, nil
}

// AppDeleteExecutor handles argocd-app-delete
type AppDeleteExecutor struct{}

func (e *AppDeleteExecutor) Type() string { return "argocd-app-delete" }

func (e *AppDeleteExecutor) Execute(ctx context.Context, step *executor.StepDefinition, res executor.TemplateResolver) (*executor.StepResult, error) {
	config := res.ResolveMap(step.Config)

	r := res.(*resolver.Resolver)
	serverURL, username, password, authToken, insecure := parseArgoCDConfig(config, r)

	client, err := getArgoCDClient(serverURL, username, password, authToken, insecure)
	if err != nil {
		return nil, err
	}

	appName := r.ResolveString(getString(config, "appName"))
	if appName == "" {
		return nil, fmt.Errorf("appName is required")
	}

	cascade := "true"
	if !getBool(config, "cascade", true) {
		cascade = "false"
	}

	path := fmt.Sprintf("applications/%s?cascade=%s", appName, cascade)

	_, err = client.doRequest(ctx, "DELETE", path, nil)
	if err != nil {
		return nil, err
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"appName": appName,
			"success": true,
		},
	}, nil
}

// AppSyncExecutor handles argocd-app-sync
type AppSyncExecutor struct{}

func (e *AppSyncExecutor) Type() string { return "argocd-app-sync" }

func (e *AppSyncExecutor) Execute(ctx context.Context, step *executor.StepDefinition, res executor.TemplateResolver) (*executor.StepResult, error) {
	config := res.ResolveMap(step.Config)

	r := res.(*resolver.Resolver)
	serverURL, username, password, authToken, insecure := parseArgoCDConfig(config, r)

	client, err := getArgoCDClient(serverURL, username, password, authToken, insecure)
	if err != nil {
		return nil, err
	}

	appName := r.ResolveString(getString(config, "appName"))
	if appName == "" {
		return nil, fmt.Errorf("appName is required")
	}

	// Build sync request body
	syncReq := map[string]interface{}{
		"sync": map[string]interface{}{},
	}

	syncOptions := []string{}

	if revision := r.ResolveString(getString(config, "revision")); revision != "" {
		syncReq["sync"].(map[string]interface{})["revision"] = revision
	}

	if getBool(config, "prune", false) {
		syncOptions = append(syncOptions, "Prune=true")
	}

	if getBool(config, "force", false) {
		syncOptions = append(syncOptions, "Force=true")
	}

	if getBool(config, "dryRun", false) {
		syncReq["sync"].(map[string]interface{})["dryRun"] = true
	}

	if resources := getStringSlice(config, "resources"); len(resources) > 0 {
		resourceList := []map[string]interface{}{}
		for _, res := range resources {
			parts := strings.Split(res, ":")
			if len(parts) == 2 {
				nsName := strings.Split(parts[1], "/")
				resourceSpec := map[string]interface{}{
					"group": "",
					"kind":  parts[0],
				}
				if len(nsName) == 2 {
					resourceSpec["namespace"] = nsName[0]
					resourceSpec["name"] = nsName[1]
				} else {
					resourceSpec["name"] = nsName[0]
				}
				resourceList = append(resourceList, resourceSpec)
			}
		}
		if len(resourceList) > 0 {
			syncReq["sync"].(map[string]interface{})["resources"] = resourceList
		}
	}

	if len(syncOptions) > 0 {
		syncReq["sync"].(map[string]interface{})["syncOptions"] = syncOptions
	}

	respBody, err := client.doRequest(ctx, "POST", fmt.Sprintf("applications/%s/sync", appName), syncReq)
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"appName": appName,
			"success": true,
			"raw":     result,
		},
	}, nil
}

// AppRollbackExecutor handles argocd-app-rollback
type AppRollbackExecutor struct{}

func (e *AppRollbackExecutor) Type() string { return "argocd-app-rollback" }

func (e *AppRollbackExecutor) Execute(ctx context.Context, step *executor.StepDefinition, res executor.TemplateResolver) (*executor.StepResult, error) {
	config := res.ResolveMap(step.Config)

	r := res.(*resolver.Resolver)
	serverURL, username, password, authToken, insecure := parseArgoCDConfig(config, r)

	client, err := getArgoCDClient(serverURL, username, password, authToken, insecure)
	if err != nil {
		return nil, err
	}

	appName := r.ResolveString(getString(config, "appName"))
	if appName == "" {
		return nil, fmt.Errorf("appName is required")
	}

	historyId := getInt(config, "historyId", -1)
	if historyId < 0 {
		return nil, fmt.Errorf("historyId is required and must be >= 0")
	}

	rollbackReq := map[string]interface{}{
		"id": historyId,
	}

	if getBool(config, "prune", false) {
		rollbackReq["prune"] = true
	}

	if getBool(config, "dryRun", false) {
		rollbackReq["dryRun"] = true
	}

	respBody, err := client.doRequest(ctx, "POST", fmt.Sprintf("applications/%s/rollback", appName), rollbackReq)
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"appName":   appName,
			"historyId": historyId,
			"success":   true,
			"raw":       result,
		},
	}, nil
}

// AppRefreshExecutor handles argocd-app-refresh
type AppRefreshExecutor struct{}

func (e *AppRefreshExecutor) Type() string { return "argocd-app-refresh" }

func (e *AppRefreshExecutor) Execute(ctx context.Context, step *executor.StepDefinition, res executor.TemplateResolver) (*executor.StepResult, error) {
	config := res.ResolveMap(step.Config)

	r := res.(*resolver.Resolver)
	serverURL, username, password, authToken, insecure := parseArgoCDConfig(config, r)

	client, err := getArgoCDClient(serverURL, username, password, authToken, insecure)
	if err != nil {
		return nil, err
	}

	appName := r.ResolveString(getString(config, "appName"))
	if appName == "" {
		return nil, fmt.Errorf("appName is required")
	}

	refreshType := "normal"
	if getBool(config, "hard", false) {
		refreshType = "hard"
	}

	path := fmt.Sprintf("applications/%s?refresh=%s", appName, refreshType)

	respBody, err := client.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"appName": appName,
			"success": true,
			"raw":     result,
		},
	}, nil
}

// AppRestartExecutor handles argocd-app-restart
type AppRestartExecutor struct{}

func (e *AppRestartExecutor) Type() string { return "argocd-app-restart" }

func (e *AppRestartExecutor) Execute(ctx context.Context, step *executor.StepDefinition, res executor.TemplateResolver) (*executor.StepResult, error) {
	config := res.ResolveMap(step.Config)

	r := res.(*resolver.Resolver)
	serverURL, username, password, authToken, insecure := parseArgoCDConfig(config, r)

	client, err := getArgoCDClient(serverURL, username, password, authToken, insecure)
	if err != nil {
		return nil, err
	}

	appName := r.ResolveString(getString(config, "appName"))
	if appName == "" {
		return nil, fmt.Errorf("appName is required")
	}

	// First do a hard refresh
	path := fmt.Sprintf("applications/%s?refresh=hard", appName)
	_, err = client.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to refresh application: %w", err)
	}

	// Then trigger a sync
	syncReq := map[string]interface{}{
		"sync": map[string]interface{}{},
	}

	respBody, err := client.doRequest(ctx, "POST", fmt.Sprintf("applications/%s/sync", appName), syncReq)
	if err != nil {
		return nil, fmt.Errorf("failed to sync application: %w", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"appName": appName,
			"success": true,
			"raw":     result,
		},
	}, nil
}

// AppStatusExecutor handles argocd-app-status
type AppStatusExecutor struct{}

func (e *AppStatusExecutor) Type() string { return "argocd-app-status" }

func (e *AppStatusExecutor) Execute(ctx context.Context, step *executor.StepDefinition, res executor.TemplateResolver) (*executor.StepResult, error) {
	config := res.ResolveMap(step.Config)

	r := res.(*resolver.Resolver)
	serverURL, username, password, authToken, insecure := parseArgoCDConfig(config, r)

	client, err := getArgoCDClient(serverURL, username, password, authToken, insecure)
	if err != nil {
		return nil, err
	}

	appName := r.ResolveString(getString(config, "appName"))
	if appName == "" {
		return nil, fmt.Errorf("appName is required")
	}

	path := fmt.Sprintf("applications/%s", appName)
	if getBool(config, "refresh", false) {
		path += "?refresh=normal"
	}

	respBody, err := client.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	output := map[string]interface{}{
		"raw": result,
	}

	if status, ok := result["status"].(map[string]interface{}); ok {
		if sync, ok := status["sync"].(map[string]interface{}); ok {
			output["syncStatus"] = sync["status"]
			if revision, ok := sync["revision"].(string); ok {
				output["revision"] = revision
			}
		}
		if health, ok := status["health"].(map[string]interface{}); ok {
			output["healthStatus"] = health["status"]
		}
		if operation, ok := status["operationState"].(map[string]interface{}); ok {
			output["operationState"] = operation
			if phase, ok := operation["phase"].(string); ok {
				output["operationPhase"] = phase
			}
		}
	}

	if spec, ok := result["spec"].(map[string]interface{}); ok {
		output["project"] = spec["project"]
	}

	return &executor.StepResult{Output: output}, nil
}

// AppHistoryExecutor handles argocd-app-history
type AppHistoryExecutor struct{}

func (e *AppHistoryExecutor) Type() string { return "argocd-app-history" }

func (e *AppHistoryExecutor) Execute(ctx context.Context, step *executor.StepDefinition, res executor.TemplateResolver) (*executor.StepResult, error) {
	config := res.ResolveMap(step.Config)

	r := res.(*resolver.Resolver)
	serverURL, username, password, authToken, insecure := parseArgoCDConfig(config, r)

	client, err := getArgoCDClient(serverURL, username, password, authToken, insecure)
	if err != nil {
		return nil, err
	}

	appName := r.ResolveString(getString(config, "appName"))
	if appName == "" {
		return nil, fmt.Errorf("appName is required")
	}

	respBody, err := client.doRequest(ctx, "GET", fmt.Sprintf("applications/%s/history", appName), nil)
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Extract and limit history
	history := []map[string]interface{}{}
	if items, ok := result["history"].([]interface{}); ok {
		limit := getInt(config, "limit", 10)
		count := 0
		for _, item := range items {
			if count >= limit {
				break
			}
			if entry, ok := item.(map[string]interface{}); ok {
				history = append(history, entry)
				count++
			}
		}
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"appName": appName,
			"history": history,
			"count":   len(history),
			"raw":     result,
		},
	}, nil
}

// AppDiffExecutor handles argocd-app-diff
type AppDiffExecutor struct{}

func (e *AppDiffExecutor) Type() string { return "argocd-app-diff" }

func (e *AppDiffExecutor) Execute(ctx context.Context, step *executor.StepDefinition, res executor.TemplateResolver) (*executor.StepResult, error) {
	config := res.ResolveMap(step.Config)

	r := res.(*resolver.Resolver)
	serverURL, username, password, authToken, insecure := parseArgoCDConfig(config, r)

	client, err := getArgoCDClient(serverURL, username, password, authToken, insecure)
	if err != nil {
		return nil, err
	}

	appName := r.ResolveString(getString(config, "appName"))
	if appName == "" {
		return nil, fmt.Errorf("appName is required")
	}

	// Optionally refresh first
	if getBool(config, "refresh", false) {
		_, err := client.doRequest(ctx, "GET", fmt.Sprintf("applications/%s?refresh=normal", appName), nil)
		if err != nil {
			return nil, fmt.Errorf("failed to refresh application: %w", err)
		}
	}

	// Get application with manifest comparison
	respBody, err := client.doRequest(ctx, "GET", fmt.Sprintf("applications/%s?refresh=normal", appName), nil)
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Extract diff information
	diff := []map[string]interface{}{}
	if status, ok := result["status"].(map[string]interface{}); ok {
		if resources, ok := status["resources"].([]interface{}); ok {
			for _, res := range resources {
				if resource, ok := res.(map[string]interface{}); ok {
					if health, ok := resource["health"].(map[string]interface{}); ok {
						if status, ok := health["status"].(string); ok && (status == "Missing" || status == "Extra") {
							diff = append(diff, map[string]interface{}{
								"kind":      resource["kind"],
								"name":      resource["name"],
								"namespace": resource["namespace"],
								"status":    status,
							})
						}
					}
				}
			}
		}
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"appName": appName,
			"diff":    diff,
			"hasDiff": len(diff) > 0,
			"raw":     result,
		},
	}, nil
}

// AppResourcesExecutor handles argocd-app-resources
type AppResourcesExecutor struct{}

func (e *AppResourcesExecutor) Type() string { return "argocd-app-resources" }

func (e *AppResourcesExecutor) Execute(ctx context.Context, step *executor.StepDefinition, res executor.TemplateResolver) (*executor.StepResult, error) {
	config := res.ResolveMap(step.Config)

	r := res.(*resolver.Resolver)
	serverURL, username, password, authToken, insecure := parseArgoCDConfig(config, r)

	client, err := getArgoCDClient(serverURL, username, password, authToken, insecure)
	if err != nil {
		return nil, err
	}

	appName := r.ResolveString(getString(config, "appName"))
	if appName == "" {
		return nil, fmt.Errorf("appName is required")
	}

	respBody, err := client.doRequest(ctx, "GET", fmt.Sprintf("applications/%s/resource-tree", appName), nil)
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Extract resources
	resources := []map[string]interface{}{}
	if nodes, ok := result["nodes"].([]interface{}); ok {
		for _, node := range nodes {
			if n, ok := node.(map[string]interface{}); ok {
				resources = append(resources, map[string]interface{}{
					"kind":      n["kind"],
					"name":      n["name"],
					"namespace": n["namespace"],
					"version":   n["version"],
					"group":     n["group"],
					"health":    n["health"],
				})
			}
		}
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"appName":   appName,
			"resources": resources,
			"count":     len(resources),
			"raw":       result,
		},
	}, nil
}

// AppLogsExecutor handles argocd-app-logs
type AppLogsExecutor struct{}

func (e *AppLogsExecutor) Type() string { return "argocd-app-logs" }

func (e *AppLogsExecutor) Execute(ctx context.Context, step *executor.StepDefinition, res executor.TemplateResolver) (*executor.StepResult, error) {
	config := res.ResolveMap(step.Config)

	r := res.(*resolver.Resolver)
	serverURL, username, password, authToken, insecure := parseArgoCDConfig(config, r)

	client, err := getArgoCDClient(serverURL, username, password, authToken, insecure)
	if err != nil {
		return nil, err
	}

	appName := r.ResolveString(getString(config, "appName"))
	if appName == "" {
		return nil, fmt.Errorf("appName is required")
	}

	// Build query parameters
	params := []string{}
	if container := r.ResolveString(getString(config, "container")); container != "" {
		params = append(params, "container="+url.QueryEscape(container))
	}

	tailLines := getInt(config, "tailLines", 100)
	params = append(params, "tailLines="+strconv.Itoa(tailLines))

	if getBool(config, "follow", false) {
		params = append(params, "follow=true")
	}

	query := ""
	if len(params) > 0 {
		query = "?" + strings.Join(params, "&")
	}

	respBody, err := client.doRequest(ctx, "GET", fmt.Sprintf("applications/%s/logs%s", appName, query), nil)
	if err != nil {
		return nil, err
	}

	// Logs may be returned as plain text or JSON
	var logs interface{}
	if err := json.Unmarshal(respBody, &logs); err != nil {
		// Return as string if not JSON
		logs = string(respBody)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"appName": appName,
			"logs":    logs,
		},
	}, nil
}

// ============================================================================
// PROJECT EXECUTORS
// ============================================================================

// ProjectListExecutor handles argocd-project-list
type ProjectListExecutor struct{}

func (e *ProjectListExecutor) Type() string { return "argocd-project-list" }

func (e *ProjectListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, res executor.TemplateResolver) (*executor.StepResult, error) {
	config := res.ResolveMap(step.Config)

	r := res.(*resolver.Resolver)
	serverURL, username, password, authToken, insecure := parseArgoCDConfig(config, r)

	client, err := getArgoCDClient(serverURL, username, password, authToken, insecure)
	if err != nil {
		return nil, err
	}

	respBody, err := client.doRequest(ctx, "GET", "projects", nil)
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Extract projects
	projects := []map[string]interface{}{}
	if items, ok := result["items"].([]interface{}); ok {
		for _, item := range items {
			if proj, ok := item.(map[string]interface{}); ok {
				projectInfo := map[string]interface{}{}
				if metadata, ok := proj["metadata"].(map[string]interface{}); ok {
					projectInfo["name"] = metadata["name"]
					projectInfo["namespace"] = metadata["namespace"]
				}
				if spec, ok := proj["spec"].(map[string]interface{}); ok {
					projectInfo["description"] = spec["description"]
					if dests, ok := spec["destinations"].([]interface{}); ok {
						projectInfo["destinations"] = dests
					}
					if sources, ok := spec["sourceRepos"].([]interface{}); ok {
						projectInfo["sourceRepos"] = sources
					}
				}
				projects = append(projects, projectInfo)
			}
		}
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"projects": projects,
			"count":    len(projects),
			"raw":      result,
		},
	}, nil
}

// ProjectGetExecutor handles argocd-project-get
type ProjectGetExecutor struct{}

func (e *ProjectGetExecutor) Type() string { return "argocd-project-get" }

func (e *ProjectGetExecutor) Execute(ctx context.Context, step *executor.StepDefinition, res executor.TemplateResolver) (*executor.StepResult, error) {
	config := res.ResolveMap(step.Config)

	r := res.(*resolver.Resolver)
	serverURL, username, password, authToken, insecure := parseArgoCDConfig(config, r)

	client, err := getArgoCDClient(serverURL, username, password, authToken, insecure)
	if err != nil {
		return nil, err
	}

	name := r.ResolveString(getString(config, "name"))
	if name == "" {
		return nil, fmt.Errorf("name is required")
	}

	respBody, err := client.doRequest(ctx, "GET", fmt.Sprintf("projects/%s", name), nil)
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"name": name,
			"raw":  result,
		},
	}, nil
}

// ProjectCreateExecutor handles argocd-project-create
type ProjectCreateExecutor struct{}

func (e *ProjectCreateExecutor) Type() string { return "argocd-project-create" }

func (e *ProjectCreateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, res executor.TemplateResolver) (*executor.StepResult, error) {
	config := res.ResolveMap(step.Config)

	r := res.(*resolver.Resolver)
	serverURL, username, password, authToken, insecure := parseArgoCDConfig(config, r)

	client, err := getArgoCDClient(serverURL, username, password, authToken, insecure)
	if err != nil {
		return nil, err
	}

	name := r.ResolveString(getString(config, "name"))
	if name == "" {
		return nil, fmt.Errorf("name is required")
	}

	project := map[string]interface{}{
		"metadata": map[string]interface{}{
			"name":      name,
			"namespace": "argocd",
		},
		"spec": map[string]interface{}{},
	}

	if description := r.ResolveString(getString(config, "description")); description != "" {
		project["spec"].(map[string]interface{})["description"] = description
	}

	// Add destinations if provided
	if destinationsRaw := getMap(config, "destinations"); destinationsRaw != nil {
		project["spec"].(map[string]interface{})["destinations"] = destinationsRaw
	} else {
		// Default to local cluster
		project["spec"].(map[string]interface{})["destinations"] = []map[string]interface{}{
			{"server": "https://kubernetes.default.svc", "namespace": "*"},
		}
	}

	// Add source repos if provided
	if sourceRepos := getStringSlice(config, "sourceRepos"); len(sourceRepos) > 0 {
		project["spec"].(map[string]interface{})["sourceRepos"] = sourceRepos
	} else {
		// Default to allow all
		project["spec"].(map[string]interface{})["sourceRepos"] = []string{"*"}
	}

	respBody, err := client.doRequest(ctx, "POST", "projects", project)
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"name":    name,
			"success": true,
			"raw":     result,
		},
	}, nil
}

// ============================================================================
// REPOSITORY EXECUTORS
// ============================================================================

// RepoAddExecutor handles argocd-repo-add
type RepoAddExecutor struct{}

func (e *RepoAddExecutor) Type() string { return "argocd-repo-add" }

func (e *RepoAddExecutor) Execute(ctx context.Context, step *executor.StepDefinition, res executor.TemplateResolver) (*executor.StepResult, error) {
	config := res.ResolveMap(step.Config)

	r := res.(*resolver.Resolver)
	serverURL, username, password, authToken, insecure := parseArgoCDConfig(config, r)

	client, err := getArgoCDClient(serverURL, username, password, authToken, insecure)
	if err != nil {
		return nil, err
	}

	repoURL := r.ResolveString(getString(config, "repoUrl"))
	if repoURL == "" {
		return nil, fmt.Errorf("repoUrl is required")
	}

	repo := map[string]interface{}{
		"repo": repoURL,
	}

	if name := r.ResolveString(getString(config, "name")); name != "" {
		repo["name"] = name
	}

	if gitUsername := r.ResolveString(getString(config, "gitUsername")); gitUsername != "" {
		repo["username"] = gitUsername
	}

	if gitPassword := r.ResolveString(getString(config, "gitPassword")); gitPassword != "" {
		repo["password"] = gitPassword
	}

	if sshKey := r.ResolveString(getString(config, "sshPrivateKey")); sshKey != "" {
		repo["sshPrivateKey"] = sshKey
	}

	if getBool(config, "repoInsecure", false) {
		repo["insecure"] = true
	}

	if getBool(config, "enableLfs", false) {
		repo["enableLfs"] = true
	}

	respBody, err := client.doRequest(ctx, "POST", "repositories", repo)
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"repoUrl": repoURL,
			"success": true,
			"raw":     result,
		},
	}, nil
}

// RepoListExecutor handles argocd-repo-list
type RepoListExecutor struct{}

func (e *RepoListExecutor) Type() string { return "argocd-repo-list" }

func (e *RepoListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, res executor.TemplateResolver) (*executor.StepResult, error) {
	config := res.ResolveMap(step.Config)

	r := res.(*resolver.Resolver)
	serverURL, username, password, authToken, insecure := parseArgoCDConfig(config, r)

	client, err := getArgoCDClient(serverURL, username, password, authToken, insecure)
	if err != nil {
		return nil, err
	}

	respBody, err := client.doRequest(ctx, "GET", "repositories", nil)
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Extract repositories
	repos := []map[string]interface{}{}
	if items, ok := result["items"].([]interface{}); ok {
		for _, item := range items {
			if repo, ok := item.(map[string]interface{}); ok {
				repoInfo := map[string]interface{}{
					"repo":  repo["repo"],
					"name":  repo["name"],
					"type":  repo["type"],
					"state": repo["connectionState"],
				}
				repos = append(repos, repoInfo)
			}
		}
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"repos": repos,
			"count": len(repos),
			"raw":   result,
		},
	}, nil
}

// ============================================================================
// CLUSTER EXECUTORS
// ============================================================================

// ClusterListExecutor handles argocd-cluster-list
type ClusterListExecutor struct{}

func (e *ClusterListExecutor) Type() string { return "argocd-cluster-list" }

func (e *ClusterListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, res executor.TemplateResolver) (*executor.StepResult, error) {
	config := res.ResolveMap(step.Config)

	r := res.(*resolver.Resolver)
	serverURL, username, password, authToken, insecure := parseArgoCDConfig(config, r)

	client, err := getArgoCDClient(serverURL, username, password, authToken, insecure)
	if err != nil {
		return nil, err
	}

	respBody, err := client.doRequest(ctx, "GET", "clusters", nil)
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Extract clusters
	clusters := []map[string]interface{}{}
	if items, ok := result["items"].([]interface{}); ok {
		for _, item := range items {
			if cluster, ok := item.(map[string]interface{}); ok {
				clusterInfo := map[string]interface{}{}
				if metadata, ok := cluster["metadata"].(map[string]interface{}); ok {
					clusterInfo["name"] = metadata["name"]
				}
				if server, ok := cluster["server"].(string); ok {
					clusterInfo["server"] = server
				}
				if info, ok := cluster["info"].(map[string]interface{}); ok {
					clusterInfo["info"] = info
				}
				if connectionState, ok := cluster["connectionState"].(map[string]interface{}); ok {
					clusterInfo["connectionState"] = connectionState
				}
				clusters = append(clusters, clusterInfo)
			}
		}
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"clusters": clusters,
			"count":    len(clusters),
			"raw":      result,
		},
	}, nil
}

// ClusterCreateExecutor handles argocd-cluster-create
type ClusterCreateExecutor struct{}

func (e *ClusterCreateExecutor) Type() string { return "argocd-cluster-create" }

func (e *ClusterCreateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, res executor.TemplateResolver) (*executor.StepResult, error) {
	config := res.ResolveMap(step.Config)

	r := res.(*resolver.Resolver)
	serverURL, username, password, authToken, insecure := parseArgoCDConfig(config, r)

	client, err := getArgoCDClient(serverURL, username, password, authToken, insecure)
	if err != nil {
		return nil, err
	}

	name := r.ResolveString(getString(config, "name"))
	if name == "" {
		return nil, fmt.Errorf("name is required")
	}

	server := r.ResolveString(getString(config, "clusterServer"))
	if server == "" {
		return nil, fmt.Errorf("server is required")
	}

	cluster := map[string]interface{}{
		"name":   name,
		"server": server,
		"config": map[string]interface{}{},
	}

	authType := getString(config, "authType")
	if authType == "" {
		authType = "serviceAccountToken"
	}

	configMap := cluster["config"].(map[string]interface{})

	if authType == "serviceAccountToken" {
		if bearerToken := r.ResolveString(getString(config, "bearerToken")); bearerToken != "" {
			configMap["bearerToken"] = bearerToken
		}
	} else if authType == "clientCertificate" {
		if tlsCert := r.ResolveString(getString(config, "tlsClientCertData")); tlsCert != "" {
			configMap["tlsClientCertData"] = tlsCert
		}
		if tlsKey := r.ResolveString(getString(config, "tlsClientKeyData")); tlsKey != "" {
			configMap["tlsClientKeyData"] = tlsKey
		}
	}

	if caData := r.ResolveString(getString(config, "caData")); caData != "" {
		configMap["caData"] = caData
	}

	if getBool(config, "clusterInsecure", false) {
		configMap["insecure"] = true
	}

	if namespaces := getStringSlice(config, "namespaces"); len(namespaces) > 0 {
		configMap["namespaces"] = namespaces
	}

	respBody, err := client.doRequest(ctx, "POST", "clusters", cluster)
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"name":    name,
			"server":  server,
			"success": true,
			"raw":     result,
		},
	}, nil
}

// ============================================================================
// SYSTEM EXECUTORS
// ============================================================================

// VersionExecutor handles argocd-version
type VersionExecutor struct{}

func (e *VersionExecutor) Type() string { return "argocd-version" }

func (e *VersionExecutor) Execute(ctx context.Context, step *executor.StepDefinition, res executor.TemplateResolver) (*executor.StepResult, error) {
	config := res.ResolveMap(step.Config)

	r := res.(*resolver.Resolver)
	serverURL, username, password, authToken, insecure := parseArgoCDConfig(config, r)

	client, err := getArgoCDClient(serverURL, username, password, authToken, insecure)
	if err != nil {
		return nil, err
	}

	respBody, err := client.doRequest(ctx, "GET", "version", nil)
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	output := map[string]interface{}{
		"raw": result,
	}

	if version, ok := result["Version"].(string); ok {
		output["version"] = version
	}
	if buildDate, ok := result["BuildDate"].(string); ok {
		output["buildDate"] = buildDate
	}
	if gitCommit, ok := result["GitCommit"].(string); ok {
		output["gitCommit"] = gitCommit
	}
	if gitTag, ok := result["GitTag"].(string); ok {
		output["gitTag"] = gitTag
	}

	return &executor.StepResult{Output: output}, nil
}
