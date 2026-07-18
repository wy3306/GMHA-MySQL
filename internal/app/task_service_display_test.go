package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	taskdomain "gmha/internal/domain/task"
)

type taskTreeDisplayRepo struct {
	parent   taskdomain.Task
	children []taskdomain.Task
	steps    map[string][]taskdomain.Step
	events   map[string][]taskdomain.Event
}

func (r *taskTreeDisplayRepo) CreateTask(context.Context, taskdomain.Task, []taskdomain.Step, []taskdomain.Event) error {
	return nil
}
func (r *taskTreeDisplayRepo) GetTask(_ context.Context, taskID string) (taskdomain.Task, bool, error) {
	if r.parent.ID == taskID {
		return r.parent, true, nil
	}
	for _, child := range r.children {
		if child.ID == taskID {
			return child, true, nil
		}
	}
	return taskdomain.Task{}, false, nil
}
func (r *taskTreeDisplayRepo) ListTasks(context.Context, int) ([]taskdomain.Task, error) {
	return []taskdomain.Task{r.parent}, nil
}
func (r *taskTreeDisplayRepo) ListTasksByStatus(context.Context, taskdomain.Status, int) ([]taskdomain.Task, error) {
	return nil, nil
}
func (r *taskTreeDisplayRepo) ListTaskPage(context.Context, taskdomain.ListQuery) ([]taskdomain.Task, int, error) {
	return []taskdomain.Task{r.parent}, 1, nil
}
func (r *taskTreeDisplayRepo) ListChildTasks(_ context.Context, parentTaskID string) ([]taskdomain.Task, error) {
	if parentTaskID != r.parent.ID {
		return nil, nil
	}
	return append([]taskdomain.Task(nil), r.children...), nil
}
func (r *taskTreeDisplayRepo) ListSteps(_ context.Context, taskID string) ([]taskdomain.Step, error) {
	return append([]taskdomain.Step(nil), r.steps[taskID]...), nil
}
func (r *taskTreeDisplayRepo) ListEvents(_ context.Context, taskID string, _ int) ([]taskdomain.Event, error) {
	return append([]taskdomain.Event(nil), r.events[taskID]...), nil
}

func TestGetTaskDetailIncludesEveryChildWorkflow(t *testing.T) {
	now := time.Now().UTC()
	parent := taskdomain.Task{ID: "workflow-parent", Type: taskdomain.TypeBatchOperation, Status: taskdomain.StatusRunning, CreatedAt: now}
	child := taskdomain.Task{ID: "workflow-child", ParentTaskID: parent.ID, Type: taskdomain.TypeMySQLInstall, MachineID: "db-1", Status: taskdomain.StatusRunning, CreatedAt: now}
	repo := &taskTreeDisplayRepo{
		parent: parent, children: []taskdomain.Task{child},
		steps: map[string][]taskdomain.Step{
			parent.ID: {{ID: "parent-step", TaskID: parent.ID, StepName: "create_children", Status: taskdomain.StepSuccess}},
			child.ID:  {{ID: "child-step", TaskID: child.ID, StepName: "initialize_mysql", Status: taskdomain.StepRunning}},
		},
		events: map[string][]taskdomain.Event{
			child.ID: {{ID: "child-event", TaskID: child.ID, StepID: "child-step", EventType: taskdomain.EventInfo, Content: "initializing", CreatedAt: now}},
		},
	}
	service := NewTaskService(repo, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)

	detail, err := service.GetTaskDetail(context.Background(), parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.ChildDetails) != 1 || detail.ChildDetails[0].Task.ID != child.ID {
		t.Fatalf("expected child workflow in parent detail: %+v", detail.ChildDetails)
	}
	if len(detail.ChildDetails[0].Steps) != 1 || detail.ChildDetails[0].Steps[0].ID != "child-step" {
		t.Fatalf("child steps missing from unified workflow: %+v", detail.ChildDetails[0].Steps)
	}
	if len(detail.ChildDetails[0].Events) != 1 || detail.ChildDetails[0].Events[0].ID != "child-event" {
		t.Fatalf("child events missing from unified workflow: %+v", detail.ChildDetails[0].Events)
	}
}
func (r *taskTreeDisplayRepo) UpdateTask(context.Context, taskdomain.Task) error   { return nil }
func (r *taskTreeDisplayRepo) UpdateStep(context.Context, taskdomain.Step) error   { return nil }
func (r *taskTreeDisplayRepo) AppendEvent(context.Context, taskdomain.Event) error { return nil }
func (r *taskTreeDisplayRepo) DeleteTask(context.Context, string) error            { return nil }

