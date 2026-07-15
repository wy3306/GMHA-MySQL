package task

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	agentdomain "gmha/internal/domain/agent"
	taskdomain "gmha/internal/domain/task"
	mysqlapp "gmha/internal/mysql"
)

// MySQLInstanceRepository 定义了 MySQL 实例仓储接口，用于查询实例信息。
type MySQLInstanceRepository interface {
	Get(ctx context.Context, machineID string, port int) (mysqlapp.Instance, bool, error)
}

// CreateMySQLUninstallTaskRequest 是创建 MySQL 卸载任务的请求参数。
type CreateMySQLUninstallTaskRequest struct {
	Machine string
	Port    int
}

// CreateMySQLUninstallTaskResult 是创建 MySQL 卸载任务的结果。
type CreateMySQLUninstallTaskResult struct {
	Task   taskdomain.Task
	Steps  []taskdomain.Step
	Events []taskdomain.Event
}

// CreateMySQLUninstallTaskUsecase 是创建 MySQL 卸载任务的用例。
type CreateMySQLUninstallTaskUsecase struct {
	machines  MachineRepository
	agents    AgentRepository
	instances MySQLInstanceRepository
}

// NewCreateMySQLUninstallTaskUsecase 创建一个新的 MySQL 卸载任务用例实例。
func NewCreateMySQLUninstallTaskUsecase(machines MachineRepository, agents AgentRepository, instances MySQLInstanceRepository) *CreateMySQLUninstallTaskUsecase {
	return &CreateMySQLUninstallTaskUsecase{
		machines:  machines,
		agents:    agents,
		instances: instances,
	}
}

// Execute 执行创建 MySQL 卸载任务的流程，包括验证参数、查询实例信息和构建卸载规格。
func (u *CreateMySQLUninstallTaskUsecase) Execute(ctx context.Context, req CreateMySQLUninstallTaskRequest) (CreateMySQLUninstallTaskResult, error) {
	target := strings.TrimSpace(req.Machine)
	if target == "" {
		return CreateMySQLUninstallTaskResult{}, errors.New("machine is required")
	}
	if req.Port <= 0 {
		return CreateMySQLUninstallTaskResult{}, errors.New("port is required")
	}

	machine, ok, err := (&CreateExecTaskUsecase{machines: u.machines, agents: u.agents}).resolveMachine(ctx, target)
	if err != nil {
		return CreateMySQLUninstallTaskResult{}, err
	}
	if !ok {
		return CreateMySQLUninstallTaskResult{}, fmt.Errorf("machine %s not found", target)
	}
	agent, ok, err := u.agents.GetByMachineID(ctx, machine.ID)
	if err != nil {
		return CreateMySQLUninstallTaskResult{}, err
	}
	if !ok || agent.State != agentdomain.StateOnline {
		return CreateMySQLUninstallTaskResult{}, errors.New("online agent is required for mysql uninstall")
	}
	instance, ok, err := u.instances.Get(ctx, machine.ID, req.Port)
	if err != nil {
		return CreateMySQLUninstallTaskResult{}, err
	}
	spec := defaultMySQLUninstallSpec(req.Port)
	if ok {
		spec = mysqlUninstallSpecFromInstance(req.Port, instance)
	}

	specJSON, _ := json.Marshal(spec)

	now := time.Now().UTC()
	taskID := fmt.Sprintf("task-%d", now.UnixNano())
	task := taskdomain.Task{
		ID:              taskID,
		Type:            taskdomain.TypeMySQLUninstall,
		MachineID:       machine.ID,
		AgentID:         agent.ID,
		Status:          taskdomain.StatusPending,
		ProgressPercent: 0,
		CurrentStep:     "等待派发",
		SpecJSON:        specJSON,
		CreatedAt:       now,
	}
	steps := buildMySQLUninstallSteps(taskID)
	events := []taskdomain.Event{{
		ID:        fmt.Sprintf("task-event-%d", now.UnixNano()),
		TaskID:    taskID,
		StepID:    steps[0].ID,
		EventType: taskdomain.EventInfo,
		Content:   "mysql_uninstall task created",
		CreatedAt: now,
	}}
	return CreateMySQLUninstallTaskResult{Task: task, Steps: steps, Events: events}, nil
}

