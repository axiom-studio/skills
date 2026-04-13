# Temporal Skill

Temporal workflow orchestration for Atlas agents. Manage durable workflows, signals, queries, and execution history.

## Overview

The Temporal skill provides comprehensive node types for Temporal workflow orchestration. It supports workflow execution, signaling, querying, cancellation, and history inspection.

## Node Types

### `temporal-workflow-start`

Start a new workflow execution.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| address | string | Yes | Temporal server address |
| namespace | string | No | Namespace (default: default) |
| workflowId | string | Yes | Workflow ID |
| workflowType | string | Yes | Workflow type name |
| taskQueue | string | Yes | Task queue name |
| input | array | No | Workflow input arguments |
| runTimeout | string | No | Run timeout |
| workflowTimeout | string | No | Workflow timeout |
| taskTimeout | string | No | Task timeout |
| retryPolicy | object | No | Retry policy configuration |

**Output:**

```json
{
  "success": true,
  "workflowId": "my-workflow-123",
  "runId": "abc123-def456",
  "message": "Workflow started successfully"
}
```

---

### `temporal-workflow-list`

List workflow executions.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| address | string | Yes | Temporal server address |
| namespace | string | No | Namespace |
| query | string | No | SQL-like query filter |
| pageSize | integer | No | Page size |

**Output:**

```json
{
  "executions": [
    {
      "workflowId": "my-workflow-123",
      "runId": "abc123",
      "type": "OrderProcessing",
      "startTime": "2024-01-15T10:30:00.000Z",
      "status": "Running"
    }
  ]
}
```

---

### `temporal-workflow-get`

Get workflow execution details.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| address | string | Yes | Temporal server address |
| namespace | string | No | Namespace |
| workflowId | string | Yes | Workflow ID |
| runId | string | No | Run ID |

---

### `temporal-workflow-cancel`

Cancel a workflow execution.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| address | string | Yes | Temporal server address |
| namespace | string | No | Namespace |
| workflowId | string | Yes | Workflow ID |
| runId | string | No | Run ID |

---

### `temporal-workflow-terminate`

Terminate a workflow execution.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| address | string | Yes | Temporal server address |
| namespace | string | No | Namespace |
| workflowId | string | Yes | Workflow ID |
| runId | string | No | Run ID |
| reason | string | No | Termination reason |

---

### `temporal-signal-send`

Send a signal to a workflow.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| address | string | Yes | Temporal server address |
| namespace | string | No | Namespace |
| workflowId | string | Yes | Workflow ID |
| runId | string | No | Run ID |
| signalName | string | Yes | Signal name |
| signalData | any | No | Signal data |

---

### `temporal-query`

Query a workflow state.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| address | string | Yes | Temporal server address |
| namespace | string | No | Namespace |
| workflowId | string | Yes | Workflow ID |
| runId | string | No | Run ID |
| queryType | string | Yes | Query type (e.g., __stack_trace) |
| queryArgs | array | No | Query arguments |

---

### `temporal-search-attributes`

Get search attributes.

## Authentication

Supports mTLS and API key authentication.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| clientCert | string | No | Client certificate |
| clientKey | string | No | Client key |
| apiKey | string | No | API key |

## Usage Examples

```yaml
# Start workflow
- type: temporal-workflow-start
  config:
    address: "temporal.example.com:7233"
    namespace: "production"
    workflowId: "order-123"
    workflowType: "OrderProcessing"
    taskQueue: "order-processing"
    input:
      - orderId: "123"
        amount: 99.99

# Send signal
- type: temporal-signal-send
  config:
    address: "temporal.example.com:7233"
    workflowId: "order-123"
    signalName: "cancel"
    signalData:
      reason: "customer_request"

# Query workflow
- type: temporal-query
  config:
    address: "temporal.example.com:7233"
    workflowId: "order-123"
    queryType: "status"
```

## License

MIT License - See [LICENSE](LICENSE) for details.