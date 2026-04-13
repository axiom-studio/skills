package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/axiom-studio/skills.sdk/executor"
	"github.com/axiom-studio/skills.sdk/grpc"
	"github.com/axiom-studio/skills.sdk/resolver"
	mgmtClient "github.com/auth0/go-auth0/v2/management/client"
	"github.com/auth0/go-auth0/v2/management"
	"github.com/auth0/go-auth0/v2/management/option"
)

const (
	iconAuth0 = "lock"
)

// Auth0Config holds Auth0 connection configuration
type Auth0Config struct {
	Domain       string
	ClientID     string
	ClientSecret string
}

// Auth0 client cache
var (
	auth0Clients = make(map[string]*mgmtClient.Management)
	clientMux    sync.RWMutex
)

func main() {
	// Get port from env or use default
	port := os.Getenv("SKILL_PORT")
	if port == "" {
		port = "50094"
	}

	// Create skill server
	server := grpc.NewSkillServer("skill-auth0", "1.0.0")

	// Register User executors with schemas
	server.RegisterExecutorWithSchema("auth0-user-list", &UserListExecutor{}, UserListSchema)
	server.RegisterExecutorWithSchema("auth0-user-create", &UserCreateExecutor{}, UserCreateSchema)
	server.RegisterExecutorWithSchema("auth0-user-update", &UserUpdateExecutor{}, UserUpdateSchema)
	server.RegisterExecutorWithSchema("auth0-user-delete", &UserDeleteExecutor{}, UserDeleteSchema)

	// Register Role executors with schemas
	server.RegisterExecutorWithSchema("auth0-role-list", &RoleListExecutor{}, RoleListSchema)
	server.RegisterExecutorWithSchema("auth0-role-assign", &RoleAssignExecutor{}, RoleAssignSchema)

	// Register Connection executor with schema
	server.RegisterExecutorWithSchema("auth0-connection-list", &ConnectionListExecutor{}, ConnectionListSchema)

	// Register Client executor with schema
	server.RegisterExecutorWithSchema("auth0-client-list", &ClientListExecutor{}, ClientListSchema)

	// Register Rule executor with schema
	server.RegisterExecutorWithSchema("auth0-rule-list", &RuleListExecutor{}, RuleListSchema)

	// Register Log executor with schema
	server.RegisterExecutorWithSchema("auth0-log-list", &LogListExecutor{}, LogListSchema)

	fmt.Printf("Starting skill-auth0 gRPC server on port %s\n", port)
	if err := server.Serve(port); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to serve: %v\n", err)
		os.Exit(1)
	}
}

// ============================================================================
// AUTH0 CLIENT HELPERS
// ============================================================================

