package app

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	hadomain "gmha/internal/domain/ha"
	machinedomain "gmha/internal/domain/machine"
	mysqlapp "gmha/internal/mysql"
)

func TestArchitectureCommandsUseAgentManagedCredentialsWhenPasswordOmitted(t *testing.T) {
	client := mysqlArchitectureClient("", 3307)
	if !strings.Contains(client, "--defaults-extra-file=__GMHA_MYSQL_DEFAULTS_FILE__") || !strings.Contains(client, "--port=3307") || strings.Contains(client, "MYSQL_PWD") {
		t.Fatalf("unexpected passwordless architecture client: %s", client)
	}
	ptOptions := ptArchitectureOptions("", 3307)
	if !strings.Contains(ptOptions, "--defaults-file=__GMHA_MYSQL_DEFAULTS_FILE__") || strings.Contains(ptOptions, "--password") {
		t.Fatalf("unexpected passwordless PT options: %s", ptOptions)
	}
	command := mysqlArchitectureCommand("", 3307, "SELECT 1")
	if !strings.Contains(command, "--skip-column-names") {
		t.Fatalf("parsed architecture probes must omit column headers: %s", command)
	}
}

func TestArchitectureRootBootstrapUsesLocalSocket(t *testing.T) {
	client := mysqlArchitectureRootSocketClient("root-secret", "/data/3306/mysql.sock")
	for _, required := range []string{"MYSQL_PWD=", "--protocol=socket", "--socket='/data/3306/mysql.sock'", "--user=root"} {
		if !strings.Contains(client, required) {
			t.Fatalf("root bootstrap client does not contain %q: %s", required, client)
		}
	}
}

func TestArchitecturePreflightChecksManagementPrivileges(t *testing.T) {
	command := architecturePreflightCommand("", 3307)
	for _, required := range []string{"SHOW GRANTS FOR CURRENT_USER", "SYSTEM_VARIABLES_ADMIN", "SUPER", "REPLICATION (SLAVE|REPLICA)", "exit 77"} {
		if !strings.Contains(command, required) {
			t.Fatalf("preflight command does not contain %q: %s", required, command)
		}
	}
}

func TestKillBusinessSessionsToleratesThreadRacesButVerifiesFence(t *testing.T) {
	command := killBusinessSessionsCommand(hadomain.ArchitectureAdjustmentRequest{
		ReplicationUser: "mha", ManagementUsers: []string{"monitor", "backup"},
	}, "db-1", 3306)
	for _, required := range []string{
		"event_scheduler", "Daemon", "KILL CONNECTION", "|| true",
		"SELECT COUNT(*)", "business session(s) remain", "GMHA_BUSINESS_SESSIONS_CLEARED",
	} {
		if !strings.Contains(command, required) {
			t.Fatalf("business-session fencing command does not contain %q: %s", required, command)
		}
	}
}

func TestArchitecturePlanRepairsLegacyMHAPrivilegesWhenRootBootstrapProvided(t *testing.T) {
	steps := architecturePlanSteps(hadomain.ArchitectureAdjustmentRequest{
		Architecture: hadomain.ArchitectureMasterSlave,
		RootPassword: "one-time-root-secret",
	})
	if len(steps) < 3 || steps[0].Code != "acquire_lock" || steps[1].Code != "repair_management_privileges" || steps[2].Code != "preflight" {
		t.Fatalf("management repair must run before MHA preflight: %+v", steps)
	}
	for index, step := range steps {
		if step.Order != index+1 {
			t.Fatalf("step %s order=%d, want %d", step.Code, step.Order, index+1)
		}
	}
}

func TestArchitecturePlanRepairsPrivilegesWithPerNodeRootCredentials(t *testing.T) {
	steps := architecturePlanSteps(hadomain.ArchitectureAdjustmentRequest{
		Architecture:  hadomain.ArchitectureMGRRouter,
		RootPasswords: map[string]string{"db-1": "one", "db-2": "two", "db-3": "three"},
	})
	if len(steps) < 2 || steps[1].Code != "repair_management_privileges" {
		t.Fatalf("per-node root credentials must schedule management privilege repair: %+v", steps)
	}
}

func TestArchitectureManagementRepairIncludesMGRPrivileges(t *testing.T) {
	modern := architectureManagementPrivileges(true)
	for _, required := range []string{"GROUP_REPLICATION_ADMIN", "PERSIST_RO_VARIABLES_ADMIN", "CONNECTION_ADMIN", "REPLICATION_APPLIER"} {
		if !strings.Contains(modern, required) {
			t.Fatalf("management repair privilege set is missing %s", required)
		}
	}
	legacy := architectureManagementPrivileges(false)
	if !strings.Contains(legacy, "SUPER") || strings.Contains(legacy, "GROUP_REPLICATION_ADMIN") {
		t.Fatalf("legacy privilege set is not version-safe: %s", legacy)
	}
}

func TestMGRDynamicAdminPrivilegesFollowServerVersion(t *testing.T) {
	if got := strings.Join(mgrDynamicAdminPrivileges("8.0.17"), ","); strings.Contains(got, "REPLICATION_APPLIER") {
		t.Fatalf("MySQL 8.0.17 must not receive REPLICATION_APPLIER: %s", got)
	}
	for _, version := range []string{"8.0.18", "8.4.10", "9.7.1"} {
		got := strings.Join(mgrDynamicAdminPrivileges(version), ",")
		for _, required := range []string{"GROUP_REPLICATION_ADMIN", "PERSIST_RO_VARIABLES_ADMIN", "REPLICATION_APPLIER", "BACKUP_ADMIN", "CLONE_ADMIN"} {
			if !strings.Contains(got, required) {
				t.Fatalf("MySQL %s MGR grant is missing %s: %s", version, required, got)
			}
		}
	}
}

func TestValidateArchitectureRequestRejectsDelayedMaster(t *testing.T) {
	req := hadomain.ArchitectureAdjustmentRequest{
		Architecture: hadomain.ArchitectureMasterSlave,
		Nodes: []hadomain.ArchitectureNodeRequest{
			{MachineID: "primary", Role: "M", DelaySeconds: 30},
			{MachineID: "replica", Role: "S"},
		},
	}
	if err := validateArchitectureRequest(req); err == nil || !strings.Contains(err.Error(), "delayed replica") {
		t.Fatalf("expected delayed master validation error, got %v", err)
	}
}

