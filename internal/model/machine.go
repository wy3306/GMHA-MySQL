package model

import (
	"GMHA-MySQL/internal/store"
	"context"
	"time"

	"gorm.io/gorm"
)

// Machine 机器信息
type Machine struct {
	ID        string `gorm:"primaryKey;type:varchar(64)"` // IP 或 UUID
	ClusterID string `gorm:"type:varchar(64);index"`

	IP          string `gorm:"type:varchar(64);not null"`
	SSHPort     int
	SSHUser     string `gorm:"type:varchar(64)"`
	SSHPassword string `gorm:"type:varchar(255)"`

	// 硬件信息
	CPUCores int
	Arch     string `gorm:"type:varchar(16)"` // x86/arm
	Memory   int64  // MB

	// 磁盘空间
	DataDiskTotal int64
	DataDiskFree  int64
	LogDiskTotal  int64
	LogDiskFree   int64

	// 路径配置
	DataDirPath   string `gorm:"type:varchar(255)"`
	LogDirPath    string `gorm:"type:varchar(255)"`
	DBInstallPath string `gorm:"type:varchar(255)"`

	// 状态信息
	Status    string `gorm:"type:varchar(32)"`
	NTPStatus bool

	// 网卡信息 (JSON)
	NetworkInterfaces string `gorm:"type:text"`

	CreatedAt time.Time
	UpdatedAt time.Time
}

// ---------------------------------------------------------------------
// Machine Repository
// ---------------------------------------------------------------------

// MachineRepository 定义机器操作接口
type MachineRepository interface {
	Create(ctx context.Context, m *Machine) error
	Update(ctx context.Context, m *Machine) error
	Delete(ctx context.Context, id string) error
	Get(ctx context.Context, id string) (*Machine, error)
	ListByCluster(ctx context.Context, clusterID string) ([]Machine, error)
}

type machineRepo struct {
	db *gorm.DB
}

func NewMachineRepository(db *gorm.DB) MachineRepository {
	if db == nil {
		db = store.GetDB()
	}
	return &machineRepo{db: db}
}

func (r *machineRepo) Create(ctx context.Context, m *Machine) error {
	return r.db.WithContext(ctx).Create(m).Error
}

func (r *machineRepo) Update(ctx context.Context, m *Machine) error {
	return r.db.WithContext(ctx).Save(m).Error
}

func (r *machineRepo) Delete(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Delete(&Machine{}, "id = ?", id).Error
}

func (r *machineRepo) Get(ctx context.Context, id string) (*Machine, error) {
	var m Machine
	err := r.db.WithContext(ctx).First(&m, "id = ?", id).Error
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func (r *machineRepo) ListByCluster(ctx context.Context, clusterID string) ([]Machine, error) {
	var list []Machine
	err := r.db.WithContext(ctx).Where("cluster_id = ?", clusterID).Find(&list).Error
	return list, err
}
