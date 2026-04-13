package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
	iconJenkins = "settings"
)

// JenkinsClient represents a Jenkins API client
type JenkinsClient struct {
	baseURL    string
	username   string
	apiToken   string
	httpClient *http.Client
}

// Client cache
var (
	clients   = make(map[string]*JenkinsClient)
	clientMux sync.RWMutex
)

// JenkinsJob represents a Jenkins job
type JenkinsJob struct {
	Name        string       `json:"name"`
	URL         string       `json:"url"`
	Color       string       `json:"color"`
	Buildable   bool         `json:"buildable"`
	InQueue     bool         `json:"inQueue"`
	KeepDependencies bool  `json:"keepDependencies"`
	LastBuild   *JenkinsBuild `json:"lastBuild,omitempty"`
	Jobs        []JenkinsJob `json:"jobs,omitempty"` // For folders
}

// JenkinsBuild represents a Jenkins build
type JenkinsBuild struct {
	Number      int      `json:"number"`
	URL         string   `json:"url"`
	Result      string   `json:"result"`
	Building    bool     `json:"building"`
	Duration    int64    `json:"duration"`
	Timestamp   int64    `json:"timestamp"`
	DisplayName string   `json:"displayName"`
	Description string   `json:"description"`
	Actions     []Action `json:"actions,omitempty"`
}

// Action represents a build action
type Action struct {
	Class       string            `json:"class,omitempty"`
	Parameters  []BuildParameter  `json:"parameters,omitempty"`
	Causes      []BuildCause      `json:"causes,omitempty"`
}

// BuildParameter represents a build parameter
type BuildParameter struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// BuildCause represents why a build was triggered
type BuildCause struct {
	Class           string `json:"class"`
	ShortDescription string `json:"shortDescription"`
	UserID          string `json:"userId,omitempty"`
	UpstreamProject string `json:"upstreamProject,omitempty"`
	UpstreamBuild   int    `json:"upstreamBuild,omitempty"`
}

// JenkinsNode represents a Jenkins agent/node
type JenkinsNode struct {
	DisplayName       string `json:"displayName"`
	NumExecutors      int    `json:"numExecutors"`
	Description       string `json:"description"`
	Offline           bool   `json:"offline"`
	Idle              bool   `json:"idle"`
	TemporarilyOffline bool  `json:"temporarilyOffline"`
	Icon              string `json:"icon"`
}

// JenkinsQueueItem represents an item in the build queue
type JenkinsQueueItem struct {
	ID              int      `json:"id"`
	Task            TaskInfo `json:"task"`
	Buildable       bool     `json:"buildable"`
	BuildableStart  int64    `json:"buildableStartMilliseconds"`
	Why             string   `json:"why"`
	Stuck           bool     `json:"stuck"`
	InQueueSince    int64    `json:"inQueueSince"`
	Params          string   `json:"params"`
	IsBlocked       bool     `json:"isBlocked"`
	IsBuildable     bool     `json:"isBuildable"`
}

// TaskInfo represents task information in queue
type TaskInfo struct {
	Name  string `json:"name"`
	URL   string `json:"url"`
	Color string `json:"color"`
}

// JenkinsView represents a Jenkins view
type JenkinsView struct {
	Name        string `json:"name"`
	URL         string `json:"url"`
	Description string `json:"description"`
}

// JenkinsCredential represents a credential entry
type JenkinsCredential struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
	Description string `json:"description"`
	TypeName    string `json:"typeName"`
}

// JenkinsPlugin represents a plugin
type JenkinsPlugin struct {
	ShortName     string `json:"shortName"`
	LongName      string `json:"longName"`
	Version       string `json:"version"`
	Active        bool   `json:"active"`
	Enabled       bool   `json:"enabled"`
	Pinned        bool   `json:"pinned"`
	Deleted       bool   `json:"deleted"`
	Downgradable  bool   `json:"downgradable"`
	HasUpdate     bool   `json:"hasUpdate"`
}

// QueueResponse represents the queue API response
type QueueResponse struct {
	Items []JenkinsQueueItem `json:"items"`
}

// ComputerResponse represents the computer/nodes API response
type ComputerResponse struct {
	Computer []JenkinsNode `json:"computer"`
}

// PluginResponse represents the plugin manager API response
type PluginResponse struct {
	Plugins []JenkinsPlugin `json:"plugins"`
}

// CredentialResponse represents credentials API response
type CredentialResponse struct {
	Credentials []JenkinsCredential `json:"credentials"`
}

// ViewResponse represents views API response
type ViewResponse struct {
	Views []JenkinsView `json:"views"`
}

// BuildListResponse represents a list of builds
type BuildListResponse struct {
	Builds []JenkinsBuild `json:"builds"`
}

// JobResponse represents job API response
type JobResponse struct {
	Jobs []JenkinsJob `json:"jobs"`
}

// TriggerBuildResponse represents the response from triggering a build
type TriggerBuildResponse struct {
	Success     bool   `json:"success"`
	BuildNumber int    `json:"buildNumber,omitempty"`
	QueueID     int    `json:"queueId,omitempty"`
	Message     string `json:"message"`
}

// BuildLogsResponse represents build logs response
type BuildLogsResponse struct {
	Logs        string `json:"logs"`
	BuildNumber int    `json:"buildNumber"`
	JobName     string `json:"jobName"`
}

// JobStatusResponse represents job/build status
type JobStatusResponse struct {
	Number      int    `json:"number"`
	Result      string `json:"result"`
	Building    bool   `json:"building"`
	Duration    int64  `json:"duration"`
	Timestamp   int64  `json:"timestamp"`
	DisplayName string `json:"displayName"`
	Description string `json:"description"`
	URL         string `json:"url"`
}

// JobListResponse represents job list output
type JobListResponse struct {
	Jobs  []JenkinsJobSummary `json:"jobs"`
	Count int                 `json:"count"`
}

// JenkinsJobSummary is a simplified job summary
type JenkinsJobSummary struct {
	Name      string            `json:"name"`
	URL       string            `json:"url"`
	Color     string            `json:"color"`
	Buildable bool              `json:"buildable"`
	InQueue   bool              `json:"inQueue"`
	LastBuild *BuildSummary     `json:"lastBuild,omitempty"`
	Jobs      []JenkinsJobSummary `json:"jobs,omitempty"`
}

