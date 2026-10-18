package db

import (
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// NewPostgres 建立 GORM 连接
func NewPostgres(dsn string) (*gorm.DB, error) {
	return gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger:         logger.Default.LogMode(logger.Warn),
		TranslateError: true,
	})
}

// AutoMigrate 建表并补索引。
// 模型由各业务模块自己导出（module/xxx.Models），在 main 里汇总后传进来，
// 这样新增模块时不用改本文件
func AutoMigrate(db *gorm.DB, models ...any) error {
	if len(models) == 0 {
		return nil
	}
	return db.AutoMigrate(models...)
}
