package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/axiom-studio/skills.sdk/executor"
	"github.com/axiom-studio/skills.sdk/grpc"
	"github.com/axiom-studio/skills.sdk/resolver"
)

const (
	iconTelegram = "send"
)

// TelegramConfig holds Telegram bot configuration
type TelegramConfig struct {
	BotToken string `json:"botToken"`
}

// HTTPClient is the HTTP client for Telegram API calls
var httpClient = &http.Client{
	Timeout: 30 * time.Second,
}

// TelegramAPIBase is the base URL for Telegram Bot API
const TelegramAPIBase = "https://api.telegram.org/bot"

// ============================================================================
// MAIN
// ============================================================================

func main() {
	// Get port from env or use default
	port := os.Getenv("SKILL_PORT")
	if port == "" {
		port = "50109"
	}

	// Create skill server
	server := grpc.NewSkillServer("skill-telegram", "1.0.0")

	// Register executors with schemas
	server.RegisterExecutorWithSchema("telegram-send-message", &SendMessageExecutor{}, SendMessageSchema)
	server.RegisterExecutorWithSchema("telegram-edit-message", &EditMessageExecutor{}, EditMessageSchema)
	server.RegisterExecutorWithSchema("telegram-delete-message", &DeleteMessageExecutor{}, DeleteMessageSchema)
	server.RegisterExecutorWithSchema("telegram-send-photo", &SendPhotoExecutor{}, SendPhotoSchema)
	server.RegisterExecutorWithSchema("telegram-send-document", &SendDocumentExecutor{}, SendDocumentSchema)
	server.RegisterExecutorWithSchema("telegram-get-updates", &GetUpdatesExecutor{}, GetUpdatesSchema)
	server.RegisterExecutorWithSchema("telegram-get-chat", &GetChatExecutor{}, GetChatSchema)
	server.RegisterExecutorWithSchema("telegram-set-webhook", &SetWebhookExecutor{}, SetWebhookSchema)

	fmt.Printf("Starting skill-telegram gRPC server on port %s\n", port)
	if err := server.Serve(port); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to serve: %v\n", err)
		os.Exit(1)
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

// ============================================================================
// TELEGRAM API HELPERS
// ============================================================================

// TelegramResponse is the standard Telegram API response structure
type TelegramResponse struct {
	OK          bool            `json:"ok"`
	Result      json.RawMessage `json:"result,omitempty"`
	Description string          `json:"description,omitempty"`
	ErrorCode   int             `json:"error_code,omitempty"`
	Parameters  struct {
		RetryAfter      int `json:"retry_after,omitempty"`
		MigrateToChatID int `json:"migrate_to_chat_id,omitempty"`
	} `json:"parameters,omitempty"`
}

// makeTelegramRequest makes a POST request to the Telegram Bot API
func makeTelegramRequest(botToken, method string, params map[string]interface{}) ([]byte, error) {
	url := TelegramAPIBase + botToken + "/" + method

	jsonData, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal params: %w", err)
	}

	req, err := http.NewRequest("POST", url, bytes.NewReader(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	return body, nil
}

// makeTelegramMultipartRequest makes a multipart POST request for file uploads
func makeTelegramMultipartRequest(botToken, method string, params map[string]string, fileField, fileName string, fileData []byte) ([]byte, error) {
	url := TelegramAPIBase + botToken + "/" + method

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	// Add text fields
	for key, value := range params {
		if err := writer.WriteField(key, value); err != nil {
			return nil, fmt.Errorf("failed to write field %s: %w", key, err)
		}
	}

	// Add file
	if fileData != nil {
		part, err := writer.CreateFormFile(fileField, fileName)
		if err != nil {
			return nil, fmt.Errorf("failed to create form file: %w", err)
		}
		if _, err := part.Write(fileData); err != nil {
			return nil, fmt.Errorf("failed to write file data: %w", err)
		}
	}

	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("failed to close multipart writer: %w", err)
	}

	req, err := http.NewRequest("POST", url, body)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	return respBody, nil
}

// parseTelegramResponse parses a Telegram API response
func parseTelegramResponse(body []byte, result interface{}) error {
	var resp TelegramResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}

	if !resp.OK {
		if resp.ErrorCode == 401 {
			return fmt.Errorf("invalid bot token")
		}
		if resp.ErrorCode == 400 {
			return fmt.Errorf("bad request: %s", resp.Description)
		}
		if resp.ErrorCode == 403 {
			return fmt.Errorf("forbidden: %s", resp.Description)
		}
		if resp.ErrorCode == 429 {
			return fmt.Errorf("rate limited, retry after %d seconds", resp.Parameters.RetryAfter)
		}
		return fmt.Errorf("telegram API error (%d): %s", resp.ErrorCode, resp.Description)
	}

	if result != nil && len(resp.Result) > 0 {
		if err := json.Unmarshal(resp.Result, result); err != nil {
			return fmt.Errorf("failed to parse result: %w", err)
		}
	}

	return nil
}

