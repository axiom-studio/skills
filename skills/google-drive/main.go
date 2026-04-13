package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/axiom-studio/skills.sdk/executor"
	"github.com/axiom-studio/skills.sdk/grpc"
	"github.com/axiom-studio/skills.sdk/resolver"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
)

const (
	iconGoogleDrive = "hard-drive"
)

// Google Drive clients cache
var (
	driveClients   = make(map[string]*drive.Service)
	driveClientMux sync.RWMutex
)

func main() {
	// Get port from env or use default
	port := os.Getenv("SKILL_PORT")
	if port == "" {
		port = "50113"
	}

	// Create skill server
	server := grpc.NewSkillServer("skill-google-drive", "1.0.0")

	// Register Google Drive executors with schemas
	server.RegisterExecutorWithSchema("gdrive-list", &GDriveListExecutor{}, GDriveListSchema)
	server.RegisterExecutorWithSchema("gdrive-upload", &GDriveUploadExecutor{}, GDriveUploadSchema)
	server.RegisterExecutorWithSchema("gdrive-download", &GDriveDownloadExecutor{}, GDriveDownloadSchema)
	server.RegisterExecutorWithSchema("gdrive-delete", &GDriveDeleteExecutor{}, GDriveDeleteSchema)
	server.RegisterExecutorWithSchema("gdrive-share", &GDriveShareExecutor{}, GDriveShareSchema)
	server.RegisterExecutorWithSchema("gdrive-search", &GDriveSearchExecutor{}, GDriveSearchSchema)
	server.RegisterExecutorWithSchema("gdrive-folder-create", &GDriveFolderCreateExecutor{}, GDriveFolderCreateSchema)
	server.RegisterExecutorWithSchema("gdrive-permission-list", &GDrivePermissionListExecutor{}, GDrivePermissionListSchema)

	fmt.Printf("Starting skill-google-drive gRPC server on port %s\n", port)
	if err := server.Serve(port); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to serve: %v\n", err)
		os.Exit(1)
	}
}

// ============================================================================
// GOOGLE DRIVE CLIENT HELPERS
// ============================================================================

// GDriveConfig holds Google Drive connection configuration
type GDriveConfig struct {
	// OAuth2 authentication
	AccessToken  string
	RefreshToken string
	TokenJSON    string

	// Service account authentication
	ServiceAccountKey string

	// Optional: specific scopes
	Scopes []string
}

// getDriveService returns a Google Drive service (cached)
func getDriveService(cfg GDriveConfig) (*drive.Service, error) {
	// Create cache key based on auth method
	cacheKey := ""
	if cfg.AccessToken != "" {
		cacheKey = "oauth:" + cfg.AccessToken[:min(20, len(cfg.AccessToken))]
	} else if cfg.ServiceAccountKey != "" {
		cacheKey = "sa:" + cfg.ServiceAccountKey[:min(20, len(cfg.ServiceAccountKey))]
	} else if cfg.TokenJSON != "" {
		cacheKey = "json:" + cfg.TokenJSON[:min(20, len(cfg.TokenJSON))]
	}

	driveClientMux.RLock()
	client, ok := driveClients[cacheKey]
	driveClientMux.RUnlock()

	if ok {
		return client, nil
	}

	driveClientMux.Lock()
	defer driveClientMux.Unlock()

	// Double check
	if client, ok := driveClients[cacheKey]; ok {
		return client, nil
	}

	// Default scopes
	scopes := cfg.Scopes
	if len(scopes) == 0 {
		scopes = []string{drive.DriveScope}
	}

	var err error
	var svc *drive.Service

	// Try service account first
	if cfg.ServiceAccountKey != "" {
		svc, err = createServiceAccountService(cfg.ServiceAccountKey, scopes)
	} else if cfg.TokenJSON != "" {
		svc, err = createServiceFromTokenJSON(cfg.TokenJSON, scopes)
	} else if cfg.AccessToken != "" {
		svc, err = createServiceFromAccessToken(cfg.AccessToken, cfg.RefreshToken, scopes)
	} else {
		// Try ADC (Application Default Credentials)
		svc, err = createServiceFromADC(scopes)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to create Drive service: %w", err)
	}

	driveClients[cacheKey] = svc
	return svc, nil
}

// createServiceAccountService creates a Drive service from service account JSON key
func createServiceAccountService(serviceAccountKey string, scopes []string) (*drive.Service, error) {
	creds, err := google.CredentialsFromJSON(
		context.Background(),
		[]byte(serviceAccountKey),
		scopes...,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to parse service account key: %w", err)
	}

	svc, err := drive.NewService(context.Background(), option.WithCredentials(creds))
	if err != nil {
		return nil, fmt.Errorf("failed to create Drive service: %w", err)
	}

	return svc, nil
}

// createServiceFromTokenJSON creates a Drive service from OAuth2 token JSON
func createServiceFromTokenJSON(tokenJSON string, scopes []string) (*drive.Service, error) {
	// Try to parse as token directly
	return createServiceFromAccessTokenJSON(tokenJSON, scopes)
}

