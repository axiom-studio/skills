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

// Trello API configuration
const (
	TrelloAPIBase = "https://api.trello.com/1"
)

// Trello client cache
var (
	clients     = make(map[string]*TrelloClient)
	clientMutex sync.RWMutex
)

// TrelloClient represents a Trello API client
type TrelloClient struct {
	APIKey  string
	Token   string
}

// ============================================================================
// TRELLO API RESPONSE TYPES
// ============================================================================

// TrelloBoard represents a Trello board
type TrelloBoard struct {
	ID             string            `json:"id"`
	Name           string            `json:"name"`
	Description    string            `json:"desc"`
	Closed         bool              `json:"closed"`
	IDOrganization string            `json:"idOrganization"`
	Pinned         bool              `json:"pinned"`
	URL            string            `json:"url"`
	Prefs          *BoardPrefs       `json:"prefs"`
	Labels         []TrelloLabel     `json:"labels"`
	Lists          []TrelloList      `json:"lists"`
	Cards          []TrelloCard      `json:"cards"`
	Members        []TrelloMember    `json:"members"`
	DateLastActivity string          `json:"dateLastActivity"`
}

// BoardPrefs represents board preferences
type BoardPrefs struct {
	PermissionLevel   string `json:"permissionLevel"`
	Voting            string `json:"voting"`
	Comments          string `json:"comments"`
	Invitations       string `json:"invitations"`
	SelfJoin          bool   `json:"selfJoin"`
	CardCovers        bool   `json:"cardCovers"`
	IsTemplate        bool   `json:"isTemplate"`
	CardAging         string `json:"cardAging"`
	CalendarFeedEnabled bool `json:"calendarFeedEnabled"`
	Background        string `json:"background"`
	BackgroundImageURL string `json:"backgroundImageURL"`
}

// TrelloList represents a Trello list
type TrelloList struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Closed  bool   `json:"closed"`
	IDBoard string `json:"idBoard"`
	Pos     float64 `json:"pos"`
}

// TrelloCard represents a Trello card
type TrelloCard struct {
	ID              string            `json:"id"`
	Name            string            `json:"name"`
	Description     string            `json:"desc"`
	IDBoard         string            `json:"idBoard"`
	IDList          string            `json:"idList"`
	IDChecklists    []string          `json:"idChecklists"`
	IDMembers       []string          `json:"idMembers"`
	IDLabels        []string          `json:"idLabels"`
	Badges          *CardBadges       `json:"badges"`
	DateLastActivity string           `json:"dateLastActivity"`
	DueDate         string            `json:"dueDate"`
	DueComplete     bool              `json:"dueComplete"`
	Pos             float64           `json:"pos"`
	Closed          bool              `json:"closed"`
	Labels          []TrelloLabel     `json:"labels"`
	Members         []TrelloMember    `json:"members"`
	Checklists      []TrelloChecklist `json:"checklists"`
	Attachments     []TrelloAttachment `json:"attachments"`
}

// CardBadges represents card badges info
type CardBadges struct {
	Attachments      int  `json:"attachments"`
	CheckItems       int  `json:"checkItems"`
	Comments         int  `json:"comments"`
	Description      bool `json:"description"`
	Due              string `json:"due"`
	DueComplete      bool `json:"dueComplete"`
	Members          int  `json:"members"`
	Stickers         int  `json:"stickers"`
	Subscribed       bool `json:"subscribed"`
	ViewingMemberVoted bool `json:"viewingMemberVoted"`
	Votes            int  `json:"votes"`
}

// TrelloLabel represents a Trello label
type TrelloLabel struct {
	ID    string `json:"id"`
	IDBoard string `json:"idBoard"`
	Name  string `json:"name"`
	Color string `json:"color"`
}

// TrelloMember represents a Trello board member
type TrelloMember struct {
	ID            string       `json:"id"`
	Username      string       `json:"username"`
	FullName      string       `json:"fullName"`
	AvatarHash    string       `json:"avatarHash"`
	AvatarURL     string       `json:"avatarUrl"`
	Initials      string       `json:"initials"`
	AccountType   string       `json:"accountType"`
	MemberType    string       `json:"memberType"`
	Confirmed     bool         `json:"confirmed"`
	Email         string       `json:"email"`
	GravatarHash  string       `json:"gravatarHash"`
	IDPremOrgsAdmin []string   `json:"idPremOrgsAdmin"`
}

// TrelloChecklist represents a Trello checklist
type TrelloChecklist struct {
	ID       string           `json:"id"`
	IDBoard  string           `json:"idBoard"`
	IDCard   string           `json:"idCard"`
	Name     string           `json:"name"`
	Pos      float64          `json:"pos"`
	CheckItems []CheckItem    `json:"checkItems"`
}

// CheckItem represents a checklist item
type CheckItem struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	State    string `json:"state"`
	Pos      float64 `json:"pos"`
}

// TrelloAttachment represents a card attachment
type TrelloAttachment struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	URL      string `json:"url"`
	PreviewURL string `json:"previewUrl"`
	MimeType string `json:"mimeType"`
}

// TrelloAction represents a Trello action/activity
type TrelloAction struct {
	ID       string      `json:"id"`
	IDMemberCreator string `json:"idMemberCreator"`
	MemberCreator *TrelloMember `json:"memberCreator"`
	Type     string      `json:"type"`
	Date     string      `json:"date"`
	Data     *ActionData `json:"data"`
}

