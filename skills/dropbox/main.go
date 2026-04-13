package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/axiom-studio/skills.sdk/executor"
	"github.com/axiom-studio/skills.sdk/grpc"
	"github.com/axiom-studio/skills.sdk/resolver"
)

const (
	// Dropbox API v2 endpoints
	DropboxAPIBase    = "https://api.dropboxapi.com/2"
	DropboxContentBase = "https://content.dropboxapi.com/2"
	IconDropbox       = "cloud"
)

// DropboxClient represents a Dropbox API client
type DropboxClient struct {
	AccessToken string
	HTTPClient  *http.Client
}

// Dropbox clients cache
var (
	dropboxClients = make(map[string]*DropboxClient)
	clientMux      sync.RWMutex
)

func main() {
	// Get port from env or use default
	port := os.Getenv("SKILL_PORT")
	if port == "" {
		port = "50052"
	}

	// Create skill server
	server := grpc.NewSkillServer("skill-dropbox", "1.0.0")

	// Register Dropbox executors with schemas
	server.RegisterExecutorWithSchema("dropbox-upload", &DropboxUploadExecutor{}, DropboxUploadSchema)
	server.RegisterExecutorWithSchema("dropbox-download", &DropboxDownloadExecutor{}, DropboxDownloadSchema)
	server.RegisterExecutorWithSchema("dropbox-list", &DropboxListExecutor{}, DropboxListSchema)
	server.RegisterExecutorWithSchema("dropbox-delete", &DropboxDeleteExecutor{}, DropboxDeleteSchema)
	server.RegisterExecutorWithSchema("dropbox-share", &DropboxShareExecutor{}, DropboxShareSchema)
	server.RegisterExecutorWithSchema("dropbox-search", &DropboxSearchExecutor{}, DropboxSearchSchema)

	fmt.Printf("Starting skill-dropbox gRPC server on port %s\n", port)
	if err := server.Serve(port); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to serve: %v\n", err)
		os.Exit(1)
	}
}

// ============================================================================
// DROPBOX CLIENT HELPERS
// ============================================================================

// getDropboxClient returns or creates a Dropbox client (cached)
func getDropboxClient(accessToken string) *DropboxClient {
	clientMux.RLock()
	client, ok := dropboxClients[accessToken]
	clientMux.RUnlock()

	if ok {
		return client
	}

	clientMux.Lock()
	defer clientMux.Unlock()

	// Double check
	if client, ok := dropboxClients[accessToken]; ok {
		return client
	}

	client = &DropboxClient{
		AccessToken: accessToken,
		HTTPClient: &http.Client{
			Timeout: 300 * time.Second, // 5 minute timeout for large file operations
		},
	}
	dropboxClients[accessToken] = client
	return client
}

// doRequest performs an HTTP POST request to the Dropbox API with JSON arg header
func (c *DropboxClient) doRequest(ctx context.Context, endpoint string, arg interface{}, body io.Reader) (*http.Response, error) {
	url := DropboxAPIBase + endpoint

	// Marshal arg to JSON
	argJSON, err := json.Marshal(arg)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal arg: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, body)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set Dropbox-specific headers
	req.Header.Set("Authorization", "Bearer "+c.AccessToken)
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Dropbox-API-Arg", string(argJSON))

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	return resp, nil
}

// doRPCRequest performs an HTTP POST request to the Dropbox API (RPC-style, no file content)
func (c *DropboxClient) doRPCRequest(ctx context.Context, endpoint string, arg interface{}) (*http.Response, error) {
	url := DropboxAPIBase + endpoint

	// Marshal arg to JSON
	argJSON, err := json.Marshal(arg)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal arg: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(argJSON))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set Dropbox-specific headers
	req.Header.Set("Authorization", "Bearer "+c.AccessToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	return resp, nil
}

// doDownloadRequest performs an HTTP POST request to download file content
func (c *DropboxClient) doDownloadRequest(ctx context.Context, endpoint string, arg interface{}) (*http.Response, error) {
	url := DropboxContentBase + endpoint

	// Marshal arg to JSON
	argJSON, err := json.Marshal(arg)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal arg: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(argJSON))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set Dropbox-specific headers
	req.Header.Set("Authorization", "Bearer "+c.AccessToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	return resp, nil
}

// doUploadRequest performs an HTTP POST request to upload file content
func (c *DropboxClient) doUploadRequest(ctx context.Context, endpoint string, arg interface{}, content []byte) (*http.Response, error) {
	url := DropboxContentBase + endpoint

	// Marshal arg to JSON
	argJSON, err := json.Marshal(arg)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal arg: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(content))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set Dropbox-specific headers
	req.Header.Set("Authorization", "Bearer "+c.AccessToken)
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Dropbox-API-Arg", string(argJSON))

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	return resp, nil
}

