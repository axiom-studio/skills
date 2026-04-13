package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/axiom-studio/skills.sdk/executor"
	"github.com/axiom-studio/skills.sdk/grpc"
	"github.com/axiom-studio/skills.sdk/resolver"
)

func main() {
	// Get port from env or use default
	port := os.Getenv("SKILL_PORT")
	if port == "" {
		port = "50053"
	}

	// Create skill server
	server := grpc.NewSkillServer("skill-confluence", "1.0.0")

	// Register all Confluence executors with schemas
	server.RegisterExecutorWithSchema("confluence-page-list", &PageListExecutor{}, PageListSchema)
	server.RegisterExecutorWithSchema("confluence-page-get", &PageGetExecutor{}, PageGetSchema)
	server.RegisterExecutorWithSchema("confluence-page-create", &PageCreateExecutor{}, PageCreateSchema)
	server.RegisterExecutorWithSchema("confluence-page-update", &PageUpdateExecutor{}, PageUpdateSchema)
	server.RegisterExecutorWithSchema("confluence-page-delete", &PageDeleteExecutor{}, PageDeleteSchema)
	server.RegisterExecutorWithSchema("confluence-space-list", &SpaceListExecutor{}, SpaceListSchema)
	server.RegisterExecutorWithSchema("confluence-space-get", &SpaceGetExecutor{}, SpaceGetSchema)
	server.RegisterExecutorWithSchema("confluence-attachment-upload", &AttachmentUploadExecutor{}, AttachmentUploadSchema)
	server.RegisterExecutorWithSchema("confluence-attachment-list", &AttachmentListExecutor{}, AttachmentListSchema)
	server.RegisterExecutorWithSchema("confluence-attachment-delete", &AttachmentDeleteExecutor{}, AttachmentDeleteSchema)
	server.RegisterExecutorWithSchema("confluence-comment-create", &CommentCreateExecutor{}, CommentCreateSchema)
	server.RegisterExecutorWithSchema("confluence-comment-list", &CommentListExecutor{}, CommentListSchema)
	server.RegisterExecutorWithSchema("confluence-search", &SearchExecutor{}, SearchSchema)

	fmt.Printf("Starting skill-confluence gRPC server on port %s\n", port)
	if err := server.Serve(port); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to serve: %v\n", err)
		os.Exit(1)
	}
}

// ConfluenceClient handles API communication with Confluence REST API
type ConfluenceClient struct {
	baseURL    string
	apiToken   string
	email      string
	httpClient *http.Client
}

// NewConfluenceClient creates a new Confluence API client
func NewConfluenceClient(baseURL, apiToken, email string) *ConfluenceClient {
	return &ConfluenceClient{
		baseURL:    strings.TrimSuffix(baseURL, "/"),
		apiToken:   apiToken,
		email:      email,
		httpClient: &http.Client{},
	}
}

// doRequest performs an authenticated HTTP request
func (c *ConfluenceClient) doRequest(ctx context.Context, method, endpoint string, body io.Reader, contentType string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+endpoint, body)
	if err != nil {
		return nil, err
	}

	// Basic auth for Confluence Cloud API
	req.SetBasicAuth(c.email, c.apiToken)
	req.Header.Set("Accept", "application/json")

	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	} else if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	return c.httpClient.Do(req)
}

// doRequestNoAuth performs an HTTP request without authentication (for PAT tokens in header)
func (c *ConfluenceClient) doRequestWithPAT(ctx context.Context, method, endpoint string, body io.Reader, contentType string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+endpoint, body)
	if err != nil {
		return nil, err
	}

	// Bearer token for Personal Access Tokens
	req.Header.Set("Authorization", "Bearer "+c.apiToken)
	req.Header.Set("Accept", "application/json")

	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	} else if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	return c.httpClient.Do(req)
}

// ============================================================================
// confluence-page-list
// ============================================================================

// PageListExecutor handles confluence-page-list node type
type PageListExecutor struct{}

// PageListConfig defines the typed configuration for listing pages
type PageListConfig struct {
	BaseURL      string `json:"baseUrl" description:"Confluence base URL (e.g., https://your-domain.atlassian.net/wiki)"`
	APIToken     string `json:"apiToken" description:"Confluence API token or Personal Access Token"`
	Email        string `json:"email" description:"Confluence account email (not required for PAT)"`
	SpaceKey     string `json:"spaceKey" description:"Space key to filter pages"`
	ParentID     string `json:"parentId" description:"Parent page ID to get child pages"`
	Title        string `json:"title" description:"Filter by page title"`
	Expand       string `json:"expand" description:"Fields to expand (comma-separated, e.g., body.storage,version,space)"`
	Limit        int    `json:"limit" default:"25" description:"Maximum results to return (1-100)"`
	Start        int    `json:"start" default:"0" description:"Starting index for pagination"`
	OrderBy      string `json:"orderBy" default:"title" description:"Order by field (e.g., title, created, modified)"`
}

// PageListSchema is the UI schema for page listing
var PageListSchema = resolver.NewSchemaBuilder("confluence-page-list").
	WithName("List Confluence Pages").
	WithCategory("confluence").
	WithIcon("list").
	WithDescription("List pages in a Confluence space with filtering and pagination").
	AddSection("Connection").
		AddTextField("baseUrl", "Base URL",
			resolver.WithRequired(),
			resolver.WithPlaceholder("https://your-domain.atlassian.net/wiki"),
		).
		AddTextField("apiToken", "API Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("ATATT3xFfGF0..."),
			resolver.WithHint("Generate from Atlassian account settings"),
		).
		AddTextField("email", "Email",
			resolver.WithPlaceholder("user@example.com"),
			resolver.WithHint("Required for API token auth, not for PAT"),
		).
		EndSection().
	AddSection("Filters").
		AddTextField("spaceKey", "Space Key",
			resolver.WithPlaceholder("DEV"),
			resolver.WithHint("Filter by space"),
		).
		AddTextField("parentId", "Parent Page ID",
			resolver.WithPlaceholder("12345678"),
			resolver.WithHint("Get child pages of a specific page"),
		).
		AddTextField("title", "Title Filter",
			resolver.WithPlaceholder("My Page"),
			resolver.WithHint("Filter pages by title"),
		).
		EndSection().
	AddSection("Pagination & Sorting").
		AddTextField("expand", "Expand Fields",
			resolver.WithPlaceholder("body.storage,version,space"),
			resolver.WithHint("Comma-separated fields to include"),
		).
		AddNumberField("limit", "Limit",
			resolver.WithDefault(25),
			resolver.WithMinMax(1, 100),
		).
		AddNumberField("start", "Start Index",
			resolver.WithDefault(0),
		).
		AddTextField("orderBy", "Order By",
			resolver.WithDefault("title"),
			resolver.WithPlaceholder("title, created, modified"),
		).
		EndSection().
	Build()

