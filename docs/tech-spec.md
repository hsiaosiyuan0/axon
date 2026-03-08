# Axon — Technical Specification & Development Plan

> 状态：Phase 1-4 已完成 ✅ | 本文档定义 Phase 5+ 方向
> 更新：2026-03-07

---

## 现状总结（Phase 1–4 已完成）

| Phase | 内容 | 状态 |
|-------|------|------|
| 1 | 主链路：SQLite 存储、CLI 骨架、FTS5 BM25、PureGo Embedding | ✅ |
| 2 | HTML 提取、URL 插件、向量搜索、关系图、MCP Server | ✅ |
| 3 | TUI（Bubble Tea）、re-embed、Collection 名称解析、GitHub Actions 跨平台打包 | ✅ |
| 4 | ONNX fallback 优化、Notion 导入、LLM 分类增强（ClassifyResult + SuggestNew）| ✅ |

当前产物：单二进制 `axon`，~11MB，支持 darwin/linux amd64/arm64 + windows/amd64。

---

## 技术债 & 已知缺陷

| 问题 | 文件 | 优先级 |
|------|------|--------|
| `store.Store` 接口与实际 `store.DB` 结构体不匹配（两套 API 并存） | `internal/store/` | 🔴 高 |
| `ingest/service.go` 仍引用旧 `store.Documents()`（未迁移到新 store 层） | `internal/ingest/service.go` | 🔴 高 |
| `embed.Embedder.Embed()` 签名不一致（单文本 vs 批量） | `internal/embed/` | 🟡 中 |
| `axon relate --auto` O(n²) 全量扫描，KB 超 10k chunks 时会卡 | `cmd/relate.go` | 🟡 中 |
| MCP Server 只有 4 个工具，缺少 `memory_delete` / `memory_update` | `mcp/server.go` | 🟢 低 |
| TUI 搜索无 debounce，每次按键都触发全量检索 | `cmd/tui.go` | 🟢 低 |

---

## Phase 5 — 稳定性 & 存储统一

**目标：** 消除技术债，统一内部 API，为 Phase 6 功能扩展打好基础。

### 5.1 存储层统一（Store Interface Cleanup）

**问题：** 项目演进中出现两套存储 API：
- 旧版：`service.go` 中调用 `s.db.Documents().Create()`（`documents` 表）
- 新版：`cmd/add.go` 调用 `svc.Add()` → 写 `sources` + `chunks` 表

**方案：**
- 移除 `internal/store/documents.go`（旧 documents 表及接口）
- `store.Store` 接口统一暴露：`Sources()` / `Chunks()` / `Embeddings()` / `Collections()` / `Relations()` / `Models()`
- `ingest.Service` 全面迁移到新 store API
- 添加迁移脚本：旧 `documents` 表数据迁移到 `sources` + `chunks`

**接口定义：**
```go
type Store interface {
    Sources()     SourceRepo
    Chunks()      ChunkRepo
    Embeddings()  EmbeddingRepo
    Collections() CollectionRepo
    Relations()   RelationRepo
    Models()      ModelRepo
    Close() error
}
```

**文件变更：**
- `internal/store/store.go` — 统一 Store 接口
- `internal/store/documents.go` — 删除
- `internal/ingest/service.go` — 迁移调用
- `internal/store/migrate.go` — 新增迁移工具

### 5.2 Embedder 接口统一

**问题：** 两处 `Embed` 签名不同：
- `embed.Embedder.Embed(text string) ([]float32, error)` — 旧单文本接口
- `embed.Embedder.Embed(ctx, []string) ([][]float32, error)` — 新批量接口

**方案：** 统一为批量接口，旧单文本调用包装为单元素 slice。

```go
type Embedder interface {
    Embed(ctx context.Context, texts []string) ([][]float32, error)
    Dims() int
    ModelName() string
    Provider() string
}
```

### 5.3 TUI Debounce

- 搜索输入 150ms debounce，避免逐字触发检索
- 使用 `time.AfterFunc` + cancel channel 实现

### 5.4 MCP 工具补全

新增工具：
| Tool | 参数 | 描述 |
|------|------|------|
| `memory_delete` | `source_id` | 删除一条记录及其 chunks/embeddings |
| `memory_update` | `source_id`, `text` | 更新内容并重新 embed |
| `memory_stats` | — | 返回 KB 统计（sources/chunks/collections 数量） |

