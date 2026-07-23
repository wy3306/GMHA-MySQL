package dynamic

import "testing"

func TestAvailableMemorySupportsCurrentAndLegacyLinuxMeminfo(t *testing.T) {
	current := parseProcMeminfo([]byte("MemTotal: 1000 kB\nMemAvailable: 420 kB\n"))
	if got := availableMemoryKB(current); got != 420 {
		t.Fatalf("current kernel available = %d, want 420", got)
	}

	legacy := parseProcMeminfo([]byte(
		"MemTotal: 1000 kB\nMemFree: 100 kB\nBuffers: 50 kB\nCached: 200 kB\nSReclaimable: 30 kB\nShmem: 20 kB\n",
	))
	if got := availableMemoryKB(legacy); got != 360 {
		t.Fatalf("legacy kernel available = %d, want 360", got)
	}
}

func TestAvailableMemoryNeverExceedsTotal(t *testing.T) {
	values := map[string]uint64{"MemTotal": 100, "MemFree": 90, "Cached": 80}
	if got := availableMemoryKB(values); got != 100 {
		t.Fatalf("available = %d, want capped value 100", got)
	}
}
