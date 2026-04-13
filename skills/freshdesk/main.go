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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/axiom-studio/skills.sdk/executor"
	"github.com/axiom-studio/skills.sdk/grpc"
	"github.com/axiom-studio/skills.sdk/resolver"
)

const (
	iconFreshdesk = "life-buoy"
)

// Freshdesk client cache
var (
	clients     = make(map[string]*FreshdeskClient)
	clientMutex sync.RWMutex
)

// FreshdeskClient represents a Freshdesk API client
type FreshdeskClient struct {
	Domain     string
	APIKey     string
	HTTPClient *http.Client
}

// ============================================================================
// FRESHDESK API RESPONSE TYPES
// ============================================================================

// FreshdeskTicket represents a Freshdesk ticket
type FreshdeskTicket struct {
	ID              int64             `json:"id,omitempty"`
	Subject         string            `json:"subject,omitempty"`
	Description     string            `json:"description,omitempty"`
	DescriptionText string            `json:"description_text,omitempty"`
	Status          int               `json:"status,omitempty"`
	Priority        int               `json:"priority,omitempty"`
	Type            string            `json:"type,omitempty"`
	Source          int               `json:"source,omitempty"`
	CreatedAt       string            `json:"created_at,omitempty"`
	UpdatedAt       string            `json:"updated_at,omitempty"`
	DueBy           string            `json:"due_by,omitempty"`
	FrDueBy         string            `json:"fr_due_by,omitempty"`
	IsEscalated     bool              `json:"is_escalated,omitempty"`
	AssigneeID      int64             `json:"assignee_id,omitempty"`
	GroupID         int64             `json:"group_id,omitempty"`
	RequesterID     int64             `json:"requester_id,omitempty"`
	Email           string            `json:"email,omitempty"`
	Name            string            `json:"name,omitempty"`
	Tags            []string          `json:"tags,omitempty"`
	CCEmails        []string          `json:"cc_emails,omitempty"`
	FrEscalated     bool              `json:"fr_escalated,omitempty"`
	Spam            bool              `json:"spam,omitempty"`
	Deleted         bool              `json:"deleted,omitempty"`
	CustomFields    map[string]interface{} `json:"custom_fields,omitempty"`
	ToEmails        []string          `json:"to_emails,omitempty"`
	ProductID       int64             `json:"product_id,omitempty"`
	CompanyID       int64             `json:"company_id,omitempty"`
}

// FreshdeskAgent represents a Freshdesk agent
type FreshdeskAgent struct {
	ID             int64              `json:"id,omitempty"`
	Email          string             `json:"email,omitempty"`
	Name           string             `json:"name,omitempty"`
	Mobile         string             `json:"mobile,omitempty"`
	Phone          string             `json:"phone,omitempty"`
	Active         bool               `json:"active,omitempty"`
	Available      bool               `json:"available,omitempty"`
	AvailableSince string             `json:"available_since,omitempty"`
	Type           string             `json:"type,omitempty"`
	Occasional     bool               `json:"occasional,omitempty"`
	Signature      string             `json:"signature,omitempty"`
	TicketScope    int                `json:"ticket_scope,omitempty"`
	GroupIDs       []int64            `json:"group_ids,omitempty"`
	RoleIDs        []int64            `json:"role_ids,omitempty"`
	CreatedAt      string             `json:"created_at,omitempty"`
	UpdatedAt      string             `json:"updated_at,omitempty"`
	LastActiveAt   string             `json:"last_active_at,omitempty"`
	LastLoginAt    string             `json:"last_login_at,omitempty"`
	Contact        FreshdeskContact   `json:"contact,omitempty"`
}

// FreshdeskContact represents a Freshdesk contact
type FreshdeskContact struct {
	ID               int64             `json:"id,omitempty"`
	Name             string            `json:"name,omitempty"`
	Email            string            `json:"email,omitempty"`
	Phone            string            `json:"phone,omitempty"`
	Mobile           string            `json:"mobile,omitempty"`
	TwitterID        string            `json:"twitter_id,omitempty"`
	UniqueExternalID string            `json:"unique_external_id,omitempty"`
	Language         string            `json:"language,omitempty"`
	Timezone         string            `json:"time_zone,omitempty"`
	CreatedAt        string            `json:"created_at,omitempty"`
	UpdatedAt        string            `json:"updated_at,omitempty"`
	CompanyID        int64             `json:"company_id,omitempty"`
	ViewAllTickets   bool              `json:"view_all_tickets,omitempty"`
	Deleted          bool              `json:"deleted,omitempty"`
	Active           bool              `json:"active,omitempty"`
	Address          string            `json:"address,omitempty"`
	Description      string            `json:"description,omitempty"`
	JobTitle         string            `json:"job_title,omitempty"`
	Tags             []string          `json:"tags,omitempty"`
	CustomFields     map[string]interface{} `json:"custom_fields,omitempty"`
	OtherEmails      []string          `json:"other_emails,omitempty"`
	OtherPhones      []string          `json:"other_phones,omitempty"`
	SocialProfiles   []SocialProfile   `json:"social_profiles,omitempty"`
	Avatar           Avatar            `json:"avatar,omitempty"`
}

// SocialProfile represents a social media profile
type SocialProfile struct {
	Provider string `json:"provider,omitempty"`
	URL      string `json:"url,omitempty"`
}

// Avatar represents an avatar image
type Avatar struct {
	AvatarURL string `json:"avatar_url,omitempty"`
}

// FreshdeskSolution represents a solution folder/category
type FreshdeskSolution struct {
	ID               int64  `json:"id,omitempty"`
	Name             string `json:"name,omitempty"`
	Description      string `json:"description,omitempty"`
	Position         int    `json:"position,omitempty"`
	CreatedAt        string `json:"created_at,omitempty"`
	UpdatedAt        string `json:"updated_at,omitempty"`
	VisibleInPortals []int  `json:"visible_in_portals,omitempty"`
	CategoryID       int64  `json:"category_id,omitempty"`
	FolderID         int64  `json:"folder_id,omitempty"`
	ParentID         int64  `json:"parent_id,omitempty"`
}

// FreshdeskArticle represents a knowledge base article
type FreshdeskArticle struct {
	ID              int64             `json:"id,omitempty"`
	Type            int               `json:"type,omitempty"`
	CategoryID      int64             `json:"category_id,omitempty"`
	FolderID        int64             `json:"folder_id,omitempty"`
	Title           string            `json:"title,omitempty"`
	Description     string            `json:"description,omitempty"`
	DescriptionText string            `json:"description_text,omitempty"`
	Status          int               `json:"status,omitempty"`
	AgentID         int64             `json:"agent_id,omitempty"`
	Views           int64             `json:"views,omitempty"`
	Tags            []string          `json:"tags,omitempty"`
	SEOData         SEOData           `json:"seo_data,omitempty"`
	ThumbsUp        int64             `json:"thumbs_up,omitempty"`
	ThumbsDown      int64             `json:"thumbs_down,omitempty"`
	CreatedAt       string            `json:"created_at,omitempty"`
	UpdatedAt       string            `json:"updated_at,omitempty"`
	CloudFiles      []CloudFile       `json:"cloud_files,omitempty"`
	Attachments     []Attachment      `json:"attachments,omitempty"`
}