func (e *PageListExecutor) Type() string { return "confluence-page-list" }

func (e *PageListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg PageListConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	} else {
		return nil, fmt.Errorf("invalid resolver type")
	}

	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("baseUrl is required")
	}
	if cfg.APIToken == "" {
		return nil, fmt.Errorf("apiToken is required")
	}

	client := NewConfluenceClient(cfg.BaseURL, cfg.APIToken, cfg.Email)

	// Build query parameters
	params := url.Values{}
	params.Set("limit", strconv.Itoa(cfg.Limit))
	params.Set("start", strconv.Itoa(cfg.Start))

	if cfg.SpaceKey != "" {
		params.Set("spaceKey", cfg.SpaceKey)
	}
	if cfg.ParentID != "" {
		params.Set("parent", cfg.ParentID)
	}
	if cfg.Title != "" {
		params.Set("title", cfg.Title)
	}
	if cfg.Expand != "" {
		params.Set("expand", cfg.Expand)
	}
	if cfg.OrderBy != "" {
		params.Set("orderby", cfg.OrderBy)
	}

	endpoint := "/rest/api/content?" + params.Encode()

	resp, err := client.doRequest(ctx, "GET", endpoint, nil, "")
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API error (%d): %s", resp.StatusCode, string(body))
	}

	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	pages := []interface{}{}
	if results, ok := result["results"].([]interface{}); ok {
		pages = results
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"pages":   pages,
			"count":   len(pages),
			"size":    result["size"],
			"start":   result["start"],
			"limit":   result["limit"],
			"hasNext": getLinksNext(result),
			"raw":     result,
		},
	}, nil
}

// ============================================================================
// confluence-page-get
// ============================================================================

// PageGetExecutor handles confluence-page-get node type
type PageGetExecutor struct{}

// PageGetConfig defines the typed configuration for getting pages
type PageGetConfig struct {
	BaseURL    string `json:"baseUrl" description:"Confluence base URL"`
	APIToken   string `json:"apiToken" description:"Confluence API token"`
	Email      string `json:"email" description:"Confluence account email"`
	PageID     string `json:"pageId" description:"Specific page ID to retrieve"`
	SpaceKey   string `json:"spaceKey" description:"Space key to search in"`
	Title      string `json:"title" description:"Page title to search for"`
	Expand     string `json:"expand" description:"Fields to expand (comma-separated)"`
	Version    int    `json:"version" description:"Specific version to retrieve (optional)"`
}

// PageGetSchema is the UI schema for getting pages
var PageGetSchema = resolver.NewSchemaBuilder("confluence-page-get").
	WithName("Get Confluence Page").
	WithCategory("confluence").
	WithIcon("file-text").
	WithDescription("Retrieve Confluence page content by ID or search by title").
	AddSection("Connection").
		AddTextField("baseUrl", "Base URL",
			resolver.WithRequired(),
		).
		AddTextField("apiToken", "API Token",
			resolver.WithRequired(),
		).
		AddTextField("email", "Email",
			resolver.WithRequired(),
		).
		EndSection().
	AddSection("Page Lookup").
		AddTextField("pageId", "Page ID",
			resolver.WithPlaceholder("12345678"),
			resolver.WithHint("Provide page ID for direct lookup"),
		).
		AddTextField("spaceKey", "Space Key",
			resolver.WithPlaceholder("DEV"),
			resolver.WithHint("Used with title for searching"),
		).
		AddTextField("title", "Title",
			resolver.WithPlaceholder("My Page"),
			resolver.WithHint("Search by title within space"),
		).
		AddTextField("expand", "Expand Fields",
			resolver.WithPlaceholder("body.storage,version,space,ancestors"),
			resolver.WithHint("Comma-separated fields to include"),
		).
		AddNumberField("version", "Version (Optional)",
			resolver.WithHint("Get specific version of the page"),
		).
		EndSection().
	Build()

func (e *PageGetExecutor) Type() string { return "confluence-page-get" }

