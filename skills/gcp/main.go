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

	computeapiv1 "cloud.google.com/go/compute/apiv1"
	computepb "cloud.google.com/go/compute/apiv1/computepb"
	containerapiv1 "cloud.google.com/go/container/apiv1"
	containerpb "cloud.google.com/go/container/apiv1/containerpb"
	functionsapiv1 "cloud.google.com/go/functions/apiv1"
	functionspb "cloud.google.com/go/functions/apiv1/functionspb"
	"cloud.google.com/go/pubsub"
	"cloud.google.com/go/storage"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/axiom-studio/skills.sdk/executor"
	skillgrpc "github.com/axiom-studio/skills.sdk/grpc"
	"github.com/axiom-studio/skills.sdk/resolver"
)

const (
	iconGCP = "cloud"
)

// GCP clients cache
var (
	gcpConfigs       = make(map[string]*option.ClientOption)
	gcpConfigMux     sync.RWMutex
	computeClients   = make(map[string]*computeapiv1.InstancesClient)
	storageClients   = make(map[string]*storage.Client)
	pubsubClients    = make(map[string]*pubsub.Client)
	containerClients = make(map[string]*containerapiv1.ClusterManagerClient)
	functionClients  = make(map[string]*functionsapiv1.CloudFunctionsClient)
	clientMux        sync.RWMutex
)

func main() {
	// Get port from env or use default
	port := os.Getenv("SKILL_PORT")
	if port == "" {
		port = "50081"
	}

	// Create skill server
	server := skillgrpc.NewSkillServer("skill-gcp", "1.0.0")

	// Register Compute Engine executors
	server.RegisterExecutorWithSchema("gcp-instance-list", &GCPInstanceListExecutor{}, GCPInstanceListSchema)
	server.RegisterExecutorWithSchema("gcp-instance-start", &GCPInstanceStartExecutor{}, GCPInstanceStartSchema)
	server.RegisterExecutorWithSchema("gcp-instance-stop", &GCPInstanceStopExecutor{}, GCPInstanceStopSchema)

	// Register GKE executors
	server.RegisterExecutorWithSchema("gcp-gke-list", &GCPGKEListExecutor{}, GCPGKEListSchema)
	server.RegisterExecutorWithSchema("gcp-gke-get-credentials", &GCPGKEGetCredentialsExecutor{}, GCPGKEGetCredentialsSchema)

	// Register Cloud Functions executors
	server.RegisterExecutorWithSchema("gcp-function-list", &GCPFunctionListExecutor{}, GCPFunctionListSchema)
	server.RegisterExecutorWithSchema("gcp-function-deploy", &GCPFunctionDeployExecutor{}, GCPFunctionDeploySchema)
	server.RegisterExecutorWithSchema("gcp-function-invoke", &GCPFunctionInvokeExecutor{}, GCPFunctionInvokeSchema)

	// Register Pub/Sub executors
	server.RegisterExecutorWithSchema("gcp-pubsub-publish", &GCPPubSubPublishExecutor{}, GCPPubSubPublishSchema)
	server.RegisterExecutorWithSchema("gcp-pubsub-subscribe", &GCPPubSubSubscribeExecutor{}, GCPPubSubSubscribeSchema)

	// Register Cloud Storage executors
	server.RegisterExecutorWithSchema("gcp-storage-list", &GCPStorageListExecutor{}, GCPStorageListSchema)
	server.RegisterExecutorWithSchema("gcp-storage-upload", &GCPStorageUploadExecutor{}, GCPStorageUploadSchema)
	server.RegisterExecutorWithSchema("gcp-storage-download", &GCPStorageDownloadExecutor{}, GCPStorageDownloadSchema)

	fmt.Printf("Starting skill-gcp gRPC server on port %s\n", port)
	if err := server.Serve(port); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to serve: %v\n", err)
		os.Exit(1)
	}
}

// ============================================================================
// GCP CONFIGURATION AND CLIENT HELPERS
// ============================================================================

// GCPConfig holds GCP authentication configuration
type GCPConfig struct {
	ProjectID   string
	Credentials string
	Region      string
	Zone        string
}

// parseGCPConfig extracts GCP configuration from config map
func parseGCPConfig(config map[string]interface{}) GCPConfig {
	cfg := GCPConfig{
		ProjectID:   getString(config, "projectId"),
		Credentials: getString(config, "credentials"),
		Region:      getString(config, "region"),
		Zone:        getString(config, "zone"),
	}
	return cfg
}

// getCredentialsOption creates a client option from credentials JSON
func getCredentialsOption(credentials string) (option.ClientOption, error) {
	if credentials == "" {
		return nil, fmt.Errorf("credentials are required")
	}
	return option.WithCredentialsJSON([]byte(credentials)), nil
}

// getComputeClient returns a Compute Engine client (cached)
func getComputeClient(cfg GCPConfig) (*computeapiv1.InstancesClient, error) {
	cacheKey := fmt.Sprintf("%s:%s", cfg.ProjectID, cfg.Credentials)

	clientMux.RLock()
	client, ok := computeClients[cacheKey]
	clientMux.RUnlock()

	if ok {
		return client, nil
	}

	credOpt, err := getCredentialsOption(cfg.Credentials)
	if err != nil {
		return nil, err
	}

	clientMux.Lock()
	defer clientMux.Unlock()

	ctx := context.Background()
	client, err = computeapiv1.NewInstancesRESTClient(ctx, credOpt)
	if err != nil {
		return nil, fmt.Errorf("failed to create compute client: %w", err)
	}

	computeClients[cacheKey] = client
	return client, nil
}

// getStorageClient returns a Storage client (cached)
func getStorageClient(cfg GCPConfig) (*storage.Client, error) {
	cacheKey := fmt.Sprintf("%s:%s", cfg.ProjectID, cfg.Credentials)

	clientMux.RLock()
	client, ok := storageClients[cacheKey]
	clientMux.RUnlock()

	if ok {
		return client, nil
	}

	credOpt, err := getCredentialsOption(cfg.Credentials)
	if err != nil {
		return nil, err
	}

	clientMux.Lock()
	defer clientMux.Unlock()

	ctx := context.Background()
	client, err = storage.NewClient(ctx, credOpt)
	if err != nil {
		return nil, fmt.Errorf("failed to create storage client: %w", err)
	}

	storageClients[cacheKey] = client
	return client, nil
}

// getPubsubClient returns a Pub/Sub client (cached)
func getPubsubClient(cfg GCPConfig) (*pubsub.Client, error) {
	cacheKey := fmt.Sprintf("%s:%s", cfg.ProjectID, cfg.Credentials)

	clientMux.RLock()
	client, ok := pubsubClients[cacheKey]
	clientMux.RUnlock()

	if ok {
		return client, nil
	}

	credOpt, err := getCredentialsOption(cfg.Credentials)
	if err != nil {
		return nil, err
	}

	clientMux.Lock()
	defer clientMux.Unlock()

	ctx := context.Background()
	client, err = pubsub.NewClient(ctx, cfg.ProjectID, credOpt)
	if err != nil {
		return nil, fmt.Errorf("failed to create pubsub client: %w", err)
	}

	pubsubClients[cacheKey] = client
	return client, nil
}

