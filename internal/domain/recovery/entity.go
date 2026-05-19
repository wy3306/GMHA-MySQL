// Package recovery 定义了自动恢复领域的实体和仓储接口。
// 当 Agent 离线时，系统会自动尝试通过 SSH 启动或重启 Agent 服务，并等待心跳恢复。
package recovery

import (
	"context"
	"time"
)

type Status string

const (
	StatusPending          Status = "pending"
	StatusConfirming       Status = "confirming"
	StatusExecuting        Status = "executing"
	StatusWaitingHeartbeat Status = "waiting_heartbeat"
	StatusSucceeded        Status = "succeeded"
	StatusFailed           Status = "failed"
	StatusSuppressed       Status = "suppressed"
)

type Trigger string

const (
	TriggerOfflineAuto Trigger = "offline_auto"
	TriggerManual      Trigger = "manual"
)

type Action string

const (
	ActionNone    Action = "none"
	ActionStart   Action = "start"
	ActionRestart Action = "restart"
)

// Task 表示一次自动恢复任务，记录恢复过程的状态和结果。
type Task struct {
	ID                string
	AgentID           string
	MachineID         string
	MachineIP         string
	Status            Status
	Trigger           Trigger
	Action            Action
	Reason            string
	Attempt           int
	MaxAttempts       int
	HeartbeatDeadline *time.Time
	LastError         string
	LastSSHOutput     string
	SuppressedUntil   *time.Time
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// LatestState 记录机器的最新恢复状态，用于冷却抑制和恢复进度跟踪。
type LatestState struct {
	MachineID           string
	InProgress          bool
	LastAttemptAt       *time.Time
	LastSuccessAt       *time.Time
	ConsecutiveFailures int
	SuppressedUntil     *time.Time
	LastTaskID          string
	LastResult          string
	UpdatedAt           time.Time
}

// Repository 定义了恢复领域的仓储接口。
type Repository interface {
	CreateTask(ctx context.Context, task Task) (Task, error)
	UpdateTask(ctx context.Context, task Task) error
	ListRecent(ctx context.Context, limit int) ([]Task, error)
	GetLatestState(ctx context.Context, machineID string) (LatestState, bool, error)
	ListLatestStates(ctx context.Context) ([]LatestState, error)
	SaveLatestState(ctx context.Context, state LatestState) error
	TryAcquireLock(ctx context.Context, machineID string, lockUntil time.Time) (bool, error)
	ReleaseLock(ctx context.Context, machineID string) error
}
