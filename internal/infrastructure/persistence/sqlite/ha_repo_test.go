package sqlite

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestHARepositoryFailoverLockIsClusterScoped(t *testing.T) {
	db, err := sql.Open("sqlite", t.TempDir()+"/ha.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	clusterRepo := NewClusterRepository(db)
	if err := clusterRepo.Migrate(); err != nil {
		t.Fatal(err)
	}
	mysqlRepo := NewMySQLInstanceRepository(db)
	if err := mysqlRepo.Migrate(); err != nil {
		t.Fatal(err)
	}
	repo := NewHARepository(db)
	if err := repo.Migrate(); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := repo.AcquireFailoverLock(ctx, "c1", "fo-1", "test", time.Minute); err != nil {
		t.Fatalf("first lock failed: %v", err)
	}
	if err := repo.AcquireFailoverLock(ctx, "c1", "fo-2", "test", time.Minute); err == nil {
		t.Fatal("expected second lock for same cluster to fail")
	}
	if err := repo.AcquireFailoverLock(ctx, "c2", "fo-3", "test", time.Minute); err != nil {
		t.Fatalf("different cluster lock should succeed: %v", err)
	}
	if err := repo.ReleaseFailoverLock(ctx, "c1", "fo-1"); err != nil {
		t.Fatal(err)
	}
	if err := repo.AcquireFailoverLock(ctx, "c1", "fo-4", "test", time.Minute); err != nil {
		t.Fatalf("lock after release failed: %v", err)
	}
}
