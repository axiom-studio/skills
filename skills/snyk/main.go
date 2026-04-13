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
	"sync"
	"time"

	"github.com/axiom-studio/skills.sdk/executor"
	"github.com/axiom-studio/skills.sdk/grpc"
	"github.com/axiom-studio/skills.sdk/resolver"
)

const (
	iconSnyk        = "shield"
	snykAPIBase     = "https://api.snyk.io"
	snykAPIVersion  = "2024-01-23"
	defaultOrgLimit = 100
)

// Snyk clients cache
var (
	httpClients = make(map[string]*http.Client)
	clientMux   sync.RWMutex
)

func main() {
	// Get port from env or use default
	port := os.Getenv("SKILL_PORT")
	if port == "" {
		port = "50095"
	}

	// Create skill server
	server := grpc.NewSkillServer("skill-snyk", "1.0.0")

	// Register Snyk executors with schemas
	server.RegisterExecutorWithSchema("snyk-project-list", &SnykProjectListExecutor{}, SnykProjectListSchema)
	server.RegisterExecutorWithSchema("snyk-project-scan", &SnykProjectScanExecutor{}, SnykProjectScanSchema)
	server.RegisterExecutorWithSchema("snyk-issue-list", &SnykIssueListExecutor{}, SnykIssueListSchema)
	server.RegisterExecutorWithSchema("snyk-issue-ignore", &SnykIssueIgnoreExecutor{}, SnykIssueIgnoreSchema)
	server.RegisterExecutorWithSchema("snyk-test-deps", &SnykTestDepsExecutor{}, SnykTestDepsSchema)
	server.RegisterExecutorWithSchema("snyk-monitor", &SnykMonitorExecutor{}, SnykMonitorSchema)
	server.RegisterExecutorWithSchema("snyk-severity-list", &SnykSeverityListExecutor{}, SnykSeverityListSchema)
	server.RegisterExecutorWithSchema("snyk-license-list", &SnykLicenseListExecutor{}, SnykLicenseListSchema)

	fmt.Printf("Starting skill-snyk gRPC server on port %s\n", port)
	if err := server.Serve(port); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to serve: %v\n", err)
		os.Exit(1)
	}
}

// ============================================================================
// SNYK CLIENT HELPERS
// ============================================================================

// SnykConfig holds Snyk connection configuration
type SnykConfig struct {
	APIToken string
	OrgID    string
	BaseURL  string
}

// getHTTPClient returns an HTTP client (cached)
func getHTTPClient() *http.Client {
	clientMux.RLock()
	client, ok := httpClients["default"]
	clientMux.RUnlock()

	if ok {
		return client
	}

	clientMux.Lock()
	defer clientMux.Unlock()

	client = &http.Client{
		Timeout: 120 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 10,
			IdleConnTimeout:     90 * time.Second,
		},
	}
	httpClients["default"] = client
	return client
}

