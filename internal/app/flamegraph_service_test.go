package app

import (
	"testing"
	"time"

	flamegraphdomain "gmha/internal/domain/flamegraph"
)

func TestNextFlameGraphRunIntervalRemainsAnchored(t *testing.T) {
	start := time.Date(2026, 7, 23, 1, 0, 0, 0, time.UTC)
	schedule := flamegraphdomain.Schedule{
		ScheduleType: flamegraphdomain.ScheduleInterval, StartAt: start, IntervalMinutes: 30,
	}
	got := nextFlameGraphRun(schedule, start.Add(31*time.Minute), false)
	want := start.Add(time.Hour)
	if !got.Equal(want) {
		t.Fatalf("next=%s want=%s", got, want)
	}
}

func TestNextFlameGraphRunOneShotDisablesAfterRun(t *testing.T) {
	now := time.Date(2026, 7, 23, 1, 0, 0, 0, time.UTC)
	schedule := flamegraphdomain.Schedule{ScheduleType: flamegraphdomain.ScheduleOnce, StartAt: now}
	if got := nextFlameGraphRun(schedule, now, false); !got.Equal(now) {
		t.Fatalf("new due one-shot next=%s want=%s", got, now)
	}
	if got := nextFlameGraphRun(schedule, now, true); !got.IsZero() {
		t.Fatalf("completed one-shot should be disabled, got %s", got)
	}
}

func TestValidateFlameGraphCaptureRejectsProcFSSystemSampling(t *testing.T) {
	err := validateFlameGraphCapture(FlameGraphCaptureRequest{
		MachineID: "machine-1", TargetType: flamegraphdomain.TargetSystem,
		DurationSec: 30, FrequencyHz: 99, Backend: flamegraphdomain.BackendProcFS,
	})
	if err == nil {
		t.Fatal("expected system procfs validation error")
	}
}