func (e *PageGetExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg PageGetConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	} else {
		return nil, fmt.Errorf("invalid resolver type")
	}

	if cfg.BaseURL == "" || cfg.APIToken == "" {
		return nil, fmt.Errorf("connection credentials are required")
	}

	client := NewConfluenceClient(cfg.BaseURL, cfg.APIToken, cfg.Email)

	var endpoint string
	if cfg.PageID != "" {
		// Direct page lookup
		endpoint = fmt.Sprintf("/rest/api/content/%s", cfg.PageID)
		if cfg.Expand != "" {
			endpoint += fmt.Sprintf("?expand=%s", url.QueryEscape(cfg.Expand))
		}
	} else if cfg.SpaceKey != "" && cfg.Title != "" {
		// Search by space and title
		params := url.Values{}
		params.Set("spaceKey", cfg.SpaceKey)
		params.Set("title", cfg.Title)
		params.Set("limit", "1")
		if cfg.Expand != "" {
			params.Set("expand", cfg.Expand)
		}
		endpoint = "/rest/api/content?" + params.Encode()
	} else {
		return nil, fmt.Errorf("either pageId or both spaceKey and title are required")
	}

	resp, err := client.doRequest(ctx, "GET", endpoint, nil, "")
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API error (%d): %s", resp.StatusCode, string(body))
	}

	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	output := map[string]interface{}{
		"raw": result,
	}

	// Handle both single page and search results
	if results, ok := result["results"].([]interface{}); ok {
		if len(results) > 0 {
			output["page"] = results[0]
			output["count"] = len(results)
			output["pages"] = results
		} else {
			output["page"] = nil
			output["count"] = 0
			output["pages"] = []interface{}{}
		}
	} else {
		output["page"] = result
		output["count"] = 1
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// ============================================================================
// confluence-page-create
// ============================================================================

// PageCreateExecutor handles confluence-page-create node type
type PageCreateExecutor struct{}

// PageCreateConfig defines the typed configuration for page creation
type PageCreateConfig struct {
	BaseURL    string `json:"baseUrl" description:"Confluence base URL (e.g., https://your-domain.atlassian.net/wiki)"`
	APIToken   string `json:"apiToken" description:"Confluence API token"`
	Email      string `json:"email" description:"Confluence account email"`
	SpaceKey   string `json:"spaceKey" description:"Space key where page will be created"`
	Title      string `json:"title" description:"Page title"`
	Content    string `json:"content" description:"Page content in Confluence storage format (HTML)"`
	ParentID   string `json:"parentId" description:"Optional parent page ID for nested pages"`
}

// PageCreateSchema is the UI schema for page creation
var PageCreateSchema = resolver.NewSchemaBuilder("confluence-page-create").
	WithName("Create Confluence Page").
	WithCategory("confluence").
	WithIcon("file-plus").
	WithDescription("Create a new page in Confluence").
	AddSection("Connection").
		AddTextField("baseUrl", "Base URL",
			resolver.WithRequired(),
			resolver.WithPlaceholder("https://your-domain.atlassian.net/wiki"),
		).
		AddTextField("apiToken", "API Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("ATATT3xFfGF0..."),
			resolver.WithHint("Generate from Atlassian account settings"),
		).
		AddTextField("email", "Email",
			resolver.WithRequired(),
			resolver.WithPlaceholder("user@example.com"),
		).
		EndSection().
	AddSection("Page Details").
		AddTextField("spaceKey", "Space Key",
			resolver.WithRequired(),
			resolver.WithPlaceholder("DEV"),
		).
		AddTextField("title", "Title",
			resolver.WithRequired(),
			resolver.WithPlaceholder("My New Page"),
		).
		AddCodeField("content", "Content", "html",
			resolver.WithRequired(),
			resolver.WithHeight(200),
			resolver.WithPlaceholder("<h1>Heading</h1><p>Content here...</p>"),
			resolver.WithHint("Use Confluence storage format (HTML)"),
		).
		AddTextField("parentId", "Parent Page ID (Optional)",
			resolver.WithPlaceholder("12345678"),
			resolver.WithHint("Leave empty for top-level page"),
		).
		EndSection().
	Build()

func (e *PageCreateExecutor) Type() string { return "confluence-page-create" }

func (e *PageCreateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg PageCreateConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	} else {
		return nil, fmt.Errorf("invalid resolver type")
	}

	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("baseUrl is required")
	}
	if cfg.APIToken == "" {
		return nil, fmt.Errorf("apiToken is required")
	}
	if cfg.Email == "" {
		return nil, fmt.Errorf("email is required")
	}
	if cfg.SpaceKey == "" {
		return nil, fmt.Errorf("spaceKey is required")
	}
	if cfg.Title == "" {
		return nil, fmt.Errorf("title is required")
	}
	if cfg.Content == "" {
		return nil, fmt.Errorf("content is required")
	}

	client := NewConfluenceClient(cfg.BaseURL, cfg.APIToken, cfg.Email)

	// Build page payload
	pageData := map[string]interface{}{
		"type":  "page",
		"title": cfg.Title,
		"space": map[string]string{"key": cfg.SpaceKey},
		"body": map[string]interface{}{
			"storage": map[string]interface{}{
				"value":          cfg.Content,
				"representation": "storage",
			},
		},
	}

	if cfg.ParentID != "" {
		pageData["ancestors"] = []map[string]string{{"id": cfg.ParentID}}
	}

	jsonData, err := json.Marshal(pageData)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal page data: %w", err)
	}

	resp, err := client.doRequest(ctx, "POST", "/rest/api/content", bytes.NewReader(jsonData), "application/json")
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API error (%d): %s", resp.StatusCode, string(body))
	}

	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	pageID, _ := result["id"].(string)
	pageTitle, _ := result["title"].(string)

	return &executor.StepResult{
		Output: map[string]interface{}{
			"id":      pageID,
			"title":   pageTitle,
			"type":    result["type"],
			"status":  result["status"],
			"space":   result["space"],
			"version": result["version"],
			"links":   result["_links"],
			"self":    buildPageURL(cfg.BaseURL, pageID),
		},
	}, nil
}

// ============================================================================
// confluence-page-update
// ============================================================================

// PageUpdateExecutor handles confluence-page-update node type
type PageUpdateExecutor struct{}

// PageUpdateConfig defines the typed configuration for page update
type PageUpdateConfig struct {
	BaseURL   string `json:"baseUrl" description:"Confluence base URL"`
	APIToken  string `json:"apiToken" description:"Confluence API token"`
	Email     string `json:"email" description:"Confluence account email"`
	PageID    string `json:"pageId" description:"ID of the page to update"`
	Title     string `json:"title" description:"New page title (optional, keeps existing if empty)"`
	Content   string `json:"content" description:"New page content in Confluence storage format"`
	MinorEdit bool   `json:"minorEdit" default:"false" description:"Mark as minor edit"`
	Message   string `json:"message" description:"Optional version message"`
}

// PageUpdateSchema is the UI schema for page update
var PageUpdateSchema = resolver.NewSchemaBuilder("confluence-page-update").
	WithName("Update Confluence Page").
	WithCategory("confluence").
	WithIcon("edit").
	WithDescription("Update an existing Confluence page").
	AddSection("Connection").
		AddTextField("baseUrl", "Base URL",
			resolver.WithRequired(),
		).
		AddTextField("apiToken", "API Token",
			resolver.WithRequired(),
		).
		AddTextField("email", "Email",
			resolver.WithRequired(),
		).
		EndSection().
	AddSection("Page Update").
		AddTextField("pageId", "Page ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("12345678"),
		).
		AddTextField("title", "Title (Optional)",
			resolver.WithPlaceholder("Updated Title"),
			resolver.WithHint("Leave empty to keep existing title"),
		).
		AddCodeField("content", "Content", "html",
			resolver.WithRequired(),
			resolver.WithHeight(200),
		).
		AddToggleField("minorEdit", "Minor Edit",
			resolver.WithDefault(false),
		).
		AddTextField("message", "Version Message (Optional)",
			resolver.WithPlaceholder("Updated documentation"),
		).
		EndSection().
	Build()

func (e *PageUpdateExecutor) Type() string { return "confluence-page-update" }

