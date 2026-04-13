package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3Types "github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/axiom-studio/skills.sdk/executor"
	"github.com/axiom-studio/skills.sdk/grpc"
	"github.com/axiom-studio/skills.sdk/resolver"
)

const (
	iconS3 = "hard-drive"
)

// S3 client cache
var (
	s3Clients   = make(map[string]*s3.Client)
	s3ClientsMux sync.RWMutex
)

// AWSConfig holds AWS connection configuration
type AWSConfig struct {
	AccessKeyID     string
	SecretAccessKey string
	Region          string
	SessionToken    string
	Profile         string
	Endpoint        string // For S3-compatible services
	UsePathStyle    bool   // For S3-compatible services
}

// getS3Config returns an AWS config
func getS3Config(awsCfg AWSConfig) (aws.Config, error) {
	var opts []func(*config.LoadOptions) error

	if awsCfg.Region != "" {
		opts = append(opts, config.WithRegion(awsCfg.Region))
	}

	if awsCfg.Profile != "" && awsCfg.AccessKeyID == "" {
		opts = append(opts, config.WithSharedConfigProfile(awsCfg.Profile))
	}

	if awsCfg.AccessKeyID != "" && awsCfg.SecretAccessKey != "" {
		opts = append(opts, config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			awsCfg.AccessKeyID,
			awsCfg.SecretAccessKey,
			awsCfg.SessionToken,
		)))
	}

	// Load AWS config
	cfg, err := config.LoadDefaultConfig(context.Background(), opts...)
	if err != nil {
		return aws.Config{}, fmt.Errorf("failed to load AWS config: %w", err)
	}

	return cfg, nil
}

// getS3Client returns an S3 client (cached)
func getS3Client(awsCfg AWSConfig) (*s3.Client, error) {
	cacheKey := fmt.Sprintf("%s:%s:%s:%s", awsCfg.AccessKeyID, awsCfg.Region, awsCfg.Profile, awsCfg.Endpoint)

	s3ClientsMux.RLock()
	client, ok := s3Clients[cacheKey]
	s3ClientsMux.RUnlock()

	if ok {
		return client, nil
	}

	cfg, err := getS3Config(awsCfg)
	if err != nil {
		return nil, err
	}

	s3ClientsMux.Lock()
	defer s3ClientsMux.Unlock()

	// Apply custom endpoint if specified
	var s3Opts []func(*s3.Options)
	if awsCfg.Endpoint != "" {
		s3Opts = append(s3Opts, func(o *s3.Options) {
			o.BaseEndpoint = aws.String(awsCfg.Endpoint)
		})
	}
	if awsCfg.UsePathStyle {
		s3Opts = append(s3Opts, func(o *s3.Options) {
			o.UsePathStyle = true
		})
	}

	client = s3.NewFromConfig(cfg, s3Opts...)
	s3Clients[cacheKey] = client
	return client, nil
}

// parseAWSConfig extracts AWS configuration from config map
func parseAWSConfig(configMap map[string]interface{}) AWSConfig {
	return AWSConfig{
		AccessKeyID:     getString(configMap, "awsAccessKeyId"),
		SecretAccessKey: getString(configMap, "awsSecretAccessKey"),
		Region:          getString(configMap, "awsRegion"),
		SessionToken:    getString(configMap, "awsSessionToken"),
		Profile:         getString(configMap, "awsProfile"),
		Endpoint:        getString(configMap, "awsEndpoint"),
		UsePathStyle:    getBool(configMap, "awsUsePathStyle", false),
	}
}

