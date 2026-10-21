# 溜来（stock-agent）· 源码地图

顶层平级模块，无根 README，各模块自管：
- `server/` Go 网关（Gin+GORM+PG+Redis+WS）。与小程序**平级**，不是小程序的子工程；新后端能力一律在此按模块扩展，禁止新建第二个后端工程 → `mem:server/core`
- `agent/` Python 智能体（FastAPI + LangGraph）→ `mem:agent/core`
- `stock-agent-miniapp/` 微信小程序（原生 + TDesign）→ `mem:miniapp/core`
- `ingest/` 文档入库（解析→分块→embedding→pgvector）。独立服务，compose 中属 `ingest` profile，默认不启动
- `deploy/` nginx.conf + 部署说明；`scripts/dev.sh` 统一命令入口（本机无 make）
- `.codebuddy/rules/` 三条强制约定：`server-conventions.md`、`agent-graph.md`、`miniapp-gotchas.md`

## 全局不变量
- 品牌名「溜来」；`new-projects` 只是目录名
- 一套 PostgreSQL(pgvector)+Redis 供全部服务，唯一真源是 `server/docker-compose.yml`
- 端口：Go 8080 / agent 8000 / pg 5432 / redis 6379
- 所有服务默认凭据 `postgres:postgres`、库名 `stock_agent`、`sslmode=disable`
- 本机 Windows：默认终端 shell 是 PowerShell 7（`pwsh`），`/c/...` 这类 MSYS 路径写法不可用；`make` 不存在

## 相关
技术选型 `mem:tech_stack`；命令 `mem:suggested_commands`；约定 `mem:conventions`；收尾 `mem:task_completion`