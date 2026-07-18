package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	collectdomain "gmha/internal/collect"
	hadomain "gmha/internal/domain/ha"
	machinedomain "gmha/internal/domain/machine"
	mysqlapp "gmha/internal/mysql"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"time"

	taskdomain "gmha/internal/domain/task"
	taskusecase "gmha/internal/usecase/task"
)

const mysqlDefaultsFilePlaceholder = "__GMHA_MYSQL_DEFAULTS_FILE__"

// TaskConnection 定义了 Agent 任务连接接口，用于向 Agent 发送任务下发消息。
type TaskConnection interface {
	Send(taskdomain.DispatchEnvelope) error
}

// TaskService 是任务管理服务，负责任务的创建、下发、状态跟踪、报告处理，
// 以及批量 MySQL 安装/卸载等集群级操作。通过 WebSocket 与 Agent 通信。
type TaskService struct {
	repo           taskdomain.Repository
	createExec     *taskusecase.CreateExecTaskUsecase
	createCollect  *taskusecase.CreateCollectMachineInfoUsecase
	createStatic   *taskusecase.CreateCollectStaticInfoUsecase
	createMySQL    *taskusecase.CreateMySQLInstallTaskUsecase
	uninstallMySQL *taskusecase.CreateMySQLUninstallTaskUsecase
	createTopology *taskusecase.CreateMySQLTopologyTaskUsecase
	machineInfo    MachineInfoSaver
	staticInfo     StaticInfoSaver
	machines       TaskMachineRepository
	mysqlInstance  MySQLInstanceSaver
	mu             sync.RWMutex
	agents         map[string]TaskConnection
	agentCaps      map[string]map[string]bool
	agentMachines  map[string]string
	machineAgents  map[string]string
}

// MachineInfoSaver 定义了机器信息保存接口。
type MachineInfoSaver interface {
	Save(ctx context.Context, item collectdomain.MachineInfo) error
	Get(ctx context.Context, machineID string) (collectdomain.MachineInfo, bool, error)
}

// StaticInfoSaver 定义了静态信息保存和查询接口。
type StaticInfoSaver interface {
	Save(ctx context.Context, item collectdomain.StaticInfo) error
	Get(ctx context.Context, machineID string) (collectdomain.StaticInfo, bool, error)
}

// TaskMachineRepository 定义了任务服务所需的机器查询接口。
type TaskMachineRepository interface {
	GetByID(ctx context.Context, machineID string) (machinedomain.Machine, bool, error)
	List(ctx context.Context) ([]machinedomain.Machine, error)
}

// MySQLInstanceSaver 定义了 MySQL 实例的完整 CRUD 接口。
type MySQLInstanceSaver interface {
	Save(ctx context.Context, item mysqlapp.Instance) error
	List(ctx context.Context) ([]mysqlapp.Instance, error)
	Delete(ctx context.Context, machineID string, port int) error
	UpdateStatus(ctx context.Context, machineID string, port int, status string) error
	Get(ctx context.Context, machineID string, port int) (mysqlapp.Instance, bool, error)
}

// TaskDetail 是任务的完整详情，包含任务本身、步骤和事件列表。
type TaskDetail struct {
	Task         taskdomain.Task    `json:"task"`
	MachineName  string             `json:"machine_name,omitempty"`
	MachineIP    string             `json:"machine_ip,omitempty"`
	Steps        []taskdomain.Step  `json:"steps"`
	Events       []taskdomain.Event `json:"events"`
	Children     []taskdomain.Task  `json:"children,omitempty"`
	ChildDetails []TaskDetail       `json:"child_details,omitempty"`
}

type TaskListQuery = taskdomain.ListQuery

type TaskListPage struct {
	Items []taskdomain.Task `json:"items"`
	Total int               `json:"total"`
	Page  int               `json:"page"`
	Size  int               `json:"page_size"`
}

type DeleteTasksRequest struct {
	TaskIDs     []string
	AllFiltered bool
	Query       TaskListQuery
}

type DeleteTaskResult struct {
	TaskID string `json:"task_id"`
	Error  string `json:"error,omitempty"`
}

type DeleteTasksResult struct {
	Requested int                `json:"requested"`
	Deleted   int                `json:"deleted"`
	Failed    int                `json:"failed"`
	Items     []DeleteTaskResult `json:"items"`
}

type taskPageRepository interface {
	ListTaskPage(ctx context.Context, query TaskListQuery) ([]taskdomain.Task, int, error)
}

type childTaskRepository interface {
	ListChildTasks(ctx context.Context, parentTaskID string) ([]taskdomain.Task, error)
}

type taskHierarchyRepository interface {
	ListChildTasks(ctx context.Context, parentTaskID string) ([]taskdomain.Task, error)
	AssignParentTasks(ctx context.Context, parentTaskID string, childTaskIDs []string) error
	DeleteTaskTree(ctx context.Context, taskID string) error
}

// ClusterMySQLInstallRequest 是集群级 MySQL 批量安装请求。
type ClusterMySQLInstallRequest struct {
	Cluster           string
	Port              int
	ServerIDStart     int
	MySQLUser         string
	InstanceDir       string
	DataDir           string
	BinlogDir         string
	RedoDir           string
	UndoDir           string
	TmpDir            string
	BaseDir           string
	MyCnfPath         string
	SocketPath        string
	ErrorLog          string
	PIDFile           string
	CharacterSetsDir  string
	PluginDir         string
	RootPassword      string
	Profile           string
	Version           string
	Architecture      string
	InstallPTTools    bool
	InstallXtraBackup bool
	MemoryAllocator   string
	RuntimeParameters map[string]string
	Accounts          []taskdomain.MySQLAccountSpec
}

// ClusterMySQLInstallItem 是集群 MySQL 安装的单台机器结果。
type ClusterMySQLInstallItem struct {
	MachineID string     `json:"machine_id"`
	Name      string     `json:"name"`
	IP        string     `json:"ip"`
	Task      TaskDetail `json:"task,omitempty"`
	Error     string     `json:"error,omitempty"`
}

// ClusterMySQLInstallResult 是集群 MySQL 批量安装的总结果。
type ClusterMySQLInstallResult struct {
	Cluster string                    `json:"cluster"`
	Parent  TaskDetail                `json:"parent"`
	Created int                       `json:"created"`
	Failed  int                       `json:"failed"`
	Items   []ClusterMySQLInstallItem `json:"items"`
}

// ClusterMySQLUninstallRequest 是集群级 MySQL 批量卸载请求。
type ClusterMySQLUninstallRequest struct {
	Cluster string
	Port    int
}

// ClusterMySQLUninstallItem 是集群 MySQL 卸载的单台机器结果。
type ClusterMySQLUninstallItem struct {
	MachineID string     `json:"machine_id"`
	Name      string     `json:"name"`
	IP        string     `json:"ip"`
	Task      TaskDetail `json:"task,omitempty"`
	Error     string     `json:"error,omitempty"`
}

// ClusterMySQLUninstallResult 是集群 MySQL 批量卸载的总结果。
type ClusterMySQLUninstallResult struct {
	Cluster string                      `json:"cluster"`
	Parent  TaskDetail                  `json:"parent"`
	Created int                         `json:"created"`
	Failed  int                         `json:"failed"`
	Items   []ClusterMySQLUninstallItem `json:"items"`
}

// MySQLTopologyTaskResult 是 MySQL 拓扑检查任务的创建结果。
type MySQLTopologyTaskResult struct {
	Created int          `json:"created"`
	Parent  TaskDetail   `json:"parent"`
	Tasks   []TaskDetail `json:"tasks"`
}

type MySQLUpgradeRequest struct {
	Machine          string
	Port             int
	PackageName      string
	PrecheckTaskID   string
	Force            bool
	RiskAcknowledged bool
}

type MySQLUpgradePlan struct {
	CurrentVersion string     `json:"current_version"`
	TargetVersion  string     `json:"target_version"`
	CurrentPackage string     `json:"current_package"`
	TargetPackage  string     `json:"target_package"`
	PrecheckTaskID string     `json:"precheck_task_id,omitempty"`
	Forced         bool       `json:"forced"`
	Task           TaskDetail `json:"task"`
}

type MySQLUpgradePrecheckPlan struct {
	CurrentVersion string     `json:"current_version"`
	TargetVersion  string     `json:"target_version"`
	CurrentPackage string     `json:"current_package"`
	TargetPackage  string     `json:"target_package"`
	Checker        string     `json:"checker"`
	Task           TaskDetail `json:"task"`
}

// ExecTaskOptions 为复用 exec 协议的业务任务补充可展示的操作元数据。
// Agent 仍按 exec 执行，任务中心则可据此展示真实的数据库操作名称与步骤。
type ExecTaskOptions struct {
	ParentTaskID    string
	Operation       string
	DisplayName     string
	StepName        string
	Port            int
	Commands        []taskdomain.ExecCommandStep
	RollbackCommand string
	PackageName     string
	TaskType        taskdomain.Type
}

// CreateBatchTrackingTask creates one business-level parent for a fan-out
// operation. Agent execution tasks must be attached through AttachChildTasks.
func (s *TaskService) CreateBatchTrackingTask(ctx context.Context, operation, displayName, target string) (TaskDetail, error) {
	if s.repo == nil {
		return TaskDetail{}, errors.New("task repository is not configured")
	}
	now := time.Now().UTC()
	taskID := fmt.Sprintf("batch-task-%d", now.UnixNano())
	spec, err := json.Marshal(map[string]any{"operation": strings.TrimSpace(operation), "display_name": strings.TrimSpace(displayName), "target": strings.TrimSpace(target), "creation_failures": 0})
	if err != nil {
		return TaskDetail{}, err
	}
	task := taskdomain.Task{ID: taskID, Type: taskdomain.TypeBatchOperation, MachineID: strings.TrimSpace(target), AgentID: "manager", Status: taskdomain.StatusPending, CurrentStep: "create_children", SpecJSON: spec, CreatedAt: now}
	steps := []taskdomain.Step{{ID: taskID + "-children", TaskID: taskID, StepNo: 1, StepName: "create_children", Status: taskdomain.StepRunning, Message: "正在创建 Agent 执行子任务。", StartedAt: &now}}
	events := []taskdomain.Event{{ID: taskID + "-created", TaskID: taskID, StepID: steps[0].ID, EventType: taskdomain.EventInfo, Content: "批量父任务已创建。", CreatedAt: now}}
	if err := s.repo.CreateTask(ctx, task, steps, events); err != nil {
		return TaskDetail{}, err
	}
	return s.GetTaskDetail(ctx, taskID)
}