func TestValidateArchitectureRequestAllowsThreeMasterRoots(t *testing.T) {
	req := hadomain.ArchitectureAdjustmentRequest{
		Architecture: hadomain.ArchitectureMultiMaster,
		Nodes: []hadomain.ArchitectureNodeRequest{
			{MachineID: "m1", Port: 3306, Role: "M"},
			{MachineID: "m2", Port: 3306, Role: "M"},
			{MachineID: "m3", Port: 3306, Role: "M"},
		},
	}
	if err := validateArchitectureRequest(req); err != nil {
		t.Fatalf("expected three-master architecture to be valid, got %v", err)
	}
}

func TestValidateArchitectureRequestRejectsTwoNodeMultiMaster(t *testing.T) {
	req := hadomain.ArchitectureAdjustmentRequest{
		Architecture: hadomain.ArchitectureMultiMaster,
		Nodes: []hadomain.ArchitectureNodeRequest{
			{MachineID: "m1", Port: 3306, Role: "M"},
			{MachineID: "m2", Port: 3306, Role: "M"},
		},
	}
	if err := validateArchitectureRequest(req); err == nil || !strings.Contains(err.Error(), "at least three masters") {
		t.Fatalf("expected multi-master validation error, got %v", err)
	}
}

func TestValidateMGRRouterArchitecture(t *testing.T) {
	valid := hadomain.ArchitectureAdjustmentRequest{
		Architecture:                hadomain.ArchitectureMGRRouter,
		PreferredNewMasterMachineID: "db-1",
		MGRPort:                     33061,
		RouterPort:                  6446,
		Nodes: []hadomain.ArchitectureNodeRequest{
			{MachineID: "db-1", Port: 3306, Role: "M"},
			{MachineID: "db-2", Port: 3306, Role: "S"},
			{MachineID: "db-3", Port: 3306, Role: "S"},
		},
	}
	if err := validateArchitectureRequest(valid); err != nil {
		t.Fatalf("three-member MGR should be valid: %v", err)
	}
	tests := []struct {
		name   string
		mutate func(*hadomain.ArchitectureAdjustmentRequest)
		want   string
	}{
		{"even members", func(req *hadomain.ArchitectureAdjustmentRequest) {
			req.Nodes = append(req.Nodes, hadomain.ArchitectureNodeRequest{MachineID: "db-4", Port: 3306, Role: "S"})
		}, "odd number"},
		{"two primaries", func(req *hadomain.ArchitectureAdjustmentRequest) { req.Nodes[1].Role = "M" }, "exactly one"},
		{"preferred secondary", func(req *hadomain.ArchitectureAdjustmentRequest) { req.PreferredNewMasterMachineID = "db-2" }, "must reference"},
		{"async source", func(req *hadomain.ArchitectureAdjustmentRequest) { req.Nodes[1].SourceMachineID = "db-1" }, "cannot declare"},
		{"vip", func(req *hadomain.ArchitectureAdjustmentRequest) { req.MoveVIP = true }, "cannot migrate"},
		{"router port pair", func(req *hadomain.ArchitectureAdjustmentRequest) { req.RouterPort = 65535 }, "leave room"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := valid
			req.Nodes = append([]hadomain.ArchitectureNodeRequest(nil), valid.Nodes...)
			test.mutate(&req)
			if err := validateArchitectureRequest(req); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected %q error, got %v", test.want, err)
			}
		})
	}
}

func TestMGRRouterCommandsAreDeterministicAndUsePrimaryPort(t *testing.T) {
	groupA := deterministicMGRUUID("production")
	groupB := deterministicMGRUUID("production")
	if groupA != groupB || len(groupA) != 36 {
		t.Fatalf("group UUID must be stable: %q / %q", groupA, groupB)
	}
	req := hadomain.ArchitectureAdjustmentRequest{
		Architecture: hadomain.ArchitectureMGRRouter, RouterPort: 6446,
		ReplicationUser: "mha", ReplicationPassword: "secret",
	}
	command := mysqlRouterDeployCommand(
		"production", req, hadomain.ArchitectureNodeRequest{MachineID: "db-2", Port: 4406},
		machinedomain.Machine{IP: "10.0.0.1"}, 3306,
		"https://manager/router-x86.tar.xz", "https://manager/router-arm.tar.xz", "abc", "def",
	)
	for _, required := range []string{"mha@10.0.0.1:3306", "--conf-base-port=6446", "sha256sum -c", "mysqlrouter-gmha_"} {
		if !strings.Contains(command, required) {
			t.Fatalf("Router deploy command missing %q: %s", required, command)
		}
	}
	for _, required := range []string{`rm -rf -- "$config_dir"`, `install_tmp="${target}.new.$$"`, `cp -a -- "$root_dir" "$install_tmp"`} {
		if !strings.Contains(command, required) {
			t.Fatalf("Router deploy command missing safe idempotent cleanup %q: %s", required, command)
		}
	}
	if strings.Contains(command, "mha@10.0.0.1:4406") {
		t.Fatalf("Router bootstrap must use the primary MySQL port, not the local member port: %s", command)
	}
	specialUserReq := req
	specialUserReq.ReplicationUser = "mha@ops"
	specialUserCommand := mysqlRouterDeployCommand(
		"production", specialUserReq, hadomain.ArchitectureNodeRequest{MachineID: "db-2", Port: 4406},
		machinedomain.Machine{IP: "10.0.0.1"}, 3306,
		"https://manager/router-x86.tar.xz", "", strings.Repeat("a", 64), "",
	)
	if !strings.Contains(specialUserCommand, "mha%40ops@10.0.0.1:3306") {
		t.Fatalf("Router bootstrap URI must percent-encode the account name: %s", specialUserCommand)
	}
	shellCommand := mysqlShellDeployCommand("https://manager/shell-x86.tar.gz", "", "abc", "")
	for _, required := range []string{"sha256sum -c", "/opt/gmha/mysql-shell/current/bin/mysqlsh --version"} {
		if !strings.Contains(shellCommand, required) {
			t.Fatalf("MySQL Shell deploy command missing %q: %s", required, shellCommand)
		}
	}
	configCommand := mgrConfigCommand(
		hadomain.ArchitectureAdjustmentRequest{MGRGroupName: groupA, MGRPort: 33061},
		hadomain.ArchitectureNodeRequest{MachineID: "db-1", Port: 3306},
		machinedomain.Machine{IP: "10.0.0.1"},
		mysqlapp.Instance{Port: 3306, ServerID: 101, Version: "8.4.10", BaseDir: "/opt/mysql", MyCnfPath: "/data/3306/my.cnf", SystemdUnit: "mysqld-3306"},
		"10.0.0.1:33061,10.0.0.2:33061,10.0.0.3:33061",
		"10.0.0.1,10.0.0.2,10.0.0.3",
	)
	legacyConfigCommand := mgrConfigCommand(
		hadomain.ArchitectureAdjustmentRequest{MGRGroupName: groupA, MGRPort: 33061},
		hadomain.ArchitectureNodeRequest{MachineID: "db-1", Port: 3306},
		machinedomain.Machine{IP: "10.0.0.1"},
		mysqlapp.Instance{Port: 3306, ServerID: 102, Version: "8.0.17", BaseDir: "/opt/mysql", MyCnfPath: "/data/3306/my.cnf", SystemdUnit: "mysqld-3306"},
		"10.0.0.1:33061,10.0.0.2:33061,10.0.0.3:33061",
		"10.0.0.1,10.0.0.2,10.0.0.3",
	)
	bootstrapCommand := mgrBootstrapPrimaryCommand("mysql --defaults-extra-file=/tmp/client.cnf")
	for _, required := range []string{"group_replication_bootstrap_group=ON", "START GROUP_REPLICATION", "group_replication_bootstrap_group=OFF", "if !"} {
		if !strings.Contains(bootstrapCommand, required) {
			t.Fatalf("MGR bootstrap command missing fail-closed cleanup %q: %s", required, bootstrapCommand)
		}
	}
	resetGTIDCommand := mgrResetEmptyGTIDCommand("mysql --defaults-extra-file=/tmp/client.cnf", "", 3306)
	for _, required := range []string{"RESET BINARY LOGS AND GTIDS", "RESET MASTER", "super_read_only=ON", "MGR_GTID_EMPTY"} {
		if !strings.Contains(resetGTIDCommand, required) {
			t.Fatalf("MGR empty-node GTID reset command missing %q: %s", required, resetGTIDCommand)
		}
	}
	for _, required := range []string{"server_id=101", "MGR_SERVER_ID_OK", "group_replication_ip_allowlist=", "log_replica_updates=ON", "group_replication_recovery_use_ssl=ON"} {
		if !strings.Contains(configCommand, required) {
			t.Fatalf("modern MGR config missing %q: %s", required, configCommand)
		}
	}
	for _, required := range []string{"group_replication_ip_whitelist=", "log_slave_updates=ON", "transaction_write_set_extraction=XXHASH64"} {
		if !strings.Contains(legacyConfigCommand, required) {
			t.Fatalf("MySQL 8.0.17 MGR config missing %q: %s", required, legacyConfigCommand)
		}
	}
	for name, script := range map[string]string{
		"Router deploy":  command,
		"Shell deploy":   shellCommand,
		"MGR config":     configCommand,
		"legacy config":  legacyConfigCommand,
		"MGR bootstrap":  bootstrapCommand,
		"MGR GTID reset": resetGTIDCommand,
		"Router cleanup": mysqlRouterCleanupCommand("production"),
	} {
		if output, syntaxErr := exec.Command("bash", "-n", "-c", script).CombinedOutput(); syntaxErr != nil {
			t.Fatalf("%s command has invalid shell syntax: %v\n%s\n%s", name, syntaxErr, output, script)
		}
	}
}

