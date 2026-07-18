package sqlite

import (
	"context"
	"database/sql"
	"testing"
	"time"

	dynamicdomain "gmha/internal/domain/dynamic"
	heartbeatdomain "gmha/internal/domain/heartbeat"
	_ "modernc.org/sqlite"
)

func TestHeartbeatMetricHistoryRoundTrip(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := NewHeartbeatRepository(NewDB(db, DialectSQLite))
	if err := repo.Migrate(); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	want := heartbeatdomain.MetricSnapshot{AgentID: "a1", MachineID: "m1", ClusterID: "demo", CollectedAt: now, Metrics: []dynamicdomain.MetricResult{{Name: "cpu_usage_percent", Success: true, Value: 42.5, CollectedAt: now}}}
	if err := repo.AppendMetricSnapshot(context.Background(), want); err != nil {
		t.Fatal(err)
	}
	items, err := repo.ListMetricSnapshots(context.Background(), "demo", now.Add(-time.Minute), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].MachineID != "m1" || len(items[0].Metrics) != 1 {
		t.Fatalf("unexpected history: %+v", items)
	}
	if value, ok := items[0].Metrics[0].Value.(float64); !ok || value != 42.5 {
		t.Fatalf("unexpected metric value: %#v", items[0].Metrics[0].Value)
	}
}
