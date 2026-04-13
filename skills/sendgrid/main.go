package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/axiom-studio/skills.sdk/executor"
	"github.com/axiom-studio/skills.sdk/grpc"
	"github.com/axiom-studio/skills.sdk/resolver"
)

func main() {
	// Get port from env or use default
	port := os.Getenv("SKILL_PORT")
	if port == "" {
		port = "50053"
	}

	// Create skill server
	server := grpc.NewSkillServer("skill-sendgrid", "1.0.0")

	// Register SendGrid executors with schemas
	server.RegisterExecutorWithSchema("sg-send", &SGSendExecutor{}, SGSendSchema)
	server.RegisterExecutorWithSchema("sg-template", &SGTemplateExecutor{}, SGTemplateSchema)
	server.RegisterExecutorWithSchema("sg-contact", &SGContactExecutor{}, SGContactSchema)
	server.RegisterExecutorWithSchema("sg-list", &SGListExecutor{}, SGListSchema)
	server.RegisterExecutorWithSchema("sg-campaign", &SGCampaignExecutor{}, SGCampaignSchema)

	fmt.Printf("Starting skill-sendgrid gRPC server on port %s\n", port)
	if err := server.Serve(port); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to serve: %v\n", err)
		os.Exit(1)
	}
}

// sendGridRequest makes an HTTP request to the SendGrid API
func sendGridRequest(ctx context.Context, apiKey, method, endpoint string, body interface{}) ([]byte, error) {
	var reqBody io.Reader
	if body != nil {
		jsonData, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
		reqBody = bytes.NewBuffer(jsonData)
	}

	req, err := http.NewRequestWithContext(ctx, method, "https://api.sendgrid.com/v3/"+endpoint, reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("sendgrid API error (%d): %s", resp.StatusCode, string(respBody))
	}

	return respBody, nil
}

// SGSendExecutor handles sg-send node type
type SGSendExecutor struct{}

// SGSendConfig defines the typed configuration for sg-send
type SGSendConfig struct {
	APIKey      string                 `json:"apiKey" description:"SendGrid API key, supports {{secrets.xxx}}"`
	FromEmail   string                 `json:"fromEmail" description:"Sender email address"`
	FromName    string                 `json:"fromName" description:"Sender name"`
	ToEmails    []string               `json:"toEmails" description:"Recipient email addresses"`
	CCEmails    []string               `json:"ccEmails" description:"CC email addresses"`
	BCEmails    []string               `json:"bccEmails" description:"BCC email addresses"`
	Subject     string                 `json:"subject" description:"Email subject"`
	Content     string                 `json:"content" description:"Email body content"`
	ContentType string                 `json:"contentType" default:"text/plain" options:"text/plain,text/html" description:"Content type"`
	TemplateID  string                 `json:"templateId" description:"Dynamic template ID (optional)"`
	TemplateData map[string]interface{} `json:"templateData" description:"Data for dynamic template substitution"`
}

// SGSendSchema is the UI schema for sg-send
var SGSendSchema = resolver.NewSchemaBuilder("sg-send").
	WithName("Send Email").
	WithCategory("sendgrid").
	WithIcon("mail").
	WithDescription("Send emails via SendGrid").
	AddSection("Authentication").
		AddExpressionField("apiKey", "API Key",
			resolver.WithRequired(),
			resolver.WithPlaceholder("SG.xxxxx"),
			resolver.WithHint("Supports {{secrets.sendgrid_api_key}} for secure storage"),
		).
		EndSection().
	AddSection("Sender").
		AddTextField("fromEmail", "From Email",
			resolver.WithRequired(),
			resolver.WithPlaceholder("sender@example.com"),
		).
		AddTextField("fromName", "From Name",
			resolver.WithDefault("Sender"),
			resolver.WithPlaceholder("Your Name"),
		).
		EndSection().
	AddSection("Recipients").
		AddTagsField("toEmails", "To Emails",
			resolver.WithRequired(),
			resolver.WithHint("One or more recipient email addresses"),
		).
		AddTagsField("ccEmails", "CC Emails").
		AddTagsField("bccEmails", "BCC Emails").
		EndSection().
	AddSection("Content").
		AddTextField("subject", "Subject",
			resolver.WithRequired(),
			resolver.WithPlaceholder("Email subject"),
		).
		AddSelectField("contentType", "Content Type", []resolver.SelectOption{
			{Label: "Plain Text", Value: "text/plain"},
			{Label: "HTML", Value: "text/html"},
		}, resolver.WithDefault("text/plain")).
		AddTextareaField("content", "Content",
			resolver.WithRequired(),
			resolver.WithRows(10),
			resolver.WithHint("Email body content"),
		).
		EndSection().
	AddSection("Template (Optional)").
		AddTextField("templateId", "Template ID",
			resolver.WithPlaceholder("d-xxxxx"),
			resolver.WithHint("Dynamic template ID from SendGrid"),
		).
		AddJSONField("templateData", "Template Data",
			resolver.WithHint("Data for dynamic template substitution"),
		).
		EndSection().
	Build()

