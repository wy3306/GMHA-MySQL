package app

import (
	"encoding/json"
	"strings"
	"testing"

	taskdomain "gmha/internal/domain/task"
)

func TestTaskForDisplayKeepsMetadataAndHidesCommand(t *testing.T) {
	spec, _ := json.Marshal(taskdomain.ExecSpec{
		Command: "MYSQL_PWD=secret mysql -e 'select 1'", Operation: "mysql_collect",
		DisplayName: "采集 MySQL 运行数据", Port: 3306,
	})
	item := taskForDisplay(taskdomain.Task{Type: taskdomain.TypeExec, SpecJSON: spec})
	if strings.Contains(string(item.SpecJSON), "secret") || strings.Contains(string(item.SpecJSON), "MYSQL_PWD") {
		t.Fatalf("display spec leaked command: %s", item.SpecJSON)
	}
	var display taskdomain.ExecSpec
	if err := json.Unmarshal(item.SpecJSON, &display); err != nil {
		t.Fatalf("unmarshal display spec: %v", err)
	}
	if display.Operation != "mysql_collect" || display.DisplayName == "" || display.Port != 3306 {
		t.Fatalf("display metadata was lost: %+v", display)
	}
}

func TestTaskForDisplayCompactsMySQLInstallAndHidesSecrets(t *testing.T) {
	spec, _ := json.Marshal(taskdomain.MySQLInstallSpec{
		Port: 3306, ServerID: 2, MySQLUser: "mysql", RootPassword: "root-secret",
		Profile: "default", PackageName: "mysql.tar.xz", MyCnfContent: "large-config-body",
		Accounts: []taskdomain.MySQLAccountSpec{{Username: "monitor", Password: "account-secret"}},
	})
	item := taskForDisplay(taskdomain.Task{Type: taskdomain.TypeMySQLInstall, SpecJSON: spec})
	display := string(item.SpecJSON)
	for _, forbidden := range []string{"root-secret", "account-secret", "large-config-body", "root_password", "accounts", "my_cnf_content"} {
		if strings.Contains(display, forbidden) {
			t.Fatalf("display spec leaked %q: %s", forbidden, display)
		}
	}
	if !strings.Contains(display, `"port":3306`) || !strings.Contains(display, `"package_name":"mysql.tar.xz"`) {
		t.Fatalf("display metadata was lost: %s", display)
	}
}

func TestTaskForDisplayHidesTopologyPasswords(t *testing.T) {
	spec, _ := json.Marshal(taskdomain.MySQLTopologySpec{
		Topology: "primary_replica", Port: 3306, RootPassword: "root-secret",
		ReplicationUser: "repl", ReplicationPassword: "repl-secret",
	})
	item := taskForDisplay(taskdomain.Task{Type: taskdomain.TypeMySQLTopology, SpecJSON: spec})
	display := string(item.SpecJSON)
	if strings.Contains(display, "secret") || strings.Contains(display, "password") {
		t.Fatalf("display spec leaked topology credentials: %s", display)
	}
}

func TestTaskForDisplayKeepsPlatformOperationLinks(t *testing.T) {
	spec, _ := json.Marshal(taskdomain.PlatformOperationSpec{
		Operation: "cluster-mysql-install", DisplayName: "批量部署 MySQL", Method: "POST", Path: "/api/v1/tasks/cluster-mysql-install",
		HTTPStatus: 200, RelatedTaskIDs: []string{"task-one", "task-two"},
	})
	item := taskForDisplay(taskdomain.Task{Type: taskdomain.TypePlatformOperation, SpecJSON: spec})
	var display taskdomain.PlatformOperationSpec
	if err := json.Unmarshal(item.SpecJSON, &display); err != nil {
		t.Fatal(err)
	}
	if display.DisplayName != "批量部署 MySQL" || len(display.RelatedTaskIDs) != 2 {
		t.Fatalf("platform operation metadata was lost: %+v", display)
	}
}
