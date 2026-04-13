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
	"strconv"
	"strings"
	"time"

	"github.com/axiom-studio/skills.sdk/executor"
	"github.com/axiom-studio/skills.sdk/grpc"
	"github.com/axiom-studio/skills.sdk/resolver"
)

func main() {
	port := os.Getenv("SKILL_PORT")
	if port == "" {
		port = "50123"
	}

	server := grpc.NewSkillServer("skill-newrelic", "1.0.0")

	// Register all New Relic node types with schemas
	server.RegisterExecutorWithSchema("newrelic-query-nrql", &NRQLQueryExecutor{}, NRQLQuerySchema)
	server.RegisterExecutorWithSchema("newrelic-deploy-marker", &DeployMarkerExecutor{}, DeployMarkerSchema)
	server.RegisterExecutorWithSchema("newrelic-alert-policy-list", &AlertPolicyListExecutor{}, AlertPolicyListSchema)
	server.RegisterExecutorWithSchema("newrelic-alert-policy-create", &AlertPolicyCreateExecutor{}, AlertPolicyCreateSchema)
	server.RegisterExecutorWithSchema("newrelic-dashboard-list", &DashboardListExecutor{}, DashboardListSchema)
	server.RegisterExecutorWithSchema("newrelic-dashboard-create", &DashboardCreateExecutor{}, DashboardCreateSchema)
	server.RegisterExecutorWithSchema("newrelic-apdex", &ApdexExecutor{}, ApdexSchema)
	server.RegisterExecutorWithSchema("newrelic-application-list", &ApplicationListExecutor{}, ApplicationListSchema)

	fmt.Printf("Starting skill-newrelic gRPC server on port %s\n", port)
	if err := server.Serve(port); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to serve: %v\n", err)
		os.Exit(1)
	}
}

// ============================================================================
// HTTP Client Helpers
// ============================================================================

// newRelicClient handles New Relic API communication
type newRelicClient struct {
	apiKey    string
	accountID string
	baseURL   string
	httpClient *http.Client
}

func newNewRelicClient(apiKey, accountID string) *newRelicClient {
	baseURL := "https://api.newrelic.com"
	// Check for EU region
	if strings.Contains(apiKey, "eu") || os.Getenv("NEW_RELIC_REGION") == "eu" {
		baseURL = "https://api.eu.newrelic.com"
	}
	return &newRelicClient{
		apiKey:    apiKey,
		accountID: accountID,
		baseURL:   baseURL,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (c *newRelicClient) doRequest(ctx context.Context, method, path string, body interface{}) ([]byte, error) {
	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
		reqBody = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	req.Header.Set("API-Key", c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	return respBody, nil
}

// getAPIKey extracts API key from config or bindings
func getAPIKey(step *executor.StepDefinition, templateResolver executor.TemplateResolver) string {
	// Try config first
	if apiKey, ok := step.Config["apiKey"].(string); ok && apiKey != "" {
		return templateResolver.ResolveString(apiKey)
	}
	// Try bindings via resolver's GetBinding if available
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if key := r.GetBinding("newrelic_api_key"); key != nil {
			if s, ok := key.(string); ok {
				return s
			}
		}
	}
	// Try environment
	return os.Getenv("NEW_RELIC_API_KEY")
}

// getAccountID extracts account ID from config
func getAccountID(step *executor.StepDefinition, resolver executor.TemplateResolver) string {
	if accountID, ok := step.Config["accountId"].(string); ok && accountID != "" {
		return resolver.ResolveString(accountID)
	}
	if accountID, ok := step.Config["accountId"].(float64); ok {
		return strconv.FormatFloat(accountID, 'f', 0, 64)
	}
	if accountID, ok := step.Config["accountId"].(int); ok {
		return strconv.Itoa(accountID)
	}
	return os.Getenv("NEW_RELIC_ACCOUNT_ID")
}

// ============================================================================
// NRQL Query Executor
// ============================================================================

type NRQLQueryExecutor struct{}

type NRQLQueryConfig struct {
	APIKey    string `json:"apiKey" description:"New Relic API key (supports {{bindings.xxx}})"`
	AccountID string `json:"accountId" description:"New Relic Account ID"`
	NRQL      string `json:"nrql" description:"NRQL query to execute"`
}

var NRQLQuerySchema = resolver.NewSchemaBuilder("newrelic-query-nrql").
	WithName("NRQL Query").
	WithCategory("monitoring").
	WithIcon("activity").
	WithDescription("Execute NRQL queries against New Relic data").
	AddSection("Authentication").
		AddExpressionField("apiKey", "API Key",
			resolver.WithRequired(),
			resolver.WithPlaceholder("NRAK-xxxxxxxxxxxxx"),
			resolver.WithSensitive(),
			resolver.WithHint("Supports {{bindings.newrelic_api_key}}"),
		).
		AddTextField("accountId", "Account ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("1234567"),
		).
		EndSection().
	AddSection("Query").
		AddCodeField("nrql", "NRQL Query", "sql",
			resolver.WithRequired(),
			resolver.WithHeight(150),
			resolver.WithPlaceholder("SELECT average(cpuPercent) FROM SystemSample WHERE entity.guid = 'xxx' SINCE 1 hour ago"),
		).
		EndSection().
	Build()

func (e *NRQLQueryExecutor) Type() string { return "newrelic-query-nrql" }

func (e *NRQLQueryExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg NRQLQueryConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	} else {
		cfg.APIKey = getAPIKey(step, templateResolver)
		cfg.AccountID = getAccountID(step, templateResolver)
		if nrql, ok := step.Config["nrql"].(string); ok {
			cfg.NRQL = nrql
		}
	}

	if cfg.APIKey == "" {
		return nil, fmt.Errorf("apiKey is required")
	}
	if cfg.AccountID == "" {
		return nil, fmt.Errorf("accountId is required")
	}
	if cfg.NRQL == "" {
		return nil, fmt.Errorf("nrql is required")
	}

	client := newNewRelicClient(cfg.APIKey, cfg.AccountID)

	// Build NRQL query request
	queryBody := map[string]string{
		"query": cfg.NRQL,
	}

	respBody, err := client.doRequest(ctx, "POST", fmt.Sprintf("/graphql", ), queryBody)
	if err != nil {
		// Try the NRQL API endpoint as fallback
		nrqlURL := fmt.Sprintf("/v1/accounts/%s/nrql/results?nrql=%s",
			url.PathEscape(cfg.AccountID),
			url.QueryEscape(cfg.NRQL))
		respBody, err = client.doRequest(ctx, "GET", nrqlURL, nil)
		if err != nil {
			return nil, fmt.Errorf("NRQL query failed: %w", err)
		}
	}

	// Parse response
	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"results": result,
			"query":   cfg.NRQL,
		},
	}, nil
}

