package platformtests

import (
	"context"
	"encoding/json"
	kernelruntime "github.com/axiom-studio/openseal/pkg/runtime"
	"path/filepath"
	"testing"

	"github.com/axiom-studio/openseal/pkg/skill"
	"github.com/axiom-studio/skills.sdk/executor"
	"github.com/axiom-studio/skills/internal/integration"
)

func TestCompiledPlanBindsAndExecutesThroughOpenSeal(t *testing.T) {
	for _, transport := range []string{"api", "mcp"} {
		t.Run(transport, func(t *testing.T) {
			path := "../../skills/api/examples/records.json"
			if transport == "mcp" {
				path = "../../skills/mcp/examples/connection.json"
			}
			p, err := integration.Load(path)
			if err != nil {
				t.Fatal(err)
			}
			bundle, err := integration.Compile(p)
			if err != nil {
				t.Fatal(err)
			}
			plan := bundle["bindingPlan"].(map[string]interface{})
			upsert := plan["upsertArguments"].(map[string]interface{})
			data, _ := json.Marshal(upsert)
			var fields struct {
				BindingID            string                                   `json:"bindingId"`
				SkillID              string                                   `json:"skillId"`
				SkillVersion         string                                   `json:"skillVersion"`
				AllowedActions       []string                                 `json:"allowedActions"`
				MaximumRisk          skill.RiskLevel                          `json:"maximumRisk"`
				Config               map[string]interface{}                   `json:"config"`
				ArgumentRestrictions map[string]map[string]skill.ArgumentRule `json:"argumentRestrictions"`
			}
			if err = json.Unmarshal(data, &fields); err != nil {
				t.Fatal(err)
			}
			base, err := integration.BaseManifest(transport, "Learn and use a service")
			if err != nil {
				t.Fatal(err)
			}
			data, _ = json.Marshal(base["definition"])
			var def skill.Definition
			if err = json.Unmarshal(data, &def); err != nil {
				t.Fatal(err)
			}
			dbPath := filepath.Join(t.TempDir(), "bindings.db")
			store, err := kernelruntime.NewSQLiteStore(dbPath)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			catalog := skill.NewCatalogWithStore(store)
			ctx := context.Background()
			if err = catalog.Register(ctx, &def); err != nil {
				t.Fatal("base manifest rejected", err)
			}
			binding := &skill.Binding{ID: fields.BindingID, Scope: skill.ScopeReference{Kind: "tenant", ID: "tenant-a"}, DeploymentID: "agent-a", SkillID: fields.SkillID, SkillVersion: fields.SkillVersion, AllowedActions: fields.AllowedActions, MaximumRisk: fields.MaximumRisk, Config: fields.Config, ArgumentRestrictions: fields.ArgumentRestrictions, Credentials: map[string]skill.CredentialReference{integration.AccessBinding: {Kind: integration.AccessBinding, ID: "opaque-service-access"}}}
			created, err := catalog.UpsertBinding(ctx, skill.UpsertBindingRequest{Binding: binding, ExpectedRevision: 0, Actor: skill.BindingActor{Type: "user", ID: "operator"}, Reason: "Authorize learned service"})
			if err != nil {
				t.Fatal("binding plan rejected", err)
			}
			if created.Revision != 1 {
				t.Fatal("incorrect creation revision")
			}
			if _, err = catalog.UpsertBinding(ctx, skill.UpsertBindingRequest{Binding: binding, ExpectedRevision: 0, Actor: skill.BindingActor{Type: "user", ID: "operator"}, Reason: "Stale replay"}); err == nil {
				t.Fatal("stale creation overwrote existing binding")
			}
			if err = store.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := kernelruntime.NewSQLiteStore(dbPath)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			catalog = skill.NewCatalogWithStore(reopened)
			if err = catalog.Register(ctx, kernelruntime.SkillManagementSkill()); err != nil {
				t.Fatal(err)
			}
			if err = catalog.ValidateDefinitionInput(ctx, kernelruntime.SkillManagementSkillID, kernelruntime.SkillManagementSkillVersion, "", kernelruntime.SkillActionUpsertBinding, upsert); err != nil {
				t.Fatal("management action rejected generated plan", err)
			}
			inspect := transport + "-inspect"
			bound, err := catalog.Resolve(ctx, binding.Scope, binding.DeploymentID, def.ID, def.Version, inspect, skill.BindingReference{ID: created.ID, Revision: created.Revision})
			if err != nil {
				t.Fatal(err)
			}
			input := map[string]interface{}{"profileHash": bundle["profileHash"]}
			if err = catalog.ValidateInput(ctx, bound, input); err != nil {
				t.Fatal(err)
			}
			for _, bad := range []map[string]interface{}{{"profileHash": "wrong"}, {"profileHash": bundle["profileHash"], "integration": fields.Config["integration"]}, {"profileHash": bundle["profileHash"], integration.AccessBinding: "injected-secret"}} {
				if err = catalog.ValidateInput(ctx, bound, bad); err == nil {
					t.Fatal("untrusted argument accepted")
				}
			}
			config, err := skill.MaterializeTransportArguments(bound, input)
			if err != nil {
				t.Fatal(err)
			}
			for k, v := range bound.Binding.Config {
				if _, exists := config[k]; exists {
					t.Fatal("host config collision")
				}
				config[k] = v
			}
			result, err := (&integration.BoundAdapter{Transport: transport, Mode: "inspect"}).Execute(ctx, &executor.StepDefinition{Config: config}, nil)
			if err != nil || result.Output["profileHash"] != bundle["profileHash"] {
				t.Fatal("host invocation failed", err)
			}
			if _, err = catalog.Resolve(ctx, skill.ScopeReference{Kind: "tenant", ID: "tenant-b"}, binding.DeploymentID, def.ID, def.Version, inspect, skill.BindingReference{ID: created.ID, Revision: created.Revision}); err == nil {
				t.Fatal("cross-tenant access succeeded")
			}
			if _, err = catalog.Resolve(ctx, binding.Scope, "agent-b", def.ID, def.Version, inspect, skill.BindingReference{ID: created.ID, Revision: created.Revision}); err == nil {
				t.Fatal("cross-agent access succeeded")
			}
			if transport == "api" {
				read, err := catalog.Resolve(ctx, binding.Scope, binding.DeploymentID, def.ID, def.Version, "api-read", skill.BindingReference{ID: created.ID, Revision: created.Revision})
				if err != nil {
					t.Fatal(err)
				}
				if err = catalog.ValidateInput(ctx, read, map[string]interface{}{"profileHash": bundle["profileHash"], "operation": "record-create", "arguments": map[string]interface{}{}}); err == nil {
					t.Fatal("write accepted through read restriction")
				}
			}
		})
	}
}
