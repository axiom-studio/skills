package main

import (
	"context"
	"fmt"
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
	server := grpc.NewSkillServer("skill-discord", "1.0.0")

	// Register Discord executors with schemas
	server.RegisterExecutorWithSchema("discord-send", &DiscordSendExecutor{}, DiscordSendSchema)
	server.RegisterExecutorWithSchema("discord-embed", &DiscordEmbedExecutor{}, DiscordEmbedSchema)
	server.RegisterExecutorWithSchema("discord-react", &DiscordReactExecutor{}, DiscordReactSchema)
	server.RegisterExecutorWithSchema("discord-channel", &DiscordChannelExecutor{}, DiscordChannelSchema)
	server.RegisterExecutorWithSchema("discord-user", &DiscordUserExecutor{}, DiscordUserSchema)
	server.RegisterExecutorWithSchema("discord-webhook", &DiscordWebhookExecutor{}, DiscordWebhookSchema)

	fmt.Printf("Starting skill-discord gRPC server on port %s\n", port)
	if err := server.Serve(port); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to serve: %v\n", err)
		os.Exit(1)
	}
}

// ============================================================================
// Discord Send Executor
// ============================================================================

// DiscordSendExecutor handles discord-send node type
type DiscordSendExecutor struct{}

// DiscordSendConfig defines the typed configuration for discord-send
type DiscordSendConfig struct {
	Token       string `json:"token" description:"Discord bot token, supports {{secrets.xxx}}"`
	ChannelID   string `json:"channelId" description:"Channel ID to send message to"`
	Content     string `json:"content" description:"Message content to send"`
	TTS         bool   `json:"tts" default:"false" description:"Send as text-to-speech"`
	ReplyTo     string `json:"replyTo" description:"Message ID to reply to (optional)"`
	MentionUser bool   `json:"mentionUser" default:"false" description:"Mention user when replying"`
}

// DiscordSendSchema is the UI schema for discord-send
var DiscordSendSchema = resolver.NewSchemaBuilder("discord-send").
	WithName("Send Discord Message").
	WithCategory("discord").
	WithIcon("message-square").
	WithDescription("Send a text message to a Discord channel").
	AddSection("Authentication").
		AddTextField("token", "Bot Token",
			resolver.WithRequired(),
			resolver.WithPlaceholder("MTAx..."),
			resolver.WithHint("Use {{secrets.discord_token}} for secure storage"),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Message").
		AddTextField("channelId", "Channel ID",
			resolver.WithRequired(),
			resolver.WithPlaceholder("123456789012345678"),
			resolver.WithHint("Right-click channel > Copy ID"),
		).
		AddTextareaField("content", "Content",
			resolver.WithRequired(),
			resolver.WithRows(4),
			resolver.WithPlaceholder("Hello from Atlas!"),
			resolver.WithHint("Supports Markdown formatting"),
		).
		AddToggleField("tts", "Text-to-Speech",
			resolver.WithDefault(false),
		).
		EndSection().
	AddSection("Reply Options").
		AddTextField("replyTo", "Reply To Message ID",
			resolver.WithPlaceholder("123456789012345678"),
			resolver.WithHint("Leave empty to send as new message"),
		).
		AddToggleField("mentionUser", "Mention User",
			resolver.WithDefault(false),
			resolver.WithHint("Mention the user when replying"),
		).
		EndSection().
	Build()

func (e *DiscordSendExecutor) Type() string { return "discord-send" }

func (e *DiscordSendExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	// Parse config into typed struct
	var cfg DiscordSendConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	} else {
		return nil, fmt.Errorf("invalid resolver type")
	}

	if cfg.Token == "" {
		return nil, fmt.Errorf("token is required")
	}
	if cfg.ChannelID == "" {
		return nil, fmt.Errorf("channelId is required")
	}
	if cfg.Content == "" {
		return nil, fmt.Errorf("content is required")
	}

	// Build message payload
	payload := map[string]interface{}{
		"content": cfg.Content,
		"tts":     cfg.TTS,
	}

	// Add reply reference if specified
	if cfg.ReplyTo != "" {
		payload["message_reference"] = map[string]interface{}{
			"message_id": cfg.ReplyTo,
		}
		if cfg.MentionUser {
			payload["allowed_mentions"] = map[string]interface{}{
				"replied_user": true,
			}
		}
	}

	// Send message via Discord API
	result, err := callDiscordAPI(ctx, cfg.Token, "POST",
		fmt.Sprintf("/channels/%s/messages", cfg.ChannelID), payload)
	if err != nil {
		return nil, fmt.Errorf("failed to send message: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"messageId":   result["id"],
			"channelId":   cfg.ChannelID,
			"content":     cfg.Content,
			"timestamp":   result["timestamp"],
			"success":     true,
		},
	}, nil
}

