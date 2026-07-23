package mysqldynamic

import "testing"

func TestNormalizeMemoryModuleRowsKeepsModuleIdentity(t *testing.T) {
	rows := []map[string]any{{
		"event_name":    "memory/innodb/buf_buf_pool",
		"current_bytes": int64(1024),
		"high_bytes":    int64(2048),
	}}
	got := normalizeMemoryModuleRows(rows)
	if len(got) != 1 {
		t.Fatalf("rows = %d, want 1", len(got))
	}
	if got[0]["group"] != "innodb" || got[0]["module"] != "buf_buf_pool" {
		t.Fatalf("unexpected module identity: %+v", got[0])
	}
	if got[0]["current_bytes"] != int64(1024) || got[0]["high_bytes"] != int64(2048) {
		t.Fatalf("unexpected byte values: %+v", got[0])
	}
}
