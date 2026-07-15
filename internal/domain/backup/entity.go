package backup

import (
	"context"
	"time"
)

const (
	ScheduleWeekly  = "weekly"
	ScheduleCustom  = "custom"
	ScheduleOnce    = "once"
	TypeFull        = "full"
	TypeIncremental = "incremental"
	RunPending      = "pending"
	RunRunning      = "running"
	RunSuccess      = "success"
	RunFailed       = "failed"
)

// Policy 是集群 MySQL 物理备份策略。StartAt 同时表示首次发起时间；
// weekly 策略使用它的时分秒，custom 策略以它作为间隔计算基准。
type Policy struct {
	ID                   string            `json:"id"`
	Name                 string            `json:"name"`
	Cluster              string            `json:"cluster"`
	MachineID            string            `json:"machine_id"`
	Port                 int               `json:"port"`
	BackupType           string            `json:"backup_type"`
	DiskUsageThreshold   int               `json:"disk_usage_threshold"`
	ScheduleType         string            `json:"schedule_type"`
	Weekdays             []int             `json:"weekdays"`
	WeekdayBackupTypes   map[string]string `json:"weekday_backup_types,omitempty"`
	IntervalMinutes      int               `json:"interval_minutes"`
	StartAt              time.Time         `json:"start_at"`
	RetryCount           int               `json:"retry_count"`
	RetryIntervalSeconds int               `json:"retry_interval_seconds"`
	IncludeBinlog        bool              `json:"include_binlog"`
	BackupLocation       string            `json:"backup_location"`
	MySQLUser            string            `json:"mysql_user"`
	MySQLPassword        string            `json:"-"`
	Enabled              bool              `json:"enabled"`
	LastRunAt            time.Time         `json:"last_run_at,omitempty"`
	NextRunAt            time.Time         `json:"next_run_at,omitempty"`
	CreatedAt            time.Time         `json:"created_at"`
	UpdatedAt            time.Time         `json:"updated_at"`
}

type Run struct {
	ID            string    `json:"id"`
	PolicyID      string    `json:"policy_id"`
	Cluster       string    `json:"cluster"`
	MachineID     string    `json:"machine_id"`
	MachineName   string    `json:"machine_name,omitempty"`
	MachineIP     string    `json:"machine_ip,omitempty"`
	Port          int       `json:"port"`
	BackupType    string    `json:"backup_type"`
	BaseRunID     string    `json:"base_run_id,omitempty"`
	BackupPath    string    `json:"backup_path"`
	TaskID        string    `json:"task_id"`
	Status        string    `json:"status"`
	IncludeBinlog bool      `json:"include_binlog"`
	RestoreTaskID string    `json:"restore_task_id,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	Logs          []Log     `json:"logs,omitempty"`
	LastError     string    `json:"last_error,omitempty"`
}

type Log struct {
	Time    time.Time `json:"time"`
	Level   string    `json:"level"`
	Message string    `json:"message"`
}

type Repository interface {
	SavePolicy(context.Context, Policy) error
	GetPolicy(context.Context, string) (Policy, bool, error)
	ListPolicies(context.Context, string) ([]Policy, error)
	ListDuePolicies(context.Context, time.Time) ([]Policy, error)
	UpdatePolicySchedule(context.Context, string, time.Time, time.Time, bool) error
	DeletePolicy(context.Context, string) error
	SaveRun(context.Context, Run) error
	GetRun(context.Context, string) (Run, bool, error)
	ListRuns(context.Context, string, int) ([]Run, error)
	SetRestoreTask(context.Context, string, string) error
}