// ============================================================================
// Discord Embed Executor
// ============================================================================

// DiscordEmbedExecutor handles discord-embed node type
type DiscordEmbedExecutor struct{}

// DiscordEmbedConfig defines the typed configuration for discord-embed
type DiscordEmbedConfig struct {
	Token         string                 `json:"token" description:"Discord bot token"`
	ChannelID     string                 `json:"channelId" description:"Channel ID to send embed to"`
	Content       string                 `json:"content" description:"Message content above embed (optional)"`
	Title         string                 `json:"title" description:"Embed title"`
	Description   string                 `json:"description" description:"Embed description"`
	Color         string                 `json:"color" default:"#5865F2" description:"Embed color (hex)"`
	URL           string                 `json:"url" description:"Embed title URL (optional)"`
	AuthorName    string                 `json:"authorName" description:"Author name (optional)"`
	AuthorURL     string                 `json:"authorURL" description:"Author URL (optional)"`
	AuthorIcon    string                 `json:"authorIcon" description:"Author icon URL (optional)"`
	FooterText    string                 `json:"footerText" description:"Footer text (optional)"`
	FooterIcon    string                 `json:"footerIcon" description:"Footer icon URL (optional)"`
	Thumbnail     string                 `json:"thumbnail" description:"Thumbnail image URL (optional)"`
	Image         string                 `json:"image" description:"Main image URL (optional)"`
	Fields        []EmbedFieldConfig     `json:"fields" description:"Embed fields (optional)"`
	Timestamp     bool                   `json:"timestamp" default:"true" description:"Include current timestamp"`
}

// EmbedFieldConfig defines a single embed field
type EmbedFieldConfig struct {
	Name   string `json:"name" description:"Field name"`
	Value  string `json:"value" description:"Field value"`
	Inline bool   `json:"inline" default:"false" description:"Display inline"`
}

// DiscordEmbedSchema is the UI schema for discord-embed
var DiscordEmbedSchema = resolver.NewSchemaBuilder("discord-embed").
	WithName("Send Discord Embed").
	WithCategory("discord").
	WithIcon("box").
	WithDescription("Send a rich embed message to Discord").
	AddSection("Authentication").
		AddTextField("token", "Bot Token",
			resolver.WithRequired(),
			resolver.WithSensitive(),
		).
		AddTextField("channelId", "Channel ID",
			resolver.WithRequired(),
		).
		EndSection().
	AddSection("Message").
		AddTextareaField("content", "Message Content",
			resolver.WithRows(2),
			resolver.WithPlaceholder("Optional text above the embed"),
		).
		EndSection().
	AddSection("Embed Content").
		AddTextField("title", "Title",
			resolver.WithRequired(),
			resolver.WithPlaceholder("Amazing Embed"),
		).
		AddTextareaField("description", "Description",
			resolver.WithRequired(),
			resolver.WithRows(3),
			resolver.WithPlaceholder("Embed description text"),
		).
		AddTextField("color", "Color",
			resolver.WithDefault("#5865F2"),
			resolver.WithPlaceholder("#5865F2"),
			resolver.WithHint("Hex color code"),
		).
		AddTextField("url", "Title URL",
			resolver.WithPlaceholder("https://example.com"),
		).
		EndSection().
	AddSection("Author").
		AddTextField("authorName", "Author Name",
			resolver.WithPlaceholder("Bot Name"),
		).
		AddTextField("authorURL", "Author URL",
			resolver.WithPlaceholder("https://example.com"),
		).
		AddTextField("authorIcon", "Author Icon URL",
			resolver.WithPlaceholder("https://example.com/icon.png"),
		).
		EndSection().
	AddSection("Footer & Images").
		AddTextField("footerText", "Footer Text",
			resolver.WithPlaceholder("Powered by Atlas"),
		).
		AddTextField("footerIcon", "Footer Icon URL",
			resolver.WithPlaceholder("https://example.com/footer.png"),
		).
		AddTextField("thumbnail", "Thumbnail URL",
			resolver.WithPlaceholder("https://example.com/thumb.png"),
		).
		AddTextField("image", "Main Image URL",
			resolver.WithPlaceholder("https://example.com/image.png"),
		).
		EndSection().
	AddSection("Fields").
		AddJSONField("fields", "Fields",
			resolver.WithHint("Array of {name, value, inline} objects"),
			resolver.WithPlaceholder(`[{"name": "Field 1", "value": "Value 1", "inline": true}]`),
		).
		AddToggleField("timestamp", "Include Timestamp",
			resolver.WithDefault(true),
		).
		EndSection().
	Build()

