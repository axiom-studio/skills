package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"github.com/axiom-studio/skills.sdk/executor"
	"github.com/axiom-studio/skills.sdk/resolver"
	"github.com/axiom-studio/skills/internal/isolation"
)

type adyenRedirect struct{ target *url.URL }

func (r adyenRedirect) RoundTrip(request *http.Request) (*http.Response, error) {
	request.URL.Scheme, request.URL.Host = r.target.Scheme, r.target.Host
	return http.DefaultTransport.RoundTrip(request)
}

func TestAdyenSharedRuntimeKeepsTenantsApart(t *testing.T) {
	server := isolation.Upstream(t, func(r *http.Request) string { return r.Header.Get("X-API-Key") }, func(w http.ResponseWriter, _ *http.Request, tenant isolation.Tenant) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"merchantReference": tenant.Marker})
	})
	target, _ := url.Parse(server.URL)
	previous := adyenClient
	adyenClient = &http.Client{Transport: adyenRedirect{target}}
	t.Cleanup(func() { adyenClient = previous })
	call := func(bindings map[string]interface{}) (*executor.StepResult, error) {
		step := &executor.StepDefinition{Config: map[string]interface{}{"merchantAccount": "Merchant", "pspReference": "PSP123"}}
		return (&PaymentGetExecutor{}).Execute(context.Background(), step, resolver.New(resolver.Config{Bindings: bindings}))
	}
	isolation.Run(t, 16, func(tenant isolation.Tenant) (interface{}, error) {
		result, err := call(map[string]interface{}{adyenCredentialName: tenant.Credential})
		if err != nil {
			return nil, err
		}
		return result.Output, nil
	})
	if _, err := call(map[string]interface{}{}); err == nil {
		t.Fatal("a request without credentials reused another tenant's")
	}
}
