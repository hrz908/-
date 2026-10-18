package alert

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"stock-agent-server/internal/deps"
	"stock-agent-server/internal/quote"
	"stock-agent-server/internal/resp"
	"stock-agent-server/internal/stock"
	"stock-agent-server/internal/util"
	"stock-agent-server/internal/ws"
)

// 提醒防抖锁 TTL（秒）
const alertLockTTL = 60

/* ===================== 请求 / 响应结构 ===================== */

type CreateAlertReq struct {
	StockCode      string `json:"stock_code"`
	ConditionType  string `json:"condition_type"`
	ConditionValue string `json:"condition_value"`
}

// UpdateAlertReq 局部更新：指针为空表示「不改这个字段」
type UpdateAlertReq struct {
	ConditionType  *string `json:"condition_type"`
	ConditionValue *string `json:"condition_value"`
	Enabled        *bool   `json:"enabled"`
}

// AlertItem 提醒规则。current_price 来自 Redis，读不到就是 nil
type AlertItem struct {
	ID              int64   `json:"id"`
	StockCode       string  `json:"stock_code"`
	ConditionType   string  `json:"condition_type"`
	ConditionValue  string  `json:"condition_value"`
	Enabled         bool    `json:"enabled"`
	CurrentPrice    *string `json:"current_price"`
	LastTriggeredAt *string `json:"last_triggered_at"`
	CreatedAt       string  `json:"created_at"`
}

/* ===================== 四个接口的校验点一览 =====================
| 接口              | 校验点                                   | 失败返回                      |
|-------------------|------------------------------------------|-------------------------------|
| POST   /alerts    | stock_code 缺失或格式非法                 | 400 股票代码格式不正确        |
| POST   /alerts    | condition_type 不在枚举内                 | 400 条件类型不合法            |
| POST   /alerts    | condition_value 非数字或 <= 0             | 400 目标价必须大于 0          |
| POST   /alerts    | 同用户同股票同方向已有启用规则            | 409 该股票的该方向提醒已存在  |
| GET    /alerts    | 按 user_id 过滤（可选 stock_code）        | 500 查询提醒失败              |
| PUT    /alerts/:id| id 非法                                   | 400 规则 id 不合法            |
| PUT    /alerts/:id| condition_type 不在枚举内                 | 400 条件类型不合法            |
| PUT    /alerts/:id| condition_value 非数字或 <= 0             | 400 目标价必须大于 0          |
| PUT    /alerts/:id| id + user_id 定位不到                     | 404 规则不存在或无权限        |
| DELETE /alerts/:id| id 非法                                   | 400 规则 id 不合法            |
| DELETE /alerts/:id| id + user_id 定位不到                     | 404 规则不存在或无权限        |
=========================================================== */

/* ===================== 新建 ===================== */

// CreateAlert POST /api/v1/alerts
func CreateAlert(c *gin.Context) {
	ctx := c.Request.Context()
	userID := c.GetInt64("user_id")

	var req CreateAlertReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.CodeBadRequest, "请求参数不合法")
		return
	}

	code, ok := stock.NormalizeCode(req.StockCode)
	if !ok {
		resp.Fail(c, resp.CodeBadRequest, "股票代码格式不正确，应为 SH/SZ/BJ + 6 位数字")
		return
	}
	if !ValidCondition(req.ConditionType) {
		resp.Fail(c, resp.CodeBadRequest, "条件类型不合法")
		return
	}
	target, errMsg := parseTarget(req.ConditionValue)
	if errMsg != "" {
		resp.Fail(c, resp.CodeBadRequest, errMsg)
		return
	}

	// 同一用户、同一股票、同一方向只允许一条启用规则
	var exist AlertRule
	err := deps.DB.WithContext(ctx).
		Where("user_id = ? AND stock_code = ? AND condition_type = ? AND enabled = ?",
			userID, code, req.ConditionType, true).
		First(&exist).Error
	if err == nil {
		resp.Fail(c, resp.CodeConflict, "该股票的该方向提醒已存在")
		return
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		log.Printf("query alert rule failed: user=%d err=%v", userID, err)
		resp.Fail(c, resp.CodeInternal, "创建提醒失败")
		return
	}

	rule := AlertRule{
		UserID:         userID,
		StockCode:      code,
		ConditionType:  req.ConditionType,
		ConditionValue: target,
		Enabled:        true,
	}
	if err := deps.DB.WithContext(ctx).Create(&rule).Error; err != nil {
		log.Printf("create alert rule failed: user=%d code=%s err=%v", userID, code, err)
		resp.Fail(c, resp.CodeInternal, "创建提醒失败")
		return
	}

	resp.OK(c, toAlertItem(rule, nil))
}

/* ===================== 列表 ===================== */

