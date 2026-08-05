package handler

import (
	"testing"

	hadomain "gmha/internal/domain/ha"
)

func TestOfflineMGRTopologyUsesDurableIntent(t *testing.T) {
	view := clusterTopologyView{Nodes: []clusterTopologyNode{
		{MachineID: "m2", IP: "10.0.0.2", Port: 3306, Role: "standalone"},
		{MachineID: "m1", IP: "10.0.0.1", Port: 3306, Role: "standalone"},
		{MachineID: "m3", IP: "10.0.0.3", Port: 3306, Role: "standalone"},
	}, TopologySource: "runtime"}
	intent := hadomain.TopologyIntent{Architecture: hadomain.ArchitectureMGRRouter, PrimaryMachineID: "m1", MGRGroupName: "group-1", Nodes: []hadomain.ArchitectureNodeRequest{
		{MachineID: "m1", Port: 3306, Role: "M"},
		{MachineID: "m2", Port: 3306, Role: "S"},
		{MachineID: "m3", Port: 3306, Role: "S"},
	}}
	applyTopologyIntent(&view, intent)
	evaluateTopologyConsistency(&view)
	if view.Architecture != hadomain.ArchitectureMGRRouter || view.TopologySource != "desired_state" || len(view.Edges) != 2 {
		t.Fatalf("offline MGR topology was lost: %+v", view)
	}
	if view.Nodes[0].MachineID != "m1" || view.Nodes[0].Role != "MGR_PRIMARY" || view.Nodes[1].Role != "MGR_SECONDARY" || view.Consistency != "unknown" {
		t.Fatalf("offline MGR roles or recovery state are misleading: %+v", view)
	}
}

func TestAsyncConsistencyRejectsMissingDesiredEdge(t *testing.T) {
	view := clusterTopologyView{Architecture: hadomain.ArchitectureMasterSlave, TopologySource: "runtime", expectedEdges: 2, Edges: []clusterTopologyEdge{{Observed: true, IORunning: "Yes", SQLRunning: "Yes", Lag: "0"}}}
	evaluateTopologyConsistency(&view)
	if view.Consistency != "inconsistent" {
		t.Fatalf("partial async topology reported consistent: %+v", view)
	}
}

func TestOfflineMGRMetadataDoesNotOverrideAsyncIntent(t *testing.T) {
	view := clusterTopologyView{Nodes: []clusterTopologyNode{
		{MachineID: "m1", IP: "10.0.0.1", Port: 3306, Role: "M", GroupName: "group-1", GroupState: "OFFLINE", GroupRole: "PRIMARY"},
		{MachineID: "m2", IP: "10.0.0.2", Port: 3306, Role: "S", GroupName: "group-1", GroupState: "OFFLINE", GroupRole: "SECONDARY"},
	}, Edges: []clusterTopologyEdge{{SourceIP: "10.0.0.1", SourcePort: 3306, TargetIP: "10.0.0.2", TargetPort: 3306, Observed: true}}, TopologySource: "runtime"}
	intent := hadomain.TopologyIntent{Architecture: hadomain.ArchitectureMasterSlave, PrimaryMachineID: "m1", Nodes: []hadomain.ArchitectureNodeRequest{
		{MachineID: "m1", Port: 3306, Role: "M"},
		{MachineID: "m2", Port: 3306, Role: "S", SourceMachineID: "m1"},
	}}

	applyTopologyIntent(&view, intent)

	if view.Architecture != hadomain.ArchitectureMasterSlave || view.TopologySource != "runtime+desired_state" {
		t.Fatalf("offline MGR metadata overrode the asynchronous topology: %+v", view)
	}
	if hasObservedMGR(view.Nodes) {
		t.Fatalf("offline MGR metadata was treated as a live group: %+v", view.Nodes)
	}
}
