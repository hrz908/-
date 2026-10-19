package router

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"stock-agent-server/internal/middleware"
	"stock-agent-server/internal/module/alert"
	authapi "stock-agent-server/internal/module/auth"
	"stock-agent-server/internal/module/stock"
	"stock-agent-server/internal/module/watchlist"
	"stock-agent-server/internal/ws"
)

// New 组装路由：
//   - 全局中间件（按注册顺序执行）：Recovery → RequestID → CORS → RateLimit → AccessLog
//   - 路由组级：/api/v1 下的 authorized 分组统一挂 auth 中间件
//   - 单路由级：无（鉴权统一放在组上，避免漏挂）
//
// 各业务模块自带 Register(g *gin.RouterGroup)，新增页面后端时：
// 在这里 import 进来、调一次 Register 即可，不用改本文件以外的东西
func New() *gin.Engine {
	r := gin.New()

	// Recovery 必须最先注册，才能兜住后续中间件和 Handler 的 panic
	r.Use(middleware.Recovery())
	r.Use(middleware.RequestID())
	r.Use(middleware.CORS())
	// 限流放在鉴权之前：未登录的刷接口也要被挡住
	// （Redis 客户端由 deps 提供，与业务包保持一致）
	r.Use(middleware.RateLimit(100, time.Minute))
	r.Use(middleware.AccessLog())

	// 健康检查：给容器 / 编排探针用，不鉴权、也不该被限流误伤
	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": gin.H{"status": "ok"}})
	})

	api := r.Group("/api/v1")

	// 公开路由：登录
	authapi.Register(api)

	// 受保护路由：统一挂 auth，user_id 由中间件注入
	authorized := api.Group("")
	authorized.Use(middleware.Auth())
	{
		watchlist.Register(authorized)
		alert.Register(authorized)
		stock.Register(authorized)

		// WebSocket 长连接：握手无法带自定义头，token 走 query
		authorized.GET("/ws", ws.Serve)
	}

	return r
}
