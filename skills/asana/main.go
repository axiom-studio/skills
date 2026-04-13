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
	"sync"

	"github.com/axiom-studio/skills.sdk/executor"
	"github.com/axiom-studio/skills.sdk/grpc"
	"github.com/axiom-studio/skills.sdk/resolver"
)

// Asana API configuration
const (
	AsanaAPIBase = "https://app.asana.com/api/1.0"
)

// Asana client cache
var (
	clients     = make(map[string]*AsanaClient)
	clientMutex sync.RWMutex
)

// AsanaClient represents an Asana API client
type AsanaClient struct {
	APIToken string
}

// ============================================================================
// ASANA API RESPONSE TYPES
// ============================================================================

// AsanaTask represents an Asana task
type AsanaTask struct {
	GID           string            `json:"gid,omitempty"`
	Name          string            `json:"name,omitempty"`
	Notes         string            `json:"notes,omitempty"`
	Completed     bool              `json:"completed,omitempty"`
	Assignee      *AsanaUser        `json:"assignee,omitempty"`
	AssigneeStatus string           `json:"assignee_status,omitempty"`
	CompletedAt   string            `json:"completed_at,omitempty"`
	CreatedAt     string            `json:"created_at,omitempty"`
	ModifiedAt    string            `json:"modified_at,omitempty"`
	DueOn         string            `json:"due_on,omitempty"`
	DueAt         string            `json:"due_at,omitempty"`
	Projects      []AsanaProject    `json:"projects,omitempty"`
	Memberships   []AsanaMembership `json:"memberships,omitempty"`
	Tags          []AsanaTag        `json:"tags,omitempty"`
	Parent        *AsanaTask        `json:"parent,omitempty"`
	Followers     []AsanaUser       `json:"followers,omitempty"`
	Hearted       bool              `json:"hearted,omitempty"`
	Hearts        []AsanaHeart      `json:"hearts,omitempty"`
	NumHearts     int64             `json:"num_hearts,omitempty"`
	NumLikes      int64             `json:"num_likes,omitempty"`
	Liked         bool              `json:"liked,omitempty"`
	Likes         []AsanaLike       `json:"likes,omitempty"`
	ResourceType  string            `json:"resource_type,omitempty"`
}

// AsanaProject represents an Asana project
type AsanaProject struct {
	GID          string     `json:"gid,omitempty"`
	Name         string     `json:"name,omitempty"`
	Notes        string     `json:"notes,omitempty"`
	Color        string     `json:"color,omitempty"`
	CreatedAt    string     `json:"created_at,omitempty"`
	ModifiedAt   string     `json:"modified_at,omitempty"`
	DueDate      string     `json:"due_date,omitempty"`
	DueOn        string     `json:"due_on,omitempty"`
	CurrentStatus  *AsanaProjectStatus `json:"current_status,omitempty"`
	CustomFields []AsanaCustomField `json:"custom_fields,omitempty"`
	Followers    []AsanaUser  `json:"followers,omitempty"`
	Icon         string       `json:"icon,omitempty"`
	Owner        *AsanaUser   `json:"owner,omitempty"`
	Team         *AsanaTeam   `json:"team,omitempty"`
	Workspace    *AsanaWorkspace `json:"workspace,omitempty"`
	Archived     bool         `json:"archived,omitempty"`
	Public       bool         `json:"public,omitempty"`
	ResourceType string       `json:"resource_type,omitempty"`
}

// AsanaProjectStatus represents a project status
type AsanaProjectStatus struct {
	GID          string `json:"gid,omitempty"`
	Title        string `json:"title,omitempty"`
	Text         string `json:"text,omitempty"`
	Color        string `json:"color,omitempty"`
	CreatedAt    string `json:"created_at,omitempty"`
	CreatedBy    *AsanaUser `json:"created_by,omitempty"`
	ResourceType string `json:"resource_type,omitempty"`
}

// AsanaSection represents an Asana section
type AsanaSection struct {
	GID          string `json:"gid,omitempty"`
	Name         string `json:"name,omitempty"`
	CreatedAt    string `json:"created_at,omitempty"`
	Project      *AsanaProject `json:"project,omitempty"`
	ResourceType string `json:"resource_type,omitempty"`
}

// AsanaStory represents an Asana story (comment/activity)
type AsanaStory struct {
	GID          string     `json:"gid,omitempty"`
	CreatedAt    string     `json:"created_at,omitempty"`
	CreatedBy    *AsanaUser `json:"created_by,omitempty"`
	ResourceType string     `json:"resource_type,omitempty"`
	Text         string     `json:"text,omitempty"`
	HTMLText     string     `json:"html_text,omitempty"`
	IsEdited     bool       `json:"is_edited,omitempty"`
	IsPinned     bool       `json:"is_pinned,omitempty"`
	NumLikes     int64      `json:"num_likes,omitempty"`
	Liked        bool       `json:"liked,omitempty"`
	Likes        []AsanaLike `json:"likes,omitempty"`
	Target       *AsanaTask `json:"target,omitempty"`
	Source       string     `json:"source,omitempty"`
	Type         string     `json:"type,omitempty"`
}

// AsanaTag represents an Asana tag
type AsanaTag struct {
	GID          string `json:"gid,omitempty"`
	Name         string `json:"name,omitempty"`
	Color        string `json:"color,omitempty"`
	Notes        string `json:"notes,omitempty"`
	CreatedAt    string `json:"created_at,omitempty"`
	Followers    []AsanaUser `json:"followers,omitempty"`
	Workspace    *AsanaWorkspace `json:"workspace,omitempty"`
	PermalinkURL string `json:"permalink_url,omitempty"`
	ResourceType string `json:"resource_type,omitempty"`
}

