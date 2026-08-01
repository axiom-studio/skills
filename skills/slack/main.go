package main

import (
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
	iconSlack               = "message-circle"
	slackBaseURL            = "https://slack.com/api"
	slackHTTPPort           = "50054"
	slackSkillID            = "skill-slack"
	slackSkillVersion       = "2.2.8"
	slackBotTokenCredential = "slack_bot_token"
)

var slackHTTPClient = &http.Client{Timeout: 30 * time.Second}
var slackBaseURLOverride string

func main() {
	port := os.Getenv("SKILL_PORT")
	if port == "" {
		port = slackHTTPPort
	}

	server := grpc.NewSkillServer(slackSkillID, slackSkillVersion)
	server.RegisterExecutorWithSchema("slack-send-message", &SlackSendMessageExecutor{}, SlackSendMessageSchema)
	server.RegisterExecutorWithSchema("slack-read-messages", &SlackReadMessagesExecutor{}, SlackReadMessagesSchema)
	server.RegisterExecutorWithSchema("slack-channel-list", &SlackChannelListExecutor{}, SlackChannelListSchema)
	server.RegisterExecutorWithSchema("slack-add-reaction", &SlackAddReactionExecutor{}, SlackAddReactionSchema)
	server.RegisterExecutorWithSchema("slack-remove-reaction", &SlackRemoveReactionExecutor{}, SlackRemoveReactionSchema)
	server.RegisterExecutorWithSchema("slack-update-message", &SlackUpdateMessageExecutor{}, SlackUpdateMessageSchema)
	server.RegisterExecutorWithSchema("slack-delete-message", &SlackDeleteMessageExecutor{}, SlackDeleteMessageSchema)
	server.RegisterExecutorWithSchema("slack-create-channel", &SlackCreateChannelExecutor{}, SlackCreateChannelSchema)
	server.RegisterExecutorWithSchema("slack-rename-channel", &SlackRenameChannelExecutor{}, SlackRenameChannelSchema)
	server.RegisterExecutorWithSchema("slack-archive-channel", &SlackArchiveChannelExecutor{}, SlackArchiveChannelSchema)
	server.RegisterExecutorWithSchema("slack-set-channel-topic", &SlackSetChannelTopicExecutor{}, SlackSetChannelTopicSchema)
	server.RegisterExecutorWithSchema("slack-set-channel-purpose", &SlackSetChannelPurposeExecutor{}, SlackSetChannelPurposeSchema)
	server.RegisterExecutorWithSchema("slack-send-ephemeral-message", &SlackSendEphemeralMessageExecutor{}, SlackSendEphemeralMessageSchema)
	server.RegisterExecutorWithSchema("slack-list-users", &SlackListUsersExecutor{}, SlackListUsersSchema)
	conversationAdapter := newSlackAdapter("", os.Getenv("SLACK_API_BASE_URL"), nil)
	server.RegisterExecutor(slackIngressNodeType, &slackIngressExecutor{adapter: conversationAdapter})
	server.RegisterExecutor(slackDeliveryNodeType, &slackDeliveryExecutor{adapter: conversationAdapter})
	server.RegisterExecutor(slackCallbackNodeType, &slackCallbackExecutor{adapter: conversationAdapter})

	fmt.Printf("Starting skill-slack gRPC server on port %s\n", port)
	if err := server.Serve(port); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to serve: %v\n", err)
		os.Exit(1)
	}
}

type SlackChannel struct {
	ID                 string      `json:"id"`
	Name               string      `json:"name"`
	IsArchived         bool        `json:"is_archived"`
	IsPrivate          bool        `json:"is_private"`
	IsIM               bool        `json:"is_im"`
	IsGeneral          bool        `json:"is_general"`
	Creator            string      `json:"creator"`
	NumMembers         int         `json:"num_members"`
	Created            int64       `json:"created"`
	Topic              interface{} `json:"topic"`
	Purpose            interface{} `json:"purpose"`
	PreviousChannelID  string      `json:"previous_channel_id"`
	NextChannelID      string      `json:"next_channel_id"`
	LastRead           string      `json:"last_read"`
	UnreadCountDisplay int         `json:"unread_count_display"`
}

type SlackMessage struct {
	Type            string   `json:"type"`
	User            string   `json:"user"`
	Text            string   `json:"text"`
	Timestamp       string   `json:"ts"`
	ThreadTs        string   `json:"thread_ts"`
	ReplyCount      int      `json:"reply_count"`
	ReplyUsers      []string `json:"reply_users"`
	ReplyUsersCount int      `json:"reply_users_count"`
	LatestReply     string   `json:"latest_reply"`
	IsLocked        bool     `json:"is_locked"`
	Team            string   `json:"team"`
	BotID           string   `json:"bot_id"`
}

type SlackMessagesResponse struct {
	OK               bool           `json:"ok"`
	Error            string         `json:"error"`
	Channels         []SlackChannel `json:"channels"`
	Messages         []SlackMessage `json:"messages"`
	HasMore          bool           `json:"has_more"`
	ResponseMetadata struct {
		NextCursor string `json:"next_cursor"`
	} `json:"response_metadata"`
}

