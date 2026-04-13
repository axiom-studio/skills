package main

import (
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/IBM/sarama"
	"github.com/axiom-studio/skills.sdk/executor"
	"github.com/axiom-studio/skills.sdk/grpc"
	"github.com/axiom-studio/skills.sdk/resolver"
	"github.com/xdg-go/scram"
)

const (
	iconKafka = "radio"
)

// Kafka client cache
var (
	kafkaConfigs   = make(map[string]*sarama.Config)
	kafkaConfigMux sync.RWMutex
	clusterAdmins  = make(map[string]sarama.ClusterAdmin)
	adminMux       sync.RWMutex
	producers      = make(map[string]sarama.SyncProducer)
	producerMux    sync.RWMutex
	consumers      = make(map[string]sarama.Consumer)
	consumerMux    sync.RWMutex
	clients        = make(map[string]sarama.Client)
	clientCacheMux sync.RWMutex
)

// SCRAM client implementation
var (
	SHA256 scram.HashGeneratorFcn = sha256.New
	SHA512 scram.HashGeneratorFcn = sha512.New
)

type XDGSCRAMClient struct {
	*scram.Client
	*scram.ClientConversation
	scram.HashGeneratorFcn
}

func (x *XDGSCRAMClient) Begin(userName, password, authzID string) (err error) {
	x.Client, err = x.HashGeneratorFcn.NewClient(userName, password, authzID)
	if err != nil {
		return err
	}
	x.ClientConversation = x.Client.NewConversation()
	return nil
}

func (x *XDGSCRAMClient) Step(challenge string) (response string, err error) {
	response, err = x.ClientConversation.Step(challenge)
	return
}

func (x *XDGSCRAMClient) Done() bool {
	return x.ClientConversation.Done()
}

func main() {
	// Get port from env or use default
	port := os.Getenv("SKILL_PORT")
	if port == "" {
		port = "50089"
	}

	// Create skill server
	server := grpc.NewSkillServer("skill-kafka", "1.0.0")

	// Register Kafka executors with schemas
	server.RegisterExecutorWithSchema("kafka-produce", &KafkaProduceExecutor{}, KafkaProduceSchema)
	server.RegisterExecutorWithSchema("kafka-consume", &KafkaConsumeExecutor{}, KafkaConsumeSchema)
	server.RegisterExecutorWithSchema("kafka-topic-create", &KafkaTopicCreateExecutor{}, KafkaTopicCreateSchema)
	server.RegisterExecutorWithSchema("kafka-topic-list", &KafkaTopicListExecutor{}, KafkaTopicListSchema)
	server.RegisterExecutorWithSchema("kafka-topic-delete", &KafkaTopicDeleteExecutor{}, KafkaTopicDeleteSchema)
	server.RegisterExecutorWithSchema("kafka-topic-describe", &KafkaTopicDescribeExecutor{}, KafkaTopicDescribeSchema)
	server.RegisterExecutorWithSchema("kafka-consumer-group-list", &KafkaConsumerGroupListExecutor{}, KafkaConsumerGroupListSchema)
	server.RegisterExecutorWithSchema("kafka-consumer-group-reset", &KafkaConsumerGroupResetExecutor{}, KafkaConsumerGroupResetSchema)
	server.RegisterExecutorWithSchema("kafka-partition-list", &KafkaPartitionListExecutor{}, KafkaPartitionListSchema)
	server.RegisterExecutorWithSchema("kafka-offset-get", &KafkaOffsetGetExecutor{}, KafkaOffsetGetSchema)

	fmt.Printf("Starting skill-kafka gRPC server on port %s\n", port)
	if err := server.Serve(port); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to serve: %v\n", err)
		os.Exit(1)
	}
}

// ============================================================================
// KAFKA CONFIGURATION
// ============================================================================

// KafkaConfig holds Kafka connection configuration
type KafkaConfig struct {
	Brokers          []string
	SASLUsername     string
	SASLPassword     string
	SASLMechanism    string
	TLSEnabled       bool
	TLSSkipVerify    bool
	TLSCertFile      string
	TLSKeyFile       string
	TLSCAFile        string
	ClientID         string
	Version          string
	Timeout          int
	MaxOpenRequests  int
}

// getKafkaConfig returns a sarama config (cached)
func getKafkaConfig(cfg KafkaConfig) (*sarama.Config, error) {
	// Create cache key
	cacheKey := fmt.Sprintf("%s:%s:%s:%s:%v", strings.Join(cfg.Brokers, ","), cfg.SASLUsername, cfg.SASLMechanism, cfg.ClientID, cfg.TLSEnabled)

	kafkaConfigMux.RLock()
	cachedConfig, ok := kafkaConfigs[cacheKey]
	kafkaConfigMux.RUnlock()

	if ok {
		return cachedConfig, nil
	}

	kafkaConfigMux.Lock()
	defer kafkaConfigMux.Unlock()

	// Double check
	if cachedConfig, ok := kafkaConfigs[cacheKey]; ok {
		return cachedConfig, nil
	}

	// Create new config
	config := sarama.NewConfig()

	// Set version
	if cfg.Version != "" {
		version, err := sarama.ParseKafkaVersion(cfg.Version)
		if err != nil {
			return nil, fmt.Errorf("failed to parse Kafka version: %w", err)
		}
		config.Version = version
	} else {
		config.Version = sarama.V2_8_0_0 // Default to a recent version
	}

	// Set client ID
	if cfg.ClientID != "" {
		config.ClientID = cfg.ClientID
	} else {
		config.ClientID = "axiom-skill-kafka"
	}

	// Set timeout
	if cfg.Timeout > 0 {
		config.Net.DialTimeout = time.Duration(cfg.Timeout) * time.Second
		config.Net.ReadTimeout = time.Duration(cfg.Timeout) * time.Second
		config.Net.WriteTimeout = time.Duration(cfg.Timeout) * time.Second
	} else {
		config.Net.DialTimeout = 30 * time.Second
		config.Net.ReadTimeout = 30 * time.Second
		config.Net.WriteTimeout = 30 * time.Second
	}

	// Max open requests
	if cfg.MaxOpenRequests > 0 {
		config.Net.MaxOpenRequests = cfg.MaxOpenRequests
	} else {
		config.Net.MaxOpenRequests = 5
	}

	// Producer config
	config.Producer.Return.Successes = true
	config.Producer.Return.Errors = true
	config.Producer.RequiredAcks = sarama.WaitForAll
	config.Producer.Retry.Max = 3
	config.Producer.Timeout = 30 * time.Second

	// Consumer config
	config.Consumer.Return.Errors = true
	config.Consumer.Offsets.Initial = sarama.OffsetOldest

	// TLS configuration
	if cfg.TLSEnabled {
		config.Net.TLS.Enable = true

		tlsConfig, err := setupTLSConfig(cfg)
		if err != nil {
			return nil, fmt.Errorf("failed to setup TLS: %w", err)
		}
		config.Net.TLS.Config = tlsConfig
	}

	// SASL configuration
	if cfg.SASLUsername != "" && cfg.SASLPassword != "" {
		config.Net.SASL.Enable = true
		config.Net.SASL.User = cfg.SASLUsername
		config.Net.SASL.Password = cfg.SASLPassword

		// Set SASL mechanism
		switch strings.ToUpper(cfg.SASLMechanism) {
		case "PLAIN", "":
			config.Net.SASL.Mechanism = sarama.SASLTypePlaintext
		case "SCRAM-SHA-256":
			config.Net.SASL.Mechanism = sarama.SASLTypeSCRAMSHA256
			config.Net.SASL.SCRAMClientGeneratorFunc = func() sarama.SCRAMClient {
				return &XDGSCRAMClient{HashGeneratorFcn: SHA256}
			}
		case "SCRAM-SHA-512":
			config.Net.SASL.Mechanism = sarama.SASLTypeSCRAMSHA512
			config.Net.SASL.SCRAMClientGeneratorFunc = func() sarama.SCRAMClient {
				return &XDGSCRAMClient{HashGeneratorFcn: SHA512}
			}
		case "AWS_MSK_IAM":
			config.Net.SASL.Mechanism = sarama.SASLTypeOAuth
		default:
			config.Net.SASL.Mechanism = sarama.SASLTypePlaintext
		}
	}

	// Cache the config
	kafkaConfigs[cacheKey] = config
	return config, nil
}

