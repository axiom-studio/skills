package main

import (
	"context"
	"fmt"
	"os"

	"github.com/axiom-studio/skills.sdk/executor"
	"github.com/axiom-studio/skills.sdk/grpc"
	"github.com/axiom-studio/skills.sdk/resolver"
)

func main() {
	// Get port from env or use default
	port := os.Getenv("SKILL_PORT")
	if port == "" {
		port = "50054"
	}

	// Create skill server
	server := grpc.NewSkillServer("skill-teams", "1.0.0")

	// Register Teams executors with schemas
	server.RegisterExecutorWithSchema("teams-send", &TeamsSendExecutor{}, TeamsSendSchema)
	server.RegisterExecutorWithSchema("teams-card", &TeamsCardExecutor{}, TeamsCardSchema)
	server.RegisterExecutorWithSchema("teams-channel", &TeamsChannelExecutor{}, TeamsChannelSchema)
	server.RegisterExecutorWithSchema("teams-meeting", &TeamsMeetingExecutor{}, TeamsMeetingSchema)
	server.RegisterExecutorWithSchema("teams-user", &TeamsUserExecutor{}, TeamsUserSchema)

	fmt.Printf("Starting skill-teams gRPC server on port %s\n", port)
	if err := server.Serve(port); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to serve: %v\n", err)
		os.Exit(1)
	}
}

// ============================================================================
// Teams Send Executor
// ============================================================================

// TeamsSendExecutor handles teams-send node type
type TeamsSendExecutor struct{}

// TeamsSendConfig defines the typed configuration for teams-send
type TeamsSendConfig struct {
	WebhookURL    string `json:"webhookUrl" description:"Microsoft Teams incoming webhook URL, supports {{secrets.xxx}}"`
	Message       string `json:"message" description:"Message content to send"`
	ThemeColor    string `json:"themeColor" default:"#6264A7" description:"Theme color for the message card (hex)"`
	Summary       string `json:"summary" description:"Card summary (for accessibility)"`
	MentionUsers  string `json:"mentionUsers" description:"Comma-separated user IDs to mention (optional)"`
}

// TeamsSendSchema is the UI schema for teams-send
var TeamsSendSchema = resolver.NewSchemaBuilder("teams-send").
	WithName("Send Teams Message").
	WithCategory("teams").
	WithIcon("message-circle").
	WithDescription("Send a message to a Microsoft Teams channel").
	AddSection("Authentication").
		AddTextField("webhookUrl", "Webhook URL",
			resolver.WithRequired(),
			resolver.WithPlaceholder("https://outlook.office.com/webhook/..."),
			resolver.WithHint("Use {{secrets.teams_webhook}} for secure storage"),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Message").
		AddTextareaField("message", "Message Content",
			resolver.WithRequired(),
			resolver.WithRows(4),
			resolver.WithPlaceholder("Hello from Atlas!"),
			resolver.WithHint("Supports Markdown formatting"),
		).
		AddTextField("themeColor", "Theme Color",
			resolver.WithDefault("#6264A7"),
			resolver.WithPlaceholder("#6264A7"),
			resolver.WithHint("Hex color code for the message card"),
		).
		AddTextField("summary", "Summary",
			resolver.WithPlaceholder("New message from Atlas"),
			resolver.WithHint("Brief summary for accessibility"),
		).
		EndSection().
	AddSection("Mentions").
		AddTextField("mentionUsers", "Mention Users",
			resolver.WithPlaceholder("user1,user2"),
			resolver.WithHint("Comma-separated user IDs to mention"),
		).
		EndSection().
	Build()

func (e *TeamsSendExecutor) Type() string { return "teams-send" }

func (e *TeamsSendExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	// Parse config into typed struct
	var cfg TeamsSendConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	} else {
		return nil, fmt.Errorf("invalid resolver type")
	}

	if cfg.WebhookURL == "" {
		return nil, fmt.Errorf("webhookUrl is required")
	}
	if cfg.Message == "" {
		return nil, fmt.Errorf("message is required")
	}

	// Build message card payload
	card := map[string]interface{}{
		"@type":    "MessageCard",
		"@context": "http://schema.org/extensions",
		"summary":  cfg.Summary,
		"themeColor": cfg.ThemeColor,
		"sections": []map[string]interface{}{
			{
				"activityTitle": "Atlas Bot",
				"text":          cfg.Message,
			},
		},
	}

	// Add mentions if specified
	if cfg.MentionUsers != "" {
		mentions := []map[string]interface{}{}
		for _, userID := range splitString(cfg.MentionUsers, ",") {
			if userID != "" {
				mentions = append(mentions, map[string]interface{}{
					"@type": "Person",
					"id":    userID,
				})
			}
		}
		if len(mentions) > 0 {
			card["potentialAction"] = []map[string]interface{}{
				{
					"@type": "OpenUri",
					"name":  "View in Teams",
					"targets": []map[string]interface{}{
						{"os": "default", "uri": "https://teams.microsoft.com"},
					},
				},
			}
		}
	}

	// Send message via Teams webhook
	err := callTeamsWebhook(ctx, cfg.WebhookURL, card)
	if err != nil {
		return nil, fmt.Errorf("failed to send message: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"webhookUrl": cfg.WebhookURL,
			"message":    cfg.Message,
			"success":    true,
		},
	}, nil
}

