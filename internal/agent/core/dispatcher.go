package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	taskdomain "gmha/internal/domain/task"
)

// TaskHandler 定义任务处理器接口，每种任务类型对应一个处理器实现。
type TaskHandler interface {
	Type() string
	Handle(ctx context.Context, task taskdomain.DispatchTask, reporter *Reporter) error
}

// Dispatcher 是任务分发器，根据任务类型将任务路由到对应的处理器执行。
type Dispatcher struct {
	mu       sync.RWMutex
	handlers map[string]TaskHandler
}

// NewDispatcher 创建一个新的任务分发器，并注册传入的处理器。
func NewDispatcher(handlers ...TaskHandler) *Dispatcher {
	d := &Dispatcher{handlers: make(map[string]TaskHandler)}
	for _, handler := range handlers {
		d.Register(handler)
	}
	return d
}

// Register 注册一个任务处理器，按处理器类型进行索引。
func (d *Dispatcher) Register(handler TaskHandler) {
	if handler == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.handlers[handler.Type()] = handler
}

// Types 返回所有已注册的任务类型列表。
func (d *Dispatcher) Types() []string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	out := make([]string, 0, len(d.handlers))
	for typ := range d.handlers {
		out = append(out, typ)
	}
	sort.Strings(out)
	return out
}

// Dispatch 根据任务类型查找对应处理器并执行任务，同时上报任务开始状态。
func (d *Dispatcher) Dispatch(ctx context.Context, envelope taskdomain.DispatchEnvelope, reporter *Reporter) error {
	d.mu.RLock()
	handler, ok := d.handlers[envelope.Task.Type]
	d.mu.RUnlock()
	if !ok {
		return d.reportFailure(reporter, envelope.Task, fmt.Errorf("unsupported task type %s", envelope.Task.Type))
	}

	startedAt := time.Now().UTC()
	firstStep := firstStep(envelope.Task.Steps)
	_ = reporter.Report(taskdomain.ReportEnvelope{
		TaskID:      envelope.Task.ID,
		Status:      taskdomain.StatusRunning,
		Progress:    0,
		CurrentStep: firstStep.StepName,
		Step: &taskdomain.StepReport{
			StepID:    firstStep.ID,
			StepNo:    firstStep.StepNo,
			StepName:  firstStep.StepName,
			Status:    taskdomain.StepRunning,
			Message:   "task accepted by agent",
			StartedAt: &startedAt,
		},
		Event: &taskdomain.Event{
			TaskID:    envelope.Task.ID,
			StepID:    firstStep.ID,
			EventType: taskdomain.EventInfo,
			Content:   "agent accepted task",
		},
	})

	if err := handler.Handle(ctx, envelope.Task, reporter); err != nil {
		var reported ReportedTaskError
		if errors.As(err, &reported) {
			return err
		}
		return d.reportFailure(reporter, envelope.Task, err)
	}
	return nil
}

// ReportedTaskError 表示已上报过的任务错误，避免重复上报失败状态。
type ReportedTaskError struct {
	Err error
}

func (e ReportedTaskError) Error() string {
	if e.Err == nil {
		return "task failed"
	}
	return e.Err.Error()
}

func (e ReportedTaskError) Unwrap() error {
	return e.Err
}

func (d *Dispatcher) reportFailure(reporter *Reporter, task taskdomain.DispatchTask, err error) error {
	now := time.Now().UTC()
	step := firstStep(task.Steps)
	_ = reporter.Report(taskdomain.ReportEnvelope{
		TaskID:      task.ID,
		Status:      taskdomain.StatusFailed,
		Progress:    100,
		CurrentStep: step.StepName,
		Step: &taskdomain.StepReport{
			StepID:     step.ID,
			StepNo:     step.StepNo,
			StepName:   step.StepName,
			Status:     taskdomain.StepFailed,
			Message:    err.Error(),
			FinishedAt: &now,
		},
		Event: &taskdomain.Event{
			TaskID:    task.ID,
			StepID:    step.ID,
			EventType: taskdomain.EventError,
			Content:   err.Error(),
		},
	})
	return err
}

// DecodeExecSpec 从任务的 Spec 字段解码出命令执行规格。
func DecodeExecSpec(task taskdomain.DispatchTask) (taskdomain.ExecSpec, error) {
	var spec taskdomain.ExecSpec
	if len(task.Spec) == 0 {
		return taskdomain.ExecSpec{}, errors.New("task spec is empty")
	}
	if err := json.Unmarshal(task.Spec, &spec); err != nil {
		return taskdomain.ExecSpec{}, err
	}
	if spec.Command == "" {
		return taskdomain.ExecSpec{}, errors.New("exec command is empty")
	}
	return spec, nil
}

func firstStep(steps []taskdomain.DispatchStep) taskdomain.DispatchStep {
	if len(steps) == 0 {
		return taskdomain.DispatchStep{ID: "", StepNo: 1, StepName: "exec"}
	}
	return steps[0]
}
