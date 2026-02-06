package model

// NetworkInterface 代表机器上的一个网卡
type NetworkInterface struct {
	ID         uint        `gorm:"primaryKey" json:"id"`
	MachineID  uint        `json:"machine_id"`
	Name       string      `gorm:"size:64;not null" json:"name"` // 网卡名称, e.g. eth0
	MacAddress string      `gorm:"size:64" json:"mac_address"`   // MAC地址
	IPs        []IPAddress `gorm:"-" json:"ips"`
}

// NewNetworkInterface 创建一个新的网卡
func (n NetworkInterface) NewNetworkInterface(machineID uint, name, macAddress string) *NetworkInterface {
	return &NetworkInterface{
		MachineID:  machineID,
		Name:       name,
		MacAddress: macAddress,
	}
}

// 添加IP地址
func (n *NetworkInterface) AddIP(ip *IPAddress) {
	n.IPs = append(n.IPs, *ip)
}

// 删除IP地址
func (n *NetworkInterface) RemoveIP(ip *IPAddress) {
	for i, ipAddr := range n.IPs {
		if ipAddr.Address == ip.Address {
			n.IPs = append(n.IPs[:i], n.IPs[i+1:]...)
			return
		}
	}
}
