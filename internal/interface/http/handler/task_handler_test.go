package handler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	taskdomain "gmha/internal/domain/task"
	mysqlapp "gmha/internal/mysql"
)

func validAutomationRequest() clusterAutomationRequest {
	return clusterAutomationRequest{
		Clusters:       []string{"cluster-a"},
		Port:           3306,
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

func TestClusterAutomationDatabaseCommandUsesRegisteredInstanceAndManagedCredential(t *testing.T) {
	req := validAutomationRequest()
	req.Operation = "collect_mysql"
	instance := mysqlapp.Instance{BaseDir: "/opt/mysql-8.0.44", Port: 3306, Version: "8.0.44"}

	if err := validateClusterAutomationRequest(req); err != nil {
		t.Fatalf("managed-credential request should be valid without browser credentials: %v", err)
	}
	command, err := clusterAutomationCommand(req, instance)
	if err != nil {
		t.Fatalf("clusterAutomationCommand() error = %v", err)
	}
	for _, want := range []string{"/opt/mysql-8.0.44/bin/mysql", mysqlDefaultsFilePlaceholder, "GMHA_MYSQL_INSTANCE", "GMHA_MYSQL_STATUS"} {
		if !strings.Contains(command, want) {
			t.Fatalf("instance-aware command missing %q: %s", want, command)
		}
	}
	for _, forbidden := range []string{"MYSQL_PWD", "--password"} {
		if strings.Contains(command, forbidden) {
			t.Fatalf("managed-credential command leaked %q: %s", forbidden, command)
		}
	}
}

func TestClusterAutomationValidationDoesNotRequireBrowserDatabaseCredential(t *testing.T) {
	req := validAutomationRequest()
	req.Operation = "collect_mysql"
	if err := validateClusterAutomationRequest(req); err != nil {
		t.Fatalf("Agent-managed database credentials should not be required in the request: %v", err)
	}
}

func TestParseMySQLAutomationCollectionBuildsStructuredRow(t *testing.T) {
	row := clusterAutomationCollectionRow{}
	parseMySQLAutomationCollection(&row, []taskdomain.Event{{Content: strings.Join([]string{
		"GMHA_MYSQL_INSTANCE\tdb-01\t8.0.44\t3306",
		"GMHA_MYSQL_STATUS\tThreads_connected\t17",
		"GMHA_MYSQL_STATUS\tThreads_running\t3",
		"GMHA_MYSQL_RATE\tQPS\t27",
		"GMHA_MYSQL_RATE\tTPS\t5",
		"GMHA_MYSQL_STATUS\tQuestions\t1002",
		"GMHA_MYSQL_STATUS\tSlow_queries\t4",
		"GMHA_MYSQL_STATUS\tUptime\t3600",
	}, "\n")}})
	if row.Hostname != "db-01" || row.MySQLVersion != "8.0.44" || row.MySQLPort != 3306 {
		t.Fatalf("unexpected instance row: %+v", row)
	}
	if row.ThreadsConnected != "17" || row.ThreadsRunning != "3" || row.QPS != "27" || row.TPS != "5" || row.Questions != "1002" || row.SlowQueries != "4" || row.Uptime != "3600" {
		t.Fatalf("unexpected status values: %+v", row)
	}
}

func TestAutomationCollectionRejectsMismatchedTaskTypes(t *testing.T) {
	machineTask := taskdomain.Task{Type: taskdomain.TypeCollectMachineInfo}
	mysqlTask := taskdomain.Task{Type: taskdomain.TypeExec, SpecJSON: []byte(`{"operation":"mysql_collect"}`)}
	if !automationCollectionTaskMatches("collect_machine", machineTask) || automationCollectionTaskMatches("collect_mysql", machineTask) {
		t.Fatal("machine collection task matching is incorrect")
	}
	if !automationCollectionTaskMatches("collect_mysql", mysqlTask) || automationCollectionTaskMatches("collect_machine", mysqlTask) {
		t.Fatal("MySQL collection task matching is incorrect")
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

func validMySQLIndexRequest() mysqlIndexTaskRequest {
	return mysqlIndexTaskRequest{
		Port: 3306, Action: "create", Schema: "app", Table: "orders", Name: "idx_status_created",
		Kind: "btree", Columns: []mysqlIndexColumnRequest{{Name: "status"}, {Name: "created_at", Direction: "DESC"}},
		LockMode: "none", Purpose: "降低订单状态查询扫描行数", Impact: "增加索引空间并观察写入 TPS", LockAcknowledged: true,
	}
}

func TestMySQLIndexValidationRequiresPurposeImpactAndLockAcknowledgement(t *testing.T) {
	req := validMySQLIndexRequest()
	req.Purpose = ""
	if err := validateMySQLIndexRequest(req); err == nil || !strings.Contains(err.Error(), "purpose") {
		t.Fatalf("missing purpose should be rejected, got %v", err)
	}
	req = validMySQLIndexRequest()
	req.LockAcknowledged = false
	if err := validateMySQLIndexRequest(req); err == nil || !strings.Contains(err.Error(), "acknowledgement") {
		t.Fatalf("missing lock acknowledgement should be rejected, got %v", err)
	}
}

func TestMySQLIndexNativeCreateUsesExplicitNonEscalatingLock(t *testing.T) {
	req := validMySQLIndexRequest()
	commands, display, err := mysqlIndexTaskCommands("/opt/mysql", req)
	if err != nil {
		t.Fatal(err)
	}
	if len(commands) != 3 || display != "创建索引 app.orders.idx_status_created" {
		t.Fatalf("unexpected native workflow: %q %#v", display, commands)
	}
	joined := commands[0].Command + commands[1].Command + commands[2].Command
	for _, want := range []string{"GMHA_MYSQL_INDEX_IMPACT", "ADD INDEX `idx_status_created` (`status`, `created_at` DESC)", "ALGORITHM=INPLACE, LOCK=NONE", "GMHA_MYSQL_INDEX_VERIFIED"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("native index workflow missing %q: %s", want, joined)
		}
	}
	if strings.Contains(joined, "--password") || strings.Contains(joined, "MYSQL_PWD") {
		t.Fatalf("index workflow must only use the Agent credential file: %s", joined)
	}
}

func TestMySQLIndexPTOnlineCreateDryRunsAndThrottlesBeforeExecute(t *testing.T) {
	req := validMySQLIndexRequest()
	req.OnlineWithPT = true
	commands, display, err := mysqlIndexTaskCommands("/opt/mysql", req)
	if err != nil {
		t.Fatal(err)
	}
	if len(commands) != 4 || !strings.HasPrefix(display, "PT 在线创建索引") {
		t.Fatalf("unexpected PT workflow: %q %#v", display, commands)
	}
	if !strings.Contains(commands[0].Command, "command -v pt-online-schema-change") || !strings.Contains(commands[1].Command, "--dry-run") || !strings.Contains(commands[2].Command, "--execute") {
		t.Fatalf("PT workflow must verify, dry-run and execute in order: %#v", commands)
	}
	for _, want := range []string{"--max-load=Threads_running=25", "--critical-load=Threads_running=50", "--max-lag=10", "--progress=percentage,1", "--alter-foreign-keys-method=auto", mysqlDefaultsFilePlaceholder} {
		if !strings.Contains(commands[2].Command, want) {
			t.Fatalf("PT execute command missing %q: %s", want, commands[2].Command)
		}
	}
}

func TestMySQLIndexPTWorkflowCommandsExecuteWithManagedCredentialPlaceholder(t *testing.T) {
	root := t.TempDir()
	mysqlBin := filepath.Join(root, "mysql", "bin")
	if err := os.MkdirAll(mysqlBin, 0o755); err != nil {
		t.Fatal(err)
	}
	mysqlScript := "#!/bin/sh\nprintf 'GMHA_FAKE_MYSQL\\n'\n"
	if err := os.WriteFile(filepath.Join(mysqlBin, "mysql"), []byte(mysqlScript), 0o755); err != nil {
		t.Fatal(err)
	}
	ptScript := "#!/bin/sh\ncase \" $* \" in *\" --version \"*) echo 'pt-online-schema-change 3.7.1';; *\" --execute \"*) echo 'Copying app.orders: 50%'; echo 'Copying app.orders: 100%';; *) echo 'dry run ok';; esac\n"
	if err := os.WriteFile(filepath.Join(root, "pt-online-schema-change"), []byte(ptScript), 0o755); err != nil {
		t.Fatal(err)
	}
	defaultsFile := filepath.Join(root, "client.cnf")
	if err := os.WriteFile(defaultsFile, []byte("[client]\nuser=mha\npassword=redacted\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	req := validMySQLIndexRequest()
	req.OnlineWithPT = true
	commands, _, err := mysqlIndexTaskCommands(filepath.Join(root, "mysql"), req)
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range commands {
		command := strings.ReplaceAll(step.Command, mysqlDefaultsFilePlaceholder, shellQuote(defaultsFile))
		cmd := exec.Command("sh", "-lc", command)
		cmd.Env = append(os.Environ(), "PATH="+root+":"+os.Getenv("PATH"))
		output, runErr := cmd.CombinedOutput()
		if runErr != nil {
			t.Fatalf("%s failed: %v\n%s\ncommand=%s", step.Name, runErr, output, command)
		}
	}
}

func validMySQLOnlineDDLRequest() mysqlOnlineDDLTaskRequest {
	return mysqlOnlineDDLTaskRequest{
		Port:                   3306,
		Action:                 "dry_run",
		Schema:                 "app",
		Table:                  "orders",
		Alter:                  "ADD COLUMN fulfillment_status varchar(32) NOT NULL DEFAULT 'pending'",
		Purpose:                "记录订单履约状态",
		Impact:                 "复制原表并短暂获取元数据锁完成切换",
		MaxLoadThreadsRunning:  25,
		CriticalThreadsRunning: 50,
		MaxLagSeconds:          10,
		ChunkTimeSeconds:       0.5,
		CheckIntervalSeconds:   1,
		AlterForeignKeysMethod: "auto",
	}
}

func TestMySQLOnlineDDLValidationSeparatesDryRunAndExecution(t *testing.T) {
	req := validMySQLOnlineDDLRequest()
	if err := validateMySQLOnlineDDLRequest(req); err != nil {
		t.Fatalf("valid dry run should pass: %v", err)
	}
	req.Action = "execute"
	if err := validateMySQLOnlineDDLRequest(req); err == nil || !strings.Contains(err.Error(), "acknowledgement") {
		t.Fatalf("execution without acknowledgement should fail, got %v", err)
	}
	req.RiskAcknowledged = true
	req.Confirmation = "app.orders"
	if err := validateMySQLOnlineDDLRequest(req); err != nil {
		t.Fatalf("acknowledged execution should pass: %v", err)
	}
}

func TestMySQLOnlineDDLRejectsStatementsAndLockEscalation(t *testing.T) {
	for _, alter := range []string{
		"ALTER TABLE orders ADD COLUMN unsafe int",
		"ADD COLUMN safe int; DROP TABLE orders",
		"ADD COLUMN safe int, LOCK=EXCLUSIVE",
		"RENAME TO archived_orders",
		"ADD COLUMN safe int /* skip checks */",
	} {
		req := validMySQLOnlineDDLRequest()
		req.Alter = alter
		if err := validateMySQLOnlineDDLRequest(req); err == nil {
			t.Fatalf("unsafe ALTER clause should be rejected: %q", alter)
		}
	}
}

func TestMySQLOnlineDDLCommandsAlwaysDryRunBeforeExecute(t *testing.T) {
	req := validMySQLOnlineDDLRequest()
	req.Action = "execute"
	req.RiskAcknowledged = true
	req.Confirmation = "app.orders"
	commands, display, err := mysqlOnlineDDLTaskCommands("/opt/mysql", req)
	if err != nil {
		t.Fatal(err)
	}
	if len(commands) != 4 || display != "PT 在线 DDL app.orders" {
		t.Fatalf("unexpected online DDL workflow: %q %#v", display, commands)
	}
	if !strings.Contains(commands[0].Command, "command -v pt-online-schema-change") ||
		!strings.Contains(commands[1].Command, "--dry-run") ||
		!strings.Contains(commands[2].Command, "--execute") ||
		!strings.Contains(commands[3].Command, "GMHA_ONLINE_DDL_VERIFIED") {
		t.Fatalf("online DDL workflow must precheck, dry-run, execute and verify: %#v", commands)
	}
	for _, want := range []string{
		"--max-load=Threads_running=25",
		"--critical-load=Threads_running=50",
		"--max-lag=10",
		"--chunk-time=0.5",
		"--check-interval=1",
		"--alter-foreign-keys-method=auto",
		mysqlDefaultsFilePlaceholder,
	} {
		if !strings.Contains(commands[2].Command, want) {
			t.Fatalf("PT execute command missing %q: %s", want, commands[2].Command)
		}
	}
	for _, forbidden := range []string{"--password", "MYSQL_PWD"} {
		if strings.Contains(commands[2].Command, forbidden) {
			t.Fatalf("online DDL command leaked forbidden credential mechanism %q", forbidden)
		}
	}
}

func TestMySQLOnlineDDLDefaultsAreConservative(t *testing.T) {
	req := validMySQLOnlineDDLRequest()
	req.MaxLoadThreadsRunning = 0
	req.CriticalThreadsRunning = 0
	req.MaxLagSeconds = 0
	req.ChunkTimeSeconds = 0
	req.CheckIntervalSeconds = 0
	req.AlterForeignKeysMethod = ""
	normalized := normalizeMySQLOnlineDDLRequest(req)
	if normalized.MaxLoadThreadsRunning != 25 || normalized.CriticalThreadsRunning != 50 ||
		normalized.MaxLagSeconds != 10 || normalized.ChunkTimeSeconds != 0.5 ||
		normalized.CheckIntervalSeconds != 1 || normalized.AlterForeignKeysMethod != "auto" {
		t.Fatalf("unexpected online DDL defaults: %#v", normalized)
	}
}

func validMySQLArchiveRequest() mysqlArchiveTaskRequest {
	return mysqlArchiveTaskRequest{
		Port: 3306, Action: "dry_run",
		SourceSchema: "app", SourceTable: "orders",
		DestinationSchema: "archive", DestinationTable: "orders_2026",
		Where: "created_at < NOW() - INTERVAL 180 DAY",
		Index: "PRIMARY", BatchSize: 1000, SleepSeconds: 1, RunTimeSeconds: 3600,
		DeleteSource: true,
	}
}

func TestMySQLArchiveValidationSeparatesPreviewAndExecution(t *testing.T) {
	req := validMySQLArchiveRequest()
	if err := validateMySQLArchiveRequest(req); err != nil {
		t.Fatalf("valid archive preview should pass: %v", err)
	}
	req.Action = "execute"
	if err := validateMySQLArchiveRequest(req); err == nil || !strings.Contains(err.Error(), "acknowledgement") {
		t.Fatalf("execution without acknowledgement should fail, got %v", err)
	}
	req.RiskAcknowledged = true
	req.Confirmation = mysqlArchiveConfirmation(req)
	if err := validateMySQLArchiveRequest(req); err != nil {
		t.Fatalf("acknowledged execution should pass: %v", err)
	}
}

func TestMySQLArchiveRejectsUnsafeOrUnboundedTargets(t *testing.T) {
	for _, where := range []string{
		"1=1",
		"created_at < NOW(); DROP TABLE app.orders",
		"created_at < NOW() -- all rows",
		"SLEEP(10)=0",
		"id IN (SELECT id FROM old_orders);",
	} {
		req := validMySQLArchiveRequest()
		req.Where = where
		if err := validateMySQLArchiveRequest(req); err == nil {
			t.Fatalf("unsafe archive predicate should be rejected: %q", where)
		}
	}
	req := validMySQLArchiveRequest()
	req.DestinationSchema, req.DestinationTable = req.SourceSchema, req.SourceTable
	if err := validateMySQLArchiveRequest(req); err == nil {
		t.Fatal("same source and destination table should be rejected")
	}
}

func TestMySQLArchiveCommandsPrepareDryRunExecuteAndVerify(t *testing.T) {
	req := validMySQLArchiveRequest()
	req.Action = "execute"
	req.RiskAcknowledged = true
	req.Confirmation = mysqlArchiveConfirmation(req)
	commands, display, err := mysqlArchiveTaskCommands("/opt/mysql", req)
	if err != nil {
		t.Fatal(err)
	}
	if len(commands) != 5 || display != "PT 数据归档 app.orders → archive.orders_2026" {
		t.Fatalf("unexpected archive workflow: %q %#v", display, commands)
	}
	for index, want := range []string{"pt-archiver --version", "CREATE TABLE IF NOT EXISTS", "--dry-run", "pt-archiver --source", "GMHA_ARCHIVE_REMAINING"} {
		if !strings.Contains(commands[index].Command, want) {
			t.Fatalf("archive step %d missing %q: %s", index+1, want, commands[index].Command)
		}
	}
	for _, want := range []string{"LIMIT 100000", "FORCE INDEX (`PRIMARY`)", "EXPLAIN SELECT"} {
		if !strings.Contains(commands[0].Command, want) {
			t.Fatalf("archive precheck must bound and explain its source scan, missing %q: %s", want, commands[0].Command)
		}
	}
	execute := commands[3].Command
	for _, want := range []string{
		"--limit=1000", "--commit-each", "--sleep=1", "--progress=1000", "--retries=3",
		"--run-time=3600s", "i=PRIMARY", mysqlDefaultsFilePlaceholder,
	} {
		if !strings.Contains(execute, want) {
			t.Fatalf("archive command missing %q: %s", want, execute)
		}
	}
	for _, forbidden := range []string{"--password", "MYSQL_PWD", "--no-delete"} {
		if strings.Contains(execute, forbidden) {
			t.Fatalf("move-mode archive command contains forbidden option %q: %s", forbidden, execute)
		}
	}
	req.DeleteSource = false
	req.Confirmation = mysqlArchiveConfirmation(req)
	copyCommands, _, err := mysqlArchiveTaskCommands("/opt/mysql", req)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(copyCommands[3].Command, "--no-delete") {
		t.Fatalf("copy-only archive must preserve source rows: %s", copyCommands[3].Command)
	}
}

func TestMySQLArchiveGeneratedWorkflowRunsWithStubTools(t *testing.T) {
	root := t.TempDir()
	mysqlBin := filepath.Join(root, "mysql", "bin")
	if err := os.MkdirAll(mysqlBin, 0o755); err != nil {
		t.Fatal(err)
	}
	mysqlScript := "#!/bin/sh\ncase \" $* \" in *\"SELECT COUNT(*) FROM information_schema.tables\"*) echo 1;; *) echo 'GMHA_ARCHIVE_STUB ok';; esac\n"
	if err := os.WriteFile(filepath.Join(mysqlBin, "mysql"), []byte(mysqlScript), 0o755); err != nil {
		t.Fatal(err)
	}
	ptScript := "#!/bin/sh\ncase \" $* \" in *\" --version \"*) echo 'pt-archiver 3.7.1';; *\" --dry-run \"*) echo 'SELECT dry run';; *) echo 'TIME ELAPSED COUNT'; echo '2026-07-23T09:10:11 1 1000'; echo 'INSERT 1000'; echo 'DELETE 1000';; esac\n"
	if err := os.WriteFile(filepath.Join(root, "pt-archiver"), []byte(ptScript), 0o755); err != nil {
		t.Fatal(err)
	}
	defaultsFile := filepath.Join(root, "client.cnf")
	if err := os.WriteFile(defaultsFile, []byte("[client]\nuser=mha\npassword=redacted\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	req := validMySQLArchiveRequest()
	req.Action, req.RiskAcknowledged = "execute", true
	req.Confirmation = mysqlArchiveConfirmation(req)
	commands, _, err := mysqlArchiveTaskCommands(filepath.Join(root, "mysql"), req)
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range commands {
		command := strings.ReplaceAll(step.Command, mysqlDefaultsFilePlaceholder, shellQuote(defaultsFile))
		cmd := exec.Command("sh", "-lc", command)
		cmd.Env = append(os.Environ(), "PATH="+root+":"+os.Getenv("PATH"))
		output, runErr := cmd.CombinedOutput()
		if runErr != nil {
			t.Fatalf("%s failed: %v\n%s\ncommand=%s", step.Name, runErr, output, command)
		}
	}
}

func TestMySQLIndexDeleteProtectsPrimaryAndRequiresExactConfirmation(t *testing.T) {
	req := mysqlIndexTaskRequest{Action: "delete", Schema: "app", Table: "orders", Name: "PRIMARY", Confirmation: "app.orders.PRIMARY"}
	if err := validateMySQLIndexRequest(req); err == nil || !strings.Contains(err.Error(), "primary") {
		t.Fatalf("primary deletion must be rejected, got %v", err)
	}
	req.Name = "idx_status"
	if err := validateMySQLIndexRequest(req); err == nil || !strings.Contains(err.Error(), "exact") {
		t.Fatalf("inexact confirmation must be rejected, got %v", err)
	}
	req.Confirmation = "app.orders.idx_status"
	if err := validateMySQLIndexRequest(req); err != nil {
		t.Fatalf("exact confirmation should be accepted: %v", err)
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

func TestMySQL57UserPrivilegesRejectDynamicGrants(t *testing.T) {
	if err := validateMySQLUserPrivilegesForVersion([]string{"SELECT", "SUPER"}, "5.7.44"); err != nil {
		t.Fatalf("MySQL 5.7 static privileges should be accepted: %v", err)
	}
	if err := validateMySQLUserPrivilegesForVersion([]string{"BACKUP_ADMIN"}, "5.7.44"); err == nil || !strings.Contains(err.Error(), "does not support dynamic privilege") {
		t.Fatalf("MySQL 5.7 dynamic privilege should be rejected clearly, got %v", err)
	}
	if err := validateMySQLUserPrivilegesForVersion([]string{"BACKUP_ADMIN"}, "8.0.44"); err != nil {
		t.Fatalf("MySQL 8 dynamic privilege should be accepted: %v", err)
	}
	if err := validateMySQLUserPrivilegesForVersion([]string{"BACKUP_ADMIN"}, "8.0.11"); err == nil {
		t.Fatal("early MySQL 8.0 must use the conservative static-privilege path")
	}
	if err := validateMySQLUserPrivilegesForVersion([]string{"CLONE_ADMIN"}, "8.0.17"); err != nil {
		t.Fatalf("MySQL 8.0.17 Clone privilege should be accepted: %v", err)
	}
}

func TestClusterAutomationCommandForRestartParameter(t *testing.T) {
	req := validAutomationRequest()
	req.Operation = "mysql_parameter"
	req.ParameterName = "max_connections"
	req.ParameterValue = "512"
	req.ApplyMode = "restart"
	req.ConfigPath = "/etc/my.cnf"
	req.SystemdUnit = "mysqld"

	if err := validateClusterAutomationRequest(req); err != nil {
		t.Fatalf("parameter request should be valid: %v", err)
	}
	command, err := clusterAutomationCommand(req)
	if err != nil {
		t.Fatalf("clusterAutomationCommand() error = %v", err)
	}
	for _, want := range []string{"GMHA_PARAMETER_NAME='max_connections'", "512", "systemctl restart 'mysqld'"} {
		if !strings.Contains(command, want) {
			t.Fatalf("generated command missing %q: %s", want, command)
		}
	}
	if strings.Contains(command, "SET GLOBAL max_connections") {
		t.Fatalf("restart-only apply mode must not change the running value before restart: %s", command)
	}
}

func TestClusterAutomationCommandForDynamicAndConfigParameter(t *testing.T) {
	req := validAutomationRequest()
	req.Operation = "mysql_parameter"
	req.ParameterName = "max_connections"
	req.ParameterValue = "512"
	req.ApplyMode = "both"
	req.ConfigPath = "/etc/my.cnf"

	command, err := clusterAutomationCommand(req)
	if err != nil {
		t.Fatalf("clusterAutomationCommand() error = %v", err)
	}
	for _, want := range []string{"GMHA_PARAMETER_NAME='max_connections'", "SET GLOBAL max_connections ="} {
		if !strings.Contains(command, want) {
			t.Fatalf("generated command missing %q: %s", want, command)
		}
	}
	if strings.Contains(command, "systemctl restart") {
		t.Fatalf("dynamic-and-config apply mode must not restart MySQL: %s", command)
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

func TestMySQL57ParameterCommandsMapAliasesAndAvoidResetPersist(t *testing.T) {
	client := "mysql-client"
	update, _, _, _, err := mysqlParameterCommand(client, mysqlParameterTaskRequest{Action: "update", Name: "binlog_expire_logs_seconds", Value: "604800", ApplyMode: "both", ConfigPath: "/data/3306/my.cnf", Port: 3306, Version: "5.7.44"})
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"expire_logs_days=7", "SET GLOBAL expire_logs_days"} {
		if !strings.Contains(update, expected) {
			t.Fatalf("MySQL 5.7 update missing %q: %s", expected, update)
		}
	}
	remove, _, _, _, err := mysqlParameterCommand(client, mysqlParameterTaskRequest{Action: "delete", Name: "transaction_isolation", ConfigPath: "/data/3306/my.cnf", ApplyMode: "dynamic", Port: 3306, Version: "5.7.44"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(remove, "SET GLOBAL transaction_isolation = DEFAULT") || strings.Contains(remove, "RESET PERSIST") {
		t.Fatalf("unexpected MySQL 5.7 delete command: %s", remove)
	}
	legacyName, _, err := mysqlParameterForVersion("transaction_isolation", "READ-COMMITTED", "5.7.9")
	if err != nil || legacyName != "transaction_isolation" || mysqlDynamicParameterNameForVersion(legacyName, "5.7.9") != "tx_isolation" {
		t.Fatalf("MySQL 5.7.9 should keep transaction_isolation in my.cnf and use tx_isolation dynamically, got %q, %v", legacyName, err)
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

func TestClusterAutomationNormalizesClusterSelection(t *testing.T) {
	got := normalizeAutomationClusters([]string{" cluster-a ", "cluster-b", "cluster-a", "", "cluster-b"})
	if strings.Join(got, ",") != "cluster-a,cluster-b" {
		t.Fatalf("unexpected normalized clusters: %#v", got)
	}
}

func TestClusterAutomationShellHasAuditableBusinessMetadata(t *testing.T) {
	opts, ok := clusterAutomationTaskOptions(clusterAutomationRequest{Operation: "shell"})
	if !ok || opts.Operation != "cluster_shell" || opts.DisplayName != "执行集群 Shell 脚本" || opts.StepName == "" {
		t.Fatalf("unexpected shell task options: %+v, %v", opts, ok)
	}
}

func TestClusterAutomationRejectsOversizedOrBinaryShell(t *testing.T) {
	for name, script := range map[string]string{
		"oversized": strings.Repeat("x", 256*1024+1),
		"nul":       "hostname\x00id",
	} {
		t.Run(name, func(t *testing.T) {
			req := validAutomationRequest()
			req.Operation, req.Script = "shell", script
			if err := validateClusterAutomationRequest(req); err == nil {
				t.Fatal("unsafe shell payload was accepted")
			}
		})
	}
}