type slackAuthResponse struct {
	OK     bool   `json:"ok"`
	Error  string `json:"error"`
	TeamID string `json:"team_id"`
	Team   string `json:"team"`
}

type slackMutationResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error"`
	TS    string `json:"ts"`
}

type slackConversationResponse struct {
	OK      bool                   `json:"ok"`
	Error   string                 `json:"error"`
	Channel map[string]interface{} `json:"channel"`
}

type SlackUser struct {
	ID       string `json:"id"`
	TeamID   string `json:"team_id"`
	Name     string `json:"name"`
	Deleted  bool   `json:"deleted"`
	RealName string `json:"real_name"`
	Email    string `json:"email"`
	BotID    string `json:"bot_id"`
}

type slackUserResponse struct {
	OK      bool        `json:"ok"`
	Error   string      `json:"error"`
	Members []SlackUser `json:"members"`
}

type slackEphemeralResponse struct {
	OK        bool   `json:"ok"`
	Error     string `json:"error"`
	Channel   string `json:"channel"`
	MessageTs string `json:"message_ts"`
}

// ---------------------------------------------------------------------------
// Slack Send Message
// ---------------------------------------------------------------------------

// SlackSendMessageExecutor handles slack-send-message node type.
type SlackSendMessageExecutor struct{}

func (e *SlackSendMessageExecutor) Type() string { return "slack-send-message" }

func (e *SlackSendMessageExecutor) Execute(ctx context.Context, step *executor.StepDefinition, _ executor.TemplateResolver) (*executor.StepResult, error) {
	config := step.Config
	token := getString(config, slackBotTokenCredential)
	channel := getString(config, "channel")
	text := getString(config, "message")
	if text == "" {
		text = getString(config, "text")
	}
	threadTs := getString(config, "threadTs")

	if token == "" {
		return nil, fmt.Errorf("Slack connection is required")
	}
	if channel == "" {
		return nil, fmt.Errorf("channel is required")
	}
	if text == "" {
		return nil, fmt.Errorf("message is required")
	}

	channelID, err := resolveChannelID(ctx, token, channel)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve channel: %w", err)
	}

	params := url.Values{}
	params.Set("channel", channelID)
	params.Set("text", text)
	if threadTs != "" {
		params.Set("thread_ts", threadTs)
	}

	resp, err := doSlackRequest(ctx, token, "POST", "/chat.postMessage", params)
	if err != nil {
		return nil, err
	}

	var result struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
		TS    string `json:"ts"`
	}
	if err := parseSlackResponse(resp, &result); err != nil {
		return nil, err
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":   true,
			"channel":   channelID,
			"timestamp": result.TS,
			"message":   "Message sent successfully",
		},
	}, nil
}

// ---------------------------------------------------------------------------
// Slack Read Messages
// ---------------------------------------------------------------------------

// SlackReadMessagesExecutor handles slack-read-messages node type.
type SlackReadMessagesExecutor struct{}

func (e *SlackReadMessagesExecutor) Type() string { return "slack-read-messages" }

func (e *SlackReadMessagesExecutor) Execute(ctx context.Context, step *executor.StepDefinition, _ executor.TemplateResolver) (*executor.StepResult, error) {
	config := step.Config
	token := getString(config, slackBotTokenCredential)
	channel := getString(config, "channel")
	limit := getInt(config, "limit", 10)

	if token == "" {
		return nil, fmt.Errorf("Slack connection is required")
	}
	if channel == "" {
		return nil, fmt.Errorf("channel is required")
	}

	channelID, err := resolveChannelID(ctx, token, channel)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve channel: %w", err)
	}

	params := url.Values{}
	params.Set("channel", channelID)
	params.Set("limit", strconv.Itoa(limit))

	resp, err := doSlackRequest(ctx, token, "GET", "/conversations.history", params)
	if err != nil {
		return nil, err
	}

	var result SlackMessagesResponse
	if err := parseSlackResponse(resp, &result); err != nil {
		return nil, err
	}

	messages := make([]map[string]interface{}, 0, len(result.Messages))
	for _, msg := range result.Messages {
		messages = append(messages, map[string]interface{}{
			"type":            msg.Type,
			"user":            msg.User,
			"text":            msg.Text,
			"timestamp":       msg.Timestamp,
			"threadTs":        msg.ThreadTs,
			"replyCount":      msg.ReplyCount,
			"replyUsers":      msg.ReplyUsers,
			"botId":           msg.BotID,
			"latestReply":     msg.LatestReply,
			"replyUsersCount": msg.ReplyUsersCount,
			"isLocked":        msg.IsLocked,
			"isBot":           msg.BotID != "",
		})
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":  true,
			"channel":  channelID,
			"messages": messages,
			"count":    len(messages),
			"hasMore":  result.HasMore,
		},
	}, nil
}

// ---------------------------------------------------------------------------
// Slack List Channels
// ---------------------------------------------------------------------------

// SlackChannelListExecutor handles slack-channel-list node type.
type SlackChannelListExecutor struct{}

func (e *SlackChannelListExecutor) Type() string { return "slack-channel-list" }

