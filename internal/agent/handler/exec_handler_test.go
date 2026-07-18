package handler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gmha/internal/agent/mysqlcheck"
)

func TestExecHandlerCreatesEphemeralMySQLDefaultsFromAgentConfig(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, mysqlcheck.DefaultConfigFile)
	err := mysqlcheck.UpsertInstance(configPath, mysqlcheck.InstanceConfig{
		Port: 3306, Socket: "/data/3306/mysql.sock", Username: "mha", Password: "monitor-secret",
		ManagementUsername: "root", ManagementPassword: "root-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewExecHandler("", dir)
	defaultsPath, err := handler.createMySQLDefaultsFile(3306)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(defaultsPath)
	info, err := os.Stat(defaultsPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("defaults file permission = %o, want 600", info.Mode().Perm())
	}
	data, err := os.ReadFile(defaultsPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, `user="mha"`) || !strings.Contains(text, `password="monitor-secret"`) || strings.Contains(text, "root-secret") {
		t.Fatalf("unexpected defaults file: %s", text)
	}
}

func TestMySQLPortFromCommand(t *testing.T) {
	for _, command := range []string{"mysql --port=3307", "mysql --port 3307 --execute=select"} {
		if got := mysqlPortFromCommand(command); got != 3307 {
			t.Fatalf("mysqlPortFromCommand(%q) = %d", command, got)
		}
	}
}