// ActionData represents action data
type ActionData struct {
	Board    *TrelloBoard  `json:"board,omitempty"`
	Card     *TrelloCard   `json:"card,omitempty"`
	List     *TrelloList   `json:"list,omitempty"`
	Member   *TrelloMember `json:"member,omitempty"`
	Text     string        `json:"text,omitempty"`
}

// TrelloListResponse represents a paginated list response
type TrelloListResponse struct {
	Data []json.RawMessage `json:"data"`
}

// ============================================================================
// MAIN
// ============================================================================

func main() {
	// Get port from env or use default
	port := os.Getenv("SKILL_PORT")
	if port == "" {
		port = "50106"
	}

	// Create skill server
	server := grpc.NewSkillServer("skill-trello", "1.0.0")

	// Register Trello executors with schemas
	server.RegisterExecutorWithSchema("trello-board-list", &TrelloBoardListExecutor{}, TrelloBoardListSchema)
	server.RegisterExecutorWithSchema("trello-board-get", &TrelloBoardGetExecutor{}, TrelloBoardGetSchema)
	server.RegisterExecutorWithSchema("trello-card-list", &TrelloCardListExecutor{}, TrelloCardListSchema)
	server.RegisterExecutorWithSchema("trello-card-create", &TrelloCardCreateExecutor{}, TrelloCardCreateSchema)
	server.RegisterExecutorWithSchema("trello-card-update", &TrelloCardUpdateExecutor{}, TrelloCardUpdateSchema)
	server.RegisterExecutorWithSchema("trello-card-move", &TrelloCardMoveExecutor{}, TrelloCardMoveSchema)
	server.RegisterExecutorWithSchema("trello-list-list", &TrelloListListExecutor{}, TrelloListListSchema)
	server.RegisterExecutorWithSchema("trello-label-add", &TrelloLabelAddExecutor{}, TrelloLabelAddSchema)
	server.RegisterExecutorWithSchema("trello-member-add", &TrelloMemberAddExecutor{}, TrelloMemberAddSchema)

	fmt.Printf("Starting skill-trello gRPC server on port %s\n", port)
	if err := server.Serve(port); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to serve: %v\n", err)
		os.Exit(1)
	}
}

// getClient returns or creates a Trello client (cached)
func getClient(apiKey, token string) *TrelloClient {
	key := apiKey + ":" + token
	clientMutex.RLock()
	client, ok := clients[key]
	clientMutex.RUnlock()

	if ok {
		return client
	}

	clientMutex.Lock()
	defer clientMutex.Unlock()

	// Double check
	if client, ok := clients[key]; ok {
		return client
	}

	client = &TrelloClient{APIKey: apiKey, Token: token}
	clients[key] = client
	return client
}

// doRequest performs an HTTP request to the Trello API
func (c *TrelloClient) doRequest(ctx context.Context, method, path string, body interface{}) (*http.Response, error) {
	var reqBody io.Reader
	if body != nil {
		jsonData, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
		reqBody = bytes.NewBuffer(jsonData)
	}

	// Add API key and token to query parameters
	reqURL := TrelloAPIBase + path
	separator := "?"
	if bytes.Contains([]byte(path), []byte("?")) {
		separator = "&"
	}
	reqURL = reqURL + separator + "key=" + url.QueryEscape(c.APIKey) + "&token=" + url.QueryEscape(c.Token)

	req, err := http.NewRequestWithContext(ctx, method, reqURL, reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	return resp, nil
}

// decodeResponse decodes a Trello API response
func decodeResponse(resp *http.Response, result interface{}) error {
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var errResp map[string]interface{}
		if err := json.Unmarshal(body, &errResp); err == nil {
			if msg, ok := errResp["message"].(string); ok {
				return fmt.Errorf("Trello API error (%d): %s", resp.StatusCode, msg)
			}
		}
		return fmt.Errorf("Trello API error (%d): %s", resp.StatusCode, string(body))
	}

	if err := json.Unmarshal(body, result); err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
	}

	return nil
}

// decodeListResponse decodes a Trello API list response
func decodeListResponse(resp *http.Response) ([]json.RawMessage, error) {
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var errResp map[string]interface{}
		if err := json.Unmarshal(body, &errResp); err == nil {
			if msg, ok := errResp["message"].(string); ok {
				return nil, fmt.Errorf("Trello API error (%d): %s", resp.StatusCode, msg)
			}
		}
		return nil, fmt.Errorf("Trello API error (%d): %s", resp.StatusCode, string(body))
	}

	var listResp []json.RawMessage
	if err := json.Unmarshal(body, &listResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return listResp, nil
}

// ============================================================================
// TRELLO-BOARD-LIST
// ============================================================================

// TrelloBoardListConfig defines the configuration for trello-board-list
type TrelloBoardListConfig struct {
	APIKey     string `json:"apiKey" description:"Trello API Key"`
	Token      string `json:"token" description:"Trello API Token"`
	Member     string `json:"member" description:"Member ID (use 'me' for current user)"`
	Filter     string `json:"filter" description:"Filter boards: visible, open, closed, all"`
	Limit      int    `json:"limit" default:"50" description:"Maximum number of boards to return"`
}

