package app

import (
	"context"
	"testing"
	"time"

	machinedomain "gmha/internal/domain/machine"
	sqldomain "gmha/internal/domain/sqldiagnostic"
	mysqlapp "gmha/internal/mysql"
)

type diagnosticAggregateRepo struct {
	SQLDiagnosticRepository
	snapshots []sqldomain.DigestSnapshot
	events    []sqldomain.StatementEvent
	statuses  []sqldomain.InstanceStatus
	runs      []sqldomain.InstanceStatus
}

func (r *diagnosticAggregateRepo) ListDigestSnapshots(context.Context, time.Time, time.Time) ([]sqldomain.DigestSnapshot, error) {
	return r.snapshots, nil
}
func (r *diagnosticAggregateRepo) ListStatementEvents(context.Context, sqldomain.StatementEventQuery) ([]sqldomain.StatementEvent, error) {
	return r.events, nil
}
func (r *diagnosticAggregateRepo) ListInstanceStatuses(context.Context) ([]sqldomain.InstanceStatus, error) {
	return r.statuses, nil
}
func (r *diagnosticAggregateRepo) ListCollectionStatuses(context.Context, time.Time, time.Time) ([]sqldomain.InstanceStatus, error) {
	return r.runs, nil
}

type diagnosticInstanceRepo struct {
	MySQLInstanceRepository
	items []mysqlapp.Instance
}

func (r *diagnosticInstanceRepo) List(context.Context) ([]mysqlapp.Instance, error) {
	return r.items, nil
}

type diagnosticMachineRepo struct {
	machinedomain.Repository
	items map[string]machinedomain.Machine
}

func (r *diagnosticMachineRepo) GetByID(_ context.Context, id string) (machinedomain.Machine, bool, error) {
	item, ok := r.items[id]
	return item, ok, nil
}

func TestTopSQLUsesWindowDeltasAndIncludesNewDigest(t *testing.T) {
	end := time.Now().UTC().Add(-time.Minute)
	start := end.Add(-2 * time.Minute)
	instance := sqldomain.Instance{MachineID: "machine-1", MachineName: "db-1", MachineIP: "10.0.0.1", Cluster: "orders", Port: 3306, Version: "8.0.40"}
	boot := start.Add(-time.Hour).Unix()
	snapshot := func(id, digest string, at time.Time, count uint64, latency float64, firstSeen time.Time) sqldomain.DigestSnapshot {
		return sqldomain.DigestSnapshot{
			ID: id, Instance: instance, ServerBootID: boot, Digest: digest,
			DigestText: "SELECT * FROM orders WHERE id = ?", Database: "orders",
			Count: count, SumTimerWaitMS: latency, SumRowsExamined: count * 2,
			FirstSeenAt: firstSeen, LastSeenAt: at, CollectedAt: at,
		}
	}
	repo := &diagnosticAggregateRepo{
		snapshots: []sqldomain.DigestSnapshot{
			snapshot("old-base", "old", start.Add(-time.Minute), 100, 1000, start.Add(-time.Hour)),
			snapshot("old-now", "old", start.Add(time.Minute), 110, 1200, start.Add(-time.Hour)),
			snapshot("new-now", "new", start.Add(time.Minute), 5, 50, start.Add(30*time.Second)),
			snapshot("reset-base", "reset", start.Add(-time.Minute), 100, 300, start.Add(-time.Hour)),
			snapshot("reset-now", "reset", start.Add(time.Minute), 3, 30, start.Add(-time.Hour)),
		},
		events: []sqldomain.StatementEvent{{
			ID: "event", Instance: instance, Digest: "old", Database: "orders",
			DurationMS: 15, StartedAt: start.Add(time.Minute), EndedAt: start.Add(time.Minute + time.Second),
		}},
	}
	status := sqldomain.InstanceStatus{
		Instance: instance, Status: "ok", CollectionMode: "full", LastAttemptAt: end,
		LastSuccessAt: end, PerformanceSchemaAvailable: true,
		HistoryLongConsumerEnabled: true, DigestConsumerEnabled: true,
	}
	foreignInstance := sqldomain.Instance{MachineID: "machine-2", MachineName: "db-2", MachineIP: "10.0.0.2", Cluster: "billing", Port: 3306}
	repo.statuses = []sqldomain.InstanceStatus{
		status,
		{Instance: foreignInstance, Status: "error", LastAttemptAt: end, LastError: "unreachable"},
	}
	repo.runs = []sqldomain.InstanceStatus{
		{Instance: instance, Status: "ok", CollectionMode: "full", LastAttemptAt: start, LastSuccessAt: start, HistoryLongConsumerEnabled: true},
		status,
	}
	cfg := sqldomain.DefaultConfig()
	cfg.CollectionIntervalSeconds = 60
	service := &SQLDiagnosticService{
		repo: repo,
		instances: &diagnosticInstanceRepo{items: []mysqlapp.Instance{
			{MachineID: instance.MachineID, Port: instance.Port, Version: instance.Version},
			{MachineID: foreignInstance.MachineID, Port: foreignInstance.Port, Version: "8.0.40"},
		}},
		machines: &diagnosticMachineRepo{items: map[string]machinedomain.Machine{
			instance.MachineID:        {ID: instance.MachineID, Name: instance.MachineName, IP: instance.MachineIP, Cluster: instance.Cluster},
			foreignInstance.MachineID: {ID: foreignInstance.MachineID, Name: foreignInstance.MachineName, IP: foreignInstance.MachineIP, Cluster: foreignInstance.Cluster},
		}},
		config: cfg,
	}
	result, err := service.TopSQL(context.Background(), SQLDiagnosticHistoryQuery{Start: start, End: end, Cluster: "orders", Limit: 10}, "total_latency_ms")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 3 {
		t.Fatalf("expected three digest aggregates, got %+v", result.Items)
	}
	if !result.Coverage.Complete || len(result.Coverage.Statuses) != 1 || result.Coverage.Statuses[0].Instance.Cluster != "orders" {
		t.Fatalf("coverage leaked another cluster: %+v", result.Coverage)
	}
	byDigest := make(map[string]SQLTopItem)
	for _, item := range result.Items {
		byDigest[item.Digest] = item
	}
	if got := byDigest["old"]; got.ExecutionCount != 10 || got.TotalLatencyMS != 200 || got.AverageLatencyMS != 20 || got.MaxObservedMS != 15 {
		t.Fatalf("incorrect old digest delta: %+v", got)
	}
	if got := byDigest["new"]; got.ExecutionCount != 5 || got.TotalLatencyMS != 50 {
		t.Fatalf("new in-window digest should use a zero baseline: %+v", got)
	}
	if got := byDigest["reset"]; got.ExecutionCount != 3 || got.TotalLatencyMS != 30 {
		t.Fatalf("counter reset should use current values: %+v", got)
	}
	ascending, err := service.TopSQL(context.Background(), SQLDiagnosticHistoryQuery{
		Start: start, End: end, Cluster: "orders", SortDirection: "asc", Limit: 10,
	}, "total_latency_ms")
	if err != nil {
		t.Fatal(err)
	}
	if ascending.SortDirection != "asc" || ascending.Items[0].Digest != "reset" || ascending.Items[2].Digest != "old" {
		t.Fatalf("TOP-SQL ascending order is incorrect: %+v", ascending.Items)
	}
}

