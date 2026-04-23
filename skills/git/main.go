package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/axiom-studio/skills.sdk/executor"
	"github.com/axiom-studio/skills.sdk/grpc"
	"github.com/axiom-studio/skills.sdk/resolver"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
)

const (
	iconGit = "git-branch"
)

func main() {
	port := os.Getenv("SKILL_PORT")
	if port == "" {
		port = "50095"
	}

	server := grpc.NewSkillServer("skill-git", "1.0.0")

	server.RegisterExecutorWithSchema("git-health", &HealthExecutor{}, HealthSchema)

	server.RegisterExecutorWithSchema("git-clone", &CloneExecutor{}, CloneSchema)
	server.RegisterExecutorWithSchema("git-pull", &PullExecutor{}, PullSchema)
	server.RegisterExecutorWithSchema("git-commit", &CommitExecutor{}, CommitSchema)
	server.RegisterExecutorWithSchema("git-push", &PushExecutor{}, PushSchema)
	server.RegisterExecutorWithSchema("git-branch", &BranchExecutor{}, BranchSchema)
	server.RegisterExecutorWithSchema("git-branch-list", &BranchListExecutor{}, BranchListSchema)
	server.RegisterExecutorWithSchema("git-status", &StatusExecutor{}, StatusSchema)

	fmt.Printf("Starting skill-git gRPC server on port %s\n", port)
	if err := server.Serve(port); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to serve: %v\n", err)
		os.Exit(1)
	}
}

