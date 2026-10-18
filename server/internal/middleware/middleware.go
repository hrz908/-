package middleware

import (
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"stock-agent-server/internal/auth"
	"stock-agent-server/internal/deps"
	"stock-agent-server/internal/resp"
)

const requestIDKey = "request_id"

// Recovery panic 恢复：单请求崩溃不拖垮进程，统一回 code:500
func Recovery() gin.HandlerFunc {
	return gin.CustomRecovery(func(c *gin.Context, recovered any) {
		log.Printf("rid=%s panic: %v", RequestIDFrom(c), recovered)
		resp.Fail(c, resp.CodeInternal, "服务内部错误")
		c.Abort()
	})
}

// RequestID 透传或生成 X-Request-ID，写进 context 与响应头，便于日志追踪
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := strings.TrimSpace(c.GetHeader("X-Request-ID"))
		if id == "" {
			id = uuid.NewString()
		}
		c.Set(requestIDKey, id)
		c.Writer.Header().Set("X-Request-ID", id)
		c.Next()
	}
}

// RequestIDFrom 取当前请求的 X-Request-ID（日志用）
func RequestIDFrom(c *gin.Context) string {
	if v, ok := c.Get(requestIDKey); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// CORS 放行小程序域名的跨域请求，并直接吃掉 OPTIONS 预检
func CORS() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "GET,POST,PUT,DELETE,OPTIONS")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type,Authorization,X-Request-ID")
		c.Writer.Header().Set("Access-Control-Max-Age", "86400")
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

// Auth 解析 Authorization: Bearer <token>，校验通过后把 user_id 注入 context。
// user_id 只从这里取，禁止从请求体 / query 读取，防止越权。
// 例外：WebSocket 握手无法自定义请求头，故兜底支持 ?token=
func Auth() gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, err := auth.ParseToken(bearer(c))
		if err != nil || uid <= 0 {
			resp.Fail(c, resp.CodeUnauthorized, "未登录或登录已失效")
			c.Abort()
			return
		}
		c.Set("user_id", uid)
		c.Next()
	}
}

func bearer(c *gin.Context) string {
	h := c.GetHeader("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return strings.TrimSpace(c.Query("token"))
}

// RateLimit 基于 Redis 有序集合的滑动窗口限流（按 IP）
func RateLimit(limit int64, window time.Duration) gin.HandlerFunc {
	var seq int64
	return func(c *gin.Context) {
		rdb := deps.RDB
		// 探针几秒一次，不该被限流计数挡下
		if rdb == nil || limit <= 0 || c.FullPath() == "/healthz" {
			c.Next()
			return
		}
		ctx := c.Request.Context()
		key := "rl:" + c.ClientIP()
		now := time.Now().UnixMilli()
		pipe := rdb.Pipeline()
		pipe.ZRemRangeByScore(ctx, key, "0", strconv.FormatInt(now-window.Milliseconds(), 10))
		cnt := pipe.ZCard(ctx, key)
		// member 必须唯一，否则同毫秒内多次请求会被 ZADD 覆盖而漏计
		member := strconv.FormatInt(now, 10) + "-" + strconv.FormatInt(atomic.AddInt64(&seq, 1), 36) + "-" + uuid.NewString()
		pipe.ZAdd(ctx, key, redis.Z{Score: float64(now), Member: member})
		pipe.Expire(ctx, key, window)
		if _, err := pipe.Exec(ctx); err != nil {
			// 限流是旁路逻辑，Redis 故障不拦业务请求
			log.Printf("rid=%s rate limit error, skip: %v", RequestIDFrom(c), err)
		} else if cnt.Val() >= limit {
			resp.Fail(c, resp.CodeTooManyRequests, "请求过于频繁，请稍后再试")
			c.Abort()
			return
		}
		c.Next()
	}
}

// AccessLog 记录 method / path / status / latency / requestID
func AccessLog() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		log.Printf("rid=%s %s %s status=%d latency=%s",
			RequestIDFrom(c), c.Request.Method, c.FullPath(), c.Writer.Status(), time.Since(start))
	}
}
