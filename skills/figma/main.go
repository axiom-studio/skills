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
	"strings"
	"sync"
	"time"

	"github.com/axiom-studio/skills.sdk/executor"
	"github.com/axiom-studio/skills.sdk/grpc"
	"github.com/axiom-studio/skills.sdk/resolver"
)

const (
	iconFigma       = "pen-tool"
	figmaAPIBaseURL = "https://api.figma.com/v1"
)

// Figma clients cache
var (
	httpClients = make(map[string]*http.Client)
	clientMux   sync.RWMutex
)

func main() {
	// Get port from env or use default
	port := os.Getenv("SKILL_PORT")
	if port == "" {
		port = "50121"
	}

	// Create skill server
	server := grpc.NewSkillServer("skill-figma", "1.0.0")

	// Register Figma executors with schemas
	server.RegisterExecutorWithSchema("figma-file-list", &FigmaFileListExecutor{}, FigmaFileListSchema)
	server.RegisterExecutorWithSchema("figma-file-get", &FigmaFileGetExecutor{}, FigmaFileGetSchema)
	server.RegisterExecutorWithSchema("figma-file-export", &FigmaFileExportExecutor{}, FigmaFileExportSchema)
	server.RegisterExecutorWithSchema("figma-comment-create", &FigmaCommentCreateExecutor{}, FigmaCommentCreateSchema)
	server.RegisterExecutorWithSchema("figma-comment-list", &FigmaCommentListExecutor{}, FigmaCommentListSchema)
	server.RegisterExecutorWithSchema("figma-team-projects", &FigmaTeamProjectsExecutor{}, FigmaTeamProjectsSchema)
	server.RegisterExecutorWithSchema("figma-project-files", &FigmaProjectFilesExecutor{}, FigmaProjectFilesSchema)
	server.RegisterExecutorWithSchema("figma-style-list", &FigmaStyleListExecutor{}, FigmaStyleListSchema)

	fmt.Printf("Starting skill-figma gRPC server on port %s\n", port)
	if err := server.Serve(port); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to serve: %v\n", err)
		os.Exit(1)
	}
}

// ============================================================================
// FIGMA CLIENT HELPERS
// ============================================================================

// getHTTPClient returns an HTTP client (cached)
func getHTTPClient() *http.Client {
	clientMux.RLock()
	client, ok := httpClients["default"]
	clientMux.RUnlock()

	if ok {
		return client
	}

	clientMux.Lock()
	defer clientMux.Unlock()

	// Double check
	if client, ok := httpClients["default"]; ok {
		return client
	}

	client = &http.Client{
		Timeout: 60 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 10,
			IdleConnTimeout:     90 * time.Second,
		},
	}
	httpClients["default"] = client
	return client
}

