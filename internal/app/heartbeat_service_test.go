package app

import (
	"context"
	"strings"
	"testing"
	"time"

	dynamicdomain "gmha/internal/domain/dynamic"
	hadomain "gmha/internal/domain/ha"
	hbdomain "gmha/internal/domain/heartbeat"
	machinedomain "gmha/internal/domain/machine"
	mysqlapp "gmha/internal/mysql"
)

func TestMachineStatusFromHeartbeatUsesConnectivityState(t *testing.T) {
	tests := []struct {
		name      string
		state     hbdomain.AgentState
		want      machinedomain.Status
		wantError string
		ok        bool
	}{
		{name: "online", state: hbdomain.StateOnline, want: machinedomain.StatusAgentOnline, ok: true},
		{name: "degraded remains reachable", state: hbdomain.StateDegraded, want: machinedomain.StatusAgentOnline, ok: true},
		{name: "suspect", state: hbdomain.StateSuspect, want: machinedomain.StatusAgentError, wantError: "heartbeat delayed", ok: true},
		{name: "offline", state: hbdomain.StateOffline, want: machinedomain.StatusAgentError, wantError: "heartbeat delayed", ok: true},
		{name: "init is not authoritative", state: hbdomain.StateInit, ok: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, gotError, ok := machineStatusFromHeartbeat(test.state, "heartbeat delayed")
			if ok != test.ok || got != test.want || gotError != test.wantError {
				t.Fatalf("machineStatusFromHeartbeat(%s) = (%s, %q, %v), want (%s, %q, %v)", test.state, got, gotError, ok, test.want, test.wantError, test.ok)
			}
		})
	}
}

type registeredMySQLStub struct {
	ports    map[int]bool
	statuses map[int]string
}

func (s *registeredMySQLStub) UpdateStatus(_ context.Context, _ string, port int, status string) error {
	if s.statuses == nil {
		s.statuses = make(map[int]string)
	}
	s.statuses[port] = status
	return nil
}

func (s *registeredMySQLStub) Get(_ context.Context, machineID string, port int) (mysqlapp.Instance, bool, error) {
	if machineID == "machine-1" && s.ports[port] {
		return mysqlapp.Instance{MachineID: machineID, Port: port}, true, nil
	}
	return mysqlapp.Instance{}, false, nil
}

func TestFilterUnregisteredMySQLMetrics(t *testing.T) {
	service := &HeartbeatService{mysql: &registeredMySQLStub{ports: map[int]bool{3307: true}}}
	payload := hbdomain.HeartbeatPayload{MachineID: "machine-1", Metrics: []dynamicdomain.MetricResult{
		{Name: "cpu_usage_percent", Success: true, Value: 10},
		{Name: "mysql_process_alive", Success: true, Value: false, Labels: map[string]string{"mysql_port": "3306"}},
		{Name: "mysql_process_alive", Success: true, Value: true, Labels: map[string]string{"mysql_port": "3307"}},
	}}

	filtered := service.filterUnregisteredMySQLMetrics(context.Background(), payload)
	if len(filtered.Metrics) != 2 || filtered.Metrics[0].Name != "cpu_usage_percent" || filtered.Metrics[1].Labels["mysql_port"] != "3307" {
		t.Fatalf("unexpected filtered metrics: %+v", filtered.Metrics)
	}
}

type alertMachineStub struct {
	machines map[string]machinedomain.Machine
}

func (s *alertMachineStub) UpdateStatus(context.Context, string, machinedomain.Status, string) error {
	return nil
}

func (s *alertMachineStub) GetByID(_ context.Context, machineID string) (machinedomain.Machine, bool, error) {
	machine, ok := s.machines[machineID]
	return machine, ok, nil
}

type alertTopologyStub struct {
	intent hadomain.TopologyIntent
}

func (s alertTopologyStub) GetTopologyIntent(_ context.Context, clusterID string) (hadomain.TopologyIntent, bool, error) {
	return s.intent, clusterID == s.intent.ClusterID, nil
}

