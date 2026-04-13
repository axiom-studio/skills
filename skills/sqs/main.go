package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqsTypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"

	"github.com/axiom-studio/skills.sdk/executor"
	"github.com/axiom-studio/skills.sdk/grpc"
	"github.com/axiom-studio/skills.sdk/resolver"
)

const (
	iconSQS = "mail"
)

// SQS client cache
var (
	sqsClients   = make(map[string]*sqs.Client)
	sqsClientsMux sync.RWMutex
)

// AWSConfig holds AWS connection configuration
type AWSConfig struct {
	AccessKeyID     string
	SecretAccessKey string
	Region          string
	SessionToken    string
	Profile         string
	Endpoint        string
}

// getSQSConfig returns an AWS config
func getSQSConfig(awsCfg AWSConfig) (aws.Config, error) {
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

// getSQSClient returns an SQS client (cached)
func getSQSClient(awsCfg AWSConfig) (*sqs.Client, error) {
	cacheKey := fmt.Sprintf("%s:%s:%s:%s", awsCfg.AccessKeyID, awsCfg.Region, awsCfg.Profile, awsCfg.Endpoint)

	sqsClientsMux.RLock()
	client, ok := sqsClients[cacheKey]
	sqsClientsMux.RUnlock()

	if ok {
		return client, nil
	}

	cfg, err := getSQSConfig(awsCfg)
	if err != nil {
		return nil, err
	}

	sqsClientsMux.Lock()
	defer sqsClientsMux.Unlock()

	// Apply custom endpoint if specified
	var sqsOpts []func(*sqs.Options)
	if awsCfg.Endpoint != "" {
		sqsOpts = append(sqsOpts, func(o *sqs.Options) {
			o.BaseEndpoint = aws.String(awsCfg.Endpoint)
		})
	}

	client = sqs.NewFromConfig(cfg, sqsOpts...)
	sqsClients[cacheKey] = client
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

// Helper to get int32 from config
func getInt32(configMap map[string]interface{}, key string, def int32) int32 {
	if v, ok := configMap[key]; ok {
		switch n := v.(type) {
		case float64:
			return int32(n)
		case int:
			return int32(n)
		case int32:
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

func main() {
	// Get port from env or use default
	port := os.Getenv("SKILL_PORT")
	if port == "" {
		port = "50092"
	}

	// Create skill server
	server := grpc.NewSkillServer("skill-sqs", "1.0.0")

	// Register message operation executors
	server.RegisterExecutorWithSchema("sqs-send-message", &SendMessageExecutor{}, SendMessageSchema)
	server.RegisterExecutorWithSchema("sqs-receive-message", &ReceiveMessageExecutor{}, ReceiveMessageSchema)
	server.RegisterExecutorWithSchema("sqs-delete-message", &DeleteMessageExecutor{}, DeleteMessageSchema)

	// Register queue management executors
	server.RegisterExecutorWithSchema("sqs-queue-create", &QueueCreateExecutor{}, QueueCreateSchema)
	server.RegisterExecutorWithSchema("sqs-queue-list", &QueueListExecutor{}, QueueListSchema)
	server.RegisterExecutorWithSchema("sqs-queue-delete", &QueueDeleteExecutor{}, QueueDeleteSchema)
	server.RegisterExecutorWithSchema("sqs-queue-purge", &QueuePurgeExecutor{}, QueuePurgeSchema)
	server.RegisterExecutorWithSchema("sqs-queue-attributes", &QueueAttributesExecutor{}, QueueAttributesSchema)

	// Register dead-letter queue executor
	server.RegisterExecutorWithSchema("sqs-dead-letter-queue", &DeadLetterQueueExecutor{}, DeadLetterQueueSchema)

	fmt.Printf("Starting skill-sqs gRPC server on port %s\n", port)
	if err := server.Serve(port); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to serve: %v\n", err)
		os.Exit(1)
	}
}

// ============================================================================
// SCHEMAS
// ============================================================================

// addConnectionSection adds AWS connection fields to schema builder
func addConnectionSection(builder *resolver.SchemaBuilder) *resolver.SchemaBuilder {
	return builder.
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
				resolver.WithPlaceholder("https://sqs.us-east-1.amazonaws.com"),
				resolver.WithHint("Custom SQS endpoint URL (for LocalStack or compatible services)"),
			).
			EndSection()
}

// SendMessageSchema is the UI schema for sqs-send-message
var SendMessageSchema = addConnectionSection(resolver.NewSchemaBuilder("sqs-send-message")).
	WithName("Send SQS Message").
	WithCategory("action").
	WithIcon(iconSQS).
	WithDescription("Send a message to an SQS queue").
	AddSection("Queue").
		AddExpressionField("queueUrl", "Queue URL",
			resolver.WithRequired(),
			resolver.WithPlaceholder("https://sqs.us-east-1.amazonaws.com/123456789012/my-queue"),
			resolver.WithHint("Full URL of the SQS queue"),
		).
		EndSection().
	AddSection("Message").
		AddTextareaField("messageBody", "Message Body",
			resolver.WithRequired(),
			resolver.WithRows(4),
			resolver.WithPlaceholder("{\"key\": \"value\"}"),
			resolver.WithHint("Message content (JSON or plain text)"),
		).
		AddNumberField("delaySeconds", "Delay Seconds",
			resolver.WithDefault(0),
			resolver.WithMinMax(0, 900),
			resolver.WithHint("Delay delivery in seconds (0-900)"),
		).
		AddExpressionField("messageGroupId", "Message Group ID",
			resolver.WithHint("Required for FIFO queues"),
		).
		AddExpressionField("messageDeduplicationId", "Message Deduplication ID",
			resolver.WithHint("Used for deduplication in FIFO queues"),
		).
		AddJSONField("messageAttributes", "Message Attributes",
			resolver.WithHint("Custom message attributes as JSON"),
			resolver.WithHeight(150),
		).
		EndSection().
	Build()

// ReceiveMessageSchema is the UI schema for sqs-receive-message
var ReceiveMessageSchema = addConnectionSection(resolver.NewSchemaBuilder("sqs-receive-message")).
	WithName("Receive SQS Messages").
	WithCategory("action").
	WithIcon(iconSQS).
	WithDescription("Receive messages from an SQS queue").
	AddSection("Queue").
		AddExpressionField("queueUrl", "Queue URL",
			resolver.WithRequired(),
			resolver.WithPlaceholder("https://sqs.us-east-1.amazonaws.com/123456789012/my-queue"),
			resolver.WithHint("Full URL of the SQS queue"),
		).
		EndSection().
	AddSection("Options").
		AddNumberField("maxNumberOfMessages", "Max Messages",
			resolver.WithDefault(1),
			resolver.WithMinMax(1, 10),
			resolver.WithHint("Maximum number of messages to receive (1-10)"),
		).
		AddNumberField("waitTimeSeconds", "Wait Time (seconds)",
			resolver.WithDefault(0),
			resolver.WithMinMax(0, 20),
			resolver.WithHint("Long polling wait time in seconds (0-20)"),
		).
		AddNumberField("visibilityTimeout", "Visibility Timeout",
			resolver.WithDefault(30),
			resolver.WithMinMax(0, 43200),
			resolver.WithHint("Visibility timeout in seconds (0-43200)"),
		).
		AddNumberField("receiveRequestAttemptId", "Receive Request Attempt ID",
			resolver.WithHint("Unique identifier for deduplication"),
		).
		EndSection().
	Build()

// DeleteMessageSchema is the UI schema for sqs-delete-message
var DeleteMessageSchema = addConnectionSection(resolver.NewSchemaBuilder("sqs-delete-message")).
	WithName("Delete SQS Message").
	WithCategory("action").
	WithIcon(iconSQS).
	WithDescription("Delete a message from an SQS queue").
	AddSection("Queue").
		AddExpressionField("queueUrl", "Queue URL",
			resolver.WithRequired(),
			resolver.WithPlaceholder("https://sqs.us-east-1.amazonaws.com/123456789012/my-queue"),
			resolver.WithHint("Full URL of the SQS queue"),
		).
		EndSection().
	AddSection("Message").
		AddExpressionField("receiptHandle", "Receipt Handle",
			resolver.WithRequired(),
			resolver.WithPlaceholder("AQEBz..."),
			resolver.WithHint("Receipt handle from the received message"),
		).
		EndSection().
	Build()

// QueueCreateSchema is the UI schema for sqs-queue-create
var QueueCreateSchema = addConnectionSection(resolver.NewSchemaBuilder("sqs-queue-create")).
	WithName("Create SQS Queue").
	WithCategory("action").
	WithIcon(iconSQS).
	WithDescription("Create a new SQS queue").
	AddSection("Queue").
		AddExpressionField("queueName", "Queue Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-queue"),
			resolver.WithHint("Name for the new queue"),
		).
		EndSection().
	AddSection("Options").
		AddToggleField("fifo", "FIFO Queue",
			resolver.WithDefault(false),
			resolver.WithHint("Create a FIFO queue (requires .fifo suffix in name)"),
		).
		AddNumberField("delaySeconds", "Default Delay Seconds",
			resolver.WithDefault(0),
			resolver.WithMinMax(0, 900),
			resolver.WithHint("Default delay for messages (0-900 seconds)"),
		).
		AddNumberField("visibilityTimeout", "Visibility Timeout",
			resolver.WithDefault(30),
			resolver.WithMinMax(0, 43200),
			resolver.WithHint("Default visibility timeout (0-43200 seconds)"),
		).
		AddNumberField("messageRetentionPeriod", "Message Retention Period",
			resolver.WithDefault(345600),
			resolver.WithMinMax(60, 1209600),
			resolver.WithHint("How long to retain messages in seconds (60-1209600, default 4 days)"),
		).
		AddNumberField("receiveWaitTimeSeconds", "Receive Wait Time",
			resolver.WithDefault(0),
			resolver.WithMinMax(0, 20),
			resolver.WithHint("Long polling wait time (0-20 seconds)"),
		).
		AddNumberField("maxMessageSize", "Max Message Size",
			resolver.WithDefault(262144),
			resolver.WithMinMax(1024, 262144),
			resolver.WithHint("Maximum message size in bytes (1024-262144)"),
		).
		EndSection().
	AddSection("Dead Letter Queue").
		AddExpressionField("redrivePolicyQueueArn", "DLQ ARN",
			resolver.WithHint("ARN of the dead-letter queue"),
		).
		AddNumberField("maxReceiveCount", "Max Receive Count",
			resolver.WithDefault(5),
			resolver.WithHint("Number of times a message can be received before being moved to DLQ"),
		).
		EndSection().
	Build()

// QueueListSchema is the UI schema for sqs-queue-list
var QueueListSchema = addConnectionSection(resolver.NewSchemaBuilder("sqs-queue-list")).
	WithName("List SQS Queues").
	WithCategory("action").
	WithIcon(iconSQS).
	WithDescription("List SQS queues").
	AddSection("Filters").
		AddExpressionField("queueNamePrefix", "Queue Name Prefix",
			resolver.WithPlaceholder("my-"),
			resolver.WithHint("Filter queues by name prefix"),
		).
		EndSection().
	AddSection("Options").
		AddNumberField("maxResults", "Max Results",
			resolver.WithDefault(100),
			resolver.WithMinMax(1, 1000),
			resolver.WithHint("Maximum number of queues to return"),
		).
		EndSection().
	Build()

// QueueDeleteSchema is the UI schema for sqs-queue-delete
var QueueDeleteSchema = addConnectionSection(resolver.NewSchemaBuilder("sqs-queue-delete")).
	WithName("Delete SQS Queue").
	WithCategory("action").
	WithIcon(iconSQS).
	WithDescription("Delete an SQS queue").
	AddSection("Queue").
		AddExpressionField("queueUrl", "Queue URL",
			resolver.WithRequired(),
			resolver.WithPlaceholder("https://sqs.us-east-1.amazonaws.com/123456789012/my-queue"),
			resolver.WithHint("Full URL of the SQS queue to delete"),
		).
		EndSection().
	Build()

// QueuePurgeSchema is the UI schema for sqs-queue-purge
var QueuePurgeSchema = addConnectionSection(resolver.NewSchemaBuilder("sqs-queue-purge")).
	WithName("Purge SQS Queue").
	WithCategory("action").
	WithIcon(iconSQS).
	WithDescription("Purge all messages from an SQS queue").
	AddSection("Queue").
		AddExpressionField("queueUrl", "Queue URL",
			resolver.WithRequired(),
			resolver.WithPlaceholder("https://sqs.us-east-1.amazonaws.com/123456789012/my-queue"),
			resolver.WithHint("Full URL of the SQS queue to purge"),
		).
		EndSection().
	Build()

// QueueAttributesSchema is the UI schema for sqs-queue-attributes
var QueueAttributesSchema = addConnectionSection(resolver.NewSchemaBuilder("sqs-queue-attributes")).
	WithName("Get SQS Queue Attributes").
	WithCategory("action").
	WithIcon(iconSQS).
	WithDescription("Get attributes of an SQS queue").
	AddSection("Queue").
		AddExpressionField("queueUrl", "Queue URL",
			resolver.WithRequired(),
			resolver.WithPlaceholder("https://sqs.us-east-1.amazonaws.com/123456789012/my-queue"),
			resolver.WithHint("Full URL of the SQS queue"),
		).
		EndSection().
	AddSection("Attributes").
		AddSelectField("attributeNames", "Attributes",
			[]resolver.SelectOption{
				{Label: "All", Value: "All"},
				{Label: "ApproximateNumberOfMessages", Value: "ApproximateNumberOfMessages"},
				{Label: "ApproximateNumberOfMessagesNotVisible", Value: "ApproximateNumberOfMessagesNotVisible"},
				{Label: "ApproximateNumberOfMessagesDelayed", Value: "ApproximateNumberOfMessagesDelayed"},
				{Label: "CreatedTimestamp", Value: "CreatedTimestamp"},
				{Label: "LastModifiedTimestamp", Value: "LastModifiedTimestamp"},
				{Label: "VisibilityTimeout", Value: "VisibilityTimeout"},
				{Label: "MaximumMessageSize", Value: "MaximumMessageSize"},
				{Label: "MessageRetentionPeriod", Value: "MessageRetentionPeriod"},
				{Label: "DelaySeconds", Value: "DelaySeconds"},
				{Label: "ReceiveMessageWaitTimeSeconds", Value: "ReceiveMessageWaitTimeSeconds"},
				{Label: "RedrivePolicy", Value: "RedrivePolicy"},
				{Label: "QueueArn", Value: "QueueArn"},
			},
			resolver.WithDefault("All"),
			resolver.WithHint("Attributes to retrieve"),
		).
		EndSection().
	Build()

// DeadLetterQueueSchema is the UI schema for sqs-dead-letter-queue
var DeadLetterQueueSchema = addConnectionSection(resolver.NewSchemaBuilder("sqs-dead-letter-queue")).
	WithName("Configure SQS Dead Letter Queue").
	WithCategory("action").
	WithIcon(iconSQS).
	WithDescription("Configure or remove dead-letter queue settings for a queue").
	AddSection("Source Queue").
		AddExpressionField("queueUrl", "Queue URL",
			resolver.WithRequired(),
			resolver.WithPlaceholder("https://sqs.us-east-1.amazonaws.com/123456789012/my-queue"),
			resolver.WithHint("Full URL of the source queue"),
		).
		EndSection().
	AddSection("Dead Letter Queue").
		AddExpressionField("deadLetterQueueArn", "DLQ ARN",
			resolver.WithHint("ARN of the dead-letter queue (leave empty to remove DLQ configuration)"),
		).
		AddNumberField("maxReceiveCount", "Max Receive Count",
			resolver.WithDefault(5),
			resolver.WithMinMax(1, 1000),
			resolver.WithHint("Number of times a message can be received before being moved to DLQ"),
		).
		AddToggleField("removePolicy", "Remove DLQ Policy",
			resolver.WithDefault(false),
			resolver.WithHint("Remove the redrive policy from the queue"),
		).
		EndSection().
	Build()

// ============================================================================
// SQS SEND MESSAGE EXECUTOR
// ============================================================================

// SendMessageExecutor handles sqs-send-message node type
type SendMessageExecutor struct{}

func (e *SendMessageExecutor) Type() string { return "sqs-send-message" }

func (e *SendMessageExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)
	awsCfg := parseAWSConfig(config)

	queueUrl := resolver.ResolveString(getString(config, "queueUrl"))
	messageBody := resolver.ResolveString(getString(config, "messageBody"))
	delaySeconds := getInt32(config, "delaySeconds", 0)
	messageGroupId := resolver.ResolveString(getString(config, "messageGroupId"))
	messageDeduplicationId := resolver.ResolveString(getString(config, "messageDeduplicationId"))
	messageAttributesJSON := getString(config, "messageAttributes")

	if queueUrl == "" {
		return nil, fmt.Errorf("queue URL is required")
	}
	if messageBody == "" {
		return nil, fmt.Errorf("message body is required")
	}

	client, err := getSQSClient(awsCfg)
	if err != nil {
		return nil, err
	}

	// Build message attributes
	var messageAttributes map[string]sqsTypes.MessageAttributeValue
	if messageAttributesJSON != "" {
		var attrs map[string]interface{}
		if err := json.Unmarshal([]byte(messageAttributesJSON), &attrs); err != nil {
			return nil, fmt.Errorf("invalid message attributes JSON: %w", err)
		}
		messageAttributes = make(map[string]sqsTypes.MessageAttributeValue)
		for k, v := range attrs {
			if attrMap, ok := v.(map[string]interface{}); ok {
				dataType := getString(attrMap, "DataType")
				if dataType == "" {
					dataType = "String"
				}
				attr := sqsTypes.MessageAttributeValue{
					DataType: aws.String(dataType),
				}
				if stringValue, ok := attrMap["StringValue"].(string); ok {
					attr.StringValue = aws.String(stringValue)
				}
				if binaryValue, ok := attrMap["BinaryValue"].(string); ok {
					attr.BinaryValue = []byte(binaryValue)
				}
				messageAttributes[k] = attr
			}
		}
	}

	// Build send message input
	input := &sqs.SendMessageInput{
		QueueUrl:     aws.String(queueUrl),
		MessageBody:  aws.String(messageBody),
		DelaySeconds: int32(delaySeconds),
	}

	if len(messageAttributes) > 0 {
		input.MessageAttributes = messageAttributes
	}

	if messageGroupId != "" {
		input.MessageGroupId = aws.String(messageGroupId)
	}

	if messageDeduplicationId != "" {
		input.MessageDeduplicationId = aws.String(messageDeduplicationId)
	}

	// Send the message
	result, err := client.SendMessage(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("failed to send message: %w", err)
	}

	output := map[string]interface{}{
		"success":          true,
		"messageId":        aws.ToString(result.MessageId),
		"md5OfMessageBody": aws.ToString(result.MD5OfMessageBody),
	}

	if result.MD5OfMessageAttributes != nil {
		output["md5OfMessageAttributes"] = aws.ToString(result.MD5OfMessageAttributes)
	}
	if result.SequenceNumber != nil {
		output["sequenceNumber"] = aws.ToString(result.SequenceNumber)
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// ============================================================================
// SQS RECEIVE MESSAGE EXECUTOR
// ============================================================================

// ReceiveMessageExecutor handles sqs-receive-message node type
type ReceiveMessageExecutor struct{}

func (e *ReceiveMessageExecutor) Type() string { return "sqs-receive-message" }

func (e *ReceiveMessageExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)
	awsCfg := parseAWSConfig(config)

	queueUrl := resolver.ResolveString(getString(config, "queueUrl"))
	maxNumberOfMessages := int32(getInt(config, "maxNumberOfMessages", 1))
	waitTimeSeconds := int32(getInt(config, "waitTimeSeconds", 0))
	visibilityTimeout := int32(getInt(config, "visibilityTimeout", 30))
	receiveRequestAttemptId := resolver.ResolveString(getString(config, "receiveRequestAttemptId"))

	if queueUrl == "" {
		return nil, fmt.Errorf("queue URL is required")
	}

	// Validate maxNumberOfMessages
	if maxNumberOfMessages < 1 {
		maxNumberOfMessages = 1
	}
	if maxNumberOfMessages > 10 {
		maxNumberOfMessages = 10
	}

	client, err := getSQSClient(awsCfg)
	if err != nil {
		return nil, err
	}

	// Build receive message input
	input := &sqs.ReceiveMessageInput{
		QueueUrl:            aws.String(queueUrl),
		MaxNumberOfMessages: int32(maxNumberOfMessages),
		WaitTimeSeconds:     int32(waitTimeSeconds),
		VisibilityTimeout:   int32(visibilityTimeout),
		MessageAttributeNames: []string{"All"},
		AttributeNames:      []sqsTypes.QueueAttributeName{sqsTypes.QueueAttributeNameAll},
	}

	if receiveRequestAttemptId != "" {
		input.ReceiveRequestAttemptId = aws.String(receiveRequestAttemptId)
	}

	// Receive messages
	result, err := client.ReceiveMessage(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("failed to receive messages: %w", err)
	}

	// Format messages for output
	var messages []map[string]interface{}
	for _, msg := range result.Messages {
		messageData := map[string]interface{}{
			"messageId":     aws.ToString(msg.MessageId),
			"body":          aws.ToString(msg.Body),
			"receiptHandle": aws.ToString(msg.ReceiptHandle),
			"md5OfBody":     aws.ToString(msg.MD5OfBody),
		}

		// Convert attributes (includes MessageGroupId, MessageDeduplicationId, SequenceNumber for FIFO)
		if len(msg.Attributes) > 0 {
			attrs := make(map[string]string)
			for k, v := range msg.Attributes {
				attrs[string(k)] = v
			}
			messageData["attributes"] = attrs

			// Extract FIFO-specific fields from attributes
			if val, ok := attrs["MessageGroupId"]; ok {
				messageData["messageGroupId"] = val
			}
			if val, ok := attrs["MessageDeduplicationId"]; ok {
				messageData["messageDeduplicationId"] = val
			}
			if val, ok := attrs["SequenceNumber"]; ok {
				messageData["sequenceNumber"] = val
			}
		}

		// Convert message attributes
		if len(msg.MessageAttributes) > 0 {
			msgAttrs := make(map[string]interface{})
			for k, v := range msg.MessageAttributes {
				attrData := map[string]interface{}{
					"dataType": aws.ToString(v.DataType),
				}
				if v.StringValue != nil {
					attrData["stringValue"] = aws.ToString(v.StringValue)
				}
				if v.BinaryValue != nil {
					attrData["binaryValue"] = string(v.BinaryValue)
				}
				msgAttrs[k] = attrData
			}
			messageData["messageAttributes"] = msgAttrs
		}

		messages = append(messages, messageData)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":  true,
			"messages": messages,
			"count":    len(messages),
		},
	}, nil
}

// ============================================================================
// SQS DELETE MESSAGE EXECUTOR
// ============================================================================

// DeleteMessageExecutor handles sqs-delete-message node type
type DeleteMessageExecutor struct{}

func (e *DeleteMessageExecutor) Type() string { return "sqs-delete-message" }

func (e *DeleteMessageExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)
	awsCfg := parseAWSConfig(config)

	queueUrl := resolver.ResolveString(getString(config, "queueUrl"))
	receiptHandle := resolver.ResolveString(getString(config, "receiptHandle"))

	if queueUrl == "" {
		return nil, fmt.Errorf("queue URL is required")
	}
	if receiptHandle == "" {
		return nil, fmt.Errorf("receipt handle is required")
	}

	client, err := getSQSClient(awsCfg)
	if err != nil {
		return nil, err
	}

	// Delete the message
	_, err = client.DeleteMessage(ctx, &sqs.DeleteMessageInput{
		QueueUrl:      aws.String(queueUrl),
		ReceiptHandle: aws.String(receiptHandle),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to delete message: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":       true,
			"message":       "Message deleted successfully",
			"receiptHandle": receiptHandle,
		},
	}, nil
}

