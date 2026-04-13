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
	"strings"
	"sync"
	"time"

	"github.com/axiom-studio/skills.sdk/executor"
	"github.com/axiom-studio/skills.sdk/grpc"
	"github.com/axiom-studio/skills.sdk/resolver"
)

// ClickUp API configuration
const (
	ClickUpAPIBase = "https://api.clickup.com/api/v2"
)

// ClickUp client cache
var (
	clients     = make(map[string]*ClickUpClient)
	clientMutex sync.RWMutex
)

// ClickUpClient represents a ClickUp API client
type ClickUpClient struct {
	APIToken string
}

// ============================================================================
// CLICKUP API RESPONSE TYPES
// ============================================================================

// ClickUpTask represents a ClickUp task
type ClickUpTask struct {
	ID              string                 `json:"id,omitempty"`
	CustomID        string                 `json:"custom_id,omitempty"`
	CustomItemID    string                 `json:"custom_item_id,omitempty"`
	Name            string                 `json:"name,omitempty"`
	TextContent     string                 `json:"text_content,omitempty"`
	Description     string                 `json:"description,omitempty"`
	Status          *ClickUpStatus         `json:"status,omitempty"`
	Orderindex      string                 `json:"orderindex,omitempty"`
	DateCreated     string                 `json:"date_created,omitempty"`
	DateUpdated     string                 `json:"date_updated,omitempty"`
	DateClosed      string                 `json:"date_closed,omitempty"`
	DateDone        string                 `json:"date_done,omitempty"`
	Archived        bool                   `json:"archived,omitempty"`
	Creator         *ClickUpUser           `json:"creator,omitempty"`
	Assignees       []ClickUpUser          `json:"assignees,omitempty"`
	Watchers        []ClickUpUser          `json:"watchers,omitempty"`
	Checklists      []ClickUpChecklist     `json:"checklists,omitempty"`
	Tags            []ClickUpTag           `json:"tags,omitempty"`
	Parent          *ClickUpTask           `json:"parent,omitempty"`
	Priority        *ClickUpPriority       `json:"priority,omitempty"`
	DueDate         string                 `json:"due_date,omitempty"`
	DueDateTime     bool                   `json:"due_date_time,omitempty"`
	TimeEstimate    int64                  `json:"time_estimate,omitempty"`
	StartTime       string                 `json:"start_date,omitempty"`
	StartDateTime   bool                   `json:"start_date_time,omitempty"`
	Points          float64                `json:"points,omitempty"`
	CustomFields    []ClickUpCustomField   `json:"custom_fields,omitempty"`
	Dependencies    []ClickUpDependency    `json:"dependencies,omitempty"`
	LinkedTasks     []ClickUpLinkedTask    `json:"linked_tasks,omitempty"`
	TeamID          string                 `json:"team_id,omitempty"`
	URL             string                 `json:"url,omitempty"`
	PermissionLevel string                 `json:"permission_level,omitempty"`
	List            *ClickUpList           `json:"list,omitempty"`
	Project         *ClickUpProject        `json:"project,omitempty"`
	Folder          *ClickUpFolder         `json:"folder,omitempty"`
	Space           *ClickUpSpace          `json:"space,omitempty"`
	Attachments     []ClickUpAttachment    `json:"attachments,omitempty"`
}

// ClickUpSpace represents a ClickUp space
type ClickUpSpace struct {
	ID          string          `json:"id,omitempty"`
	Name        string          `json:"name,omitempty"`
	Color       string          `json:"color,omitempty"`
	Private     bool            `json:"private,omitempty"`
	Statuses    []ClickUpStatus `json:"statuses,omitempty"`
	MultipleAssignees bool      `json:"multiple_assignees,omitempty"`
	Features    *ClickUpFeatures `json:"features,omitempty"`
	Inbox       *ClickUpInbox   `json:"inbox,omitempty"`
}

// ClickUpProject represents a ClickUp project (folder in API v2)
type ClickUpProject struct {
	ID          string          `json:"id,omitempty"`
	Name        string          `json:"name,omitempty"`
	Color       string          `json:"color,omitempty"`
	Private     bool            `json:"private,omitempty"`
	Space       *ClickUpSpace   `json:"space,omitempty"`
	TaskCount   int64           `json:"task_count,omitempty"`
	Statuses    []ClickUpStatus `json:"statuses,omitempty"`
	Lists       []ClickUpList   `json:"lists,omitempty"`
}

// ClickUpFolder represents a ClickUp folder
type ClickUpFolder struct {
	ID          string          `json:"id,omitempty"`
	Name        string          `json:"name,omitempty"`
	Color       string          `json:"color,omitempty"`
	Private     bool            `json:"private,omitempty"`
	Space       *ClickUpSpace   `json:"space,omitempty"`
	TaskCount   int64           `json:"task_count,omitempty"`
	Statuses    []ClickUpStatus `json:"statuses,omitempty"`
	Lists       []ClickUpList   `json:"lists,omitempty"`
}

// ClickUpList represents a ClickUp list
type ClickUpList struct {
	ID          string          `json:"id,omitempty"`
	Name        string          `json:"name,omitempty"`
	Color       string          `json:"color,omitempty"`
	Private     bool            `json:"private,omitempty"`
	Content     string          `json:"content,omitempty"`
	Statuses    []ClickUpStatus `json:"statuses,omitempty"`
	Assignee    *ClickUpUser    `json:"assignee,omitempty"`
	TaskCount   int64           `json:"task_count,omitempty"`
	DueDate     string          `json:"due_date,omitempty"`
	DueDateTime bool            `json:"due_date_time,omitempty"`
	StartDate   string          `json:"start_date,omitempty"`
	StartDateTime bool          `json:"start_date_time,omitempty"`
	Folder      *ClickUpFolder  `json:"folder,omitempty"`
	Space       *ClickUpSpace   `json:"space,omitempty"`
}

// ClickUpStatus represents a task status
type ClickUpStatus struct {
	Status      string `json:"status,omitempty"`
	Color       string `json:"color,omitempty"`
	Type        string `json:"type,omitempty"`
	OrderIndex  int64  `json:"orderindex,omitempty"`
}

