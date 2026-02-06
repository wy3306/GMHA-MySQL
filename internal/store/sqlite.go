package store

import (
	"fmt"
	"os"
	"path/filepath"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// DB 是全局数据库实例
var DB *gorm.DB

// InitSQLite 初始化 SQLite 数据库
// 注意：此函数不再负责 AutoMigrate，以避免循环依赖
func InitSQLite(dataPath string) error {
	// 确保目录存在
	dir := filepath.Dir(dataPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create data dir failed: %w", err)
	}

	// 连接 SQLite
	var err error
	DB, err = gorm.Open(sqlite.Open(dataPath), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Warn),
	})
	if err != nil {
		return fmt.Errorf("failed to connect database: %w", err)
	}

	return nil
}

// GetDB 获取数据库实例
func GetDB() *gorm.DB {
	return DB
}
