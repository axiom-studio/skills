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
	"time"

	"github.com/axiom-studio/skills.sdk/executor"
	"github.com/axiom-studio/skills.sdk/grpc"
	"github.com/axiom-studio/skills.sdk/resolver"
)

// Calendly API configuration
const (
	CalendlyAPIBase = "https://api.calendly.com"
)

// Calendly client cache
var (
	calendlyClients = make(map[string]*CalendlyClient)
	clientMutex     sync.RWMutex
)

// CalendlyClient represents a Calendly API client
type CalendlyClient struct {
	APIToken string
}

// ============================================================================
// CALENDLY API RESPONSE TYPES
// ============================================================================

// CalendlyUser represents a Calendly user
type CalendlyUser struct {
	URI         string           `json:"uri"`
	Email       string           `json:"email"`
	Name        string           `json:"name"`
	Slug        string           `json:"slug"`
	Timezone    string           `json:"timezone"`
	AvatarURL   string           `json:"avatar_url"`
	CreatedAt   time.Time        `json:"created_at"`
	UpdatedAt   time.Time        `json:"updated_at"`
	FirstName   string           `json:"first_name"`
	LastName    string           `json:"last_name"`
	ColorScheme string           `json:"color_scheme"`
	Links       CalendlyLinks    `json:"links"`
	Organization CalendlyOrganization `json:"organization"`
}

