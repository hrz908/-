package middleware

import (
	"log"
	"net/http"
	"runtime/debug"
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

const (
	requestIDKey = "request_id"

	// requestIDMaxLen 客户端 X-Request-ID 长度上限：不限制会让超长值/控制字符进日志，
	// 造成日志注入与洪水
	requestIDMaxLen = 64
)

// Recovery panic 恢复：单请求崩溃不拖垮进程，统一回 code:500。
func Recovery() gin.HandlerFunc {
	return gin.CustomRecovery(func(c *gin.Context, recovered any) {
		// 带上堆栈，否则线上 panic 只有一行消息、无从定位
		log.Printf("rid=%s panic: %v\n%s", RequestIDFrom(c), recovered, debug.Stack())
		resp.Fail(c, resp.CodeInternal, "服务内部错误")
		c.Abort()
	})
}

// RequestID 透传或生成 X-Request-ID，写入 context 与响应头，便于日志追踪。
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := sanitizeRequestID(c.GetHeader("X-Request-ID"))
		if id == "" {
			id = uuid.NewString()
		}
		c.Set(requestIDKey, id)
		c.Writer.Header().Set("X-Request-ID", id)
		c.Next()
	}
}

// sanitizeRequestID 只留可打印 ASCII 并限长，其余视为未传（重新生成）。
func sanitizeRequestID(raw string) string {
	id := strings.TrimSpace(raw)
	if id == "" || len(id) > requestIDMaxLen {
		return ""
	}
	for i := 0; i < len(id); i++ {
		if id[i] < 0x20 || id[i] > 0x7e {
			return ""
		}
	}
	return id
}

// RequestIDFrom 取当前请求的 X-Request-ID（日志用）。
func RequestIDFrom(c *gin.Context) string {
	if v, ok := c.Get(requestIDKey); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// CORS 放行跨域并直接吞掉 OPTIONS 预检；origins 为逗号分隔白名单，* 表示全部（默认）。
// 目前用 Bearer token，白名单影响不大；但若某天改用 Cookie 鉴权，
// 通配符会使 Allow-Credentials 失效且极难排查，故按白名单实现。
func CORS(origins string) gin.HandlerFunc {
	allow := parseOrigins(origins)

	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if allow[origin] || allow["*"] {
			c.Writer.Header().Set("Access-Control-Allow-Origin", origin)
			c.Writer.Header().Set("Vary", "Origin")
		}
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

func parseOrigins(raw string) map[string]bool {
	out := map[string]bool{}
	for _, o := range strings.Split(raw, ",") {
		o = strings.TrimSpace(o)
		if o != "" {
			out[o] = true
		}
	}
	if len(out) == 0 {
		out["*"] = true
	}
	return out
}

// Auth 解析 Authorization: Bearer <token>，通过后把 user_id 注入 context。
// user_id 只从这里取，禁止从请求体 / query 读取，防越权。
func Auth() gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, err := auth.ParseToken(bearer(c, false))
		if err != nil || uid <= 0 {
			resp.Fail(c, resp.CodeUnauthorized, "未登录或登录已失效")
			c.Abort()
			return
		}
		c.Set("user_id", uid)
		c.Next()
	}
}

// AuthAllowQueryToken 同 Auth，额外允许 ?token= 兜底，仅供 WS 握手（无法带自定义请求头）。
// token 会留在代理日志与浏览器历史，故不对普通接口开放。
func AuthAllowQueryToken() gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, err := auth.ParseToken(bearer(c, true))
		if err != nil || uid <= 0 {
			resp.Fail(c, resp.CodeUnauthorized, "未登录或登录已失效")
			c.Abort()
			return
		}
		c.Set("user_id", uid)
		c.Next()
	}
}

func bearer(c *gin.Context, allowQuery bool) string {
	h := c.GetHeader("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	if allowQuery {
		return strings.TrimSpace(c.Query("token"))
	}
	return ""
}