// ============================================================================
// SQS QUEUE CREATE EXECUTOR
// ============================================================================

// QueueCreateExecutor handles sqs-queue-create node type
type QueueCreateExecutor struct{}

func (e *QueueCreateExecutor) Type() string { return "sqs-queue-create" }

func (e *QueueCreateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)
	awsCfg := parseAWSConfig(config)

	queueName := resolver.ResolveString(getString(config, "queueName"))
	isFifo := getBool(config, "fifo", false)
	delaySeconds := getInt32(config, "delaySeconds", 0)
	visibilityTimeout := getInt32(config, "visibilityTimeout", 30)
	messageRetentionPeriod := getInt32(config, "messageRetentionPeriod", 345600)
	receiveWaitTimeSeconds := getInt32(config, "receiveWaitTimeSeconds", 0)
	maxMessageSize := getInt32(config, "maxMessageSize", 262144)
	redrivePolicyQueueArn := resolver.ResolveString(getString(config, "redrivePolicyQueueArn"))
	maxReceiveCount := getInt32(config, "maxReceiveCount", 5)

	if queueName == "" {
		return nil, fmt.Errorf("queue name is required")
	}

	// Add .fifo suffix if FIFO queue and name doesn't already have it
	if isFifo && !strings.HasSuffix(queueName, ".fifo") {
		queueName = queueName + ".fifo"
	}

	client, err := getSQSClient(awsCfg)
	if err != nil {
		return nil, err
	}

	// Build queue attributes
	attributes := make(map[string]string)
	attributes["DelaySeconds"] = strconv.Itoa(int(delaySeconds))
	attributes["VisibilityTimeout"] = strconv.Itoa(int(visibilityTimeout))
	attributes["MessageRetentionPeriod"] = strconv.Itoa(int(messageRetentionPeriod))
	attributes["ReceiveMessageWaitTimeSeconds"] = strconv.Itoa(int(receiveWaitTimeSeconds))
	attributes["MaximumMessageSize"] = strconv.Itoa(int(maxMessageSize))

	if isFifo {
		attributes["FifoQueue"] = "true"
	}

	// Add redrive policy if DLQ is configured
	if redrivePolicyQueueArn != "" {
		redrivePolicy := map[string]interface{}{
			"deadLetterTargetArn": redrivePolicyQueueArn,
			"maxReceiveCount":     maxReceiveCount,
		}
		redrivePolicyJSON, err := json.Marshal(redrivePolicy)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal redrive policy: %w", err)
		}
		attributes["RedrivePolicy"] = string(redrivePolicyJSON)
	}

	// Create the queue
	result, err := client.CreateQueue(ctx, &sqs.CreateQueueInput{
		QueueName:  aws.String(queueName),
		Attributes: attributes,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create queue: %w", err)
	}

	// Get queue attributes to return more info
	queueUrl := aws.ToString(result.QueueUrl)
	attrResult, err := client.GetQueueAttributes(ctx, &sqs.GetQueueAttributesInput{
		QueueUrl:       aws.String(queueUrl),
		AttributeNames: []sqsTypes.QueueAttributeName{sqsTypes.QueueAttributeNameAll},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get queue attributes: %w", err)
	}

	// Convert attributes to map (already strings)
	queueAttrs := attrResult.Attributes

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":    true,
			"queueUrl":   queueUrl,
			"queueName":  queueName,
			"attributes": queueAttrs,
		},
	}, nil
}

