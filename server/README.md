# 股票助手 · 后端服务（server）

技术栈：**Gin + GORM + PostgreSQL + go-redis/v9 + gorilla/websocket**

**仓库定位**：本目录与小程序工程 `stock-agent-miniapp` **平级**，是一个独立的 Go 后端服务，
不属于任何单个小程序工程。后续其它页面（或其它端）需要后端能力时，一律在**本目录内按模块扩展**，
不要再新建第二个后端工程。

当前已实现「自选股」页面所需接口：添加自选（带成本价 / 持股数量）、查询列表（含浮动盈亏与涨跌排序）、
手动刷新行情（含提醒检查与 WebSocket 推送）、提醒规则 CRUD。

---

## 一、运行

### 1）起依赖（PostgreSQL + Redis）

```bash
cd server
docker compose up -d     # 首次会自动跑 migrations/ 下的 SQL 建库建表
docker compose ps        # 等两个容器都 healthy 再起服务
```

> `docker-compose.yml` 把 `migrations/` 挂到了 `docker-entrypoint-initdb.d`，
> 只在**数据目录为空时**执行一次。之后改表请加新的迁移文件，别直接改 `001_init.sql`。

### 2）起服务

```bash
go mod tidy          # 首次需要联网拉依赖
go run .
```

冒烟验证：

```bash
curl -s http://127.0.0.1:8080/healthz
# {"code":0,"data":{"status":"ok"},"message":"success"}
```

环境变量（都有默认值，本地可直接起）：

| 变量 | 默认值 | 说明 |
|---|---|---|
| `ADDR` | `:8080` | 监听地址 |
| `DSN` | `postgres://postgres:postgres@localhost:5432/stock_agent?sslmode=disable` | PostgreSQL |
| `REDIS_ADDR` | `127.0.0.1:6379` | Redis |
| `REDIS_PASSWORD` | 空 | Redis 密码 |
| `REDIS_DB` | `0` | Redis 库 |
| `JWT_SECRET` | `please-change-me` | **上线必须改** |
| `QUOTE_API` | 空 | 自建行情服务基地址；**为空时用内置真实源**（腾讯财经批量接口，见 `quote/source.go`） |

启动时会自动建表（AutoMigrate）。表结构见 `migrations/001_init.sql`。

---

## 二、目录结构

```
server/
  main.go                       # 启动、依赖注入、优雅关闭
  internal/
    config/config.go            # 环境变量
    deps/deps.go                # 全局 db / rdb
    db/postgres.go              # GORM 连接 + 自动迁移
    db/redis.go                 # Redis 客户端
    quote/quote.go              # 行情缓存：MGET 批量读、Pipeline 批量写
    quote/source.go             # fetchStockQuote（内置腾讯真实源 / 自建源，可替换）
    resp/resp.go                # 统一响应体 + resp() 辅助函数
    middleware/middleware.go    # Recovery / RequestID / CORS / Auth / RateLimit / AccessLog
    ws/hub.go                   # WebSocket 连接中心 + Push
    hook/hook.go                # 模块间事件（行情已更新）
    stock/code.go               # 股票代码归一化 / 展示格式
    util/amount.go              # 金额与涨跌幅格式化
    module/                     # 业务模块：一个页面一个包，自带 model/handler/route
      auth/
        handler.go              # 登录：code → uid → JWT
        route.go
      watchlist/
        model.go                # UserWatchlist
        handler.go              # 添加 / 列表 / 刷新
        route.go
      alert/
        model.go                # AlertRule + 条件枚举
        handler.go              # 提醒 CRUD + checkAlerts
        route.go
    router/router.go            # 只负责挂载各模块
  migrations/001_init.sql       # 建表 SQL
```

**分层约定**：`internal/` 下除 `module/` 外都是与具体页面无关的共享基础设施；
`module/xxx/` 才是业务代码，每个模块自带 `model.go` / `handler.go` / `route.go`。

