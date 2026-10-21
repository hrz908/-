# agent/（Python 智能体）

FastAPI 入口 `main.py`；LangGraph 构图在 `graph.py`。详细图路由/生成约定见 `.codebuddy/rules/agent-graph.md`。

## 结构
- `subgraphs/rag.py` 知识库检索子图；`subgraphs/mcp.py` **行情取数子图**（有界 ReAct，最多 3 轮，节点 tool_call → tool_execute → synthesize）。注意：这里的 mcp 是内部命名（路由意图标签），**不是 Model Context Protocol**
- `nodes/classify.py` 意图分类；`nodes/post.py` 收尾（滚动摘要写库）
- `prompts/*.xml` jinja2 渲染：`router.xml`（意图）、`mcp.xml`（合成回答）等
- `tools/` 工具：get_quote / get_kline / get_valuation / symbol_search（回调 Go 网关，需 `GO_SERVER_URL` + `GO_SERVER_TOKEN`，缺 token 会 401）；Tushare 增强工具在 token 为空时不注册
- `retriever.py` 混合检索：pgvector 向量 + PostgreSQL 全文，RRF(k=60) 融合，余弦阈值 `RAG_THRESHOLD`（默认 0.2，bge-m3 命中约 0.22~0.5，调高会导致 RAG 恒 escalate 到取数分支）
- `checkpointer.py` AsyncPostgresSaver + AsyncConnectionPool(max_size 20)
- `llm.py` 统一取 LLM；`config.py` 全部走环境变量，默认 localhost

## 事件协议
向网关/前端发 `status` / `token` / `tool_start` / `tool_end` / `card` / `table` / `done` / `error`；结构化数据走独立 card/table 事件，不进 token 流。

## 部署位置
容器内 `GO_SERVER_URL` 必须用 `http://host.docker.internal:8080`。

## 相关
`mem:core`、`mem:conventions`