func TestPlannedMGRServerIDsRepairDuplicatesDeterministically(t *testing.T) {
	req := hadomain.ArchitectureAdjustmentRequest{
		Architecture: hadomain.ArchitectureMGRRouter,
		MGRGroupName: deterministicMGRUUID("production"),
		Nodes: []hadomain.ArchitectureNodeRequest{
			{MachineID: "db-1", Port: 3306, Role: "M"},
			{MachineID: "db-2", Port: 3306, Role: "S"},
			{MachineID: "db-3", Port: 3306, Role: "S"},
		},
	}
	instances := map[string]mysqlapp.Instance{
		"db-1": {MachineID: "db-1", Port: 3306, ServerID: 1},
		"db-2": {MachineID: "db-2", Port: 3306, ServerID: 1},
		"db-3": {MachineID: "db-3", Port: 3306, ServerID: 3},
	}
	first := plannedMGRServerIDs(req, instances)
	second := plannedMGRServerIDs(req, instances)
	if first["db-1"] == 1 || first["db-2"] == 1 {
		t.Fatalf("every member of a duplicate server_id set should be repaired: %#v", first)
	}
	if first["db-3"] != 3 {
		t.Fatalf("an already unique server_id should be preserved: %#v", first)
	}
	seen := map[int]bool{}
	for _, node := range req.Nodes {
		serverID := first[node.MachineID]
		if serverID <= 0 || seen[serverID] {
			t.Fatalf("planned server_id must be positive and unique: %#v", first)
		}
		if second[node.MachineID] != serverID {
			t.Fatalf("planned server_id must be deterministic: first=%#v second=%#v", first, second)
		}
		seen[serverID] = true
	}
}

func TestMGRVersionFloorSupportsVersionAwareRollingUpgrade(t *testing.T) {
	for _, version := range []string{"8.0.17", "8.0.44", "8.4.10", "9.7.1"} {
		if !mgrVersionSupported(version) {
			t.Errorf("mgrVersionSupported(%q)=false", version)
		}
	}
	for _, version := range []string{"", "5.7.44", "8.0.11", "8.0.16"} {
		if mgrVersionSupported(version) {
			t.Errorf("mgrVersionSupported(%q)=true", version)
		}
	}
}

func TestMGRToolArtifactsRequireVerifiedSHA256(t *testing.T) {
	valid := strings.Repeat("ab", 32)
	if !validSHA256Hex(valid) {
		t.Fatal("valid SHA256 was rejected")
	}
	for _, value := range []string{"", "abc", strings.Repeat("z", 64), strings.Repeat("a", 63)} {
		if validSHA256Hex(value) {
			t.Fatalf("invalid SHA256 was accepted: %q", value)
		}
	}
}

func TestMGRRouterPlanIncludesManagedShellAndRouter(t *testing.T) {
	steps := architecturePlanSteps(hadomain.ArchitectureAdjustmentRequest{Architecture: hadomain.ArchitectureMGRRouter})
	codes := make(map[string]int, len(steps))
	for index, step := range steps {
		codes[step.Code] = index
		if step.Order != index+1 {
			t.Fatalf("step %s order=%d, want %d", step.Code, step.Order, index+1)
		}
	}
	if codes["deploy_mysql_shell"] <= codes["verify_group"] || codes["adopt_innodb_cluster"] <= codes["deploy_mysql_shell"] || codes["deploy_mysql_router"] <= codes["adopt_innodb_cluster"] {
		t.Fatalf("invalid MGR dependency order: %+v", steps)
	}
	if codes["align_group_data"] <= codes["drain_business_sessions"] || codes["align_group_data"] >= codes["configure_group_replication"] {
		t.Fatalf("MGR data/GTID alignment must run after fencing and before configuration: %+v", steps)
	}
}