func (e *DiscordEmbedExecutor) Type() string { return "discord-embed" }

func (e *DiscordEmbedExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	// Parse config
	var cfg DiscordEmbedConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	}

	if cfg.Token == "" {
		return nil, fmt.Errorf("token is required")
	}
	if cfg.ChannelID == "" {
		return nil, fmt.Errorf("channelId is required")
	}
	if cfg.Title == "" {
		return nil, fmt.Errorf("title is required")
	}

	// Build embed object
	embed := map[string]interface{}{
		"title":       cfg.Title,
		"description": cfg.Description,
	}

	// Parse and set color
	if cfg.Color != "" {
		if colorInt := hexToColorInt(cfg.Color); colorInt >= 0 {
			embed["color"] = colorInt
		}
	}

	// Add URL if specified
	if cfg.URL != "" {
		embed["url"] = cfg.URL
	}

	// Add author if specified
	if cfg.AuthorName != "" {
		author := map[string]interface{}{"name": cfg.AuthorName}
		if cfg.AuthorURL != "" {
			author["url"] = cfg.AuthorURL
		}
		if cfg.AuthorIcon != "" {
			author["icon_url"] = cfg.AuthorIcon
		}
		embed["author"] = author
	}

	// Add footer if specified
	if cfg.FooterText != "" {
		footer := map[string]interface{}{"text": cfg.FooterText}
		if cfg.FooterIcon != "" {
			footer["icon_url"] = cfg.FooterIcon
		}
		embed["footer"] = footer
	}

	// Add thumbnail if specified
	if cfg.Thumbnail != "" {
		embed["thumbnail"] = map[string]interface{}{"url": cfg.Thumbnail}
	}

	// Add image if specified
	if cfg.Image != "" {
		embed["image"] = map[string]interface{}{"url": cfg.Image}
	}

	// Add fields if specified
	if len(cfg.Fields) > 0 {
		fields := make([]map[string]interface{}, 0, len(cfg.Fields))
		for _, f := range cfg.Fields {
			field := map[string]interface{}{
				"name":   f.Name,
				"value":  f.Value,
				"inline": f.Inline,
			}
			fields = append(fields, field)
		}
		embed["fields"] = fields
	}

	// Add timestamp if enabled
	if cfg.Timestamp {
		embed["timestamp"] = "{{.Now}}" // Will be resolved by Discord API
	}

	// Build message payload
	payload := map[string]interface{}{
		"embeds": []map[string]interface{}{embed},
	}
	if cfg.Content != "" {
		payload["content"] = cfg.Content
	}

	// Send message via Discord API
	result, err := callDiscordAPI(ctx, cfg.Token, "POST",
		fmt.Sprintf("/channels/%s/messages", cfg.ChannelID), payload)
	if err != nil {
		return nil, fmt.Errorf("failed to send embed: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"messageId": result["id"],
			"channelId": cfg.ChannelID,
			"title":     cfg.Title,
			"timestamp": result["timestamp"],
			"success":   true,
		},
	}, nil
}

// hexToColorInt converts a hex color string to Discord's integer format
func hexToColorInt(hex string) int {
	// Remove # prefix if present
	if len(hex) > 0 && hex[0] == '#' {
		hex = hex[1:]
	}
	
	// Parse hex color
	var r, g, b int
	if len(hex) == 6 {
		fmt.Sscanf(hex, "%02x%02x%02x", &r, &g, &b)
		return (r << 16) + (g << 8) + b
	} else if len(hex) == 3 {
		fmt.Sscanf(hex, "%1x%1x%1x", &r, &g, &b)
		r *= 17
		g *= 17
		b *= 17
		return (r << 16) + (g << 8) + b
	}
	return -1
}

// ============================================================================
// Discord React Executor
// ============================================================================

// DiscordReactExecutor handles discord-react node type
type DiscordReactExecutor struct{}

