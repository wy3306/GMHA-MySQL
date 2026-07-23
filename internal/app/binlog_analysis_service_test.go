package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"gmha/internal/binloganalyzer"
	machinedomain "gmha/internal/domain/machine"
	mysqlapp "gmha/internal/mysql"
)

func newBinlogAnalysisTestService(t *testing.T) *BinlogAnalysisService {
	t.Helper()
	service := NewBinlogAnalysisService(
		&histogramInstanceRepo{item: mysqlapp.Instance{MachineID: "machine-1", Port: 3306, Version: "8.0.40"}},
		&histogramMachineRepo{item: machinedomain.Machine{ID: "machine-1", Name: "db-1", IP: "10.0.0.1", Cluster: "orders"}},
		histogramPresetRepo{},
	)
	t.Cleanup(service.Close)
	return service
}

func TestBinlogAnalysisUsesManagedCredentialWithoutExposingIt(t *testing.T) {
	service := newBinlogAnalysisTestService(t)
	configs := make(chan binloganalyzer.Config, 1)
	service.analyze = func(_ context.Context, cfg binloganalyzer.Config, emit func(binloganalyzer.Progress)) (*binloganalyzer.Result, error) {
		configs <- cfg
		emit(binloganalyzer.Progress{Phase: "running", FilesTotal: 1, FilesCompleted: 1})
		return &binloganalyzer.Result{Summary: binloganalyzer.Summary{TotalRows: 42, FilesAnalyzed: 1}}, nil
	}
	task, err := service.Create(context.Background(), BinlogAnalysisRequest{
		MachineID: "machine-1", Port: 3306,
		StartTime: time.Now().Add(-time.Hour), EndTime: time.Now(),
		BigTxnMode: "rows", BigTxnRowsThreshold: 1000,
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg := <-configs
	if cfg.User != "mha" || cfg.Password != "secret" || cfg.Host != "10.0.0.1" {
		t.Fatalf("unexpected analysis config: user=%q password=%q host=%q", cfg.User, cfg.Password, cfg.Host)
	}
	var completed BinlogAnalysisTask
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); {
		completed, _ = service.Get(task.ID)
		if completed.Status == BinlogAnalysisCompleted {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if completed.Status != BinlogAnalysisCompleted || completed.Summary == nil || completed.Summary.TotalRows != 42 {
		t.Fatalf("task did not complete with result: %+v", completed)
	}
	payload, _ := json.Marshal(completed)
	if strings.Contains(string(payload), "secret") {
		t.Fatal("managed database password leaked into task JSON")
	}
}

func TestBinlogAnalysisCancelStopsRunningTask(t *testing.T) {
	service := newBinlogAnalysisTestService(t)
	started := make(chan struct{})
	service.analyze = func(ctx context.Context, _ binloganalyzer.Config, _ func(binloganalyzer.Progress)) (*binloganalyzer.Result, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	task, err := service.Create(context.Background(), BinlogAnalysisRequest{
		MachineID: "machine-1", Port: 3306,
		StartTime: time.Now().Add(-time.Hour), EndTime: time.Now(),
		BigTxnMode: "rows", BigTxnRowsThreshold: 1000,
	})
	if err != nil {
		t.Fatal(err)
	}
	<-started
	canceled, err := service.Cancel(task.ID)
	if err != nil || canceled.Status != BinlogAnalysisCanceled {
		t.Fatalf("cancel failed: task=%+v err=%v", canceled, err)
	}
}