**新增一个页面的后端，三步**：

1. 新建 `internal/module/<模块名>/`，写 `model.go`（GORM 结构体 + `var Models = []any{...}`）、
   `handler.go`（业务）、`route.go`（`func Register(g *gin.RouterGroup)`）；
2. 在 `router/router.go` 里 import 并调用一次 `<模块名>.Register(authorized)`
   （公开接口则挂到未鉴权的 `api` 组上）；
3. 在 `main.go` 的 `models` 汇总处加上该模块的 `Models`，让建表覆盖到新表。

模块之间**不要互相 import**：跨模块的联动走 `internal/hook` 的事件订阅
（现在的用法：`watchlist` 刷新完广播“行情已更新”，`alert` 订阅后做提醒检查）。

---

## 三、接口总表

| 方法 | 路径 | 作用 | 需登录 |
|---|---|---|---|
| POST | `/api/v1/auth/login` | 登录发 JWT | 否 |
| GET | `/api/v1/watchlist` | 列表（含盈亏、排序） | 是 |
| POST | `/api/v1/watchlist` | 添加 / 更新成本与数量 | 是 |
| POST | `/api/v1/watchlist/refresh` | 手动刷新 + 提醒检查 | 是 |
| DELETE | `/api/v1/watchlist/:stock_code` | 移出自选（软删除） | 是 |
| POST | `/api/v1/alerts` | 新建提醒规则 | 是 |
| GET | `/api/v1/alerts` | 本人提醒列表 | 是 |
| PUT | `/api/v1/alerts/:id` | 修改提醒 | 是 |
| DELETE | `/api/v1/alerts/:id` | 删除提醒 | 是 |
| GET | `/api/v1/ws` | WebSocket 长连接 | 是 |

---

## 四、业务规则与降级策略

### 添加自选股

1. 先拿行情再写库——**行情接口失败直接返回 502，不写数据库**，避免出现「有记录但没名称」的脏数据；
2. 写库用 Upsert（`OnConflict`），基于 `(user_id, stock_code)` 唯一索引；
3. 重复添加时**只更新 `cost_price` / `quantity`（传了才更新，没传保持原值）**，不覆盖股票名称；
4. 写库成功后把行情快照写入 Redis（`stock:{code}:latest`，30 秒过期），**Redis 失败不影响接口成功返回**，只记日志。

### 查询列表

1. 基础信息来自 PostgreSQL，实时价从 Redis **一次 MGET 批量读取**（50 只自选股从 50 次往返降为 1 次）；
2. 盈亏在后端算好：
   - `profit_amount = (最新价 - 成本价) × 数量`
   - `profit_rate = (最新价 - 成本价) × 数量 ÷ (成本价 × 数量) × 100`
   - 成本价为 0（用户没填）时盈亏比例记 0，避免除零；
3. 同时返回总计 `total_profit` / `total_profit_rate` 与 `up_count` / `down_count`；
4. 排序：`filter=up` 按涨跌幅降序、`filter=down` 升序，`filter=all` 默认按加入时间倒序（也可显式传 `order=asc|desc`）；
5. `pending: true` 表示刚加入还没有行情（前端渲染 `--`），它不参与涨跌榜与统计。

### 手动刷新

1. 去重 → 按 20 只一批切分 → 批与批之间并发（上限 5），
   内置源支持一次查多只，50 只自选股只要 3 次 HTTP 往返；
2. 成功 → 写 Redis 并用新数据返回；
3. 失败 → **降级读旧缓存**，标记 `stale: true`；旧缓存也没有 → 价格 0、`pending: true`、`stale: true`；
4. 行情更新完成后才广播「行情已更新」事件，alert 模块收到后做提醒检查，避免拿旧价误判；
5. **本接口不写 `user_watchlist` 表**（列表没变化，只更新缓存）；
6. 返回结构与 `GET /watchlist` 一致（`{list, total_profit, ...}`），前端可直接整段替换；
7. 不使用 cron，所有行情拉取由前端主动触发。