// getContainerClient returns a GKE Cluster Manager client (cached)
func getContainerClient(cfg GCPConfig) (*containerapiv1.ClusterManagerClient, error) {
	cacheKey := fmt.Sprintf("%s:%s", cfg.ProjectID, cfg.Credentials)

	clientMux.RLock()
	client, ok := containerClients[cacheKey]
	clientMux.RUnlock()

	if ok {
		return client, nil
	}

	credOpt, err := getCredentialsOption(cfg.Credentials)
	if err != nil {
		return nil, err
	}

	clientMux.Lock()
	defer clientMux.Unlock()

	ctx := context.Background()
	client, err = containerapiv1.NewClusterManagerClient(ctx, credOpt)
	if err != nil {
		return nil, fmt.Errorf("failed to create container client: %w", err)
	}

	containerClients[cacheKey] = client
	return client, nil
}

// getFunctionsClient returns a Cloud Functions client (cached)
func getFunctionsClient(cfg GCPConfig) (*functionsapiv1.CloudFunctionsClient, error) {
	cacheKey := fmt.Sprintf("%s:%s", cfg.ProjectID, cfg.Credentials)

	clientMux.RLock()
	client, ok := functionClients[cacheKey]
	clientMux.RUnlock()

	if ok {
		return client, nil
	}

	credOpt, err := getCredentialsOption(cfg.Credentials)
	if err != nil {
		return nil, err
	}

	clientMux.Lock()
	defer clientMux.Unlock()

	ctx := context.Background()
	client, err = functionsapiv1.NewCloudFunctionsClient(ctx, credOpt)
	if err != nil {
		return nil, fmt.Errorf("failed to create functions client: %w", err)
	}

	functionClients[cacheKey] = client
	return client, nil
}

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

// Helper to get bool from config
func getBool(config map[string]interface{}, key string, def bool) bool {
	if v, ok := config[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return def
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

// formatOperationError formats a Google API error
func formatOperationError(err error, operation string) string {
	if gerr, ok := err.(*googleapi.Error); ok {
		return fmt.Sprintf("%s failed: %s (code: %d)", operation, gerr.Message, gerr.Code)
	}
	return fmt.Sprintf("%s failed: %v", operation, err)
}

// ============================================================================
// SCHEMAS
// ============================================================================

// GCPInstanceListSchema is the UI schema for gcp-instance-list
var GCPInstanceListSchema = resolver.NewSchemaBuilder("gcp-instance-list").
	WithName("List GCP Instances").
	WithCategory("action").
	WithIcon(iconGCP).
	WithDescription("List all Compute Engine VM instances in your GCP project").
	AddSection("GCP Connection").
		AddExpressionField("projectId", "Project ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-gcp-project"),
			resolver.WithHint("GCP project ID"),
		).
		AddExpressionField("credentials", "Service Account Credentials",
			resolver.WithRequired(),
			resolver.WithSensitive(),
			resolver.WithHint("Service account JSON key content"),
		).
		EndSection().
	AddSection("Filters").
		AddExpressionField("zone", "Zone",
			resolver.WithPlaceholder("us-central1-a"),
			resolver.WithHint("Filter by zone (e.g., us-central1-a)"),
		).
		AddExpressionField("region", "Region",
			resolver.WithPlaceholder("us-central1"),
			resolver.WithHint("Filter by region"),
		).
		EndSection().
	Build()

// GCPInstanceStartSchema is the UI schema for gcp-instance-start
var GCPInstanceStartSchema = resolver.NewSchemaBuilder("gcp-instance-start").
	WithName("Start GCP Instance").
	WithCategory("action").
	WithIcon(iconGCP).
	WithDescription("Start a stopped Compute Engine instance").
	AddSection("GCP Connection").
		AddExpressionField("projectId", "Project ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-gcp-project"),
		).
		AddExpressionField("credentials", "Service Account Credentials",
			resolver.WithRequired(),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Instance").
		AddExpressionField("zone", "Zone",
			resolver.WithRequired(),
			resolver.WithPlaceholder("us-central1-a"),
			resolver.WithHint("Zone of the instance"),
		).
		AddExpressionField("instanceName", "Instance Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-instance"),
			resolver.WithHint("Name of the instance to start"),
		).
		EndSection().
	Build()

// GCPInstanceStopSchema is the UI schema for gcp-instance-stop
var GCPInstanceStopSchema = resolver.NewSchemaBuilder("gcp-instance-stop").
	WithName("Stop GCP Instance").
	WithCategory("action").
	WithIcon(iconGCP).
	WithDescription("Stop a running Compute Engine instance").
	AddSection("GCP Connection").
		AddExpressionField("projectId", "Project ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-gcp-project"),
		).
		AddExpressionField("credentials", "Service Account Credentials",
			resolver.WithRequired(),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Instance").
		AddExpressionField("zone", "Zone",
			resolver.WithRequired(),
			resolver.WithPlaceholder("us-central1-a"),
			resolver.WithHint("Zone of the instance"),
		).
		AddExpressionField("instanceName", "Instance Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-instance"),
			resolver.WithHint("Name of the instance to stop"),
		).
		EndSection().
	Build()

// GCPGKEListSchema is the UI schema for gcp-gke-list
var GCPGKEListSchema = resolver.NewSchemaBuilder("gcp-gke-list").
	WithName("List GKE Clusters").
	WithCategory("action").
	WithIcon(iconGCP).
	WithDescription("List all GKE clusters in your GCP project").
	AddSection("GCP Connection").
		AddExpressionField("projectId", "Project ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-gcp-project"),
		).
		AddExpressionField("credentials", "Service Account Credentials",
			resolver.WithRequired(),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Filters").
		AddExpressionField("region", "Region",
			resolver.WithPlaceholder("us-central1"),
			resolver.WithHint("Filter by region or zone"),
		).
		EndSection().
	Build()

// GCPGKEGetCredentialsSchema is the UI schema for gcp-gke-get-credentials
var GCPGKEGetCredentialsSchema = resolver.NewSchemaBuilder("gcp-gke-get-credentials").
	WithName("Get GKE Credentials").
	WithCategory("action").
	WithIcon(iconGCP).
	WithDescription("Get kubeconfig credentials for a GKE cluster").
	AddSection("GCP Connection").
		AddExpressionField("projectId", "Project ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-gcp-project"),
		).
		AddExpressionField("credentials", "Service Account Credentials",
			resolver.WithRequired(),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Cluster").
		AddExpressionField("region", "Region/Zone",
			resolver.WithRequired(),
			resolver.WithPlaceholder("us-central1"),
			resolver.WithHint("Cluster region or zone"),
		).
		AddExpressionField("clusterName", "Cluster Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-cluster"),
			resolver.WithHint("Name of the GKE cluster"),
		).
		EndSection().
	Build()

