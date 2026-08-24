package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/axiom-studio/skills.sdk/executor"
)

const (
	githubCallbackNodeType   = "github.callback.ingress"
	githubCallbackEnvelope   = "_opensealCallbackRequest"
	githubWebhookSecretKey   = "webhook_secret"
	maximumGitHubWebhookSize = 1 << 20
)

type githubCallbackExecutor struct{}

func (*githubCallbackExecutor) Type() string { return githubCallbackNodeType }

func (*githubCallbackExecutor) Execute(_ context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	if step == nil {
		return nil, errors.New("GitHub callback request is required")
	}
	config := make(map[string]interface{}, len(step.Config)+1)
	for key, value := range step.Config {
		config[key] = value
	}
	if bindings, ok := resolver.(executor.BindingResolver); ok {
		config[githubWebhookSecretKey], _ = bindings.GetBinding(githubWebhookSecretKey).(string)
	}
	defer delete(config, githubWebhookSecretKey)
	output, err := normalizeGitHubCallback(config)
	return &executor.StepResult{Output: output}, err
}

type githubCallbackAdapterEnvelope struct {
	Operation    string                      `json:"operation"`
	Registration *githubCallbackRegistration `json:"registration"`
	Request      *githubCallbackRequest      `json:"request"`
}

type githubCallbackRegistration struct {
	ID            string                 `json:"id"`
	Provider      string                 `json:"provider"`
	Configuration map[string]interface{} `json:"configuration,omitempty"`
}

type githubCallbackRequest struct {
	Method  string              `json:"method"`
	Headers map[string][]string `json:"headers,omitempty"`
	Body    []byte              `json:"body"`
}

type githubNormalizedCallbackEvent struct {
	ID         string                 `json:"id"`
	Type       string                 `json:"type"`
	Source     string                 `json:"source"`
	Subject    string                 `json:"subject,omitempty"`
	OccurredAt time.Time              `json:"occurredAt"`
	Attributes map[string]interface{} `json:"attributes,omitempty"`
	Payload    map[string]interface{} `json:"payload,omitempty"`
}

