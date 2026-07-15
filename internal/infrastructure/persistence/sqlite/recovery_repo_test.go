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