// createServiceFromAccessTokenJSON creates a Drive service from OAuth2 access token JSON
func createServiceFromAccessTokenJSON(tokenJSON string, scopes []string) (*drive.Service, error) {
	var token oauth2.Token
	if err := json.Unmarshal([]byte(tokenJSON), &token); err != nil {
		return nil, fmt.Errorf("failed to parse token JSON: %w", err)
	}

	// Create a minimal config for token-based auth
	conf := &oauth2.Config{
		Scopes: scopes,
	}

	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{})
	tokenSource := conf.TokenSource(ctx, &token)

	svc, err := drive.NewService(ctx, option.WithTokenSource(tokenSource))
	if err != nil {
		return nil, fmt.Errorf("failed to create Drive service: %w", err)
	}

	return svc, nil
}

// createServiceFromAccessToken creates a Drive service from access/refresh tokens
func createServiceFromAccessToken(accessToken, refreshToken string, scopes []string) (*drive.Service, error) {
	token := &oauth2.Token{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		TokenType:    "Bearer",
	}

	conf := &oauth2.Config{
		Scopes: scopes,
	}

	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{})
	tokenSource := conf.TokenSource(ctx, token)

	svc, err := drive.NewService(ctx, option.WithTokenSource(tokenSource))
	if err != nil {
		return nil, fmt.Errorf("failed to create Drive service: %w", err)
	}

	return svc, nil
}

// createServiceFromADC creates a Drive service using Application Default Credentials
func createServiceFromADC(scopes []string) (*drive.Service, error) {
	ctx := context.Background()
	creds, err := google.FindDefaultCredentials(ctx, scopes...)
	if err != nil {
		return nil, fmt.Errorf("failed to find default credentials: %w", err)
	}

	svc, err := drive.NewService(ctx, option.WithCredentials(creds))
	if err != nil {
		return nil, fmt.Errorf("failed to create Drive service: %w", err)
	}

	return svc, nil
}

// min returns the minimum of two integers
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
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

// parseGDriveConfig extracts Google Drive configuration from config map
func parseGDriveConfig(config map[string]interface{}) GDriveConfig {
	return GDriveConfig{
		AccessToken:       getString(config, "accessToken"),
		RefreshToken:      getString(config, "refreshToken"),
		TokenJSON:         getString(config, "tokenJson"),
		ServiceAccountKey: getString(config, "serviceAccountKey"),
		Scopes:            getStringSlice(config, "scopes"),
	}
}

// ============================================================================
// SCHEMAS
// ============================================================================

