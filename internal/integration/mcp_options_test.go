package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

func configuredMCP() *Profile {
	return &Profile{Version: 1, ID: "configured-mcp", Transport: "mcp", Endpoint: "https://example.com:8443/mcp", Sources: []string{"user configuration"}, Operations: []Operation{}, MCP: &MCPOptions{Transport: "streamable-http", TimeoutSeconds: 90}}
}
func TestMCPConfigurationValidation(t *testing.T) {
	p := configuredMCP()
	if e := p.Validate(); e != nil {
		t.Fatal(e)
	}
	p.MCP.Headers = []MCPSecretValue{{Name: "X-Workspace", Field: "h0"}}
	if p.Validate() == nil {
		t.Fatal("secret references without credential accepted")
	}
	p.Credential = &Credential{Binding: AccessBinding, Field: "token", Header: "Authorization"}
	if e := p.Validate(); e != nil {
		t.Fatal(e)
	}
	plan, e := BindingPlan(p)
	if e != nil {
		t.Fatal(e)
	}
	arguments := plan["upsertArguments"].(map[string]interface{})
	round, e := boundProfile(arguments["config"].(map[string]interface{})["integration"])
	if e != nil || Digest(round) != Digest(p) {
		t.Fatalf("configuration round trip: %v", e)
	}
	for _, h := range []string{"Host", "Mcp-Session-Id", "Content-Type", "Bad\r\nHeader"} {
		p.MCP.Headers[0].Name = h
		if p.Validate() == nil {
			t.Fatalf("accepted %s", h)
		}
	}
	p.MCP.Headers = nil
	p.MCP.TimeoutSeconds = 301
	if p.Validate() == nil {
		t.Fatal("unbounded timeout accepted")
	}
}
func TestMCPAuthenticationAndMultipleHeaders(t *testing.T) {
	p := configuredMCP()
	p.Credential = &Credential{Binding: AccessBinding, Field: "token", Header: "Authorization"}
	p.MCP.Headers = []MCPSecretValue{{Name: "X-Workspace", Field: "h0"}}
	p.MCP.Authentication = &MCPAuthentication{Kind: "basic"}
	r, e := New(p)
	if e != nil {
		t.Fatal(e)
	}
	h, e := r.mcpHeaders(t.Context(), `{"user":"kev","pass":"private","h0":"workspace"}`)
	if e != nil {
		t.Fatal(e)
	}
	if h.Get("X-Workspace") != "workspace" || h.Get("Authorization") != "Basic a2V2OnByaXZhdGU=" {
		t.Fatal("incorrect headers")
	}
	value := r.redactMCP(map[string]interface{}{"text": "private workspace"}, `{"pass":"private","h0":"workspace"}`, false).(map[string]interface{})
	if strings.Contains(value["text"].(string), "private") || strings.Contains(value["text"].(string), "workspace") {
		t.Fatal("bundle leaked")
	}
}
func TestMCPAnonymousDoesNotRequireCredential(t *testing.T) {
	p := configuredMCP()
	r, e := New(p)
	if e != nil {
		t.Fatal(e)
	}
	h, e := r.mcpHeaders(t.Context(), "")
	if e != nil || len(h) != 0 {
		t.Fatal("anonymous MCP requires a token")
	}
}
func TestMCPStdioIsolationAndLifecycle(t *testing.T) {
	t.Setenv("INTEGRATION_STDIO_ALLOWED", "1")
	t.Setenv("SENTINEL_PRIVATE_FIXTURE", "do-not-inherit")
	p := configuredMCP()
	p.Endpoint = ""
	p.MCP = &MCPOptions{Transport: "stdio", Command: "/bin/sh", Args: []string{"-c", `test -z "$SENTINEL_PRIVATE_FIXTURE" || exit 1; while read line; do case "$line" in *'"method":"initialize"'*) echo '{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-11-25","capabilities":{"tools":{}}}}';; *'"method":"tools/list"'*) echo '{"jsonrpc":"2.0","id":3,"result":{"tools":[]}}';; esac; done`}}
	r, e := New(p)
	if e != nil {
		t.Fatal(e)
	}
	tools, e := r.Discover(t.Context(), "")
	if e != nil || len(tools) != 0 {
		t.Fatalf("stdio discovery: %v", e)
	}
}

type optionsRoundTripper func(*http.Request) (*http.Response, error)