// checkResponse checks the response for errors and returns the body
func checkResponse(resp *http.Response) ([]byte, error) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return body, nil
	}

	return nil, &DropboxAPIError{
		StatusCode: resp.StatusCode,
		Message:    string(body),
	}
}

// checkResponseNoBody checks the response for errors without reading body (for downloads)
func checkResponseNoBody(resp *http.Response) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}

	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return &DropboxAPIError{
		StatusCode: resp.StatusCode,
		Message:    string(body),
	}
}

// DropboxAPIError represents a Dropbox API error
type DropboxAPIError struct {
	StatusCode int
	Message    string
}

func (e *DropboxAPIError) Error() string {
	// Try to parse error message
	var errData map[string]interface{}
	if err := json.Unmarshal([]byte(e.Message), &errData); err == nil {
		if summary, ok := errData["error_summary"].(string); ok {
			return fmt.Sprintf("Dropbox API error: status %d - %s", e.StatusCode, summary)
		}
		if errObj, ok := errData["error"].(map[string]interface{}); ok {
			if msg, ok := errObj["reason"].(string); ok {
				return fmt.Sprintf("Dropbox API error: status %d - %s", e.StatusCode, msg)
			}
		}
	}
	return fmt.Sprintf("Dropbox API error: status %d - %s", e.StatusCode, e.Message)
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

// ============================================================================
// DROPBOX API DATA STRUCTURES
// ============================================================================

// DropboxFileMetadata represents file metadata from Dropbox
type DropboxFileMetadata struct {
	Name            string `json:"name"`
	ID              string `json:"id"`
	ClientModified  string `json:"client_modified,omitempty"`
	ServerModified  string `json:"server_modified,omitempty"`
	Rev             string `json:"rev,omitempty"`
	Size            int64  `json:"size,omitempty"`
	PathLower       string `json:"path_lower,omitempty"`
	PathDisplay     string `json:"path_display,omitempty"`
	SharingInfo     *SharingInfo `json:"sharing_info,omitempty"`
	IsDownloadable  bool   `json:"is_downloadable,omitempty"`
	PropertyGroups  []PropertyGroup `json:"property_groups,omitempty"`
	HasExplicitSharedAncestor bool `json:"has_explicit_shared_ancestor,omitempty"`
	ContentHash     string `json:"content_hash,omitempty"`
	FileLockInfo    *FileLockInfo `json:"file_lock_info,omitempty"`
}

// DropboxFolderMetadata represents folder metadata from Dropbox
type DropboxFolderMetadata struct {
	Name            string `json:"name"`
	ID              string `json:"id"`
	PathLower       string `json:"path_lower,omitempty"`
	PathDisplay     string `json:"path_display,omitempty"`
	SharingInfo     *SharingInfo `json:"sharing_info,omitempty"`
	PropertyGroups  []PropertyGroup `json:"property_groups,omitempty"`
}

// DropboxDeletedMetadata represents deleted item metadata
type DropboxDeletedMetadata struct {
	Name        string `json:"name"`
	ID          string `json:"id"`
	PathLower   string `json:"path_lower,omitempty"`
	PathDisplay string `json:"path_display,omitempty"`
}

// SharingInfo represents sharing information
type SharingInfo struct {
	ReadOnly             bool   `json:"read_only,omitempty"`
	ParentSharedFolderID string `json:"parent_shared_folder_id,omitempty"`
	ModifiedBy           string `json:"modified_by,omitempty"`
}

// PropertyGroup represents a property group
type PropertyGroup struct {
	TemplateID string `json:"template_id"`
	Fields     []Field `json:"fields"`
}

// Field represents a field in a property group
type Field struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// FileLockInfo represents file lock information
type FileLockInfo struct {
	Created    string `json:"created"`
	LockholderName string `json:"lockholder_name"`
	IsLockholder bool   `json:"is_lockholder"`
}

// DropboxListFolderResult represents the result of list_folder
type DropboxListFolderResult struct {
	Entries   []map[string]interface{} `json:"entries"`
	Cursor    string                   `json:"cursor"`
	HasMore   bool                     `json:"has_more"`
}

// DropboxSearchMatch represents a search result match
type DropboxSearchMatch struct {
	MatchType   string                 `json:"match_type"`
	Metadata    map[string]interface{} `json:"metadata"`
	MatchScore  float64                `json:"match_score,omitempty"`
	Highlights  []string               `json:"highlights,omitempty"`
}

// DropboxSearchResult represents the result of search_v2
type DropboxSearchResult struct {
	Matches []DropboxSearchMatch `json:"matches"`
	HasMore bool                 `json:"has_more"`
	Start   int                  `json:"start"`
}

// DropboxSharedLink represents a shared link
type DropboxSharedLink struct {
	URL                string                 `json:"url"`
	Name               string                 `json:"name"`
	LinkPermissions    *LinkPermissions       `json:"link_permissions"`
	Expires            string                 `json:"expires,omitempty"`
	PathLower          string                 `json:"path_lower,omitempty"`
	ID                 string                 `json:"id"`
	FileMetadata       *DropboxFileMetadata   `json:"file_metadata,omitempty"`
	FolderMetadata     *DropboxFolderMetadata `json:"folder_metadata,omitempty"`
}

// LinkPermissions represents link permissions
type LinkPermissions struct {
	CanRevoke bool        `json:"can_revoke"`
	ResolvedVisibility interface{} `json:"resolved_visibility"`
	RequestedVisibility interface{} `json:"requested_visibility"`
	RequirePassword   bool        `json:"require_password"`
	EffectiveAudience interface{} `json:"effective_audience"`
}

// DropboxDeleteResult represents the result of delete_v2
type DropboxDeleteResult struct {
	Metadata map[string]interface{} `json:"metadata"`
}

// ============================================================================
// SCHEMAS
// ============================================================================

// DropboxUploadSchema is the UI schema for dropbox-upload
var DropboxUploadSchema = resolver.NewSchemaBuilder("dropbox-upload").
	WithName("Dropbox Upload").
	WithCategory("storage").
	WithIcon(IconDropbox).
	WithDescription("Upload files to Dropbox").
	AddSection("Authentication").
		AddExpressionField("accessToken", "Access Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("sl.ABC123..."),
			resolver.WithHint("Dropbox access token (supports {{bindings.xxx}})"),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Upload").
		AddTextField("path", "Destination Path",
			resolver.WithRequired(),
			resolver.WithPlaceholder("/folder/file.txt"),
			resolver.WithHint("Full path including filename"),
		).
		AddTextareaField("content", "File Content",
			resolver.WithRequired(),
			resolver.WithRows(10),
			resolver.WithHint("Content to upload"),
		).
		AddSelectField("mode", "Upload Mode", []resolver.SelectOption{
			{Label: "Add (overwrite if exists)", Value: "add"},
			{Label: "Update (fail if not exists)", Value: "update"},
			{Label: "Add (fail if exists)", Value: "add_no_overwrite"},
		}, resolver.WithDefault("add")).
		AddToggleField("autorename", "Auto-rename",
			resolver.WithDefault(false),
			resolver.WithHint("Auto-rename file if conflict exists"),
		).
		AddToggleField("Mute", "Mute Notifications",
			resolver.WithDefault(false),
			resolver.WithHint("Don't send notification emails"),
		).
		EndSection().
	Build()

// DropboxDownloadSchema is the UI schema for dropbox-download
var DropboxDownloadSchema = resolver.NewSchemaBuilder("dropbox-download").
	WithName("Dropbox Download").
	WithCategory("storage").
	WithIcon(IconDropbox).
	WithDescription("Download files from Dropbox").
	AddSection("Authentication").
		AddExpressionField("accessToken", "Access Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("sl.ABC123..."),
			resolver.WithHint("Dropbox access token (supports {{bindings.xxx}})"),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Download").
		AddTextField("path", "File Path",
			resolver.WithRequired(),
			resolver.WithPlaceholder("/folder/file.txt"),
			resolver.WithHint("Full path to the file"),
		).
		AddToggleField("base64Encode", "Base64 Encode",
			resolver.WithDefault(false),
			resolver.WithHint("Encode binary content as base64"),
		).
		EndSection().
	Build()

// DropboxListSchema is the UI schema for dropbox-list
var DropboxListSchema = resolver.NewSchemaBuilder("dropbox-list").
	WithName("Dropbox List").
	WithCategory("storage").
	WithIcon(IconDropbox).
	WithDescription("List contents of a Dropbox folder").
	AddSection("Authentication").
		AddExpressionField("accessToken", "Access Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("sl.ABC123..."),
			resolver.WithHint("Dropbox access token (supports {{bindings.xxx}})"),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("List Options").
		AddTextField("path", "Folder Path",
			resolver.WithDefault(""),
			resolver.WithPlaceholder("/folder"),
			resolver.WithHint("Leave empty for root directory"),
		).
		AddToggleField("recursive", "Recursive",
			resolver.WithDefault(false),
			resolver.WithHint("Include subfolders"),
		).
		AddToggleField("includeDeleted", "Include Deleted",
			resolver.WithDefault(false),
			resolver.WithHint("Include deleted items"),
		).
		AddToggleField("includeHasExplicitSharedAncestor", "Include Shared Ancestor Info",
			resolver.WithDefault(false),
			resolver.WithHint("Include shared ancestor information"),
		).
		AddNumberField("limit", "Max Entries",
			resolver.WithDefault(100),
			resolver.WithMinMax(1, 2000),
			resolver.WithHint("Maximum entries to return"),
		).
		EndSection().
	Build()

// DropboxDeleteSchema is the UI schema for dropbox-delete
var DropboxDeleteSchema = resolver.NewSchemaBuilder("dropbox-delete").
	WithName("Dropbox Delete").
	WithCategory("storage").
	WithIcon(IconDropbox).
	WithDescription("Delete files or folders from Dropbox").
	AddSection("Authentication").
		AddExpressionField("accessToken", "Access Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("sl.ABC123..."),
			resolver.WithHint("Dropbox access token (supports {{bindings.xxx}})"),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Delete").
		AddTextField("path", "Path to Delete",
			resolver.WithRequired(),
			resolver.WithPlaceholder("/folder/file.txt"),
			resolver.WithHint("Full path to file or folder"),
		).
		EndSection().
	Build()

// DropboxShareSchema is the UI schema for dropbox-share
var DropboxShareSchema = resolver.NewSchemaBuilder("dropbox-share").
	WithName("Dropbox Share").
	WithCategory("storage").
	WithIcon(IconDropbox).
	WithDescription("Create shared links for Dropbox files or folders").
	AddSection("Authentication").
		AddExpressionField("accessToken", "Access Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("sl.ABC123..."),
			resolver.WithHint("Dropbox access token (supports {{bindings.xxx}})"),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Share Options").
		AddTextField("path", "Path to Share",
			resolver.WithRequired(),
			resolver.WithPlaceholder("/folder/file.txt"),
			resolver.WithHint("Full path to file or folder"),
		).
		AddSelectField("visibility", "Link Visibility", []resolver.SelectOption{
			{Label: "Public (anyone with link)", Value: "public"},
			{Label: "Team (people in your team)", Value: "team_only"},
			{Label: "No one (password required)", Value: "no_one"},
		}, resolver.WithDefault("public")).
		AddTextField("password", "Link Password (Optional)",
			resolver.WithDefault(""),
			resolver.WithHint("Password protect the shared link"),
		).
		AddTextField("expires", "Expiration (Optional)",
			resolver.WithDefault(""),
			resolver.WithPlaceholder("2026-12-31T23:59:59Z"),
			resolver.WithHint("ISO 8601 format"),
		).
		EndSection().
	Build()

// DropboxSearchSchema is the UI schema for dropbox-search
var DropboxSearchSchema = resolver.NewSchemaBuilder("dropbox-search").
	WithName("Dropbox Search").
	WithCategory("storage").
	WithIcon(IconDropbox).
	WithDescription("Search for files in Dropbox").
	AddSection("Authentication").
		AddExpressionField("accessToken", "Access Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("sl.ABC123..."),
			resolver.WithHint("Dropbox access token (supports {{bindings.xxx}})"),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Search Options").
		AddTextField("query", "Search Query",
			resolver.WithRequired(),
			resolver.WithPlaceholder("filename.txt"),
			resolver.WithHint("Search term or filename"),
		).
		AddTextField("path", "Restrict to Path (Optional)",
			resolver.WithDefault(""),
			resolver.WithPlaceholder("/folder"),
			resolver.WithHint("Limit search to this folder"),
		).
		AddNumberField("maxResults", "Max Results",
			resolver.WithDefault(20),
			resolver.WithMinMax(1, 1000),
		).
		AddSelectField("orderBy", "Order By", []resolver.SelectOption{
			{Label: "Relevance", Value: "relevance"},
			{Label: "Last Modified", Value: "modified"},
			{Label: "Size", Value: "size"},
		}, resolver.WithDefault("relevance")).
		AddSelectField("filenameOnly", "Search Filenames Only", []resolver.SelectOption{
			{Label: "Search content and filenames", Value: "false"},
			{Label: "Search filenames only", Value: "true"},
		}, resolver.WithDefault("false")).
		EndSection().
	Build()

// ============================================================================
// DROPBOX-UPLOAD EXECUTOR
// ============================================================================

// DropboxUploadExecutor handles dropbox-upload node type
type DropboxUploadExecutor struct{}

// DropboxUploadConfig defines the typed configuration for dropbox-upload
type DropboxUploadConfig struct {
	AccessToken string `json:"accessToken" description:"Dropbox access token"`
	Path        string `json:"path" description:"Destination path in Dropbox"`
	Content     string `json:"content" description:"File content to upload"`
	Mode        string `json:"mode" default:"add" description:"Upload mode: add, update, add_no_overwrite"`
	Autorename  bool   `json:"autorename" default:"false" description:"Auto-rename on conflict"`
	Mute        bool   `json:"mute" default:"false" description:"Don't send notifications"`
}

func (e *DropboxUploadExecutor) Type() string { return "dropbox-upload" }

func (e *DropboxUploadExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	// Parse config
	var cfg DropboxUploadConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	} else {
		return nil, fmt.Errorf("invalid resolver type")
	}

	if cfg.AccessToken == "" {
		return nil, fmt.Errorf("accessToken is required")
	}
	if cfg.Path == "" {
		return nil, fmt.Errorf("path is required")
	}
	if cfg.Content == "" {
		return nil, fmt.Errorf("content is required")
	}

	// Create Dropbox client
	client := getDropboxClient(cfg.AccessToken)

	// Build upload arg
	uploadArg := map[string]interface{}{
		"path":       cfg.Path,
		"mode":       map[string]string{".tag": cfg.Mode},
		"autorename": cfg.Autorename,
		"mute":       cfg.Mute,
	}

	// Upload file
	content := []byte(cfg.Content)
	resp, err := client.doUploadRequest(ctx, "/files/upload", uploadArg, content)
	if err != nil {
		return nil, fmt.Errorf("upload request failed: %w", err)
	}

	body, err := checkResponse(resp)
	if err != nil {
		return nil, fmt.Errorf("upload failed: %w", err)
	}

	// Parse response
	var fileMeta DropboxFileMetadata
	if err := json.Unmarshal(body, &fileMeta); err != nil {
		return nil, fmt.Errorf("failed to parse upload response: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":  true,
			"path":     fileMeta.PathDisplay,
			"id":       fileMeta.ID,
			"name":     fileMeta.Name,
			"size":     fileMeta.Size,
			"rev":      fileMeta.Rev,
			"modified": fileMeta.ClientModified,
			"message":  fmt.Sprintf("Successfully uploaded to %s", fileMeta.PathDisplay),
		},
	}, nil
}

// ============================================================================
// DROPBOX-DOWNLOAD EXECUTOR
// ============================================================================

// DropboxDownloadExecutor handles dropbox-download node type
type DropboxDownloadExecutor struct{}

// DropboxDownloadConfig defines the typed configuration for dropbox-download
type DropboxDownloadConfig struct {
	AccessToken string `json:"accessToken" description:"Dropbox access token"`
	Path        string `json:"path" description:"Path to file in Dropbox"`
	Base64Encode bool  `json:"base64Encode" default:"false" description:"Encode content as base64"`
}

func (e *DropboxDownloadExecutor) Type() string { return "dropbox-download" }

func (e *DropboxDownloadExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	// Parse config
	var cfg DropboxDownloadConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	} else {
		return nil, fmt.Errorf("invalid resolver type")
	}

	if cfg.AccessToken == "" {
		return nil, fmt.Errorf("accessToken is required")
	}
	if cfg.Path == "" {
		return nil, fmt.Errorf("path is required")
	}

	// Create Dropbox client
	client := getDropboxClient(cfg.AccessToken)

	// Build download arg
	downloadArg := map[string]string{
		"path": cfg.Path,
	}

	// Download file
	resp, err := client.doDownloadRequest(ctx, "/files/download", downloadArg)
	if err != nil {
		return nil, fmt.Errorf("download request failed: %w", err)
	}

	if err := checkResponseNoBody(resp); err != nil {
		return nil, fmt.Errorf("download failed: %w", err)
	}

	// Read metadata from headers
	var fileMeta DropboxFileMetadata
	if metadata := resp.Header.Get("Dropbox-API-Result"); metadata != "" {
		if err := json.Unmarshal([]byte(metadata), &fileMeta); err != nil {
			// Continue without metadata if parsing fails
		}
	}

	// Read content
	content, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("failed to read content: %w", err)
	}

	// Encode as base64 if requested
	var contentOutput interface{} = string(content)
	if cfg.Base64Encode {
		contentOutput = base64.StdEncoding.EncodeToString(content)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":  true,
			"path":     cfg.Path,
			"name":     fileMeta.Name,
			"size":     len(content),
			"content":  contentOutput,
			"modified": fileMeta.ClientModified,
			"message":  fmt.Sprintf("Successfully downloaded %s", cfg.Path),
		},
	}, nil
}

// ============================================================================
// DROPBOX-LIST EXECUTOR
// ============================================================================

// DropboxListExecutor handles dropbox-list node type
type DropboxListExecutor struct{}

// DropboxListConfig defines the typed configuration for dropbox-list
type DropboxListConfig struct {
	AccessToken string `json:"accessToken" description:"Dropbox access token"`
	Path        string `json:"path" default:"" description:"Folder path to list"`
	Recursive   bool   `json:"recursive" default:"false" description:"List recursively"`
	Limit       int    `json:"limit" default:"100" description:"Maximum entries"`
	IncludeDeleted bool `json:"includeDeleted" default:"false" description:"Include deleted items"`
	IncludeHasExplicitSharedAncestor bool `json:"includeHasExplicitSharedAncestor" default:"false" description:"Include shared ancestor info"`
}

func (e *DropboxListExecutor) Type() string { return "dropbox-list" }

func (e *DropboxListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	// Parse config
	var cfg DropboxListConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	} else {
		return nil, fmt.Errorf("invalid resolver type")
	}

	if cfg.AccessToken == "" {
		return nil, fmt.Errorf("accessToken is required")
	}

	// Create Dropbox client
	client := getDropboxClient(cfg.AccessToken)

	// Build list arg
	path := cfg.Path
	if path == "" {
		path = ""
	}

	listArg := map[string]interface{}{
		"path":                             path,
		"recursive":                        cfg.Recursive,
		"include_deleted":                  cfg.IncludeDeleted,
		"include_has_explicit_shared_ancestor": cfg.IncludeHasExplicitSharedAncestor,
		"limit":                            cfg.Limit,
		"include_mounted_folders":          false,
		"include_non_downloadable_files":   false,
	}

	// List folder
	resp, err := client.doRPCRequest(ctx, "/files/list_folder", listArg)
	if err != nil {
		return nil, fmt.Errorf("list request failed: %w", err)
	}

	body, err := checkResponse(resp)
	if err != nil {
		return nil, fmt.Errorf("list failed: %w", err)
	}

	// Parse response
	var result DropboxListFolderResult
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse list response: %w", err)
	}

	// Build entries list
	var entries []map[string]interface{}
	for _, entry := range result.Entries {
		entryMap := make(map[string]interface{})

		if name, ok := entry["name"]; ok {
			entryMap["name"] = name
		}
		if id, ok := entry["id"]; ok {
			entryMap["id"] = id
		}
		if pathDisplay, ok := entry["path_display"]; ok {
			entryMap["path"] = pathDisplay
		}
		if pathLower, ok := entry["path_lower"]; ok {
			entryMap["path_lower"] = pathLower
		}

		// Determine type
		if tag, ok := entry[".tag"]; ok {
			switch tag {
			case "file":
				entryMap["type"] = "file"
				if size, ok := entry["size"]; ok {
					entryMap["size"] = size
				}
				if clientModified, ok := entry["client_modified"]; ok {
					entryMap["modified"] = clientModified
				}
				if serverModified, ok := entry["server_modified"]; ok {
					entryMap["server_modified"] = serverModified
				}
				if rev, ok := entry["rev"]; ok {
					entryMap["rev"] = rev
				}
				if contentHash, ok := entry["content_hash"]; ok {
					entryMap["content_hash"] = contentHash
				}
			case "folder":
				entryMap["type"] = "folder"
			case "deleted":
				entryMap["type"] = "deleted"
			}
		}

		// Sharing info
		if sharingInfo, ok := entry["sharing_info"]; ok {
			entryMap["sharing_info"] = sharingInfo
		}

		entries = append(entries, entryMap)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":   true,
			"path":      path,
			"entries":   entries,
			"count":     len(entries),
			"hasMore":   result.HasMore,
			"cursor":    result.Cursor,
			"recursive": cfg.Recursive,
			"message":   fmt.Sprintf("Listed %d items in %s", len(entries), path),
		},
	}, nil
}

