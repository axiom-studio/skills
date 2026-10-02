package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/axiom-studio/skills.sdk/executor"
)

type githubWriteTransport func(*http.Request) (*http.Response, error)

func (f githubWriteTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestCommitPublishesAtomicFilesAndReplaysWithoutDuplicateCommit(t *testing.T) {
	parent, baseTree, tree, commit, blob := strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("c", 40), strings.Repeat("d", 40), strings.Repeat("e", 40)
	head := parent
	commits, pushes := 0, 0
	previous := githubClient
	defer func() { githubClient = previous }()
	githubClient = &http.Client{Transport: githubWriteTransport(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "Bearer SECRET" {
			t.Fatal("missing bound credential")
		}
		var payload map[string]interface{}
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&payload)
		}
		var output interface{}
		switch r.Method + " " + r.URL.Path {
		case "GET /repos/axiom-studio/cortex/git/ref/heads/main":
			output = map[string]interface{}{"object": map[string]interface{}{"sha": head}}
		case "GET /repos/axiom-studio/cortex/git/commits/" + parent:
			output = map[string]interface{}{"tree": map[string]interface{}{"sha": baseTree}}
		case "GET /repos/axiom-studio/cortex/git/commits/" + commit:
			output = map[string]interface{}{"tree": map[string]interface{}{"sha": tree}, "message": "Publish files", "parents": []map[string]interface{}{{"sha": parent}}}
		case "POST /repos/axiom-studio/cortex/git/blobs":
			if payload["content"] != "  exact content\n" {
				t.Fatalf("whitespace lost: %#v", payload)
			}
			output = map[string]interface{}{"sha": blob}
		case "POST /repos/axiom-studio/cortex/git/trees":
			entries := payload["tree"].([]interface{})
			if len(entries) != 2 || payload["base_tree"] != baseTree {
				t.Fatalf("tree=%#v", payload)
			}
			if entries[0].(map[string]interface{})["mode"] != "100755" || entries[1].(map[string]interface{})["sha"] != nil {
				t.Fatal("mode/deletion lost")
			}
			output = map[string]interface{}{"sha": tree}
		case "POST /repos/axiom-studio/cortex/git/commits":
			commits++
			output = map[string]interface{}{"sha": commit}
		case "PATCH /repos/axiom-studio/cortex/git/refs/heads/main":
			if payload["force"] != false || payload["sha"] != commit {
				t.Fatal("unsafe push")
			}
			pushes++
			head = commit
			output = map[string]interface{}{"object": map[string]interface{}{"sha": commit}}
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		encoded, _ := json.Marshal(output)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(encoded))), Header: make(http.Header)}, nil
	})}
	step := &executor.StepDefinition{Config: map[string]interface{}{"owner": "axiom-studio", "repository": "cortex", "branch": "main", "expectedHeadSha": parent, "message": "Publish files", "files": []interface{}{map[string]interface{}{"path": "script.sh", "content": "  exact content\n", "mode": "100755"}, map[string]interface{}{"path": "old.txt", "delete": true}}}}
	r := githubBindingResolver{bindings: map[string]interface{}{"token": "SECRET"}}
	for i := 0; i < 2; i++ {
		result, err := executeCommitCreate(context.Background(), step, r)
		if err != nil || result.Output["published"] != true || result.Output["replayed"] != (i == 1) {
			t.Fatalf("result=%#v err=%v", result, err)
		}
	}
	if commits != 1 || pushes != 1 {
		t.Fatalf("commits=%d pushes=%d", commits, pushes)
	}
	// A different operation must not be mistaken for the successful retry.
	step.Config["message"] = "Different change"
	if _, err := executeCommitCreate(context.Background(), step, r); !errors.Is(err, errGitHubConflict) {
		t.Fatalf("stale head=%v", err)
	}
}

func TestRepositoryWriteValidationBeforeNetwork(t *testing.T) {
	for _, branch := range []string{"main..oops", "a//b", "main.lock", ".hidden", "main/"} {
		if validWriteBranch(branch) {
			t.Fatalf("accepted branch %q", branch)
		}
	}
	for _, files := range []string{`[{"path":"a","content":"x"}] []`, `[{"path":"../oops","content":"x"}]`, `[{"path":"a","delete":true,"content":"x"}]`, `[{"path":"a","content":"x","encoding":"base64"}]`, `[{"path":"a"}]`, `[{"path":"a","content":"x"},{"path":"a","content":"y"}]`, `[{"path":"/etc/a","content":"x"}]`} {
		if _, err := decodeFileChanges(&executor.StepDefinition{Config: map[string]interface{}{"files": files}}); err == nil {
			t.Fatalf("accepted %s", files)
		}
	}
}

func TestBranchCreateNeverReplacesExistingDifferentHead(t *testing.T) {
	previous := githubClient
	defer func() { githubClient = previous }()
	githubClient = &http.Client{Transport: githubWriteTransport(func(r *http.Request) (*http.Response, error) {
		status, body := 422, `{}`
		if r.Method == http.MethodGet {
			status = 200
			body = `{"object":{"sha":"` + strings.Repeat("b", 40) + `"}}`
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}
	step := &executor.StepDefinition{Config: map[string]interface{}{"owner": "axiom-studio", "repository": "cortex", "branch": "feature/test", "sha": strings.Repeat("a", 40)}}
	if _, err := executeBranchCreate(context.Background(), step, githubBindingResolver{bindings: map[string]interface{}{"token": "SECRET"}}); !errors.Is(err, errGitHubConflict) {
		t.Fatal(err)
	}
}

func TestCommitPublishesRejectsConcurrentBranchUpdate(t *testing.T) {
	parent, baseTree, tree, commit := strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("c", 40), strings.Repeat("d", 40)
	previous := githubClient
	defer func() { githubClient = previous }()
	attemptedPush := false
	githubClient = &http.Client{Transport: githubWriteTransport(func(r *http.Request) (*http.Response, error) {
		status, body := 200, `{}`
		switch r.Method + " " + r.URL.Path {
		case "GET /repos/team/project/git/ref/heads/main":
			body = `{"object":{"sha":"` + parent + `"}}`
		case "GET /repos/team/project/git/commits/" + parent:
			body = `{"tree":{"sha":"` + baseTree + `"}}`
		case "POST /repos/team/project/git/blobs":
			body = `{"sha":"` + baseTree + `"}`
		case "POST /repos/team/project/git/trees":
			body = `{"sha":"` + tree + `"}`
		case "POST /repos/team/project/git/commits":
			body = `{"sha":"` + commit + `"}`
		case "PATCH /repos/team/project/git/refs/heads/main":
			var payload map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&payload)
			if payload["force"] != false {
				t.Fatal("concurrent change could be overwritten")
			}
			attemptedPush = true
			status = 409
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}
	step := &executor.StepDefinition{Config: map[string]interface{}{"owner": "team", "repository": "project", "branch": "main", "expectedHeadSha": parent, "message": "Update", "files": `[{"path":"a.txt","content":"new"}]`}}
	result, err := executeCommitCreate(context.Background(), step, githubBindingResolver{bindings: map[string]interface{}{"token": "SECRET"}})
	if !attemptedPush || !errors.Is(err, errGitHubConflict) || result != nil {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}
