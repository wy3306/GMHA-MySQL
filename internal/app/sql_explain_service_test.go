package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	machinedomain "gmha/internal/domain/machine"
	sqldomain "gmha/internal/domain/sqldiagnostic"
	taskdomain "gmha/internal/domain/task"
	mysqlapp "gmha/internal/mysql"
)

type explainPresetRepo struct{ MySQLAccountPresetRepository }

func (explainPresetRepo) List(context.Context) ([]taskdomain.MySQLAccountSpec, error) {
	return []taskdomain.MySQLAccountSpec{
		{Role: mysqlapp.AccountRoleMonitor, Username: "monitor", Password: "monitor-secret", Enabled: true},
		{Role: mysqlapp.AccountRoleMHA, Username: "mha", Password: "mha-secret", Enabled: true},
	}, nil
}

type fakeExecutionPlanExplainer struct {
	calls      int
	instance   sqldomain.Instance
	credential mysqlapp.DiagnosticCredential
	database   string
	statement  string
}

func (f *fakeExecutionPlanExplainer) Explain(_ context.Context, instance sqldomain.Instance, credential mysqlapp.DiagnosticCredential, database, statement string) (mysqlapp.ExecutionPlan, error) {
	f.calls++
	f.instance, f.credential, f.database, f.statement = instance, credential, database, statement
	return mysqlapp.ExecutionPlan{
		Instance: instance, Database: database, SQL: statement,
		Columns: []string{"id", "type", "key", "rows"},
		Rows:    []map[string]any{{"id": int64(1), "type": "ref", "key": "idx_customer", "rows": int64(12)}},
	}, nil
}

func newExplainTestService(explainer mysqlapp.ExecutionPlanExplainer) *SQLDiagnosticService {
	return &SQLDiagnosticService{
		instances: &diagnosticInstanceRepo{items: []mysqlapp.Instance{{MachineID: "machine-1", Port: 3306, Version: "8.0.40"}}},
		machines: &diagnosticMachineRepo{items: map[string]machinedomain.Machine{
			"machine-1": {ID: "machine-1", Name: "db-1", IP: "10.0.0.1", Cluster: "orders"},
		}},
		presets:   explainPresetRepo{},
		explainer: explainer,
	}
}

func TestSQLDiagnosticExplainUsesSelectedInstanceAndManagedCredential(t *testing.T) {
	explainer := &fakeExecutionPlanExplainer{}
	service := newExplainTestService(explainer)
	result, err := service.Explain(context.Background(), SQLExplainRequest{
		MachineID: "machine-1", Port: 3306, Database: "orders", SQL: "SELECT * FROM orders WHERE customer_id = 7;",
	})
	if err != nil {
		t.Fatal(err)
	}
	if explainer.calls != 1 || explainer.instance.MachineIP != "10.0.0.1" || explainer.instance.Port != 3306 {
		t.Fatalf("wrong execution target: calls=%d instance=%+v", explainer.calls, explainer.instance)
	}
	if explainer.credential.Username != "mha" || explainer.database != "orders" || strings.HasSuffix(explainer.statement, ";") {
		t.Fatalf("wrong explain inputs: credential=%+v database=%q sql=%q", explainer.credential, explainer.database, explainer.statement)
	}
	if len(result.Rows) != 1 || result.Rows[0]["key"] != "idx_customer" {
		t.Fatalf("unexpected plan: %+v", result)
	}
}

func TestSQLDiagnosticExplainRejectsUnknownInstanceAndExplainAnalyze(t *testing.T) {
	explainer := &fakeExecutionPlanExplainer{}
	service := newExplainTestService(explainer)
	_, err := service.Explain(context.Background(), SQLExplainRequest{
		MachineID: "missing", Port: 3306, SQL: "SELECT 1",
	})
	if !errors.Is(err, ErrSQLExplainInvalid) || explainer.calls != 0 {
		t.Fatalf("unknown instance should fail before explain: calls=%d err=%v", explainer.calls, err)
	}
	_, err = service.Explain(context.Background(), SQLExplainRequest{
		MachineID: "machine-1", Port: 3306, SQL: "EXPLAIN ANALYZE SELECT * FROM orders",
	})
	if !errors.Is(err, ErrSQLExplainInvalid) || explainer.calls != 0 {
		t.Fatalf("EXPLAIN ANALYZE should be rejected: calls=%d err=%v", explainer.calls, err)
	}
}