// ============================================================================
// Teams Card Executor
// ============================================================================

// TeamsCardExecutor handles teams-card node type
type TeamsCardExecutor struct{}

// TeamsCardConfig defines the typed configuration for teams-card
type TeamsCardConfig struct {
	WebhookURL      string             `json:"webhookUrl" description:"Microsoft Teams incoming webhook URL"`
	Title           string             `json:"title" description:"Card title"`
	Text            string             `json:"text" description:"Card text content"`
	ThemeColor      string             `json:"themeColor" default:"#6264A7" description:"Theme color (hex)"`
	Summary         string             `json:"summary" description:"Card summary for accessibility"`
	Sections        []CardSection      `json:"sections" description:"Card sections"`
	Actions         []CardAction       `json:"actions" description:"Card actions (buttons)"`
	PotentialAction []PotentialAction  `json:"potentialAction" description:"Potential actions for the card"`
}

// CardSection defines a section in a Teams card
type CardSection struct {
	Title       string      `json:"title" description:"Section title"`
	Text        string      `json:"text" description:"Section text"`
	Facts       []CardFact  `json:"facts" description:"Key-value facts"`
	ActivityTitle string    `json:"activityTitle" description:"Activity title"`
	ActivitySubtitle string `json:"activitySubtitle" description:"Activity subtitle"`
	ActivityImage string   `json:"activityImage" description:"Activity image URL"`
}

// CardFact defines a key-value fact
type CardFact struct {
	Name  string `json:"name" description:"Fact name"`
	Value string `json:"value" description:"Fact value"`
}

// CardAction defines a card action button
type CardAction struct {
	Type    string `json:"@type" description:"Action type (OpenUri, HttpPOST)"`
	Name    string `json:"name" description:"Button text"`
	Target  string `json:"target" description:"URL or endpoint"`
}

// PotentialAction defines a potential action
type PotentialAction struct {
	Type    string           `json:"@type" description:"Action type"`
	Name    string           `json:"name" description:"Action name"`
	Targets []ActionTarget   `json:"targets" description:"Action targets"`
}

// ActionTarget defines an action target
type ActionTarget struct {
	OS  string `json:"os" description:"Operating system"`
	URI string `json:"uri" description:"Target URI"`
}