func (e *SGSendExecutor) Type() string { return "sg-send" }

func (e *SGSendExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	// Parse config into typed struct
	var cfg SGSendConfig
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

	if cfg.FromEmail == "" {
		return nil, fmt.Errorf("fromEmail is required")
	}

	if len(cfg.ToEmails) == 0 {
		return nil, fmt.Errorf("toEmails is required")
	}

	// Build personalizations
	personalization := map[string]interface{}{
		"to": make([]map[string]string, 0, len(cfg.ToEmails)),
	}

	for _, email := range cfg.ToEmails {
		personalization["to"] = append(personalization["to"].([]map[string]string), map[string]string{"email": email})
	}

	if len(cfg.CCEmails) > 0 {
		ccList := make([]map[string]string, 0, len(cfg.CCEmails))
		for _, email := range cfg.CCEmails {
			ccList = append(ccList, map[string]string{"email": email})
		}
		personalization["cc"] = ccList
	}

	if len(cfg.BCEmails) > 0 {
		bccList := make([]map[string]string, 0, len(cfg.BCEmails))
		for _, email := range cfg.BCEmails {
			bccList = append(bccList, map[string]string{"email": email})
		}
		personalization["bcc"] = bccList
	}

	if cfg.TemplateID != "" && len(cfg.TemplateData) > 0 {
		personalization["dynamic_template_data"] = cfg.TemplateData
	}

	// Build request body
	body := map[string]interface{}{
		"personalizations": []map[string]interface{}{personalization},
		"from": map[string]string{
			"email": cfg.FromEmail,
			"name":  cfg.FromName,
		},
		"subject": cfg.Subject,
	}

	if cfg.TemplateID != "" {
		body["template_id"] = cfg.TemplateID
	} else {
		body["content"] = []map[string]interface{}{
			{
				"type":  cfg.ContentType,
				"value": cfg.Content,
			},
		}
	}

	// Send request
	respBody, err := sendGridRequest(ctx, cfg.APIKey, "POST", "mail/send", body)
	if err != nil {
		return nil, fmt.Errorf("failed to send email: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success": true,
			"message": "Email sent successfully",
			"to":      cfg.ToEmails,
			"subject": cfg.Subject,
			"response": string(respBody),
		},
	}, nil
}

// SGTemplateExecutor handles sg-template node type
type SGTemplateExecutor struct{}

// SGTemplateConfig defines the typed configuration for sg-template
type SGTemplateConfig struct {
	APIKey     string `json:"apiKey" description:"SendGrid API key"`
	Action     string `json:"action" options:"list,get,create,update,delete" description:"Template action to perform"`
	TemplateID string `json:"templateId" description:"Template ID for get/update/delete"`
	Name       string `json:"name" description:"Template name for create/update"`
	Content    string `json:"content" description:"Template HTML content for create/update"`
	Subject    string `json:"subject" description:"Template subject for create/update"`
}