// makeSnykRequest makes an authenticated request to the Snyk API
func makeSnykRequest(ctx context.Context, cfg SnykConfig, method, path string, body interface{}) ([]byte, error) {
	if cfg.APIToken == "" {
		return nil, fmt.Errorf("Snyk API token is required")
	}

	if cfg.OrgID == "" {
		return nil, fmt.Errorf("Snyk Organization ID is required")
	}

	baseURL := snykAPIBase
	if cfg.BaseURL != "" {
		baseURL = cfg.BaseURL
	}

	// Ensure baseURL doesn't end with slash
	baseURL = strings.TrimSuffix(baseURL, "/")

	fullURL := baseURL + path

	var reqBody io.Reader
	if body != nil {
		jsonData, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
		reqBody = bytes.NewReader(jsonData)
	}

	req, err := http.NewRequestWithContext(ctx, method, fullURL, reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	req.Header.Set("Authorization", "token "+cfg.APIToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("snyk-version-date", snykAPIVersion)

	client := getHTTPClient()
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("Snyk API error (%d): %s", resp.StatusCode, string(respBody))
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

// parseSnykConfig extracts Snyk configuration from config map
func parseSnykConfig(config map[string]interface{}) SnykConfig {
	return SnykConfig{
		APIToken: getString(config, "apiToken"),
		OrgID:    getString(config, "orgId"),
		BaseURL:  getString(config, "baseURL"),
	}
}

// ============================================================================
// SNYK API RESPONSE STRUCTURES
// ============================================================================

// SnykProject represents a Snyk project
type SnykProject struct {
	ID             string                 `json:"id"`
	Name           string                 `json:"name"`
	Type           string                 `json:"type"`
	TargetFile     string                 `json:"targetFile,omitempty"`
	TargetReference string                `json:"targetReference,omitempty"`
	Origin         string                 `json:"origin,omitempty"`
	Created        string                 `json:"created,omitempty"`
	Updated        string                 `json:"updated,omitempty"`
	RemoteRepoURL  string                 `json:"remoteRepoUrl,omitempty"`
	IsMonitored    bool                   `json:"isMonitored,omitempty"`
	Lifecycle      string                 `json:"lifecycle,omitempty"`
	Readonly       bool                   `json:"readonly,omitempty"`
	TestFrequency  string                 `json:"testFrequency,omitempty"`
	Settings       map[string]interface{} `json:"settings,omitempty"`
}

// SnykIssue represents a vulnerability or issue
type SnykIssue struct {
	ID               string                 `json:"id"`
	Title            string                 `json:"title"`
	Description      string                 `json:"description,omitempty"`
	Severity         string                 `json:"severity"`
	PriorityScore    float64                `json:"priorityScore,omitempty"`
	Type             string                 `json:"type"`
	Package          string                 `json:"package,omitempty"`
	Version          string                 `json:"version,omitempty"`
	From             []string               `json:"from,omitempty"`
	UpgradePath      []string               `json:"upgradePath,omitempty"`
	IsPatchable      bool                   `json:"isPatchable,omitempty"`
	IsUpgradable     bool                   `json:"isUpgradable,omitempty"`
	CVEs             []string               `json:"cves,omitempty"`
	CWEs             []string               `json:"cwes,omitempty"`
	CVSSScore        float64                `json:"cvssScore,omitempty"`
	ExploitMaturity  string                 `json:"exploitMaturity,omitempty"`
	Modules          []string               `json:"modules,omitempty"`
	Published        string                 `json:"published,omitempty"`
	DisclosureTime   string                 `json:"disclosureTime,omitempty"`
	Identifiers      map[string]interface{} `json:"identifiers,omitempty"`
	Credits          []string               `json:"credits,omitempty"`
	References       []string               `json:"references,omitempty"`
	Functions        []string               `json:"functions,omitempty"`
	Details          map[string]interface{} `json:"details,omitempty"`
}

// SnykDependency represents a dependency
type SnykDependency struct {
	Name         string  `json:"name"`
	Version      string  `json:"version"`
	DepType      string  `json:"depType,omitempty"`
	IssueCount   int     `json:"issueCount,omitempty"`
	License      string  `json:"license,omitempty"`
	Licenses     []string `json:"licenses,omitempty"`
	IsDirect     bool    `json:"isDirect,omitempty"`
	IsTransitive bool    `json:"isTransitive,omitempty"`
}

// SnykLicenseViolation represents a license policy violation
type SnykLicenseViolation struct {
	ID            string `json:"id"`
	Package       string `json:"package"`
	Version       string `json:"version"`
	License       string `json:"license"`
	PolicyAction  string `json:"policyAction"`
	Severity      string `json:"severity"`
	DependencyPath []string `json:"dependencyPath,omitempty"`
}

// SnykScanResult represents a scan result
type SnykScanResult struct {
	ScanID       string                 `json:"scanId"`
	Status       string                 `json:"status"`
	ProjectID    string                 `json:"projectId"`
	TriggeredAt  string                 `json:"triggeredAt"`
	FinishedAt   string                 `json:"finishedAt,omitempty"`
	IssueCounts  map[string]int         `json:"issueCounts,omitempty"`
	Dependencies int                    `json:"dependencies,omitempty"`
	Messages     []string               `json:"messages,omitempty"`
}

// SnykIgnore represents an ignored issue
type SnykIgnore struct {
	ID         string `json:"id"`
	IssueID    string `json:"issueId"`
	ProjectID  string `json:"projectId"`
	Reason     string `json:"reason"`
	Expires    string `json:"expires,omitempty"`
	CreatedBy  string `json:"createdBy,omitempty"`
	Created    string `json:"created,omitempty"`
}

// SnykProjectsResponse represents the API response for listing projects
type SnykProjectsResponse struct {
	JSONAPI struct {
		Version string `json:"version"`
	} `json:"jsonapi"`
	Links struct {
		Self string `json:"self"`
		Next string `json:"next,omitempty"`
	} `json:"links"`
	Data []struct {
		ID         string `json:"id"`
		Type       string `json:"type"`
		Attributes struct {
			Name          string `json:"name"`
			TargetFile    string `json:"targetFile,omitempty"`
			TargetReference string `json:"targetReference,omitempty"`
			Origin        string `json:"origin,omitempty"`
			Created       string `json:"created,omitempty"`
			Updated       string `json:"updated,omitempty"`
			RemoteRepoURL string `json:"remoteRepoUrl,omitempty"`
			IsMonitored   bool   `json:"isMonitored,omitempty"`
			Lifecycle     string `json:"lifecycle,omitempty"`
			Readonly      bool   `json:"readonly,omitempty"`
			TestFrequency string `json:"testFrequency,omitempty"`
			Type          string `json:"type,omitempty"`
		} `json:"attributes"`
		Relationships struct {
			Organization struct {
				Data struct {
					ID   string `json:"id"`
					Type string `json:"type"`
				} `json:"data"`
			} `json:"organization"`
		} `json:"relationships"`
	} `json:"data"`
	Meta struct {
		Pagination struct {
			Total int `json:"total"`
			Limit int `json:"limit"`
		} `json:"pagination"`
	} `json:"meta"`
}

// SnykIssuesResponse represents the API response for listing issues
type SnykIssuesResponse struct {
	JSONAPI struct {
		Version string `json:"version"`
	} `json:"jsonapi"`
	Links struct {
		Self string `json:"self"`
		Next string `json:"next,omitempty"`
	} `json:"links"`
	Data []struct {
		ID         string `json:"id"`
		Type       string `json:"type"`
		Attributes struct {
			Key           string  `json:"key"`
			Title         string  `json:"title"`
			Description   string  `json:"description,omitempty"`
			Severity      string  `json:"severity"`
			PriorityScore float64 `json:"priorityScore,omitempty"`
			Type          string  `json:"type"`
			Package       string  `json:"package,omitempty"`
			Version       string  `json:"version,omitempty"`
			IsPatchable   bool    `json:"isPatchable,omitempty"`
			IsUpgradable  bool    `json:"isUpgradable,omitempty"`
			CVEs          []string `json:"cves,omitempty"`
			CVSSScore     float64 `json:"cvssScore,omitempty"`
			ExploitMaturity string `json:"exploitMaturity,omitempty"`
			Published     string  `json:"published,omitempty"`
			DisclosureTime string `json:"disclosureTime,omitempty"`
		} `json:"attributes"`
	} `json:"data"`
	Meta struct {
		Pagination struct {
			Total int `json:"total"`
			Limit int `json:"limit"`
		} `json:"pagination"`
	} `json:"meta"`
}

// SnykScanResponse represents the API response for creating a scan
type SnykScanResponse struct {
	JSONAPI struct {
		Version string `json:"version"`
	} `json:"jsonapi"`
	Data struct {
		ID         string `json:"id"`
		Type       string `json:"type"`
		Attributes struct {
			Status      string `json:"status"`
			TriggeredAt string `json:"triggeredAt"`
		} `json:"attributes"`
	} `json:"data"`
}

// SnykIgnoreResponse represents the API response for creating an ignore
type SnykIgnoreResponse struct {
	JSONAPI struct {
		Version string `json:"version"`
	} `json:"jsonapi"`
	Data struct {
		ID         string `json:"id"`
		Type       string `json:"type"`
		Attributes struct {
			Reason    string `json:"reason"`
			Expires   string `json:"expires,omitempty"`
			CreatedBy string `json:"createdBy,omitempty"`
			Created   string `json:"created,omitempty"`
		} `json:"attributes"`
		Relationships struct {
			Issue struct {
				Data struct {
					ID string `json:"id"`
				} `json:"data"`
			} `json:"issue"`
			Project struct {
				Data struct {
					ID string `json:"id"`
				} `json:"data"`
			} `json:"project"`
		} `json:"relationships"`
	} `json:"data"`
}

// SnykDependenciesResponse represents the API response for listing dependencies
type SnykDependenciesResponse struct {
	JSONAPI struct {
		Version string `json:"version"`
	} `json:"jsonapi"`
	Links struct {
		Self string `json:"self"`
		Next string `json:"next,omitempty"`
	} `json:"links"`
	Data []struct {
		ID         string `json:"id"`
		Type       string `json:"type"`
		Attributes struct {
			Name      string   `json:"name"`
			Version   string   `json:"version"`
			DepType   string   `json:"depType,omitempty"`
			Licenses  []string `json:"licenses,omitempty"`
			IssueIDs  []string `json:"issueIds,omitempty"`
			IssueCount int     `json:"issueCount,omitempty"`
		} `json:"attributes"`
	} `json:"data"`
	Meta struct {
		Pagination struct {
			Total int `json:"total"`
			Limit int `json:"limit"`
		} `json:"pagination"`
	} `json:"meta"`
}

// SnykMonitorResponse represents the API response for monitoring
type SnykMonitorResponse struct {
	JSONAPI struct {
		Version string `json:"version"`
	} `json:"jsonapi"`
	Data struct {
		ID         string `json:"id"`
		Type       string `json:"type"`
		Attributes struct {
			Status     string `json:"status"`
			MonitoredAt string `json:"monitoredAt"`
		} `json:"attributes"`
	} `json:"data"`
}

// ============================================================================
// SCHEMAS
// ============================================================================

// SnykProjectListSchema is the UI schema for snyk-project-list
var SnykProjectListSchema = resolver.NewSchemaBuilder("snyk-project-list").
	WithName("List Snyk Projects").
	WithCategory("action").
	WithIcon(iconSnyk).
	WithDescription("List all Snyk projects in your organization").
	AddSection("Connection").
		AddExpressionField("apiToken", "API Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("Enter your Snyk API token"),
			resolver.WithHint("Snyk API token from Settings > API Tokens"),
			resolver.WithSensitive(),
		).
		AddExpressionField("orgId", "Organization ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("Enter your Snyk Organization ID"),
			resolver.WithHint("Snyk Organization ID (found in Organization Settings)"),
		).
		AddExpressionField("baseURL", "Base URL",
			resolver.WithPlaceholder("https://api.snyk.io"),
			resolver.WithHint("Optional: Custom Snyk API base URL"),
		).
		EndSection().
	AddSection("Options").
		AddExpressionField("origin", "Origin Filter",
			resolver.WithPlaceholder("github, gitlab, bitbucket, cli"),
			resolver.WithHint("Filter projects by origin source"),
		).
		AddExpressionField("lifecycle", "Lifecycle Filter",
			resolver.WithPlaceholder("production, development"),
			resolver.WithHint("Filter projects by lifecycle stage"),
		).
		AddNumberField("limit", "Limit",
			resolver.WithDefault(100),
			resolver.WithMinMax(1, 500),
			resolver.WithHint("Maximum number of projects to return"),
		).
		EndSection().
	Build()

// SnykProjectScanSchema is the UI schema for snyk-project-scan
var SnykProjectScanSchema = resolver.NewSchemaBuilder("snyk-project-scan").
	WithName("Scan Snyk Project").
	WithCategory("action").
	WithIcon(iconSnyk).
	WithDescription("Trigger a vulnerability scan for a Snyk project").
	AddSection("Connection").
		AddExpressionField("apiToken", "API Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("Enter your Snyk API token"),
			resolver.WithHint("Snyk API token from Settings > API Tokens"),
			resolver.WithSensitive(),
		).
		AddExpressionField("orgId", "Organization ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("Enter your Snyk Organization ID"),
			resolver.WithHint("Snyk Organization ID"),
		).
		AddExpressionField("baseURL", "Base URL",
			resolver.WithPlaceholder("https://api.snyk.io"),
			resolver.WithHint("Optional: Custom Snyk API base URL"),
		).
		EndSection().
	AddSection("Project").
		AddExpressionField("projectId", "Project ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("Enter project ID"),
			resolver.WithHint("The Snyk project ID to scan"),
		).
		EndSection().
	AddSection("Options").
		AddToggleField("includeDependencies", "Include Dependencies",
			resolver.WithDefault(true),
			resolver.WithHint("Include dependency graph in scan"),
		).
		EndSection().
	Build()

// SnykIssueListSchema is the UI schema for snyk-issue-list
var SnykIssueListSchema = resolver.NewSchemaBuilder("snyk-issue-list").
	WithName("List Snyk Issues").
	WithCategory("action").
	WithIcon(iconSnyk).
	WithDescription("List vulnerabilities and issues for a Snyk project").
	AddSection("Connection").
		AddExpressionField("apiToken", "API Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("Enter your Snyk API token"),
			resolver.WithHint("Snyk API token from Settings > API Tokens"),
			resolver.WithSensitive(),
		).
		AddExpressionField("orgId", "Organization ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("Enter your Snyk Organization ID"),
			resolver.WithHint("Snyk Organization ID"),
		).
		AddExpressionField("baseURL", "Base URL",
			resolver.WithPlaceholder("https://api.snyk.io"),
			resolver.WithHint("Optional: Custom Snyk API base URL"),
		).
		EndSection().
	AddSection("Project").
		AddExpressionField("projectId", "Project ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("Enter project ID"),
			resolver.WithHint("The Snyk project ID"),
		).
		EndSection().
	AddSection("Filters").
		AddSelectField("severity", "Severity",
			[]resolver.SelectOption{
				{Label: "All", Value: ""},
				{Label: "Critical", Value: "critical"},
				{Label: "High", Value: "high"},
				{Label: "Medium", Value: "medium"},
				{Label: "Low", Value: "low"},
			},
			resolver.WithDefault(""),
			resolver.WithHint("Filter by severity level"),
		).
		AddSelectField("issueType", "Issue Type",
			[]resolver.SelectOption{
				{Label: "All", Value: ""},
				{Label: "Vulnerability", Value: "vuln"},
				{Label: "License", Value: "license"},
				{Label: "Configuration", Value: "config"},
			},
			resolver.WithDefault(""),
			resolver.WithHint("Filter by issue type"),
		).
		AddToggleField("isPatchable", "Patchable Only",
			resolver.WithDefault(false),
			resolver.WithHint("Only show issues that have a patch available"),
		).
		AddToggleField("isIgnored", "Include Ignored",
			resolver.WithDefault(false),
			resolver.WithHint("Include issues that have been ignored"),
		).
		EndSection().
	AddSection("Options").
		AddNumberField("limit", "Limit",
			resolver.WithDefault(100),
			resolver.WithMinMax(1, 500),
			resolver.WithHint("Maximum number of issues to return"),
		).
		EndSection().
	Build()

// SnykIssueIgnoreSchema is the UI schema for snyk-issue-ignore
var SnykIssueIgnoreSchema = resolver.NewSchemaBuilder("snyk-issue-ignore").
	WithName("Ignore Snyk Issue").
	WithCategory("action").
	WithIcon(iconSnyk).
	WithDescription("Ignore a vulnerability or issue in Snyk").
	AddSection("Connection").
		AddExpressionField("apiToken", "API Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("Enter your Snyk API token"),
			resolver.WithHint("Snyk API token from Settings > API Tokens"),
			resolver.WithSensitive(),
		).
		AddExpressionField("orgId", "Organization ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("Enter your Snyk Organization ID"),
			resolver.WithHint("Snyk Organization ID"),
		).
		AddExpressionField("baseURL", "Base URL",
			resolver.WithPlaceholder("https://api.snyk.io"),
			resolver.WithHint("Optional: Custom Snyk API base URL"),
		).
		EndSection().
	AddSection("Issue").
		AddExpressionField("projectId", "Project ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("Enter project ID"),
			resolver.WithHint("The Snyk project ID"),
		).
		AddExpressionField("issueId", "Issue ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("Enter issue ID"),
			resolver.WithHint("The issue/vulnerability ID to ignore"),
		).
		EndSection().
	AddSection("Ignore Settings").
		AddTextareaField("reason", "Reason",
			resolver.WithRequired(),
			resolver.WithRows(3),
			resolver.WithPlaceholder("Explain why this issue is being ignored..."),
			resolver.WithHint("Reason for ignoring this issue (required)"),
		).
		AddExpressionField("expires", "Expires",
			resolver.WithPlaceholder("2024-12-31"),
			resolver.WithHint("Optional: Date when the ignore expires (YYYY-MM-DD)"),
		).
		EndSection().
	Build()

// SnykTestDepsSchema is the UI schema for snyk-test-deps
var SnykTestDepsSchema = resolver.NewSchemaBuilder("snyk-test-deps").
	WithName("Test Dependencies").
	WithCategory("action").
	WithIcon(iconSnyk).
	WithDescription("Test project dependencies for vulnerabilities").
	AddSection("Connection").
		AddExpressionField("apiToken", "API Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("Enter your Snyk API token"),
			resolver.WithHint("Snyk API token from Settings > API Tokens"),
			resolver.WithSensitive(),
		).
		AddExpressionField("orgId", "Organization ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("Enter your Snyk Organization ID"),
			resolver.WithHint("Snyk Organization ID"),
		).
		AddExpressionField("baseURL", "Base URL",
			resolver.WithPlaceholder("https://api.snyk.io"),
			resolver.WithHint("Optional: Custom Snyk API base URL"),
		).
		EndSection().
	AddSection("Project").
		AddExpressionField("projectId", "Project ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("Enter project ID"),
			resolver.WithHint("The Snyk project ID"),
		).
		EndSection().
	AddSection("Options").
		AddSelectField("severity", "Min Severity",
			[]resolver.SelectOption{
				{Label: "All", Value: ""},
				{Label: "Low", Value: "low"},
				{Label: "Medium", Value: "medium"},
				{Label: "High", Value: "high"},
				{Label: "Critical", Value: "critical"},
			},
			resolver.WithDefault(""),
			resolver.WithHint("Minimum severity to report"),
		).
		AddNumberField("limit", "Limit",
			resolver.WithDefault(100),
			resolver.WithMinMax(1, 500),
			resolver.WithHint("Maximum number of dependencies to return"),
		).
		EndSection().
	Build()

// SnykMonitorSchema is the UI schema for snyk-monitor
var SnykMonitorSchema = resolver.NewSchemaBuilder("snyk-monitor").
	WithName("Monitor Project").
	WithCategory("action").
	WithIcon(iconSnyk).
	WithDescription("Monitor a project for new vulnerabilities").
	AddSection("Connection").
		AddExpressionField("apiToken", "API Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("Enter your Snyk API token"),
			resolver.WithHint("Snyk API token from Settings > API Tokens"),
			resolver.WithSensitive(),
		).
		AddExpressionField("orgId", "Organization ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("Enter your Snyk Organization ID"),
			resolver.WithHint("Snyk Organization ID"),
		).
		AddExpressionField("baseURL", "Base URL",
			resolver.WithPlaceholder("https://api.snyk.io"),
			resolver.WithHint("Optional: Custom Snyk API base URL"),
		).
		EndSection().
	AddSection("Project").
		AddExpressionField("projectId", "Project ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("Enter project ID"),
			resolver.WithHint("The Snyk project ID to monitor"),
		).
		EndSection().
	AddSection("Options").
		AddToggleField("enableNotifications", "Enable Notifications",
			resolver.WithDefault(true),
			resolver.WithHint("Receive notifications for new vulnerabilities"),
		).
		EndSection().
	Build()

// SnykSeverityListSchema is the UI schema for snyk-severity-list
var SnykSeverityListSchema = resolver.NewSchemaBuilder("snyk-severity-list").
	WithName("List Issues by Severity").
	WithCategory("action").
	WithIcon(iconSnyk).
	WithDescription("List issues grouped by severity level").
	AddSection("Connection").
		AddExpressionField("apiToken", "API Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("Enter your Snyk API token"),
			resolver.WithHint("Snyk API token from Settings > API Tokens"),
			resolver.WithSensitive(),
		).
		AddExpressionField("orgId", "Organization ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("Enter your Snyk Organization ID"),
			resolver.WithHint("Snyk Organization ID"),
		).
		AddExpressionField("baseURL", "Base URL",
			resolver.WithPlaceholder("https://api.snyk.io"),
			resolver.WithHint("Optional: Custom Snyk API base URL"),
		).
		EndSection().
	AddSection("Project").
		AddExpressionField("projectId", "Project ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("Enter project ID"),
			resolver.WithHint("The Snyk project ID"),
		).
		EndSection().
	AddSection("Options").
		AddToggleField("includeCounts", "Include Counts",
			resolver.WithDefault(true),
			resolver.WithHint("Include issue counts per severity"),
		).
		AddToggleField("includeDetails", "Include Details",
			resolver.WithDefault(true),
			resolver.WithHint("Include full issue details"),
		).
		EndSection().
	Build()

// SnykLicenseListSchema is the UI schema for snyk-license-list
var SnykLicenseListSchema = resolver.NewSchemaBuilder("snyk-license-list").
	WithName("List License Violations").
	WithCategory("action").
	WithIcon(iconSnyk).
	WithDescription("List license policy violations in a project").
	AddSection("Connection").
		AddExpressionField("apiToken", "API Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("Enter your Snyk API token"),
			resolver.WithHint("Snyk API token from Settings > API Tokens"),
			resolver.WithSensitive(),
		).
		AddExpressionField("orgId", "Organization ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("Enter your Snyk Organization ID"),
			resolver.WithHint("Snyk Organization ID"),
		).
		AddExpressionField("baseURL", "Base URL",
			resolver.WithPlaceholder("https://api.snyk.io"),
			resolver.WithHint("Optional: Custom Snyk API base URL"),
		).
		EndSection().
	AddSection("Project").
		AddExpressionField("projectId", "Project ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("Enter project ID"),
			resolver.WithHint("The Snyk project ID"),
		).
		EndSection().
	AddSection("Filters").
		AddSelectField("policyAction", "Policy Action",
			[]resolver.SelectOption{
				{Label: "All", Value: ""},
				{Label: "Deny", Value: "deny"},
				{Label: "Warn", Value: "warn"},
				{Label: "Review", Value: "review"},
			},
			resolver.WithDefault(""),
			resolver.WithHint("Filter by license policy action"),
		).
		EndSection().
	AddSection("Options").
		AddNumberField("limit", "Limit",
			resolver.WithDefault(100),
			resolver.WithMinMax(1, 500),
			resolver.WithHint("Maximum number of violations to return"),
		).
		EndSection().
	Build()

// ============================================================================
// EXECUTORS
// ============================================================================

// SnykProjectListExecutor handles snyk-project-list node type
type SnykProjectListExecutor struct{}

func (e *SnykProjectListExecutor) Type() string { return "snyk-project-list" }

func (e *SnykProjectListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)
	snykCfg := parseSnykConfig(config)

	origin := getString(config, "origin")
	lifecycle := getString(config, "lifecycle")
	limit := getInt(config, "limit", defaultOrgLimit)

	// Build query parameters
	queryParams := url.Values{}
	queryParams.Set("version", snykAPIVersion)
	queryParams.Set("limit", fmt.Sprintf("%d", limit))

	if origin != "" {
		queryParams.Set("filters.origin", origin)
	}
	if lifecycle != "" {
		queryParams.Set("filters.lifecycle", lifecycle)
	}

	path := fmt.Sprintf("/orgs/%s/projects?%s", snykCfg.OrgID, queryParams.Encode())

	respBody, err := makeSnykRequest(ctx, snykCfg, http.MethodGet, path, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list projects: %w", err)
	}

	var resp SnykProjectsResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Convert to output format
	projects := make([]map[string]interface{}, 0, len(resp.Data))
	for _, item := range resp.Data {
		project := map[string]interface{}{
			"id":              item.ID,
			"name":            item.Attributes.Name,
			"type":            item.Attributes.Type,
			"targetFile":      item.Attributes.TargetFile,
			"targetReference": item.Attributes.TargetReference,
			"origin":          item.Attributes.Origin,
			"created":         item.Attributes.Created,
			"updated":         item.Attributes.Updated,
			"remoteRepoUrl":   item.Attributes.RemoteRepoURL,
			"isMonitored":     item.Attributes.IsMonitored,
			"lifecycle":       item.Attributes.Lifecycle,
			"readonly":        item.Attributes.Readonly,
			"testFrequency":   item.Attributes.TestFrequency,
		}
		projects = append(projects, project)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"projects": projects,
			"total":    resp.Meta.Pagination.Total,
			"limit":    resp.Meta.Pagination.Limit,
		},
	}, nil
}

// SnykProjectScanExecutor handles snyk-project-scan node type
type SnykProjectScanExecutor struct{}

func (e *SnykProjectScanExecutor) Type() string { return "snyk-project-scan" }

func (e *SnykProjectScanExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)
	snykCfg := parseSnykConfig(config)

	projectId := getString(config, "projectId")
	if projectId == "" {
		return nil, fmt.Errorf("projectId is required")
	}

	includeDeps := getBool(config, "includeDependencies", true)

	// Build scan request
	scanReq := map[string]interface{}{
		"targetId": projectId,
		"attributes": map[string]interface{}{
			"includeDependencies": includeDeps,
		},
	}

	path := fmt.Sprintf("/orgs/%s/scans", snykCfg.OrgID)

	respBody, err := makeSnykRequest(ctx, snykCfg, http.MethodPost, path, scanReq)
	if err != nil {
		return nil, fmt.Errorf("failed to trigger scan: %w", err)
	}

	var resp SnykScanResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"scanId":      resp.Data.ID,
			"status":      resp.Data.Attributes.Status,
			"triggeredAt": resp.Data.Attributes.TriggeredAt,
			"projectId":   projectId,
			"message":     "Scan triggered successfully",
		},
	}, nil
}