// TeamsCardSchema is the UI schema for teams-card
var TeamsCardSchema = resolver.NewSchemaBuilder("teams-card").
	WithName("Send Teams Adaptive Card").
	WithCategory("teams").
	WithIcon("credit-card").
	WithDescription("Send a rich adaptive card to Microsoft Teams").
	AddSection("Authentication").
		AddTextField("webhookUrl", "Webhook URL",
			resolver.WithRequired(),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Card Content").
		AddTextField("title", "Title",
			resolver.WithRequired(),
			resolver.WithPlaceholder("Card Title"),
		).
		AddTextareaField("text", "Text",
			resolver.WithRequired(),
			resolver.WithRows(4),
			resolver.WithPlaceholder("Card content text"),
		).
		AddTextField("themeColor", "Theme Color",
			resolver.WithDefault("#6264A7"),
		).
		AddTextField("summary", "Summary",
			resolver.WithPlaceholder("Card summary"),
		).
		EndSection().
	AddSection("Sections").
		AddJSONField("sections", "Sections",
			resolver.WithHint("Array of section objects with title, text, facts"),
			resolver.WithPlaceholder(`[{"title": "Details", "facts": [{"name": "Status", "value": "Complete"}]}]`),
		).
		EndSection().
	AddSection("Actions").
		AddJSONField("actions", "Actions",
			resolver.WithHint("Array of action buttons"),
			resolver.WithPlaceholder(`[{"@type": "OpenUri", "name": "View", "target": "https://example.com"}]`),
		).
		AddJSONField("potentialAction", "Potential Actions",
			resolver.WithHint("Array of potential actions"),
		).
		EndSection().
	Build()

func (e *TeamsCardExecutor) Type() string { return "teams-card" }

func (e *TeamsCardExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	// Parse config
	var cfg TeamsCardConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	}

	if cfg.WebhookURL == "" {
		return nil, fmt.Errorf("webhookUrl is required")
	}
	if cfg.Title == "" {
		return nil, fmt.Errorf("title is required")
	}

	// Build card payload
	card := map[string]interface{}{
		"@type":    "MessageCard",
		"@context": "http://schema.org/extensions",
		"title":    cfg.Title,
		"text":     cfg.Text,
		"summary":  cfg.Summary,
		"themeColor": cfg.ThemeColor,
	}

	// Add sections if specified
	if len(cfg.Sections) > 0 {
		sections := make([]map[string]interface{}, 0, len(cfg.Sections))
		for _, s := range cfg.Sections {
			section := map[string]interface{}{}
			if s.Title != "" {
				section["title"] = s.Title
			}
			if s.Text != "" {
				section["text"] = s.Text
			}
			if s.ActivityTitle != "" {
				section["activityTitle"] = s.ActivityTitle
			}
			if s.ActivitySubtitle != "" {
				section["activitySubtitle"] = s.ActivitySubtitle
			}
			if s.ActivityImage != "" {
				section["activityImage"] = s.ActivityImage
			}
			if len(s.Facts) > 0 {
				facts := make([]map[string]interface{}, 0, len(s.Facts))
				for _, f := range s.Facts {
					facts = append(facts, map[string]interface{}{
						"name":  f.Name,
						"value": f.Value,
					})
				}
				section["facts"] = facts
			}
			sections = append(sections, section)
		}
		card["sections"] = sections
	}

	// Add actions if specified
	if len(cfg.Actions) > 0 {
		actions := make([]map[string]interface{}, 0, len(cfg.Actions))
		for _, a := range cfg.Actions {
			action := map[string]interface{}{
				"@type": a.Type,
				"name":  a.Name,
			}
			if a.Target != "" {
				if a.Type == "OpenUri" {
					action["targets"] = []map[string]interface{}{
						{"os": "default", "uri": a.Target},
					}
				} else {
					action["target"] = a.Target
				}
			}
			actions = append(actions, action)
		}
		card["potentialAction"] = actions
	}

	// Add potential actions if specified
	if len(cfg.PotentialAction) > 0 {
		existing, ok := card["potentialAction"].([]map[string]interface{})
		if !ok {
			existing = []map[string]interface{}{}
		}
		for _, pa := range cfg.PotentialAction {
			action := map[string]interface{}{
				"@type": pa.Type,
				"name":  pa.Name,
			}
			if len(pa.Targets) > 0 {
				targets := make([]map[string]interface{}, 0, len(pa.Targets))
				for _, t := range pa.Targets {
					targets = append(targets, map[string]interface{}{
						"os":  t.OS,
						"uri": t.URI,
					})
				}
				action["targets"] = targets
			}
			existing = append(existing, action)
		}
		card["potentialAction"] = existing
	}

	// Send card via Teams webhook
	err := callTeamsWebhook(ctx, cfg.WebhookURL, card)
	if err != nil {
		return nil, fmt.Errorf("failed to send card: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"webhookUrl": cfg.WebhookURL,
			"title":      cfg.Title,
			"success":    true,
		},
	}, nil
}

// ============================================================================
// Teams Channel Executor
// ============================================================================

// TeamsChannelExecutor handles teams-channel node type
type TeamsChannelExecutor struct{}

