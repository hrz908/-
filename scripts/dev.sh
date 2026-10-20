#!/usr/bin/env bash
# 溜来（stock-agent）常用任务入口
#
# 环境：Git Bash / MSYS2（Windows）或任意 bash
# 用法：bash scripts/dev.sh <命令>      # 不用 chmod 也能跑
#      bash scripts/dev.sh help       # 看全部命令
#
# 为什么不用 Makefile：这台机器上 make 不可用；而且 Makefile 配方行必须以 tab
# 开头，编辑器格式化或跨系统拷贝常把它换成空格，直接报 missing separator。

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SERVER_DIR="$ROOT/server"
MINIAPP_DIR="$ROOT/stock-agent-miniapp"
DB_NAME="stock_agent"

usage() {
  cat <<'EOF'
溜来 · 常用命令（bash scripts/dev.sh <命令>）

基础设施
  up            起依赖：postgres + redis + agent（等 healthy 再 dev）
  down          停依赖（数据保留在卷里）
  ps            容器状态
  logs          全部容器日志
  logs-agent    只看 agent 日志（回答慢 / 卡住时看这个）
  psql          进数据库
  redis         进 Redis（限流 rl:* 、日配额 rl:quota:*）

Go 网关（跑在宿主机，必须带 server/.env）
  dev           起网关
  build         编译
  test          单元测试
  race          单测 + 竞态检测（动过 WS / 并发代码就跑这个）
  fmt           gofmt -w
  lint          golangci-lint
  lint-install  安装 golangci-lint
  clean         清测试缓存

小程序
  miniapp       npm install（换机器首次必做）
  fix-npm       「构建 npm」之后必须跑，否则 PC 端微信白屏

其它
  help          显示本帮助
EOF
}

die() { echo "✗ $*" >&2; exit 1; }
need_compose() { command -v docker >/dev/null 2>&1 || die "没找到 docker，先启动 Docker Desktop"; }

cmd_up() { need_compose; docker compose -f "$SERVER_DIR/docker-compose.yml" up -d; cmd_ps; }
cmd_down() { need_compose; docker compose -f "$SERVER_DIR/docker-compose.yml" down; }
cmd_ps() { need_compose; docker compose -f "$SERVER_DIR/docker-compose.yml" ps; }
cmd_logs() { need_compose; docker compose -f "$SERVER_DIR/docker-compose.yml" logs -f; }
cmd_logs_agent() { need_compose; docker compose -f "$SERVER_DIR/docker-compose.yml" logs -f agent; }
cmd_psql() { need_compose; docker compose -f "$SERVER_DIR/docker-compose.yml" exec postgres psql -U postgres -d "$DB_NAME"; }
cmd_redis() { need_compose; docker compose -f "$SERVER_DIR/docker-compose.yml" exec redis redis-cli; }

cmd_dev() {
  [[ -f "$SERVER_DIR/.env" ]] || die "缺少 $SERVER_DIR/.env（被 git 忽略），没有它连不上库与 Redis"
  command -v go >/dev/null 2>&1 || die "没找到 go"
  cd "$SERVER_DIR"
  set -a; . ./.env; set +a
  exec go run .
}
cmd_build() { (cd "$SERVER_DIR" && go build -o stock-agent-server .); }
cmd_test() { (cd "$SERVER_DIR" && go test ./...); }
cmd_race() { (cd "$SERVER_DIR" && go test ./... -race); }
cmd_fmt() { (cd "$SERVER_DIR" && gofmt -w .); }
cmd_clean() { (cd "$SERVER_DIR" && go clean -testcache); }
cmd_lint() {
  command -v golangci-lint >/dev/null 2>&1 || die "没装 golangci-lint，先跑：bash scripts/dev.sh lint-install"
  (cd "$SERVER_DIR" && golangci-lint run ./...)
}
cmd_lint_install() { go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest; }

cmd_miniapp() { (cd "$MINIAPP_DIR" && npm install); }
cmd_fix_npm() { (cd "$MINIAPP_DIR" && npm run fix:npm); }

cmd="${1:-help}"
case "$cmd" in
  up)             cmd_up ;;
  down)           cmd_down ;;
  ps)             cmd_ps ;;
  logs)           cmd_logs ;;
  logs-agent)     cmd_logs_agent ;;
  psql)           cmd_psql ;;
  redis)          cmd_redis ;;
  dev)            cmd_dev ;;
  build)          cmd_build ;;
  test)           cmd_test ;;
  race)           cmd_race ;;
  fmt)            cmd_fmt ;;
  lint)           cmd_lint ;;
  lint-install)   cmd_lint_install ;;
  clean)          cmd_clean ;;
  miniapp)        cmd_miniapp ;;
  fix-npm)        cmd_fix_npm ;;
  help|-h|--help) usage ;;
  *)              echo "✗ 未知命令：$cmd" >&2; echo; usage; exit 1 ;;
esac