func (f optionsRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }
func TestMCPOAuthResourceAndRedaction(t *testing.T) {
	p := configuredMCP()
	p.Credential = &Credential{Binding: AccessBinding, Field: "token", Header: "Authorization"}
	p.MCP.Authentication = &MCPAuthentication{Kind: "oauth2", Grant: "client_credentials", TokenURL: "https://auth.example.com/token", ClientID: "client", Resource: p.Endpoint, Scopes: []string{"read"}}
	r, e := New(p)
	if e != nil {
		t.Fatal(e)
	}
	r.client = &http.Client{Transport: optionsRoundTripper(func(req *http.Request) (*http.Response, error) {
		if e := req.ParseForm(); e != nil {
			return nil, e
		}
		if req.Form.Get("resource") != p.Endpoint || req.Form.Get("scope") != "read" || req.Form.Get("client_secret") != "private" {
			return nil, fmt.Errorf("missing OAuth fields")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"access_token":"fresh-private","token_type":"Bearer"}`)), Header: http.Header{}}, nil
	})}
	h, e := r.mcpHeaders(context.Background(), `{"client":"private"}`)
	if e != nil || h.Get("Authorization") != "Bearer fresh-private" {
		t.Fatalf("OAuth: %v", e)
	}
	b, _ := json.Marshal(r.redactMCP("fresh-private", `{"client":"private"}`, false))
	if strings.Contains(string(b), "fresh-private") {
		t.Fatal("OAuth token leaked")
	}
}

func TestMCPSettingsBindingCannotOverrideManagedAccess(t *testing.T) {
	p := &Profile{Credential: &Credential{Binding: AccessBinding, Field: "access_token"}, MCP: &MCPOptions{ValuesBinding: "integration-settings"}}
	cfg := map[string]interface{}{AccessBinding: "managed-token", "integration-settings": map[string]interface{}{"settings": `{"h0":"custom-value"}`}}
	token, err := boundToken(p, cfg, nil)
	if err != nil || mcpSecrets(token)["access"] != "managed-token" || mcpSecrets(token)["h0"] != "custom-value" {
		t.Fatalf("binding merge failed: %v", err)
	}
	cfg["integration-settings"] = map[string]interface{}{"settings": `{"access":"attacker-token"}`}
	if _, err = boundToken(p, cfg, nil); err == nil {
		t.Fatal("secondary binding replaced managed token")
	}
}

func TestMCP2026Metadata(t *testing.T) {
	p := configuredMCP()
	p.MCP.ProtocolVersion = "2026-07-28"
	r, err := New(p)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	r.client = &http.Client{Transport: optionsRoundTripper(func(req *http.Request) (*http.Response, error) {
		calls++
		var body map[string]interface{}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["method"] != "tools/list" || req.Header.Get("Mcp-Method") != "tools/list" || req.Header.Get("MCP-Protocol-Version") != "2026-07-28" {
			t.Fatal("modern metadata missing or legacy initialize sent")
		}
		params := body["params"].(map[string]interface{})
		meta := params["_meta"].(map[string]interface{})
		if meta["io.modelcontextprotocol/protocolVersion"] != "2026-07-28" {
			t.Fatal("missing version")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"jsonrpc":"2.0","id":1,"result":{"tools":[]}}`))}, nil
	})}
	if _, err := r.Discover(t.Context(), ""); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("unexpected session or initialize requests")
	}
}

func TestMCPLegacySSETransport(t *testing.T) {
	p := configuredMCP()
	p.MCP.Transport = "sse"
	p.MCP.ProtocolVersion = "2024-11-05"
	r, err := New(p)
	if err != nil {
		t.Fatal(err)
	}
	reader, writer := io.Pipe()
	defer writer.Close()
	r.client = &http.Client{Transport: optionsRoundTripper(func(req *http.Request) (*http.Response, error) {
		if req.Method == "GET" {
			go func() { fmt.Fprint(writer, "event: endpoint\ndata: /messages?session=fixture\n\n") }()
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: reader}, nil
		}
		if req.URL.Path != "/messages" {
			t.Fatal("wrong legacy message endpoint")
		}
		var msg map[string]interface{}
		if err := json.NewDecoder(req.Body).Decode(&msg); err != nil {
			t.Fatal(err)
		}
		var result string
		switch msg["method"] {
		case "initialize":
			result = `{"protocolVersion":"2024-11-05","capabilities":{"tools":{}}}`
		case "tools/list":
			result = `{"tools":[]}`
		}
		if result != "" {
			go func() {
				fmt.Fprintf(writer, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":%.0f,\"result\":%s}\n\n", msg["id"], result)
			}()
		}
		return &http.Response{StatusCode: 202, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}
	if _, err = r.Discover(t.Context(), ""); err != nil {
		t.Fatal(err)
	}
}
