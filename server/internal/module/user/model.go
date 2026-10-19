package user

import "time"

// User 对应 users 表：一个微信用户一行，openid 唯一。
// 微信不提供微信号（wx.login 只给 openid），所以这里不存 wechat_id
type User struct {
	ID        int64     `gorm:"primaryKey;column:id" json:"id"`
	OpenID    string    `gorm:"column:openid;type:varchar(64);not null;uniqueIndex" json:"openid"`
	Nickname  string    `gorm:"column:nickname;type:varchar(64)" json:"nickname"`
	Avatar    string    `gorm:"column:avatar;type:varchar(512)" json:"avatar"` // 站内相对路径，如 /uploads/avatars/xxx.png
	CreatedAt time.Time `gorm:"column:created_at;autoCreateTime" json:"created_at"`
	UpdatedAt time.Time `gorm:"column:updated_at;autoUpdateTime" json:"updated_at"`
}

func (User) TableName() string { return "users" }

// Models 供 db.AutoMigrate 统一收集，新增表时往这里加
var Models = []any{
	&User{},
}
