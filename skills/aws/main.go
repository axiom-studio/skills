package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdaTypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/axiom-studio/skills.sdk/executor"
	"github.com/axiom-studio/skills.sdk/grpc"
	"github.com/axiom-studio/skills.sdk/resolver"
)

const (
	iconAWS = "cloud"
)

func main() {
	// Get port from env or use default
	port := os.Getenv("SKILL_PORT")
	if port == "" {
		port = "50057"
	}

	// Create skill server
	server := grpc.NewSkillServer("skill-aws", awsSkillVersion)

	// Register S3 executors with schemas
	server.RegisterExecutorWithSchema("aws-s3-list-buckets", &S3ListBucketsExecutor{}, S3ListBucketsSchema)
	server.RegisterExecutorWithSchema("aws-s3-list-objects", &S3ListObjectsExecutor{}, S3ListObjectsSchema)
	server.RegisterExecutorWithSchema("aws-s3-get-object", &S3GetObjectExecutor{}, S3GetObjectSchema)
	server.RegisterExecutorWithSchema("aws-s3-put-object", &S3PutObjectExecutor{}, S3PutObjectSchema)
	server.RegisterExecutorWithSchema("aws-s3-delete-object", &S3DeleteObjectExecutor{}, S3DeleteObjectSchema)

	// Register EC2 executors with schemas
	server.RegisterExecutorWithSchema("aws-ec2-list-instances", &EC2ListInstancesExecutor{}, EC2ListInstancesSchema)
	server.RegisterExecutorWithSchema("aws-ec2-start-instance", &EC2StartInstanceExecutor{}, EC2StartInstanceSchema)
	server.RegisterExecutorWithSchema("aws-ec2-stop-instance", &EC2StopInstanceExecutor{}, EC2StopInstanceSchema)
	server.RegisterExecutorWithSchema("aws-ec2-reboot-instance", &EC2RebootInstanceExecutor{}, EC2RebootInstanceSchema)

	// Register Lambda executors with schemas
	server.RegisterExecutorWithSchema("aws-lambda-list-functions", &LambdaListFunctionsExecutor{}, LambdaListFunctionsSchema)
	server.RegisterExecutorWithSchema("aws-lambda-invoke", &LambdaInvokeExecutor{}, LambdaInvokeSchema)
	server.RegisterExecutorWithSchema("aws-lambda-get-function", &LambdaGetFunctionExecutor{}, LambdaGetFunctionSchema)

	fmt.Printf("Starting skill-aws gRPC server on port %s\n", port)
	if err := server.Serve(port); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to serve: %v\n", err)
		os.Exit(1)
	}
}

// ============================================================================
// AWS CLIENT HELPERS
// ============================================================================

// The host binds the two fields of one AWS credential to every request. A
// shared runtime builds its AWS configuration from them alone: no profile,
// environment or instance-role fallback, and nothing cached across requests.
const (
	awsAccessKeyIDCredential     = "aws_access_key_id"
	awsSecretAccessKeyCredential = "aws_secret_access_key"
	awsDefaultRegion             = "us-east-1"
	awsSkillVersion              = "1.1.0"
)

var (
	awsRegionPattern = regexp.MustCompile(`^[a-z]{2}(-[a-z]+)+-[0-9]$`)
	// awsEndpointOverride redirects every service client in tests.
	awsEndpointOverride string
)

func boundAWSCredential(templateResolver executor.TemplateResolver, name string) string {
	bindings, ok := templateResolver.(executor.BindingResolver)
	if !ok {
		return ""
	}
	value, _ := bindings.GetBinding(name).(string)
	if strings.ContainsAny(value, "\r\n") {
		return ""
	}
	return strings.TrimSpace(value)
}

// parseAWSConfig builds the AWS configuration of one request.
func parseAWSConfig(step *executor.StepDefinition, templateResolver executor.TemplateResolver) (aws.Config, error) {
	accessKeyID := boundAWSCredential(templateResolver, awsAccessKeyIDCredential)
	secretAccessKey := boundAWSCredential(templateResolver, awsSecretAccessKeyCredential)
	if accessKeyID == "" || secretAccessKey == "" {
		return aws.Config{}, fmt.Errorf("connect AWS credentials (access key ID and secret access key) before using this action")
	}
	region := strings.TrimSpace(getString(step.Config, "region"))
	if region == "" {
		region = awsDefaultRegion
	}
	if !awsRegionPattern.MatchString(region) {
		return aws.Config{}, fmt.Errorf("region %q is not a valid AWS region", region)
	}
	cfg := aws.Config{
		Region:      region,
		Credentials: credentials.NewStaticCredentialsProvider(accessKeyID, secretAccessKey, ""),
		HTTPClient:  awsHTTPClient,
	}
	if awsEndpointOverride != "" {
		cfg.BaseEndpoint = aws.String(awsEndpointOverride)
	}
	return cfg, nil
}

