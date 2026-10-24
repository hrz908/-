package alert

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"stock-agent-server/internal/deps"
	"stock-agent-server/internal/quote"
	"stock-agent-server/internal/resp"
	"stock-agent-server/internal/domain/stockcode"
	"stock-agent-server/internal/util"
	"stock-agent-server/internal/wecom"
	"stock-agent-server/internal/ws"
)

// 提醒防抖锁 TTL（秒）
const alertLockTTL = 60

// stockNameMax 股票名称长度上限，与列类型 varchar(100) 对应
const stockNameMax = 100

// 收盘确认型的生效时刻：A 股 15:00 收盘，收盘前不判定 close 型规则。
// 取的是服务器本地时间，部署时把服务器时区设为 Asia/Shanghai 即可
const closeHour = 15

/* ===================== 请求 / 响应结构 ===================== */

type CreateAlertReq struct {
	StockCode      string `json:"stock_code"`
	StockName      string `json:"stock_name"`
	ConditionType  string `json:"condition_type"`
	Threshold      string `json:"threshold"`
	VolumeBase     int    `json:"volume_base"`
	CheckFrequency string `json:"check_frequency"`
}

// UpdateAlertReq 局部更新：指针为空表示「不改这个字段」
type UpdateAlertReq struct {
	ConditionType  *string `json:"condition_type"`
	Threshold      *string `json:"threshold"`
	VolumeBase     *int    `json:"volume_base"`
	CheckFrequency *string `json:"check_frequency"`
	Enabled        *bool   `json:"enabled"`
}

// AlertItem 提醒规则。current_price / change_percent 来自 Redis，读不到就是 nil
type AlertItem struct {
	ID              int64   `json:"id"`
	StockCode       string  `json:"stock_code"`
	StockName       string  `json:"stock_name"`
	ConditionType   string  `json:"condition_type"`
	Threshold       string  `json:"threshold"`
	VolumeBase      int     `json:"volume_base"`
	CheckFrequency  string  `json:"check_frequency"`
	Enabled         bool    `json:"enabled"`
	CurrentPrice    *string `json:"current_price"`
	ChangePercent   *string `json:"change_percent"`
	LastTriggeredAt *string `json:"last_triggered_at"`
	CreatedAt       string  `json:"created_at"`
}