// setupTLSConfig creates TLS configuration
func setupTLSConfig(cfg KafkaConfig) (*tls.Config, error) {
	tlsConfig := &tls.Config{
		InsecureSkipVerify: cfg.TLSSkipVerify,
	}

	// Load client certificate and key
	if cfg.TLSCertFile != "" && cfg.TLSKeyFile != "" {
		cert, err := tls.LoadX509KeyPair(cfg.TLSCertFile, cfg.TLSKeyFile)
		if err != nil {
			return nil, fmt.Errorf("failed to load client certificate: %w", err)
		}
		tlsConfig.Certificates = []tls.Certificate{cert}
	}

	// Load CA certificate
	if cfg.TLSCAFile != "" {
		caCert, err := os.ReadFile(cfg.TLSCAFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read CA certificate: %w", err)
		}

		caCertPool := x509.NewCertPool()
		if !caCertPool.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("failed to parse CA certificate")
		}
		tlsConfig.RootCAs = caCertPool
	}

	return tlsConfig, nil
}

// getClusterAdmin returns a cluster admin client (cached)
func getClusterAdmin(cfg KafkaConfig) (sarama.ClusterAdmin, error) {
	cacheKey := fmt.Sprintf("%s:%s:%s:%s:%v", strings.Join(cfg.Brokers, ","), cfg.SASLUsername, cfg.SASLMechanism, cfg.ClientID, cfg.TLSEnabled)

	adminMux.RLock()
	admin, ok := clusterAdmins[cacheKey]
	adminMux.RUnlock()

	if ok {
		return admin, nil
	}

	config, err := getKafkaConfig(cfg)
	if err != nil {
		return nil, err
	}

	adminMux.Lock()
	defer adminMux.Unlock()

	admin, err = sarama.NewClusterAdmin(cfg.Brokers, config)
	if err != nil {
		return nil, fmt.Errorf("failed to create cluster admin: %w", err)
	}

	clusterAdmins[cacheKey] = admin
	return admin, nil
}

// getSyncProducer returns a sync producer (cached)
func getSyncProducer(cfg KafkaConfig) (sarama.SyncProducer, error) {
	cacheKey := fmt.Sprintf("%s:%s:%s:%s:%v", strings.Join(cfg.Brokers, ","), cfg.SASLUsername, cfg.SASLMechanism, cfg.ClientID, cfg.TLSEnabled)

	producerMux.RLock()
	producer, ok := producers[cacheKey]
	producerMux.RUnlock()

	if ok {
		return producer, nil
	}

	config, err := getKafkaConfig(cfg)
	if err != nil {
		return nil, err
	}

	producerMux.Lock()
	defer producerMux.Unlock()

	producer, err = sarama.NewSyncProducer(cfg.Brokers, config)
	if err != nil {
		return nil, fmt.Errorf("failed to create sync producer: %w", err)
	}

	producers[cacheKey] = producer
	return producer, nil
}

// getConsumer returns a consumer (cached)
func getConsumer(cfg KafkaConfig) (sarama.Consumer, error) {
	cacheKey := fmt.Sprintf("%s:%s:%s:%s:%v:consumer", strings.Join(cfg.Brokers, ","), cfg.SASLUsername, cfg.SASLMechanism, cfg.ClientID, cfg.TLSEnabled)

	consumerMux.RLock()
	consumer, ok := consumers[cacheKey]
	consumerMux.RUnlock()

	if ok {
		return consumer, nil
	}

	config, err := getKafkaConfig(cfg)
	if err != nil {
		return nil, err
	}

	consumerMux.Lock()
	defer consumerMux.Unlock()

	consumer, err = sarama.NewConsumer(cfg.Brokers, config)
	if err != nil {
		return nil, fmt.Errorf("failed to create consumer: %w", err)
	}

	consumers[cacheKey] = consumer
	return consumer, nil
}

