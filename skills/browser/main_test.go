package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/axiom-studio/skills.sdk/executor"
	"gopkg.in/yaml.v3"
)

type testResolver struct {
	bindings map[string]interface{}
}

func (*testResolver) ResolveString(value string) string                              { return value }
func (*testResolver) ResolveMap(value map[string]interface{}) map[string]interface{} { return value }
func (*testResolver) EvaluateCondition(string) bool                                  { return false }
func (*testResolver) SetVariable(string, interface{})                                {}
func (*testResolver) GetStepOutput(string) interface{}                               { return nil }
func (*testResolver) SetStepOutput(string, interface{})                              {}
func (r *testResolver) GetBinding(name string) interface{}                           { return r.bindings[name] }
func (r *testResolver) GetBindings() map[string]interface{}                          { return r.bindings }

type fakePage struct {
	url, title, text string
	values           map[string]string
	closed           bool
}

type fakeEngine struct {
	mu                  sync.Mutex
	pages               map[string]*fakePage
	batchCalls          int
	mutationCalls       int
	closeCalls          int
	redirectURL         string
	blockWait           bool
	secret              string
	openCalls           int
	openFailures        []error
	disconnectSnapshots int
}

func newFakeEngine() *fakeEngine {
	return &fakeEngine{pages: map[string]*fakePage{}}
}

func (e *fakeEngine) page(s *browserSession) *fakePage {
	page := e.pages[s.id]
	if page == nil {
		page = &fakePage{values: map[string]string{}}
		e.pages[s.id] = page
	}
	return page
}

func (e *fakeEngine) Run(ctx context.Context, s *browserSession, args ...string) (map[string]interface{}, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	page := e.page(s)
	if len(args) == 0 {
		return nil, errors.New("missing command")
	}
	switch args[0] {
	case "--profile":
		if len(args) < 3 || args[2] != "open" {
			return nil, errors.New("bad profile command")
		}
		page.url = args[3]
		e.openCalls++
		page.title = "Fixture"
		if e.redirectURL != "" {
			page.url = e.redirectURL
		}
		return map[string]interface{}{"url": page.url, "title": page.title}, nil
	case "open":
		e.openCalls++
		if len(e.openFailures) > 0 {
			err := e.openFailures[0]
			e.openFailures = e.openFailures[1:]
			return nil, err
		}
		page.url = args[1]
		page.title = "Fixture"
		page.text = "New posts and comments"
		if e.redirectURL != "" {
			page.url = e.redirectURL
		}
		return map[string]interface{}{"url": page.url, "title": page.title}, nil
	case "set":
		return map[string]interface{}{"width": 800, "height": 600}, nil
	case "get":
		switch args[1] {
		case "url":
			return map[string]interface{}{"url": page.url}, nil
		case "title":
			return map[string]interface{}{"title": page.title}, nil
		case "text":
			text := page.text
			if e.secret != "" {
				text += " " + e.secret
			}
			return map[string]interface{}{"text": text}, nil
		}
	case "snapshot":
		if e.disconnectSnapshots > 0 {
			e.disconnectSnapshots--
			return nil, errors.New("browser engine command failed: CDP response channel closed")
		}
		passwordValue := page.values["e3"]
		if passwordValue == "" {
			passwordValue = e.secret
		}
		snapshot := `- textbox "Comment" [ref=e1]
- button "Submit comment" [ref=e2]
- textbox "Password" [ref=e3]`
		if passwordValue != "" {
			snapshot += ": " + passwordValue
		}
		return map[string]interface{}{
			"snapshot": snapshot,
			"refs": map[string]interface{}{
				"e1": map[string]interface{}{"role": "textbox", "name": "Comment", "value": page.values["e1"]},
				"e2": map[string]interface{}{"role": "button", "name": "Submit comment"},
				"e3": map[string]interface{}{"role": "textbox", "name": "Password", "value": passwordValue},
			},
		}, nil
	case "wait":
		if e.blockWait {
			e.mu.Unlock()
			<-ctx.Done()
			e.mu.Lock()
			return nil, ctx.Err()
		}
		return map[string]interface{}{"waited": true}, nil
	case "screenshot":
		if err := os.WriteFile(args[1], []byte("\x89PNG\r\n\x1a\nfixture"), 0600); err != nil {
			return nil, err
		}
		return map[string]interface{}{"path": args[1]}, nil
	case "close":
		page.closed = true
		e.closeCalls++
		return map[string]interface{}{"closed": true}, nil
	}
	return nil, fmt.Errorf("unsupported fake command: %v", args)
}