// windowScript 滑动窗口限流：先清理窗口外记录再计数，「放行才写入」，
// 被 429 的请求不占窗口容量（旧实现先 ZAdd 后判断，被拒请求仍抬升计数，阈值偏紧）。
// 读-判-写在一段 Lua 内完成，杜绝并发竞态。
var windowScript = redis.NewScript(`
local key = KEYS[1]
local now = tonumber(ARGV[1])
local window_ms = tonumber(ARGV[2])
local limit = tonumber(ARGV[3])
local member = ARGV[4]

redis.call('ZREMRANGEBYSCORE', key, 0, now - window_ms)
local cnt = redis.call('ZCARD', key)
local ttl = math.ceil(window_ms / 1000)
if cnt >= limit then
    redis.call('EXPIRE', key, ttl)
    return 0
end
redis.call('ZADD', key, now, member)
redis.call('EXPIRE', key, ttl)
return 1
`)

// RateLimit 基于 Redis 有序集合的滑动窗口限流。
// name 区分限流域（"ip" / "login" / "asr" 互不共享窗口），ident 为限流主体（IP 或用户 ID）。
func RateLimit(name, ident string, limit int64, window time.Duration) gin.HandlerFunc {
	var seq int64
	return func(c *gin.Context) {
		rdb := deps.RDB
		// 探针几秒一次，不该被限流计数挡下
		if rdb == nil || limit <= 0 || c.FullPath() == "/healthz" {
			c.Next()
			return
		}
		ctx := c.Request.Context()
		key := "rl:" + name + ":" + ident
		now := time.Now().UnixMilli()
		// member 必须唯一，否则同毫秒内多次请求会被 ZADD 覆盖而漏计
		member := strconv.FormatInt(now, 10) + "-" + strconv.FormatInt(atomic.AddInt64(&seq, 1), 36) + "-" + uuid.NewString()
		allowed, err := windowScript.Run(ctx, rdb, []string{key},
			now, window.Milliseconds(), limit, member).Int()
		if err != nil {
			// 限流是旁路逻辑，Redis 故障不拦业务请求
			log.Printf("rid=%s rate limit error, skip: %v", RequestIDFrom(c), err)
		} else if allowed == 0 {
			resp.Fail(c, resp.CodeTooManyRequests, "请求过于频繁，请稍后再试")
			c.Abort()
			return
		}
		c.Next()
	}
}

// IPRateLimit 全局按 IP 限流。
func IPRateLimit(limit int64, window time.Duration) gin.HandlerFunc {
	return ipRateLimitNamed("ip", limit, window)
}

// LoginRateLimit 登录接口专用按 IP 限流，与全局档分桶：
// 全局档放行后，同一 IP 仍需单独过这道更严的闸。
func LoginRateLimit(limit int64, window time.Duration) gin.HandlerFunc {
	return ipRateLimitNamed("login", limit, window)
}

func ipRateLimitNamed(name string, limit int64, window time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		RateLimit(name, c.ClientIP(), limit, window)(c)
	}
}

// UserRateLimit 按登录用户限流（user_id 由 Auth 注入），取不到时回退按 IP，
// 保证公开路由挂上也有保护。用于消耗上游资源的接口：行情/搜索、ASR。
func UserRateLimit(limit int64, window time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		ident := ""
		if uid, ok := c.Get("user_id"); ok {
			ident = "u:" + strconv.FormatInt(uid.(int64), 10)
		}
		if ident == "" {
			ident = "ip:" + c.ClientIP()
		}
		RateLimit("user", ident, limit, window)(c)
	}
}

// AccessLog 记录 method / path / status / latency / requestID。
func AccessLog() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		log.Printf("rid=%s %s %s status=%d latency=%s",
			RequestIDFrom(c), c.Request.Method, c.FullPath(), c.Writer.Status(), time.Since(start))
	}
}