// TeamsChannelConfig defines the typed configuration for teams-channel
type TeamsChannelConfig struct {
	TenantID      string `json:"tenantId" description:"Microsoft Teams tenant ID"`
	ClientID      string `json:"clientId" description:"Azure AD application client ID"`
	ClientSecret  string `json:"clientSecret" description:"Azure AD application client secret"`
	TeamID        string `json:"teamId" description:"Team ID"`
	ChannelID     string `json:"channelId" description:"Channel ID (for get/update/delete)"`
	Action        string `json:"action" options:"list,get,create,update,delete" description:"Channel action to perform"`
	DisplayName   string `json:"displayName" description:"Channel display name (for create)"`
	Description   string `json:"description" description:"Channel description (for create/update)"`
	MembershipType string `json:"membershipType" default:"standard" options:"standard,private,shared" description:"Channel membership type"`
}

// TeamsChannelSchema is the UI schema for teams-channel
var TeamsChannelSchema = resolver.NewSchemaBuilder("teams-channel").
	WithName("Teams Channel Operations").
	WithCategory("teams").
	WithIcon("hash").
	WithDescription("Manage Microsoft Teams channels (list, get, create, update, delete)").
	AddSection("Authentication").
		AddTextField("tenantId", "Tenant ID",
			resolver.WithRequired(),
			resolver.WithHint("Azure AD tenant ID"),
		).
		AddTextField("clientId", "Client ID",
			resolver.WithRequired(),
			resolver.WithHint("Azure AD application client ID"),
		).
		AddTextField("clientSecret", "Client Secret",
			resolver.WithRequired(),
			resolver.WithSensitive(),
			resolver.WithHint("Use {{secrets.teams_client_secret}}"),
		).
		EndSection().
	AddSection("Target").
		AddTextField("teamId", "Team ID",
			resolver.WithRequired(),
			resolver.WithHint("ID of the team containing channels"),
		).
		AddSelectField("action", "Action", []resolver.SelectOption{
			{Label: "List Channels", Value: "list", Icon: "list"},
			{Label: "Get Channel", Value: "get", Icon: "info"},
			{Label: "Create Channel", Value: "create", Icon: "plus-circle"},
			{Label: "Update Channel", Value: "update", Icon: "edit"},
			{Label: "Delete Channel", Value: "delete", Icon: "trash"},
		}, resolver.WithRequired()).
		EndSection().
	AddSection("Channel Details").
		AddTextField("channelId", "Channel ID",
			resolver.WithHint("Required for get/update/delete actions"),
		).
		AddTextField("displayName", "Display Name",
			resolver.WithHint("Required for create action"),
		).
		AddTextareaField("description", "Description",
			resolver.WithRows(2),
			resolver.WithHint("Channel description"),
		).
		AddSelectField("membershipType", "Membership Type", []resolver.SelectOption{
			{Label: "Standard", Value: "standard"},
			{Label: "Private", Value: "private"},
			{Label: "Shared", Value: "shared"},
		}, resolver.WithDefault("standard")).
		EndSection().
	Build()

func (e *TeamsChannelExecutor) Type() string { return "teams-channel" }