// GCPFunctionListSchema is the UI schema for gcp-function-list
var GCPFunctionListSchema = resolver.NewSchemaBuilder("gcp-function-list").
	WithName("List Cloud Functions").
	WithCategory("action").
	WithIcon(iconGCP).
	WithDescription("List all Cloud Functions in your GCP project").
	AddSection("GCP Connection").
		AddExpressionField("projectId", "Project ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-gcp-project"),
		).
		AddExpressionField("credentials", "Service Account Credentials",
			resolver.WithRequired(),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Filters").
		AddExpressionField("region", "Region",
			resolver.WithPlaceholder("us-central1"),
			resolver.WithHint("Filter by region"),
		).
		EndSection().
	Build()

// GCPFunctionDeploySchema is the UI schema for gcp-function-deploy
var GCPFunctionDeploySchema = resolver.NewSchemaBuilder("gcp-function-deploy").
	WithName("Deploy Cloud Function").
	WithCategory("action").
	WithIcon(iconGCP).
	WithDescription("Deploy a new Cloud Function or update an existing one").
	AddSection("GCP Connection").
		AddExpressionField("projectId", "Project ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-gcp-project"),
		).
		AddExpressionField("credentials", "Service Account Credentials",
			resolver.WithRequired(),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Function").
		AddExpressionField("region", "Region",
			resolver.WithRequired(),
			resolver.WithPlaceholder("us-central1"),
		).
		AddExpressionField("functionName", "Function Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-function"),
		).
		AddExpressionField("runtime", "Runtime",
			resolver.WithRequired(),
			resolver.WithPlaceholder("nodejs18"),
			resolver.WithHint("Runtime: nodejs18, python311, go121, etc."),
		).
		AddExpressionField("entryPoint", "Entry Point",
			resolver.WithRequired(),
			resolver.WithPlaceholder("handler"),
			resolver.WithHint("Function entry point name"),
		).
		EndSection().
	AddSection("Source").
		AddExpressionField("sourceUrl", "Source URL",
			resolver.WithPlaceholder("gs://my-bucket/function-source.zip"),
			resolver.WithHint("GCS URL of source archive"),
		).
		AddExpressionField("sourceDir", "Source Directory",
			resolver.WithPlaceholder("/path/to/source"),
			resolver.WithHint("Local source directory (for local deployment)"),
		).
		EndSection().
	AddSection("Options").
		AddToggleField("triggerHttp", "HTTP Trigger",
			resolver.WithDefault(true),
			resolver.WithHint("Enable HTTP trigger"),
		).
		AddExpressionField("timeout", "Timeout",
			resolver.WithDefault("60s"),
			resolver.WithPlaceholder("60s"),
			resolver.WithHint("Function timeout"),
		).
		AddNumberField("memoryMB", "Memory (MB)",
			resolver.WithDefault(256),
			resolver.WithHint("Memory allocation in MB"),
		).
		AddKeyValueField("environmentVariables", "Environment Variables",
			resolver.WithHint("Environment variables for the function"),
		).
		EndSection().
	Build()

// GCPFunctionInvokeSchema is the UI schema for gcp-function-invoke
var GCPFunctionInvokeSchema = resolver.NewSchemaBuilder("gcp-function-invoke").
	WithName("Invoke Cloud Function").
	WithCategory("action").
	WithIcon(iconGCP).
	WithDescription("Invoke a Cloud Function").
	AddSection("GCP Connection").
		AddExpressionField("projectId", "Project ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-gcp-project"),
		).
		AddExpressionField("credentials", "Service Account Credentials",
			resolver.WithRequired(),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Function").
		AddExpressionField("region", "Region",
			resolver.WithRequired(),
			resolver.WithPlaceholder("us-central1"),
		).
		AddExpressionField("functionName", "Function Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-function"),
		).
		EndSection().
	AddSection("Request").
		AddSelectField("method", "HTTP Method",
			[]resolver.SelectOption{
				{Label: "GET", Value: "GET"},
				{Label: "POST", Value: "POST"},
				{Label: "PUT", Value: "PUT"},
				{Label: "DELETE", Value: "DELETE"},
				{Label: "PATCH", Value: "PATCH"},
			},
			resolver.WithDefault("POST"),
		).
		AddJSONField("body", "Request Body",
			resolver.WithHeight(150),
			resolver.WithHint("JSON body to send"),
		).
		AddKeyValueField("headers", "Headers",
			resolver.WithHint("Custom HTTP headers"),
		).
		EndSection().
	Build()

// GCPPubSubPublishSchema is the UI schema for gcp-pubsub-publish
var GCPPubSubPublishSchema = resolver.NewSchemaBuilder("gcp-pubsub-publish").
	WithName("Publish to Pub/Sub").
	WithCategory("action").
	WithIcon(iconGCP).
	WithDescription("Publish a message to a Pub/Sub topic").
	AddSection("GCP Connection").
		AddExpressionField("projectId", "Project ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-gcp-project"),
		).
		AddExpressionField("credentials", "Service Account Credentials",
			resolver.WithRequired(),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Topic").
		AddExpressionField("topic", "Topic Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-topic"),
			resolver.WithHint("Pub/Sub topic name"),
		).
		EndSection().
	AddSection("Message").
		AddTextareaField("message", "Message Body",
			resolver.WithRequired(),
			resolver.WithRows(6),
			resolver.WithPlaceholder("Message content here..."),
			resolver.WithHint("Message body to publish"),
		).
		AddKeyValueField("attributes", "Attributes",
			resolver.WithHint("Message attributes (key-value pairs)"),
		).
		AddExpressionField("orderingKey", "Ordering Key",
			resolver.WithHint("Ordering key for ordered delivery"),
		).
		EndSection().
	Build()

// GCPPubSubSubscribeSchema is the UI schema for gcp-pubsub-subscribe
var GCPPubSubSubscribeSchema = resolver.NewSchemaBuilder("gcp-pubsub-subscribe").
	WithName("Subscribe to Pub/Sub").
	WithCategory("action").
	WithIcon(iconGCP).
	WithDescription("Pull messages from a Pub/Sub subscription").
	AddSection("GCP Connection").
		AddExpressionField("projectId", "Project ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-gcp-project"),
		).
		AddExpressionField("credentials", "Service Account Credentials",
			resolver.WithRequired(),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Subscription").
		AddExpressionField("subscription", "Subscription Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-subscription"),
			resolver.WithHint("Pub/Sub subscription name"),
		).
		EndSection().
	AddSection("Options").
		AddNumberField("maxMessages", "Max Messages",
			resolver.WithDefault(10),
			resolver.WithMinMax(1, 1000),
			resolver.WithHint("Maximum messages to pull"),
		).
		AddToggleField("ack", "Auto Acknowledge",
			resolver.WithDefault(true),
			resolver.WithHint("Automatically acknowledge messages after pulling"),
		).
		EndSection().
	Build()

