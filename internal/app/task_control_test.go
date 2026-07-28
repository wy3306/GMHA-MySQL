package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"

	agentdomain "gmha/internal/domain/agent"
	machinedomain "gmha/internal/domain/machine"
	taskdomain "gmha/internal/domain/task"
	persistence "gmha/internal/infrastructure/persistence/sqlite"
	taskusecase "gmha/internal/usecase/task"
	_ "modernc.org/sqlite"
)

type taskControlMachines struct {
	item machinedomain.Machine
}

func (m taskControlMachines) GetByIP(_ context.Context, ip string) (machinedomain.Machine, bool, error) {
	return m.item, m.item.IP == ip, nil
}
func (m taskControlMachines) List(context.Context) ([]machinedomain.Machine, error) {
	return []machinedomain.Machine{m.item}, nil
}

type taskControlAgents struct {
	item agentdomain.Agent
}

func (a taskControlAgents) GetByMachineID(_ context.Context, machineID string) (agentdomain.Agent, bool, error) {
	return a.item, a.item.MachineID == machineID, nil
}

type taskControlConnection struct {
	envelopes []taskdomain.DispatchEnvelope
}

func (c *taskControlConnection) Send(envelope taskdomain.DispatchEnvelope) error {
	c.envelopes = append(c.envelopes, envelope)
	return nil
}