// getClient returns a client (cached)
func getClient(cfg KafkaConfig) (sarama.Client, error) {
	cacheKey := fmt.Sprintf("%s:%s:%s:%s:%v:client", strings.Join(cfg.Brokers, ","), cfg.SASLUsername, cfg.SASLMechanism, cfg.ClientID, cfg.TLSEnabled)

	clientCacheMux.RLock()
	client, ok := clients[cacheKey]
	clientCacheMux.RUnlock()

	if ok {
		return client, nil
	}

	config, err := getKafkaConfig(cfg)
	if err != nil {
		return nil, err
	}

	clientCacheMux.Lock()
	defer clientCacheMux.Unlock()

	client, err = sarama.NewClient(cfg.Brokers, config)
	if err != nil {
		return nil, fmt.Errorf("failed to create client: %w", err)
	}

	clients[cacheKey] = client
	return client, nil
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

// parseKafkaConfig extracts Kafka configuration from config map
func parseKafkaConfig(config map[string]interface{}) KafkaConfig {
	brokers := getStringSlice(config, "brokers")
	if len(brokers) == 0 {
		// Try single broker field
		if broker := getString(config, "broker"); broker != "" {
			brokers = strings.Split(broker, ",")
		}
	}

	return KafkaConfig{
		Brokers:       brokers,
		SASLUsername:  getString(config, "saslUsername"),
		SASLPassword:  getString(config, "saslPassword"),
		SASLMechanism: getString(config, "saslMechanism"),
		TLSEnabled:    getBool(config, "tlsEnabled", false),
		TLSSkipVerify: getBool(config, "tlsSkipVerify", false),
		TLSCertFile:   getString(config, "tlsCertFile"),
		TLSKeyFile:    getString(config, "tlsKeyFile"),
		TLSCAFile:     getString(config, "tlsCAFile"),
		ClientID:      getString(config, "clientId"),
		Version:       getString(config, "version"),
		Timeout:       getInt(config, "timeout", 30),
	}
}

// ============================================================================
// SCHEMAS
// ============================================================================

// KafkaProduceSchema is the UI schema for kafka-produce
var KafkaProduceSchema = resolver.NewSchemaBuilder("kafka-produce").
	WithName("Produce Kafka Message").
	WithCategory("action").
	WithIcon(iconKafka).
	WithDescription("Produce a message to a Kafka topic").
	AddSection("Connection").
		AddTagsField("brokers", "Brokers",
			resolver.WithRequired(),
			resolver.WithPlaceholder("localhost:9092"),
			resolver.WithHint("Kafka broker addresses (comma-separated or array)"),
		).
		AddExpressionField("saslUsername", "SASL Username",
			resolver.WithHint("SASL username for authentication (leave empty for no auth)"),
		).
		AddExpressionField("saslPassword", "SASL Password",
			resolver.WithSensitive(),
			resolver.WithHint("SASL password for authentication"),
		).
		AddSelectField("saslMechanism", "SASL Mechanism",
			[]resolver.SelectOption{
				{Label: "PLAIN", Value: "PLAIN"},
				{Label: "SCRAM-SHA-256", Value: "SCRAM-SHA-256"},
				{Label: "SCRAM-SHA-512", Value: "SCRAM-SHA-512"},
			},
			resolver.WithDefault("PLAIN"),
			resolver.WithHint("SASL authentication mechanism"),
		).
		AddToggleField("tlsEnabled", "TLS Enabled",
			resolver.WithDefault(false),
			resolver.WithHint("Enable TLS encryption"),
		).
		AddToggleField("tlsSkipVerify", "Skip TLS Verify",
			resolver.WithDefault(false),
			resolver.WithHint("Skip TLS certificate verification"),
			resolver.WithShowIf("tlsEnabled", true),
		).
		AddExpressionField("tlsCertFile", "TLS Cert File",
			resolver.WithHint("Path to client certificate file"),
			resolver.WithShowIf("tlsEnabled", true),
		).
		AddExpressionField("tlsKeyFile", "TLS Key File",
			resolver.WithHint("Path to client private key file"),
			resolver.WithShowIf("tlsEnabled", true),
		).
		AddExpressionField("tlsCAFile", "TLS CA File",
			resolver.WithHint("Path to CA certificate file"),
			resolver.WithShowIf("tlsEnabled", true),
		).
		AddExpressionField("clientId", "Client ID",
			resolver.WithPlaceholder("axiom-skill-kafka"),
			resolver.WithHint("Client ID for Kafka connections"),
		).
		AddExpressionField("version", "Kafka Version",
			resolver.WithPlaceholder("2.8.0"),
			resolver.WithHint("Kafka version (e.g., 2.8.0, 3.0.0)"),
		).
		EndSection().
	AddSection("Message").
		AddExpressionField("topic", "Topic",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-topic"),
			resolver.WithHint("Target Kafka topic"),
		).
		AddExpressionField("key", "Key",
			resolver.WithHint("Message key (optional, used for partitioning)"),
		).
		AddTextareaField("value", "Value",
			resolver.WithRequired(),
			resolver.WithRows(6),
			resolver.WithPlaceholder("Message content..."),
			resolver.WithHint("Message value/content"),
		).
		AddSelectField("valueFormat", "Value Format",
			[]resolver.SelectOption{
				{Label: "Plain Text", Value: "text"},
				{Label: "JSON", Value: "json"},
			},
			resolver.WithDefault("text"),
			resolver.WithHint("Format of the message value"),
		).
		EndSection().
	AddSection("Options").
		AddNumberField("partition", "Partition",
			resolver.WithDefault(-1),
			resolver.WithHint("Target partition (-1 for automatic partitioning based on key)"),
		).
		AddNumberField("maxRetries", "Max Retries",
			resolver.WithDefault(3),
			resolver.WithHint("Maximum number of retry attempts"),
		).
		EndSection().
	Build()

// KafkaConsumeSchema is the UI schema for kafka-consume
var KafkaConsumeSchema = resolver.NewSchemaBuilder("kafka-consume").
	WithName("Consume Kafka Messages").
	WithCategory("action").
	WithIcon(iconKafka).
	WithDescription("Consume messages from a Kafka topic").
	AddSection("Connection").
		AddTagsField("brokers", "Brokers",
			resolver.WithRequired(),
			resolver.WithPlaceholder("localhost:9092"),
			resolver.WithHint("Kafka broker addresses"),
		).
		AddExpressionField("saslUsername", "SASL Username",
			resolver.WithHint("SASL username for authentication"),
		).
		AddExpressionField("saslPassword", "SASL Password",
			resolver.WithSensitive(),
			resolver.WithHint("SASL password for authentication"),
		).
		AddSelectField("saslMechanism", "SASL Mechanism",
			[]resolver.SelectOption{
				{Label: "PLAIN", Value: "PLAIN"},
				{Label: "SCRAM-SHA-256", Value: "SCRAM-SHA-256"},
				{Label: "SCRAM-SHA-512", Value: "SCRAM-SHA-512"},
			},
			resolver.WithDefault("PLAIN"),
			resolver.WithHint("SASL authentication mechanism"),
		).
		AddToggleField("tlsEnabled", "TLS Enabled",
			resolver.WithDefault(false),
			resolver.WithHint("Enable TLS encryption"),
		).
		AddToggleField("tlsSkipVerify", "Skip TLS Verify",
			resolver.WithDefault(false),
			resolver.WithHint("Skip TLS certificate verification"),
			resolver.WithShowIf("tlsEnabled", true),
		).
		AddExpressionField("tlsCertFile", "TLS Cert File",
			resolver.WithHint("Path to client certificate file"),
			resolver.WithShowIf("tlsEnabled", true),
		).
		AddExpressionField("tlsKeyFile", "TLS Key File",
			resolver.WithHint("Path to client private key file"),
			resolver.WithShowIf("tlsEnabled", true),
		).
		AddExpressionField("tlsCAFile", "TLS CA File",
			resolver.WithHint("Path to CA certificate file"),
			resolver.WithShowIf("tlsEnabled", true),
		).
		AddExpressionField("clientId", "Client ID",
			resolver.WithPlaceholder("axiom-skill-kafka"),
			resolver.WithHint("Client ID for Kafka connections"),
		).
		AddExpressionField("version", "Kafka Version",
			resolver.WithPlaceholder("2.8.0"),
			resolver.WithHint("Kafka version"),
		).
		EndSection().
	AddSection("Consumer").
		AddExpressionField("topic", "Topic",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-topic"),
			resolver.WithHint("Kafka topic to consume from"),
		).
		AddExpressionField("consumerGroup", "Consumer Group",
			resolver.WithHint("Consumer group ID (leave empty for standalone consumption)"),
		).
		AddSelectField("offset", "Offset",
			[]resolver.SelectOption{
				{Label: "Oldest", Value: "oldest"},
				{Label: "Newest", Value: "newest"},
			},
			resolver.WithDefault("newest"),
			resolver.WithHint("Starting offset (oldest or newest)"),
		).
		EndSection().
	AddSection("Options").
		AddNumberField("maxMessages", "Max Messages",
			resolver.WithDefault(100),
			resolver.WithHint("Maximum number of messages to consume"),
		).
		AddNumberField("timeoutMs", "Timeout (ms)",
			resolver.WithDefault(5000),
			resolver.WithHint("Timeout for consuming messages"),
		).
		AddNumberField("partition", "Partition",
			resolver.WithDefault(-1),
			resolver.WithHint("Specific partition to consume from (-1 for all partitions)"),
		).
		EndSection().
	Build()

// KafkaTopicCreateSchema is the UI schema for kafka-topic-create
var KafkaTopicCreateSchema = resolver.NewSchemaBuilder("kafka-topic-create").
	WithName("Create Kafka Topic").
	WithCategory("action").
	WithIcon(iconKafka).
	WithDescription("Create a new Kafka topic").
	AddSection("Connection").
		AddTagsField("brokers", "Brokers",
			resolver.WithRequired(),
			resolver.WithPlaceholder("localhost:9092"),
			resolver.WithHint("Kafka broker addresses"),
		).
		AddExpressionField("saslUsername", "SASL Username",
			resolver.WithHint("SASL username for authentication"),
		).
		AddExpressionField("saslPassword", "SASL Password",
			resolver.WithSensitive(),
			resolver.WithHint("SASL password for authentication"),
		).
		AddSelectField("saslMechanism", "SASL Mechanism",
			[]resolver.SelectOption{
				{Label: "PLAIN", Value: "PLAIN"},
				{Label: "SCRAM-SHA-256", Value: "SCRAM-SHA-256"},
				{Label: "SCRAM-SHA-512", Value: "SCRAM-SHA-512"},
			},
			resolver.WithDefault("PLAIN"),
			resolver.WithHint("SASL authentication mechanism"),
		).
		AddToggleField("tlsEnabled", "TLS Enabled",
			resolver.WithDefault(false),
			resolver.WithHint("Enable TLS encryption"),
		).
		AddToggleField("tlsSkipVerify", "Skip TLS Verify",
			resolver.WithDefault(false),
			resolver.WithHint("Skip TLS certificate verification"),
			resolver.WithShowIf("tlsEnabled", true),
		).
		AddExpressionField("tlsCertFile", "TLS Cert File",
			resolver.WithHint("Path to client certificate file"),
			resolver.WithShowIf("tlsEnabled", true),
		).
		AddExpressionField("tlsKeyFile", "TLS Key File",
			resolver.WithHint("Path to client private key file"),
			resolver.WithShowIf("tlsEnabled", true),
		).
		AddExpressionField("tlsCAFile", "TLS CA File",
			resolver.WithHint("Path to CA certificate file"),
			resolver.WithShowIf("tlsEnabled", true),
		).
		AddExpressionField("clientId", "Client ID",
			resolver.WithPlaceholder("axiom-skill-kafka"),
			resolver.WithHint("Client ID for Kafka connections"),
		).
		AddExpressionField("version", "Kafka Version",
			resolver.WithPlaceholder("2.8.0"),
			resolver.WithHint("Kafka version"),
		).
		EndSection().
	AddSection("Topic").
		AddExpressionField("topic", "Topic Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-new-topic"),
			resolver.WithHint("Name of the topic to create"),
		).
		AddNumberField("partitions", "Partitions",
			resolver.WithDefault(3),
			resolver.WithHint("Number of partitions"),
		).
		AddNumberField("replicationFactor", "Replication Factor",
			resolver.WithDefault(1),
			resolver.WithHint("Replication factor (number of replicas)"),
		).
		EndSection().
	AddSection("Advanced").
		AddKeyValueField("config", "Topic Config",
			resolver.WithHint("Additional topic configuration (key-value pairs)"),
		).
		EndSection().
	Build()

