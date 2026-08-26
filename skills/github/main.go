package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/axiom-studio/skills.sdk/executor"
	"github.com/axiom-studio/skills.sdk/grpc"
	"github.com/axiom-studio/skills.sdk/resolver"
)

const (
	skillID      = "skill-github"
	skillVersion = "1.2.2"
	defaultPort  = "50051"
	iconGitHub   = "github"
)

var (
	githubAPIBase = "https://api.github.com"
	githubClient  = &http.Client{Timeout: 45 * time.Second}
	namePattern   = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,100}$`)
	branchPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,254}$`)
	commitPattern = regexp.MustCompile(`^[a-fA-F0-9]{40}$`)
)

func main() {
	server := grpc.NewSkillServer(skillID, skillVersion)
	server.RegisterExecutorWithSchema("github-repository-get", &repositoryGetExecutor{}, repositoryGetSchema)
	server.RegisterExecutorWithSchema("github-pull-request-list", &pullRequestListExecutor{}, pullRequestListSchema)
	server.RegisterExecutorWithSchema("github-pull-request-get", &pullRequestGetExecutor{}, pullRequestGetSchema)
	server.RegisterExecutorWithSchema("github-pull-request-files", &pullRequestFilesExecutor{}, pullRequestFilesSchema)
	server.RegisterExecutorWithSchema("github-pull-request-create", &pullRequestCreateExecutor{}, pullRequestCreateSchema)
	server.RegisterExecutorWithSchema("github-pull-request-review-create", &pullRequestReviewCreateExecutor{}, pullRequestReviewCreateSchema)
	registerExtendedGitHubActions(server)
	server.RegisterExecutor(githubCallbackNodeType, &githubCallbackExecutor{})
	port := strings.TrimSpace(os.Getenv("SKILL_PORT"))
	if port == "" {
		port = defaultPort
	}
	if err := server.Serve(port); err != nil {
		fmt.Fprintln(os.Stderr, "GitHub Skill server failed")
		os.Exit(1)
	}
}

var repositoryGetSchema = resolver.NewSchemaBuilder("github-repository-get").
	WithName("Get GitHub repository").WithCategory("action").WithIcon(iconGitHub).
	WithDescription("Read repository metadata from GitHub").
	AddSection("Repository").
	AddExpressionField("owner", "Owner", resolver.WithRequired()).
	AddExpressionField("repository", "Repository", resolver.WithRequired()).
	EndSection().Build()

var pullRequestListSchema = resolver.NewSchemaBuilder("github-pull-request-list").
	WithName("List GitHub pull requests").WithCategory("action").WithIcon(iconGitHub).
	WithDescription("List bounded pull requests in one GitHub repository").
	AddSection("Repository").
	AddExpressionField("owner", "Owner", resolver.WithRequired()).
	AddExpressionField("repository", "Repository", resolver.WithRequired()).
	AddExpressionField("state", "State", resolver.WithDefault("open")).
	EndSection().Build()

var pullRequestCreateSchema = resolver.NewSchemaBuilder("github-pull-request-create").
	WithName("Create GitHub pull request").WithCategory("action").WithIcon(iconGitHub).
	WithDescription("Create an idempotent pull request in one GitHub repository").
	AddSection("Repository").
	AddExpressionField("owner", "Owner", resolver.WithRequired()).
	AddExpressionField("repository", "Repository", resolver.WithRequired()).
	EndSection().
	AddSection("Pull request").
	AddExpressionField("title", "Title", resolver.WithRequired()).
	AddTextareaField("body", "Body", resolver.WithRows(8)).
	AddExpressionField("head", "Head branch", resolver.WithRequired()).
	AddExpressionField("base", "Base branch", resolver.WithRequired()).
	EndSection().Build()

