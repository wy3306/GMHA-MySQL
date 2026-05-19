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
	Machine string
	Command string
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
	if command == "" {
		return CreateExecTaskResult{}, errors.New("command is required")
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
	specJSON, _ := json.Marshal(taskdomain.ExecSpec{Command: command})
	taskID := fmt.Sprintf("task-%d", now.UnixNano())
	stepID := fmt.Sprintf("task-step-%d", now.UnixNano())

	task := taskdomain.Task{
		ID:              taskID,
		Type:            taskdomain.TypeExec,
		MachineID:       machine.ID,
		AgentID:         agent.ID,
		Status:          taskdomain.StatusPending,
		ProgressPercent: 0,
		CurrentStep:     "等待派发",
		SpecJSON:        specJSON,
		CreatedAt:       now,
	}
	step := taskdomain.Step{
		ID:       stepID,
		TaskID:   taskID,
		StepNo:   1,
		StepName: "exec",
		Status:   taskdomain.StepPending,
		Message:  "等待 Agent 接收任务",
	}
	event := taskdomain.Event{
		ID:        fmt.Sprintf("task-event-%d", now.UnixNano()),
		TaskID:    taskID,
		StepID:    stepID,
		EventType: taskdomain.EventInfo,
		Content:   "task created",
		CreatedAt: now,
	}

	return CreateExecTaskResult{
		Task:   task,
		Steps:  []taskdomain.Step{step},
		Events: []taskdomain.Event{event},
	}, nil
}

// resolveMachine 根据选择器（IP 或名称）解析机器信息。
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
		if item.Name == selector {
			return item, true, nil
		}
	}
	return machinedomain.Machine{}, false, nil
}
