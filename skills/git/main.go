package main

import (
	"context"
	"fmt"
	"os"

	"github.com/axiom-studio/skills.sdk/executor"
	"github.com/axiom-studio/skills.sdk/grpc"
	"github.com/axiom-studio/skills.sdk/resolver"
	"github.com/go-git/go-git/v5"
)

const (
	iconGit = "git-branch"
)

func main() {
	// Get port from env or use default
	port := os.Getenv("SKILL_PORT")
	if port == "" {
		port = "50095"
	}

	// Create skill server
	server := grpc.NewSkillServer("skill-git", "1.0.0")

	// Register health check
	server.RegisterExecutorWithSchema("git-health", &HealthExecutor{}, HealthSchema)

	// Repository executors
	server.RegisterExecutorWithSchema("git-clone", &CloneExecutor{}, CloneSchema)
	server.RegisterExecutorWithSchema("git-pull", &PullExecutor{}, PullSchema)
	server.RegisterExecutorWithSchema("git-commit", &CommitExecutor{}, CommitSchema)
	server.RegisterExecutorWithSchema("git-push", &PushExecutor{}, PushSchema)
	server.RegisterExecutorWithSchema("git-branch", &BranchExecutor{}, BranchSchema)
	server.RegisterExecutorWithSchema("git-status", &StatusExecutor{}, StatusSchema)

	fmt.Printf("Starting skill-git gRPC server on port %s\n", port)
	if err := server.Serve(port); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to serve: %v\n", err)
		os.Exit(1)
	}
}

// ============================================================================
// CONFIG HELPERS
// ============================================================================

// Helper to get string from config
func getString(config map[string]interface{}, key string) string {
	if v, ok := config[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// Helper to get bool from config
func getBool(config map[string]interface{}, key string, def bool) bool {
	if v, ok := config[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
		// Handle string "true"/"false"
		if s, ok := v.(string); ok {
			return s == "true"
		}
	}
	return def
}

// ============================================================================
// SCHEMAS
// ============================================================================

// HealthSchema is the UI schema for git-health
var HealthSchema = resolver.NewSchemaBuilder("git-health").
	WithName("Git Health Check").
	WithCategory("action").
	WithIcon(iconGit).
	WithDescription("Check if Git skill is running").
	Build()

// CloneSchema is the UI schema for git-clone
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

// PullSchema is the UI schema for git-pull
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

// CommitSchema is the UI schema for git-commit
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

// PushSchema is the UI schema for git-push
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

// BranchSchema is the UI schema for git-branch
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

// StatusSchema is the UI schema for git-status
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

// ============================================================================
// EXECUTORS
// ============================================================================

// HealthExecutor handles git-health
type HealthExecutor struct{}

func (e *HealthExecutor) Type() string { return "git-health" }

func (e *HealthExecutor) Execute(ctx context.Context, step *executor.StepDefinition, res executor.TemplateResolver) (*executor.StepResult, error) {
	return &executor.StepResult{
		Output: map[string]interface{}{
			"status": "OK",
		},
	}, nil
}

// CloneExecutor handles git-clone
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

	// Placeholder implementation
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

// PullExecutor handles git-pull
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

	// Placeholder implementation
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

// CommitExecutor handles git-commit
type CommitExecutor struct{}

func (e *CommitExecutor) Type() string { return "git-commit" }

func (e *CommitExecutor) Execute(ctx context.Context, step *executor.StepDefinition, res executor.TemplateResolver) (*executor.StepResult, error) {
	config := res.ResolveMap(step.Config)

	path := res.ResolveString(getString(config, "path"))
	message := res.ResolveString(getString(config, "message"))
	authorName := res.ResolveString(getString(config, "authorName"))
	authorEmail := res.ResolveString(getString(config, "authorEmail"))
	all := getBool(config, "all", true)

	if path == "" {
		return nil, fmt.Errorf("local path is required")
	}
	if message == "" {
		return nil, fmt.Errorf("commit message is required")
	}

	// Placeholder implementation
	return &executor.StepResult{
		Output: map[string]interface{}{
			"message":    "Commit not yet implemented",
			"path":       path,
			"commitMsg":  message,
			"authorName": authorName,
			"authorEmail": authorEmail,
			"stageAll":   all,
			"success":    false,
		},
	}, nil
}

// PushExecutor handles git-push
type PushExecutor struct{}

func (e *PushExecutor) Type() string { return "git-push" }

func (e *PushExecutor) Execute(ctx context.Context, step *executor.StepDefinition, res executor.TemplateResolver) (*executor.StepResult, error) {
	config := res.ResolveMap(step.Config)

	path := res.ResolveString(getString(config, "path"))
	remote := res.ResolveString(getString(config, "remote"))
	branch := res.ResolveString(getString(config, "branch"))
	username := res.ResolveString(getString(config, "username"))
	password := res.ResolveString(getString(config, "password"))
	_ = password // TODO: implement password authentication
	force := getBool(config, "force", false)

	if path == "" {
		return nil, fmt.Errorf("local path is required")
	}

	// Placeholder implementation
	return &executor.StepResult{
		Output: map[string]interface{}{
			"message": "Push not yet implemented",
			"path":    path,
			"remote":  remote,
			"branch":  branch,
			"username": username,
			"force":   force,
			"success": false,
		},
	}, nil
}

// BranchExecutor handles git-branch
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

	// Placeholder implementation
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

// StatusExecutor handles git-status
type StatusExecutor struct{}

func (e *StatusExecutor) Type() string { return "git-status" }

func (e *StatusExecutor) Execute(ctx context.Context, step *executor.StepDefinition, res executor.TemplateResolver) (*executor.StepResult, error) {
	config := res.ResolveMap(step.Config)

	path := res.ResolveString(getString(config, "path"))
	porcelain := getBool(config, "porcelain", false)

	if path == "" {
		return nil, fmt.Errorf("local path is required")
	}

	// Placeholder implementation - try to open the repo to check if it's valid
	_, err := git.PlainOpen(path)
	if err != nil {
		return &executor.StepResult{
			Output: map[string]interface{}{
				"message":   "Not a valid git repository",
				"path":      path,
				"porcelain": porcelain,
				"valid":     false,
			},
		}, nil
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"message":  "Status not yet implemented",
			"path":     path,
			"porcelain": porcelain,
			"valid":    true,
			"success":  false,
		},
	}, nil
}