/* ===================== 各接口的校验点一览 =====================
| 接口               | 校验点                                  | 失败返回                      |
|--------------------|-----------------------------------------|-------------------------------|
| POST   /alerts     | stock_code 缺失或格式非法                | 400 股票代码格式不正确        |
| POST   /alerts     | condition_type 不在枚举内                | 400 条件类型不合法            |
| POST   /alerts     | threshold 非法或超出该类取值范围         | 400 阈值不合法                |
| POST   /alerts     | 成交量异动但 volume_base 不在 5/10/20    | 400 基准周期不合法            |
| POST   /alerts     | check_frequency 不在枚举内               | 400 检查频率不合法            |
| POST   /alerts     | 同用户同股票同条件已有启用规则           | 409 该股票的该条件提醒已存在  |
| GET    /alerts     | 按 user_id 过滤（可选 stock_code）       | 500 查询提醒失败              |
| PUT    /alerts/:id | id 非法                                  | 400 规则 id 不合法            |
| PUT    /alerts/:id | condition_type / threshold 等非法        | 400 对应文案                  |
| PUT    /alerts/:id | id + user_id 定位不到                    | 404 规则不存在或无权限        |
| DELETE /alerts/:id | id 非法                                  | 400 规则 id 不合法            |
| DELETE /alerts/:id | id + user_id 定位不到                    | 404 规则不存在或无权限        |
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
	condType := normalizeCondition(req.ConditionType)
	if !ValidCondition(condType) {
		resp.Fail(c, resp.CodeBadRequest, "条件类型不合法")
		return
	}
	threshold, errMsg := parseThreshold(condType, req.Threshold)
	if errMsg != "" {
		resp.Fail(c, resp.CodeBadRequest, errMsg)
		return
	}
	volumeBase, errMsg := normalizeVolumeBase(condType, req.VolumeBase)
	if errMsg != "" {
		resp.Fail(c, resp.CodeBadRequest, errMsg)
		return
	}
	freq := strings.TrimSpace(req.CheckFrequency)
	if freq == "" {
		freq = FreqRealtime
	}
	if !ValidFrequency(freq) {
		resp.Fail(c, resp.CodeBadRequest, "检查频率不合法")
		return
	}
	// 股票名称长度：列是 varchar(100)，超长时错误来自 DB、返回的是 500，
	// 在入口按 400 挡掉
	stockName := strings.TrimSpace(req.StockName)
	if utf8.RuneCountInString(stockName) > stockNameMax {
		resp.Fail(c, resp.CodeBadRequest, "股票名称过长")
		return
	}

	// 同一用户、同一股票、同一条件类型只允许一条启用规则。
	// 这里是给用户的友好提示；并发下两个请求可能同时通过检查，
	// 真正的保证是 (user_id, stock_code, condition_type) 唯一索引，
	// 冲突时下面的 Create 会返回 409
	var exist AlertRule
	err := deps.DB.WithContext(ctx).
		Where("user_id = ? AND stock_code = ? AND condition_type = ? AND enabled = ?",
			userID, code, condType, true).
		First(&exist).Error
	if err == nil {
		resp.Fail(c, resp.CodeConflict, "该股票的该条件提醒已存在")
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
		StockName:      stockName,
		ConditionType:  condType,
		Threshold:      threshold,
		VolumeBase:     volumeBase,
		CheckFrequency: freq,
		Enabled:        true,
	}
	if err := deps.DB.WithContext(ctx).Create(&rule).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			resp.Fail(c, resp.CodeConflict, "该股票的该条件提醒已存在")
			return
		}
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

	// 条件类型的最终值：请求带了就用新的，否则沿用库里的，
	// 阈值 / 基准周期的校验要跟着最终值走
	condType := rule.ConditionType
	if req.ConditionType != nil {
		condType = normalizeCondition(*req.ConditionType)
		if !ValidCondition(condType) {
			resp.Fail(c, resp.CodeBadRequest, "条件类型不合法")
			return
		}
	}

	updates := map[string]any{}
	if req.ConditionType != nil {
		updates["condition_type"] = condType
	}
	if req.Threshold != nil {
		v, msg := parseThreshold(condType, *req.Threshold)
		if msg != "" {
			resp.Fail(c, resp.CodeBadRequest, msg)
			return
		}
		updates["threshold"] = v
	}
	if req.VolumeBase != nil {
		n, msg := normalizeVolumeBase(condType, *req.VolumeBase)
		if msg != "" {
			resp.Fail(c, resp.CodeBadRequest, msg)
			return
		}
		updates["volume_base"] = n
	} else if condType != CondVolume {
		// 从成交量切成其它条件时，基准周期必须归 0，
		// 否则库里留着 20/10/5，与创建时的归一化不一致
		updates["volume_base"] = 0
	}
	if req.CheckFrequency != nil {
		if !ValidFrequency(*req.CheckFrequency) {
			resp.Fail(c, resp.CodeBadRequest, "检查频率不合法")
			return
		}
		updates["check_frequency"] = *req.CheckFrequency
	}
	// 判定条件变了就清掉上次触发时间，让它能重新提醒
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
		delAlertLock(ctx, userID, rule)
	}

	var fresh AlertRule
	if err := deps.DB.WithContext(ctx).
		Where("id = ? AND user_id = ?", id, userID).
		First(&fresh).Error; err != nil {
		// 重查失败不能静默用旧值响应：客户端会拿到修改前的数据还以为成功了
		log.Printf("reload alert rule failed: id=%d err=%v", id, err)
		resp.Fail(c, resp.CodeInternal, "修改提醒失败")
		return
	} else {
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
	delAlertLock(ctx, userID, rule)

	resp.OK(c, gin.H{"id": id})
}

/* ===================== 刷新后的提醒检查 ===================== */

