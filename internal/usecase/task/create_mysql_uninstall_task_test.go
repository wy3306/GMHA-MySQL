package task

import (
	"testing"

	mysqlapp "gmha/internal/mysql"
)

func TestMySQLUninstallSpecFillsAdoptedInstanceMetadata(t *testing.T) {
	instance := mysqlapp.Instance{
		Port: 3306, MySQLUser: "mha", DataDir: "/data/3306/data",
		BinlogDir: "/data/3306/binlog", RedoDir: "/data/3306/redo",
		UndoDir: "/data/3306/undo", TmpDir: "/data/3306/tmp",
		SystemdUnit: "mysqld", SocketPath: "/data/3306/data/mysql.sock",
	}
	spec := mysqlUninstallSpecFromInstance(3306, instance)

	if spec.InstanceDir != "/data/3306" {
		t.Fatalf("InstanceDir = %q, want /data/3306", spec.InstanceDir)
	}
	if spec.BaseDir != "/usr/local/mysql-3306" || spec.MyCnfPath != "/data/3306/my.cnf" {
		t.Fatalf("missing safe defaults: %+v", spec)
	}
	for name, value := range map[string]string{
		"instance_dir": spec.InstanceDir, "data_dir": spec.DataDir, "base_dir": spec.BaseDir,
	} {
		if value == "" || value == "." || value == "/" {
			t.Fatalf("unsafe %s generated: %q", name, value)
		}
	}
}

func TestInferInstanceDirRejectsBroadOrMixedParents(t *testing.T) {
	if got := inferInstanceDir(defaultMySQLUninstallSpec(3306)); got != "/data/3306" {
		t.Fatalf("inferInstanceDir() = %q, want /data/3306", got)
	}
	mixed := defaultMySQLUninstallSpec(3306)
	mixed.BinlogDir = "/logs/3306/binlog"
	if got := inferInstanceDir(mixed); got != "" {
		t.Fatalf("inferInstanceDir() = %q for mixed parents, want empty", got)
	}
}
