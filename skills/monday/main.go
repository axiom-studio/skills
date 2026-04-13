package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"sync"

	"github.com/axiom-studio/skills.sdk/executor"
	"github.com/axiom-studio/skills.sdk/grpc"
	"github.com/axiom-studio/skills.sdk/resolver"
)

// Monday.com API configuration
const (
	MondayAPIURL = "https://api.monday.com/v2"
)

// Monday client cache
var (
	clients     = make(map[string]*MondayClient)
	clientMutex sync.RWMutex
)

// MondayClient represents a Monday.com API client
type MondayClient struct {
	APIToken string
	HTTP     *http.Client
}

// ============================================================================
// MONDAY.COM API RESPONSE TYPES
// ============================================================================

// MondayBoard represents a Monday.com board
type MondayBoard struct {
	ID            int                  `json:"id"`
	Name          string               `json:"name"`
	BoardType     string               `json:"board_type"`
	Position      string               `json:"position"`
	Workspace     *MondayWorkspace     `json:"workspace"`
	Owner         *MondayUser          `json:"owner"`
	Teams         []MondayTeam         `json:"teams"`
	Subscribers   []MondayUser         `json:"subscribers"`
	Columns       []MondayColumn       `json:"columns"`
	Groups        []MondayGroup        `json:"groups"`
	Items         []MondayItem         `json:"items"`
	Views         []MondayView         `json:"views"`
	Permissions   []MondayPermission   `json:"permissions"`
	Tags          []MondayTag          `json:"tags"`
	Archived      bool                 `json:"archived"`
	Favorites     bool                 `json:"favorites"`
	Icon          string               `json:"icon"`
	CreatedAt     string               `json:"created_at"`
	CurrentUsage  *MondayBoardUsage    `json:"current_usage"`
	Metadata      *MondayBoardMetadata `json:"metadata"`
	ItemsCount    int                  `json:"items_count"`
}

// MondayWorkspace represents a Monday.com workspace
type MondayWorkspace struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"`
}

// MondayUser represents a Monday.com user
type MondayUser struct {
	ID                 int         `json:"id"`
	Name               string      `json:"name"`
	Email              string      `json:"email"`
	EmailVerified      bool        `json:"email_verified"`
	Phone              string      `json:"phone"`
	PhotoURL           string      `json:"photo_url"`
	PhotoURLSmall      string      `json:"photo_url_small"`
	CreatedAt          string      `json:"created_at"`
	IsAdmin            bool        `json:"is_admin"`
	IsPending          bool        `json:"is_pending"`
	IsGuest            bool        `json:"is_guest"`
	IsOwner            bool        `json:"is_owner"`
	Birthday           string      `json:"birthday"`
	CountryCode        string      `json:"country_code"`
	TimezoneIdentifier string      `json:"timezone_identifier"`
	Language           string      `json:"language"`
	Teams              []MondayTeam `json:"teams"`
	Url                string      `json:"url"`
}

// MondayTeam represents a Monday.com team
type MondayTeam struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// MondayColumn represents a Monday.com column
type MondayColumn struct {
	ID          string      `json:"id"`
	Title       string      `json:"title"`
	Type        string      `json:"type"`
	Position    json.Number `json:"position"`
	SettingsStr string      `json:"settings_str"`
	Width       int         `json:"width"`
	Archived    bool        `json:"archived"`
}

// MondayGroup represents a Monday.com group
type MondayGroup struct {
	ID         string       `json:"id"`
	Title      string       `json:"title"`
	Color      string       `json:"color"`
	Position   json.Number  `json:"position"`
	Archived   bool         `json:"archived"`
	Deleted    bool         `json:"deleted"`
	Items      []MondayItem `json:"items"`
	ItemsCount int          `json:"items_count"`
}

// MondayItem represents a Monday.com item (task/pulse)
type MondayItem struct {
	ID            int                 `json:"id"`
	Name          string              `json:"name"`
	CreatedAt     string              `json:"created_at"`
	UpdatedAt     string              `json:"updated_at"`
	State         string              `json:"state"`
	Creator       *MondayUser         `json:"creator"`
	Owner         *MondayUser         `json:"owner"`
	Parent        *MondayItem         `json:"parent"`
	Group         *MondayGroup        `json:"group"`
	Board         *MondayBoard        `json:"board"`
	ColumnValues  []MondayColumnValue `json:"column_values"`
	Subitems      []MondayItem        `json:"subitems"`
	Subscriptions []MondaySubscription `json:"subscriptions"`
	Tags          []MondayTag         `json:"tags"`
	Logs          []MondayLog         `json:"logs"`
	Updates       []MondayUpdate      `json:"updates"`
	Files         []MondayFile        `json:"files"`
}

// MondayColumnValue represents a column value for an item
type MondayColumnValue struct {
	Title       string        `json:"title"`
	Description string        `json:"description"`
	Text        string        `json:"text"`
	Value       string        `json:"value"`
	Type        string        `json:"type"`
	State       string        `json:"state"`
	Label       string        `json:"label"`
	Column      *MondayColumn `json:"column"`
}