// ClickUpUser represents a ClickUp user
type ClickUpUser struct {
	ID       string `json:"id,omitempty"`
	Username string `json:"username,omitempty"`
	Email    string `json:"email,omitempty"`
	Color    string `json:"color,omitempty"`
	ProfilePicture string `json:"profilePicture,omitempty"`
	Initials string `json:"initials,omitempty"`
}

// ClickUpPriority represents task priority
type ClickUpPriority struct {
	Priority string `json:"priority,omitempty"`
	Color    string `json:"color,omitempty"`
	OrderIndex int64 `json:"orderindex,omitempty"`
}

// ClickUpTag represents a tag
type ClickUpTag struct {
	Name    string `json:"name,omitempty"`
	TagID   string `json:"id,omitempty"`
	Color   string `json:"color,omitempty"`
}

// ClickUpChecklist represents a checklist
type ClickUpChecklist struct {
	ID       string `json:"id,omitempty"`
	TaskID   string `json:"task_id,omitempty"`
	Name     string `json:"name,omitempty"`
	OrderIndex int64 `json:"orderindex,omitempty"`
	Items    []ClickUpChecklistItem `json:"items,omitempty"`
}

// ClickUpChecklistItem represents a checklist item
type ClickUpChecklistItem struct {
	ID       string `json:"id,omitempty"`
	Name     string `json:"name,omitempty"`
	OrderIndex int64 `json:"orderindex,omitempty"`
	AssignedTo []ClickUpUser `json:"assigned_to,omitempty"`
	Resolved bool   `json:"resolved,omitempty"`
}

// ClickUpCustomField represents a custom field
type ClickUpCustomField struct {
	ID       string `json:"id,omitempty"`
	Name     string `json:"name,omitempty"`
	Type     string `json:"type,omitempty"`
	Value    interface{} `json:"value,omitempty"`
}

// ClickUpDependency represents a task dependency
type ClickUpDependency struct {
	TaskID     string `json:"task_id,omitempty"`
	DependsOn  string `json:"depends_on,omitempty"`
	Type       int64  `json:"type,omitempty"`
}

// ClickUpLinkedTask represents a linked task
type ClickUpLinkedTask struct {
	TaskID    string `json:"task_id,omitempty"`
	LinkID    string `json:"link_id,omitempty"`
	TaskName  string `json:"task_name,omitempty"`
}

// ClickUpAttachment represents an attachment
type ClickUpAttachment struct {
	ID       string `json:"id,omitempty"`
	Version  int64  `json:"version,omitempty"`
	Date     string `json:"date,omitempty"`
	Title    string `json:"title,omitempty"`
	MimeType string `json:"mime_type,omitempty"`
	URL      string `json:"url,omitempty"`
}

// ClickUpFeatures represents space features
type ClickUpFeatures struct {
	DueDates        *ClickUpFeatureFlag `json:"due_dates,omitempty"`
	TimeTracking    *ClickUpFeatureFlag `json:"time_tracking,omitempty"`
	Points          *ClickUpFeatureFlag `json:"points,omitempty"`
	CustomFields    *ClickUpFeatureFlag `json:"custom_fields,omitempty"`
	Dependencies    *ClickUpFeatureFlag `json:"dependencies,omitempty"`
	Tags            *ClickUpFeatureFlag `json:"tags,omitempty"`
	Checklists      *ClickUpFeatureFlag `json:"checklists,omitempty"`
	Assignees       *ClickUpFeatureFlag `json:"assignees,omitempty"`
}

// ClickUpFeatureFlag represents a feature flag
type ClickUpFeatureFlag struct {
	Enabled bool `json:"enabled,omitempty"`
}

// ClickUpInbox represents space inbox
type ClickUpInbox struct {
	Enabled bool `json:"enabled,omitempty"`
}

// ClickUpComment represents a comment
type ClickUpComment struct {
	ID          string       `json:"id,omitempty"`
	TaskID      string       `json:"task_id,omitempty"`
	CommentText string       `json:"comment_text,omitempty"`
	User        *ClickUpUser `json:"user,omitempty"`
	AssignedBy  *ClickUpUser `json:"assigned_by,omitempty"`
	AssignedTo  []ClickUpUser `json:"assigned_to,omitempty"`
	Resolved    bool         `json:"resolved,omitempty"`
	Date        string       `json:"date,omitempty"`
	Reactions   []ClickUpReaction `json:"reactions,omitempty"`
	Attachments []ClickUpAttachment `json:"attachments,omitempty"`
}

// ClickUpReaction represents a comment reaction
type ClickUpReaction struct {
	Reaction string     `json:"reaction,omitempty"`
	User     *ClickUpUser `json:"user,omitempty"`
}

// ClickUpTimeEntry represents a time entry
type ClickUpTimeEntry struct {
	ID          string       `json:"id,omitempty"`
	TaskID      string       `json:"task_id,omitempty"`
	User        *ClickUpUser `json:"user,omitempty"`
	Description string       `json:"description,omitempty"`
	Duration    int64        `json:"duration,omitempty"`
	Start       int64        `json:"start,omitempty"`
	End         int64        `json:"end,omitempty"`
	IsRunning   bool         `json:"is_running,omitempty"`
	Billable    bool         `json:"billable,omitempty"`
	TagIDs      []string     `json:"tag_ids,omitempty"`
}

// ClickUpTeam represents a team
type ClickUpTeam struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
}

// ClickUpListResponse represents a paginated list response
type ClickUpListResponse struct {
	Spaces   []ClickUpSpace   `json:"spaces,omitempty"`
	Folders  []ClickUpFolder  `json:"folders,omitempty"`
	Lists    []ClickUpList    `json:"lists,omitempty"`
	Tasks    []ClickUpTask    `json:"tasks,omitempty"`
	Comments []ClickUpComment `json:"comments,omitempty"`
}

// ClickUpSingleResponse represents a single resource response
type ClickUpSingleResponse struct {
	Space      *ClickUpSpace      `json:"space,omitempty"`
	Folder     *ClickUpFolder     `json:"folder,omitempty"`
	List       *ClickUpList       `json:"list,omitempty"`
	Task       *ClickUpTask       `json:"task,omitempty"`
	Comment    *ClickUpComment    `json:"comment,omitempty"`
	TimeEntry  *ClickUpTimeEntry  `json:"time_entry,omitempty"`
}

