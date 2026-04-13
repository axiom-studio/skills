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

const (
	hubspotAPIBase = "https://api.hubapi.com"
)

func main() {
	// Get port from env or use default
	port := os.Getenv("SKILL_PORT")
	if port == "" {
		port = "50053"
	}

	// Create skill server
	server := grpc.NewSkillServer("skill-hubspot", "1.0.0")

	// Register HubSpot executors with schemas
	server.RegisterExecutorWithSchema("hs-contact-create", &HSContactCreateExecutor{}, HSContactCreateSchema)
	server.RegisterExecutorWithSchema("hs-contact-update", &HSContactUpdateExecutor{}, HSContactUpdateSchema)
	server.RegisterExecutorWithSchema("hs-contact-search", &HSContactSearchExecutor{}, HSContactSearchSchema)
	server.RegisterExecutorWithSchema("hs-deal-create", &HSDealCreateExecutor{}, HSDealCreateSchema)
	server.RegisterExecutorWithSchema("hs-deal-update", &HSDealUpdateExecutor{}, HSDealUpdateSchema)
	server.RegisterExecutorWithSchema("hs-company-create", &HSCompanyCreateExecutor{}, HSCompanyCreateSchema)
	server.RegisterExecutorWithSchema("hs-ticket-create", &HSTicketCreateExecutor{}, HSTicketCreateSchema)
	server.RegisterExecutorWithSchema("hs-engagement", &HSEngagementExecutor{}, HSEngagementSchema)

	fmt.Printf("Starting skill-hubspot gRPC server on port %s\n", port)
	if err := server.Serve(port); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to serve: %v\n", err)
		os.Exit(1)
	}
}

// makeHubSpotRequest makes an HTTP request to the HubSpot API
func makeHubSpotRequest(ctx context.Context, apiKey, method, endpoint string, body interface{}) ([]byte, error) {
	var reqBody io.Reader
	if body != nil {
		jsonData, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
		reqBody = bytes.NewReader(jsonData)
	}

	req, err := http.NewRequestWithContext(ctx, method, hubspotAPIBase+endpoint, reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("hubspot API error (%d): %s", resp.StatusCode, string(respBody))
	}

	return respBody, nil
}

// ============================================================================
// HSContactCreateExecutor
// ============================================================================

// HSContactCreateExecutor handles hs-contact-create node type
type HSContactCreateExecutor struct{}

// HSContactCreateConfig defines the typed configuration for hs-contact-create
type HSContactCreateConfig struct {
	APIKey     string                 `json:"apiKey" description:"HubSpot API key or access token, supports {{secrets.xxx}}"`
	Email      string                 `json:"email" description:"Contact email address"`
	FirstName  string                 `json:"firstName" description:"Contact first name"`
	LastName   string                 `json:"lastName" description:"Contact last name"`
	Phone      string                 `json:"phone" description:"Contact phone number"`
	Company    string                 `json:"company" description:"Contact company name"`
	Properties map[string]interface{} `json:"properties" description:"Additional contact properties"`
}

// HSContactCreateSchema is the UI schema for hs-contact-create
var HSContactCreateSchema = resolver.NewSchemaBuilder("hs-contact-create").
	WithName("Create HubSpot Contact").
	WithCategory("crm").
	WithIcon("user-plus").
	WithDescription("Create a new contact in HubSpot CRM").
	AddSection("Authentication").
		AddExpressionField("apiKey", "API Key",
			resolver.WithRequired(),
			resolver.WithPlaceholder("pat-na1-xxx-xxx-xxx"),
			resolver.WithHint("Supports {{secrets.xxx}} for secure credential storage"),
		).
		EndSection().
	AddSection("Contact Information").
		AddTextField("email", "Email",
			resolver.WithRequired(),
			resolver.WithPlaceholder("john@example.com"),
		).
		AddTextField("firstName", "First Name",
			resolver.WithPlaceholder("John"),
		).
		AddTextField("lastName", "Last Name",
			resolver.WithPlaceholder("Doe"),
		).
		AddTextField("phone", "Phone",
			resolver.WithPlaceholder("+1-555-123-4567"),
		).
		AddTextField("company", "Company",
			resolver.WithPlaceholder("Acme Inc"),
		).
		EndSection().
	AddSection("Additional Properties").
		AddJSONField("properties", "Custom Properties",
			resolver.WithHeight(100),
			resolver.WithHint("Additional HubSpot contact properties as key-value pairs"),
		).
		EndSection().
	Build()

func (e *HSContactCreateExecutor) Type() string { return "hs-contact-create" }