// ============================================================================
// Deploy Marker Executor
// ============================================================================

type DeployMarkerExecutor struct{}

type DeployMarkerConfig struct {
	APIKey        string `json:"apiKey" description:"New Relic API key"`
	AccountID     string `json:"accountId" description:"New Relic Account ID"`
	ApplicationID string `json:"applicationId" description:"Application ID to mark"`
	Revision      string `json:"revision" description:"Deployment revision/version"`
	Changelog     string `json:"changelog" description:"Description of changes"`
	User          string `json:"user" description:"User who performed the deployment"`
}

var DeployMarkerSchema = resolver.NewSchemaBuilder("newrelic-deploy-marker").
	WithName("Create Deploy Marker").
	WithCategory("monitoring").
	WithIcon("git-commit").
	WithDescription("Create a deployment marker in New Relic").
	AddSection("Authentication").
		AddExpressionField("apiKey", "API Key",
			resolver.WithRequired(),
			resolver.WithSensitive(),
		).
		AddTextField("accountId", "Account ID",
			resolver.WithRequired(),
		).
		EndSection().
	AddSection("Deployment").
		AddTextField("applicationId", "Application ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("123456789"),
			resolver.WithHint("New Relic Application ID"),
		).
		AddTextField("revision", "Revision/Version",
			resolver.WithRequired(),
			resolver.WithPlaceholder("v1.2.3"),
		).
		AddTextareaField("changelog", "Changelog",
			resolver.WithRows(3),
			resolver.WithPlaceholder("Bug fixes and performance improvements"),
		).
		AddTextField("user", "Deployed By",
			resolver.WithPlaceholder("CI/CD Pipeline"),
		).
		EndSection().
	Build()

func (e *DeployMarkerExecutor) Type() string { return "newrelic-deploy-marker" }