// getAuth0Client returns an Auth0 Management API client (cached)
func getAuth0Client(cfg Auth0Config) (*mgmtClient.Management, error) {
	cacheKey := fmt.Sprintf("%s:%s:%s", cfg.Domain, cfg.ClientID, cfg.ClientSecret)

	clientMux.RLock()
	mgmt, ok := auth0Clients[cacheKey]
	clientMux.RUnlock()

	if ok && mgmt != nil {
		return mgmt, nil
	}

	clientMux.Lock()
	defer clientMux.Unlock()

	// Create new management client using client credentials
	mgmt, err := mgmtClient.New(
		cfg.Domain,
		option.WithClientCredentials(
			context.Background(),
			cfg.ClientID,
			cfg.ClientSecret,
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create Auth0 management client: %w", err)
	}

	auth0Clients[cacheKey] = mgmt
	return mgmt, nil
}

// parseAuth0Config extracts Auth0 configuration from config map
func parseAuth0Config(config map[string]interface{}) Auth0Config {
	return Auth0Config{
		Domain:       getString(config, "domain"),
		ClientID:     getString(config, "clientId"),
		ClientSecret: getString(config, "clientSecret"),
	}
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
			return strings.Split(arr, ",")
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

// Helper to convert string to pointer
func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// Helper to convert int to pointer
func intPtr(i int) *int {
	return &i
}

// Helper to convert bool to pointer
func boolPtr(b bool) *bool {
	return &b
}

// ============================================================================
// SCHEMAS
// ============================================================================

// UserListSchema is the UI schema for auth0-user-list
var UserListSchema = resolver.NewSchemaBuilder("auth0-user-list").
	WithName("List Auth0 Users").
	WithCategory("action").
	WithIcon(iconAuth0).
	WithDescription("List users from Auth0 identity platform").
	AddSection("Auth0 Connection").
		AddExpressionField("domain", "Domain",
			resolver.WithPlaceholder("myorg.auth0.com"),
			resolver.WithRequired(),
			resolver.WithHint("Your Auth0 domain (e.g., myorg.auth0.com)"),
		).
		AddExpressionField("clientId", "Client ID",
			resolver.WithPlaceholder("YOUR_CLIENT_ID"),
			resolver.WithRequired(),
			resolver.WithHint("Auth0 Management API client ID"),
		).
		AddExpressionField("clientSecret", "Client Secret",
			resolver.WithSensitive(),
			resolver.WithRequired(),
			resolver.WithHint("Auth0 Management API client secret"),
		).
		EndSection().
	AddSection("Filters").
		AddExpressionField("q", "Search Query",
			resolver.WithPlaceholder("john.doe@example.com"),
			resolver.WithHint("Search users by email, name, or other fields"),
		).
		AddExpressionField("connection", "Connection",
			resolver.WithPlaceholder("Username-Password-Authentication"),
			resolver.WithHint("Filter by connection name"),
		).
		EndSection().
	AddSection("Options").
		AddNumberField("per_page", "Per Page",
			resolver.WithDefault(50),
			resolver.WithMinMax(1, 100),
			resolver.WithHint("Number of results per page"),
		).
		AddNumberField("page", "Page",
			resolver.WithDefault(0),
			resolver.WithHint("Page number (0-indexed)"),
		).
		AddExpressionField("sort", "Sort",
			resolver.WithPlaceholder("created_at:1"),
			resolver.WithHint("Sort field and order (1=asc, -1=desc)"),
		).
		EndSection().
	Build()

// UserCreateSchema is the UI schema for auth0-user-create
var UserCreateSchema = resolver.NewSchemaBuilder("auth0-user-create").
	WithName("Create Auth0 User").
	WithCategory("action").
	WithIcon(iconAuth0).
	WithDescription("Create a new user in Auth0").
	AddSection("Auth0 Connection").
		AddExpressionField("domain", "Domain",
			resolver.WithPlaceholder("myorg.auth0.com"),
			resolver.WithRequired(),
		).
		AddExpressionField("clientId", "Client ID",
			resolver.WithPlaceholder("YOUR_CLIENT_ID"),
			resolver.WithRequired(),
		).
		AddExpressionField("clientSecret", "Client Secret",
			resolver.WithSensitive(),
			resolver.WithRequired(),
		).
		EndSection().
	AddSection("User Profile").
		AddExpressionField("connection", "Connection",
			resolver.WithRequired(),
			resolver.WithPlaceholder("Username-Password-Authentication"),
			resolver.WithHint("Connection to use for user creation"),
		).
		AddExpressionField("email", "Email",
			resolver.WithRequired(),
			resolver.WithPlaceholder("user@example.com"),
		).
		AddExpressionField("password", "Password",
			resolver.WithSensitive(),
			resolver.WithHint("Initial password (optional for some connections)"),
		).
		AddExpressionField("name", "Name",
			resolver.WithPlaceholder("John Doe"),
			resolver.WithHint("Full name of the user"),
		).
		AddExpressionField("given_name", "Given Name",
			resolver.WithPlaceholder("John"),
			resolver.WithHint("First name"),
		).
		AddExpressionField("family_name", "Family Name",
			resolver.WithPlaceholder("Doe"),
			resolver.WithHint("Last name"),
		).
		AddExpressionField("nickname", "Nickname",
			resolver.WithPlaceholder("JD"),
		).
		AddExpressionField("phone_number", "Phone Number",
			resolver.WithPlaceholder("+1-555-123-4567"),
		).
		EndSection().
	AddSection("Options").
		AddToggleField("email_verified", "Email Verified",
			resolver.WithDefault(false),
			resolver.WithHint("Mark email as verified"),
		).
		AddToggleField("phone_verified", "Phone Verified",
			resolver.WithDefault(false),
			resolver.WithHint("Mark phone as verified"),
		).
		AddJSONField("user_metadata", "User Metadata",
			resolver.WithHint("Custom user metadata (JSON)"),
		).
		AddJSONField("app_metadata", "App Metadata",
			resolver.WithHint("Custom app metadata (JSON)"),
		).
		EndSection().
	Build()

// UserUpdateSchema is the UI schema for auth0-user-update
var UserUpdateSchema = resolver.NewSchemaBuilder("auth0-user-update").
	WithName("Update Auth0 User").
	WithCategory("action").
	WithIcon(iconAuth0).
	WithDescription("Update an existing user in Auth0").
	AddSection("Auth0 Connection").
		AddExpressionField("domain", "Domain",
			resolver.WithPlaceholder("myorg.auth0.com"),
			resolver.WithRequired(),
		).
		AddExpressionField("clientId", "Client ID",
			resolver.WithPlaceholder("YOUR_CLIENT_ID"),
			resolver.WithRequired(),
		).
		AddExpressionField("clientSecret", "Client Secret",
			resolver.WithSensitive(),
			resolver.WithRequired(),
		).
		EndSection().
	AddSection("User Identification").
		AddExpressionField("user_id", "User ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("auth0|123abc456def"),
			resolver.WithHint("Auth0 user ID to update"),
		).
		EndSection().
	AddSection("User Profile").
		AddExpressionField("email", "Email",
			resolver.WithPlaceholder("user@example.com"),
		).
		AddExpressionField("password", "Password",
			resolver.WithSensitive(),
			resolver.WithHint("New password (leave empty to keep current)"),
		).
		AddExpressionField("name", "Name",
			resolver.WithPlaceholder("John Doe"),
		).
		AddExpressionField("given_name", "Given Name",
			resolver.WithPlaceholder("John"),
		).
		AddExpressionField("family_name", "Family Name",
			resolver.WithPlaceholder("Doe"),
		).
		AddExpressionField("nickname", "Nickname",
			resolver.WithPlaceholder("JD"),
		).
		AddExpressionField("phone_number", "Phone Number",
			resolver.WithPlaceholder("+1-555-123-4567"),
		).
		AddExpressionField("picture", "Picture URL",
			resolver.WithPlaceholder("https://example.com/avatar.jpg"),
		).
		EndSection().
	AddSection("Options").
		AddToggleField("email_verified", "Email Verified",
			resolver.WithDefault(false),
			resolver.WithHint("Mark email as verified"),
		).
		AddToggleField("phone_verified", "Phone Verified",
			resolver.WithDefault(false),
			resolver.WithHint("Mark phone as verified"),
		).
		AddJSONField("user_metadata", "User Metadata",
			resolver.WithHint("Custom user metadata (JSON)"),
		).
		AddJSONField("app_metadata", "App Metadata",
			resolver.WithHint("Custom app metadata (JSON)"),
		).
		AddToggleField("block", "Block User",
			resolver.WithDefault(false),
			resolver.WithHint("Block the user from logging in"),
		).
		EndSection().
	Build()

// UserDeleteSchema is the UI schema for auth0-user-delete
var UserDeleteSchema = resolver.NewSchemaBuilder("auth0-user-delete").
	WithName("Delete Auth0 User").
	WithCategory("action").
	WithIcon(iconAuth0).
	WithDescription("Delete a user from Auth0").
	AddSection("Auth0 Connection").
		AddExpressionField("domain", "Domain",
			resolver.WithPlaceholder("myorg.auth0.com"),
			resolver.WithRequired(),
		).
		AddExpressionField("clientId", "Client ID",
			resolver.WithPlaceholder("YOUR_CLIENT_ID"),
			resolver.WithRequired(),
		).
		AddExpressionField("clientSecret", "Client Secret",
			resolver.WithSensitive(),
			resolver.WithRequired(),
		).
		EndSection().
	AddSection("User Identification").
		AddExpressionField("user_id", "User ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("auth0|123abc456def"),
			resolver.WithHint("Auth0 user ID to delete"),
		).
		EndSection().
	Build()

// RoleListSchema is the UI schema for auth0-role-list
var RoleListSchema = resolver.NewSchemaBuilder("auth0-role-list").
	WithName("List Auth0 Roles").
	WithCategory("action").
	WithIcon(iconAuth0).
	WithDescription("List roles from Auth0").
	AddSection("Auth0 Connection").
		AddExpressionField("domain", "Domain",
			resolver.WithPlaceholder("myorg.auth0.com"),
			resolver.WithRequired(),
		).
		AddExpressionField("clientId", "Client ID",
			resolver.WithPlaceholder("YOUR_CLIENT_ID"),
			resolver.WithRequired(),
		).
		AddExpressionField("clientSecret", "Client Secret",
			resolver.WithSensitive(),
			resolver.WithRequired(),
		).
		EndSection().
	AddSection("Options").
		AddNumberField("per_page", "Per Page",
			resolver.WithDefault(50),
			resolver.WithMinMax(1, 100),
			resolver.WithHint("Number of results per page"),
		).
		AddNumberField("page", "Page",
			resolver.WithDefault(0),
			resolver.WithHint("Page number (0-indexed)"),
		).
		EndSection().
	Build()

// RoleAssignSchema is the UI schema for auth0-role-assign
var RoleAssignSchema = resolver.NewSchemaBuilder("auth0-role-assign").
	WithName("Assign Auth0 Role to User").
	WithCategory("action").
	WithIcon(iconAuth0).
	WithDescription("Assign a role to a user in Auth0").
	AddSection("Auth0 Connection").
		AddExpressionField("domain", "Domain",
			resolver.WithPlaceholder("myorg.auth0.com"),
			resolver.WithRequired(),
		).
		AddExpressionField("clientId", "Client ID",
			resolver.WithPlaceholder("YOUR_CLIENT_ID"),
			resolver.WithRequired(),
		).
		AddExpressionField("clientSecret", "Client Secret",
			resolver.WithSensitive(),
			resolver.WithRequired(),
		).
		EndSection().
	AddSection("Role and User").
		AddExpressionField("user_id", "User ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("auth0|123abc456def"),
			resolver.WithHint("Auth0 user ID"),
		).
		AddExpressionField("role_id", "Role ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("rol_abc123def456"),
			resolver.WithHint("Auth0 role ID to assign"),
		).
		EndSection().
	Build()

// ConnectionListSchema is the UI schema for auth0-connection-list
var ConnectionListSchema = resolver.NewSchemaBuilder("auth0-connection-list").
	WithName("List Auth0 Connections").
	WithCategory("action").
	WithIcon(iconAuth0).
	WithDescription("List connections from Auth0").
	AddSection("Auth0 Connection").
		AddExpressionField("domain", "Domain",
			resolver.WithPlaceholder("myorg.auth0.com"),
			resolver.WithRequired(),
		).
		AddExpressionField("clientId", "Client ID",
			resolver.WithPlaceholder("YOUR_CLIENT_ID"),
			resolver.WithRequired(),
		).
		AddExpressionField("clientSecret", "Client Secret",
			resolver.WithSensitive(),
			resolver.WithRequired(),
		).
		EndSection().
	AddSection("Filters").
		AddExpressionField("strategy", "Strategy",
			resolver.WithPlaceholder("auth0"),
			resolver.WithHint("Filter by connection strategy (e.g., auth0, google-oauth2, facebook)"),
		).
		EndSection().
	AddSection("Options").
		AddNumberField("per_page", "Per Page",
			resolver.WithDefault(50),
			resolver.WithMinMax(1, 100),
			resolver.WithHint("Number of results per page"),
		).
		AddNumberField("page", "Page",
			resolver.WithDefault(0),
			resolver.WithHint("Page number (0-indexed)"),
		).
		EndSection().
	Build()

// ClientListSchema is the UI schema for auth0-client-list
var ClientListSchema = resolver.NewSchemaBuilder("auth0-client-list").
	WithName("List Auth0 Applications").
	WithCategory("action").
	WithIcon(iconAuth0).
	WithDescription("List applications/clients from Auth0").
	AddSection("Auth0 Connection").
		AddExpressionField("domain", "Domain",
			resolver.WithPlaceholder("myorg.auth0.com"),
			resolver.WithRequired(),
		).
		AddExpressionField("clientId", "Client ID",
			resolver.WithPlaceholder("YOUR_CLIENT_ID"),
			resolver.WithRequired(),
		).
		AddExpressionField("clientSecret", "Client Secret",
			resolver.WithSensitive(),
			resolver.WithRequired(),
		).
		EndSection().
	AddSection("Filters").
		AddExpressionField("app_type", "App Type",
			resolver.WithPlaceholder("spa, regular_web, native, m2m"),
			resolver.WithHint("Filter by application type"),
		).
		EndSection().
	AddSection("Options").
		AddNumberField("per_page", "Per Page",
			resolver.WithDefault(50),
			resolver.WithMinMax(1, 100),
			resolver.WithHint("Number of results per page"),
		).
		AddNumberField("page", "Page",
			resolver.WithDefault(0),
			resolver.WithHint("Page number (0-indexed)"),
		).
		AddToggleField("include_fields", "Include All Fields",
			resolver.WithDefault(true),
			resolver.WithHint("Include all fields in response"),
		).
		EndSection().
	Build()

// RuleListSchema is the UI schema for auth0-rule-list
var RuleListSchema = resolver.NewSchemaBuilder("auth0-rule-list").
	WithName("List Auth0 Rules").
	WithCategory("action").
	WithIcon(iconAuth0).
	WithDescription("List rules from Auth0").
	AddSection("Auth0 Connection").
		AddExpressionField("domain", "Domain",
			resolver.WithPlaceholder("myorg.auth0.com"),
			resolver.WithRequired(),
		).
		AddExpressionField("clientId", "Client ID",
			resolver.WithPlaceholder("YOUR_CLIENT_ID"),
			resolver.WithRequired(),
		).
		AddExpressionField("clientSecret", "Client Secret",
			resolver.WithSensitive(),
			resolver.WithRequired(),
		).
		EndSection().
	AddSection("Options").
		AddNumberField("per_page", "Per Page",
			resolver.WithDefault(50),
			resolver.WithMinMax(1, 100),
			resolver.WithHint("Number of results per page"),
		).
		AddNumberField("page", "Page",
			resolver.WithDefault(0),
			resolver.WithHint("Page number (0-indexed)"),
		).
		AddToggleField("include_totals", "Include Totals",
			resolver.WithDefault(false),
			resolver.WithHint("Include totals in response"),
		).
		EndSection().
	Build()

// LogListSchema is the UI schema for auth0-log-list
var LogListSchema = resolver.NewSchemaBuilder("auth0-log-list").
	WithName("List Auth0 Logs").
	WithCategory("action").
	WithIcon(iconAuth0).
	WithDescription("List audit logs from Auth0").
	AddSection("Auth0 Connection").
		AddExpressionField("domain", "Domain",
			resolver.WithPlaceholder("myorg.auth0.com"),
			resolver.WithRequired(),
		).
		AddExpressionField("clientId", "Client ID",
			resolver.WithPlaceholder("YOUR_CLIENT_ID"),
			resolver.WithRequired(),
		).
		AddExpressionField("clientSecret", "Client Secret",
			resolver.WithSensitive(),
			resolver.WithRequired(),
		).
		EndSection().
	AddSection("Filters").
		AddExpressionField("q", "Search Query",
			resolver.WithPlaceholder("type:s OR type:fp"),
			resolver.WithHint("Query string to filter logs (e.g., type:s for success logins)"),
		).
		EndSection().
	AddSection("Options").
		AddNumberField("per_page", "Per Page",
			resolver.WithDefault(50),
			resolver.WithMinMax(1, 100),
			resolver.WithHint("Number of results per page"),
		).
		AddNumberField("page", "Page",
			resolver.WithDefault(0),
			resolver.WithHint("Page number (0-indexed)"),
		).
		AddExpressionField("sort", "Sort",
			resolver.WithPlaceholder("date:-1"),
			resolver.WithHint("Sort field and order (1=asc, -1=desc)"),
		).
		EndSection().
	Build()

// ============================================================================
// USER EXECUTORS
// ============================================================================

// UserListExecutor handles auth0-user-list node type
type UserListExecutor struct{}

func (e *UserListExecutor) Type() string { return "auth0-user-list" }

func (e *UserListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	auth0Cfg := parseAuth0Config(step.Config)

	mgmt, err := getAuth0Client(auth0Cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create Auth0 client: %w", err)
	}

	q := getString(step.Config, "q")
	connection := getString(step.Config, "connection")
	perPage := getInt(step.Config, "per_page", 50)
	page := getInt(step.Config, "page", 0)
	sort := getString(step.Config, "sort")

	// Build request parameters
	params := &management.ListUsersRequestParameters{
		Page:    management.Int(page),
		PerPage: management.Int(perPage),
	}

	if q != "" {
		params.Q = management.String(q)
	}

	if connection != "" {
		params.Connection = management.String(connection)
	}

	if sort != "" {
		params.Sort = management.String(sort)
	}

	// List users
	usersPage, err := mgmt.Users.List(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("failed to list users: %w", err)
	}

	// Convert users to output format
	var userList []map[string]interface{}
	for _, user := range usersPage.Results {
		userMap := map[string]interface{}{
			"user_id":     user.GetUserID(),
			"email":       user.GetEmail(),
			"name":        user.GetName(),
			"nickname":    user.GetNickname(),
			"created_at":  user.GetCreatedAt(),
			"updated_at":  user.GetUpdatedAt(),
			"picture":     user.GetPicture(),
		}
		if user.GetEmailVerified() {
			userMap["email_verified"] = true
		}
		if user.GetPhoneVerified() {
			userMap["phone_verified"] = true
		}
		if user.GetPhoneNumber() != "" {
			userMap["phone_number"] = user.GetPhoneNumber()
		}
		if user.GetGivenName() != "" {
			userMap["given_name"] = user.GetGivenName()
		}
		if user.GetFamilyName() != "" {
			userMap["family_name"] = user.GetFamilyName()
		}
		if user.GetUserMetadata() != nil {
			userMap["user_metadata"] = user.GetUserMetadata()
		}
		if user.GetAppMetadata() != nil {
			userMap["app_metadata"] = user.GetAppMetadata()
		}
		userList = append(userList, userMap)
	}

	output := map[string]interface{}{
		"users": userList,
		"total": len(userList),
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// UserCreateExecutor handles auth0-user-create node type
type UserCreateExecutor struct{}

func (e *UserCreateExecutor) Type() string { return "auth0-user-create" }

func (e *UserCreateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	auth0Cfg := parseAuth0Config(step.Config)

	mgmt, err := getAuth0Client(auth0Cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create Auth0 client: %w", err)
	}

	connection := getString(step.Config, "connection")
	if connection == "" {
		return nil, fmt.Errorf("connection is required")
	}

	email := getString(step.Config, "email")
	if email == "" {
		return nil, fmt.Errorf("email is required")
	}

	// Build user creation request
	createUser := &management.CreateUserRequestContent{
		Connection: connection,
	}

	// Set optional fields using setters
	createUser.SetEmail(management.String(email))

	if password := getString(step.Config, "password"); password != "" {
		createUser.SetPassword(management.String(password))
	}
	if name := getString(step.Config, "name"); name != "" {
		createUser.SetName(management.String(name))
	}
	if givenName := getString(step.Config, "given_name"); givenName != "" {
		createUser.SetGivenName(management.String(givenName))
	}
	if familyName := getString(step.Config, "family_name"); familyName != "" {
		createUser.SetFamilyName(management.String(familyName))
	}
	if nickname := getString(step.Config, "nickname"); nickname != "" {
		createUser.SetNickname(management.String(nickname))
	}
	if phoneNumber := getString(step.Config, "phone_number"); phoneNumber != "" {
		createUser.SetPhoneNumber(management.String(phoneNumber))
	}
	emailVerified := getBool(step.Config, "email_verified", false)
	if emailVerified {
		createUser.SetEmailVerified(management.Bool(emailVerified))
	}
	phoneVerified := getBool(step.Config, "phone_verified", false)
	if phoneVerified {
		createUser.SetPhoneVerified(management.Bool(phoneVerified))
	}

	// Parse user_metadata if provided
	if userMetadataJSON := getMap(step.Config, "user_metadata"); userMetadataJSON != nil {
		createUser.SetUserMetadata((*management.UserMetadata)(&userMetadataJSON))
	}

	// Parse app_metadata if provided
	if appMetadataJSON := getMap(step.Config, "app_metadata"); appMetadataJSON != nil {
		createUser.SetAppMetadata((*management.AppMetadata)(&appMetadataJSON))
	}

	// Create user
	createdUser, err := mgmt.Users.Create(ctx, createUser)
	if err != nil {
		return nil, fmt.Errorf("failed to create user: %w", err)
	}

	output := map[string]interface{}{
		"user_id":        createdUser.GetUserID(),
		"email":          createdUser.GetEmail(),
		"name":           createdUser.GetName(),
		"connection":     connection,
		"created_at":     createdUser.GetCreatedAt(),
		"email_verified": createdUser.GetEmailVerified(),
	}

	if createdUser.GetNickname() != "" {
		output["nickname"] = createdUser.GetNickname()
	}
	if createdUser.GetPhoneNumber() != "" {
		output["phone_number"] = createdUser.GetPhoneNumber()
	}
	if createdUser.GetUserMetadata() != nil {
		output["user_metadata"] = createdUser.GetUserMetadata()
	}
	if createdUser.GetAppMetadata() != nil {
		output["app_metadata"] = createdUser.GetAppMetadata()
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// UserUpdateExecutor handles auth0-user-update node type
type UserUpdateExecutor struct{}

func (e *UserUpdateExecutor) Type() string { return "auth0-user-update" }

func (e *UserUpdateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	auth0Cfg := parseAuth0Config(step.Config)

	mgmt, err := getAuth0Client(auth0Cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create Auth0 client: %w", err)
	}

	userID := getString(step.Config, "user_id")
	if userID == "" {
		return nil, fmt.Errorf("user_id is required")
	}

	// Build user update request
	updateUser := &management.UpdateUserRequestContent{}
	hasUpdate := false

	// Set fields that are provided
	if email := getString(step.Config, "email"); email != "" {
		updateUser.SetEmail(management.String(email))
		hasUpdate = true
	}
	if password := getString(step.Config, "password"); password != "" {
		updateUser.SetPassword(management.String(password))
		hasUpdate = true
	}
	if name := getString(step.Config, "name"); name != "" {
		updateUser.SetName(management.String(name))
		hasUpdate = true
	}
	if givenName := getString(step.Config, "given_name"); givenName != "" {
		updateUser.SetGivenName(management.String(givenName))
		hasUpdate = true
	}
	if familyName := getString(step.Config, "family_name"); familyName != "" {
		updateUser.SetFamilyName(management.String(familyName))
		hasUpdate = true
	}
	if nickname := getString(step.Config, "nickname"); nickname != "" {
		updateUser.SetNickname(management.String(nickname))
		hasUpdate = true
	}
	if phoneNumber := getString(step.Config, "phone_number"); phoneNumber != "" {
		updateUser.SetPhoneNumber(management.String(phoneNumber))
		hasUpdate = true
	}
	if picture := getString(step.Config, "picture"); picture != "" {
		updateUser.SetPicture(management.String(picture))
		hasUpdate = true
	}

	// Handle boolean fields
	if step.Config["email_verified"] != nil {
		updateUser.SetEmailVerified(management.Bool(getBool(step.Config, "email_verified", false)))
		hasUpdate = true
	}
	if step.Config["phone_verified"] != nil {
		updateUser.SetPhoneVerified(management.Bool(getBool(step.Config, "phone_verified", false)))
		hasUpdate = true
	}
	if step.Config["block"] != nil {
		updateUser.SetBlocked(management.Bool(getBool(step.Config, "block", false)))
		hasUpdate = true
	}

	// Parse user_metadata if provided
	if userMetadataJSON := getMap(step.Config, "user_metadata"); userMetadataJSON != nil {
		updateUser.SetUserMetadata((*management.UserMetadata)(&userMetadataJSON))
		hasUpdate = true
	}

	// Parse app_metadata if provided
	if appMetadataJSON := getMap(step.Config, "app_metadata"); appMetadataJSON != nil {
		updateUser.SetAppMetadata((*management.AppMetadata)(&appMetadataJSON))
		hasUpdate = true
	}

	if !hasUpdate {
		return nil, fmt.Errorf("at least one field to update is required")
	}

	// Update user
	updatedUser, err := mgmt.Users.Update(ctx, userID, updateUser)
	if err != nil {
		return nil, fmt.Errorf("failed to update user: %w", err)
	}

	output := map[string]interface{}{
		"user_id":        updatedUser.GetUserID(),
		"email":          updatedUser.GetEmail(),
		"name":           updatedUser.GetName(),
		"updated_at":     updatedUser.GetUpdatedAt(),
		"email_verified": updatedUser.GetEmailVerified(),
	}

	if updatedUser.GetNickname() != "" {
		output["nickname"] = updatedUser.GetNickname()
	}
	if updatedUser.GetPhoneNumber() != "" {
		output["phone_number"] = updatedUser.GetPhoneNumber()
	}
	if updatedUser.GetUserMetadata() != nil {
		output["user_metadata"] = updatedUser.GetUserMetadata()
	}
	if updatedUser.GetAppMetadata() != nil {
		output["app_metadata"] = updatedUser.GetAppMetadata()
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// UserDeleteExecutor handles auth0-user-delete node type
type UserDeleteExecutor struct{}

func (e *UserDeleteExecutor) Type() string { return "auth0-user-delete" }

func (e *UserDeleteExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	auth0Cfg := parseAuth0Config(step.Config)

	mgmt, err := getAuth0Client(auth0Cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create Auth0 client: %w", err)
	}

	userID := getString(step.Config, "user_id")
	if userID == "" {
		return nil, fmt.Errorf("user_id is required")
	}

	// Delete user
	err = mgmt.Users.Delete(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to delete user: %w", err)
	}

	output := map[string]interface{}{
		"success":    true,
		"user_id":    userID,
		"deleted_at": time.Now().UTC().Format(time.RFC3339),
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// ============================================================================
// ROLE EXECUTORS
// ============================================================================

// RoleListExecutor handles auth0-role-list node type
type RoleListExecutor struct{}

func (e *RoleListExecutor) Type() string { return "auth0-role-list" }

func (e *RoleListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	auth0Cfg := parseAuth0Config(step.Config)

	mgmt, err := getAuth0Client(auth0Cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create Auth0 client: %w", err)
	}

	perPage := getInt(step.Config, "per_page", 50)
	page := getInt(step.Config, "page", 0)

	// Build request parameters
	params := &management.ListRolesRequestParameters{
		Page:    management.Int(page),
		PerPage: management.Int(perPage),
	}

	// List roles
	rolesPage, err := mgmt.Roles.List(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("failed to list roles: %w", err)
	}

	// Convert roles to output format
	var roleList []map[string]interface{}
	for _, role := range rolesPage.Results {
		roleMap := map[string]interface{}{
			"id":          role.GetID(),
			"name":        role.GetName(),
			"description": role.GetDescription(),
		}
		roleList = append(roleList, roleMap)
	}

	output := map[string]interface{}{
		"roles": roleList,
		"total": len(roleList),
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// RoleAssignExecutor handles auth0-role-assign node type
type RoleAssignExecutor struct{}

func (e *RoleAssignExecutor) Type() string { return "auth0-role-assign" }

func (e *RoleAssignExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	auth0Cfg := parseAuth0Config(step.Config)

	mgmt, err := getAuth0Client(auth0Cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create Auth0 client: %w", err)
	}

	userID := getString(step.Config, "user_id")
	if userID == "" {
		return nil, fmt.Errorf("user_id is required")
	}

	roleID := getString(step.Config, "role_id")
	if roleID == "" {
		return nil, fmt.Errorf("role_id is required")
	}

	// Assign role to user using Users.Roles.Assign
	assignRequest := &management.AssignUserRolesRequestContent{
		Roles: []string{roleID},
	}
	err = mgmt.Users.Roles.Assign(ctx, userID, assignRequest)
	if err != nil {
		return nil, fmt.Errorf("failed to assign role: %w", err)
	}

	output := map[string]interface{}{
		"success":     true,
		"user_id":     userID,
		"role_id":     roleID,
		"assigned_at": time.Now().UTC().Format(time.RFC3339),
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// ============================================================================
// CONNECTION EXECUTORS
// ============================================================================

// ConnectionListExecutor handles auth0-connection-list node type
type ConnectionListExecutor struct{}

func (e *ConnectionListExecutor) Type() string { return "auth0-connection-list" }

func (e *ConnectionListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	auth0Cfg := parseAuth0Config(step.Config)

	mgmt, err := getAuth0Client(auth0Cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create Auth0 client: %w", err)
	}

	strategy := getString(step.Config, "strategy")
	take := getInt(step.Config, "per_page", 50)
	from := getString(step.Config, "page")

	// Build request parameters - connections use checkpoint pagination
	params := &management.ListConnectionsQueryParameters{
		Take: management.Int(take),
	}

	if from != "" && from != "0" {
		// For checkpoint pagination, we'd need the actual checkpoint ID
		// For simplicity, we just use page 0
	}

	if strategy != "" {
		// Strategy requires converting to ConnectionStrategyEnum
		// For now, we'll skip this filter as it requires enum conversion
	}

	// List connections
	connectionsPage, err := mgmt.Connections.List(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("failed to list connections: %w", err)
	}

	// Convert connections to output format
	var connectionList []map[string]interface{}
	for _, conn := range connectionsPage.Results {
		connMap := map[string]interface{}{
			"id":                   conn.GetID(),
			"name":                 conn.GetName(),
			"strategy":             conn.GetStrategy(),
			"options":              conn.GetOptions(),
			"realms":               conn.GetRealms(),
			"is_domain_connection": conn.GetIsDomainConnection(),
		}
		if conn.GetShowAsButton() {
			connMap["show_as_button"] = true
		}
		connectionList = append(connectionList, connMap)
	}

	output := map[string]interface{}{
		"connections": connectionList,
		"total":       len(connectionList),
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// ============================================================================
// CLIENT EXECUTORS
// ============================================================================

// ClientListExecutor handles auth0-client-list node type
type ClientListExecutor struct{}

func (e *ClientListExecutor) Type() string { return "auth0-client-list" }

func (e *ClientListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	auth0Cfg := parseAuth0Config(step.Config)

	mgmt, err := getAuth0Client(auth0Cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create Auth0 client: %w", err)
	}

	appType := getString(step.Config, "app_type")
	perPage := getInt(step.Config, "per_page", 50)
	page := getInt(step.Config, "page", 0)
	includeFields := getBool(step.Config, "include_fields", true)

	// Build request parameters
	params := &management.ListClientsRequestParameters{
		Page:          management.Int(page),
		PerPage:       management.Int(perPage),
		IncludeFields: management.Bool(includeFields),
	}

	if appType != "" {
		params.AppType = management.String(appType)
	}

	// List clients
	clientsPage, err := mgmt.Clients.List(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("failed to list clients: %w", err)
	}

	// Convert clients to output format
	var clientList []map[string]interface{}
	for _, c := range clientsPage.Results {
		clientMap := map[string]interface{}{
			"client_id":       c.GetClientID(),
			"name":            c.GetName(),
			"description":     c.GetDescription(),
			"app_type":        c.GetAppType(),
			"logo_uri":        c.GetLogoURI(),
			"is_first_party":  c.GetIsFirstParty(),
			"oidc_conformant": c.GetOidcConformant(),
		}
		if len(c.GetCallbacks()) > 0 {
			clientMap["callbacks"] = c.GetCallbacks()
		}
		if len(c.GetAllowedOrigins()) > 0 {
			clientMap["allowed_origins"] = c.GetAllowedOrigins()
		}
		if len(c.GetAllowedLogoutURLs()) > 0 {
			clientMap["allowed_logout_urls"] = c.GetAllowedLogoutURLs()
		}
		clientList = append(clientList, clientMap)
	}

	output := map[string]interface{}{
		"clients": clientList,
		"total":   len(clientList),
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// ============================================================================
// RULE EXECUTORS
// ============================================================================

// RuleListExecutor handles auth0-rule-list node type
type RuleListExecutor struct{}

func (e *RuleListExecutor) Type() string { return "auth0-rule-list" }

func (e *RuleListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	auth0Cfg := parseAuth0Config(step.Config)

	mgmt, err := getAuth0Client(auth0Cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create Auth0 client: %w", err)
	}

	perPage := getInt(step.Config, "per_page", 50)
	page := getInt(step.Config, "page", 0)
	includeTotals := getBool(step.Config, "include_totals", false)

	// Build request parameters
	params := &management.ListRulesRequestParameters{
		Page:          management.Int(page),
		PerPage:       management.Int(perPage),
		IncludeTotals: management.Bool(includeTotals),
	}

	// List rules
	rulesPage, err := mgmt.Rules.List(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("failed to list rules: %w", err)
	}

	// Convert rules to output format
	var ruleList []map[string]interface{}
	for _, rule := range rulesPage.Results {
		ruleMap := map[string]interface{}{
			"id":         rule.GetID(),
			"name":       rule.GetName(),
			"script":     rule.GetScript(),
			"enabled":    rule.GetEnabled(),
			"order":      rule.GetOrder(),
			"stage":      rule.GetStage(),
		}
		ruleList = append(ruleList, ruleMap)
	}

	output := map[string]interface{}{
		"rules": ruleList,
		"total": len(ruleList),
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// ============================================================================
// LOG EXECUTORS
// ============================================================================

// LogListExecutor handles auth0-log-list node type
type LogListExecutor struct{}

func (e *LogListExecutor) Type() string { return "auth0-log-list" }

func (e *LogListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	auth0Cfg := parseAuth0Config(step.Config)

	mgmt, err := getAuth0Client(auth0Cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create Auth0 client: %w", err)
	}

	q := getString(step.Config, "q")
	perPage := getInt(step.Config, "per_page", 50)
	page := getInt(step.Config, "page", 0)
	sort := getString(step.Config, "sort")

	// Build request parameters
	params := &management.ListLogsRequestParameters{
		Page:    management.Int(page),
		PerPage: management.Int(perPage),
	}

	if q != "" {
		params.Search = management.String(q)
	}

	if sort != "" {
		params.Sort = management.String(sort)
	}

	// List logs
	logsPage, err := mgmt.Logs.List(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("failed to list logs: %w", err)
	}

	// Convert logs to output format
	var logList []map[string]interface{}
	for _, log := range logsPage.Results {
		logMap := map[string]interface{}{
			"log_id":     log.GetLogID(),
			"type":       log.GetType(),
			"description": log.GetDescription(),
			"date":       log.GetDate(),
			"user_id":    log.GetUserID(),
			"client_id":  log.GetClientID(),
			"connection": log.GetConnection(),
			"ip":         log.GetIP(),
		}
		if log.GetDetails() != nil {
			logMap["details"] = log.GetDetails()
		}
		logList = append(logList, logMap)
	}

	output := map[string]interface{}{
		"logs":  logList,
		"total": len(logList),
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}