// GCPStorageListSchema is the UI schema for gcp-storage-list
var GCPStorageListSchema = resolver.NewSchemaBuilder("gcp-storage-list").
	WithName("List Storage Buckets").
	WithCategory("action").
	WithIcon(iconGCP).
	WithDescription("List all Cloud Storage buckets in your GCP project").
	AddSection("GCP Connection").
		AddExpressionField("projectId", "Project ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-gcp-project"),
		).
		AddExpressionField("credentials", "Service Account Credentials",
			resolver.WithRequired(),
			resolver.WithSensitive(),
		).
		EndSection().
	Build()

// GCPStorageUploadSchema is the UI schema for gcp-storage-upload
var GCPStorageUploadSchema = resolver.NewSchemaBuilder("gcp-storage-upload").
	WithName("Upload to Cloud Storage").
	WithCategory("action").
	WithIcon(iconGCP).
	WithDescription("Upload a file to Cloud Storage").
	AddSection("GCP Connection").
		AddExpressionField("projectId", "Project ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-gcp-project"),
		).
		AddExpressionField("credentials", "Service Account Credentials",
			resolver.WithRequired(),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Bucket").
		AddExpressionField("bucket", "Bucket Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-bucket"),
			resolver.WithHint("Cloud Storage bucket name"),
		).
		AddExpressionField("objectName", "Object Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("path/to/file.txt"),
			resolver.WithHint("Destination object name (path)"),
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
			resolver.WithDefault("text/plain"),
			resolver.WithPlaceholder("text/plain"),
			resolver.WithHint("MIME type of the content"),
		).
		EndSection().
	Build()

// GCPStorageDownloadSchema is the UI schema for gcp-storage-download
var GCPStorageDownloadSchema = resolver.NewSchemaBuilder("gcp-storage-download").
	WithName("Download from Cloud Storage").
	WithCategory("action").
	WithIcon(iconGCP).
	WithDescription("Download a file from Cloud Storage").
	AddSection("GCP Connection").
		AddExpressionField("projectId", "Project ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-gcp-project"),
		).
		AddExpressionField("credentials", "Service Account Credentials",
			resolver.WithRequired(),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Bucket").
		AddExpressionField("bucket", "Bucket Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-bucket"),
			resolver.WithHint("Cloud Storage bucket name"),
		).
		AddExpressionField("objectName", "Object Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("path/to/file.txt"),
			resolver.WithHint("Object name (path) to download"),
		).
		EndSection().
	AddSection("Options").
		AddToggleField("base64Encode", "Base64 Encode",
			resolver.WithDefault(false),
			resolver.WithHint("Encode the content as base64 (useful for binary files)"),
		).
		EndSection().
	Build()

// ============================================================================
// COMPUTE ENGINE EXECUTORS
// ============================================================================

// GCPInstanceListExecutor handles gcp-instance-list node type
type GCPInstanceListExecutor struct{}

func (e *GCPInstanceListExecutor) Type() string { return "gcp-instance-list" }

func (e *GCPInstanceListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	cfg := parseGCPConfig(step.Config)
	zone := getString(step.Config, "zone")
	region := getString(step.Config, "region")

	client, err := getComputeClient(cfg)
	if err != nil {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": err.Error()},
		}, nil
	}

	var instances []map[string]interface{}

	if zone != "" {
		zoneReq := &computepb.ListInstancesRequest{
			Project: cfg.ProjectID,
			Zone:    zone,
		}
		it := client.List(ctx, zoneReq)
		for {
			inst, err := it.Next()
			if err == iterator.Done {
				break
			}
			if err != nil {
				return &executor.StepResult{
					Output: map[string]interface{}{"error": formatOperationError(err, "list instances")},
				}, nil
			}
			instances = append(instances, instanceToMap(inst))
		}
	} else {
		// Use aggregated list for all zones
		aggReq := &computepb.AggregatedListInstancesRequest{
			Project: cfg.ProjectID,
		}
		it := client.AggregatedList(ctx, aggReq)
		for {
			resp, err := it.Next()
			if err == iterator.Done {
				break
			}
			if err != nil {
				return &executor.StepResult{
					Output: map[string]interface{}{"error": formatOperationError(err, "list instances")},
				}, nil
			}
			if resp.Value != nil && resp.Value.Instances != nil {
				for _, inst := range resp.Value.Instances {
					// Filter by region if specified
					if region != "" && !strings.Contains(inst.GetZone(), region) {
						continue
					}
					instances = append(instances, instanceToMap(inst))
				}
			}
		}
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"instances": instances,
			"count":     len(instances),
		},
	}, nil
}

func instanceToMap(inst *computepb.Instance) map[string]interface{} {
	result := map[string]interface{}{
		"name":         "",
		"id":           "",
		"zone":         "",
		"machineType":  "",
		"status":       "",
		"creationTimestamp": "",
		"networkInterfaces": []map[string]interface{}{},
	}

	if inst.Name != nil {
		result["name"] = *inst.Name
	}
	if inst.Id != nil {
		result["id"] = fmt.Sprintf("%d", *inst.Id)
	}
	if inst.Zone != nil {
		// Extract zone name from full URL
		parts := strings.Split(*inst.Zone, "/")
		result["zone"] = parts[len(parts)-1]
	}
	if inst.MachineType != nil {
		parts := strings.Split(*inst.MachineType, "/")
		result["machineType"] = parts[len(parts)-1]
	}
	if inst.Status != nil {
		result["status"] = *inst.Status
	}
	if inst.CreationTimestamp != nil {
		result["creationTimestamp"] = *inst.CreationTimestamp
	}

	// Network interfaces
	if inst.NetworkInterfaces != nil {
		nics := make([]map[string]interface{}, 0, len(inst.NetworkInterfaces))
		for _, nic := range inst.NetworkInterfaces {
			nicMap := map[string]interface{}{
				"network":       "",
				"networkIP":     "",
				"accessConfigs": []map[string]interface{}{},
			}
			if nic.Network != nil {
				parts := strings.Split(*nic.Network, "/")
				nicMap["network"] = parts[len(parts)-1]
			}
			if nic.NetworkIP != nil {
				nicMap["networkIP"] = *nic.NetworkIP
			}
			if nic.AccessConfigs != nil {
				accessConfigs := make([]map[string]interface{}, 0, len(nic.AccessConfigs))
				for _, ac := range nic.AccessConfigs {
					acMap := map[string]interface{}{
						"type":  "",
						"natIP": "",
					}
					if ac.Type != nil {
						acMap["type"] = *ac.Type
					}
					if ac.NatIP != nil {
						acMap["natIP"] = *ac.NatIP
					}
					accessConfigs = append(accessConfigs, acMap)
				}
				nicMap["accessConfigs"] = accessConfigs
			}
			nics = append(nics, nicMap)
		}
		result["networkInterfaces"] = nics
	}

	return result
}

