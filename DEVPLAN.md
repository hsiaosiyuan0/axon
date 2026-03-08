# Axon — 开发计划

> 重新规划于：2026-03-08
> 基于：product.md + architecture.md + 实际代码库审查
> 注：本项目使用 AI 辅助开发，速度远快于传统排期，计划不按周/天估算，只跟踪里程碑完成状态。

---

## 一、当前真实状态

### 产品定位（来自 product.md）
个人本地知识库 & 记忆体，**单二进制、跨平台 CLI**，核心价值：
- 本地优先（SQLite，无需联网）
- 单二进制（无依赖，下载即用）
- 关系感知（引用/语义关系图）
- AI 就绪（MCP 直接接入 Claude/Cursor）

### 代码量现状
- 22 个 cmd，22 个 internal 模块，编译通过
- 但大量模块是**一次性生成的骨架代码**，未经实际使用验证

### 已经真正可用的功能 ✅
| 功能 | 验证状态 |
|------|---------|
| `axon init` | ✅ 实测通过 |
| `axon add <file>` | ✅ 实测通过（FTS5 索引正常） |
| `axon query <text>` | ✅ BM25 命中，分数正确 |
| `axon collection new/list` | ✅ 实测通过 |
| `axon status` | ✅ 实测通过 |
| PureGo Embedder + 向量搜索 | ✅ 集成测试通过 |
| BM25 + RRF 混合检索 | ✅ 集成测试通过 |
| CJK 分词 | ✅ 单元测试通过 |

### 实现了但**未验证**的功能 🟡
| 功能 | 问题 |
|------|------|
| `axon add <url>` | HTML→plaintext 已实现，未实测 |
| `axon serve` HTTP API | 代码完整，未实测 |
| `axon tui` | Bubble Tea 框架搭建完成，实际交互未验证 |
| `axon mcp` MCP Server | stdio JSON-RPC 实现，未接入 Claude 验证 |
| ~~`axon watch`~~ | ✅ 已完成：debounce + daemon + stop，M4 验证通过 |
| `axon vault` Obsidian 导入 | 解析器完整，端到端未验证 |
| `axon chat` RAG 对话 | 需要 AXON_LLM_API_KEY，未实测 |
| `axon relate --auto` | 向量相似度 O(n²)，未验证实际效果 |
| `axon sync` | 三个 backend 代码完整，未实测 |
| `axon export` | Markdown/JSON/JSONL/Anki，未验证 |
| `axon dedupe` | 精确+语义去重，未验证 |
| `axon re-embed` | 实现完整，未验证 |

### 核心缺失 🔴
| 问题 | 影响 |
|------|------|
| ~~ONNX Embedder 是 stub~~ | ✅ 已完成：bge-small-zh-v1.5 内嵌二进制，开箱即用 |
| ~~`axon model download` 未实现~~ | ✅ 已完成：完整模型注册表 + 多镜像支持 |
| MCP 未实际接入 Claude Desktop | 最重要的 AI 接入场景未验证 |
| ~~`axon add` 后无法知道存了什么~~ | ✅ 已完成：`axon list` 命令实现 |

---

## 二、产品核心路径（按优先级）

重新对齐 product.md，Axon 的**核心使用场景**是：

```
1. axon add <file/url>     ← 存入知识
2. axon query <text>       ← 检索知识  
3. axon mcp → Claude       ← AI 调用知识
4. axon tui                ← 人工浏览知识
```

一切开发优先级围绕这 4 条路径展开。

---

## 三、开发计划

### Milestone 1 — 核心路径可靠（当前最优先）

**目标：让 add → query → mcp 这条主链路真正可靠、可演示**

#### M1.1 补齐 `axon add` 可见性
- [x] 实现 `axon list` 命令：列出已添加的 sources（title + origin + chunk 数）
- [x] `axon add` 成功后显示更友好的摘要（已添加 N 个片段，前 3 个标题）
- [x] `axon add <url>` 实测并修复 bug

#### M1.2 MCP 实测接入 Claude Desktop
- [ ] ⚠️ **[待产品完成后验证]** 本地运行 `axon mcp`，配置 Claude Desktop，验证 6 个工具可用
- [ ] ⚠️ **[待产品完成后验证]** 修复发现的 MCP 协议 bug（JSON-RPC 格式、工具描述等）
- [x] 补充 MCP 工具：`memory_delete` / `memory_stats`（代码完成，待 Claude Desktop 验证）

#### M1.3 搜索质量基线确认
- [x] 添加 10-20 篇真实文档，验证 BM25 中文检索质量
- [x] 确认 RRF 融合是否正常工作（而不是只有 BM25 在起作用）
- [x] `axon query` 输出格式优化：snippet 显示更清晰，collection 显示名称而非 UUID

**完成标准：** 能向朋友演示"加文档 → 问 Claude → Claude 正确引用知识"

---

### Milestone 2 — 本地 Embedding ✅

**目标：不依赖 API Key，语义搜索质量达到可用水平**

#### M2.1 选型决策 ✅
采用方案 B（ONNX 本地 Embedding）+ 方案 A（API Embedding）双轨：
- 优先级：`API Key 配置 > ONNX 本地 > PureGo 兜底`
- ONNX 通过 `-tags onnx` build tag 隔离，不破坏单二进制发布

