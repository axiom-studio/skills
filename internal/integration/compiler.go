package integration

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/axiom-studio/skills.sdk/executor"
	"gopkg.in/yaml.v3"
)

// Compile produces a canonical SkillDefinition and a normalized profile. It is pure:
// it neither fetches documentation nor executes, installs, or authorizes operations.
func Compile(p *Profile) (map[string]interface{}, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	hash := Digest(p)
	actions := map[string]interface{}{}
	actions[p.Transport+"-describe"] = action(p.Transport+"-describe", "Inspect mounted integration contract", "read", object(map[string]interface{}{}), nil)
	for _, op := range p.Operations {
		schema := object(map[string]interface{}{"profileHash": map[string]interface{}{"type": "string", "const": hash}, "arguments": op.InputSchema}, "profileHash", "arguments")
		actions[op.Name] = action(op.Name, op.Description, op.Effect, schema, p.Credential)
	}
	if p.Transport == "mcp" {
		actions["mcp-discover"] = action("mcp-discover", "Discover tool definitions without invoking them", "read", object(map[string]interface{}{"profileHash": map[string]interface{}{"type": "string", "const": hash}}, "profileHash"), p.Credential)
	}
	manifest := map[string]interface{}{
		"apiVersion": "openseal.dev/v1alpha1", "kind": "SkillDefinition",
		"definition": map[string]interface{}{
			"id": "skill-" + p.ID, "version": RuntimeVersion + "-" + hash[:12], "name": p.ID, "description": "Pinned " + p.Transport + " integration",
			"actions": actions, "transport": map[string]interface{}{"kind": "tool", "endpoint": "skill-" + p.ID},
			"installers": []interface{}{map[string]interface{}{"id": "oci", "kind": "oci", "package": "axiomstudio/skill-" + p.Transport + ":" + RuntimeVersion}},
			"source":     map[string]interface{}{"format": "axiom.skill/v1", "reference": p.ID, "resolvedVersion": RuntimeVersion + "-" + hash[:12]},
		},
	}
	data, err := yaml.Marshal(manifest)
	if err != nil {
		return nil, err
	}
	result := map[string]interface{}{"manifest": string(data), "profile": p, "profileHash": hash}
	plan, planErr := BindingPlan(p)
	if planErr != nil {
		result["bindingPlanError"] = planErr.Error()
	} else {
		result["bindingPlan"] = plan
	}
	return result, nil
}
func object(props map[string]interface{}, required ...string) map[string]interface{} {
	m := map[string]interface{}{"type": "object", "properties": props, "additionalProperties": false}
	if len(required) > 0 {
		m["required"] = required
	}
	return m
}
func action(name, description, effect string, schema map[string]interface{}, credential *Credential) map[string]interface{} {
	risk, side := "production", "external"
	if effect == "read" {
		risk, side = "read", "read"
	}
	a := map[string]interface{}{"name": name, "description": description, "inputSchema": schema, "risk": risk, "sideEffect": side, "idempotency": "none", "retry": map[string]interface{}{"maxAttempts": 1}, "transport": map[string]interface{}{"kind": "tool", "endpoint": name}}
	if effect == "write" {
		a["idempotency"] = "required"
	}
	if credential != nil {
		a["credentials"] = []interface{}{map[string]interface{}{"name": credential.Binding, "kind": credential.Binding}}
	}
	return a
}

type CompilerAdapter struct{ Transport string }

func (a *CompilerAdapter) Type() string { return a.Transport + "-compile" }
func (a *CompilerAdapter) Execute(_ context.Context, step *executor.StepDefinition, res executor.TemplateResolver) (*executor.StepResult, error) {
	// Profile is literal contract data, never template-resolved against secret bindings.
	raw, err := json.Marshal(step.Config["profile"])
	if err != nil || len(raw) > MaxBytes {
		return nil, fmt.Errorf("invalid or oversized profile")
	}
	p, err := decodeProfile(raw)
	if err != nil {
		return nil, err
	}
	if p.Transport != a.Transport {
		return nil, fmt.Errorf("wrong transport for compiler")
	}
	result, err := Compile(p)
	if err != nil {
		return nil, err
	}
	return &executor.StepResult{Output: result}, nil
}