func (e *SlackChannelListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := step.Config
	token := getString(config, slackBotTokenCredential)
	if token == "" {
		if bindings, ok := resolver.(executor.BindingResolver); ok {
			token, _ = bindings.GetBinding(slackBotTokenCredential).(string)
		}
	}
	token = strings.TrimSpace(token)
	types := strings.TrimSpace(getString(config, "types"))
	if types == "" {
		types = "public_channel,private_channel"
	}

	if token == "" {
		return nil, fmt.Errorf("Slack connection is required")
	}
	limit := getInt(config, "limit", 100)
	if limit < 1 || limit > 200 {
		return nil, fmt.Errorf("limit must be between 1 and 200")
	}
	cursor := strings.TrimSpace(getString(config, "cursor"))
	query := strings.ToLower(strings.TrimSpace(getString(config, "query")))
	authBody, err := doSlackRequest(ctx, token, "GET", "/auth.test", url.Values{})
	if err != nil {
		return nil, err
	}
	var auth slackAuthResponse
	if err := parseSlackResponse(authBody, &auth); err != nil {
		return nil, err
	}
	if strings.TrimSpace(auth.TeamID) == "" {
		return nil, fmt.Errorf("Slack connection did not return a workspace identity")
	}

	params := url.Values{}
	params.Set("exclude_archived", "true")
	params.Set("types", types)
	params.Set("limit", strconv.Itoa(limit))
	if cursor != "" {
		params.Set("cursor", cursor)
	}

	resp, err := doSlackRequest(ctx, token, "GET", "/conversations.list", params)
	if err != nil {
		return nil, err
	}

	var result SlackMessagesResponse
	if err := parseSlackResponse(resp, &result); err != nil {
		return nil, err
	}

	channels := make([]map[string]interface{}, 0, len(result.Channels))
	for _, ch := range result.Channels {
		if query != "" && !strings.Contains(strings.ToLower(ch.Name), query) {
			continue
		}
		channels = append(channels, map[string]interface{}{
			"id":          ch.ID,
			"name":        ch.Name,
			"description": slackDescription(ch.Purpose, ch.Topic),
			"isArchived":  ch.IsArchived,
			"isPrivate":   ch.IsPrivate,
			"isIm":        ch.IsIM,
			"isGeneral":   ch.IsGeneral,
			"creator":     ch.Creator,
			"numMembers":  ch.NumMembers,
			"created":     ch.Created,
			"topic":       ch.Topic,
			"purpose":     ch.Purpose,
			"previousId":  ch.PreviousChannelID,
			"nextId":      ch.NextChannelID,
			"lastRead":    ch.LastRead,
			"unreadCount": ch.UnreadCountDisplay,
		})
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success": true,
			"connection": map[string]interface{}{
				"installationId": strings.TrimSpace(auth.TeamID),
				"displayName":    strings.TrimSpace(auth.Team),
			},
			"channels":   channels,
			"count":      len(channels),
			"nextCursor": strings.TrimSpace(result.ResponseMetadata.NextCursor),
		},
	}, nil
}

func slackDescription(values ...interface{}) string {
	for _, value := range values {
		object, ok := value.(map[string]interface{})
		if !ok {
			continue
		}
		if text, ok := object["value"].(string); ok && strings.TrimSpace(text) != "" {
			return strings.TrimSpace(text)
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// Slack Add Reaction
// ---------------------------------------------------------------------------

// SlackAddReactionExecutor handles slack-add-reaction node type.
type SlackAddReactionExecutor struct{}

func (e *SlackAddReactionExecutor) Type() string { return "slack-add-reaction" }

func (e *SlackAddReactionExecutor) Execute(ctx context.Context, step *executor.StepDefinition, _ executor.TemplateResolver) (*executor.StepResult, error) {
	config := step.Config
	token := getString(config, slackBotTokenCredential)
	channel := getString(config, "channel")
	timestamp := getString(config, "timestamp")
	emoji := strings.Trim(getString(config, "emoji"), ":")

	if token == "" {
		return nil, fmt.Errorf("Slack connection is required")
	}
	if channel == "" {
		return nil, fmt.Errorf("channel is required")
	}
	if timestamp == "" {
		return nil, fmt.Errorf("timestamp is required")
	}
	if emoji == "" {
		return nil, fmt.Errorf("emoji is required")
	}

	channelID, err := resolveChannelID(ctx, token, channel)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve channel: %w", err)
	}

	params := url.Values{}
	params.Set("channel", channelID)
	params.Set("timestamp", timestamp)
	params.Set("name", emoji)

	resp, err := doSlackRequest(ctx, token, "POST", "/reactions.add", params)
	if err != nil {
		return nil, err
	}

	var result slackMutationResponse
	if err := parseSlackResponse(resp, &result); err != nil {
		return nil, err
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":   true,
			"channel":   channelID,
			"timestamp": timestamp,
			"emoji":     emoji,
			"message":   "Reaction added successfully",
		},
	}, nil
}

// ---------------------------------------------------------------------------
// Slack Remove Reaction
// ---------------------------------------------------------------------------

// SlackRemoveReactionExecutor handles slack-remove-reaction node type.
type SlackRemoveReactionExecutor struct{}

func (e *SlackRemoveReactionExecutor) Type() string { return "slack-remove-reaction" }

func (e *SlackRemoveReactionExecutor) Execute(ctx context.Context, step *executor.StepDefinition, _ executor.TemplateResolver) (*executor.StepResult, error) {
	config := step.Config
	token := getString(config, slackBotTokenCredential)
	channel := getString(config, "channel")
	timestamp := getString(config, "timestamp")
	emoji := strings.Trim(getString(config, "emoji"), ":")

	if token == "" {
		return nil, fmt.Errorf("Slack connection is required")
	}
	if channel == "" {
		return nil, fmt.Errorf("channel is required")
	}
	if timestamp == "" {
		return nil, fmt.Errorf("timestamp is required")
	}
	if emoji == "" {
		return nil, fmt.Errorf("emoji is required")
	}

	channelID, err := resolveChannelID(ctx, token, channel)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve channel: %w", err)
	}

	params := url.Values{}
	params.Set("channel", channelID)
	params.Set("timestamp", timestamp)
	params.Set("name", emoji)

	resp, err := doSlackRequest(ctx, token, "POST", "/reactions.remove", params)
	if err != nil {
		return nil, err
	}

	var result slackMutationResponse
	if err := parseSlackResponse(resp, &result); err != nil {
		return nil, err
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":   true,
			"channel":   channelID,
			"timestamp": timestamp,
			"emoji":     emoji,
			"message":   "Reaction removed successfully",
		},
	}, nil
}

