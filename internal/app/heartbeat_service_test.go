package app

import (
	"context"
	"testing"
	"time"

	dynamicdomain "gmha/internal/domain/dynamic"
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
	ports map[int]bool
}

func (s *registeredMySQLStub) UpdateStatus(context.Context, string, int, string) error { return nil }

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
