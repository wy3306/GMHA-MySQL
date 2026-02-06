package model

import (
	"gorm.io/gorm"
)

// AutoMigrate 执行数据库迁移，自动创建或更新表结构
func AutoMigrate(db *gorm.DB) error {
	return db.AutoMigrate(
		&Cluster{},
		&Machine{},
		&NetworkInterface{},
		&IPAddress{},
		&Agent{},
	)
}