// Helper to get string from config
func getString(configMap map[string]interface{}, key string) string {
	if v, ok := configMap[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// Helper to get int from config
func getInt(configMap map[string]interface{}, key string, def int) int {
	if v, ok := configMap[key]; ok {
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
func getBool(configMap map[string]interface{}, key string, def bool) bool {
	if v, ok := configMap[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return def
}

// Helper to get map from config
func getMap(configMap map[string]interface{}, key string) map[string]interface{} {
	if v, ok := configMap[key]; ok {
		if m, ok := v.(map[string]interface{}); ok {
			return m
		}
	}
	return nil
}

// Helper to get string slice from config
func getStringSlice(configMap map[string]interface{}, key string) []string {
	if v, ok := configMap[key]; ok {
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

func main() {
	// Get port from env or use default
	port := os.Getenv("SKILL_PORT")
	if port == "" {
		port = "50114"
	}

	// Create skill server
	server := grpc.NewSkillServer("skill-s3", "1.0.0")

	// Register S3 executors with schemas
	server.RegisterExecutorWithSchema("s3-list-buckets", &S3ListBucketsExecutor{}, S3ListBucketsSchema)
	server.RegisterExecutorWithSchema("s3-list-objects", &S3ListObjectsExecutor{}, S3ListObjectsSchema)
	server.RegisterExecutorWithSchema("s3-upload", &S3UploadExecutor{}, S3UploadSchema)
	server.RegisterExecutorWithSchema("s3-download", &S3DownloadExecutor{}, S3DownloadSchema)
	server.RegisterExecutorWithSchema("s3-delete", &S3DeleteExecutor{}, S3DeleteSchema)
	server.RegisterExecutorWithSchema("s3-presign", &S3PresignExecutor{}, S3PresignSchema)
	server.RegisterExecutorWithSchema("s3-bucket-create", &S3BucketCreateExecutor{}, S3BucketCreateSchema)
	server.RegisterExecutorWithSchema("s3-bucket-delete", &S3BucketDeleteExecutor{}, S3BucketDeleteSchema)
	server.RegisterExecutorWithSchema("s3-copy-object", &S3CopyObjectExecutor{}, S3CopyObjectSchema)

	fmt.Printf("Starting skill-s3 gRPC server on port %s\n", port)
	if err := server.Serve(port); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to serve: %v\n", err)
		os.Exit(1)
	}
}

// ============================================================================
// SCHEMAS
// ============================================================================

// S3ListBucketsSchema is the UI schema for s3-list-buckets
var S3ListBucketsSchema = resolver.NewSchemaBuilder("s3-list-buckets").
	WithName("List S3 Buckets").
	WithCategory("action").
	WithIcon(iconS3).
	WithDescription("List all S3 buckets in your AWS account").
	AddSection("AWS Connection").
		AddExpressionField("awsAccessKeyId", "Access Key ID",
			resolver.WithPlaceholder("AKIAIOSFODNN7EXAMPLE"),
			resolver.WithHint("AWS Access Key ID (optional if using IAM roles or AWS credentials file)"),
		).
		AddExpressionField("awsSecretAccessKey", "Secret Access Key",
			resolver.WithSensitive(),
			resolver.WithHint("AWS Secret Access Key (optional if using IAM roles or AWS credentials file)"),
		).
		AddExpressionField("awsRegion", "Region",
			resolver.WithPlaceholder("us-east-1"),
			resolver.WithHint("AWS region (e.g., us-east-1, eu-west-1)"),
		).
		AddExpressionField("awsProfile", "Profile",
			resolver.WithPlaceholder("default"),
			resolver.WithHint("AWS profile name from credentials file"),
		).
		AddExpressionField("awsEndpoint", "Custom Endpoint",
			resolver.WithPlaceholder("https://s3.amazonaws.com"),
			resolver.WithHint("Custom S3 endpoint URL (for S3-compatible services like MinIO)"),
		).
		AddToggleField("awsUsePathStyle", "Use Path Style",
			resolver.WithDefault(false),
			resolver.WithHint("Use path-style URLs (for S3-compatible services)"),
		).
		EndSection().
	Build()

// S3ListObjectsSchema is the UI schema for s3-list-objects
var S3ListObjectsSchema = resolver.NewSchemaBuilder("s3-list-objects").
	WithName("List S3 Objects").
	WithCategory("action").
	WithIcon(iconS3).
	WithDescription("List objects in an S3 bucket").
	AddSection("AWS Connection").
		AddExpressionField("awsAccessKeyId", "Access Key ID",
			resolver.WithPlaceholder("AKIAIOSFODNN7EXAMPLE"),
		).
		AddExpressionField("awsSecretAccessKey", "Secret Access Key",
			resolver.WithSensitive(),
		).
		AddExpressionField("awsRegion", "Region",
			resolver.WithPlaceholder("us-east-1"),
		).
		AddExpressionField("awsProfile", "Profile",
			resolver.WithPlaceholder("default"),
		).
		AddExpressionField("awsEndpoint", "Custom Endpoint",
			resolver.WithPlaceholder("https://s3.amazonaws.com"),
		).
		AddToggleField("awsUsePathStyle", "Use Path Style",
			resolver.WithDefault(false),
		).
		EndSection().
	AddSection("Bucket").
		AddExpressionField("bucket", "Bucket Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-bucket"),
			resolver.WithHint("Name of the S3 bucket"),
		).
		AddExpressionField("prefix", "Prefix",
			resolver.WithPlaceholder("folder/"),
			resolver.WithHint("Filter objects by prefix (folder path)"),
		).
		AddExpressionField("delimiter", "Delimiter",
			resolver.WithPlaceholder("/"),
			resolver.WithHint("Delimiter for grouping objects (e.g., / for folder-like structure)"),
		).
		EndSection().
	AddSection("Options").
		AddNumberField("limit", "Limit",
			resolver.WithDefault(1000),
			resolver.WithMinMax(1, 10000),
			resolver.WithHint("Maximum number of objects to return"),
		).
		AddExpressionField("continuationToken", "Continuation Token",
			resolver.WithHint("Token to continue listing from a previous request"),
		).
		EndSection().
	Build()

// S3UploadSchema is the UI schema for s3-upload
var S3UploadSchema = resolver.NewSchemaBuilder("s3-upload").
	WithName("Upload to S3").
	WithCategory("action").
	WithIcon(iconS3).
	WithDescription("Upload an object to an S3 bucket").
	AddSection("AWS Connection").
		AddExpressionField("awsAccessKeyId", "Access Key ID",
			resolver.WithPlaceholder("AKIAIOSFODNN7EXAMPLE"),
		).
		AddExpressionField("awsSecretAccessKey", "Secret Access Key",
			resolver.WithSensitive(),
		).
		AddExpressionField("awsRegion", "Region",
			resolver.WithPlaceholder("us-east-1"),
		).
		AddExpressionField("awsProfile", "Profile",
			resolver.WithPlaceholder("default"),
		).
		AddExpressionField("awsEndpoint", "Custom Endpoint",
			resolver.WithPlaceholder("https://s3.amazonaws.com"),
		).
		AddToggleField("awsUsePathStyle", "Use Path Style",
			resolver.WithDefault(false),
		).
		EndSection().
	AddSection("Object").
		AddExpressionField("bucket", "Bucket Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-bucket"),
			resolver.WithHint("Name of the S3 bucket"),
		).
		AddExpressionField("key", "Object Key",
			resolver.WithRequired(),
			resolver.WithPlaceholder("path/to/file.txt"),
			resolver.WithHint("Key (path) for the object"),
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
		AddExpressionField("contentEncoding", "Content Encoding",
			resolver.WithPlaceholder("gzip"),
			resolver.WithHint("Content encoding (e.g., gzip)"),
		).
		EndSection().
	AddSection("Metadata").
		AddKeyValueField("metadata", "Custom Metadata",
			resolver.WithHint("Custom metadata key-value pairs"),
		).
		AddExpressionField("cacheControl", "Cache Control",
			resolver.WithPlaceholder("max-age=3600"),
			resolver.WithHint("Cache-Control header value"),
		).
		AddExpressionField("acl", "ACL",
			resolver.WithPlaceholder("private"),
			resolver.WithHint("Canned ACL (private, public-read, public-read-write, authenticated-read)"),
		).
		AddSelectField("storageClass", "Storage Class",
			[]resolver.SelectOption{
				{Label: "Standard", Value: "STANDARD"},
				{Label: "Intelligent-Tiering", Value: "INTELLIGENT_TIERING"},
				{Label: "Standard-IA", Value: "STANDARD_IA"},
				{Label: "One Zone-IA", Value: "ONEZONE_IA"},
				{Label: "Glacier", Value: "GLACIER"},
				{Label: "Glacier Deep Archive", Value: "DEEP_ARCHIVE"},
				{Label: "Glacier Instant Retrieval", Value: "GLACIER_IR"},
			},
			resolver.WithDefault("STANDARD"),
			resolver.WithHint("S3 storage class"),
		).
		EndSection().
	Build()

// S3DownloadSchema is the UI schema for s3-download
var S3DownloadSchema = resolver.NewSchemaBuilder("s3-download").
	WithName("Download from S3").
	WithCategory("action").
	WithIcon(iconS3).
	WithDescription("Download an object from an S3 bucket").
	AddSection("AWS Connection").
		AddExpressionField("awsAccessKeyId", "Access Key ID",
			resolver.WithPlaceholder("AKIAIOSFODNN7EXAMPLE"),
		).
		AddExpressionField("awsSecretAccessKey", "Secret Access Key",
			resolver.WithSensitive(),
		).
		AddExpressionField("awsRegion", "Region",
			resolver.WithPlaceholder("us-east-1"),
		).
		AddExpressionField("awsProfile", "Profile",
			resolver.WithPlaceholder("default"),
		).
		AddExpressionField("awsEndpoint", "Custom Endpoint",
			resolver.WithPlaceholder("https://s3.amazonaws.com"),
		).
		AddToggleField("awsUsePathStyle", "Use Path Style",
			resolver.WithDefault(false),
		).
		EndSection().
	AddSection("Object").
		AddExpressionField("bucket", "Bucket Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-bucket"),
			resolver.WithHint("Name of the S3 bucket"),
		).
		AddExpressionField("key", "Object Key",
			resolver.WithRequired(),
			resolver.WithPlaceholder("path/to/file.txt"),
			resolver.WithHint("Key (path) of the object to retrieve"),
		).
		AddExpressionField("versionId", "Version ID",
			resolver.WithHint("Specific version ID to retrieve (for versioned buckets)"),
		).
		EndSection().
	AddSection("Options").
		AddToggleField("base64Encode", "Base64 Encode",
			resolver.WithDefault(false),
			resolver.WithHint("Encode the content as base64 (useful for binary files)"),
		).
		AddNumberField("maxSize", "Max Size (bytes)",
			resolver.WithDefault(10485760),
			resolver.WithMinMax(1, 1073741824),
			resolver.WithHint("Maximum object size to download (default: 10MB)"),
		).
		EndSection().
	Build()

// S3DeleteSchema is the UI schema for s3-delete
var S3DeleteSchema = resolver.NewSchemaBuilder("s3-delete").
	WithName("Delete S3 Object").
	WithCategory("action").
	WithIcon(iconS3).
	WithDescription("Delete an object from an S3 bucket").
	AddSection("AWS Connection").
		AddExpressionField("awsAccessKeyId", "Access Key ID",
			resolver.WithPlaceholder("AKIAIOSFODNN7EXAMPLE"),
		).
		AddExpressionField("awsSecretAccessKey", "Secret Access Key",
			resolver.WithSensitive(),
		).
		AddExpressionField("awsRegion", "Region",
			resolver.WithPlaceholder("us-east-1"),
		).
		AddExpressionField("awsProfile", "Profile",
			resolver.WithPlaceholder("default"),
		).
		AddExpressionField("awsEndpoint", "Custom Endpoint",
			resolver.WithPlaceholder("https://s3.amazonaws.com"),
		).
		AddToggleField("awsUsePathStyle", "Use Path Style",
			resolver.WithDefault(false),
		).
		EndSection().
	AddSection("Object").
		AddExpressionField("bucket", "Bucket Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-bucket"),
			resolver.WithHint("Name of the S3 bucket"),
		).
		AddExpressionField("key", "Object Key",
			resolver.WithRequired(),
			resolver.WithPlaceholder("path/to/file.txt"),
			resolver.WithHint("Key (path) of the object to delete"),
		).
		AddExpressionField("versionId", "Version ID",
			resolver.WithHint("Specific version ID to delete (for versioned buckets)"),
		).
		EndSection().
	Build()

// S3PresignSchema is the UI schema for s3-presign
var S3PresignSchema = resolver.NewSchemaBuilder("s3-presign").
	WithName("Generate Presigned URL").
	WithCategory("action").
	WithIcon(iconS3).
	WithDescription("Generate a presigned URL for S3 object access").
	AddSection("AWS Connection").
		AddExpressionField("awsAccessKeyId", "Access Key ID",
			resolver.WithPlaceholder("AKIAIOSFODNN7EXAMPLE"),
		).
		AddExpressionField("awsSecretAccessKey", "Secret Access Key",
			resolver.WithSensitive(),
		).
		AddExpressionField("awsRegion", "Region",
			resolver.WithPlaceholder("us-east-1"),
		).
		AddExpressionField("awsProfile", "Profile",
			resolver.WithPlaceholder("default"),
		).
		AddExpressionField("awsEndpoint", "Custom Endpoint",
			resolver.WithPlaceholder("https://s3.amazonaws.com"),
		).
		AddToggleField("awsUsePathStyle", "Use Path Style",
			resolver.WithDefault(false),
		).
		EndSection().
	AddSection("Object").
		AddExpressionField("bucket", "Bucket Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-bucket"),
			resolver.WithHint("Name of the S3 bucket"),
		).
		AddExpressionField("key", "Object Key",
			resolver.WithRequired(),
			resolver.WithPlaceholder("path/to/file.txt"),
			resolver.WithHint("Key (path) of the object"),
		).
		EndSection().
	AddSection("Options").
		AddSelectField("operation", "Operation",
			[]resolver.SelectOption{
				{Label: "Get Object (Download)", Value: "GetObject"},
				{Label: "Put Object (Upload)", Value: "PutObject"},
				{Label: "Delete Object", Value: "DeleteObject"},
			},
			resolver.WithDefault("GetObject"),
			resolver.WithHint("S3 operation for the presigned URL"),
		).
		AddNumberField("expiresIn", "Expiration (seconds)",
			resolver.WithDefault(3600),
			resolver.WithMinMax(1, 604800),
			resolver.WithHint("URL expiration time in seconds (max 7 days)"),
		).
		AddExpressionField("contentType", "Content Type",
			resolver.WithPlaceholder("text/plain"),
			resolver.WithHint("Content type for PutObject operations"),
		).
		EndSection().
	Build()

// S3BucketCreateSchema is the UI schema for s3-bucket-create
var S3BucketCreateSchema = resolver.NewSchemaBuilder("s3-bucket-create").
	WithName("Create S3 Bucket").
	WithCategory("action").
	WithIcon(iconS3).
	WithDescription("Create a new S3 bucket").
	AddSection("AWS Connection").
		AddExpressionField("awsAccessKeyId", "Access Key ID",
			resolver.WithPlaceholder("AKIAIOSFODNN7EXAMPLE"),
		).
		AddExpressionField("awsSecretAccessKey", "Secret Access Key",
			resolver.WithSensitive(),
		).
		AddExpressionField("awsRegion", "Region",
			resolver.WithRequired(),
			resolver.WithPlaceholder("us-east-1"),
			resolver.WithHint("AWS region where the bucket will be created"),
		).
		AddExpressionField("awsProfile", "Profile",
			resolver.WithPlaceholder("default"),
		).
		AddExpressionField("awsEndpoint", "Custom Endpoint",
			resolver.WithPlaceholder("https://s3.amazonaws.com"),
		).
		AddToggleField("awsUsePathStyle", "Use Path Style",
			resolver.WithDefault(false),
		).
		EndSection().
	AddSection("Bucket").
		AddExpressionField("bucket", "Bucket Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-new-bucket"),
			resolver.WithHint("Name of the S3 bucket to create (must be globally unique)"),
		).
		AddSelectField("acl", "ACL",
			[]resolver.SelectOption{
				{Label: "Private", Value: "private"},
				{Label: "Public Read", Value: "public-read"},
				{Label: "Public Read/Write", Value: "public-read-write"},
				{Label: "Authenticated Read", Value: "authenticated-read"},
			},
			resolver.WithDefault("private"),
			resolver.WithHint("Bucket ACL"),
		).
		AddSelectField("storageClass", "Storage Class",
			[]resolver.SelectOption{
				{Label: "Standard", Value: "STANDARD"},
				{Label: "Intelligent-Tiering", Value: "INTELLIGENT_TIERING"},
				{Label: "Standard-IA", Value: "STANDARD_IA"},
				{Label: "One Zone-IA", Value: "ONEZONE_IA"},
				{Label: "Glacier", Value: "GLACIER"},
				{Label: "Glacier Deep Archive", Value: "DEEP_ARCHIVE"},
			},
			resolver.WithDefault("STANDARD"),
			resolver.WithHint("Default storage class for objects"),
		).
		EndSection().
	AddSection("Options").
		AddToggleField("versioning", "Enable Versioning",
			resolver.WithDefault(false),
			resolver.WithHint("Enable versioning for the bucket"),
		).
		AddToggleField("blockPublicAccess", "Block Public Access",
			resolver.WithDefault(true),
			resolver.WithHint("Block all public access to the bucket"),
		).
		EndSection().
	Build()

// S3BucketDeleteSchema is the UI schema for s3-bucket-delete
var S3BucketDeleteSchema = resolver.NewSchemaBuilder("s3-bucket-delete").
	WithName("Delete S3 Bucket").
	WithCategory("action").
	WithIcon(iconS3).
	WithDescription("Delete an S3 bucket (must be empty first)").
	AddSection("AWS Connection").
		AddExpressionField("awsAccessKeyId", "Access Key ID",
			resolver.WithPlaceholder("AKIAIOSFODNN7EXAMPLE"),
		).
		AddExpressionField("awsSecretAccessKey", "Secret Access Key",
			resolver.WithSensitive(),
		).
		AddExpressionField("awsRegion", "Region",
			resolver.WithRequired(),
			resolver.WithPlaceholder("us-east-1"),
		).
		AddExpressionField("awsProfile", "Profile",
			resolver.WithPlaceholder("default"),
		).
		AddExpressionField("awsEndpoint", "Custom Endpoint",
			resolver.WithPlaceholder("https://s3.amazonaws.com"),
		).
		AddToggleField("awsUsePathStyle", "Use Path Style",
			resolver.WithDefault(false),
		).
		EndSection().
	AddSection("Bucket").
		AddExpressionField("bucket", "Bucket Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-bucket"),
			resolver.WithHint("Name of the S3 bucket to delete"),
		).
		EndSection().
	AddSection("Options").
		AddToggleField("force", "Force Delete",
			resolver.WithDefault(false),
			resolver.WithHint("Delete all objects before deleting the bucket"),
		).
		EndSection().
	Build()

