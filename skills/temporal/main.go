package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"os"
	"time"

	"github.com/axiom-studio/skills.sdk/executor"
	"github.com/axiom-studio/skills.sdk/grpc"
	"github.com/axiom-studio/skills.sdk/resolver"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"
)

// TemporalSkill implements the Temporal workflow orchestration skill
type TemporalSkill struct {
	grpc.SkillServer
	defaultClient client.Client
}

// WorkflowStartConfig configures starting a workflow
type WorkflowStartConfig struct {
	WorkflowID            string                 `json:"workflowId"`
	TaskQueue             string                 `json:"taskQueue"`
	WorkflowType          string                 `json:"workflowType"`
	Input                 map[string]interface{} `json:"input"`
	ExecutionTimeout      string                 `json:"executionTimeout"`
	RunTimeout            string                 `json:"runTimeout"`
	TaskTimeout           string                 `json:"taskTimeout"`
	WorkflowIDReusePolicy string                 `json:"workflowIdReusePolicy"`
	RetryPolicy           *RetryPolicyConfig     `json:"retryPolicy"`
	Memo                  map[string]interface{} `json:"memo"`
	SearchAttributes      map[string]interface{} `json:"searchAttributes"`
}

// RetryPolicyConfig configures retry behavior
type RetryPolicyConfig struct {
	InitialInterval    string  `json:"initialInterval"`
	BackoffCoefficient float64 `json:"backoffCoefficient"`
	MaximumInterval    string  `json:"maximumInterval"`
	MaximumAttempts    int32   `json:"maximumAttempts"`
}

// WorkflowListConfig configures listing workflows
type WorkflowListConfig struct {
	Query         string `json:"query"`
	PageSize      int32  `json:"pageSize"`
	NextPageToken string `json:"nextPageToken"`
}

// WorkflowGetConfig configures getting workflow details
type WorkflowGetConfig struct {
	WorkflowID string `json:"workflowId"`
	RunID      string `json:"runId"`
}

// WorkflowCancelConfig configures cancelling a workflow
type WorkflowCancelConfig struct {
	WorkflowID string `json:"workflowId"`
	RunID      string `json:"runId"`
}

// WorkflowTerminateConfig configures terminating a workflow
type WorkflowTerminateConfig struct {
	WorkflowID string `json:"workflowId"`
	RunID      string `json:"runId"`
	Reason     string `json:"reason"`
	Details    string `json:"details"`
}

// SignalSendConfig configures sending a signal to a workflow
type SignalSendConfig struct {
	WorkflowID string                 `json:"workflowId"`
	RunID      string                 `json:"runId"`
	SignalName string                 `json:"signalName"`
	Input      map[string]interface{} `json:"input"`
}

// QueryConfig configures querying a workflow
type QueryConfig struct {
	WorkflowID string                 `json:"workflowId"`
	RunID      string                 `json:"runId"`
	QueryType  string                 `json:"queryType"`
	QueryArgs  map[string]interface{} `json:"queryArgs"`
}

// SearchAttributesConfig configures updating search attributes
type SearchAttributesConfig struct {
	WorkflowID       string                 `json:"workflowId"`
	RunID            string                 `json:"runId"`
	SearchAttributes map[string]interface{} `json:"searchAttributes"`
}

// NewTemporalSkill creates a new Temporal skill instance
func NewTemporalSkill() *TemporalSkill {
	return &TemporalSkill{}
}

