package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/axiom-studio/skills.sdk/executor"
	"github.com/axiom-studio/skills.sdk/grpc"
	"github.com/axiom-studio/skills.sdk/resolver"
	"github.com/okta/okta-sdk-golang/v5/okta"
)

const (
	iconOkta = "lock"
)

// OktaConfig holds Okta connection configuration
type OktaConfig struct {
	OrgURL   string
	APIToken string
}

// Okta client cache
var (
	oktaClients   = make(map[string]*okta.APIClient)
	clientMux     sync.RWMutex
)

func main() {
	// Get port from env or use default
	port := os.Getenv("SKILL_PORT")
	if port == "" {
		port = "50093"
	}

	// Create skill server
	server := grpc.NewSkillServer("skill-okta", "1.0.0")

	// Register User executors with schemas
	server.RegisterExecutorWithSchema("okta-user-list", &UserListExecutor{}, UserListSchema)
	server.RegisterExecutorWithSchema("okta-user-create", &UserCreateExecutor{}, UserCreateSchema)
	server.RegisterExecutorWithSchema("okta-user-update", &UserUpdateExecutor{}, UserUpdateSchema)
	server.RegisterExecutorWithSchema("okta-user-deactivate", &UserDeactivateExecutor{}, UserDeactivateSchema)

	// Register Group executors with schemas
	server.RegisterExecutorWithSchema("okta-group-list", &GroupListExecutor{}, GroupListSchema)
	server.RegisterExecutorWithSchema("okta-group-create", &GroupCreateExecutor{}, GroupCreateSchema)
	server.RegisterExecutorWithSchema("okta-group-add-user", &GroupAddUserExecutor{}, GroupAddUserSchema)

	// Register Application executors with schemas
	server.RegisterExecutorWithSchema("okta-app-list", &AppListExecutor{}, AppListSchema)
	server.RegisterExecutorWithSchema("okta-app-assign-user", &AppAssignUserExecutor{}, AppAssignUserSchema)

	// Register MFA and Session executors with schemas
	server.RegisterExecutorWithSchema("okta-mfa-enroll", &MFAEnrollExecutor{}, MFAEnrollSchema)
	server.RegisterExecutorWithSchema("okta-session-revoke", &SessionRevokeExecutor{}, SessionRevokeSchema)

	fmt.Printf("Starting skill-okta gRPC server on port %s\n", port)
	if err := server.Serve(port); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to serve: %v\n", err)
		os.Exit(1)
	}
}

// ============================================================================
// OKTA CLIENT HELPERS
// ============================================================================

// getOktaClient returns an Okta API client (cached)
func getOktaClient(cfg OktaConfig) (*okta.APIClient, error) {
	cacheKey := fmt.Sprintf("%s:%s", cfg.OrgURL, cfg.APIToken)

	clientMux.RLock()
	client, ok := oktaClients[cacheKey]
	clientMux.RUnlock()

	if ok && client != nil {
		return client, nil
	}

	clientMux.Lock()
	defer clientMux.Unlock()

	// Create new configuration
	configuration, err := okta.NewConfiguration(
		okta.WithOrgUrl(cfg.OrgURL),
		okta.WithToken(cfg.APIToken),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create Okta configuration: %w", err)
	}

	client = okta.NewAPIClient(configuration)
	oktaClients[cacheKey] = client
	return client, nil
}