// ============================================================================
// SQS QUEUE LIST EXECUTOR
// ============================================================================

// QueueListExecutor handles sqs-queue-list node type
type QueueListExecutor struct{}

func (e *QueueListExecutor) Type() string { return "sqs-queue-list" }

func (e *QueueListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)
	awsCfg := parseAWSConfig(config)

	queueNamePrefix := resolver.ResolveString(getString(config, "queueNamePrefix"))
	maxResults := getInt(config, "maxResults", 100)

	client, err := getSQSClient(awsCfg)
	if err != nil {
		return nil, err
	}

	// Build list queues input
	input := &sqs.ListQueuesInput{
		MaxResults: aws.Int32(int32(maxResults)),
	}

	if queueNamePrefix != "" {
		input.QueueNamePrefix = aws.String(queueNamePrefix)
	}

	// List queues
	result, err := client.ListQueues(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("failed to list queues: %w", err)
	}

	// Format queue URLs (already strings)
	queueUrls := result.QueueUrls

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":   true,
			"queueUrls": queueUrls,
			"count":     len(queueUrls),
		},
	}, nil
}

// ============================================================================
// SQS QUEUE DELETE EXECUTOR
// ============================================================================

// QueueDeleteExecutor handles sqs-queue-delete node type
type QueueDeleteExecutor struct{}

