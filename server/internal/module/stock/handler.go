package stock

import (
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"stock-agent-server/internal/deps"
	"stock-agent-server/internal/quote"
	"stock-agent-server/internal/resp"
	"stock-agent-server/internal/stock"
)

// searchCacheTTL 搜索结果缓存 1 小时。
// 股票代码不会新增 / 消失得很快，同一 query 的相关性排序在长时间窗口内稳定，
// 1 小时足以让单条 query 的请求量大幅降低
const searchCacheTTL = time.Hour

// quoteCacheTTL 行情详情缓存 30 秒，与 quote 包的快照缓存约定一致
const quoteCacheTTL = 30 * time.Second

// klineCacheTTL K 线缓存 10 分钟：日 K 盘中变化有限，
// 但比行情快照低频得多，缓存收益高且不会让前端看到明显陈旧的数据
const klineCacheTTL = 10 * time.Minute

// maxLimit 单次最多返回 10 条，避免前端渲染过多 + 防止恶意参数拖慢后端
const maxLimit = 10

// defaultKlineCount K 线默认根数；上限由 quote.MaxKlineCount 兜底
const defaultKlineCount = 10

// SearchStock GET /api/v1/stocks/search?q=<query>&limit=5
// 股票模糊搜索：用于添加自选时的下拉联想。
//
// 走 Redis 缓存：搜索是高频接口但实时性低（同 query 一小时内结果稳定），
// 缓存降本明显。空 query 直接返回空数组，前端在防抖窗口过滤即可
func SearchStock(c *gin.Context) {
	q := strings.TrimSpace(c.Query("q"))
	if q == "" {
		resp.OK(c, []quote.SearchResult{})
		return
	}

	limit, err := strconv.Atoi(c.DefaultQuery("limit", "5"))
	if err != nil || limit <= 0 {
		limit = 5
	}
	if limit > maxLimit {
		limit = maxLimit
	}

	cacheKey := "stock:search:" + q + ":" + strconv.Itoa(limit)
	ctx := c.Request.Context()

	// 缓存命中直接吐 bytes，省一次序列化
	if deps.RDB != nil {
		if cached, err := deps.RDB.Get(ctx, cacheKey).Bytes(); err == nil && len(cached) > 0 {
			c.Header("X-Cache", "HIT")
			c.Data(200, "application/json; charset=utf-8", cached)
			return
		}
	}

	results := quote.Search(ctx, q, limit)
	// 永远返回非 nil 切片，前端 map/foreach 不用判空
	if results == nil {
		results = []quote.SearchResult{}
	}
	// 序列化与缓存写同一份字节：保证缓存内容与真实响应逐字节一致
	body, err := resp.JSONBytes(resp.CodeOK, "ok", results)
	if err != nil {
		log.Printf("marshal search result failed: %v", err)
		resp.Fail(c, resp.CodeInternal, "序列化失败")
		return
	}

	// 空结果不写缓存：空往往来自第三方临时抽风或超时，
	// 缓存住会让搜索长达 1 小时什么都搜不到，比多打几次请求糟糕得多。
	// 缓存写失败也不阻断，搜索是体验加分项
	if deps.RDB != nil && len(results) > 0 {
		_ = deps.RDB.Set(ctx, cacheKey, body, searchCacheTTL).Err()
	}

	c.Header("X-Cache", "MISS")
	c.Data(200, "application/json; charset=utf-8", body)
}