// parseOktaConfig extracts Okta configuration from config map
func parseOktaConfig(config map[string]interface{}) OktaConfig {
	return OktaConfig{
		OrgURL:   getString(config, "oktaOrgUrl"),
		APIToken: getString(config, "oktaApiToken"),
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

// Helper to get string value for NullableString setters
func getStrValue(val string) string {
	return val
}

// ============================================================================
// SCHEMAS
// ============================================================================

// UserListSchema is the UI schema for okta-user-list
var UserListSchema = resolver.NewSchemaBuilder("okta-user-list").
	WithName("List Okta Users").
	WithCategory("action").
	WithIcon(iconOkta).
	WithDescription("List users from Okta identity management").
	AddSection("Okta Connection").
		AddExpressionField("oktaOrgUrl", "Org URL",
			resolver.WithPlaceholder("https://dev-123456.okta.com"),
			resolver.WithRequired(),
			resolver.WithHint("Your Okta organization URL"),
		).
		AddExpressionField("oktaApiToken", "API Token",
			resolver.WithSensitive(),
			resolver.WithRequired(),
			resolver.WithHint("Okta API token with user read permissions"),
		).
		EndSection().
	AddSection("Filters").
		AddExpressionField("query", "Search Query",
			resolver.WithPlaceholder("john.doe"),
			resolver.WithHint("Search users by name, email, or login"),
		).
		AddExpressionField("status", "Status",
			resolver.WithPlaceholder("ACTIVE"),
			resolver.WithHint("Filter by status: ACTIVE, DEACTIVATED, STAGED, PASSWORD_EXPIRED, RECOVERY, LOCKED_OUT"),
		).
		EndSection().
	AddSection("Options").
		AddNumberField("limit", "Limit",
			resolver.WithDefault(50),
			resolver.WithMinMax(1, 200),
			resolver.WithHint("Maximum number of users to return"),
		).
		EndSection().
	Build()

// UserCreateSchema is the UI schema for okta-user-create
var UserCreateSchema = resolver.NewSchemaBuilder("okta-user-create").
	WithName("Create Okta User").
	WithCategory("action").
	WithIcon(iconOkta).
	WithDescription("Create a new user in Okta").
	AddSection("Okta Connection").
		AddExpressionField("oktaOrgUrl", "Org URL",
			resolver.WithPlaceholder("https://dev-123456.okta.com"),
			resolver.WithRequired(),
		).
		AddExpressionField("oktaApiToken", "API Token",
			resolver.WithSensitive(),
			resolver.WithRequired(),
		).
		EndSection().
	AddSection("User Profile").
		AddExpressionField("firstName", "First Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("John"),
		).
		AddExpressionField("lastName", "Last Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("Doe"),
		).
		AddExpressionField("email", "Email",
			resolver.WithRequired(),
			resolver.WithPlaceholder("john.doe@example.com"),
		).
		AddExpressionField("login", "Login",
			resolver.WithRequired(),
			resolver.WithPlaceholder("john.doe@example.com"),
			resolver.WithHint("Username for login (usually email)"),
		).
		AddExpressionField("password", "Password",
			resolver.WithSensitive(),
			resolver.WithHint("Initial password (optional for staged user)"),
		).
		EndSection().
	AddSection("Options").
		AddToggleField("activate", "Activate User",
			resolver.WithDefault(true),
			resolver.WithHint("Activate the user immediately"),
		).
		AddToggleField("provider", "Use Okta as Provider",
			resolver.WithDefault(true),
			resolver.WithHint("Set Okta as the authentication provider"),
		).
		EndSection().
	Build()

// UserUpdateSchema is the UI schema for okta-user-update
var UserUpdateSchema = resolver.NewSchemaBuilder("okta-user-update").
	WithName("Update Okta User").
	WithCategory("action").
	WithIcon(iconOkta).
	WithDescription("Update an existing user in Okta").
	AddSection("Okta Connection").
		AddExpressionField("oktaOrgUrl", "Org URL",
			resolver.WithPlaceholder("https://dev-123456.okta.com"),
			resolver.WithRequired(),
		).
		AddExpressionField("oktaApiToken", "API Token",
			resolver.WithSensitive(),
			resolver.WithRequired(),
		).
		EndSection().
	AddSection("User Identification").
		AddExpressionField("userId", "User ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("00u1234567890abcdef"),
			resolver.WithHint("Okta user ID to update"),
		).
		EndSection().
	AddSection("User Profile").
		AddExpressionField("firstName", "First Name",
			resolver.WithPlaceholder("John"),
		).
		AddExpressionField("lastName", "Last Name",
			resolver.WithPlaceholder("Doe"),
		).
		AddExpressionField("email", "Email",
			resolver.WithPlaceholder("john.doe@example.com"),
		).
		AddExpressionField("login", "Login",
			resolver.WithPlaceholder("john.doe@example.com"),
		).
		AddExpressionField("mobilePhone", "Mobile Phone",
			resolver.WithPlaceholder("+1-555-123-4567"),
		).
		EndSection().
	Build()

// UserDeactivateSchema is the UI schema for okta-user-deactivate
var UserDeactivateSchema = resolver.NewSchemaBuilder("okta-user-deactivate").
	WithName("Deactivate Okta User").
	WithCategory("action").
	WithIcon(iconOkta).
	WithDescription("Deactivate a user in Okta").
	AddSection("Okta Connection").
		AddExpressionField("oktaOrgUrl", "Org URL",
			resolver.WithPlaceholder("https://dev-123456.okta.com"),
			resolver.WithRequired(),
		).
		AddExpressionField("oktaApiToken", "API Token",
			resolver.WithSensitive(),
			resolver.WithRequired(),
		).
		EndSection().
	AddSection("User Identification").
		AddExpressionField("userId", "User ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("00u1234567890abcdef"),
			resolver.WithHint("Okta user ID to deactivate"),
		).
		EndSection().
	AddSection("Options").
		AddToggleField("sendEmail", "Send Email Notification",
			resolver.WithDefault(false),
			resolver.WithHint("Send deactivation email to the user"),
		).
		EndSection().
	Build()

// GroupListSchema is the UI schema for okta-group-list
var GroupListSchema = resolver.NewSchemaBuilder("okta-group-list").
	WithName("List Okta Groups").
	WithCategory("action").
	WithIcon(iconOkta).
	WithDescription("List groups from Okta").
	AddSection("Okta Connection").
		AddExpressionField("oktaOrgUrl", "Org URL",
			resolver.WithPlaceholder("https://dev-123456.okta.com"),
			resolver.WithRequired(),
		).
		AddExpressionField("oktaApiToken", "API Token",
			resolver.WithSensitive(),
			resolver.WithRequired(),
		).
		EndSection().
	AddSection("Filters").
		AddExpressionField("query", "Search Query",
			resolver.WithPlaceholder("Admins"),
			resolver.WithHint("Search groups by name"),
		).
		EndSection().
	AddSection("Options").
		AddNumberField("limit", "Limit",
			resolver.WithDefault(50),
			resolver.WithMinMax(1, 200),
			resolver.WithHint("Maximum number of groups to return"),
		).
		EndSection().
	Build()

// GroupCreateSchema is the UI schema for okta-group-create
var GroupCreateSchema = resolver.NewSchemaBuilder("okta-group-create").
	WithName("Create Okta Group").
	WithCategory("action").
	WithIcon(iconOkta).
	WithDescription("Create a new group in Okta").
	AddSection("Okta Connection").
		AddExpressionField("oktaOrgUrl", "Org URL",
			resolver.WithPlaceholder("https://dev-123456.okta.com"),
			resolver.WithRequired(),
		).
		AddExpressionField("oktaApiToken", "API Token",
			resolver.WithSensitive(),
			resolver.WithRequired(),
		).
		EndSection().
	AddSection("Group Profile").
		AddExpressionField("name", "Group Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("Engineering Team"),
		).
		AddExpressionField("description", "Description",
			resolver.WithPlaceholder("Engineering department members"),
		).
		EndSection().
	Build()

// GroupAddUserSchema is the UI schema for okta-group-add-user
var GroupAddUserSchema = resolver.NewSchemaBuilder("okta-group-add-user").
	WithName("Add User to Okta Group").
	WithCategory("action").
	WithIcon(iconOkta).
	WithDescription("Add a user to an Okta group").
	AddSection("Okta Connection").
		AddExpressionField("oktaOrgUrl", "Org URL",
			resolver.WithPlaceholder("https://dev-123456.okta.com"),
			resolver.WithRequired(),
		).
		AddExpressionField("oktaApiToken", "API Token",
			resolver.WithSensitive(),
			resolver.WithRequired(),
		).
		EndSection().
	AddSection("Group and User").
		AddExpressionField("groupId", "Group ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("00g1234567890abcdef"),
			resolver.WithHint("Okta group ID"),
		).
		AddExpressionField("userId", "User ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("00u1234567890abcdef"),
			resolver.WithHint("Okta user ID to add to group"),
		).
		EndSection().
	Build()

// AppListSchema is the UI schema for okta-app-list
var AppListSchema = resolver.NewSchemaBuilder("okta-app-list").
	WithName("List Okta Applications").
	WithCategory("action").
	WithIcon(iconOkta).
	WithDescription("List applications from Okta").
	AddSection("Okta Connection").
		AddExpressionField("oktaOrgUrl", "Org URL",
			resolver.WithPlaceholder("https://dev-123456.okta.com"),
			resolver.WithRequired(),
		).
		AddExpressionField("oktaApiToken", "API Token",
			resolver.WithSensitive(),
			resolver.WithRequired(),
		).
		EndSection().
	AddSection("Filters").
		AddExpressionField("query", "Search Query",
			resolver.WithPlaceholder("Salesforce"),
			resolver.WithHint("Search applications by name or label"),
		).
		AddExpressionField("status", "Status",
			resolver.WithPlaceholder("ACTIVE"),
			resolver.WithHint("Filter by status: ACTIVE, INACTIVE, DEPRECATED"),
		).
		EndSection().
	AddSection("Options").
		AddNumberField("limit", "Limit",
			resolver.WithDefault(50),
			resolver.WithMinMax(1, 200),
			resolver.WithHint("Maximum number of applications to return"),
		).
		EndSection().
	Build()

// AppAssignUserSchema is the UI schema for okta-app-assign-user
var AppAssignUserSchema = resolver.NewSchemaBuilder("okta-app-assign-user").
	WithName("Assign User to Okta Application").
	WithCategory("action").
	WithIcon(iconOkta).
	WithDescription("Assign a user to an Okta application").
	AddSection("Okta Connection").
		AddExpressionField("oktaOrgUrl", "Org URL",
			resolver.WithPlaceholder("https://dev-123456.okta.com"),
			resolver.WithRequired(),
		).
		AddExpressionField("oktaApiToken", "API Token",
			resolver.WithSensitive(),
			resolver.WithRequired(),
		).
		EndSection().
	AddSection("Application and User").
		AddExpressionField("appId", "Application ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("0oa1234567890abcdef"),
			resolver.WithHint("Okta application ID"),
		).
		AddExpressionField("userId", "User ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("00u1234567890abcdef"),
			resolver.WithHint("Okta user ID to assign"),
		).
		EndSection().
	AddSection("Profile").
		AddJSONField("profile", "Application Profile",
			resolver.WithHint("JSON profile for the application assignment (optional)"),
		).
		EndSection().
	Build()

// MFAEnrollSchema is the UI schema for okta-mfa-enroll
var MFAEnrollSchema = resolver.NewSchemaBuilder("okta-mfa-enroll").
	WithName("Enroll User in Okta MFA").
	WithCategory("action").
	WithIcon(iconOkta).
	WithDescription("Enroll a user in multi-factor authentication").
	AddSection("Okta Connection").
		AddExpressionField("oktaOrgUrl", "Org URL",
			resolver.WithPlaceholder("https://dev-123456.okta.com"),
			resolver.WithRequired(),
		).
		AddExpressionField("oktaApiToken", "API Token",
			resolver.WithSensitive(),
			resolver.WithRequired(),
		).
		EndSection().
	AddSection("User and Factor").
		AddExpressionField("userId", "User ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("00u1234567890abcdef"),
			resolver.WithHint("Okta user ID to enroll"),
		).
		AddSelectField("factorType", "Factor Type",
			[]resolver.SelectOption{
				{Label: "SMS", Value: "sms"},
				{Label: "Email", Value: "email"},
				{Label: "TOTP (Authenticator)", Value: "token:software:totp"},
				{Label: "Push", Value: "push"},
				{Label: "WebAuthn", Value: "webauthn"},
			},
			resolver.WithRequired(),
			resolver.WithDefault("sms"),
			resolver.WithHint("Type of MFA factor to enroll"),
		).
		EndSection().
	AddSection("Factor Profile").
		AddExpressionField("phoneNumber", "Phone Number",
			resolver.WithPlaceholder("+1-555-123-4567"),
			resolver.WithHint("Phone number for SMS factor"),
		).
		AddExpressionField("email", "Email",
			resolver.WithPlaceholder("user@example.com"),
			resolver.WithHint("Email for email factor"),
		).
		EndSection().
	Build()

// SessionRevokeSchema is the UI schema for okta-session-revoke
var SessionRevokeSchema = resolver.NewSchemaBuilder("okta-session-revoke").
	WithName("Revoke Okta Session").
	WithCategory("action").
	WithIcon(iconOkta).
	WithDescription("Revoke a user's Okta session").
	AddSection("Okta Connection").
		AddExpressionField("oktaOrgUrl", "Org URL",
			resolver.WithPlaceholder("https://dev-123456.okta.com"),
			resolver.WithRequired(),
		).
		AddExpressionField("oktaApiToken", "API Token",
			resolver.WithSensitive(),
			resolver.WithRequired(),
		).
		EndSection().
	AddSection("Session").
		AddExpressionField("sessionId", "Session ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("s1234567890abcdef"),
			resolver.WithHint("Okta session ID to revoke"),
		).
		EndSection().
	Build()

// ============================================================================
// USER EXECUTORS
// ============================================================================

// UserListExecutor handles okta-user-list node type
type UserListExecutor struct{}

func (e *UserListExecutor) Type() string { return "okta-user-list" }

func (e *UserListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	oktaCfg := parseOktaConfig(step.Config)

	client, err := getOktaClient(oktaCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create Okta client: %w", err)
	}

	query := getString(step.Config, "query")
	status := getString(step.Config, "status")
	limit := getInt(step.Config, "limit", 50)

	// Build request
	req := client.UserAPI.ListUsers(ctx)

	// Add query parameters
	if query != "" {
		req = req.Q(query)
	}
	if status != "" {
		req = req.Filter(fmt.Sprintf("status eq \"%s\"", status))
	}
	req = req.Limit(int32(limit))

	users, resp, err := req.Execute()
	if err != nil {
		return nil, fmt.Errorf("failed to list users: %w", err)
	}

	// Convert users to output format
	var userList []map[string]interface{}
	for _, user := range users {
		profile := user.GetProfile()
		userMap := map[string]interface{}{
			"id":        user.GetId(),
			"login":     profile.GetLogin(),
			"email":     profile.GetEmail(),
			"firstName": profile.GetFirstName(),
			"lastName":  profile.GetLastName(),
			"status":    user.GetStatus(),
			"created":   user.GetCreated(),
			"activated": user.GetActivated(),
		}
		if profile.MobilePhone.IsSet() {
			userMap["mobilePhone"] = profile.GetMobilePhone()
		}
		userList = append(userList, userMap)
	}

	output := map[string]interface{}{
		"users":      userList,
		"count":      len(userList),
		"responseId": resp.Header.Get("x-okta-request-id"),
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// UserCreateExecutor handles okta-user-create node type
type UserCreateExecutor struct{}

func (e *UserCreateExecutor) Type() string { return "okta-user-create" }

func (e *UserCreateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	oktaCfg := parseOktaConfig(step.Config)

	client, err := getOktaClient(oktaCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create Okta client: %w", err)
	}

	// Create user profile
	profile := okta.NewUserProfile()
	profile.SetFirstName(getStrValue(getString(step.Config, "firstName")))
	profile.SetLastName(getStrValue(getString(step.Config, "lastName")))
	profile.SetEmail(getStrValue(getString(step.Config, "email")))
	profile.SetLogin(getStrValue(getString(step.Config, "login")))

	// Create user request
	createUser := okta.NewCreateUserRequest(*profile)

	// Set credentials if password provided
	password := getString(step.Config, "password")
	if password != "" {
		credentials := okta.NewUserCredentials()
		passwordCred := okta.NewPasswordCredential()
		passwordCred.SetValue(password)
		credentials.SetPassword(*passwordCred)

		// Set provider
		if getBool(step.Config, "provider", true) {
			provider := okta.NewAuthenticationProvider()
			provider.SetType("OKTA")
			credentials.SetProvider(*provider)
		}

		createUser.SetCredentials(*credentials)
	}

	// Set activation option
	activate := getBool(step.Config, "activate", true)

	req := client.UserAPI.CreateUser(ctx).Body(*createUser)
	if !activate {
		req = req.Activate(false)
	}

	user, resp, err := req.Execute()
	if err != nil {
		return nil, fmt.Errorf("failed to create user: %w", err)
	}

	createdProfile := user.GetProfile()
	output := map[string]interface{}{
		"id":         user.GetId(),
		"login":      createdProfile.GetLogin(),
		"email":      createdProfile.GetEmail(),
		"firstName":  createdProfile.GetFirstName(),
		"lastName":   createdProfile.GetLastName(),
		"status":     user.GetStatus(),
		"created":    user.GetCreated(),
		"activated":  user.GetActivated(),
		"responseId": resp.Header.Get("x-okta-request-id"),
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// UserUpdateExecutor handles okta-user-update node type
type UserUpdateExecutor struct{}

func (e *UserUpdateExecutor) Type() string { return "okta-user-update" }

func (e *UserUpdateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	oktaCfg := parseOktaConfig(step.Config)

	client, err := getOktaClient(oktaCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create Okta client: %w", err)
	}

	userId := getString(step.Config, "userId")
	if userId == "" {
		return nil, fmt.Errorf("userId is required")
	}

	// Get existing user first to preserve existing profile data
	existingUser, _, err := client.UserAPI.GetUser(ctx, userId).Execute()
	if err != nil {
		return nil, fmt.Errorf("failed to get existing user: %w", err)
	}

	// Build profile updates
	existingProfile := existingUser.GetProfile()

	if firstName := getString(step.Config, "firstName"); firstName != "" {
		existingProfile.SetFirstName(firstName)
	}
	if lastName := getString(step.Config, "lastName"); lastName != "" {
		existingProfile.SetLastName(lastName)
	}
	if email := getString(step.Config, "email"); email != "" {
		existingProfile.SetEmail(email)
	}
	if login := getString(step.Config, "login"); login != "" {
		existingProfile.SetLogin(login)
	}
	if mobilePhone := getString(step.Config, "mobilePhone"); mobilePhone != "" {
		existingProfile.SetMobilePhone(mobilePhone)
	}

	// Create update request
	updateRequest := okta.NewUpdateUserRequest()
	updateRequest.SetProfile(existingProfile)

	user, resp, err := client.UserAPI.UpdateUser(ctx, userId).User(*updateRequest).Execute()
	if err != nil {
		return nil, fmt.Errorf("failed to update user: %w", err)
	}

	updatedProfile := user.GetProfile()
	output := map[string]interface{}{
		"id":         user.GetId(),
		"login":      updatedProfile.GetLogin(),
		"email":      updatedProfile.GetEmail(),
		"firstName":  updatedProfile.GetFirstName(),
		"lastName":   updatedProfile.GetLastName(),
		"status":     user.GetStatus(),
		"updated":    time.Now().UTC().Format(time.RFC3339),
		"responseId": resp.Header.Get("x-okta-request-id"),
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// UserDeactivateExecutor handles okta-user-deactivate node type
type UserDeactivateExecutor struct{}

func (e *UserDeactivateExecutor) Type() string { return "okta-user-deactivate" }

func (e *UserDeactivateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	oktaCfg := parseOktaConfig(step.Config)

	client, err := getOktaClient(oktaCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create Okta client: %w", err)
	}

	userId := getString(step.Config, "userId")
	if userId == "" {
		return nil, fmt.Errorf("userId is required")
	}

	sendEmail := getBool(step.Config, "sendEmail", false)

	req := client.UserAPI.DeactivateUser(ctx, userId)
	if sendEmail {
		req = req.SendEmail(true)
	}

	resp, err := req.Execute()
	if err != nil {
		return nil, fmt.Errorf("failed to deactivate user: %w", err)
	}

	output := map[string]interface{}{
		"success":    true,
		"userId":     userId,
		"status":     "DEACTIVATED",
		"responseId": resp.Header.Get("x-okta-request-id"),
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// ============================================================================
// GROUP EXECUTORS
// ============================================================================

// GroupListExecutor handles okta-group-list node type
type GroupListExecutor struct{}

func (e *GroupListExecutor) Type() string { return "okta-group-list" }

func (e *GroupListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	oktaCfg := parseOktaConfig(step.Config)

	client, err := getOktaClient(oktaCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create Okta client: %w", err)
	}

	query := getString(step.Config, "query")
	limit := getInt(step.Config, "limit", 50)

	req := client.GroupAPI.ListGroups(ctx)

	if query != "" {
		req = req.Q(query)
	}
	req = req.Limit(int32(limit))

	groups, resp, err := req.Execute()
	if err != nil {
		return nil, fmt.Errorf("failed to list groups: %w", err)
	}

	// Convert groups to output format
	var groupList []map[string]interface{}
	for _, group := range groups {
		profile := group.GetProfile()
		groupMap := map[string]interface{}{
			"id":                    group.GetId(),
			"name":                  profile.GetName(),
			"description":           profile.GetDescription(),
			"created":               group.GetCreated(),
			"lastUpdated":           group.GetLastUpdated(),
			"lastMembershipUpdated": group.GetLastMembershipUpdated(),
		}
		groupList = append(groupList, groupMap)
	}

	output := map[string]interface{}{
		"groups":     groupList,
		"count":      len(groupList),
		"responseId": resp.Header.Get("x-okta-request-id"),
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// GroupCreateExecutor handles okta-group-create node type
type GroupCreateExecutor struct{}

func (e *GroupCreateExecutor) Type() string { return "okta-group-create" }

func (e *GroupCreateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	oktaCfg := parseOktaConfig(step.Config)

	client, err := getOktaClient(oktaCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create Okta client: %w", err)
	}

	name := getString(step.Config, "name")
	if name == "" {
		return nil, fmt.Errorf("group name is required")
	}

	description := getString(step.Config, "description")

	// Create group profile
	profile := okta.NewGroupProfile()
	profile.SetName(name)
	profile.SetDescription(description)

	// Create group object
	group := okta.NewGroup()
	group.SetProfile(*profile)

	createdGroup, resp, err := client.GroupAPI.CreateGroup(ctx).Group(*group).Execute()
	if err != nil {
		return nil, fmt.Errorf("failed to create group: %w", err)
	}

	createdProfile := createdGroup.GetProfile()
	output := map[string]interface{}{
		"id":          createdGroup.GetId(),
		"name":        createdProfile.GetName(),
		"description": createdProfile.GetDescription(),
		"created":     createdGroup.GetCreated(),
		"responseId":  resp.Header.Get("x-okta-request-id"),
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// GroupAddUserExecutor handles okta-group-add-user node type
type GroupAddUserExecutor struct{}

func (e *GroupAddUserExecutor) Type() string { return "okta-group-add-user" }

func (e *GroupAddUserExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	oktaCfg := parseOktaConfig(step.Config)

	client, err := getOktaClient(oktaCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create Okta client: %w", err)
	}

	groupId := getString(step.Config, "groupId")
	userId := getString(step.Config, "userId")

	if groupId == "" {
		return nil, fmt.Errorf("groupId is required")
	}
	if userId == "" {
		return nil, fmt.Errorf("userId is required")
	}

	resp, err := client.GroupAPI.AssignUserToGroup(ctx, groupId, userId).Execute()
	if err != nil {
		return nil, fmt.Errorf("failed to add user to group: %w", err)
	}

	output := map[string]interface{}{
		"success":    true,
		"groupId":    groupId,
		"userId":     userId,
		"responseId": resp.Header.Get("x-okta-request-id"),
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// ============================================================================
// APPLICATION EXECUTORS
// ============================================================================

// AppListExecutor handles okta-app-list node type
type AppListExecutor struct{}

func (e *AppListExecutor) Type() string { return "okta-app-list" }

func (e *AppListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	oktaCfg := parseOktaConfig(step.Config)

	client, err := getOktaClient(oktaCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create Okta client: %w", err)
	}

	query := getString(step.Config, "query")
	status := getString(step.Config, "status")
	limit := getInt(step.Config, "limit", 50)

	req := client.ApplicationAPI.ListApplications(ctx)

	if query != "" {
		req = req.Q(query)
	}
	if status != "" {
		req = req.Filter(fmt.Sprintf("status eq \"%s\"", status))
	}
	req = req.Limit(int32(limit))

	apps, resp, err := req.Execute()
	if err != nil {
		return nil, fmt.Errorf("failed to list applications: %w", err)
	}

	// Convert applications to output format
	var appList []map[string]interface{}
	for _, app := range apps {
		// Get the actual application instance
		actualApp := app.GetActualInstance()
		
		// Extract common Application fields using type assertion
		var id, name, label, status, signOnMode, created, lastUpdated interface{}
		
		switch v := actualApp.(type) {
		case *okta.AutoLoginApplication:
			id = v.GetId()
			name = v.GetName()
			label = v.GetLabel()
			status = v.GetStatus()
			signOnMode = v.GetSignOnMode()
			created = v.GetCreated()
			lastUpdated = v.GetLastUpdated()
		case *okta.BasicAuthApplication:
			id = v.GetId()
			name = v.GetName()
			label = v.GetLabel()
			status = v.GetStatus()
			signOnMode = v.GetSignOnMode()
			created = v.GetCreated()
			lastUpdated = v.GetLastUpdated()
		case *okta.BookmarkApplication:
			id = v.GetId()
			label = v.GetLabel()
			status = v.GetStatus()
			signOnMode = v.GetSignOnMode()
			created = v.GetCreated()
			lastUpdated = v.GetLastUpdated()
		case *okta.BrowserPluginApplication:
			id = v.GetId()
			name = v.GetName()
			label = v.GetLabel()
			status = v.GetStatus()
			signOnMode = v.GetSignOnMode()
			created = v.GetCreated()
			lastUpdated = v.GetLastUpdated()
		case *okta.OpenIdConnectApplication:
			id = v.GetId()
			name = v.GetName()
			label = v.GetLabel()
			status = v.GetStatus()
			signOnMode = v.GetSignOnMode()
			created = v.GetCreated()
			lastUpdated = v.GetLastUpdated()
		case *okta.Saml11Application:
			id = v.GetId()
			name = v.GetName()
			label = v.GetLabel()
			status = v.GetStatus()
			signOnMode = v.GetSignOnMode()
			created = v.GetCreated()
			lastUpdated = v.GetLastUpdated()
		case *okta.SamlApplication:
			id = v.GetId()
			name = v.GetName()
			label = v.GetLabel()
			status = v.GetStatus()
			signOnMode = v.GetSignOnMode()
			created = v.GetCreated()
			lastUpdated = v.GetLastUpdated()
		case *okta.SecurePasswordStoreApplication:
			id = v.GetId()
			name = v.GetName()
			label = v.GetLabel()
			status = v.GetStatus()
			signOnMode = v.GetSignOnMode()
			created = v.GetCreated()
			lastUpdated = v.GetLastUpdated()
		case *okta.WsFederationApplication:
			id = v.GetId()
			label = v.GetLabel()
			status = v.GetStatus()
			signOnMode = v.GetSignOnMode()
			created = v.GetCreated()
			lastUpdated = v.GetLastUpdated()
		}
		
		appMap := map[string]interface{}{
			"id":          id,
			"name":        name,
			"label":       label,
			"status":      status,
			"created":     created,
			"lastUpdated": lastUpdated,
		}
		if signOnMode != nil && signOnMode != "" {
			appMap["signOnMode"] = signOnMode
		}
		appList = append(appList, appMap)
	}

	output := map[string]interface{}{
		"applications": appList,
		"count":        len(appList),
		"responseId":   resp.Header.Get("x-okta-request-id"),
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// AppAssignUserExecutor handles okta-app-assign-user node type
type AppAssignUserExecutor struct{}

func (e *AppAssignUserExecutor) Type() string { return "okta-app-assign-user" }

func (e *AppAssignUserExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	oktaCfg := parseOktaConfig(step.Config)

	client, err := getOktaClient(oktaCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create Okta client: %w", err)
	}

	appId := getString(step.Config, "appId")
	userId := getString(step.Config, "userId")

	if appId == "" {
		return nil, fmt.Errorf("appId is required")
	}
	if userId == "" {
		return nil, fmt.Errorf("userId is required")
	}

	// Build assignment request
	assignment := okta.NewAppUserAssignRequest(userId)

	// Parse profile if provided
	profileJSON := getString(step.Config, "profile")
	if profileJSON != "" {
		var profile map[string]interface{}
		if err := json.Unmarshal([]byte(profileJSON), &profile); err == nil {
			assignment.SetProfile(profile)
		}
	}

	appUser, resp, err := client.ApplicationUsersAPI.AssignUserToApplication(ctx, appId).AppUser(*assignment).Execute()
	if err != nil {
		return nil, fmt.Errorf("failed to assign user to application: %w", err)
	}

	output := map[string]interface{}{
		"success":    true,
		"appId":      appId,
		"userId":     userId,
		"assignedId": appUser.GetId(),
		"responseId": resp.Header.Get("x-okta-request-id"),
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// ============================================================================
// MFA EXECUTORS
// ============================================================================

// MFAEnrollExecutor handles okta-mfa-enroll node type
type MFAEnrollExecutor struct{}

func (e *MFAEnrollExecutor) Type() string { return "okta-mfa-enroll" }

func (e *MFAEnrollExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	oktaCfg := parseOktaConfig(step.Config)

	client, err := getOktaClient(oktaCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create Okta client: %w", err)
	}

	userId := getString(step.Config, "userId")
	factorType := getString(step.Config, "factorType")

	if userId == "" {
		return nil, fmt.Errorf("userId is required")
	}
	if factorType == "" {
		factorType = "sms"
	}

	// Build factor request based on type using the union type
	var factor okta.ListFactors200ResponseInner

	switch factorType {
	case "sms":
		phoneNumber := getString(step.Config, "phoneNumber")
		if phoneNumber == "" {
			return nil, fmt.Errorf("phoneNumber is required for SMS factor")
		}
		smsFactor := okta.NewUserFactorSMS()
		smsFactor.SetFactorType("sms")
		smsFactor.SetProvider("OKTA")
		profile := okta.NewUserFactorSMSProfile()
		profile.SetPhoneNumber(phoneNumber)
		smsFactor.SetProfile(*profile)
		factor = okta.UserFactorSMSAsListFactors200ResponseInner(smsFactor)

	case "email":
		email := getString(step.Config, "email")
		if email == "" {
			return nil, fmt.Errorf("email is required for email factor")
		}
		emailFactor := okta.NewUserFactorEmail()
		emailFactor.SetFactorType("email")
		emailFactor.SetProvider("OKTA")
		profile := okta.NewUserFactorEmailProfile()
		profile.SetEmail(email)
		emailFactor.SetProfile(*profile)
		factor = okta.UserFactorEmailAsListFactors200ResponseInner(emailFactor)

	case "token:software:totp":
		totpFactor := okta.NewUserFactorTOTP()
		totpFactor.SetFactorType("token:software:totp")
		totpFactor.SetProvider("GOOGLE")
		factor = okta.UserFactorTOTPAsListFactors200ResponseInner(totpFactor)

	case "push":
		pushFactor := okta.NewUserFactorPush()
		pushFactor.SetFactorType("push")
		pushFactor.SetProvider("OKTA")
		factor = okta.UserFactorPushAsListFactors200ResponseInner(pushFactor)

	case "webauthn":
		webauthnFactor := okta.NewUserFactorWebAuthn()
		webauthnFactor.SetFactorType("webauthn")
		webauthnFactor.SetProvider("FIDO")
		factor = okta.UserFactorWebAuthnAsListFactors200ResponseInner(webauthnFactor)

	default:
		return nil, fmt.Errorf("unsupported factor type: %s", factorType)
	}

	enrolledFactorResult, resp, err := client.UserFactorAPI.EnrollFactor(ctx, userId).Body(factor).Execute()
	if err != nil {
		return nil, fmt.Errorf("failed to enroll user in MFA: %w", err)
	}

	// Extract factor from union type
	actualFactor := enrolledFactorResult.GetActualInstance()
	
	var factorId, factorTypeOut, provider, status string
	var factorProfile map[string]interface{}
	
	switch v := actualFactor.(type) {
	case *okta.UserFactorSMS:
		factorId = v.GetId()
		factorTypeOut = fmt.Sprintf("%v", v.GetFactorType())
		provider = v.GetProvider()
		status = v.GetStatus()
		if v.HasProfile() {
			profileBytes, _ := json.Marshal(v.GetProfile())
			json.Unmarshal(profileBytes, &factorProfile)
		}
	case *okta.UserFactorEmail:
		factorId = v.GetId()
		factorTypeOut = fmt.Sprintf("%v", v.GetFactorType())
		provider = v.GetProvider()
		status = v.GetStatus()
		if v.HasProfile() {
			profileBytes, _ := json.Marshal(v.GetProfile())
			json.Unmarshal(profileBytes, &factorProfile)
		}
	case *okta.UserFactorTOTP:
		factorId = v.GetId()
		factorTypeOut = fmt.Sprintf("%v", v.GetFactorType())
		provider = v.GetProvider()
		status = v.GetStatus()
	case *okta.UserFactorPush:
		factorId = v.GetId()
		factorTypeOut = fmt.Sprintf("%v", v.GetFactorType())
		provider = v.GetProvider()
		status = v.GetStatus()
	case *okta.UserFactorWebAuthn:
		factorId = v.GetId()
		factorTypeOut = fmt.Sprintf("%v", v.GetFactorType())
		provider = v.GetProvider()
		status = v.GetStatus()
	}

	output := map[string]interface{}{
		"success":    true,
		"userId":     userId,
		"factorId":   factorId,
		"factorType": factorTypeOut,
		"provider":   provider,
		"status":     status,
		"responseId": resp.Header.Get("x-okta-request-id"),
	}

	// Include enrollment details if available
	if factorProfile != nil {
		output["profile"] = factorProfile
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// ============================================================================
// SESSION EXECUTORS
// ============================================================================

// SessionRevokeExecutor handles okta-session-revoke node type
type SessionRevokeExecutor struct{}

func (e *SessionRevokeExecutor) Type() string { return "okta-session-revoke" }

func (e *SessionRevokeExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	oktaCfg := parseOktaConfig(step.Config)

	client, err := getOktaClient(oktaCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create Okta client: %w", err)
	}

	sessionId := getString(step.Config, "sessionId")
	if sessionId == "" {
		return nil, fmt.Errorf("sessionId is required")
	}

	resp, err := client.SessionAPI.RevokeSession(ctx, sessionId).Execute()
	if err != nil {
		return nil, fmt.Errorf("failed to revoke session: %w", err)
	}

	output := map[string]interface{}{
		"success":    true,
		"sessionId":  sessionId,
		"status":     "REVOKED",
		"responseId": resp.Header.Get("x-okta-request-id"),
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}
