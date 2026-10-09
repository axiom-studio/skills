package main

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"testing"

	"github.com/axiom-studio/skills.sdk/executor"
	"github.com/axiom-studio/skills.sdk/resolver"
	"github.com/axiom-studio/skills/internal/isolation"
)

var sigV4AccessKey = regexp.MustCompile(`Credential=([^/]+)/`)

func TestAWSSharedRuntimeKeepsTenantsApart(t *testing.T) {
	server := isolation.Upstream(t, func(r *http.Request) string {
		if match := sigV4AccessKey.FindStringSubmatch(r.Header.Get("Authorization")); match != nil {
			return match[1]
		}
		return ""
	}, func(w http.ResponseWriter, _ *http.Request, tenant isolation.Tenant) {
		w.Header().Set("Content-Type", "application/xml")
		fmt.Fprintf(w, `<ListAllMyBucketsResult><Buckets><Bucket><Name>%s</Name><CreationDate>2026-01-01T00:00:00.000Z</CreationDate></Bucket></Buckets></ListAllMyBucketsResult>`, tenant.Marker)
	})
	previous := awsEndpointOverride
	awsEndpointOverride = server.URL
	t.Cleanup(func() { awsEndpointOverride = previous })
	call := func(bindings map[string]interface{}) (*executor.StepResult, error) {
		step := &executor.StepDefinition{Config: map[string]interface{}{"region": "eu-west-1"}}
		return (&S3ListBucketsExecutor{}).Execute(context.Background(), step, resolver.New(resolver.Config{Bindings: bindings}))
	}
	isolation.Run(t, 16, func(tenant isolation.Tenant) (interface{}, error) {
		result, err := call(map[string]interface{}{awsAccessKeyIDCredential: tenant.Credential, awsSecretAccessKeyCredential: "secret-" + tenant.Name})
		if err != nil {
			return nil, err
		}
		return result.Output, nil
	})
	for name, bindings := range map[string]map[string]interface{}{
		"no credentials": {},
		"no secret":      {awsAccessKeyIDCredential: isolation.Tenants[0].Credential},
	} {
		if _, err := call(bindings); err == nil {
			t.Fatalf("%s: the request used another identity instead of failing", name)
		}
	}
	if _, err := (&S3ListBucketsExecutor{}).Execute(context.Background(), &executor.StepDefinition{Config: map[string]interface{}{"region": "../evil"}},
		resolver.New(resolver.Config{Bindings: map[string]interface{}{awsAccessKeyIDCredential: "a", awsSecretAccessKeyCredential: "b"}})); err == nil {
		t.Fatal("an invalid region was accepted")
	}
}
