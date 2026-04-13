# Hugging Face Skill

Hugging Face model hub operations for Atlas agents. Access models, datasets, and Spaces for AI/ML workflows.

## Overview

The Hugging Face skill provides comprehensive node types for accessing the Hugging Face Hub. It supports model inference, model/dataset listing and downloading, and Spaces management.

## Node Types

### `hf-inference`

Run inference on a Hugging Face model.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| apiToken | string | Yes | Hugging Face API token |
| model | string | Yes | Model ID (e.g., gpt2, bert-base-uncased) |
| inputs | string | Yes | Input text/data |
| parameters | object | No | Model parameters |

**Output:**

```json
{
  "result": [
    {
      "generated_text": "The future of AI is bright and promising."
    }
  ]
}
```

### `hf-model-list`

List models from the Hub.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| apiToken | string | No | API token (optional for public models) |
| search | string | No | Search query |
| filter | string | No | Filter by task or library |
| sort | string | No | Sort by (downloads, likes, etc.) |

### `hf-model-info`

Get model details.

### `hf-model-download`

Download a model.

### `hf-dataset-list`

List datasets.

### `hf-dataset-download`

Download a dataset.

### `hf-space-create`

Create a Space.

### `hf-space-list`

List Spaces.

## Authentication

Get API token from Hugging Face Settings > Access Tokens.

## Usage Examples

```yaml
# Run inference
- type: hf-inference
  config:
    apiToken: "{{secrets.huggingface.apiToken}}"
    model: "gpt2"
    inputs: "The future of AI is"
    parameters:
      max_new_tokens: 50
      temperature: 0.7

# List models
- type: hf-model-list
  config:
    search: "text-classification"
    filter: "pytorch"
```

## License

MIT License - See [LICENSE](LICENSE) for details.