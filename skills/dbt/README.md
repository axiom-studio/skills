# dbt Skill

dbt data transformation operations for Atlas agents. Run models, tests, generate docs, and manage snapshots.

## Overview

The dbt skill provides comprehensive node types for dbt (data build tool) operations. It supports model execution, testing, documentation generation, seeding, and snapshot management.

## Node Types

### `dbt-run`

Run dbt models.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| projectDir | string | Yes | dbt project directory |
| profilesDir | string | No | Profiles directory |
| profile | string | No | Profile name |
| target | string | No | Target environment |
| select | array | No | Select models |
| exclude | array | No | Exclude models |
| tags | array | No | Filter by tags |
| fullRefresh | boolean | No | Full refresh |

**Output:**

```json
{
  "success": true,
  "results": [
    {
      "name": "staging_orders",
      "status": "success",
      "executionTime": 2.5,
      "rowsAffected": 10000
    }
  ],
  "elapsedTime": 15.3
}
```

### `dbt-test`

Run dbt tests.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| projectDir | string | Yes | dbt project directory |
| select | array | No | Select tests |
| exclude | array | No | Exclude tests |
| severity | string | No | Severity threshold |

### `dbt-compile`

Compile dbt models.

### `dbt-docs-generate`

Generate documentation.

### `dbt-seed`

Load seed files.

### `dbt-snapshot`

Run snapshots.

### `dbt-debug`

Run debug command.

### `dbt-list`

List resources.

## Usage Examples

```yaml
# Run models
- type: dbt-run
  config:
    projectDir: "/app/dbt"
    profile: "prod"
    target: "production"
    select: ["tag:daily", "model.marts.*"]

# Run tests
- type: dbt-test
  config:
    projectDir: "/app/dbt"
    profile: "prod"
    select: ["tag:critical"]

# Generate docs
- type: dbt-docs-generate
  config:
    projectDir: "/app/dbt"
```

## License

MIT License - See [LICENSE](LICENSE) for details.