// S3CopyObjectSchema is the UI schema for s3-copy-object
var S3CopyObjectSchema = resolver.NewSchemaBuilder("s3-copy-object").
	WithName("Copy S3 Object").
	WithCategory("action").
	WithIcon(iconS3).
	WithDescription("Copy an S3 object to a new location").
	AddSection("AWS Connection").
		AddExpressionField("awsAccessKeyId", "Access Key ID",
			resolver.WithPlaceholder("AKIAIOSFODNN7EXAMPLE"),
		).
		AddExpressionField("awsSecretAccessKey", "Secret Access Key",
			resolver.WithSensitive(),
		).
		AddExpressionField("awsRegion", "Region",
			resolver.WithPlaceholder("us-east-1"),
		).
		AddExpressionField("awsProfile", "Profile",
			resolver.WithPlaceholder("default"),
		).
		AddExpressionField("awsEndpoint", "Custom Endpoint",
			resolver.WithPlaceholder("https://s3.amazonaws.com"),
		).
		AddToggleField("awsUsePathStyle", "Use Path Style",
			resolver.WithDefault(false),
		).
		EndSection().
	AddSection("Source").
		AddExpressionField("sourceBucket", "Source Bucket",
			resolver.WithRequired(),
			resolver.WithPlaceholder("source-bucket"),
			resolver.WithHint("Name of the source bucket"),
		).
		AddExpressionField("sourceKey", "Source Key",
			resolver.WithRequired(),
			resolver.WithPlaceholder("path/to/source.txt"),
			resolver.WithHint("Key of the source object"),
		).
		AddExpressionField("sourceVersionId", "Source Version ID",
			resolver.WithHint("Version ID of the source object (for versioned buckets)"),
		).
		EndSection().
	AddSection("Destination").
		AddExpressionField("destinationBucket", "Destination Bucket",
			resolver.WithRequired(),
			resolver.WithPlaceholder("destination-bucket"),
			resolver.WithHint("Name of the destination bucket (same as source if not specified)"),
		).
		AddExpressionField("destinationKey", "Destination Key",
			resolver.WithRequired(),
			resolver.WithPlaceholder("path/to/destination.txt"),
			resolver.WithHint("Key for the copied object"),
		).
		EndSection().
	AddSection("Options").
		AddKeyValueField("metadata", "Custom Metadata",
			resolver.WithHint("Custom metadata (replaces existing metadata)"),
		).
		AddExpressionField("contentType", "Content Type",
			resolver.WithPlaceholder("text/plain"),
			resolver.WithHint("Content type for the copied object"),
		).
		AddExpressionField("acl", "ACL",
			resolver.WithPlaceholder("private"),
			resolver.WithHint("Canned ACL for the copied object"),
		).
		AddSelectField("storageClass", "Storage Class",
			[]resolver.SelectOption{
				{Label: "Standard", Value: "STANDARD"},
				{Label: "Intelligent-Tiering", Value: "INTELLIGENT_TIERING"},
				{Label: "Standard-IA", Value: "STANDARD_IA"},
				{Label: "One Zone-IA", Value: "ONEZONE_IA"},
				{Label: "Glacier", Value: "GLACIER"},
				{Label: "Glacier Deep Archive", Value: "DEEP_ARCHIVE"},
			},
			resolver.WithDefault("STANDARD"),
			resolver.WithHint("Storage class for the copied object"),
		).
		AddToggleField("replaceMetadata", "Replace Metadata",
			resolver.WithDefault(false),
			resolver.WithHint("Replace metadata on the copied object"),
		).
		EndSection().
	Build()