// ---------------------------------------------------------------------------
// Slack Update Message
// ---------------------------------------------------------------------------

// SlackUpdateMessageExecutor handles slack-update-message node type.
type SlackUpdateMessageExecutor struct{}

func (e *SlackUpdateMessageExecutor) Type() string { return "slack-update-message" }

func (e *SlackUpdateMessageExecutor) Execute(ctx context.Context, step *executor.StepDefinition, _ executor.TemplateResolver) (*executor.StepResult, error) {
	config := step.Config
	token := getString(config, slackBotTokenCredential)
	channel := getString(config, "channel")
	timestamp := getString(config, "timestamp")
	text := getString(config, "text")

	if token == "" {
		return nil, fmt.Errorf("Slack connection is required")
	}
	if channel == "" {
		return nil, fmt.Errorf("channel is required")
	}
	if timestamp == "" {
		return nil, fmt.Errorf("timestamp is required")
	}
	if text == "" {
		return nil, fmt.Errorf("text is required")
	}

	channelID, err := resolveChannelID(ctx, token, channel)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve channel: %w", err)
	}

	params := url.Values{}
	params.Set("channel", channelID)
	params.Set("ts", timestamp)
	params.Set("text", text)

	resp, err := doSlackRequest(ctx, token, "POST", "/chat.update", params)
	if err != nil {
		return nil, err
	}

	var result slackMutationResponse
	if err := parseSlackResponse(resp, &result); err != nil {
		return nil, err
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":   true,
			"channel":   channelID,
			"timestamp": timestamp,
			"message":   "Message updated successfully",
		},
	}, nil
}

// ---------------------------------------------------------------------------
// Slack Delete Message
// ---------------------------------------------------------------------------

// SlackDeleteMessageExecutor handles slack-delete-message node type.
type SlackDeleteMessageExecutor struct{}

func (e *SlackDeleteMessageExecutor) Type() string { return "slack-delete-message" }

func (e *SlackDeleteMessageExecutor) Execute(ctx context.Context, step *executor.StepDefinition, _ executor.TemplateResolver) (*executor.StepResult, error) {
	config := step.Config
	token := getString(config, slackBotTokenCredential)
	channel := getString(config, "channel")
	timestamp := getString(config, "timestamp")

	if token == "" {
		return nil, fmt.Errorf("Slack connection is required")
	}
	if channel == "" {
		return nil, fmt.Errorf("channel is required")
	}
	if timestamp == "" {
		return nil, fmt.Errorf("timestamp is required")
	}

	channelID, err := resolveChannelID(ctx, token, channel)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve channel: %w", err)
	}

	params := url.Values{}
	params.Set("channel", channelID)
	params.Set("ts", timestamp)

	resp, err := doSlackRequest(ctx, token, "POST", "/chat.delete", params)
	if err != nil {
		return nil, err
	}

	var result slackMutationResponse
	if err := parseSlackResponse(resp, &result); err != nil {
		return nil, err
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":   true,
			"channel":   channelID,
			"timestamp": timestamp,
			"message":   "Message deleted successfully",
		},
	}, nil
}

// ---------------------------------------------------------------------------
// Slack Create Channel
// ---------------------------------------------------------------------------

type SlackCreateChannelExecutor struct{}

func (e *SlackCreateChannelExecutor) Type() string { return "slack-create-channel" }

