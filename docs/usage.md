# Usage

## Commands

```
axon init                          Initialize knowledge base
axon add <file|url>                Add a document (auto-classify)
axon add <file> -c <collection>    Add to specific collection
axon query <text>                  Hybrid search
axon query <text> --rerank         Search with 2-stage reranking
axon tui                           Interactive TUI search
axon serve                         HTTP REST API + Web UI
axon mcp                           MCP server (for Claude Desktop)

axon collection list               List collections
axon collection new                Create collection (interactive)
axon collection rm <id>            Delete collection

axon import <dir>                  Import directory of files
axon import --notion <dir>         Import Notion export
axon vault <obsidian-dir>          Import Obsidian vault (with [[wikilinks]])

axon relate                        Build relationship graph
axon relate --auto                 Auto-discover semantic relations
axon relate --llm                  LLM-extracted semantic triples
axon graph                         Visualize graph in terminal

axon watch <dir>                   Watch directory for changes
axon status                        Knowledge base health overview
axon export                        Export to Markdown / JSON / JSONL / Anki
axon dedupe                        Detect and remove duplicate content

axon re-embed -m <model>           Re-embed with new model
axon model list                    List embedding models
axon upgrade                       Check for new version
```

### Global flags

```
axon --db ~/work.db query "..."    Use a different knowledge base
axon --db ~/research.db add ...    Switch vaults on the fly
```

---

## Collections

Collections organize your knowledge into themed groups. Axon auto-classifies documents using LLM (if configured) or lets you choose.

```bash
axon collection new         # interactive creation
axon collection list        # show all collections
axon query "..." -c notes   # search within a collection
```

Built-in collection types:

| Type | Best for | Chunking |
|------|----------|---------|
| `notes` | Study notes, reading notes | By heading |
| `diary` | Journal entries | By date/paragraph |
| `work` | Meeting notes, PRDs | By heading |
| `code` | Code snippets, scripts | By function/block |
| `custom` | Custom — configurable | Configurable |

---

## Search

Axon uses **hybrid search**: BM25 full-text search fused with vector similarity via Reciprocal Rank Fusion (RRF).

```bash
# Basic search
axon query "Go channels and goroutines"

# Limit to collection
axon query "transformer attention" -c research

# With 2-stage reranking (improves precision)
axon query "API design" --rerank

# LLM-powered reranking
axon query "system design" --rerank --rerank-mode llm

# Get more results
axon query "..." -n 10
```

BM25 search automatically handles Chinese, Japanese, and Korean text using unigram + bigram tokenization.

---

## Knowledge Relationships

Axon builds a knowledge graph with multiple relation types:

| Type | How it's built |
|------|---------------|
| `ref` | Markdown `[link](...)` / Obsidian `[[wikilink]]` |
| `similar` | Vector cosine similarity > threshold |
| `semantic` | LLM-extracted subject-predicate-object triples |
| `cite` | Blockquote / citation patterns |

```bash
axon relate --auto --threshold 0.85   # auto-discover via vector similarity
axon relate --llm --source <id>       # LLM-extract rich semantic triples
axon graph                            # visualize in terminal
axon serve                            # full graph in browser → http://localhost:7474/ui
```

---

## Supported Formats

| Format | Command |
|--------|---------|
| Markdown (`.md`) | `axon add notes.md` |
| Plain text (`.txt`) | `axon add log.txt` |
| PDF (`.pdf`) | `axon add paper.pdf` |
| URL | `axon add https://...` |
| Code snippets | `axon add snippet.go` |
| Obsidian vault | `axon vault ~/vault/` |
| Notion export | `axon import --notion ~/notion-export/` |
| Directory | `axon import ~/docs/` |

---

## Multiple Knowledge Bases

Use `--db` to maintain separate knowledge bases for different projects:

```bash
axon --db ~/work.db init
axon --db ~/work.db add meeting.md -c planning
axon --db ~/work.db query "Q3 goals"

AXON_DB=~/research.db axon query "transformer"
```

---

## Watch Mode

Auto-ingest files as they change:

```bash
axon watch ~/notes/                        # watch a directory
axon watch ~/notes/ ~/docs/ --ext .md     # multiple dirs, filter by extension
axon watch ~/notes/ --interval 1s -v      # verbose, 1s polling
```

---

## Export

```bash
axon export -o ~/backup/                  # Markdown (one file per source)
axon export -f json -o axon-backup.json   # JSON bundle
axon export -f jsonl -o axon-backup.jsonl # JSONL (streaming)
axon export -f anki -o axon-cards.apkg   # Anki flashcard deck
```

Anki export creates one flashcard per knowledge chunk:
- **Front**: Section heading or first sentence
- **Back**: Full chunk content + source attribution
- **Tags**: `axon`, collection name

Import in Anki: **File → Import → select `.apkg`**

---

## Deduplication

```bash
axon dedupe                        # preview duplicates (dry-run)
axon dedupe --threshold 0.95       # near-duplicate detection
axon dedupe --confirm              # actually remove duplicates
axon dedupe --exact-only --confirm # exact hash match only (faster)
```