func TestSortSlowSQLItemsSupportsAllColumnsAndDirections(t *testing.T) {
	start := time.Now().UTC()
	items := []SQLHistoryItem{
		{ID: "a", StartedAt: start, DurationMS: 300, RowsExamined: 30, RowsSent: 3, ErrorCount: 0},
		{ID: "b", StartedAt: start.Add(time.Second), DurationMS: 100, RowsExamined: 10, RowsSent: 5, ErrorCount: 2},
		{ID: "c", StartedAt: start.Add(2 * time.Second), DurationMS: 200, RowsExamined: 20, RowsSent: 1, ErrorCount: 1},
	}
	cases := []struct {
		sortBy    string
		direction string
		wantFirst string
	}{
		{"duration_ms", "desc", "a"},
		{"duration_ms", "asc", "b"},
		{"started_at", "desc", "c"},
		{"rows_examined", "asc", "b"},
		{"rows_sent", "desc", "b"},
		{"error_count", "desc", "b"},
	}
	for _, test := range cases {
		copyOfItems := append([]SQLHistoryItem(nil), items...)
		sortSlowSQLItems(copyOfItems, test.sortBy, test.direction)
		if copyOfItems[0].ID != test.wantFirst {
			t.Errorf("%s %s: got first %s, want %s", test.sortBy, test.direction, copyOfItems[0].ID, test.wantFirst)
		}
	}
}

func TestSessionHasCompletedEventUsesDigestInstanceAndStart(t *testing.T) {
	instance := sqldomain.Instance{MachineID: "machine-1", Port: 3306}
	start := time.Now().UTC()
	session := sqldomain.Session{Instance: instance, Digest: "abc", QueryStartedAt: start, TimingPrecisionMS: 1000}
	events := []sqldomain.StatementEvent{{Instance: instance, Digest: "abc", StartedAt: start.Add(time.Second)}}
	if !sessionHasCompletedEvent(session, events) {
		t.Fatal("matching completed event was not detected")
	}
	events[0].Digest = "different"
	if sessionHasCompletedEvent(session, events) {
		t.Fatal("different digest must not be de-duplicated")
	}
}