func (e *SlackCreateChannelExecutor) Execute(ctx context.Context, step *executor.StepDefinition, _ executor.TemplateResolver) (*executor.StepResult, error) {
	config := step.Config
	token := getString(config, slackBotTokenCredential)
	name := strings.TrimSpace(getString(config, "name"))
	isPrivate := getBool(config, "isPrivate", false)

	if token == "" {
		return nil, fmt.Errorf("Slack connection is required")
	}
	if name == "" {
		return nil, fmt.Errorf("name is required")
	}

	params := url.Values{}
	params.Set("name", name)
	if isPrivate {
		params.Set("is_private", "true")
	}

	respBody, err := doSlackRequest(ctx, token, "POST", "/conversations.create", params)
	if err != nil {
		return nil, err
	}

	var result slackConversationResponse
	if err := parseSlackResponse(respBody, &result); err != nil {
		return nil, err
	}

	channelID := ""
	channelName := ""
	if result.Channel != nil {
		channelID, _ = result.Channel["id"].(string)
		channelName, _ = result.Channel["name"].(string)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":   true,
			"channelId": channelID,
			"name":      channelName,
			"isPrivate": isPrivate,
			"message":   "Channel created successfully",
		},
	}, nil
}

// ---------------------------------------------------------------------------
// Slack Rename Channel
// ---------------------------------------------------------------------------

type SlackRenameChannelExecutor struct{}

func (e *SlackRenameChannelExecutor) Type() string { return "slack-rename-channel" }

func (e *SlackRenameChannelExecutor) Execute(ctx context.Context, step *executor.StepDefinition, _ executor.TemplateResolver) (*executor.StepResult, error) {
	config := step.Config
	token := getString(config, slackBotTokenCredential)
	channel := getString(config, "channel")
	name := strings.TrimSpace(getString(config, "name"))

	if token == "" {
		return nil, fmt.Errorf("Slack connection is required")
	}
	if channel == "" {
		return nil, fmt.Errorf("channel is required")
	}
	if name == "" {
		return nil, fmt.Errorf("name is required")
	}

	channelID, err := resolveChannelID(ctx, token, channel)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve channel: %w", err)
	}

	params := url.Values{}
	params.Set("channel", channelID)
	params.Set("name", name)

	respBody, err := doSlackRequest(ctx, token, "POST", "/conversations.rename", params)
	if err != nil {
		return nil, err
	}

	var result slackConversationResponse
	if err := parseSlackResponse(respBody, &result); err != nil {
		return nil, err
	}

	newName := name
	if result.Channel != nil {
		if n, ok := result.Channel["name"].(string); ok {
			newName = n
		}
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success": true,
			"channel": channelID,
			"name":    newName,
			"message": "Channel renamed successfully",
		},
	}, nil
}

// ---------------------------------------------------------------------------
// Slack Archive Channel
// ---------------------------------------------------------------------------

type SlackArchiveChannelExecutor struct{}

func (e *SlackArchiveChannelExecutor) Type() string { return "slack-archive-channel" }

func (e *SlackArchiveChannelExecutor) Execute(ctx context.Context, step *executor.StepDefinition, _ executor.TemplateResolver) (*executor.StepResult, error) {
	config := step.Config
	token := getString(config, slackBotTokenCredential)
	channel := getString(config, "channel")

	if token == "" {
		return nil, fmt.Errorf("Slack connection is required")
	}
	if channel == "" {
		return nil, fmt.Errorf("channel is required")
	}

	channelID, err := resolveChannelID(ctx, token, channel)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve channel: %w", err)
	}

	params := url.Values{}
	params.Set("channel", channelID)

	respBody, err := doSlackRequest(ctx, token, "POST", "/conversations.archive", params)
	if err != nil {
		return nil, err
	}
	var result slackMutationResponse
	if err := parseSlackResponse(respBody, &result); err != nil {
		return nil, err
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success": true,
			"channel": channelID,
			"message": "Channel archived successfully",
		},
	}, nil
}

// ---------------------------------------------------------------------------
// Slack Set Channel Topic
// ---------------------------------------------------------------------------

type SlackSetChannelTopicExecutor struct{}

func (e *SlackSetChannelTopicExecutor) Type() string { return "slack-set-channel-topic" }

func (e *SlackSetChannelTopicExecutor) Execute(ctx context.Context, step *executor.StepDefinition, _ executor.TemplateResolver) (*executor.StepResult, error) {
	config := step.Config
	token := getString(config, slackBotTokenCredential)
	channel := getString(config, "channel")
	topic := strings.TrimSpace(getString(config, "topic"))

	if token == "" {
		return nil, fmt.Errorf("Slack connection is required")
	}
	if channel == "" {
		return nil, fmt.Errorf("channel is required")
	}
	if topic == "" {
		return nil, fmt.Errorf("topic is required")
	}

	channelID, err := resolveChannelID(ctx, token, channel)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve channel: %w", err)
	}

	params := url.Values{}
	params.Set("channel", channelID)
	params.Set("topic", topic)

	respBody, err := doSlackRequest(ctx, token, "POST", "/conversations.setTopic", params)
	if err != nil {
		return nil, err
	}
	var result slackConversationResponse
	if err := parseSlackResponse(respBody, &result); err != nil {
		return nil, err
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success": true,
			"channel": channelID,
			"topic":   topic,
			"message": "Channel topic updated",
		},
	}, nil
}