func (e *DeployMarkerExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg DeployMarkerConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	} else {
		cfg.APIKey = getAPIKey(step, templateResolver)
		cfg.AccountID = getAccountID(step, templateResolver)
		if appID, ok := step.Config["applicationId"].(string); ok {
			cfg.ApplicationID = appID
		}
		if rev, ok := step.Config["revision"].(string); ok {
			cfg.Revision = rev
		}
		if cl, ok := step.Config["changelog"].(string); ok {
			cfg.Changelog = cl
		}
		if user, ok := step.Config["user"].(string); ok {
			cfg.User = user
		}
	}

	if cfg.APIKey == "" {
		return nil, fmt.Errorf("apiKey is required")
	}
	if cfg.AccountID == "" {
		return nil, fmt.Errorf("accountId is required")
	}
	if cfg.ApplicationID == "" {
		return nil, fmt.Errorf("applicationId is required")
	}
	if cfg.Revision == "" {
		return nil, fmt.Errorf("revision is required")
	}

	client := newNewRelicClient(cfg.APIKey, cfg.AccountID)

	// Build deployment marker
	marker := map[string]interface{}{
		"deployment": map[string]interface{}{
			"revision":    cfg.Revision,
			"description": cfg.Changelog,
			"user":        cfg.User,
		},
	}

	// Use Deployments API v2
	path := fmt.Sprintf("/v2/applications/%s/deployments.json", cfg.ApplicationID)
	respBody, err := client.doRequest(ctx, "POST", path, marker)
	if err != nil {
		return nil, fmt.Errorf("failed to create deploy marker: %w", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"deployment": result,
			"revision":   cfg.Revision,
			"success":    true,
		},
	}, nil
}

// ============================================================================
// Alert Policy List Executor
// ============================================================================

type AlertPolicyListExecutor struct{}

type AlertPolicyListConfig struct {
	APIKey    string `json:"apiKey" description:"New Relic API key"`
	AccountID string `json:"accountId" description:"New Relic Account ID"`
}

var AlertPolicyListSchema = resolver.NewSchemaBuilder("newrelic-alert-policy-list").
	WithName("List Alert Policies").
	WithCategory("monitoring").
	WithIcon("bell").
	WithDescription("List all alert policies in New Relic").
	AddSection("Authentication").
		AddExpressionField("apiKey", "API Key",
			resolver.WithRequired(),
			resolver.WithSensitive(),
		).
		AddTextField("accountId", "Account ID",
			resolver.WithRequired(),
		).
		EndSection().
	Build()

func (e *AlertPolicyListExecutor) Type() string { return "newrelic-alert-policy-list" }

func (e *AlertPolicyListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg AlertPolicyListConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	} else {
		cfg.APIKey = getAPIKey(step, templateResolver)
		cfg.AccountID = getAccountID(step, templateResolver)
	}

	if cfg.APIKey == "" {
		return nil, fmt.Errorf("apiKey is required")
	}
	if cfg.AccountID == "" {
		return nil, fmt.Errorf("accountId is required")
	}

	client := newNewRelicClient(cfg.APIKey, cfg.AccountID)

	// Use Alert Policies API v2
	path := fmt.Sprintf("/v2/alerts/policies.json")
	respBody, err := client.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list alert policies: %w", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	policies := []interface{}{}
	if data, ok := result["policies"].([]interface{}); ok {
		policies = data
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"policies": policies,
			"count":    len(policies),
		},
	}, nil
}

// ============================================================================
// Alert Policy Create Executor
// ============================================================================

type AlertPolicyCreateExecutor struct{}

type AlertPolicyCreateConfig struct {
	APIKey       string                 `json:"apiKey" description:"New Relic API key"`
	AccountID    string                 `json:"accountId" description:"New Relic Account ID"`
	PolicyName   string                 `json:"policyName" description:"Name of the alert policy"`
	IncidentType string                 `json:"incidentType" description:"Type of incident (all, each)"`
	Conditions   []map[string]interface{} `json:"conditions" description:"Alert conditions to attach"`
}

var AlertPolicyCreateSchema = resolver.NewSchemaBuilder("newrelic-alert-policy-create").
	WithName("Create Alert Policy").
	WithCategory("monitoring").
	WithIcon("bell-plus").
	WithDescription("Create a new alert policy in New Relic").
	AddSection("Authentication").
		AddExpressionField("apiKey", "API Key",
			resolver.WithRequired(),
			resolver.WithSensitive(),
		).
		AddTextField("accountId", "Account ID",
			resolver.WithRequired(),
		).
		EndSection().
	AddSection("Policy").
		AddTextField("policyName", "Policy Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("My Alert Policy"),
		).
		AddSelectField("incidentType", "Incident Type", []resolver.SelectOption{
			{Label: "All conditions must be violated", Value: "all"},
			{Label: "Any condition can trigger", Value: "any"},
		}, resolver.WithDefault("all")).
		AddJSONField("conditions", "Conditions (optional)",
			resolver.WithHint("Array of condition IDs to attach to this policy"),
			resolver.WithHeight(100),
		).
		EndSection().
	Build()