func getString(config map[string]interface{}, key string) string {
	if v, ok := config[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func getBool(config map[string]interface{}, key string, def bool) bool {
	if v, ok := config[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
		if s, ok := v.(string); ok {
			return s == "true"
		}
	}
	return def
}

func getStringSlice(config map[string]interface{}, key string) []string {
	if v, ok := config[key]; ok {
		if slice, ok := v.([]interface{}); ok {
			result := make([]string, 0, len(slice))
			for _, item := range slice {
				if s, ok := item.(string); ok {
					result = append(result, s)
				}
			}
			return result
		}
	}
	return nil
}

var HealthSchema = resolver.NewSchemaBuilder("git-health").
	WithName("Git Health Check").
	WithCategory("action").
	WithIcon(iconGit).
	WithDescription("Check if Git skill is running").
	Build()

var CloneSchema = resolver.NewSchemaBuilder("git-clone").
	WithName("Clone Repository").
	WithCategory("action").
	WithIcon(iconGit).
	WithDescription("Clone a Git repository").
	AddSection("Repository").
		AddExpressionField("url", "Repository URL",
			resolver.WithRequired(),
			resolver.WithPlaceholder("https://github.com/user/repo.git"),
			resolver.WithHint("Git repository URL to clone"),
		).
		AddExpressionField("path", "Local Path",
			resolver.WithRequired(),
			resolver.WithPlaceholder("/path/to/clone"),
			resolver.WithHint("Local directory to clone into"),
		).
		AddExpressionField("branch", "Branch",
			resolver.WithPlaceholder("main"),
			resolver.WithHint("Specific branch to clone (optional)"),
		).
		AddExpressionField("depth", "Depth",
			resolver.WithPlaceholder("0"),
			resolver.WithHint("Shallow clone depth (0 for full clone)"),
		).
		EndSection().
	AddSection("Authentication").
		AddExpressionField("username", "Username",
			resolver.WithHint("Username for authentication (optional)"),
		).
		AddExpressionField("password", "Password",
			resolver.WithSensitive(),
			resolver.WithHint("Password or token for authentication (optional)"),
		).
		EndSection().
	Build()

var PullSchema = resolver.NewSchemaBuilder("git-pull").
	WithName("Pull Changes").
	WithCategory("action").
	WithIcon(iconGit).
	WithDescription("Pull changes from remote repository").
	AddSection("Repository").
		AddExpressionField("path", "Local Path",
			resolver.WithRequired(),
			resolver.WithPlaceholder("/path/to/repo"),
			resolver.WithHint("Local repository path"),
		).
		AddExpressionField("remote", "Remote",
			resolver.WithDefault("origin"),
			resolver.WithPlaceholder("origin"),
			resolver.WithHint("Remote name"),
		).
		AddExpressionField("branch", "Branch",
			resolver.WithPlaceholder("main"),
			resolver.WithHint("Branch to pull from"),
		).
		EndSection().
	Build()

var CommitSchema = resolver.NewSchemaBuilder("git-commit").
	WithName("Commit Changes").
	WithCategory("action").
	WithIcon(iconGit).
	WithDescription("Commit changes in a repository").
	AddSection("Repository").
		AddExpressionField("path", "Local Path",
			resolver.WithRequired(),
			resolver.WithPlaceholder("/path/to/repo"),
			resolver.WithHint("Local repository path"),
		).
		AddExpressionField("message", "Commit Message",
			resolver.WithRequired(),
			resolver.WithPlaceholder("Commit message"),
			resolver.WithHint("Commit message"),
		).
		AddExpressionField("authorName", "Author Name",
			resolver.WithHint("Override commit author name"),
		).
		AddExpressionField("authorEmail", "Author Email",
			resolver.WithHint("Override commit author email"),
		).
		EndSection().
	AddSection("Options").
		AddToggleField("all", "Stage All",
			resolver.WithDefault(true),
			resolver.WithHint("Stage all modified files"),
		).
		EndSection().
	Build()

var PushSchema = resolver.NewSchemaBuilder("git-push").
	WithName("Push Changes").
	WithCategory("action").
	WithIcon(iconGit).
	WithDescription("Push commits to remote repository").
	AddSection("Repository").
		AddExpressionField("path", "Local Path",
			resolver.WithRequired(),
			resolver.WithPlaceholder("/path/to/repo"),
			resolver.WithHint("Local repository path"),
		).
		AddExpressionField("remote", "Remote",
			resolver.WithDefault("origin"),
			resolver.WithPlaceholder("origin"),
			resolver.WithHint("Remote name"),
		).
		AddExpressionField("branch", "Branch",
			resolver.WithPlaceholder("main"),
			resolver.WithHint("Branch to push"),
		).
		EndSection().
	AddSection("Authentication").
		AddExpressionField("username", "Username",
			resolver.WithHint("Username for authentication (optional)"),
		).
		AddExpressionField("password", "Password",
			resolver.WithSensitive(),
			resolver.WithHint("Password or token for authentication (optional)"),
		).
		AddToggleField("force", "Force Push",
			resolver.WithDefault(false),
			resolver.WithHint("Force push (use with caution)"),
		).
		EndSection().
	Build()

var BranchSchema = resolver.NewSchemaBuilder("git-branch").
	WithName("Branch Operations").
	WithCategory("action").
	WithIcon(iconGit).
	WithDescription("List, create, or delete branches").
	AddSection("Repository").
		AddExpressionField("path", "Local Path",
			resolver.WithRequired(),
			resolver.WithPlaceholder("/path/to/repo"),
			resolver.WithHint("Local repository path"),
		).
		EndSection().
	AddSection("Operation").
		AddSelectField("operation", "Operation",
			[]resolver.SelectOption{
				{Label: "List", Value: "list"},
				{Label: "Create", Value: "create"},
				{Label: "Delete", Value: "delete"},
			},
			resolver.WithDefault("list"),
			resolver.WithHint("Branch operation to perform"),
		).
		AddExpressionField("branchName", "Branch Name",
			resolver.WithPlaceholder("feature/new-branch"),
			resolver.WithHint("Branch name for create/delete operations"),
		).
		AddToggleField("createFrom", "Create From",
			resolver.WithDefault("HEAD"),
			resolver.WithHint("Base branch/commit for new branch"),
		).
		AddToggleField("force", "Force",
			resolver.WithDefault(false),
			resolver.WithHint("Force delete or overwrite"),
		).
		EndSection().
	Build()

var StatusSchema = resolver.NewSchemaBuilder("git-status").
	WithName("Repository Status").
	WithCategory("action").
	WithIcon(iconGit).
	WithDescription("Get the status of a Git repository").
	AddSection("Repository").
		AddExpressionField("path", "Local Path",
			resolver.WithRequired(),
			resolver.WithPlaceholder("/path/to/repo"),
			resolver.WithHint("Local repository path"),
		).
		AddToggleField("porcelain", "Porcelain",
			resolver.WithDefault(false),
			resolver.WithHint("Use porcelain format"),
		).
		EndSection().
	Build()

var BranchListSchema = resolver.NewSchemaBuilder("git-branch-list").
	WithName("List Branches").
	WithCategory("action").
	WithIcon(iconGit).
	WithDescription("List all branches in a Git repository").
	AddSection("Repository").
		AddExpressionField("path", "Local Path",
			resolver.WithRequired(),
			resolver.WithPlaceholder("/path/to/repo"),
			resolver.WithHint("Local repository path"),
		).
		EndSection().
	AddSection("Options").
		AddToggleField("all", "Include Remote",
			resolver.WithDefault(false),
			resolver.WithHint("Include remote branches"),
		).
		AddExpressionField("remote", "Remote Name",
			resolver.WithPlaceholder("origin"),
			resolver.WithHint("Filter by remote name"),
		).
		EndSection().
	Build()

type HealthExecutor struct{}

func (e *HealthExecutor) Type() string { return "git-health" }

func (e *HealthExecutor) Execute(ctx context.Context, step *executor.StepDefinition, res executor.TemplateResolver) (*executor.StepResult, error) {
	return &executor.StepResult{
		Output: map[string]interface{}{
			"status": "OK",
		},
	}, nil
}

type CloneExecutor struct{}

func (e *CloneExecutor) Type() string { return "git-clone" }

func (e *CloneExecutor) Execute(ctx context.Context, step *executor.StepDefinition, res executor.TemplateResolver) (*executor.StepResult, error) {
	config := res.ResolveMap(step.Config)

	url := res.ResolveString(getString(config, "url"))
	path := res.ResolveString(getString(config, "path"))
	branch := res.ResolveString(getString(config, "branch"))
	depth := getString(config, "depth")

	if url == "" {
		return nil, fmt.Errorf("repository URL is required")
	}
	if path == "" {
		return nil, fmt.Errorf("local path is required")
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"message":  "Clone not yet implemented",
			"url":      url,
			"path":     path,
			"branch":   branch,
			"depth":    depth,
			"success":  false,
		},
	}, nil
}

type PullExecutor struct{}

func (e *PullExecutor) Type() string { return "git-pull" }

func (e *PullExecutor) Execute(ctx context.Context, step *executor.StepDefinition, res executor.TemplateResolver) (*executor.StepResult, error) {
	config := res.ResolveMap(step.Config)

	path := res.ResolveString(getString(config, "path"))
	remote := res.ResolveString(getString(config, "remote"))
	branch := res.ResolveString(getString(config, "branch"))

	if path == "" {
		return nil, fmt.Errorf("local path is required")
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"message": "Pull not yet implemented",
			"path":    path,
			"remote":  remote,
			"branch":  branch,
			"success": false,
		},
	}, nil
}

