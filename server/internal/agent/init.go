package agent

import (
	"time"

	"github.com/redis/go-redis/v9"
)

// Options Agent 网关运行参数（由 config 注入，见 config.agent.*）。
type Options struct {
	BaseURL          string        // Python Agent 服务基地址
	QPS              int           // 单用户每秒令牌数（同时作为桶容量）
	Concurrent       int           // 单用户并发运行数
	DailyQuota       int           // 单用户每日请求配额
	ProxyConcurrency int           // 打到 Python Agent 的并发上限
	WSMaxConns       int           // 进程内 Agent WS 连接上限
	RunTimeout       time.Duration // 单次运行整体超时
	TTFTTimeout      time.Duration // 首字节等待上限
	MaxLineBytes     int           // ndjson 单行上限
}

// 模块级全局单例：与项目其它模块（auth/deps）保持一致的「main 注入一次、业务包直接取用」风格。
// 网关本身无状态，限流计数落在 Redis，代理只是 HTTP 客户端，因此单例足够。
var (
	limiter *Limiter
	proxy   *AgentProxy
)

// Init 初始化 agent 网关模块，必须在 router 组装与请求到来之前调用一次。
// redisClient 用于三道限流闸门；opts.BaseURL 指向 Python Agent 服务。
func Init(redisClient *redis.Client, opts Options) {
	if opts.QPS <= 0 {
		opts.QPS = DefaultLimits.QPS
	}
	if opts.Concurrent <= 0 {
		opts.Concurrent = DefaultLimits.Concurrent
	}
	if opts.DailyQuota <= 0 {
		opts.DailyQuota = DefaultLimits.DailyQuota
	}
	if opts.ProxyConcurrency <= 0 {
		opts.ProxyConcurrency = 64
	}
	if opts.WSMaxConns <= 0 {
		opts.WSMaxConns = 512
	}
	if opts.RunTimeout <= 0 {
		opts.RunTimeout = 120 * time.Second
	}
	if opts.TTFTTimeout <= 0 {
		opts.TTFTTimeout = 8 * time.Second
	}
	if opts.MaxLineBytes <= 0 {
		opts.MaxLineBytes = 4 << 20
	}

	limiter = NewLimiter(redisClient, Limits{QPS: opts.QPS, Concurrent: opts.Concurrent, DailyQuota: opts.DailyQuota})
	proxy = NewAgentProxy(opts)
	// 全局连接闸门在 Init 时按配置建容量（包级变量无法在 var 声明处读配置）
	globalSem = make(chan struct{}, opts.WSMaxConns)
}
