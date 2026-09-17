package platformtests

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	kernel "github.com/axiom-studio/openseal/pkg/runtime"
	"github.com/axiom-studio/openseal/pkg/skill"
	pb "github.com/axiom-studio/skills.sdk/grpc/skillpb"
	"github.com/axiom-studio/skills/internal/integration"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type liveHarness struct {
	t       *testing.T
	client  pb.SkillServiceClient
	store   *kernel.SQLiteStore
	catalog *skill.Catalog
	scope   kernel.Scope
	kind    string
	seq     int
}

func startLiveService(t *testing.T, kind string) pb.SkillServiceClient {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "skill-"+kind)
	build := exec.Command("go", "build", "-mod=mod", "-o", binary, "./skills/"+kind)
	build.Dir = "../.."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build runtime: %v\n%s", err, out)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	cmd := exec.Command(binary)
	for _, v := range os.Environ() {
		if !strings.HasPrefix(v, "SKILL_PORT=") && !strings.HasPrefix(v, "INTEGRATION_PROFILE=") {
			cmd.Env = append(cmd.Env, v)
		}
	}
	cmd.Env = append(cmd.Env, fmt.Sprintf("SKILL_PORT=%d", port))
	var logs bytes.Buffer
	cmd.Stdout = &logs
	cmd.Stderr = &logs
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := grpc.DialContext(ctx, fmt.Sprintf("127.0.0.1:%d", port), grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithBlock())
	if err != nil {
		t.Fatal("runtime connection:", err)
	}
	t.Cleanup(func() { conn.Close() })
	client := pb.NewSkillServiceClient(conn)
	health, err := client.Health(ctx, &pb.HealthRequest{})
	if err != nil || health.SkillId != "skill-"+kind {
		t.Fatal("runtime health failed", health, err)
	}
	return client
}
func newLiveHarness(t *testing.T, kind string, client pb.SkillServiceClient) *liveHarness {
	t.Helper()
	store, err := kernel.NewSQLiteStore(filepath.Join(t.TempDir(), "live.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	h := &liveHarness{t: t, client: client, store: store, catalog: skill.NewCatalogWithStore(store), scope: kernel.Scope{Kind: "tenant", ID: "live-e2e-isolated"}, kind: kind}
	// Install the real checked-in manifest, not a synthetic action contract.
	data, err := os.ReadFile("../../skills/" + kind + "/skill.yaml")
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := skill.DecodeManifestYAML(data)
	if err != nil {
		t.Fatal(err)
	}
	if err = h.catalog.Register(context.Background(), &manifest.Definition); err != nil {
		t.Fatal(err)
	}
	if err = h.catalog.Register(context.Background(), kernel.SkillManagementSkill()); err != nil {
		t.Fatal(err)
	}
	for _, b := range []*skill.Binding{
		{ID: "compiler", SkillID: "skill-" + kind, SkillVersion: integration.RuntimeVersion, AllowedActions: []string{kind + "-compile"}, MaximumRisk: skill.RiskLevelRead},
		{ID: "management", SkillID: kernel.SkillManagementSkillID, SkillVersion: kernel.SkillManagementSkillVersion, AllowedActions: []string{kernel.SkillActionUpsertBinding}, MaximumRisk: skill.RiskLevelWrite},
	} {
		b.Scope = skill.ScopeReference(h.scope)
		b.DeploymentID = "e2e-agent"
		b.Revision = 1
		if err = h.catalog.Bind(context.Background(), b); err != nil {
			t.Fatal(err)
		}
	}
	_, err = kernel.NewPortfolioService(store).CreateAgentRun(context.Background(), kernel.CreateAgentRunRequest{Scope: h.scope, Kind: kernel.RunKindConversation, Owner: kernel.ObjectiveOwner{Type: kernel.OwnerTypeAgent, ID: "e2e-agent"}, AssignedAgentID: "e2e-agent", Goal: "Validate learned integration end to end", Source: kernel.RunSourceChat})
	if err != nil {
		t.Fatal(err)
	}
	return h
}
func (h *liveHarness) invoke(ctx context.Context, inv kernel.ToolInvocation) (map[string]interface{}, error) {
	cfg := map[string]interface{}{}
	for k, v := range inv.Arguments {
		cfg[k] = v
	}
	for k, v := range inv.BindingConfig {
		if _, ok := cfg[k]; ok {
			return nil, fmt.Errorf("host config collision")
		}
		cfg[k] = v
	}
	request := &pb.ExecuteRequest{NodeId: inv.ActionCallID, NodeType: inv.Name, Config: map[string][]byte{}, Bindings: map[string][]byte{}}
	for k, v := range cfg {
		raw, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}
		request.Config[k] = raw
	}
	for k, v := range inv.Credentials {
		raw, _ := json.Marshal(v)
		request.Bindings[k] = raw
	}
	response, err := h.client.Execute(ctx, request)
	if err != nil {
		return nil, err
	}
	if response.Error != nil {
		return nil, fmt.Errorf("%s", response.Error.Message)
	}
	result := map[string]interface{}{}
	for k, raw := range response.Output {
		var v interface{}
		if err = json.Unmarshal(raw, &v); err != nil {
			return nil, err
		}
		result[k] = v
	}
	return result, nil
}
func (h *liveHarness) execute(bindingID, skillID, version, action string, args map[string]interface{}) (map[string]interface{}, error) {
	h.t.Helper()
	h.seq++
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	run, err := h.store.ClaimNextAgentRun(ctx, kernel.AgentRunClaim{Scope: h.scope, WorkerID: "live-agent", Now: time.Now().UTC(), LeaseDuration: 2 * time.Minute, AgingInterval: time.Minute})
	if err != nil || run == nil {
		return nil, fmt.Errorf("claim run: %v", err)
	}
	binding, err := h.catalog.GetBinding(ctx, skill.ScopeReference(h.scope), "e2e-agent", bindingID)
	if err != nil || binding == nil {
		return nil, fmt.Errorf("missing binding %s: %v", bindingID, err)
	}
	validator, err := kernel.NewSkillBindingActionValidator(h.catalog)
	if err != nil {
		return nil, err
	}
	policy := kernel.ActionPolicyEvaluatorFunc(func(_ context.Context, in kernel.ActionPolicyInput) (kernel.ActionPolicyDecision, error) {
		if in.Bound.Definition.ID == kernel.SkillManagementSkillID {
			return kernel.ActionPolicyDecision{Disposition: kernel.ActionDispositionRequireApproval, EligibleApprovers: []kernel.ApprovalPrincipal{{Type: "user", ID: "e2e-operator"}}, ApprovalTTL: time.Minute, Reason: "Isolated test binding approval"}, nil
		}
		return kernel.ActionPolicyDecision{Disposition: kernel.ActionDispositionAllow, Reason: "Authorized public read test"}, nil
	})
	proposal, err := kernel.NewActionCoordinator(h.store, h.store, h.catalog, policy, validator).Propose(ctx, kernel.ProposeActionRequest{Scope: h.scope, RunID: run.ID, WorkerID: "live-agent", DeploymentID: "e2e-agent", SkillID: skillID, SkillVersion: version, Action: action, BindingID: binding.ID, BindingRevision: binding.Revision, Arguments: args, IdempotencyKey: fmt.Sprintf("live-%d", h.seq), Summary: "End-to-end example"})
	if err != nil {
		return nil, err
	}
	if skillID == kernel.SkillManagementSkillID && proposal.Approval == nil {
		return nil, fmt.Errorf("management mutation bypassed expected approval")
	}
	if proposal.Approval != nil {
		_, err = kernel.NewApprovalCoordinator(h.store, h.store, kernel.EligibleApprovalAuthorizer{}).Resolve(ctx, kernel.ResolveApprovalRequest{Scope: h.scope, ApprovalID: proposal.Approval.ID, ExpectedRevision: proposal.Approval.Revision, DecisionID: "approve-" + proposal.Call.ID, Decision: kernel.ApprovalDecisionApprove, Principal: kernel.ApprovalPrincipal{Type: "user", ID: "e2e-operator"}, Reason: "Approve isolated fixture binding"})
		if err != nil {
			return nil, err
		}
	}
	tool, err := kernel.NewToolActionDispatcher(kernel.ToolInvokerFunc(h.invoke))
	if err != nil {
		return nil, err
	}
	dispatch, err := kernel.NewSkillBindingActionDispatcher(h.store, h.catalog, tool)
	if err != nil {
		return nil, err
	}
	result, err := kernel.NewActionWorker(h.store, h.catalog, nil, dispatch).RunOnce(ctx, h.scope, "live-action-worker", 2*time.Minute)
	if err != nil {
		return nil, err
	}
	if result == nil || result.Call == nil {
		return nil, fmt.Errorf("no action executed")
	}
	if result.Call.Status != kernel.ActionCallStatusSucceeded {
		return nil, fmt.Errorf("action %s: %s (%s)", action, result.Call.Error, result.Call.Status)
	}
	return result.Call.Output, nil
}
func (h *liveHarness) compile(p *integration.Profile) map[string]interface{} {
	h.t.Helper()
	raw, _ := json.Marshal(p)
	var value map[string]interface{}
	_ = json.Unmarshal(raw, &value)
	out, err := h.execute("compiler", "skill-"+h.kind, integration.RuntimeVersion, h.kind+"-compile", map[string]interface{}{"profile": value})
	if err != nil {
		h.t.Fatal("compile", err)
	}
	return out
}
func (h *liveHarness) activate(bundle map[string]interface{}, revision int64) {
	h.t.Helper()
	plan, ok := bundle["bindingPlan"].(map[string]interface{})
	if !ok {
		h.t.Fatal("missing binding plan", bundle["bindingPlanError"])
	}
	args := plan["upsertArguments"].(map[string]interface{})
	args["expectedRevision"] = revision
	if _, err := h.execute("management", kernel.SkillManagementSkillID, kernel.SkillManagementSkillVersion, kernel.SkillActionUpsertBinding, args); err != nil {
		h.t.Fatal("activate", err)
	}
	// Reload the catalog from durable storage before resolving the learned action.
	h.catalog = skill.NewCatalogWithStore(h.store)
	bindingID := args["bindingId"].(string)
	saved, err := h.catalog.GetBinding(context.Background(), skill.ScopeReference(h.scope), "e2e-agent", bindingID)
	if err != nil || saved == nil || saved.Revision != revision+1 {
		h.t.Fatalf("persisted binding revision: got %#v, error %v", saved, err)
	}
	if len(saved.Lifecycle) == 0 || saved.Lifecycle[len(saved.Lifecycle)-1].Actor.ID != "e2e-operator" {
		h.t.Fatal("binding approval actor not persisted")
	}
}
func (h *liveHarness) call(p *integration.Profile, action, operation string, args map[string]interface{}) map[string]interface{} {
	h.t.Helper()
	input := map[string]interface{}{"profileHash": integration.Digest(p)}
	if operation != "" {
		input["operation"] = operation
		input["arguments"] = args
	}
	out, err := h.execute(p.ID, "skill-"+h.kind, integration.RuntimeVersion, action, input)
	if err != nil {
		h.t.Fatal("execute", err)
	}
	return out
}

func TestLiveExamplesE2E(t *testing.T) {
	if os.Getenv("INTEGRATION_LIVE_E2E") != "1" {
		t.Skip("set INTEGRATION_LIVE_E2E=1 to call public services")
	}
	api := startLiveService(t, "api")
	mcp := startLiveService(t, "mcp")
	t.Run("github_repository", func(t *testing.T) {
		p, err := integration.Load("testdata/github-public.json")
		if err != nil {
			t.Fatal(err)
		}
		h := newLiveHarness(t, "api", api)
		h.activate(h.compile(p), 0)
		out := h.call(p, "api-read", "repository-get", map[string]interface{}{"owner": "golang", "repo": "go"})
		data := out["data"].(map[string]interface{})
		if data["full_name"] != "golang/go" || out["status"] != float64(200) {
			t.Fatal("incorrect repository result")
		}
		t.Logf("HTTP %.0f: %s, branch %s", out["status"], data["full_name"], data["default_branch"])
	})
	t.Run("weather_query", func(t *testing.T) {
		p, err := integration.Load("testdata/weather-public.json")
		if err != nil {
			t.Fatal(err)
		}
		h := newLiveHarness(t, "api", api)
		h.activate(h.compile(p), 0)
		out := h.call(p, "api-read", "weather-current", map[string]interface{}{"latitude": 12.97, "longitude": 77.59})
		data := out["data"].(map[string]interface{})
		current := data["current"].(map[string]interface{})
		if out["status"] != float64(200) || current["time"] == "" {
			t.Fatal("invalid weather result")
		}
		t.Logf("HTTP %.0f: temperature %v at %v", out["status"], current["temperature_2m"], current["time"])
	})
	t.Run("cloudflare_mcp_discover_learn_call", func(t *testing.T) {
		p, err := integration.Load("testdata/cloudflare-docs.json")
		if err != nil {
			t.Fatal(err)
		}
		h := newLiveHarness(t, "mcp", mcp)
		h.activate(h.compile(p), 0)
		discovery := h.call(p, "mcp-discover-bound", "", nil)
		tools := discovery["tools"].([]interface{})
		var selected map[string]interface{}
		for _, raw := range tools {
			snapshot := raw.(map[string]interface{})
			tool := snapshot["tool"].(map[string]interface{})
			if tool["name"] == "search_cloudflare_documentation" {
				selected = snapshot
				break
			}
		}
		if selected == nil {
			t.Fatal("documented search tool not discovered")
		}
		tool := selected["tool"].(map[string]interface{})
		p.Operations = []integration.Operation{{Name: "documentation-search", Description: "Search Cloudflare documentation", Effect: "read", Tool: tool["name"].(string), ToolHash: selected["hash"].(string), InputSchema: tool["inputSchema"].(map[string]interface{}), OutputSchema: tool["outputSchema"].(map[string]interface{})}}
		h.activate(h.compile(p), 1)
		out := h.call(p, "mcp-read", "documentation-search", map[string]interface{}{"query": "Workers KV get put bindings"})
		data := out["data"].(map[string]interface{})
		structured := data["structuredContent"].(map[string]interface{})
		results := structured["results"].([]interface{})
		if len(results) == 0 {
			t.Fatal("no search results")
		}
		first := results[0].(map[string]interface{})
		if !strings.HasPrefix(first["url"].(string), "https://developers.cloudflare.com/") {
			t.Fatal("unexpected documentation URL")
		}
		t.Logf("Discovered %d tools; binding revision 2; %d verified results; first: %s", len(tools), len(results), first["url"])
	})
}