// SnykIssueListExecutor handles snyk-issue-list node type
type SnykIssueListExecutor struct{}

func (e *SnykIssueListExecutor) Type() string { return "snyk-issue-list" }

func (e *SnykIssueListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)
	snykCfg := parseSnykConfig(config)

	projectId := getString(config, "projectId")
	if projectId == "" {
		return nil, fmt.Errorf("projectId is required")
	}

	severity := getString(config, "severity")
	issueType := getString(config, "issueType")
	isPatchable := getBool(config, "isPatchable", false)
	includeIgnored := getBool(config, "isIgnored", false)
	limit := getInt(config, "limit", defaultOrgLimit)

	// Build query parameters
	queryParams := url.Values{}
	queryParams.Set("version", snykAPIVersion)
	queryParams.Set("limit", fmt.Sprintf("%d", limit))

	if severity != "" {
		queryParams.Set("filters.severity", severity)
	}
	if issueType != "" {
		queryParams.Set("filters.type", issueType)
	}
	if isPatchable {
		queryParams.Set("filters.isPatchable", "true")
	}
	if includeIgnored {
		queryParams.Set("filters.isIgnored", "true")
	}

	path := fmt.Sprintf("/orgs/%s/issues?%s", snykCfg.OrgID, queryParams.Encode())

	respBody, err := makeSnykRequest(ctx, snykCfg, http.MethodGet, path, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list issues: %w", err)
	}

	var resp SnykIssuesResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Convert to output format
	issues := make([]map[string]interface{}, 0, len(resp.Data))
	for _, item := range resp.Data {
		issue := map[string]interface{}{
			"id":              item.ID,
			"key":             item.Attributes.Key,
			"title":           item.Attributes.Title,
			"description":     item.Attributes.Description,
			"severity":        item.Attributes.Severity,
			"priorityScore":   item.Attributes.PriorityScore,
			"type":            item.Attributes.Type,
			"package":         item.Attributes.Package,
			"version":         item.Attributes.Version,
			"isPatchable":     item.Attributes.IsPatchable,
			"isUpgradable":    item.Attributes.IsUpgradable,
			"cves":            item.Attributes.CVEs,
			"cvssScore":       item.Attributes.CVSSScore,
			"exploitMaturity": item.Attributes.ExploitMaturity,
			"published":       item.Attributes.Published,
			"disclosureTime":  item.Attributes.DisclosureTime,
		}
		issues = append(issues, issue)
	}

	// Calculate severity counts
	severityCounts := make(map[string]int)
	for _, item := range resp.Data {
		severityCounts[item.Attributes.Severity]++
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"issues":         issues,
			"total":          resp.Meta.Pagination.Total,
			"limit":          resp.Meta.Pagination.Limit,
			"severityCounts": severityCounts,
			"projectId":      projectId,
		},
	}, nil
}