// SEOData represents SEO metadata for an article
type SEOData struct {
	MetaTitle       string `json:"meta_title,omitempty"`
	MetaDescription string `json:"meta_description,omitempty"`
	MetaKeywords    string `json:"meta_keywords,omitempty"`
}

// CloudFile represents a cloud file attachment
type CloudFile struct {
	ID            int64  `json:"id,omitempty"`
	Name          string `json:"name,omitempty"`
	ContentType   string `json:"content_type,omitempty"`
	Size          int64  `json:"size,omitempty"`
	AttachmentURL string `json:"attachment_url,omitempty"`
	CreatedAt     string `json:"created_at,omitempty"`
	UpdatedAt     string `json:"updated_at,omitempty"`
}

// Attachment represents an article attachment
type Attachment struct {
	ID            int64  `json:"id,omitempty"`
	Name          string `json:"name,omitempty"`
	ContentType   string `json:"content_type,omitempty"`
	Size          int64  `json:"size,omitempty"`
	AttachmentURL string `json:"attachment_url,omitempty"`
	CreatedAt     string `json:"created_at,omitempty"`
	UpdatedAt     string `json:"updated_at,omitempty"`
}

// FreshdeskConversation represents a ticket conversation/note
type FreshdeskConversation struct {
	ID                int64        `json:"id,omitempty"`
	Body              string       `json:"body,omitempty"`
	BodyText          string       `json:"body_text,omitempty"`
	Incoming          bool         `json:"incoming,omitempty"`
	Private           bool         `json:"private,omitempty"`
	UserID            int64        `json:"user_id,omitempty"`
	SupportEmail      string       `json:"support_email,omitempty"`
	Source            int          `json:"source,omitempty"`
	Category          int          `json:"category,omitempty"`
	ToEmails          []string     `json:"to_emails,omitempty"`
	FromEmail         string       `json:"from_email,omitempty"`
	CCEmails          []string     `json:"cc_emails,omitempty"`
	BCCEmails         []string     `json:"bcc_emails,omitempty"`
	EmailFailureCount int          `json:"email_failure_count,omitempty"`
	TicketID          int64        `json:"ticket_id,omitempty"`
	CreatedAt         string       `json:"created_at,omitempty"`
	UpdatedAt         string       `json:"updated_at,omitempty"`
	Attachments       []Attachment `json:"attachments,omitempty"`
	NotifyEmails      []string     `json:"notify_emails,omitempty"`
}

// FreshdeskError represents an API error response
type FreshdeskError struct {
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
	Errors  []struct {
		Field   string `json:"field,omitempty"`
		Message string `json:"message,omitempty"`
	} `json:"errors,omitempty"`
}

func main() {
	// Get port from env or use default
	port := os.Getenv("SKILL_PORT")
	if port == "" {
		port = "50112"
	}

	// Create skill server
	server := grpc.NewSkillServer("skill-freshdesk", "1.0.0")

	// Register Ticket executors with schemas
	server.RegisterExecutorWithSchema("freshdesk-ticket-list", &TicketListExecutor{}, TicketListSchema)
	server.RegisterExecutorWithSchema("freshdesk-ticket-create", &TicketCreateExecutor{}, TicketCreateSchema)
	server.RegisterExecutorWithSchema("freshdesk-ticket-update", &TicketUpdateExecutor{}, TicketUpdateSchema)
	server.RegisterExecutorWithSchema("freshdesk-ticket-reply", &TicketReplyExecutor{}, TicketReplySchema)

	// Register Agent executor with schema
	server.RegisterExecutorWithSchema("freshdesk-agent-list", &AgentListExecutor{}, AgentListSchema)

	// Register Contact executor with schema
	server.RegisterExecutorWithSchema("freshdesk-contact-list", &ContactListExecutor{}, ContactListSchema)

	// Register Solution executor with schema
	server.RegisterExecutorWithSchema("freshdesk-solution-create", &SolutionCreateExecutor{}, SolutionCreateSchema)

	// Register Article executor with schema
	server.RegisterExecutorWithSchema("freshdesk-article-list", &ArticleListExecutor{}, ArticleListSchema)

	fmt.Printf("Starting skill-freshdesk gRPC server on port %s\n", port)
	if err := server.Serve(port); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to serve: %v\n", err)
		os.Exit(1)
	}
}

// ============================================================================
// FRESHDESK CLIENT HELPERS
// ============================================================================

// getClient returns or creates a Freshdesk client (cached)
func getClient(domain, apiKey string) *FreshdeskClient {
	cacheKey := fmt.Sprintf("%s:%s", domain, apiKey)

	clientMutex.RLock()
	client, ok := clients[cacheKey]
	clientMutex.RUnlock()

	if ok {
		return client
	}

	clientMutex.Lock()
	defer clientMutex.Unlock()

	// Double check
	if client, ok := clients[cacheKey]; ok {
		return client
	}

	client = &FreshdeskClient{
		Domain: domain,
		APIKey: apiKey,
		HTTPClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
	clients[cacheKey] = client
	return client
}

// doRequest performs an HTTP request to the Freshdesk API
func (c *FreshdeskClient) doRequest(ctx context.Context, method, path string, body interface{}) (*http.Response, error) {
	var reqBody io.Reader
	if body != nil {
		jsonData, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
		reqBody = bytes.NewBuffer(jsonData)
	}

	url := fmt.Sprintf("https://%s/api/v2/%s", c.Domain, path)

	req, err := http.NewRequestWithContext(ctx, method, url, reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Freshdesk uses Basic Auth with API key as username and "X" as password
	auth := base64.StdEncoding.EncodeToString([]byte(c.APIKey + ":X"))
	req.Header.Set("Authorization", "Basic "+auth)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	return resp, nil
}

// decodeResponse decodes a Freshdesk API response
func decodeResponse(resp *http.Response, result interface{}) error {
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var errResp FreshdeskError
		if err := json.Unmarshal(body, &errResp); err == nil {
			if errResp.Message != "" {
				return fmt.Errorf("Freshdesk API error (%d): %s", resp.StatusCode, errResp.Message)
			}
			if len(errResp.Errors) > 0 {
				return fmt.Errorf("Freshdesk API error (%d): %s", resp.StatusCode, errResp.Errors[0].Message)
			}
		}
		return fmt.Errorf("Freshdesk API error (%d): %s", resp.StatusCode, string(body))
	}

	if err := json.Unmarshal(body, result); err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
	}

	return nil
}

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