// getBotToken gets the bot token from config or resolver
func getBotToken(config map[string]interface{}, resolver executor.TemplateResolver) (string, error) {
	botToken := resolver.ResolveString(getString(config, "botToken"))
	if botToken == "" {
		return "", fmt.Errorf("bot token is required")
	}
	// Remove "Bot" prefix if present
	botToken = strings.TrimPrefix(botToken, "Bot ")
	botToken = strings.TrimSpace(botToken)
	return botToken, nil
}

// ============================================================================
// SCHEMAS
// ============================================================================

// SendMessageSchema is the UI schema for telegram-send-message
var SendMessageSchema = resolver.NewSchemaBuilder("telegram-send-message").
	WithName("Send Telegram Message").
	WithCategory("action").
	WithIcon(iconTelegram).
	WithDescription("Send a text message to a Telegram chat").
	AddSection("Connection").
	AddExpressionField("botToken", "Bot Token",
		resolver.WithRequired(),
		resolver.WithPlaceholder("123456789:ABCdefGHIjklMNOpqrsTUVwxyz"),
		resolver.WithHint("Telegram Bot Token (supports {{bindings.xxx}})"),
		resolver.WithSensitive(),
	).
	EndSection().
	AddSection("Message").
	AddExpressionField("chatId", "Chat ID",
		resolver.WithRequired(),
		resolver.WithPlaceholder("123456789 or @channelname"),
		resolver.WithHint("Chat ID or username (e.g., 123456789 or @channelname)"),
	).
	AddTextareaField("text", "Message Text",
		resolver.WithRequired(),
		resolver.WithRows(4),
		resolver.WithPlaceholder("Hello from Axiom!"),
		resolver.WithHint("Message text to send"),
	).
	AddSelectField("parseMode", "Parse Mode",
		[]resolver.SelectOption{
			{Label: "Plain Text", Value: ""},
			{Label: "Markdown", Value: "Markdown"},
			{Label: "Markdown V2", Value: "MarkdownV2"},
			{Label: "HTML", Value: "HTML"},
		},
		resolver.WithDefault(""),
		resolver.WithHint("How to parse the message text"),
	).
	EndSection().
	AddSection("Options").
	AddToggleField("disableNotification", "Disable Notification",
		resolver.WithDefault(false),
		resolver.WithHint("Send silently (users receive without sound)"),
	).
	AddToggleField("disableWebPagePreview", "Disable Web Page Preview",
		resolver.WithDefault(false),
		resolver.WithHint("Disable link preview generation"),
	).
	AddJSONField("replyMarkup", "Reply Markup",
		resolver.WithHeight(100),
		resolver.WithHint("Optional inline keyboard or reply keyboard (JSON)"),
	).
	AddNumberField("replyToMessageId", "Reply To Message ID",
		resolver.WithHint("Reply to a specific message ID"),
	).
	EndSection().
	Build()

