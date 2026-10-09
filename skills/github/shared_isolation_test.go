package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/axiom-studio/skills/internal/isolation"
	"github.com/axiom-studio/skills.sdk/executor"
)

func TestGitHubSharedRuntimeKeepsTenantsApart(t *testing.T) {
	server := isolation.Upstream(t, isolation.BearerCredential, func(w http.ResponseWriter, _ *http.Request, tenant isolation.Tenant) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"name": "README.md", "path": "README.md", "type": "file", "sha": "0123456789abcdef0123456789abcdef01234567",
			"content": base64.StdEncoding.EncodeToString([]byte(tenant.Marker)),
		})
	})
	previousBase, previousClient := githubAPIBase, githubClient
	githubAPIBase, githubClient = server.URL, server.Client()
	t.Cleanup(func() { githubAPIBase, githubClient = previousBase, previousClient })
	step := func() *executor.StepDefinition {
		return &executor.StepDefinition{Config: map[string]interface{}{"owner": "axiom-studio", "repository": "cortex", "path": "README.md", "ref": "main"}}
	}
	isolation.Run(t, 16, func(tenant isolation.Tenant) (interface{}, error) {
		result, err := executeRepositoryContentGet(context.Background(), step(), githubBindingResolver{bindings: map[string]interface{}{"token": tenant.Credential}})
		if err != nil {
			return nil, err
		}
		return result.Output, nil
	})
	if _, err := executeRepositoryContentGet(context.Background(), step(), githubBindingResolver{bindings: map[string]interface{}{}}); err == nil {
		t.Fatal("a request without credentials reused another tenant's")
	}
}
