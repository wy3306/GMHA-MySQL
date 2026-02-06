package model

// IPAddress 代表网卡上的一个IP地址
type IPAddress struct {
	ID          uint   `gorm:"primaryKey" json:"id"`
	InterfaceID uint   `json:"interface_id"`
	Address     string `gorm:"size:64;not null" json:"address"` // IP地址, e.g. 192.168.1.10
	Netmask     string `gorm:"size:64" json:"netmask"`          // 子网掩码/前缀长度, e.g. 24 or 255.255.255.0
	Version     int    `json:"version"`                         // 4 or 6
}

// NewIPAddress 创建一个新的IP地址
func (i IPAddress) NewIPAddress(interfaceID uint, address, netmask string, version int) *IPAddress {
	return &IPAddress{
		InterfaceID: interfaceID,
		Address:     address,
		Netmask:     netmask,
		Version:     version,
	}
}
