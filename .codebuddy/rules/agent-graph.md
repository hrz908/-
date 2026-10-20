---
description: Python agent（agent/）的图路由与生成约定：意图兜底、RAG 不得编造、prompt 模板、checkpointer
globs:
  - agent/**/*.py
  - agent/**/*.xml
---

# Agent 约定（agent/）

## 1. 意图分类不能只信 LLM

出现过：`查询 SH600519 贵州茅台 的实时报价` 被判成 RAG 分支，没调任何行情工具，
直接引用一份无关的测试文档作答（还带引用角标）。

要求：行情 / 报价 / K线 / 涨跌 / 成交量这类问句必须有**关键词兜底**强制走 MCP 分支，
LLM 判定只作为兜底之后的补充。

## 2. RAG 分支检索不到就直说检索不到

不得用模型知识补内容，尤其不得输出具体数值、公司名、日期。
注意：现有的数据关键词兜底（如 `_DATA_KEYWORDS`、无数据回复模板）**只在 MCP 分支的 synthesize 内生效**，
RAG 分支不受保护 —— 这是上面那个 bug 的根因。

## 3. `prompts/*.xml` 是 Jinja2 模板

改变量名要同步 `nodes/` 里传参的地方；模板里引用的变量少一个就会静默渲染成空。
人设名目前是「StockA」，而小程序展示名是「溜来」，两者不一致（是否统一待定）。

## 4. 多轮上下文靠 checkpointer

`langgraph-checkpoint-postgres`，`thread_id` 决定会话（对应库表 `checkpoints` /
`checkpoint_writes` / `checkpoint_blobs`）。测试要用不同 `thread_id` 隔离，否则上下文串味。

## 5. 工具返回可能是降级/假数据

行情源失败时会降级。回答里不得把降级数据当真实行情，也不得编造具体数值。

## 6. 慢是常态，不是 bug

一次多跳 ReAct 常见 8~35s，偶发撞上网关的 120s `runTimeout`（表现为前端收到
`context deadline exceeded`）。排查耗时看 `docker compose logs -f agent`，
或用 `curl -N http://127.0.0.1:8000/run/stream` 直连看 ndjson 事件时间线。

## 7. 向量库污染的排查入口

`documents` / `chunks` 两张表。测试产物（如 `server/tmp_smoke/` 下的一次性文档）一旦被 ingest
进库，会污染所有 RAG 回答。发现问题先查这两张表的来源字段。