// BuildSummary is a simplified build summary
type BuildSummary struct {
	Number int    `json:"number"`
	Result string `json:"result"`
	URL    string `json:"url"`
}

// QueueListResponse represents queue list output
type QueueListResponse struct {
	Items []QueueItemSummary `json:"items"`
	Count int                `json:"count"`
}

// QueueItemSummary is a simplified queue item
type QueueItemSummary struct {
	ID             int    `json:"id"`
	TaskName       string `json:"taskName"`
	Why            string `json:"why"`
	InQueueSince   string `json:"inQueueSince"`
	IsBlocked      bool   `json:"isBlocked"`
	IsBuildable    bool   `json:"isBuildable"`
	BuildableStart int64  `json:"buildableStart"`
}

// NodeListResponse represents node list output
type NodeListResponse struct {
	Nodes []NodeSummary `json:"nodes"`
	Count int           `json:"count"`
}

// NodeSummary is a simplified node summary
type NodeSummary struct {
	Name             string `json:"name"`
	DisplayName      string `json:"displayName"`
	NumExecutors     int    `json:"numExecutors"`
	Description      string `json:"description"`
	Offline          bool   `json:"offline"`
	Idle             bool   `json:"idle"`
	TemporarilyOffline bool  `json:"temporarilyOffline"`
	Icon             string `json:"icon"`
}

// ViewListResponse represents view list output
type ViewListResponse struct {
	Views []ViewSummary `json:"views"`
	Count int           `json:"count"`
}

// ViewSummary is a simplified view summary
type ViewSummary struct {
	Name        string `json:"name"`
	URL         string `json:"url"`
	Description string `json:"description"`
}

// CredentialListResponse represents credential list output
type CredentialListResponse struct {
	Credentials []CredentialSummary `json:"credentials"`
	Count       int                 `json:"count"`
}

// CredentialSummary is a simplified credential summary
type CredentialSummary struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
	Description string `json:"description"`
	TypeName    string `json:"typeName"`
}

// PluginListResponse represents plugin list output
type PluginListResponse struct {
	Plugins []PluginSummary `json:"plugins"`
	Count   int             `json:"count"`
	Active  int             `json:"active"`
}

// PluginSummary is a simplified plugin summary
type PluginSummary struct {
	ShortName    string `json:"shortName"`
	LongName     string `json:"longName"`
	Version      string `json:"version"`
	Active       bool   `json:"active"`
	Enabled      bool   `json:"enabled"`
	HasUpdate    bool   `json:"hasUpdate"`
}

// BuildListItem represents a build in the list
type BuildListItem struct {
	Number      int    `json:"number"`
	Result      string `json:"result"`
	Building    bool   `json:"building"`
	Duration    int64  `json:"duration"`
	Timestamp   int64  `json:"timestamp"`
	DisplayName string `json:"displayName"`
	URL         string `json:"url"`
}

// BuildListOutput represents build list output
type BuildListOutput struct {
	Builds []BuildListItem `json:"builds"`
	Count  int             `json:"count"`
	JobName string         `json:"jobName"`
}

// AbortBuildResponse represents abort build response
type AbortBuildResponse struct {
	Success     bool   `json:"success"`
	BuildNumber int    `json:"buildNumber"`
	JobName     string `json:"jobName"`
	Message     string `json:"message"`
}

func main() {
	port := os.Getenv("SKILL_PORT")
	if port == "" {
		port = "50085"
	}

	server := grpc.NewSkillServer("skill-jenkins", "1.0.0")

	// Register all executors with schemas
	server.RegisterExecutorWithSchema("jenkins-job-list", &JenkinsJobListExecutor{}, JenkinsJobListSchema)
	server.RegisterExecutorWithSchema("jenkins-job-trigger", &JenkinsJobTriggerExecutor{}, JenkinsJobTriggerSchema)
	server.RegisterExecutorWithSchema("jenkins-job-status", &JenkinsJobStatusExecutor{}, JenkinsJobStatusSchema)
	server.RegisterExecutorWithSchema("jenkins-build-list", &JenkinsBuildListExecutor{}, JenkinsBuildListSchema)
	server.RegisterExecutorWithSchema("jenkins-build-logs", &JenkinsBuildLogsExecutor{}, JenkinsBuildLogsSchema)
	server.RegisterExecutorWithSchema("jenkins-build-abort", &JenkinsBuildAbortExecutor{}, JenkinsBuildAbortSchema)
	server.RegisterExecutorWithSchema("jenkins-node-list", &JenkinsNodeListExecutor{}, JenkinsNodeListSchema)
	server.RegisterExecutorWithSchema("jenkins-queue-list", &JenkinsQueueListExecutor{}, JenkinsQueueListSchema)
	server.RegisterExecutorWithSchema("jenkins-view-list", &JenkinsViewListExecutor{}, JenkinsViewListSchema)
	server.RegisterExecutorWithSchema("jenkins-credential-list", &JenkinsCredentialListExecutor{}, JenkinsCredentialListSchema)
	server.RegisterExecutorWithSchema("jenkins-plugin-list", &JenkinsPluginListExecutor{}, JenkinsPluginListSchema)

	fmt.Printf("Starting skill-jenkins gRPC server on port %s\n", port)
	if err := server.Serve(port); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to serve: %v\n", err)
		os.Exit(1)
	}
}

// ============================================================================
// JENKINS CLIENT
// ============================================================================

// getJenkinsClient returns a cached Jenkins client or creates a new one
func getJenkinsClient(jenkinsURL, username, apiToken string) (*JenkinsClient, error) {
	if jenkinsURL == "" {
		return nil, fmt.Errorf("Jenkins URL is required")
	}
	if username == "" {
		return nil, fmt.Errorf("Jenkins username is required")
	}
	if apiToken == "" {
		return nil, fmt.Errorf("Jenkins API token is required")
	}

	// Normalize URL
	jenkinsURL = strings.TrimSuffix(jenkinsURL, "/")

	cacheKey := fmt.Sprintf("%s:%s", jenkinsURL, username)

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

	// Validate URL
	_, err := url.Parse(jenkinsURL)
	if err != nil {
		return nil, fmt.Errorf("invalid Jenkins URL: %w", err)
	}

	client = &JenkinsClient{
		baseURL:  jenkinsURL,
		username: username,
		apiToken: apiToken,
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
	clients[cacheKey] = client
	return client, nil
}

// doRequest performs an authenticated HTTP request to Jenkins API
func (c *JenkinsClient) doRequest(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
	reqURL := c.baseURL + path

	req, err := http.NewRequestWithContext(ctx, method, reqURL, body)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set basic auth
	req.SetBasicAuth(c.username, c.apiToken)

	// Set headers
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	return resp, nil
}

// getJSON performs a GET request and decodes JSON response
func (c *JenkinsClient) getJSON(ctx context.Context, path string, result interface{}) error {
	resp, err := c.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("resource not found: %s", path)
	}

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
	}

	if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
	}

	return nil
}

