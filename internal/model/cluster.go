package model

import (
	"time"
)

// Cluster 代表一个 MySQL 集群
type Cluster struct {
	ID              uint      `gorm:"primaryKey" json:"id"`
	Name            string    `gorm:"uniqueIndex;not null" json:"name"` // 集群名称
	Status          string    `json:"status"`                           // 状态: Online, Offline
	Description     string    `gorm:"size:256" json:"description"`      //集群描述
	Machines        []Machine `gorm:"foreignKey:ClusterID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE" json:"machines"`
	ClusterIP       string    `gorm:"size:64" json:"cluster_ip"`              // 集群IP地址
	VIP             string    `gorm:"size:64" json:"vip"`                     // 虚拟IP地址
	VIPStatus       string    `json:"vip_status"`                             // VIP状态: Allocated, Unallocated
	SSHTrust        bool      `gorm:"default:false" json:"ssh_trust"`         // 是否支持SSH互信
	HasLayer3Switch bool      `gorm:"default:false" json:"has_layer3_switch"` // 是否存在三层交换机
	ManagerPort     string    `gorm:"index" json:"manager_port"`              // 管理端口
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// NewCluster 创建一个新的集群
func (c Cluster) NewCluster(name, description string) *Cluster {
	return &Cluster{
		Name:        name,
		Status:      "Online",
		Description: description,
		VIPStatus:   "Unallocated",
	}
}

// 修改VIP
func (c *Cluster) UpdateVIP(vip string) *Cluster {
	c.VIP = vip
	c.VIPStatus = "Allocated"
	return c
}

// 修改集群状态
func (c *Cluster) UpdateStatus(status string) *Cluster {
	c.Status = status
	return c
}

// 修改集群描述
func (c *Cluster) UpdateDescription(description string) *Cluster {
	c.Description = description
	return c
}