var pullRequestGetSchema = resolver.NewSchemaBuilder("github-pull-request-get").
	WithName("Get GitHub pull request").WithCategory("action").WithIcon(iconGitHub).
	WithDescription("Read one pull request and its current head/base revisions").
	AddSection("Pull request").
	AddExpressionField("owner", "Owner", resolver.WithRequired()).
	AddExpressionField("repository", "Repository", resolver.WithRequired()).
	AddExpressionField("number", "Pull request number", resolver.WithRequired()).
	EndSection().Build()

var pullRequestFilesSchema = resolver.NewSchemaBuilder("github-pull-request-files").
	WithName("List GitHub pull request files").WithCategory("action").WithIcon(iconGitHub).
	WithDescription("Read a bounded file and patch projection for one pull request").
	AddSection("Pull request").
	AddExpressionField("owner", "Owner", resolver.WithRequired()).
	AddExpressionField("repository", "Repository", resolver.WithRequired()).
	AddExpressionField("number", "Pull request number", resolver.WithRequired()).
	EndSection().Build()

var pullRequestReviewCreateSchema = resolver.NewSchemaBuilder("github-pull-request-review-create").
	WithName("Submit GitHub pull request review").WithCategory("action").WithIcon(iconGitHub).
	WithDescription("Submit an idempotent COMMENT, APPROVE, or REQUEST_CHANGES review").
	AddSection("Pull request").
	AddExpressionField("owner", "Owner", resolver.WithRequired()).
	AddExpressionField("repository", "Repository", resolver.WithRequired()).
	AddExpressionField("number", "Pull request number", resolver.WithRequired()).
	EndSection().
	AddSection("Review").
	AddExpressionField("event", "Decision", resolver.WithRequired()).
	AddTextareaField("body", "Review summary", resolver.WithRows(10), resolver.WithRequired()).
	AddExpressionField("commitId", "Reviewed commit SHA").
	EndSection().Build()

type repositoryGetExecutor struct{}

func (*repositoryGetExecutor) Type() string { return "github-repository-get" }

func (*repositoryGetExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	owner, repository, token, err := repositoryConfig(step, resolver)
	if err != nil {
		return nil, err
	}
	var result map[string]interface{}
	if err = githubRequest(ctx, token, http.MethodGet, "/repos/"+url.PathEscape(owner)+"/"+url.PathEscape(repository), nil, &result); err != nil {
		return nil, err
	}
	return &executor.StepResult{Output: map[string]interface{}{"repository": projectRepository(result)}}, nil
}

type pullRequestListExecutor struct{}

func (*pullRequestListExecutor) Type() string { return "github-pull-request-list" }

func (*pullRequestListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	owner, repository, token, err := repositoryConfig(step, resolver)
	if err != nil {
		return nil, err
	}
	state := strings.ToLower(configString(step, "state"))
	if state == "" {
		state = "open"
	}
	if state != "open" && state != "closed" && state != "all" {
		return nil, errors.New("state must be open, closed, or all")
	}
	endpoint := "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repository) + "/pulls?per_page=50&state=" + url.QueryEscape(state)
	var result []map[string]interface{}
	if err = githubRequest(ctx, token, http.MethodGet, endpoint, nil, &result); err != nil {
		return nil, err
	}
	items := make([]map[string]interface{}, 0, len(result))
	for _, item := range result {
		items = append(items, projectPullRequest(item))
	}
	return &executor.StepResult{Output: map[string]interface{}{"pullRequests": items, "count": len(items)}}, nil
}

type pullRequestCreateExecutor struct{}

func (*pullRequestCreateExecutor) Type() string { return "github-pull-request-create" }