// post performs a POST request
func (c *JenkinsClient) post(ctx context.Context, path string, body io.Reader) (*http.Response, error) {
	return c.doRequest(ctx, http.MethodPost, path, body)
}

// getJobPath returns the job path, handling folders
func getJobPath(folder, jobName string) string {
	if folder == "" {
		return fmt.Sprintf("/job/%s", url.PathEscape(jobName))
	}
	// Clean folder path
	folder = strings.Trim(folder, "/")
	return fmt.Sprintf("/job/%s/job/%s", url.PathEscape(folder), url.PathEscape(jobName))
}

// getJobPathWithBase returns full job path including base
func getJobPathWithBase(folder, jobName string) string {
	if folder == "" {
		return fmt.Sprintf("/job/%s", url.PathEscape(jobName))
	}
	folder = strings.Trim(folder, "/")
	folderParts := strings.Split(folder, "/")
	pathParts := make([]string, 0, len(folderParts)+2)
	pathParts = append(pathParts, "job")
	for _, part := range folderParts {
		pathParts = append(pathParts, url.PathEscape(part))
	}
	pathParts = append(pathParts, "job", url.PathEscape(jobName))
	return "/" + strings.Join(pathParts, "/")
}

// ============================================================================
// HELPER FUNCTIONS
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

// JenkinsJobListSchema is the UI schema for jenkins-job-list
var JenkinsJobListSchema = resolver.NewSchemaBuilder("jenkins-job-list").
	WithName("List Jobs").
	WithCategory("action").
	WithIcon(iconJenkins).
	WithDescription("List all jobs on the Jenkins server").
	AddSection("Connection").
		AddExpressionField("url", "Jenkins URL",
			resolver.WithRequired(),
			resolver.WithPlaceholder("https://jenkins.example.com"),
			resolver.WithHint("Jenkins server URL (supports {{bindings.xxx}})"),
		).
		AddExpressionField("username", "Username",
			resolver.WithRequired(),
			resolver.WithPlaceholder("atlas-bot"),
			resolver.WithHint("Jenkins username or API token user"),
		).
		AddExpressionField("password", "API Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("11xx..."),
			resolver.WithHint("Jenkins API token (supports {{secrets.xxx}})"),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Options").
		AddExpressionField("folder", "Folder",
			resolver.WithPlaceholder("my-folder or folder/subfolder"),
			resolver.WithHint("Optional: Folder path to list jobs from"),
		).
		AddToggleField("includeFolders", "Include Folders",
			resolver.WithDefault(true),
			resolver.WithHint("Include folder entries in the results"),
		).
		EndSection().
	Build()

// JenkinsJobTriggerSchema is the UI schema for jenkins-job-trigger
var JenkinsJobTriggerSchema = resolver.NewSchemaBuilder("jenkins-job-trigger").
	WithName("Trigger Job").
	WithCategory("action").
	WithIcon(iconJenkins).
	WithDescription("Trigger a Jenkins job/build").
	AddSection("Connection").
		AddExpressionField("url", "Jenkins URL",
			resolver.WithRequired(),
			resolver.WithPlaceholder("https://jenkins.example.com"),
		).
		AddExpressionField("username", "Username",
			resolver.WithRequired(),
			resolver.WithPlaceholder("atlas-bot"),
		).
		AddExpressionField("password", "API Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("11xx..."),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Job").
		AddExpressionField("jobName", "Job Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-build-job"),
			resolver.WithHint("Name of the job to trigger"),
		).
		AddExpressionField("folder", "Folder",
			resolver.WithPlaceholder("my-folder"),
			resolver.WithHint("Optional: Folder containing the job"),
		).
		EndSection().
	AddSection("Parameters").
		AddKeyValueField("parameters", "Build Parameters",
			resolver.WithHint("Optional build parameters as key-value pairs"),
			resolver.WithKeyValuePlaceholders("PARAM_NAME", "value"),
		).
		EndSection().
	AddSection("Options").
		AddToggleField("waitForQueue", "Wait for Queue",
			resolver.WithDefault(false),
			resolver.WithHint("Wait for the build to leave the queue and get a build number"),
		).
		AddNumberField("waitTimeout", "Wait Timeout (seconds)",
			resolver.WithDefault(60),
			resolver.WithMinMax(10, 300),
			resolver.WithHint("Maximum time to wait for build number"),
		).
		EndSection().
	Build()

// JenkinsJobStatusSchema is the UI schema for jenkins-job-status
var JenkinsJobStatusSchema = resolver.NewSchemaBuilder("jenkins-job-status").
	WithName("Get Job Status").
	WithCategory("action").
	WithIcon(iconJenkins).
	WithDescription("Get the status of a Jenkins job or build").
	AddSection("Connection").
		AddExpressionField("url", "Jenkins URL",
			resolver.WithRequired(),
			resolver.WithPlaceholder("https://jenkins.example.com"),
		).
		AddExpressionField("username", "Username",
			resolver.WithRequired(),
			resolver.WithPlaceholder("atlas-bot"),
		).
		AddExpressionField("password", "API Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("11xx..."),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Job").
		AddExpressionField("jobName", "Job Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-build-job"),
		).
		AddExpressionField("folder", "Folder",
			resolver.WithPlaceholder("my-folder"),
			resolver.WithHint("Optional: Folder containing the job"),
		).
		AddNumberField("buildNumber", "Build Number",
			resolver.WithHint("Optional: Specific build number (default: last build)"),
		).
		EndSection().
	Build()

