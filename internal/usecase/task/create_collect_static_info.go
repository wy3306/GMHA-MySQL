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

// CreateCollectStaticInfoRequest 是创建静态信息采集任务的请求参数，包含 MySQL 连接信息。
type CreateCollectStaticInfoRequest struct {
	Machine       string
	MySQLPort     int
	MySQLSocket   string
	MySQLUsername string
	MySQLPassword string
}

// CreateCollectStaticInfoUsecase 是创建静态信息采集任务的用例。
type CreateCollectStaticInfoUsecase struct {
	machines MachineRepository
	agents   AgentRepository
}

// NewCreateCollectStaticInfoUsecase 创建一个新的静态信息采集任务用例实例。
func NewCreateCollectStaticInfoUsecase(machines MachineRepository, agents AgentRepository) *CreateCollectStaticInfoUsecase {
	return &CreateCollectStaticInfoUsecase{machines: machines, agents: agents}
}

// Execute 执行创建静态信息采集任务的流程，包括验证参数、解析机器和构建 MySQL 采集规格。
func (u *CreateCollectStaticInfoUsecase) Execute(ctx context.Context, req CreateCollectStaticInfoRequest) (CreateCollectMachineInfoResult, error) {
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
	if !ok || agent.State != agentdomain.StateOnline {
		return CreateCollectMachineInfoResult{}, errors.New("online agent is required for static collection")
	}
	if req.MySQLPort <= 0 {
		req.MySQLPort = 3306
	}
	if strings.TrimSpace(req.MySQLSocket) == "" {
		req.MySQLSocket = fmt.Sprintf("/data/%d/data/mysql.sock", req.MySQLPort)
	}
	if strings.TrimSpace(req.MySQLUsername) == "" {
		req.MySQLUsername = "monitor"
	}
	if strings.TrimSpace(req.MySQLPassword) == "" {
		req.MySQLPassword = "3306niubi"
	}
	specJSON, _ := json.Marshal(taskdomain.CollectStaticInfoSpec{
		MySQL: taskdomain.MySQLStaticCollectSpec{
			Enabled:  true,
			Port:     req.MySQLPort,
			Socket:   req.MySQLSocket,
			Username: req.MySQLUsername,
			Password: req.MySQLPassword,
		},
	})
	now := time.Now().UTC()
	taskID := fmt.Sprintf("task-%d", now.UnixNano())
	stepID := fmt.Sprintf("task-step-%d", now.UnixNano())
	task := taskdomain.Task{
		ID:          taskID,
		Type:        taskdomain.TypeCollectStaticInfo,
		MachineID:   machine.ID,
		AgentID:     agent.ID,
		Status:      taskdomain.StatusPending,
		CurrentStep: "等待派发",
		SpecJSON:    specJSON,
		CreatedAt:   now,
	}
	step := taskdomain.Step{ID: stepID, TaskID: taskID, StepNo: 1, StepName: "collect_static_info", Status: taskdomain.StepPending, Message: "等待 Agent 执行静态采集"}
	event := taskdomain.Event{ID: fmt.Sprintf("task-event-%d", now.UnixNano()), TaskID: taskID, StepID: stepID, EventType: taskdomain.EventInfo, Content: "collect_static_info task created", CreatedAt: now}
	return CreateCollectMachineInfoResult{Task: task, Steps: []taskdomain.Step{step}, Events: []taskdomain.Event{event}}, nil
}