// EditMessageSchema is the UI schema for telegram-edit-message
var EditMessageSchema = resolver.NewSchemaBuilder("telegram-edit-message").
	WithName("Edit Telegram Message").
	WithCategory("action").
	WithIcon(iconTelegram).
	WithDescription("Edit a previously sent message").
	AddSection("Connection").
	AddExpressionField("botToken", "Bot Token",
		resolver.WithRequired(),
		resolver.WithSensitive(),
	).
	EndSection().
	AddSection("Message").
	AddExpressionField("chatId", "Chat ID",
		resolver.WithRequired(),
		resolver.WithHint("Chat ID where the message was sent"),
	).
	AddNumberField("messageId", "Message ID",
		resolver.WithRequired(),
		resolver.WithHint("ID of the message to edit"),
	).
	AddTextareaField("text", "New Text",
		resolver.WithRequired(),
		resolver.WithRows(4),
		resolver.WithHint("New text for the message"),
	).
	AddSelectField("parseMode", "Parse Mode",
		[]resolver.SelectOption{
			{Label: "Plain Text", Value: ""},
			{Label: "Markdown", Value: "Markdown"},
			{Label: "Markdown V2", Value: "MarkdownV2"},
			{Label: "HTML", Value: "HTML"},
		},
		resolver.WithDefault(""),
	).
	EndSection().
	AddSection("Options").
	AddJSONField("replyMarkup", "Reply Markup",
		resolver.WithHeight(100),
		resolver.WithHint("Optional new inline keyboard (JSON)"),
	).
	EndSection().
	Build()

// DeleteMessageSchema is the UI schema for telegram-delete-message
var DeleteMessageSchema = resolver.NewSchemaBuilder("telegram-delete-message").
	WithName("Delete Telegram Message").
	WithCategory("action").
	WithIcon(iconTelegram).
	WithDescription("Delete a message from a chat").
	AddSection("Connection").
	AddExpressionField("botToken", "Bot Token",
		resolver.WithRequired(),
		resolver.WithSensitive(),
	).
	EndSection().
	AddSection("Message").
	AddExpressionField("chatId", "Chat ID",
		resolver.WithRequired(),
		resolver.WithHint("Chat ID where the message was sent"),
	).
	AddNumberField("messageId", "Message ID",
		resolver.WithRequired(),
		resolver.WithHint("ID of the message to delete"),
	).
	EndSection().
	Build()

// SendPhotoSchema is the UI schema for telegram-send-photo
var SendPhotoSchema = resolver.NewSchemaBuilder("telegram-send-photo").
	WithName("Send Telegram Photo").
	WithCategory("action").
	WithIcon(iconTelegram).
	WithDescription("Send a photo to a Telegram chat").
	AddSection("Connection").
	AddExpressionField("botToken", "Bot Token",
		resolver.WithRequired(),
		resolver.WithSensitive(),
	).
	EndSection().
	AddSection("Photo").
	AddExpressionField("chatId", "Chat ID",
		resolver.WithRequired(),
		resolver.WithHint("Chat ID or username"),
	).
	AddExpressionField("photoUrl", "Photo URL",
		resolver.WithHint("URL of the photo to send (optional if using file ID)"),
	).
	AddExpressionField("photoFileId", "Photo File ID",
		resolver.WithHint("Existing file ID on Telegram servers"),
	).
	AddTextareaField("photoBase64", "Photo Base64",
		resolver.WithRows(4),
		resolver.WithHint("Base64 encoded image data"),
	).
	AddTextareaField("caption", "Caption",
		resolver.WithRows(2),
		resolver.WithHint("Optional caption for the photo"),
	).
	AddSelectField("parseMode", "Parse Mode",
		[]resolver.SelectOption{
			{Label: "Plain Text", Value: ""},
			{Label: "Markdown", Value: "Markdown"},
			{Label: "Markdown V2", Value: "MarkdownV2"},
			{Label: "HTML", Value: "HTML"},
		},
		resolver.WithDefault(""),
	).
	EndSection().
	AddSection("Options").
	AddToggleField("disableNotification", "Disable Notification",
		resolver.WithDefault(false),
	).
	AddNumberField("replyToMessageId", "Reply To Message ID",
		resolver.WithHint("Reply to a specific message"),
	).
	EndSection().
	Build()

