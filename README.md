# Axon

<p align="center">
  <img src="docs/logo.jpg" alt="Axon Logo" width="200" />
  <br/>
  <em>Your personal knowledge base and memory engine.</em>
  <br/>
  <em>Local-first · Single binary · Relationship-aware · AI-ready</em>
</p>

<p align="center">
  <a href="https://github.com/hsiaosiyuan0/axon/actions/workflows/ci.yml"><img src="https://github.com/hsiaosiyuan0/axon/actions/workflows/ci.yml/badge.svg" alt="CI"/></a>
  <a href="https://github.com/hsiaosiyuan0/axon/actions/workflows/release.yml"><img src="https://github.com/hsiaosiyuan0/axon/actions/workflows/release.yml/badge.svg" alt="Release"/></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/License-MIT-blue.svg" alt="License: MIT"/></a>
  <a href="https://go.dev"><img src="https://img.shields.io/badge/Go-1.22+-00ADD8?logo=go" alt="Go"/></a>
</p>

---

Axon is a CLI tool that turns your documents, notes, web pages, and code snippets into a **searchable, relationship-aware knowledge graph** — all stored locally in a single SQLite file.

```
axon add meeting-notes.md          # ingest a document
axon add https://go.dev/blog/...   # ingest a URL
axon add paper.pdf -c research     # ingest a PDF into "research" collection
axon query "API design patterns"   # hybrid BM25 + vector search
axon tui                           # interactive TUI
axon serve                         # start HTTP API + Web UI
axon mcp                           # MCP server for Claude Desktop
```

---

## Features

| Feature | Description |
|---------|-------------|
| **Collections** | Organize knowledge into themed groups (notes, research, code…) |
| **Hybrid Search** | BM25 full-text + vector embedding, fused with RRF |
| **Knowledge Graph** | Auto-detect `[[wikilinks]]`, semantic similarity, LLM-extracted relations |
| **Multi-format** | Markdown, URL, PDF, Obsidian vault, Notion export, code snippets |
| **AI-ready** | MCP server for Claude Desktop, HTTP REST API |
| **Web UI** | Built-in D3.js knowledge graph explorer (`axon serve` → `/ui`) |
| **TUI** | Real-time interactive search in the terminal |
| **Watch mode** | Auto-ingest file changes in background |
| **Local-first** | All data stays in `~/.axon/axon.db` — no cloud required |
| **Single binary** | Download and run — zero dependencies |
| **Multi-vault** | Use `--db` to switch between multiple knowledge bases |

---

## Quick Start

```bash
axon init
axon collection new
axon add README.md
axon add https://news.ycombinator.com -c tech
axon query "machine learning"
axon serve        # → http://localhost:7474/ui
```

---

## Documentation

- [Installation](docs/installation.md)
- [Usage](docs/usage.md) — commands, search, collections, export, watch mode
- [Integrations](docs/integrations.md) — Claude Desktop (MCP), HTTP REST API
- [Configuration](docs/configuration.md) — env vars, embedding models, LLM setup
- [Development](docs/development.md) — build, project structure, roadmap
- [Architecture](docs/architecture.md) — technical deep-dive

---

## License

MIT — see [LICENSE](LICENSE)

---

## Contributing

Issues and PRs welcome at [github.com/hsiaosiyuan0/axon](https://github.com/hsiaosiyuan0/axon)

> "Every connection matters, every memory counts."