// ============================================================================
// DROPBOX-DELETE EXECUTOR
// ============================================================================

// DropboxDeleteExecutor handles dropbox-delete node type
type DropboxDeleteExecutor struct{}

// DropboxDeleteConfig defines the typed configuration for dropbox-delete
type DropboxDeleteConfig struct {
	AccessToken string `json:"accessToken" description:"Dropbox access token"`
	Path        string `json:"path" description:"Path to file or folder to delete"`
}

func (e *DropboxDeleteExecutor) Type() string { return "dropbox-delete" }

func (e *DropboxDeleteExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	// Parse config
	var cfg DropboxDeleteConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	} else {
		return nil, fmt.Errorf("invalid resolver type")
	}

	if cfg.AccessToken == "" {
		return nil, fmt.Errorf("accessToken is required")
	}
	if cfg.Path == "" {
		return nil, fmt.Errorf("path is required")
	}

	// Create Dropbox client
	client := getDropboxClient(cfg.AccessToken)

	// Build delete arg
	deleteArg := map[string]string{
		"path": cfg.Path,
	}

	// Delete file/folder
	resp, err := client.doRPCRequest(ctx, "/files/delete_v2", deleteArg)
	if err != nil {
		return nil, fmt.Errorf("delete request failed: %w", err)
	}

	body, err := checkResponse(resp)
	if err != nil {
		return nil, fmt.Errorf("delete failed: %w", err)
	}

	// Parse response
	var result DropboxDeleteResult
	if err := json.Unmarshal(body, &result); err != nil {
		// Continue even if parsing fails
	}

	// Extract metadata info
	var deletedName string
	var deletedPath string
	if result.Metadata != nil {
		if name, ok := result.Metadata["name"]; ok {
			deletedName = fmt.Sprintf("%v", name)
		}
		if pathDisplay, ok := result.Metadata["path_display"]; ok {
			deletedPath = fmt.Sprintf("%v", pathDisplay)
		}
	}
	if deletedName == "" {
		deletedName = cfg.Path
	}
	if deletedPath == "" {
		deletedPath = cfg.Path
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success": true,
			"path":    cfg.Path,
			"name":    deletedName,
			"deleted_path": deletedPath,
			"message": fmt.Sprintf("Successfully deleted %s", deletedPath),
		},
	}, nil
}