// SGTemplateSchema is the UI schema for sg-template
var SGTemplateSchema = resolver.NewSchemaBuilder("sg-template").
	WithName("Manage Templates").
	WithCategory("sendgrid").
	WithIcon("file-text").
	WithDescription("Manage SendGrid email templates").
	AddSection("Authentication").
		AddExpressionField("apiKey", "API Key",
			resolver.WithRequired(),
			resolver.WithHint("Supports {{secrets.sendgrid_api_key}}"),
		).
		EndSection().
	AddSection("Action").
		AddSelectField("action", "Action", []resolver.SelectOption{
			{Label: "List Templates", Value: "list", Icon: "list"},
			{Label: "Get Template", Value: "get", Icon: "eye"},
			{Label: "Create Template", Value: "create", Icon: "plus"},
			{Label: "Update Template", Value: "update", Icon: "edit"},
			{Label: "Delete Template", Value: "delete", Icon: "trash"},
		}, resolver.WithRequired()).
		EndSection().
	AddSection("Template Details").
		AddTextField("templateId", "Template ID",
			resolver.WithPlaceholder("d-xxxxx"),
			resolver.WithHint("Required for get/update/delete actions"),
		).
		AddTextField("name", "Template Name",
			resolver.WithPlaceholder("My Template"),
			resolver.WithHint("Required for create/update actions"),
		).
		AddTextField("subject", "Subject",
			resolver.WithPlaceholder("Email subject"),
			resolver.WithHint("For create/update actions"),
		).
		AddCodeField("content", "HTML Content", "html",
			resolver.WithHeight(200),
			resolver.WithHint("Template HTML content for create/update"),
		).
		EndSection().
	Build()

func (e *SGTemplateExecutor) Type() string { return "sg-template" }

func (e *SGTemplateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg SGTemplateConfig
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

	if cfg.Action == "" {
		return nil, fmt.Errorf("action is required")
	}

	var respBody []byte
	var err error

	switch cfg.Action {
	case "list":
		respBody, err = sendGridRequest(ctx, cfg.APIKey, "GET", "templates?limit=100", nil)
		if err != nil {
			return nil, fmt.Errorf("failed to list templates: %w", err)
		}

	case "get":
		if cfg.TemplateID == "" {
			return nil, fmt.Errorf("templateId is required for get action")
		}
		respBody, err = sendGridRequest(ctx, cfg.APIKey, "GET", "templates/"+cfg.TemplateID, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to get template: %w", err)
		}

	case "create":
		if cfg.Name == "" {
			return nil, fmt.Errorf("name is required for create action")
		}
		body := map[string]interface{}{
			"name": cfg.Name,
		}
		respBody, err = sendGridRequest(ctx, cfg.APIKey, "POST", "templates", body)
		if err != nil {
			return nil, fmt.Errorf("failed to create template: %w", err)
		}

	case "update":
		if cfg.TemplateID == "" || cfg.Name == "" {
			return nil, fmt.Errorf("templateId and name are required for update action")
		}
		body := map[string]interface{}{
			"name": cfg.Name,
		}
		if cfg.Subject != "" {
			body["subject"] = cfg.Subject
		}
		if cfg.Content != "" {
			body["content"] = []map[string]interface{}{
				{
					"type":  "text/html",
					"value": cfg.Content,
				},
			}
		}
		respBody, err = sendGridRequest(ctx, cfg.APIKey, "PATCH", "templates/"+cfg.TemplateID, body)
		if err != nil {
			return nil, fmt.Errorf("failed to update template: %w", err)
		}

	case "delete":
		if cfg.TemplateID == "" {
			return nil, fmt.Errorf("templateId is required for delete action")
		}
		respBody, err = sendGridRequest(ctx, cfg.APIKey, "DELETE", "templates/"+cfg.TemplateID, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to delete template: %w", err)
		}

	default:
		return nil, fmt.Errorf("invalid action: %s", cfg.Action)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":  true,
			"action":   cfg.Action,
			"response": result,
		},
	}, nil
}

// SGContactExecutor handles sg-contact node type
type SGContactExecutor struct{}

// SGContactConfig defines the typed configuration for sg-contact
type SGContactConfig struct {
	APIKey      string                 `json:"apiKey" description:"SendGrid API key"`
	Action      string                 `json:"action" options:"list,get,create,update,delete,search" description:"Contact action to perform"`
	ContactID   string                 `json:"contactId" description:"Contact ID for get/update/delete"`
	Email       string                 `json:"email" description:"Contact email address"`
	FirstName   string                 `json:"firstName" description:"Contact first name"`
	LastName    string                `json:"lastName" description:"Contact last name"`
	Phone       string                 `json:"phone" description:"Contact phone number"`
	CustomFields map[string]interface{} `json:"customFields" description:"Custom field values"`
	Query       string                 `json:"query" description:"Search query for search action"`
}