// ============================================================================
// S3 EXECUTORS
// ============================================================================

// S3ListBucketsExecutor handles s3-list-buckets node type
type S3ListBucketsExecutor struct{}

func (e *S3ListBucketsExecutor) Type() string { return "s3-list-buckets" }

func (e *S3ListBucketsExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	awsCfg := parseAWSConfig(step.Config)

	client, err := getS3Client(awsCfg)
	if err != nil {
		return &executor.StepResult{Output: map[string]interface{}{"error": err.Error()}}, nil
	}

	output, err := client.ListBuckets(ctx, &s3.ListBucketsInput{})
	if err != nil {
		return &executor.StepResult{Output: map[string]interface{}{"error": err.Error()}}, nil
	}

	buckets := make([]map[string]interface{}, 0, len(output.Buckets))
	for _, bucket := range output.Buckets {
		bucketInfo := map[string]interface{}{
			"name": aws.ToString(bucket.Name),
		}
		if bucket.CreationDate != nil {
			bucketInfo["creationDate"] = bucket.CreationDate.Format(time.RFC3339)
		}
		buckets = append(buckets, bucketInfo)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"buckets": buckets,
			"count":   len(buckets),
		},
	}, nil
}

// S3ListObjectsExecutor handles s3-list-objects node type
type S3ListObjectsExecutor struct{}

