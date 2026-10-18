package hook

import (
	"context"
	"log"
)

// QuoteUpdated 的回调签名：某用户的一批行情刷新完成后触发
type QuoteUpdatedFunc func(userID int64, ctx context.Context)

var quoteUpdated []QuoteUpdatedFunc

// RegisterQuoteUpdated 注册「行情刷新完成」的钩子。
// 用钩子而不是直接调用，是为了让业务模块之间零依赖：
// watchlist 只管刷新行情，谁关心刷新后要做什么由谁自己注册
func RegisterQuoteUpdated(fn QuoteUpdatedFunc) {
	quoteUpdated = append(quoteUpdated, fn)
}

// EmitQuoteUpdated 触发所有已注册的钩子。
// 单个钩子 panic 不影响其它钩子，避免一个模块拖垮整条刷新链路
func EmitQuoteUpdated(userID int64, ctx context.Context) {
	for _, fn := range quoteUpdated {
		func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("quote updated hook panic: %v", r)
				}
			}()
			fn(userID, ctx)
		}()
	}
}