// checkAlerts 检查该用户的提醒规则，命中就推送提醒（WebSocket 实时推送 +
// 企业微信应用消息，两条通道并存）。
// 防重复：SetNX alert:lock:{user_id}:{stock_code}:{condition_type}，TTL 60 秒；
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
		// 收盘确认型：只在收盘后判定，盘中即时型每次刷新都判
		if r.CheckFrequency == FreqClose && now.Hour() < closeHour {
			continue
		}
		if !isHit(r, snap) {
			continue
		}

		if deps.RDB != nil {
			ok, err := deps.RDB.SetNX(ctx, alertLockKey(userID, r), now.Unix(),
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
			"change_percent":  snap.ChangePercent.StringFixed(2),
			"condition_type":  r.ConditionType,
			"threshold":       r.Threshold.StringFixed(2),
			"volume_base":     r.VolumeBase,
			"check_frequency": r.CheckFrequency,
			"time":            now.Format(time.RFC3339),
		})

		// 额外走企业微信应用消息（@all）：与 WebSocket 提醒并存。
		// 异步发送，避免同步 HTTP 阻塞行情刷新循环；失败只记日志，
		// 不影响已完成的 WebSocket 提醒。
		// 独立 goroutine 不经过 gin 的 Recovery 中间件，必须自带 panic 兜底，
		// 否则 wecom 包一个 panic 就会击穿整个进程
		if wecom.Enabled() {
			content := buildWecomText(snap, r, now)
			stockCode := r.StockCode
			go func() {
				defer func() {
					if p := recover(); p != nil {
						log.Printf("wecom push panic: code=%s err=%v", stockCode, p)
					}
				}()
				if err := wecom.SendTextToAll(content); err != nil {
					log.Printf("wecom push failed: code=%s err=%v", stockCode, err)
				}
			}()
		}

		if err := deps.DB.WithContext(ctx).Model(&AlertRule{}).
			Where("id = ? AND user_id = ?", r.ID, userID).
			Update("last_triggered_at", now).Error; err != nil {
			log.Printf("update last_triggered_at failed: id=%d err=%v", r.ID, err)
		}
	}
}

// isHit 判断单条规则是否命中行情快照
func isHit(r AlertRule, snap *quote.StockSnapshot) bool {
	// 停牌 / 自建接口返 0 时价格是 0：0 <= 任意正阈值会让「跌破」类规则恒真，
	// 用户会收到假提醒。没行情就不判
	if snap == nil || snap.Price.IsZero() {
		return false
	}
	switch r.ConditionType {
	case CondPriceUp:
		return snap.Price.GreaterThanOrEqual(r.Threshold)
	case CondPriceDown:
		return snap.Price.LessThanOrEqual(r.Threshold)
	case CondPctUp:
		return snap.ChangePercent.GreaterThanOrEqual(r.Threshold)
	case CondPctDown:
		// 前端填的是「跌多少」的正数，比较时取负
		return snap.ChangePercent.LessThanOrEqual(r.Threshold.Neg())
	case CondVolume:
		// 行情快照里没有成交量字段，成交量异动只存规则、暂不自动判触发。
		// 等 quote 补上成交量与 N 日均量数据后，在这里补上比较逻辑即可
		return false
	default:
		return false
	}
}

/* ===================== 内部工具 ===================== */

// toAlertItem 组装返回项，顺带附上 Redis 里的当前价与涨跌幅；
// 缓存没命中时这两个字段为 nil，前端据此显示「暂无报价」
func toAlertItem(r AlertRule, snap *quote.StockSnapshot) AlertItem {
	item := AlertItem{
		ID:             r.ID,
		StockCode:      r.StockCode,
		StockName:      r.StockName,
		ConditionType:  r.ConditionType,
		Threshold:      r.Threshold.StringFixed(2),
		VolumeBase:     r.VolumeBase,
		CheckFrequency: r.CheckFrequency,
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
		cp := snap.ChangePercent.StringFixed(2)
		item.ChangePercent = &cp
	}
	return item
}

