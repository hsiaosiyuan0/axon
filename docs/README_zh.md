[English](../README.md) | **中文**

---

# Axon

<p align="center">
  <img src="logo.jpg" alt="Axon Logo" width="200" />
  <br/>
  <em>你的个人知识库与记忆引擎。</em>
  <br/>
  <em>本地优先 · 单二进制 · 关系感知 · AI 就绪</em>
</p>

<p align="center">
  <a href="https://github.com/hsiaosiyuan0/axon/actions/workflows/ci.yml"><img src="https://github.com/hsiaosiyuan0/axon/actions/workflows/ci.yml/badge.svg" alt="CI"/></a>
  <a href="https://github.com/hsiaosiyuan0/axon/actions/workflows/release.yml"><img src="https://github.com/hsiaosiyuan0/axon/actions/workflows/release.yml/badge.svg" alt="Release"/></a>
  <a href="../LICENSE"><img src="https://img.shields.io/badge/License-MIT-blue.svg" alt="License: MIT"/></a>
  <a href="https://go.dev"><img src="https://img.shields.io/badge/Go-1.22+-00ADD8?logo=go" alt="Go"/></a>
</p>

---

## Axon 是什么？

Axon 是一款 CLI 工具，将你的文档、笔记、网页和代码片段转化为**可检索的、关系感知的知识图谱**——全部存储在本地单个 SQLite 文件中。

```
axon add meeting-notes.md          # 摄入文档
axon add https://go.dev/blog/...   # 摄入网页
axon add paper.pdf -c research     # 摄入 PDF 到 "research" Collection
axon query "API 设计模式"           # BM25 + 向量混合检索
axon tui                           # 交互式 TUI
axon serve                         # 启动 HTTP API（供 Claude、Cursor 等使用）
axon mcp                           # 为 Claude Desktop 启动 MCP Server
```

---

## 5 分钟快速上手

```bash
# 1. 下载安装（macOS ARM）
curl -fsSL https://github.com/hsiaosiyuan0/axon/releases/latest/download/axon-darwin-arm64 \
  -o /usr/local/bin/axon && chmod +x /usr/local/bin/axon

# 2. 初始化知识库
axon init

# 3. 创建 Collection
axon collection new

# 4. 添加内容
axon add ~/notes/myfile.md
axon add https://go.dev/blog/concurrency

# 5. 检索
axon query "并发 goroutine"

# 6. 启动 TUI 或 Web UI
axon tui
axon serve   # → 打开 http://localhost:7474/ui
```

---

## 功能列表

| 功能 | 描述 |
|------|------|
| 🗂️ **Collections** | 按主题组织知识（笔记、研究、代码…） |
| 🔍 **混合检索** | BM25 全文 + 向量 Embedding，RRF 融合 |
| 🕸️ **知识图谱** | 自动发现 `[[wikilink]]`、语义相似、LLM 提取关系 |
| 📄 **多格式支持** | Markdown、URL、PDF、Obsidian vault、Notion 导出、代码片段 |
| 🤖 **AI 就绪** | 用于 Claude Desktop 的 MCP Server，HTTP REST API |
| 🌐 **Web UI** | 内置 D3.js 知识图谱浏览器（`axon serve` → `/ui`） |
| 📱 **TUI** | 终端交互式实时搜索 |
| 👁️ **Watch 模式** | 后台自动摄入文件变更 |
| 🔒 **本地优先** | 所有数据存储在 `~/.axon/axon.db`，无需云端 |
| 📦 **单二进制** | 下载即用，零依赖 |
| 🔑 **多知识库** | 使用 `--db` 切换多个知识库 |

---

## 安装

### 下载二进制（推荐）

