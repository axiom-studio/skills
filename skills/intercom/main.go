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

// Intercom API configuration
const (
	IntercomAPIBase    = "https://api.intercom.io"
	IntercomAPIVersion = "2.10"
)

// Intercom client cache
var (
	clients     = make(map[string]*IntercomClient)
	clientMutex sync.RWMutex
)

// IntercomClient represents an Intercom API client
type IntercomClient struct {
	APIToken string
	Client   *http.Client
}

// ============================================================================
// INTERCOM API RESPONSE TYPES
// ============================================================================

// IntercomConversation represents an Intercom conversation
type IntercomConversation struct {
	ID            string                 `json:"id,omitempty"`
	CreatedAt     int64                  `json:"created_at,omitempty"`
	UpdatedAt     int64                  `json:"updated_at,omitempty"`
	Source        *IntercomSource        `json:"source,omitempty"`
	Contacts      *IntercomContacts      `json:"contacts,omitempty"`
	Teammates     *IntercomTeammates     `json:"teammates,omitempty"`
	Title         string                 `json:"title,omitempty"`
	AdminAssignee *IntercomAdmin         `json:"admin_assignee,omitempty"`
	TeamAssignee  *IntercomTeam          `json:"team_assignee,omitempty"`
	Open          bool                   `json:"open,omitempty"`
	State         string                 `json:"state,omitempty"`
	Read          bool                   `json:"read,omitempty"`
	Tags          *IntercomTagList       `json:"tags,omitempty"`
	Priority      string                 `json:"priority,omitempty"`
	ConversationRating *IntercomRating   `json:"conversation_rating,omitempty"`
	Statistics    *IntercomStats         `json:"statistics,omitempty"`
	ConversationParts *IntercomParts     `json:"conversation_parts,omitempty"`
	Type          string                 `json:"type,omitempty"`
}

// IntercomSource represents the source of a conversation
type IntercomSource struct {
	Type          string          `json:"type,omitempty"`
	ID            string          `json:"id,omitempty"`
	DeliveredAs   string          `json:"delivered_as,omitempty"`
	Subject       string          `json:"subject,omitempty"`
	Body          string          `json:"body,omitempty"`
	Author        *IntercomAuthor `json:"author,omitempty"`
	Attachments   []interface{}   `json:"attachments,omitempty"`
	URL           string          `json:"url,omitempty"`
	Redacted      bool            `json:"redacted,omitempty"`
}

// IntercomAuthor represents a message author
type IntercomAuthor struct {
	Type    string `json:"type,omitempty"`
	ID      string `json:"id,omitempty"`
	Name    string `json:"name,omitempty"`
	Email   string `json:"email,omitempty"`
	Role    string `json:"role,omitempty"`
	Avatar  *IntercomAvatar `json:"avatar,omitempty"`
}