func (e *QueueDeleteExecutor) Type() string { return "sqs-queue-delete" }

func (e *QueueDeleteExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)
	awsCfg := parseAWSConfig(config)

	queueUrl := resolver.ResolveString(getString(config, "queueUrl"))

	if queueUrl == "" {
		return nil, fmt.Errorf("queue URL is required")
	}

	client, err := getSQSClient(awsCfg)
	if err != nil {
		return nil, err
	}

	// Delete the queue
	_, err = client.DeleteQueue(ctx, &sqs.DeleteQueueInput{
		QueueUrl: aws.String(queueUrl),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to delete queue: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":  true,
			"message":  "Queue deleted successfully",
			"queueUrl": queueUrl,
		},
	}, nil
}

// ============================================================================
// SQS QUEUE PURGE EXECUTOR
// ============================================================================

// QueuePurgeExecutor handles sqs-queue-purge node type
type QueuePurgeExecutor struct{}

func (e *QueuePurgeExecutor) Type() string { return "sqs-queue-purge" }

func (e *QueuePurgeExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)
	awsCfg := parseAWSConfig(config)

	queueUrl := resolver.ResolveString(getString(config, "queueUrl"))

	if queueUrl == "" {
		return nil, fmt.Errorf("queue URL is required")
	}

	client, err := getSQSClient(awsCfg)
	if err != nil {
		return nil, err
	}

	// Purge the queue
	_, err = client.PurgeQueue(ctx, &sqs.PurgeQueueInput{
		QueueUrl: aws.String(queueUrl),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to purge queue: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":  true,
			"message":  "Queue purged successfully",
			"queueUrl": queueUrl,
		},
	}, nil
}

