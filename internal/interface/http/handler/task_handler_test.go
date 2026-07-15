package handler

import (
	"strings"
	"testing"

	taskdomain "gmha/internal/domain/task"
)

func validAutomationRequest() clusterAutomationRequest {
	return clusterAutomationRequest{
		Clusters:       []string{"cluster-a"},
		Port:           3306,
		MySQLUser:      "root",
		MySQLPassword:  "secret",
		TargetUsername: "app_user",
		TargetPassword: "new-secret",
		TargetHost:     "%",
		Privileges:     []string{"SELECT", "PROCESS"},
	}
}

func TestTaskStatusFilterGroupsLifecycleStates(t *testing.T) {
	running := taskStatusFilter("running")
	if len(running) != 3 || running[0] != taskdomain.StatusPending || running[2] != taskdomain.StatusRunning {
		t.Fatalf("unexpected running status filter: %+v", running)
	}
	if got := taskStatusFilter("all"); len(got) != 0 {
		t.Fatalf("all should not constrain status: %+v", got)
	}
}

func TestTaskTypeFilterSupportsPlatformOperations(t *testing.T) {
	got := taskTypeFilter("platform_operation")
	if len(got) != 1 || got[0] != taskdomain.TypePlatformOperation {
		t.Fatalf("unexpected type filter: %+v", got)
	}
}

func TestClusterAutomationCommandForDatabaseUser(t *testing.T) {
	req := validAutomationRequest()
	req.Operation = "mysql_user"
	req.UserAction = "grant"

	command, err := clusterAutomationCommand(req)
	if err != nil {
		t.Fatalf("clusterAutomationCommand() error = %v", err)
	}
	if !strings.Contains(command, "GRANT SELECT, PROCESS ON *.* TO") || !strings.Contains(command, "app_user") {
		t.Fatalf("generated command did not contain expected grant: %s", command)
	}
}

func TestDatabaseAutomationTaskOptionsDescribeOperation(t *testing.T) {
	req := validAutomationRequest()
	req.Operation = "mysql_user"
	req.UserAction = "grant"

	opts, ok := databaseAutomationTaskOptions(req)
	if !ok {
		t.Fatal("mysql_user should be recognized as a database task")
	}
	if opts.Operation != "mysql_user_grant" || opts.DisplayName != "授予数据库权限" || opts.StepName != "授予数据库权限" || opts.Port != 3306 {
		t.Fatalf("unexpected database task options: %+v", opts)
	}
}

func TestClusterAutomationCommandListsUsers(t *testing.T) {
	req := validAutomationRequest()
	req.Operation = "mysql_user"
	req.UserAction = "list"
	req.TargetUsername = ""
	req.TargetHost = ""

	if err := validateClusterAutomationRequest(req); err != nil {
		t.Fatalf("list request should be valid: %v", err)
	}
	command, err := clusterAutomationCommand(req)
	if err != nil {
		t.Fatalf("clusterAutomationCommand() error = %v", err)
	}
	if !strings.Contains(command, "SELECT user, host, account_locked FROM mysql.user") {
		t.Fatalf("generated command did not contain expected user list query: %s", command)
	}
}

func TestClusterAutomationCommandForRestartParameter(t *testing.T) {
	req := validAutomationRequest()
	req.Operation = "mysql_parameter"
	req.ParameterName = "max_connections"
	req.ParameterValue = "512"
	req.ApplyMode = "both"
	req.ConfigPath = "/etc/my.cnf"
	req.SystemdUnit = "mysqld"

	if err := validateClusterAutomationRequest(req); err != nil {
		t.Fatalf("parameter request should be valid: %v", err)
	}
	command, err := clusterAutomationCommand(req)
	if err != nil {
		t.Fatalf("clusterAutomationCommand() error = %v", err)
	}
	for _, want := range []string{"SET GLOBAL max_connections =", "512", "systemctl restart 'mysqld'"} {
		if !strings.Contains(command, want) {
			t.Fatalf("generated command missing %q: %s", want, command)
		}
	}
}

func TestClusterAutomationRejectsUnsafeParameterName(t *testing.T) {
	req := validAutomationRequest()
	req.Operation = "mysql_parameter"
	req.ParameterName = "max_connections; DROP DATABASE mysql"
	req.ParameterValue = "512"
	req.ApplyMode = "dynamic"

	if err := validateClusterAutomationRequest(req); err == nil {
		t.Fatal("unsafe parameter name was accepted")
	}
}

func TestClusterAutomationRequiresTargetCluster(t *testing.T) {
	req := validAutomationRequest()
	req.Clusters = []string{"", "   "}
	req.Operation = "collect_machine"

	if err := validateClusterAutomationRequest(req); err == nil || !strings.Contains(err.Error(), "target cluster") {
		t.Fatalf("expected target cluster validation error, got %v", err)
	}
}

func TestMySQLParameterCommandsSupportCollectionDynamicUpdateAndDelete(t *testing.T) {
	client := "mysql-client"
	collect, _, _, _, err := mysqlParameterCommand(client, mysqlParameterTaskRequest{Action: "collect"})
	if err != nil || !strings.Contains(collect, "performance_schema.global_variables") || !strings.Contains(collect, "GMHA_MYSQL_PARAMETER") {
		t.Fatalf("unexpected collect command: %q, %v", collect, err)
	}
	update, _, _, _, err := mysqlParameterCommand(client, mysqlParameterTaskRequest{Action: "update", Name: "max_connections", Value: "512", ApplyMode: "both", ConfigPath: "/data/3306/my.cnf", SystemdUnit: "mysqld-3306", Restart: true, Port: 3306})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"SET GLOBAL max_connections", "max_connections=512", "systemctl restart"} {
		if !strings.Contains(update, want) {
			t.Fatalf("update command missing %q: %s", want, update)
		}
	}
	remove, _, _, _, err := mysqlParameterCommand(client, mysqlParameterTaskRequest{Action: "delete", Name: "max_connections", ConfigPath: "/data/3306/my.cnf", Port: 3306})
	if err != nil || !strings.Contains(remove, "RESET PERSIST max_connections") {
		t.Fatalf("unexpected delete command: %q, %v", remove, err)
	}
}

func TestMySQLParameterCommandRejectsUnsafeName(t *testing.T) {
	_, _, _, _, err := mysqlParameterCommand("mysql-client", mysqlParameterTaskRequest{Action: "update", Name: "max_connections; shutdown", Value: "1", ApplyMode: "dynamic", Port: 3306})
	if err == nil {
		t.Fatal("unsafe parameter name was accepted")
	}
}

func TestClusterAutomationShellSupportsMultipleClusters(t *testing.T) {
	req := validAutomationRequest()
	req.Clusters = []string{"cluster-a", "cluster-b", "cluster-c"}
	req.Operation = "shell"
	req.Script = "hostname && uptime"

	if err := validateClusterAutomationRequest(req); err != nil {
		t.Fatalf("multi-cluster shell request should be valid: %v", err)
	}
	command, err := clusterAutomationCommand(req)
	if err != nil {
		t.Fatalf("clusterAutomationCommand() error = %v", err)
	}
	if command != req.Script {
		t.Fatalf("shell command changed unexpectedly: %q", command)
	}
}