func (e *HSContactCreateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	// Parse config into typed struct
	var cfg HSContactCreateConfig
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

	if cfg.Email == "" {
		return nil, fmt.Errorf("email is required")
	}

	// Build properties
	properties := map[string]interface{}{
		"email": cfg.Email,
	}
	if cfg.FirstName != "" {
		properties["firstname"] = cfg.FirstName
	}
	if cfg.LastName != "" {
		properties["lastname"] = cfg.LastName
	}
	if cfg.Phone != "" {
		properties["phone"] = cfg.Phone
	}
	if cfg.Company != "" {
		properties["company"] = cfg.Company
	}
	for k, v := range cfg.Properties {
		properties[k] = v
	}

	reqBody := map[string]interface{}{
		"properties": properties,
	}

	respBody, err := makeHubSpotRequest(ctx, cfg.APIKey, "POST", "/crm/v3/objects/contacts", reqBody)
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"id":         result["id"],
			"properties": result["properties"],
			"createdAt":  result["createdAt"],
			"updatedAt":  result["updatedAt"],
		},
	}, nil
}

// ============================================================================
// HSContactUpdateExecutor
// ============================================================================

// HSContactUpdateExecutor handles hs-contact-update node type
type HSContactUpdateExecutor struct{}

// HSContactUpdateConfig defines the typed configuration for hs-contact-update
type HSContactUpdateConfig struct {
	APIKey     string                 `json:"apiKey" description:"HubSpot API key or access token"`
	ContactID  string                 `json:"contactId" description:"Contact ID or email"`
	Email      string                 `json:"email" description:"New email address"`
	FirstName  string                 `json:"firstName" description:"New first name"`
	LastName   string                 `json:"lastName" description:"New last name"`
	Phone      string                 `json:"phone" description:"New phone number"`
	Company    string                 `json:"company" description:"New company name"`
	Properties map[string]interface{} `json:"properties" description:"Additional contact properties to update"`
}

// HSContactUpdateSchema is the UI schema for hs-contact-update
var HSContactUpdateSchema = resolver.NewSchemaBuilder("hs-contact-update").
	WithName("Update HubSpot Contact").
	WithCategory("crm").
	WithIcon("user-edit").
	WithDescription("Update an existing contact in HubSpot CRM").
	AddSection("Authentication").
		AddExpressionField("apiKey", "API Key",
			resolver.WithRequired(),
		).
		EndSection().
	AddSection("Contact Identification").
		AddTextField("contactId", "Contact ID or Email",
			resolver.WithRequired(),
			resolver.WithPlaceholder("12345 or john@example.com"),
			resolver.WithHint("Use contact ID or email to identify the contact"),
		).
		EndSection().
	AddSection("Update Fields").
		AddTextField("email", "Email",
			resolver.WithPlaceholder("newemail@example.com"),
		).
		AddTextField("firstName", "First Name",
			resolver.WithPlaceholder("Jane"),
		).
		AddTextField("lastName", "Last Name",
			resolver.WithPlaceholder("Smith"),
		).
		AddTextField("phone", "Phone",
			resolver.WithPlaceholder("+1-555-987-6543"),
		).
		AddTextField("company", "Company",
			resolver.WithPlaceholder("New Corp"),
		).
		EndSection().
	AddSection("Additional Properties").
		AddJSONField("properties", "Custom Properties",
			resolver.WithHeight(100),
		).
		EndSection().
	Build()

func (e *HSContactUpdateExecutor) Type() string { return "hs-contact-update" }

func (e *HSContactUpdateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg HSContactUpdateConfig
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

	if cfg.ContactID == "" {
		return nil, fmt.Errorf("contactId is required")
	}

	// Build properties to update
	properties := map[string]interface{}{}
	if cfg.Email != "" {
		properties["email"] = cfg.Email
	}
	if cfg.FirstName != "" {
		properties["firstname"] = cfg.FirstName
	}
	if cfg.LastName != "" {
		properties["lastname"] = cfg.LastName
	}
	if cfg.Phone != "" {
		properties["phone"] = cfg.Phone
	}
	if cfg.Company != "" {
		properties["company"] = cfg.Company
	}
	for k, v := range cfg.Properties {
		properties[k] = v
	}

	if len(properties) == 0 {
		return nil, fmt.Errorf("at least one field to update is required")
	}

	reqBody := map[string]interface{}{
		"properties": properties,
	}

	// Determine if contactId is an email or ID
	endpoint := "/crm/v3/objects/contacts/" + cfg.ContactID
	if strings.Contains(cfg.ContactID, "@") {
		endpoint = "/crm/v3/objects/contacts/" + cfg.ContactID
	}

	respBody, err := makeHubSpotRequest(ctx, cfg.APIKey, "PATCH", endpoint, reqBody)
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"id":         result["id"],
			"properties": result["properties"],
			"updatedAt":  result["updatedAt"],
		},
	}, nil
}

// ============================================================================
// HSContactSearchExecutor
// ============================================================================

// HSContactSearchExecutor handles hs-contact-search node type
type HSContactSearchExecutor struct{}

// HSContactSearchConfig defines the typed configuration for hs-contact-search
type HSContactSearchConfig struct {
	APIKey     string   `json:"apiKey" description:"HubSpot API key or access token"`
	Query      string   `json:"query" description:"Search query string"`
	Properties []string `json:"properties" description:"Properties to return"`
	Limit      int      `json:"limit" default:"100" description:"Maximum number of results"`
}