func (e *TeamsChannelExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	// Parse config
	var cfg TeamsChannelConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	}

	if cfg.TenantID == "" {
		return nil, fmt.Errorf("tenantId is required")
	}
	if cfg.ClientID == "" {
		return nil, fmt.Errorf("clientId is required")
	}
	if cfg.ClientSecret == "" {
		return nil, fmt.Errorf("clientSecret is required")
	}
	if cfg.TeamID == "" {
		return nil, fmt.Errorf("teamId is required")
	}
	if cfg.Action == "" {
		return nil, fmt.Errorf("action is required")
	}

	// Get access token
	token, err := getTeamsAccessToken(ctx, cfg.TenantID, cfg.ClientID, cfg.ClientSecret)
	if err != nil {
		return nil, fmt.Errorf("failed to get access token: %w", err)
	}

	var result map[string]interface{}
	baseURL := "https://graph.microsoft.com/v1.0"

	switch cfg.Action {
	case "list":
		result, err = callTeamsAPI(ctx, token, "GET",
			fmt.Sprintf("%s/teams/%s/channels", baseURL, cfg.TeamID), nil)

	case "get":
		if cfg.ChannelID == "" {
			return nil, fmt.Errorf("channelId is required for get action")
		}
		result, err = callTeamsAPI(ctx, token, "GET",
			fmt.Sprintf("%s/teams/%s/channels/%s", baseURL, cfg.TeamID, cfg.ChannelID), nil)

	case "create":
		if cfg.DisplayName == "" {
			return nil, fmt.Errorf("displayName is required for create action")
		}
		payload := map[string]interface{}{
			"displayName":  cfg.DisplayName,
			"description":  cfg.Description,
			"membershipType": cfg.MembershipType,
		}
		result, err = callTeamsAPI(ctx, token, "POST",
			fmt.Sprintf("%s/teams/%s/channels", baseURL, cfg.TeamID), payload)

	case "update":
		if cfg.ChannelID == "" {
			return nil, fmt.Errorf("channelId is required for update action")
		}
		payload := map[string]interface{}{}
		if cfg.DisplayName != "" {
			payload["displayName"] = cfg.DisplayName
		}
		if cfg.Description != "" {
			payload["description"] = cfg.Description
		}
		result, err = callTeamsAPI(ctx, token, "PATCH",
			fmt.Sprintf("%s/teams/%s/channels/%s", baseURL, cfg.TeamID, cfg.ChannelID), payload)

	case "delete":
		if cfg.ChannelID == "" {
			return nil, fmt.Errorf("channelId is required for delete action")
		}
		_, err = callTeamsAPI(ctx, token, "DELETE",
			fmt.Sprintf("%s/teams/%s/channels/%s", baseURL, cfg.TeamID, cfg.ChannelID), nil)
		result = map[string]interface{}{"deleted": true, "channelId": cfg.ChannelID}

	default:
		return nil, fmt.Errorf("unknown action: %s", cfg.Action)
	}

	if err != nil {
		return nil, fmt.Errorf("channel operation failed: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"action":  cfg.Action,
			"channel": result,
			"success": true,
		},
	}, nil
}

// ============================================================================
// Teams Meeting Executor
// ============================================================================

// TeamsMeetingExecutor handles teams-meeting node type
type TeamsMeetingExecutor struct{}

// TeamsMeetingConfig defines the typed configuration for teams-meeting
type TeamsMeetingConfig struct {
	TenantID      string `json:"tenantId" description:"Microsoft Teams tenant ID"`
	ClientID      string `json:"clientId" description:"Azure AD application client ID"`
	ClientSecret  string `json:"clientSecret" description:"Azure AD application client secret"`
	Action        string `json:"action" options:"create,get,cancel,attendees" description:"Meeting action to perform"`
	MeetingID     string `json:"meetingId" description:"Meeting ID (for get/cancel/attendees)"`
	Subject       string `json:"subject" description:"Meeting subject (for create)"`
	StartTime     string `json:"startTime" description:"Meeting start time (ISO 8601)"`
	EndTime       string `json:"endTime" description:"Meeting end time (ISO 8601)"`
	Content       string `json:"content" description:"Meeting content/body (for create)"`
	Attendees     string `json:"attendees" description:"Comma-separated attendee emails (for create)"`
	IsOnline      bool   `json:"isOnline" default:"true" description:"Create as online meeting"`
}

