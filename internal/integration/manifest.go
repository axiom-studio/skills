package integration

import "fmt"

// BaseManifest is the installable shared runtime contract. Binding config is
// deliberately absent from model input schemas and supplied only by the host.
func BaseManifest(transport, instructions string) (map[string]interface{}, error) {
	if transport != "api" && transport != "mcp" {
		return nil, fmt.Errorf("unsupported transport")
	}
	hash := map[string]interface{}{"type": "string", "pattern": "^[0-9a-f]{64}$"}
	call := object(map[string]interface{}{"profileHash": hash, "operation": map[string]interface{}{"type": "string", "minLength": 1}, "arguments": map[string]interface{}{"type": "object"}}, "profileHash", "operation", "arguments")
	actions := map[string]interface{}{}
	compileName := transport + "-compile"
	compiled := action(compileName, "Compile a learned contract and its governed binding plan without activating it", "read", object(map[string]interface{}{"profile": map[string]interface{}{"type": "object"}}, "profile"), nil)
	compiled["sideEffect"] = "none"
	compiled["idempotency"] = "supported"
	actions[compileName] = compiled
	inspectName := transport + "-inspect"
	actions[inspectName] = action(inspectName, "Inspect this binding's learned contract", "read", object(map[string]interface{}{"profileHash": hash}, "profileHash"), nil)
	for _, effect := range []string{"read", "write"} {
		name := transport + "-" + effect
		entry := action(name, "Execute a pinned "+effect+" operation from this binding", effect, call, nil)
		entry["credentials"] = optionalAccess()
		actions[name] = entry
	}
	if transport == "mcp" {
		entry := action("mcp-discover-bound", "Discover tool contracts on this binding's MCP endpoint", "read", object(map[string]interface{}{"profileHash": hash}, "profileHash"), nil)
		entry["credentials"] = optionalAccess()
		actions["mcp-discover-bound"] = entry
	}
	reference := object(map[string]interface{}{"binding": map[string]interface{}{"type": "string"}, "field": map[string]interface{}{"type": "string"}, "header": map[string]interface{}{"type": "string"}, "prefix": map[string]interface{}{"type": "string"}}, "binding", "field", "header", "prefix")
	integration := object(map[string]interface{}{"profile": map[string]interface{}{"type": "object"}, "access": reference}, "profile")
	// Optional for compilation-only bindings; execution fails without configuration.
	config := object(map[string]interface{}{"integration": integration})
	definition := map[string]interface{}{
		"id": "skill-" + transport, "version": RuntimeVersion, "name": "Generic " + transport + " Skill", "description": "Learn service contracts and execute governed provider-neutral blocks",
		"category": "integration", "tags": []string{"generic", transport, "integration"}, "bindingConfigSchema": config, "actions": actions,
		"prompt":     map[string]interface{}{"instructions": instructions, "userInvocable": true},
		"transport":  map[string]interface{}{"kind": "tool", "endpoint": compileName},
		"installers": []interface{}{map[string]interface{}{"id": "oci", "kind": "oci", "package": "axiomstudio/skill-" + transport + ":" + RuntimeVersion}},
		"source":     map[string]interface{}{"format": "axiom.skill/v1", "reference": "skill-" + transport, "resolvedVersion": RuntimeVersion, "publisher": "Axiom Studio", "license": "MIT"},
	}
	return map[string]interface{}{"apiVersion": "openseal.dev/v1alpha1", "kind": "SkillDefinition", "definition": definition}, nil
}
func optionalAccess() []interface{} {
	return []interface{}{map[string]interface{}{"name": AccessBinding, "kind": AccessBinding, "optional": true}}
}
