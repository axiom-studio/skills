package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/axiom-studio/skills.sdk/executor"
	"github.com/axiom-studio/skills.sdk/grpc"
	"github.com/axiom-studio/skills.sdk/resolver"
)

type githubOperationExecutor struct {
	operation string
	execute   func(context.Context, *executor.StepDefinition, executor.TemplateResolver) (*executor.StepResult, error)
}

func (e *githubOperationExecutor) Type() string { return e.operation }

func (e *githubOperationExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
	return e.execute(ctx, step, templateResolver)
}

func registerExtendedGitHubActions(server *grpc.SkillServer) {
	actions := []struct {
		name    string
		schema  *resolver.NodeSchema
		execute func(context.Context, *executor.StepDefinition, executor.TemplateResolver) (*executor.StepResult, error)
	}{
		{"github-repository-content-get", repositoryContentGetSchema, executeRepositoryContentGet},
		{"github-branch-list", branchListSchema, executeBranchList},
		{"github-commit-list", commitListSchema, executeCommitList},
		{"github-issue-list", issueListSchema, executeIssueList},
		{"github-issue-get", issueGetSchema, executeIssueGet},
		{"github-issue-create", issueCreateSchema, executeIssueCreate},
		{"github-issue-update", issueUpdateSchema, executeIssueUpdate},
		{"github-issue-comments-list", issueCommentsListSchema, executeIssueCommentsList},
		{"github-issue-comment-create", issueCommentCreateSchema, executeIssueCommentCreate},
		{"github-pull-request-update", pullRequestUpdateSchema, executePullRequestUpdate},
		{"github-pull-request-comments-list", pullRequestCommentsListSchema, executePullRequestCommentsList},
		{"github-pull-request-comment-create", pullRequestCommentCreateSchema, executePullRequestCommentCreate},
		{"github-pull-request-reviews-list", pullRequestReviewsListSchema, executePullRequestReviewsList},
		{"github-pull-request-checks-list", pullRequestChecksListSchema, executePullRequestChecksList},
		{"github-pull-request-merge", pullRequestMergeSchema, executePullRequestMerge},
		{"github-workflow-list", workflowListSchema, executeWorkflowList},
		{"github-workflow-runs-list", workflowRunsListSchema, executeWorkflowRunsList},
		{"github-workflow-dispatch", workflowDispatchSchema, executeWorkflowDispatch},
	}
	for _, action := range actions {
		server.RegisterExecutorWithSchema(action.name, &githubOperationExecutor{operation: action.name, execute: action.execute}, action.schema)
	}
}

func repositoryFields(builder *resolver.SchemaBuilder) *resolver.SchemaBuilder {
	return builder.AddSection("Repository").
		AddExpressionField("owner", "Owner", resolver.WithRequired()).
		AddExpressionField("repository", "Repository", resolver.WithRequired()).
		EndSection()
}

var repositoryContentGetSchema = repositoryFields(resolver.NewSchemaBuilder("github-repository-content-get").
	WithName("Get GitHub repository content").WithCategory("action").WithIcon(iconGitHub).
	WithDescription("Read one text file or directory listing at a branch, tag, or commit")).
	AddSection("Content").AddExpressionField("path", "Path", resolver.WithRequired()).
	AddExpressionField("ref", "Branch, tag, or commit").EndSection().Build()

var branchListSchema = repositoryFields(resolver.NewSchemaBuilder("github-branch-list").
	WithName("List GitHub branches").WithCategory("action").WithIcon(iconGitHub).
	WithDescription("List up to 100 branches in one repository")).Build()

var commitListSchema = repositoryFields(resolver.NewSchemaBuilder("github-commit-list").
	WithName("List GitHub commits").WithCategory("action").WithIcon(iconGitHub).
	WithDescription("List up to 100 commits, optionally from one branch or path")).
	AddSection("Filter").AddExpressionField("ref", "Branch, tag, or commit").
	AddExpressionField("path", "Path").EndSection().Build()

var issueListSchema = repositoryFields(resolver.NewSchemaBuilder("github-issue-list").
	WithName("List GitHub issues").WithCategory("action").WithIcon(iconGitHub).
	WithDescription("List up to 100 issues with optional state and label filters")).
	AddSection("Filter").AddExpressionField("state", "State", resolver.WithDefault("open")).
	AddExpressionField("labels", "Comma-separated labels").EndSection().Build()

