package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
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
	BoxAPIBase    = "https://api.box.com/2.0"
	BoxUploadBase = "https://upload.box.com/api/2.0"
	IconBox       = "archive"
)

// BoxClient represents a Box API client
type BoxClient struct {
	AccessToken string
	HTTPClient  *http.Client
}

// Box clients cache
var (
	boxClients = make(map[string]*BoxClient)
	clientMux  sync.RWMutex
)

func main() {
	// Get port from env or use default
	port := os.Getenv("SKILL_PORT")
	if port == "" {
		port = "50115"
	}

	// Create skill server
	server := grpc.NewSkillServer("skill-box", "1.0.0")

	// Register Box executors with schemas
	server.RegisterExecutorWithSchema("box-list", &BoxListExecutor{}, BoxListSchema)
	server.RegisterExecutorWithSchema("box-upload", &BoxUploadExecutor{}, BoxUploadSchema)
	server.RegisterExecutorWithSchema("box-download", &BoxDownloadExecutor{}, BoxDownloadSchema)
	server.RegisterExecutorWithSchema("box-delete", &BoxDeleteExecutor{}, BoxDeleteSchema)
	server.RegisterExecutorWithSchema("box-share", &BoxShareExecutor{}, BoxShareSchema)
	server.RegisterExecutorWithSchema("box-folder-create", &BoxFolderCreateExecutor{}, BoxFolderCreateSchema)
	server.RegisterExecutorWithSchema("box-search", &BoxSearchExecutor{}, BoxSearchSchema)
	server.RegisterExecutorWithSchema("box-metadata-get", &BoxMetadataGetExecutor{}, BoxMetadataGetSchema)

	fmt.Printf("Starting skill-box gRPC server on port %s\n", port)
	if err := server.Serve(port); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to serve: %v\n", err)
		os.Exit(1)
	}
}

// ============================================================================
// BOX CLIENT HELPERS
// ============================================================================

// getBoxClient returns or creates a Box client (cached)
func getBoxClient(accessToken string) *BoxClient {
	clientMux.RLock()
	client, ok := boxClients[accessToken]
	clientMux.RUnlock()

	if ok {
		return client
	}

	clientMux.Lock()
	defer clientMux.Unlock()

	// Double check
	if client, ok := boxClients[accessToken]; ok {
		return client
	}

	client = &BoxClient{
		AccessToken: accessToken,
		HTTPClient: &http.Client{
			Timeout: 120 * time.Second,
		},
	}
	boxClients[accessToken] = client
	return client
}

// doRequest performs an HTTP request to the Box API
func (c *BoxClient) doRequest(ctx context.Context, method, url string, body io.Reader, contentType string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.AccessToken)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	return resp, nil
}

// checkResponse checks the response for errors
func checkResponse(resp *http.Response) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}

	body, _ := io.ReadAll(resp.Body)
	return &BoxAPIError{
		StatusCode: resp.StatusCode,
		Message:    string(body),
	}
}

// BoxAPIError represents a Box API error
type BoxAPIError struct {
	StatusCode int
	Message    string
}

