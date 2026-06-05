package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/axiom-studio/skills.sdk/executor"
	"github.com/axiom-studio/skills.sdk/grpc"
	"github.com/axiom-studio/skills.sdk/resolver"
)

const (
	heygenAPIBaseURL = "https://api.heygen.com"
	iconHeyGen       = "video"
)

var httpClient = &http.Client{Timeout: 120 * time.Second}

func main() {
	port := os.Getenv("SKILL_PORT")
	if port == "" {
		port = "50132"
	}

	server := grpc.NewSkillServer("skill-heygen", "1.0.0")
	server.RegisterExecutorWithSchema("heygen-create-video", &CreateVideoExecutor{}, CreateVideoSchema)
	server.RegisterExecutorWithSchema("heygen-get-video", &GetVideoExecutor{}, GetVideoSchema)
	server.RegisterExecutorWithSchema("heygen-list-voices", &ListVoicesExecutor{}, ListVoicesSchema)

	fmt.Printf("Starting skill-heygen gRPC server on port %s\n", port)
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

func heygenRequest(ctx context.Context, apiKey, method, path string, body interface{}, headers map[string]string) (map[string]interface{}, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("HeyGen API key is required")
	}

	var bodyReader io.Reader
	if body != nil {
		bodyBytes, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request: %w", err)
		}
		bodyReader = bytes.NewReader(bodyBytes)
	}

	req, err := http.NewRequestWithContext(ctx, method, heygenAPIBaseURL+path, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for key, value := range headers {
		if value != "" {
			req.Header.Set(key, value)
		}
	}

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
		return nil, formatAPIError("HeyGen", resp.StatusCode, respBody)
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
		if errValue, ok := errResp["error"].(string); ok {
			return fmt.Errorf("%s API error (%d): %s", provider, statusCode, errValue)
		}
		if data, ok := errResp["data"].(map[string]interface{}); ok {
			if msg, ok := data["message"].(string); ok {
				return fmt.Errorf("%s API error (%d): %s", provider, statusCode, msg)
			}
		}
	}
	return fmt.Errorf("%s API error (%d): %s", provider, statusCode, string(body))
}

type CreateVideoExecutor struct{}

type CreateVideoConfig struct {
	APIKey         string                 `json:"apiKey" description:"HeyGen API key"`
	AvatarID       string                 `json:"avatarId" description:"HeyGen avatar ID"`
	Script         string                 `json:"script" description:"Text script for the avatar to speak"`
	VoiceID        string                 `json:"voiceId" description:"Optional voice ID"`
	Title          string                 `json:"title" description:"Display title"`
	Resolution     string                 `json:"resolution" default:"1080p" description:"Output resolution"`
	AspectRatio    string                 `json:"aspectRatio" default:"16:9" description:"Output aspect ratio"`
	CallbackURL    string                 `json:"callbackUrl" description:"Webhook callback URL"`
	CallbackID     string                 `json:"callbackId" description:"Caller-defined callback identifier"`
	IdempotencyKey string                 `json:"idempotencyKey" description:"Safe retry key"`
	Background     map[string]interface{} `json:"background" description:"Optional background setting"`
	VoiceSettings  map[string]interface{} `json:"voiceSettings" description:"Optional voice settings"`
	Engine         map[string]interface{} `json:"engine" description:"Optional engine setting"`
}

