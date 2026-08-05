package app

import (
	"context"
	"testing"
	"time"

	hbdomain "gmha/internal/domain/heartbeat"
	mysqlapp "gmha/internal/mysql"
)

type instanceViewRepository struct {
	items []mysqlapp.Instance
}

func (r *instanceViewRepository) List(context.Context) ([]mysqlapp.Instance, error) {
	return append([]mysqlapp.Instance(nil), r.items...), nil
}
func (r *instanceViewRepository) Get(_ context.Context, machineID string, port int) (mysqlapp.Instance, bool, error) {
	for _, item := range r.items {
		if item.MachineID == machineID && item.Port == port {
			return item, true, nil
		}
	}
	return mysqlapp.Instance{}, false, nil
}
func (r *instanceViewRepository) Delete(context.Context, string, int) error { return nil }
func (r *instanceViewRepository) UpdateStatus(context.Context, string, int, string) error {
	return nil
}
func (r *instanceViewRepository) PruneUninstalled(context.Context) (int64, error) { return 0, nil }

type instanceViewHeartbeatRepository struct {
	items []hbdomain.LatestStatus
}

func (r *instanceViewHeartbeatRepository) UpsertLatestStatus(context.Context, hbdomain.LatestStatus) error {
	return nil
}
func (r *instanceViewHeartbeatRepository) AppendEvent(context.Context, hbdomain.StateEvent) error {
	return nil
}
func (r *instanceViewHeartbeatRepository) ListLatest(context.Context) ([]hbdomain.LatestStatus, error) {
	return append([]hbdomain.LatestStatus(nil), r.items...), nil
}
func (r *instanceViewHeartbeatRepository) DeleteLatestByMachineID(context.Context, string) error {
	return nil
}

func TestListInstanceViewsDoesNotExposeStaleHealthyStateForOfflineMachine(t *testing.T) {
	checkedAt := time.Now().UTC().Add(-time.Minute)
	instances := &instanceViewRepository{items: []mysqlapp.Instance{{
		MachineID: "machine-1", Port: 3306, Status: mysqlapp.StatusRunning,
	}}}
	heartbeats := &instanceViewHeartbeatRepository{items: []hbdomain.LatestStatus{{
		AgentID: "agent-1", MachineID: "machine-1", CurrentState: hbdomain.StateOffline,
		LastHeartbeatAt: checkedAt, LastErrorSummary: "heartbeat timeout after 30s",
		Checks: []hbdomain.HealthCheck{{Name: "mysql.heartbeat.3306", Status: hbdomain.CheckOK, CheckedAt: checkedAt}},
	}}}
	heartbeat := NewHeartbeatService(heartbeats, HeartbeatConfig{}, nil, nil, nil)
	service := NewMySQLService(instances, nil, heartbeat, nil)

	views, err := service.ListInstanceViews(context.Background())
	if err != nil {
		t.Fatalf("ListInstanceViews: %v", err)
	}
	if len(views) != 1 {
		t.Fatalf("views = %d, want 1", len(views))
	}
	view := views[0]
	if view.AgentState != string(hbdomain.StateOffline) || view.Status != mysqlapp.StatusHeartbeatFailed || view.HeartbeatStatus != string(hbdomain.CheckFail) {
		t.Fatalf("offline instance exposed as healthy: %+v", view)
	}
	if view.HeartbeatDetail != "heartbeat timeout after 30s" {
		t.Fatalf("heartbeat detail = %q", view.HeartbeatDetail)
	}
}
