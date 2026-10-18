package watchlist

import (
	"log"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"gorm.io/gorm/clause"

	"stock-agent-server/internal/deps"
	"stock-agent-server/internal/hook"
	"stock-agent-server/internal/quote"
	"stock-agent-server/internal/resp"
	"stock-agent-server/internal/stock"
	"stock-agent-server/internal/util"
)

// noQuote 无行情占位，与小程序 pending 态的展示保持一致
const noQuote = "--"

// 计算常量：具名之后盈亏公式读起来更直白，也免得重复 NewFromInt
var (
	zero    = decimal.Zero
	hundred = decimal.NewFromInt(100)
)

func logf(format string, args ...any) {
	log.Printf(format, args...)
}

/* ===================== 请求 / 响应结构 ===================== */

// AddWatchReq 添加自选入参。cost_price / quantity 用指针区分「没传」和「传了 0」
type AddWatchReq struct {
	StockCode string  `json:"stock_code"`
	CostPrice *string `json:"cost_price"`
	Quantity  *int    `json:"quantity"`
}

type AddWatchResp struct {
	ID        int64  `json:"id"`
	StockCode string `json:"stock_code"`
	StockName string `json:"stock_name"`
}

// WatchItem 与小程序自选股列表的一条记录一一对应：
// code / name / price_text / pct_text / up 可直接用于渲染，
// pending 表示刚加入还没有行情（前端展示 --）
type WatchItem struct {
	ID           int64  `json:"id"`
	StockCode    string `json:"stock_code"`     // SH600519
	Code         string `json:"code"`           // "SH 600519"，与前端展示格式一致
	Name         string `json:"name"`           // 股票名称
	Price        string `json:"price"`          // 原始价格，保留 2 位
	PriceText    string `json:"price_text"`     // 带千分位：1,705.30
	ChangePct    string `json:"change_percent"` // 涨跌幅：1.23
	PctText      string `json:"pct_text"`       // 带符号：+1.23%
	Up           bool   `json:"up"`
	CostPrice    string `json:"cost_price"`
	Quantity     int    `json:"quantity"`
	ProfitAmount string `json:"profit_amount"` // 浮动盈亏金额
	ProfitRate   string `json:"profit_rate"`   // 浮动盈亏比例（%）
	Pending      bool   `json:"pending"`       // 无行情
	Stale        bool   `json:"stale"`         // 用的是旧缓存
	UpdatedAt    string `json:"updated_at"`
}

// WatchListResp 列表：含单只盈亏与总盈亏统计
type WatchListResp struct {
	List            []WatchItem `json:"list"`
	TotalProfit     string      `json:"total_profit"`
	TotalProfitRate string      `json:"total_profit_rate"`
	UpCount         int         `json:"up_count"`
	DownCount       int         `json:"down_count"`
}

/* ===================== 查询列表 ===================== */

// GetWatchlist GET /api/v1/watchlist
// 执行顺序：PG 取基础信息 → Redis 批量取实时价 → decimal 算盈亏 → 按涨跌幅排序。
// 降级：Redis 不可用时不报错，按「无行情」处理（pending: true）
func GetWatchlist(c *gin.Context) {
	ctx := c.Request.Context()
	userID := c.GetInt64("user_id")

	var rows []UserWatchlist
	if err := deps.DB.WithContext(ctx).
		Where("user_id = ? AND enabled = ?", userID, true).
		Order("created_at DESC").
		Find(&rows).Error; err != nil {
		logf("query watchlist failed: user=%d err=%v", userID, err)
		resp.Fail(c, resp.CodeInternal, "查询自选股失败")
		return
	}

	codes := make([]string, 0, len(rows))
	for _, r := range rows {
		codes = append(codes, r.StockCode)
	}
	quotes, err := quote.BatchGetQuotes(ctx, codes)
	if err != nil {
		logf("batch get quotes failed, degrade: %v", err)
		quotes = map[string]*quote.StockSnapshot{}
	}

	filter := c.DefaultQuery("filter", "all")
	items, totalProfit, totalCostValue := buildItems(rows, quotes, filter)
	sortItems(items, filter, c.DefaultQuery("order", ""))

	resp.OK(c, WatchListResp{
		List:            items,
		TotalProfit:     util.Money(totalProfit),
		TotalProfitRate: sumRate(totalProfit, totalCostValue),
		UpCount:         countUp(items),
		DownCount:       countDown(items),
	})
}

/* ===================== 添加自选 ===================== */

