package model

type Agent struct {
	ID        uint   `gorm:"primaryKey" json:"id"`
	MachineID uint   `gorm:"index;not null" json:"machine_id"`
	Name      string `gorm:"size:64;not null" json:"name"`          // 代理名称, e.g. gmha-agent-1
	IPAddress string `gorm:"size:64;not null" json:"ip_address"`    // 代理IP地址
	Port      int    `gorm:"default:9990" json:"port"`              // 代理端口, 默认9990
	Status    string `gorm:"size:64;default:running" json:"status"` // 代理状态, 默认running
}

// NewAgent 创建一个新的代理
func (a Agent) NewAgent(machineID uint, name, ipAddress string, port int) *Agent {
	return &Agent{
		MachineID: machineID,
		Name:      name,
		IPAddress: ipAddress,
		Port:      port,
	}
}
