# Replicate Skill

Replicate ML model hosting operations for Atlas agents. Run models, manage predictions, and create trainings.

## Overview

The Replicate skill provides comprehensive node types for Replicate ML model platform. It supports model execution, prediction management, and training job creation.

## Node Types

### `replicate-run`

Run a model prediction.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| apiToken | string | Yes | Replicate API token |
| model | string | Yes | Model name (owner/name) |
| version | string | No | Model version |
| input | object | Yes | Model input parameters |
| wait | boolean | No | Wait for completion |

**Output:**

```json
{
  "success": true,
  "predictionId": "abc123",
  "status": "succeeded",
  "output": ["result1", "result2"],
  "metrics": {"predictTime": 2.5}
}
```

### `replicate-prediction-status`

Get prediction status.

### `replicate-prediction-cancel`

Cancel a prediction.

### `replicate-model-list`

List models.

### `replicate-model-get`

Get model details.

### `replicate-training-create`

Create a training job.

### `replicate-training-status`

Get training status.

## Authentication

Get API token from Replicate Settings > API Tokens.

## Usage Examples

```yaml
# Run model
- type: replicate-run
  config:
    apiToken: "{{secrets.replicate.apiToken}}"
    model: "stability-ai/sdxl"
    input:
      prompt: "A futuristic city at sunset"
      width: 1024
      height: 1024
      num_outputs: 1
    wait: true

# Check prediction status
- type: replicate-prediction-status
  config:
    apiToken: "{{secrets.replicate.apiToken}}"
    predictionId: "abc123"
```

## License

MIT License - See [LICENSE](LICENSE) for details.