// HSContactSearchSchema is the UI schema for hs-contact-search
var HSContactSearchSchema = resolver.NewSchemaBuilder("hs-contact-search").
	WithName("Search HubSpot Contacts").
	WithCategory("crm").
	WithIcon("search").
	WithDescription("Search for contacts in HubSpot CRM").
	AddSection("Authentication").
		AddExpressionField("apiKey", "API Key",
			resolver.WithRequired(),
		).
		EndSection().
	AddSection("Search").
		AddTextField("query", "Search Query",
			resolver.WithRequired(),
			resolver.WithPlaceholder("john@example.com"),
			resolver.WithHint("Search by email, name, or other properties"),
		).
		AddTagsField("properties", "Properties to Return",
			resolver.WithHint("email, firstname, lastname, company, etc."),
		).
		AddNumberField("limit", "Limit",
			resolver.WithDefault(100),
			resolver.WithMinMax(1, 1000),
		).
		EndSection().
	Build()

func (e *HSContactSearchExecutor) Type() string { return "hs-contact-search" }

func (e *HSContactSearchExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg HSContactSearchConfig
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

	if cfg.Query == "" {
		return nil, fmt.Errorf("query is required")
	}

	if cfg.Limit <= 0 {
		cfg.Limit = 100
	}

	// Build search request
	searchRequest := map[string]interface{}{
		"query": cfg.Query,
		"limit": cfg.Limit,
	}

	if len(cfg.Properties) > 0 {
		searchRequest["properties"] = cfg.Properties
	}

	jsonData, err := json.Marshal(searchRequest)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal search request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", hubspotAPIBase+"/crm/v3/objects/contacts/search", bytes.NewReader(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("hubspot API error (%d): %s", resp.StatusCode, string(respBody))
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	results, _ := result["results"].([]interface{})

	return &executor.StepResult{
		Output: map[string]interface{}{
			"contacts": results,
			"count":    len(results),
			"total":    result["total"],
		},
	}, nil
}

// ============================================================================
// HSDealCreateExecutor
// ============================================================================

// HSDealCreateExecutor handles hs-deal-create node type
type HSDealCreateExecutor struct{}

// HSDealCreateConfig defines the typed configuration for hs-deal-create
type HSDealCreateConfig struct {
	APIKey       string                 `json:"apiKey" description:"HubSpot API key or access token"`
	DealName     string                 `json:"dealName" description:"Name of the deal"`
	Amount       float64                `json:"amount" description:"Deal amount"`
	Currency     string                 `json:"currency" default:"USD" description:"Currency code"`
	Stage        string                 `json:"stage" description:"Deal stage (e.g., appointmentscheduled, qualifiedtobuy)"`
	CloseDate    string                 `json:"closeDate" description:"Expected close date (YYYY-MM-DD)"`
	CompanyID    string                 `json:"companyId" description:"Associated company ID"`
	ContactID    string                 `json:"contactId" description:"Associated contact ID"`
	DealPipeline string                 `json:"dealPipeline" description:"Deal pipeline ID"`
	Properties   map[string]interface{} `json:"properties" description:"Additional deal properties"`
}

// HSDealCreateSchema is the UI schema for hs-deal-create
var HSDealCreateSchema = resolver.NewSchemaBuilder("hs-deal-create").
	WithName("Create HubSpot Deal").
	WithCategory("crm").
	WithIcon("dollar-sign").
	WithDescription("Create a new deal in HubSpot CRM").
	AddSection("Authentication").
		AddExpressionField("apiKey", "API Key",
			resolver.WithRequired(),
		).
		EndSection().
	AddSection("Deal Information").
		AddTextField("dealName", "Deal Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("Enterprise License Deal"),
		).
		AddNumberField("amount", "Amount",
			resolver.WithPlaceholder("10000"),
			resolver.WithHint("Deal amount in cents"),
		).
		AddTextField("currency", "Currency",
			resolver.WithDefault("USD"),
			resolver.WithPlaceholder("USD"),
		).
		AddTextField("stage", "Deal Stage",
			resolver.WithPlaceholder("appointmentscheduled"),
			resolver.WithHint("e.g., appointmentscheduled, qualifiedtobuy, closedwon"),
		).
		AddTextField("closeDate", "Close Date",
			resolver.WithPlaceholder("2026-12-31"),
		).
		EndSection().
	AddSection("Associations").
		AddTextField("companyId", "Company ID",
			resolver.WithPlaceholder("12345"),
		).
		AddTextField("contactId", "Contact ID",
			resolver.WithPlaceholder("67890"),
		).
		AddTextField("dealPipeline", "Pipeline ID",
			resolver.WithPlaceholder("default"),
		).
		EndSection().
	AddSection("Additional Properties").
		AddJSONField("properties", "Custom Properties",
			resolver.WithHeight(100),
		).
		EndSection().
	Build()

func (e *HSDealCreateExecutor) Type() string { return "hs-deal-create" }