```bash
# macOS（Apple Silicon）
curl -fsSL https://github.com/hsiaosiyuan0/axon/releases/latest/download/axon-darwin-arm64 \
  -o /usr/local/bin/axon && chmod +x /usr/local/bin/axon

# macOS（Intel）
curl -fsSL https://github.com/hsiaosiyuan0/axon/releases/latest/download/axon-darwin-amd64 \
  -o /usr/local/bin/axon && chmod +x /usr/local/bin/axon

# Linux（amd64）
curl -fsSL https://github.com/hsiaosiyuan0/axon/releases/latest/download/axon-linux-amd64 \
  -o /usr/local/bin/axon && chmod +x /usr/local/bin/axon
```

<details>
<summary>从源码构建</summary>

```bash
git clone https://github.com/hsiaosiyuan0/axon
cd axon
make build       # 生成 ./axon
make install     # 复制到 /usr/local/bin/axon
```

**要求**：Go 1.22+，GCC（CGO/SQLite 需要）

</details>

---

## 命令参考

```
axon init                          初始化知识库
axon add <file|url>                添加文档（自动分类）
axon add <file> -c <collection>    添加到指定 Collection
axon query <text>                  混合检索
axon query <text> --rerank         带两阶段重排的检索
axon tui                           交互式 TUI 搜索
axon serve                         HTTP REST API + Web UI
axon mcp                           MCP Server（用于 Claude Desktop）

axon collection list               列出所有 Collection
axon collection new                新建 Collection（交互式）
axon collection rm <id>            删除 Collection

axon import <dir>                  批量导入目录
axon import --notion <dir>         导入 Notion 导出
axon vault <obsidian-dir>          导入 Obsidian vault（含 [[wikilink]]）

axon relate                        构建关系图
axon relate --auto                 自动发现语义关系
axon relate --llm                  LLM 提取语义三元组
axon graph                         终端可视化知识图谱

axon watch <dir>                   监听目录变更
axon watch --daemon                后台运行
axon watch stop                    停止后台 daemon
axon status                        知识库健康概览
axon export                        导出为 Markdown / JSON / JSONL / Anki
axon dedupe                        检测并清理重复内容

axon re-embed -m <model>           用新模型重新 Embedding
axon model list                    查看 Embedding 模型
axon config set llm.key sk-...     配置 LLM API Key
axon upgrade                       检查新版本
```

### 全局标志

```
axon --db ~/work.db query "..."    使用指定知识库
axon --db ~/research.db add ...   在不同知识库间切换
```

---

## Collections

Collection 将知识组织成主题分组。Axon 可使用 LLM 自动分类（若已配置），或允许手动选择。

```bash
axon collection new         # 交互式创建
axon collection list        # 显示所有 Collection
axon query "..." -c notes   # 限定 Collection 检索
```

内置 Collection 类型：

| 类型 | 适合内容 | 切片策略 |
|------|---------|---------|
| `notes` | 学习笔记、读书笔记 | 按标题层级 |
| `diary` | 日记条目 | 按日期/段落 |
| `work` | 会议记录、PRD | 按标题层级 |
| `code` | 代码片段、脚本 | 按函数/块 |
| `custom` | 自定义 | 可配置 |

---

## 检索

Axon 使用**混合检索**：BM25 全文检索与向量相似度检索通过 RRF（Reciprocal Rank Fusion）融合。

```bash
# 基础检索
axon query "Go channel 和 goroutine"

# 限定 Collection
axon query "transformer attention" -c research

# 带两阶段重排（提高精度）
axon query "API 设计" --rerank

# LLM 驱动的重排
axon query "系统设计" --rerank --rerank-mode llm

# 获取更多结果
axon query "..." -n 10
```

### CJK 支持

BM25 检索自动处理中文、日文、韩文文本，使用 unigram + bigram 分词。

---

## 知识关系

Axon 构建包含多种关系类型的知识图谱：

| 类型 | 建立方式 |
|------|---------|
| `ref` | Markdown `[链接](...)` / Obsidian `[[wikilink]]` |
| `similar` | 向量余弦相似度 > 阈值 |
| `semantic` | LLM 提取的主谓宾三元组 |
| `cite` | 引用块 / 引用模式 |

