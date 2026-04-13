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
	"sync"
	"time"

	"github.com/axiom-studio/skills.sdk/executor"
	"github.com/axiom-studio/skills.sdk/grpc"
	"github.com/axiom-studio/skills.sdk/resolver"
)

const (
	iconGrafana = "bar-chart"
)

// GrafanaClient represents a Grafana API client
type GrafanaClient struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

// NewGrafanaClient creates a new Grafana client
func NewGrafanaClient(baseURL, apiKey string) *GrafanaClient {
	return &GrafanaClient{
		baseURL: strings.TrimSuffix(baseURL, "/"),
		apiKey:  apiKey,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// doRequest performs an HTTP request to the Grafana API
func (c *GrafanaClient) doRequest(ctx context.Context, method, path string, body interface{}) ([]byte, error) {
	var reqBody io.Reader
	if body != nil {
		jsonBody, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
		reqBody = bytes.NewReader(jsonBody)
	}

	url := fmt.Sprintf("%s%s", c.baseURL, path)

	req, err := http.NewRequestWithContext(ctx, method, url, reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

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
		return nil, fmt.Errorf("grafana API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	return respBody, nil
}

// ============================================================================
// DASHBOARD OPERATIONS
// ============================================================================

// ListDashboards lists all dashboards
func (c *GrafanaClient) ListDashboards(ctx context.Context) ([]DashboardMeta, error) {
	respBody, err := c.doRequest(ctx, "GET", "/api/search?type=dash-db", nil)
	if err != nil {
		return nil, err
	}

	var dashboards []DashboardMeta
	if err := json.Unmarshal(respBody, &dashboards); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return dashboards, nil
}

// GetDashboard gets a dashboard by UID
func (c *GrafanaClient) GetDashboard(ctx context.Context, uid string) (*DashboardFull, error) {
	respBody, err := c.doRequest(ctx, "GET", fmt.Sprintf("/api/dashboards/uid/%s", uid), nil)
	if err != nil {
		return nil, err
	}

	var result DashboardFull
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return &result, nil
}

// CreateDashboard creates a new dashboard
func (c *GrafanaClient) CreateDashboard(ctx context.Context, dashboard map[string]interface{}) (*DashboardCreateResponse, error) {
	respBody, err := c.doRequest(ctx, "POST", "/api/dashboards/db", dashboard)
	if err != nil {
		return nil, err
	}

	var result DashboardCreateResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return &result, nil
}

// UpdateDashboard updates an existing dashboard
func (c *GrafanaClient) UpdateDashboard(ctx context.Context, dashboard map[string]interface{}) (*DashboardCreateResponse, error) {
	respBody, err := c.doRequest(ctx, "POST", "/api/dashboards/db", dashboard)
	if err != nil {
		return nil, err
	}

	var result DashboardCreateResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return &result, nil
}

// DeleteDashboard deletes a dashboard by UID
func (c *GrafanaClient) DeleteDashboard(ctx context.Context, uid string) (*DashboardDeleteResponse, error) {
	respBody, err := c.doRequest(ctx, "DELETE", fmt.Sprintf("/api/dashboards/uid/%s", uid), nil)
	if err != nil {
		return nil, err
	}

	var result DashboardDeleteResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return &result, nil
}

// ============================================================================
// DATASOURCE OPERATIONS
// ============================================================================

// ListDatasources lists all datasources
func (c *GrafanaClient) ListDatasources(ctx context.Context) ([]Datasource, error) {
	respBody, err := c.doRequest(ctx, "GET", "/api/datasources", nil)
	if err != nil {
		return nil, err
	}

	var datasources []Datasource
	if err := json.Unmarshal(respBody, &datasources); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return datasources, nil
}

// GetDatasource gets a datasource by ID
func (c *GrafanaClient) GetDatasource(ctx context.Context, id int) (*Datasource, error) {
	respBody, err := c.doRequest(ctx, "GET", fmt.Sprintf("/api/datasources/%d", id), nil)
	if err != nil {
		return nil, err
	}

	var datasource Datasource
	if err := json.Unmarshal(respBody, &datasource); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return &datasource, nil
}

// GetDatasourceByName gets a datasource by name
func (c *GrafanaClient) GetDatasourceByName(ctx context.Context, name string) (*Datasource, error) {
	respBody, err := c.doRequest(ctx, "GET", fmt.Sprintf("/api/datasources/name/%s", name), nil)
	if err != nil {
		return nil, err
	}

	var datasource Datasource
	if err := json.Unmarshal(respBody, &datasource); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return &datasource, nil
}

// CreateDatasource creates a new datasource
func (c *GrafanaClient) CreateDatasource(ctx context.Context, datasource DatasourceCreate) (*DatasourceCreateResponse, error) {
	respBody, err := c.doRequest(ctx, "POST", "/api/datasources", datasource)
	if err != nil {
		return nil, err
	}

	var result DatasourceCreateResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return &result, nil
}

// ============================================================================
// ANNOTATION OPERATIONS
// ============================================================================

// CreateAnnotation creates a new annotation
func (c *GrafanaClient) CreateAnnotation(ctx context.Context, annotation AnnotationCreate) (*AnnotationCreateResponse, error) {
	respBody, err := c.doRequest(ctx, "POST", "/api/annotations", annotation)
	if err != nil {
		return nil, err
	}

	var result AnnotationCreateResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return &result, nil
}

// ============================================================================
// ALERT RULE OPERATIONS
// ============================================================================

// ListAlertRules lists all alert rules
func (c *GrafanaClient) ListAlertRules(ctx context.Context) ([]AlertRule, error) {
	respBody, err := c.doRequest(ctx, "GET", "/api/v1/provisioning/alert-rules", nil)
	if err != nil {
		return nil, err
	}

	var rules []AlertRule
	if err := json.Unmarshal(respBody, &rules); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return rules, nil
}

// GetAlertRule gets an alert rule by UID
func (c *GrafanaClient) GetAlertRule(ctx context.Context, uid string) (*AlertRule, error) {
	respBody, err := c.doRequest(ctx, "GET", fmt.Sprintf("/api/v1/provisioning/alert-rules/%s", uid), nil)
	if err != nil {
		return nil, err
	}

	var rule AlertRule
	if err := json.Unmarshal(respBody, &rule); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return &rule, nil
}

// CreateAlertRule creates a new alert rule
func (c *GrafanaClient) CreateAlertRule(ctx context.Context, rule AlertRuleCreate) (*AlertRuleCreateResponse, error) {
	respBody, err := c.doRequest(ctx, "POST", "/api/v1/provisioning/alert-rules", rule)
	if err != nil {
		return nil, err
	}

	var result AlertRuleCreateResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return &result, nil
}

// ============================================================================
// DATA STRUCTURES
// ============================================================================

// DashboardMeta represents dashboard metadata from search
type DashboardMeta struct {
	ID          int64    `json:"id"`
	UID         string   `json:"uid"`
	Title       string   `json:"title"`
	URI         string   `json:"uri"`
	URL         string   `json:"url"`
	Slug        string   `json:"slug"`
	Type        string   `json:"type"`
	Tags        []string `json:"tags"`
	IsStarred   bool     `json:"isStarred"`
	FolderID    int64    `json:"folderId"`
	FolderUID   string   `json:"folderUid"`
	FolderTitle string   `json:"folderTitle"`
}

// DashboardFull represents a full dashboard response
type DashboardFull struct {
	Dashboard map[string]interface{} `json:"dashboard"`
	Meta      DashboardMeta          `json:"meta"`
}

// DashboardCreateResponse represents the response from creating/updating a dashboard
type DashboardCreateResponse struct {
	ID      int64  `json:"id"`
	UID     string `json:"uid"`
	URL     string `json:"url"`
	Status  string `json:"status"`
	Version int    `json:"version"`
	Slug    string `json:"slug"`
}

// DashboardDeleteResponse represents the response from deleting a dashboard
type DashboardDeleteResponse struct {
	ID    int64  `json:"id"`
	UID   string `json:"uid"`
	Title string `json:"title"`
}

// Datasource represents a Grafana datasource
type Datasource struct {
	ID             int64             `json:"id"`
	UID            string            `json:"uid"`
	Name           string            `json:"name"`
	Type           string            `json:"type"`
	URL            string            `json:"url"`
	Access         string            `json:"access"`
	Database       string            `json:"database"`
	User           string            `json:"user"`
	IsDefault      bool              `json:"isDefault"`
	BasicAuth      bool              `json:"basicAuth"`
	BasicAuthUser  string            `json:"basicAuthUser"`
	WithCredentials bool             `json:"withCredentials"`
	JSONData       map[string]interface{} `json:"jsonData"`
	SecureJSONData map[string]interface{} `json:"secureJsonData"`
	ReadOnly       bool              `json:"readOnly"`
}

// DatasourceCreate represents a datasource creation request
type DatasourceCreate struct {
	Name            string                 `json:"name"`
	Type            string                 `json:"type"`
	URL             string                 `json:"url"`
	Access          string                 `json:"access,omitempty"`
	Database        string                 `json:"database,omitempty"`
	User            string                 `json:"user,omitempty"`
	IsDefault       bool                   `json:"isDefault,omitempty"`
	BasicAuth       bool                   `json:"basicAuth,omitempty"`
	BasicAuthUser   string                 `json:"basicAuthUser,omitempty"`
	WithCredentials bool                   `json:"withCredentials,omitempty"`
	JSONData        map[string]interface{} `json:"jsonData,omitempty"`
	SecureJSONData  map[string]interface{} `json:"secureJsonData,omitempty"`
	UID             string                 `json:"uid,omitempty"`
	ReadOnly        bool                   `json:"readOnly,omitempty"`
}

// DatasourceCreateResponse represents the response from creating a datasource
type DatasourceCreateResponse struct {
	ID      int64  `json:"id"`
	UID     string `json:"uid"`
	Name    string `json:"name"`
	Message string `json:"message"`
}

// AnnotationCreate represents an annotation creation request
type AnnotationCreate struct {
	DashboardID int64    `json:"dashboardId,omitempty"`
	DashboardUID string  `json:"dashboardUID,omitempty"`
	PanelID     int64    `json:"panelId,omitempty"`
	Time        int64    `json:"time,omitempty"`
	TimeEnd     int64    `json:"timeEnd,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Text        string   `json:"text"`
}

// AnnotationCreateResponse represents the response from creating an annotation
type AnnotationCreateResponse struct {
	ID      int64  `json:"id"`
	Message string `json:"message"`
}

// AlertRule represents a Grafana alert rule
type AlertRule struct {
	ID            int64                  `json:"id"`
	UID           string                 `json:"uid"`
	Title         string                 `json:"title"`
	FolderUID     string                 `json:"folderUID"`
	RuleGroup     string                 `json:"ruleGroup"`
	OrgID         int64                  `json:"orgId"`
	ExecErrState  string                 `json:"execErrState"`
	NoDataState   string                 `json:"noDataState"`
	For           string                 `json:"for"`
	Data          []AlertQuery           `json:"data"`
	Updated       time.Time              `json:"updated"`
	Provenance    string                 `json:"provenance"`
	TitleString   string                 `json:"titleString"`
	Condition     string                 `json:"condition"`
	Labels        map[string]string      `json:"labels"`
	Annotations   map[string]string      `json:"annotations"`
	IsPaused      bool                   `json:"isPaused"`
}

// AlertQuery represents a query in an alert rule
type AlertQuery struct {
	RefID             string                 `json:"refId"`
	QueryType         string                 `json:"queryType"`
	RelativeTimeRange AlertTimeRange         `json:"relativeTimeRange"`
	DatasourceUID     string                 `json:"datasourceUid"`
	Model             map[string]interface{} `json:"model"`
}

// AlertTimeRange represents a time range for alert queries
type AlertTimeRange struct {
	From int `json:"from"`
	To   int `json:"to"`
}

// AlertRuleCreate represents an alert rule creation request
type AlertRuleCreate struct {
	Title         string                 `json:"title"`
	FolderUID     string                 `json:"folderUID"`
	RuleGroup     string                 `json:"ruleGroup"`
	OrgID         int64                  `json:"orgId,omitempty"`
	ExecErrState  string                 `json:"execErrState,omitempty"`
	NoDataState   string                 `json:"noDataState,omitempty"`
	For           string                 `json:"for,omitempty"`
	Data          []AlertQuery           `json:"data"`
	Condition     string                 `json:"condition"`
	Labels        map[string]string      `json:"labels,omitempty"`
	Annotations   map[string]string      `json:"annotations,omitempty"`
	IsPaused      bool                   `json:"isPaused,omitempty"`
}

// AlertRuleCreateResponse represents the response from creating an alert rule
type AlertRuleCreateResponse struct {
	ID    int64  `json:"id"`
	UID   string `json:"uid"`
	Title string `json:"title"`
}

// ============================================================================
// CLIENT CACHE
// ============================================================================

var (
	clients   = make(map[string]*GrafanaClient)
	clientMux sync.RWMutex
)

// getGrafanaClient returns a cached Grafana client
func getGrafanaClient(baseURL, apiKey string) (*GrafanaClient, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("Grafana API key is required")
	}
	if baseURL == "" {
		return nil, fmt.Errorf("Grafana base URL is required")
	}

	cacheKey := fmt.Sprintf("%s:%s", baseURL, apiKey)

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

	client = NewGrafanaClient(baseURL, apiKey)
	clients[cacheKey] = client
	return client, nil
}

// ============================================================================
// HELPER FUNCTIONS
// ============================================================================

// getString gets a string value from config
func getString(config map[string]interface{}, key string) string {
	if v, ok := config[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// getInt gets an int value from config
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

// getBool gets a bool value from config
func getBool(config map[string]interface{}, key string, def bool) bool {
	if v, ok := config[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return def
}

// getMap gets a map value from config
func getMap(config map[string]interface{}, key string) map[string]interface{} {
	if v, ok := config[key]; ok {
		if m, ok := v.(map[string]interface{}); ok {
			return m
		}
	}
	return nil
}

// getInterfaceSlice gets a slice of interfaces from config
func getInterfaceSlice(config map[string]interface{}, key string) []interface{} {
	if v, ok := config[key]; ok {
		if arr, ok := v.([]interface{}); ok {
			return arr
		}
	}
	return nil
}

// ============================================================================
// SCHEMAS
// ============================================================================

// DashboardListSchema is the UI schema for grafana-dashboard-list
var DashboardListSchema = resolver.NewSchemaBuilder("grafana-dashboard-list").
	WithName("List Dashboards").
	WithCategory("action").
	WithIcon(iconGrafana).
	WithDescription("List all dashboards in Grafana").
	AddSection("Connection").
		AddExpressionField("baseURL", "Grafana URL",
			resolver.WithRequired(),
			resolver.WithPlaceholder("https://grafana.example.com"),
			resolver.WithHint("Grafana server URL (supports {{bindings.xxx}})"),
		).
		AddExpressionField("apiKey", "API Key",
			resolver.WithRequired(),
			resolver.WithPlaceholder("glsa_xxx"),
			resolver.WithHint("Grafana API key or service account token (supports {{bindings.xxx}})"),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Filters").
		AddExpressionField("folderUID", "Folder UID",
			resolver.WithHint("Filter by folder UID (optional)"),
		).
		AddTagsField("tags", "Tags",
			resolver.WithHint("Filter by tags (optional)"),
		).
		EndSection().
	Build()

// DashboardCreateSchema is the UI schema for grafana-dashboard-create
var DashboardCreateSchema = resolver.NewSchemaBuilder("grafana-dashboard-create").
	WithName("Create Dashboard").
	WithCategory("action").
	WithIcon(iconGrafana).
	WithDescription("Create a new dashboard in Grafana").
	AddSection("Connection").
		AddExpressionField("baseURL", "Grafana URL",
			resolver.WithRequired(),
			resolver.WithPlaceholder("https://grafana.example.com"),
			resolver.WithHint("Grafana server URL (supports {{bindings.xxx}})"),
		).
		AddExpressionField("apiKey", "API Key",
			resolver.WithRequired(),
			resolver.WithPlaceholder("glsa_xxx"),
			resolver.WithHint("Grafana API key or service account token (supports {{bindings.xxx}})"),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Dashboard").
		AddExpressionField("title", "Title",
			resolver.WithRequired(),
			resolver.WithPlaceholder("My Dashboard"),
			resolver.WithHint("Dashboard title"),
		).
		AddExpressionField("uid", "UID",
			resolver.WithHint("Optional custom UID (auto-generated if not provided)"),
		).
		AddExpressionField("folderUID", "Folder UID",
			resolver.WithHint("Folder UID to place dashboard in (optional)"),
		).
		AddJSONField("panels", "Panels",
			resolver.WithHeight(200),
			resolver.WithHint("Dashboard panels configuration (optional)"),
		).
		AddTagsField("tags", "Tags",
			resolver.WithHint("Dashboard tags"),
		).
		AddToggleField("editable", "Editable",
			resolver.WithDefault(true),
			resolver.WithHint("Allow dashboard editing"),
		).
		EndSection().
	Build()

// DashboardUpdateSchema is the UI schema for grafana-dashboard-update
var DashboardUpdateSchema = resolver.NewSchemaBuilder("grafana-dashboard-update").
	WithName("Update Dashboard").
	WithCategory("action").
	WithIcon(iconGrafana).
	WithDescription("Update an existing dashboard in Grafana").
	AddSection("Connection").
		AddExpressionField("baseURL", "Grafana URL",
			resolver.WithRequired(),
			resolver.WithPlaceholder("https://grafana.example.com"),
			resolver.WithHint("Grafana server URL (supports {{bindings.xxx}})"),
		).
		AddExpressionField("apiKey", "API Key",
			resolver.WithRequired(),
			resolver.WithPlaceholder("glsa_xxx"),
			resolver.WithHint("Grafana API key or service account token (supports {{bindings.xxx}})"),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Dashboard").
		AddExpressionField("uid", "Dashboard UID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-dashboard-uid"),
			resolver.WithHint("UID of the dashboard to update"),
		).
		AddExpressionField("title", "Title",
			resolver.WithHint("New title (optional)"),
		).
		AddJSONField("panels", "Panels",
			resolver.WithHeight(200),
			resolver.WithHint("Updated panels configuration (optional)"),
		).
		AddTagsField("tags", "Tags",
			resolver.WithHint("Updated tags (optional)"),
		).
		AddNumberField("version", "Version",
			resolver.WithHint("Dashboard version (required for updates to prevent conflicts)"),
		).
		EndSection().
	Build()

// DashboardDeleteSchema is the UI schema for grafana-dashboard-delete
var DashboardDeleteSchema = resolver.NewSchemaBuilder("grafana-dashboard-delete").
	WithName("Delete Dashboard").
	WithCategory("action").
	WithIcon(iconGrafana).
	WithDescription("Delete a dashboard from Grafana").
	AddSection("Connection").
		AddExpressionField("baseURL", "Grafana URL",
			resolver.WithRequired(),
			resolver.WithPlaceholder("https://grafana.example.com"),
			resolver.WithHint("Grafana server URL (supports {{bindings.xxx}})"),
		).
		AddExpressionField("apiKey", "API Key",
			resolver.WithRequired(),
			resolver.WithPlaceholder("glsa_xxx"),
			resolver.WithHint("Grafana API key or service account token (supports {{bindings.xxx}})"),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Dashboard").
		AddExpressionField("uid", "Dashboard UID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-dashboard-uid"),
			resolver.WithHint("UID of the dashboard to delete"),
		).
		EndSection().
	Build()

// DatasourceListSchema is the UI schema for grafana-datasource-list
var DatasourceListSchema = resolver.NewSchemaBuilder("grafana-datasource-list").
	WithName("List Datasources").
	WithCategory("action").
	WithIcon(iconGrafana).
	WithDescription("List all datasources in Grafana").
	AddSection("Connection").
		AddExpressionField("baseURL", "Grafana URL",
			resolver.WithRequired(),
			resolver.WithPlaceholder("https://grafana.example.com"),
			resolver.WithHint("Grafana server URL (supports {{bindings.xxx}})"),
		).
		AddExpressionField("apiKey", "API Key",
			resolver.WithRequired(),
			resolver.WithPlaceholder("glsa_xxx"),
			resolver.WithHint("Grafana API key or service account token (supports {{bindings.xxx}})"),
			resolver.WithSensitive(),
		).
		EndSection().
	Build()

// DatasourceCreateSchema is the UI schema for grafana-datasource-create
var DatasourceCreateSchema = resolver.NewSchemaBuilder("grafana-datasource-create").
	WithName("Create Datasource").
	WithCategory("action").
	WithIcon(iconGrafana).
	WithDescription("Create a new datasource in Grafana").
	AddSection("Connection").
		AddExpressionField("baseURL", "Grafana URL",
			resolver.WithRequired(),
			resolver.WithPlaceholder("https://grafana.example.com"),
			resolver.WithHint("Grafana server URL (supports {{bindings.xxx}})"),
		).
		AddExpressionField("apiKey", "API Key",
			resolver.WithRequired(),
			resolver.WithPlaceholder("glsa_xxx"),
			resolver.WithHint("Grafana API key or service account token (supports {{bindings.xxx}})"),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Datasource").
		AddExpressionField("name", "Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("My Datasource"),
			resolver.WithHint("Datasource name"),
		).
		AddSelectField("type", "Type",
			[]resolver.SelectOption{
				{Label: "Prometheus", Value: "prometheus"},
				{Label: "Loki", Value: "loki"},
				{Label: "Elasticsearch", Value: "elasticsearch"},
				{Label: "InfluxDB", Value: "influxdb"},
				{Label: "MySQL", Value: "mysql"},
				{Label: "PostgreSQL", Value: "postgres"},
				{Label: "MSSQL", Value: "mssql"},
				{Label: "CloudWatch", Value: "cloudwatch"},
				{Label: "Azure Monitor", Value: "grafana-azure-monitor-datasource"},
				{Label: "Google Cloud Monitoring", Value: "stackdriver"},
				{Label: "Jaeger", Value: "jaeger"},
				{Label: "Zipkin", Value: "zipkin"},
				{Label: "Tempo", Value: "tempo"},
				{Label: "Graphite", Value: "graphite"},
				{Label: "OpenTSDB", Value: "opentsdb"},
			},
			resolver.WithRequired(),
			resolver.WithHint("Datasource type"),
		).
		AddExpressionField("url", "URL",
			resolver.WithRequired(),
			resolver.WithPlaceholder("http://prometheus:9090"),
			resolver.WithHint("Datasource URL"),
		).
		AddExpressionField("uid", "UID",
			resolver.WithHint("Optional custom UID"),
		).
		AddExpressionField("database", "Database",
			resolver.WithHint("Database name (for SQL/InfluxDB datasources)"),
		).
		AddExpressionField("user", "User",
			resolver.WithHint("Username (for SQL datasources)"),
		).
		AddToggleField("isDefault", "Set as Default",
			resolver.WithDefault(false),
			resolver.WithHint("Set as default datasource"),
		).
		AddToggleField("basicAuth", "Enable Basic Auth",
			resolver.WithDefault(false),
			resolver.WithHint("Enable basic authentication"),
		).
		AddExpressionField("basicAuthUser", "Basic Auth User",
			resolver.WithHint("Basic auth username"),
		).
		AddExpressionField("basicAuthPassword", "Basic Auth Password",
			resolver.WithHint("Basic auth password"),
			resolver.WithSensitive(),
		).
		AddJSONField("jsonData", "JSON Data",
			resolver.WithHeight(150),
			resolver.WithHint("Additional JSON configuration (type-specific)"),
		).
		AddJSONField("secureJsonData", "Secure JSON Data",
			resolver.WithHeight(150),
			resolver.WithHint("Secure JSON data (passwords, tokens)"),
			resolver.WithSensitive(),
		).
		EndSection().
	Build()

// AnnotationCreateSchema is the UI schema for grafana-annotation-create
var AnnotationCreateSchema = resolver.NewSchemaBuilder("grafana-annotation-create").
	WithName("Create Annotation").
	WithCategory("action").
	WithIcon(iconGrafana).
	WithDescription("Create an annotation in Grafana").
	AddSection("Connection").
		AddExpressionField("baseURL", "Grafana URL",
			resolver.WithRequired(),
			resolver.WithPlaceholder("https://grafana.example.com"),
			resolver.WithHint("Grafana server URL (supports {{bindings.xxx}})"),
		).
		AddExpressionField("apiKey", "API Key",
			resolver.WithRequired(),
			resolver.WithPlaceholder("glsa_xxx"),
			resolver.WithHint("Grafana API key or service account token (supports {{bindings.xxx}})"),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Annotation").
		AddExpressionField("dashboardUID", "Dashboard UID",
			resolver.WithHint("Dashboard UID to annotate (optional for global annotations)"),
		).
		AddNumberField("panelId", "Panel ID",
			resolver.WithHint("Panel ID for panel-specific annotations (optional)"),
		).
		AddNumberField("time", "Time (ms)",
			resolver.WithHint("Unix timestamp in milliseconds (defaults to now)"),
		).
		AddNumberField("timeEnd", "End Time (ms)",
			resolver.WithHint("End timestamp for region annotations (optional)"),
		).
		AddTagsField("tags", "Tags",
			resolver.WithHint("Annotation tags"),
		).
		AddTextareaField("text", "Text",
			resolver.WithRequired(),
			resolver.WithRows(4),
			resolver.WithPlaceholder("Annotation text..."),
			resolver.WithHint("Annotation text content"),
		).
		EndSection().
	Build()

// AlertRuleListSchema is the UI schema for grafana-alert-rule-list
var AlertRuleListSchema = resolver.NewSchemaBuilder("grafana-alert-rule-list").
	WithName("List Alert Rules").
	WithCategory("action").
	WithIcon(iconGrafana).
	WithDescription("List all alert rules in Grafana").
	AddSection("Connection").
		AddExpressionField("baseURL", "Grafana URL",
			resolver.WithRequired(),
			resolver.WithPlaceholder("https://grafana.example.com"),
			resolver.WithHint("Grafana server URL (supports {{bindings.xxx}})"),
		).
		AddExpressionField("apiKey", "API Key",
			resolver.WithRequired(),
			resolver.WithPlaceholder("glsa_xxx"),
			resolver.WithHint("Grafana API key or service account token (supports {{bindings.xxx}})"),
			resolver.WithSensitive(),
		).
		EndSection().
	Build()

// AlertRuleCreateSchema is the UI schema for grafana-alert-rule-create
var AlertRuleCreateSchema = resolver.NewSchemaBuilder("grafana-alert-rule-create").
	WithName("Create Alert Rule").
	WithCategory("action").
	WithIcon(iconGrafana).
	WithDescription("Create a new alert rule in Grafana").
	AddSection("Connection").
		AddExpressionField("baseURL", "Grafana URL",
			resolver.WithRequired(),
			resolver.WithPlaceholder("https://grafana.example.com"),
			resolver.WithHint("Grafana server URL (supports {{bindings.xxx}})"),
		).
		AddExpressionField("apiKey", "API Key",
			resolver.WithRequired(),
			resolver.WithPlaceholder("glsa_xxx"),
			resolver.WithHint("Grafana API key or service account token (supports {{bindings.xxx}})"),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Alert Rule").
		AddExpressionField("title", "Title",
			resolver.WithRequired(),
			resolver.WithPlaceholder("My Alert Rule"),
			resolver.WithHint("Alert rule title"),
		).
		AddExpressionField("folderUID", "Folder UID",
			resolver.WithRequired(),
			resolver.WithHint("Folder UID containing the alert rule"),
		).
		AddExpressionField("ruleGroup", "Rule Group",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-alert-group"),
			resolver.WithHint("Rule group name"),
		).
		AddExpressionField("for", "For Duration",
			resolver.WithDefault("5m"),
			resolver.WithHint("How long the condition must be true before firing (e.g., 5m, 1h)"),
		).
		AddSelectField("noDataState", "No Data State",
			[]resolver.SelectOption{
				{Label: "NoData", Value: "NoData"},
				{Label: "Alerting", Value: "Alerting"},
				{Label: "OK", Value: "OK"},
				{Label: "KeepLast", Value: "KeepLast"},
			},
			resolver.WithDefault("NoData"),
			resolver.WithHint("State when no data is returned"),
		).
		AddSelectField("execErrState", "Execution Error State",
			[]resolver.SelectOption{
				{Label: "Error", Value: "Error"},
				{Label: "Alerting", Value: "Alerting"},
				{Label: "OK", Value: "OK"},
				{Label: "KeepLast", Value: "KeepLast"},
			},
			resolver.WithDefault("Error"),
			resolver.WithHint("State when query execution fails"),
		).
		AddExpressionField("condition", "Condition",
			resolver.WithRequired(),
			resolver.WithPlaceholder("A"),
			resolver.WithHint("RefID of the query to use as the alert condition"),
		).
		AddJSONField("data", "Queries",
			resolver.WithRequired(),
			resolver.WithHeight(250),
			resolver.WithHint(`Alert queries: [{"refId":"A","datasourceUid":"xxx","relativeTimeRange":{"from":600,"to":0},"model":{}}]`),
		).
		AddJSONField("labels", "Labels",
			resolver.WithHeight(100),
			resolver.WithHint("Alert labels: {\"severity\":\"critical\"}"),
		).
		AddJSONField("annotations", "Annotations",
			resolver.WithHeight(100),
			resolver.WithHint("Alert annotations: {\"summary\":\"High CPU usage\"}"),
		).
		EndSection().
	Build()

// ============================================================================
// EXECUTORS
// ============================================================================

// DashboardListExecutor handles grafana-dashboard-list
type DashboardListExecutor struct{}

func (e *DashboardListExecutor) Type() string {
	return "grafana-dashboard-list"
}

func (e *DashboardListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	baseURL := resolver.ResolveString(getString(config, "baseURL"))
	apiKey := resolver.ResolveString(getString(config, "apiKey"))

	client, err := getGrafanaClient(baseURL, apiKey)
	if err != nil {
		return nil, err
	}

	dashboards, err := client.ListDashboards(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list dashboards: %w", err)
	}

	// Convert to output format
	output := make([]map[string]interface{}, len(dashboards))
	for i, d := range dashboards {
		output[i] = map[string]interface{}{
			"id":          d.ID,
			"uid":         d.UID,
			"title":       d.Title,
			"uri":         d.URI,
			"url":         d.URL,
			"slug":        d.Slug,
			"type":        d.Type,
			"tags":        d.Tags,
			"isStarred":   d.IsStarred,
			"folderId":    d.FolderID,
			"folderUid":   d.FolderUID,
			"folderTitle": d.FolderTitle,
		}
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"dashboards": output,
			"count":      len(dashboards),
		},
	}, nil
}

// DashboardCreateExecutor handles grafana-dashboard-create
type DashboardCreateExecutor struct{}

func (e *DashboardCreateExecutor) Type() string {
	return "grafana-dashboard-create"
}

func (e *DashboardCreateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	baseURL := resolver.ResolveString(getString(config, "baseURL"))
	apiKey := resolver.ResolveString(getString(config, "apiKey"))

	client, err := getGrafanaClient(baseURL, apiKey)
	if err != nil {
		return nil, err
	}

	title := resolver.ResolveString(getString(config, "title"))
	if title == "" {
		return nil, fmt.Errorf("dashboard title is required")
	}

	// Build dashboard object
	dashboard := map[string]interface{}{
		"dashboard": map[string]interface{}{
			"id":        nil,
			"uid":       resolver.ResolveString(getString(config, "uid")),
			"title":     title,
			"tags":      getInterfaceSlice(config, "tags"),
			"timezone":  "browser",
			"editable":  getBool(config, "editable", true),
			"version":   0,
			"schemaVersion": 38,
		},
		"folderUid": resolver.ResolveString(getString(config, "folderUID")),
		"overwrite": false,
	}

	// Add panels if provided
	if panels := getInterfaceSlice(config, "panels"); panels != nil {
		dashboard["dashboard"].(map[string]interface{})["panels"] = panels
	}

	// Remove empty uid
	if dashboard["dashboard"].(map[string]interface{})["uid"] == "" {
		delete(dashboard["dashboard"].(map[string]interface{}), "uid")
	}

	result, err := client.CreateDashboard(ctx, dashboard)
	if err != nil {
		return nil, fmt.Errorf("failed to create dashboard: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"id":      result.ID,
			"uid":     result.UID,
			"url":     result.URL,
			"status":  result.Status,
			"version": result.Version,
			"slug":    result.Slug,
		},
	}, nil
}

// DashboardUpdateExecutor handles grafana-dashboard-update
type DashboardUpdateExecutor struct{}

func (e *DashboardUpdateExecutor) Type() string {
	return "grafana-dashboard-update"
}

func (e *DashboardUpdateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	baseURL := resolver.ResolveString(getString(config, "baseURL"))
	apiKey := resolver.ResolveString(getString(config, "apiKey"))

	client, err := getGrafanaClient(baseURL, apiKey)
	if err != nil {
		return nil, err
	}

	uid := resolver.ResolveString(getString(config, "uid"))
	if uid == "" {
		return nil, fmt.Errorf("dashboard UID is required")
	}

	// Get existing dashboard first
	existing, err := client.GetDashboard(ctx, uid)
	if err != nil {
		return nil, fmt.Errorf("failed to get existing dashboard: %w", err)
	}

	// Update fields
	dashboard := existing.Dashboard
	if title := resolver.ResolveString(getString(config, "title")); title != "" {
		dashboard["title"] = title
	}
	if tags := getInterfaceSlice(config, "tags"); tags != nil {
		dashboard["tags"] = tags
	}
	if panels := getInterfaceSlice(config, "panels"); panels != nil {
		dashboard["panels"] = panels
	}

	// Get version
	version := getInt(config, "version", 0)
	if version == 0 {
		if v, ok := dashboard["version"].(float64); ok {
			version = int(v) + 1
		} else {
			version = 1
		}
	}
	dashboard["version"] = version

	request := map[string]interface{}{
		"dashboard": dashboard,
		"overwrite": true,
	}

	result, err := client.UpdateDashboard(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("failed to update dashboard: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"id":      result.ID,
			"uid":     result.UID,
			"url":     result.URL,
			"status":  result.Status,
			"version": result.Version,
		},
	}, nil
}

// DashboardDeleteExecutor handles grafana-dashboard-delete
type DashboardDeleteExecutor struct{}

func (e *DashboardDeleteExecutor) Type() string {
	return "grafana-dashboard-delete"
}

func (e *DashboardDeleteExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	baseURL := resolver.ResolveString(getString(config, "baseURL"))
	apiKey := resolver.ResolveString(getString(config, "apiKey"))

	client, err := getGrafanaClient(baseURL, apiKey)
	if err != nil {
		return nil, err
	}

	uid := resolver.ResolveString(getString(config, "uid"))
	if uid == "" {
		return nil, fmt.Errorf("dashboard UID is required")
	}

	result, err := client.DeleteDashboard(ctx, uid)
	if err != nil {
		return nil, fmt.Errorf("failed to delete dashboard: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"id":    result.ID,
			"uid":   result.UID,
			"title": result.Title,
			"deleted": true,
		},
	}, nil
}

// DatasourceListExecutor handles grafana-datasource-list
type DatasourceListExecutor struct{}

func (e *DatasourceListExecutor) Type() string {
	return "grafana-datasource-list"
}

func (e *DatasourceListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	baseURL := resolver.ResolveString(getString(config, "baseURL"))
	apiKey := resolver.ResolveString(getString(config, "apiKey"))

	client, err := getGrafanaClient(baseURL, apiKey)
	if err != nil {
		return nil, err
	}

	datasources, err := client.ListDatasources(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list datasources: %w", err)
	}

	// Convert to output format
	output := make([]map[string]interface{}, len(datasources))
	for i, d := range datasources {
		output[i] = map[string]interface{}{
			"id":              d.ID,
			"uid":             d.UID,
			"name":            d.Name,
			"type":            d.Type,
			"url":             d.URL,
			"access":          d.Access,
			"database":        d.Database,
			"user":            d.User,
			"isDefault":       d.IsDefault,
			"basicAuth":       d.BasicAuth,
			"basicAuthUser":   d.BasicAuthUser,
			"withCredentials": d.WithCredentials,
			"readOnly":        d.ReadOnly,
		}
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"datasources": output,
			"count":       len(datasources),
		},
	}, nil
}

// DatasourceCreateExecutor handles grafana-datasource-create
type DatasourceCreateExecutor struct{}

func (e *DatasourceCreateExecutor) Type() string {
	return "grafana-datasource-create"
}

func (e *DatasourceCreateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	baseURL := resolver.ResolveString(getString(config, "baseURL"))
	apiKey := resolver.ResolveString(getString(config, "apiKey"))

	client, err := getGrafanaClient(baseURL, apiKey)
	if err != nil {
		return nil, err
	}

	name := resolver.ResolveString(getString(config, "name"))
	dsType := resolver.ResolveString(getString(config, "type"))
	url := resolver.ResolveString(getString(config, "url"))

	if name == "" {
		return nil, fmt.Errorf("datasource name is required")
	}
	if dsType == "" {
		return nil, fmt.Errorf("datasource type is required")
	}
	if url == "" {
		return nil, fmt.Errorf("datasource URL is required")
	}

	datasource := DatasourceCreate{
		Name:            name,
		Type:            dsType,
		URL:             url,
		Access:          getString(config, "access"),
		Database:        resolver.ResolveString(getString(config, "database")),
		User:            resolver.ResolveString(getString(config, "user")),
		IsDefault:       getBool(config, "isDefault", false),
		BasicAuth:       getBool(config, "basicAuth", false),
		BasicAuthUser:   resolver.ResolveString(getString(config, "basicAuthUser")),
		WithCredentials: getBool(config, "withCredentials", false),
		UID:             resolver.ResolveString(getString(config, "uid")),
		ReadOnly:        getBool(config, "readOnly", false),
	}

	// Add jsonData if provided
	if jsonData := getMap(config, "jsonData"); jsonData != nil {
		datasource.JSONData = jsonData
	}

	// Add secureJsonData if provided
	if secureJsonData := getMap(config, "secureJsonData"); secureJsonData != nil {
		datasource.SecureJSONData = secureJsonData
	}

	// Add basicAuthPassword to secureJsonData if provided
	if basicAuthPassword := resolver.ResolveString(getString(config, "basicAuthPassword")); basicAuthPassword != "" {
		if datasource.SecureJSONData == nil {
			datasource.SecureJSONData = make(map[string]interface{})
		}
		datasource.SecureJSONData["basicAuthPassword"] = basicAuthPassword
	}

	result, err := client.CreateDatasource(ctx, datasource)
	if err != nil {
		return nil, fmt.Errorf("failed to create datasource: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"id":      result.ID,
			"uid":     result.UID,
			"name":    result.Name,
			"message": result.Message,
		},
	}, nil
}

// AnnotationCreateExecutor handles grafana-annotation-create
type AnnotationCreateExecutor struct{}

func (e *AnnotationCreateExecutor) Type() string {
	return "grafana-annotation-create"
}

func (e *AnnotationCreateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	baseURL := resolver.ResolveString(getString(config, "baseURL"))
	apiKey := resolver.ResolveString(getString(config, "apiKey"))

	client, err := getGrafanaClient(baseURL, apiKey)
	if err != nil {
		return nil, err
	}

	text := resolver.ResolveString(getString(config, "text"))
	if text == "" {
		return nil, fmt.Errorf("annotation text is required")
	}

	annotation := AnnotationCreate{
		DashboardUID: resolver.ResolveString(getString(config, "dashboardUID")),
		PanelID:      int64(getInt(config, "panelId", 0)),
		Time:         int64(getInt(config, "time", 0)),
		TimeEnd:      int64(getInt(config, "timeEnd", 0)),
		Tags:         getStringSliceFromConfig(config, "tags"),
		Text:         text,
	}

	// Set default time if not provided
	if annotation.Time == 0 {
		annotation.Time = time.Now().UnixMilli()
	}

	result, err := client.CreateAnnotation(ctx, annotation)
	if err != nil {
		return nil, fmt.Errorf("failed to create annotation: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"id":      result.ID,
			"message": result.Message,
		},
	}, nil
}

// AlertRuleListExecutor handles grafana-alert-rule-list
type AlertRuleListExecutor struct{}

func (e *AlertRuleListExecutor) Type() string {
	return "grafana-alert-rule-list"
}

func (e *AlertRuleListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	baseURL := resolver.ResolveString(getString(config, "baseURL"))
	apiKey := resolver.ResolveString(getString(config, "apiKey"))

	client, err := getGrafanaClient(baseURL, apiKey)
	if err != nil {
		return nil, err
	}

	rules, err := client.ListAlertRules(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list alert rules: %w", err)
	}

	// Convert to output format
	output := make([]map[string]interface{}, len(rules))
	for i, r := range rules {
		output[i] = map[string]interface{}{
			"id":           r.ID,
			"uid":          r.UID,
			"title":        r.Title,
			"folderUID":    r.FolderUID,
			"ruleGroup":    r.RuleGroup,
			"orgId":        r.OrgID,
			"execErrState": r.ExecErrState,
			"noDataState":  r.NoDataState,
			"for":          r.For,
			"condition":    r.Condition,
			"labels":       r.Labels,
			"annotations":  r.Annotations,
			"isPaused":     r.IsPaused,
			"updated":      r.Updated.Format(time.RFC3339),
		}
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"alertRules": output,
			"count":      len(rules),
		},
	}, nil
}

// AlertRuleCreateExecutor handles grafana-alert-rule-create
type AlertRuleCreateExecutor struct{}

func (e *AlertRuleCreateExecutor) Type() string {
	return "grafana-alert-rule-create"
}

func (e *AlertRuleCreateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	baseURL := resolver.ResolveString(getString(config, "baseURL"))
	apiKey := resolver.ResolveString(getString(config, "apiKey"))

	client, err := getGrafanaClient(baseURL, apiKey)
	if err != nil {
		return nil, err
	}

	title := resolver.ResolveString(getString(config, "title"))
	folderUID := resolver.ResolveString(getString(config, "folderUID"))
	ruleGroup := resolver.ResolveString(getString(config, "ruleGroup"))
	condition := resolver.ResolveString(getString(config, "condition"))

	if title == "" {
		return nil, fmt.Errorf("alert rule title is required")
	}
	if folderUID == "" {
		return nil, fmt.Errorf("folder UID is required")
	}
	if ruleGroup == "" {
		return nil, fmt.Errorf("rule group is required")
	}
	if condition == "" {
		return nil, fmt.Errorf("condition is required")
	}

	// Parse queries
	dataRaw := getInterfaceSlice(config, "data")
	if dataRaw == nil || len(dataRaw) == 0 {
		return nil, fmt.Errorf("at least one query is required")
	}

	// Convert queries
	var queries []AlertQuery
	for _, q := range dataRaw {
		qMap, ok := q.(map[string]interface{})
		if !ok {
			continue
		}

		query := AlertQuery{
			RefID:         getString(qMap, "refId"),
			QueryType:     getString(qMap, "queryType"),
			DatasourceUID: getString(qMap, "datasourceUid"),
		}

		// Parse time range
		if rt, ok := qMap["relativeTimeRange"].(map[string]interface{}); ok {
			query.RelativeTimeRange = AlertTimeRange{
				From: getInt(rt, "from", 600),
				To:   getInt(rt, "to", 0),
			}
		} else {
			query.RelativeTimeRange = AlertTimeRange{From: 600, To: 0}
		}

		// Parse model
		if model, ok := qMap["model"].(map[string]interface{}); ok {
			query.Model = model
		}

		queries = append(queries, query)
	}

	if len(queries) == 0 {
		return nil, fmt.Errorf("no valid queries provided")
	}

	rule := AlertRuleCreate{
		Title:        title,
		FolderUID:    folderUID,
		RuleGroup:    ruleGroup,
		For:          resolver.ResolveString(getString(config, "for")),
		NoDataState:  resolver.ResolveString(getString(config, "noDataState")),
		ExecErrState: resolver.ResolveString(getString(config, "execErrState")),
		Condition:    condition,
		Data:         queries,
		IsPaused:     getBool(config, "isPaused", false),
	}

	// Parse labels
	if labelsRaw := getMap(config, "labels"); labelsRaw != nil {
		rule.Labels = make(map[string]string)
		for k, v := range labelsRaw {
			if s, ok := v.(string); ok {
				rule.Labels[k] = s
			}
		}
	}

	// Parse annotations
	if annotationsRaw := getMap(config, "annotations"); annotationsRaw != nil {
		rule.Annotations = make(map[string]string)
		for k, v := range annotationsRaw {
			if s, ok := v.(string); ok {
				rule.Annotations[k] = s
			}
		}
	}

	result, err := client.CreateAlertRule(ctx, rule)
	if err != nil {
		return nil, fmt.Errorf("failed to create alert rule: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"id":    result.ID,
			"uid":   result.UID,
			"title": result.Title,
		},
	}, nil
}

// Helper function to get string slice from config
func getStringSliceFromConfig(config map[string]interface{}, key string) []string {
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

// ============================================================================
// MAIN
// ============================================================================

func main() {
	// Get port from env or use default
	port := os.Getenv("SKILL_PORT")
	if port == "" {
		port = "50124"
	}

	// Create skill server
	server := grpc.NewSkillServer("skill-grafana", "1.0.0")

	// Register all executors with their schemas
	server.RegisterExecutorWithSchema("grafana-dashboard-list", &DashboardListExecutor{}, DashboardListSchema)
	server.RegisterExecutorWithSchema("grafana-dashboard-create", &DashboardCreateExecutor{}, DashboardCreateSchema)
	server.RegisterExecutorWithSchema("grafana-dashboard-update", &DashboardUpdateExecutor{}, DashboardUpdateSchema)
	server.RegisterExecutorWithSchema("grafana-dashboard-delete", &DashboardDeleteExecutor{}, DashboardDeleteSchema)
	server.RegisterExecutorWithSchema("grafana-datasource-list", &DatasourceListExecutor{}, DatasourceListSchema)
	server.RegisterExecutorWithSchema("grafana-datasource-create", &DatasourceCreateExecutor{}, DatasourceCreateSchema)
	server.RegisterExecutorWithSchema("grafana-annotation-create", &AnnotationCreateExecutor{}, AnnotationCreateSchema)
	server.RegisterExecutorWithSchema("grafana-alert-rule-list", &AlertRuleListExecutor{}, AlertRuleListSchema)
	server.RegisterExecutorWithSchema("grafana-alert-rule-create", &AlertRuleCreateExecutor{}, AlertRuleCreateSchema)

	fmt.Printf("Starting skill-grafana gRPC server on port %s\n", port)
	if err := server.Serve(port); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to serve: %v\n", err)
		os.Exit(1)
	}
}