func (e *AlertPolicyCreateExecutor) Type() string { return "newrelic-alert-policy-create" }

func (e *AlertPolicyCreateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg AlertPolicyCreateConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	} else {
		cfg.APIKey = getAPIKey(step, templateResolver)
		cfg.AccountID = getAccountID(step, templateResolver)
		if name, ok := step.Config["policyName"].(string); ok {
			cfg.PolicyName = name
		}
		if itype, ok := step.Config["incidentType"].(string); ok {
			cfg.IncidentType = itype
		}
		if conds, ok := step.Config["conditions"].([]interface{}); ok {
			for _, c := range conds {
				if cm, ok := c.(map[string]interface{}); ok {
					cfg.Conditions = append(cfg.Conditions, cm)
				}
			}
		}
	}

	if cfg.APIKey == "" {
		return nil, fmt.Errorf("apiKey is required")
	}
	if cfg.AccountID == "" {
		return nil, fmt.Errorf("accountId is required")
	}
	if cfg.PolicyName == "" {
		return nil, fmt.Errorf("policyName is required")
	}

	client := newNewRelicClient(cfg.APIKey, cfg.AccountID)

	// Build policy
	policy := map[string]interface{}{
		"policy": map[string]interface{}{
			"name":          cfg.PolicyName,
			"incident_type": cfg.IncidentType,
		},
	}

	path := "/v2/alerts/policies.json"
	respBody, err := client.doRequest(ctx, "POST", path, policy)
	if err != nil {
		return nil, fmt.Errorf("failed to create alert policy: %w", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	policyData, _ := result["policy"].(map[string]interface{})
	policyID := ""
	if id, ok := policyData["id"].(float64); ok {
		policyID = fmt.Sprintf("%.0f", id)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"policy":   policyData,
			"policyId": policyID,
			"success":  true,
		},
	}, nil
}

// ============================================================================
// Dashboard List Executor
// ============================================================================

type DashboardListExecutor struct{}

type DashboardListConfig struct {
	APIKey    string `json:"apiKey" description:"New Relic API key"`
	AccountID string `json:"accountId" description:"New Relic Account ID"`
}

var DashboardListSchema = resolver.NewSchemaBuilder("newrelic-dashboard-list").
	WithName("List Dashboards").
	WithCategory("monitoring").
	WithIcon("layout-dashboard").
	WithDescription("List all dashboards in New Relic").
	AddSection("Authentication").
		AddExpressionField("apiKey", "API Key",
			resolver.WithRequired(),
			resolver.WithSensitive(),
		).
		AddTextField("accountId", "Account ID",
			resolver.WithRequired(),
		).
		EndSection().
	Build()

func (e *DashboardListExecutor) Type() string { return "newrelic-dashboard-list" }

func (e *DashboardListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg DashboardListConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	} else {
		cfg.APIKey = getAPIKey(step, templateResolver)
		cfg.AccountID = getAccountID(step, templateResolver)
	}

	if cfg.APIKey == "" {
		return nil, fmt.Errorf("apiKey is required")
	}
	if cfg.AccountID == "" {
		return nil, fmt.Errorf("accountId is required")
	}

	client := newNewRelicClient(cfg.APIKey, cfg.AccountID)

	// Use Dashboards API
	path := fmt.Sprintf("/v2/dashboards.json")
	respBody, err := client.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list dashboards: %w", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	dashboards := []interface{}{}
	if data, ok := result["dashboards"].([]interface{}); ok {
		dashboards = data
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"dashboards": dashboards,
			"count":      len(dashboards),
		},
	}, nil
}

// ============================================================================
// Dashboard Create Executor
// ============================================================================

type DashboardCreateExecutor struct{}

type DashboardCreateConfig struct {
	APIKey       string                 `json:"apiKey" description:"New Relic API key"`
	AccountID    string                 `json:"accountId" description:"New Relic Account ID"`
	DashboardName string                `json:"dashboardName" description:"Name of the dashboard"`
	Widgets      []map[string]interface{} `json:"widgets" description:"Dashboard widgets configuration"`
}