// AsanaUser represents an Asana user
type AsanaUser struct {
	GID          string `json:"gid,omitempty"`
	Name         string `json:"name,omitempty"`
	Email        string `json:"email,omitempty"`
	Photo        *AsanaPhoto `json:"photo,omitempty"`
	ResourceType string `json:"resource_type,omitempty"`
}

// AsanaPhoto represents a user photo
type AsanaPhoto struct {
	Image21x21  string `json:"image_21x21,omitempty"`
	Image27x27  string `json:"image_27x27,omitempty"`
	Image36x36  string `json:"image_36x36,omitempty"`
	Image60x60  string `json:"image_60x60,omitempty"`
	Image128x128 string `json:"image_128x128,omitempty"`
}

// AsanaWorkspace represents an Asana workspace
type AsanaWorkspace struct {
	GID          string `json:"gid,omitempty"`
	Name         string `json:"name,omitempty"`
	ResourceType string `json:"resource_type,omitempty"`
}

// AsanaTeam represents an Asana team
type AsanaTeam struct {
	GID          string `json:"gid,omitempty"`
	Name         string `json:"name,omitempty"`
	ResourceType string `json:"resource_type,omitempty"`
}

// AsanaMembership represents a task's membership in a project/section
type AsanaMembership struct {
	Project  *AsanaProject `json:"project,omitempty"`
	Section  *AsanaSection `json:"section,omitempty"`
}

// AsanaCustomField represents a custom field value
type AsanaCustomField struct {
	GID          string      `json:"gid,omitempty"`
	Name         string      `json:"name,omitempty"`
	Type         string      `json:"type,omitempty"`
	TextValue    string      `json:"text_value,omitempty"`
	NumberValue  json.Number `json:"number_value,omitempty"`
	BoolValue    bool        `json:"bool_value,omitempty"`
	EnumValue    *AsanaEnumValue `json:"enum_value,omitempty"`
	EnumOptions  []AsanaEnumOption `json:"enum_options,omitempty"`
	MultiEnumValues []AsanaEnumValue `json:"multi_enum_values,omitempty"`
	ResourceType string      `json:"resource_type,omitempty"`
}

// AsanaEnumValue represents an enum value
type AsanaEnumValue struct {
	GID          string `json:"gid,omitempty"`
	Name         string `json:"name,omitempty"`
	Enabled      bool   `json:"enabled,omitempty"`
	Color        string `json:"color,omitempty"`
	ResourceType string `json:"resource_type,omitempty"`
}

// AsanaEnumOption represents an enum option
type AsanaEnumOption struct {
	GID          string `json:"gid,omitempty"`
	Name         string `json:"name,omitempty"`
	Enabled      bool   `json:"enabled,omitempty"`
	Color        string `json:"color,omitempty"`
	ResourceType string `json:"resource_type,omitempty"`
}

// AsanaHeart represents a heart reaction
type AsanaHeart struct {
	GID     string     `json:"gid,omitempty"`
	User    *AsanaUser `json:"user,omitempty"`
	CreatedAt string   `json:"created_at,omitempty"`
}

// AsanaLike represents a like reaction
type AsanaLike struct {
	GID     string     `json:"gid,omitempty"`
	User    *AsanaUser `json:"user,omitempty"`
	CreatedAt string   `json:"created_at,omitempty"`
}

// AsanaListResponse represents a paginated list response
type AsanaListResponse struct {
	Data     []json.RawMessage `json:"data"`
	NextPage *AsanaNextPage    `json:"next_page,omitempty"`
}

// AsanaNextPage represents pagination info
type AsanaNextPage struct {
	Offset string `json:"offset,omitempty"`
	Path   string `json:"path,omitempty"`
}

// AsanaSingleResponse represents a single resource response
type AsanaSingleResponse struct {
	Data json.RawMessage `json:"data"`
}

func main() {
	// Get port from env or use default
	port := os.Getenv("SKILL_PORT")
	if port == "" {
		port = "50105"
	}

	// Create skill server
	server := grpc.NewSkillServer("skill-asana", "1.0.0")

	// Register Asana executors with schemas
	server.RegisterExecutorWithSchema("asana-task-list", &AsanaTaskListExecutor{}, AsanaTaskListSchema)
	server.RegisterExecutorWithSchema("asana-task-create", &AsanaTaskCreateExecutor{}, AsanaTaskCreateSchema)
	server.RegisterExecutorWithSchema("asana-task-update", &AsanaTaskUpdateExecutor{}, AsanaTaskUpdateSchema)
	server.RegisterExecutorWithSchema("asana-task-delete", &AsanaTaskDeleteExecutor{}, AsanaTaskDeleteSchema)
	server.RegisterExecutorWithSchema("asana-project-list", &AsanaProjectListExecutor{}, AsanaProjectListSchema)
	server.RegisterExecutorWithSchema("asana-project-get", &AsanaProjectGetExecutor{}, AsanaProjectGetSchema)
	server.RegisterExecutorWithSchema("asana-section-list", &AsanaSectionListExecutor{}, AsanaSectionListSchema)
	server.RegisterExecutorWithSchema("asana-story-create", &AsanaStoryCreateExecutor{}, AsanaStoryCreateSchema)
	server.RegisterExecutorWithSchema("asana-subtask-list", &AsanaSubtaskListExecutor{}, AsanaSubtaskListSchema)
	server.RegisterExecutorWithSchema("asana-tag-list", &AsanaTagListExecutor{}, AsanaTagListSchema)

	fmt.Printf("Starting skill-asana gRPC server on port %s\n", port)
	if err := server.Serve(port); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to serve: %v\n", err)
		os.Exit(1)
	}
}