// ============================================================================
// MAIN
// ============================================================================

func main() {
	// Get port from env or use default
	port := os.Getenv("SKILL_PORT")
	if port == "" {
		port = "50108"
	}

	// Create skill server
	server := grpc.NewSkillServer("skill-clickup", "1.0.0")

	// Register ClickUp executors with schemas
	server.RegisterExecutorWithSchema("clickup-task-list", &ClickUpTaskListExecutor{}, ClickUpTaskListSchema)
	server.RegisterExecutorWithSchema("clickup-task-create", &ClickUpTaskCreateExecutor{}, ClickUpTaskCreateSchema)
	server.RegisterExecutorWithSchema("clickup-task-update", &ClickUpTaskUpdateExecutor{}, ClickUpTaskUpdateSchema)
	server.RegisterExecutorWithSchema("clickup-task-delete", &ClickUpTaskDeleteExecutor{}, ClickUpTaskDeleteSchema)
	server.RegisterExecutorWithSchema("clickup-space-list", &ClickUpSpaceListExecutor{}, ClickUpSpaceListSchema)
	server.RegisterExecutorWithSchema("clickup-folder-list", &ClickUpFolderListExecutor{}, ClickUpFolderListSchema)
	server.RegisterExecutorWithSchema("clickup-list-list", &ClickUpListListExecutor{}, ClickUpListListSchema)
	server.RegisterExecutorWithSchema("clickup-time-track", &ClickUpTimeTrackExecutor{}, ClickUpTimeTrackSchema)
	server.RegisterExecutorWithSchema("clickup-comment-create", &ClickUpCommentCreateExecutor{}, ClickUpCommentCreateSchema)

	fmt.Printf("Starting skill-clickup gRPC server on port %s\n", port)
	if err := server.Serve(port); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to serve: %v\n", err)
		os.Exit(1)
	}
}

// getClient returns or creates a ClickUp client (cached)
func getClient(apiToken string) *ClickUpClient {
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

	client = &ClickUpClient{APIToken: apiToken}
	clients[apiToken] = client
	return client
}

// doRequest performs an HTTP request to the ClickUp API
func (c *ClickUpClient) doRequest(ctx context.Context, method, path string, body interface{}) (*http.Response, error) {
	var reqBody io.Reader
	if body != nil {
		jsonData, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
		reqBody = bytes.NewBuffer(jsonData)
	}

	reqURL := ClickUpAPIBase + path
	req, err := http.NewRequestWithContext(ctx, method, reqURL, reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", c.APIToken)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	return resp, nil
}

// decodeResponse decodes a ClickUp API response
func decodeResponse(resp *http.Response, result interface{}) error {
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var errResp map[string]interface{}
		if err := json.Unmarshal(body, &errResp); err == nil {
			if msg, ok := errResp["err"].(string); ok && msg != "" {
				return fmt.Errorf("ClickUp API error (%d): %s", resp.StatusCode, msg)
			}
			if errs, ok := errResp["errors"].([]interface{}); ok && len(errs) > 0 {
				return fmt.Errorf("ClickUp API error (%d): %v", resp.StatusCode, errs[0])
			}
		}
		return fmt.Errorf("ClickUp API error (%d): %s", resp.StatusCode, string(body))
	}

	if err := json.Unmarshal(body, result); err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
	}

	return nil
}

// ============================================================================
// CLICKUP-TASK-LIST
// ============================================================================

// ClickUpTaskListConfig defines the configuration for clickup-task-list
type ClickUpTaskListConfig struct {
	APIToken    string   `json:"apiToken" description:"ClickUp API Token"`
	ListID      string   `json:"listId" description:"List ID to get tasks from"`
	Archive     bool     `json:"archive" default:"false" description:"Include archived tasks"`
	Page        int      `json:"page" default:"0" description:"Page number"`
	OrderBy     string   `json:"orderBy" description:"Order by field (id, created, updated, due_date)"`
	Descending  bool     `json:"descending" default:"false" description:"Sort descending"`
	Statuses    []string `json:"statuses" description:"Statuses to filter by"`
	IncludeMarkedDone bool `json:"includeMarkedDone" default:"false" description:"Include completed tasks"`
	Assignees   []string `json:"assignees" description:"Assignee IDs to filter by"`
	Tags        []string `json:"tags" description:"Tags to filter by"`
	DueDateGt   string   `json:"dueDateGt" description:"Due date greater than (timestamp)"`
	DueDateLt   string   `json:"dueDateLt" description:"Due date less than (timestamp)"`
	DateCreatedGt string `json:"dateCreatedGt" description:"Date created greater than (timestamp)"`
	DateCreatedLt string `json:"dateCreatedLt" description:"Date created less than (timestamp)"`
	DateUpdatedGt string `json:"dateUpdatedGt" description:"Date updated greater than (timestamp)"`
	DateUpdatedLt string `json:"dateUpdatedLt" description:"Date updated less than (timestamp)"`
	CustomFields string `json:"customFields" description:"Custom fields filter (JSON)"`
}