// GDriveListSchema is the UI schema for gdrive-list
var GDriveListSchema = resolver.NewSchemaBuilder("gdrive-list").
	WithName("Google Drive List").
	WithCategory("storage").
	WithIcon(iconGoogleDrive).
	WithDescription("List files and folders in Google Drive").
	AddSection("Authentication").
		AddExpressionField("accessToken", "Access Token",
			resolver.WithPlaceholder("ya29.a0A..."),
			resolver.WithHint("OAuth2 access token (optional if using service account)"),
			resolver.WithSensitive(),
		).
		AddExpressionField("serviceAccountKey", "Service Account Key",
			resolver.WithPlaceholder(`{"type":"service_account",...}`),
			resolver.WithHint("Service account JSON key (optional if using OAuth)"),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("List Options").
		AddTextField("folderId", "Folder ID",
			resolver.WithDefault("root"),
			resolver.WithPlaceholder("root or folder ID"),
			resolver.WithHint("Folder to list (use 'root' for My Drive)"),
		).
		AddTextField("query", "Search Query",
			resolver.WithPlaceholder("mimeType='application/pdf'"),
			resolver.WithHint("Optional Drive query to filter results"),
		).
		AddToggleField("recursive", "Recursive",
			resolver.WithDefault(false),
			resolver.WithHint("Include files in subfolders"),
		).
		AddNumberField("limit", "Max Results",
			resolver.WithDefault(100),
			resolver.WithMinMax(1, 1000),
			resolver.WithHint("Maximum number of files to return"),
		).
		EndSection().
	Build()

// GDriveUploadSchema is the UI schema for gdrive-upload
var GDriveUploadSchema = resolver.NewSchemaBuilder("gdrive-upload").
	WithName("Google Drive Upload").
	WithCategory("storage").
	WithIcon(iconGoogleDrive).
	WithDescription("Upload files to Google Drive").
	AddSection("Authentication").
		AddExpressionField("accessToken", "Access Token",
			resolver.WithPlaceholder("ya29.a0A..."),
			resolver.WithHint("OAuth2 access token"),
			resolver.WithSensitive(),
		).
		AddExpressionField("serviceAccountKey", "Service Account Key",
			resolver.WithPlaceholder(`{"type":"service_account",...}`),
			resolver.WithHint("Service account JSON key"),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Upload Options").
		AddTextField("folderId", "Destination Folder",
			resolver.WithDefault("root"),
			resolver.WithPlaceholder("root or folder ID"),
			resolver.WithHint("Folder to upload to"),
		).
		AddTextField("filename", "Filename",
			resolver.WithRequired(),
			resolver.WithPlaceholder("document.pdf"),
			resolver.WithHint("Name for the uploaded file"),
		).
		AddTextareaField("content", "File Content",
			resolver.WithRequired(),
			resolver.WithRows(10),
			resolver.WithHint("File content (use base64 for binary files)"),
		).
		AddToggleField("base64Decode", "Base64 Decode",
			resolver.WithDefault(false),
			resolver.WithHint("Decode content from base64 before uploading"),
		).
		AddTextField("mimeType", "MIME Type",
			resolver.WithDefault("application/octet-stream"),
			resolver.WithPlaceholder("text/plain, application/pdf, etc."),
			resolver.WithHint("MIME type of the file"),
		).
		AddToggleField("overwrite", "Overwrite Existing",
			resolver.WithDefault(false),
			resolver.WithHint("Replace file if name already exists"),
		).
		EndSection().
	Build()

// GDriveDownloadSchema is the UI schema for gdrive-download
var GDriveDownloadSchema = resolver.NewSchemaBuilder("gdrive-download").
	WithName("Google Drive Download").
	WithCategory("storage").
	WithIcon(iconGoogleDrive).
	WithDescription("Download files from Google Drive").
	AddSection("Authentication").
		AddExpressionField("accessToken", "Access Token",
			resolver.WithPlaceholder("ya29.a0A..."),
			resolver.WithHint("OAuth2 access token"),
			resolver.WithSensitive(),
		).
		AddExpressionField("serviceAccountKey", "Service Account Key",
			resolver.WithPlaceholder(`{"type":"service_account",...}`),
			resolver.WithHint("Service account JSON key"),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Download Options").
		AddTextField("fileId", "File ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("1a2b3c4d5e6f..."),
			resolver.WithHint("Google Drive file ID"),
		).
		AddToggleField("base64Encode", "Base64 Encode",
			resolver.WithDefault(false),
			resolver.WithHint("Encode binary content as base64"),
		).
		EndSection().
	Build()

// GDriveDeleteSchema is the UI schema for gdrive-delete
var GDriveDeleteSchema = resolver.NewSchemaBuilder("gdrive-delete").
	WithName("Google Drive Delete").
	WithCategory("storage").
	WithIcon(iconGoogleDrive).
	WithDescription("Delete files or folders from Google Drive").
	AddSection("Authentication").
		AddExpressionField("accessToken", "Access Token",
			resolver.WithPlaceholder("ya29.a0A..."),
			resolver.WithHint("OAuth2 access token"),
			resolver.WithSensitive(),
		).
		AddExpressionField("serviceAccountKey", "Service Account Key",
			resolver.WithPlaceholder(`{"type":"service_account",...}`),
			resolver.WithHint("Service account JSON key"),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Delete Options").
		AddTextField("fileId", "File ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("1a2b3c4d5e6f..."),
			resolver.WithHint("Google Drive file or folder ID to delete"),
		).
		AddToggleField("permanent", "Permanent Delete",
			resolver.WithDefault(false),
			resolver.WithHint("Permanently delete instead of moving to trash"),
		).
		EndSection().
	Build()

// GDriveShareSchema is the UI schema for gdrive-share
var GDriveShareSchema = resolver.NewSchemaBuilder("gdrive-share").
	WithName("Google Drive Share").
	WithCategory("storage").
	WithIcon(iconGoogleDrive).
	WithDescription("Share files or folders on Google Drive").
	AddSection("Authentication").
		AddExpressionField("accessToken", "Access Token",
			resolver.WithPlaceholder("ya29.a0A..."),
			resolver.WithHint("OAuth2 access token"),
			resolver.WithSensitive(),
		).
		AddExpressionField("serviceAccountKey", "Service Account Key",
			resolver.WithPlaceholder(`{"type":"service_account",...}`),
			resolver.WithHint("Service account JSON key"),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Share Options").
		AddTextField("fileId", "File ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("1a2b3c4d5e6f..."),
			resolver.WithHint("Google Drive file or folder ID to share"),
		).
		AddTextField("emailAddress", "Email Address",
			resolver.WithPlaceholder("user@example.com"),
			resolver.WithHint("Email address to share with (leave empty for anyone link)"),
		).
		AddSelectField("role", "Permission Role",
			[]resolver.SelectOption{
				{Label: "Reader (view only)", Value: "reader"},
				{Label: "Commenter (view and comment)", Value: "commenter"},
				{Label: "Writer (edit)", Value: "writer"},
				{Label: "Organizer (full control)", Value: "organizer"},
			},
			resolver.WithDefault("reader"),
			resolver.WithHint("Permission level to grant"),
		).
		AddSelectField("type", "Permission Type",
			[]resolver.SelectOption{
				{Label: "User/Group", Value: "user"},
				{Label: "Anyone with link", Value: "anyone"},
				{Label: "Domain users", Value: "domain"},
			},
			resolver.WithDefault("user"),
			resolver.WithHint("Who can access the file"),
		).
		AddTextField("domain", "Domain",
			resolver.WithPlaceholder("example.com"),
			resolver.WithHint("Domain name (required for domain type)"),
		).
		AddToggleField("sendEmail", "Send Email Notification",
			resolver.WithDefault(true),
			resolver.WithHint("Send email notification to recipient"),
		).
		AddTextareaField("emailMessage", "Email Message",
			resolver.WithRows(3),
			resolver.WithPlaceholder("Here's the file you requested..."),
			resolver.WithHint("Custom message for the email notification"),
		).
		EndSection().
	Build()

// GDriveSearchSchema is the UI schema for gdrive-search
var GDriveSearchSchema = resolver.NewSchemaBuilder("gdrive-search").
	WithName("Google Drive Search").
	WithCategory("storage").
	WithIcon(iconGoogleDrive).
	WithDescription("Search for files in Google Drive").
	AddSection("Authentication").
		AddExpressionField("accessToken", "Access Token",
			resolver.WithPlaceholder("ya29.a0A..."),
			resolver.WithHint("OAuth2 access token"),
			resolver.WithSensitive(),
		).
		AddExpressionField("serviceAccountKey", "Service Account Key",
			resolver.WithPlaceholder(`{"type":"service_account",...}`),
			resolver.WithHint("Service account JSON key"),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Search Options").
		AddTextField("query", "Search Query",
			resolver.WithRequired(),
			resolver.WithPlaceholder("name contains 'report'"),
			resolver.WithHint("Drive search query (full text or query syntax)"),
		).
		AddTextField("mimeType", "MIME Type Filter",
			resolver.WithPlaceholder("application/pdf"),
			resolver.WithHint("Filter by MIME type (e.g., application/pdf, application/vnd.google-apps.folder)"),
		).
		AddTextField("folderId", "Search in Folder",
			resolver.WithDefault("root"),
			resolver.WithPlaceholder("root or folder ID"),
			resolver.WithHint("Limit search to specific folder"),
		).
		AddToggleField("includeTrash", "Include Trashed",
			resolver.WithDefault(false),
			resolver.WithHint("Include files in trash"),
		).
		AddNumberField("limit", "Max Results",
			resolver.WithDefault(50),
			resolver.WithMinMax(1, 1000),
			resolver.WithHint("Maximum number of results"),
		).
		EndSection().
	Build()

// GDriveFolderCreateSchema is the UI schema for gdrive-folder-create
var GDriveFolderCreateSchema = resolver.NewSchemaBuilder("gdrive-folder-create").
	WithName("Google Drive Create Folder").
	WithCategory("storage").
	WithIcon(iconGoogleDrive).
	WithDescription("Create a new folder in Google Drive").
	AddSection("Authentication").
		AddExpressionField("accessToken", "Access Token",
			resolver.WithPlaceholder("ya29.a0A..."),
			resolver.WithHint("OAuth2 access token"),
			resolver.WithSensitive(),
		).
		AddExpressionField("serviceAccountKey", "Service Account Key",
			resolver.WithPlaceholder(`{"type":"service_account",...}`),
			resolver.WithHint("Service account JSON key"),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Folder Options").
		AddTextField("name", "Folder Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("New Folder"),
			resolver.WithHint("Name for the new folder"),
		).
		AddTextField("parentFolderId", "Parent Folder",
			resolver.WithDefault("root"),
			resolver.WithPlaceholder("root or folder ID"),
			resolver.WithHint("Parent folder (use 'root' for My Drive)"),
		).
		EndSection().
	Build()

// GDrivePermissionListSchema is the UI schema for gdrive-permission-list
var GDrivePermissionListSchema = resolver.NewSchemaBuilder("gdrive-permission-list").
	WithName("Google Drive List Permissions").
	WithCategory("storage").
	WithIcon(iconGoogleDrive).
	WithDescription("List permissions for a Google Drive file or folder").
	AddSection("Authentication").
		AddExpressionField("accessToken", "Access Token",
			resolver.WithPlaceholder("ya29.a0A..."),
			resolver.WithHint("OAuth2 access token"),
			resolver.WithSensitive(),
		).
		AddExpressionField("serviceAccountKey", "Service Account Key",
			resolver.WithPlaceholder(`{"type":"service_account",...}`),
			resolver.WithHint("Service account JSON key"),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Permission Options").
		AddTextField("fileId", "File ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("1a2b3c4d5e6f..."),
			resolver.WithHint("Google Drive file or folder ID"),
		).
		EndSection().
	Build()

// ============================================================================
// GDRIVE-LIST EXECUTOR
// ============================================================================

// GDriveListExecutor handles gdrive-list node type
type GDriveListExecutor struct{}

// GDriveListConfig defines the typed configuration for gdrive-list
type GDriveListConfig struct {
	AccessToken       string `json:"accessToken" description:"OAuth2 access token"`
	ServiceAccountKey string `json:"serviceAccountKey" description:"Service account JSON key"`
	FolderId          string `json:"folderId" default:"root" description:"Folder ID to list"`
	Query             string `json:"query" description:"Optional Drive query"`
	Recursive         bool   `json:"recursive" default:"false" description:"List recursively"`
	Limit             int    `json:"limit" default:"100" description:"Maximum results"`
}

func (e *GDriveListExecutor) Type() string { return "gdrive-list" }

func (e *GDriveListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	// Parse config
	var cfg GDriveListConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	} else {
		return nil, fmt.Errorf("invalid resolver type")
	}

	gdriveCfg := parseGDriveConfig(step.Config)
	svc, err := getDriveService(gdriveCfg)
	if err != nil {
		return nil, err
	}

	folderId := cfg.FolderId
	if folderId == "" {
		folderId = "root"
	}

	limit := cfg.Limit
	if limit <= 0 {
		limit = 100
	}

	// Build query
	q := fmt.Sprintf("'%s' in parents and trashed=false", folderId)
	if cfg.Query != "" {
		q = fmt.Sprintf("(%s) and %s", cfg.Query, q)
	}
	if !cfg.Recursive {
		q = fmt.Sprintf("'%s' in parents and trashed=false", folderId)
	}

	// List files
	var files []*drive.File
	err = svc.Files.List().
		Q(q).
		Fields("files(id, name, mimeType, size, createdTime, modifiedTime, owners, parents)").
		PageSize(int64(limit)).
		Pages(ctx, func(fl *drive.FileList) error {
			files = append(files, fl.Files...)
			return nil
		})

	if err != nil {
		return nil, fmt.Errorf("failed to list files: %w", err)
	}

	// Build output
	entries := make([]map[string]interface{}, 0, len(files))
	for _, f := range files {
		entry := map[string]interface{}{
			"id":         f.Id,
			"name":       f.Name,
			"mimeType":   f.MimeType,
			"size":       f.Size,
			"created":    f.CreatedTime,
			"modified":   f.ModifiedTime,
			"isFolder":   f.MimeType == "application/vnd.google-apps.folder",
			"parents":    f.Parents,
		}
		if len(f.Owners) > 0 {
			entry["owner"] = f.Owners[0].DisplayName
			entry["ownerEmail"] = f.Owners[0].EmailAddress
		}
		entries = append(entries, entry)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success": true,
			"folder":  folderId,
			"files":   entries,
			"count":   len(entries),
			"message": fmt.Sprintf("Listed %d items", len(entries)),
		},
	}, nil
}

// ============================================================================
// GDRIVE-UPLOAD EXECUTOR
// ============================================================================

// GDriveUploadExecutor handles gdrive-upload node type
type GDriveUploadExecutor struct{}

// GDriveUploadConfig defines the typed configuration for gdrive-upload
type GDriveUploadConfig struct {
	AccessToken       string `json:"accessToken" description:"OAuth2 access token"`
	ServiceAccountKey string `json:"serviceAccountKey" description:"Service account JSON key"`
	FolderId          string `json:"folderId" default:"root" description:"Destination folder ID"`
	Filename          string `json:"filename" description:"Filename for uploaded file"`
	Content           string `json:"content" description:"File content"`
	Base64Decode      bool   `json:"base64Decode" default:"false" description:"Decode base64 content"`
	MimeType          string `json:"mimeType" default:"application/octet-stream" description:"MIME type"`
	Overwrite         bool   `json:"overwrite" default:"false" description:"Overwrite existing file"`
}

func (e *GDriveUploadExecutor) Type() string { return "gdrive-upload" }

func (e *GDriveUploadExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	// Parse config
	var cfg GDriveUploadConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	} else {
		return nil, fmt.Errorf("invalid resolver type")
	}

	gdriveCfg := parseGDriveConfig(step.Config)
	svc, err := getDriveService(gdriveCfg)
	if err != nil {
		return nil, err
	}

	if cfg.Filename == "" {
		return nil, fmt.Errorf("filename is required")
	}

	if cfg.Content == "" {
		return nil, fmt.Errorf("content is required")
	}

	// Decode content if base64
	content := []byte(cfg.Content)
	if cfg.Base64Decode {
		decoded, err := base64.StdEncoding.DecodeString(cfg.Content)
		if err != nil {
			return nil, fmt.Errorf("failed to decode base64 content: %w", err)
		}
		content = decoded
	}

	folderId := cfg.FolderId
	if folderId == "" {
		folderId = "root"
	}

	// Check for existing file if overwrite is enabled
	fileId := ""
	if cfg.Overwrite {
		query := fmt.Sprintf("name='%s' and '%s' in parents and trashed=false", cfg.Filename, folderId)
		existing, err := svc.Files.List().Q(query).Fields("files(id)").Do()
		if err != nil {
			return nil, fmt.Errorf("failed to check for existing file: %w", err)
		}
		if len(existing.Files) > 0 {
			fileId = existing.Files[0].Id
		}
	}

	// Create file metadata
	file := &drive.File{
		Name:     cfg.Filename,
		MimeType: cfg.MimeType,
		Parents:  []string{folderId},
	}

	var uploadedFile *drive.File

	if fileId != "" {
		// Update existing file
		uploadedFile, err = svc.Files.Update(fileId, file).
			Media(strings.NewReader(string(content)), googleapi.ContentType(cfg.MimeType)).
			Fields("id, name, mimeType, size, webViewLink, webContentLink").
			Do()
	} else {
		// Create new file
		uploadedFile, err = svc.Files.Create(file).
			Media(strings.NewReader(string(content)), googleapi.ContentType(cfg.MimeType)).
			Fields("id, name, mimeType, size, webViewLink, webContentLink").
			Do()
	}

	if err != nil {
		return nil, fmt.Errorf("failed to upload file: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":        true,
			"fileId":         uploadedFile.Id,
			"fileName":       uploadedFile.Name,
			"mimeType":       uploadedFile.MimeType,
			"size":           uploadedFile.Size,
			"webViewLink":    uploadedFile.WebViewLink,
			"webContentLink": uploadedFile.WebContentLink,
			"folderId":       folderId,
			"overwritten":    fileId != "",
			"message":        fmt.Sprintf("Successfully uploaded %s", uploadedFile.Name),
		},
	}, nil
}

// ============================================================================
// GDRIVE-DOWNLOAD EXECUTOR
// ============================================================================

// GDriveDownloadExecutor handles gdrive-download node type
type GDriveDownloadExecutor struct{}

// GDriveDownloadConfig defines the typed configuration for gdrive-download
type GDriveDownloadConfig struct {
	AccessToken       string `json:"accessToken" description:"OAuth2 access token"`
	ServiceAccountKey string `json:"serviceAccountKey" description:"Service account JSON key"`
	FileId            string `json:"fileId" description:"File ID to download"`
	Base64Encode      bool   `json:"base64Encode" default:"false" description:"Encode content as base64"`
}

func (e *GDriveDownloadExecutor) Type() string { return "gdrive-download" }

func (e *GDriveDownloadExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	// Parse config
	var cfg GDriveDownloadConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	} else {
		return nil, fmt.Errorf("invalid resolver type")
	}

	gdriveCfg := parseGDriveConfig(step.Config)
	svc, err := getDriveService(gdriveCfg)
	if err != nil {
		return nil, err
	}

	if cfg.FileId == "" {
		return nil, fmt.Errorf("fileId is required")
	}

	// Get file metadata first
	file, err := svc.Files.Get(cfg.FileId).
		Fields("id, name, mimeType, size, webViewLink").
		Do()
	if err != nil {
		return nil, fmt.Errorf("failed to get file metadata: %w", err)
	}

	// Download file content
	res, err := svc.Files.Get(cfg.FileId).Download()
	if err != nil {
		return nil, fmt.Errorf("failed to download file: %w", err)
	}
	defer res.Body.Close()

	content, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read file content: %w", err)
	}

	outputContent := string(content)
	if cfg.Base64Encode {
		outputContent = base64.StdEncoding.EncodeToString(content)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":      true,
			"fileId":       file.Id,
			"fileName":     file.Name,
			"mimeType":     file.MimeType,
			"size":         file.Size,
			"content":      outputContent,
			"base64Encoded": cfg.Base64Encode,
			"webViewLink":  file.WebViewLink,
			"message":      fmt.Sprintf("Successfully downloaded %s", file.Name),
		},
	}, nil
}

// ============================================================================
// GDRIVE-DELETE EXECUTOR
// ============================================================================

// GDriveDeleteExecutor handles gdrive-delete node type
type GDriveDeleteExecutor struct{}

// GDriveDeleteConfig defines the typed configuration for gdrive-delete
type GDriveDeleteConfig struct {
	AccessToken       string `json:"accessToken" description:"OAuth2 access token"`
	ServiceAccountKey string `json:"serviceAccountKey" description:"Service account JSON key"`
	FileId            string `json:"fileId" description:"File ID to delete"`
	Permanent         bool   `json:"permanent" default:"false" description:"Permanently delete"`
}

func (e *GDriveDeleteExecutor) Type() string { return "gdrive-delete" }

func (e *GDriveDeleteExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	// Parse config
	var cfg GDriveDeleteConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	} else {
		return nil, fmt.Errorf("invalid resolver type")
	}

	gdriveCfg := parseGDriveConfig(step.Config)
	svc, err := getDriveService(gdriveCfg)
	if err != nil {
		return nil, err
	}

	if cfg.FileId == "" {
		return nil, fmt.Errorf("fileId is required")
	}

	// Get file info before deleting
	file, err := svc.Files.Get(cfg.FileId).Fields("id, name, mimeType").Do()
	if err != nil {
		if gerr, ok := err.(*googleapi.Error); ok && gerr.Code == 404 {
			return nil, fmt.Errorf("file not found: %s", cfg.FileId)
		}
		return nil, fmt.Errorf("failed to get file info: %w", err)
	}

	fileName := file.Name
	isFolder := file.MimeType == "application/vnd.google-apps.folder"

	if cfg.Permanent {
		// Permanently delete
		err = svc.Files.Delete(cfg.FileId).Do()
	} else {
		// Move to trash (update trashed field)
		_, err = svc.Files.Update(cfg.FileId, &drive.File{Trashed: true}).Do()
	}

	if err != nil {
		return nil, fmt.Errorf("failed to delete file: %w", err)
	}

	deleteType := "trashed"
	if cfg.Permanent {
		deleteType = "permanently deleted"
	}

	itemType := "file"
	if isFolder {
		itemType = "folder"
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":    true,
			"fileId":     cfg.FileId,
			"fileName":   fileName,
			"isFolder":   isFolder,
			"deleteType": deleteType,
			"message":    fmt.Sprintf("Successfully %s %s '%s'", deleteType, itemType, fileName),
		},
	}, nil
}

