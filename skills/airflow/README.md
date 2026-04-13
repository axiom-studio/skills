# Apache Airflow Skill

Apache Airflow workflow orchestration for Atlas agents. Trigger DAGs, monitor runs, and manage tasks.

## Overview

The Airflow skill provides comprehensive node types for Apache Airflow workflow orchestration. It supports DAG triggering, run monitoring, task log retrieval, and DAG management.

## Node Types

### `airflow-dag-list`

List all DAGs.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| baseUrl | string | Yes | Airflow web URL |
| username | string | Yes | Username |
| password | string | Yes | Password |
| tags | array | No | Filter by tags |
| paused | boolean | No | Filter by paused status |

**Output:**

```json
{
  "dags": [
    {
      "dag_id": "etl_pipeline",
      "dag_display_name": "ETL Pipeline",
      "is_paused": false,
      "tags": ["etl", "production"],
      "schedule_interval": "@daily"
    }
  ]
}
```

### `airflow-dag-trigger`

Trigger a DAG run.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| baseUrl | string | Yes | Airflow web URL |
| username | string | Yes | Username |
| password | string | Yes | Password |
| dagId | string | Yes | DAG ID |
| conf | object | No | DAG run configuration |
| logicalDate | string | No | Logical date |

**Output:**

```json
{
  "success": true,
  "dagRunId": "manual__2024-01-15T10:30:00",
  "state": "queued",
  "message": "DAG run triggered successfully"
}
```

### `airflow-dag-status`

Get DAG run status.

### `airflow-dag-pause`

Pause/unpause a DAG.

### `airflow-dag-unpause`

Unpause a DAG.

### `airflow-task-list`

List tasks in a DAG.

### `airflow-task-logs`

Get task logs.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| baseUrl | string | Yes | Airflow web URL |
| username | string | Yes | Username |
| password | string | Yes | Password |
| dagId | string | Yes | DAG ID |
| dagRunId | string | Yes | DAG run ID |
| taskId | string | Yes | Task ID |
| tryNumber | integer | No | Try number |

### `airflow-run-list`

List DAG runs.

### `airflow-connection-list`

List connections.

## Authentication

Use Airflow web credentials or API token.

## Usage Examples

```yaml
# Trigger DAG
- type: airflow-dag-trigger
  config:
    baseUrl: "https://airflow.example.com"
    username: "atlas-bot"
    password: "{{secrets.airflow.password}}"
    dagId: "etl_pipeline"
    conf:
      environment: "production"
      load_date: "2024-01-15"

# Get task logs
- type: airflow-task-logs
  config:
    baseUrl: "https://airflow.example.com"
    username: "atlas-bot"
    password: "{{secrets.airflow.password}}"
    dagId: "etl_pipeline"
    dagRunId: "manual__2024-01-15"
    taskId: "extract"
```

## License

MIT License - See [LICENSE](LICENSE) for details.