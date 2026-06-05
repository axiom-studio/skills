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
	"strconv"
	"strings"
	"time"

	"github.com/axiom-studio/skills.sdk/executor"
	"github.com/axiom-studio/skills.sdk/grpc"
	"github.com/axiom-studio/skills.sdk/resolver"
)

const (
	defaultPipedriveBaseURL = "https://api.pipedrive.com"
	iconPipedrive           = "kanban"
)

var httpClient = &http.Client{Timeout: 60 * time.Second}

func main() {
	port := os.Getenv("SKILL_PORT")
	if port == "" {
		port = "50133"
	}

	server := grpc.NewSkillServer("skill-pipedrive", "1.0.0")
	server.RegisterExecutorWithSchema("pipedrive-api-request", &APIRequestExecutor{}, APIRequestSchema)
	server.RegisterExecutorWithSchema("pipedrive-search", &SearchExecutor{}, SearchSchema)
	server.RegisterExecutorWithSchema("pipedrive-list", &ListExecutor{}, ListSchema)
	server.RegisterExecutorWithSchema("pipedrive-get", &GetExecutor{}, GetSchema)
	server.RegisterExecutorWithSchema("pipedrive-create-record", &CreateRecordExecutor{}, CreateRecordSchema)
	server.RegisterExecutorWithSchema("pipedrive-update-record", &UpdateRecordExecutor{}, UpdateRecordSchema)
	server.RegisterExecutorWithSchema("pipedrive-delete-record", &DeleteRecordExecutor{}, DeleteRecordSchema)
	server.RegisterExecutorWithSchema("pipedrive-create-person", &CreatePersonExecutor{}, CreatePersonSchema)
	server.RegisterExecutorWithSchema("pipedrive-create-organization", &CreateOrganizationExecutor{}, CreateOrganizationSchema)
	server.RegisterExecutorWithSchema("pipedrive-create-deal", &CreateDealExecutor{}, CreateDealSchema)
	server.RegisterExecutorWithSchema("pipedrive-update-deal", &UpdateDealExecutor{}, UpdateDealSchema)
	server.RegisterExecutorWithSchema("pipedrive-create-activity", &CreateActivityExecutor{}, CreateActivitySchema)

	fmt.Printf("Starting skill-pipedrive gRPC server on port %s\n", port)
	if err := server.Serve(port); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to serve: %v\n", err)
		os.Exit(1)
	}
}

