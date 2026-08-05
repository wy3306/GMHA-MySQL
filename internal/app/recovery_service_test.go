package app

import (
	"testing"
	"time"

	recoverydomain "gmha/internal/domain/recovery"
)

func TestRecoveryStateExpirationRequiresAnOldActiveLease(t *testing.T) {
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	oldAttempt := now.Add(-10 * time.Minute)
	freshAttempt := now.Add(-time.Minute)

	if !recoveryStateExpired(recoverydomain.LatestState{InProgress: true, LastAttemptAt: &oldAttempt, UpdatedAt: oldAttempt}, now, 3*time.Minute) {
		t.Fatal("an abandoned recovery must expire")
	}
	if recoveryStateExpired(recoverydomain.LatestState{InProgress: true, LastAttemptAt: &freshAttempt, UpdatedAt: freshAttempt}, now, 3*time.Minute) {
		t.Fatal("an active recovery lease must remain visible")
	}
	if recoveryStateExpired(recoverydomain.LatestState{InProgress: false, LastAttemptAt: &oldAttempt, UpdatedAt: oldAttempt}, now, 3*time.Minute) {
		t.Fatal("an idle state must never be treated as stale recovery")
	}
}
