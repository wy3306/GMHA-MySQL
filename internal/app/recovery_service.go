package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	agentdomain "gmha/internal/domain/agent"
	hbdomain "gmha/internal/domain/heartbeat"
	machinedomain "gmha/internal/domain/machine"
	recoverydomain "gmha/internal/domain/recovery"
)

// RecoveryExecutor 定义了恢复执行器的接口，通过 SSH 检查和控制 Agent 服务。
type RecoveryExecutor interface {
	Inspect(ctx context.Context, endpoint machinedomain.Endpoint, auth machinedomain.SSHAuth) (string, error)
	Start(ctx context.Context, endpoint machinedomain.Endpoint, auth machinedomain.SSHAuth) (string, error)
	Restart(ctx context.Context, endpoint machinedomain.Endpoint, auth machinedomain.SSHAuth) (string, error)
}

// RecoveryService 是自动恢复服务，负责扫描离线 Agent、创建恢复任务、
// 通过 SSH 检查和重启 Agent 服务、等待心跳恢复。支持冷却抑制机制。
type RecoveryService struct {
	repo        recoverydomain.Repository
	machineRepo machinedomain.Repository
	agentRepo   agentdomain.Repository
	heartbeat   *HeartbeatService
	executor    RecoveryExecutor
}

type RecoveryConfig struct {
	ScanInterval        time.Duration
	ConfirmWindow       time.Duration
	WaitHeartbeat       time.Duration
	MinAutoRecoverEvery time.Duration
	SuppressAfterFails  int
	SuppressFor         time.Duration
	MaxRecentTasks      int
}

type RecoveryView struct {
	MachineID     string `json:"machine_id"`
	MachineIP     string `json:"machine_ip"`
	Status        string `json:"status"`
	Trigger       string `json:"trigger"`
	Action        string `json:"action"`
	Attempt       int    `json:"attempt"`
	LastError     string `json:"last_error"`
	LastSSHOutput string `json:"last_ssh_output"`
	CreatedAt     string `json:"created_at"`
}

func NewRecoveryService(repo recoverydomain.Repository, machineRepo machinedomain.Repository, agentRepo agentdomain.Repository, heartbeat *HeartbeatService, executor RecoveryExecutor) *RecoveryService {
	return &RecoveryService{repo: repo, machineRepo: machineRepo, agentRepo: agentRepo, heartbeat: heartbeat, executor: executor}
}

func (s *RecoveryService) config() RecoveryConfig {
	return RecoveryConfig{
		ScanInterval:        10 * time.Second,
		ConfirmWindow:       3 * time.Second,
		WaitHeartbeat:       20 * time.Second,
		MinAutoRecoverEvery: 2 * time.Minute,
		SuppressAfterFails:  3,
		SuppressFor:         15 * time.Minute,
		MaxRecentTasks:      20,
	}
}

func (s *RecoveryService) ListRecent(ctx context.Context, limit int) ([]RecoveryView, error) {
	if limit <= 0 {
		limit = s.config().MaxRecentTasks
	}
	items, err := s.repo.ListRecent(ctx, limit)
	if err != nil {
		return nil, err
	}
	out := make([]RecoveryView, 0, len(items))
	for _, item := range items {
		out = append(out, RecoveryView{
			MachineID:     item.MachineID,
			MachineIP:     item.MachineIP,
			Status:        string(item.Status),
			Trigger:       string(item.Trigger),
			Action:        string(item.Action),
			Attempt:       item.Attempt,
			LastError:     item.LastError,
			LastSSHOutput: item.LastSSHOutput,
			CreatedAt:     item.CreatedAt.Format("2006-01-02 15:04:05"),
		})
	}
	return out, nil
}

func (s *RecoveryService) LatestSnapshot(ctx context.Context) (map[string]recoverydomain.LatestState, error) {
	items, err := s.repo.ListLatestStates(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]recoverydomain.LatestState, len(items))
	for _, item := range items {
		out[item.MachineID] = item
	}
	return out, nil
}