// GCPInstanceStartExecutor handles gcp-instance-start node type
type GCPInstanceStartExecutor struct{}

func (e *GCPInstanceStartExecutor) Type() string { return "gcp-instance-start" }

func (e *GCPInstanceStartExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	cfg := parseGCPConfig(step.Config)
	zone := getString(step.Config, "zone")
	instanceName := getString(step.Config, "instanceName")

	if zone == "" || instanceName == "" {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": "zone and instanceName are required"},
		}, nil
	}

	client, err := getComputeClient(cfg)
	if err != nil {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": err.Error()},
		}, nil
	}

	req := &computepb.StartInstanceRequest{
		Project:    cfg.ProjectID,
		Zone:       zone,
		Instance:   instanceName,
	}

	op, err := client.Start(ctx, req)
	if err != nil {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": formatOperationError(err, "start instance")},
		}, nil
	}

	// Wait for operation to complete
	if err := op.Wait(ctx); err != nil {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": formatOperationError(err, "start instance operation")},
		}, nil
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":      true,
			"instanceName": instanceName,
			"status":       "PROVISIONING",
			"message":      "Instance start operation completed successfully",
		},
	}, nil
}

// GCPInstanceStopExecutor handles gcp-instance-stop node type
type GCPInstanceStopExecutor struct{}

func (e *GCPInstanceStopExecutor) Type() string { return "gcp-instance-stop" }

func (e *GCPInstanceStopExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	cfg := parseGCPConfig(step.Config)
	zone := getString(step.Config, "zone")
	instanceName := getString(step.Config, "instanceName")

	if zone == "" || instanceName == "" {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": "zone and instanceName are required"},
		}, nil
	}

	client, err := getComputeClient(cfg)
	if err != nil {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": err.Error()},
		}, nil
	}

	req := &computepb.StopInstanceRequest{
		Project:  cfg.ProjectID,
		Zone:     zone,
		Instance: instanceName,
	}

	op, err := client.Stop(ctx, req)
	if err != nil {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": formatOperationError(err, "stop instance")},
		}, nil
	}

	// Wait for operation to complete
	if err := op.Wait(ctx); err != nil {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": formatOperationError(err, "stop instance operation")},
		}, nil
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":      true,
			"instanceName": instanceName,
			"status":       "TERMINATED",
			"message":      "Instance stop operation completed successfully",
		},
	}, nil
}

// ============================================================================
// GKE EXECUTORS
// ============================================================================

// GCPGKEListExecutor handles gcp-gke-list node type
type GCPGKEListExecutor struct{}

func (e *GCPGKEListExecutor) Type() string { return "gcp-gke-list" }

func (e *GCPGKEListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	cfg := parseGCPConfig(step.Config)
	region := getString(step.Config, "region")

	client, err := getContainerClient(cfg)
	if err != nil {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": err.Error()},
		}, nil
	}

	var clusters []map[string]interface{}

	// List clusters in all locations
	req := &containerpb.ListClustersRequest{
		Parent: fmt.Sprintf("projects/%s/locations/-", cfg.ProjectID),
	}

	resp, err := client.ListClusters(ctx, req)
	if err != nil {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": formatOperationError(err, "list clusters")},
		}, nil
	}

	for _, cluster := range resp.Clusters {
		// Filter by region if specified
		if region != "" && !strings.Contains(cluster.Location, region) {
			continue
		}
		clusters = append(clusters, clusterToMap(cluster))
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"clusters": clusters,
			"count":    len(clusters),
		},
	}, nil
}

func clusterToMap(cluster *containerpb.Cluster) map[string]interface{} {
	result := map[string]interface{}{
		"name":                 cluster.Name,
		"location":             cluster.Location,
		"initialNodeCount":     cluster.InitialNodeCount,
		"currentMasterVersion": cluster.CurrentMasterVersion,
		"status":               cluster.Status.String(),
		"endpoint":             "",
		"nodeCount":            cluster.CurrentNodeCount,
	}

	if cluster.Endpoint != "" {
		result["endpoint"] = cluster.Endpoint
	}

	// Node pools info
	if cluster.NodePools != nil {
		nodePools := make([]map[string]interface{}, 0, len(cluster.NodePools))
		for _, np := range cluster.NodePools {
			npMap := map[string]interface{}{
				"name":         np.Name,
				"initialCount": np.InitialNodeCount,
				"version":      np.Version,
				"status":       np.Status.String(),
			}
			if np.Autoscaling != nil {
				npMap["autoscaling"] = np.Autoscaling.Enabled
			}
			if np.Config != nil && np.Config.MachineType != "" {
				npMap["machineType"] = np.Config.MachineType
			}
			if np.Management != nil {
				npMap["autoRepair"] = np.Management.AutoRepair
				npMap["autoUpgrade"] = np.Management.AutoUpgrade
			}
			nodePools = append(nodePools, npMap)
		}
		result["nodePools"] = nodePools
	}

	return result
}

// GCPGKEGetCredentialsExecutor handles gcp-gke-get-credentials node type
type GCPGKEGetCredentialsExecutor struct{}

func (e *GCPGKEGetCredentialsExecutor) Type() string { return "gcp-gke-get-credentials" }

func (e *GCPGKEGetCredentialsExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	cfg := parseGCPConfig(step.Config)
	region := getString(step.Config, "region")
	clusterName := getString(step.Config, "clusterName")

	if region == "" || clusterName == "" {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": "region and clusterName are required"},
		}, nil
	}

	client, err := getContainerClient(cfg)
	if err != nil {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": err.Error()},
		}, nil
	}

	// Get cluster info
	clusterReq := &containerpb.GetClusterRequest{
		Name: fmt.Sprintf("projects/%s/locations/%s/clusters/%s", cfg.ProjectID, region, clusterName),
	}

	cluster, err := client.GetCluster(ctx, clusterReq)
	if err != nil {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": formatOperationError(err, "get cluster")},
		}, nil
	}

	// Generate kubeconfig content
	kubeconfig := generateKubeconfig(cfg, region, clusterName, cluster)

	clusterInfo := map[string]interface{}{
		"name":                 cluster.Name,
		"location":             cluster.Location,
		"endpoint":             cluster.Endpoint,
		"currentMasterVersion": cluster.CurrentMasterVersion,
		"status":               cluster.Status.String(),
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"kubeconfig":  kubeconfig,
			"clusterInfo": clusterInfo,
			"message":     fmt.Sprintf("Retrieved credentials for GKE cluster '%s'", clusterName),
		},
	}, nil
}