// DiscordReactConfig defines the typed configuration for discord-react
type DiscordReactConfig struct {
	Token       string `json:"token" description:"Discord bot token"`
	ChannelID   string `json:"channelId" description:"Channel ID containing the message"`
	MessageID   string `json:"messageId" description:"Message ID to react to"`
	Emoji       string `json:"emoji" description:"Emoji to react with (unicode or custom emoji ID)"`
	EmojiType   string `json:"emojiType" default:"unicode" options:"unicode,custom" description:"Type of emoji"`
	EmojiName   string `json:"emojiName" description:"Custom emoji name (for custom emojis)"`
}

// DiscordReactSchema is the UI schema for discord-react
var DiscordReactSchema = resolver.NewSchemaBuilder("discord-react").
	WithName("Add Discord Reaction").
	WithCategory("discord").
	WithIcon("smile").
	WithDescription("Add a reaction emoji to a Discord message").
	AddSection("Authentication").
		AddTextField("token", "Bot Token",
			resolver.WithRequired(),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Target").
		AddTextField("channelId", "Channel ID",
			resolver.WithRequired(),
		).
		AddTextField("messageId", "Message ID",
			resolver.WithRequired(),
			resolver.WithHint("Right-click message > Copy ID"),
		).
		EndSection().
	AddSection("Reaction").
		AddSelectField("emojiType", "Emoji Type", []resolver.SelectOption{
			{Label: "Unicode Emoji", Value: "unicode", Icon: "smile"},
			{Label: "Custom Emoji", Value: "custom", Icon: "image"},
		}, resolver.WithDefault("unicode")).
		AddTextField("emoji", "Emoji",
			resolver.WithRequired(),
			resolver.WithPlaceholder("👍 or <:emojiName:123456789>"),
			resolver.WithHint("Unicode emoji or custom emoji ID"),
		).
		AddTextField("emojiName", "Custom Emoji Name",
			resolver.WithPlaceholder("emoji_name"),
			resolver.WithHint("Required for custom emojis"),
		).
		EndSection().
	Build()

func (e *DiscordReactExecutor) Type() string { return "discord-react" }

func (e *DiscordReactExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	// Parse config
	var cfg DiscordReactConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	}

	if cfg.Token == "" {
		return nil, fmt.Errorf("token is required")
	}
	if cfg.ChannelID == "" {
		return nil, fmt.Errorf("channelId is required")
	}
	if cfg.MessageID == "" {
		return nil, fmt.Errorf("messageId is required")
	}
	if cfg.Emoji == "" {
		return nil, fmt.Errorf("emoji is required")
	}

	// Format emoji for API
	emojiStr := cfg.Emoji
	if cfg.EmojiType == "custom" && cfg.EmojiName != "" {
		emojiStr = fmt.Sprintf("%s:%s", cfg.EmojiName, cfg.Emoji)
	}

	// URL encode emoji for path
	emojiEncoded := urlEncodeEmoji(emojiStr)

	// Add reaction via Discord API
	_, err := callDiscordAPI(ctx, cfg.Token, "PUT",
		fmt.Sprintf("/channels/%s/messages/%s/reactions/%s/@me", cfg.ChannelID, cfg.MessageID, emojiEncoded),
		nil)
	if err != nil {
		return nil, fmt.Errorf("failed to add reaction: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"messageId": cfg.MessageID,
			"channelId": cfg.ChannelID,
			"emoji":     emojiStr,
			"success":   true,
		},
	}, nil
}

// urlEncodeEmoji URL-encodes an emoji string for Discord API paths
func urlEncodeEmoji(emoji string) string {
	// Simple URL encoding for emoji
	// In production, use proper URL encoding
	result := ""
	for _, r := range emoji {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == ':' {
			result += string(r)
		} else {
			// Simple percent encoding for non-ASCII
			result += fmt.Sprintf("%%%02X", r)
		}
	}
	return result
}

// ============================================================================
// Discord Channel Executor
// ============================================================================

// DiscordChannelExecutor handles discord-channel node type
type DiscordChannelExecutor struct{}

// DiscordChannelConfig defines the typed configuration for discord-channel
type DiscordChannelConfig struct {
	Token       string `json:"token" description:"Discord bot token"`
	GuildID     string `json:"guildId" description:"Server/Guild ID"`
	Action      string `json:"action" options:"create,list,get,delete,update" description:"Channel action to perform"`
	ChannelID   string `json:"channelId" description:"Channel ID (for get/delete/update)"`
	Name        string `json:"name" description:"Channel name (for create)"`
	ChannelType string `json:"channelType" default:"text" options:"text,voice,category,announcement" description:"Channel type (for create)"`
	Topic       string `json:"topic" description:"Channel topic (for create/update)"`
	NSFW        bool   `json:"nsfw" default:"false" description:"Mark as NSFW"`
	Reason      string `json:"reason" description:"Audit log reason"`
}

