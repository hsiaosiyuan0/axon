# Axon — Technical Architecture

## 整体架构

```
┌─────────────────────────────────────────────────────┐
│                      界面层                          │
│           TUI (Bubble Tea)  │  MCP (stdio)           │
│                CLI (Cobra)                           │
└─────────────────────┬───────────────────────────────┘
                      │
┌─────────────────────▼───────────────────────────────┐
│                     核心服务层                        │
│   Ingest  │  Query  │  Relate  │  Collection Mgr     │
└─────────────────────┬───────────────────────────────┘
                      │
┌─────────────────────▼───────────────────────────────┐
│                     能力层                            │
│  Embedder  │  Chunker  │  RelParser  │  LLM Router   │
│  BM25(FTS5)│  Reranker │  Plugin Host                │
└─────────────────────┬───────────────────────────────┘
                      │
┌─────────────────────▼───────────────────────────────┐
│                     存储层                            │
│              SQLite + sqlite-vec                     │
│                ~/.axon/axon.db                       │
└─────────────────────────────────────────────────────┘
```

---

## 数据库 Schema

### `collections` — 知识库目录

```sql
CREATE TABLE collections (
    id              TEXT PRIMARY KEY,
    name            TEXT NOT NULL,
    type            TEXT,               -- diary|work|code|notes|custom
    description     TEXT,
    model_name      TEXT,               -- 绑定的 embedding 模型
    chunk_strategy  TEXT DEFAULT 'markdown',
    meta            JSON,
    created_at      DATETIME,
    updated_at      DATETIME
);
```

### `sources` — 原始数据（核心，永不丢失）

```sql
CREATE TABLE sources (
    id              TEXT PRIMARY KEY,
    collection      TEXT NOT NULL,

    -- 数据源
    source_type     TEXT NOT NULL,      -- file|url|snippet|plugin:xxx
    origin          TEXT NOT NULL,      -- 文件绝对路径 / URL / 自定义标识
    origin_hash     TEXT,               -- 内容 hash，用于检测变更

    -- 原始内容（永久保存）
    raw_content     BLOB,               -- 原始字节
    raw_encoding    TEXT DEFAULT 'utf-8',
    raw_mime        TEXT,               -- text/markdown|text/html|text/plain

    -- 提取后纯文本（永久保存，供切片用）
    plain_text      TEXT,

    -- 元数据
    title           TEXT,
    lang            TEXT,
    meta            JSON,               -- 按 source_type 不同内容不同

    created_at      DATETIME,
    updated_at      DATETIME,
    fetched_at      DATETIME
);
```

`meta` 示例：
```json
// file: {"size": 4096, "mtime": "...", "filename": "meeting.md"}
// url:  {"status_code": 200, "final_url": "https://..."}
// plugin:github-issue: {"repo": "owner/repo", "issue_number": 42}
```

### `chunks` — 知识片段（可从 sources 重建）

```sql
CREATE TABLE chunks (
    id              TEXT PRIMARY KEY,
    source_id       TEXT NOT NULL REFERENCES sources(id),
    collection      TEXT NOT NULL,

    content         TEXT NOT NULL,
    content_hash    TEXT,

    position        INTEGER,            -- 第几个 chunk
    char_start      INTEGER,            -- 在 plain_text 中起始位置
    char_end        INTEGER,
    section         TEXT,               -- 所属章节标题

    meta            JSON,
    created_at      DATETIME
);

-- FTS5 全文索引（内置 BM25）
CREATE VIRTUAL TABLE chunks_fts USING fts5(
    content,
    content='chunks',
    content_rowid='rowid'
);
```

### `embeddings` — 向量（可重算，独立存储）

