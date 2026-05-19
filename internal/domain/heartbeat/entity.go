// Package heartbeat 定义了心跳领域的实体、值对象和仓储接口。
// 心跳是 Agent 与 Manager 之间保持连接的核心机制，通过 gRPC 双向流实现。
// Manager 通过心跳状态管理实现 Agent 存活检测、状态转换和自动恢复。
package heartbeat

import (
	"context"
	"time"

	dynamicdomain "gmha/internal/domain/dynamic"
)

// AgentState 表示 Agent 在心跳系统中的状态。
type AgentState string

const (
	StateInit     AgentState = "INIT"      // 初始状态，Agent 刚注册
	StateOnline   AgentState = "ONLINE"    // 在线，心跳正常
	StateSuspect  AgentState = "SUSPECT"   // 疑似离线，心跳超时但未确认
	StateDegraded AgentState = "DEGRADED"  // 降级状态，健康检查异常
	StateOffline  AgentState = "OFFLINE"   // 离线，确认无心跳
)

// HealthLevel 表示 Agent 的整体健康等级。
type HealthLevel string

const (
	HealthHealthy   HealthLevel = "HEALTHY"   // 健康，所有检查通过
	HealthDegraded  HealthLevel = "DEGRADED"  // 降级，部分检查异常
	HealthUnhealthy HealthLevel = "UNHEALTHY" // 不健康，多项检查失败
)

// CheckStatus 表示单项健康检查的状态。
type CheckStatus string

const (
	CheckOK      CheckStatus = "OK"      // 检查通过
	CheckWarn    CheckStatus = "WARN"    // 检查告警
	CheckFail    CheckStatus = "FAIL"    // 检查失败
	CheckUnknown CheckStatus = "UNKNOWN" // 检查结果未知
)

// HealthCheck 表示单项健康检查的结果。
type HealthCheck struct {
	Name      string      `json:"name"`
	Status    CheckStatus `json:"status"`
	Detail    string      `json:"detail"`
	CheckedAt time.Time   `json:"checked_at"`
}

// HeartbeatPayload 是 Agent 发送给 Manager 的心跳载荷，包含身份信息、运行时状态、
// 健康检查结果和动态采集的指标数据。
type HeartbeatPayload struct {
	AgentID             string
	MachineID           string
	ClusterID           string
	Hostname            string
	Version             string
	BootID              string
	StreamID            string
	Seq                 uint64
	SentAt              time.Time
	UptimeSec           uint64
	HeartbeatIntervalMs uint32
	OverallHealth       HealthLevel
	Summary             string
	Checks              []HealthCheck
	Metrics             []dynamicdomain.MetricResult
}

// LatestStatus 是 Manager 端维护的 Agent 最新状态快照，包含当前状态、
// 最近心跳时间、连续丢失次数等信息，用于状态判断和故障检测。
type LatestStatus struct {
	AgentID              string
	MachineID            string
	ClusterID            string
	Hostname             string
	Version              string
	CurrentState         AgentState
	OverallHealth        HealthLevel
	LastHeartbeatAt      time.Time
	LastHealthyAt        *time.Time
	LastStateChangeAt    time.Time
	LastSeq              uint64
	LastBootID           string
	ConsecutiveMisses    int
	ConsecutiveBadChecks int
	LastErrorSummary     string
	Checks               []HealthCheck
	Metrics              []dynamicdomain.MetricResult
	UpdatedAt            time.Time
}

// StateEvent 记录 Agent 状态变更事件，用于审计和故障追溯。
type StateEvent struct {
	ID           string
	AgentID      string
	MachineID    string
	EventType    string
	PrevState    AgentState
	NewState     AgentState
	Reason       string
	HeartbeatSeq uint64
	PayloadJSON  string
	CreatedAt    time.Time
}

// Repository 定义了心跳领域的仓储接口，用于持久化心跳状态和事件。
type Repository interface {
	UpsertLatestStatus(ctx context.Context, item LatestStatus) error
	AppendEvent(ctx context.Context, item StateEvent) error
	ListLatest(ctx context.Context) ([]LatestStatus, error)
	DeleteLatestByMachineID(ctx context.Context, machineID string) error
}
