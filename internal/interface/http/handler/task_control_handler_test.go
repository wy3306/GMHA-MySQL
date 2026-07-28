package handler

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"gmha/internal/app"
	taskdomain "gmha/internal/domain/task"
	persistence "gmha/internal/infrastructure/persistence/sqlite"
	_ "modernc.org/sqlite"
)

func TestHandleTaskControlSkipsPendingTask(t *testing.T) {
	db, err := sql.Open("sqlite", t.TempDir()+"/task-control-http.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := persistence.NewTaskRepository(persistence.NewDB(db, persistence.DialectSQLite))
	if err := repo.Migrate(); err != nil {
		t.Fatal(err)
	}
	task := taskdomain.Task{ID: "http-skip-1", Type: taskdomain.TypeExec, MachineID: "db-1", Status: taskdomain.StatusPending, CreatedAt: time.Now().UTC()}
	steps := []taskdomain.Step{{ID: "http-skip-step", TaskID: task.ID, StepNo: 1, StepName: "exec", Status: taskdomain.StepPending}}
	if err := repo.CreateTask(context.Background(), task, steps, nil); err != nil {
		t.Fatal(err)
	}
	service := app.NewTaskService(repo, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/control", strings.NewReader(`{"task_id":"http-skip-1","action":"skip","confirmation":"SKIP http-skip-1"}`))
	recorder := httptest.NewRecorder()

	NewTaskHandler(service).HandleTaskControl(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	stored, ok, err := repo.GetTask(context.Background(), task.ID)
	if err != nil || !ok {
		t.Fatalf("read skipped task: ok=%t err=%v", ok, err)
	}
	if stored.Status != taskdomain.StatusSkipped {
		t.Fatalf("status=%s, want skipped", stored.Status)
	}
}