// ============================================================================
// GDRIVE-SHARE EXECUTOR
// ============================================================================

// GDriveShareExecutor handles gdrive-share node type
type GDriveShareExecutor struct{}

// GDriveShareConfig defines the typed configuration for gdrive-share
type GDriveShareConfig struct {
	AccessToken       string `json:"accessToken" description:"OAuth2 access token"`
	ServiceAccountKey string `json:"serviceAccountKey" description:"Service account JSON key"`
	FileId            string `json:"fileId" description:"File ID to share"`
	EmailAddress      string `json:"emailAddress" description:"Email to share with"`
	Role              string `json:"role" default:"reader" description:"Permission role"`
	Type              string `json:"type" default:"user" description:"Permission type"`
	Domain            string `json:"domain" description:"Domain for domain type"`
	SendEmail         bool   `json:"sendEmail" default:"true" description:"Send email notification"`
	EmailMessage      string `json:"emailMessage" description:"Email message"`
}

func (e *GDriveShareExecutor) Type() string { return "gdrive-share" }

func (e *GDriveShareExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	// Parse config
	var cfg GDriveShareConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	} else {
		return nil, fmt.Errorf("invalid resolver type")
	}

	gdriveCfg := parseGDriveConfig(step.Config)
	svc, err := getDriveService(gdriveCfg)
	if err != nil {
		return nil, err
	}

	if cfg.FileId == "" {
		return nil, fmt.Errorf("fileId is required")
	}

	// Get file info
	file, err := svc.Files.Get(cfg.FileId).Fields("id, name").Do()
	if err != nil {
		return nil, fmt.Errorf("failed to get file info: %w", err)
	}

	// Create permission
	permission := &drive.Permission{
		Role: cfg.Role,
		Type: cfg.Type,
	}

	if cfg.EmailAddress != "" && cfg.Type == "user" {
		permission.EmailAddress = cfg.EmailAddress
	}
	if cfg.Domain != "" && cfg.Type == "domain" {
		permission.Domain = cfg.Domain
	}

	// Set up request options
	call := svc.Permissions.Create(cfg.FileId, permission).
		Fields("id, role, type, emailAddress, domain, displayName")

	if cfg.Type == "user" && cfg.SendEmail {
		call = call.SendNotificationEmail(true)
		if cfg.EmailMessage != "" {
			call = call.EmailMessage(cfg.EmailMessage)
		}
	}

	createdPerm, err := call.Do()
	if err != nil {
		return nil, fmt.Errorf("failed to create permission: %w", err)
	}

	// Note: When sharing with "anyone", the permission itself makes the file accessible
	// No additional file update is needed

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":       true,
			"fileId":        cfg.FileId,
			"fileName":      file.Name,
			"permissionId":  createdPerm.Id,
			"role":          createdPerm.Role,
			"type":          createdPerm.Type,
			"emailAddress":  createdPerm.EmailAddress,
			"domain":        createdPerm.Domain,
			"displayName":   createdPerm.DisplayName,
			"message":       fmt.Sprintf("Successfully shared '%s' with %s access", file.Name, cfg.Role),
		},
	}, nil
}

