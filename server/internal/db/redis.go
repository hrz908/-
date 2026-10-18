package db

import (
	"context"
	"log"

	"github.com/redis/go-redis/v9"
)

// NewRedis 建立 Redis 连接。
// Redis 在本项目只承担缓存与限流，连不上不阻断启动，业务侧自行降级
func NewRedis(ctx context.Context, addr, password string, db int) *redis.Client {
	client := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: password,
		DB:       db,
	})
	if err := client.Ping(ctx).Err(); err != nil {
		log.Printf("redis ping failed (degrade mode): %v", err)
	}
	return client
}