func TestListTaskPageNestsSanitizedChildrenUnderParent(t *testing.T) {
	now := time.Now().UTC()
	childSpec, _ := json.Marshal(taskdomain.MySQLInstallSpec{Port: 3306, RootPassword: "must-not-leak", PackageName: "mysql-8.4.tar.xz"})
	repo := &taskTreeDisplayRepo{
		parent:   taskdomain.Task{ID: "business-parent", Type: taskdomain.TypeBatchOperation, Status: taskdomain.StatusRunning, CreatedAt: now},
		children: []taskdomain.Task{{ID: "execution-child", ParentTaskID: "business-parent", Type: taskdomain.TypeMySQLInstall, MachineID: "db-1", Status: taskdomain.StatusRunning, ProgressPercent: 45, SpecJSON: childSpec, CreatedAt: now}},
	}
	service := NewTaskService(repo, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)

	page, err := service.ListTaskPage(context.Background(), TaskListQuery{Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || len(page.Items) != 1 || len(page.Items[0].Children) != 1 {
		t.Fatalf("expected one parent with one nested child: %+v", page)
	}
	child := page.Items[0].Children[0]
	if child.ID != "execution-child" || child.ParentTaskID != "business-parent" {
		t.Fatalf("unexpected nested child: %+v", child)
	}
	if strings.Contains(string(child.SpecJSON), "must-not-leak") {
		t.Fatalf("nested child leaked execution secret: %s", child.SpecJSON)
	}
}

func TestTaskForDisplayKeepsMetadataAndHidesCommand(t *testing.T) {
	spec, _ := json.Marshal(taskdomain.ExecSpec{
		Command: "MYSQL_PWD=secret mysql -e 'select 1'", Operation: "mysql_collect",
		DisplayName: "采集 MySQL 运行数据", Port: 3306,
	})
	item := taskForDisplay(taskdomain.Task{Type: taskdomain.TypeExec, SpecJSON: spec})
	if strings.Contains(string(item.SpecJSON), "secret") || strings.Contains(string(item.SpecJSON), "MYSQL_PWD") {
		t.Fatalf("display spec leaked command: %s", item.SpecJSON)
	}
	var display taskdomain.ExecSpec
	if err := json.Unmarshal(item.SpecJSON, &display); err != nil {
		t.Fatalf("unmarshal display spec: %v", err)
	}
	if display.Operation != "mysql_collect" || display.DisplayName == "" || display.Port != 3306 {
		t.Fatalf("display metadata was lost: %+v", display)
	}
}

func TestTaskForDisplayCompactsMySQLInstallAndHidesSecrets(t *testing.T) {
	spec, _ := json.Marshal(taskdomain.MySQLInstallSpec{
		Port: 3306, ServerID: 2, MySQLUser: "mysql", RootPassword: "root-secret",
		Profile: "default", PackageName: "mysql.tar.xz", MyCnfContent: "large-config-body",
		Accounts: []taskdomain.MySQLAccountSpec{{Username: "monitor", Password: "account-secret"}},
	})
	item := taskForDisplay(taskdomain.Task{Type: taskdomain.TypeMySQLInstall, SpecJSON: spec})
	display := string(item.SpecJSON)
	for _, forbidden := range []string{"root-secret", "account-secret", "large-config-body", "root_password", "accounts", "my_cnf_content"} {
		if strings.Contains(display, forbidden) {
			t.Fatalf("display spec leaked %q: %s", forbidden, display)
		}
	}
	if !strings.Contains(display, `"port":3306`) || !strings.Contains(display, `"package_name":"mysql.tar.xz"`) {
		t.Fatalf("display metadata was lost: %s", display)
	}
}

func TestTaskForDisplayHidesTopologyPasswords(t *testing.T) {
	spec, _ := json.Marshal(taskdomain.MySQLTopologySpec{
		Topology: "primary_replica", Port: 3306, RootPassword: "root-secret",
		ReplicationUser: "repl", ReplicationPassword: "repl-secret",
	})
	item := taskForDisplay(taskdomain.Task{Type: taskdomain.TypeMySQLTopology, SpecJSON: spec})
	display := string(item.SpecJSON)
	if strings.Contains(display, "secret") || strings.Contains(display, "password") {
		t.Fatalf("display spec leaked topology credentials: %s", display)
	}
}

func TestTaskForDisplayKeepsPlatformOperationLinks(t *testing.T) {
	spec, _ := json.Marshal(taskdomain.PlatformOperationSpec{
		Operation: "cluster-mysql-install", DisplayName: "批量部署 MySQL", Method: "POST", Path: "/api/v1/tasks/cluster-mysql-install",
		HTTPStatus: 200, RelatedTaskIDs: []string{"task-one", "task-two"},
	})
	item := taskForDisplay(taskdomain.Task{Type: taskdomain.TypePlatformOperation, SpecJSON: spec})
	var display taskdomain.PlatformOperationSpec
	if err := json.Unmarshal(item.SpecJSON, &display); err != nil {
		t.Fatal(err)
	}
	if display.DisplayName != "批量部署 MySQL" || len(display.RelatedTaskIDs) != 2 {
		t.Fatalf("platform operation metadata was lost: %+v", display)
	}
}

func TestMySQLUpgradePrecheckGateRequiresMatchingFreshSuccessfulReport(t *testing.T) {
	spec, _ := json.Marshal(taskdomain.ExecSpec{Operation: "mysql_upgrade_precheck", Port: 3306, PackageName: "mysql-8.4.6-linux-glibc2.17-x86_64.tar.xz"})
	repo := &taskTreeDisplayRepo{parent: taskdomain.Task{
		ID: "precheck-1", Type: taskdomain.TypeMySQLUpgrade, MachineID: "db-1",
		Status: taskdomain.StatusSuccess, SpecJSON: spec, CreatedAt: time.Now().UTC(),
	}}
	service := NewTaskService(repo, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if err := service.validateMySQLUpgradePrecheck(context.Background(), "precheck-1", "db-1", 3306, "mysql-8.4.6-linux-glibc2.17-x86_64.tar.xz"); err != nil {
		t.Fatalf("matching successful precheck should pass: %v", err)
	}
	if err := service.validateMySQLUpgradePrecheck(context.Background(), "precheck-1", "db-2", 3306, "mysql-8.4.6-linux-glibc2.17-x86_64.tar.xz"); err == nil {
		t.Fatal("report for another machine must not pass")
	}
	repo.parent.CreatedAt = time.Now().Add(-31 * time.Minute)
	if err := service.validateMySQLUpgradePrecheck(context.Background(), "precheck-1", "db-1", 3306, "mysql-8.4.6-linux-glibc2.17-x86_64.tar.xz"); err == nil || !strings.Contains(err.Error(), "30") {
		t.Fatalf("stale report should be rejected, got %v", err)
	}
}
