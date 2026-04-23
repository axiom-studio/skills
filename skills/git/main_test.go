package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/axiom-studio/skills.sdk/executor"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// mockResolver is a no-op TemplateResolver that returns values unchanged
type mockResolver struct{}

func (m *mockResolver) ResolveString(s string) string                             { return s }
func (m *mockResolver) ResolveMap(input map[string]interface{}) map[string]interface{} { return input }
func (m *mockResolver) EvaluateCondition(c string) bool                            { return false }
func (m *mockResolver) SetVariable(name string, value interface{})                 {}
func (m *mockResolver) GetStepOutput(stepName string) interface{}                  { return nil }
func (m *mockResolver) SetStepOutput(stepName string, output interface{})          {}

func newStep(config map[string]interface{}) *executor.StepDefinition {
	return &executor.StepDefinition{
		Name:   "test-step",
		Type:   "test",
		Config: config,
	}
}

func newCtx() context.Context {
	return context.Background()
}

// --- Type() Tests ---

func TestHealthExecutor_Type(t *testing.T) {
	e := &HealthExecutor{}
	if e.Type() != "git-health" {
		t.Errorf("expected Type() 'git-health', got %s", e.Type())
	}
}

func TestCloneExecutor_Type(t *testing.T) {
	e := &CloneExecutor{}
	if e.Type() != "git-clone" {
		t.Errorf("expected Type() 'git-clone', got %s", e.Type())
	}
}

func TestPullExecutor_Type(t *testing.T) {
	e := &PullExecutor{}
	if e.Type() != "git-pull" {
		t.Errorf("expected Type() 'git-pull', got %s", e.Type())
	}
}

func TestCommitExecutor_Type(t *testing.T) {
	e := &CommitExecutor{}
	if e.Type() != "git-commit" {
		t.Errorf("expected Type() 'git-commit', got %s", e.Type())
	}
}

func TestPushExecutor_Type(t *testing.T) {
	e := &PushExecutor{}
	if e.Type() != "git-push" {
		t.Errorf("expected Type() 'git-push', got %s", e.Type())
	}
}

func TestBranchExecutor_Type(t *testing.T) {
	e := &BranchExecutor{}
	if e.Type() != "git-branch" {
		t.Errorf("expected Type() 'git-branch', got %s", e.Type())
	}
}

func TestStatusExecutor_Type(t *testing.T) {
	e := &StatusExecutor{}
	if e.Type() != "git-status" {
		t.Errorf("expected Type() 'git-status', got %s", e.Type())
	}
}

func TestBranchListExecutor_Type(t *testing.T) {
	e := &BranchListExecutor{}
	if e.Type() != "git-branch-list" {
		t.Errorf("expected Type() 'git-branch-list', got %s", e.Type())
	}
}

// --- HealthExecutor Tests ---