// CalendlyOrganization represents a Calendly organization
type CalendlyOrganization struct {
	URI       string `json:"uri"`
	Name      string `json:"name"`
	Slug      string `json:"slug"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// CalendlyLinks represents resource links
type CalendlyLinks struct {
	Self string `json:"self"`
}

// CalendlyScheduledEvent represents a scheduled event
type CalendlyScheduledEvent struct {
	URI                  string                    `json:"uri"`
	Name                 string                    `json:"name"`
	Status               string                    `json:"status"`
	StartTime            time.Time                 `json:"start_time"`
	EndTime              time.Time                 `json:"end_time"`
	Timezone             string                    `json:"timezone"`
	CreatedAt            time.Time                 `json:"created_at"`
	UpdatedAt            time.Time                 `json:"updated_at"`
	CalendlyCancelURL    string                    `json:"calendly_cancel_url"`
	CalendlyRescheduleURL string                   `json:"calendly_reschedule_url"`
	CalendlyJoinURL        string                  `json:"calendly_join_url"`
	CalendlyViewURL        string                  `json:"calendly_view_url"`
	EventType            *CalendlyEventTypeRef     `json:"event_type"`
	Invitee              *CalendlyInvitee          `json:"invitee"`
	Invitees             []CalendlyInvitee         `json:"invitees"`
	Location             *CalendlyLocation         `json:"location"`
	CustomQuestions      []CalendlyCustomQuestion  `json:"custom_questions"`
	Answers              []CalendlyAnswer          `json:"answers"`
	AdditionalInfo       map[string]interface{}    `json:"additional_info"`
}

// CalendlyEventTypeRef represents a reference to an event type
type CalendlyEventTypeRef struct {
	URI  string `json:"uri"`
	Name string `json:"name"`
}

// CalendlyInvitee represents an event invitee
type CalendlyInvitee struct {
	Email       string `json:"email"`
	Name        string `json:"name"`
	Timezone    string `json:"timezone"`
	PhoneNumber string `json:"phone_number,omitempty"`
}

// CalendlyLocation represents event location
type CalendlyLocation struct {
	Type     string `json:"type"` // "physical", "phone", "zoom", "teams", "meet", "custom"
	Location string `json:"location"`
}

// CalendlyCustomQuestion represents a custom question
type CalendlyCustomQuestion struct {
	Position int    `json:"position"`
	Name     string `json:"name"`
	Type     string `json:"type"` // "text", "multiline_text", "checkbox", "radio", "select", "int", "float"
	Required bool   `json:"required"`
}

// CalendlyAnswer represents an answer to a custom question
type CalendlyAnswer struct {
	Position int         `json:"position"`
	Question string      `json:"question"`
	Answer   interface{} `json:"answer"`
}

// CalendlyEventCollection represents a collection of events
type CalendlyEventCollection struct {
	Collection []CalendlyScheduledEvent `json:"collection"`
	Pagination *CalendlyPagination        `json:"pagination"`
}

// CalendlyPagination represents pagination info
type CalendlyPagination struct {
	Count    int                  `json:"count"`
	NextPage *CalendlyNextPageRef `json:"next_page"`
}

// CalendlyNextPageRef represents a next page reference
type CalendlyNextPageRef struct {
	URI string `json:"uri"`
}

// CalendlyUserResponse represents a user API response
type CalendlyUserResponse struct {
	Resource CalendlyUser `json:"resource"`
}

// CalendlyScheduledEventResponse represents a single event API response
type CalendlyScheduledEventResponse struct {
	Resource CalendlyScheduledEvent `json:"resource"`
}

// CalendlyAvailabilityResponse represents availability response
type CalendlyAvailabilityResponse struct {
	Schedule CalendlySchedule `json:"schedule"`
}

// CalendlySchedule represents a schedule
type CalendlySchedule struct {
	URI           string                  `json:"uri"`
	Name          string                  `json:"name"`
	Timezone      string                  `json:"timezone"`
	Rules         []CalendlyScheduleRule  `json:"rules"`
	OverrideSets  []CalendlyOverrideSet   `json:"override_sets"`
	Hours         []CalendlyHours         `json:"hours"`
	Availabilities []CalendlyAvailability `json:"availabilities"`
}

// CalendlyScheduleRule represents a schedule rule
type CalendlyScheduleRule struct {
	Type       string   `json:"type"` // "wday", "date", "date_range"
	Weekday    int      `json:"weekday,omitempty"`
	Date       string   `json:"date,omitempty"`
	StartDate  string   `json:"start_date,omitempty"`
	EndDate    string   `json:"end_date,omitempty"`
	Duration   int      `json:"duration"`
	Intervals  []CalendlyInterval `json:"intervals"`
}

// CalendlyInterval represents a time interval
type CalendlyInterval struct {
	StartSeconds int `json:"start_seconds"`
	EndSeconds   int `json:"end_seconds"`
}

// CalendlyHours represents available hours
type CalendlyHours struct {
	DaysOfWeek []int              `json:"days_of_week"`
	Type       string             `json:"type"`
	Intervals  []CalendlyInterval `json:"intervals"`
}

// CalendlyAvailability represents an availability slot
type CalendlyAvailability struct {
	StartTime time.Time `json:"start_time"`
	EndTime   time.Time `json:"end_time"`
}

// CalendlyOverrideSet represents override hours
type CalendlyOverrideSet struct {
	Date      string             `json:"date"`
	Intervals []CalendlyInterval `json:"intervals"`
}

// CalendlyWebhookSubscription represents a webhook subscription
type CalendlyWebhookSubscription struct {
	URI              string   `json:"uri"`
	CallbackURL      string   `json:"callback_url"`
	Organization     string   `json:"organization"`
	Events           []string `json:"events"`
	State            string   `json:"state"` // "active", "paused"
	CreatedAt        string   `json:"created_at"`
	UpdatedAt        string   `json:"updated_at"`
	Scope            string   `json:"scope"` // "organization", "user"
	User             string   `json:"user,omitempty"`
	SigningSecret    string   `json:"signing_secret,omitempty"`
}

// CalendlyWebhookSubscriptionResponse represents webhook creation response
type CalendlyWebhookSubscriptionResponse struct {
	Resource CalendlyWebhookSubscription `json:"resource"`
}

// CalendlyError represents an API error response
type CalendlyError struct {
	Errors []CalendlyErrorDetail `json:"errors"`
}

// CalendlyErrorDetail represents a single error detail
type CalendlyErrorDetail struct {
	Message string `json:"message"`
	Code    string `json:"code,omitempty"`
	Details map[string]interface{} `json:"details,omitempty"`
}

func main() {
	// Get port from env or use default
	port := os.Getenv("SKILL_PORT")
	if port == "" {
		port = "50119"
	}

	// Create skill server
	server := grpc.NewSkillServer("skill-calendly", "1.0.0")

	// Register Calendly executors with schemas
	server.RegisterExecutorWithSchema("calendly-event-list", &CalendlyEventListExecutor{}, CalendlyEventListSchema)
	server.RegisterExecutorWithSchema("calendly-event-get", &CalendlyEventGetExecutor{}, CalendlyEventGetSchema)
	server.RegisterExecutorWithSchema("calendly-event-create", &CalendlyEventCreateExecutor{}, CalendlyEventCreateSchema)
	server.RegisterExecutorWithSchema("calendly-cancel", &CalendlyCancelExecutor{}, CalendlyCancelSchema)
	server.RegisterExecutorWithSchema("calendly-reschedule", &CalendlyRescheduleExecutor{}, CalendlyRescheduleSchema)
	server.RegisterExecutorWithSchema("calendly-availability", &CalendlyAvailabilityExecutor{}, CalendlyAvailabilitySchema)
	server.RegisterExecutorWithSchema("calendly-user-get", &CalendlyUserGetExecutor{}, CalendlyUserGetSchema)
	server.RegisterExecutorWithSchema("calendly-webhook-create", &CalendlyWebhookCreateExecutor{}, CalendlyWebhookCreateSchema)

	fmt.Printf("Starting skill-calendly gRPC server on port %s\n", port)
	if err := server.Serve(port); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to serve: %v\n", err)
		os.Exit(1)
	}
}

// getClient returns or creates a Calendly client (cached)
func getClient(apiToken string) *CalendlyClient {
	clientMutex.RLock()
	client, ok := calendlyClients[apiToken]
	clientMutex.RUnlock()

	if ok {
		return client
	}

	clientMutex.Lock()
	defer clientMutex.Unlock()

	// Double check
	if client, ok := calendlyClients[apiToken]; ok {
		return client
	}

	client = &CalendlyClient{APIToken: apiToken}
	calendlyClients[apiToken] = client
	return client
}

// doRequest performs an HTTP request to the Calendly API
func (c *CalendlyClient) doRequest(ctx context.Context, method, path string, body interface{}) (*http.Response, error) {
	var reqBody io.Reader
	if body != nil {
		jsonData, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
		reqBody = bytes.NewBuffer(jsonData)
	}

	reqURL := CalendlyAPIBase + path
	req, err := http.NewRequestWithContext(ctx, method, reqURL, reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.APIToken))
	req.Header.Set("Content-Type", "application/json")

	httpClient := &http.Client{
		Timeout: 30 * time.Second,
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	return resp, nil
}

// decodeResponse decodes a Calendly API response
func decodeResponse(resp *http.Response, result interface{}) error {
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var errResp CalendlyError
		if err := json.Unmarshal(body, &errResp); err == nil {
			if len(errResp.Errors) > 0 {
				return fmt.Errorf("Calendly API error (%d): %s", resp.StatusCode, errResp.Errors[0].Message)
			}
		}
		return fmt.Errorf("Calendly API error (%d): %s", resp.StatusCode, string(body))
	}

	if err := json.Unmarshal(body, result); err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
	}

	return nil
}

// ============================================================================
// CALENDLY-EVENT-LIST
// ============================================================================

// CalendlyEventListConfig defines the configuration for calendly-event-list
type CalendlyEventListConfig struct {
	APIToken   string `json:"apiToken" description:"Calendly API token"`
	User       string `json:"user" description:"User URI to filter events (optional, defaults to current user)"`
	EventType  string `json:"eventType" description:"Event type URI to filter by (optional)"`
	Status     string `json:"status" description:"Filter by status: active, canceled, or empty for all"`
	StartTime  string `json:"startTime" description:"Filter events starting after this time (ISO 8601)"`
	EndTime    string `json:"endTime" description:"Filter events starting before this time (ISO 8601)"`
	InviteeEmail string `json:"inviteeEmail" description:"Filter by invitee email (optional)"`
	Count      int    `json:"count" default:"20" description:"Number of events to return (max 100)"`
}

// CalendlyEventListSchema is the UI schema for calendly-event-list
var CalendlyEventListSchema = resolver.NewSchemaBuilder("calendly-event-list").
	WithName("Calendly List Events").
	WithCategory("calendly").
	WithIcon("calendar").
	WithDescription("List scheduled events from Calendly").
	AddSection("Connection").
		AddExpressionField("apiToken", "API Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9..."),
			resolver.WithHint("Calendly API token (use {{secrets.calendly_token}})"),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Filters").
		AddTextField("user", "User URI",
			resolver.WithPlaceholder("https://api.calendly.com/users/XXX"),
			resolver.WithHint("Optional: Filter events for specific user"),
		).
		AddTextField("eventType", "Event Type URI",
			resolver.WithPlaceholder("https://api.calendly.com/event_types/XXX"),
			resolver.WithHint("Optional: Filter by event type"),
		).
		AddSelectField("status", "Status",
			[]resolver.SelectOption{
				{Label: "All", Value: ""},
				{Label: "Active", Value: "active"},
				{Label: "Canceled", Value: "canceled"},
			},
			resolver.WithDefault(""),
		).
		AddTextField("inviteeEmail", "Invitee Email",
			resolver.WithPlaceholder("user@example.com"),
			resolver.WithHint("Optional: Filter by invitee email"),
		).
		EndSection().
	AddSection("Time Range").
		AddTextField("startTime", "Start Time",
			resolver.WithPlaceholder("2024-01-01T00:00:00Z"),
			resolver.WithHint("Optional: ISO 8601 format"),
		).
		AddTextField("endTime", "End Time",
			resolver.WithPlaceholder("2024-12-31T23:59:59Z"),
			resolver.WithHint("Optional: ISO 8601 format"),
		).
		EndSection().
	AddSection("Options").
		AddNumberField("count", "Limit",
			resolver.WithDefault(20),
			resolver.WithMinMax(1, 100),
		).
		EndSection().
	Build()

type CalendlyEventListExecutor struct{}

func (e *CalendlyEventListExecutor) Type() string { return "calendly-event-list" }

func (e *CalendlyEventListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg CalendlyEventListConfig
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
	params.Set("count", fmt.Sprintf("%d", cfg.Count))

	if cfg.User != "" {
		params.Set("user", cfg.User)
	}
	if cfg.EventType != "" {
		params.Set("event_type", cfg.EventType)
	}
	if cfg.Status != "" {
		params.Set("status", cfg.Status)
	}
	if cfg.StartTime != "" {
		params.Set("start_time", cfg.StartTime)
	}
	if cfg.EndTime != "" {
		params.Set("end_time", cfg.EndTime)
	}
	if cfg.InviteeEmail != "" {
		params.Set("invitee_email", cfg.InviteeEmail)
	}

	path := "/scheduled_events?" + params.Encode()

	resp, err := client.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}

	var collection CalendlyEventCollection
	if err := decodeResponse(resp, &collection); err != nil {
		return nil, err
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"events":     collection.Collection,
			"count":      len(collection.Collection),
			"hasMore":    collection.Pagination != nil && collection.Pagination.NextPage != nil,
			"nextPageURI": getNextPageURI(collection.Pagination),
		},
	}, nil
}

func getNextPageURI(pagination *CalendlyPagination) string {
	if pagination != nil && pagination.NextPage != nil {
		return pagination.NextPage.URI
	}
	return ""
}

// ============================================================================
// CALENDLY-EVENT-GET
// ============================================================================

// CalendlyEventGetConfig defines the configuration for calendly-event-get
type CalendlyEventGetConfig struct {
	APIToken string `json:"apiToken" description:"Calendly API token"`
	EventURI string `json:"eventUri" description:"URI of the event to retrieve"`
}

// CalendlyEventGetSchema is the UI schema for calendly-event-get
var CalendlyEventGetSchema = resolver.NewSchemaBuilder("calendly-event-get").
	WithName("Calendly Get Event").
	WithCategory("calendly").
	WithIcon("calendar-check").
	WithDescription("Get details of a specific Calendly event").
	AddSection("Connection").
		AddExpressionField("apiToken", "API Token",
			resolver.WithRequired(),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Event").
		AddTextField("eventUri", "Event URI",
			resolver.WithRequired(),
			resolver.WithPlaceholder("https://api.calendly.com/scheduled_events/XXX"),
			resolver.WithHint("URI of the event to retrieve"),
		).
		EndSection().
	Build()

type CalendlyEventGetExecutor struct{}

func (e *CalendlyEventGetExecutor) Type() string { return "calendly-event-get" }

func (e *CalendlyEventGetExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg CalendlyEventGetConfig
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
	if cfg.EventURI == "" {
		return nil, fmt.Errorf("eventUri is required")
	}

	client := getClient(cfg.APIToken)

	// Extract path from URI
	eventPath := extractPathFromURI(cfg.EventURI)
	if eventPath == "" {
		return nil, fmt.Errorf("invalid event URI")
	}

	resp, err := client.doRequest(ctx, "GET", eventPath, nil)
	if err != nil {
		return nil, err
	}

	var eventResp CalendlyScheduledEventResponse
	if err := decodeResponse(resp, &eventResp); err != nil {
		return nil, err
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"event":      eventResp.Resource,
			"uri":        eventResp.Resource.URI,
			"name":       eventResp.Resource.Name,
			"status":     eventResp.Resource.Status,
			"startTime":  eventResp.Resource.StartTime,
			"endTime":    eventResp.Resource.EndTime,
			"invitee":    eventResp.Resource.Invitee,
			"location":   eventResp.Resource.Location,
			"joinURL":    eventResp.Resource.CalendlyJoinURL,
			"viewURL":    eventResp.Resource.CalendlyViewURL,
			"cancelURL":  eventResp.Resource.CalendlyCancelURL,
		},
	}, nil
}

// ============================================================================
// CALENDLY-EVENT-CREATE
// ============================================================================

// CalendlyEventCreateConfig defines the configuration for calendly-event-create
type CalendlyEventCreateConfig struct {
	APIToken        string                 `json:"apiToken" description:"Calendly API token"`
	EventTypeURI    string                 `json:"eventTypeUri" description:"URI of the event type to book"`
	InviteeEmail    string                 `json:"inviteeEmail" description:"Invitee's email address"`
	InviteeName     string                 `json:"inviteeName" description:"Invitee's name"`
	InviteeTimezone string                 `json:"inviteeTimezone" description:"Invitee's timezone"`
	InviteePhone    string                 `json:"inviteePhone" description:"Invitee's phone number (optional)"`
	StartTime       string                 `json:"startTime" description:"Event start time (ISO 8601)"`
	EndTime         string                 `json:"endTime" description:"Event end time (ISO 8601)"`
	Location        string                 `json:"location" description:"Event location (optional)"`
	LocationType    string                 `json:"locationType" description:"Location type: physical, phone, zoom, teams, meet, custom"`
	CustomAnswers   map[string]interface{} `json:"customAnswers" description:"Answers to custom questions (optional)"`
}

// CalendlyEventCreateSchema is the UI schema for calendly-event-create
var CalendlyEventCreateSchema = resolver.NewSchemaBuilder("calendly-event-create").
	WithName("Calendly Create Event").
	WithCategory("calendly").
	WithIcon("calendar-plus").
	WithDescription("Create a new scheduled event in Calendly").
	AddSection("Connection").
		AddExpressionField("apiToken", "API Token",
			resolver.WithRequired(),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Event Type").
		AddTextField("eventTypeUri", "Event Type URI",
			resolver.WithRequired(),
			resolver.WithPlaceholder("https://api.calendly.com/event_types/XXX"),
			resolver.WithHint("URI of the event type to book"),
		).
		EndSection().
	AddSection("Invitee Details").
		AddTextField("inviteeEmail", "Email",
			resolver.WithRequired(),
			resolver.WithPlaceholder("user@example.com"),
		).
		AddTextField("inviteeName", "Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("John Doe"),
		).
		AddTextField("inviteeTimezone", "Timezone",
			resolver.WithRequired(),
			resolver.WithPlaceholder("America/New_York"),
			resolver.WithHint("IANA timezone name"),
		).
		AddTextField("inviteePhone", "Phone",
			resolver.WithPlaceholder("+1-555-123-4567"),
			resolver.WithHint("Optional: Phone number"),
		).
		EndSection().
	AddSection("Event Time").
		AddTextField("startTime", "Start Time",
			resolver.WithRequired(),
			resolver.WithPlaceholder("2024-01-15T10:00:00Z"),
			resolver.WithHint("ISO 8601 format"),
		).
		AddTextField("endTime", "End Time",
			resolver.WithRequired(),
			resolver.WithPlaceholder("2024-01-15T10:30:00Z"),
			resolver.WithHint("ISO 8601 format"),
		).
		EndSection().
	AddSection("Location").
		AddSelectField("locationType", "Location Type",
			[]resolver.SelectOption{
				{Label: "Physical", Value: "physical"},
				{Label: "Phone", Value: "phone"},
				{Label: "Zoom", Value: "zoom"},
				{Label: "Microsoft Teams", Value: "teams"},
				{Label: "Google Meet", Value: "meet"},
				{Label: "Custom", Value: "custom"},
			},
			resolver.WithDefault("zoom"),
		).
		AddTextField("location", "Location Details",
			resolver.WithPlaceholder("https://zoom.us/j/123456789"),
			resolver.WithHint("Address, phone number, or video conference URL"),
		).
		EndSection().
	AddSection("Advanced").
		AddJSONField("customAnswers", "Custom Answers",
			resolver.WithHeight(100),
			resolver.WithHint("Answers to custom questions as key-value pairs"),
		).
		EndSection().
	Build()

type CalendlyEventCreateExecutor struct{}

func (e *CalendlyEventCreateExecutor) Type() string { return "calendly-event-create" }

func (e *CalendlyEventCreateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg CalendlyEventCreateConfig
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
	if cfg.EventTypeURI == "" {
		return nil, fmt.Errorf("eventTypeUri is required")
	}
	if cfg.InviteeEmail == "" {
		return nil, fmt.Errorf("inviteeEmail is required")
	}
	if cfg.InviteeName == "" {
		return nil, fmt.Errorf("inviteeName is required")
	}
	if cfg.StartTime == "" {
		return nil, fmt.Errorf("startTime is required")
	}
	if cfg.EndTime == "" {
		return nil, fmt.Errorf("endTime is required")
	}

	client := getClient(cfg.APIToken)

	// Build request body
	requestBody := map[string]interface{}{
		"event_type": cfg.EventTypeURI,
		"invitee": map[string]interface{}{
			"email":    cfg.InviteeEmail,
			"name":     cfg.InviteeName,
			"timezone": cfg.InviteeTimezone,
		},
		"start_time": cfg.StartTime,
		"end_time":   cfg.EndTime,
	}

	// Add optional fields
	if cfg.InviteePhone != "" {
		requestBody["invitee"].(map[string]interface{})["phone_number"] = cfg.InviteePhone
	}

	if cfg.LocationType != "" {
		location := map[string]interface{}{
			"type": cfg.LocationType,
		}
		if cfg.Location != "" {
			location["location"] = cfg.Location
		}
		requestBody["location"] = location
	}

	if len(cfg.CustomAnswers) > 0 {
		answers := make([]map[string]interface{}, 0)
		for key, value := range cfg.CustomAnswers {
			answers = append(answers, map[string]interface{}{
				"question": key,
				"answer":   value,
			})
		}
		requestBody["answers"] = answers
	}

	resp, err := client.doRequest(ctx, "POST", "/scheduled_events", requestBody)
	if err != nil {
		return nil, err
	}

	var eventResp CalendlyScheduledEventResponse
	if err := decodeResponse(resp, &eventResp); err != nil {
		return nil, err
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"event":       eventResp.Resource,
			"uri":         eventResp.Resource.URI,
			"name":        eventResp.Resource.Name,
			"status":      eventResp.Resource.Status,
			"startTime":   eventResp.Resource.StartTime,
			"endTime":     eventResp.Resource.EndTime,
			"joinURL":     eventResp.Resource.CalendlyJoinURL,
			"viewURL":     eventResp.Resource.CalendlyViewURL,
			"cancelURL":   eventResp.Resource.CalendlyCancelURL,
			"rescheduleURL": eventResp.Resource.CalendlyRescheduleURL,
		},
	}, nil
}

// ============================================================================
// CALENDLY-CANCEL
// ============================================================================

// CalendlyCancelConfig defines the configuration for calendly-cancel
type CalendlyCancelConfig struct {
	APIToken   string `json:"apiToken" description:"Calendly API token"`
	EventURI   string `json:"eventUri" description:"URI of the event to cancel"`
	Reason     string `json:"reason" description:"Optional reason for cancellation"`
}

// CalendlyCancelSchema is the UI schema for calendly-cancel
var CalendlyCancelSchema = resolver.NewSchemaBuilder("calendly-cancel").
	WithName("Calendly Cancel Event").
	WithCategory("calendly").
	WithIcon("calendar-x").
	WithDescription("Cancel a scheduled Calendly event").
	AddSection("Connection").
		AddExpressionField("apiToken", "API Token",
			resolver.WithRequired(),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Event").
		AddTextField("eventUri", "Event URI",
			resolver.WithRequired(),
			resolver.WithPlaceholder("https://api.calendly.com/scheduled_events/XXX"),
			resolver.WithHint("URI of the event to cancel"),
		).
		EndSection().
	AddSection("Options").
		AddTextareaField("reason", "Cancellation Reason",
			resolver.WithRows(3),
			resolver.WithPlaceholder("Reason for cancellation (optional)"),
			resolver.WithHint("This will be included in the cancellation email"),
		).
		EndSection().
	Build()

type CalendlyCancelExecutor struct{}

func (e *CalendlyCancelExecutor) Type() string { return "calendly-cancel" }

func (e *CalendlyCancelExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg CalendlyCancelConfig
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
	if cfg.EventURI == "" {
		return nil, fmt.Errorf("eventUri is required")
	}

	client := getClient(cfg.APIToken)

	// Extract path from URI
	eventPath := extractPathFromURI(cfg.EventURI)
	if eventPath == "" {
		return nil, fmt.Errorf("invalid event URI")
	}

	// Build request body
	requestBody := map[string]interface{}{}
	if cfg.Reason != "" {
		requestBody["reason"] = cfg.Reason
	}

	cancellationPath := eventPath + "/cancellation"

	resp, err := client.doRequest(ctx, "POST", cancellationPath, requestBody)
	if err != nil {
		return nil, err
	}

	var eventResp CalendlyScheduledEventResponse
	if err := decodeResponse(resp, &eventResp); err != nil {
		return nil, err
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"event":     eventResp.Resource,
			"uri":       eventResp.Resource.URI,
			"status":    eventResp.Resource.Status,
			"canceled":  eventResp.Resource.Status == "canceled",
			"updatedAt": eventResp.Resource.UpdatedAt,
		},
	}, nil
}

// ============================================================================
// CALENDLY-RESCHEDULE
// ============================================================================

// CalendlyRescheduleConfig defines the configuration for calendly-reschedule
type CalendlyRescheduleConfig struct {
	APIToken   string `json:"apiToken" description:"Calendly API token"`
	EventURI   string `json:"eventUri" description:"URI of the event to reschedule"`
	StartTime  string `json:"startTime" description:"New start time (ISO 8601)"`
	EndTime    string `json:"endTime" description:"New end time (ISO 8601)"`
	Reason     string `json:"reason" description:"Optional reason for rescheduling"`
}

// CalendlyRescheduleSchema is the UI schema for calendly-reschedule
var CalendlyRescheduleSchema = resolver.NewSchemaBuilder("calendly-reschedule").
	WithName("Calendly Reschedule Event").
	WithCategory("calendly").
	WithIcon("calendar-clock").
	WithDescription("Reschedule an existing Calendly event").
	AddSection("Connection").
		AddExpressionField("apiToken", "API Token",
			resolver.WithRequired(),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Event").
		AddTextField("eventUri", "Event URI",
			resolver.WithRequired(),
			resolver.WithPlaceholder("https://api.calendly.com/scheduled_events/XXX"),
			resolver.WithHint("URI of the event to reschedule"),
		).
		EndSection().
	AddSection("New Time").
		AddTextField("startTime", "New Start Time",
			resolver.WithRequired(),
			resolver.WithPlaceholder("2024-01-15T14:00:00Z"),
			resolver.WithHint("ISO 8601 format"),
		).
		AddTextField("endTime", "New End Time",
			resolver.WithRequired(),
			resolver.WithPlaceholder("2024-01-15T14:30:00Z"),
			resolver.WithHint("ISO 8601 format"),
		).
		EndSection().
	AddSection("Options").
		AddTextareaField("reason", "Reschedule Reason",
			resolver.WithRows(3),
			resolver.WithPlaceholder("Reason for rescheduling (optional)"),
			resolver.WithHint("This will be included in the reschedule notification"),
		).
		EndSection().
	Build()

type CalendlyRescheduleExecutor struct{}

func (e *CalendlyRescheduleExecutor) Type() string { return "calendly-reschedule" }

func (e *CalendlyRescheduleExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg CalendlyRescheduleConfig
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
	if cfg.EventURI == "" {
		return nil, fmt.Errorf("eventUri is required")
	}
	if cfg.StartTime == "" {
		return nil, fmt.Errorf("startTime is required")
	}
	if cfg.EndTime == "" {
		return nil, fmt.Errorf("endTime is required")
	}

	client := getClient(cfg.APIToken)

	// Extract path from URI
	eventPath := extractPathFromURI(cfg.EventURI)
	if eventPath == "" {
		return nil, fmt.Errorf("invalid event URI")
	}

	// Build request body
	requestBody := map[string]interface{}{
		"start_time": cfg.StartTime,
		"end_time":   cfg.EndTime,
	}
	if cfg.Reason != "" {
		requestBody["reason"] = cfg.Reason
	}

	reschedulePath := eventPath + "/reschedule"

	resp, err := client.doRequest(ctx, "POST", reschedulePath, requestBody)
	if err != nil {
		return nil, err
	}

	var eventResp CalendlyScheduledEventResponse
	if err := decodeResponse(resp, &eventResp); err != nil {
		return nil, err
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"event":       eventResp.Resource,
			"uri":         eventResp.Resource.URI,
			"status":      eventResp.Resource.Status,
			"startTime":   eventResp.Resource.StartTime,
			"endTime":     eventResp.Resource.EndTime,
			"rescheduled": true,
			"updatedAt":   eventResp.Resource.UpdatedAt,
		},
	}, nil
}

// ============================================================================
// CALENDLY-AVAILABILITY
// ============================================================================

// CalendlyAvailabilityConfig defines the configuration for calendly-availability
type CalendlyAvailabilityConfig struct {
	APIToken      string `json:"apiToken" description:"Calendly API token"`
	UserURI       string `json:"userUri" description:"User URI (optional, defaults to current user)"`
	EventTypeURI  string `json:"eventTypeUri" description:"Event type URI to check availability for"`
	StartTime     string `json:"startTime" description:"Start of availability window (ISO 8601)"`
	EndTime       string `json:"endTime" description:"End of availability window (ISO 8601)"`
	Duration      int    `json:"duration" description:"Required duration in minutes"`
	MaxResults    int    `json:"maxResults" default:"10" description:"Maximum number of slots to return"`
}

// CalendlyAvailabilitySchema is the UI schema for calendly-availability
var CalendlyAvailabilitySchema = resolver.NewSchemaBuilder("calendly-availability").
	WithName("Calendly Get Availability").
	WithCategory("calendly").
	WithIcon("calendar-search").
	WithDescription("Get available time slots from Calendly").
	AddSection("Connection").
		AddExpressionField("apiToken", "API Token",
			resolver.WithRequired(),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Event Type").
		AddTextField("eventTypeUri", "Event Type URI",
			resolver.WithRequired(),
			resolver.WithPlaceholder("https://api.calendly.com/event_types/XXX"),
			resolver.WithHint("URI of the event type to check availability for"),
		).
		EndSection().
	AddSection("User").
		AddTextField("userUri", "User URI",
			resolver.WithPlaceholder("https://api.calendly.com/users/XXX"),
			resolver.WithHint("Optional: Check availability for specific user"),
		).
		EndSection().
	AddSection("Time Window").
		AddTextField("startTime", "Start Time",
			resolver.WithRequired(),
			resolver.WithPlaceholder("2024-01-15T00:00:00Z"),
			resolver.WithHint("ISO 8601 format"),
		).
		AddTextField("endTime", "End Time",
			resolver.WithRequired(),
			resolver.WithPlaceholder("2024-01-15T23:59:59Z"),
			resolver.WithHint("ISO 8601 format"),
		).
		EndSection().
	AddSection("Options").
		AddNumberField("duration", "Duration (minutes)",
			resolver.WithDefault(30),
			resolver.WithHint("Required meeting duration"),
		).
		AddNumberField("maxResults", "Max Results",
			resolver.WithDefault(10),
			resolver.WithMinMax(1, 50),
		).
		EndSection().
	Build()

type CalendlyAvailabilityExecutor struct{}

func (e *CalendlyAvailabilityExecutor) Type() string { return "calendly-availability" }

func (e *CalendlyAvailabilityExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg CalendlyAvailabilityConfig
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
	if cfg.EventTypeURI == "" {
		return nil, fmt.Errorf("eventTypeUri is required")
	}
	if cfg.StartTime == "" {
		return nil, fmt.Errorf("startTime is required")
	}
	if cfg.EndTime == "" {
		return nil, fmt.Errorf("endTime is required")
	}

	client := getClient(cfg.APIToken)

	// Build query parameters
	params := url.Values{}
	params.Set("event_type", cfg.EventTypeURI)
	params.Set("start_time", cfg.StartTime)
	params.Set("end_time", cfg.EndTime)
	params.Set("duration", fmt.Sprintf("%d", cfg.Duration))
	params.Set("max_results", fmt.Sprintf("%d", cfg.MaxResults))

	if cfg.UserURI != "" {
		params.Set("user", cfg.UserURI)
	}

	path := "/timeslots/available?" + params.Encode()

	resp, err := client.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}

	var collection CalendlyEventCollection
	if err := decodeResponse(resp, &collection); err != nil {
		return nil, err
	}

	// Extract available time slots
	availableSlots := make([]map[string]interface{}, 0, len(collection.Collection))
	for _, event := range collection.Collection {
		slot := map[string]interface{}{
			"startTime": event.StartTime,
			"endTime":   event.EndTime,
		}
		availableSlots = append(availableSlots, slot)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"availableSlots": availableSlots,
			"count":          len(availableSlots),
			"startTime":      cfg.StartTime,
			"endTime":        cfg.EndTime,
			"duration":       cfg.Duration,
		},
	}, nil
}

// ============================================================================
// CALENDLY-USER-GET
// ============================================================================

// CalendlyUserGetConfig defines the configuration for calendly-user-get
type CalendlyUserGetConfig struct {
	APIToken string `json:"apiToken" description:"Calendly API token"`
	UserURI  string `json:"userUri" description:"User URI (optional, defaults to current user)"`
}

// CalendlyUserGetSchema is the UI schema for calendly-user-get
var CalendlyUserGetSchema = resolver.NewSchemaBuilder("calendly-user-get").
	WithName("Calendly Get User").
	WithCategory("calendly").
	WithIcon("user").
	WithDescription("Get user information from Calendly").
	AddSection("Connection").
		AddExpressionField("apiToken", "API Token",
			resolver.WithRequired(),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("User").
		AddTextField("userUri", "User URI",
			resolver.WithPlaceholder("https://api.calendly.com/users/XXX"),
			resolver.WithHint("Optional: Leave empty to get current authenticated user"),
		).
		EndSection().
	Build()

type CalendlyUserGetExecutor struct{}

func (e *CalendlyUserGetExecutor) Type() string { return "calendly-user-get" }

func (e *CalendlyUserGetExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg CalendlyUserGetConfig
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

	// Determine path
	path := "/users/me"
	if cfg.UserURI != "" {
		userPath := extractPathFromURI(cfg.UserURI)
		if userPath != "" {
			path = userPath
		}
	}

	resp, err := client.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}

	var userResp CalendlyUserResponse
	if err := decodeResponse(resp, &userResp); err != nil {
		return nil, err
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"user":        userResp.Resource,
			"uri":         userResp.Resource.URI,
			"email":       userResp.Resource.Email,
			"name":        userResp.Resource.Name,
			"slug":        userResp.Resource.Slug,
			"timezone":    userResp.Resource.Timezone,
			"avatarURL":   userResp.Resource.AvatarURL,
			"organization": userResp.Resource.Organization,
		},
	}, nil
}

// ============================================================================
// CALENDLY-WEBHOOK-CREATE
// ============================================================================

// CalendlyWebhookCreateConfig defines the configuration for calendly-webhook-create
type CalendlyWebhookCreateConfig struct {
	APIToken    string   `json:"apiToken" description:"Calendly API token"`
	CallbackURL string   `json:"callbackUrl" description:"URL to receive webhook notifications"`
	Events      []string `json:"events" description:"Events to subscribe to"`
	Scope       string   `json:"scope" description:"Webhook scope: organization or user"`
	UserURI     string   `json:"userUri" description:"User URI (required for user scope)"`
	Active      bool     `json:"active" default:"true" description:"Whether the webhook should be active"`
}

// CalendlyWebhookCreateSchema is the UI schema for calendly-webhook-create
var CalendlyWebhookCreateSchema = resolver.NewSchemaBuilder("calendly-webhook-create").
	WithName("Calendly Create Webhook").
	WithCategory("calendly").
	WithIcon("webhook").
	WithDescription("Create a webhook subscription in Calendly").
	AddSection("Connection").
		AddExpressionField("apiToken", "API Token",
			resolver.WithRequired(),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Webhook Configuration").
		AddTextField("callbackUrl", "Callback URL",
			resolver.WithRequired(),
			resolver.WithPlaceholder("https://your-domain.com/webhooks/calendly"),
			resolver.WithHint("URL that will receive webhook notifications"),
		).
		AddSelectField("scope", "Scope",
			[]resolver.SelectOption{
				{Label: "Organization", Value: "organization"},
				{Label: "User", Value: "user"},
			},
			resolver.WithDefault("organization"),
		).
		EndSection().
	AddSection("Events").
		AddTagsField("events", "Events to Subscribe",
			resolver.WithRequired(),
			resolver.WithHint("e.g., invitee.created, invitee.canceled, event_type.updated"),
		).
		EndSection().
	AddSection("User Scope").
		AddTextField("userUri", "User URI",
			resolver.WithPlaceholder("https://api.calendly.com/users/XXX"),
			resolver.WithHint("Required when scope is 'user'"),
			resolver.WithShowIf("scope", "user"),
		).
		EndSection().
	AddSection("Options").
		AddToggleField("active", "Active",
			resolver.WithDefault(true),
			resolver.WithHint("Whether the webhook should be active immediately"),
		).
		EndSection().
	Build()

type CalendlyWebhookCreateExecutor struct{}

func (e *CalendlyWebhookCreateExecutor) Type() string { return "calendly-webhook-create" }

func (e *CalendlyWebhookCreateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg CalendlyWebhookCreateConfig
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
	if cfg.CallbackURL == "" {
		return nil, fmt.Errorf("callbackUrl is required")
	}
	if len(cfg.Events) == 0 {
		return nil, fmt.Errorf("events are required")
	}

	client := getClient(cfg.APIToken)

	// Build request body
	requestBody := map[string]interface{}{
		"callback_url": cfg.CallbackURL,
		"events":       cfg.Events,
		"scope":        cfg.Scope,
		"state":        "active",
	}

	if !cfg.Active {
		requestBody["state"] = "paused"
	}

	if cfg.Scope == "user" {
		if cfg.UserURI == "" {
			return nil, fmt.Errorf("userUri is required when scope is 'user'")
		}
		requestBody["user"] = cfg.UserURI
	}

	resp, err := client.doRequest(ctx, "POST", "/webhook_subscriptions", requestBody)
	if err != nil {
		return nil, err
	}

	var webhookResp CalendlyWebhookSubscriptionResponse
	if err := decodeResponse(resp, &webhookResp); err != nil {
		return nil, err
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"webhook":       webhookResp.Resource,
			"uri":           webhookResp.Resource.URI,
			"callbackURL":   webhookResp.Resource.CallbackURL,
			"events":        webhookResp.Resource.Events,
			"scope":         webhookResp.Resource.Scope,
			"state":         webhookResp.Resource.State,
			"signingSecret": webhookResp.Resource.SigningSecret,
			"createdAt":     webhookResp.Resource.CreatedAt,
		},
	}, nil
}

// ============================================================================
// HELPER FUNCTIONS
// ============================================================================

// extractPathFromURI extracts the API path from a full Calendly URI
func extractPathFromURI(uri string) string {
	if uri == "" {
		return ""
	}

	// Calendly URIs are full URLs like https://api.calendly.com/scheduled_events/XXX
	// We need to extract just the path part
	if !bytes.HasPrefix([]byte(uri), []byte(CalendlyAPIBase)) {
		return ""
	}

	return uri[len(CalendlyAPIBase):]
}

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
