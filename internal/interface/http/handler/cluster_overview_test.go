package handler

import (
	"encoding/json"
	"testing"
	"time"

	dynamicdomain "gmha/internal/domain/dynamic"
	heartbeatdomain "gmha/internal/domain/heartbeat"
)

func TestClusterOverviewSummaryJSONExposesEveryDashboardMetric(t *testing.T) {
	payload, err := json.Marshal(clusterOverviewSummary{QPS: 1, TPS: 2, ConnectedSessions: 3, ActiveSessions: 4, LockWaitSessions: 5, ReplicationLag: 6, CPUPercent: 7, IOBusyPercent: 8, IOReadBytes: 9, IOWriteBytes: 10, DataBytes: 11, IndexBytes: 12, MachineCount: 13})
	if err != nil {
		t.Fatal(err)
	}
	var values map[string]any
	if err := json.Unmarshal(payload, &values); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"qps", "tps", "connected_sessions", "active_sessions", "lock_wait_sessions", "replication_lag_seconds", "cpu_percent", "io_busy_percent", "io_read_bytes_sec", "io_write_bytes_sec", "data_bytes", "index_bytes", "machine_count"} {
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
			metric("mysql_threads_connected", 8.0, base), metric("mysql_active_connections", 3.0, base),
			metric("mysql_lock_wait_sessions", 1.0, base), metric("mysql_replication_lag", 2.0, base),
			metric("io_status", map[string]any{"sda": map[string]any{"busy_ratio": .25, "read_bytes_sec": 1000.0, "write_bytes_sec": 500.0}}, base),
			metric("network_throughput", map[string]any{"eth0": map[string]any{"receive_bytes_sec": 2000.0, "transmit_bytes_sec": 1000.0}}, base),
		}},
		{MachineID: "m1", CollectedAt: base.Add(10 * time.Second), Metrics: []dynamicdomain.MetricResult{
			metric("mysql_qps", 150.0, base.Add(10*time.Second)), metric("mysql_tps", 30.0, base.Add(10*time.Second)), metric("cpu_usage_percent", 40.0, base.Add(10*time.Second)),
			metric("mysql_threads_connected", 10.0, base.Add(10*time.Second)), metric("mysql_active_connections", 5.0, base.Add(10*time.Second)),
			metric("mysql_lock_wait_sessions", 2.0, base.Add(10*time.Second)), metric("mysql_replication_lag", 4.0, base.Add(10*time.Second)),
			metric("mysql_data_disk_usage", map[string]any{"path": "/srv/mysql/3306/data", "used_percent": 72.0}, base.Add(10*time.Second)),
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
	if point.ConnectedSessions != 9 || point.ActiveSessions != 4 || point.LockWaitSessions != 1.5 || point.ReplicationLag != 3 {
		t.Fatalf("unexpected performance gauges: %+v", point)
	}
}

func TestMySQLDataDiskMetricDrivesOverviewAndMachineUsage(t *testing.T) {
	summary := clusterOverviewSummary{}
	machine := clusterOverviewMachine{}
	applyCurrentMySQLMetric(&summary, &machine, dynamicdomain.MetricResult{
		Name:    "mysql_data_disk_usage",
		Success: true,
		Value:   map[string]any{"path": "/srv/mysql/3306/data", "used_percent": 72.0},
	})
	applyCurrentMySQLMetric(&summary, &machine, dynamicdomain.MetricResult{
		Name:    "mysql_binlog_disk_usage",
		Success: true,
		Value:   map[string]any{"path": "/logs/mysql", "used_percent": 99.0},
	})
	if summary.DiskUsedPercent != 72 || machine.DiskUsedPercent != 72 {
		t.Fatalf("overview must use the database data directory disk: summary=%+v machine=%+v", summary, machine)
	}
}

func TestOverviewStorageKeepsSeparateFilesystemsAndMergesSharedPurposes(t *testing.T) {
	node := clusterTopologyNode{MachineID: "m1", Name: "db-1", IP: "10.0.0.1"}
	filesystems := []overviewStorageUsage{
		{mount: "/", source: "/dev/sda1", fsType: "xfs", totalBytes: 100, usedBytes: 50, availableBytes: 50, usedPercent: 50, available: true},
		{mount: "/data", source: "/dev/sdb1", fsType: "xfs", totalBytes: 200, usedBytes: 120, availableBytes: 80, usedPercent: 60, available: true},
		{mount: "/backup", source: "nas:/backup", fsType: "nfs4", totalBytes: 1000, usedBytes: 700, availableBytes: 300, usedPercent: 70, available: true},
	}
	dataFS, ok := overviewFilesystemForPath(filesystems, "/data/mysql/3306/data")
	if !ok || dataFS.mount != "/data" {
		t.Fatalf("data path resolved to wrong filesystem: %+v", dataFS)
	}
	backupFS, ok := overviewFilesystemForPath(filesystems, "/backup/gmha")
	if !ok || backupFS.mount != "/backup" {
		t.Fatalf("backup path resolved to wrong filesystem: %+v", backupFS)
	}
	items := map[string]*clusterOverviewStorage{}
	addOverviewStorage(items, node, dataFS, "数据", "/data/mysql/3306/data", 3306)
	addOverviewStorage(items, node, dataFS, "Binlog", "/data/mysql/3306/binlog", 3306)
	addOverviewStorage(items, node, backupFS, "备份/NAS", "/backup/gmha", 3306)
	if len(items) != 2 {
		t.Fatalf("expected shared data/binlog disk plus separate NAS disk, got %+v", items)
	}
	data := items[overviewStorageKey("m1", dataFS)]
	if data == nil || len(data.Purposes) != 2 || data.TotalBytes != 200 || data.AvailableBytes != 80 {
		t.Fatalf("shared filesystem capacity not merged correctly: %+v", data)
	}
}

func TestFilterOverviewSnapshotsForInstanceKeepsHostAndSelectedPortMetrics(t *testing.T) {
	at := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC)
	snapshots := []heartbeatdomain.MetricSnapshot{
		{MachineID: "m1", CollectedAt: at, Metrics: []dynamicdomain.MetricResult{
			{Name: "cpu_usage_percent", Value: 20},
			{Name: "mysql_qps", Value: 100, Labels: map[string]string{"mysql_port": "3306"}},
			{Name: "mysql_qps", Value: 200, Labels: map[string]string{"mysql_port": "3307"}},
		}},
		{MachineID: "m2", CollectedAt: at, Metrics: []dynamicdomain.MetricResult{
			{Name: "cpu_usage_percent", Value: 90},
			{Name: "mysql_qps", Value: 900, Labels: map[string]string{"mysql_port": "3307"}},
		}},
	}
	filtered := filterOverviewSnapshotsForInstance(snapshots, clusterTopologyNode{MachineID: "m1", Port: 3307})
	if len(filtered) != 1 || len(filtered[0].Metrics) != 2 {
		t.Fatalf("unexpected filtered snapshots: %+v", filtered)
	}
	if filtered[0].Metrics[0].Name != "cpu_usage_percent" || filtered[0].Metrics[1].Labels["mysql_port"] != "3307" {
		t.Fatalf("wrong metrics retained for selected instance: %+v", filtered[0].Metrics)
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