// FinalizeBatchTrackingTask records fan-out creation results. Runtime status
// and progress are subsequently aggregated from the attached child tasks.
func (s *TaskService) FinalizeBatchTrackingTask(ctx context.Context, parentTaskID string, created, failed int) error {
	task, ok, err := s.repo.GetTask(ctx, strings.TrimSpace(parentTaskID))
	if err != nil || !ok {
		if err != nil {
			return err
		}
		return errors.New("batch parent task not found")
	}
	var spec map[string]any
	_ = json.Unmarshal(task.SpecJSON, &spec)
	if spec == nil {
		spec = map[string]any{}
	}
	spec["targets"] = created + failed
	spec["created"] = created
	spec["creation_failures"] = failed
	task.SpecJSON, _ = json.Marshal(spec)
	now := time.Now().UTC()
	steps, err := s.repo.ListSteps(ctx, task.ID)
	if err != nil {
		return err
	}
	if len(steps) > 0 {
		steps[0].Message = fmt.Sprintf("已创建 %d 个执行子任务，%d 个目标创建失败。", created, failed)
		steps[0].FinishedAt = &now
		if created == 0 && failed > 0 {
			steps[0].Status = taskdomain.StepFailed
		} else {
			steps[0].Status = taskdomain.StepSuccess
		}
		if err := s.repo.UpdateStep(ctx, steps[0]); err != nil {
			return err
		}
	}
	if created == 0 {
		task.ProgressPercent = 100
		task.FinishedAt = &now
		if failed > 0 {
			task.Status, task.CurrentStep = taskdomain.StatusFailed, "create_children"
		} else {
			task.Status, task.CurrentStep = taskdomain.StatusSuccess, "completed"
		}
	} else {
		task.Status, task.CurrentStep = taskdomain.StatusRunning, "waiting_children"
		if task.StartedAt == nil {
			task.StartedAt = &now
		}
	}
	if err := s.repo.UpdateTask(ctx, task); err != nil {
		return err
	}
	return s.syncParentTask(ctx, task.ID)
}

// AttachChildTasks establishes the only supported parent-child relation.
func (s *TaskService) AttachChildTasks(ctx context.Context, parentTaskID string, childTaskIDs []string) error {
	repo, ok := s.repo.(taskHierarchyRepository)
	if !ok {
		return errors.New("task hierarchy repository is not configured")
	}
	if err := repo.AssignParentTasks(ctx, strings.TrimSpace(parentTaskID), childTaskIDs); err != nil {
		return err
	}
	return s.syncParentTask(ctx, parentTaskID)
}

// aggregateParentTask derives the business task state from its direct Agent
// execution children. The stored parent is kept as an audit anchor; callers
// always receive a current aggregate even when an Agent report just arrived.
func (s *TaskService) aggregateParentTask(ctx context.Context, parent taskdomain.Task) (taskdomain.Task, []taskdomain.Task, error) {
	repo, ok := s.repo.(childTaskRepository)
	if !ok {
		return parent, nil, nil
	}
	children, err := repo.ListChildTasks(ctx, parent.ID)
	if err != nil || len(children) == 0 {
		return parent, children, err
	}
	// Manager-driven workflows own their state machine and step progress. Their
	// Agent children are displayed hierarchically, but must not complete the
	// parent before later workflow steps have even been created.
	if parent.Type == taskdomain.TypeArchitecture || parent.Type == taskdomain.TypeClusterBootstrap {
		return parent, children, nil
	}
	completed, failed, progress := 0, 0, 0
	parentFailed := parent.Status == taskdomain.StatusFailed
	var earliestStart *time.Time
	var latestFinish *time.Time
	for _, child := range children {
		progress += child.ProgressPercent
		if child.Status == taskdomain.StatusSuccess || child.Status == taskdomain.StatusFailed {
			completed++
		}
		if child.Status == taskdomain.StatusFailed {
			failed++
		}
		if child.StartedAt != nil && (earliestStart == nil || child.StartedAt.Before(*earliestStart)) {
			value := *child.StartedAt
			earliestStart = &value
		}
		if child.FinishedAt != nil && (latestFinish == nil || child.FinishedAt.After(*latestFinish)) {
			value := *child.FinishedAt
			latestFinish = &value
		}
	}
	creationFailures := 0
	var spec map[string]any
	if json.Unmarshal(parent.SpecJSON, &spec) == nil {
		if value, ok := spec["creation_failures"].(float64); ok {
			creationFailures = int(value)
		}
	}
	parent.ProgressPercent = progress / len(children)
	if earliestStart != nil {
		parent.StartedAt = earliestStart
	}
	if completed == len(children) {
		parent.ProgressPercent = 100
		parent.FinishedAt = latestFinish
		if parent.FinishedAt == nil {
			now := time.Now().UTC()
			parent.FinishedAt = &now
		}
		if parentFailed || failed > 0 || creationFailures > 0 {
			parent.Status = taskdomain.StatusFailed
			parent.CurrentStep = fmt.Sprintf("子任务完成 %d/%d，失败 %d", completed, len(children), failed+creationFailures)
		} else {
			parent.Status = taskdomain.StatusSuccess
			parent.CurrentStep = fmt.Sprintf("子任务完成 %d/%d", completed, len(children))
		}
		return parent, children, nil
	}
	parent.FinishedAt = nil
	if parent.StartedAt == nil {
		parent.Status = taskdomain.StatusPending
	} else {
		parent.Status = taskdomain.StatusRunning
	}
	parent.CurrentStep = fmt.Sprintf("子任务完成 %d/%d", completed, len(children))
	return parent, children, nil
}

func (s *TaskService) syncParentTask(ctx context.Context, parentTaskID string) error {
	parentTaskID = strings.TrimSpace(parentTaskID)
	if parentTaskID == "" {
		return nil
	}
	parent, ok, err := s.repo.GetTask(ctx, parentTaskID)
	if err != nil || !ok {
		return err
	}
	aggregated, children, err := s.aggregateParentTask(ctx, parent)
	if err != nil || len(children) == 0 {
		return err
	}
	return s.repo.UpdateTask(ctx, aggregated)
}

// NewTaskService 创建任务管理服务实例。
func NewTaskService(repo taskdomain.Repository, createExec *taskusecase.CreateExecTaskUsecase, createCollect *taskusecase.CreateCollectMachineInfoUsecase, createStatic *taskusecase.CreateCollectStaticInfoUsecase, createMySQL *taskusecase.CreateMySQLInstallTaskUsecase, uninstallMySQL *taskusecase.CreateMySQLUninstallTaskUsecase, createTopology *taskusecase.CreateMySQLTopologyTaskUsecase, machineInfo MachineInfoSaver, staticInfo StaticInfoSaver, machines TaskMachineRepository, mysqlInstance MySQLInstanceSaver) *TaskService {
	return &TaskService{
		repo:           repo,
		createExec:     createExec,
		createCollect:  createCollect,
		createStatic:   createStatic,
		createMySQL:    createMySQL,
		uninstallMySQL: uninstallMySQL,
		createTopology: createTopology,
		machineInfo:    machineInfo,
		staticInfo:     staticInfo,
		machines:       machines,
		mysqlInstance:  mysqlInstance,
		agents:         make(map[string]TaskConnection),
		agentCaps:      make(map[string]map[string]bool),
		agentMachines:  make(map[string]string),
		machineAgents:  make(map[string]string),
	}
}

// IsAgentConnected 返回 Agent 是否具有可下发任务的实时连接。
func (s *TaskService) IsAgentConnected(agentID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.agents[agentID]
	return ok
}

// CreateMySQLTopologyTasks 创建 MySQL 拓扑检查任务并尝试下发。
func (s *TaskService) CreateMySQLTopologyTasks(ctx context.Context, req taskusecase.CreateMySQLTopologyTaskRequest) (MySQLTopologyTaskResult, error) {
	if s.createTopology == nil {
		return MySQLTopologyTaskResult{}, errors.New("mysql topology task usecase not configured")
	}
	result, err := s.createTopology.Execute(ctx, req)
	if err != nil {
		return MySQLTopologyTaskResult{}, err
	}
	parent, err := s.CreateBatchTrackingTask(ctx, "mysql_topology", "创建 MySQL 复制拓扑", req.Topology)
	if err != nil {
		return MySQLTopologyTaskResult{}, err
	}
	out := MySQLTopologyTaskResult{Created: len(result.Tasks), Parent: parent}
	created := 0
	for _, task := range result.Tasks {
		task.ParentTaskID = parent.Task.ID
		steps := result.Steps[task.ID]
		events := result.Events[task.ID]
		if err := s.repo.CreateTask(ctx, task, steps, events); err != nil {
			_ = s.FinalizeBatchTrackingTask(ctx, parent.Task.ID, created, len(result.Tasks)-created)
			return MySQLTopologyTaskResult{}, err
		}
		created++
		_ = s.tryDispatchPendingTask(ctx, task.ID)
		detail, err := s.GetTaskDetail(ctx, task.ID)
		if err != nil {
			return MySQLTopologyTaskResult{}, err
		}
		out.Tasks = append(out.Tasks, detail)
	}
	if err := s.FinalizeBatchTrackingTask(ctx, parent.Task.ID, created, len(result.Tasks)-created); err != nil {
		return MySQLTopologyTaskResult{}, err
	}
	out.Parent, _ = s.GetTaskDetail(ctx, parent.Task.ID)
	return out, nil
}

// CreateArchitectureTrackingTask 创建一个由 Manager 驱动的架构调整父任务。
// 它不下发给单个 Agent，而是把预检、选举、追平、切主和拓扑复核串成一条可审计任务。
func (s *TaskService) CreateArchitectureTrackingTask(ctx context.Context, run hadomain.ArchitectureRun) error {
	spec, err := json.Marshal(map[string]any{"run_id": run.RunID, "cluster": run.ClusterID, "architecture": run.Request.Architecture})
	if err != nil {
		return err
	}
	task := taskdomain.Task{ID: run.RunID, Type: taskdomain.TypeArchitecture, MachineID: run.ClusterID, AgentID: "manager", Status: taskdomain.StatusPending, ProgressPercent: 0, CurrentStep: "waiting_start", SpecJSON: spec, CreatedAt: run.CreatedAt}
	steps := make([]taskdomain.Step, 0, len(run.Plan.Steps))
	for _, planStep := range run.Plan.Steps {
		steps = append(steps, taskdomain.Step{ID: run.RunID + "-" + planStep.Code, TaskID: run.RunID, StepNo: planStep.Order, StepName: planStep.Code, Status: taskdomain.StepPending, Message: planStep.Name + "：" + planStep.Description})
	}
	events := []taskdomain.Event{{ID: run.RunID + "-created", TaskID: run.RunID, EventType: taskdomain.EventInfo, Content: "架构调整任务已创建，等待 Manager 开始安全执行。", CreatedAt: run.CreatedAt}}
	return s.repo.CreateTask(ctx, task, steps, events)
}