// getClient creates or returns a cached Temporal client based on config
func (s *TemporalSkill) getClient(ctx context.Context, bindings map[string]interface{}) (client.Client, error) {
	// Check for cached default client
	if s.defaultClient != nil {
		return s.defaultClient, nil
	}

	// Parse connection options from bindings
	host := ""
	if h, ok := bindings["host"]; ok {
		if hs, ok := h.(string); ok {
			host = hs
		}
	}
	if host == "" {
		host = "localhost:7233"
	}

	namespace := ""
	if n, ok := bindings["namespace"]; ok {
		if ns, ok := n.(string); ok {
			namespace = ns
		}
	}
	if namespace == "" {
		namespace = "default"
	}

	apiKey := ""
	if k, ok := bindings["apiKey"]; ok {
		if ks, ok := k.(string); ok {
			apiKey = ks
		}
	}

	// TLS configuration
	enableTLS := false
	if t, ok := bindings["enableTLS"]; ok {
		switch tv := t.(type) {
		case bool:
			enableTLS = tv
		case string:
			enableTLS = tv == "true" || tv == "1"
		}
	}

	options := client.Options{
		HostPort:  host,
		Namespace: namespace,
	}

	if apiKey != "" {
		options.Credentials = client.NewAPIKeyStaticCredentials(apiKey)
	}

	if enableTLS {
		options.ConnectionOptions = client.ConnectionOptions{
			TLS: &tls.Config{
				MinVersion: tls.VersionTLS12,
			},
		}
	}

	c, err := client.Dial(options)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Temporal: %w", err)
	}

	s.defaultClient = c
	return c, nil
}

// parseDuration parses a duration string
func parseDuration(s string) (time.Duration, error) {
	if s == "" {
		return 0, nil
	}
	return time.ParseDuration(s)
}

// getWorkflowIDReusePolicy converts string policy to enum
func getWorkflowIDReusePolicy(policy string) enums.WorkflowIdReusePolicy {
	switch policy {
	case "AllowDuplicate":
		return enums.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE
	case "AllowDuplicateFailedOnly":
		return enums.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE_FAILED_ONLY
	case "RejectDuplicate":
		return enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE
	case "TerminateIfRunning":
		return enums.WORKFLOW_ID_REUSE_POLICY_TERMINATE_IF_RUNNING
	default:
		return enums.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE
	}
}

// getStringValue gets a string value from config with optional template resolution
func getStringValue(config map[string]interface{}, key string, r *resolver.Resolver) string {
	if v, ok := config[key]; ok {
		switch val := v.(type) {
		case string:
			if r != nil {
				return r.ResolveString(val)
			}
			return val
		case []byte:
			return string(val)
		default:
			return fmt.Sprintf("%v", val)
		}
	}
	return ""
}

// getBoolValue gets a bool value from config
func getBoolValue(config map[string]interface{}, key string) bool {
	if v, ok := config[key]; ok {
		switch val := v.(type) {
		case bool:
			return val
		case string:
			return val == "true" || val == "1" || val == "yes"
		case []byte:
			s := string(val)
			return s == "true" || s == "1" || s == "yes"
		default:
			return false
		}
	}
	return false
}

// getIntValue gets an int value from config
func getIntValue(config map[string]interface{}, key string) int {
	if v, ok := config[key]; ok {
		switch val := v.(type) {
		case int:
			return val
		case int64:
			return int(val)
		case float64:
			return int(val)
		case string:
			var i int
			fmt.Sscanf(val, "%d", &i)
			return i
		default:
			return 0
		}
	}
	return 0
}

// getInt32Value gets an int32 value from config
func getInt32Value(config map[string]interface{}, key string) int32 {
	if v, ok := config[key]; ok {
		switch val := v.(type) {
		case int:
			return int32(val)
		case int32:
			return val
		case int64:
			return int32(val)
		case float64:
			return int32(val)
		case string:
			var i int32
			fmt.Sscanf(val, "%d", &i)
			return i
		default:
			return 0
		}
	}
	return 0
}

// getFloatValue gets a float64 value from config
func getFloatValue(config map[string]interface{}, key string) float64 {
	if v, ok := config[key]; ok {
		switch val := v.(type) {
		case float64:
			return val
		case float32:
			return float64(val)
		case int:
			return float64(val)
		case string:
			var f float64
			fmt.Sscanf(val, "%f", &f)
			return f
		default:
			return 0
		}
	}
	return 0
}

// WorkflowStartExecutor implements executor.StepExecutor for workflow start
type WorkflowStartExecutor struct {
	skill *TemporalSkill
}

func (e *WorkflowStartExecutor) Type() string { return "temporal-workflow-start" }