func (e *HSDealCreateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg HSDealCreateConfig
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

	if cfg.DealName == "" {
		return nil, fmt.Errorf("dealName is required")
	}

	// Build properties
	properties := map[string]interface{}{
		"dealname": cfg.DealName,
	}
	if cfg.Amount > 0 {
		properties["amount"] = fmt.Sprintf("%.0f", cfg.Amount)
	}
	if cfg.Currency != "" {
		properties["currency"] = cfg.Currency
	}
	if cfg.Stage != "" {
		properties["dealstage"] = cfg.Stage
	}
	if cfg.CloseDate != "" {
		properties["closedate"] = cfg.CloseDate
	}
	if cfg.DealPipeline != "" {
		properties["pipeline"] = cfg.DealPipeline
	}
	for k, v := range cfg.Properties {
		properties[k] = v
	}

	reqBody := map[string]interface{}{
		"properties": properties,
	}

	respBody, err := makeHubSpotRequest(ctx, cfg.APIKey, "POST", "/crm/v3/objects/deals", reqBody)
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Create associations if provided
	if cfg.CompanyID != "" || cfg.ContactID != "" {
		associations := []map[string]interface{}{}
		if cfg.CompanyID != "" {
			associations = append(associations, map[string]interface{}{
				"to": map[string]string{"id": cfg.CompanyID},
				"types": []map[string]string{
					{"associationCategory": "HUBSPOT_DEFINED", "associationTypeId": "13"},
				},
			})
		}
		if cfg.ContactID != "" {
			associations = append(associations, map[string]interface{}{
				"to": map[string]string{"id": cfg.ContactID},
				"types": []map[string]string{
					{"associationCategory": "HUBSPOT_DEFINED", "associationTypeId": "14"},
				},
			})
		}

		assocBody := map[string]interface{}{"inputs": associations}
		dealID := result["id"].(string)
		_, err := makeHubSpotRequest(ctx, cfg.APIKey, "POST", "/crm/v3/objects/deals/"+dealID+"/associations", assocBody)
		if err != nil {
			// Log warning but don't fail the deal creation
			fmt.Fprintf(os.Stderr, "Warning: failed to create associations: %v\n", err)
		}
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"id":         result["id"],
			"properties": result["properties"],
			"createdAt":  result["createdAt"],
			"updatedAt":  result["updatedAt"],
		},
	}, nil
}

// ============================================================================
// HSDealUpdateExecutor
// ============================================================================

// HSDealUpdateExecutor handles hs-deal-update node type
type HSDealUpdateExecutor struct{}

// HSDealUpdateConfig defines the typed configuration for hs-deal-update
type HSDealUpdateConfig struct {
	APIKey       string                 `json:"apiKey" description:"HubSpot API key or access token"`
	DealID       string                 `json:"dealId" description:"Deal ID to update"`
	DealName     string                 `json:"dealName" description:"New deal name"`
	Amount       float64                `json:"amount" description:"New deal amount"`
	Stage        string                 `json:"stage" description:"New deal stage"`
	CloseDate    string                 `json:"closeDate" description:"New close date (YYYY-MM-DD)"`
	Properties   map[string]interface{} `json:"properties" description:"Additional deal properties to update"`
}

// HSDealUpdateSchema is the UI schema for hs-deal-update
var HSDealUpdateSchema = resolver.NewSchemaBuilder("hs-deal-update").
	WithName("Update HubSpot Deal").
	WithCategory("crm").
	WithIcon("edit-3").
	WithDescription("Update an existing deal in HubSpot CRM").
	AddSection("Authentication").
		AddExpressionField("apiKey", "API Key",
			resolver.WithRequired(),
		).
		EndSection().
	AddSection("Deal Identification").
		AddTextField("dealId", "Deal ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("12345"),
		).
		EndSection().
	AddSection("Update Fields").
		AddTextField("dealName", "Deal Name",
			resolver.WithPlaceholder("Updated Deal Name"),
		).
		AddNumberField("amount", "Amount",
			resolver.WithPlaceholder("15000"),
		).
		AddTextField("stage", "Deal Stage",
			resolver.WithPlaceholder("closedwon"),
		).
		AddTextField("closeDate", "Close Date",
			resolver.WithPlaceholder("2026-06-30"),
		).
		EndSection().
	AddSection("Additional Properties").
		AddJSONField("properties", "Custom Properties",
			resolver.WithHeight(100),
		).
		EndSection().
	Build()

func (e *HSDealUpdateExecutor) Type() string { return "hs-deal-update" }

