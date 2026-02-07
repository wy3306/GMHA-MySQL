package model

import (
	"time"
)

// Machine 代表一台物理机或虚拟机
type Machine struct {
	ID                 uint               `gorm:"primaryKey" json:"id"`
	ClusterID          uint               `json:"cluster_id"`                            // 所属集群ID 保留字段，不用于实际关联
	Hostname           string             `gorm:"uniqueIndex;not null" json:"hostname"`  // 主机名
	IP                 string             `gorm:"uniqueIndex;not null" json:"ip"`        // IP地址
	User               string             `gorm:"not null" json:"user"`                  // 用户名
	Password           string             `gorm:"not null" json:"password"`              // 密码
	SSHPort            string             `gorm:"not null" json:"ssh_port"`              // SSH端口
	Mask               string             `gorm:"not null" json:"mask"`                  // 子网掩码
	Status             string             `json:"status"`                                // 状态: Online, Offline
	Arch               string             `gorm:"not null" json:"arch"`                  // 架构
	CPU                string             `gorm:"not null" json:"cpu"`                   // CPU型号
	Mem                string             `gorm:"not null" json:"mem"`                   // 内存大小
	LinuxDistro        string             `gorm:"not null" json:"linux_distro"`          // Linux发行版
	LinuxVersion       string             `gorm:"not null" json:"linux_version"`         // Linux版本
	KernelVersion      string             `gorm:"not null" json:"kernel_version"`        // 内核版本
	DataDir            string             `gorm:"not null" json:"data_dir"`              // 数据目录
	LogDir             string             `gorm:"not null" json:"log_dir"`               // 日志目录
	InstallDir         string             `gorm:"not null" json:"install_dir"`           // 安装目录
	DataDirSize        string             `gorm:"not null" json:"data_dir_size"`         // 数据目录磁盘大小
	LogDirSize         string             `gorm:"not null" json:"log_dir_size"`          // 日志目录磁盘大小
	InstallDirSize     string             `gorm:"not null" json:"install_dir_size"`      // 安装目录磁盘大小
	DataDirUsedSize    string             `gorm:"not null" json:"data_dir_used_size"`    // 数据目录磁盘已用大小
	LogDirUsedSize     string             `gorm:"not null" json:"log_dir_used_size"`     // 日志目录磁盘已用大小
	InstallDirUsedSize string             `gorm:"not null" json:"install_dir_used_size"` // 安装目录磁盘已用大小
	NICs               []NetworkInterface `gorm:"-" json:"nics"`
	CreatedAt          time.Time          `json:"created_at"`
	UpdatedAt          time.Time          `json:"updated_at"`
}

// NewMachine 创建一个新的物理机或虚拟机
func (m Machine) NewMachine(hostname string) *Machine {
	return &Machine{
		Hostname: hostname,
		Status:   "Online",
	}
}