func (e *WorkflowStartExecutor) Execute(ctx context.Context, step *executor.StepDefinition, r executor.TemplateResolver) (*executor.StepResult, error) {
	config := step.Config
	res, ok := r.(*resolver.Resolver)
	if !ok {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": "invalid resolver type"},
			Error:  fmt.Errorf("invalid resolver type"),
		}, nil
	}

	c, err := e.skill.getClient(ctx, res.GetBindings())
	if err != nil {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": err.Error()},
			Error:  err,
		}, nil
	}

	// Resolve config values
	wfID := getStringValue(config, "workflowId", res)
	taskQueue := getStringValue(config, "taskQueue", res)
	workflowType := getStringValue(config, "workflowType", res)
	executionTimeout := getStringValue(config, "executionTimeout", res)
	runTimeout := getStringValue(config, "runTimeout", res)
	taskTimeout := getStringValue(config, "taskTimeout", res)
	reusePolicy := getStringValue(config, "workflowIdReusePolicy", res)

	// Validate required fields
	if wfID == "" {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": "workflowId is required"},
			Error:  fmt.Errorf("workflowId is required"),
		}, nil
	}
	if taskQueue == "" {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": "taskQueue is required"},
			Error:  fmt.Errorf("taskQueue is required"),
		}, nil
	}
	if workflowType == "" {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": "workflowType is required"},
			Error:  fmt.Errorf("workflowType is required"),
		}, nil
	}

	// Build workflow options
	options := client.StartWorkflowOptions{
		ID:        wfID,
		TaskQueue: taskQueue,
	}

	// Parse timeouts
	if executionTimeout != "" {
		if d, err := parseDuration(executionTimeout); err == nil && d > 0 {
			options.WorkflowExecutionTimeout = d
		}
	}
	if runTimeout != "" {
		if d, err := parseDuration(runTimeout); err == nil && d > 0 {
			options.WorkflowRunTimeout = d
		}
	}
	if taskTimeout != "" {
		if d, err := parseDuration(taskTimeout); err == nil && d > 0 {
			options.WorkflowTaskTimeout = d
		}
	}

	// Set workflow ID reuse policy
	options.WorkflowIDReusePolicy = getWorkflowIDReusePolicy(reusePolicy)

	// Set retry policy if provided
	if retryConfig, ok := config["retryPolicy"].(map[string]interface{}); ok {
		options.RetryPolicy = &temporal.RetryPolicy{
			BackoffCoefficient: getFloatValue(retryConfig, "backoffCoefficient"),
			MaximumAttempts:    getInt32Value(retryConfig, "maximumAttempts"),
		}
		if initialInterval := getStringValue(retryConfig, "initialInterval", res); initialInterval != "" {
			if d, err := parseDuration(initialInterval); err == nil && d > 0 {
				options.RetryPolicy.InitialInterval = d
			}
		}
		if maxInterval := getStringValue(retryConfig, "maximumInterval", res); maxInterval != "" {
			if d, err := parseDuration(maxInterval); err == nil && d > 0 {
				options.RetryPolicy.MaximumInterval = d
			}
		}
	}

	// Set memo if provided
	if memo, ok := config["memo"].(map[string]interface{}); ok && len(memo) > 0 {
		options.Memo = memo
	}

	// Set search attributes if provided
	if sa, ok := config["searchAttributes"].(map[string]interface{}); ok && len(sa) > 0 {
		options.SearchAttributes = sa
	}

	// Prepare workflow input
	var inputArgs []interface{}
	if input, ok := config["input"]; ok && input != nil {
		inputArgs = append(inputArgs, input)
	}

	// Start the workflow
	run, err := c.ExecuteWorkflow(ctx, options, workflowType, inputArgs...)
	if err != nil {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": fmt.Sprintf("failed to start workflow: %v", err)},
			Error:  err,
		}, nil
	}

	// Return workflow run information
	return &executor.StepResult{
		Output: map[string]interface{}{
			"workflowId": run.GetID(),
			"runId":      run.GetRunID(),
			"success":    true,
		},
	}, nil
}