func (e *HSDealUpdateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg HSDealUpdateConfig
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

	if cfg.DealID == "" {
		return nil, fmt.Errorf("dealId is required")
	}

	// Build properties to update
	properties := map[string]interface{}{}
	if cfg.DealName != "" {
		properties["dealname"] = cfg.DealName
	}
	if cfg.Amount > 0 {
		properties["amount"] = fmt.Sprintf("%.0f", cfg.Amount)
	}
	if cfg.Stage != "" {
		properties["dealstage"] = cfg.Stage
	}
	if cfg.CloseDate != "" {
		properties["closedate"] = cfg.CloseDate
	}
	for k, v := range cfg.Properties {
		properties[k] = v
	}

	if len(properties) == 0 {
		return nil, fmt.Errorf("at least one field to update is required")
	}

	reqBody := map[string]interface{}{
		"properties": properties,
	}

	endpoint := "/crm/v3/objects/deals/" + cfg.DealID
	respBody, err := makeHubSpotRequest(ctx, cfg.APIKey, "PATCH", endpoint, reqBody)
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"id":         result["id"],
			"properties": result["properties"],
			"updatedAt":  result["updatedAt"],
		},
	}, nil
}

// ============================================================================
// HSCompanyCreateExecutor
// ============================================================================

// HSCompanyCreateExecutor handles hs-company-create node type
type HSCompanyCreateExecutor struct{}

// HSCompanyCreateConfig defines the typed configuration for hs-company-create
type HSCompanyCreateConfig struct {
	APIKey       string                 `json:"apiKey" description:"HubSpot API key or access token"`
	Name         string                 `json:"name" description:"Company name"`
	Domain       string                 `json:"domain" description:"Company website domain"`
	Industry     string                 `json:"industry" description:"Company industry"`
	AnnualRevenue int64                 `json:"annualRevenue" description:"Company annual revenue"`
	NumberOfEmployees int               `json:"numberOfEmployees" description:"Number of employees"`
	Phone        string                 `json:"phone" description:"Company phone number"`
	Address      string                 `json:"address" description:"Company street address"`
	City         string                 `json:"city" description:"Company city"`
	State        string                 `json:"state" description:"Company state"`
	Zip          string                 `json:"zip" description:"Company zip code"`
	Country      string                 `json:"country" description:"Company country"`
	Properties   map[string]interface{} `json:"properties" description:"Additional company properties"`
}

// HSCompanyCreateSchema is the UI schema for hs-company-create
var HSCompanyCreateSchema = resolver.NewSchemaBuilder("hs-company-create").
	WithName("Create HubSpot Company").
	WithCategory("crm").
	WithIcon("building").
	WithDescription("Create a new company in HubSpot CRM").
	AddSection("Authentication").
		AddExpressionField("apiKey", "API Key",
			resolver.WithRequired(),
		).
		EndSection().
	AddSection("Company Information").
		AddTextField("name", "Company Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("Acme Corporation"),
		).
		AddTextField("domain", "Domain",
			resolver.WithPlaceholder("acme.com"),
			resolver.WithHint("Company website domain"),
		).
		AddTextField("industry", "Industry",
			resolver.WithPlaceholder("Technology"),
		).
		AddNumberField("annualRevenue", "Annual Revenue",
			resolver.WithPlaceholder("10000000"),
		).
		AddNumberField("numberOfEmployees", "Number of Employees",
			resolver.WithPlaceholder("500"),
		).
		EndSection().
	AddSection("Contact Information").
		AddTextField("phone", "Phone",
			resolver.WithPlaceholder("+1-555-123-4567"),
		).
		AddTextField("address", "Address",
			resolver.WithPlaceholder("123 Main St"),
		).
		AddTextField("city", "City",
			resolver.WithPlaceholder("San Francisco"),
		).
		AddTextField("state", "State",
			resolver.WithPlaceholder("CA"),
		).
		AddTextField("zip", "Zip Code",
			resolver.WithPlaceholder("94105"),
		).
		AddTextField("country", "Country",
			resolver.WithPlaceholder("US"),
		).
		EndSection().
	AddSection("Additional Properties").
		AddJSONField("properties", "Custom Properties",
			resolver.WithHeight(100),
		).
		EndSection().
	Build()

func (e *HSCompanyCreateExecutor) Type() string { return "hs-company-create" }

func (e *HSCompanyCreateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg HSCompanyCreateConfig
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

	if cfg.Name == "" {
		return nil, fmt.Errorf("name is required")
	}

	// Build properties
	properties := map[string]interface{}{
		"name": cfg.Name,
	}
	if cfg.Domain != "" {
		properties["domain"] = cfg.Domain
	}
	if cfg.Industry != "" {
		properties["industry"] = cfg.Industry
	}
	if cfg.AnnualRevenue > 0 {
		properties["annualrevenue"] = cfg.AnnualRevenue
	}
	if cfg.NumberOfEmployees > 0 {
		properties["numberofemployees"] = cfg.NumberOfEmployees
	}
	if cfg.Phone != "" {
		properties["phone"] = cfg.Phone
	}
	if cfg.Address != "" {
		properties["address"] = cfg.Address
	}
	if cfg.City != "" {
		properties["city"] = cfg.City
	}
	if cfg.State != "" {
		properties["state"] = cfg.State
	}
	if cfg.Zip != "" {
		properties["zip"] = cfg.Zip
	}
	if cfg.Country != "" {
		properties["country"] = cfg.Country
	}
	for k, v := range cfg.Properties {
		properties[k] = v
	}

	reqBody := map[string]interface{}{
		"properties": properties,
	}

	respBody, err := makeHubSpotRequest(ctx, cfg.APIKey, "POST", "/crm/v3/objects/companies", reqBody)
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"id":         result["id"],
			"properties": result["properties"],
			"createdAt":  result["createdAt"],
			"updatedAt":  result["updatedAt"],
		},
	}, nil
}

