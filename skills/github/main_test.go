package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/axiom-studio/skills.sdk/executor"
)

func TestPullRequestCreateUsesBearerCredentialAndProjectsResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/repos/axiom-studio/cortex/pulls" || request.Header.Get("Authorization") != "Bearer SECRET" {
			t.Fatalf("request = %s %s auth=%q", request.Method, request.URL.Path, request.Header.Get("Authorization"))
		}
		_ = json.NewEncoder(response).Encode(map[string]interface{}{"number": 42, "title": "Fix", "state": "open", "html_url": "https://github.com/axiom-studio/cortex/pull/42", "head": map[string]interface{}{"ref": "fix/test"}, "base": map[string]interface{}{"ref": "main"}})
	}))
	defer server.Close()
	previousBase, previousClient := githubAPIBase, githubClient
	githubAPIBase, githubClient = server.URL, server.Client()
	defer func() { githubAPIBase, githubClient = previousBase, previousClient }()
	result, err := (&pullRequestCreateExecutor{}).Execute(context.Background(), &executor.StepDefinition{Config: map[string]interface{}{"owner": "axiom-studio", "repository": "cortex", "token": "SECRET", "title": "Fix", "head": "fix/test", "base": "main"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	pullRequest := result.Output["pullRequest"].(map[string]interface{})
	if pullRequest["number"] != float64(42) || pullRequest["head"] != "fix/test" {
		t.Fatalf("pull request = %#v", pullRequest)
	}
}

func TestRepositoryConfigurationRejectsUnsafeIdentityAndMissingCredential(t *testing.T) {
	for name, config := range map[string]map[string]interface{}{
		"unsafe owner":  {"owner": "../owner", "repository": "repo", "token": "SECRET"},
		"missing token": {"owner": "owner", "repository": "repo"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, _, err := repositoryConfig(&executor.StepDefinition{Config: config}); err == nil {
				t.Fatal("invalid repository authority was accepted")
			}
		})
	}
}