// WorkflowListExecutor implements executor.StepExecutor for workflow list
type WorkflowListExecutor struct {
	skill *TemporalSkill
}

func (e *WorkflowListExecutor) Type() string { return "temporal-workflow-list" }

func (e *WorkflowListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, r executor.TemplateResolver) (*executor.StepResult, error) {
	config := step.Config
	res, ok := r.(*resolver.Resolver)
	if !ok {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": "invalid resolver type"},
			Error:  fmt.Errorf("invalid resolver type"),
		}, nil
	}

	c, err := e.skill.getClient(ctx, res.GetBindings())
	if err != nil {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": err.Error()},
			Error:  err,
		}, nil
	}

	// Resolve config values
	query := getStringValue(config, "query", res)
	if query == "" {
		query = "WorkflowStatus = 'Running'"
	}
	pageSize := int32(getIntValue(config, "pageSize"))
	if pageSize == 0 {
		pageSize = 100
	}
	nextPageToken := getStringValue(config, "nextPageToken", res)

	// Build list request
	request := &workflowservice.ListWorkflowExecutionsRequest{
		Query:      query,
		PageSize:   pageSize,
	}
	if nextPageToken != "" {
		request.NextPageToken = []byte(nextPageToken)
	}

	// Execute the list query
	resp, err := c.ListWorkflow(ctx, request)
	if err != nil {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": fmt.Sprintf("failed to list workflows: %v", err)},
			Error:  err,
		}, nil
	}

	var workflows []map[string]interface{}
	for _, execution := range resp.Executions {
		wf := map[string]interface{}{
			"workflowId": execution.Execution.WorkflowId,
			"runId":      execution.Execution.RunId,
			"type":       execution.Type.Name,
			"status":     execution.Status.String(),
		}
		if execution.StartTime != nil {
			wf["startTime"] = execution.StartTime.AsTime().Format(time.RFC3339)
		}
		if execution.CloseTime != nil {
			wf["closeTime"] = execution.CloseTime.AsTime().Format(time.RFC3339)
		}
		if execution.HistoryLength > 0 {
			wf["historyLength"] = execution.HistoryLength
		}
		workflows = append(workflows, wf)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"workflows":     workflows,
			"count":         len(workflows),
			"nextPageToken": string(resp.NextPageToken),
			"success":       true,
		},
	}, nil
}

// WorkflowGetExecutor implements executor.StepExecutor for workflow get
type WorkflowGetExecutor struct {
	skill *TemporalSkill
}

func (e *WorkflowGetExecutor) Type() string { return "temporal-workflow-get" }