// JenkinsBuildListSchema is the UI schema for jenkins-build-list
var JenkinsBuildListSchema = resolver.NewSchemaBuilder("jenkins-build-list").
	WithName("List Builds").
	WithCategory("action").
	WithIcon(iconJenkins).
	WithDescription("List builds for a Jenkins job").
	AddSection("Connection").
		AddExpressionField("url", "Jenkins URL",
			resolver.WithRequired(),
			resolver.WithPlaceholder("https://jenkins.example.com"),
		).
		AddExpressionField("username", "Username",
			resolver.WithRequired(),
			resolver.WithPlaceholder("atlas-bot"),
		).
		AddExpressionField("password", "API Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("11xx..."),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Job").
		AddExpressionField("jobName", "Job Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-build-job"),
		).
		AddExpressionField("folder", "Folder",
			resolver.WithPlaceholder("my-folder"),
			resolver.WithHint("Optional: Folder containing the job"),
		).
		EndSection().
	AddSection("Options").
		AddNumberField("limit", "Limit",
			resolver.WithDefault(20),
			resolver.WithMinMax(1, 100),
			resolver.WithHint("Maximum number of builds to return"),
		).
		EndSection().
	Build()

// JenkinsBuildLogsSchema is the UI schema for jenkins-build-logs
var JenkinsBuildLogsSchema = resolver.NewSchemaBuilder("jenkins-build-logs").
	WithName("Get Build Logs").
	WithCategory("action").
	WithIcon(iconJenkins).
	WithDescription("Get console output/logs for a Jenkins build").
	AddSection("Connection").
		AddExpressionField("url", "Jenkins URL",
			resolver.WithRequired(),
			resolver.WithPlaceholder("https://jenkins.example.com"),
		).
		AddExpressionField("username", "Username",
			resolver.WithRequired(),
			resolver.WithPlaceholder("atlas-bot"),
		).
		AddExpressionField("password", "API Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("11xx..."),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Job").
		AddExpressionField("jobName", "Job Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-build-job"),
		).
		AddExpressionField("folder", "Folder",
			resolver.WithPlaceholder("my-folder"),
			resolver.WithHint("Optional: Folder containing the job"),
		).
		AddNumberField("buildNumber", "Build Number",
			resolver.WithHint("Optional: Build number (default: last build)"),
		).
		EndSection().
	AddSection("Options").
		AddNumberField("startLine", "Start Line",
			resolver.WithDefault(0),
			resolver.WithHint("Start line for logs (0 = from beginning)"),
		).
		AddNumberField("limit", "Max Lines",
			resolver.WithDefault(1000),
			resolver.WithHint("Maximum number of lines to return"),
		).
		EndSection().
	Build()

// JenkinsBuildAbortSchema is the UI schema for jenkins-build-abort
var JenkinsBuildAbortSchema = resolver.NewSchemaBuilder("jenkins-build-abort").
	WithName("Abort Build").
	WithCategory("action").
	WithIcon(iconJenkins).
	WithDescription("Abort a running Jenkins build").
	AddSection("Connection").
		AddExpressionField("url", "Jenkins URL",
			resolver.WithRequired(),
			resolver.WithPlaceholder("https://jenkins.example.com"),
		).
		AddExpressionField("username", "Username",
			resolver.WithRequired(),
			resolver.WithPlaceholder("atlas-bot"),
		).
		AddExpressionField("password", "API Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("11xx..."),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Job").
		AddExpressionField("jobName", "Job Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-build-job"),
		).
		AddExpressionField("folder", "Folder",
			resolver.WithPlaceholder("my-folder"),
			resolver.WithHint("Optional: Folder containing the job"),
		).
		AddNumberField("buildNumber", "Build Number",
			resolver.WithRequired(),
			resolver.WithHint("Build number to abort"),
		).
		EndSection().
	AddSection("Options").
		AddExpressionField("reason", "Abort Reason",
			resolver.WithPlaceholder("Aborted by user"),
			resolver.WithHint("Optional reason for aborting"),
		).
		EndSection().
	Build()

// JenkinsNodeListSchema is the UI schema for jenkins-node-list
var JenkinsNodeListSchema = resolver.NewSchemaBuilder("jenkins-node-list").
	WithName("List Nodes").
	WithCategory("action").
	WithIcon(iconJenkins).
	WithDescription("List all Jenkins nodes/agents").
	AddSection("Connection").
		AddExpressionField("url", "Jenkins URL",
			resolver.WithRequired(),
			resolver.WithPlaceholder("https://jenkins.example.com"),
		).
		AddExpressionField("username", "Username",
			resolver.WithRequired(),
			resolver.WithPlaceholder("atlas-bot"),
		).
		AddExpressionField("password", "API Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("11xx..."),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Options").
		AddToggleField("includeOffline", "Include Offline Nodes",
			resolver.WithDefault(true),
			resolver.WithHint("Include offline nodes in the results"),
		).
		EndSection().
	Build()

// JenkinsQueueListSchema is the UI schema for jenkins-queue-list
var JenkinsQueueListSchema = resolver.NewSchemaBuilder("jenkins-queue-list").
	WithName("List Queue").
	WithCategory("action").
	WithIcon(iconJenkins).
	WithDescription("List items in the Jenkins build queue").
	AddSection("Connection").
		AddExpressionField("url", "Jenkins URL",
			resolver.WithRequired(),
			resolver.WithPlaceholder("https://jenkins.example.com"),
		).
		AddExpressionField("username", "Username",
			resolver.WithRequired(),
			resolver.WithPlaceholder("atlas-bot"),
		).
		AddExpressionField("password", "API Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("11xx..."),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Options").
		AddToggleField("includeBlocked", "Include Blocked",
			resolver.WithDefault(true),
			resolver.WithHint("Include blocked queue items"),
		).
		EndSection().
	Build()

// JenkinsViewListSchema is the UI schema for jenkins-view-list
var JenkinsViewListSchema = resolver.NewSchemaBuilder("jenkins-view-list").
	WithName("List Views").
	WithCategory("action").
	WithIcon(iconJenkins).
	WithDescription("List all Jenkins views").
	AddSection("Connection").
		AddExpressionField("url", "Jenkins URL",
			resolver.WithRequired(),
			resolver.WithPlaceholder("https://jenkins.example.com"),
		).
		AddExpressionField("username", "Username",
			resolver.WithRequired(),
			resolver.WithPlaceholder("atlas-bot"),
		).
		AddExpressionField("password", "API Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("11xx..."),
			resolver.WithSensitive(),
		).
		EndSection().
	Build()

