# Go 网关（stock-agent-server）多阶段构建
#
# 构建（在仓库根执行）：
#   docker build -f server/Dockerfile -t stock-agent-server ./server
# 生产 compose（deploy/docker-compose.prod.yml）已带好 GOPROXY build arg。
#
# 说明：
#   - 纯 Go 依赖（gin/go-redis/pgx 系均无 CGO），CGO_ENABLED=0 静态编译，
#     直接用 alpine 跑，无需 glibc 兼容层；
#   - 带 ca-certificates：网关要直连腾讯行情源 / 微信 code2session / 硅基流动 ASR；
#   - 配置全部走环境变量（server/internal/config：无前缀 + 点转下划线），
#     不需要 config.yaml，容器里 WORKDIR 保持干净。

FROM golang:1.26 AS builder

WORKDIR /build

# 国内服务器拉 Go 模块默认走 proxy.golang.org 会超时，必须换 goproxy.cn；
# 海外网络可构建时传 --build-arg GOPROXY=https://proxy.golang.org,direct 覆盖
ARG GOPROXY=https://goproxy.cn,direct
ENV GOPROXY=${GOPROXY} \
    CGO_ENABLED=0 \
    GOOS=linux

# 先只拷 go.mod/go.sum，依赖下载层可被缓存（改业务代码不重新下模块）
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go build -trimpath -ldflags="-s -w" -o /build/stock-agent-server .

FROM alpine:3.21

RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app
COPY --from=builder /build/stock-agent-server /app/stock-agent-server

# 上传目录：生产用卷挂载覆盖（见 docker-compose.prod.yml 的 uploads volume）
RUN mkdir -p /app/uploads

EXPOSE 8080

ENTRYPOINT ["/app/stock-agent-server"]
