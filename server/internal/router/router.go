package router

import (
	"path/filepath"
	"time"

	"github.com/gin-gonic/gin"

	"stock-agent-server/internal/agent"
	"stock-agent-server/internal/asr"
	"stock-agent-server/internal/config"
	"stock-agent-server/internal/middleware"
	"stock-agent-server/internal/module/alert"
	authapi "stock-agent-server/internal/module/auth"
	"stock-agent-server/internal/module/stock"
	"stock-agent-server/internal/module/user"
	"stock-agent-server/internal/module/watchlist"
	"stock-agent-server/internal/resp"
	"stock-agent-server/internal/upload"
	"stock-agent-server/internal/ws"
)

// New 组装路由：
//   - 全局中间件按序执行：Recovery → RequestID → CORS → RateLimit → AccessLog；
//   - /api/v1 下的 authorized 组统一挂 auth 中间件；
//   - /ws 系列用允许 ?token= 的鉴权变体（握手无法带自定义请求头）。
//
// 各业务模块自带 Register(g *gin.RouterGroup)，新增接口只需在此 import 并调一次。
func New(cfg config.Config) *gin.Engine {
	r := gin.New()

	// 只信任本机反代：gin 默认信任所有代理的 X-Forwarded-For，否则按 IP 限流可被伪造头绕过
	if err := r.SetTrustedProxies([]string{"127.0.0.1"}); err != nil {
		panic(err)
	}

	// Recovery 必须最先注册，才能兜住后续中间件与 Handler 的 panic
	r.Use(middleware.Recovery())
	r.Use(middleware.RequestID())
	r.Use(middleware.CORS(cfg.AllowOrigins))
	// 限流放在鉴权之前：未登录的刷接口也要被挡住（Redis 客户端由 deps 提供）
	r.Use(middleware.IPRateLimit(cfg.RateLimit.IPPerMin, time.Minute))
	r.Use(middleware.AccessLog())

	// 健康检查给容器 / 编排探针用，不鉴权也不该被限流误伤
	r.GET("/healthz", func(c *gin.Context) {
		resp.OK(c, gin.H{"status": "ok"})
	})

	// 只暴露 avatars 子目录：挂整个 upload.Dir 会让目录路径返回文件列表（信息泄露），
	// 且读写共用同一个 upload.Dir，不会出现「写到 A、从 B 读」的错位
	r.Static("/uploads/avatars", filepath.Join(upload.Dir, "avatars"))

	api := r.Group("/api/v1")

	// 公开路由：登录（内部另挂了更严的限流）
	authapi.Register(api, cfg.RateLimit.LoginPerMin)

	// WS 长连接必须挂在 authorized 组之外：该组的 Auth() 只认 Header Bearer，
	// 握手没有 Header，挂在组内会先被 401 拦下，到不了允许 query token 的中间件
	api.GET("/ws", middleware.AuthAllowQueryToken(), ws.Serve)
	api.GET("/ws/agent", middleware.AuthAllowQueryToken(), agent.ServeWS)
	// 实时语音识别：小程序把录音分片以二进制帧直传，这里转发腾讯云并回传结果
	api.GET("/ws/asr", middleware.AuthAllowQueryToken(), asr.ServeStream)

	// 受保护路由：统一挂 auth（Header Bearer），user_id 由中间件注入
	authorized := api.Group("")
	authorized.Use(middleware.Auth())
	{
		watchlist.Register(authorized, cfg.WatchlistMaxItems)
		alert.Register(authorized)
		user.Register(authorized)

		// 行情与搜索会放大成多个第三方上游请求（腾讯行情 / smartbox），故按用户单独限流
		market := authorized.Group("")
		market.Use(middleware.UserRateLimit(cfg.RateLimit.MarketPerMin, time.Minute))
		stock.Register(market)

		// Agent 对话 SSE 兜底（H5 / 不支持 WS 的环境），走 Header 鉴权，留在组内
		authorized.POST("/agent/stream", agent.ServeSSE)

		// 会话历史：列表 / 注册 / 恢复消息 / 删除；注册表与 checkpoint 由 Agent 侧持有，网关只转发
		authorized.GET("/agent/sessions", agent.ServeListSessions)
		authorized.POST("/agent/sessions", agent.ServeCreateSession)
		authorized.GET("/agent/sessions/:sid/messages", agent.ServeSessionMessages)
		authorized.DELETE("/agent/sessions/:sid", agent.ServeDeleteSession)

		// 语音转文字：每次识别都消耗上游付费额度，不能匿名调用，再叠一层用户级限流
		authorized.POST("/asr/transcribe",
			middleware.UserRateLimit(cfg.RateLimit.ASRPerMin, time.Minute), asr.Transcribe)
	}

	return r
}