// TrelloBoardListSchema is the UI schema for trello-board-list
var TrelloBoardListSchema = resolver.NewSchemaBuilder("trello-board-list").
	WithName("Trello List Boards").
	WithCategory("trello").
	WithIcon("columns").
	WithDescription("List Trello boards for a member").
	AddSection("Connection").
		AddExpressionField("apiKey", "API Key",
			resolver.WithRequired(),
			resolver.WithPlaceholder("Your Trello API Key"),
			resolver.WithHint("Get from https://trello.com/app-key (use {{secrets.trello_api_key}})"),
			resolver.WithSensitive(),
		).
		AddExpressionField("token", "API Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("Your Trello API Token"),
			resolver.WithHint("Trello API Token (use {{secrets.trello_token}})"),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Filters").
		AddTextField("member", "Member ID",
			resolver.WithDefault("me"),
			resolver.WithHint("Member ID or 'me' for current user"),
		).
		AddSelectField("filter", "Filter",
			[]resolver.SelectOption{
				{Label: "Visible", Value: "visible"},
				{Label: "Open", Value: "open"},
				{Label: "Closed", Value: "closed"},
				{Label: "All", Value: "all"},
			},
			resolver.WithDefault("visible"),
		).
		EndSection().
	AddSection("Options").
		AddNumberField("limit", "Limit",
			resolver.WithDefault(50),
			resolver.WithMinMax(1, 100),
		).
		EndSection().
	Build()

type TrelloBoardListExecutor struct{}

func (e *TrelloBoardListExecutor) Type() string { return "trello-board-list" }

func (e *TrelloBoardListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg TrelloBoardListConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	} else {
		return nil, fmt.Errorf("invalid resolver type")
	}

	if cfg.APIKey == "" {
		return nil, fmt.Errorf("apiKey is required")
	}
	if cfg.Token == "" {
		return nil, fmt.Errorf("token is required")
	}

	client := getClient(cfg.APIKey, cfg.Token)

	// Build path with query parameters
	path := fmt.Sprintf("/members/%s/boards?limit=%d&filter=%s",
		url.QueryEscape(cfg.Member), cfg.Limit, url.QueryEscape(cfg.Filter))

	resp, err := client.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}

	listResp, err := decodeListResponse(resp)
	if err != nil {
		return nil, err
	}

	// Parse boards from raw JSON
	boards := make([]TrelloBoard, 0, len(listResp))
	for _, raw := range listResp {
		var board TrelloBoard
		if err := json.Unmarshal(raw, &board); err != nil {
			continue
		}
		boards = append(boards, board)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"boards": boards,
			"count":  len(boards),
		},
	}, nil
}

// ============================================================================
// TRELLO-BOARD-GET
// ============================================================================

// TrelloBoardGetConfig defines the configuration for trello-board-get
type TrelloBoardGetConfig struct {
	APIKey     string `json:"apiKey" description:"Trello API Key"`
	Token      string `json:"token" description:"Trello API Token"`
	BoardID    string `json:"boardId" description:"Board ID to get"`
	Cards      bool   `json:"cards" description:"Include cards in response"`
	Lists      bool   `json:"lists" description:"Include lists in response"`
	Labels     bool   `json:"labels" description:"Include labels in response"`
	Members    bool   `json:"members" description:"Include members in response"`
}

// TrelloBoardGetSchema is the UI schema for trello-board-get
var TrelloBoardGetSchema = resolver.NewSchemaBuilder("trello-board-get").
	WithName("Trello Get Board").
	WithCategory("trello").
	WithIcon("columns").
	WithDescription("Get details of a specific Trello board").
	AddSection("Connection").
		AddExpressionField("apiKey", "API Key",
			resolver.WithRequired(),
			resolver.WithSensitive(),
		).
		AddExpressionField("token", "API Token",
			resolver.WithRequired(),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Board").
		AddTextField("boardId", "Board ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("Board ID or URL"),
		).
		EndSection().
	AddSection("Options").
		AddToggleField("cards", "Include Cards",
			resolver.WithDefault(false),
		).
		AddToggleField("lists", "Include Lists",
			resolver.WithDefault(false),
		).
		AddToggleField("labels", "Include Labels",
			resolver.WithDefault(false),
		).
		AddToggleField("members", "Include Members",
			resolver.WithDefault(false),
		).
		EndSection().
	Build()

type TrelloBoardGetExecutor struct{}

func (e *TrelloBoardGetExecutor) Type() string { return "trello-board-get" }

func (e *TrelloBoardGetExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg TrelloBoardGetConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	} else {
		return nil, fmt.Errorf("invalid resolver type")
	}

	if cfg.APIKey == "" {
		return nil, fmt.Errorf("apiKey is required")
	}
	if cfg.Token == "" {
		return nil, fmt.Errorf("token is required")
	}
	if cfg.BoardID == "" {
		return nil, fmt.Errorf("boardId is required")
	}

	client := getClient(cfg.APIKey, cfg.Token)

	// Build fields parameter
	fields := []string{"id", "name", "desc", "closed", "idOrganization", "pinned", "url", "prefs", "dateLastActivity"}
	if cfg.Cards {
		fields = append(fields, "cards")
	}
	if cfg.Lists {
		fields = append(fields, "lists")
	}
	if cfg.Labels {
		fields = append(fields, "labels")
	}
	if cfg.Members {
		fields = append(fields, "members")
	}

	path := fmt.Sprintf("/boards/%s?fields=%s",
		url.QueryEscape(cfg.BoardID),
		url.QueryEscape(joinStrings(fields, ",")))

	resp, err := client.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}

	var board TrelloBoard
	if err := decodeResponse(resp, &board); err != nil {
		return nil, err
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"board": board,
			"id":    board.ID,
			"name":  board.Name,
		},
	}, nil
}

