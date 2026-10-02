package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Provider labels explain where a message came from; they grant no authority.
type slackMessageSource struct {
	Provider               string    `json:"provider"`
	WorkspaceID            string    `json:"workspaceId,omitempty"`
	ChannelID              string    `json:"channelId"`
	ChannelName            string    `json:"channelName,omitempty"`
	ChannelType            string    `json:"channelType,omitempty"`
	ThreadID               string    `json:"threadId,omitempty"`
	MessageID              string    `json:"messageId"`
	ParticipantID          string    `json:"participantId"`
	ParticipantDisplayName string    `json:"participantDisplayName,omitempty"`
	OccurredAt             time.Time `json:"occurredAt,omitempty"`
}

func slackIdentityLabel(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 160 || strings.ContainsAny(value, "\r\n\x00") {
		return ""
	}
	return value
}

// Read after acknowledgement, with the same bound token and exact IDs. Missing
// scopes, deleted users, and lookup failures retain IDs without guessing names.
func (a *slackAdapter) readMessageIdentity(ctx context.Context, token string, envelope *adapterEnvelope) *slackMessageSource {
	event := envelope.Event
	if event.ExternalParticipantID == "" {
		return nil
	}
	source := &slackMessageSource{Provider: "slack", ChannelID: event.ExternalConversationID,
		ThreadID: event.ExternalThreadID, MessageID: event.ExternalMessageID, ParticipantID: event.ExternalParticipantID, OccurredAt: event.OccurredAt}
	if team, ok := event.Attributes["teamId"].(string); ok {
		source.WorkspaceID = team
	}
	if kind, ok := event.Attributes["channelType"].(string); ok {
		source.ChannelType = slackIdentityLabel(kind)
	}
	if event.Direct {
		source.ChannelType = "im"
	}
	lookupCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	response, status, _, err := a.slackJSON(lookupCtx, token, http.MethodGet, "/users.info", url.Values{"user": {source.ParticipantID}}, nil)
	var user struct {
		OK   bool `json:"ok"`
		User struct {
			ID       string `json:"id"`
			Name     string `json:"name"`
			RealName string `json:"real_name"`
			Profile  struct {
				DisplayName string `json:"display_name"`
				RealName    string `json:"real_name"`
			} `json:"profile"`
		} `json:"user"`
	}
	if err == nil && status == http.StatusOK && json.Unmarshal(response, &user) == nil && user.OK && user.User.ID == source.ParticipantID {
		source.ParticipantDisplayName = slackIdentityLabel(firstNonEmpty(user.User.Profile.DisplayName, user.User.Profile.RealName, user.User.RealName, user.User.Name))
	}
	response, status, _, err = a.slackJSON(lookupCtx, token, http.MethodGet, "/conversations.info", url.Values{"channel": {source.ChannelID}}, nil)
	var channel struct {
		OK      bool `json:"ok"`
		Channel struct {
			ID        string `json:"id"`
			Name      string `json:"name"`
			IsIM      bool   `json:"is_im"`
			IsMPIM    bool   `json:"is_mpim"`
			IsPrivate bool   `json:"is_private"`
		} `json:"channel"`
	}
	if err == nil && status == http.StatusOK && json.Unmarshal(response, &channel) == nil && channel.OK && channel.Channel.ID == source.ChannelID {
		source.ChannelName = slackIdentityLabel(channel.Channel.Name)
		switch {
		case channel.Channel.IsIM:
			source.ChannelType = "im"
		case channel.Channel.IsMPIM:
			source.ChannelType = "mpim"
		case channel.Channel.IsPrivate:
			source.ChannelType = "private_channel"
		default:
			source.ChannelType = "public_channel"
		}
	}
	return source
}