// getClient returns or creates an Asana client (cached)
func getClient(apiToken string) *AsanaClient {
	clientMutex.RLock()
	client, ok := clients[apiToken]
	clientMutex.RUnlock()

	if ok {
		return client
	}

	clientMutex.Lock()
	defer clientMutex.Unlock()

	// Double check
	if client, ok := clients[apiToken]; ok {
		return client
	}

	client = &AsanaClient{APIToken: apiToken}
	clients[apiToken] = client
	return client
}

// doRequest performs an HTTP request to the Asana API
func (c *AsanaClient) doRequest(ctx context.Context, method, path string, body interface{}) (*http.Response, error) {
	var reqBody io.Reader
	if body != nil {
		jsonData, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
		reqBody = bytes.NewBuffer(jsonData)
	}

	reqURL := AsanaAPIBase + path
	req, err := http.NewRequestWithContext(ctx, method, reqURL, reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.APIToken))
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	return resp, nil
}

// decodeResponse decodes an Asana API response
func decodeResponse(resp *http.Response, result interface{}) error {
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var errResp map[string]interface{}
		if err := json.Unmarshal(body, &errResp); err == nil {
			if msg, ok := errResp["errors"].([]interface{}); ok && len(msg) > 0 {
				if errMap, ok := msg[0].(map[string]interface{}); ok {
					return fmt.Errorf("Asana API error (%d): %v", resp.StatusCode, errMap["message"])
				}
			}
		}
		return fmt.Errorf("Asana API error (%d): %s", resp.StatusCode, string(body))
	}

	if err := json.Unmarshal(body, result); err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
	}

	return nil
}

// ============================================================================
// ASANA-TASK-LIST
// ============================================================================

// AsanaTaskListConfig defines the configuration for asana-task-list
type AsanaTaskListConfig struct {
	APIToken     string   `json:"apiToken" description:"Asana Personal Access Token"`
	Project      string   `json:"project" description:"Project ID to list tasks from"`
	Section      string   `json:"section" description:"Optional section ID to filter tasks"`
	Assignee     string   `json:"assignee" description:"Optional assignee ID to filter tasks"`
	Completed    bool     `json:"completed" default:"false" description:"Filter by completion status"`
	Workspace    string   `json:"workspace" description:"Optional workspace ID"`
	Limit        int      `json:"limit" default:"50" description:"Maximum number of tasks to return"`
	OptFields    []string `json:"optFields" description:"Optional fields to include"`
}

// AsanaTaskListSchema is the UI schema for asana-task-list
var AsanaTaskListSchema = resolver.NewSchemaBuilder("asana-task-list").
	WithName("Asana List Tasks").
	WithCategory("asana").
	WithIcon("check-square").
	WithDescription("List tasks from an Asana project or workspace").
	AddSection("Connection").
		AddExpressionField("apiToken", "API Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("1/1234567890abcdef..."),
			resolver.WithHint("Asana Personal Access Token (use {{secrets.asana_token}})"),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Filters").
		AddTextField("project", "Project ID",
			resolver.WithPlaceholder("1234567890"),
			resolver.WithHint("Project ID to list tasks from"),
		).
		AddTextField("section", "Section ID",
			resolver.WithPlaceholder("1234567890"),
			resolver.WithHint("Optional section ID to filter tasks"),
		).
		AddTextField("assignee", "Assignee ID",
			resolver.WithPlaceholder("1234567890"),
			resolver.WithHint("Optional assignee ID (use 'me' for current user)"),
		).
		AddTextField("workspace", "Workspace ID",
			resolver.WithPlaceholder("1234567890"),
			resolver.WithHint("Optional workspace ID"),
		).
		AddToggleField("completed", "Show Completed",
			resolver.WithDefault(false),
			resolver.WithHint("Include completed tasks"),
		).
		EndSection().
	AddSection("Options").
		AddNumberField("limit", "Limit",
			resolver.WithDefault(50),
			resolver.WithMinMax(1, 100),
		).
		AddTagsField("optFields", "Optional Fields",
			resolver.WithHint("Additional fields to include: notes, assignee, due_on, etc."),
		).
		EndSection().
	Build()

type AsanaTaskListExecutor struct{}

func (e *AsanaTaskListExecutor) Type() string { return "asana-task-list" }

func (e *AsanaTaskListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg AsanaTaskListConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	} else {
		return nil, fmt.Errorf("invalid resolver type")
	}

	if cfg.APIToken == "" {
		return nil, fmt.Errorf("apiToken is required")
	}

	client := getClient(cfg.APIToken)

	// Build query parameters
	params := url.Values{}
	params.Set("limit", fmt.Sprintf("%d", cfg.Limit))

	if cfg.Project != "" {
		params.Set("project", cfg.Project)
	}
	if cfg.Section != "" {
		params.Set("section", cfg.Section)
	}
	if cfg.Assignee != "" {
		params.Set("assignee", cfg.Assignee)
	}
	if cfg.Workspace != "" {
		params.Set("workspace", cfg.Workspace)
	}
	params.Set("completed", fmt.Sprintf("%v", cfg.Completed))

	if len(cfg.OptFields) > 0 {
		params.Set("opt_fields", joinStrings(cfg.OptFields, ","))
	}

	path := "/tasks?" + params.Encode()

	resp, err := client.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}

	var listResp AsanaListResponse
	if err := decodeResponse(resp, &listResp); err != nil {
		return nil, err
	}

	// Parse tasks from raw JSON
	tasks := make([]AsanaTask, 0, len(listResp.Data))
	for _, raw := range listResp.Data {
		var task AsanaTask
		if err := json.Unmarshal(raw, &task); err != nil {
			continue
		}
		tasks = append(tasks, task)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"tasks":  tasks,
			"count":  len(tasks),
			"offset": getNextPageOffset(listResp.NextPage),
		},
	}, nil
}