// ============================================================================
// TRELLO-CARD-LIST
// ============================================================================

// TrelloCardListConfig defines the configuration for trello-card-list
type TrelloCardListConfig struct {
	APIKey     string `json:"apiKey" description:"Trello API Key"`
	Token      string `json:"token" description:"Trello API Token"`
	BoardID    string `json:"boardId" description:"Board ID to list cards from"`
	ListID     string `json:"listId" description:"Optional list ID to filter cards"`
	Cards      string `json:"cards" description:"Card filter: open, closed, all, visible"`
	Limit      int    `json:"limit" default:"50" description:"Maximum number of cards to return"`
	Fields     string `json:"fields" description:"Fields to return: all, id, name, desc, etc."`
}

// TrelloCardListSchema is the UI schema for trello-card-list
var TrelloCardListSchema = resolver.NewSchemaBuilder("trello-card-list").
	WithName("Trello List Cards").
	WithCategory("trello").
	WithIcon("check-square").
	WithDescription("List cards from a Trello board or list").
	AddSection("Connection").
		AddExpressionField("apiKey", "API Key",
			resolver.WithRequired(),
			resolver.WithSensitive(),
		).
		AddExpressionField("token", "API Token",
			resolver.WithRequired(),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Location").
		AddTextField("boardId", "Board ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("Board ID"),
		).
		AddTextField("listId", "List ID",
			resolver.WithPlaceholder("Optional list ID to filter"),
		).
		EndSection().
	AddSection("Options").
		AddSelectField("cards", "Card Filter",
			[]resolver.SelectOption{
				{Label: "Open", Value: "open"},
				{Label: "Closed", Value: "closed"},
				{Label: "All", Value: "all"},
				{Label: "Visible", Value: "visible"},
			},
			resolver.WithDefault("open"),
		).
		AddNumberField("limit", "Limit",
			resolver.WithDefault(50),
			resolver.WithMinMax(1, 100),
		).
		EndSection().
	Build()

type TrelloCardListExecutor struct{}

func (e *TrelloCardListExecutor) Type() string { return "trello-card-list" }

func (e *TrelloCardListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg TrelloCardListConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	} else {
		return nil, fmt.Errorf("invalid resolver type")
	}

	if cfg.APIKey == "" {
		return nil, fmt.Errorf("apiKey is required")
	}
	if cfg.Token == "" {
		return nil, fmt.Errorf("token is required")
	}
	if cfg.BoardID == "" {
		return nil, fmt.Errorf("boardId is required")
	}

	client := getClient(cfg.APIKey, cfg.Token)

	// Build path with query parameters
	path := fmt.Sprintf("/boards/%s/cards?cards=%s&limit=%d",
		url.QueryEscape(cfg.BoardID),
		url.QueryEscape(cfg.Cards),
		cfg.Limit)

	if cfg.ListID != "" {
		// Filter by list - need to get cards from specific list
		path = fmt.Sprintf("/lists/%s/cards?cards=%s&limit=%d",
			url.QueryEscape(cfg.ListID),
			url.QueryEscape(cfg.Cards),
			cfg.Limit)
	}

	resp, err := client.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}

	listResp, err := decodeListResponse(resp)
	if err != nil {
		return nil, err
	}

	// Parse cards from raw JSON
	cards := make([]TrelloCard, 0, len(listResp))
	for _, raw := range listResp {
		var card TrelloCard
		if err := json.Unmarshal(raw, &card); err != nil {
			continue
		}
		cards = append(cards, card)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"cards": cards,
			"count": len(cards),
		},
	}, nil
}

// ============================================================================
// TRELLO-CARD-CREATE
// ============================================================================

// TrelloCardCreateConfig defines the configuration for trello-card-create
type TrelloCardCreateConfig struct {
	APIKey     string   `json:"apiKey" description:"Trello API Key"`
	Token      string   `json:"token" description:"Trello API Token"`
	Name       string   `json:"name" description:"Card name"`
	Description string  `json:"description" description:"Card description"`
	ListID     string   `json:"listId" description:"List ID to create card in"`
	Position   string   `json:"position" description:"Position: top, bottom, or number"`
	Labels     []string `json:"labels" description:"Label IDs to apply"`
	Members    []string `json:"members" description:"Member IDs to assign"`
	DueDate    string   `json:"dueDate" description:"Due date (ISO 8601)"`
}