// SnykIssueIgnoreExecutor handles snyk-issue-ignore node type
type SnykIssueIgnoreExecutor struct{}

func (e *SnykIssueIgnoreExecutor) Type() string { return "snyk-issue-ignore" }

func (e *SnykIssueIgnoreExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)
	snykCfg := parseSnykConfig(config)

	projectId := getString(config, "projectId")
	issueId := getString(config, "issueId")
	reason := getString(config, "reason")
	expires := getString(config, "expires")

	if projectId == "" {
		return nil, fmt.Errorf("projectId is required")
	}
	if issueId == "" {
		return nil, fmt.Errorf("issueId is required")
	}
	if reason == "" {
		return nil, fmt.Errorf("reason is required")
	}

	// Build ignore request
	ignoreReq := map[string]interface{}{
		"targetId": issueId,
		"attributes": map[string]interface{}{
			"reason": map[string]interface{}{
				"category": "custom",
				"text":     reason,
			},
		},
	}

	if expires != "" {
		ignoreReq["attributes"].(map[string]interface{})["expires"] = expires
	}

	path := fmt.Sprintf("/orgs/%s/ignores", snykCfg.OrgID)

	respBody, err := makeSnykRequest(ctx, snykCfg, http.MethodPost, path, ignoreReq)
	if err != nil {
		return nil, fmt.Errorf("failed to create ignore: %w", err)
	}

	var resp SnykIgnoreResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"ignoreId":    resp.Data.ID,
			"issueId":     resp.Data.Relationships.Issue.Data.ID,
			"projectId":   resp.Data.Relationships.Project.Data.ID,
			"reason":      resp.Data.Attributes.Reason,
			"expires":     resp.Data.Attributes.Expires,
			"createdBy":   resp.Data.Attributes.CreatedBy,
			"created":     resp.Data.Attributes.Created,
			"message":     "Issue ignored successfully",
		},
	}, nil
}

