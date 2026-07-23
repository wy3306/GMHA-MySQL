package handler

import (
	"archive/zip"
	"bytes"
	"io"
	"strings"
	"testing"
	"time"

	taskdomain "gmha/internal/domain/task"
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