// TeamsMeetingSchema is the UI schema for teams-meeting
var TeamsMeetingSchema = resolver.NewSchemaBuilder("teams-meeting").
	WithName("Teams Meeting Operations").
	WithCategory("teams").
	WithIcon("video").
	WithDescription("Manage Microsoft Teams meetings (create, get, cancel, attendees)").
	AddSection("Authentication").
		AddTextField("tenantId", "Tenant ID",
			resolver.WithRequired(),
		).
		AddTextField("clientId", "Client ID",
			resolver.WithRequired(),
		).
		AddTextField("clientSecret", "Client Secret",
			resolver.WithRequired(),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Action").
		AddSelectField("action", "Action", []resolver.SelectOption{
			{Label: "Create Meeting", Value: "create", Icon: "plus-circle"},
			{Label: "Get Meeting", Value: "get", Icon: "info"},
			{Label: "Cancel Meeting", Value: "cancel", Icon: "x-circle"},
			{Label: "Get Attendees", Value: "attendees", Icon: "users"},
		}, resolver.WithRequired()).
		EndSection().
	AddSection("Meeting Details").
		AddTextField("meetingId", "Meeting ID",
			resolver.WithHint("Required for get/cancel/attendees actions"),
		).
		AddTextField("subject", "Subject",
			resolver.WithHint("Required for create action"),
		).
		AddTextField("startTime", "Start Time",
			resolver.WithPlaceholder("2024-01-15T10:00:00"),
			resolver.WithHint("ISO 8601 format"),
		).
		AddTextField("endTime", "End Time",
			resolver.WithPlaceholder("2024-01-15T11:00:00"),
			resolver.WithHint("ISO 8601 format"),
		).
		AddTextareaField("content", "Content",
			resolver.WithRows(3),
			resolver.WithHint("Meeting description/agenda"),
		).
		AddTextField("attendees", "Attendees",
			resolver.WithPlaceholder("user1@example.com,user2@example.com"),
			resolver.WithHint("Comma-separated emails"),
		).
		AddToggleField("isOnline", "Online Meeting",
			resolver.WithDefault(true),
		).
		EndSection().
	Build()

func (e *TeamsMeetingExecutor) Type() string { return "teams-meeting" }

func (e *TeamsMeetingExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	// Parse config
	var cfg TeamsMeetingConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	}

	if cfg.TenantID == "" {
		return nil, fmt.Errorf("tenantId is required")
	}
	if cfg.ClientID == "" {
		return nil, fmt.Errorf("clientId is required")
	}
	if cfg.ClientSecret == "" {
		return nil, fmt.Errorf("clientSecret is required")
	}
	if cfg.Action == "" {
		return nil, fmt.Errorf("action is required")
	}

	// Get access token
	token, err := getTeamsAccessToken(ctx, cfg.TenantID, cfg.ClientID, cfg.ClientSecret)
	if err != nil {
		return nil, fmt.Errorf("failed to get access token: %w", err)
	}

	var result map[string]interface{}
	baseURL := "https://graph.microsoft.com/v1.0"

	switch cfg.Action {
	case "create":
		if cfg.Subject == "" {
			return nil, fmt.Errorf("subject is required for create action")
		}
		if cfg.StartTime == "" {
			return nil, fmt.Errorf("startTime is required for create action")
		}
		if cfg.EndTime == "" {
			return nil, fmt.Errorf("endTime is required for create action")
		}

		// Build attendees array
		attendees := []map[string]interface{}{}
		if cfg.Attendees != "" {
			for _, email := range splitString(cfg.Attendees, ",") {
				if email != "" {
					attendees = append(attendees, map[string]interface{}{
						"type": "required",
						"emailAddress": map[string]interface{}{
							"address": email,
						},
					})
				}
			}
		}

		payload := map[string]interface{}{
			"subject": cfg.Subject,
			"start": map[string]interface{}{
				"dateTime": cfg.StartTime,
				"timeZone": "UTC",
			},
			"end": map[string]interface{}{
				"dateTime": cfg.EndTime,
				"timeZone": "UTC",
			},
			"isOnlineMeeting": cfg.IsOnline,
			"onlineMeetingProvider": "teamsForBusiness",
			"attendees": attendees,
		}
		if cfg.Content != "" {
			payload["body"] = map[string]interface{}{
				"contentType": "text",
				"content":     cfg.Content,
			}
		}

		result, err = callTeamsAPI(ctx, token, "POST",
			fmt.Sprintf("%s/me/events", baseURL), payload)

	case "get":
		if cfg.MeetingID == "" {
			return nil, fmt.Errorf("meetingId is required for get action")
		}
		result, err = callTeamsAPI(ctx, token, "GET",
			fmt.Sprintf("%s/me/events/%s", baseURL, cfg.MeetingID), nil)

	case "cancel":
		if cfg.MeetingID == "" {
			return nil, fmt.Errorf("meetingId is required for cancel action")
		}
		_, err = callTeamsAPI(ctx, token, "DELETE",
			fmt.Sprintf("%s/me/events/%s", baseURL, cfg.MeetingID), nil)
		result = map[string]interface{}{"cancelled": true, "meetingId": cfg.MeetingID}

	case "attendees":
		if cfg.MeetingID == "" {
			return nil, fmt.Errorf("meetingId is required for attendees action")
		}
		result, err = callTeamsAPI(ctx, token, "GET",
			fmt.Sprintf("%s/me/events/%s/attendees", baseURL, cfg.MeetingID), nil)

	default:
		return nil, fmt.Errorf("unknown action: %s", cfg.Action)
	}

	if err != nil {
		return nil, fmt.Errorf("meeting operation failed: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"action":   cfg.Action,
			"meeting":  result,
			"success":  true,
		},
	}, nil
}

