package hook

import (
	"context"
	"log"
	"sync"
)

// QuoteUpdatedFunc 的回调签名：某用户的一批行情刷新完成后触发
type QuoteUpdatedFunc func(userID int64, ctx context.Context)

var (
	mu           sync.Mutex
	quoteUpdated []QuoteUpdatedFunc
)

// RegisterQuoteUpdated 注册「行情刷新完成」的钩子。
// 用钩子而不是直接调用，是为了让业务模块之间零依赖：
// watchlist 只管刷新行情，谁关心刷新后要做什么由谁自己注册
func RegisterQuoteUpdated(fn QuoteUpdatedFunc) {
	mu.Lock()
	defer mu.Unlock()
	quoteUpdated = append(quoteUpdated, fn)
}

// EmitQuoteUpdated 同步触发所有已注册的钩子（在调用方 goroutine 内跑完）
func EmitQuoteUpdated(userID int64, ctx context.Context) {
	for _, fn := range funcs() {
		safeRun(fn, userID, ctx)
	}
}

// EmitQuoteUpdatedAsync 异步触发。
// 提醒检查里有 DB 查询、Redis SetNX、WebSocket 推送，全部同步跑完才返回
// 会明显拖慢 refresh 的响应；异步后请求先返回，检查在后台完成。
// 用 context.WithoutCancel 脱离请求 ctx——请求结束原 ctx 即被取消，
// 直接传会让检查中途死掉
func EmitQuoteUpdatedAsync(userID int64, ctx context.Context) {
	fns := funcs()
	if len(fns) == 0 {
		return
	}
	go func() {
		bg := context.WithoutCancel(ctx)
		for _, fn := range fns {
			safeRun(fn, userID, bg)
		}
	}()
}

// funcs 拷贝一份再遍历：注册与触发解耦，避免边遍历边注册
func funcs() []QuoteUpdatedFunc {
	mu.Lock()
	defer mu.Unlock()
	return append([]QuoteUpdatedFunc(nil), quoteUpdated...)
}

// safeRun 单个钩子 panic 不影响其它钩子，避免一个模块拖垮整条刷新链路
func safeRun(fn QuoteUpdatedFunc, userID int64, ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("quote updated hook panic: %v", r)
		}
	}()
	fn(userID, ctx)
}
