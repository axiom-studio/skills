package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/axiom-studio/skills.sdk/executor"
	"github.com/axiom-studio/skills.sdk/grpc"
)

type SonarQubeSkill struct {
	client *http.Client
}

// SonarQube API Response structures
type Project struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	QualityGate string `json:"qualityGateStatus"`
}

type ProjectsResponse struct {
	Components []Project `json:"components"`
}

type Issue struct {
	Key          string      `json:"key"`
	Rule         string      `json:"rule"`
	Component    string      `json:"component"`
	Project      string      `json:"project"`
	Line         int         `json:"line"`
	Hash         string      `json:"hash"`
	Message      string      `json:"message"`
	Status       string      `json:"status"`
	Severity     string      `json:"severity"`
	Type         string      `json:"type"`
	CreationDate string      `json:"creationDate"`
	UpdateDate   string      `json:"updateDate"`
	Assignee     string      `json:"assignee"`
	Effort       string      `json:"effort"`
	Tags         []string    `json:"tags"`
	Comments     []Comment   `json:"comments"`
}

type Comment struct {
	Login        string `json:"login"`
	Comment      string `json:"comment"`
	Upvotes      int    `json:"upvotes"`
	Downvotes    int    `json:"downvotes"`
	Author       string `json:"author"`
	CreationDate string `json:"creationDate"`
}

type IssuesResponse struct {
	Issues     []Issue       `json:"issues"`
	Total      int           `json:"total"`
	P          int           `json:"p"`
	Ps         int           `json:"ps"`
	Components []Component   `json:"components"`
	Rules      []RuleSummary `json:"rules"`
}

type Component struct {
	Key  string `json:"key"`
	Name string `json:"name"`
	Path string `json:"path"`
}

type RuleSummary struct {
	Key      string `json:"key"`
	Name     string `json:"name"`
	Severity string `json:"severity"`
	Type     string `json:"type"`
}

type QualityGateResponse struct {
	ProjectKey        string      `json:"projectKey"`
	ProjectName       string      `json:"projectName"`
	GateName          string      `json:"qualityGateName"`
	Status            string      `json:"projectStatus"`
	Conditions        []Condition `json:"conditions"`
	IgnoredConditions []Condition `json:"ignoredConditions"`
}

type Condition struct {
	Status       string `json:"status"`
	MetricKey    string `json:"metricKey"`
	MetricName   string `json:"metricName"`
	Comparator   string `json:"comparator"`
	PeriodIndex  int    `json:"periodIndex"`
	ErrorThreshold string `json:"errorThreshold"`
	ActualValue  string `json:"actualValue"`
}

type Measure struct {
	Metric         string `json:"metric"`
	Value          string `json:"value"`
	FormattedValue string `json:"formattedValue"`
	BestValue      bool   `json:"bestValue"`
}

type MeasuresResponse struct {
	Component struct {
		Key      string    `json:"key"`
		Name     string    `json:"name"`
		Measures []Measure `json:"measures"`
	} `json:"component"`
}

type Rule struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Severity    string `json:"severity"`
	Type        string `json:"type"`
	Status      string `json:"status"`
	InternalKey string `json:"internalKey"`
	IsTemplate  bool   `json:"isTemplate"`
	Lang        string `json:"lang"`
	Tags        string `json:"tags"`
}

type RulesResponse struct {
	Rules []Rule `json:"rules"`
	Total int    `json:"total"`
	P     int    `json:"p"`
	Ps    int    `json:"ps"`
}

type Branch struct {
	Name         string `json:"name"`
	IsMain       bool   `json:"isMain"`
	Merged       bool   `json:"merged"`
	PullRequest  string `json:"pullRequest"`
	Status       string `json:"status"`
	AnalysisDate string `json:"analysisDate"`
}

type BranchesResponse struct {
	Branches []Branch `json:"branches"`
}

type ScanTask struct {
	AnalysisId      string `json:"analysisId"`
	Status          string `json:"status"`
	ComponentKey    string `json:"componentKey"`
	ComponentName   string `json:"componentName"`
	Type            string `json:"type"`
	SubmittedAt     string `json:"submittedAt"`
	StartedAt       string `json:"startedAt"`
	FinishedAt      string `json:"finishedAt"`
	ExecutionTimeMs int64  `json:"executionTimeMs"`
}