// MondayView represents a board view
type MondayView struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	Settings string `json:"settings"`
}

// MondayPermission represents board permissions
type MondayPermission struct {
	UserID     int    `json:"user_id"`
	Permission string `json:"permission"`
}

// MondayTag represents a tag
type MondayTag struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
}

// MondayBoardUsage represents board usage limits
type MondayBoardUsage struct {
	BoardID   int  `json:"board_id"`
	Used      int  `json:"used"`
	Total     int  `json:"total"`
	Unlimited bool `json:"unlimited"`
}

// MondayBoardMetadata represents board metadata
type MondayBoardMetadata struct {
	Identifier string `json:"identifier"`
}

// MondaySubscription represents an item subscription
type MondaySubscription struct {
	ID int `json:"id"`
}

// MondayLog represents an activity log entry
type MondayLog struct {
	ID        int    `json:"id"`
	CreatedAt string `json:"created_at"`
	Body      string `json:"body"`
}

// MondayUpdate represents an update/comment
type MondayUpdate struct {
	ID        int         `json:"id"`
	CreatedAt string      `json:"created_at"`
	Body      string      `json:"body"`
	Creator   *MondayUser `json:"creator"`
}

// MondayFile represents a file attachment
type MondayFile struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	URL       string `json:"url"`
	CreatedAt string `json:"created_at"`
}

// MondayGraphQLResponse represents a GraphQL API response
type MondayGraphQLResponse struct {
	Data   map[string]interface{} `json:"data"`
	Errors []MondayGraphQLError   `json:"errors"`
}

// MondayGraphQLError represents a GraphQL error
type MondayGraphQLError struct {
	Message   string                 `json:"message"`
	Locations []MondayErrorLocation  `json:"locations"`
	Path      []interface{}          `json:"path"`
	Extensions map[string]interface{} `json:"extensions"`
}

// MondayErrorLocation represents an error location
type MondayErrorLocation struct {
	Line   int `json:"line"`
	Column int `json:"column"`
}

// ============================================================================
// MAIN
// ============================================================================

func main() {
	// Get port from env or use default
	port := os.Getenv("SKILL_PORT")
	if port == "" {
		port = "50107"
	}

	// Create skill server
	server := grpc.NewSkillServer("skill-monday", "1.0.0")

	// Register Monday executors with schemas
	server.RegisterExecutorWithSchema("monday-board-list", &MondayBoardListExecutor{}, MondayBoardListSchema)
	server.RegisterExecutorWithSchema("monday-board-get", &MondayBoardGetExecutor{}, MondayBoardGetSchema)
	server.RegisterExecutorWithSchema("monday-item-list", &MondayItemListExecutor{}, MondayItemListSchema)
	server.RegisterExecutorWithSchema("monday-item-create", &MondayItemCreateExecutor{}, MondayItemCreateSchema)
	server.RegisterExecutorWithSchema("monday-item-update", &MondayItemUpdateExecutor{}, MondayItemUpdateSchema)
	server.RegisterExecutorWithSchema("monday-column-list", &MondayColumnListExecutor{}, MondayColumnListSchema)
	server.RegisterExecutorWithSchema("monday-group-list", &MondayGroupListExecutor{}, MondayGroupListSchema)
	server.RegisterExecutorWithSchema("monday-query", &MondayQueryExecutor{}, MondayQuerySchema)

	fmt.Printf("Starting skill-monday gRPC server on port %s\n", port)
	if err := server.Serve(port); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to serve: %v\n", err)
		os.Exit(1)
	}
}

// getClient returns or creates a Monday client (cached)
func getClient(apiToken string) *MondayClient {
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

	client = &MondayClient{
		APIToken: apiToken,
		HTTP: &http.Client{
			Timeout: 0,
		},
	}
	clients[apiToken] = client
	return client
}