var DashboardCreateSchema = resolver.NewSchemaBuilder("newrelic-dashboard-create").
	WithName("Create Dashboard").
	WithCategory("monitoring").
	WithIcon("layout-dashboard").
	WithDescription("Create a new dashboard in New Relic").
	AddSection("Authentication").
		AddExpressionField("apiKey", "API Key",
			resolver.WithRequired(),
			resolver.WithSensitive(),
		).
		AddTextField("accountId", "Account ID",
			resolver.WithRequired(),
		).
		EndSection().
	AddSection("Dashboard").
		AddTextField("dashboardName", "Dashboard Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("My Dashboard"),
		).
		AddJSONField("widgets", "Widgets Configuration",
			resolver.WithHint("Array of widget definitions with visualization, title, and NRQL queries"),
			resolver.WithHeight(200),
		).
		EndSection().
	Build()

func (e *DashboardCreateExecutor) Type() string { return "newrelic-dashboard-create" }

func (e *DashboardCreateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg DashboardCreateConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	} else {
		cfg.APIKey = getAPIKey(step, templateResolver)
		cfg.AccountID = getAccountID(step, templateResolver)
		if name, ok := step.Config["dashboardName"].(string); ok {
			cfg.DashboardName = name
		}
		if widgets, ok := step.Config["widgets"].([]interface{}); ok {
			for _, w := range widgets {
				if wm, ok := w.(map[string]interface{}); ok {
					cfg.Widgets = append(cfg.Widgets, wm)
				}
			}
		}
	}

	if cfg.APIKey == "" {
		return nil, fmt.Errorf("apiKey is required")
	}
	if cfg.AccountID == "" {
		return nil, fmt.Errorf("accountId is required")
	}
	if cfg.DashboardName == "" {
		return nil, fmt.Errorf("dashboardName is required")
	}

	client := newNewRelicClient(cfg.APIKey, cfg.AccountID)

	// Build dashboard
	widgets := make([]map[string]interface{}, 0)
	for _, w := range cfg.Widgets {
		widgets = append(widgets, w)
	}

	dashboard := map[string]interface{}{
		"dashboard": map[string]interface{}{
			"name":          cfg.DashboardName,
			"visibility":    "all",
			"editable":      "editable_by_all",
			"ui_url":        "",
			"widgets":       widgets,
		},
	}

	path := "/v2/dashboards.json"
	respBody, err := client.doRequest(ctx, "POST", path, dashboard)
	if err != nil {
		return nil, fmt.Errorf("failed to create dashboard: %w", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	dashboardData, _ := result["dashboard"].(map[string]interface{})
	dashboardID := ""
	if id, ok := dashboardData["id"].(float64); ok {
		dashboardID = fmt.Sprintf("%.0f", id)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"dashboard":   dashboardData,
			"dashboardId": dashboardID,
			"success":     true,
		},
	}, nil
}

// ============================================================================
// Apdex Executor
// ============================================================================

type ApdexExecutor struct{}

type ApdexConfig struct {
	APIKey        string `json:"apiKey" description:"New Relic API key"`
	AccountID     string `json:"accountId" description:"New Relic Account ID"`
	ApplicationID string `json:"applicationId" description:"Application ID"`
	TimeRange     string `json:"timeRange" description:"Time range (e.g., '1 hour ago', '30 minutes ago')"`
}

var ApdexSchema = resolver.NewSchemaBuilder("newrelic-apdex").
	WithName("Get Apdex Score").
	WithCategory("monitoring").
	WithIcon("gauge").
	WithDescription("Get Apdex score for a New Relic application").
	AddSection("Authentication").
		AddExpressionField("apiKey", "API Key",
			resolver.WithRequired(),
			resolver.WithSensitive(),
		).
		AddTextField("accountId", "Account ID",
			resolver.WithRequired(),
		).
		EndSection().
	AddSection("Application").
		AddTextField("applicationId", "Application ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("123456789"),
		).
		AddTextField("timeRange", "Time Range",
			resolver.WithDefault("1 hour ago"),
			resolver.WithPlaceholder("1 hour ago"),
			resolver.WithHint("New Relic time range syntax"),
		).
		EndSection().
	Build()

func (e *ApdexExecutor) Type() string { return "newrelic-apdex" }