func (e *S3ListObjectsExecutor) Type() string { return "s3-list-objects" }

func (e *S3ListObjectsExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	awsCfg := parseAWSConfig(step.Config)
	bucket := getString(step.Config, "bucket")
	prefix := getString(step.Config, "prefix")
	delimiter := getString(step.Config, "delimiter")
	limit := getInt(step.Config, "limit", 1000)
	continuationToken := getString(step.Config, "continuationToken")

	if bucket == "" {
		return &executor.StepResult{Output: map[string]interface{}{"error": "bucket is required"}}, nil
	}

	client, err := getS3Client(awsCfg)
	if err != nil {
		return &executor.StepResult{Output: map[string]interface{}{"error": err.Error()}}, nil
	}

	var maxKeys int32 = int32(limit)
	input := &s3.ListObjectsV2Input{
		Bucket:  aws.String(bucket),
		MaxKeys: &maxKeys,
	}

	if prefix != "" {
		input.Prefix = aws.String(prefix)
	}
	if delimiter != "" {
		input.Delimiter = aws.String(delimiter)
	}
	if continuationToken != "" {
		input.ContinuationToken = aws.String(continuationToken)
	}

	output, err := client.ListObjectsV2(ctx, input)
	if err != nil {
		return &executor.StepResult{Output: map[string]interface{}{"error": err.Error()}}, nil
	}

	objects := make([]map[string]interface{}, 0, len(output.Contents))
	for _, obj := range output.Contents {
		objInfo := map[string]interface{}{
			"key":          aws.ToString(obj.Key),
			"size":         obj.Size,
			"lastModified": obj.LastModified.Format(time.RFC3339),
			"eTag":         strings.Trim(aws.ToString(obj.ETag), "\""),
			"storageClass": string(obj.StorageClass),
		}
		objects = append(objects, objInfo)
	}

	commonPrefixes := make([]string, 0, len(output.CommonPrefixes))
	for _, cp := range output.CommonPrefixes {
		commonPrefixes = append(commonPrefixes, aws.ToString(cp.Prefix))
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"objects":           objects,
			"commonPrefixes":    commonPrefixes,
			"count":             len(objects),
			"isTruncated":       output.IsTruncated,
			"nextContinuationToken": aws.ToString(output.NextContinuationToken),
			"prefix":            aws.ToString(output.Prefix),
			"delimiter":         aws.ToString(output.Delimiter),
		},
	}, nil
}

// S3UploadExecutor handles s3-upload node type
type S3UploadExecutor struct{}

func (e *S3UploadExecutor) Type() string { return "s3-upload" }

