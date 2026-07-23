// instance.go 定义 MySQL 实例的数据模型和状态常量。
package mysql

import "time"

// MySQL 实例状态常量，用于标识实例的运行状态。
const (
	// StatusRunning 表示实例正在运行。
	StatusRunning = "running"
	// StatusHeartbeatFailed 表示实例心跳检测失败。
	StatusHeartbeatFailed = "heartbeat_failed"
	// StatusInstanceError 表示实例发生错误。
	StatusInstanceError = "instance_error"
	// StatusStopped 表示实例已由管理端安全关闭。
	StatusStopped = "stopped"
)

// Instance 定义 MySQL 实例的完整信息，包括机器 ID、端口、目录路径、状态等。
type Instance struct {
	MachineID    string    `json:"machine_id"`
	Port         int       `json:"port"`
	ServerID     int       `json:"server_id"`
	MySQLUser    string    `json:"mysql_user"`
	InstanceDir  string    `json:"instance_dir"`
	DataDir      string    `json:"data_dir"`
	BinlogDir    string    `json:"binlog_dir"`
	RedoDir      string    `json:"redo_dir"`
	UndoDir      string    `json:"undo_dir"`
	TmpDir       string    `json:"tmp_dir"`
	BaseDir      string    `json:"base_dir"`
	Profile      string    `json:"profile"`
	PackageName  string    `json:"package_name"`
	Version      string    `json:"version"`
	Architecture string    `json:"architecture"`
	SystemdUnit  string    `json:"systemd_unit"`
	MyCnfPath    string    `json:"my_cnf_path"`
	SocketPath   string    `json:"socket_path"`
	Status       string    `json:"status"`
	LastTaskID   string    `json:"last_task_id"`
	UpdatedAt    time.Time `json:"updated_at"`
}
