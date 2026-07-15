package ha

import "time"

const (
	ArchitectureMasterSlave          = "master_slave"
	ArchitectureDualMaster           = "dual_master"
	ArchitectureMultiMaster          = "multi_master"
	ArchitectureKeepalivedDualMaster = "keepalived_dual_master"
)

const (
	ArchitectureRunPending      = "pending"
	ArchitectureRunRunning      = "running"
	ArchitectureRunWaitingForce = "waiting_force_confirmation"
	ArchitectureRunSucceeded    = "success"
	ArchitectureRunFailed       = "failed"
)

// ArchitectureNodeRequest 描述架构调整后单个 MySQL 实例的目标角色。
type ArchitectureNodeRequest struct {
	MachineID        string `json:"machine_id"`
	Port             int    `json:"port"`
	Role             string `json:"role"`
	SourceMachineID  string `json:"source_machine_id,omitempty"`
	DelaySeconds     int    `json:"delay_seconds,omitempty"`
	ElectionPriority int    `json:"election_priority,omitempty"`
}

// ArchitectureAdjustmentRequest 是架构调整预检与执行共用的请求。
type ArchitectureAdjustmentRequest struct {
	Architecture                string                    `json:"architecture"`
	CurrentMasterMachineID      string                    `json:"current_master_machine_id,omitempty"`
	PreferredNewMasterMachineID string                    `json:"preferred_new_master_machine_id,omitempty"`
	MoveVIP                     bool                      `json:"move_vip"`
	ForceAfterTimeout           bool                      `json:"force_after_timeout"`
	ManagementUsers             []string                  `json:"management_users,omitempty"`
	RootPassword                string                    `json:"root_password,omitempty"`
	ReplicationUser             string                    `json:"replication_user,omitempty"`
	ReplicationPassword         string                    `json:"replication_password,omitempty"`
	Nodes                       []ArchitectureNodeRequest `json:"nodes"`
}

// ArchitecturePlanStep 是 Manager 必须按顺序执行的安全步骤。
type ArchitecturePlanStep struct {
	Order                int    `json:"order"`
	Code                 string `json:"code"`
	Name                 string `json:"name"`
	Description          string `json:"description"`
	Destructive          bool   `json:"destructive"`
	RequiresConfirmation bool   `json:"requires_confirmation"`
}

// ArchitectureAdjustmentPlan 是切换前生成的只读计划和候选选举结果。
type ArchitectureAdjustmentPlan struct {
	PlanID                    string                 `json:"plan_id"`
	ClusterID                 string                 `json:"cluster_id"`
	Architecture              string                 `json:"architecture"`
	SelectedCandidate         CandidateScore         `json:"selected_candidate"`
	RankedCandidates          []CandidateScore       `json:"ranked_candidates"`
	Steps                     []ArchitecturePlanStep `json:"steps"`
	VIPRouteMode              string                 `json:"vip_route_mode,omitempty"`
	VIPAddresses              []string               `json:"vip_addresses,omitempty"`
	WaitDelayTimeoutSeconds   int                    `json:"wait_delay_timeout_seconds"`
	RequiresForceConfirmation bool                   `json:"requires_force_confirmation"`
	Executable                bool                   `json:"executable"`
	BlockingReasons           []string               `json:"blocking_reasons,omitempty"`
	Warnings                  []string               `json:"warnings,omitempty"`
	CreatedAt                 time.Time              `json:"created_at"`
}

// ArchitectureRun 保存一次在线架构调整的可审计状态。密码不会写入该结构或数据库。
type ArchitectureRun struct {
	RunID          string                        `json:"run_id"`
	ClusterID      string                        `json:"cluster_id"`
	Status         string                        `json:"status"`
	CurrentStep    string                        `json:"current_step,omitempty"`
	Plan           ArchitectureAdjustmentPlan    `json:"plan"`
	Request        ArchitectureAdjustmentRequest `json:"request"`
	StepResults    []ArchitectureRunStepResult   `json:"step_results,omitempty"`
	TaskIDs        []string                      `json:"task_ids,omitempty"`
	ForceConfirmed bool                          `json:"force_confirmed"`
	Error          string                        `json:"error,omitempty"`
	CreatedAt      time.Time                     `json:"created_at"`
	UpdatedAt      time.Time                     `json:"updated_at"`
	FinishedAt     *time.Time                    `json:"finished_at,omitempty"`
}

type ArchitectureRunStepResult struct {
	Code       string     `json:"code"`
	Name       string     `json:"name"`
	Status     string     `json:"status"`
	TaskIDs    []string   `json:"task_ids,omitempty"`
	Message    string     `json:"message,omitempty"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}