// ListAlerts GET /api/v1/alerts（可按 stock_code 过滤）
func ListAlerts(c *gin.Context) {
	ctx := c.Request.Context()
	userID := c.GetInt64("user_id")

	q := deps.DB.WithContext(ctx).Where("user_id = ?", userID)
	if raw := strings.TrimSpace(c.Query("stock_code")); raw != "" {
		code, ok := stock.NormalizeCode(raw)
		if !ok {
			resp.Fail(c, resp.CodeBadRequest, "股票代码格式不正确，应为 SH/SZ/BJ + 6 位数字")
			return
		}
		q = q.Where("stock_code = ?", code)
	}

	var rules []AlertRule
	if err := q.Order("created_at DESC").Find(&rules).Error; err != nil {
		log.Printf("list alert rules failed: user=%d err=%v", userID, err)
		resp.Fail(c, resp.CodeInternal, "查询提醒失败")
		return
	}

	// 批量读当前价，前端可直接展示「距离目标还差多少」
	quotes, err := quote.BatchGetQuotes(ctx, uniqueCodes(rules))
	if err != nil {
		log.Printf("batch get quotes for alerts failed: %v", err)
		quotes = map[string]*quote.StockSnapshot{}
	}

	items := make([]AlertItem, 0, len(rules))
	for _, r := range rules {
		items = append(items, toAlertItem(r, quotes[r.StockCode]))
	}
	resp.OK(c, items)
}

/* ===================== 修改 ===================== */

// UpdateAlert PUT /api/v1/alerts/:id
func UpdateAlert(c *gin.Context) {
	ctx := c.Request.Context()
	userID := c.GetInt64("user_id")

	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		resp.Fail(c, resp.CodeBadRequest, "规则 id 不合法")
		return
	}

	var req UpdateAlertReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.CodeBadRequest, "请求参数不合法")
		return
	}

	// 先用 id + user_id 定位：查不到统一 404。
	// 刻意不区分「不存在」与「不是本人的」，避免被枚举探测他人规则 id
	var rule AlertRule
	if err := deps.DB.WithContext(ctx).
		Where("id = ? AND user_id = ?", id, userID).
		First(&rule).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			resp.Fail(c, resp.CodeNotFound, "规则不存在或无权限")
			return
		}
		log.Printf("query alert rule failed: id=%d err=%v", id, err)
		resp.Fail(c, resp.CodeInternal, "修改提醒失败")
		return
	}

	updates := map[string]any{}
	if req.ConditionType != nil {
		if !ValidCondition(*req.ConditionType) {
			resp.Fail(c, resp.CodeBadRequest, "条件类型不合法")
			return
		}
		updates["condition_type"] = *req.ConditionType
	}
	if req.ConditionValue != nil {
		v, msg := parseTarget(*req.ConditionValue)
		if msg != "" {
			resp.Fail(c, resp.CodeBadRequest, msg)
			return
		}
		updates["condition_value"] = v
	}
	// 条件变了就清掉上次触发时间，让它能重新提醒
	if len(updates) > 0 {
		updates["last_triggered_at"] = nil
	}
	if req.Enabled != nil {
		updates["enabled"] = *req.Enabled
	}

	if len(updates) == 0 {
		resp.OK(c, toAlertItem(rule, nil))
		return
	}
	// 更新同样带 user_id 过滤，双重保险
	if err := deps.DB.WithContext(ctx).Model(&AlertRule{}).
		Where("id = ? AND user_id = ?", id, userID).
		Updates(updates).Error; err != nil {
		log.Printf("update alert rule failed: id=%d err=%v", id, err)
		resp.Fail(c, resp.CodeInternal, "修改提醒失败")
		return
	}

	// 停用即解锁：清掉防抖锁，避免残留锁挡住后续推送
	if req.Enabled != nil && !*req.Enabled {
		delAlertLock(ctx, userID, rule.StockCode)
	}

	var fresh AlertRule
	if err := deps.DB.WithContext(ctx).
		Where("id = ? AND user_id = ?", id, userID).
		First(&fresh).Error; err == nil {
		rule = fresh
	}
	snap, _ := quote.GetQuoteFromRedis(ctx, rule.StockCode)
	resp.OK(c, toAlertItem(rule, snap))
}

/* ===================== 删除 ===================== */

// DeleteAlert DELETE /api/v1/alerts/:id
// 采用硬删：表里没有 deleted_at 字段，且规则是用户私有资源，不需要回收站；
// 如后续要审计，再加 deleted_at 改软删即可
func DeleteAlert(c *gin.Context) {
	ctx := c.Request.Context()
	userID := c.GetInt64("user_id")

	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		resp.Fail(c, resp.CodeBadRequest, "规则 id 不合法")
		return
	}

	var rule AlertRule
	if err := deps.DB.WithContext(ctx).
		Where("id = ? AND user_id = ?", id, userID).
		First(&rule).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			resp.Fail(c, resp.CodeNotFound, "规则不存在或无权限")
			return
		}
		log.Printf("query alert rule failed: id=%d err=%v", id, err)
		resp.Fail(c, resp.CodeInternal, "删除提醒失败")
		return
	}

	if err := deps.DB.WithContext(ctx).
		Where("id = ? AND user_id = ?", id, userID).
		Delete(&AlertRule{}).Error; err != nil {
		log.Printf("delete alert rule failed: id=%d err=%v", id, err)
		resp.Fail(c, resp.CodeInternal, "删除提醒失败")
		return
	}

	// 删除后清防抖锁，否则残留锁会挡住同一股票后续新建的规则
	delAlertLock(ctx, userID, rule.StockCode)

	resp.OK(c, gin.H{"id": id})
}