// ============================================================================
// DROPBOX-SHARE EXECUTOR
// ============================================================================

// DropboxShareExecutor handles dropbox-share node type
type DropboxShareExecutor struct{}

// DropboxShareConfig defines the typed configuration for dropbox-share
type DropboxShareConfig struct {
	AccessToken string `json:"accessToken" description:"Dropbox access token"`
	Path        string `json:"path" description:"Path to file or folder to share"`
	Visibility  string `json:"visibility" default:"public" description:"Link visibility: public, team_only, no_one"`
	Password    string `json:"password" default:"" description:"Optional password for the link"`
	Expires     string `json:"expires" default:"" description:"Optional expiration time (ISO 8601)"`
}

func (e *DropboxShareExecutor) Type() string { return "dropbox-share" }

func (e *DropboxShareExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	// Parse config
	var cfg DropboxShareConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	} else {
		return nil, fmt.Errorf("invalid resolver type")
	}

	if cfg.AccessToken == "" {
		return nil, fmt.Errorf("accessToken is required")
	}
	if cfg.Path == "" {
		return nil, fmt.Errorf("path is required")
	}

	// Create Dropbox client
	client := getDropboxClient(cfg.AccessToken)

	// Build shared link settings
	settings := map[string]interface{}{
		"requested_visibility": map[string]string{
			".tag": cfg.Visibility,
		},
	}

	// Build create shared link arg
	createArg := map[string]interface{}{
		"path":     cfg.Path,
		"settings": settings,
	}

	// Create shared link
	resp, err := client.doRPCRequest(ctx, "/sharing/create_shared_link_with_settings", createArg)
	if err != nil {
		return nil, fmt.Errorf("create shared link request failed: %w", err)
	}

	body, err := checkResponse(resp)
	if err != nil {
		// If link already exists, get existing links
		if strings.Contains(err.Error(), "shared_link_already_exists") || strings.Contains(err.Error(), "path_looks_like_folder") {
			// Try to get existing shared links
			listArg := map[string]interface{}{
				"path": cfg.Path,
			}
			listResp, listErr := client.doRPCRequest(ctx, "/sharing/list_shared_links", listArg)
			if listErr == nil {
				listBody, listErr2 := checkResponse(listResp)
				if listErr2 == nil {
					var listResult map[string]interface{}
					if json.Unmarshal(listBody, &listResult) == nil {
						if links, ok := listResult["links"].([]interface{}); ok && len(links) > 0 {
							if firstLink, ok := links[0].(map[string]interface{}); ok {
								if url, ok := firstLink["url"].(string); ok {
									return &executor.StepResult{
										Output: map[string]interface{}{
											"success": true,
											"path":    cfg.Path,
											"url":     url,
											"existing": true,
											"message": fmt.Sprintf("Retrieved existing shared link for %s", cfg.Path),
										},
									}, nil
								}
							}
						}
					}
				}
			}
		}
		return nil, fmt.Errorf("create shared link failed: %w", err)
	}

	// Parse response
	var link DropboxSharedLink
	if err := json.Unmarshal(body, &link); err != nil {
		return nil, fmt.Errorf("failed to parse shared link response: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success": true,
			"path":    cfg.Path,
			"url":     link.URL,
			"id":      link.ID,
			"name":    link.Name,
			"expires": link.Expires,
			"message": fmt.Sprintf("Successfully created shared link for %s", cfg.Path),
		},
	}, nil
}