```bash
# 自动发现语义关系（向量相似度）
axon relate --auto --threshold 0.85

# LLM 提取丰富语义三元组
axon relate --llm --source <id>

# 终端可视化
axon graph

# 浏览器中查看完整图谱（D3.js）
axon serve  # → http://localhost:7474/ui
```

---

## AI 集成

### Claude Desktop（MCP）

首先配置 API Key：

```bash
axon config set llm.key sk-your-api-key
```

然后添加到 `~/.config/claude/claude_desktop_config.json`：

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

可用工具：`memory_query`、`memory_add`、`memory_relate`、`memory_collections`、`memory_delete`、`memory_stats`

### HTTP REST API

```bash
axon serve                           # 默认：http://localhost:7474
axon serve --addr :8080 --key mysecret

# 端点
GET  /health
GET  /v1/status
GET  /v1/collections
GET  /v1/query?q=...&collection=...&limit=5
POST /v1/query        # JSON body
POST /v1/add          # 添加文件/URL/片段
GET  /v1/graph        # 知识图谱 JSON
GET  /v1/watch        # Server-Sent Events 流
GET  /ui              # Web UI（D3.js 图谱浏览器）
```

---

## 多知识库

使用 `--db` 维护不同项目的独立知识库：

```bash
# 工作知识库
axon --db ~/work.db init
axon --db ~/work.db add meeting.md -c planning
axon --db ~/work.db query "Q3 目标"

# 研究知识库
axon --db ~/research.db add paper.pdf -c papers
axon --db ~/research.db query "attention mechanism"

# 通过环境变量切换
AXON_DB=~/research.db axon query "transformer"
```

---

## 支持的格式

| 格式 | 命令 |
|------|------|
| Markdown（`.md`） | `axon add notes.md` |
| 纯文本（`.txt`） | `axon add log.txt` |
| PDF（`.pdf`） | `axon add paper.pdf` |
| URL | `axon add https://...` |
| 代码片段 | `axon add snippet.go` |
| Obsidian vault | `axon vault ~/vault/` |
| Notion 导出 | `axon import --notion ~/notion-export/` |
| 目录 | `axon import ~/docs/` |

---

## 导出

```bash
# Markdown（按 Collection 组织，每个 source 一个文件）
axon export -o ~/backup/

# JSON bundle（所有 source + 关系）
axon export -f json -o axon-backup.json

# JSONL（流式，每行一个 source）
axon export -f jsonl -o axon-backup.jsonl

# Anki 闪卡（每个 chunk 一张卡片）
axon export -f anki -o axon-cards.apkg
```

---

## 配置

所有设置均可通过配置文件或环境变量配置：

```bash
# 持久化配置（推荐）
axon config init                    # 生成 ~/.axon/config.toml
axon config set llm.key sk-...      # 设置 LLM API Key
axon config set embed.provider api  # 切换为 API Embedding
axon config show                    # 查看当前配置（Key 自动脱敏）
```

| 环境变量 | 默认值 | 描述 |
|---------|-------|------|
| `AXON_DB` | `~/.axon/axon.db` | 数据库路径 |
| `AXON_EMBED_PROVIDER` | `onnx` | Embedding 后端：`onnx` \| `api` \| `purego` |
| `AXON_LLM_ENDPOINT` | `https://api.openai.com/v1` | LLM API 端点 |
| `AXON_LLM_API_KEY` | _（无）_ | LLM API Key |
| `AXON_LLM_MODEL` | `gpt-4o-mini` | LLM 模型名称 |
| `AXON_API_KEY` | _（无）_ | HTTP API 鉴权 Key |

### 使用本地/替代 LLM

