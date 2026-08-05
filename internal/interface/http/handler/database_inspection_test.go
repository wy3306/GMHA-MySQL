package handler

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"gmha/internal/app"
	taskdomain "gmha/internal/domain/task"
	persistence "gmha/internal/infrastructure/persistence/sqlite"
	_ "modernc.org/sqlite"
)

func TestDatabaseInspectionCommandIncludesExpectedChecks(t *testing.T) {
	for _, operation := range []string{"database_inspection", "database_deep_inspection"} {
		if err := validateClusterAutomationRequest(clusterAutomationRequest{Clusters: []string{"prod"}, Operation: operation, Port: 3306}); err != nil {
			t.Fatalf("%s request should be valid: %v", operation, err)
		}
	}
	if err := validateClusterAutomationRequest(clusterAutomationRequest{Clusters: []string{"prod"}, Operation: "database_inspection"}); err == nil {
		t.Fatal("inspection request without a valid port should fail")
	}
	standard := databaseInspectionCommand("mysql-client", false)
	if !strings.Contains(standard, "GMHA_INSPECTION_META") || !strings.Contains(standard, "connection_usage") {
		t.Fatalf("standard command is missing inspection markers: %s", standard)
	}
	if strings.Contains(standard, "tables_without_pk") {
		t.Fatal("standard command unexpectedly includes deep table checks")
	}
	deep := databaseInspectionCommand("mysql-client", true)
	for _, marker := range []string{"tables_without_pk", "long_transactions", "buffer_pool_hit", "GMHA_INSPECTION_END"} {
		if !strings.Contains(deep, marker) {
			t.Fatalf("deep command is missing %q", marker)
		}
	}
}

func TestParseDatabaseInspectionEvents(t *testing.T) {
	target := databaseInspectionTarget{Cluster: "prod", Machine: "db-1", IP: "10.0.0.10", Port: 3306, Level: "deep"}
	events := []taskdomain.Event{{Content: strings.Join([]string{
		"GMHA_INSPECTION_META\tdb-1\t8.0.36\t3306\t2026-07-23 10:00:00",
		"GMHA_INSPECTION_CHECK\t连接\tconnection_usage\t连接使用率\tcritical\twarning\t78%\t< 70%\t连接偏高\t检查连接池",
		"GMHA_INSPECTION_CHECK\t容量\tdatabase_size\t业务数据容量\tinfo\tinfo\t12 GB\t信息项\t容量统计\t规划容量",
	}, "\n")}}
	checks, hostname, version := parseDatabaseInspectionEvents("task-1", target, events)
	if hostname != "db-1" || version != "8.0.36" {
		t.Fatalf("unexpected metadata: hostname=%q version=%q", hostname, version)
	}
	if len(checks) != 2 {
		t.Fatalf("expected 2 checks, got %d", len(checks))
	}
	if checks[0].Status != "warning" || checks[0].Recommendation != "检查连接池" || checks[0].Cluster != "prod" {
		t.Fatalf("unexpected parsed check: %+v", checks[0])
	}
	if score := inspectionScore(2, 1); score != 64 {
		t.Fatalf("unexpected inspection score: %d", score)
	}
}

func TestInspectionOfficeExportsAreValidPackages(t *testing.T) {
	now := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC)
	result := databaseInspectionResult{
		Ready: true, Exported: now,
		Targets: []databaseInspectionTarget{{TaskID: "task-1", Cluster: "prod", Machine: "db-1", IP: "10.0.0.10", Port: 3306, Version: "8.0.36", Level: "deep", Status: "success", Score: 92, Passed: 5, Warnings: 1}},
		Checks:  []databaseInspectionCheck{{TaskID: "task-1", Cluster: "prod", Machine: "db-1", IP: "10.0.0.10", Port: 3306, Level: "deep", Category: "连接", Code: "connection_usage", Title: "连接使用率", Severity: "critical", Status: "warning", Value: "78%", Threshold: "< 70%", Description: "连接偏高", Recommendation: "检查连接池"}},
	}
	docx, err := buildInspectionDOCX(result)
	if err != nil {
		t.Fatal(err)
	}
	assertZipEntries(t, docx, map[string]string{
		"word/document.xml": "GMHA 数据库巡检报告",
		"word/styles.xml":   "Heading1",
		"_rels/.rels":       "officeDocument",
	})
	xlsx, err := buildInspectionXLSX(result)
	if err != nil {
		t.Fatal(err)
	}
	assertZipEntries(t, xlsx, map[string]string{
		"xl/workbook.xml":          "巡检明细",
		"xl/worksheets/sheet1.xml": "connection_usage",
		"xl/styles.xml":            "Microsoft YaHei",
	})
}

