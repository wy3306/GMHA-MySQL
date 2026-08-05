package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"

	hadomain "gmha/internal/domain/ha"
	taskdomain "gmha/internal/domain/task"

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

func TestHARepositoryTopologyIntentSurvivesRestartWithoutCredentials(t *testing.T) {
	db, err := sql.Open("sqlite", t.TempDir()+"/topology-intent.db")
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
	intent := hadomain.TopologyIntent{ClusterID: "prod", Architecture: hadomain.ArchitectureMasterSlave, PrimaryMachineID: "m1", Nodes: []hadomain.ArchitectureNodeRequest{{MachineID: "m1", Port: 3306, Role: "M"}, {MachineID: "m2", Port: 3306, Role: "S", SourceMachineID: "m1"}}}
	if err := repo.SaveTopologyIntent(context.Background(), intent); err != nil {
		t.Fatal(err)
	}
	saved, found, err := repo.GetTopologyIntent(context.Background(), "prod")
	if err != nil || !found {
		t.Fatalf("intent missing after restart: found=%v err=%v", found, err)
	}
	if saved.Architecture != hadomain.ArchitectureMasterSlave || saved.PrimaryMachineID != "m1" || len(saved.Nodes) != 2 {
		t.Fatalf("unexpected topology intent: %+v", saved)
	}
	payload, _ := json.Marshal(saved)
	if strings.Contains(strings.ToLower(string(payload)), "password") {
		t.Fatalf("topology intent persisted credentials: %s", payload)
	}
}

func TestHARepositoryBackfillsSuccessfulArchitectureRun(t *testing.T) {
	db, err := sql.Open("sqlite", t.TempDir()+"/topology-backfill.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := NewDB(db, DialectSQLite)
	if err := NewMachineRepository(store).Migrate(); err != nil {
		t.Fatal(err)
	}
	if err := NewClusterRepository(store).Migrate(); err != nil {
		t.Fatal(err)
	}
	if err := NewMySQLInstanceRepository(store).Migrate(); err != nil {
		t.Fatal(err)
	}
	if err := NewTaskRepository(store).Migrate(); err != nil {
		t.Fatal(err)
	}
	repo := NewHARepository(store)
	if err := repo.Migrate(); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	run := hadomain.ArchitectureRun{RunID: "old-success", ClusterID: "prod", Status: hadomain.ArchitectureRunSucceeded, CreatedAt: now.Add(-time.Hour), UpdatedAt: now, FinishedAt: &now, Request: hadomain.ArchitectureAdjustmentRequest{Architecture: hadomain.ArchitectureMGRRouter, PreferredNewMasterMachineID: "m1", MGRGroupName: "group-1", Nodes: []hadomain.ArchitectureNodeRequest{{MachineID: "m1", Role: "M"}, {MachineID: "m2", Role: "S"}, {MachineID: "m3", Role: "S"}}}}
	if err := repo.SaveArchitectureRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	if err := repo.BackfillTopologyIntents(context.Background()); err != nil {
		t.Fatal(err)
	}
	intent, found, err := repo.GetTopologyIntent(context.Background(), "prod")
	if err != nil || !found {
		t.Fatalf("backfilled intent missing: found=%v err=%v", found, err)
	}
	if intent.Architecture != hadomain.ArchitectureMGRRouter || intent.PrimaryMachineID != "m1" || len(intent.Nodes) != 3 {
		t.Fatalf("unexpected backfilled intent: %+v", intent)
	}
}

func TestHARepositoryBackfillsLegacyAsyncTopologyWithoutPasswords(t *testing.T) {
	db, err := sql.Open("sqlite", t.TempDir()+"/legacy-async-backfill.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := NewDB(db, DialectSQLite)
	if err := NewMachineRepository(store).Migrate(); err != nil {
		t.Fatal(err)
	}
	if err := NewClusterRepository(store).Migrate(); err != nil {
		t.Fatal(err)
	}
	if err := NewMySQLInstanceRepository(store).Migrate(); err != nil {
		t.Fatal(err)
	}
	if err := NewTaskRepository(store).Migrate(); err != nil {
		t.Fatal(err)
	}
	repo := NewHARepository(store)
	if err := repo.Migrate(); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := db.Exec(`insert into machines(id,name,ip,ssh_port,ssh_user,cluster_name,status,created_at,updated_at) values('m1','db1','10.0.0.1',22,'root','prod','online',?,?)`, now, now); err != nil {
		t.Fatal(err)
	}
	spec := taskdomain.MySQLTopologySpec{Topology: "master_slave", RootPassword: "root-secret", ReplicationPassword: "repl-secret", PrimaryMachine: "m1", Nodes: []taskdomain.MySQLTopologyNodeSpec{{MachineID: "m1", Role: "M", Port: 3306}, {MachineID: "m2", Role: "S", Port: 3306, SourceMachineID: "m1"}}}
	payload, _ := json.Marshal(spec)
	if _, err := db.Exec(`insert into tasks(id,type,machine_id,agent_id,status,spec_json,created_at,finished_at) values('topology-1','mysql_topology','m1','a1','success',?,?,?)`, string(payload), now, now); err != nil {
		t.Fatal(err)
	}
	if err := repo.BackfillTopologyIntents(context.Background()); err != nil {
		t.Fatal(err)
	}
	intent, found, err := repo.GetTopologyIntent(context.Background(), "prod")
	if err != nil || !found {
		t.Fatalf("legacy async intent missing: found=%v err=%v", found, err)
	}
	if intent.Architecture != hadomain.ArchitectureMasterSlave || len(intent.Nodes) != 2 || intent.Nodes[1].SourceMachineID != "m1" {
		t.Fatalf("unexpected async intent: %+v", intent)
	}
	encoded, _ := json.Marshal(intent)
	if strings.Contains(string(encoded), "secret") {
		t.Fatalf("legacy credentials leaked into topology intent: %s", encoded)
	}
}
