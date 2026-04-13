package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/axiom-studio/skills.sdk/executor"
	"github.com/axiom-studio/skills.sdk/grpc"
	"github.com/axiom-studio/skills.sdk/resolver"
)

const (
	hfAPIBase        = "https://api-inference.huggingface.co"
	hfHubAPIBase     = "https://huggingface.co/api"
	hfDefaultTimeout = 120 * time.Second
)

// HTTP client with timeout
var httpClient = &http.Client{
	Timeout: hfDefaultTimeout,
}

// Cache for model info
var (
	modelInfoCache   = make(map[string]*ModelInfo)
	modelInfoCacheMux sync.RWMutex
)

func main() {
	port := os.Getenv("SKILL_PORT")
	if port == "" {
		port = "50097"
	}

	server := grpc.NewSkillServer("skill-huggingface", "1.0.0")

	// Register all executors with schemas
	server.RegisterExecutorWithSchema("hf-inference", &InferenceExecutor{}, InferenceSchema)
	server.RegisterExecutorWithSchema("hf-model-list", &ModelListExecutor{}, ModelListSchema)
	server.RegisterExecutorWithSchema("hf-model-info", &ModelInfoExecutor{}, ModelInfoSchema)
	server.RegisterExecutorWithSchema("hf-model-download", &ModelDownloadExecutor{}, ModelDownloadSchema)
	server.RegisterExecutorWithSchema("hf-dataset-list", &DatasetListExecutor{}, DatasetListSchema)
	server.RegisterExecutorWithSchema("hf-dataset-download", &DatasetDownloadExecutor{}, DatasetDownloadSchema)
	server.RegisterExecutorWithSchema("hf-space-create", &SpaceCreateExecutor{}, SpaceCreateSchema)
	server.RegisterExecutorWithSchema("hf-space-list", &SpaceListExecutor{}, SpaceListSchema)

	fmt.Printf("Starting skill-huggingface gRPC server on port %s\n", port)
	if err := server.Serve(port); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to serve: %v\n", err)
		os.Exit(1)
	}
}

// ============================================================================
// HELPER FUNCTIONS
// ============================================================================

