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

func TestReplaceExecCommandPlaceholdersAlsoSupportsRollback(t *testing.T) {
	command := "mysql --defaults-extra-file=" + mysqlDefaultsFilePlaceholder + " && curl __GMHA_MANAGER_URL__/health"
	got := replaceExecCommandPlaceholders(command, "http://manager:8080", "/tmp/credentials with space.cnf")
	if strings.Contains(got, mysqlDefaultsFilePlaceholder) || strings.Contains(got, "__GMHA_MANAGER_URL__") {
		t.Fatalf("placeholders were not fully replaced: %s", got)
	}
	for _, expected := range []string{"http://manager:8080/health", "'/tmp/credentials with space.cnf'"} {
		if !strings.Contains(got, expected) {
			t.Fatalf("expected replacement %q in %s", expected, got)
		}
	}
}

func TestPTOnlineProgressParsesCopyPercentage(t *testing.T) {
	tests := []struct {
		line string
		want int
		ok   bool
	}{
		{"Copying `app`.`orders`: 42% 00:12 remain", 42, true},
		{"PT online copy 100%", 100, true},
		{"progress unavailable", 0, false},
		{"Copying: 101%", 101, false},
	}
	for _, test := range tests {
		got, ok := ptOnlineProgress(test.line)
		if got != test.want || ok != test.ok {
			t.Fatalf("ptOnlineProgress(%q) = (%d,%v), want (%d,%v)", test.line, got, ok, test.want, test.ok)
		}
	}
}

func TestPTArchiverProgressParsesProcessedRows(t *testing.T) {
	tests := []struct {
		line string
		want int64
		ok   bool
	}{
		{"2026-07-23T09:10:11 12 5000", 5000, true},
		{"2026-07-23 09:10:11 12 6000", 6000, true},
		{"SELECT 5000", 0, false},
		{"2026-07-23T09:10:11 elapsed rows", 0, false},
	}
	for _, test := range tests {
		got, ok := ptArchiverProgress(test.line)
		if got != test.want || ok != test.ok {
			t.Fatalf("ptArchiverProgress(%q) = (%d,%v), want (%d,%v)", test.line, got, ok, test.want, test.ok)
		}
	}
}
