package handler

import (
	"os/exec"
	"strings"
	"testing"

	"gmha/internal/app"
	machinedomain "gmha/internal/domain/machine"
	mysqlapp "gmha/internal/mysql"
)

func TestValidateMySQLLifecycleRequestRequiresExplicitRiskGate(t *testing.T) {
	req := mysqlLifecycleRequest{Machine: "10.0.0.1", Port: 3306, Action: "restart", Confirmation: "RESTART 10.0.0.1:3306"}
	if err := validateMySQLLifecycleRequest(req); err == nil || !strings.Contains(err.Error(), "acknowledgement") {
		t.Fatalf("missing risk acknowledgement should fail, got %v", err)
	}
	req.RiskAcknowledged = true
	if err := validateMySQLLifecycleRequest(req); err != nil {
		t.Fatalf("valid restart request rejected: %v", err)
	}
	req.Action = "poweroff"
	if err := validateMySQLLifecycleRequest(req); err == nil {
		t.Fatal("host poweroff must not be accepted as an instance lifecycle action")
	}
}

func TestMySQLRestartCommandsPreserveTopologyAndVerifyData(t *testing.T) {
	target := mysqlapp.Instance{Port: 3306, BaseDir: "/opt/mysql", SystemdUnit: "mysqld-3306.service"}
	members := []app.MySQLInstanceTarget{
		{Machine: machinedomain.Machine{IP: "10.0.0.1"}, Instance: mysqlapp.Instance{Port: 3306}},
		{Machine: machinedomain.Machine{IP: "10.0.0.2"}, Instance: mysqlapp.Instance{Port: 3306}},
	}
	commands, err := mysqlLifecycleCommands(target, "10.0.0.1", members, "restart", "/tmp/gmha-test", "/var/lock/gmha-test", true, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(commands) != 6 {
		t.Fatalf("deep restart should have six guarded steps, got %d", len(commands))
	}
	all := ""
	for _, command := range commands {
		all += "\n" + command.Command
		if output, syntaxErr := exec.Command("bash", "-n", "-c", command.Command).CombinedOutput(); syntaxErr != nil {
			t.Fatalf("restart workflow shell syntax is invalid: %v\n%s\n%s", syntaxErr, output, command.Command)
		}
	}
	for _, required := range []string{
		mysqlDefaultsFilePlaceholder,
		"topology.before",
		"topology.after",
		"diff -u",
		"SHOW REPLICA STATUS",
		"SHOW SLAVE STATUS",
		"GTID_SUBSET",
		"replication_group_members",
		"GMHA_MGR_HEALTH_OK",
		"pt-table-checksum",
		"systemctl restart",
		"Another MySQL lifecycle operation is active",
		"GMHA_MYSQL_SAFE_RESTART_OK",
	} {
		if !strings.Contains(all, required) {
			t.Fatalf("restart workflow missing %q", required)
		}
	}
	for _, forbidden := range []string{"--password=", "MYSQL_PWD", "poweroff", "shutdown -h"} {
		if strings.Contains(all, forbidden) {
			t.Fatalf("restart workflow contains unsafe credential or host action %q", forbidden)
		}
	}
}

func TestMySQLLifecycleCommandsVerifyMGRQuorum(t *testing.T) {
	target := mysqlapp.Instance{Port: 3306, SystemdUnit: "mysqld-3306.service"}
	members := []app.MySQLInstanceTarget{
		{Machine: machinedomain.Machine{IP: "10.0.0.1"}, Instance: target},
		{Machine: machinedomain.Machine{IP: "10.0.0.2"}, Instance: mysqlapp.Instance{Port: 3306}},
		{Machine: machinedomain.Machine{IP: "10.0.0.3"}, Instance: mysqlapp.Instance{Port: 3306}},
	}
	commands, err := mysqlLifecycleCommands(target, "10.0.0.1", members, "shutdown", "/tmp/gmha-test", "/var/lock/gmha-test", true, true)
	if err != nil {
		t.Fatal(err)
	}
	all := ""
	for _, command := range commands {
		all += "\n" + command.Command
		if output, syntaxErr := exec.Command("bash", "-n", "-c", command.Command).CombinedOutput(); syntaxErr != nil {
			t.Fatalf("MGR shutdown workflow shell syntax is invalid: %v\n%s\n%s", syntaxErr, output, command.Command)
		}
	}
	for _, required := range []string{
		"mgr_member_state",
		"mgr_group_health",
		"members=3",
		"members=2",
		"10.0.0.2",
		"async PT checksum step skipped",
	} {
		if !strings.Contains(all, required) {
			t.Fatalf("MGR lifecycle workflow missing %q", required)
		}
	}
}

func TestMySQLShutdownStopsOnlyManagedService(t *testing.T) {
	target := mysqlapp.Instance{Port: 3307, SystemdUnit: "mysqld-3307.service"}
	members := []app.MySQLInstanceTarget{{Machine: machinedomain.Machine{IP: "10.0.0.3"}, Instance: target}}
	commands, err := mysqlLifecycleCommands(target, "10.0.0.3", members, "shutdown", "/tmp/gmha-test", "/var/lock/gmha-test", false, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(commands) != 2 {
		t.Fatalf("single-instance shutdown should have precheck and stop steps, got %d", len(commands))
	}
	stop := commands[1].Command
	if !strings.Contains(stop, "systemctl stop 'mysqld-3307'") || !strings.Contains(stop, "systemctl is-active") {
		t.Fatalf("shutdown step does not safely stop and verify the managed unit: %s", stop)
	}
	for _, forbidden := range []string{"poweroff", "shutdown -h", "reboot"} {
		if strings.Contains(stop, forbidden) {
			t.Fatalf("instance shutdown must not affect host: found %q", forbidden)
		}
	}
}
