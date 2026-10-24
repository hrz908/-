package alert

import (
	"sync"

	"github.com/gin-gonic/gin"

	"stock-agent-server/internal/hook"
)

// subscribe 保证「行情已更新」事件只订阅一次。Register 理论只在启动时调用一次，
// 加 Once 是防误调用导致同一条提醒被检查两遍（配合 SetNX 才不会推重）
var subscribe sync.Once

func subscribeOnce() {
	subscribe.Do(func() {
		hook.RegisterQuoteUpdated(checkAlerts)
	})
}

// Register 注册本模块路由，并订阅行情刷新完成事件
func Register(g *gin.RouterGroup) {
	subscribeOnce()

	g.POST("/alerts", CreateAlert)
	g.GET("/alerts", ListAlerts)
	g.PUT("/alerts/:id", UpdateAlert)
	g.DELETE("/alerts/:id", DeleteAlert)
}