func (*pullRequestCreateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	owner, repository, token, err := repositoryConfig(step, resolver)
	if err != nil {
		return nil, err
	}
	title, body := configString(step, "title"), configString(step, "body")
	head, base := configString(step, "head"), configString(step, "base")
	if title == "" || len(title) > 256 || !branchPattern.MatchString(head) || !branchPattern.MatchString(base) {
		return nil, errors.New("title and valid head/base branches are required")
	}
	endpoint := "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repository) + "/pulls"
	var result map[string]interface{}
	err = githubRequest(ctx, token, http.MethodPost, endpoint, map[string]interface{}{"title": title, "body": body, "head": head, "base": base}, &result)
	if errors.Is(err, errGitHubConflict) {
		query := endpoint + "?state=open&per_page=20&head=" + url.QueryEscape(owner+":"+head) + "&base=" + url.QueryEscape(base)
		var existing []map[string]interface{}
		if lookupErr := githubRequest(ctx, token, http.MethodGet, query, nil, &existing); lookupErr != nil || len(existing) == 0 {
			return nil, err
		}
		result = existing[0]
		err = nil
	}
	if err != nil {
		return nil, err
	}
	return &executor.StepResult{Output: map[string]interface{}{"pullRequest": projectPullRequest(result)}}, nil
}

type pullRequestGetExecutor struct{}

func (*pullRequestGetExecutor) Type() string { return "github-pull-request-get" }

func (*pullRequestGetExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	owner, repository, token, err := repositoryConfig(step, resolver)
	if err != nil {
		return nil, err
	}
	number, err := pullRequestNumber(step)
	if err != nil {
		return nil, err
	}
	var result map[string]interface{}
	if err = githubRequest(ctx, token, http.MethodGet, repositoryEndpoint(owner, repository)+"/pulls/"+strconv.Itoa(number), nil, &result); err != nil {
		return nil, err
	}
	return &executor.StepResult{Output: map[string]interface{}{"pullRequest": projectPullRequest(result)}}, nil
}

type pullRequestFilesExecutor struct{}

func (*pullRequestFilesExecutor) Type() string { return "github-pull-request-files" }

func (*pullRequestFilesExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	owner, repository, token, err := repositoryConfig(step, resolver)
	if err != nil {
		return nil, err
	}
	number, err := pullRequestNumber(step)
	if err != nil {
		return nil, err
	}
	var result []map[string]interface{}
	endpoint := repositoryEndpoint(owner, repository) + "/pulls/" + strconv.Itoa(number) + "/files?per_page=100"
	if err = githubRequest(ctx, token, http.MethodGet, endpoint, nil, &result); err != nil {
		return nil, err
	}
	files := make([]map[string]interface{}, 0, len(result))
	for _, file := range result {
		files = append(files, map[string]interface{}{
			"filename": file["filename"], "status": file["status"], "additions": file["additions"],
			"deletions": file["deletions"], "changes": file["changes"], "patch": file["patch"],
		})
	}
	return &executor.StepResult{Output: map[string]interface{}{"files": files, "count": len(files)}}, nil
}

type pullRequestReviewCreateExecutor struct{}

func (*pullRequestReviewCreateExecutor) Type() string { return "github-pull-request-review-create" }

func (*pullRequestReviewCreateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	owner, repository, token, err := repositoryConfig(step, resolver)
	if err != nil {
		return nil, err
	}
	number, err := pullRequestNumber(step)
	if err != nil {
		return nil, err
	}
	event := strings.ToUpper(configString(step, "event"))
	body, commitID := configString(step, "body"), configString(step, "commitId")
	if event != "COMMENT" && event != "APPROVE" && event != "REQUEST_CHANGES" {
		return nil, errors.New("review event must be COMMENT, APPROVE, or REQUEST_CHANGES")
	}
	if body == "" || len(body) > 65536 {
		return nil, errors.New("a bounded review body is required")
	}
	payload := map[string]interface{}{"event": event, "body": body}
	if commitID != "" {
		if !commitPattern.MatchString(commitID) {
			return nil, errors.New("review commitId must be a Git commit SHA")
		}
		payload["commit_id"] = commitID
	}
	var result map[string]interface{}
	endpoint := repositoryEndpoint(owner, repository) + "/pulls/" + strconv.Itoa(number) + "/reviews"
	if err = githubRequest(ctx, token, http.MethodPost, endpoint, payload, &result); err != nil {
		return nil, err
	}
	return &executor.StepResult{Output: map[string]interface{}{"review": map[string]interface{}{
		"id": result["id"], "state": result["state"], "url": result["html_url"], "body": result["body"], "commitId": result["commit_id"],
	}}}, nil
}

