package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/axiom-studio/skills.sdk/executor"
)

const RuntimeVersion = "0.2.0"
const AccessBinding = "integration-access"

// BindingPlan uses OpenSeal's existing governed upsert_binding action. The
// profile is host-owned binding configuration, never a model-call argument.
func BindingPlan(p *Profile) (map[string]interface{}, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	var profile map[string]interface{}
	if err = json.Unmarshal(raw, &profile); err != nil {
		return nil, err
	}
	delete(profile, "credential")
	integration := map[string]interface{}{"profile": profile}
	if p.Credential != nil {
		integration["access"] = p.Credential
	}
	// Match the platform's secret-free configuration invariant. In particular,
	// do not encode the contract as a string to bypass host inspection.
	if err := nonSecretConfig(integration); err != nil {
		return nil, err
	}
	hash := Digest(p)
	names := map[string][]string{}
	blocks := []interface{}{}
	for _, op := range p.Operations {
		entrypoint := p.Transport + "-" + op.Effect
		names[entrypoint] = append(names[entrypoint], op.Name)
		blocks = append(blocks, map[string]interface{}{
			"name": op.Name, "description": op.Description, "inputSchema": op.InputSchema,
			"skillId": "skill-" + p.Transport, "skillVersion": RuntimeVersion, "bindingId": p.ID,
			"action": entrypoint, "arguments": map[string]interface{}{"profileHash": hash, "operation": op.Name},
		})
	}
	inspect := p.Transport + "-inspect"
	allowed := []string{inspect}
	restrictions := map[string]interface{}{inspect: map[string]interface{}{"profileHash": map[string]interface{}{"const": hash}}}
	risk := "read"
	for action, ops := range names {
		sort.Strings(ops)
		allowed = append(allowed, action)
		restrictions[action] = map[string]interface{}{"profileHash": map[string]interface{}{"const": hash}, "operation": map[string]interface{}{"enum": ops}}
		if action == p.Transport+"-write" {
			risk = "production"
		}
	}
	if p.Transport == "mcp" {
		allowed = append(allowed, "mcp-discover-bound")
		restrictions["mcp-discover-bound"] = map[string]interface{}{"profileHash": map[string]interface{}{"const": hash}}
	}
	sort.Strings(allowed)
	arguments := map[string]interface{}{
		"bindingId": p.ID, "expectedRevision": 0, "skillId": "skill-" + p.Transport, "skillVersion": RuntimeVersion,
		"allowedActions": allowed, "enablePrompt": false, "maximumRisk": risk, "argumentRestrictions": restrictions,
		"config": map[string]interface{}{"integration": integration},
	}
	result := map[string]interface{}{"state": "prepared", "managementSkillId": "openseal.skills", "managementAction": "upsert_binding", "upsertArguments": arguments, "blocks": blocks}
	if p.Credential != nil {
		result["requiredAccess"] = map[string]interface{}{"binding": AccessBinding, "kind": AccessBinding, "field": p.Credential.Field}
		result["requiresAccessReference"] = true
	} else {
		result["requiresAccessReference"] = false
	}
	return result, nil
}

// BoundAdapter runs only through the trusted host, which merges binding config
// after schema/authorization checks and rejects collisions with model inputs.
// As with every SDK service, direct untrusted gRPC access is not supported.
type BoundAdapter struct {
	Transport  string
	Mode       string
	newRuntime func(*Profile) (*Runtime, error)
}

