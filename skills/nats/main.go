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
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

const (
	iconNATS = "radio"
)

// NATS clients cache
var (
	natsConnections = make(map[string]*nats.Conn)
	natsMux         sync.RWMutex
	jsClients       = make(map[string]jetstream.JetStream)
	jsMux           sync.RWMutex
)

func main() {
	// Get port from env or use default
	port := os.Getenv("SKILL_PORT")
	if port == "" {
		port = "50091"
	}

	// Create skill server
	server := grpc.NewSkillServer("skill-nats", "1.0.0")

	// Register NATS Core executors
	server.RegisterExecutorWithSchema("nats-publish", &NatsPublishExecutor{}, NatsPublishSchema)
	server.RegisterExecutorWithSchema("nats-subscribe", &NatsSubscribeExecutor{}, NatsSubscribeSchema)
	server.RegisterExecutorWithSchema("nats-request", &NatsRequestExecutor{}, NatsRequestSchema)

	// Register JetStream executors
	server.RegisterExecutorWithSchema("nats-jetstream-create", &NatsJetStreamCreateExecutor{}, NatsJetStreamCreateSchema)
	server.RegisterExecutorWithSchema("nats-jetstream-list", &NatsJetStreamListExecutor{}, NatsJetStreamListSchema)
	server.RegisterExecutorWithSchema("nats-stream-info", &NatsStreamInfoExecutor{}, NatsStreamInfoSchema)

	// Register Consumer executors
	server.RegisterExecutorWithSchema("nats-consumer-create", &NatsConsumerCreateExecutor{}, NatsConsumerCreateSchema)
	server.RegisterExecutorWithSchema("nats-consumer-list", &NatsConsumerListExecutor{}, NatsConsumerListSchema)

	fmt.Printf("Starting skill-nats gRPC server on port %s\n", port)
	if err := server.Serve(port); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to serve: %v\n", err)
		os.Exit(1)
	}
}

// ============================================================================
// NATS CONNECTION HELPERS
// ============================================================================

// NATSConfig holds NATS connection configuration
type NATSConfig struct {
	Servers         string
	Username        string
	Password        string
	Token           string
	CredentialsFile string
	TLSRequired     bool
}

// getNATSConnection returns a NATS connection (cached)
func getNATSConnection(cfg NATSConfig) (*nats.Conn, error) {
	cacheKey := fmt.Sprintf("%s:%s:%s", cfg.Servers, cfg.Username, cfg.CredentialsFile)

	natsMux.RLock()
	conn, ok := natsConnections[cacheKey]
	natsMux.RUnlock()

	if ok && conn != nil && conn.IsConnected() {
		return conn, nil
	}

	natsMux.Lock()
	defer natsMux.Unlock()

	// Double check
	if conn, ok := natsConnections[cacheKey]; ok && conn != nil && conn.IsConnected() {
		return conn, nil
	}

	// Close existing connection if present
	if conn, ok := natsConnections[cacheKey]; ok && conn != nil {
		conn.Close()
	}

	// Build connection options
	var opts []nats.Option

	if cfg.Username != "" && cfg.Password != "" {
		opts = append(opts, nats.UserInfo(cfg.Username, cfg.Password))
	}

	if cfg.Token != "" {
		opts = append(opts, nats.Token(cfg.Token))
	}

	if cfg.CredentialsFile != "" {
		opts = append(opts, nats.UserCredentials(cfg.CredentialsFile))
	}

	if cfg.TLSRequired {
		opts = append(opts, nats.Secure(nil))
	}

	opts = append(opts, nats.Name("axiom-skill-nats"))
	opts = append(opts, nats.Timeout(10*time.Second))
	opts = append(opts, nats.ReconnectWait(2*time.Second))
	opts = append(opts, nats.MaxReconnects(10))

	// Parse servers
	servers := strings.Split(cfg.Servers, ",")
	for i, s := range servers {
		servers[i] = strings.TrimSpace(s)
	}

	// Connect to NATS
	conn, err := nats.Connect(strings.Join(servers, ","), opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to NATS: %w", err)
	}

	// Cache the connection
	natsConnections[cacheKey] = conn
	return conn, nil
}