// AddWatchlist POST /api/v1/watchlist
// 执行顺序：代码归一化 → 校验 → 拉行情（失败直接 502，不写库）→ Upsert → 落缓存。
// 为什么行情优先：拿不到名称就写库会产生「有记录无名称」的脏数据
func AddWatchlist(c *gin.Context) {
	ctx := c.Request.Context()
	userID := c.GetInt64("user_id")

	var req AddWatchReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.CodeBadRequest, "请求参数不合法")
		return
	}

	code, ok := stock.NormalizeCode(req.StockCode)
	if !ok {
		resp.Fail(c, resp.CodeBadRequest, "股票代码格式不正确，应为 SH/SZ/BJ + 6 位数字")
		return
	}

	cost, errMsg := parseCost(req.CostPrice)
	if errMsg != "" {
		resp.Fail(c, resp.CodeBadRequest, errMsg)
		return
	}
	qty, errMsg := parseQuantity(req.Quantity)
	if errMsg != "" {
		resp.Fail(c, resp.CodeBadRequest, errMsg)
		return
	}

	snap, err := quote.FetchOne(ctx, code)
	if err != nil || snap == nil {
		logf("fetch stock quote failed: code=%s err=%v", code, err)
		resp.Fail(c, resp.CodeBadGateway, "行情获取失败，请稍后重试")
		return
	}
	name := snap.Name
	if strings.TrimSpace(name) == "" {
		name = code
	}

	row := UserWatchlist{
		UserID:    userID,
		StockCode: code,
		StockName: name,
		CostPrice: cost,
		Quantity:  qty,
		Enabled:   true,
	}
	// Upsert：唯一索引冲突时更新成本、数量与时间，不覆盖股票名称。
	// 带上 enabled 是为了「移出后重新添加」能恢复这条记录，
	// 否则用户会看到添加成功但列表里没有
	if err := deps.DB.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "user_id"}, {Name: "stock_code"}},
		DoUpdates: clause.AssignmentColumns([]string{"cost_price", "quantity", "enabled", "updated_at"}),
	}).Create(&row).Error; err != nil {
		logf("upsert watchlist failed: user=%d code=%s err=%v", userID, code, err)
		resp.Fail(c, resp.CodeInternal, "添加自选股失败")
		return
	}
	// 冲突更新时 GORM 借 Postgres 的 RETURNING 回填主键；万一没回填就查一次
	if row.ID == 0 {
		var exist UserWatchlist
		if err := deps.DB.WithContext(ctx).
			Where("user_id = ? AND stock_code = ?", userID, code).
			First(&exist).Error; err == nil {
			row.ID = exist.ID
		}
	}

	// 缓存失败不影响本次添加成功，只记日志
	if err := quote.SaveQuoteToRedis(ctx, snap); err != nil {
		logf("save quote to redis failed: code=%s err=%v", code, err)
	}

	resp.OK(c, AddWatchResp{ID: row.ID, StockCode: row.StockCode, StockName: row.StockName})
}

/* ===================== 手动刷新 ===================== */

// RefreshWatchlist POST /api/v1/watchlist/refresh
// 执行顺序：查列表 → errgroup 并发拉行情（上限 5）→ pipeline 批量写缓存
//
//	→ 失败的降级读旧缓存 → 广播「行情已更新」钩子
//
// 降级策略：拉取失败先退回 Redis 旧值并标记 stale=true；
//
//	旧值也没有才置 pending=true、价格 0，前端据此显示 --
//
// 本接口只更新 Redis，不写 user_watchlist 表（列表本身没变化）。
// 刷新完成后通过 hook 广播，由关心该事件模块（如 alert）自行处理，避免模块间互相依赖
func RefreshWatchlist(c *gin.Context) {
	ctx := c.Request.Context()
	userID := c.GetInt64("user_id")

	var rows []UserWatchlist
	if err := deps.DB.WithContext(ctx).
		Where("user_id = ? AND enabled = ?", userID, true).
		Find(&rows).Error; err != nil {
		logf("query watchlist failed: user=%d err=%v", userID, err)
		resp.Fail(c, resp.CodeInternal, "刷新行情失败")
		return
	}

	// 没有自选股也正常返回空数组，走统一响应体
	codes := make([]string, 0, len(rows))
	for _, r := range rows {
		codes = append(codes, r.StockCode)
	}

	fresh, failed := quote.FetchQuotes(ctx, codes)

	if err := quote.BatchSaveQuotes(ctx, snapshotValues(fresh)); err != nil {
		logf("batch save quotes failed: %v", err)
	}

	// 拉取失败的：降级读旧缓存；读不到就保持无行情
	staleMap := map[string]*quote.StockSnapshot{}
	if len(failed) > 0 {
		old, err := quote.BatchGetQuotes(ctx, failed)
		if err != nil {
			logf("fallback read quotes failed: %v", err)
		} else {
			staleMap = old
		}
	}

	filter := c.DefaultQuery("filter", "all")
	items := make([]WatchItem, 0, len(rows))
	totalProfit := zero
	totalCostValue := zero

	for _, r := range rows {
		item := buildEmptyItem(r)
		switch {
		case fresh[r.StockCode] != nil:
			item = buildItem(r, fresh[r.StockCode], false)
		case staleMap[r.StockCode] != nil:
			item = buildItem(r, staleMap[r.StockCode], true)
		}

		if filter == "up" || filter == "down" {
			if item.Pending {
				continue // 涨跌榜不列无行情的自选
			}
		}
		items = append(items, item)

		if item.Pending {
			// 无行情不计入盈亏统计
			continue
		}
		if amt, err := decimal.NewFromString(item.ProfitAmount); err == nil {
			totalProfit = totalProfit.Add(amt)
		}
		totalCostValue = totalCostValue.Add(r.CostPrice.Mul(decimal.NewFromInt(int64(r.Quantity))))
	}
	sortItems(items, filter, c.DefaultQuery("order", ""))

	// 行情更新完成，广播给所有关心方（提醒检查等）
	hook.EmitQuoteUpdated(userID, ctx)

	// 返回结构与列表接口一致，前端可直接整段替换列表与统计
	resp.OK(c, WatchListResp{
		List:            items,
		TotalProfit:     util.Money(totalProfit),
		TotalProfitRate: sumRate(totalProfit, totalCostValue),
		UpCount:         countUp(items),
		DownCount:       countDown(items),
	})
}