func TestOpenRecoversTimedOutStaleDaemonOnce(t *testing.T) {
	engine := newFakeEngine()
	engine.openFailures = []error{errors.New("browser engine command failed: Operation timed out. The page may still be loading")}
	service := testService(t, engine, time.Minute)

	result, err := execute(t, service, "browser-open", map[string]interface{}{
		"sessionId": "recover-open", "profileName": "agent-profile", "url": "https://example.com/r/vibecoding/new",
	}, nil)
	if err != nil || result["success"] != true {
		t.Fatalf("open=%#v error=%v", result, err)
	}
	if engine.openCalls != 2 || engine.closeCalls != 1 {
		t.Fatalf("open calls=%d close calls=%d", engine.openCalls, engine.closeCalls)
	}
	session, err := service.session("recover-open")
	if err != nil {
		t.Fatal(err)
	}
	if session.meta.ProfileName != "agent-profile" || session.meta.ClosedAt != nil {
		t.Fatalf("recovered metadata=%#v", session.meta)
	}
}

func (e *fakeEngine) RunBatch(_ context.Context, s *browserSession, commands [][]string) ([]map[string]interface{}, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.batchCalls++
	page := e.page(s)
	results := make([]map[string]interface{}, 0, len(commands))
	for _, command := range commands {
		switch command[0] {
		case "fill":
			e.mutationCalls++
			page.values[strings.TrimPrefix(command[1], "@")] = command[2]
			results = append(results, map[string]interface{}{"ok": true})
		case "type":
			e.mutationCalls++
			ref := strings.TrimPrefix(command[1], "@")
			page.values[ref] += command[2]
			results = append(results, map[string]interface{}{"ok": true})
		case "select":
			e.mutationCalls++
			page.values[strings.TrimPrefix(command[1], "@")] = command[2]
			results = append(results, map[string]interface{}{"ok": true})
		case "click":
			e.mutationCalls++
			if command[1] == "@e2" {
				page.text = "Comment posted: " + page.values["e1"]
			}
			results = append(results, map[string]interface{}{"ok": true})
		case "get":
			results = append(results, map[string]interface{}{"value": page.values[strings.TrimPrefix(command[2], "@")]})
		default:
			return nil, errors.New("unsupported mutation")
		}
	}
	return results, nil
}

func (e *fakeEngine) Close(ctx context.Context, s *browserSession) error {
	_, err := e.Run(ctx, s, "close")
	return err
}

func (*fakeEngine) Ready(context.Context) error { return nil }

func TestAgentBrowserEnvironmentSeparatesCommandAndSessionTimeouts(t *testing.T) {
	engine := &agentBrowserEngine{idleTimeout: 17 * time.Minute}
	session := &browserSession{runtimeDir: t.TempDir(), profileDir: filepath.Join(t.TempDir(), "profile")}
	values := map[string]string{}
	for _, entry := range engine.environment(session) {
		name, value, ok := strings.Cut(entry, "=")
		if ok {
			values[name] = value
		}
	}
	if got, want := values["AGENT_BROWSER_DEFAULT_TIMEOUT"], "30000"; got != want {
		t.Fatalf("command timeout=%q want %q", got, want)
	}
	if got, want := values["AGENT_BROWSER_IDLE_TIMEOUT"], "1020000"; got != want {
		t.Fatalf("session idle timeout=%q want %q", got, want)
	}
	if got := values["AGENT_BROWSER_PROFILE"]; got != session.profileDir {
		t.Fatalf("profile=%q want %q", got, session.profileDir)
	}
}

