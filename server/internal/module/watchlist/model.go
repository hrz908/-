package watchlist

import (
	"time"

	"github.com/shopspring/decimal"
)

// UserWatchlist 对应 user_watchlist 表。
// 唯一索引 (user_id, stock_code) 由标签声明，AutoMigrate 时建立，
// 也是「添加自选股」Upsert 的冲突判定依据
type UserWatchlist struct {
	ID        int64           `gorm:"primaryKey;column:id" json:"id"`
	UserID    int64           `gorm:"column:user_id;not null;uniqueIndex:uk_user_stock,priority:1" json:"user_id"`
	StockCode string          `gorm:"column:stock_code;type:varchar(20);not null;uniqueIndex:uk_user_stock,priority:2" json:"stock_code"`
	StockName string          `gorm:"column:stock_name;type:varchar(100)" json:"stock_name"`
	CostPrice decimal.Decimal `gorm:"column:cost_price;type:numeric(18,4);default:0" json:"cost_price"`
	Quantity  int             `gorm:"column:quantity;default:0" json:"quantity"`
	Enabled   bool            `gorm:"column:enabled;default:true" json:"enabled"`
	CreatedAt time.Time       `gorm:"column:created_at;autoCreateTime" json:"created_at"`
	UpdatedAt time.Time       `gorm:"column:updated_at;autoUpdateTime" json:"updated_at"`
}

func (UserWatchlist) TableName() string { return "user_watchlist" }

// Models 供 db.AutoMigrate 统一收集，新增表时往这里加
var Models = []any{
	&UserWatchlist{},
}