// DiscordChannelSchema is the UI schema for discord-channel
var DiscordChannelSchema = resolver.NewSchemaBuilder("discord-channel").
	WithName("Discord Channel Operations").
	WithCategory("discord").
	WithIcon("hash").
	WithDescription("Manage Discord channels (create, list, get, delete, update)").
	AddSection("Authentication").
		AddTextField("token", "Bot Token",
			resolver.WithRequired(),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Target").
		AddTextField("guildId", "Guild/Server ID",
			resolver.WithRequired(),
			resolver.WithHint("Right-click server > Copy ID"),
		).
		AddSelectField("action", "Action", []resolver.SelectOption{
			{Label: "Create Channel", Value: "create", Icon: "plus-circle"},
			{Label: "List Channels", Value: "list", Icon: "list"},
			{Label: "Get Channel", Value: "get", Icon: "info"},
			{Label: "Delete Channel", Value: "delete", Icon: "trash"},
			{Label: "Update Channel", Value: "update", Icon: "edit"},
		}, resolver.WithRequired()).
		EndSection().
	AddSection("Channel").
		AddTextField("channelId", "Channel ID",
			resolver.WithHint("Required for get/delete/update actions"),
		).
		AddTextField("name", "Channel Name",
			resolver.WithHint("Required for create action"),
		).
		AddSelectField("channelType", "Channel Type", []resolver.SelectOption{
			{Label: "Text", Value: "text"},
			{Label: "Voice", Value: "voice"},
			{Label: "Category", Value: "category"},
			{Label: "Announcement", Value: "announcement"},
		}, resolver.WithDefault("text")).
		AddTextareaField("topic", "Topic",
			resolver.WithRows(2),
			resolver.WithHint("Channel description (for create/update)"),
		).
		AddToggleField("nsfw", "Mark as NSFW",
			resolver.WithDefault(false),
		).
		EndSection().
	AddSection("Audit Log").
		AddTextField("reason", "Reason",
			resolver.WithPlaceholder("Automated action by Atlas"),
			resolver.WithHint("Reason for audit log"),
		).
		EndSection().
	Build()

func (e *DiscordChannelExecutor) Type() string { return "discord-channel" }

func (e *DiscordChannelExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	// Parse config
	var cfg DiscordChannelConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	}

	if cfg.Token == "" {
		return nil, fmt.Errorf("token is required")
	}
	if cfg.GuildID == "" {
		return nil, fmt.Errorf("guildId is required")
	}
	if cfg.Action == "" {
		return nil, fmt.Errorf("action is required")
	}

	headers := map[string]string{}
	if cfg.Reason != "" {
		headers["X-Audit-Log-Reason"] = cfg.Reason
	}

	var result map[string]interface{}
	var err error

	switch cfg.Action {
	case "create":
		if cfg.Name == "" {
			return nil, fmt.Errorf("name is required for create action")
		}
		channelType := 0 // Text
		switch cfg.ChannelType {
		case "voice":
			channelType = 2
		case "category":
			channelType = 4
		case "announcement":
			channelType = 5
		}
		payload := map[string]interface{}{
			"name": cfg.Name,
			"type": channelType,
		}
		if cfg.Topic != "" {
			payload["topic"] = cfg.Topic
		}
		if cfg.NSFW {
			payload["nsfw"] = true
		}
		result, err = callDiscordAPIWithHeaders(ctx, cfg.Token, "POST",
			fmt.Sprintf("/guilds/%s/channels", cfg.GuildID), payload, headers)

	case "list":
		result, err = callDiscordAPI(ctx, cfg.Token, "GET",
			fmt.Sprintf("/guilds/%s/channels", cfg.GuildID), nil)

	case "get":
		if cfg.ChannelID == "" {
			return nil, fmt.Errorf("channelId is required for get action")
		}
		result, err = callDiscordAPI(ctx, cfg.Token, "GET",
			fmt.Sprintf("/channels/%s", cfg.ChannelID), nil)

	case "delete":
		if cfg.ChannelID == "" {
			return nil, fmt.Errorf("channelId is required for delete action")
		}
		_, err = callDiscordAPIWithHeaders(ctx, cfg.Token, "DELETE",
			fmt.Sprintf("/channels/%s", cfg.ChannelID), nil, headers)
		result = map[string]interface{}{"deleted": true, "channelId": cfg.ChannelID}

	case "update":
		if cfg.ChannelID == "" {
			return nil, fmt.Errorf("channelId is required for update action")
		}
		payload := map[string]interface{}{}
		if cfg.Name != "" {
			payload["name"] = cfg.Name
		}
		if cfg.Topic != "" {
			payload["topic"] = cfg.Topic
		}
		if cfg.NSFW {
			payload["nsfw"] = true
		}
		result, err = callDiscordAPIWithHeaders(ctx, cfg.Token, "PATCH",
			fmt.Sprintf("/channels/%s", cfg.ChannelID), payload, headers)

	default:
		return nil, fmt.Errorf("unknown action: %s", cfg.Action)
	}

	if err != nil {
		return nil, fmt.Errorf("channel operation failed: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"action":  cfg.Action,
			"channel": result,
			"success": true,
		},
	}, nil
}

