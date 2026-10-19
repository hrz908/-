package user

import (
	"github.com/gin-gonic/gin"
)

// Register 注册本模块的路由。
// 三个接口都要登录，由 router 传入已挂 auth 的组
func Register(g *gin.RouterGroup) {
	g.GET("/user/profile", GetProfile)
	g.PUT("/user/profile", UpdateProfile)
	g.POST("/user/avatar", UploadAvatar)
}