func TestExistingMGRPlanSwitchesPrimaryAndReconcilesRouter(t *testing.T) {
	steps := architecturePlanSteps(hadomain.ArchitectureAdjustmentRequest{
		Architecture:        hadomain.ArchitectureMGRRouter,
		CurrentArchitecture: hadomain.ArchitectureMGRRouter,
	})
	codes := make(map[string]bool, len(steps))
	for _, step := range steps {
		codes[step.Code] = true
	}
	for _, required := range []string{"bootstrap_group", "verify_group", "deploy_mysql_shell", "adopt_innodb_cluster", "deploy_mysql_router", "verify_router"} {
		if !codes[required] {
			t.Fatalf("existing MGR primary switch is missing %s: %+v", required, steps)
		}
	}
	for _, forbidden := range []string{"configure_group_replication"} {
		if codes[forbidden] {
			t.Fatalf("existing MGR primary switch must not run %s: %+v", forbidden, steps)
		}
	}
}

func TestMGRReentryPlanCleansManagedRouterBeforePreflight(t *testing.T) {
	steps := architecturePlanSteps(hadomain.ArchitectureAdjustmentRequest{
		Architecture:        hadomain.ArchitectureMGRRouter,
		CurrentArchitecture: hadomain.ArchitectureMasterSlave,
	})
	positions := map[string]int{}
	for index, step := range steps {
		positions[step.Code] = index
	}
	if positions["cleanup_stale_router"] == 0 || positions["preflight"] == 0 || positions["stop_stale_group"] == 0 {
		t.Fatalf("MGR reentry plan is missing stale Router cleanup or preflight: %+v", steps)
	}
	if positions["cleanup_stale_router"] >= positions["preflight"] {
		t.Fatalf("managed Router ports must be released before MGR port preflight: %+v", steps)
	}
	if positions["stop_stale_group"] <= positions["preflight"] || positions["stop_stale_group"] >= positions["align_group_data"] {
		t.Fatalf("stale Group Replication must stop after preflight and before data alignment: %+v", steps)
	}
}

func TestMGRToMasterSlavePlanUsesExplicitTeardown(t *testing.T) {
	steps := architecturePlanSteps(hadomain.ArchitectureAdjustmentRequest{
		Architecture:        hadomain.ArchitectureMasterSlave,
		CurrentArchitecture: hadomain.ArchitectureMGRRouter,
	})
	codes := map[string]bool{}
	for _, step := range steps {
		codes[step.Code] = true
	}
	for _, required := range []string{"freeze_business_access", "verify_group", "teardown_mgr", "promote_new_master", "reconfigure_topology", "verify_topology", "pt_verify_replication"} {
		if !codes[required] {
			t.Fatalf("MGR-to-master-slave plan is missing %s: %+v", required, steps)
		}
	}
	for _, unwanted := range []string{"elect_candidate", "force_gate", "pt_repair_on_failure"} {
		if codes[unwanted] {
			t.Fatalf("MGR-to-master-slave conversion must not use generic failover step %s: %+v", unwanted, steps)
		}
	}
}

func TestMGRAsyncTeardownDisablesRestartAndRecoveryChannel(t *testing.T) {
	command := mgrAsyncTeardownCommand(
		hadomain.ArchitectureAdjustmentRequest{},
		hadomain.ArchitectureNodeRequest{MachineID: "db-1", Port: 3306},
		mysqlapp.Instance{MyCnfPath: "/etc/mysql/db-1.cnf"},
	)
	for _, required := range []string{"STOP GROUP_REPLICATION", "group_replication_start_on_boot=OFF", "group_replication_recovery", "replication_group_members", "/etc/mysql/db-1.cnf"} {
		if !strings.Contains(command, required) {
			t.Fatalf("MGR teardown command is missing %q: %s", required, command)
		}
	}
	check := exec.Command("bash", "-n")
	check.Stdin = strings.NewReader(command)
	if output, err := check.CombinedOutput(); err != nil {
		t.Fatalf("MGR teardown command has invalid shell syntax: %v\n%s\n%s", err, output, command)
	}
}

func TestMGRRouterCleanupIsScopedToCluster(t *testing.T) {
	command := mysqlRouterCleanupCommand("production")
	safeName := safeMGRClusterName("production")
	for _, required := range []string{
		"mysqlrouter-" + safeName,
		"/etc/mysqlrouter/" + safeName,
		`rm -f -- "/etc/systemd/system/${unit}.service"`,
		`rm -rf -- "$config_dir"`,
	} {
		if !strings.Contains(command, required) {
			t.Fatalf("Router cleanup command missing %q: %s", required, command)
		}
	}
	for _, forbidden := range []string{"rm -rf /etc/mysqlrouter", "rm -rf /opt/gmha/mysql-router", "rm -rf /opt/gmha/mysql-shell"} {
		if strings.Contains(command, forbidden) {
			t.Fatalf("Router cleanup command is too broad (%q): %s", forbidden, command)
		}
	}
}

func TestValidateArchitectureRequestAllowsIndependentInstances(t *testing.T) {
	req := hadomain.ArchitectureAdjustmentRequest{
		Architecture: hadomain.ArchitectureStandalone,
		Nodes: []hadomain.ArchitectureNodeRequest{
			{MachineID: "db-1", Port: 3306, Role: "I"},
			{MachineID: "db-2", Port: 3306, Role: "I"},
		},
	}
	if err := validateArchitectureRequest(req); err != nil {
		t.Fatalf("independent topology should be valid: %v", err)
	}
}