func (e *S3UploadExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	awsCfg := parseAWSConfig(step.Config)
	bucket := getString(step.Config, "bucket")
	key := getString(step.Config, "key")
	content := getString(step.Config, "content")
	contentType := getString(step.Config, "contentType")
	contentEncoding := getString(step.Config, "contentEncoding")
	base64Decode := getBool(step.Config, "base64Decode", false)
	cacheControl := getString(step.Config, "cacheControl")
	acl := getString(step.Config, "acl")
	storageClass := getString(step.Config, "storageClass")
	metadata := getMap(step.Config, "metadata")

	if bucket == "" {
		return &executor.StepResult{Output: map[string]interface{}{"error": "bucket is required"}}, nil
	}
	if key == "" {
		return &executor.StepResult{Output: map[string]interface{}{"error": "key is required"}}, nil
	}

	client, err := getS3Client(awsCfg)
	if err != nil {
		return &executor.StepResult{Output: map[string]interface{}{"error": err.Error()}}, nil
	}

	// Decode content if needed
	var contentBytes []byte
	if base64Decode {
		contentBytes, err = base64.StdEncoding.DecodeString(content)
		if err != nil {
			return &executor.StepResult{Output: map[string]interface{}{"error": fmt.Sprintf("failed to decode base64: %v", err)}}, nil
		}
	} else {
		contentBytes = []byte(content)
	}

	// Build PutObject input
	input := &s3.PutObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
		Body:   bytes.NewReader(contentBytes),
	}

	if contentType != "" {
		input.ContentType = aws.String(contentType)
	}
	if contentEncoding != "" {
		input.ContentEncoding = aws.String(contentEncoding)
	}
	if cacheControl != "" {
		input.CacheControl = aws.String(cacheControl)
	}
	if acl != "" {
		input.ACL = s3Types.ObjectCannedACL(acl)
	}
	if storageClass != "" {
		input.StorageClass = s3Types.StorageClass(storageClass)
	}
	if metadata != nil {
		input.Metadata = make(map[string]string)
		for k, v := range metadata {
			if s, ok := v.(string); ok {
				input.Metadata[k] = s
			}
		}
	}

	result, err := client.PutObject(ctx, input)
	if err != nil {
		return &executor.StepResult{Output: map[string]interface{}{"error": err.Error()}}, nil
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":   true,
			"bucket":    bucket,
			"key":       key,
			"eTag":      strings.Trim(aws.ToString(result.ETag), "\""),
			"versionId": aws.ToString(result.VersionId),
		},
	}, nil
}

// S3DownloadExecutor handles s3-download node type
type S3DownloadExecutor struct{}

func (e *S3DownloadExecutor) Type() string { return "s3-download" }

func (e *S3DownloadExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	awsCfg := parseAWSConfig(step.Config)
	bucket := getString(step.Config, "bucket")
	key := getString(step.Config, "key")
	versionId := getString(step.Config, "versionId")
	base64Encode := getBool(step.Config, "base64Encode", false)
	maxSize := getInt(step.Config, "maxSize", 10485760)

	if bucket == "" {
		return &executor.StepResult{Output: map[string]interface{}{"error": "bucket is required"}}, nil
	}
	if key == "" {
		return &executor.StepResult{Output: map[string]interface{}{"error": "key is required"}}, nil
	}

	client, err := getS3Client(awsCfg)
	if err != nil {
		return &executor.StepResult{Output: map[string]interface{}{"error": err.Error()}}, nil
	}

	input := &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	}
	if versionId != "" {
		input.VersionId = aws.String(versionId)
	}

	output, err := client.GetObject(ctx, input)
	if err != nil {
		return &executor.StepResult{Output: map[string]interface{}{"error": err.Error()}}, nil
	}
	defer output.Body.Close()

	// Read body with size limit
	var bodyBuffer bytes.Buffer
	limitedReader := io.LimitReader(output.Body, int64(maxSize)+1)
	n, err := io.Copy(&bodyBuffer, limitedReader)
	if err != nil {
		return &executor.StepResult{Output: map[string]interface{}{"error": fmt.Sprintf("failed to read object: %v", err)}}, nil
	}

	if n > int64(maxSize) {
		return &executor.StepResult{Output: map[string]interface{}{"error": fmt.Sprintf("object size (%d bytes) exceeds maximum allowed (%d bytes)", n, maxSize)}}, nil
	}

	contentBytes := bodyBuffer.Bytes()
	var content string
	if base64Encode {
		content = base64.StdEncoding.EncodeToString(contentBytes)
	} else {
		content = string(contentBytes)
	}

	metadata := make(map[string]string)
	for k, v := range output.Metadata {
		metadata[k] = v
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":       true,
			"bucket":        bucket,
			"key":           key,
			"content":       content,
			"contentType":   aws.ToString(output.ContentType),
			"contentLength": output.ContentLength,
			"eTag":          strings.Trim(aws.ToString(output.ETag), "\""),
			"versionId":     aws.ToString(output.VersionId),
			"lastModified":  output.LastModified.Format(time.RFC3339),
			"metadata":      metadata,
			"storageClass":  string(output.StorageClass),
		},
	}, nil
}

// S3DeleteExecutor handles s3-delete node type
type S3DeleteExecutor struct{}

func (e *S3DeleteExecutor) Type() string { return "s3-delete" }

func (e *S3DeleteExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	awsCfg := parseAWSConfig(step.Config)
	bucket := getString(step.Config, "bucket")
	key := getString(step.Config, "key")
	versionId := getString(step.Config, "versionId")

	if bucket == "" {
		return &executor.StepResult{Output: map[string]interface{}{"error": "bucket is required"}}, nil
	}
	if key == "" {
		return &executor.StepResult{Output: map[string]interface{}{"error": "key is required"}}, nil
	}

	client, err := getS3Client(awsCfg)
	if err != nil {
		return &executor.StepResult{Output: map[string]interface{}{"error": err.Error()}}, nil
	}

	input := &s3.DeleteObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	}
	if versionId != "" {
		input.VersionId = aws.String(versionId)
	}

	result, err := client.DeleteObject(ctx, input)
	if err != nil {
		return &executor.StepResult{Output: map[string]interface{}{"error": err.Error()}}, nil
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":       true,
			"bucket":        bucket,
			"key":           key,
			"versionId":     aws.ToString(result.VersionId),
			"deleteMarker":  result.DeleteMarker,
		},
	}, nil
}