// TrelloCardCreateSchema is the UI schema for trello-card-create
var TrelloCardCreateSchema = resolver.NewSchemaBuilder("trello-card-create").
	WithName("Trello Create Card").
	WithCategory("trello").
	WithIcon("plus-circle").
	WithDescription("Create a new card in Trello").
	AddSection("Connection").
		AddExpressionField("apiKey", "API Key",
			resolver.WithRequired(),
			resolver.WithSensitive(),
		).
		AddExpressionField("token", "API Token",
			resolver.WithRequired(),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Card Details").
		AddTextField("name", "Card Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("New Card"),
		).
		AddTextareaField("description", "Description",
			resolver.WithRows(4),
			resolver.WithPlaceholder("Card description..."),
		).
		EndSection().
	AddSection("Location").
		AddTextField("listId", "List ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("List ID to create card in"),
		).
		AddSelectField("position", "Position",
			[]resolver.SelectOption{
				{Label: "Top", Value: "top"},
				{Label: "Bottom", Value: "bottom"},
			},
			resolver.WithDefault("top"),
		).
		EndSection().
	AddSection("Assignment").
		AddTextField("dueDate", "Due Date",
			resolver.WithPlaceholder("ISO 8601 datetime (e.g., 2024-12-31T23:59:59Z)"),
		).
		AddTagsField("labels", "Labels",
			resolver.WithHint("Label IDs to apply"),
		).
		AddTagsField("members", "Members",
			resolver.WithHint("Member IDs to assign"),
		).
		EndSection().
	Build()

type TrelloCardCreateExecutor struct{}

func (e *TrelloCardCreateExecutor) Type() string { return "trello-card-create" }

func (e *TrelloCardCreateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg TrelloCardCreateConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	} else {
		return nil, fmt.Errorf("invalid resolver type")
	}

	if cfg.APIKey == "" {
		return nil, fmt.Errorf("apiKey is required")
	}
	if cfg.Token == "" {
		return nil, fmt.Errorf("token is required")
	}
	if cfg.Name == "" {
		return nil, fmt.Errorf("name is required")
	}
	if cfg.ListID == "" {
		return nil, fmt.Errorf("listId is required")
	}

	client := getClient(cfg.APIKey, cfg.Token)

	// Build request body
	requestBody := map[string]interface{}{
		"name": cfg.Name,
		"idList": cfg.ListID,
	}

	if cfg.Description != "" {
		requestBody["desc"] = cfg.Description
	}
	if cfg.Position != "" {
		requestBody["pos"] = cfg.Position
	}
	if cfg.DueDate != "" {
		requestBody["due"] = cfg.DueDate
	}
	if len(cfg.Labels) > 0 {
		requestBody["idLabels"] = cfg.Labels
	}
	if len(cfg.Members) > 0 {
		requestBody["idMembers"] = cfg.Members
	}

	resp, err := client.doRequest(ctx, "POST", "/cards", requestBody)
	if err != nil {
		return nil, err
	}

	var card TrelloCard
	if err := decodeResponse(resp, &card); err != nil {
		return nil, err
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"card": card,
			"id":   card.ID,
			"name": card.Name,
		},
	}, nil
}

// ============================================================================
// TRELLO-CARD-UPDATE
// ============================================================================

// TrelloCardUpdateConfig defines the configuration for trello-card-update
type TrelloCardUpdateConfig struct {
	APIKey     string   `json:"apiKey" description:"Trello API Key"`
	Token      string   `json:"token" description:"Trello API Token"`
	CardID     string   `json:"cardId" description:"Card ID to update"`
	Name       string   `json:"name" description:"New card name"`
	Description string  `json:"description" description:"New card description"`
	Closed     *bool    `json:"closed" description:"Archive/unarchive card"`
	DueDate    string   `json:"dueDate" description:"New due date"`
	Labels     []string `json:"labels" description:"Label IDs to set"`
	Members    []string `json:"members" description:"Member IDs to assign"`
}

// TrelloCardUpdateSchema is the UI schema for trello-card-update
var TrelloCardUpdateSchema = resolver.NewSchemaBuilder("trello-card-update").
	WithName("Trello Update Card").
	WithCategory("trello").
	WithIcon("edit").
	WithDescription("Update an existing Trello card").
	AddSection("Connection").
		AddExpressionField("apiKey", "API Key",
			resolver.WithRequired(),
			resolver.WithSensitive(),
		).
		AddExpressionField("token", "API Token",
			resolver.WithRequired(),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Card").
		AddTextField("cardId", "Card ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("Card ID to update"),
		).
		EndSection().
	AddSection("Updates").
		AddTextField("name", "Card Name",
			resolver.WithPlaceholder("New name"),
		).
		AddTextareaField("description", "Description",
			resolver.WithRows(4),
			resolver.WithPlaceholder("New description..."),
		).
		AddToggleField("closed", "Archived",
			resolver.WithHint("Archive (true) or unarchive (false) the card"),
		).
		AddTextField("dueDate", "Due Date",
			resolver.WithPlaceholder("ISO 8601 datetime or empty to clear"),
		).
		EndSection().
	AddSection("Assignment").
		AddTagsField("labels", "Labels",
			resolver.WithHint("Label IDs to set (replaces existing)"),
		).
		AddTagsField("members", "Members",
			resolver.WithHint("Member IDs to assign (replaces existing)"),
		).
		EndSection().
	Build()

type TrelloCardUpdateExecutor struct{}

func (e *TrelloCardUpdateExecutor) Type() string { return "trello-card-update" }

func (e *TrelloCardUpdateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg TrelloCardUpdateConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	} else {
		return nil, fmt.Errorf("invalid resolver type")
	}

	if cfg.APIKey == "" {
		return nil, fmt.Errorf("apiKey is required")
	}
	if cfg.Token == "" {
		return nil, fmt.Errorf("token is required")
	}
	if cfg.CardID == "" {
		return nil, fmt.Errorf("cardId is required")
	}

	client := getClient(cfg.APIKey, cfg.Token)

	// Build request body
	requestBody := map[string]interface{}{}

	if cfg.Name != "" {
		requestBody["name"] = cfg.Name
	}
	if cfg.Description != "" {
		requestBody["desc"] = cfg.Description
	}
	if cfg.Closed != nil {
		requestBody["closed"] = *cfg.Closed
	}
	if cfg.DueDate != "" {
		requestBody["due"] = cfg.DueDate
	} else if cfg.DueDate == "" && step.Config["dueDate"] != nil {
		// Explicitly clear due date if empty string provided
		requestBody["due"] = nil
	}
	if len(cfg.Labels) > 0 {
		requestBody["idLabels"] = cfg.Labels
	}
	if len(cfg.Members) > 0 {
		requestBody["idMembers"] = cfg.Members
	}

	// Check if we have any updates
	if len(requestBody) == 0 {
		return nil, fmt.Errorf("no updates provided")
	}

	resp, err := client.doRequest(ctx, "PUT", "/cards/"+url.QueryEscape(cfg.CardID), requestBody)
	if err != nil {
		return nil, err
	}

	var card TrelloCard
	if err := decodeResponse(resp, &card); err != nil {
		return nil, err
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"card": card,
			"id":   card.ID,
			"name": card.Name,
		},
	}, nil
}

