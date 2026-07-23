package mysql

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	mysqlDriver "github.com/go-sql-driver/mysql"
	sqldomain "gmha/internal/domain/sqldiagnostic"
)

type DiagnosticCredential struct {
	Username string
	Password string
}

type DiagnosticCapabilities struct {
	PerformanceSchema  bool
	HistoryLong        bool
	DigestStatements   bool
	SlowLogTable       bool
	SlowLogThresholdMS int64
	ServerUnix         float64
	ServerBootID       int64
	SQLTextLimit       int
}

type DiagnosticClient struct {
	ConnectTimeout time.Duration
	QueryTimeout   time.Duration
}

func (c DiagnosticClient) Open(instance sqldomain.Instance, credential DiagnosticCredential) (*sql.DB, error) {
	if strings.TrimSpace(instance.MachineIP) == "" || instance.Port <= 0 {
		return nil, errors.New("invalid mysql diagnostic endpoint")
	}
	if strings.TrimSpace(credential.Username) == "" || credential.Password == "" {
		return nil, errors.New("mysql diagnostic credential is not configured")
	}
	timeout := c.ConnectTimeout
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	cfg := mysqlDriver.NewConfig()
	cfg.User = credential.Username
	cfg.Passwd = credential.Password
	cfg.Net = "tcp"
	cfg.Addr = fmt.Sprintf("%s:%d", instance.MachineIP, instance.Port)
	cfg.Timeout, cfg.ReadTimeout, cfg.WriteTimeout = timeout, timeout, timeout
	cfg.ParseTime = true
	cfg.Collation = "utf8mb4_unicode_ci"
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(2)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(2 * time.Minute)
	return db, nil
}

func (c DiagnosticClient) Capabilities(ctx context.Context, db *sql.DB) (DiagnosticCapabilities, error) {
	queryCtx, cancel := c.queryContext(ctx)
	defer cancel()
	var performance int
	var sqlTextLimit sql.NullInt64
	var serverUnix float64
	if err := db.QueryRowContext(queryCtx, `
		select @@performance_schema, @@performance_schema_max_sql_text_length, unix_timestamp(now(6))
	`).Scan(&performance, &sqlTextLimit, &serverUnix); err != nil {
		return DiagnosticCapabilities{}, err
	}
	result := DiagnosticCapabilities{
		PerformanceSchema: performance == 1,
		ServerUnix:        serverUnix,
		SQLTextLimit:      int(sqlTextLimit.Int64),
	}
	var uptime int64
	queryCtx, cancel = c.queryContext(ctx)
	err := db.QueryRowContext(queryCtx, `
		select cast(variable_value as unsigned)
		from performance_schema.global_status where variable_name = 'Uptime'
	`).Scan(&uptime)
	cancel()
	if err == nil {
		result.ServerBootID = int64(serverUnix) - uptime
	} else {
		// The boot identifier is only used for event de-duplication. A bounded
		// timestamp fallback is safer than dropping all history collection.
		result.ServerBootID = int64(serverUnix) / 3600 * 3600
	}
	if !result.PerformanceSchema {
		return c.slowLogCapabilities(ctx, db, result)
	}
	queryCtx, cancel = c.queryContext(ctx)
	rows, err := db.QueryContext(queryCtx, `
		select name, enabled from performance_schema.setup_consumers
		where name in ('events_statements_history_long', 'statements_digest')
	`)
	if err == nil {
		for rows.Next() {
			var name, enabled string
			if scanErr := rows.Scan(&name, &enabled); scanErr != nil {
				err = scanErr
				break
			}
			switch name {
			case "events_statements_history_long":
				result.HistoryLong = strings.EqualFold(enabled, "YES")
			case "statements_digest":
				result.DigestStatements = strings.EqualFold(enabled, "YES")
			}
		}
		if closeErr := rows.Close(); err == nil {
			err = closeErr
		}
	}
	cancel()
	if err != nil {
		return result, fmt.Errorf("inspect performance_schema consumers: %w", err)
	}
	return c.slowLogCapabilities(ctx, db, result)
}

