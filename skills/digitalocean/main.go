package main

import (
	"bytes"
	"context"
	"encoding/base64"
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
	iconDigitalOcean = "cloud"
	doAPIBaseURL     = "https://api.digitalocean.com"
	doSpacesBaseURL  = "https://{{region}}.digitaloceanspaces.com"
)

// DigitalOcean API clients cache
var (
	httpClients = make(map[string]*http.Client)
	clientMux   sync.RWMutex
)

func main() {
	// Get port from env or use default
	port := os.Getenv("SKILL_PORT")
	if port == "" {
		port = "50083"
	}

	// Create skill server
	server := grpc.NewSkillServer("skill-digitalocean", "1.0.0")

	// Register Droplet executors
	server.RegisterExecutorWithSchema("do-droplet-list", &DropletListExecutor{}, DropletListSchema)
	server.RegisterExecutorWithSchema("do-droplet-create", &DropletCreateExecutor{}, DropletCreateSchema)
	server.RegisterExecutorWithSchema("do-droplet-delete", &DropletDeleteExecutor{}, DropletDeleteSchema)
	server.RegisterExecutorWithSchema("do-droplet-power", &DropletPowerExecutor{}, DropletPowerSchema)

	// Register Kubernetes executors
	server.RegisterExecutorWithSchema("do-kubernetes-list", &KubernetesListExecutor{}, KubernetesListSchema)
	server.RegisterExecutorWithSchema("do-kubernetes-create", &KubernetesCreateExecutor{}, KubernetesCreateSchema)

	// Register Spaces executors
	server.RegisterExecutorWithSchema("do-space-list", &SpaceListExecutor{}, SpaceListSchema)
	server.RegisterExecutorWithSchema("do-space-upload", &SpaceUploadExecutor{}, SpaceUploadSchema)
	server.RegisterExecutorWithSchema("do-space-download", &SpaceDownloadExecutor{}, SpaceDownloadSchema)

	// Register App Platform executors
	server.RegisterExecutorWithSchema("do-app-list", &AppListExecutor{}, AppListSchema)
	server.RegisterExecutorWithSchema("do-app-deploy", &AppDeployExecutor{}, AppDeploySchema)

	// Register Database executor
	server.RegisterExecutorWithSchema("do-database-list", &DatabaseListExecutor{}, DatabaseListSchema)

	// Register Load Balancer executor
	server.RegisterExecutorWithSchema("do-loadbalancer-list", &LoadBalancerListExecutor{}, LoadBalancerListSchema)

	fmt.Printf("Starting skill-digitalocean gRPC server on port %s\n", port)
	if err := server.Serve(port); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to serve: %v\n", err)
		os.Exit(1)
	}
}

// ============================================================================
// DIGITALOCEAN CLIENT HELPERS
// ============================================================================

