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
	"sync"
	"time"

	"github.com/axiom-studio/skills.sdk/executor"
	"github.com/axiom-studio/skills.sdk/grpc"
	"github.com/axiom-studio/skills.sdk/resolver"
)

const (
	iconWhatsApp  = "message-circle"
	apiBaseURL    = "https://graph.facebook.com/v18.0"
	defaultTimeout = 30 * time.Second
)

// HTTPClient cache for connection reuse
var (
	httpClient *http.Client
	clientOnce sync.Once
)

// getHTTPClient returns a shared HTTP client
func getHTTPClient() *http.Client {
	clientOnce.Do(func() {
		httpClient = &http.Client{
			Timeout: defaultTimeout,
		}
	})
	return httpClient
}

func main() {
	// Get port from env or use default
	port := os.Getenv("SKILL_PORT")
	if port == "" {
		port = "50110"
	}

	// Create skill server
	server := grpc.NewSkillServer("skill-whatsapp", "1.0.0")

	// Register executors with schemas
	server.RegisterExecutorWithSchema("whatsapp-send-message", &SendMessageExecutor{}, SendMessageSchema)
	server.RegisterExecutorWithSchema("whatsapp-send-template", &SendTemplateExecutor{}, SendTemplateSchema)
	server.RegisterExecutorWithSchema("whatsapp-send-media", &SendMediaExecutor{}, SendMediaSchema)
	server.RegisterExecutorWithSchema("whatsapp-mark-read", &MarkReadExecutor{}, MarkReadSchema)
	server.RegisterExecutorWithSchema("whatsapp-contact-list", &ContactListExecutor{}, ContactListSchema)
	server.RegisterExecutorWithSchema("whatsapp-phone-number-list", &PhoneNumberListExecutor{}, PhoneNumberListSchema)
	server.RegisterExecutorWithSchema("whatsapp-business-profile", &BusinessProfileExecutor{}, BusinessProfileSchema)

	fmt.Printf("Starting skill-whatsapp gRPC server on port %s\n", port)
	if err := server.Serve(port); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to serve: %v\n", err)
		os.Exit(1)
	}
}

// ============================================================================
// HTTP CLIENT HELPERS
// ============================================================================