func TestPlanStandaloneArchitectureIsExecutableWithoutMasterElection(t *testing.T) {
	machines := []machinedomain.Machine{{ID: "db-1", Name: "DB-01", IP: "10.0.0.1", Cluster: "demo"}, {ID: "db-2", Name: "DB-02", IP: "10.0.0.2", Cluster: "demo"}}
	instances := []mysqlapp.Instance{{MachineID: "db-1", Port: 3306, ServerID: 1, Status: mysqlapp.StatusRunning}, {MachineID: "db-2", Port: 3306, ServerID: 2, Status: mysqlapp.StatusRunning}}
	service := NewHAService(fakeHARepo{}, vipScopeMachineRepo{items: machines}, fakeArchitectureInstanceRepo{items: instances})
	plan, err := service.PlanArchitectureAdjustment(context.Background(), "demo", hadomain.ArchitectureAdjustmentRequest{
		Architecture: hadomain.ArchitectureStandalone,
		Nodes:        []hadomain.ArchitectureNodeRequest{{MachineID: "db-1", Port: 3306, Role: "I"}, {MachineID: "db-2", Port: 3306, Role: "I"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Executable || plan.SelectedCandidate.MachineID != "" || len(plan.Steps) == 0 {
		t.Fatalf("unexpected standalone plan: %+v", plan)
	}
}

func TestValidateArchitectureRequestRejectsMixedIndependentReplicationRoles(t *testing.T) {
	req := hadomain.ArchitectureAdjustmentRequest{
		Architecture: hadomain.ArchitectureMasterSlave,
		Nodes: []hadomain.ArchitectureNodeRequest{
			{MachineID: "db-1", Port: 3306, Role: "M"},
			{MachineID: "db-2", Port: 3306, Role: "I"},
		},
	}
	if err := validateArchitectureRequest(req); err == nil || !strings.Contains(err.Error(), "independent") {
		t.Fatalf("mixed replicated/independent roles must be rejected, got %v", err)
	}
}

func TestValidateArchitectureRequestAllowsVIPMoveWhenStartingFromIndependentWriters(t *testing.T) {
	req := hadomain.ArchitectureAdjustmentRequest{
		Architecture: hadomain.ArchitectureMasterSlave,
		MoveVIP:      true,
		Nodes: []hadomain.ArchitectureNodeRequest{
			{MachineID: "new-primary", Role: "M"},
			{MachineID: "replica", Role: "S"},
		},
	}
	if err := validateArchitectureRequest(req); err != nil {
		t.Fatalf("independent writers are fenced together before VIP movement: %v", err)
	}
}

func TestValidateArchitectureRequestAllowsInitialVIPBindingWithoutCurrentMaster(t *testing.T) {
	req := hadomain.ArchitectureAdjustmentRequest{
		Architecture:                hadomain.ArchitectureMasterSlave,
		PreferredNewMasterMachineID: "new-primary",
		MoveVIP:                     true,
		InitializeVIP:               true,
		Nodes: []hadomain.ArchitectureNodeRequest{
			{MachineID: "new-primary", Role: "M"},
			{MachineID: "replica", Role: "S"},
		},
	}
	if err := validateArchitectureRequest(req); err != nil {
		t.Fatalf("expected initial VIP binding to be valid without an old master, got %v", err)
	}
}

func TestValidateArchitectureRequestRejectsExternallyAmbiguousVIPInitialization(t *testing.T) {
	req := hadomain.ArchitectureAdjustmentRequest{
		Architecture:                hadomain.ArchitectureMasterSlave,
		CurrentMasterMachineID:      "primary",
		PreferredNewMasterMachineID: "replica",
		MoveVIP:                     true,
		InitializeVIP:               true,
		Nodes: []hadomain.ArchitectureNodeRequest{
			{MachineID: "primary", Role: "S"},
			{MachineID: "replica", Role: "M"},
		},
	}
	if err := validateArchitectureRequest(req); err == nil || !strings.Contains(err.Error(), "cannot declare a current master") {
		t.Fatalf("expected ambiguous initialization to be rejected, got %v", err)
	}
}

func TestValidateArchitectureRequestRejectsVIPMoveToSameMaster(t *testing.T) {
	req := hadomain.ArchitectureAdjustmentRequest{
		Architecture:                hadomain.ArchitectureMasterSlave,
		MoveVIP:                     true,
		CurrentMasterMachineID:      "primary",
		PreferredNewMasterMachineID: "primary",
		Nodes: []hadomain.ArchitectureNodeRequest{
			{MachineID: "primary", Role: "M"},
			{MachineID: "replica", Role: "S"},
		},
	}
	if err := validateArchitectureRequest(req); err == nil || !strings.Contains(err.Error(), "different target master") {
		t.Fatalf("expected same-master VIP validation error, got %v", err)
	}
}

func TestValidateArchitectureRequestAllowsVIPOnlyBindingOnDualMaster(t *testing.T) {
	req := hadomain.ArchitectureAdjustmentRequest{
		Architecture:                hadomain.ArchitectureDualMaster,
		PreferredNewMasterMachineID: "db-1",
		MoveVIP:                     true,
		InitializeVIP:               true,
		VIPOnly:                     true,
		Nodes: []hadomain.ArchitectureNodeRequest{
			{MachineID: "db-1", Role: "M"},
			{MachineID: "db-2", Role: "M"},
		},
	}
	if err := validateArchitectureRequest(req); err != nil {
		t.Fatalf("expected VIP-only initial binding on dual master to be valid, got %v", err)
	}
}

func TestVIPOnlyPlanDoesNotChangeMySQLTopology(t *testing.T) {
	steps := architecturePlanSteps(hadomain.ArchitectureAdjustmentRequest{VIPOnly: true, MoveVIP: true})
	want := []string{"acquire_lock", "preflight", "freeze_business_access", "drain_business_sessions", "check_vip_conflict", "withdraw_vip", "verify_zero_vip", "bind_vip", "verify_single_vip", "resume_business_connections", "release_lock"}
	if len(steps) != len(want) {
		t.Fatalf("unexpected VIP-only step count: %+v", steps)
	}
	for index, code := range want {
		if steps[index].Code != code || steps[index].Order != index+1 {
			t.Fatalf("unexpected VIP-only workflow at %d: %+v", index, steps)
		}
	}
	for _, step := range steps {
		switch step.Code {
		case "freeze_old_master", "promote_new_master", "repoint_replicas", "pt_verify_replication":
			t.Fatalf("VIP-only workflow must not change MySQL topology: %+v", steps)
		}
	}
}

func TestVIPOnlyInitialBindingSkipsBusinessPause(t *testing.T) {
	steps := architecturePlanSteps(hadomain.ArchitectureAdjustmentRequest{VIPOnly: true, MoveVIP: true, InitializeVIP: true})
	for _, step := range steps {
		switch step.Code {
		case "freeze_business_access", "drain_business_sessions", "resume_business_connections":
			t.Fatalf("initial binding has no existing VIP traffic to pause: %+v", steps)
		}
	}
}

func TestDualToMasterSlavePlanSkipsElectionAndPromotion(t *testing.T) {
	steps := architecturePlanSteps(hadomain.ArchitectureAdjustmentRequest{CurrentArchitecture: hadomain.ArchitectureDualMaster, Architecture: hadomain.ArchitectureMasterSlave})
	codes := map[string]bool{}
	for _, step := range steps {
		codes[step.Code] = true
	}
	for _, unwanted := range []string{"elect_candidate", "promote_new_master", "force_gate", "pt_repair_on_failure"} {
		if codes[unwanted] {
			t.Fatalf("dual-to-master-slave must skip %s: %+v", unwanted, steps)
		}
	}
	for _, required := range []string{"freeze_business_access", "wait_replication_zero", "reconfigure_topology", "verify_topology", "pt_verify_replication"} {
		if !codes[required] {
			t.Fatalf("dual-to-master-slave is missing %s: %+v", required, steps)
		}
	}
}

func TestMasterSlaveToDualPlanSkipsExistingMasterPromotion(t *testing.T) {
	steps := architecturePlanSteps(hadomain.ArchitectureAdjustmentRequest{CurrentArchitecture: hadomain.ArchitectureMasterSlave, Architecture: hadomain.ArchitectureDualMaster})
	for _, step := range steps {
		if step.Code == "elect_candidate" || step.Code == "promote_new_master" || step.Code == "force_gate" {
			t.Fatalf("master-slave-to-dual must not repeat election/promotion: %+v", steps)
		}
	}
}

func TestValidateArchitectureRequestRejectsVIPOnlyReplicaTarget(t *testing.T) {
	req := hadomain.ArchitectureAdjustmentRequest{
		Architecture:                hadomain.ArchitectureMasterSlave,
		CurrentMasterMachineID:      "db-1",
		PreferredNewMasterMachineID: "db-2",
		MoveVIP:                     true,
		VIPOnly:                     true,
		Nodes: []hadomain.ArchitectureNodeRequest{
			{MachineID: "db-1", Role: "M"},
			{MachineID: "db-2", Role: "S", SourceMachineID: "db-1"},
		},
	}
	if err := validateArchitectureRequest(req); err == nil || !strings.Contains(err.Error(), "target must be a master") {
		t.Fatalf("expected VIP-only replica target to be rejected, got %v", err)
	}
}

func TestArchitectureCandidateScoresUseRequestedInstancePort(t *testing.T) {
	req := hadomain.ArchitectureAdjustmentRequest{
		Architecture:           hadomain.ArchitectureMasterSlave,
		CurrentMasterMachineID: "primary",
		Nodes: []hadomain.ArchitectureNodeRequest{
			{MachineID: "primary", Port: 3306, Role: "M"},
			{MachineID: "replica", Port: 3307, Role: "M"},
		},
	}
	machines := map[string]machinedomain.Machine{
		"primary": {ID: "primary", Name: "db-1", IP: "10.0.0.1"},
		"replica": {ID: "replica", Name: "db-2", IP: "10.0.0.2"},
	}
	instances := map[string][]mysqlapp.Instance{
		"primary": {{MachineID: "primary", Port: 3306, ServerID: 1, Status: mysqlapp.StatusRunning}},
		"replica": {
			{MachineID: "replica", Port: 3306, ServerID: 1, Status: mysqlapp.StatusRunning},
			{MachineID: "replica", Port: 3307, ServerID: 2, Status: mysqlapp.StatusRunning},
		},
	}

	scores := architectureCandidateScores("demo", req, machines, instances)
	if len(scores) != 2 {
		t.Fatalf("score count = %d, want 2", len(scores))
	}
	if !scores[1].Eligible || scores[1].Port != 3307 || scores[1].InstanceID != "replica:3307" {
		t.Fatalf("requested instance was not selected correctly: %+v", scores[1])
	}
}

func TestArchitecturePlanPromotesBeforeVIPMove(t *testing.T) {
	steps := architecturePlanSteps(hadomain.ArchitectureAdjustmentRequest{MoveVIP: true, CurrentMasterMachineID: "old-primary", PreferredNewMasterMachineID: "new-primary"})
	orders := make(map[string]int, len(steps))
	for _, step := range steps {
		orders[step.Code] = step.Order
	}
	if orders["promote_new_master"] == 0 || orders["move_vip"] == 0 || orders["promote_new_master"] >= orders["move_vip"] {
		t.Fatalf("unsafe plan ordering: promote=%d move_vip=%d", orders["promote_new_master"], orders["move_vip"])
	}
	if orders["wait_replication_zero"] >= orders["force_gate"] {
		t.Fatalf("force confirmation must follow replication wait: %+v", orders)
	}
	if orders["pt_verify_replication"] <= orders["verify_topology"] || orders["pt_verify_replication"] >= orders["move_vip"] {
		t.Fatalf("mandatory PT verification must follow topology verification and precede VIP movement: %+v", orders)
	}
	if orders["resume_business_connections"] <= orders["verify_single_vip"] || orders["resume_business_connections"] >= orders["release_lock"] {
		t.Fatalf("business connections must resume only after VIP verification and before lock release: %+v", orders)
	}
}

func TestIndependentToReplicaPlanAlignsGTIDBeforeReplication(t *testing.T) {
	steps := architecturePlanSteps(hadomain.ArchitectureAdjustmentRequest{Architecture: hadomain.ArchitectureMasterSlave})
	orders := map[string]int{}
	for _, step := range steps {
		orders[step.Code] = step.Order
	}
	if orders["elect_candidate"] == 0 || orders["align_replica_gtid"] == 0 || orders["promote_new_master"] == 0 || orders["repoint_replicas"] == 0 {
		t.Fatalf("independent-to-replica plan is incomplete: %+v", orders)
	}
	if !(orders["elect_candidate"] < orders["align_replica_gtid"] && orders["align_replica_gtid"] < orders["promote_new_master"] && orders["promote_new_master"] < orders["repoint_replicas"]) {
		t.Fatalf("unsafe GTID alignment ordering: %+v", orders)
	}
}

func TestStandalonePlanVerifiesWithPTBeforeDetach(t *testing.T) {
	steps := architecturePlanSteps(hadomain.ArchitectureAdjustmentRequest{Architecture: hadomain.ArchitectureStandalone})
	orders := make(map[string]int, len(steps))
	for _, step := range steps {
		orders[step.Code] = step.Order
	}
	if orders["pt_verify_before_split"] == 0 || orders["detach_replication"] == 0 || orders["pt_verify_before_split"] >= orders["detach_replication"] {
		t.Fatalf("standalone split must pass PT verification before replication is detached: %+v", orders)
	}
	if orders["resume_business_connections"] <= orders["verify_topology"] || orders["resume_business_connections"] >= orders["release_lock"] {
		t.Fatalf("standalone nodes must stay offline until topology verification completes: %+v", orders)
	}
}

func TestIndependentToReplicationPlanFreezesBeforeElection(t *testing.T) {
	steps := architecturePlanSteps(hadomain.ArchitectureAdjustmentRequest{Architecture: hadomain.ArchitectureDualMaster})
	orders := make(map[string]int, len(steps))
	for _, step := range steps {
		orders[step.Code] = step.Order
	}
	if orders["freeze_old_master"] == 0 || orders["elect_candidate"] == 0 || orders["freeze_old_master"] >= orders["elect_candidate"] {
		t.Fatalf("independent writers must be frozen before GTID election: %+v", orders)
	}
	if orders["pt_verify_replication"] <= orders["repoint_replicas"] {
		t.Fatalf("new replication must be followed by PT verification: %+v", orders)
	}
	if orders["resume_business_connections"] <= orders["pt_verify_replication"] || orders["resume_business_connections"] >= orders["release_lock"] {
		t.Fatalf("independent writers must stay offline until PT verification completes: %+v", orders)
	}
}

func TestArchitectureCandidateRejectsReplicaRole(t *testing.T) {
	req := hadomain.ArchitectureAdjustmentRequest{Architecture: hadomain.ArchitectureMasterSlave, Nodes: []hadomain.ArchitectureNodeRequest{{MachineID: "replica", Port: 3306, Role: "S"}}}
	scores := architectureCandidateScores("demo", req,
		map[string]machinedomain.Machine{"replica": {ID: "replica", Name: "db-2", IP: "10.0.0.2"}},
		map[string][]mysqlapp.Instance{"replica": {{MachineID: "replica", Port: 3306, ServerID: 2, Status: mysqlapp.StatusRunning}}},
	)
	if len(scores) != 1 || scores[0].Eligible || !strings.Contains(strings.Join(scores[0].RejectReasons, " "), "target role") {
		t.Fatalf("replica role was eligible for promotion: %+v", scores)
	}
}

func TestArchitectureCandidateAllowsInPlaceCurrentMasterEdit(t *testing.T) {
	req := hadomain.ArchitectureAdjustmentRequest{Architecture: hadomain.ArchitectureMasterSlave, CurrentMasterMachineID: "primary", PreferredNewMasterMachineID: "primary", Nodes: []hadomain.ArchitectureNodeRequest{{MachineID: "primary", Port: 3306, Role: "M"}, {MachineID: "replica", Port: 3306, Role: "S"}}}
	scores := architectureCandidateScores("demo", req,
		map[string]machinedomain.Machine{"primary": {ID: "primary", Name: "db-1", IP: "10.0.0.1"}, "replica": {ID: "replica", Name: "db-2", IP: "10.0.0.2"}},
		map[string][]mysqlapp.Instance{"primary": {{MachineID: "primary", Port: 3306, ServerID: 1, Status: mysqlapp.StatusRunning}}, "replica": {{MachineID: "replica", Port: 3306, ServerID: 2, Status: mysqlapp.StatusRunning}}},
	)
	if len(scores) != 2 || !scores[0].Eligible {
		t.Fatalf("current primary should remain eligible for an in-place topology edit: %+v", scores)
	}
}

func TestArchitectureIndependentNodesAreValidatedWithoutElection(t *testing.T) {
	req := hadomain.ArchitectureAdjustmentRequest{Architecture: hadomain.ArchitectureStandalone, CurrentMasterMachineID: "db-1", Nodes: []hadomain.ArchitectureNodeRequest{{MachineID: "db-1", Port: 3306, Role: "I"}, {MachineID: "db-2", Port: 3306, Role: "I"}}}
	scores := architectureCandidateScores("demo", req,
		map[string]machinedomain.Machine{"db-1": {ID: "db-1", IP: "10.0.0.1"}, "db-2": {ID: "db-2", IP: "10.0.0.2"}},
		map[string][]mysqlapp.Instance{"db-1": {{MachineID: "db-1", Port: 3306, ServerID: 1, Status: mysqlapp.StatusRunning}}, "db-2": {{MachineID: "db-2", Port: 3306, ServerID: 2, Status: mysqlapp.StatusRunning}}},
	)
	if len(scores) != 2 || !scores[0].Eligible || !scores[1].Eligible {
		t.Fatalf("independent nodes should pass health validation without master election: %+v", scores)
	}
}

func TestL2VIPCommandsRemoveBeforeBindAndAnnounce(t *testing.T) {
	vip := hadomain.ClusterVIPConfig{VIPAddress: "10.0.0.100", VIPPrefix: 24, DefaultInterface: "eth0", ArpingCount: 4}
	remove := l2VIPRemoveCommand(vip)
	bind := l2VIPBindCommand(vip)
	for _, want := range []string{"ip addr del", "grep -Fxq"} {
		if !strings.Contains(remove, want) {
			t.Fatalf("remove command missing %q: %s", want, remove)
		}
	}
	for _, want := range []string{"ip addr add", "arping -U -c 4", "grep -Fxq"} {
		if !strings.Contains(bind, want) {
			t.Fatalf("bind command missing %q: %s", want, bind)
		}
	}
}

func TestBGPCommandsWithdrawBeforeAnnounce(t *testing.T) {
	vip := hadomain.ClusterVIPConfig{VIPAddress: "10.0.0.100", BGPLocalAS: 65000, BGPPeerAS: 65001, BGPPeerAddress: "10.0.0.254", BGPCommunity: "65000:100"}
	withdraw := bgpVIPWithdrawCommand(vip)
	announce := bgpVIPAnnounceCommand(vip, machinedomain.Machine{IP: "10.0.0.2"})
	for _, want := range []string{"no network 10.0.0.100/32", "ip addr del"} {
		if !strings.Contains(withdraw, want) {
			t.Fatalf("withdraw command missing %q: %s", want, withdraw)
		}
	}
	for _, want := range []string{"neighbor 10.0.0.254 remote-as 65001", "network 10.0.0.100/32 route-map GMHA-VIP", "show bgp ipv4 unicast"} {
		if !strings.Contains(announce, want) {
			t.Fatalf("announce command missing %q: %s", want, announce)
		}
	}
}

func TestGTIDTransactionCount(t *testing.T) {
	set := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa:1-10:15,bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb:1-3"
	if got := gtidTransactionCount(set); got != 14 {
		t.Fatalf("gtidTransactionCount() = %d, want 14", got)
	}
}

func TestGTIDSetSubsetRejectsDivergentHistory(t *testing.T) {
	uuid := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	if !gtidSetSubset(uuid+":1-5", uuid+":1-10") {
		t.Fatal("expected earlier GTID set to be a subset")
	}
	if gtidSetSubset(uuid+":1-5:12", uuid+":1-10") {
		t.Fatal("divergent GTID interval must not be treated as a subset")
	}
	if gtidSetSubset("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb:1", uuid+":1-10") {
		t.Fatal("different source UUID must not be treated as a subset")
	}
	if !gtidSetSubset(uuid+":1-10", uuid+":1-5:6-10") {
		t.Fatal("adjacent intervals should be normalized before subset comparison")
	}
}

func TestDelayedReplicaHealthChecksConfiguredDelay(t *testing.T) {
	command := delayedReplicationHealthCommand("secret", 3306, 3600)
	for _, want := range []string{"SQL_Delay", "3600", "Replica_IO_Running", "Replica_SQL_Running"} {
		if !strings.Contains(command, want) {
			t.Fatalf("delayed replica command missing %q", want)
		}
	}
}

func TestReplicationCatchupChecksExecutedGTID(t *testing.T) {
	command := replicationCatchupCommand("secret", 3306)
	for _, want := range []string{"GTID_SUBSET", "RECEIVED_TRANSACTION_SET", "gtid_executed", "Seconds_Behind"} {
		if !strings.Contains(command, want) {
			t.Fatalf("replication catchup command missing %q", want)
		}
	}
	statusPrefix := strings.Split(command, " -e 'SHOW REPLICA STATUS")[0]
	if strings.Contains(statusPrefix, "--skip-column-names") {
		t.Fatalf("SHOW REPLICA STATUS must retain field labels for awk parsing: %s", command)
	}
}

func TestArchitectureShellCommandsParse(t *testing.T) {
	vip := hadomain.ClusterVIPConfig{ClusterID: "demo", VIPAddress: "10.0.0.100", VIPPrefix: 24, DefaultInterface: "eth0", BGPLocalAS: 65000, BGPPeerAS: 65001, BGPPeerAddress: "10.0.0.254"}
	commands := map[string]string{
		"pt install":          installCompatiblePTCommand("secret", 3306),
		"replication wait":    replicationCatchupCommand("secret", 3306),
		"delayed replication": delayedReplicationHealthCommand("secret", 3306, 600),
		"standalone detach":   standaloneDetachCommand("secret", 3306),
		"standalone verify":   verifyIndependentNodeCommand("secret", 3306),
		"PT checksum":         ptChecksumCommand("secret", 3306),
		"PT checksum verify":  ptChecksumVerificationCommand("secret", 3306),
		"L2 withdraw":         l2VIPRemoveCommand(vip),
		"L2 bind":             l2VIPBindCommand(vip),
		"BGP withdraw":        bgpVIPWithdrawCommand(vip),
		"BGP announce":        bgpVIPAnnounceCommand(vip, machinedomain.Machine{IP: "10.0.0.2"}),
	}
	for name, command := range commands {
		if output, err := exec.Command("sh", "-n", "-c", command).CombinedOutput(); err != nil {
			t.Errorf("%s command has invalid shell syntax: %v\n%s", name, err, output)
		}
	}
}

func TestStandaloneDetachKeepsBusinessConnectionsOfflineUntilVerification(t *testing.T) {
	command := standaloneDetachCommand("secret", 3306)
	if !strings.Contains(command, "offline_mode=ON") || strings.Contains(command, "offline_mode=OFF") {
		t.Fatalf("standalone detach must keep business connections isolated: %s", command)
	}
}

func TestArchitectureRoleChangesPersistAcrossMySQLRestart(t *testing.T) {
	client := mysqlArchitectureClient("secret", 3306)
	writable := mysqlRolePersistenceCommand(client, false)
	for _, required := range []string{
		"SET PERSIST super_read_only=OFF",
		"SET PERSIST read_only=OFF",
		"SET GLOBAL super_read_only=OFF",
		"SET GLOBAL read_only=OFF",
	} {
		if !strings.Contains(writable, required) {
			t.Fatalf("writable role command missing %q: %s", required, writable)
		}
	}
	readOnly := mysqlRolePersistenceCommand(client, true)
	for _, required := range []string{
		"SET PERSIST read_only=ON",
		"SET PERSIST super_read_only=ON",
		"SET GLOBAL read_only=ON",
		"SET GLOBAL super_read_only=ON",
	} {
		if !strings.Contains(readOnly, required) {
			t.Fatalf("read-only role command missing %q: %s", required, readOnly)
		}
	}
	for name, command := range map[string]string{"writable": writable, "read-only": readOnly} {
		if output, err := exec.Command("sh", "-n", "-c", command).CombinedOutput(); err != nil {
			t.Fatalf("%s role command has invalid shell syntax: %v\n%s", name, err, output)
		}
	}
}

func TestDualMasterVerificationRequiresBothWritableFlagsOff(t *testing.T) {
	req := hadomain.ArchitectureAdjustmentRequest{
		Architecture: hadomain.ArchitectureDualMaster,
		Nodes: []hadomain.ArchitectureNodeRequest{
			{MachineID: "db-1", Port: 3306, Role: "M"},
			{MachineID: "db-2", Port: 3306, Role: "M"},
		},
	}
	for _, node := range req.Nodes {
		command := verifyArchitectureNodeCommand(req, node, "db-1")
		for _, required := range []string{"@@read_only=0", "@@super_read_only=0", "ROLE_OK"} {
			if !strings.Contains(command, required) {
				t.Fatalf("dual-master verification for %s missing %q: %s", node.MachineID, required, command)
			}
		}
	}
}

func TestPTCheckRequiresOfflineInstallationAndVersionGate(t *testing.T) {
	command := installCompatiblePTCommand("secret", 3306)
	for _, want := range []string{"offline dependencies are missing", "enable offline PT installation", "min_pt=3.7.1", "8.*|9.*", "pt-table-checksum", "perl -MDBI -MDBD::mysql"} {
		if !strings.Contains(command, want) {
			t.Fatalf("PT install command missing %q", want)
		}
	}
	for _, forbidden := range []string{"repo.percona.com", "apt-get", "dnf install", "yum install"} {
		if strings.Contains(command, forbidden) {
			t.Fatalf("PT compatibility check must not access package repositories: %s", command)
		}
	}
}

func TestPTChecksumSupportsRowBinlogClusters(t *testing.T) {
	command := ptChecksumCommand("secret", 3306)
	for _, required := range []string{"--no-check-binlog-format", "--ignore-databases=mysql,sys,performance_schema,information_schema,gmha,percona", "GMHA_PT_NO_BUSINESS_TABLES"} {
		if !strings.Contains(command, required) {
			t.Fatalf("PT checksum command missing %q: %s", required, command)
		}
	}
}

type fakeArchitectureInstanceRepo struct{ items []mysqlapp.Instance }

func (r fakeArchitectureInstanceRepo) List(context.Context) ([]mysqlapp.Instance, error) {
	return r.items, nil
}
func (r fakeArchitectureInstanceRepo) Get(context.Context, string, int) (mysqlapp.Instance, bool, error) {
	return mysqlapp.Instance{}, false, nil
}
func (r fakeArchitectureInstanceRepo) Delete(context.Context, string, int) error { return nil }
func (r fakeArchitectureInstanceRepo) UpdateStatus(context.Context, string, int, string) error {
	return nil
}
func (r fakeArchitectureInstanceRepo) PruneUninstalled(context.Context) (int64, error) { return 0, nil }