// DOConfig holds DigitalOcean connection configuration
type DOConfig struct {
	APIToken string
	Region   string
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

// doRequest makes a request to the DigitalOcean API
func doRequest(ctx context.Context, cfg DOConfig, method, path string, body interface{}) ([]byte, error) {
	if cfg.APIToken == "" {
		return nil, fmt.Errorf("DigitalOcean API token is required")
	}

	var reqBody io.Reader
	if body != nil {
		jsonData, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
		reqBody = bytes.NewReader(jsonData)
	}

	url := doAPIBaseURL + path
	req, err := http.NewRequestWithContext(ctx, method, url, reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+cfg.APIToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

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
		return nil, fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	return respBody, nil
}

// doSpacesRequest makes a request to the DigitalOcean Spaces API (S3-compatible)
func doSpacesRequest(ctx context.Context, cfg DOConfig, method, bucket, key string, body []byte) ([]byte, error) {
	if cfg.APIToken == "" {
		return nil, fmt.Errorf("DigitalOcean API token is required")
	}

	region := cfg.Region
	if region == "" {
		region = "nyc3"
	}

	url := strings.Replace(doSpacesBaseURL, "{{region}}", region, 1) + "/" + bucket
	if key != "" {
		url += "/" + key
	}

	var reqBody io.Reader
	if body != nil {
		reqBody = bytes.NewReader(body)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Spaces uses the API token as the access key
	req.Header.Set("Authorization", "Bearer "+cfg.APIToken)
	if body != nil {
		req.Header.Set("Content-Type", "application/octet-stream")
	}

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
		return nil, fmt.Errorf("Spaces API error (status %d): %s", resp.StatusCode, string(respBody))
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

// Helper to get map from config
func getMap(config map[string]interface{}, key string) map[string]interface{} {
	if v, ok := config[key]; ok {
		if m, ok := v.(map[string]interface{}); ok {
			return m
		}
	}
	return nil
}

// parseDOConfig extracts DigitalOcean configuration from config map
func parseDOConfig(config map[string]interface{}) DOConfig {
	return DOConfig{
		APIToken: getString(config, "apiToken"),
		Region:   getString(config, "region"),
	}
}

// ============================================================================
// SCHEMAS
// ============================================================================

// DropletListSchema is the UI schema for do-droplet-list
var DropletListSchema = resolver.NewSchemaBuilder("do-droplet-list").
	WithName("List Droplets").
	WithCategory("action").
	WithIcon(iconDigitalOcean).
	WithDescription("List all Droplets in your DigitalOcean account").
	AddSection("Connection").
		AddExpressionField("apiToken", "API Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("dop_v1_..."),
			resolver.WithHint("DigitalOcean API token (supports {{bindings.xxx}})"),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Filters").
		AddExpressionField("region", "Region",
			resolver.WithPlaceholder("nyc3"),
			resolver.WithHint("Filter by region slug (e.g., nyc3, sfo3)"),
		).
		AddExpressionField("tag", "Tag",
			resolver.WithPlaceholder("production"),
			resolver.WithHint("Filter by tag"),
		).
		AddExpressionField("status", "Status",
			resolver.WithPlaceholder("active"),
			resolver.WithHint("Filter by status (active, archived, etc.)"),
		).
		EndSection().
	AddSection("Options").
		AddNumberField("limit", "Limit",
			resolver.WithDefault(100),
			resolver.WithMinMax(1, 200),
			resolver.WithHint("Maximum number of droplets to return"),
		).
		AddNumberField("page", "Page",
			resolver.WithDefault(1),
			resolver.WithMinMax(1, 999),
			resolver.WithHint("Page number for pagination"),
		).
		EndSection().
	Build()

// DropletCreateSchema is the UI schema for do-droplet-create
var DropletCreateSchema = resolver.NewSchemaBuilder("do-droplet-create").
	WithName("Create Droplet").
	WithCategory("action").
	WithIcon(iconDigitalOcean).
	WithDescription("Create a new Droplet in DigitalOcean").
	AddSection("Connection").
		AddExpressionField("apiToken", "API Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("dop_v1_..."),
			resolver.WithHint("DigitalOcean API token"),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Droplet Configuration").
		AddExpressionField("name", "Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-droplet"),
			resolver.WithHint("Name for the new droplet"),
		).
		AddExpressionField("region", "Region",
			resolver.WithRequired(),
			resolver.WithPlaceholder("nyc3"),
			resolver.WithHint("Region slug (e.g., nyc3, sfo3, lon1)"),
		).
		AddExpressionField("size", "Size",
			resolver.WithRequired(),
			resolver.WithPlaceholder("s-1vcpu-1gb"),
			resolver.WithHint("Size slug (e.g., s-1vcpu-1gb, s-2vcpu-2gb)"),
		).
		AddExpressionField("image", "Image",
			resolver.WithRequired(),
			resolver.WithPlaceholder("ubuntu-22-04-x64"),
			resolver.WithHint("Image slug or ID (e.g., ubuntu-22-04-x64, debian-11-x64)"),
		).
		EndSection().
	AddSection("SSH Keys").
		AddTagsField("sshKeys", "SSH Key Fingerprints",
			resolver.WithHint("SSH key fingerprints to add"),
		).
		AddExpressionField("sshKeyIDs", "SSH Key IDs",
			resolver.WithPlaceholder("12345,67890"),
			resolver.WithHint("Comma-separated SSH key IDs"),
		).
		EndSection().
	AddSection("Options").
		AddExpressionField("backups", "Enable Backups",
			resolver.WithPlaceholder("false"),
			resolver.WithHint("Enable automated backups (true/false)"),
		).
		AddExpressionField("ipv6", "Enable IPv6",
			resolver.WithPlaceholder("false"),
			resolver.WithHint("Enable IPv6 networking (true/false)"),
		).
		AddExpressionField("privateNetworking", "Private Networking",
			resolver.WithPlaceholder("true"),
			resolver.WithHint("Enable private networking (true/false)"),
		).
		AddExpressionField("monitoring", "Enable Monitoring",
			resolver.WithPlaceholder("true"),
			resolver.WithHint("Enable monitoring agent (true/false)"),
		).
		AddTagsField("tags", "Tags",
			resolver.WithHint("Tags to apply to the droplet"),
		).
		AddExpressionField("userData", "User Data",
			resolver.WithHint("Cloud-init user data script"),
		).
		AddExpressionField("vpcUUID", "VPC UUID",
			resolver.WithHint("VPC UUID for private networking"),
		).
		EndSection().
	Build()

// DropletDeleteSchema is the UI schema for do-droplet-delete
var DropletDeleteSchema = resolver.NewSchemaBuilder("do-droplet-delete").
	WithName("Delete Droplet").
	WithCategory("action").
	WithIcon(iconDigitalOcean).
	WithDescription("Delete a Droplet from DigitalOcean").
	AddSection("Connection").
		AddExpressionField("apiToken", "API Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("dop_v1_..."),
			resolver.WithHint("DigitalOcean API token"),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Droplet").
		AddExpressionField("dropletId", "Droplet ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("12345678"),
			resolver.WithHint("ID of the droplet to delete"),
		).
		EndSection().
	Build()

// DropletPowerSchema is the UI schema for do-droplet-power
var DropletPowerSchema = resolver.NewSchemaBuilder("do-droplet-power").
	WithName("Droplet Power Action").
	WithCategory("action").
	WithIcon(iconDigitalOcean).
	WithDescription("Perform power actions on a Droplet (boot, shutdown, reboot, etc.)").
	AddSection("Connection").
		AddExpressionField("apiToken", "API Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("dop_v1_..."),
			resolver.WithHint("DigitalOcean API token"),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Droplet").
		AddExpressionField("dropletId", "Droplet ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("12345678"),
			resolver.WithHint("ID of the droplet"),
		).
		EndSection().
	AddSection("Action").
		AddSelectField("action", "Power Action",
			[]resolver.SelectOption{
				{Label: "Boot", Value: "boot"},
				{Label: "Shutdown", Value: "shutdown"},
				{Label: "Reboot", Value: "reboot"},
				{Label: "Power Off", Value: "power_off"},
				{Label: "Power On", Value: "power_on"},
				{Label: "Power Cycle", Value: "power_cycle"},
				{Label: "Enable Backups", Value: "enable_backups"},
				{Label: "Disable Backups", Value: "disable_backups"},
				{Label: "Enable IPv6", Value: "enable_ipv6"},
				{Label: "Enable Private Networking", Value: "enable_private_networking"},
			},
			resolver.WithDefault("reboot"),
			resolver.WithHint("Power action to perform"),
		).
		EndSection().
	Build()

// KubernetesListSchema is the UI schema for do-kubernetes-list
var KubernetesListSchema = resolver.NewSchemaBuilder("do-kubernetes-list").
	WithName("List Kubernetes Clusters").
	WithCategory("action").
	WithIcon(iconDigitalOcean).
	WithDescription("List all Kubernetes clusters in your DigitalOcean account").
	AddSection("Connection").
		AddExpressionField("apiToken", "API Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("dop_v1_..."),
			resolver.WithHint("DigitalOcean API token"),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Options").
		AddNumberField("limit", "Limit",
			resolver.WithDefault(100),
			resolver.WithMinMax(1, 200),
			resolver.WithHint("Maximum number of clusters to return"),
		).
		AddNumberField("page", "Page",
			resolver.WithDefault(1),
			resolver.WithMinMax(1, 999),
			resolver.WithHint("Page number for pagination"),
		).
		EndSection().
	Build()

// KubernetesCreateSchema is the UI schema for do-kubernetes-create
var KubernetesCreateSchema = resolver.NewSchemaBuilder("do-kubernetes-create").
	WithName("Create Kubernetes Cluster").
	WithCategory("action").
	WithIcon(iconDigitalOcean).
	WithDescription("Create a new Kubernetes cluster in DigitalOcean").
	AddSection("Connection").
		AddExpressionField("apiToken", "API Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("dop_v1_..."),
			resolver.WithHint("DigitalOcean API token"),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Cluster Configuration").
		AddExpressionField("name", "Cluster Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-k8s-cluster"),
			resolver.WithHint("Name for the new cluster"),
		).
		AddExpressionField("region", "Region",
			resolver.WithRequired(),
			resolver.WithPlaceholder("nyc3"),
			resolver.WithHint("Region slug (e.g., nyc3, sfo3)"),
		).
		AddExpressionField("version", "Kubernetes Version",
			resolver.WithPlaceholder("1.28.2-do.0"),
			resolver.WithHint("Kubernetes version (leave empty for latest)"),
		).
		EndSection().
	AddSection("Node Pool").
		AddExpressionField("nodePoolName", "Node Pool Name",
			resolver.WithDefault("default-pool"),
			resolver.WithHint("Name for the default node pool"),
		).
		AddExpressionField("nodeSize", "Node Size",
			resolver.WithRequired(),
			resolver.WithPlaceholder("s-2vcpu-4gb"),
			resolver.WithHint("Size slug for nodes (e.g., s-2vcpu-4gb)"),
		).
		AddNumberField("nodeCount", "Node Count",
			resolver.WithDefault(2),
			resolver.WithMinMax(1, 50),
			resolver.WithHint("Number of nodes in the pool"),
		).
		EndSection().
	AddSection("Options").
		AddTagsField("tags", "Tags",
			resolver.WithHint("Tags to apply to the cluster"),
		).
		AddToggleField("ha", "High Availability",
			resolver.WithDefault(false),
			resolver.WithHint("Enable highly-available control plane"),
		).
		AddToggleField("autoUpgrade", "Auto Upgrade",
			resolver.WithDefault(false),
			resolver.WithHint("Enable automatic Kubernetes upgrades"),
		).
		EndSection().
	Build()

// SpaceListSchema is the UI schema for do-space-list
var SpaceListSchema = resolver.NewSchemaBuilder("do-space-list").
	WithName("List Spaces").
	WithCategory("action").
	WithIcon(iconDigitalOcean).
	WithDescription("List all Spaces (S3-compatible object storage) in your DigitalOcean account").
	AddSection("Connection").
		AddExpressionField("apiToken", "API Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("dop_v1_..."),
			resolver.WithHint("DigitalOcean API token"),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Options").
		AddExpressionField("region", "Region",
			resolver.WithPlaceholder("nyc3"),
			resolver.WithHint("Filter by region (optional)"),
		).
		EndSection().
	Build()

// SpaceUploadSchema is the UI schema for do-space-upload
var SpaceUploadSchema = resolver.NewSchemaBuilder("do-space-upload").
	WithName("Upload to Space").
	WithCategory("action").
	WithIcon(iconDigitalOcean).
	WithDescription("Upload a file to a DigitalOcean Space").
	AddSection("Connection").
		AddExpressionField("apiToken", "API Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("dop_v1_..."),
			resolver.WithHint("DigitalOcean API token"),
			resolver.WithSensitive(),
		).
		AddExpressionField("region", "Region",
			resolver.WithRequired(),
			resolver.WithPlaceholder("nyc3"),
			resolver.WithHint("Space region (e.g., nyc3, sfo3)"),
		).
		EndSection().
	AddSection("Space Configuration").
		AddExpressionField("bucket", "Bucket Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-space"),
			resolver.WithHint("Name of the Space bucket"),
		).
		AddExpressionField("key", "Object Key",
			resolver.WithRequired(),
			resolver.WithPlaceholder("path/to/file.txt"),
			resolver.WithHint("Key (path) for the uploaded object"),
		).
		EndSection().
	AddSection("Content").
		AddTextareaField("content", "Content",
			resolver.WithRequired(),
			resolver.WithRows(6),
			resolver.WithPlaceholder("File content here..."),
			resolver.WithHint("Content to upload (text or base64 for binary)"),
		).
		AddToggleField("base64Decode", "Base64 Decode",
			resolver.WithDefault(false),
			resolver.WithHint("Decode content from base64 before uploading"),
		).
		AddExpressionField("contentType", "Content Type",
			resolver.WithPlaceholder("text/plain"),
			resolver.WithHint("MIME type of the content"),
		).
		EndSection().
	Build()

// SpaceDownloadSchema is the UI schema for do-space-download
var SpaceDownloadSchema = resolver.NewSchemaBuilder("do-space-download").
	WithName("Download from Space").
	WithCategory("action").
	WithIcon(iconDigitalOcean).
	WithDescription("Download a file from a DigitalOcean Space").
	AddSection("Connection").
		AddExpressionField("apiToken", "API Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("dop_v1_..."),
			resolver.WithHint("DigitalOcean API token"),
			resolver.WithSensitive(),
		).
		AddExpressionField("region", "Region",
			resolver.WithRequired(),
			resolver.WithPlaceholder("nyc3"),
			resolver.WithHint("Space region (e.g., nyc3, sfo3)"),
		).
		EndSection().
	AddSection("Space Configuration").
		AddExpressionField("bucket", "Bucket Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-space"),
			resolver.WithHint("Name of the Space bucket"),
		).
		AddExpressionField("key", "Object Key",
			resolver.WithRequired(),
			resolver.WithPlaceholder("path/to/file.txt"),
			resolver.WithHint("Key (path) of the object to download"),
		).
		EndSection().
	AddSection("Options").
		AddToggleField("base64Encode", "Base64 Encode",
			resolver.WithDefault(false),
			resolver.WithHint("Encode the content as base64 (useful for binary files)"),
		).
		EndSection().
	Build()

// AppListSchema is the UI schema for do-app-list
var AppListSchema = resolver.NewSchemaBuilder("do-app-list").
	WithName("List Apps").
	WithCategory("action").
	WithIcon(iconDigitalOcean).
	WithDescription("List all Apps in DigitalOcean App Platform").
	AddSection("Connection").
		AddExpressionField("apiToken", "API Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("dop_v1_..."),
			resolver.WithHint("DigitalOcean API token"),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Options").
		AddNumberField("limit", "Limit",
			resolver.WithDefault(100),
			resolver.WithMinMax(1, 200),
			resolver.WithHint("Maximum number of apps to return"),
		).
		AddNumberField("page", "Page",
			resolver.WithDefault(1),
			resolver.WithMinMax(1, 999),
			resolver.WithHint("Page number for pagination"),
		).
		EndSection().
	Build()

// AppDeploySchema is the UI schema for do-app-deploy
var AppDeploySchema = resolver.NewSchemaBuilder("do-app-deploy").
	WithName("Deploy App").
	WithCategory("action").
	WithIcon(iconDigitalOcean).
	WithDescription("Trigger a new deployment for a DigitalOcean App").
	AddSection("Connection").
		AddExpressionField("apiToken", "API Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("dop_v1_..."),
			resolver.WithHint("DigitalOcean API token"),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("App Configuration").
		AddExpressionField("appId", "App ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("abc123-def456"),
			resolver.WithHint("ID of the app to deploy"),
		).
		EndSection().
	AddSection("Options").
		AddExpressionField("force", "Force Build",
			resolver.WithPlaceholder("false"),
			resolver.WithHint("Force a full rebuild (true/false)"),
		).
		EndSection().
	Build()

// DatabaseListSchema is the UI schema for do-database-list
var DatabaseListSchema = resolver.NewSchemaBuilder("do-database-list").
	WithName("List Databases").
	WithCategory("action").
	WithIcon(iconDigitalOcean).
	WithDescription("List all Database clusters in your DigitalOcean account").
	AddSection("Connection").
		AddExpressionField("apiToken", "API Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("dop_v1_..."),
			resolver.WithHint("DigitalOcean API token"),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Options").
		AddExpressionField("engine", "Engine",
			resolver.WithPlaceholder("mysql"),
			resolver.WithHint("Filter by engine (mysql, postgres, redis, mongodb)"),
		).
		AddNumberField("limit", "Limit",
			resolver.WithDefault(100),
			resolver.WithMinMax(1, 200),
			resolver.WithHint("Maximum number of databases to return"),
		).
		AddNumberField("page", "Page",
			resolver.WithDefault(1),
			resolver.WithMinMax(1, 999),
			resolver.WithHint("Page number for pagination"),
		).
		EndSection().
	Build()

// LoadBalancerListSchema is the UI schema for do-loadbalancer-list
var LoadBalancerListSchema = resolver.NewSchemaBuilder("do-loadbalancer-list").
	WithName("List Load Balancers").
	WithCategory("action").
	WithIcon(iconDigitalOcean).
	WithDescription("List all Load Balancers in your DigitalOcean account").
	AddSection("Connection").
		AddExpressionField("apiToken", "API Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("dop_v1_..."),
			resolver.WithHint("DigitalOcean API token"),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Options").
		AddExpressionField("region", "Region",
			resolver.WithPlaceholder("nyc3"),
			resolver.WithHint("Filter by region"),
		).
		AddNumberField("limit", "Limit",
			resolver.WithDefault(100),
			resolver.WithMinMax(1, 200),
			resolver.WithHint("Maximum number of load balancers to return"),
		).
		AddNumberField("page", "Page",
			resolver.WithDefault(1),
			resolver.WithMinMax(1, 999),
			resolver.WithHint("Page number for pagination"),
		).
		EndSection().
	Build()

// ============================================================================
// DROPLET EXECUTORS
// ============================================================================

// DropletListExecutor handles do-droplet-list
type DropletListExecutor struct{}

func (e *DropletListExecutor) Type() string { return "do-droplet-list" }

func (e *DropletListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)
	cfg := parseDOConfig(config)

	// Build query parameters
	queryParams := []string{}
	if region := getString(config, "region"); region != "" {
		queryParams = append(queryParams, "region="+region)
	}
	if tag := getString(config, "tag"); tag != "" {
		queryParams = append(queryParams, "tag_name="+tag)
	}
	if status := getString(config, "status"); status != "" {
		queryParams = append(queryParams, "status="+status)
	}
	limit := getInt(config, "limit", 100)
	page := getInt(config, "page", 1)
	queryParams = append(queryParams, fmt.Sprintf("per_page=%d", limit))
	queryParams = append(queryParams, fmt.Sprintf("page=%d", page))

	query := ""
	if len(queryParams) > 0 {
		query = "?" + strings.Join(queryParams, "&")
	}

	respBody, err := doRequest(ctx, cfg, http.MethodGet, "/v2/droplets"+query, nil)
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &executor.StepResult{
		Output: result,
	}, nil
}

// DropletCreateExecutor handles do-droplet-create
type DropletCreateExecutor struct{}

func (e *DropletCreateExecutor) Type() string { return "do-droplet-create" }

func (e *DropletCreateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)
	cfg := parseDOConfig(config)

	// Build request body
	body := map[string]interface{}{
		"name":   getString(config, "name"),
		"region": getString(config, "region"),
		"size":   getString(config, "size"),
		"image":  getString(config, "image"),
	}

	// Add SSH keys
	sshKeys := []interface{}{}
	if keyIDs := getString(config, "sshKeyIDs"); keyIDs != "" {
		for _, id := range strings.Split(keyIDs, ",") {
			id = strings.TrimSpace(id)
			if id != "" {
				sshKeys = append(sshKeys, map[string]interface{}{"id": id})
			}
		}
	}
	for _, fp := range getStringSlice(config, "sshKeys") {
		sshKeys = append(sshKeys, map[string]interface{}{"fingerprint": fp})
	}
	if len(sshKeys) > 0 {
		body["ssh_keys"] = sshKeys
	}

	// Add optional fields
	if backups := getString(config, "backups"); backups != "" {
		body["backups"] = strings.ToLower(backups) == "true"
	}
	if ipv6 := getString(config, "ipv6"); ipv6 != "" {
		body["ipv6"] = strings.ToLower(ipv6) == "true"
	}
	if privateNetworking := getString(config, "privateNetworking"); privateNetworking != "" {
		body["private_networking"] = strings.ToLower(privateNetworking) == "true"
	}
	if monitoring := getString(config, "monitoring"); monitoring != "" {
		body["monitoring"] = strings.ToLower(monitoring) == "true"
	}
	if tags := getStringSlice(config, "tags"); len(tags) > 0 {
		body["tags"] = tags
	}
	if userData := getString(config, "userData"); userData != "" {
		body["user_data"] = userData
	}
	if vpcUUID := getString(config, "vpcUUID"); vpcUUID != "" {
		body["vpc_uuid"] = vpcUUID
	}

	respBody, err := doRequest(ctx, cfg, http.MethodPost, "/v2/droplets", body)
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &executor.StepResult{
		Output: result,
	}, nil
}

// DropletDeleteExecutor handles do-droplet-delete
type DropletDeleteExecutor struct{}

func (e *DropletDeleteExecutor) Type() string { return "do-droplet-delete" }

func (e *DropletDeleteExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)
	cfg := parseDOConfig(config)

	dropletID := getString(config, "dropletId")
	if dropletID == "" {
		return nil, fmt.Errorf("droplet ID is required")
	}

	_, err := doRequest(ctx, cfg, http.MethodDelete, "/v2/droplets/"+dropletID, nil)
	if err != nil {
		return nil, err
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":   true,
			"dropletId": dropletID,
			"message":   "Droplet deleted successfully",
		},
	}, nil
}

// DropletPowerExecutor handles do-droplet-power
type DropletPowerExecutor struct{}

func (e *DropletPowerExecutor) Type() string { return "do-droplet-power" }

func (e *DropletPowerExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)
	cfg := parseDOConfig(config)

	dropletID := getString(config, "dropletId")
	if dropletID == "" {
		return nil, fmt.Errorf("droplet ID is required")
	}

	actionType := getString(config, "action")
	if actionType == "" {
		actionType = "reboot"
	}

	// Map action types to DO API action types
	actionMap := map[string]string{
		"boot":                        "boot",
		"shutdown":                    "shutdown",
		"reboot":                      "reboot",
		"power_off":                   "power_off",
		"power_on":                    "power_on",
		"power_cycle":                 "power_cycle",
		"enable_backups":              "enable_backups",
		"disable_backups":             "disable_backups",
		"enable_ipv6":                 "enable_ipv6",
		"enable_private_networking":   "enable_private_networking",
	}

	doActionType, ok := actionMap[actionType]
	if !ok {
		return nil, fmt.Errorf("unknown action type: %s", actionType)
	}

	body := map[string]interface{}{
		"type": doActionType,
	}

	respBody, err := doRequest(ctx, cfg, http.MethodPost, "/v2/droplets/"+dropletID+"/actions", body)
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &executor.StepResult{
		Output: result,
	}, nil
}

// ============================================================================
// KUBERNETES EXECUTORS
// ============================================================================

// KubernetesListExecutor handles do-kubernetes-list
type KubernetesListExecutor struct{}

func (e *KubernetesListExecutor) Type() string { return "do-kubernetes-list" }

func (e *KubernetesListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)
	cfg := parseDOConfig(config)

	limit := getInt(config, "limit", 100)
	page := getInt(config, "page", 1)
	query := fmt.Sprintf("?per_page=%d&page=%d", limit, page)

	respBody, err := doRequest(ctx, cfg, http.MethodGet, "/v2/kubernetes/clusters"+query, nil)
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &executor.StepResult{
		Output: result,
	}, nil
}

// KubernetesCreateExecutor handles do-kubernetes-create
type KubernetesCreateExecutor struct{}

func (e *KubernetesCreateExecutor) Type() string { return "do-kubernetes-create" }

func (e *KubernetesCreateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)
	cfg := parseDOConfig(config)

	// Build node pool
	nodePool := map[string]interface{}{
		"name":      getString(config, "nodePoolName"),
		"size":      getString(config, "nodeSize"),
		"count":     getInt(config, "nodeCount", 2),
		"auto_scale": false,
	}
	if nodePool["name"] == "" {
		nodePool["name"] = "default-pool"
	}

	// Build request body
	body := map[string]interface{}{
		"name":    getString(config, "name"),
		"region":  getString(config, "region"),
		"version": getString(config, "version"),
		"node_pools": []map[string]interface{}{nodePool},
	}

	// Add optional fields
	if tags := getStringSlice(config, "tags"); len(tags) > 0 {
		body["tags"] = tags
	}
	if ha := getBool(config, "ha", false); ha {
		body["ha"] = true
	}
	if autoUpgrade := getBool(config, "autoUpgrade", false); autoUpgrade {
		body["auto_upgrade"] = true
	}

	respBody, err := doRequest(ctx, cfg, http.MethodPost, "/v2/kubernetes/clusters", body)
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &executor.StepResult{
		Output: result,
	}, nil
}

// ============================================================================
// SPACES EXECUTORS
// ============================================================================

// SpaceListExecutor handles do-space-list
type SpaceListExecutor struct{}

func (e *SpaceListExecutor) Type() string { return "do-space-list" }

func (e *SpaceListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)
	cfg := parseDOConfig(config)

	// Use the DO API to list spaces
	region := getString(config, "region")
	path := "/v2/spaces"
	if region != "" {
		path += "?region=" + region
	}

	respBody, err := doRequest(ctx, cfg, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &executor.StepResult{
		Output: result,
	}, nil
}

// SpaceUploadExecutor handles do-space-upload
type SpaceUploadExecutor struct{}

func (e *SpaceUploadExecutor) Type() string { return "do-space-upload" }

func (e *SpaceUploadExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)
	cfg := parseDOConfig(config)

	bucket := getString(config, "bucket")
	key := getString(config, "key")
	content := getString(config, "content")
	contentType := getString(config, "contentType")
	base64Decode := getBool(config, "base64Decode", false)

	if bucket == "" {
		return nil, fmt.Errorf("bucket name is required")
	}
	if key == "" {
		return nil, fmt.Errorf("object key is required")
	}

	// Process content
	var contentBytes []byte
	if base64Decode {
		// Decode from base64
		decoded, err := base64DecodeString(content)
		if err != nil {
			return nil, fmt.Errorf("failed to decode base64: %w", err)
		}
		contentBytes = decoded
	} else {
		contentBytes = []byte(content)
	}

	// Set default content type
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	// Upload to Spaces
	_, err := doSpacesRequest(ctx, cfg, http.MethodPut, bucket, key, contentBytes)
	if err != nil {
		return nil, err
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":     true,
			"bucket":      bucket,
			"key":         key,
			"size":        len(contentBytes),
			"contentType": contentType,
			"message":     "File uploaded successfully",
		},
	}, nil
}

// SpaceDownloadExecutor handles do-space-download
type SpaceDownloadExecutor struct{}

func (e *SpaceDownloadExecutor) Type() string { return "do-space-download" }

func (e *SpaceDownloadExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)
	cfg := parseDOConfig(config)

	bucket := getString(config, "bucket")
	key := getString(config, "key")
	base64Encode := getBool(config, "base64Encode", false)

	if bucket == "" {
		return nil, fmt.Errorf("bucket name is required")
	}
	if key == "" {
		return nil, fmt.Errorf("object key is required")
	}

	// Download from Spaces
	contentBytes, err := doSpacesRequest(ctx, cfg, http.MethodGet, bucket, key, nil)
	if err != nil {
		return nil, err
	}

	// Build output
	output := map[string]interface{}{
		"success": true,
		"bucket":  bucket,
		"key":     key,
		"size":    len(contentBytes),
	}

	if base64Encode {
		output["content"] = base64EncodeBytes(contentBytes)
		output["encoding"] = "base64"
	} else {
		output["content"] = string(contentBytes)
		output["encoding"] = "text"
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// ============================================================================
// APP PLATFORM EXECUTORS
// ============================================================================

// AppListExecutor handles do-app-list
type AppListExecutor struct{}

func (e *AppListExecutor) Type() string { return "do-app-list" }

func (e *AppListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)
	cfg := parseDOConfig(config)

	limit := getInt(config, "limit", 100)
	page := getInt(config, "page", 1)
	query := fmt.Sprintf("?per_page=%d&page=%d", limit, page)

	respBody, err := doRequest(ctx, cfg, http.MethodGet, "/v2/apps"+query, nil)
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &executor.StepResult{
		Output: result,
	}, nil
}

// AppDeployExecutor handles do-app-deploy
type AppDeployExecutor struct{}

func (e *AppDeployExecutor) Type() string { return "do-app-deploy" }

func (e *AppDeployExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)
	cfg := parseDOConfig(config)

	appID := getString(config, "appId")
	if appID == "" {
		return nil, fmt.Errorf("app ID is required")
	}

	forceBuild := strings.ToLower(getString(config, "force")) == "true"

	body := map[string]interface{}{
		"force_build": forceBuild,
	}

	respBody, err := doRequest(ctx, cfg, http.MethodPost, "/v2/apps/"+appID+"/deployments", body)
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &executor.StepResult{
		Output: result,
	}, nil
}

// ============================================================================
// DATABASE EXECUTOR
// ============================================================================

// DatabaseListExecutor handles do-database-list
type DatabaseListExecutor struct{}

func (e *DatabaseListExecutor) Type() string { return "do-database-list" }

func (e *DatabaseListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)
	cfg := parseDOConfig(config)

	queryParams := []string{}
	if engine := getString(config, "engine"); engine != "" {
		queryParams = append(queryParams, "engine="+engine)
	}
	limit := getInt(config, "limit", 100)
	page := getInt(config, "page", 1)
	queryParams = append(queryParams, fmt.Sprintf("per_page=%d", limit))
	queryParams = append(queryParams, fmt.Sprintf("page=%d", page))

	query := ""
	if len(queryParams) > 0 {
		query = "?" + strings.Join(queryParams, "&")
	}

	respBody, err := doRequest(ctx, cfg, http.MethodGet, "/v2/databases"+query, nil)
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &executor.StepResult{
		Output: result,
	}, nil
}