// executeGraphQL executes a GraphQL query against Monday.com API
func (c *MondayClient) executeGraphQL(ctx context.Context, query string, variables map[string]interface{}) (*MondayGraphQLResponse, error) {
	body := map[string]interface{}{
		"query":     query,
		"variables": variables,
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", MondayAPIURL, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", c.APIToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Monday API error (%d): %s", resp.StatusCode, string(respBody))
	}

	var gqlResp MondayGraphQLResponse
	if err := json.Unmarshal(respBody, &gqlResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if len(gqlResp.Errors) > 0 {
		errMsg := "GraphQL errors:"
		for _, e := range gqlResp.Errors {
			errMsg += fmt.Sprintf(" %s", e.Message)
		}
		return nil, fmt.Errorf("%s", errMsg)
	}

	return &gqlResp, nil
}

// ============================================================================
// MONDAY-BOARD-LIST
// ============================================================================

// MondayBoardListConfig defines the configuration for monday-board-list
type MondayBoardListConfig struct {
	APIToken  string `json:"apiToken" description:"Monday.com API token"`
	Limit     int    `json:"limit" default:"25" description:"Maximum number of boards to return"`
	Page      int    `json:"page" default:"1" description:"Page number for pagination"`
	BoardType string `json:"boardType" description:"Filter by board type (standard, private, shareable, etc.)"`
	Archived  *bool  `json:"archived" description:"Filter by archived status"`
	Workspace string `json:"workspace" description:"Filter by workspace ID"`
}

// MondayBoardListSchema is the UI schema for monday-board-list
var MondayBoardListSchema = resolver.NewSchemaBuilder("monday-board-list").
	WithName("Monday List Boards").
	WithCategory("monday").
	WithIcon("layout").
	WithDescription("List boards from Monday.com").
	AddSection("Connection").
		AddExpressionField("apiToken", "API Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("eyJhbGciOiJIUzI1NiJ9..."),
			resolver.WithHint("Monday.com API token (use {{secrets.monday_token}})"),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Filters").
		AddTextField("boardType", "Board Type",
			resolver.WithPlaceholder("standard, private, shareable"),
			resolver.WithHint("Filter by board type"),
		).
		AddToggleField("archived", "Include Archived",
			resolver.WithDefault(false),
			resolver.WithHint("Include archived boards"),
		).
		AddTextField("workspace", "Workspace ID",
			resolver.WithPlaceholder("123456"),
			resolver.WithHint("Filter by workspace ID"),
		).
		EndSection().
	AddSection("Pagination").
		AddNumberField("limit", "Limit",
			resolver.WithDefault(25),
			resolver.WithMinMax(1, 100),
		).
		AddNumberField("page", "Page",
			resolver.WithDefault(1),
			resolver.WithMinMax(1, 1000),
		).
		EndSection().
	Build()

type MondayBoardListExecutor struct{}

func (e *MondayBoardListExecutor) Type() string { return "monday-board-list" }

func (e *MondayBoardListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg MondayBoardListConfig
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

	// Build query
	query := `
		query ($limit: Int, $page: Int, $board_type: BoardType, $archived: Boolean, $workspace_ids: [Int!]) {
			boards (
				limit: $limit,
				page: $page,
				board_type: $board_type,
				archived: $archived,
				workspace_ids: $workspace_ids
			) {
				id
				name
				board_type
				position
				archived
				favorites
				icon
				created_at
				workspace {
					id
					name
					kind
				}
				owner {
					id
					name
					email
				}
				subscribers {
					id
					name
					email
				}
				teams {
					id
					name
				}
				tags {
					id
					name
					color
				}
				columns {
					id
					title
					type
					position
				}
				groups {
					id
					title
					color
					position
				}
				items_count
			}
		}
	`

	variables := make(map[string]interface{})
	variables["limit"] = cfg.Limit
	variables["page"] = cfg.Page

	if cfg.BoardType != "" {
		variables["board_type"] = cfg.BoardType
	}
	if cfg.Archived != nil {
		variables["archived"] = *cfg.Archived
	}
	if cfg.Workspace != "" {
		workspaceID, err := strconv.Atoi(cfg.Workspace)
		if err == nil {
			variables["workspace_ids"] = []int{workspaceID}
		}
	}

	resp, err := client.executeGraphQL(ctx, query, variables)
	if err != nil {
		return nil, err
	}

	// Parse boards from response
	boardsData, ok := resp.Data["boards"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid response format: boards not found")
	}

	boards := make([]MondayBoard, 0, len(boardsData))
	for _, b := range boardsData {
		boardJSON, err := json.Marshal(b)
		if err != nil {
			continue
		}
		var board MondayBoard
		if err := json.Unmarshal(boardJSON, &board); err != nil {
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
// MONDAY-BOARD-GET
// ============================================================================

// MondayBoardGetConfig defines the configuration for monday-board-get
type MondayBoardGetConfig struct {
	APIToken string `json:"apiToken" description:"Monday.com API token"`
	BoardID  int    `json:"boardId" description:"Board ID to retrieve"`
}

// MondayBoardGetSchema is the UI schema for monday-board-get
var MondayBoardGetSchema = resolver.NewSchemaBuilder("monday-board-get").
	WithName("Monday Get Board").
	WithCategory("monday").
	WithIcon("layout").
	WithDescription("Get a specific board from Monday.com").
	AddSection("Connection").
		AddExpressionField("apiToken", "API Token",
			resolver.WithRequired(),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Board").
		AddNumberField("boardId", "Board ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("123456"),
		).
		EndSection().
	Build()

type MondayBoardGetExecutor struct{}

func (e *MondayBoardGetExecutor) Type() string { return "monday-board-get" }

func (e *MondayBoardGetExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg MondayBoardGetConfig
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
	if cfg.BoardID == 0 {
		return nil, fmt.Errorf("boardId is required")
	}

	client := getClient(cfg.APIToken)

	query := `
		query ($id: Int!) {
			boards (ids: [$id]) {
				id
				name
				board_type
				position
				archived
				favorites
				icon
				created_at
				workspace {
					id
					name
					kind
				}
				owner {
					id
					name
					email
				}
				subscribers {
					id
					name
					email
				}
				teams {
					id
					name
				}
				tags {
					id
					name
					color
				}
				columns {
					id
					title
					type
					position
					width
					settings_str
				}
				groups {
					id
					title
					color
					position
					archived
					items_count
				}
				views {
					id
					name
					type
				}
				items_count
			}
		}
	`

	variables := map[string]interface{}{
		"id": cfg.BoardID,
	}

	resp, err := client.executeGraphQL(ctx, query, variables)
	if err != nil {
		return nil, err
	}

	boardsData, ok := resp.Data["boards"].([]interface{})
	if !ok || len(boardsData) == 0 {
		return nil, fmt.Errorf("board not found")
	}

	boardJSON, _ := json.Marshal(boardsData[0])
	var board MondayBoard
	if err := json.Unmarshal(boardJSON, &board); err != nil {
		return nil, fmt.Errorf("failed to parse board: %w", err)
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
// MONDAY-ITEM-LIST
// ============================================================================

// MondayItemListConfig defines the configuration for monday-item-list
type MondayItemListConfig struct {
	APIToken     string   `json:"apiToken" description:"Monday.com API token"`
	BoardID      int      `json:"boardId" description:"Board ID to list items from"`
	GroupID      string   `json:"groupId" description:"Optional group ID to filter items"`
	Limit        int      `json:"limit" default:"25" description:"Maximum number of items to return"`
	Page         int      `json:"page" default:"1" description:"Page number for pagination"`
	ColumnIDs    []string `json:"columnIds" description:"Specific column IDs to include in column_values"`
	State        string   `json:"state" description:"Filter by state (all, active, archived, deleted)"`
	SearchTerm   string   `json:"searchTerm" description:"Search term to filter items by name"`
}

// MondayItemListSchema is the UI schema for monday-item-list
var MondayItemListSchema = resolver.NewSchemaBuilder("monday-item-list").
	WithName("Monday List Items").
	WithCategory("monday").
	WithIcon("list").
	WithDescription("List items from a Monday.com board").
	AddSection("Connection").
		AddExpressionField("apiToken", "API Token",
			resolver.WithRequired(),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Location").
		AddNumberField("boardId", "Board ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("123456"),
		).
		AddTextField("groupId", "Group ID",
			resolver.WithPlaceholder("group_1"),
			resolver.WithHint("Optional: filter by specific group"),
		).
		EndSection().
	AddSection("Filters").
		AddTextField("state", "State",
			resolver.WithPlaceholder("all, active, archived, deleted"),
			resolver.WithDefault("all"),
		).
		AddTextField("searchTerm", "Search Term",
			resolver.WithPlaceholder("Search items by name"),
		).
		EndSection().
	AddSection("Options").
		AddNumberField("limit", "Limit",
			resolver.WithDefault(25),
			resolver.WithMinMax(1, 100),
		).
		AddNumberField("page", "Page",
			resolver.WithDefault(1),
		).
		AddTagsField("columnIds", "Column IDs",
			resolver.WithHint("Specific columns to include in values"),
		).
		EndSection().
	Build()

type MondayItemListExecutor struct{}

func (e *MondayItemListExecutor) Type() string { return "monday-item-list" }

func (e *MondayItemListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg MondayItemListConfig
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
	if cfg.BoardID == 0 {
		return nil, fmt.Errorf("boardId is required")
	}

	client := getClient(cfg.APIToken)

	// Build query
	query := `
		query ($board_ids: [Int!]!, $limit: Int, $page: Int, $group_ids: [String!], $column_ids: [String!], $state: State, $search_term: String) {
			boards (ids: $board_ids) {
				id
				name
				groups (ids: $group_ids) {
					id
					title
					color
					items (limit: $limit, page: $page, state: $state, search_term: $search_term, column_ids: $column_ids) {
						id
						name
						created_at
						updated_at
						state
						creator {
							id
							name
							email
						}
						owner {
							id
							name
							email
						}
						group {
							id
							title
							color
						}
						column_values {
							id
							title
							text
							value
							type
							state
							label
							column {
								id
								title
								type
							}
						}
						subitems (state: $state) {
							id
							name
							state
						}
						tags {
							id
							name
							color
						}
						updates (limit: 5) {
							id
							body
							created_at
							creator {
								id
								name
							}
						}
					}
				}
			}
		}
	`

	variables := map[string]interface{}{
		"board_ids": []int{cfg.BoardID},
		"limit":     cfg.Limit,
		"page":      cfg.Page,
	}

	if cfg.GroupID != "" {
		variables["group_ids"] = []string{cfg.GroupID}
	}
	if len(cfg.ColumnIDs) > 0 {
		variables["column_ids"] = cfg.ColumnIDs
	}
	if cfg.State != "" {
		variables["state"] = cfg.State
	}
	if cfg.SearchTerm != "" {
		variables["search_term"] = cfg.SearchTerm
	}

	resp, err := client.executeGraphQL(ctx, query, variables)
	if err != nil {
		return nil, err
	}

	// Parse items from response
	boardsData, ok := resp.Data["boards"].([]interface{})
	if !ok || len(boardsData) == 0 {
		return nil, fmt.Errorf("board not found")
	}

	var allItems []MondayItem
	for _, b := range boardsData {
		boardMap, ok := b.(map[string]interface{})
		if !ok {
			continue
		}
		groupsData, ok := boardMap["groups"].([]interface{})
		if !ok {
			continue
		}
		for _, g := range groupsData {
			groupMap, ok := g.(map[string]interface{})
			if !ok {
				continue
			}
			itemsData, ok := groupMap["items"].([]interface{})
			if !ok {
				continue
			}
			for _, i := range itemsData {
				itemJSON, err := json.Marshal(i)
				if err != nil {
					continue
				}
				var item MondayItem
				if err := json.Unmarshal(itemJSON, &item); err != nil {
					continue
				}
				allItems = append(allItems, item)
			}
		}
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"items": allItems,
			"count": len(allItems),
		},
	}, nil
}

// ============================================================================
// MONDAY-ITEM-CREATE
// ============================================================================

// MondayItemCreateConfig defines the configuration for monday-item-create
type MondayItemCreateConfig struct {
	APIToken     string            `json:"apiToken" description:"Monday.com API token"`
	BoardID      int               `json:"boardId" description:"Board ID to create item in"`
	GroupName    string            `json:"groupName" description:"Group name (creates if not exists)"`
	GroupID      string            `json:"groupId" description:"Group ID (alternative to groupName)"`
	ItemName     string            `json:"itemName" description:"Name of the item to create"`
	ColumnValues map[string]string `json:"columnValues" description:"Column values as JSON object (column_id: value)"`
	ParentItemID int               `json:"parentItemId" description:"Parent item ID for subitems"`
	Duplicate    bool              `json:"duplicate" description:"Create as duplicate of existing item"`
}

// MondayItemCreateSchema is the UI schema for monday-item-create
var MondayItemCreateSchema = resolver.NewSchemaBuilder("monday-item-create").
	WithName("Monday Create Item").
	WithCategory("monday").
	WithIcon("plus-circle").
	WithDescription("Create a new item in a Monday.com board").
	AddSection("Connection").
		AddExpressionField("apiToken", "API Token",
			resolver.WithRequired(),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Location").
		AddNumberField("boardId", "Board ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("123456"),
		).
		AddTextField("groupName", "Group Name",
			resolver.WithPlaceholder("Main Group"),
			resolver.WithHint("Group name (creates if not exists)"),
		).
		AddTextField("groupId", "Group ID",
			resolver.WithPlaceholder("group_1"),
			resolver.WithHint("Alternative to group name"),
		).
		EndSection().
	AddSection("Item Details").
		AddTextField("itemName", "Item Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("New Item"),
		).
		AddJSONField("columnValues", "Column Values",
			resolver.WithHeight(100),
			resolver.WithHint(`JSON object: {"column_id": "value"}`),
		).
		EndSection().
	AddSection("Advanced").
		AddNumberField("parentItemId", "Parent Item ID",
			resolver.WithHint("For creating subitems"),
		).
		AddToggleField("duplicate", "Create as Duplicate",
			resolver.WithDefault(false),
			resolver.WithHint("Create as duplicate of existing item"),
		).
		EndSection().
	Build()

type MondayItemCreateExecutor struct{}

func (e *MondayItemCreateExecutor) Type() string { return "monday-item-create" }

func (e *MondayItemCreateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg MondayItemCreateConfig
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
	if cfg.BoardID == 0 {
		return nil, fmt.Errorf("boardId is required")
	}
	if cfg.ItemName == "" {
		return nil, fmt.Errorf("itemName is required")
	}

	client := getClient(cfg.APIToken)

	// Determine group identifier
	groupParam := ""
	if cfg.GroupID != "" {
		groupParam = cfg.GroupID
	} else if cfg.GroupName != "" {
		groupParam = cfg.GroupName
	}

	// Build mutation
	mutation := `
		mutation ($board_id: ID!, $group_id: String, $item_name: String!, $column_values: String, $create_duplicate: Boolean, $parent_item_id: ID) {
			create_item (
				board_id: $board_id,
				group_id: $group_id,
				item_name: $item_name,
				column_values: $column_values,
				create_duplicate: $create_duplicate,
				parent_item_id: $parent_item_id
			) {
				id
				name
				created_at
				updated_at
				state
				creator {
					id
					name
					email
				}
				owner {
					id
					name
					email
				}
				group {
					id
					title
					color
				}
				board {
					id
					name
				}
				column_values {
					id
					title
					text
					value
					type
				}
			}
		}
	`

	variables := map[string]interface{}{
		"board_id":         cfg.BoardID,
		"item_name":        cfg.ItemName,
		"create_duplicate": cfg.Duplicate,
	}

	if groupParam != "" {
		variables["group_id"] = groupParam
	}

	if len(cfg.ColumnValues) > 0 {
		columnValuesJSON, err := json.Marshal(cfg.ColumnValues)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal column_values: %w", err)
		}
		variables["column_values"] = string(columnValuesJSON)
	}

	if cfg.ParentItemID != 0 {
		variables["parent_item_id"] = cfg.ParentItemID
	}

	resp, err := client.executeGraphQL(ctx, mutation, variables)
	if err != nil {
		return nil, err
	}

	itemData, ok := resp.Data["create_item"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("failed to create item")
	}

	itemJSON, _ := json.Marshal(itemData)
	var item MondayItem
	if err := json.Unmarshal(itemJSON, &item); err != nil {
		return nil, fmt.Errorf("failed to parse item: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"item":    item,
			"id":      item.ID,
			"name":    item.Name,
			"success": true,
		},
	}, nil
}

// ============================================================================
// MONDAY-ITEM-UPDATE
// ============================================================================

// MondayItemUpdateConfig defines the configuration for monday-item-update
type MondayItemUpdateConfig struct {
	APIToken     string            `json:"apiToken" description:"Monday.com API token"`
	ItemID       int               `json:"itemId" description:"Item ID to update"`
	ItemName     string            `json:"itemName" description:"New item name"`
	ColumnValues map[string]string `json:"columnValues" description:"Column values to update as JSON object"`
	Archive      bool              `json:"archive" description:"Archive the item"`
	Delete       bool              `json:"delete" description:"Delete the item"`
	MoveToGroup  string            `json:"moveToGroup" description:"Move item to group ID"`
}

// MondayItemUpdateSchema is the UI schema for monday-item-update
var MondayItemUpdateSchema = resolver.NewSchemaBuilder("monday-item-update").
	WithName("Monday Update Item").
	WithCategory("monday").
	WithIcon("edit").
	WithDescription("Update an existing item in Monday.com").
	AddSection("Connection").
		AddExpressionField("apiToken", "API Token",
			resolver.WithRequired(),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Item").
		AddNumberField("itemId", "Item ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("1234567890"),
		).
		EndSection().
	AddSection("Updates").
		AddTextField("itemName", "Item Name",
			resolver.WithPlaceholder("New name"),
		).
		AddJSONField("columnValues", "Column Values",
			resolver.WithHeight(100),
			resolver.WithHint(`JSON object: {"column_id": "value"}`),
		).
		AddTextField("moveToGroup", "Move to Group ID",
			resolver.WithPlaceholder("group_1"),
			resolver.WithHint("Move item to different group"),
		).
		EndSection().
	AddSection("Actions").
		AddToggleField("archive", "Archive Item",
			resolver.WithDefault(false),
			resolver.WithHint("Archive this item"),
		).
		AddToggleField("delete", "Delete Item",
			resolver.WithDefault(false),
			resolver.WithHint("Delete this item permanently"),
		).
		EndSection().
	Build()

type MondayItemUpdateExecutor struct{}

func (e *MondayItemUpdateExecutor) Type() string { return "monday-item-update" }

func (e *MondayItemUpdateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg MondayItemUpdateConfig
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
	if cfg.ItemID == 0 {
		return nil, fmt.Errorf("itemId is required")
	}

	client := getClient(cfg.APIToken)

	// Handle delete
	if cfg.Delete {
		mutation := `
			mutation ($id: ID!) {
				delete_item (id: $id) {
					id
				}
			}
		`
		variables := map[string]interface{}{
			"id": cfg.ItemID,
		}

		_, err := client.executeGraphQL(ctx, mutation, variables)
		if err != nil {
			return nil, err
		}

		return &executor.StepResult{
			Output: map[string]interface{}{
				"id":      cfg.ItemID,
				"deleted": true,
				"success": true,
			},
		}, nil
	}

	// Handle archive
	if cfg.Archive {
		mutation := `
			mutation ($id: ID!) {
				archive_item (id: $id) {
					id
				}
			}
		`
		variables := map[string]interface{}{
			"id": cfg.ItemID,
		}

		_, err := client.executeGraphQL(ctx, mutation, variables)
		if err != nil {
			return nil, err
		}

		return &executor.StepResult{
			Output: map[string]interface{}{
				"id":       cfg.ItemID,
				"archived": true,
				"success":  true,
			},
		}, nil
	}

	// Build update mutation
	variables := map[string]interface{}{
		"id": cfg.ItemID,
	}

	if cfg.ItemName != "" {
		variables["name"] = cfg.ItemName
	}

	if len(cfg.ColumnValues) > 0 {
		columnValuesJSON, err := json.Marshal(cfg.ColumnValues)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal column_values: %w", err)
		}
		variables["column_values"] = string(columnValuesJSON)
	}

	if cfg.MoveToGroup != "" {
		variables["move_to_group_id"] = cfg.MoveToGroup
	}

	if len(variables) == 1 {
		return nil, fmt.Errorf("no updates specified")
	}

	// Build dynamic mutation based on provided fields
	var mutation string
	if cfg.ItemName != "" && len(cfg.ColumnValues) > 0 && cfg.MoveToGroup != "" {
		mutation = `
			mutation ($id: ID!, $name: String!, $column_values: String!, $move_to_group_id: String) {
				change_item_values (
					item_id: $id,
					should_change_name: true,
					new_name: $name,
					column_values: $column_values,
					move_to_group_id: $move_to_group_id
				) {
					id
					name
					updated_at
					column_values {
						id
						title
						text
						value
						type
					}
				}
			}
		`
	} else if cfg.ItemName != "" && len(cfg.ColumnValues) > 0 {
		mutation = `
			mutation ($id: ID!, $name: String!, $column_values: String!) {
				change_item_values (
					item_id: $id,
					should_change_name: true,
					new_name: $name,
					column_values: $column_values
				) {
					id
					name
					updated_at
					column_values {
						id
						title
						text
						value
						type
					}
				}
			}
		`
	} else if cfg.ItemName != "" && cfg.MoveToGroup != "" {
		mutation = `
			mutation ($id: ID!, $name: String!, $move_to_group_id: String) {
				change_item_values (
					item_id: $id,
					should_change_name: true,
					new_name: $name,
					move_to_group_id: $move_to_group_id
				) {
					id
					name
					updated_at
				}
			}
		`
	} else if len(cfg.ColumnValues) > 0 && cfg.MoveToGroup != "" {
		mutation = `
			mutation ($id: ID!, $column_values: String!, $move_to_group_id: String) {
				change_item_values (
					item_id: $id,
					column_values: $column_values,
					move_to_group_id: $move_to_group_id
				) {
					id
					name
					updated_at
					column_values {
						id
						title
						text
						value
						type
					}
				}
			}
		`
	} else if cfg.ItemName != "" {
		mutation = `
			mutation ($id: ID!, $name: String!) {
				change_item_values (
					item_id: $id,
					should_change_name: true,
					new_name: $name
				) {
					id
					name
					updated_at
				}
			}
		`
	} else if len(cfg.ColumnValues) > 0 {
		mutation = `
			mutation ($id: ID!, $column_values: String!) {
				change_item_values (
					item_id: $id,
					column_values: $column_values
				) {
					id
					name
					updated_at
					column_values {
						id
						title
						text
						value
						type
					}
				}
			}
		`
	} else if cfg.MoveToGroup != "" {
		mutation = `
			mutation ($id: ID!, $move_to_group_id: String) {
				change_item_values (
					item_id: $id,
					move_to_group_id: $move_to_group_id
				) {
					id
					name
					updated_at
				}
			}
		`
	}

	resp, err := client.executeGraphQL(ctx, mutation, variables)
	if err != nil {
		return nil, err
	}

	itemData, ok := resp.Data["change_item_values"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("failed to update item")
	}

	itemJSON, _ := json.Marshal(itemData)
	var item MondayItem
	if err := json.Unmarshal(itemJSON, &item); err != nil {
		return nil, fmt.Errorf("failed to parse item: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"item":    item,
			"id":      item.ID,
			"name":    item.Name,
			"success": true,
		},
	}, nil
}

// ============================================================================
// MONDAY-COLUMN-LIST
// ============================================================================

// MondayColumnListConfig defines the configuration for monday-column-list
type MondayColumnListConfig struct {
	APIToken string `json:"apiToken" description:"Monday.com API token"`
	BoardID  int    `json:"boardId" description:"Board ID to list columns from"`
}

// MondayColumnListSchema is the UI schema for monday-column-list
var MondayColumnListSchema = resolver.NewSchemaBuilder("monday-column-list").
	WithName("Monday List Columns").
	WithCategory("monday").
	WithIcon("columns").
	WithDescription("List columns from a Monday.com board").
	AddSection("Connection").
		AddExpressionField("apiToken", "API Token",
			resolver.WithRequired(),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Board").
		AddNumberField("boardId", "Board ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("123456"),
		).
		EndSection().
	Build()

type MondayColumnListExecutor struct{}

func (e *MondayColumnListExecutor) Type() string { return "monday-column-list" }

func (e *MondayColumnListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg MondayColumnListConfig
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
	if cfg.BoardID == 0 {
		return nil, fmt.Errorf("boardId is required")
	}

	client := getClient(cfg.APIToken)

	query := `
		query ($id: Int!) {
			boards (ids: [$id]) {
				id
				name
				columns {
					id
					title
					type
					position
					width
					archived
					settings_str
				}
			}
		}
	`

	variables := map[string]interface{}{
		"id": cfg.BoardID,
	}

	resp, err := client.executeGraphQL(ctx, query, variables)
	if err != nil {
		return nil, err
	}

	boardsData, ok := resp.Data["boards"].([]interface{})
	if !ok || len(boardsData) == 0 {
		return nil, fmt.Errorf("board not found")
	}

	boardMap, ok := boardsData[0].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid board data")
	}

	columnsData, ok := boardMap["columns"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("no columns found")
	}

	columns := make([]MondayColumn, 0, len(columnsData))
	for _, c := range columnsData {
		colJSON, err := json.Marshal(c)
		if err != nil {
			continue
		}
		var col MondayColumn
		if err := json.Unmarshal(colJSON, &col); err != nil {
			continue
		}
		columns = append(columns, col)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"columns":  columns,
			"count":    len(columns),
			"board_id": cfg.BoardID,
		},
	}, nil
}

// ============================================================================
// MONDAY-GROUP-LIST
// ============================================================================

// MondayGroupListConfig defines the configuration for monday-group-list
type MondayGroupListConfig struct {
	APIToken string `json:"apiToken" description:"Monday.com API token"`
	BoardID  int    `json:"boardId" description:"Board ID to list groups from"`
}

// MondayGroupListSchema is the UI schema for monday-group-list
var MondayGroupListSchema = resolver.NewSchemaBuilder("monday-group-list").
	WithName("Monday List Groups").
	WithCategory("monday").
	WithIcon("folder").
	WithDescription("List groups from a Monday.com board").
	AddSection("Connection").
		AddExpressionField("apiToken", "API Token",
			resolver.WithRequired(),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Board").
		AddNumberField("boardId", "Board ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("123456"),
		).
		EndSection().
	Build()

type MondayGroupListExecutor struct{}

func (e *MondayGroupListExecutor) Type() string { return "monday-group-list" }

func (e *MondayGroupListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg MondayGroupListConfig
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
	if cfg.BoardID == 0 {
		return nil, fmt.Errorf("boardId is required")
	}

	client := getClient(cfg.APIToken)

	query := `
		query ($id: Int!) {
			boards (ids: [$id]) {
				id
				name
				groups {
					id
					title
					color
					position
					archived
					deleted
					items_count
				}
			}
		}
	`

	variables := map[string]interface{}{
		"id": cfg.BoardID,
	}

	resp, err := client.executeGraphQL(ctx, query, variables)
	if err != nil {
		return nil, err
	}

	boardsData, ok := resp.Data["boards"].([]interface{})
	if !ok || len(boardsData) == 0 {
		return nil, fmt.Errorf("board not found")
	}

	boardMap, ok := boardsData[0].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid board data")
	}

	groupsData, ok := boardMap["groups"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("no groups found")
	}

	groups := make([]MondayGroup, 0, len(groupsData))
	for _, g := range groupsData {
		groupJSON, err := json.Marshal(g)
		if err != nil {
			continue
		}
		var group MondayGroup
		if err := json.Unmarshal(groupJSON, &group); err != nil {
			continue
		}
		groups = append(groups, group)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"groups":   groups,
			"count":    len(groups),
			"board_id": cfg.BoardID,
		},
	}, nil
}

// ============================================================================
// MONDAY-QUERY
// ============================================================================

// MondayQueryConfig defines the configuration for monday-query
type MondayQueryConfig struct {
	APIToken  string                 `json:"apiToken" description:"Monday.com API token"`
	Query     string                 `json:"query" description:"GraphQL query to execute"`
	Variables map[string]interface{} `json:"variables" description:"GraphQL variables as JSON object"`
}

// MondayQuerySchema is the UI schema for monday-query
var MondayQuerySchema = resolver.NewSchemaBuilder("monday-query").
	WithName("Monday GraphQL Query").
	WithCategory("monday").
	WithIcon("code").
	WithDescription("Execute a custom GraphQL query against Monday.com API").
	AddSection("Connection").
		AddExpressionField("apiToken", "API Token",
			resolver.WithRequired(),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Query").
		AddCodeField("query", "GraphQL Query", "graphql",
			resolver.WithRequired(),
			resolver.WithHeight(200),
			resolver.WithPlaceholder("query {\n  boards {\n    id\n    name\n  }\n}"),
		).
		EndSection().
	AddSection("Variables").
		AddJSONField("variables", "Variables",
			resolver.WithHeight(100),
			resolver.WithHint("GraphQL variables as JSON object"),
		).
		EndSection().
	Build()

type MondayQueryExecutor struct{}

func (e *MondayQueryExecutor) Type() string { return "monday-query" }

func (e *MondayQueryExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg MondayQueryConfig
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
	if cfg.Query == "" {
		return nil, fmt.Errorf("query is required")
	}

	client := getClient(cfg.APIToken)

	variables := cfg.Variables
	if variables == nil {
		variables = make(map[string]interface{})
	}

	resp, err := client.executeGraphQL(ctx, cfg.Query, variables)
	if err != nil {
		return nil, err
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"data": resp.Data,
		},
	}, nil
}
