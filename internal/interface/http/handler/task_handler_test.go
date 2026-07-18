package handler

import (
	"os"
	"os/exec"
	"path/filepath"
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
	if !strings.Contains(command, "GMHA_MYSQL_USER") || !strings.Contains(command, "FROM mysql.user u") || !strings.Contains(command, "information_schema.user_privileges") {
		t.Fatalf("generated command did not contain expected user list query: %s", command)
	}
}

func TestMySQLUserTaskUsesAgentManagedMHACredential(t *testing.T) {
	command, err := mysqlUserTaskCommand("/opt/mysql", mysqlUserTaskRequest{Port: 3306, Action: "list"})
	if err != nil {
		t.Fatalf("mysqlUserTaskCommand() error = %v", err)
	}
	for _, want := range []string{"/opt/mysql/bin/mysql", "__GMHA_MYSQL_DEFAULTS_FILE__", "GMHA_MYSQL_USER", "management_account"} {
		if !strings.Contains(command, want) {
			t.Fatalf("MHA user task command missing %q: %s", want, command)
		}
	}
	for _, forbidden := range []string{"MYSQL_PWD", "root-secret", "mysql_password"} {
		if strings.Contains(command, forbidden) {
			t.Fatalf("MHA user task command leaked or requested %q: %s", forbidden, command)
		}
	}
}

func TestMySQLUserLockAndUnlockSQL(t *testing.T) {
	for action, want := range map[string]string{"lock": "ACCOUNT LOCK", "unlock": "ACCOUNT UNLOCK"} {
		sql, err := mysqlUserSQL(action, "app_user", "10.0.%", "", nil)
		if err != nil {
			t.Fatalf("mysqlUserSQL(%s) error = %v", action, err)
		}
		if !strings.Contains(sql, want) || !strings.Contains(sql, "'app_user'@'10.0.%'") {
			t.Fatalf("unexpected %s SQL: %s", action, sql)
		}
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
	if err != nil || !strings.Contains(remove, "RESET PERSIST IF EXISTS max_connections") {
		t.Fatalf("unexpected delete command: %q, %v", remove, err)
	}
}

func TestMySQLParameterCollectionPreservesSQLQuotesThroughShell(t *testing.T) {
	command, _, _, _, err := mysqlParameterCommand("printf '%s\\n'", mysqlParameterTaskRequest{Action: "collect"})
	if err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command("sh", "-c", command).CombinedOutput()
	if err != nil {
		t.Fatalf("execute generated collection command: %v: %s", err, output)
	}
	got := strings.TrimSpace(string(output))
	for _, want := range []string{
		"--execute=SELECT CONCAT('GMHA_MYSQL_PARAMETER\\t'",
		"FROM performance_schema.global_variables",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("shell changed collection SQL; missing %q in %q", want, got)
		}
	}
	if strings.Contains(got, `\\"`) {
		t.Fatalf("collection SQL contains leaked shell escapes: %q", got)
	}
}

func TestMySQLParameterConfigMutationIsScopedAndAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "my config.cnf")
	original := "[client]\nmax_connections=11\n\n[mysqld]\nmax_connections=100\nmax_connections = 101\nport=3306\n\n[mysqldump]\nmax_connections=7\n"
	if err := os.WriteFile(path, []byte(original), 0o640); err != nil {
		t.Fatal(err)
	}
	value := "512&64|quoted ' value"
	command := mysqlParameterConfigCommand(path, "max_connections", value, false)
	if output, err := exec.Command("sh", "-c", command).CombinedOutput(); err != nil {
		t.Fatalf("update generated config command: %v: %s\n%s", err, output, command)
	}
	updatedBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	updated := string(updatedBytes)
	if strings.Count(updated, "max_connections=512&64|quoted ' value") != 1 {
		t.Fatalf("mysqld value was not replaced exactly once:\n%s", updated)
	}
	for _, unchanged := range []string{"[client]\nmax_connections=11", "[mysqldump]\nmax_connections=7"} {
		if !strings.Contains(updated, unchanged) {
			t.Fatalf("non-mysqld section changed; missing %q:\n%s", unchanged, updated)
		}
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o640 {
		t.Fatalf("config mode was not preserved: info=%v err=%v", info, err)
	}
	if backups, _ := filepath.Glob(path + ".gmha.*.bak"); len(backups) != 1 {
		t.Fatalf("expected one backup, got %v", backups)
	}

	command = mysqlParameterConfigCommand(path, "max_connections", "", true)
	if output, err := exec.Command("sh", "-c", command).CombinedOutput(); err != nil {
		t.Fatalf("delete generated config command: %v: %s", err, output)
	}
	removedBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	removed := string(removedBytes)
	if strings.Contains(removed, "max_connections=512") {
		t.Fatalf("mysqld value was not deleted:\n%s", removed)
	}
	for _, unchanged := range []string{"[client]\nmax_connections=11", "[mysqldump]\nmax_connections=7"} {
		if !strings.Contains(removed, unchanged) {
			t.Fatalf("delete changed non-mysqld section; missing %q:\n%s", unchanged, removed)
		}
	}
}

func TestMySQLParameterInvalidConfigIsRolledBack(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "my.cnf")
	original := "[mysqld]\nmax_connections=100\n"
	if err := os.WriteFile(path, []byte(original), 0o640); err != nil {
		t.Fatal(err)
	}
	command, _, _, _, err := mysqlParameterCommand("printf '%s\\n'", mysqlParameterTaskRequest{
		Action: "update", Name: "max_connections", Value: "invalid", ApplyMode: "config",
		ConfigPath: path, Port: 3306, MySQLDPath: "/usr/bin/false",
	})
	if err != nil {
		t.Fatal(err)
	}
	output, runErr := exec.Command("sh", "-c", command).CombinedOutput()
	if runErr == nil || !strings.Contains(string(output), "GMHA_CONFIG_VALIDATION_FAILED") {
		t.Fatalf("expected validation failure and rollback, err=%v output=%s", runErr, output)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != original {
		t.Fatalf("invalid config was not rolled back:\n%s", after)
	}
}

func TestMySQLParameterCommandRejectsUnsafeName(t *testing.T) {
	_, _, _, _, err := mysqlParameterCommand("mysql-client", mysqlParameterTaskRequest{Action: "update", Name: "max_connections; shutdown", Value: "1", ApplyMode: "dynamic", Port: 3306})
	if err == nil {
		t.Fatal("unsafe parameter name was accepted")
	}
}

func TestMySQLParameterBatchClassifiesDynamicAndRestartChanges(t *testing.T) {
	command, err := mysqlParameterBatchCommand("mysql-client", mysqlParameterTargetRequest{Port: 3306, ConfigPath: "/data/3306/my.cnf", SystemdUnit: "mysqld-3306"}, []mysqlParameterChangeRequest{
		{Action: "update", Name: "max_connections", Value: "512"},
		{Action: "update", Name: "skip_name_resolve", Value: "1"},
	}, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"SET GLOBAL max_connections", "max_connections=512", "skip_name_resolve=1", "systemctl restart 'mysqld-3306'"} {
		if !strings.Contains(command, want) {
			t.Fatalf("batch command missing %q: %s", want, command)
		}
	}
	if got := strings.Count(command, "systemctl restart"); got != 1 {
		t.Fatalf("batch must restart once, got %d restarts: %s", got, command)
	}
	if !mysqlParameterIsDynamic("MAX_CONNECTIONS") || mysqlParameterIsDynamic("skip_name_resolve") {
		t.Fatal("unexpected parameter apply classification")
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
