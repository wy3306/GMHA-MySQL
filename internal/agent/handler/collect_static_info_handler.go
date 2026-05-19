package handler

import (
	"context"
	"encoding/json"
	"time"

	agentcollect "gmha/internal/agent/collect"
	agentcore "gmha/internal/agent/core"
	taskdomain "gmha/internal/domain/task"
)

// CollectStaticInfoHandler 是静态信息采集任务处理器，负责采集主机和 MySQL 的静态配置信息。
type CollectStaticInfoHandler struct {
	collector *agentcollect.StaticCollector
}

// NewCollectStaticInfoHandler 创建一个新的静态信息采集任务处理器实例。
func NewCollectStaticInfoHandler(collector *agentcollect.StaticCollector) *CollectStaticInfoHandler {
	return &CollectStaticInfoHandler{collector: collector}
}

// Type 返回该处理器处理的任务类型。
func (h *CollectStaticInfoHandler) Type() string {
	return string(taskdomain.TypeCollectStaticInfo)
}

// Handle 执行静态信息采集任务，采集完成后将结果上报到管理端。
func (h *CollectStaticInfoHandler) Handle(ctx context.Context, task taskdomain.DispatchTask, reporter *agentcore.Reporter) error {
	var spec taskdomain.CollectStaticInfoSpec
	if len(task.Spec) > 0 {
		if err := json.Unmarshal(task.Spec, &spec); err != nil {
			return err
		}
	}
	info, err := h.collector.Collect(ctx, spec)
	if err != nil {
		return err
	}
	resultJSON, err := json.Marshal(info)
	if err != nil {
		return err
	}
	step := firstStep(task.Steps)
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
			Message:    "static info collected",
			FinishedAt: &finishedAt,
		},
		Event: &taskdomain.Event{
			TaskID:    task.ID,
			StepID:    step.ID,
			EventType: taskdomain.EventInfo,
			Content:   "static info collected",
		},
		Result: resultJSON,
	})
}
