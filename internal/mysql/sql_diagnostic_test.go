package mysql

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	sqldomain "gmha/internal/domain/sqldiagnostic"
)

func TestPrepareSQLTextRedactsAuthenticationSecrets(t *testing.T) {
	cfg := sqldomain.DefaultConfig()
	text, truncated := prepareSQLText(`CREATE USER app IDENTIFIED BY 'super-secret'`, cfg)
	if truncated || strings.Contains(text, "super-secret") || !strings.Contains(text, "[REDACTED]") {
		t.Fatalf("authentication secret was not safely redacted: %q", text)
	}
	text, _ = prepareSQLText(`ALTER USER app IDENTIFIED WITH caching_sha2_password BY "another-secret"`, cfg)
	if strings.Contains(text, "another-secret") {
		t.Fatalf("plugin authentication secret was not redacted: %q", text)
	}
}

func TestPrepareSQLTextLiteralRedactionAndUTF8Truncation(t *testing.T) {
	cfg := sqldomain.DefaultConfig()
	cfg.RedactLiterals = true
	text, _ := prepareSQLText(`select * from users where email='dba@example.com' and id=42`, cfg)
	if strings.Contains(text, "dba@example.com") || strings.Contains(text, "42") {
		t.Fatalf("literal redaction failed: %q", text)
	}
	cfg.RedactLiterals = false
	cfg.MaxSQLTextBytes = 8
	text, truncated := prepareSQLText("查询数据库abc", cfg)
	if !truncated || !utf8.ValidString(text) || len(text) > 8 {
		t.Fatalf("utf-8 truncation failed: %q, truncated=%v", text, truncated)
	}
}

func TestDiagnosticIDsAreStableAndStatementSpecific(t *testing.T) {
	instance := sqldomain.Instance{MachineID: "machine-1", Port: 3306}
	start := time.Date(2026, 7, 23, 0, 0, 0, 0, time.UTC)
	first := SessionID(instance, 10, start, "abc")
	if first != SessionID(instance, 10, start, "abc") {
		t.Fatal("session ID is not stable")
	}
	if first == SessionID(instance, 10, start.Add(time.Second), "abc") {
		t.Fatal("different statement start should not reuse a session ID")
	}
	if SQLFingerprint("select   1") != SQLFingerprint(" select 1 ") {
		t.Fatal("fingerprint should normalize whitespace")
	}
}

func TestSlowLogCapabilityAndUserHostParsing(t *testing.T) {
	if !containsCSVToken("FILE,TABLE", "table") || containsCSVToken("FILE", "TABLE") {
		t.Fatal("log_output token parsing is incorrect")
	}
	user, host := splitSlowLogUserHost("app[app] @ api-1 [10.0.0.8]")
	if user != "app" || host != "10.0.0.8" {
		t.Fatalf("unexpected slow log user_host parse: user=%q host=%q", user, host)
	}
}
