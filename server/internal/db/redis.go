package db

import (
	"context"
	"log"
	"time"

	"github.com/redis/go-redis/v9"
)

// redisPingTimeout 启动探活超时。
// 不能沿用 main 的 signal context：Redis 挂起时会一直拖住启动流程。
// 与 postgres.go 里的 pingTimeout 同名会冲突，加前缀区分
const redisPingTimeout = 3 * time.Second

// NewRedis 建立 Redis 连接。
// Redis 在本项目只承担缓存与限流，连不上不阻断启动，业务侧自行降级
func NewRedis(_ context.Context, addr, password string, db int) *redis.Client {
	client := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: password,
		DB:       db,
	})
	ctx, cancel := context.WithTimeout(context.Background(), redisPingTimeout)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		log.Printf("redis ping failed (degrade mode): %v", err)
	}
	return client
}