func TestHealthExecutor_Execute(t *testing.T) {
	e := &HealthExecutor{}
	result, err := e.Execute(newCtx(), newStep(nil), &mockResolver{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected result")
	}
	status, ok := result.Output["status"].(string)
	if !ok || status != "OK" {
		t.Errorf("expected status 'OK', got %v", result.Output["status"])
	}
}

// --- CloneExecutor Tests ---

func TestCloneExecutor_Execute_MissingUrl(t *testing.T) {
	e := &CloneExecutor{}
	_, err := e.Execute(newCtx(), newStep(map[string]interface{}{
		"path": "/tmp/test-clone",
	}), &mockResolver{})
	if err == nil {
		t.Error("expected error for missing url")
	}
}

func TestCloneExecutor_Execute_MissingPath(t *testing.T) {
	e := &CloneExecutor{}
	_, err := e.Execute(newCtx(), newStep(map[string]interface{}{
		"url": "https://github.com/example/repo.git",
	}), &mockResolver{})
	if err == nil {
		t.Error("expected error for missing path")
	}
}

func TestCloneExecutor_Execute_ReturnsStubMessage(t *testing.T) {
	e := &CloneExecutor{}
	// Clone is not yet implemented - should return a stub result
	result, err := e.Execute(newCtx(), newStep(map[string]interface{}{
		"url":  "https://github.com/example/repo.git",
		"path": "/tmp/test-clone-stub",
	}), &mockResolver{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected result")
	}
	success, ok := result.Output["success"].(bool)
	if !ok || success {
		t.Error("expected success=false for unimplemented clone")
	}
}

// --- PullExecutor Tests ---

func TestPullExecutor_Execute_MissingPath(t *testing.T) {
	e := &PullExecutor{}
	_, err := e.Execute(newCtx(), newStep(nil), &mockResolver{})
	if err == nil {
		t.Error("expected error for missing path")
	}
}

func TestPullExecutor_Execute_ReturnsStubMessage(t *testing.T) {
	e := &PullExecutor{}
	// Pull is not yet implemented - should return a stub result
	result, err := e.Execute(newCtx(), newStep(map[string]interface{}{
		"path":   "/tmp/test-pull-stub",
		"remote": "origin",
	}), &mockResolver{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected result")
	}
	success, ok := result.Output["success"].(bool)
	if !ok || success {
		t.Error("expected success=false for unimplemented pull")
	}
}

// --- CommitExecutor Tests ---

func TestCommitExecutor_Execute_MissingPath(t *testing.T) {
	e := &CommitExecutor{}
	_, err := e.Execute(newCtx(), newStep(map[string]interface{}{
		"message": "test commit",
	}), &mockResolver{})
	if err == nil {
		t.Error("expected error for missing path")
	}
}

func TestCommitExecutor_Execute_MissingMessage(t *testing.T) {
	e := &CommitExecutor{}
	_, err := e.Execute(newCtx(), newStep(map[string]interface{}{
		"path": "/tmp/test-repo",
	}), &mockResolver{})
	if err == nil {
		t.Error("expected error for missing message")
	}
}

func TestCommitExecutor_Execute_InvalidRepo(t *testing.T) {
	e := &CommitExecutor{}
	_, err := e.Execute(newCtx(), newStep(map[string]interface{}{
		"path":    "/tmp/nonexistent-repo",
		"message": "test commit",
	}), &mockResolver{})
	if err == nil {
		t.Error("expected error for invalid repo path")
	}
}

func TestCommitExecutor_Execute_NoChanges(t *testing.T) {
	// Create a temp git repo with an initial commit
	tmpDir := t.TempDir()
	repoPath := filepath.Join(tmpDir, "test-repo")

	repo, err := git.PlainInit(repoPath, false)
	if err != nil {
		t.Fatalf("failed to init repo: %v", err)
	}

	sig := &object.Signature{Name: "Test", Email: "test@example.com"}

	// Create a file and commit it
	filePath := filepath.Join(repoPath, "initial.txt")
	if err := os.WriteFile(filePath, []byte("initial content\n"), 0644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	w, err := repo.Worktree()
	if err != nil {
		t.Fatalf("failed to get worktree: %v", err)
	}
	_, err = w.Add("initial.txt")
	if err != nil {
		t.Fatalf("failed to add file: %v", err)
	}
	_, err = w.Commit("Initial commit", &git.CommitOptions{
		Author: sig,
	})
	if err != nil {
		t.Fatalf("failed to commit: %v", err)
	}

	// Now try to commit with no changes - should fail
	e := &CommitExecutor{}
	_, err = e.Execute(newCtx(), newStep(map[string]interface{}{
		"path":    repoPath,
		"message": "no changes commit",
	}), &mockResolver{})
	if err == nil {
		t.Error("expected error for no changes to commit")
	}
}

func TestCommitExecutor_Execute_Success(t *testing.T) {
	// Create a temp git repo with an initial commit
	tmpDir := t.TempDir()
	repoPath := filepath.Join(tmpDir, "test-repo")

	repo, err := git.PlainInit(repoPath, false)
	if err != nil {
		t.Fatalf("failed to init repo: %v", err)
	}

	sig := &object.Signature{Name: "Test", Email: "test@example.com"}

	// Create initial file + commit
	filePath := filepath.Join(repoPath, "initial.txt")
	if err := os.WriteFile(filePath, []byte("initial content\n"), 0644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	w, err := repo.Worktree()
	if err != nil {
		t.Fatalf("failed to get worktree: %v", err)
	}
	_, err = w.Add("initial.txt")
	if err != nil {
		t.Fatalf("failed to add file: %v", err)
	}
	_, err = w.Commit("Initial commit", &git.CommitOptions{
		Author: sig,
	})
	if err != nil {
		t.Fatalf("failed to commit: %v", err)
	}

	// Now modify a file and commit the change
	modifiedPath := filepath.Join(repoPath, "modified.txt")
	if err := os.WriteFile(modifiedPath, []byte("new content\n"), 0644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	e := &CommitExecutor{}
	result, err := e.Execute(newCtx(), newStep(map[string]interface{}{
		"path":    repoPath,
		"message": "Add modified file",
		"all":     true,
	}), &mockResolver{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected result")
	}

	success, ok := result.Output["success"].(bool)
	if !ok || !success {
		t.Error("expected success=true")
	}

	sha, ok := result.Output["sha"].(string)
	if !ok || sha == "" {
		t.Error("expected non-empty sha")
	}

	msg, ok := result.Output["message"].(string)
	if !ok || msg != "Add modified file" {
		t.Errorf("expected message 'Add modified file', got %v", result.Output["message"])
	}
}

// --- PushExecutor Tests ---

func TestPushExecutor_Execute_MissingPath(t *testing.T) {
	e := &PushExecutor{}
	_, err := e.Execute(newCtx(), newStep(nil), &mockResolver{})
	if err == nil {
		t.Error("expected error for missing path")
	}
}

func TestPushExecutor_Execute_ReturnsStubMessage(t *testing.T) {
	e := &PushExecutor{}
	result, err := e.Execute(newCtx(), newStep(map[string]interface{}{
		"path": "/tmp/test-push-stub",
	}), &mockResolver{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected result")
	}
	success, ok := result.Output["success"].(bool)
	if !ok || success {
		t.Error("expected success=false for unimplemented push")
	}
}

// --- BranchExecutor Tests ---

func TestBranchExecutor_Execute_MissingPath(t *testing.T) {
	e := &BranchExecutor{}
	_, err := e.Execute(newCtx(), newStep(nil), &mockResolver{})
	if err == nil {
		t.Error("expected error for missing path")
	}
}

func TestBranchExecutor_Execute_ReturnsStubMessage(t *testing.T) {
	e := &BranchExecutor{}
	result, err := e.Execute(newCtx(), newStep(map[string]interface{}{
		"path":      "/tmp/test-branch-stub",
		"operation": "list",
	}), &mockResolver{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected result")
	}
	success, ok := result.Output["success"].(bool)
	if !ok || success {
		t.Error("expected success=false for unimplemented branch operation")
	}
}

// --- StatusExecutor Tests ---

func TestStatusExecutor_Execute_MissingPath(t *testing.T) {
	e := &StatusExecutor{}
	_, err := e.Execute(newCtx(), newStep(nil), &mockResolver{})
	if err == nil {
		t.Error("expected error for missing path")
	}
}

func TestStatusExecutor_Execute_InvalidRepo(t *testing.T) {
	e := &StatusExecutor{}
	result, err := e.Execute(newCtx(), newStep(map[string]interface{}{
		"path": "/tmp/nonexistent-repo-for-status",
	}), &mockResolver{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected result")
	}
	valid, ok := result.Output["valid"].(bool)
	if !ok || valid {
		t.Error("expected valid=false for non-repo path")
	}
	success, ok := result.Output["success"].(bool)
	if !ok || success {
		t.Error("expected success=false for invalid repo")
	}
}

func TestStatusExecutor_Execute_CleanRepo(t *testing.T) {
	tmpDir := t.TempDir()
	repoPath := filepath.Join(tmpDir, "test-repo")

	_, err := git.PlainInit(repoPath, false)
	if err != nil {
		t.Fatalf("failed to init repo: %v", err)
	}

	e := &StatusExecutor{}
	result, err := e.Execute(newCtx(), newStep(map[string]interface{}{
		"path": repoPath,
	}), &mockResolver{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected result")
	}

	success, ok := result.Output["success"].(bool)
	if !ok || !success {
		t.Error("expected success=true")
	}

	clean, ok := result.Output["isClean"].(bool)
	if !ok || !clean {
		t.Error("expected isClean=true for empty repo")
	}

	summary, ok := result.Output["summary"].(string)
	if !ok || summary != "Working tree clean" {
		t.Errorf("expected summary 'Working tree clean', got %v", result.Output["summary"])
	}
}

func TestStatusExecutor_Execute_WithUntrackedFile(t *testing.T) {
	tmpDir := t.TempDir()
	repoPath := filepath.Join(tmpDir, "test-repo")

	_, err := git.PlainInit(repoPath, false)
	if err != nil {
		t.Fatalf("failed to init repo: %v", err)
	}

	// Create an untracked file
	untrackedPath := filepath.Join(repoPath, "untracked.txt")
	if err := os.WriteFile(untrackedPath, []byte("untracked content\n"), 0644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	e := &StatusExecutor{}
	result, err := e.Execute(newCtx(), newStep(map[string]interface{}{
		"path": repoPath,
	}), &mockResolver{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected result")
	}

	success, ok := result.Output["success"].(bool)
	if !ok || !success {
		t.Error("expected success=true")
	}

	clean, ok := result.Output["isClean"].(bool)
	if !ok || clean {
		t.Error("expected isClean=false with untracked file")
	}

	untracked, ok := result.Output["untracked"].([]string)
	if !ok || len(untracked) == 0 {
		t.Errorf("expected untracked files, got %v", result.Output["untracked"])
	}

	summary, ok := result.Output["summary"].(string)
	if !ok {
		t.Error("expected summary field")
	}
	if summary == "" || summary == "Working tree clean" {
		t.Errorf("expected non-clean summary, got %v", summary)
	}
}

// --- BranchListExecutor Tests ---

func TestBranchListExecutor_Execute_MissingPath(t *testing.T) {
	e := &BranchListExecutor{}
	_, err := e.Execute(newCtx(), newStep(nil), &mockResolver{})
	if err == nil {
		t.Error("expected error for missing path")
	}
}

func TestBranchListExecutor_Execute_InvalidRepo(t *testing.T) {
	e := &BranchListExecutor{}
	_, err := e.Execute(newCtx(), newStep(map[string]interface{}{
		"path": "/tmp/nonexistent-repo-for-branches",
	}), &mockResolver{})
	if err == nil {
		t.Error("expected error for non-repo path")
	}
}

func TestBranchListExecutor_Execute_NewRepo(t *testing.T) {
	tmpDir := t.TempDir()
	repoPath := filepath.Join(tmpDir, "test-repo")

	_, err := git.PlainInit(repoPath, false)
	if err != nil {
		t.Fatalf("failed to init repo: %v", err)
	}

	e := &BranchListExecutor{}
	result, err := e.Execute(newCtx(), newStep(map[string]interface{}{
		"path": repoPath,
	}), &mockResolver{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected result")
	}

	success, ok := result.Output["success"].(bool)
	if !ok || !success {
		t.Error("expected success=true")
	}

	branches, ok := result.Output["branches"].([]string)
	if !ok {
		t.Errorf("expected branches to be []string, got %T", result.Output["branches"])
	}

	count, ok := result.Output["count"].(int)
	if !ok {
		t.Errorf("expected count to be int, got %T", result.Output["count"])
	} else if count != len(branches) {
		t.Errorf("count %d != len(branches) %d", count, len(branches))
	}
}

func TestBranchListExecutor_Execute_WithBranches(t *testing.T) {
	tmpDir := t.TempDir()
	repoPath := filepath.Join(tmpDir, "test-repo")

	repo, err := git.PlainInit(repoPath, false)
	if err != nil {
		t.Fatalf("failed to init repo: %v", err)
	}

	sig := &object.Signature{Name: "Test", Email: "test@example.com"}

	// Create initial commit (requires at least one file)
	filePath := filepath.Join(repoPath, "initial.txt")
	if err := os.WriteFile(filePath, []byte("initial content\n"), 0644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	w, err := repo.Worktree()
	if err != nil {
		t.Fatalf("failed to get worktree: %v", err)
	}
	_, err = w.Add("initial.txt")
	if err != nil {
		t.Fatalf("failed to add file: %v", err)
	}
	_, err = w.Commit("Initial commit", &git.CommitOptions{
		Author: sig,
	})
	if err != nil {
		t.Fatalf("failed to commit: %v", err)
	}

	// Create additional branches
	err = w.Checkout(&git.CheckoutOptions{
		Branch: "refs/heads/feature-a",
		Create: true,
	})
	if err != nil {
		t.Fatalf("failed to create feature-a branch: %v", err)
	}

	err = w.Checkout(&git.CheckoutOptions{
		Branch: "refs/heads/feature-b",
		Create: true,
	})
	if err != nil {
		t.Fatalf("failed to create feature-b branch: %v", err)
	}

	// Go back to master/main
	headRef, err := repo.Head()
	if err != nil {
		t.Fatalf("failed to get head: %v", err)
	}
	err = w.Checkout(&git.CheckoutOptions{
		Branch: headRef.Name(),
	})
	if err != nil {
		t.Fatalf("failed to checkout original branch: %v", err)
	}

	e := &BranchListExecutor{}
	result, err := e.Execute(newCtx(), newStep(map[string]interface{}{
		"path": repoPath,
		"all":  true,
	}), &mockResolver{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected result")
	}

	success, ok := result.Output["success"].(bool)
	if !ok || !success {
		t.Error("expected success=true")
	}

	branches, ok := result.Output["branches"].([]string)
	if !ok {
		t.Errorf("expected branches to be []string, got %T", result.Output["branches"])
	}

	count, ok := result.Output["count"].(int)
	if !ok {
		t.Errorf("expected count to be int, got %T", result.Output["count"])
	} else if count != len(branches) {
		t.Errorf("count %d != len(branches) %d", count, len(branches))
	}

	// Should have at least 3 branches (master/main + feature-a + feature-b)
	expectedMin := 3
	if len(branches) < expectedMin {
		t.Errorf("expected at least %d branches, got %d: %v", expectedMin, len(branches), branches)
	}

	// Verify our branches are present
	branchMap := make(map[string]bool)
	for _, b := range branches {
		branchMap[b] = true
	}
	for _, expected := range []string{"feature-a", "feature-b"} {
		if !branchMap[expected] {
			t.Errorf("expected branch %q not found in %v", expected, branches)
		}
	}
}

// --- Schema Tests ---

func TestSchemasNotNil(t *testing.T) {
	schemas := []struct {
		name  string
		value interface{}
	}{
		{"HealthSchema", HealthSchema},
		{"CloneSchema", CloneSchema},
		{"PullSchema", PullSchema},
		{"CommitSchema", CommitSchema},
		{"PushSchema", PushSchema},
		{"BranchSchema", BranchSchema},
		{"StatusSchema", StatusSchema},
		{"BranchListSchema", BranchListSchema},
	}

	for _, s := range schemas {
		if s.value == nil {
			t.Errorf("%s should not be nil", s.name)
		}
	}
}