var issueGetSchema = repositoryFields(resolver.NewSchemaBuilder("github-issue-get").
	WithName("Get GitHub issue").WithCategory("action").WithIcon(iconGitHub).
	WithDescription("Read one GitHub issue")).
	AddSection("Issue").AddExpressionField("number", "Issue number", resolver.WithRequired()).EndSection().Build()

var issueCreateSchema = repositoryFields(resolver.NewSchemaBuilder("github-issue-create").
	WithName("Create GitHub issue").WithCategory("action").WithIcon(iconGitHub).
	WithDescription("Create an issue in one repository")).
	AddSection("Issue").AddExpressionField("title", "Title", resolver.WithRequired()).
	AddTextareaField("body", "Body", resolver.WithRows(10)).
	AddExpressionField("labels", "Comma-separated labels").EndSection().Build()

var issueUpdateSchema = repositoryFields(resolver.NewSchemaBuilder("github-issue-update").
	WithName("Update GitHub issue").WithCategory("action").WithIcon(iconGitHub).
	WithDescription("Update an issue title, body, labels, or state")).
	AddSection("Issue").AddExpressionField("number", "Issue number", resolver.WithRequired()).
	AddExpressionField("title", "Title").AddTextareaField("body", "Body", resolver.WithRows(10)).
	AddExpressionField("labels", "Comma-separated labels").AddExpressionField("state", "State").EndSection().Build()

var issueCommentsListSchema = numberedSchema("github-issue-comments-list", "List GitHub issue comments", "Read up to 100 comments on an issue", "Issue")
var issueCommentCreateSchema = commentSchema("github-issue-comment-create", "Comment on GitHub issue", "Create a comment on an issue", "Issue")
var pullRequestCommentsListSchema = numberedSchema("github-pull-request-comments-list", "List GitHub pull request comments", "Read up to 100 conversation comments on a pull request", "Pull request")
var pullRequestCommentCreateSchema = commentSchema("github-pull-request-comment-create", "Comment on GitHub pull request", "Create a conversation comment on a pull request", "Pull request")
var pullRequestReviewsListSchema = numberedSchema("github-pull-request-reviews-list", "List GitHub pull request reviews", "Read up to 100 submitted reviews", "Pull request")
var pullRequestChecksListSchema = numberedSchema("github-pull-request-checks-list", "List GitHub pull request checks", "Read checks for the current pull request head revision", "Pull request")

var pullRequestUpdateSchema = repositoryFields(resolver.NewSchemaBuilder("github-pull-request-update").
	WithName("Update GitHub pull request").WithCategory("action").WithIcon(iconGitHub).
	WithDescription("Update pull request title, body, base, or state")).
	AddSection("Pull request").AddExpressionField("number", "Pull request number", resolver.WithRequired()).
	AddExpressionField("title", "Title").AddTextareaField("body", "Body", resolver.WithRows(10)).
	AddExpressionField("base", "Base branch").AddExpressionField("state", "State").EndSection().Build()

var pullRequestMergeSchema = repositoryFields(resolver.NewSchemaBuilder("github-pull-request-merge").
	WithName("Merge GitHub pull request").WithCategory("action").WithIcon(iconGitHub).
	WithDescription("Merge one pull request, optionally requiring an exact inspected head revision")).
	AddSection("Pull request").AddExpressionField("number", "Pull request number", resolver.WithRequired()).
	AddExpressionField("headSha", "Expected head commit SHA").
	AddExpressionField("method", "Merge method", resolver.WithDefault("merge")).
	AddExpressionField("commitTitle", "Commit title").
	AddTextareaField("commitMessage", "Commit message", resolver.WithRows(6)).EndSection().Build()

var workflowListSchema = repositoryFields(resolver.NewSchemaBuilder("github-workflow-list").
	WithName("List GitHub Actions workflows").WithCategory("action").WithIcon(iconGitHub).
	WithDescription("List up to 100 Actions workflows in one repository")).Build()