func (e *PageUpdateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg PageUpdateConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	} else {
		return nil, fmt.Errorf("invalid resolver type")
	}

	if cfg.BaseURL == "" || cfg.APIToken == "" || cfg.Email == "" {
		return nil, fmt.Errorf("connection credentials are required")
	}
	if cfg.PageID == "" {
		return nil, fmt.Errorf("pageId is required")
	}
	if cfg.Content == "" {
		return nil, fmt.Errorf("content is required")
	}

	client := NewConfluenceClient(cfg.BaseURL, cfg.APIToken, cfg.Email)

	// First, get current page to retrieve version number and current title
	resp, err := client.doRequest(ctx, "GET", fmt.Sprintf("/rest/api/content/%s?expand=version", cfg.PageID), nil, "")
	if err != nil {
		return nil, fmt.Errorf("failed to get page: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API error getting page (%d): %s", resp.StatusCode, string(body))
	}

	var page map[string]interface{}
	if err := json.Unmarshal(body, &page); err != nil {
		return nil, fmt.Errorf("failed to parse page: %w", err)
	}

	version, ok := page["version"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("version information not found")
	}
	versionNum := int(version["number"].(float64))

	// Use existing title if not provided
	title := cfg.Title
	if title == "" {
		title, _ = page["title"].(string)
	}

	// Build update payload
	updateData := map[string]interface{}{
		"id":    cfg.PageID,
		"type":  "page",
		"title": title,
		"body": map[string]interface{}{
			"storage": map[string]interface{}{
				"value":          cfg.Content,
				"representation": "storage",
			},
		},
		"version": map[string]interface{}{
			"number":    versionNum + 1,
			"minorEdit": cfg.MinorEdit,
		},
	}

	if cfg.Message != "" {
		updateData["version"].(map[string]interface{})["message"] = cfg.Message
	}

	jsonData, err := json.Marshal(updateData)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal update data: %w", err)
	}

	resp, err = client.doRequest(ctx, "PUT", fmt.Sprintf("/rest/api/content/%s", cfg.PageID), bytes.NewReader(jsonData), "application/json")
	if err != nil {
		return nil, fmt.Errorf("update request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err = io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API error (%d): %s", resp.StatusCode, string(body))
	}

	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	pageID, _ := result["id"].(string)
	pageTitle, _ := result["title"].(string)

	return &executor.StepResult{
		Output: map[string]interface{}{
			"id":      pageID,
			"title":   pageTitle,
			"type":    result["type"],
			"status":  result["status"],
			"version": result["version"],
			"links":   result["_links"],
			"self":    buildPageURL(cfg.BaseURL, pageID),
		},
	}, nil
}

// ============================================================================
// confluence-page-delete
// ============================================================================

// PageDeleteExecutor handles confluence-page-delete node type
type PageDeleteExecutor struct{}

// PageDeleteConfig defines the typed configuration for page deletion
type PageDeleteConfig struct {
	BaseURL  string `json:"baseUrl" description:"Confluence base URL"`
	APIToken string `json:"apiToken" description:"Confluence API token"`
	Email    string `json:"email" description:"Confluence account email"`
	PageID   string `json:"pageId" description:"ID of the page to delete"`
}

// PageDeleteSchema is the UI schema for page deletion
var PageDeleteSchema = resolver.NewSchemaBuilder("confluence-page-delete").
	WithName("Delete Confluence Page").
	WithCategory("confluence").
	WithIcon("trash-2").
	WithDescription("Delete a Confluence page permanently").
	AddSection("Connection").
		AddTextField("baseUrl", "Base URL",
			resolver.WithRequired(),
		).
		AddTextField("apiToken", "API Token",
			resolver.WithRequired(),
		).
		AddTextField("email", "Email",
			resolver.WithRequired(),
		).
		EndSection().
	AddSection("Page Deletion").
		AddTextField("pageId", "Page ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("12345678"),
			resolver.WithHint("ID of the page to delete"),
		).
		EndSection().
	Build()

func (e *PageDeleteExecutor) Type() string { return "confluence-page-delete" }

func (e *PageDeleteExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg PageDeleteConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	} else {
		return nil, fmt.Errorf("invalid resolver type")
	}

	if cfg.BaseURL == "" || cfg.APIToken == "" || cfg.Email == "" {
		return nil, fmt.Errorf("connection credentials are required")
	}
	if cfg.PageID == "" {
		return nil, fmt.Errorf("pageId is required")
	}

	client := NewConfluenceClient(cfg.BaseURL, cfg.APIToken, cfg.Email)

	resp, err := client.doRequest(ctx, "DELETE", fmt.Sprintf("/rest/api/content/%s", cfg.PageID), nil, "")
	if err != nil {
		return nil, fmt.Errorf("delete request failed: %w", err)
	}
	defer resp.Body.Close()

	// Confluence returns 204 No Content on successful deletion
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API error (%d): %s", resp.StatusCode, string(body))
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success": true,
			"message": fmt.Sprintf("Page %s deleted successfully", cfg.PageID),
			"pageId":  cfg.PageID,
		},
	}, nil
}

// ============================================================================
// confluence-space-list
// ============================================================================

// SpaceListExecutor handles confluence-space-list node type
type SpaceListExecutor struct{}

// SpaceListConfig defines the typed configuration for listing spaces
type SpaceListConfig struct {
	BaseURL   string `json:"baseUrl" description:"Confluence base URL"`
	APIToken  string `json:"apiToken" description:"Confluence API token"`
	Email     string `json:"email" description:"Confluence account email"`
	SpaceType string `json:"spaceType" default:"global" description:"Type of spaces to list (global, personal)"`
	Status    string `json:"status" default:"current" description:"Space status (current, archived)"`
	Expand    string `json:"expand" description:"Fields to expand (permissions,roles,icon)"`
	Limit     int    `json:"limit" default:"25" description:"Maximum results to return"`
	Start     int    `json:"start" default:"0" description:"Starting index for pagination"`
}

