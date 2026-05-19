package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	collectdomain "gmha/internal/collect"
	machinedomain "gmha/internal/domain/machine"
	mysqlapp "gmha/internal/mysql"
	"strings"
	"sync"
	"time"

	taskdomain "gmha/internal/domain/task"
	taskusecase "gmha/internal/usecase/task"
)

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
}

// MachineInfoSaver 定义了机器信息保存接口。
type MachineInfoSaver interface {
	Save(ctx context.Context, item collectdomain.MachineInfo) error
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
	Task        taskdomain.Task    `json:"task"`
	MachineName string             `json:"machine_name,omitempty"`
	MachineIP   string             `json:"machine_ip,omitempty"`
	Steps       []taskdomain.Step  `json:"steps"`
	Events      []taskdomain.Event `json:"events"`
}

// ClusterMySQLInstallRequest 是集群级 MySQL 批量安装请求。
type ClusterMySQLInstallRequest struct {
	Cluster          string
	Port             int
	ServerIDStart    int
	MySQLUser        string
	InstanceDir      string
	DataDir          string
	BinlogDir        string
	RedoDir          string
	UndoDir          string
	TmpDir           string
	BaseDir          string
	MyCnfPath        string
	SocketPath       string
	ErrorLog         string
	PIDFile          string
	CharacterSetsDir string
	PluginDir        string
	RootPassword     string
	Profile          string
	Accounts         []taskdomain.MySQLAccountSpec
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
	Created int                         `json:"created"`
	Failed  int                         `json:"failed"`
	Items   []ClusterMySQLUninstallItem `json:"items"`
}

// MySQLTopologyTaskResult 是 MySQL 拓扑检查任务的创建结果。
type MySQLTopologyTaskResult struct {
	Created int          `json:"created"`
	Tasks   []TaskDetail `json:"tasks"`
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
	}
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
	out := MySQLTopologyTaskResult{Created: len(result.Tasks)}
	for _, task := range result.Tasks {
		steps := result.Steps[task.ID]
		events := result.Events[task.ID]
		if err := s.repo.CreateTask(ctx, task, steps, events); err != nil {
			return MySQLTopologyTaskResult{}, err
		}
		_ = s.tryDispatchPendingTask(ctx, task.ID)
		detail, err := s.GetTaskDetail(ctx, task.ID)
		if err != nil {
			return MySQLTopologyTaskResult{}, err
		}
		out.Tasks = append(out.Tasks, detail)
	}
	return out, nil
}

// RegisterAgent 注册 Agent 的任务连接（无能力声明）。
func (s *TaskService) RegisterAgent(agentID string, conn TaskConnection) {
	s.RegisterAgentWithCapabilities(agentID, conn, nil)
}

// RegisterAgentWithCapabilities 注册 Agent 的任务连接及其支持的任务类型。
func (s *TaskService) RegisterAgentWithCapabilities(agentID string, conn TaskConnection, capabilities []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.agents[agentID] = conn
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
	}
}

// IsAgentOnline 检查指定 Agent 的任务连接是否在线。
func (s *TaskService) IsAgentOnline(agentID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.agents[agentID]
	return ok
}

// CreateExecTask 创建 Shell 命令执行任务并尝试下发。
func (s *TaskService) CreateExecTask(ctx context.Context, machine, command string) (TaskDetail, error) {
	if s.createExec == nil {
		return TaskDetail{}, errors.New("task usecase not configured")
	}
	result, err := s.createExec.Execute(ctx, taskusecase.CreateExecTaskRequest{
		Machine: machine,
		Command: command,
	})
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

	result := ClusterMySQLInstallResult{Cluster: cluster}
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
			Machine:          machine.IP,
			Port:             req.Port,
			ServerID:         serverID,
			MySQLUser:        req.MySQLUser,
			InstanceDir:      req.InstanceDir,
			DataDir:          req.DataDir,
			BinlogDir:        req.BinlogDir,
			RedoDir:          req.RedoDir,
			UndoDir:          req.UndoDir,
			TmpDir:           req.TmpDir,
			BaseDir:          req.BaseDir,
			MyCnfPath:        req.MyCnfPath,
			SocketPath:       req.SocketPath,
			ErrorLog:         req.ErrorLog,
			PIDFile:          req.PIDFile,
			CharacterSetsDir: req.CharacterSetsDir,
			PluginDir:        req.PluginDir,
			RootPassword:     req.RootPassword,
			Profile:          req.Profile,
			Accounts:         req.Accounts,
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
	if len(result.Items) == 0 {
		return ClusterMySQLInstallResult{}, fmt.Errorf("cluster %s has no machines", cluster)
	}
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

		item := ClusterMySQLUninstallItem{MachineID: machine.ID, Name: machine.Name, IP: machine.IP}
		uninstallReq := taskusecase.CreateMySQLUninstallTaskRequest{
			Machine: machine.IP,
			Port:    req.Port,
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
				Port:        spec.Port,
				ServerID:    spec.ServerID,
				MySQLUser:   spec.MySQLUser,
				InstanceDir: spec.InstanceDir,
				DataDir:     spec.DataDir,
				BinlogDir:   spec.BinlogDir,
				RedoDir:     spec.RedoDir,
				UndoDir:     spec.UndoDir,
				TmpDir:      spec.TmpDir,
				BaseDir:     spec.BaseDir,
				Profile:     spec.Profile,
				PackageName: spec.PackageName,
				SystemdUnit: spec.SystemdUnitName,
				MyCnfPath:   spec.MyCnfPath,
				SocketPath:  spec.SocketPath,
			}
		}
		if err := s.mysqlInstance.Save(ctx, mysqlapp.Instance{
			MachineID:   task.MachineID,
			Port:        result.Port,
			ServerID:    result.ServerID,
			MySQLUser:   result.MySQLUser,
			InstanceDir: result.InstanceDir,
			DataDir:     result.DataDir,
			BinlogDir:   result.BinlogDir,
			RedoDir:     result.RedoDir,
			UndoDir:     result.UndoDir,
			TmpDir:      result.TmpDir,
			BaseDir:     result.BaseDir,
			Profile:     result.Profile,
			PackageName: result.PackageName,
			SystemdUnit: result.SystemdUnit,
			MyCnfPath:   result.MyCnfPath,
			SocketPath:  result.SocketPath,
			Status:      mysqlapp.StatusRunning,
			LastTaskID:  task.ID,
			UpdatedAt:   now,
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
	return s.repo.ListTasks(ctx, limit)
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
	events, err := s.repo.ListEvents(ctx, taskID, 200)
	if err != nil {
		return TaskDetail{}, err
	}
	detail := TaskDetail{Task: task, Steps: steps, Events: events}
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

func stepID(step *taskdomain.StepReport) string {
	if step == nil {
		return ""
	}
	return step.StepID
}
