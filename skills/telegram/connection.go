package main

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/axiom-studio/skills.sdk/executor"
	"github.com/axiom-studio/skills.sdk/resolver"
)

// Discovery identifies the credential's bot, not an invented list of accessible
// chats. Telegram has no Bot API enumeration of every conversation.
var GetMeSchema = resolver.NewSchemaBuilder("telegram-get-me").WithName("Get Telegram Bot Identity").WithCategory("action").WithIcon(iconTelegram).WithDescription("Verify the saved Telegram bot and its installation identity without consuming updates").Build()

type GetMeExecutor struct{}

func (e *GetMeExecutor) Type() string { return "telegram-get-me" }
func (e *GetMeExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templates executor.TemplateResolver) (*executor.StepResult, error) {
	config := telegramAdapterConfig(step.Config, templates)
	defer clearTelegramAdapterConfig(config)
	token, _ := config[telegramCredentialKey].(string)
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("Telegram bot credential is unavailable")
	}
	adapter := newTelegramConversationAdapter(telegramAPIBaseOverride, httpClient)
	bot, err := adapter.getTelegramIdentity(ctx, token)
	if err != nil {
		return nil, err
	}
	id := strconv.FormatInt(bot.ID, 10)
	name := telegramLabel(bot.FirstName)
	if bot.Username != "" {
		name = "@" + bot.Username
	}
	return &executor.StepResult{Output: map[string]interface{}{"success": true, "bot": bot, "botId": id, "botUsername": bot.Username, "items": []interface{}{}, "nextCursor": "", "connection": map[string]interface{}{"installationId": id, "applicationId": id, "displayName": name}}}, nil
}
func (a *telegramConversationAdapter) getTelegramIdentity(ctx context.Context, token string) (telegramUser, error) {
	a.identityMu.Lock()
	defer a.identityMu.Unlock()
	digest := telegramDigest([]byte(token))
	if a.identityTokenDigest == digest && time.Now().Before(a.identityExpires) {
		return a.identity, nil
	}
	response, status, err := a.telegramJSON(ctx, token, "getMe", map[string]interface{}{})
	var bot telegramUser
	if err != nil || status != 200 || !response.OK || json.Unmarshal(response.Result, &bot) != nil || !bot.IsBot || bot.ID < 1 || strconv.FormatInt(bot.ID, 10) != telegramBotID(token) || telegramLabel(bot.Username) == "" {
		return telegramUser{}, errors.New("Telegram bot identity could not be verified")
	}
	a.identity = bot
	a.identityTokenDigest = digest
	a.identityExpires = time.Now().Add(5 * time.Minute)
	return bot, nil
}