// S3PresignExecutor handles s3-presign node type
type S3PresignExecutor struct{}

func (e *S3PresignExecutor) Type() string { return "s3-presign" }

func (e *S3PresignExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	awsCfg := parseAWSConfig(step.Config)
	bucket := getString(step.Config, "bucket")
	key := getString(step.Config, "key")
	operation := getString(step.Config, "operation")
	expiresIn := getInt(step.Config, "expiresIn", 3600)
	contentType := getString(step.Config, "contentType")

	if bucket == "" {
		return &executor.StepResult{Output: map[string]interface{}{"error": "bucket is required"}}, nil
	}
	if key == "" {
		return &executor.StepResult{Output: map[string]interface{}{"error": "key is required"}}, nil
	}

	client, err := getS3Client(awsCfg)
	if err != nil {
		return &executor.StepResult{Output: map[string]interface{}{"error": err.Error()}}, nil
	}

	presignClient := s3.NewPresignClient(client)

	var presignURL string
	var expires time.Duration = time.Duration(expiresIn) * time.Second

	switch operation {
	case "PutObject", "putobject":
		input := &s3.PutObjectInput{
			Bucket: aws.String(bucket),
			Key:    aws.String(key),
		}
		if contentType != "" {
			input.ContentType = aws.String(contentType)
		}
		presignReq, err := presignClient.PresignPutObject(ctx, input, s3.WithPresignExpires(expires))
		if err != nil {
			return &executor.StepResult{Output: map[string]interface{}{"error": err.Error()}}, nil
		}
		presignURL = presignReq.URL

	case "DeleteObject", "deleteobject":
		input := &s3.DeleteObjectInput{
			Bucket: aws.String(bucket),
			Key:    aws.String(key),
		}
		presignReq, err := presignClient.PresignDeleteObject(ctx, input, s3.WithPresignExpires(expires))
		if err != nil {
			return &executor.StepResult{Output: map[string]interface{}{"error": err.Error()}}, nil
		}
		presignURL = presignReq.URL

	case "GetObject", "getobject", "":
		input := &s3.GetObjectInput{
			Bucket: aws.String(bucket),
			Key:    aws.String(key),
		}
		presignReq, err := presignClient.PresignGetObject(ctx, input, s3.WithPresignExpires(expires))
		if err != nil {
			return &executor.StepResult{Output: map[string]interface{}{"error": err.Error()}}, nil
		}
		presignURL = presignReq.URL

	default:
		return &executor.StepResult{Output: map[string]interface{}{"error": fmt.Sprintf("unknown operation: %s", operation)}}, nil
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":   true,
			"url":       presignURL,
			"bucket":    bucket,
			"key":       key,
			"operation": operation,
			"expiresIn": expiresIn,
		},
	}, nil
}

// S3BucketCreateExecutor handles s3-bucket-create node type
type S3BucketCreateExecutor struct{}

func (e *S3BucketCreateExecutor) Type() string { return "s3-bucket-create" }

func (e *S3BucketCreateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	awsCfg := parseAWSConfig(step.Config)
	bucket := getString(step.Config, "bucket")
	acl := getString(step.Config, "acl")
	storageClass := getString(step.Config, "storageClass")
	enableVersioning := getBool(step.Config, "versioning", false)
	blockPublicAccess := getBool(step.Config, "blockPublicAccess", true)

	if bucket == "" {
		return &executor.StepResult{Output: map[string]interface{}{"error": "bucket is required"}}, nil
	}

	client, err := getS3Client(awsCfg)
	if err != nil {
		return &executor.StepResult{Output: map[string]interface{}{"error": err.Error()}}, nil
	}

	// Build CreateBucket input
	createInput := &s3.CreateBucketInput{
		Bucket: aws.String(bucket),
	}

	// Set ACL if specified
	if acl != "" {
		createInput.ACL = s3Types.BucketCannedACL(acl)
	}

	// Set location constraint for non-us-east-1 regions
	region := awsCfg.Region
	if region != "" && region != "us-east-1" {
		createInput.CreateBucketConfiguration = &s3Types.CreateBucketConfiguration{
			LocationConstraint: s3Types.BucketLocationConstraint(region),
		}
	}

	_, err = client.CreateBucket(ctx, createInput)
	if err != nil {
		return &executor.StepResult{Output: map[string]interface{}{"error": err.Error()}}, nil
	}

	// Set bucket versioning if enabled
	if enableVersioning {
		_, err = client.PutBucketVersioning(ctx, &s3.PutBucketVersioningInput{
			Bucket: aws.String(bucket),
			VersioningConfiguration: &s3Types.VersioningConfiguration{
				Status: s3Types.BucketVersioningStatusEnabled,
			},
		})
		if err != nil {
			return &executor.StepResult{Output: map[string]interface{}{"error": fmt.Sprintf("bucket created but failed to enable versioning: %v", err)}}, nil
		}
	}

	// Set block public access if enabled
	if blockPublicAccess {
		_, err = client.PutPublicAccessBlock(ctx, &s3.PutPublicAccessBlockInput{
			Bucket: aws.String(bucket),
			PublicAccessBlockConfiguration: &s3Types.PublicAccessBlockConfiguration{
				BlockPublicAcls:       aws.Bool(true),
				BlockPublicPolicy:     aws.Bool(true),
				IgnorePublicAcls:      aws.Bool(true),
				RestrictPublicBuckets: aws.Bool(true),
			},
		})
		if err != nil {
			return &executor.StepResult{Output: map[string]interface{}{"error": fmt.Sprintf("bucket created but failed to set public access block: %v", err)}}, nil
		}
	}

	// Set default storage class if specified
	if storageClass != "" && storageClass != "STANDARD" {
		// Note: Setting default storage class via lifecycle rules is complex
		// This is a simplified placeholder - in production you'd want proper lifecycle management
		_ = storageClass // acknowledged but not implemented in this simple version
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":          true,
			"bucket":           bucket,
			"region":           region,
			"versioning":       enableVersioning,
			"blockPublicAccess": blockPublicAccess,
		},
	}, nil
}

