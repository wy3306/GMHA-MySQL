package handler

import (
	"strings"
	"testing"

	taskdomain "gmha/internal/domain/task"
)

func TestRemoveInstancePathsCommandIncludesExternalDataDirectories(t *testing.T) {
	spec := taskdomain.MySQLUninstallSpec{
		InstanceDir: "/srv/mysql/3306",
		DataDir:     "/data/mysql/3306",
		BinlogDir:   "/logs/mysql/3306/binlog",
		RedoDir:     "/fast/mysql/3306/redo",
		UndoDir:     "/fast/mysql/3306/undo",
		TmpDir:      "/tmp/mysql-3306",
	}
	command := removeInstancePathsCommand(spec)
	for _, path := range []string{spec.InstanceDir, spec.DataDir, spec.BinlogDir, spec.RedoDir, spec.UndoDir, spec.TmpDir} {
		if !strings.Contains(command, path) {
			t.Fatalf("cleanup command %q does not contain %q", command, path)
		}
	}
}

func TestValidateUninstallSpecRejectsBroadDataDirectory(t *testing.T) {
	spec := taskdomain.MySQLUninstallSpec{InstanceDir: "/data/3306", DataDir: "/data", BaseDir: "/usr/local/mysql"}
	if err := validateUninstallSpec(spec); err == nil {
		t.Fatal("expected unsafe data directory to be rejected")
	}
}

func TestMySQLPackageInstallDirSeparatesInstancesAndSupportsLegacy(t *testing.T) {
	packageName := "mysql-8.0.44-linux-x86_64.tar.xz"
	first := mysqlPackageInstallDir("/usr/local/mysql-3306", packageName, 3306, "mysqld-3306")
	second := mysqlPackageInstallDir("/usr/local/mysql-3307", packageName, 3307, "mysqld-3307")
	if first == second || !strings.HasSuffix(first, "-3306") || !strings.HasSuffix(second, "-3307") {
		t.Fatalf("instance package directories are not isolated: %q %q", first, second)
	}
	legacy := mysqlPackageInstallDir("/usr/local/mysql", packageName, 3306, "mysqld")
	if strings.HasSuffix(legacy, "-3306") {
		t.Fatalf("legacy install directory should remain unsuffixed: %q", legacy)
	}
}
