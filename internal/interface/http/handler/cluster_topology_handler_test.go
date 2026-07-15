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