// S3BucketDeleteExecutor handles s3-bucket-delete node type
type S3BucketDeleteExecutor struct{}

func (e *S3BucketDeleteExecutor) Type() string { return "s3-bucket-delete" }

func (e *S3BucketDeleteExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	awsCfg := parseAWSConfig(step.Config)
	bucket := getString(step.Config, "bucket")
	force := getBool(step.Config, "force", false)

	if bucket == "" {
		return &executor.StepResult{Output: map[string]interface{}{"error": "bucket is required"}}, nil
	}

	client, err := getS3Client(awsCfg)
	if err != nil {
		return &executor.StepResult{Output: map[string]interface{}{"error": err.Error()}}, nil
	}

	// Force delete: remove all objects first
	if force {
		// List and delete all object versions
		listInput := &s3.ListObjectVersionsInput{
			Bucket: aws.String(bucket),
		}

		for {
			listOutput, err := client.ListObjectVersions(ctx, listInput)
			if err != nil {
				return &executor.StepResult{Output: map[string]interface{}{"error": fmt.Sprintf("failed to list objects: %v", err)}}, nil
			}

			// Delete all versions and delete markers
			var objectsToDelete []s3Types.ObjectIdentifier
			for _, obj := range listOutput.Versions {
				objectsToDelete = append(objectsToDelete, s3Types.ObjectIdentifier{
					Key:       obj.Key,
					VersionId: obj.VersionId,
				})
			}
			for _, dm := range listOutput.DeleteMarkers {
				objectsToDelete = append(objectsToDelete, s3Types.ObjectIdentifier{
					Key:       dm.Key,
					VersionId: dm.VersionId,
				})
			}

			if len(objectsToDelete) > 0 {
				_, err = client.DeleteObjects(ctx, &s3.DeleteObjectsInput{
					Bucket: aws.String(bucket),
					Delete: &s3Types.Delete{
						Objects: objectsToDelete,
						Quiet:   aws.Bool(true),
					},
				})
				if err != nil {
					return &executor.StepResult{Output: map[string]interface{}{"error": fmt.Sprintf("failed to delete objects: %v", err)}}, nil
				}
			}

			if listOutput.IsTruncated == nil || !*listOutput.IsTruncated {
				break
			}
			listInput.KeyMarker = listOutput.NextKeyMarker
			listInput.VersionIdMarker = listOutput.NextVersionIdMarker
		}
	}

	// Delete the bucket
	_, err = client.DeleteBucket(ctx, &s3.DeleteBucketInput{
		Bucket: aws.String(bucket),
	})
	if err != nil {
		return &executor.StepResult{Output: map[string]interface{}{"error": err.Error()}}, nil
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success": true,
			"bucket":  bucket,
		},
	}, nil
}

// S3CopyObjectExecutor handles s3-copy-object node type
type S3CopyObjectExecutor struct{}

func (e *S3CopyObjectExecutor) Type() string { return "s3-copy-object" }

func (e *S3CopyObjectExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	awsCfg := parseAWSConfig(step.Config)
	sourceBucket := getString(step.Config, "sourceBucket")
	sourceKey := getString(step.Config, "sourceKey")
	sourceVersionId := getString(step.Config, "sourceVersionId")
	destinationBucket := getString(step.Config, "destinationBucket")
	destinationKey := getString(step.Config, "destinationKey")
	contentType := getString(step.Config, "contentType")
	acl := getString(step.Config, "acl")
	storageClass := getString(step.Config, "storageClass")
	replaceMetadata := getBool(step.Config, "replaceMetadata", false)
	metadata := getMap(step.Config, "metadata")

	if sourceBucket == "" {
		return &executor.StepResult{Output: map[string]interface{}{"error": "sourceBucket is required"}}, nil
	}
	if sourceKey == "" {
		return &executor.StepResult{Output: map[string]interface{}{"error": "sourceKey is required"}}, nil
	}
	if destinationBucket == "" {
		destinationBucket = sourceBucket
	}
	if destinationKey == "" {
		return &executor.StepResult{Output: map[string]interface{}{"error": "destinationKey is required"}}, nil
	}

	client, err := getS3Client(awsCfg)
	if err != nil {
		return &executor.StepResult{Output: map[string]interface{}{"error": err.Error()}}, nil
	}

	// Build copy source
	copySource := url.PathEscape(fmt.Sprintf("%s/%s", sourceBucket, sourceKey))
	if sourceVersionId != "" {
		copySource = url.PathEscape(fmt.Sprintf("%s/%s?versionId=%s", sourceBucket, sourceKey, sourceVersionId))
	}

	input := &s3.CopyObjectInput{
		Bucket:     aws.String(destinationBucket),
		Key:        aws.String(destinationKey),
		CopySource: aws.String(copySource),
	}

	if contentType != "" {
		input.ContentType = aws.String(contentType)
	}
	if acl != "" {
		input.ACL = s3Types.ObjectCannedACL(acl)
	}
	if storageClass != "" {
		input.StorageClass = s3Types.StorageClass(storageClass)
	}
	if replaceMetadata {
		input.MetadataDirective = s3Types.MetadataDirectiveCopy
	}
	if metadata != nil {
		input.Metadata = make(map[string]string)
		for k, v := range metadata {
			if s, ok := v.(string); ok {
				input.Metadata[k] = s
			}
		}
	}

	result, err := client.CopyObject(ctx, input)
	if err != nil {
		return &executor.StepResult{Output: map[string]interface{}{"error": err.Error()}}, nil
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":          true,
			"sourceBucket":     sourceBucket,
			"sourceKey":        sourceKey,
			"destinationBucket": destinationBucket,
			"destinationKey":   destinationKey,
			"eTag":             strings.Trim(aws.ToString(result.CopyObjectResult.ETag), "\""),
			"versionId":        aws.ToString(result.VersionId),
			"lastModified":     result.CopyObjectResult.LastModified.Format(time.RFC3339),
		},
	}, nil
}
