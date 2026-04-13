# ArgoCD Skill

ArgoCD GitOps operations for Atlas agents.

## Overview

This skill provides comprehensive ArgoCD operations for GitOps workflows, including application management, synchronization, rollbacks, project management, and repository configuration.

## Features

- **Application Management**: Create, update, delete, and sync applications
- **Sync Operations**: Sync applications with various strategies and options
- **Rollback**: View history and rollback to previous revisions
- **Resource Management**: View application resources and logs
- **Project Management**: Manage ArgoCD projects
- **Repository Management**: Add and list Git repositories
- **Cluster Management**: List connected clusters

## Node Types

### Application Operations

#### argocd-app-sync

Sync an ArgoCD application.

**Configuration:**
- `server`: ArgoCD server address
- `authToken`: Authentication token
- `appName`: Application name
- `revision`: Revision to sync to (optional, defaults to HEAD)
- `prune`: Prune resources (default: false)
- `dryRun`: Dry run mode (default: false)
- `strategy`: Sync strategy (apply, hook, replace)
- `force`: Force sync (default: false)
- `async`: Async sync (default: false)

**Output:**
- `status`: Sync status
- `operationId`: Operation ID

#### argocd-app-status

Get application sync and health status.

**Configuration:**
- `server`: ArgoCD server address
- `authToken`: Authentication token
- `appName`: Application name
- `refresh`: Refresh status before returning (default: true)

**Output:**
- `syncStatus`: Sync status (Synced, OutOfSync, Unknown)
- `healthStatus`: Health status (Healthy, Degraded, Progressing, Unknown)
- `revision`: Current revision
- `conditions`: Status conditions

#### argocd-app-get

Get application details.

**Configuration:**
- `server`: ArgoCD server address
- `authToken`: Authentication token
- `appName`: Application name

**Output:**
- `name`: Application name
- `project`: Project name
- `source`: Git source details
- `destination`: Deployment destination
- `status`: Application status

#### argocd-app-create

Create a new application.

**Configuration:**
- `server`: ArgoCD server address
- `authToken`: Authentication token
- `name`: Application name
- `project`: Project name (default: default)
- `repoUrl`: Git repository URL
- `path`: Application path in repo
- `revision`: Git revision (default: HEAD)
- `destServer`: Destination cluster URL
- `destNamespace`: Destination namespace
- `syncPolicy`: Sync policy (automated, manual)
- `prune`: Auto-prune (default: false)
- `selfHeal`: Self-heal (default: false)

**Output:**
- `name`: Created application name
- `status`: Creation status

#### argocd-app-update

Update an existing application.

**Configuration:**
- `server`: ArgoCD server address
- `authToken`: Authentication token
- `name`: Application name
- `repoUrl`: New repository URL (optional)
- `path`: New path (optional)
- `revision`: New revision (optional)
- `destNamespace`: New namespace (optional)
- `syncPolicy`: New sync policy (optional)

**Output:**
- `name`: Updated application name
- `status`: Update status

#### argocd-app-delete

Delete an application.

**Configuration:**
- `server`: ArgoCD server address
- `authToken`: Authentication token
- `appName`: Application name
- `cascade`: Cascade delete resources (default: true)

**Output:**
- `status`: Deletion status

#### argocd-app-diff

Compare application state with Git.

**Configuration:**
- `server`: ArgoCD server address
- `authToken`: Authentication token
- `appName`: Application name
- `revision`: Revision to compare (optional)

**Output:**
- `diff`: Diff output
- `hasDifferences`: Boolean indicating differences

#### argocd-app-history

View application sync history.

**Configuration:**
- `server`: ArgoCD server address
- `authToken`: Authentication token
- `appName`: Application name

**Output:**
- `history`: List of sync history entries
- `count`: Number of history entries

#### argocd-app-rollback

Rollback application to a previous revision.

**Configuration:**
- `server`: ArgoCD server address
- `authToken`: Authentication token
- `appName`: Application name
- `historyId`: History ID to rollback to

**Output:**
- `status`: Rollback status
- `revision`: Rolled back to revision

#### argocd-app-refresh

Refresh application state.

**Configuration:**
- `server`: ArgoCD server address
- `authToken`: Authentication token
- `appName`: Application name
- `hard`: Hard refresh (default: false)

**Output:**
- `status`: Refresh status

#### argocd-app-resources

List application resources.

**Configuration:**
- `server`: ArgoCD server address
- `authToken`: Authentication token
- `appName`: Application name
- `namespace`: Filter by namespace (optional)
- `kind`: Filter by kind (optional)
- `group`: Filter by group (optional)

**Output:**
- `resources`: List of resources
- `count`: Resource count

#### argocd-app-logs

Get application pod logs.

