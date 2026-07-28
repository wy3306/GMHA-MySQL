package handler

import "testing"

func TestTopologyEdgeIncludesConfiguredSQLDelay(t *testing.T) {
	node := clusterTopologyNode{IP: "10.0.0.2", Port: 3306, Name: "replica"}
	edge, ok := topologyEdgeFromMetric(node, map[string]any{
		"io_running":  "Yes",
		"sql_running": "Yes",
		"lag_seconds": 120,
		"replica_status": map[string]any{
			"Source_Host": "10.0.0.1",
			"Source_Port": 3306,
			"SQL_Delay":   3600,
		},
	})
	if !ok {
		t.Fatal("expected replication edge")
	}
	if edge.SQLDelay != 3600 || edge.SourceIP != "10.0.0.1" || edge.Lag != "120" {
		t.Fatalf("unexpected topology edge: %+v", edge)
	}
}

func TestApplyTopologyGroupReplication(t *testing.T) {
	node := clusterTopologyNode{Name: "db-1"}
	applyTopologyGroupReplication(&node, map[string]any{
		"active":       true,
		"group_name":   "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
		"member_count": 3,
		"online_count": 3,
		"quorum":       true,
		"self": map[string]any{
			"member_id": "server-uuid-1", "member_state": "ONLINE", "member_role": "PRIMARY",
		},
	})
	if node.GroupRole != "PRIMARY" || node.GroupState != "ONLINE" || node.GroupMembers != 3 || node.GroupOnline != 3 || !node.GroupQuorum {
		t.Fatalf("unexpected MGR topology node: %+v", node)
	}
}