// ============================================================================
// HSTicketCreateExecutor
// ============================================================================

// HSTicketCreateExecutor handles hs-ticket-create node type
type HSTicketCreateExecutor struct{}

// HSTicketCreateConfig defines the typed configuration for hs-ticket-create
type HSTicketCreateConfig struct {
	APIKey      string                 `json:"apiKey" description:"HubSpot API key or access token"`
	Subject     string                 `json:"subject" description:"Ticket subject"`
	Content     string                 `json:"content" description:"Ticket description/content"`
	Status      string                 `json:"status" default:"NEW" description:"Ticket status (NEW, IN_PROGRESS, CLOSED)"`
	Priority    string                 `json:"priority" default:"MEDIUM" description:"Ticket priority (LOW, MEDIUM, HIGH, URGENT)"`
	ContactID   string                 `json:"contactId" description:"Associated contact ID"`
	CompanyID   string                 `json:"companyId" description:"Associated company ID"`
	OwnerID     string                 `json:"ownerId" description:"Assigned owner ID"`
	Category    string                 `json:"category" description:"Ticket category"`
	Properties  map[string]interface{} `json:"properties" description:"Additional ticket properties"`
}

// HSTicketCreateSchema is the UI schema for hs-ticket-create
var HSTicketCreateSchema = resolver.NewSchemaBuilder("hs-ticket-create").
	WithName("Create HubSpot Ticket").
	WithCategory("crm").
	WithIcon("alert-circle").
	WithDescription("Create a new support ticket in HubSpot CRM").
	AddSection("Authentication").
		AddExpressionField("apiKey", "API Key",
			resolver.WithRequired(),
		).
		EndSection().
	AddSection("Ticket Information").
		AddTextField("subject", "Subject",
			resolver.WithRequired(),
			resolver.WithPlaceholder("Unable to access account"),
		).
		AddTextareaField("content", "Description",
			resolver.WithRequired(),
			resolver.WithRows(4),
			resolver.WithPlaceholder("Customer is unable to log in..."),
		).
		AddSelectField("status", "Status", []resolver.SelectOption{
			{Label: "New", Value: "NEW"},
			{Label: "In Progress", Value: "IN_PROGRESS"},
			{Label: "Closed", Value: "CLOSED"},
		}, resolver.WithDefault("NEW")).
		AddSelectField("priority", "Priority", []resolver.SelectOption{
			{Label: "Low", Value: "LOW"},
			{Label: "Medium", Value: "MEDIUM"},
			{Label: "High", Value: "HIGH"},
			{Label: "Urgent", Value: "URGENT"},
		}, resolver.WithDefault("MEDIUM")).
		EndSection().
	AddSection("Associations").
		AddTextField("contactId", "Contact ID",
			resolver.WithPlaceholder("12345"),
		).
		AddTextField("companyId", "Company ID",
			resolver.WithPlaceholder("67890"),
		).
		AddTextField("ownerId", "Owner ID",
			resolver.WithPlaceholder("1"),
		).
		AddTextField("category", "Category",
			resolver.WithPlaceholder("TECHNICAL_SUPPORT"),
		).
		EndSection().
	AddSection("Additional Properties").
		AddJSONField("properties", "Custom Properties",
			resolver.WithHeight(100),
		).
		EndSection().
	Build()

func (e *HSTicketCreateExecutor) Type() string { return "hs-ticket-create" }