// SpaceListSchema is the UI schema for space listing
var SpaceListSchema = resolver.NewSchemaBuilder("confluence-space-list").
	WithName("List Confluence Spaces").
	WithCategory("confluence").
	WithIcon("folder").
	WithDescription("List all Confluence spaces with filtering options").
	AddSection("Connection").
		AddTextField("baseUrl", "Base URL",
			resolver.WithRequired(),
		).
		AddTextField("apiToken", "API Token",
			resolver.WithRequired(),
		).
		AddTextField("email", "Email",
			resolver.WithRequired(),
		).
		EndSection().
	AddSection("Filters").
		AddSelectField("spaceType", "Space Type", []resolver.SelectOption{
			{Label: "Global", Value: "global"},
			{Label: "Personal", Value: "personal"},
		}, resolver.WithDefault("global")).
		AddSelectField("status", "Status", []resolver.SelectOption{
			{Label: "Current", Value: "current"},
			{Label: "Archived", Value: "archived"},
		}, resolver.WithDefault("current")).
		EndSection().
	AddSection("Pagination").
		AddTextField("expand", "Expand Fields",
			resolver.WithPlaceholder("permissions,roles,icon"),
		).
		AddNumberField("limit", "Limit",
			resolver.WithDefault(25),
			resolver.WithMinMax(1, 100),
		).
		AddNumberField("start", "Start Index",
			resolver.WithDefault(0),
		).
		EndSection().
	Build()

func (e *SpaceListExecutor) Type() string { return "confluence-space-list" }

func (e *SpaceListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg SpaceListConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	} else {
		return nil, fmt.Errorf("invalid resolver type")
	}

	if cfg.BaseURL == "" || cfg.APIToken == "" {
		return nil, fmt.Errorf("connection credentials are required")
	}

	client := NewConfluenceClient(cfg.BaseURL, cfg.APIToken, cfg.Email)

	// Build query parameters
	params := url.Values{}
	params.Set("type", cfg.SpaceType)
	params.Set("status", cfg.Status)
	params.Set("limit", strconv.Itoa(cfg.Limit))
	params.Set("start", strconv.Itoa(cfg.Start))

	if cfg.Expand != "" {
		params.Set("expand", cfg.Expand)
	}

	endpoint := "/rest/api/space?" + params.Encode()

	resp, err := client.doRequest(ctx, "GET", endpoint, nil, "")
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API error (%d): %s", resp.StatusCode, string(body))
	}

	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	spaces := []interface{}{}
	if results, ok := result["results"].([]interface{}); ok {
		spaces = results
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"spaces":  spaces,
			"count":   len(spaces),
			"size":    result["size"],
			"start":   result["start"],
			"limit":   result["limit"],
			"hasNext": getLinksNext(result),
			"raw":     result,
		},
	}, nil
}

// ============================================================================
// confluence-space-get
// ============================================================================

// SpaceGetExecutor handles confluence-space-get node type
type SpaceGetExecutor struct{}

// SpaceGetConfig defines the typed configuration for getting a space
type SpaceGetConfig struct {
	BaseURL  string `json:"baseUrl" description:"Confluence base URL"`
	APIToken string `json:"apiToken" description:"Confluence API token"`
	Email    string `json:"email" description:"Confluence account email"`
	SpaceKey string `json:"spaceKey" description:"Space key to retrieve"`
	Expand   string `json:"expand" description:"Fields to expand (permissions,roles,icon,metadata)"`
}

// SpaceGetSchema is the UI schema for getting a space
var SpaceGetSchema = resolver.NewSchemaBuilder("confluence-space-get").
	WithName("Get Confluence Space").
	WithCategory("confluence").
	WithIcon("folder-open").
	WithDescription("Get details of a specific Confluence space by key").
	AddSection("Connection").
		AddTextField("baseUrl", "Base URL",
			resolver.WithRequired(),
		).
		AddTextField("apiToken", "API Token",
			resolver.WithRequired(),
		).
		AddTextField("email", "Email",
			resolver.WithRequired(),
		).
		EndSection().
	AddSection("Space Lookup").
		AddTextField("spaceKey", "Space Key",
			resolver.WithRequired(),
			resolver.WithPlaceholder("DEV"),
		).
		AddTextField("expand", "Expand Fields",
			resolver.WithPlaceholder("permissions,roles,icon,metadata"),
		).
		EndSection().
	Build()

func (e *SpaceGetExecutor) Type() string { return "confluence-space-get" }

func (e *SpaceGetExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg SpaceGetConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	} else {
		return nil, fmt.Errorf("invalid resolver type")
	}

	if cfg.BaseURL == "" || cfg.APIToken == "" || cfg.Email == "" {
		return nil, fmt.Errorf("connection credentials are required")
	}
	if cfg.SpaceKey == "" {
		return nil, fmt.Errorf("spaceKey is required")
	}

	client := NewConfluenceClient(cfg.BaseURL, cfg.APIToken, cfg.Email)

	endpoint := fmt.Sprintf("/rest/api/space/%s", cfg.SpaceKey)
	if cfg.Expand != "" {
		endpoint += fmt.Sprintf("?expand=%s", url.QueryEscape(cfg.Expand))
	}

	resp, err := client.doRequest(ctx, "GET", endpoint, nil, "")
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API error (%d): %s", resp.StatusCode, string(body))
	}

	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	spaceKey, _ := result["key"].(string)
	spaceName, _ := result["name"].(string)

	return &executor.StepResult{
		Output: map[string]interface{}{
			"key":         spaceKey,
			"name":        spaceName,
			"type":        result["type"],
			"status":      result["status"],
			"_links":      result["_links"],
			"permissions": result["permissions"],
			"roles":       result["roles"],
			"icon":        result["icon"],
			"metadata":    result["metadata"],
			"raw":         result,
		},
	}, nil
}

// ============================================================================
// confluence-attachment-upload
// ============================================================================

// AttachmentUploadExecutor handles confluence-attachment-upload node type
type AttachmentUploadExecutor struct{}

// AttachmentUploadConfig defines the typed configuration for attachment upload
type AttachmentUploadConfig struct {
	BaseURL     string `json:"baseUrl" description:"Confluence base URL"`
	APIToken    string `json:"apiToken" description:"Confluence API token"`
	Email       string `json:"email" description:"Confluence account email"`
	PageID      string `json:"pageId" description:"Page ID to attach the file to"`
	FileName    string `json:"fileName" description:"Name of the file"`
	FileContent string `json:"fileContent" description:"Base64 encoded file content"`
	Comment     string `json:"comment" description:"Attachment comment (optional)"`
}

