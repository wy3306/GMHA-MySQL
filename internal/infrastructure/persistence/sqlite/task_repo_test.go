package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	taskdomain "gmha/internal/domain/task"
	_ "modernc.org/sqlite"
)

func TestTaskRepositoryReturnsCompleteOrderedEventTimeline(t *testing.T) {
	db, err := sql.Open("sqlite", t.TempDir()+"/tasks.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := NewTaskRepository(NewDB(db, DialectSQLite))
	if err := repo.Migrate(); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 7, 15, 9, 30, 0, 123456789, time.UTC)
	task := taskdomain.Task{ID: "task-timeline", Type: taskdomain.TypeMySQLInstall, MachineID: "machine-1", AgentID: "agent-1", Status: taskdomain.StatusRunning, CreatedAt: base}
	step := taskdomain.Step{ID: "task-timeline-step", TaskID: task.ID, StepNo: 1, StepName: "install", Status: taskdomain.StepRunning, StartedAt: &base}
	events := make([]taskdomain.Event, 0, 205)
	for i := 0; i < 205; i++ {
		events = append(events, taskdomain.Event{ID: fmt.Sprintf("event-%03d", i), TaskID: task.ID, StepID: step.ID, EventType: taskdomain.EventLog, Content: fmt.Sprintf("line-%03d", i), CreatedAt: base.Add(time.Duration(i) * time.Nanosecond)})
	}
	if err := repo.CreateTask(context.Background(), task, []taskdomain.Step{step}, events); err != nil {
		t.Fatal(err)
	}
	got, err := repo.ListEvents(context.Background(), task.ID, -1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(events) {
		t.Fatalf("event timeline was truncated: got %d want %d", len(got), len(events))
	}
	if got[0].Content != "line-000" || got[len(got)-1].Content != "line-204" {
		t.Fatalf("event timeline order is incorrect: first=%q last=%q", got[0].Content, got[len(got)-1].Content)
	}
	stored, ok, err := repo.GetTask(context.Background(), task.ID)
	if err != nil || !ok {
		t.Fatalf("GetTask() = %+v, %v, %v", stored, ok, err)
	}
	if !stored.CreatedAt.Equal(base) {
		t.Fatalf("task timestamp lost precision: got %s want %s", stored.CreatedAt, base)
	}
}

func TestTaskRepositoryPagesAndFiltersTaskHistory(t *testing.T) {
	db, err := sql.Open("sqlite", t.TempDir()+"/task-page.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := NewTaskRepository(NewDB(db, DialectSQLite))
	if err := repo.Migrate(); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)
	items := []taskdomain.Task{
		{ID: "task-install", Type: taskdomain.TypeMySQLInstall, MachineID: "db-a", Status: taskdomain.StatusRunning, CurrentStep: "initialize_mysql", SpecJSON: []byte(`{"port":3306}`), CreatedAt: base},
		{ID: "platform-task-machine", Type: taskdomain.TypePlatformOperation, MachineID: "machine-a", Status: taskdomain.StatusSuccess, CurrentStep: "维护机器资源", SpecJSON: []byte(`{"display_name":"维护机器资源"}`), CreatedAt: base.Add(time.Second)},
		{ID: "task-failed", Type: taskdomain.TypeExec, MachineID: "db-b", Status: taskdomain.StatusFailed, CurrentStep: "collect logs", CreatedAt: base.Add(2 * time.Second)},
	}
	for _, item := range items {
		if err := repo.CreateTask(context.Background(), item, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	page, total, err := repo.ListTaskPage(context.Background(), taskdomain.ListQuery{Limit: 1, Offset: 0, Statuses: []taskdomain.Status{taskdomain.StatusSuccess}, Types: []taskdomain.Type{taskdomain.TypePlatformOperation}, Keyword: "机器"})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(page) != 1 || page[0].ID != "platform-task-machine" {
		t.Fatalf("unexpected filtered task page: total=%d items=%+v", total, page)
	}
}

func TestTaskRepositoryKeepsChildrenOutOfTopLevelHistory(t *testing.T) {
	db, err := sql.Open("sqlite", t.TempDir()+"/task-children.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := NewTaskRepository(NewDB(db, DialectSQLite))
	if err := repo.Migrate(); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	parent := taskdomain.Task{ID: "cluster-bootstrap-1", Type: taskdomain.TypeClusterBootstrap, MachineID: "demo", Status: taskdomain.StatusRunning, CreatedAt: now}
	child := taskdomain.Task{ID: "task-install-child", ParentTaskID: parent.ID, Type: taskdomain.TypeMySQLInstall, MachineID: "db-1", Status: taskdomain.StatusRunning, CreatedAt: now.Add(time.Second)}
	for _, item := range []taskdomain.Task{parent, child} {
		if err := repo.CreateTask(context.Background(), item, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	page, total, err := repo.ListTaskPage(context.Background(), taskdomain.ListQuery{Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(page) != 1 || page[0].ID != parent.ID {
		t.Fatalf("unexpected top-level tasks: total=%d items=%+v", total, page)
	}
	children, err := repo.ListChildTasks(context.Background(), parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(children) != 1 || children[0].ID != child.ID || children[0].ParentTaskID != parent.ID {
		t.Fatalf("unexpected child tasks: %+v", children)
	}
}

func TestTaskRepositoryKeepsCollectionJobsOutOfTaskCenter(t *testing.T) {
	db, err := sql.Open("sqlite", t.TempDir()+"/task-internal-collection.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := NewTaskRepository(NewDB(db, DialectSQLite))
	if err := repo.Migrate(); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	items := []taskdomain.Task{
		{ID: "collect-runtime", Type: taskdomain.TypeCollectMachineInfo, MachineID: "db-1", Status: taskdomain.StatusSuccess, CreatedAt: now},
		{ID: "collect-static", Type: taskdomain.TypeCollectStaticInfo, MachineID: "db-1", Status: taskdomain.StatusSuccess, CreatedAt: now.Add(time.Second)},
		{ID: "mysql-install", Type: taskdomain.TypeMySQLInstall, MachineID: "db-1", Status: taskdomain.StatusSuccess, CreatedAt: now.Add(2 * time.Second)},
	}
	for _, item := range items {
		if err := repo.CreateTask(context.Background(), item, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	page, total, err := repo.ListTaskPage(context.Background(), taskdomain.ListQuery{Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(page) != 1 || page[0].ID != "mysql-install" {
		t.Fatalf("collection jobs leaked into task center page: total=%d items=%+v", total, page)
	}
	recent, err := repo.ListTasks(context.Background(), 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(recent) != 1 || recent[0].ID != "mysql-install" {
		t.Fatalf("collection jobs leaked into recent tasks: %+v", recent)
	}
}

func TestTaskRepositoryDeleteTaskRemovesCompleteHistory(t *testing.T) {
	db, err := sql.Open("sqlite", t.TempDir()+"/task-delete.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := NewTaskRepository(NewDB(db, DialectSQLite))
	if err := repo.Migrate(); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	task := taskdomain.Task{ID: "task-delete", Type: taskdomain.TypeExec, MachineID: "db-a", Status: taskdomain.StatusSuccess, CreatedAt: now}
	step := taskdomain.Step{ID: "task-delete-step", TaskID: task.ID, StepNo: 1, StepName: "exec", Status: taskdomain.StepSuccess, StartedAt: &now, FinishedAt: &now}
	event := taskdomain.Event{ID: "task-delete-event", TaskID: task.ID, StepID: step.ID, EventType: taskdomain.EventInfo, Content: "done", CreatedAt: now}
	if err := repo.CreateTask(context.Background(), task, []taskdomain.Step{step}, []taskdomain.Event{event}); err != nil {
		t.Fatal(err)
	}
	if err := repo.DeleteTask(context.Background(), task.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := repo.GetTask(context.Background(), task.ID); err != nil || ok {
		t.Fatalf("deleted task still exists: ok=%v err=%v", ok, err)
	}
	steps, err := repo.ListSteps(context.Background(), task.ID)
	if err != nil || len(steps) != 0 {
		t.Fatalf("task steps were not deleted: steps=%+v err=%v", steps, err)
	}
	events, err := repo.ListEvents(context.Background(), task.ID, -1)
	if err != nil || len(events) != 0 {
		t.Fatalf("task events were not deleted: events=%+v err=%v", events, err)
	}
}

func TestTaskRepositoryAssignsAndDeletesNestedTaskTree(t *testing.T) {
	db, err := sql.Open("sqlite", t.TempDir()+"/task-tree.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := NewTaskRepository(NewDB(db, DialectSQLite))
	if err := repo.Migrate(); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	items := []taskdomain.Task{
		{ID: "business-parent", Type: taskdomain.TypeBatchOperation, Status: taskdomain.StatusSuccess, CreatedAt: now},
		{ID: "workflow-child", Type: taskdomain.TypeArchitecture, Status: taskdomain.StatusSuccess, CreatedAt: now.Add(time.Second)},
		{ID: "agent-grandchild", ParentTaskID: "workflow-child", Type: taskdomain.TypeExec, Status: taskdomain.StatusSuccess, CreatedAt: now.Add(2 * time.Second)},
	}
	for _, item := range items {
		step := taskdomain.Step{ID: item.ID + "-step", TaskID: item.ID, StepNo: 1, StepName: "run", Status: taskdomain.StepSuccess, StartedAt: &now, FinishedAt: &now}
		event := taskdomain.Event{ID: item.ID + "-event", TaskID: item.ID, StepID: step.ID, EventType: taskdomain.EventInfo, Content: "done", CreatedAt: now}
		if err := repo.CreateTask(context.Background(), item, []taskdomain.Step{step}, []taskdomain.Event{event}); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.AssignParentTasks(context.Background(), "business-parent", []string{"workflow-child"}); err != nil {
		t.Fatal(err)
	}
	children, err := repo.ListChildTasks(context.Background(), "business-parent")
	if err != nil || len(children) != 1 || children[0].ID != "workflow-child" {
		t.Fatalf("assigned children = %+v, err=%v", children, err)
	}
	if err := repo.DeleteTaskTree(context.Background(), "business-parent"); err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if _, ok, err := repo.GetTask(context.Background(), item.ID); err != nil || ok {
			t.Fatalf("task %s survived tree deletion: ok=%v err=%v", item.ID, ok, err)
		}
		steps, _ := repo.ListSteps(context.Background(), item.ID)
		events, _ := repo.ListEvents(context.Background(), item.ID, -1)
		if len(steps) != 0 || len(events) != 0 {
			t.Fatalf("history for %s survived: steps=%d events=%d", item.ID, len(steps), len(events))
		}
	}
}
