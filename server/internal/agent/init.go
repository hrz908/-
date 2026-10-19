package agent

import (
	"github.com/redis/go-redis/v9"
)

// 模块级全局单例：与项目其它模块（auth/deps）保持一致的「main 注入一次、业务包直接取用」风格。
// 网关本身无状态，限流计数落在 Redis，代理只是 HTTP 客户端，因此单例足够。
var (
	limiter *Limiter
	proxy   *AgentProxy
)

// Init 初始化 agent 网关模块，必须在 router 组装与请求到来之前调用一次。
// redisClient 用于三道限流闸门；agentBaseURL 指向 Python Agent 服务（如 http://127.0.0.1:8000）。
func Init(redisClient *redis.Client, agentBaseURL string) {
	limiter = NewLimiter(redisClient, DefaultLimits)
	proxy = NewAgentProxy(agentBaseURL)
}