func generateKubeconfig(cfg GCPConfig, location, clusterName string, cluster *containerpb.Cluster) string {
	// Generate a basic kubeconfig YAML
	// In production, this would use proper certificate handling
	kubeconfig := fmt.Sprintf(`apiVersion: v1
kind: Config
clusters:
- name: %s
  cluster:
    server: https://%s
    certificate-authority-data: %s
contexts:
- name: %s
  context:
    cluster: %s
    user: %s
current-context: %s
users:
- name: %s
  user:
    exec:
      apiVersion: client.authentication.k8s.io/v1beta1
      command: gke-gcloud-auth-plugin
      args:
      - "--context=%s"
`,
		clusterName,
		cluster.Endpoint,
		base64.StdEncoding.EncodeToString([]byte(cluster.MasterAuth.GetClusterCaCertificate())),
		clusterName,
		clusterName,
		cfg.ProjectID,
		clusterName,
		cfg.ProjectID,
		fmt.Sprintf("gke_%s_%s_%s", cfg.ProjectID, location, clusterName),
	)

	return kubeconfig
}

// ============================================================================
// CLOUD FUNCTIONS EXECUTORS
// ============================================================================

// GCPFunctionListExecutor handles gcp-function-list node type
type GCPFunctionListExecutor struct{}

func (e *GCPFunctionListExecutor) Type() string { return "gcp-function-list" }

func (e *GCPFunctionListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	cfg := parseGCPConfig(step.Config)
	region := getString(step.Config, "region")

	client, err := getFunctionsClient(cfg)
	if err != nil {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": err.Error()},
		}, nil
	}

	var functions []map[string]interface{}

	// List all functions in the project
	parent := fmt.Sprintf("projects/%s/locations/-", cfg.ProjectID)
	if region != "" {
		parent = fmt.Sprintf("projects/%s/locations/%s", cfg.ProjectID, region)
	}

	req := &functionspb.ListFunctionsRequest{
		Parent: parent,
	}

	it := client.ListFunctions(ctx, req)
	for {
		fn, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return &executor.StepResult{
				Output: map[string]interface{}{"error": formatOperationError(err, "list functions")},
			}, nil
		}
		functions = append(functions, functionToMap(fn))
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"functions": functions,
			"count":     len(functions),
		},
	}, nil
}

func functionToMap(fn *functionspb.CloudFunction) map[string]interface{} {
	result := map[string]interface{}{
		"name":        "",
		"status":      "",
		"runtime":     "",
		"entryPoint":  "",
		"httpsUrl":    "",
		"region":      "",
		"updateTime":  "",
	}

	// Extract function name from full resource name
	// Format: projects/PROJECT_ID/locations/REGION/functions/FUNCTION_NAME
	parts := strings.Split(fn.Name, "/")
	if len(parts) >= 6 {
		result["name"] = parts[len(parts)-1]
		result["region"] = parts[len(parts)-3]
	}

	result["status"] = fn.Status.String()
	result["runtime"] = fn.Runtime
	result["entryPoint"] = fn.EntryPoint

	if trigger := fn.GetHttpsTrigger(); trigger != nil {
		result["httpsUrl"] = trigger.Url
	}

	if fn.UpdateTime != nil {
		result["updateTime"] = fn.UpdateTime.AsTime().Format(time.RFC3339)
	}

	return result
}

// GCPFunctionDeployExecutor handles gcp-function-deploy node type
type GCPFunctionDeployExecutor struct{}

func (e *GCPFunctionDeployExecutor) Type() string { return "gcp-function-deploy" }

func (e *GCPFunctionDeployExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	cfg := parseGCPConfig(step.Config)
	region := getString(step.Config, "region")
	functionName := getString(step.Config, "functionName")
	runtime := getString(step.Config, "runtime")
	entryPoint := getString(step.Config, "entryPoint")
	sourceUrl := getString(step.Config, "sourceUrl")
	sourceDir := getString(step.Config, "sourceDir")
	triggerHttp := getBool(step.Config, "triggerHttp", true)
	timeout := getString(step.Config, "timeout")
	memoryMB := getInt(step.Config, "memoryMB", 256)
	envVars := getMap(step.Config, "environmentVariables")

	if region == "" || functionName == "" || runtime == "" || entryPoint == "" {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": "region, functionName, runtime, and entryPoint are required"},
		}, nil
	}

	if sourceUrl == "" && sourceDir == "" {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": "sourceUrl or sourceDir is required"},
		}, nil
	}

	client, err := getFunctionsClient(cfg)
	if err != nil {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": err.Error()},
		}, nil
	}

	// Build the function configuration
	function := &functionspb.CloudFunction{
		Name:              fmt.Sprintf("projects/%s/locations/%s/functions/%s", cfg.ProjectID, region, functionName),
		Runtime:           runtime,
		EntryPoint:        entryPoint,
		AvailableMemoryMb: int32(memoryMB),
		Timeout: &durationpb.Duration{
			Seconds: int64(parseTimeout(timeout)),
		},
		EnvironmentVariables: convertEnvVars(envVars),
	}

	// Set source
	if sourceUrl != "" {
		function.SourceCode = &functionspb.CloudFunction_SourceArchiveUrl{
			SourceArchiveUrl: sourceUrl,
		}
	}

	// Set trigger
	if triggerHttp {
		function.Trigger = &functionspb.CloudFunction_HttpsTrigger{
			HttpsTrigger: &functionspb.HttpsTrigger{},
		}
	}

	// Create or update the function
	createReq := &functionspb.CreateFunctionRequest{
		Location: fmt.Sprintf("projects/%s/locations/%s", cfg.ProjectID, region),
		Function: function,
	}

	createOp, err := client.CreateFunction(ctx, createReq)
	if err != nil {
		// If already exists, try to update
		if strings.Contains(err.Error(), "already exists") {
			updateReq := &functionspb.UpdateFunctionRequest{
				Function: function,
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"runtime", "entryPoint", "sourceCode", "availableMemoryMb", "timeout", "environmentVariables"},
				},
			}
			updateOp, err := client.UpdateFunction(ctx, updateReq)
			if err != nil {
				return &executor.StepResult{
					Output: map[string]interface{}{"error": formatOperationError(err, "update function")},
				}, nil
			}
			// Wait for update operation
			resp, err := updateOp.Wait(ctx)
			if err != nil {
				return &executor.StepResult{
					Output: map[string]interface{}{"error": formatOperationError(err, "update function operation")},
				}, nil
			}
			httpsUrl := ""
			if resp.GetHttpsTrigger() != nil {
				httpsUrl = resp.GetHttpsTrigger().Url
			}
			return &executor.StepResult{
				Output: map[string]interface{}{
					"success":      true,
					"functionName": functionName,
					"region":       region,
					"httpsUrl":     httpsUrl,
					"status":       resp.Status.String(),
					"message":      fmt.Sprintf("Function '%s' updated successfully", functionName),
				},
			}, nil
		} else {
			return &executor.StepResult{
				Output: map[string]interface{}{"error": formatOperationError(err, "create function")},
			}, nil
		}
	}

	// Wait for create operation to complete
	resp, err := createOp.Wait(ctx)
	if err != nil {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": formatOperationError(err, "deploy function operation")},
		}, nil
	}

	httpsUrl := ""
	if resp.GetHttpsTrigger() != nil {
		httpsUrl = resp.GetHttpsTrigger().Url
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":      true,
			"functionName": functionName,
			"region":       region,
			"httpsUrl":     httpsUrl,
			"status":       resp.Status.String(),
			"message":      fmt.Sprintf("Function '%s' deployed successfully", functionName),
		},
	}, nil
}