func (e *BoxAPIError) Error() string {
	return fmt.Sprintf("Box API error: status %d - %s", e.StatusCode, e.Message)
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
// BOX API DATA STRUCTURES
// ============================================================================

// BoxFile represents a Box file object
type BoxFile struct {
	Type            string            `json:"type"`
	ID              string            `json:"id"`
	Name            string            `json:"name"`
	Size            int64             `json:"size,omitempty"`
	PathCollection  *BoxPathCollection `json:"path_collection,omitempty"`
	CreatedAt       string            `json:"created_at,omitempty"`
	ModifiedAt      string            `json:"modified_at,omitempty"`
	Description     string            `json:"description,omitempty"`
	ContentType     string            `json:"content_type,omitempty"`
	SharedLink      *BoxSharedLink    `json:"shared_link,omitempty"`
	Permissions     *BoxPermissions   `json:"permissions,omitempty"`
	ETag            string            `json:"etag,omitempty"`
	SequenceID      string            `json:"sequence_id,omitempty"`
	SHA1            string            `json:"sha1,omitempty"`
	FileVersion     *BoxFileVersion   `json:"file_version,omitempty"`
	TrashedAt       string            `json:"trashed_at,omitempty"`
	PurgedAt        string            `json:"purged_at,omitempty"`
	ContentCreatedAt string           `json:"content_created_at,omitempty"`
	ContentModifiedAt string          `json:"content_modified_at,omitempty"`
	CreatedBy       *BoxUser          `json:"created_by,omitempty"`
	ModifiedBy      *BoxUser          `json:"modified_by,omitempty"`
	OwnedBy         *BoxUser          `json:"owned_by,omitempty"`
	Parent          *BoxFolderRef     `json:"parent,omitempty"`
	ItemStatus      string            `json:"item_status,omitempty"`
	Metadata        map[string]interface{} `json:"metadata,omitempty"`
}

// BoxFolder represents a Box folder object
type BoxFolder struct {
	Type            string            `json:"type"`
	ID              string            `json:"id"`
	Name            string            `json:"name"`
	PathCollection  *BoxPathCollection `json:"path_collection,omitempty"`
	CreatedAt       string            `json:"created_at,omitempty"`
	ModifiedAt      string            `json:"modified_at,omitempty"`
	Description     string            `json:"description,omitempty"`
	Size            int64             `json:"size,omitempty"`
	Permissions     *BoxPermissions   `json:"permissions,omitempty"`
	ETag            string            `json:"etag,omitempty"`
	SequenceID      string            `json:"sequence_id,omitempty"`
	SharedLink      *BoxSharedLink    `json:"shared_link,omitempty"`
	FolderUploadEmail *BoxFolderUploadEmail `json:"folder_upload_email,omitempty"`
	Parent          *BoxFolderRef     `json:"parent,omitempty"`
	ItemStatus      string            `json:"item_status,omitempty"`
	ItemCollection  *BoxItemCollection `json:"item_collection,omitempty"`
	TrashedAt       string            `json:"trashed_at,omitempty"`
	PurgedAt        string            `json:"purged_at,omitempty"`
	ContentCreatedAt string           `json:"content_created_at,omitempty"`
	ContentModifiedAt string          `json:"content_modified_at,omitempty"`
	CreatedBy       *BoxUser          `json:"created_by,omitempty"`
	ModifiedBy      *BoxUser          `json:"modified_by,omitempty"`
	OwnedBy         *BoxUser          `json:"owned_by,omitempty"`
	Metadata        map[string]interface{} `json:"metadata,omitempty"`
}

// BoxPathCollection represents the path to an item
type BoxPathCollection struct {
	TotalCount int               `json:"total_count"`
	Entries    []BoxFolderRef    `json:"entries"`
}

// BoxFolderRef represents a folder reference
type BoxFolderRef struct {
	Type string `json:"type"`
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

// BoxSharedLink represents a shared link
type BoxSharedLink struct {
	URL                string `json:"url"`
	DownloadURL        string `json:"download_url"`
	VanityURL          string `json:"vanity_url"`
	EffectiveAccess    string `json:"effective_access"`
	EffectivePermission string `json:"effective_permission"`
	IsPasswordEnabled  bool   `json:"is_password_enabled"`
	UnsharedAt         string `json:"unshared_at"`
	DownloadCount      int    `json:"download_count"`
	PreviewCount       int    `json:"preview_count"`
	Access             string `json:"access"`
	Permissions        *BoxSharedLinkPermissions `json:"permissions"`
}

// BoxSharedLinkPermissions represents shared link permissions
type BoxSharedLinkPermissions struct {
	CanDownload bool `json:"can_download"`
	CanPreview  bool `json:"can_preview"`
	CanEdit     bool `json:"can_edit"`
}

// BoxPermissions represents item permissions
type BoxPermissions struct {
	CanDownload            bool `json:"can_download"`
	CanPreview             bool `json:"can_preview"`
	CanUpload              bool `json:"can_upload"`
	CanComment             bool `json:"can_comment"`
	CanRename              bool `json:"can_rename"`
	CanDelete              bool `json:"can_delete"`
	CanShare               bool `json:"can_share"`
	CanSetShareAccess      bool `json:"can_set_share_access"`
	CanInviteCollaborator  bool `json:"can_invite_collaborator"`
	CanPathRename          bool `json:"can_path_rename"`
}

// BoxFileVersion represents a file version
type BoxFileVersion struct {
	Type       string `json:"type"`
	ID         string `json:"id"`
	SHA1       string `json:"sha1"`
	Name       string `json:"name"`
	Size       int64  `json:"size"`
	CreatedAt  string `json:"created_at"`
	ModifiedAt string `json:"modified_at"`
}

// BoxUser represents a Box user
type BoxUser struct {
	Type    string `json:"type"`
	ID      string `json:"id"`
	Name    string `json:"name"`
	Login   string `json:"login"`
	CreatedAt string `json:"created_at,omitempty"`
}

// BoxFolderUploadEmail represents folder upload email
type BoxFolderUploadEmail struct {
	Access string `json:"access"`
	Email  string `json:"email"`
}

// BoxItemCollection represents a collection of items
type BoxItemCollection struct {
	TotalCount int             `json:"total_count"`
	Entries    []BoxItemEntry  `json:"entries"`
	Offset     int             `json:"offset"`
	Limit      int             `json:"limit"`
	Order      []BoxOrderEntry `json:"order"`
}

// BoxItemEntry represents an item in a collection
type BoxItemEntry struct {
	Type string          `json:"type"`
	ID   string          `json:"id"`
	Name string          `json:"name"`
	File *BoxFile        `json:"-"`
	Folder *BoxFolder    `json:"-"`
}

// BoxOrderEntry represents an order entry
type BoxOrderEntry struct {
	By      string `json:"by"`
	Direction string `json:"direction"`
}

// BoxSearchResult represents search results
type BoxSearchResult struct {
	Type         string `json:"type"`
	ID           string `json:"id"`
	Name         string `json:"name"`
	Description  string `json:"description,omitempty"`
	Size         int64  `json:"size,omitempty"`
	PathCollection *BoxPathCollection `json:"path_collection,omitempty"`
	CreatedAt    string `json:"created_at,omitempty"`
	ModifiedAt   string `json:"modified_at,omitempty"`
	ContentType  string `json:"content_type,omitempty"`
	Parent       *BoxFolderRef `json:"parent,omitempty"`
	ItemStatus   string `json:"item_status,omitempty"`
}

// BoxSearchResponse represents the search API response
type BoxSearchResponse struct {
	Type         string           `json:"type"`
	Limit        int              `json:"limit"`
	Offset       int              `json:"offset"`
	TotalCount   int              `json:"total_count"`
	Entries      []BoxSearchResult `json:"entries"`
}

// BoxItemsResponse represents folder items response
type BoxItemsResponse struct {
	Type         string           `json:"type"`
	ID           string           `json:"id"`
	Name         string           `json:"name"`
	TotalCount   int              `json:"total_count"`
	Entries      []BoxItem        `json:"entries"`
	Offset       int              `json:"offset"`
	Limit        int              `json:"limit"`
	Order        []BoxOrderEntry  `json:"order"`
}

// BoxItem represents an item (file or folder)
type BoxItem struct {
	Type   string `json:"type"`
	ID     string `json:"id"`
	Name   string `json:"name"`
	Size   int64  `json:"size,omitempty"`
	*BoxFile
	*BoxFolder
}

// BoxMetadataResponse represents metadata response
type BoxMetadataResponse struct {
	Template   string                 `json:"$template"`
	Scope      string                 `json:"$scope"`
	Version    int                    `json:"$version"`
	Type       string                 `json:"$type"`
	ID         string                 `json:"$id"`
	Parent     string                 `json:"$parent"`
	TemplateVersion int               `json:"$templateVersion"`
	CanEdit    bool                   `json:"$canEdit"`
	RawData    map[string]interface{} `json:"-"`
}

// ============================================================================
// SCHEMAS
// ============================================================================

// BoxListSchema is the UI schema for box-list
var BoxListSchema = resolver.NewSchemaBuilder("box-list").
	WithName("List Box Files/Folders").
	WithCategory("action").
	WithIcon(IconBox).
	WithDescription("List files and folders in a Box folder").
	AddSection("Connection").
		AddExpressionField("accessToken", "Access Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("YOUR_BOX_ACCESS_TOKEN"),
			resolver.WithHint("Box OAuth2 access token (use {{secrets.box_access_token}})"),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Folder").
		AddExpressionField("folderId", "Folder ID",
			resolver.WithDefault("0"),
			resolver.WithHint("Box folder ID (0 = root folder)"),
		).
		EndSection().
	AddSection("Options").
		AddNumberField("limit", "Limit",
			resolver.WithDefault(100),
			resolver.WithMinMax(1, 1000),
			resolver.WithHint("Maximum number of items to return"),
		).
		AddNumberField("offset", "Offset",
			resolver.WithDefault(0),
			resolver.WithHint("Offset for pagination"),
		).
		AddExpressionField("fields", "Fields",
			resolver.WithPlaceholder("name,size,modified_at"),
			resolver.WithHint("Comma-separated list of fields to return"),
		).
		EndSection().
	Build()

// BoxUploadSchema is the UI schema for box-upload
var BoxUploadSchema = resolver.NewSchemaBuilder("box-upload").
	WithName("Upload File to Box").
	WithCategory("action").
	WithIcon(IconBox).
	WithDescription("Upload a file to a Box folder").
	AddSection("Connection").
		AddExpressionField("accessToken", "Access Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("YOUR_BOX_ACCESS_TOKEN"),
			resolver.WithHint("Box OAuth2 access token"),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Destination").
		AddExpressionField("folderId", "Folder ID",
			resolver.WithDefault("0"),
			resolver.WithHint("Box folder ID to upload to (0 = root folder)"),
		).
		AddExpressionField("fileName", "File Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("document.pdf"),
			resolver.WithHint("Name for the uploaded file"),
		).
		EndSection().
	AddSection("Content").
		AddTextareaField("content", "Content",
			resolver.WithRequired(),
			resolver.WithRows(8),
			resolver.WithPlaceholder("File content here..."),
			resolver.WithHint("File content (use base64 for binary files)"),
		).
		AddToggleField("base64Decode", "Base64 Decode",
			resolver.WithDefault(false),
			resolver.WithHint("Decode content from base64 before uploading"),
		).
		AddExpressionField("contentType", "Content Type",
			resolver.WithPlaceholder("application/pdf"),
			resolver.WithHint("MIME type of the file"),
		).
		EndSection().
	Build()

// BoxDownloadSchema is the UI schema for box-download
var BoxDownloadSchema = resolver.NewSchemaBuilder("box-download").
	WithName("Download File from Box").
	WithCategory("action").
	WithIcon(IconBox).
	WithDescription("Download a file from Box").
	AddSection("Connection").
		AddExpressionField("accessToken", "Access Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("YOUR_BOX_ACCESS_TOKEN"),
			resolver.WithHint("Box OAuth2 access token"),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("File").
		AddExpressionField("fileId", "File ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("123456789"),
			resolver.WithHint("Box file ID to download"),
		).
		EndSection().
	AddSection("Options").
		AddToggleField("base64Encode", "Base64 Encode",
			resolver.WithDefault(false),
			resolver.WithHint("Encode the content as base64 (useful for binary files)"),
		).
		EndSection().
	Build()

// BoxDeleteSchema is the UI schema for box-delete
var BoxDeleteSchema = resolver.NewSchemaBuilder("box-delete").
	WithName("Delete Box Item").
	WithCategory("action").
	WithIcon(IconBox).
	WithDescription("Delete a file or folder from Box").
	AddSection("Connection").
		AddExpressionField("accessToken", "Access Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("YOUR_BOX_ACCESS_TOKEN"),
			resolver.WithHint("Box OAuth2 access token"),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Item").
		AddExpressionField("itemId", "Item ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("123456789"),
			resolver.WithHint("Box file or folder ID to delete"),
		).
		AddSelectField("itemType", "Item Type",
			[]resolver.SelectOption{
				{Label: "File", Value: "file"},
				{Label: "Folder", Value: "folder"},
			},
			resolver.WithDefault("file"),
			resolver.WithHint("Type of item to delete"),
		).
		EndSection().
	AddSection("Options").
		AddToggleField("recursive", "Recursive",
			resolver.WithDefault(false),
			resolver.WithHint("Delete folder and all contents (only for folders)"),
		).
		EndSection().
	Build()

// BoxShareSchema is the UI schema for box-share
var BoxShareSchema = resolver.NewSchemaBuilder("box-share").
	WithName("Create/Update Box Shared Link").
	WithCategory("action").
	WithIcon(IconBox).
	WithDescription("Create or update a shared link for a Box file or folder").
	AddSection("Connection").
		AddExpressionField("accessToken", "Access Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("YOUR_BOX_ACCESS_TOKEN"),
			resolver.WithHint("Box OAuth2 access token"),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Item").
		AddExpressionField("itemId", "Item ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("123456789"),
			resolver.WithHint("Box file or folder ID to share"),
		).
		AddSelectField("itemType", "Item Type",
			[]resolver.SelectOption{
				{Label: "File", Value: "file"},
				{Label: "Folder", Value: "folder"},
			},
			resolver.WithDefault("file"),
			resolver.WithHint("Type of item to share"),
		).
		EndSection().
	AddSection("Shared Link Settings").
		AddSelectField("access", "Access Level",
			[]resolver.SelectOption{
				{Label: "Open (Anyone with link)", Value: "open"},
				{Label: "Company (People in your company)", Value: "company"},
				{Label: "Collaborators (Invited people only)", Value: "collaborators"},
			},
			resolver.WithDefault("open"),
			resolver.WithHint("Who can access the shared link"),
		).
		AddToggleField("canDownload", "Allow Download",
			resolver.WithDefault(true),
			resolver.WithHint("Allow downloading the file"),
		).
		AddToggleField("canPreview", "Allow Preview",
			resolver.WithDefault(true),
			resolver.WithHint("Allow previewing the file"),
		).
		AddExpressionField("unsharedAt", "Unshare At",
			resolver.WithPlaceholder("2025-12-31T23:59:59-08:00"),
			resolver.WithHint("ISO 8601 date when link expires (optional)"),
		).
		EndSection().
	Build()

// BoxFolderCreateSchema is the UI schema for box-folder-create
var BoxFolderCreateSchema = resolver.NewSchemaBuilder("box-folder-create").
	WithName("Create Box Folder").
	WithCategory("action").
	WithIcon(IconBox).
	WithDescription("Create a new folder in Box").
	AddSection("Connection").
		AddExpressionField("accessToken", "Access Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("YOUR_BOX_ACCESS_TOKEN"),
			resolver.WithHint("Box OAuth2 access token"),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Folder").
		AddExpressionField("name", "Folder Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("New Folder"),
			resolver.WithHint("Name for the new folder"),
		).
		AddExpressionField("parentFolderId", "Parent Folder ID",
			resolver.WithDefault("0"),
			resolver.WithHint("Parent folder ID (0 = root folder)"),
		).
		EndSection().
	AddSection("Options").
		AddExpressionField("description", "Description",
			resolver.WithPlaceholder("Folder description"),
			resolver.WithHint("Optional description for the folder"),
		).
		EndSection().
	Build()

// BoxSearchSchema is the UI schema for box-search
var BoxSearchSchema = resolver.NewSchemaBuilder("box-search").
	WithName("Search Box").
	WithCategory("action").
	WithIcon(IconBox).
	WithDescription("Search for files and folders in Box").
	AddSection("Connection").
		AddExpressionField("accessToken", "Access Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("YOUR_BOX_ACCESS_TOKEN"),
			resolver.WithHint("Box OAuth2 access token"),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Search").
		AddExpressionField("query", "Search Query",
			resolver.WithRequired(),
			resolver.WithPlaceholder("document"),
			resolver.WithHint("Search query string"),
		).
		EndSection().
	AddSection("Filters").
		AddExpressionField("fileExtensions", "File Extensions",
			resolver.WithPlaceholder("pdf,docx,xlsx"),
			resolver.WithHint("Comma-separated list of file extensions to filter"),
		).
		AddExpressionField("contentTypes", "Content Types",
			resolver.WithPlaceholder("name,description,file_content"),
			resolver.WithHint("Comma-separated list of content types to search"),
		).
		AddExpressionField("ancestorFolderIds", "Ancestor Folder IDs",
			resolver.WithPlaceholder("12345,67890"),
			resolver.WithHint("Comma-separated list of folder IDs to search within"),
		).
		EndSection().
	AddSection("Options").
		AddNumberField("limit", "Limit",
			resolver.WithDefault(100),
			resolver.WithMinMax(1, 1000),
			resolver.WithHint("Maximum number of results to return"),
		).
		AddNumberField("offset", "Offset",
			resolver.WithDefault(0),
			resolver.WithHint("Offset for pagination"),
		).
		EndSection().
	Build()

// BoxMetadataGetSchema is the UI schema for box-metadata-get
var BoxMetadataGetSchema = resolver.NewSchemaBuilder("box-metadata-get").
	WithName("Get Box Metadata").
	WithCategory("action").
	WithIcon(IconBox).
	WithDescription("Get metadata for a Box file").
	AddSection("Connection").
		AddExpressionField("accessToken", "Access Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("YOUR_BOX_ACCESS_TOKEN"),
			resolver.WithHint("Box OAuth2 access token"),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("File").
		AddExpressionField("fileId", "File ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("123456789"),
			resolver.WithHint("Box file ID"),
		).
		EndSection().
	AddSection("Metadata").
		AddExpressionField("scope", "Scope",
			resolver.WithDefault("enterprise"),
			resolver.WithHint("Metadata scope (enterprise or global)"),
		).
		AddExpressionField("template", "Template",
			resolver.WithDefault("properties"),
			resolver.WithHint("Metadata template name"),
		).
		EndSection().
	Build()

// ============================================================================
// BOX-LIST EXECUTOR
// ============================================================================

// BoxListExecutor handles box-list node type
type BoxListExecutor struct{}

func (e *BoxListExecutor) Type() string { return "box-list" }

func (e *BoxListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	accessToken := resolver.ResolveString(getString(config, "accessToken"))
	if accessToken == "" {
		return nil, fmt.Errorf("accessToken is required")
	}

	folderID := resolver.ResolveString(getString(config, "folderId"))
	if folderID == "" {
		folderID = "0"
	}

	limit := getInt(config, "limit", 100)
	offset := getInt(config, "offset", 0)
	fields := resolver.ResolveString(getString(config, "fields"))

	client := getBoxClient(accessToken)

	// Build URL
	urlStr := fmt.Sprintf("%s/folders/%s/items?limit=%d&offset=%d", BoxAPIBase, folderID, limit, offset)
	if fields != "" {
		urlStr += "&fields=" + url.QueryEscape(fields)
	}

	resp, err := client.doRequest(ctx, "GET", urlStr, nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if err := checkResponse(resp); err != nil {
		return nil, err
	}

	var result BoxItemsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	// Convert items to output format
	var files []map[string]interface{}
	var folders []map[string]interface{}

	for _, entry := range result.Entries {
		item := make(map[string]interface{})
		item["type"] = entry.Type
		item["id"] = entry.ID
		item["name"] = entry.Name

		if entry.Type == "file" {
			if entry.Size > 0 {
				item["size"] = entry.Size
			}
			files = append(files, item)
		} else if entry.Type == "folder" {
			folders = append(folders, item)
		}
	}

	output := map[string]interface{}{
		"folder_id":    folderID,
		"folder_name":  result.Name,
		"total_count":  result.TotalCount,
		"offset":       result.Offset,
		"limit":        result.Limit,
		"files":        files,
		"folders":      folders,
		"items":        result.Entries,
		"file_count":   len(files),
		"folder_count": len(folders),
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// ============================================================================
// BOX-UPLOAD EXECUTOR
// ============================================================================

// BoxUploadExecutor handles box-upload node type
type BoxUploadExecutor struct{}

func (e *BoxUploadExecutor) Type() string { return "box-upload" }

func (e *BoxUploadExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	accessToken := resolver.ResolveString(getString(config, "accessToken"))
	if accessToken == "" {
		return nil, fmt.Errorf("accessToken is required")
	}

	folderID := resolver.ResolveString(getString(config, "folderId"))
	if folderID == "" {
		folderID = "0"
	}

	fileName := resolver.ResolveString(getString(config, "fileName"))
	if fileName == "" {
		return nil, fmt.Errorf("fileName is required")
	}

	content := resolver.ResolveString(getString(config, "content"))
	if content == "" {
		return nil, fmt.Errorf("content is required")
	}

	base64Decode := getBool(config, "base64Decode", false)
	contentType := resolver.ResolveString(getString(config, "contentType"))
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	var fileContent []byte
	if base64Decode {
		var err error
		fileContent, err = decodeBase64(content)
		if err != nil {
			return nil, fmt.Errorf("failed to decode base64 content: %w", err)
		}
	} else {
		fileContent = []byte(content)
	}

	client := getBoxClient(accessToken)

	// Create multipart form
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	// Add attributes
	attributes := map[string]interface{}{
		"name": fileName,
		"parent": map[string]string{
			"id": folderID,
		},
	}
	attributesJSON, _ := json.Marshal(attributes)
	err := writer.WriteField("attributes", string(attributesJSON))
	if err != nil {
		return nil, fmt.Errorf("failed to write attributes: %w", err)
	}

	// Add file
	part, err := writer.CreateFormFile("file", fileName)
	if err != nil {
		return nil, fmt.Errorf("failed to create form file: %w", err)
	}
	_, err = part.Write(fileContent)
	if err != nil {
		return nil, fmt.Errorf("failed to write file content: %w", err)
	}

	err = writer.Close()
	if err != nil {
		return nil, fmt.Errorf("failed to close writer: %w", err)
	}

	// Upload file
	urlStr := fmt.Sprintf("%s/files/content", BoxUploadBase)
	req, err := http.NewRequestWithContext(ctx, "POST", urlStr, body)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := client.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("upload request failed: %w", err)
	}
	defer resp.Body.Close()

	if err := checkResponse(resp); err != nil {
		return nil, err
	}

	var uploadedFile BoxFile
	if err := json.NewDecoder(resp.Body).Decode(&uploadedFile); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	output := map[string]interface{}{
		"id":           uploadedFile.ID,
		"type":         uploadedFile.Type,
		"name":         uploadedFile.Name,
		"size":         uploadedFile.Size,
		"content_type": uploadedFile.ContentType,
		"sha1":         uploadedFile.SHA1,
		"etag":         uploadedFile.ETag,
		"created_at":   uploadedFile.CreatedAt,
		"modified_at":  uploadedFile.ModifiedAt,
	}

	if uploadedFile.Parent != nil {
		output["parent_id"] = uploadedFile.Parent.ID
		output["parent_name"] = uploadedFile.Parent.Name
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// ============================================================================
// BOX-DOWNLOAD EXECUTOR
// ============================================================================

// BoxDownloadExecutor handles box-download node type
type BoxDownloadExecutor struct{}

func (e *BoxDownloadExecutor) Type() string { return "box-download" }

func (e *BoxDownloadExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	accessToken := resolver.ResolveString(getString(config, "accessToken"))
	if accessToken == "" {
		return nil, fmt.Errorf("accessToken is required")
	}

	fileID := resolver.ResolveString(getString(config, "fileId"))
	if fileID == "" {
		return nil, fmt.Errorf("fileId is required")
	}

	base64Encode := getBool(config, "base64Encode", false)

	client := getBoxClient(accessToken)

	// Download file
	urlStr := fmt.Sprintf("%s/files/%s/content", BoxAPIBase, fileID)
	resp, err := client.doRequest(ctx, "GET", urlStr, nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if err := checkResponse(resp); err != nil {
		return nil, err
	}

	// Read content
	content, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Get file info first to include in output
	infoURL := fmt.Sprintf("%s/files/%s", BoxAPIBase, fileID)
	infoResp, err := client.doRequest(ctx, "GET", infoURL, nil, "")
	if err == nil {
		defer infoResp.Body.Close()
		var fileInfo BoxFile
		if err := json.NewDecoder(infoResp.Body).Decode(&fileInfo); err == nil {
			output := map[string]interface{}{
				"id":           fileInfo.ID,
				"name":         fileInfo.Name,
				"size":         fileInfo.Size,
				"content_type": fileInfo.ContentType,
				"sha1":         fileInfo.SHA1,
				"etag":         fileInfo.ETag,
			}

			if base64Encode {
				output["content_base64"] = encodeBase64(content)
				output["encoding"] = "base64"
			} else {
				output["content"] = string(content)
				output["encoding"] = "utf-8"
			}

			return &executor.StepResult{
				Output: output,
			}, nil
		}
	}

	// Fallback without file info
	output := map[string]interface{}{
		"id": fileID,
	}

	if base64Encode {
		output["content_base64"] = encodeBase64(content)
		output["encoding"] = "base64"
	} else {
		output["content"] = string(content)
		output["encoding"] = "utf-8"
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// ============================================================================
// BOX-DELETE EXECUTOR
// ============================================================================

// BoxDeleteExecutor handles box-delete node type
type BoxDeleteExecutor struct{}

func (e *BoxDeleteExecutor) Type() string { return "box-delete" }

func (e *BoxDeleteExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	accessToken := resolver.ResolveString(getString(config, "accessToken"))
	if accessToken == "" {
		return nil, fmt.Errorf("accessToken is required")
	}

	itemID := resolver.ResolveString(getString(config, "itemId"))
	if itemID == "" {
		return nil, fmt.Errorf("itemId is required")
	}

	itemType := resolver.ResolveString(getString(config, "itemType"))
	if itemType == "" {
		itemType = "file"
	}

	recursive := getBool(config, "recursive", false)

	client := getBoxClient(accessToken)

	// Build URL
	var urlStr string
	if itemType == "folder" {
		urlStr = fmt.Sprintf("%s/folders/%s", BoxAPIBase, itemID)
		if recursive {
			urlStr += "?recursive=true"
		}
	} else {
		urlStr = fmt.Sprintf("%s/files/%s", BoxAPIBase, itemID)
	}

	resp, err := client.doRequest(ctx, "DELETE", urlStr, nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if err := checkResponse(resp); err != nil {
		return nil, err
	}

	output := map[string]interface{}{
		"success":     true,
		"item_id":     itemID,
		"item_type":   itemType,
		"deleted":     true,
		"recursive":   recursive,
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// ============================================================================
// BOX-SHARE EXECUTOR
// ============================================================================

// BoxShareExecutor handles box-share node type
type BoxShareExecutor struct{}

func (e *BoxShareExecutor) Type() string { return "box-share" }

func (e *BoxShareExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	accessToken := resolver.ResolveString(getString(config, "accessToken"))
	if accessToken == "" {
		return nil, fmt.Errorf("accessToken is required")
	}

	itemID := resolver.ResolveString(getString(config, "itemId"))
	if itemID == "" {
		return nil, fmt.Errorf("itemId is required")
	}

	itemType := resolver.ResolveString(getString(config, "itemType"))
	if itemType == "" {
		itemType = "file"
	}

	access := resolver.ResolveString(getString(config, "access"))
	if access == "" {
		access = "open"
	}

	canDownload := getBool(config, "canDownload", true)
	canPreview := getBool(config, "canPreview", true)
	unsharedAt := resolver.ResolveString(getString(config, "unsharedAt"))

	client := getBoxClient(accessToken)

	// Build request body
	sharedLink := map[string]interface{}{
		"access": access,
		"permissions": map[string]bool{
			"can_download": canDownload,
			"can_preview":  canPreview,
		},
	}

	if unsharedAt != "" {
		sharedLink["unshared_at"] = unsharedAt
	}

	requestBody := map[string]interface{}{
		"shared_link": sharedLink,
	}

	jsonBody, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request body: %w", err)
	}

	// Build URL
	var urlStr string
	if itemType == "folder" {
		urlStr = fmt.Sprintf("%s/folders/%s", BoxAPIBase, itemID)
	} else {
		urlStr = fmt.Sprintf("%s/files/%s", BoxAPIBase, itemID)
	}

	resp, err := client.doRequest(ctx, "PUT", urlStr, bytes.NewReader(jsonBody), "application/json")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if err := checkResponse(resp); err != nil {
		return nil, err
	}

	// Decode response
	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	output := map[string]interface{}{
		"item_id":   itemID,
		"item_type": itemType,
		"shared":    true,
	}

	if sharedLink, ok := result["shared_link"].(map[string]interface{}); ok {
		output["shared_link_url"] = sharedLink["url"]
		output["shared_link_download_url"] = sharedLink["download_url"]
		output["access"] = sharedLink["access"]
		if perms, ok := sharedLink["permissions"].(map[string]interface{}); ok {
			output["can_download"] = perms["can_download"]
			output["can_preview"] = perms["can_preview"]
		}
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// ============================================================================
// BOX-FOLDER-CREATE EXECUTOR
// ============================================================================

// BoxFolderCreateExecutor handles box-folder-create node type
type BoxFolderCreateExecutor struct{}

func (e *BoxFolderCreateExecutor) Type() string { return "box-folder-create" }

func (e *BoxFolderCreateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	accessToken := resolver.ResolveString(getString(config, "accessToken"))
	if accessToken == "" {
		return nil, fmt.Errorf("accessToken is required")
	}

	name := resolver.ResolveString(getString(config, "name"))
	if name == "" {
		return nil, fmt.Errorf("name is required")
	}

	parentFolderID := resolver.ResolveString(getString(config, "parentFolderId"))
	if parentFolderID == "" {
		parentFolderID = "0"
	}

	description := resolver.ResolveString(getString(config, "description"))

	client := getBoxClient(accessToken)

	// Build request body
	requestBody := map[string]interface{}{
		"name": name,
		"parent": map[string]string{
			"id": parentFolderID,
		},
	}

	if description != "" {
		requestBody["description"] = description
	}

	jsonBody, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request body: %w", err)
	}

	// Create folder
	urlStr := fmt.Sprintf("%s/folders", BoxAPIBase)
	resp, err := client.doRequest(ctx, "POST", urlStr, bytes.NewReader(jsonBody), "application/json")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if err := checkResponse(resp); err != nil {
		return nil, err
	}

	var folder BoxFolder
	if err := json.NewDecoder(resp.Body).Decode(&folder); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	output := map[string]interface{}{
		"id":          folder.ID,
		"type":        folder.Type,
		"name":        folder.Name,
		"description": folder.Description,
		"created_at":  folder.CreatedAt,
		"modified_at": folder.ModifiedAt,
		"parent_id":   parentFolderID,
	}

	if folder.Parent != nil {
		output["parent_name"] = folder.Parent.Name
	}

	if folder.PathCollection != nil {
		output["path_collection"] = folder.PathCollection
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// ============================================================================
// BOX-SEARCH EXECUTOR
// ============================================================================

// BoxSearchExecutor handles box-search node type
type BoxSearchExecutor struct{}

func (e *BoxSearchExecutor) Type() string { return "box-search" }

func (e *BoxSearchExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	accessToken := resolver.ResolveString(getString(config, "accessToken"))
	if accessToken == "" {
		return nil, fmt.Errorf("accessToken is required")
	}

	query := resolver.ResolveString(getString(config, "query"))
	if query == "" {
		return nil, fmt.Errorf("query is required")
	}

	limit := getInt(config, "limit", 100)
	offset := getInt(config, "offset", 0)
	fileExtensions := resolver.ResolveString(getString(config, "fileExtensions"))
	contentTypes := resolver.ResolveString(getString(config, "contentTypes"))
	ancestorFolderIDs := resolver.ResolveString(getString(config, "ancestorFolderIds"))

	client := getBoxClient(accessToken)

	// Build URL with query parameters
	urlStr := fmt.Sprintf("%s/search?query=%s&limit=%d&offset=%d",
		BoxAPIBase,
		url.QueryEscape(query),
		limit,
		offset,
	)

	if fileExtensions != "" {
		urlStr += "&file_extensions=" + url.QueryEscape(fileExtensions)
	}

	if contentTypes != "" {
		urlStr += "&content_types=" + url.QueryEscape(contentTypes)
	}

	if ancestorFolderIDs != "" {
		urlStr += "&ancestor_folder_ids=" + url.QueryEscape(ancestorFolderIDs)
	}

	resp, err := client.doRequest(ctx, "GET", urlStr, nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if err := checkResponse(resp); err != nil {
		return nil, err
	}

	var result BoxSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	// Convert results to output format
	var items []map[string]interface{}
	for _, entry := range result.Entries {
		item := map[string]interface{}{
			"type":         entry.Type,
			"id":           entry.ID,
			"name":         entry.Name,
			"description":  entry.Description,
			"size":         entry.Size,
			"content_type": entry.ContentType,
			"created_at":   entry.CreatedAt,
			"modified_at":  entry.ModifiedAt,
			"item_status":  entry.ItemStatus,
		}

		if entry.Parent != nil {
			item["parent_id"] = entry.Parent.ID
			item["parent_name"] = entry.Parent.Name
		}

		if entry.PathCollection != nil {
			item["path_collection"] = entry.PathCollection
		}

		items = append(items, item)
	}

	output := map[string]interface{}{
		"query":        query,
		"total_count":  result.TotalCount,
		"limit":        result.Limit,
		"offset":       result.Offset,
		"results":      items,
		"result_count": len(items),
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// ============================================================================
// BOX-METADATA-GET EXECUTOR
// ============================================================================

// BoxMetadataGetExecutor handles box-metadata-get node type
type BoxMetadataGetExecutor struct{}

func (e *BoxMetadataGetExecutor) Type() string { return "box-metadata-get" }

func (e *BoxMetadataGetExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	accessToken := resolver.ResolveString(getString(config, "accessToken"))
	if accessToken == "" {
		return nil, fmt.Errorf("accessToken is required")
	}

	fileID := resolver.ResolveString(getString(config, "fileId"))
	if fileID == "" {
		return nil, fmt.Errorf("fileId is required")
	}

	scope := resolver.ResolveString(getString(config, "scope"))
	if scope == "" {
		scope = "enterprise"
	}

	template := resolver.ResolveString(getString(config, "template"))
	if template == "" {
		template = "properties"
	}

	client := getBoxClient(accessToken)

	// Build URL
	urlStr := fmt.Sprintf("%s/files/%s/metadata/%s/%s",
		BoxAPIBase,
		fileID,
		scope,
		template,
	)

	resp, err := client.doRequest(ctx, "GET", urlStr, nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if err := checkResponse(resp); err != nil {
		return nil, err
	}

	// Decode response as generic map to preserve all metadata fields
	var metadata map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&metadata); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	output := map[string]interface{}{
		"file_id":  fileID,
		"scope":    scope,
		"template": template,
		"metadata": metadata,
	}

	// Extract common fields
	if tmpl, ok := metadata["$template"].(string); ok {
		output["$template"] = tmpl
	}
	if sc, ok := metadata["$scope"].(string); ok {
		output["$scope"] = sc
	}
	if ver, ok := metadata["$version"].(float64); ok {
		output["$version"] = int(ver)
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// ============================================================================
// HELPER FUNCTIONS
// ============================================================================

// encodeBase64 encodes bytes to base64 string
func encodeBase64(data []byte) string {
	return string(encodeBase64Bytes(data))
}

// encodeBase64Bytes encodes bytes to base64
func encodeBase64Bytes(data []byte) []byte {
	encoded := make([]byte, base64EncodedLen(len(data)))
	base64Encode(encoded, data)
	return encoded
}

// base64EncodedLen returns the length of base64 encoded data
func base64EncodedLen(n int) int {
	return ((n + 2) / 3) * 4
}

// base64Encode encodes src to dst
func base64Encode(dst, src []byte) {
	const base64Std = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	for len(src) > 2 {
		dst[0] = base64Std[src[0]>>2]
		dst[1] = base64Std[((src[0]&0x03)<<4)|(src[1]>>4)]
		dst[2] = base64Std[((src[1]&0x0f)<<2)|(src[2]>>6)]
		dst[3] = base64Std[src[2]&0x3f]
		src = src[3:]
		dst = dst[4:]
	}
	if len(src) == 2 {
		dst[0] = base64Std[src[0]>>2]
		dst[1] = base64Std[((src[0]&0x03)<<4)|(src[1]>>4)]
		dst[2] = base64Std[(src[1]&0x0f)<<2]
		dst[3] = '='
	} else if len(src) == 1 {
		dst[0] = base64Std[src[0]>>2]
		dst[1] = base64Std[(src[0]&0x03)<<4]
		dst[2] = '='
		dst[3] = '='
	}
}

// decodeBase64 decodes a base64 string
func decodeBase64(s string) ([]byte, error) {
	const base64Std = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	// Create decoding map
	decodeMap := make([]int8, 256)
	for i := range decodeMap {
		decodeMap[i] = -1
	}
	for i, c := range base64Std {
		decodeMap[c] = int8(i)
	}
	decodeMap['='] = 0

	// Remove whitespace
	s = strings.TrimSpace(s)
	if len(s)%4 != 0 {
		return nil, fmt.Errorf("invalid base64 length")
	}

	// Count padding
	padding := 0
	if len(s) > 0 && s[len(s)-1] == '=' {
		padding++
	}
	if len(s) > 1 && s[len(s)-2] == '=' {
		padding++
	}

	// Decode
	dst := make([]byte, 0, len(s)/4*3)
	for i := 0; i < len(s); i += 4 {
		v0 := decodeMap[s[i]]
		v1 := decodeMap[s[i+1]]
		v2 := decodeMap[s[i+2]]
		v3 := decodeMap[s[i+3]]

		if v0 < 0 || v1 < 0 || v2 < 0 || v3 < 0 {
			return nil, fmt.Errorf("invalid base64 character")
		}

		dst = append(dst, byte(v0<<2|v1>>4))
		if s[i+2] != '=' {
			dst = append(dst, byte(v1<<4|v2>>2))
		}
		if s[i+3] != '=' {
			dst = append(dst, byte(v2<<6|v3))
		}
	}

	return dst[:len(dst)-padding], nil
}