// sumRate 总盈亏比例 = 总盈亏 ÷ 总成本；成本为 0 时记 0，避免除零
func sumRate(profit, costValue decimal.Decimal) string {
	if !costValue.IsPositive() {
		return "0.00"
	}
	return profit.Div(costValue).Mul(hundred).StringFixed(2)
}

// DeleteWatchlist DELETE /api/v1/watchlist/:stock_code
// 软删除：只把 enabled 置为 false，不物理删行。
// 表里有 enabled 字段就是为了支持「移出但保留历史」，
// 硬删会让成本价等建仓信息一并丢失，重新添加还得再填一遍。
// 更新必须带 user_id 过滤，防止改到别人的记录
func DeleteWatchlist(c *gin.Context) {
	ctx := c.Request.Context()
	userID := c.GetInt64("user_id")

	code, ok := stock.NormalizeCode(c.Param("stock_code"))
	if !ok {
		resp.Fail(c, resp.CodeBadRequest, "股票代码格式不正确，应为 SH/SZ/BJ + 6 位数字")
		return
	}

	res := deps.DB.WithContext(ctx).Model(&UserWatchlist{}).
		Where("user_id = ? AND stock_code = ?", userID, code).
		Update("enabled", false)
	if res.Error != nil {
		logf("delete watchlist failed: user=%d code=%s err=%v", userID, code, res.Error)
		resp.Fail(c, resp.CodeInternal, "移出自选失败")
		return
	}
	if res.RowsAffected == 0 {
		// 查不到就统一 404，不区分「不存在」与「不是本人的」
		resp.Fail(c, resp.CodeNotFound, "自选不存在或无权限")
		return
	}

	resp.OK(c, gin.H{"stock_code": code})
}

/* ===================== 组装返回项 ===================== */

// buildItem 把「数据库行 + 行情快照」组装成列表项，盈亏在后端算好
func buildItem(row UserWatchlist, snap *quote.StockSnapshot, stale bool) WatchItem {
	item := WatchItem{
		ID:        row.ID,
		StockCode: row.StockCode,
		Code:      stock.DisplayCode(row.StockCode),
		Name:      row.StockName,
		CostPrice: row.CostPrice.StringFixed(4),
		Quantity:  row.Quantity,
		Stale:     stale,
		UpdatedAt: row.UpdatedAt.Format(time.RFC3339),
	}
	if snap == nil || snap.Price.IsZero() {
		item.Pending = true
		item.Price = "0"
		item.PriceText = noQuote
		item.ChangePct = "0"
		item.PctText = noQuote
		item.ProfitAmount = "0.00"
		item.ProfitRate = "0.00"
		return item
	}

	item.Price = util.Money(snap.Price)
	item.PriceText = util.Thousands(snap.Price)
	item.ChangePct = snap.ChangePercent.StringFixed(2)
	item.PctText = util.WithSign(snap.ChangePercent) + "%"
	item.Up = snap.ChangePercent.IsPositive()
	item.ProfitAmount, item.ProfitRate = calcProfit(snap.Price, row.CostPrice, row.Quantity)
	return item
}