// ============================================================================
// Discord User Executor
// ============================================================================

// DiscordUserExecutor handles discord-user node type
type DiscordUserExecutor struct{}

// DiscordUserConfig defines the typed configuration for discord-user
type DiscordUserConfig struct {
	Token     string `json:"token" description:"Discord bot token"`
	GuildID   string `json:"guildId" description:"Server/Guild ID"`
	Action    string `json:"action" options:"get,kick,ban,unban,timeout,get-role,add-role,remove-role" description:"User action to perform"`
	UserID    string `json:"userId" description:"User ID"`
	Reason    string `json:"reason" description:"Audit log reason"`
	Duration  int    `json:"duration" description:"Timeout duration in seconds"`
	RoleID    string `json:"roleId" description:"Role ID (for role operations)"`
	DeleteDays int    `json:"deleteDays" default:"0" description:"Days of messages to delete (for ban)"`
}

// DiscordUserSchema is the UI schema for discord-user
var DiscordUserSchema = resolver.NewSchemaBuilder("discord-user").
	WithName("Discord User Operations").
	WithCategory("discord").
	WithIcon("users").
	WithDescription("Manage Discord users (get, kick, ban, timeout, roles)").
	AddSection("Authentication").
		AddTextField("token", "Bot Token",
			resolver.WithRequired(),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Target").
		AddTextField("guildId", "Guild/Server ID",
			resolver.WithRequired(),
		).
		AddSelectField("action", "Action", []resolver.SelectOption{
			{Label: "Get User Info", Value: "get", Icon: "user"},
			{Label: "Kick User", Value: "kick", Icon: "log-out"},
			{Label: "Ban User", Value: "ban", Icon: "ban"},
			{Label: "Unban User", Value: "unban", Icon: "check-circle"},
			{Label: "Timeout User", Value: "timeout", Icon: "clock"},
			{Label: "Get User Roles", Value: "get-role", Icon: "shield"},
			{Label: "Add Role", Value: "add-role", Icon: "plus-circle"},
			{Label: "Remove Role", Value: "remove-role", Icon: "minus-circle"},
		}, resolver.WithRequired()).
		EndSection().
	AddSection("User & Role").
		AddTextField("userId", "User ID",
			resolver.WithRequired(),
			resolver.WithHint("Right-click user > Copy ID"),
		).
		AddTextField("roleId", "Role ID",
			resolver.WithHint("Required for role operations"),
		).
		EndSection().
	AddSection("Options").
		AddTextField("reason", "Reason",
			resolver.WithPlaceholder("Violated server rules"),
			resolver.WithHint("Audit log reason"),
		).
		AddNumberField("duration", "Timeout Duration (seconds)",
			resolver.WithHint("For timeout action"),
		).
		AddNumberField("deleteDays", "Delete Messages (days)",
			resolver.WithDefault(0),
			resolver.WithHint("Days of messages to delete on ban (0-7)"),
		).
		EndSection().
	Build()

func (e *DiscordUserExecutor) Type() string { return "discord-user" }

func (e *DiscordUserExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	// Parse config
	var cfg DiscordUserConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	}

	if cfg.Token == "" {
		return nil, fmt.Errorf("token is required")
	}
	if cfg.GuildID == "" {
		return nil, fmt.Errorf("guildId is required")
	}
	if cfg.Action == "" {
		return nil, fmt.Errorf("action is required")
	}
	if cfg.UserID == "" {
		return nil, fmt.Errorf("userId is required")
	}

	headers := map[string]string{}
	if cfg.Reason != "" {
		headers["X-Audit-Log-Reason"] = cfg.Reason
	}

	var result map[string]interface{}
	var err error

	switch cfg.Action {
	case "get":
		result, err = callDiscordAPI(ctx, cfg.Token, "GET",
			fmt.Sprintf("/guilds/%s/members/%s", cfg.GuildID, cfg.UserID), nil)

	case "kick":
		_, err = callDiscordAPIWithHeaders(ctx, cfg.Token, "DELETE",
			fmt.Sprintf("/guilds/%s/members/%s", cfg.GuildID, cfg.UserID), nil, headers)
		result = map[string]interface{}{"kicked": true, "userId": cfg.UserID}

	case "ban":
		params := ""
		if cfg.DeleteDays > 0 {
			if cfg.DeleteDays > 7 {
				cfg.DeleteDays = 7
			}
			params = fmt.Sprintf("?delete_message_days=%d", cfg.DeleteDays)
		}
		_, err = callDiscordAPIWithHeaders(ctx, cfg.Token, "PUT",
			fmt.Sprintf("/guilds/%s/bans/%s%s", cfg.GuildID, cfg.UserID, params), nil, headers)
		result = map[string]interface{}{"banned": true, "userId": cfg.UserID}

	case "unban":
		_, err = callDiscordAPIWithHeaders(ctx, cfg.Token, "DELETE",
			fmt.Sprintf("/guilds/%s/bans/%s", cfg.GuildID, cfg.UserID), nil, headers)
		result = map[string]interface{}{"unbanned": true, "userId": cfg.UserID}

	case "timeout":
		var timeoutUntil string
		if cfg.Duration > 0 {
			// Calculate timeout timestamp (simplified - in production use proper time handling)
			timeoutUntil = fmt.Sprintf("{{.Now +%d seconds}}", cfg.Duration)
		}
		payload := map[string]interface{}{
			"communication_disabled_until": timeoutUntil,
		}
		result, err = callDiscordAPIWithHeaders(ctx, cfg.Token, "PATCH",
			fmt.Sprintf("/guilds/%s/members/%s", cfg.GuildID, cfg.UserID), payload, headers)

	case "get-role":
		result, err = callDiscordAPI(ctx, cfg.Token, "GET",
			fmt.Sprintf("/guilds/%s/members/%s", cfg.GuildID, cfg.UserID), nil)

	case "add-role":
		if cfg.RoleID == "" {
			return nil, fmt.Errorf("roleId is required for add-role action")
		}
		_, err = callDiscordAPIWithHeaders(ctx, cfg.Token, "PUT",
			fmt.Sprintf("/guilds/%s/members/%s/roles/%s", cfg.GuildID, cfg.UserID, cfg.RoleID), nil, headers)
		result = map[string]interface{}{"roleAdded": true, "userId": cfg.UserID, "roleId": cfg.RoleID}

	case "remove-role":
		if cfg.RoleID == "" {
			return nil, fmt.Errorf("roleId is required for remove-role action")
		}
		_, err = callDiscordAPIWithHeaders(ctx, cfg.Token, "DELETE",
			fmt.Sprintf("/guilds/%s/members/%s/roles/%s", cfg.GuildID, cfg.UserID, cfg.RoleID), nil, headers)
		result = map[string]interface{}{"roleRemoved": true, "userId": cfg.UserID, "roleId": cfg.RoleID}

	default:
		return nil, fmt.Errorf("unknown action: %s", cfg.Action)
	}

	if err != nil {
		return nil, fmt.Errorf("user operation failed: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"action":  cfg.Action,
			"user":    result,
			"success": true,
		},
	}, nil
}