#### M2.2 ONNX 实现 ✅
- [x] `go.mod` 引入 `yalue/onnxruntime_go` + `daulet/tokenizers`
- [x] `internal/embed/onnx.go` 完整实现（tokenize → inference → mean pool → L2 norm）
- [x] `internal/embed/model_assets.go`：`go:embed` 将 `model.onnx` + `tokenizer.json` 打包进二进制
- [x] `internal/embed/model_assets_stub.go`：非 ONNX build 的 stub
- [x] `scripts/build.sh --onnx`：自动下载模型 + 编译，支持 `HF_MIRROR` 环境变量
- [x] 二进制大小 76MB（含 ONNX runtime + bge-small-zh-v1.5 量化模型）

#### M2.3 模型注册表 ✅
- [x] `internal/modelreg/registry.go`：6 个预设模型（bge-small/base/large-zh, bge-m3, e5-small 等）
- [x] `internal/modelreg/download.go`：带进度条下载器，支持 Git LFS 指针处理
- [x] `axon model list`：显示所有模型状态（embedded / ready / not downloaded）
- [x] `axon model download <name> --mirror <hf-mirror|modelscope|自定义URL>`
- [x] `axon model mirrors`：列出所有镜像预设
- [x] 内置默认模型 `bge-small-zh-v1.5` 在 `axon init` 时自动解压，无需网络

**完成标准：** 无 API Key 情况下，中文语义搜索真实可用 ✅

---

### Milestone 3 — TUI 完善 ✅

**目标：让 `axon tui` 成为日常使用的主要界面**

- [x] 实测 `axon tui`，记录实际 bug
- [x] 搜索输入 150ms 防抖（避免逐字触发）
- [x] 结果列表：显示 collection 标签 + 评分 + 来源文件名
- [x] 详情预览：Enter 展开全文，支持滚动
- [x] Collection 筛选：`c` 键弹出 picker 面板
- [x] 退出确认：`q` 键首次按提示，再按退出；`ctrl+c` 直接退出

**完成标准：** 日常笔记搜索全部通过 TUI 完成，不需要 CLI ✅

---

### Milestone 4 — Watch + 自动同步 ✅

**目标：知识库与本地文件自动保持同步**

- [x] `axon watch ~/notes/` 实测稳定性（24h 运行）
- [x] 修复：同一文件快速多次保存只触发一次 ingest（2s debounce）
- [x] 实现 `axon watch --daemon`：后台运行，写 PID 文件
- [x] 实现 `axon watch stop`：停止后台 daemon
- [x] 日志写入 `~/.axon/watch.log`

**Obsidian 集成（高价值用户场景）：**
- [x] `axon vault ~/MyVault` 端到端实测
- [x] 验证 `[[wikilink]]` 关系是否正确建立
- [x] 与 `axon watch` 结合：`axon watch ~/vault --vault` 变更自动更新

**完成标准：** Obsidian vault 修改笔记后 5 秒内 Claude 能检索到新内容 ✅

---

### Milestone 5 — 对外发布 v0.1.0 ✅

**目标：有一个可以公开分享的版本**

- [x] `go test ./...` 全部通过（5 个包，21/21 release check）
- [x] README 精简到"5分钟上手"（Quick Start 提前，折叠源码构建）
- [x] GitHub Actions release 完善（test job 前置，5 平台打包，CHANGELOG 链接）
- [x] Demo 脚本 `scripts/demo.sh`（端到端 init → add → query）
- [x] CHANGELOG.md 第一个条目（完整 v0.1.0 功能列表）
- [x] LICENSE 文件（MIT）
- [x] 中文文档 `docs/README_zh.md`
- [x] `axon --version` 标志
- [x] Release checklist `scripts/check-release.sh`（21/21 通过）

**完成标准：** README 里的命令 100% 可以跑通 ✅

---

## 四、明确不做的事（范围控制）

以下功能在 v0.5.0 之前**不推进**：

| 功能 | 原因 |
|------|------|
| `axon sync` WebDAV/S3 | 代码已有但复杂，核心场景不需要 |
| `axon export` Anki | 边缘用户场景 |
| `axon chat` RAG 对话 | Claude 通过 MCP 已覆盖此场景 |
| 外部插件系统 | 生态建设，发布后再做 |
| CrossEncoder Reranker | 搜索质量未成为瓶颈前无需优化 |
| Plugin Hub | 发布后社区驱动 |
| Web UI | TUI 优先，Web UI 是额外工作 |

---

## 五、下一步行动

**当前最优先：**
1. MCP 实测接入 Claude Desktop（最重要的 AI 接入场景）
2. 验证 `axon add <url>` 端到端正常工作
3. 记录 bug 列表，推进 M1.2

**后续候选方向（Milestone 6+）：**
- 外部插件系统（发布后社区驱动）
- mobile sync（低优先级）
- Web UI 完善

---

## 六、技术决策记录（ADR）

### ADR-001: SQLite as sole storage ✅ 不变
单 SQLite，FTS5 BM25 + 手写向量搜索。个人 KB 规模完全够用。

### ADR-002: Embedding 优先级
`API Key 配置 > ONNX 本地 > PureGo 兜底`
- PureGo 只是兜底，不应依赖它做语义搜索

### ADR-003: MCP 优先于 HTTP API
Claude / Cursor 直接用 MCP，HTTP API 是次要接口。

### ADR-004: ONNX 通过 build tag 隔离
`-tags onnx` 单独构建，默认二进制不含 ONNX Runtime 动态库依赖。

### ADR-005: 功能深度优于功能广度（新增）
已有 22 个命令，但大多数未经验证。**不再新增命令，专注让已有命令可靠。**
