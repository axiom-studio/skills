# MCP Tools Skill

Essential MCP (Model Context Protocol) tools for Axiom agents. This skill provides web browsing, screenshots, search, URL fetching, and memory capabilities.

## Tools Included

### Web Browsing (Puppeteer)
- `web_navigate` - Navigate to a URL in a headless browser
- `web_screenshot` - Take a screenshot of the current page
- `web_click` - Click an element on the page
- `web_type` - Type text into an input field

### Web Search (Brave)
- `web_search` - Search the web using Brave Search
- `web_local_search` - Search for local businesses and places

### Web Fetch
- `web_fetch` - Fetch content from a URL and convert to markdown

### Memory (Knowledge Graph)
- `memory_read` - Read the entire knowledge graph
- `memory_search` - Search for nodes in the knowledge graph
- `memory_add` - Add a new memory to the knowledge graph
- `memory_create_entities` - Create new entities
- `memory_create_relations` - Create relations between entities

## Installation

1. Add this skill repository to Axiom:
   ```
   Repository URL: https://github.com/axiom-studio/skills.skill-mcp
   ```

2. Install the skill from the marketplace

## Requirements

### Required
- **Node.js** and **npm/npx** - For Puppeteer and Brave Search tools

### Optional
- **Python** and **uvx** - For the Fetch tool
- **Brave Search API Key** - Required for `web_search` and `web_local_search`

## Configuration

### Brave Search API Key

For web search tools, you need a Brave Search API key:

1. Visit https://brave.com/search/api/
2. Sign up for a free account (2000 queries/month free tier)
3. Create an API key
4. Add the key to your Axiom Vault:
   - Go to **Vault → Add Credential**
   - Name: `brave_api_key`
   - Type: Generic Key-Value
   - Add key: `api_key` with your Brave API key

## Usage

Once installed, these tools are automatically available to AI agents. The agent can use them during reasoning to:

- Browse websites and take screenshots
- Search the web for current information
- Fetch and extract content from URLs
- Store and retrieve memories across sessions

## Examples

### Web Search
```json
{
  "tool": "web_search",
  "query": "latest developments in AI 2024",
  "count": 10
}
```

### Take Screenshot
```json
{
  "tool": "web_screenshot",
  "name": "homepage",
  "width": 1280,
  "height": 720
}
```

### Fetch URL Content
```json
{
  "tool": "web_fetch",
  "url": "https://example.com/article"
}
```

### Store Memory
```json
{
  "tool": "memory_add",
  "content": "User prefers dark mode in all applications"
}
```

## License

Apache-2.0