var CreateVideoSchema = resolver.NewSchemaBuilder("heygen-create-video").
	WithName("Create Video").
	WithCategory("action").
	WithIcon(iconHeyGen).
	WithDescription("Create a HeyGen avatar video").
	AddSection("Connection").
	AddExpressionField("apiKey", "API Key",
		resolver.WithRequired(),
		resolver.WithPlaceholder("Enter your HeyGen API key"),
		resolver.WithHint("HeyGen API key (supports {{bindings.xxx}})"),
		resolver.WithSensitive(),
	).
	EndSection().
	AddSection("Avatar").
	AddExpressionField("avatarId", "Avatar ID",
		resolver.WithRequired(),
		resolver.WithPlaceholder("avatar_id"),
	).
	AddExpressionField("voiceId", "Voice ID",
		resolver.WithPlaceholder("Optional voice_id"),
	).
	EndSection().
	AddSection("Script").
	AddTextareaField("script", "Script",
		resolver.WithRequired(),
		resolver.WithRows(6),
		resolver.WithPlaceholder("Write the script the avatar should speak."),
	).
	EndSection().
	AddSection("Output").
	AddExpressionField("title", "Title",
		resolver.WithPlaceholder("Campaign intro"),
	).
	AddSelectField("resolution", "Resolution",
		[]resolver.SelectOption{
			{Label: "1080p", Value: "1080p"},
			{Label: "720p", Value: "720p"},
			{Label: "4k", Value: "4k"},
		},
		resolver.WithDefault("1080p"),
	).
	AddSelectField("aspectRatio", "Aspect Ratio",
		[]resolver.SelectOption{
			{Label: "16:9", Value: "16:9"},
			{Label: "9:16", Value: "9:16"},
			{Label: "1:1", Value: "1:1"},
			{Label: "4:5", Value: "4:5"},
			{Label: "5:4", Value: "5:4"},
			{Label: "Auto", Value: "auto"},
		},
		resolver.WithDefault("16:9"),
	).
	EndSection().
	AddSection("Advanced").
	AddExpressionField("callbackUrl", "Callback URL",
		resolver.WithPlaceholder("https://example.com/webhook"),
	).
	AddExpressionField("callbackId", "Callback ID").
	AddExpressionField("idempotencyKey", "Idempotency Key",
		resolver.WithHint("Optional key for safely retrying create requests"),
	).
	AddJSONField("background", "Background",
		resolver.WithHeight(120),
		resolver.WithHint(`Optional JSON, for example {"type":"color","value":"#ffffff"}`),
	).
	AddJSONField("voiceSettings", "Voice Settings",
		resolver.WithHeight(120),
		resolver.WithHint(`Optional JSON, for example {"speed":1,"pitch":0,"volume":1}`),
	).
	AddJSONField("engine", "Engine",
		resolver.WithHeight(100),
		resolver.WithHint(`Optional JSON, for example {"type":"avatar_v"}`),
	).
	EndSection().
	Build()

func (e *CreateVideoExecutor) Type() string { return "heygen-create-video" }

func (e *CreateVideoExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg CreateVideoConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	}

	if cfg.AvatarID == "" {
		return nil, fmt.Errorf("avatarId is required")
	}
	if cfg.Script == "" {
		return nil, fmt.Errorf("script is required")
	}

	body := map[string]interface{}{
		"type":          "avatar",
		"avatar_id":     cfg.AvatarID,
		"script":        cfg.Script,
		"resolution":    cfg.Resolution,
		"aspect_ratio":  cfg.AspectRatio,
		"output_format": "mp4",
	}
	if cfg.Title != "" {
		body["title"] = cfg.Title
	}
	if cfg.VoiceID != "" {
		body["voice_id"] = cfg.VoiceID
	}
	if cfg.CallbackURL != "" {
		body["callback_url"] = cfg.CallbackURL
	}
	if cfg.CallbackID != "" {
		body["callback_id"] = cfg.CallbackID
	}
	if len(cfg.Background) > 0 {
		body["background"] = cfg.Background
	}
	if len(cfg.VoiceSettings) > 0 {
		body["voice_settings"] = cfg.VoiceSettings
	}
	if len(cfg.Engine) > 0 {
		body["engine"] = cfg.Engine
	}

	headers := map[string]string{"Idempotency-Key": cfg.IdempotencyKey}
	result, err := heygenRequest(ctx, cfg.APIKey, http.MethodPost, "/v3/videos", body, headers)
	if err != nil {
		return nil, err
	}

	return &executor.StepResult{Output: result}, nil
}

type GetVideoExecutor struct{}

type GetVideoConfig struct {
	APIKey  string `json:"apiKey" description:"HeyGen API key"`
	VideoID string `json:"videoId" description:"Video ID"`
}

