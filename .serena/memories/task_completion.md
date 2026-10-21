# 收尾检查清单

## 动了 Go（server/）
```
cd server
gofmt -w .
go build ./...
go test ./...
golangci-lint run ./...
```
- 动过 WS / 并发代码：`go test ./... -race`
- gofmt 结果要先排除 CRLF 误报，见 `mem:conventions`
- 服务起不来先查 `server/.env` 是否存在

## 动了 Python agent
- `ruff check .`（配置在 `agent/pyproject.toml`，line-length 100）；无单测
- 起服务看日志：`docker compose -f server/docker-compose.yml logs -f agent`

## 动了 ingest
- 入库验证：`psql` 里 `select title, embed_model, chunk_count, status from documents;` / `select count(*) from chunks;`

## 动了小程序
- 微信开发者工具手动编译 + 预览（服务端口已关闭，无法 CLI 自动化）
- 若执行过「构建 npm」：`npm run fix:npm`，否则 PC 端白屏

## 提交前
- `git status` / `git diff` 自查
- 不要提交：`server/.env`、`server/tmp_smoke/`、`.playwright-cli/`、`stock-agent-miniapp/miniprogram_npm/`
- 冒烟产物 `server/tmp_smoke/` 已被 gitignore（混进向量库会污染 RAG 检索）