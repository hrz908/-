package db

import (
	"context"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// 连接池：不设上限时突发流量会把 Postgres 连接打满，
// 取值与本地容器资源匹配（compose 未单独限流，按单实例默认档给）
const (
	maxOpenConns    = 25
	maxIdleConns    = 25
	connMaxLifetime = 5 * time.Minute
	pingTimeout     = 3 * time.Second
)

// NewPostgres 建立 GORM 连接：配好连接池并立即探活。
// 探活是为了让连接串写错这类问题在启动时就暴露，而不是等第一个业务请求
func NewPostgres(dsn string) (*gorm.DB, error) {
	gdb, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger:         logger.Default.LogMode(logger.Warn),
		TranslateError: true,
	})
	if err != nil {
		return nil, err
	}

	sqlDB, err := gdb.DB()
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxOpenConns(maxOpenConns)
	sqlDB.SetMaxIdleConns(maxIdleConns)
	sqlDB.SetConnMaxLifetime(connMaxLifetime)

	ctx, cancel := context.WithTimeout(context.Background(), pingTimeout)
	defer cancel()
	if err := sqlDB.PingContext(ctx); err != nil {
		return nil, err
	}
	return gdb, nil
}

// AutoMigrate 建表并补索引。
// 模型由各业务模块自己导出（module/xxx.Models），在 main 里汇总后传进来，
// 这样新增模块时不用改本文件。
// 注意：AutoMigrate 只会新增列/索引，不会改名或删列；
// 老库的结构升级走 migrations/ 下的脚本（与 compose 的 initdb 配合）
func AutoMigrate(db *gorm.DB, models ...any) error {
	if len(models) == 0 {
		return nil
	}
	return db.AutoMigrate(models...)
}