func (e *ApdexExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg ApdexConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	} else {
		cfg.APIKey = getAPIKey(step, templateResolver)
		cfg.AccountID = getAccountID(step, templateResolver)
		if appID, ok := step.Config["applicationId"].(string); ok {
			cfg.ApplicationID = appID
		}
		if tr, ok := step.Config["timeRange"].(string); ok {
			cfg.TimeRange = tr
		}
		if cfg.TimeRange == "" {
			cfg.TimeRange = "1 hour ago"
		}
	}

	if cfg.APIKey == "" {
		return nil, fmt.Errorf("apiKey is required")
	}
	if cfg.AccountID == "" {
		return nil, fmt.Errorf("accountId is required")
	}
	if cfg.ApplicationID == "" {
		return nil, fmt.Errorf("applicationId is required")
	}

	client := newNewRelicClient(cfg.APIKey, cfg.AccountID)

	// Query Apdex using NRQL
	nrql := fmt.Sprintf("SELECT apdex(duration) FROM Transaction WHERE appId = '%s' SINCE %s",
		cfg.ApplicationID, cfg.TimeRange)

	queryBody := map[string]string{
		"query": nrql,
	}

	respBody, err := client.doRequest(ctx, "POST", "/graphql", queryBody)
	if err != nil {
		// Fallback to NRQL API
		nrqlURL := fmt.Sprintf("/v1/accounts/%s/nrql/results?nrql=%s",
			url.PathEscape(cfg.AccountID),
			url.QueryEscape(nrql))
		respBody, err = client.doRequest(ctx, "GET", nrqlURL, nil)
		if err != nil {
			return nil, fmt.Errorf("Apdex query failed: %w", err)
		}
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Extract Apdex score from results
	var apdexScore float64
	if data, ok := result["data"].(map[string]interface{}); ok {
		if actor, ok := data["actor"].(map[string]interface{}); ok {
			if query, ok := actor["query"].(map[string]interface{}); ok {
				if results, ok := query["results"].([]interface{}); ok && len(results) > 0 {
					if r, ok := results[0].(map[string]interface{}); ok {
						if score, ok := r["apdex"].(float64); ok {
							apdexScore = score
						}
					}
				}
			}
		}
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"apdex":       apdexScore,
			"application": cfg.ApplicationID,
			"timeRange":   cfg.TimeRange,
		},
	}, nil
}

// ============================================================================
// Application List Executor
// ============================================================================

type ApplicationListExecutor struct{}

type ApplicationListConfig struct {
	APIKey    string `json:"apiKey" description:"New Relic API key"`
	AccountID string `json:"accountId" description:"New Relic Account ID"`
	Name      string `json:"name" description:"Filter by application name (optional)"`
	Language  string `json:"language" description:"Filter by language (optional)"`
}

var ApplicationListSchema = resolver.NewSchemaBuilder("newrelic-application-list").
	WithName("List Applications").
	WithCategory("monitoring").
	WithIcon("application").
	WithDescription("List applications in New Relic APM").
	AddSection("Authentication").
		AddExpressionField("apiKey", "API Key",
			resolver.WithRequired(),
			resolver.WithSensitive(),
		).
		AddTextField("accountId", "Account ID",
			resolver.WithRequired(),
		).
		EndSection().
	AddSection("Filters").
		AddTextField("name", "Application Name",
			resolver.WithPlaceholder("my-app"),
			resolver.WithHint("Filter by name (optional)"),
		).
		AddTextField("language", "Language",
			resolver.WithPlaceholder("nodejs, python, java"),
			resolver.WithHint("Filter by language (optional)"),
		).
		EndSection().
	Build()

func (e *ApplicationListExecutor) Type() string { return "newrelic-application-list" }

func (e *ApplicationListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg ApplicationListConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	} else {
		cfg.APIKey = getAPIKey(step, templateResolver)
		cfg.AccountID = getAccountID(step, templateResolver)
		if name, ok := step.Config["name"].(string); ok {
			cfg.Name = name
		}
		if lang, ok := step.Config["language"].(string); ok {
			cfg.Language = lang
		}
	}

	if cfg.APIKey == "" {
		return nil, fmt.Errorf("apiKey is required")
	}
	if cfg.AccountID == "" {
		return nil, fmt.Errorf("accountId is required")
	}

	client := newNewRelicClient(cfg.APIKey, cfg.AccountID)

	// Build query parameters
	params := url.Values{}
	if cfg.Name != "" {
		params.Set("filter[name]", cfg.Name)
	}
	if cfg.Language != "" {
		params.Set("filter[language]", cfg.Language)
	}

	path := "/v2/applications.json"
	if len(params) > 0 {
		path += "?" + params.Encode()
	}

	respBody, err := client.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list applications: %w", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	applications := []interface{}{}
	if data, ok := result["applications"].([]interface{}); ok {
		applications = data
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"applications": applications,
			"count":        len(applications),
		},
	}, nil
}