// SnykTestDepsExecutor handles snyk-test-deps node type
type SnykTestDepsExecutor struct{}

func (e *SnykTestDepsExecutor) Type() string { return "snyk-test-deps" }

func (e *SnykTestDepsExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)
	snykCfg := parseSnykConfig(config)

	projectId := getString(config, "projectId")
	if projectId == "" {
		return nil, fmt.Errorf("projectId is required")
	}

	minSeverity := getString(config, "severity")
	limit := getInt(config, "limit", defaultOrgLimit)

	// Build query parameters
	queryParams := url.Values{}
	queryParams.Set("version", snykAPIVersion)
	queryParams.Set("limit", fmt.Sprintf("%d", limit))

	path := fmt.Sprintf("/orgs/%s/packages?%s", snykCfg.OrgID, queryParams.Encode())

	respBody, err := makeSnykRequest(ctx, snykCfg, http.MethodGet, path, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list dependencies: %w", err)
	}

	var resp SnykDependenciesResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Convert to output format
	dependencies := make([]map[string]interface{}, 0, len(resp.Data))
	vulnerableDeps := 0

	for _, item := range resp.Data {
		dep := map[string]interface{}{
			"id":         item.ID,
			"name":       item.Attributes.Name,
			"version":    item.Attributes.Version,
			"depType":    item.Attributes.DepType,
			"licenses":   item.Attributes.Licenses,
			"issueIds":   item.Attributes.IssueIDs,
			"issueCount": item.Attributes.IssueCount,
			"isVulnerable": len(item.Attributes.IssueIDs) > 0,
		}

		if len(item.Attributes.IssueIDs) > 0 {
			vulnerableDeps++
		}

		// Filter by minimum severity if specified
		if minSeverity != "" && len(item.Attributes.IssueIDs) > 0 {
			// We would need to fetch issue details to filter properly
			// For now, include all deps with issues
			dependencies = append(dependencies, dep)
		} else if minSeverity == "" {
			dependencies = append(dependencies, dep)
		} else if len(item.Attributes.IssueIDs) > 0 {
			dependencies = append(dependencies, dep)
		}
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"dependencies":   dependencies,
			"total":          resp.Meta.Pagination.Total,
			"vulnerableCount": vulnerableDeps,
			"projectId":      projectId,
		},
	}, nil
}