// JenkinsCredentialListSchema is the UI schema for jenkins-credential-list
var JenkinsCredentialListSchema = resolver.NewSchemaBuilder("jenkins-credential-list").
	WithName("List Credentials").
	WithCategory("action").
	WithIcon(iconJenkins).
	WithDescription("List Jenkins credentials (requires admin access)").
	AddSection("Connection").
		AddExpressionField("url", "Jenkins URL",
			resolver.WithRequired(),
			resolver.WithPlaceholder("https://jenkins.example.com"),
		).
		AddExpressionField("username", "Username",
			resolver.WithRequired(),
			resolver.WithPlaceholder("atlas-bot"),
		).
		AddExpressionField("password", "API Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("11xx..."),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Options").
		AddExpressionField("domain", "Domain",
			resolver.WithPlaceholder("_"),
			resolver.WithHint("Credential domain (default: _)"),
		).
		AddExpressionField("folder", "Folder",
			resolver.WithPlaceholder("my-folder"),
			resolver.WithHint("Optional: Folder for folder-level credentials"),
		).
		EndSection().
	Build()

// JenkinsPluginListSchema is the UI schema for jenkins-plugin-list
var JenkinsPluginListSchema = resolver.NewSchemaBuilder("jenkins-plugin-list").
	WithName("List Plugins").
	WithCategory("action").
	WithIcon(iconJenkins).
	WithDescription("List installed Jenkins plugins").
	AddSection("Connection").
		AddExpressionField("url", "Jenkins URL",
			resolver.WithRequired(),
			resolver.WithPlaceholder("https://jenkins.example.com"),
		).
		AddExpressionField("username", "Username",
			resolver.WithRequired(),
			resolver.WithPlaceholder("atlas-bot"),
		).
		AddExpressionField("password", "API Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("11xx..."),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Options").
		AddToggleField("activeOnly", "Active Only",
			resolver.WithDefault(false),
			resolver.WithHint("Only list active plugins"),
		).
		EndSection().
	Build()

// ============================================================================
// EXECUTORS
// ============================================================================

// JenkinsJobListExecutor handles jenkins-job-list
type JenkinsJobListExecutor struct{}

func (e *JenkinsJobListExecutor) Type() string { return "jenkins-job-list" }

func (e *JenkinsJobListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	jenkinsURL := getString(step.Config, "url")
	username := getString(step.Config, "username")
	apiToken := getString(step.Config, "password")
	folder := getString(step.Config, "folder")
	includeFolders := getBool(step.Config, "includeFolders", true)

	client, err := getJenkinsClient(jenkinsURL, username, apiToken)
	if err != nil {
		return nil, fmt.Errorf("failed to create Jenkins client: %w", err)
	}

	var path string
	if folder == "" {
		path = "/api/json?tree=jobs[name,url,color,buildable,inQueue,lastBuild[number,result,url]]"
	} else {
		jobPath := getJobPathWithBase(folder, "")
		// Remove trailing "job/" for folder listing
		jobPath = strings.TrimSuffix(jobPath, "/job/")
		path = jobPath + "/api/json?tree=jobs[name,url,color,buildable,inQueue,lastBuild[number,result,url]]"
	}

	var jobResp JobResponse
	if err := client.getJSON(ctx, path, &jobResp); err != nil {
		return nil, fmt.Errorf("failed to list jobs: %w", err)
	}

	jobs := make([]JenkinsJobSummary, 0)
	for _, job := range jobResp.Jobs {
		// Skip folders if not including them
		if !includeFolders && job.Color == "" && len(job.Jobs) > 0 {
			continue
		}

		summary := JenkinsJobSummary{
			Name:      job.Name,
			URL:       job.URL,
			Color:     job.Color,
			Buildable: job.Buildable,
			InQueue:   job.InQueue,
		}

		if job.LastBuild != nil {
			summary.LastBuild = &BuildSummary{
				Number: job.LastBuild.Number,
				Result: job.LastBuild.Result,
				URL:    job.LastBuild.URL,
			}
		}

		// Recursively process folder jobs
		if len(job.Jobs) > 0 {
			for _, subJob := range job.Jobs {
				subSummary := JenkinsJobSummary{
					Name:      subJob.Name,
					URL:       subJob.URL,
					Color:     subJob.Color,
					Buildable: subJob.Buildable,
					InQueue:   subJob.InQueue,
				}
				if subJob.LastBuild != nil {
					subSummary.LastBuild = &BuildSummary{
						Number: subJob.LastBuild.Number,
						Result: subJob.LastBuild.Result,
						URL:    subJob.LastBuild.URL,
					}
				}
				summary.Jobs = append(summary.Jobs, subSummary)
			}
		}

		jobs = append(jobs, summary)
	}

	output := JobListResponse{
		Jobs:  jobs,
		Count: len(jobs),
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"jobs":  output.Jobs,
			"count": output.Count,
		},
	}, nil
}

// JenkinsJobTriggerExecutor handles jenkins-job-trigger
type JenkinsJobTriggerExecutor struct{}

func (e *JenkinsJobTriggerExecutor) Type() string { return "jenkins-job-trigger" }

