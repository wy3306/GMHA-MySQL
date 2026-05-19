package task

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	agentdomain "gmha/internal/domain/agent"
	taskdomain "gmha/internal/domain/task"
)

// CreateCollectMachineInfoRequest 是创建机器信息采集任务的请求参数。
type CreateCollectMachineInfoRequest struct {
	Machine string
}

// CreateCollectMachineInfoResult 是创建机器信息采集任务的结果。
type CreateCollectMachineInfoResult struct {
	Task   taskdomain.Task
	Steps  []taskdomain.Step
	Events []taskdomain.Event
}

// CreateCollectMachineInfoUsecase 是创建机器信息采集任务的用例。
type CreateCollectMachineInfoUsecase struct {
	machines MachineRepository
	agents   AgentRepository
}

// NewCreateCollectMachineInfoUsecase 创建一个新的机器信息采集任务用例实例。
func NewCreateCollectMachineInfoUsecase(machines MachineRepository, agents AgentRepository) *CreateCollectMachineInfoUsecase {
	return &CreateCollectMachineInfoUsecase{machines: machines, agents: agents}
}

// Execute 执行创建机器信息采集任务的流程。
func (u *CreateCollectMachineInfoUsecase) Execute(ctx context.Context, req CreateCollectMachineInfoRequest) (CreateCollectMachineInfoResult, error) {
	target := strings.TrimSpace(req.Machine)
	if target == "" {
		return CreateCollectMachineInfoResult{}, errors.New("machine is required")
	}
	machine, ok, err := (&CreateExecTaskUsecase{machines: u.machines, agents: u.agents}).resolveMachine(ctx, target)
	if err != nil {
		return CreateCollectMachineInfoResult{}, err
	}
	if !ok {
		return CreateCollectMachineInfoResult{}, fmt.Errorf("machine %s not found", target)
	}
	agent, ok, err := u.agents.GetByMachineID(ctx, machine.ID)
	if err != nil {
		return CreateCollectMachineInfoResult{}, err
	}
	if !ok {
		return CreateCollectMachineInfoResult{}, errors.New("agent not found")
	}
	if agent.State != agentdomain.StateOnline {
		return CreateCollectMachineInfoResult{}, fmt.Errorf("agent state %s does not allow collection", agent.State)
	}

	now := time.Now().UTC()
	specJSON, _ := json.Marshal(taskdomain.CollectMachineInfoSpec{})
	taskID := fmt.Sprintf("task-%d", now.UnixNano())
	stepID := fmt.Sprintf("task-step-%d", now.UnixNano())
	task := taskdomain.Task{
		ID:              taskID,
		Type:            taskdomain.TypeCollectMachineInfo,
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
		StepName: "collect_machine_info",
		Status:   taskdomain.StepPending,
		Message:  "等待 Agent 执行采集",
	}
	event := taskdomain.Event{
		ID:        fmt.Sprintf("task-event-%d", now.UnixNano()),
		TaskID:    taskID,
		StepID:    stepID,
		EventType: taskdomain.EventInfo,
		Content:   "collect_machine_info task created",
		CreatedAt: now,
	}
	return CreateCollectMachineInfoResult{
		Task:   task,
		Steps:  []taskdomain.Step{step},
		Events: []taskdomain.Event{event},
	}, nil
}
