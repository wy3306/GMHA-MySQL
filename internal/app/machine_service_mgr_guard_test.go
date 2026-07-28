package app

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	agentdomain "gmha/internal/domain/agent"
	clusterdomain "gmha/internal/domain/cluster"
	machinedomain "gmha/internal/domain/machine"
	taskdomain "gmha/internal/domain/task"
	persistencesqlite "gmha/internal/infrastructure/persistence/sqlite"
	mysqlapp "gmha/internal/mysql"
	taskusecase "gmha/internal/usecase/task"

	_ "modernc.org/sqlite"
)

func TestActiveMGRMemberBlocksClusterRenameDeleteAndUnassign(t *testing.T) {
	db, err := sql.Open("sqlite", t.TempDir()+"/machine-mgr-guard.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	store := persistencesqlite.NewDB(db, persistencesqlite.DialectSQLite)
	clusters := persistencesqlite.NewClusterRepository(store)
	machines := persistencesqlite.NewMachineRepository(store)
	agents := persistencesqlite.NewAgentRepository(store)
	instances := persistencesqlite.NewMySQLInstanceRepository(store)
	tasksRepo := persistencesqlite.NewTaskRepository(store)
	haRepo := persistencesqlite.NewHARepository(store)
	for name, migrate := range map[string]func() error{
		"cluster":  clusters.Migrate,
		"machine":  machines.Migrate,
		"agent":    agents.Migrate,
		"instance": instances.Migrate,
		"task":     tasksRepo.Migrate,
		"ha":       haRepo.Migrate,
	} {
		if err := migrate(); err != nil {
			t.Fatalf("migrate %s: %v", name, err)
		}
	}

	ctx := context.Background()
	if err := clusters.Create(ctx, clusterdomain.Cluster{Name: "prod"}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	machine := machinedomain.Machine{
		ID: "db-1", Name: "DB-01", IP: "10.0.0.1", SSHPort: 22, SSHUser: "root",
		Cluster: "prod", Status: machinedomain.StatusAgentOnline, CreatedAt: now, UpdatedAt: now,
	}
	if _, err := machines.Save(ctx, machine); err != nil {
		t.Fatal(err)
	}
	if _, err := agents.Save(ctx, agentdomain.Agent{
		ID: "agent-db-1", MachineID: machine.ID, State: agentdomain.StateOnline, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := instances.Save(ctx, mysqlapp.Instance{
		MachineID: machine.ID, Port: 3306, ServerID: 101, Status: mysqlapp.StatusRunning, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	createExec := taskusecase.NewCreateExecTaskUsecase(machines, agents)
	createTopology := taskusecase.NewCreateMySQLTopologyTaskUsecase(machines, agents, instances)
	tasks := NewTaskService(tasksRepo, createExec, nil, nil, nil, nil, createTopology, nil, nil, machines, instances)
	agent := &recordingArchitectureAgent{service: tasks, failMGRGuard: true}
	tasks.RegisterAgentForMachineWithCapabilities(
		"agent-db-1", machine.ID, agent,
		[]string{string(taskdomain.TypeExec), taskdomain.CapabilityMySQLDefaultsFile},
	)
	ha := NewHAService(haRepo, machines, instances)
	ha.ConfigureArchitectureExecutor(tasks)
	tasks.ConfigureClusterSafetyDependencies(ha, nil)
	service := NewMachineService(nil, machines, clusters, nil, nil, nil, nil, nil, nil, tasks)
	service.ConfigureClusterDependencies(ha, nil)

	for name, operation := range map[string]func() error{
		"rename":   func() error { return service.UpdateCluster(ctx, "prod", "prod-new", "") },
		"delete":   func() error { return service.DeleteCluster(ctx, "prod") },
		"unassign": func() error { return service.UnassignMachineCluster(ctx, machine.ID) },
		"traditional_topology": func() error {
			_, err := tasks.CreateMySQLTopologyTasks(ctx, taskusecase.CreateMySQLTopologyTaskRequest{
				Topology: "master_slave", Port: 3306, RootPassword: "root-secret",
				Nodes: []taskusecase.CreateMySQLTopologyNodeRequest{
					{Machine: machine.ID, Port: 3306, Role: "M"},
					{Machine: "db-2", Port: 3306, Role: "S"},
				},
			})
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			err := operation()
			if err == nil || !strings.Contains(err.Error(), "活动 MGR") {
				t.Fatalf("active MGR %s should be blocked, got %v", name, err)
			}
		})
	}

	stored, found, err := machines.GetByID(ctx, machine.ID)
	if err != nil || !found || stored.Cluster != "prod" {
		t.Fatalf("blocked operations changed membership: %+v, found=%v err=%v", stored, found, err)
	}
	if _, found, err := clusters.Get(ctx, "prod"); err != nil || !found {
		t.Fatalf("blocked operations changed cluster: found=%v err=%v", found, err)
	}
}
