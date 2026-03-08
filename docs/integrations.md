# Integrations

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
