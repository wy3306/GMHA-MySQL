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
type ExecHandler struct{}

// NewExecHandler 创建一个新的命令执行任务处理器实例。
func NewExecHandler() *ExecHandler {
	return &ExecHandler{}
}

// Type 返回该处理器处理的任务类型。
func (h *ExecHandler) Type() string {
	return string(taskdomain.TypeExec)
}

// Handle 执行命令任务，执行 Shell 命令并实时上报执行状态和输出。
func (h *ExecHandler) Handle(ctx context.Context, task taskdomain.DispatchTask, reporter *agentcore.Reporter) error {
	spec, err := agentcore.DecodeExecSpec(task)
	if err != nil {
		return err
	}
	step := firstStep(task.Steps)
	startedAt := time.Now().UTC()
	_ = reporter.Report(taskdomain.ReportEnvelope{
		TaskID:      task.ID,
		Status:      taskdomain.StatusRunning,
		Progress:    0,
		CurrentStep: step.StepName,
		Step: &taskdomain.StepReport{
			StepID:    step.ID,
			StepNo:    step.StepNo,
			StepName:  step.StepName,
			Status:    taskdomain.StepRunning,
			Message:   "执行命令",
			StartedAt: &startedAt,
		},
	})

	output, err := agentcore.NewCommandRunner().RunShell(ctx, task.ID, step.StepName, spec.Command)
	if err != nil {
		content := joinOutput(output, "")
		finishedAt := time.Now().UTC()
		return reporter.Report(taskdomain.ReportEnvelope{
			TaskID:      task.ID,
			Status:      taskdomain.StatusFailed,
			Progress:    100,
			CurrentStep: step.StepName,
			Step: &taskdomain.StepReport{
				StepID:     step.ID,
				StepNo:     step.StepNo,
				StepName:   step.StepName,
				Status:     taskdomain.StepFailed,
				Message:    content,
				StartedAt:  &startedAt,
				FinishedAt: &finishedAt,
			},
			Event: &taskdomain.Event{
				TaskID:    task.ID,
				StepID:    step.ID,
				EventType: taskdomain.EventError,
				Content:   content,
			},
			Error: fmt.Sprintf("exec failed: %v", err),
		})
	}

	output = joinOutput(output, "")
	finishedAt := time.Now().UTC()
	return reporter.Report(taskdomain.ReportEnvelope{
		TaskID:      task.ID,
		Status:      taskdomain.StatusSuccess,
		Progress:    100,
		CurrentStep: step.StepName,
		Step: &taskdomain.StepReport{
			StepID:     step.ID,
			StepNo:     step.StepNo,
			StepName:   step.StepName,
			Status:     taskdomain.StepSuccess,
			Message:    output,
			StartedAt:  &startedAt,
			FinishedAt: &finishedAt,
		},
		Event: &taskdomain.Event{
			TaskID:    task.ID,
			StepID:    step.ID,
			EventType: taskdomain.EventLog,
			Content:   output,
		},
	})
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