type TaskResponse struct {
	Task ScanTask `json:"task"`
}

type CeActivityResponse struct {
	Tasks []ScanTask `json:"tasks"`
}

func NewSonarQubeSkill() *SonarQubeSkill {
	return &SonarQubeSkill{
		client: &http.Client{},
	}
}

// getConfigString safely gets a string value from the config map
func getConfigString(config map[string]interface{}, key string) string {
	if val, ok := config[key]; ok {
		switch v := val.(type) {
		case string:
			return v
		case []byte:
			return string(v)
		default:
			return fmt.Sprintf("%v", v)
		}
	}
	return ""
}

// doRequest performs an HTTP request to the SonarQube API with token authentication
func (s *SonarQubeSkill) doRequest(ctx context.Context, method, server, path string, token string, params url.Values) ([]byte, error) {
	var req *http.Request
	var err error

	urlStr := fmt.Sprintf("%s/api/%s", strings.TrimSuffix(server, "/"), path)

	if params != nil && len(params) > 0 {
		urlStr += "?" + params.Encode()
	}

	if method == "POST" || method == "PUT" {
		req, err = http.NewRequestWithContext(ctx, method, urlStr, bytes.NewReader([]byte(params.Encode())))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		req, err = http.NewRequestWithContext(ctx, method, urlStr, nil)
		if err != nil {
			return nil, err
		}
	}

	// SonarQube uses Basic Auth with the token as username and empty password
	auth := base64.StdEncoding.EncodeToString([]byte(token + ":"))
	req.Header.Set("Authorization", "Basic "+auth)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("sonarqube error (status %d): %s", resp.StatusCode, string(respBody))
	}

	return respBody, nil
}

// doRequestWithJSON performs an HTTP request with JSON body
func (s *SonarQubeSkill) doRequestWithJSON(ctx context.Context, method, server, path string, token string, body interface{}) ([]byte, error) {
	var reqBody io.Reader
	if body != nil {
		jsonBody, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal body: %w", err)
		}
		reqBody = bytes.NewReader(jsonBody)
	}

	urlStr := fmt.Sprintf("%s/api/%s", strings.TrimSuffix(server, "/"), path)

	req, err := http.NewRequestWithContext(ctx, method, urlStr, reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	// SonarQube uses Basic Auth with the token as username and empty password
	auth := base64.StdEncoding.EncodeToString([]byte(token + ":"))
	req.Header.Set("Authorization", "Basic "+auth)

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("sonarqube error (status %d): %s", resp.StatusCode, string(respBody))
	}

	return respBody, nil
}

// makeStepResult creates a successful StepResult with string output
func makeStepResult(output map[string]string) *executor.StepResult {
	interfaceOutput := make(map[string]interface{}, len(output))
	for k, v := range output {
		interfaceOutput[k] = v
	}
	return &executor.StepResult{
		Output: interfaceOutput,
	}
}

// makeErrorResult creates an error StepResult
func makeErrorResult(message string) *executor.StepResult {
	return &executor.StepResult{
		Error: fmt.Errorf("%s", message),
	}
}

// ============================================================================
// SonarQube Project List Executor
// ============================================================================

type SonarProjectListExecutor struct {
	*SonarQubeSkill
}

func (e *SonarProjectListExecutor) Type() string {
	return "sonar-project-list"
}

