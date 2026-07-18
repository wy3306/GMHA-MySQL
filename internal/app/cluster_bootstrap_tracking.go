package app

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	taskdomain "gmha/internal/domain/task"
)

var clusterBootstrapSteps = []struct {
	code string
	name string
}{
	{"create_install_tasks", "创建 MySQL 安装子任务"},
	{"wait_install_tasks", "等待全部实例安装完成"},
	{"save_vip", "保存并校验 VIP 配置"},
	{"apply_architecture", "应用主从复制架构"},
	{"verify", "验证拓扑与 VIP"},
}

// CreateClusterBootstrapTrackingTask 创建批量安装与架构初始化的 Manager 父任务。
func (s *TaskService) CreateClusterBootstrapTrackingTask(ctx context.Context, taskID, cluster, architecture string, targets int) (TaskDetail, error) {
	now := time.Now().UTC()
	spec, err := json.Marshal(map[string]any{
		"operation": "mysql_cluster_bootstrap", "display_name": "批量安装并初始化 MySQL 架构",
		"cluster": cluster, "architecture": architecture, "targets": targets,
	})
	if err != nil {
		return TaskDetail{}, err
	}
	task := taskdomain.Task{ID: taskID, Type: taskdomain.TypeClusterBootstrap, MachineID: cluster, AgentID: "manager", Status: taskdomain.StatusPending, CurrentStep: clusterBootstrapSteps[0].code, SpecJSON: spec, CreatedAt: now}
	steps := make([]taskdomain.Step, 0, len(clusterBootstrapSteps))
	for index, item := range clusterBootstrapSteps {
		steps = append(steps, taskdomain.Step{ID: taskID + "-" + item.code, TaskID: taskID, StepNo: index + 1, StepName: item.code, Status: taskdomain.StepPending, Message: item.name})
	}
	events := []taskdomain.Event{{ID: fmt.Sprintf("%s-created-%d", taskID, now.UnixNano()), TaskID: taskID, StepID: steps[0].ID, EventType: taskdomain.EventInfo, Content: fmt.Sprintf("已创建集群 %s 的批量安装与架构初始化流程，共 %d 个目标实例。", cluster, targets), CreatedAt: now}}
	if err := s.repo.CreateTask(ctx, task, steps, events); err != nil {
		return TaskDetail{}, err
	}
	return s.GetTaskDetail(ctx, taskID)
}

// UpdateClusterBootstrapStep 更新父任务步骤，并把子任务编号写入事件日志。
func (s *TaskService) UpdateClusterBootstrapStep(ctx context.Context, taskID, stepName string, status taskdomain.StepStatus, message string, relatedTaskIDs []string) error {
	task, found, err := s.repo.GetTask(ctx, taskID)
	if err != nil || !found {
		if err != nil {
			return err
		}
		return fmt.Errorf("cluster bootstrap task %s not found", taskID)
	}
	steps, err := s.repo.ListSteps(ctx, taskID)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	completed := 0
	var stepID string
	for i := range steps {
		step := steps[i]
		if step.StepName == stepName {
			stepID = step.ID
			step.Status = status
			step.Message = message
			if status == taskdomain.StepRunning && step.StartedAt == nil {
				step.StartedAt = &now
			}
			if status == taskdomain.StepSuccess || status == taskdomain.StepFailed {
				if step.StartedAt == nil {
					step.StartedAt = &now
				}
				step.FinishedAt = &now
			}
			if err := s.repo.UpdateStep(ctx, step); err != nil {
				return err
			}
		}
		if step.Status == taskdomain.StepSuccess || (step.StepName == stepName && status == taskdomain.StepSuccess) {
			completed++
		}
	}
	if task.StartedAt == nil {
		task.StartedAt = &now
	}
	task.CurrentStep = stepName
	task.Status = taskdomain.StatusRunning
	task.ProgressPercent = completed * 100 / len(steps)
	if status == taskdomain.StepFailed {
		task.Status, task.ProgressPercent, task.FinishedAt = taskdomain.StatusFailed, 100, &now
	} else if completed == len(steps) {
		task.Status, task.ProgressPercent, task.FinishedAt = taskdomain.StatusSuccess, 100, &now
	}
	if err := s.repo.UpdateTask(ctx, task); err != nil {
		return err
	}
	content := message
	if len(relatedTaskIDs) > 0 {
		content += "；关联子任务：" + joinTaskIDs(relatedTaskIDs)
	}
	eventType := taskdomain.EventInfo
	if status == taskdomain.StepFailed {
		eventType = taskdomain.EventError
	}
	return s.repo.AppendEvent(ctx, taskdomain.Event{ID: fmt.Sprintf("%s-%s-%d", taskID, stepName, now.UnixNano()), TaskID: taskID, StepID: stepID, EventType: eventType, Content: content, CreatedAt: now})
}

func joinTaskIDs(items []string) string {
	out := ""
	for i, item := range items {
		if i > 0 {
			out += "、"
		}
		out += item
	}
	return out
}