// SendDocumentSchema is the UI schema for telegram-send-document
var SendDocumentSchema = resolver.NewSchemaBuilder("telegram-send-document").
	WithName("Send Telegram Document").
	WithCategory("action").
	WithIcon(iconTelegram).
	WithDescription("Send a document/file to a Telegram chat").
	AddSection("Connection").
	AddExpressionField("botToken", "Bot Token",
		resolver.WithRequired(),
		resolver.WithSensitive(),
	).
	EndSection().
	AddSection("Document").
	AddExpressionField("chatId", "Chat ID",
		resolver.WithRequired(),
		resolver.WithHint("Chat ID or username"),
	).
	AddExpressionField("documentUrl", "Document URL",
		resolver.WithHint("URL of the document to send"),
	).
	AddExpressionField("documentFileId", "Document File ID",
		resolver.WithHint("Existing file ID on Telegram servers"),
	).
	AddTextareaField("documentBase64", "Document Base64",
		resolver.WithRows(4),
		resolver.WithHint("Base64 encoded file data"),
	).
	AddExpressionField("fileName", "File Name",
		resolver.WithHint("Name of the file (required when sending base64)"),
	).
	AddTextareaField("caption", "Caption",
		resolver.WithRows(2),
		resolver.WithHint("Optional caption for the document"),
	).
	AddSelectField("parseMode", "Parse Mode",
		[]resolver.SelectOption{
			{Label: "Plain Text", Value: ""},
			{Label: "Markdown", Value: "Markdown"},
			{Label: "Markdown V2", Value: "MarkdownV2"},
			{Label: "HTML", Value: "HTML"},
		},
		resolver.WithDefault(""),
	).
	EndSection().
	AddSection("Options").
	AddToggleField("disableNotification", "Disable Notification",
		resolver.WithDefault(false),
	).
	AddNumberField("replyToMessageId", "Reply To Message ID",
		resolver.WithHint("Reply to a specific message"),
	).
	EndSection().
	Build()

// GetUpdatesSchema is the UI schema for telegram-get-updates
var GetUpdatesSchema = resolver.NewSchemaBuilder("telegram-get-updates").
	WithName("Get Telegram Updates").
	WithCategory("action").
	WithIcon(iconTelegram).
	WithDescription("Get updates from the Telegram Bot API (long polling)").
	AddSection("Connection").
	AddExpressionField("botToken", "Bot Token",
		resolver.WithRequired(),
		resolver.WithSensitive(),
	).
	EndSection().
	AddSection("Options").
	AddNumberField("offset", "Offset",
		resolver.WithHint("Identifier of the first update to return"),
	).
	AddNumberField("limit", "Limit",
		resolver.WithDefault(100),
		resolver.WithMinMax(1, 100),
		resolver.WithHint("Maximum number of updates to retrieve"),
	).
	AddNumberField("timeout", "Timeout",
		resolver.WithDefault(30),
		resolver.WithHint("Timeout in seconds for long polling"),
	).
	AddTagsField("allowedUpdates", "Allowed Updates",
		resolver.WithHint("Types of updates to receive (e.g., message, callback_query)"),
	).
	EndSection().
	Build()

// GetChatSchema is the UI schema for telegram-get-chat
var GetChatSchema = resolver.NewSchemaBuilder("telegram-get-chat").
	WithName("Get Telegram Chat Info").
	WithCategory("action").
	WithIcon(iconTelegram).
	WithDescription("Get information about a Telegram chat").
	AddSection("Connection").
	AddExpressionField("botToken", "Bot Token",
		resolver.WithRequired(),
		resolver.WithSensitive(),
	).
	EndSection().
	AddSection("Chat").
	AddExpressionField("chatId", "Chat ID",
		resolver.WithRequired(),
		resolver.WithPlaceholder("123456789 or @channelname"),
		resolver.WithHint("Chat ID or username"),
	).
	EndSection().
	Build()