// getString safely gets a string from config
func getString(config map[string]interface{}, key string) string {
	if v, ok := config[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// getInt safely gets an int from config
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

// getBool safely gets a bool from config
func getBool(config map[string]interface{}, key string, def bool) bool {
	if v, ok := config[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return def
}

// getStringSlice safely gets a string slice from config
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

// getInterfaceSlice safely gets an interface slice from config
func getInterfaceSlice(config map[string]interface{}, key string) []interface{} {
	if v, ok := config[key]; ok {
		if arr, ok := v.([]interface{}); ok {
			return arr
		}
	}
	return nil
}

// getMap safely gets a map from config
func getMap(config map[string]interface{}, key string) map[string]interface{} {
	if v, ok := config[key]; ok {
		if m, ok := v.(map[string]interface{}); ok {
			return m
		}
	}
	return nil
}

// makeHFRequest makes an authenticated request to Hugging Face API
func makeHFRequest(ctx context.Context, method, endpoint, token string, body interface{}) (*http.Response, error) {
	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
		reqBody = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, endpoint, reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	return resp, nil
}

// makeHFDownloadRequest makes a request for downloading files
func makeHFDownloadRequest(ctx context.Context, url, token string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	return resp, nil
}

// ModelInfo represents Hugging Face model information
type ModelInfo struct {
	ID            string   `json:"id"`
	ModelID       string   `json:"modelId"`
	PipelineTag   string   `json:"pipeline_tag"`
	LibraryName   string   `json:"library_name"`
	Tags          []string `json:"tags"`
	Downloads     int      `json:"downloads"`
	Likes         int      `json:"likes"`
	LastModified  string   `json:"lastModified"`
	CreatedAt     string   `json:"createdAt"`
	Private       bool     `json:"private"`
	Disabled      bool     `json:"disabled"`
	Gated         string   `json:"gated"`
	Transformers  string   `json:"transformersInfo,omitempty"`
	Config        map[string]interface{} `json:"config,omitempty"`
}

// DatasetInfo represents Hugging Face dataset information
type DatasetInfo struct {
	ID           string   `json:"id"`
	DatasetID    string   `json:"id"`
	Author       string   `json:"author"`
	CardData     string   `json:"cardData,omitempty"`
	Disabled     bool     `json:"disabled"`
	Downloads    int      `json:"downloads"`
	Gated        string   `json:"gated"`
	LastModified string   `json:"lastModified"`
	Likes        int      `json:"likes"`
	Private      bool     `json:"private"`
	Tags         []string `json:"tags"`
}

// SpaceInfo represents Hugging Face Space information
type SpaceInfo struct {
	ID           string `json:"id"`
	SpaceID      string `json:"id"`
	Author       string `json:"author"`
	CardData     string `json:"cardData,omitempty"`
	Color        string `json:"color"`
	CreatedAt    string `json:"createdAt"`
	Disabled     bool   `json:"disabled"`
	LastModified string `json:"lastModified"`
	Likes        int    `json:"likes"`
	Private      bool   `json:"private"`
	Runtime      struct {
		Stage     string `json:"stage"`
		Hostname  string `json:"hostname"`
		Replicas  int    `json:"replicas"`
	} `json:"runtime,omitempty"`
	SDK       string `json:"sdk"`
	Subdomain string `json:"subdomain"`
	Tags      []string `json:"tags"`
	Title     string `json:"title"`
}

// ============================================================================
// SCHEMAS
// ============================================================================

// InferenceSchema is the UI schema for hf-inference
var InferenceSchema = resolver.NewSchemaBuilder("hf-inference").
	WithName("Hugging Face Inference").
	WithCategory("action").
	WithIcon("cpu").
	WithDescription("Run inference on a Hugging Face model").
	AddSection("Connection").
		AddExpressionField("apiKey", "API Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("hf_..."),
			resolver.WithHint("Hugging Face API token (supports {{bindings.xxx}})"),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Model").
		AddExpressionField("model", "Model ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("google/flan-t5-base"),
			resolver.WithHint("Model ID in format author/model-name"),
		).
		AddSelectField("task", "Task Type",
			[]resolver.SelectOption{
				{Label: "Text Generation", Value: "text-generation"},
				{Label: "Text Classification", Value: "text-classification"},
				{Label: "Token Classification", Value: "token-classification"},
				{Label: "Question Answering", Value: "question-answering"},
				{Label: "Fill Mask", Value: "fill-mask"},
				{Label: "Summarization", Value: "summarization"},
				{Label: "Translation", Value: "translation"},
				{Label: "Text2Text Generation", Value: "text2text-generation"},
				{Label: "Feature Extraction", Value: "feature-extraction"},
				{Label: "Sentence Similarity", Value: "sentence-similarity"},
				{Label: "Zero-Shot Classification", Value: "zero-shot-classification"},
				{Label: "Image Classification", Value: "image-classification"},
				{Label: "Object Detection", Value: "object-detection"},
				{Label: "Image Segmentation", Value: "image-segmentation"},
				{Label: "Text-to-Image", Value: "text-to-image"},
				{Label: "Image-to-Text", Value: "image-to-text"},
				{Label: "Automatic Speech Recognition", Value: "automatic-speech-recognition"},
				{Label: "Audio Classification", Value: "audio-classification"},
			},
			resolver.WithHint("Optional: specify task type if not auto-detected"),
		).
		EndSection().
	AddSection("Input").
		AddExpressionField("inputs", "Input",
			resolver.WithRequired(),
			resolver.WithHint("Input text, image URL, or audio URL depending on task"),
		).
		AddTextareaField("prompt", "Prompt (for text generation)",
			resolver.WithRows(3),
			resolver.WithHint("Optional prompt template for text generation"),
		).
		EndSection().
	AddSection("Parameters").
		AddSliderField("temperature", "Temperature", 0, 2,
			resolver.WithDefault(1.0),
			resolver.WithStep(0.1),
			resolver.WithHint("Controls randomness in generation"),
		).
		AddNumberField("maxNewTokens", "Max New Tokens",
			resolver.WithDefault(100),
			resolver.WithHint("Maximum tokens to generate"),
		).
		AddNumberField("topK", "Top K",
			resolver.WithDefault(50),
			resolver.WithHint("Sample from top K tokens"),
		).
		AddSliderField("topP", "Top P", 0, 1,
			resolver.WithDefault(1.0),
			resolver.WithStep(0.05),
			resolver.WithHint("Nucleus sampling threshold"),
		).
		AddToggleField("waitForModel", "Wait for Model",
			resolver.WithDefault(true),
			resolver.WithHint("Wait for model to load if not ready"),
		).
		EndSection().
	Build()

// ModelListSchema is the UI schema for hf-model-list
var ModelListSchema = resolver.NewSchemaBuilder("hf-model-list").
	WithName("List Models").
	WithCategory("query").
	WithIcon("list").
	WithDescription("Search and list models from Hugging Face Hub").
	AddSection("Connection").
		AddExpressionField("apiKey", "API Token",
			resolver.WithPlaceholder("hf_..."),
			resolver.WithHint("Hugging Face API token for private models"),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Filters").
		AddExpressionField("search", "Search Query",
			resolver.WithPlaceholder("bert, gpt2, etc."),
			resolver.WithHint("Search term for model name or description"),
		).
		AddExpressionField("author", "Author",
			resolver.WithPlaceholder("google, meta, etc."),
			resolver.WithHint("Filter by model author/organization"),
		).
		AddTagsField("pipelineTags", "Pipeline Tags",
			resolver.WithHint("Filter by task type (text-classification, etc.)"),
		).
		AddTagsField("library", "Library",
			resolver.WithHint("Filter by library (transformers, pytorch, etc.)"),
		).
		AddToggleField("full", "Full Info",
			resolver.WithDefault(false),
			resolver.WithHint("Include full model information"),
		).
		EndSection().
	AddSection("Pagination").
		AddNumberField("limit", "Limit",
			resolver.WithDefault(20),
			resolver.WithHint("Maximum number of models to return"),
		).
		AddNumberField("offset", "Offset",
			resolver.WithDefault(0),
			resolver.WithHint("Number of models to skip"),
		).
		AddSelectField("sort", "Sort By",
			[]resolver.SelectOption{
				{Label: "Downloads", Value: "downloads"},
				{Label: "Likes", Value: "likes"},
				{Label: "Last Modified", Value: "lastModified"},
				{Label: "Created At", Value: "createdAt"},
			},
			resolver.WithDefault("downloads"),
		).
		AddSelectField("direction", "Direction",
			[]resolver.SelectOption{
				{Label: "Descending", Value: "-1"},
				{Label: "Ascending", Value: "1"},
			},
			resolver.WithDefault("-1"),
		).
		EndSection().
	Build()

// ModelInfoSchema is the UI schema for hf-model-info
var ModelInfoSchema = resolver.NewSchemaBuilder("hf-model-info").
	WithName("Get Model Info").
	WithCategory("query").
	WithIcon("info").
	WithDescription("Get detailed information about a specific model").
	AddSection("Connection").
		AddExpressionField("apiKey", "API Token",
			resolver.WithPlaceholder("hf_..."),
			resolver.WithHint("Hugging Face API token for private models"),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Model").
		AddExpressionField("modelId", "Model ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("google/flan-t5-base"),
			resolver.WithHint("Model ID in format author/model-name"),
		).
		EndSection().
	Build()

// ModelDownloadSchema is the UI schema for hf-model-download
var ModelDownloadSchema = resolver.NewSchemaBuilder("hf-model-download").
	WithName("Download Model File").
	WithCategory("action").
	WithIcon("download").
	WithDescription("Download a specific file from a Hugging Face model repository").
	AddSection("Connection").
		AddExpressionField("apiKey", "API Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("hf_..."),
			resolver.WithHint("Hugging Face API token"),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Model").
		AddExpressionField("modelId", "Model ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("google/flan-t5-base"),
			resolver.WithHint("Model ID in format author/model-name"),
		).
		AddExpressionField("filename", "Filename",
			resolver.WithRequired(),
			resolver.WithPlaceholder("config.json, pytorch_model.bin, etc."),
			resolver.WithHint("Path to the file within the model repository"),
		).
		AddExpressionField("revision", "Revision",
			resolver.WithDefault("main"),
			resolver.WithPlaceholder("main"),
			resolver.WithHint("Branch or commit hash"),
		).
		EndSection().
	AddSection("Output").
		AddSelectField("outputFormat", "Output Format",
			[]resolver.SelectOption{
				{Label: "Base64 Encoded", Value: "base64"},
				{Label: "Raw JSON", Value: "json"},
				{Label: "Raw Text", Value: "text"},
			},
			resolver.WithDefault("base64"),
			resolver.WithHint("How to return the file content"),
		).
		EndSection().
	Build()

// DatasetListSchema is the UI schema for hf-dataset-list
var DatasetListSchema = resolver.NewSchemaBuilder("hf-dataset-list").
	WithName("List Datasets").
	WithCategory("query").
	WithIcon("database").
	WithDescription("Search and list datasets from Hugging Face Hub").
	AddSection("Connection").
		AddExpressionField("apiKey", "API Token",
			resolver.WithPlaceholder("hf_..."),
			resolver.WithHint("Hugging Face API token for private datasets"),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Filters").
		AddExpressionField("search", "Search Query",
			resolver.WithPlaceholder("squad, imdb, etc."),
			resolver.WithHint("Search term for dataset name or description"),
		).
		AddExpressionField("author", "Author",
			resolver.WithPlaceholder("google, meta, etc."),
			resolver.WithHint("Filter by dataset author/organization"),
		).
		AddTagsField("tags", "Tags",
			resolver.WithHint("Filter by tags (task_categories, languages, etc.)"),
		).
		AddToggleField("full", "Full Info",
			resolver.WithDefault(false),
			resolver.WithHint("Include full dataset information"),
		).
		EndSection().
	AddSection("Pagination").
		AddNumberField("limit", "Limit",
			resolver.WithDefault(20),
			resolver.WithHint("Maximum number of datasets to return"),
		).
		AddNumberField("offset", "Offset",
			resolver.WithDefault(0),
			resolver.WithHint("Number of datasets to skip"),
		).
		AddSelectField("sort", "Sort By",
			[]resolver.SelectOption{
				{Label: "Downloads", Value: "downloads"},
				{Label: "Likes", Value: "likes"},
				{Label: "Last Modified", Value: "lastModified"},
			},
			resolver.WithDefault("downloads"),
		).
		AddSelectField("direction", "Direction",
			[]resolver.SelectOption{
				{Label: "Descending", Value: "-1"},
				{Label: "Ascending", Value: "1"},
			},
			resolver.WithDefault("-1"),
		).
		EndSection().
	Build()

// DatasetDownloadSchema is the UI schema for hf-dataset-download
var DatasetDownloadSchema = resolver.NewSchemaBuilder("hf-dataset-download").
	WithName("Download Dataset File").
	WithCategory("action").
	WithIcon("download").
	WithDescription("Download a specific file from a Hugging Face dataset repository").
	AddSection("Connection").
		AddExpressionField("apiKey", "API Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("hf_..."),
			resolver.WithHint("Hugging Face API token"),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Dataset").
		AddExpressionField("datasetId", "Dataset ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("squad, imdb, etc."),
			resolver.WithHint("Dataset ID in format author/dataset-name"),
		).
		AddExpressionField("filename", "Filename",
			resolver.WithRequired(),
			resolver.WithPlaceholder("train.json, test.csv, etc."),
			resolver.WithHint("Path to the file within the dataset repository"),
		).
		AddExpressionField("revision", "Revision",
			resolver.WithDefault("main"),
			resolver.WithPlaceholder("main"),
			resolver.WithHint("Branch or commit hash"),
		).
		EndSection().
	AddSection("Output").
		AddSelectField("outputFormat", "Output Format",
			[]resolver.SelectOption{
				{Label: "Base64 Encoded", Value: "base64"},
				{Label: "Raw JSON", Value: "json"},
				{Label: "Raw Text", Value: "text"},
			},
			resolver.WithDefault("base64"),
			resolver.WithHint("How to return the file content"),
		).
		EndSection().
	Build()

// SpaceCreateSchema is the UI schema for hf-space-create
var SpaceCreateSchema = resolver.NewSchemaBuilder("hf-space-create").
	WithName("Create Space").
	WithCategory("action").
	WithIcon("plus").
	WithDescription("Create a new Hugging Face Space").
	AddSection("Connection").
		AddExpressionField("apiKey", "API Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("hf_..."),
			resolver.WithHint("Hugging Face API token with write permissions"),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Space Configuration").
		AddExpressionField("spaceId", "Space ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("username/my-space"),
			resolver.WithHint("Space ID in format username/space-name"),
		).
		AddSelectField("sdk", "SDK",
			[]resolver.SelectOption{
				{Label: "Gradio", Value: "gradio"},
				{Label: "Streamlit", Value: "streamlit"},
				{Label: "Docker", Value: "docker"},
				{Label: "Static", Value: "static"},
			},
			resolver.WithRequired(),
			resolver.WithHint("SDK to use for the Space"),
		).
		AddSelectField("visibility", "Visibility",
			[]resolver.SelectOption{
				{Label: "Public", Value: "public"},
				{Label: "Private", Value: "private"},
			},
			resolver.WithDefault("public"),
			resolver.WithHint("Space visibility"),
		).
		EndSection().
	AddSection("Hardware (Optional)").
		AddSelectField("hardware", "Hardware",
			[]resolver.SelectOption{
				{Label: "CPU Basic", Value: "cpu-basic"},
				{Label: "CPU Upgrade", Value: "cpu-upgrade"},
				{Label: "T4 Small", Value: "t4-small"},
				{Label: "T4 Medium", Value: "t4-medium"},
				{Label: "A10G Small", Value: "a10g-small"},
				{Label: "A10G Large", Value: "a10g-large"},
				{Label: "A100 Large", Value: "a100-large"},
			},
			resolver.WithDefault("cpu-basic"),
			resolver.WithHint("Hardware configuration for the Space"),
		).
		EndSection().
	Build()

// SpaceListSchema is the UI schema for hf-space-list
var SpaceListSchema = resolver.NewSchemaBuilder("hf-space-list").
	WithName("List Spaces").
	WithCategory("query").
	WithIcon("list").
	WithDescription("List Hugging Face Spaces").
	AddSection("Connection").
		AddExpressionField("apiKey", "API Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("hf_..."),
			resolver.WithHint("Hugging Face API token"),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Filters").
		AddExpressionField("author", "Author",
			resolver.WithPlaceholder("username"),
			resolver.WithHint("Filter by author username"),
		).
		AddExpressionField("search", "Search Query",
			resolver.WithPlaceholder("search term"),
			resolver.WithHint("Search term for space name or description"),
		).
		AddTagsField("tags", "Tags",
			resolver.WithHint("Filter by tags"),
		).
		AddSelectField("sort", "Sort By",
			[]resolver.SelectOption{
				{Label: "Likes", Value: "likes"},
				{Label: "Last Modified", Value: "lastModified"},
				{Label: "Created At", Value: "createdAt"},
			},
			resolver.WithDefault("likes"),
		).
		EndSection().
	AddSection("Pagination").
		AddNumberField("limit", "Limit",
			resolver.WithDefault(20),
			resolver.WithHint("Maximum number of spaces to return"),
		).
		EndSection().
	Build()

// ============================================================================
// HF-INFERENCE EXECUTOR
// ============================================================================

// InferenceExecutor handles hf-inference
type InferenceExecutor struct{}

// Type returns the executor type
func (e *InferenceExecutor) Type() string {
	return "hf-inference"
}

// Execute runs inference on a Hugging Face model
func (e *InferenceExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	apiKey := resolver.ResolveString(getString(config, "apiKey"))
	model := resolver.ResolveString(getString(config, "model"))
	task := resolver.ResolveString(getString(config, "task"))
	inputs := resolver.ResolveString(getString(config, "inputs"))
	prompt := resolver.ResolveString(getString(config, "prompt"))

	if model == "" {
		return nil, fmt.Errorf("model ID is required")
	}
	if inputs == "" {
		return nil, fmt.Errorf("inputs are required")
	}

	// Prepend prompt to inputs if provided
	if prompt != "" {
		inputs = prompt + "\n" + inputs
	}

	// Build inference URL
	inferenceURL := fmt.Sprintf("%s/models/%s", hfAPIBase, url.PathEscape(model))
	if task != "" {
		inferenceURL = fmt.Sprintf("%s/pipeline/%s/%s", hfAPIBase, task, url.PathEscape(model))
	}

	// Build request body
	requestBody := map[string]interface{}{
		"inputs": inputs,
	}

	// Add parameters for text generation tasks
	params := map[string]interface{}{}
	if temp, ok := config["temperature"]; ok {
		switch v := temp.(type) {
		case float64:
			params["temperature"] = v
		case int:
			params["temperature"] = float64(v)
		}
	}
	if maxTokens, ok := config["maxNewTokens"]; ok {
		switch v := maxTokens.(type) {
		case float64:
			params["max_new_tokens"] = int(v)
		case int:
			params["max_new_tokens"] = v
		}
	}
	if topK, ok := config["topK"]; ok {
		switch v := topK.(type) {
		case float64:
			params["top_k"] = int(v)
		case int:
			params["top_k"] = v
		}
	}
	if topP, ok := config["topP"]; ok {
		switch v := topP.(type) {
		case float64:
			params["top_p"] = v
		case int:
			params["top_p"] = float64(v)
		}
	}

	if len(params) > 0 {
		requestBody["parameters"] = params
	}

	// Add wait_for_model parameter
	if waitForModel, ok := config["waitForModel"]; ok {
		if b, ok := waitForModel.(bool); ok {
			requestBody["wait_for_model"] = b
		}
	}

	// Make API request
	resp, err := makeHFRequest(ctx, "POST", inferenceURL, apiKey, requestBody)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("inference failed (status %d): %s", resp.StatusCode, string(body))
	}

	// Parse response
	var result interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Build output
	output := map[string]interface{}{
		"model":    model,
		"task":     task,
		"input":    inputs,
		"response": result,
	}

	// Extract text from common response formats
	if arr, ok := result.([]interface{}); ok && len(arr) > 0 {
		if first, ok := arr[0].(map[string]interface{}); ok {
			if text, ok := first["generated_text"].(string); ok {
				output["text"] = text
			}
			if score, ok := first["score"]; ok {
				output["score"] = score
			}
			if label, ok := first["label"]; ok {
				output["label"] = label
			}
		}
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// ============================================================================
// HF-MODEL-LIST EXECUTOR
// ============================================================================

// ModelListExecutor handles hf-model-list
type ModelListExecutor struct{}

// Type returns the executor type
func (e *ModelListExecutor) Type() string {
	return "hf-model-list"
}

// Execute lists models from Hugging Face Hub
func (e *ModelListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	apiKey := resolver.ResolveString(getString(config, "apiKey"))
	search := resolver.ResolveString(getString(config, "search"))
	author := resolver.ResolveString(getString(config, "author"))
	pipelineTags := getStringSlice(config, "pipelineTags")
	library := getStringSlice(config, "library")
	full := getBool(config, "full", false)
	limit := getInt(config, "limit", 20)
	offset := getInt(config, "offset", 0)
	sort := resolver.ResolveString(getString(config, "sort"))
	direction := resolver.ResolveString(getString(config, "direction"))

	// Build query parameters
	queryParams := url.Values{}
	if search != "" {
		queryParams.Set("search", search)
	}
	if author != "" {
		queryParams.Set("author", author)
	}
	for _, tag := range pipelineTags {
		queryParams.Add("pipeline_tag", tag)
	}
	for _, lib := range library {
		queryParams.Add("library", lib)
	}
	if full {
		queryParams.Set("full", "true")
	}
	if limit > 0 {
		queryParams.Set("limit", fmt.Sprintf("%d", limit))
	}
	if offset > 0 {
		queryParams.Set("offset", fmt.Sprintf("%d", offset))
	}
	if sort != "" {
		queryParams.Set("sort", sort)
	}
	if direction != "" {
		queryParams.Set("direction", direction)
	}

	// Build URL
	listURL := fmt.Sprintf("%s/models?%s", hfHubAPIBase, queryParams.Encode())

	// Make API request
	resp, err := makeHFRequest(ctx, "GET", listURL, apiKey, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to list models (status %d): %s", resp.StatusCode, string(body))
	}

	// Parse response
	var models []ModelInfo
	if err := json.Unmarshal(body, &models); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Build output
	modelList := make([]map[string]interface{}, 0, len(models))
	for _, model := range models {
		modelMap := map[string]interface{}{
			"id":           model.ID,
			"modelId":      model.ModelID,
			"pipelineTag":  model.PipelineTag,
			"libraryName":  model.LibraryName,
			"tags":         model.Tags,
			"downloads":    model.Downloads,
			"likes":        model.Likes,
			"lastModified": model.LastModified,
			"createdAt":    model.CreatedAt,
			"private":      model.Private,
			"disabled":     model.Disabled,
			"gated":        model.Gated,
		}
		modelList = append(modelList, modelMap)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"models": modelList,
			"count":  len(modelList),
		},
	}, nil
}

// ============================================================================
// HF-MODEL-INFO EXECUTOR
// ============================================================================

// ModelInfoExecutor handles hf-model-info
type ModelInfoExecutor struct{}

// Type returns the executor type
func (e *ModelInfoExecutor) Type() string {
	return "hf-model-info"
}

// Execute gets detailed information about a model
func (e *ModelInfoExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	apiKey := resolver.ResolveString(getString(config, "apiKey"))
	modelId := resolver.ResolveString(getString(config, "modelId"))

	if modelId == "" {
		return nil, fmt.Errorf("model ID is required")
	}

	// Check cache
	modelInfoCacheMux.RLock()
	if info, ok := modelInfoCache[modelId]; ok {
		modelInfoCacheMux.RUnlock()
		return &executor.StepResult{
			Output: map[string]interface{}{
				"model": info,
			},
		}, nil
	}
	modelInfoCacheMux.RUnlock()

	// Build URL
	infoURL := fmt.Sprintf("%s/models/%s", hfHubAPIBase, url.PathEscape(modelId))

	// Make API request
	resp, err := makeHFRequest(ctx, "GET", infoURL, apiKey, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to get model info (status %d): %s", resp.StatusCode, string(body))
	}

	// Parse response
	var info ModelInfo
	if err := json.Unmarshal(body, &info); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Cache the result
	modelInfoCacheMux.Lock()
	modelInfoCache[modelId] = &info
	modelInfoCacheMux.Unlock()

	// Build output
	output := map[string]interface{}{
		"id":            info.ID,
		"modelId":       info.ModelID,
		"pipelineTag":   info.PipelineTag,
		"libraryName":   info.LibraryName,
		"tags":          info.Tags,
		"downloads":     info.Downloads,
		"likes":         info.Likes,
		"lastModified":  info.LastModified,
		"createdAt":     info.CreatedAt,
		"private":       info.Private,
		"disabled":      info.Disabled,
		"gated":         info.Gated,
		"transformers":  info.Transformers,
		"config":        info.Config,
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// ============================================================================
// HF-MODEL-DOWNLOAD EXECUTOR
// ============================================================================

// ModelDownloadExecutor handles hf-model-download
type ModelDownloadExecutor struct{}

// Type returns the executor type
func (e *ModelDownloadExecutor) Type() string {
	return "hf-model-download"
}

// Execute downloads a file from a model repository
func (e *ModelDownloadExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	apiKey := resolver.ResolveString(getString(config, "apiKey"))
	modelId := resolver.ResolveString(getString(config, "modelId"))
	filename := resolver.ResolveString(getString(config, "filename"))
	revision := resolver.ResolveString(getString(config, "revision"))
	outputFormat := resolver.ResolveString(getString(config, "outputFormat"))

	if modelId == "" {
		return nil, fmt.Errorf("model ID is required")
	}
	if filename == "" {
		return nil, fmt.Errorf("filename is required")
	}
	if revision == "" {
		revision = "main"
	}
	if outputFormat == "" {
		outputFormat = "base64"
	}

	// Build download URL
	downloadURL := fmt.Sprintf("https://huggingface.co/%s/resolve/%s/%s",
		url.PathEscape(modelId),
		url.PathEscape(revision),
		url.PathEscape(filename))

	// Make download request
	resp, err := makeHFDownloadRequest(ctx, downloadURL, apiKey)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("file not found: %s in model %s", filename, modelId)
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("download failed (status %d): %s", resp.StatusCode, string(body))
	}

	// Read file content
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	// Format output based on requested format
	var content interface{}
	switch outputFormat {
	case "base64":
		content = base64.StdEncoding.EncodeToString(body)
	case "json":
		var jsonData interface{}
		if err := json.Unmarshal(body, &jsonData); err != nil {
			content = string(body)
		} else {
			content = jsonData
		}
	case "text":
		content = string(body)
	default:
		content = base64.StdEncoding.EncodeToString(body)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"modelId":    modelId,
			"filename":   filename,
			"revision":   revision,
			"size":       len(body),
			"format":     outputFormat,
			"content":    content,
			"mimeType":   resp.Header.Get("Content-Type"),
		},
	}, nil
}

// ============================================================================
// HF-DATASET-LIST EXECUTOR
// ============================================================================

// DatasetListExecutor handles hf-dataset-list
type DatasetListExecutor struct{}

// Type returns the executor type
func (e *DatasetListExecutor) Type() string {
	return "hf-dataset-list"
}

// Execute lists datasets from Hugging Face Hub
func (e *DatasetListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	apiKey := resolver.ResolveString(getString(config, "apiKey"))
	search := resolver.ResolveString(getString(config, "search"))
	author := resolver.ResolveString(getString(config, "author"))
	tags := getStringSlice(config, "tags")
	full := getBool(config, "full", false)
	limit := getInt(config, "limit", 20)
	offset := getInt(config, "offset", 0)
	sort := resolver.ResolveString(getString(config, "sort"))
	direction := resolver.ResolveString(getString(config, "direction"))

	// Build query parameters
	queryParams := url.Values{}
	if search != "" {
		queryParams.Set("search", search)
	}
	if author != "" {
		queryParams.Set("author", author)
	}
	for _, tag := range tags {
		queryParams.Add("filter", tag)
	}
	if full {
		queryParams.Set("full", "true")
	}
	if limit > 0 {
		queryParams.Set("limit", fmt.Sprintf("%d", limit))
	}
	if offset > 0 {
		queryParams.Set("offset", fmt.Sprintf("%d", offset))
	}
	if sort != "" {
		queryParams.Set("sort", sort)
	}
	if direction != "" {
		queryParams.Set("direction", direction)
	}

	// Build URL
	listURL := fmt.Sprintf("%s/datasets?%s", hfHubAPIBase, queryParams.Encode())

	// Make API request
	resp, err := makeHFRequest(ctx, "GET", listURL, apiKey, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to list datasets (status %d): %s", resp.StatusCode, string(body))
	}

	// Parse response
	var datasets []DatasetInfo
	if err := json.Unmarshal(body, &datasets); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Build output
	datasetList := make([]map[string]interface{}, 0, len(datasets))
	for _, ds := range datasets {
		datasetMap := map[string]interface{}{
			"id":           ds.ID,
			"datasetId":    ds.DatasetID,
			"author":       ds.Author,
			"disabled":     ds.Disabled,
			"downloads":    ds.Downloads,
			"gated":        ds.Gated,
			"lastModified": ds.LastModified,
			"likes":        ds.Likes,
			"private":      ds.Private,
			"tags":         ds.Tags,
		}
		datasetList = append(datasetList, datasetMap)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"datasets": datasetList,
			"count":    len(datasetList),
		},
	}, nil
}

// ============================================================================
// HF-DATASET-DOWNLOAD EXECUTOR
// ============================================================================

// DatasetDownloadExecutor handles hf-dataset-download
type DatasetDownloadExecutor struct{}

// Type returns the executor type
func (e *DatasetDownloadExecutor) Type() string {
	return "hf-dataset-download"
}

// Execute downloads a file from a dataset repository
func (e *DatasetDownloadExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	apiKey := resolver.ResolveString(getString(config, "apiKey"))
	datasetId := resolver.ResolveString(getString(config, "datasetId"))
	filename := resolver.ResolveString(getString(config, "filename"))
	revision := resolver.ResolveString(getString(config, "revision"))
	outputFormat := resolver.ResolveString(getString(config, "outputFormat"))

	if datasetId == "" {
		return nil, fmt.Errorf("dataset ID is required")
	}
	if filename == "" {
		return nil, fmt.Errorf("filename is required")
	}
	if revision == "" {
		revision = "main"
	}
	if outputFormat == "" {
		outputFormat = "base64"
	}

	// Build download URL
	downloadURL := fmt.Sprintf("https://huggingface.co/datasets/%s/resolve/%s/%s",
		url.PathEscape(datasetId),
		url.PathEscape(revision),
		url.PathEscape(filename))

	// Make download request
	resp, err := makeHFDownloadRequest(ctx, downloadURL, apiKey)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("file not found: %s in dataset %s", filename, datasetId)
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("download failed (status %d): %s", resp.StatusCode, string(body))
	}

	// Read file content
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	// Format output based on requested format
	var content interface{}
	switch outputFormat {
	case "base64":
		content = base64.StdEncoding.EncodeToString(body)
	case "json":
		var jsonData interface{}
		if err := json.Unmarshal(body, &jsonData); err != nil {
			content = string(body)
		} else {
			content = jsonData
		}
	case "text":
		content = string(body)
	default:
		content = base64.StdEncoding.EncodeToString(body)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"datasetId":  datasetId,
			"filename":   filename,
			"revision":   revision,
			"size":       len(body),
			"format":     outputFormat,
			"content":    content,
			"mimeType":   resp.Header.Get("Content-Type"),
		},
	}, nil
}

// ============================================================================
// HF-SPACE-CREATE EXECUTOR
// ============================================================================

// SpaceCreateExecutor handles hf-space-create
type SpaceCreateExecutor struct{}

// Type returns the executor type
func (e *SpaceCreateExecutor) Type() string {
	return "hf-space-create"
}

// Execute creates a new Hugging Face Space
func (e *SpaceCreateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	apiKey := resolver.ResolveString(getString(config, "apiKey"))
	spaceId := resolver.ResolveString(getString(config, "spaceId"))
	sdk := resolver.ResolveString(getString(config, "sdk"))
	visibility := resolver.ResolveString(getString(config, "visibility"))
	hardware := resolver.ResolveString(getString(config, "hardware"))

	if spaceId == "" {
		return nil, fmt.Errorf("space ID is required")
	}
	if sdk == "" {
		return nil, fmt.Errorf("SDK is required")
	}
	if visibility == "" {
		visibility = "public"
	}
	if hardware == "" {
		hardware = "cpu-basic"
	}

	// Build request body
	requestBody := map[string]interface{}{
		"id":   spaceId,
		"sdk":  sdk,
		"private": visibility == "private",
	}

	// Add hardware if specified
	if hardware != "" && hardware != "cpu-basic" {
		requestBody["hardware"] = hardware
	}

	// Build URL
	createURL := fmt.Sprintf("%s/spaces", hfHubAPIBase)

	// Make API request
	resp, err := makeHFRequest(ctx, "POST", createURL, apiKey, requestBody)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("failed to create space (status %d): %s", resp.StatusCode, string(body))
	}

	// Parse response
	var space SpaceInfo
	if err := json.Unmarshal(body, &space); err != nil {
		// If parsing fails, still return basic info
		return &executor.StepResult{
			Output: map[string]interface{}{
				"spaceId":    spaceId,
				"sdk":        sdk,
				"visibility": visibility,
				"hardware":   hardware,
				"status":     "created",
			},
		}, nil
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"id":           space.ID,
			"spaceId":      space.SpaceID,
			"author":       space.Author,
			"sdk":          space.SDK,
			"private":      space.Private,
			"subdomain":    space.Subdomain,
			"url":          fmt.Sprintf("https://huggingface.co/spaces/%s", space.SpaceID),
			"status":       "created",
			"createdAt":    space.CreatedAt,
		},
	}, nil
}

// ============================================================================
// HF-SPACE-LIST EXECUTOR
// ============================================================================

// SpaceListExecutor handles hf-space-list
type SpaceListExecutor struct{}

// Type returns the executor type
func (e *SpaceListExecutor) Type() string {
	return "hf-space-list"
}

// Execute lists Hugging Face Spaces
func (e *SpaceListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	apiKey := resolver.ResolveString(getString(config, "apiKey"))
	author := resolver.ResolveString(getString(config, "author"))
	search := resolver.ResolveString(getString(config, "search"))
	tags := getStringSlice(config, "tags")
	sort := resolver.ResolveString(getString(config, "sort"))
	limit := getInt(config, "limit", 20)

	// Build query parameters
	queryParams := url.Values{}
	if author != "" {
		queryParams.Set("author", author)
	}
	if search != "" {
		queryParams.Set("search", search)
	}
	for _, tag := range tags {
		queryParams.Add("filter", tag)
	}
	if sort != "" {
		queryParams.Set("sort", sort)
	}
	if limit > 0 {
		queryParams.Set("limit", fmt.Sprintf("%d", limit))
	}

	// Build URL
	listURL := fmt.Sprintf("%s/spaces?%s", hfHubAPIBase, queryParams.Encode())

	// Make API request
	resp, err := makeHFRequest(ctx, "GET", listURL, apiKey, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to list spaces (status %d): %s", resp.StatusCode, string(body))
	}

	// Parse response
	var spaces []SpaceInfo
	if err := json.Unmarshal(body, &spaces); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Build output
	spaceList := make([]map[string]interface{}, 0, len(spaces))
	for _, space := range spaces {
		spaceMap := map[string]interface{}{
			"id":           space.ID,
			"spaceId":      space.SpaceID,
			"author":       space.Author,
			"sdk":          space.SDK,
			"title":        space.Title,
			"private":      space.Private,
			"disabled":     space.Disabled,
			"likes":        space.Likes,
			"lastModified": space.LastModified,
			"createdAt":    space.CreatedAt,
			"subdomain":    space.Subdomain,
			"url":          fmt.Sprintf("https://huggingface.co/spaces/%s", space.SpaceID),
			"tags":         space.Tags,
		}
		if space.Runtime.Stage != "" {
			spaceMap["runtime"] = map[string]interface{}{
				"stage":    space.Runtime.Stage,
				"hostname": space.Runtime.Hostname,
				"replicas": space.Runtime.Replicas,
			}
		}
		spaceList = append(spaceList, spaceMap)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"spaces": spaceList,
			"count":  len(spaceList),
		},
	}, nil
}
