package router

import (
	"path/filepath"
	"time"

	"github.com/gin-gonic/gin"

	"stock-agent-server/internal/agent"
	"stock-agent-server/internal/asr"
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
//   - 全局中间件（按注册顺序执行）：Recovery → RequestID → CORS → RateLimit → AccessLog
//   - 路由组级：/api/v1 下的 authorized 分组统一挂 auth 中间件
//   - 单路由级：/ws 用允许 ?token= 的鉴权变体（握手无法带自定义头）
//
// 各业务模块自带 Register(g *gin.RouterGroup)，新增页面后端时：
// 在这里 import 进来、调一次 Register 即可，不用改本文件以外的东西
func New(allowOrigins string) *gin.Engine {
	r := gin.New()

	// 只信任本机反代：gin 默认信任所有代理的 X-Forwarded-For，
	// 不限的话限流按 IP 分组可被伪造头绕过
	if err := r.SetTrustedProxies([]string{"127.0.0.1"}); err != nil {
		panic(err)
	}

	// Recovery 必须最先注册，才能兜住后续中间件和 Handler 的 panic
	r.Use(middleware.Recovery())
	r.Use(middleware.RequestID())
	r.Use(middleware.CORS(allowOrigins))
	// 限流放在鉴权之前：未登录的刷接口也要被挡住
	// （Redis 客户端由 deps 提供，与业务包保持一致）
	r.Use(middleware.RateLimit(100, time.Minute))
	r.Use(middleware.AccessLog())

	// 健康检查：给容器 / 编排探针用，不鉴权、也不该被限流误伤
	r.GET("/healthz", func(c *gin.Context) {
		resp.OK(c, gin.H{"status": "ok"})
	})

	// 上传的头像。只暴露 avatars 子目录：
	// 整个 upload.Dir 挂出去会让目录路径返回文件列表（信息泄露），
	// 而且目录与写入用的是同一个 upload.Dir，不会出现「写到 A、从 B 读」的错位
	r.Static("/uploads/avatars", filepath.Join(upload.Dir, "avatars"))

	api := r.Group("/api/v1")

	// 公开路由：登录（内部对登录单独挂了更严的限流）
	authapi.Register(api)

	// WebSocket 长连接：握手无法带自定义头，token 只能走 query（?token=）。
	// 必须注册在 authorized 组「之外」——该组的 Auth() 只认 Header Bearer，
	// WS 握手没有 Header，若挂在组内会先被 Auth() 以 401 拦下，根本到不了
	// 下面这个允许 query token 的中间件。故这里直接挂在 api 上，只用 AuthAllowQueryToken。
	api.GET("/ws", middleware.AuthAllowQueryToken(), ws.Serve)
	api.GET("/ws/agent", middleware.AuthAllowQueryToken(), agent.ServeWS)

	// 受保护路由：统一挂 auth（Header Bearer），user_id 由中间件注入
	authorized := api.Group("")
	authorized.Use(middleware.Auth())
	{
		watchlist.Register(authorized)
		alert.Register(authorized)
		stock.Register(authorized)
		user.Register(authorized)

		// Agent AI 对话 SSE 兜底（H5 / 不支持 WS 的环境）：走 Header 鉴权，留在组内
		authorized.POST("/agent/stream", agent.ServeSSE)

		// 语音转文字：multipart 上传录音，网关转发上游 ASR。
		// 挂在鉴权组内——每次识别都消耗上游额度，不能匿名调用
		authorized.POST("/asr/transcribe", asr.Transcribe)
	}

	return r
}