// ClickUpTaskListSchema is the UI schema for clickup-task-list
var ClickUpTaskListSchema = resolver.NewSchemaBuilder("clickup-task-list").
	WithName("ClickUp List Tasks").
	WithCategory("clickup").
	WithIcon("check-circle").
	WithDescription("List tasks from a ClickUp list").
	AddSection("Connection").
		AddExpressionField("apiToken", "API Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("pk_xxxxxxxxxxxxxxxxxxxx"),
			resolver.WithHint("ClickUp API Token (use {{secrets.clickup_token}})"),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Task Location").
		AddTextField("listId", "List ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("1234567890"),
			resolver.WithHint("ClickUp List ID to get tasks from"),
		).
		EndSection().
	AddSection("Filters").
		AddToggleField("archive", "Include Archived",
			resolver.WithDefault(false),
		).
		AddTagsField("statuses", "Statuses",
			resolver.WithHint("Filter by status names (e.g., todo, in progress)"),
		).
		AddTagsField("assignees", "Assignees",
			resolver.WithHint("Filter by assignee IDs"),
		).
		AddTagsField("tags", "Tags",
			resolver.WithHint("Filter by tag names"),
		).
		AddToggleField("includeMarkedDone", "Include Completed",
			resolver.WithDefault(false),
			resolver.WithHint("Include tasks marked as done"),
		).
		EndSection().
	AddSection("Date Filters").
		AddTextField("dueDateGt", "Due Date After",
			resolver.WithPlaceholder("Unix timestamp"),
			resolver.WithHint("Tasks with due date after this timestamp"),
		).
		AddTextField("dueDateLt", "Due Date Before",
			resolver.WithPlaceholder("Unix timestamp"),
			resolver.WithHint("Tasks with due date before this timestamp"),
		).
		AddTextField("dateCreatedGt", "Created After",
			resolver.WithPlaceholder("Unix timestamp"),
		).
		AddTextField("dateCreatedLt", "Created Before",
			resolver.WithPlaceholder("Unix timestamp"),
		).
		AddTextField("dateUpdatedGt", "Updated After",
			resolver.WithPlaceholder("Unix timestamp"),
		).
		AddTextField("dateUpdatedLt", "Updated Before",
			resolver.WithPlaceholder("Unix timestamp"),
		).
		EndSection().
	AddSection("Sorting").
		AddTextField("orderBy", "Order By",
			resolver.WithPlaceholder("id, created, updated, due_date"),
			resolver.WithHint("Field to order results by"),
		).
		AddToggleField("descending", "Descending",
			resolver.WithDefault(false),
			resolver.WithHint("Sort in descending order"),
		).
		AddNumberField("page", "Page",
			resolver.WithDefault(0),
			resolver.WithHint("Page number for pagination"),
		).
		EndSection().
	Build()

type ClickUpTaskListExecutor struct{}

func (e *ClickUpTaskListExecutor) Type() string { return "clickup-task-list" }

func (e *ClickUpTaskListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg ClickUpTaskListConfig
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
	if cfg.ListID == "" {
		return nil, fmt.Errorf("listId is required")
	}

	client := getClient(cfg.APIToken)

	// Build query parameters
	params := url.Values{}
	params.Set("archived", fmt.Sprintf("%v", cfg.Archive))
	params.Set("page", fmt.Sprintf("%d", cfg.Page))
	params.Set("include_marked_done", fmt.Sprintf("%v", cfg.IncludeMarkedDone))
	params.Set("descending", fmt.Sprintf("%v", cfg.Descending))

	if cfg.OrderBy != "" {
		params.Set("order_by", cfg.OrderBy)
	}
	if len(cfg.Statuses) > 0 {
		for _, status := range cfg.Statuses {
			params.Add("statuses[]", status)
		}
	}
	if len(cfg.Assignees) > 0 {
		for _, assignee := range cfg.Assignees {
			params.Add("assignees[]", assignee)
		}
	}
	if len(cfg.Tags) > 0 {
		for _, tag := range cfg.Tags {
			params.Add("tags[]", tag)
		}
	}
	if cfg.DueDateGt != "" {
		params.Set("due_date_gt", cfg.DueDateGt)
	}
	if cfg.DueDateLt != "" {
		params.Set("due_date_lt", cfg.DueDateLt)
	}
	if cfg.DateCreatedGt != "" {
		params.Set("date_created_gt", cfg.DateCreatedGt)
	}
	if cfg.DateCreatedLt != "" {
		params.Set("date_created_lt", cfg.DateCreatedLt)
	}
	if cfg.DateUpdatedGt != "" {
		params.Set("date_updated_gt", cfg.DateUpdatedGt)
	}
	if cfg.DateUpdatedLt != "" {
		params.Set("date_updated_lt", cfg.DateUpdatedLt)
	}

	path := "/list/" + cfg.ListID + "/task?" + params.Encode()

	resp, err := client.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}

	var listResp ClickUpListResponse
	if err := decodeResponse(resp, &listResp); err != nil {
		return nil, err
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"tasks": listResp.Tasks,
			"count": len(listResp.Tasks),
		},
	}, nil
}

// ============================================================================
// CLICKUP-TASK-CREATE
// ============================================================================

// ClickUpTaskCreateConfig defines the configuration for clickup-task-create
type ClickUpTaskCreateConfig struct {
	APIToken      string                 `json:"apiToken" description:"ClickUp API Token"`
	ListID        string                 `json:"listId" description:"List ID to create task in"`
	Name          string                 `json:"name" description:"Task name"`
	Description   string                 `json:"description" description:"Task description (Markdown)"`
	Assignees     []string               `json:"assignees" description:"Assignee user IDs"`
	Tags          []string               `json:"tags" description:"Tag names"`
	Status        string                 `json:"status" description:"Task status"`
	Priority      int                    `json:"priority" description:"Priority (1=Urgent, 2=High, 3=Normal, 4=Low)"`
	DueDate       string                 `json:"dueDate" description:"Due date (Unix timestamp in ms)"`
	StartDate     string                 `json:"startDate" description:"Start date (Unix timestamp in ms)"`
	DueDateTime   bool                   `json:"dueDateTime" description:"Include time in due date"`
	StartDateTime bool                   `json:"startDateTime" description:"Include time in start date"`
	TimeEstimate  int64                  `json:"timeEstimate" description:"Time estimate in milliseconds"`
	Parent        string                 `json:"parent" description:"Parent task ID for subtasks"`
	CustomFields  map[string]interface{} `json:"customFields" description:"Custom field values"`
	Links         []string               `json:"links" description:"Task links (URLs)"`
	CheckRequiredCustomFields bool       `json:"checkRequiredCustomFields" default:"false" description:"Check required custom fields"`
}

