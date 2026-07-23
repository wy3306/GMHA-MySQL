package app_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"gmha/internal/app"
	clusterdomain "gmha/internal/domain/cluster"
	hadomain "gmha/internal/domain/ha"
	"gmha/internal/infrastructure/persistence/sqlite"
	_ "modernc.org/sqlite"
)

func TestListClustersExposesNameAsClusterID(t *testing.T) {
	db, err := sql.Open("sqlite", t.TempDir()+"/clusters.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	database := sqlite.NewDB(db, sqlite.DialectSQLite)
	machines := sqlite.NewMachineRepository(database)
	clusters := sqlite.NewClusterRepository(database)
	if err := machines.Migrate(); err != nil {
		t.Fatal(err)
	}
	if err := clusters.Migrate(); err != nil {
		t.Fatal(err)
	}
	if err := clusters.Create(context.Background(), clusterdomain.Cluster{
		Name:        "demo",
		Description: "demo cluster",
	}); err != nil {
		t.Fatal(err)
	}

	service := app.NewMachineService(nil, machines, clusters, nil, nil, nil, nil, nil, nil, nil)
	items, err := service.ListClusters(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("expected one cluster, got %d", len(items))
	}
	if items[0].ID != "demo" {
		t.Fatalf("expected cluster ID to match its unique name, got %q", items[0].ID)
	}
}

func TestClusterRenameAndDeleteRejectUnmigratedVIPReferences(t *testing.T) {
	db, err := sql.Open("sqlite", t.TempDir()+"/cluster-dependencies.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	database := sqlite.NewDB(db, sqlite.DialectSQLite)
	machines := sqlite.NewMachineRepository(database)
	clusters := sqlite.NewClusterRepository(database)
	instances := sqlite.NewMySQLInstanceRepository(database)
	haRepo := sqlite.NewHARepository(database)
	for _, migrate := range []func() error{machines.Migrate, clusters.Migrate, instances.Migrate, haRepo.Migrate} {
		if err := migrate(); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	if err := clusters.Create(ctx, clusterdomain.Cluster{Name: "prod"}); err != nil {
		t.Fatal(err)
	}
	ha := app.NewHAService(haRepo, machines, instances)
	if _, err := ha.SaveVIPConfig(ctx, "prod", hadomain.ClusterVIPConfig{
		VIPName: "业务 VIP", VIPAddress: "10.0.0.100", VIPPrefix: 24, DefaultInterface: "eth0",
	}); err != nil {
		t.Fatal(err)
	}
	service := app.NewMachineService(nil, machines, clusters, nil, nil, nil, nil, nil, nil, nil)
	service.ConfigureClusterDependencies(ha, nil)

	if err := service.UpdateCluster(ctx, "prod", "prod-new", "renamed"); err == nil || !strings.Contains(err.Error(), "业务 VIP") {
		t.Fatalf("cluster rename did not protect VIP references: %v", err)
	}
	if err := service.DeleteCluster(ctx, "prod"); err == nil || !strings.Contains(err.Error(), "业务 VIP") {
		t.Fatalf("cluster delete did not protect VIP references: %v", err)
	}
	if items, err := service.ListClusters(ctx); err != nil || len(items) != 1 || items[0].Name != "prod" {
		t.Fatalf("blocked operations changed the cluster: %#v, %v", items, err)
	}
}

func TestClusterCleanupDeletesAnEmptyCluster(t *testing.T) {
	db, err := sql.Open("sqlite", t.TempDir()+"/empty-cleanup.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	database := sqlite.NewDB(db, sqlite.DialectSQLite)
	machines := sqlite.NewMachineRepository(database)
	clusters := sqlite.NewClusterRepository(database)
	tasks := sqlite.NewTaskRepository(database)
	for _, migrate := range []func() error{machines.Migrate, clusters.Migrate, tasks.Migrate} {
		if err := migrate(); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	if err := clusters.Create(ctx, clusterdomain.Cluster{Name: "empty"}); err != nil {
		t.Fatal(err)
	}
	taskService := app.NewTaskService(tasks, nil, nil, nil, nil, nil, nil, nil, nil, machines, nil)
	service := app.NewMachineService(nil, machines, clusters, nil, nil, nil, nil, nil, nil, taskService)

	result, err := service.CleanupCluster(ctx, "empty")
	if err != nil {
		t.Fatal(err)
	}
	if result.Cluster != "empty" || len(result.Items) != 0 {
		t.Fatalf("unexpected empty cleanup result: %#v", result)
	}
	if items, err := service.ListClusters(ctx); err != nil || len(items) != 0 {
		t.Fatalf("empty cluster still exists after cleanup: %#v, %v", items, err)
	}
}
