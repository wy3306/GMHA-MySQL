package app

import (
	"strings"
	"testing"

	hadomain "gmha/internal/domain/ha"
	machinedomain "gmha/internal/domain/machine"
	mysqlapp "gmha/internal/mysql"
)

func TestMGRPreflightDoesNotRequirePluginBeforeFirstConversion(t *testing.T) {
	firstConversion := mgrPreflightSQL(false)
	if strings.Contains(firstConversion, "@@group_replication_group_name") ||
		strings.Contains(firstConversion, "replication_group_members") {
		t.Fatalf("first MGR conversion must not query variables or tables that only exist after the plugin loads: %s", firstConversion)
	}
	existingGroup := mgrPreflightSQL(true)
	for _, required := range []string{"@@group_replication_group_name", "replication_group_members"} {
		if !strings.Contains(existingGroup, required) {
			t.Fatalf("existing MGR validation must query %q: %s", required, existingGroup)
		}
	}
}

func TestMGRConfigAutomaticallyLoadsGroupReplicationPlugin(t *testing.T) {
	command := mgrConfigCommand(
		hadomain.ArchitectureAdjustmentRequest{MGRGroupName: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", MGRPort: 33061},
		hadomain.ArchitectureNodeRequest{MachineID: "db-1", Port: 3306},
		machinedomain.Machine{ID: "db-1", IP: "10.0.0.1"},
		mysqlapp.Instance{MachineID: "db-1", Port: 3306, ServerID: 1, Version: "8.0.44", BaseDir: "/usr/local/mysql", MyCnfPath: "/data/3306/my.cnf", SystemdUnit: "mysqld-3306"},
		"10.0.0.1:33061",
		"10.0.0.1",
	)
	for _, required := range []string{
		"plugin_load_add=group_replication.so",
		"plugin_load_add=mysql_clone.so",
		"report_host=10.0.0.1",
		"SELECT @@global.plugin_dir",
		"MGR prerequisite missing:",
		"MGR_PLUGINS_OK",
		"systemctl restart",
		"--validate-config",
		"cp -a \"$backup\" \"$cnf\"",
	} {
		if !strings.Contains(command, required) {
			t.Fatalf("automatic MGR plugin activation command is missing %q: %s", required, command)
		}
	}
}

func TestMGRBootstrapReusesManagedAccountWithoutAlteringItself(t *testing.T) {
	command := mgrBootstrapAccountCommand("mysql --defaults-extra-file=managed", "mha", "secret", "mha", "GROUP_REPLICATION_ADMIN", 3306)
	for _, forbidden := range []string{"ALTER USER", "GRANT ALL PRIVILEGES", "CREATE USER"} {
		if strings.Contains(command, forbidden) {
			t.Fatalf("managed MHA account must not alter or re-grant itself via its own session: %s", command)
		}
	}
	for _, required := range []string{"MYSQL_PWD=", "--user='mha'", "--execute='SELECT 1'"} {
		if !strings.Contains(command, required) {
			t.Fatalf("managed account credential check is missing %q: %s", required, command)
		}
	}
}

func TestMySQLShellReleaseMustMatchServerSeries(t *testing.T) {
	if !sameMajorMinorRelease("8.0.46", "8.0.44") {
		t.Fatal("MySQL Shell 8.0 must be accepted for a MySQL 8.0 server")
	}
	if sameMajorMinorRelease("9.7.1", "8.0.44") {
		t.Fatal("MySQL Shell 9.7 must not be selected for a MySQL 8.0 server")
	}
}

func TestValidateMGRActionConfirmation(t *testing.T) {
	tests := []MGRManagementActionRequest{
		{Action: "set_primary", MachineID: "m2", Confirmation: "SET PRIMARY m2"},
		{Action: "rejoin_member", MachineID: "m3", Confirmation: "REJOIN m3"},
		{Action: "rescan_metadata", Confirmation: "RESCAN prod"},
		{Action: "rotate_recovery_passwords", Confirmation: "ROTATE RECOVERY prod"},
		{Action: "reboot_complete_outage", MachineID: "m2", Confirmation: "REBOOT MGR prod"},
	}
	for _, req := range tests {
		if err := validateMGRActionConfirmation("prod", req); err != nil {
			t.Fatalf("%s confirmation rejected: %v", req.Action, err)
		}
		req.Confirmation += " "
		if err := validateMGRActionConfirmation("prod", req); err == nil {
			t.Fatalf("%s accepted an inexact confirmation", req.Action)
		}
	}
}

func TestParseMGRMemberMarker(t *testing.T) {
	output := "task output\n" + mgrProbeMarker + "uuid-1\t101\t8.4.6\tgroup-1\tONLINE\tSECONDARY\t1\t1\t2\t3\t4\n"
	member, ok := parseMGRMemberMarker(output)
	if !ok {
		t.Fatal("member marker was not parsed")
	}
	if member.ServerUUID != "uuid-1" || member.ServerID != 101 || member.State != "ONLINE" || member.Role != "SECONDARY" {
		t.Fatalf("unexpected member: %#v", member)
	}
	if member.ApplyQueue != 2 || member.RemoteApplyQueue != 3 || member.Conflicts != 4 || !member.Reachable {
		t.Fatalf("unexpected member statistics: %#v", member)
	}
}

func TestParseMGRJSONMarker(t *testing.T) {
	var payload struct {
		OK bool `json:"ok"`
	}
	if err := parseMGRJSONMarker("noise\n"+mgrJSONMarker+`{"ok":true}`+"\n", &payload); err != nil {
		t.Fatal(err)
	}
	if !payload.OK {
		t.Fatal("JSON marker payload was not decoded")
	}
}

func TestMGRStatusScriptPrintsMarkerAndJSONOnSeparateLines(t *testing.T) {
	script := mgrStatusScript()
	if !strings.Contains(script, `print("`+mgrJSONMarker+`")`) || !strings.Contains(script, "JSON.stringify(") || !strings.Contains(script, ",null,2)") {
		t.Fatalf("MGR status output must be split into report-safe lines: %s", script)
	}
}

func TestMGROnlineSeedsPreferPrimaryAndRetainFallbacks(t *testing.T) {
	targets := []mgrClusterTarget{
		{machine: machinedomain.Machine{ID: "secondary"}, member: MGRManagementMember{State: "ONLINE", Role: "SECONDARY"}},
		{machine: machinedomain.Machine{ID: "offline"}, member: MGRManagementMember{State: "OFFLINE", Role: "SECONDARY"}},
		{machine: machinedomain.Machine{ID: "primary"}, member: MGRManagementMember{State: "ONLINE", Role: "PRIMARY"}},
	}
	seeds := mgrOnlineSeeds(targets)
	if len(seeds) != 2 || seeds[0].machine.ID != "primary" || seeds[1].machine.ID != "secondary" {
		t.Fatalf("unexpected AdminAPI seed order: %#v", seeds)
	}
}

func TestPrepareMGRActionsUseAdminAPIWithoutForce(t *testing.T) {
	targets := []mgrClusterTarget{
		{machine: machinedomain.Machine{ID: "m1", IP: "10.0.0.1"}, instance: mysqlapp.Instance{MachineID: "m1", Port: 3306}, member: MGRManagementMember{MachineID: "m1", State: "ONLINE", Role: "PRIMARY", Reachable: true}},
		{machine: machinedomain.Machine{ID: "m2", IP: "10.0.0.2"}, instance: mysqlapp.Instance{MachineID: "m2", Port: 3306}, member: MGRManagementMember{MachineID: "m2", State: "ONLINE", Role: "SECONDARY", Reachable: true}},
		{machine: machinedomain.Machine{ID: "m3", IP: "10.0.0.3"}, instance: mysqlapp.Instance{MachineID: "m3", Port: 3306}, member: MGRManagementMember{MachineID: "m3", State: "OFFLINE", Role: "SECONDARY", Reachable: true}},
	}
	status := MGRManagementStatus{Available: true, Quorum: true, MemberCount: 3, OnlineMemberCount: 2}
	cases := []struct {
		req  MGRManagementActionRequest
		want string
	}{
		{MGRManagementActionRequest{Action: "set_primary", MachineID: "m2"}, "setPrimaryInstance"},
		{MGRManagementActionRequest{Action: "rejoin_member", MachineID: "m3"}, "rejoinInstance"},
		{MGRManagementActionRequest{Action: "rescan_metadata"}, "rescan"},
	}
	for _, item := range cases {
		_, script, err := prepareMGRAction("prod", item.req, status, targets, "mha")
		if err != nil {
			t.Fatalf("%s: %v", item.req.Action, err)
		}
		if !strings.Contains(script, item.want) {
			t.Fatalf("%s script does not call %s: %s", item.req.Action, item.want, script)
		}
	}

	outageTargets := append([]mgrClusterTarget(nil), targets...)
	for index := range outageTargets {
		outageTargets[index].member.State = "OFFLINE"
		outageTargets[index].member.Role = "UNKNOWN"
	}
	outage := MGRManagementStatus{Available: true, MemberCount: 3, OnlineMemberCount: 0}
	_, script, err := prepareMGRAction("prod", MGRManagementActionRequest{Action: "reboot_complete_outage", MachineID: "m2"}, outage, outageTargets, "mha")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(script, "rebootClusterFromCompleteOutage") || strings.Contains(script, "{primary:") || strings.Contains(strings.ToLower(script), "force") {
		t.Fatalf("unsafe outage recovery script: %s", script)
	}
}

func TestMGRCompleteOutageVerificationRequiresAllApplied(t *testing.T) {
	command := mgrRecoveredGroupVerifyCommand("mha", "secret", 3306, "group-1", 3)
	for _, required := range []string{"COUNT(*)=3", "MEMBER_STATE", "ONLINE", "MEMBER_ROLE", "PRIMARY", "COUNT_TRANSACTIONS_IN_QUEUE", "COUNT_TRANSACTIONS_REMOTE_IN_APPLIER_QUEUE", "MGR_RECOVERY_CONSISTENT"} {
		if !strings.Contains(command, required) {
			t.Fatalf("complete-outage verification missing %q: %s", required, command)
		}
	}
}

func TestRecoveryAccountRotationRequiresEveryMemberOnline(t *testing.T) {
	targets := []mgrClusterTarget{{
		machine:  machinedomain.Machine{ID: "m1", IP: "10.0.0.1"},
		instance: mysqlapp.Instance{MachineID: "m1", Port: 3306},
		member:   MGRManagementMember{MachineID: "m1", State: "ONLINE", Role: "PRIMARY", Reachable: true},
	}}
	_, _, err := prepareMGRAction("prod", MGRManagementActionRequest{Action: "rotate_recovery_passwords"}, MGRManagementStatus{Available: true, Healthy: false}, targets, "mha")
	if err == nil {
		t.Fatal("rotation was allowed on an unhealthy group")
	}
	_, script, err := prepareMGRAction("prod", MGRManagementActionRequest{Action: "rotate_recovery_passwords"}, MGRManagementStatus{Available: true, Healthy: true}, targets, "mha")
	if err != nil || !strings.Contains(script, "resetRecoveryAccountsPassword") {
		t.Fatalf("healthy rotation was not prepared: script=%s err=%v", script, err)
	}
}