// mysqlUninstallSpecFromInstance 将可能来自“采纳已有实例”的不完整记录合并到
// 安全默认值中。空字段不能覆盖默认值，否则 filepath.Clean("") 会变成 "."，
// Agent 的路径保护会正确拒绝卸载。
func mysqlUninstallSpecFromInstance(port int, instance mysqlapp.Instance) taskdomain.MySQLUninstallSpec {
	spec := defaultMySQLUninstallSpec(port)
	if instance.Port > 0 {
		spec.Port = instance.Port
	}
	spec.MySQLUser = firstNonEmpty(instance.MySQLUser, spec.MySQLUser)
	spec.DataDir = firstNonEmpty(instance.DataDir, spec.DataDir)
	spec.BinlogDir = firstNonEmpty(instance.BinlogDir, spec.BinlogDir)
	spec.RedoDir = firstNonEmpty(instance.RedoDir, spec.RedoDir)
	spec.UndoDir = firstNonEmpty(instance.UndoDir, spec.UndoDir)
	spec.TmpDir = firstNonEmpty(instance.TmpDir, spec.TmpDir)
	spec.InstanceDir = firstNonEmpty(instance.InstanceDir, inferInstanceDir(spec), spec.InstanceDir)
	spec.BaseDir = firstNonEmpty(instance.BaseDir, spec.BaseDir)
	spec.PackageName = strings.TrimSpace(instance.PackageName)
	spec.SystemdUnitName = firstNonEmpty(instance.SystemdUnit, spec.SystemdUnitName)
	spec.MyCnfPath = firstNonEmpty(instance.MyCnfPath, filepath.Join(spec.InstanceDir, "my.cnf"))
	spec.SocketPath = firstNonEmpty(instance.SocketPath, filepath.Join(spec.DataDir, "mysql.sock"))
	return spec
}

func inferInstanceDir(spec taskdomain.MySQLUninstallSpec) string {
	paths := []string{spec.DataDir, spec.BinlogDir, spec.RedoDir, spec.UndoDir, spec.TmpDir}
	common := ""
	for _, value := range paths {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		parent := filepath.Dir(filepath.Clean(value))
		if common == "" {
			common = parent
			continue
		}
		if common != parent {
			return ""
		}
	}
	switch common {
	case "", ".", "/", "/data", "/var", "/usr", "/usr/local", "/opt", "/home", "/tmp":
		return ""
	default:
		return common
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

// defaultMySQLUninstallSpec 根据端口号生成默认的 MySQL 卸载规格。
func defaultMySQLUninstallSpec(port int) taskdomain.MySQLUninstallSpec {
	instanceDir := fmt.Sprintf("/data/%d", port)
	dataDir := instanceDir + "/data"
	return taskdomain.MySQLUninstallSpec{
		Port:            port,
		MySQLUser:       "mysql",
		InstanceDir:     instanceDir,
		DataDir:         dataDir,
		BinlogDir:       instanceDir + "/binlog",
		RedoDir:         instanceDir + "/redo",
		UndoDir:         instanceDir + "/undo",
		TmpDir:          instanceDir + "/tmp",
		BaseDir:         "/usr/local/mysql",
		SystemdUnitName: "mysqld",
		MyCnfPath:       instanceDir + "/my.cnf",
		SocketPath:      dataDir + "/mysql.sock",
		ExtraPaths: []string{
			"/etc/profile.d/mysql.sh",
			"/etc/security/limits.d/mysql.conf",
			"/etc/sysctl.d/99-gmha-mysql.conf",
		},
	}
}

// buildMySQLUninstallSteps 构建 MySQL 卸载任务的所有步骤。
func buildMySQLUninstallSteps(taskID string) []taskdomain.Step {
	names := []string{
		"stop_mysql",
		"disable_systemd",
		"remove_systemd",
		"remove_instance_dirs",
		"remove_mysql_package",
		"remove_base_symlink",
		"remove_config_files",
		"daemon_reload",
		"verify_removed",
	}
	steps := make([]taskdomain.Step, 0, len(names))
	now := time.Now().UTC().UnixNano()
	for i, name := range names {
		steps = append(steps, taskdomain.Step{
			ID:       fmt.Sprintf("task-step-%d-%d", now, i+1),
			TaskID:   taskID,
			StepNo:   i + 1,
			StepName: name,
			Status:   taskdomain.StepPending,
			Message:  "等待 Agent 执行",
		})
	}
	return steps
}
