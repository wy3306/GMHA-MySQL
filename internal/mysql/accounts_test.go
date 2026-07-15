// accounts_test.go 包含账号初始化相关功能的单元测试。
package mysql

import (
	"errors"
	"strings"
	"testing"
)

// TestNormalizeAccountSpecsDefaults 测试默认账号规格的生成，验证数量、启用状态和默认值。
func TestNormalizeAccountSpecsDefaults(t *testing.T) {
	items := NormalizeAccountSpecs(nil)
	if len(items) != 3 {
		t.Fatalf("expected 3 default accounts, got %d", len(items))
	}
	for _, item := range items {
		if !item.Enabled {
			t.Fatalf("default account should be enabled: %+v", item)
		}
		if item.Password != DefaultAccountPassword {
			t.Fatalf("unexpected default password for %s", item.Role)
		}
		if item.Host != DefaultAccountHost {
			t.Fatalf("unexpected default host for %s", item.Role)
		}
	}
}

func TestNormalizeAccountSpecsKeepsMultipleCustomUsers(t *testing.T) {
	items := NormalizeAccountSpecs([]AccountSpec{
		{Role: "custom_app", Username: "app_user", Password: "secret-1", Host: "10.0.0.%", Enabled: true, Privileges: []string{"SELECT"}},
		{Role: "custom_report", Username: "report_user", Password: "secret-2", Host: "10.0.1.%", Enabled: true, Privileges: []string{"SELECT", "SHOW VIEW"}},
	})
	if len(items) != 5 {
		t.Fatalf("expected 3 preset and 2 custom accounts, got %d", len(items))
	}
	if items[3].Username != "app_user" || items[4].Username != "report_user" {
		t.Fatalf("custom account order or names changed: %#v", items[3:])
	}
	if err := ValidateAccountSpecs(items); err != nil {
		t.Fatalf("custom accounts should validate: %v", err)
	}
}

// TestNormalizeAccountSpecsOverrideAndDisable 测试账号规格的覆盖和禁用功能。
func TestNormalizeAccountSpecsOverrideAndDisable(t *testing.T) {
	items := NormalizeAccountSpecs([]AccountSpec{
		{Role: AccountRoleMonitor, Username: "mon", Enabled: true},
		{Role: AccountRoleBackup, Enabled: false},
	})
	var monitor AccountSpec
	var backup AccountSpec
	for _, item := range items {
		switch item.Role {
		case AccountRoleMonitor:
			monitor = item
		case AccountRoleBackup:
			backup = item
		}
	}
	if monitor.Username != "mon" || monitor.Password != DefaultAccountPassword || monitor.Host != DefaultAccountHost {
		t.Fatalf("monitor defaults not filled: %+v", monitor)
	}
	if backup.Enabled {
		t.Fatalf("backup should be disabled")
	}
}

// TestValidateAccountSpecsRejectsInvalidInput 测试非法账号规格的验证，确保拒绝无效的用户名和主机地址。
func TestValidateAccountSpecsRejectsInvalidInput(t *testing.T) {
	err := ValidateAccountSpecs([]AccountSpec{{Role: AccountRoleMonitor, Username: "bad user", Password: "x", Host: "%", Enabled: true}})
	if err == nil {
		t.Fatal("expected invalid username error")
	}
	err = ValidateAccountSpecs([]AccountSpec{{Role: AccountRoleMonitor, Username: "monitor", Password: "x", Host: "bad host", Enabled: true}})
	if err == nil {
		t.Fatal("expected invalid host error")
	}
}

// TestAccountSQLStepsForMHA 测试 MHA 账号的 SQL 步骤生成，验证 CREATE USER、ALTER USER 和 GRANT 语句。
func TestAccountSQLStepsForMHA(t *testing.T) {
	steps := accountSQLSteps(AccountSpec{Role: AccountRoleMHA, Username: "mha", Password: "p'ass", Host: "%", Enabled: true})
	text := make([]string, 0, len(steps))
	for _, step := range steps {
		text = append(text, step.Name+":"+step.SQL)
	}
	joined := strings.Join(text, "\n")
	for _, want := range []string{
		"CREATE USER IF NOT EXISTS 'mha'@'%' IDENTIFIED BY 'p''ass'",
		"ALTER USER 'mha'@'%' IDENTIFIED BY 'p''ass'",
		"GRANT CREATE, ALTER, DROP, INSERT, UPDATE, DELETE, SELECT",
		"GRANT BACKUP_ADMIN, CLONE_ADMIN ON *.* TO 'mha'@'%'",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("generated SQL missing %q:\n%s", want, joined)
		}
	}
}

// TestAccountInitConnectFailureResult 测试连接失败结果的构建，验证可重试标记和错误信息格式。
func TestAccountInitConnectFailureResult(t *testing.T) {
	result := accountInitConnectFailure(errors.New("connection refused"))
	if result.Success || !result.Retryable || len(result.Items) != 1 {
		t.Fatalf("unexpected connect failure result: %+v", result)
	}
	if !strings.Contains(result.Items[0].Error, "连接失败") {
		t.Fatalf("unexpected item error: %+v", result.Items[0])
	}
}
