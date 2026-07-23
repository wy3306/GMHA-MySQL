package mysql_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	sqldomain "gmha/internal/domain/sqldiagnostic"
	sqliterepo "gmha/internal/infrastructure/persistence/sqlite"
	mysqlapp "gmha/internal/mysql"
	_ "modernc.org/sqlite"
)

// TestSQLDiagnosticMySQLEndToEnd is intentionally opt-in because it needs a
// disposable MySQL server. It verifies the same source queries used in
// production and then closes/reopens the Manager SQLite database to prove that
// collected session, statement and digest records survive a process restart.
func TestSQLDiagnosticMySQLEndToEnd(t *testing.T) {
	addr := os.Getenv("GMHA_TEST_MYSQL_ADDR")
	if addr == "" {
		t.Skip("set GMHA_TEST_MYSQL_ADDR to run the MySQL integration test")
	}
	host, port := splitAddress(t, addr)
	user := envOr("GMHA_TEST_MYSQL_USER", "root")
	password := os.Getenv("GMHA_TEST_MYSQL_PASSWORD")
	if password == "" {
		t.Fatal("GMHA_TEST_MYSQL_PASSWORD is required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	instance := sqldomain.Instance{
		MachineID: "mysql-e2e", MachineName: "mysql-e2e", MachineIP: host,
		Cluster: "integration", Port: port, Version: "8.0",
	}
	client := mysqlapp.DiagnosticClient{ConnectTimeout: 15 * time.Second, QueryTimeout: 5 * time.Second}
	credential := mysqlapp.DiagnosticCredential{Username: user, Password: password}
	admin, err := client.Open(instance, credential)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	waitForMySQL(t, ctx, client, admin)

	setupStatements := []string{
		"create database if not exists gmha_diag_e2e",
		"create table if not exists gmha_diag_e2e.probe (id int primary key, value int not null)",
		"insert into gmha_diag_e2e.probe(id, value) values (1, 10), (2, 20) on duplicate key update value=values(value)",
	}
	for _, statement := range setupStatements {
		if _, err := admin.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := admin.ExecContext(ctx, `
		update performance_schema.setup_consumers
		set enabled='YES'
		where name in ('events_statements_history_long', 'statements_digest')
	`); err != nil {
		t.Fatal(err)
	}

	caps, err := client.Capabilities(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	if !caps.PerformanceSchema || !caps.HistoryLong || !caps.DigestStatements {
		t.Fatalf("required performance_schema sources unavailable: %+v", caps)
	}
	cfg := sqldomain.DefaultConfig()

	sqlitePath := filepath.Join(t.TempDir(), "manager.db")
	repo, metadataDB := openDiagnosticRepository(t, sqlitePath)
	cfg.RetentionHours = 48
	if err := repo.SaveConfig(ctx, cfg); err != nil {
		t.Fatal(err)
	}

	liveDone := make(chan error, 1)
	go func() {
		var ignored, sum int
		liveDone <- admin.QueryRowContext(ctx,
			"select /* gmha_live_probe */ sleep(3), sum(value) from gmha_diag_e2e.probe",
		).Scan(&ignored, &sum)
	}()
	live := waitForLiveSession(t, ctx, client, admin, instance, cfg)
	if live.ElapsedMS <= 0 || live.QueryStartedAt.IsZero() || live.TimingSource == "" {
		t.Fatalf("live timing fields are incomplete: %+v", live)
	}
	observedAt := time.Now().UTC()
	if err := repo.SaveSessionSnapshot(ctx, instance, observedAt, []sqldomain.Session{live}); err != nil {
		t.Fatal(err)
	}
	if err := <-liveDone; err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveSessionSnapshot(ctx, instance, time.Now().UTC(), nil); err != nil {
		t.Fatal(err)
	}

	firstDigests, err := client.DigestSnapshots(ctx, admin, instance, caps, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveDigestSnapshots(ctx, firstDigests); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 3; index++ {
		var sum int
		if err := admin.QueryRowContext(ctx, "select sum(value) from gmha_diag_e2e.probe").Scan(&sum); err != nil {
			t.Fatal(err)
		}
		if sum != 30 {
			t.Fatalf("unexpected probe sum: %d", sum)
		}
	}
	time.Sleep(20 * time.Millisecond)
	secondDigests, err := client.DigestSnapshots(ctx, admin, instance, caps, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveDigestSnapshots(ctx, secondDigests); err != nil {
		t.Fatal(err)
	}
	if !hasDigestForProbe(secondDigests) {
		t.Fatal("digest snapshot did not contain the probe SQL")
	}

	var completedValue int
	if err := admin.QueryRowContext(ctx,
		"select /* gmha_history_probe */ value from gmha_diag_e2e.probe where id=1",
	).Scan(&completedValue); err != nil {
		t.Fatal(err)
	}
	events, err := client.StatementHistory(ctx, admin, instance, caps, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !hasSQL(events, "gmha_history_probe") {
		t.Fatal("history_long did not return the completed probe SQL")
	}
	if err := repo.SaveStatementEvents(ctx, events); err != nil {
		t.Fatal(err)
	}

	for _, statement := range []string{
		"set global log_output='TABLE'",
		"set global slow_query_log=ON",
		"set global long_query_time=0.05",
	} {
		if _, err := admin.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	slowConn, err := client.Open(instance, credential)
	if err != nil {
		t.Fatal(err)
	}
	defer slowConn.Close()
	if err := client.Ping(ctx, slowConn); err != nil {
		t.Fatal(err)
	}
	var slept, slowSum int
	slowStarted := time.Now().UTC().Add(-time.Second)
	if err := slowConn.QueryRowContext(ctx,
		"select /* gmha_slow_probe */ sleep(0.12), sum(value) from gmha_diag_e2e.probe",
	).Scan(&slept, &slowSum); err != nil {
		t.Fatal(err)
	}
	caps, err = client.Capabilities(ctx, slowConn)
	if err != nil {
		t.Fatal(err)
	}
	if !caps.SlowLogTable {
		t.Fatalf("TABLE slow log was not detected: %+v", caps)
	}
	slowEvents := waitForSlowLog(t, ctx, client, slowConn, instance, caps, slowStarted, cfg, "gmha_slow_probe")
	if !hasSQL(slowEvents, "gmha_slow_probe") {
		t.Fatal("mysql.slow_log did not return the slow probe SQL")
	}
	if err := repo.SaveStatementEvents(ctx, slowEvents); err != nil {
		t.Fatal(err)
	}

	killConn, err := slowConn.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer killConn.Close()
	var killProcessID uint64
	if err := killConn.QueryRowContext(ctx, "select connection_id()").Scan(&killProcessID); err != nil {
		t.Fatal(err)
	}
	type killResult struct {
		err     error
		slept   int
		elapsed time.Duration
	}
	killDone := make(chan killResult, 1)
	go func() {
		started := time.Now()
		var ignored, sum int
		err := killConn.QueryRowContext(ctx,
			"select /* gmha_kill_probe */ sleep(10), sum(value) from gmha_diag_e2e.probe",
		).Scan(&ignored, &sum)
		killDone <- killResult{err: err, slept: ignored, elapsed: time.Since(started)}
	}()
	killTarget := waitForLiveSessionToken(t, ctx, client, admin, instance, cfg, "gmha_kill_probe")
	if killTarget.ProcessID != killProcessID {
		t.Fatalf("live process id mismatch: got %d want %d", killTarget.ProcessID, killProcessID)
	}
	if err := client.KillQuery(ctx, admin, killTarget.ProcessID); err != nil {
		t.Fatal(err)
	}
	killed := <-killDone
	if killed.elapsed >= 5*time.Second {
		t.Fatalf("KILL QUERY did not stop the statement promptly: %s", killed.elapsed)
	}
	if killed.err == nil && killed.slept != 1 {
		t.Fatalf("KILL QUERY did not interrupt SLEEP: result=%d elapsed=%s", killed.slept, killed.elapsed)
	}
	if killed.err != nil {
		message := strings.ToLower(killed.err.Error())
		if !strings.Contains(message, "interrupted") && !strings.Contains(message, "1317") {
			t.Fatalf("KILL QUERY returned an unexpected target error: %v", killed.err)
		}
	}

	if err := metadataDB.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, reopenedDB := openDiagnosticRepository(t, sqlitePath)
	defer reopenedDB.Close()
	reloadedConfig, err := reopened.LoadConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if reloadedConfig.RetentionHours != 48 {
		t.Fatalf("configuration was not durable: %+v", reloadedConfig)
	}
	windowStart, windowEnd := time.Now().UTC().Add(-2*time.Hour), time.Now().UTC().Add(2*time.Hour)
	sessions, err := reopened.ListSessions(ctx, windowStart, windowEnd)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) == 0 || sessions[0].EndedAt == nil || sessions[0].SQLText == "" {
		t.Fatalf("session lifecycle was not durable: %+v", sessions)
	}
	persistedEvents, err := reopened.ListStatementEvents(ctx, sqldomain.StatementEventQuery{
		Start: windowStart, End: windowEnd, Cluster: instance.Cluster, Limit: 10000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !hasSQL(persistedEvents, "gmha_history_probe") || !hasSQL(persistedEvents, "gmha_slow_probe") {
		t.Fatalf("statement persistence is incomplete; got %d events", len(persistedEvents))
	}
	persistedDigests, err := reopened.ListDigestSnapshots(ctx, windowStart, windowEnd)
	if err != nil {
		t.Fatal(err)
	}
	if len(persistedDigests) < 2 || !hasDigestForProbe(persistedDigests) {
		t.Fatalf("digest snapshots were not durable: %d", len(persistedDigests))
	}
}

func splitAddress(t *testing.T, value string) (string, int) {
	t.Helper()
	index := strings.LastIndex(value, ":")
	if index <= 0 {
		t.Fatalf("invalid GMHA_TEST_MYSQL_ADDR %q", value)
	}
	port, err := strconv.Atoi(value[index+1:])
	if err != nil {
		t.Fatal(err)
	}
	return value[:index], port
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func waitForMySQL(t *testing.T, ctx context.Context, client mysqlapp.DiagnosticClient, db *sql.DB) {
	t.Helper()
	var lastErr error
	for attempt := 0; attempt < 60; attempt++ {
		lastErr = client.Ping(ctx, db)
		if lastErr == nil {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("mysql did not become ready: %v", lastErr)
}

func waitForLiveSession(t *testing.T, ctx context.Context, client mysqlapp.DiagnosticClient, db *sql.DB, instance sqldomain.Instance, cfg sqldomain.Config) sqldomain.Session {
	t.Helper()
	return waitForLiveSessionToken(t, ctx, client, db, instance, cfg, "gmha_live_probe")
}

func waitForLiveSessionToken(t *testing.T, ctx context.Context, client mysqlapp.DiagnosticClient, db *sql.DB, instance sqldomain.Instance, cfg sqldomain.Config, token string) sqldomain.Session {
	t.Helper()
	var lastErr error
	for attempt := 0; attempt < 80; attempt++ {
		items, _, err := client.LiveSessions(ctx, db, instance, cfg)
		lastErr = err
		for _, item := range items {
			if strings.Contains(strings.ToLower(item.SQLText), strings.ToLower(token)) {
				return item
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("live SQL %q was not observed: %v", token, lastErr)
	return sqldomain.Session{}
}

func openDiagnosticRepository(t *testing.T, path string) (*sqliterepo.SQLDiagnosticRepository, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	repo := sqliterepo.NewSQLDiagnosticRepository(sqliterepo.NewDB(db, sqliterepo.DialectSQLite))
	if err := repo.Migrate(); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	return repo, db
}

func waitForSlowLog(t *testing.T, ctx context.Context, client mysqlapp.DiagnosticClient, db *sql.DB, instance sqldomain.Instance, caps mysqlapp.DiagnosticCapabilities, since time.Time, cfg sqldomain.Config, token string) []sqldomain.StatementEvent {
	t.Helper()
	var lastErr error
	for attempt := 0; attempt < 40; attempt++ {
		items, err := client.SlowLogEvents(ctx, db, instance, caps, since, cfg)
		lastErr = err
		if err == nil && hasSQL(items, token) {
			return items
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("slow SQL %q was not observed: %v", token, lastErr)
	return nil
}

func hasSQL(items []sqldomain.StatementEvent, token string) bool {
	for _, item := range items {
		if strings.Contains(strings.ToLower(item.SQLText), strings.ToLower(token)) {
			return true
		}
	}
	return false
}

func hasDigestForProbe(items []sqldomain.DigestSnapshot) bool {
	for _, item := range items {
		if strings.Contains(strings.ToLower(item.DigestText), "gmha_diag_e2e") {
			return true
		}
	}
	return false
}