// Helper to get int64 from config
func getInt64(config map[string]interface{}, key string, def int64) int64 {
	if v, ok := config[key]; ok {
		switch n := v.(type) {
		case float64:
			return int64(n)
		case int:
			return int64(n)
		case int64:
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
			if arr == "" {
				return nil
			}
			return []string{arr}
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
// SCHEMAS
// ============================================================================

// TicketListSchema is the UI schema for freshdesk-ticket-list
var TicketListSchema = resolver.NewSchemaBuilder("freshdesk-ticket-list").
	WithName("List Freshdesk Tickets").
	WithCategory("action").
	WithIcon(iconFreshdesk).
	WithDescription("List tickets from Freshdesk helpdesk").
	AddSection("Connection").
		AddExpressionField("domain", "Domain",
			resolver.WithRequired(),
			resolver.WithPlaceholder("yourcompany.freshdesk.com"),
			resolver.WithHint("Your Freshdesk domain"),
		).
		AddExpressionField("apiKey", "API Key",
			resolver.WithRequired(),
			resolver.WithSensitive(),
			resolver.WithHint("Freshdesk API key"),
		).
		EndSection().
	AddSection("Filters").
		AddTextField("email", "Requester Email",
			resolver.WithPlaceholder("user@example.com"),
			resolver.WithHint("Filter by requester email"),
		).
		AddTextField("companyId", "Company ID",
			resolver.WithPlaceholder("123456"),
			resolver.WithHint("Filter by company ID"),
		).
		AddTextField("userId", "User ID",
			resolver.WithPlaceholder("123456"),
			resolver.WithHint("Filter by user ID"),
		).
		AddSelectField("status", "Status", []resolver.SelectOption{
			{Label: "Open", Value: "2"},
			{Label: "Pending", Value: "3"},
			{Label: "Resolved", Value: "4"},
			{Label: "Closed", Value: "5"},
		},
			resolver.WithHint("Filter by ticket status"),
		).
		AddSelectField("priority", "Priority", []resolver.SelectOption{
			{Label: "Low", Value: "1"},
			{Label: "Medium", Value: "2"},
			{Label: "High", Value: "3"},
			{Label: "Urgent", Value: "4"},
		},
			resolver.WithHint("Filter by priority"),
		).
		EndSection().
	AddSection("Options").
		AddNumberField("page", "Page",
			resolver.WithDefault(1),
			resolver.WithHint("Page number"),
		).
		AddNumberField("perPage", "Per Page",
			resolver.WithDefault(30),
			resolver.WithMinMax(1, 100),
			resolver.WithHint("Results per page"),
		).
		AddTextField("orderBy", "Order By",
			resolver.WithPlaceholder("created_at"),
			resolver.WithHint("Field to order by (created_at, updated_at, due_by, etc.)"),
		).
		AddToggleField("descending", "Descending",
			resolver.WithDefault(true),
			resolver.WithHint("Sort in descending order"),
		).
		EndSection().
	Build()

// TicketCreateSchema is the UI schema for freshdesk-ticket-create
var TicketCreateSchema = resolver.NewSchemaBuilder("freshdesk-ticket-create").
	WithName("Create Freshdesk Ticket").
	WithCategory("action").
	WithIcon(iconFreshdesk).
	WithDescription("Create a new ticket in Freshdesk").
	AddSection("Connection").
		AddExpressionField("domain", "Domain",
			resolver.WithRequired(),
			resolver.WithPlaceholder("yourcompany.freshdesk.com"),
		).
		AddExpressionField("apiKey", "API Key",
			resolver.WithRequired(),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Ticket Details").
		AddTextField("subject", "Subject",
			resolver.WithRequired(),
			resolver.WithPlaceholder("Issue summary"),
		).
		AddTextareaField("description", "Description",
			resolver.WithRequired(),
			resolver.WithRows(5),
			resolver.WithPlaceholder("Detailed description of the issue"),
		).
		EndSection().
	AddSection("Requester").
		AddTextField("email", "Requester Email",
			resolver.WithRequired(),
			resolver.WithPlaceholder("user@example.com"),
			resolver.WithHint("Email of the ticket requester"),
		).
		AddTextField("name", "Requester Name",
			resolver.WithPlaceholder("John Doe"),
			resolver.WithHint("Name of the requester"),
		).
		EndSection().
	AddSection("Classification").
		AddSelectField("status", "Status", []resolver.SelectOption{
			{Label: "Open", Value: "2"},
			{Label: "Pending", Value: "3"},
			{Label: "Resolved", Value: "4"},
			{Label: "Closed", Value: "5"},
		},
			resolver.WithDefault("2"),
		).
		AddSelectField("priority", "Priority", []resolver.SelectOption{
			{Label: "Low", Value: "1"},
			{Label: "Medium", Value: "2"},
			{Label: "High", Value: "3"},
			{Label: "Urgent", Value: "4"},
		},
			resolver.WithDefault("2"),
		).
		AddSelectField("type", "Type", []resolver.SelectOption{
			{Label: "Question", Value: "Question"},
			{Label: "Incident", Value: "Incident"},
			{Label: "Problem", Value: "Problem"},
			{Label: "Feature Request", Value: "Feature Request"},
			{Label: "Refund", Value: "Refund"},
		},
			resolver.WithHint("Ticket type"),
		).
		EndSection().
	AddSection("Assignment").
		AddTextField("assigneeId", "Assignee ID",
			resolver.WithPlaceholder("123456"),
			resolver.WithHint("Agent ID to assign the ticket to"),
		).
		AddTextField("groupId", "Group ID",
			resolver.WithPlaceholder("123456"),
			resolver.WithHint("Group ID to assign the ticket to"),
		).
		EndSection().
	AddSection("Options").
		AddTagsField("tags", "Tags",
			resolver.WithHint("Tags to apply to the ticket"),
		).
		AddTextField("ccEmails", "CC Emails",
			resolver.WithPlaceholder("user1@example.com,user2@example.com"),
			resolver.WithHint("Comma-separated CC email addresses"),
		).
		EndSection().
	Build()

// TicketUpdateSchema is the UI schema for freshdesk-ticket-update
var TicketUpdateSchema = resolver.NewSchemaBuilder("freshdesk-ticket-update").
	WithName("Update Freshdesk Ticket").
	WithCategory("action").
	WithIcon(iconFreshdesk).
	WithDescription("Update an existing Freshdesk ticket").
	AddSection("Connection").
		AddExpressionField("domain", "Domain",
			resolver.WithRequired(),
			resolver.WithPlaceholder("yourcompany.freshdesk.com"),
		).
		AddExpressionField("apiKey", "API Key",
			resolver.WithRequired(),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Ticket").
		AddTextField("ticketId", "Ticket ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("123456"),
			resolver.WithHint("ID of the ticket to update"),
		).
		EndSection().
	AddSection("Updates").
		AddTextField("subject", "Subject",
			resolver.WithPlaceholder("New subject"),
		).
		AddTextareaField("description", "Description",
			resolver.WithRows(5),
			resolver.WithPlaceholder("New description"),
		).
		AddSelectField("status", "Status", []resolver.SelectOption{
			{Label: "Open", Value: "2"},
			{Label: "Pending", Value: "3"},
			{Label: "Resolved", Value: "4"},
			{Label: "Closed", Value: "5"},
		},
		).
		AddSelectField("priority", "Priority", []resolver.SelectOption{
			{Label: "Low", Value: "1"},
			{Label: "Medium", Value: "2"},
			{Label: "High", Value: "3"},
			{Label: "Urgent", Value: "4"},
		},
		).
		AddSelectField("type", "Type", []resolver.SelectOption{
			{Label: "Question", Value: "Question"},
			{Label: "Incident", Value: "Incident"},
			{Label: "Problem", Value: "Problem"},
			{Label: "Feature Request", Value: "Feature Request"},
			{Label: "Refund", Value: "Refund"},
		},
		).
		EndSection().
	AddSection("Assignment").
		AddTextField("assigneeId", "Assignee ID",
			resolver.WithPlaceholder("123456"),
			resolver.WithHint("Agent ID to assign the ticket to"),
		).
		AddTextField("groupId", "Group ID",
			resolver.WithPlaceholder("123456"),
			resolver.WithHint("Group ID to assign the ticket to"),
		).
		EndSection().
	AddSection("Options").
		AddTagsField("tags", "Tags",
			resolver.WithHint("Tags to set on the ticket"),
		).
		AddTextField("ccEmails", "CC Emails",
			resolver.WithPlaceholder("user1@example.com,user2@example.com"),
		).
		EndSection().
	Build()

// TicketReplySchema is the UI schema for freshdesk-ticket-reply
var TicketReplySchema = resolver.NewSchemaBuilder("freshdesk-ticket-reply").
	WithName("Reply to Freshdesk Ticket").
	WithCategory("action").
	WithIcon(iconFreshdesk).
	WithDescription("Add a reply or note to a Freshdesk ticket").
	AddSection("Connection").
		AddExpressionField("domain", "Domain",
			resolver.WithRequired(),
			resolver.WithPlaceholder("yourcompany.freshdesk.com"),
		).
		AddExpressionField("apiKey", "API Key",
			resolver.WithRequired(),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Ticket").
		AddTextField("ticketId", "Ticket ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("123456"),
			resolver.WithHint("ID of the ticket to reply to"),
		).
		EndSection().
	AddSection("Reply").
		AddTextareaField("body", "Message Body",
			resolver.WithRequired(),
			resolver.WithRows(6),
			resolver.WithPlaceholder("Your reply message..."),
		).
		EndSection().
	AddSection("Options").
		AddToggleField("private", "Private Note",
			resolver.WithDefault(false),
			resolver.WithHint("Add as a private note instead of public reply"),
		).
		AddTextField("fromEmail", "From Email",
			resolver.WithPlaceholder("support@yourcompany.com"),
			resolver.WithHint("Email address to send from"),
		).
		AddTextField("ccEmails", "CC Emails",
			resolver.WithPlaceholder("user1@example.com,user2@example.com"),
			resolver.WithHint("Comma-separated CC email addresses"),
		).
		AddTextField("bccEmails", "BCC Emails",
			resolver.WithPlaceholder("user1@example.com"),
			resolver.WithHint("Comma-separated BCC email addresses"),
		).
		AddTextField("notifyEmails", "Notify Emails",
			resolver.WithPlaceholder("user1@example.com"),
			resolver.WithHint("Email addresses to notify (without sending reply)"),
		).
		EndSection().
	Build()

// AgentListSchema is the UI schema for freshdesk-agent-list
var AgentListSchema = resolver.NewSchemaBuilder("freshdesk-agent-list").
	WithName("List Freshdesk Agents").
	WithCategory("action").
	WithIcon(iconFreshdesk).
	WithDescription("List agents from Freshdesk").
	AddSection("Connection").
		AddExpressionField("domain", "Domain",
			resolver.WithRequired(),
			resolver.WithPlaceholder("yourcompany.freshdesk.com"),
		).
		AddExpressionField("apiKey", "API Key",
			resolver.WithRequired(),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Filters").
		AddToggleField("active", "Active Only",
			resolver.WithDefault(true),
			resolver.WithHint("Show only active agents"),
		).
		EndSection().
	AddSection("Options").
		AddNumberField("page", "Page",
			resolver.WithDefault(1),
		).
		AddNumberField("perPage", "Per Page",
			resolver.WithDefault(30),
			resolver.WithMinMax(1, 100),
		).
		EndSection().
	Build()

// ContactListSchema is the UI schema for freshdesk-contact-list
var ContactListSchema = resolver.NewSchemaBuilder("freshdesk-contact-list").
	WithName("List Freshdesk Contacts").
	WithCategory("action").
	WithIcon(iconFreshdesk).
	WithDescription("List contacts from Freshdesk").
	AddSection("Connection").
		AddExpressionField("domain", "Domain",
			resolver.WithRequired(),
			resolver.WithPlaceholder("yourcompany.freshdesk.com"),
		).
		AddExpressionField("apiKey", "API Key",
			resolver.WithRequired(),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Filters").
		AddTextField("email", "Email",
			resolver.WithPlaceholder("user@example.com"),
			resolver.WithHint("Filter by email address"),
		).
		AddTextField("phone", "Phone",
			resolver.WithPlaceholder("+1234567890"),
			resolver.WithHint("Filter by phone number"),
		).
		AddTextField("companyId", "Company ID",
			resolver.WithPlaceholder("123456"),
			resolver.WithHint("Filter by company ID"),
		).
		AddToggleField("active", "Active Only",
			resolver.WithDefault(true),
			resolver.WithHint("Show only active contacts"),
		).
		EndSection().
	AddSection("Options").
		AddNumberField("page", "Page",
			resolver.WithDefault(1),
		).
		AddNumberField("perPage", "Per Page",
			resolver.WithDefault(30),
			resolver.WithMinMax(1, 100),
		).
		EndSection().
	Build()

// SolutionCreateSchema is the UI schema for freshdesk-solution-create
var SolutionCreateSchema = resolver.NewSchemaBuilder("freshdesk-solution-create").
	WithName("Create Freshdesk Solution").
	WithCategory("action").
	WithIcon(iconFreshdesk).
	WithDescription("Create a new solution folder in Freshdesk").
	AddSection("Connection").
		AddExpressionField("domain", "Domain",
			resolver.WithRequired(),
			resolver.WithPlaceholder("yourcompany.freshdesk.com"),
		).
		AddExpressionField("apiKey", "API Key",
			resolver.WithRequired(),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Solution Details").
		AddTextField("name", "Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("Solution Folder Name"),
		).
		AddTextareaField("description", "Description",
			resolver.WithRows(4),
			resolver.WithPlaceholder("Description of the solution folder"),
		).
		EndSection().
	AddSection("Location").
		AddTextField("parentId", "Parent Folder ID",
			resolver.WithPlaceholder("123456"),
			resolver.WithHint("Parent folder ID for nested solutions"),
		).
		EndSection().
	AddSection("Visibility").
		AddTextField("visibleInPortals", "Visible In Portals",
			resolver.WithPlaceholder("1"),
			resolver.WithHint("Comma-separated portal IDs"),
		).
		EndSection().
	Build()

// ArticleListSchema is the UI schema for freshdesk-article-list
var ArticleListSchema = resolver.NewSchemaBuilder("freshdesk-article-list").
	WithName("List Freshdesk Articles").
	WithCategory("action").
	WithIcon(iconFreshdesk).
	WithDescription("List knowledge base articles from Freshdesk").
	AddSection("Connection").
		AddExpressionField("domain", "Domain",
			resolver.WithRequired(),
			resolver.WithPlaceholder("yourcompany.freshdesk.com"),
		).
		AddExpressionField("apiKey", "API Key",
			resolver.WithRequired(),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Filters").
		AddTextField("folderId", "Folder ID",
			resolver.WithPlaceholder("123456"),
			resolver.WithHint("Filter by folder ID"),
		).
		AddTextField("categoryId", "Category ID",
			resolver.WithPlaceholder("123456"),
			resolver.WithHint("Filter by category ID"),
		).
		AddTextField("agentId", "Agent ID",
			resolver.WithPlaceholder("123456"),
			resolver.WithHint("Filter by author agent ID"),
		).
		AddSelectField("status", "Status", []resolver.SelectOption{
			{Label: "Draft", Value: "1"},
			{Label: "Published", Value: "2"},
		},
			resolver.WithHint("Filter by article status"),
		).
		EndSection().
	AddSection("Options").
		AddNumberField("page", "Page",
			resolver.WithDefault(1),
		).
		AddNumberField("perPage", "Per Page",
			resolver.WithDefault(30),
			resolver.WithMinMax(1, 100),
		).
		EndSection().
	Build()

// ============================================================================
// TICKET EXECUTORS
// ============================================================================

// TicketListConfig defines the configuration for freshdesk-ticket-list
type TicketListConfig struct {
	Domain     string `json:"domain" description:"Freshdesk domain"`
	APIKey     string `json:"apiKey" description:"Freshdesk API key"`
	Email      string `json:"email" description:"Filter by requester email"`
	CompanyID  string `json:"companyId" description:"Filter by company ID"`
	UserID     string `json:"userId" description:"Filter by user ID"`
	Status     string `json:"status" description:"Filter by status"`
	Priority   string `json:"priority" description:"Filter by priority"`
	Page       int    `json:"page" default:"1" description:"Page number"`
	PerPage    int    `json:"perPage" default:"30" description:"Results per page"`
	OrderBy    string `json:"orderBy" description:"Order by field"`
	Descending bool   `json:"descending" default:"true" description:"Sort descending"`
}

// TicketListExecutor handles freshdesk-ticket-list node type
type TicketListExecutor struct{}

func (e *TicketListExecutor) Type() string { return "freshdesk-ticket-list" }

func (e *TicketListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg TicketListConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	} else {
		return nil, fmt.Errorf("invalid resolver type")
	}

	if cfg.Domain == "" {
		return nil, fmt.Errorf("domain is required")
	}
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("apiKey is required")
	}

	client := getClient(cfg.Domain, cfg.APIKey)

	// Build query parameters
	path := "tickets?"
	if cfg.Email != "" {
		path += fmt.Sprintf("email=%s&", cfg.Email)
	}
	if cfg.CompanyID != "" {
		path += fmt.Sprintf("company_id=%s&", cfg.CompanyID)
	}
	if cfg.UserID != "" {
		path += fmt.Sprintf("user_id=%s&", cfg.UserID)
	}
	if cfg.Status != "" {
		path += fmt.Sprintf("status=%s&", cfg.Status)
	}
	if cfg.Priority != "" {
		path += fmt.Sprintf("priority=%s&", cfg.Priority)
	}
	if cfg.Page > 0 {
		path += fmt.Sprintf("page=%d&", cfg.Page)
	}
	if cfg.PerPage > 0 {
		path += fmt.Sprintf("per_page=%d&", cfg.PerPage)
	}
	if cfg.OrderBy != "" {
		path += fmt.Sprintf("order_by=%s&", cfg.OrderBy)
	}
	if cfg.Descending {
		path += "order_type=desc&"
	} else {
		path += "order_type=asc&"
	}

	// Remove trailing &
	if len(path) > 0 && path[len(path)-1] == '&' {
		path = path[:len(path)-1]
	}

	resp, err := client.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}

	var tickets []FreshdeskTicket
	if err := decodeResponse(resp, &tickets); err != nil {
		return nil, err
	}

	// Convert to output format
	var ticketList []map[string]interface{}
	for _, ticket := range tickets {
		ticketMap := map[string]interface{}{
			"id":           ticket.ID,
			"subject":      ticket.Subject,
			"description":  ticket.Description,
			"status":       ticket.Status,
			"priority":     ticket.Priority,
			"type":         ticket.Type,
			"source":       ticket.Source,
			"created_at":   ticket.CreatedAt,
			"updated_at":   ticket.UpdatedAt,
			"due_by":       ticket.DueBy,
			"assignee_id":  ticket.AssigneeID,
			"group_id":     ticket.GroupID,
			"requester_id": ticket.RequesterID,
			"email":        ticket.Email,
			"name":         ticket.Name,
			"tags":         ticket.Tags,
			"is_escalated": ticket.IsEscalated,
		}
		ticketList = append(ticketList, ticketMap)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"tickets": ticketList,
			"count":   len(ticketList),
		},
	}, nil
}

// TicketCreateConfig defines the configuration for freshdesk-ticket-create
type TicketCreateConfig struct {
	Domain      string   `json:"domain" description:"Freshdesk domain"`
	APIKey      string   `json:"apiKey" description:"Freshdesk API key"`
	Subject     string   `json:"subject" description:"Ticket subject"`
	Description string   `json:"description" description:"Ticket description"`
	Email       string   `json:"email" description:"Requester email"`
	Name        string   `json:"name" description:"Requester name"`
	Status      string   `json:"status" description:"Ticket status"`
	Priority    string   `json:"priority" description:"Ticket priority"`
	Type        string   `json:"type" description:"Ticket type"`
	AssigneeID  string   `json:"assigneeId" description:"Assignee agent ID"`
	GroupID     string   `json:"groupId" description:"Group ID"`
	Tags        []string `json:"tags" description:"Tags"`
	CCEmails    string   `json:"ccEmails" description:"CC emails"`
}

// TicketCreateExecutor handles freshdesk-ticket-create node type
type TicketCreateExecutor struct{}

func (e *TicketCreateExecutor) Type() string { return "freshdesk-ticket-create" }

func (e *TicketCreateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg TicketCreateConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	} else {
		return nil, fmt.Errorf("invalid resolver type")
	}

	if cfg.Domain == "" {
		return nil, fmt.Errorf("domain is required")
	}
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("apiKey is required")
	}
	if cfg.Subject == "" {
		return nil, fmt.Errorf("subject is required")
	}
	if cfg.Description == "" {
		return nil, fmt.Errorf("description is required")
	}
	if cfg.Email == "" {
		return nil, fmt.Errorf("email is required")
	}

	client := getClient(cfg.Domain, cfg.APIKey)

	// Build request body
	requestBody := map[string]interface{}{
		"subject":     cfg.Subject,
		"description": cfg.Description,
		"email":       cfg.Email,
	}

	if cfg.Name != "" {
		requestBody["name"] = cfg.Name
	}
	if cfg.Status != "" {
		status := getInt(map[string]interface{}{"s": cfg.Status}, "s", 2)
		requestBody["status"] = status
	}
	if cfg.Priority != "" {
		priority := getInt(map[string]interface{}{"p": cfg.Priority}, "p", 2)
		requestBody["priority"] = priority
	}
	if cfg.Type != "" {
		requestBody["type"] = cfg.Type
	}
	if cfg.AssigneeID != "" {
		requestBody["assignee_id"] = getInt64(map[string]interface{}{"a": cfg.AssigneeID}, "a", 0)
	}
	if cfg.GroupID != "" {
		requestBody["group_id"] = getInt64(map[string]interface{}{"g": cfg.GroupID}, "g", 0)
	}
	if len(cfg.Tags) > 0 {
		requestBody["tags"] = cfg.Tags
	}
	if cfg.CCEmails != "" {
		ccEmails := parseEmails(cfg.CCEmails)
		requestBody["cc_emails"] = ccEmails
	}

	resp, err := client.doRequest(ctx, "POST", "tickets", requestBody)
	if err != nil {
		return nil, err
	}

	var ticket FreshdeskTicket
	if err := decodeResponse(resp, &ticket); err != nil {
		return nil, err
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"ticket": map[string]interface{}{
				"id":           ticket.ID,
				"subject":      ticket.Subject,
				"description":  ticket.Description,
				"status":       ticket.Status,
				"priority":     ticket.Priority,
				"type":         ticket.Type,
				"created_at":   ticket.CreatedAt,
				"assignee_id":  ticket.AssigneeID,
				"group_id":     ticket.GroupID,
				"requester_id": ticket.RequesterID,
				"email":        ticket.Email,
				"name":         ticket.Name,
				"tags":         ticket.Tags,
			},
			"id":      ticket.ID,
			"subject": ticket.Subject,
		},
	}, nil
}

// TicketUpdateConfig defines the configuration for freshdesk-ticket-update
type TicketUpdateConfig struct {
	Domain      string   `json:"domain" description:"Freshdesk domain"`
	APIKey      string   `json:"apiKey" description:"Freshdesk API key"`
	TicketID    string   `json:"ticketId" description:"Ticket ID to update"`
	Subject     string   `json:"subject" description:"New subject"`
	Description string   `json:"description" description:"New description"`
	Status      string   `json:"status" description:"New status"`
	Priority    string   `json:"priority" description:"New priority"`
	Type        string   `json:"type" description:"New type"`
	AssigneeID  string   `json:"assigneeId" description:"New assignee ID"`
	GroupID     string   `json:"groupId" description:"New group ID"`
	Tags        []string `json:"tags" description:"New tags"`
	CCEmails    string   `json:"ccEmails" description:"New CC emails"`
}

// TicketUpdateExecutor handles freshdesk-ticket-update node type
type TicketUpdateExecutor struct{}

func (e *TicketUpdateExecutor) Type() string { return "freshdesk-ticket-update" }

func (e *TicketUpdateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg TicketUpdateConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	} else {
		return nil, fmt.Errorf("invalid resolver type")
	}

	if cfg.Domain == "" {
		return nil, fmt.Errorf("domain is required")
	}
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("apiKey is required")
	}
	if cfg.TicketID == "" {
		return nil, fmt.Errorf("ticketId is required")
	}

	client := getClient(cfg.Domain, cfg.APIKey)

	// Build request body
	requestBody := make(map[string]interface{})

	if cfg.Subject != "" {
		requestBody["subject"] = cfg.Subject
	}
	if cfg.Description != "" {
		requestBody["description"] = cfg.Description
	}
	if cfg.Status != "" {
		status := getInt(map[string]interface{}{"s": cfg.Status}, "s", 0)
		requestBody["status"] = status
	}
	if cfg.Priority != "" {
		priority := getInt(map[string]interface{}{"p": cfg.Priority}, "p", 0)
		requestBody["priority"] = priority
	}
	if cfg.Type != "" {
		requestBody["type"] = cfg.Type
	}
	if cfg.AssigneeID != "" {
		requestBody["assignee_id"] = getInt64(map[string]interface{}{"a": cfg.AssigneeID}, "a", 0)
	}
	if cfg.GroupID != "" {
		requestBody["group_id"] = getInt64(map[string]interface{}{"g": cfg.GroupID}, "g", 0)
	}
	if len(cfg.Tags) > 0 {
		requestBody["tags"] = cfg.Tags
	}
	if cfg.CCEmails != "" {
		ccEmails := parseEmails(cfg.CCEmails)
		requestBody["cc_emails"] = ccEmails
	}

	resp, err := client.doRequest(ctx, "PUT", fmt.Sprintf("tickets/%s", cfg.TicketID), requestBody)
	if err != nil {
		return nil, err
	}

	var ticket FreshdeskTicket
	if err := decodeResponse(resp, &ticket); err != nil {
		return nil, err
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"ticket": map[string]interface{}{
				"id":          ticket.ID,
				"subject":     ticket.Subject,
				"description": ticket.Description,
				"status":      ticket.Status,
				"priority":    ticket.Priority,
				"type":        ticket.Type,
				"updated_at":  ticket.UpdatedAt,
				"assignee_id": ticket.AssigneeID,
				"group_id":    ticket.GroupID,
				"tags":        ticket.Tags,
			},
			"id":      ticket.ID,
			"success": true,
		},
	}, nil
}

