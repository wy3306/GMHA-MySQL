// Package task 定义了任务领域的实体和仓储接口。
// 任务是 Manager 下发给 Agent 执行的工作单元，包括命令执行、信息采集、MySQL 安装/卸载、拓扑搭建等。
package task

import (
	"context"
	"encoding/json"
	"time"
)

type Type string

const (
	TypeExec               Type = "exec"
	TypeCollectMachineInfo Type = "collect_machine_info"
	TypeCollectStaticInfo  Type = "collect_static_info"
	TypeMySQLInstall       Type = "mysql_install"
	TypeMySQLUninstall     Type = "mysql_uninstall"
	TypeMySQLTopology      Type = "mysql_topology"
)

type Status string

const (
	StatusPending Status = "pending"
	StatusSent    Status = "sent"
	StatusRunning Status = "running"
	StatusSuccess Status = "success"
	StatusFailed  Status = "failed"
)

type StepStatus string

const (
	StepPending StepStatus = "pending"
	StepRunning StepStatus = "running"
	StepSuccess StepStatus = "success"
	StepFailed  StepStatus = "failed"
)

type EventType string

const (
	EventLog   EventType = "log"
	EventInfo  EventType = "info"
	EventError EventType = "error"
)

// Task 是任务实体的领域模型，表示一个需要 Agent 执行的工作单元。
type Task struct {
	ID              string
	Type            Type
	MachineID       string
	AgentID         string
	Status          Status
	ProgressPercent int
	CurrentStep     string
	SpecJSON        json.RawMessage
	CreatedAt       time.Time
	StartedAt       *time.Time
	FinishedAt      *time.Time
}

// Step 表示任务的一个执行步骤，用于跟踪任务的分步执行进度。
type Step struct {
	ID         string
	TaskID     string
	StepNo     int
	StepName   string
	Status     StepStatus
	Message    string
	StartedAt  *time.Time
	FinishedAt *time.Time
}

// Event 记录任务执行过程中的日志、信息和错误事件。
type Event struct {
	ID        string
	TaskID    string
	StepID    string
	EventType EventType
	Content   string
	CreatedAt time.Time
}

// ExecSpec 是命令执行任务的规格参数。
type ExecSpec struct {
	Command string `json:"command"`
}

type CollectMachineInfoSpec struct{}

type CollectStaticInfoSpec struct {
	MySQL MySQLStaticCollectSpec `json:"mysql"`
}