func (s *RecoveryService) TriggerManualRecoverByIP(ctx context.Context, ip string) (RecoveryView, error) {
	machine, ok, err := s.machineRepo.GetByIP(ctx, ip)
	if err != nil {
		return RecoveryView{}, err
	}
	if !ok {
		return RecoveryView{}, errors.New("machine not found")
	}
	task := recoverydomain.Task{
		ID:          fmt.Sprintf("recovery-%d", time.Now().UnixNano()),
		MachineID:   machine.ID,
		MachineIP:   machine.IP,
		Status:      recoverydomain.StatusPending,
		Trigger:     recoverydomain.TriggerManual,
		Action:      recoverydomain.ActionNone,
		Attempt:     1,
		MaxAttempts: 3,
	}
	if agent, ok, _ := s.agentRepo.GetByMachineID(ctx, machine.ID); ok {
		task.AgentID = agent.ID
	}
	task, err = s.repo.CreateTask(ctx, task)
	if err != nil {
		return RecoveryView{}, err
	}
	if err := s.ExecuteTask(ctx, task); err != nil {
		return RecoveryView{}, err
	}
	return RecoveryView{
		MachineID:     task.MachineID,
		MachineIP:     task.MachineIP,
		Status:        string(task.Status),
		Trigger:       string(task.Trigger),
		Action:        string(task.Action),
		Attempt:       task.Attempt,
		LastError:     task.LastError,
		LastSSHOutput: task.LastSSHOutput,
		CreatedAt:     task.CreatedAt.Format("2006-01-02 15:04:05"),
	}, nil
}

func (s *RecoveryService) ExecuteTask(ctx context.Context, task recoverydomain.Task) error {
	cfg := s.config()
	lockUntil := time.Now().Add(2 * time.Minute)
	locked, err := s.repo.TryAcquireLock(ctx, task.MachineID, lockUntil)
	if err != nil {
		return err
	}
	if !locked {
		return errors.New("recovery already in progress")
	}
	defer s.repo.ReleaseLock(ctx, task.MachineID)

	state, _, _ := s.repo.GetLatestState(ctx, task.MachineID)
	if state.SuppressedUntil != nil && state.SuppressedUntil.After(time.Now()) {
		task.Status = recoverydomain.StatusSuppressed
		task.LastError = "recovery suppressed by cooldown"
		_ = s.repo.UpdateTask(ctx, task)
		return errors.New(task.LastError)
	}
	state.MachineID = task.MachineID
	state.InProgress = true
	state.LastAttemptAt = ptrTime(time.Now())
	state.LastTaskID = task.ID
	state.LastResult = "executing"
	_ = s.repo.SaveLatestState(ctx, state)

	task.Status = recoverydomain.StatusConfirming
	_ = s.repo.UpdateTask(ctx, task)
	time.Sleep(cfg.ConfirmWindow)
	if hb, ok, _ := s.heartbeat.GetByMachineID(ctx, task.MachineID); ok {
		if hb.CurrentState == hbdomain.StateOnline || hb.CurrentState == hbdomain.StateDegraded {
			task.Status = recoverydomain.StatusSucceeded
			task.LastError = ""
			task.LastSSHOutput = "already recovered during confirmation window"
			_ = s.repo.UpdateTask(ctx, task)
			return s.repo.SaveLatestState(ctx, recoverydomain.LatestState{
				MachineID:     task.MachineID,
				InProgress:    false,
				LastSuccessAt: ptrTime(time.Now()),
				LastTaskID:    task.ID,
				LastResult:    "already recovered",
			})
		}
	}

	machine, ok, err := s.machineRepo.GetByID(ctx, task.MachineID)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("machine not found")
	}
	endpoint := machinedomain.Endpoint{IP: machine.IP, SSHPort: machine.SSHPort}
	auth := machinedomain.SSHAuth{User: machine.SSHUser}

	task.Status = recoverydomain.StatusExecuting
	_ = s.repo.UpdateTask(ctx, task)
	inspectOut, err := s.executor.Inspect(ctx, endpoint, auth)
	if err != nil {
		task.Status = recoverydomain.StatusFailed
		task.LastError = err.Error()
		task.LastSSHOutput = inspectOut
		_ = s.repo.UpdateTask(ctx, task)
		return s.markFailed(ctx, task)
	}

	action := recoverydomain.ActionRestart
	if containsAny(inspectOut, "inactive", "failed") {
		action = recoverydomain.ActionStart
	}
	task.Action = action
	_ = s.repo.UpdateTask(ctx, task)

	var execOut string
	if action == recoverydomain.ActionStart {
		execOut, err = s.executor.Start(ctx, endpoint, auth)
	} else {
		execOut, err = s.executor.Restart(ctx, endpoint, auth)
	}
	if err != nil {
		task.Status = recoverydomain.StatusFailed
		task.LastError = err.Error()
		task.LastSSHOutput = execOut
		_ = s.repo.UpdateTask(ctx, task)
		return s.markFailed(ctx, task)
	}

	task.Status = recoverydomain.StatusWaitingHeartbeat
	task.LastSSHOutput = execOut
	deadline := time.Now().Add(cfg.WaitHeartbeat)
	task.HeartbeatDeadline = &deadline
	_ = s.repo.UpdateTask(ctx, task)

	waitCtx, cancel := context.WithTimeout(ctx, cfg.WaitHeartbeat)
	defer cancel()
	if err := s.heartbeat.WaitForOnline(waitCtx, task.MachineID, cfg.WaitHeartbeat); err != nil {
		task.Status = recoverydomain.StatusFailed
		task.LastError = err.Error()
		_ = s.repo.UpdateTask(ctx, task)
		return s.markFailed(ctx, task)
	}

	task.Status = recoverydomain.StatusSucceeded
	task.LastError = ""
	_ = s.repo.UpdateTask(ctx, task)
	return s.repo.SaveLatestState(ctx, recoverydomain.LatestState{
		MachineID:           task.MachineID,
		InProgress:          false,
		LastAttemptAt:       ptrTime(time.Now()),
		LastSuccessAt:       ptrTime(time.Now()),
		ConsecutiveFailures: 0,
		LastTaskID:          task.ID,
		LastResult:          "recovered",
	})
}