// figmaRequest makes an authenticated request to the Figma API
func figmaRequest(ctx context.Context, method, endpoint string, apiKey string, body interface{}) ([]byte, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("Figma API token is required")
	}

	var reqBody io.Reader
	if body != nil {
		jsonData, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
		reqBody = bytes.NewReader(jsonData)
	}

	req, err := http.NewRequestWithContext(ctx, method, figmaAPIBaseURL+endpoint, reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("X-Figma-Token", apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	client := getHTTPClient()
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("Figma API error (%d): %s", resp.StatusCode, string(respBody))
	}

	return respBody, nil
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

// ============================================================================
// FIGMA API DATA STRUCTURES
// ============================================================================

// FigmaFile represents a Figma file
type FigmaFile struct {
	Key         string    `json:"key"`
	Name        string    `json:"name"`
	LastModified time.Time `json:"last_modified"`
	ThumbnailURL string   `json:"thumbnail_url"`
	EditorType  string    `json:"editor_type"`
	Version     string    `json:"version"`
	Role        string    `json:"role"`
	IsArchived  bool      `json:"is_archived"`
	LinkAccess  string    `json:"link_access"`
}

// FigmaNode represents a node in a Figma file
type FigmaNode struct {
	ID              string                 `json:"id"`
	Name            string                 `json:"name"`
	Type            string                 `json:"type"`
	Visible         bool                   `json:"visible"`
	Opacity         float64                `json:"opacity"`
	BoundVariables  map[string]interface{} `json:"boundVariables"`
	Fills           []interface{}          `json:"fills"`
	Strokes         []interface{}          `json:"strokes"`
	StrokeWeight    float64                `json:"strokeWeight"`
	CornerRadius    float64                `json:"cornerRadius"`
	Constraints     interface{}            `json:"constraints"`
	LayoutGrids     []interface{}          `json:"layoutGrids"`
	AutoLayout      interface{}            `json:"autoLayout"`
	Children        []FigmaNode            `json:"children"`
	ComponentID     string                 `json:"componentId"`
	ComponentSetID  string                 `json:"componentSetId"`
	Prototype       interface{}            `json:"prototype"`
	BlendMode       string                 `json:"blendMode"`
	PreserveRatio   bool                   `json:"preserveRatio"`
	Rotation        float64                `json:"rotation"`
	Size            FigmaSize              `json:"size"`
	AbsoluteBoundingBox FigmaBoundingBox   `json:"absoluteBoundingBox"`
}

// FigmaSize represents dimensions
type FigmaSize struct {
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

// FigmaBoundingBox represents a bounding box
type FigmaBoundingBox struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

// FigmaComment represents a comment on a Figma file
type FigmaComment struct {
	ID           string    `json:"id"`
	FileKey      string    `json:"file_key"`
	ParentID     string    `json:"parent_id"`
	UserID       string    `json:"user_id"`
	CreatedAt    time.Time `json:"created_at"`
	Message      string    `json:"message"`
	ClientMeta   interface{} `json:"client_meta"`
	Reactions    []interface{} `json:"reactions"`
	ResolvedAt   *time.Time `json:"resolved_at"`
	ResolvedBy   interface{} `json:"resolved_by"`
}

// FigmaProject represents a Figma project
type FigmaProject struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	CreatedAt  time.Time `json:"created_at"`
	ModifiedAt time.Time `json:"modified_at"`
}

// FigmaStyle represents a style in a Figma file
type FigmaStyle struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	StyleType   string `json:"style_type"`
	Description string `json:"description"`
	FileKey     string `json:"file_key"`
	NodeID      string `json:"node_id"`
}

// FigmaUser represents a Figma user
type FigmaUser struct {
	ID        string `json:"id"`
	Handle    string `json:"handle"`
	Email     string `json:"email"`
	ImgURL    string `json:"img_url"`
}

// ============================================================================
// SCHEMAS
// ============================================================================