func normalizeGitHubCallback(config map[string]interface{}) (map[string]interface{}, error) {
	raw, ok := config[githubCallbackEnvelope]
	if !ok {
		return nil, errors.New("GitHub callback request is required")
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil, errors.New("GitHub callback request is invalid")
	}
	var envelope githubCallbackAdapterEnvelope
	if json.Unmarshal(encoded, &envelope) != nil || envelope.Operation != "callback" || envelope.Registration == nil ||
		envelope.Request == nil || envelope.Request.Method != http.MethodPost || envelope.Registration.Provider != "github" ||
		len(envelope.Request.Body) == 0 || len(envelope.Request.Body) > maximumGitHubWebhookSize {
		return nil, errors.New("GitHub callback request is invalid")
	}
	secret, _ := config[githubWebhookSecretKey].(string)
	if !verifyGitHubWebhook(envelope.Request.Body, firstGitHubHeader(envelope.Request.Headers, "X-Hub-Signature-256"), secret) {
		return map[string]interface{}{"statusCode": http.StatusUnauthorized, "contentType": "text/plain", "body": []byte("invalid GitHub signature")}, nil
	}
	eventName := strings.TrimSpace(firstGitHubHeader(envelope.Request.Headers, "X-GitHub-Event"))
	if eventName == "ping" {
		return map[string]interface{}{"statusCode": http.StatusOK, "contentType": "application/json", "body": `{"ok":true}`, "events": []githubNormalizedCallbackEvent{}}, nil
	}
	if eventName != "pull_request" {
		return map[string]interface{}{"statusCode": http.StatusAccepted, "events": []githubNormalizedCallbackEvent{}}, nil
	}
	var payload struct {
		Action     string `json:"action"`
		Number     int    `json:"number"`
		Repository struct {
			Name     string `json:"name"`
			FullName string `json:"full_name"`
			Owner    struct {
				Login string `json:"login"`
			} `json:"owner"`
		} `json:"repository"`
		PullRequest map[string]interface{} `json:"pull_request"`
		Sender      struct {
			Login string `json:"login"`
		} `json:"sender"`
	}
	if json.Unmarshal(envelope.Request.Body, &payload) != nil || payload.Number < 1 || payload.Repository.Owner.Login == "" || payload.Repository.Name == "" {
		return map[string]interface{}{"statusCode": http.StatusBadRequest}, nil
	}
	action := strings.ToLower(strings.TrimSpace(payload.Action))
	if action != "opened" && action != "reopened" && action != "synchronize" && action != "ready_for_review" {
		return map[string]interface{}{"statusCode": http.StatusAccepted, "events": []githubNormalizedCallbackEvent{}}, nil
	}
	configuredOwner := githubConfigString(envelope.Registration.Configuration, "owner")
	configuredRepository := githubConfigString(envelope.Registration.Configuration, "repository")
	if configuredOwner != "" && !strings.EqualFold(configuredOwner, payload.Repository.Owner.Login) ||
		configuredRepository != "" && !strings.EqualFold(configuredRepository, payload.Repository.Name) {
		return map[string]interface{}{"statusCode": http.StatusAccepted, "events": []githubNormalizedCallbackEvent{}}, nil
	}
	fullName := strings.TrimSpace(payload.Repository.FullName)
	if fullName == "" {
		fullName = payload.Repository.Owner.Login + "/" + payload.Repository.Name
	}
	pullRequest := projectPullRequest(payload.PullRequest)
	pullRequest["number"] = payload.Number
	occurredAt := time.Now().UTC()
	if rawTime, _ := payload.PullRequest["updated_at"].(string); rawTime != "" {
		if parsed, parseErr := time.Parse(time.RFC3339, rawTime); parseErr == nil {
			occurredAt = parsed.UTC()
		}
	}
	deliveryID := strings.TrimSpace(firstGitHubHeader(envelope.Request.Headers, "X-GitHub-Delivery"))
	if deliveryID == "" {
		digest := sha256.Sum256(envelope.Request.Body)
		deliveryID = hex.EncodeToString(digest[:])
	}
	event := githubNormalizedCallbackEvent{
		ID: "github:" + deliveryID, Type: "github.pull_request." + action, Source: "github",
		Subject: fullName + "#" + strconv.Itoa(payload.Number), OccurredAt: occurredAt,
		Attributes: map[string]interface{}{"owner": payload.Repository.Owner.Login, "repository": payload.Repository.Name, "number": payload.Number, "action": action},
		Payload:    map[string]interface{}{"repository": map[string]interface{}{"owner": payload.Repository.Owner.Login, "name": payload.Repository.Name, "fullName": fullName}, "pullRequest": pullRequest, "sender": payload.Sender.Login},
	}
	return map[string]interface{}{"statusCode": http.StatusAccepted, "contentType": "application/json", "body": `{"ok":true}`, "events": []githubNormalizedCallbackEvent{event}}, nil
}

func verifyGitHubWebhook(body []byte, signature, secret string) bool {
	secret = strings.TrimSpace(secret)
	signature = strings.TrimSpace(signature)
	if secret == "" || !strings.HasPrefix(signature, "sha256=") {
		return false
	}
	want, err := hex.DecodeString(strings.TrimPrefix(signature, "sha256="))
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	return hmac.Equal(mac.Sum(nil), want)
}

func firstGitHubHeader(headers map[string][]string, name string) string {
	for key, values := range headers {
		if strings.EqualFold(key, name) && len(values) > 0 {
			return values[0]
		}
	}
	return ""
}

func githubConfigString(config map[string]interface{}, key string) string {
	value, _ := config[key].(string)
	return strings.TrimSpace(value)
}
