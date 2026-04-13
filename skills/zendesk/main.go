package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
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
	server := grpc.NewSkillServer("skill-zendesk", "1.0.0")

	// Register Zendesk executors with schemas
	server.RegisterExecutorWithSchema("zendesk-ticket-create", &ZendeskTicketCreateExecutor{}, ZendeskTicketCreateSchema)
	server.RegisterExecutorWithSchema("zendesk-ticket-update", &ZendeskTicketUpdateExecutor{}, ZendeskTicketUpdateSchema)
	server.RegisterExecutorWithSchema("zendesk-ticket-search", &ZendeskTicketSearchExecutor{}, ZendeskTicketSearchSchema)
	server.RegisterExecutorWithSchema("zendesk-user", &ZendeskUserExecutor{}, ZendeskUserSchema)
	server.RegisterExecutorWithSchema("zendesk-macro", &ZendeskMacroExecutor{}, ZendeskMacroSchema)
	server.RegisterExecutorWithSchema("zendesk-view", &ZendeskViewExecutor{}, ZendeskViewSchema)

	fmt.Printf("Starting skill-zendesk gRPC server on port %s\n", port)
	if err := server.Serve(port); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to serve: %v\n", err)
		os.Exit(1)
	}
}

// ZendeskClient represents a Zendesk API client
type ZendeskClient struct {
	Subdomain  string
	Email      string
	APIToken   string
	HTTPClient *http.Client
}

// NewZendeskClient creates a new Zendesk API client
func NewZendeskClient(subdomain, email, apiToken string) *ZendeskClient {
	return &ZendeskClient{
		Subdomain:  subdomain,
		Email:      email,
		APIToken:   apiToken,
		HTTPClient: &http.Client{},
	}
}

// getBaseURL returns the base API URL
func (c *ZendeskClient) getBaseURL() string {
	return fmt.Sprintf("https://%s.zendesk.com/api/v2", c.Subdomain)
}

