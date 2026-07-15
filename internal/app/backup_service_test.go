package app

import (
	"strings"
	"testing"
	"time"

	backupdomain "gmha/internal/domain/backup"
)

func TestNextBackupTimeCustomInterval(t *testing.T) {
	start := time.Date(2026, 7, 13, 10, 0, 0, 0, time.Local)
	p := backupdomain.Policy{ScheduleType: backupdomain.ScheduleCustom, StartAt: start, IntervalMinutes: 30}
	got := nextBackupTime(p, start.Add(31*time.Minute))
	want := start.Add(60 * time.Minute).UTC()
	if !got.Equal(want) {
		t.Fatalf("next=%s want=%s", got, want)
	}
}

func TestNextBackupTimeWeekly(t *testing.T) {
	start := time.Date(2026, 7, 13, 3, 15, 0, 0, time.Local) // Monday
	p := backupdomain.Policy{ScheduleType: backupdomain.ScheduleWeekly, StartAt: start, Weekdays: []int{1, 5}}
	got := nextBackupTime(p, time.Date(2026, 7, 13, 4, 0, 0, 0, time.Local))
	want := time.Date(2026, 7, 17, 3, 15, 0, 0, time.Local).UTC()
	if !got.Equal(want) {
		t.Fatalf("next=%s want=%s", got, want)
	}
}

func TestNextBackupTimeWeeklyDoesNotRunBeforeStartDate(t *testing.T) {
	start := time.Date(2027, 7, 12, 4, 0, 0, 0, time.Local) // Monday
	p := backupdomain.Policy{ScheduleType: backupdomain.ScheduleWeekly, StartAt: start, Weekdays: []int{1, 5}}
	got := nextBackupTime(p, time.Date(2026, 7, 13, 4, 0, 0, 0, time.Local))
	if !got.Equal(start.UTC()) {
		t.Fatalf("next=%s want=%s", got, start.UTC())
	}
}

func TestRenderRemoteScriptQuotesArguments(t *testing.T) {
	command := renderRemoteScript("#!/bin/bash\necho ok\n", "backup", []string{"--target-dir", "/data/a b", "--user", "o'reilly"})
	for _, expected := range []string{"base64 -d", "'/data/a b'", `'o'"'"'reilly'`, "rm -f"} {
		if !strings.Contains(command, expected) {
			t.Fatalf("command does not contain %q: %s", expected, command)
		}
	}
}
