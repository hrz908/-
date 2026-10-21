# 常用命令

## 基础设施（Docker）
- 起依赖：`docker compose -f server/docker-compose.yml up -d`（postgres/redis/agent，等 healthy 再起服务）
- 停：`docker compose -f server/docker-compose.yml down`（数据保留在卷里；`-v` 才删卷）
- 状态/日志：`... ps` / `... logs -f` / `... logs -f agent`
- 进库：`docker compose -f server/docker-compose.yml exec postgres psql -U postgres -d stock_agent`
- 进 Redis：`... exec redis redis-cli`
- ingest 属于 profile：`docker compose --profile ingest up -d`（监听目录容器内为 `/data/docs`）

## 统一入口（需 bash，本机无 make）
`bash scripts/dev.sh <cmd>`：`up|down|ps|logs|logs-agent|psql|redis|dev|build|test|race|fmt|lint|lint-install|clean|miniapp|fix-npm|help`
PowerShell 里执行：`bash scripts/dev.sh dev`

## Go（server/）
- 起服务：`cd server && go run .`（**必须存在 server/.env**，被 git 忽略，缺失直接 die）
- 编译：`go build -o stock-agent-server .`
- 测试：`go test ./...`；动过 WS/并发再加 `-race`
- 格式/静态检查：`gofmt -w .`、`golangci-lint run ./...`
- 冒烟：`curl -s http://127.0.0.1:8080/healthz` → `{"code":0,..."status":"ok"}`

## Python
- agent：`cd agent && uvicorn main:app --host 0.0.0.0 --port 8000 --reload`（需 OPENAI_API_BASE/KEY）
- ingest：`python main.py scan|watch|once <path>|reembed --model <m>`

## 小程序
- `npm install`（换机器首次必做）
- `npm run fix:npm`（**「构建 npm」之后必须跑**，否则 PC 端微信白屏）

## Windows 差异（相对 unix shell）
- 路径：`Set-Location 'C:\Users\...'`，**`/c/...` 不存在**
- `where` → `Get-Command`；`grep|sed|rm -rf` 等需走 bash 或换 PowerShell 等价
- winget 装完软件后当前会话 PATH 不刷新，需新开终端
- 装 golangci-lint：`winget install --id GolangCI.golangci-lint --exact`（go install 装的是 v1，与 v2 配置不兼容）