func newTaskControlTestService(t *testing.T) (*TaskService, taskdomain.Repository) {
	t.Helper()
	db, err := sql.Open("sqlite", t.TempDir()+"/task-control.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	repo := persistence.NewTaskRepository(persistence.NewDB(db, persistence.DialectSQLite))
	if err := repo.Migrate(); err != nil {
		t.Fatal(err)
	}
	return NewTaskService(repo, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil), repo
}

func TestTaskControlSkipTransitionsTaskAndSteps(t *testing.T) {
	service, repo := newTaskControlTestService(t)
	now := time.Now().UTC()
	spec, _ := json.Marshal(taskdomain.ExecSpec{Command: "MYSQL_PWD=secret mysql -e 'select 1'", RollbackCommand: "restore-secret"})
	task := taskdomain.Task{ID: "task-pending", Type: taskdomain.TypeExec, MachineID: "db-1", Status: taskdomain.StatusPending, SpecJSON: spec, CreatedAt: now}
	step := taskdomain.Step{ID: "step-pending", TaskID: task.ID, StepNo: 1, StepName: "exec", Status: taskdomain.StepPending}
	if err := repo.CreateTask(context.Background(), task, []taskdomain.Step{step}, nil); err != nil {
		t.Fatal(err)
	}

	detail, err := service.GetTaskDetail(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !detail.Controls.Skip.Allowed || detail.Controls.Skip.Confirmation != "SKIP "+task.ID {
		t.Fatalf("pending leaf task should be skippable: %+v", detail.Controls.Skip)
	}
	if _, err := service.ControlTask(context.Background(), TaskControlRequest{TaskID: task.ID, Action: TaskControlSkip, Confirmation: "wrong"}); err == nil {
		t.Fatal("skip must require the exact confirmation")
	}
	result, err := service.ControlTask(context.Background(), TaskControlRequest{TaskID: task.ID, Action: TaskControlSkip, Confirmation: "SKIP " + task.ID})
	if err != nil {
		t.Fatal(err)
	}
	if result.Task.Task.Status != taskdomain.StatusSkipped || result.Task.Task.ProgressPercent != 100 {
		t.Fatalf("unexpected skipped task: %+v", result.Task.Task)
	}
	if len(result.Task.Steps) != 1 || result.Task.Steps[0].Status != taskdomain.StepSkipped {
		t.Fatalf("pending steps must become skipped: %+v", result.Task.Steps)
	}
	if len(result.Task.Events) == 0 || !strings.Contains(result.Task.Events[len(result.Task.Events)-1].Content, "未下发") {
		t.Fatalf("skip audit event missing: %+v", result.Task.Events)
	}
	stored, ok, err := repo.GetTask(context.Background(), task.ID)
	if err != nil || !ok {
		t.Fatalf("read skipped task: ok=%t err=%v", ok, err)
	}
	if strings.Contains(string(stored.SpecJSON), "secret") || !strings.Contains(string(stored.SpecJSON), "redacted after skip") {
		t.Fatalf("skipped task command was not redacted: %s", stored.SpecJSON)
	}
}

func TestTaskControlsExplainRollbackSafety(t *testing.T) {
	service := &TaskService{}
	specJSON, _ := json.Marshal(taskdomain.ExecSpec{Operation: "mysql_upgrade", RollbackCommand: "restore-safe-state"})
	failed := taskdomain.Task{ID: "upgrade-1", Type: taskdomain.TypeMySQLUpgrade, Status: taskdomain.StatusFailed, SpecJSON: specJSON}

	controls := service.taskControls(failed, nil, []taskdomain.Event{{Content: "自动回滚失败: exit 1\nGMHA_ROLLBACK_RESULT=failed"}}, false)
	if !controls.Rollback.Allowed || controls.RollbackClass != RollbackAutomatic || controls.Rollback.Confirmation != "ROLLBACK "+failed.ID {
		t.Fatalf("failed automatic rollback should allow a confirmed retry: %+v", controls)
	}
	controls = service.taskControls(failed, nil, []taskdomain.Event{{Content: "GMHA_ROLLBACK_RESULT=success"}}, false)
	if controls.Rollback.Allowed || !strings.Contains(controls.Rollback.Reason, "已完成") {
		t.Fatalf("successful automatic rollback must not be repeated: %+v", controls)
	}
	uninstall := service.taskControls(taskdomain.Task{ID: "remove-1", Type: taskdomain.TypeMySQLUninstall, Status: taskdomain.StatusFailed}, nil, nil, false)
	if uninstall.RollbackClass != RollbackIrreversible || !uninstall.Irreversible || uninstall.Rollback.Allowed {
		t.Fatalf("uninstall must be explicitly classified as irreversible: %+v", uninstall)
	}
	collect := service.taskControls(taskdomain.Task{ID: "collect-1", Type: taskdomain.TypeCollectMachineInfo, Status: taskdomain.StatusSuccess}, nil, nil, false)
	if collect.RollbackClass != RollbackNotRequired {
		t.Fatalf("read-only collection should not pretend to need rollback: %+v", collect)
	}
}

func TestRunningAndChildTasksCannotBeSkipped(t *testing.T) {
	service := &TaskService{}
	running := service.taskControls(taskdomain.Task{ID: "running-1", Type: taskdomain.TypeExec, Status: taskdomain.StatusRunning}, nil, nil, false)
	if running.Skip.Allowed || !strings.Contains(running.Skip.Reason, "Agent") {
		t.Fatalf("running tasks cannot be safely skipped without cancellation: %+v", running.Skip)
	}
	child := service.taskControls(taskdomain.Task{ID: "child-1", ParentTaskID: "parent-1", Type: taskdomain.TypeExec, Status: taskdomain.StatusPending}, nil, nil, false)
	if child.Skip.Allowed || !strings.Contains(child.Skip.Reason, "父级") {
		t.Fatalf("orchestrated child must not be skipped alone: %+v", child.Skip)
	}
}

func TestTaskControlRollbackCreatesIndependentRecoveryTask(t *testing.T) {
	_, repo := newTaskControlTestService(t)
	machine := machinedomain.Machine{ID: "db-1", Name: "DB-01", IP: "192.0.2.10"}
	agent := agentdomain.Agent{ID: "agent-db-1", MachineID: machine.ID, State: agentdomain.StateOnline}
	createExec := taskusecase.NewCreateExecTaskUsecase(taskControlMachines{item: machine}, taskControlAgents{item: agent})
	service := NewTaskService(repo, createExec, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	spec, _ := json.Marshal(taskdomain.ExecSpec{Operation: "mysql_upgrade", DisplayName: "升级 MySQL", RollbackCommand: "restore-safe-state"})
	original := taskdomain.Task{ID: "upgrade-failed", Type: taskdomain.TypeMySQLUpgrade, MachineID: machine.ID, AgentID: agent.ID, Status: taskdomain.StatusFailed, SpecJSON: spec, CreatedAt: time.Now().UTC()}
	event := taskdomain.Event{ID: "rollback-failed", TaskID: original.ID, EventType: taskdomain.EventError, Content: "GMHA_ROLLBACK_RESULT=failed", CreatedAt: time.Now().UTC()}
	if err := repo.CreateTask(context.Background(), original, nil, []taskdomain.Event{event}); err != nil {
		t.Fatal(err)
	}

	result, err := service.ControlTask(context.Background(), TaskControlRequest{TaskID: original.ID, Action: TaskControlRollback, Confirmation: "ROLLBACK " + original.ID})
	if err != nil {
		t.Fatal(err)
	}
	if result.Task.Task.ID == original.ID || result.Task.Task.Type != taskdomain.TypeExec || result.Task.Task.Status != taskdomain.StatusPending {
		t.Fatalf("rollback retry must be a new pending exec task: %+v", result.Task.Task)
	}
	stored, ok, err := repo.GetTask(context.Background(), result.Task.Task.ID)
	if err != nil || !ok {
		t.Fatalf("read recovery task: ok=%t err=%v", ok, err)
	}
	var recoverySpec taskdomain.ExecSpec
	if err := json.Unmarshal(stored.SpecJSON, &recoverySpec); err != nil {
		t.Fatal(err)
	}
	if recoverySpec.Command != "restore-safe-state" || recoverySpec.Operation != "manual_rollback" {
		t.Fatalf("unexpected recovery task spec: %+v", recoverySpec)
	}
	unchanged, _, err := repo.GetTask(context.Background(), original.ID)
	if err != nil || unchanged.Status != taskdomain.StatusFailed {
		t.Fatalf("original failure audit record was changed: %+v err=%v", unchanged, err)
	}
}

func TestTaskControlRetryResumesFailedMySQLInstallFromFailedStep(t *testing.T) {
	service, repo := newTaskControlTestService(t)
	now := time.Now().UTC()
	finished := now.Add(time.Minute)
	task := taskdomain.Task{
		ID: "install-failed", Type: taskdomain.TypeMySQLInstall,
		MachineID: "db-1", AgentID: "agent-db-1", Status: taskdomain.StatusFailed,
		ProgressPercent: 66, CurrentStep: "install_pt_tools",
		CreatedAt: now.Add(-time.Minute), StartedAt: &now, FinishedAt: &finished,
		SpecJSON: json.RawMessage(`{"port":3306,"install_pt_tools":true}`),
	}
	steps := []taskdomain.Step{
		{ID: "step-verify", TaskID: task.ID, StepNo: 1, StepName: "verify_mysql", Status: taskdomain.StepSuccess, StartedAt: &now, FinishedAt: &finished},
		{ID: "step-pt", TaskID: task.ID, StepNo: 2, StepName: "install_pt_tools", Status: taskdomain.StepFailed, Message: "missing DBI", StartedAt: &now, FinishedAt: &finished},
		{ID: "step-account", TaskID: task.ID, StepNo: 3, StepName: "init_accounts", Status: taskdomain.StepPending},
	}
	if err := repo.CreateTask(context.Background(), task, steps, nil); err != nil {
		t.Fatal(err)
	}

	detail, err := service.GetTaskDetail(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Controls.Retry.Allowed || !strings.Contains(detail.Controls.Retry.Reason, "Agent") {
		t.Fatalf("resume must wait for a compatible online Agent: %+v", detail.Controls.Retry)
	}

	conn := &taskControlConnection{}
	service.RegisterAgentForMachineWithCapabilities(
		task.AgentID,
		task.MachineID,
		conn,
		[]string{string(taskdomain.TypeMySQLInstall), taskdomain.CapabilityTaskStepResumeV1},
	)
	detail, err = service.GetTaskDetail(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !detail.Controls.Retry.Allowed || detail.Controls.Retry.Confirmation != "RETRY "+task.ID {
		t.Fatalf("failed install should be resumable: %+v", detail.Controls.Retry)
	}
	if _, err := service.ControlTask(context.Background(), TaskControlRequest{
		TaskID: task.ID, Action: TaskControlRetry, Confirmation: "wrong",
	}); err == nil {
		t.Fatal("resume must require exact confirmation")
	}

	result, err := service.ControlTask(context.Background(), TaskControlRequest{
		TaskID: task.ID, Action: TaskControlRetry, Confirmation: "RETRY " + task.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Task.Task.Status != taskdomain.StatusSent || result.Task.Task.FinishedAt != nil {
		t.Fatalf("resumed task should be dispatched without a finish time: %+v", result.Task.Task)
	}
	if len(conn.envelopes) != 1 || len(conn.envelopes[0].Task.Steps) != 3 {
		t.Fatalf("resume dispatch missing: %+v", conn.envelopes)
	}
	dispatched := conn.envelopes[0].Task.Steps
	if dispatched[0].Status != taskdomain.StepSuccess || dispatched[1].Status != taskdomain.StepPending || dispatched[2].Status != taskdomain.StepPending {
		t.Fatalf("successful step must be preserved and remaining steps reset: %+v", dispatched)
	}
	hasRetryAudit := false
	for _, event := range result.Task.Events {
		hasRetryAudit = hasRetryAudit || strings.Contains(event.Content, "GMHA_TASK_RETRY_ATTEMPT=1")
	}
	if !hasRetryAudit {
		t.Fatalf("resume audit event missing: %+v", result.Task.Events)
	}
}
