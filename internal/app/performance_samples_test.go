package app

import (
	"testing"
	"time"

	dynamicdomain "gmha/internal/domain/dynamic"
	hbdomain "gmha/internal/domain/heartbeat"
)

func TestNormalizePerformanceSamplesFlattensMachinePayloads(t *testing.T) {
	at := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	status := hbdomain.LatestStatus{AgentID: "agent-1", MachineID: "machine-1", ClusterID: "cluster-1"}
	samples := normalizePerformanceSamples(status, []dynamicdomain.MetricResult{
		{Name: "io_status", Category: "host", Success: true, ValueType: dynamicdomain.ValueTypeMap, CollectedAt: at, Value: map[string]any{
			"nvme0n1": map[string]any{"busy_ratio": 0.42, "read_iops": 120.0, "write_iops": 80.0, "read_bytes_sec": 4096.0, "write_bytes_sec": 2048.0},
		}},
		{Name: "network_throughput", Category: "host", Success: true, ValueType: dynamicdomain.ValueTypeMap, CollectedAt: at, Value: map[string]any{
			"eth0": map[string]any{"receive_bytes_sec": 1024.0, "transmit_bytes_sec": 512.0},
		}},
	}, at)

	assertSample := func(name, labelKey, labelValue string, expected float64) {
		t.Helper()
		for _, item := range samples {
			if item.MetricName == name && item.Labels[labelKey] == labelValue && item.NumericValue != nil {
				if *item.NumericValue != expected {
					t.Fatalf("%s = %v, want %v", name, *item.NumericValue, expected)
				}
				return
			}
		}
		t.Fatalf("missing normalized sample %s", name)
	}
	assertSample("host_disk_busy_percent", "device", "nvme0n1", 42)
	assertSample("host_disk_read_iops", "device", "nvme0n1", 120)
	assertSample("host_network_receive_bytes_sec", "interface", "eth0", 1024)
	assertSample("host_network_transmit_bytes_sec", "interface", "eth0", 512)
}

func TestNormalizePerformanceSamplesPreservesMySQLInstance(t *testing.T) {
	at := time.Now().UTC()
	samples := normalizePerformanceSamples(hbdomain.LatestStatus{
		AgentID: "a", MachineID: "m", ClusterID: "c",
	}, []dynamicdomain.MetricResult{{
		Name: "mysql_threads_connected", Category: "connections", Success: true,
		ValueType: dynamicdomain.ValueTypeFloat, Value: float64(18), CollectedAt: at,
		Labels: map[string]string{"mysql_host": "10.0.0.8", "mysql_port": "3307"},
	}}, at)
	if len(samples) != 1 {
		t.Fatalf("samples = %d, want 1", len(samples))
	}
	if samples[0].Instance != "10.0.0.8:3307" || samples[0].NumericValue == nil || *samples[0].NumericValue != 18 {
		t.Fatalf("unexpected sample: %+v", samples[0])
	}
}

func TestNormalizePerformanceSamplesFlattensMemoryAnalysis(t *testing.T) {
	at := time.Now().UTC()
	samples := normalizePerformanceSamples(hbdomain.LatestStatus{
		AgentID: "a", MachineID: "m", ClusterID: "c",
	}, []dynamicdomain.MetricResult{
		{Name: "host_memory_detail", Category: "memory", Success: true, ValueType: dynamicdomain.ValueTypeMap, CollectedAt: at, Value: map[string]any{
			"total_bytes": 1000.0, "available_bytes": 400.0, "mysql_process_rss_bytes": 250.0,
		}},
		{Name: "mysql_memory_modules", Category: "memory", Success: true, ValueType: dynamicdomain.ValueTypeArray, CollectedAt: at,
			Labels: map[string]string{"mysql_host": "127.0.0.1", "mysql_port": "3306"},
			Value: []map[string]any{
				{"event_name": "memory/innodb/buf_buf_pool", "group": "innodb", "module": "buf_buf_pool", "current_bytes": 300.0, "high_bytes": 500.0, "current_count_used": 3.0},
				{"event_name": "memory/sql/TABLE", "group": "sql", "module": "TABLE", "current_bytes": 100.0, "high_bytes": 150.0, "current_count_used": 2.0},
			}},
	}, at)

	find := func(name string, labels map[string]string) float64 {
		t.Helper()
		for _, sample := range samples {
			if sample.MetricName != name || sample.NumericValue == nil {
				continue
			}
			match := true
			for key, value := range labels {
				match = match && sample.Labels[key] == value
			}
			if match {
				return *sample.NumericValue
			}
		}
		t.Fatalf("missing sample %s with labels %+v", name, labels)
		return 0
	}
	if got := find("host_memory_available_bytes", nil); got != 400 {
		t.Fatalf("host available = %v, want 400", got)
	}
	if got := find("mysql_memory_module_bytes", map[string]string{"group": "innodb"}); got != 300 {
		t.Fatalf("innodb bytes = %v, want 300", got)
	}
	if got := find("mysql_memory_tracked_bytes", nil); got != 400 {
		t.Fatalf("tracked bytes = %v, want 400", got)
	}
	if got := find("mysql_memory_high_water_bytes", nil); got != 650 {
		t.Fatalf("high water bytes = %v, want 650", got)
	}
}
