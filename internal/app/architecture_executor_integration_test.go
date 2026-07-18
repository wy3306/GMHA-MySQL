package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	agentdomain "gmha/internal/domain/agent"
	clusterdomain "gmha/internal/domain/cluster"
	hadomain "gmha/internal/domain/ha"
	machinedomain "gmha/internal/domain/machine"
	taskdomain "gmha/internal/domain/task"
	persistencesqlite "gmha/internal/infrastructure/persistence/sqlite"
	mysqlapp "gmha/internal/mysql"
	taskusecase "gmha/internal/usecase/task"

	_ "modernc.org/sqlite"
)

// recordingArchitectureAgent models the Manager/Agent transport boundary. It
// records the exact exec task received by an Agent and reports completion only
// after TaskService has persisted the dispatched state.
type recordingArchitectureAgent struct {
	service  *TaskService
	serverID int
	mu       sync.Mutex
	commands []string
}

func (a *recordingArchitectureAgent) Send(envelope taskdomain.DispatchEnvelope) error {
	var spec taskdomain.ExecSpec
	if err := json.Unmarshal(envelope.Task.Spec, &spec); err != nil {
		return err
	}
	command := spec.Command
	a.mu.Lock()
	a.commands = append(a.commands, command)
	a.mu.Unlock()
	step := envelope.Task.Steps[0]
	go func() {
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			task, found, err := a.service.repo.GetTask(context.Background(), envelope.Task.ID)
			if err == nil && found && task.Status == taskdomain.StatusSent {
				now := time.Now().UTC()
				message := "OK"
				if strings.Contains(command, "CONCAT_WS") && strings.Contains(command, "@@global.gtid_executed") {
					message = strings.Join([]string{strconv.Itoa(a.serverID), "ON", "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa:1-10", "0", "0"}, "|")
				} else if strings.Contains(command, "CONCAT_WS") && strings.Contains(command, "@@global.gtid_mode") {
					message = strings.Join([]string{strconv.Itoa(a.serverID), "ON", "0", "0"}, "|")
				}
				_ = a.service.HandleReport(context.Background(), taskdomain.ReportEnvelope{
					TaskID: envelope.Task.ID, Status: taskdomain.StatusSuccess, Progress: 100, CurrentStep: step.StepName,
					Step: &taskdomain.StepReport{StepID: step.ID, StepNo: step.StepNo, StepName: step.StepName, Status: taskdomain.StepSuccess, Message: message, StartedAt: &now, FinishedAt: &now},
				})
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()
	return nil
}

func (a *recordingArchitectureAgent) joinedCommands() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return strings.Join(a.commands, "\n")
}

func TestArchitectureLifecycleRunsThroughAgentPTAndTaskCenter(t *testing.T) {
	db, err := sql.Open("sqlite", t.TempDir()+"/architecture-integration.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	store := persistencesqlite.NewDB(db, persistencesqlite.DialectSQLite)
	clusterRepo := persistencesqlite.NewClusterRepository(store)
	machineRepo := persistencesqlite.NewMachineRepository(store)
	agentRepo := persistencesqlite.NewAgentRepository(store)
	instanceRepo := persistencesqlite.NewMySQLInstanceRepository(store)
	taskRepo := persistencesqlite.NewTaskRepository(store)
	haRepo := persistencesqlite.NewHARepository(store)
	for name, migrate := range map[string]func() error{
		"cluster": clusterRepo.Migrate, "machine": machineRepo.Migrate, "agent": agentRepo.Migrate,
		"instance": instanceRepo.Migrate, "task": taskRepo.Migrate, "ha": haRepo.Migrate,
	} {
		if err := migrate(); err != nil {
			t.Fatalf("migrate %s: %v", name, err)
		}
	}
	ctx := context.Background()
	if err := clusterRepo.Create(ctx, clusterdomain.Cluster{Name: "demo"}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	machines := []machinedomain.Machine{
		{ID: "db-1", Name: "DB-01", IP: "10.0.0.1", SSHPort: 22, SSHUser: "root", Cluster: "demo", Status: machinedomain.StatusAgentOnline, CreatedAt: now, UpdatedAt: now},
		{ID: "db-2", Name: "DB-02", IP: "10.0.0.2", SSHPort: 22, SSHUser: "root", Cluster: "demo", Status: machinedomain.StatusAgentOnline, CreatedAt: now, UpdatedAt: now},
	}
	for index, machine := range machines {
		if _, err := machineRepo.Save(ctx, machine); err != nil {
			t.Fatal(err)
		}
		if _, err := agentRepo.Save(ctx, agentdomain.Agent{ID: "agent-" + machine.ID, MachineID: machine.ID, State: agentdomain.StateOnline, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
		if err := instanceRepo.Save(ctx, mysqlapp.Instance{MachineID: machine.ID, Port: 3306, ServerID: index + 1, Status: mysqlapp.StatusRunning, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	createExec := taskusecase.NewCreateExecTaskUsecase(machineRepo, agentRepo)
	tasks := NewTaskService(taskRepo, createExec, nil, nil, nil, nil, nil, nil, nil, machineRepo, instanceRepo)
	agent1 := &recordingArchitectureAgent{service: tasks, serverID: 1}
	agent2 := &recordingArchitectureAgent{service: tasks, serverID: 2}
	capabilities := []string{string(taskdomain.TypeExec), taskdomain.CapabilityMySQLDefaultsFile}
	tasks.RegisterAgentForMachineWithCapabilities("agent-db-1", "db-1", agent1, capabilities)
	tasks.RegisterAgentForMachineWithCapabilities("agent-db-2", "db-2", agent2, capabilities)
	service := NewHAService(haRepo, machineRepo, instanceRepo)
	service.ConfigureArchitectureExecutor(tasks)

	run, err := service.StartArchitectureAdjustment(ctx, "demo", hadomain.ArchitectureAdjustmentRequest{
		Architecture: hadomain.ArchitectureStandalone, CurrentMasterMachineID: "db-1",
		Nodes: []hadomain.ArchitectureNodeRequest{{MachineID: "db-1", Port: 3306, Role: "I"}, {MachineID: "db-2", Port: 3306, Role: "I"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	completed := waitArchitectureIntegrationRun(t, service, run.RunID)
	if completed.Status != hadomain.ArchitectureRunSucceeded {
		t.Fatalf("architecture run did not succeed: status=%s step=%s error=%s\ncommands:\n%s\n%s", completed.Status, completed.CurrentStep, completed.Error, agent1.joinedCommands(), agent2.joinedCommands())
	}
	commands := agent1.joinedCommands() + "\n" + agent2.joinedCommands()
	for _, required := range []string{"__GMHA_MYSQL_DEFAULTS_FILE__", "pt-table-checksum", "RESET REPLICA ALL", "offline_mode=ON", "offline_mode=OFF"} {
		if !strings.Contains(commands, required) {
			t.Fatalf("Manager did not dispatch required Agent command %q", required)
		}
	}
	detail, err := tasks.GetTaskDetail(ctx, run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Task.Type != taskdomain.TypeArchitecture || detail.Task.Status != taskdomain.StatusSuccess || len(detail.ChildDetails) == 0 {
		t.Fatalf("architecture workflow was not recorded with Agent children: %+v", detail)
	}
	for _, step := range detail.Steps {
		if step.Status != taskdomain.StepSuccess {
			t.Fatalf("task-center step %s status=%s, want success", step.StepName, step.Status)
		}
	}

	dualRun, err := service.StartArchitectureAdjustment(ctx, "demo", hadomain.ArchitectureAdjustmentRequest{
		Architecture: hadomain.ArchitectureDualMaster, PreferredNewMasterMachineID: "db-1",
		Nodes: []hadomain.ArchitectureNodeRequest{{MachineID: "db-1", Port: 3306, Role: "M", ElectionPriority: 100}, {MachineID: "db-2", Port: 3306, Role: "M", ElectionPriority: 90}},
	})
	if err != nil {
		t.Fatal(err)
	}
	completed = waitArchitectureIntegrationRun(t, service, dualRun.RunID)
	if completed.Status != hadomain.ArchitectureRunSucceeded {
		t.Fatalf("standalone-to-dual-master run failed: step=%s error=%s", completed.CurrentStep, completed.Error)
	}

	swapRun, err := service.StartArchitectureAdjustment(ctx, "demo", hadomain.ArchitectureAdjustmentRequest{
		Architecture: hadomain.ArchitectureMasterSlave, CurrentMasterMachineID: "db-1", PreferredNewMasterMachineID: "db-2",
		Nodes: []hadomain.ArchitectureNodeRequest{{MachineID: "db-1", Port: 3306, Role: "S", SourceMachineID: "db-2", ElectionPriority: 90}, {MachineID: "db-2", Port: 3306, Role: "M", ElectionPriority: 100}},
	})
	if err != nil {
		t.Fatal(err)
	}
	completed = waitArchitectureIntegrationRun(t, service, swapRun.RunID)
	if completed.Status != hadomain.ArchitectureRunSucceeded {
		t.Fatalf("master/replica swap run failed: step=%s error=%s", completed.CurrentStep, completed.Error)
	}
	commands = agent1.joinedCommands() + "\n" + agent2.joinedCommands()
	for _, required := range []string{"CHANGE REPLICATION SOURCE TO", "SOURCE_AUTO_POSITION=1", "GET_SOURCE_PUBLIC_KEY=1", "START REPLICA", "pt-table-checksum"} {
		if !strings.Contains(commands, required) {
			t.Fatalf("replication lifecycle did not dispatch %q", required)
		}
	}
	for _, runID := range []string{dualRun.RunID, swapRun.RunID} {
		detail, err := tasks.GetTaskDetail(ctx, runID)
		if err != nil {
			t.Fatal(err)
		}
		if detail.Task.Status != taskdomain.StatusSuccess || len(detail.ChildDetails) == 0 {
			t.Fatalf("run %s was not fully recorded in task center: %+v", runID, detail)
		}
	}
}

func waitArchitectureIntegrationRun(t *testing.T, service *HAService, runID string) hadomain.ArchitectureRun {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		run, found, err := service.GetArchitectureRun(context.Background(), "demo", runID)
		if err != nil {
			t.Fatal(err)
		}
		if found && (run.Status == hadomain.ArchitectureRunSucceeded || run.Status == hadomain.ArchitectureRunFailed) {
			return run
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for architecture run %s", runID)
	return hadomain.ArchitectureRun{}
}
