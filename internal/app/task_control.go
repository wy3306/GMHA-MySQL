package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	taskdomain "gmha/internal/domain/task"
)

const (
	TaskControlSkip     = "skip"
	TaskControlRetry    = "retry"
	TaskControlRollback = "rollback"

	RollbackNotRequired  = "not_required"
	RollbackAutomatic    = "automatic"
	RollbackManual       = "manual"
	RollbackIrreversible = "irreversible"
	RollbackUnavailable  = "unavailable"
)

// TaskControlAction describes one task-center action without leaking an
// executable command to the browser.
type TaskControlAction struct {
	Allowed              bool   `json:"allowed"`
	Reason               string `json:"reason"`
	RequiresConfirmation bool   `json:"requires_confirmation,omitempty"`
	Confirmation         string `json:"confirmation,omitempty"`
}

// TaskControls is an authoritative server-side capability decision. The UI
// renders these values instead of guessing from a status or task type.
type TaskControls struct {
	Skip            TaskControlAction `json:"skip"`
	Retry           TaskControlAction `json:"retry"`
	Rollback        TaskControlAction `json:"rollback"`
	RollbackClass   string            `json:"rollback_class"`
	RollbackSummary string            `json:"rollback_summary"`
	Irreversible    bool              `json:"irreversible"`
}

type TaskControlRequest struct {
	TaskID       string `json:"task_id"`
	Action       string `json:"action"`
	Confirmation string `json:"confirmation,omitempty"`
}

type TaskControlResult struct {
	Action string     `json:"action"`
	Task   TaskDetail `json:"task"`
}

