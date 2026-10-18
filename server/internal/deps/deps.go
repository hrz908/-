package deps

import (
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

// 提示词里「可直接使用的全局变量」集中放在这里，main 启动时注入一次，
// 业务包统一通过 deps.DB / deps.RDB 访问，避免到处传参
var (
	DB  *gorm.DB
	RDB *redis.Client
)

func Init(db *gorm.DB, rdb *redis.Client) {
	DB = db
	RDB = rdb
}