// ============================================================================
// Discord Webhook Executor
// ============================================================================

// DiscordWebhookExecutor handles discord-webhook node type
type DiscordWebhookExecutor struct{}

// DiscordWebhookConfig defines the typed configuration for discord-webhook
type DiscordWebhookConfig struct {
	WebhookURL  string `json:"webhookUrl" description:"Discord webhook URL"`
	Content     string `json:"content" description:"Message content"`
	Username    string `json:"username" description:"Override webhook username"`
	AvatarURL   string `json:"avatarUrl" description:"Override webhook avatar URL"`
	TTS         bool   `json:"tts" default:"false" description:"Send as text-to-speech"`
	ThreadID    string `json:"threadId" description:"Send to specific thread (optional)"`
	Wait        bool   `json:"wait" default:"false" description:"Wait for server confirmation"`
}

// DiscordWebhookSchema is the UI schema for discord-webhook
var DiscordWebhookSchema = resolver.NewSchemaBuilder("discord-webhook").
	WithName("Send Discord Webhook").
	WithCategory("discord").
	WithIcon("webhook").
	WithDescription("Send a message via Discord webhook").
	AddSection("Webhook").
		AddTextField("webhookUrl", "Webhook URL",
			resolver.WithRequired(),
			resolver.WithPlaceholder("https://discord.com/api/webhooks/..."),
			resolver.WithHint("Full webhook URL from Discord"),
			resolver.WithSensitive(),
		).
		EndSection().
	AddSection("Message").
		AddTextareaField("content", "Content",
			resolver.WithRequired(),
			resolver.WithRows(4),
			resolver.WithPlaceholder("Message from webhook"),
		).
		AddToggleField("tts", "Text-to-Speech",
			resolver.WithDefault(false),
		).
		EndSection().
	AddSection("Overrides").
		AddTextField("username", "Username",
			resolver.WithPlaceholder("Webhook Bot"),
			resolver.WithHint("Override the webhook's default username"),
		).
		AddTextField("avatarUrl", "Avatar URL",
			resolver.WithPlaceholder("https://example.com/avatar.png"),
			resolver.WithHint("Override the webhook's default avatar"),
		).
		EndSection().
	AddSection("Advanced").
		AddTextField("threadId", "Thread ID",
			resolver.WithHint("Send message to a specific thread"),
		).
		AddToggleField("wait", "Wait for Response",
			resolver.WithDefault(false),
			resolver.WithHint("Wait for server confirmation and return message data"),
		).
		EndSection().
	Build()