func (s *TaskService) taskControls(task taskdomain.Task, steps []taskdomain.Step, events []taskdomain.Event, hasChildren bool) TaskControls {
	controls := TaskControls{
		Skip:            TaskControlAction{Reason: "仅尚未下发且不属于编排流程的任务可跳过。"},
		Retry:           TaskControlAction{Reason: "仅支持从失败的 MySQL 安装任务中安全续跑。"},
		Rollback:        TaskControlAction{Reason: "该任务没有可由平台安全执行的回滚动作。"},
		RollbackClass:   RollbackUnavailable,
		RollbackSummary: "未声明可验证的回滚动作，需要按任务日志人工恢复。",
	}

	switch {
	case task.Status != taskdomain.StatusPending:
		controls.Skip.Reason = "任务已经下发或结束；当前 Agent 协议不支持安全中断运行中的命令。"
	case task.ParentTaskID != "":
		controls.Skip.Reason = "子任务由父级编排控制，不能单独跳过。"
	case hasChildren:
		controls.Skip.Reason = "父任务包含执行中的编排步骤，不能直接跳过。"
	default:
		controls.Skip = TaskControlAction{
			Allowed: true, Reason: "任务尚未下发，可以安全标记为跳过。",
			RequiresConfirmation: true, Confirmation: "SKIP " + task.ID,
		}
	}

	switch {
	case task.Status != taskdomain.StatusFailed:
		controls.Retry.Reason = "仅失败任务可以在修复问题后继续。"
	case task.ParentTaskID != "":
		controls.Retry.Reason = "子任务由父级编排控制，不能单独续跑。"
	case hasChildren:
		controls.Retry.Reason = "父任务包含编排子任务，不能从单个步骤续跑。"
	case task.Type != taskdomain.TypeMySQLInstall:
		controls.Retry.Reason = "该任务类型尚未声明步骤级幂等语义，不能安全续跑。"
	default:
		failedStep, ok := firstFailedTaskStep(steps)
		if !ok {
			controls.Retry.Reason = "任务没有可定位的失败步骤，不能确定安全续跑位置。"
			break
		}
		if compatible, reason := s.MachineCapability(task.MachineID, taskdomain.CapabilityTaskStepResumeV1); !compatible {
			controls.Retry.Reason = reason + "；升级并等待 Agent 在线后才可从失败步骤继续。"
			break
		}
		controls.Retry = TaskControlAction{
			Allowed:              true,
			Reason:               fmt.Sprintf("保留已成功步骤，从“%s”重新执行失败步骤及后续步骤。", failedStep.StepName),
			RequiresConfirmation: true,
			Confirmation:         "RETRY " + task.ID,
		}
	}

	switch task.Type {
	case taskdomain.TypeCollectMachineInfo, taskdomain.TypeCollectStaticInfo, taskdomain.TypeFlameGraph:
		controls.RollbackClass = RollbackNotRequired
		controls.RollbackSummary = "该任务只采集或分析数据，不修改目标主机，无需回滚。"
		controls.Rollback.Reason = controls.RollbackSummary
		return controls
	case taskdomain.TypeMySQLUninstall:
		controls.RollbackClass = RollbackIrreversible
		controls.RollbackSummary = "卸载可能删除实例目录和数据，不能一键回滚；必须重新安装并从已验证备份恢复。"
		controls.Rollback.Reason = controls.RollbackSummary
		controls.Irreversible = true
		return controls
	case taskdomain.TypeMySQLInstall:
		controls.RollbackClass = RollbackManual
		controls.RollbackSummary = "安装可能已创建用户、目录、配置和数据；平台不会自动删除这些内容，请使用显式卸载流程并先核对数据。"
		controls.Rollback.Reason = controls.RollbackSummary
		return controls
	case taskdomain.TypeMySQLTopology, taskdomain.TypeArchitecture, taskdomain.TypeClusterBootstrap, taskdomain.TypeMySQLClusterUpgrade:
		controls.RollbackClass = RollbackManual
		controls.RollbackSummary = "该任务涉及多个节点或数据库状态，不能用单条命令安全回滚；请按任务日志和备份恢复计划处理。"
		controls.Rollback.Reason = controls.RollbackSummary
		return controls
	}

	var spec taskdomain.ExecSpec
	if task.Type == taskdomain.TypeExec || task.Type == taskdomain.TypeMySQLUpgrade {
		_ = json.Unmarshal(task.SpecJSON, &spec)
	}
	if isReadOnlyTaskOperation(spec.Operation) {
		controls.RollbackClass = RollbackNotRequired
		controls.RollbackSummary = "该任务是检查或采集操作，不修改受管资源，无需回滚。"
		controls.Rollback.Reason = controls.RollbackSummary
		return controls
	}
	if strings.TrimSpace(spec.RollbackCommand) == "" || spec.RollbackCommand == "[redacted after execution]" {
		return controls
	}

	controls.RollbackClass = RollbackAutomatic
	controls.RollbackSummary = "任务失败时 Agent 会自动执行受保护的恢复动作；只有自动恢复失败后才允许人工重试。"
	controls.Rollback.Reason = "自动恢复尚未失败，无需人工重试。"
	if task.Status != taskdomain.StatusFailed {
		controls.Rollback.Reason = "仅失败任务可以重试恢复动作。"
		return controls
	}
	result := rollbackResult(events)
	switch result {
	case "success":
		controls.Rollback.Reason = "Agent 已完成受保护恢复动作；该动作可能因安全保护而保留当前版本，请核对日志，无需盲目重复执行。"
	case "failed":
		controls.Rollback = TaskControlAction{
			Allowed: true, Reason: "自动恢复失败，可以重试任务声明的受保护恢复动作。",
			RequiresConfirmation: true, Confirmation: "ROLLBACK " + task.ID,
		}
	default:
		controls.Rollback.Reason = "没有收到可信的自动恢复结果，平台不会盲目执行回滚。"
	}
	return controls
}