var awsHTTPClient = awshttp.NewBuildableClient().WithTimeout(60 * time.Second)

func getS3Client(cfg aws.Config) (*s3.Client, error) {
	return s3.NewFromConfig(cfg, func(o *s3.Options) { o.UsePathStyle = awsEndpointOverride != "" }), nil
}

func getEC2Client(cfg aws.Config) (*ec2.Client, error) { return ec2.NewFromConfig(cfg), nil }

func getLambdaClient(cfg aws.Config) (*lambda.Client, error) { return lambda.NewFromConfig(cfg), nil }

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
			// Handle comma-separated string
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

// Helper to convert string to pointer
func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// Helper to convert int to pointer
func intPtr(i int) *int {
	return &i
}

// Helper to convert int32 to pointer
func int32Ptr(i int32) *int32 {
	return &i
}

// Helper to convert int64 to pointer
func int64Ptr(i int64) *int64 {
	return &i
}

// Helper to convert bool to pointer
func boolPtr(b bool) *bool {
	return &b
}

// ============================================================================
// SCHEMAS
// ============================================================================

// S3ListBucketsSchema is the UI schema for aws-s3-list-buckets
var S3ListBucketsSchema = resolver.NewSchemaBuilder("aws-s3-list-buckets").
	WithName("List S3 Buckets").
	WithCategory("action").
	WithIcon(iconAWS).
	WithDescription("List all S3 buckets in your AWS account").
	AddSection("AWS Region").
	AddExpressionField("region", "Region",
		resolver.WithPlaceholder("us-east-1"),
		resolver.WithHint("AWS region (e.g., us-east-1, eu-west-1)"),
	).
	EndSection().
	Build()