// SnykMonitorExecutor handles snyk-monitor node type
type SnykMonitorExecutor struct{}

func (e *SnykMonitorExecutor) Type() string { return "snyk-monitor" }

func (e *SnykMonitorExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)
	snykCfg := parseSnykConfig(config)

	projectId := getString(config, "projectId")
	enableNotifications := getBool(config, "enableNotifications", true)

	if projectId == "" {
		return nil, fmt.Errorf("projectId is required")
	}

	// Build monitor request
	monitorReq := map[string]interface{}{
		"targetId": projectId,
		"attributes": map[string]interface{}{
			"enableNotifications": enableNotifications,
		},
	}

	path := fmt.Sprintf("/orgs/%s/monitor", snykCfg.OrgID)

	respBody, err := makeSnykRequest(ctx, snykCfg, http.MethodPost, path, monitorReq)
	if err != nil {
		return nil, fmt.Errorf("failed to enable monitoring: %w", err)
	}

	var resp SnykMonitorResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"monitorId":   resp.Data.ID,
			"status":      resp.Data.Attributes.Status,
			"monitoredAt": resp.Data.Attributes.MonitoredAt,
			"projectId":   projectId,
			"message":     "Monitoring enabled successfully",
		},
	}, nil
}

// SnykSeverityListExecutor handles snyk-severity-list node type
type SnykSeverityListExecutor struct{}