// ============================================================================
// TRELLO-CARD-MOVE
// ============================================================================

// TrelloCardMoveConfig defines the configuration for trello-card-move
type TrelloCardMoveConfig struct {
	APIKey     string `json:"apiKey" description:"Trello API Key"`
	Token      string `json:"token" description:"Trello API Token"`
	CardID     string `json:"cardId" description:"Card ID to move"`
	ListID     string `json:"listId" description:"List ID to move card to"`
	Position   string `json:"position" description:"Position in list: top, bottom, or number"`
}

// TrelloCardMoveSchema is the UI schema for trello-card-move
var TrelloCardMoveSchema = resolver.NewSchemaBuilder("trello-card-move").
	WithName("Trello Move Card").
	WithCategory("trello").
	WithIcon("arrow-right").
	WithDescription("Move a card to a different list or position").
	AddSection("Connection").
		AddExpressionField("apiKey", "API Key",
			resolver.WithRequired(),
			resolver.WithSensitive(),
		).
		AddExpressionField("token", "API Token",
			resolver.WithRequired(),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Card").
		AddTextField("cardId", "Card ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("Card ID to move"),
		).
		EndSection().
	AddSection("Destination").
		AddTextField("listId", "List ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("List ID to move to"),
		).
		AddSelectField("position", "Position",
			[]resolver.SelectOption{
				{Label: "Top", Value: "top"},
				{Label: "Bottom", Value: "bottom"},
			},
			resolver.WithDefault("top"),
		).
		EndSection().
	Build()

type TrelloCardMoveExecutor struct{}

func (e *TrelloCardMoveExecutor) Type() string { return "trello-card-move" }

func (e *TrelloCardMoveExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg TrelloCardMoveConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	} else {
		return nil, fmt.Errorf("invalid resolver type")
	}

	if cfg.APIKey == "" {
		return nil, fmt.Errorf("apiKey is required")
	}
	if cfg.Token == "" {
		return nil, fmt.Errorf("token is required")
	}
	if cfg.CardID == "" {
		return nil, fmt.Errorf("cardId is required")
	}
	if cfg.ListID == "" {
		return nil, fmt.Errorf("listId is required")
	}

	client := getClient(cfg.APIKey, cfg.Token)

	// Build request body
	requestBody := map[string]interface{}{
		"idList": cfg.ListID,
	}

	if cfg.Position != "" {
		requestBody["pos"] = cfg.Position
	}

	resp, err := client.doRequest(ctx, "PUT", "/cards/"+url.QueryEscape(cfg.CardID), requestBody)
	if err != nil {
		return nil, err
	}

	var card TrelloCard
	if err := decodeResponse(resp, &card); err != nil {
		return nil, err
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"card": card,
			"id":   card.ID,
			"name": card.Name,
			"idList": card.IDList,
		},
	}, nil
}

// ============================================================================
// TRELLO-LIST-LIST
// ============================================================================

// TrelloListListConfig defines the configuration for trello-list-list
type TrelloListListConfig struct {
	APIKey     string `json:"apiKey" description:"Trello API Key"`
	Token      string `json:"token" description:"Trello API Token"`
	BoardID    string `json:"boardId" description:"Board ID to list lists from"`
	Filter     string `json:"filter" description:"List filter: open, closed, all"`
	Cards      bool   `json:"cards" description:"Include cards in each list"`
}