### 移出自选

- 走**软删除**：只把 `enabled` 置为 false，不物理删行，
  保留成本价等建仓信息，重新添加时自动恢复（Upsert 会带上 `enabled`）；
- 更新必须带 `user_id` 过滤，防止改到别人的记录；
- 查不到统一 404，不区分「不存在」与「非本人」。

### 提醒规则

- 条件枚举：`price_gte`（最新价 ≥ 目标价）、`price_lte`（最新价 ≤ 目标价）；
- 同一用户、同一股票、同一方向只允许一条**启用**规则，重复创建返回 409；
- `PUT` / `DELETE` 一律用 `id + user_id` 双条件定位，查不到统一 404，不区分「不存在」与「非本人」，防止枚举探测；
- 改了 `condition_value` / `condition_type` 会清空 `last_triggered_at`，让规则能重新触发；
- 删除或停用时 `DEL alert:lock:{user_id}:{stock_code}`，避免残留防抖锁挡住后续推送；
- 推送防重复：`SetNX alert:lock:{user_id}:{stock_code}`，TTL 60 秒，写不进去就跳过。

### 金额精度

所有价格、成本、盈亏的计算一律使用 `github.com/shopspring/decimal`，**禁止用 `float64` 做加减乘除**（否则会出现 `3682.4000000000005` 这类误差）。对外输出的金额统一 `StringFixed(2)`，成本价 `StringFixed(4)`。

### 行情源

**默认开箱即用**：没配 `QUOTE_API` 时走内置的真实行情源——腾讯财经批量接口
`https://qt.gtimg.cn/q=sh600519,sz000001`。它返回 GBK 文本（用 `golang.org/x/text` 转 UTF-8），
字段以 `~` 分隔，我们取下标 `1`（名称）、`3`（最新价）、`32`（涨跌幅）。

**换成自建源**：设 `QUOTE_API` 即可，协议约定：

```http
GET {QUOTE_API}?code=SH600519
→ {"code":"SH600519","name":"贵州茅台","price":"1329.00","change_percent":"1.22"}
```

数值一律用字符串传递，避免中间环节走 float64 丢失精度。

**拉取策略**：去重 → 按 20 只一批切分 → 批与批之间并发（上限 5）。
内置源支持一次查多只，所以 50 只自选股只要 3 次 HTTP 往返，而不是 50 次。

**异常处理**：

- 单次请求超时 3 秒，第三方卡住不会拖死刷新接口；
- HTTP 客户端全局复用（连接池），避免批量刷新时反复建连；
- 单批失败只影响批内 code，其余照常返回，由调用方降级；
- 停牌 / 退市 / 代码不存在时价格字段为空，解析阶段直接丢弃，不会写入脏数据。

---

## 五、与小程序自选股页面的字段映射

页面列表项当前字段 → 接口返回：

| 前端字段 | 接口字段 | 说明 |
|---|---|---|
| `name` | `name` | 股票名称 |
| `code` | `code` | 展示格式 `"SH 600519"`，与前端一致 |
| `price` | `price_text` | 带千分位，如 `1,684.50` |
| `pct` | `pct_text` | 带符号，如 `+1.12%` |
| `up` | `up` | 涨跌幅 > 0 |
| `cost` | `cost_price` | 成本价（元） |
| `qty` | `quantity` | 持股数量（股） |
| `pending` | `pending` | 无行情时展示 `--` |
| — | `profit_amount` / `profit_rate` | 后端算好的浮动盈亏 |
| — | `stale` | true 表示价格来自旧缓存 |

前端 Tab 与 query 参数：`全部 → filter=all`、`涨幅榜 → filter=up`、`跌幅榜 → filter=down`。

---

## 六、curl 测试