// ============================================================================
// DROPBOX-SEARCH EXECUTOR
// ============================================================================

// DropboxSearchExecutor handles dropbox-search node type
type DropboxSearchExecutor struct{}

// DropboxSearchConfig defines the typed configuration for dropbox-search
type DropboxSearchConfig struct {
	AccessToken  string `json:"accessToken" description:"Dropbox access token"`
	Query        string `json:"query" description:"Search query string"`
	Path         string `json:"path" default:"" description:"Restrict search to this path"`
	MaxResults   int    `json:"maxResults" default:"20" description:"Maximum results"`
	OrderBy      string `json:"orderBy" default:"relevance" description:"Sort order: relevance, modified, size"`
	FilenameOnly bool   `json:"filenameOnly" default:"false" description:"Search filenames only"`
}

func (e *DropboxSearchExecutor) Type() string { return "dropbox-search" }

func (e *DropboxSearchExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	// Parse config
	var cfg DropboxSearchConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	} else {
		return nil, fmt.Errorf("invalid resolver type")
	}

	if cfg.AccessToken == "" {
		return nil, fmt.Errorf("accessToken is required")
	}
	if cfg.Query == "" {
		return nil, fmt.Errorf("query is required")
	}

	// Create Dropbox client
	client := getDropboxClient(cfg.AccessToken)

	// Build search options
	options := map[string]interface{}{
		"filename_only": cfg.FilenameOnly,
	}

	// Build search arg
	searchArg := map[string]interface{}{
		"query":      cfg.Query,
		"max_results": cfg.MaxResults,
		"order_by": map[string]string{
			".tag": cfg.OrderBy,
		},
		"options": []map[string]interface{}{options},
	}

	// Add path restriction if provided
	if cfg.Path != "" {
		searchArg["path"] = cfg.Path
	}

	// Execute search
	resp, err := client.doRPCRequest(ctx, "/files/search_v2", searchArg)
	if err != nil {
		return nil, fmt.Errorf("search request failed: %w", err)
	}

	body, err := checkResponse(resp)
	if err != nil {
		return nil, fmt.Errorf("search failed: %w", err)
	}

	// Parse response
	var result DropboxSearchResult
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse search response: %w", err)
	}

	// Build results
	var results []map[string]interface{}
	for _, match := range result.Matches {
		resultMap := map[string]interface{}{
			"matchScore": match.MatchScore,
			"matchType":  match.MatchType,
			"highlights": match.Highlights,
		}

		// Copy metadata
		if match.Metadata != nil {
			if name, ok := match.Metadata["name"]; ok {
				resultMap["name"] = name
			}
			if id, ok := match.Metadata["id"]; ok {
				resultMap["id"] = id
			}
			if pathDisplay, ok := match.Metadata["path_display"]; ok {
				resultMap["path"] = pathDisplay
			}
			if pathLower, ok := match.Metadata["path_lower"]; ok {
				resultMap["path_lower"] = pathLower
			}

			// Determine type
			if tag, ok := match.Metadata[".tag"]; ok {
				switch tag {
				case "file":
					resultMap["type"] = "file"
					if size, ok := match.Metadata["size"]; ok {
						resultMap["size"] = size
					}
					if clientModified, ok := match.Metadata["client_modified"]; ok {
						resultMap["modified"] = clientModified
					}
				case "folder":
					resultMap["type"] = "folder"
				}
			}
		}

		results = append(results, resultMap)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":    true,
			"query":      cfg.Query,
			"results":    results,
			"count":      len(results),
			"hasMore":    result.HasMore,
			"orderBy":    cfg.OrderBy,
			"path":       cfg.Path,
			"message":    fmt.Sprintf("Found %d results for '%s'", len(results), cfg.Query),
		},
	}, nil
}