// S3ListObjectsSchema is the UI schema for aws-s3-list-objects
var S3ListObjectsSchema = resolver.NewSchemaBuilder("aws-s3-list-objects").
	WithName("List S3 Objects").
	WithCategory("action").
	WithIcon(iconAWS).
	WithDescription("List objects in an S3 bucket").
	AddSection("AWS Region").
	AddExpressionField("region", "Region",
		resolver.WithPlaceholder("us-east-1"),
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
	EndSection().
	Build()

// S3GetObjectSchema is the UI schema for aws-s3-get-object
var S3GetObjectSchema = resolver.NewSchemaBuilder("aws-s3-get-object").
	WithName("Get S3 Object").
	WithCategory("action").
	WithIcon(iconAWS).
	WithDescription("Download an object from an S3 bucket").
	AddSection("AWS Region").
	AddExpressionField("region", "Region",
		resolver.WithPlaceholder("us-east-1"),
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
		resolver.WithHint("Maximum object size to download (default: 10MB)"),
	).
	EndSection().
	Build()

// S3PutObjectSchema is the UI schema for aws-s3-put-object
var S3PutObjectSchema = resolver.NewSchemaBuilder("aws-s3-put-object").
	WithName("Put S3 Object").
	WithCategory("action").
	WithIcon(iconAWS).
	WithDescription("Upload an object to an S3 bucket").
	AddSection("AWS Region").
	AddExpressionField("region", "Region",
		resolver.WithPlaceholder("us-east-1"),
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
	EndSection().
	AddSection("Metadata").
	AddKeyValueField("metadata", "Custom Metadata",
		resolver.WithHint("Custom metadata key-value pairs"),
	).
	AddExpressionField("cacheControl", "Cache Control",
		resolver.WithPlaceholder("max-age=3600"),
		resolver.WithHint("Cache-Control header value"),
	).
	EndSection().
	Build()

// S3DeleteObjectSchema is the UI schema for aws-s3-delete-object
var S3DeleteObjectSchema = resolver.NewSchemaBuilder("aws-s3-delete-object").
	WithName("Delete S3 Object").
	WithCategory("action").
	WithIcon(iconAWS).
	WithDescription("Delete an object from an S3 bucket").
	AddSection("AWS Region").
	AddExpressionField("region", "Region",
		resolver.WithPlaceholder("us-east-1"),
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

// EC2ListInstancesSchema is the UI schema for aws-ec2-list-instances
var EC2ListInstancesSchema = resolver.NewSchemaBuilder("aws-ec2-list-instances").
	WithName("List EC2 Instances").
	WithCategory("action").
	WithIcon(iconAWS).
	WithDescription("List EC2 instances in your AWS account").
	AddSection("AWS Region").
	AddExpressionField("region", "Region",
		resolver.WithRequired(),
		resolver.WithPlaceholder("us-east-1"),
		resolver.WithHint("AWS region to list instances from"),
	).
	EndSection().
	AddSection("Filters").
	AddTagsField("instanceIds", "Instance IDs",
		resolver.WithHint("Filter by specific instance IDs"),
	).
	AddExpressionField("state", "State",
		resolver.WithPlaceholder("running, stopped, terminated"),
		resolver.WithHint("Filter by instance state"),
	).
	AddTagsField("tags", "Tags",
		resolver.WithHint("Filter by tags (format: key=value)"),
	).
	EndSection().
	AddSection("Options").
	AddNumberField("limit", "Limit",
		resolver.WithDefault(100),
		resolver.WithMinMax(1, 1000),
		resolver.WithHint("Maximum number of instances to return"),
	).
	EndSection().
	Build()

// EC2StartInstanceSchema is the UI schema for aws-ec2-start-instance
var EC2StartInstanceSchema = resolver.NewSchemaBuilder("aws-ec2-start-instance").
	WithName("Start EC2 Instance").
	WithCategory("action").
	WithIcon(iconAWS).
	WithDescription("Start a stopped EC2 instance").
	AddSection("AWS Region").
	AddExpressionField("region", "Region",
		resolver.WithRequired(),
		resolver.WithPlaceholder("us-east-1"),
	).
	EndSection().
	AddSection("Instance").
	AddExpressionField("instanceId", "Instance ID",
		resolver.WithRequired(),
		resolver.WithPlaceholder("i-1234567890abcdef0"),
		resolver.WithHint("ID of the EC2 instance to start"),
	).
	EndSection().
	Build()

// EC2StopInstanceSchema is the UI schema for aws-ec2-stop-instance
var EC2StopInstanceSchema = resolver.NewSchemaBuilder("aws-ec2-stop-instance").
	WithName("Stop EC2 Instance").
	WithCategory("action").
	WithIcon(iconAWS).
	WithDescription("Stop a running EC2 instance").
	AddSection("AWS Region").
	AddExpressionField("region", "Region",
		resolver.WithRequired(),
		resolver.WithPlaceholder("us-east-1"),
	).
	EndSection().
	AddSection("Instance").
	AddExpressionField("instanceId", "Instance ID",
		resolver.WithRequired(),
		resolver.WithPlaceholder("i-1234567890abcdef0"),
		resolver.WithHint("ID of the EC2 instance to stop"),
	).
	EndSection().
	AddSection("Options").
	AddToggleField("force", "Force Stop",
		resolver.WithDefault(false),
		resolver.WithHint("Force stop the instance (like pulling the power cord)"),
	).
	AddToggleField("hibernate", "Hibernate",
		resolver.WithDefault(false),
		resolver.WithHint("Hibernate the instance instead of stopping"),
	).
	EndSection().
	Build()

// EC2RebootInstanceSchema is the UI schema for aws-ec2-reboot-instance
var EC2RebootInstanceSchema = resolver.NewSchemaBuilder("aws-ec2-reboot-instance").
	WithName("Reboot EC2 Instance").
	WithCategory("action").
	WithIcon(iconAWS).
	WithDescription("Reboot a running EC2 instance").
	AddSection("AWS Region").
	AddExpressionField("region", "Region",
		resolver.WithRequired(),
		resolver.WithPlaceholder("us-east-1"),
	).
	EndSection().
	AddSection("Instance").
	AddExpressionField("instanceId", "Instance ID",
		resolver.WithRequired(),
		resolver.WithPlaceholder("i-1234567890abcdef0"),
		resolver.WithHint("ID of the EC2 instance to reboot"),
	).
	EndSection().
	Build()

// LambdaListFunctionsSchema is the UI schema for aws-lambda-list-functions
var LambdaListFunctionsSchema = resolver.NewSchemaBuilder("aws-lambda-list-functions").
	WithName("List Lambda Functions").
	WithCategory("action").
	WithIcon(iconAWS).
	WithDescription("List Lambda functions in your AWS account").
	AddSection("AWS Region").
	AddExpressionField("region", "Region",
		resolver.WithRequired(),
		resolver.WithPlaceholder("us-east-1"),
	).
	EndSection().
	AddSection("Options").
	AddNumberField("limit", "Limit",
		resolver.WithDefault(50),
		resolver.WithMinMax(1, 10000),
		resolver.WithHint("Maximum number of functions to return"),
	).
	EndSection().
	Build()

// LambdaInvokeSchema is the UI schema for aws-lambda-invoke
var LambdaInvokeSchema = resolver.NewSchemaBuilder("aws-lambda-invoke").
	WithName("Invoke Lambda Function").
	WithCategory("action").
	WithIcon(iconAWS).
	WithDescription("Invoke a Lambda function").
	AddSection("AWS Region").
	AddExpressionField("region", "Region",
		resolver.WithRequired(),
		resolver.WithPlaceholder("us-east-1"),
	).
	EndSection().
	AddSection("Function").
	AddExpressionField("functionName", "Function Name",
		resolver.WithRequired(),
		resolver.WithPlaceholder("my-function"),
		resolver.WithHint("Name or ARN of the Lambda function"),
	).
	AddExpressionField("qualifier", "Qualifier",
		resolver.WithPlaceholder("$LATEST or version number"),
		resolver.WithHint("Version or alias to invoke"),
	).
	EndSection().
	AddSection("Payload").
	AddJSONField("payload", "Payload",
		resolver.WithHeight(150),
		resolver.WithHint("JSON payload to pass to the function"),
	).
	EndSection().
	AddSection("Options").
	AddSelectField("invocationType", "Invocation Type",
		[]resolver.SelectOption{
			{Label: "RequestResponse", Value: "RequestResponse"},
			{Label: "Event (Async)", Value: "Event"},
			{Label: "DryRun", Value: "DryRun"},
		},
		resolver.WithDefault("RequestResponse"),
		resolver.WithHint("Invocation type: RequestResponse (sync), Event (async), or DryRun"),
	).
	EndSection().
	Build()

// LambdaGetFunctionSchema is the UI schema for aws-lambda-get-function
var LambdaGetFunctionSchema = resolver.NewSchemaBuilder("aws-lambda-get-function").
	WithName("Get Lambda Function").
	WithCategory("action").
	WithIcon(iconAWS).
	WithDescription("Get details about a Lambda function").
	AddSection("AWS Region").
	AddExpressionField("region", "Region",
		resolver.WithRequired(),
		resolver.WithPlaceholder("us-east-1"),
	).
	EndSection().
	AddSection("Function").
	AddExpressionField("functionName", "Function Name",
		resolver.WithRequired(),
		resolver.WithPlaceholder("my-function"),
		resolver.WithHint("Name or ARN of the Lambda function"),
	).
	AddExpressionField("qualifier", "Qualifier",
		resolver.WithPlaceholder("$LATEST or version number"),
		resolver.WithHint("Version or alias to retrieve"),
	).
	EndSection().
	Build()

// ============================================================================
// S3 EXECUTORS
// ============================================================================

// S3ListBucketsExecutor handles aws-s3-list-buckets node type
type S3ListBucketsExecutor struct{}

func (e *S3ListBucketsExecutor) Type() string { return "aws-s3-list-buckets" }

func (e *S3ListBucketsExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	awsCfg, err := parseAWSConfig(step, templateResolver)
	if err != nil {
		return nil, err
	}

	client, err := getS3Client(awsCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create S3 client: %w", err)
	}

	result, err := client.ListBuckets(ctx, &s3.ListBucketsInput{})
	if err != nil {
		return nil, fmt.Errorf("failed to list buckets: %w", err)
	}

	buckets := make([]map[string]interface{}, 0, len(result.Buckets))
	for _, bucket := range result.Buckets {
		bucketInfo := map[string]interface{}{
			"name":         aws.ToString(bucket.Name),
			"creationDate": bucket.CreationDate.Format(time.RFC3339),
		}
		buckets = append(buckets, bucketInfo)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success": true,
			"buckets": buckets,
			"count":   len(buckets),
		},
	}, nil
}

// S3ListObjectsExecutor handles aws-s3-list-objects node type
type S3ListObjectsExecutor struct{}

func (e *S3ListObjectsExecutor) Type() string { return "aws-s3-list-objects" }

func (e *S3ListObjectsExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	awsCfg, err := parseAWSConfig(step, templateResolver)
	if err != nil {
		return nil, err
	}
	bucket := getString(step.Config, "bucket")
	prefix := getString(step.Config, "prefix")
	delimiter := getString(step.Config, "delimiter")
	limit := getInt(step.Config, "limit", 1000)

	if bucket == "" {
		return nil, fmt.Errorf("bucket name is required")
	}

	client, err := getS3Client(awsCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create S3 client: %w", err)
	}

	input := &s3.ListObjectsV2Input{
		Bucket: aws.String(bucket),
	}

	if prefix != "" {
		input.Prefix = aws.String(prefix)
	}
	if delimiter != "" {
		input.Delimiter = aws.String(delimiter)
	}

	objects := make([]map[string]interface{}, 0)
	var continuationToken *string

	for len(objects) < limit {
		input.MaxKeys = aws.Int32(int32(limit - len(objects)))
		if continuationToken != nil {
			input.ContinuationToken = continuationToken
		}

		result, err := client.ListObjectsV2(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("failed to list objects: %w", err)
		}

		for _, obj := range result.Contents {
			objects = append(objects, map[string]interface{}{
				"key":          aws.ToString(obj.Key),
				"size":         obj.Size,
				"lastModified": obj.LastModified.Format(time.RFC3339),
				"etag":         strings.Trim(aws.ToString(obj.ETag), "\""),
				"storageClass": string(obj.StorageClass),
			})
		}

		// Check for common prefixes (folders)
		for _, prefix := range result.CommonPrefixes {
			objects = append(objects, map[string]interface{}{
				"key":          aws.ToString(prefix.Prefix),
				"size":         0,
				"type":         "prefix",
				"lastModified": "",
			})
		}

		if result.IsTruncated == nil || !*result.IsTruncated {
			break
		}
		continuationToken = result.NextContinuationToken
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success": true,
			"bucket":  bucket,
			"prefix":  prefix,
			"objects": objects,
			"count":   len(objects),
		},
	}, nil
}

// S3GetObjectExecutor handles aws-s3-get-object node type
type S3GetObjectExecutor struct{}

func (e *S3GetObjectExecutor) Type() string { return "aws-s3-get-object" }

func (e *S3GetObjectExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	awsCfg, err := parseAWSConfig(step, templateResolver)
	if err != nil {
		return nil, err
	}
	bucket := getString(step.Config, "bucket")
	key := getString(step.Config, "key")
	versionID := getString(step.Config, "versionId")
	base64Encode := getBool(step.Config, "base64Encode", false)
	maxSize := getInt(step.Config, "maxSize", 10485760)

	if bucket == "" {
		return nil, fmt.Errorf("bucket name is required")
	}
	if key == "" {
		return nil, fmt.Errorf("object key is required")
	}

	client, err := getS3Client(awsCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create S3 client: %w", err)
	}

	input := &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	}

	if versionID != "" {
		input.VersionId = aws.String(versionID)
	}

	result, err := client.GetObject(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("failed to get object: %w", err)
	}
	defer result.Body.Close()

	// Check size
	if result.ContentLength != nil && *result.ContentLength > int64(maxSize) {
		return nil, fmt.Errorf("object size %d exceeds maximum allowed size %d", *result.ContentLength, maxSize)
	}

	// Read content
	buf := new(bytes.Buffer)
	_, err = buf.ReadFrom(result.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read object content: %w", err)
	}

	content := buf.Bytes()
	var contentStr string
	if base64Encode {
		contentStr = base64.StdEncoding.EncodeToString(content)
	} else {
		contentStr = string(content)
	}

	output := map[string]interface{}{
		"success":      true,
		"bucket":       bucket,
		"key":          key,
		"content":      contentStr,
		"size":         len(content),
		"contentType":  aws.ToString(result.ContentType),
		"lastModified": result.LastModified.Format(time.RFC3339),
		"etag":         strings.Trim(aws.ToString(result.ETag), "\""),
	}

	if versionID != "" {
		output["versionId"] = versionID
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// S3PutObjectExecutor handles aws-s3-put-object node type
type S3PutObjectExecutor struct{}

func (e *S3PutObjectExecutor) Type() string { return "aws-s3-put-object" }

func (e *S3PutObjectExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	awsCfg, err := parseAWSConfig(step, templateResolver)
	if err != nil {
		return nil, err
	}
	bucket := getString(step.Config, "bucket")
	key := getString(step.Config, "key")
	content := getString(step.Config, "content")
	base64Decode := getBool(step.Config, "base64Decode", false)
	contentType := getString(step.Config, "contentType")
	cacheControl := getString(step.Config, "cacheControl")
	metadata := getMap(step.Config, "metadata")

	if bucket == "" {
		return nil, fmt.Errorf("bucket name is required")
	}
	if key == "" {
		return nil, fmt.Errorf("object key is required")
	}
	if content == "" {
		return nil, fmt.Errorf("content is required")
	}

	client, err := getS3Client(awsCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create S3 client: %w", err)
	}

	var contentBytes []byte
	if base64Decode {
		decoded, err := base64.StdEncoding.DecodeString(content)
		if err != nil {
			return nil, fmt.Errorf("failed to decode base64 content: %w", err)
		}
		contentBytes = decoded
	} else {
		contentBytes = []byte(content)
	}

	input := &s3.PutObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
		Body:   bytes.NewReader(contentBytes),
	}

	if contentType != "" {
		input.ContentType = aws.String(contentType)
	}
	if cacheControl != "" {
		input.CacheControl = aws.String(cacheControl)
	}
	if len(metadata) > 0 {
		input.Metadata = make(map[string]string)
		for k, v := range metadata {
			if s, ok := v.(string); ok {
				input.Metadata[k] = s
			}
		}
	}

	result, err := client.PutObject(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("failed to put object: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":   true,
			"bucket":    bucket,
			"key":       key,
			"etag":      strings.Trim(aws.ToString(result.ETag), "\""),
			"versionId": aws.ToString(result.VersionId),
			"size":      len(contentBytes),
		},
	}, nil
}

// S3DeleteObjectExecutor handles aws-s3-delete-object node type
type S3DeleteObjectExecutor struct{}

func (e *S3DeleteObjectExecutor) Type() string { return "aws-s3-delete-object" }

func (e *S3DeleteObjectExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	awsCfg, err := parseAWSConfig(step, templateResolver)
	if err != nil {
		return nil, err
	}
	bucket := getString(step.Config, "bucket")
	key := getString(step.Config, "key")
	versionID := getString(step.Config, "versionId")

	if bucket == "" {
		return nil, fmt.Errorf("bucket name is required")
	}
	if key == "" {
		return nil, fmt.Errorf("object key is required")
	}

	client, err := getS3Client(awsCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create S3 client: %w", err)
	}

	input := &s3.DeleteObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	}

	if versionID != "" {
		input.VersionId = aws.String(versionID)
	}

	_, err = client.DeleteObject(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("failed to delete object: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":   true,
			"bucket":    bucket,
			"key":       key,
			"versionId": versionID,
			"message":   "Object deleted successfully",
		},
	}, nil
}

