package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// Internal host operation, never an agent tool: the canonical gateway URL and
// verified installation ID are supplied by the governed setup route.
type telegramWebhookRequest struct {
	URL            string `json:"url"`
	InstallationID string `json:"installationId"`
}
type telegramWebhookInfo struct {
	URL                string `json:"url"`
	PendingUpdateCount int64  `json:"pending_update_count"`
	LastErrorDate      int64  `json:"last_error_date"`
	LastErrorMessage   string `json:"last_error_message"`
}

func validatedTelegramWebhookURL(value string) bool {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path == "" || strings.TrimSpace(value) != value {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") {
		return false
	}
	if ip := net.ParseIP(host); ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast()) {
		return false
	}
	switch parsed.Port() {
	case "", "443", "80", "88", "8443":
		return true
	default:
		return false
	}
}
func (a *telegramConversationAdapter) readWebhookInfo(ctx context.Context, token string) (telegramWebhookInfo, error) {
	response, status, err := a.telegramJSON(ctx, token, "getWebhookInfo", map[string]interface{}{})
	var info telegramWebhookInfo
	var mandatory struct {
		URL *string `json:"url"`
	}
	if err == nil && (json.Unmarshal(response.Result, &mandatory) != nil || mandatory.URL == nil) {
		return info, errors.New("Telegram webhook status is invalid")
	}
	if err != nil || status != 200 || !response.OK || json.Unmarshal(response.Result, &info) != nil {
		return info, errors.New("Telegram webhook status is unavailable")
	}
	return info, nil
}
func (a *telegramConversationAdapter) manageWebhook(ctx context.Context, token string, request *telegramAdapterRequest) (map[string]interface{}, error) {
	if request.Gateway == nil || request.Gateway.Provider != "telegram" || request.Gateway.Scope.Kind == "" || request.Gateway.Scope.ID == "" || request.Gateway.DeploymentID == "" || request.Webhook == nil || request.Webhook.InstallationID == "" || request.Gateway.InstallationID != request.Webhook.InstallationID || (request.Gateway.ApplicationID != "" && request.Gateway.ApplicationID != request.Webhook.InstallationID) || !validatedTelegramWebhookURL(request.Webhook.URL) {
		return nil, errors.New("Reviewed Telegram webhook configuration is required")
	}
	output := map[string]interface{}{"status": "unavailable", "installationId": request.Webhook.InstallationID, "url": request.Webhook.URL, "pendingUpdateCount": int64(0)}
	bot, err := a.getTelegramIdentity(ctx, token)
	if err != nil {
		output["errorCode"] = "bot_identity_unavailable"
		return output, nil
	}
	if strconv.FormatInt(bot.ID, 10) != request.Webhook.InstallationID {
		output["status"] = "conflict"
		output["errorCode"] = "bot_installation_mismatch"
		return output, nil
	}
	info, err := a.readWebhookInfo(ctx, token)
	if err != nil {
		output["errorCode"] = "webhook_status_unavailable"
		return output, nil
	}
	output["pendingUpdateCount"] = info.PendingUpdateCount
	if info.LastErrorDate > 0 {
		output["lastErrorDate"] = info.LastErrorDate
	}
	if info.LastErrorMessage != "" {
		output["lastErrorMessage"] = "Telegram reports a webhook delivery error."
	}
	if info.URL != "" && info.URL != request.Webhook.URL {
		output["status"] = "conflict"
		output["errorCode"] = "existing_webhook"
		return output, nil
	}
	if request.Operation == "webhook.status" {
		if info.URL == request.Webhook.URL {
			output["status"] = "configured"
		} else {
			output["status"] = "removed"
			output["url"] = ""
		}
		return output, nil
	}
	method := "setWebhook"
	params := map[string]interface{}{"url": request.Webhook.URL, "secret_token": telegramWebhookSecret(token), "allowed_updates": []string{"message", "channel_post"}, "drop_pending_updates": false}
	if request.Operation == "webhook.remove" {
		if info.URL == "" {
			output["status"] = "removed"
			output["url"] = ""
			return output, nil
		}
		method = "deleteWebhook"
		params = map[string]interface{}{"drop_pending_updates": false}
	} else if request.Operation != "webhook.configure" {
		return nil, errors.New("Telegram webhook operation is invalid")
	}
	response, status, err := a.telegramJSON(ctx, token, method, params)
	var success bool
	if err != nil || status != 200 || !response.OK || json.Unmarshal(response.Result, &success) != nil || !success {
		output["errorCode"] = "webhook_update_unconfirmed"
		return output, nil
	}
	confirmed, err := a.readWebhookInfo(ctx, token)
	if err != nil {
		output["errorCode"] = "webhook_confirmation_unavailable"
		return output, nil
	}
	output["pendingUpdateCount"] = confirmed.PendingUpdateCount
	if confirmed.LastErrorDate > 0 {
		output["lastErrorDate"] = confirmed.LastErrorDate
	} else {
		delete(output, "lastErrorDate")
	}
	if confirmed.LastErrorMessage != "" {
		output["lastErrorMessage"] = "Telegram reports a webhook delivery error."
	} else {
		delete(output, "lastErrorMessage")
	}
	if request.Operation == "webhook.remove" && confirmed.URL == "" {
		output["status"] = "removed"
		output["url"] = ""
		delete(output, "lastErrorMessage")
		delete(output, "lastErrorDate")
		return output, nil
	}
	if request.Operation == "webhook.configure" && confirmed.URL == request.Webhook.URL {
		output["status"] = "configured"
		return output, nil
	}
	output["status"] = "conflict"
	output["errorCode"] = "webhook_url_changed"
	return output, nil
}
