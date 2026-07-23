package handler

import (
	"strings"
	"testing"

	taskdomain "gmha/internal/domain/task"
)

func mysql57TopologySpec() taskdomain.MySQLTopologySpec {
	return taskdomain.MySQLTopologySpec{
		Port: 3306, RootPassword: "secret", ReplicationUser: "repl", ReplicationPassword: "repl-secret", CloneUser: "clone", ClonePassword: "clone-secret",
		ParallelType: "LOGICAL_CLOCK", ParallelWorkers: 4,
		Node: taskdomain.MySQLTopologyNodeSpec{MachineID: "m1", Version: "5.7.44", Role: "M", BaseDir: "/usr/local/mysql-3306", MyCnfPath: "/data/3306/my.cnf", SocketPath: "/data/3306/data/mysql.sock", MySQLUser: "mysql"},
	}
}

func TestMySQL57TopologyUsesLegacyConfigAndAccounts(t *testing.T) {
	spec := mysql57TopologySpec()
	config := topologyConfigureMyCNFCommand(spec)
	for _, expected := range []string{"log_slave_updates=ON", "skip_slave_start=ON", "slave_parallel_type=LOGICAL_CLOCK", "--verbose --help"} {
		if !strings.Contains(config, expected) {
			t.Fatalf("MySQL 5.7 topology config missing %q: %s", expected, config)
		}
	}
	for _, forbidden := range []string{"log_replica_updates=ON", "skip_replica_start=ON"} {
		if strings.Contains(config, forbidden) {
			t.Fatalf("MySQL 5.7 topology config contains %q: %s", forbidden, config)
		}
	}
	if !strings.Contains(config, "grep -q -- '--validate-config'") {
		t.Fatalf("MySQL config validation should detect --validate-config at runtime: %s", config)
	}
	account := topologyPrepareAccountsCommand(spec)
	if !strings.Contains(account, "REPLICATION SLAVE") || strings.Contains(account, "CLONE_ADMIN") || strings.Contains(account, "INSTALL PLUGIN clone") {
		t.Fatalf("unexpected MySQL 5.7 topology account command: %s", account)
	}
}

func TestMySQL57TopologyRejectsClone(t *testing.T) {
	spec := mysql57TopologySpec()
	spec.UseClone = true
	spec.Node.RequiresClone = true
	command := topologyCloneCommand(spec)
	if !strings.Contains(command, "unavailable on MySQL 5.7") || !strings.Contains(command, "exit 1") {
		t.Fatalf("unexpected MySQL 5.7 clone command: %s", command)
	}
}

func TestEarlyMySQL80TopologyUsesLegacyAliasesAndCloneBoundary(t *testing.T) {
	spec := mysql57TopologySpec()
	spec.Node.Version = "8.0.25"
	config := topologyConfigureMyCNFCommand(spec)
	for _, expected := range []string{"log_slave_updates=ON", "skip_slave_start=ON", "slave_parallel_workers=4"} {
		if !strings.Contains(config, expected) {
			t.Fatalf("MySQL 8.0.25 topology config missing %q: %s", expected, config)
		}
	}
	spec.Node.Version = "8.0.16"
	spec.UseClone = true
	spec.Node.RequiresClone = true
	spec.Node.SourceIP = "10.0.0.10"
	spec.Node.SourcePort = 3306
	if command := topologyCloneCommand(spec); !strings.Contains(command, "unavailable on MySQL 8.0.16") {
		t.Fatalf("MySQL 8.0.16 should reject Clone: %s", command)
	}
	spec.Node.Version = "8.0.17"
	if command := topologyCloneCommand(spec); !strings.Contains(command, "CLONE INSTANCE FROM") {
		t.Fatalf("MySQL 8.0.17 should support Clone: %s", command)
	}
}

func TestModernTopologyDoesNotProvisionCloneAccountUnlessRequested(t *testing.T) {
	spec := mysql57TopologySpec()
	spec.Node.Version = "9.7.1"
	command := topologyPrepareAccountsCommand(spec)
	if strings.Contains(command, "CLONE_ADMIN") || strings.Contains(command, "INSTALL PLUGIN clone") {
		t.Fatalf("Clone account and plugin must be opt-in: %s", command)
	}
	spec.UseClone = true
	command = topologyPrepareAccountsCommand(spec)
	for _, expected := range []string{"CLONE_ADMIN", "BACKUP_ADMIN", "INSTALL PLUGIN clone"} {
		if !strings.Contains(command, expected) {
			t.Fatalf("Clone provisioning missing %q: %s", expected, command)
		}
	}
}