// KafkaTopicListSchema is the UI schema for kafka-topic-list
var KafkaTopicListSchema = resolver.NewSchemaBuilder("kafka-topic-list").
	WithName("List Kafka Topics").
	WithCategory("action").
	WithIcon(iconKafka).
	WithDescription("List all Kafka topics").
	AddSection("Connection").
		AddTagsField("brokers", "Brokers",
			resolver.WithRequired(),
			resolver.WithPlaceholder("localhost:9092"),
			resolver.WithHint("Kafka broker addresses"),
		).
		AddExpressionField("saslUsername", "SASL Username",
			resolver.WithHint("SASL username for authentication"),
		).
		AddExpressionField("saslPassword", "SASL Password",
			resolver.WithSensitive(),
			resolver.WithHint("SASL password for authentication"),
		).
		AddSelectField("saslMechanism", "SASL Mechanism",
			[]resolver.SelectOption{
				{Label: "PLAIN", Value: "PLAIN"},
				{Label: "SCRAM-SHA-256", Value: "SCRAM-SHA-256"},
				{Label: "SCRAM-SHA-512", Value: "SCRAM-SHA-512"},
			},
			resolver.WithDefault("PLAIN"),
			resolver.WithHint("SASL authentication mechanism"),
		).
		AddToggleField("tlsEnabled", "TLS Enabled",
			resolver.WithDefault(false),
			resolver.WithHint("Enable TLS encryption"),
		).
		AddToggleField("tlsSkipVerify", "Skip TLS Verify",
			resolver.WithDefault(false),
			resolver.WithHint("Skip TLS certificate verification"),
			resolver.WithShowIf("tlsEnabled", true),
		).
		AddExpressionField("tlsCertFile", "TLS Cert File",
			resolver.WithHint("Path to client certificate file"),
			resolver.WithShowIf("tlsEnabled", true),
		).
		AddExpressionField("tlsKeyFile", "TLS Key File",
			resolver.WithHint("Path to client private key file"),
			resolver.WithShowIf("tlsEnabled", true),
		).
		AddExpressionField("tlsCAFile", "TLS CA File",
			resolver.WithHint("Path to CA certificate file"),
			resolver.WithShowIf("tlsEnabled", true),
		).
		AddExpressionField("clientId", "Client ID",
			resolver.WithPlaceholder("axiom-skill-kafka"),
			resolver.WithHint("Client ID for Kafka connections"),
		).
		AddExpressionField("version", "Kafka Version",
			resolver.WithPlaceholder("2.8.0"),
			resolver.WithHint("Kafka version"),
		).
		EndSection().
	AddSection("Filters").
		AddExpressionField("pattern", "Pattern",
			resolver.WithHint("Filter topics by name pattern (regex)"),
		).
		EndSection().
	Build()

// KafkaTopicDeleteSchema is the UI schema for kafka-topic-delete
var KafkaTopicDeleteSchema = resolver.NewSchemaBuilder("kafka-topic-delete").
	WithName("Delete Kafka Topic").
	WithCategory("action").
	WithIcon(iconKafka).
	WithDescription("Delete a Kafka topic").
	AddSection("Connection").
		AddTagsField("brokers", "Brokers",
			resolver.WithRequired(),
			resolver.WithPlaceholder("localhost:9092"),
			resolver.WithHint("Kafka broker addresses"),
		).
		AddExpressionField("saslUsername", "SASL Username",
			resolver.WithHint("SASL username for authentication"),
		).
		AddExpressionField("saslPassword", "SASL Password",
			resolver.WithSensitive(),
			resolver.WithHint("SASL password for authentication"),
		).
		AddSelectField("saslMechanism", "SASL Mechanism",
			[]resolver.SelectOption{
				{Label: "PLAIN", Value: "PLAIN"},
				{Label: "SCRAM-SHA-256", Value: "SCRAM-SHA-256"},
				{Label: "SCRAM-SHA-512", Value: "SCRAM-SHA-512"},
			},
			resolver.WithDefault("PLAIN"),
			resolver.WithHint("SASL authentication mechanism"),
		).
		AddToggleField("tlsEnabled", "TLS Enabled",
			resolver.WithDefault(false),
			resolver.WithHint("Enable TLS encryption"),
		).
		AddToggleField("tlsSkipVerify", "Skip TLS Verify",
			resolver.WithDefault(false),
			resolver.WithHint("Skip TLS certificate verification"),
			resolver.WithShowIf("tlsEnabled", true),
		).
		AddExpressionField("tlsCertFile", "TLS Cert File",
			resolver.WithHint("Path to client certificate file"),
			resolver.WithShowIf("tlsEnabled", true),
		).
		AddExpressionField("tlsKeyFile", "TLS Key File",
			resolver.WithHint("Path to client private key file"),
			resolver.WithShowIf("tlsEnabled", true),
		).
		AddExpressionField("tlsCAFile", "TLS CA File",
			resolver.WithHint("Path to CA certificate file"),
			resolver.WithShowIf("tlsEnabled", true),
		).
		AddExpressionField("clientId", "Client ID",
			resolver.WithPlaceholder("axiom-skill-kafka"),
			resolver.WithHint("Client ID for Kafka connections"),
		).
		AddExpressionField("version", "Kafka Version",
			resolver.WithPlaceholder("2.8.0"),
			resolver.WithHint("Kafka version"),
		).
		EndSection().
	AddSection("Topic").
		AddExpressionField("topic", "Topic Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-topic"),
			resolver.WithHint("Name of the topic to delete"),
		).
		EndSection().
	Build()

// KafkaTopicDescribeSchema is the UI schema for kafka-topic-describe
var KafkaTopicDescribeSchema = resolver.NewSchemaBuilder("kafka-topic-describe").
	WithName("Describe Kafka Topic").
	WithCategory("action").
	WithIcon(iconKafka).
	WithDescription("Get detailed information about a Kafka topic").
	AddSection("Connection").
		AddTagsField("brokers", "Brokers",
			resolver.WithRequired(),
			resolver.WithPlaceholder("localhost:9092"),
			resolver.WithHint("Kafka broker addresses"),
		).
		AddExpressionField("saslUsername", "SASL Username",
			resolver.WithHint("SASL username for authentication"),
		).
		AddExpressionField("saslPassword", "SASL Password",
			resolver.WithSensitive(),
			resolver.WithHint("SASL password for authentication"),
		).
		AddSelectField("saslMechanism", "SASL Mechanism",
			[]resolver.SelectOption{
				{Label: "PLAIN", Value: "PLAIN"},
				{Label: "SCRAM-SHA-256", Value: "SCRAM-SHA-256"},
				{Label: "SCRAM-SHA-512", Value: "SCRAM-SHA-512"},
			},
			resolver.WithDefault("PLAIN"),
			resolver.WithHint("SASL authentication mechanism"),
		).
		AddToggleField("tlsEnabled", "TLS Enabled",
			resolver.WithDefault(false),
			resolver.WithHint("Enable TLS encryption"),
		).
		AddToggleField("tlsSkipVerify", "Skip TLS Verify",
			resolver.WithDefault(false),
			resolver.WithHint("Skip TLS certificate verification"),
			resolver.WithShowIf("tlsEnabled", true),
		).
		AddExpressionField("tlsCertFile", "TLS Cert File",
			resolver.WithHint("Path to client certificate file"),
			resolver.WithShowIf("tlsEnabled", true),
		).
		AddExpressionField("tlsKeyFile", "TLS Key File",
			resolver.WithHint("Path to client private key file"),
			resolver.WithShowIf("tlsEnabled", true),
		).
		AddExpressionField("tlsCAFile", "TLS CA File",
			resolver.WithHint("Path to CA certificate file"),
			resolver.WithShowIf("tlsEnabled", true),
		).
		AddExpressionField("clientId", "Client ID",
			resolver.WithPlaceholder("axiom-skill-kafka"),
			resolver.WithHint("Client ID for Kafka connections"),
		).
		AddExpressionField("version", "Kafka Version",
			resolver.WithPlaceholder("2.8.0"),
			resolver.WithHint("Kafka version"),
		).
		EndSection().
	AddSection("Topic").
		AddExpressionField("topic", "Topic Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-topic"),
			resolver.WithHint("Name of the topic to describe"),
		).
		EndSection().
	Build()

