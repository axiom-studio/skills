package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/axiom-studio/skills.sdk/executor"
	"github.com/axiom-studio/skills.sdk/resolver"
)

var branchCreateSchema = repositoryFields(resolver.NewSchemaBuilder("github-branch-create").
	WithName("Create GitHub branch").WithCategory("action").WithIcon(iconGitHub).
	WithDescription("Create a branch from an exact inspected commit; never replace an existing branch")).
	AddSection("Branch").AddExpressionField("branch", "New branch", resolver.WithRequired()).
	AddExpressionField("sha", "Source commit SHA", resolver.WithRequired()).EndSection().Build()

var commitCreateSchema = repositoryFields(resolver.NewSchemaBuilder("github-commit-create").
	WithName("Commit and publish GitHub files").WithCategory("action").WithIcon(iconGitHub).
	WithDescription("Commit file additions, updates and deletions together and publish to an explicit branch without force-pushing")).
	AddSection("Commit").AddExpressionField("branch", "Destination branch", resolver.WithRequired()).
	AddExpressionField("expectedHeadSha", "Inspected branch head SHA", resolver.WithRequired()).
	AddTextareaField("message", "Commit message", resolver.WithRows(4), resolver.WithRequired()).
	AddTextareaField("files", "File changes as JSON array", resolver.WithRows(10), resolver.WithRequired()).EndSection().Build()

func validWriteBranch(branch string) bool {
	if !branchPattern.MatchString(branch) || strings.Contains(branch, "..") || strings.Contains(branch, "//") || strings.HasSuffix(branch, "/") {
		return false
	}
	for _, part := range strings.Split(branch, "/") {
		if strings.HasPrefix(part, ".") || strings.HasSuffix(part, ".lock") || strings.HasSuffix(part, ".") {
			return false
		}
	}
	return true
}

func executeBranchCreate(ctx context.Context, step *executor.StepDefinition, r executor.TemplateResolver) (*executor.StepResult, error) {
	owner, repo, token, err := repositoryConfig(step, r)
	if err != nil {
		return nil, err
	}
	branch, sha := configString(step, "branch"), configString(step, "sha")
	if !validWriteBranch(branch) || !commitPattern.MatchString(sha) {
		return nil, errors.New("valid branch and exact source commit SHA are required")
	}
	var ref map[string]interface{}
	endpoint := repositoryEndpoint(owner, repo) + "/git/refs"
	err = githubRequest(ctx, token, http.MethodPost, endpoint, map[string]interface{}{"ref": "refs/heads/" + branch, "sha": sha}, &ref)
	replayed := false
	if errors.Is(err, errGitHubConflict) {
		if lookupErr := githubRequest(ctx, token, http.MethodGet, repositoryEndpoint(owner, repo)+"/git/ref/heads/"+escapePath(branch), nil, &ref); lookupErr != nil || !strings.EqualFold(nestedString(ref, "object", "sha"), sha) {
			return nil, errGitHubConflict
		}
		replayed, err = true, nil
	}
	if err != nil {
		return nil, err
	}
	return &executor.StepResult{Output: map[string]interface{}{"branch": branch, "sha": nestedString(ref, "object", "sha"), "replayed": replayed}}, nil
}

type repositoryFileChange struct {
	Path     string  `json:"path"`
	Content  *string `json:"content,omitempty"`
	Encoding string  `json:"encoding,omitempty"`
	Mode     string  `json:"mode,omitempty"`
	Delete   bool    `json:"delete,omitempty"`
}

func decodeFileChanges(step *executor.StepDefinition) ([]repositoryFileChange, error) {
	// Accept both native structured arguments and the workflow editor's JSON field.
	var raw []byte
	if value, ok := step.Config["files"].(string); ok {
		raw = []byte(value)
	} else {
		var err error
		raw, err = json.Marshal(step.Config["files"])
		if err != nil {
			return nil, err
		}
	}
	if len(raw) > 2<<20 {
		return nil, errors.New("file changes exceed the 2 MiB request limit")
	}
	var files []repositoryFileChange
	d := json.NewDecoder(strings.NewReader(string(raw)))
	d.DisallowUnknownFields()
	if err := d.Decode(&files); err != nil || len(files) < 1 || len(files) > 100 {
		return nil, errors.New("files must contain between 1 and 100 valid file changes")
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return nil, errors.New("file changes must contain exactly one JSON array")
	}
	seen := map[string]bool{}
	for i := range files {
		f := &files[i]
		if f.Path == "" || len(f.Path) > 1024 || strings.HasPrefix(f.Path, "/") || strings.HasSuffix(f.Path, "/") || strings.ContainsAny(f.Path, "\\\x00\r\n") || strings.Contains(f.Path, "//") || unsafePath(f.Path) || seen[f.Path] {
			return nil, errors.New("file paths must be safe, relative and unique")
		}
		seen[f.Path] = true
		if f.Delete {
			if f.Content != nil || f.Encoding != "" || f.Mode != "" {
				return nil, errors.New("deleted files cannot include content, encoding or mode")
			}
			continue
		}
		if f.Content == nil {
			return nil, errors.New("non-deleted files require content, including empty files")
		}
		if f.Mode == "" {
			f.Mode = "100644"
		}
		if f.Mode != "100644" && f.Mode != "100755" {
			return nil, errors.New("file mode must be 100644 or 100755")
		}
		if f.Encoding == "" {
			f.Encoding = "utf-8"
		}
		if f.Encoding == "base64" {
			if _, err := base64.StdEncoding.DecodeString(*f.Content); err != nil {
				return nil, errors.New("file content is invalid base64")
			}
		} else if f.Encoding != "utf-8" || !utf8.ValidString(*f.Content) {
			return nil, errors.New("file encoding must be utf-8 or base64")
		}
	}
	return files, nil
}