func (e *SonarProjectListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	server := getConfigString(step.Config, "server")
	token := getConfigString(step.Config, "authToken")
	qualifiers := getConfigString(step.Config, "qualifiers")
	if qualifiers == "" {
		qualifiers = "TRK" // TRK = Projects
	}

	params := url.Values{}
	params.Set("qualifiers", qualifiers)

	if ps := getConfigString(step.Config, "pageSize"); ps != "" {
		params.Set("ps", ps)
	}

	if p := getConfigString(step.Config, "page"); p != "" {
		params.Set("p", p)
	}

	respBody, err := e.doRequest(ctx, "GET", server, "projects/search", token, params)
	if err != nil {
		return makeErrorResult(err.Error()), nil
	}

	var result ProjectsResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return makeErrorResult(fmt.Sprintf("failed to parse response: %v", err)), nil
	}

	output := map[string]string{
		"result": string(respBody),
		"total":  strconv.Itoa(len(result.Components)),
	}

	var projectNames []string
	var projectKeys []string
	for _, proj := range result.Components {
		projectNames = append(projectNames, proj.Name)
		projectKeys = append(projectKeys, proj.Key)
	}

	output["projectNames"] = strings.Join(projectNames, ",")
	output["projectKeys"] = strings.Join(projectKeys, ",")

	return makeStepResult(output), nil
}

// ============================================================================
// SonarQube Scan Executor
// ============================================================================

type SonarScanExecutor struct {
	*SonarQubeSkill
}

func (e *SonarScanExecutor) Type() string {
	return "sonar-scan"
}

func (e *SonarScanExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	server := getConfigString(step.Config, "server")
	token := getConfigString(step.Config, "authToken")
	projectKey := getConfigString(step.Config, "projectKey")
	branch := getConfigString(step.Config, "branch")
	pullRequest := getConfigString(step.Config, "pullRequest")

	if projectKey == "" {
		return makeErrorResult("projectKey is required"), nil
	}

	// Get the last analysis/scan status
	params := url.Values{}
	params.Set("component", projectKey)
	params.Set("ps", "1")
	params.Set("onlyCurrents", "false")

	respBody, err := e.doRequest(ctx, "GET", server, "ce/activity", token, params)
	if err != nil {
		return makeErrorResult(err.Error()), nil
	}

	var activity CeActivityResponse
	if err := json.Unmarshal(respBody, &activity); err != nil {
		return makeErrorResult(fmt.Sprintf("failed to parse response: %v", err)), nil
	}

	output := map[string]string{
		"result":     string(respBody),
		"projectKey": projectKey,
		"message":    "To trigger a new scan, use sonar-scanner CLI or configure a CI/CD pipeline with SonarQube integration",
	}

	if len(activity.Tasks) > 0 {
		lastTask := activity.Tasks[0]
		output["lastAnalysisId"] = lastTask.AnalysisId
		output["lastStatus"] = lastTask.Status
		output["lastSubmittedAt"] = lastTask.SubmittedAt
		if lastTask.FinishedAt != "" {
			output["lastFinishedAt"] = lastTask.FinishedAt
		}
		if lastTask.ExecutionTimeMs > 0 {
			output["lastExecutionTimeMs"] = strconv.FormatInt(lastTask.ExecutionTimeMs, 10)
		}
	}

	if branch != "" {
		output["branch"] = branch
	}
	if pullRequest != "" {
		output["pullRequest"] = pullRequest
	}

	return makeStepResult(output), nil
}

// ============================================================================
// SonarQube Issues List Executor
// ============================================================================

type SonarIssuesListExecutor struct {
	*SonarQubeSkill
}

func (e *SonarIssuesListExecutor) Type() string {
	return "sonar-issues-list"
}

