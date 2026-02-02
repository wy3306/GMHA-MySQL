package model

import (
	"GMHA-MySQL/internal/store"
	"context"
	"time"

	"gorm.io/gorm"
)

// ClusterStatus 定义集群状态枚举
type ClusterStatus int

const (
	ClusterStatusUnknown ClusterStatus = iota
	ClusterStatusNormal
	ClusterStatusWarning
	ClusterStatusError
)

// Cluster 集群信息
type Cluster struct {
	ID               string `gorm:"primaryKey;type:varchar(64)"`
	Name             string `gorm:"uniqueIndex;type:varchar(128);not null"`
	Description      string `gorm:"type:varchar(255)"`
	VIP              string `gorm:"type:varchar(64)"`
	VIPEnabled       bool
	HasL3Switch      bool
	Status           ClusterStatus
	HasSplitBrain    bool
	SSHTrustComplete bool

	// 关联
	Machines  []Machine  `gorm:"foreignKey:ClusterID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE;"`
	Databases []Database `gorm:"foreignKey:ClusterID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE;"`

	CreatedAt time.Time
	UpdatedAt time.Time
	DeletedAt gorm.DeletedAt `gorm:"index"`
}

// ---------------------------------------------------------------------
// Cluster Repository
// ---------------------------------------------------------------------

// ClusterRepository 定义集群操作接口
type ClusterRepository interface {
	Create(ctx context.Context, c *Cluster) error
	Update(ctx context.Context, c *Cluster) error
	Delete(ctx context.Context, id string) error
	Get(ctx context.Context, id string) (*Cluster, error)
	GetWithDetails(ctx context.Context, id string) (*Cluster, error)
	List(ctx context.Context) ([]Cluster, error)
}

// clusterRepo 实现 ClusterRepository 接口
type clusterRepo struct {
	db *gorm.DB
}

// NewClusterRepository 创建一个新的集群仓库实例
func NewClusterRepository(db *gorm.DB) ClusterRepository {
	if db == nil {
		db = store.GetDB()
	}
	return &clusterRepo{db: db}
}

// Create 创建集群
func (r *clusterRepo) Create(ctx context.Context, c *Cluster) error {
	return r.db.WithContext(ctx).Create(c).Error
}

// Update 更新集群
func (r *clusterRepo) Update(ctx context.Context, c *Cluster) error {
	return r.db.WithContext(ctx).Save(c).Error
}

// Delete 删除集群
func (r *clusterRepo) Delete(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Delete(&Cluster{}, "id = ?", id).Error
}

// Get 获取集群详情
func (r *clusterRepo) Get(ctx context.Context, id string) (*Cluster, error) {
	var c Cluster
	err := r.db.WithContext(ctx).First(&c, "id = ?", id).Error
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// GetWithDetails 获取集群详情 (包含关联数据)
func (r *clusterRepo) GetWithDetails(ctx context.Context, id string) (*Cluster, error) {
	var c Cluster
	err := r.db.WithContext(ctx).
		Preload("Machines").
		Preload("Databases").
		First(&c, "id = ?", id).Error
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// List 列出所有集群
func (r *clusterRepo) List(ctx context.Context) ([]Cluster, error) {
	var list []Cluster
	err := r.db.WithContext(ctx).Find(&list).Error
	return list, err
}
