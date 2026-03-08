# Axon — Product Document

## 产品定位

**个人本地知识库 & 记忆体**

一个单二进制、跨平台的 CLI 工具，让用户在本地构建自己的知识图谱。  
支持文档、网页、代码片段、日记等多种知识类型，通过混合检索和关系图谱实现智能召回。

---

## 目标用户

- 知识工作者：研究员、工程师、作家
- 重度笔记用户：Obsidian / Notion 用户想要更强的检索能力
- AI 工具重度用户：希望给 Claude / Cursor 接入个人知识库
- 隐私敏感用户：不愿意把私人数据上传云端

---

## 核心价值

| 价值 | 描述 |
|------|------|
| 本地优先 | 所有数据存在本地 SQLite，无需联网即可使用 |
| 单二进制 | 无需安装依赖，下载即用，跨平台 |
| 关系感知 | 不只是搜索，还能理解知识之间的引用和关联 |
| 可进化 | 技术进步时可用新 Embedding 模型重新处理已有数据 |
| AI 就绪 | 通过 MCP 协议直接接入 Claude、Cursor 等 AI 工具 |

---

## Collection 类型

| 类型 | 标识 | 适合内容 | 切片策略 |
|------|------|---------|---------|
| 日记 | `diary` | 日记、流水账 | 按日期/段落 |
| 工作文档 | `work` | 会议记录、方案、PRD | 按标题层级 |
| 代码片段 | `code` | 代码、脚本、配置 | 按函数/块 |
| 学习笔记 | `notes` | 读书笔记、课程笔记 | 按章节/概念 |
| 自定义 | `custom` | 用户自定义 | 可配置 |

---

## 知识关系类型

| 关系 | 说明 | 建立方式 |
|------|------|---------|
| `ref` | 文档引用 | 解析 Markdown 链接 / Wiki 链接 |
| `cite` | 内容引用 | 解析引用块 |
| `similar` | 语义相似 | 向量余弦相似度 > 阈值 |
| `parent` | 父子关系 | 文档目录结构 |
| `child` | 子父关系 | 同上反向 |

关系支持跨 Collection，支持更新（文件变更时重新解析）。

---

## LLM 辅助功能

1. **自动分类**：添加文档时，LLM 分析内容建议放入哪个 Collection
2. **关系提取**：LLM 从内容中提取语义关联，补充解析器无法发现的关系
3. **智能重排**：使用 Reranker 模型对混合检索结果重排序

以上功能均为可选，用户可完全离线使用。

---

## 用户体验流程

### 添加文档
```
axon add meeting.md
→ [Plugin] 读取文件内容
→ [LLM] 建议：放入"工作文档" (理由: 包含会议记录)
→ [TUI] 确认 / 选择其他 / 新建 Collection
→ [Chunker] 按 Markdown 标题切片
→ [Embedder] 计算向量
→ [RelParser] 提取引用关系
→ ✅ 已添加 12 个知识片段
```

### 查询
```
axon query "API 设计讨论"
→ [BM25] 关键词检索 top-20
→ [Vector] 语义检索 top-20
→ [RRF] 融合排序
→ [Reranker] (可选) 精排
→ 返回 top-5 片段 + 来源 + 关系链
```

---

## CLI 命令

```
axon init                          初始化知识库
axon add <file|url>                添加知识（自动分类）
axon add <file> -c <collection>    添加到指定 Collection
axon query <text>                  混合检索
axon query <text> -c <collection>  限定 Collection 检索

axon collection list               列出所有 Collection
axon collection new                新建 Collection（交互式）
axon collection rm <id>            删除 Collection

axon model list                    查看支持的 Embedding 模型
axon model download <name>         下载本地模型
axon model rm <name>               删除本地模型

axon re-embed -c <collection> -m <model>  重新 Embedding
axon embed prune --keep <model>           清理旧向量

axon relate <id>                   查看某个知识的关系图

axon tui                           启动 TUI
axon mcp                           启动 MCP Server (stdio)

axon plugin list                   查看已安装插件
axon plugin install <name>         安装插件
```

---

## MCP 工具列表

| Tool | 描述 |
|------|------|
| `memory_query` | 搜索知识库 |
| `memory_add` | 添加知识片段 |
| `memory_relate` | 查询知识关系 |
| `memory_collections` | 列出所有 Collection |

Claude Desktop 配置：
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