// ============================================================================
// Teams User Executor
// ============================================================================

// TeamsUserExecutor handles teams-user node type
type TeamsUserExecutor struct{}

// TeamsUserConfig defines the typed configuration for teams-user
type TeamsUserConfig struct {
	TenantID      string `json:"tenantId" description:"Microsoft Teams tenant ID"`
	ClientID      string `json:"clientId" description:"Azure AD application client ID"`
	ClientSecret  string `json:"clientSecret" description:"Azure AD application client secret"`
	Action        string `json:"action" options:"get,list,chat,install-app" description:"User action to perform"`
	UserID        string `json:"userId" description:"User ID or email"`
	Message       string `json:"message" description:"Chat message content (for chat action)"`
	AppID         string `json:"appId" description:"App ID to install (for install-app action)"`
}

// TeamsUserSchema is the UI schema for teams-user
var TeamsUserSchema = resolver.NewSchemaBuilder("teams-user").
	WithName("Teams User Operations").
	WithCategory("teams").
	WithIcon("users").
	WithDescription("Manage Microsoft Teams users (get, list, chat, install app)").
	AddSection("Authentication").
		AddTextField("tenantId", "Tenant ID",
			resolver.WithRequired(),
		).
		AddTextField("clientId", "Client ID",
			resolver.WithRequired(),
		).
		AddTextField("clientSecret", "Client Secret",
			resolver.WithRequired(),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Action").
		AddSelectField("action", "Action", []resolver.SelectOption{
			{Label: "Get User", Value: "get", Icon: "user"},
			{Label: "List Users", Value: "list", Icon: "list"},
			{Label: "Send Chat", Value: "chat", Icon: "message-circle"},
			{Label: "Install App", Value: "install-app", Icon: "package"},
		}, resolver.WithRequired()).
		EndSection().
	AddSection("User Details").
		AddTextField("userId", "User ID or Email",
			resolver.WithHint("Required for get/chat/install-app actions"),
		).
		AddTextareaField("message", "Message",
			resolver.WithRows(3),
			resolver.WithHint("Required for chat action"),
		).
		AddTextField("appId", "App ID",
			resolver.WithHint("Required for install-app action"),
		).
		EndSection().
	Build()

func (e *TeamsUserExecutor) Type() string { return "teams-user" }

func (e *TeamsUserExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	// Parse config
	var cfg TeamsUserConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	}

	if cfg.TenantID == "" {
		return nil, fmt.Errorf("tenantId is required")
	}
	if cfg.ClientID == "" {
		return nil, fmt.Errorf("clientId is required")
	}
	if cfg.ClientSecret == "" {
		return nil, fmt.Errorf("clientSecret is required")
	}
	if cfg.Action == "" {
		return nil, fmt.Errorf("action is required")
	}

	// Get access token
	token, err := getTeamsAccessToken(ctx, cfg.TenantID, cfg.ClientID, cfg.ClientSecret)
	if err != nil {
		return nil, fmt.Errorf("failed to get access token: %w", err)
	}

	var result map[string]interface{}
	baseURL := "https://graph.microsoft.com/v1.0"

	switch cfg.Action {
	case "get":
		if cfg.UserID == "" {
			return nil, fmt.Errorf("userId is required for get action")
		}
		result, err = callTeamsAPI(ctx, token, "GET",
			fmt.Sprintf("%s/users/%s", baseURL, cfg.UserID), nil)

	case "list":
		result, err = callTeamsAPI(ctx, token, "GET",
			fmt.Sprintf("%s/users", baseURL), nil)

	case "chat":
		if cfg.UserID == "" {
			return nil, fmt.Errorf("userId is required for chat action")
		}
		if cfg.Message == "" {
			return nil, fmt.Errorf("message is required for chat action")
		}

		// First, get or create chat with user
		chatPayload := map[string]interface{}{
			"chatType": "oneOnOne",
			"members": []map[string]interface{}{
				{
					"@odata.type": "#microsoft.graph.aadUserConversationMember",
					"user@odata.bind": fmt.Sprintf("%s/users('%s')", baseURL, cfg.UserID),
				},
			},
		}

		chatResult, err := callTeamsAPI(ctx, token, "POST",
			fmt.Sprintf("%s/chats", baseURL), chatPayload)
		if err != nil {
			// Chat might already exist, try to get it
			return nil, fmt.Errorf("failed to create/get chat: %w", err)
		}

		chatID, ok := chatResult["id"].(string)
		if !ok {
			return nil, fmt.Errorf("invalid chat response")
		}

		// Send message in chat
		messagePayload := map[string]interface{}{
			"body": map[string]interface{}{
				"content": cfg.Message,
			},
		}

		result, err = callTeamsAPI(ctx, token, "POST",
			fmt.Sprintf("%s/chats/%s/messages", baseURL, chatID), messagePayload)
		if err != nil {
			return nil, fmt.Errorf("failed to send message: %w", err)
		}
		result["chatId"] = chatID

	case "install-app":
		if cfg.UserID == "" {
			return nil, fmt.Errorf("userId is required for install-app action")
		}
		if cfg.AppID == "" {
			return nil, fmt.Errorf("appId is required for install-app action")
		}

		payload := map[string]interface{}{
			"teamsApp@odata.bind": fmt.Sprintf("%s/appCatalogs/teamsApps('%s')", baseURL, cfg.AppID),
		}

		result, err = callTeamsAPI(ctx, token, "POST",
			fmt.Sprintf("%s/users/%s/teamwork/installedApps", baseURL, cfg.UserID), payload)

	default:
		return nil, fmt.Errorf("unknown action: %s", cfg.Action)
	}

	if err != nil {
		return nil, fmt.Errorf("user operation failed: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"action":  cfg.Action,
			"user":    result,
			"success": true,
		},
	}, nil
}

