---
description: Go 网关（server）的约定：错误文案、WS 事件契约、限流闸门、金额精度、迁移规则
globs:
  - server/**/*.go
---

# 后端约定（server）

## 1. 错误文案必须中文，禁止透传 `err.Error()`

用户看到的是前端 `wx.showToast(data.msg)`，英文原文直接弹给用户。
当前仍存在的几处（都属于待修）：
`context deadline exceeded`（120s 超时）、`rate limit exceeded`、`too many concurrent sessions`、
`daily quota exceeded`（均为 `internal/agent/limiter.go` 的错误）、以及 `internal/agent/ws_agent.go`
超时路径的 `err.Error()`。

新增错误码时，在网关侧映射成中文短句再下发。

## 2. WS 连接级不主动断连

协议错误（非法 JSON、空 query）**只回 error 事件**，连接继续可复用。客户端可能只是格式笔误。
只有读超时 / 心跳失败 / 客户端断开才关闭连接。

## 3. 事件契约改动要同步三处

`internal/agent/event.go` 定义：token / card / table / status / tool_start / tool_end / sources / error / done。
改字段必须同步：
- `stock-agent-miniapp/pages/chat/chat.js` 的解析（`quoteCard()` / `klineCard()`）
- `server/README.md` 第三节接口总表

## 4. 限流是三道闸门 + 一个全局限流

- `internal/agent/limiter.go`：QPS=2、并发=3、日配额=500（按 user_id，Redis 键 `rl:quota:{uid}:{date}`）
- `Allow` 通过即占并发槽，**任何出口都要 `defer ReleaseConcurrent`**（它内部脱离 ctx，客户端断开也能归还）
- 限流是旁路逻辑：Redis 故障 fail-open，不拦业务请求
- HTTP 全局 `RateLimit(100, time.Minute)` 按 IP，覆盖**含登录在内的所有路由** —— 压测时容易 429

## 5. 并发写 WS 必须走 `agentConn.mu`

gorilla/websocket 不支持并发写，心跳 ping、事件推送、关闭来自不同 goroutine，
全部经 `agentConn.writeJSON/writePing/close` 串行化，不要直接碰底层 conn。

## 6. 金额一律 `shopspring/decimal`

禁止 float 参与金额运算与比较（成本价、浮动盈亏、阈值）。

## 7. 迁移是手写 SQL

放在 `migrations/00N_*.sql`，服务启动时自动执行。新增就加新序号文件，**不改已提交的文件**。

## 8. `thread_id` 由服务端派生

格式 `default:{user_id}:{session_id}`，客户端无法伪造他人会话（`ws_agent.go` 里拼装）。

## 9. 上游依赖的失败要区分对待

- 行情源取不到数据：走降级策略，不要编造数值
- ASR 上游失败：502 不透传原文；空识别结果返回 400（避免前端拿到空结果以为成功）
