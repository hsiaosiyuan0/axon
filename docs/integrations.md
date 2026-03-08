# Integrations

## Claude Code CLI (MCP)

Axon exposes an MCP server over stdio, letting Claude Code use your local knowledge base as a memory tool during conversations.

### Available tools

| Tool | Description |
|---|---|
| `memory_query` | Hybrid BM25 + vector search across the knowledge base |
| `memory_add` | Add a text snippet to the knowledge base |
| `memory_collections` | List all collections |
| `memory_relate` | Get relations for a knowledge chunk |
| `memory_delete` | Delete a source by its original file path or URL |
| `memory_stats` | Return source / chunk / collection counts |

### Setup

**1. Initialise the knowledge base (first time only):**

```bash
axon init
```

**2. Register axon as an MCP server:**

```bash
# Available to you across all projects
claude mcp add --transport stdio --scope user axon -- axon mcp
```

> If `axon` is not on the PATH that Claude Code sees, use the full path:
> ```bash
> claude mcp add --transport stdio --scope user axon -- /usr/local/bin/axon mcp
> ```

**3. Verify:**

```bash
claude mcp list
# axon    stdio    axon mcp
```

### Share config with your team

Commit a `.mcp.json` at the project root so the whole team gets the same setup:

```bash
claude mcp add --transport stdio --scope project axon -- axon mcp
git add .mcp.json && git commit -m "chore: add axon MCP server config"
```

The generated `.mcp.json`:

```json
{
  "mcpServers": {
    "axon": {
      "type": "stdio",
      "command": "axon",
      "args": ["mcp"]
    }
  }
}
```

> Claude Code prompts each team member to approve project-scoped servers on first use.

### Use a project-specific knowledge base

Point axon at a different database with `--db` or `AXON_DB`:

```bash
claude mcp add --transport stdio --scope project axon-proj \
  -- axon --db /path/to/project.db mcp
```

Or via environment variable in `.mcp.json`:

```json
{
  "mcpServers": {
    "axon": {
      "type": "stdio",
      "command": "axon",
      "args": ["mcp"],
      "env": { "AXON_DB": "/path/to/project.db" }
    }
  }
}
```

### Example usage

Once connected, Claude Code calls the tools automatically. You can also direct it:

```
Search my knowledge base for anything about the authentication refactor.
```

```
Remember this decision: we chose JWT over sessions because the API is stateless.
```

```
What collections do I have in axon?
```

### Remove the server

```bash
claude mcp remove axon
```

---

## Claude Desktop (MCP)

Add to `~/.config/claude/claude_desktop_config.json`:

```json
{
  "mcpServers": {
    "axon": {
      "command": "/usr/local/bin/axon",
      "args": ["mcp"]
    }
  }
}
```

Available MCP tools: `memory_query`, `memory_add`, `memory_relate`, `memory_collections`, `memory_delete`, `memory_stats`

---

## HTTP REST API

```bash
axon serve                           # default: http://localhost:7474
axon serve --addr :8080 --key mysecret
```

### Endpoints

```
GET  /health
GET  /v1/status
GET  /v1/collections
GET  /v1/query?q=...&collection=...&limit=5
POST /v1/query        # JSON body
POST /v1/add          # add file/URL/snippet
GET  /v1/graph        # knowledge graph JSON
GET  /v1/watch        # Server-Sent Events stream
GET  /ui              # Web UI (D3.js graph explorer)
```

### Examples

```bash
# Query
curl "http://localhost:7474/v1/query?q=Go+concurrency&limit=3"

# Add
curl -X POST http://localhost:7474/v1/add \
  -H 'Content-Type: application/json' \
  -d '{"origin": "https://go.dev/blog/pipelines", "collection": "go"}'
```