type CommitExecutor struct{}

func (e *CommitExecutor) Type() string { return "git-commit" }

func (e *CommitExecutor) Execute(ctx context.Context, step *executor.StepDefinition, res executor.TemplateResolver) (*executor.StepResult, error) {
	config := res.ResolveMap(step.Config)

	path := res.ResolveString(getString(config, "path"))
	message := res.ResolveString(getString(config, "message"))
	all := getBool(config, "all", true)

	if path == "" {
		return nil, fmt.Errorf("local path is required")
	}
	if message == "" {
		return nil, fmt.Errorf("commit message is required")
	}

	repo, err := git.PlainOpen(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open repository: %w", err)
	}

	worktree, err := repo.Worktree()
	if err != nil {
		return nil, fmt.Errorf("failed to get worktree: %w", err)
	}

	files := getStringSlice(config, "files")

	if all {
		err = worktree.AddWithOptions(&git.AddOptions{
			All: true,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to stage all changes: %w", err)
		}
	} else if len(files) > 0 {
		for _, file := range files {
			_, err = worktree.Add(file)
			if err != nil {
				return nil, fmt.Errorf("failed to stage file %s: %w", file, err)
			}
		}
	}

	status, err := worktree.Status()
	if err != nil {
		return nil, fmt.Errorf("failed to get status: %w", err)
	}

	if len(status) == 0 {
		return nil, fmt.Errorf("no changes to commit")
	}

	hash, err := worktree.Commit(message, &git.CommitOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to commit: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"sha":     hash.String(),
			"message": message,
			"path":    path,
			"success": true,
		},
	}, nil
}

type PushExecutor struct{}

func (e *PushExecutor) Type() string { return "git-push" }

func (e *PushExecutor) Execute(ctx context.Context, step *executor.StepDefinition, res executor.TemplateResolver) (*executor.StepResult, error) {
	config := res.ResolveMap(step.Config)

	path := res.ResolveString(getString(config, "path"))
	remote := res.ResolveString(getString(config, "remote"))
	branch := res.ResolveString(getString(config, "branch"))
	username := res.ResolveString(getString(config, "username"))
	password := res.ResolveString(getString(config, "password"))
	_ = password
	force := getBool(config, "force", false)

	if path == "" {
		return nil, fmt.Errorf("local path is required")
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"message":  "Push not yet implemented",
			"path":     path,
			"remote":   remote,
			"branch":   branch,
			"username": username,
			"force":    force,
			"success":  false,
		},
	}, nil
}

type BranchExecutor struct{}

func (e *BranchExecutor) Type() string { return "git-branch" }