```sql
CREATE TABLE embeddings (
    id              TEXT PRIMARY KEY,
    chunk_id        TEXT NOT NULL REFERENCES chunks(id),

    model_name      TEXT NOT NULL,      -- bge-m3|text-embedding-3-small
    model_version   TEXT,
    provider        TEXT,               -- local|openai|zhipu|local-go

    vector          BLOB NOT NULL,
    dim             INTEGER NOT NULL,

    created_at      DATETIME,

    UNIQUE(chunk_id, model_name)        -- 同一 chunk 可存多个模型向量
);
```

### `relations` — 知识关系图（支持跨 Collection）

```sql
CREATE TABLE relations (
    id              TEXT PRIMARY KEY,

    from_type       TEXT NOT NULL,      -- source|chunk
    from_id         TEXT NOT NULL,
    from_collection TEXT NOT NULL,

    to_type         TEXT NOT NULL,      -- source|chunk
    to_id           TEXT NOT NULL,
    to_collection   TEXT NOT NULL,      -- 跨 collection！

    rel_type        TEXT NOT NULL,      -- ref|cite|similar|parent|child|plugin:xxx
    weight          REAL DEFAULT 1.0,
    bidirectional   BOOLEAN DEFAULT FALSE,

    established_by  TEXT,               -- parser|vector-sim|llm|user|plugin:xxx
    evidence        TEXT,               -- 建立关系的证据文本

    meta            JSON,
    created_at      DATETIME,
    updated_at      DATETIME
);

CREATE INDEX idx_relations_from ON relations(from_type, from_id);
CREATE INDEX idx_relations_to   ON relations(to_type, to_id);
```

### `models` — 模型注册表

```sql
CREATE TABLE models (
    name            TEXT PRIMARY KEY,
    version         TEXT,
    provider        TEXT,               -- local-onnx|openai|zhipu|local-go
    dim             INTEGER,
    lang            TEXT,               -- zh|en|multilingual
    local_path      TEXT,
    api_config      JSON,
    is_available    BOOLEAN,
    created_at      DATETIME
);
```

### `re_embed_jobs` — 重新 Embedding 任务

```sql
CREATE TABLE re_embed_jobs (
    id              TEXT PRIMARY KEY,
    collection      TEXT,               -- NULL 表示整库
    old_model       TEXT,
    new_model       TEXT NOT NULL,
    status          TEXT DEFAULT 'pending',  -- pending|running|done|failed
    progress        INTEGER DEFAULT 0,
    total           INTEGER,
    error           TEXT,
    created_at      DATETIME,
    started_at      DATETIME,
    finished_at     DATETIME
);
```

---

## 核心接口定义

### Embedder

```go
type Embedder interface {
    Embed(ctx context.Context, texts []string) ([][]float32, error)
    Dim() int
    ModelName() string
}
// 实现：ONNXEmbedder | APIEmbedder | PureGoEmbedder
// 优先级：API Key配置 > ONNX本地 > PureGo兜底
```

### Source Plugin

```go
type SourcePlugin interface {
    Describe() PluginMeta
    Fetch(origin string, config map[string]any) (SourceData, error)
    HasChanged(origin string, lastHash string) (bool, error)
    ExtractRelations(content string) ([]RelationHint, error) // 可选
}

type SourceData struct {
    RawContent []byte
    RawMime    string
    PlainText  string
    Title      string
    Lang       string
    Meta       map[string]any
}
```

### Chunker

```go
type Chunker interface {
    Chunk(plain string, strategy ChunkStrategy) ([]Chunk, error)
}
// 策略：markdown(按标题) | paragraph | sentence | code(按函数) | fixed
```

### Reranker

```go
type Reranker interface {
    Rerank(ctx context.Context, query string, results []SearchResult) ([]SearchResult, error)
}
// 实现：CrossEncoderReranker | APIReranker | RRFReranker(默认)
```

---

## 混合检索流程

```
query
  ├─ BM25 (SQLite FTS5)  → top-20
  ├─ Vector Search        → top-20 (per collection, per model)
  └─ RRF Merge            → top-10
       └─ Reranker (可选) → top-5
```

