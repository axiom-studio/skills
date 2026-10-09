package main

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/axiom-studio/skills.sdk/executor"
	"github.com/axiom-studio/skills/internal/isolation"
)

type workspaceBindings struct {
	executor.TemplateResolver
	token string
}

func (b workspaceBindings) GetBinding(name string) interface{} {
	if name == credentialName {
		return b.token
	}
	return nil
}
func (b workspaceBindings) GetBindings() map[string]interface{} {
	return map[string]interface{}{credentialName: b.token}
}

func TestWorkspaceSharedRuntimeKeepsTenantsApart(t *testing.T) {
	var op operation
	for _, candidate := range operations() {
		if candidate.Name == "google-drive-list-files" {
			op = candidate
		}
	}
	if op.Name == "" {
		t.Fatal("google-drive-list-files operation is missing")
	}
	previous := client
	t.Cleanup(func() { client = previous })
	client = &http.Client{Transport: workspaceTransport(func(r *http.Request) (*http.Response, error) {
		tenant, ok := isolation.TenantForCredential(isolation.BearerCredential(r))
		if !ok {
			t.Errorf("Google received an unknown or missing credential")
			return &http.Response{StatusCode: 401, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
		}
		body := `{"files":[{"id":"` + tenant.Marker + `"}]}`
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	shared := &workspaceExecutor{op: op}
	isolation.Run(t, 16, func(tenant isolation.Tenant) (interface{}, error) {
		result, err := shared.Execute(context.Background(), &executor.StepDefinition{Config: map[string]any{}}, workspaceBindings{token: tenant.Credential})
		if err != nil {
			return nil, err
		}
		return result.Output, nil
	})
	if _, err := shared.Execute(context.Background(), &executor.StepDefinition{Config: map[string]any{}}, workspaceBindings{}); err == nil {
		t.Fatal("a request without credentials reused another tenant's")
	}
}