// SyncArchitectureTrackingTask 将架构执行器的实时状态同步到任务中心。
func (s *TaskService) SyncArchitectureTrackingTask(ctx context.Context, run hadomain.ArchitectureRun) error {
	task, found, err := s.repo.GetTask(ctx, run.RunID)
	if err != nil || !found {
		return err
	}
	now := run.UpdatedAt
	if now.IsZero() {
		now = time.Now().UTC()
	}
	switch run.Status {
	case hadomain.ArchitectureRunSucceeded:
		task.Status, task.ProgressPercent, task.CurrentStep, task.FinishedAt = taskdomain.StatusSuccess, 100, "release_lock", run.FinishedAt
	case hadomain.ArchitectureRunFailed:
		task.Status, task.CurrentStep, task.FinishedAt = taskdomain.StatusFailed, run.CurrentStep, run.FinishedAt
	case hadomain.ArchitectureRunRunning, hadomain.ArchitectureRunWaitingForce:
		task.Status, task.CurrentStep = taskdomain.StatusRunning, run.CurrentStep
		if task.StartedAt == nil {
			started := now
			task.StartedAt = &started
		}
	default:
		task.Status, task.CurrentStep = taskdomain.StatusPending, run.CurrentStep
	}
	steps, err := s.repo.ListSteps(ctx, run.RunID)
	if err != nil {
		return err
	}
	results := make(map[string]hadomain.ArchitectureRunStepResult)
	for _, result := range run.StepResults {
		results[result.Code] = result
	}
	completed := 0
	for _, step := range steps {
		result, ok := results[step.StepName]
		if !ok && run.Status == hadomain.ArchitectureRunFailed && step.StepName == run.CurrentStep {
			step.Status = taskdomain.StepFailed
			step.Message = run.Error
		} else if step.StepName == "acquire_lock" && run.Status != hadomain.ArchitectureRunPending {
			step.Status = taskdomain.StepSuccess
			step.Message = "已获取集群级切换锁，禁止并发架构变更。"
			completed++
		} else if step.StepName == "force_gate" && run.ForceConfirmed {
			step.Status = taskdomain.StepSuccess
			step.Message = "复制追平超时后已获得人工强制切换确认。"
			completed++
		} else if step.StepName == "release_lock" && run.Status == hadomain.ArchitectureRunSucceeded {
			step.Status = taskdomain.StepSuccess
			step.Message = "拓扑复核通过，切换锁已释放。"
			completed++
		} else if !ok && run.Status == hadomain.ArchitectureRunSucceeded && architectureStepMayBeSkipped(step.StepName) {
			step.Status = taskdomain.StepSuccess
			step.Message = "当前转换路径无需执行此步骤，Manager 已安全跳过。"
			completed++
		} else if ok {
			step.StartedAt = &result.StartedAt
			step.FinishedAt = result.FinishedAt
			step.Message = result.Message
			if step.Message == "" {
				step.Message = result.Name
				if len(result.TaskIDs) > 0 {
					step.Message += " · Agent 子任务：" + strings.Join(result.TaskIDs, "、")
				}
			}
			switch result.Status {
			case "success":
				step.Status = taskdomain.StepSuccess
				completed++
			case "failed":
				step.Status = taskdomain.StepFailed
			default:
				step.Status = taskdomain.StepRunning
			}
		} else if step.StepName == run.CurrentStep {
			step.Status = taskdomain.StepRunning
			if run.Status == hadomain.ArchitectureRunWaitingForce {
				step.Message = "复制未在 60 秒内追平，正在等待人工确认是否强制切主。"
			}
		}
		if err := s.repo.UpdateStep(ctx, step); err != nil {
			return err
		}
	}
	if task.Status != taskdomain.StatusSuccess && len(steps) > 0 {
		task.ProgressPercent = completed * 100 / len(steps)
	}
	return s.repo.UpdateTask(ctx, task)
}

func architectureStepMayBeSkipped(code string) bool {
	switch code {
	case "freeze_old_master", "kill_business_sessions", "wait_replication_zero", "force_gate", "fence_old_master", "pt_repair_on_failure", "pt_verify_before_split":
		return true
	default:
		return false
	}
}

// RegisterAgent 注册 Agent 的任务连接（无能力声明）。
func (s *TaskService) RegisterAgent(agentID string, conn TaskConnection) {
	s.RegisterAgentWithCapabilities(agentID, conn, nil)
}

// RegisterAgentWithCapabilities 注册 Agent 的任务连接及其支持的任务类型。
func (s *TaskService) RegisterAgentWithCapabilities(agentID string, conn TaskConnection, capabilities []string) {
	s.RegisterAgentForMachineWithCapabilities(agentID, "", conn, capabilities)
}

// RegisterAgentForMachineWithCapabilities registers the live connection and
// retains its machine identity so feature gates can be evaluated before a task
// is created.
func (s *TaskService) RegisterAgentForMachineWithCapabilities(agentID, machineID string, conn TaskConnection, capabilities []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.agents[agentID] = conn
	machineID = strings.TrimSpace(machineID)
	if previous := s.agentMachines[agentID]; previous != "" && previous != machineID {
		delete(s.machineAgents, previous)
	}
	if machineID != "" {
		s.agentMachines[agentID] = machineID
		s.machineAgents[machineID] = agentID
	}
	if len(capabilities) == 0 {
		delete(s.agentCaps, agentID)
		return
	}
	caps := make(map[string]bool, len(capabilities))
	for _, capability := range capabilities {
		capability = strings.TrimSpace(capability)
		if capability != "" {
			caps[capability] = true
		}
	}
	s.agentCaps[agentID] = caps
}

// UnregisterAgent 注销 Agent 的任务连接。
func (s *TaskService) UnregisterAgent(agentID string, conn TaskConnection) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.agents[agentID]
	if ok && current == conn {
		delete(s.agents, agentID)
		delete(s.agentCaps, agentID)
		if machineID := s.agentMachines[agentID]; machineID != "" {
			delete(s.machineAgents, machineID)
		}
		delete(s.agentMachines, agentID)
	}
}

// MachineCapability reports whether the currently connected Agent for a
// machine explicitly advertises a feature capability. Missing declarations
// are never treated as compatible, because legacy Agents only advertised task
// types and cannot safely execute commands that depend on newer substitutions.
func (s *TaskService) MachineCapability(machineID, capability string) (bool, string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	machineID = strings.TrimSpace(machineID)
	agentID := s.machineAgents[machineID]
	if agentID == "" {
		agentID = "agent-" + machineID
	}
	if _, online := s.agents[agentID]; !online {
		return false, "Agent 未连接任务通道"
	}
	if !s.agentCaps[agentID][capability] {
		return false, "Agent 版本过旧，缺少安全 MySQL 凭证注入能力"
	}
	return true, ""
}

// IsAgentOnline 检查指定 Agent 的任务连接是否在线。
func (s *TaskService) IsAgentOnline(agentID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.agents[agentID]
	return ok
}

// ListClusterMachines returns the managed machines that belong to any selected
// cluster. It deliberately lives in TaskService so HTTP automation handlers do
// not need direct access to infrastructure repositories.
func (s *TaskService) ListClusterMachines(ctx context.Context, clusters []string) ([]machinedomain.Machine, error) {
	if s.machines == nil {
		return nil, errors.New("machine repository not configured")
	}
	selected := make(map[string]bool, len(clusters))
	for _, name := range clusters {
		if name = strings.TrimSpace(name); name != "" {
			selected[name] = true
		}
	}
	if len(selected) == 0 {
		return nil, errors.New("at least one cluster is required")
	}
	items, err := s.machines.List(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]machinedomain.Machine, 0, len(items))
	for _, machine := range items {
		if selected[machine.Cluster] {
			result = append(result, machine)
		}
	}
	if len(result) == 0 {
		return nil, errors.New("no machines found in selected clusters")
	}
	return result, nil
}

// CreateExecTask 创建 Shell 命令执行任务并尝试下发。
func (s *TaskService) CreateExecTask(ctx context.Context, machine, command string) (TaskDetail, error) {
	return s.CreateExecTaskWithOptions(ctx, machine, command, ExecTaskOptions{})
}

// CreateExecTaskWithOptions 创建带业务语义的命令任务。
func (s *TaskService) CreateExecTaskWithOptions(ctx context.Context, machine, command string, opts ExecTaskOptions) (TaskDetail, error) {
	if s.createExec == nil {
		return TaskDetail{}, errors.New("task usecase not configured")
	}
	result, err := s.createExec.Execute(ctx, taskusecase.CreateExecTaskRequest{
		Machine: machine, Command: command, Commands: opts.Commands, RollbackCommand: opts.RollbackCommand, Operation: opts.Operation,
		DisplayName: opts.DisplayName, StepName: opts.StepName, Port: opts.Port, PackageName: opts.PackageName, TaskType: opts.TaskType,
	})
	if err != nil {
		return TaskDetail{}, err
	}
	result.Task.ParentTaskID = strings.TrimSpace(opts.ParentTaskID)
	if err := s.repo.CreateTask(ctx, result.Task, result.Steps, result.Events); err != nil {
		return TaskDetail{}, err
	}
	_ = s.tryDispatchPendingTask(ctx, result.Task.ID)

	task, _, err := s.repo.GetTask(ctx, result.Task.ID)
	if err != nil {
		return TaskDetail{}, err
	}
	steps, err := s.repo.ListSteps(ctx, result.Task.ID)
	if err != nil {
		return TaskDetail{}, err
	}
	events, err := s.repo.ListEvents(ctx, result.Task.ID, 100)
	if err != nil {
		return TaskDetail{}, err
	}
	return TaskDetail{Task: taskForDisplay(task), Steps: steps, Events: events}, nil
}

// RedactExecTaskCommand removes a completed one-off command from durable task
// storage. Architecture operations use this after the Agent has consumed the
// command because the transient command can contain database credentials.
func (s *TaskService) RedactExecTaskCommand(ctx context.Context, taskID string) error {
	task, ok, err := s.repo.GetTask(ctx, taskID)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("task not found")
	}
	if task.Type != taskdomain.TypeExec && task.Type != taskdomain.TypeMySQLUpgrade {
		return errors.New("only exec workflow commands can be redacted")
	}
	if task.Status != taskdomain.StatusSuccess && task.Status != taskdomain.StatusFailed {
		return errors.New("cannot redact a command before the task is terminal")
	}
	var spec taskdomain.ExecSpec
	_ = json.Unmarshal(task.SpecJSON, &spec)
	spec.Command = "[redacted after execution]"
	for i := range spec.Commands {
		spec.Commands[i].Command = "[redacted after execution]"
	}
	if spec.RollbackCommand != "" {
		spec.RollbackCommand = "[redacted after execution]"
	}
	task.SpecJSON, _ = json.Marshal(spec)
	return s.repo.UpdateTask(ctx, task)
}

// CreateCollectMachineInfoTask 创建机器信息采集任务并尝试下发。
func (s *TaskService) CreateCollectMachineInfoTask(ctx context.Context, machine string) (TaskDetail, error) {
	if s.createCollect == nil {
		return TaskDetail{}, errors.New("collect task usecase not configured")
	}
	result, err := s.createCollect.Execute(ctx, taskusecase.CreateCollectMachineInfoRequest{Machine: machine})
	if err != nil {
		return TaskDetail{}, err
	}
	if err := s.repo.CreateTask(ctx, result.Task, result.Steps, result.Events); err != nil {
		return TaskDetail{}, err
	}
	_ = s.tryDispatchPendingTask(ctx, result.Task.ID)
	task, _, err := s.repo.GetTask(ctx, result.Task.ID)
	if err != nil {
		return TaskDetail{}, err
	}
	steps, err := s.repo.ListSteps(ctx, result.Task.ID)
	if err != nil {
		return TaskDetail{}, err
	}
	events, err := s.repo.ListEvents(ctx, result.Task.ID, 100)
	if err != nil {
		return TaskDetail{}, err
	}
	return TaskDetail{Task: task, Steps: steps, Events: events}, nil
}

// CreateCollectStaticInfoTask 创建静态信息采集任务并尝试下发。
func (s *TaskService) CreateCollectStaticInfoTask(ctx context.Context, req taskusecase.CreateCollectStaticInfoRequest) (TaskDetail, error) {
	if s.createStatic == nil {
		return TaskDetail{}, errors.New("static collect task usecase not configured")
	}
	result, err := s.createStatic.Execute(ctx, req)
	if err != nil {
		return TaskDetail{}, err
	}
	if err := s.repo.CreateTask(ctx, result.Task, result.Steps, result.Events); err != nil {
		return TaskDetail{}, err
	}
	_ = s.tryDispatchPendingTask(ctx, result.Task.ID)
	return s.GetTaskDetail(ctx, result.Task.ID)
}

