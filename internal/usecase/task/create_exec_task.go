// Package task 是任务管理的应用服务层，负责创建和编排各种运维任务。
package task

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	agentdomain "gmha/internal/domain/agent"
	machinedomain "gmha/internal/domain/machine"
	taskdomain "gmha/internal/domain/task"
)

// MachineRepository 定义了机器仓储接口，支持按 IP 查询和列表查询。
type MachineRepository interface {
	GetByIP(ctx context.Context, ip string) (machinedomain.Machine, bool, error)
	List(ctx context.Context) ([]machinedomain.Machine, error)
}

// AgentRepository 定义了 Agent 仓储接口，用于查询 Agent 信息。
type AgentRepository interface {
	GetByMachineID(ctx context.Context, machineID string) (agentdomain.Agent, bool, error)
}

// CreateExecTaskRequest 是创建命令执行任务的请求参数。
type CreateExecTaskRequest struct {
	Machine         string
	Command         string
	Commands        []taskdomain.ExecCommandStep
	RollbackCommand string
	Operation       string
	DisplayName     string
	StepName        string
	Port            int
	PackageName     string
	TaskType        taskdomain.Type
}

// CreateExecTaskResult 是创建命令执行任务的结果，包含任务、步骤和事件。
type CreateExecTaskResult struct {
	Task   taskdomain.Task
	Steps  []taskdomain.Step
	Events []taskdomain.Event
}

// CreateExecTaskUsecase 是创建命令执行任务的用例，负责验证机器和 Agent 状态并构建任务。
type CreateExecTaskUsecase struct {
	machines MachineRepository
	agents   AgentRepository
}

// NewCreateExecTaskUsecase 创建一个新的命令执行任务用例实例。
func NewCreateExecTaskUsecase(machines MachineRepository, agents AgentRepository) *CreateExecTaskUsecase {
	return &CreateExecTaskUsecase{machines: machines, agents: agents}
}

// Execute 执行创建命令执行任务的流程，包括验证参数、解析机器和检查 Agent 状态。
func (u *CreateExecTaskUsecase) Execute(ctx context.Context, req CreateExecTaskRequest) (CreateExecTaskResult, error) {
	target := strings.TrimSpace(req.Machine)
	command := strings.TrimSpace(req.Command)
	if target == "" {
		return CreateExecTaskResult{}, errors.New("machine is required")
	}
	if command == "" && len(req.Commands) == 0 {
		return CreateExecTaskResult{}, errors.New("command is required")
	}
	for i := range req.Commands {
		req.Commands[i].Name = strings.TrimSpace(req.Commands[i].Name)
		req.Commands[i].Command = strings.TrimSpace(req.Commands[i].Command)
		if req.Commands[i].Name == "" || req.Commands[i].Command == "" {
			return CreateExecTaskResult{}, fmt.Errorf("workflow step %d requires name and command", i+1)
		}
	}

	machine, ok, err := u.resolveMachine(ctx, target)
	if err != nil {
		return CreateExecTaskResult{}, err
	}
	if !ok {
		return CreateExecTaskResult{}, fmt.Errorf("machine %s not found", target)
	}

	agent, ok, err := u.agents.GetByMachineID(ctx, machine.ID)
	if err != nil {
		return CreateExecTaskResult{}, err
	}
	if !ok {
		return CreateExecTaskResult{}, errors.New("agent not found")
	}
	if agent.State != agentdomain.StateOnline {
		return CreateExecTaskResult{}, fmt.Errorf("agent state %s does not allow task execution", agent.State)
	}

	now := time.Now().UTC()
	operation := strings.TrimSpace(req.Operation)
	displayName := strings.TrimSpace(req.DisplayName)
	stepName := strings.TrimSpace(req.StepName)
	if stepName == "" {
		stepName = "exec"
	}
	specJSON, _ := json.Marshal(taskdomain.ExecSpec{
		Command: command, Commands: req.Commands, RollbackCommand: strings.TrimSpace(req.RollbackCommand), Operation: operation, DisplayName: displayName, Port: req.Port, PackageName: strings.TrimSpace(req.PackageName),
	})
	taskID := fmt.Sprintf("task-%d", now.UnixNano())

	taskType := req.TaskType
	if taskType == "" {
		taskType = taskdomain.TypeExec
	}
	task := taskdomain.Task{
		ID:              taskID,
		Type:            taskType,
		MachineID:       machine.ID,
		AgentID:         agent.ID,
		Status:          taskdomain.StatusPending,
		ProgressPercent: 0,
		CurrentStep:     "等待派发",
		SpecJSON:        specJSON,
		CreatedAt:       now,
	}
	steps := make([]taskdomain.Step, 0, max(1, len(req.Commands)))
	if len(req.Commands) == 0 {
		steps = append(steps, taskdomain.Step{ID: fmt.Sprintf("task-step-%d-1", now.UnixNano()), TaskID: taskID, StepNo: 1, StepName: stepName, Status: taskdomain.StepPending, Message: "等待 Agent 接收任务"})
	} else {
		for i, item := range req.Commands {
			steps = append(steps, taskdomain.Step{ID: fmt.Sprintf("task-step-%d-%d", now.UnixNano(), i+1), TaskID: taskID, StepNo: i + 1, StepName: item.Name, Status: taskdomain.StepPending, Message: "等待 Agent 接收任务"})
		}
	}
	event := taskdomain.Event{
		ID:        fmt.Sprintf("task-event-%d", now.UnixNano()),
		TaskID:    taskID,
		StepID:    steps[0].ID,
		EventType: taskdomain.EventInfo,
		Content:   taskCreatedEvent(displayName),
		CreatedAt: now,
	}

	return CreateExecTaskResult{
		Task:   task,
		Steps:  steps,
		Events: []taskdomain.Event{event},
	}, nil
}

func taskCreatedEvent(displayName string) string {
	if displayName == "" {
		return "task created"
	}
	return displayName + "任务已创建"
}

// resolveMachine 根据选择器（ID、IP 或名称）解析机器信息。
func (u *CreateExecTaskUsecase) resolveMachine(ctx context.Context, selector string) (machinedomain.Machine, bool, error) {
	if item, ok, err := u.machines.GetByIP(ctx, selector); err != nil {
		return machinedomain.Machine{}, false, err
	} else if ok {
		return item, true, nil
	}
	items, err := u.machines.List(ctx)
	if err != nil {
		return machinedomain.Machine{}, false, err
	}
	for _, item := range items {
		if item.ID == selector || item.Name == selector {
			return item, true, nil
		}
	}
	return machinedomain.Machine{}, false, nil
}