// SGContactSchema is the UI schema for sg-contact
var SGContactSchema = resolver.NewSchemaBuilder("sg-contact").
	WithName("Manage Contacts").
	WithCategory("sendgrid").
	WithIcon("users").
	WithDescription("Manage SendGrid Marketing Campaigns contacts").
	AddSection("Authentication").
		AddExpressionField("apiKey", "API Key",
			resolver.WithRequired(),
			resolver.WithHint("Supports {{secrets.sendgrid_api_key}}"),
		).
		EndSection().
	AddSection("Action").
		AddSelectField("action", "Action", []resolver.SelectOption{
			{Label: "List Contacts", Value: "list", Icon: "list"},
			{Label: "Get Contact", Value: "get", Icon: "eye"},
			{Label: "Create Contact", Value: "create", Icon: "plus"},
			{Label: "Update Contact", Value: "update", Icon: "edit"},
			{Label: "Delete Contact", Value: "delete", Icon: "trash"},
			{Label: "Search Contacts", Value: "search", Icon: "search"},
		}, resolver.WithRequired()).
		EndSection().
	AddSection("Contact Details").
		AddTextField("contactId", "Contact ID",
			resolver.WithHint("Required for get/update/delete actions"),
		).
		AddTextField("email", "Email",
			resolver.WithPlaceholder("contact@example.com"),
			resolver.WithHint("Required for create action"),
		).
		AddTextField("firstName", "First Name").
		AddTextField("lastName", "Last Name").
		AddTextField("phone", "Phone Number").
		AddJSONField("customFields", "Custom Fields",
			resolver.WithHint("Additional custom field values"),
		).
		EndSection().
	AddSection("Search").
		AddTextField("query", "Search Query",
			resolver.WithPlaceholder("email LIKE '%example.com%'"),
			resolver.WithHint("SQL-like query for search action"),
		).
		EndSection().
	Build()

func (e *SGContactExecutor) Type() string { return "sg-contact" }

func (e *SGContactExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg SGContactConfig
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

	if cfg.Action == "" {
		return nil, fmt.Errorf("action is required")
	}

	var respBody []byte
	var err error

	switch cfg.Action {
	case "list":
		respBody, err = sendGridRequest(ctx, cfg.APIKey, "GET", "marketing/contacts?limit=100", nil)
		if err != nil {
			return nil, fmt.Errorf("failed to list contacts: %w", err)
		}

	case "get":
		if cfg.ContactID == "" {
			return nil, fmt.Errorf("contactId is required for get action")
		}
		respBody, err = sendGridRequest(ctx, cfg.APIKey, "GET", "marketing/contacts/"+cfg.ContactID, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to get contact: %w", err)
		}

	case "create":
		if cfg.Email == "" {
			return nil, fmt.Errorf("email is required for create action")
		}
		body := map[string]interface{}{
			"contacts": []map[string]interface{}{
				{
					"email": cfg.Email,
				},
			},
		}
		if cfg.FirstName != "" {
			body["contacts"].([]map[string]interface{})[0]["first_name"] = cfg.FirstName
		}
		if cfg.LastName != "" {
			body["contacts"].([]map[string]interface{})[0]["last_name"] = cfg.LastName
		}
		if cfg.Phone != "" {
			body["contacts"].([]map[string]interface{})[0]["phone_number"] = cfg.Phone
		}
		if len(cfg.CustomFields) > 0 {
			for k, v := range cfg.CustomFields {
				body["contacts"].([]map[string]interface{})[0] = map[string]interface{}{
					"email":         cfg.Email,
					"first_name":    cfg.FirstName,
					"last_name":     cfg.LastName,
					"phone_number":  cfg.Phone,
				}
				body["contacts"].([]map[string]interface{})[0][k] = v
			}
		}
		respBody, err = sendGridRequest(ctx, cfg.APIKey, "PUT", "marketing/contacts", body)
		if err != nil {
			return nil, fmt.Errorf("failed to create contact: %w", err)
		}

	case "update":
		if cfg.ContactID == "" {
			return nil, fmt.Errorf("contactId is required for update action")
		}
		body := map[string]interface{}{}
		if cfg.Email != "" {
			body["email"] = cfg.Email
		}
		if cfg.FirstName != "" {
			body["first_name"] = cfg.FirstName
		}
		if cfg.LastName != "" {
			body["last_name"] = cfg.LastName
		}
		if cfg.Phone != "" {
			body["phone_number"] = cfg.Phone
		}
		respBody, err = sendGridRequest(ctx, cfg.APIKey, "PATCH", "marketing/contacts/"+cfg.ContactID, body)
		if err != nil {
			return nil, fmt.Errorf("failed to update contact: %w", err)
		}

	case "delete":
		if cfg.ContactID == "" {
			return nil, fmt.Errorf("contactId is required for delete action")
		}
		body := map[string]interface{}{
			"contact_ids": []string{cfg.ContactID},
		}
		respBody, err = sendGridRequest(ctx, cfg.APIKey, "POST", "marketing/contacts/delete", body)
		if err != nil {
			return nil, fmt.Errorf("failed to delete contact: %w", err)
		}

	case "search":
		if cfg.Query == "" {
			return nil, fmt.Errorf("query is required for search action")
		}
		body := map[string]interface{}{
			"query": cfg.Query,
		}
		respBody, err = sendGridRequest(ctx, cfg.APIKey, "POST", "marketing/contacts/search", body)
		if err != nil {
			return nil, fmt.Errorf("failed to search contacts: %w", err)
		}

	default:
		return nil, fmt.Errorf("invalid action: %s", cfg.Action)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":  true,
			"action":   cfg.Action,
			"response": result,
		},
	}, nil
}