// ============================================================================
// GDRIVE-SEARCH EXECUTOR
// ============================================================================

// GDriveSearchExecutor handles gdrive-search node type
type GDriveSearchExecutor struct{}

// GDriveSearchConfig defines the typed configuration for gdrive-search
type GDriveSearchConfig struct {
	AccessToken       string `json:"accessToken" description:"OAuth2 access token"`
	ServiceAccountKey string `json:"serviceAccountKey" description:"Service account JSON key"`
	Query             string `json:"query" description:"Search query"`
	MimeType          string `json:"mimeType" description:"MIME type filter"`
	FolderId          string `json:"folderId" default:"root" description:"Folder to search in"`
	IncludeTrash      bool   `json:"includeTrash" default:"false" description:"Include trashed files"`
	Limit             int    `json:"limit" default:"50" description:"Maximum results"`
}

func (e *GDriveSearchExecutor) Type() string { return "gdrive-search" }

func (e *GDriveSearchExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	// Parse config
	var cfg GDriveSearchConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	} else {
		return nil, fmt.Errorf("invalid resolver type")
	}

	gdriveCfg := parseGDriveConfig(step.Config)
	svc, err := getDriveService(gdriveCfg)
	if err != nil {
		return nil, err
	}

	if cfg.Query == "" {
		return nil, fmt.Errorf("query is required")
	}

	limit := cfg.Limit
	if limit <= 0 {
		limit = 50
	}

	// Build search query
	q := cfg.Query

	// Add MIME type filter if specified
	if cfg.MimeType != "" {
		q = fmt.Sprintf("%s and mimeType='%s'", q, cfg.MimeType)
	}

	// Add folder filter if not searching everywhere
	if cfg.FolderId != "" && cfg.FolderId != "all" {
		if strings.Contains(q, " in parents") {
			// Query already has parent filter
		} else {
			q = fmt.Sprintf("%s and '%s' in parents", q, cfg.FolderId)
		}
	}

	// Handle trash
	if !cfg.IncludeTrash {
		q = fmt.Sprintf("%s and trashed=false", q)
	}

	// Execute search
	var files []*drive.File
	err = svc.Files.List().
		Q(q).
		Fields("files(id, name, mimeType, size, createdTime, modifiedTime, owners, parents, webViewLink)").
		PageSize(int64(limit)).
		Pages(ctx, func(fl *drive.FileList) error {
			files = append(files, fl.Files...)
			return nil
		})

	if err != nil {
		return nil, fmt.Errorf("search failed: %w", err)
	}

	// Build results
	results := make([]map[string]interface{}, 0, len(files))
	for _, f := range files {
		result := map[string]interface{}{
			"id":         f.Id,
			"name":       f.Name,
			"mimeType":   f.MimeType,
			"size":       f.Size,
			"created":    f.CreatedTime,
			"modified":   f.ModifiedTime,
			"isFolder":   f.MimeType == "application/vnd.google-apps.folder",
			"webViewLink": f.WebViewLink,
			"parents":    f.Parents,
		}
		if len(f.Owners) > 0 {
			result["owner"] = f.Owners[0].DisplayName
			result["ownerEmail"] = f.Owners[0].EmailAddress
		}
		results = append(results, result)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":  true,
			"query":    cfg.Query,
			"results":  results,
			"count":    len(results),
			"hasMore":  len(results) >= limit,
			"message":  fmt.Sprintf("Found %d results for '%s'", len(results), cfg.Query),
		},
	}, nil
}

