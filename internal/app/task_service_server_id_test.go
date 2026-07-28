package app

import (
	"context"
	"strings"
	"testing"

	machinedomain "gmha/internal/domain/machine"
	mysqlapp "gmha/internal/mysql"
)

type serverIDMachineRepo struct {
	items []machinedomain.Machine
}

func (r *serverIDMachineRepo) GetByID(_ context.Context, id string) (machinedomain.Machine, bool, error) {
	for _, item := range r.items {
		if item.ID == id {
			return item, true, nil
		}
	}
	return machinedomain.Machine{}, false, nil
}

func (r *serverIDMachineRepo) List(context.Context) ([]machinedomain.Machine, error) {
	return append([]machinedomain.Machine(nil), r.items...), nil
}

type serverIDInstanceRepo struct {
	items []mysqlapp.Instance
}

func (r *serverIDInstanceRepo) Save(_ context.Context, item mysqlapp.Instance) error {
	for index := range r.items {
		if r.items[index].MachineID == item.MachineID && r.items[index].Port == item.Port {
			r.items[index] = item
			return nil
		}
	}
	r.items = append(r.items, item)
	return nil
}

func (r *serverIDInstanceRepo) List(context.Context) ([]mysqlapp.Instance, error) {
	return append([]mysqlapp.Instance(nil), r.items...), nil
}

func (r *serverIDInstanceRepo) Delete(_ context.Context, machineID string, port int) error {
	return nil
}

func (r *serverIDInstanceRepo) UpdateStatus(_ context.Context, machineID string, port int, status string) error {
	return nil
}

func (r *serverIDInstanceRepo) Get(_ context.Context, machineID string, port int) (mysqlapp.Instance, bool, error) {
	for _, item := range r.items {
		if item.MachineID == machineID && item.Port == port {
			return item, true, nil
		}
	}
	return mysqlapp.Instance{}, false, nil
}

func TestValidateMySQLServerIDChangeRequiresClusterUniqueValue(t *testing.T) {
	machines := &serverIDMachineRepo{items: []machinedomain.Machine{
		{ID: "db-1", Name: "DB-01", IP: "10.0.0.1", Cluster: "production"},
		{ID: "db-2", Name: "DB-02", IP: "10.0.0.2", Cluster: "production"},
		{ID: "db-3", Name: "DB-03", IP: "10.0.1.3", Cluster: "reporting"},
	}}
	instances := &serverIDInstanceRepo{items: []mysqlapp.Instance{
		{MachineID: "db-1", Port: 3306, ServerID: 101},
		{MachineID: "db-2", Port: 3306, ServerID: 102},
		{MachineID: "db-3", Port: 3306, ServerID: 103},
	}}
	service := &TaskService{machines: machines, mysqlInstance: instances}

	if err := service.ValidateMySQLServerIDChange(context.Background(), "10.0.0.1", 3306, 102); err == nil || !strings.Contains(err.Error(), "DB-02:3306") {
		t.Fatalf("same-cluster duplicate should be rejected with its owner, got %v", err)
	}
	if err := service.ValidateMySQLServerIDChange(context.Background(), "10.0.0.1", 3306, 103); err != nil {
		t.Fatalf("a value used only in another cluster should be allowed: %v", err)
	}
	if err := service.ValidateMySQLServerIDChange(context.Background(), "10.0.0.1", 3306, 101); err == nil || !strings.Contains(err.Error(), "当前值相同") {
		t.Fatalf("an unchanged server_id should not create a restart task, got %v", err)
	}
	if err := service.ValidateMySQLServerIDChange(context.Background(), "10.0.0.1", 3306, 0); err == nil {
		t.Fatal("server_id zero should be rejected")
	}
}

func TestUpdateMySQLInstanceServerIDSynchronizesInventory(t *testing.T) {
	instances := &serverIDInstanceRepo{items: []mysqlapp.Instance{{MachineID: "db-1", Port: 3306, ServerID: 1}}}
	service := &TaskService{mysqlInstance: instances}
	if err := service.UpdateMySQLInstanceServerID(context.Background(), "db-1", 3306, 101); err != nil {
		t.Fatal(err)
	}
	got, ok, err := instances.Get(context.Background(), "db-1", 3306)
	if err != nil || !ok || got.ServerID != 101 || got.UpdatedAt.IsZero() {
		t.Fatalf("inventory was not synchronized: instance=%+v ok=%v err=%v", got, ok, err)
	}
}
