package alert

import (
	"time"

	"github.com/shopspring/decimal"
)

// 提醒条件枚举：集中定义，业务代码里不出现魔法字符串
const (
	CondPriceGTE = "price_gte" // 最新价 >= 目标价
	CondPriceLTE = "price_lte" // 最新价 <= 目标价
)

// ValidCondition 判断条件类型是否合法
func ValidCondition(t string) bool {
	return t == CondPriceGTE || t == CondPriceLTE
}

// AlertRule 对应 alert_rule 表。
// 查询 / 更新 / 删除都必须带 user_id 过滤，防止跨用户操作
type AlertRule struct {
	ID              int64           `gorm:"primaryKey;column:id" json:"id"`
	UserID          int64           `gorm:"column:user_id;not null;index:idx_alert_user" json:"user_id"`
	StockCode       string          `gorm:"column:stock_code;type:varchar(20);not null" json:"stock_code"`
	ConditionType   string          `gorm:"column:condition_type;type:varchar(20);not null" json:"condition_type"`
	ConditionValue  decimal.Decimal `gorm:"column:condition_value;type:numeric(18,4);not null" json:"condition_value"`
	Enabled         bool            `gorm:"column:enabled;default:true" json:"enabled"`
	CreatedAt       time.Time       `gorm:"column:created_at;autoCreateTime" json:"created_at"`
	LastTriggeredAt *time.Time      `gorm:"column:last_triggered_at" json:"last_triggered_at"`
}

func (AlertRule) TableName() string { return "alert_rule" }

// Models 供 db.AutoMigrate 统一收集
var Models = []any{
	&AlertRule{},
}
