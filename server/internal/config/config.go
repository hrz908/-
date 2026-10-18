package config

import (
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
	QuoteAPI      string // 行情接口基地址；为空时返回演示数据
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
		return def
	}
	return n
}