// CreateMySQLInstallTask 创建 MySQL 安装任务并尝试下发。
// 若机器信息不存在会自动触发采集。
func (s *TaskService) CreateMySQLInstallTask(ctx context.Context, req taskusecase.CreateMySQLInstallTaskRequest) (TaskDetail, error) {
	if s.createMySQL == nil {
		return TaskDetail{}, errors.New("mysql install task usecase not configured")
	}
	result, err := s.createMySQL.Execute(ctx, req)
	if errors.Is(err, taskusecase.ErrMachineInfoNotFound) {
		if collectErr := s.collectMachineInfoBeforeInstall(ctx, req.Machine); collectErr != nil {
			return TaskDetail{}, fmt.Errorf("%w: auto collect failed: %v", err, collectErr)
		}
		result, err = s.createMySQL.Execute(ctx, req)
	}
	if err != nil {
		return TaskDetail{}, err
	}
	if err := s.repo.CreateTask(ctx, result.Task, result.Steps, result.Events); err != nil {
		return TaskDetail{}, err
	}
	_ = s.tryDispatchPendingTask(ctx, result.Task.ID)
	task, _, err := s.repo.GetTask(ctx, result.Task.ID)
	if err != nil {
		return TaskDetail{}, err
	}
	steps, err := s.repo.ListSteps(ctx, result.Task.ID)
	if err != nil {
		return TaskDetail{}, err
	}
	events, err := s.repo.ListEvents(ctx, result.Task.ID, 200)
	if err != nil {
		return TaskDetail{}, err
	}
	return TaskDetail{Task: task, Steps: steps, Events: events}, nil
}

// ListMySQLPackages 返回可选安装版本，供 Web 与 CLI 在创建任务前展示。
func (s *TaskService) ListMySQLPackages() ([]mysqlapp.PackageOption, error) {
	if s.createMySQL == nil {
		return nil, errors.New("mysql install task usecase not configured")
	}
	return s.createMySQL.ListPackages()
}

// ResolveMySQLInstance resolves a machine selector and returns the registered
// instance paths used by parameter and upgrade operations.
func (s *TaskService) ResolveMySQLInstance(ctx context.Context, selector string, port int) (machinedomain.Machine, mysqlapp.Instance, error) {
	if s.machines == nil || s.mysqlInstance == nil {
		return machinedomain.Machine{}, mysqlapp.Instance{}, errors.New("machine or mysql instance repository is not configured")
	}
	items, err := s.machines.List(ctx)
	if err != nil {
		return machinedomain.Machine{}, mysqlapp.Instance{}, err
	}
	for _, machine := range items {
		if machine.ID != selector && machine.IP != selector && machine.Name != selector {
			continue
		}
		instance, ok, err := s.mysqlInstance.Get(ctx, machine.ID, port)
		if err != nil {
			return machinedomain.Machine{}, mysqlapp.Instance{}, err
		}
		if !ok {
			return machinedomain.Machine{}, mysqlapp.Instance{}, errors.New("mysql instance not found")
		}
		return machine, instance, nil
	}
	return machinedomain.Machine{}, mysqlapp.Instance{}, fmt.Errorf("machine %s not found", selector)
}

func (s *TaskService) resolveMySQLUpgrade(ctx context.Context, machineName string, port int, packageName string) (machinedomain.Machine, mysqlapp.Instance, mysqlapp.Package, string, error) {
	if s.createMySQL == nil || s.machineInfo == nil || s.mysqlInstance == nil || s.machines == nil {
		return machinedomain.Machine{}, mysqlapp.Instance{}, mysqlapp.Package{}, "", errors.New("mysql upgrade dependencies are not configured")
	}
	machineName, packageName = strings.TrimSpace(machineName), strings.TrimSpace(packageName)
	if machineName == "" || packageName == "" || port <= 0 || port > 65535 {
		return machinedomain.Machine{}, mysqlapp.Instance{}, mysqlapp.Package{}, "", errors.New("machine, port and target package are required")
	}
	machine, instance, err := s.ResolveMySQLInstance(ctx, machineName, port)
	if err != nil {
		return machinedomain.Machine{}, mysqlapp.Instance{}, mysqlapp.Package{}, "", err
	}
	info, ok, err := s.machineInfo.Get(ctx, machine.ID)
	if err != nil || !ok {
		if err == nil {
			err = errors.New("machine architecture and glibc information must be collected before upgrade")
		}
		return machinedomain.Machine{}, mysqlapp.Instance{}, mysqlapp.Package{}, "", err
	}
	targetPackage, err := s.createMySQL.ResolvePackage(info, packageName)
	if err != nil {
		return machinedomain.Machine{}, mysqlapp.Instance{}, mysqlapp.Package{}, "", err
	}
	currentVersion := strings.TrimSpace(instance.Version)
	if currentVersion == "" {
		currentVersion, err = mysqlapp.PackageVersion(instance.PackageName)
		if err != nil {
			return machinedomain.Machine{}, mysqlapp.Instance{}, mysqlapp.Package{}, "", fmt.Errorf("cannot determine current MySQL version from %s: %w", instance.PackageName, err)
		}
	}
	if err := mysqlapp.ValidateUpgradeCompatibility(currentVersion, targetPackage.Version); err != nil {
		return machinedomain.Machine{}, mysqlapp.Instance{}, mysqlapp.Package{}, "", err
	}
	return machine, instance, targetPackage, currentVersion, nil
}

// CreateMySQLUpgradePrecheck creates a separate, reviewable report. Credentials
// are injected by the Agent from its local 0600 management configuration and
// never cross the Manager API or appear in the durable task specification.
func (s *TaskService) CreateMySQLUpgradePrecheck(ctx context.Context, req MySQLUpgradeRequest) (MySQLUpgradePrecheckPlan, error) {
	machine, instance, targetPackage, currentVersion, err := s.resolveMySQLUpgrade(ctx, req.Machine, req.Port, req.PackageName)
	if err != nil {
		return MySQLUpgradePrecheckPlan{}, err
	}
	if compatible, reason := s.MachineCapability(machine.ID, taskdomain.CapabilityMySQLDefaultsFile); !compatible {
		return MySQLUpgradePrecheckPlan{}, errors.New(reason)
	}
	baseDir, myCnf := filepath.Clean(instance.BaseDir), filepath.Clean(instance.MyCnfPath)
	q := upgradeShellQuote
	defaults := mysqlDefaultsFilePlaceholder
	client := q(baseDir+"/bin/mysql") + " --defaults-extra-file=" + defaults + fmt.Sprintf(" --protocol=tcp --host=127.0.0.1 --port=%d --batch --raw --table", instance.Port)
	checker := "if command -v mysqlsh >/dev/null 2>&1; then echo 'checker=mysqlsh util.checkForServerUpgrade'; mysqlsh --defaults-file=" + defaults + fmt.Sprintf(" --host=127.0.0.1 --port=%d -- util check-for-server-upgrade --target-version=%s; else echo 'checker=mysqlcheck --check-upgrade (mysqlsh unavailable)'; %s --defaults-extra-file=%s --protocol=tcp --host=127.0.0.1 --port=%d --all-databases --check-upgrade; fi", instance.Port, q(targetPackage.Version), q(baseDir+"/bin/mysqlcheck"), defaults, instance.Port)
	commands := []taskdomain.ExecCommandStep{
		{Name: "安装布局与系统环境", Command: strings.Join([]string{
			"echo 'GMHA_PRECHECK_SECTION=安装布局与系统环境'",
			"test -L " + q(baseDir) + " || { echo 'BLOCKER: base_dir 不是软链接，禁止执行换链升级'; exit 1; }",
			"current_target=$(readlink -f " + q(baseDir) + ") && test -x \"$current_target/bin/mysqld\"",
			"echo current_target=$current_target",
			"echo current_binary_version=$(" + q(baseDir+"/bin/mysqld") + " --version)",
			"echo target_version=" + q(targetPackage.Version) + " target_package=" + q(targetPackage.FileName),
			"echo machine_arch=$(uname -m) glibc=$(ldd --version 2>&1 | head -1)",
			"df -Pk " + q(filepath.Dir(baseDir)) + " | tail -1",
			"test -r " + q(myCnf) + " && " + q(baseDir+"/bin/mysqld") + " --defaults-file=" + q(myCnf) + " --validate-config",
		}, " && ")},
		{Name: "运行状态与复制风险", Command: "echo 'GMHA_PRECHECK_SECTION=运行状态与复制风险' && " + client + " --execute=" + q("SELECT @@version AS current_version, @@version_comment AS edition, @@global.read_only AS read_only, @@global.super_read_only AS super_read_only; SELECT COUNT(*) AS active_transactions FROM information_schema.innodb_trx; SELECT CHANNEL_NAME, SERVICE_STATE, LAST_ERROR_NUMBER, LAST_ERROR_MESSAGE FROM performance_schema.replication_connection_status; SELECT CHANNEL_NAME, SERVICE_STATE, LAST_ERROR_NUMBER, LAST_ERROR_MESSAGE FROM performance_schema.replication_applier_status_by_coordinator;")},
		{Name: "MySQL 官方升级检查", Command: "echo 'GMHA_PRECHECK_SECTION=MySQL 官方升级检查' && " + checker},
	}
	detail, err := s.CreateExecTaskWithOptions(ctx, machine.IP, "", ExecTaskOptions{Operation: "mysql_upgrade_precheck", DisplayName: fmt.Sprintf("MySQL %s → %s 升级预检", currentVersion, targetPackage.Version), Port: instance.Port, PackageName: targetPackage.FileName, Commands: commands, TaskType: taskdomain.TypeMySQLUpgrade})
	if err != nil {
		return MySQLUpgradePrecheckPlan{}, err
	}
	return MySQLUpgradePrecheckPlan{CurrentVersion: currentVersion, TargetVersion: targetPackage.Version, CurrentPackage: instance.PackageName, TargetPackage: targetPackage.FileName, Checker: "MySQL Shell Upgrade Checker（优先）/ mysqlcheck --check-upgrade（回退）", Task: detail}, nil
}

func (s *TaskService) validateMySQLUpgradePrecheck(ctx context.Context, taskID, machineID string, port int, packageName string) error {
	if strings.TrimSpace(taskID) == "" {
		return errors.New("请先完成升级预检")
	}
	task, ok, err := s.repo.GetTask(ctx, strings.TrimSpace(taskID))
	if err != nil || !ok {
		if err == nil {
			err = errors.New("预检任务不存在")
		}
		return err
	}
	var spec taskdomain.ExecSpec
	if err := json.Unmarshal(task.SpecJSON, &spec); err != nil {
		return errors.New("预检任务数据无效")
	}
	if task.Status != taskdomain.StatusSuccess || task.MachineID != machineID || spec.Operation != "mysql_upgrade_precheck" || spec.Port != port || spec.PackageName != packageName {
		return errors.New("预检未通过，或报告与当前实例/目标版本不匹配")
	}
	if time.Since(task.CreatedAt) > 30*time.Minute {
		return errors.New("预检报告已超过 30 分钟，请重新检查")
	}
	return nil
}