// ============================================================================
// SQS QUEUE ATTRIBUTES EXECUTOR
// ============================================================================

// QueueAttributesExecutor handles sqs-queue-attributes node type
type QueueAttributesExecutor struct{}

func (e *QueueAttributesExecutor) Type() string { return "sqs-queue-attributes" }

func (e *QueueAttributesExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)
	awsCfg := parseAWSConfig(config)

	queueUrl := resolver.ResolveString(getString(config, "queueUrl"))
	attributeName := resolver.ResolveString(getString(config, "attributeNames"))

	if queueUrl == "" {
		return nil, fmt.Errorf("queue URL is required")
	}

	client, err := getSQSClient(awsCfg)
	if err != nil {
		return nil, err
	}

	// Determine attribute names to fetch
	var attributeNames []sqsTypes.QueueAttributeName
	if attributeName == "" || attributeName == "All" {
		attributeNames = []sqsTypes.QueueAttributeName{sqsTypes.QueueAttributeNameAll}
	} else {
		attributeNames = []sqsTypes.QueueAttributeName{sqsTypes.QueueAttributeName(attributeName)}
	}

	// Get queue attributes
	result, err := client.GetQueueAttributes(ctx, &sqs.GetQueueAttributesInput{
		QueueUrl:       aws.String(queueUrl),
		AttributeNames: attributeNames,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get queue attributes: %w", err)
	}

	// Convert attributes to map (already strings)
	attributes := result.Attributes

	// Parse numeric attributes for convenience
	numericAttrs := make(map[string]int64)
	for _, key := range []string{"ApproximateNumberOfMessages", "ApproximateNumberOfMessagesNotVisible",
		"ApproximateNumberOfMessagesDelayed", "VisibilityTimeout", "MaximumMessageSize",
		"MessageRetentionPeriod", "DelaySeconds", "ReceiveMessageWaitTimeSeconds", "CreatedTimestamp", "LastModifiedTimestamp"} {
		if val, ok := attributes[key]; ok {
			if numVal, err := strconv.ParseInt(val, 10, 64); err == nil {
				numericAttrs[key] = numVal
			}
		}
	}

	// Parse redrive policy if present
	var redrivePolicy map[string]interface{}
	if redrivePolicyStr, ok := attributes[string(sqsTypes.QueueAttributeNameRedrivePolicy)]; ok && redrivePolicyStr != "" {
		if err := json.Unmarshal([]byte(redrivePolicyStr), &redrivePolicy); err != nil {
			redrivePolicy = map[string]interface{}{"raw": redrivePolicyStr}
		}
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":           true,
			"queueUrl":          queueUrl,
			"attributes":        attributes,
			"numericAttributes": numericAttrs,
			"redrivePolicy":     redrivePolicy,
		},
	}, nil
}

