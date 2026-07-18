package app_test

import (
	"context"
	"database/sql"
	"testing"

	"gmha/internal/app"
	machinedomain "gmha/internal/domain/machine"
	"gmha/internal/infrastructure/persistence/sqlite"
	_ "modernc.org/sqlite"
)

func TestBatchDeleteMachinesCreatesOneRootTask(t *testing.T) {
	db, err := sql.Open("sqlite", t.TempDir()+"/batch-delete.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	database := sqlite.NewDB(db, sqlite.DialectSQLite)
	machines := sqlite.NewMachineRepository(database)
	tasks := sqlite.NewTaskRepository(database)
	if err := machines.Migrate(); err != nil {
		t.Fatal(err)
	}
	if err := tasks.Migrate(); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, machine := range []machinedomain.Machine{
		{ID: "machine-a", Name: "db-a", IP: "192.0.2.10", SSHPort: 22, SSHUser: "root"},
		{ID: "machine-b", Name: "db-b", IP: "192.0.2.11", SSHPort: 22, SSHUser: "root"},
	} {
		if _, err := machines.Save(ctx, machine); err != nil {
			t.Fatal(err)
		}
	}
	taskService := app.NewTaskService(tasks, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	machineService := app.NewMachineService(nil, machines, nil, nil, nil, nil, nil, nil, nil, taskService)

	result, err := machineService.DeleteMachinesWithOptions(ctx, []string{"machine-a", "machine-b"}, app.DeleteMachineOptions{DetachOnly: true}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if result.Succeeded != 2 || result.Failed != 0 || result.TaskID == "" {
		t.Fatalf("unexpected batch result: %+v", result)
	}
	page, err := taskService.ListTaskPage(ctx, app.TaskListQuery{Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].ID != result.TaskID {
		t.Fatalf("one batch action must expose exactly one root task: %+v", page)
	}
	if len(page.Items[0].Children) != 2 {
		t.Fatalf("machine executions must be nested below the batch task: %+v", page.Items[0].Children)
	}
	detail, err := taskService.GetTaskDetail(ctx, result.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.ChildDetails) != 2 {
		t.Fatalf("expected two machine child workflows, got %d", len(detail.ChildDetails))
	}
}