func executeCommitCreate(ctx context.Context, step *executor.StepDefinition, r executor.TemplateResolver) (*executor.StepResult, error) {
	owner, repo, token, err := repositoryConfig(step, r)
	if err != nil {
		return nil, err
	}
	branch, expected, message := configString(step, "branch"), configString(step, "expectedHeadSha"), configString(step, "message")
	if !validWriteBranch(branch) || !commitPattern.MatchString(expected) || message == "" || len(message) > 65536 {
		return nil, errors.New("valid branch, exact inspected head SHA and bounded commit message are required")
	}
	files, err := decodeFileChanges(step)
	if err != nil {
		return nil, err
	}
	base := repositoryEndpoint(owner, repo)
	var ref, parent map[string]interface{}
	if err = githubRequest(ctx, token, http.MethodGet, base+"/git/ref/heads/"+escapePath(branch), nil, &ref); err != nil {
		return nil, err
	}
	head := nestedString(ref, "object", "sha")
	if !commitPattern.MatchString(head) {
		return nil, errors.New("GitHub returned an invalid branch head")
	}
	if err = githubRequest(ctx, token, http.MethodGet, base+"/git/commits/"+expected, nil, &parent); err != nil {
		return nil, err
	}
	baseTree := nestedString(parent, "tree", "sha")
	if !commitPattern.MatchString(baseTree) {
		return nil, errors.New("GitHub returned an invalid parent tree")
	}
	// A replay is accepted only for the same parent, message and complete tree.
	var previous map[string]interface{}
	if !strings.EqualFold(head, expected) {
		if err = githubRequest(ctx, token, http.MethodGet, base+"/git/commits/"+head, nil, &previous); err != nil {
			return nil, err
		}
		parents, _ := previous["parents"].([]interface{})
		if len(parents) != 1 || previous["message"] != message {
			return nil, errGitHubConflict
		}
		p, _ := parents[0].(map[string]interface{})
		if p["sha"] != expected {
			return nil, errGitHubConflict
		}
	}
	entries := make([]map[string]interface{}, 0, len(files))
	for _, f := range files {
		entry := map[string]interface{}{"path": f.Path, "mode": "100644", "type": "blob", "sha": nil}
		if !f.Delete {
			var blob map[string]interface{}
			if err = githubRequest(ctx, token, http.MethodPost, base+"/git/blobs", map[string]interface{}{"content": *f.Content, "encoding": f.Encoding}, &blob); err != nil {
				return nil, err
			}
			sha, _ := blob["sha"].(string)
			if !commitPattern.MatchString(sha) {
				return nil, errors.New("GitHub returned an invalid blob SHA")
			}
			entry["sha"], entry["mode"] = sha, f.Mode
		}
		entries = append(entries, entry)
	}
	var tree map[string]interface{}
	if err = githubRequest(ctx, token, http.MethodPost, base+"/git/trees", map[string]interface{}{"base_tree": baseTree, "tree": entries}, &tree); err != nil {
		return nil, err
	}
	treeSHA, _ := tree["sha"].(string)
	if !commitPattern.MatchString(treeSHA) {
		return nil, errors.New("GitHub returned an invalid tree SHA")
	}
	if previous != nil {
		if nestedString(previous, "tree", "sha") != treeSHA {
			return nil, errGitHubConflict
		}
		return publishedCommit(branch, head, treeSHA, true), nil
	}
	if treeSHA == baseTree {
		return nil, errors.New("file changes do not change the inspected repository tree")
	}
	var commit map[string]interface{}
	if err = githubRequest(ctx, token, http.MethodPost, base+"/git/commits", map[string]interface{}{"message": message, "tree": treeSHA, "parents": []string{expected}}, &commit); err != nil {
		return nil, err
	}
	sha, _ := commit["sha"].(string)
	if !commitPattern.MatchString(sha) {
		return nil, errors.New("GitHub returned an invalid commit SHA")
	}
	// A concurrent branch update cannot be overwritten: GitHub enforces fast-forward.
	if err = githubRequest(ctx, token, http.MethodPatch, base+"/git/refs/heads/"+escapePath(branch), map[string]interface{}{"sha": sha, "force": false}, &ref); err != nil {
		return nil, err
	}
	return publishedCommit(branch, sha, treeSHA, false), nil
}

func publishedCommit(branch, sha, tree string, replayed bool) *executor.StepResult {
	return &executor.StepResult{Output: map[string]interface{}{"branch": branch, "commitSha": sha, "treeSha": tree, "published": true, "replayed": replayed}}
}