var workflowRunsListSchema = repositoryFields(resolver.NewSchemaBuilder("github-workflow-runs-list").
	WithName("List GitHub Actions workflow runs").WithCategory("action").WithIcon(iconGitHub).
	WithDescription("List up to 100 recent workflow runs")).
	AddSection("Filter").AddExpressionField("workflow", "Workflow ID or file name").
	AddExpressionField("branch", "Branch").AddExpressionField("status", "Status").EndSection().Build()

var workflowDispatchSchema = repositoryFields(resolver.NewSchemaBuilder("github-workflow-dispatch").
	WithName("Dispatch GitHub Actions workflow").WithCategory("action").WithIcon(iconGitHub).
	WithDescription("Dispatch a workflow on an explicit branch or tag")).
	AddSection("Workflow").AddExpressionField("workflow", "Workflow ID or file name", resolver.WithRequired()).
	AddExpressionField("ref", "Branch or tag", resolver.WithRequired()).
	AddTextareaField("inputs", "Inputs as JSON object", resolver.WithRows(6)).EndSection().Build()

func numberedSchema(id, name, description, section string) *resolver.NodeSchema {
	return repositoryFields(resolver.NewSchemaBuilder(id).WithName(name).WithCategory("action").WithIcon(iconGitHub).WithDescription(description)).
		AddSection(section).AddExpressionField("number", section+" number", resolver.WithRequired()).EndSection().Build()
}

func commentSchema(id, name, description, section string) *resolver.NodeSchema {
	return repositoryFields(resolver.NewSchemaBuilder(id).WithName(name).WithCategory("action").WithIcon(iconGitHub).WithDescription(description)).
		AddSection(section).AddExpressionField("number", section+" number", resolver.WithRequired()).
		AddTextareaField("body", "Comment", resolver.WithRows(8), resolver.WithRequired()).EndSection().Build()
}

func executeRepositoryContentGet(ctx context.Context, step *executor.StepDefinition, r executor.TemplateResolver) (*executor.StepResult, error) {
	owner, repo, token, err := repositoryConfig(step, r)
	if err != nil {
		return nil, err
	}
	path := strings.Trim(strings.TrimSpace(configString(step, "path")), "/")
	if path == "" || unsafePath(path) {
		return nil, errors.New("a safe repository path is required")
	}
	endpoint := repositoryEndpoint(owner, repo) + "/contents/" + escapePath(path)
	if ref := configString(step, "ref"); ref != "" {
		endpoint += "?ref=" + url.QueryEscape(ref)
	}
	var raw interface{}
	if err = githubRequest(ctx, token, http.MethodGet, endpoint, nil, &raw); err != nil {
		return nil, err
	}
	if listing, ok := raw.([]interface{}); ok {
		items := make([]map[string]interface{}, 0, len(listing))
		for _, value := range listing {
			if item, ok := value.(map[string]interface{}); ok {
				items = append(items, projectContent(item, false))
			}
		}
		return output(map[string]interface{}{"entries": items, "count": len(items)})
	}
	item, ok := raw.(map[string]interface{})
	if !ok {
		return nil, errors.New("GitHub returned invalid repository content")
	}
	return output(map[string]interface{}{"content": projectContent(item, true)})
}

func executeBranchList(ctx context.Context, step *executor.StepDefinition, r executor.TemplateResolver) (*executor.StepResult, error) {
	return listSimple(ctx, step, r, "/branches?per_page=100", "branches", func(v map[string]interface{}) map[string]interface{} {
		return map[string]interface{}{"name": v["name"], "protected": v["protected"], "sha": nestedString(v, "commit", "sha")}
	})
}

func executeCommitList(ctx context.Context, step *executor.StepDefinition, r executor.TemplateResolver) (*executor.StepResult, error) {
	query := url.Values{"per_page": {"100"}}
	if v := configString(step, "ref"); v != "" {
		query.Set("sha", v)
	}
	if v := configString(step, "path"); v != "" {
		query.Set("path", v)
	}
	return listSimple(ctx, step, r, "/commits?"+query.Encode(), "commits", projectCommit)
}

