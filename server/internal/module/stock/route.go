package stock

import "github.com/gin-gonic/gin"

// Register 把本模块的路由挂到 /api/v1 下。
// 调用方负责挂鉴权中间件（router.New 里 authorized 组统一挂了 Auth）
func Register(g *gin.RouterGroup) {
	g.GET("/stocks/search", SearchStock)
	g.GET("/stock/quote", GetQuote)
	g.GET("/stock/kline", GetKline)
}