// ClickUpTaskCreateSchema is the UI schema for clickup-task-create
var ClickUpTaskCreateSchema = resolver.NewSchemaBuilder("clickup-task-create").
	WithName("ClickUp Create Task").
	WithCategory("clickup").
	WithIcon("plus-circle").
	WithDescription("Create a new task in ClickUp").
	AddSection("Connection").
		AddExpressionField("apiToken", "API Token",
			resolver.WithRequired(),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Task Location").
		AddTextField("listId", "List ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("1234567890"),
			resolver.WithHint("ClickUp List ID to create task in"),
		).
		EndSection().
	AddSection("Task Details").
		AddTextField("name", "Task Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("New Task"),
		).
		AddTextareaField("description", "Description",
			resolver.WithRows(4),
			resolver.WithPlaceholder("Task description in Markdown..."),
			resolver.WithHint("Supports Markdown formatting"),
		).
		EndSection().
	AddSection("Assignment").
		AddTagsField("assignees", "Assignees",
			resolver.WithHint("User IDs to assign"),
		).
		AddTextField("status", "Status",
			resolver.WithPlaceholder("todo, in progress, etc."),
			resolver.WithHint("Task status name"),
		).
		AddSelectField("priority", "Priority",
			[]resolver.SelectOption{
				{Label: "Urgent", Value: "1"},
				{Label: "High", Value: "2"},
				{Label: "Normal", Value: "3"},
				{Label: "Low", Value: "4"},
			},
			resolver.WithHint("Task priority level"),
		).
		EndSection().
	AddSection("Dates").
		AddTextField("dueDate", "Due Date",
			resolver.WithPlaceholder("Unix timestamp in milliseconds"),
			resolver.WithHint("e.g., 1700000000000"),
		).
		AddTextField("startDate", "Start Date",
			resolver.WithPlaceholder("Unix timestamp in milliseconds"),
		).
		AddToggleField("dueDateTime", "Include Due Time",
			resolver.WithDefault(false),
		).
		AddToggleField("startDateTime", "Include Start Time",
			resolver.WithDefault(false),
		).
		AddNumberField("timeEstimate", "Time Estimate",
			resolver.WithHint("Time estimate in milliseconds"),
		).
		EndSection().
	AddSection("Advanced").
		AddTagsField("tags", "Tags",
			resolver.WithHint("Tag names to apply"),
		).
		AddTextField("parent", "Parent Task ID",
			resolver.WithHint("For creating subtasks"),
		).
		AddTagsField("links", "Links",
			resolver.WithHint("URLs to attach to the task"),
		).
		AddJSONField("customFields", "Custom Fields",
			resolver.WithHeight(100),
			resolver.WithHint("Custom field values as {field_id: value}"),
		).
		AddToggleField("checkRequiredCustomFields", "Check Required Custom Fields",
			resolver.WithDefault(false),
		).
		EndSection().
	Build()

type ClickUpTaskCreateExecutor struct{}

func (e *ClickUpTaskCreateExecutor) Type() string { return "clickup-task-create" }

func (e *ClickUpTaskCreateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg ClickUpTaskCreateConfig
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
	if cfg.ListID == "" {
		return nil, fmt.Errorf("listId is required")
	}
	if cfg.Name == "" {
		return nil, fmt.Errorf("name is required")
	}

	client := getClient(cfg.APIToken)

	// Build request body
	requestBody := map[string]interface{}{
		"name": cfg.Name,
	}

	if cfg.Description != "" {
		requestBody["description"] = cfg.Description
	}
	if len(cfg.Assignees) > 0 {
		requestBody["assignees"] = cfg.Assignees
	}
	if len(cfg.Tags) > 0 {
		requestBody["tags"] = cfg.Tags
	}
	if cfg.Status != "" {
		requestBody["status"] = cfg.Status
	}
	if cfg.Priority > 0 {
		requestBody["priority"] = cfg.Priority
	}
	if cfg.DueDate != "" {
		requestBody["due_date"] = cfg.DueDate
		requestBody["due_date_time"] = cfg.DueDateTime
	}
	if cfg.StartDate != "" {
		requestBody["start_date"] = cfg.StartDate
		requestBody["start_date_time"] = cfg.StartDateTime
	}
	if cfg.TimeEstimate > 0 {
		requestBody["time_estimate"] = cfg.TimeEstimate
	}
	if cfg.Parent != "" {
		requestBody["parent"] = cfg.Parent
	}
	if len(cfg.CustomFields) > 0 {
		requestBody["custom_fields"] = cfg.CustomFields
	}
	if len(cfg.Links) > 0 {
		requestBody["links"] = cfg.Links
	}
	requestBody["check_required_custom_fields"] = cfg.CheckRequiredCustomFields

	resp, err := client.doRequest(ctx, "POST", "/list/"+cfg.ListID+"/task", requestBody)
	if err != nil {
		return nil, err
	}

	var singleResp ClickUpSingleResponse
	if err := decodeResponse(resp, &singleResp); err != nil {
		return nil, err
	}

	if singleResp.Task == nil {
		return nil, fmt.Errorf("no task returned from API")
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"task": singleResp.Task,
			"id":   singleResp.Task.ID,
			"name": singleResp.Task.Name,
			"url":  singleResp.Task.URL,
		},
	}, nil
}

// ============================================================================
// CLICKUP-TASK-UPDATE
// ============================================================================

// ClickUpTaskUpdateConfig defines the configuration for clickup-task-update
type ClickUpTaskUpdateConfig struct {
	APIToken      string                 `json:"apiToken" description:"ClickUp API Token"`
	TaskID        string                 `json:"taskId" description:"Task ID to update"`
	Name          string                 `json:"name" description:"New task name"`
	Description   string                 `json:"description" description:"New task description"`
	Assignees     []string               `json:"assignees" description:"Assignee user IDs"`
	Tags          []string               `json:"tags" description:"Tag names"`
	Status        string                 `json:"status" description:"Task status"`
	Priority      int                    `json:"priority" description:"Priority (1=Urgent, 2=High, 3=Normal, 4=Low)"`
	DueDate       string                 `json:"dueDate" description:"Due date (Unix timestamp in ms)"`
	StartDate     string                 `json:"startDate" description:"Start date (Unix timestamp in ms)"`
	DueDateTime   *bool                  `json:"dueDateTime" description:"Include time in due date"`
	StartDateTime *bool                  `json:"startDateTime" description:"Include time in start date"`
	TimeEstimate  int64                  `json:"timeEstimate" description:"Time estimate in milliseconds"`
	CustomFields  map[string]interface{} `json:"customFields" description:"Custom field values"`
	Archived      *bool                  `json:"archived" description:"Archive/unarchive task"`
}