// ---------------------------------------------------------------------------
// Slack Set Channel Purpose
// ---------------------------------------------------------------------------

type SlackSetChannelPurposeExecutor struct{}

func (e *SlackSetChannelPurposeExecutor) Type() string { return "slack-set-channel-purpose" }

func (e *SlackSetChannelPurposeExecutor) Execute(ctx context.Context, step *executor.StepDefinition, _ executor.TemplateResolver) (*executor.StepResult, error) {
	config := step.Config
	token := getString(config, slackBotTokenCredential)
	channel := getString(config, "channel")
	purpose := strings.TrimSpace(getString(config, "purpose"))

	if token == "" {
		return nil, fmt.Errorf("Slack connection is required")
	}
	if channel == "" {
		return nil, fmt.Errorf("channel is required")
	}
	if purpose == "" {
		return nil, fmt.Errorf("purpose is required")
	}

	channelID, err := resolveChannelID(ctx, token, channel)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve channel: %w", err)
	}

	params := url.Values{}
	params.Set("channel", channelID)
	params.Set("purpose", purpose)

	respBody, err := doSlackRequest(ctx, token, "POST", "/conversations.setPurpose", params)
	if err != nil {
		return nil, err
	}
	var result slackConversationResponse
	if err := parseSlackResponse(respBody, &result); err != nil {
		return nil, err
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success": true,
			"channel": channelID,
			"purpose": purpose,
			"message": "Channel purpose updated",
		},
	}, nil
}

// ---------------------------------------------------------------------------
// Slack Send Ephemeral Message
// ---------------------------------------------------------------------------

type SlackSendEphemeralMessageExecutor struct{}

func (e *SlackSendEphemeralMessageExecutor) Type() string { return "slack-send-ephemeral-message" }

func (e *SlackSendEphemeralMessageExecutor) Execute(ctx context.Context, step *executor.StepDefinition, _ executor.TemplateResolver) (*executor.StepResult, error) {
	config := step.Config
	token := getString(config, slackBotTokenCredential)
	channel := getString(config, "channel")
	user := getString(config, "user")
	text := getString(config, "text")

	if token == "" {
		return nil, fmt.Errorf("Slack connection is required")
	}
	if channel == "" {
		return nil, fmt.Errorf("channel is required")
	}
	if user == "" {
		return nil, fmt.Errorf("user is required")
	}
	if text == "" {
		return nil, fmt.Errorf("text is required")
	}

	channelID, err := resolveChannelID(ctx, token, channel)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve channel: %w", err)
	}

	params := url.Values{}
	params.Set("channel", channelID)
	params.Set("user", user)
	params.Set("text", text)

	respBody, err := doSlackRequest(ctx, token, "POST", "/chat.postEphemeral", params)
	if err != nil {
		return nil, err
	}

	var result slackEphemeralResponse
	if err := parseSlackResponse(respBody, &result); err != nil {
		return nil, err
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":   true,
			"channel":   channelID,
			"user":      user,
			"message":   "Ephemeral message sent",
			"messageTs": result.MessageTs,
		},
	}, nil
}

// ---------------------------------------------------------------------------
// Slack List Users
// ---------------------------------------------------------------------------

type SlackListUsersExecutor struct{}

func (e *SlackListUsersExecutor) Type() string { return "slack-list-users" }

func (e *SlackListUsersExecutor) Execute(ctx context.Context, step *executor.StepDefinition, _ executor.TemplateResolver) (*executor.StepResult, error) {
	config := step.Config
	token := getString(config, slackBotTokenCredential)
	limit := getInt(config, "limit", 100)

	if token == "" {
		return nil, fmt.Errorf("Slack connection is required")
	}
	if limit <= 0 {
		limit = 100
	}

	params := url.Values{}
	params.Set("limit", strconv.Itoa(limit))

	respBody, err := doSlackRequest(ctx, token, "GET", "/users.list", params)
	if err != nil {
		return nil, err
	}

	var result slackUserResponse
	if err := parseSlackResponse(respBody, &result); err != nil {
		return nil, err
	}

	members := make([]map[string]interface{}, 0, len(result.Members))
	for _, member := range result.Members {
		members = append(members, map[string]interface{}{
			"id":       member.ID,
			"name":     member.Name,
			"realName": member.RealName,
			"teamId":   member.TeamID,
			"email":    member.Email,
			"deleted":  member.Deleted,
			"botId":    member.BotID,
		})
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success": true,
			"members": members,
			"count":   len(members),
		},
	}, nil
}

// ---------------------------------------------------------------------------
// Slack helpers
// ---------------------------------------------------------------------------

func doSlackRequest(ctx context.Context, token, method, endpoint string, queryParams url.Values) ([]byte, error) {
	baseURL := slackBaseURL
	if strings.TrimSpace(slackBaseURLOverride) != "" {
		baseURL = strings.TrimRight(slackBaseURLOverride, "/")
	}
	requestURL := baseURL + endpoint
	var req *http.Request
	var err error

	if method == http.MethodGet {
		u, parseErr := url.Parse(requestURL)
		if parseErr != nil {
			return nil, fmt.Errorf("invalid url %q: %w", endpoint, parseErr)
		}
		u.RawQuery = queryParams.Encode()
		req, err = http.NewRequestWithContext(ctx, method, u.String(), nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
		}
	} else {
		req, err = http.NewRequestWithContext(ctx, method, requestURL, strings.NewReader(queryParams.Encode()))
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")

	resp, err := slackHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("slack request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read slack response: %w", err)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("slack API returned status %d: %s", resp.StatusCode, string(body))
	}

	return body, nil
}