// IntercomAvatar represents user avatar
type IntercomAvatar struct {
	Type string `json:"type,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
}

// IntercomContacts represents contacts in a conversation
type IntercomContacts struct {
	Type  string            `json:"type,omitempty"`
	Contacts []IntercomContact `json:"contacts,omitempty"`
}

// IntercomContact represents a contact
type IntercomContact struct {
	Type    string `json:"type,omitempty"`
	ID      string `json:"id,omitempty"`
	Role    string `json:"role,omitempty"`
	Name    string `json:"name,omitempty"`
	Email   string `json:"email,omitempty"`
	Phone   string `json:"phone,omitempty"`
}

// IntercomTeammates represents teammates in a conversation
type IntercomTeammates struct {
	Type      string         `json:"type,omitempty"`
	Teammates []IntercomAdmin `json:"teammates,omitempty"`
}

// IntercomAdmin represents an admin/teammate
type IntercomAdmin struct {
	ID         string `json:"id,omitempty"`
	Type       string `json:"type,omitempty"`
	Name       string `json:"name,omitempty"`
	Email      string `json:"email,omitempty"`
	JobTitle   string `json:"job_title,omitempty"`
	Avatar     *IntercomAvatar `json:"avatar,omitempty"`
}

// IntercomTeam represents a team
type IntercomTeam struct {
	ID   string `json:"id,omitempty"`
	Type string `json:"type,omitempty"`
	Name string `json:"name,omitempty"`
}

// IntercomTagList represents tags on a conversation
type IntercomTagList struct {
	Type string           `json:"type,omitempty"`
	Tags []IntercomTag    `json:"tags,omitempty"`
}

// IntercomTag represents a tag
type IntercomTag struct {
	Type      string `json:"type,omitempty"`
	ID        string `json:"id,omitempty"`
	Name      string `json:"name,omitempty"`
	AppliedAt int64  `json:"applied_at,omitempty"`
	AppliedBy *IntercomAppliedBy `json:"applied_by,omitempty"`
}

// IntercomAppliedBy represents who applied a tag
type IntercomAppliedBy struct {
	ID   string `json:"id,omitempty"`
	Type string `json:"type,omitempty"`
}

// IntercomRating represents a conversation rating
type IntercomRating struct {
	Rating   int    `json:"rating,omitempty"`
	Remark   string `json:"remark,omitempty"`
	CreatedAt int64 `json:"created_at,omitempty"`
	Contact  *IntercomContact `json:"contact,omitempty"`
	Teammate *IntercomAdmin `json:"teammate,omitempty"`
}

// IntercomStats represents conversation statistics
type IntercomStats struct {
	TimeToAssignment        int `json:"time_to_assignment,omitempty"`
	TimeToAdminReply        int `json:"time_to_admin_reply,omitempty"`
	TimeToFirstClose        int `json:"time_to_first_close,omitempty"`
	TimeToLastClose         int `json:"time_to_last_close,omitempty"`
	CountReopens            int `json:"count_reopens,omitempty"`
	CountAssignments        int `json:"count_assignments,omitempty"`
	CountConversationParts  int `json:"count_conversation_parts,omitempty"`
}

// IntercomParts represents conversation parts
type IntercomParts struct {
	Type          string              `json:"type,omitempty"`
	ConversationParts []IntercomPart   `json:"conversation_parts,omitempty"`
	TotalCount    int                 `json:"total_count,omitempty"`
}

// IntercomPart represents a part of a conversation
type IntercomPart struct {
	Type        string          `json:"type,omitempty"`
	ID          string          `json:"id,omitempty"`
	PartType    string          `json:"part_type,omitempty"`
	Body        string          `json:"body,omitempty"`
	CreatedAt   int64           `json:"created_at,omitempty"`
	UpdatedAt   int64           `json:"updated_at,omitempty"`
	NotifiedAt  int64           `json:"notified_at,omitempty"`
	AssignedTo  *IntercomAdmin  `json:"assigned_to,omitempty"`
	Author      *IntercomAuthor `json:"author,omitempty"`
	Attachments []interface{}   `json:"attachments,omitempty"`
	Redacted    bool            `json:"redacted,omitempty"`
	ExternalID  string          `json:"external_id,omitempty"`
}

// IntercomUser represents an Intercom user/contact
type IntercomUser struct {
	Type            string                 `json:"type,omitempty"`
	ID              string                 `json:"id,omitempty"`
	UserID          string                 `json:"user_id,omitempty"`
	Email           string                 `json:"email,omitempty"`
	Phone           string                 `json:"phone,omitempty"`
	Name            string                 `json:"name,omitempty"`
	Avatar          *IntercomAvatar        `json:"avatar,omitempty"`
	OwnerID         string                 `json:"owner_id,omitempty"`
	SocialProfiles  *IntercomSocialProfiles `json:"social_profiles,omitempty"`
	HasHardBounced  bool                   `json:"has_hard_bounced,omitempty"`
	MarkedEmailAsSpam bool                 `json:"marked_email_as_spam,omitempty"`
	UnsubscribedFromEmails bool            `json:"unsubscribed_from_emails,omitempty"`
	CreatedAt       int64                  `json:"created_at,omitempty"`
	UpdatedAt       int64                  `json:"updated_at,omitempty"`
	SignedUpAt      int64                  `json:"signed_up_at,omitempty"`
	LastSeenAt      int64                  `json:"last_seen_at,omitempty"`
	LastRepliedAt   int64                  `json:"last_replied_at,omitempty"`
	LastContactedAt int64                  `json:"last_contacted_at,omitempty"`
	LastEmailOpenedAt int64                `json:"last_email_opened_at,omitempty"`
	LastEmailClickedAt int64               `json:"last_email_clicked_at,omitempty"`
	LanguageOverride string                `json:"language_override,omitempty"`
	Browser         string                 `json:"browser,omitempty"`
	BrowserVersion  string                 `json:"browser_version,omitempty"`
	BrowserLanguage string                 `json:"browser_language,omitempty"`
	OS              string                 `json:"os,omitempty"`
	Location        *IntercomLocation      `json:"location,omitempty"`
	AndroidAppName  string                 `json:"android_app_name,omitempty"`
	AndroidAppVersion string               `json:"android_app_version,omitempty"`
	AndroidDevice   string                 `json:"android_device,omitempty"`
	AndroidOSVersion string                `json:"android_os_version,omitempty"`
	AndroidSDKVersion string               `json:"android_sdk_version,omitempty"`
	AndroidLastSeenAt int64                `json:"android_last_seen_at,omitempty"`
	IOSAppName      string                 `json:"ios_app_name,omitempty"`
	IOSAppVersion   string                 `json:"ios_app_version,omitempty"`
	IOSDevice       string                 `json:"ios_device,omitempty"`
	IOSOSVersion    string                 `json:"ios_os_version,omitempty"`
	IOSBundleID     string                 `json:"ios_bundle_id,omitempty"`
	IOSLastSeenAt   int64                  `json:"ios_last_seen_at,omitempty"`
	CustomAttributes map[string]interface{} `json:"custom_attributes,omitempty"`
	Tags            *IntercomTagList       `json:"tags,omitempty"`
	Notes           *IntercomNoteList      `json:"notes,omitempty"`
	Companies       *IntercomCompanyList   `json:"companies,omitempty"`
	OptedOutSubscriptionTypes *IntercomSubscriptionTypes `json:"opted_out_subscription_types,omitempty"`
	OptedInSubscriptionTypes  *IntercomSubscriptionTypes `json:"opted_in_subscription_types,omitempty"`
}

// IntercomLocation represents user location
type IntercomLocation struct {
	Type      string `json:"type,omitempty"`
	Country   string `json:"country,omitempty"`
	Region    string `json:"region,omitempty"`
	City      string `json:"city,omitempty"`
}

// IntercomSocialProfiles represents social profiles
type IntercomSocialProfiles struct {
	Type          string                    `json:"type,omitempty"`
	SocialProfiles []IntercomSocialProfile  `json:"social_profiles,omitempty"`
}

// IntercomSocialProfile represents a social profile
type IntercomSocialProfile struct {
	Type     string `json:"type,omitempty"`
	Name     string `json:"name,omitempty"`
	Username string `json:"username,omitempty"`
	URL      string `json:"url,omitempty"`
}

// IntercomNoteList represents notes on a user
type IntercomNoteList struct {
	Type  string            `json:"type,omitempty"`
	Notes []IntercomNote    `json:"notes,omitempty"`
}

// IntercomNote represents a note
type IntercomNote struct {
	Type      string `json:"type,omitempty"`
	ID        string `json:"id,omitempty"`
	CreatedAt int64  `json:"created_at,omitempty"`
	Body      string `json:"body,omitempty"`
	Author    *IntercomAuthor `json:"author,omitempty"`
}

// IntercomCompanyList represents companies
type IntercomCompanyList struct {
	Type      string               `json:"type,omitempty"`
	Companies []IntercomCompany    `json:"companies,omitempty"`
}

// IntercomCompany represents a company
type IntercomCompany struct {
	Type      string `json:"type,omitempty"`
	ID        string `json:"id,omitempty"`
	CompanyID string `json:"company_id,omitempty"`
	Name      string `json:"name,omitempty"`
}

// IntercomSubscriptionTypes represents subscription types
type IntercomSubscriptionTypes struct {
	Type             string `json:"type,omitempty"`
	SubscriptionTypes []interface{} `json:"subscription_types,omitempty"`
}

// IntercomMessage represents a message
type IntercomMessage struct {
	Type        string          `json:"type,omitempty"`
	ID          string          `json:"id,omitempty"`
	CreatedAt   int64           `json:"created_at,omitempty"`
	Subject     string          `json:"subject,omitempty"`
	Body        string          `json:"body,omitempty"`
	MessageType string          `json:"message_type,omitempty"`
	ConversationID string       `json:"conversation_id,omitempty"`
	From        *IntercomAuthor `json:"from,omitempty"`
	To          *IntercomTo     `json:"to,omitempty"`
	Owner       *IntercomAuthor `json:"owner,omitempty"`
}

// IntercomTo represents message recipient
type IntercomTo struct {
	Type  string `json:"type,omitempty"`
	ID    string `json:"id,omitempty"`
	Email string `json:"email,omitempty"`
}

// IntercomListResponse represents a paginated list response
type IntercomListResponse struct {
	Type    string          `json:"type,omitempty"`
	Pages   *IntercomPages  `json:"pages,omitempty"`
	TotalCount int          `json:"total_count,omitempty"`
	Data    []json.RawMessage `json:"data,omitempty"`
	// For conversations list
	Conversations []json.RawMessage `json:"conversations,omitempty"`
	// For users list
	Users []json.RawMessage `json:"users,omitempty"`
	// For tags list
	Tags []json.RawMessage `json:"tags,omitempty"`
}

// IntercomPages represents pagination info
type IntercomPages struct {
	Type       string `json:"type,omitempty"`
	Page       int    `json:"page,omitempty"`
	PerPage    int    `json:"per_page,omitempty"`
	TotalPages int    `json:"total_pages,omitempty"`
	TotalCount int    `json:"total_count,omitempty"`
	Next       *string `json:"next,omitempty"`
}

// IntercomSingleResponse represents a single resource response
type IntercomSingleResponse struct {
	Type string          `json:"type,omitempty"`
	Data json.RawMessage `json:"data,omitempty"`
}

// ============================================================================
// SCHEMAS
// ============================================================================

// IntercomConversationListSchema is the UI schema for intercom-conversation-list
var IntercomConversationListSchema = resolver.NewSchemaBuilder("intercom-conversation-list").
	WithName("List Intercom Conversations").
	WithCategory("intercom").
	WithIcon("message-square").
	WithDescription("List conversations from Intercom").
	AddSection("Connection").
		AddExpressionField("apiToken", "API Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("dG9rZW5fYWJjMTIz..."),
			resolver.WithHint("Intercom API access token (use {{secrets.intercom_token}})"),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Filters").
		AddTextField("state", "State",
			resolver.WithPlaceholder("open, closed, all"),
			resolver.WithHint("Filter by conversation state (default: open)"),
		).
		AddTextField("assignedTo", "Assigned To",
			resolver.WithPlaceholder("me, unassigned, or admin ID"),
			resolver.WithHint("Filter by assignment"),
		).
		AddTextField("query", "Query",
			resolver.WithPlaceholder("email:test@example.com"),
			resolver.WithHint("Search query using Intercom query language"),
		).
		EndSection().
	AddSection("Options").
		AddNumberField("page", "Page",
			resolver.WithDefault(1),
			resolver.WithHint("Page number for pagination"),
		).
		AddNumberField("perPage", "Per Page",
			resolver.WithDefault(20),
			resolver.WithMinMax(1, 150),
			resolver.WithHint("Number of results per page"),
		).
		EndSection().
	Build()

// IntercomConversationGetSchema is the UI schema for intercom-conversation-get
var IntercomConversationGetSchema = resolver.NewSchemaBuilder("intercom-conversation-get").
	WithName("Get Intercom Conversation").
	WithCategory("intercom").
	WithIcon("message-square").
	WithDescription("Get details of a specific Intercom conversation").
	AddSection("Connection").
		AddExpressionField("apiToken", "API Token",
			resolver.WithRequired(),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Conversation").
		AddTextField("conversationId", "Conversation ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("1234567890"),
		).
		EndSection().
	Build()

// IntercomConversationReplySchema is the UI schema for intercom-conversation-reply
var IntercomConversationReplySchema = resolver.NewSchemaBuilder("intercom-conversation-reply").
	WithName("Reply to Intercom Conversation").
	WithCategory("intercom").
	WithIcon("reply").
	WithDescription("Reply to an Intercom conversation").
	AddSection("Connection").
		AddExpressionField("apiToken", "API Token",
			resolver.WithRequired(),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Reply Details").
		AddTextField("conversationId", "Conversation ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("1234567890"),
		).
		AddTextareaField("body", "Message Body",
			resolver.WithRequired(),
			resolver.WithRows(4),
			resolver.WithPlaceholder("Type your reply here..."),
		).
		AddTextField("messageType", "Message Type",
			resolver.WithDefault("comment"),
			resolver.WithHint("Type: comment, note, or assignment"),
		).
		EndSection().
	AddSection("Assignment (Optional)").
		AddTextField("assigneeId", "Assignee ID",
			resolver.WithPlaceholder("Admin ID to assign conversation"),
			resolver.WithHint("Leave empty to not change assignment"),
		).
		EndSection().
	Build()

// IntercomUserListSchema is the UI schema for intercom-user-list
var IntercomUserListSchema = resolver.NewSchemaBuilder("intercom-user-list").
	WithName("List Intercom Users").
	WithCategory("intercom").
	WithIcon("users").
	WithDescription("List users/contacts from Intercom").
	AddSection("Connection").
		AddExpressionField("apiToken", "API Token",
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
		AddTextField("userId", "User ID",
			resolver.WithPlaceholder("external_user_id"),
			resolver.WithHint("Filter by external user ID"),
		).
		AddTextField("query", "Query",
			resolver.WithPlaceholder("email:test@example.com"),
			resolver.WithHint("Search query using Intercom query language"),
		).
		EndSection().
	AddSection("Options").
		AddNumberField("page", "Page",
			resolver.WithDefault(1),
			resolver.WithHint("Page number for pagination"),
		).
		AddNumberField("perPage", "Per Page",
			resolver.WithDefault(50),
			resolver.WithMinMax(1, 150),
			resolver.WithHint("Number of results per page"),
		).
		EndSection().
	Build()

// IntercomUserCreateSchema is the UI schema for intercom-user-create
var IntercomUserCreateSchema = resolver.NewSchemaBuilder("intercom-user-create").
	WithName("Create Intercom User").
	WithCategory("intercom").
	WithIcon("user-plus").
	WithDescription("Create a new user/contact in Intercom").
	AddSection("Connection").
		AddExpressionField("apiToken", "API Token",
			resolver.WithRequired(),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("User Details").
		AddTextField("email", "Email",
			resolver.WithPlaceholder("user@example.com"),
			resolver.WithHint("User email address"),
		).
		AddTextField("phone", "Phone",
			resolver.WithPlaceholder("+1234567890"),
			resolver.WithHint("User phone number"),
		).
		AddTextField("userId", "External User ID",
			resolver.WithPlaceholder("your_internal_user_id"),
			resolver.WithHint("Your internal user identifier"),
		).
		AddTextField("name", "Name",
			resolver.WithPlaceholder("John Doe"),
			resolver.WithHint("User full name"),
		).
		EndSection().
	AddSection("Location").
		AddTextField("city", "City",
			resolver.WithPlaceholder("San Francisco"),
		).
		AddTextField("region", "Region/State",
			resolver.WithPlaceholder("California"),
		).
		AddTextField("country", "Country",
			resolver.WithPlaceholder("United States"),
		).
		EndSection().
	AddSection("Advanced").
		AddJSONField("customAttributes", "Custom Attributes",
			resolver.WithHint("Custom user attributes as JSON"),
		).
		AddJSONField("companies", "Companies",
			resolver.WithHint("Company associations as JSON array"),
		).
		EndSection().
	Build()

// IntercomMessageSendSchema is the UI schema for intercom-message-send
var IntercomMessageSendSchema = resolver.NewSchemaBuilder("intercom-message-send").
	WithName("Send Intercom Message").
	WithCategory("intercom").
	WithIcon("send").
	WithDescription("Send a message to a user via Intercom").
	AddSection("Connection").
		AddExpressionField("apiToken", "API Token",
			resolver.WithRequired(),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Message Details").
		AddTextField("messageType", "Message Type",
			resolver.WithRequired(),
			resolver.WithDefault("email"),
			resolver.WithHint("Type: email, inapp, facebook, or sms"),
		).
		AddTextField("subject", "Subject",
			resolver.WithPlaceholder("Message subject"),
			resolver.WithHint("Required for email messages"),
		).
		AddTextareaField("body", "Body",
			resolver.WithRequired(),
			resolver.WithRows(4),
			resolver.WithPlaceholder("Message content..."),
		).
		EndSection().
	AddSection("Recipient").
		AddTextField("toUserId", "User ID",
			resolver.WithPlaceholder("intercom_user_id"),
			resolver.WithHint("Intercom user ID"),
		).
		AddTextField("toEmail", "Email",
			resolver.WithPlaceholder("user@example.com"),
			resolver.WithHint("Recipient email (alternative to user ID)"),
		).
		EndSection().
	AddSection("From (Optional)").
		AddTextField("fromAdminId", "From Admin ID",
			resolver.WithPlaceholder("admin_id"),
			resolver.WithHint("Admin ID sending the message"),
		).
		EndSection().
	Build()

// IntercomTagCreateSchema is the UI schema for intercom-tag-create
var IntercomTagCreateSchema = resolver.NewSchemaBuilder("intercom-tag-create").
	WithName("Create Intercom Tag").
	WithCategory("intercom").
	WithIcon("tag").
	WithDescription("Create a new tag in Intercom").
	AddSection("Connection").
		AddExpressionField("apiToken", "API Token",
			resolver.WithRequired(),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Tag Details").
		AddTextField("name", "Tag Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("Support Priority"),
			resolver.WithHint("Name of the tag to create"),
		).
		EndSection().
	Build()

// IntercomTagApplySchema is the UI schema for intercom-tag-apply
var IntercomTagApplySchema = resolver.NewSchemaBuilder("intercom-tag-apply").
	WithName("Apply Intercom Tag").
	WithCategory("intercom").
	WithIcon("tag").
	WithDescription("Apply a tag to a user or conversation in Intercom").
	AddSection("Connection").
		AddExpressionField("apiToken", "API Token",
			resolver.WithRequired(),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Tag Details").
		AddTextField("tagId", "Tag ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("tag_123456"),
			resolver.WithHint("ID of the tag to apply"),
		).
		AddTextField("tagName", "Tag Name",
			resolver.WithPlaceholder("Existing tag name"),
			resolver.WithHint("Or use tag name instead of ID"),
		).
		EndSection().
	AddSection("Target").
		AddTextField("userId", "User ID",
			resolver.WithPlaceholder("user_id"),
			resolver.WithHint("User ID to apply tag to"),
		).
		AddTextField("conversationId", "Conversation ID",
			resolver.WithPlaceholder("conversation_id"),
			resolver.WithHint("Conversation ID to apply tag to"),
		).
		EndSection().
	Build()

// ============================================================================
// MAIN
// ============================================================================

func main() {
	// Get port from env or use default
	port := os.Getenv("SKILL_PORT")
	if port == "" {
		port = "50111"
	}

	// Create skill server
	server := grpc.NewSkillServer("skill-intercom", "1.0.0")

	// Register Intercom executors with schemas
	server.RegisterExecutorWithSchema("intercom-conversation-list", &ConversationListExecutor{}, IntercomConversationListSchema)
	server.RegisterExecutorWithSchema("intercom-conversation-get", &ConversationGetExecutor{}, IntercomConversationGetSchema)
	server.RegisterExecutorWithSchema("intercom-conversation-reply", &ConversationReplyExecutor{}, IntercomConversationReplySchema)
	server.RegisterExecutorWithSchema("intercom-user-list", &UserListExecutor{}, IntercomUserListSchema)
	server.RegisterExecutorWithSchema("intercom-user-create", &UserCreateExecutor{}, IntercomUserCreateSchema)
	server.RegisterExecutorWithSchema("intercom-message-send", &MessageSendExecutor{}, IntercomMessageSendSchema)
	server.RegisterExecutorWithSchema("intercom-tag-create", &TagCreateExecutor{}, IntercomTagCreateSchema)
	server.RegisterExecutorWithSchema("intercom-tag-apply", &TagApplyExecutor{}, IntercomTagApplySchema)

	fmt.Printf("Starting skill-intercom gRPC server on port %s\n", port)
	if err := server.Serve(port); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to serve: %v\n", err)
		os.Exit(1)
	}
}

// ============================================================================
// INTERCOM CLIENT HELPERS
// ============================================================================

// getClient returns or creates an Intercom client (cached)
func getClient(apiToken string) *IntercomClient {
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

	client = &IntercomClient{
		APIToken: apiToken,
		Client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
	clients[apiToken] = client
	return client
}

// doRequest performs an HTTP request to the Intercom API
func (c *IntercomClient) doRequest(ctx context.Context, method, path string, body interface{}) (*http.Response, error) {
	var reqBody io.Reader
	if body != nil {
		jsonData, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
		reqBody = bytes.NewBuffer(jsonData)
	}

	reqURL := IntercomAPIBase + path
	req, err := http.NewRequestWithContext(ctx, method, reqURL, reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set Intercom API headers
	req.Header.Set("Authorization", "Bearer "+c.APIToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Intercom-Version", IntercomAPIVersion)
	req.Header.Set("Accept", "application/json")

	resp, err := c.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	return resp, nil
}

// decodeResponse decodes an Intercom API response
func decodeResponse(resp *http.Response, result interface{}) error {
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var errResp map[string]interface{}
		if err := json.Unmarshal(body, &errResp); err == nil {
			if errMsg, ok := errResp["message"].(string); ok {
				return fmt.Errorf("Intercom API error (%d): %s", resp.StatusCode, errMsg)
			}
		}
		return fmt.Errorf("Intercom API error (%d): %s", resp.StatusCode, string(body))
	}

	if err := json.Unmarshal(body, result); err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
	}

	return nil
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
// INTERCOM-CONVERSATION-LIST
// ============================================================================

// ConversationListExecutor handles intercom-conversation-list node type
type ConversationListExecutor struct{}

func (e *ConversationListExecutor) Type() string { return "intercom-conversation-list" }

func (e *ConversationListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	apiToken := getString(step.Config, "apiToken")
	if apiToken == "" {
		return nil, fmt.Errorf("apiToken is required")
	}

	client := getClient(apiToken)

	// Build query parameters
	params := url.Values{}

	state := getString(step.Config, "state")
	if state == "" {
		state = "open"
	}
	params.Set("state", state)

	assignedTo := getString(step.Config, "assignedTo")
	if assignedTo != "" {
		params.Set("assigned_to", assignedTo)
	}

	query := getString(step.Config, "query")
	if query != "" {
		params.Set("query", query)
	}

	page := getInt(step.Config, "page", 1)
	perPage := getInt(step.Config, "perPage", 20)
	params.Set("page", fmt.Sprintf("%d", page))
	params.Set("per_page", fmt.Sprintf("%d", perPage))

	path := "/conversations?" + params.Encode()

	resp, err := client.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}

	var listResp IntercomListResponse
	if err := decodeResponse(resp, &listResp); err != nil {
		return nil, err
	}

	// Parse conversations from raw JSON
	conversations := make([]IntercomConversation, 0, len(listResp.Conversations))
	for _, raw := range listResp.Conversations {
		var conv IntercomConversation
		if err := json.Unmarshal(raw, &conv); err != nil {
			continue
		}
		conversations = append(conversations, conv)
	}

	// Build output
	output := map[string]interface{}{
		"conversations": conversations,
		"count":         len(conversations),
		"total_count":   listResp.TotalCount,
	}

	if listResp.Pages != nil {
		output["page"] = listResp.Pages.Page
		output["per_page"] = listResp.Pages.PerPage
		output["total_pages"] = listResp.Pages.TotalPages
		if listResp.Pages.Next != nil {
			output["next_page"] = *listResp.Pages.Next
		}
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// ============================================================================
// INTERCOM-CONVERSATION-GET
// ============================================================================

// ConversationGetExecutor handles intercom-conversation-get node type
type ConversationGetExecutor struct{}

func (e *ConversationGetExecutor) Type() string { return "intercom-conversation-get" }

func (e *ConversationGetExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	apiToken := getString(step.Config, "apiToken")
	if apiToken == "" {
		return nil, fmt.Errorf("apiToken is required")
	}

	conversationID := getString(step.Config, "conversationId")
	if conversationID == "" {
		return nil, fmt.Errorf("conversationId is required")
	}

	client := getClient(apiToken)

	path := fmt.Sprintf("/conversations/%s", conversationID)

	resp, err := client.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}

	var conversation IntercomConversation
	if err := decodeResponse(resp, &conversation); err != nil {
		return nil, err
	}

	// Build output with useful fields
	output := map[string]interface{}{
		"conversation": conversation,
		"id":           conversation.ID,
		"state":        conversation.State,
		"open":         conversation.Open,
		"title":        conversation.Title,
		"created_at":   conversation.CreatedAt,
		"updated_at":   conversation.UpdatedAt,
	}

	if conversation.Source != nil && conversation.Source.Author != nil {
		output["author_name"] = conversation.Source.Author.Name
		output["author_email"] = conversation.Source.Author.Email
	}

	if conversation.AdminAssignee != nil {
		output["assignee_name"] = conversation.AdminAssignee.Name
		output["assignee_email"] = conversation.AdminAssignee.Email
	}

	if conversation.Tags != nil && len(conversation.Tags.Tags) > 0 {
		var tagNames []string
		for _, tag := range conversation.Tags.Tags {
			tagNames = append(tagNames, tag.Name)
		}
		output["tags"] = tagNames
	}

	if conversation.Statistics != nil {
		output["time_to_assignment"] = conversation.Statistics.TimeToAssignment
		output["time_to_admin_reply"] = conversation.Statistics.TimeToAdminReply
		output["count_reopens"] = conversation.Statistics.CountReopens
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// ============================================================================
// INTERCOM-CONVERSATION-REPLY
// ============================================================================

// ConversationReplyExecutor handles intercom-conversation-reply node type
type ConversationReplyExecutor struct{}

func (e *ConversationReplyExecutor) Type() string { return "intercom-conversation-reply" }

func (e *ConversationReplyExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	apiToken := getString(step.Config, "apiToken")
	if apiToken == "" {
		return nil, fmt.Errorf("apiToken is required")
	}

	conversationID := getString(step.Config, "conversationId")
	if conversationID == "" {
		return nil, fmt.Errorf("conversationId is required")
	}

	body := getString(step.Config, "body")
	if body == "" {
		return nil, fmt.Errorf("body is required")
	}

	messageType := getString(step.Config, "messageType")
	if messageType == "" {
		messageType = "comment"
	}

	client := getClient(apiToken)

	// Build request body
	requestBody := map[string]interface{}{
		"message_type": messageType,
		"body":         body,
	}

	// Add assignee if provided
	assigneeID := getString(step.Config, "assigneeId")
	if assigneeID != "" {
		requestBody["assignee_id"] = assigneeID
	}

	path := fmt.Sprintf("/conversations/%s/reply", conversationID)

	resp, err := client.doRequest(ctx, "POST", path, requestBody)
	if err != nil {
		return nil, err
	}

	var conversation IntercomConversation
	if err := decodeResponse(resp, &conversation); err != nil {
		return nil, err
	}

	output := map[string]interface{}{
		"conversation": conversation,
		"id":           conversation.ID,
		"success":      true,
		"state":        conversation.State,
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// ============================================================================
// INTERCOM-USER-LIST
// ============================================================================

// UserListExecutor handles intercom-user-list node type
type UserListExecutor struct{}

func (e *UserListExecutor) Type() string { return "intercom-user-list" }

func (e *UserListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	apiToken := getString(step.Config, "apiToken")
	if apiToken == "" {
		return nil, fmt.Errorf("apiToken is required")
	}

	client := getClient(apiToken)

	// Build query parameters
	params := url.Values{}

	email := getString(step.Config, "email")
	if email != "" {
		params.Set("email", email)
	}

	phone := getString(step.Config, "phone")
	if phone != "" {
		params.Set("phone", phone)
	}

	userID := getString(step.Config, "userId")
	if userID != "" {
		params.Set("user_id", userID)
	}

	query := getString(step.Config, "query")
	if query != "" {
		params.Set("query", query)
	}

	page := getInt(step.Config, "page", 1)
	perPage := getInt(step.Config, "perPage", 50)
	params.Set("page", fmt.Sprintf("%d", page))
	params.Set("per_page", fmt.Sprintf("%d", perPage))

	path := "/contacts?" + params.Encode()

	resp, err := client.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}

	var listResp IntercomListResponse
	if err := decodeResponse(resp, &listResp); err != nil {
		return nil, err
	}

	// Parse users from raw JSON
	users := make([]IntercomUser, 0, len(listResp.Users))
	for _, raw := range listResp.Users {
		var user IntercomUser
		if err := json.Unmarshal(raw, &user); err != nil {
			continue
		}
		users = append(users, user)
	}

	// Build output
	output := map[string]interface{}{
		"users":       users,
		"count":       len(users),
		"total_count": listResp.TotalCount,
	}

	if listResp.Pages != nil {
		output["page"] = listResp.Pages.Page
		output["per_page"] = listResp.Pages.PerPage
		output["total_pages"] = listResp.Pages.TotalPages
		if listResp.Pages.Next != nil {
			output["next_page"] = *listResp.Pages.Next
		}
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// ============================================================================
// INTERCOM-USER-CREATE
// ============================================================================

// UserCreateExecutor handles intercom-user-create node type
type UserCreateExecutor struct{}

func (e *UserCreateExecutor) Type() string { return "intercom-user-create" }

func (e *UserCreateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	apiToken := getString(step.Config, "apiToken")
	if apiToken == "" {
		return nil, fmt.Errorf("apiToken is required")
	}

	client := getClient(apiToken)

	// Build request body
	requestBody := map[string]interface{}{}

	email := getString(step.Config, "email")
	if email != "" {
		requestBody["email"] = email
	}

	phone := getString(step.Config, "phone")
	if phone != "" {
		requestBody["phone"] = phone
	}

	userID := getString(step.Config, "userId")
	if userID != "" {
		requestBody["user_id"] = userID
	}

	name := getString(step.Config, "name")
	if name != "" {
		requestBody["name"] = name
	}

	// Build location if provided
	city := getString(step.Config, "city")
	region := getString(step.Config, "region")
	country := getString(step.Config, "country")
	if city != "" || region != "" || country != "" {
		location := map[string]interface{}{}
		if city != "" {
			location["city"] = city
		}
		if region != "" {
			location["region"] = region
		}
		if country != "" {
			location["country"] = country
		}
		requestBody["location"] = location
	}

	// Add custom attributes if provided
	customAttrs := getMap(step.Config, "customAttributes")
	if customAttrs != nil && len(customAttrs) > 0 {
		requestBody["custom_attributes"] = customAttrs
	}

	// Add companies if provided
	companies := getMap(step.Config, "companies")
	if companies != nil {
		requestBody["companies"] = companies
	}

	resp, err := client.doRequest(ctx, "POST", "/contacts", requestBody)
	if err != nil {
		return nil, err
	}

	var user IntercomUser
	if err := decodeResponse(resp, &user); err != nil {
		return nil, err
	}

	output := map[string]interface{}{
		"user":    user,
		"id":      user.ID,
		"success": true,
	}

	if user.Email != "" {
		output["email"] = user.Email
	}
	if user.Name != "" {
		output["name"] = user.Name
	}
	if user.UserID != "" {
		output["user_id"] = user.UserID
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// ============================================================================
// INTERCOM-MESSAGE-SEND
// ============================================================================

// MessageSendExecutor handles intercom-message-send node type
type MessageSendExecutor struct{}

func (e *MessageSendExecutor) Type() string { return "intercom-message-send" }

func (e *MessageSendExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	apiToken := getString(step.Config, "apiToken")
	if apiToken == "" {
		return nil, fmt.Errorf("apiToken is required")
	}

	messageType := getString(step.Config, "messageType")
	if messageType == "" {
		messageType = "email"
	}

	body := getString(step.Config, "body")
	if body == "" {
		return nil, fmt.Errorf("body is required")
	}

	client := getClient(apiToken)

	// Build request body
	requestBody := map[string]interface{}{
		"message_type": messageType,
		"body":         body,
	}

	// Add subject for email messages
	subject := getString(step.Config, "subject")
	if messageType == "email" && subject != "" {
		requestBody["subject"] = subject
	}

	// Build recipient (to)
	toUserID := getString(step.Config, "toUserId")
	toEmail := getString(step.Config, "toEmail")

	to := map[string]interface{}{}
	if toUserID != "" {
		to["type"] = "user"
		to["id"] = toUserID
	} else if toEmail != "" {
		to["type"] = "contact"
		to["email"] = toEmail
	} else {
		return nil, fmt.Errorf("toUserId or toEmail is required")
	}
	requestBody["to"] = to

	// Add sender (from) if provided
	fromAdminID := getString(step.Config, "fromAdminId")
	if fromAdminID != "" {
		requestBody["from"] = map[string]interface{}{
			"type": "admin",
			"id":   fromAdminID,
		}
	}

	resp, err := client.doRequest(ctx, "POST", "/messages", requestBody)
	if err != nil {
		return nil, err
	}

	var message IntercomMessage
	if err := decodeResponse(resp, &message); err != nil {
		return nil, err
	}

	output := map[string]interface{}{
		"message": message,
		"id":      message.ID,
		"success": true,
		"type":    message.MessageType,
	}

	if message.ConversationID != "" {
		output["conversation_id"] = message.ConversationID
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// ============================================================================
// INTERCOM-TAG-CREATE
// ============================================================================

// TagCreateExecutor handles intercom-tag-create node type
type TagCreateExecutor struct{}

func (e *TagCreateExecutor) Type() string { return "intercom-tag-create" }

func (e *TagCreateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	apiToken := getString(step.Config, "apiToken")
	if apiToken == "" {
		return nil, fmt.Errorf("apiToken is required")
	}

	name := getString(step.Config, "name")
	if name == "" {
		return nil, fmt.Errorf("name is required")
	}

	client := getClient(apiToken)

	// Build request body
	requestBody := map[string]interface{}{
		"name": name,
	}

	resp, err := client.doRequest(ctx, "POST", "/tags", requestBody)
	if err != nil {
		return nil, err
	}

	var tag IntercomTag
	if err := decodeResponse(resp, &tag); err != nil {
		return nil, err
	}

	output := map[string]interface{}{
		"tag":     tag,
		"id":      tag.ID,
		"name":    tag.Name,
		"success": true,
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// ============================================================================
// INTERCOM-TAG-APPLY
// ============================================================================

// TagApplyExecutor handles intercom-tag-apply node type
type TagApplyExecutor struct{}

func (e *TagApplyExecutor) Type() string { return "intercom-tag-apply" }

func (e *TagApplyExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	apiToken := getString(step.Config, "apiToken")
	if apiToken == "" {
		return nil, fmt.Errorf("apiToken is required")
	}

	tagID := getString(step.Config, "tagId")
	tagName := getString(step.Config, "tagName")
	userId := getString(step.Config, "userId")
	conversationID := getString(step.Config, "conversationId")

	if tagID == "" && tagName == "" {
		return nil, fmt.Errorf("tagId or tagName is required")
	}
	if userId == "" && conversationID == "" {
		return nil, fmt.Errorf("userId or conversationId is required")
	}

	client := getClient(apiToken)

	// Build request body
	requestBody := map[string]interface{}{}

	// Set tag identifier
	if tagID != "" {
		requestBody["tag"] = map[string]interface{}{
			"id": tagID,
		}
	} else {
		requestBody["tag"] = map[string]interface{}{
			"name": tagName,
		}
	}

	// Set user or conversation identifier
	var path string
	if userId != "" {
		// Check if it's an intercom ID or external user_id
		if strings.HasPrefix(userId, "cont_") || strings.HasPrefix(userId, "user_") {
			requestBody["contact"] = map[string]interface{}{
				"id": userId,
			}
		} else {
			requestBody["contact"] = map[string]interface{}{
				"user_id": userId,
			}
		}
		path = "/contacts/tags"
	} else if conversationID != "" {
		requestBody["conversation"] = map[string]interface{}{
			"id": conversationID,
		}
		path = "/conversations/tags"
	}

	resp, err := client.doRequest(ctx, "POST", path, requestBody)
	if err != nil {
		return nil, err
	}

	// Tag apply returns the tag object
	var tag IntercomTag
	if err := decodeResponse(resp, &tag); err != nil {
		// If decode fails, try to return success anyway
		return &executor.StepResult{
			Output: map[string]interface{}{
				"success": true,
				"tag_id":  tagID,
				"tag_name": tagName,
			},
		}, nil
	}

	output := map[string]interface{}{
		"tag":     tag,
		"id":      tag.ID,
		"name":    tag.Name,
		"success": true,
	}

	if userId != "" {
		output["user_id"] = userId
	}
	if conversationID != "" {
		output["conversation_id"] = conversationID
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}