// TicketReplyConfig defines the configuration for freshdesk-ticket-reply
type TicketReplyConfig struct {
	Domain       string `json:"domain" description:"Freshdesk domain"`
	APIKey       string `json:"apiKey" description:"Freshdesk API key"`
	TicketID     string `json:"ticketId" description:"Ticket ID to reply to"`
	Body         string `json:"body" description:"Reply message body"`
	Private      bool   `json:"private" description:"Private note"`
	FromEmail    string `json:"fromEmail" description:"From email address"`
	CCEmails     string `json:"ccEmails" description:"CC emails"`
	BCCEmails    string `json:"bccEmails" description:"BCC emails"`
	NotifyEmails string `json:"notifyEmails" description:"Notify emails"`
}

// TicketReplyExecutor handles freshdesk-ticket-reply node type
type TicketReplyExecutor struct{}

func (e *TicketReplyExecutor) Type() string { return "freshdesk-ticket-reply" }

func (e *TicketReplyExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg TicketReplyConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	} else {
		return nil, fmt.Errorf("invalid resolver type")
	}

	if cfg.Domain == "" {
		return nil, fmt.Errorf("domain is required")
	}
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("apiKey is required")
	}
	if cfg.TicketID == "" {
		return nil, fmt.Errorf("ticketId is required")
	}
	if cfg.Body == "" {
		return nil, fmt.Errorf("body is required")
	}

	client := getClient(cfg.Domain, cfg.APIKey)

	// Build request body
	requestBody := map[string]interface{}{
		"body":    cfg.Body,
		"private": cfg.Private,
	}

	if cfg.FromEmail != "" {
		requestBody["from_email"] = cfg.FromEmail
	}
	if cfg.CCEmails != "" {
		ccEmails := parseEmails(cfg.CCEmails)
		requestBody["cc_emails"] = ccEmails
	}
	if cfg.BCCEmails != "" {
		bccEmails := parseEmails(cfg.BCCEmails)
		requestBody["bcc_emails"] = bccEmails
	}
	if cfg.NotifyEmails != "" {
		notifyEmails := parseEmails(cfg.NotifyEmails)
		requestBody["notify_emails"] = notifyEmails
	}

	resp, err := client.doRequest(ctx, "POST", fmt.Sprintf("tickets/%s/conversations", cfg.TicketID), requestBody)
	if err != nil {
		return nil, err
	}

	var conversation FreshdeskConversation
	if err := decodeResponse(resp, &conversation); err != nil {
		return nil, err
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"conversation": map[string]interface{}{
				"id":         conversation.ID,
				"body":       conversation.Body,
				"private":    conversation.Private,
				"incoming":   conversation.Incoming,
				"created_at": conversation.CreatedAt,
				"user_id":    conversation.UserID,
			},
			"id":      conversation.ID,
			"success": true,
		},
	}, nil
}