func (e *JenkinsJobTriggerExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	jenkinsURL := getString(step.Config, "url")
	username := getString(step.Config, "username")
	apiToken := getString(step.Config, "password")
	jobName := getString(step.Config, "jobName")
	folder := getString(step.Config, "folder")
	parameters := getMap(step.Config, "parameters")
	waitForQueue := getBool(step.Config, "waitForQueue", false)
	waitTimeout := getInt(step.Config, "waitTimeout", 60)

	if jobName == "" {
		return nil, fmt.Errorf("jobName is required")
	}

	client, err := getJenkinsClient(jenkinsURL, username, apiToken)
	if err != nil {
		return nil, fmt.Errorf("failed to create Jenkins client: %w", err)
	}

	jobPath := getJobPathWithBase(folder, jobName)

	// Build parameters
	var bodyData string
	if len(parameters) > 0 {
		params := url.Values{}
		for k, v := range parameters {
			params.Set(k, fmt.Sprintf("%v", v))
		}
		bodyData = params.Encode()
	}

	// Trigger the build
	var reqBody io.Reader
	if bodyData != "" {
		reqBody = strings.NewReader(bodyData)
	}

	resp, err := client.post(ctx, jobPath+"/build", reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to trigger build: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed to trigger build: HTTP %d - %s", resp.StatusCode, string(body))
	}

	// Get queue ID from Location header or X-Jenkins header
	queueID := 0
	location := resp.Header.Get("Location")
	if location != "" {
		// Try to extract queue ID from location
		if strings.Contains(location, "/queue/") {
			parts := strings.Split(location, "/")
			for i, part := range parts {
				if part == "queue" && i+1 < len(parts) {
					if id, err := strconv.Atoi(parts[i+1]); err == nil {
						queueID = id
					}
				}
			}
		}
	}

	result := TriggerBuildResponse{
		Success: true,
		QueueID: queueID,
		Message: "Build triggered successfully",
	}

	// Wait for build number if requested
	if waitForQueue && queueID > 0 {
		buildNum, err := e.waitForBuildNumber(ctx, client, queueID, waitTimeout)
		if err != nil {
			// Don't fail, just log warning
			result.Message = fmt.Sprintf("Build triggered (queue ID: %d), waiting for build number: %v", queueID, err)
		} else {
			result.BuildNumber = buildNum
			result.Message = fmt.Sprintf("Build triggered successfully (build #%d)", buildNum)
		}
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":     result.Success,
			"buildNumber": result.BuildNumber,
			"queueId":     result.QueueID,
			"message":     result.Message,
		},
	}, nil
}

func (e *JenkinsJobTriggerExecutor) waitForBuildNumber(ctx context.Context, client *JenkinsClient, queueID int, timeout int) (int, error) {
	deadline := time.Now().Add(time.Duration(timeout) * time.Second)

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		default:
		}

		var queueItem struct {
			Task      TaskInfo `json:"task"`
			Buildable bool     `json:"buildable"`
			Why       string   `json:"why"`
			Executable *struct {
				Number int `json:"number"`
			} `json:"executable"`
		}

		path := fmt.Sprintf("/queue/item/%d/api/json", queueID)
		if err := client.getJSON(ctx, path, &queueItem); err != nil {
			return 0, err
		}

		if queueItem.Executable != nil && queueItem.Executable.Number > 0 {
			return queueItem.Executable.Number, nil
		}

		if !queueItem.Buildable && queueItem.Why != "" {
			return 0, fmt.Errorf("build is blocked: %s", queueItem.Why)
		}

		time.Sleep(2 * time.Second)
	}

	return 0, fmt.Errorf("timeout waiting for build number")
}

// JenkinsJobStatusExecutor handles jenkins-job-status
type JenkinsJobStatusExecutor struct{}

func (e *JenkinsJobStatusExecutor) Type() string { return "jenkins-job-status" }

func (e *JenkinsJobStatusExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	jenkinsURL := getString(step.Config, "url")
	username := getString(step.Config, "username")
	apiToken := getString(step.Config, "password")
	jobName := getString(step.Config, "jobName")
	folder := getString(step.Config, "folder")
	buildNumber := getInt(step.Config, "buildNumber", 0)

	if jobName == "" {
		return nil, fmt.Errorf("jobName is required")
	}

	client, err := getJenkinsClient(jenkinsURL, username, apiToken)
	if err != nil {
		return nil, fmt.Errorf("failed to create Jenkins client: %w", err)
	}

	jobPath := getJobPathWithBase(folder, jobName)

	var path string
	if buildNumber > 0 {
		path = fmt.Sprintf("%s/%d/api/json", jobPath, buildNumber)
	} else {
		path = fmt.Sprintf("%s/lastBuild/api/json", jobPath)
	}

	var build JenkinsBuild
	if err := client.getJSON(ctx, path, &build); err != nil {
		return nil, fmt.Errorf("failed to get build status: %w", err)
	}

	output := JobStatusResponse{
		Number:      build.Number,
		Result:      build.Result,
		Building:    build.Building,
		Duration:    build.Duration,
		Timestamp:   build.Timestamp,
		DisplayName: build.DisplayName,
		Description: build.Description,
		URL:         build.URL,
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"number":      output.Number,
			"result":      output.Result,
			"building":    output.Building,
			"duration":    output.Duration,
			"timestamp":   output.Timestamp,
			"displayName": output.DisplayName,
			"description": output.Description,
			"url":         output.URL,
		},
	}, nil
}

// JenkinsBuildListExecutor handles jenkins-build-list
type JenkinsBuildListExecutor struct{}

func (e *JenkinsBuildListExecutor) Type() string { return "jenkins-build-list" }

func (e *JenkinsBuildListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	jenkinsURL := getString(step.Config, "url")
	username := getString(step.Config, "username")
	apiToken := getString(step.Config, "password")
	jobName := getString(step.Config, "jobName")
	folder := getString(step.Config, "folder")
	limit := getInt(step.Config, "limit", 20)

	if jobName == "" {
		return nil, fmt.Errorf("jobName is required")
	}

	client, err := getJenkinsClient(jenkinsURL, username, apiToken)
	if err != nil {
		return nil, fmt.Errorf("failed to create Jenkins client: %w", err)
	}

	jobPath := getJobPathWithBase(folder, jobName)
	path := fmt.Sprintf("%s/api/json?tree=builds[number,result,building,duration,timestamp,displayName,url]{0,%d}", jobPath, limit)

	var job struct {
		Builds []JenkinsBuild `json:"builds"`
	}
	if err := client.getJSON(ctx, path, &job); err != nil {
		return nil, fmt.Errorf("failed to list builds: %w", err)
	}

	builds := make([]BuildListItem, 0, len(job.Builds))
	for _, b := range job.Builds {
		builds = append(builds, BuildListItem{
			Number:      b.Number,
			Result:      b.Result,
			Building:    b.Building,
			Duration:    b.Duration,
			Timestamp:   b.Timestamp,
			DisplayName: b.DisplayName,
			URL:         b.URL,
		})
	}

	output := BuildListOutput{
		Builds:  builds,
		Count:   len(builds),
		JobName: jobName,
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"builds":  output.Builds,
			"count":   output.Count,
			"jobName": output.JobName,
		},
	}, nil
}

// JenkinsBuildLogsExecutor handles jenkins-build-logs
type JenkinsBuildLogsExecutor struct{}