// ============================================================================
// EC2 EXECUTORS
// ============================================================================

// EC2ListInstancesExecutor handles aws-ec2-list-instances node type
type EC2ListInstancesExecutor struct{}

func (e *EC2ListInstancesExecutor) Type() string { return "aws-ec2-list-instances" }

func (e *EC2ListInstancesExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	awsCfg, err := parseAWSConfig(step, templateResolver)
	if err != nil {
		return nil, err
	}
	instanceIDs := getStringSlice(step.Config, "instanceIds")
	state := getString(step.Config, "state")
	tags := getStringSlice(step.Config, "tags")
	limit := getInt(step.Config, "limit", 100)

	if awsCfg.Region == "" {
		return nil, fmt.Errorf("AWS region is required")
	}

	client, err := getEC2Client(awsCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create EC2 client: %w", err)
	}

	// Build filters
	var filters []types.Filter

	if state != "" {
		filters = append(filters, types.Filter{
			Name:   aws.String("instance-state-name"),
			Values: []string{state},
		})
	}

	for _, tag := range tags {
		parts := strings.SplitN(tag, "=", 2)
		if len(parts) == 2 {
			filters = append(filters, types.Filter{
				Name:   aws.String("tag:" + parts[0]),
				Values: []string{parts[1]},
			})
		}
	}

	input := &ec2.DescribeInstancesInput{}

	if len(instanceIDs) > 0 {
		input.InstanceIds = instanceIDs
	}
	if len(filters) > 0 {
		input.Filters = filters
	}

	instances := make([]map[string]interface{}, 0)
	var nextToken *string

	for len(instances) < limit {
		if nextToken != nil {
			input.NextToken = nextToken
		}

		result, err := client.DescribeInstances(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("failed to describe instances: %w", err)
		}

		for _, reservation := range result.Reservations {
			for _, instance := range reservation.Instances {
				if len(instances) >= limit {
					break
				}

				instanceInfo := map[string]interface{}{
					"instanceId":   aws.ToString(instance.InstanceId),
					"instanceType": string(instance.InstanceType),
					"state":        string(instance.State.Name),
					"stateCode":    instance.State.Code,
					"imageId":      aws.ToString(instance.ImageId),
					"launchTime":   instance.LaunchTime.Format(time.RFC3339),
					"privateIp":    aws.ToString(instance.PrivateIpAddress),
					"publicIp":     aws.ToString(instance.PublicIpAddress),
					"subnetId":     aws.ToString(instance.SubnetId),
					"vpcId":        aws.ToString(instance.VpcId),
					"architecture": string(instance.Architecture),
				}

				// Add tags
				if len(instance.Tags) > 0 {
					tagsMap := make(map[string]string)
					for _, tag := range instance.Tags {
						tagsMap[aws.ToString(tag.Key)] = aws.ToString(tag.Value)
					}
					instanceInfo["tags"] = tagsMap
					if name, ok := tagsMap["Name"]; ok {
						instanceInfo["name"] = name
					}
				}

				// Add security groups
				if len(instance.SecurityGroups) > 0 {
					sgs := make([]map[string]string, 0)
					for _, sg := range instance.SecurityGroups {
						sgs = append(sgs, map[string]string{
							"id":   aws.ToString(sg.GroupId),
							"name": aws.ToString(sg.GroupName),
						})
					}
					instanceInfo["securityGroups"] = sgs
				}

				instances = append(instances, instanceInfo)
			}
		}

		if result.NextToken == nil {
			break
		}
		nextToken = result.NextToken
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":   true,
			"region":    awsCfg.Region,
			"instances": instances,
			"count":     len(instances),
		},
	}, nil
}