func (e *SonarIssuesListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	server := getConfigString(step.Config, "server")
	token := getConfigString(step.Config, "authToken")
	projectKey := getConfigString(step.Config, "projectKey")
	branch := getConfigString(step.Config, "branch")
	statuses := getConfigString(step.Config, "statuses")
	severities := getConfigString(step.Config, "severities")
	types := getConfigString(step.Config, "types")
	tags := getConfigString(step.Config, "tags")
	assignee := getConfigString(step.Config, "assignee")
	ps := getConfigString(step.Config, "pageSize")
	p := getConfigString(step.Config, "page")

	if projectKey == "" {
		return makeErrorResult("projectKey is required"), nil
	}

	params := url.Values{}
	params.Set("projects", projectKey)

	if branch != "" {
		params.Set("branch", branch)
	}
	if statuses != "" {
		params.Set("statuses", statuses)
	}
	if severities != "" {
		params.Set("severities", severities)
	}
	if types != "" {
		params.Set("types", types)
	}
	if tags != "" {
		params.Set("tags", tags)
	}
	if assignee != "" {
		params.Set("assignees", assignee)
	}
	if ps != "" {
		params.Set("ps", ps)
	} else {
		params.Set("ps", "100")
	}
	if p != "" {
		params.Set("p", p)
	}

	respBody, err := e.doRequest(ctx, "GET", server, "issues/search", token, params)
	if err != nil {
		return makeErrorResult(err.Error()), nil
	}

	var result IssuesResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return makeErrorResult(fmt.Sprintf("failed to parse response: %v", err)), nil
	}

	output := map[string]string{
		"result": string(respBody),
		"total":  strconv.Itoa(result.Total),
	}

	var issueKeys []string
	var issueMessages []string
	for _, issue := range result.Issues {
		issueKeys = append(issueKeys, issue.Key)
		issueMessages = append(issueMessages, fmt.Sprintf("[%s] %s", issue.Severity, issue.Message))
	}

	output["issueKeys"] = strings.Join(issueKeys, ",")
	output["issueCount"] = strconv.Itoa(len(result.Issues))

	if len(issueMessages) > 0 {
		output["issues"] = strings.Join(issueMessages, " | ")
	}

	return makeStepResult(output), nil
}

// ============================================================================
// SonarQube Issue Assign Executor
// ============================================================================

type SonarIssueAssignExecutor struct {
	*SonarQubeSkill
}

func (e *SonarIssueAssignExecutor) Type() string {
	return "sonar-issue-assign"
}

func (e *SonarIssueAssignExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	server := getConfigString(step.Config, "server")
	token := getConfigString(step.Config, "authToken")
	issueKey := getConfigString(step.Config, "issueKey")
	assignee := getConfigString(step.Config, "assignee")

	if issueKey == "" {
		return makeErrorResult("issueKey is required"), nil
	}
	if assignee == "" {
		return makeErrorResult("assignee is required"), nil
	}

	params := url.Values{}
	params.Set("issue", issueKey)
	params.Set("assignee", assignee)

	respBody, err := e.doRequest(ctx, "POST", server, "issues/assign", token, params)
	if err != nil {
		return makeErrorResult(err.Error()), nil
	}

	return makeStepResult(map[string]string{
		"result":   string(respBody),
		"issueKey": issueKey,
		"assignee": assignee,
		"success":  "true",
	}), nil
}

// ============================================================================
// SonarQube Quality Gate Executor
// ============================================================================

type SonarQualityGateExecutor struct {
	*SonarQubeSkill
}

func (e *SonarQualityGateExecutor) Type() string {
	return "sonar-quality-gate"
}

func (e *SonarQualityGateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	server := getConfigString(step.Config, "server")
	token := getConfigString(step.Config, "authToken")
	projectKey := getConfigString(step.Config, "projectKey")
	branch := getConfigString(step.Config, "branch")

	if projectKey == "" {
		return makeErrorResult("projectKey is required"), nil
	}

	params := url.Values{}
	params.Set("projectKey", projectKey)
	if branch != "" {
		params.Set("branch", branch)
	}

	respBody, err := e.doRequest(ctx, "GET", server, "qualitygates/project_status", token, params)
	if err != nil {
		return makeErrorResult(err.Error()), nil
	}

	var result QualityGateResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return makeErrorResult(fmt.Sprintf("failed to parse response: %v", err)), nil
	}

	output := map[string]string{
		"result":        string(respBody),
		"status":        result.Status,
		"gateName":      result.GateName,
		"projectKey":    result.ProjectKey,
		"projectName":   result.ProjectName,
		"conditionsMet": "true",
	}

	for _, cond := range result.Conditions {
		if cond.Status != "OK" {
			output["conditionsMet"] = "false"
			break
		}
	}

	var conditionsSummary []string
	for _, cond := range result.Conditions {
		conditionsSummary = append(conditionsSummary, fmt.Sprintf("%s: %s (%s)", cond.MetricName, cond.Status, cond.ActualValue))
	}
	if len(conditionsSummary) > 0 {
		output["conditions"] = strings.Join(conditionsSummary, " | ")
	}

	return makeStepResult(output), nil
}