func executeIssueList(ctx context.Context, step *executor.StepDefinition, r executor.TemplateResolver) (*executor.StepResult, error) {
	state := strings.ToLower(configString(step, "state"))
	if state == "" {
		state = "open"
	}
	if state != "open" && state != "closed" && state != "all" {
		return nil, errors.New("state must be open, closed, or all")
	}
	query := url.Values{"per_page": {"100"}, "state": {state}}
	if labels := configString(step, "labels"); labels != "" {
		query.Set("labels", labels)
	}
	owner, repo, token, err := repositoryConfig(step, r)
	if err != nil {
		return nil, err
	}
	var values []map[string]interface{}
	if err = githubRequest(ctx, token, http.MethodGet, repositoryEndpoint(owner, repo)+"/issues?"+query.Encode(), nil, &values); err != nil {
		return nil, err
	}
	issues := make([]map[string]interface{}, 0, len(values))
	for _, value := range values {
		if value["pull_request"] == nil {
			issues = append(issues, projectIssue(value))
		}
	}
	return output(map[string]interface{}{"issues": issues, "count": len(issues)})
}

func executeIssueGet(ctx context.Context, step *executor.StepDefinition, r executor.TemplateResolver) (*executor.StepResult, error) {
	return getNumbered(ctx, step, r, "/issues/", "issue", projectIssue)
}

func executeIssueCreate(ctx context.Context, step *executor.StepDefinition, r executor.TemplateResolver) (*executor.StepResult, error) {
	title := configString(step, "title")
	if title == "" || len(title) > 256 {
		return nil, errors.New("a bounded issue title is required")
	}
	payload := map[string]interface{}{"title": title, "body": configString(step, "body")}
	if labels := csvStrings(configString(step, "labels")); len(labels) > 0 {
		payload["labels"] = labels
	}
	return mutateIssue(ctx, step, r, http.MethodPost, "/issues", payload)
}

func executeIssueUpdate(ctx context.Context, step *executor.StepDefinition, r executor.TemplateResolver) (*executor.StepResult, error) {
	n, err := pullRequestNumber(step)
	if err != nil {
		return nil, err
	}
	payload := optionalFields(step, "title", "body")
	if state := strings.ToLower(configString(step, "state")); state != "" {
		if state != "open" && state != "closed" {
			return nil, errors.New("state must be open or closed")
		}
		payload["state"] = state
	}
	if labels := configString(step, "labels"); labels != "" {
		payload["labels"] = csvStrings(labels)
	}
	if len(payload) == 0 {
		return nil, errors.New("at least one issue update is required")
	}
	return mutateIssue(ctx, step, r, http.MethodPatch, "/issues/"+strconv.Itoa(n), payload)
}

func executeIssueCommentsList(ctx context.Context, step *executor.StepDefinition, r executor.TemplateResolver) (*executor.StepResult, error) {
	return listComments(ctx, step, r, "comments")
}
func executeIssueCommentCreate(ctx context.Context, step *executor.StepDefinition, r executor.TemplateResolver) (*executor.StepResult, error) {
	return createComment(ctx, step, r)
}
func executePullRequestCommentsList(ctx context.Context, step *executor.StepDefinition, r executor.TemplateResolver) (*executor.StepResult, error) {
	return listComments(ctx, step, r, "comments")
}
func executePullRequestCommentCreate(ctx context.Context, step *executor.StepDefinition, r executor.TemplateResolver) (*executor.StepResult, error) {
	return createComment(ctx, step, r)
}

func executePullRequestUpdate(ctx context.Context, step *executor.StepDefinition, r executor.TemplateResolver) (*executor.StepResult, error) {
	n, err := pullRequestNumber(step)
	if err != nil {
		return nil, err
	}
	payload := optionalFields(step, "title", "body", "base")
	if state := strings.ToLower(configString(step, "state")); state != "" {
		if state != "open" && state != "closed" {
			return nil, errors.New("state must be open or closed")
		}
		payload["state"] = state
	}
	if len(payload) == 0 {
		return nil, errors.New("at least one pull request update is required")
	}
	owner, repo, token, err := repositoryConfig(step, r)
	if err != nil {
		return nil, err
	}
	var value map[string]interface{}
	if err = githubRequest(ctx, token, http.MethodPatch, repositoryEndpoint(owner, repo)+"/pulls/"+strconv.Itoa(n), payload, &value); err != nil {
		return nil, err
	}
	return output(map[string]interface{}{"pullRequest": projectPullRequest(value)})
}

