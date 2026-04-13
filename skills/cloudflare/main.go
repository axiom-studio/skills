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
	iconCloudflare = "cloud"
	apiBaseURL     = "https://api.cloudflare.com/client/v4"
)

// CloudflareConfig holds Cloudflare API configuration
type CloudflareConfig struct {
	APIToken string `json:"apiToken"`
	Email    string `json:"email"`
	APIKey   string `json:"apiKey"`
}

// parseCloudflareConfig extracts Cloudflare configuration from config map
func parseCloudflareConfig(config map[string]interface{}) CloudflareConfig {
	return CloudflareConfig{
		APIToken: getString(config, "apiToken"),
		Email:    getString(config, "email"),
		APIKey:   getString(config, "apiKey"),
	}
}

// cfHTTPClient caches HTTP clients per API token
var (
	cfClients  = make(map[string]*http.Client)
	cfClientMux sync.RWMutex
)

// getCFClient returns an HTTP client configured for Cloudflare API
func getCFClient(cfg CloudflareConfig) *http.Client {
	cacheKey := cfg.APIToken
	if cacheKey == "" {
		cacheKey = cfg.APIKey
	}

	cfClientMux.RLock()
	client, ok := cfClients[cacheKey]
	cfClientMux.RUnlock()

	if ok {
		return client
	}

	cfClientMux.Lock()
	defer cfClientMux.Unlock()

	client = &http.Client{
		Timeout: 60 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 10,
			IdleConnTimeout:     90 * time.Second,
		},
	}
	cfClients[cacheKey] = client
	return client
}

// cfRequest makes an authenticated request to Cloudflare API
func cfRequest(ctx context.Context, cfg CloudflareConfig, method, endpoint string, body interface{}) ([]byte, error) {
	client := getCFClient(cfg)

	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
		reqBody = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, apiBaseURL+endpoint, reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set authentication headers
	if cfg.APIToken != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.APIToken)
	} else if cfg.APIKey != "" && cfg.Email != "" {
		req.Header.Set("X-Auth-Email", cfg.Email)
		req.Header.Set("X-Auth-Key", cfg.APIKey)
	} else {
		return nil, fmt.Errorf("either apiToken or (email + apiKey) must be provided")
	}

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

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var errResp CloudflareErrorResponse
		if err := json.Unmarshal(respBody, &errResp); err == nil && len(errResp.Errors) > 0 {
			return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, errResp.Errors[0].Message)
		}
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(respBody))
	}

	return respBody, nil
}

// CloudflareErrorResponse represents Cloudflare error response
type CloudflareErrorResponse struct {
	Success  bool `json:"success"`
	Errors   []struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"errors"`
}

// CloudflareSuccessResponse represents Cloudflare success response
type CloudflareSuccessResponse struct {
	Success  bool            `json:"success"`
	Result   json.RawMessage `json:"result"`
	Messages []string        `json:"messages"`
	Errors   []struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"errors"`
}

// ============================================================================
// DNS RECORD TYPES
// ============================================================================

// DNSRecord represents a DNS record
type DNSRecord struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	Name      string `json:"name"`
	Content   string `json:"content"`
	Proxiable bool   `json:"proxiable"`
	Proxied   bool   `json:"proxied"`
	TTL       int    `json:"ttl"`
	Priority  *int   `json:"priority,omitempty"`
	Comment   string `json:"comment"`
	Tag       string `json:"tag"`
	CreatedOn string `json:"created_on"`
	ModifiedOn string `json:"modified_on"`
	ZoneID    string `json:"zone_id"`
	ZoneName  string `json:"zone_name"`
}

// DNSListResponse represents DNS list response
type DNSListResponse struct {
	Success bool        `json:"success"`
	Result  []DNSRecord `json:"result"`
	ResultInfo struct {
		Page       int `json:"page"`
		PerPage    int `json:"per_page"`
		TotalPages int `json:"total_pages"`
		Count      int `json:"count"`
		Total      int `json:"total"`
	} `json:"result_info"`
}

// Zone represents a Cloudflare zone
type Zone struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	Paused    bool   `json:"paused"`
	Type      string `json:"type"`
	DevelopMode bool `json:"development_mode"`
	NameServers []string `json:"name_servers"`
	OriginalNameServers []string `json:"original_name_servers"`
	OriginalRegistrar   string `json:"original_registrar"`
	OriginalDNCHost     string `json:"original_dnshost"`
	CreatedOn string `json:"created_on"`
	ModifiedOn string `json:"modified_on"`
	ActivatedOn string `json:"activated_on"`
	Meta        struct {
		Step               int  `json:"step"`
		WildcardProxiable  bool `json:"wildcard_proxiable"`
		PhishingDetected   bool `json:"phishing_detected"`
		CustomCertificate  bool `json:"custom_certificate"`
		MultipleRailgunsAllowed bool `json:"multiple_railguns_allowed"`
	} `json:"meta"`
	Owner   Owner  `json:"owner"`
	Account Account `json:"account"`
	Permissions []string `json:"permissions"`
	Plan      Plan   `json:"plan"`
}

// Owner represents zone owner
type Owner struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	Email     string `json:"email,omitempty"`
}

// Account represents Cloudflare account
type Account struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Plan represents zone plan
type Plan struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Price          int    `json:"price"`
	Currency       string `json:"currency"`
	Frequency      string `json:"frequency"`
	LegacyID       string `json:"legacy_id"`
	IsSubscribed   bool   `json:"is_subscribed"`
	CanSubscribe   bool   `json:"can_subscribe"`
	LegacyDiscount bool   `json:"legacy_discount"`
	ExternallyPaid bool   `json:"externally_paid"`
}

// ZoneListResponse represents zone list response
type ZoneListResponse struct {
	Success bool   `json:"success"`
	Result  []Zone `json:"result"`
	ResultInfo struct {
		Page       int `json:"page"`
		PerPage    int `json:"per_page"`
		TotalPages int `json:"total_pages"`
		Count      int `json:"count"`
		Total      int `json:"total"`
	} `json:"result_info"`
}

// ============================================================================
// WORKER TYPES
// ============================================================================

// WorkerScript represents a Cloudflare Worker script
type WorkerScript struct {
	ID             string            `json:"id"`
	CreatedOn      string            `json:"created_on"`
	ModifiedOn     string            `json:"modified_on"`
	Tag            string            `json:"tag"`
	TagProfile     string            `json:"tag_profile"`
	UsageModel     string            `json:"usage_model"`
	Environment    string            `json:"environment"`
	Script         string            `json:"script"`
	Handlers       []string          `json:"handlers"`
	LastDeployedFrom string          `json:"last_deployed_from"`
	DeploymentID   string            `json:"deployment_id"`
	Logpush        bool              `json:"logpush"`
	CompatibilityDate string         `json:"compatibility_date"`
	CompatibilityFlags []string      `json:"compatibility_flags"`
	Bindings       []WorkerBinding   `json:"bindings"`
}

// WorkerBinding represents a Worker binding
type WorkerBinding struct {
	Type         string `json:"type"`
	Name         string `json:"name"`
	Text         string `json:"text,omitempty"`
	Json         interface{} `json:"json,omitempty"`
	Bucket       string `json:"bucket,omitempty"`
	Key          string `json:"key,omitempty"`
	TableName    string `json:"table_name,omitempty"`
	DatabaseID   string `json:"database_id,omitempty"`
	Service      string `json:"service,omitempty"`
	Environment  string `json:"environment,omitempty"`
	Script       string `json:"script,omitempty"`
	Module       bool   `json:"module,omitempty"`
}

// WorkerListResponse represents worker list response
type WorkerListResponse struct {
	Success bool           `json:"success"`
	Result  []WorkerScript `json:"result"`
}

