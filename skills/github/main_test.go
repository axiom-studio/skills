package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/axiom-studio/skills.sdk/executor"
	"gopkg.in/yaml.v3"
)

type githubBindingResolver struct {
	executor.TemplateResolver
	bindings map[string]interface{}
}

func TestRepositoryContentGetDecodesBoundedText(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/repos/axiom-studio/cortex/contents/README.md" || request.URL.Query().Get("ref") != "main" {
			t.Fatalf("request URL = %s", request.URL.String())
		}
		_ = json.NewEncoder(response).Encode(map[string]interface{}{
			"name": "README.md", "path": "README.md", "type": "file", "sha": strings.Repeat("a", 40), "content": "aGVsbG8=",
		})
	}))
	defer server.Close()
	previousBase, previousClient := githubAPIBase, githubClient
	githubAPIBase, githubClient = server.URL, server.Client()
	defer func() { githubAPIBase, githubClient = previousBase, previousClient }()
	step := &executor.StepDefinition{Config: map[string]interface{}{"owner": "axiom-studio", "repository": "cortex", "path": "README.md", "ref": "main"}}
	result, err := executeRepositoryContentGet(context.Background(), step, githubBindingResolver{bindings: map[string]interface{}{"token": "SECRET"}})
	if err != nil {
		t.Fatal(err)
	}
	content := result.Output["content"].(map[string]interface{})
	if content["content"] != "hello" {
		t.Fatalf("content = %#v", content)
	}
}

func TestPullRequestMergePinsInspectedRevision(t *testing.T) {
	headSHA := strings.Repeat("b", 40)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPut || request.URL.Path != "/repos/axiom-studio/cortex/pulls/42/merge" {
			t.Fatalf("request = %s %s", request.Method, request.URL.Path)
		}
		var payload map[string]interface{}
		_ = json.NewDecoder(request.Body).Decode(&payload)
		if payload["sha"] != headSHA || payload["merge_method"] != "squash" {
			t.Fatalf("payload = %#v", payload)
		}
		_ = json.NewEncoder(response).Encode(map[string]interface{}{"merged": true, "sha": headSHA, "message": "merged"})
	}))
	defer server.Close()
	previousBase, previousClient := githubAPIBase, githubClient
	githubAPIBase, githubClient = server.URL, server.Client()
	defer func() { githubAPIBase, githubClient = previousBase, previousClient }()
	step := &executor.StepDefinition{Config: map[string]interface{}{"owner": "axiom-studio", "repository": "cortex", "number": 42, "headSha": headSHA, "method": "squash"}}
	if _, err := executePullRequestMerge(context.Background(), step, githubBindingResolver{bindings: map[string]interface{}{"token": "SECRET"}}); err != nil {
		t.Fatal(err)
	}
}

func TestManifestMatchesCompleteGitHubSurface(t *testing.T) {
	raw, err := os.ReadFile("skill.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Definition struct {
			Version string                 `yaml:"version"`
			Actions map[string]interface{} `yaml:"actions"`
			Prompt  struct {
				AllowedTools []string `yaml:"allowedTools"`
			} `yaml:"prompt"`
			Installers []struct {
				Package string `yaml:"package"`
			} `yaml:"installers"`
		} `yaml:"definition"`
	}
	if err = yaml.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Definition.Version != skillVersion || len(manifest.Definition.Actions) != 24 || len(manifest.Definition.Prompt.AllowedTools) != 24 {
		t.Fatalf("manifest version=%q actions=%d allowed=%d", manifest.Definition.Version, len(manifest.Definition.Actions), len(manifest.Definition.Prompt.AllowedTools))
	}
	for _, action := range manifest.Definition.Prompt.AllowedTools {
		if _, ok := manifest.Definition.Actions[action]; !ok {
			t.Fatalf("allowed tool %q has no action", action)
		}
	}
	if len(manifest.Definition.Installers) != 1 || manifest.Definition.Installers[0].Package != "axiomstudio/skill-github:"+skillVersion {
		t.Fatalf("installer = %#v", manifest.Definition.Installers)
	}
}

func (r githubBindingResolver) GetBinding(name string) interface{}  { return r.bindings[name] }
func (r githubBindingResolver) GetBindings() map[string]interface{} { return r.bindings }

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
	step := &executor.StepDefinition{Config: map[string]interface{}{"owner": "axiom-studio", "repository": "cortex", "title": "Fix", "head": "fix/test", "base": "main"}}
	result, err := (&pullRequestCreateExecutor{}).Execute(context.Background(), step, githubBindingResolver{bindings: map[string]interface{}{"token": "SECRET"}})
	if err != nil {
		t.Fatal(err)
	}
	pullRequest := result.Output["pullRequest"].(map[string]interface{})
	if pullRequest["number"] != float64(42) || pullRequest["head"] != "fix/test" {
		t.Fatalf("pull request = %#v", pullRequest)
	}
	if _, leaked := step.Config["token"]; leaked {
		t.Fatal("governed credential leaked into ordinary action config")
	}
}

func TestRepositoryConfigurationRejectsUnsafeIdentityAndMissingCredential(t *testing.T) {
	for name, config := range map[string]map[string]interface{}{
		"unsafe owner":  {"owner": "../owner", "repository": "repo", "token": "SECRET"},
		"missing token": {"owner": "owner", "repository": "repo"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, _, err := repositoryConfig(&executor.StepDefinition{Config: config}, nil); err == nil {
				t.Fatal("invalid repository authority was accepted")
			}
		})
	}
}
