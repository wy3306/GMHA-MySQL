package mysqldynamic

import (
	"testing"

	dynamicdomain "gmha/internal/domain/dynamic"
)

func TestMySQLLogTimestampSupportsCurrentAndLegacyFormats(t *testing.T) {
	for _, line := range []string{
		"2026-07-23T10:11:12.123456+08:00 0 [ERROR] [MY-000001] message",
		"2026-07-23 10:11:12 [Warning] legacy message",
	} {
		if parsed, ok := mysqlLogTimestamp(line); !ok || parsed.IsZero() {
			t.Fatalf("failed to parse timestamp from %q", line)
		}
	}
	if _, ok := mysqlLogTimestamp("continuation without timestamp"); ok {
		t.Fatal("unexpected timestamp in continuation line")
	}
}

func TestAllDefaultMySQLMetricsHaveCollectorMappings(t *testing.T) {
	config := dynamicdomain.BuildDefaultMySQLDynamicCollectConfig()
	for _, task := range config.Tasks {
		if !task.Enabled {
			t.Fatalf("metric remains disabled: %s", task.Name)
		}
	}
}
