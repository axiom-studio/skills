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
	if eventName != "pull_request" && eventName != "issues" && eventName != "issue_comment" && eventName != "workflow_run" {
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
		Issue       map[string]interface{} `json:"issue"`
		Comment     map[string]interface{} `json:"comment"`
		WorkflowRun map[string]interface{} `json:"workflow_run"`
		Sender      struct {
			Login string `json:"login"`
		} `json:"sender"`
	}
	if json.Unmarshal(envelope.Request.Body, &payload) != nil || payload.Repository.Owner.Login == "" || payload.Repository.Name == "" {
		return map[string]interface{}{"statusCode": http.StatusBadRequest}, nil
	}
	action := strings.ToLower(strings.TrimSpace(payload.Action))
	allowedActions := map[string]map[string]bool{
		"pull_request":  {"opened": true, "reopened": true, "synchronize": true, "ready_for_review": true, "closed": true},
		"issues":        {"opened": true, "reopened": true, "edited": true, "closed": true},
		"issue_comment": {"created": true, "edited": true},
		"workflow_run":  {"requested": true, "in_progress": true, "completed": true},
	}
	if !allowedActions[eventName][action] {
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
	if payload.Number < 1 {
		payload.Number = githubInteger(payload.Issue["number"])
	}
	if eventName != "workflow_run" && payload.Number < 1 {
		return map[string]interface{}{"statusCode": http.StatusBadRequest}, nil
	}
	attributes := map[string]interface{}{"owner": payload.Repository.Owner.Login, "repository": payload.Repository.Name, "action": action}
	eventPayload := map[string]interface{}{
		"repository": map[string]interface{}{"owner": payload.Repository.Owner.Login, "name": payload.Repository.Name, "fullName": fullName},
		"sender":     payload.Sender.Login,
	}
	subject := fullName + "#" + strconv.Itoa(payload.Number)
	eventType := "github." + eventName + "." + action
	updatedObject := payload.PullRequest
	switch eventName {
	case "pull_request":
		pullRequest := projectPullRequest(payload.PullRequest)
		pullRequest["number"] = payload.Number
		eventPayload["pullRequest"] = pullRequest
		attributes["number"] = payload.Number
	case "issues":
		eventPayload["issue"] = projectIssue(payload.Issue)
		attributes["number"] = payload.Number
	case "issue_comment":
		eventPayload["issue"] = projectIssue(payload.Issue)
		eventPayload["comment"] = projectComment(payload.Comment)
		attributes["number"] = payload.Number
		updatedObject = payload.Comment
	case "workflow_run":
		runID := githubInteger(payload.WorkflowRun["id"])
		if runID < 1 {
			return map[string]interface{}{"statusCode": http.StatusBadRequest}, nil
		}
		eventPayload["workflowRun"] = projectWorkflowRun(payload.WorkflowRun)
		attributes["runId"] = runID
		subject = fullName + "/actions/runs/" + strconv.Itoa(runID)
		updatedObject = payload.WorkflowRun
	}
	occurredAt := time.Now().UTC()
	if rawTime, _ := updatedObject["updated_at"].(string); rawTime != "" {
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
		ID: "github:" + deliveryID, Type: eventType, Source: "github", Subject: subject, OccurredAt: occurredAt,
		Attributes: attributes, Payload: eventPayload,
	}
	return map[string]interface{}{"statusCode": http.StatusAccepted, "contentType": "application/json", "body": `{"ok":true}`, "events": []githubNormalizedCallbackEvent{event}}, nil
}

func githubInteger(value interface{}) int {
	switch number := value.(type) {
	case int:
		return number
	case float64:
		return int(number)
	case json.Number:
		result, _ := strconv.Atoi(number.String())
		return result
	default:
		return 0
	}
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
