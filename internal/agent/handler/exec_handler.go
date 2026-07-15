package handler

import (
	"context"
	"fmt"
	"strings"
	"time"

	agentcore "gmha/internal/agent/core"
	taskdomain "gmha/internal/domain/task"
)

// ExecHandler 是命令执行任务处理器，负责在代理主机上执行 Shell 命令并上报执行结果。
type ExecHandler struct {
	managerHTTPAddr string
	taskType        string
}

// NewExecHandler 创建一个新的命令执行任务处理器实例。
func NewExecHandler(managerHTTPAddr ...string) *ExecHandler {
	addr := ""
	if len(managerHTTPAddr) > 0 {
		addr = strings.TrimRight(strings.TrimSpace(strings.Split(managerHTTPAddr[0], ",")[0]), "/")
	}
	return &ExecHandler{managerHTTPAddr: addr, taskType: string(taskdomain.TypeExec)}
}

// NewMySQLUpgradeHandler advertises the multi-step workflow capability
// separately so older Agents reject upgrades instead of treating them as a
// legacy one-command exec task.
func NewMySQLUpgradeHandler(managerHTTPAddr string) *ExecHandler {
	h := NewExecHandler(managerHTTPAddr)
	h.taskType = string(taskdomain.TypeMySQLUpgrade)
	return h
}

// Type 返回该处理器处理的任务类型。
func (h *ExecHandler) Type() string {
	return h.taskType
}

// Handle 执行命令任务，执行 Shell 命令并实时上报执行状态和输出。
func (h *ExecHandler) Handle(ctx context.Context, task taskdomain.DispatchTask, reporter *agentcore.Reporter) error {
	spec, err := agentcore.DecodeExecSpec(task)
	if err != nil {
		return err
	}
	commands := spec.Commands
	if len(commands) == 0 {
		commands = []taskdomain.ExecCommandStep{{Name: firstStep(task.Steps).StepName, Command: spec.Command}}
	}
	if len(commands) != len(task.Steps) {
		return fmt.Errorf("exec workflow step mismatch: task has %d steps, spec has %d commands", len(task.Steps), len(commands))
	}
	runner := agentcore.NewCommandRunner()
	for i, item := range commands {
		step := task.Steps[i]
		startedAt := time.Now().UTC()
		progress := i * 100 / len(commands)
		_ = reporter.Report(taskdomain.ReportEnvelope{TaskID: task.ID, Status: taskdomain.StatusRunning, Progress: progress, CurrentStep: step.StepName, Step: &taskdomain.StepReport{StepID: step.ID, StepNo: step.StepNo, StepName: step.StepName, Status: taskdomain.StepRunning, Message: "执行命令", StartedAt: &startedAt}})
		command := strings.ReplaceAll(item.Command, "__GMHA_MANAGER_URL__", h.managerHTTPAddr)
		output, runErr := runner.RunShell(ctx, task.ID, step.StepName, command)
		finishedAt := time.Now().UTC()
		content := joinOutput(output, "")
		if runErr != nil {
			if rollback := strings.TrimSpace(spec.RollbackCommand); rollback != "" {
				rollback = strings.ReplaceAll(rollback, "__GMHA_MANAGER_URL__", h.managerHTTPAddr)
				rollbackOutput, rollbackErr := runner.RunShell(ctx, task.ID, "自动回滚", rollback)
				content += "\n\n自动回滚:\n" + joinOutput(rollbackOutput, "")
				if rollbackErr != nil {
					content += "\n自动回滚失败: " + rollbackErr.Error()
				}
			}
			return reporter.Report(taskdomain.ReportEnvelope{TaskID: task.ID, Status: taskdomain.StatusFailed, Progress: 100, CurrentStep: step.StepName, Step: &taskdomain.StepReport{StepID: step.ID, StepNo: step.StepNo, StepName: step.StepName, Status: taskdomain.StepFailed, Message: content, StartedAt: &startedAt, FinishedAt: &finishedAt}, Event: &taskdomain.Event{TaskID: task.ID, StepID: step.ID, EventType: taskdomain.EventError, Content: content}, Error: fmt.Sprintf("exec failed: %v", runErr)})
		}
		status := taskdomain.StatusRunning
		if i == len(commands)-1 {
			status = taskdomain.StatusSuccess
		}
		if err := reporter.Report(taskdomain.ReportEnvelope{TaskID: task.ID, Status: status, Progress: (i + 1) * 100 / len(commands), CurrentStep: step.StepName, Step: &taskdomain.StepReport{StepID: step.ID, StepNo: step.StepNo, StepName: step.StepName, Status: taskdomain.StepSuccess, Message: content, StartedAt: &startedAt, FinishedAt: &finishedAt}, Event: &taskdomain.Event{TaskID: task.ID, StepID: step.ID, EventType: taskdomain.EventLog, Content: content}}); err != nil {
			return err
		}
	}
	return nil
}

func joinOutput(stdout, stderr string) string {
	parts := make([]string, 0, 2)
	if text := strings.TrimSpace(stdout); text != "" {
		parts = append(parts, text)
	}
	if text := strings.TrimSpace(stderr); text != "" {
		parts = append(parts, text)
	}
	if len(parts) == 0 {
		return "command completed with empty output"
	}
	return strings.Join(parts, "\n")
}

func firstStep(steps []taskdomain.DispatchStep) taskdomain.DispatchStep {
	if len(steps) == 0 {
		return taskdomain.DispatchStep{ID: "", StepNo: 1, StepName: "exec"}
	}
	return steps[0]
}