// ============================================================================
// GDRIVE-FOLDER-CREATE EXECUTOR
// ============================================================================

// GDriveFolderCreateExecutor handles gdrive-folder-create node type
type GDriveFolderCreateExecutor struct{}

// GDriveFolderCreateConfig defines the typed configuration for gdrive-folder-create
type GDriveFolderCreateConfig struct {
	AccessToken       string `json:"accessToken" description:"OAuth2 access token"`
	ServiceAccountKey string `json:"serviceAccountKey" description:"Service account JSON key"`
	Name              string `json:"name" description:"Folder name"`
	ParentFolderId    string `json:"parentFolderId" default:"root" description:"Parent folder ID"`
}

func (e *GDriveFolderCreateExecutor) Type() string { return "gdrive-folder-create" }

func (e *GDriveFolderCreateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	// Parse config
	var cfg GDriveFolderCreateConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	} else {
		return nil, fmt.Errorf("invalid resolver type")
	}

	gdriveCfg := parseGDriveConfig(step.Config)
	svc, err := getDriveService(gdriveCfg)
	if err != nil {
		return nil, err
	}

	if cfg.Name == "" {
		return nil, fmt.Errorf("name is required")
	}

	parentFolderId := cfg.ParentFolderId
	if parentFolderId == "" {
		parentFolderId = "root"
	}

	// Create folder metadata
	folder := &drive.File{
		Name:     cfg.Name,
		MimeType: "application/vnd.google-apps.folder",
		Parents:  []string{parentFolderId},
	}

	createdFolder, err := svc.Files.Create(folder).
		Fields("id, name, mimeType, webViewLink, parents").
		Do()
	if err != nil {
		return nil, fmt.Errorf("failed to create folder: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":     true,
			"folderId":    createdFolder.Id,
			"folderName":  createdFolder.Name,
			"parentId":    parentFolderId,
			"webViewLink": createdFolder.WebViewLink,
			"parents":     createdFolder.Parents,
			"message":     fmt.Sprintf("Successfully created folder '%s'", createdFolder.Name),
		},
	}, nil
}