// CreateMySQLUpgradeTask builds a fully logged in-place upgrade workflow. The
// active installation path remains stable and only its symbolic-link target is
// atomically replaced. Binary rollback is automatic only before the new server
// has started; after a data-dictionary upgrade, restoring a verified backup is
// safer than starting old binaries against upgraded data files.
func (s *TaskService) CreateMySQLUpgradeTask(ctx context.Context, req MySQLUpgradeRequest) (MySQLUpgradePlan, error) {
	machine, instance, targetPackage, currentVersion, err := s.resolveMySQLUpgrade(ctx, req.Machine, req.Port, req.PackageName)
	if err != nil {
		return MySQLUpgradePlan{}, err
	}
	if !req.RiskAcknowledged {
		return MySQLUpgradePlan{}, errors.New("必须确认已完成可恢复备份并接受停机与兼容性风险")
	}
	if compatible, reason := s.MachineCapability(machine.ID, taskdomain.CapabilityMySQLDefaultsFile); !compatible {
		return MySQLUpgradePlan{}, errors.New(reason)
	}
	if !req.Force {
		if err := s.validateMySQLUpgradePrecheck(ctx, req.PrecheckTaskID, machine.ID, instance.Port, targetPackage.FileName); err != nil {
			return MySQLUpgradePlan{}, err
		}
	}
	baseDir := filepath.Clean(instance.BaseDir)
	myCnf := filepath.Clean(instance.MyCnfPath)
	unit := strings.TrimSuffix(strings.TrimSpace(instance.SystemdUnit), ".service")
	if unit == "" {
		unit = fmt.Sprintf("mysqld-%d", instance.Port)
	}
	targetDir := filepath.Join(filepath.Dir(baseDir), strings.TrimSuffix(targetPackage.FileName, ".tar.xz"))
	archive := filepath.Join("/tmp", targetPackage.FileName)
	stateDir := fmt.Sprintf("/var/lib/gmha/mysql-upgrade-%d", instance.Port)
	client := fmt.Sprintf("%s/bin/mysql --defaults-extra-file=%s --protocol=tcp --host=127.0.0.1 --port=%d --batch --raw --skip-column-names", upgradeShellQuote(baseDir), mysqlDefaultsFilePlaceholder, instance.Port)
	downloadURL := "__GMHA_MANAGER_URL__/api/v1/software/mysql/" + url.PathEscape(targetPackage.FileName)
	q := upgradeShellQuote
	commands := []taskdomain.ExecCommandStep{
		{Name: "执行前安全复核", Command: strings.Join([]string{
			fmt.Sprintf("echo force_upgrade=%t precheck_task=%s", req.Force, q(req.PrecheckTaskID)),
			"test -L " + q(baseDir) + " || { echo 'MySQL base_dir is not a symbolic link'; exit 1; }",
			"test -x " + q(baseDir+"/bin/mysqld"),
			"echo current_version=$(" + q(baseDir+"/bin/mysqld") + " --version)",
			"echo target_package=" + q(targetPackage.FileName) + " target_version=" + q(targetPackage.Version),
			"echo machine_arch=$(uname -m) glibc=$(ldd --version 2>&1 | head -1)",
			"df -h " + q(filepath.Dir(baseDir)),
		}, " && ")},
		{Name: "暂停数据库写入", Command: "mkdir -p " + q(stateDir) + " && readlink -f " + q(baseDir) + " > " + q(stateDir+"/old_target") + " && " + client + " --execute=" + q("SELECT @@global.read_only") + " > " + q(stateDir+"/read_only") + " && " + client + " --execute=" + q("SET GLOBAL super_read_only=ON; SET GLOBAL read_only=ON;") + " && for i in $(seq 1 60); do n=$(" + client + " --execute=" + q("SELECT COUNT(*) FROM information_schema.innodb_trx") + "); [ \"${n:-1}\" = 0 ] && break; sleep 1; done; [ \"${n:-1}\" = 0 ]"},
		{Name: "下载目标安装包", Command: "rm -f " + q(archive) + " && if command -v curl >/dev/null 2>&1; then curl -fL --retry 3 -o " + q(archive) + " " + q(downloadURL) + "; else wget -O " + q(archive) + " " + q(downloadURL) + "; fi && test -s " + q(archive)},
		{Name: "解压并检查新版本", Command: "rm -rf " + q(targetDir) + " && tar -xJf " + q(archive) + " -C " + q(filepath.Dir(baseDir)) + " && test -x " + q(targetDir+"/bin/mysqld") + " && " + q(targetDir+"/bin/mysqld") + " --version && chown -R " + q(instance.MySQLUser+":"+instance.MySQLUser) + " " + q(targetDir)},
		{Name: "停止数据库服务", Command: "systemctl stop " + q(unit) + " && ! systemctl is-active --quiet " + q(unit)},
		{Name: "原子切换软连接", Command: "ln -sfn " + q(targetDir) + " " + q(baseDir+".gmha-next") + " && mv -Tf " + q(baseDir+".gmha-next") + " " + q(baseDir) + " && test \"$(readlink -f " + q(baseDir) + ")\" = " + q(targetDir)},
		{Name: "校验升级后配置", Command: q(baseDir+"/bin/mysqld") + " --defaults-file=" + q(myCnf) + " --validate-config"},
		{Name: "启动与数据字典升级", Command: "systemctl start " + q(unit) + " && touch " + q(stateDir+"/new_server_started") + " && for i in $(seq 1 120); do " + client + " --execute=" + q("SELECT 1") + " >/dev/null 2>&1 && break; sleep 1; done && " + client + " --execute=" + q("SELECT CONCAT('upgraded_version=', @@version); SELECT TABLE_SCHEMA, COUNT(*) FROM information_schema.tables GROUP BY TABLE_SCHEMA ORDER BY TABLE_SCHEMA;")},
		{Name: "数据库完整性检查", Command: fmt.Sprintf("%s/bin/mysqlcheck --defaults-extra-file=%s --protocol=tcp --host=127.0.0.1 --port=%d --all-databases --check-upgrade", q(baseDir), mysqlDefaultsFilePlaceholder, instance.Port)},
		{Name: "主从复制检查与修复", Command: client + " --execute=" + q("START REPLICA") + " 2>/dev/null || " + client + " --execute=" + q("START SLAVE") + " 2>/dev/null || true; " + client + " --execute=" + q("SELECT CHANNEL_NAME, SERVICE_STATE, LAST_ERROR_NUMBER, LAST_ERROR_MESSAGE FROM performance_schema.replication_connection_status; SELECT CHANNEL_NAME, SERVICE_STATE, LAST_ERROR_NUMBER, LAST_ERROR_MESSAGE FROM performance_schema.replication_applier_status_by_coordinator;")},
		{Name: "恢复业务访问", Command: "old_read_only=$(cat " + q(stateDir+"/read_only") + "); if [ \"$old_read_only\" = 0 ]; then " + client + " --execute=" + q("SET GLOBAL super_read_only=OFF; SET GLOBAL read_only=OFF;") + "; fi; " + client + " --execute=" + q("SELECT @@version, @@global.read_only, @@global.super_read_only;") + " && rm -rf " + q(stateDir)},
	}
	rollback := "old_target=$(cat " + q(stateDir+"/old_target") + " 2>/dev/null || true); if [ -e " + q(stateDir+"/new_server_started") + " ]; then echo 'SAFEGUARD: 新版本已启动并可能升级数据字典，不自动切回旧二进制；实例保持新版本并需要人工检查或从备份恢复'; systemctl start " + q(unit) + " || true; else systemctl stop " + q(unit) + " || true; if [ -n \"$old_target\" ] && [ -x \"$old_target/bin/mysqld\" ]; then ln -sfn \"$old_target\" " + q(baseDir+".gmha-rollback") + " && mv -Tf " + q(baseDir+".gmha-rollback") + " " + q(baseDir) + "; fi; systemctl start " + q(unit) + " || true; echo rollback_target=$(readlink -f " + q(baseDir) + "); fi"
	displayName := fmt.Sprintf("MySQL %s 升级到 %s", currentVersion, targetPackage.Version)
	if req.Force {
		displayName = "[强制] " + displayName
	}
	detail, err := s.CreateExecTaskWithOptions(ctx, machine.IP, "", ExecTaskOptions{Operation: "mysql_upgrade", DisplayName: displayName, Port: instance.Port, PackageName: targetPackage.FileName, Commands: commands, RollbackCommand: rollback, TaskType: taskdomain.TypeMySQLUpgrade})
	if err != nil {
		return MySQLUpgradePlan{}, err
	}
	return MySQLUpgradePlan{CurrentVersion: currentVersion, TargetVersion: targetPackage.Version, CurrentPackage: instance.PackageName, TargetPackage: targetPackage.FileName, PrecheckTaskID: req.PrecheckTaskID, Forced: req.Force, Task: detail}, nil
}

func upgradeShellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

// CreateClusterMySQLInstallTasks 为集群内所有机器创建 MySQL 安装任务。
func (s *TaskService) CreateClusterMySQLInstallTasks(ctx context.Context, req ClusterMySQLInstallRequest) (ClusterMySQLInstallResult, error) {
	cluster := strings.TrimSpace(req.Cluster)
	if cluster == "" {
		return ClusterMySQLInstallResult{}, errors.New("cluster is required")
	}
	if s.machines == nil {
		return ClusterMySQLInstallResult{}, errors.New("machine repository not configured")
	}
	machines, err := s.machines.List(ctx)
	if err != nil {
		return ClusterMySQLInstallResult{}, err
	}

	targetCount := 0
	for _, machine := range machines {
		if machine.Cluster == cluster {
			targetCount++
		}
	}
	if targetCount == 0 {
		return ClusterMySQLInstallResult{}, fmt.Errorf("cluster %s has no machines", cluster)
	}
	parent, err := s.CreateBatchTrackingTask(ctx, "cluster_mysql_install", "批量安装 MySQL", cluster)
	if err != nil {
		return ClusterMySQLInstallResult{}, err
	}
	result := ClusterMySQLInstallResult{Cluster: cluster, Parent: parent}
	serverID := req.ServerIDStart
	if serverID <= 0 {
		serverID = 1
	}
	for _, machine := range machines {
		if machine.Cluster != cluster {
			continue
		}
		item := ClusterMySQLInstallItem{MachineID: machine.ID, Name: machine.Name, IP: machine.IP}
		installReq := taskusecase.CreateMySQLInstallTaskRequest{
			ParentTaskID:      parent.Task.ID,
			Machine:           machine.IP,
			Port:              req.Port,
			ServerID:          serverID,
			MySQLUser:         req.MySQLUser,
			InstanceDir:       req.InstanceDir,
			DataDir:           req.DataDir,
			BinlogDir:         req.BinlogDir,
			RedoDir:           req.RedoDir,
			UndoDir:           req.UndoDir,
			TmpDir:            req.TmpDir,
			BaseDir:           req.BaseDir,
			MyCnfPath:         req.MyCnfPath,
			SocketPath:        req.SocketPath,
			ErrorLog:          req.ErrorLog,
			PIDFile:           req.PIDFile,
			CharacterSetsDir:  req.CharacterSetsDir,
			PluginDir:         req.PluginDir,
			RootPassword:      req.RootPassword,
			Profile:           req.Profile,
			Version:           req.Version,
			Architecture:      req.Architecture,
			InstallPTTools:    req.InstallPTTools,
			InstallXtraBackup: req.InstallXtraBackup,
			MemoryAllocator:   req.MemoryAllocator,
			RuntimeParameters: req.RuntimeParameters,
			Accounts:          req.Accounts,
		}
		detail, err := s.CreateMySQLInstallTask(ctx, installReq)
		if err != nil {
			item.Error = err.Error()
			result.Failed++
		} else {
			item.Task = detail
			result.Created++
		}
		result.Items = append(result.Items, item)
		serverID++
	}
	if err := s.FinalizeBatchTrackingTask(ctx, parent.Task.ID, result.Created, result.Failed); err != nil {
		return ClusterMySQLInstallResult{}, err
	}
	result.Parent, _ = s.GetTaskDetail(ctx, parent.Task.ID)
	return result, nil
}

