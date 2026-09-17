package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/axiom-studio/skills.sdk/executor"
	"github.com/axiom-studio/skills.sdk/resolver"
	"gopkg.in/yaml.v3"
)

func schema(s string) map[string]interface{} {
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		panic(err)
	}
	return m
}
func fixture() *Profile {
	return &Profile{Version: 1, ID: "example-service", Transport: "api", Endpoint: "https://service.example", Sources: []string{"https://service.example/docs"}, Credential: &Credential{Binding: "service-token", Field: "token", Header: "X-Client-Key"}, FixedQuery: map[string]string{"account": "bound-account"}, Operations: []Operation{{Name: "get-record", Description: "Read one record", Effect: "read", Method: "GET", Path: "/v2/records/{id}", PathParams: map[string]string{"id": "recordId"}, QueryParams: map[string]string{"search": "search"}, InputSchema: schema(`{"type":"object","properties":{"recordId":{"type":"string"},"search":{"type":"string"}},"required":["recordId"],"additionalProperties":false}`)}}}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func fakeRuntime(t *testing.T, p *Profile, fn func(*http.Request) (*http.Response, error)) *Runtime {
	t.Helper()
	r, err := New(p)
	if err != nil {
		t.Fatal(err)
	}
	r.client = &http.Client{Transport: roundTrip(fn), CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return r
}
func response(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}
func TestAPIMappingAndCredentialIsolation(t *testing.T) {
	r := fakeRuntime(t, fixture(), func(req *http.Request) (*http.Response, error) {
		if req.URL.String() != "https://service.example/v2/records/a%20b?account=bound-account&search=a%26b%3D1" {
			t.Fatal(req.URL.String())
		}
		if req.Header.Get("X-Client-Key") != "secret-value" {
			t.Fatal("credential not injected")
		}
		return response(200, `{"note":"echo secret-value","access_token":"other-secret","value":7}`), nil
	})
	result, err := r.Call(context.Background(), "get-record", r.Hash, map[string]interface{}{"recordId": "a b", "search": "a&b=1"}, "secret-value")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(result)
	if strings.Contains(string(b), "secret-value") || strings.Contains(string(b), "other-secret") {
		t.Fatal("credential leaked")
	}
}
func TestInvalidCallsNeverReachNetwork(t *testing.T) {
	calls := 0
	r := fakeRuntime(t, fixture(), func(*http.Request) (*http.Response, error) { calls++; return response(200, `{}`), nil })
	tests := []struct {
		name, hash string
		args       map[string]interface{}
	}{
		{"get-record", "wrong", map[string]interface{}{"recordId": "1"}},
		{"delete-record", r.Hash, map[string]interface{}{"recordId": "1"}},
		{"get-record", r.Hash, map[string]interface{}{}},
		{"get-record", r.Hash, map[string]interface{}{"recordId": 5}},
		{"get-record", r.Hash, map[string]interface{}{"recordId": "1", "endpoint": "https://attacker.example"}},
		{"get-record", r.Hash, map[string]interface{}{"recordId": "../admin"}},
		{"get-record", r.Hash, map[string]interface{}{"recordId": "%2fadmin"}},
	}
	for _, tt := range tests {
		if _, err := r.Call(context.Background(), tt.name, tt.hash, tt.args, "token"); err == nil {
			t.Fatal("unsafe call accepted")
		}
	}
	if calls != 0 {
		t.Fatalf("%d unexpected network calls", calls)
	}
}
func TestWritesNeverRetryAndErrorsDoNotLeak(t *testing.T) {
	p := fixture()
	op := &p.Operations[0]
	op.Method = "POST"
	op.Effect = "write"
	op.BodyParam = "payload"
	op.InputSchema["properties"].(map[string]interface{})["payload"] = schema(`{"type":"object"}`)
	calls := 0
	r := fakeRuntime(t, p, func(req *http.Request) (*http.Response, error) {
		calls++
		b, _ := io.ReadAll(req.Body)
		if string(b) != `{"value":7}` {
			t.Fatal(string(b))
		}
		return response(503, `{"error":"credential-secret"}`), nil
	})
	_, err := r.Call(context.Background(), op.Name, r.Hash, map[string]interface{}{"recordId": "1", "payload": map[string]interface{}{"value": 7}}, "credential-secret")
	if err == nil || strings.Contains(err.Error(), "credential-secret") || calls != 1 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}
func TestTransportRejectsRedirectsPrivateAddressesAndProxies(t *testing.T) {
	for _, s := range []string{"127.0.0.1", "::1", "10.0.0.1", "169.254.169.254", "100.100.100.200", "::ffff:127.0.0.1", "224.0.0.1", "0.0.0.0", "198.18.0.1", "64:ff9b::7f00:1"} {
		if publicIP(netip.MustParseAddr(s)) {
			t.Fatal("unsafe IP allowed", s)
		}
	}
	if !publicIP(netip.MustParseAddr("1.1.1.1")) {
		t.Fatal("public IP rejected")
	}
	client := NewHTTPClient()
	transport := client.Transport.(*http.Transport)
	if transport.Proxy != nil {
		t.Fatal("environment proxy enabled")
	}
	conn, err := transport.DialContext(context.Background(), "tcp", "127.0.0.1:443")
	if err == nil {
		conn.Close()
		t.Fatal("private dial allowed")
	}
	if client.CheckRedirect(nil, nil) != http.ErrUseLastResponse {
		t.Fatal("redirect allowed")
	}
}
func TestCompileIsDeterministicAndPinsPolicy(t *testing.T) {
	p := fixture()
	a, err := Compile(p)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := Compile(p)
	if a["manifest"] != b["manifest"] {
		t.Fatal("non deterministic compile")
	}
	var doc map[string]interface{}
	if err := yaml.Unmarshal([]byte(a["manifest"].(string)), &doc); err != nil {
		t.Fatal(err)
	}
	def := doc["definition"].(map[string]interface{})
	act := def["actions"].(map[string]interface{})["get-record"].(map[string]interface{})
	if act["risk"] != "read" || act["idempotency"] != "none" {
		t.Fatal(act)
	}
	inputs := act["inputSchema"].(map[string]interface{})["properties"].(map[string]interface{})
	if inputs["profileHash"].(map[string]interface{})["const"] != a["profileHash"] {
		t.Fatal("hash not enforced in manifest")
	}
	p.Operations[0].Path = "/v3/records/{id}"
	c, _ := Compile(p)
	if c["profileHash"] == a["profileHash"] {
		t.Fatal("contract change did not invalidate hash")
	}
}
func TestUnsupportedProfilesFailClosed(t *testing.T) {
	tests := []func(*Profile){
		func(p *Profile) { p.Endpoint = "http://example.com" },
		func(p *Profile) { p.Endpoint = "https://token@example.com" },
		func(p *Profile) { p.Operations[0].Method = "DELETE" },
		func(p *Profile) { p.Operations[0].QueryParams["account"] = "search" },
		func(p *Profile) { p.Operations[0].InputSchema["$ref"] = "https://attacker.example/schema" },
		func(p *Profile) { p.Operations[0].InputSchema["unevaluatedProperties"] = false },
		func(p *Profile) { p.Operations[0].Path = "//evil.example" },
		func(p *Profile) { p.Operations[0].Path = "/v2/{other}" },
	}
	for i, change := range tests {
		p := fixture()
		change(p)
		if p.Validate() == nil {
			t.Fatalf("unsafe profile %d accepted", i)
		}
	}
	if _, err := decodeProfile([]byte(`{"version":1,"unexpected":"x"}`)); err == nil {
		t.Fatal("unknown fields accepted")
	}
}
func TestResponseBoundsAndSchemaMismatch(t *testing.T) {
	for _, body := range []string{strings.Repeat("x", MaxBytes+1), `not JSON`, `{"value":"wrong"}`} {
		p := fixture()
		p.Operations[0].OutputSchema = schema(`{"type":"object","properties":{"value":{"type":"integer"}},"required":["value"]}`)
		r := fakeRuntime(t, p, func(*http.Request) (*http.Response, error) { return response(200, body), nil })
		if _, err := r.Call(context.Background(), "get-record", r.Hash, map[string]interface{}{"recordId": "1"}, "token"); err == nil {
			t.Fatal("invalid response accepted")
		}
	}
}
func TestAdapterResolvesCredentialFromBinding(t *testing.T) {
	r := fakeRuntime(t, fixture(), func(req *http.Request) (*http.Response, error) {
		if req.Header.Get("X-Client-Key") != "from-binding" {
			t.Fatal("incorrect credential source")
		}
		return response(200, `{}`), nil
	})
	res := resolver.New(resolver.Config{Bindings: map[string]interface{}{"service-token": map[string]interface{}{"token": "from-binding"}}})
	a := &Adapter{Runtime: r, Name: "get-record", Operation: "get-record"}
	_, err := a.Execute(context.Background(), &executor.StepDefinition{Config: map[string]interface{}{"profileHash": r.Hash, "arguments": map[string]interface{}{"recordId": "1"}}}, res)
	if err != nil {
		t.Fatal(err)
	}
}

func mcpFixture() (*Profile, map[string]interface{}) {
	tool := map[string]interface{}{"name": "lookup", "description": "Read a value", "inputSchema": schema(`{"type":"object","properties":{"key":{"type":"string"}},"required":["key"],"additionalProperties":false}`), "outputSchema": schema(`{"type":"object","properties":{"value":{"type":"integer"}},"required":["value"]}`)}
	p := fixture()
	p.Transport = "mcp"
	p.Endpoint = "https://service.example/mcp"
	p.Operations = []Operation{{Name: "lookup-value", Description: "Lookup a value", Effect: "read", Tool: "lookup", ToolHash: Digest(tool), InputSchema: tool["inputSchema"].(map[string]interface{})}}
	return p, tool
}
func mcpHandler(t *testing.T, tool map[string]interface{}, drift, toolError bool, calls *int) func(*http.Request) (*http.Response, error) {
	return func(req *http.Request) (*http.Response, error) {
		if req.Method == "DELETE" {
			return response(204, ""), nil
		}
		var msg map[string]interface{}
		if err := json.NewDecoder(req.Body).Decode(&msg); err != nil {
			t.Fatal(err)
		}
		method := msg["method"].(string)
		var result interface{}
		if method != "initialize" && (req.Header.Get("Mcp-Session-Id") != "session-1" || req.Header.Get("MCP-Protocol-Version") != "2025-11-25") {
			t.Fatal("session/version not propagated")
		}
		switch method {
		case "initialize":
			result = map[string]interface{}{"protocolVersion": "2025-11-25", "capabilities": map[string]interface{}{"tools": map[string]interface{}{}}}
		case "notifications/initialized":
			return response(202, ""), nil
		case "tools/list":
			params := msg["params"].(map[string]interface{})
			if params["cursor"] == nil {
				result = map[string]interface{}{"tools": []interface{}{}, "nextCursor": "page-2"}
			} else {
				if params["cursor"] != "page-2" {
					t.Fatal("bad cursor")
				}
				if drift {
					tool["description"] = "changed"
				}
				result = map[string]interface{}{"tools": []interface{}{tool}}
			}
		case "tools/call":
			*calls++
			params := msg["params"].(map[string]interface{})
			if params["name"] != "lookup" {
				t.Fatal("wrong tool")
			}
			result = map[string]interface{}{"content": []interface{}{}, "structuredContent": map[string]interface{}{"value": 7}, "isError": toolError}
		default:
			t.Fatal(method)
		}
		b, _ := json.Marshal(map[string]interface{}{"jsonrpc": "2.0", "id": msg["id"], "result": result})
		resp := response(200, string(b))
		if method == "initialize" {
			resp.Header.Set("Mcp-Session-Id", "session-1")
		}
		return resp, nil
	}
}
func TestMCPNegotiationPaginationAndCall(t *testing.T) {
	p, tool := mcpFixture()
	calls := 0
	r := fakeRuntime(t, p, mcpHandler(t, tool, false, false, &calls))
	_, err := r.Call(context.Background(), "lookup-value", r.Hash, map[string]interface{}{"key": "a"}, "token")
	if err != nil || calls != 1 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}
func TestMCPDriftAndToolErrors(t *testing.T) {
	for _, drift := range []bool{false, true} {
		p, tool := mcpFixture()
		calls := 0
		r := fakeRuntime(t, p, mcpHandler(t, tool, drift, true, &calls))
		_, err := r.Call(context.Background(), "lookup-value", r.Hash, map[string]interface{}{"key": "a"}, "token")
		if err == nil {
			t.Fatal("expected failure")
		}
		if drift && calls != 0 {
			t.Fatal("drifted tool was invoked")
		}
	}
}
func TestMCPStreamingResponseDoesNotWaitForEOF(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress\",\"params\":{}}\n\n")
		fmt.Fprint(w, "data: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"ok\":true}}\n\n")
		w.(http.Flusher).Flush()
		<-req.Context().Done()
	}))
	defer server.Close()
	p, _ := mcpFixture()
	r, _ := New(p)
	// Only the test transport routes to loopback. Production construction has no bypass flag.
	r.client = &http.Client{Transport: roundTrip(func(req *http.Request) (*http.Response, error) {
		copy := req.Clone(req.Context())
		u := *req.URL
		copy.URL = &u
		copy.URL.Scheme = "http"
		copy.URL.Host = strings.TrimPrefix(server.URL, "http://")
		return http.DefaultTransport.RoundTrip(copy)
	})}
	s := &mcpSession{r: r, token: "token"}
	result, err := s.send(context.Background(), "tools/list", map[string]interface{}{}, false)
	if err != nil || result["ok"] != true {
		t.Fatal(result, err)
	}
}
func TestRPCRejectsWrongIDsAndServerRequests(t *testing.T) {
	for _, body := range []string{`{"jsonrpc":"2.0","id":2,"result":{}}`, `{"jsonrpc":"2.0","id":1,"method":"sampling/createMessage"}`, `{"jsonrpc":"2.0","id":1,"error":{"code":-32601,"message":"token-secret"}}`} {
		_, _, err := rpcResponse([]byte(body), 1)
		if err == nil || strings.Contains(err.Error(), "token-secret") {
			t.Fatal("invalid envelope accepted or error leaked")
		}
	}
}