// ClickUpTaskUpdateSchema is the UI schema for clickup-task-update
var ClickUpTaskUpdateSchema = resolver.NewSchemaBuilder("clickup-task-update").
	WithName("ClickUp Update Task").
	WithCategory("clickup").
	WithIcon("edit").
	WithDescription("Update an existing ClickUp task").
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
		AddTextareaField("description", "Description",
			resolver.WithRows(4),
			resolver.WithPlaceholder("New description..."),
		).
		AddTagsField("assignees", "Assignees",
			resolver.WithHint("User IDs to assign"),
		).
		AddTextField("status", "Status",
			resolver.WithPlaceholder("todo, in progress, etc."),
		).
		AddSelectField("priority", "Priority",
			[]resolver.SelectOption{
				{Label: "Urgent", Value: "1"},
				{Label: "High", Value: "2"},
				{Label: "Normal", Value: "3"},
				{Label: "Low", Value: "4"},
			},
		).
		AddToggleField("archived", "Archived",
			resolver.WithHint("Archive or unarchive task"),
		).
		EndSection().
	AddSection("Dates").
		AddTextField("dueDate", "Due Date",
			resolver.WithPlaceholder("Unix timestamp in milliseconds"),
		).
		AddTextField("startDate", "Start Date",
			resolver.WithPlaceholder("Unix timestamp in milliseconds"),
		).
		AddToggleField("dueDateTime", "Include Due Time",
			resolver.WithDefault(false),
		).
		AddToggleField("startDateTime", "Include Start Time",
			resolver.WithDefault(false),
		).
		AddNumberField("timeEstimate", "Time Estimate",
			resolver.WithHint("Time estimate in milliseconds"),
		).
		EndSection().
	AddSection("Advanced").
		AddTagsField("tags", "Tags",
			resolver.WithHint("Tag names to apply"),
		).
		AddJSONField("customFields", "Custom Fields",
			resolver.WithHeight(100),
		).
		EndSection().
	Build()

type ClickUpTaskUpdateExecutor struct{}

func (e *ClickUpTaskUpdateExecutor) Type() string { return "clickup-task-update" }

func (e *ClickUpTaskUpdateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg ClickUpTaskUpdateConfig
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

	// Build request body
	requestBody := map[string]interface{}{}

	if cfg.Name != "" {
		requestBody["name"] = cfg.Name
	}
	if cfg.Description != "" {
		requestBody["description"] = cfg.Description
	}
	if len(cfg.Assignees) > 0 {
		requestBody["assignees"] = cfg.Assignees
	}
	if len(cfg.Tags) > 0 {
		requestBody["tags"] = cfg.Tags
	}
	if cfg.Status != "" {
		requestBody["status"] = cfg.Status
	}
	if cfg.Priority > 0 {
		requestBody["priority"] = cfg.Priority
	}
	if cfg.DueDate != "" {
		requestBody["due_date"] = cfg.DueDate
		if cfg.DueDateTime != nil {
			requestBody["due_date_time"] = *cfg.DueDateTime
		}
	}
	if cfg.StartDate != "" {
		requestBody["start_date"] = cfg.StartDate
		if cfg.StartDateTime != nil {
			requestBody["start_date_time"] = *cfg.StartDateTime
		}
	}
	if cfg.TimeEstimate > 0 {
		requestBody["time_estimate"] = cfg.TimeEstimate
	}
	if len(cfg.CustomFields) > 0 {
		requestBody["custom_fields"] = cfg.CustomFields
	}
	if cfg.Archived != nil {
		requestBody["archived"] = *cfg.Archived
	}

	resp, err := client.doRequest(ctx, "PUT", "/task/"+cfg.TaskID, requestBody)
	if err != nil {
		return nil, err
	}

	var singleResp ClickUpSingleResponse
	if err := decodeResponse(resp, &singleResp); err != nil {
		return nil, err
	}

	if singleResp.Task == nil {
		return nil, fmt.Errorf("no task returned from API")
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"task": singleResp.Task,
			"id":   singleResp.Task.ID,
			"name": singleResp.Task.Name,
		},
	}, nil
}

// ============================================================================
// CLICKUP-TASK-DELETE
// ============================================================================

// ClickUpTaskDeleteConfig defines the configuration for clickup-task-delete
type ClickUpTaskDeleteConfig struct {
	APIToken string `json:"apiToken" description:"ClickUp API Token"`
	TaskID   string `json:"taskId" description:"Task ID to delete"`
}

// ClickUpTaskDeleteSchema is the UI schema for clickup-task-delete
var ClickUpTaskDeleteSchema = resolver.NewSchemaBuilder("clickup-task-delete").
	WithName("ClickUp Delete Task").
	WithCategory("clickup").
	WithIcon("trash-2").
	WithDescription("Delete a task from ClickUp").
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
			resolver.WithHint("ClickUp Task ID to delete"),
		).
		EndSection().
	Build()

type ClickUpTaskDeleteExecutor struct{}

func (e *ClickUpTaskDeleteExecutor) Type() string { return "clickup-task-delete" }

func (e *ClickUpTaskDeleteExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg ClickUpTaskDeleteConfig
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

	resp, err := client.doRequest(ctx, "DELETE", "/task/"+cfg.TaskID, nil)
	if err != nil {
		return nil, err
	}

	// Check for success (204 No Content is expected)
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ClickUp API error (%d): %s", resp.StatusCode, string(body))
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success": true,
			"taskId":  cfg.TaskID,
			"message": "Task deleted successfully",
		},
	}, nil
}

// ============================================================================
// CLICKUP-SPACE-LIST
// ============================================================================

// ClickUpSpaceListConfig defines the configuration for clickup-space-list
type ClickUpSpaceListConfig struct {
	APIToken string `json:"apiToken" description:"ClickUp API Token"`
	TeamID   string `json:"teamId" description:"Team ID to list spaces from"`
	Archive  bool   `json:"archive" default:"false" description:"Include archived spaces"`
}