// SGListExecutor handles sg-list node type
type SGListExecutor struct{}

// SGListConfig defines the typed configuration for sg-list
type SGListConfig struct {
	APIKey   string `json:"apiKey" description:"SendGrid API key"`
	Action   string `json:"action" options:"list,get,create,update,delete" description:"List action to perform"`
	ListID   string `json:"listId" description:"List ID for get/update/delete"`
	Name     string `json:"name" description:"List name for create/update"`
	ContactIDs []string `json:"contactIds" description:"Contact IDs to add to list"`
}

// SGListSchema is the UI schema for sg-list
var SGListSchema = resolver.NewSchemaBuilder("sg-list").
	WithName("Manage Contact Lists").
	WithCategory("sendgrid").
	WithIcon("list").
	WithDescription("Manage SendGrid contact lists").
	AddSection("Authentication").
		AddExpressionField("apiKey", "API Key",
			resolver.WithRequired(),
			resolver.WithHint("Supports {{secrets.sendgrid_api_key}}"),
		).
		EndSection().
	AddSection("Action").
		AddSelectField("action", "Action", []resolver.SelectOption{
			{Label: "List All", Value: "list", Icon: "list"},
			{Label: "Get List", Value: "get", Icon: "eye"},
			{Label: "Create List", Value: "create", Icon: "plus"},
			{Label: "Update List", Value: "update", Icon: "edit"},
			{Label: "Delete List", Value: "delete", Icon: "trash"},
		}, resolver.WithRequired()).
		EndSection().
	AddSection("List Details").
		AddTextField("listId", "List ID",
			resolver.WithHint("Required for get/update/delete actions"),
		).
		AddTextField("name", "List Name",
			resolver.WithPlaceholder("My Contact List"),
			resolver.WithHint("Required for create/update actions"),
		).
		AddTagsField("contactIds", "Contact IDs",
			resolver.WithHint("Contact IDs to add to the list"),
		).
		EndSection().
	Build()

func (e *SGListExecutor) Type() string { return "sg-list" }

func (e *SGListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg SGListConfig
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

	if cfg.Action == "" {
		return nil, fmt.Errorf("action is required")
	}

	var respBody []byte
	var err error

	switch cfg.Action {
	case "list":
		respBody, err = sendGridRequest(ctx, cfg.APIKey, "GET", "marketing/lists?limit=100", nil)
		if err != nil {
			return nil, fmt.Errorf("failed to list contact lists: %w", err)
		}

	case "get":
		if cfg.ListID == "" {
			return nil, fmt.Errorf("listId is required for get action")
		}
		respBody, err = sendGridRequest(ctx, cfg.APIKey, "GET", "marketing/lists/"+cfg.ListID, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to get contact list: %w", err)
		}

	case "create":
		if cfg.Name == "" {
			return nil, fmt.Errorf("name is required for create action")
		}
		body := map[string]interface{}{
			"name": cfg.Name,
		}
		respBody, err = sendGridRequest(ctx, cfg.APIKey, "POST", "marketing/lists", body)
		if err != nil {
			return nil, fmt.Errorf("failed to create contact list: %w", err)
		}

	case "update":
		if cfg.ListID == "" || cfg.Name == "" {
			return nil, fmt.Errorf("listId and name are required for update action")
		}
		body := map[string]interface{}{
			"name": cfg.Name,
		}
		respBody, err = sendGridRequest(ctx, cfg.APIKey, "PUT", "marketing/lists/"+cfg.ListID, body)
		if err != nil {
			return nil, fmt.Errorf("failed to update contact list: %w", err)
		}

	case "delete":
		if cfg.ListID == "" {
			return nil, fmt.Errorf("listId is required for delete action")
		}
		respBody, err = sendGridRequest(ctx, cfg.APIKey, "DELETE", "marketing/lists/"+cfg.ListID, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to delete contact list: %w", err)
		}

	default:
		return nil, fmt.Errorf("invalid action: %s", cfg.Action)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":  true,
			"action":   cfg.Action,
			"response": result,
		},
	}, nil
}