/* ===================== 刷新后的提醒检查 ===================== */

// checkAlerts 检查该用户的提醒规则，命中就通过 WebSocket 推送。
// 防重复：SetNX alert:lock:{user_id}:{stock_code}，TTL 60 秒；
//
//	写不进去说明 60 秒内已经推过，直接跳过
func checkAlerts(userID int64, ctx context.Context) {
	var rules []AlertRule
	if err := deps.DB.WithContext(ctx).
		Where("user_id = ? AND enabled = ?", userID, true).
		Find(&rules).Error; err != nil {
		log.Printf("query alert rules failed: user=%d err=%v", userID, err)
		return
	}
	if len(rules) == 0 {
		return
	}

	quotes, err := quote.BatchGetQuotes(ctx, uniqueCodes(rules))
	if err != nil {
		log.Printf("batch get quotes for alerts failed: %v", err)
		return
	}

	now := time.Now()
	for _, r := range rules {
		snap := quotes[r.StockCode]
		if snap == nil {
			continue // 没行情不判
		}
		if !isHit(r.ConditionType, snap.Price, r.ConditionValue) {
			continue
		}

		if deps.RDB != nil {
			ok, err := deps.RDB.SetNX(ctx, alertLockKey(userID, r.StockCode), now.Unix(),
				time.Duration(alertLockTTL)*time.Second).Result()
			if err != nil {
				log.Printf("set alert lock failed: code=%s err=%v", r.StockCode, err)
				continue
			}
			if !ok {
				continue // 60 秒内已推过，防重复
			}
		}

		ws.Push(userID, map[string]any{
			"type":            "alert",
			"stock_code":      r.StockCode,
			"stock_name":      snap.Name,
			"price":           util.Money(snap.Price),
			"condition_type":  r.ConditionType,
			"condition_value": r.ConditionValue.StringFixed(2),
			"time":            now.Format(time.RFC3339),
		})

		if err := deps.DB.WithContext(ctx).Model(&AlertRule{}).
			Where("id = ? AND user_id = ?", r.ID, userID).
			Update("last_triggered_at", now).Error; err != nil {
			log.Printf("update last_triggered_at failed: id=%d err=%v", r.ID, err)
		}
	}
}

// isHit 判断单条规则是否命中
func isHit(condType string, price, target decimal.Decimal) bool {
	switch condType {
	case CondPriceGTE:
		return price.GreaterThanOrEqual(target)
	case CondPriceLTE:
		return price.LessThanOrEqual(target)
	default:
		return false
	}
}

/* ===================== 内部工具 ===================== */

// toAlertItem 组装返回项，顺带附上 Redis 里的当前价；
// 缓存没命中时 current_price 为 nil，前端据此显示「暂无报价」
func toAlertItem(r AlertRule, snap *quote.StockSnapshot) AlertItem {
	item := AlertItem{
		ID:             r.ID,
		StockCode:      r.StockCode,
		ConditionType:  r.ConditionType,
		ConditionValue: r.ConditionValue.StringFixed(2),
		Enabled:        r.Enabled,
		CreatedAt:      r.CreatedAt.Format(time.RFC3339),
	}
	if r.LastTriggeredAt != nil {
		t := r.LastTriggeredAt.Format(time.RFC3339)
		item.LastTriggeredAt = &t
	}
	if snap != nil {
		p := util.Money(snap.Price)
		item.CurrentPrice = &p
	}
	return item
}

// alertLockKey 提醒防抖锁：alert:lock:{user_id}:{stock_code}
func alertLockKey(userID int64, stockCode string) string {
	return fmt.Sprintf("alert:lock:%d:%s", userID, stockCode)
}

// delAlertLock 清掉某股票在该用户下的提醒防抖锁，失败只记日志
func delAlertLock(ctx context.Context, userID int64, stockCode string) {
	if deps.RDB == nil {
		return
	}
	if err := deps.RDB.Del(ctx, alertLockKey(userID, stockCode)).Err(); err != nil {
		log.Printf("del alert lock failed: code=%s err=%v", stockCode, err)
	}
}

func uniqueCodes(rules []AlertRule) []string {
	seen := make(map[string]bool, len(rules))
	out := make([]string, 0, len(rules))
	for _, r := range rules {
		if !seen[r.StockCode] {
			seen[r.StockCode] = true
			out = append(out, r.StockCode)
		}
	}
	return out
}

// parseTarget 解析提醒目标价：必须是数字且 > 0
func parseTarget(raw string) (decimal.Decimal, string) {
	v, err := decimal.NewFromString(strings.TrimSpace(raw))
	if err != nil {
		return decimal.Zero, "目标价必须是数字"
	}
	if !v.IsPositive() {
		return decimal.Zero, "目标价必须大于 0"
	}
	return v, ""
}