var errGitHubConflict = errors.New("GitHub rejected a conflicting pull request")

func githubRequest(ctx context.Context, token, method, endpoint string, body, output interface{}) error {
	if strings.TrimSpace(token) == "" {
		return errors.New("GitHub credential is required")
	}
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return errors.New("GitHub request is invalid")
		}
		reader = strings.NewReader(string(encoded))
	}
	request, err := http.NewRequestWithContext(ctx, method, githubAPIBase+endpoint, reader)
	if err != nil {
		return errors.New("GitHub request could not be created")
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := githubClient.Do(request)
	if err != nil {
		return errors.New("GitHub request failed")
	}
	defer response.Body.Close()
	limited := io.LimitReader(response.Body, 1<<20)
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		if response.StatusCode == http.StatusUnprocessableEntity || response.StatusCode == http.StatusConflict {
			return errGitHubConflict
		}
		return fmt.Errorf("GitHub request returned HTTP %d", response.StatusCode)
	}
	if output == nil {
		_, _ = io.Copy(io.Discard, limited)
		return nil
	}
	if err = json.NewDecoder(limited).Decode(output); err != nil {
		return errors.New("GitHub returned an invalid response")
	}
	return nil
}

func repositoryConfig(step *executor.StepDefinition, resolver executor.TemplateResolver) (string, string, string, error) {
	owner, repository := configString(step, "owner"), configString(step, "repository")
	token := ""
	if bindings, ok := resolver.(executor.BindingResolver); ok {
		token, _ = bindings.GetBinding("token").(string)
	}
	if token = strings.TrimSpace(token); token == "" {
		token = configString(step, "token")
	}
	if !namePattern.MatchString(owner) || !namePattern.MatchString(repository) {
		return "", "", "", errors.New("valid GitHub owner and repository are required")
	}
	if token == "" {
		return "", "", "", errors.New("GitHub credential is required")
	}
	return owner, repository, token, nil
}

func repositoryEndpoint(owner, repository string) string {
	return "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repository)
}

func pullRequestNumber(step *executor.StepDefinition) (int, error) {
	var number int
	switch value := step.Config["number"].(type) {
	case int:
		number = value
	case int64:
		number = int(value)
	case float64:
		number = int(value)
	case string:
		number, _ = strconv.Atoi(strings.TrimSpace(value))
	}
	if number < 1 {
		return 0, errors.New("a positive pull request number is required")
	}
	return number, nil
}

func configString(step *executor.StepDefinition, key string) string {
	if step == nil {
		return ""
	}
	value, _ := step.Config[key].(string)
	return strings.TrimSpace(value)
}

func projectRepository(value map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{"id": value["id"], "fullName": value["full_name"], "defaultBranch": value["default_branch"], "private": value["private"], "url": value["html_url"]}
}

func projectPullRequest(value map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{
		"number": value["number"], "title": value["title"], "body": value["body"], "state": value["state"],
		"url": value["html_url"], "draft": value["draft"], "mergeable": value["mergeable"],
		"head": nestedString(value, "head", "ref"), "headSha": nestedString(value, "head", "sha"),
		"base": nestedString(value, "base", "ref"), "baseSha": nestedString(value, "base", "sha"),
		"author": nestedString(value, "user", "login"), "changedFiles": value["changed_files"],
		"additions": value["additions"], "deletions": value["deletions"], "commits": value["commits"],
	}
}

func nestedString(value map[string]interface{}, outer, inner string) string {
	nested, _ := value[outer].(map[string]interface{})
	result, _ := nested[inner].(string)
	return result
}
