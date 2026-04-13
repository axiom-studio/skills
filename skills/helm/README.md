# Helm Skill

Helm Kubernetes package manager operations for Atlas agents. Manage chart installations, upgrades, rollbacks, and repositories.

## Overview

The Helm skill provides comprehensive node types for managing Kubernetes applications via Helm. It supports chart installation, upgrades, rollbacks, repository management, and release operations.

## Node Types

### `helm-install`

Install a Helm chart.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| releaseName | string | Yes | Release name |
| chart | string | Yes | Chart name or path |
| namespace | string | No | Target namespace |
| createNamespace | boolean | No | Create namespace if missing |
| values | object | No | Override values |
| valuesFile | string | No | Values file path |
| repo | string | No | Chart repository URL |
| version | string | No | Chart version |
| timeout | string | No | Timeout (e.g., 10m) |
| wait | boolean | No | Wait for resources |
| waitForJobs | boolean | No | Wait for Jobs to complete |

**Output:**

```json
{
  "success": true,
  "releaseName": "nginx",
  "namespace": "web",
  "status": "deployed",
  "revision": 1,
  "chart": "nginx-15.0.0",
  "appVersion": "1.25.3"
}
```

---

### `helm-upgrade`

Upgrade a release.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| releaseName | string | Yes | Release name |
| chart | string | Yes | Chart name |
| namespace | string | No | Namespace |
| values | object | No | Override values |
| reuseValues | boolean | No | Reuse existing values |
| resetValues | boolean | No | Reset to chart defaults |
| force | boolean | No | Force resource updates |
| timeout | string | No | Timeout |
| wait | boolean | No | Wait for resources |

---

### `helm-uninstall`

Uninstall a release.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| releaseName | string | Yes | Release name |
| namespace | string | No | Namespace |
| keepHistory | boolean | No | Keep release history |

---

### `helm-rollback`

Rollback a release.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| releaseName | string | Yes | Release name |
| namespace | string | No | Namespace |
| revision | integer | No | Revision to rollback to |
| timeout | string | No | Timeout |
| wait | boolean | No | Wait for resources |

---

### `helm-list`

List releases.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| namespace | string | No | Namespace filter |
| all | boolean | No | List all releases |
| filter | string | No | Name filter regex |

---

### `helm-status`

Get release status.

### `helm-history`

Get release history.

### `helm-repo-add`

Add a chart repository.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| name | string | Yes | Repository name |
| url | string | Yes | Repository URL |
| username | string | No | Username |
| password | string | No | Password |
| passCredentialsAll | boolean | No | Pass credentials to all requests |

---

### `helm-repo-list`

List repositories.

### `helm-repo-update`

Update repository indexes.

### `helm-repo-remove`

Remove a repository.

### `helm-search`

Search for charts.

### `helm-template`

Render chart templates.

### `helm-values`

Get release values.

### `helm-pull`

Download a chart.

## Usage Examples

```yaml
# Add repo and install
- type: helm-repo-add
  config:
    name: "bitnami"
    url: "https://charts.bitnami.com/bitnami"

- type: helm-install
  config:
    releaseName: "nginx"
    chart: "bitnami/nginx"
    namespace: "web"
    createNamespace: true
    values:
      replicaCount: 3
      service:
        type: "LoadBalancer"

# Upgrade with new values
- type: helm-upgrade
  config:
    releaseName: "nginx"
    chart: "bitnami/nginx"
    namespace: "web"
    values:
      replicaCount: 5
```

## License

MIT License - See [LICENSE](LICENSE) for details.