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
