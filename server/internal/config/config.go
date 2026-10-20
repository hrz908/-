package config

import (
	"log"
	"os"
	"strconv"
)

// Config 运行期配置，全部来自环境变量，便于本地 / 容器切换
type Config struct {
	Addr          string // HTTP 监听地址
	DSN           string // PostgreSQL 连接串
	RedisAddr     string
	RedisPassword string
	RedisDB       int
	JWTSecret     string
	QuoteAPI      string // 行情接口基地址；为空时用内置腾讯源
	WechatAppID   string // 小程序 appid；与 Secret 同时为空时，dev 下登录退化为演示账号
	WechatSecret  string // 小程序 secret
	WeComCorpID   string // 企业微信企业 ID（corpid）；与下面两项同时配置才启用提醒推送
	WeComSecret   string // 企业微信自建应用 secret
	WeComAgentID  int    // 企业微信自建应用 agentid
	UploadDir     string // 上传文件落盘目录，静态路由前缀 /uploads/avatars
	Env           string // dev（默认）/ prod。prod 下强制要求 JWT_SECRET 与微信凭据
	AllowOrigins  string // CORS 允许的 Origin，逗号分隔；* 表示全部（默认）
	AgentBaseURL  string // Python Agent 服务基地址，网关反向代理到该地址的 /run/stream
}

func Load() Config {
	return Config{
		Addr:          env("ADDR", ":8080"),
		DSN:           env("DSN", "postgres://postgres:postgres@localhost:5432/stock_agent?sslmode=disable"),
		RedisAddr:     env("REDIS_ADDR", "127.0.0.1:6379"),
		RedisPassword: env("REDIS_PASSWORD", ""),
		RedisDB:       envInt("REDIS_DB", 0),
		JWTSecret:     env("JWT_SECRET", "please-change-me"),
		QuoteAPI:      env("QUOTE_API", ""),
		WechatAppID:   env("WECHAT_APPID", ""),
		WechatSecret:  env("WECHAT_SECRET", ""),
		// 企业微信自建应用凭据：默认值即用户提供的三个 ID，开箱可用；
		// 生产环境建议用环境变量覆盖，尤其是 secret（敏感，勿提交到公开仓库）
		WeComCorpID:  env("WECOM_CORP_ID", "wwb79b9d374733e09e"),
		WeComSecret:  env("WECOM_SECRET", "2XyncvexPia7u-EIUr3xGerRrbluXw-oRmvdTW23NaA"),
		WeComAgentID: envInt("WECOM_AGENT_ID", 1000002),
		UploadDir:    env("UPLOAD_DIR", "./uploads"),
		Env:          env("ENV", "dev"),
		AllowOrigins: env("ALLOW_ORIGINS", "*"),
		AgentBaseURL: env("AGENT_BASE_URL", "http://127.0.0.1:8000"),
	}
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		// 写错环境变量时静默回退默认值，问题会藏到运行期才暴露，直接启动即失败
		log.Fatalf("invalid %s=%q: %v", key, v, err)
	}
	return n
}