func (e *JenkinsBuildLogsExecutor) Type() string { return "jenkins-build-logs" }

func (e *JenkinsBuildLogsExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	jenkinsURL := getString(step.Config, "url")
	username := getString(step.Config, "username")
	apiToken := getString(step.Config, "password")
	jobName := getString(step.Config, "jobName")
	folder := getString(step.Config, "folder")
	buildNumber := getInt(step.Config, "buildNumber", 0)
	startLine := getInt(step.Config, "startLine", 0)
	maxLines := getInt(step.Config, "limit", 1000)

	if jobName == "" {
		return nil, fmt.Errorf("jobName is required")
	}

	client, err := getJenkinsClient(jenkinsURL, username, apiToken)
	if err != nil {
		return nil, fmt.Errorf("failed to create Jenkins client: %w", err)
	}

	jobPath := getJobPathWithBase(folder, jobName)

	// Get last build number if not specified
	actualBuildNumber := buildNumber
	if actualBuildNumber == 0 {
		var job struct {
			LastBuild *JenkinsBuild `json:"lastBuild"`
		}
		path := fmt.Sprintf("%s/api/json?tree=lastBuild[number]", jobPath)
		if err := client.getJSON(ctx, path, &job); err != nil {
			return nil, fmt.Errorf("failed to get last build number: %w", err)
		}
		if job.LastBuild == nil {
			return nil, fmt.Errorf("no builds found for job %s", jobName)
		}
		actualBuildNumber = job.LastBuild.Number
	}

	// Get console text
	path := fmt.Sprintf("%s/%d/consoleText", jobPath, actualBuildNumber)
	if startLine > 0 {
		path = fmt.Sprintf("%s/%d/consoleText?start=%d", jobPath, actualBuildNumber, startLine)
	}

	resp, err := client.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get logs: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("build #%d not found for job %s", actualBuildNumber, jobName)
	}

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed to get logs: HTTP %d - %s", resp.StatusCode, string(body))
	}

	logsBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read logs: %w", err)
	}

	logs := string(logsBytes)

	// Limit lines if requested
	if maxLines > 0 {
		lines := strings.Split(logs, "\n")
		if len(lines) > maxLines {
			lines = lines[:maxLines]
			logs = strings.Join(lines, "\n")
		}
	}

	output := BuildLogsResponse{
		Logs:        logs,
		BuildNumber: actualBuildNumber,
		JobName:     jobName,
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"logs":        output.Logs,
			"buildNumber": output.BuildNumber,
			"jobName":     output.JobName,
		},
	}, nil
}

// JenkinsBuildAbortExecutor handles jenkins-build-abort
type JenkinsBuildAbortExecutor struct{}

func (e *JenkinsBuildAbortExecutor) Type() string { return "jenkins-build-abort" }

func (e *JenkinsBuildAbortExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	jenkinsURL := getString(step.Config, "url")
	username := getString(step.Config, "username")
	apiToken := getString(step.Config, "password")
	jobName := getString(step.Config, "jobName")
	folder := getString(step.Config, "folder")
	buildNumber := getInt(step.Config, "buildNumber", 0)
	reason := getString(step.Config, "reason")

	if jobName == "" {
		return nil, fmt.Errorf("jobName is required")
	}
	if buildNumber <= 0 {
		return nil, fmt.Errorf("buildNumber is required and must be positive")
	}

	client, err := getJenkinsClient(jenkinsURL, username, apiToken)
	if err != nil {
		return nil, fmt.Errorf("failed to create Jenkins client: %w", err)
	}

	jobPath := getJobPathWithBase(folder, jobName)
	path := fmt.Sprintf("%s/%d/stop", jobPath, buildNumber)

	// Add reason as parameter if provided
	if reason != "" {
		path = fmt.Sprintf("%s?reason=%s", path, url.QueryEscape(reason))
	}

	resp, err := client.post(ctx, path, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to abort build: %w", err)
	}
	defer resp.Body.Close()

	// Jenkins returns 200 or 302 on successful abort
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusFound {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed to abort build: HTTP %d - %s", resp.StatusCode, string(body))
	}

	output := AbortBuildResponse{
		Success:     true,
		BuildNumber: buildNumber,
		JobName:     jobName,
		Message:     fmt.Sprintf("Build #%d aborted successfully", buildNumber),
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":     output.Success,
			"buildNumber": output.BuildNumber,
			"jobName":     output.JobName,
			"message":     output.Message,
		},
	}, nil
}

// JenkinsNodeListExecutor handles jenkins-node-list
type JenkinsNodeListExecutor struct{}

func (e *JenkinsNodeListExecutor) Type() string { return "jenkins-node-list" }

func (e *JenkinsNodeListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	jenkinsURL := getString(step.Config, "url")
	username := getString(step.Config, "username")
	apiToken := getString(step.Config, "password")
	includeOffline := getBool(step.Config, "includeOffline", true)

	client, err := getJenkinsClient(jenkinsURL, username, apiToken)
	if err != nil {
		return nil, fmt.Errorf("failed to create Jenkins client: %w", err)
	}

	path := "/computer/api/json?tree=computer[displayName,numExecutors,description,offline,idle,temporarilyOffline,icon]"

	var computerResp ComputerResponse
	if err := client.getJSON(ctx, path, &computerResp); err != nil {
		return nil, fmt.Errorf("failed to list nodes: %w", err)
	}

	nodes := make([]NodeSummary, 0)
	for _, node := range computerResp.Computer {
		if !includeOffline && node.Offline {
			continue
		}

		nodes = append(nodes, NodeSummary{
			Name:             node.DisplayName,
			DisplayName:      node.DisplayName,
			NumExecutors:     node.NumExecutors,
			Description:      node.Description,
			Offline:          node.Offline,
			Idle:             node.Idle,
			TemporarilyOffline: node.TemporarilyOffline,
			Icon:             node.Icon,
		})
	}

	output := NodeListResponse{
		Nodes: nodes,
		Count: len(nodes),
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"nodes": output.Nodes,
			"count": output.Count,
		},
	}, nil
}

// JenkinsQueueListExecutor handles jenkins-queue-list
type JenkinsQueueListExecutor struct{}

func (e *JenkinsQueueListExecutor) Type() string { return "jenkins-queue-list" }

