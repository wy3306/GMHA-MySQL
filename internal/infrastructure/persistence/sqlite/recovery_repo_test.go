package sqlite

import (
	"context"
	"database/sql"
	"testing"
	"time"

	recoverydomain "gmha/internal/domain/recovery"
	_ "modernc.org/sqlite"
)

func TestRecoveryTaskHistorySurvivesMachineCleanup(t *testing.T) {
	db, err := sql.Open("sqlite", t.TempDir()+"/recovery.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := NewRecoveryRepository(NewDB(db, DialectSQLite))
	if err := repo.Migrate(); err != nil {
		t.Fatal(err)
	}
	created := time.Date(2026, 7, 15, 11, 0, 0, 123456789, time.UTC)
	_, err = repo.CreateTask(context.Background(), recoverydomain.Task{ID: "recovery-history", MachineID: "machine-deleted", MachineIP: "10.0.0.8", Status: recoverydomain.StatusFailed, Trigger: recoverydomain.TriggerManual, Action: recoverydomain.ActionRestart, LastError: "heartbeat timeout", CreatedAt: created})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.DeleteByMachineID(context.Background(), "machine-deleted"); err != nil {
		t.Fatal(err)
	}
	items, err := repo.ListRecent(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != "recovery-history" || items[0].LastError != "heartbeat timeout" {
		t.Fatalf("recovery audit history was deleted: %+v", items)
	}
	if !items[0].CreatedAt.Equal(created) {
		t.Fatalf("recovery task timestamp lost precision: got %s want %s", items[0].CreatedAt, created)
	}
}

func TestRecoveryMigrationClosesInterruptedActiveState(t *testing.T) {
	db, err := sql.Open("sqlite", t.TempDir()+"/recovery-restart.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := NewRecoveryRepository(NewDB(db, DialectSQLite))
	if err := repo.Migrate(); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	now := time.Now().UTC().Add(-10 * time.Minute)
	task, err := repo.CreateTask(ctx, recoverydomain.Task{ID: "recovery-interrupted", MachineID: "machine-1", MachineIP: "192.0.2.10", Status: recoverydomain.StatusExecuting, Trigger: recoverydomain.TriggerOfflineAuto, Action: recoverydomain.ActionRestart, CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveLatestState(ctx, recoverydomain.LatestState{MachineID: task.MachineID, InProgress: true, LastAttemptAt: &now, LastTaskID: task.ID, LastResult: "executing"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Migrate(); err != nil {
		t.Fatal(err)
	}
	state, found, err := repo.GetLatestState(ctx, task.MachineID)
	if err != nil || !found || state.InProgress || state.LastResult != "recovery interrupted by manager restart" {
		t.Fatalf("interrupted latest state was not reconciled: %+v found=%v err=%v", state, found, err)
	}
	tasks, err := repo.ListRecent(ctx, 10)
	if err != nil || len(tasks) != 1 || tasks[0].Status != recoverydomain.StatusFailed || tasks[0].LastError != "recovery interrupted by manager restart" {
		t.Fatalf("interrupted task was not closed: %+v err=%v", tasks, err)
	}
}
