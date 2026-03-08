# Development

## Build

```bash
make build      # build with version injected → ./axon
make install    # copy to /usr/local/bin/axon
make test       # run tests
```

## Project structure

```
axon/
├── cmd/              Cobra CLI commands
├── internal/
│   ├── store/        SQLite repositories
│   ├── ingest/       Ingestion pipeline (fetch → chunk → embed → relate)
│   ├── chunk/        Markdown / Paragraph / Fixed chunkers
│   ├── embed/        Embedder interface (API / ONNX / PureGo)
│   ├── hybrid/       BM25 + vector RRF fusion
│   ├── rerank/       Token overlap & LLM rerankers
│   ├── relate/       LLM triple extraction + progress
│   ├── plugin/       File / URL / PDF / Notion plugins
│   ├── obsidian/     Obsidian vault parser + wikilink resolver
│   ├── classify/     LLM collection classifier
│   ├── watch/        File system watcher (polling)
│   ├── graph/        Knowledge graph builder
│   ├── api/          HTTP REST API server
│   ├── ui/           Embedded Web UI (D3.js)
│   ├── anki/         Anki .apkg export
│   ├── dedupe/       Duplicate detection
│   └── tokenize/     CJK tokenizer for BM25
├── mcp/              MCP server (stdio JSON-RPC)
└── models/           Embedding model registry YAML
```

Data directory:

```
~/.axon/
├── axon.db           SQLite (FTS5 + embeddings + relations)
└── models/           Local ONNX models (optional)
```

## Git commit messages

All commit messages **must** follow [Conventional Commits v1.0.0](https://www.conventionalcommits.org/en/v1.0.0/) and be written in **English**.

### Format

```
<type>[optional scope]: <description>

[optional body]

[optional footer(s)]
```

### Types

| Type | When to use |
|---|---|
| `feat` | A new feature visible to users |
| `fix` | A bug fix |
| `docs` | Documentation only changes |
| `refactor` | Code change that neither fixes a bug nor adds a feature |
| `test` | Adding or updating tests |
| `chore` | Build process, dependency updates, tooling |
| `perf` | Performance improvement |
| `ci` | CI/CD configuration changes |

### Rules

- **Description**: lowercase, imperative mood, no trailing period — `add foo`, not `Added foo.`
- **Scope** (optional): the package or area affected, e.g. `feat(classify):`, `fix(ingest):`
- **Breaking changes**: append `!` after the type/scope, e.g. `feat!:` or `feat(api)!:`, and add a `BREAKING CHANGE:` footer
- **Body**: wrap at 72 characters; explain *why*, not *what*
- **One logical change per commit** — do not bundle unrelated changes

### Examples

```
feat(classify): add NLI cross-encoder provider for local classification

Adds nli-deberta-v3-small as an ONNX-based zero-shot classifier (~80%
accuracy) so users can classify collections without a remote LLM API key.
The model is downloaded automatically on first use.
```

```
fix(ingest): require user confirmation before auto-assigning collection
```

```
chore(makefile): add build-onnx-dev target for faster iterative builds
```

```
feat!: rename config key embed.provider to embed.backend

BREAKING CHANGE: existing config files using embed.provider must be
updated to embed.backend. Run `axon config set embed.backend <value>`.
```

## Adding a new command

1. Create `cmd/mycommand.go` with a `myCmd *cobra.Command`
2. Register in `cmd/root.go`: `rootCmd.AddCommand(myCmd)`
3. Use `config.Load(globalDB)` to get config (respects `--db` flag)

## Integrating with Claude Code CLI

Axon exposes a [Model Context Protocol (MCP)](https://modelcontextprotocol.io/) server over stdio, which lets Claude Code use your local knowledge base as a memory tool during conversations.

### Available MCP tools

| Tool | Description |
|---|---|
| `memory_query` | Hybrid BM25 + vector search across the knowledge base |
| `memory_add` | Add a text snippet to the knowledge base |
| `memory_collections` | List all collections |
| `memory_relate` | Get relations for a knowledge chunk |
| `memory_delete` | Delete a source by its original file path or URL |
| `memory_stats` | Return source / chunk / collection counts |

### Setup

**Step 1 — Install axon and ensure it is on your PATH:**

```bash
# Verify
axon --version
which axon   # must return a path, e.g. /usr/local/bin/axon
```

**Step 2 — Initialise the knowledge base (first time only):**

```bash
axon init
```

**Step 3 — Register axon as an MCP server in Claude Code:**

```bash
# Available to you across all projects
claude mcp add --transport stdio --scope user axon -- axon mcp
```

> If `axon` is not on the system PATH that Claude Code sees, use the absolute path:
> ```bash
> claude mcp add --transport stdio --scope user axon -- /usr/local/bin/axon mcp
> ```

**Step 4 — Verify the server is recognised:**

```bash
claude mcp list
# axon    stdio    axon mcp
```

### Sharing the config with your team

To share the MCP server configuration with everyone in a repository, add it to `.mcp.json` at the project root and commit it:

```bash
claude mcp add --transport stdio --scope project axon -- axon mcp
# creates / updates .mcp.json
git add .mcp.json && git commit -m "chore: add axon MCP server config"
```

The resulting `.mcp.json` looks like:

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

### Targeting a specific knowledge base

By default axon uses `~/.axon/axon.db`. Pass `--db` to point at a different database — useful when you maintain per-project knowledge bases:

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

### Example usage in Claude Code

Once the server is running, Claude Code calls the tools automatically. You can also direct it explicitly:

```
Search my knowledge base for anything about the authentication refactor.
```

```
Remember this decision: we chose JWT over sessions because the API is stateless.
```

```
What collections do I have in axon?
```

### Removing the server

```bash
claude mcp remove axon
```

## Roadmap

- [x] Phase 1 — Core: init, add, query, collections, BM25+vector hybrid
- [x] Phase 2 — URL ingestion, HTML→text, MCP server
- [x] Phase 3 — TUI, re-embed, GitHub Actions CI/CD
- [x] Phase 4 — LLM classification, semantic relations, Notion import
- [x] Phase 5 — Watch mode (file system sync)
- [x] Phase 6 — HTTP API, status, export (MD/JSON/JSONL)
- [x] Phase 7 — API auth, Obsidian vault, LLM triples, reranker
- [x] Phase 8 — Graph API, ASCII graph, CJK tokenizer, SSE watch
- [x] Phase 9 — Web UI (D3.js), LLM resume/checkpoint, MCP rerank, dedupe
- [x] Phase 10 — PDF support, multi-vault (`--db`), `axon upgrade`, Anki export
- [x] Phase 11 — ONNX local embeddings (bge-small-zh embedded in binary, model registry, mirror support)
