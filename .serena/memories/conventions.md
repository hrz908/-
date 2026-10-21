# 代码约定

## Go（server/）
- 分层：`internal/` 下除 `module/` 全是共享基础设施；`module/xxx/` 每模块自带 `model.go` + `handler.go` + `route.go`（`func Register(g *gin.RouterGroup)`）
- 加新页面后端三步：建 `module/<名>/` → `router/router.go` 里 Register → `main.go` 的 models 汇总加上（否则不建表）
- **模块之间禁止互相 import**；跨模块联动走 `internal/hook` 事件订阅（现用法：watchlist 刷新完广播「行情已更新」，alert 订阅做提醒检查）
- 金额用 `shopspring/decimal`；响应统一走 `internal/resp`；限流/鉴权/CORS 在 `internal/middleware`
- 迁移：`migrations/` 只在**数据卷为空时**执行一次（挂到 docker-entrypoint-initdb.d），改表必须新增迁移文件，禁止改已有的
- 配置：数值型环境变量解析失败**直接终止启动**（不静默回退）；`ENV=prod` 时 JWT_SECRET 或微信凭据仍是默认值则拒绝启动

## 行尾陷阱（Windows 必知）
- 本机 `core.autocrlf=true` → Go 文件检出为 CRLF，gofmt 只认 LF，导致 **44 个文件里 41 个报未格式化**
- 修法：在 `server/` 加 `.gitattributes` 写 `*.go text eol=lf` 后 `git add --renormalize .`；临时可用 `gofmt -w .`
- golangci-lint 默认 `max-same-issues: 3`，同类问题只显示 3 条，**看全量要加 `--max-same-issues=0`**

## Python agent
- 意图路由：`mcp`（要实时/结构化数据，走工具）/`rag`（查知识库）/`chat`/`clarify`
- RAG 不得编造；未命中要如实说明；无数据且命中行情关键词时走固定兜底文案
- 行情/ K 线每轮必须重新取数，**禁止沿用历史消息里的数字**（等于给过期答案）
- 工具子图轮次上限 3；无数据不得自由发挥
- 容器里访问宿主机 Go 服务必须用 `host.docker.internal`（不是 127.0.0.1）

## 小程序
- 颜色一律 `var(--c-blue)` `#2559EB`，**禁用 TDesign 默认主色 #0052D9**
- 改输入栏 / 弹窗 / 手势 / WS 消息渲染前必读 `.codebuddy/rules/miniapp-gotchas.md`
- 昵称输入：`nicknameFocus` 必须镜像真实焦点（收键盘用 `wx.hideKeyboard()`）；`type=nickname` 只在「取微信昵称」期间存在

## 提交
- Conventional Commits + 中文正文，scope 用中文模块名：`feat(小程序): `、`fix(server): `、`chore: `
- 微信开发者工具**服务端口已关闭**，不做命令行自动化验证，改完手动编译查看