func (e *HSTicketCreateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg HSTicketCreateConfig
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

	if cfg.Subject == "" {
		return nil, fmt.Errorf("subject is required")
	}

	if cfg.Content == "" {
		return nil, fmt.Errorf("content is required")
	}

	// Build properties
	properties := map[string]interface{}{
		"subject": cfg.Subject,
		"content": cfg.Content,
	}
	if cfg.Status != "" {
		properties["hs_ticket_status"] = cfg.Status
	}
	if cfg.Priority != "" {
		properties["hs_ticket_priority"] = cfg.Priority
	}
	if cfg.Category != "" {
		properties["hs_ticket_category"] = cfg.Category
	}
	for k, v := range cfg.Properties {
		properties[k] = v
	}

	reqBody := map[string]interface{}{
		"properties": properties,
	}

	respBody, err := makeHubSpotRequest(ctx, cfg.APIKey, "POST", "/crm/v3/objects/tickets", reqBody)
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Create associations if provided
	if cfg.ContactID != "" || cfg.CompanyID != "" || cfg.OwnerID != "" {
		associations := []map[string]interface{}{}
		if cfg.ContactID != "" {
			associations = append(associations, map[string]interface{}{
				"to": map[string]string{"id": cfg.ContactID},
				"types": []map[string]string{
					{"associationCategory": "HUBSPOT_DEFINED", "associationTypeId": "16"},
				},
			})
		}
		if cfg.CompanyID != "" {
			associations = append(associations, map[string]interface{}{
				"to": map[string]string{"id": cfg.CompanyID},
				"types": []map[string]string{
					{"associationCategory": "HUBSPOT_DEFINED", "associationTypeId": "17"},
				},
			})
		}
		if cfg.OwnerID != "" {
			associations = append(associations, map[string]interface{}{
				"to": map[string]string{"id": cfg.OwnerID},
				"types": []map[string]string{
					{"associationCategory": "HUBSPOT_DEFINED", "associationTypeId": "2"},
				},
			})
		}

		assocBody := map[string]interface{}{"inputs": associations}
		ticketID := result["id"].(string)
		_, err := makeHubSpotRequest(ctx, cfg.APIKey, "POST", "/crm/v3/objects/tickets/"+ticketID+"/associations", assocBody)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to create associations: %v\n", err)
		}
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"id":         result["id"],
			"properties": result["properties"],
			"createdAt":  result["createdAt"],
			"updatedAt":  result["updatedAt"],
		},
	}, nil
}

// ============================================================================
// HSEngagementExecutor
// ============================================================================

// HSEngagementExecutor handles hs-engagement node type
type HSEngagementExecutor struct{}

// HSEngagementConfig defines the typed configuration for hs-engagement
type HSEngagementConfig struct {
	APIKey        string                 `json:"apiKey" description:"HubSpot API key or access token"`
	EngagementType string                `json:"engagementType" default:"NOTE" options:"NOTE:NOTE,CALL:CALL,EMAIL:EMAIL,TASK:TASK,MEETING:MEETING" description:"Type of engagement"`
	Subject       string                 `json:"subject" description:"Engagement subject/title"`
	Body          string                 `json:"body" description:"Engagement body/content"`
	ContactID     string                 `json:"contactId" description:"Associated contact ID"`
	CompanyID     string                 `json:"companyId" description:"Associated company ID"`
	DealID        string                 `json:"dealId" description:"Associated deal ID"`
	TicketID      string                 `json:"ticketId" description:"Associated ticket ID"`
	OwnerID       string                 `json:"ownerId" description:"Owner ID"`
	Status        string                 `json:"status" description:"Engagement status (for tasks)"`
	DueDate       string                 `json:"dueDate" description:"Due date for tasks/meetings"`
	Properties    map[string]interface{} `json:"properties" description:"Additional engagement properties"`
}

// HSEngagementSchema is the UI schema for hs-engagement
var HSEngagementSchema = resolver.NewSchemaBuilder("hs-engagement").
	WithName("Create HubSpot Engagement").
	WithCategory("crm").
	WithIcon("activity").
	WithDescription("Create engagements (notes, calls, emails, tasks, meetings) in HubSpot").
	AddSection("Authentication").
		AddExpressionField("apiKey", "API Key",
			resolver.WithRequired(),
		).
		EndSection().
	AddSection("Engagement Details").
		AddSelectField("engagementType", "Type", []resolver.SelectOption{
			{Label: "Note", Value: "NOTE", Icon: "file-text"},
			{Label: "Call", Value: "CALL", Icon: "phone"},
			{Label: "Email", Value: "EMAIL", Icon: "mail"},
			{Label: "Task", Value: "TASK", Icon: "check-square"},
			{Label: "Meeting", Value: "MEETING", Icon: "calendar"},
		}, resolver.WithDefault("NOTE")).
		AddTextField("subject", "Subject",
			resolver.WithPlaceholder("Follow-up call"),
		).
		AddTextareaField("body", "Body/Content",
			resolver.WithRequired(),
			resolver.WithRows(4),
			resolver.WithPlaceholder("Discussed product features and pricing..."),
		).
		EndSection().
	AddSection("Associations").
		AddTextField("contactId", "Contact ID",
			resolver.WithPlaceholder("12345"),
		).
		AddTextField("companyId", "Company ID",
			resolver.WithPlaceholder("67890"),
		).
		AddTextField("dealId", "Deal ID",
			resolver.WithPlaceholder("11111"),
		).
		AddTextField("ticketId", "Ticket ID",
			resolver.WithPlaceholder("22222"),
		).
		AddTextField("ownerId", "Owner ID",
			resolver.WithPlaceholder("1"),
		).
		EndSection().
	AddSection("Task/Meeting Options").
		AddTextField("status", "Status",
			resolver.WithPlaceholder("NOT_STARTED"),
			resolver.WithHint("For tasks: NOT_STARTED, IN_PROGRESS, COMPLETED"),
		).
		AddTextField("dueDate", "Due Date",
			resolver.WithPlaceholder("2026-04-01"),
			resolver.WithHint("For tasks and meetings"),
		).
		EndSection().
	AddSection("Additional Properties").
		AddJSONField("properties", "Custom Properties",
			resolver.WithHeight(100),
		).
		EndSection().
	Build()