func executePullRequestReviewsList(ctx context.Context, step *executor.StepDefinition, r executor.TemplateResolver) (*executor.StepResult, error) {
	n, err := pullRequestNumber(step)
	if err != nil {
		return nil, err
	}
	return listSimple(ctx, step, r, "/pulls/"+strconv.Itoa(n)+"/reviews?per_page=100", "reviews", projectReview)
}

func executePullRequestChecksList(ctx context.Context, step *executor.StepDefinition, r executor.TemplateResolver) (*executor.StepResult, error) {
	owner, repo, token, err := repositoryConfig(step, r)
	if err != nil {
		return nil, err
	}
	n, err := pullRequestNumber(step)
	if err != nil {
		return nil, err
	}
	var pr map[string]interface{}
	if err = githubRequest(ctx, token, http.MethodGet, repositoryEndpoint(owner, repo)+"/pulls/"+strconv.Itoa(n), nil, &pr); err != nil {
		return nil, err
	}
	sha := nestedString(pr, "head", "sha")
	var result struct {
		CheckRuns []map[string]interface{} `json:"check_runs"`
		Total     int                      `json:"total_count"`
	}
	if err = githubRequest(ctx, token, http.MethodGet, repositoryEndpoint(owner, repo)+"/commits/"+url.PathEscape(sha)+"/check-runs?per_page=100", nil, &result); err != nil {
		return nil, err
	}
	checks := make([]map[string]interface{}, 0, len(result.CheckRuns))
	for _, v := range result.CheckRuns {
		checks = append(checks, projectCheck(v))
	}
	return output(map[string]interface{}{"headSha": sha, "checks": checks, "count": len(checks), "total": result.Total})
}

func executePullRequestMerge(ctx context.Context, step *executor.StepDefinition, r executor.TemplateResolver) (*executor.StepResult, error) {
	owner, repo, token, err := repositoryConfig(step, r)
	if err != nil {
		return nil, err
	}
	n, err := pullRequestNumber(step)
	if err != nil {
		return nil, err
	}
	method := strings.ToLower(configString(step, "method"))
	if method == "" {
		method = "merge"
	}
	if method != "merge" && method != "squash" && method != "rebase" {
		return nil, errors.New("merge method must be merge, squash, or rebase")
	}
	payload := map[string]interface{}{"merge_method": method}
	if sha := configString(step, "headSha"); sha != "" {
		if !commitPattern.MatchString(sha) {
			return nil, errors.New("headSha must be a Git commit SHA")
		}
		payload["sha"] = sha
	}
	if v := configString(step, "commitTitle"); v != "" {
		payload["commit_title"] = v
	}
	if v := configString(step, "commitMessage"); v != "" {
		payload["commit_message"] = v
	}
	var result map[string]interface{}
	if err = githubRequest(ctx, token, http.MethodPut, repositoryEndpoint(owner, repo)+"/pulls/"+strconv.Itoa(n)+"/merge", payload, &result); err != nil {
		return nil, err
	}
	return output(map[string]interface{}{"merge": map[string]interface{}{"merged": result["merged"], "sha": result["sha"], "message": result["message"]}})
}

func executeWorkflowList(ctx context.Context, step *executor.StepDefinition, r executor.TemplateResolver) (*executor.StepResult, error) {
	owner, repo, token, err := repositoryConfig(step, r)
	if err != nil {
		return nil, err
	}
	var result struct {
		Workflows []map[string]interface{} `json:"workflows"`
		Total     int                      `json:"total_count"`
	}
	if err = githubRequest(ctx, token, http.MethodGet, repositoryEndpoint(owner, repo)+"/actions/workflows?per_page=100", nil, &result); err != nil {
		return nil, err
	}
	items := make([]map[string]interface{}, 0, len(result.Workflows))
	for _, v := range result.Workflows {
		items = append(items, map[string]interface{}{"id": v["id"], "name": v["name"], "path": v["path"], "state": v["state"], "url": v["html_url"]})
	}
	return output(map[string]interface{}{"workflows": items, "count": len(items), "total": result.Total})
}