// ============================================================================
// AGENT EXECUTORS
// ============================================================================

// AgentListConfig defines the configuration for freshdesk-agent-list
type AgentListConfig struct {
	Domain  string `json:"domain" description:"Freshdesk domain"`
	APIKey  string `json:"apiKey" description:"Freshdesk API key"`
	Active  bool   `json:"active" default:"true" description:"Active only"`
	Page    int    `json:"page" default:"1" description:"Page number"`
	PerPage int    `json:"perPage" default:"30" description:"Results per page"`
}

// AgentListExecutor handles freshdesk-agent-list node type
type AgentListExecutor struct{}

func (e *AgentListExecutor) Type() string { return "freshdesk-agent-list" }

func (e *AgentListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg AgentListConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	} else {
		return nil, fmt.Errorf("invalid resolver type")
	}

	if cfg.Domain == "" {
		return nil, fmt.Errorf("domain is required")
	}
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("apiKey is required")
	}

	client := getClient(cfg.Domain, cfg.APIKey)

	// Build query parameters
	path := "agents?"
	if cfg.Page > 0 {
		path += fmt.Sprintf("page=%d&", cfg.Page)
	}
	if cfg.PerPage > 0 {
		path += fmt.Sprintf("per_page=%d&", cfg.PerPage)
	}

	// Remove trailing &
	if len(path) > 0 && path[len(path)-1] == '&' {
		path = path[:len(path)-1]
	}

	resp, err := client.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}

	var agents []FreshdeskAgent
	if err := decodeResponse(resp, &agents); err != nil {
		return nil, err
	}

	// Filter by active if requested
	if cfg.Active {
		activeAgents := make([]FreshdeskAgent, 0)
		for _, agent := range agents {
			if agent.Active {
				activeAgents = append(activeAgents, agent)
			}
		}
		agents = activeAgents
	}

	// Convert to output format
	var agentList []map[string]interface{}
	for _, agent := range agents {
		agentMap := map[string]interface{}{
			"id":             agent.ID,
			"email":          agent.Email,
			"name":           agent.Name,
			"mobile":         agent.Mobile,
			"phone":          agent.Phone,
			"active":         agent.Active,
			"available":      agent.Available,
			"type":           agent.Type,
			"occasional":     agent.Occasional,
			"signature":      agent.Signature,
			"ticket_scope":   agent.TicketScope,
			"group_ids":      agent.GroupIDs,
			"role_ids":       agent.RoleIDs,
			"created_at":     agent.CreatedAt,
			"updated_at":     agent.UpdatedAt,
			"last_active_at": agent.LastActiveAt,
			"last_login_at":  agent.LastLoginAt,
		}
		agentList = append(agentList, agentMap)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"agents": agentList,
			"count":  len(agentList),
		},
	}, nil
}