// ============================================================================
// GDRIVE-PERMISSION-LIST EXECUTOR
// ============================================================================

// GDrivePermissionListExecutor handles gdrive-permission-list node type
type GDrivePermissionListExecutor struct{}

// GDrivePermissionListConfig defines the typed configuration for gdrive-permission-list
type GDrivePermissionListConfig struct {
	AccessToken       string `json:"accessToken" description:"OAuth2 access token"`
	ServiceAccountKey string `json:"serviceAccountKey" description:"Service account JSON key"`
	FileId            string `json:"fileId" description:"File ID to list permissions for"`
}

func (e *GDrivePermissionListExecutor) Type() string { return "gdrive-permission-list" }

func (e *GDrivePermissionListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	// Parse config
	var cfg GDrivePermissionListConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	} else {
		return nil, fmt.Errorf("invalid resolver type")
	}

	gdriveCfg := parseGDriveConfig(step.Config)
	svc, err := getDriveService(gdriveCfg)
	if err != nil {
		return nil, err
	}

	if cfg.FileId == "" {
		return nil, fmt.Errorf("fileId is required")
	}

	// Get file info
	file, err := svc.Files.Get(cfg.FileId).Fields("id, name").Do()
	if err != nil {
		return nil, fmt.Errorf("failed to get file info: %w", err)
	}

	// List permissions
	permissions, err := svc.Permissions.List(cfg.FileId).
		Fields("permissions(id, role, type, emailAddress, domain, displayName, photoLink, deleted)").
		Do()
	if err != nil {
		return nil, fmt.Errorf("failed to list permissions: %w", err)
	}

	// Build permissions list
	perms := make([]map[string]interface{}, 0, len(permissions.Permissions))
	for _, p := range permissions.Permissions {
		perm := map[string]interface{}{
			"id":          p.Id,
			"role":        p.Role,
			"type":        p.Type,
			"displayName": p.DisplayName,
			"deleted":     p.Deleted,
		}
		if p.EmailAddress != "" {
			perm["emailAddress"] = p.EmailAddress
		}
		if p.Domain != "" {
			perm["domain"] = p.Domain
		}
		if p.PhotoLink != "" {
			perm["photoLink"] = p.PhotoLink
		}
		perms = append(perms, perm)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":     true,
			"fileId":      cfg.FileId,
			"fileName":    file.Name,
			"permissions": perms,
			"count":       len(perms),
			"message":     fmt.Sprintf("Found %d permissions for '%s'", len(perms), file.Name),
		},
	}, nil
}