func executeWorkflowRunsList(ctx context.Context, step *executor.StepDefinition, r executor.TemplateResolver) (*executor.StepResult, error) {
	workflow := configString(step, "workflow")
	path := "/actions/runs"
	if workflow != "" {
		path = "/actions/workflows/" + url.PathEscape(workflow) + "/runs"
	}
	query := url.Values{"per_page": {"100"}}
	if v := configString(step, "branch"); v != "" {
		query.Set("branch", v)
	}
	if v := configString(step, "status"); v != "" {
		query.Set("status", v)
	}
	owner, repo, token, err := repositoryConfig(step, r)
	if err != nil {
		return nil, err
	}
	var result struct {
		Runs  []map[string]interface{} `json:"workflow_runs"`
		Total int                      `json:"total_count"`
	}
	if err = githubRequest(ctx, token, http.MethodGet, repositoryEndpoint(owner, repo)+path+"?"+query.Encode(), nil, &result); err != nil {
		return nil, err
	}
	items := make([]map[string]interface{}, 0, len(result.Runs))
	for _, v := range result.Runs {
		items = append(items, projectWorkflowRun(v))
	}
	return output(map[string]interface{}{"runs": items, "count": len(items), "total": result.Total})
}

func executeWorkflowDispatch(ctx context.Context, step *executor.StepDefinition, r executor.TemplateResolver) (*executor.StepResult, error) {
	owner, repo, token, err := repositoryConfig(step, r)
	if err != nil {
		return nil, err
	}
	workflow, ref := configString(step, "workflow"), configString(step, "ref")
	if workflow == "" || ref == "" {
		return nil, errors.New("workflow and ref are required")
	}
	inputs := map[string]interface{}{}
	if raw := configString(step, "inputs"); raw != "" {
		if err = jsonUnmarshalObject(raw, &inputs); err != nil {
			return nil, err
		}
	}
	if err = githubRequest(ctx, token, http.MethodPost, repositoryEndpoint(owner, repo)+"/actions/workflows/"+url.PathEscape(workflow)+"/dispatches", map[string]interface{}{"ref": ref, "inputs": inputs}, nil); err != nil {
		return nil, err
	}
	return output(map[string]interface{}{"dispatched": true, "workflow": workflow, "ref": ref})
}

func listSimple(ctx context.Context, step *executor.StepDefinition, r executor.TemplateResolver, suffix, key string, project func(map[string]interface{}) map[string]interface{}) (*executor.StepResult, error) {
	owner, repo, token, err := repositoryConfig(step, r)
	if err != nil {
		return nil, err
	}
	var values []map[string]interface{}
	if err = githubRequest(ctx, token, http.MethodGet, repositoryEndpoint(owner, repo)+suffix, nil, &values); err != nil {
		return nil, err
	}
	items := make([]map[string]interface{}, 0, len(values))
	for _, v := range values {
		items = append(items, project(v))
	}
	return output(map[string]interface{}{key: items, "count": len(items)})
}
func getNumbered(ctx context.Context, step *executor.StepDefinition, r executor.TemplateResolver, suffix, key string, project func(map[string]interface{}) map[string]interface{}) (*executor.StepResult, error) {
	owner, repo, token, err := repositoryConfig(step, r)
	if err != nil {
		return nil, err
	}
	n, err := pullRequestNumber(step)
	if err != nil {
		return nil, err
	}
	var value map[string]interface{}
	if err = githubRequest(ctx, token, http.MethodGet, repositoryEndpoint(owner, repo)+suffix+strconv.Itoa(n), nil, &value); err != nil {
		return nil, err
	}
	return output(map[string]interface{}{key: project(value)})
}
func mutateIssue(ctx context.Context, step *executor.StepDefinition, r executor.TemplateResolver, method, suffix string, payload map[string]interface{}) (*executor.StepResult, error) {
	owner, repo, token, err := repositoryConfig(step, r)
	if err != nil {
		return nil, err
	}
	var value map[string]interface{}
	if err = githubRequest(ctx, token, method, repositoryEndpoint(owner, repo)+suffix, payload, &value); err != nil {
		return nil, err
	}
	return output(map[string]interface{}{"issue": projectIssue(value)})
}
func listComments(ctx context.Context, step *executor.StepDefinition, r executor.TemplateResolver, key string) (*executor.StepResult, error) {
	n, err := pullRequestNumber(step)
	if err != nil {
		return nil, err
	}
	return listSimple(ctx, step, r, "/issues/"+strconv.Itoa(n)+"/comments?per_page=100", key, projectComment)
}
func createComment(ctx context.Context, step *executor.StepDefinition, r executor.TemplateResolver) (*executor.StepResult, error) {
	n, err := pullRequestNumber(step)
	if err != nil {
		return nil, err
	}
	body := configString(step, "body")
	if body == "" || len(body) > 65536 {
		return nil, errors.New("a bounded comment body is required")
	}
	owner, repo, token, err := repositoryConfig(step, r)
	if err != nil {
		return nil, err
	}
	var value map[string]interface{}
	if err = githubRequest(ctx, token, http.MethodPost, repositoryEndpoint(owner, repo)+"/issues/"+strconv.Itoa(n)+"/comments", map[string]interface{}{"body": body}, &value); err != nil {
		return nil, err
	}
	return output(map[string]interface{}{"comment": projectComment(value)})
}
func output(value map[string]interface{}) (*executor.StepResult, error) {
	return &executor.StepResult{Output: value}, nil
}