跨 Collection 向量检索：
- 各 Collection 用各自模型分别检索
- 结果在 RRF 阶段统一合并

---

## 插件系统

```
内置插件（编译进二进制）：
  file     → origin: /abs/path/file.md
  url      → origin: https://...
  snippet  → origin: axon://snippet/<id>

外部插件（~/.axon/plugins/ 目录下可执行文件）：
  kb-plugin-github   → plugin:github-issue
  kb-plugin-notion   → plugin:notion
  kb-plugin-youtube  → plugin:youtube

通信协议：stdin/stdout JSON-RPC
```

---

## 项目目录结构

```
axon/
├── main.go
├── go.mod
├── go.sum
│
├── cmd/                        # CLI 命令 (Cobra)
│   ├── root.go
│   ├── init.go
│   ├── add.go
│   ├── query.go
│   ├── collection.go
│   ├── model.go
│   ├── re_embed.go
│   ├── relate.go
│   ├── tui.go
│   ├── mcp.go
│   └── plugin.go
│
├── internal/
│   ├── config/                 # 配置管理
│   │   └── config.go
│   ├── store/                  # SQLite 存储层
│   │   ├── db.go               # 连接、迁移
│   │   ├── collections.go
│   │   ├── sources.go
│   │   ├── chunks.go
│   │   ├── embeddings.go
│   │   ├── relations.go
│   │   └── models.go
│   ├── embed/                  # Embedding 实现
│   │   ├── embedder.go         # interface + factory
│   │   ├── onnx.go
│   │   ├── api.go
│   │   └── purego.go           # 纯 Go 兜底
│   ├── chunk/                  # 文档切片
│   │   ├── chunker.go
│   │   ├── markdown.go
│   │   ├── code.go
│   │   └── plain.go
│   ├── relate/                 # 关系提取与更新
│   │   ├── parser.go           # 解析 Markdown 链接
│   │   └── updater.go          # 文件变更时更新关系
│   ├── hybrid/                 # 混合检索
│   │   ├── search.go
│   │   └── rrf.go
│   ├── rerank/                 # 重排
│   │   ├── reranker.go
│   │   ├── crossencoder.go
│   │   └── api.go
│   ├── llm/                    # LLM 路由（分类/关系提取）
│   │   ├── router.go
│   │   ├── classify.go
│   │   └── extract.go
│   └── plugin/                 # 插件宿主
│       ├── host.go
│       ├── builtin_file.go
│       ├── builtin_url.go
│       └── builtin_snippet.go
│
├── tui/                        # Bubble Tea TUI
│   ├── app.go
│   ├── views/
│   │   ├── home.go
│   │   ├── search.go
│   │   ├── collection.go
│   │   └── relate.go
│   └── styles/
│       └── theme.go
│
├── mcp/                        # MCP Server
│   ├── server.go
│   └── tools.go
│
├── docs/
│   ├── product.md
│   └── architecture.md         # 本文件
│
└── models/                     # 内置 PureGo 模型数据
    └── registry.yaml
```

---

## 开发阶段规划

### Phase 1 — 主链路跑通
- [ ] SQLite 存储层（schema + CRUD）
- [ ] API Embedding（OpenAI 兼容）
- [ ] 文件插件（file）
- [ ] Markdown 切片
- [ ] 基础 CLI：`init` / `add` / `query`
- [ ] FTS5 BM25 + 向量混合检索（RRF）

### Phase 2 — 完整功能
- [ ] Collection 管理（含模型选择）
- [ ] 关系图提取 & 更新
- [ ] LLM 辅助分类
- [ ] ONNX 本地 Embedding
- [ ] URL 插件
- [ ] Re-embed 功能

### Phase 3 — 体验打磨
- [ ] TUI（Bubble Tea）
- [ ] MCP Server
- [ ] 纯 Go 兜底 Embedding
- [ ] Reranker 支持
- [ ] 外部插件系统
- [ ] 跨平台打包 & 分发
