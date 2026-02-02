package model

import "gorm.io/gorm"

// AutoMigrate 执行数据库自动迁移
func AutoMigrate(db *gorm.DB) error {
	return db.AutoMigrate(
		&Cluster{},
		&Machine{},
		&Database{},
	)
}