func (e *WorkflowGetExecutor) Execute(ctx context.Context, step *executor.StepDefinition, r executor.TemplateResolver) (*executor.StepResult, error) {
	config := step.Config
	res, ok := r.(*resolver.Resolver)
	if !ok {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": "invalid resolver type"},
			Error:  fmt.Errorf("invalid resolver type"),
		}, nil
	}

	c, err := e.skill.getClient(ctx, res.GetBindings())
	if err != nil {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": err.Error()},
			Error:  err,
		}, nil
	}

	// Resolve config values
	wfID := getStringValue(config, "workflowId", res)
	runID := getStringValue(config, "runId", res)

	if wfID == "" {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": "workflowId is required"},
			Error:  fmt.Errorf("workflowId is required"),
		}, nil
	}

	// Describe the workflow execution
	resp, err := c.DescribeWorkflowExecution(ctx, wfID, runID)
	if err != nil {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": fmt.Sprintf("failed to get workflow: %v", err)},
			Error:  err,
		}, nil
	}

	// Build response
	result := map[string]interface{}{
		"workflowId": resp.WorkflowExecutionInfo.Execution.WorkflowId,
		"runId":      resp.WorkflowExecutionInfo.Execution.RunId,
		"type":       resp.WorkflowExecutionInfo.Type.Name,
		"status":     resp.WorkflowExecutionInfo.Status.String(),
		"taskQueue":  resp.WorkflowExecutionInfo.TaskQueue,
	}

	if resp.WorkflowExecutionInfo.StartTime != nil {
		result["startTime"] = resp.WorkflowExecutionInfo.StartTime.AsTime().Format(time.RFC3339)
	}
	if resp.WorkflowExecutionInfo.CloseTime != nil {
		result["closeTime"] = resp.WorkflowExecutionInfo.CloseTime.AsTime().Format(time.RFC3339)
	}
	if resp.WorkflowExecutionInfo.HistoryLength > 0 {
		result["historyLength"] = resp.WorkflowExecutionInfo.HistoryLength
	}

	// Include pending activities
	if resp.PendingActivities != nil {
		var pending []map[string]interface{}
		for _, pa := range resp.PendingActivities {
			activity := map[string]interface{}{
				"activityId":   pa.ActivityId,
				"activityType": pa.ActivityType.Name,
				"state":        pa.State.String(),
				"attempt":      pa.Attempt,
			}
			if pa.ScheduledTime != nil {
				activity["scheduledTime"] = pa.ScheduledTime.AsTime().Format(time.RFC3339)
			}
			if pa.LastHeartbeatTime != nil {
				activity["lastHeartbeatTime"] = pa.LastHeartbeatTime.AsTime().Format(time.RFC3339)
			}
			pending = append(pending, activity)
		}
		result["pendingActivities"] = pending
	}

	// Include pending children
	if resp.PendingChildren != nil {
		var children []map[string]interface{}
		for _, pc := range resp.PendingChildren {
			child := map[string]interface{}{
				"workflowId": pc.WorkflowId,
				"runId":      pc.RunId,
			}
			children = append(children, child)
		}
		result["pendingChildren"] = children
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"result": result,
			"status": resp.WorkflowExecutionInfo.Status.String(),
			"success": true,
		},
	}, nil
}

// WorkflowCancelExecutor implements executor.StepExecutor for workflow cancel
type WorkflowCancelExecutor struct {
	skill *TemporalSkill
}

func (e *WorkflowCancelExecutor) Type() string { return "temporal-workflow-cancel" }

func (e *WorkflowCancelExecutor) Execute(ctx context.Context, step *executor.StepDefinition, r executor.TemplateResolver) (*executor.StepResult, error) {
	config := step.Config
	res, ok := r.(*resolver.Resolver)
	if !ok {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": "invalid resolver type"},
			Error:  fmt.Errorf("invalid resolver type"),
		}, nil
	}

	c, err := e.skill.getClient(ctx, res.GetBindings())
	if err != nil {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": err.Error()},
			Error:  err,
		}, nil
	}

	// Resolve config values
	wfID := getStringValue(config, "workflowId", res)
	runID := getStringValue(config, "runId", res)

	if wfID == "" {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": "workflowId is required"},
			Error:  fmt.Errorf("workflowId is required"),
		}, nil
	}

	// Cancel the workflow
	err = c.CancelWorkflow(ctx, wfID, runID)
	if err != nil {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": fmt.Sprintf("failed to cancel workflow: %v", err)},
			Error:  err,
		}, nil
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success": true,
			"message": "Workflow cancellation request sent successfully",
		},
	}, nil
}

// WorkflowTerminateExecutor implements executor.StepExecutor for workflow terminate
type WorkflowTerminateExecutor struct {
	skill *TemporalSkill
}

func (e *WorkflowTerminateExecutor) Type() string { return "temporal-workflow-terminate" }

func (e *WorkflowTerminateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, r executor.TemplateResolver) (*executor.StepResult, error) {
	config := step.Config
	res, ok := r.(*resolver.Resolver)
	if !ok {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": "invalid resolver type"},
			Error:  fmt.Errorf("invalid resolver type"),
		}, nil
	}

	c, err := e.skill.getClient(ctx, res.GetBindings())
	if err != nil {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": err.Error()},
			Error:  err,
		}, nil
	}

	// Resolve config values
	wfID := getStringValue(config, "workflowId", res)
	runID := getStringValue(config, "runId", res)
	reason := getStringValue(config, "reason", res)
	details := getStringValue(config, "details", res)

	if wfID == "" {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": "workflowId is required"},
			Error:  fmt.Errorf("workflowId is required"),
		}, nil
	}

	// Build terminate details
	var termDetails []interface{}
	if details != "" {
		termDetails = append(termDetails, details)
	}

	// Terminate the workflow
	err = c.TerminateWorkflow(ctx, wfID, runID, reason, termDetails...)
	if err != nil {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": fmt.Sprintf("failed to terminate workflow: %v", err)},
			Error:  err,
		}, nil
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success": true,
			"message": "Workflow terminated successfully",
		},
	}, nil
}