func parseSlackResponse(body []byte, out interface{}) error {
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("failed to parse slack response: %w", err)
	}

	var okResp struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &okResp); err == nil {
		if !okResp.OK {
			return fmt.Errorf("slack API error: %s", okResp.Error)
		}
	}
	return nil
}

func resolveChannelID(ctx context.Context, token, channel string) (string, error) {
	channel = strings.TrimSpace(channel)
	if channel == "" {
		return "", fmt.Errorf("channel is required")
	}
	if strings.HasPrefix(channel, "C") || strings.HasPrefix(channel, "G") || strings.HasPrefix(channel, "D") || strings.HasPrefix(channel, "U") {
		return channel, nil
	}
	if strings.HasPrefix(channel, "#") {
		channel = strings.TrimPrefix(channel, "#")
	}

	params := url.Values{}
	params.Set("types", "public_channel,private_channel")
	params.Set("exclude_archived", "true")
	params.Set("limit", "1000")

	respBody, err := doSlackRequest(ctx, token, "GET", "/conversations.list", params)
	if err != nil {
		return "", err
	}

	var resp SlackMessagesResponse
	if err := parseSlackResponse(respBody, &resp); err != nil {
		return "", err
	}

	for _, conv := range resp.Channels {
		if strings.EqualFold(conv.Name, channel) {
			return conv.ID, nil
		}
	}

	return "", fmt.Errorf("channel not found: %s", channel)
}

func getString(config map[string]interface{}, key string) string {
	if v, ok := config[key]; ok {
		switch s := v.(type) {
		case string:
			return s
		case fmt.Stringer:
			return s.String()
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
		case string:
			if i, err := strconv.Atoi(n); err == nil {
				return i
			}
		}
	}
	return def
}

func getBool(config map[string]interface{}, key string, def bool) bool {
	if v, ok := config[key]; ok {
		switch b := v.(type) {
		case bool:
			return b
		case string:
			return strings.EqualFold(b, "true") || b == "1"
		}
	}
	return def
}

// ---------------------------------------------------------------------------
// UI schemas
// ---------------------------------------------------------------------------

var SlackSendMessageSchema = resolver.NewSchemaBuilder("slack-send-message").
	WithName("Send Message").
	WithCategory("action").
	WithIcon(iconSlack).
	WithDescription("Send a message to a Slack channel").
	AddSection("Message").
	AddExpressionField("channel", "Channel", resolver.WithRequired(), resolver.WithPlaceholder("C123... or #general")).
	AddTextareaField("message", "Message Text", resolver.WithRequired()).
	EndSection().
	Build()

var SlackReadMessagesSchema = resolver.NewSchemaBuilder("slack-read-messages").
	WithName("Read Messages").
	WithCategory("action").
	WithIcon(iconSlack).
	WithDescription("Read recent messages from a Slack channel").
	AddSection("Filters").
	AddExpressionField("channel", "Channel", resolver.WithRequired(), resolver.WithPlaceholder("C123... or #general")).
	AddNumberField("limit", "Limit",
		resolver.WithDefault(10),
		resolver.WithMinMax(1, 200),
	).
	EndSection().
	Build()

var SlackChannelListSchema = resolver.NewSchemaBuilder("slack-channel-list").
	WithName("List Channels").
	WithCategory("action").
	WithIcon(iconSlack).
	WithDescription("List available Slack channels").
	AddSection("Filters").
	AddTextField("types", "Channel Types", resolver.WithPlaceholder("public_channel,private_channel")).
	EndSection().
	Build()

var SlackAddReactionSchema = resolver.NewSchemaBuilder("slack-add-reaction").
	WithName("Add Reaction").
	WithCategory("action").
	WithIcon(iconSlack).
	WithDescription("Add an emoji reaction to a message").
	AddSection("Message").
	AddExpressionField("channel", "Channel", resolver.WithRequired(), resolver.WithPlaceholder("C123... or #general")).
	AddExpressionField("timestamp", "Message Timestamp", resolver.WithRequired(), resolver.WithPlaceholder("173...")).
	AddExpressionField("emoji", "Emoji", resolver.WithRequired(), resolver.WithPlaceholder("thumbsup")).
	EndSection().
	Build()

var SlackRemoveReactionSchema = resolver.NewSchemaBuilder("slack-remove-reaction").
	WithName("Remove Reaction").
	WithCategory("action").
	WithIcon(iconSlack).
	WithDescription("Remove an emoji reaction from a message").
	AddSection("Message").
	AddExpressionField("channel", "Channel", resolver.WithRequired(), resolver.WithPlaceholder("C123... or #general")).
	AddExpressionField("timestamp", "Message Timestamp", resolver.WithRequired(), resolver.WithPlaceholder("173...")).
	AddExpressionField("emoji", "Emoji", resolver.WithRequired(), resolver.WithPlaceholder("thumbsup")).
	EndSection().
	Build()