func (e *HSEngagementExecutor) Type() string { return "hs-engagement" }

func (e *HSEngagementExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg HSEngagementConfig
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

	if cfg.EngagementType == "" {
		cfg.EngagementType = "NOTE"
	}

	if cfg.Body == "" {
		return nil, fmt.Errorf("body is required")
	}

	// Build engagement properties based on type
	var endpoint string
	var reqBody map[string]interface{}

	switch cfg.EngagementType {
	case "NOTE":
		endpoint = "/crm/v3/objects/notes"
		properties := map[string]interface{}{
			"hs_note_body": cfg.Body,
		}
		if cfg.Subject != "" {
			properties["hs_note_subject"] = cfg.Subject
		}
		reqBody = map[string]interface{}{"properties": properties}

	case "CALL":
		endpoint = "/crm/v3/objects/calls"
		properties := map[string]interface{}{
			"hs_call_body": cfg.Body,
		}
		if cfg.Subject != "" {
			properties["hs_call_title"] = cfg.Subject
		}
		if cfg.Status != "" {
			properties["hs_call_status"] = cfg.Status
		}
		reqBody = map[string]interface{}{"properties": properties}

	case "EMAIL":
		endpoint = "/crm/v3/objects/emails"
		properties := map[string]interface{}{
			"hs_email_body":    cfg.Body,
			"hs_email_direction": "OUTBOUND_EMAIL",
		}
		if cfg.Subject != "" {
			properties["hs_email_subject"] = cfg.Subject
		}
		reqBody = map[string]interface{}{"properties": properties}

	case "TASK":
		endpoint = "/crm/v3/objects/tasks"
		properties := map[string]interface{}{
			"hs_task_body": cfg.Body,
		}
		if cfg.Subject != "" {
			properties["hs_task_subject"] = cfg.Subject
		}
		if cfg.Status != "" {
			properties["hs_task_status"] = cfg.Status
		}
		if cfg.DueDate != "" {
			properties["hs_task_due_date"] = cfg.DueDate
		}
		reqBody = map[string]interface{}{"properties": properties}

	case "MEETING":
		endpoint = "/crm/v3/objects/meetings"
		properties := map[string]interface{}{
			"hs_meeting_body": cfg.Body,
		}
		if cfg.Subject != "" {
			properties["hs_meeting_title"] = cfg.Subject
		}
		if cfg.DueDate != "" {
			properties["hs_meeting_start_time"] = cfg.DueDate
			properties["hs_meeting_end_time"] = cfg.DueDate
		}
		reqBody = map[string]interface{}{"properties": properties}

	default:
		return nil, fmt.Errorf("unsupported engagement type: %s", cfg.EngagementType)
	}

	// Add custom properties
	for k, v := range cfg.Properties {
		if reqBody["properties"] == nil {
			reqBody["properties"] = make(map[string]interface{})
		}
		reqBody["properties"].(map[string]interface{})[k] = v
	}

	respBody, err := makeHubSpotRequest(ctx, cfg.APIKey, "POST", endpoint, reqBody)
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Create associations if provided
	assocTypeID := getAssociationTypeID(cfg.EngagementType)
	if (cfg.ContactID != "" || cfg.CompanyID != "" || cfg.DealID != "" || cfg.TicketID != "") && assocTypeID > 0 {
		associations := []map[string]interface{}{}
		if cfg.ContactID != "" {
			associations = append(associations, createAssociation(cfg.ContactID, "16")) // Contact association
		}
		if cfg.CompanyID != "" {
			associations = append(associations, createAssociation(cfg.CompanyID, "17")) // Company association
		}
		if cfg.DealID != "" {
			associations = append(associations, createAssociation(cfg.DealID, "19")) // Deal association
		}
		if cfg.TicketID != "" {
			associations = append(associations, createAssociation(cfg.TicketID, "20")) // Ticket association
		}

		assocBody := map[string]interface{}{"inputs": associations}
		objectID := result["id"].(string)
		_, err := makeHubSpotRequest(ctx, cfg.APIKey, "POST", endpoint+"/"+objectID+"/associations", assocBody)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to create associations: %v\n", err)
		}
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"id":         result["id"],
			"type":       cfg.EngagementType,
			"properties": result["properties"],
			"createdAt":  result["createdAt"],
			"updatedAt":  result["updatedAt"],
		},
	}, nil
}

func createAssociation(id, assocTypeID string) map[string]interface{} {
	return map[string]interface{}{
		"to": map[string]string{"id": id},
		"types": []map[string]string{
			{"associationCategory": "HUBSPOT_DEFINED", "associationTypeId": assocTypeID},
		},
	}
}

func getAssociationTypeID(engagementType string) int {
	// Returns a non-zero value if associations are supported
	// All engagement types support associations
	return 1
}
