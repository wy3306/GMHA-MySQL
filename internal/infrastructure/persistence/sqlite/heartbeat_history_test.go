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
	want := heartbeatdomain.MetricSnapshot{AgentID: "a1", MachineID: "m1", ClusterID: "demo", Abnormal: true, CollectedAt: now, Metrics: []dynamicdomain.MetricResult{{Name: "cpu_usage_percent", Success: true, Value: 42.5, CollectedAt: now}}}
	if err := repo.AppendMetricSnapshot(context.Background(), want); err != nil {
		t.Fatal(err)
	}
	for _, collectedAt := range []time.Time{now.Add(-2 * time.Minute), now.Add(2 * time.Minute)} {
		item := want
		item.CollectedAt = collectedAt
		if err := repo.AppendMetricSnapshot(context.Background(), item); err != nil {
			t.Fatal(err)
		}
	}
	items, err := repo.ListMetricSnapshots(context.Background(), "demo", now.Add(-time.Minute), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].MachineID != "m1" || len(items[0].Metrics) != 1 {
		t.Fatalf("unexpected history: %+v", items)
	}
	if value, ok := items[0].Metrics[0].Value.(float64); !ok || value != 42.5 {
		t.Fatalf("unexpected metric value: %#v", items[0].Metrics[0].Value)
	}
	if !items[0].Abnormal {
		t.Fatal("abnormal heartbeat marker was not persisted")
	}
	window, err := repo.ListMetricSnapshotsRange(context.Background(), "demo", now.Add(-time.Minute), now.Add(time.Minute), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(window) != 1 || !window[0].CollectedAt.Equal(now) {
		t.Fatalf("unexpected bounded history: %+v", window)
	}
}

func TestHeartbeatMetricSamplesAreDurableIdempotentAndBounded(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := NewHeartbeatRepository(NewDB(db, DialectSQLite))
	if err := repo.Migrate(); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC)
	value := 42.5
	item := heartbeatdomain.MetricSample{
		AgentID: "a1", MachineID: "m1", ClusterID: "demo", Scope: "machine",
		Category: "cpu", MetricName: "cpu_usage_percent", Labels: map[string]string{"cpu": "all"},
		ValueType: dynamicdomain.ValueTypeFloat, NumericValue: &value, Value: value,
		Success: true, CollectedAt: now,
	}
	if err := repo.AppendMetricSamples(context.Background(), []heartbeatdomain.MetricSample{item, item}); err != nil {
		t.Fatal(err)
	}
	items, err := repo.ListMetricSamples(context.Background(), heartbeatdomain.MetricSampleQuery{
		ClusterID: "demo", Metric: "cpu_usage_percent",
		StartAt: now.Add(-time.Minute), EndAt: now.Add(time.Minute), Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("deduplicated samples = %d, want 1", len(items))
	}
	if items[0].NumericValue == nil || *items[0].NumericValue != value || items[0].Labels["cpu"] != "all" {
		t.Fatalf("unexpected persisted sample: %+v", items[0])
	}
	outside, err := repo.ListMetricSamples(context.Background(), heartbeatdomain.MetricSampleQuery{
		ClusterID: "demo", Metric: "cpu_usage_percent",
		StartAt: now.Add(time.Minute), EndAt: now.Add(2 * time.Minute), Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(outside) != 0 {
		t.Fatalf("out-of-range samples = %d, want 0", len(outside))
	}
}

func TestMetricHistoryCleanupIsBounded(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := NewHeartbeatRepository(NewDB(db, DialectSQLite))
	if err := repo.Migrate(); err != nil {
		t.Fatal(err)
	}
	old := time.Now().UTC().Add(-8 * 24 * time.Hour).Format(time.RFC3339Nano)
	for index := 0; index < metricCleanupBatchSize+25; index++ {
		if _, err := db.Exec(`insert into agent_metric_snapshot(agent_id,machine_id,cluster_id,metrics_json,collected_at) values(?,?,?,?,?)`, "a1", "m1", "demo", "[]", old); err != nil {
			t.Fatal(err)
		}
	}
	deleted, err := repo.deleteExpiredMetricBatch(context.Background(), "agent_metric_snapshot", time.Now().UTC().Add(-metricHistoryRetention).Format(time.RFC3339Nano))
	if err != nil {
		t.Fatal(err)
	}
	if deleted != metricCleanupBatchSize {
		t.Fatalf("deleted = %d, want bounded batch %d", deleted, metricCleanupBatchSize)
	}
	var remaining int
	if err := db.QueryRow(`select count(*) from agent_metric_snapshot`).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 25 {
		t.Fatalf("remaining = %d, want 25", remaining)
	}
}

func TestMetricHistoryCleanupRetainsAbnormalHeartbeats(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := NewHeartbeatRepository(NewDB(db, DialectSQLite))
	if err := repo.Migrate(); err != nil {
		t.Fatal(err)
	}
	old := time.Now().UTC().Add(-8 * 24 * time.Hour)
	for _, item := range []heartbeatdomain.MetricSnapshot{
		{AgentID: "normal", MachineID: "m1", ClusterID: "demo", CollectedAt: old, Metrics: []dynamicdomain.MetricResult{{Name: "cpu", Success: true}}},
		{AgentID: "abnormal", MachineID: "m2", ClusterID: "demo", Abnormal: true, CollectedAt: old, Metrics: []dynamicdomain.MetricResult{{Name: "cpu", Success: true}}},
	} {
		if err := repo.AppendMetricSnapshot(context.Background(), item); err != nil {
			t.Fatal(err)
		}
	}
	value := 1.0
	if err := repo.AppendMetricSamples(context.Background(), []heartbeatdomain.MetricSample{
		{AgentID: "normal", MachineID: "m1", ClusterID: "demo", MetricName: "cpu", NumericValue: &value, Value: value, Success: true, CollectedAt: old},
		{AgentID: "failed", MachineID: "m2", ClusterID: "demo", MetricName: "cpu", NumericValue: &value, Value: value, Success: false, Error: "collector failed", CollectedAt: old},
		{AgentID: "abnormal", MachineID: "m3", ClusterID: "demo", MetricName: "cpu", NumericValue: &value, Value: value, Success: true, HeartbeatAbnormal: true, CollectedAt: old},
	}); err != nil {
		t.Fatal(err)
	}
	deleted, err := repo.CleanupMetricHistory(context.Background(), time.Now().UTC().Add(-metricHistoryRetention))
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 2 {
		t.Fatalf("deleted = %d, want the normal snapshot and sample only", deleted)
	}
	var snapshots, samples int
	if err := db.QueryRow(`select count(*) from agent_metric_snapshot`).Scan(&snapshots); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`select count(*) from performance_metric_sample`).Scan(&samples); err != nil {
		t.Fatal(err)
	}
	if snapshots != 1 || samples != 2 {
		t.Fatalf("retained snapshots=%d samples=%d, want 1 abnormal snapshot and 2 abnormal samples", snapshots, samples)
	}
}