// SetWebhookSchema is the UI schema for telegram-set-webhook
var SetWebhookSchema = resolver.NewSchemaBuilder("telegram-set-webhook").
	WithName("Set Telegram Webhook").
	WithCategory("action").
	WithIcon(iconTelegram).
	WithDescription("Set a webhook for the Telegram bot to receive updates").
	AddSection("Connection").
	AddExpressionField("botToken", "Bot Token",
		resolver.WithRequired(),
		resolver.WithSensitive(),
	).
	EndSection().
	AddSection("Webhook").
	AddExpressionField("url", "Webhook URL",
		resolver.WithRequired(),
		resolver.WithPlaceholder("https://your-domain.com/webhook"),
		resolver.WithHint("HTTPS URL to receive updates"),
	).
	AddTagsField("allowedUpdates", "Allowed Updates",
		resolver.WithHint("Types of updates to receive"),
	).
	AddExpressionField("secretToken", "Secret Token",
		resolver.WithHint("Secret token for webhook verification"),
	).
	EndSection().
	AddSection("Options").
	AddToggleField("dropPendingUpdates", "Drop Pending Updates",
		resolver.WithDefault(false),
		resolver.WithHint("Drop all pending updates before setting webhook"),
	).
	EndSection().
	Build()

// ============================================================================
// EXECUTORS
// ============================================================================

// SendMessageExecutor handles telegram-send-message
type SendMessageExecutor struct{}

func (e *SendMessageExecutor) Type() string { return "telegram-send-message" }

func (e *SendMessageExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	botToken, err := getBotToken(config, resolver)
	if err != nil {
		return nil, err
	}

	chatId := resolver.ResolveString(getString(config, "chatId"))
	if chatId == "" {
		return nil, fmt.Errorf("chat ID is required")
	}

	text := resolver.ResolveString(getString(config, "text"))
	if text == "" {
		return nil, fmt.Errorf("message text is required")
	}

	params := map[string]interface{}{
		"chat_id": chatId,
		"text":    text,
	}

	parseMode := resolver.ResolveString(getString(config, "parseMode"))
	if parseMode != "" {
		params["parse_mode"] = parseMode
	}

	if getBool(config, "disableNotification", false) {
		params["disable_notification"] = true
	}

	if getBool(config, "disableWebPagePreview", false) {
		params["disable_web_page_preview"] = true
	}

	replyToMessageId := getInt(config, "replyToMessageId", 0)
	if replyToMessageId > 0 {
		params["reply_to_message_id"] = replyToMessageId
	}

	replyMarkup := getString(config, "replyMarkup")
	if replyMarkup != "" {
		resolvedMarkup := resolver.ResolveString(replyMarkup)
		var markup interface{}
		if err := json.Unmarshal([]byte(resolvedMarkup), &markup); err == nil {
			params["reply_markup"] = markup
		}
	}

	body, err := makeTelegramRequest(botToken, "sendMessage", params)
	if err != nil {
		return nil, err
	}

	var message map[string]interface{}
	if err := parseTelegramResponse(body, &message); err != nil {
		return nil, err
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":   true,
			"message":   message,
			"chatId":    chatId,
			"messageId": int(message["message_id"].(float64)),
		},
	}, nil
}

// EditMessageExecutor handles telegram-edit-message
type EditMessageExecutor struct{}

func (e *EditMessageExecutor) Type() string { return "telegram-edit-message" }