// EC2StartInstanceExecutor handles aws-ec2-start-instance node type
type EC2StartInstanceExecutor struct{}

func (e *EC2StartInstanceExecutor) Type() string { return "aws-ec2-start-instance" }

func (e *EC2StartInstanceExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	awsCfg, err := parseAWSConfig(step, templateResolver)
	if err != nil {
		return nil, err
	}
	instanceID := getString(step.Config, "instanceId")

	if awsCfg.Region == "" {
		return nil, fmt.Errorf("AWS region is required")
	}
	if instanceID == "" {
		return nil, fmt.Errorf("instance ID is required")
	}

	client, err := getEC2Client(awsCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create EC2 client: %w", err)
	}

	input := &ec2.StartInstancesInput{
		InstanceIds: []string{instanceID},
	}

	result, err := client.StartInstances(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("failed to start instance: %w", err)
	}

	var previousState, currentState string
	if len(result.StartingInstances) > 0 {
		previousState = string(result.StartingInstances[0].PreviousState.Name)
		currentState = string(result.StartingInstances[0].CurrentState.Name)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":       true,
			"instanceId":    instanceID,
			"previousState": previousState,
			"currentState":  currentState,
			"message":       "Instance start initiated successfully",
		},
	}, nil
}

// EC2StopInstanceExecutor handles aws-ec2-stop-instance node type
type EC2StopInstanceExecutor struct{}