// TrelloListListSchema is the UI schema for trello-list-list
var TrelloListListSchema = resolver.NewSchemaBuilder("trello-list-list").
	WithName("Trello List Lists").
	WithCategory("trello").
	WithIcon("list").
	WithDescription("List all lists on a Trello board").
	AddSection("Connection").
		AddExpressionField("apiKey", "API Key",
			resolver.WithRequired(),
			resolver.WithSensitive(),
		).
		AddExpressionField("token", "API Token",
			resolver.WithRequired(),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Board").
		AddTextField("boardId", "Board ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("Board ID"),
		).
		EndSection().
	AddSection("Options").
		AddSelectField("filter", "Filter",
			[]resolver.SelectOption{
				{Label: "Open", Value: "open"},
				{Label: "Closed", Value: "closed"},
				{Label: "All", Value: "all"},
			},
			resolver.WithDefault("open"),
		).
		AddToggleField("cards", "Include Cards",
			resolver.WithDefault(false),
			resolver.WithHint("Include cards in each list response"),
		).
		EndSection().
	Build()

type TrelloListListExecutor struct{}

func (e *TrelloListListExecutor) Type() string { return "trello-list-list" }

func (e *TrelloListListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg TrelloListListConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	} else {
		return nil, fmt.Errorf("invalid resolver type")
	}

	if cfg.APIKey == "" {
		return nil, fmt.Errorf("apiKey is required")
	}
	if cfg.Token == "" {
		return nil, fmt.Errorf("token is required")
	}
	if cfg.BoardID == "" {
		return nil, fmt.Errorf("boardId is required")
	}

	client := getClient(cfg.APIKey, cfg.Token)

	// Build path with query parameters
	path := fmt.Sprintf("/boards/%s/lists?filter=%s",
		url.QueryEscape(cfg.BoardID),
		url.QueryEscape(cfg.Filter))

	resp, err := client.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}

	listResp, err := decodeListResponse(resp)
	if err != nil {
		return nil, err
	}

	// Parse lists from raw JSON
	lists := make([]TrelloList, 0, len(listResp))
	for _, raw := range listResp {
		var list TrelloList
		if err := json.Unmarshal(raw, &list); err != nil {
			continue
		}
		lists = append(lists, list)
	}

	// If cards requested, fetch cards for each list
	if cfg.Cards {
		type ListWithCards struct {
			TrelloList
			Cards []TrelloCard `json:"cards"`
		}
		
		listsWithCards := make([]ListWithCards, 0, len(lists))
		for _, list := range lists {
			listWithCards := ListWithCards{TrelloList: list}
			
			cardPath := fmt.Sprintf("/lists/%s/cards?cards=open", url.QueryEscape(list.ID))
			cardResp, err := client.doRequest(ctx, "GET", cardPath, nil)
			if err != nil {
				continue
			}
			
			cardListResp, err := decodeListResponse(cardResp)
			if err != nil {
				continue
			}
			
			cards := make([]TrelloCard, 0, len(cardListResp))
			for _, raw := range cardListResp {
				var card TrelloCard
				if err := json.Unmarshal(raw, &card); err != nil {
					continue
				}
				cards = append(cards, card)
			}
			listWithCards.Cards = cards
			listsWithCards = append(listsWithCards, listWithCards)
		}
		
		return &executor.StepResult{
			Output: map[string]interface{}{
				"lists": listsWithCards,
				"count": len(listsWithCards),
			},
		}, nil
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"lists": lists,
			"count": len(lists),
		},
	}, nil
}

// ============================================================================
// TRELLO-LABEL-ADD
// ============================================================================

// TrelloLabelAddConfig defines the configuration for trello-label-add
type TrelloLabelAddConfig struct {
	APIKey     string `json:"apiKey" description:"Trello API Key"`
	Token      string `json:"token" description:"Trello API Token"`
	BoardID    string `json:"boardId" description:"Board ID to add label to"`
	Name       string `json:"name" description:"Label name"`
	Color      string `json:"color" description:"Label color: green, yellow, orange, red, purple, blue, sky, lime, pink, black"`
}

// TrelloLabelAddSchema is the UI schema for trello-label-add
var TrelloLabelAddSchema = resolver.NewSchemaBuilder("trello-label-add").
	WithName("Trello Add Label").
	WithCategory("trello").
	WithIcon("tag").
	WithDescription("Add a new label to a Trello board").
	AddSection("Connection").
		AddExpressionField("apiKey", "API Key",
			resolver.WithRequired(),
			resolver.WithSensitive(),
		).
		AddExpressionField("token", "API Token",
			resolver.WithRequired(),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Label").
		AddTextField("boardId", "Board ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("Board ID"),
		).
		AddTextField("name", "Label Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("Label name"),
		).
		AddSelectField("color", "Color",
			[]resolver.SelectOption{
				{Label: "Green", Value: "green"},
				{Label: "Yellow", Value: "yellow"},
				{Label: "Orange", Value: "orange"},
				{Label: "Red", Value: "red"},
				{Label: "Purple", Value: "purple"},
				{Label: "Blue", Value: "blue"},
				{Label: "Sky", Value: "sky"},
				{Label: "Lime", Value: "lime"},
				{Label: "Pink", Value: "pink"},
				{Label: "Black", Value: "black"},
				{Label: "None", Value: "none"},
			},
			resolver.WithDefault("green"),
		).
		EndSection().
	Build()

type TrelloLabelAddExecutor struct{}

func (e *TrelloLabelAddExecutor) Type() string { return "trello-label-add" }

func (e *TrelloLabelAddExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg TrelloLabelAddConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	} else {
		return nil, fmt.Errorf("invalid resolver type")
	}

	if cfg.APIKey == "" {
		return nil, fmt.Errorf("apiKey is required")
	}
	if cfg.Token == "" {
		return nil, fmt.Errorf("token is required")
	}
	if cfg.BoardID == "" {
		return nil, fmt.Errorf("boardId is required")
	}
	if cfg.Name == "" {
		return nil, fmt.Errorf("name is required")
	}

	client := getClient(cfg.APIKey, cfg.Token)

	// Build request body
	requestBody := map[string]interface{}{
		"idBoard": cfg.BoardID,
		"name":    cfg.Name,
	}

	if cfg.Color != "" {
		requestBody["color"] = cfg.Color
	}

	resp, err := client.doRequest(ctx, "POST", "/labels", requestBody)
	if err != nil {
		return nil, err
	}

	var label TrelloLabel
	if err := decodeResponse(resp, &label); err != nil {
		return nil, err
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"label": label,
			"id":    label.ID,
			"name":  label.Name,
		},
	}, nil
}