// SGCampaignExecutor handles sg-campaign node type
type SGCampaignExecutor struct{}

// SGCampaignConfig defines the typed configuration for sg-campaign
type SGCampaignConfig struct {
	APIKey       string   `json:"apiKey" description:"SendGrid API key"`
	Action       string   `json:"action" options:"list,get,create,update,delete,schedule,send" description:"Campaign action to perform"`
	CampaignID   string   `json:"campaignId" description:"Campaign ID for get/update/delete/schedule/send"`
	Name         string   `json:"name" description:"Campaign name"`
	Subject      string   `json:"subject" description:"Campaign subject"`
	FromEmail    string   `json:"fromEmail" description:"Sender email"`
	FromName     string   `json:"fromName" description:"Sender name"`
	ListIDs      []string `json:"listIds" description:"Contact list IDs to send to"`
	TemplateID   string   `json:"templateId" description:"Dynamic template ID"`
	CustomUnsubscribeURL string `json:"customUnsubscribeUrl" description:"Custom unsubscribe URL"`
	SendTime     string   `json:"sendTime" description:"Scheduled send time (ISO 8601)"`
}

// SGCampaignSchema is the UI schema for sg-campaign
var SGCampaignSchema = resolver.NewSchemaBuilder("sg-campaign").
	WithName("Manage Campaigns").
	WithCategory("sendgrid").
	WithIcon("send").
	WithDescription("Manage SendGrid email campaigns").
	AddSection("Authentication").
		AddExpressionField("apiKey", "API Key",
			resolver.WithRequired(),
			resolver.WithHint("Supports {{secrets.sendgrid_api_key}}"),
		).
		EndSection().
	AddSection("Action").
		AddSelectField("action", "Action", []resolver.SelectOption{
			{Label: "List Campaigns", Value: "list", Icon: "list"},
			{Label: "Get Campaign", Value: "get", Icon: "eye"},
			{Label: "Create Campaign", Value: "create", Icon: "plus"},
			{Label: "Update Campaign", Value: "update", Icon: "edit"},
			{Label: "Delete Campaign", Value: "delete", Icon: "trash"},
			{Label: "Schedule Campaign", Value: "schedule", Icon: "clock"},
			{Label: "Send Campaign", Value: "send", Icon: "send"},
		}, resolver.WithRequired()).
		EndSection().
	AddSection("Campaign Details").
		AddTextField("campaignId", "Campaign ID",
			resolver.WithHint("Required for get/update/delete/schedule/send actions"),
		).
		AddTextField("name", "Campaign Name",
			resolver.WithPlaceholder("My Campaign"),
			resolver.WithHint("Required for create action"),
		).
		AddTextField("subject", "Subject",
			resolver.WithPlaceholder("Campaign subject"),
			resolver.WithHint("Required for create action"),
		).
		AddTextField("fromEmail", "From Email",
			resolver.WithPlaceholder("sender@example.com"),
			resolver.WithHint("Required for create action"),
		).
		AddTextField("fromName", "From Name",
			resolver.WithDefault("Sender"),
		).
		AddTagsField("listIds", "List IDs",
			resolver.WithHint("Contact list IDs to send to"),
		).
		AddTextField("templateId", "Template ID",
			resolver.WithPlaceholder("d-xxxxx"),
			resolver.WithHint("Dynamic template ID"),
		).
		AddTextField("customUnsubscribeUrl", "Custom Unsubscribe URL",
			resolver.WithPlaceholder("https://example.com/unsubscribe"),
		).
		AddTextField("sendTime", "Send Time",
			resolver.WithPlaceholder("2024-01-15T10:00:00Z"),
			resolver.WithHint("ISO 8601 format for scheduled sending"),
		).
		EndSection().
	Build()

