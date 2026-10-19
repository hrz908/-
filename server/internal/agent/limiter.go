package agent

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/redis/go-redis/v9"
)

// 三道闸门各自对应的错误。调用方据此映射 error code 回传前端，
// 限流是「拒绝本次请求」而非「断开连接」，因此用结构化错误而非 panic/断连。
var (
	ErrRateLimit         = errors.New("rate limit exceeded")
	ErrTooManyConcurrent = errors.New("too many concurrent sessions")
	ErrQuotaExceeded     = errors.New("daily quota exceeded")
)

// Limits 限流参数。三道闸门：QPS（令牌桶）、并发（计数）、日配额（计数+过期）。
type Limits struct {
	QPS        int // 每秒令牌数，同时作为桶容量
	Concurrent int // 单用户并发运行数
	DailyQuota int // 单用户每日请求配额
}

// DefaultLimits 默认限流档位：2/s、3 并发、500/天。
var DefaultLimits = Limits{QPS: 2, Concurrent: 3, DailyQuota: 500}

// Limiter 基于 Redis 的限流器。所有计数落在 Redis，天然支持多实例网关共享配额。
type Limiter struct {
	r      *redis.Client
	limits Limits
}

func NewLimiter(r *redis.Client, limits Limits) *Limiter {
	return &Limiter{r: r, limits: limits}
}

// concKeyTTL 并发计数键的兜底过期时间。
// 正常路径下请求结束会 DECR 归还；万一进程崩溃漏还，靠 TTL 自动清零，
// 避免某个用户的并发槽被永久占死。取值远大于单次运行上限（120s）即可。
const concKeyTTL = time.Hour

// qpsScript 令牌桶（原子执行）。
// HMGET 取出剩余令牌与上次补充时间，按流逝时间补充令牌（不超过容量），
// 够扣则扣减返回 1，不够返回 0。整个读-算-写在一段 Lua 内完成，杜绝并发竞态。
var qpsScript = redis.NewScript(`
local key = KEYS[1]
local rate = tonumber(ARGV[1])
local capacity = tonumber(ARGV[2])
local now = tonumber(ARGV[3])
local requested = 1

local data = redis.call('HMGET', key, 'tokens', 'last')
local tokens = tonumber(data[1]) or capacity
local last = tonumber(data[2]) or now

local elapsed = math.max(0, now - last)
tokens = math.min(capacity, tokens + elapsed * rate)

if tokens < requested then
    redis.call('HMSET', key, 'tokens', tokens, 'last', now)
    redis.call('EXPIRE', key, 10)
    return 0
end

tokens = tokens - requested
redis.call('HMSET', key, 'tokens', tokens, 'last', now)
redis.call('EXPIRE', key, 10)
return 1
`)

// concScript 并发闸门（原子执行）。
// INCR 占槽，超限立即 DECR 回滚并返回 0；成功则刷新兜底 TTL 返回 1。
// 用 Lua 保证「判断+回滚」原子，避免两个请求同时 INCR 到超限却都放行。
var concScript = redis.NewScript(`
local count = redis.call('INCR', KEYS[1])
if count > tonumber(ARGV[1]) then
    redis.call('DECR', KEYS[1])
    return 0
end
redis.call('EXPIRE', KEYS[1], tonumber(ARGV[2]))
return 1
`)

// quotaScript 日配额闸门（原子执行）。
// INCR 计数，首次（count==1）设置到当日 24:00 的过期；超限则 DECR 回滚返回 0。
// 回滚保证「被拒绝的请求不消耗配额」，计数始终等于当日实际放行数。
var quotaScript = redis.NewScript(`
local count = redis.call('INCR', KEYS[1])
if count == 1 then
    redis.call('EXPIRE', KEYS[1], tonumber(ARGV[1]))
end
if count > tonumber(ARGV[2]) then
    redis.call('DECR', KEYS[1])
    return 0
end
return 1
`)

// Allow 依次过三道闸门。全部通过返回 nil，此时已占用一个并发槽，
// 调用方必须在请求结束后 defer ReleaseConcurrent 归还。
//
// 任一闸门因 Redis 故障无法判定时，选择「失败放行」（fail-open）：
// 限流是旁路保护，Redis 抖动不应直接打断业务；与项目既有 middleware.RateLimit 的策略一致。
func (l *Limiter) Allow(ctx context.Context, uid int64) error {
	// 闸门一：QPS 令牌桶
	qpsKey := fmt.Sprintf("rl:qps:%d", uid)
	nowSec := float64(time.Now().UnixMilli()) / 1000.0
	res, err := qpsScript.Run(ctx, l.r, []string{qpsKey}, l.limits.QPS, l.limits.QPS, nowSec).Int()
	if err != nil {
		log.Printf("agent limiter qps error (fail-open): %v", err)
	} else if res == 0 {
		return ErrRateLimit
	}

	// 闸门二：并发计数（成功后占槽，需配对归还）
	concKey := fmt.Sprintf("rl:conc:%d", uid)
	res, err = concScript.Run(ctx, l.r, []string{concKey}, l.limits.Concurrent, int(concKeyTTL.Seconds())).Int()
	if err != nil {
		log.Printf("agent limiter concurrent error (fail-open): %v", err)
	} else if res == 0 {
		return ErrTooManyConcurrent
	}

	// 闸门三：日配额。失败时要回滚上面已占的并发槽，否则会泄漏一个并发额度。
	quotaKey := fmt.Sprintf("rl:quota:%d:%s", uid, time.Now().Format("2006-01-02"))
	res, err = quotaScript.Run(ctx, l.r, []string{quotaKey}, secondsToMidnight(), l.limits.DailyQuota).Int()
	if err != nil {
		log.Printf("agent limiter quota error (fail-open): %v", err)
	} else if res == 0 {
		l.r.Decr(ctx, concKey) // 回滚并发槽
		return ErrQuotaExceeded
	}

	return nil
}

// ReleaseConcurrent 归还一个并发槽（DECR）。
// 内部用 WithoutCancel 脱离请求上下文：客户端断开后 ctx 往往已取消，
// 但归还动作必须成功执行，否则并发计数只增不减，用户会被自己的僵尸计数锁死。
func (l *Limiter) ReleaseConcurrent(ctx context.Context, uid int64) {
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	if err := l.r.Decr(rctx, fmt.Sprintf("rl:conc:%d", uid)).Err(); err != nil {
		log.Printf("agent limiter release concurrent error: %v", err)
	}
}

// secondsToMidnight 距离当日 24:00 的秒数，用作日配额键的过期时间，
// 让计数在跨天后自动清零，无需额外的定时清理任务。
func secondsToMidnight() int {
	now := time.Now()
	midnight := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, now.Location())
	sec := int(midnight.Sub(now).Seconds())
	if sec <= 0 {
		sec = 1 // 边界保护：恰好午夜时至少给 1 秒，避免 EXPIRE 0 立即删除
	}
	return sec
}

// limitCode 把限流错误映射为回传前端的 error code。
func limitCode(err error) string {
	switch {
	case errors.Is(err, ErrTooManyConcurrent):
		return "too_many_concurrent"
	case errors.Is(err, ErrQuotaExceeded):
		return "quota_exceeded"
	default:
		return "rate_limit"
	}
}
