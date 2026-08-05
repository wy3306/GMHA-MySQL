package app

import (
	"strings"
	"testing"
)

func TestAsyncReplicationRecoveryValidatesSourceAndGTIDBeforeSuccess(t *testing.T) {
	command := asyncReplicationRecoveryCommand("mha", "secret", 3306, "10.0.0.1", 3307, 0)
	for _, required := range []string{"Source_Host", "Master_Host", "10.0.0.1", "3307", "START REPLICA", "START SLAVE", "GTID_SUBSET", "Seconds_Behind_", "Last_(IO|SQL)_Error", "ASYNC_REPLICATION_CONSISTENT"} {
		if !strings.Contains(command, required) {
			t.Fatalf("async restart recovery missing %q: %s", required, command)
		}
	}
	if strings.Contains(command, "CHANGE REPLICATION") || strings.Contains(command, "CHANGE MASTER") {
		t.Fatalf("automatic recovery must not rebuild replication metadata: %s", command)
	}
}

func TestDelayedReplicaRecoveryPreservesConfiguredDelay(t *testing.T) {
	command := asyncReplicationRecoveryCommand("mha", "secret", 3306, "10.0.0.1", 3306, 300)
	if !strings.Contains(command, "SQL_Delay") || !strings.Contains(command, `"$configured_delay" = 300`) || !strings.Contains(command, "ASYNC_REPLICATION_DELAYED_HEALTHY") {
		t.Fatalf("delayed replica recovery does not preserve delay: %s", command)
	}
}