// CreateClusterMySQLUninstallTasks 为集群内所有机器创建 MySQL 卸载任务。
func (s *TaskService) CreateClusterMySQLUninstallTasks(ctx context.Context, req ClusterMySQLUninstallRequest) (ClusterMySQLUninstallResult, error) {
	cluster := strings.TrimSpace(req.Cluster)
	if cluster == "" {
		return ClusterMySQLUninstallResult{}, errors.New("cluster is required")
	}
	if s.machines == nil || s.mysqlInstance == nil {
		return ClusterMySQLUninstallResult{}, errors.New("machine or mysql instance repository not configured")
	}
	machines, err := s.machines.List(ctx)
	if err != nil {
		return ClusterMySQLUninstallResult{}, err
	}

	result := ClusterMySQLUninstallResult{Cluster: cluster}
	var parent TaskDetail
	for _, machine := range machines {
		if machine.Cluster != cluster {
			continue
		}
		// 检查该机器上是否真的有该端口的 MySQL 实例记录
		_, ok, err := s.mysqlInstance.Get(ctx, machine.ID, req.Port)
		if err != nil {
			continue // 忽略查询错误，尝试下一台
		}
		if !ok {
			// 如果没有记录，跳过，不计入失败，因为可能本来就没装
			continue
		}
		if parent.Task.ID == "" {
			parent, err = s.CreateBatchTrackingTask(ctx, "cluster_mysql_uninstall", "批量卸载 MySQL", cluster)
			if err != nil {
				return ClusterMySQLUninstallResult{}, err
			}
			result.Parent = parent
		}

		item := ClusterMySQLUninstallItem{MachineID: machine.ID, Name: machine.Name, IP: machine.IP}
		uninstallReq := taskusecase.CreateMySQLUninstallTaskRequest{
			ParentTaskID: parent.Task.ID,
			Machine:      machine.IP,
			Port:         req.Port,
		}
		detail, err := s.CreateMySQLUninstallTask(ctx, uninstallReq)
		if err != nil {
			item.Error = err.Error()
			result.Failed++
		} else {
			item.Task = detail
			result.Created++
		}
		result.Items = append(result.Items, item)
	}
	if len(result.Items) == 0 {
		return ClusterMySQLUninstallResult{}, fmt.Errorf("集群 %s 中未找到运行在端口 %d 的 MySQL 实例", cluster, req.Port)
	}
	if err := s.FinalizeBatchTrackingTask(ctx, parent.Task.ID, result.Created, result.Failed); err != nil {
		return ClusterMySQLUninstallResult{}, err
	}
	result.Parent, _ = s.GetTaskDetail(ctx, parent.Task.ID)
	return result, nil
}

func (s *TaskService) collectMachineInfoBeforeInstall(ctx context.Context, machine string) error {
	if s.createCollect == nil {
		return errors.New("collect machine info usecase not configured")
	}
	result, err := s.createCollect.Execute(ctx, taskusecase.CreateCollectMachineInfoRequest{Machine: machine})
	if err != nil {
		return err
	}
	if err := s.repo.CreateTask(ctx, result.Task, result.Steps, result.Events); err != nil {
		return err
	}
	_ = s.tryDispatchPendingTask(ctx, result.Task.ID)
	detail, err := s.WaitForTask(ctx, result.Task.ID, 45*time.Second)
	if err != nil {
		return err
	}
	if detail.Task.Status != taskdomain.StatusSuccess {
		return fmt.Errorf("collect machine info task failed: %s", emptyTaskError(TaskDetail(detail)))
	}
	return nil
}

// CreateMySQLUninstallTask 创建 MySQL 卸载任务并尝试下发。
func (s *TaskService) CreateMySQLUninstallTask(ctx context.Context, req taskusecase.CreateMySQLUninstallTaskRequest) (TaskDetail, error) {
	if s.uninstallMySQL == nil {
		return TaskDetail{}, errors.New("mysql uninstall task usecase not configured")
	}
	result, err := s.uninstallMySQL.Execute(ctx, req)
	if err != nil {
		return TaskDetail{}, err
	}
	if err := s.repo.CreateTask(ctx, result.Task, result.Steps, result.Events); err != nil {
		return TaskDetail{}, err
	}
	_ = s.tryDispatchPendingTask(ctx, result.Task.ID)
	task, _, err := s.repo.GetTask(ctx, result.Task.ID)
	if err != nil {
		return TaskDetail{}, err
	}
	steps, err := s.repo.ListSteps(ctx, result.Task.ID)
	if err != nil {
		return TaskDetail{}, err
	}
	events, err := s.repo.ListEvents(ctx, result.Task.ID, 200)
	if err != nil {
		return TaskDetail{}, err
	}
	return TaskDetail{Task: task, Steps: steps, Events: events}, nil
}

func (s *TaskService) dispatch(ctx context.Context, task taskdomain.Task, steps []taskdomain.Step) error {
	s.mu.RLock()
	conn, ok := s.agents[task.AgentID]
	supportsTask := s.agentSupportsLocked(task.AgentID, string(task.Type))
	s.mu.RUnlock()
	if !ok {
		return errors.New("agent task connection is offline")
	}
	if !supportsTask {
		return fmt.Errorf("connected agent does not support task type %s; upgrade agent first", task.Type)
	}

	dispatchSteps := make([]taskdomain.DispatchStep, 0, len(steps))
	for _, step := range steps {
		dispatchSteps = append(dispatchSteps, taskdomain.DispatchStep{
			ID:       step.ID,
			StepNo:   step.StepNo,
			StepName: step.StepName,
		})
	}
	envelope := taskdomain.DispatchEnvelope{
		Kind: "task_dispatch",
		Task: taskdomain.DispatchTask{
			ID:        task.ID,
			Type:      string(task.Type),
			MachineID: task.MachineID,
			AgentID:   task.AgentID,
			Spec:      append(json.RawMessage(nil), task.SpecJSON...),
			Steps:     dispatchSteps,
		},
	}
	if err := conn.Send(envelope); err != nil {
		return err
	}
	now := time.Now().UTC()
	task.Status = taskdomain.StatusSent
	task.CurrentStep = "任务已下发"
	task.StartedAt = &now
	if err := s.repo.UpdateTask(ctx, task); err != nil {
		return err
	}
	return s.repo.AppendEvent(ctx, taskdomain.Event{
		ID:        fmt.Sprintf("task-event-%d", time.Now().UnixNano()),
		TaskID:    task.ID,
		StepID:    steps[0].ID,
		EventType: taskdomain.EventInfo,
		Content:   "task dispatched to agent",
		CreatedAt: now,
	})
}

// DispatchPending 批量下发所有待处理的任务。
func (s *TaskService) DispatchPending(ctx context.Context, limit int) error {
	items, err := s.repo.ListTasksByStatus(ctx, taskdomain.StatusPending, limit)
	if err != nil {
		return err
	}
	for _, task := range items {
		if err := s.tryDispatchPendingTask(ctx, task.ID); err != nil {
			continue
		}
	}
	return nil
}

func (s *TaskService) tryDispatchPendingTask(ctx context.Context, taskID string) error {
	task, ok, err := s.repo.GetTask(ctx, taskID)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("task not found")
	}
	if task.Status != taskdomain.StatusPending {
		return nil
	}
	steps, err := s.repo.ListSteps(ctx, task.ID)
	if err != nil {
		return err
	}
	if err := s.dispatch(ctx, task, steps); err != nil {
		if strings.Contains(err.Error(), "does not support task type") {
			_ = s.markTaskDispatchFailed(ctx, task, steps, err)
		}
		return err
	}
	return nil
}

func (s *TaskService) agentSupportsLocked(agentID, taskType string) bool {
	caps, ok := s.agentCaps[agentID]
	if !ok || len(caps) == 0 {
		return taskType == string(taskdomain.TypeExec) || taskType == string(taskdomain.TypeCollectMachineInfo) || taskType == string(taskdomain.TypeCollectStaticInfo)
	}
	return caps[taskType]
}

func (s *TaskService) markTaskDispatchFailed(ctx context.Context, task taskdomain.Task, steps []taskdomain.Step, cause error) error {
	now := time.Now().UTC()
	task.Status = taskdomain.StatusFailed
	task.ProgressPercent = 100
	if len(steps) > 0 {
		task.CurrentStep = steps[0].StepName
		steps[0].Status = taskdomain.StepFailed
		steps[0].Message = cause.Error()
		steps[0].StartedAt = &now
		steps[0].FinishedAt = &now
		_ = s.repo.UpdateStep(ctx, steps[0])
	}
	task.StartedAt = &now
	task.FinishedAt = &now
	if err := s.repo.UpdateTask(ctx, task); err != nil {
		return err
	}
	stepID := ""
	if len(steps) > 0 {
		stepID = steps[0].ID
	}
	return s.repo.AppendEvent(ctx, taskdomain.Event{
		ID:        fmt.Sprintf("task-event-%d", time.Now().UnixNano()),
		TaskID:    task.ID,
		StepID:    stepID,
		EventType: taskdomain.EventError,
		Content:   cause.Error(),
		CreatedAt: now,
	})
}

// HandleReport 处理 Agent 上报的任务执行报告，更新任务和步骤状态，处理副作用（如保存 MySQL 实例）。
func (s *TaskService) HandleReport(ctx context.Context, report taskdomain.ReportEnvelope) error {
	task, ok, err := s.repo.GetTask(ctx, report.TaskID)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("task not found")
	}
	if task.ParentTaskID != "" {
		defer func() { _ = s.syncParentTask(context.WithoutCancel(ctx), task.ParentTaskID) }()
	}
	if task.Status == taskdomain.StatusSuccess || task.Status == taskdomain.StatusFailed {
		return s.applyTerminalTaskSideEffects(ctx, task, report, time.Now().UTC())
	}

	now := time.Now().UTC()
	if task.StartedAt == nil {
		task.StartedAt = &now
	}
	task.Status = report.Status
	task.ProgressPercent = report.Progress
	task.CurrentStep = report.CurrentStep
	if report.Status == taskdomain.StatusSuccess || report.Status == taskdomain.StatusFailed {
		task.FinishedAt = &now
	}
	if err := s.repo.UpdateTask(ctx, task); err != nil {
		return err
	}

	if report.Step != nil {
		steps, err := s.repo.ListSteps(ctx, report.TaskID)
		if err != nil {
			return err
		}
		for _, step := range steps {
			if step.ID != report.Step.StepID {
				continue
			}
			step.Status = report.Step.Status
			step.Message = report.Step.Message
			if report.Step.StartedAt != nil && step.StartedAt == nil {
				step.StartedAt = report.Step.StartedAt
			}
			if report.Step.FinishedAt != nil {
				step.FinishedAt = report.Step.FinishedAt
			}
			if err := s.repo.UpdateStep(ctx, step); err != nil {
				return err
			}
			break
		}
	}

	if report.Event != nil {
		if report.Event.ID == "" {
			report.Event.ID = fmt.Sprintf("task-event-%d", time.Now().UnixNano())
		}
		if report.Event.CreatedAt.IsZero() {
			report.Event.CreatedAt = now
		}
		report.Event.TaskID = report.TaskID
		if err := s.repo.AppendEvent(ctx, *report.Event); err != nil {
			return err
		}
	}
	if len(report.Result) > 0 && task.Type == taskdomain.TypeCollectMachineInfo && s.machineInfo != nil {
		var info collectdomain.MachineInfo
		if err := json.Unmarshal(report.Result, &info); err == nil {
			info.MachineID = task.MachineID
			info.TimeOffsetMS = time.Now().UTC().UnixMilli() - info.AgentTimeUnixMS
			info.UpdatedAt = now
			if err := s.machineInfo.Save(ctx, info); err != nil {
				return err
			}
		}
	}
	if len(report.Result) > 0 && task.Type == taskdomain.TypeCollectStaticInfo && s.staticInfo != nil {
		var info collectdomain.StaticInfo
		if err := json.Unmarshal(report.Result, &info); err == nil {
			info.MachineID = task.MachineID
			nowMS := time.Now().UTC().UnixMilli()
			info.Host.TimeOffsetMS = nowMS - info.AgentTimeUnixMS
			if s.machines != nil {
				if machine, ok, machineErr := s.machines.GetByID(ctx, task.MachineID); machineErr == nil && ok {
					info.Host.MachineStatus = string(machine.Status)
					info.Host.SSHUser = machine.SSHUser
					info.Host.SSHPort = machine.SSHPort
					info.Host.SSHAvailable = machine.Status != machinedomain.StatusSSHFailed && machine.Status != machinedomain.StatusPending
				}
			}
			info.UpdatedAt = now
			if err := s.staticInfo.Save(ctx, info); err != nil {
				return err
			}
		}
	}
	if err := s.applyTerminalTaskSideEffects(ctx, task, report, now); err != nil {
		return err
	}
	if report.Error != "" {
		return s.repo.AppendEvent(ctx, taskdomain.Event{
			ID:        fmt.Sprintf("task-event-%d", time.Now().UnixNano()),
			TaskID:    report.TaskID,
			StepID:    stepID(report.Step),
			EventType: taskdomain.EventError,
			Content:   report.Error,
			CreatedAt: now,
		})
	}
	return nil
}