func TestEnrichAlertTopologyAddsInstanceAndReplicationContext(t *testing.T) {
	machines := &alertMachineStub{machines: map[string]machinedomain.Machine{
		"primary": {ID: "primary", Name: "db-primary", IP: "10.8.0.10", Cluster: "prod"},
		"replica": {ID: "replica", Name: "db-replica", IP: "10.8.0.11", Cluster: "prod"},
	}}
	service := &HeartbeatService{machines: machines, alertTopology: alertTopologyStub{intent: hadomain.TopologyIntent{
		ClusterID: "prod", Architecture: hadomain.ArchitectureMasterSlave, PrimaryMachineID: "primary",
		Nodes: []hadomain.ArchitectureNodeRequest{
			{MachineID: "primary", Port: 3306, Role: "M"},
			{MachineID: "replica", Port: 3307, Role: "S", SourceMachineID: "primary"},
		},
	}}}
	payload := hbdomain.HeartbeatPayload{
		MachineID: "replica", MachineName: "db-replica", MachineIP: "10.8.0.11", ClusterID: "prod",
		Metrics: []dynamicdomain.MetricResult{
			{Name: "mysql_role", Value: "replica", Labels: map[string]string{"mysql_port": "3307"}},
			{Name: "mysql_replication_lag", Value: 42, Labels: map[string]string{"mysql_port": "3307"}},
		},
	}

	enriched := service.enrichAlertTopology(context.Background(), payload)
	labels := enriched.Metrics[1].Labels
	want := map[string]string{
		"instance_name": "db-replica:3307", "instance_endpoint": "10.8.0.11:3307",
		"cluster_name": "prod", "instance_role": "从库", "topology_architecture_label": "主从复制",
		"source_instance_name": "db-primary:3306", "source_instance_endpoint": "10.8.0.10:3306",
		"instance_relation": "从库，复制自 db-primary:3306", "observed_instance_role": "replica",
	}
	for key, value := range want {
		if labels[key] != value {
			t.Fatalf("label %s = %q, want %q; labels=%+v", key, labels[key], value, labels)
		}
	}
}

func TestAlertRuntimeReplicationSourceSupportsMySQLEightAndLegacyFields(t *testing.T) {
	for name, value := range map[string]any{
		"mysql8": map[string]any{"replica_status": map[string]any{"Source_Host": "10.8.0.10", "Source_Port": 3306}},
		"legacy": map[string]any{"replica_status": map[string]string{"Master_Host": "db-primary", "Master_Port": "3307"}},
	} {
		t.Run(name, func(t *testing.T) {
			if source := alertRuntimeReplicationSource(value); source == "" || !strings.Contains(source, ":33") {
				t.Fatalf("unexpected replication source %q", source)
			}
		})
	}
}

func TestSyncMySQLStateTreatsOfflineAgentChecksAsUnavailable(t *testing.T) {
	mysql := &registeredMySQLStub{}
	service := &HeartbeatService{mysql: mysql}
	service.syncMySQLState(context.Background(), hbdomain.LatestStatus{
		MachineID:    "machine-1",
		CurrentState: hbdomain.StateOffline,
		Checks: []hbdomain.HealthCheck{{
			Name: "mysql.heartbeat.3306", Status: hbdomain.CheckOK,
		}},
	})

	if got := mysql.statuses[3306]; got != mysqlapp.StatusHeartbeatFailed {
		t.Fatalf("offline Agent status = %q, want %q", got, mysqlapp.StatusHeartbeatFailed)
	}
}