// ============================================================================
// ASANA-TASK-CREATE
// ============================================================================

// AsanaTaskCreateConfig defines the configuration for asana-task-create
type AsanaTaskCreateConfig struct {
	APIToken     string                 `json:"apiToken" description:"Asana Personal Access Token"`
	Name         string                 `json:"name" description:"Task name"`
	Notes        string                 `json:"notes" description:"Task description/notes"`
	Project      string                 `json:"project" description:"Project ID to create task in"`
	Section      string                 `json:"section" description:"Optional section ID"`
	Assignee     string                 `json:"assignee" description:"Assignee user ID"`
	DueOn        string                 `json:"dueOn" description:"Due date (YYYY-MM-DD)"`
	DueAt        string                 `json:"dueAt" description:"Due time (ISO 8601)"`
	Workspace    string                 `json:"workspace" description:"Workspace ID"`
	Parent       string                 `json:"parent" description:"Parent task ID for subtasks"`
	Tags         []string               `json:"tags" description:"Tag IDs to apply"`
	CustomFields map[string]interface{} `json:"customFields" description:"Custom field values"`
	Followers    []string               `json:"followers" description:"User IDs to add as followers"`
}

// AsanaTaskCreateSchema is the UI schema for asana-task-create
var AsanaTaskCreateSchema = resolver.NewSchemaBuilder("asana-task-create").
	WithName("Asana Create Task").
	WithCategory("asana").
	WithIcon("plus-circle").
	WithDescription("Create a new task in Asana").
	AddSection("Connection").
		AddExpressionField("apiToken", "API Token",
			resolver.WithRequired(),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Task Details").
		AddTextField("name", "Task Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("New Task"),
		).
		AddTextareaField("notes", "Notes",
			resolver.WithRows(4),
			resolver.WithPlaceholder("Task description..."),
		).
		EndSection().
	AddSection("Location").
		AddTextField("project", "Project ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("1234567890"),
		).
		AddTextField("section", "Section ID",
			resolver.WithPlaceholder("1234567890"),
		).
		AddTextField("workspace", "Workspace ID",
			resolver.WithPlaceholder("1234567890"),
		).
		EndSection().
	AddSection("Assignment").
		AddTextField("assignee", "Assignee ID",
			resolver.WithPlaceholder("1234567890 or 'me'"),
		).
		AddTextField("dueOn", "Due Date",
			resolver.WithPlaceholder("YYYY-MM-DD"),
		).
		AddTextField("dueAt", "Due Time",
			resolver.WithPlaceholder("ISO 8601 datetime"),
		).
		EndSection().
	AddSection("Advanced").
		AddTextField("parent", "Parent Task ID",
			resolver.WithHint("For creating subtasks"),
		).
		AddTagsField("tags", "Tags",
			resolver.WithHint("Tag IDs to apply"),
		).
		AddTagsField("followers", "Followers",
			resolver.WithHint("User IDs to add as followers"),
		).
		AddJSONField("customFields", "Custom Fields",
			resolver.WithHeight(100),
			resolver.WithHint("Custom field values as key-value pairs"),
		).
		EndSection().
	Build()

type AsanaTaskCreateExecutor struct{}

func (e *AsanaTaskCreateExecutor) Type() string { return "asana-task-create" }

func (e *AsanaTaskCreateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg AsanaTaskCreateConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	} else {
		return nil, fmt.Errorf("invalid resolver type")
	}

	if cfg.APIToken == "" {
		return nil, fmt.Errorf("apiToken is required")
	}
	if cfg.Name == "" {
		return nil, fmt.Errorf("name is required")
	}
	if cfg.Project == "" && cfg.Workspace == "" && cfg.Parent == "" {
		return nil, fmt.Errorf("project, workspace, or parent is required")
	}

	client := getClient(cfg.APIToken)

	// Build request body
	requestBody := map[string]interface{}{
		"data": map[string]interface{}{
			"name": cfg.Name,
		},
	}

	data := requestBody["data"].(map[string]interface{})

	if cfg.Notes != "" {
		data["notes"] = cfg.Notes
	}
	if cfg.Project != "" {
		data["projects"] = []string{cfg.Project}
	}
	if cfg.Assignee != "" {
		data["assignee"] = cfg.Assignee
	}
	if cfg.DueOn != "" {
		data["due_on"] = cfg.DueOn
	}
	if cfg.DueAt != "" {
		data["due_at"] = cfg.DueAt
	}
	if cfg.Workspace != "" {
		data["workspace"] = cfg.Workspace
	}
	if cfg.Parent != "" {
		data["parent"] = cfg.Parent
	}
	if len(cfg.Tags) > 0 {
		data["tags"] = cfg.Tags
	}
	if len(cfg.Followers) > 0 {
		data["followers"] = cfg.Followers
	}
	if len(cfg.CustomFields) > 0 {
		data["custom_fields"] = cfg.CustomFields
	}

	resp, err := client.doRequest(ctx, "POST", "/tasks", requestBody)
	if err != nil {
		return nil, err
	}

	var singleResp AsanaSingleResponse
	if err := decodeResponse(resp, &singleResp); err != nil {
		return nil, err
	}

	var task AsanaTask
	if err := json.Unmarshal(singleResp.Data, &task); err != nil {
		return nil, fmt.Errorf("failed to parse task: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"task": task,
			"id":   task.GID,
			"name": task.Name,
		},
	}, nil
}

// ============================================================================
// ASANA-TASK-UPDATE
// ============================================================================

