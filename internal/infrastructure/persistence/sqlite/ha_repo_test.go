package sqlite

import (
	"context"
	"database/sql"
	"testing"
	"time"

	hadomain "gmha/internal/domain/ha"

	_ "modernc.org/sqlite"
)

func TestHARepositoryFailoverLockIsClusterScoped(t *testing.T) {
	db, err := sql.Open("sqlite", t.TempDir()+"/ha.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := NewDB(db, DialectSQLite)
	clusterRepo := NewClusterRepository(store)
	if err := clusterRepo.Migrate(); err != nil {
		t.Fatal(err)
	}
	mysqlRepo := NewMySQLInstanceRepository(store)
	if err := mysqlRepo.Migrate(); err != nil {
		t.Fatal(err)
	}
	repo := NewHARepository(store)
	if err := repo.Migrate(); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := repo.AcquireFailoverLock(ctx, "c1", "fo-1", "test", time.Minute); err != nil {
		t.Fatalf("first lock failed: %v", err)
	}
	if err := repo.RenewFailoverLock(ctx, "c1", "fo-1", 2*time.Minute); err != nil {
		t.Fatalf("lock renewal failed: %v", err)
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

func TestHARepositoryFailoverLockCanAtomicallyReplaceExpiredOwner(t *testing.T) {
	db, err := sql.Open("sqlite", t.TempDir()+"/expired-lock.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := NewDB(db, DialectSQLite)
	if err := NewClusterRepository(store).Migrate(); err != nil {
		t.Fatal(err)
	}
	if err := NewMySQLInstanceRepository(store).Migrate(); err != nil {
		t.Fatal(err)
	}
	repo := NewHARepository(store)
	if err := repo.Migrate(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`insert into failover_lock(cluster_id,failover_id,lock_owner,locked_at,expires_at) values('c1','old','test','2020-01-01T00:00:00Z','2020-01-01T00:01:00Z')`); err != nil {
		t.Fatal(err)
	}
	if err := repo.AcquireFailoverLock(context.Background(), "c1", "new", "test", time.Minute); err != nil {
		t.Fatalf("expired lock should be replaced: %v", err)
	}
	var owner string
	if err := db.QueryRow(`select failover_id from failover_lock where cluster_id='c1'`).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	if owner != "new" {
		t.Fatalf("lock owner = %q, want new", owner)
	}
}

func TestHARepositoryVIPConfigRoundTripIncludesBGP(t *testing.T) {
	db, err := sql.Open("sqlite", t.TempDir()+"/vip.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := NewDB(db, DialectSQLite)
	if err := NewClusterRepository(store).Migrate(); err != nil {
		t.Fatal(err)
	}
	if err := NewMySQLInstanceRepository(store).Migrate(); err != nil {
		t.Fatal(err)
	}
	repo := NewHARepository(store)
	if err := repo.Migrate(); err != nil {
		t.Fatal(err)
	}
	input := hadomain.ClusterVIPConfig{ClusterID: "demo", VIPName: "business", VIPAddress: "10.0.0.100", VIPPrefix: 32, VIPRouteMode: hadomain.VipRouteModeBGP, VIPManageMode: "GMHA_MANAGED", BGPEnabled: true, BGPLocalAS: 65000, BGPPeerAS: 65001, BGPPeerAddress: "10.0.0.254", BGPRouterID: "10.0.0.1", BGPCommunity: "65000:100", Enabled: true}
	saved, err := repo.UpsertVIPConfig(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if saved.BGPLocalAS != input.BGPLocalAS || saved.BGPPeerAddress != input.BGPPeerAddress || saved.BGPCommunity != input.BGPCommunity {
		t.Fatalf("BGP fields did not round trip: %+v", saved)
	}
}

func TestHARepositoryArchitectureRunRoundTripAndRestartRecovery(t *testing.T) {
	db, err := sql.Open("sqlite", t.TempDir()+"/architecture.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := NewDB(db, DialectSQLite)
	if err := NewClusterRepository(store).Migrate(); err != nil {
		t.Fatal(err)
	}
	if err := NewMySQLInstanceRepository(store).Migrate(); err != nil {
		t.Fatal(err)
	}
	repo := NewHARepository(store)
	if err := repo.Migrate(); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	run := hadomain.ArchitectureRun{RunID: "run-1", ClusterID: "demo", Status: hadomain.ArchitectureRunRunning, CurrentStep: "promote_new_master", CreatedAt: now, UpdatedAt: now, Request: hadomain.ArchitectureAdjustmentRequest{RootPassword: "", ReplicationPassword: ""}}
	if err := repo.SaveArchitectureRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	if err := repo.MarkInterruptedArchitectureRuns(context.Background()); err != nil {
		t.Fatal(err)
	}
	saved, found, err := repo.GetArchitectureRun(context.Background(), "demo", "run-1")
	if err != nil || !found {
		t.Fatalf("run not found after recovery: found=%v err=%v", found, err)
	}
	if saved.Status != hadomain.ArchitectureRunFailed || saved.CurrentStep != "manager_restart_recovery" || saved.FinishedAt == nil {
		t.Fatalf("interrupted run was not reconciled: %+v", saved)
	}
	if saved.Request.RootPassword != "" || saved.Request.ReplicationPassword != "" {
		t.Fatal("architecture credentials must never be persisted")
	}
}