// FigmaFileListSchema is the UI schema for figma-file-list
var FigmaFileListSchema = resolver.NewSchemaBuilder("figma-file-list").
	WithName("List Figma Files").
	WithCategory("action").
	WithIcon(iconFigma).
	WithDescription("List files from a Figma team or project").
	AddSection("Connection").
		AddExpressionField("apiKey", "API Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("figd_xxxxxxxxxxxxxxxxxxxx"),
			resolver.WithHint("Figma Personal Access Token (supports {{bindings.xxx}})"),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Team").
		AddExpressionField("teamId", "Team ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("123456789012345"),
			resolver.WithHint("Figma Team ID to list files from"),
		).
		EndSection().
	AddSection("Filters").
		AddTextField("project-id", "Project ID (Optional)",
			resolver.WithPlaceholder("123456789012345"),
			resolver.WithHint("Filter by specific project ID"),
		).
		AddNumberField("limit", "Limit",
			resolver.WithDefault(100),
			resolver.WithMinMax(1, 500),
			resolver.WithHint("Maximum number of files to return"),
		).
		EndSection().
	Build()

// FigmaFileGetSchema is the UI schema for figma-file-get
var FigmaFileGetSchema = resolver.NewSchemaBuilder("figma-file-get").
	WithName("Get Figma File").
	WithCategory("action").
	WithIcon(iconFigma).
	WithDescription("Get file metadata and nodes from a Figma file").
	AddSection("Connection").
		AddExpressionField("apiKey", "API Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("figd_xxxxxxxxxxxxxxxxxxxx"),
			resolver.WithHint("Figma Personal Access Token"),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("File").
		AddExpressionField("fileKey", "File Key",
			resolver.WithRequired(),
			resolver.WithPlaceholder("abc123xyz456"),
			resolver.WithHint("Figma file key (from file URL)"),
		).
		AddTagsField("nodeIds", "Node IDs",
			resolver.WithHint("Specific node IDs to retrieve (leave empty for root)"),
		).
		AddNumberField("depth", "Depth",
			resolver.WithDefault(1),
			resolver.WithMinMax(0, 10),
			resolver.WithHint("Depth of nodes to retrieve (0 = metadata only)"),
		).
		EndSection().
	Build()

// FigmaFileExportSchema is the UI schema for figma-file-export
var FigmaFileExportSchema = resolver.NewSchemaBuilder("figma-file-export").
	WithName("Export Figma Assets").
	WithCategory("action").
	WithIcon(iconFigma).
	WithDescription("Export images/assets from a Figma file").
	AddSection("Connection").
		AddExpressionField("apiKey", "API Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("figd_xxxxxxxxxxxxxxxxxxxx"),
			resolver.WithHint("Figma Personal Access Token"),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("File & Nodes").
		AddExpressionField("fileKey", "File Key",
			resolver.WithRequired(),
			resolver.WithPlaceholder("abc123xyz456"),
			resolver.WithHint("Figma file key"),
		).
		AddTagsField("nodeIds", "Node IDs",
			resolver.WithRequired(),
			resolver.WithHint("Node IDs to export (e.g., 1:2, 1:3)"),
		).
		EndSection().
	AddSection("Export Settings").
		AddSelectField("format", "Format",
			[]resolver.SelectOption{
				{Label: "PNG", Value: "png"},
				{Label: "JPEG", Value: "jpg"},
				{Label: "SVG", Value: "svg"},
				{Label: "PDF", Value: "pdf"},
			},
			resolver.WithDefault("png"),
		).
		AddTextField("scale", "Scale",
			resolver.WithPlaceholder("1, 2, 0.5"),
			resolver.WithHint("Scale factor (e.g., 1, 2, 0.5)"),
		).
		AddTextField("constraint", "Constraint",
			resolver.WithPlaceholder("width=800, height=600"),
			resolver.WithHint("Size constraint (e.g., width=800)"),
		).
		EndSection().
	Build()

// FigmaCommentCreateSchema is the UI schema for figma-comment-create
var FigmaCommentCreateSchema = resolver.NewSchemaBuilder("figma-comment-create").
	WithName("Create Figma Comment").
	WithCategory("action").
	WithIcon(iconFigma).
	WithDescription("Create a comment on a Figma file").
	AddSection("Connection").
		AddExpressionField("apiKey", "API Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("figd_xxxxxxxxxxxxxxxxxxxx"),
			resolver.WithHint("Figma Personal Access Token"),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("File & Location").
		AddExpressionField("fileKey", "File Key",
			resolver.WithRequired(),
			resolver.WithPlaceholder("abc123xyz456"),
			resolver.WithHint("Figma file key"),
		).
		AddExpressionField("nodeId", "Node ID",
			resolver.WithHint("Node ID to comment on (optional)"),
		).
		AddNumberField("x", "X Position",
			resolver.WithHint("X position on canvas (0-1, optional)"),
		).
		AddNumberField("y", "Y Position",
			resolver.WithHint("Y position on canvas (0-1, optional)"),
		).
		EndSection().
	AddSection("Comment").
		AddTextareaField("message", "Message",
			resolver.WithRequired(),
			resolver.WithRows(4),
			resolver.WithPlaceholder("Add your comment here..."),
			resolver.WithHint("Comment text (supports Markdown)"),
		).
		EndSection().
	Build()

// FigmaCommentListSchema is the UI schema for figma-comment-list
var FigmaCommentListSchema = resolver.NewSchemaBuilder("figma-comment-list").
	WithName("List Figma Comments").
	WithCategory("action").
	WithIcon(iconFigma).
	WithDescription("List comments from a Figma file").
	AddSection("Connection").
		AddExpressionField("apiKey", "API Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("figd_xxxxxxxxxxxxxxxxxxxx"),
			resolver.WithHint("Figma Personal Access Token"),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("File").
		AddExpressionField("fileKey", "File Key",
			resolver.WithRequired(),
			resolver.WithPlaceholder("abc123xyz456"),
			resolver.WithHint("Figma file key"),
		).
		EndSection().
	AddSection("Filters").
		AddToggleField("includeResolved", "Include Resolved",
			resolver.WithDefault(false),
			resolver.WithHint("Include resolved comments"),
		).
		EndSection().
	Build()

// FigmaTeamProjectsSchema is the UI schema for figma-team-projects
var FigmaTeamProjectsSchema = resolver.NewSchemaBuilder("figma-team-projects").
	WithName("List Team Projects").
	WithCategory("action").
	WithIcon(iconFigma).
	WithDescription("List all projects in a Figma team").
	AddSection("Connection").
		AddExpressionField("apiKey", "API Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("figd_xxxxxxxxxxxxxxxxxxxx"),
			resolver.WithHint("Figma Personal Access Token"),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Team").
		AddExpressionField("teamId", "Team ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("123456789012345"),
			resolver.WithHint("Figma Team ID"),
		).
		EndSection().
	Build()

// FigmaProjectFilesSchema is the UI schema for figma-project-files
var FigmaProjectFilesSchema = resolver.NewSchemaBuilder("figma-project-files").
	WithName("List Project Files").
	WithCategory("action").
	WithIcon(iconFigma).
	WithDescription("List all files in a Figma project").
	AddSection("Connection").
		AddExpressionField("apiKey", "API Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("figd_xxxxxxxxxxxxxxxxxxxx"),
			resolver.WithHint("Figma Personal Access Token"),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Project").
		AddExpressionField("projectId", "Project ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("123456789012345"),
			resolver.WithHint("Figma Project ID"),
		).
		EndSection().
	Build()

// FigmaStyleListSchema is the UI schema for figma-style-list
var FigmaStyleListSchema = resolver.NewSchemaBuilder("figma-style-list").
	WithName("List Figma Styles").
	WithCategory("action").
	WithIcon(iconFigma).
	WithDescription("List all styles from a Figma file").
	AddSection("Connection").
		AddExpressionField("apiKey", "API Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("figd_xxxxxxxxxxxxxxxxxxxx"),
			resolver.WithHint("Figma Personal Access Token"),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("File").
		AddExpressionField("fileKey", "File Key",
			resolver.WithRequired(),
			resolver.WithPlaceholder("abc123xyz456"),
			resolver.WithHint("Figma file key"),
		).
		EndSection().
	AddSection("Filters").
		AddSelectField("styleType", "Style Type",
			[]resolver.SelectOption{
				{Label: "All Types", Value: ""},
				{Label: "Fill", Value: "FILL"},
				{Label: "Text", Value: "TEXT"},
				{Label: "Effect", Value: "EFFECT"},
				{Label: "Grid", Value: "GRID"},
			},
			resolver.WithDefault(""),
		).
		EndSection().
	Build()

// ============================================================================
// EXECUTORS
// ============================================================================

// FigmaFileListExecutor handles figma-file-list node type
type FigmaFileListExecutor struct{}

func (e *FigmaFileListExecutor) Type() string { return "figma-file-list" }

func (e *FigmaFileListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	apiKey := getString(step.Config, "apiKey")
	teamId := getString(step.Config, "teamId")
	projectID := getString(step.Config, "project-id")
	limit := getInt(step.Config, "limit", 100)

	if apiKey == "" {
		return nil, fmt.Errorf("apiKey is required")
	}
	if teamId == "" {
		return nil, fmt.Errorf("teamId is required")
	}

	var files []FigmaFile

	if projectID != "" {
		// Get files from specific project
		endpoint := fmt.Sprintf("/projects/%s/files", projectID)
		respBody, err := figmaRequest(ctx, http.MethodGet, endpoint, apiKey, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to get project files: %w", err)
		}

		var result struct {
			Files []FigmaFile `json:"files"`
		}
		if err := json.Unmarshal(respBody, &result); err != nil {
			return nil, fmt.Errorf("failed to parse response: %w", err)
		}
		files = result.Files
	} else {
		// Get files from team (via projects)
		endpoint := fmt.Sprintf("/teams/%s/projects", teamId)
		respBody, err := figmaRequest(ctx, http.MethodGet, endpoint, apiKey, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to get team projects: %w", err)
		}

		var result struct {
			Projects []struct {
				ID    string `json:"id"`
				Name  string `json:"name"`
				Files []FigmaFile `json:"files"`
			} `json:"projects"`
		}
		if err := json.Unmarshal(respBody, &result); err != nil {
			return nil, fmt.Errorf("failed to parse response: %w", err)
		}

		// Collect all files from all projects
		for _, project := range result.Projects {
			files = append(files, project.Files...)
			if len(files) >= limit {
				files = files[:limit]
				break
			}
		}
	}

	// Format output
	result := make([]map[string]interface{}, 0, len(files))
	for _, file := range files {
		result = append(result, map[string]interface{}{
			"key":           file.Key,
			"name":          file.Name,
			"lastModified":  file.LastModified,
			"thumbnailUrl":  file.ThumbnailURL,
			"editorType":    file.EditorType,
			"version":       file.Version,
			"role":          file.Role,
			"isArchived":    file.IsArchived,
			"linkAccess":    file.LinkAccess,
		})
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success": true,
			"files":   result,
			"count":   len(result),
			"teamId":  teamId,
		},
	}, nil
}

// FigmaFileGetExecutor handles figma-file-get node type
type FigmaFileGetExecutor struct{}

func (e *FigmaFileGetExecutor) Type() string { return "figma-file-get" }

func (e *FigmaFileGetExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	apiKey := getString(step.Config, "apiKey")
	fileKey := getString(step.Config, "fileKey")
	nodeIds := getStringSlice(step.Config, "nodeIds")
	depth := getInt(step.Config, "depth", 1)

	if apiKey == "" {
		return nil, fmt.Errorf("apiKey is required")
	}
	if fileKey == "" {
		return nil, fmt.Errorf("fileKey is required")
	}

	// Build endpoint
	endpoint := fmt.Sprintf("/files/%s", fileKey)

	// Add query parameters
	params := url.Values{}
	if len(nodeIds) > 0 {
		params.Set("ids", strings.Join(nodeIds, ","))
	}
	if depth > 0 {
		params.Set("depth", strconv.Itoa(depth))
	}
	if len(params) > 0 {
		endpoint += "?" + params.Encode()
	}

	respBody, err := figmaRequest(ctx, http.MethodGet, endpoint, apiKey, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get file: %w", err)
	}

	var result struct {
		Name       string                 `json:"name"`
		Key        string                 `json:"key"`
		LastModified string               `json:"last_modified"`
		ThumbnailURL string               `json:"thumbnail_url"`
		Version    string                 `json:"version"`
		Role       string                 `json:"role"`
		EditorType string                 `json:"editor_type"`
		IsArchived bool                   `json:"is_archived"`
		LinkAccess string                 `json:"link_access"`
		Components map[string]interface{} `json:"components"`
		ComponentSets map[string]interface{} `json:"component_sets"`
		SchemaVersion float64             `json:"schemaVersion"`
		Styles     map[string]interface{} `json:"styles"`
		Document   map[string]interface{} `json:"document"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Build output
	output := map[string]interface{}{
		"success":      true,
		"key":          result.Key,
		"name":         result.Name,
		"lastModified": result.LastModified,
		"thumbnailUrl": result.ThumbnailURL,
		"version":      result.Version,
		"role":         result.Role,
		"editorType":   result.EditorType,
		"isArchived":   result.IsArchived,
		"linkAccess":   result.LinkAccess,
		"schemaVersion": result.SchemaVersion,
	}

	if result.Document != nil {
		output["document"] = result.Document
	}
	if result.Components != nil {
		output["components"] = result.Components
	}
	if result.ComponentSets != nil {
		output["componentSets"] = result.ComponentSets
	}
	if result.Styles != nil {
		output["styles"] = result.Styles
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// FigmaFileExportExecutor handles figma-file-export node type
type FigmaFileExportExecutor struct{}

func (e *FigmaFileExportExecutor) Type() string { return "figma-file-export" }

func (e *FigmaFileExportExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	apiKey := getString(step.Config, "apiKey")
	fileKey := getString(step.Config, "fileKey")
	nodeIds := getStringSlice(step.Config, "nodeIds")
	format := getString(step.Config, "format")
	scale := getString(step.Config, "scale")
	constraint := getString(step.Config, "constraint")

	if apiKey == "" {
		return nil, fmt.Errorf("apiKey is required")
	}
	if fileKey == "" {
		return nil, fmt.Errorf("fileKey is required")
	}
	if len(nodeIds) == 0 {
		return nil, fmt.Errorf("nodeIds is required")
	}

	if format == "" {
		format = "png"
	}

	// Build endpoint
	endpoint := fmt.Sprintf("/images/%s", fileKey)

	// Add query parameters
	params := url.Values{}
	params.Set("ids", strings.Join(nodeIds, ","))
	params.Set("format", format)
	if scale != "" {
		params.Set("scale", scale)
	}
	if constraint != "" {
		params.Set("constraint", constraint)
	}
	endpoint += "?" + params.Encode()

	respBody, err := figmaRequest(ctx, http.MethodGet, endpoint, apiKey, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get export URLs: %w", err)
	}

	var result struct {
		Err  string             `json:"err"`
		Images map[string]string `json:"images"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if result.Err != "" {
		return nil, fmt.Errorf("Figma API error: %s", result.Err)
	}

	// Format output
	images := make([]map[string]interface{}, 0, len(result.Images))
	for nodeId, imageURL := range result.Images {
		images = append(images, map[string]interface{}{
			"nodeId": nodeId,
			"url":    imageURL,
			"format": format,
		})
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success": true,
			"fileKey": fileKey,
			"format":  format,
			"images":  images,
			"count":   len(images),
		},
	}, nil
}

// FigmaCommentCreateExecutor handles figma-comment-create node type
type FigmaCommentCreateExecutor struct{}

func (e *FigmaCommentCreateExecutor) Type() string { return "figma-comment-create" }

func (e *FigmaCommentCreateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	apiKey := getString(step.Config, "apiKey")
	fileKey := getString(step.Config, "fileKey")
	nodeId := getString(step.Config, "nodeId")
	message := getString(step.Config, "message")
	x := getFloat64(step.Config, "x", -1)
	y := getFloat64(step.Config, "y", -1)

	if apiKey == "" {
		return nil, fmt.Errorf("apiKey is required")
	}
	if fileKey == "" {
		return nil, fmt.Errorf("fileKey is required")
	}
	if message == "" {
		return nil, fmt.Errorf("message is required")
	}

	// Build request body
	body := map[string]interface{}{
		"message": message,
	}

	// Add client_meta for position if provided
	if nodeId != "" || (x >= 0 && y >= 0) {
		clientMeta := map[string]interface{}{}
		if nodeId != "" {
			clientMeta["node_id"] = nodeId
		}
		if x >= 0 && y >= 0 {
			clientMeta["point"] = map[string]float64{
				"x": x,
				"y": y,
			}
		}
		body["client_meta"] = clientMeta
	}

	endpoint := fmt.Sprintf("/files/%s/comments", fileKey)
	respBody, err := figmaRequest(ctx, http.MethodPost, endpoint, apiKey, body)
	if err != nil {
		return nil, fmt.Errorf("failed to create comment: %w", err)
	}

	var result FigmaComment
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":   true,
			"commentId": result.ID,
			"fileKey":   fileKey,
			"message":   result.Message,
			"createdAt": result.CreatedAt,
			"userId":    result.UserID,
		},
	}, nil
}

// FigmaCommentListExecutor handles figma-comment-list node type
type FigmaCommentListExecutor struct{}

func (e *FigmaCommentListExecutor) Type() string { return "figma-comment-list" }

func (e *FigmaCommentListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	apiKey := getString(step.Config, "apiKey")
	fileKey := getString(step.Config, "fileKey")
	includeResolved := getBool(step.Config, "includeResolved", false)

	if apiKey == "" {
		return nil, fmt.Errorf("apiKey is required")
	}
	if fileKey == "" {
		return nil, fmt.Errorf("fileKey is required")
	}

	endpoint := fmt.Sprintf("/files/%s/comments", fileKey)
	respBody, err := figmaRequest(ctx, http.MethodGet, endpoint, apiKey, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get comments: %w", err)
	}

	var result struct {
		Comments []FigmaComment `json:"comments"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Filter resolved comments if needed
	comments := result.Comments
	if !includeResolved {
		filtered := make([]FigmaComment, 0)
		for _, comment := range comments {
			if comment.ResolvedAt == nil {
				filtered = append(filtered, comment)
			}
		}
		comments = filtered
	}

	// Format output
	resultList := make([]map[string]interface{}, 0, len(comments))
	for _, comment := range comments {
		commentMap := map[string]interface{}{
			"id":         comment.ID,
			"fileKey":    comment.FileKey,
			"parentId":   comment.ParentID,
			"userId":     comment.UserID,
			"createdAt":  comment.CreatedAt,
			"message":    comment.Message,
			"reactions":  comment.Reactions,
			"resolved":   comment.ResolvedAt != nil,
		}
		if comment.ResolvedAt != nil {
			commentMap["resolvedAt"] = comment.ResolvedAt
		}
		resultList = append(resultList, commentMap)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":  true,
			"fileKey":  fileKey,
			"comments": resultList,
			"count":    len(resultList),
		},
	}, nil
}

// FigmaTeamProjectsExecutor handles figma-team-projects node type
type FigmaTeamProjectsExecutor struct{}

func (e *FigmaTeamProjectsExecutor) Type() string { return "figma-team-projects" }

func (e *FigmaTeamProjectsExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	apiKey := getString(step.Config, "apiKey")
	teamId := getString(step.Config, "teamId")

	if apiKey == "" {
		return nil, fmt.Errorf("apiKey is required")
	}
	if teamId == "" {
		return nil, fmt.Errorf("teamId is required")
	}

	endpoint := fmt.Sprintf("/teams/%s/projects", teamId)
	respBody, err := figmaRequest(ctx, http.MethodGet, endpoint, apiKey, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get team projects: %w", err)
	}

	var result struct {
		Projects []FigmaProject `json:"projects"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Format output
	projectList := make([]map[string]interface{}, 0, len(result.Projects))
	for _, project := range result.Projects {
		projectList = append(projectList, map[string]interface{}{
			"id":         project.ID,
			"name":       project.Name,
			"createdAt":  project.CreatedAt,
			"modifiedAt": project.ModifiedAt,
		})
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":  true,
			"teamId":   teamId,
			"projects": projectList,
			"count":    len(projectList),
		},
	}, nil
}

// FigmaProjectFilesExecutor handles figma-project-files node type
type FigmaProjectFilesExecutor struct{}

func (e *FigmaProjectFilesExecutor) Type() string { return "figma-project-files" }

func (e *FigmaProjectFilesExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	apiKey := getString(step.Config, "apiKey")
	projectId := getString(step.Config, "projectId")

	if apiKey == "" {
		return nil, fmt.Errorf("apiKey is required")
	}
	if projectId == "" {
		return nil, fmt.Errorf("projectId is required")
	}

	endpoint := fmt.Sprintf("/projects/%s/files", projectId)
	respBody, err := figmaRequest(ctx, http.MethodGet, endpoint, apiKey, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get project files: %w", err)
	}

	var result struct {
		Files []FigmaFile `json:"files"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Format output
	fileList := make([]map[string]interface{}, 0, len(result.Files))
	for _, file := range result.Files {
		fileList = append(fileList, map[string]interface{}{
			"key":          file.Key,
			"name":         file.Name,
			"lastModified": file.LastModified,
			"thumbnailUrl": file.ThumbnailURL,
			"editorType":   file.EditorType,
			"version":      file.Version,
			"role":         file.Role,
			"isArchived":   file.IsArchived,
			"linkAccess":   file.LinkAccess,
		})
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":   true,
			"projectId": projectId,
			"files":     fileList,
			"count":     len(fileList),
		},
	}, nil
}

// FigmaStyleListExecutor handles figma-style-list node type
type FigmaStyleListExecutor struct{}

func (e *FigmaStyleListExecutor) Type() string { return "figma-style-list" }

func (e *FigmaStyleListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	apiKey := getString(step.Config, "apiKey")
	fileKey := getString(step.Config, "fileKey")
	styleType := getString(step.Config, "styleType")

	if apiKey == "" {
		return nil, fmt.Errorf("apiKey is required")
	}
	if fileKey == "" {
		return nil, fmt.Errorf("fileKey is required")
	}

	// Get file with styles
	endpoint := fmt.Sprintf("/files/%s", fileKey)
	respBody, err := figmaRequest(ctx, http.MethodGet, endpoint, apiKey, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get file styles: %w", err)
	}

	var result struct {
		Styles map[string]struct {
			Name      string `json:"name"`
			StyleType string `json:"style_type"`
			NodeID    string `json:"node_id"`
		} `json:"styles"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Format output and filter by style type
	styleList := make([]map[string]interface{}, 0)
	for key, style := range result.Styles {
		// Filter by style type if specified
		if styleType != "" && style.StyleType != styleType {
			continue
		}

		styleList = append(styleList, map[string]interface{}{
			"key":       key,
			"name":      style.Name,
			"styleType": style.StyleType,
			"nodeId":    style.NodeID,
			"fileKey":   fileKey,
		})
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":   true,
			"fileKey":   fileKey,
			"styles":    styleList,
			"count":     len(styleList),
			"styleType": styleType,
		},
	}, nil
}

// getFloat64 helper to get float64 from config
func getFloat64(config map[string]interface{}, key string, def float64) float64 {
	if v, ok := config[key]; ok {
		switch n := v.(type) {
		case float64:
			return n
		case int:
			return float64(n)
		case string:
			if f, err := strconv.ParseFloat(n, 64); err == nil {
				return f
			}
		}
	}
	return def
}