// AsanaTaskUpdateConfig defines the configuration for asana-task-update
type AsanaTaskUpdateConfig struct {
	APIToken     string                 `json:"apiToken" description:"Asana Personal Access Token"`
	TaskID       string                 `json:"taskId" description:"Task ID to update"`
	Name         string                 `json:"name" description:"New task name"`
	Notes        string                 `json:"notes" description:"New task notes"`
	Assignee     string                 `json:"assignee" description:"New assignee ID"`
	Completed    *bool                  `json:"completed" description:"Completion status"`
	DueOn        string                 `json:"dueOn" description:"New due date"`
	DueAt        string                 `json:"dueAt" description:"New due time"`
	Section      string                 `json:"section" description:"New section ID"`
	Tags         []string               `json:"tags" description:"Tag IDs to set"`
	CustomFields map[string]interface{} `json:"customFields" description:"Custom field values"`
}

// AsanaTaskUpdateSchema is the UI schema for asana-task-update
var AsanaTaskUpdateSchema = resolver.NewSchemaBuilder("asana-task-update").
	WithName("Asana Update Task").
	WithCategory("asana").
	WithIcon("edit").
	WithDescription("Update an existing Asana task").
	AddSection("Connection").
		AddExpressionField("apiToken", "API Token",
			resolver.WithRequired(),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Task").
		AddTextField("taskId", "Task ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("1234567890"),
		).
		EndSection().
	AddSection("Updates").
		AddTextField("name", "Task Name",
			resolver.WithPlaceholder("New name"),
		).
		AddTextareaField("notes", "Notes",
			resolver.WithRows(4),
			resolver.WithPlaceholder("New description..."),
		).
		AddTextField("assignee", "Assignee ID",
			resolver.WithPlaceholder("1234567890 or 'me'"),
		).
		AddToggleField("completed", "Completed",
			resolver.WithHint("Mark task as completed"),
		).
		AddTextField("dueOn", "Due Date",
			resolver.WithPlaceholder("YYYY-MM-DD"),
		).
		AddTextField("dueAt", "Due Time",
			resolver.WithPlaceholder("ISO 8601 datetime"),
		).
		AddTextField("section", "Section ID",
			resolver.WithPlaceholder("Move to section"),
		).
		EndSection().
	AddSection("Advanced").
		AddTagsField("tags", "Tags",
			resolver.WithHint("Tag IDs to set"),
		).
		AddJSONField("customFields", "Custom Fields",
			resolver.WithHeight(100),
		).
		EndSection().
	Build()

type AsanaTaskUpdateExecutor struct{}

func (e *AsanaTaskUpdateExecutor) Type() string { return "asana-task-update" }

func (e *AsanaTaskUpdateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg AsanaTaskUpdateConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	} else {
		return nil, fmt.Errorf("invalid resolver type")
	}

	if cfg.APIToken == "" {
		return nil, fmt.Errorf("apiToken is required")
	}
	if cfg.TaskID == "" {
		return nil, fmt.Errorf("taskId is required")
	}

	client := getClient(cfg.APIToken)

	// Build request body with only provided fields
	requestBody := map[string]interface{}{
		"data": map[string]interface{}{},
	}

	data := requestBody["data"].(map[string]interface{})

	if cfg.Name != "" {
		data["name"] = cfg.Name
	}
	if cfg.Notes != "" {
		data["notes"] = cfg.Notes
	}
	if cfg.Assignee != "" {
		data["assignee"] = cfg.Assignee
	}
	if cfg.Completed != nil {
		data["completed"] = *cfg.Completed
	}
	if cfg.DueOn != "" {
		data["due_on"] = cfg.DueOn
	}
	if cfg.DueAt != "" {
		data["due_at"] = cfg.DueAt
	}
	if cfg.Section != "" {
		data["memberships"] = []map[string]interface{}{
			{"section": cfg.Section},
		}
	}
	if len(cfg.Tags) > 0 {
		data["tags"] = cfg.Tags
	}
	if len(cfg.CustomFields) > 0 {
		data["custom_fields"] = cfg.CustomFields
	}

	resp, err := client.doRequest(ctx, "PUT", "/tasks/"+cfg.TaskID, requestBody)
	if err != nil {
		return nil, err
	}

	var singleResp AsanaSingleResponse
	if err := decodeResponse(resp, &singleResp); err != nil {
		return nil, err
	}

	var task AsanaTask
	if err := json.Unmarshal(singleResp.Data, &task); err != nil {
		return nil, fmt.Errorf("failed to parse task: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"task": task,
			"id":   task.GID,
			"name": task.Name,
		},
	}, nil
}

// ============================================================================
// ASANA-TASK-DELETE
// ============================================================================

// AsanaTaskDeleteConfig defines the configuration for asana-task-delete
type AsanaTaskDeleteConfig struct {
	APIToken string `json:"apiToken" description:"Asana Personal Access Token"`
	TaskID   string `json:"taskId" description:"Task ID to delete"`
}

