package model

import (
	"GMHA-MySQL/internal/store"
	"context"
	"time"

	"gorm.io/gorm"
)

// Database 数据库实例信息
type Database struct {
	ID        uint   `gorm:"primaryKey"`
	ClusterID string `gorm:"type:varchar(64);index"`
	MachineID string `gorm:"type:varchar(64);index"`

	IP   string `gorm:"type:varchar(64);not null"`
	Port int

	// 账号信息
	ReplUser     string `gorm:"type:varchar(64)"`
	ReplPassword string `gorm:"type:varchar(255)"`

	// 实例状态
	Version   string `gorm:"type:varchar(32)"`
	Status    string `gorm:"type:varchar(32)"`
	IsMaster  bool
	ReplDelay float64 // 主从延迟

	// 主库信息
	MasterIP   string `gorm:"type:varchar(64)"`
	MasterPort int

	// 备份策略
	BackupEnabled bool
	BackupPolicy  string `gorm:"type:varchar(255)"`

	CreatedAt time.Time
	UpdatedAt time.Time
}

// ---------------------------------------------------------------------
// Database Repository
// ---------------------------------------------------------------------

// DatabaseRepository 定义数据库实例操作接口
type DatabaseRepository interface {
	Create(ctx context.Context, db *Database) error
	Update(ctx context.Context, db *Database) error
	Delete(ctx context.Context, id uint) error
	Get(ctx context.Context, id uint) (*Database, error)
	ListByCluster(ctx context.Context, clusterID string) ([]Database, error)
}

type databaseRepo struct {
	db *gorm.DB
}

func NewDatabaseRepository(db *gorm.DB) DatabaseRepository {
	if db == nil {
		db = store.GetDB()
	}
	return &databaseRepo{db: db}
}

func (r *databaseRepo) Create(ctx context.Context, db *Database) error {
	return r.db.WithContext(ctx).Create(db).Error
}

func (r *databaseRepo) Update(ctx context.Context, db *Database) error {
	return r.db.WithContext(ctx).Save(db).Error
}

func (r *databaseRepo) Delete(ctx context.Context, id uint) error {
	return r.db.WithContext(ctx).Delete(&Database{}, id).Error
}

func (r *databaseRepo) Get(ctx context.Context, id uint) (*Database, error) {
	var db Database
	err := r.db.WithContext(ctx).First(&db, id).Error
	if err != nil {
		return nil, err
	}
	return &db, nil
}

func (r *databaseRepo) ListByCluster(ctx context.Context, clusterID string) ([]Database, error) {
	var list []Database
	err := r.db.WithContext(ctx).Where("cluster_id = ?", clusterID).Find(&list).Error
	return list, err
}
