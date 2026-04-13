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

	"github.com/axiom-studio/skills.sdk/executor"
	"github.com/axiom-studio/skills.sdk/grpc"
	"github.com/axiom-studio/skills.sdk/resolver"
)

const (
	iconCohere       = "cpu"
	cohereAPIBaseURL = "https://api.cohere.ai/v1"
)

// HTTPClient is a reusable HTTP client
var httpClient = &http.Client{}

// ============================================================================
// MAIN
// ============================================================================

func main() {
	// Get port from env or use default
	port := os.Getenv("SKILL_PORT")
	if port == "" {
		port = "50098"
	}

	// Create skill server
	server := grpc.NewSkillServer("skill-cohere", "1.0.0")

	// Register executors for all node types from skill.yaml
	server.RegisterExecutorWithSchema("cohere-generate", &GenerateExecutor{}, GenerateSchema)
	server.RegisterExecutorWithSchema("cohere-chat", &ChatExecutor{}, ChatSchema)
	server.RegisterExecutorWithSchema("cohere-embed", &EmbedExecutor{}, EmbedSchema)
	server.RegisterExecutorWithSchema("cohere-classify", &ClassifyExecutor{}, ClassifySchema)
	server.RegisterExecutorWithSchema("cohere-summarize", &SummarizeExecutor{}, SummarizeSchema)
	server.RegisterExecutorWithSchema("cohere-rerank", &RerankExecutor{}, RerankSchema)
	server.RegisterExecutorWithSchema("cohere-detect-language", &DetectLanguageExecutor{}, DetectLanguageSchema)
	server.RegisterExecutorWithSchema("cohere-tokenize", &TokenizeExecutor{}, TokenizeSchema)

	fmt.Printf("Starting skill-cohere gRPC server on port %s\n", port)
	if err := server.Serve(port); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to serve: %v\n", err)
		os.Exit(1)
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

// Helper to get float from config
func getFloat(config map[string]interface{}, key string, def float64) float64 {
	if v, ok := config[key]; ok {
		switch n := v.(type) {
		case float64:
			return n
		case int:
			return float64(n)
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

// Helper to get interface slice from config
func getInterfaceSlice(config map[string]interface{}, key string) []interface{} {
	if v, ok := config[key]; ok {
		if arr, ok := v.([]interface{}); ok {
			return arr
		}
	}
	return nil
}

// ============================================================================
// HTTP CLIENT HELPER
// ============================================================================

// cohereRequest makes an HTTP POST request to the Cohere API
func cohereRequest(ctx context.Context, apiKey, endpoint string, requestBody interface{}, responseBody interface{}) error {
	if apiKey == "" {
		return fmt.Errorf("Cohere API key is required")
	}

	reqBody, err := json.Marshal(requestBody)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", cohereAPIBaseURL+endpoint, bytes.NewReader(reqBody))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("x-client-name", "axiom-skill-cohere")

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		var errResp map[string]interface{}
		if err := json.Unmarshal(respBody, &errResp); err == nil {
			if msg, ok := errResp["message"].(string); ok {
				return fmt.Errorf("Cohere API error (%d): %s", resp.StatusCode, msg)
			}
		}
		return fmt.Errorf("Cohere API error (%d): %s", resp.StatusCode, string(respBody))
	}

	if err := json.Unmarshal(respBody, responseBody); err != nil {
		return fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return nil
}

// ============================================================================
// SCHEMAS
// ============================================================================

// GenerateSchema is the UI schema for cohere-generate
var GenerateSchema = resolver.NewSchemaBuilder("cohere-generate").
	WithName("Cohere Generate").
	WithCategory("action").
	WithIcon(iconCohere).
	WithDescription("Generate text using Cohere's language models").
	AddSection("Connection").
		AddExpressionField("apiKey", "API Key",
			resolver.WithRequired(),
			resolver.WithPlaceholder("Enter your Cohere API key"),
			resolver.WithHint("Cohere API key (supports {{bindings.xxx}})"),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Model").
		AddSelectField("model", "Model",
			[]resolver.SelectOption{
				{Label: "Command R+ (Recommended)", Value: "command-r-plus"},
				{Label: "Command R", Value: "command-r"},
				{Label: "Command", Value: "command"},
				{Label: "Command Nightly", Value: "command-nightly"},
				{Label: "Command Light", Value: "command-light"},
				{Label: "Command Light Nightly", Value: "command-light-nightly"},
			},
			resolver.WithDefault("command-r-plus"),
			resolver.WithHint("The Cohere model to use"),
		).
		EndSection().
	AddSection("Prompt").
		AddTextareaField("prompt", "Prompt",
			resolver.WithRequired(),
			resolver.WithRows(6),
			resolver.WithPlaceholder("Enter your prompt here..."),
			resolver.WithHint("The text prompt to generate from"),
		).
		EndSection().
	AddSection("Parameters").
		AddSliderField("temperature", "Temperature", 0, 5,
			resolver.WithDefault(0.3),
			resolver.WithStep(0.1),
			resolver.WithHint("Controls randomness (0 = deterministic, 5 = very random)"),
		).
		AddNumberField("maxTokens", "Max Tokens",
			resolver.WithDefault(2048),
			resolver.WithHint("Maximum tokens to generate"),
		).
		AddSliderField("topP", "Top P", 0, 1,
			resolver.WithDefault(0.75),
			resolver.WithStep(0.05),
			resolver.WithHint("Nucleus sampling threshold"),
		).
		AddSliderField("topK", "Top K", 0, 500,
			resolver.WithDefault(0),
			resolver.WithStep(1),
			resolver.WithHint("Only sample from top K options (0 = disabled)"),
		).
		AddNumberField("numGenerations", "Num Generations",
			resolver.WithDefault(1),
			resolver.WithHint("Number of generations to return"),
		).
		EndSection().
	AddSection("Advanced").
		AddTagsField("stopSequences", "Stop Sequences",
			resolver.WithHint("Sequences where generation stops"),
		).
		AddSliderField("frequencyPenalty", "Frequency Penalty", 0, 1,
			resolver.WithDefault(0),
			resolver.WithStep(0.05),
			resolver.WithHint("Penalty for token frequency"),
		).
		AddSliderField("presencePenalty", "Presence Penalty", 0, 1,
			resolver.WithDefault(0),
			resolver.WithStep(0.05),
			resolver.WithHint("Penalty for token presence"),
		).
		AddJSONField("returnLikelihoods", "Return Likelihoods",
			resolver.WithHeight(50),
			resolver.WithHint("Return token likelihoods: NONE, START, END, ALL"),
		).
		EndSection().
	Build()

// ChatSchema is the UI schema for cohere-chat
var ChatSchema = resolver.NewSchemaBuilder("cohere-chat").
	WithName("Cohere Chat").
	WithCategory("action").
	WithIcon(iconCohere).
	WithDescription("Chat with Cohere's conversational models").
	AddSection("Connection").
		AddExpressionField("apiKey", "API Key",
			resolver.WithRequired(),
			resolver.WithPlaceholder("Enter your Cohere API key"),
			resolver.WithHint("Cohere API key (supports {{bindings.xxx}})"),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Model").
		AddSelectField("model", "Model",
			[]resolver.SelectOption{
				{Label: "Command R+ (Recommended)", Value: "command-r-plus"},
				{Label: "Command R", Value: "command-r"},
				{Label: "Command", Value: "command"},
			},
			resolver.WithDefault("command-r-plus"),
			resolver.WithHint("The Cohere model to use"),
		).
		EndSection().
	AddSection("Messages").
		AddTextareaField("message", "Message",
			resolver.WithRequired(),
			resolver.WithRows(4),
			resolver.WithPlaceholder("Enter your message..."),
			resolver.WithHint("The user message to send"),
		).
		AddJSONField("chatHistory", "Chat History",
			resolver.WithHeight(150),
			resolver.WithHint(`Array of previous messages: [{"role": "USER", "message": "..."}]`),
		).
		AddTextareaField("systemPrompt", "System Prompt",
			resolver.WithRows(4),
			resolver.WithPlaceholder("You are a helpful assistant..."),
			resolver.WithHint("Optional system prompt to set the behavior"),
		).
		EndSection().
	AddSection("Parameters").
		AddSliderField("temperature", "Temperature", 0, 5,
			resolver.WithDefault(0.3),
			resolver.WithStep(0.1),
			resolver.WithHint("Controls randomness"),
		).
		AddNumberField("maxTokens", "Max Tokens",
			resolver.WithDefault(1024),
			resolver.WithHint("Maximum tokens to generate"),
		).
		AddSliderField("topP", "Top P", 0, 1,
			resolver.WithDefault(0.75),
			resolver.WithStep(0.05),
			resolver.WithHint("Nucleus sampling threshold"),
		).
		AddSliderField("topK", "Top K", 0, 500,
			resolver.WithDefault(0),
			resolver.WithStep(1),
			resolver.WithHint("Only sample from top K options"),
		).
		EndSection().
	AddSection("Advanced").
		AddJSONField("connectors", "Connectors",
			resolver.WithHeight(100),
			resolver.WithHint("Connectors for web search or other integrations"),
		).
		AddJSONField("documents", "Documents",
			resolver.WithHeight(100),
			resolver.WithHint("Documents to use for grounded generation"),
		).
		AddJSONField("tools", "Tools",
			resolver.WithHeight(100),
			resolver.WithHint("Tool definitions for function calling"),
		).
		AddToggleField("promptTruncation", "Prompt Truncation",
			resolver.WithDefault(false),
			resolver.WithHint("Enable automatic prompt truncation"),
		).
		AddTagsField("stopSequences", "Stop Sequences",
			resolver.WithHint("Sequences where generation stops"),
		).
		EndSection().
	Build()

// EmbedSchema is the UI schema for cohere-embed
var EmbedSchema = resolver.NewSchemaBuilder("cohere-embed").
	WithName("Cohere Embed").
	WithCategory("action").
	WithIcon(iconCohere).
	WithDescription("Generate embeddings using Cohere's embedding models").
	AddSection("Connection").
		AddExpressionField("apiKey", "API Key",
			resolver.WithRequired(),
			resolver.WithPlaceholder("Enter your Cohere API key"),
			resolver.WithHint("Cohere API key (supports {{bindings.xxx}})"),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Model").
		AddSelectField("model", "Model",
			[]resolver.SelectOption{
				{Label: "embed-english-v3.0 (Recommended)", Value: "embed-english-v3.0"},
				{Label: "embed-multilingual-v3.0", Value: "embed-multilingual-v3.0"},
				{Label: "embed-english-light-v3.0", Value: "embed-english-light-v3.0"},
				{Label: "embed-multilingual-light-v3.0", Value: "embed-multilingual-light-v3.0"},
				{Label: "embed-english-v2.0", Value: "embed-english-v2.0"},
				{Label: "embed-english-light-v2.0", Value: "embed-english-light-v2.0"},
				{Label: "embed-multilingual-v2.0", Value: "embed-multilingual-v2.0"},
			},
			resolver.WithDefault("embed-english-v3.0"),
			resolver.WithHint("The embedding model to use"),
		).
		EndSection().
	AddSection("Input").
		AddTextareaField("input", "Input Text",
			resolver.WithRequired(),
			resolver.WithRows(4),
			resolver.WithPlaceholder("Text to generate embeddings for..."),
			resolver.WithHint("Single text to embed"),
		).
		AddJSONField("inputArray", "Input Array",
			resolver.WithHeight(100),
			resolver.WithHint("JSON array of strings for batch embedding (overrides input text)"),
		).
		EndSection().
	AddSection("Options").
		AddSelectField("inputType", "Input Type",
			[]resolver.SelectOption{
				{Label: "Search Query", Value: "search_query"},
				{Label: "Search Document", Value: "search_document"},
				{Label: "Classification", Value: "classification"},
				{Label: "Clustering", Value: "clustering"},
			},
			resolver.WithDefault("search_document"),
			resolver.WithHint("Type of input for optimized embeddings"),
		).
		AddSelectField("truncate", "Truncate",
			[]resolver.SelectOption{
				{Label: "None", Value: "NONE"},
				{Label: "Start", Value: "START"},
				{Label: "End", Value: "END"},
			},
			resolver.WithDefault("END"),
			resolver.WithHint("How to handle texts longer than max length"),
		).
		EndSection().
	Build()

// ClassifySchema is the UI schema for cohere-classify
var ClassifySchema = resolver.NewSchemaBuilder("cohere-classify").
	WithName("Cohere Classify").
	WithCategory("action").
	WithIcon(iconCohere).
	WithDescription("Classify text using Cohere's classification models").
	AddSection("Connection").
		AddExpressionField("apiKey", "API Key",
			resolver.WithRequired(),
			resolver.WithPlaceholder("Enter your Cohere API key"),
			resolver.WithHint("Cohere API key (supports {{bindings.xxx}})"),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Model").
		AddSelectField("model", "Model",
			[]resolver.SelectOption{
				{Label: "embed-english-v3.0", Value: "embed-english-v3.0"},
				{Label: "embed-multilingual-v3.0", Value: "embed-multilingual-v3.0"},
				{Label: "embed-english-light-v3.0", Value: "embed-english-light-v3.0"},
			},
			resolver.WithDefault("embed-english-v3.0"),
			resolver.WithHint("Model to use for classification"),
		).
		EndSection().
	AddSection("Input").
		AddTextareaField("text", "Text to Classify",
			resolver.WithRequired(),
			resolver.WithRows(4),
			resolver.WithPlaceholder("Text to classify..."),
			resolver.WithHint("The text to classify"),
		).
		EndSection().
	AddSection("Examples").
		AddJSONField("examples", "Training Examples",
			resolver.WithRequired(),
			resolver.WithHeight(200),
			resolver.WithHint(`Array of examples: [{"text": "...", "label": "..."}]`),
		).
		EndSection().
	AddSection("Options").
		AddSelectField("truncate", "Truncate",
			[]resolver.SelectOption{
				{Label: "None", Value: "NONE"},
				{Label: "Start", Value: "START"},
				{Label: "End", Value: "END"},
			},
			resolver.WithDefault("END"),
		).
		EndSection().
	Build()

// SummarizeSchema is the UI schema for cohere-summarize
var SummarizeSchema = resolver.NewSchemaBuilder("cohere-summarize").
	WithName("Cohere Summarize").
	WithCategory("action").
	WithIcon(iconCohere).
	WithDescription("Summarize text using Cohere's summarization models").
	AddSection("Connection").
		AddExpressionField("apiKey", "API Key",
			resolver.WithRequired(),
			resolver.WithPlaceholder("Enter your Cohere API key"),
			resolver.WithHint("Cohere API key (supports {{bindings.xxx}})"),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Model").
		AddSelectField("model", "Model",
			[]resolver.SelectOption{
				{Label: "summarize", Value: "summarize"},
				{Label: "Command R+", Value: "command-r-plus"},
				{Label: "Command R", Value: "command-r"},
			},
			resolver.WithDefault("summarize"),
			resolver.WithHint("Model to use for summarization"),
		).
		EndSection().
	AddSection("Input").
		AddTextareaField("text", "Text to Summarize",
			resolver.WithRequired(),
			resolver.WithRows(8),
			resolver.WithPlaceholder("Enter the text to summarize..."),
			resolver.WithHint("The text to summarize"),
		).
		EndSection().
	AddSection("Options").
		AddSelectField("length", "Summary Length",
			[]resolver.SelectOption{
				{Label: "Short", Value: "short"},
				{Label: "Medium", Value: "medium"},
				{Label: "Long", Value: "long"},
			},
			resolver.WithDefault("medium"),
			resolver.WithHint("Desired length of the summary"),
		).
		AddSelectField("format", "Format",
			[]resolver.SelectOption{
				{Label: "Paragraph", Value: "paragraph"},
				{Label: "Bullet Points", Value: "bullets"},
			},
			resolver.WithDefault("paragraph"),
			resolver.WithHint("Format of the summary"),
		).
		AddTextareaField("extractiveness", "Extractiveness",
			resolver.WithPlaceholder("high"),
			resolver.WithHint("How extractive vs abstractive (low, medium, high)"),
		).
		AddSliderField("temperature", "Temperature", 0, 5,
			resolver.WithDefault(0.3),
			resolver.WithStep(0.1),
			resolver.WithHint("Controls randomness"),
		).
		AddNumberField("maxTokens", "Max Tokens",
			resolver.WithHint("Maximum tokens in summary"),
		).
		EndSection().
	Build()

// RerankSchema is the UI schema for cohere-rerank
var RerankSchema = resolver.NewSchemaBuilder("cohere-rerank").
	WithName("Cohere Rerank").
	WithCategory("action").
	WithIcon(iconCohere).
	WithDescription("Rerank documents using Cohere's reranking models").
	AddSection("Connection").
		AddExpressionField("apiKey", "API Key",
			resolver.WithRequired(),
			resolver.WithPlaceholder("Enter your Cohere API key"),
			resolver.WithHint("Cohere API key (supports {{bindings.xxx}})"),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Model").
		AddSelectField("model", "Model",
			[]resolver.SelectOption{
				{Label: "rerank-english-v3.0 (Recommended)", Value: "rerank-english-v3.0"},
				{Label: "rerank-multilingual-v3.0", Value: "rerank-multilingual-v3.0"},
				{Label: "rerank-english-v2.0", Value: "rerank-english-v2.0"},
				{Label: "rerank-multilingual-v2.0", Value: "rerank-multilingual-v2.0"},
			},
			resolver.WithDefault("rerank-english-v3.0"),
			resolver.WithHint("The reranking model to use"),
		).
		EndSection().
	AddSection("Input").
		AddTextareaField("query", "Query",
			resolver.WithRequired(),
			resolver.WithRows(3),
			resolver.WithPlaceholder("Search query..."),
			resolver.WithHint("The query to rank documents against"),
		).
		AddJSONField("documents", "Documents",
			resolver.WithRequired(),
			resolver.WithHeight(200),
			resolver.WithHint(`Array of documents: ["doc1", "doc2", ...] or [{"text": "..."}, ...]`),
		).
		EndSection().
	AddSection("Options").
		AddNumberField("topN", "Top N",
			resolver.WithDefault(5),
			resolver.WithHint("Number of top documents to return"),
		).
		AddExpressionField("returnDocuments", "Return Documents",
			resolver.WithDefault("true"),
			resolver.WithHint("Whether to return document content"),
		).
		AddNumberField("maxChunksPerDoc", "Max Chunks Per Doc",
			resolver.WithHint("Maximum chunks per document"),
		).
		EndSection().
	Build()

// DetectLanguageSchema is the UI schema for cohere-detect-language
var DetectLanguageSchema = resolver.NewSchemaBuilder("cohere-detect-language").
	WithName("Cohere Detect Language").
	WithCategory("action").
	WithIcon(iconCohere).
	WithDescription("Detect the language of text using Cohere").
	AddSection("Connection").
		AddExpressionField("apiKey", "API Key",
			resolver.WithRequired(),
			resolver.WithPlaceholder("Enter your Cohere API key"),
			resolver.WithHint("Cohere API key (supports {{bindings.xxx}})"),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Input").
		AddTextareaField("text", "Text",
			resolver.WithRequired(),
			resolver.WithRows(4),
			resolver.WithPlaceholder("Enter text to detect language..."),
			resolver.WithHint("Text to detect language for"),
		).
		EndSection().
	Build()

// TokenizeSchema is the UI schema for cohere-tokenize
var TokenizeSchema = resolver.NewSchemaBuilder("cohere-tokenize").
	WithName("Cohere Tokenize").
	WithCategory("action").
	WithIcon(iconCohere).
	WithDescription("Tokenize text using Cohere's tokenizer").
	AddSection("Connection").
		AddExpressionField("apiKey", "API Key",
			resolver.WithRequired(),
			resolver.WithPlaceholder("Enter your Cohere API key"),
			resolver.WithHint("Cohere API key (supports {{bindings.xxx}})"),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Model").
		AddSelectField("model", "Model",
			[]resolver.SelectOption{
				{Label: "command-r-plus", Value: "command-r-plus"},
				{Label: "command-r", Value: "command-r"},
				{Label: "command", Value: "command"},
				{Label: "command-light", Value: "command-light"},
			},
			resolver.WithDefault("command-r-plus"),
			resolver.WithHint("Model whose tokenizer to use"),
		).
		EndSection().
	AddSection("Input").
		AddTextareaField("text", "Text",
			resolver.WithRequired(),
			resolver.WithRows(4),
			resolver.WithPlaceholder("Enter text to tokenize..."),
			resolver.WithHint("Text to tokenize"),
		).
		EndSection().
	Build()

// ============================================================================
// EXECUTORS
// ============================================================================

// GenerateExecutor handles cohere-generate node type
type GenerateExecutor struct{}

func (e *GenerateExecutor) Type() string { return "cohere-generate" }

func (e *GenerateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	apiKey := getString(config, "apiKey")
	if apiKey == "" {
		return nil, fmt.Errorf("apiKey is required")
	}

	model := getString(config, "model")
	if model == "" {
		model = "command-r-plus"
	}

	prompt := getString(config, "prompt")
	if prompt == "" {
		return nil, fmt.Errorf("prompt is required")
	}

	// Build request
	req := map[string]interface{}{
		"model":  model,
		"prompt": prompt,
	}

	// Add optional parameters
	if maxTokens := getInt(config, "maxTokens", 0); maxTokens > 0 {
		req["max_tokens"] = maxTokens
	}
	if temperature := getFloat(config, "temperature", 0); temperature > 0 {
		req["temperature"] = temperature
	}
	if topP := getFloat(config, "topP", 0); topP > 0 {
		req["p"] = topP
	}
	if topK := getInt(config, "topK", 0); topK > 0 {
		req["k"] = topK
	}
	if numGenerations := getInt(config, "numGenerations", 0); numGenerations > 0 {
		req["num_generations"] = numGenerations
	}
	if stopSequences := getStringSlice(config, "stopSequences"); len(stopSequences) > 0 {
		req["stop_sequences"] = stopSequences
	}
	if frequencyPenalty := getFloat(config, "frequencyPenalty", 0); frequencyPenalty > 0 {
		req["frequency_penalty"] = frequencyPenalty
	}
	if presencePenalty := getFloat(config, "presencePenalty", 0); presencePenalty > 0 {
		req["presence_penalty"] = presencePenalty
	}
	if returnLikelihoods := getString(config, "returnLikelihoods"); returnLikelihoods != "" {
		req["return_likelihoods"] = returnLikelihoods
	}

	// Make API call
	var resp map[string]interface{}
	if err := cohereRequest(ctx, apiKey, "/generate", req, &resp); err != nil {
		return nil, err
	}

	// Build output
	generations := make([]map[string]interface{}, 0)
	if generationsRaw, ok := resp["generations"].([]interface{}); ok {
		for _, gen := range generationsRaw {
			if genMap, ok := gen.(map[string]interface{}); ok {
				generations = append(generations, genMap)
			}
		}
	}

	var text string
	if len(generations) > 0 {
		if textVal, ok := generations[0]["text"].(string); ok {
			text = textVal
		}
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":     true,
			"text":        text,
			"generations": generations,
			"id":          resp["id"],
			"meta":        resp["meta"],
		},
	}, nil
}

// ChatExecutor handles cohere-chat node type
type ChatExecutor struct{}

func (e *ChatExecutor) Type() string { return "cohere-chat" }

func (e *ChatExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	apiKey := getString(config, "apiKey")
	if apiKey == "" {
		return nil, fmt.Errorf("apiKey is required")
	}

	model := getString(config, "model")
	if model == "" {
		model = "command-r-plus"
	}

	message := getString(config, "message")
	if message == "" {
		return nil, fmt.Errorf("message is required")
	}

	// Build request
	req := map[string]interface{}{
		"model":   model,
		"message": message,
	}

	// Add chat history
	if chatHistoryRaw := getInterfaceSlice(config, "chatHistory"); len(chatHistoryRaw) > 0 {
		chatHistory := make([]map[string]interface{}, 0, len(chatHistoryRaw))
		for _, h := range chatHistoryRaw {
			if hMap, ok := h.(map[string]interface{}); ok {
				chatHistory = append(chatHistory, hMap)
			}
		}
		req["chat_history"] = chatHistory
	}

	// Add system prompt
	if systemPrompt := getString(config, "systemPrompt"); systemPrompt != "" {
		req["preamble"] = systemPrompt
	}

	// Add optional parameters
	if maxTokens := getInt(config, "maxTokens", 0); maxTokens > 0 {
		req["max_tokens"] = maxTokens
	}
	if temperature := getFloat(config, "temperature", 0); temperature > 0 {
		req["temperature"] = temperature
	}
	if topP := getFloat(config, "topP", 0); topP > 0 {
		req["p"] = topP
	}
	if topK := getInt(config, "topK", 0); topK > 0 {
		req["k"] = topK
	}
	if stopSequences := getStringSlice(config, "stopSequences"); len(stopSequences) > 0 {
		req["stop_sequences"] = stopSequences
	}

	// Add connectors
	if connectorsRaw := getInterfaceSlice(config, "connectors"); len(connectorsRaw) > 0 {
		connectors := make([]map[string]interface{}, 0, len(connectorsRaw))
		for _, c := range connectorsRaw {
			if cMap, ok := c.(map[string]interface{}); ok {
				connectors = append(connectors, cMap)
			}
		}
		req["connectors"] = connectors
	}

	// Add documents
	if documentsRaw := getInterfaceSlice(config, "documents"); len(documentsRaw) > 0 {
		documents := make([]map[string]interface{}, 0, len(documentsRaw))
		for _, d := range documentsRaw {
			if dMap, ok := d.(map[string]interface{}); ok {
				documents = append(documents, dMap)
			}
		}
		req["documents"] = documents
	}

	// Add tools
	if toolsRaw := getInterfaceSlice(config, "tools"); len(toolsRaw) > 0 {
		tools := make([]map[string]interface{}, 0, len(toolsRaw))
		for _, t := range toolsRaw {
			if tMap, ok := t.(map[string]interface{}); ok {
				tools = append(tools, tMap)
			}
		}
		req["tools"] = tools
	}

	// Make API call
	var resp map[string]interface{}
	if err := cohereRequest(ctx, apiKey, "/chat", req, &resp); err != nil {
		return nil, err
	}

	// Build output
	text, _ := resp["text"].(string)

	var chatHistory []interface{}
	if historyRaw, ok := resp["chat_history"].([]interface{}); ok {
		chatHistory = historyRaw
	}

	var documents []interface{}
	if docsRaw, ok := resp["documents"].([]interface{}); ok {
		documents = docsRaw
	}

	var citations []interface{}
	if citationsRaw, ok := resp["citations"].([]interface{}); ok {
		citations = citationsRaw
	}

	var toolCalls []interface{}
	if toolCallsRaw, ok := resp["tool_calls"].([]interface{}); ok {
		toolCalls = toolCallsRaw
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":      true,
			"text":         text,
			"chatHistory":  chatHistory,
			"documents":    documents,
			"citations":    citations,
			"toolCalls":    toolCalls,
			"generationId": resp["generation_id"],
			"responseId":   resp["response_id"],
			"meta":         resp["meta"],
		},
	}, nil
}

// EmbedExecutor handles cohere-embed node type
type EmbedExecutor struct{}

func (e *EmbedExecutor) Type() string { return "cohere-embed" }

func (e *EmbedExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	apiKey := getString(config, "apiKey")
	if apiKey == "" {
		return nil, fmt.Errorf("apiKey is required")
	}

	model := getString(config, "model")
	if model == "" {
		model = "embed-english-v3.0"
	}

	// Get input - can be string or array
	var texts []string
	if inputArrayStr := getString(config, "inputArray"); inputArrayStr != "" {
		if err := json.Unmarshal([]byte(inputArrayStr), &texts); err != nil {
			return nil, fmt.Errorf("failed to parse inputArray: %w", err)
		}
	} else {
		input := getString(config, "input")
		if input == "" {
			return nil, fmt.Errorf("input or inputArray is required")
		}
		texts = []string{input}
	}

	// Build request
	req := map[string]interface{}{
		"model": model,
		"texts": texts,
	}

	// Add optional parameters
	if inputType := getString(config, "inputType"); inputType != "" {
		req["input_type"] = inputType
	}
	if truncate := getString(config, "truncate"); truncate != "" {
		req["truncate"] = truncate
	}

	// Make API call
	var resp map[string]interface{}
	if err := cohereRequest(ctx, apiKey, "/embed", req, &resp); err != nil {
		return nil, err
	}

	// Build output
	embeddings := make([][]float64, 0)
	if embeddingsRaw, ok := resp["embeddings"].([]interface{}); ok {
		for _, emb := range embeddingsRaw {
			if embFloat, ok := emb.([]interface{}); ok {
				floatEmb := make([]float64, len(embFloat))
				for i, v := range embFloat {
					if fv, ok := v.(float64); ok {
						floatEmb[i] = fv
					}
				}
				embeddings = append(embeddings, floatEmb)
			}
		}
	}

	var meta interface{}
	if metaVal, ok := resp["meta"]; ok {
		meta = metaVal
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":    true,
			"model":      resp["model"],
			"embeddings": embeddings,
			"meta":       meta,
		},
	}, nil
}

// ClassifyExecutor handles cohere-classify node type
type ClassifyExecutor struct{}

func (e *ClassifyExecutor) Type() string { return "cohere-classify" }

func (e *ClassifyExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	apiKey := getString(config, "apiKey")
	if apiKey == "" {
		return nil, fmt.Errorf("apiKey is required")
	}

	model := getString(config, "model")
	if model == "" {
		model = "embed-english-v3.0"
	}

	text := getString(config, "text")
	if text == "" {
		return nil, fmt.Errorf("text is required")
	}

	examplesRaw := getInterfaceSlice(config, "examples")
	if len(examplesRaw) == 0 {
		return nil, fmt.Errorf("examples are required")
	}

	// Parse examples
	examples := make([]map[string]interface{}, 0, len(examplesRaw))
	for _, ex := range examplesRaw {
		if exMap, ok := ex.(map[string]interface{}); ok {
			examples = append(examples, exMap)
		}
	}

	// Build request
	req := map[string]interface{}{
		"model":    model,
		"text":     text,
		"examples": examples,
	}

	// Add optional parameters
	if truncate := getString(config, "truncate"); truncate != "" {
		req["truncate"] = truncate
	}

	// Make API call
	var resp map[string]interface{}
	if err := cohereRequest(ctx, apiKey, "/classify", req, &resp); err != nil {
		return nil, err
	}

	// Build output
	var classifications []interface{}
	if classRaw, ok := resp["classifications"].([]interface{}); ok {
		classifications = classRaw
	}

	var prediction map[string]interface{}
	if len(classifications) > 0 {
		if classMap, ok := classifications[0].(map[string]interface{}); ok {
			prediction = classMap
		}
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":        true,
			"classifications": classifications,
			"prediction":     prediction,
			"meta":           resp["meta"],
		},
	}, nil
}

// SummarizeExecutor handles cohere-summarize node type
type SummarizeExecutor struct{}

func (e *SummarizeExecutor) Type() string { return "cohere-summarize" }

func (e *SummarizeExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	apiKey := getString(config, "apiKey")
	if apiKey == "" {
		return nil, fmt.Errorf("apiKey is required")
	}

	model := getString(config, "model")
	if model == "" {
		model = "summarize"
	}

	text := getString(config, "text")
	if text == "" {
		return nil, fmt.Errorf("text is required")
	}

	// Build request
	req := map[string]interface{}{
		"model": model,
		"text":  text,
	}

	// Add optional parameters
	if length := getString(config, "length"); length != "" {
		req["length"] = length
	}
	if format := getString(config, "format"); format != "" {
		req["format"] = format
	}
	if extractiveness := getString(config, "extractiveness"); extractiveness != "" {
		req["extractiveness"] = extractiveness
	}
	if temperature := getFloat(config, "temperature", 0); temperature > 0 {
		req["temperature"] = temperature
	}
	if maxTokens := getInt(config, "maxTokens", 0); maxTokens > 0 {
		req["max_tokens"] = maxTokens
	}

	// Make API call
	var resp map[string]interface{}
	if err := cohereRequest(ctx, apiKey, "/summarize", req, &resp); err != nil {
		return nil, err
	}

	// Build output
	summary, _ := resp["summary"].(string)

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":  true,
			"summary":  summary,
			"id":       resp["id"],
			"meta":     resp["meta"],
		},
	}, nil
}

// RerankExecutor handles cohere-rerank node type
type RerankExecutor struct{}

func (e *RerankExecutor) Type() string { return "cohere-rerank" }

func (e *RerankExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	apiKey := getString(config, "apiKey")
	if apiKey == "" {
		return nil, fmt.Errorf("apiKey is required")
	}

	model := getString(config, "model")
	if model == "" {
		model = "rerank-english-v3.0"
	}

	query := getString(config, "query")
	if query == "" {
		return nil, fmt.Errorf("query is required")
	}

	documentsRaw := getInterfaceSlice(config, "documents")
	if len(documentsRaw) == 0 {
		return nil, fmt.Errorf("documents are required")
	}

	// Parse documents - can be strings or objects
	documents := make([]interface{}, 0, len(documentsRaw))
	for _, doc := range documentsRaw {
		if docStr, ok := doc.(string); ok {
			documents = append(documents, map[string]interface{}{"text": docStr})
		} else if docMap, ok := doc.(map[string]interface{}); ok {
			documents = append(documents, docMap)
		}
	}

	// Build request
	req := map[string]interface{}{
		"model":     model,
		"query":     query,
		"documents": documents,
	}

	// Add optional parameters
	if topN := getInt(config, "topN", 0); topN > 0 {
		req["top_n"] = topN
	}
	if returnDocuments := getBool(config, "returnDocuments", true); !returnDocuments {
		req["return_documents"] = false
	}
	if maxChunksPerDoc := getInt(config, "maxChunksPerDoc", 0); maxChunksPerDoc > 0 {
		req["max_chunks_per_doc"] = maxChunksPerDoc
	}

	// Make API call
	var resp map[string]interface{}
	if err := cohereRequest(ctx, apiKey, "/rerank", req, &resp); err != nil {
		return nil, err
	}

	// Build output
	results := make([]map[string]interface{}, 0)
	if resultsRaw, ok := resp["results"].([]interface{}); ok {
		for _, res := range resultsRaw {
			if resMap, ok := res.(map[string]interface{}); ok {
				results = append(results, resMap)
			}
		}
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success": true,
			"results": results,
			"meta":    resp["meta"],
		},
	}, nil
}

// DetectLanguageExecutor handles cohere-detect-language node type
type DetectLanguageExecutor struct{}

func (e *DetectLanguageExecutor) Type() string { return "cohere-detect-language" }

func (e *DetectLanguageExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	apiKey := getString(config, "apiKey")
	if apiKey == "" {
		return nil, fmt.Errorf("apiKey is required")
	}

	text := getString(config, "text")
	if text == "" {
		return nil, fmt.Errorf("text is required")
	}

	// Build request
	req := map[string]interface{}{
		"texts": []string{text},
	}

	// Make API call
	var resp map[string]interface{}
	if err := cohereRequest(ctx, apiKey, "/detect-language", req, &resp); err != nil {
		return nil, err
	}

	// Build output
	var results []interface{}
	if resultsRaw, ok := resp["results"].([]interface{}); ok {
		results = resultsRaw
	}

	var detectedLanguage string
	var languageCode string
	if len(results) > 0 {
		if resMap, ok := results[0].(map[string]interface{}); ok {
			if lang, ok := resMap["language_code"].(string); ok {
				languageCode = lang
				detectedLanguage = lang
			}
		}
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":          true,
			"languageCode":     languageCode,
			"detectedLanguage": detectedLanguage,
			"results":          results,
			"meta":             resp["meta"],
		},
	}, nil
}

// TokenizeExecutor handles cohere-tokenize node type
type TokenizeExecutor struct{}

func (e *TokenizeExecutor) Type() string { return "cohere-tokenize" }

func (e *TokenizeExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	apiKey := getString(config, "apiKey")
	if apiKey == "" {
		return nil, fmt.Errorf("apiKey is required")
	}

	model := getString(config, "model")
	if model == "" {
		model = "command-r-plus"
	}

	text := getString(config, "text")
	if text == "" {
		return nil, fmt.Errorf("text is required")
	}

	// Build request
	req := map[string]interface{}{
		"model": model,
		"text":  text,
	}

	// Make API call
	var resp map[string]interface{}
	if err := cohereRequest(ctx, apiKey, "/tokenize", req, &resp); err != nil {
		return nil, err
	}

	// Build output
	var tokens []interface{}
	if tokensRaw, ok := resp["tokens"].([]interface{}); ok {
		tokens = tokensRaw
	}

	tokenCount := 0
	if count, ok := resp["token_count"].(float64); ok {
		tokenCount = int(count)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":     true,
			"tokens":      tokens,
			"tokenCount":  tokenCount,
			"text":        resp["text"],
		},
	}, nil
}