func getString(config map[string]interface{}, key string) string {
	if v, ok := config[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

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

func getFloat(config map[string]interface{}, key string, def float64) float64 {
	if v, ok := config[key]; ok {
		switch n := v.(type) {
		case float64:
			return n
		case int:
			return float64(n)
		}
	}
	return def
}

func getBool(config map[string]interface{}, key string, def bool) bool {
	if v, ok := config[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return def
}

func getMap(config map[string]interface{}, key string) map[string]interface{} {
	if v, ok := config[key]; ok {
		if m, ok := v.(map[string]interface{}); ok {
			return m
		}
	}
	return nil
}

func getBaseURL(config map[string]interface{}, templateResolver executor.TemplateResolver) string {
	baseURL := templateResolver.ResolveString(getString(config, "baseURL"))
	if baseURL == "" {
		return defaultPipedriveBaseURL
	}
	return strings.TrimRight(baseURL, "/")
}

func addIfString(body map[string]interface{}, key, value string) {
	if value != "" {
		body[key] = value
	}
}

func addIfInt(body map[string]interface{}, key string, value int) {
	if value != 0 {
		body[key] = value
	}
}

func mergeMap(dst map[string]interface{}, src map[string]interface{}) {
	for key, value := range src {
		dst[key] = value
	}
}

func entityPath(entity string) (string, error) {
	switch strings.ToLower(entity) {
	case "deal", "deals":
		return "/api/v2/deals", nil
	case "person", "persons", "people":
		return "/api/v2/persons", nil
	case "organization", "organizations", "org", "orgs":
		return "/api/v2/organizations", nil
	case "activity", "activities":
		return "/api/v2/activities", nil
	case "lead", "leads":
		return "/api/v1/leads", nil
	case "note", "notes":
		return "/api/v1/notes", nil
	case "product", "products":
		return "/api/v2/products", nil
	case "pipeline", "pipelines":
		return "/api/v2/pipelines", nil
	case "stage", "stages":
		return "/api/v2/stages", nil
	case "project", "projects":
		return "/api/v1/projects", nil
	case "user", "users":
		return "/api/v1/users", nil
	case "filter", "filters":
		return "/api/v1/filters", nil
	case "currency", "currencies":
		return "/api/v1/currencies", nil
	case "deal-field", "deal-fields", "dealfields":
		return "/api/v1/dealFields", nil
	case "person-field", "person-fields", "personfields":
		return "/api/v1/personFields", nil
	case "organization-field", "organization-fields", "organizationfields":
		return "/api/v1/organizationFields", nil
	case "activity-field", "activity-fields", "activityfields":
		return "/api/v1/activityFields", nil
	case "product-field", "product-fields", "productfields":
		return "/api/v1/productFields", nil
	default:
		return "", fmt.Errorf("unsupported entity %q", entity)
	}
}

func pipedriveRequest(ctx context.Context, apiToken, baseURL, method, path string, query url.Values, body interface{}) (map[string]interface{}, error) {
	if apiToken == "" {
		return nil, fmt.Errorf("apiToken is required")
	}
	if query == nil {
		query = url.Values{}
	}
	query.Set("api_token", apiToken)

	fullURL := strings.TrimRight(baseURL, "/") + path
	if encoded := query.Encode(); encoded != "" {
		fullURL += "?" + encoded
	}

	var bodyReader io.Reader
	if body != nil {
		bodyBytes, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request: %w", err)
		}
		bodyReader = bytes.NewReader(bodyBytes)
	}

	req, err := http.NewRequestWithContext(ctx, method, fullURL, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, formatAPIError(resp.StatusCode, respBody)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}
	return result, nil
}

func formatAPIError(statusCode int, body []byte) error {
	var errResp map[string]interface{}
	if err := json.Unmarshal(body, &errResp); err == nil {
		if msg, ok := errResp["error"].(string); ok {
			return fmt.Errorf("Pipedrive API error (%d): %s", statusCode, msg)
		}
		if msg, ok := errResp["error_info"].(string); ok {
			return fmt.Errorf("Pipedrive API error (%d): %s", statusCode, msg)
		}
		if msg, ok := errResp["message"].(string); ok {
			return fmt.Errorf("Pipedrive API error (%d): %s", statusCode, msg)
		}
	}
	return fmt.Errorf("Pipedrive API error (%d): %s", statusCode, string(body))
}

type APIRequestExecutor struct{}

var APIRequestSchema = resolver.NewSchemaBuilder("pipedrive-api-request").
	WithName("API Request").
	WithCategory("crm").
	WithIcon("send").
	WithDescription("Call any JSON Pipedrive API endpoint").
	AddSection("Connection").
	AddExpressionField("apiToken", "API Token",
		resolver.WithRequired(),
		resolver.WithPlaceholder("Enter your Pipedrive API token"),
		resolver.WithSensitive(),
	).
	AddExpressionField("baseURL", "Base URL",
		resolver.WithDefault(defaultPipedriveBaseURL),
		resolver.WithPlaceholder(defaultPipedriveBaseURL),
	).
	EndSection().
	AddSection("Request").
	AddSelectField("method", "Method",
		[]resolver.SelectOption{
			{Label: "GET", Value: "GET"},
			{Label: "POST", Value: "POST"},
			{Label: "PATCH", Value: "PATCH"},
			{Label: "PUT", Value: "PUT"},
			{Label: "DELETE", Value: "DELETE"},
		},
		resolver.WithDefault("GET"),
	).
	AddExpressionField("path", "Path",
		resolver.WithRequired(),
		resolver.WithPlaceholder("/api/v2/deals"),
	).
	AddJSONField("query", "Query",
		resolver.WithHeight(120),
		resolver.WithHint(`Optional query params, for example {"limit": 50}`),
	).
	AddJSONField("body", "Body",
		resolver.WithHeight(220),
		resolver.WithHint("JSON body for POST, PATCH, or PUT requests"),
	).
	EndSection().
	Build()

func (e *APIRequestExecutor) Type() string { return "pipedrive-api-request" }

func (e *APIRequestExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := templateResolver.ResolveMap(step.Config)
	apiToken := templateResolver.ResolveString(getString(config, "apiToken"))
	method := strings.ToUpper(templateResolver.ResolveString(getString(config, "method")))
	if method == "" {
		method = http.MethodGet
	}
	path := templateResolver.ResolveString(getString(config, "path"))
	if path == "" {
		return nil, fmt.Errorf("path is required")
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	query := url.Values{}
	for key, value := range getMap(config, "query") {
		query.Set(key, fmt.Sprint(value))
	}

	var body interface{}
	if m := getMap(config, "body"); len(m) > 0 {
		body = m
	}

	result, err := pipedriveRequest(ctx, apiToken, getBaseURL(config, templateResolver), method, path, query, body)
	if err != nil {
		return nil, err
	}
	return &executor.StepResult{Output: result}, nil
}

type SearchExecutor struct{}

var SearchSchema = resolver.NewSchemaBuilder("pipedrive-search").
	WithName("Search Pipedrive").
	WithCategory("crm").
	WithIcon("search").
	WithDescription("Search across Pipedrive CRM items").
	AddSection("Connection").
	AddExpressionField("apiToken", "API Token",
		resolver.WithRequired(),
		resolver.WithPlaceholder("Enter your Pipedrive API token"),
		resolver.WithSensitive(),
	).
	AddExpressionField("baseURL", "Base URL",
		resolver.WithDefault(defaultPipedriveBaseURL),
		resolver.WithPlaceholder(defaultPipedriveBaseURL),
	).
	EndSection().
	AddSection("Search").
	AddExpressionField("term", "Term",
		resolver.WithRequired(),
		resolver.WithPlaceholder("Acme"),
	).
	AddSelectField("itemTypes", "Item Types",
		[]resolver.SelectOption{
			{Label: "All", Value: ""},
			{Label: "Deals", Value: "deal"},
			{Label: "Persons", Value: "person"},
			{Label: "Organizations", Value: "organization"},
			{Label: "Leads", Value: "lead"},
			{Label: "Products", Value: "product"},
		},
		resolver.WithHint("Use a comma-separated expression for multiple types"),
	).
	AddExpressionField("fields", "Fields",
		resolver.WithPlaceholder("name,email,phone,title"),
	).
	AddToggleField("exactMatch", "Exact Match",
		resolver.WithDefault(false),
	).
	AddNumberField("limit", "Limit",
		resolver.WithDefault(100),
		resolver.WithMinMax(1, 100),
	).
	EndSection().
	Build()

func (e *SearchExecutor) Type() string { return "pipedrive-search" }

func (e *SearchExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := templateResolver.ResolveMap(step.Config)
	apiToken := templateResolver.ResolveString(getString(config, "apiToken"))
	baseURL := getBaseURL(config, templateResolver)
	term := templateResolver.ResolveString(getString(config, "term"))
	if term == "" {
		return nil, fmt.Errorf("term is required")
	}

	query := url.Values{}
	query.Set("term", term)
	if itemTypes := templateResolver.ResolveString(getString(config, "itemTypes")); itemTypes != "" {
		query.Set("item_types", itemTypes)
	}
	if fields := templateResolver.ResolveString(getString(config, "fields")); fields != "" {
		query.Set("fields", fields)
	}
	if getBool(config, "exactMatch", false) {
		query.Set("exact_match", "true")
	}
	if limit := getInt(config, "limit", 100); limit > 0 {
		query.Set("limit", strconv.Itoa(limit))
	}

	result, err := pipedriveRequest(ctx, apiToken, baseURL, http.MethodGet, "/api/v2/itemSearch", query, nil)
	if err != nil {
		return nil, err
	}
	return &executor.StepResult{Output: result}, nil
}

type ListExecutor struct{}

var ListSchema = resolver.NewSchemaBuilder("pipedrive-list").
	WithName("List Records").
	WithCategory("crm").
	WithIcon(iconPipedrive).
	WithDescription("List Pipedrive deals, persons, organizations, or activities").
	AddSection("Connection").
	AddExpressionField("apiToken", "API Token", resolver.WithRequired(), resolver.WithSensitive()).
	AddExpressionField("baseURL", "Base URL", resolver.WithDefault(defaultPipedriveBaseURL)).
	EndSection().
	AddSection("Record").
	AddSelectField("entity", "Entity",
		[]resolver.SelectOption{
			{Label: "Deals", Value: "deals"},
			{Label: "Persons", Value: "persons"},
			{Label: "Organizations", Value: "organizations"},
			{Label: "Activities", Value: "activities"},
			{Label: "Leads", Value: "leads"},
			{Label: "Notes", Value: "notes"},
			{Label: "Products", Value: "products"},
			{Label: "Pipelines", Value: "pipelines"},
			{Label: "Stages", Value: "stages"},
			{Label: "Projects", Value: "projects"},
			{Label: "Users", Value: "users"},
			{Label: "Filters", Value: "filters"},
			{Label: "Currencies", Value: "currencies"},
		},
		resolver.WithRequired(),
	).
	AddJSONField("filters", "Filters",
		resolver.WithHeight(160),
		resolver.WithHint(`Optional query params, for example {"limit": 50, "cursor": "..."}`),
	).
	EndSection().
	Build()

func (e *ListExecutor) Type() string { return "pipedrive-list" }

func (e *ListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := templateResolver.ResolveMap(step.Config)
	path, err := entityPath(templateResolver.ResolveString(getString(config, "entity")))
	if err != nil {
		return nil, err
	}

	query := url.Values{}
	for key, value := range getMap(config, "filters") {
		query.Set(key, fmt.Sprint(value))
	}

	result, err := pipedriveRequest(ctx, templateResolver.ResolveString(getString(config, "apiToken")), getBaseURL(config, templateResolver), http.MethodGet, path, query, nil)
	if err != nil {
		return nil, err
	}
	return &executor.StepResult{Output: result}, nil
}

type GetExecutor struct{}

var GetSchema = resolver.NewSchemaBuilder("pipedrive-get").
	WithName("Get Record").
	WithCategory("crm").
	WithIcon(iconPipedrive).
	WithDescription("Get a Pipedrive record by ID").
	AddSection("Connection").
	AddExpressionField("apiToken", "API Token", resolver.WithRequired(), resolver.WithSensitive()).
	AddExpressionField("baseURL", "Base URL", resolver.WithDefault(defaultPipedriveBaseURL)).
	EndSection().
	AddSection("Record").
	AddSelectField("entity", "Entity",
		[]resolver.SelectOption{
			{Label: "Deal", Value: "deal"},
			{Label: "Person", Value: "person"},
			{Label: "Organization", Value: "organization"},
			{Label: "Activity", Value: "activity"},
			{Label: "Lead", Value: "lead"},
			{Label: "Note", Value: "note"},
			{Label: "Product", Value: "product"},
			{Label: "Pipeline", Value: "pipeline"},
			{Label: "Stage", Value: "stage"},
			{Label: "Project", Value: "project"},
			{Label: "User", Value: "user"},
			{Label: "Filter", Value: "filter"},
		},
		resolver.WithRequired(),
	).
	AddExpressionField("id", "ID",
		resolver.WithRequired(),
	).
	EndSection().
	Build()

func (e *GetExecutor) Type() string { return "pipedrive-get" }

func (e *GetExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := templateResolver.ResolveMap(step.Config)
	path, err := entityPath(templateResolver.ResolveString(getString(config, "entity")))
	if err != nil {
		return nil, err
	}
	id := templateResolver.ResolveString(getString(config, "id"))
	if id == "" {
		return nil, fmt.Errorf("id is required")
	}

	result, err := pipedriveRequest(ctx, templateResolver.ResolveString(getString(config, "apiToken")), getBaseURL(config, templateResolver), http.MethodGet, path+"/"+url.PathEscape(id), nil, nil)
	if err != nil {
		return nil, err
	}
	return &executor.StepResult{Output: result}, nil
}

type CreateRecordExecutor struct{}

var CreateRecordSchema = resolver.NewSchemaBuilder("pipedrive-create-record").
	WithName("Create Record").
	WithCategory("crm").
	WithIcon("plus-circle").
	WithDescription("Create a Pipedrive record with a raw JSON body").
	AddSection("Connection").
	AddExpressionField("apiToken", "API Token", resolver.WithRequired(), resolver.WithSensitive()).
	AddExpressionField("baseURL", "Base URL", resolver.WithDefault(defaultPipedriveBaseURL)).
	EndSection().
	AddSection("Record").
	AddSelectField("entity", "Entity",
		[]resolver.SelectOption{
			{Label: "Deal", Value: "deal"},
			{Label: "Person", Value: "person"},
			{Label: "Organization", Value: "organization"},
			{Label: "Activity", Value: "activity"},
			{Label: "Lead", Value: "lead"},
			{Label: "Note", Value: "note"},
			{Label: "Product", Value: "product"},
			{Label: "Pipeline", Value: "pipeline"},
			{Label: "Stage", Value: "stage"},
			{Label: "Project", Value: "project"},
			{Label: "Filter", Value: "filter"},
		},
		resolver.WithRequired(),
	).
	AddJSONField("data", "Data", resolver.WithRequired(), resolver.WithHeight(260)).
	EndSection().
	Build()

func (e *CreateRecordExecutor) Type() string { return "pipedrive-create-record" }

func (e *CreateRecordExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := templateResolver.ResolveMap(step.Config)
	path, err := entityPath(templateResolver.ResolveString(getString(config, "entity")))
	if err != nil {
		return nil, err
	}
	data := getMap(config, "data")
	if len(data) == 0 {
		return nil, fmt.Errorf("data is required")
	}

	result, err := pipedriveRequest(ctx, templateResolver.ResolveString(getString(config, "apiToken")), getBaseURL(config, templateResolver), http.MethodPost, path, nil, data)
	if err != nil {
		return nil, err
	}
	return &executor.StepResult{Output: result}, nil
}

type UpdateRecordExecutor struct{}

var UpdateRecordSchema = resolver.NewSchemaBuilder("pipedrive-update-record").
	WithName("Update Record").
	WithCategory("crm").
	WithIcon("edit").
	WithDescription("Update a Pipedrive record with a raw JSON body").
	AddSection("Connection").
	AddExpressionField("apiToken", "API Token", resolver.WithRequired(), resolver.WithSensitive()).
	AddExpressionField("baseURL", "Base URL", resolver.WithDefault(defaultPipedriveBaseURL)).
	EndSection().
	AddSection("Record").
	AddSelectField("entity", "Entity",
		[]resolver.SelectOption{
			{Label: "Deal", Value: "deal"},
			{Label: "Person", Value: "person"},
			{Label: "Organization", Value: "organization"},
			{Label: "Activity", Value: "activity"},
			{Label: "Lead", Value: "lead"},
			{Label: "Note", Value: "note"},
			{Label: "Product", Value: "product"},
			{Label: "Pipeline", Value: "pipeline"},
			{Label: "Stage", Value: "stage"},
			{Label: "Project", Value: "project"},
			{Label: "Filter", Value: "filter"},
		},
		resolver.WithRequired(),
	).
	AddExpressionField("id", "ID", resolver.WithRequired()).
	AddJSONField("data", "Data", resolver.WithRequired(), resolver.WithHeight(260)).
	EndSection().
	Build()

func (e *UpdateRecordExecutor) Type() string { return "pipedrive-update-record" }

func (e *UpdateRecordExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := templateResolver.ResolveMap(step.Config)
	path, err := entityPath(templateResolver.ResolveString(getString(config, "entity")))
	if err != nil {
		return nil, err
	}
	id := templateResolver.ResolveString(getString(config, "id"))
	if id == "" {
		return nil, fmt.Errorf("id is required")
	}
	data := getMap(config, "data")
	if len(data) == 0 {
		return nil, fmt.Errorf("data is required")
	}

	method := http.MethodPatch
	if strings.Contains(path, "/api/v1/notes") {
		method = http.MethodPut
	}
	result, err := pipedriveRequest(ctx, templateResolver.ResolveString(getString(config, "apiToken")), getBaseURL(config, templateResolver), method, path+"/"+url.PathEscape(id), nil, data)
	if err != nil {
		return nil, err
	}
	return &executor.StepResult{Output: result}, nil
}

type DeleteRecordExecutor struct{}

var DeleteRecordSchema = resolver.NewSchemaBuilder("pipedrive-delete-record").
	WithName("Delete Record").
	WithCategory("crm").
	WithIcon("trash-2").
	WithDescription("Delete a Pipedrive record by ID").
	AddSection("Connection").
	AddExpressionField("apiToken", "API Token", resolver.WithRequired(), resolver.WithSensitive()).
	AddExpressionField("baseURL", "Base URL", resolver.WithDefault(defaultPipedriveBaseURL)).
	EndSection().
	AddSection("Record").
	AddSelectField("entity", "Entity",
		[]resolver.SelectOption{
			{Label: "Deal", Value: "deal"},
			{Label: "Person", Value: "person"},
			{Label: "Organization", Value: "organization"},
			{Label: "Activity", Value: "activity"},
			{Label: "Lead", Value: "lead"},
			{Label: "Note", Value: "note"},
			{Label: "Product", Value: "product"},
			{Label: "Project", Value: "project"},
			{Label: "Filter", Value: "filter"},
		},
		resolver.WithRequired(),
	).
	AddExpressionField("id", "ID", resolver.WithRequired()).
	EndSection().
	Build()

func (e *DeleteRecordExecutor) Type() string { return "pipedrive-delete-record" }

func (e *DeleteRecordExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := templateResolver.ResolveMap(step.Config)
	path, err := entityPath(templateResolver.ResolveString(getString(config, "entity")))
	if err != nil {
		return nil, err
	}
	id := templateResolver.ResolveString(getString(config, "id"))
	if id == "" {
		return nil, fmt.Errorf("id is required")
	}

	result, err := pipedriveRequest(ctx, templateResolver.ResolveString(getString(config, "apiToken")), getBaseURL(config, templateResolver), http.MethodDelete, path+"/"+url.PathEscape(id), nil, nil)
	if err != nil {
		return nil, err
	}
	return &executor.StepResult{Output: result}, nil
}

type CreatePersonExecutor struct{}

var CreatePersonSchema = resolver.NewSchemaBuilder("pipedrive-create-person").
	WithName("Create Person").
	WithCategory("crm").
	WithIcon("user-plus").
	WithDescription("Create a Pipedrive person").
	AddSection("Connection").
	AddExpressionField("apiToken", "API Token", resolver.WithRequired(), resolver.WithSensitive()).
	AddExpressionField("baseURL", "Base URL", resolver.WithDefault(defaultPipedriveBaseURL)).
	EndSection().
	AddSection("Person").
	AddExpressionField("name", "Name", resolver.WithRequired(), resolver.WithPlaceholder("Jane Smith")).
	AddNumberField("orgId", "Organization ID").
	AddExpressionField("email", "Email", resolver.WithPlaceholder("jane@example.com")).
	AddExpressionField("phone", "Phone", resolver.WithPlaceholder("+1 555 0100")).
	AddJSONField("data", "Additional Fields", resolver.WithHeight(160)).
	EndSection().
	Build()

func (e *CreatePersonExecutor) Type() string { return "pipedrive-create-person" }

func (e *CreatePersonExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := templateResolver.ResolveMap(step.Config)
	name := templateResolver.ResolveString(getString(config, "name"))
	if name == "" {
		return nil, fmt.Errorf("name is required")
	}

	body := map[string]interface{}{"name": name}
	addIfInt(body, "org_id", getInt(config, "orgId", 0))
	addIfString(body, "email", templateResolver.ResolveString(getString(config, "email")))
	addIfString(body, "phone", templateResolver.ResolveString(getString(config, "phone")))
	mergeMap(body, getMap(config, "data"))

	result, err := pipedriveRequest(ctx, templateResolver.ResolveString(getString(config, "apiToken")), getBaseURL(config, templateResolver), http.MethodPost, "/api/v2/persons", nil, body)
	if err != nil {
		return nil, err
	}
	return &executor.StepResult{Output: result}, nil
}

type CreateOrganizationExecutor struct{}

var CreateOrganizationSchema = resolver.NewSchemaBuilder("pipedrive-create-organization").
	WithName("Create Organization").
	WithCategory("crm").
	WithIcon("building-2").
	WithDescription("Create a Pipedrive organization").
	AddSection("Connection").
	AddExpressionField("apiToken", "API Token", resolver.WithRequired(), resolver.WithSensitive()).
	AddExpressionField("baseURL", "Base URL", resolver.WithDefault(defaultPipedriveBaseURL)).
	EndSection().
	AddSection("Organization").
	AddExpressionField("name", "Name", resolver.WithRequired(), resolver.WithPlaceholder("Acme Inc")).
	AddNumberField("ownerId", "Owner ID").
	AddJSONField("data", "Additional Fields", resolver.WithHeight(160)).
	EndSection().
	Build()

func (e *CreateOrganizationExecutor) Type() string { return "pipedrive-create-organization" }

func (e *CreateOrganizationExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := templateResolver.ResolveMap(step.Config)
	name := templateResolver.ResolveString(getString(config, "name"))
	if name == "" {
		return nil, fmt.Errorf("name is required")
	}

	body := map[string]interface{}{"name": name}
	addIfInt(body, "owner_id", getInt(config, "ownerId", 0))
	mergeMap(body, getMap(config, "data"))

	result, err := pipedriveRequest(ctx, templateResolver.ResolveString(getString(config, "apiToken")), getBaseURL(config, templateResolver), http.MethodPost, "/api/v2/organizations", nil, body)
	if err != nil {
		return nil, err
	}
	return &executor.StepResult{Output: result}, nil
}

type CreateDealExecutor struct{}

var CreateDealSchema = resolver.NewSchemaBuilder("pipedrive-create-deal").
	WithName("Create Deal").
	WithCategory("crm").
	WithIcon("badge-dollar-sign").
	WithDescription("Create a Pipedrive deal").
	AddSection("Connection").
	AddExpressionField("apiToken", "API Token", resolver.WithRequired(), resolver.WithSensitive()).
	AddExpressionField("baseURL", "Base URL", resolver.WithDefault(defaultPipedriveBaseURL)).
	EndSection().
	AddSection("Deal").
	AddExpressionField("title", "Title", resolver.WithRequired(), resolver.WithPlaceholder("Acme renewal")).
	AddNumberField("value", "Value").
	AddExpressionField("currency", "Currency", resolver.WithPlaceholder("USD")).
	AddNumberField("personId", "Person ID").
	AddNumberField("orgId", "Organization ID").
	AddNumberField("pipelineId", "Pipeline ID").
	AddNumberField("stageId", "Stage ID").
	AddJSONField("data", "Additional Fields", resolver.WithHeight(180)).
	EndSection().
	Build()

func (e *CreateDealExecutor) Type() string { return "pipedrive-create-deal" }

func (e *CreateDealExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := templateResolver.ResolveMap(step.Config)
	title := templateResolver.ResolveString(getString(config, "title"))
	if title == "" {
		return nil, fmt.Errorf("title is required")
	}

	body := map[string]interface{}{"title": title}
	if value := getFloat(config, "value", 0); value != 0 {
		body["value"] = value
	}
	addIfString(body, "currency", templateResolver.ResolveString(getString(config, "currency")))
	addIfInt(body, "person_id", getInt(config, "personId", 0))
	addIfInt(body, "org_id", getInt(config, "orgId", 0))
	addIfInt(body, "pipeline_id", getInt(config, "pipelineId", 0))
	addIfInt(body, "stage_id", getInt(config, "stageId", 0))
	mergeMap(body, getMap(config, "data"))

	result, err := pipedriveRequest(ctx, templateResolver.ResolveString(getString(config, "apiToken")), getBaseURL(config, templateResolver), http.MethodPost, "/api/v2/deals", nil, body)
	if err != nil {
		return nil, err
	}
	return &executor.StepResult{Output: result}, nil
}

type UpdateDealExecutor struct{}

var UpdateDealSchema = resolver.NewSchemaBuilder("pipedrive-update-deal").
	WithName("Update Deal").
	WithCategory("crm").
	WithIcon("badge-dollar-sign").
	WithDescription("Update a Pipedrive deal").
	AddSection("Connection").
	AddExpressionField("apiToken", "API Token", resolver.WithRequired(), resolver.WithSensitive()).
	AddExpressionField("baseURL", "Base URL", resolver.WithDefault(defaultPipedriveBaseURL)).
	EndSection().
	AddSection("Deal").
	AddExpressionField("dealId", "Deal ID", resolver.WithRequired()).
	AddJSONField("data", "Fields", resolver.WithRequired(), resolver.WithHeight(220)).
	EndSection().
	Build()

func (e *UpdateDealExecutor) Type() string { return "pipedrive-update-deal" }

func (e *UpdateDealExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := templateResolver.ResolveMap(step.Config)
	dealID := templateResolver.ResolveString(getString(config, "dealId"))
	if dealID == "" {
		return nil, fmt.Errorf("dealId is required")
	}
	data := getMap(config, "data")
	if len(data) == 0 {
		return nil, fmt.Errorf("data is required")
	}

	result, err := pipedriveRequest(ctx, templateResolver.ResolveString(getString(config, "apiToken")), getBaseURL(config, templateResolver), http.MethodPatch, "/api/v2/deals/"+url.PathEscape(dealID), nil, data)
	if err != nil {
		return nil, err
	}
	return &executor.StepResult{Output: result}, nil
}

type CreateActivityExecutor struct{}

var CreateActivitySchema = resolver.NewSchemaBuilder("pipedrive-create-activity").
	WithName("Create Activity").
	WithCategory("crm").
	WithIcon("calendar-plus").
	WithDescription("Create a Pipedrive activity").
	AddSection("Connection").
	AddExpressionField("apiToken", "API Token", resolver.WithRequired(), resolver.WithSensitive()).
	AddExpressionField("baseURL", "Base URL", resolver.WithDefault(defaultPipedriveBaseURL)).
	EndSection().
	AddSection("Activity").
	AddExpressionField("subject", "Subject", resolver.WithRequired(), resolver.WithPlaceholder("Follow up")).
	AddExpressionField("type", "Type", resolver.WithPlaceholder("call")).
	AddNumberField("dealId", "Deal ID").
	AddNumberField("personId", "Person ID").
	AddNumberField("orgId", "Organization ID").
	AddExpressionField("dueDate", "Due Date", resolver.WithPlaceholder("2026-06-30")).
	AddExpressionField("dueTime", "Due Time", resolver.WithPlaceholder("14:00")).
	AddJSONField("data", "Additional Fields", resolver.WithHeight(180)).
	EndSection().
	Build()

func (e *CreateActivityExecutor) Type() string { return "pipedrive-create-activity" }

func (e *CreateActivityExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := templateResolver.ResolveMap(step.Config)
	subject := templateResolver.ResolveString(getString(config, "subject"))
	if subject == "" {
		return nil, fmt.Errorf("subject is required")
	}

	body := map[string]interface{}{"subject": subject}
	addIfString(body, "type", templateResolver.ResolveString(getString(config, "type")))
	addIfInt(body, "deal_id", getInt(config, "dealId", 0))
	addIfInt(body, "person_id", getInt(config, "personId", 0))
	addIfInt(body, "org_id", getInt(config, "orgId", 0))
	addIfString(body, "due_date", templateResolver.ResolveString(getString(config, "dueDate")))
	addIfString(body, "due_time", templateResolver.ResolveString(getString(config, "dueTime")))
	mergeMap(body, getMap(config, "data"))

	result, err := pipedriveRequest(ctx, templateResolver.ResolveString(getString(config, "apiToken")), getBaseURL(config, templateResolver), http.MethodPost, "/api/v2/activities", nil, body)
	if err != nil {
		return nil, err
	}
	return &executor.StepResult{Output: result}, nil
}
