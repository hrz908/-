#!/usr/bin/env bash
# ===== StockA 生产部署脚本（Ubuntu 22.04/24.04，国内服务器）=====
#
# 用法（全程 sudo）：
#   sudo bash scripts/deploy.sh install-docker   # 首次：装 Docker + nginx + 防火墙
#   sudo bash scripts/deploy.sh deploy           # 首次部署（生成 .env/证书，构建启动）
#   sudo bash scripts/deploy.sh update           # 日常更新（git pull + 重建）
#   sudo bash scripts/deploy.sh restart          # 重启全部服务
#   sudo bash scripts/deploy.sh logs [服务名]    # 看日志（server/agent/postgres/redis）
#   sudo bash scripts/deploy.sh ps               # 看容器状态
#
# 前置条件：已 git clone 本仓库到 /opt/stock-agent（见 deploy/README.prod.md 第 1 步）。

set -euo pipefail

REPO_URL="https://github.com/hrz908/-.git"
BRANCH="main"
DEPLOY_DIR="/opt/stock-agent"
COMPOSE_FILE="docker-compose.prod.yml"
SSL_DIR="/etc/nginx/ssl"

log()  { echo -e "\033[32m[deploy]\033[0m $*"; }
warn() { echo -e "\033[33m[deploy]\033[0m $*"; }
die()  { echo -e "\033[31m[deploy] 错误: $*\033[0m" >&2; exit 1; }

require_root() {
    [ "$(id -u)" -eq 0 ] || die "请用 sudo 运行：sudo bash scripts/deploy.sh $*"
}

require_repo() {
    [ -d "$DEPLOY_DIR/.git" ] || die "$DEPLOY_DIR 不是 git 仓库。先执行：git clone $REPO_URL $DEPLOY_DIR"
}

require_docker() {
    command -v docker >/dev/null 2>&1 || die "未检测到 docker，先执行：sudo bash scripts/deploy.sh install-docker"
    docker info >/dev/null 2>&1 || die "docker 未运行：systemctl start docker"
}

# ---------------------------------------------------------------- install-docker
cmd_install_docker() {
    require_root install-docker

    if command -v docker >/dev/null 2>&1; then
        log "docker 已存在，跳过安装（如需重装请手动处理）"
    else
        log "安装 docker-ce（阿里云源）..."
        apt-get update
        apt-get install -y ca-certificates curl gnupg
        install -m 0755 -d /etc/apt/keyrings
        curl -fsSL https://mirrors.aliyun.com/docker-ce/linux/ubuntu/gpg \
            -o /etc/apt/keyrings/docker.asc
        chmod a+r /etc/apt/keyrings/docker.asc
        echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://mirrors.aliyun.com/docker-ce/linux/ubuntu $(. /etc/os-release && echo "$VERSION_CODENAME") stable" \
            > /etc/apt/sources.list.d/docker.list
        apt-get update
        apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
        systemctl enable docker
        systemctl start docker
    fi

    log "配置 registry 镜像加速（拉镜像失败时可删掉 /etc/docker/daemon.json 里的 mirrors 两项）..."
    mkdir -p /etc/docker
    if [ ! -f /etc/docker/daemon.json ]; then
        cat > /etc/docker/daemon.json <<'EOF'
{
  "registry-mirrors": ["https://docker.1ms.run", "https://docker.m.daocloud.io"]
}
EOF
        systemctl restart docker
    fi

    if ! command -v nginx >/dev/null 2>&1; then
        log "安装 nginx..."
        apt-get update
        apt-get install -y nginx
        systemctl enable nginx
    else
        log "nginx 已存在，跳过安装"
    fi

    log "配置防火墙 ufw（仅放行 SSH/80/443）..."
    if ufw status 2>/dev/null | grep -q "Status: active"; then
        ufw allow OpenSSH
        ufw allow 80/tcp
        ufw allow 443/tcp
    else
        warn "ufw 未激活，跳过。请确认云厂商安全组已放行 22/80/443"
    fi

    # 把调用者加入 docker 组，以后免 sudo 用 docker（重新登录生效）
    if [ -n "${SUDO_USER:-}" ] && id "$SUDO_USER" >/dev/null 2>&1; then
        usermod -aG docker "$SUDO_USER" || true
        log "已将 $SUDO_USER 加入 docker 组（重新登录后生效）"
    fi

    log "install-docker 完成。下一步：sudo bash scripts/deploy.sh deploy"
}