func isReadOnlyTaskOperation(operation string) bool {
	value := strings.ToLower(strings.TrimSpace(operation))
	for _, marker := range []string{"collect", "inspection", "precheck", "check", "probe", "diagnos", "analy", "flamegraph"} {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return false
}

func rollbackResult(events []taskdomain.Event) string {
	for i := len(events) - 1; i >= 0; i-- {
		content := events[i].Content
		switch {
		case strings.Contains(content, "GMHA_ROLLBACK_RESULT=failed"), strings.Contains(content, "自动回滚失败"):
			return "failed"
		case strings.Contains(content, "GMHA_ROLLBACK_RESULT=success"):
			return "success"
		case strings.Contains(content, "自动回滚:"):
			// Backward compatibility for reports created before the structured
			// marker was introduced. A failed legacy report is matched above.
			return "success"
		}
	}
	return ""
}

// ControlTask applies a capability checked task-center action.
func (s *TaskService) ControlTask(ctx context.Context, req TaskControlRequest) (TaskControlResult, error) {
	req.TaskID = strings.TrimSpace(req.TaskID)
	req.Action = strings.ToLower(strings.TrimSpace(req.Action))
	if req.TaskID == "" {
		return TaskControlResult{}, errors.New("task_id is required")
	}
	switch req.Action {
	case TaskControlSkip:
		detail, err := s.skipTask(ctx, req.TaskID, req.Confirmation)
		return TaskControlResult{Action: req.Action, Task: detail}, err
	case TaskControlRetry:
		detail, err := s.retryFailedTask(ctx, req.TaskID, req.Confirmation)
		return TaskControlResult{Action: req.Action, Task: detail}, err
	case TaskControlRollback:
		detail, err := s.retryTaskRollback(ctx, req.TaskID, req.Confirmation)
		return TaskControlResult{Action: req.Action, Task: detail}, err
	default:
		return TaskControlResult{}, fmt.Errorf("unsupported task action %q", req.Action)
	}
}

func (s *TaskService) skipTask(ctx context.Context, taskID, confirmation string) (TaskDetail, error) {
	s.controlMu.Lock()
	defer s.controlMu.Unlock()

	task, ok, err := s.repo.GetTask(ctx, taskID)
	if err != nil {
		return TaskDetail{}, err
	}
	if !ok {
		return TaskDetail{}, errors.New("task not found")
	}
	events, err := s.repo.ListEvents(ctx, taskID, -1)
	if err != nil {
		return TaskDetail{}, err
	}
	hasChildren := false
	if repo, ok := s.repo.(childTaskRepository); ok {
		children, listErr := repo.ListChildTasks(ctx, taskID)
		if listErr != nil {
			return TaskDetail{}, listErr
		}
		hasChildren = len(children) > 0
	}
	steps, err := s.repo.ListSteps(ctx, taskID)
	if err != nil {
		return TaskDetail{}, err
	}
	action := s.taskControls(task, steps, events, hasChildren).Skip
	if !action.Allowed {
		return TaskDetail{}, errors.New(action.Reason)
	}
	if strings.TrimSpace(confirmation) != action.Confirmation {
		return TaskDetail{}, fmt.Errorf("confirmation must equal %q", action.Confirmation)
	}

	now := time.Now().UTC()
	for i := range steps {
		if steps[i].Status == taskdomain.StepSuccess || steps[i].Status == taskdomain.StepFailed || steps[i].Status == taskdomain.StepSkipped {
			continue
		}
		steps[i].Status = taskdomain.StepSkipped
		steps[i].Message = "由用户在任务中心跳过，未下发到 Agent。"
		steps[i].FinishedAt = &now
		if err := s.repo.UpdateStep(ctx, steps[i]); err != nil {
			return TaskDetail{}, err
		}
	}
	task.Status = taskdomain.StatusSkipped
	task.ProgressPercent = 100
	task.CurrentStep = "已跳过"
	task.FinishedAt = &now
	if task.Type == taskdomain.TypeExec || task.Type == taskdomain.TypeMySQLUpgrade {
		var spec taskdomain.ExecSpec
		if json.Unmarshal(task.SpecJSON, &spec) == nil {
			spec.Command = "[redacted after skip]"
			for i := range spec.Commands {
				spec.Commands[i].Command = "[redacted after skip]"
			}
			if spec.RollbackCommand != "" {
				spec.RollbackCommand = "[redacted after skip]"
			}
			task.SpecJSON, _ = json.Marshal(spec)
		}
	}
	if err := s.repo.UpdateTask(ctx, task); err != nil {
		return TaskDetail{}, err
	}
	if err := s.repo.AppendEvent(ctx, taskdomain.Event{
		ID: fmt.Sprintf("task-event-%d", now.UnixNano()), TaskID: taskID,
		EventType: taskdomain.EventInfo, Content: "用户在任务中心跳过任务；任务未下发到 Agent。", CreatedAt: now,
	}); err != nil {
		return TaskDetail{}, err
	}
	return s.GetTaskDetail(ctx, taskID)
}

func firstFailedTaskStep(steps []taskdomain.Step) (taskdomain.Step, bool) {
	for _, step := range steps {
		if step.Status == taskdomain.StepFailed {
			return step, true
		}
	}
	return taskdomain.Step{}, false
}

func taskRetryAttempt(events []taskdomain.Event) int {
	attempt := 0
	for _, event := range events {
		if strings.Contains(event.Content, "GMHA_TASK_RETRY_ATTEMPT=") {
			attempt++
		}
	}
	return attempt + 1
}

func (s *TaskService) retryFailedTask(ctx context.Context, taskID, confirmation string) (TaskDetail, error) {
	var failedStep taskdomain.Step
	err := func() error {
		s.controlMu.Lock()
		defer s.controlMu.Unlock()

		task, ok, err := s.repo.GetTask(ctx, taskID)
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("task not found")
		}
		steps, err := s.repo.ListSteps(ctx, taskID)
		if err != nil {
			return err
		}
		events, err := s.repo.ListEvents(ctx, taskID, -1)
		if err != nil {
			return err
		}
		hasChildren := false
		if repo, ok := s.repo.(childTaskRepository); ok {
			children, listErr := repo.ListChildTasks(ctx, taskID)
			if listErr != nil {
				return listErr
			}
			hasChildren = len(children) > 0
		}
		action := s.taskControls(task, steps, events, hasChildren).Retry
		if !action.Allowed {
			return errors.New(action.Reason)
		}
		if strings.TrimSpace(confirmation) != action.Confirmation {
			return fmt.Errorf("confirmation must equal %q", action.Confirmation)
		}
		failedStep, ok = firstFailedTaskStep(steps)
		if !ok {
			return errors.New("task has no failed step")
		}

		successful := 0
		for i := range steps {
			if steps[i].Status == taskdomain.StepSuccess {
				successful++
				continue
			}
			steps[i].Status = taskdomain.StepPending
			steps[i].StartedAt = nil
			steps[i].FinishedAt = nil
			if steps[i].ID == failedStep.ID {
				steps[i].Message = "等待修复后重新执行"
			} else {
				steps[i].Message = "等待前序步骤完成"
			}
			if err := s.repo.UpdateStep(ctx, steps[i]); err != nil {
				return err
			}
		}
		task.Status = taskdomain.StatusPending
		task.ProgressPercent = 0
		if len(steps) > 0 {
			task.ProgressPercent = successful * 100 / len(steps)
		}
		task.CurrentStep = "等待从 " + failedStep.StepName + " 继续"
		task.FinishedAt = nil
		if err := s.repo.UpdateTask(ctx, task); err != nil {
			return err
		}
		now := time.Now().UTC()
		attempt := taskRetryAttempt(events)
		return s.repo.AppendEvent(ctx, taskdomain.Event{
			ID:        fmt.Sprintf("task-event-%d", now.UnixNano()),
			TaskID:    taskID,
			StepID:    failedStep.ID,
			EventType: taskdomain.EventInfo,
			Content:   fmt.Sprintf("用户确认问题已修复，从步骤 %s 继续；GMHA_TASK_RETRY_ATTEMPT=%d", failedStep.StepName, attempt),
			CreatedAt: now,
		})
	}()
	if err != nil {
		return TaskDetail{}, err
	}

	// Dispatch is deliberately attempted after releasing controlMu. If the
	// Agent disconnects in this small window, the task remains pending and the
	// normal dispatcher will send it after reconnection.
	_ = s.tryDispatchPendingTask(ctx, taskID)
	return s.GetTaskDetail(ctx, taskID)
}

