package authapi

import (
	"time"

	"github.com/gin-gonic/gin"

	"stock-agent-server/internal/middleware"
)

// Register 注册本模块路由。登录是公开接口（此时还没 token），单独挂更严的限流：
// 全局 100/min 对「微信 code 爆破」太松，默认 10/min 够正常使用（档位见 config.ratelimit.login_per_min）
func Register(g *gin.RouterGroup, loginLimit int64) {
	g.POST("/auth/login", middleware.LoginRateLimit(loginLimit, time.Minute), Login)
}
