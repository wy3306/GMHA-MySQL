package handler

import (
	"encoding/json"
	"testing"
	"time"

	dynamicdomain "gmha/internal/domain/dynamic"
	heartbeatdomain "gmha/internal/domain/heartbeat"
)

func TestClusterOverviewSummaryJSONExposesEveryDashboardMetric(t *testing.T) {
	payload, err := json.Marshal(clusterOverviewSummary{QPS: 1, TPS: 2, CPUPercent: 3, IOBusyPercent: 4, IOReadBytes: 5, IOWriteBytes: 6, DataBytes: 7, IndexBytes: 8, MachineCount: 9})
	if err != nil {
		t.Fatal(err)
	}
	var values map[string]any
	if err := json.Unmarshal(payload, &values); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"qps", "tps", "cpu_percent", "io_busy_percent", "io_read_bytes_sec", "io_write_bytes_sec", "data_bytes", "index_bytes", "machine_count"} {
		if _, ok := values[key]; !ok {
			t.Fatalf("dashboard field %s missing from %s", key, payload)
		}
	}
}

func TestAggregateOverviewHistoryCalculatesRatesAndResources(t *testing.T) {
	base := time.Date(2026, 7, 17, 10, 0, 0, 0, time.UTC)
	metric := func(name string, value any, at time.Time) dynamicdomain.MetricResult {
		return dynamicdomain.MetricResult{Name: name, Success: true, Value: value, CollectedAt: at, Labels: map[string]string{"mysql_port": "3306"}}
	}
	snapshots := []heartbeatdomain.MetricSnapshot{
		{MachineID: "m1", CollectedAt: base, Metrics: []dynamicdomain.MetricResult{
			metric("mysql_qps", 100.0, base), metric("mysql_tps", 10.0, base), metric("cpu_usage_percent", 20.0, base),
			metric("io_status", map[string]any{"sda": map[string]any{"busy_ratio": .25, "read_bytes_sec": 1000.0, "write_bytes_sec": 500.0}}, base),
			metric("network_throughput", map[string]any{"eth0": map[string]any{"receive_bytes_sec": 2000.0, "transmit_bytes_sec": 1000.0}}, base),
		}},
		{MachineID: "m1", CollectedAt: base.Add(10 * time.Second), Metrics: []dynamicdomain.MetricResult{
			metric("mysql_qps", 150.0, base.Add(10*time.Second)), metric("mysql_tps", 30.0, base.Add(10*time.Second)), metric("cpu_usage_percent", 40.0, base.Add(10*time.Second)),
			metric("filesystem_usage", []any{map[string]any{"mount": "/data", "used_percent": 72.0}}, base.Add(10*time.Second)),
		}},
	}
	series := aggregateOverviewHistory(snapshots, 15)
	if len(series) != 1 {
		t.Fatalf("expected one bucket, got %+v", series)
	}
	point := series[0]
	if point.QPS != 5 || point.TPS != 2 {
		t.Fatalf("unexpected calculated rates: %+v", point)
	}
	if point.CPUPercent != 30 || point.IOBusyPercent != 25 || point.DiskUsedPercent != 72 {
		t.Fatalf("unexpected resources: %+v", point)
	}
	if point.NetworkReceiveBytes != 2000 || point.NetworkTransmitBytes != 1000 {
		t.Fatalf("unexpected network rates: %+v", point)
	}
}

func TestOverviewArchitectureLabel(t *testing.T) {
	if got := overviewArchitecture(map[string]int{"M": 1, "S": 2}, 3); got != "一主 2 从" {
		t.Fatalf("unexpected architecture: %s", got)
	}
	if got := overviewArchitecture(nil, 0); got != "尚未部署实例" {
		t.Fatalf("unexpected empty architecture: %s", got)
	}
}

func TestOverviewStartupRatesProvideImmediateFallback(t *testing.T) {
	qps, tps := overviewStartupRates([]clusterTopologyNode{{QPS: "1200", TPS: "300", Uptime: "60"}, {QPS: "600", TPS: "120", Uptime: "60"}})
	if qps != 30 || tps != 7 {
		t.Fatalf("unexpected fallback rates qps=%v tps=%v", qps, tps)
	}
}