// ============================================================================
// TRELLO-MEMBER-ADD
// ============================================================================

// TrelloMemberAddConfig defines the configuration for trello-member-add
type TrelloMemberAddConfig struct {
	APIKey     string `json:"apiKey" description:"Trello API Key"`
	Token      string `json:"token" description:"Trello API Token"`
	BoardID    string `json:"boardId" description:"Board ID to add member to"`
	MemberID   string `json:"memberId" description:"Member ID or email to add"`
	MemberType string `json:"memberType" description:"Member type: normal, admin, observer"`
}

// TrelloMemberAddSchema is the UI schema for trello-member-add
var TrelloMemberAddSchema = resolver.NewSchemaBuilder("trello-member-add").
	WithName("Trello Add Member").
	WithCategory("trello").
	WithIcon("user-plus").
	WithDescription("Add a member to a Trello board").
	AddSection("Connection").
		AddExpressionField("apiKey", "API Key",
			resolver.WithRequired(),
			resolver.WithSensitive(),
		).
		AddExpressionField("token", "API Token",
			resolver.WithRequired(),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Board").
		AddTextField("boardId", "Board ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("Board ID"),
		).
		EndSection().
	AddSection("Member").
		AddTextField("memberId", "Member ID or Email",
			resolver.WithRequired(),
			resolver.WithPlaceholder("Member ID or email address"),
			resolver.WithHint("ID of existing Trello member or email to invite"),
		).
		AddSelectField("memberType", "Member Type",
			[]resolver.SelectOption{
				{Label: "Normal", Value: "normal"},
				{Label: "Admin", Value: "admin"},
				{Label: "Observer", Value: "observer"},
			},
			resolver.WithDefault("normal"),
		).
		EndSection().
	Build()

type TrelloMemberAddExecutor struct{}

func (e *TrelloMemberAddExecutor) Type() string { return "trello-member-add" }

func (e *TrelloMemberAddExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg TrelloMemberAddConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	} else {
		return nil, fmt.Errorf("invalid resolver type")
	}

	if cfg.APIKey == "" {
		return nil, fmt.Errorf("apiKey is required")
	}
	if cfg.Token == "" {
		return nil, fmt.Errorf("token is required")
	}
	if cfg.BoardID == "" {
		return nil, fmt.Errorf("boardId is required")
	}
	if cfg.MemberID == "" {
		return nil, fmt.Errorf("memberId is required")
	}

	client := getClient(cfg.APIKey, cfg.Token)

	// Build request body
	requestBody := map[string]interface{}{
		"type": cfg.MemberType,
	}

	if cfg.MemberType == "" {
		requestBody["type"] = "normal"
	}

	// Determine if memberId is an email or ID
	// For emails, we need to use the invite endpoint
	// For member IDs, we use the regular member endpoint
	if isEmail(cfg.MemberID) {
		// Invite by email
		requestBody["email"] = cfg.MemberID
		
		resp, err := client.doRequest(ctx, "POST", "/boards/"+url.QueryEscape(cfg.BoardID)+"/members", requestBody)
		if err != nil {
			return nil, err
		}

		var member TrelloMember
		if err := decodeResponse(resp, &member); err != nil {
			return nil, err
		}

		return &executor.StepResult{
			Output: map[string]interface{}{
				"member": member,
				"id":     member.ID,
				"username": member.Username,
			},
		}, nil
	} else {
		// Add existing member by ID
		resp, err := client.doRequest(ctx, "PUT", "/boards/"+url.QueryEscape(cfg.BoardID)+"/members/"+url.QueryEscape(cfg.MemberID), requestBody)
		if err != nil {
			return nil, err
		}

		var member TrelloMember
		if err := decodeResponse(resp, &member); err != nil {
			return nil, err
		}

		return &executor.StepResult{
			Output: map[string]interface{}{
				"member": member,
				"id":     member.ID,
				"username": member.Username,
			},
		}, nil
	}
}

// ============================================================================
// UTILITY FUNCTIONS
// ============================================================================

// joinStrings joins a slice of strings with a separator
func joinStrings(strs []string, sep string) string {
	if len(strs) == 0 {
		return ""
	}
	result := strs[0]
	for _, s := range strs[1:] {
		result += sep + s
	}
	return result
}

// isEmail checks if a string looks like an email address
func isEmail(s string) bool {
	for _, c := range s {
		if c == '@' {
			return true
		}
	}
	return false
}