func (e *EditMessageExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	botToken, err := getBotToken(config, resolver)
	if err != nil {
		return nil, err
	}

	chatId := resolver.ResolveString(getString(config, "chatId"))
	if chatId == "" {
		return nil, fmt.Errorf("chat ID is required")
	}

	messageId := getInt(config, "messageId", 0)
	if messageId == 0 {
		return nil, fmt.Errorf("message ID is required")
	}

	text := resolver.ResolveString(getString(config, "text"))
	if text == "" {
		return nil, fmt.Errorf("new text is required")
	}

	params := map[string]interface{}{
		"chat_id":    chatId,
		"message_id": messageId,
		"text":       text,
	}

	parseMode := resolver.ResolveString(getString(config, "parseMode"))
	if parseMode != "" {
		params["parse_mode"] = parseMode
	}

	replyMarkup := getString(config, "replyMarkup")
	if replyMarkup != "" {
		resolvedMarkup := resolver.ResolveString(replyMarkup)
		var markup interface{}
		if err := json.Unmarshal([]byte(resolvedMarkup), &markup); err == nil {
			params["reply_markup"] = markup
		}
	}

	body, err := makeTelegramRequest(botToken, "editMessageText", params)
	if err != nil {
		return nil, err
	}

	var result interface{}
	if err := parseTelegramResponse(body, &result); err != nil {
		return nil, err
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":   true,
			"chatId":    chatId,
			"messageId": messageId,
			"result":    result,
		},
	}, nil
}

// DeleteMessageExecutor handles telegram-delete-message
type DeleteMessageExecutor struct{}

func (e *DeleteMessageExecutor) Type() string { return "telegram-delete-message" }

func (e *DeleteMessageExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	botToken, err := getBotToken(config, resolver)
	if err != nil {
		return nil, err
	}

	chatId := resolver.ResolveString(getString(config, "chatId"))
	if chatId == "" {
		return nil, fmt.Errorf("chat ID is required")
	}

	messageId := getInt(config, "messageId", 0)
	if messageId == 0 {
		return nil, fmt.Errorf("message ID is required")
	}

	params := map[string]interface{}{
		"chat_id":    chatId,
		"message_id": messageId,
	}

	body, err := makeTelegramRequest(botToken, "deleteMessage", params)
	if err != nil {
		return nil, err
	}

	var deleted bool
	if err := parseTelegramResponse(body, &deleted); err != nil {
		return nil, err
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":   deleted,
			"chatId":    chatId,
			"messageId": messageId,
		},
	}, nil
}

// SendPhotoExecutor handles telegram-send-photo
type SendPhotoExecutor struct{}

func (e *SendPhotoExecutor) Type() string { return "telegram-send-photo" }

func (e *SendPhotoExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	botToken, err := getBotToken(config, resolver)
	if err != nil {
		return nil, err
	}

	chatId := resolver.ResolveString(getString(config, "chatId"))
	if chatId == "" {
		return nil, fmt.Errorf("chat ID is required")
	}

	photoUrl := resolver.ResolveString(getString(config, "photoUrl"))
	photoFileId := resolver.ResolveString(getString(config, "photoFileId"))
	photoBase64 := resolver.ResolveString(getString(config, "photoBase64"))

	params := map[string]string{
		"chat_id": chatId,
	}

	caption := resolver.ResolveString(getString(config, "caption"))
	if caption != "" {
		params["caption"] = caption
	}

	parseMode := resolver.ResolveString(getString(config, "parseMode"))
	if parseMode != "" {
		params["parse_mode"] = parseMode
	}

	if getBool(config, "disableNotification", false) {
		params["disable_notification"] = "true"
	}

	replyToMessageId := getInt(config, "replyToMessageId", 0)
	if replyToMessageId > 0 {
		params["reply_to_message_id"] = fmt.Sprintf("%d", replyToMessageId)
	}

	var body []byte
	var photoData []byte
	var fileName string

	// Determine photo source
	if photoFileId != "" {
		// Use existing file ID
		params["photo"] = photoFileId
		body, err = makeTelegramRequest(botToken, "sendPhoto", stringMapToInterface(params))
	} else if photoUrl != "" {
		// Use URL
		params["photo"] = photoUrl
		body, err = makeTelegramRequest(botToken, "sendPhoto", stringMapToInterface(params))
	} else if photoBase64 != "" {
		// Use base64 data
		photoData, err = decodeBase64File(photoBase64)
		if err != nil {
			return nil, fmt.Errorf("failed to decode photo: %w", err)
		}
		fileName = "photo.jpg"
		body, err = makeTelegramMultipartRequest(botToken, "sendPhoto", params, "photo", fileName, photoData)
	} else {
		return nil, fmt.Errorf("photo URL, file ID, or base64 data is required")
	}

	if err != nil {
		return nil, err
	}

	var message map[string]interface{}
	if err := parseTelegramResponse(body, &message); err != nil {
		return nil, err
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":   true,
			"message":   message,
			"chatId":    chatId,
			"messageId": int(getFloat64(message, "message_id")),
		},
	}, nil
}