func (e *BranchExecutor) Execute(ctx context.Context, step *executor.StepDefinition, res executor.TemplateResolver) (*executor.StepResult, error) {
	config := res.ResolveMap(step.Config)

	path := res.ResolveString(getString(config, "path"))
	operation := res.ResolveString(getString(config, "operation"))
	branchName := res.ResolveString(getString(config, "branchName"))
	createFrom := res.ResolveString(getString(config, "createFrom"))
	force := getBool(config, "force", false)

	if path == "" {
		return nil, fmt.Errorf("local path is required")
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"message":    "Branch operation not yet implemented",
			"path":       path,
			"operation":  operation,
			"branchName": branchName,
			"createFrom": createFrom,
			"force":      force,
			"success":    false,
		},
	}, nil
}

type StatusExecutor struct{}

func (e *StatusExecutor) Type() string { return "git-status" }

func (e *StatusExecutor) Execute(ctx context.Context, step *executor.StepDefinition, res executor.TemplateResolver) (*executor.StepResult, error) {
	config := res.ResolveMap(step.Config)

	path := res.ResolveString(getString(config, "path"))
	porcelain := getBool(config, "porcelain", false)

	if path == "" {
		return nil, fmt.Errorf("local path is required")
	}

	repo, err := git.PlainOpen(path)
	if err != nil {
		return &executor.StepResult{
			Output: map[string]interface{}{
				"message":   "Not a valid git repository",
				"path":      path,
				"porcelain": porcelain,
				"valid":     false,
				"success":   false,
			},
		}, nil
	}

	worktree, err := repo.Worktree()
	if err != nil {
		return nil, fmt.Errorf("failed to get worktree: %w", err)
	}

	status, err := worktree.Status()
	if err != nil {
		return nil, fmt.Errorf("failed to get status: %w", err)
	}

	var staged, unstaged, untracked []string

	for file, fileStatus := range status {
		if fileStatus.Staging == git.Untracked && fileStatus.Worktree == git.Untracked {
			untracked = append(untracked, file)
			continue
		}

		if fileStatus.Staging != git.Untracked && fileStatus.Staging != git.Unmodified {
			staged = append(staged, file)
		}

		if fileStatus.Worktree != git.Unmodified {
			unstaged = append(unstaged, file)
		}
	}

	isClean := len(staged) == 0 && len(unstaged) == 0 && len(untracked) == 0

	var summary string
	if isClean {
		summary = "Working tree clean"
	} else {
		parts := []string{}
		if len(staged) > 0 {
			parts = append(parts, fmt.Sprintf("%d staged", len(staged)))
		}
		if len(unstaged) > 0 {
			parts = append(parts, fmt.Sprintf("%d unstaged", len(unstaged)))
		}
		if len(untracked) > 0 {
			parts = append(parts, fmt.Sprintf("%d untracked", len(untracked)))
		}
		summary = strings.Join(parts, ", ")
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":   true,
			"isClean":   isClean,
			"staged":    staged,
			"unstaged":  unstaged,
			"untracked": untracked,
			"summary":   summary,
			"path":      path,
			"valid":     true,
		},
	}, nil
}

type BranchListExecutor struct{}

func (e *BranchListExecutor) Type() string { return "git-branch-list" }

func (e *BranchListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, res executor.TemplateResolver) (*executor.StepResult, error) {
	config := res.ResolveMap(step.Config)

	path := res.ResolveString(getString(config, "path"))
	includeRemote := getBool(config, "all", false)
	remoteName := res.ResolveString(getString(config, "remote"))

	if path == "" {
		return nil, fmt.Errorf("local path is required")
	}

	repo, err := git.PlainOpen(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open repository: %w", err)
	}

	branches, err := repo.Branches()
	if err != nil {
		return nil, fmt.Errorf("failed to get branches: %w", err)
	}
	defer branches.Close()

	var branchNames []string

	err = branches.ForEach(func(ref *plumbing.Reference) error {
		if ref.Type() == plumbing.SymbolicReference && ref.Type() != plumbing.HashReference {
			return nil
		}
		branchName := ref.Name().Short()
		if remoteName != "" {
			expectedPrefix := remoteName + "/"
			if branchName != remoteName && len(branchName) > len(expectedPrefix) && branchName[:len(expectedPrefix)] == expectedPrefix {
				branchNames = append(branchNames, branchName)
			}
		} else if ref.Name().IsBranch() || (includeRemote && ref.Name().IsRemote()) {
			branchNames = append(branchNames, branchName)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to iterate branches: %w", err)
	}

	headRef, err := repo.Head()
	var currentBranch string
	if err == nil && headRef != nil {
		if headRef.Type() == plumbing.SymbolicReference {
			currentBranch = headRef.Name().Short()
		} else if headRef.Type() == plumbing.HashReference {
			currentBranch = headRef.Hash().String()[:7]
		}
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":       true,
			"branches":      branchNames,
			"currentBranch": currentBranch,
			"count":         len(branchNames),
		},
	}, nil
}