func escapePath(path string) string {
	parts := strings.Split(path, "/")
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	return strings.Join(parts, "/")
}
func unsafePath(path string) bool {
	for _, part := range strings.Split(path, "/") {
		if part == "." || part == ".." {
			return true
		}
	}
	return false
}
func csvStrings(value string) []string {
	raw := strings.Split(value, ",")
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}
func optionalFields(step *executor.StepDefinition, keys ...string) map[string]interface{} {
	out := map[string]interface{}{}
	for _, key := range keys {
		if value := configString(step, key); value != "" {
			out[key] = value
		}
	}
	return out
}
func projectContent(v map[string]interface{}, decode bool) map[string]interface{} {
	out := map[string]interface{}{"name": v["name"], "path": v["path"], "type": v["type"], "sha": v["sha"], "size": v["size"], "url": v["html_url"]}
	if decode && v["type"] == "file" {
		if encoded, _ := v["content"].(string); encoded != "" {
			if data, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(encoded, "\n", "")); err == nil && len(data) <= 1<<20 {
				out["content"] = string(data)
			}
		}
	}
	return out
}
func projectCommit(v map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{"sha": v["sha"], "url": v["html_url"], "message": nestedString(v, "commit", "message"), "author": nestedString(v, "author", "login"), "committer": nestedString(v, "committer", "login")}
}
func projectIssue(v map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{"number": v["number"], "title": v["title"], "body": v["body"], "state": v["state"], "url": v["html_url"], "author": nestedString(v, "user", "login"), "labels": v["labels"], "comments": v["comments"], "pullRequest": v["pull_request"] != nil}
}
func projectComment(v map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{"id": v["id"], "body": v["body"], "url": v["html_url"], "author": nestedString(v, "user", "login"), "createdAt": v["created_at"], "updatedAt": v["updated_at"]}
}
func projectReview(v map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{"id": v["id"], "state": v["state"], "body": v["body"], "url": v["html_url"], "author": nestedString(v, "user", "login"), "commitId": v["commit_id"], "submittedAt": v["submitted_at"]}
}
func projectCheck(v map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{"id": v["id"], "name": v["name"], "status": v["status"], "conclusion": v["conclusion"], "url": v["html_url"], "startedAt": v["started_at"], "completedAt": v["completed_at"]}
}
func projectWorkflowRun(v map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{"id": v["id"], "name": v["name"], "event": v["event"], "status": v["status"], "conclusion": v["conclusion"], "branch": v["head_branch"], "headSha": v["head_sha"], "url": v["html_url"], "createdAt": v["created_at"], "updatedAt": v["updated_at"]}
}
func jsonUnmarshalObject(raw string, target *map[string]interface{}) error {
	if err := json.Unmarshal([]byte(raw), target); err != nil {
		return errors.New("workflow inputs must be a JSON object")
	}
	return nil
}