// doRequest performs an HTTP request to the Zendesk API
func (c *ZendeskClient) doRequest(ctx context.Context, method, endpoint string, body interface{}) ([]byte, error) {
	var reqBody io.Reader
	if body != nil {
		jsonData, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
		reqBody = bytes.NewBuffer(jsonData)
	}

	url := c.getBaseURL() + endpoint
	req, err := http.NewRequestWithContext(ctx, method, url, reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.SetBasicAuth(c.Email+"/token", c.APIToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	return respBody, nil
}

// ZendeskTicketCreateExecutor handles zendesk-ticket-create node type
type ZendeskTicketCreateExecutor struct{}

// ZendeskTicketCreateConfig defines the typed configuration for zendesk-ticket-create
type ZendeskTicketCreateConfig struct {
	Subdomain   string                 `json:"subdomain" description:"Zendesk subdomain (e.g., 'company' for company.zendesk.com)"`
	Email       string                 `json:"email" description:"Zendesk API email"`
	APIToken    string                 `json:"apiToken" description:"Zendesk API token"`
	Subject     string                 `json:"subject" description:"Ticket subject"`
	Description string                 `json:"description" description:"Ticket description/body"`
	RequesterID int64                  `json:"requesterId" description:"Requester user ID (optional)"`
	Requester   map[string]interface{} `json:"requester" description:"Requester details if creating new user (name, email)"`
	Priority    string                 `json:"priority" default:"normal" options:"low,normal,high,urgent" description:"Ticket priority"`
	Status      string                 `json:"status" default:"new" options:"new,open,pending,hold,solved,closed" description:"Ticket status"`
	Type        string                 `json:"type" default:"question" options:"question,incident,problem,task" description:"Ticket type"`
	Tags        []string               `json:"tags" description:"Ticket tags"`
	CustomFields []map[string]interface{} `json:"customFields" description:"Custom field values"`
}

// ZendeskTicketCreateSchema is the UI schema for zendesk-ticket-create
var ZendeskTicketCreateSchema = resolver.NewSchemaBuilder("zendesk-ticket-create").
	WithName("Create Zendesk Ticket").
	WithCategory("support").
	WithIcon("plus-circle").
	WithDescription("Create a new support ticket in Zendesk").
	AddSection("Authentication").
		AddTextField("subdomain", "Zendesk Subdomain",
			resolver.WithRequired(),
			resolver.WithPlaceholder("company"),
			resolver.WithHint("Your Zendesk subdomain (e.g., 'company' for company.zendesk.com)"),
		).
		AddTextField("email", "API Email",
			resolver.WithRequired(),
			resolver.WithPlaceholder("agent@company.com"),
		).
		AddTextField("apiToken", "API Token",
			resolver.WithRequired(),
			resolver.WithHint("Zendesk API token or password"),
		).
		EndSection().
	AddSection("Ticket Details").
		AddTextField("subject", "Subject",
			resolver.WithRequired(),
			resolver.WithPlaceholder("Issue with login"),
		).
		AddTextareaField("description", "Description",
			resolver.WithRequired(),
			resolver.WithRows(5),
			resolver.WithPlaceholder("Describe the issue in detail..."),
		).
		AddSelectField("priority", "Priority", []resolver.SelectOption{
			{Label: "Low", Value: "low"},
			{Label: "Normal", Value: "normal", Icon: "circle"},
			{Label: "High", Value: "high", Icon: "alert-circle"},
			{Label: "Urgent", Value: "urgent", Icon: "alert-triangle"},
		}, resolver.WithDefault("normal")).
		AddSelectField("status", "Status", []resolver.SelectOption{
			{Label: "New", Value: "new"},
			{Label: "Open", Value: "open"},
			{Label: "Pending", Value: "pending"},
			{Label: "Hold", Value: "hold"},
			{Label: "Solved", Value: "solved"},
			{Label: "Closed", Value: "closed"},
		}, resolver.WithDefault("new")).
		AddSelectField("type", "Type", []resolver.SelectOption{
			{Label: "Question", Value: "question"},
			{Label: "Incident", Value: "incident"},
			{Label: "Problem", Value: "problem"},
			{Label: "Task", Value: "task"},
		}, resolver.WithDefault("question")).
		EndSection().
	AddSection("Requester").
		AddNumberField("requesterId", "Requester ID",
			resolver.WithHint("Existing user ID. Leave empty to create new requester"),
		).
		AddJSONField("requester", "New Requester Details",
			resolver.WithHint("JSON with name and email if creating new requester"),
		).
		EndSection().
	AddSection("Organization").
		AddTagsField("tags", "Tags",
			resolver.WithHint("Labels to categorize the ticket"),
		).
		AddJSONField("customFields", "Custom Fields",
			resolver.WithHint("Array of {id, value} objects for custom fields"),
		).
		EndSection().
	Build()

func (e *ZendeskTicketCreateExecutor) Type() string { return "zendesk-ticket-create" }

func (e *ZendeskTicketCreateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	// Parse config into typed struct
	var cfg ZendeskTicketCreateConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	}

	if cfg.Subdomain == "" {
		return nil, fmt.Errorf("subdomain is required")
	}
	if cfg.Email == "" {
		return nil, fmt.Errorf("email is required")
	}
	if cfg.APIToken == "" {
		return nil, fmt.Errorf("apiToken is required")
	}
	if cfg.Subject == "" {
		return nil, fmt.Errorf("subject is required")
	}
	if cfg.Description == "" {
		return nil, fmt.Errorf("description is required")
	}

	client := NewZendeskClient(cfg.Subdomain, cfg.Email, cfg.APIToken)

	// Build ticket payload
	ticket := map[string]interface{}{
		"ticket": map[string]interface{}{
			"subject":     cfg.Subject,
			"description": cfg.Description,
			"priority":    cfg.Priority,
			"status":      cfg.Status,
			"type":        cfg.Type,
		},
	}

	if cfg.RequesterID > 0 {
		ticket["ticket"].(map[string]interface{})["requester_id"] = cfg.RequesterID
	} else if cfg.Requester != nil && len(cfg.Requester) > 0 {
		ticket["ticket"].(map[string]interface{})["requester"] = cfg.Requester
	}

	if len(cfg.Tags) > 0 {
		ticket["ticket"].(map[string]interface{})["tags"] = cfg.Tags
	}

	if len(cfg.CustomFields) > 0 {
		ticket["ticket"].(map[string]interface{})["custom_fields"] = cfg.CustomFields
	}

	respBody, err := client.doRequest(ctx, "POST", "/tickets.json", ticket)
	if err != nil {
		return nil, fmt.Errorf("failed to create ticket: %w", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &executor.StepResult{
		Output: result,
	}, nil
}

// ZendeskTicketUpdateExecutor handles zendesk-ticket-update node type
type ZendeskTicketUpdateExecutor struct{}

// ZendeskTicketUpdateConfig defines the typed configuration for zendesk-ticket-update
type ZendeskTicketUpdateConfig struct {
	Subdomain   string                 `json:"subdomain" description:"Zendesk subdomain"`
	Email       string                 `json:"email" description:"Zendesk API email"`
	APIToken    string                 `json:"apiToken" description:"Zendesk API token"`
	TicketID    int64                  `json:"ticketId" description:"Ticket ID to update"`
	Subject     string                 `json:"subject" description:"New subject (optional)"`
	Description string                 `json:"description" description:"New description (optional)"`
	Comment     string                 `json:"comment" description:"Add a comment to the ticket"`
	Priority    string                 `json:"priority" options:"low,normal,high,urgent" description:"New priority"`
	Status      string                 `json:"status" options:"new,open,pending,hold,solved,closed" description:"New status"`
	Type        string                 `json:"type" options:"question,incident,problem,task" description:"New type"`
	AssigneeID  int64                  `json:"assigneeId" description:"Assign to user ID"`
	Tags        []string               `json:"tags" description:"Ticket tags (replaces existing)"`
	CustomFields []map[string]interface{} `json:"customFields" description:"Custom field values"`
}

// ZendeskTicketUpdateSchema is the UI schema for zendesk-ticket-update
var ZendeskTicketUpdateSchema = resolver.NewSchemaBuilder("zendesk-ticket-update").
	WithName("Update Zendesk Ticket").
	WithCategory("support").
	WithIcon("edit").
	WithDescription("Update an existing Zendesk ticket").
	AddSection("Authentication").
		AddTextField("subdomain", "Zendesk Subdomain",
			resolver.WithRequired(),
		).
		AddTextField("email", "API Email",
			resolver.WithRequired(),
		).
		AddTextField("apiToken", "API Token",
			resolver.WithRequired(),
		).
		EndSection().
	AddSection("Ticket").
		AddNumberField("ticketId", "Ticket ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("12345"),
		).
		EndSection().
	AddSection("Updates").
		AddTextField("subject", "Subject",
			resolver.WithHint("Leave empty to keep current"),
		).
		AddTextareaField("description", "Description",
			resolver.WithRows(4),
			resolver.WithHint("Leave empty to keep current"),
		).
		AddTextareaField("comment", "Add Comment",
			resolver.WithRows(3),
			resolver.WithHint("Add a public comment to the ticket"),
		).
		AddSelectField("priority", "Priority", []resolver.SelectOption{
			{Label: "Low", Value: "low"},
			{Label: "Normal", Value: "normal"},
			{Label: "High", Value: "high"},
			{Label: "Urgent", Value: "urgent"},
		}).
		AddSelectField("status", "Status", []resolver.SelectOption{
			{Label: "New", Value: "new"},
			{Label: "Open", Value: "open"},
			{Label: "Pending", Value: "pending"},
			{Label: "Hold", Value: "hold"},
			{Label: "Solved", Value: "solved"},
			{Label: "Closed", Value: "closed"},
		}).
		AddSelectField("type", "Type", []resolver.SelectOption{
			{Label: "Question", Value: "question"},
			{Label: "Incident", Value: "incident"},
			{Label: "Problem", Value: "problem"},
			{Label: "Task", Value: "task"},
		}).
		AddNumberField("assigneeId", "Assignee ID",
			resolver.WithHint("User ID to assign ticket to"),
		).
		EndSection().
	AddSection("Organization").
		AddTagsField("tags", "Tags",
			resolver.WithHint("Replaces existing tags"),
		).
		AddJSONField("customFields", "Custom Fields",
			resolver.WithHint("Array of {id, value} objects"),
		).
		EndSection().
	Build()

func (e *ZendeskTicketUpdateExecutor) Type() string { return "zendesk-ticket-update" }

func (e *ZendeskTicketUpdateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg ZendeskTicketUpdateConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	}

	if cfg.Subdomain == "" {
		return nil, fmt.Errorf("subdomain is required")
	}
	if cfg.Email == "" {
		return nil, fmt.Errorf("email is required")
	}
	if cfg.APIToken == "" {
		return nil, fmt.Errorf("apiToken is required")
	}
	if cfg.TicketID == 0 {
		return nil, fmt.Errorf("ticketId is required")
	}

	client := NewZendeskClient(cfg.Subdomain, cfg.Email, cfg.APIToken)

	// Build ticket update payload
	ticketUpdate := map[string]interface{}{}

	if cfg.Subject != "" {
		ticketUpdate["subject"] = cfg.Subject
	}
	if cfg.Description != "" {
		ticketUpdate["description"] = cfg.Description
	}
	if cfg.Priority != "" {
		ticketUpdate["priority"] = cfg.Priority
	}
	if cfg.Status != "" {
		ticketUpdate["status"] = cfg.Status
	}
	if cfg.Type != "" {
		ticketUpdate["type"] = cfg.Type
	}
	if cfg.AssigneeID > 0 {
		ticketUpdate["assignee_id"] = cfg.AssigneeID
	}
	if len(cfg.Tags) > 0 {
		ticketUpdate["tags"] = cfg.Tags
	}
	if len(cfg.CustomFields) > 0 {
		ticketUpdate["custom_fields"] = cfg.CustomFields
	}

	payload := map[string]interface{}{
		"ticket": ticketUpdate,
	}

	// Add comment if provided
	if cfg.Comment != "" {
		ticketUpdate["comment"] = map[string]interface{}{
			"body": cfg.Comment,
		}
	}

	respBody, err := client.doRequest(ctx, "PUT", fmt.Sprintf("/tickets/%d.json", cfg.TicketID), payload)
	if err != nil {
		return nil, fmt.Errorf("failed to update ticket: %w", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &executor.StepResult{
		Output: result,
	}, nil
}

// ZendeskTicketSearchExecutor handles zendesk-ticket-search node type
type ZendeskTicketSearchExecutor struct{}

// ZendeskTicketSearchConfig defines the typed configuration for zendesk-ticket-search
type ZendeskTicketSearchConfig struct {
	Subdomain string   `json:"subdomain" description:"Zendesk subdomain"`
	Email     string   `json:"email" description:"Zendesk API email"`
	APIToken  string   `json:"apiToken" description:"Zendesk API token"`
	Query     string   `json:"query" description:"Search query (Zendesk search syntax)"`
	Status    string   `json:"status" options:"new,open,pending,hold,solved,closed" description:"Filter by status"`
	Type      string   `json:"type" options:"question,incident,problem,task" description:"Filter by type"`
	Priority  string   `json:"priority" options:"low,normal,high,urgent" description:"Filter by priority"`
	Assignee  string   `json:"assignee" description:"Filter by assignee email or ID"`
	Requester string   `json:"requester" description:"Filter by requester email or ID"`
	Tags      []string `json:"tags" description:"Filter by tags"`
	Page      int      `json:"page" default:"1" description:"Page number"`
	PerPage   int      `json:"perPage" default:"20" description:"Results per page (max 100)"`
}

// ZendeskTicketSearchSchema is the UI schema for zendesk-ticket-search
var ZendeskTicketSearchSchema = resolver.NewSchemaBuilder("zendesk-ticket-search").
	WithName("Search Zendesk Tickets").
	WithCategory("support").
	WithIcon("search").
	WithDescription("Search for tickets in Zendesk").
	AddSection("Authentication").
		AddTextField("subdomain", "Zendesk Subdomain",
			resolver.WithRequired(),
		).
		AddTextField("email", "API Email",
			resolver.WithRequired(),
		).
		AddTextField("apiToken", "API Token",
			resolver.WithRequired(),
		).
		EndSection().
	AddSection("Search").
		AddTextareaField("query", "Search Query",
			resolver.WithRows(3),
			resolver.WithPlaceholder("status:open type:question"),
			resolver.WithHint("Zendesk search syntax. Leave empty and use filters below"),
		).
		AddSelectField("status", "Status", []resolver.SelectOption{
			{Label: "Any", Value: ""},
			{Label: "New", Value: "new"},
			{Label: "Open", Value: "open"},
			{Label: "Pending", Value: "pending"},
			{Label: "Hold", Value: "hold"},
			{Label: "Solved", Value: "solved"},
			{Label: "Closed", Value: "closed"},
		}).
		AddSelectField("type", "Type", []resolver.SelectOption{
			{Label: "Any", Value: ""},
			{Label: "Question", Value: "question"},
			{Label: "Incident", Value: "incident"},
			{Label: "Problem", Value: "problem"},
			{Label: "Task", Value: "task"},
		}).
		AddSelectField("priority", "Priority", []resolver.SelectOption{
			{Label: "Any", Value: ""},
			{Label: "Low", Value: "low"},
			{Label: "Normal", Value: "normal"},
			{Label: "High", Value: "high"},
			{Label: "Urgent", Value: "urgent"},
		}).
		EndSection().
	AddSection("Filters").
		AddTextField("assignee", "Assignee",
			resolver.WithHint("Email or user ID"),
		).
		AddTextField("requester", "Requester",
			resolver.WithHint("Email or user ID"),
		).
		AddTagsField("tags", "Tags").
		EndSection().
	AddSection("Pagination").
		AddNumberField("page", "Page",
			resolver.WithDefault(1),
		).
		AddNumberField("perPage", "Per Page",
			resolver.WithDefault(20),
			resolver.WithHint("Max 100"),
		).
		EndSection().
	Build()

func (e *ZendeskTicketSearchExecutor) Type() string { return "zendesk-ticket-search" }

func (e *ZendeskTicketSearchExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg ZendeskTicketSearchConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	}

	if cfg.Subdomain == "" {
		return nil, fmt.Errorf("subdomain is required")
	}
	if cfg.Email == "" {
		return nil, fmt.Errorf("email is required")
	}
	if cfg.APIToken == "" {
		return nil, fmt.Errorf("apiToken is required")
	}

	client := NewZendeskClient(cfg.Subdomain, cfg.Email, cfg.APIToken)

	// Build search query
	queryParts := []string{}

	if cfg.Query != "" {
		queryParts = append(queryParts, cfg.Query)
	}
	if cfg.Status != "" {
		queryParts = append(queryParts, fmt.Sprintf("status:%s", cfg.Status))
	}
	if cfg.Type != "" {
		queryParts = append(queryParts, fmt.Sprintf("type:%s", cfg.Type))
	}
	if cfg.Priority != "" {
		queryParts = append(queryParts, fmt.Sprintf("priority:%s", cfg.Priority))
	}
	if cfg.Assignee != "" {
		queryParts = append(queryParts, fmt.Sprintf("assignee:%s", cfg.Assignee))
	}
	if cfg.Requester != "" {
		queryParts = append(queryParts, fmt.Sprintf("requester:%s", cfg.Requester))
	}
	for _, tag := range cfg.Tags {
		queryParts = append(queryParts, fmt.Sprintf("tags:%s", tag))
	}

	query := strings.Join(queryParts, " ")
	if query == "" {
		query = "status<closed" // Default to open tickets
	}

	// Build URL with pagination
	page := cfg.Page
	if page < 1 {
		page = 1
	}
	perPage := cfg.PerPage
	if perPage < 1 {
		perPage = 20
	}
	if perPage > 100 {
		perPage = 100
	}

	endpoint := fmt.Sprintf("/search.json?query=%s&page=%d&per_page=%d", query, page, perPage)

	respBody, err := client.doRequest(ctx, "GET", endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to search tickets: %w", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &executor.StepResult{
		Output: result,
	}, nil
}

// ZendeskUserExecutor handles zendesk-user node type
type ZendeskUserExecutor struct{}

// ZendeskUserConfig defines the typed configuration for zendesk-user
type ZendeskUserConfig struct {
	Subdomain string   `json:"subdomain" description:"Zendesk subdomain"`
	Email     string   `json:"email" description:"Zendesk API email"`
	APIToken  string   `json:"apiToken" description:"Zendesk API token"`
	Operation string   `json:"operation" options:"get,create,update,search,list" description:"User operation"`
	UserID    int64    `json:"userId" description:"User ID for get/update operations"`
	Name      string   `json:"name" description:"User name for create/update"`
	UserEmail string   `json:"userEmail" description:"User email for create/update/search"`
	Role      string   `json:"role" default:"end-user" options:"end-user,agent,admin" description:"User role"`
	Phone     string   `json:"phone" description:"User phone number"`
	Notes     string   `json:"notes" description:"User notes"`
	Tags      []string `json:"tags" description:"User tags"`
}

// ZendeskUserSchema is the UI schema for zendesk-user
var ZendeskUserSchema = resolver.NewSchemaBuilder("zendesk-user").
	WithName("Zendesk User Operations").
	WithCategory("support").
	WithIcon("users").
	WithDescription("Manage Zendesk users").
	AddSection("Authentication").
		AddTextField("subdomain", "Zendesk Subdomain",
			resolver.WithRequired(),
		).
		AddTextField("email", "API Email",
			resolver.WithRequired(),
		).
		AddTextField("apiToken", "API Token",
			resolver.WithRequired(),
		).
		EndSection().
	AddSection("Operation").
		AddSelectField("operation", "Operation", []resolver.SelectOption{
			{Label: "Get User", Value: "get", Icon: "user"},
			{Label: "Create User", Value: "create", Icon: "user-plus"},
			{Label: "Update User", Value: "update", Icon: "user-edit"},
			{Label: "Search Users", Value: "search", Icon: "search"},
			{Label: "List Users", Value: "list", Icon: "list"},
		}, resolver.WithRequired()).
		AddNumberField("userId", "User ID",
			resolver.WithHint("Required for get/update operations"),
		).
		EndSection().
	AddSection("User Details").
		AddTextField("name", "Name",
			resolver.WithHint("Required for create, optional for update"),
		).
		AddTextField("userEmail", "Email",
			resolver.WithHint("Required for create/search, optional for update"),
		).
		AddSelectField("role", "Role", []resolver.SelectOption{
			{Label: "End User", Value: "end-user"},
			{Label: "Agent", Value: "agent"},
			{Label: "Admin", Value: "admin"},
		}, resolver.WithDefault("end-user")).
		AddTextField("phone", "Phone",
			resolver.WithHint("Optional"),
		).
		AddTextareaField("notes", "Notes",
			resolver.WithRows(3),
			resolver.WithHint("Optional notes about the user"),
		).
		AddTagsField("tags", "Tags").
		EndSection().
	Build()

func (e *ZendeskUserExecutor) Type() string { return "zendesk-user" }

func (e *ZendeskUserExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg ZendeskUserConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	}

	if cfg.Subdomain == "" {
		return nil, fmt.Errorf("subdomain is required")
	}
	if cfg.Email == "" {
		return nil, fmt.Errorf("email is required")
	}
	if cfg.APIToken == "" {
		return nil, fmt.Errorf("apiToken is required")
	}
	if cfg.Operation == "" {
		return nil, fmt.Errorf("operation is required")
	}

	client := NewZendeskClient(cfg.Subdomain, cfg.Email, cfg.APIToken)

	var endpoint string
	var method string
	var payload map[string]interface{}

	switch cfg.Operation {
	case "get":
		if cfg.UserID == 0 {
			return nil, fmt.Errorf("userId is required for get operation")
		}
		endpoint = fmt.Sprintf("/users/%d.json", cfg.UserID)
		method = "GET"

	case "create":
		if cfg.Name == "" {
			return nil, fmt.Errorf("name is required for create operation")
		}
		if cfg.UserEmail == "" {
			return nil, fmt.Errorf("userEmail is required for create operation")
		}
		endpoint = "/users.json"
		method = "POST"
		userData := map[string]interface{}{
			"name":  cfg.Name,
			"email": cfg.UserEmail,
			"role":  cfg.Role,
		}
		if cfg.Phone != "" {
			userData["phone"] = cfg.Phone
		}
		if cfg.Notes != "" {
			userData["notes"] = cfg.Notes
		}
		if len(cfg.Tags) > 0 {
			userData["tags"] = cfg.Tags
		}
		payload = map[string]interface{}{"user": userData}

	case "update":
		if cfg.UserID == 0 {
			return nil, fmt.Errorf("userId is required for update operation")
		}
		endpoint = fmt.Sprintf("/users/%d.json", cfg.UserID)
		method = "PUT"
		userData := map[string]interface{}{}
		if cfg.Name != "" {
			userData["name"] = cfg.Name
		}
		if cfg.UserEmail != "" {
			userData["email"] = cfg.UserEmail
		}
		if cfg.Role != "" {
			userData["role"] = cfg.Role
		}
		if cfg.Phone != "" {
			userData["phone"] = cfg.Phone
		}
		if cfg.Notes != "" {
			userData["notes"] = cfg.Notes
		}
		if len(cfg.Tags) > 0 {
			userData["tags"] = cfg.Tags
		}
		payload = map[string]interface{}{"user": userData}

	case "search":
		if cfg.UserEmail == "" {
			return nil, fmt.Errorf("userEmail is required for search operation")
		}
		endpoint = fmt.Sprintf("/users/search.json?query=%s", cfg.UserEmail)
		method = "GET"

	case "list":
		endpoint = "/users.json"
		method = "GET"

	default:
		return nil, fmt.Errorf("unknown operation: %s", cfg.Operation)
	}

	respBody, err := client.doRequest(ctx, method, endpoint, payload)
	if err != nil {
		return nil, fmt.Errorf("failed to execute user operation: %w", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &executor.StepResult{
		Output: result,
	}, nil
}

// ZendeskMacroExecutor handles zendesk-macro node type
type ZendeskMacroExecutor struct{}

// ZendeskMacroConfig defines the typed configuration for zendesk-macro
type ZendeskMacroConfig struct {
	Subdomain string `json:"subdomain" description:"Zendesk subdomain"`
	Email     string `json:"email" description:"Zendesk API email"`
	APIToken  string `json:"apiToken" description:"Zendesk API token"`
	Operation string `json:"operation" options:"list,get,apply" description:"Macro operation"`
	MacroID   int64  `json:"macroId" description:"Macro ID for get/apply operations"`
	TicketID  int64  `json:"ticketId" description:"Ticket ID to apply macro to"`
}

// ZendeskMacroSchema is the UI schema for zendesk-macro
var ZendeskMacroSchema = resolver.NewSchemaBuilder("zendesk-macro").
	WithName("Zendesk Macro Operations").
	WithCategory("support").
	WithIcon("zap").
	WithDescription("List, get, and apply Zendesk macros").
	AddSection("Authentication").
		AddTextField("subdomain", "Zendesk Subdomain",
			resolver.WithRequired(),
		).
		AddTextField("email", "API Email",
			resolver.WithRequired(),
		).
		AddTextField("apiToken", "API Token",
			resolver.WithRequired(),
		).
		EndSection().
	AddSection("Operation").
		AddSelectField("operation", "Operation", []resolver.SelectOption{
			{Label: "List Macros", Value: "list", Icon: "list"},
			{Label: "Get Macro", Value: "get", Icon: "file-text"},
			{Label: "Apply Macro", Value: "apply", Icon: "play"},
		}, resolver.WithRequired()).
		AddNumberField("macroId", "Macro ID",
			resolver.WithHint("Required for get/apply operations"),
		).
		AddNumberField("ticketId", "Ticket ID",
			resolver.WithHint("Required for apply operation"),
		).
		EndSection().
	Build()

func (e *ZendeskMacroExecutor) Type() string { return "zendesk-macro" }

func (e *ZendeskMacroExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg ZendeskMacroConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	}

	if cfg.Subdomain == "" {
		return nil, fmt.Errorf("subdomain is required")
	}
	if cfg.Email == "" {
		return nil, fmt.Errorf("email is required")
	}
	if cfg.APIToken == "" {
		return nil, fmt.Errorf("apiToken is required")
	}
	if cfg.Operation == "" {
		return nil, fmt.Errorf("operation is required")
	}

	client := NewZendeskClient(cfg.Subdomain, cfg.Email, cfg.APIToken)

	var endpoint string
	var method string
	var payload map[string]interface{}

	switch cfg.Operation {
	case "list":
		endpoint = "/macros.json"
		method = "GET"

	case "get":
		if cfg.MacroID == 0 {
			return nil, fmt.Errorf("macroId is required for get operation")
		}
		endpoint = fmt.Sprintf("/macros/%d.json", cfg.MacroID)
		method = "GET"

	case "apply":
		if cfg.MacroID == 0 {
			return nil, fmt.Errorf("macroId is required for apply operation")
		}
		if cfg.TicketID == 0 {
			return nil, fmt.Errorf("ticketId is required for apply operation")
		}
		endpoint = fmt.Sprintf("/tickets/%d/macros/%d/apply.json", cfg.TicketID, cfg.MacroID)
		method = "PUT"
		payload = map[string]interface{}{}

	default:
		return nil, fmt.Errorf("unknown operation: %s", cfg.Operation)
	}

	respBody, err := client.doRequest(ctx, method, endpoint, payload)
	if err != nil {
		return nil, fmt.Errorf("failed to execute macro operation: %w", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &executor.StepResult{
		Output: result,
	}, nil
}

// ZendeskViewExecutor handles zendesk-view node type
type ZendeskViewExecutor struct{}

// ZendeskViewConfig defines the typed configuration for zendesk-view
type ZendeskViewConfig struct {
	Subdomain string `json:"subdomain" description:"Zendesk subdomain"`
	Email     string `json:"email" description:"Zendesk API email"`
	APIToken  string `json:"apiToken" description:"Zendesk API token"`
	Operation string `json:"operation" options:"list,get,tickets" description:"View operation"`
	ViewID    int64  `json:"viewId" description:"View ID for get/tickets operations"`
	Page      int    `json:"page" default:"1" description:"Page number for tickets"`
}

// ZendeskViewSchema is the UI schema for zendesk-view
var ZendeskViewSchema = resolver.NewSchemaBuilder("zendesk-view").
	WithName("Zendesk View Operations").
	WithCategory("support").
	WithIcon("eye").
	WithDescription("List views and get tickets from views").
	AddSection("Authentication").
		AddTextField("subdomain", "Zendesk Subdomain",
			resolver.WithRequired(),
		).
		AddTextField("email", "API Email",
			resolver.WithRequired(),
		).
		AddTextField("apiToken", "API Token",
			resolver.WithRequired(),
		).
		EndSection().
	AddSection("Operation").
		AddSelectField("operation", "Operation", []resolver.SelectOption{
			{Label: "List Views", Value: "list", Icon: "list"},
			{Label: "Get View", Value: "get", Icon: "eye"},
			{Label: "Get View Tickets", Value: "tickets", Icon: "ticket"},
		}, resolver.WithRequired()).
		AddNumberField("viewId", "View ID",
			resolver.WithHint("Required for get/tickets operations"),
		).
		AddNumberField("page", "Page",
			resolver.WithDefault(1),
			resolver.WithHint("For tickets operation"),
		).
		EndSection().
	Build()

func (e *ZendeskViewExecutor) Type() string { return "zendesk-view" }

func (e *ZendeskViewExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg ZendeskViewConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	}

	if cfg.Subdomain == "" {
		return nil, fmt.Errorf("subdomain is required")
	}
	if cfg.Email == "" {
		return nil, fmt.Errorf("email is required")
	}
	if cfg.APIToken == "" {
		return nil, fmt.Errorf("apiToken is required")
	}
	if cfg.Operation == "" {
		return nil, fmt.Errorf("operation is required")
	}

	client := NewZendeskClient(cfg.Subdomain, cfg.Email, cfg.APIToken)

	var endpoint string
	var method string

	switch cfg.Operation {
	case "list":
		endpoint = "/views.json"
		method = "GET"

	case "get":
		if cfg.ViewID == 0 {
			return nil, fmt.Errorf("viewId is required for get operation")
		}
		endpoint = fmt.Sprintf("/views/%d.json", cfg.ViewID)
		method = "GET"

	case "tickets":
		if cfg.ViewID == 0 {
			return nil, fmt.Errorf("viewId is required for tickets operation")
		}
		page := cfg.Page
		if page < 1 {
			page = 1
		}
		endpoint = fmt.Sprintf("/views/%d/tickets.json?page=%d", cfg.ViewID, page)
		method = "GET"

	default:
		return nil, fmt.Errorf("unknown operation: %s", cfg.Operation)
	}

	respBody, err := client.doRequest(ctx, method, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to execute view operation: %w", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &executor.StepResult{
		Output: result,
	}, nil
}