// ============================================================================
// CONTACT EXECUTORS
// ============================================================================

// ContactListConfig defines the configuration for freshdesk-contact-list
type ContactListConfig struct {
	Domain    string `json:"domain" description:"Freshdesk domain"`
	APIKey    string `json:"apiKey" description:"Freshdesk API key"`
	Email     string `json:"email" description:"Filter by email"`
	Phone     string `json:"phone" description:"Filter by phone"`
	CompanyID string `json:"companyId" description:"Filter by company ID"`
	Active    bool   `json:"active" default:"true" description:"Active only"`
	Page      int    `json:"page" default:"1" description:"Page number"`
	PerPage   int    `json:"perPage" default:"30" description:"Results per page"`
}

// ContactListExecutor handles freshdesk-contact-list node type
type ContactListExecutor struct{}

func (e *ContactListExecutor) Type() string { return "freshdesk-contact-list" }

func (e *ContactListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg ContactListConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	} else {
		return nil, fmt.Errorf("invalid resolver type")
	}

	if cfg.Domain == "" {
		return nil, fmt.Errorf("domain is required")
	}
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("apiKey is required")
	}

	client := getClient(cfg.Domain, cfg.APIKey)

	// Build query parameters
	path := "contacts?"
	if cfg.Email != "" {
		path += fmt.Sprintf("email=%s&", cfg.Email)
	}
	if cfg.Phone != "" {
		path += fmt.Sprintf("phone=%s&", cfg.Phone)
	}
	if cfg.CompanyID != "" {
		path += fmt.Sprintf("company_id=%s&", cfg.CompanyID)
	}
	if cfg.Page > 0 {
		path += fmt.Sprintf("page=%d&", cfg.Page)
	}
	if cfg.PerPage > 0 {
		path += fmt.Sprintf("per_page=%d&", cfg.PerPage)
	}

	// Remove trailing &
	if len(path) > 0 && path[len(path)-1] == '&' {
		path = path[:len(path)-1]
	}

	resp, err := client.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}

	var contacts []FreshdeskContact
	if err := decodeResponse(resp, &contacts); err != nil {
		return nil, err
	}

	// Filter by active if requested
	if cfg.Active {
		activeContacts := make([]FreshdeskContact, 0)
		for _, contact := range contacts {
			if contact.Active && !contact.Deleted {
				activeContacts = append(activeContacts, contact)
			}
		}
		contacts = activeContacts
	}

	// Convert to output format
	var contactList []map[string]interface{}
	for _, contact := range contacts {
		contactMap := map[string]interface{}{
			"id":               contact.ID,
			"name":             contact.Name,
			"email":            contact.Email,
			"phone":            contact.Phone,
			"mobile":           contact.Mobile,
			"twitter_id":       contact.TwitterID,
			"language":         contact.Language,
			"time_zone":        contact.Timezone,
			"created_at":       contact.CreatedAt,
			"updated_at":       contact.UpdatedAt,
			"company_id":       contact.CompanyID,
			"view_all_tickets": contact.ViewAllTickets,
			"active":           contact.Active,
			"address":          contact.Address,
			"description":      contact.Description,
			"job_title":        contact.JobTitle,
			"tags":             contact.Tags,
			"other_emails":     contact.OtherEmails,
			"other_phones":     contact.OtherPhones,
		}
		contactList = append(contactList, contactMap)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"contacts": contactList,
			"count":    len(contactList),
		},
	}, nil
}