func (s *TaskService) applyTerminalTaskSideEffects(ctx context.Context, task taskdomain.Task, report taskdomain.ReportEnvelope, now time.Time) error {
	if report.Status != taskdomain.StatusSuccess {
		return nil
	}
	if task.Type == taskdomain.TypeMySQLInstall && s.mysqlInstance != nil {
		var result taskdomain.MySQLInstallResult
		if len(report.Result) > 0 {
			_ = json.Unmarshal(report.Result, &result)
		}
		var spec taskdomain.MySQLInstallSpec
		_ = json.Unmarshal(task.SpecJSON, &spec)
		if result.Port == 0 {
			result = taskdomain.MySQLInstallResult{
				Port:         spec.Port,
				ServerID:     spec.ServerID,
				MySQLUser:    spec.MySQLUser,
				InstanceDir:  spec.InstanceDir,
				DataDir:      spec.DataDir,
				BinlogDir:    spec.BinlogDir,
				RedoDir:      spec.RedoDir,
				UndoDir:      spec.UndoDir,
				TmpDir:       spec.TmpDir,
				BaseDir:      spec.BaseDir,
				Profile:      spec.Profile,
				PackageName:  spec.PackageName,
				Version:      spec.Version,
				Architecture: spec.Architecture,
				SystemdUnit:  spec.SystemdUnitName,
				MyCnfPath:    spec.MyCnfPath,
				SocketPath:   spec.SocketPath,
			}
		}
		// Older Agents may return the original result shape without the newly
		// separated version and architecture fields. Keep mixed-version Manager /
		// Agent deployments safe by filling structured metadata from the task spec.
		if result.PackageName == "" {
			result.PackageName = spec.PackageName
		}
		if result.Version == "" {
			result.Version = spec.Version
			if result.Version == "" {
				result.Version, _ = mysqlapp.PackageVersion(result.PackageName)
			}
		}
		if result.Architecture == "" {
			result.Architecture = spec.Architecture
			if result.Architecture == "" {
				result.Architecture, _ = mysqlapp.PackageArchitecture(result.PackageName)
			}
		}
		if err := s.mysqlInstance.Save(ctx, mysqlapp.Instance{
			MachineID:    task.MachineID,
			Port:         result.Port,
			ServerID:     result.ServerID,
			MySQLUser:    result.MySQLUser,
			InstanceDir:  result.InstanceDir,
			DataDir:      result.DataDir,
			BinlogDir:    result.BinlogDir,
			RedoDir:      result.RedoDir,
			UndoDir:      result.UndoDir,
			TmpDir:       result.TmpDir,
			BaseDir:      result.BaseDir,
			Profile:      result.Profile,
			PackageName:  result.PackageName,
			Version:      result.Version,
			Architecture: result.Architecture,
			SystemdUnit:  result.SystemdUnit,
			MyCnfPath:    result.MyCnfPath,
			SocketPath:   result.SocketPath,
			Status:       mysqlapp.StatusRunning,
			LastTaskID:   task.ID,
			UpdatedAt:    now,
		}); err != nil {
			return err
		}
	}
	if task.Type == taskdomain.TypeMySQLUninstall && s.mysqlInstance != nil {
		var result taskdomain.MySQLUninstallResult
		if len(report.Result) > 0 {
			_ = json.Unmarshal(report.Result, &result)
		}
		var spec taskdomain.MySQLUninstallSpec
		_ = json.Unmarshal(task.SpecJSON, &spec)
		if result.Port == 0 {
			result.Port = spec.Port
		}
		if err := s.mysqlInstance.Delete(ctx, task.MachineID, result.Port); err != nil {
			return err
		}
	}
	if task.Type == taskdomain.TypeMySQLUpgrade && s.mysqlInstance != nil {
		var spec taskdomain.ExecSpec
		_ = json.Unmarshal(task.SpecJSON, &spec)
		if spec.Operation == "mysql_upgrade" && spec.Port > 0 && spec.PackageName != "" {
			instance, ok, err := s.mysqlInstance.Get(ctx, task.MachineID, spec.Port)
			if err != nil {
				return err
			}
			if ok {
				instance.PackageName = spec.PackageName
				if version, versionErr := mysqlapp.PackageVersion(spec.PackageName); versionErr == nil {
					instance.Version = version
				}
				if architecture, archErr := mysqlapp.PackageArchitecture(spec.PackageName); archErr == nil {
					instance.Architecture = architecture
				}
				instance.Status = mysqlapp.StatusRunning
				instance.LastTaskID = task.ID
				instance.UpdatedAt = now
				if err := s.mysqlInstance.Save(ctx, instance); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// WaitForTask 等待任务完成（成功或失败），支持超时。
func (s *TaskService) WaitForTask(ctx context.Context, taskID string, timeout time.Duration) (TaskDetail, error) {
	waitCtx := ctx
	var cancel context.CancelFunc
	if timeout > 0 {
		waitCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	for {
		item, err := s.GetTaskDetail(waitCtx, taskID)
		if err != nil {
			return TaskDetail{}, err
		}
		switch item.Task.Status {
		case taskdomain.StatusSuccess, taskdomain.StatusFailed:
			return item, nil
		}
		select {
		case <-waitCtx.Done():
			item, innerErr := s.GetTaskDetail(context.Background(), taskID)
			if innerErr == nil && item.Task.Status == taskdomain.StatusPending {
				return TaskDetail{}, errors.New("timed out waiting for task completion; task is still pending, ensure manager service is running and agent task connection is online")
			}
			return TaskDetail{}, errors.New("timed out waiting for task completion")
		case <-ticker.C:
		}
	}
}

// ListTasks 列出最近的任务。
func (s *TaskService) ListTasks(ctx context.Context, limit int) ([]taskdomain.Task, error) {
	items, err := s.repo.ListTasks(ctx, limit)
	if err != nil {
		return nil, err
	}
	for i := range items {
		if aggregated, children, aggregateErr := s.aggregateParentTask(ctx, items[i]); aggregateErr == nil {
			items[i] = aggregated
			items[i].Children = taskChildrenForDisplay(children)
		}
		items[i] = taskForDisplay(items[i])
	}
	return items, nil
}

func (s *TaskService) ListTaskPage(ctx context.Context, query TaskListQuery) (TaskListPage, error) {
	if query.Limit <= 0 {
		query.Limit = 50
	}
	if query.Limit > 200 {
		query.Limit = 200
	}
	if query.Offset < 0 {
		query.Offset = 0
	}
	if repo, ok := s.repo.(taskPageRepository); ok {
		items, total, err := repo.ListTaskPage(ctx, query)
		if err != nil {
			return TaskListPage{}, err
		}
		for i := range items {
			if aggregated, children, aggregateErr := s.aggregateParentTask(ctx, items[i]); aggregateErr == nil {
				items[i] = aggregated
				items[i].Children = taskChildrenForDisplay(children)
			}
			items[i] = taskForDisplay(items[i])
		}
		return TaskListPage{Items: items, Total: total, Page: query.Offset/query.Limit + 1, Size: query.Limit}, nil
	}
	items, err := s.ListTasks(ctx, query.Offset+query.Limit)
	if err != nil {
		return TaskListPage{}, err
	}
	total := len(items)
	if query.Offset >= total {
		items = []taskdomain.Task{}
	} else {
		end := query.Offset + query.Limit
		if end > total {
			end = total
		}
		items = items[query.Offset:end]
	}
	return TaskListPage{Items: items, Total: total, Page: query.Offset/query.Limit + 1, Size: query.Limit}, nil
}

func taskChildrenForDisplay(children []taskdomain.Task) []taskdomain.Task {
	if len(children) == 0 {
		return nil
	}
	out := make([]taskdomain.Task, len(children))
	for i := range children {
		out[i] = taskForDisplay(children[i])
	}
	return out
}

// RecordPlatformOperation persists a synchronous management action in the same
// task timeline used by Agent work. It intentionally stores only operational
// metadata, never request bodies or credentials.
func (s *TaskService) RecordPlatformOperation(ctx context.Context, spec taskdomain.PlatformOperationSpec, startedAt, finishedAt time.Time, operationErr string) (TaskDetail, error) {
	if s.repo == nil {
		return TaskDetail{}, errors.New("task repository is not configured")
	}
	if startedAt.IsZero() {
		startedAt = time.Now().UTC()
	}
	if finishedAt.Before(startedAt) {
		finishedAt = startedAt
	}
	if parentID := s.commonRelatedParent(ctx, spec.RelatedTaskIDs); parentID != "" {
		return s.GetTaskDetail(ctx, parentID)
	}
	data, err := json.Marshal(spec)
	if err != nil {
		return TaskDetail{}, err
	}
	taskID := fmt.Sprintf("platform-task-%d", time.Now().UTC().UnixNano())
	if strings.TrimSpace(spec.RequestID) != "" {
		digest := sha256.Sum256([]byte(spec.Method + "\x00" + spec.Path + "\x00" + spec.RequestID))
		taskID = fmt.Sprintf("platform-task-%x", digest[:12])
		if _, ok, lookupErr := s.repo.GetTask(ctx, taskID); lookupErr != nil {
			return TaskDetail{}, lookupErr
		} else if ok {
			return s.GetTaskDetail(ctx, taskID)
		}
	}
	status := taskdomain.StatusSuccess
	stepStatus := taskdomain.StepSuccess
	message := "平台操作执行成功"
	eventType := taskdomain.EventInfo
	if strings.TrimSpace(operationErr) != "" {
		status = taskdomain.StatusFailed
		stepStatus = taskdomain.StepFailed
		message = strings.TrimSpace(operationErr)
		eventType = taskdomain.EventError
	}
	task := taskdomain.Task{
		ID: taskID, Type: taskdomain.TypePlatformOperation, MachineID: spec.Target,
		Status: status, ProgressPercent: 100, CurrentStep: spec.DisplayName,
		SpecJSON: data, CreatedAt: startedAt, StartedAt: &startedAt, FinishedAt: &finishedAt,
	}
	steps := []taskdomain.Step{
		{ID: taskID + "-request", TaskID: taskID, StepNo: 1, StepName: "接收操作请求", Status: taskdomain.StepSuccess, Message: spec.Method + " " + spec.Path, StartedAt: &startedAt, FinishedAt: &startedAt},
		{ID: taskID + "-execute", TaskID: taskID, StepNo: 2, StepName: spec.DisplayName, Status: stepStatus, Message: message, StartedAt: &startedAt, FinishedAt: &finishedAt},
	}
	for _, relatedID := range spec.RelatedTaskIDs {
		steps = append(steps, taskdomain.Step{ID: fmt.Sprintf("%s-related-%d", taskID, len(steps)+1), TaskID: taskID, StepNo: len(steps) + 1, StepName: "关联执行任务", Status: taskdomain.StepSuccess, Message: relatedID, StartedAt: &finishedAt, FinishedAt: &finishedAt})
	}
	events := []taskdomain.Event{
		{ID: taskID + "-created", TaskID: taskID, StepID: steps[0].ID, EventType: taskdomain.EventInfo, Content: "平台已接收操作请求", CreatedAt: startedAt},
		{ID: taskID + "-result", TaskID: taskID, StepID: steps[1].ID, EventType: eventType, Content: message, CreatedAt: finishedAt},
	}
	if err := s.repo.CreateTask(ctx, task, steps, events); err != nil {
		// Concurrent retries can both pass the lookup. The deterministic task
		// ID lets one writer win without adding a duplicate audit record.
		if strings.TrimSpace(spec.RequestID) != "" {
			if _, ok, lookupErr := s.repo.GetTask(ctx, taskID); lookupErr == nil && ok {
				return s.GetTaskDetail(ctx, taskID)
			}
		}
		return TaskDetail{}, err
	}
	if len(spec.RelatedTaskIDs) > 0 {
		if err := s.AttachChildTasks(ctx, taskID, spec.RelatedTaskIDs); err != nil {
			return TaskDetail{}, err
		}
		return s.GetTaskDetail(ctx, taskID)
	}
	return TaskDetail{Task: taskForDisplay(task), Steps: steps, Events: events}, nil
}

func (s *TaskService) commonRelatedParent(ctx context.Context, taskIDs []string) string {
	if len(taskIDs) == 0 {
		return ""
	}
	candidate := ""
	items := make([]taskdomain.Task, 0, len(taskIDs))
	for _, id := range taskIDs {
		item, ok, err := s.repo.GetTask(ctx, strings.TrimSpace(id))
		if err != nil || !ok {
			return ""
		}
		items = append(items, item)
		if item.ParentTaskID != "" {
			if candidate == "" {
				candidate = item.ParentTaskID
			} else if candidate != item.ParentTaskID {
				return ""
			}
		}
	}
	if candidate == "" {
		return ""
	}
	for _, item := range items {
		if item.ID != candidate && item.ParentTaskID != candidate {
			return ""
		}
	}
	return candidate
}

// DeleteTask removes a terminal task together with its steps and events.
// Pending or active tasks must remain durable until the Agent reports a final
// state, otherwise execution could continue without an audit trail.
func (s *TaskService) DeleteTask(ctx context.Context, taskID string) error {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return errors.New("task id is required")
	}
	task, ok, err := s.repo.GetTask(ctx, taskID)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("task not found")
	}
	if task.ParentTaskID != "" {
		return fmt.Errorf("task %s is a child task; delete its parent task %s instead", taskID, task.ParentTaskID)
	}
	if aggregated, children, aggregateErr := s.aggregateParentTask(ctx, task); aggregateErr != nil {
		return aggregateErr
	} else if len(children) > 0 {
		task = aggregated
	}
	if err := s.ensureTaskTreeTerminal(ctx, task); err != nil {
		return err
	}
	if repo, ok := s.repo.(taskHierarchyRepository); ok {
		return repo.DeleteTaskTree(ctx, taskID)
	}
	return s.repo.DeleteTask(ctx, taskID)
}

func (s *TaskService) ensureTaskTreeTerminal(ctx context.Context, item taskdomain.Task) error {
	if item.Status != taskdomain.StatusSuccess && item.Status != taskdomain.StatusFailed {
		return fmt.Errorf("task %s is %s and cannot be deleted before completion", item.ID, item.Status)
	}
	repo, ok := s.repo.(childTaskRepository)
	if !ok {
		return nil
	}
	children, err := repo.ListChildTasks(ctx, item.ID)
	if err != nil {
		return err
	}
	for _, child := range children {
		if err := s.ensureTaskTreeTerminal(ctx, child); err != nil {
			return err
		}
	}
	return nil
}

func (s *TaskService) DeleteTasks(ctx context.Context, req DeleteTasksRequest) (DeleteTasksResult, error) {
	ids := append([]string(nil), req.TaskIDs...)
	if req.AllFiltered {
		query := req.Query
		query.Offset, query.Limit = 0, 200
		for {
			page, err := s.ListTaskPage(ctx, query)
			if err != nil {
				return DeleteTasksResult{}, err
			}
			for _, item := range page.Items {
				ids = append(ids, item.ID)
			}
			query.Offset += len(page.Items)
			if len(page.Items) == 0 || query.Offset >= page.Total {
				break
			}
		}
	}
	seen := make(map[string]bool, len(ids))
	result := DeleteTasksResult{Items: []DeleteTaskResult{}}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		result.Requested++
		item := DeleteTaskResult{TaskID: id}
		if err := s.DeleteTask(ctx, id); err != nil {
			item.Error = err.Error()
			result.Failed++
		} else {
			result.Deleted++
		}
		result.Items = append(result.Items, item)
	}
	if result.Requested == 0 {
		return result, errors.New("at least one task id or all_filtered is required")
	}
	return result, nil
}

// GetTaskDetail 获取任务的完整详情（任务、步骤、事件、机器信息）。
func (s *TaskService) GetTaskDetail(ctx context.Context, taskID string) (TaskDetail, error) {
	task, ok, err := s.repo.GetTask(ctx, taskID)
	if err != nil {
		return TaskDetail{}, err
	}
	if !ok {
		return TaskDetail{}, errors.New("task not found")
	}
	steps, err := s.repo.ListSteps(ctx, taskID)
	if err != nil {
		return TaskDetail{}, err
	}
	events, err := s.repo.ListEvents(ctx, taskID, -1)
	if err != nil {
		return TaskDetail{}, err
	}
	detail := TaskDetail{Task: taskForDisplay(task), Steps: steps, Events: events}
	if repo, ok := s.repo.(childTaskRepository); ok {
		children, err := repo.ListChildTasks(ctx, taskID)
		if err != nil {
			return TaskDetail{}, err
		}
		for i := range children {
			children[i] = taskForDisplay(children[i])
			childDetail, detailErr := s.GetTaskDetail(ctx, children[i].ID)
			if detailErr != nil {
				return TaskDetail{}, detailErr
			}
			detail.ChildDetails = append(detail.ChildDetails, childDetail)
		}
		if len(children) > 0 {
			if aggregated, _, aggregateErr := s.aggregateParentTask(ctx, task); aggregateErr == nil {
				detail.Task = taskForDisplay(aggregated)
			}
		}
		detail.Children = children
	}
	if s.machines != nil {
		machine, ok, err := s.machines.GetByID(ctx, task.MachineID)
		if err != nil {
			return TaskDetail{}, err
		}
		if ok {
			detail.MachineName = machine.Name
			detail.MachineIP = machine.IP
		}
	}
	return detail, nil
}

// taskForDisplay 只保留任务中心展示所需的业务元数据。
// 完整任务规格会被 Agent 用于执行，可能包含密码、配置文件正文和下载地址，
// 不能通过任务列表或详情接口返回给浏览器。
func taskForDisplay(task taskdomain.Task) taskdomain.Task {
	if len(task.SpecJSON) == 0 {
		return task
	}
	var display any
	switch task.Type {
	case taskdomain.TypeExec, taskdomain.TypeMySQLUpgrade:
		var spec taskdomain.ExecSpec
		if json.Unmarshal(task.SpecJSON, &spec) != nil {
			task.SpecJSON = json.RawMessage(`{}`)
			return task
		}
		display = taskdomain.ExecSpec{Operation: spec.Operation, DisplayName: spec.DisplayName, Port: spec.Port, PackageName: spec.PackageName}
	case taskdomain.TypeMySQLInstall:
		var spec taskdomain.MySQLInstallSpec
		if json.Unmarshal(task.SpecJSON, &spec) != nil {
			task.SpecJSON = json.RawMessage(`{}`)
			return task
		}
		display = map[string]any{
			"port": spec.Port, "server_id": spec.ServerID, "mysql_user": spec.MySQLUser,
			"profile": spec.Profile, "version": spec.Version, "architecture": spec.Architecture,
			"package_name": spec.PackageName, "install_pt_tools": spec.InstallPTTools, "install_xtrabackup": spec.InstallXtraBackup, "memory_allocator": spec.MemoryAllocator,
		}
	case taskdomain.TypeMySQLUninstall:
		var spec taskdomain.MySQLUninstallSpec
		if json.Unmarshal(task.SpecJSON, &spec) != nil {
			task.SpecJSON = json.RawMessage(`{}`)
			return task
		}
		display = map[string]any{"port": spec.Port, "mysql_user": spec.MySQLUser, "package_name": spec.PackageName}
	case taskdomain.TypeMySQLTopology:
		var spec taskdomain.MySQLTopologySpec
		if json.Unmarshal(task.SpecJSON, &spec) != nil {
			task.SpecJSON = json.RawMessage(`{}`)
			return task
		}
		display = map[string]any{
			"topology": spec.Topology, "port": spec.Port, "use_clone": spec.UseClone,
			"parallel_type": spec.ParallelType, "parallel_workers": spec.ParallelWorkers,
		}
	case taskdomain.TypeCollectStaticInfo:
		var spec taskdomain.CollectStaticInfoSpec
		if json.Unmarshal(task.SpecJSON, &spec) != nil {
			task.SpecJSON = json.RawMessage(`{}`)
			return task
		}
		display = map[string]any{"mysql": map[string]any{
			"enabled": spec.MySQL.Enabled, "host": spec.MySQL.Host, "port": spec.MySQL.Port,
			"socket": spec.MySQL.Socket, "username": spec.MySQL.Username,
		}}
	case taskdomain.TypePlatformOperation:
		var spec taskdomain.PlatformOperationSpec
		if json.Unmarshal(task.SpecJSON, &spec) != nil {
			task.SpecJSON = json.RawMessage(`{}`)
			return task
		}
		display = spec
	case taskdomain.TypeBatchOperation, taskdomain.TypeClusterBootstrap, taskdomain.TypeArchitecture:
		var spec map[string]any
		if json.Unmarshal(task.SpecJSON, &spec) != nil {
			task.SpecJSON = json.RawMessage(`{}`)
			return task
		}
		display = map[string]any{
			"operation": spec["operation"], "display_name": spec["display_name"], "target": spec["target"],
			"cluster": spec["cluster"], "architecture": spec["architecture"], "targets": spec["targets"],
			"created": spec["created"], "creation_failures": spec["creation_failures"], "run_id": spec["run_id"],
		}
	default:
		// 未知任务类型默认不暴露规格，新增类型必须显式声明可展示字段。
		display = map[string]any{}
	}
	if data, err := json.Marshal(display); err == nil {
		task.SpecJSON = data
	} else {
		task.SpecJSON = json.RawMessage(`{}`)
	}
	return task
}

func stepID(step *taskdomain.StepReport) string {
	if step == nil {
		return ""
	}
	return step.StepID
}