func (e *JenkinsQueueListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	jenkinsURL := getString(step.Config, "url")
	username := getString(step.Config, "username")
	apiToken := getString(step.Config, "password")
	includeBlocked := getBool(step.Config, "includeBlocked", true)

	client, err := getJenkinsClient(jenkinsURL, username, apiToken)
	if err != nil {
		return nil, fmt.Errorf("failed to create Jenkins client: %w", err)
	}

	path := "/queue/api/json?tree=items[id,task[name,url,color],buildable,buildableStartMilliseconds,why,stuck,inQueueSince,params,isBlocked,isBuildable]"

	var queueResp QueueResponse
	if err := client.getJSON(ctx, path, &queueResp); err != nil {
		return nil, fmt.Errorf("failed to list queue: %w", err)
	}

	items := make([]QueueItemSummary, 0)
	for _, item := range queueResp.Items {
		if !includeBlocked && item.IsBlocked {
			continue
		}

		items = append(items, QueueItemSummary{
			ID:             item.ID,
			TaskName:       item.Task.Name,
			Why:            item.Why,
			InQueueSince:   time.Unix(item.InQueueSince/1000, 0).Format(time.RFC3339),
			IsBlocked:      item.IsBlocked,
			IsBuildable:    item.IsBuildable,
			BuildableStart: item.BuildableStart,
		})
	}

	output := QueueListResponse{
		Items: items,
		Count: len(items),
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"items": output.Items,
			"count": output.Count,
		},
	}, nil
}

// JenkinsViewListExecutor handles jenkins-view-list
type JenkinsViewListExecutor struct{}

func (e *JenkinsViewListExecutor) Type() string { return "jenkins-view-list" }

func (e *JenkinsViewListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	jenkinsURL := getString(step.Config, "url")
	username := getString(step.Config, "username")
	apiToken := getString(step.Config, "password")

	client, err := getJenkinsClient(jenkinsURL, username, apiToken)
	if err != nil {
		return nil, fmt.Errorf("failed to create Jenkins client: %w", err)
	}

	path := "/api/json?tree=views[name,url,description]"

	var viewResp ViewResponse
	if err := client.getJSON(ctx, path, &viewResp); err != nil {
		return nil, fmt.Errorf("failed to list views: %w", err)
	}

	views := make([]ViewSummary, 0)
	for _, view := range viewResp.Views {
		views = append(views, ViewSummary{
			Name:        view.Name,
			URL:         view.URL,
			Description: view.Description,
		})
	}

	output := ViewListResponse{
		Views: views,
		Count: len(views),
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"views": output.Views,
			"count": output.Count,
		},
	}, nil
}

// JenkinsCredentialListExecutor handles jenkins-credential-list
type JenkinsCredentialListExecutor struct{}

func (e *JenkinsCredentialListExecutor) Type() string { return "jenkins-credential-list" }

func (e *JenkinsCredentialListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	jenkinsURL := getString(step.Config, "url")
	username := getString(step.Config, "username")
	apiToken := getString(step.Config, "password")
	domain := getString(step.Config, "domain")
	folder := getString(step.Config, "folder")

	if domain == "" {
		domain = "_"
	}

	client, err := getJenkinsClient(jenkinsURL, username, apiToken)
	if err != nil {
		return nil, fmt.Errorf("failed to create Jenkins client: %w", err)
	}

	// Build path for credentials
	var path string
	if folder == "" {
		path = fmt.Sprintf("/credentials/store/system/domain/%s/api/json?tree=credentials[id,displayName,description,typeName]", url.PathEscape(domain))
	} else {
		jobPath := getJobPathWithBase(folder, "")
		jobPath = strings.TrimSuffix(jobPath, "/job/")
		path = fmt.Sprintf("%s/credentials/domain/%s/api/json?tree=credentials[id,displayName,description,typeName]", jobPath, url.PathEscape(domain))
	}

	var credResp struct {
		Credentials []JenkinsCredential `json:"credentials"`
	}
	if err := client.getJSON(ctx, path, &credResp); err != nil {
		// Credentials API might not be available or user lacks permission
		return nil, fmt.Errorf("failed to list credentials (requires admin): %w", err)
	}

	credentials := make([]CredentialSummary, 0)
	for _, cred := range credResp.Credentials {
		credentials = append(credentials, CredentialSummary{
			ID:          cred.ID,
			DisplayName: cred.DisplayName,
			Description: cred.Description,
			TypeName:    cred.TypeName,
		})
	}

	output := CredentialListResponse{
		Credentials: credentials,
		Count:       len(credentials),
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"credentials": output.Credentials,
			"count":       output.Count,
		},
	}, nil
}

// JenkinsPluginListExecutor handles jenkins-plugin-list
type JenkinsPluginListExecutor struct{}

func (e *JenkinsPluginListExecutor) Type() string { return "jenkins-plugin-list" }

func (e *JenkinsPluginListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	jenkinsURL := getString(step.Config, "url")
	username := getString(step.Config, "username")
	apiToken := getString(step.Config, "password")
	activeOnly := getBool(step.Config, "activeOnly", false)

	client, err := getJenkinsClient(jenkinsURL, username, apiToken)
	if err != nil {
		return nil, fmt.Errorf("failed to create Jenkins client: %w", err)
	}

	path := "/pluginManager/api/json?tree=plugins[shortName,longName,version,active,enabled,pinned,deleted,downgradable,hasUpdate]"

	var pluginResp PluginResponse
	if err := client.getJSON(ctx, path, &pluginResp); err != nil {
		return nil, fmt.Errorf("failed to list plugins: %w", err)
	}

	plugins := make([]PluginSummary, 0)
	activeCount := 0
	for _, plugin := range pluginResp.Plugins {
		if activeOnly && !plugin.Active {
			continue
		}
		if plugin.Active {
			activeCount++
		}

		plugins = append(plugins, PluginSummary{
			ShortName: plugin.ShortName,
			LongName:  plugin.LongName,
			Version:   plugin.Version,
			Active:    plugin.Active,
			Enabled:   plugin.Enabled,
			HasUpdate: plugin.HasUpdate,
		})
	}

	output := PluginListResponse{
		Plugins: plugins,
		Count:   len(plugins),
		Active:  activeCount,
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"plugins": output.Plugins,
			"count":   output.Count,
			"active":  output.Active,
		},
	}, nil
}