var SlackUpdateMessageSchema = resolver.NewSchemaBuilder("slack-update-message").
	WithName("Update Message").
	WithCategory("action").
	WithIcon(iconSlack).
	WithDescription("Update text of an existing message").
	AddSection("Message").
	AddExpressionField("channel", "Channel", resolver.WithRequired(), resolver.WithPlaceholder("C123... or #general")).
	AddExpressionField("timestamp", "Message Timestamp", resolver.WithRequired(), resolver.WithPlaceholder("173...")).
	AddTextareaField("text", "Message Text", resolver.WithRequired()).
	EndSection().
	Build()

var SlackDeleteMessageSchema = resolver.NewSchemaBuilder("slack-delete-message").
	WithName("Delete Message").
	WithCategory("action").
	WithIcon(iconSlack).
	WithDescription("Delete a message from a channel").
	AddSection("Message").
	AddExpressionField("channel", "Channel", resolver.WithRequired(), resolver.WithPlaceholder("C123... or #general")).
	AddExpressionField("timestamp", "Message Timestamp", resolver.WithRequired(), resolver.WithPlaceholder("173...")).
	EndSection().
	Build()

var SlackCreateChannelSchema = resolver.NewSchemaBuilder("slack-create-channel").
	WithName("Create Channel").
	WithCategory("action").
	WithIcon(iconSlack).
	WithDescription("Create a new Slack channel").
	AddSection("Channel").
	AddTextField("name", "Channel Name", resolver.WithRequired(), resolver.WithPlaceholder("dev-updates")).
	AddToggleField("isPrivate", "Private Channel", resolver.WithDefault(false), resolver.WithHint("Create as private channel")).
	EndSection().
	Build()

var SlackRenameChannelSchema = resolver.NewSchemaBuilder("slack-rename-channel").
	WithName("Rename Channel").
	WithCategory("action").
	WithIcon(iconSlack).
	WithDescription("Rename an existing Slack channel").
	AddSection("Channel").
	AddExpressionField("channel", "Channel", resolver.WithRequired(), resolver.WithPlaceholder("C123... or #general")).
	AddTextField("name", "New Name", resolver.WithRequired(), resolver.WithPlaceholder("new-channel-name")).
	EndSection().
	Build()

var SlackArchiveChannelSchema = resolver.NewSchemaBuilder("slack-archive-channel").
	WithName("Archive Channel").
	WithCategory("action").
	WithIcon(iconSlack).
	WithDescription("Archive a Slack channel").
	AddSection("Channel").
	AddExpressionField("channel", "Channel", resolver.WithRequired(), resolver.WithPlaceholder("C123... or #general")).
	EndSection().
	Build()

var SlackSetChannelTopicSchema = resolver.NewSchemaBuilder("slack-set-channel-topic").
	WithName("Set Channel Topic").
	WithCategory("action").
	WithIcon(iconSlack).
	WithDescription("Set the topic for a Slack channel").
	AddSection("Message").
	AddExpressionField("channel", "Channel", resolver.WithRequired(), resolver.WithPlaceholder("C123... or #general")).
	AddTextareaField("topic", "Topic", resolver.WithRequired(), resolver.WithPlaceholder("Quarterly planning updates")).
	EndSection().
	Build()

var SlackSetChannelPurposeSchema = resolver.NewSchemaBuilder("slack-set-channel-purpose").
	WithName("Set Channel Purpose").
	WithCategory("action").
	WithIcon(iconSlack).
	WithDescription("Set the purpose for a Slack channel").
	AddSection("Message").
	AddExpressionField("channel", "Channel", resolver.WithRequired(), resolver.WithPlaceholder("C123... or #general")).
	AddTextareaField("purpose", "Purpose", resolver.WithRequired(), resolver.WithPlaceholder("Channel usage details")).
	EndSection().
	Build()

var SlackSendEphemeralMessageSchema = resolver.NewSchemaBuilder("slack-send-ephemeral-message").
	WithName("Send Ephemeral Message").
	WithCategory("action").
	WithIcon(iconSlack).
	WithDescription("Send an ephemeral message visible only to one user").
	AddSection("Message").
	AddExpressionField("channel", "Channel", resolver.WithRequired(), resolver.WithPlaceholder("C123... or #general")).
	AddExpressionField("user", "User", resolver.WithRequired(), resolver.WithPlaceholder("U123...")).
	AddTextareaField("text", "Message Text", resolver.WithRequired(), resolver.WithPlaceholder("This message is only visible to the user.")).
	EndSection().
	Build()

var SlackListUsersSchema = resolver.NewSchemaBuilder("slack-list-users").
	WithName("List Users").
	WithCategory("action").
	WithIcon(iconSlack).
	WithDescription("List users in your Slack workspace").
	AddSection("Options").
	AddNumberField("limit", "Limit", resolver.WithDefault(100), resolver.WithMinMax(1, 1000)).
	EndSection().
	Build()
