# Cohere Skill

Cohere AI language model operations for Atlas agents. Generate text, create embeddings, classify, summarize, and more.

## Overview

The Cohere skill provides comprehensive node types for Cohere's AI language models. It supports text generation, embeddings, classification, summarization, language detection, and tokenization.

## Node Types

### `cohere-generate`

Generate text using Cohere models.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| apiKey | string | Yes | Cohere API key |
| model | string | No | Model name (default: command) |
| prompt | string | Yes | Input prompt |
| maxTokens | integer | No | Max tokens to generate |
| temperature | number | No | Sampling temperature |
| k | integer | No | Top-k sampling |
| p | number | No | Top-p sampling |
| stopSequences | array | No | Stop sequences |

**Output:**

```json
{
  "generations": [
    {
      "text": "The future of AI looks promising with advances in...",
      "finishReason": "COMPLETE"
    }
  ],
  "tokenCount": {"promptTokens": 10, "responseTokens": 50}
}
```

### `cohere-chat`

Chat with a Cohere model.

### `cohere-embed`

Create text embeddings.

**Configuration:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| apiKey | string | Yes | Cohere API key |
| texts | array | Yes | Texts to embed |
| model | string | No | Embedding model |
| truncate | string | No | Truncate strategy |

### `cohere-classify`

Classify text.

### `cohere-summarize`

Summarize text.

### `cohere-rerank`

Rerank documents.

### `cohere-detect-language`

Detect text language.

### `cohere-tokenize`

Tokenize text.

## Authentication

Get API key from Cohere Dashboard.

## Usage Examples

```yaml
# Generate text
- type: cohere-generate
  config:
    apiKey: "{{secrets.cohere.apiKey}}"
    model: "command"
    prompt: "Write a product description for a smart watch"
    maxTokens: 200
    temperature: 0.7

# Create embeddings
- type: cohere-embed
  config:
    apiKey: "{{secrets.cohere.apiKey}}"
    texts:
      - "Document 1 content"
      - "Document 2 content"
    model: "embed-english-v3.0"

# Summarize
- type: cohere-summarize
  config:
    apiKey: "{{secrets.cohere.apiKey}}"
    text: "Long article text here..."
```

## License

MIT License - See [LICENSE](LICENSE) for details.