// SendDocumentExecutor handles telegram-send-document
type SendDocumentExecutor struct{}

func (e *SendDocumentExecutor) Type() string { return "telegram-send-document" }

func (e *SendDocumentExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	botToken, err := getBotToken(config, resolver)
	if err != nil {
		return nil, err
	}

	chatId := resolver.ResolveString(getString(config, "chatId"))
	if chatId == "" {
		return nil, fmt.Errorf("chat ID is required")
	}

	documentUrl := resolver.ResolveString(getString(config, "documentUrl"))
	documentFileId := resolver.ResolveString(getString(config, "documentFileId"))
	documentBase64 := resolver.ResolveString(getString(config, "documentBase64"))
	fileName := resolver.ResolveString(getString(config, "fileName"))

	params := map[string]string{
		"chat_id": chatId,
	}

	caption := resolver.ResolveString(getString(config, "caption"))
	if caption != "" {
		params["caption"] = caption
	}

	parseMode := resolver.ResolveString(getString(config, "parseMode"))
	if parseMode != "" {
		params["parse_mode"] = parseMode
	}

	if getBool(config, "disableNotification", false) {
		params["disable_notification"] = "true"
	}

	replyToMessageId := getInt(config, "replyToMessageId", 0)
	if replyToMessageId > 0 {
		params["reply_to_message_id"] = fmt.Sprintf("%d", replyToMessageId)
	}

	var body []byte
	var documentData []byte

	// Determine document source
	if documentFileId != "" {
		// Use existing file ID
		params["document"] = documentFileId
		body, err = makeTelegramRequest(botToken, "sendDocument", stringMapToInterface(params))
	} else if documentUrl != "" {
		// Use URL
		params["document"] = documentUrl
		body, err = makeTelegramRequest(botToken, "sendDocument", stringMapToInterface(params))
	} else if documentBase64 != "" {
		// Use base64 data
		documentData, err = decodeBase64File(documentBase64)
		if err != nil {
			return nil, fmt.Errorf("failed to decode document: %w", err)
		}
		if fileName == "" {
			fileName = "document"
		}
		body, err = makeTelegramMultipartRequest(botToken, "sendDocument", params, "document", fileName, documentData)
	} else {
		return nil, fmt.Errorf("document URL, file ID, or base64 data is required")
	}

	if err != nil {
		return nil, err
	}

	var message map[string]interface{}
	if err := parseTelegramResponse(body, &message); err != nil {
		return nil, err
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":   true,
			"message":   message,
			"chatId":    chatId,
			"messageId": int(getFloat64(message, "message_id")),
		},
	}, nil
}

// GetUpdatesExecutor handles telegram-get-updates
type GetUpdatesExecutor struct{}

func (e *GetUpdatesExecutor) Type() string { return "telegram-get-updates" }

