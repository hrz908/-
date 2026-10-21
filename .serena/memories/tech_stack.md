# 技术栈与版本

## Go（server/）
- go.mod：`go 1.26.0`（本机 go1.27.2）
- Gin 1.9.1、GORM 1.25.7 + gorm.io/driver/postgres 1.5.7、go-redis/v9 9.5.1、gorilla/websocket 1.5.1
- shopspring/decimal 1.4.0（**金额一律用它，不用 float**）、golang-jwt/jwt v5、google/uuid、golang.org/x/text、x/sync
- lint：golangci-lint **2.15**（v2 schema，配置 `server/.golangci.yml`：standard + bodyclose）

## Python（agent/、ingest/）
- 镜像 `python:3.11-slim`（agent/pyproject.toml 只放 ruff/pyright 工具配置，**没有 [project]，不是可安装包**）
- agent：FastAPI + uvicorn、LangGraph>=0.2、langchain>=0.3、langchain-openai、langgraph-checkpoint-postgres、psycopg[binary,pool]、redis、tiktoken、jinja2（渲染 prompts/*.xml）
- ingest：psycopg、redis、watchdog、tiktoken、numpy、pymupdf4llm、pandas、python-docx、python-pptx、openai（embedding 降级）
- ruff：line-length 100，target py311，规则 E,F,I,UP,B,ASYNC

## 存储
- PostgreSQL 16 + pgvector（镜像 `pgvector/pgvector:pg16`，`CREATE EXTENSION vector` 依赖它）
- Redis 7 alpine（限流 `rl:*`、日配额 `rl:quota:*`、embedding 缓存 `emb:` TTL 7 天）

## 小程序
- 原生小程序 + `tdesign-miniprogram ^1.17.0`、marked ^4、tinycolor2、tslib

## LLM / 外部
- OpenAI 兼容协议，默认 SiliconFlow：LLM `deepseek-ai/DeepSeek-V3`、small `Qwen/Qwen2.5-7B-Instruct`、embedding `BAAI/bge-m3`（**1024 维，agent 与 ingest 必须一致**）
- 行情内置腾讯财经批量接口（`server/internal/quote/source.go`）；Tushare Pro 为可选增强（token 为空则不注册工具）

## MCP / IDE
- CodeBuddy MCP 配置 `~/.codebuddy/mcp.json`（TDesign、GitHub、Context7、postgres、serena）
- Serena 已激活本项目，语言服务器 go + python