// ============================================================================
// SonarQube Measures Executor
// ============================================================================

type SonarMeasuresExecutor struct {
	*SonarQubeSkill
}

func (e *SonarMeasuresExecutor) Type() string {
	return "sonar-measures"
}

func (e *SonarMeasuresExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	server := getConfigString(step.Config, "server")
	token := getConfigString(step.Config, "authToken")
	projectKey := getConfigString(step.Config, "projectKey")
	branch := getConfigString(step.Config, "branch")
	metrics := getConfigString(step.Config, "metrics")

	if projectKey == "" {
		return makeErrorResult("projectKey is required"), nil
	}

	if metrics == "" {
		metrics = "ncloc,coverage,duplicated_lines_density,sqale_index,violations,security_rating,reliability_rating"
	}

	params := url.Values{}
	params.Set("componentKeys", projectKey)
	params.Set("metricKeys", metrics)

	if branch != "" {
		params.Set("branch", branch)
	}

	respBody, err := e.doRequest(ctx, "GET", server, "measures/component", token, params)
	if err != nil {
		return makeErrorResult(err.Error()), nil
	}

	var result MeasuresResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return makeErrorResult(fmt.Sprintf("failed to parse response: %v", err)), nil
	}

	output := map[string]string{
		"result":        string(respBody),
		"componentKey":  result.Component.Key,
		"componentName": result.Component.Name,
	}

	for _, measure := range result.Component.Measures {
		switch measure.Metric {
		case "ncloc":
			output["linesOfCode"] = measure.Value
			output["linesOfCodeFormatted"] = measure.FormattedValue
		case "coverage":
			output["coverage"] = measure.Value
			output["coverageFormatted"] = measure.FormattedValue
		case "duplicated_lines_density":
			output["duplicationDensity"] = measure.Value
			output["duplicationDensityFormatted"] = measure.FormattedValue
		case "sqale_index":
			output["technicalDebt"] = measure.Value
			output["technicalDebtFormatted"] = measure.FormattedValue
		case "violations":
			output["violations"] = measure.Value
			output["violationsFormatted"] = measure.FormattedValue
		case "security_rating":
			output["securityRating"] = measure.Value
			output["securityRatingFormatted"] = measure.FormattedValue
		case "reliability_rating":
			output["reliabilityRating"] = measure.Value
			output["reliabilityRatingFormatted"] = measure.FormattedValue
		case "sqale_rating":
			output["maintainabilityRating"] = measure.Value
			output["maintainabilityRatingFormatted"] = measure.FormattedValue
		case "security_hotspots_reviewed":
			output["securityHotspotsReviewed"] = measure.Value
		case "security_hotspots":
			output["securityHotspots"] = measure.Value
		case "alert_status":
			output["alertStatus"] = measure.Value
		default:
			output[measure.Metric] = measure.Value
			output[measure.Metric+"Formatted"] = measure.FormattedValue
		}
	}

	return makeStepResult(output), nil
}

// ============================================================================
// SonarQube Rules List Executor
// ============================================================================

type SonarRulesListExecutor struct {
	*SonarQubeSkill
}

func (e *SonarRulesListExecutor) Type() string {
	return "sonar-rules-list"
}