// SignalSendExecutor implements executor.StepExecutor for signal send
type SignalSendExecutor struct {
	skill *TemporalSkill
}

func (e *SignalSendExecutor) Type() string { return "temporal-signal-send" }

func (e *SignalSendExecutor) Execute(ctx context.Context, step *executor.StepDefinition, r executor.TemplateResolver) (*executor.StepResult, error) {
	config := step.Config
	res, ok := r.(*resolver.Resolver)
	if !ok {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": "invalid resolver type"},
			Error:  fmt.Errorf("invalid resolver type"),
		}, nil
	}

	c, err := e.skill.getClient(ctx, res.GetBindings())
	if err != nil {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": err.Error()},
			Error:  err,
		}, nil
	}

	// Resolve config values
	wfID := getStringValue(config, "workflowId", res)
	runID := getStringValue(config, "runId", res)
	signalName := getStringValue(config, "signalName", res)

	if wfID == "" {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": "workflowId is required"},
			Error:  fmt.Errorf("workflowId is required"),
		}, nil
	}
	if signalName == "" {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": "signalName is required"},
			Error:  fmt.Errorf("signalName is required"),
		}, nil
	}

	// Prepare signal input
	var signalArg interface{}
	if input, ok := config["input"]; ok && input != nil {
		signalArg = input
	}

	// Send the signal
	err = c.SignalWorkflow(ctx, wfID, runID, signalName, signalArg)
	if err != nil {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": fmt.Sprintf("failed to send signal: %v", err)},
			Error:  err,
		}, nil
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":    true,
			"message":    "Signal sent successfully",
			"signalName": signalName,
		},
	}, nil
}

// QueryExecutor implements executor.StepExecutor for workflow query
type QueryExecutor struct {
	skill *TemporalSkill
}

func (e *QueryExecutor) Type() string { return "temporal-query" }

func (e *QueryExecutor) Execute(ctx context.Context, step *executor.StepDefinition, r executor.TemplateResolver) (*executor.StepResult, error) {
	config := step.Config
	res, ok := r.(*resolver.Resolver)
	if !ok {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": "invalid resolver type"},
			Error:  fmt.Errorf("invalid resolver type"),
		}, nil
	}

	c, err := e.skill.getClient(ctx, res.GetBindings())
	if err != nil {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": err.Error()},
			Error:  err,
		}, nil
	}

	// Resolve config values
	wfID := getStringValue(config, "workflowId", res)
	runID := getStringValue(config, "runId", res)
	queryType := getStringValue(config, "queryType", res)

	if wfID == "" {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": "workflowId is required"},
			Error:  fmt.Errorf("workflowId is required"),
		}, nil
	}
	if queryType == "" {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": "queryType is required"},
			Error:  fmt.Errorf("queryType is required"),
		}, nil
	}

	// Prepare query arguments
	var queryArgs []interface{}
	if queryArgsConfig, ok := config["queryArgs"]; ok && queryArgsConfig != nil {
		if argsMap, ok := queryArgsConfig.(map[string]interface{}); ok {
			// If QueryArgs has an "args" key that's a slice, use that
			if args, ok := argsMap["args"].([]interface{}); ok {
				queryArgs = args
			} else {
				// Otherwise pass the whole map as a single argument
				queryArgs = append(queryArgs, argsMap)
			}
		} else {
			queryArgs = append(queryArgs, queryArgsConfig)
		}
	}

	// Execute the query
	encodedValue, err := c.QueryWorkflow(ctx, wfID, runID, queryType, queryArgs...)
	if err != nil {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": fmt.Sprintf("failed to query workflow: %v", err)},
			Error:  err,
		}, nil
	}

	// Decode the result
	var result interface{}
	err = encodedValue.Get(&result)
	if err != nil {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": fmt.Sprintf("failed to decode query result: %v", err)},
			Error:  err,
		}, nil
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"result":    result,
			"queryType": queryType,
			"success":   true,
		},
	}, nil
}