// KafkaConsumerGroupListSchema is the UI schema for kafka-consumer-group-list
var KafkaConsumerGroupListSchema = resolver.NewSchemaBuilder("kafka-consumer-group-list").
	WithName("List Consumer Groups").
	WithCategory("action").
	WithIcon(iconKafka).
	WithDescription("List all Kafka consumer groups").
	AddSection("Connection").
		AddTagsField("brokers", "Brokers",
			resolver.WithRequired(),
			resolver.WithPlaceholder("localhost:9092"),
			resolver.WithHint("Kafka broker addresses"),
		).
		AddExpressionField("saslUsername", "SASL Username",
			resolver.WithHint("SASL username for authentication"),
		).
		AddExpressionField("saslPassword", "SASL Password",
			resolver.WithSensitive(),
			resolver.WithHint("SASL password for authentication"),
		).
		AddSelectField("saslMechanism", "SASL Mechanism",
			[]resolver.SelectOption{
				{Label: "PLAIN", Value: "PLAIN"},
				{Label: "SCRAM-SHA-256", Value: "SCRAM-SHA-256"},
				{Label: "SCRAM-SHA-512", Value: "SCRAM-SHA-512"},
			},
			resolver.WithDefault("PLAIN"),
			resolver.WithHint("SASL authentication mechanism"),
		).
		AddToggleField("tlsEnabled", "TLS Enabled",
			resolver.WithDefault(false),
			resolver.WithHint("Enable TLS encryption"),
		).
		AddToggleField("tlsSkipVerify", "Skip TLS Verify",
			resolver.WithDefault(false),
			resolver.WithHint("Skip TLS certificate verification"),
			resolver.WithShowIf("tlsEnabled", true),
		).
		AddExpressionField("tlsCertFile", "TLS Cert File",
			resolver.WithHint("Path to client certificate file"),
			resolver.WithShowIf("tlsEnabled", true),
		).
		AddExpressionField("tlsKeyFile", "TLS Key File",
			resolver.WithHint("Path to client private key file"),
			resolver.WithShowIf("tlsEnabled", true),
		).
		AddExpressionField("tlsCAFile", "TLS CA File",
			resolver.WithHint("Path to CA certificate file"),
			resolver.WithShowIf("tlsEnabled", true),
		).
		AddExpressionField("clientId", "Client ID",
			resolver.WithPlaceholder("axiom-skill-kafka"),
			resolver.WithHint("Client ID for Kafka connections"),
		).
		AddExpressionField("version", "Kafka Version",
			resolver.WithPlaceholder("2.8.0"),
			resolver.WithHint("Kafka version"),
		).
		EndSection().
	Build()

// KafkaConsumerGroupResetSchema is the UI schema for kafka-consumer-group-reset
var KafkaConsumerGroupResetSchema = resolver.NewSchemaBuilder("kafka-consumer-group-reset").
	WithName("Reset Consumer Group Offsets").
	WithCategory("action").
	WithIcon(iconKafka).
	WithDescription("Reset offsets for a Kafka consumer group").
	AddSection("Connection").
		AddTagsField("brokers", "Brokers",
			resolver.WithRequired(),
			resolver.WithPlaceholder("localhost:9092"),
			resolver.WithHint("Kafka broker addresses"),
		).
		AddExpressionField("saslUsername", "SASL Username",
			resolver.WithHint("SASL username for authentication"),
		).
		AddExpressionField("saslPassword", "SASL Password",
			resolver.WithSensitive(),
			resolver.WithHint("SASL password for authentication"),
		).
		AddSelectField("saslMechanism", "SASL Mechanism",
			[]resolver.SelectOption{
				{Label: "PLAIN", Value: "PLAIN"},
				{Label: "SCRAM-SHA-256", Value: "SCRAM-SHA-256"},
				{Label: "SCRAM-SHA-512", Value: "SCRAM-SHA-512"},
			},
			resolver.WithDefault("PLAIN"),
			resolver.WithHint("SASL authentication mechanism"),
		).
		AddToggleField("tlsEnabled", "TLS Enabled",
			resolver.WithDefault(false),
			resolver.WithHint("Enable TLS encryption"),
		).
		AddToggleField("tlsSkipVerify", "Skip TLS Verify",
			resolver.WithDefault(false),
			resolver.WithHint("Skip TLS certificate verification"),
			resolver.WithShowIf("tlsEnabled", true),
		).
		AddExpressionField("tlsCertFile", "TLS Cert File",
			resolver.WithHint("Path to client certificate file"),
			resolver.WithShowIf("tlsEnabled", true),
		).
		AddExpressionField("tlsKeyFile", "TLS Key File",
			resolver.WithHint("Path to client private key file"),
			resolver.WithShowIf("tlsEnabled", true),
		).
		AddExpressionField("tlsCAFile", "TLS CA File",
			resolver.WithHint("Path to CA certificate file"),
			resolver.WithShowIf("tlsEnabled", true),
		).
		AddExpressionField("clientId", "Client ID",
			resolver.WithPlaceholder("axiom-skill-kafka"),
			resolver.WithHint("Client ID for Kafka connections"),
		).
		AddExpressionField("version", "Kafka Version",
			resolver.WithPlaceholder("2.8.0"),
			resolver.WithHint("Kafka version"),
		).
		EndSection().
	AddSection("Consumer Group").
		AddExpressionField("consumerGroup", "Consumer Group",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-consumer-group"),
			resolver.WithHint("Consumer group ID"),
		).
		AddExpressionField("topic", "Topic",
			resolver.WithHint("Specific topic to reset (leave empty for all topics)"),
		).
		EndSection().
	AddSection("Reset Options").
		AddSelectField("resetTo", "Reset To",
			[]resolver.SelectOption{
				{Label: "Earliest", Value: "earliest"},
				{Label: "Latest", Value: "latest"},
				{Label: "Specific Offset", Value: "offset"},
				{Label: "Shift By", Value: "shift"},
			},
			resolver.WithDefault("latest"),
			resolver.WithHint("Where to reset offsets to"),
		).
		AddNumberField("offset", "Offset Value",
			resolver.WithHint("Offset value (for 'offset' reset) or shift amount (for 'shift' reset)"),
		).
		EndSection().
	Build()