// AttachmentUploadSchema is the UI schema for attachment upload
var AttachmentUploadSchema = resolver.NewSchemaBuilder("confluence-attachment-upload").
	WithName("Upload Confluence Attachment").
	WithCategory("confluence").
	WithIcon("upload-cloud").
	WithDescription("Upload a file attachment to a Confluence page").
	AddSection("Connection").
		AddTextField("baseUrl", "Base URL",
			resolver.WithRequired(),
		).
		AddTextField("apiToken", "API Token",
			resolver.WithRequired(),
		).
		AddTextField("email", "Email",
			resolver.WithRequired(),
		).
		EndSection().
	AddSection("Attachment Details").
		AddTextField("pageId", "Page ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("12345678"),
		).
		AddTextField("fileName", "File Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("document.pdf"),
		).
		AddTextareaField("fileContent", "File Content (Base64)",
			resolver.WithRequired(),
			resolver.WithRows(10),
			resolver.WithHint("Base64 encoded file content"),
		).
		AddTextField("comment", "Comment (Optional)",
			resolver.WithPlaceholder("Uploaded via API"),
		).
		EndSection().
	Build()

func (e *AttachmentUploadExecutor) Type() string { return "confluence-attachment-upload" }

func (e *AttachmentUploadExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg AttachmentUploadConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	} else {
		return nil, fmt.Errorf("invalid resolver type")
	}

	if cfg.BaseURL == "" || cfg.APIToken == "" || cfg.Email == "" {
		return nil, fmt.Errorf("connection credentials are required")
	}
	if cfg.PageID == "" {
		return nil, fmt.Errorf("pageId is required")
	}
	if cfg.FileName == "" {
		return nil, fmt.Errorf("fileName is required")
	}
	if cfg.FileContent == "" {
		return nil, fmt.Errorf("fileContent is required")
	}

	client := NewConfluenceClient(cfg.BaseURL, cfg.APIToken, cfg.Email)

	// Decode base64 content
	fileData, err := base64.StdEncoding.DecodeString(cfg.FileContent)
	if err != nil {
		// If not base64, use as-is
		fileData = []byte(cfg.FileContent)
	}

	// Create multipart form
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	// Add file part
	part, err := writer.CreateFormFile("file", cfg.FileName)
	if err != nil {
		return nil, fmt.Errorf("failed to create form file: %w", err)
	}
	if _, err := part.Write(fileData); err != nil {
		return nil, fmt.Errorf("failed to write file data: %w", err)
	}

	// Add comment if provided
	if cfg.Comment != "" {
		if err := writer.WriteField("comment", cfg.Comment); err != nil {
			return nil, fmt.Errorf("failed to write comment: %w", err)
		}
	}

	writer.Close()

	// Create request
	endpoint := fmt.Sprintf("/rest/api/content/%s/child/attachment", cfg.PageID)
	req, err := http.NewRequestWithContext(ctx, "POST", client.baseURL+endpoint, body)
	if err != nil {
		return nil, err
	}

	req.SetBasicAuth(client.email, client.apiToken)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Atlassian-Token", "no-check")
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := client.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("upload request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API error (%d): %s", resp.StatusCode, string(respBody))
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Get attachment details
	var attachmentID string
	var downloadLink string
	if results, ok := result["results"].([]interface{}); ok && len(results) > 0 {
		if att, ok := results[0].(map[string]interface{}); ok {
			attachmentID, _ = att["id"].(string)
			if links, ok := att["_links"].(map[string]interface{}); ok {
				downloadLink, _ = links["download"].(string)
			}
		}
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"attachment":   result,
			"attachmentId": attachmentID,
			"fileName":     cfg.FileName,
			"downloadLink": downloadLink,
			"message":      "Attachment uploaded successfully",
		},
	}, nil
}

// ============================================================================
// confluence-attachment-list
// ============================================================================

// AttachmentListExecutor handles confluence-attachment-list node type
type AttachmentListExecutor struct{}

// AttachmentListConfig defines the typed configuration for listing attachments
type AttachmentListConfig struct {
	BaseURL  string `json:"baseUrl" description:"Confluence base URL"`
	APIToken string `json:"apiToken" description:"Confluence API token"`
	Email    string `json:"email" description:"Confluence account email"`
	PageID   string `json:"pageId" description:"Page ID to list attachments for"`
	Expand   string `json:"expand" description:"Fields to expand (version,container)"`
	Limit    int    `json:"limit" default:"25" description:"Maximum results to return"`
}

// AttachmentListSchema is the UI schema for attachment listing
var AttachmentListSchema = resolver.NewSchemaBuilder("confluence-attachment-list").
	WithName("List Confluence Attachments").
	WithCategory("confluence").
	WithIcon("paperclip").
	WithDescription("List all attachments on a Confluence page").
	AddSection("Connection").
		AddTextField("baseUrl", "Base URL",
			resolver.WithRequired(),
		).
		AddTextField("apiToken", "API Token",
			resolver.WithRequired(),
		).
		AddTextField("email", "Email",
			resolver.WithRequired(),
		).
		EndSection().
	AddSection("Attachment List").
		AddTextField("pageId", "Page ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("12345678"),
		).
		AddTextField("expand", "Expand Fields",
			resolver.WithPlaceholder("version,container"),
		).
		AddNumberField("limit", "Limit",
			resolver.WithDefault(25),
			resolver.WithMinMax(1, 100),
		).
		EndSection().
	Build()

func (e *AttachmentListExecutor) Type() string { return "confluence-attachment-list" }

func (e *AttachmentListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg AttachmentListConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	} else {
		return nil, fmt.Errorf("invalid resolver type")
	}

	if cfg.BaseURL == "" || cfg.APIToken == "" || cfg.Email == "" {
		return nil, fmt.Errorf("connection credentials are required")
	}
	if cfg.PageID == "" {
		return nil, fmt.Errorf("pageId is required")
	}

	client := NewConfluenceClient(cfg.BaseURL, cfg.APIToken, cfg.Email)

	endpoint := fmt.Sprintf("/rest/api/content/%s/child/attachment?limit=%d", cfg.PageID, cfg.Limit)
	if cfg.Expand != "" {
		endpoint += fmt.Sprintf("&expand=%s", url.QueryEscape(cfg.Expand))
	}

	resp, err := client.doRequest(ctx, "GET", endpoint, nil, "")
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API error (%d): %s", resp.StatusCode, string(body))
	}

	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	attachments := []interface{}{}
	if results, ok := result["results"].([]interface{}); ok {
		attachments = results
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"attachments": attachments,
			"count":       len(attachments),
			"size":        result["size"],
			"raw":         result,
		},
	}, nil
}

// ============================================================================
// confluence-attachment-delete
// ============================================================================

// AttachmentDeleteExecutor handles confluence-attachment-delete node type
type AttachmentDeleteExecutor struct{}