func (e *EC2StopInstanceExecutor) Type() string { return "aws-ec2-stop-instance" }

func (e *EC2StopInstanceExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	awsCfg, err := parseAWSConfig(step, templateResolver)
	if err != nil {
		return nil, err
	}
	instanceID := getString(step.Config, "instanceId")
	force := getBool(step.Config, "force", false)
	hibernate := getBool(step.Config, "hibernate", false)

	if awsCfg.Region == "" {
		return nil, fmt.Errorf("AWS region is required")
	}
	if instanceID == "" {
		return nil, fmt.Errorf("instance ID is required")
	}

	client, err := getEC2Client(awsCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create EC2 client: %w", err)
	}

	input := &ec2.StopInstancesInput{
		InstanceIds: []string{instanceID},
		Force:       &force,
	}

	if hibernate {
		input.Hibernate = &hibernate
	}

	result, err := client.StopInstances(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("failed to stop instance: %w", err)
	}

	var previousState, currentState string
	if len(result.StoppingInstances) > 0 {
		previousState = string(result.StoppingInstances[0].PreviousState.Name)
		currentState = string(result.StoppingInstances[0].CurrentState.Name)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":       true,
			"instanceId":    instanceID,
			"previousState": previousState,
			"currentState":  currentState,
			"message":       "Instance stop initiated successfully",
		},
	}, nil
}