func (e *SGCampaignExecutor) Type() string { return "sg-campaign" }

func (e *SGCampaignExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	var cfg SGCampaignConfig
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

	if cfg.Action == "" {
		return nil, fmt.Errorf("action is required")
	}

	var respBody []byte
	var err error

	switch cfg.Action {
	case "list":
		respBody, err = sendGridRequest(ctx, cfg.APIKey, "GET", "marketing/campaigns?limit=100", nil)
		if err != nil {
			return nil, fmt.Errorf("failed to list campaigns: %w", err)
		}

	case "get":
		if cfg.CampaignID == "" {
			return nil, fmt.Errorf("campaignId is required for get action")
		}
		respBody, err = sendGridRequest(ctx, cfg.APIKey, "GET", "marketing/campaigns/"+cfg.CampaignID, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to get campaign: %w", err)
		}

	case "create":
		if cfg.Name == "" || cfg.Subject == "" || cfg.FromEmail == "" || len(cfg.ListIDs) == 0 {
			return nil, fmt.Errorf("name, subject, fromEmail, and listIds are required for create action")
		}
		body := map[string]interface{}{
			"name":     cfg.Name,
			"subject":  cfg.Subject,
			"from": map[string]string{
				"email": cfg.FromEmail,
				"name":  cfg.FromName,
			},
			"list_ids": cfg.ListIDs,
		}
		if cfg.TemplateID != "" {
			body["template_id"] = cfg.TemplateID
		}
		if cfg.CustomUnsubscribeURL != "" {
			body["custom_unsubscribe_url"] = cfg.CustomUnsubscribeURL
		}
		respBody, err = sendGridRequest(ctx, cfg.APIKey, "POST", "marketing/campaigns", body)
		if err != nil {
			return nil, fmt.Errorf("failed to create campaign: %w", err)
		}

	case "update":
		if cfg.CampaignID == "" {
			return nil, fmt.Errorf("campaignId is required for update action")
		}
		body := map[string]interface{}{}
		if cfg.Name != "" {
			body["name"] = cfg.Name
		}
		if cfg.Subject != "" {
			body["subject"] = cfg.Subject
		}
		if cfg.FromEmail != "" {
			body["from"] = map[string]string{
				"email": cfg.FromEmail,
				"name":  cfg.FromName,
			}
		}
		if len(cfg.ListIDs) > 0 {
			body["list_ids"] = cfg.ListIDs
		}
		if cfg.TemplateID != "" {
			body["template_id"] = cfg.TemplateID
		}
		respBody, err = sendGridRequest(ctx, cfg.APIKey, "PATCH", "marketing/campaigns/"+cfg.CampaignID, body)
		if err != nil {
			return nil, fmt.Errorf("failed to update campaign: %w", err)
		}

	case "delete":
		if cfg.CampaignID == "" {
			return nil, fmt.Errorf("campaignId is required for delete action")
		}
		respBody, err = sendGridRequest(ctx, cfg.APIKey, "DELETE", "marketing/campaigns/"+cfg.CampaignID, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to delete campaign: %w", err)
		}

	case "schedule":
		if cfg.CampaignID == "" || cfg.SendTime == "" {
			return nil, fmt.Errorf("campaignId and sendTime are required for schedule action")
		}
		body := map[string]interface{}{
			"send_at": cfg.SendTime,
		}
		respBody, err = sendGridRequest(ctx, cfg.APIKey, "POST", "marketing/campaigns/"+cfg.CampaignID+"/schedules", body)
		if err != nil {
			return nil, fmt.Errorf("failed to schedule campaign: %w", err)
		}

	case "send":
		if cfg.CampaignID == "" {
			return nil, fmt.Errorf("campaignId is required for send action")
		}
		respBody, err = sendGridRequest(ctx, cfg.APIKey, "POST", "marketing/campaigns/"+cfg.CampaignID+"/schedules/now", nil)
		if err != nil {
			return nil, fmt.Errorf("failed to send campaign: %w", err)
		}

	default:
		return nil, fmt.Errorf("invalid action: %s", cfg.Action)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":  true,
			"action":   cfg.Action,
			"response": result,
		},
	}, nil
}