```bash
# 使用 Ollama
AXON_LLM_ENDPOINT=http://localhost:11434/v1 \
AXON_LLM_MODEL=llama3.2 \
axon relate --llm

# 使用 OpenRouter
AXON_LLM_ENDPOINT=https://openrouter.ai/api/v1 \
AXON_LLM_API_KEY=sk-or-... \
axon relate --llm
```

---

## Watch 模式

文件变更时自动摄入：

```bash
# 监听 ~/notes 目录
axon watch ~/notes/

# 后台运行（daemon 模式）
axon watch ~/notes/ --daemon
axon watch status
axon watch stop

# 监听多个目录，只关注 .md 文件
axon watch ~/notes/ ~/docs/ --ext .md
```

---

## 去重

```bash
# 预览重复项（默认 dry-run）
axon dedupe

# 近重复检测（向量相似度）
axon dedupe --threshold 0.95

# 实际删除重复项
axon dedupe --confirm

# 仅精确 hash 匹配（更快）
axon dedupe --exact-only --confirm
```

---

## 架构

```
~/.axon/
├── axon.db           SQLite（FTS5 + embeddings + relations）
├── config.toml       持久化配置
└── models/           本地 ONNX 模型（可选）

axon/
├── cmd/              Cobra CLI 命令
├── internal/
│   ├── store/        SQLite 数据仓库
│   ├── ingest/       摄入流水线（fetch → chunk → embed → relate）
│   ├── chunk/        Markdown / 段落 / 固定大小切片器
│   ├── embed/        Embedder 接口（API / ONNX / PureGo）
│   ├── hybrid/       BM25 + 向量 RRF 融合
│   ├── rerank/       Token overlap & LLM 重排器
│   ├── relate/       LLM 三元组提取 + 断点续传
│   ├── plugin/       文件 / URL / PDF / Notion 插件
│   ├── obsidian/     Obsidian vault 解析器 + wikilink 解析
│   ├── classify/     LLM Collection 分类器
│   ├── watch/        文件系统监听器（轮询）
│   ├── graph/        知识图谱构建器
│   ├── api/          HTTP REST API Server
│   ├── ui/           内嵌 Web UI（D3.js）
│   ├── anki/         Anki .apkg 导出
│   ├── dedupe/       重复检测
│   └── tokenize/     CJK 分词器（BM25）
├── mcp/              MCP Server（stdio JSON-RPC）
└── models/           Embedding 模型注册表 YAML
```

---

## 开发

```bash
# 构建（注入版本号）
make build

# 运行测试
make test

# 本地安装
make install

# 检查版本
axon upgrade
```

---

## Roadmap

- [x] Phase 1 — 核心：init、add、query、collections、BM25+向量混合
- [x] Phase 2 — URL 摄入、HTML→文本、MCP Server
- [x] Phase 3 — TUI、re-embed、GitHub Actions CI/CD
- [x] Phase 4 — LLM 分类、语义关系、Notion 导入
- [x] Phase 5 — Watch 模式（文件系统同步）
- [x] Phase 6 — HTTP API、status、export（MD/JSON/JSONL）
- [x] Phase 7 — API 鉴权、Obsidian vault、LLM 三元组、重排器
- [x] Phase 8 — Graph API、ASCII 图谱、CJK 分词、SSE watch
- [x] Phase 9 — Web UI（D3.js）、LLM 断点续传、MCP rerank、去重
- [x] Phase 10 — PDF 支持、多 vault（`--db`）、`axon upgrade`、Anki 导出
- [x] Phase 11 — RAG 对话、多设备同步、集成测试、外部插件系统
- [ ] v0.2.0 — ONNX 本地 Embedding 真实集成、`axon chat --tui`、WebSocket 实时推送

---

## 许可证

MIT — 详见 [LICENSE](../LICENSE)

---

## 贡献

欢迎提交 Issue 和 PR：[github.com/hsiaosiyuan0/axon](https://github.com/hsiaosiyuan0/axon)

---

*"每一个连接都有意义，每一段记忆都值得保存。"*