func (s *TaskService) retryTaskRollback(ctx context.Context, taskID, confirmation string) (TaskDetail, error) {
	task, ok, err := s.repo.GetTask(ctx, taskID)
	if err != nil {
		return TaskDetail{}, err
	}
	if !ok {
		return TaskDetail{}, errors.New("task not found")
	}
	events, err := s.repo.ListEvents(ctx, taskID, -1)
	if err != nil {
		return TaskDetail{}, err
	}
	steps, err := s.repo.ListSteps(ctx, taskID)
	if err != nil {
		return TaskDetail{}, err
	}
	action := s.taskControls(task, steps, events, false).Rollback
	if !action.Allowed {
		return TaskDetail{}, errors.New(action.Reason)
	}
	if strings.TrimSpace(confirmation) != action.Confirmation {
		return TaskDetail{}, fmt.Errorf("confirmation must equal %q", action.Confirmation)
	}
	var spec taskdomain.ExecSpec
	if err := json.Unmarshal(task.SpecJSON, &spec); err != nil || strings.TrimSpace(spec.RollbackCommand) == "" {
		return TaskDetail{}, errors.New("task rollback specification is unavailable")
	}
	name := strings.TrimSpace(spec.DisplayName)
	if name == "" {
		name = task.ID
	}
	return s.CreateExecTaskWithOptions(ctx, task.MachineID, spec.RollbackCommand, ExecTaskOptions{
		Operation:   "manual_rollback",
		DisplayName: "恢复重试 · " + name,
		StepName:    "retry_rollback",
		Port:        spec.Port,
		TaskType:    taskdomain.TypeExec,
	})
}
