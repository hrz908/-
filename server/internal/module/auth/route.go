package authapi

import (
	"time"

	"github.com/gin-gonic/gin"

	"stock-agent-server/internal/middleware"
)

// Register 注册本模块的路由。
// 登录是公开接口（此时还没有 token），但对它单独挂一档更严的限流：
// 全局 100/min 对「微信 code 爆破」这类场景太松，10/min 足够正常使用
func Register(g *gin.RouterGroup) {
	g.POST("/auth/login", middleware.RateLimit(10, time.Minute), Login)
}