func TestSyncMySQLStateKeepsFreshHealthyCheckRunning(t *testing.T) {
	mysql := &registeredMySQLStub{}
	service := &HeartbeatService{mysql: mysql}
	service.syncMySQLState(context.Background(), hbdomain.LatestStatus{
		MachineID:    "machine-1",
		CurrentState: hbdomain.StateOnline,
		Checks: []hbdomain.HealthCheck{{
			Name: "mysql.heartbeat.3306", Status: hbdomain.CheckOK,
		}},
	})

	if got := mysql.statuses[3306]; got != mysqlapp.StatusRunning {
		t.Fatalf("online Agent status = %q, want %q", got, mysqlapp.StatusRunning)
	}
}

func TestUpdateDynamicCollectConfigRejectsMySQLTasks(t *testing.T) {
	service := &HeartbeatService{}
	cfg := service.UpdateDynamicCollectConfig(dynamicdomain.DynamicCollectConfig{Tasks: []dynamicdomain.CollectTaskSpec{
		{Name: "cpu_usage_percent"},
		{Name: "mysql_process_alive"},
	}})
	if len(cfg.Tasks) != 1 || cfg.Tasks[0].Name != "cpu_usage_percent" {
		t.Fatalf("mysql collectors must not run in host collector: %+v", cfg.Tasks)
	}
}

func TestDashboardMetricSnapshotFiltersAndThrottles(t *testing.T) {
	service := &HeartbeatService{metricSnapshotAt: make(map[string]time.Time)}
	now := time.Now().UTC()
	items := service.dashboardMetricSnapshot("agent-1", []dynamicdomain.MetricResult{
		{Name: "cpu_usage_percent", Value: 20},
		{Name: "mysql_qps", Value: 100},
		{Name: "mysql_data_disk_usage", Value: map[string]any{"path": "/srv/mysql/data", "used_percent": 72}},
		{Name: "filesystem_usage", Value: []any{map[string]any{"mount": "/", "used_percent": 99}}},
		{Name: "mysql_threads_connected", Value: 8},
	}, now)
	if len(items) != 4 || items[0].Name != "cpu_usage_percent" || items[1].Name != "mysql_qps" || items[2].Name != "mysql_data_disk_usage" || items[3].Name != "mysql_threads_connected" {
		t.Fatalf("unexpected overview snapshot: %+v", items)
	}
	if second := service.dashboardMetricSnapshot("agent-1", items, now.Add(10*time.Second)); len(second) != 0 {
		t.Fatalf("snapshot must be throttled, got %+v", second)
	}
	if third := service.dashboardMetricSnapshot("agent-1", items, now.Add(15*time.Second)); len(third) != 4 {
		t.Fatalf("snapshot should resume after 15 seconds, got %+v", third)
	}
}

func TestHeartbeatHistoryCleanupRunsEveryTwoHours(t *testing.T) {
	service := &HeartbeatService{}
	if got := service.HistoryCleanupInterval(); got != 2*time.Hour {
		t.Fatalf("cleanup interval = %s, want 2h", got)
	}
}

func TestHeartbeatAbnormalityIncludesHealthChecksAndMetricFailures(t *testing.T) {
	healthy := hbdomain.LatestStatus{
		CurrentState:  hbdomain.StateOnline,
		OverallHealth: hbdomain.HealthHealthy,
		Checks:        []hbdomain.HealthCheck{{Name: "self", Status: hbdomain.CheckOK}},
		Metrics:       []dynamicdomain.MetricResult{{Name: "cpu", Success: true}},
	}
	if heartbeatIsAbnormal(healthy) {
		t.Fatal("healthy heartbeat was marked abnormal")
	}
	warned := healthy
	warned.Checks = []hbdomain.HealthCheck{{Name: "mysql", Status: hbdomain.CheckWarn}}
	if !heartbeatIsAbnormal(warned) {
		t.Fatal("warning health check must preserve the heartbeat")
	}
	failedMetric := healthy
	failedMetric.Metrics = []dynamicdomain.MetricResult{{Name: "cpu", Success: false}}
	if !heartbeatIsAbnormal(failedMetric) {
		t.Fatal("failed metric must preserve the heartbeat")
	}
}