func TestFormAndTextBodies(t *testing.T) {
	for _, tt := range []struct {
		encoding          string
		payload           interface{}
		want, contentType string
	}{
		{"form", map[string]interface{}{"title": "a & b", "enabled": true}, "enabled=true&title=a+%26+b", "application/x-www-form-urlencoded"},
		{"text", "measurement,host=a value=7", "measurement,host=a value=7", "text/plain"},
	} {
		p := fixture()
		op := &p.Operations[0]
		op.Method = "POST"
		op.Effect = "write"
		op.BodyParam = "payload"
		op.BodyEncoding = tt.encoding
		op.ResponseEncoding = "text"
		if tt.encoding == "form" {
			op.InputSchema["properties"].(map[string]interface{})["payload"] = schema(`{"type":"object"}`)
		} else {
			op.InputSchema["properties"].(map[string]interface{})["payload"] = schema(`{"type":"string"}`)
		}
		r := fakeRuntime(t, p, func(req *http.Request) (*http.Response, error) {
			b, _ := io.ReadAll(req.Body)
			if string(b) != tt.want || req.Header.Get("Content-Type") != tt.contentType {
				t.Fatal(string(b), req.Header)
			}
			return response(200, "accepted"), nil
		})
		out, err := r.Call(context.Background(), op.Name, r.Hash, map[string]interface{}{"recordId": "1", "payload": tt.payload}, "token")
		if err != nil || out["data"] != "accepted" {
			t.Fatal(out, err)
		}
	}
}
func TestCompilerDoesNotResolveSecretTemplates(t *testing.T) {
	p := fixture()
	p.Operations[0].Description = "{{bindings.service-token.token}}"
	b, _ := json.Marshal(p)
	var value map[string]interface{}
	_ = json.Unmarshal(b, &value)
	res := resolver.New(resolver.Config{Bindings: map[string]interface{}{"service-token": map[string]interface{}{"token": "never-expand-this"}}})
	a := &CompilerAdapter{Transport: "api"}
	result, err := a.Execute(context.Background(), &executor.StepDefinition{Config: map[string]interface{}{"profile": value}}, res)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(result.Output)
	if strings.Contains(string(encoded), "never-expand-this") {
		t.Fatal("compiler expanded secret binding")
	}
}
func TestRuntimeOwnsProfileSnapshot(t *testing.T) {
	p := fixture()
	r, err := New(p)
	if err != nil {
		t.Fatal(err)
	}
	p.Endpoint = "https://attacker.example"
	p.Operations[0].InputSchema["type"] = "string"
	if r.Profile.Endpoint != "https://service.example" || r.Profile.Operations[0].InputSchema["type"] != "object" {
		t.Fatal("runtime profile changed through authoring object")
	}
}

func TestDiscoveryRedactionPreservesSchemaProperties(t *testing.T) {
	tool := map[string]interface{}{"inputSchema": schema(`{"type":"object","properties":{"token":{"type":"string"}}}`), "description": "echo credential-value"}
	original := Digest(tool)
	safe := redactValue(tool, "credential-value", false).(map[string]interface{})
	if safe["description"] != "echo [REDACTED]" {
		t.Fatal("credential not redacted")
	}
	if _, err := compileSchema(safe["inputSchema"].(map[string]interface{})); err != nil {
		t.Fatal("schema corrupted by redaction", err)
	}
	if Digest(tool) != original {
		t.Fatal("redaction modified original tool contract")
	}
}