// AsanaTaskDeleteSchema is the UI schema for asana-task-delete
var AsanaTaskDeleteSchema = resolver.NewSchemaBuilder("asana-task-delete").
	WithName("Asana Delete Task").
	WithCategory("asana").
	WithIcon("trash").
	WithDescription("Delete a task from Asana").
	AddSection("Connection").
		AddExpressionField("apiToken", "API Token",
			resolver.WithRequired(),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Task").
		AddTextField("taskId", "Task ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("1234567890"),
		).
		EndSection().
	Build()

type AsanaTaskDeleteExecutor struct{}

func (e *AsanaTaskDeleteExecutor) Type() string { return "asana-task-delete" }

func (e *AsanaTaskDeleteExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg AsanaTaskDeleteConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	} else {
		return nil, fmt.Errorf("invalid resolver type")
	}

	if cfg.APIToken == "" {
		return nil, fmt.Errorf("apiToken is required")
	}
	if cfg.TaskID == "" {
		return nil, fmt.Errorf("taskId is required")
	}

	client := getClient(cfg.APIToken)

	resp, err := client.doRequest(ctx, "DELETE", "/tasks/"+cfg.TaskID, nil)
	if err != nil {
		return nil, err
	}

	// Asana returns empty response on successful delete
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("Asana API error (%d): %s", resp.StatusCode, string(body))
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success": true,
			"taskId":  cfg.TaskID,
		},
	}, nil
}

// ============================================================================
// ASANA-PROJECT-LIST
// ============================================================================

// AsanaProjectListConfig defines the configuration for asana-project-list
type AsanaProjectListConfig struct {
	APIToken  string   `json:"apiToken" description:"Asana Personal Access Token"`
	Workspace string   `json:"workspace" description:"Workspace ID to list projects from"`
	Team      string   `json:"team" description:"Optional team ID to filter projects"`
	Archived  bool     `json:"archived" default:"false" description:"Include archived projects"`
	Limit     int      `json:"limit" default:"50" description:"Maximum number of projects"`
	OptFields []string `json:"optFields" description:"Optional fields to include"`
}

// AsanaProjectListSchema is the UI schema for asana-project-list
var AsanaProjectListSchema = resolver.NewSchemaBuilder("asana-project-list").
	WithName("Asana List Projects").
	WithCategory("asana").
	WithIcon("folder").
	WithDescription("List projects from an Asana workspace").
	AddSection("Connection").
		AddExpressionField("apiToken", "API Token",
			resolver.WithRequired(),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Filters").
		AddTextField("workspace", "Workspace ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("1234567890"),
		).
		AddTextField("team", "Team ID",
			resolver.WithPlaceholder("1234567890"),
		).
		AddToggleField("archived", "Include Archived",
			resolver.WithDefault(false),
		).
		EndSection().
	AddSection("Options").
		AddNumberField("limit", "Limit",
			resolver.WithDefault(50),
			resolver.WithMinMax(1, 100),
		).
		AddTagsField("optFields", "Optional Fields",
			resolver.WithHint("Additional fields: notes, color, due_date, etc."),
		).
		EndSection().
	Build()

type AsanaProjectListExecutor struct{}

func (e *AsanaProjectListExecutor) Type() string { return "asana-project-list" }

func (e *AsanaProjectListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg AsanaProjectListConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	} else {
		return nil, fmt.Errorf("invalid resolver type")
	}

	if cfg.APIToken == "" {
		return nil, fmt.Errorf("apiToken is required")
	}
	if cfg.Workspace == "" {
		return nil, fmt.Errorf("workspace is required")
	}

	client := getClient(cfg.APIToken)

	// Build query parameters
	params := url.Values{}
	params.Set("workspace", cfg.Workspace)
	params.Set("archived", fmt.Sprintf("%v", cfg.Archived))
	params.Set("limit", fmt.Sprintf("%d", cfg.Limit))

	if cfg.Team != "" {
		params.Set("team", cfg.Team)
	}
	if len(cfg.OptFields) > 0 {
		params.Set("opt_fields", joinStrings(cfg.OptFields, ","))
	}

	path := "/projects?" + params.Encode()

	resp, err := client.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}

	var listResp AsanaListResponse
	if err := decodeResponse(resp, &listResp); err != nil {
		return nil, err
	}

	// Parse projects from raw JSON
	projects := make([]AsanaProject, 0, len(listResp.Data))
	for _, raw := range listResp.Data {
		var project AsanaProject
		if err := json.Unmarshal(raw, &project); err != nil {
			continue
		}
		projects = append(projects, project)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"projects": projects,
			"count":    len(projects),
			"offset":   getNextPageOffset(listResp.NextPage),
		},
	}, nil
}

// ============================================================================
// ASANA-PROJECT-GET
// ============================================================================

// AsanaProjectGetConfig defines the configuration for asana-project-get
type AsanaProjectGetConfig struct {
	APIToken  string   `json:"apiToken" description:"Asana Personal Access Token"`
	ProjectID string   `json:"projectId" description:"Project ID to retrieve"`
	OptFields []string `json:"optFields" description:"Optional fields to include"`
}

// AsanaProjectGetSchema is the UI schema for asana-project-get
var AsanaProjectGetSchema = resolver.NewSchemaBuilder("asana-project-get").
	WithName("Asana Get Project").
	WithCategory("asana").
	WithIcon("folder-open").
	WithDescription("Get details of a specific Asana project").
	AddSection("Connection").
		AddExpressionField("apiToken", "API Token",
			resolver.WithRequired(),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Project").
		AddTextField("projectId", "Project ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("1234567890"),
		).
		EndSection().
	AddSection("Options").
		AddTagsField("optFields", "Optional Fields",
			resolver.WithHint("Additional fields: notes, custom_fields, members, etc."),
		).
		EndSection().
	Build()

type AsanaProjectGetExecutor struct{}

func (e *AsanaProjectGetExecutor) Type() string { return "asana-project-get" }

func (e *AsanaProjectGetExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg AsanaProjectGetConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	} else {
		return nil, fmt.Errorf("invalid resolver type")
	}

	if cfg.APIToken == "" {
		return nil, fmt.Errorf("apiToken is required")
	}
	if cfg.ProjectID == "" {
		return nil, fmt.Errorf("projectId is required")
	}

	client := getClient(cfg.APIToken)

	// Build query parameters
	params := url.Values{}
	if len(cfg.OptFields) > 0 {
		params.Set("opt_fields", joinStrings(cfg.OptFields, ","))
	}

	path := "/projects/" + cfg.ProjectID
	if len(params) > 0 {
		path += "?" + params.Encode()
	}

	resp, err := client.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}

	var singleResp AsanaSingleResponse
	if err := decodeResponse(resp, &singleResp); err != nil {
		return nil, err
	}

	var project AsanaProject
	if err := json.Unmarshal(singleResp.Data, &project); err != nil {
		return nil, fmt.Errorf("failed to parse project: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"project": project,
			"id":      project.GID,
			"name":    project.Name,
		},
	}, nil
}