func (c DiagnosticClient) slowLogCapabilities(ctx context.Context, db *sql.DB, result DiagnosticCapabilities) (DiagnosticCapabilities, error) {
	queryCtx, cancel := c.queryContext(ctx)
	defer cancel()
	var enabled int
	var output string
	var threshold float64
	if err := db.QueryRowContext(queryCtx, `select @@slow_query_log, @@log_output, @@long_query_time`).Scan(&enabled, &output, &threshold); err != nil {
		return result, fmt.Errorf("inspect slow query log: %w", err)
	}
	result.SlowLogTable = enabled == 1 && containsCSVToken(output, "TABLE")
	result.SlowLogThresholdMS = int64(threshold * 1000)
	return result, nil
}

func (c DiagnosticClient) LiveSessions(ctx context.Context, db *sql.DB, instance sqldomain.Instance, cfg sqldomain.Config) ([]sqldomain.Session, float64, error) {
	queryCtx, cancel := c.queryContext(ctx)
	defer cancel()
	rows, err := db.QueryContext(queryCtx, `
		select unix_timestamp(now(6)), p.id, coalesce(t.thread_id, 0), p.user,
			p.host, coalesce(p.db, ''), p.command, coalesce(p.state, ''),
			coalesce(es.sql_text, p.info, ''), coalesce(es.digest, ''),
			coalesce(es.digest_text, ''),
			case when es.timer_wait is null then p.time * 1000
				else es.timer_wait / 1000000000 end,
			case when es.timer_wait is null then 'processlist_seconds'
				else 'performance_schema_timer' end,
			case when es.timer_wait is null then 1000 else 1 end
		from information_schema.processlist p
		left join performance_schema.threads t on t.processlist_id = p.id
		left join performance_schema.events_statements_current es on es.thread_id = t.thread_id
		where p.id <> connection_id() and p.command <> 'Sleep'
			and coalesce(es.sql_text, p.info, '') <> ''
		order by 12 desc
	`)
	if err != nil {
		return c.liveSessionsFallback(ctx, db, instance, cfg)
	}
	defer rows.Close()
	return scanLiveSessions(rows, instance, cfg)
}

func (c DiagnosticClient) liveSessionsFallback(ctx context.Context, db *sql.DB, instance sqldomain.Instance, cfg sqldomain.Config) ([]sqldomain.Session, float64, error) {
	queryCtx, cancel := c.queryContext(ctx)
	defer cancel()
	rows, err := db.QueryContext(queryCtx, `
		select unix_timestamp(now(6)), id, 0, user, host, coalesce(db, ''),
			command, coalesce(state, ''), coalesce(info, ''), '', '', time * 1000,
			'processlist_seconds', 1000
		from information_schema.processlist
		where id <> connection_id() and command <> 'Sleep' and coalesce(info, '') <> ''
		order by time desc
	`)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	return scanLiveSessions(rows, instance, cfg)
}

func scanLiveSessions(rows *sql.Rows, instance sqldomain.Instance, cfg sqldomain.Config) ([]sqldomain.Session, float64, error) {
	observedAt := time.Now().UTC()
	var serverUnix float64
	var out []sqldomain.Session
	for rows.Next() {
		var item sqldomain.Session
		var sqlText, digest, digestText string
		var elapsed float64
		if err := rows.Scan(&serverUnix, &item.ProcessID, &item.ThreadID, &item.User,
			&item.ClientHost, &item.Database, &item.Command, &item.State, &sqlText,
			&digest, &digestText, &elapsed, &item.TimingSource, &item.TimingPrecisionMS); err != nil {
			return nil, serverUnix, err
		}
		item.Instance = instance
		item.ElapsedMS = int64(elapsed)
		if item.ElapsedMS < 0 {
			item.ElapsedMS = 0
		}
		item.MaxElapsedMS = item.ElapsedMS
		item.SQLText, item.SQLTextTruncated = prepareSQLText(sqlText, cfg)
		item.Digest = strings.ToLower(strings.TrimSpace(digest))
		if item.Digest == "" {
			item.Digest = SQLFingerprint(sqlText)
		}
		item.DigestText, _ = prepareSQLText(digestText, cfg)
		queryStartedUnix := serverUnix - float64(item.ElapsedMS)/1000
		item.QueryStartedAt = unixFloatTime(queryStartedUnix)
		item.FirstSeenAt, item.LastSeenAt = observedAt, observedAt
		item.SampleCount = 1
		item.Source = "processlist"
		item.ID = SessionID(instance, item.ProcessID, item.QueryStartedAt, item.Digest)
		out = append(out, item)
	}
	return out, serverUnix, rows.Err()
}

