package task

import (
	"context"
	"encoding/json"
	"testing"

	agentdomain "gmha/internal/domain/agent"
	machinedomain "gmha/internal/domain/machine"
	taskdomain "gmha/internal/domain/task"
)

type execMachineRepo struct{ machine machinedomain.Machine }

func (r execMachineRepo) GetByIP(_ context.Context, ip string) (machinedomain.Machine, bool, error) {
	if r.machine.IP != ip {
		return machinedomain.Machine{}, false, nil
	}
	return r.machine, true, nil
}
func (r execMachineRepo) List(context.Context) ([]machinedomain.Machine, error) {
	return []machinedomain.Machine{r.machine}, nil
}

type execAgentRepo struct{ agent agentdomain.Agent }

func (r execAgentRepo) GetByMachineID(context.Context, string) (agentdomain.Agent, bool, error) {
	return r.agent, true, nil
}

func TestCreateExecTaskPreservesDatabaseOperationMetadata(t *testing.T) {
	machine := machinedomain.Machine{ID: "machine-1", IP: "10.0.0.8"}
	agent := agentdomain.Agent{ID: "agent-1", MachineID: machine.ID, State: agentdomain.StateOnline}
	usecase := NewCreateExecTaskUsecase(execMachineRepo{machine: machine}, execAgentRepo{agent: agent})

	result, err := usecase.Execute(context.Background(), CreateExecTaskRequest{
		Machine: machine.IP, Command: "mysql -e 'select 1'", Operation: "mysql_collect",
		DisplayName: "采集 MySQL 运行数据", StepName: "查询数据库运行状态", Port: 3307,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	var spec taskdomain.ExecSpec
	if err := json.Unmarshal(result.Task.SpecJSON, &spec); err != nil {
		t.Fatalf("unmarshal spec: %v", err)
	}
	if spec.Operation != "mysql_collect" || spec.DisplayName != "采集 MySQL 运行数据" || spec.Port != 3307 {
		t.Fatalf("unexpected operation metadata: %+v", spec)
	}
	if len(result.Steps) != 1 || result.Steps[0].StepName != "查询数据库运行状态" {
		t.Fatalf("unexpected task steps: %+v", result.Steps)
	}
	if len(result.Events) != 1 || result.Events[0].Content != "采集 MySQL 运行数据任务已创建" {
		t.Fatalf("unexpected task event: %+v", result.Events)
	}
}

func TestCreateExecTaskResolvesMachineID(t *testing.T) {
	machine := machinedomain.Machine{ID: "machine-db315bd06bd5de65", Name: "db-primary", IP: "10.0.0.8"}
	agent := agentdomain.Agent{ID: "agent-1", MachineID: machine.ID, State: agentdomain.StateOnline}
	usecase := NewCreateExecTaskUsecase(execMachineRepo{machine: machine}, execAgentRepo{agent: agent})

	result, err := usecase.Execute(context.Background(), CreateExecTaskRequest{
		Machine: machine.ID, Command: "agent-native-stack-sampling", TaskType: taskdomain.TypeFlameGraph,
	})
	if err != nil {
		t.Fatalf("Execute() with machine ID error = %v", err)
	}
	if result.Task.MachineID != machine.ID {
		t.Fatalf("task machine ID = %q, want %q", result.Task.MachineID, machine.ID)
	}
}

func TestCreateExecTaskBuildsLoggedWorkflowSteps(t *testing.T) {
	machine := machinedomain.Machine{ID: "machine-1", IP: "10.0.0.8"}
	agent := agentdomain.Agent{ID: "agent-1", MachineID: machine.ID, State: agentdomain.StateOnline}
	usecase := NewCreateExecTaskUsecase(execMachineRepo{machine: machine}, execAgentRepo{agent: agent})
	commands := []taskdomain.ExecCommandStep{{Name: "兼容性检查", Command: "mysql --version"}, {Name: "软连接切换", Command: "ln -sfn target current"}}
	result, err := usecase.Execute(context.Background(), CreateExecTaskRequest{Machine: machine.IP, Commands: commands, RollbackCommand: "ln -sfn old current", Operation: "mysql_upgrade", PackageName: "mysql-8.4.6-linux-glibc2.17-x86_64.tar.xz"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Steps) != 2 || result.Steps[0].StepName != "兼容性检查" || result.Steps[1].StepName != "软连接切换" {
		t.Fatalf("unexpected workflow steps: %#v", result.Steps)
	}
	var spec taskdomain.ExecSpec
	if err := json.Unmarshal(result.Task.SpecJSON, &spec); err != nil {
		t.Fatal(err)
	}
	if len(spec.Commands) != 2 || spec.RollbackCommand == "" || spec.PackageName == "" {
		t.Fatalf("workflow was not preserved: %#v", spec)
	}
}