func (e *DiscordWebhookExecutor) Type() string { return "discord-webhook" }

func (e *DiscordWebhookExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	// Parse config
	var cfg DiscordWebhookConfig
	if r, ok := templateResolver.(*resolver.Resolver); ok {
		if err := resolver.ResolveConfig(step.Config, &cfg, r); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	}

	if cfg.WebhookURL == "" {
		return nil, fmt.Errorf("webhookUrl is required")
	}
	if cfg.Content == "" {
		return nil, fmt.Errorf("content is required")
	}

	// Build payload
	payload := map[string]interface{}{
		"content": cfg.Content,
		"tts":     cfg.TTS,
	}
	if cfg.Username != "" {
		payload["username"] = cfg.Username
	}
	if cfg.AvatarURL != "" {
		payload["avatar_url"] = cfg.AvatarURL
	}

	// Build URL with query params
	url := cfg.WebhookURL
	if cfg.Wait {
		url += "?wait=true"
	}
	if cfg.ThreadID != "" {
		if cfg.Wait {
			url += "&thread_id=" + cfg.ThreadID
		} else {
			url += "?thread_id=" + cfg.ThreadID
		}
	}

	// Send webhook
	result, err := callDiscordWebhook(ctx, url, payload)
	if err != nil {
		return nil, fmt.Errorf("failed to send webhook: %w", err)
	}

	output := map[string]interface{}{
		"success": true,
		"content": cfg.Content,
	}
	if result != nil {
		if id, ok := result["id"]; ok {
			output["messageId"] = id
		}
		if ts, ok := result["timestamp"]; ok {
			output["timestamp"] = ts
		}
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// ============================================================================
// Helper Functions
// ============================================================================

// callDiscordAPI makes a request to the Discord API
func callDiscordAPI(ctx context.Context, token, method, path string, payload map[string]interface{}) (map[string]interface{}, error) {
	return callDiscordAPIWithHeaders(ctx, token, method, path, payload, nil)
}

// callDiscordAPIWithHeaders makes a request to the Discord API with custom headers
func callDiscordAPIWithHeaders(ctx context.Context, token, method, path string, payload map[string]interface{}, headers map[string]string) (map[string]interface{}, error) {
	// This is a placeholder - in production, implement actual HTTP client
	// For now, return a mock response
	_ = ctx
	_ = token
	_ = method
	_ = path
	_ = payload
	_ = headers

	// Mock response for development
	return map[string]interface{}{
		"id":        "mock_message_id",
		"timestamp": "2024-01-01T00:00:00.000Z",
	}, nil
}

// callDiscordWebhook makes a request to a Discord webhook
func callDiscordWebhook(ctx context.Context, url string, payload map[string]interface{}) (map[string]interface{}, error) {
	// This is a placeholder - in production, implement actual HTTP client
	_ = ctx
	_ = url
	_ = payload

	// Mock response for development
	return map[string]interface{}{
		"id":        "mock_webhook_message_id",
		"timestamp": "2024-01-01T00:00:00.000Z",
	}, nil
}
