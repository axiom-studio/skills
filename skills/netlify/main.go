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
)

// Netlify API configuration
const (
	NetlifyAPIBase = "https://api.netlify.com/api/v1"
)

// NetlifySkill represents the Netlify skill
type NetlifySkill struct {
	client *http.Client
}

// NewNetlifySkill creates a new Netlify skill instance
func NewNetlifySkill() *NetlifySkill {
	return &NetlifySkill{
		client: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

// ============================================================================
// HELPER FUNCTIONS
// ============================================================================

// getString safely extracts a string from config map
func getString(config map[string]interface{}, key string) string {
	if v, ok := config[key]; ok {
		switch val := v.(type) {
		case string:
			return val
		case []byte:
			return string(val)
		case float64:
			return fmt.Sprintf("%v", val)
		case bool:
			return fmt.Sprintf("%v", val)
		default:
			return fmt.Sprintf("%v", val)
		}
	}
	return ""
}

// getBool safely extracts a bool from config map
func getBool(config map[string]interface{}, key string, defaultVal bool) bool {
	if v, ok := config[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
		if s, ok := v.(string); ok {
			return s == "true"
		}
	}
	return defaultVal
}

// getInt safely extracts an int from config map
func getInt(config map[string]interface{}, key string, defaultVal int) int {
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
	return defaultVal
}

// ============================================================================
// NETLIFY API RESPONSE TYPES
// ============================================================================

// NetlifySite represents a Netlify site
type NetlifySite struct {
	ID                 string                 `json:"id,omitempty"`
	Name               string                 `json:"name,omitempty"`
	CustomDomain       string                 `json:"custom_domain,omitempty"`
	AdminURL           string                 `json:"admin_url,omitempty"`
	URL                string                 `json:"url,omitempty"`
	SSLURL             string                 `json:"ssl_url,omitempty"`
	State              string                 `json:"state,omitempty"`
	Plan               string                 `json:"plan,omitempty"`
	NotificationEmail  string                 `json:"notification_email,omitempty"`
	AccountName        string                 `json:"account_name,omitempty"`
	AccountSlug        string                 `json:"account_slug,omitempty"`
	GitProvider        string                 `json:"git_provider,omitempty"`
	DeployURL          string                 `json:"deploy_url,omitempty"`
	CreatedAt          string                 `json:"created_at,omitempty"`
	UpdatedAt          string                 `json:"updated_at,omitempty"`
	UserID             string                 `json:"user_id,omitempty"`
	SessionID          string                 `json:"session_id,omitempty"`
	SSL                bool                   `json:"ssl,omitempty"`
	ForceSSL           bool                   `json:"force_ssl,omitempty"`
	ManagedDNS         bool                   `json:"managed_dns,omitempty"`
	DeployHook         string                 `json:"deploy_hook,omitempty"`
	Capabilities       map[string]interface{} `json:"capabilities,omitempty"`
	ProcessingSettings map[string]interface{} `json:"processing_settings,omitempty"`
	BuildSettings      map[string]interface{} `json:"build_settings,omitempty"`
}

// NetlifyDeploy represents a Netlify deployment
type NetlifyDeploy struct {
	ID                  string                 `json:"id,omitempty"`
	SiteID              string                 `json:"site_id,omitempty"`
	UserID              string                 `json:"user_id,omitempty"`
	BuildID             string                 `json:"build_id,omitempty"`
	State               string                 `json:"state,omitempty"`
	Name                string                 `json:"name,omitempty"`
	URL                 string                 `json:"url,omitempty"`
	SSLURL              string                 `json:"ssl_url,omitempty"`
	AdminURL            string                 `json:"admin_url,omitempty"`
	DeployURL           string                 `json:"deploy_url,omitempty"`
	DeploySSLURL        string                 `json:"deploy_ssl_url,omitempty"`
	ScreenshotURL       string                 `json:"screenshot_url,omitempty"`
	ReviewID            interface{}            `json:"review_id,omitempty"`
	Draft               bool                   `json:"draft,omitempty"`
	Required            bool                   `json:"required,omitempty"`
	ErrorMessage        string                 `json:"error_message,omitempty"`
	Branch              string                 `json:"branch,omitempty"`
	CommitRef           string                 `json:"commit_ref,omitempty"`
	CommitURL           string                 `json:"commit_url,omitempty"`
	Skip                bool                   `json:"skip,omitempty"`
	CreatedAt           string                 `json:"created_at,omitempty"`
	UpdatedAt           string                 `json:"updated_at,omitempty"`
	PublishedAt         string                 `json:"published_at,omitempty"`
	Context             string                 `json:"context,omitempty"`
	DeployTime          int                    `json:"deploy_time,omitempty"`
	AvailableFunctions  []string               `json:"available_functions,omitempty"`
	Summary             *NetlifyDeploySummary  `json:"summary,omitempty"`
	SiteCapabilities    map[string]interface{} `json:"site_capabilities,omitempty"`
	CommittedAt         string                 `json:"committed_at,omitempty"`
	ReviewURL           string                 `json:"review_url,omitempty"`
	ManualDeploy        bool                   `json:"manual_deploy,omitempty"`
	FileTrackingOptions map[string]interface{} `json:"file_tracking_options,omitempty"`
	Framework           string                 `json:"framework,omitempty"`
	FunctionSchedules   []interface{}          `json:"function_schedules,omitempty"`
}

// NetlifyDeploySummary represents deployment summary
type NetlifyDeploySummary struct {
	Status   string        `json:"status,omitempty"`
	Messages []interface{} `json:"messages,omitempty"`
}

// NetlifyDNSRecord represents a DNS record
type NetlifyDNSRecord struct {
	ID          string `json:"id,omitempty"`
	DNSZoneID   string `json:"dns_zone_id,omitempty"`
	SiteID      string `json:"site_id,omitempty"`
	Hostname    string `json:"hostname,omitempty"`
	Type        string `json:"type,omitempty"`
	Value       string `json:"value,omitempty"`
	TTL         int    `json:"ttl,omitempty"`
	Priority    int    `json:"priority,omitempty"`
	Weight      int    `json:"weight,omitempty"`
	Port        int    `json:"port,omitempty"`
	Flag        int    `json:"flag,omitempty"`
	Tag         string `json:"tag,omitempty"`
	Managed     bool   `json:"managed,omitempty"`
	DNSZoneIDv2 string `json:"dns_zone_id_v2,omitempty"`
}

// NetlifyEnvVar represents an environment variable
type NetlifyEnvVar struct {
	Key       string `json:"key,omitempty"`
	Value     string `json:"value,omitempty"`
	Context   string `json:"context,omitempty"`
	Branch    string `json:"branch,omitempty"`
	Sensitive bool   `json:"sensitive,omitempty"`
	ID        string `json:"id,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
}

// NetlifyBuildHook represents a build hook
type NetlifyBuildHook struct {
	ID        string `json:"id,omitempty"`
	Title     string `json:"title,omitempty"`
	URL       string `json:"url,omitempty"`
	SiteID    string `json:"site_id,omitempty"`
	Disabled  bool   `json:"disabled,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

// NetlifyError represents a Netlify API error response
type NetlifyError struct {
	Message string `json:"message,omitempty"`
}

// ============================================================================
// HTTP CLIENT HELPERS
// ============================================================================

// doRequest performs an HTTP request to the Netlify API
func (s *NetlifySkill) doRequest(ctx context.Context, method, path string, token string, body interface{}) ([]byte, error) {
	var reqBody io.Reader
	if body != nil {
		jsonBody, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
		reqBody = bytes.NewReader(jsonBody)
	}

	url := fmt.Sprintf("%s/%s", strings.TrimSuffix(NetlifyAPIBase, "/"), strings.TrimPrefix(path, "/"))

	req, err := http.NewRequestWithContext(ctx, method, url, reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "axiom-skills-netlify/1.0.0")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode >= 400 {
		var netlifyErr NetlifyError
		if err := json.Unmarshal(respBody, &netlifyErr); err == nil && netlifyErr.Message != "" {
			return nil, fmt.Errorf("netlify API error (%d): %s", resp.StatusCode, netlifyErr.Message)
		}
		return nil, fmt.Errorf("netlify API error (%d): %s", resp.StatusCode, string(respBody))
	}

	return respBody, nil
}

// ============================================================================
// NETLIFY-DEPLOY EXECUTOR
// ============================================================================

// DeployExecutor handles netlify-deploy node type
type DeployExecutor struct {
	*NetlifySkill
}

func (e *DeployExecutor) Type() string { return "netlify-deploy" }

func (e *DeployExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := step.Config
	token := getString(config, "authToken")
	siteID := getString(config, "siteId")
	branch := getString(config, "branch")
	commitRef := getString(config, "commitRef")
	context := getString(config, "context")
	title := getString(config, "title")

	if token == "" {
		return nil, fmt.Errorf("authToken is required")
	}
	if siteID == "" {
		return nil, fmt.Errorf("siteId is required")
	}

	// Build deploy request body
	deployReq := map[string]interface{}{}

	if branch != "" {
		deployReq["branch"] = branch
	}
	if commitRef != "" {
		deployReq["commit_ref"] = commitRef
	}
	if context != "" {
		deployReq["context"] = context
	}
	if title != "" {
		deployReq["title"] = title
	}

	respBody, err := e.doRequest(ctx, "POST", fmt.Sprintf("/sites/%s/deploys", siteID), token, deployReq)
	if err != nil {
		return nil, err
	}

	var deploy NetlifyDeploy
	if err := json.Unmarshal(respBody, &deploy); err != nil {
		return nil, fmt.Errorf("failed to parse response: %v", err)
	}

	output := map[string]interface{}{
		"result": json.RawMessage(respBody),
		"id":     deploy.ID,
		"state":  deploy.State,
		"url":    deploy.URL,
		"siteId": siteID,
	}

	return &executor.StepResult{Output: output}, nil
}

// ============================================================================
// NETLIFY-SITE-LIST EXECUTOR
// ============================================================================

// SiteListExecutor handles netlify-site-list node type
type SiteListExecutor struct {
	*NetlifySkill
}

func (e *SiteListExecutor) Type() string { return "netlify-site-list" }

func (e *SiteListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := step.Config
	token := getString(config, "authToken")
	filter := getString(config, "filter")
	name := getString(config, "name")

	if token == "" {
		return nil, fmt.Errorf("authToken is required")
	}

	// Build query parameters
	path := "/sites"
	queryParams := []string{}

	if filter != "" {
		queryParams = append(queryParams, fmt.Sprintf("filter=%s", filter))
	}
	if name != "" {
		queryParams = append(queryParams, fmt.Sprintf("name=%s", name))
	}

	if len(queryParams) > 0 {
		path += "?" + strings.Join(queryParams, "&")
	}

	respBody, err := e.doRequest(ctx, "GET", path, token, nil)
	if err != nil {
		return nil, err
	}

	var sites []NetlifySite
	if err := json.Unmarshal(respBody, &sites); err != nil {
		return nil, fmt.Errorf("failed to parse response: %v", err)
	}

	// Build summary output
	var siteSummaries []map[string]interface{}
	for _, site := range sites {
		summary := map[string]interface{}{
			"id":           site.ID,
			"name":         site.Name,
			"url":          site.URL,
			"state":        site.State,
			"customDomain": site.CustomDomain,
			"sslUrl":       site.SSLURL,
			"createdAt":    site.CreatedAt,
		}
		siteSummaries = append(siteSummaries, summary)
	}

	output := map[string]interface{}{
		"result": json.RawMessage(respBody),
		"count":  len(sites),
		"sites":  siteSummaries,
	}

	return &executor.StepResult{Output: output}, nil
}

// ============================================================================
// NETLIFY-SITE-GET EXECUTOR
// ============================================================================

// SiteGetExecutor handles netlify-site-get node type
type SiteGetExecutor struct {
	*NetlifySkill
}

func (e *SiteGetExecutor) Type() string { return "netlify-site-get" }

func (e *SiteGetExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := step.Config
	token := getString(config, "authToken")
	siteID := getString(config, "siteId")

	if token == "" {
		return nil, fmt.Errorf("authToken is required")
	}
	if siteID == "" {
		return nil, fmt.Errorf("siteId is required")
	}

	respBody, err := e.doRequest(ctx, "GET", fmt.Sprintf("/sites/%s", siteID), token, nil)
	if err != nil {
		return nil, err
	}

	var site NetlifySite
	if err := json.Unmarshal(respBody, &site); err != nil {
		return nil, fmt.Errorf("failed to parse response: %v", err)
	}

	output := map[string]interface{}{
		"result":       json.RawMessage(respBody),
		"id":           site.ID,
		"name":         site.Name,
		"url":          site.URL,
		"sslUrl":       site.SSLURL,
		"customDomain": site.CustomDomain,
		"adminUrl":     site.AdminURL,
		"state":        site.State,
		"plan":         site.Plan,
		"accountName":  site.AccountName,
		"gitProvider":  site.GitProvider,
		"deployUrl":    site.DeployURL,
		"createdAt":    site.CreatedAt,
		"updatedAt":    site.UpdatedAt,
	}

	return &executor.StepResult{Output: output}, nil
}

// ============================================================================
// NETLIFY-SITE-CREATE EXECUTOR
// ============================================================================

// SiteCreateExecutor handles netlify-site-create node type
type SiteCreateExecutor struct {
	*NetlifySkill
}

func (e *SiteCreateExecutor) Type() string { return "netlify-site-create" }

func (e *SiteCreateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := step.Config
	token := getString(config, "authToken")
	name := getString(config, "name")
	customDomain := getString(config, "customDomain")
	accountSlug := getString(config, "accountSlug")

	if token == "" {
		return nil, fmt.Errorf("authToken is required")
	}
	if name == "" {
		return nil, fmt.Errorf("name is required")
	}

	// Build site creation request
	createReq := map[string]interface{}{
		"name": name,
	}

	if customDomain != "" {
		createReq["custom_domain"] = customDomain
	}
	if accountSlug != "" {
		createReq["account_slug"] = accountSlug
	}

	respBody, err := e.doRequest(ctx, "POST", "/sites", token, createReq)
	if err != nil {
		return nil, err
	}

	var site NetlifySite
	if err := json.Unmarshal(respBody, &site); err != nil {
		return nil, fmt.Errorf("failed to parse response: %v", err)
	}

	output := map[string]interface{}{
		"result":       json.RawMessage(respBody),
		"id":           site.ID,
		"name":         site.Name,
		"url":          site.URL,
		"adminUrl":     site.AdminURL,
		"deployUrl":    site.DeployURL,
		"customDomain": site.CustomDomain,
		"state":        site.State,
		"success":      true,
	}

	return &executor.StepResult{Output: output}, nil
}

// ============================================================================
// NETLIFY-DEPLOY-LIST EXECUTOR
// ============================================================================

// DeployListExecutor handles netlify-deploy-list node type
type DeployListExecutor struct {
	*NetlifySkill
}

func (e *DeployListExecutor) Type() string { return "netlify-deploy-list" }

func (e *DeployListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := step.Config
	token := getString(config, "authToken")
	siteID := getString(config, "siteId")
	branch := getString(config, "branch")
	state := getString(config, "state")
	context := getString(config, "context")
	limit := getInt(config, "limit", 50)

	if token == "" {
		return nil, fmt.Errorf("authToken is required")
	}
	if siteID == "" {
		return nil, fmt.Errorf("siteId is required")
	}

	// Build query parameters
	path := fmt.Sprintf("/sites/%s/deploys", siteID)
	queryParams := []string{}

	if branch != "" {
		queryParams = append(queryParams, fmt.Sprintf("branch=%s", branch))
	}
	if state != "" {
		queryParams = append(queryParams, fmt.Sprintf("state=%s", state))
	}
	if context != "" {
		queryParams = append(queryParams, fmt.Sprintf("context=%s", context))
	}
	if limit > 0 {
		queryParams = append(queryParams, fmt.Sprintf("per_page=%d", limit))
	}

	if len(queryParams) > 0 {
		path += "?" + strings.Join(queryParams, "&")
	}

	respBody, err := e.doRequest(ctx, "GET", path, token, nil)
	if err != nil {
		return nil, err
	}

	var deploys []NetlifyDeploy
	if err := json.Unmarshal(respBody, &deploys); err != nil {
		return nil, fmt.Errorf("failed to parse response: %v", err)
	}

	// Build deploy summaries
	var deploySummaries []map[string]interface{}
	for _, deploy := range deploys {
		summary := map[string]interface{}{
			"id":        deploy.ID,
			"state":     deploy.State,
			"url":       deploy.URL,
			"branch":    deploy.Branch,
			"commitRef": deploy.CommitRef,
			"context":   deploy.Context,
			"createdAt": deploy.CreatedAt,
		}
		deploySummaries = append(deploySummaries, summary)
	}

	output := map[string]interface{}{
		"result":  json.RawMessage(respBody),
		"count":   len(deploys),
		"deploys": deploySummaries,
		"siteId":  siteID,
	}

	return &executor.StepResult{Output: output}, nil
}

// ============================================================================
// NETLIFY-DNS-RECORDS EXECUTOR
// ============================================================================

// DNSRecordsExecutor handles netlify-dns-records node type
type DNSRecordsExecutor struct {
	*NetlifySkill
}

func (e *DNSRecordsExecutor) Type() string { return "netlify-dns-records" }

func (e *DNSRecordsExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := step.Config
	token := getString(config, "authToken")
	siteID := getString(config, "siteId")
	recordType := getString(config, "type")
	hostname := getString(config, "hostname")

	if token == "" {
		return nil, fmt.Errorf("authToken is required")
	}
	if siteID == "" {
		return nil, fmt.Errorf("siteId is required")
	}

	// Build query parameters
	path := fmt.Sprintf("/sites/%s/dns_records", siteID)
	queryParams := []string{}

	if recordType != "" {
		queryParams = append(queryParams, fmt.Sprintf("type=%s", recordType))
	}
	if hostname != "" {
		queryParams = append(queryParams, fmt.Sprintf("hostname=%s", hostname))
	}

	if len(queryParams) > 0 {
		path += "?" + strings.Join(queryParams, "&")
	}

	respBody, err := e.doRequest(ctx, "GET", path, token, nil)
	if err != nil {
		return nil, err
	}

	var records []NetlifyDNSRecord
	if err := json.Unmarshal(respBody, &records); err != nil {
		return nil, fmt.Errorf("failed to parse response: %v", err)
	}

	// Build record summaries
	var recordSummaries []map[string]interface{}
	for _, record := range records {
		summary := map[string]interface{}{
			"id":       record.ID,
			"hostname": record.Hostname,
			"type":     record.Type,
			"value":    record.Value,
			"ttl":      record.TTL,
		}
		recordSummaries = append(recordSummaries, summary)
	}

	output := map[string]interface{}{
		"result":  json.RawMessage(respBody),
		"count":   len(records),
		"records": recordSummaries,
		"siteId":  siteID,
	}

	return &executor.StepResult{Output: output}, nil
}

// ============================================================================
// NETLIFY-ENV-SET EXECUTOR
// ============================================================================

// EnvSetExecutor handles netlify-env-set node type
type EnvSetExecutor struct {
	*NetlifySkill
}

func (e *EnvSetExecutor) Type() string { return "netlify-env-set" }

func (e *EnvSetExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := step.Config
	token := getString(config, "authToken")
	siteID := getString(config, "siteId")
	key := getString(config, "key")
	value := getString(config, "value")
	context := getString(config, "context")
	branch := getString(config, "branch")
	sensitive := getBool(config, "sensitive", false)

	if token == "" {
		return nil, fmt.Errorf("authToken is required")
	}
	if siteID == "" {
		return nil, fmt.Errorf("siteId is required")
	}
	if key == "" {
		return nil, fmt.Errorf("key is required")
	}
	if value == "" {
		return nil, fmt.Errorf("value is required")
	}

	// Build env var request
	envReq := map[string]interface{}{
		"key":   key,
		"value": value,
	}

	if context != "" {
		envReq["context"] = context
	}
	if branch != "" {
		envReq["branch"] = branch
	}
	if sensitive {
		envReq["sensitive"] = true
	}

	respBody, err := e.doRequest(ctx, "POST", fmt.Sprintf("/sites/%s/env", siteID), token, envReq)
	if err != nil {
		return nil, err
	}

	var envVar NetlifyEnvVar
	if err := json.Unmarshal(respBody, &envVar); err != nil {
		return nil, fmt.Errorf("failed to parse response: %v", err)
	}

	output := map[string]interface{}{
		"result":    json.RawMessage(respBody),
		"key":       envVar.Key,
		"context":   envVar.Context,
		"branch":    envVar.Branch,
		"sensitive": envVar.Sensitive,
		"success":   true,
		"siteId":    siteID,
	}

	return &executor.StepResult{Output: output}, nil
}

// ============================================================================
// NETLIFY-ENV-LIST EXECUTOR
// ============================================================================

// EnvListExecutor handles netlify-env-list node type
type EnvListExecutor struct {
	*NetlifySkill
}

func (e *EnvListExecutor) Type() string { return "netlify-env-list" }

func (e *EnvListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := step.Config
	token := getString(config, "authToken")
	siteID := getString(config, "siteId")
	context := getString(config, "context")

	if token == "" {
		return nil, fmt.Errorf("authToken is required")
	}
	if siteID == "" {
		return nil, fmt.Errorf("siteId is required")
	}

	// Build query parameters
	path := fmt.Sprintf("/sites/%s/env", siteID)
	queryParams := []string{}

	if context != "" {
		queryParams = append(queryParams, fmt.Sprintf("context=%s", context))
	}

	if len(queryParams) > 0 {
		path += "?" + strings.Join(queryParams, "&")
	}

	respBody, err := e.doRequest(ctx, "GET", path, token, nil)
	if err != nil {
		return nil, err
	}

	var envVars []NetlifyEnvVar
	if err := json.Unmarshal(respBody, &envVars); err != nil {
		return nil, fmt.Errorf("failed to parse response: %v", err)
	}

	// Build env var summaries
	var envVarSummaries []map[string]interface{}
	for _, envVar := range envVars {
		summary := map[string]interface{}{
			"key":       envVar.Key,
			"context":   envVar.Context,
			"branch":    envVar.Branch,
			"sensitive": envVar.Sensitive,
		}
		envVarSummaries = append(envVarSummaries, summary)
	}

	output := map[string]interface{}{
		"result":  json.RawMessage(respBody),
		"count":   len(envVars),
		"envVars": envVarSummaries,
		"siteId":  siteID,
	}

	return &executor.StepResult{Output: output}, nil
}

// ============================================================================
// NETLIFY-BUILD-HOOK-CREATE EXECUTOR
// ============================================================================

// BuildHookCreateExecutor handles netlify-build-hook-create node type
type BuildHookCreateExecutor struct {
	*NetlifySkill
}

func (e *BuildHookCreateExecutor) Type() string { return "netlify-build-hook-create" }

func (e *BuildHookCreateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := step.Config
	token := getString(config, "authToken")
	siteID := getString(config, "siteId")
	title := getString(config, "title")
	branch := getString(config, "branch")

	if token == "" {
		return nil, fmt.Errorf("authToken is required")
	}
	if siteID == "" {
		return nil, fmt.Errorf("siteId is required")
	}
	if title == "" {
		return nil, fmt.Errorf("title is required")
	}

	// Build hook request
	hookReq := map[string]interface{}{
		"title": title,
	}

	if branch != "" {
		hookReq["branch"] = branch
	}

	respBody, err := e.doRequest(ctx, "POST", fmt.Sprintf("/sites/%s/hooks", siteID), token, hookReq)
	if err != nil {
		return nil, err
	}

	var hook NetlifyBuildHook
	if err := json.Unmarshal(respBody, &hook); err != nil {
		return nil, fmt.Errorf("failed to parse response: %v", err)
	}

	output := map[string]interface{}{
		"result":    json.RawMessage(respBody),
		"id":        hook.ID,
		"title":     hook.Title,
		"url":       hook.URL,
		"siteId":    hook.SiteID,
		"disabled":  hook.Disabled,
		"createdAt": hook.CreatedAt,
		"success":   true,
	}

	return &executor.StepResult{Output: output}, nil
}

// ============================================================================
// MAIN
// ============================================================================

func main() {
	port := os.Getenv("SKILL_PORT")
	if port == "" {
		port = "50127"
	}

	server := grpc.NewSkillServer("skill-netlify", "1.0.0")
	skill := NewNetlifySkill()

	// Register all executors based on skill.yaml nodeTypes
	server.RegisterExecutor("netlify-deploy", &DeployExecutor{NetlifySkill: skill})
	server.RegisterExecutor("netlify-site-list", &SiteListExecutor{NetlifySkill: skill})
	server.RegisterExecutor("netlify-site-get", &SiteGetExecutor{NetlifySkill: skill})
	server.RegisterExecutor("netlify-site-create", &SiteCreateExecutor{NetlifySkill: skill})
	server.RegisterExecutor("netlify-deploy-list", &DeployListExecutor{NetlifySkill: skill})
	server.RegisterExecutor("netlify-dns-records", &DNSRecordsExecutor{NetlifySkill: skill})
	server.RegisterExecutor("netlify-env-set", &EnvSetExecutor{NetlifySkill: skill})
	server.RegisterExecutor("netlify-env-list", &EnvListExecutor{NetlifySkill: skill})
	server.RegisterExecutor("netlify-build-hook-create", &BuildHookCreateExecutor{NetlifySkill: skill})

	fmt.Printf("Starting skill-netlify gRPC server on port %s\n", port)
	if err := server.Serve(port); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to start server: %v\n", err)
		os.Exit(1)
	}
}