// EC2RebootInstanceExecutor handles aws-ec2-reboot-instance node type
type EC2RebootInstanceExecutor struct{}

func (e *EC2RebootInstanceExecutor) Type() string { return "aws-ec2-reboot-instance" }

func (e *EC2RebootInstanceExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	awsCfg, err := parseAWSConfig(step, templateResolver)
	if err != nil {
		return nil, err
	}
	instanceID := getString(step.Config, "instanceId")

	if awsCfg.Region == "" {
		return nil, fmt.Errorf("AWS region is required")
	}
	if instanceID == "" {
		return nil, fmt.Errorf("instance ID is required")
	}

	client, err := getEC2Client(awsCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create EC2 client: %w", err)
	}

	input := &ec2.RebootInstancesInput{
		InstanceIds: []string{instanceID},
	}

	_, err = client.RebootInstances(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("failed to reboot instance: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":    true,
			"instanceId": instanceID,
			"message":    "Instance reboot initiated successfully",
		},
	}, nil
}

// ============================================================================
// LAMBDA EXECUTORS
// ============================================================================

// LambdaListFunctionsExecutor handles aws-lambda-list-functions node type
type LambdaListFunctionsExecutor struct{}

func (e *LambdaListFunctionsExecutor) Type() string { return "aws-lambda-list-functions" }

func (e *LambdaListFunctionsExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	awsCfg, err := parseAWSConfig(step, templateResolver)
	if err != nil {
		return nil, err
	}
	limit := getInt(step.Config, "limit", 50)

	if awsCfg.Region == "" {
		return nil, fmt.Errorf("AWS region is required")
	}

	client, err := getLambdaClient(awsCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create Lambda client: %w", err)
	}

	functions := make([]map[string]interface{}, 0)
	var marker *string

	for len(functions) < limit {
		input := &lambda.ListFunctionsInput{
			MaxItems: aws.Int32(int32(limit - len(functions))),
		}

		if marker != nil {
			input.Marker = marker
		}

		result, err := client.ListFunctions(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("failed to list functions: %w", err)
		}

		for _, fn := range result.Functions {
			functionInfo := map[string]interface{}{
				"functionName": aws.ToString(fn.FunctionName),
				"functionArn":  aws.ToString(fn.FunctionArn),
				"runtime":      string(fn.Runtime),
				"handler":      aws.ToString(fn.Handler),
				"codeSize":     fn.CodeSize,
				"description":  aws.ToString(fn.Description),
				"timeout":      fn.Timeout,
				"memorySize":   fn.MemorySize,
				"lastModified": aws.ToString(fn.LastModified),
				"codeSha256":   aws.ToString(fn.CodeSha256),
				"version":      aws.ToString(fn.Version),
				"role":         aws.ToString(fn.Role),
			}

			if fn.LastUpdateStatus != "" {
				functionInfo["lastUpdateStatus"] = string(fn.LastUpdateStatus)
			}

			functions = append(functions, functionInfo)
		}

		if result.NextMarker == nil {
			break
		}
		marker = result.NextMarker
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":   true,
			"region":    awsCfg.Region,
			"functions": functions,
			"count":     len(functions),
		},
	}, nil
}