// ============================================================================
// SOLUTION EXECUTORS
// ============================================================================

// SolutionCreateConfig defines the configuration for freshdesk-solution-create
type SolutionCreateConfig struct {
	Domain           string `json:"domain" description:"Freshdesk domain"`
	APIKey           string `json:"apiKey" description:"Freshdesk API key"`
	Name             string `json:"name" description:"Solution name"`
	Description      string `json:"description" description:"Solution description"`
	ParentID         string `json:"parentId" description:"Parent folder ID"`
	VisibleInPortals string `json:"visibleInPortals" description:"Portal IDs"`
}

// SolutionCreateExecutor handles freshdesk-solution-create node type
type SolutionCreateExecutor struct{}

func (e *SolutionCreateExecutor) Type() string { return "freshdesk-solution-create" }

func (e *SolutionCreateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg SolutionCreateConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	} else {
		return nil, fmt.Errorf("invalid resolver type")
	}

	if cfg.Domain == "" {
		return nil, fmt.Errorf("domain is required")
	}
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("apiKey is required")
	}
	if cfg.Name == "" {
		return nil, fmt.Errorf("name is required")
	}

	client := getClient(cfg.Domain, cfg.APIKey)

	// Build request body
	requestBody := map[string]interface{}{
		"name": cfg.Name,
	}

	if cfg.Description != "" {
		requestBody["description"] = cfg.Description
	}
	if cfg.ParentID != "" {
		requestBody["parent_id"] = getInt64(map[string]interface{}{"p": cfg.ParentID}, "p", 0)
	}
	if cfg.VisibleInPortals != "" {
		portalIDs := parseInts(cfg.VisibleInPortals)
		requestBody["visible_in_portals"] = portalIDs
	}

	resp, err := client.doRequest(ctx, "POST", "solutions/folders", requestBody)
	if err != nil {
		return nil, err
	}

	var solution FreshdeskSolution
	if err := decodeResponse(resp, &solution); err != nil {
		return nil, err
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"solution": map[string]interface{}{
				"id":          solution.ID,
				"name":        solution.Name,
				"description": solution.Description,
				"position":    solution.Position,
				"parent_id":   solution.ParentID,
				"created_at":  solution.CreatedAt,
				"updated_at":  solution.UpdatedAt,
			},
			"id":      solution.ID,
			"name":    solution.Name,
			"success": true,
		},
	}, nil
}