func (e *GetUpdatesExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	botToken, err := getBotToken(config, resolver)
	if err != nil {
		return nil, err
	}

	params := map[string]interface{}{}

	offset := getInt(config, "offset", 0)
	if offset > 0 {
		params["offset"] = offset
	}

	limit := getInt(config, "limit", 100)
	if limit > 0 {
		params["limit"] = limit
	}

	timeout := getInt(config, "timeout", 30)
	if timeout > 0 {
		params["timeout"] = timeout
	}

	allowedUpdates := getStringSlice(config, "allowedUpdates")
	if len(allowedUpdates) > 0 {
		params["allowed_updates"] = allowedUpdates
	}

	body, err := makeTelegramRequest(botToken, "getUpdates", params)
	if err != nil {
		return nil, err
	}

	var updates []map[string]interface{}
	if err := parseTelegramResponse(body, &updates); err != nil {
		return nil, err
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success": true,
			"updates": updates,
			"count":   len(updates),
		},
	}, nil
}

// GetChatExecutor handles telegram-get-chat
type GetChatExecutor struct{}

func (e *GetChatExecutor) Type() string { return "telegram-get-chat" }

func (e *GetChatExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	botToken, err := getBotToken(config, resolver)
	if err != nil {
		return nil, err
	}

	chatId := resolver.ResolveString(getString(config, "chatId"))
	if chatId == "" {
		return nil, fmt.Errorf("chat ID is required")
	}

	params := map[string]interface{}{
		"chat_id": chatId,
	}

	body, err := makeTelegramRequest(botToken, "getChat", params)
	if err != nil {
		return nil, err
	}

	var chat map[string]interface{}
	if err := parseTelegramResponse(body, &chat); err != nil {
		return nil, err
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success": true,
			"chat":    chat,
			"chatId":  chatId,
		},
	}, nil
}

// SetWebhookExecutor handles telegram-set-webhook
type SetWebhookExecutor struct{}

func (e *SetWebhookExecutor) Type() string { return "telegram-set-webhook" }

func (e *SetWebhookExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	botToken, err := getBotToken(config, resolver)
	if err != nil {
		return nil, err
	}

	url := resolver.ResolveString(getString(config, "url"))
	if url == "" {
		return nil, fmt.Errorf("webhook URL is required")
	}

	params := map[string]interface{}{
		"url": url,
	}

	allowedUpdates := getStringSlice(config, "allowedUpdates")
	if len(allowedUpdates) > 0 {
		params["allowed_updates"] = allowedUpdates
	}

	secretToken := resolver.ResolveString(getString(config, "secretToken"))
	if secretToken != "" {
		params["secret_token"] = secretToken
	}

	if getBool(config, "dropPendingUpdates", false) {
		params["drop_pending_updates"] = true
	}

	body, err := makeTelegramRequest(botToken, "setWebhook", params)
	if err != nil {
		return nil, err
	}

	var result bool
	if err := parseTelegramResponse(body, &result); err != nil {
		return nil, err
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success": result,
			"url":     url,
		},
	}, nil
}

// ============================================================================
// HELPER FUNCTIONS
// ============================================================================

// stringMapToInterface converts map[string]string to map[string]interface{}
func stringMapToInterface(m map[string]string) map[string]interface{} {
	result := make(map[string]interface{})
	for k, v := range m {
		result[k] = v
	}
	return result
}

// getFloat64 safely gets a float64 from a map
func getFloat64(m map[string]interface{}, key string) float64 {
	if v, ok := m[key]; ok {
		switch val := v.(type) {
		case float64:
			return val
		case int:
			return float64(val)
		case int64:
			return float64(val)
		}
	}
	return 0
}

// decodeBase64File decodes a base64 string, handling data URI prefixes
func decodeBase64File(data string) ([]byte, error) {
	// Handle data URI prefix (e.g., "data:image/jpeg;base64,")
	if idx := strings.Index(data, ","); idx != -1 {
		data = data[idx+1:]
	}
	return base64Decode(data)
}

// base64Decode decodes a base64 string
func base64Decode(data string) ([]byte, error) {
	// Standard base64
	decoded, err := base64.StdEncoding.DecodeString(data)
	if err == nil {
		return decoded, nil
	}
	// URL-safe base64
	return base64.URLEncoding.DecodeString(data)
}