// WorkerUploadRequest represents worker upload request
type WorkerUploadRequest struct {
	Script             string                 `json:"script"`
	Handlers           []string               `json:"handlers,omitempty"`
	CompatibilityDate  string                 `json:"compatibility_date,omitempty"`
	CompatibilityFlags []string               `json:"compatibility_flags,omitempty"`
	Bindings           []WorkerBinding        `json:"bindings,omitempty"`
	Migrations         *WorkerMigrations      `json:"migrations,omitempty"`
	TailConsumers      []interface{}          `json:"tail_consumers,omitempty"`
	Logpush            *bool                  `json:"logpush,omitempty"`
	Placement          *WorkerPlacement       `json:"placement,omitempty"`
	KeepVars           bool                   `json:"keep_vars,omitempty"`
	KeepSecrets        bool                   `json:"keep_secrets,omitempty"`
}

// WorkerMigrations represents worker migrations
type WorkerMigrations struct {
	NewTag           string   `json:"new_tag"`
	NewSqliteDatabases []string `json:"new_sqlite_databases,omitempty"`
	DeletedDurableObjects []string `json:"deleted_durable_objects,omitempty"`
	DeletedClasses   []string `json:"deleted_classes,omitempty"`
	DeletedHandlers  []string `json:"deleted_handlers,omitempty"`
	DeletedR2Buckets []string `json:"deleted_r2_buckets,omitempty"`
	DeletedKVNamespaces []string `json:"deleted_kv_namespaces,omitempty"`
	DeletedHyperdrive []string `json:"deleted_hyperdrive,omitempty"`
	DeletedQueues    []string `json:"deleted_queues,omitempty"`
	DeletedD1Databases []string `json:"deleted_d1_databases,omitempty"`
	DeletedVectorizeIndexes []string `json:"deleted_vectorize_indexes,omitempty"`
	DeletedImagesImages []string `json:"deleted_images_images,omitempty"`
	DeletedTextToSpeechModels []string `json:"deleted_text_to_speech_models,omitempty"`
	DeletedSpeechToTextModels []string `json:"deleted_speech_to_text_models,omitempty"`
	DeletedAiGateways []string `json:"deleted_ai_gateways,omitempty"`
	DeletedVersionMetadata []string `json:"deleted_version_metadata,omitempty"`
	DeletedAssets     []string `json:"deleted_assets,omitempty"`
	DeletedAssetsConfig *bool   `json:"deleted_assets_config,omitempty"`
	DeletedBrowserRendering *bool `json:"deleted_browser_rendering,omitempty"`
	DeletedAnalyticsEngineDatasets []string `json:"deleted_analytics_engine_datasets,omitempty"`
	DeletedDispatchNamespaces []string `json:"deleted_dispatch_namespaces,omitempty"`
	DeletedLifecycles []string `json:"deleted_lifecycles,omitempty"`
	DeletedMTLSCertificates []string `json:"deleted_mtls_certificates,omitempty"`
	DeletedPipelines []string `json:"deleted_pipelines,omitempty"`
	DeletedAssetsAndConfig *bool `json:"deleted_assets_and_config,omitempty"`
}

// WorkerPlacement represents worker placement
type WorkerPlacement struct {
	Mode string `json:"mode"`
}

// ============================================================================
// R2 TYPES
// ============================================================================

// R2Bucket represents an R2 bucket
type R2Bucket struct {
	Name      string `json:"name"`
	CreatedAt string `json:"created_at"`
	Location  string `json:"location"`
}

// R2ListResponse represents R2 bucket list response
type R2ListResponse struct {
	Success bool       `json:"success"`
	Result  []R2Bucket `json:"result"`
}

// R2Object represents an R2 object
type R2Object struct {
	Key          string `json:"key"`
	LastModified string `json:"last_modified"`
	ETag         string `json:"etag"`
	Size         int64  `json:"size"`
	HTTPMetadata struct {
		ContentType string `json:"content_type"`
	} `json:"http_metadata"`
	CustomMetadata map[string]string `json:"custom_metadata"`
	Range          string `json:"range"`
	Checksums      struct {
		MD5  string `json:"md5"`
		SHA1 string `json:"sha1"`
		SHA256 string `json:"sha256"`
		SHA384 string `json:"sha384"`
		SHA512 string `json:"sha512"`
		CRC32 string `json:"crc32"`
	} `json:"checksums"`
}

// R2ObjectsListResponse represents R2 objects list response
type R2ObjectsListResponse struct {
	Success bool       `json:"success"`
	Result  R2ObjectsResult `json:"result"`
}

// R2ObjectsResult represents R2 objects result
type R2ObjectsResult struct {
	Objects    []R2Object `json:"objects"`
	Delimiters []string   `json:"delimiters"`
}

// ============================================================================
// D1 TYPES
// ============================================================================

// D1Database represents a D1 database
type D1Database struct {
	UUID          string `json:"uuid"`
	Name          string `json:"name"`
	Version       string `json:"version"`
	CreatedAt     string `json:"created_at"`
	NumTables     int    `json:"num_tables"`
	FileSize      int64  `json:"file_size"`
	FileSizeHuman string `json:"file_size_human"`
}

// D1ListResponse represents D1 database list response
type D1ListResponse struct {
	Success bool       `json:"success"`
	Result  []D1Database `json:"result"`
}

// D1QueryResult represents D1 query result
type D1QueryResult struct {
	Success bool         `json:"success"`
	Result  D1QueryData  `json:"result"`
	Errors  []struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"errors"`
	Messages []string `json:"messages"`
}

// D1QueryData represents D1 query data
type D1QueryData struct {
	Results    []map[string]interface{} `json:"results"`
	Meta       D1QueryMeta              `json:"meta"`
	ServedBy   string                   `json:"served_by"`
	Duration   float64                  `json:"duration"`
	Changes    int                      `json:"changes"`
	LastRowID  int64                    `json:"last_row_id"`
	ChangedDB  bool                     `json:"changed_db"`
	SizeAfter  int64                    `json:"size_after"`
}

// D1QueryMeta represents D1 query metadata
type D1QueryMeta struct {
	Duration float64 `json:"duration"`
	RowsRead int64   `json:"rows_read"`
	RowsWritten int64 `json:"rows_written"`
}

// ============================================================================
// WAF TYPES
// ============================================================================

// WAFRule represents a WAF custom rule
type WAFRule struct {
	ID            string `json:"id"`
	Priority      int    `json:"priority"`
	Action        string `json:"action"`
	Enabled       bool   `json:"enabled"`
	Description   string `json:"description"`
	Ruleset       string `json:"ruleset"`
	RulesetID     string `json:"ruleset_id"`
	Expression    string `json:"expression"`
	Ref           string `json:"ref"`
	LastModified  string `json:"last_modified"`
}

// WAFRuleListResponse represents WAF rule list response
type WAFRuleListResponse struct {
	Success bool      `json:"success"`
	Result  []WAFRule `json:"result"`
	ResultInfo struct {
		Page       int `json:"page"`
		PerPage    int `json:"per_page"`
		TotalPages int `json:"total_pages"`
		Count      int `json:"count"`
		Total      int `json:"total"`
	} `json:"result_info"`
}

// WAFRuleCreateRequest represents WAF rule create request
type WAFRuleCreateRequest struct {
	ID          string `json:"id,omitempty"`
	Priority    int    `json:"priority"`
	Action      string `json:"action"`
	Enabled     bool   `json:"enabled"`
	Description string `json:"description,omitempty"`
	Expression  string `json:"expression"`
	Ref         string `json:"ref,omitempty"`
}

// WAFRuleCreateResponse represents WAF rule create response
type WAFRuleCreateResponse struct {
	Success bool      `json:"success"`
	Result  []WAFRule `json:"result"`
	Errors  []struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"errors"`
	Messages []string `json:"messages"`
}

// ============================================================================
// HELPER FUNCTIONS
// ============================================================================