// ============================================================================
// ASANA-SECTION-LIST
// ============================================================================

// AsanaSectionListConfig defines the configuration for asana-section-list
type AsanaSectionListConfig struct {
	APIToken  string   `json:"apiToken" description:"Asana Personal Access Token"`
	ProjectID string   `json:"projectId" description:"Project ID to list sections from"`
	OptFields []string `json:"optFields" description:"Optional fields to include"`
}

// AsanaSectionListSchema is the UI schema for asana-section-list
var AsanaSectionListSchema = resolver.NewSchemaBuilder("asana-section-list").
	WithName("Asana List Sections").
	WithCategory("asana").
	WithIcon("columns").
	WithDescription("List sections from an Asana project").
	AddSection("Connection").
		AddExpressionField("apiToken", "API Token",
			resolver.WithRequired(),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Project").
		AddTextField("projectId", "Project ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("1234567890"),
		).
		EndSection().
	AddSection("Options").
		AddTagsField("optFields", "Optional Fields",
			resolver.WithHint("Additional fields to include"),
		).
		EndSection().
	Build()

type AsanaSectionListExecutor struct{}

func (e *AsanaSectionListExecutor) Type() string { return "asana-section-list" }

func (e *AsanaSectionListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg AsanaSectionListConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	} else {
		return nil, fmt.Errorf("invalid resolver type")
	}

	if cfg.APIToken == "" {
		return nil, fmt.Errorf("apiToken is required")
	}
	if cfg.ProjectID == "" {
		return nil, fmt.Errorf("projectId is required")
	}

	client := getClient(cfg.APIToken)

	// Build query parameters
	params := url.Values{}
	params.Set("project", cfg.ProjectID)

	if len(cfg.OptFields) > 0 {
		params.Set("opt_fields", joinStrings(cfg.OptFields, ","))
	}

	path := "/sections?" + params.Encode()

	resp, err := client.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}

	var listResp AsanaListResponse
	if err := decodeResponse(resp, &listResp); err != nil {
		return nil, err
	}

	// Parse sections from raw JSON
	sections := make([]AsanaSection, 0, len(listResp.Data))
	for _, raw := range listResp.Data {
		var section AsanaSection
		if err := json.Unmarshal(raw, &section); err != nil {
			continue
		}
		sections = append(sections, section)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"sections": sections,
			"count":    len(sections),
		},
	}, nil
}

// ============================================================================
// ASANA-STORY-CREATE
// ============================================================================

// AsanaStoryCreateConfig defines the configuration for asana-story-create
type AsanaStoryCreateConfig struct {
	APIToken string `json:"apiToken" description:"Asana Personal Access Token"`
	TaskID   string `json:"taskId" description:"Task ID to add story to"`
	Text     string `json:"text" description:"Story/comment text"`
}

// AsanaStoryCreateSchema is the UI schema for asana-story-create
var AsanaStoryCreateSchema = resolver.NewSchemaBuilder("asana-story-create").
	WithName("Asana Create Story/Comment").
	WithCategory("asana").
	WithIcon("message-circle").
	WithDescription("Add a comment or story to an Asana task").
	AddSection("Connection").
		AddExpressionField("apiToken", "API Token",
			resolver.WithRequired(),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Story").
		AddTextField("taskId", "Task ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("1234567890"),
		).
		AddTextareaField("text", "Text",
			resolver.WithRequired(),
			resolver.WithRows(4),
			resolver.WithPlaceholder("Add a comment..."),
		).
		EndSection().
	Build()

type AsanaStoryCreateExecutor struct{}

func (e *AsanaStoryCreateExecutor) Type() string { return "asana-story-create" }

func (e *AsanaStoryCreateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg AsanaStoryCreateConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	} else {
		return nil, fmt.Errorf("invalid resolver type")
	}

	if cfg.APIToken == "" {
		return nil, fmt.Errorf("apiToken is required")
	}
	if cfg.TaskID == "" {
		return nil, fmt.Errorf("taskId is required")
	}
	if cfg.Text == "" {
		return nil, fmt.Errorf("text is required")
	}

	client := getClient(cfg.APIToken)

	// Build request body
	requestBody := map[string]interface{}{
		"data": map[string]interface{}{
			"text": cfg.Text,
		},
	}

	resp, err := client.doRequest(ctx, "POST", "/tasks/"+cfg.TaskID+"/stories", requestBody)
	if err != nil {
		return nil, err
	}

	var singleResp AsanaSingleResponse
	if err := decodeResponse(resp, &singleResp); err != nil {
		return nil, err
	}

	var story AsanaStory
	if err := json.Unmarshal(singleResp.Data, &story); err != nil {
		return nil, fmt.Errorf("failed to parse story: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"story": story,
			"id":    story.GID,
			"text":  story.Text,
		},
	}, nil
}

// ============================================================================
// ASANA-SUBTASK-LIST
// ============================================================================