// ClickUpSpaceListSchema is the UI schema for clickup-space-list
var ClickUpSpaceListSchema = resolver.NewSchemaBuilder("clickup-space-list").
	WithName("ClickUp List Spaces").
	WithCategory("clickup").
	WithIcon("layout").
	WithDescription("List spaces in a ClickUp team").
	AddSection("Connection").
		AddExpressionField("apiToken", "API Token",
			resolver.WithRequired(),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Team").
		AddTextField("teamId", "Team ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("1234567890"),
			resolver.WithHint("ClickUp Team ID"),
		).
		EndSection().
	AddSection("Options").
		AddToggleField("archive", "Include Archived",
			resolver.WithDefault(false),
			resolver.WithHint("Include archived spaces"),
		).
		EndSection().
	Build()

type ClickUpSpaceListExecutor struct{}

func (e *ClickUpSpaceListExecutor) Type() string { return "clickup-space-list" }

func (e *ClickUpSpaceListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg ClickUpSpaceListConfig
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
	if cfg.TeamID == "" {
		return nil, fmt.Errorf("teamId is required")
	}

	client := getClient(cfg.APIToken)

	params := url.Values{}
	params.Set("archived", fmt.Sprintf("%v", cfg.Archive))

	path := "/team/" + cfg.TeamID + "/space?" + params.Encode()

	resp, err := client.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}

	var listResp ClickUpListResponse
	if err := decodeResponse(resp, &listResp); err != nil {
		return nil, err
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"spaces": listResp.Spaces,
			"count":  len(listResp.Spaces),
		},
	}, nil
}

// ============================================================================
// CLICKUP-FOLDER-LIST
// ============================================================================

// ClickUpFolderListConfig defines the configuration for clickup-folder-list
type ClickUpFolderListConfig struct {
	APIToken string `json:"apiToken" description:"ClickUp API Token"`
	SpaceID  string `json:"spaceId" description:"Space ID to list folders from"`
}

// ClickUpFolderListSchema is the UI schema for clickup-folder-list
var ClickUpFolderListSchema = resolver.NewSchemaBuilder("clickup-folder-list").
	WithName("ClickUp List Folders").
	WithCategory("clickup").
	WithIcon("folder").
	WithDescription("List folders in a ClickUp space").
	AddSection("Connection").
		AddExpressionField("apiToken", "API Token",
			resolver.WithRequired(),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Space").
		AddTextField("spaceId", "Space ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("1234567890"),
			resolver.WithHint("ClickUp Space ID"),
		).
		EndSection().
	Build()

type ClickUpFolderListExecutor struct{}

func (e *ClickUpFolderListExecutor) Type() string { return "clickup-folder-list" }

func (e *ClickUpFolderListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg ClickUpFolderListConfig
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
	if cfg.SpaceID == "" {
		return nil, fmt.Errorf("spaceId is required")
	}

	client := getClient(cfg.APIToken)

	path := "/space/" + cfg.SpaceID + "/folder"

	resp, err := client.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}

	var listResp ClickUpListResponse
	if err := decodeResponse(resp, &listResp); err != nil {
		return nil, err
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"folders": listResp.Folders,
			"count":   len(listResp.Folders),
		},
	}, nil
}

// ============================================================================
// CLICKUP-LIST-LIST
// ============================================================================

// ClickUpListListConfig defines the configuration for clickup-list-list
type ClickUpListListConfig struct {
	APIToken string `json:"apiToken" description:"ClickUp API Token"`
	FolderID string `json:"folderId" description:"Folder ID to list lists from"`
	SpaceID  string `json:"spaceId" description:"Space ID to list lists from (if no folder)"`
	Archive  bool   `json:"archive" default:"false" description:"Include archived lists"`
}

// ClickUpListListSchema is the UI schema for clickup-list-list
var ClickUpListListSchema = resolver.NewSchemaBuilder("clickup-list-list").
	WithName("ClickUp List Lists").
	WithCategory("clickup").
	WithIcon("list").
	WithDescription("List lists in a ClickUp folder or space").
	AddSection("Connection").
		AddExpressionField("apiToken", "API Token",
			resolver.WithRequired(),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Location").
		AddTextField("folderId", "Folder ID",
			resolver.WithPlaceholder("1234567890"),
			resolver.WithHint("ClickUp Folder ID (optional if using spaceId)"),
		).
		AddTextField("spaceId", "Space ID",
			resolver.WithPlaceholder("1234567890"),
			resolver.WithHint("ClickUp Space ID (used if no folderId)"),
		).
		EndSection().
	AddSection("Options").
		AddToggleField("archive", "Include Archived",
			resolver.WithDefault(false),
			resolver.WithHint("Include archived lists"),
		).
		EndSection().
	Build()

type ClickUpListListExecutor struct{}

func (e *ClickUpListListExecutor) Type() string { return "clickup-list-list" }

func (e *ClickUpListListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg ClickUpListListConfig
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
	if cfg.FolderID == "" && cfg.SpaceID == "" {
		return nil, fmt.Errorf("folderId or spaceId is required")
	}

	client := getClient(cfg.APIToken)

	var path string
	if cfg.FolderID != "" {
		params := url.Values{}
		params.Set("archived", fmt.Sprintf("%v", cfg.Archive))
		path = "/folder/" + cfg.FolderID + "/list?" + params.Encode()
	} else {
		params := url.Values{}
		params.Set("archived", fmt.Sprintf("%v", cfg.Archive))
		path = "/space/" + cfg.SpaceID + "/list?" + params.Encode()
	}

	resp, err := client.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}

	var listResp ClickUpListResponse
	if err := decodeResponse(resp, &listResp); err != nil {
		return nil, err
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"lists": listResp.Lists,
			"count": len(listResp.Lists),
		},
	}, nil
}

// ============================================================================
// CLICKUP-TIME-TRACK
// ============================================================================

// ClickUpTimeTrackConfig defines the configuration for clickup-time-track
type ClickUpTimeTrackConfig struct {
	APIToken    string `json:"apiToken" description:"ClickUp API Token"`
	TaskID      string `json:"taskId" description:"Task ID to track time on"`
	Description string `json:"description" description:"Time entry description"`
	StartTime   string `json:"startTime" description:"Start time (Unix timestamp in ms)"`
	Duration    int64  `json:"duration" description:"Duration in milliseconds"`
	Billable    bool   `json:"billable" default:"false" description:"Mark as billable"`
	Assignee    string `json:"assignee" description:"User ID to assign time entry to"`
}