func (c DiagnosticClient) StatementHistory(ctx context.Context, db *sql.DB, instance sqldomain.Instance, caps DiagnosticCapabilities, cfg sqldomain.Config) ([]sqldomain.StatementEvent, error) {
	if !caps.PerformanceSchema || !caps.HistoryLong {
		return nil, nil
	}
	queryCtx, cancel := c.queryContext(ctx)
	defer cancel()
	rows, err := db.QueryContext(queryCtx, `
		select h.thread_id, h.event_id, h.event_name, coalesce(t.processlist_user, ''),
			coalesce(t.processlist_host, ''), coalesce(h.current_schema, ''),
			coalesce(h.sql_text, ''), coalesce(h.digest, ''), coalesce(h.digest_text, ''),
			coalesce(h.timer_start, 0), coalesce(h.timer_wait, 0), coalesce(h.lock_time, 0),
			coalesce(h.rows_affected, 0), coalesce(h.rows_sent, 0), coalesce(h.rows_examined, 0),
			coalesce(h.created_tmp_disk_tables, 0), coalesce(h.no_index_used, 0),
			coalesce(h.errors, 0), coalesce(h.warnings, 0)
		from performance_schema.events_statements_history_long h
		left join performance_schema.threads t on t.thread_id = h.thread_id
		where h.sql_text is not null and h.sql_text <> ''
			and h.event_name not like 'statement/com/%'
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	collectedAt := time.Now().UTC()
	var out []sqldomain.StatementEvent
	for rows.Next() {
		var item sqldomain.StatementEvent
		var sqlText, digest string
		var timerStart, timerWait, lockTime uint64
		if err := rows.Scan(&item.ThreadID, &item.EventID, &item.EventName, &item.User,
			&item.ClientHost, &item.Database, &sqlText, &digest, &item.DigestText, &timerStart, &timerWait, &lockTime,
			&item.RowsAffected, &item.RowsSent, &item.RowsExamined, &item.CreatedTmpDisk,
			&item.NoIndexUsed, &item.ErrorCount, &item.WarningCount); err != nil {
			return nil, err
		}
		item.Instance, item.ServerBootID, item.CollectedAt = instance, caps.ServerBootID, collectedAt
		item.SQLText, _ = prepareSQLText(sqlText, cfg)
		item.Digest = strings.ToLower(strings.TrimSpace(digest))
		if item.Digest == "" {
			item.Digest = SQLFingerprint(sqlText)
		}
		item.DigestText, _ = prepareSQLText(item.DigestText, cfg)
		item.DurationMS = float64(timerWait) / 1e9
		item.LockTimeMS = float64(lockTime) / 1e9
		item.StartedAt = time.Unix(caps.ServerBootID, 0).UTC().Add(time.Duration(timerStart / 1000))
		item.EndedAt = item.StartedAt.Add(time.Duration(timerWait / 1000))
		if item.StartedAt.After(collectedAt.Add(time.Minute)) || item.StartedAt.Before(time.Unix(caps.ServerBootID, 0).Add(-time.Minute)) {
			item.EndedAt = unixFloatTime(caps.ServerUnix)
			item.StartedAt = item.EndedAt.Add(-time.Duration(timerWait / 1000))
		}
		item.ID = StatementEventID(instance, caps.ServerBootID, item.ThreadID, item.EventID)
		out = append(out, item)
	}
	return out, rows.Err()
}

func (c DiagnosticClient) SlowLogEvents(ctx context.Context, db *sql.DB, instance sqldomain.Instance, caps DiagnosticCapabilities, since time.Time, cfg sqldomain.Config) ([]sqldomain.StatementEvent, error) {
	if !caps.SlowLogTable {
		return nil, nil
	}
	queryCtx, cancel := c.queryContext(ctx)
	defer cancel()
	rows, err := db.QueryContext(queryCtx, `
		select unix_timestamp(start_time),
			unix_timestamp(start_time) + time_to_sec(query_time),
			coalesce(user_host, ''), coalesce(db, ''), coalesce(sql_text, ''),
			coalesce(thread_id, 0), time_to_sec(query_time) * 1000,
			time_to_sec(lock_time) * 1000, coalesce(rows_sent, 0),
			coalesce(rows_examined, 0)
		from mysql.slow_log
		where start_time >= from_unixtime(?)
		order by start_time desc
		limit 100000
	`, since.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	collectedAt := time.Now().UTC()
	var out []sqldomain.StatementEvent
	for rows.Next() {
		var item sqldomain.StatementEvent
		var startedUnix, endedUnix, durationMS, lockMS float64
		var userHost, sqlText string
		if err := rows.Scan(&startedUnix, &endedUnix, &userHost, &item.Database,
			&sqlText, &item.ThreadID, &durationMS, &lockMS, &item.RowsSent,
			&item.RowsExamined); err != nil {
			return nil, err
		}
		item.Instance, item.ServerBootID, item.CollectedAt = instance, caps.ServerBootID, collectedAt
		item.EventName = "mysql.slow_log"
		item.User, item.ClientHost = splitSlowLogUserHost(userHost)
		item.SQLText, _ = prepareSQLText(sqlText, cfg)
		item.Digest = SQLFingerprint(sqlText)
		item.DurationMS, item.LockTimeMS = durationMS, lockMS
		item.StartedAt, item.EndedAt = unixFloatTime(startedUnix), unixFloatTime(endedUnix)
		item.ID = stableDiagnosticID(instance.Key(), "slow_log", item.ThreadID, item.StartedAt.UnixNano(), item.Digest)
		out = append(out, item)
	}
	return out, rows.Err()
}

func (c DiagnosticClient) DigestSnapshots(ctx context.Context, db *sql.DB, instance sqldomain.Instance, caps DiagnosticCapabilities, cfg sqldomain.Config) ([]sqldomain.DigestSnapshot, error) {
	if !caps.PerformanceSchema || !caps.DigestStatements {
		return nil, nil
	}
	queryCtx, cancel := c.queryContext(ctx)
	defer cancel()
	rows, err := db.QueryContext(queryCtx, `
		select coalesce(digest, ''), coalesce(digest_text, ''), coalesce(schema_name, ''),
			count_star, sum_timer_wait / 1000000000, max_timer_wait / 1000000000,
			sum_lock_time / 1000000000, sum_rows_affected, sum_rows_sent,
			sum_rows_examined, sum_errors, sum_warnings,
			unix_timestamp(first_seen), unix_timestamp(last_seen)
		from performance_schema.events_statements_summary_by_digest
		where digest is not null and count_star > 0
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	collectedAt := time.Now().UTC()
	var out []sqldomain.DigestSnapshot
	for rows.Next() {
		var item sqldomain.DigestSnapshot
		var firstUnix, lastUnix sql.NullFloat64
		if err := rows.Scan(&item.Digest, &item.DigestText, &item.Database, &item.Count,
			&item.SumTimerWaitMS, &item.MaxTimerWaitMS, &item.SumLockTimeMS,
			&item.SumRowsAffected, &item.SumRowsSent, &item.SumRowsExamined,
			&item.SumErrors, &item.SumWarnings, &firstUnix, &lastUnix); err != nil {
			return nil, err
		}
		item.Instance, item.ServerBootID, item.CollectedAt = instance, caps.ServerBootID, collectedAt
		item.Digest = strings.ToLower(strings.TrimSpace(item.Digest))
		item.DigestText, _ = prepareSQLText(item.DigestText, cfg)
		item.FirstSeenAt, item.LastSeenAt = unixFloatTime(firstUnix.Float64), unixFloatTime(lastUnix.Float64)
		item.ID = DigestSnapshotID(instance, caps.ServerBootID, item.Digest, collectedAt)
		out = append(out, item)
	}
	return out, rows.Err()
}

func (c DiagnosticClient) KillQuery(ctx context.Context, db *sql.DB, processID uint64) error {
	if processID == 0 {
		return errors.New("process_id is required")
	}
	queryCtx, cancel := c.queryContext(ctx)
	defer cancel()
	_, err := db.ExecContext(queryCtx, fmt.Sprintf("KILL QUERY %d", processID))
	return err
}

func (c DiagnosticClient) Ping(ctx context.Context, db *sql.DB) error {
	queryCtx, cancel := c.queryContext(ctx)
	defer cancel()
	return db.PingContext(queryCtx)
}

func (c DiagnosticClient) queryContext(ctx context.Context) (context.Context, context.CancelFunc) {
	timeout := c.QueryTimeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return context.WithTimeout(ctx, timeout)
}

func SessionID(instance sqldomain.Instance, processID uint64, startedAt time.Time, digest string) string {
	return stableDiagnosticID(instance.Key(), processID, startedAt.Unix(), strings.ToLower(digest))
}

func StatementEventID(instance sqldomain.Instance, bootID int64, threadID, eventID uint64) string {
	return stableDiagnosticID(instance.Key(), bootID, threadID, eventID)
}

func DigestSnapshotID(instance sqldomain.Instance, bootID int64, digest string, collectedAt time.Time) string {
	return stableDiagnosticID(instance.Key(), bootID, strings.ToLower(digest), collectedAt.UnixNano())
}

func SQLFingerprint(text string) string {
	normalized := strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	sum := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(sum[:])
}

func stableDiagnosticID(parts ...any) string {
	hash := sha256.New()
	for index, value := range parts {
		if index > 0 {
			_, _ = hash.Write([]byte{0})
		}
		_, _ = fmt.Fprint(hash, value)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

var (
	sqlQuotedLiteral = regexp.MustCompile(`(?s)'(?:''|\\.|[^'])*'|"(?:""|\\.|[^"])*"`)
	sqlNumberLiteral = regexp.MustCompile(`\b(?:0x[0-9a-fA-F]+|\d+(?:\.\d+)?)\b`)
	sqlSecretClause  = regexp.MustCompile(`(?i)(identified\s+(?:with\s+\S+\s+)?by|password\s*=)\s*('[^']*'|"[^"]*"|\S+)`)
)

func prepareSQLText(text string, cfg sqldomain.Config) (string, bool) {
	if !cfg.CaptureSQLText {
		return "", strings.TrimSpace(text) != ""
	}
	// Authentication secrets are always removed, even when general literal
	// redaction is disabled.
	text = sqlSecretClause.ReplaceAllString(text, "$1 '[REDACTED]'")
	if cfg.RedactLiterals {
		text = sqlQuotedLiteral.ReplaceAllString(text, "?")
		text = sqlNumberLiteral.ReplaceAllString(text, "?")
	}
	limit := cfg.MaxSQLTextBytes
	if limit <= 0 {
		limit = sqldomain.DefaultMaxSQLTextBytes
	}
	if len(text) <= limit {
		return text, false
	}
	for limit > 0 && !utf8.ValidString(text[:limit]) {
		limit--
	}
	return text[:limit], true
}

func unixFloatTime(value float64) time.Time {
	if value <= 0 {
		return time.Time{}
	}
	seconds := int64(value)
	nanos := int64((value - float64(seconds)) * float64(time.Second))
	return time.Unix(seconds, nanos).UTC()
}

func containsCSVToken(value, wanted string) bool {
	for _, item := range strings.Split(value, ",") {
		if strings.EqualFold(strings.TrimSpace(item), wanted) {
			return true
		}
	}
	return false
}

func splitSlowLogUserHost(value string) (string, string) {
	value = strings.TrimSpace(value)
	user := value
	if index := strings.Index(value, "["); index >= 0 {
		user = strings.TrimSpace(value[:index])
	}
	if index := strings.Index(user, "@"); index >= 0 {
		user = strings.TrimSpace(user[:index])
	}
	host := ""
	if index := strings.LastIndex(value, "["); index >= 0 {
		if end := strings.Index(value[index:], "]"); end > 0 {
			host = strings.TrimSpace(value[index+1 : index+end])
		}
	}
	return user, host
}
