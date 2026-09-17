package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/axiom-studio/skills.sdk/executor"
	"github.com/axiom-studio/skills.sdk/resolver"
)

func configFromPlan(t *testing.T, p *Profile) (map[string]interface{}, map[string]interface{}) {
	t.Helper()
	plan, err := BindingPlan(p)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(plan)
	var normalized map[string]interface{}
	_ = json.Unmarshal(b, &normalized)
	args := normalized["upsertArguments"].(map[string]interface{})
	return args["config"].(map[string]interface{}), normalized
}
func TestBindingPlanRoundTripAndStableRestrictions(t *testing.T) {
	p := fixture()
	cfg, plan := configFromPlan(t, p)
	rebuilt, err := boundProfile(cfg["integration"])
	if err != nil {
		t.Fatal(err)
	}
	if Digest(rebuilt) != Digest(p) {
		t.Fatal("bound contract does not match compiled hash")
	}
	args := plan["upsertArguments"].(map[string]interface{})
	if args["maximumRisk"] != "read" || args["skillId"] != "skill-api" || args["skillVersion"] != RuntimeVersion {
		t.Fatal(args)
	}
	if !reflect.DeepEqual(args["allowedActions"], []interface{}{"api-inspect", "api-read"}) {
		t.Fatal(args["allowedActions"])
	}
	restrictions := args["argumentRestrictions"].(map[string]interface{})["api-read"].(map[string]interface{})
	if restrictions["profileHash"].(map[string]interface{})["const"] != Digest(p) {
		t.Fatal("profile not pinned")
	}
	if !reflect.DeepEqual(restrictions["operation"].(map[string]interface{})["enum"], []interface{}{"get-record"}) {
		t.Fatal("operations not restricted")
	}
	if _, ok := args["accessReferences"]; ok || plan["requiresAccessReference"] != true {
		t.Fatal("credential identity was invented")
	}
}
func TestBoundRuntimeUsesConfiguredCredentialAndDoesNotResolveProfileTemplates(t *testing.T) {
	p := fixture()
	p.Operations[0].Description = "{{bindings.integration-access.token}}"
	cfg, _ := configFromPlan(t, p)
	cfg["profileHash"] = Digest(p)
	cfg["operation"] = "get-record"
	cfg["arguments"] = map[string]interface{}{"recordId": "1"}
	calls := 0
	a := &BoundAdapter{Transport: "api", Mode: "read", newRuntime: func(p *Profile) (*Runtime, error) {
		return fakeRuntime(t, p, func(req *http.Request) (*http.Response, error) {
			calls++
			if req.Header.Get("X-Client-Key") != "private-value" {
				t.Fatal("wrong credential")
			}
			return response(200, `{"value":7}`), nil
		}), nil
	}}
	res := resolver.New(resolver.Config{Bindings: map[string]interface{}{AccessBinding: map[string]interface{}{"token": "private-value"}}})
	if _, err := a.Execute(context.Background(), &executor.StepDefinition{Config: cfg}, res); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal(calls)
	}
	profile, _ := boundProfile(cfg["integration"])
	if strings.Contains(profile.Operations[0].Description, "private-value") {
		t.Fatal("secret expanded into profile")
	}
}
func TestBoundReadCannotExecuteWriteOrChangedContract(t *testing.T) {
	p := fixture()
	p.Operations[0].Method = "DELETE"
	p.Operations[0].Effect = "write"
	cfg, _ := configFromPlan(t, p)
	cfg["profileHash"] = Digest(p)
	cfg["operation"] = "get-record"
	cfg["arguments"] = map[string]interface{}{"recordId": "1"}
	calls := 0
	factory := func(p *Profile) (*Runtime, error) {
		return fakeRuntime(t, p, func(*http.Request) (*http.Response, error) { calls++; return response(200, `{}`), nil }), nil
	}
	a := &BoundAdapter{Transport: "api", Mode: "read", newRuntime: factory}
	if _, err := a.Execute(context.Background(), &executor.StepDefinition{Config: cfg}, nil); err == nil || !strings.Contains(err.Error(), "effect") {
		t.Fatal("write accepted on read action", err)
	}
	a.Mode = "write"
	cfg["profileHash"] = "outdated"
	if _, err := a.Execute(context.Background(), &executor.StepDefinition{Config: cfg}, nil); err == nil {
		t.Fatal("stale profile accepted")
	}
	if calls != 0 {
		t.Fatal("rejected calls reached network")
	}
}
func TestBoundCredentialHostTransportAndMissingConfig(t *testing.T) {
	p := fixture()
	for _, v := range []interface{}{"raw-access-value", `{"token":"raw-access-value"}`, map[string]interface{}{"token": "raw-access-value"}} {
		got, err := boundToken(p, map[string]interface{}{AccessBinding: v}, nil)
		if err != nil || got != "raw-access-value" {
			t.Fatal(got, err)
		}
	}
	if _, err := boundToken(p, map[string]interface{}{}, nil); err == nil {
		t.Fatal("missing auth accepted")
	}
	a := &BoundAdapter{Transport: "api", Mode: "read"}
	if _, err := a.Execute(context.Background(), &executor.StepDefinition{Config: map[string]interface{}{}}, nil); err == nil {
		t.Fatal("caller without host configuration accepted")
	}
}
func TestBindingConfigNeverEncodesSecretsToBypassHost(t *testing.T) {
	p := fixture()
	p.FixedQuery["api_key"] = "should-not-be-stored"
	if _, err := BindingPlan(p); err == nil {
		t.Fatal("secret-like configuration was accepted")
	}
	bundle, err := Compile(p)
	if err != nil {
		t.Fatal(err)
	}
	if bundle["bindingPlanError"] == nil || bundle["bindingPlan"] != nil {
		t.Fatal("invalid activation plan returned")
	}
}
func TestBaseManifestUsesHostOnlyConfigurationAndExactTransports(t *testing.T) {
	for _, kind := range []string{"api", "mcp"} {
		m, err := BaseManifest(kind, "Learn the service")
		if err != nil {
			t.Fatal(err)
		}
		def := m["definition"].(map[string]interface{})
		actions := def["actions"].(map[string]interface{})
		for name, raw := range actions {
			a := raw.(map[string]interface{})
			if a["transport"].(map[string]interface{})["endpoint"] != name {
				t.Fatal("incorrect routing")
			}
			props := a["inputSchema"].(map[string]interface{})["properties"].(map[string]interface{})
			if props["integration"] != nil || props[AccessBinding] != nil {
				t.Fatal("host-only config exposed as model argument")
			}
		}
		if actions[kind+"-read"].(map[string]interface{})["risk"] != "read" || actions[kind+"-write"].(map[string]interface{})["risk"] != "production" {
			t.Fatal("effect policy wrong")
		}
	}
}