// ============================================================================
// ARTICLE EXECUTORS
// ============================================================================

// ArticleListConfig defines the configuration for freshdesk-article-list
type ArticleListConfig struct {
	Domain     string `json:"domain" description:"Freshdesk domain"`
	APIKey     string `json:"apiKey" description:"Freshdesk API key"`
	FolderID   string `json:"folderId" description:"Filter by folder ID"`
	CategoryID string `json:"categoryId" description:"Filter by category ID"`
	AgentID    string `json:"agentId" description:"Filter by agent ID"`
	Status     string `json:"status" description:"Filter by status"`
	Page       int    `json:"page" default:"1" description:"Page number"`
	PerPage    int    `json:"perPage" default:"30" description:"Results per page"`
}

// ArticleListExecutor handles freshdesk-article-list node type
type ArticleListExecutor struct{}

func (e *ArticleListExecutor) Type() string { return "freshdesk-article-list" }

func (e *ArticleListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg ArticleListConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	} else {
		return nil, fmt.Errorf("invalid resolver type")
	}

	if cfg.Domain == "" {
		return nil, fmt.Errorf("domain is required")
	}
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("apiKey is required")
	}

	client := getClient(cfg.Domain, cfg.APIKey)

	// Build query parameters
	path := "solutions/articles?"
	if cfg.FolderID != "" {
		path += fmt.Sprintf("folder_id=%s&", cfg.FolderID)
	}
	if cfg.CategoryID != "" {
		path += fmt.Sprintf("category_id=%s&", cfg.CategoryID)
	}
	if cfg.AgentID != "" {
		path += fmt.Sprintf("agent_id=%s&", cfg.AgentID)
	}
	if cfg.Status != "" {
		path += fmt.Sprintf("status=%s&", cfg.Status)
	}
	if cfg.Page > 0 {
		path += fmt.Sprintf("page=%d&", cfg.Page)
	}
	if cfg.PerPage > 0 {
		path += fmt.Sprintf("per_page=%d&", cfg.PerPage)
	}

	// Remove trailing &
	if len(path) > 0 && path[len(path)-1] == '&' {
		path = path[:len(path)-1]
	}

	resp, err := client.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}

	var articles []FreshdeskArticle
	if err := decodeResponse(resp, &articles); err != nil {
		return nil, err
	}

	// Convert to output format
	var articleList []map[string]interface{}
	for _, article := range articles {
		articleMap := map[string]interface{}{
			"id":          article.ID,
			"type":        article.Type,
			"category_id": article.CategoryID,
			"folder_id":   article.FolderID,
			"title":       article.Title,
			"description": article.Description,
			"status":      article.Status,
			"agent_id":    article.AgentID,
			"views":       article.Views,
			"tags":        article.Tags,
			"thumbs_up":   article.ThumbsUp,
			"thumbs_down": article.ThumbsDown,
			"created_at":  article.CreatedAt,
			"updated_at":  article.UpdatedAt,
		}
		articleList = append(articleList, articleMap)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"articles": articleList,
			"count":    len(articleList),
		},
	}, nil
}

// ============================================================================
// HELPER FUNCTIONS
// ============================================================================

// parseEmails parses a comma-separated string of emails into a slice
func parseEmails(emails string) []string {
	if emails == "" {
		return nil
	}
	result := make([]string, 0)
	for _, email := range strings.Split(emails, ",") {
		trimmed := strings.TrimSpace(email)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

// parseInts parses a comma-separated string of integers into a slice
func parseInts(s string) []int {
	if s == "" {
		return nil
	}
	result := make([]int, 0)
	for _, item := range strings.Split(s, ",") {
		trimmed := strings.TrimSpace(item)
		if trimmed != "" {
			if n, err := strconv.Atoi(trimmed); err == nil {
				result = append(result, n)
			}
		}
	}
	return result
}