**交付物：**
- [ ] Store 接口统一 + 迁移脚本
- [ ] Embedder 接口统一
- [ ] TUI debounce
- [ ] MCP 工具补全（3 个）
- [ ] 全量编译 + 基础测试

---

## Phase 6 — ONNX 本地模型真正集成

**目标：** 无需 API Key，本地高质量 Embedding。

### 6.1 ONNX Runtime 集成

**技术选型：**
- [`github.com/yalue/onnxruntime_go`](https://github.com/yalue/onnxruntime_go) — 纯 Go ONNX Runtime 绑定
- 预打包 `libonnxruntime` 动态库（darwin/linux amd64/arm64）
- 构建 tag：`--tags onnx`，不影响默认无 CGO 构建

**支持模型：**
| 模型 | 维度 | 大小 | 语言 | 优先级 |
|------|------|------|------|--------|
| `bge-small-en-v1.5` | 384 | 133MB | 英文 | 🔴 P0 |
| `bge-small-zh-v1.5` | 512 | 93MB | 中文 | 🔴 P0 |
| `bge-m3` | 1024 | 570MB | 多语言 | 🟡 P1 |

**模型下载：**
```
axon model download bge-small-zh-v1.5
→ 下载到 ~/.axon/models/bge-small-zh-v1.5.onnx
→ 注册到 models 表
→ 可通过 axon re-embed -m bge-small-zh-v1.5 迁移
```

**文件变更：**
- `internal/embed/onnx.go` — 完整实现（当前为骨架）
- `cmd/model.go` — `axon model download` 实现
- `Makefile` — 新增 `make build-onnx` target
- `.github/workflows/release.yml` — ONNX 构建矩阵

### 6.2 模型自动选择策略

```
用户未指定模型时：
  1. 检测内容语言（简单 heuristic：CJK 字符占比）
  2. 已下载模型中选最合适的
  3. 兜底：PureGo
```

**交付物：**
- [ ] `onnx.go` 完整实现
- [ ] `axon model download` 命令
- [ ] bge-small-en/zh 集成测试
- [ ] ONNX 构建 GitHub Actions

---

## Phase 7 — Watch 模式 & 增量更新

**目标：** 文件变更自动感知，KB 与本地笔记保持同步。

### 7.1 Watch 模式

```
axon watch ~/notes -c notes
→ 启动文件系统监听（fsnotify）
→ 文件创建/修改 → 增量 ingest
→ 文件删除 → 标记 source 为 archived
→ 守护进程模式（--daemon flag）
→ 状态写入 ~/.axon/watch.pid
```

**技术：**
- `github.com/fsnotify/fsnotify` — 跨平台文件系统事件
- 去抖动：500ms 窗口内的同一文件事件合并
- 日志：`~/.axon/watch.log`

### 7.2 增量 Ingest

- 基于 `origin_hash` 检测内容变更
- 变更时：删除旧 chunks/embeddings → 重新切片 → 重新 embed → 更新关系
- 未变更：跳过（已在 Phase 2 实现，此处完善边缘 case）

### 7.3 Collection 同步配置

```yaml
# ~/.axon/sync.yaml
syncs:
  - path: ~/notes/work
    collection: work
    glob: "*.md"
  - path: ~/notes/diary
    collection: diary
    glob: "*.md"
    exclude: ["templates/**", ".obsidian/**"]
```

**交付物：**
- [ ] `axon watch` 命令
- [ ] fsnotify 集成
- [ ] 增量更新边缘 case 修复
- [ ] sync.yaml 配置支持
- [ ] `--daemon` 后台模式

---

## Phase 8 — Reranker & 检索质量提升

**目标：** 检索精度从"够用"升级到"好用"。

### 8.1 Reranker 接口

```go
type Reranker interface {
    Rerank(ctx context.Context, query string, results []SearchResult) ([]SearchResult, error)
    Name() string
}
```

**实现：**
| 实现 | 方式 | 何时使用 |
|------|------|---------|
| `RRFReranker` | 纯算法（已有）| 默认，无需配置 |
| `CrossEncoderReranker` | ONNX cross-encoder | 安装 ONNX 后可用 |
| `APIReranker` | Cohere / Jina API | 有 API Key 时 |

**推荐模型：** `cross-encoder/ms-marco-MiniLM-L-6-v2`（82MB）

### 8.2 查询扩展（Query Expansion）

```
axon query "性能优化" --expand
→ LLM 扩展为：["性能优化", "performance tuning", "profiling", "benchmark"]
→ 对每个扩展词检索，结果合并后 rerank
```

### 8.3 上下文感知检索

- `axon query` 支持 `--context` flag，传入前置对话或背景文本
- 用于 MCP 场景：AI 工具将当前对话 context 传入提升相关性

**交付物：**
- [ ] Reranker 接口 + RRF（已有，整理为正式接口）
- [ ] CrossEncoderReranker（ONNX）
- [ ] APIReranker（Cohere/Jina）
- [ ] `--rerank` flag for `axon query`
- [ ] Query expansion（可选，需 LLM）

---

## Phase 9 — 外部插件系统

**目标：** 社区可扩展，支持 GitHub Issues / YouTube / Notion API 等第三方源。

### 9.1 插件协议

```
通信：stdin/stdout newline-delimited JSON-RPC 2.0
生命周期：axon 作为宿主，按需 spawn 插件进程

请求格式：
{"jsonrpc":"2.0","id":1,"method":"fetch","params":{"origin":"...", "config":{}}}

响应格式：
{"jsonrpc":"2.0","id":1,"result":{"raw_content":"...","plain_text":"...","title":"..."}}
```

### 9.2 插件 manifest

```yaml
# ~/.axon/plugins/axon-github/plugin.yaml
name: axon-github
version: 0.1.0
description: GitHub Issues & PRs
source_type: github-issue
binary: ./axon-github          # 相对于 plugin.yaml 的路径
schemes:
  - "https://github.com/*/issues/*"
  - "https://github.com/*/pull/*"
```

### 9.3 内置插件规划

| 插件名 | 数据源 | 优先级 |
|--------|--------|--------|
| `axon-github` | GitHub Issues/PRs/Discussions | 🟡 P1 |
| `axon-youtube` | YouTube 字幕 | 🟡 P1 |
| `axon-notion-api` | Notion API（非导出） | 🟢 P2 |
| `axon-rss` | RSS/Atom Feed 订阅 | 🟢 P2 |

**命令：**
```
axon plugin list
axon plugin install axon-github
axon plugin rm axon-github
```

**交付物：**
- [ ] 插件宿主 `internal/plugin/host.go`
- [ ] JSON-RPC 通信层
- [ ] `axon plugin` 命令组
- [ ] `axon-github` 参考实现
- [ ] 插件 SDK（Go 库，方便社区开发）

---

## 里程碑与时间线

```
Phase 5  [稳定性]      ~1周    存储统一、接口清理、MCP 补全
Phase 6  [ONNX]        ~2周    本地模型真正可用
Phase 7  [Watch]       ~1周    文件同步自动化
Phase 8  [Reranker]    ~1周    检索质量提升
Phase 9  [插件系统]    ~2周    社区扩展能力
                       ────
总计                   ~7周    → v0.5.0 正式版
```

---

## v0.5.0 发布标准

- [ ] Phase 5–9 全部完成
- [ ] `axon add` / `axon query` / `axon tui` / `axon mcp` 无已知 bug
- [ ] ONNX 本地 Embedding 在 macOS/Linux 可用
- [ ] Watch 模式稳定运行 24h 无崩溃
- [ ] GitHub Release 5 平台二进制（含 ONNX 版）
- [ ] README 完整安装 & 使用文档
- [ ] CHANGELOG.md

---

## 技术选型参考

| 功能 | 选型 | 备注 |
|------|------|------|
| CLI 框架 | `spf13/cobra` | ✅ 已用 |
| TUI 框架 | `charmbracelet/bubbletea` | ✅ 已用 |
| SQLite | `mattn/go-sqlite3` + FTS5 | ✅ 已用 |
| ONNX Runtime | `yalue/onnxruntime_go` | Phase 6 |
| 文件监听 | `fsnotify/fsnotify` | Phase 7 |
| 跨平台打包 | GitHub Actions matrix | ✅ 已有 |
| 测试 | `testify/assert` + SQLite in-memory | Phase 5 补充 |