func (e *SnykSeverityListExecutor) Type() string { return "snyk-severity-list" }

func (e *SnykSeverityListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)
	snykCfg := parseSnykConfig(config)

	projectId := getString(config, "projectId")
	if projectId == "" {
		return nil, fmt.Errorf("projectId is required")
	}

	includeCounts := getBool(config, "includeCounts", true)
	includeDetails := getBool(config, "includeDetails", true)

	// Initialize severity levels
	severityLevels := []string{"critical", "high", "medium", "low"}
	severityMap := make(map[string]interface{})

	for _, severity := range severityLevels {
		// Build query parameters for each severity
		queryParams := url.Values{}
		queryParams.Set("version", snykAPIVersion)
		queryParams.Set("filters.severity", severity)
		queryParams.Set("limit", "500")

		path := fmt.Sprintf("/orgs/%s/issues?%s", snykCfg.OrgID, queryParams.Encode())

		respBody, err := makeSnykRequest(ctx, snykCfg, http.MethodGet, path, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to list %s severity issues: %w", severity, err)
		}

		var resp SnykIssuesResponse
		if err := json.Unmarshal(respBody, &resp); err != nil {
			return nil, fmt.Errorf("failed to parse response: %w", err)
		}

		severityData := make(map[string]interface{})

		if includeCounts {
			severityData["count"] = len(resp.Data)
			severityData["total"] = resp.Meta.Pagination.Total
		}

		if includeDetails {
			issues := make([]map[string]interface{}, 0, len(resp.Data))
			for _, item := range resp.Data {
				issue := map[string]interface{}{
					"id":              item.ID,
					"key":             item.Attributes.Key,
					"title":           item.Attributes.Title,
					"severity":        item.Attributes.Severity,
					"priorityScore":   item.Attributes.PriorityScore,
					"type":            item.Attributes.Type,
					"package":         item.Attributes.Package,
					"version":         item.Attributes.Version,
					"isPatchable":     item.Attributes.IsPatchable,
					"cves":            item.Attributes.CVEs,
					"cvssScore":       item.Attributes.CVSSScore,
				}
				issues = append(issues, issue)
			}
			severityData["issues"] = issues
		}

		severityMap[severity] = severityData
	}

	// Calculate total counts
	totalCounts := make(map[string]int)
	for _, severity := range severityLevels {
		if data, ok := severityMap[severity].(map[string]interface{}); ok {
			if count, ok := data["count"].(int); ok {
				totalCounts[severity] = count
			}
		}
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"projectId":    projectId,
			"bySeverity":   severityMap,
			"totalCounts":  totalCounts,
			"summary": map[string]interface{}{
				"critical": totalCounts["critical"],
				"high":     totalCounts["high"],
				"medium":   totalCounts["medium"],
				"low":      totalCounts["low"],
			},
		},
	}, nil
}