func (a *BoundAdapter) Type() string {
	if a.Mode == "discover" {
		return "mcp-discover-bound"
	}
	return a.Transport + "-" + a.Mode
}
func (a *BoundAdapter) Execute(ctx context.Context, step *executor.StepDefinition, res executor.TemplateResolver) (*executor.StepResult, error) {
	p, err := boundProfile(step.Config["integration"])
	if err != nil {
		return nil, err
	}
	if p.Transport != a.Transport {
		return nil, fmt.Errorf("bound profile transport mismatch")
	}
	factory := a.newRuntime
	if factory == nil {
		factory = New
	}
	r, err := factory(p)
	if err != nil {
		return nil, err
	}
	hash, _ := step.Config["profileHash"].(string)
	if hash != r.Hash {
		return nil, fmt.Errorf("bound profile hash mismatch")
	}
	if a.Mode == "inspect" {
		return &executor.StepResult{Output: map[string]interface{}{"profile": r.Profile, "profileHash": r.Hash}}, nil
	}
	name, _ := step.Config["operation"].(string)
	if a.Mode != "discover" {
		found := false
		for _, op := range p.Operations {
			if op.Name == name {
				found = true
				if op.Effect != a.Mode {
					return nil, fmt.Errorf("operation effect does not match authorized action")
				}
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("operation is not in bound profile")
		}
	}
	token, err := boundToken(p, step.Config, res)
	if err != nil {
		return nil, err
	}
	if a.Mode == "discover" {
		if a.Transport != "mcp" {
			return nil, fmt.Errorf("MCP discovery required")
		}
		tools, err := r.Discover(ctx, token)
		if err != nil {
			return nil, err
		}
		return &executor.StepResult{Output: map[string]interface{}{"tools": tools, "profileHash": r.Hash}}, nil
	}
	args, ok := step.Config["arguments"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("arguments object required")
	}
	// Resolve only operation input, never the trusted contract or access metadata.
	if res != nil {
		args = res.ResolveMap(args)
	}
	output, err := r.Call(ctx, name, hash, args, token)
	if err != nil {
		return nil, err
	}
	output["profileHash"] = r.Hash
	output["operation"] = name
	return &executor.StepResult{Output: output}, nil
}
func boundProfile(value interface{}) (*Profile, error) {
	raw, err := json.Marshal(value)
	if err != nil || len(raw) > MaxBytes {
		return nil, fmt.Errorf("invalid bound integration")
	}
	var envelope struct {
		Profile map[string]interface{} `json:"profile"`
		Access  *Credential            `json:"access,omitempty"`
	}
	// Strictly inspect the envelope; credentials never come from this configuration.
	var m map[string]interface{}
	if json.Unmarshal(raw, &m) != nil || m == nil {
		return nil, fmt.Errorf("host-owned integration configuration required")
	}
	for key := range m {
		if key != "profile" && key != "access" {
			return nil, fmt.Errorf("unknown integration configuration field")
		}
	}
	if err := nonSecretConfig(m); err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&envelope) != nil || envelope.Profile == nil {
		return nil, fmt.Errorf("invalid integration profile")
	}
	if _, exists := envelope.Profile["credential"]; exists {
		return nil, fmt.Errorf("use access reference metadata, not profile credential configuration")
	}
	if envelope.Access != nil {
		envelope.Profile["credential"] = envelope.Access
	}
	encoded, _ := json.Marshal(envelope.Profile)
	return decodeProfile(encoded)
}
func boundToken(p *Profile, cfg map[string]interface{}, res executor.TemplateResolver) (string, error) {
	if p.Credential == nil {
		return "", nil
	}
	var value interface{}
	if br, ok := res.(executor.BindingResolver); ok {
		value = br.GetBinding(AccessBinding)
	}
	// Cortex's trusted ToolInvoker can supply resolved credentials as transport
	// config. This key is absent from all model-visible input schemas.
	if value == nil {
		value = cfg[AccessBinding]
	}
	if s, ok := value.(string); ok {
		var object map[string]interface{}
		if json.Unmarshal([]byte(s), &object) == nil && object != nil {
			value = object
		} else if s != "" {
			return s, nil
		}
	}
	if object, ok := value.(map[string]interface{}); ok {
		if token, ok := object[p.Credential.Field].(string); ok && token != "" {
			return token, nil
		}
	}
	return "", fmt.Errorf("integration-access credential binding is missing or invalid")
}

func nonSecretConfig(v interface{}) error {
	switch value := v.(type) {
	case map[string]interface{}:
		for k, child := range value {
			normalized := strings.ToLower(strings.NewReplacer("_", "", "-", "").Replace(strings.TrimSpace(k)))
			if normalized == "token" || strings.HasSuffix(normalized, "apikey") || strings.HasSuffix(normalized, "password") || strings.HasSuffix(normalized, "secret") || strings.HasSuffix(normalized, "credential") || strings.HasSuffix(normalized, "credentialid") || strings.HasSuffix(normalized, "accesstoken") || strings.HasSuffix(normalized, "refreshtoken") {
				return fmt.Errorf("binding configuration contains secret-like field %q; keep credentials in opaque access references", k)
			}
			if err := nonSecretConfig(child); err != nil {
				return err
			}
		}
	case []interface{}:
		for _, child := range value {
			if err := nonSecretConfig(child); err != nil {
				return err
			}
		}
	}
	return nil
}
