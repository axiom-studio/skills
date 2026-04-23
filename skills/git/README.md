# Git Skill

Git version control operations for Atlas agents using pure Go (go-git library).

## Overview

This skill provides Git operations for workflow automation, including repository cloning, committing, pushing, pulling, branch management, and status checking.

## Features

- **Clone Repository**: Clone public or private Git repositories
- **Commit Changes**: Stage and commit changes with custom author info
- **Push Changes**: Push commits to remote repositories
- **Pull Changes**: Pull latest changes from remote
- **Branch Operations**: Create, list, and delete branches
- **Status Check**: Get working tree status with staged/unstaged/untracked files

## Node Types

### git-health

Health check for the Git skill service.

**Configuration:** None

**Output:**
- `status`: Always returns "OK"

---

### git-clone

Clone a Git repository.

**Configuration:**
- `url` (string, required): Repository URL to clone
- `path` (string, required): Local directory to clone into
- `branch` (string, optional): Specific branch to clone (default: default branch)
- `depth` (integer, optional): Shallow clone depth (0 for full clone)
- `username` (string, optional): Username for authentication
- `password` (string, optional): Password or token for authentication

**Output:**
- `success`: Whether operation succeeded
- `url`: Repository URL
- `path`: Local path
- `branch`: Branch cloned
- `depth`: Clone depth
- `message`: Status message

---

### git-commit

Commit changes in a repository.

**Configuration:**
- `path` (string, required): Local repository path
- `message` (string, required): Commit message
- `all` (boolean, optional): Stage all modified files (default: true)
- `files` (array, optional): Specific files to stage (if not using all)
- `authorName` (string, optional): Override commit author name
- `authorEmail` (string, optional): Override commit author email
- `amend` (boolean, optional): Amend previous commit

**Output:**
- `success`: Whether operation succeeded
- `sha`: Commit hash
- `message`: Commit message
- `path`: Repository path

---

### git-push

Push commits to remote repository.

**Configuration:**
- `path` (string, required): Local repository path
- `remote` (string, optional): Remote name (default: origin)
- `branch` (string, optional): Branch to push
- `username` (string, optional): Username for authentication
- `password` (string, optional): Password or token for authentication
- `force` (boolean, optional): Force push (default: false)

**Output:**
- `success`: Whether operation succeeded
- `message`: Status message
- `path`: Repository path
- `remote`: Remote name
- `branch`: Branch pushed

---

### git-pull

Pull changes from remote repository.

**Configuration:**
- `path` (string, required): Local repository path
- `remote` (string, optional): Remote name (default: origin)
- `branch` (string, optional): Branch to pull
- `rebase` (boolean, optional): Use rebase instead of merge (default: false)

**Output:**
- `success`: Whether operation succeeded
- `message`: Status message
- `path`: Repository path
- `remote`: Remote name
- `branch`: Branch pulled

---

### git-branch

Branch operations: list, create, or delete branches.

**Configuration:**
- `path` (string, required): Local repository path
- `operation` (string, optional): Operation to perform (list, create, delete)
- `branchName` (string, optional): Branch name for create/delete operations
- `createFrom` (string, optional): Base branch/commit for new branch (default: HEAD)
- `force` (boolean, optional): Force delete or overwrite (default: false)

**Output:**
- `success`: Whether operation succeeded
- `message`: Status message
- `path`: Repository path
- `operation`: Operation performed
- `branchName`: Branch name

---

### git-branch-list

List all branches in a repository.

**Configuration:**
- `path` (string, required): Local repository path
- `all` (boolean, optional): Include remote branches (default: false)
- `remote` (string, optional): Filter by remote name

**Output:**
- `success`: Whether operation succeeded
- `branches`: List of branch names
- `currentBranch`: Currently checked out branch
- `count`: Number of branches

---

### git-status

Get the status of a Git repository.

**Configuration:**
- `path` (string, required): Local repository path
- `porcelain` (boolean, optional): Use porcelain format (default: false)

**Output:**
- `success`: Whether operation succeeded
- `isClean`: Whether working tree is clean
- `staged`: List of staged files
- `unstaged`: List of modified but unstaged files
- `untracked`: List of untracked files
- `summary`: Human-readable status summary
- `valid`: Whether path is a valid Git repository
- `path`: Repository path

## Usage Examples

### Clone a Repository

```yaml
- id: clone-repo
  type: git-clone
  config:
    url: https://github.com/myorg/myrepo.git
    path: /tmp/myrepo
    branch: main
    depth: 0
```

### Commit Changes

```yaml
- id: commit-changes
  type: git-commit
  config:
    path: /tmp/myrepo
    message: "Add new feature"
    all: true
    authorName: "Atlas Agent"
    authorEmail: "agent@axiomstudio.ai"
```

### Push Changes

```yaml
- id: push-changes
  type: git-push
  config:
    path: /tmp/myrepo
    remote: origin
    branch: main
    force: false
```

### Pull Changes

```yaml
- id: pull-changes
  type: git-pull
  config:
    path: /tmp/myrepo
    remote: origin
    branch: main
    rebase: false
```

### List Branches

```yaml
- id: list-branches
  type: git-branch-list
  config:
    path: /tmp/myrepo
    all: true
    remote: origin
```

### Check Status

```yaml
- id: check-status
  type: git-status
  config:
    path: /tmp/myrepo
    porcelain: false
```

## Authentication

The skill supports multiple authentication methods:

- **None**: Public repositories
- **Basic Auth**: Username and password/token for private repos
- **SSH**: SSH key-based authentication (coming soon)

For private repositories, pass credentials via configuration:

```yaml
config:
  username: "{{secrets.github_user}}"
  password: "{{secrets.github_token}}"
```

## Building

```bash
cd skills/git
go mod tidy
CGO_ENABLED=0 go build -o skill-git .
```

## Cross-Compile

Build for different platforms:

```bash
# Linux AMD64
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o skill-git-linux-amd64 .

# Linux ARM64
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o skill-git-linux-arm64 .

# macOS AMD64
GOOS=darwin GOARCH=amd64 CGO_ENABLED=0 go build -o skill-git-darwin-amd64 .

# macOS ARM64 (Apple Silicon)
GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -o skill-git-darwin-arm64 .

# Windows AMD64
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o skill-git-windows-amd64.exe .
```

## Running

```bash
# Default port 50095
./skill-git

# Custom port
SKILL_PORT=50080 ./skill-git
```

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `SKILL_PORT` | 50095 | gRPC server port |

## Dependencies

- [go-git](https://github.com/go-git/go-git): Pure Go Git implementation
- Axiom Studio Skill SDK

## License

MIT