// ============================================================================
// SQS DEAD LETTER QUEUE EXECUTOR
// ============================================================================

// DeadLetterQueueExecutor handles sqs-dead-letter-queue node type
type DeadLetterQueueExecutor struct{}

func (e *DeadLetterQueueExecutor) Type() string { return "sqs-dead-letter-queue" }

func (e *DeadLetterQueueExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)
	awsCfg := parseAWSConfig(config)

	queueUrl := resolver.ResolveString(getString(config, "queueUrl"))
	deadLetterQueueArn := resolver.ResolveString(getString(config, "deadLetterQueueArn"))
	maxReceiveCount := getInt32(config, "maxReceiveCount", 5)
	removePolicy := getBool(config, "removePolicy", false)

	if queueUrl == "" {
		return nil, fmt.Errorf("queue URL is required")
	}

	client, err := getSQSClient(awsCfg)
	if err != nil {
		return nil, err
	}

	// Get current attributes to preserve other settings
	currentAttrs, err := client.GetQueueAttributes(ctx, &sqs.GetQueueAttributesInput{
		QueueUrl:       aws.String(queueUrl),
		AttributeNames: []sqsTypes.QueueAttributeName{sqsTypes.QueueAttributeNameAll},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get current queue attributes: %w", err)
	}

	// Build attributes to update
	attributes := make(map[string]string)

	if removePolicy {
		// Remove redrive policy
		attributes["RedrivePolicy"] = ""
	} else if deadLetterQueueArn != "" {
		// Set redrive policy
		redrivePolicy := map[string]interface{}{
			"deadLetterTargetArn": deadLetterQueueArn,
			"maxReceiveCount":     maxReceiveCount,
		}
		redrivePolicyJSON, err := json.Marshal(redrivePolicy)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal redrive policy: %w", err)
		}
		attributes["RedrivePolicy"] = string(redrivePolicyJSON)
	} else {
		// Keep existing redrive policy if any
		if existingPolicy, ok := currentAttrs.Attributes[string(sqsTypes.QueueAttributeNameRedrivePolicy)]; ok {
			attributes["RedrivePolicy"] = existingPolicy
		}
	}

	// Set queue attributes
	if len(attributes) > 0 {
		_, err = client.SetQueueAttributes(ctx, &sqs.SetQueueAttributesInput{
			QueueUrl:   aws.String(queueUrl),
			Attributes: attributes,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to set queue attributes: %w", err)
		}
	}

	// Get updated attributes
	updatedAttrs, err := client.GetQueueAttributes(ctx, &sqs.GetQueueAttributesInput{
		QueueUrl:       aws.String(queueUrl),
		AttributeNames: []sqsTypes.QueueAttributeName{sqsTypes.QueueAttributeNameRedrivePolicy},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get updated queue attributes: %w", err)
	}

	var redrivePolicy map[string]interface{}
	if policyStr, ok := updatedAttrs.Attributes[string(sqsTypes.QueueAttributeNameRedrivePolicy)]; ok && policyStr != "" {
		if err := json.Unmarshal([]byte(policyStr), &redrivePolicy); err != nil {
			redrivePolicy = map[string]interface{}{"raw": policyStr}
		}
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":       true,
			"message":       "Dead letter queue configuration updated",
			"queueUrl":      queueUrl,
			"redrivePolicy": redrivePolicy,
			"policyRemoved": removePolicy,
		},
	}, nil
}