type MySQLStaticCollectSpec struct {
	Enabled  bool   `json:"enabled"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Socket   string `json:"socket"`
	Username string `json:"username"`
	Password string `json:"password"`
}

// MySQLInstallSpec 是 MySQL 安装任务的规格参数，包含端口、目录、配置、账号等所有安装信息。
type MySQLInstallSpec struct {
	Port               int                `json:"port"`
	ServerID           int                `json:"server_id"`
	MySQLUser          string             `json:"mysql_user"`
	InstanceDir        string             `json:"instance_dir"`
	DataDir            string             `json:"data_dir"`
	BinlogDir          string             `json:"binlog_dir"`
	RedoDir            string             `json:"redo_dir"`
	UndoDir            string             `json:"undo_dir"`
	TmpDir             string             `json:"tmp_dir"`
	BaseDir            string             `json:"base_dir"`
	RootPassword       string             `json:"root_password"`
	Profile            string             `json:"profile"`
	PackageName        string             `json:"package_name"`
	PackageDownloadURL string             `json:"package_download_url"`
	MyCnfPath          string             `json:"my_cnf_path"`
	MyCnfContent       string             `json:"my_cnf_content"`
	SocketPath         string             `json:"socket_path"`
	ErrorLog           string             `json:"error_log"`
	PIDFile            string             `json:"pid_file"`
	CharacterSetsDir   string             `json:"character_sets_dir"`
	PluginDir          string             `json:"plugin_dir"`
	SystemdUnitName    string             `json:"systemd_unit_name"`
	SystemdContent     string             `json:"systemd_content"`
	LimitsPath         string             `json:"limits_path"`
	LimitsContent      string             `json:"limits_content"`
	SysctlPath         string             `json:"sysctl_path"`
	SysctlContent      string             `json:"sysctl_content"`
	EnvFilePath        string             `json:"env_file_path"`
	EnvContent         string             `json:"env_content"`
	Accounts           []MySQLAccountSpec `json:"accounts"`
}

type MySQLAccountSpec struct {
	Role           string `json:"role"`
	Username       string `json:"username"`
	Password       string `json:"password"`
	Host           string `json:"host"`
	Enabled        bool   `json:"enabled"`
	ExtendedBackup bool   `json:"extended_backup,omitempty"`
}

type MySQLInstallResult struct {
	Port        int                     `json:"port"`
	ServerID    int                     `json:"server_id"`
	MySQLUser   string                  `json:"mysql_user"`
	InstanceDir string                  `json:"instance_dir"`
	DataDir     string                  `json:"data_dir"`
	BinlogDir   string                  `json:"binlog_dir"`
	RedoDir     string                  `json:"redo_dir"`
	UndoDir     string                  `json:"undo_dir"`
	TmpDir      string                  `json:"tmp_dir"`
	BaseDir     string                  `json:"base_dir"`
	Profile     string                  `json:"profile"`
	PackageName string                  `json:"package_name"`
	SystemdUnit string                  `json:"systemd_unit"`
	MyCnfPath   string                  `json:"my_cnf_path"`
	SocketPath  string                  `json:"socket_path"`
	AccountInit *MySQLAccountInitResult `json:"account_init,omitempty"`
}

type MySQLAccountInitResult struct {
	Enabled        bool                         `json:"enabled"`
	Success        bool                         `json:"success"`
	PartialSuccess bool                         `json:"partial_success"`
	Retryable      bool                         `json:"retryable"`
	Summary        string                       `json:"summary"`
	Items          []MySQLAccountInitItemResult `json:"items"`
}

type MySQLAccountInitItemResult struct {
	Role            string   `json:"role"`
	Username        string   `json:"username"`
	Host            string   `json:"host"`
	Enabled         bool     `json:"enabled"`
	Skipped         bool     `json:"skipped"`
	UserCreated     bool     `json:"user_created"`
	PasswordUpdated bool     `json:"password_updated"`
	Granted         bool     `json:"granted"`
	Success         bool     `json:"success"`
	Retryable       bool     `json:"retryable"`
	Error           string   `json:"error"`
	ExecutedSteps   []string `json:"executed_steps"`
}

// MySQLUninstallSpec 是 MySQL 卸载任务的规格参数。
type MySQLUninstallSpec struct {
	Port            int      `json:"port"`
	MySQLUser       string   `json:"mysql_user"`
	InstanceDir     string   `json:"instance_dir"`
	DataDir         string   `json:"data_dir"`
	BinlogDir       string   `json:"binlog_dir"`
	RedoDir         string   `json:"redo_dir"`
	UndoDir         string   `json:"undo_dir"`
	TmpDir          string   `json:"tmp_dir"`
	BaseDir         string   `json:"base_dir"`
	PackageName     string   `json:"package_name"`
	SystemdUnitName string   `json:"systemd_unit_name"`
	MyCnfPath       string   `json:"my_cnf_path"`
	SocketPath      string   `json:"socket_path"`
	ExtraPaths      []string `json:"extra_paths"`
}

type MySQLUninstallResult struct {
	Port        int    `json:"port"`
	InstanceDir string `json:"instance_dir"`
	BaseDir     string `json:"base_dir"`
	SystemdUnit string `json:"systemd_unit"`
}

// MySQLTopologySpec 是 MySQL 拓扑搭建任务的规格参数，包含主从复制配置。
type MySQLTopologySpec struct {
	Topology            string                  `json:"topology"`
	Port                int                     `json:"port"`
	RootPassword        string                  `json:"root_password"`
	ReplicationUser     string                  `json:"replication_user"`
	ReplicationPassword string                  `json:"replication_password"`
	CloneUser           string                  `json:"clone_user"`
	ClonePassword       string                  `json:"clone_password"`
	UseClone            bool                    `json:"use_clone"`
	PrimaryMachine      string                  `json:"primary_machine,omitempty"`
	CloneSeedMachine    string                  `json:"clone_seed_machine,omitempty"`
	ParallelType        string                  `json:"parallel_type"`
	ParallelWorkers     int                     `json:"parallel_workers"`
	Node                MySQLTopologyNodeSpec   `json:"node"`
	Nodes               []MySQLTopologyNodeSpec `json:"nodes"`
}

type MySQLTopologyNodeSpec struct {
	MachineID                string `json:"machine_id"`
	MachineName              string `json:"machine_name"`
	IP                       string `json:"ip"`
	Port                     int    `json:"port"`
	Role                     string `json:"role"`
	ServerID                 int    `json:"server_id"`
	AutoIncrementOffset      int    `json:"auto_increment_offset"`
	AutoIncrementIncrement   int    `json:"auto_increment_increment"`
	SourceMachineID          string `json:"source_machine_id,omitempty"`
	SourceMachineName        string `json:"source_machine_name,omitempty"`
	SourceIP                 string `json:"source_ip,omitempty"`
	SourcePort               int    `json:"source_port,omitempty"`
	InstanceDir              string `json:"instance_dir"`
	DataDir                  string `json:"data_dir"`
	BaseDir                  string `json:"base_dir"`
	MySQLUser                string `json:"mysql_user"`
	MyCnfPath                string `json:"my_cnf_path"`
	SocketPath               string `json:"socket_path"`
	SystemdUnitName          string `json:"systemd_unit_name"`
	ResetServerUUID          bool   `json:"reset_server_uuid"`
	ReadOnly                 bool   `json:"read_only"`
	SuperReadOnly            bool   `json:"super_read_only"`
	RequiresReplicationSetup bool   `json:"requires_replication_setup"`
	RequiresClone            bool   `json:"requires_clone"`
}

type MySQLTopologyResult struct {
	Topology string                `json:"topology"`
	Port     int                   `json:"port"`
	Node     MySQLTopologyNodeSpec `json:"node"`
}

// DispatchEnvelope 是任务分发的消息信封，用于通过 WebSocket 推送任务给 Agent。
type DispatchEnvelope struct {
	Kind string       `json:"kind"`
	Task DispatchTask `json:"task"`
}

type DispatchTask struct {
	ID        string          `json:"id"`
	Type      string          `json:"type"`
	MachineID string          `json:"machine_id"`
	AgentID   string          `json:"agent_id"`
	Spec      json.RawMessage `json:"spec"`
	Steps     []DispatchStep  `json:"steps"`
}

type DispatchStep struct {
	ID       string `json:"id"`
	StepNo   int    `json:"step_no"`
	StepName string `json:"step_name"`
}

// ReportEnvelope 是任务进度上报的消息信封，Agent 通过 HTTP 回报任务执行状态。
type ReportEnvelope struct {
	Kind        string          `json:"kind"`
	AgentID     string          `json:"agent_id"`
	MachineID   string          `json:"machine_id"`
	TaskID      string          `json:"task_id"`
	Status      Status          `json:"status"`
	Progress    int             `json:"progress_percent"`
	CurrentStep string          `json:"current_step"`
	Step        *StepReport     `json:"step,omitempty"`
	Event       *Event          `json:"event,omitempty"`
	Result      json.RawMessage `json:"result,omitempty"`
	Error       string          `json:"error,omitempty"`
}

type StepReport struct {
	StepID     string     `json:"step_id"`
	StepNo     int        `json:"step_no"`
	StepName   string     `json:"step_name"`
	Status     StepStatus `json:"status"`
	Message    string     `json:"message"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

// Repository 定义了任务领域的仓储接口。
type Repository interface {
	CreateTask(ctx context.Context, task Task, steps []Step, events []Event) error
	GetTask(ctx context.Context, taskID string) (Task, bool, error)
	ListTasks(ctx context.Context, limit int) ([]Task, error)
	ListTasksByStatus(ctx context.Context, status Status, limit int) ([]Task, error)
	ListSteps(ctx context.Context, taskID string) ([]Step, error)
	ListEvents(ctx context.Context, taskID string, limit int) ([]Event, error)
	UpdateTask(ctx context.Context, task Task) error
	UpdateStep(ctx context.Context, step Step) error
	AppendEvent(ctx context.Context, event Event) error
}