func parseTimeout(timeout string) int32 {
	if timeout == "" {
		return 60
	}
	// Parse timeout like "60s", "1m", etc.
	if strings.HasSuffix(timeout, "s") {
		var seconds int
		fmt.Sscanf(timeout, "%d", &seconds)
		return int32(seconds)
	}
	if strings.HasSuffix(timeout, "m") {
		var minutes int
		fmt.Sscanf(timeout, "%d", &minutes)
		return int32(minutes * 60)
	}
	return 60
}

func convertEnvVars(envVars map[string]interface{}) map[string]string {
	result := make(map[string]string)
	for k, v := range envVars {
		if s, ok := v.(string); ok {
			result[k] = s
		}
	}
	return result
}

// GCPFunctionInvokeExecutor handles gcp-function-invoke node type
type GCPFunctionInvokeExecutor struct{}

func (e *GCPFunctionInvokeExecutor) Type() string { return "gcp-function-invoke" }

func (e *GCPFunctionInvokeExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	cfg := parseGCPConfig(step.Config)
	region := getString(step.Config, "region")
	functionName := getString(step.Config, "functionName")
	method := getString(step.Config, "method")
	body := getMap(step.Config, "body")
	headers := getMap(step.Config, "headers")

	if region == "" || functionName == "" {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": "region and functionName are required"},
		}, nil
	}

	if method == "" {
		method = "POST"
	}

	client, err := getFunctionsClient(cfg)
	if err != nil {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": err.Error()},
		}, nil
	}

	// Get function to retrieve URL
	name := fmt.Sprintf("projects/%s/locations/%s/functions/%s", cfg.ProjectID, region, functionName)
	getReq := &functionspb.GetFunctionRequest{
		Name: name,
	}

	fn, err := client.GetFunction(ctx, getReq)
	if err != nil {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": formatOperationError(err, "get function")},
		}, nil
	}

	httpsUrl := ""
	if trigger := fn.GetHttpsTrigger(); trigger != nil {
		httpsUrl = trigger.Url
	}

	if httpsUrl == "" {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": "function does not have an HTTPS trigger"},
		}, nil
	}

	// Prepare request body
	var bodyReader io.Reader
	if body != nil {
		bodyBytes, _ := json.Marshal(body)
		bodyReader = bytes.NewReader(bodyBytes)
	}

	// Create HTTP request
	req, err := http.NewRequest(method, httpsUrl, bodyReader)
	if err != nil {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": fmt.Sprintf("failed to create request: %v", err)},
		}, nil
	}

	req.Header.Set("Content-Type", "application/json")

	// Add custom headers
	if headers != nil {
		for k, v := range headers {
			if s, ok := v.(string); ok {
				req.Header.Set(k, s)
			}
		}
	}

	// Add authorization header using credentials
	if cfg.Credentials != "" {
		// In production, you would get a proper OAuth token
		// For now, we'll use the service account directly
		req.Header.Set("Authorization", "Bearer "+getAccessTokenFromCredentials(cfg.Credentials))
	}

	// Make the request
	httpClient := &http.Client{
		Timeout: 30 * time.Second,
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": fmt.Sprintf("failed to invoke function: %v", err)},
		}, nil
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": fmt.Sprintf("failed to read response: %v", err)},
		}, nil
	}

	// Try to parse response as JSON
	var respJSON interface{}
	if err := json.Unmarshal(respBody, &respJSON); err != nil {
		respJSON = string(respBody)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":      resp.StatusCode >= 200 && resp.StatusCode < 300,
			"statusCode":   resp.StatusCode,
			"body":         respJSON,
			"headers":      resp.Header,
			"functionName": functionName,
		},
	}, nil
}

func getAccessTokenFromCredentials(credentialsJSON string) string {
	// In production, this would use proper OAuth token exchange
	// For now, return a placeholder
	// The actual implementation would use golang.org/x/oauth2/google
	return "placeholder-token"
}

// ============================================================================
// PUB/SUB EXECUTORS
// ============================================================================

// GCPPubSubPublishExecutor handles gcp-pubsub-publish node type
type GCPPubSubPublishExecutor struct{}

func (e *GCPPubSubPublishExecutor) Type() string { return "gcp-pubsub-publish" }

func (e *GCPPubSubPublishExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	cfg := parseGCPConfig(step.Config)
	topicName := getString(step.Config, "topic")
	message := getString(step.Config, "message")
	attributes := getMap(step.Config, "attributes")
	orderingKey := getString(step.Config, "orderingKey")

	if topicName == "" || message == "" {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": "topic and message are required"},
		}, nil
	}

	client, err := getPubsubClient(cfg)
	if err != nil {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": err.Error()},
		}, nil
	}

	// Get or create topic
	topic := client.Topic(topicName)
	exists, err := topic.Exists(ctx)
	if err != nil {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": formatOperationError(err, "check topic existence")},
		}, nil
	}

	if !exists {
		// Create topic if it doesn't exist
		topic, err = client.CreateTopic(ctx, topicName)
		if err != nil {
			return &executor.StepResult{
				Output: map[string]interface{}{"error": formatOperationError(err, "create topic")},
			}, nil
		}
	}

	// Build the message
	pubsubMessage := &pubsub.Message{
		Data: []byte(message),
	}

	if attributes != nil {
		pubsubMessage.Attributes = make(map[string]string)
		for k, v := range attributes {
			if s, ok := v.(string); ok {
				pubsubMessage.Attributes[k] = s
			}
		}
	}

	if orderingKey != "" {
		pubsubMessage.OrderingKey = orderingKey
	}

	// Publish the message
	result := topic.Publish(ctx, pubsubMessage)

	// Wait for the result
	messageID, err := result.Get(ctx)
	if err != nil {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": formatOperationError(err, "publish message")},
		}, nil
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":   true,
			"messageId": messageID,
			"topic":     topicName,
			"message":   "Message published successfully",
		},
	}, nil
}

// GCPPubSubSubscribeExecutor handles gcp-pubsub-subscribe node type
type GCPPubSubSubscribeExecutor struct{}

func (e *GCPPubSubSubscribeExecutor) Type() string { return "gcp-pubsub-subscribe" }

func (e *GCPPubSubSubscribeExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	cfg := parseGCPConfig(step.Config)
	subscriptionName := getString(step.Config, "subscription")
	maxMessages := getInt(step.Config, "maxMessages", 10)
	autoAck := getBool(step.Config, "ack", true)

	if subscriptionName == "" {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": "subscription is required"},
		}, nil
	}

	client, err := getPubsubClient(cfg)
	if err != nil {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": err.Error()},
		}, nil
	}

	// Get subscription
	sub := client.Subscription(subscriptionName)
	exists, err := sub.Exists(ctx)
	if err != nil {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": formatOperationError(err, "check subscription existence")},
		}, nil
	}

	if !exists {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": fmt.Sprintf("subscription '%s' does not exist", subscriptionName)},
		}, nil
	}

	// Configure receive settings
	sub.ReceiveSettings.MaxOutstandingMessages = maxMessages

	// Pull messages
	var messages []map[string]interface{}
	var mu sync.Mutex
	var receivedCount int

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	err = sub.Receive(ctx, func(ctx context.Context, msg *pubsub.Message) {
		mu.Lock()
		defer mu.Unlock()

		msgMap := map[string]interface{}{
			"id":          msg.ID,
			"data":        string(msg.Data),
			"publishTime": msg.PublishTime.Format(time.RFC3339),
			"attributes":  msg.Attributes,
			"orderingKey": msg.OrderingKey,
		}

		messages = append(messages, msgMap)
		receivedCount++

		if autoAck {
			msg.Ack()
		}

		if receivedCount >= maxMessages {
			cancel()
		}
	})

	if err != nil && err != context.Canceled {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": formatOperationError(err, "receive messages")},
		}, nil
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"messages":    messages,
			"count":       len(messages),
			"subscription": subscriptionName,
		},
	}, nil
}

// ============================================================================
// CLOUD STORAGE EXECUTORS
// ============================================================================

// GCPStorageListExecutor handles gcp-storage-list node type
type GCPStorageListExecutor struct{}

func (e *GCPStorageListExecutor) Type() string { return "gcp-storage-list" }

func (e *GCPStorageListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	cfg := parseGCPConfig(step.Config)

	client, err := getStorageClient(cfg)
	if err != nil {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": err.Error()},
		}, nil
	}

	var buckets []map[string]interface{}

	it := client.Buckets(ctx, cfg.ProjectID)
	for {
		bucketAttrs, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return &executor.StepResult{
				Output: map[string]interface{}{"error": formatOperationError(err, "list buckets")},
			}, nil
		}
		buckets = append(buckets, bucketToMap(bucketAttrs))
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"buckets": buckets,
			"count":   len(buckets),
		},
	}, nil
}

func bucketToMap(bucket *storage.BucketAttrs) map[string]interface{} {
	result := map[string]interface{}{
		"name":          bucket.Name,
		"location":      bucket.Location,
		"locationType":  bucket.LocationType,
		"storageClass":  bucket.StorageClass,
		"created":       bucket.Created.Format(time.RFC3339),
		"versioning":    bucket.VersioningEnabled,
		"requesterPays": bucket.RequesterPays,
	}

	if !bucket.Created.IsZero() {
		result["created"] = bucket.Created.Format(time.RFC3339)
	}

	return result
}

// GCPStorageUploadExecutor handles gcp-storage-upload node type
type GCPStorageUploadExecutor struct{}

func (e *GCPStorageUploadExecutor) Type() string { return "gcp-storage-upload" }

func (e *GCPStorageUploadExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	cfg := parseGCPConfig(step.Config)
	bucket := getString(step.Config, "bucket")
	objectName := getString(step.Config, "objectName")
	content := getString(step.Config, "content")
	contentType := getString(step.Config, "contentType")
	base64Decode := getBool(step.Config, "base64Decode", false)

	if bucket == "" || objectName == "" || content == "" {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": "bucket, objectName, and content are required"},
		}, nil
	}

	if contentType == "" {
		contentType = "text/plain"
	}

	// Decode base64 if requested
	var contentBytes []byte
	if base64Decode {
		var err error
		contentBytes, err = base64.StdEncoding.DecodeString(content)
		if err != nil {
			return &executor.StepResult{
				Output: map[string]interface{}{"error": fmt.Sprintf("failed to decode base64: %v", err)},
			}, nil
		}
	} else {
		contentBytes = []byte(content)
	}

	client, err := getStorageClient(cfg)
	if err != nil {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": err.Error()},
		}, nil
	}

	// Get bucket handle
	obj := client.Bucket(bucket).Object(objectName)

	// Create writer
	w := obj.NewWriter(ctx)
	w.ContentType = contentType

	// Write content
	if _, err := w.Write(contentBytes); err != nil {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": formatOperationError(err, "write object")},
		}, nil
	}

	// Close the writer
	if err := w.Close(); err != nil {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": formatOperationError(err, "close object writer")},
		}, nil
	}

	// Get object attributes for response
	attrs, err := obj.Attrs(ctx)
	if err != nil {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": formatOperationError(err, "get object attributes")},
		}, nil
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":    true,
			"bucket":     bucket,
			"objectName": objectName,
			"size":       attrs.Size,
			"md5Hash":    attrs.MD5,
			"crc32c":     attrs.CRC32C,
			"mediaLink":  attrs.MediaLink,
			"message":    fmt.Sprintf("Uploaded '%s' to bucket '%s'", objectName, bucket),
		},
	}, nil
}

// GCPStorageDownloadExecutor handles gcp-storage-download node type
type GCPStorageDownloadExecutor struct{}

func (e *GCPStorageDownloadExecutor) Type() string { return "gcp-storage-download" }

func (e *GCPStorageDownloadExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	cfg := parseGCPConfig(step.Config)
	bucket := getString(step.Config, "bucket")
	objectName := getString(step.Config, "objectName")
	base64Encode := getBool(step.Config, "base64Encode", false)

	if bucket == "" || objectName == "" {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": "bucket and objectName are required"},
		}, nil
	}

	client, err := getStorageClient(cfg)
	if err != nil {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": err.Error()},
		}, nil
	}

	// Get bucket handle
	obj := client.Bucket(bucket).Object(objectName)

	// Get object attributes first
	attrs, err := obj.Attrs(ctx)
	if err != nil {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": formatOperationError(err, "get object attributes")},
		}, nil
	}

	// Create reader
	r, err := obj.NewReader(ctx)
	if err != nil {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": formatOperationError(err, "create object reader")},
		}, nil
	}
	defer r.Close()

	// Read content
	content, err := io.ReadAll(r)
	if err != nil {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": formatOperationError(err, "read object")},
		}, nil
	}

	// Encode as base64 if requested
	var contentStr string
	if base64Encode {
		contentStr = base64.StdEncoding.EncodeToString(content)
	} else {
		contentStr = string(content)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":     true,
			"bucket":      bucket,
			"objectName":  objectName,
			"contentType": attrs.ContentType,
			"size":        attrs.Size,
			"md5Hash":     attrs.MD5,
			"crc32c":      attrs.CRC32C,
			"content":     contentStr,
			"base64Encoded": base64Encode,
			"message":     fmt.Sprintf("Downloaded '%s' from bucket '%s'", objectName, bucket),
		},
	}, nil
}
