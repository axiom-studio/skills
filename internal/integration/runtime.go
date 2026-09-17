package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/axiom-studio/skills.sdk/executor"
)

type Runtime struct {
	Profile *Profile
	Hash    string
	client  *http.Client
}

func New(p *Profile) (*Runtime, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	// Own an immutable snapshot, separate from the authoring caller's maps.
	data, _ := json.Marshal(p)
	var copy Profile
	_ = json.Unmarshal(data, &copy)
	return &Runtime{Profile: &copy, Hash: Digest(copy), client: NewHTTPClient()}, nil
}
func (r *Runtime) Call(ctx context.Context, name, hash string, args map[string]interface{}, token string) (map[string]interface{}, error) {
	if hash != r.Hash {
		return nil, fmt.Errorf("profile hash mismatch; regenerate block for the mounted profile")
	}
	var selected *Operation
	for _, op := range r.Profile.Operations {
		if op.Name == name {
			selected = &op
			break
		}
	}
	if selected == nil {
		return nil, fmt.Errorf("operation is not in the mounted profile")
	}
	raw, err := json.Marshal(args)
	if err != nil || len(raw) > MaxBytes {
		return nil, fmt.Errorf("invalid or oversized arguments")
	}
	if err := validateValue(selected.InputSchema, args); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if r.Profile.Transport == "api" {
		return r.callAPI(ctx, *selected, args, token)
	}
	return r.callMCP(ctx, *selected, args, token)
}

type Adapter struct {
	Runtime   *Runtime
	Name      string
	Operation string
}

func (a *Adapter) Type() string { return a.Name }
func (a *Adapter) Execute(ctx context.Context, step *executor.StepDefinition, res executor.TemplateResolver) (*executor.StepResult, error) {
	cfg := step.Config
	if res != nil {
		cfg = res.ResolveMap(cfg)
	}
	r := a.Runtime
	if a.Name == r.Profile.Transport+"-describe" {
		return &executor.StepResult{Output: map[string]interface{}{"profile": r.Profile, "profileHash": r.Hash}}, nil
	}
	token := ""
	if c := r.Profile.Credential; c != nil {
		br, ok := res.(executor.BindingResolver)
		if !ok {
			return nil, fmt.Errorf("credential binding resolver required")
		}
		binding, ok := br.GetBinding(c.Binding).(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("credential binding missing")
		}
		token, _ = binding[c.Field].(string)
		if token == "" {
			return nil, fmt.Errorf("credential binding field missing")
		}
	}
	hash, _ := cfg["profileHash"].(string)
	if hash != r.Hash {
		return nil, fmt.Errorf("profile hash mismatch")
	}
	if a.Name == "mcp-discover" {
		tools, err := r.Discover(ctx, token)
		if err != nil {
			return nil, err
		}
		return &executor.StepResult{Output: map[string]interface{}{"tools": tools, "profileHash": r.Hash}}, nil
	}
	name := a.Operation
	if name == "" {
		name, _ = cfg["operation"].(string)
	}
	args, ok := cfg["arguments"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("arguments object required")
	}
	out, err := r.Call(ctx, name, hash, args, token)
	if err != nil {
		return nil, err
	}
	out["profileHash"] = r.Hash
	out["operation"] = name
	return &executor.StepResult{Output: out}, nil
}
