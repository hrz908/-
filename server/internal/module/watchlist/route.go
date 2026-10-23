package watchlist

import (
	"github.com/gin-gonic/gin"
)

// Register 注册本模块的路由。
// 传进来的 group 决定了前缀与中间件：这里由 router 传入已挂 auth 的组。
// maxItems 是单用户自选股数量上限（配置注入；refresh 会放大上游请求量，不能不设限）
func Register(g *gin.RouterGroup, maxItems int) {
	if maxItems > 0 {
		maxWatchlist = maxItems
	}
	g.GET("/watchlist", GetWatchlist)
	g.POST("/watchlist", AddWatchlist)
	g.POST("/watchlist/refresh", RefreshWatchlist)
	g.DELETE("/watchlist/:stock_code", DeleteWatchlist)
}