func (e *SonarRulesListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	server := getConfigString(step.Config, "server")
	token := getConfigString(step.Config, "authToken")
	qualities := getConfigString(step.Config, "qualities")
	severities := getConfigString(step.Config, "severities")
	types := getConfigString(step.Config, "types")
	tags := getConfigString(step.Config, "tags")
	q := getConfigString(step.Config, "query")
	ps := getConfigString(step.Config, "pageSize")
	p := getConfigString(step.Config, "page")

	params := url.Values{}

	if qualities != "" {
		params.Set("qualities", qualities)
	}
	if severities != "" {
		params.Set("severities", severities)
	}
	if types != "" {
		params.Set("types", types)
	}
	if tags != "" {
		params.Set("tags", tags)
	}
	if q != "" {
		params.Set("q", q)
	}
	if ps != "" {
		params.Set("ps", ps)
	} else {
		params.Set("ps", "100")
	}
	if p != "" {
		params.Set("p", p)
	}

	respBody, err := e.doRequest(ctx, "GET", server, "rules/search", token, params)
	if err != nil {
		return makeErrorResult(err.Error()), nil
	}

	var result RulesResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return makeErrorResult(fmt.Sprintf("failed to parse response: %v", err)), nil
	}

	output := map[string]string{
		"result": string(respBody),
		"total":  strconv.Itoa(result.Total),
	}

	var ruleKeys []string
	var ruleNames []string
	for _, rule := range result.Rules {
		ruleKeys = append(ruleKeys, rule.Key)
		ruleNames = append(ruleNames, fmt.Sprintf("[%s] %s", rule.Severity, rule.Name))
	}

	output["ruleKeys"] = strings.Join(ruleKeys, ",")
	output["ruleCount"] = strconv.Itoa(len(result.Rules))

	if len(ruleNames) > 0 {
		output["rules"] = strings.Join(ruleNames, " | ")
	}

	return makeStepResult(output), nil
}

// ============================================================================
// SonarQube Branches List Executor
// ============================================================================

type SonarBranchesListExecutor struct {
	*SonarQubeSkill
}

func (e *SonarBranchesListExecutor) Type() string {
	return "sonar-branches-list"
}

func (e *SonarBranchesListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	server := getConfigString(step.Config, "server")
	token := getConfigString(step.Config, "authToken")
	projectKey := getConfigString(step.Config, "projectKey")

	if projectKey == "" {
		return makeErrorResult("projectKey is required"), nil
	}

	params := url.Values{}
	params.Set("project", projectKey)

	respBody, err := e.doRequest(ctx, "GET", server, "project_branches/list", token, params)
	if err != nil {
		return makeErrorResult(err.Error()), nil
	}

	var result BranchesResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return makeErrorResult(fmt.Sprintf("failed to parse response: %v", err)), nil
	}

	output := map[string]string{
		"result": string(respBody),
		"total":  strconv.Itoa(len(result.Branches)),
	}

	var branchNames []string
	for _, branch := range result.Branches {
		branchNames = append(branchNames, branch.Name)
		if branch.IsMain {
			output["mainBranch"] = branch.Name
			output["mainBranchStatus"] = branch.Status
		}
	}

	output["branches"] = strings.Join(branchNames, ",")

	var branchDetails []string
	for _, branch := range result.Branches {
		detail := fmt.Sprintf("%s (main: %v, status: %s)", branch.Name, branch.IsMain, branch.Status)
		if branch.AnalysisDate != "" {
			detail += fmt.Sprintf(", analyzed: %s", branch.AnalysisDate)
		}
		branchDetails = append(branchDetails, detail)
	}
	if len(branchDetails) > 0 {
		output["branchDetails"] = strings.Join(branchDetails, " | ")
	}

	return makeStepResult(output), nil
}

func main() {
	port := os.Getenv("SKILL_PORT")
	if port == "" {
		port = "50096"
	}

	server := grpc.NewSkillServer("skill-sonarqube", "1.0.0")
	skill := NewSonarQubeSkill()

	// Register executors based on skill.yaml nodeTypes
	server.RegisterExecutor("sonar-project-list", &SonarProjectListExecutor{skill})
	server.RegisterExecutor("sonar-scan", &SonarScanExecutor{skill})
	server.RegisterExecutor("sonar-issues-list", &SonarIssuesListExecutor{skill})
	server.RegisterExecutor("sonar-issue-assign", &SonarIssueAssignExecutor{skill})
	server.RegisterExecutor("sonar-quality-gate", &SonarQualityGateExecutor{skill})
	server.RegisterExecutor("sonar-measures", &SonarMeasuresExecutor{skill})
	server.RegisterExecutor("sonar-rules-list", &SonarRulesListExecutor{skill})
	server.RegisterExecutor("sonar-branches-list", &SonarBranchesListExecutor{skill})

	fmt.Printf("Starting skill-sonarqube gRPC server on port %s\n", port)
	if err := server.Serve(":" + port); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to start server: %v\n", err)
		os.Exit(1)
	}
}
