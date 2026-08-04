package app

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sqliteinfra "gmha/internal/infrastructure/persistence/sqlite"
)

func newWALMaintenanceTestDatabase(t *testing.T) (*sql.DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "manager.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`
		pragma journal_mode = wal;
		pragma wal_autocheckpoint = 0;
		create table wal_test (id integer primary key, payload text not null);
	`); err != nil {
		t.Fatal(err)
	}
	payload := strings.Repeat("wal-data-", 1024)
	for i := 0; i < 64; i++ {
		if _, err := db.Exec(`insert into wal_test(payload) values (?)`, payload); err != nil {
			t.Fatal(err)
		}
	}
	return db, path
}

func TestDatabaseMaintenanceServiceStatusAndCleanup(t *testing.T) {
	db, path := newWALMaintenanceTestDatabase(t)
	service := NewDatabaseMaintenanceService(db, sqliteinfra.DialectSQLite, Config{
		DBPath:         path,
		DatabaseDriver: "sqlite",
	})

	before, err := service.WALStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !before.Supported || before.JournalMode != "wal" {
		t.Fatalf("unexpected WAL status: %+v", before)
	}
	if before.WALBytes == 0 {
		t.Fatalf("expected WAL data before cleanup: %+v", before)
	}

	result, err := service.CleanupWAL(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !result.Completed || result.Busy != 0 {
		t.Fatalf("cleanup did not complete: %+v", result)
	}
	if result.Before.WALBytes == 0 || result.After.WALBytes != 0 {
		t.Fatalf("WAL was not truncated: before=%d after=%d", result.Before.WALBytes, result.After.WALBytes)
	}
	if result.ReleasedBytes != result.Before.WALBytes {
		t.Fatalf("unexpected released bytes: %+v", result)
	}
	if info, err := os.Stat(path + "-wal"); err != nil || info.Size() != 0 {
		t.Fatalf("WAL file is not empty: info=%v err=%v", info, err)
	}
}

func TestDatabaseMaintenanceServiceRejectsNonSQLite(t *testing.T) {
	service := NewDatabaseMaintenanceService(nil, sqliteinfra.DialectMySQL, Config{
		DatabaseDriver: "mysql",
		DatabaseDSN:    "gmha:top-secret@tcp(database.internal:3306)/gmha",
	})
	status, err := service.WALStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Supported || status.Driver != "mysql" || status.Reason == "" || status.DatabasePath != "" {
		t.Fatalf("unexpected unsupported status: %+v", status)
	}
	if _, err := service.CleanupWAL(context.Background()); !errors.Is(err, ErrWALCleanupUnsupported) {
		t.Fatalf("expected unsupported cleanup error, got %v", err)
	}
}

func TestDatabaseMaintenanceServiceReportsBusyReader(t *testing.T) {
	db, path := newWALMaintenanceTestDatabase(t)
	reader, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	reader.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = reader.Close() })

	tx, err := reader.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var count int
	if err := tx.QueryRow(`select count(*) from wal_test`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`insert into wal_test(payload) values ('newer-than-reader')`); err != nil {
		t.Fatal(err)
	}

	service := NewDatabaseMaintenanceService(db, sqliteinfra.DialectSQLite, Config{DBPath: path, DatabaseDriver: "sqlite"})
	result, err := service.CleanupWAL(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Completed || result.Busy == 0 {
		t.Fatalf("expected a busy checkpoint result, got %+v", result)
	}
	if !strings.Contains(result.Message, "活跃读取事务") {
		t.Fatalf("unexpected busy message: %q", result.Message)
	}
}

func TestDatabaseMaintenanceServiceRejectsConcurrentCleanup(t *testing.T) {
	db, path := newWALMaintenanceTestDatabase(t)
	service := NewDatabaseMaintenanceService(db, sqliteinfra.DialectSQLite, Config{DBPath: path, DatabaseDriver: "sqlite"})
	service.cleaning.Store(true)
	if _, err := service.CleanupWAL(context.Background()); !errors.Is(err, ErrWALCleanupInProgress) {
		t.Fatalf("expected in-progress error, got %v", err)
	}
}

func TestSQLiteMaintenanceFilePathHandlesURIAndMemory(t *testing.T) {
	want := filepath.Join(t.TempDir(), "manager db.sqlite")
	got := sqliteMaintenanceFilePath(Config{DatabaseDSN: "file:" + strings.ReplaceAll(want, " ", "%20") + "?cache=shared"})
	if got != want {
		t.Fatalf("path=%q want=%q", got, want)
	}
	if got := sqliteMaintenanceFilePath(Config{DatabaseDSN: "file:memory-db?mode=memory&cache=shared"}); got != "" {
		t.Fatalf("memory DSN resolved to %q", got)
	}
}
