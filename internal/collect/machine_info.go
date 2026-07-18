// Package collect 提供机器信息采集相关的数据模型定义。
package collect

import "time"

// MachineInfo 定义机器的完整信息，包括硬件配置、操作系统、网络、安全和时间同步等状态。
type MachineInfo struct {
	MachineID       string             `json:"machine_id,omitempty"`
	Hostname        string             `json:"hostname"`
	IPs             []string           `json:"ips"`
	Interfaces      []NetworkInterface `json:"interfaces"`
	CPUCores        int                `json:"cpu_cores"`
	MemoryGB        int                `json:"memory_gb"`
	Arch            string             `json:"arch"`
	GlibcVersion    string             `json:"glibc_version"`
	OS              string             `json:"os"`
	DiskFreeGB      int                `json:"disk_free_gb"`
	SELinux         string             `json:"selinux"`
	Firewall        string             `json:"firewall"`
	SwapEnabled     bool               `json:"swap_enabled"`
	NTPEnabled      bool               `json:"ntp_enabled"`
	TimeOffsetMS    int64              `json:"time_offset_ms"`
	AgentTimeUnixMS int64              `json:"agent_time_unix_ms,omitempty"`
	UpdatedAt       time.Time          `json:"updated_at,omitempty"`
}

// NetworkInterface 定义网络接口信息，包含接口名称和绑定的 IP 地址列表。
type NetworkInterface struct {
	Name string   `json:"name"`
	IPs  []string `json:"ips"`
}

// StaticInfo 定义机器的静态信息汇总，包含主机信息和 MySQL 信息两部分。
type StaticInfo struct {
	MachineID       string          `json:"machine_id,omitempty"`
	Host            HostStaticInfo  `json:"host"`
	MySQL           MySQLStaticInfo `json:"mysql"`
	AgentTimeUnixMS int64           `json:"agent_time_unix_ms,omitempty"`
	CollectedAt     time.Time       `json:"collected_at,omitempty"`
	UpdatedAt       time.Time       `json:"updated_at,omitempty"`
}

// HostStaticInfo 定义主机的静态信息，包括系统状态、网络配置、SSH 访问、安全设置等。
type HostStaticInfo struct {
	MachineStatus  string             `json:"machine_status"`
	Arch           string             `json:"arch"`
	IPs            []string           `json:"ips"`
	Interfaces     []NetworkInterface `json:"interfaces"`
	SSHUser        string             `json:"ssh_user"`
	SSHAvailable   bool               `json:"ssh_available"`
	SSHPort        int                `json:"ssh_port"`
	GlibcVersion   string             `json:"glibc_version"`
	MemoryGB       int                `json:"memory_gb"`
	CPUCores       int                `json:"cpu_cores"`
	OS             string             `json:"os"`
	SwapEnabled    bool               `json:"swap_enabled"`
	NTPEnabled     bool               `json:"ntp_enabled"`
	TimeOffsetMS   int64              `json:"time_offset_ms"`
	SELinux        string             `json:"selinux"`
	Firewall       string             `json:"firewall"`
	MySQLInstalled bool               `json:"mysql_installed"`
	GMHAInstalled  bool               `json:"gmha_installed"`
}

// MySQLStaticInfo 定义 MySQL 的静态信息，包括安装状态、版本、端口、目录路径等。
type MySQLStaticInfo struct {
	Installed  bool   `json:"installed"`
	CollectOK  bool   `json:"collect_ok"`
	Error      string `json:"error,omitempty"`
	ServerID   int    `json:"server_id"`
	BaseDir    string `json:"base_dir"`
	Version    string `json:"version"`
	Port       int    `json:"port"`
	ConfigFile string `json:"config_file"`
	SlowLog    string `json:"slow_log"`
	ErrorLog   string `json:"error_log"`
	Socket     string `json:"socket"`
	DataDir    string `json:"data_dir"`
	UndoDir    string `json:"undo_dir"`
	RedoDir    string `json:"redo_dir"`
	BinlogDir  string `json:"binlog_dir"`
	TmpDir     string `json:"tmp_dir"`
}