// AttachmentDeleteConfig defines the typed configuration for attachment deletion
type AttachmentDeleteConfig struct {
	BaseURL      string `json:"baseUrl" description:"Confluence base URL"`
	APIToken     string `json:"apiToken" description:"Confluence API token"`
	Email        string `json:"email" description:"Confluence account email"`
	AttachmentID string `json:"attachmentId" description:"Attachment ID to delete (e.g., att123456)"`
}

// AttachmentDeleteSchema is the UI schema for attachment deletion
var AttachmentDeleteSchema = resolver.NewSchemaBuilder("confluence-attachment-delete").
	WithName("Delete Confluence Attachment").
	WithCategory("confluence").
	WithIcon("trash").
	WithDescription("Delete an attachment from a Confluence page").
	AddSection("Connection").
		AddTextField("baseUrl", "Base URL",
			resolver.WithRequired(),
		).
		AddTextField("apiToken", "API Token",
			resolver.WithRequired(),
		).
		AddTextField("email", "Email",
			resolver.WithRequired(),
		).
		EndSection().
	AddSection("Attachment Deletion").
		AddTextField("attachmentId", "Attachment ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("att123456"),
			resolver.WithHint("ID of the attachment to delete"),
		).
		EndSection().
	Build()

func (e *AttachmentDeleteExecutor) Type() string { return "confluence-attachment-delete" }

func (e *AttachmentDeleteExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg AttachmentDeleteConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	} else {
		return nil, fmt.Errorf("invalid resolver type")
	}

	if cfg.BaseURL == "" || cfg.APIToken == "" || cfg.Email == "" {
		return nil, fmt.Errorf("connection credentials are required")
	}
	if cfg.AttachmentID == "" {
		return nil, fmt.Errorf("attachmentId is required")
	}

	client := NewConfluenceClient(cfg.BaseURL, cfg.APIToken, cfg.Email)

	endpoint := fmt.Sprintf("/rest/api/content/%s", cfg.AttachmentID)

	resp, err := client.doRequest(ctx, "DELETE", endpoint, nil, "")
	if err != nil {
		return nil, fmt.Errorf("delete request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API error (%d): %s", resp.StatusCode, string(body))
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":      true,
			"message":      fmt.Sprintf("Attachment %s deleted successfully", cfg.AttachmentID),
			"attachmentId": cfg.AttachmentID,
		},
	}, nil
}

// ============================================================================
// confluence-comment-create
// ============================================================================

// CommentCreateExecutor handles confluence-comment-create node type
type CommentCreateExecutor struct{}

// CommentCreateConfig defines the typed configuration for comment creation
type CommentCreateConfig struct {
	BaseURL   string `json:"baseUrl" description:"Confluence base URL"`
	APIToken  string `json:"apiToken" description:"Confluence API token"`
	Email     string `json:"email" description:"Confluence account email"`
	PageID    string `json:"pageId" description:"Page ID to add comment to"`
	Content   string `json:"content" description:"Comment content in Confluence storage format (HTML)"`
}

// CommentCreateSchema is the UI schema for comment creation
var CommentCreateSchema = resolver.NewSchemaBuilder("confluence-comment-create").
	WithName("Create Confluence Comment").
	WithCategory("confluence").
	WithIcon("message-square").
	WithDescription("Add a comment to a Confluence page").
	AddSection("Connection").
		AddTextField("baseUrl", "Base URL",
			resolver.WithRequired(),
		).
		AddTextField("apiToken", "API Token",
			resolver.WithRequired(),
		).
		AddTextField("email", "Email",
			resolver.WithRequired(),
		).
		EndSection().
	AddSection("Comment Details").
		AddTextField("pageId", "Page ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("12345678"),
		).
		AddCodeField("content", "Comment Content", "html",
			resolver.WithRequired(),
			resolver.WithHeight(100),
			resolver.WithPlaceholder("<p>This is my comment...</p>"),
			resolver.WithHint("Use Confluence storage format (HTML)"),
		).
		EndSection().
	Build()

func (e *CommentCreateExecutor) Type() string { return "confluence-comment-create" }

func (e *CommentCreateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg CommentCreateConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	} else {
		return nil, fmt.Errorf("invalid resolver type")
	}

	if cfg.BaseURL == "" || cfg.APIToken == "" || cfg.Email == "" {
		return nil, fmt.Errorf("connection credentials are required")
	}
	if cfg.PageID == "" {
		return nil, fmt.Errorf("pageId is required")
	}
	if cfg.Content == "" {
		return nil, fmt.Errorf("content is required")
	}

	client := NewConfluenceClient(cfg.BaseURL, cfg.APIToken, cfg.Email)

	// Build comment payload
	commentData := map[string]interface{}{
		"type": "comment",
		"container": map[string]string{
			"id": cfg.PageID,
		},
		"body": map[string]interface{}{
			"storage": map[string]interface{}{
				"value":          cfg.Content,
				"representation": "storage",
			},
		},
	}

	jsonData, err := json.Marshal(commentData)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal comment data: %w", err)
	}

	resp, err := client.doRequest(ctx, "POST", "/rest/api/content", bytes.NewReader(jsonData), "application/json")
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API error (%d): %s", resp.StatusCode, string(body))
	}

	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	commentID, _ := result["id"].(string)

	return &executor.StepResult{
		Output: map[string]interface{}{
			"id":        commentID,
			"type":      result["type"],
			"status":    result["status"],
			"container": result["container"],
			"body":      result["body"],
			"version":   result["version"],
			"links":     result["_links"],
			"raw":       result,
		},
	}, nil
}

// ============================================================================
// confluence-comment-list
// ============================================================================

// CommentListExecutor handles confluence-comment-list node type
type CommentListExecutor struct{}

// CommentListConfig defines the typed configuration for listing comments
type CommentListConfig struct {
	BaseURL  string `json:"baseUrl" description:"Confluence base URL"`
	APIToken string `json:"apiToken" description:"Confluence API token"`
	Email    string `json:"email" description:"Confluence account email"`
	PageID   string `json:"pageId" description:"Page ID to list comments for"`
	Expand   string `json:"expand" description:"Fields to expand (body.storage,version,container)"`
	Limit    int    `json:"limit" default:"25" description:"Maximum results to return"`
}