// buildItems 列表接口的组装：涨跌幅榜过滤掉无行情的（与前端 filterList 口径一致）
func buildItems(rows []UserWatchlist, quotes map[string]*quote.StockSnapshot, filter string) ([]WatchItem, decimal.Decimal, decimal.Decimal) {
	skipPending := filter == "up" || filter == "down"

	items := make([]WatchItem, 0, len(rows))
	totalProfit := zero
	totalCostValue := zero

	for _, r := range rows {
		it := buildItem(r, quotes[r.StockCode], false)
		if skipPending && it.Pending {
			continue
		}
		items = append(items, it)
		if it.Pending {
			// 无行情不计入盈亏统计
			continue
		}
		if amt, err := decimal.NewFromString(it.ProfitAmount); err == nil {
			totalProfit = totalProfit.Add(amt)
		}
		totalCostValue = totalCostValue.Add(r.CostPrice.Mul(decimal.NewFromInt(int64(r.Quantity))))
	}
	return items, totalProfit, totalCostValue
}

// buildEmptyItem 刷新失败且无缓存可退：价格归零并标记 pending / stale
func buildEmptyItem(row UserWatchlist) WatchItem {
	return WatchItem{
		ID:           row.ID,
		StockCode:    row.StockCode,
		Code:         stock.DisplayCode(row.StockCode),
		Name:         row.StockName,
		Price:        "0",
		PriceText:    noQuote,
		ChangePct:    "0",
		PctText:      noQuote,
		CostPrice:    row.CostPrice.StringFixed(4),
		Quantity:     row.Quantity,
		ProfitAmount: "0.00",
		ProfitRate:   "0.00",
		Pending:      true,
		Stale:        true,
		UpdatedAt:    row.UpdatedAt.Format(time.RFC3339),
	}
}

// calcProfit 浮动盈亏：(最新价 - 成本价) × 数量。
// 全程 decimal，不用 float64，避免 3682.4000000000005 这类误差。
// 成本为 0（用户没填）时盈亏比例记 0，避免除零
func calcProfit(price, cost decimal.Decimal, quantity int) (amount string, rate string) {
	qty := decimal.NewFromInt(int64(quantity))
	amountDec := price.Sub(cost).Mul(qty)
	costValue := cost.Mul(qty)

	rateDec := zero
	if costValue.IsPositive() {
		rateDec = amountDec.Div(costValue).Mul(hundred)
	}
	return util.Money(amountDec), rateDec.StringFixed(2)
}

// sortItems 按涨跌幅排序。
// filter=up（涨幅榜）降序、filter=down（跌幅榜）升序；
// filter=all 时只有显式传 order 才排序，否则保持 SQL 的 created_at DESC 顺序
func sortItems(items []WatchItem, filter, order string) {
	desc := true
	switch filter {
	case "up":
		desc = true
	case "down":
		desc = false
	case "all":
		if order != "asc" && order != "desc" {
			return
		}
		desc = order == "desc"
	default:
		return
	}

	sort.Slice(items, func(i, j int) bool {
		a, _ := decimal.NewFromString(items[i].ChangePct)
		b, _ := decimal.NewFromString(items[j].ChangePct)
		if desc {
			return a.GreaterThan(b)
		}
		return a.LessThan(b)
	})
}

func countUp(items []WatchItem) int {
	n := 0
	for _, it := range items {
		if !it.Pending && it.Up {
			n++
		}
	}
	return n
}

func countDown(items []WatchItem) int {
	n := 0
	for _, it := range items {
		if !it.Pending && !it.Up {
			n++
		}
	}
	return n
}

func snapshotValues(m map[string]*quote.StockSnapshot) []*quote.StockSnapshot {
	out := make([]*quote.StockSnapshot, 0, len(m))
	for _, s := range m {
		out = append(out, s)
	}
	return out
}

// parseCost 解析成本价：没传返回零值；传了必须是数字且 >= 0
func parseCost(raw *string) (decimal.Decimal, string) {
	if raw == nil {
		return zero, ""
	}
	v, err := decimal.NewFromString(strings.TrimSpace(*raw))
	if err != nil {
		return zero, "成本价必须是数字"
	}
	if v.IsNegative() {
		return zero, "成本价不能为负"
	}
	return v, ""
}

// parseQuantity 解析持股数量：没传为 0；传了不能为负
func parseQuantity(raw *int) (int, string) {
	if raw == nil {
		return 0, ""
	}
	if *raw < 0 {
		return 0, "持股数量不能为负"
	}
	return *raw, ""
}