// alertLockKey 提醒防抖锁：alert:lock:{user_id}:{stock_code}:{condition_type}
// 带上条件类型，同一股票的多条不同条件规则互不干扰
func alertLockKey(userID int64, r AlertRule) string {
	return fmt.Sprintf("alert:lock:%d:%s:%s", userID, r.StockCode, r.ConditionType)
}

// delAlertLock 清掉某条规则的防抖锁，失败只记日志
func delAlertLock(ctx context.Context, userID int64, r AlertRule) {
	if deps.RDB == nil {
		return
	}
	if err := deps.RDB.Del(ctx, alertLockKey(userID, r)).Err(); err != nil {
		log.Printf("del alert lock failed: code=%s err=%v", r.StockCode, err)
	}
}

// uniqueCodes 规则里的股票代码去重（供批量读行情用）
func uniqueCodes(rules []AlertRule) []string {
	codes := make([]string, 0, len(rules))
	for _, r := range rules {
		codes = append(codes, r.StockCode)
	}
	return util.Unique(codes)
}

// parseThreshold 解析阈值：必须是数字，且落在对应条件的取值范围内
func parseThreshold(condType, raw string) (decimal.Decimal, string) {
	v, err := decimal.NewFromString(strings.TrimSpace(raw))
	if err != nil {
		return decimal.Zero, "阈值必须是数字"
	}
	switch condType {
	case CondPriceUp, CondPriceDown:
		if !v.IsPositive() {
			return decimal.Zero, "价格阈值必须大于 0"
		}
	case CondPctUp, CondPctDown:
		if !v.IsPositive() || v.GreaterThan(decimal.NewFromInt(100)) {
			return decimal.Zero, "涨跌幅阈值需在 0 ~ 100 之间"
		}
	case CondVolume:
		if v.LessThan(decimal.NewFromFloat(0.1)) || v.GreaterThan(decimal.NewFromInt(100)) {
			return decimal.Zero, "成交量倍数需在 0.1 ~ 100 之间"
		}
	}
	return v, ""
}

// normalizeVolumeBase 只有成交量异动需要基准周期，且必须在 5 / 10 / 20 内；
// 其他条件一律归 0，避免库里留下无意义的取值。
// 成交量条件下传 0 表示「用默认值 20 日均量」，不是报错
func normalizeVolumeBase(condType string, n int) (int, string) {
	if condType != CondVolume {
		return 0, ""
	}
	if n == 0 {
		return 20, ""
	}
	if !ValidVolumeBase(n) {
		return 0, "基准周期只能是 5 / 10 / 20 日均量"
	}
	return n, ""
}

// normalizeCondition 条件类型统一小写去空格：与 CheckFrequency 的处理一致，
// 否则 " price_up" 会被判成非法
func normalizeCondition(t string) string {
	return strings.ToLower(strings.TrimSpace(t))
}

// buildWecomText 组装企业微信文本消息内容。
// 字段与 WebSocket 推送保持一致；text 类型不渲染 markdown，故用纯文本
func buildWecomText(snap *quote.StockSnapshot, r AlertRule, t time.Time) string {
	name := r.StockName
	if name == "" {
		name = snap.Name
	}
	var b strings.Builder
	b.WriteString("📊 股票提醒\n")
	fmt.Fprintf(&b, "%s（%s）%s\n", name, r.StockCode, conditionLabel(r.ConditionType))
	fmt.Fprintf(&b, "当前价：%s\n", util.Money(snap.Price))
	fmt.Fprintf(&b, "涨跌幅：%s%%\n", snap.ChangePercent.StringFixed(2))
	fmt.Fprintf(&b, "触发阈值：%s\n", r.Threshold.StringFixed(2))
	b.WriteString("时间：" + t.Format("2006-01-02 15:04:05"))
	return b.String()
}

// conditionLabel 条件类型的中文描述，用于企业微信消息文案
func conditionLabel(condType string) string {
	switch condType {
	case CondPriceUp:
		return "价格突破"
	case CondPriceDown:
		return "价格跌破"
	case CondPctUp:
		return "涨幅提醒"
	case CondPctDown:
		return "跌幅提醒"
	case CondVolume:
		return "成交量异动"
	default:
		return "提醒触发"
	}
}