// CommentListSchema is the UI schema for comment listing
var CommentListSchema = resolver.NewSchemaBuilder("confluence-comment-list").
	WithName("List Confluence Comments").
	WithCategory("confluence").
	WithIcon("message-circle").
	WithDescription("List all comments on a Confluence page").
	AddSection("Connection").
		AddTextField("baseUrl", "Base URL",
			resolver.WithRequired(),
		).
		AddTextField("apiToken", "API Token",
			resolver.WithRequired(),
		).
		AddTextField("email", "Email",
			resolver.WithRequired(),
		).
		EndSection().
	AddSection("Comment List").
		AddTextField("pageId", "Page ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("12345678"),
		).
		AddTextField("expand", "Expand Fields",
			resolver.WithPlaceholder("body.storage,version,container"),
		).
		AddNumberField("limit", "Limit",
			resolver.WithDefault(25),
			resolver.WithMinMax(1, 100),
		).
		EndSection().
	Build()

func (e *CommentListExecutor) Type() string { return "confluence-comment-list" }

func (e *CommentListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg CommentListConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	} else {
		return nil, fmt.Errorf("invalid resolver type")
	}

	if cfg.BaseURL == "" || cfg.APIToken == "" || cfg.Email == "" {
		return nil, fmt.Errorf("connection credentials are required")
	}
	if cfg.PageID == "" {
		return nil, fmt.Errorf("pageId is required")
	}

	client := NewConfluenceClient(cfg.BaseURL, cfg.APIToken, cfg.Email)

	endpoint := fmt.Sprintf("/rest/api/content/%s/child/comment?limit=%d", cfg.PageID, cfg.Limit)
	if cfg.Expand != "" {
		endpoint += fmt.Sprintf("&expand=%s", url.QueryEscape(cfg.Expand))
	}

	resp, err := client.doRequest(ctx, "GET", endpoint, nil, "")
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API error (%d): %s", resp.StatusCode, string(body))
	}

	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	comments := []interface{}{}
	if results, ok := result["results"].([]interface{}); ok {
		comments = results
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"comments": comments,
			"count":    len(comments),
			"size":     result["size"],
			"raw":      result,
		},
	}, nil
}

// ============================================================================
// confluence-search
// ============================================================================

// SearchExecutor handles confluence-search node type
type SearchExecutor struct{}

// SearchConfig defines the typed configuration for search operations
type SearchConfig struct {
	BaseURL    string `json:"baseUrl" description:"Confluence base URL"`
	APIToken   string `json:"apiToken" description:"Confluence API token"`
	Email      string `json:"email" description:"Confluence account email"`
	Query      string `json:"query" description:"CQL search query (e.g., 'type=page AND space=DEV')"`
	Expand     string `json:"expand" description:"Fields to expand (body.storage,version,space)"`
	Limit      int    `json:"limit" default:"25" description:"Maximum results to return"`
	Start      int    `json:"start" default:"0" description:"Starting index for pagination"`
	IncludeArchived bool `json:"includeArchived" default:"false" description:"Include archived content"`
}

// SearchSchema is the UI schema for search operations
var SearchSchema = resolver.NewSchemaBuilder("confluence-search").
	WithName("Search Confluence").
	WithCategory("confluence").
	WithIcon("search").
	WithDescription("Search Confluence content using CQL (Confluence Query Language)").
	AddSection("Connection").
		AddTextField("baseUrl", "Base URL",
			resolver.WithRequired(),
		).
		AddTextField("apiToken", "API Token",
			resolver.WithRequired(),
		).
		AddTextField("email", "Email",
			resolver.WithRequired(),
		).
		EndSection().
	AddSection("Search Query").
		AddTextField("query", "CQL Query",
			resolver.WithRequired(),
			resolver.WithPlaceholder("type=page AND space=DEV AND text ~ 'documentation'"),
			resolver.WithHint("Use Confluence Query Language (CQL)"),
		).
		AddTextField("expand", "Expand Fields",
			resolver.WithPlaceholder("body.storage,version,space"),
		).
		AddNumberField("limit", "Limit",
			resolver.WithDefault(25),
			resolver.WithMinMax(1, 100),
		).
		AddNumberField("start", "Start Index",
			resolver.WithDefault(0),
		).
		AddToggleField("includeArchived", "Include Archived",
			resolver.WithDefault(false),
		).
		EndSection().
	Build()

func (e *SearchExecutor) Type() string { return "confluence-search" }

func (e *SearchExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg SearchConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	} else {
		return nil, fmt.Errorf("invalid resolver type")
	}

	if cfg.BaseURL == "" || cfg.APIToken == "" || cfg.Email == "" {
		return nil, fmt.Errorf("connection credentials are required")
	}
	if cfg.Query == "" {
		return nil, fmt.Errorf("query is required")
	}

	client := NewConfluenceClient(cfg.BaseURL, cfg.APIToken, cfg.Email)

	// Build query parameters
	params := url.Values{}
	params.Set("cql", cfg.Query)
	params.Set("limit", strconv.Itoa(cfg.Limit))
	params.Set("start", strconv.Itoa(cfg.Start))

	if cfg.Expand != "" {
		params.Set("expand", cfg.Expand)
	}
	if cfg.IncludeArchived {
		params.Set("includeArchived", "true")
	}

	endpoint := "/rest/api/search?" + params.Encode()

	resp, err := client.doRequest(ctx, "GET", endpoint, nil, "")
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API error (%d): %s", resp.StatusCode, string(body))
	}

	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	results := []interface{}{}
	if searchResults, ok := result["results"].([]interface{}); ok {
		results = searchResults
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"results":   results,
			"count":     len(results),
			"size":      result["size"],
			"start":     result["start"],
			"limit":     result["limit"],
			"totalSize": result["totalSize"],
			"cqlQuery":  result["cqlQuery"],
			"raw":       result,
		},
	}, nil
}

// ============================================================================
// Helper Functions
// ============================================================================

// getLinksNext safely checks if there's a next page link in the response
func getLinksNext(result map[string]interface{}) bool {
	links, ok := result["_links"].(map[string]interface{})
	if !ok {
		return false
	}
	_, exists := links["next"]
	return exists
}

// buildPageURL constructs a human-readable Confluence page URL
func buildPageURL(baseURL, pageID string) string {
	baseURL = strings.TrimSuffix(baseURL, "/")
	// Remove /wiki suffix if present for URL construction
	baseURL = strings.TrimSuffix(baseURL, "/wiki")
	return fmt.Sprintf("%s/spaces/allpages/pages/%s", baseURL, pageID)
}