// LambdaInvokeExecutor handles aws-lambda-invoke node type
type LambdaInvokeExecutor struct{}

func (e *LambdaInvokeExecutor) Type() string { return "aws-lambda-invoke" }

func (e *LambdaInvokeExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	awsCfg, err := parseAWSConfig(step, templateResolver)
	if err != nil {
		return nil, err
	}
	functionName := getString(step.Config, "functionName")
	qualifier := getString(step.Config, "qualifier")
	payload := getMap(step.Config, "payload")
	invocationType := getString(step.Config, "invocationType")

	if awsCfg.Region == "" {
		return nil, fmt.Errorf("AWS region is required")
	}
	if functionName == "" {
		return nil, fmt.Errorf("function name is required")
	}

	client, err := getLambdaClient(awsCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create Lambda client: %w", err)
	}

	// Convert payload to JSON
	var payloadBytes []byte
	if payload != nil {
		payloadBytes, err = json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal payload: %w", err)
		}
	} else {
		payloadBytes = []byte("{}")
	}

	input := &lambda.InvokeInput{
		FunctionName: aws.String(functionName),
		Payload:      payloadBytes,
	}

	if qualifier != "" {
		input.Qualifier = aws.String(qualifier)
	}

	if invocationType != "" {
		input.InvocationType = lambdaTypes.InvocationType(invocationType)
	}

	result, err := client.Invoke(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("failed to invoke function: %w", err)
	}

	output := map[string]interface{}{
		"success":         true,
		"functionName":    functionName,
		"statusCode":      result.StatusCode,
		"executedVersion": aws.ToString(result.ExecutedVersion),
	}

	if result.FunctionError != nil {
		output["error"] = aws.ToString(result.FunctionError)
	}

	if result.LogResult != nil {
		logResult, err := base64.StdEncoding.DecodeString(*result.LogResult)
		if err == nil {
			output["logs"] = string(logResult)
		}
	}

	if len(result.Payload) > 0 {
		var payloadResult interface{}
		if err := json.Unmarshal(result.Payload, &payloadResult); err == nil {
			output["payload"] = payloadResult
		} else {
			output["payload"] = string(result.Payload)
		}
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// LambdaGetFunctionExecutor handles aws-lambda-get-function node type
type LambdaGetFunctionExecutor struct{}

func (e *LambdaGetFunctionExecutor) Type() string { return "aws-lambda-get-function" }

func (e *LambdaGetFunctionExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	awsCfg, err := parseAWSConfig(step, templateResolver)
	if err != nil {
		return nil, err
	}
	functionName := getString(step.Config, "functionName")
	qualifier := getString(step.Config, "qualifier")

	if awsCfg.Region == "" {
		return nil, fmt.Errorf("AWS region is required")
	}
	if functionName == "" {
		return nil, fmt.Errorf("function name is required")
	}

	client, err := getLambdaClient(awsCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create Lambda client: %w", err)
	}

	input := &lambda.GetFunctionInput{
		FunctionName: aws.String(functionName),
	}

	if qualifier != "" {
		input.Qualifier = aws.String(qualifier)
	}

	result, err := client.GetFunction(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("failed to get function: %w", err)
	}

	output := map[string]interface{}{
		"success": true,
	}

	// Configuration details
	if result.Configuration != nil {
		cfg := result.Configuration
		output["configuration"] = map[string]interface{}{
			"functionName":     aws.ToString(cfg.FunctionName),
			"functionArn":      aws.ToString(cfg.FunctionArn),
			"runtime":          string(cfg.Runtime),
			"handler":          aws.ToString(cfg.Handler),
			"codeSize":         cfg.CodeSize,
			"description":      aws.ToString(cfg.Description),
			"timeout":          cfg.Timeout,
			"memorySize":       cfg.MemorySize,
			"lastModified":     aws.ToString(cfg.LastModified),
			"codeSha256":       aws.ToString(cfg.CodeSha256),
			"version":          aws.ToString(cfg.Version),
			"role":             aws.ToString(cfg.Role),
			"state":            string(cfg.State),
			"lastUpdateStatus": string(cfg.LastUpdateStatus),
		}
	}

	// Code location
	if result.Code != nil {
		output["code"] = map[string]interface{}{
			"repositoryType":   aws.ToString(result.Code.RepositoryType),
			"location":         aws.ToString(result.Code.Location),
			"imageUri":         aws.ToString(result.Code.ImageUri),
			"resolvedImageUri": aws.ToString(result.Code.ResolvedImageUri),
		}
	}

	// Tags
	if len(result.Tags) > 0 {
		output["tags"] = result.Tags
	}

	// Concurrency
	if result.Concurrency != nil {
		output["concurrency"] = map[string]interface{}{
			"reservedConcurrentExecutions": result.Concurrency.ReservedConcurrentExecutions,
		}
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}
