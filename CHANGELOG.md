# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

---

## [0.1.0] - 2026-03-08

### Added

#### Core Commands
- `axon init` — Initialize local knowledge base (SQLite + FTS5)
- `axon add <file|url>` — Ingest documents, web pages, PDFs with hybrid BM25+vector search
- `axon query <text>` — Hybrid search with RRF fusion, optional 2-stage reranking
- `axon list` — List all sources in the knowledge base
- `axon status` — Knowledge base health overview with embedding coverage stats
- `axon collection` — Create, list, delete knowledge collections

#### Search & Retrieval
- BM25 full-text search via SQLite FTS5
- PureGo TF-IDF vector embeddings (no API key required)
- OpenAI-compatible API embedding support (`AXON_EMBED_PROVIDER=api`)
- Reciprocal Rank Fusion (RRF) for hybrid BM25+vector results
- Token-overlap reranker (`--rerank`) for improved precision
- LLM-powered reranker (`--rerank-mode llm`)
- CJK tokenizer — unigram + bigram tokenization for Chinese/Japanese/Korean

#### Interfaces
- `axon tui` — Interactive terminal UI (Bubble Tea): real-time search, collection filter, preview, quit confirm
- `axon serve` — HTTP REST API server (`/v1/query`, `/v1/add`, `/v1/graph`, `/v1/watch` SSE) with built-in D3.js Web UI
- `axon mcp` — MCP server for Claude Desktop (tools: `memory_query`, `memory_add`, `memory_relate`, `memory_collections`, `memory_delete`, `memory_stats`)
- `axon graph` — ASCII terminal visualization of knowledge graph
- `axon config` — Persistent configuration via `~/.axon/config.toml` (`axon config init/set/show`)

#### Knowledge Graph
- `axon relate` — Build relationship graph (auto vector similarity, LLM triples)
- `axon relate --auto` — Auto-discover semantic relations via vector cosine similarity
- `axon relate --llm` — LLM-extracted subject-predicate-object triples with checkpoint/resume
- Wikilink relations from Obsidian `[[links]]` and Markdown `[text](url)` links
- `GET /v1/graph` — Graph API: nodes + edges JSON, BFS depth filtering

#### Import & Ingestion
- `axon vault <dir>` — Import Obsidian vault with `[[wikilink]]` relation extraction
- `axon import <dir>` — Batch import directory of files
- `axon import --notion <dir>` — Import Notion HTML/Markdown exports
- `axon add <url>` — URL ingestion with HTML → plaintext extraction
- PDF support via `github.com/ledongthuc/pdf` (pure Go)

#### Automation
- `axon watch <dir>` — File watcher with 2s debounce, daemon mode (`--daemon`), PID management
- `axon watch stop` / `axon watch status` — Daemon lifecycle management
- Logs at `~/.axon/watch.log`

#### Data Management
- `axon export` — Export to Markdown (per-source files), JSON bundle, JSONL, or Anki `.apkg`
- `axon dedupe` — Exact hash + near-duplicate (vector cosine) detection with dry-run mode
- `axon re-embed -m <model>` — Re-embed collection with a different model
- `axon sync` — Multi-device sync via WebDAV, S3-compatible, or local directory

#### AI Integration
- `axon chat` — RAG conversation mode with streaming output (requires `AXON_LLM_API_KEY`)
- LLM-assisted collection classification (`AXON_LLM_API_KEY` optional)
- `axon serve --key <secret>` — API key authentication for HTTP server

#### Models & Embedding
- `axon model list/download/rm/mirrors` — Model management
- Built-in PureGo TF-IDF embedder (zero dependencies, offline)
- ONNX embedder support via `-tags onnx` build (bge-small-zh-v1.5, bge-m3, e5-small-v2, etc.)
- Model mirror support: HuggingFace, hf-mirror.com (中国大陆), ModelScope
- `axon model download <name> --mirror hf-mirror` — Accelerated download for mainland China

#### Infrastructure
- `--db <path>` global flag — Multiple knowledge base support
- `axon upgrade` — Version check against GitHub releases
- `axon plugin` — External plugin system (stdin/stdout JSON-RPC protocol)
- GitHub Actions CI: build + vet + test on every push/PR
- GitHub Actions Release: 5-platform builds (macOS amd64/arm64, Linux amd64/arm64, Windows amd64)
- Makefile targets: `build`, `build-onnx`, `test`, `install`, `release`, `clean`

### Architecture

- **Storage**: SQLite with FTS5 full-text search + hand-written vector cosine similarity search
- **Embedding**: PureGo TF-IDF (default, offline), OpenAI-compatible API, ONNX local models (build tag)
- **Chunking**: Markdown heading-aware, paragraph, fixed-size strategies
- **Search**: BM25 + vector RRF fusion, optional token-overlap or LLM reranker
- **Relations**: `ref` (links), `similar` (vector), `semantic` (LLM triples), `cite` (blockquotes), `wikilink` (Obsidian)
- **Binary size**: ~13 MB (fts5 build), ~76 MB (onnx build with embedded model)
- **Dependencies**: minimal — Cobra, Bubble Tea/Lipgloss, go-sqlite3, ledongthuc/pdf

---

[0.1.0]: https://github.com/hsiaosiyuan0/axon/releases/tag/v0.1.0
