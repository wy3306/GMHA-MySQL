package sqlite

import (
	"context"
	"database/sql"
	"testing"
	"time"

	managerdomain "gmha/internal/domain/manager"

	_ "modernc.org/sqlite"
)

func TestManagerHARepositoryPersistsTopologyAndActiveNode(t *testing.T) {
	db, err := sql.Open("sqlite", t.TempDir()+"/manager-ha.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := NewManagerHARepository(NewDB(db, DialectSQLite))
	if err := repo.Migrate(); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	cfg := managerdomain.HAConfig{Enabled: true, VIP: "10.0.0.100", Prefix: 24, Interface: "eth0", InstallDir: "/opt/gmha", ServiceName: "gmha-manager"}
	if err := repo.SaveConfig(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	gotCfg, err := repo.GetConfig(ctx)
	if err != nil || !gotCfg.Enabled || gotCfg.VIP != cfg.VIP {
		t.Fatalf("unexpected config: %+v err=%v", gotCfg, err)
	}
	for _, node := range []managerdomain.Node{
		{ID: "m1", MachineID: "machine-1", Name: "manager-1", IP: "10.0.0.11", HTTPAddress: "http://10.0.0.11:8080", GRPCAddress: "10.0.0.11:9100", VIPInterface: "bond0", Role: "active", State: "online"},
		{ID: "m2", MachineID: "machine-2", Name: "manager-2", IP: "10.0.0.12", HTTPAddress: "http://10.0.0.12:8080", GRPCAddress: "10.0.0.12:9100", VIPInterface: "ens192", Role: "standby", State: "online"},
	} {
		if err := repo.SaveNode(ctx, node); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.SetActive(ctx, "m2", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	items, err := repo.ListNodes(ctx)
	if err != nil || len(items) != 2 {
		t.Fatalf("unexpected nodes: %+v err=%v", items, err)
	}
	for _, item := range items {
		if item.ID == "m2" && item.Role != "active" {
			t.Fatalf("m2 role = %s", item.Role)
		}
		if item.ID == "m2" && item.VIPInterface != "ens192" {
			t.Fatalf("m2 vip interface = %s", item.VIPInterface)
		}
		if item.ID == "m1" && item.Role != "standby" {
			t.Fatalf("m1 role = %s", item.Role)
		}
	}
}