var GetVideoSchema = resolver.NewSchemaBuilder("heygen-get-video").
	WithName("Get Video").
	WithCategory("action").
	WithIcon(iconHeyGen).
	WithDescription("Get HeyGen video details and output URLs").
	AddSection("Connection").
	AddExpressionField("apiKey", "API Key",
		resolver.WithRequired(),
		resolver.WithPlaceholder("Enter your HeyGen API key"),
		resolver.WithSensitive(),
	).
	EndSection().
	AddSection("Video").
	AddExpressionField("videoId", "Video ID",
		resolver.WithRequired(),
		resolver.WithPlaceholder("video_id"),
	).
	EndSection().
	Build()

func (e *GetVideoExecutor) Type() string { return "heygen-get-video" }

func (e *GetVideoExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := templateResolver.ResolveMap(step.Config)
	apiKey := templateResolver.ResolveString(getString(config, "apiKey"))
	videoID := templateResolver.ResolveString(getString(config, "videoId"))
	if videoID == "" {
		return nil, fmt.Errorf("videoId is required")
	}

	result, err := heygenRequest(ctx, apiKey, http.MethodGet, "/v3/videos/"+url.PathEscape(videoID), nil, nil)
	if err != nil {
		return nil, err
	}
	return &executor.StepResult{Output: result}, nil
}

type ListVoicesExecutor struct{}

var ListVoicesSchema = resolver.NewSchemaBuilder("heygen-list-voices").
	WithName("List Voices").
	WithCategory("action").
	WithIcon("mic").
	WithDescription("List HeyGen voices with optional filters").
	AddSection("Connection").
	AddExpressionField("apiKey", "API Key",
		resolver.WithRequired(),
		resolver.WithPlaceholder("Enter your HeyGen API key"),
		resolver.WithSensitive(),
	).
	EndSection().
	AddSection("Filters").
	AddSelectField("type", "Type",
		[]resolver.SelectOption{
			{Label: "Public", Value: "public"},
			{Label: "Private", Value: "private"},
		},
		resolver.WithDefault("public"),
	).
	AddExpressionField("engine", "Engine",
		resolver.WithPlaceholder("starfish"),
	).
	AddExpressionField("language", "Language",
		resolver.WithPlaceholder("English"),
	).
	AddSelectField("gender", "Gender",
		[]resolver.SelectOption{
			{Label: "Any", Value: ""},
			{Label: "Male", Value: "male"},
			{Label: "Female", Value: "female"},
		},
	).
	AddNumberField("limit", "Limit",
		resolver.WithDefault(20),
		resolver.WithMinMax(1, 100),
	).
	AddExpressionField("token", "Page Token").
	EndSection().
	Build()

func (e *ListVoicesExecutor) Type() string { return "heygen-list-voices" }

func (e *ListVoicesExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := templateResolver.ResolveMap(step.Config)
	apiKey := templateResolver.ResolveString(getString(config, "apiKey"))

	values := url.Values{}
	if voiceType := templateResolver.ResolveString(getString(config, "type")); voiceType != "" {
		values.Set("type", voiceType)
	}
	if engine := templateResolver.ResolveString(getString(config, "engine")); engine != "" {
		values.Set("engine", engine)
	}
	if language := templateResolver.ResolveString(getString(config, "language")); language != "" {
		values.Set("language", language)
	}
	if gender := templateResolver.ResolveString(getString(config, "gender")); gender != "" {
		values.Set("gender", gender)
	}
	if token := templateResolver.ResolveString(getString(config, "token")); token != "" {
		values.Set("token", token)
	}
	limit := getInt(config, "limit", 20)
	if limit > 0 {
		values.Set("limit", strconv.Itoa(limit))
	}

	path := "/v3/voices"
	if encoded := values.Encode(); encoded != "" {
		path += "?" + encoded
	}
	result, err := heygenRequest(ctx, apiKey, http.MethodGet, path, nil, nil)
	if err != nil {
		return nil, err
	}
	return &executor.StepResult{Output: result}, nil
}