// getJetStreamClient returns a JetStream client (cached)
func getJetStreamClient(cfg NATSConfig) (jetstream.JetStream, error) {
	cacheKey := fmt.Sprintf("%s:%s:%s:js", cfg.Servers, cfg.Username, cfg.CredentialsFile)

	jsMux.RLock()
	js, ok := jsClients[cacheKey]
	jsMux.RUnlock()

	if ok {
		return js, nil
	}

	conn, err := getNATSConnection(cfg)
	if err != nil {
		return nil, err
	}

	jsMux.Lock()
	defer jsMux.Unlock()

	// Double check
	if js, ok := jsClients[cacheKey]; ok {
		return js, nil
	}

	// Create JetStream context
	jsClient, err := jetstream.New(conn)
	if err != nil {
		return nil, fmt.Errorf("failed to create JetStream client: %w", err)
	}

	jsClients[cacheKey] = jsClient
	return jsClient, nil
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

// parseNATSConfig extracts NATS configuration from config map
func parseNATSConfig(config map[string]interface{}) NATSConfig {
	return NATSConfig{
		Servers:         getString(config, "natsServers"),
		Username:        getString(config, "natsUsername"),
		Password:        getString(config, "natsPassword"),
		Token:           getString(config, "natsToken"),
		CredentialsFile: getString(config, "natsCredentialsFile"),
		TLSRequired:     getBool(config, "natsTLSRequired", false),
	}
}

// ============================================================================
// SCHEMAS
// ============================================================================

// NatsPublishSchema is the UI schema for nats-publish
var NatsPublishSchema = resolver.NewSchemaBuilder("nats-publish").
	WithName("NATS Publish").
	WithCategory("action").
	WithIcon(iconNATS).
	WithDescription("Publish a message to a NATS subject").
	AddSection("NATS Connection").
		AddExpressionField("natsServers", "Servers",
			resolver.WithRequired(),
			resolver.WithPlaceholder("nats://localhost:4222"),
			resolver.WithHint("NATS server URLs (comma-separated)"),
		).
		AddExpressionField("natsUsername", "Username",
			resolver.WithPlaceholder("username"),
			resolver.WithHint("NATS username (optional)"),
		).
		AddExpressionField("natsPassword", "Password",
			resolver.WithSensitive(),
			resolver.WithHint("NATS password (optional)"),
		).
		AddExpressionField("natsToken", "Auth Token",
			resolver.WithSensitive(),
			resolver.WithHint("NATS auth token (optional, alternative to username/password)"),
		).
		AddExpressionField("natsCredentialsFile", "Credentials File",
			resolver.WithPlaceholder("/path/to/creds.jwt"),
			resolver.WithHint("Path to NATS credentials file (optional)"),
		).
		EndSection().
	AddSection("Message").
		AddExpressionField("subject", "Subject",
			resolver.WithRequired(),
			resolver.WithPlaceholder("example.subject"),
			resolver.WithHint("NATS subject to publish to"),
		).
		AddTextareaField("payload", "Payload",
			resolver.WithRequired(),
			resolver.WithRows(6),
			resolver.WithPlaceholder("Message content..."),
			resolver.WithHint("Message payload (supports {{bindings.xxx}} and {{prev.xxx}})"),
		).
		AddExpressionField("contentType", "Content Type",
			resolver.WithPlaceholder("text/plain"),
			resolver.WithHint("Content-Type header (optional)"),
		).
		EndSection().
	AddSection("Headers").
		AddKeyValueField("headers", "Headers",
			resolver.WithHint("Custom headers as key-value pairs"),
		).
		EndSection().
	AddSection("Options").
		AddToggleField("useJetStream", "Use JetStream",
			resolver.WithDefault(false),
			resolver.WithHint("Enable JetStream publish with ack"),
		).
		AddNumberField("timeout", "Timeout (ms)",
			resolver.WithDefault(5000),
			resolver.WithMinMax(100, 60000),
			resolver.WithHint("Publish timeout in milliseconds"),
		).
		EndSection().
	Build()

// NatsSubscribeSchema is the UI schema for nats-subscribe
var NatsSubscribeSchema = resolver.NewSchemaBuilder("nats-subscribe").
	WithName("NATS Subscribe").
	WithCategory("action").
	WithIcon(iconNATS).
	WithDescription("Subscribe to a NATS subject and receive messages").
	AddSection("NATS Connection").
		AddExpressionField("natsServers", "Servers",
			resolver.WithRequired(),
			resolver.WithPlaceholder("nats://localhost:4222"),
			resolver.WithHint("NATS server URLs (comma-separated)"),
		).
		AddExpressionField("natsUsername", "Username",
			resolver.WithPlaceholder("username"),
			resolver.WithHint("NATS username (optional)"),
		).
		AddExpressionField("natsPassword", "Password",
			resolver.WithSensitive(),
			resolver.WithHint("NATS password (optional)"),
		).
		AddExpressionField("natsToken", "Auth Token",
			resolver.WithSensitive(),
			resolver.WithHint("NATS auth token (optional)"),
		).
		EndSection().
	AddSection("Subscription").
		AddExpressionField("subject", "Subject",
			resolver.WithRequired(),
			resolver.WithPlaceholder("example.subject"),
			resolver.WithHint("NATS subject to subscribe to (supports wildcards like *.subject)"),
		).
		AddExpressionField("queueGroup", "Queue Group",
			resolver.WithPlaceholder("my-queue"),
			resolver.WithHint("Queue group name for load-balanced subscriptions (optional)"),
		).
		EndSection().
	AddSection("Options").
		AddSelectField("subscriptionType", "Subscription Type",
			[]resolver.SelectOption{
				{Label: "Core NATS", Value: "core"},
				{Label: "JetStream Consumer", Value: "jetstream"},
			},
			resolver.WithDefault("core"),
			resolver.WithHint("Type of subscription"),
		).
		AddExpressionField("streamName", "Stream Name",
			resolver.WithHint("JetStream stream name (required for JetStream subscription)"),
		).
		AddExpressionField("consumerName", "Consumer Name",
			resolver.WithHint("JetStream consumer name (optional, creates if not exists)"),
		).
		AddNumberField("maxMessages", "Max Messages",
			resolver.WithDefault(100),
			resolver.WithMinMax(1, 10000),
			resolver.WithHint("Maximum number of messages to receive"),
		).
		AddNumberField("timeout", "Timeout (ms)",
			resolver.WithDefault(10000),
			resolver.WithMinMax(1000, 120000),
			resolver.WithHint("Timeout to wait for messages"),
		).
		AddToggleField("autoAck", "Auto Acknowledge",
			resolver.WithDefault(true),
			resolver.WithHint("Automatically acknowledge JetStream messages"),
		).
		EndSection().
	Build()

// NatsRequestSchema is the UI schema for nats-request
var NatsRequestSchema = resolver.NewSchemaBuilder("nats-request").
	WithName("NATS Request-Reply").
	WithCategory("action").
	WithIcon(iconNATS).
	WithDescription("Send a request and wait for a reply").
	AddSection("NATS Connection").
		AddExpressionField("natsServers", "Servers",
			resolver.WithRequired(),
			resolver.WithPlaceholder("nats://localhost:4222"),
			resolver.WithHint("NATS server URLs (comma-separated)"),
		).
		AddExpressionField("natsUsername", "Username",
			resolver.WithPlaceholder("username"),
			resolver.WithHint("NATS username (optional)"),
		).
		AddExpressionField("natsPassword", "Password",
			resolver.WithSensitive(),
			resolver.WithHint("NATS password (optional)"),
		).
		AddExpressionField("natsToken", "Auth Token",
			resolver.WithSensitive(),
			resolver.WithHint("NATS auth token (optional)"),
		).
		EndSection().
	AddSection("Request").
		AddExpressionField("subject", "Subject",
			resolver.WithRequired(),
			resolver.WithPlaceholder("example.request"),
			resolver.WithHint("NATS subject for the request"),
		).
		AddTextareaField("payload", "Payload",
			resolver.WithRequired(),
			resolver.WithRows(6),
			resolver.WithPlaceholder("Request content..."),
			resolver.WithHint("Request payload"),
		).
		AddExpressionField("contentType", "Content Type",
			resolver.WithPlaceholder("application/json"),
			resolver.WithHint("Content-Type header"),
		).
		EndSection().
	AddSection("Options").
		AddNumberField("timeout", "Timeout (ms)",
			resolver.WithDefault(5000),
			resolver.WithMinMax(100, 60000),
			resolver.WithHint("Timeout to wait for reply"),
		).
		EndSection().
	Build()

// NatsJetStreamCreateSchema is the UI schema for nats-jetstream-create
var NatsJetStreamCreateSchema = resolver.NewSchemaBuilder("nats-jetstream-create").
	WithName("Create JetStream Stream").
	WithCategory("action").
	WithIcon(iconNATS).
	WithDescription("Create or update a JetStream stream").
	AddSection("NATS Connection").
		AddExpressionField("natsServers", "Servers",
			resolver.WithRequired(),
			resolver.WithPlaceholder("nats://localhost:4222"),
			resolver.WithHint("NATS server URLs"),
		).
		AddExpressionField("natsUsername", "Username",
			resolver.WithPlaceholder("username"),
		).
		AddExpressionField("natsPassword", "Password",
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Stream Configuration").
		AddExpressionField("streamName", "Stream Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("MY_STREAM"),
			resolver.WithHint("Name of the stream to create"),
		).
		AddTagsField("subjects", "Subjects",
			resolver.WithRequired(),
			resolver.WithHint("Subjects to capture (e.g., events.>, logs.*)"),
		).
		AddSelectField("storage", "Storage Type",
			[]resolver.SelectOption{
				{Label: "File", Value: "file"},
				{Label: "Memory", Value: "memory"},
			},
			resolver.WithDefault("file"),
			resolver.WithHint("Storage backend for the stream"),
		).
		AddSelectField("retention", "Retention Policy",
			[]resolver.SelectOption{
				{Label: "Limits", Value: "limits"},
				{Label: "Interest", Value: "interest"},
				{Label: "Work Queue", Value: "workqueue"},
			},
			resolver.WithDefault("limits"),
			resolver.WithHint("Message retention policy"),
		).
		EndSection().
	AddSection("Limits").
		AddNumberField("maxMessages", "Max Messages",
			resolver.WithDefault(-1),
			resolver.WithHint("Maximum number of messages (-1 for unlimited)"),
		).
		AddNumberField("maxBytes", "Max Bytes",
			resolver.WithDefault(-1),
			resolver.WithHint("Maximum size in bytes (-1 for unlimited)"),
		).
		AddNumberField("maxAge", "Max Age (hours)",
			resolver.WithDefault(0),
			resolver.WithHint("Maximum message age (0 for no limit)"),
		).
		AddNumberField("maxMsgSize", "Max Message Size",
			resolver.WithDefault(-1),
			resolver.WithHint("Maximum individual message size (-1 for unlimited)"),
		).
		EndSection().
	AddSection("Replication").
		AddNumberField("replicas", "Replicas",
			resolver.WithDefault(1),
			resolver.WithHint("Number of replicas for high availability"),
		).
		AddToggleField("deduplication", "Deduplication",
			resolver.WithDefault(true),
			resolver.WithHint("Enable message deduplication"),
		).
		AddNumberField("dedupWindow", "Dedup Window (ms)",
			resolver.WithDefault(120000),
			resolver.WithHint("Deduplication window in milliseconds"),
		).
		EndSection().
	Build()

// NatsJetStreamListSchema is the UI schema for nats-jetstream-list
var NatsJetStreamListSchema = resolver.NewSchemaBuilder("nats-jetstream-list").
	WithName("List JetStream Streams").
	WithCategory("action").
	WithIcon(iconNATS).
	WithDescription("List all JetStream streams").
	AddSection("NATS Connection").
		AddExpressionField("natsServers", "Servers",
			resolver.WithRequired(),
			resolver.WithPlaceholder("nats://localhost:4222"),
			resolver.WithHint("NATS server URLs"),
		).
		AddExpressionField("natsUsername", "Username",
			resolver.WithPlaceholder("username"),
		).
		AddExpressionField("natsPassword", "Password",
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Options").
		AddExpressionField("subjectFilter", "Subject Filter",
			resolver.WithPlaceholder("events.>"),
			resolver.WithHint("Filter streams by subject pattern (optional)"),
		).
		EndSection().
	Build()

// NatsStreamInfoSchema is the UI schema for nats-stream-info
var NatsStreamInfoSchema = resolver.NewSchemaBuilder("nats-stream-info").
	WithName("Get Stream Info").
	WithCategory("action").
	WithIcon(iconNATS).
	WithDescription("Get detailed information about a JetStream stream").
	AddSection("NATS Connection").
		AddExpressionField("natsServers", "Servers",
			resolver.WithRequired(),
			resolver.WithPlaceholder("nats://localhost:4222"),
		).
		AddExpressionField("natsUsername", "Username",
			resolver.WithPlaceholder("username"),
		).
		AddExpressionField("natsPassword", "Password",
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Stream").
		AddExpressionField("streamName", "Stream Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("MY_STREAM"),
			resolver.WithHint("Name of the stream"),
		).
		EndSection().
	Build()

// NatsConsumerCreateSchema is the UI schema for nats-consumer-create
var NatsConsumerCreateSchema = resolver.NewSchemaBuilder("nats-consumer-create").
	WithName("Create JetStream Consumer").
	WithCategory("action").
	WithIcon(iconNATS).
	WithDescription("Create or update a JetStream consumer").
	AddSection("NATS Connection").
		AddExpressionField("natsServers", "Servers",
			resolver.WithRequired(),
			resolver.WithPlaceholder("nats://localhost:4222"),
		).
		AddExpressionField("natsUsername", "Username",
			resolver.WithPlaceholder("username"),
		).
		AddExpressionField("natsPassword", "Password",
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Consumer Configuration").
		AddExpressionField("streamName", "Stream Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("MY_STREAM"),
			resolver.WithHint("Name of the stream"),
		).
		AddExpressionField("consumerName", "Consumer Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-consumer"),
			resolver.WithHint("Name of the consumer"),
		).
		AddSelectField("deliverPolicy", "Deliver Policy",
			[]resolver.SelectOption{
				{Label: "All", Value: "all"},
				{Label: "Last", Value: "last"},
				{Label: "New", Value: "new"},
				{Label: "By Start Sequence", Value: "by_start_sequence"},
				{Label: "By Start Time", Value: "by_start_time"},
				{Label: "Last Per Subject", Value: "last_per_subject"},
			},
			resolver.WithDefault("all"),
			resolver.WithHint("Where to start delivering messages"),
		).
		AddSelectField("ackPolicy", "Ack Policy",
			[]resolver.SelectOption{
				{Label: "Explicit", Value: "explicit"},
				{Label: "None", Value: "none"},
				{Label: "All", Value: "all"},
			},
			resolver.WithDefault("explicit"),
			resolver.WithHint("Message acknowledgment policy"),
		).
		EndSection().
	AddSection("Delivery").
		AddExpressionField("deliverSubject", "Deliver Subject",
			resolver.WithPlaceholder("my.deliver.subject"),
			resolver.WithHint("Subject for push-based delivery (leave empty for pull-based)"),
		).
		AddNumberField("maxDeliver", "Max Deliveries",
			resolver.WithDefault(-1),
			resolver.WithHint("Maximum delivery attempts (-1 for unlimited)"),
		).
		AddNumberField("rateLimitBps", "Rate Limit (bps)",
			resolver.WithDefault(0),
			resolver.WithHint("Rate limit in bits per second (0 for no limit)"),
		).
		EndSection().
	AddSection("Acknowledgment").
		AddNumberField("ackWait", "Ack Wait (seconds)",
			resolver.WithDefault(30),
			resolver.WithHint("Time to wait for acknowledgment"),
		).
		AddNumberField("maxAckPending", "Max Ack Pending",
			resolver.WithDefault(1000),
			resolver.WithHint("Maximum pending acknowledgments"),
		).
		EndSection().
	AddSection("Filtering").
		AddExpressionField("filterSubject", "Filter Subject",
			resolver.WithPlaceholder("events.>"),
			resolver.WithHint("Filter messages by subject pattern"),
		).
		EndSection().
	Build()

// NatsConsumerListSchema is the UI schema for nats-consumer-list
var NatsConsumerListSchema = resolver.NewSchemaBuilder("nats-consumer-list").
	WithName("List Consumers").
	WithCategory("action").
	WithIcon(iconNATS).
	WithDescription("List all consumers for a JetStream stream").
	AddSection("NATS Connection").
		AddExpressionField("natsServers", "Servers",
			resolver.WithRequired(),
			resolver.WithPlaceholder("nats://localhost:4222"),
		).
		AddExpressionField("natsUsername", "Username",
			resolver.WithPlaceholder("username"),
		).
		AddExpressionField("natsPassword", "Password",
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Stream").
		AddExpressionField("streamName", "Stream Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("MY_STREAM"),
			resolver.WithHint("Name of the stream"),
		).
		EndSection().
	Build()

// ============================================================================
// NATS PUBLISH EXECUTOR
// ============================================================================

// NatsPublishExecutor handles nats-publish node type
type NatsPublishExecutor struct{}

func (e *NatsPublishExecutor) Type() string { return "nats-publish" }

func (e *NatsPublishExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	natsCfg := parseNATSConfig(config)
	subject := getString(config, "subject")
	payload := getString(config, "payload")
	contentType := getString(config, "contentType")
	headers := getMap(config, "headers")
	useJetStream := getBool(config, "useJetStream", false)
	timeout := time.Duration(getInt(config, "timeout", 5000)) * time.Millisecond

	if subject == "" {
		return &executor.StepResult{Output: map[string]interface{}{"success": false, "error": "subject is required"}}, nil
	}

	// Connect to NATS
	conn, err := getNATSConnection(natsCfg)
	if err != nil {
		return &executor.StepResult{Output: map[string]interface{}{"success": false, "error": err.Error()}}, nil
	}

	// Create message
	msg := &nats.Msg{
		Subject: subject,
		Data:    []byte(payload),
	}

	// Add headers if provided
	if len(headers) > 0 {
		msg.Header = make(nats.Header)
		for k, v := range headers {
			if s, ok := v.(string); ok {
				msg.Header.Add(k, s)
			}
		}
	}

	// Add content type if provided
	if contentType != "" {
		if msg.Header == nil {
			msg.Header = make(nats.Header)
		}
		msg.Header.Set("Content-Type", contentType)
	}

	var ackResult interface{}

	if useJetStream {
		// Use JetStream publish with acknowledgment
		js, err := getJetStreamClient(natsCfg)
		if err != nil {
			return &executor.StepResult{Output: map[string]interface{}{"success": false, "error": err.Error()}}, nil
		}

		jsCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()

		ack, err := js.PublishMsg(jsCtx, msg)
		if err != nil {
			return &executor.StepResult{Output: map[string]interface{}{"success": false, "error": err.Error()}}, nil
		}

		ackResult = map[string]interface{}{
			"stream":    ack.Stream,
			"sequence":  ack.Sequence,
			"domain":    ack.Domain,
			"duplicate": ack.Duplicate,
		}
	} else {
		// Core NATS publish
		err = conn.PublishMsg(msg)
		if err != nil {
			return &executor.StepResult{Output: map[string]interface{}{"success": false, "error": err.Error()}}, nil
		}
		ackResult = "published"
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success": true,
			"subject": subject,
			"ack":     ackResult,
		},
	}, nil
}

// ============================================================================
// NATS SUBSCRIBE EXECUTOR
// ============================================================================

// NatsSubscribeExecutor handles nats-subscribe node type
type NatsSubscribeExecutor struct{}

func (e *NatsSubscribeExecutor) Type() string { return "nats-subscribe" }

func (e *NatsSubscribeExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	natsCfg := parseNATSConfig(config)
	subject := getString(config, "subject")
	queueGroup := getString(config, "queueGroup")
	subscriptionType := getString(config, "subscriptionType")
	if subscriptionType == "" {
		subscriptionType = "core"
	}
	streamName := getString(config, "streamName")
	consumerName := getString(config, "consumerName")
	maxMessages := getInt(config, "maxMessages", 100)
	timeout := time.Duration(getInt(config, "timeout", 10000)) * time.Millisecond
	autoAck := getBool(config, "autoAck", true)

	if subject == "" {
		return &executor.StepResult{Output: map[string]interface{}{"success": false, "error": "subject is required"}}, nil
	}

	conn, err := getNATSConnection(natsCfg)
	if err != nil {
		return &executor.StepResult{Output: map[string]interface{}{"success": false, "error": err.Error()}}, nil
	}

	var messages []map[string]interface{}

	if subscriptionType == "jetstream" {
		// JetStream subscription
		if streamName == "" {
			return &executor.StepResult{Output: map[string]interface{}{"success": false, "error": "streamName is required for JetStream subscription"}}, nil
		}

		js, err := getJetStreamClient(natsCfg)
		if err != nil {
			return &executor.StepResult{Output: map[string]interface{}{"success": false, "error": err.Error()}}, nil
		}

		var consumer jetstream.Consumer
		if consumerName != "" {
			consumer, err = js.Consumer(ctx, streamName, consumerName)
			if err != nil {
				// Consumer doesn't exist, try to create it
				consumerCfg := jetstream.ConsumerConfig{
					Name:      consumerName,
					AckPolicy: jetstream.AckExplicitPolicy,
				}
				consumer, err = js.CreateConsumer(ctx, streamName, consumerCfg)
				if err != nil {
					return &executor.StepResult{Output: map[string]interface{}{"success": false, "error": fmt.Sprintf("failed to get/create consumer: %v", err)}}, nil
				}
			}
		} else {
			// Create ephemeral consumer
			consumerCfg := jetstream.ConsumerConfig{
				AckPolicy: jetstream.AckExplicitPolicy,
			}
			consumer, err = js.CreateConsumer(ctx, streamName, consumerCfg)
			if err != nil {
				return &executor.StepResult{Output: map[string]interface{}{"success": false, "error": fmt.Sprintf("failed to create consumer: %v", err)}}, nil
			}
		}

		// Fetch messages
		iter, err := consumer.Fetch(maxMessages, jetstream.FetchMaxWait(timeout))
		if err != nil {
			return &executor.StepResult{Output: map[string]interface{}{"success": false, "error": err.Error()}}, nil
		}

		for msg := range iter.Messages() {
			metadata, err := msg.Metadata()
			if err != nil {
				continue
			}

			msgData := map[string]interface{}{
				"subject":  msg.Subject(),
				"data":     string(msg.Data()),
				"sequence": metadata.Sequence.Stream,
			}

			// Get headers
			headers := make(map[string]string)
			for k, v := range msg.Headers() {
				if len(v) > 0 {
					headers[k] = v[0]
				}
			}
			if len(headers) > 0 {
				msgData["headers"] = headers
			}

			if autoAck {
				msg.Ack()
			}

			messages = append(messages, msgData)
		}
	} else {
		// Core NATS subscription - use synchronous subscription
		var sub *nats.Subscription
		if queueGroup != "" {
			sub, err = conn.QueueSubscribeSync(subject, queueGroup)
		} else {
			sub, err = conn.SubscribeSync(subject)
		}

		if err != nil {
			return &executor.StepResult{Output: map[string]interface{}{"success": false, "error": err.Error()}}, nil
		}
		defer sub.Unsubscribe()

		// Collect messages
		received := 0
		for received < maxMessages {
			msg, err := sub.NextMsg(100 * time.Millisecond)
			if err != nil {
				// Timeout or error - stop collecting
				break
			}

			msgData := map[string]interface{}{
				"subject": msg.Subject,
				"data":    string(msg.Data),
			}

			if len(msg.Header) > 0 {
				headers := make(map[string]string)
				for k, v := range msg.Header {
					if len(v) > 0 {
						headers[k] = v[0]
					}
				}
				msgData["headers"] = headers
			}

			messages = append(messages, msgData)
			received++
		}
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":  true,
			"subject":  subject,
			"messages": messages,
			"count":    len(messages),
		},
	}, nil
}

// ============================================================================
// NATS REQUEST EXECUTOR
// ============================================================================

// NatsRequestExecutor handles nats-request node type
type NatsRequestExecutor struct{}

func (e *NatsRequestExecutor) Type() string { return "nats-request" }

func (e *NatsRequestExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	natsCfg := parseNATSConfig(config)
	subject := getString(config, "subject")
	payload := getString(config, "payload")
	contentType := getString(config, "contentType")
	timeout := time.Duration(getInt(config, "timeout", 5000)) * time.Millisecond

	if subject == "" {
		return &executor.StepResult{Output: map[string]interface{}{"success": false, "error": "subject is required"}}, nil
	}

	conn, err := getNATSConnection(natsCfg)
	if err != nil {
		return &executor.StepResult{Output: map[string]interface{}{"success": false, "error": err.Error()}}, nil
	}

	// Create message
	msg := &nats.Msg{
		Subject: subject,
		Data:    []byte(payload),
	}

	if contentType != "" {
		msg.Header = make(nats.Header)
		msg.Header.Set("Content-Type", contentType)
	}

	// Core NATS request-reply
	replyMsg, err := conn.Request(msg.Subject, msg.Data, timeout)
	if err != nil {
		return &executor.StepResult{Output: map[string]interface{}{"success": false, "error": err.Error()}}, nil
	}

	replyData := string(replyMsg.Data)
	var replyHeaders map[string]string

	if len(replyMsg.Header) > 0 {
		replyHeaders = make(map[string]string)
		for k, v := range replyMsg.Header {
			if len(v) > 0 {
				replyHeaders[k] = v[0]
			}
		}
	}

	output := map[string]interface{}{
		"success": true,
		"subject": subject,
		"reply":   replyData,
	}

	if len(replyHeaders) > 0 {
		output["replyHeaders"] = replyHeaders
	}

	return &executor.StepResult{Output: output}, nil
}

// ============================================================================
// JETSTREAM CREATE EXECUTOR
// ============================================================================

// NatsJetStreamCreateExecutor handles nats-jetstream-create node type
type NatsJetStreamCreateExecutor struct{}

func (e *NatsJetStreamCreateExecutor) Type() string { return "nats-jetstream-create" }

func (e *NatsJetStreamCreateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	natsCfg := parseNATSConfig(config)
	streamName := getString(config, "streamName")
	subjects := getStringSlice(config, "subjects")
	storage := getString(config, "storage")
	if storage == "" {
		storage = "file"
	}
	retention := getString(config, "retention")
	if retention == "" {
		retention = "limits"
	}
	maxMessages := getInt64(config, "maxMessages", -1)
	maxBytes := getInt64(config, "maxBytes", -1)
	maxAge := getInt(config, "maxAge", 0)
	maxMsgSize := getInt(config, "maxMsgSize", -1)
	replicas := getInt(config, "replicas", 1)
	deduplication := getBool(config, "deduplication", true)
	dedupWindow := getInt(config, "dedupWindow", 120000)

	if streamName == "" {
		return &executor.StepResult{Output: map[string]interface{}{"success": false, "error": "streamName is required"}}, nil
	}

	if len(subjects) == 0 {
		return &executor.StepResult{Output: map[string]interface{}{"success": false, "error": "subjects is required"}}, nil
	}

	js, err := getJetStreamClient(natsCfg)
	if err != nil {
		return &executor.StepResult{Output: map[string]interface{}{"success": false, "error": err.Error()}}, nil
	}

	// Map storage type
	var storageType jetstream.StorageType
	switch storage {
	case "memory":
		storageType = jetstream.MemoryStorage
	default:
		storageType = jetstream.FileStorage
	}

	// Map retention policy
	var retentionPolicy jetstream.RetentionPolicy
	switch retention {
	case "interest":
		retentionPolicy = jetstream.InterestPolicy
	case "workqueue":
		retentionPolicy = jetstream.WorkQueuePolicy
	default:
		retentionPolicy = jetstream.LimitsPolicy
	}

	// Build stream config
	streamCfg := jetstream.StreamConfig{
		Name:      streamName,
		Subjects:  subjects,
		Storage:   storageType,
		Retention: retentionPolicy,
		MaxMsgs:   maxMessages,
		MaxBytes:  maxBytes,
		Replicas:  replicas,
		Discard:   jetstream.DiscardOld,
	}

	// Set max message size if specified
	if maxMsgSize > 0 {
		streamCfg.MaxMsgSize = int32(maxMsgSize)
	}

	// Set deduplication window (0 disables deduplication)
	if deduplication && dedupWindow > 0 {
		streamCfg.Duplicates = time.Duration(dedupWindow) * time.Millisecond
	} else {
		streamCfg.Duplicates = 0
	}

	if maxAge > 0 {
		streamCfg.MaxAge = time.Duration(maxAge) * time.Hour
	}

	// Create or update stream
	jsCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	stream, err := js.CreateStream(jsCtx, streamCfg)
	if err != nil {
		// Try to update if stream already exists
		stream, err = js.UpdateStream(jsCtx, streamCfg)
		if err != nil {
			return &executor.StepResult{Output: map[string]interface{}{"success": false, "error": err.Error()}}, nil
		}
	}

	info := stream.CachedInfo()

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":   true,
			"stream":    streamName,
			"created":   info.Created.UnixNano(),
			"subjects":  subjects,
			"storage":   storage,
			"retention": retention,
			"replicas":  replicas,
		},
	}, nil
}

// ============================================================================
// JETSTREAM LIST EXECUTOR
// ============================================================================

// NatsJetStreamListExecutor handles nats-jetstream-list node type
type NatsJetStreamListExecutor struct{}

func (e *NatsJetStreamListExecutor) Type() string { return "nats-jetstream-list" }

func (e *NatsJetStreamListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	natsCfg := parseNATSConfig(config)
	subjectFilter := getString(config, "subjectFilter")

	js, err := getJetStreamClient(natsCfg)
	if err != nil {
		return &executor.StepResult{Output: map[string]interface{}{"success": false, "error": err.Error()}}, nil
	}

	jsCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	var streams []map[string]interface{}

	// List all streams - the lister returns StreamInfo directly
	streamLister := js.ListStreams(jsCtx)
	for info := range streamLister.Info() {
		// Apply subject filter if provided
		if subjectFilter != "" {
			matched := false
			for _, subj := range info.Config.Subjects {
				if matchSubject(subj, subjectFilter) {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}

		streamData := map[string]interface{}{
			"name":      info.Config.Name,
			"subjects":  info.Config.Subjects,
			"storage":   info.Config.Storage.String(),
			"retention": info.Config.Retention.String(),
			"replicas":  info.Config.Replicas,
			"messages":  info.State.Msgs,
			"bytes":     info.State.Bytes,
			"created":   info.Created.UnixNano(),
		}

		streams = append(streams, streamData)
	}

	if err := streamLister.Err(); err != nil {
		return &executor.StepResult{Output: map[string]interface{}{"success": false, "error": err.Error()}}, nil
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success": true,
			"streams": streams,
			"count":   len(streams),
		},
	}, nil
}

// ============================================================================
// STREAM INFO EXECUTOR
// ============================================================================

// NatsStreamInfoExecutor handles nats-stream-info node type
type NatsStreamInfoExecutor struct{}

func (e *NatsStreamInfoExecutor) Type() string { return "nats-stream-info" }

func (e *NatsStreamInfoExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	natsCfg := parseNATSConfig(config)
	streamName := getString(config, "streamName")

	if streamName == "" {
		return &executor.StepResult{Output: map[string]interface{}{"success": false, "error": "streamName is required"}}, nil
	}

	js, err := getJetStreamClient(natsCfg)
	if err != nil {
		return &executor.StepResult{Output: map[string]interface{}{"success": false, "error": err.Error()}}, nil
	}

	jsCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	stream, err := js.Stream(jsCtx, streamName)
	if err != nil {
		return &executor.StepResult{Output: map[string]interface{}{"success": false, "error": err.Error()}}, nil
	}

	info := stream.CachedInfo()

	// Get consumer count
	consumerCount := 0
	consumerLister := stream.ListConsumers(jsCtx)
	for range consumerLister.Info() {
		consumerCount++
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success": true,
			"info": map[string]interface{}{
				"name":          info.Config.Name,
				"subjects":      info.Config.Subjects,
				"storage":       info.Config.Storage.String(),
				"retention":     info.Config.Retention.String(),
				"replicas":      info.Config.Replicas,
				"maxMessages":   info.Config.MaxMsgs,
				"maxBytes":      info.Config.MaxBytes,
				"maxAge":        info.Config.MaxAge.Nanoseconds(),
				"maxMsgSize":    info.Config.MaxMsgSize,
				"deduplication": info.Config.Duplicates > 0,
				"dedupWindow":   info.Config.Duplicates.Nanoseconds(),
				"state": map[string]interface{}{
					"messages":      info.State.Msgs,
					"bytes":         info.State.Bytes,
					"firstSeq":      info.State.FirstSeq,
					"lastSeq":       info.State.LastSeq,
					"consumerCount": consumerCount,
					"numSubjects":   info.State.NumSubjects,
				},
				"created": info.Created.UnixNano(),
			},
		},
	}, nil
}

// ============================================================================
// CONSUMER CREATE EXECUTOR
// ============================================================================

// NatsConsumerCreateExecutor handles nats-consumer-create node type
type NatsConsumerCreateExecutor struct{}

func (e *NatsConsumerCreateExecutor) Type() string { return "nats-consumer-create" }

func (e *NatsConsumerCreateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	natsCfg := parseNATSConfig(config)
	streamName := getString(config, "streamName")
	consumerName := getString(config, "consumerName")
	deliverPolicy := getString(config, "deliverPolicy")
	if deliverPolicy == "" {
		deliverPolicy = "all"
	}
	ackPolicy := getString(config, "ackPolicy")
	if ackPolicy == "" {
		ackPolicy = "explicit"
	}
	deliverSubject := getString(config, "deliverSubject")
	maxDeliver := getInt(config, "maxDeliver", -1)
	rateLimitBps := getInt64(config, "rateLimitBps", 0)
	ackWait := getInt(config, "ackWait", 30)
	maxAckPending := getInt(config, "maxAckPending", 1000)
	filterSubject := getString(config, "filterSubject")

	if streamName == "" {
		return &executor.StepResult{Output: map[string]interface{}{"success": false, "error": "streamName is required"}}, nil
	}

	if consumerName == "" {
		return &executor.StepResult{Output: map[string]interface{}{"success": false, "error": "consumerName is required"}}, nil
	}

	js, err := getJetStreamClient(natsCfg)
	if err != nil {
		return &executor.StepResult{Output: map[string]interface{}{"success": false, "error": err.Error()}}, nil
	}

	// Map deliver policy
	var dp jetstream.DeliverPolicy
	switch deliverPolicy {
	case "last":
		dp = jetstream.DeliverLastPolicy
	case "new":
		dp = jetstream.DeliverNewPolicy
	case "by_start_sequence":
		dp = jetstream.DeliverByStartSequencePolicy
	case "by_start_time":
		dp = jetstream.DeliverByStartTimePolicy
	case "last_per_subject":
		dp = jetstream.DeliverLastPerSubjectPolicy
	default:
		dp = jetstream.DeliverAllPolicy
	}

	// Map ack policy
	var ap jetstream.AckPolicy
	switch ackPolicy {
	case "none":
		ap = jetstream.AckNonePolicy
	case "all":
		ap = jetstream.AckAllPolicy
	default:
		ap = jetstream.AckExplicitPolicy
	}

	consumerCfg := jetstream.ConsumerConfig{
		Name:          consumerName,
		DeliverPolicy: dp,
		AckPolicy:     ap,
		AckWait:       time.Duration(ackWait) * time.Second,
		MaxDeliver:    maxDeliver,
		MaxAckPending: maxAckPending,
		RateLimit:     uint64(rateLimitBps),
		FilterSubject: filterSubject,
	}

	// For push consumers, set deliver subject
	if deliverSubject != "" {
		consumerCfg.Durable = consumerName
		// Note: In the new API, push consumers are created differently
		// We'll create a pull consumer and the user can use the fetch API
	}

	jsCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	consumer, err := js.CreateConsumer(jsCtx, streamName, consumerCfg)
	if err != nil {
		// Try to get existing consumer
		consumer, err = js.Consumer(jsCtx, streamName, consumerName)
		if err != nil {
			return &executor.StepResult{Output: map[string]interface{}{"success": false, "error": err.Error()}}, nil
		}
	}

	info := consumer.CachedInfo()

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":       true,
			"stream":        streamName,
			"consumer":      consumerName,
			"deliverPolicy": deliverPolicy,
			"ackPolicy":     ackPolicy,
			"created":       info.Created.UnixNano(),
		},
	}, nil
}

