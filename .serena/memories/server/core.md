# server/（Go 网关）

Gin + GORM + PostgreSQL + go-redis/v9 + gorilla/websocket。与小程序工程平级的独立后端。
结构细节与接口总表见 `server/README.md`。

## 目录要点
- `main.go` 启动/依赖注入/优雅关闭 + models 汇总；`internal/config/config.go` 环境变量；`internal/deps` 全局 db/rdb
- `internal/quote/` 行情缓存（MGET 批量读、Pipeline 批量写）+ `source.go` 内置腾讯真实源（`QUOTE_API` 为空时用内置）
- `internal/ws/hub.go` WS 连接中心与推送；`internal/hook/` 模块间事件；`internal/resp/` 统一响应体；`internal/util/amount.go` 金额与涨跌幅格式化
- `internal/module/` 一个页面一个包（auth / watchlist / alert / ...），自带 model/handler/route
- `migrations/` 001 建表、002 用户表、003 alert_rule 升级、004 唯一索引、005 agent（documents/chunks/agent_runs + pgvector hnsw 索引）

## 关键行为
- AutoMigrate 自动建表；SQL 迁移只在数据卷为空时执行一次
- 接口前缀 `/api/v1`；JWT 鉴权；`AuthAllowQueryToken` 允许 query 带 token；限流 middleware（Redis 故障 fail-open）
- 默认 DSN `postgres://postgres:postgres@localhost:5432/stock_agent?sslmode=disable`，Redis `127.0.0.1:6379`
- ASR：`/api/v1/asr/transcribe` 走 SiliconFlow 的 SenseVoiceSmall，密钥回退 `OPENAI_API_KEY`
- 扩展方式、模块隔离、迁移规则见 `mem:conventions`

## 相关
`mem:core`、`mem:conventions`、`mem:suggested_commands`