// KafkaPartitionListSchema is the UI schema for kafka-partition-list
var KafkaPartitionListSchema = resolver.NewSchemaBuilder("kafka-partition-list").
	WithName("List Partitions").
	WithCategory("action").
	WithIcon(iconKafka).
	WithDescription("List partitions for a Kafka topic").
	AddSection("Connection").
		AddTagsField("brokers", "Brokers",
			resolver.WithRequired(),
			resolver.WithPlaceholder("localhost:9092"),
			resolver.WithHint("Kafka broker addresses"),
		).
		AddExpressionField("saslUsername", "SASL Username",
			resolver.WithHint("SASL username for authentication"),
		).
		AddExpressionField("saslPassword", "SASL Password",
			resolver.WithSensitive(),
			resolver.WithHint("SASL password for authentication"),
		).
		AddSelectField("saslMechanism", "SASL Mechanism",
			[]resolver.SelectOption{
				{Label: "PLAIN", Value: "PLAIN"},
				{Label: "SCRAM-SHA-256", Value: "SCRAM-SHA-256"},
				{Label: "SCRAM-SHA-512", Value: "SCRAM-SHA-512"},
			},
			resolver.WithDefault("PLAIN"),
			resolver.WithHint("SASL authentication mechanism"),
		).
		AddToggleField("tlsEnabled", "TLS Enabled",
			resolver.WithDefault(false),
			resolver.WithHint("Enable TLS encryption"),
		).
		AddToggleField("tlsSkipVerify", "Skip TLS Verify",
			resolver.WithDefault(false),
			resolver.WithHint("Skip TLS certificate verification"),
			resolver.WithShowIf("tlsEnabled", true),
		).
		AddExpressionField("tlsCertFile", "TLS Cert File",
			resolver.WithHint("Path to client certificate file"),
			resolver.WithShowIf("tlsEnabled", true),
		).
		AddExpressionField("tlsKeyFile", "TLS Key File",
			resolver.WithHint("Path to client private key file"),
			resolver.WithShowIf("tlsEnabled", true),
		).
		AddExpressionField("tlsCAFile", "TLS CA File",
			resolver.WithHint("Path to CA certificate file"),
			resolver.WithShowIf("tlsEnabled", true),
		).
		AddExpressionField("clientId", "Client ID",
			resolver.WithPlaceholder("axiom-skill-kafka"),
			resolver.WithHint("Client ID for Kafka connections"),
		).
		AddExpressionField("version", "Kafka Version",
			resolver.WithPlaceholder("2.8.0"),
			resolver.WithHint("Kafka version"),
		).
		EndSection().
	AddSection("Topic").
		AddExpressionField("topic", "Topic Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-topic"),
			resolver.WithHint("Kafka topic name"),
		).
		EndSection().
	Build()

// KafkaOffsetGetSchema is the UI schema for kafka-offset-get
var KafkaOffsetGetSchema = resolver.NewSchemaBuilder("kafka-offset-get").
	WithName("Get Topic Offsets").
	WithCategory("action").
	WithIcon(iconKafka).
	WithDescription("Get current offsets for a Kafka topic").
	AddSection("Connection").
		AddTagsField("brokers", "Brokers",
			resolver.WithRequired(),
			resolver.WithPlaceholder("localhost:9092"),
			resolver.WithHint("Kafka broker addresses"),
		).
		AddExpressionField("saslUsername", "SASL Username",
			resolver.WithHint("SASL username for authentication"),
		).
		AddExpressionField("saslPassword", "SASL Password",
			resolver.WithSensitive(),
			resolver.WithHint("SASL password for authentication"),
		).
		AddSelectField("saslMechanism", "SASL Mechanism",
			[]resolver.SelectOption{
				{Label: "PLAIN", Value: "PLAIN"},
				{Label: "SCRAM-SHA-256", Value: "SCRAM-SHA-256"},
				{Label: "SCRAM-SHA-512", Value: "SCRAM-SHA-512"},
			},
			resolver.WithDefault("PLAIN"),
			resolver.WithHint("SASL authentication mechanism"),
		).
		AddToggleField("tlsEnabled", "TLS Enabled",
			resolver.WithDefault(false),
			resolver.WithHint("Enable TLS encryption"),
		).
		AddToggleField("tlsSkipVerify", "Skip TLS Verify",
			resolver.WithDefault(false),
			resolver.WithHint("Skip TLS certificate verification"),
			resolver.WithShowIf("tlsEnabled", true),
		).
		AddExpressionField("tlsCertFile", "TLS Cert File",
			resolver.WithHint("Path to client certificate file"),
			resolver.WithShowIf("tlsEnabled", true),
		).
		AddExpressionField("tlsKeyFile", "TLS Key File",
			resolver.WithHint("Path to client private key file"),
			resolver.WithShowIf("tlsEnabled", true),
		).
		AddExpressionField("tlsCAFile", "TLS CA File",
			resolver.WithHint("Path to CA certificate file"),
			resolver.WithShowIf("tlsEnabled", true),
		).
		AddExpressionField("clientId", "Client ID",
			resolver.WithPlaceholder("axiom-skill-kafka"),
			resolver.WithHint("Client ID for Kafka connections"),
		).
		AddExpressionField("version", "Kafka Version",
			resolver.WithPlaceholder("2.8.0"),
			resolver.WithHint("Kafka version"),
		).
		EndSection().
	AddSection("Topic").
		AddExpressionField("topic", "Topic Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("my-topic"),
			resolver.WithHint("Kafka topic name"),
		).
		AddSelectField("offsetType", "Offset Type",
			[]resolver.SelectOption{
				{Label: "Newest (Latest)", Value: "newest"},
				{Label: "Oldest", Value: "oldest"},
				{Label: "Both", Value: "both"},
			},
			resolver.WithDefault("both"),
			resolver.WithHint("Type of offset to retrieve"),
		).
		EndSection().
	Build()

// ============================================================================
// KAFKA PRODUCE EXECUTOR
// ============================================================================

// KafkaProduceExecutor handles kafka-produce node type
type KafkaProduceExecutor struct{}

func (e *KafkaProduceExecutor) Type() string { return "kafka-produce" }

