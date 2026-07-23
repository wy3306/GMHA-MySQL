package sqlite

import (
	"context"
	"database/sql"
	"testing"
	"time"

	sqldomain "gmha/internal/domain/sqldiagnostic"
	_ "modernc.org/sqlite"
)

func newSQLDiagnosticTestRepository(t *testing.T) (*SQLDiagnosticRepository, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	repo := NewSQLDiagnosticRepository(NewDB(db, DialectSQLite))
	if err := repo.Migrate(); err != nil {
		t.Fatal(err)
	}
	return repo, db
}

func TestSQLDiagnosticRepositorySessionLifecycleAndConfig(t *testing.T) {
	repo, _ := newSQLDiagnosticTestRepository(t)
	ctx := context.Background()
	cfg, err := repo.LoadConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Enabled || cfg.CollectionIntervalSeconds != 5 || cfg.RetentionHours != 24 {
		t.Fatalf("unexpected default config: %+v", cfg)
	}
	cfg.RetentionHours = 72
	cfg.RedactLiterals = true
	if err := repo.SaveConfig(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	reloaded, err := repo.LoadConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.RetentionHours != 72 || !reloaded.RedactLiterals {
		t.Fatalf("config was not persisted: %+v", reloaded)
	}

	instance := sqldomain.Instance{MachineID: "machine-1", MachineName: "db-1", MachineIP: "10.0.0.1", Cluster: "orders", Port: 3306, Version: "8.0.40"}
	t1 := time.Date(2026, 7, 23, 2, 0, 0, 0, time.UTC)
	session := sqldomain.Session{
		ID: "session-1", Instance: instance, ProcessID: 42, User: "app",
		SQLText: "select * from orders", Digest: "digest-1",
		QueryStartedAt: t1.Add(-2 * time.Second), FirstSeenAt: t1, LastSeenAt: t1,
		ElapsedMS: 2000, MaxElapsedMS: 2000, SampleCount: 1, Source: "processlist",
	}
	if err := repo.SaveSessionSnapshot(ctx, instance, t1, []sqldomain.Session{session}); err != nil {
		t.Fatal(err)
	}
	t2 := t1.Add(5 * time.Second)
	session.FirstSeenAt, session.LastSeenAt = t2, t2
	session.ElapsedMS, session.MaxElapsedMS = 7000, 7000
	if err := repo.SaveSessionSnapshot(ctx, instance, t2, []sqldomain.Session{session}); err != nil {
		t.Fatal(err)
	}
	t3 := t2.Add(5 * time.Second)
	if err := repo.SaveSessionSnapshot(ctx, instance, t3, nil); err != nil {
		t.Fatal(err)
	}
	items, err := repo.ListSessions(ctx, t1.Add(-time.Minute), t3.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("expected one lifecycle, got %d", len(items))
	}
	got := items[0]
	if got.SampleCount != 2 || got.MaxElapsedMS != 7000 || got.EndedAt == nil || !got.EndedAt.Equal(t3) {
		t.Fatalf("unexpected persisted lifecycle: %+v", got)
	}
}

func TestSQLDiagnosticRepositoryEventsStatusAndPurge(t *testing.T) {
	repo, _ := newSQLDiagnosticTestRepository(t)
	ctx := context.Background()
	now := time.Date(2026, 7, 23, 3, 0, 0, 0, time.UTC)
	instance := sqldomain.Instance{MachineID: "machine-1", MachineName: "db-1", MachineIP: "10.0.0.1", Cluster: "orders", Port: 3306}
	event := sqldomain.StatementEvent{
		ID: "event-1", Instance: instance, ServerBootID: 100, ThreadID: 2, EventID: 3,
		User: "app", ClientHost: "10.0.0.8", SQLText: "select 1", Digest: "digest-1", DurationMS: 1200,
		StartedAt: now.Add(-1200 * time.Millisecond), EndedAt: now, CollectedAt: now,
	}
	if err := repo.SaveStatementEvents(ctx, []sqldomain.StatementEvent{event, event}); err != nil {
		t.Fatal(err)
	}
	events, err := repo.ListStatementEvents(ctx, sqldomain.StatementEventQuery{Start: now.Add(-time.Hour), End: now.Add(time.Hour), MinimumDurationMS: 1000, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].DurationMS != 1200 || events[0].User != "app" {
		t.Fatalf("event de-duplication or threshold failed: %+v", events)
	}
	status := sqldomain.InstanceStatus{
		Instance: instance, Status: "ok", CollectionMode: "full",
		LastAttemptAt: now, LastSuccessAt: now, PerformanceSchemaAvailable: true,
		HistoryLongConsumerEnabled: true, DigestConsumerEnabled: true,
	}
	if err := repo.SaveInstanceStatus(ctx, status); err != nil {
		t.Fatal(err)
	}
	runs, err := repo.ListCollectionStatuses(ctx, now.Add(-time.Minute), now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].CollectionMode != "full" || !runs[0].HistoryLongConsumerEnabled {
		t.Fatalf("unexpected collection run: %+v", runs)
	}
	if _, err := repo.PurgeBefore(ctx, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	events, err = repo.ListStatementEvents(ctx, sqldomain.StatementEventQuery{Start: now.Add(-time.Hour), End: now.Add(time.Hour), Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("expected event to be purged, got %+v", events)
	}
}
