// Package model 存放跨模块共享的数据模型。
//
// User 同时被「登录」（按 openid 建号）与「用户资料」（读写昵称头像）两个模块使用；
// 放在这里避免模块之间横向 import（README 分层约定：module/xxx 是业务，不横向依赖）
package model

import "time"

// User 对应 users 表：一个微信用户一行，openid 唯一。
// 微信不提供微信号（wx.login 只给 openid），故不存 wechat_id
type User struct {
	ID        int64     `gorm:"primaryKey;column:id"`
	OpenID    string    `gorm:"column:openid;type:varchar(64);not null;uniqueIndex"`
	Nickname  string    `gorm:"column:nickname;type:varchar(64)"`
	Avatar    string    `gorm:"column:avatar;type:varchar(512)"` // 站内相对路径，如 /uploads/avatars/xxx.png
	CreatedAt time.Time `gorm:"column:created_at;autoCreateTime"`
	UpdatedAt time.Time `gorm:"column:updated_at;autoUpdateTime"`
}

func (User) TableName() string { return "users" }

// Models 供 db.AutoMigrate 统一收集，新增表时往这里加
var Models = []any{
	&User{},
}
