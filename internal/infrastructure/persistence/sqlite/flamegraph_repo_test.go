package sqlite

import (
	"context"
	"database/sql"
	"testing"
	"time"

	flamegraphdomain "gmha/internal/domain/flamegraph"
	_ "modernc.org/sqlite"
)

func newFlameGraphTestRepository(t *testing.T) *FlameGraphRepository {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	repo := NewFlameGraphRepository(NewDB(db, DialectSQLite))
	if err := repo.Migrate(); err != nil {
		t.Fatal(err)
	}
	return repo
}

func TestFlameGraphRepositoryPersistsProfilesAndKeepsListCompact(t *testing.T) {
	repo := newFlameGraphTestRepository(t)
	ctx := context.Background()
	now := time.Date(2026, 7, 23, 3, 0, 0, 0, time.UTC)
	profile := flamegraphdomain.Profile{
		ID: "fg-1", Cluster: "orders", MachineID: "machine-1", TargetType: "process", Target: "mysqld",
		DurationSec: 30, FrequencyHz: 99, RequestedTool: "auto", Status: "pending", CreatedAt: now,
	}
	if err := repo.CreateProfile(ctx, profile); err != nil {
		t.Fatal(err)
	}
	if err := repo.AttachProfileTask(ctx, profile.ID, "task-1"); err != nil {
		t.Fatal(err)
	}
	folded := "process:mysqld;dispatch;execute 42\n"
	if err := repo.CompleteProfile(ctx, profile.ID, "success", "perf", 42, 1, folded, "", now, now.Add(30*time.Second)); err != nil {
		t.Fatal(err)
	}
	got, ok, err := repo.GetProfile(ctx, profile.ID)
	if err != nil || !ok {
		t.Fatalf("GetProfile() ok=%v err=%v", ok, err)
	}
	if got.TaskID != "task-1" || got.FoldedStacks != folded || got.SampleCount != 42 || got.Backend != "perf" {
		t.Fatalf("unexpected profile: %+v", got)
	}
	items, err := repo.ListProfiles(ctx, "orders", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].FoldedStacks != "" {
		t.Fatalf("list should omit large folded payload: %+v", items)
	}
}

func TestFlameGraphRepositoryListsDueSchedules(t *testing.T) {
	repo := newFlameGraphTestRepository(t)
	ctx := context.Background()
	now := time.Date(2026, 7, 23, 3, 0, 0, 0, time.UTC)
	schedule := flamegraphdomain.Schedule{
		ID: "schedule-1", Name: "daily", Cluster: "orders", MachineID: "machine-1", TargetType: "system",
		DurationSec: 30, FrequencyHz: 99, Backend: "auto", ScheduleType: "once", StartAt: now,
		Enabled: true, NextRunAt: now, CreatedAt: now, UpdatedAt: now,
	}
	if err := repo.SaveSchedule(ctx, schedule); err != nil {
		t.Fatal(err)
	}
	items, err := repo.ListDueSchedules(ctx, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != schedule.ID {
		t.Fatalf("unexpected due schedules: %+v", items)
	}
	if err := repo.UpdateScheduleRun(ctx, schedule.ID, now, time.Time{}, false); err != nil {
		t.Fatal(err)
	}
	items, err = repo.ListDueSchedules(ctx, now.Add(time.Hour))
	if err != nil || len(items) != 0 {
		t.Fatalf("completed one-shot schedule remained due: %+v err=%v", items, err)
	}
}