// SnykLicenseListExecutor handles snyk-license-list node type
type SnykLicenseListExecutor struct{}

func (e *SnykLicenseListExecutor) Type() string { return "snyk-license-list" }

func (e *SnykLicenseListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)
	snykCfg := parseSnykConfig(config)

	projectId := getString(config, "projectId")
	if projectId == "" {
		return nil, fmt.Errorf("projectId is required")
	}

	policyAction := getString(config, "policyAction")
	limit := getInt(config, "limit", defaultOrgLimit)

	// Build query parameters for license issues
	queryParams := url.Values{}
	queryParams.Set("version", snykAPIVersion)
	queryParams.Set("limit", fmt.Sprintf("%d", limit))
	queryParams.Set("filters.type", "license")

	if policyAction != "" {
		// Note: policyAction filtering may require additional API calls
		// depending on Snyk API support
		queryParams.Set("filters.policyAction", policyAction)
	}

	path := fmt.Sprintf("/orgs/%s/issues?%s", snykCfg.OrgID, queryParams.Encode())

	respBody, err := makeSnykRequest(ctx, snykCfg, http.MethodGet, path, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list license issues: %w", err)
	}

	var resp SnykIssuesResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Convert to output format - license violations
	violations := make([]map[string]interface{}, 0, len(resp.Data))
	licenseCounts := make(map[string]int)

	for _, item := range resp.Data {
		if item.Attributes.Type != "license" {
			continue
		}

		violation := map[string]interface{}{
			"id":           item.ID,
			"package":      item.Attributes.Package,
			"version":      item.Attributes.Version,
			"license":      item.Attributes.Title, // License name is in title for license issues
			"severity":     item.Attributes.Severity,
			"policyAction": "deny", // Default assumption
			"key":          item.Attributes.Key,
		}

		// Count by license
		licenseCounts[item.Attributes.Title]++

		violations = append(violations, violation)
	}

	// Get license summary from dependencies
	depQueryParams := url.Values{}
	depQueryParams.Set("version", snykAPIVersion)
	depQueryParams.Set("limit", fmt.Sprintf("%d", limit))

	depPath := fmt.Sprintf("/orgs/%s/packages?%s", snykCfg.OrgID, depQueryParams.Encode())

	depRespBody, err := makeSnykRequest(ctx, snykCfg, http.MethodGet, depPath, nil)
	if err == nil {
		var depResp SnykDependenciesResponse
		if err := json.Unmarshal(depRespBody, &depResp); err == nil {
			licenseSummary := make(map[string]interface{})
			for _, item := range depResp.Data {
				for _, lic := range item.Attributes.Licenses {
					if _, exists := licenseSummary[lic]; !exists {
						licenseSummary[lic] = 0
					}
					// This is a simplified count
				}
			}
			return &executor.StepResult{
				Output: map[string]interface{}{
					"violations":    violations,
					"total":         len(violations),
					"licenseCounts": licenseCounts,
					"projectId":     projectId,
				},
			}, nil
		}
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"violations":    violations,
			"total":         len(violations),
			"licenseCounts": licenseCounts,
			"projectId":     projectId,
		},
	}, nil
}