> 先登录拿 token：
> `TOKEN=$(curl -s -X POST http://localhost:8080/api/v1/auth/login -H 'Content-Type: application/json' -d '{"code":"demo"}' | python -c "import sys,json;print(json.load(sys.stdin)['data']['token'])")`

**1）正常添加（带成本价与数量）**

```bash
curl -s -X POST http://localhost:8080/api/v1/watchlist \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"stock_code":"SH600519","cost_price":"1680.50","quantity":100}'
```

**2）重复添加并更新成本价（Upsert，不报错）**

```bash
curl -s -X POST http://localhost:8080/api/v1/watchlist \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"stock_code":"SH 600519","cost_price":"1700.00","quantity":200}'
```

**3）无效代码（返回 400）**

```bash
curl -s -X POST http://localhost:8080/api/v1/watchlist \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"stock_code":"99999999"}'
```

**4）查询列表（涨幅榜）**

```bash
curl -s "http://localhost:8080/api/v1/watchlist?filter=up" -H "Authorization: Bearer $TOKEN"
```

**5）手动刷新（正常）**

```bash
curl -s -X POST http://localhost:8080/api/v1/watchlist/refresh -H "Authorization: Bearer $TOKEN"
```

**6）无自选股时刷新（返回空数组）**

```bash
# 换一个未添加过自选的账号 token 后再执行
curl -s -X POST http://localhost:8080/api/v1/watchlist/refresh -H "Authorization: Bearer $TOKEN2"
```

**7）新建提醒规则**

```bash
curl -s -X POST http://localhost:8080/api/v1/alerts \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"stock_code":"SH600519","condition_type":"price_gte","condition_value":"1800.00"}'
```

**8）重复方向（返回 409）**

```bash
curl -s -X POST http://localhost:8080/api/v1/alerts \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"stock_code":"SH600519","condition_type":"price_gte","condition_value":"1850.00"}'
```

**9）越权改他人规则（返回 404）**

```bash
# 用 TOKEN2 去改 TOKEN 的规则 id=1
curl -s -X PUT http://localhost:8080/api/v1/alerts/1 \
  -H "Authorization: Bearer $TOKEN2" -H 'Content-Type: application/json' \
  -d '{"enabled":false}'
```

**10）删除规则**

```bash
curl -s -X DELETE http://localhost:8080/api/v1/alerts/1 -H "Authorization: Bearer $TOKEN"
```

---

## 七、小程序对接

小程序工程在 `../stock-agent-miniapp`（与本目录平级）。

**请求基地址**：`stock-agent-miniapp/config/index.js`。
小程序没有 `.env`，官方推荐用 `envVersion` 区分环境，我们照此办理：

| envVersion | 含义 | apiBase |
|---|---|---|
| `develop` | 开发版（开发者工具 / 真机调试） | `http://127.0.0.1:8080` |
| `trial` | 体验版 | `https://trial-api.example.com` |
| `release` | 正式版 | `https://api.example.com` |

换环境、换机器只改这一张表，其余文件一律从 `config` 取。

> 开发阶段需在开发者工具「详情 → 本地设置」勾选 **不校验合法域名**，
> 否则请求 `127.0.0.1` 会被拦截。正式上线前把地址换成已备案域名并去掉该勾选。

**Token**：`utils/request.js` 全权负责

1. 首次发请求前先 `wx.login()` 拿 code → `POST /api/v1/auth/login` 换 JWT；
2. JWT 写入本地缓存（`token` / `user_id`），后续请求自动带 `Authorization: Bearer <token>`；
3. 请求返回 **401 时静默重新登录一次并重放原请求**，用户不会看到「未登录」；
4. 并发请求共享同一个登录 Promise，不会重复触发 `wx.login`。

**调用约定**：页面只调 `services/`，不直接碰 `wx.request`。
`services/watchlist.js` 暴露 `fetchList` / `add` / `refresh` / `remove` 四个方法。