func TestSnapshotRecoversDeadDaemonWithPersistentProfile(t *testing.T) {
	engine := newFakeEngine()
	service := testService(t, engine, time.Minute)
	_, err := execute(t, service, "browser-open", map[string]interface{}{
		"sessionId": "recover", "profileName": "reddit-auth", "url": "https://example.com/r/vibecoding/new",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	engine.disconnectSnapshots = 1
	result, err := execute(t, service, "browser-snapshot", map[string]interface{}{"sessionId": "recover"}, nil)
	if err != nil || result["success"] != true {
		t.Fatalf("snapshot=%#v error=%v", result, err)
	}
	if engine.openCalls != 2 || engine.closeCalls != 1 {
		t.Fatalf("open calls=%d close calls=%d", engine.openCalls, engine.closeCalls)
	}
	session, err := service.session("recover")
	if err != nil {
		t.Fatal(err)
	}
	if session.meta.ProfileName != "reddit-auth" || session.meta.ViewportWidth != 1440 || session.meta.ViewportHeight != 900 {
		t.Fatalf("recovered metadata=%#v", session.meta)
	}
}

func TestShutdownPreservesDurableSessionForReplacementWorker(t *testing.T) {
	workspace := t.TempDir()
	firstEngine := newFakeEngine()
	first, err := newBrowserService(workspace, newDestinationPolicy("", true), time.Minute, firstEngine)
	if err != nil {
		t.Fatal(err)
	}
	_, err = execute(t, first, "browser-open", map[string]interface{}{
		"sessionId": "replacement", "profileName": "reddit-auth", "url": "https://example.com/r/vibecoding/new",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	first.Shutdown(context.Background())
	if firstEngine.closeCalls != 1 {
		t.Fatalf("shutdown engine closes = %d", firstEngine.closeCalls)
	}

	secondEngine := newFakeEngine()
	second, err := newBrowserService(workspace, newDestinationPolicy("", true), time.Minute, secondEngine)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { second.Shutdown(context.Background()) })
	session, err := second.session("replacement")
	if err != nil {
		t.Fatal(err)
	}
	if session.meta.ClosedAt != nil {
		t.Fatal("replacement worker loaded process shutdown as an explicit session close")
	}
	secondEngine.disconnectSnapshots = 1
	result, err := execute(t, second, "browser-snapshot", map[string]interface{}{"sessionId": "replacement"}, nil)
	if err != nil || result["success"] != true {
		t.Fatalf("replacement snapshot=%#v error=%v", result, err)
	}
	if secondEngine.openCalls != 1 {
		t.Fatalf("replacement recovery opens = %d", secondEngine.openCalls)
	}
}

func testService(t *testing.T, engine *fakeEngine, idle time.Duration) *browserService {
	t.Helper()
	service, err := newBrowserService(t.TempDir(), newDestinationPolicy("", true), idle, engine)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { service.Shutdown(context.Background()) })
	return service
}

func execute(t *testing.T, service *browserService, action string, config map[string]interface{}, r *testResolver) (map[string]interface{}, error) {
	t.Helper()
	if r == nil {
		r = &testResolver{}
	}
	result, err := (&actionExecutor{action: action, service: service}).Execute(context.Background(), &executor.StepDefinition{Config: config}, r)
	if err != nil {
		return nil, err
	}
	return result.Output, nil
}

func openFixture(t *testing.T, service *browserService, sessionID string) {
	t.Helper()
	if _, err := execute(t, service, "browser-open", map[string]interface{}{
		"sessionId": sessionID, "url": "https://example.com/r/vibecoding/new",
	}, nil); err != nil {
		t.Fatal(err)
	}
}

func findReference(t *testing.T, snapshot map[string]interface{}, name string) string {
	t.Helper()
	for _, raw := range snapshot["elements"].([]map[string]interface{}) {
		if raw["name"] == name {
			return raw["reference"].(string)
		}
	}
	t.Fatalf("reference named %q not found: %#v", name, snapshot)
	return ""
}

func TestNavigationReadAndStableReferences(t *testing.T) {
	engine := newFakeEngine()
	service := testService(t, engine, time.Minute)
	openFixture(t, service, "reader")
	read, err := execute(t, service, "browser-read", map[string]interface{}{"sessionId": "reader", "maxCharacters": 1000}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(read["text"].(string), "posts and comments") {
		t.Fatalf("unexpected read output: %#v", read)
	}
	snapshot, err := execute(t, service, "browser-snapshot", map[string]interface{}{"sessionId": "reader"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ref := findReference(t, snapshot, "Comment")
	if !strings.HasPrefix(ref, "s2:e") || !strings.Contains(snapshot["snapshot"].(string), "ref="+ref) {
		t.Fatalf("reference was not generation scoped: %#v", snapshot)
	}
}

func TestFormFillSubmitOnceAndVerify(t *testing.T) {
	engine := newFakeEngine()
	service := testService(t, engine, time.Minute)
	openFixture(t, service, "poster")
	snapshot, _ := execute(t, service, "browser-snapshot", map[string]interface{}{"sessionId": "poster"}, nil)
	commentRef := findReference(t, snapshot, "Comment")
	comment := "Exact approved fixture comment."
	fillConfig := map[string]interface{}{
		"sessionId": "poster", "target": commentRef, "value": comment, "intent": "Fill the exact approved comment",
		"writeAuthorized": true, "idempotencyKey": "fill-comment-0001",
	}
	if _, err := execute(t, service, "browser-fill", fillConfig, nil); err != nil {
		t.Fatal(err)
	}
	duplicate, err := execute(t, service, "browser-fill", fillConfig, nil)
	if err != nil {
		t.Fatal(err)
	}
	if duplicate["duplicate"] != true || engine.mutationCalls != 1 {
		t.Fatalf("fill was not deduplicated: %#v calls=%d", duplicate, engine.mutationCalls)
	}
	snapshot, _ = execute(t, service, "browser-snapshot", map[string]interface{}{"sessionId": "poster"}, nil)
	submitRef := findReference(t, snapshot, "Submit comment")
	clickConfig := map[string]interface{}{
		"sessionId": "poster", "target": submitRef, "intent": "Submit the exact approved comment once",
		"writeAuthorized": true, "idempotencyKey": "submit-comment-0001",
	}
	if _, err := execute(t, service, "browser-click", clickConfig, nil); err != nil {
		t.Fatal(err)
	}
	duplicate, err = execute(t, service, "browser-click", clickConfig, nil)
	if err != nil {
		t.Fatal(err)
	}
	if duplicate["duplicate"] != true || engine.mutationCalls != 2 {
		t.Fatalf("submit was not deduplicated: %#v calls=%d", duplicate, engine.mutationCalls)
	}
	read, _ := execute(t, service, "browser-read", map[string]interface{}{"sessionId": "poster"}, nil)
	if !strings.Contains(read["text"].(string), comment) {
		t.Fatalf("exact comment was not verified: %#v", read)
	}
}

func TestMutationReportsChallengeAsTypedHumanIntervention(t *testing.T) {
	engine := newFakeEngine()
	service := testService(t, engine, time.Minute)
	openFixture(t, service, "challenge")
	snapshot, err := execute(t, service, "browser-snapshot", map[string]interface{}{"sessionId": "challenge"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	engine.mu.Lock()
	engine.pages["challenge"].title = "Verify you are human - CAPTCHA"
	engine.mu.Unlock()
	output, err := execute(t, service, "browser-click", map[string]interface{}{
		"sessionId": "challenge", "target": findReference(t, snapshot, "Submit comment"),
		"intent": "Continue to the exact approved destination", "writeAuthorized": true,
		"idempotencyKey": "challenge-click-0001",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if output["requiresHuman"] != true || output["verificationRequired"] != false {
		t.Fatalf("challenge outcome = %#v", output)
	}
	values, ok := output["challenges"].([]string)
	if !ok || len(values) != 1 || values[0] != "captcha" {
		t.Fatalf("challenge types = %#v", output["challenges"])
	}
}

func TestStaleReferencesAndWriteAuthorization(t *testing.T) {
	service := testService(t, newFakeEngine(), time.Minute)
	openFixture(t, service, "stale")
	snapshot, _ := execute(t, service, "browser-snapshot", map[string]interface{}{"sessionId": "stale"}, nil)
	ref := findReference(t, snapshot, "Comment")
	config := map[string]interface{}{
		"sessionId": "stale", "target": ref, "value": "one", "intent": "Fill fixture",
		"writeAuthorized": true, "idempotencyKey": "stale-fill-0001",
	}
	if _, err := execute(t, service, "browser-fill", config, nil); err != nil {
		t.Fatal(err)
	}
	config["idempotencyKey"] = "stale-fill-0002"
	config["value"] = "two"
	if _, err := execute(t, service, "browser-fill", config, nil); err == nil || !strings.Contains(err.Error(), "stale element reference") {
		t.Fatalf("expected stale reference error, got %v", err)
	}
	config["writeAuthorized"] = false
	if _, err := execute(t, service, "browser-fill", config, nil); err == nil || !strings.Contains(err.Error(), "writeAuthorized") {
		t.Fatalf("expected authorization error, got %v", err)
	}
}

func TestSessionIsolationAndProfileTraversal(t *testing.T) {
	engine := newFakeEngine()
	service := testService(t, engine, time.Minute)
	openFixture(t, service, "alpha")
	openFixture(t, service, "beta")
	snapshot, _ := execute(t, service, "browser-snapshot", map[string]interface{}{"sessionId": "alpha"}, nil)
	ref := findReference(t, snapshot, "Comment")
	_, err := execute(t, service, "browser-fill", map[string]interface{}{
		"sessionId": "alpha", "target": ref, "value": "alpha-only", "intent": "Fill alpha",
		"writeAuthorized": true, "idempotencyKey": "alpha-fill-0001",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if engine.pages["beta"].values["e1"] != "" {
		t.Fatalf("session state leaked: %#v", engine.pages)
	}
	for _, bad := range []string{"../escape", "a/b", ".hidden", "two dots"} {
		_, err := execute(t, service, "browser-open", map[string]interface{}{
			"sessionId": "safe", "profileName": bad, "url": "https://example.com",
		}, nil)
		if err == nil {
			t.Fatalf("accepted unsafe profile %q", bad)
		}
	}
	if _, err := execute(t, service, "browser-open", map[string]interface{}{
		"sessionId": "../escape", "url": "https://example.com",
	}, nil); err == nil {
		t.Fatal("accepted unsafe session ID")
	}
}

func TestSSRFAndRedirectBlocking(t *testing.T) {
	policy := newDestinationPolicy("", false)
	for _, raw := range []string{
		"http://127.0.0.1/admin", "http://[::1]/", "http://169.254.169.254/latest/meta-data",
		"http://metadata.google.internal/", "file:///etc/passwd", "https://user:pass@example.com/",
	} {
		if _, err := policy.validateURL(context.Background(), raw); err == nil {
			t.Fatalf("accepted blocked URL %q", raw)
		}
	}
	if _, err := newDestinationPolicy("allowed.example", false).validateURL(context.Background(), "https://example.com"); err == nil {
		t.Fatal("hostname allowlist was not enforced")
	}
	engine := newFakeEngine()
	engine.redirectURL = "http://127.0.0.1/internal"
	service, err := newBrowserService(t.TempDir(), policy, time.Minute, engine)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { service.Shutdown(context.Background()) })
	_, err = execute(t, service, "browser-open", map[string]interface{}{"sessionId": "redirect", "url": "https://example.com"}, nil)
	if err == nil || !strings.Contains(err.Error(), "blocked destination") {
		t.Fatalf("redirect destination was not revalidated: %v", err)
	}
}

func TestSafeProxyBlocksPrivateDial(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("should not be reached"))
	}))
	defer target.Close()
	proxy, err := startSafeProxy(newDestinationPolicy("", false))
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close(context.Background())
	proxyURL, _ := url.Parse(proxy.URL())
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}
	resp, err := client.Get(target.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("proxy status = %d, want 403", resp.StatusCode)
	}
}

func TestSecretRedactionAndCredentialFill(t *testing.T) {
	const secret = "s3cr3t-value-never-log"
	engine := newFakeEngine()
	engine.secret = secret
	service := testService(t, engine, time.Minute)
	openFixture(t, service, "secret")
	r := &testResolver{bindings: map[string]interface{}{"username": "fixture-user", "password": secret}}
	snapshot, err := execute(t, service, "browser-snapshot", map[string]interface{}{"sessionId": "secret"}, r)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(snapshot)
	if strings.Contains(string(encoded), secret) {
		t.Fatalf("snapshot leaked secret: %s", encoded)
	}
	commentRef := findReference(t, snapshot, "Comment")
	if _, err := execute(t, service, "browser-fill", map[string]interface{}{
		"sessionId": "secret", "target": commentRef, "value": secret,
		"intent": "Unsafe literal credential", "writeAuthorized": true, "idempotencyKey": "secret-literal-0001",
	}, r); err == nil || !strings.Contains(err.Error(), "browser-fill-secret") {
		t.Fatalf("bound secret was accepted as a literal: %v", err)
	}
	passwordRef := findReference(t, snapshot, "Password")
	if _, err := execute(t, service, "browser-fill-secret", map[string]interface{}{
		"sessionId": "secret", "target": passwordRef, "credentialField": "password",
		"intent": "Fill the approved login credential", "writeAuthorized": true, "idempotencyKey": "secret-fill-0001",
	}, r); err != nil {
		t.Fatal(err)
	}
	if engine.pages["secret"].values["e3"] != secret {
		t.Fatal("bound credential was not delivered to the engine")
	}
	if _, err := execute(t, service, "browser-screenshot", map[string]interface{}{"sessionId": "secret"}, r); err == nil || !strings.Contains(err.Error(), "blocked") {
		t.Fatalf("secret-tainted screenshot was not blocked: %v", err)
	}
}

func TestManagedCredentialFieldsAreConsumedEphemerally(t *testing.T) {
	const secret = "managed-secret-never-persist"
	engine := newFakeEngine()
	service := testService(t, engine, time.Minute)
	openFixture(t, service, "managed-secret")
	snapshot, err := execute(t, service, "browser-snapshot", map[string]interface{}{"sessionId": "managed-secret"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	config := map[string]interface{}{
		"sessionId": "managed-secret", "target": findReference(t, snapshot, "Password"), "credentialField": "password",
		"intent": "Fill the approved managed credential", "writeAuthorized": true, "idempotencyKey": "managed-secret-fill-0001",
		"username": "fixture-user", "password": secret,
	}
	output, err := execute(t, service, "browser-fill-secret", config, nil)
	if err != nil {
		t.Fatal(err)
	}
	if engine.pages["managed-secret"].values["e3"] != secret {
		t.Fatal("managed credential was not delivered to the engine")
	}
	if _, exists := config["username"]; exists {
		t.Fatal("managed username remained in execution config")
	}
	if _, exists := config["password"]; exists {
		t.Fatal("managed password remained in execution config")
	}
	encoded, _ := json.Marshal(output)
	if strings.Contains(string(encoded), secret) {
		t.Fatalf("managed credential leaked in output: %s", encoded)
	}
	followUp, err := execute(t, service, "browser-snapshot", map[string]interface{}{"sessionId": "managed-secret"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ = json.Marshal(followUp)
	if strings.Contains(string(encoded), secret) {
		t.Fatalf("later snapshot leaked a previously filled credential: %s", encoded)
	}
	var passwordElement map[string]interface{}
	for _, element := range followUp["elements"].([]map[string]interface{}) {
		if element["name"] == "Password" {
			passwordElement = element
			break
		}
	}
	if passwordElement == nil || passwordElement["filled"] != true {
		t.Fatalf("filled credential occupancy was not preserved: %#v", passwordElement)
	}
	if _, exists := passwordElement["value"]; exists {
		t.Fatalf("filled credential value was exposed: %#v", passwordElement)
	}
}

func TestStableSnapshotTreatsMissingEditableValueAsEmpty(t *testing.T) {
	_, refs := stableSnapshot(1, `- textbox "Username" [ref=e1]`, map[string]interface{}{
		"e1": map[string]interface{}{"role": "textbox", "name": "Username"},
	})
	element := refs["s1:e1"]
	if element["filled"] != false || element["value"] != nil {
		t.Fatalf("missing editable value occupancy = %#v", element)
	}
}

func TestTimeoutAndIdleCleanup(t *testing.T) {
	engine := newFakeEngine()
	service := testService(t, engine, 50*time.Millisecond)
	openFixture(t, service, "idle")
	engine.blockWait = true
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := (&actionExecutor{action: "browser-wait", service: service}).Execute(ctx, &executor.StepDefinition{Config: map[string]interface{}{
		"sessionId": "idle", "condition": "duration", "milliseconds": 1000, "timeoutSeconds": 1,
	}}, &testResolver{})
	if !errors.Is(err, context.DeadlineExceeded) && (err == nil || !strings.Contains(err.Error(), "deadline exceeded")) {
		t.Fatalf("expected bounded timeout, got %v", err)
	}
	engine.blockWait = false
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		session, _ := service.session("idle")
		session.mu.Lock()
		closed := session.meta.ClosedAt != nil
		session.mu.Unlock()
		if closed {
			if engine.closeCalls == 0 {
				t.Fatal("idle close did not reach browser engine")
			}
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("idle session was not cleaned up")
}

func TestExplicitClosePreservesNamedProfile(t *testing.T) {
	engine := newFakeEngine()
	service := testService(t, engine, time.Minute)
	if _, err := execute(t, service, "browser-open", map[string]interface{}{
		"sessionId": "named", "profileName": "reddit-auth", "url": "https://example.com",
	}, nil); err != nil {
		t.Fatal(err)
	}
	output, err := execute(t, service, "browser-close", map[string]interface{}{"sessionId": "named"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if output["profilePreserved"] != true || engine.closeCalls != 1 {
		t.Fatalf("unexpected close receipt: %#v calls=%d", output, engine.closeCalls)
	}
	if _, err := os.Stat(filepath.Join(service.workspace, "profiles", "reddit-auth")); err != nil {
		t.Fatalf("persistent profile was removed: %v", err)
	}
}

func TestManifestAndSchemasDeclareAllActions(t *testing.T) {
	encoded, err := os.ReadFile("skill.yaml")
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	var manifest map[string]interface{}
	if err := yaml.Unmarshal(encoded, &manifest); err != nil {
		t.Fatalf("manifest is not valid YAML: %v", err)
	}
	for _, header := range []string{
		"apiVersion: openseal.dev/v1alpha1", "kind: SkillDefinition", "kind: oci",
		"package: axiomstudio/skill-browser:1.1.14", "version: 1.1.14", "durability: persistent",
		"mountPath: /var/lib/openseal-browser", "minimumCapacity: 1Gi", "retention: retain", "writableGroup: 1001",
	} {
		if !strings.Contains(text, header) {
			t.Fatalf("manifest missing %q", header)
		}
	}
	expected := []string{
		"browser-health", "browser-open", "browser-snapshot", "browser-read", "browser-click", "browser-commit", "browser-fill", "browser-fill-secret",
		"browser-type", "browser-select", "browser-wait", "browser-screenshot", "browser-session-status", "browser-close",
	}
	if len(actionSchemas) != len(expected) {
		t.Fatalf("schema count = %d, want %d", len(actionSchemas), len(expected))
	}
	for _, action := range expected {
		if actionSchemas[action] == nil || !strings.Contains(text, "        "+action+":") {
			t.Fatalf("missing action or schema %q", action)
		}
	}
	if strings.Contains(text, "javascript") || strings.Contains(text, "executablePath") || strings.Contains(text, "launchArgs") {
		t.Fatal("manifest exposes an unrestricted browser control")
	}
	if strings.Contains(text, "\n        executables:") {
		t.Fatal("managed Browser service incorrectly requires its private executables on the turn host")
	}
}

func TestAgentBrowserLocalFixture(t *testing.T) {
	if os.Getenv("BROWSER_E2E") != "1" {
		t.Skip("set BROWSER_E2E=1 to run the local Chromium fixture")
	}
	if _, err := exec.LookPath("agent-browser"); err != nil {
		t.Skip("agent-browser is unavailable")
	}
	chromium, err := exec.LookPath("chromium")
	if err != nil {
		t.Skip("chromium is unavailable")
	}
	const comment = "Exact approved local fixture comment."
	var submitted struct {
		sync.Mutex
		gets  int
		count int
		text  string
	}
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet:
			submitted.Lock()
			submitted.gets++
			submitted.Unlock()
			w.Header().Set("Content-Type", "text/html")
			_, _ = fmt.Fprint(w, `<main><h1>Thread</h1><form method="post"><label>Comment<textarea name="comment"></textarea></label><button type="submit">Submit comment</button></form></main>`)
		case r.Method == http.MethodPost:
			_ = r.ParseForm()
			submitted.Lock()
			submitted.count++
			submitted.text = r.Form.Get("comment")
			submitted.Unlock()
			w.Header().Set("Content-Type", "text/html")
			_, _ = fmt.Fprintf(w, `<main><p id="posted">%s</p></main>`, r.Form.Get("comment"))
		}
	}))
	defer fixture.Close()
	policy := newDestinationPolicy("", true)
	fixtureURL, _ := url.Parse(fixture.URL)
	if err := policy.addAllowedPorts(fixtureURL.Port()); err != nil {
		t.Fatal(err)
	}
	proxy, err := startSafeProxy(policy)
	if err != nil {
		t.Fatal(err)
	}
	engine := &agentBrowserEngine{binary: "agent-browser", executable: chromium, proxyURL: proxy.URL()}
	service, err := newBrowserService(t.TempDir(), policy, time.Minute, engine)
	if err != nil {
		t.Fatal(err)
	}
	service.proxy = proxy
	t.Cleanup(func() { service.Shutdown(context.Background()) })
	if _, err := execute(t, service, "browser-open", map[string]interface{}{
		"sessionId": "e2e", "profileName": "e2e-profile", "url": fixture.URL,
	}, nil); err != nil {
		t.Fatal(err)
	}
	snapshot, err := execute(t, service, "browser-snapshot", map[string]interface{}{"sessionId": "e2e"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot["elements"].([]map[string]interface{})) == 0 {
		submitted.Lock()
		gets := submitted.gets
		submitted.Unlock()
		t.Fatalf("real browser fixture rendered no elements (fixture GETs=%d): %#v", gets, snapshot)
	}
	var initialCommentElement map[string]interface{}
	for _, element := range snapshot["elements"].([]map[string]interface{}) {
		if element["name"] == "Comment" {
			initialCommentElement = element
			break
		}
	}
	if initialCommentElement == nil || initialCommentElement["filled"] != false {
		t.Fatalf("real browser did not report empty control occupancy: %#v", initialCommentElement)
	}
	commentRef := findReference(t, snapshot, "Comment")
	if _, err := execute(t, service, "browser-fill", map[string]interface{}{
		"sessionId": "e2e", "target": commentRef, "value": comment, "intent": "Fill exact fixture comment",
		"writeAuthorized": true, "idempotencyKey": "e2e-fill-0001",
	}, nil); err != nil {
		t.Fatal(err)
	}
	snapshot, err = execute(t, service, "browser-snapshot", map[string]interface{}{"sessionId": "e2e"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var commentElement map[string]interface{}
	for _, element := range snapshot["elements"].([]map[string]interface{}) {
		if element["name"] == "Comment" {
			commentElement = element
			break
		}
	}
	if commentElement == nil || commentElement["filled"] != true {
		t.Fatalf("real browser did not report filled control occupancy: %#v", commentElement)
	}
	submitRef := findReference(t, snapshot, "Submit comment")
	click := map[string]interface{}{
		"sessionId": "e2e", "target": submitRef, "intent": "Submit exact fixture comment once",
		"writeAuthorized": true, "idempotencyKey": "e2e-submit-0001",
	}
	if _, err := execute(t, service, "browser-click", click, nil); err != nil {
		t.Fatal(err)
	}
	if duplicate, err := execute(t, service, "browser-click", click, nil); err != nil || duplicate["duplicate"] != true {
		t.Fatalf("duplicate click = %#v, %v", duplicate, err)
	}
	if _, err := execute(t, service, "browser-wait", map[string]interface{}{
		"sessionId": "e2e", "condition": "text", "text": comment, "timeoutSeconds": 5,
	}, nil); err != nil {
		t.Fatal(err)
	}
	read, err := execute(t, service, "browser-read", map[string]interface{}{"sessionId": "e2e"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	submitted.Lock()
	defer submitted.Unlock()
	if submitted.count != 1 || submitted.text != comment || !strings.Contains(read["text"].(string), comment) {
		t.Fatalf("submission count=%d text=%q read=%#v", submitted.count, submitted.text, read)
	}
}
