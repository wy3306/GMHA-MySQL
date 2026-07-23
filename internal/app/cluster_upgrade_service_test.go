package app

import (
	"reflect"
	"testing"

	hadomain "gmha/internal/domain/ha"
)

func TestClusterUpgradeStagesPreserveSafeRollingOrder(t *testing.T) {
	stages := clusterUpgradeStages()
	got := make([]string, 0, len(stages))
	for _, stage := range stages {
		got = append(got, stage.Code)
		if stage.Status != clusterUpgradePending {
			t.Fatalf("stage %s starts in %q, want pending", stage.Code, stage.Status)
		}
	}
	want := []string{
		"cluster_preflight",
		"precheck_all_nodes",
		"upgrade_replicas",
		"verify_replicas",
		"switch_to_upgraded_replica",
		"upgrade_original_primary",
		"verify_original_primary",
		"switch_back_original_primary",
		"final_cluster_verify",
		"release_maintenance_lock",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected rolling upgrade order:\ngot  %v\nwant %v", got, want)
	}
}

func TestClusterUpgradeReplicaOrderUpgradesCandidateLast(t *testing.T) {
	run := ClusterUpgradeRun{
		OriginalPrimaryMachineID:  "primary",
		TemporaryPrimaryMachineID: "replica-b",
		Nodes: []ClusterUpgradeNode{
			{MachineID: "replica-b"},
			{MachineID: "primary"},
			{MachineID: "replica-c"},
			{MachineID: "replica-a"},
		},
	}
	got := clusterUpgradeReplicaOrder(run)
	want := []string{"replica-a", "replica-c", "replica-b"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("replica order = %v, want %v", got, want)
	}
}

func TestClusterUpgradeSwitchAwayDetachesOldPrimaryWithoutForce(t *testing.T) {
	run := ClusterUpgradeRun{
		OriginalPrimaryMachineID:  "primary",
		TemporaryPrimaryMachineID: "replica-a",
		Nodes: []ClusterUpgradeNode{
			{MachineID: "primary", Port: 3306},
			{MachineID: "replica-a", Port: 3306},
			{MachineID: "replica-b", Port: 3306, DelaySeconds: 30},
		},
	}
	request := clusterUpgradeSwitchoverRequest(run, "primary", "replica-a", true)
	if request.ForceAfterTimeout {
		t.Fatal("rolling upgrade switchover must never force after timeout")
	}
	if !request.MoveVIP {
		t.Fatal("rolling upgrade switchover must move the business VIP")
	}
	if !reflect.DeepEqual(request.MaintenanceDetachedMachineIDs, []string{"primary"}) {
		t.Fatalf("detached nodes = %v, want old primary", request.MaintenanceDetachedMachineIDs)
	}
	participating := architectureParticipationRequest(request)
	if len(participating.Nodes) != 2 {
		t.Fatalf("participating node count = %d, want 2", len(participating.Nodes))
	}
	for _, node := range participating.Nodes {
		if node.MachineID == "primary" {
			t.Fatal("old primary must remain fenced and absent from the mixed-version replication topology")
		}
		if node.MachineID == "replica-a" && (node.Role != "M" || node.SourceMachineID != "") {
			t.Fatalf("temporary primary role is invalid: %+v", node)
		}
		if node.MachineID == "replica-b" && (node.Role != "S" || node.SourceMachineID != "replica-a" || node.DelaySeconds != 30) {
			t.Fatalf("remaining replica topology is invalid: %+v", node)
		}
	}
}

func TestClusterUpgradeSwitchBackIncludesEveryUpgradedNode(t *testing.T) {
	run := ClusterUpgradeRun{
		OriginalPrimaryMachineID:  "primary",
		TemporaryPrimaryMachineID: "replica-a",
		Nodes: []ClusterUpgradeNode{
			{MachineID: "primary", Port: 3306},
			{MachineID: "replica-a", Port: 3306},
			{MachineID: "replica-b", Port: 3306},
		},
	}
	request := clusterUpgradeSwitchoverRequest(run, "replica-a", "primary", false)
	if len(request.MaintenanceDetachedMachineIDs) != 0 {
		t.Fatalf("switch-back unexpectedly detaches nodes: %v", request.MaintenanceDetachedMachineIDs)
	}
	if len(request.Nodes) != len(run.Nodes) {
		t.Fatalf("switch-back nodes = %d, want %d", len(request.Nodes), len(run.Nodes))
	}
	for _, node := range request.Nodes {
		if node.MachineID == "primary" {
			if node.Role != "M" || node.SourceMachineID != "" || node.ElectionPriority != 1000 {
				t.Fatalf("original primary is not the preferred writable target: %+v", node)
			}
			continue
		}
		if node.Role != "S" || node.SourceMachineID != "primary" {
			t.Fatalf("switch-back replica topology is invalid: %+v", node)
		}
	}
}

func TestArchitectureParticipationRequestDoesNotMutateOriginal(t *testing.T) {
	request := hadomain.ArchitectureAdjustmentRequest{
		MaintenanceDetachedMachineIDs: []string{"old"},
		Nodes: []hadomain.ArchitectureNodeRequest{
			{MachineID: "old"},
			{MachineID: "new"},
		},
	}
	filtered := architectureParticipationRequest(request)
	if len(filtered.Nodes) != 1 || filtered.Nodes[0].MachineID != "new" {
		t.Fatalf("filtered nodes = %+v", filtered.Nodes)
	}
	if len(request.Nodes) != 2 {
		t.Fatalf("original request was mutated: %+v", request.Nodes)
	}
}

func TestMySQLBool(t *testing.T) {
	for _, value := range []string{"1", "ON", " true ", "Yes"} {
		if !mysqlBool(value) {
			t.Errorf("mysqlBool(%q) = false, want true", value)
		}
	}
	for _, value := range []string{"0", "OFF", "", "no"} {
		if mysqlBool(value) {
			t.Errorf("mysqlBool(%q) = true, want false", value)
		}
	}
}