// GetQuote GET /api/v1/stock/quote?code=600519
// 单只股票的完整行情快照：价格、涨跌、开高低、量额、状态与时间。
//
// 走 Redis 短缓存（30 秒）：详情页高频打开，同一只股票半分钟内的
// 重复请求没必要每次都打到第三方行情源
func GetQuote(c *gin.Context) {
	code, ok := stock.NormalizeCode(c.Query("code"))
	if !ok {
		resp.Fail(c, resp.CodeBadRequest, "股票代码格式不正确，应为 SH/SZ/BJ + 6 位数字")
		return
	}

	cacheKey := "stock:quote:" + code
	ctx := c.Request.Context()

	if deps.RDB != nil {
		if cached, err := deps.RDB.Get(ctx, cacheKey).Bytes(); err == nil && len(cached) > 0 {
			c.Header("X-Cache", "HIT")
			c.Data(200, "application/json; charset=utf-8", cached)
			return
		}
	}

	detail, err := quote.FetchDetail(ctx, code)
	if err != nil || detail == nil {
		log.Printf("fetch quote detail failed: code=%s err=%v", code, err)
		resp.Fail(c, resp.CodeBadGateway, "行情获取失败，请稍后重试")
		return
	}

	body, err := resp.JSONBytes(resp.CodeOK, "ok", detail)
	if err != nil {
		log.Printf("marshal quote detail failed: %v", err)
		resp.Fail(c, resp.CodeInternal, "序列化失败")
		return
	}
	if deps.RDB != nil {
		_ = deps.RDB.Set(ctx, cacheKey, body, quoteCacheTTL).Err()
	}

	c.Header("X-Cache", "MISS")
	c.Data(200, "application/json; charset=utf-8", body)
}

// GetKline GET /api/v1/stock/kline?code=600519&period=day&count=10
// 前复权 K 线：period 取 day/week/month（默认 day），count 默认 10、上限 365。
// 数据来自腾讯 fqkline 接口，同样走 Redis 缓存降低第三方压力
func GetKline(c *gin.Context) {
	code, ok := stock.NormalizeCode(c.Query("code"))
	if !ok {
		resp.Fail(c, resp.CodeBadRequest, "股票代码格式不正确，应为 SH/SZ/BJ + 6 位数字")
		return
	}

	period := strings.ToLower(strings.TrimSpace(c.DefaultQuery("period", "day")))
	if period != "day" && period != "week" && period != "month" {
		resp.Fail(c, resp.CodeBadRequest, "period 仅支持 day/week/month")
		return
	}

	count, err := strconv.Atoi(c.DefaultQuery("count", strconv.Itoa(defaultKlineCount)))
	if err != nil || count <= 0 {
		count = defaultKlineCount
	}
	if count > quote.MaxKlineCount {
		count = quote.MaxKlineCount
	}

	cacheKey := "stock:kline:" + code + ":" + period + ":" + strconv.Itoa(count)
	ctx := c.Request.Context()

	if deps.RDB != nil {
		if cached, err := deps.RDB.Get(ctx, cacheKey).Bytes(); err == nil && len(cached) > 0 {
			c.Header("X-Cache", "HIT")
			c.Data(200, "application/json; charset=utf-8", cached)
			return
		}
	}

	rows, err := quote.FetchKline(ctx, code, period, count)
	if err != nil {
		log.Printf("fetch kline failed: code=%s period=%s err=%v", code, period, err)
		resp.Fail(c, resp.CodeBadGateway, "K 线获取失败，请稍后重试")
		return
	}

	data := gin.H{
		"code":   code,
		"period": period,
		// 永远返回非 nil 切片，前端 map/foreach 不用判空
		"rows": func() []quote.KlineRow {
			if rows == nil {
				return []quote.KlineRow{}
			}
			return rows
		}(),
	}

	body, err := resp.JSONBytes(resp.CodeOK, "ok", data)
	if err != nil {
		log.Printf("marshal kline failed: %v", err)
		resp.Fail(c, resp.CodeInternal, "序列化失败")
		return
	}
	// 空结果不写缓存：同搜索，空往往来自第三方临时抽风，缓存住会放大故障窗口
	if deps.RDB != nil && len(rows) > 0 {
		_ = deps.RDB.Set(ctx, cacheKey, body, klineCacheTTL).Err()
	}

	c.Header("X-Cache", "MISS")
	c.Data(200, "application/json; charset=utf-8", body)
}
