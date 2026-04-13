package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/axiom-studio/skills.sdk/executor"
	"github.com/axiom-studio/skills.sdk/grpc"
	"github.com/axiom-studio/skills.sdk/resolver"
	amqp "github.com/rabbitmq/amqp091-go"
)

const (
	iconRabbitMQ = "mail"
)

// Connection pool for RabbitMQ clients
var (
	connections   = make(map[string]*amqp.Connection)
	connectionMux sync.RWMutex
)

// Config holds RabbitMQ connection configuration
type RabbitMQConfig struct {
	URL      string `json:"url"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
	VHost    string `json:"vhost"`
}

func main() {
	// Get port from env or use default
	port := os.Getenv("SKILL_PORT")
	if port == "" {
		port = "50090"
	}

	// Create skill server
	server := grpc.NewSkillServer("skill-rabbitmq", "1.0.0")

	// Register executors - Publish/Consume
	server.RegisterExecutorWithSchema("rabbitmq-publish", &PublishExecutor{}, PublishSchema)
	server.RegisterExecutorWithSchema("rabbitmq-consume", &ConsumeExecutor{}, ConsumeSchema)

	// Register executors - Queue operations
	server.RegisterExecutorWithSchema("rabbitmq-queue-create", &QueueCreateExecutor{}, QueueCreateSchema)
	server.RegisterExecutorWithSchema("rabbitmq-queue-list", &QueueListExecutor{}, QueueListSchema)
	server.RegisterExecutorWithSchema("rabbitmq-queue-delete", &QueueDeleteExecutor{}, QueueDeleteSchema)
	server.RegisterExecutorWithSchema("rabbitmq-queue-purge", &QueuePurgeExecutor{}, QueuePurgeSchema)

	// Register executors - Exchange operations
	server.RegisterExecutorWithSchema("rabbitmq-exchange-create", &ExchangeCreateExecutor{}, ExchangeCreateSchema)
	server.RegisterExecutorWithSchema("rabbitmq-exchange-list", &ExchangeListExecutor{}, ExchangeListSchema)

	// Register executors - Binding operations
	server.RegisterExecutorWithSchema("rabbitmq-binding-create", &BindingCreateExecutor{}, BindingCreateSchema)
	server.RegisterExecutorWithSchema("rabbitmq-binding-list", &BindingListExecutor{}, BindingListSchema)

	fmt.Printf("Starting skill-rabbitmq gRPC server on port %s\n", port)
	if err := server.Serve(port); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to serve: %v\n", err)
		os.Exit(1)
	}
}

// ============================================================================
// RABBITMQ CONNECTION HELPERS
// ============================================================================

// getRabbitMQConnection returns a cached RabbitMQ connection
func getRabbitMQConnection(config RabbitMQConfig) (*amqp.Connection, error) {
	url := getConnectionString(config)

	cacheKey := url

	connectionMux.RLock()
	conn, ok := connections[cacheKey]
	connectionMux.RUnlock()

	if ok && !conn.IsClosed() {
		return conn, nil
	}

	connectionMux.Lock()
	defer connectionMux.Unlock()

	// Double check
	if conn, ok := connections[cacheKey]; ok && !conn.IsClosed() {
		return conn, nil
	}

	// Close old connection if exists
	if conn != nil && !conn.IsClosed() {
		conn.Close()
	}

	// Create new connection
	var err error
	conn, err = amqp.Dial(url)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to RabbitMQ: %w", err)
	}

	connections[cacheKey] = conn
	return conn, nil
}

// getConnectionString builds a RabbitMQ connection URL
func getConnectionString(config RabbitMQConfig) string {
	if config.URL != "" {
		return config.URL
	}

	host := config.Host
	if host == "" {
		host = "localhost"
	}

	port := config.Port
	if port == 0 {
		port = 5672
	}

	username := config.Username
	if username == "" {
		username = "guest"
	}

	password := config.Password
	if password == "" {
		password = "guest"
	}

	vhost := config.VHost
	if vhost == "" {
		vhost = "/"
	} else {
		vhost = "/" + strings.TrimPrefix(vhost, "/")
	}

	return fmt.Sprintf("amqp://%s:%s@%s:%d%s", username, password, host, port, vhost)
}

// createChannel creates a new channel from a connection
func createChannel(config RabbitMQConfig) (*amqp.Connection, *amqp.Channel, error) {
	conn, err := getRabbitMQConnection(config)
	if err != nil {
		return nil, nil, err
	}

	ch, err := conn.Channel()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to open channel: %w", err)
	}

	return conn, ch, nil
}

// closeConnection closes a connection (for cleanup)
func closeConnection(conn *amqp.Connection) {
	if conn != nil && !conn.IsClosed() {
		conn.Close()
	}
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

// Helper to get int64 from config
func getInt64(config map[string]interface{}, key string, def int64) int64 {
	if v, ok := config[key]; ok {
		switch n := v.(type) {
		case float64:
			return int64(n)
		case int:
			return int64(n)
		case int64:
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

// parseRabbitMQConfig extracts RabbitMQ config from resolved config map
func parseRabbitMQConfig(config map[string]interface{}) RabbitMQConfig {
	return RabbitMQConfig{
		URL:      getString(config, "url"),
		Host:     getString(config, "host"),
		Port:     getInt(config, "port", 5672),
		Username: getString(config, "username"),
		Password: getString(config, "password"),
		VHost:    getString(config, "vhost"),
	}
}

// ============================================================================
// CONNECTION SCHEMA SECTION
// ============================================================================

func addConnectionSection(builder *resolver.SchemaBuilder) *resolver.SchemaBuilder {
	return builder.
		AddSection("Connection").
		AddExpressionField("url", "Connection URL",
			resolver.WithPlaceholder("amqp://guest:guest@localhost:5672/"),
			resolver.WithHint("Full RabbitMQ connection URL (overrides other connection settings)"),
		).
		AddExpressionField("host", "Host",
			resolver.WithDefault("localhost"),
			resolver.WithPlaceholder("localhost"),
			resolver.WithHint("RabbitMQ server hostname"),
		).
		AddNumberField("port", "Port",
			resolver.WithDefault(5672),
			resolver.WithHint("RabbitMQ AMQP port (default: 5672)"),
		).
		AddExpressionField("username", "Username",
			resolver.WithDefault("guest"),
			resolver.WithPlaceholder("guest"),
			resolver.WithHint("RabbitMQ username"),
		).
		AddExpressionField("password", "Password",
			resolver.WithDefault("guest"),
			resolver.WithPlaceholder("guest"),
			resolver.WithHint("RabbitMQ password"),
			resolver.WithSensitive(),
		).
		AddExpressionField("vhost", "Virtual Host",
			resolver.WithDefault("/"),
			resolver.WithPlaceholder("/"),
			resolver.WithHint("RabbitMQ virtual host"),
		).
		EndSection()
}

// ============================================================================
// SCHEMAS - PUBLISH/CONSUME
// ============================================================================

// PublishSchema is the UI schema for rabbitmq-publish
var PublishSchema = addConnectionSection(resolver.NewSchemaBuilder("rabbitmq-publish")).
	WithName("RabbitMQ Publish").
	WithCategory("action").
	WithIcon(iconRabbitMQ).
	WithDescription("Publish a message to a RabbitMQ exchange or queue").
	AddSection("Message").
		AddExpressionField("exchange", "Exchange",
			resolver.WithPlaceholder("my.exchange"),
			resolver.WithHint("Exchange name (leave empty for default exchange)"),
		).
		AddExpressionField("routingKey", "Routing Key",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my.routing.key"),
			resolver.WithHint("Routing key for the message"),
		).
		AddTextareaField("message", "Message Body",
			resolver.WithRequired(),
			resolver.WithRows(4),
			resolver.WithPlaceholder("Hello, RabbitMQ!"),
			resolver.WithHint("Message body content"),
		).
		AddSelectField("contentType", "Content Type",
			[]resolver.SelectOption{
				{Label: "Text Plain", Value: "text/plain"},
				{Label: "JSON", Value: "application/json"},
				{Label: "XML", Value: "application/xml"},
				{Label: "Octet Stream", Value: "application/octet-stream"},
			},
			resolver.WithDefault("text/plain"),
			resolver.WithHint("MIME content type of the message"),
		).
		AddToggleField("persistent", "Persistent",
			resolver.WithDefault(true),
			resolver.WithHint("Make message persistent (survive broker restart)"),
		).
		AddJSONField("headers", "Headers",
			resolver.WithHint("Custom message headers (JSON object)"),
			resolver.WithHeight(150),
		).
		EndSection().
	Build()

// ConsumeSchema is the UI schema for rabbitmq-consume
var ConsumeSchema = addConnectionSection(resolver.NewSchemaBuilder("rabbitmq-consume")).
	WithName("RabbitMQ Consume").
	WithCategory("action").
	WithIcon(iconRabbitMQ).
	WithDescription("Consume messages from a RabbitMQ queue").
	AddSection("Queue").
		AddExpressionField("queue", "Queue Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my.queue"),
			resolver.WithHint("Name of the queue to consume from"),
		).
		EndSection().
	AddSection("Options").
		AddNumberField("maxMessages", "Max Messages",
			resolver.WithDefault(1),
			resolver.WithHint("Maximum number of messages to consume (0 = unlimited)"),
		).
		AddNumberField("timeout", "Timeout (seconds)",
			resolver.WithDefault(30),
			resolver.WithHint("Timeout for waiting for messages (0 = no timeout)"),
		).
		AddToggleField("autoAck", "Auto Acknowledge",
			resolver.WithDefault(true),
			resolver.WithHint("Automatically acknowledge messages"),
		).
		EndSection().
	Build()

// ============================================================================
// SCHEMAS - QUEUE OPERATIONS
// ============================================================================

// QueueCreateSchema is the UI schema for rabbitmq-queue-create
var QueueCreateSchema = addConnectionSection(resolver.NewSchemaBuilder("rabbitmq-queue-create")).
	WithName("RabbitMQ Queue Create").
	WithCategory("action").
	WithIcon(iconRabbitMQ).
	WithDescription("Create a new RabbitMQ queue").
	AddSection("Queue").
		AddExpressionField("queue", "Queue Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my.queue"),
			resolver.WithHint("Name of the queue to create"),
		).
		EndSection().
	AddSection("Options").
		AddToggleField("durable", "Durable",
			resolver.WithDefault(true),
			resolver.WithHint("Queue survives broker restart"),
		).
		AddToggleField("autoDelete", "Auto Delete",
			resolver.WithDefault(false),
			resolver.WithHint("Delete queue when last consumer unsubscribes"),
		).
		AddToggleField("exclusive", "Exclusive",
			resolver.WithDefault(false),
			resolver.WithHint("Exclusive to this connection"),
		).
		AddToggleField("noWait", "No Wait",
			resolver.WithDefault(false),
			resolver.WithHint("Do not wait for server confirmation"),
		).
		AddJSONField("args", "Arguments",
			resolver.WithHint("Queue arguments (JSON object, e.g., for TTL, max length)"),
			resolver.WithHeight(150),
		).
		EndSection().
	Build()

// QueueListSchema is the UI schema for rabbitmq-queue-list
var QueueListSchema = addConnectionSection(resolver.NewSchemaBuilder("rabbitmq-queue-list")).
	WithName("RabbitMQ Queue List").
	WithCategory("action").
	WithIcon(iconRabbitMQ).
	WithDescription("List all RabbitMQ queues").
	AddSection("Options").
		AddExpressionField("pattern", "Pattern",
			resolver.WithPlaceholder("my.*"),
			resolver.WithHint("Filter queues by name pattern (supports * wildcard)"),
		).
		EndSection().
	Build()

// QueueDeleteSchema is the UI schema for rabbitmq-queue-delete
var QueueDeleteSchema = addConnectionSection(resolver.NewSchemaBuilder("rabbitmq-queue-delete")).
	WithName("RabbitMQ Queue Delete").
	WithCategory("action").
	WithIcon(iconRabbitMQ).
	WithDescription("Delete a RabbitMQ queue").
	AddSection("Queue").
		AddExpressionField("queue", "Queue Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my.queue"),
			resolver.WithHint("Name of the queue to delete"),
		).
		EndSection().
	AddSection("Options").
		AddToggleField("ifUnused", "If Unused",
			resolver.WithDefault(false),
			resolver.WithHint("Only delete if queue has no consumers"),
		).
		AddToggleField("ifEmpty", "If Empty",
			resolver.WithDefault(false),
			resolver.WithHint("Only delete if queue is empty"),
		).
		EndSection().
	Build()

// QueuePurgeSchema is the UI schema for rabbitmq-queue-purge
var QueuePurgeSchema = addConnectionSection(resolver.NewSchemaBuilder("rabbitmq-queue-purge")).
	WithName("RabbitMQ Queue Purge").
	WithCategory("action").
	WithIcon(iconRabbitMQ).
	WithDescription("Purge all messages from a RabbitMQ queue").
	AddSection("Queue").
		AddExpressionField("queue", "Queue Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my.queue"),
			resolver.WithHint("Name of the queue to purge"),
		).
		EndSection().
	Build()

// ============================================================================
// SCHEMAS - EXCHANGE OPERATIONS
// ============================================================================

// ExchangeCreateSchema is the UI schema for rabbitmq-exchange-create
var ExchangeCreateSchema = addConnectionSection(resolver.NewSchemaBuilder("rabbitmq-exchange-create")).
	WithName("RabbitMQ Exchange Create").
	WithCategory("action").
	WithIcon(iconRabbitMQ).
	WithDescription("Create a new RabbitMQ exchange").
	AddSection("Exchange").
		AddExpressionField("exchange", "Exchange Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my.exchange"),
			resolver.WithHint("Name of the exchange to create"),
		).
		AddSelectField("exchangeType", "Exchange Type",
			[]resolver.SelectOption{
				{Label: "Direct", Value: "direct"},
				{Label: "Fanout", Value: "fanout"},
				{Label: "Topic", Value: "topic"},
				{Label: "Headers", Value: "headers"},
			},
			resolver.WithDefault("direct"),
			resolver.WithHint("Type of exchange"),
		).
		EndSection().
	AddSection("Options").
		AddToggleField("durable", "Durable",
			resolver.WithDefault(true),
			resolver.WithHint("Exchange survives broker restart"),
		).
		AddToggleField("autoDelete", "Auto Delete",
			resolver.WithDefault(false),
			resolver.WithHint("Delete exchange when last binding is removed"),
		).
		AddToggleField("internal", "Internal",
			resolver.WithDefault(false),
			resolver.WithHint("Internal exchange (cannot be published to directly)"),
		).
		AddToggleField("noWait", "No Wait",
			resolver.WithDefault(false),
			resolver.WithHint("Do not wait for server confirmation"),
		).
		AddJSONField("args", "Arguments",
			resolver.WithHint("Exchange arguments (JSON object)"),
			resolver.WithHeight(150),
		).
		EndSection().
	Build()

// ExchangeListSchema is the UI schema for rabbitmq-exchange-list
var ExchangeListSchema = addConnectionSection(resolver.NewSchemaBuilder("rabbitmq-exchange-list")).
	WithName("RabbitMQ Exchange List").
	WithCategory("action").
	WithIcon(iconRabbitMQ).
	WithDescription("List all RabbitMQ exchanges").
	AddSection("Options").
		AddExpressionField("pattern", "Pattern",
			resolver.WithPlaceholder("my.*"),
			resolver.WithHint("Filter exchanges by name pattern (supports * wildcard)"),
		).
		EndSection().
	Build()

// ============================================================================
// SCHEMAS - BINDING OPERATIONS
// ============================================================================

// BindingCreateSchema is the UI schema for rabbitmq-binding-create
var BindingCreateSchema = addConnectionSection(resolver.NewSchemaBuilder("rabbitmq-binding-create")).
	WithName("RabbitMQ Binding Create").
	WithCategory("action").
	WithIcon(iconRabbitMQ).
	WithDescription("Create a binding between a queue and an exchange").
	AddSection("Binding").
		AddExpressionField("queue", "Queue Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my.queue"),
			resolver.WithHint("Name of the queue to bind"),
		).
		AddExpressionField("exchange", "Exchange",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my.exchange"),
			resolver.WithHint("Name of the exchange to bind to"),
		).
		AddExpressionField("routingKey", "Routing Key",
			resolver.WithPlaceholder(""),
			resolver.WithHint("Routing key for the binding"),
		).
		EndSection().
	AddSection("Options").
		AddJSONField("args", "Arguments",
			resolver.WithHint("Binding arguments (JSON object)"),
			resolver.WithHeight(150),
		).
		EndSection().
	Build()

// BindingListSchema is the UI schema for rabbitmq-binding-list
var BindingListSchema = addConnectionSection(resolver.NewSchemaBuilder("rabbitmq-binding-list")).
	WithName("RabbitMQ Binding List").
	WithCategory("action").
	WithIcon(iconRabbitMQ).
	WithDescription("List bindings for a queue or exchange").
	AddSection("Target").
		AddSelectField("targetType", "Target Type",
			[]resolver.SelectOption{
				{Label: "Queue", Value: "queue"},
				{Label: "Exchange", Value: "exchange"},
			},
			resolver.WithDefault("queue"),
			resolver.WithHint("Type of target to list bindings for"),
		).
		AddExpressionField("name", "Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my.queue"),
			resolver.WithHint("Name of the queue or exchange"),
		).
		EndSection().
	Build()

// ============================================================================
// EXECUTORS - PUBLISH/CONSUME
// ============================================================================

// PublishExecutor handles rabbitmq-publish
type PublishExecutor struct{}

// Type returns the executor type
func (e *PublishExecutor) Type() string {
	return "rabbitmq-publish"
}

// Execute runs the publish operation
func (e *PublishExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	// Get connection parameters
	rmqConfig := parseRabbitMQConfig(config)

	// Get message parameters
	exchange := resolver.ResolveString(getString(config, "exchange"))
	routingKey := resolver.ResolveString(getString(config, "routingKey"))
	message := resolver.ResolveString(getString(config, "message"))
	contentType := resolver.ResolveString(getString(config, "contentType"))
	persistent := getBool(config, "persistent", true)
	headersJSON := getString(config, "headers")

	if routingKey == "" {
		return nil, fmt.Errorf("routing key is required")
	}

	// Create channel
	_, ch, err := createChannel(rmqConfig)
	if err != nil {
		return nil, err
	}
	defer ch.Close()

	// Parse headers if provided
	var headers amqp.Table
	if headersJSON != "" {
		if err := json.Unmarshal([]byte(headersJSON), &headers); err != nil {
			return nil, fmt.Errorf("invalid headers JSON: %w", err)
		}
	}

	// Build message properties
	props := amqp.Publishing{
		ContentType:  contentType,
		Body:         []byte(message),
		DeliveryMode: amqp.Persistent,
		Timestamp:    time.Now(),
		Headers:      headers,
	}

	if !persistent {
		props.DeliveryMode = amqp.Transient
	}

	// Publish message
	err = ch.PublishWithContext(ctx, exchange, routingKey, false, false, props)
	if err != nil {
		return nil, fmt.Errorf("failed to publish message: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":    true,
			"exchange":   exchange,
			"routingKey": routingKey,
			"messageLen": len(message),
		},
	}, nil
}

// ConsumeExecutor handles rabbitmq-consume
type ConsumeExecutor struct{}

// Type returns the executor type
func (e *ConsumeExecutor) Type() string {
	return "rabbitmq-consume"
}

// Execute runs the consume operation
func (e *ConsumeExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	// Get connection parameters
	rmqConfig := parseRabbitMQConfig(config)

	// Get queue parameters
	queue := resolver.ResolveString(getString(config, "queue"))
	maxMessages := getInt(config, "maxMessages", 1)
	timeout := getInt(config, "timeout", 30)
	autoAck := getBool(config, "autoAck", true)

	if queue == "" {
		return nil, fmt.Errorf("queue name is required")
	}

	// Create channel
	_, ch, err := createChannel(rmqConfig)
	if err != nil {
		return nil, err
	}
	defer ch.Close()

	// Set up timeout context
	var cancel context.CancelFunc
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
		defer cancel()
	}

	// Start consuming
	msgs, err := ch.Consume(queue, "", autoAck, false, false, false, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to start consuming: %w", err)
	}

	// Collect messages
	var messages []map[string]interface{}
	count := 0

	for {
		select {
		case <-ctx.Done():
			return &executor.StepResult{
				Output: map[string]interface{}{
					"success":  true,
					"queue":    queue,
					"messages": messages,
					"count":    count,
					"reason":   "timeout or completed",
				},
			}, nil
		case msg, ok := <-msgs:
			if !ok {
				return &executor.StepResult{
					Output: map[string]interface{}{
						"success":  true,
						"queue":    queue,
						"messages": messages,
						"count":    count,
						"reason":   "channel closed",
					},
				}, nil
			}

			messageData := map[string]interface{}{
				"messageId":     msg.MessageId,
				"correlationId": msg.CorrelationId,
				"routingKey":    msg.RoutingKey,
				"exchange":      msg.Exchange,
				"contentType":   msg.ContentType,
				"contentEncoding": msg.ContentEncoding,
				"deliveryMode":  msg.DeliveryMode,
				"priority":      msg.Priority,
				"timestamp":     msg.Timestamp,
				"expiration":    msg.Expiration,
				"body":          string(msg.Body),
				"headers":       msg.Headers,
			}

			messages = append(messages, messageData)
			count++

			// Check if we've reached max messages
			if maxMessages > 0 && count >= maxMessages {
				return &executor.StepResult{
					Output: map[string]interface{}{
						"success":  true,
						"queue":    queue,
						"messages": messages,
						"count":    count,
					},
				}, nil
			}
		}
	}
}

// ============================================================================
// EXECUTORS - QUEUE OPERATIONS
// ============================================================================

// QueueCreateExecutor handles rabbitmq-queue-create
type QueueCreateExecutor struct{}

// Type returns the executor type
func (e *QueueCreateExecutor) Type() string {
	return "rabbitmq-queue-create"
}

// Execute runs the queue create operation
func (e *QueueCreateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	// Get connection parameters
	rmqConfig := parseRabbitMQConfig(config)

	// Get queue parameters
	queue := resolver.ResolveString(getString(config, "queue"))
	durable := getBool(config, "durable", true)
	autoDelete := getBool(config, "autoDelete", false)
	exclusive := getBool(config, "exclusive", false)
	noWait := getBool(config, "noWait", false)
	argsJSON := getString(config, "args")

	if queue == "" {
		return nil, fmt.Errorf("queue name is required")
	}

	// Create channel
	_, ch, err := createChannel(rmqConfig)
	if err != nil {
		return nil, err
	}
	defer ch.Close()

	// Parse arguments if provided
	var args amqp.Table
	if argsJSON != "" {
		if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
			return nil, fmt.Errorf("invalid arguments JSON: %w", err)
		}
	}

	// Declare queue
	q, err := ch.QueueDeclare(queue, durable, autoDelete, exclusive, noWait, args)
	if err != nil {
		return nil, fmt.Errorf("failed to create queue: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":   true,
			"queue":     q.Name,
			"messages":  q.Messages,
			"consumers": q.Consumers,
		},
	}, nil
}

// QueueListExecutor handles rabbitmq-queue-list
type QueueListExecutor struct{}

// Type returns the executor type
func (e *QueueListExecutor) Type() string {
	return "rabbitmq-queue-list"
}

// Execute runs the queue list operation
func (e *QueueListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	// Get connection parameters
	rmqConfig := parseRabbitMQConfig(config)

	// Get filter pattern
	pattern := resolver.ResolveString(getString(config, "pattern"))

	// Create channel
	_, ch, err := createChannel(rmqConfig)
	if err != nil {
		return nil, err
	}
	defer ch.Close()

	// List queues using QueueInspect on common names
	// Note: RabbitMQ Go client doesn't have a direct "list all queues" method
	// We need to use a workaround - try to inspect queues or use HTTP API
	// For now, we'll return information about what we can determine

	// Since the amqp091-go client doesn't provide a way to list all queues,
	// we'll attempt to declare a temporary queue and return available info
	// In production, you might want to use the RabbitMQ Management HTTP API

	queues := []map[string]interface{}{}

	// Try to get queue info by declaring passive queues
	// This is a limitation - we can only check specific queues
	// For a full list, the Management API would be needed

	// Return a note about the limitation
	return &executor.StepResult{
		Output: map[string]interface{}{
			"success": true,
			"queues":  queues,
			"note":    "Full queue listing requires RabbitMQ Management HTTP API. This operation can only verify specific queues.",
			"pattern": pattern,
		},
	}, nil
}

// QueueDeleteExecutor handles rabbitmq-queue-delete
type QueueDeleteExecutor struct{}

// Type returns the executor type
func (e *QueueDeleteExecutor) Type() string {
	return "rabbitmq-queue-delete"
}

// Execute runs the queue delete operation
func (e *QueueDeleteExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	// Get connection parameters
	rmqConfig := parseRabbitMQConfig(config)

	// Get queue parameters
	queue := resolver.ResolveString(getString(config, "queue"))
	ifUnused := getBool(config, "ifUnused", false)
	ifEmpty := getBool(config, "ifEmpty", false)

	if queue == "" {
		return nil, fmt.Errorf("queue name is required")
	}

	// Create channel
	_, ch, err := createChannel(rmqConfig)
	if err != nil {
		return nil, err
	}
	defer ch.Close()

	// Delete queue
	msgCount, err := ch.QueueDelete(queue, ifUnused, ifEmpty, false)
	if err != nil {
		return nil, fmt.Errorf("failed to delete queue: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":    true,
			"queue":      queue,
			"deletedMsgs": msgCount,
		},
	}, nil
}

// QueuePurgeExecutor handles rabbitmq-queue-purge
type QueuePurgeExecutor struct{}

// Type returns the executor type
func (e *QueuePurgeExecutor) Type() string {
	return "rabbitmq-queue-purge"
}

// Execute runs the queue purge operation
func (e *QueuePurgeExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	// Get connection parameters
	rmqConfig := parseRabbitMQConfig(config)

	// Get queue name
	queue := resolver.ResolveString(getString(config, "queue"))

	if queue == "" {
		return nil, fmt.Errorf("queue name is required")
	}

	// Create channel
	_, ch, err := createChannel(rmqConfig)
	if err != nil {
		return nil, err
	}
	defer ch.Close()

	// Purge queue
	msgCount, err := ch.QueuePurge(queue, false)
	if err != nil {
		return nil, fmt.Errorf("failed to purge queue: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":   true,
			"queue":     queue,
			"purgedMsgs": msgCount,
		},
	}, nil
}

// ============================================================================
// EXECUTORS - EXCHANGE OPERATIONS
// ============================================================================

// ExchangeCreateExecutor handles rabbitmq-exchange-create
type ExchangeCreateExecutor struct{}

// Type returns the executor type
func (e *ExchangeCreateExecutor) Type() string {
	return "rabbitmq-exchange-create"
}

// Execute runs the exchange create operation
func (e *ExchangeCreateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	// Get connection parameters
	rmqConfig := parseRabbitMQConfig(config)

	// Get exchange parameters
	exchange := resolver.ResolveString(getString(config, "exchange"))
	exchangeType := resolver.ResolveString(getString(config, "exchangeType"))
	durable := getBool(config, "durable", true)
	autoDelete := getBool(config, "autoDelete", false)
	internal := getBool(config, "internal", false)
	noWait := getBool(config, "noWait", false)
	argsJSON := getString(config, "args")

	if exchange == "" {
		return nil, fmt.Errorf("exchange name is required")
	}

	if exchangeType == "" {
		exchangeType = "direct"
	}

	// Create channel
	_, ch, err := createChannel(rmqConfig)
	if err != nil {
		return nil, err
	}
	defer ch.Close()

	// Parse arguments if provided
	var args amqp.Table
	if argsJSON != "" {
		if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
			return nil, fmt.Errorf("invalid arguments JSON: %w", err)
		}
	}

	// Declare exchange
	err = ch.ExchangeDeclare(exchange, exchangeType, durable, autoDelete, internal, noWait, args)
	if err != nil {
		return nil, fmt.Errorf("failed to create exchange: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":      true,
			"exchange":     exchange,
			"exchangeType": exchangeType,
			"durable":      durable,
		},
	}, nil
}

// ExchangeListExecutor handles rabbitmq-exchange-list
type ExchangeListExecutor struct{}

// Type returns the executor type
func (e *ExchangeListExecutor) Type() string {
	return "rabbitmq-exchange-list"
}

// Execute runs the exchange list operation
func (e *ExchangeListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	// Get connection parameters
	rmqConfig := parseRabbitMQConfig(config)

	// Get filter pattern
	pattern := resolver.ResolveString(getString(config, "pattern"))

	// Create channel
	_, ch, err := createChannel(rmqConfig)
	if err != nil {
		return nil, err
	}
	defer ch.Close()

	// List exchanges - similar limitation as queues
	// The amqp091-go client doesn't provide a direct "list all exchanges" method
	// We'll return common exchange types info

	exchanges := []map[string]interface{}{
		{"name": "", "type": "direct", "note": "Default exchange"},
		{"name": "amq.direct", "type": "direct"},
		{"name": "amq.fanout", "type": "fanout"},
		{"name": "amq.topic", "type": "topic"},
		{"name": "amq.headers", "type": "headers"},
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":   true,
			"exchanges": exchanges,
			"pattern":   pattern,
			"note":      "Full exchange listing requires RabbitMQ Management HTTP API. Showing default exchanges.",
		},
	}, nil
}

// ============================================================================
// EXECUTORS - BINDING OPERATIONS
// ============================================================================

// BindingCreateExecutor handles rabbitmq-binding-create
type BindingCreateExecutor struct{}

// Type returns the executor type
func (e *BindingCreateExecutor) Type() string {
	return "rabbitmq-binding-create"
}

// Execute runs the binding create operation
func (e *BindingCreateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	// Get connection parameters
	rmqConfig := parseRabbitMQConfig(config)

	// Get binding parameters
	queue := resolver.ResolveString(getString(config, "queue"))
	exchange := resolver.ResolveString(getString(config, "exchange"))
	routingKey := resolver.ResolveString(getString(config, "routingKey"))
	argsJSON := getString(config, "args")

	if queue == "" {
		return nil, fmt.Errorf("queue name is required")
	}
	if exchange == "" {
		return nil, fmt.Errorf("exchange name is required")
	}

	// Create channel
	_, ch, err := createChannel(rmqConfig)
	if err != nil {
		return nil, err
	}
	defer ch.Close()

	// Parse arguments if provided
	var args amqp.Table
	if argsJSON != "" {
		if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
			return nil, fmt.Errorf("invalid arguments JSON: %w", err)
		}
	}

	// Bind queue to exchange
	err = ch.QueueBind(queue, routingKey, exchange, false, args)
	if err != nil {
		return nil, fmt.Errorf("failed to create binding: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":    true,
			"queue":      queue,
			"exchange":   exchange,
			"routingKey": routingKey,
		},
	}, nil
}

// BindingListExecutor handles rabbitmq-binding-list
type BindingListExecutor struct{}

// Type returns the executor type
func (e *BindingListExecutor) Type() string {
	return "rabbitmq-binding-list"
}

// Execute runs the binding list operation
func (e *BindingListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	// Get connection parameters
	rmqConfig := parseRabbitMQConfig(config)

	// Get parameters
	targetType := resolver.ResolveString(getString(config, "targetType"))
	name := resolver.ResolveString(getString(config, "name"))

	if name == "" {
		return nil, fmt.Errorf("name is required")
	}

	// Create channel
	_, ch, err := createChannel(rmqConfig)
	if err != nil {
		return nil, err
	}
	defer ch.Close()

	// List bindings - the amqp091-go client has limited binding listing support
	// We can try to get bindings for a specific queue
	bindings := []map[string]interface{}{}

	if targetType == "queue" {
		// For queues, we can't directly list bindings with the AMQP client
		// This would require the Management HTTP API
		return &executor.StepResult{
			Output: map[string]interface{}{
				"success":    true,
				"targetType": targetType,
				"name":       name,
				"bindings":   bindings,
				"note":       "Full binding listing requires RabbitMQ Management HTTP API.",
			},
		}, nil
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":    true,
			"targetType": targetType,
			"name":       name,
			"bindings":   bindings,
			"note":       "Binding listing requires RabbitMQ Management HTTP API.",
		},
	}, nil
}