// ClickUpTimeTrackSchema is the UI schema for clickup-time-track
var ClickUpTimeTrackSchema = resolver.NewSchemaBuilder("clickup-time-track").
	WithName("ClickUp Track Time").
	WithCategory("clickup").
	WithIcon("clock").
	WithDescription("Start or log time tracking on a ClickUp task").
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
			resolver.WithHint("ClickUp Task ID"),
		).
		EndSection().
	AddSection("Time Entry").
		AddTextareaField("description", "Description",
			resolver.WithRows(2),
			resolver.WithPlaceholder("What did you work on?"),
		).
		AddTextField("startTime", "Start Time",
			resolver.WithPlaceholder("Unix timestamp in milliseconds"),
			resolver.WithHint("Leave empty to start now"),
		).
		AddNumberField("duration", "Duration",
			resolver.WithHint("Duration in milliseconds (e.g., 3600000 for 1 hour)"),
		).
		AddToggleField("billable", "Billable",
			resolver.WithDefault(false),
			resolver.WithHint("Mark time as billable"),
		).
		AddTextField("assignee", "Assignee",
			resolver.WithPlaceholder("User ID"),
			resolver.WithHint("User ID to assign time entry to (defaults to authenticated user)"),
		).
		EndSection().
	Build()

type ClickUpTimeTrackExecutor struct{}

func (e *ClickUpTimeTrackExecutor) Type() string { return "clickup-time-track" }

func (e *ClickUpTimeTrackExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg ClickUpTimeTrackConfig
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

	// Build request body
	requestBody := map[string]interface{}{}

	if cfg.Description != "" {
		requestBody["description"] = cfg.Description
	}
	if cfg.StartTime != "" {
		requestBody["start"] = cfg.StartTime
	}
	if cfg.Duration > 0 {
		requestBody["duration"] = cfg.Duration
	}
	requestBody["billable"] = cfg.Billable
	if cfg.Assignee != "" {
		requestBody["assignee"] = cfg.Assignee
	}

	resp, err := client.doRequest(ctx, "POST", "/task/"+cfg.TaskID+"/time_entries", requestBody)
	if err != nil {
		return nil, err
	}

	var singleResp ClickUpSingleResponse
	if err := decodeResponse(resp, &singleResp); err != nil {
		return nil, err
	}

	if singleResp.TimeEntry == nil {
		return nil, fmt.Errorf("no time entry returned from API")
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"timeEntry": singleResp.TimeEntry,
			"id":        singleResp.TimeEntry.ID,
			"duration":  singleResp.TimeEntry.Duration,
			"isRunning": singleResp.TimeEntry.IsRunning,
		},
	}, nil
}

// ============================================================================
// CLICKUP-COMMENT-CREATE
// ============================================================================

// ClickUpCommentCreateConfig defines the configuration for clickup-comment-create
type ClickUpCommentCreateConfig struct {
	APIToken    string   `json:"apiToken" description:"ClickUp API Token"`
	TaskID      string   `json:"taskId" description:"Task ID to add comment to"`
	CommentText string   `json:"commentText" description:"Comment text (Markdown supported)"`
	Assignees   []string `json:"assignees" description:"User IDs to assign"`
}

// ClickUpCommentCreateSchema is the UI schema for clickup-comment-create
var ClickUpCommentCreateSchema = resolver.NewSchemaBuilder("clickup-comment-create").
	WithName("ClickUp Create Comment").
	WithCategory("clickup").
	WithIcon("message-square").
	WithDescription("Add a comment to a ClickUp task").
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
			resolver.WithHint("ClickUp Task ID"),
		).
		EndSection().
	AddSection("Comment").
		AddTextareaField("commentText", "Comment",
			resolver.WithRequired(),
			resolver.WithRows(4),
			resolver.WithPlaceholder("Add your comment here..."),
			resolver.WithHint("Supports Markdown formatting"),
		).
		AddTagsField("assignees", "Assignees",
			resolver.WithHint("User IDs to assign in the comment"),
		).
		EndSection().
	Build()

type ClickUpCommentCreateExecutor struct{}

func (e *ClickUpCommentCreateExecutor) Type() string { return "clickup-comment-create" }

func (e *ClickUpCommentCreateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg ClickUpCommentCreateConfig
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
	if cfg.CommentText == "" {
		return nil, fmt.Errorf("commentText is required")
	}

	client := getClient(cfg.APIToken)

	// Build request body
	requestBody := map[string]interface{}{
		"comment_text": cfg.CommentText,
	}

	if len(cfg.Assignees) > 0 {
		requestBody["assignees"] = cfg.Assignees
	}

	resp, err := client.doRequest(ctx, "POST", "/task/"+cfg.TaskID+"/comment", requestBody)
	if err != nil {
		return nil, err
	}

	var singleResp ClickUpSingleResponse
	if err := decodeResponse(resp, &singleResp); err != nil {
		return nil, err
	}

	if singleResp.Comment == nil {
		return nil, fmt.Errorf("no comment returned from API")
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"comment": singleResp.Comment,
			"id":      singleResp.Comment.ID,
			"text":    singleResp.Comment.CommentText,
		},
	}, nil
}

// ============================================================================
// UTILITY FUNCTIONS
// ============================================================================

// joinStrings joins a slice of strings with a separator
func joinStrings(strs []string, sep string) string {
	if len(strs) == 0 {
		return ""
	}
	return strings.Join(strs, sep)
}

// formatDuration formats milliseconds to human readable
func formatDuration(ms int64) string {
	duration := time.Duration(ms) * time.Millisecond
	hours := int(duration.Hours())
	minutes := int(duration.Minutes()) % 60
	seconds := int(duration.Seconds()) % 60

	if hours > 0 {
		return fmt.Sprintf("%dh %dm %ds", hours, minutes, seconds)
	}
	if minutes > 0 {
		return fmt.Sprintf("%dm %ds", minutes, seconds)
	}
	return fmt.Sprintf("%ds", seconds)
}
