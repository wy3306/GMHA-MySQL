package app

import (
	"strings"
	"testing"
)

func TestMySQLUpgradeArchiveLayout(t *testing.T) {
	for _, tc := range []struct {
		name       string
		wantTarget string
		wantFlag   string
	}{
		{name: "mysql-5.7.44-linux-glibc2.17-x86_64.tar.xz", wantTarget: "mysql-5.7.44-linux-glibc2.17-x86_64", wantFlag: "-xJf"},
		{name: "mysql-8.0.44-linux-glibc2.17-x86_64.tar.gz", wantTarget: "mysql-8.0.44-linux-glibc2.17-x86_64", wantFlag: "-xzf"},
		{name: "mysql-8.4.6-linux-glibc2.17-x86_64.tgz", wantTarget: "mysql-8.4.6-linux-glibc2.17-x86_64", wantFlag: "-xzf"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			target, flag, err := mysqlUpgradeArchiveLayout(tc.name)
			if err != nil {
				t.Fatal(err)
			}
			if target != tc.wantTarget || flag != tc.wantFlag {
				t.Fatalf("got target=%q flag=%q, want target=%q flag=%q", target, flag, tc.wantTarget, tc.wantFlag)
			}
		})
	}
	if _, _, err := mysqlUpgradeArchiveLayout("mysql-8.0.44.rpm"); err == nil {
		t.Fatal("unsupported archive should be rejected")
	}
}

func TestMySQLUpgradeRestoreReadOnlyPreservesBothFlags(t *testing.T) {
	command := mysqlUpgradeRestoreReadOnlyCommand("mysql-client", "/var/lib/gmha/mysql-upgrade-3306")
	for _, expected := range []string{"read_only", "super_read_only=OFF", "read_only=OFF", "${1:-1}", "${2:-1}"} {
		if !strings.Contains(command, expected) {
			t.Fatalf("restore command should contain %q: %s", expected, command)
		}
	}
}

func TestMySQLUpgradeValidateConfigSupports57(t *testing.T) {
	legacy := mysqlUpgradeValidateConfigCommand("/mysql/bin/mysqld", "/data/3306/my.cnf", "5.7.44")
	if !strings.Contains(legacy, "--verbose --help") || !strings.Contains(legacy, "grep -q -- '--validate-config'") {
		t.Fatalf("MySQL 5.7 should use the compatible config check: %s", legacy)
	}
	modern := mysqlUpgradeValidateConfigCommand("/mysql/bin/mysqld", "/data/3306/my.cnf", "8.0.44")
	if !strings.Contains(modern, "--validate-config") {
		t.Fatalf("MySQL 8 should use --validate-config: %s", modern)
	}
}