// ============================================================================
// CONSUMER LIST EXECUTOR
// ============================================================================

// NatsConsumerListExecutor handles nats-consumer-list node type
type NatsConsumerListExecutor struct{}

func (e *NatsConsumerListExecutor) Type() string { return "nats-consumer-list" }

func (e *NatsConsumerListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	natsCfg := parseNATSConfig(config)
	streamName := getString(config, "streamName")

	if streamName == "" {
		return &executor.StepResult{Output: map[string]interface{}{"success": false, "error": "streamName is required"}}, nil
	}

	js, err := getJetStreamClient(natsCfg)
	if err != nil {
		return &executor.StepResult{Output: map[string]interface{}{"success": false, "error": err.Error()}}, nil
	}

	jsCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// Get the stream first
	stream, err := js.Stream(jsCtx, streamName)
	if err != nil {
		return &executor.StepResult{Output: map[string]interface{}{"success": false, "error": err.Error()}}, nil
	}

	var consumers []map[string]interface{}

	// List consumers - the lister returns ConsumerInfo directly
	consumerLister := stream.ListConsumers(jsCtx)
	for info := range consumerLister.Info() {
		consumerData := map[string]interface{}{
			"name":          info.Name,
			"stream":        info.Stream,
			"deliverPolicy": info.Config.DeliverPolicy.String(),
			"ackPolicy":     info.Config.AckPolicy.String(),
			"ackWait":       info.Config.AckWait.Nanoseconds(),
			"maxDeliver":    info.Config.MaxDeliver,
			"maxAckPending": info.Config.MaxAckPending,
			"filterSubject": info.Config.FilterSubject,
			"numPending":    info.NumPending,
			"numAckPending": info.NumAckPending,
			"created":       info.Created.UnixNano(),
		}

		consumers = append(consumers, consumerData)
	}

	if err := consumerLister.Err(); err != nil {
		return &executor.StepResult{Output: map[string]interface{}{"success": false, "error": err.Error()}}, nil
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":   true,
			"stream":    streamName,
			"consumers": consumers,
			"count":     len(consumers),
		},
	}, nil
}

// ============================================================================
// HELPERS
// ============================================================================

// matchSubject checks if a subject matches a pattern (simple wildcard matching)
func matchSubject(subject, pattern string) bool {
	if subject == pattern {
		return true
	}

	subjectParts := strings.Split(subject, ".")
	patternParts := strings.Split(pattern, ".")

	if len(patternParts) > len(subjectParts) {
		return false
	}

	for i, patternPart := range patternParts {
		if patternPart == ">" {
			return true
		}
		if patternPart == "*" {
			continue
		}
		if i >= len(subjectParts) || subjectParts[i] != patternPart {
			return false
		}
	}

	return len(subjectParts) == len(patternParts)
}

// serializeJSON converts a value to JSON string
func serializeJSON(v interface{}) string {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(data)
}