func (e *KafkaProduceExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	kafkaCfg := parseKafkaConfig(config)
	if len(kafkaCfg.Brokers) == 0 {
		return nil, fmt.Errorf("brokers configuration is required")
	}

	topic := getString(config, "topic")
	if topic == "" {
		return nil, fmt.Errorf("topic is required")
	}

	value := getString(config, "value")
	if value == "" {
		return nil, fmt.Errorf("value is required")
	}

	key := getString(config, "key")
	partition := getInt(config, "partition", -1)
	maxRetries := getInt(config, "maxRetries", 3)

	// Get producer
	producer, err := getSyncProducer(kafkaCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create producer: %w", err)
	}

	// Create message
	msg := &sarama.ProducerMessage{
		Topic:     topic,
		Partition: int32(partition),
		Value:     sarama.StringEncoder(value),
	}

	if key != "" {
		msg.Key = sarama.StringEncoder(key)
	}

	// Send message with retries
	var producedOffset int64
	var producedPartition int32
	var producedKey string

	for i := 0; i <= maxRetries; i++ {
		partition, offset, err := producer.SendMessage(msg)
		if err != nil {
			if i == maxRetries {
				return nil, fmt.Errorf("failed to produce message after %d retries: %w", maxRetries, err)
			}
			time.Sleep(time.Duration(i+1) * time.Second)
			continue
		}
		producedPartition = partition
		producedOffset = offset
		if msg.Key != nil {
			producedKey = string(msg.Key.(sarama.StringEncoder))
		}
		break
	}

	output := map[string]interface{}{
		"success":   true,
		"topic":     topic,
		"partition": producedPartition,
		"offset":    producedOffset,
		"key":       producedKey,
		"timestamp": time.Now().UnixMilli(),
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// ============================================================================
// KAFKA CONSUME EXECUTOR
// ============================================================================

// KafkaConsumeExecutor handles kafka-consume node type
type KafkaConsumeExecutor struct{}

func (e *KafkaConsumeExecutor) Type() string { return "kafka-consume" }

func (e *KafkaConsumeExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	kafkaCfg := parseKafkaConfig(config)
	if len(kafkaCfg.Brokers) == 0 {
		return nil, fmt.Errorf("brokers configuration is required")
	}

	topic := getString(config, "topic")
	if topic == "" {
		return nil, fmt.Errorf("topic is required")
	}

	offset := getString(config, "offset")
	maxMessages := getInt(config, "maxMessages", 100)
	timeoutMs := getInt(config, "timeoutMs", 5000)
	partition := getInt(config, "partition", -1)

	// Get consumer
	consumer, err := getConsumer(kafkaCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create consumer: %w", err)
	}

	// Get partitions
	partitions, err := consumer.Partitions(topic)
	if err != nil {
		return nil, fmt.Errorf("failed to get partitions: %w", err)
	}

	if len(partitions) == 0 {
		return nil, fmt.Errorf("no partitions found for topic %s", topic)
	}

	// Determine which partitions to consume from
	targetPartitions := partitions
	if partition >= 0 {
		targetPartitions = []int32{int32(partition)}
	}

	// Collect messages
	var messages []map[string]interface{}
	timeout := time.After(time.Duration(timeoutMs) * time.Millisecond)

	for _, p := range targetPartitions {
		// Get initial offset
		var initialOffset int64
		if offset == "oldest" {
			initialOffset = sarama.OffsetOldest
		} else {
			initialOffset = sarama.OffsetNewest
		}

		// Create partition consumer
		pc, err := consumer.ConsumePartition(topic, p, initialOffset)
		if err != nil {
			return nil, fmt.Errorf("failed to consume partition %d: %w", p, err)
		}
		defer pc.AsyncClose()

		// Consume messages
		for len(messages) < maxMessages {
			select {
			case msg := <-pc.Messages():
				messageData := map[string]interface{}{
					"topic":     msg.Topic,
					"partition": msg.Partition,
					"offset":    msg.Offset,
					"key":       string(msg.Key),
					"value":     string(msg.Value),
					"timestamp": msg.Timestamp.UnixMilli(),
				}
				messages = append(messages, messageData)

			case <-pc.Errors():
				// Continue on errors

			case <-timeout:
				goto done
			}
		}
	}

done:
	output := map[string]interface{}{
		"success":  true,
		"topic":    topic,
		"count":    len(messages),
		"messages": messages,
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// ============================================================================
// KAFKA TOPIC CREATE EXECUTOR
// ============================================================================

// KafkaTopicCreateExecutor handles kafka-topic-create node type
type KafkaTopicCreateExecutor struct{}

func (e *KafkaTopicCreateExecutor) Type() string { return "kafka-topic-create" }

func (e *KafkaTopicCreateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	kafkaCfg := parseKafkaConfig(config)
	if len(kafkaCfg.Brokers) == 0 {
		return nil, fmt.Errorf("brokers configuration is required")
	}

	topic := getString(config, "topic")
	if topic == "" {
		return nil, fmt.Errorf("topic is required")
	}

	partitions := getInt(config, "partitions", 3)
	replicationFactor := getInt(config, "replicationFactor", 1)

	// Get cluster admin
	admin, err := getClusterAdmin(kafkaCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create cluster admin: %w", err)
	}

	// Build topic detail
	topicDetail := &sarama.TopicDetail{
		NumPartitions:     int32(partitions),
		ReplicationFactor: int16(replicationFactor),
	}

	// Add custom config if provided
	topicConfig := getMap(config, "config")
	if topicConfig != nil {
		topicDetail.ConfigEntries = make(map[string]*string)
		for k, v := range topicConfig {
			if s, ok := v.(string); ok {
				topicDetail.ConfigEntries[k] = &s
			}
		}
	}

	// Create topic
	err = admin.CreateTopic(topic, topicDetail, false)
	if err != nil {
		if err == sarama.ErrTopicAlreadyExists {
			return &executor.StepResult{
				Output: map[string]interface{}{
					"success": false,
					"error":   "topic already exists",
					"topic":   topic,
				},
			}, nil
		}
		return nil, fmt.Errorf("failed to create topic: %w", err)
	}

	output := map[string]interface{}{
		"success":           true,
		"topic":             topic,
		"partitions":        partitions,
		"replicationFactor": replicationFactor,
		"message":           fmt.Sprintf("Topic '%s' created successfully", topic),
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// ============================================================================
// KAFKA TOPIC LIST EXECUTOR
// ============================================================================

// KafkaTopicListExecutor handles kafka-topic-list node type
type KafkaTopicListExecutor struct{}

func (e *KafkaTopicListExecutor) Type() string { return "kafka-topic-list" }

func (e *KafkaTopicListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	kafkaCfg := parseKafkaConfig(config)
	if len(kafkaCfg.Brokers) == 0 {
		return nil, fmt.Errorf("brokers configuration is required")
	}

	pattern := getString(config, "pattern")

	// Get cluster admin
	admin, err := getClusterAdmin(kafkaCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create cluster admin: %w", err)
	}

	// List topics
	topics, err := admin.ListTopics()
	if err != nil {
		return nil, fmt.Errorf("failed to list topics: %w", err)
	}

	// Filter by pattern if provided
	var topicNames []string
	for name := range topics {
		if pattern != "" {
			matched, _ := regexp.MatchString(pattern, name)
			if !matched {
				continue
			}
		}
		topicNames = append(topicNames, name)
	}

	// Sort for consistent output
	sort.Strings(topicNames)

	output := map[string]interface{}{
		"success": true,
		"count":   len(topicNames),
		"topics":  topicNames,
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// ============================================================================
// KAFKA TOPIC DELETE EXECUTOR
// ============================================================================

// KafkaTopicDeleteExecutor handles kafka-topic-delete node type
type KafkaTopicDeleteExecutor struct{}

func (e *KafkaTopicDeleteExecutor) Type() string { return "kafka-topic-delete" }

func (e *KafkaTopicDeleteExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	kafkaCfg := parseKafkaConfig(config)
	if len(kafkaCfg.Brokers) == 0 {
		return nil, fmt.Errorf("brokers configuration is required")
	}

	topic := getString(config, "topic")
	if topic == "" {
		return nil, fmt.Errorf("topic is required")
	}

	// Get cluster admin
	admin, err := getClusterAdmin(kafkaCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create cluster admin: %w", err)
	}

	// Delete topic
	err = admin.DeleteTopic(topic)
	if err != nil {
		return nil, fmt.Errorf("failed to delete topic: %w", err)
	}

	output := map[string]interface{}{
		"success": true,
		"topic":   topic,
		"message": fmt.Sprintf("Topic '%s' deleted successfully", topic),
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// ============================================================================
// KAFKA TOPIC DESCRIBE EXECUTOR
// ============================================================================

// KafkaTopicDescribeExecutor handles kafka-topic-describe node type
type KafkaTopicDescribeExecutor struct{}

func (e *KafkaTopicDescribeExecutor) Type() string { return "kafka-topic-describe" }

func (e *KafkaTopicDescribeExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	kafkaCfg := parseKafkaConfig(config)
	if len(kafkaCfg.Brokers) == 0 {
		return nil, fmt.Errorf("brokers configuration is required")
	}

	topic := getString(config, "topic")
	if topic == "" {
		return nil, fmt.Errorf("topic is required")
	}

	// Get cluster admin
	admin, err := getClusterAdmin(kafkaCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create cluster admin: %w", err)
	}

	// Describe topic to get partition metadata
	metadata, err := admin.DescribeTopics([]string{topic})
	if err != nil {
		return nil, fmt.Errorf("failed to describe topic: %w", err)
	}

	if len(metadata) == 0 || metadata[0].Name != topic {
		return nil, fmt.Errorf("topic not found: %s", topic)
	}

	topicMeta := metadata[0]

	// Build partition info
	var partitionInfo []map[string]interface{}
	for _, p := range topicMeta.Partitions {
		partitionData := map[string]interface{}{
			"partition": p.ID,
			"leader":    p.Leader,
			"replicas":  p.Replicas,
			"ISRs":      p.Isr,
		}
		partitionInfo = append(partitionInfo, partitionData)
	}

	// Get topic config entries
	allTopics, err := admin.ListTopics()
	configEntries := make(map[string]string)
	if err == nil {
		if topicDetail, exists := allTopics[topic]; exists {
			for key, value := range topicDetail.ConfigEntries {
				if value != nil {
					configEntries[key] = *value
				}
			}
		}
	}

	output := map[string]interface{}{
		"success":       true,
		"topic":         topic,
		"partitions":    len(topicMeta.Partitions),
		"partitionInfo": partitionInfo,
		"config":        configEntries,
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// ============================================================================
// KAFKA CONSUMER GROUP LIST EXECUTOR
// ============================================================================

// KafkaConsumerGroupListExecutor handles kafka-consumer-group-list node type
type KafkaConsumerGroupListExecutor struct{}

func (e *KafkaConsumerGroupListExecutor) Type() string { return "kafka-consumer-group-list" }

func (e *KafkaConsumerGroupListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	kafkaCfg := parseKafkaConfig(config)
	if len(kafkaCfg.Brokers) == 0 {
		return nil, fmt.Errorf("brokers configuration is required")
	}

	// Get cluster admin
	admin, err := getClusterAdmin(kafkaCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create cluster admin: %w", err)
	}

	// List consumer groups
	groups, err := admin.ListConsumerGroups()
	if err != nil {
		return nil, fmt.Errorf("failed to list consumer groups: %w", err)
	}

	// Extract group names
	var groupNames []string
	for name := range groups {
		groupNames = append(groupNames, name)
	}

	// Sort for consistent output
	sort.Strings(groupNames)

	// Get detailed info for each group
	var groupDetails []map[string]interface{}
	for _, groupName := range groupNames {
		desc, err := admin.DescribeConsumerGroups([]string{groupName})
		if err != nil {
			continue
		}

		if len(desc) > 0 {
			group := desc[0]
			members := make([]map[string]interface{}, 0)
			for _, m := range group.Members {
				memberData := map[string]interface{}{
					"clientId": m.ClientId,
					"host":     m.ClientHost,
				}
				members = append(members, memberData)
			}

			groupData := map[string]interface{}{
				"groupId":      group.GroupId,
				"state":        group.State,
				"protocolType": group.ProtocolType,
				"protocol":     group.Protocol,
				"members":      members,
			}
			groupDetails = append(groupDetails, groupData)
		}
	}

	output := map[string]interface{}{
		"success": true,
		"count":   len(groupNames),
		"groups":  groupNames,
		"details": groupDetails,
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// ============================================================================
// KAFKA CONSUMER GROUP RESET EXECUTOR
// ============================================================================

// KafkaConsumerGroupResetExecutor handles kafka-consumer-group-reset node type
type KafkaConsumerGroupResetExecutor struct{}

func (e *KafkaConsumerGroupResetExecutor) Type() string { return "kafka-consumer-group-reset" }

func (e *KafkaConsumerGroupResetExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	kafkaCfg := parseKafkaConfig(config)
	if len(kafkaCfg.Brokers) == 0 {
		return nil, fmt.Errorf("brokers configuration is required")
	}

	consumerGroup := getString(config, "consumerGroup")
	if consumerGroup == "" {
		return nil, fmt.Errorf("consumerGroup is required")
	}

	topic := getString(config, "topic")
	resetTo := getString(config, "resetTo")

	// Get cluster admin
	admin, err := getClusterAdmin(kafkaCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create cluster admin: %w", err)
	}

	// Get consumer group details
	desc, err := admin.DescribeConsumerGroups([]string{consumerGroup})
	if err != nil {
		return nil, fmt.Errorf("failed to describe consumer group: %w", err)
	}

	if len(desc) == 0 {
		return nil, fmt.Errorf("consumer group not found: %s", consumerGroup)
	}

	// Get client for offset operations
	client, err := getClient(kafkaCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create client: %w", err)
	}

	// Get topic partitions
	var topics []string
	if topic != "" {
		topics = []string{topic}
	} else {
		topics, err = client.Topics()
		if err != nil {
			return nil, fmt.Errorf("failed to list topics: %w", err)
		}
	}

	// Build partition offsets to delete
	resetCount := 0
	for _, t := range topics {
		partitions, err := client.Partitions(t)
		if err != nil {
			continue
		}

		for _, p := range partitions {
			// Delete the offset for this partition
			err := admin.DeleteConsumerGroupOffset(consumerGroup, t, p)
			if err != nil {
				// Offset might not exist, which is fine
				continue
			}
			resetCount++
		}
	}

	output := map[string]interface{}{
		"success":       true,
		"consumerGroup": consumerGroup,
		"topic":         topic,
		"resetTo":       resetTo,
		"offsetsReset":  resetCount,
		"message":       fmt.Sprintf("Successfully reset %d offsets for consumer group '%s'. Note: Consumer group will use %s offset on next poll.", resetCount, consumerGroup, resetTo),
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// ============================================================================
// KAFKA PARTITION LIST EXECUTOR
// ============================================================================

// KafkaPartitionListExecutor handles kafka-partition-list node type
type KafkaPartitionListExecutor struct{}

func (e *KafkaPartitionListExecutor) Type() string { return "kafka-partition-list" }

func (e *KafkaPartitionListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	kafkaCfg := parseKafkaConfig(config)
	if len(kafkaCfg.Brokers) == 0 {
		return nil, fmt.Errorf("brokers configuration is required")
	}

	topic := getString(config, "topic")
	if topic == "" {
		return nil, fmt.Errorf("topic is required")
	}

	// Get client for partition info
	client, err := getClient(kafkaCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create client: %w", err)
	}

	// Get partitions
	partitions, err := client.Partitions(topic)
	if err != nil {
		return nil, fmt.Errorf("failed to get partitions: %w", err)
	}

	// Get detailed partition info
	var partitionDetails []map[string]interface{}
	for _, p := range partitions {
		// Get leader
		leader, err := client.Leader(topic, p)
		leaderID := int32(-1)
		if err == nil && leader != nil {
			leaderID = leader.ID()
		}

		// Get replicas (returns []int32 directly)
		replicaIDs, _ := client.Replicas(topic, p)

		// Get in-sync replicas (returns []int32 directly)
		isrIDs, _ := client.InSyncReplicas(topic, p)

		// Get offsets using a temporary consumer
		newestOffset, _ := client.GetOffset(topic, p, sarama.OffsetNewest)
		oldestOffset, _ := client.GetOffset(topic, p, sarama.OffsetOldest)

		partitionData := map[string]interface{}{
			"partition":    p,
			"leader":       leaderID,
			"replicas":     replicaIDs,
			"isr":          isrIDs,
			"newestOffset": newestOffset,
			"oldestOffset": oldestOffset,
			"lag":          newestOffset - oldestOffset,
		}
		partitionDetails = append(partitionDetails, partitionData)
	}

	output := map[string]interface{}{
		"success":    true,
		"topic":      topic,
		"count":      len(partitions),
		"partitions": partitionDetails,
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// ============================================================================
// KAFKA OFFSET GET EXECUTOR
// ============================================================================

// KafkaOffsetGetExecutor handles kafka-offset-get node type
type KafkaOffsetGetExecutor struct{}

func (e *KafkaOffsetGetExecutor) Type() string { return "kafka-offset-get" }

func (e *KafkaOffsetGetExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	kafkaCfg := parseKafkaConfig(config)
	if len(kafkaCfg.Brokers) == 0 {
		return nil, fmt.Errorf("brokers configuration is required")
	}

	topic := getString(config, "topic")
	if topic == "" {
		return nil, fmt.Errorf("topic is required")
	}

	offsetType := getString(config, "offsetType")
	if offsetType == "" {
		offsetType = "both"
	}

	// Get client for offset operations
	client, err := getClient(kafkaCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create client: %w", err)
	}

	// Get partitions
	partitions, err := client.Partitions(topic)
	if err != nil {
		return nil, fmt.Errorf("failed to get partitions: %w", err)
	}

	// Get offsets for each partition
	var offsetDetails []map[string]interface{}
	for _, p := range partitions {
		partitionData := map[string]interface{}{
			"partition": p,
		}

		switch offsetType {
		case "newest":
			offset, err := client.GetOffset(topic, p, sarama.OffsetNewest)
			if err != nil {
				partitionData["error"] = err.Error()
			} else {
				partitionData["newestOffset"] = offset
			}

		case "oldest":
			offset, err := client.GetOffset(topic, p, sarama.OffsetOldest)
			if err != nil {
				partitionData["error"] = err.Error()
			} else {
				partitionData["oldestOffset"] = offset
			}

		default: // both
			newestOffset, _ := client.GetOffset(topic, p, sarama.OffsetNewest)
			oldestOffset, _ := client.GetOffset(topic, p, sarama.OffsetOldest)

			partitionData["newestOffset"] = newestOffset
			partitionData["oldestOffset"] = oldestOffset
			partitionData["lag"] = newestOffset - oldestOffset
		}

		offsetDetails = append(offsetDetails, partitionData)
	}

	output := map[string]interface{}{
		"success":    true,
		"topic":      topic,
		"offsetType": offsetType,
		"count":      len(offsetDetails),
		"offsets":    offsetDetails,
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}
