package deps

import (
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

// 全局依赖集中在此，main 启动时注入一次；
// 业务包统一通过 deps.DB / deps.RDB 访问，避免层层传参
var (
	DB  *gorm.DB
	RDB *redis.Client
)

func Init(db *gorm.DB, rdb *redis.Client) {
	DB = db
	RDB = rdb
}