// doRequest performs an HTTP request to the WhatsApp Business API
func doRequest(ctx context.Context, method, endpoint, accessToken string, body interface{}) ([]byte, error) {
	client := getHTTPClient()

	var reqBody io.Reader
	if body != nil {
		jsonBody, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
		reqBody = bytes.NewReader(jsonBody)
	}

	url := fmt.Sprintf("%s/%s", apiBaseURL, strings.TrimPrefix(endpoint, "/"))

	req, err := http.NewRequestWithContext(ctx, method, url, reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode >= 400 {
		var errResp map[string]interface{}
		json.Unmarshal(respBody, &errResp)
		return nil, fmt.Errorf("WhatsApp API error (status %d): %s", resp.StatusCode, formatAPIError(errResp))
	}

	return respBody, nil
}

// formatAPIError formats WhatsApp API error response
func formatAPIError(errResp map[string]interface{}) string {
	if err, ok := errResp["error"].(map[string]interface{}); ok {
		msg, _ := err["message"].(string)
		code, _ := err["code"].(float64)
		if msg != "" {
			return fmt.Sprintf("%.0f: %s", code, msg)
		}
	}
	return string(mustJSON(errResp))
}

func mustJSON(v interface{}) []byte {
	b, _ := json.Marshal(v)
	return b
}

// ============================================================================
// CONFIG HELPERS
// ============================================================================

// getString safely gets a string from config
func getString(config map[string]interface{}, key string) string {
	if v, ok := config[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// getInt safely gets an int from config
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

// getBool safely gets a bool from config
func getBool(config map[string]interface{}, key string, def bool) bool {
	if v, ok := config[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return def
}

// getStringSlice safely gets a string slice from config
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

// getMap safely gets a map from config
func getMap(config map[string]interface{}, key string) map[string]interface{} {
	if v, ok := config[key]; ok {
		if m, ok := v.(map[string]interface{}); ok {
			return m
		}
	}
	return nil
}

// getInterfaceSlice safely gets an interface slice from config
func getInterfaceSlice(config map[string]interface{}, key string) []interface{} {
	if v, ok := config[key]; ok {
		if arr, ok := v.([]interface{}); ok {
			return arr
		}
	}
	return nil
}

// ============================================================================
// SCHEMAS
// ============================================================================

// SendMessageSchema is the UI schema for whatsapp-send-message
var SendMessageSchema = resolver.NewSchemaBuilder("whatsapp-send-message").
	WithName("Send WhatsApp Message").
	WithCategory("action").
	WithIcon(iconWhatsApp).
	WithDescription("Send a text message via WhatsApp Business API").
	AddSection("Connection").
		AddExpressionField("accessToken", "Access Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("EAAB..."),
			resolver.WithHint("WhatsApp Business API access token (supports {{bindings.xxx}})"),
			resolver.WithSensitive(),
		).
		AddExpressionField("phoneNumberId", "Phone Number ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("123456789012345"),
			resolver.WithHint("WhatsApp phone number ID from Meta Business Suite"),
		).
		EndSection().
	AddSection("Recipient").
		AddExpressionField("recipientPhone", "Recipient Phone",
			resolver.WithRequired(),
			resolver.WithPlaceholder("+1234567890"),
			resolver.WithHint("Recipient's phone number with country code (e.g., +1234567890)"),
		).
		EndSection().
	AddSection("Message").
		AddTextareaField("message", "Message Text",
			resolver.WithRequired(),
			resolver.WithRows(4),
			resolver.WithPlaceholder("Hello! This is a message from our business."),
			resolver.WithHint("Text content of the message (supports {{bindings.xxx}} templates)"),
		).
		EndSection().
	Build()

// SendTemplateSchema is the UI schema for whatsapp-send-template
var SendTemplateSchema = resolver.NewSchemaBuilder("whatsapp-send-template").
	WithName("Send WhatsApp Template").
	WithCategory("action").
	WithIcon(iconWhatsApp).
	WithDescription("Send a pre-approved template message via WhatsApp Business API").
	AddSection("Connection").
		AddExpressionField("accessToken", "Access Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("EAAB..."),
			resolver.WithHint("WhatsApp Business API access token"),
			resolver.WithSensitive(),
		).
		AddExpressionField("phoneNumberId", "Phone Number ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("123456789012345"),
			resolver.WithHint("WhatsApp phone number ID"),
		).
		EndSection().
	AddSection("Recipient").
		AddExpressionField("recipientPhone", "Recipient Phone",
			resolver.WithRequired(),
			resolver.WithPlaceholder("+1234567890"),
			resolver.WithHint("Recipient's phone number with country code"),
		).
		EndSection().
	AddSection("Template").
		AddExpressionField("templateName", "Template Name",
			resolver.WithRequired(),
			resolver.WithPlaceholder("order_confirmation"),
			resolver.WithHint("Name of the pre-approved template"),
		).
		AddExpressionField("language", "Language",
			resolver.WithDefault("en_US"),
			resolver.WithHint("Language code for the template (e.g., en_US, es_ES)"),
		).
		AddJSONField("templateComponents", "Template Components",
			resolver.WithHeight(150),
			resolver.WithHint("Optional: Template variable substitutions. Format: [{\"type\":\"header\",\"parameters\":[{\"type\":\"text\",\"text\":\"Value\"}]},{\"type\":\"body\",\"parameters\":[{\"type\":\"text\",\"text\":\"Value\"}]}]"),
		).
		EndSection().
	Build()

// SendMediaSchema is the UI schema for whatsapp-send-media
var SendMediaSchema = resolver.NewSchemaBuilder("whatsapp-send-media").
	WithName("Send WhatsApp Media").
	WithCategory("action").
	WithIcon(iconWhatsApp).
	WithDescription("Send an image, video, document, or audio via WhatsApp Business API").
	AddSection("Connection").
		AddExpressionField("accessToken", "Access Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("EAAB..."),
			resolver.WithHint("WhatsApp Business API access token"),
			resolver.WithSensitive(),
		).
		AddExpressionField("phoneNumberId", "Phone Number ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("123456789012345"),
			resolver.WithHint("WhatsApp phone number ID"),
		).
		EndSection().
	AddSection("Recipient").
		AddExpressionField("recipientPhone", "Recipient Phone",
			resolver.WithRequired(),
			resolver.WithPlaceholder("+1234567890"),
			resolver.WithHint("Recipient's phone number with country code"),
		).
		EndSection().
	AddSection("Media").
		AddSelectField("mediaType", "Media Type",
			[]resolver.SelectOption{
				{Label: "Image", Value: "image"},
				{Label: "Video", Value: "video"},
				{Label: "Document", Value: "document"},
				{Label: "Audio", Value: "audio"},
				{Label: "Sticker", Value: "sticker"},
			},
			resolver.WithDefault("image"),
			resolver.WithHint("Type of media to send"),
		).
		AddExpressionField("mediaUrl", "Media URL",
			resolver.WithRequired(),
			resolver.WithPlaceholder("https://example.com/image.jpg"),
			resolver.WithHint("Public URL of the media file (must be accessible by WhatsApp)"),
		).
		AddExpressionField("caption", "Caption",
			resolver.WithPlaceholder("Optional caption for the media"),
			resolver.WithHint("Optional text caption (for images, videos, documents)"),
		).
		AddExpressionField("filename", "Filename",
			resolver.WithPlaceholder("document.pdf"),
			resolver.WithHint("Filename for document type media"),
		).
		EndSection().
	Build()

// MarkReadSchema is the UI schema for whatsapp-mark-read
var MarkReadSchema = resolver.NewSchemaBuilder("whatsapp-mark-read").
	WithName("Mark Messages as Read").
	WithCategory("action").
	WithIcon(iconWhatsApp).
	WithDescription("Mark WhatsApp messages as read").
	AddSection("Connection").
		AddExpressionField("accessToken", "Access Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("EAAB..."),
			resolver.WithHint("WhatsApp Business API access token"),
			resolver.WithSensitive(),
		).
		AddExpressionField("phoneNumberId", "Phone Number ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("123456789012345"),
			resolver.WithHint("WhatsApp phone number ID"),
		).
		EndSection().
	AddSection("Message").
		AddExpressionField("messageId", "Message ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("wamid.HBgN..."),
			resolver.WithHint("WhatsApp message ID to mark as read"),
		).
		EndSection().
	Build()

// ContactListSchema is the UI schema for whatsapp-contact-list
var ContactListSchema = resolver.NewSchemaBuilder("whatsapp-contact-list").
	WithName("Get Contact List").
	WithCategory("action").
	WithIcon(iconWhatsApp).
	WithDescription("Retrieve contacts from WhatsApp Business API (from message history)").
	AddSection("Connection").
		AddExpressionField("accessToken", "Access Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("EAAB..."),
			resolver.WithHint("WhatsApp Business API access token"),
			resolver.WithSensitive(),
		).
		AddExpressionField("phoneNumberId", "Phone Number ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("123456789012345"),
			resolver.WithHint("WhatsApp phone number ID"),
		).
		EndSection().
	AddSection("Options").
		AddNumberField("limit", "Limit",
			resolver.WithDefault(100),
			resolver.WithHint("Maximum number of contacts to return (1-1000)"),
		).
		AddExpressionField("cursor", "Cursor",
			resolver.WithPlaceholder("QVFIU..."),
			resolver.WithHint("Pagination cursor for next page"),
		).
		EndSection().
	Build()

// PhoneNumberListSchema is the UI schema for whatsapp-phone-number-list
var PhoneNumberListSchema = resolver.NewSchemaBuilder("whatsapp-phone-number-list").
	WithName("List Phone Numbers").
	WithCategory("action").
	WithIcon(iconWhatsApp).
	WithDescription("List all phone numbers registered to your WhatsApp Business Account").
	AddSection("Connection").
		AddExpressionField("accessToken", "Access Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("EAAB..."),
			resolver.WithHint("WhatsApp Business API access token"),
			resolver.WithSensitive(),
		).
		AddExpressionField("businessAccountId", "Business Account ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("123456789012345"),
			resolver.WithHint("WhatsApp Business Account ID from Meta Business Suite"),
		).
		EndSection().
	Build()

// BusinessProfileSchema is the UI schema for whatsapp-business-profile
var BusinessProfileSchema = resolver.NewSchemaBuilder("whatsapp-business-profile").
	WithName("Get Business Profile").
	WithCategory("action").
	WithIcon(iconWhatsApp).
	WithDescription("Retrieve the business profile information from WhatsApp Business API").
	AddSection("Connection").
		AddExpressionField("accessToken", "Access Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("EAAB..."),
			resolver.WithHint("WhatsApp Business API access token"),
			resolver.WithSensitive(),
		).
		AddExpressionField("phoneNumberId", "Phone Number ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("123456789012345"),
			resolver.WithHint("WhatsApp phone number ID"),
		).
		EndSection().
	Build()

// ============================================================================
// EXECUTORS
// ============================================================================

// SendMessageExecutor handles whatsapp-send-message
type SendMessageExecutor struct{}

// Type returns the executor type
func (e *SendMessageExecutor) Type() string {
	return "whatsapp-send-message"
}

// Execute sends a text message via WhatsApp
func (e *SendMessageExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	// Get connection parameters
	accessToken := resolver.ResolveString(getString(config, "accessToken"))
	phoneNumberId := resolver.ResolveString(getString(config, "phoneNumberId"))

	if accessToken == "" {
		return nil, fmt.Errorf("access token is required")
	}
	if phoneNumberId == "" {
		return nil, fmt.Errorf("phone number ID is required")
	}

	// Get recipient
	recipientPhone := resolver.ResolveString(getString(config, "recipientPhone"))
	if recipientPhone == "" {
		return nil, fmt.Errorf("recipient phone number is required")
	}

	// Get message
	message := resolver.ResolveString(getString(config, "message"))
	if message == "" {
		return nil, fmt.Errorf("message text is required")
	}

	// Build request body
	requestBody := map[string]interface{}{
		"messaging_product": "whatsapp",
		"to":                recipientPhone,
		"type":              "text",
		"text": map[string]interface{}{
			"body": message,
		},
	}

	// Make API call
	respBody, err := doRequest(ctx, "POST", fmt.Sprintf("/%s/messages", phoneNumberId), accessToken, requestBody)
	if err != nil {
		return nil, fmt.Errorf("failed to send message: %w", err)
	}

	// Parse response
	var response map[string]interface{}
	if err := json.Unmarshal(respBody, &response); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Build result
	result := map[string]interface{}{
		"success": true,
		"message": "Message sent successfully",
	}

	if messages, ok := response["messages"].([]interface{}); ok && len(messages) > 0 {
		if msg, ok := messages[0].(map[string]interface{}); ok {
			result["messageId"] = getStringFromMap(msg, "id")
			result["messageStatus"] = getStringFromMap(msg, "message_status")
		}
	}

	if contacts, ok := response["contacts"].([]interface{}); ok && len(contacts) > 0 {
		if contact, ok := contacts[0].(map[string]interface{}); ok {
			result["recipientId"] = getStringFromMap(contact, "wa_id")
			if input, ok := contact["input"].(map[string]interface{}); ok {
				result["recipientPhone"] = getStringFromMap(input, "to")
			}
		}
	}

	return &executor.StepResult{
		Output: result,
	}, nil
}

// SendTemplateExecutor handles whatsapp-send-template
type SendTemplateExecutor struct{}

// Type returns the executor type
func (e *SendTemplateExecutor) Type() string {
	return "whatsapp-send-template"
}

// Execute sends a template message via WhatsApp
func (e *SendTemplateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	// Get connection parameters
	accessToken := resolver.ResolveString(getString(config, "accessToken"))
	phoneNumberId := resolver.ResolveString(getString(config, "phoneNumberId"))

	if accessToken == "" {
		return nil, fmt.Errorf("access token is required")
	}
	if phoneNumberId == "" {
		return nil, fmt.Errorf("phone number ID is required")
	}

	// Get recipient
	recipientPhone := resolver.ResolveString(getString(config, "recipientPhone"))
	if recipientPhone == "" {
		return nil, fmt.Errorf("recipient phone number is required")
	}

	// Get template
	templateName := resolver.ResolveString(getString(config, "templateName"))
	if templateName == "" {
		return nil, fmt.Errorf("template name is required")
	}

	language := resolver.ResolveString(getString(config, "language"))
	if language == "" {
		language = "en_US"
	}

	// Build template components
	var components []interface{}
	componentsRaw := getInterfaceSlice(config, "templateComponents")
	if len(componentsRaw) > 0 {
		components = componentsRaw
	}

	// Build request body
	templateData := map[string]interface{}{
		"name":     templateName,
		"language": map[string]interface{}{"code": language},
	}
	if len(components) > 0 {
		templateData["components"] = components
	}

	requestBody := map[string]interface{}{
		"messaging_product": "whatsapp",
		"to":                recipientPhone,
		"type":              "template",
		"template":          templateData,
	}

	// Make API call
	respBody, err := doRequest(ctx, "POST", fmt.Sprintf("/%s/messages", phoneNumberId), accessToken, requestBody)
	if err != nil {
		return nil, fmt.Errorf("failed to send template message: %w", err)
	}

	// Parse response
	var response map[string]interface{}
	if err := json.Unmarshal(respBody, &response); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Build result
	result := map[string]interface{}{
		"success":    true,
		"message":    "Template message sent successfully",
		"template":   templateName,
		"language":   language,
		"recipient":  recipientPhone,
	}

	if messages, ok := response["messages"].([]interface{}); ok && len(messages) > 0 {
		if msg, ok := messages[0].(map[string]interface{}); ok {
			result["messageId"] = getStringFromMap(msg, "id")
			result["messageStatus"] = getStringFromMap(msg, "message_status")
		}
	}

	if contacts, ok := response["contacts"].([]interface{}); ok && len(contacts) > 0 {
		if contact, ok := contacts[0].(map[string]interface{}); ok {
			result["recipientId"] = getStringFromMap(contact, "wa_id")
		}
	}

	return &executor.StepResult{
		Output: result,
	}, nil
}

// SendMediaExecutor handles whatsapp-send-media
type SendMediaExecutor struct{}

// Type returns the executor type
func (e *SendMediaExecutor) Type() string {
	return "whatsapp-send-media"
}

// Execute sends a media message via WhatsApp
func (e *SendMediaExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	// Get connection parameters
	accessToken := resolver.ResolveString(getString(config, "accessToken"))
	phoneNumberId := resolver.ResolveString(getString(config, "phoneNumberId"))

	if accessToken == "" {
		return nil, fmt.Errorf("access token is required")
	}
	if phoneNumberId == "" {
		return nil, fmt.Errorf("phone number ID is required")
	}

	// Get recipient
	recipientPhone := resolver.ResolveString(getString(config, "recipientPhone"))
	if recipientPhone == "" {
		return nil, fmt.Errorf("recipient phone number is required")
	}

	// Get media parameters
	mediaType := resolver.ResolveString(getString(config, "mediaType"))
	if mediaType == "" {
		mediaType = "image"
	}

	mediaUrl := resolver.ResolveString(getString(config, "mediaUrl"))
	if mediaUrl == "" {
		return nil, fmt.Errorf("media URL is required")
	}

	caption := resolver.ResolveString(getString(config, "caption"))
	filename := resolver.ResolveString(getString(config, "filename"))

	// Build media object
	mediaData := map[string]interface{}{
		"link": mediaUrl,
	}
	if caption != "" && mediaType != "audio" && mediaType != "sticker" {
		mediaData["caption"] = caption
	}
	if filename != "" && mediaType == "document" {
		mediaData["filename"] = filename
	}

	// Build request body
	requestBody := map[string]interface{}{
		"messaging_product": "whatsapp",
		"to":                recipientPhone,
		"type":              mediaType,
		mediaType:           mediaData,
	}

	// Make API call
	respBody, err := doRequest(ctx, "POST", fmt.Sprintf("/%s/messages", phoneNumberId), accessToken, requestBody)
	if err != nil {
		return nil, fmt.Errorf("failed to send media message: %w", err)
	}

	// Parse response
	var response map[string]interface{}
	if err := json.Unmarshal(respBody, &response); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Build result
	result := map[string]interface{}{
		"success":   true,
		"message":   fmt.Sprintf("%s sent successfully", strings.Title(mediaType)),
		"mediaType": mediaType,
		"mediaUrl":  mediaUrl,
		"recipient": recipientPhone,
	}

	if messages, ok := response["messages"].([]interface{}); ok && len(messages) > 0 {
		if msg, ok := messages[0].(map[string]interface{}); ok {
			result["messageId"] = getStringFromMap(msg, "id")
			result["messageStatus"] = getStringFromMap(msg, "message_status")
		}
	}

	if contacts, ok := response["contacts"].([]interface{}); ok && len(contacts) > 0 {
		if contact, ok := contacts[0].(map[string]interface{}); ok {
			result["recipientId"] = getStringFromMap(contact, "wa_id")
		}
	}

	return &executor.StepResult{
		Output: result,
	}, nil
}

// MarkReadExecutor handles whatsapp-mark-read
type MarkReadExecutor struct{}

// Type returns the executor type
func (e *MarkReadExecutor) Type() string {
	return "whatsapp-mark-read"
}

// Execute marks a message as read
func (e *MarkReadExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	// Get connection parameters
	accessToken := resolver.ResolveString(getString(config, "accessToken"))
	phoneNumberId := resolver.ResolveString(getString(config, "phoneNumberId"))

	if accessToken == "" {
		return nil, fmt.Errorf("access token is required")
	}
	if phoneNumberId == "" {
		return nil, fmt.Errorf("phone number ID is required")
	}

	// Get message ID
	messageId := resolver.ResolveString(getString(config, "messageId"))
	if messageId == "" {
		return nil, fmt.Errorf("message ID is required")
	}

	// Build request body
	requestBody := map[string]interface{}{
		"messaging_product": "whatsapp",
		"status":            "read",
		"message_id":        messageId,
	}

	// Make API call
	respBody, err := doRequest(ctx, "POST", fmt.Sprintf("/%s/messages", phoneNumberId), accessToken, requestBody)
	if err != nil {
		return nil, fmt.Errorf("failed to mark message as read: %w", err)
	}

	// Parse response
	var response map[string]interface{}
	if err := json.Unmarshal(respBody, &response); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Build result
	result := map[string]interface{}{
		"success":   true,
		"message":   "Message marked as read",
		"messageId": messageId,
	}

	if success, ok := response["success"].(bool); ok {
		result["success"] = success
	}

	return &executor.StepResult{
		Output: result,
	}, nil
}

// ContactListExecutor handles whatsapp-contact-list
type ContactListExecutor struct{}

// Type returns the executor type
func (e *ContactListExecutor) Type() string {
	return "whatsapp-contact-list"
}

// Execute retrieves contacts from WhatsApp
func (e *ContactListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	// Get connection parameters
	accessToken := resolver.ResolveString(getString(config, "accessToken"))
	phoneNumberId := resolver.ResolveString(getString(config, "phoneNumberId"))

	if accessToken == "" {
		return nil, fmt.Errorf("access token is required")
	}
	if phoneNumberId == "" {
		return nil, fmt.Errorf("phone number ID is required")
	}

	// Get options
	limit := getInt(config, "limit", 100)
	cursor := resolver.ResolveString(getString(config, "cursor"))

	// Build endpoint with query parameters
	endpoint := fmt.Sprintf("/%s/messages?limit=%d", phoneNumberId, limit)
	if cursor != "" {
		endpoint += "&after=" + cursor
	}

	// Make API call
	respBody, err := doRequest(ctx, "GET", endpoint, accessToken, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve messages: %w", err)
	}

	// Parse response
	var response map[string]interface{}
	if err := json.Unmarshal(respBody, &response); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Extract unique contacts from messages
	contactsMap := make(map[string]map[string]interface{})
	var contacts []map[string]interface{}

	if messages, ok := response["messages"].([]interface{}); ok {
		for _, msgRaw := range messages {
			msg, ok := msgRaw.(map[string]interface{})
			if !ok {
				continue
			}

			from, _ := msg["from"].(string)
			if from == "" {
				continue
			}

			if _, exists := contactsMap[from]; !exists {
				contact := map[string]interface{}{
					"phone": from,
				}

				// Extract profile name if available
				if profile, ok := msg["profile"].(map[string]interface{}); ok {
					if name, ok := profile["name"].(string); ok {
						contact["name"] = name
					}
				}

				// Get message timestamp
				if timestamp, ok := msg["timestamp"].(string); ok {
					contact["lastMessageTime"] = timestamp
				}

				contactsMap[from] = contact
			}
		}
	}

	// Convert map to slice
	for _, contact := range contactsMap {
		contacts = append(contacts, contact)
	}

	// Build result
	result := map[string]interface{}{
		"success":  true,
		"contacts": contacts,
		"count":    len(contacts),
	}

	// Add pagination info
	if paging, ok := response["paging"].(map[string]interface{}); ok {
		if cursors, ok := paging["cursors"].(map[string]interface{}); ok {
			if after, ok := cursors["after"].(string); ok {
				result["nextCursor"] = after
			}
			if before, ok := cursors["before"].(string); ok {
				result["prevCursor"] = before
			}
		}
	}

	return &executor.StepResult{
		Output: result,
	}, nil
}

// PhoneNumberListExecutor handles whatsapp-phone-number-list
type PhoneNumberListExecutor struct{}

// Type returns the executor type
func (e *PhoneNumberListExecutor) Type() string {
	return "whatsapp-phone-number-list"
}

// Execute retrieves phone numbers from WhatsApp Business Account
func (e *PhoneNumberListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	// Get connection parameters
	accessToken := resolver.ResolveString(getString(config, "accessToken"))
	businessAccountId := resolver.ResolveString(getString(config, "businessAccountId"))

	if accessToken == "" {
		return nil, fmt.Errorf("access token is required")
	}
	if businessAccountId == "" {
		return nil, fmt.Errorf("business account ID is required")
	}

	// Make API call
	endpoint := fmt.Sprintf("/%s/phone_numbers", businessAccountId)
	respBody, err := doRequest(ctx, "GET", endpoint, accessToken, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve phone numbers: %w", err)
	}

	// Parse response
	var response map[string]interface{}
	if err := json.Unmarshal(respBody, &response); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Extract phone numbers
	var phoneNumbers []map[string]interface{}
	if data, ok := response["data"].([]interface{}); ok {
		for _, itemRaw := range data {
			item, ok := itemRaw.(map[string]interface{})
			if !ok {
				continue
			}

			phoneNumber := map[string]interface{}{
				"id":          getStringFromMap(item, "id"),
				"name":        getStringFromMap(item, "name"),
				"phoneNumber": getStringFromMap(item, "phone_number"),
				"status":      getStringFromMap(item, "status"),
				"qualityRating": getStringFromMap(item, "quality_rating"),
			}

			// Extract verified name
			if verifiedName, ok := item["verified_name"].(string); ok {
				phoneNumber["verifiedName"] = verifiedName
			}

			// Extract code verification details
			if codeVerification, ok := item["code_verification"].(map[string]interface{}); ok {
				phoneNumber["codeVerification"] = map[string]interface{}{
					"status":        getStringFromMap(codeVerification, "status"),
					"timeout":       getStringFromMap(codeVerification, "timeout"),
				}
			}

			phoneNumbers = append(phoneNumbers, phoneNumber)
		}
	}

	// Build result
	result := map[string]interface{}{
		"success":      true,
		"phoneNumbers": phoneNumbers,
		"count":        len(phoneNumbers),
	}

	return &executor.StepResult{
		Output: result,
	}, nil
}

// BusinessProfileExecutor handles whatsapp-business-profile
type BusinessProfileExecutor struct{}

// Type returns the executor type
func (e *BusinessProfileExecutor) Type() string {
	return "whatsapp-business-profile"
}

// Execute retrieves the business profile
func (e *BusinessProfileExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	// Get connection parameters
	accessToken := resolver.ResolveString(getString(config, "accessToken"))
	phoneNumberId := resolver.ResolveString(getString(config, "phoneNumberId"))

	if accessToken == "" {
		return nil, fmt.Errorf("access token is required")
	}
	if phoneNumberId == "" {
		return nil, fmt.Errorf("phone number ID is required")
	}

	// Make API call
	endpoint := fmt.Sprintf("/%s", phoneNumberId)
	respBody, err := doRequest(ctx, "GET", endpoint, accessToken, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve business profile: %w", err)
	}

	// Parse response
	var response map[string]interface{}
	if err := json.Unmarshal(respBody, &response); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Extract profile information
	profile := map[string]interface{}{
		"id":           getStringFromMap(response, "id"),
		"name":         getStringFromMap(response, "name"),
		"phoneNumber":  getStringFromMap(response, "phone_number"),
		"status":       getStringFromMap(response, "status"),
		"qualityRating": getStringFromMap(response, "quality_rating"),
	}

	// Extract business profile details
	if businessProfile, ok := response["business_profile"].(map[string]interface{}); ok {
		profile["businessProfile"] = map[string]interface{}{
			"about":       getStringFromMap(businessProfile, "about"),
			"address":     getStringFromMap(businessProfile, "address"),
			"description": getStringFromMap(businessProfile, "description"),
			"email":       getStringFromMap(businessProfile, "email"),
			"websites":    getStringSliceFromMap(businessProfile, "websites"),
			"vertical":    getStringFromMap(businessProfile, "vertical"),
		}
	}

	// Build result
	result := map[string]interface{}{
		"success": true,
		"profile": profile,
	}

	return &executor.StepResult{
		Output: result,
	}, nil
}

// ============================================================================
// HELPER FUNCTIONS
// ============================================================================

// getStringFromMap safely gets a string from a map
func getStringFromMap(m map[string]interface{}, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// getStringSliceFromMap safely gets a string slice from a map
func getStringSliceFromMap(m map[string]interface{}, key string) []string {
	if v, ok := m[key]; ok {
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
		}
	}
	return nil
}
