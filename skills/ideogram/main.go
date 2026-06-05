package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/axiom-studio/skills.sdk/executor"
	"github.com/axiom-studio/skills.sdk/grpc"
	"github.com/axiom-studio/skills.sdk/resolver"
)

const (
	ideogramAPIBaseURL = "https://api.ideogram.ai"
	iconIdeogram       = "image"
)

var httpClient = &http.Client{Timeout: 120 * time.Second}

func main() {
	port := os.Getenv("SKILL_PORT")
	if port == "" {
		port = "50131"
	}

	server := grpc.NewSkillServer("skill-ideogram", "1.0.0")
	server.RegisterExecutorWithSchema("ideogram-generate", &GenerateExecutor{}, GenerateSchema)

	fmt.Printf("Starting skill-ideogram gRPC server on port %s\n", port)
	if err := server.Serve(port); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to serve: %v\n", err)
		os.Exit(1)
	}
}

func getString(config map[string]interface{}, key string) string {
	if v, ok := config[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func getBool(config map[string]interface{}, key string, def bool) bool {
	if v, ok := config[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return def
}

func ideogramMultipartRequest(ctx context.Context, apiKey, endpoint string, fields map[string]string) (map[string]interface{}, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("Ideogram API key is required")
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for key, value := range fields {
		if value == "" {
			continue
		}
		if err := writer.WriteField(key, value); err != nil {
			return nil, fmt.Errorf("failed to write field %s: %w", key, err)
		}
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("failed to close multipart writer: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ideogramAPIBaseURL+endpoint, &body)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Api-Key", apiKey)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Accept", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, formatAPIError("Ideogram", resp.StatusCode, respBody)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}
	return result, nil
}

func formatAPIError(provider string, statusCode int, body []byte) error {
	var errResp map[string]interface{}
	if err := json.Unmarshal(body, &errResp); err == nil {
		if msg, ok := errResp["message"].(string); ok {
			return fmt.Errorf("%s API error (%d): %s", provider, statusCode, msg)
		}
		if detail, ok := errResp["detail"].(string); ok {
			return fmt.Errorf("%s API error (%d): %s", provider, statusCode, detail)
		}
		if errValue, ok := errResp["error"].(string); ok {
			return fmt.Errorf("%s API error (%d): %s", provider, statusCode, errValue)
		}
	}
	return fmt.Errorf("%s API error (%d): %s", provider, statusCode, string(body))
}

type GenerateExecutor struct{}

type GenerateConfig struct {
	APIKey                   string `json:"apiKey" description:"Ideogram API key"`
	TextPrompt               string `json:"textPrompt" description:"Natural-language prompt for image generation"`
	Resolution               string `json:"resolution" default:"2048x2048" description:"Output image resolution"`
	RenderingSpeed           string `json:"renderingSpeed" default:"DEFAULT" description:"Rendering speed: TURBO, DEFAULT, or QUALITY"`
	EnableCopyrightDetection bool   `json:"enableCopyrightDetection" default:"false" description:"Opt into copyright detection"`
}

var GenerateSchema = resolver.NewSchemaBuilder("ideogram-generate").
	WithName("Generate Image").
	WithCategory("action").
	WithIcon(iconIdeogram).
	WithDescription("Generate an image with Ideogram 4.0").
	AddSection("Connection").
	AddExpressionField("apiKey", "API Key",
		resolver.WithRequired(),
		resolver.WithPlaceholder("Enter your Ideogram API key"),
		resolver.WithHint("Ideogram API key (supports {{bindings.xxx}})"),
		resolver.WithSensitive(),
	).
	EndSection().
	AddSection("Prompt").
	AddTextareaField("textPrompt", "Prompt",
		resolver.WithRequired(),
		resolver.WithRows(5),
		resolver.WithPlaceholder("A bold poster that reads BUILD WITH IDEOGRAM"),
	).
	EndSection().
	AddSection("Output").
	AddSelectField("resolution", "Resolution",
		[]resolver.SelectOption{
			{Label: "Square 2048x2048", Value: "2048x2048"},
			{Label: "Landscape 2304x1728", Value: "2304x1728"},
			{Label: "Portrait 1728x2304", Value: "1728x2304"},
			{Label: "Wide 2560x1440", Value: "2560x1440"},
			{Label: "Tall 1440x2560", Value: "1440x2560"},
		},
		resolver.WithDefault("2048x2048"),
	).
	AddSelectField("renderingSpeed", "Rendering Speed",
		[]resolver.SelectOption{
			{Label: "Default", Value: "DEFAULT"},
			{Label: "Turbo", Value: "TURBO"},
			{Label: "Quality", Value: "QUALITY"},
		},
		resolver.WithDefault("DEFAULT"),
		resolver.WithHint("Ideogram currently rejects FLASH for v4 generation"),
	).
	AddToggleField("enableCopyrightDetection", "Copyright Detection",
		resolver.WithDefault(false),
	).
	EndSection().
	Build()

func (e *GenerateExecutor) Type() string { return "ideogram-generate" }

func (e *GenerateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := templateResolver.ResolveMap(step.Config)

	apiKey := templateResolver.ResolveString(getString(config, "apiKey"))
	textPrompt := templateResolver.ResolveString(getString(config, "textPrompt"))
	if textPrompt == "" {
		return nil, fmt.Errorf("textPrompt is required")
	}

	fields := map[string]string{
		"text_prompt":                textPrompt,
		"resolution":                 getString(config, "resolution"),
		"rendering_speed":            getString(config, "renderingSpeed"),
		"enable_copyright_detection": strconv.FormatBool(getBool(config, "enableCopyrightDetection", false)),
	}

	result, err := ideogramMultipartRequest(ctx, apiKey, "/v1/ideogram-v4/generate", fields)
	if err != nil {
		return nil, err
	}

	return &executor.StepResult{Output: result}, nil
}