// AsanaSubtaskListConfig defines the configuration for asana-subtask-list
type AsanaSubtaskListConfig struct {
	APIToken  string   `json:"apiToken" description:"Asana Personal Access Token"`
	ParentID  string   `json:"parentId" description:"Parent task ID"`
	OptFields []string `json:"optFields" description:"Optional fields to include"`
}

// AsanaSubtaskListSchema is the UI schema for asana-subtask-list
var AsanaSubtaskListSchema = resolver.NewSchemaBuilder("asana-subtask-list").
	WithName("Asana List Subtasks").
	WithCategory("asana").
	WithIcon("list").
	WithDescription("List subtasks of an Asana task").
	AddSection("Connection").
		AddExpressionField("apiToken", "API Token",
			resolver.WithRequired(),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Task").
		AddTextField("parentId", "Parent Task ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("1234567890"),
		).
		EndSection().
	AddSection("Options").
		AddTagsField("optFields", "Optional Fields",
			resolver.WithHint("Additional fields: notes, assignee, due_on, etc."),
		).
		EndSection().
	Build()

type AsanaSubtaskListExecutor struct{}

func (e *AsanaSubtaskListExecutor) Type() string { return "asana-subtask-list" }

func (e *AsanaSubtaskListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg AsanaSubtaskListConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	} else {
		return nil, fmt.Errorf("invalid resolver type")
	}

	if cfg.APIToken == "" {
		return nil, fmt.Errorf("apiToken is required")
	}
	if cfg.ParentID == "" {
		return nil, fmt.Errorf("parentId is required")
	}

	client := getClient(cfg.APIToken)

	// Build query parameters
	params := url.Values{}
	if len(cfg.OptFields) > 0 {
		params.Set("opt_fields", joinStrings(cfg.OptFields, ","))
	}

	path := "/tasks/" + cfg.ParentID + "/subtasks"
	if len(params) > 0 {
		path += "?" + params.Encode()
	}

	resp, err := client.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}

	var listResp AsanaListResponse
	if err := decodeResponse(resp, &listResp); err != nil {
		return nil, err
	}

	// Parse subtasks from raw JSON
	subtasks := make([]AsanaTask, 0, len(listResp.Data))
	for _, raw := range listResp.Data {
		var subtask AsanaTask
		if err := json.Unmarshal(raw, &subtask); err != nil {
			continue
		}
		subtasks = append(subtasks, subtask)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"subtasks": subtasks,
			"count":    len(subtasks),
		},
	}, nil
}

// ============================================================================
// ASANA-TAG-LIST
// ============================================================================

// AsanaTagListConfig defines the configuration for asana-tag-list
type AsanaTagListConfig struct {
	APIToken  string   `json:"apiToken" description:"Asana Personal Access Token"`
	Workspace string   `json:"workspace" description:"Workspace ID to list tags from"`
	Limit     int      `json:"limit" default:"50" description:"Maximum number of tags"`
	OptFields []string `json:"optFields" description:"Optional fields to include"`
}

// AsanaTagListSchema is the UI schema for asana-tag-list
var AsanaTagListSchema = resolver.NewSchemaBuilder("asana-tag-list").
	WithName("Asana List Tags").
	WithCategory("asana").
	WithIcon("tag").
	WithDescription("List tags from an Asana workspace").
	AddSection("Connection").
		AddExpressionField("apiToken", "API Token",
			resolver.WithRequired(),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Filters").
		AddTextField("workspace", "Workspace ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("1234567890"),
		).
		EndSection().
	AddSection("Options").
		AddNumberField("limit", "Limit",
			resolver.WithDefault(50),
			resolver.WithMinMax(1, 100),
		).
		AddTagsField("optFields", "Optional Fields",
			resolver.WithHint("Additional fields: notes, color, followers, etc."),
		).
		EndSection().
	Build()

type AsanaTagListExecutor struct{}

func (e *AsanaTagListExecutor) Type() string { return "asana-tag-list" }

func (e *AsanaTagListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg AsanaTagListConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	} else {
		return nil, fmt.Errorf("invalid resolver type")
	}

	if cfg.APIToken == "" {
		return nil, fmt.Errorf("apiToken is required")
	}
	if cfg.Workspace == "" {
		return nil, fmt.Errorf("workspace is required")
	}

	client := getClient(cfg.APIToken)

	// Build query parameters
	params := url.Values{}
	params.Set("workspace", cfg.Workspace)
	params.Set("limit", fmt.Sprintf("%d", cfg.Limit))

	if len(cfg.OptFields) > 0 {
		params.Set("opt_fields", joinStrings(cfg.OptFields, ","))
	}

	path := "/tags?" + params.Encode()

	resp, err := client.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}

	var listResp AsanaListResponse
	if err := decodeResponse(resp, &listResp); err != nil {
		return nil, err
	}

	// Parse tags from raw JSON
	tags := make([]AsanaTag, 0, len(listResp.Data))
	for _, raw := range listResp.Data {
		var tag AsanaTag
		if err := json.Unmarshal(raw, &tag); err != nil {
			continue
		}
		tags = append(tags, tag)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"tags":  tags,
			"count": len(tags),
		},
	}, nil
}

// ============================================================================
// HELPER FUNCTIONS
// ============================================================================

// joinStrings joins a slice of strings with a separator
func joinStrings(strs []string, sep string) string {
	if len(strs) == 0 {
		return ""
	}
	result := strs[0]
	for i := 1; i < len(strs); i++ {
		result += sep + strs[i]
	}
	return result
}

// getNextPageOffset returns the offset from next page info
func getNextPageOffset(nextPage *AsanaNextPage) string {
	if nextPage == nil {
		return ""
	}
	return nextPage.Offset
}
