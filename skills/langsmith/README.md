# LangSmith Skill

LangSmith LLM observability operations for Atlas agents. Trace LLM apps, manage projects, create datasets, and run evaluations.

## Overview

The LangSmith skill provides comprehensive node types for LangSmith LLM observability. It supports trace inspection, project management, dataset creation, evaluation runs, and feedback management.

## Node Types

### `langsmith-trace-list`

List traces/runs.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| apiKey | string | Yes | LangSmith API key |
| projectId | string | No | Filter by project |
| traceId | string | No | Filter by trace ID |
| limit | integer | No | Max results |

**Output:**

```json
{
  "traces": [
    {
      "id": "abc123",
      "name": "LLM Chain",
      "run_type": "chain",
      "start_time": "2024-01-15T10:30:00.000Z",
      "end_time": "2024-01-15T10:30:02.000Z",
      "status": "success"
    }
  ]
}
```

### `langsmith-trace-get`

Get trace details.

### `langsmith-project-create`

Create a project.

### `langsmith-project-list`

List projects.

### `langsmith-dataset-create`

Create a dataset.

### `langsmith-dataset-list`

List datasets.

### `langsmith-run-eval`

Run an evaluation.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| apiKey | string | Yes | LangSmith API key |
| datasetId | string | Yes | Dataset ID |
| evaluator | string | Yes | Evaluator configuration |
| llmOrChain | object | Yes | LLM or chain to evaluate |

### `langsmith-feedback-create`

Create feedback.

### `langsmith-feedback-list`

List feedback.

## Authentication

Get API key from LangSmith Settings > API Keys.

## Usage Examples

```yaml
# List traces
- type: langsmith-trace-list
  config:
    apiKey: "{{secrets.langsmith.apiKey}}"
    limit: 100

# Run evaluation
- type: langsmith-run-eval
  config:
    apiKey: "{{secrets.langsmith.apiKey}}"
    datasetId: "dataset-123"
    evaluator:
      name: "accuracy"
```

## License

MIT License - See [LICENSE](LICENSE) for details.