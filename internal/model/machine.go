package model

import (
	"time"
)

// Machine 代表一台物理机或虚拟机
type Machine struct {
	ID        uint               `gorm:"primaryKey" json:"id"`
	ClusterID uint               `json:"cluster_id"`                           // 所属集群ID
	Hostname  string             `gorm:"uniqueIndex;not null" json:"hostname"` // 主机名
	Status    string             `json:"status"`                               // 状态: Online, Offline
	NICs      []NetworkInterface `gorm:"-" json:"nics"`
	CreatedAt time.Time          `json:"created_at"`
	UpdatedAt time.Time          `json:"updated_at"`
}

// NewMachine 创建一个新的物理机或虚拟机
func (m Machine) NewMachine(hostname string) *Machine {
	return &Machine{
		Hostname: hostname,
		Status:   "Online",
	}
}