// SearchAttributesExecutor implements executor.StepExecutor for search attributes update
type SearchAttributesExecutor struct {
	skill *TemporalSkill
}

func (e *SearchAttributesExecutor) Type() string { return "temporal-search-attributes" }

func (e *SearchAttributesExecutor) Execute(ctx context.Context, step *executor.StepDefinition, r executor.TemplateResolver) (*executor.StepResult, error) {
	config := step.Config
	res, ok := r.(*resolver.Resolver)
	if !ok {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": "invalid resolver type"},
			Error:  fmt.Errorf("invalid resolver type"),
		}, nil
	}

	c, err := e.skill.getClient(ctx, res.GetBindings())
	if err != nil {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": err.Error()},
			Error:  err,
		}, nil
	}

	// Resolve config values
	wfID := getStringValue(config, "workflowId", res)
	runID := getStringValue(config, "runId", res)

	if wfID == "" {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": "workflowId is required"},
			Error:  fmt.Errorf("workflowId is required"),
		}, nil
	}

	// Get search attributes
	var searchAttrs map[string]interface{}
	if sa, ok := config["searchAttributes"].(map[string]interface{}); ok && len(sa) > 0 {
		searchAttrs = sa
	} else {
		return &executor.StepResult{
			Output: map[string]interface{}{"error": "searchAttributes is required"},
			Error:  fmt.Errorf("searchAttributes is required"),
		}, nil
	}

	// Search attributes can only be updated from within the workflow using workflow.UpsertSearchAttributes
	// The client-side approach is to use UpdateWorkflow with a custom update handler
	updateArgs := []interface{}{searchAttrs}

	_, err = c.UpdateWorkflow(ctx, wfID, runID, "upsertSearchAttributes", updateArgs...)
	if err != nil {
		return &executor.StepResult{
			Output: map[string]interface{}{
				"error": fmt.Sprintf("failed to update search attributes: %v. Note: The workflow must implement an 'upsertSearchAttributes' update handler, or use signal-send to trigger the workflow to update its attributes.", err),
			},
			Error: err,
		}, nil
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success": true,
			"message": "Search attributes updated successfully",
		},
	}, nil
}

func main() {
	port := os.Getenv("SKILL_PORT")
	if port == "" {
		port = "50129"
	}

	server := grpc.NewSkillServer("skill-temporal", "1.0.0")
	skill := NewTemporalSkill()

	// Register executors for all node types from skill.yaml
	server.RegisterExecutor("temporal-workflow-start", &WorkflowStartExecutor{skill: skill}, WorkflowStartConfig{})
	server.RegisterExecutor("temporal-workflow-list", &WorkflowListExecutor{skill: skill}, WorkflowListConfig{})
	server.RegisterExecutor("temporal-workflow-get", &WorkflowGetExecutor{skill: skill}, WorkflowGetConfig{})
	server.RegisterExecutor("temporal-workflow-cancel", &WorkflowCancelExecutor{skill: skill}, WorkflowCancelConfig{})
	server.RegisterExecutor("temporal-workflow-terminate", &WorkflowTerminateExecutor{skill: skill}, WorkflowTerminateConfig{})
	server.RegisterExecutor("temporal-signal-send", &SignalSendExecutor{skill: skill}, SignalSendConfig{})
	server.RegisterExecutor("temporal-query", &QueryExecutor{skill: skill}, QueryConfig{})
	server.RegisterExecutor("temporal-search-attributes", &SearchAttributesExecutor{skill: skill}, SearchAttributesConfig{})

	fmt.Printf("Starting skill-temporal gRPC server on port %s\n", port)
	if err := server.Serve(":" + port); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to start server: %v\n", err)
		os.Exit(1)
	}
}