**Configuration:**
- `server`: ArgoCD server address
- `authToken`: Authentication token
- `appName`: Application name
- `podName`: Pod name (optional, all pods if not specified)
- `container`: Container name (optional)
- `tailLines`: Number of lines to tail (optional)
- `sinceTime`: Logs since time (optional)

**Output:**
- `logs`: Log output

### Project Operations

#### argocd-project-list

List all projects.

**Configuration:**
- `server`: ArgoCD server address
- `authToken`: Authentication token

**Output:**
- `projects`: List of projects

#### argocd-project-get

Get project details.

**Configuration:**
- `server`: ArgoCD server address
- `authToken`: Authentication token
- `name`: Project name

**Output:**
- `name`: Project name
- `description`: Project description
- `destinations`: Allowed destinations
- `sourceRepos`: Allowed source repos

#### argocd-project-create

Create a new project.

**Configuration:**
- `server`: ArgoCD server address
- `authToken`: Authentication token
- `name`: Project name
- `description`: Project description (optional)
- `destinations`: Allowed destinations (optional)
- `sourceRepos`: Allowed source repos (optional)

**Output:**
- `name`: Created project name
- `status`: Creation status

### Repository Operations

#### argocd-repo-add

Add a Git repository.

**Configuration:**
- `server`: ArgoCD server address
- `authToken`: Authentication token
- `repoUrl`: Repository URL
- `username`: Username (optional)
- `password`: Password (optional)
- `sshPrivateKey`: SSH private key (optional)
- `insecure`: Skip TLS verification (default: false)

**Output:**
- `status`: Connection status
- `repoUrl`: Repository URL

#### argocd-repo-list

List registered repositories.

**Configuration:**
- `server`: ArgoCD server address
- `authToken`: Authentication token

**Output:**
- `repos`: List of repositories

### Cluster Operations

#### argocd-cluster-list

List connected clusters.

**Configuration:**
- `server`: ArgoCD server address
- `authToken`: Authentication token

**Output:**
- `clusters`: List of clusters
- `count`: Cluster count

### System Operations

#### argocd-version

Get ArgoCD version information.

**Configuration:**
- `server`: ArgoCD server address
- `authToken`: Authentication token

**Output:**
- `version`: ArgoCD version
- `buildDate`: Build date
- `goVersion`: Go version

## Usage Examples

### Sync an Application

```yaml
- id: sync-app
  type: argocd-app-sync
  config:
    server: https://argocd.example.com
    authToken: "{{secrets.argocd_token}}"
    appName: myapp
    prune: true
    strategy: apply
```

### Create an Application

```yaml
- id: create-app
  type: argocd-app-create
  config:
    server: https://argocd.example.com
    authToken: "{{secrets.argocd_token}}"
    name: myapp
    project: default
    repoUrl: https://github.com/myorg/myapp.git
    path: k8s/overlays/production
    revision: main
    destServer: https://kubernetes.default.svc
    destNamespace: production
    syncPolicy: automated
    prune: true
    selfHeal: true
```

### Check Application Status

```yaml
- id: check-status
  type: argocd-app-status
  config:
    server: https://argocd.example.com
    authToken: "{{secrets.argocd_token}}"
    appName: myapp
```

### Rollback Application

```yaml
- id: rollback-app
  type: argocd-app-rollback
  config:
    server: https://argocd.example.com
    authToken: "{{secrets.argocd_token}}"
    appName: myapp
    historyId: "5"
```

### Add a Repository

```yaml
- id: add-repo
  type: argocd-repo-add
  config:
    server: https://argocd.example.com
    authToken: "{{secrets.argocd_token}}"
    repoUrl: https://github.com/myorg/myrepo.git
    username: "{{secrets.github_user}}"
    password: "{{secrets.github_token}}"
```

### Get Application Logs

```yaml
- id: get-logs
  type: argocd-app-logs
  config:
    server: https://argocd.example.com
    authToken: "{{secrets.argocd_token}}"
    appName: myapp
    tailLines: "100"
```

## Authentication

### Token Authentication

Use a Bearer token for authentication:

```yaml
authToken: "{{secrets.argocd_token}}"
```

### Generating a Token

```bash
argocd account generate-token
```

## Security Considerations

- Use short-lived tokens
- Store tokens in secrets manager
- Use RBAC to limit token permissions
- Enable SSO for user authentication
- Use SSH keys for repository access

## Building

```bash
go mod tidy
CGO_ENABLED=0 go build -o skill-argocd .
```

## Running

```bash
# Default port 50073
./skill-argocd

# Custom port
SKILL_PORT=50080 ./skill-argocd
```

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `SKILL_PORT` | 50073 | gRPC server port |

## License

MIT