// ============================================================================
// LOAD BALANCER EXECUTOR
// ============================================================================

// LoadBalancerListExecutor handles do-loadbalancer-list
type LoadBalancerListExecutor struct{}

func (e *LoadBalancerListExecutor) Type() string { return "do-loadbalancer-list" }

func (e *LoadBalancerListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)
	cfg := parseDOConfig(config)

	queryParams := []string{}
	if region := getString(config, "region"); region != "" {
		queryParams = append(queryParams, "region="+region)
	}
	limit := getInt(config, "limit", 100)
	page := getInt(config, "page", 1)
	queryParams = append(queryParams, fmt.Sprintf("per_page=%d", limit))
	queryParams = append(queryParams, fmt.Sprintf("page=%d", page))

	query := ""
	if len(queryParams) > 0 {
		query = "?" + strings.Join(queryParams, "&")
	}

	respBody, err := doRequest(ctx, cfg, http.MethodGet, "/v2/load_balancers"+query, nil)
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &executor.StepResult{
		Output: result,
	}, nil
}

// ============================================================================
// HELPER FUNCTIONS
// ============================================================================

// base64DecodeString decodes a base64 string
func base64DecodeString(s string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(s)
}

// base64EncodeBytes encodes bytes to base64
func base64EncodeBytes(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}
