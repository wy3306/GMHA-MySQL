package task

import (
	"testing"

	taskdomain "gmha/internal/domain/task"
	mysqlapp "gmha/internal/mysql"
)

func TestNormalizeInstallAccountsCompletesMHAPrivileges(t *testing.T) {
	accounts := normalizeInstallAccounts([]taskdomain.MySQLAccountSpec{{
		Role: "mha", Username: "mha", Password: "secret", Host: "%", Enabled: true,
		Privileges: []string{"SELECT"},
	}})
	var privileges []string
	for _, account := range accounts {
		if account.Role == "mha" {
			privileges = account.Privileges
			break
		}
	}
	granted := make(map[string]bool, len(privileges))
	for _, privilege := range privileges {
		granted[privilege] = true
	}
	for _, required := range mysqlapp.DefaultPrivileges(mysqlapp.AccountRoleMHA) {
		if !granted[required] {
			t.Fatalf("install task MHA privileges missing %q: %#v", required, privileges)
		}
	}
}

func TestBuildMySQLInstallStepsOptionalPTTools(t *testing.T) {
	withoutPT := buildMySQLInstallSteps("task-without-pt", false, false, "system")
	withPT := buildMySQLInstallSteps("task-with-pt", true, false, "system")
	if len(withPT) != len(withoutPT)+1 {
		t.Fatalf("expected one optional PT step, got without=%d with=%d", len(withoutPT), len(withPT))
	}
	for _, step := range withoutPT {
		if step.StepName == "install_pt_tools" {
			t.Fatal("PT step must not be present unless explicitly enabled")
		}
	}
	for index, step := range withPT {
		if step.StepName != "install_pt_tools" {
			continue
		}
		if index == 0 || withPT[index-1].StepName != "verify_mysql" {
			t.Fatalf("PT step must run after MySQL verification, previous=%q", withPT[index-1].StepName)
		}
		if step.Message != "安装 PT 工具（Percona Toolkit）" {
			t.Fatalf("unexpected PT step message: %q", step.Message)
		}
		return
	}
	t.Fatal("PT step was not created")
}

func TestBuildMySQLInstallStepsOptionalXtraBackupAndTCMalloc(t *testing.T) {
	steps := buildMySQLInstallSteps("task-tools", true, true, "tcmalloc")
	index := make(map[string]int, len(steps))
	for i, step := range steps {
		index[step.StepName] = i
	}
	for _, name := range []string{"configure_memory_allocator", "initialize_mysql", "verify_mysql", "install_pt_tools", "install_xtrabackup", "init_accounts"} {
		if _, ok := index[name]; !ok {
			t.Fatalf("missing step %q", name)
		}
	}
	if index["configure_memory_allocator"] >= index["initialize_mysql"] {
		t.Fatal("tcmalloc must be configured before MySQL initialization")
	}
	if index["verify_mysql"] >= index["install_pt_tools"] || index["install_pt_tools"] >= index["install_xtrabackup"] || index["install_xtrabackup"] >= index["init_accounts"] {
		t.Fatalf("unexpected optional tool ordering: %#v", index)
	}
}

func TestMySQLSystemdUnitNameIsUniquePerPort(t *testing.T) {
	if got := mysqlSystemdUnitName(3306); got != "mysqld-3306" {
		t.Fatalf("unit name = %q", got)
	}
	if mysqlSystemdUnitName(3306) == mysqlSystemdUnitName(3307) {
		t.Fatal("different ports must not share a systemd unit")
	}
}

func TestEnsureXtraBackupAccountPrivileges(t *testing.T) {
	accounts := ensureXtraBackupAccountPrivileges(normalizeInstallAccounts(nil), "8.0.44")
	for _, account := range accounts {
		if account.Role != mysqlapp.AccountRoleBackup {
			continue
		}
		if !account.ExtendedBackup {
			t.Fatal("enabled backup account must enable extended backup privileges")
		}
		for _, privilege := range account.Privileges {
			if privilege == "BACKUP_ADMIN" {
				return
			}
		}
		t.Fatalf("backup account missing BACKUP_ADMIN: %#v", account.Privileges)
	}
	t.Fatal("backup account not found")
}

func TestManagerResourceURLUsesTargetSpecificAddress(t *testing.T) {
	usecase := &CreateMySQLInstallTaskUsecase{
		managerHTTPAddr: "http://10.211.241.17:8080",
		managerAddrForIP: func(targetIP string) string {
			if targetIP != "192.168.31.210" {
				t.Fatalf("unexpected target IP %q", targetIP)
			}
			return "http://192.168.31.59:8080,http://manager.example.com:8080"
		},
	}
	got := usecase.managerResourceURL("192.168.31.210", "/api/v1/software/mysql/mysql-8.0.44.tar.xz")
	want := "http://192.168.31.59:8080/api/v1/software/mysql/mysql-8.0.44.tar.xz"
	if got != want {
		t.Fatalf("managerResourceURL() = %q, want %q", got, want)
	}
}
