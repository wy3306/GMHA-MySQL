package dynamic

import (
	"strings"
	"testing"
)

func TestBuildDefaultMySQLDynamicCollectConfig(t *testing.T) {
	cfg := BuildDefaultMySQLDynamicCollectConfig()
	if !cfg.Enabled {
		t.Fatalf("expected mysql dynamic collection enabled")
	}
	if cfg.Version == "" {
		t.Fatalf("expected version")
	}
	seen := map[string]CollectTaskSpec{}
	for _, task := range cfg.Tasks {
		seen[task.Name] = task
		if task.Type != TaskTypeBuiltin {
			t.Fatalf("expected builtin task %s, got %s", task.Name, task.Type)
		}
		if task.Category == "" {
			t.Fatalf("expected category for %s", task.Name)
		}
		if task.IntervalSeconds <= 0 {
			t.Fatalf("expected positive interval for %s", task.Name)
		}
	}
	for _, name := range []string{
		"mysql_threads_connected", "mysql_connectivity", "mysql_qps", "mysql_error_log_size", "mysql_slow_query_threshold",
		"mysql_fragmented_table_count", "mysql_max_table_fragment_percent", "mysql_tablespace_fragment_total_bytes",
	} {
		if _, ok := seen[name]; !ok {
			t.Fatalf("expected default mysql task %s", name)
		}
	}
	if got := seen["mysql_threads_connected"].IntervalSeconds; got != 5 {
		t.Fatalf("expected 5s task, got %d", got)
	}
	if got := seen["mysql_qps"].IntervalSeconds; got != 5 {
		t.Fatalf("expected 5s task, got %d", got)
	}
	if got := seen["mysql_error_log_size"].IntervalSeconds; got != 30 {
		t.Fatalf("expected 30s task, got %d", got)
	}
	if got := seen["mysql_slow_query_threshold"].IntervalSeconds; got != 300 {
		t.Fatalf("expected 300s task, got %d", got)
	}
	for _, name := range []string{"mysql_fragmented_table_count", "mysql_max_table_fragment_percent", "mysql_tablespace_fragment_total_bytes"} {
		task := seen[name]
		if !task.Enabled || task.IntervalSeconds != 300 {
			t.Fatalf("expected enabled 300s fragmentation task %s, got %+v", name, task)
		}
		query := task.Params["query"]
		if query == "" || !strings.Contains(query, "information_schema.tables") {
			t.Fatalf("fragmentation task %s has no table metadata query: %q", name, query)
		}
	}
}