func TestDatabaseInspectionHistoryUsesPersistedTaskTree(t *testing.T) {
	db, err := sql.Open("sqlite", t.TempDir()+"/inspection-history.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := persistence.NewTaskRepository(persistence.NewDB(db, persistence.DialectSQLite))
	if err := repo.Migrate(); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 5, 9, 30, 0, 0, time.UTC)
	finished := now.Add(time.Minute)
	parent := taskdomain.Task{
		ID: "inspection-run-1", Type: taskdomain.TypeBatchOperation, AgentID: "manager",
		Status: taskdomain.StatusSuccess, ProgressPercent: 100, CreatedAt: now, FinishedAt: &finished,
		SpecJSON: []byte(`{"operation":"cluster_automation","target":"prod"}`),
	}
	child := taskdomain.Task{
		ID: "inspection-task-1", ParentTaskID: parent.ID, Type: taskdomain.TypeExec, MachineID: "db-1",
		Status: taskdomain.StatusSuccess, ProgressPercent: 100, CreatedAt: now, FinishedAt: &finished,
		SpecJSON: []byte(`{"operation":"database_inspection","port":3306}`),
	}
	if err := repo.CreateTask(context.Background(), parent, nil, nil); err != nil {
		t.Fatal(err)
	}
	events := []taskdomain.Event{{ID: "inspection-event-1", TaskID: child.ID, EventType: taskdomain.EventLog, CreatedAt: finished, Content: strings.Join([]string{
		"GMHA_INSPECTION_META\tdb-1\t8.0.36\t3306\t2026-08-05 09:31:00",
		"GMHA_INSPECTION_CHECK\t连接\tconnection_usage\t连接使用率\twarning\twarning\t75%\t< 70%\t连接偏高\t检查连接池",
	}, "\n")}}
	if err := repo.CreateTask(context.Background(), child, nil, events); err != nil {
		t.Fatal(err)
	}

	service := app.NewTaskService(repo, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/database-inspection/history?cluster=prod", nil)
	NewTaskHandler(service).HandleDatabaseInspectionHistory(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response databaseInspectionHistoryResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Total != 1 || len(response.Items) != 1 {
		t.Fatalf("unexpected history response: %+v", response)
	}
	record := response.Items[0]
	if record.ID != parent.ID || len(record.TaskIDs) != 1 || record.TaskIDs[0] != child.ID || record.AverageScore != 92 || record.Warnings != 1 {
		t.Fatalf("unexpected history record: %+v", record)
	}
	if len(record.Clusters) != 1 || record.Clusters[0] != "prod" {
		t.Fatalf("inspection cluster was not retained: %+v", record.Clusters)
	}
}

func assertZipEntries(t *testing.T, contents []byte, expected map[string]string) {
	t.Helper()
	reader, err := zip.NewReader(bytes.NewReader(contents), int64(len(contents)))
	if err != nil {
		t.Fatalf("invalid Office zip package: %v", err)
	}
	files := make(map[string]*zip.File, len(reader.File))
	for _, file := range reader.File {
		files[file.Name] = file
	}
	for name, needle := range expected {
		file := files[name]
		if file == nil {
			t.Fatalf("Office package is missing %s", name)
		}
		stream, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(stream)
		_ = stream.Close()
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), needle) {
			t.Fatalf("%s does not contain %q", name, needle)
		}
	}
}