func (s *RecoveryService) markFailed(ctx context.Context, task recoverydomain.Task) error {
	state, _, _ := s.repo.GetLatestState(ctx, task.MachineID)
	cfg := s.config()
	state.MachineID = task.MachineID
	state.InProgress = false
	state.LastAttemptAt = ptrTime(time.Now())
	state.ConsecutiveFailures++
	state.LastTaskID = task.ID
	state.LastResult = task.LastError
	if state.ConsecutiveFailures >= cfg.SuppressAfterFails {
		until := time.Now().Add(cfg.SuppressFor)
		state.SuppressedUntil = &until
	}
	return s.repo.SaveLatestState(ctx, state)
}

func (s *RecoveryService) TickInterval() time.Duration {
	return s.config().ScanInterval
}

func (s *RecoveryService) ScanAndRecover(ctx context.Context) error {
	if s == nil || s.heartbeat == nil {
		return nil
	}
	machines, err := s.machineRepo.List(ctx)
	if err != nil {
		return err
	}
	for _, machine := range machines {
		if strings.TrimSpace(machine.Cluster) == "" {
			continue
		}
		hb, ok, err := s.heartbeat.GetByMachineID(ctx, machine.ID)
		if err != nil || !ok {
			continue
		}
		if hb.CurrentState != hbdomain.StateOffline {
			continue
		}
		state, found, err := s.repo.GetLatestState(ctx, machine.ID)
		if err != nil {
			return err
		}
		if found {
			if state.InProgress {
				continue
			}
			if state.SuppressedUntil != nil && state.SuppressedUntil.After(time.Now()) {
				continue
			}
			if state.LastAttemptAt != nil && state.LastAttemptAt.Add(s.config().MinAutoRecoverEvery).After(time.Now()) {
				continue
			}
		}

		task := recoverydomain.Task{
			ID:          fmt.Sprintf("recovery-%d", time.Now().UnixNano()),
			MachineID:   machine.ID,
			MachineIP:   machine.IP,
			Status:      recoverydomain.StatusPending,
			Trigger:     recoverydomain.TriggerOfflineAuto,
			Action:      recoverydomain.ActionNone,
			Attempt:     state.ConsecutiveFailures + 1,
			MaxAttempts: s.config().SuppressAfterFails,
			Reason:      "agent heartbeat offline, auto recovery requested",
		}
		if agent, ok, _ := s.agentRepo.GetByMachineID(ctx, machine.ID); ok {
			task.AgentID = agent.ID
		}
		task, err = s.repo.CreateTask(ctx, task)
		if err != nil {
			return err
		}
		if err := s.ExecuteTask(ctx, task); err != nil {
			continue
		}
	}
	return nil
}

func ptrTime(t time.Time) *time.Time { return &t }

func containsAny(s string, terms ...string) bool {
	for _, term := range terms {
		if strings.Contains(strings.ToLower(s), strings.ToLower(term)) {
			return true
		}
	}
	return false
}