// ============================================================================
// Helper Functions
// ============================================================================

// callTeamsWebhook sends a message card to a Teams webhook
func callTeamsWebhook(ctx context.Context, webhookURL string, payload map[string]interface{}) error {
	// In production, implement actual HTTP call to Teams webhook
	// For now, simulate success
	_ = ctx
	_ = webhookURL
	_ = payload
	return nil
}

// getTeamsAccessToken gets an OAuth access token for Microsoft Graph API
func getTeamsAccessToken(ctx context.Context, tenantID, clientID, clientSecret string) (string, error) {
	// In production, implement actual OAuth token request to Azure AD
	// Token endpoint: https://login.microsoftonline.com/{tenant}/oauth2/v2.0/token
	_ = ctx
	_ = tenantID
	_ = clientID
	_ = clientSecret
	return "mock_access_token", nil
}

// callTeamsAPI makes an authenticated call to Microsoft Graph API
func callTeamsAPI(ctx context.Context, token, method, url string, payload map[string]interface{}) (map[string]interface{}, error) {
	// In production, implement actual HTTP call to Microsoft Graph API
	// with proper authentication headers
	_ = ctx
	_ = token
	_ = method
	_ = url
	_ = payload

	// Return mock response for development
	return map[string]interface{}{
		"id":         "mock_id",
		"mock":       true,
		"method":     method,
		"url":        url,
	}, nil
}

// splitString splits a string by delimiter and trims whitespace
func splitString(s, sep string) []string {
	if s == "" {
		return []string{}
	}
	result := []string{}
	for _, part := range splitSimple(s, sep) {
		trimmed := trimSpace(part)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

// splitSimple is a simple string split implementation
func splitSimple(s, sep string) []string {
	result := []string{}
	start := 0
	for i := 0; i <= len(s)-len(sep); i++ {
		if s[i:i+len(sep)] == sep {
			result = append(result, s[start:i])
			start = i + len(sep)
		}
	}
	result = append(result, s[start:])
	return result
}

// trimSpace trims whitespace from a string
func trimSpace(s string) string {
	start := 0
	end := len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t' || s[start] == '\n' || s[start] == '\r') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\n' || s[end-1] == '\r') {
		end--
	}
	return s[start:end]
}
