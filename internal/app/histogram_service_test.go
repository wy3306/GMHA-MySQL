package app

import (
	"context"
	"errors"
	"testing"

	machinedomain "gmha/internal/domain/machine"
	sqldomain "gmha/internal/domain/sqldiagnostic"
	taskdomain "gmha/internal/domain/task"
	mysqlapp "gmha/internal/mysql"
)

type histogramInstanceRepo struct {
	MySQLInstanceRepository
	item mysqlapp.Instance
}

func (r *histogramInstanceRepo) Get(_ context.Context, machineID string, port int) (mysqlapp.Instance, bool, error) {
	return r.item, r.item.MachineID == machineID && r.item.Port == port, nil
}

type histogramMachineRepo struct {
	machinedomain.Repository
	item machinedomain.Machine
}

func (r *histogramMachineRepo) GetByID(_ context.Context, id string) (machinedomain.Machine, bool, error) {
	return r.item, r.item.ID == id, nil
}

type histogramPresetRepo struct {
	MySQLAccountPresetRepository
}

func (histogramPresetRepo) List(context.Context) ([]taskdomain.MySQLAccountSpec, error) {
	return []taskdomain.MySQLAccountSpec{{
		Role: mysqlapp.AccountRoleMHA, Username: "mha", Password: "secret", Enabled: true,
	}}, nil
}

type fakeHistogramManager struct {
	inspectCalls int
	catalog      mysqlapp.HistogramCatalog
	updated      bool
	dropped      bool
}

func (m *fakeHistogramManager) Inspect(_ context.Context, instance sqldomain.Instance, _ mysqlapp.DiagnosticCredential, _, _ string) (mysqlapp.HistogramCatalog, error) {
	m.inspectCalls++
	m.catalog.Instance = instance
	return m.catalog, nil
}
func (m *fakeHistogramManager) Update(_ context.Context, _ sqldomain.Instance, _ mysqlapp.DiagnosticCredential, _, _ string, _ []string, _ int) (mysqlapp.HistogramOperationResult, error) {
	m.updated = true
	return mysqlapp.HistogramOperationResult{Action: "update"}, nil
}
func (m *fakeHistogramManager) Drop(_ context.Context, _ sqldomain.Instance, _ mysqlapp.DiagnosticCredential, _, _ string, _ []string) (mysqlapp.HistogramOperationResult, error) {
	m.dropped = true
	return mysqlapp.HistogramOperationResult{Action: "drop"}, nil
}

func newHistogramTestService(version string, manager mysqlapp.HistogramManager) *HistogramService {
	instance := mysqlapp.Instance{MachineID: "machine-1", Port: 3306, Version: version}
	return &HistogramService{
		instances: &histogramInstanceRepo{item: instance},
		machines: &histogramMachineRepo{item: machinedomain.Machine{
			ID: "machine-1", Name: "db-1", IP: "10.0.0.1", Cluster: "orders",
		}},
		presets: histogramPresetRepo{},
		manager: manager,
	}
}

func TestHistogramServiceRejectsMySQL57BeforeConnecting(t *testing.T) {
	manager := &fakeHistogramManager{}
	service := newHistogramTestService("5.7.44", manager)
	_, err := service.Inspect(context.Background(), HistogramInspectRequest{MachineID: "machine-1", Port: 3306})
	if !errors.Is(err, ErrHistogramUnsupported) || manager.inspectCalls != 0 {
		t.Fatalf("MySQL 5.7 should be rejected before connecting: calls=%d err=%v", manager.inspectCalls, err)
	}
}

func TestHistogramServiceValidatesColumnEligibility(t *testing.T) {
	manager := &fakeHistogramManager{catalog: mysqlapp.HistogramCatalog{
		Supported: true,
		Columns: []mysqlapp.HistogramColumn{
			{Name: "region", DataType: "varchar", Eligible: true},
			{Name: "payload", DataType: "json", IneligibleReason: "JSON 列不支持直方图"},
		},
	}}
	service := newHistogramTestService("8.0.40", manager)
	_, err := service.Update(context.Background(), HistogramManageRequest{
		MachineID: "machine-1", Port: 3306, Schema: "orders", Table: "sales",
		Columns: []string{"payload"}, Buckets: 100,
	})
	if err == nil || manager.updated {
		t.Fatalf("ineligible column should not be updated: updated=%v err=%v", manager.updated, err)
	}
	result, err := service.Update(context.Background(), HistogramManageRequest{
		MachineID: "machine-1", Port: 3306, Schema: "orders", Table: "sales",
		Columns: []string{"region"}, Buckets: 64,
	})
	if err != nil || !manager.updated || result.Action != "update" {
		t.Fatalf("eligible column update failed: result=%+v err=%v", result, err)
	}
}

func TestHistogramServiceOnlyDropsExistingHistograms(t *testing.T) {
	manager := &fakeHistogramManager{catalog: mysqlapp.HistogramCatalog{
		Supported: true,
		Columns:   []mysqlapp.HistogramColumn{{Name: "region", Eligible: true}},
	}}
	service := newHistogramTestService("8.4.10", manager)
	_, err := service.Drop(context.Background(), HistogramManageRequest{
		MachineID: "machine-1", Port: 3306, Schema: "orders", Table: "sales",
		Columns: []string{"region"},
	})
	if err == nil || manager.dropped {
		t.Fatalf("missing histogram should not be dropped: dropped=%v err=%v", manager.dropped, err)
	}
	manager.catalog.Histograms = []mysqlapp.Histogram{{Schema: "orders", Table: "sales", Column: "region"}}
	_, err = service.Drop(context.Background(), HistogramManageRequest{
		MachineID: "machine-1", Port: 3306, Schema: "orders", Table: "sales",
		Columns: []string{"region"},
	})
	if err != nil || !manager.dropped {
		t.Fatalf("existing histogram drop failed: dropped=%v err=%v", manager.dropped, err)
	}
}
