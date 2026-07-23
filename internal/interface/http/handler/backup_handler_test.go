package handler

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"gmha/internal/app"
	backupdomain "gmha/internal/domain/backup"
	machinedomain "gmha/internal/domain/machine"
	persistencesqlite "gmha/internal/infrastructure/persistence/sqlite"
	mysqlapp "gmha/internal/mysql"

	_ "modernc.org/sqlite"
)

func TestBackupHandlerResourceAndTargetAPIs(t *testing.T) {
	sqlDB, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "backup-api.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	sqlDB.SetMaxOpenConns(1)
	database := persistencesqlite.NewDB(sqlDB, persistencesqlite.DialectSQLite)
	machines := persistencesqlite.NewMachineRepository(database)
	instances := persistencesqlite.NewMySQLInstanceRepository(database)
	backups := persistencesqlite.NewBackupRepository(database)
	for name, migrate := range map[string]func() error{
		"machines":  machines.Migrate,
		"instances": instances.Migrate,
		"backups":   backups.Migrate,
	} {
		if err := migrate(); err != nil {
			t.Fatalf("migrate %s: %v", name, err)
		}
	}
	ctx := context.Background()
	if _, err := machines.Save(ctx, machinedomain.Machine{
		ID: "machine-01", Name: "db-replica", IP: "10.0.0.12", SSHPort: 22,
		SSHUser: "root", Cluster: "prod", Status: machinedomain.StatusAgentOnline,
	}); err != nil {
		t.Fatal(err)
	}
	if err := instances.Save(ctx, mysqlapp.Instance{
		MachineID: "machine-01", Port: 3306, Status: mysqlapp.StatusRunning,
		Version: "8.4.10", Architecture: "x86_64", PackageName: "mysql-8.4.10",
	}); err != nil {
		t.Fatal(err)
	}
	service := app.NewBackupService(backups, nil, machines, instances)
	handler := NewBackupHandler(service)

	targetResponse := httptest.NewRecorder()
	handler.HandleTargets(targetResponse, httptest.NewRequest(http.MethodGet, "/api/v1/backup/targets?cluster=prod", nil))
	if targetResponse.Code != http.StatusOK {
		t.Fatalf("targets status=%d body=%s", targetResponse.Code, targetResponse.Body.String())
	}
	var targets []app.BackupTarget
	if err := json.Unmarshal(targetResponse.Body.Bytes(), &targets); err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || !targets[0].BackupReady || targets[0].MachineID != "machine-01" {
		t.Fatalf("targets=%+v", targets)
	}

	policyBody := map[string]any{
		"name": "prod-daily", "cluster": "prod", "machine_id": "machine-01", "port": 3306,
		"backup_type": "full", "disk_usage_threshold": 95, "schedule_type": "once",
		"start_at": time.Now().Add(time.Hour).UTC(), "retry_count": 2, "retry_interval_seconds": 60,
		"include_binlog": true, "backup_location": "/backup/mysql", "mysql_user": "backup",
		"mysql_password": "secret", "enabled": true,
	}
	createResponse := callBackupHandler(t, http.MethodPost, "/api/v1/backup/policies", policyBody, handler.HandlePolicies)
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", createResponse.Code, createResponse.Body.String())
	}
	var policy backupdomain.Policy
	if err := json.Unmarshal(createResponse.Body.Bytes(), &policy); err != nil {
		t.Fatal(err)
	}
	if policy.ID == "" {
		t.Fatal("created policy has no id")
	}

	getResponse := callBackupHandler(t, http.MethodGet, "/api/v1/backup/policies/"+policy.ID, nil, handler.HandlePolicyByID)
	if getResponse.Code != http.StatusOK {
		t.Fatalf("get status=%d body=%s", getResponse.Code, getResponse.Body.String())
	}
	if bytes.Contains(getResponse.Body.Bytes(), []byte("secret")) || bytes.Contains(getResponse.Body.Bytes(), []byte("mysql_password")) {
		t.Fatalf("policy GET exposed password: %s", getResponse.Body.String())
	}

	policyBody["name"] = "prod-nightly"
	policyBody["mysql_password"] = ""
	updateResponse := callBackupHandler(t, http.MethodPut, "/api/v1/backup/policies/"+policy.ID, policyBody, handler.HandlePolicyByID)
	if updateResponse.Code != http.StatusOK {
		t.Fatalf("update status=%d body=%s", updateResponse.Code, updateResponse.Body.String())
	}
	var updated backupdomain.Policy
	if err := json.Unmarshal(updateResponse.Body.Bytes(), &updated); err != nil {
		t.Fatal(err)
	}
	if updated.Name != "prod-nightly" {
		t.Fatalf("updated policy=%+v", updated)
	}
	stored, ok, err := backups.GetPolicy(ctx, policy.ID)
	if err != nil || !ok || stored.MySQLPassword != "secret" {
		t.Fatalf("blank update password was not preserved: ok=%v err=%v policy=%+v", ok, err, stored)
	}

	if err := backups.SaveRun(ctx, backupdomain.Run{
		ID: "run-01", PolicyID: policy.ID, Cluster: "prod", MachineID: "machine-01",
		Port: 3306, BackupType: backupdomain.TypeFull, BackupPath: "/backup/mysql/run-01",
		Status: backupdomain.RunPending, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	runResponse := callBackupHandler(t, http.MethodGet, "/api/v1/backup/runs/run-01", nil, handler.HandleRunByID)
	if runResponse.Code != http.StatusOK {
		t.Fatalf("run status=%d body=%s", runResponse.Code, runResponse.Body.String())
	}
	var run backupdomain.Run
	if err := json.Unmarshal(runResponse.Body.Bytes(), &run); err != nil {
		t.Fatal(err)
	}
	if run.MachineName != "db-replica" || run.MachineIP != "10.0.0.12" {
		t.Fatalf("run target not enriched: %+v", run)
	}
	restoreResponse := callBackupHandler(t, http.MethodPost, "/api/v1/backup/runs/run-01/restore", map[string]any{
		"confirmation": "RESTORE run-01",
		"mode":         "point_in_time",
		"restore_time": time.Now().Add(-time.Hour).UTC(),
	}, handler.HandleRunByID)
	if restoreResponse.Code != http.StatusBadRequest || !bytes.Contains(restoreResponse.Body.Bytes(), []byte("Binlog")) {
		t.Fatalf("point-in-time guard status=%d body=%s", restoreResponse.Code, restoreResponse.Body.String())
	}

	missingResponse := callBackupHandler(t, http.MethodGet, "/api/v1/backup/runs/missing", nil, handler.HandleRunByID)
	if missingResponse.Code != http.StatusNotFound {
		t.Fatalf("missing run status=%d body=%s", missingResponse.Code, missingResponse.Body.String())
	}
}

func callBackupHandler(t *testing.T, method, path string, body any, handle http.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	var requestBody *bytes.Reader
	if body == nil {
		requestBody = bytes.NewReader(nil)
	} else {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		requestBody = bytes.NewReader(data)
	}
	request := httptest.NewRequest(method, path, requestBody)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handle(response, request)
	return response
}
