package authapi

import (
	"github.com/gin-gonic/gin"
)

// Register 注册公开路由：登录不需要鉴权，由 router 传入未挂 auth 的组
func Register(g *gin.RouterGroup) {
	g.POST("/auth/login", Login)
}
