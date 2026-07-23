package app

import (
	"context"
	"sync"
	"testing"
	"time"

	taskdomain "gmha/internal/domain/task"
)

type memoryWorkflowTaskRepository struct {
	mu     sync.Mutex
	tasks  map[string]taskdomain.Task
	steps  map[string][]taskdomain.Step
	events map[string][]taskdomain.Event
}

func newMemoryWorkflowTaskRepository() *memoryWorkflowTaskRepository {
	return &memoryWorkflowTaskRepository{
		tasks: make(map[string]taskdomain.Task), steps: make(map[string][]taskdomain.Step),
		events: make(map[string][]taskdomain.Event),
	}
}

func (r *memoryWorkflowTaskRepository) CreateTask(_ context.Context, task taskdomain.Task, steps []taskdomain.Step, events []taskdomain.Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tasks[task.ID] = task
	r.steps[task.ID] = append([]taskdomain.Step(nil), steps...)
	r.events[task.ID] = append([]taskdomain.Event(nil), events...)
	return nil
}
func (r *memoryWorkflowTaskRepository) GetTask(_ context.Context, id string) (taskdomain.Task, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	task, ok := r.tasks[id]
	return task, ok, nil
}
func (r *memoryWorkflowTaskRepository) ListTasks(context.Context, int) ([]taskdomain.Task, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]taskdomain.Task, 0, len(r.tasks))
	for _, task := range r.tasks {
		out = append(out, task)
	}
	return out, nil
}
func (r *memoryWorkflowTaskRepository) ListTasksByStatus(_ context.Context, status taskdomain.Status, _ int) ([]taskdomain.Task, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []taskdomain.Task
	for _, task := range r.tasks {
		if task.Status == status {
			out = append(out, task)
		}
	}
	return out, nil
}
func (r *memoryWorkflowTaskRepository) ListSteps(_ context.Context, id string) ([]taskdomain.Step, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]taskdomain.Step(nil), r.steps[id]...), nil
}
func (r *memoryWorkflowTaskRepository) ListEvents(_ context.Context, id string, _ int) ([]taskdomain.Event, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]taskdomain.Event(nil), r.events[id]...), nil
}
func (r *memoryWorkflowTaskRepository) UpdateTask(_ context.Context, task taskdomain.Task) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tasks[task.ID] = task
	return nil
}
func (r *memoryWorkflowTaskRepository) UpdateStep(_ context.Context, step taskdomain.Step) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	items := r.steps[step.TaskID]
	for i := range items {
		if items[i].ID == step.ID {
			items[i] = step
		}
	}
	r.steps[step.TaskID] = items
	return nil
}
func (r *memoryWorkflowTaskRepository) AppendEvent(_ context.Context, event taskdomain.Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events[event.TaskID] = append(r.events[event.TaskID], event)
	return nil
}
func (r *memoryWorkflowTaskRepository) DeleteTask(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.tasks, id)
	delete(r.steps, id)
	delete(r.events, id)
	return nil
}
func (r *memoryWorkflowTaskRepository) AssignParentTasks(_ context.Context, parentID string, childIDs []string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, id := range childIDs {
		task := r.tasks[id]
		task.ParentTaskID = parentID
		r.tasks[id] = task
	}
	return nil
}
func (r *memoryWorkflowTaskRepository) ListChildTasks(_ context.Context, parentID string) ([]taskdomain.Task, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []taskdomain.Task
	for _, task := range r.tasks {
		if task.ParentTaskID == parentID {
			out = append(out, task)
		}
	}
	return out, nil
}
func (r *memoryWorkflowTaskRepository) DeleteTaskTree(ctx context.Context, parentID string) error {
	children, _ := r.ListChildTasks(ctx, parentID)
	for _, child := range children {
		_ = r.DeleteTask(ctx, child.ID)
	}
	return r.DeleteTask(ctx, parentID)
}

func TestAIWorkflowTrackingTaskPreservesParentChildProgress(t *testing.T) {
	repo := newMemoryWorkflowTaskRepository()
	service := NewTaskService(repo, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	now := time.Now().UTC()
	snapshot := AIWorkflowTaskSnapshot{
		ID: "workflow-01", Goal: "先诊断再修复 DB-01", Target: "DB-01",
		Status: "running", CreatedAt: now, UpdatedAt: now, StartedAt: &now,
		Operations: []AIWorkflowTaskOperation{
			{ID: "diagnose", Title: "采集诊断", Status: "pending"},
			{ID: "repair", Title: "执行修复", Status: "pending"},
		},
	}
	parent, err := service.CreateAIWorkflowTrackingTask(context.Background(), snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if parent.Task.Type != taskdomain.TypeAIWorkflow || len(parent.Steps) != 2 {
		t.Fatalf("workflow parent was not created with durable steps: %#v", parent)
	}
	childID := "task-diagnose"
	if err := repo.CreateTask(context.Background(), taskdomain.Task{
		ID: childID, Type: taskdomain.TypeCollectMachineInfo, Status: taskdomain.StatusSuccess, CreatedAt: now,
	}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := service.AttachChildTasks(context.Background(), parent.Task.ID, []string{childID}); err != nil {
		t.Fatal(err)
	}
	finished := now.Add(time.Second)
	snapshot.CurrentOperationID = "repair"
	snapshot.UpdatedAt = finished
	snapshot.Operations[0].Status = "succeeded"
	snapshot.Operations[0].StartedAt = &now
	snapshot.Operations[0].FinishedAt = &finished
	snapshot.Operations[1].Status = "executing"
	snapshot.Operations[1].StartedAt = &finished
	if err := service.SyncAIWorkflowTrackingTask(context.Background(), snapshot); err != nil {
		t.Fatal(err)
	}
	detail, err := service.GetTaskDetail(context.Background(), parent.Task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Task.Status != taskdomain.StatusRunning || detail.Task.ProgressPercent != 50 {
		t.Fatalf("parent progress was incorrectly derived from the first child: %#v", detail.Task)
	}
	if len(detail.Children) != 1 || detail.Children[0].ID != childID {
		t.Fatalf("child task is not linked beneath workflow parent: %#v", detail.Children)
	}
}