# ---------------------------------------------------------------- deploy（首次）
gen_self_signed_cert() {
    if [ -f "$SSL_DIR/stock-agent.crt" ] && [ -f "$SSL_DIR/stock-agent.key" ]; then
        log "自签名证书已存在，跳过生成"
        return
    fi
    log "生成自签名证书（SAN 带公网 IP，微信开发者工具需勾选不校验域名）..."
    local ip
    ip=$(curl -fsS --max-time 5 https://api.ipify.org 2>/dev/null \
        || curl -fsS --max-time 5 https://ifconfig.me 2>/dev/null \
        || hostname -I | awk '{print $1}')
    mkdir -p "$SSL_DIR"
    openssl req -x509 -nodes -newkey rsa:2048 -days 3650 \
        -keyout "$SSL_DIR/stock-agent.key" \
        -out "$SSL_DIR/stock-agent.crt" \
        -subj "/CN=stock-agent" \
        -addext "subjectAltName=IP:${ip},IP:127.0.0.1,DNS:localhost"
    chmod 600 "$SSL_DIR/stock-agent.key"
    log "证书公网 IP: $ip"
}

cmd_deploy() {
    require_root deploy
    require_repo
    require_docker

    local env_file="$DEPLOY_DIR/deploy/.env"
    if [ ! -f "$env_file" ]; then
        cp "$DEPLOY_DIR/deploy/.env.production.example" "$env_file"
        warn "已生成 $env_file，请先编辑填入密钥（JWT_SECRET/POSTGRES_PASSWORD/REDIS_PASSWORD/WECHAT_SECRET/OPENAI_API_KEY），再重新运行 deploy"
        exit 1
    fi

    local missing=""
    local key val
    for key in JWT_SECRET POSTGRES_PASSWORD REDIS_PASSWORD WECHAT_SECRET OPENAI_API_KEY; do
        val=$(grep -E "^${key}=" "$env_file" | tail -1 | cut -d= -f2- || true)
        [ -z "$val" ] && missing="$missing $key"
    done
    [ -z "$missing" ] || die ".env 缺少必填项：$missing"

    gen_self_signed_cert

    log "安装 nginx 配置..."
    cp "$DEPLOY_DIR/deploy/nginx.prod.conf" /etc/nginx/conf.d/stock-agent.conf
    rm -f /etc/nginx/sites-enabled/default
    nginx -t
    systemctl reload nginx || systemctl start nginx

    log "构建并启动全部服务（首次约 5-15 分钟，主要在拉镜像和 pip 装依赖）..."
    cd "$DEPLOY_DIR/deploy"
    docker compose -f "$COMPOSE_FILE" up -d --build

    log "等待服务就绪..."
    local ok=0
    for _ in $(seq 1 30); do
        if curl -kfsS https://127.0.0.1/healthz >/dev/null 2>&1; then ok=1; break; fi
        sleep 2
    done
    [ "$ok" = "1" ] || die "健康检查未通过，请查看日志：sudo bash scripts/deploy.sh logs server"

    log "部署完成。验证：curl -k https://$(curl -fsS --max-time 5 https://api.ipify.org 2>/dev/null || echo '<服务器IP>')/healthz"
    log "下一步（小程序联调）：改 stock-agent-miniapp/config.js 的 develop 段为 https://<服务器IP>，详见 deploy/README.prod.md"
}

# ---------------------------------------------------------------- update
cmd_update() {
    require_root update
    require_repo
    require_docker

    log "拉取最新代码（$BRANCH）..."
    cd "$DEPLOY_DIR"
    git fetch origin "$BRANCH"
    git pull --ff-only origin "$BRANCH"

    log "重建并重启..."
    cd "$DEPLOY_DIR/deploy"
    docker compose -f "$COMPOSE_FILE" up -d --build
    docker image prune -f

    log "更新完成。状态：sudo bash scripts/deploy.sh ps"
}

# ---------------------------------------------------------------- 其它
cmd_restart() {
    require_root restart
    require_docker
    cd "$DEPLOY_DIR/deploy"
    docker compose -f "$COMPOSE_FILE" restart
    log "已重启"
}

cmd_logs() {
    require_docker
    cd "$DEPLOY_DIR/deploy"
    docker compose -f "$COMPOSE_FILE" logs -f --tail=100 "${@:-}"
}

cmd_ps() {
    require_docker
    cd "$DEPLOY_DIR/deploy"
    docker compose -f "$COMPOSE_FILE" ps
}

case "${1:-}" in
    install-docker) shift; cmd_install_docker "$@" ;;
    deploy)         shift; cmd_deploy "$@" ;;
    update)         shift; cmd_update "$@" ;;
    restart)        shift; cmd_restart "$@" ;;
    logs)           shift; cmd_logs "$@" ;;
    ps)             shift; cmd_ps "$@" ;;
    *)
        echo "用法: sudo bash scripts/deploy.sh {install-docker|deploy|update|restart|logs [服务]|ps}"
        exit 1
        ;;
esac