// getString helper to get string from config
func getString(config map[string]interface{}, key string) string {
	if v, ok := config[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// getInt helper to get int from config
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

// getBool helper to get bool from config
func getBool(config map[string]interface{}, key string, def bool) bool {
	if v, ok := config[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return def
}

// getMap helper to get map from config
func getMap(config map[string]interface{}, key string) map[string]interface{} {
	if v, ok := config[key]; ok {
		if m, ok := v.(map[string]interface{}); ok {
			return m
		}
	}
	return nil
}

// getStringSlice helper to get string slice from config
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

// ============================================================================
// SCHEMAS
// ============================================================================

// CFZoneListSchema is the UI schema for cf-zone-list
var CFZoneListSchema = resolver.NewSchemaBuilder("cf-zone-list").
	WithName("List Cloudflare Zones").
	WithCategory("action").
	WithIcon(iconCloudflare).
	WithDescription("List all zones (domains) in your Cloudflare account").
	AddSection("Cloudflare Authentication").
		AddExpressionField("apiToken", "API Token",
			resolver.WithSensitive(),
			resolver.WithPlaceholder("Your Cloudflare API Token"),
			resolver.WithHint("Cloudflare API Token with Zone:Read permission"),
		).
		AddExpressionField("email", "Email",
			resolver.WithPlaceholder("your@email.com"),
			resolver.WithHint("Cloudflare account email (required if using API Key instead of Token)"),
		).
		AddExpressionField("apiKey", "API Key",
			resolver.WithSensitive(),
			resolver.WithPlaceholder("Your Cloudflare API Key"),
			resolver.WithHint("Cloudflare API Key (alternative to API Token)"),
		).
		EndSection().
	AddSection("Filters").
		AddExpressionField("name", "Zone Name",
			resolver.WithPlaceholder("example.com"),
			resolver.WithHint("Filter zones by name (optional)"),
		).
		AddExpressionField("status", "Status",
			resolver.WithPlaceholder("active, pending, initializing, moved, deleted"),
			resolver.WithHint("Filter by zone status"),
		).
		EndSection().
	Build()

// CFDNSListSchema is the UI schema for cf-dns-list
var CFDNSListSchema = resolver.NewSchemaBuilder("cf-dns-list").
	WithName("List DNS Records").
	WithCategory("action").
	WithIcon(iconCloudflare).
	WithDescription("List DNS records for a Cloudflare zone").
	AddSection("Cloudflare Authentication").
		AddExpressionField("apiToken", "API Token",
			resolver.WithSensitive(),
			resolver.WithPlaceholder("Your Cloudflare API Token"),
			resolver.WithHint("Cloudflare API Token with Zone:Read and DNS:Read permissions"),
		).
		AddExpressionField("email", "Email",
			resolver.WithPlaceholder("your@email.com"),
		).
		AddExpressionField("apiKey", "API Key",
			resolver.WithSensitive(),
			resolver.WithPlaceholder("Your Cloudflare API Key"),
		).
		EndSection().
	AddSection("Zone").
		AddExpressionField("zoneId", "Zone ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("abc123..."),
			resolver.WithHint("Cloudflare Zone ID"),
		).
		EndSection().
	AddSection("Filters").
		AddExpressionField("type", "Record Type",
			resolver.WithPlaceholder("A, AAAA, CNAME, MX, TXT, etc."),
			resolver.WithHint("Filter by DNS record type"),
		).
		AddExpressionField("name", "Record Name",
			resolver.WithPlaceholder("www.example.com"),
			resolver.WithHint("Filter by record name"),
		).
		AddExpressionField("content", "Content",
			resolver.WithPlaceholder("192.0.2.1"),
			resolver.WithHint("Filter by record content"),
		).
		EndSection().
	AddSection("Options").
		AddNumberField("limit", "Limit",
			resolver.WithDefault(100),
			resolver.WithMinMax(1, 5000),
			resolver.WithHint("Maximum number of records to return"),
		).
		EndSection().
	Build()

// CFDNSCreateSchema is the UI schema for cf-dns-create
var CFDNSCreateSchema = resolver.NewSchemaBuilder("cf-dns-create").
	WithName("Create DNS Record").
	WithCategory("action").
	WithIcon(iconCloudflare).
	WithDescription("Create a new DNS record in Cloudflare").
	AddSection("Cloudflare Authentication").
		AddExpressionField("apiToken", "API Token",
			resolver.WithSensitive(),
			resolver.WithPlaceholder("Your Cloudflare API Token"),
			resolver.WithHint("Cloudflare API Token with Zone:Edit and DNS:Edit permissions"),
		).
		AddExpressionField("email", "Email",
			resolver.WithPlaceholder("your@email.com"),
		).
		AddExpressionField("apiKey", "API Key",
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Zone").
		AddExpressionField("zoneId", "Zone ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("abc123..."),
			resolver.WithHint("Cloudflare Zone ID"),
		).
		EndSection().
	AddSection("DNS Record").
		AddSelectField("type", "Record Type",
			[]resolver.SelectOption{
				{Label: "A", Value: "A"},
				{Label: "AAAA", Value: "AAAA"},
				{Label: "CNAME", Value: "CNAME"},
				{Label: "MX", Value: "MX"},
				{Label: "TXT", Value: "TXT"},
				{Label: "NS", Value: "NS"},
				{Label: "SRV", Value: "SRV"},
				{Label: "PTR", Value: "PTR"},
				{Label: "CAA", Value: "CAA"},
			},
			resolver.WithRequired(),
			resolver.WithHint("DNS record type"),
		).
		AddExpressionField("name", "Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("www.example.com"),
			resolver.WithHint("DNS record name (FQDN)"),
		).
		AddExpressionField("content", "Content",
			resolver.WithRequired(),
			resolver.WithPlaceholder("192.0.2.1"),
			resolver.WithHint("DNS record content (IP, hostname, etc.)"),
		).
		AddNumberField("ttl", "TTL",
			resolver.WithDefault(1),
			resolver.WithHint("Time to live in seconds (1 = automatic)"),
		).
		AddNumberField("priority", "Priority",
			resolver.WithHint("Priority for MX and SRV records"),
		).
		EndSection().
	AddSection("Options").
		AddToggleField("proxied", "Proxy Through Cloudflare",
			resolver.WithDefault(false),
			resolver.WithHint("Enable Cloudflare proxy (orange cloud)"),
		).
		AddExpressionField("comment", "Comment",
			resolver.WithHint("Optional comment for the record"),
		).
		EndSection().
	Build()

// CFDNSUpdateSchema is the UI schema for cf-dns-update
var CFDNSUpdateSchema = resolver.NewSchemaBuilder("cf-dns-update").
	WithName("Update DNS Record").
	WithCategory("action").
	WithIcon(iconCloudflare).
	WithDescription("Update an existing DNS record in Cloudflare").
	AddSection("Cloudflare Authentication").
		AddExpressionField("apiToken", "API Token",
			resolver.WithSensitive(),
			resolver.WithPlaceholder("Your Cloudflare API Token"),
			resolver.WithHint("Cloudflare API Token with Zone:Edit and DNS:Edit permissions"),
		).
		AddExpressionField("email", "Email",
			resolver.WithPlaceholder("your@email.com"),
		).
		AddExpressionField("apiKey", "API Key",
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Zone").
		AddExpressionField("zoneId", "Zone ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("abc123..."),
			resolver.WithHint("Cloudflare Zone ID"),
		).
		EndSection().
	AddSection("DNS Record").
		AddExpressionField("recordId", "Record ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("xyz789..."),
			resolver.WithHint("DNS Record ID to update"),
		).
		AddExpressionField("name", "Name",
			resolver.WithPlaceholder("www.example.com"),
			resolver.WithHint("New DNS record name (leave unchanged if empty)"),
		).
		AddExpressionField("content", "Content",
			resolver.WithPlaceholder("192.0.2.1"),
			resolver.WithHint("New DNS record content (leave unchanged if empty)"),
		).
		AddNumberField("ttl", "TTL",
			resolver.WithHint("New TTL in seconds (1 = automatic, leave empty to keep current)"),
		).
		AddNumberField("priority", "Priority",
			resolver.WithHint("New priority for MX and SRV records"),
		).
		EndSection().
	AddSection("Options").
		AddToggleField("proxied", "Proxy Through Cloudflare",
			resolver.WithHint("Enable/disable Cloudflare proxy"),
		).
		EndSection().
	Build()

// CFDNSDeleteSchema is the UI schema for cf-dns-delete
var CFDNSDeleteSchema = resolver.NewSchemaBuilder("cf-dns-delete").
	WithName("Delete DNS Record").
	WithCategory("action").
	WithIcon(iconCloudflare).
	WithDescription("Delete a DNS record from Cloudflare").
	AddSection("Cloudflare Authentication").
		AddExpressionField("apiToken", "API Token",
			resolver.WithSensitive(),
			resolver.WithPlaceholder("Your Cloudflare API Token"),
			resolver.WithHint("Cloudflare API Token with Zone:Edit and DNS:Edit permissions"),
		).
		AddExpressionField("email", "Email",
			resolver.WithPlaceholder("your@email.com"),
		).
		AddExpressionField("apiKey", "API Key",
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Zone").
		AddExpressionField("zoneId", "Zone ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("abc123..."),
			resolver.WithHint("Cloudflare Zone ID"),
		).
		EndSection().
	AddSection("DNS Record").
		AddExpressionField("recordId", "Record ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("xyz789..."),
			resolver.WithHint("DNS Record ID to delete"),
		).
		EndSection().
	Build()

// CFWorkerListSchema is the UI schema for cf-worker-list
var CFWorkerListSchema = resolver.NewSchemaBuilder("cf-worker-list").
	WithName("List Cloudflare Workers").
	WithCategory("action").
	WithIcon(iconCloudflare).
	WithDescription("List all Workers in your Cloudflare account").
	AddSection("Cloudflare Authentication").
		AddExpressionField("apiToken", "API Token",
			resolver.WithSensitive(),
			resolver.WithPlaceholder("Your Cloudflare API Token"),
			resolver.WithHint("Cloudflare API Token with Workers:Read permission"),
		).
		AddExpressionField("email", "Email",
			resolver.WithPlaceholder("your@email.com"),
		).
		AddExpressionField("apiKey", "API Key",
			resolver.WithSensitive(),
		).
		EndSection().
	Build()

// CFWorkerDeploySchema is the UI schema for cf-worker-deploy
var CFWorkerDeploySchema = resolver.NewSchemaBuilder("cf-worker-deploy").
	WithName("Deploy Cloudflare Worker").
	WithCategory("action").
	WithIcon(iconCloudflare).
	WithDescription("Deploy a Worker script to Cloudflare").
	AddSection("Cloudflare Authentication").
		AddExpressionField("apiToken", "API Token",
			resolver.WithSensitive(),
			resolver.WithPlaceholder("Your Cloudflare API Token"),
			resolver.WithHint("Cloudflare API Token with Workers:Write permission"),
		).
		AddExpressionField("email", "Email",
			resolver.WithPlaceholder("your@email.com"),
		).
		AddExpressionField("apiKey", "API Key",
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Worker").
		AddExpressionField("name", "Worker Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-worker"),
			resolver.WithHint("Name for the Worker"),
		).
		AddCodeField("script", "Script", "javascript",
			resolver.WithRequired(),
			resolver.WithHeight(300),
			resolver.WithHint("Worker JavaScript code"),
		).
		EndSection().
	AddSection("Options").
		AddExpressionField("compatibilityDate", "Compatibility Date",
			resolver.WithPlaceholder("2024-01-01"),
			resolver.WithHint("Worker compatibility date (YYYY-MM-DD)"),
		).
		AddTagsField("compatibilityFlags", "Compatibility Flags",
			resolver.WithHint("Compatibility flags to enable"),
		).
		AddSelectField("usageModel", "Usage Model",
			[]resolver.SelectOption{
				{Label: "Bundled", Value: "bundled"},
				{Label: "Unbound", Value: "unbound"},
			},
			resolver.WithDefault("bundled"),
			resolver.WithHint("Worker usage model"),
		).
		EndSection().
	Build()

// CFR2ListSchema is the UI schema for cf-r2-list
var CFR2ListSchema = resolver.NewSchemaBuilder("cf-r2-list").
	WithName("List R2 Buckets").
	WithCategory("action").
	WithIcon(iconCloudflare).
	WithDescription("List all R2 buckets in your Cloudflare account").
	AddSection("Cloudflare Authentication").
		AddExpressionField("apiToken", "API Token",
			resolver.WithSensitive(),
			resolver.WithPlaceholder("Your Cloudflare API Token"),
			resolver.WithHint("Cloudflare API Token with R2:Read permission"),
		).
		AddExpressionField("accountId", "Account ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("abc123..."),
			resolver.WithHint("Cloudflare Account ID"),
		).
		EndSection().
	Build()

// CFR2UploadSchema is the UI schema for cf-r2-upload
var CFR2UploadSchema = resolver.NewSchemaBuilder("cf-r2-upload").
	WithName("Upload to R2").
	WithCategory("action").
	WithIcon(iconCloudflare).
	WithDescription("Upload an object to an R2 bucket").
	AddSection("Cloudflare Authentication").
		AddExpressionField("apiToken", "API Token",
			resolver.WithSensitive(),
			resolver.WithPlaceholder("Your Cloudflare API Token"),
			resolver.WithHint("Cloudflare API Token with R2:Write permission"),
		).
		AddExpressionField("accountId", "Account ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("abc123..."),
			resolver.WithHint("Cloudflare Account ID"),
		).
		EndSection().
	AddSection("Bucket").
		AddExpressionField("bucket", "Bucket Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-bucket"),
			resolver.WithHint("R2 bucket name"),
		).
		AddExpressionField("key", "Object Key",
			resolver.WithRequired(),
			resolver.WithPlaceholder("path/to/file.txt"),
			resolver.WithHint("Object key (path) in the bucket"),
		).
		EndSection().
	AddSection("Content").
		AddTextareaField("content", "Content",
			resolver.WithRequired(),
			resolver.WithRows(10),
			resolver.WithHint("Content to upload"),
		).
		AddExpressionField("contentType", "Content Type",
			resolver.WithPlaceholder("text/plain"),
			resolver.WithHint("MIME type of the content"),
		).
		EndSection().
	Build()

// CFR2DownloadSchema is the UI schema for cf-r2-download
var CFR2DownloadSchema = resolver.NewSchemaBuilder("cf-r2-download").
	WithName("Download from R2").
	WithCategory("action").
	WithIcon(iconCloudflare).
	WithDescription("Download an object from an R2 bucket").
	AddSection("Cloudflare Authentication").
		AddExpressionField("apiToken", "API Token",
			resolver.WithSensitive(),
			resolver.WithPlaceholder("Your Cloudflare API Token"),
			resolver.WithHint("Cloudflare API Token with R2:Read permission"),
		).
		AddExpressionField("accountId", "Account ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("abc123..."),
			resolver.WithHint("Cloudflare Account ID"),
		).
		EndSection().
	AddSection("Bucket").
		AddExpressionField("bucket", "Bucket Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-bucket"),
			resolver.WithHint("R2 bucket name"),
		).
		AddExpressionField("key", "Object Key",
			resolver.WithRequired(),
			resolver.WithPlaceholder("path/to/file.txt"),
			resolver.WithHint("Object key (path) in the bucket"),
		).
		EndSection().
	Build()

// CFD1ListSchema is the UI schema for cf-d1-list
var CFD1ListSchema = resolver.NewSchemaBuilder("cf-d1-list").
	WithName("List D1 Databases").
	WithCategory("action").
	WithIcon(iconCloudflare).
	WithDescription("List all D1 databases in your Cloudflare account").
	AddSection("Cloudflare Authentication").
		AddExpressionField("apiToken", "API Token",
			resolver.WithSensitive(),
			resolver.WithPlaceholder("Your Cloudflare API Token"),
			resolver.WithHint("Cloudflare API Token with D1:Read permission"),
		).
		AddExpressionField("accountId", "Account ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("abc123..."),
			resolver.WithHint("Cloudflare Account ID"),
		).
		EndSection().
	Build()

// CFD1QuerySchema is the UI schema for cf-d1-query
var CFD1QuerySchema = resolver.NewSchemaBuilder("cf-d1-query").
	WithName("Query D1 Database").
	WithCategory("action").
	WithIcon(iconCloudflare).
	WithDescription("Execute a SQL query on a D1 database").
	AddSection("Cloudflare Authentication").
		AddExpressionField("apiToken", "API Token",
			resolver.WithSensitive(),
			resolver.WithPlaceholder("Your Cloudflare API Token"),
			resolver.WithHint("Cloudflare API Token with D1:Write permission"),
		).
		AddExpressionField("accountId", "Account ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("abc123..."),
			resolver.WithHint("Cloudflare Account ID"),
		).
		EndSection().
	AddSection("Database").
		AddExpressionField("databaseId", "Database ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("uuid-here"),
			resolver.WithHint("D1 Database UUID"),
		).
		EndSection().
	AddSection("Query").
		AddCodeField("query", "SQL Query", "sql",
			resolver.WithRequired(),
			resolver.WithHeight(150),
			resolver.WithHint("SQL query to execute"),
		).
		AddTagsField("params", "Parameters",
			resolver.WithHint("Query parameters for prepared statements"),
		).
		EndSection().
	Build()

// CFWAFListSchema is the UI schema for cf-waf-list
var CFWAFListSchema = resolver.NewSchemaBuilder("cf-waf-list").
	WithName("List WAF Rules").
	WithCategory("action").
	WithIcon(iconCloudflare).
	WithDescription("List WAF custom rules for a zone").
	AddSection("Cloudflare Authentication").
		AddExpressionField("apiToken", "API Token",
			resolver.WithSensitive(),
			resolver.WithPlaceholder("Your Cloudflare API Token"),
			resolver.WithHint("Cloudflare API Token with Zone:Read and Firewall:Read permissions"),
		).
		AddExpressionField("email", "Email",
			resolver.WithPlaceholder("your@email.com"),
		).
		AddExpressionField("apiKey", "API Key",
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Zone").
		AddExpressionField("zoneId", "Zone ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("abc123..."),
			resolver.WithHint("Cloudflare Zone ID"),
		).
		EndSection().
	Build()

// CFWAFCreateRuleSchema is the UI schema for cf-waf-create-rule
var CFWAFCreateRuleSchema = resolver.NewSchemaBuilder("cf-waf-create-rule").
	WithName("Create WAF Rule").
	WithCategory("action").
	WithIcon(iconCloudflare).
	WithDescription("Create a new WAF custom rule").
	AddSection("Cloudflare Authentication").
		AddExpressionField("apiToken", "API Token",
			resolver.WithSensitive(),
			resolver.WithPlaceholder("Your Cloudflare API Token"),
			resolver.WithHint("Cloudflare API Token with Zone:Edit and Firewall:Edit permissions"),
		).
		AddExpressionField("email", "Email",
			resolver.WithPlaceholder("your@email.com"),
		).
		AddExpressionField("apiKey", "API Key",
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Zone").
		AddExpressionField("zoneId", "Zone ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("abc123..."),
			resolver.WithHint("Cloudflare Zone ID"),
		).
		EndSection().
	AddSection("WAF Rule").
		AddExpressionField("description", "Description",
			resolver.WithRequired(),
			resolver.WithPlaceholder("Block suspicious requests"),
			resolver.WithHint("Rule description"),
		).
		AddExpressionField("expression", "Expression",
			resolver.WithRequired(),
			resolver.WithHeight(100),
			resolver.WithHint("WAF filter expression (e.g., ip.geoip.country eq \"CN\")"),
		).
		AddSelectField("action", "Action",
			[]resolver.SelectOption{
				{Label: "Block", Value: "block"},
				{Label: "Allow", Value: "allow"},
				{Label: "Challenge", Value: "challenge"},
				{Label: "JS Challenge", Value: "js_challenge"},
				{Label: "Log", Value: "log"},
				{Label: "Managed Challenge", Value: "managed_challenge"},
			},
			resolver.WithRequired(),
			resolver.WithHint("Action to take when rule matches"),
		).
		AddNumberField("priority", "Priority",
			resolver.WithDefault(1),
			resolver.WithHint("Rule priority (lower = higher priority)"),
		).
		EndSection().
	AddSection("Options").
		AddToggleField("enabled", "Enabled",
			resolver.WithDefault(true),
			resolver.WithHint("Enable the rule"),
		).
		EndSection().
	Build()

// ============================================================================
// DNS EXECUTORS
// ============================================================================

// CFDNSListExecutor handles cf-dns-list node type
type CFDNSListExecutor struct{}

func (e *CFDNSListExecutor) Type() string { return "cf-dns-list" }

func (e *CFDNSListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, r executor.TemplateResolver) (*executor.StepResult, error) {
	config := step.Config
	cfg := parseCloudflareConfig(config)

	zoneID := getString(config, "zoneId")
	if zoneID == "" {
		return nil, fmt.Errorf("zoneId is required")
	}

	recordType := getString(config, "type")
	name := getString(config, "name")
	content := getString(config, "content")
	limit := getInt(config, "limit", 100)

	// Build query parameters
	queryParams := []string{fmt.Sprintf("per_page=%d", limit)}
	if recordType != "" {
		queryParams = append(queryParams, "type="+recordType)
	}
	if name != "" {
		queryParams = append(queryParams, "name="+name)
	}
	if content != "" {
		queryParams = append(queryParams, "content="+content)
	}

	endpoint := fmt.Sprintf("/zones/%s/dns_records?%s", zoneID, strings.Join(queryParams, "&"))

	respBody, err := cfRequest(ctx, cfg, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list DNS records: %w", err)
	}

	var dnsResp DNSListResponse
	if err := json.Unmarshal(respBody, &dnsResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	output := map[string]interface{}{
		"success": dnsResp.Success,
		"records": dnsResp.Result,
		"count":   len(dnsResp.Result),
		"total":   dnsResp.ResultInfo.Total,
	}

	return &executor.StepResult{Output: output}, nil
}

// CFDNSCreateExecutor handles cf-dns-create node type
type CFDNSCreateExecutor struct{}

func (e *CFDNSCreateExecutor) Type() string { return "cf-dns-create" }

func (e *CFDNSCreateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, r executor.TemplateResolver) (*executor.StepResult, error) {
	config := step.Config
	cfg := parseCloudflareConfig(config)

	zoneID := getString(config, "zoneId")
	if zoneID == "" {
		return nil, fmt.Errorf("zoneId is required")
	}

	recordType := getString(config, "type")
	if recordType == "" {
		return nil, fmt.Errorf("type is required")
	}

	name := getString(config, "name")
	if name == "" {
		return nil, fmt.Errorf("name is required")
	}

	content := getString(config, "content")
	if content == "" {
		return nil, fmt.Errorf("content is required")
	}

	ttl := getInt(config, "ttl", 1)
	proxied := getBool(config, "proxied", false)
	comment := getString(config, "comment")

	record := map[string]interface{}{
		"type":    recordType,
		"name":    name,
		"content": content,
		"ttl":     ttl,
		"proxied": proxied,
	}

	if recordType == "MX" || recordType == "SRV" {
		if priority := getInt(config, "priority", 0); priority > 0 {
			record["priority"] = priority
		}
	}

	if comment != "" {
		record["comment"] = comment
	}

	endpoint := fmt.Sprintf("/zones/%s/dns_records", zoneID)

	respBody, err := cfRequest(ctx, cfg, http.MethodPost, endpoint, record)
	if err != nil {
		return nil, fmt.Errorf("failed to create DNS record: %w", err)
	}

	var dnsResp CloudflareSuccessResponse
	if err := json.Unmarshal(respBody, &dnsResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	var createdRecord DNSRecord
	if err := json.Unmarshal(dnsResp.Result, &createdRecord); err != nil {
		return nil, fmt.Errorf("failed to parse created record: %w", err)
	}

	output := map[string]interface{}{
		"success": dnsResp.Success,
		"record":  createdRecord,
		"id":      createdRecord.ID,
	}

	return &executor.StepResult{Output: output}, nil
}

// CFDNSUpdateExecutor handles cf-dns-update node type
type CFDNSUpdateExecutor struct{}

func (e *CFDNSUpdateExecutor) Type() string { return "cf-dns-update" }

func (e *CFDNSUpdateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, r executor.TemplateResolver) (*executor.StepResult, error) {
	config := step.Config
	cfg := parseCloudflareConfig(config)

	zoneID := getString(config, "zoneId")
	if zoneID == "" {
		return nil, fmt.Errorf("zoneId is required")
	}

	recordID := getString(config, "recordId")
	if recordID == "" {
		return nil, fmt.Errorf("recordId is required")
	}

	record := make(map[string]interface{})

	if name := getString(config, "name"); name != "" {
		record["name"] = name
	}
	if content := getString(config, "content"); content != "" {
		record["content"] = content
	}
	if ttl := getInt(config, "ttl", 0); ttl > 0 {
		record["ttl"] = ttl
	}
	if proxied, ok := config["proxied"].(bool); ok {
		record["proxied"] = proxied
	}
	if recordType := getString(config, "type"); recordType != "" {
		record["type"] = recordType
	}

	if recordType := getString(config, "type"); recordType == "MX" || recordType == "SRV" {
		if priority := getInt(config, "priority", 0); priority > 0 {
			record["priority"] = priority
		}
	} else if priority, ok := config["priority"].(float64); ok && priority > 0 {
		record["priority"] = int(priority)
	}

	endpoint := fmt.Sprintf("/zones/%s/dns_records/%s", zoneID, recordID)

	respBody, err := cfRequest(ctx, cfg, http.MethodPatch, endpoint, record)
	if err != nil {
		return nil, fmt.Errorf("failed to update DNS record: %w", err)
	}

	var dnsResp CloudflareSuccessResponse
	if err := json.Unmarshal(respBody, &dnsResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	var updatedRecord DNSRecord
	if err := json.Unmarshal(dnsResp.Result, &updatedRecord); err != nil {
		return nil, fmt.Errorf("failed to parse updated record: %w", err)
	}

	output := map[string]interface{}{
		"success": dnsResp.Success,
		"record":  updatedRecord,
		"id":      updatedRecord.ID,
	}

	return &executor.StepResult{Output: output}, nil
}

// CFDNSDeleteExecutor handles cf-dns-delete node type
type CFDNSDeleteExecutor struct{}

func (e *CFDNSDeleteExecutor) Type() string { return "cf-dns-delete" }

func (e *CFDNSDeleteExecutor) Execute(ctx context.Context, step *executor.StepDefinition, r executor.TemplateResolver) (*executor.StepResult, error) {
	config := step.Config
	cfg := parseCloudflareConfig(config)

	zoneID := getString(config, "zoneId")
	if zoneID == "" {
		return nil, fmt.Errorf("zoneId is required")
	}

	recordID := getString(config, "recordId")
	if recordID == "" {
		return nil, fmt.Errorf("recordId is required")
	}

	endpoint := fmt.Sprintf("/zones/%s/dns_records/%s", zoneID, recordID)

	respBody, err := cfRequest(ctx, cfg, http.MethodDelete, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to delete DNS record: %w", err)
	}

	var dnsResp CloudflareSuccessResponse
	if err := json.Unmarshal(respBody, &dnsResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	output := map[string]interface{}{
		"success":  dnsResp.Success,
		"deleted":  true,
		"recordId": recordID,
	}

	return &executor.StepResult{Output: output}, nil
}

// ============================================================================
// WORKER EXECUTORS
// ============================================================================

// CFWorkerListExecutor handles cf-worker-list node type
type CFWorkerListExecutor struct{}

func (e *CFWorkerListExecutor) Type() string { return "cf-worker-list" }

func (e *CFWorkerListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, r executor.TemplateResolver) (*executor.StepResult, error) {
	config := step.Config
	cfg := parseCloudflareConfig(config)

	endpoint := "/workers/scripts"

	respBody, err := cfRequest(ctx, cfg, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list workers: %w", err)
	}

	var workerResp WorkerListResponse
	if err := json.Unmarshal(respBody, &workerResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	workers := make([]map[string]interface{}, 0, len(workerResp.Result))
	for _, w := range workerResp.Result {
		workers = append(workers, map[string]interface{}{
			"id":               w.ID,
			"created_on":       w.CreatedOn,
			"modified_on":      w.ModifiedOn,
			"usage_model":      w.UsageModel,
			"handlers":         w.Handlers,
			"compatibility_date": w.CompatibilityDate,
			"compatibility_flags": w.CompatibilityFlags,
		})
	}

	output := map[string]interface{}{
		"success": workerResp.Success,
		"workers": workers,
		"count":   len(workers),
	}

	return &executor.StepResult{Output: output}, nil
}

// CFWorkerDeployExecutor handles cf-worker-deploy node type
type CFWorkerDeployExecutor struct{}

func (e *CFWorkerDeployExecutor) Type() string { return "cf-worker-deploy" }

func (e *CFWorkerDeployExecutor) Execute(ctx context.Context, step *executor.StepDefinition, r executor.TemplateResolver) (*executor.StepResult, error) {
	config := step.Config
	cfg := parseCloudflareConfig(config)

	name := getString(config, "name")
	if name == "" {
		return nil, fmt.Errorf("name is required")
	}

	script := getString(config, "script")
	if script == "" {
		return nil, fmt.Errorf("script is required")
	}

	compatibilityDate := getString(config, "compatibilityDate")
	compatibilityFlags := getStringSlice(config, "compatibilityFlags")
	usageModel := getString(config, "usageModel")

	if usageModel != "" {
		// Note: usage_model is deprecated but kept for backwards compatibility
		_ = usageModel
	}

	endpoint := fmt.Sprintf("/accounts/scripts/%s", name)

	// For worker deployment, we need to use PUT with raw script body
	client := getCFClient(cfg)

	var reqBody io.Reader = strings.NewReader(script)

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, apiBaseURL+endpoint, reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set authentication headers
	if cfg.APIToken != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.APIToken)
	} else if cfg.APIKey != "" && cfg.Email != "" {
		req.Header.Set("X-Auth-Email", cfg.Email)
		req.Header.Set("X-Auth-Key", cfg.APIKey)
	} else {
		return nil, fmt.Errorf("either apiToken or (email + apiKey) must be provided")
	}

	req.Header.Set("Content-Type", "application/javascript")

	if compatibilityDate != "" {
		req.Header.Set("Cf-Worker-Compatibility-Date", compatibilityDate)
	}
	for _, flag := range compatibilityFlags {
		req.Header.Add("Cf-Worker-Compatibility-Flag", flag)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var errResp CloudflareErrorResponse
		if err := json.Unmarshal(respBody, &errResp); err == nil && len(errResp.Errors) > 0 {
			return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, errResp.Errors[0].Message)
		}
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(respBody))
	}

	var workerResp CloudflareSuccessResponse
	if err := json.Unmarshal(respBody, &workerResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	var deployedWorker WorkerScript
	if err := json.Unmarshal(workerResp.Result, &deployedWorker); err != nil {
		return nil, fmt.Errorf("failed to parse deployed worker: %w", err)
	}

	output := map[string]interface{}{
		"success": workerResp.Success,
		"worker": map[string]interface{}{
			"id":                 deployedWorker.ID,
			"name":               name,
			"created_on":         deployedWorker.CreatedOn,
			"modified_on":        deployedWorker.ModifiedOn,
			"usage_model":        deployedWorker.UsageModel,
			"handlers":           deployedWorker.Handlers,
			"compatibility_date": deployedWorker.CompatibilityDate,
			"compatibility_flags": deployedWorker.CompatibilityFlags,
		},
	}

	return &executor.StepResult{Output: output}, nil
}

// ============================================================================
// R2 EXECUTORS
// ============================================================================

// CFR2ListExecutor handles cf-r2-list node type
type CFR2ListExecutor struct{}

func (e *CFR2ListExecutor) Type() string { return "cf-r2-list" }

func (e *CFR2ListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, r executor.TemplateResolver) (*executor.StepResult, error) {
	config := step.Config
	cfg := parseCloudflareConfig(config)

	accountID := getString(config, "accountId")
	if accountID == "" {
		return nil, fmt.Errorf("accountId is required")
	}

	endpoint := fmt.Sprintf("/accounts/%s/r2/buckets", accountID)

	respBody, err := cfRequest(ctx, cfg, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list R2 buckets: %w", err)
	}

	var r2Resp R2ListResponse
	if err := json.Unmarshal(respBody, &r2Resp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	output := map[string]interface{}{
		"success": r2Resp.Success,
		"buckets": r2Resp.Result,
		"count":   len(r2Resp.Result),
	}

	return &executor.StepResult{Output: output}, nil
}

// CFR2UploadExecutor handles cf-r2-upload node type
type CFR2UploadExecutor struct{}

func (e *CFR2UploadExecutor) Type() string { return "cf-r2-upload" }

func (e *CFR2UploadExecutor) Execute(ctx context.Context, step *executor.StepDefinition, r executor.TemplateResolver) (*executor.StepResult, error) {
	config := step.Config
	cfg := parseCloudflareConfig(config)

	accountID := getString(config, "accountId")
	if accountID == "" {
		return nil, fmt.Errorf("accountId is required")
	}

	bucket := getString(config, "bucket")
	if bucket == "" {
		return nil, fmt.Errorf("bucket is required")
	}

	key := getString(config, "key")
	if key == "" {
		return nil, fmt.Errorf("key is required")
	}

	content := getString(config, "content")
	contentType := getString(config, "contentType")
	if contentType == "" {
		contentType = "text/plain"
	}

	client := getCFClient(cfg)

	endpoint := fmt.Sprintf("/accounts/%s/r2/buckets/%s/objects/%s", accountID, bucket, key)

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, apiBaseURL+endpoint, strings.NewReader(content))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set authentication headers
	if cfg.APIToken != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.APIToken)
	} else if cfg.APIKey != "" && cfg.Email != "" {
		req.Header.Set("X-Auth-Email", cfg.Email)
		req.Header.Set("X-Auth-Key", cfg.APIKey)
	} else {
		return nil, fmt.Errorf("either apiToken or (email + apiKey) must be provided")
	}

	req.Header.Set("Content-Type", contentType)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(respBody))
	}

	output := map[string]interface{}{
		"success": true,
		"bucket":  bucket,
		"key":     key,
		"size":    len(content),
	}

	return &executor.StepResult{Output: output}, nil
}

// CFR2DownloadExecutor handles cf-r2-download node type
type CFR2DownloadExecutor struct{}

func (e *CFR2DownloadExecutor) Type() string { return "cf-r2-download" }

func (e *CFR2DownloadExecutor) Execute(ctx context.Context, step *executor.StepDefinition, r executor.TemplateResolver) (*executor.StepResult, error) {
	config := step.Config
	cfg := parseCloudflareConfig(config)

	accountID := getString(config, "accountId")
	if accountID == "" {
		return nil, fmt.Errorf("accountId is required")
	}

	bucket := getString(config, "bucket")
	if bucket == "" {
		return nil, fmt.Errorf("bucket is required")
	}

	key := getString(config, "key")
	if key == "" {
		return nil, fmt.Errorf("key is required")
	}

	client := getCFClient(cfg)

	endpoint := fmt.Sprintf("/accounts/%s/r2/buckets/%s/objects/%s", accountID, bucket, key)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiBaseURL+endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set authentication headers
	if cfg.APIToken != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.APIToken)
	} else if cfg.APIKey != "" && cfg.Email != "" {
		req.Header.Set("X-Auth-Email", cfg.Email)
		req.Header.Set("X-Auth-Key", cfg.APIKey)
	} else {
		return nil, fmt.Errorf("either apiToken or (email + apiKey) must be provided")
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(respBody))
	}

	// Get content type from response header
	contentType := resp.Header.Get("Content-Type")
	contentLength := resp.Header.Get("Content-Length")

	output := map[string]interface{}{
		"success":     true,
		"bucket":      bucket,
		"key":         key,
		"content":     string(respBody),
		"contentType": contentType,
		"size":        len(respBody),
	}
	if contentLength != "" {
		output["contentLength"] = contentLength
	}

	return &executor.StepResult{Output: output}, nil
}

// ============================================================================
// D1 EXECUTORS
// ============================================================================

// CFD1ListExecutor handles cf-d1-list node type
type CFD1ListExecutor struct{}

func (e *CFD1ListExecutor) Type() string { return "cf-d1-list" }

func (e *CFD1ListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, r executor.TemplateResolver) (*executor.StepResult, error) {
	config := step.Config
	cfg := parseCloudflareConfig(config)

	accountID := getString(config, "accountId")
	if accountID == "" {
		return nil, fmt.Errorf("accountId is required")
	}

	endpoint := fmt.Sprintf("/accounts/%s/d1/database", accountID)

	respBody, err := cfRequest(ctx, cfg, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list D1 databases: %w", err)
	}

	var d1Resp D1ListResponse
	if err := json.Unmarshal(respBody, &d1Resp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	output := map[string]interface{}{
		"success":   d1Resp.Success,
		"databases": d1Resp.Result,
		"count":     len(d1Resp.Result),
	}

	return &executor.StepResult{Output: output}, nil
}

// CFD1QueryExecutor handles cf-d1-query node type
type CFD1QueryExecutor struct{}

func (e *CFD1QueryExecutor) Type() string { return "cf-d1-query" }

func (e *CFD1QueryExecutor) Execute(ctx context.Context, step *executor.StepDefinition, r executor.TemplateResolver) (*executor.StepResult, error) {
	config := step.Config
	cfg := parseCloudflareConfig(config)

	accountID := getString(config, "accountId")
	if accountID == "" {
		return nil, fmt.Errorf("accountId is required")
	}

	databaseID := getString(config, "databaseId")
	if databaseID == "" {
		return nil, fmt.Errorf("databaseId is required")
	}

	query := getString(config, "query")
	if query == "" {
		return nil, fmt.Errorf("query is required")
	}

	params := getStringSlice(config, "params")

	// Build request body
	queryBody := map[string]interface{}{
		"sql": query,
	}
	if len(params) > 0 {
		// Convert params to []interface{} for JSON
		paramArray := make([]interface{}, len(params))
		for i, p := range params {
			paramArray[i] = p
		}
		queryBody["params"] = paramArray
	}

	endpoint := fmt.Sprintf("/accounts/%s/d1/database/%s/query", accountID, databaseID)

	respBody, err := cfRequest(ctx, cfg, http.MethodPost, endpoint, queryBody)
	if err != nil {
		return nil, fmt.Errorf("failed to execute D1 query: %w", err)
	}

	var d1Resp D1QueryResult
	if err := json.Unmarshal(respBody, &d1Resp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if !d1Resp.Success && len(d1Resp.Errors) > 0 {
		return nil, fmt.Errorf("D1 query error: %s", d1Resp.Errors[0].Message)
	}

	output := map[string]interface{}{
		"success":   d1Resp.Success,
		"results":   d1Resp.Result.Results,
		"meta":      d1Resp.Result.Meta,
		"served_by": d1Resp.Result.ServedBy,
		"duration":  d1Resp.Result.Duration,
		"changes":   d1Resp.Result.Changes,
		"count":     len(d1Resp.Result.Results),
	}

	return &executor.StepResult{Output: output}, nil
}

// ============================================================================
// WAF EXECUTORS
// ============================================================================

// CFWAFListExecutor handles cf-waf-list node type
type CFWAFListExecutor struct{}

func (e *CFWAFListExecutor) Type() string { return "cf-waf-list" }

func (e *CFWAFListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, r executor.TemplateResolver) (*executor.StepResult, error) {
	config := step.Config
	cfg := parseCloudflareConfig(config)

	zoneID := getString(config, "zoneId")
	if zoneID == "" {
		return nil, fmt.Errorf("zoneId is required")
	}

	// List custom rules from the zone
	endpoint := fmt.Sprintf("/zones/%s/rulesets/zone_custom_rules", zoneID)

	respBody, err := cfRequest(ctx, cfg, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list WAF rules: %w", err)
	}

	var wafResp CloudflareSuccessResponse
	if err := json.Unmarshal(respBody, &wafResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Parse rules from the ruleset
	var rules []WAFRule
	rulesData := make(map[string]interface{})
	if err := json.Unmarshal(wafResp.Result, &rulesData); err == nil {
		if rulesRaw, ok := rulesData["rules"].([]interface{}); ok {
			for _, r := range rulesRaw {
				if ruleMap, ok := r.(map[string]interface{}); ok {
					rule := WAFRule{
						Enabled:     true,
						Description: getString(ruleMap, "description"),
						Expression:  getString(ruleMap, "expression"),
						Action:      getString(ruleMap, "action"),
						ID:          getString(ruleMap, "id"),
						Ref:         getString(ruleMap, "ref"),
					}
					if priority, ok := ruleMap["priority"].(float64); ok {
						rule.Priority = int(priority)
					}
					rules = append(rules, rule)
				}
			}
		}
	}

	output := map[string]interface{}{
		"success": wafResp.Success,
		"rules":   rules,
		"count":   len(rules),
	}

	return &executor.StepResult{Output: output}, nil
}

// CFWAFCreateRuleExecutor handles cf-waf-create-rule node type
type CFWAFCreateRuleExecutor struct{}

func (e *CFWAFCreateRuleExecutor) Type() string { return "cf-waf-create-rule" }

func (e *CFWAFCreateRuleExecutor) Execute(ctx context.Context, step *executor.StepDefinition, r executor.TemplateResolver) (*executor.StepResult, error) {
	config := step.Config
	cfg := parseCloudflareConfig(config)

	zoneID := getString(config, "zoneId")
	if zoneID == "" {
		return nil, fmt.Errorf("zoneId is required")
	}

	description := getString(config, "description")
	if description == "" {
		return nil, fmt.Errorf("description is required")
	}

	expression := getString(config, "expression")
	if expression == "" {
		return nil, fmt.Errorf("expression is required")
	}

	action := getString(config, "action")
	if action == "" {
		return nil, fmt.Errorf("action is required")
	}

	priority := getInt(config, "priority", 1)
	enabled := getBool(config, "enabled", true)

	// Build the ruleset update request
	rulesetUpdate := map[string]interface{}{
		"description": "Custom ruleset managed by API",
		"rules": []map[string]interface{}{
			{
				"action":      action,
				"expression":  expression,
				"description": description,
				"enabled":     enabled,
				"priority":    priority,
			},
		},
	}

	endpoint := fmt.Sprintf("/zones/%s/rulesets/zone_custom_rules", zoneID)

	respBody, err := cfRequest(ctx, cfg, http.MethodPut, endpoint, rulesetUpdate)
	if err != nil {
		return nil, fmt.Errorf("failed to create WAF rule: %w", err)
	}

	var wafResp CloudflareSuccessResponse
	if err := json.Unmarshal(respBody, &wafResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Extract the created rule from response
	var createdRules []WAFRule
	rulesData := make(map[string]interface{})
	if err := json.Unmarshal(wafResp.Result, &rulesData); err == nil {
		if rulesRaw, ok := rulesData["rules"].([]interface{}); ok {
			for _, r := range rulesRaw {
				if ruleMap, ok := r.(map[string]interface{}); ok {
					rule := WAFRule{
						Enabled:     enabled,
						Description: description,
						Expression:  expression,
						Action:      action,
						Priority:    priority,
						ID:          getString(ruleMap, "id"),
						Ref:         getString(ruleMap, "ref"),
					}
					createdRules = append(createdRules, rule)
				}
			}
		}
	}

	output := map[string]interface{}{
		"success": wafResp.Success,
		"rules":   createdRules,
		"count":   len(createdRules),
	}

	return &executor.StepResult{Output: output}, nil
}

// ============================================================================
// ZONE EXECUTORS
// ============================================================================

// CFZoneListExecutor handles cf-zone-list node type
type CFZoneListExecutor struct{}

func (e *CFZoneListExecutor) Type() string { return "cf-zone-list" }

func (e *CFZoneListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, r executor.TemplateResolver) (*executor.StepResult, error) {
	config := step.Config
	cfg := parseCloudflareConfig(config)

	// Build query parameters
	queryParams := []string{"per_page=50"}
	if name := getString(config, "name"); name != "" {
		queryParams = append(queryParams, "name="+name)
	}
	if status := getString(config, "status"); status != "" {
		queryParams = append(queryParams, "status="+status)
	}

	endpoint := fmt.Sprintf("/zones?%s", strings.Join(queryParams, "&"))

	respBody, err := cfRequest(ctx, cfg, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list zones: %w", err)
	}

	var zoneResp ZoneListResponse
	if err := json.Unmarshal(respBody, &zoneResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	zones := make([]map[string]interface{}, 0, len(zoneResp.Result))
	for _, z := range zoneResp.Result {
		zones = append(zones, map[string]interface{}{
			"id":              z.ID,
			"name":            z.Name,
			"status":          z.Status,
			"paused":          z.Paused,
			"type":            z.Type,
			"development_mode": z.DevelopMode,
			"name_servers":    z.NameServers,
			"created_on":      z.CreatedOn,
			"modified_on":     z.ModifiedOn,
			"activated_on":    z.ActivatedOn,
			"plan": map[string]interface{}{
				"name": z.Plan.Name,
				"id":   z.Plan.ID,
			},
		})
	}

	output := map[string]interface{}{
		"success": zoneResp.Success,
		"zones":   zones,
		"count":   len(zones),
		"total":   zoneResp.ResultInfo.Total,
	}

	return &executor.StepResult{Output: output}, nil
}

// ============================================================================
// MAIN
// ============================================================================

func main() {
	port := os.Getenv("SKILL_PORT")
	if port == "" {
		port = "50082"
	}

	server := grpc.NewSkillServer("skill-cloudflare", "1.0.0")

	// Register DNS executors
	server.RegisterExecutorWithSchema("cf-dns-list", &CFDNSListExecutor{}, CFDNSListSchema)
	server.RegisterExecutorWithSchema("cf-dns-create", &CFDNSCreateExecutor{}, CFDNSCreateSchema)
	server.RegisterExecutorWithSchema("cf-dns-update", &CFDNSUpdateExecutor{}, CFDNSUpdateSchema)
	server.RegisterExecutorWithSchema("cf-dns-delete", &CFDNSDeleteExecutor{}, CFDNSDeleteSchema)

	// Register Worker executors
	server.RegisterExecutorWithSchema("cf-worker-list", &CFWorkerListExecutor{}, CFWorkerListSchema)
	server.RegisterExecutorWithSchema("cf-worker-deploy", &CFWorkerDeployExecutor{}, CFWorkerDeploySchema)

	// Register R2 executors
	server.RegisterExecutorWithSchema("cf-r2-list", &CFR2ListExecutor{}, CFR2ListSchema)
	server.RegisterExecutorWithSchema("cf-r2-upload", &CFR2UploadExecutor{}, CFR2UploadSchema)
	server.RegisterExecutorWithSchema("cf-r2-download", &CFR2DownloadExecutor{}, CFR2DownloadSchema)

	// Register D1 executors
	server.RegisterExecutorWithSchema("cf-d1-list", &CFD1ListExecutor{}, CFD1ListSchema)
	server.RegisterExecutorWithSchema("cf-d1-query", &CFD1QueryExecutor{}, CFD1QuerySchema)

	// Register WAF executors
	server.RegisterExecutorWithSchema("cf-waf-list", &CFWAFListExecutor{}, CFWAFListSchema)
	server.RegisterExecutorWithSchema("cf-waf-create-rule", &CFWAFCreateRuleExecutor{}, CFWAFCreateRuleSchema)

	// Register Zone executors
	server.RegisterExecutorWithSchema("cf-zone-list", &CFZoneListExecutor{}, CFZoneListSchema)

	fmt.Printf("Starting skill-cloudflare gRPC server on port %s\n", port)
	if err := server.Serve(port); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to serve: %v\n", err)
		os.Exit(1)
	}
}
