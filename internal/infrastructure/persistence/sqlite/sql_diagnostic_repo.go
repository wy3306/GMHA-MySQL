package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	sqldomain "gmha/internal/domain/sqldiagnostic"
)

// SQLDiagnosticRepository stores diagnostic samples in the Manager metadata
// database. Despite the package name, DB translates the schema and bind syntax
// for SQLite, MySQL and PostgreSQL.
type SQLDiagnosticRepository struct{ db *DB }

func NewSQLDiagnosticRepository(db *DB) *SQLDiagnosticRepository {
	return &SQLDiagnosticRepository{db: db}
}

func (r *SQLDiagnosticRepository) Migrate() error {
	_, err := r.db.Exec(`
		create table if not exists sql_diagnostic_config (
			id text primary key,
			enabled integer not null default 1,
			collection_interval_seconds integer not null default 5,
			slow_threshold_ms integer not null default 1000,
			retention_hours integer not null default 24,
			max_sql_text_bytes integer not null default 65536,
			capture_sql_text integer not null default 1,
			redact_literals integer not null default 0,
			updated_at text not null
		);
		create table if not exists sql_diagnostic_sessions (
			id text primary key,
			machine_id text not null,
			machine_name text not null default '',
			machine_ip text not null default '',
			cluster_name text not null default '',
			port integer not null,
			version text not null default '',
			process_id bigint not null,
			thread_id bigint not null default 0,
			db_user varchar(191) not null default '',
			client_host varchar(191) not null default '',
			database_name text not null default '',
			command text not null default '',
			state text not null default '',
			sql_text text not null default '',
			digest text not null default '',
			digest_text text not null default '',
			query_started_at text not null,
			first_seen_at text not null,
			last_seen_at text not null,
			ended_at text,
			elapsed_ms bigint not null default 0,
			max_elapsed_ms bigint not null default 0,
			timing_source varchar(64) not null default 'processlist_seconds',
			timing_precision_ms integer not null default 1000,
			sample_count bigint not null default 1,
			source text not null default 'processlist',
			sql_text_truncated integer not null default 0
		);
		create index if not exists idx_sql_diag_sessions_instance on sql_diagnostic_sessions(machine_id, port, last_seen_at);
		create index if not exists idx_sql_diag_sessions_window on sql_diagnostic_sessions(query_started_at, last_seen_at);
		create index if not exists idx_sql_diag_sessions_process on sql_diagnostic_sessions(machine_id, port, process_id);
		create table if not exists sql_diagnostic_statement_events (
			id text primary key,
			machine_id text not null,
			machine_name text not null default '',
			machine_ip text not null default '',
			cluster_name text not null default '',
			port integer not null,
			version text not null default '',
			server_boot_id bigint not null default 0,
			thread_id bigint not null,
			event_id bigint not null,
			event_name text not null default '',
			db_user varchar(191) not null default '',
			client_host varchar(191) not null default '',
			database_name text not null default '',
			sql_text text not null default '',
			digest text not null default '',
			digest_text text not null default '',
			duration_ms double precision not null default 0,
			lock_time_ms double precision not null default 0,
			rows_affected bigint not null default 0,
			rows_sent bigint not null default 0,
			rows_examined bigint not null default 0,
			created_tmp_disk_tables bigint not null default 0,
			no_index_used integer not null default 0,
			error_count bigint not null default 0,
			warning_count bigint not null default 0,
			started_at text not null,
			ended_at text not null,
			collected_at text not null
		);
		create index if not exists idx_sql_diag_events_window on sql_diagnostic_statement_events(ended_at);
		create index if not exists idx_sql_diag_events_instance on sql_diagnostic_statement_events(machine_id, port, ended_at);
		create table if not exists sql_diagnostic_digest_snapshots (
			id text primary key,
			machine_id text not null,
			machine_name text not null default '',
			machine_ip text not null default '',
			cluster_name text not null default '',
			port integer not null,
			version text not null default '',
			server_boot_id bigint not null default 0,
			digest text not null default '',
			digest_text text not null default '',
			database_name text not null default '',
			count_star bigint not null default 0,
			sum_timer_wait_ms double precision not null default 0,
			max_timer_wait_ms double precision not null default 0,
			sum_lock_time_ms double precision not null default 0,
			sum_rows_affected bigint not null default 0,
			sum_rows_sent bigint not null default 0,
			sum_rows_examined bigint not null default 0,
			sum_errors bigint not null default 0,
			sum_warnings bigint not null default 0,
			first_seen_at text not null,
			last_seen_at text not null,
			collected_at text not null
		);
		create index if not exists idx_sql_diag_digest_window on sql_diagnostic_digest_snapshots(collected_at);
		create index if not exists idx_sql_diag_digest_instance on sql_diagnostic_digest_snapshots(machine_id, port, collected_at);
		create table if not exists sql_diagnostic_instance_status (
			id text primary key,
			machine_id text not null,
			machine_name text not null default '',
			machine_ip text not null default '',
			cluster_name text not null default '',
			port integer not null,
			version text not null default '',
			status text not null default 'unknown',
			collection_mode varchar(32) not null default 'full',
			last_attempt_at text not null,
			last_success_at text,
			last_error text not null default '',
			live_session_count integer not null default 0,
			performance_schema_available integer not null default 0,
			history_long_consumer_enabled integer not null default 0,
			digest_consumer_enabled integer not null default 0,
			slow_log_table_available integer not null default 0,
			slow_log_threshold_ms integer not null default 0,
			server_clock_offset_ms bigint not null default 0,
			sql_text_limit integer not null default 0
		);
		create table if not exists sql_diagnostic_collection_runs (
			id text primary key,
			machine_id text not null,
			machine_name text not null default '',
			machine_ip text not null default '',
			cluster_name text not null default '',
			port integer not null,
			version text not null default '',
			status text not null,
			collection_mode varchar(32) not null default 'full',
			last_attempt_at text not null,
			last_success_at text,
			last_error text not null default '',
			live_session_count integer not null default 0,
			performance_schema_available integer not null default 0,
			history_long_consumer_enabled integer not null default 0,
			digest_consumer_enabled integer not null default 0,
			slow_log_table_available integer not null default 0,
			slow_log_threshold_ms integer not null default 0,
			server_clock_offset_ms bigint not null default 0,
			sql_text_limit integer not null default 0
		);
		create index if not exists idx_sql_diag_collection_window on sql_diagnostic_collection_runs(last_attempt_at);
		create index if not exists idx_sql_diag_collection_instance on sql_diagnostic_collection_runs(machine_id, port, last_attempt_at);
		create table if not exists sql_diagnostic_kill_audit (
			id text primary key,
			machine_id text not null,
			machine_name text not null default '',
			machine_ip text not null default '',
			cluster_name text not null default '',
			port integer not null,
			version text not null default '',
			process_id bigint not null,
			expected_digest text not null default '',
			expected_started_at text not null,
			sql_text text not null default '',
			db_user text not null default '',
			client_host text not null default '',
			reason text not null default '',
			request_source text not null default '',
			status text not null,
			error text not null default '',
			requested_at text not null,
			completed_at text
		);
		create index if not exists idx_sql_diag_kill_audit_time on sql_diagnostic_kill_audit(requested_at);
	`)
	if err != nil {
		return err
	}
	for _, statement := range []string{
		`alter table sql_diagnostic_instance_status add column collection_mode varchar(32) not null default 'full'`,
		`alter table sql_diagnostic_collection_runs add column collection_mode varchar(32) not null default 'full'`,
		`alter table sql_diagnostic_sessions add column timing_source varchar(64) not null default 'processlist_seconds'`,
		`alter table sql_diagnostic_sessions add column timing_precision_ms integer not null default 1000`,
		`alter table sql_diagnostic_statement_events add column db_user varchar(191) not null default ''`,
		`alter table sql_diagnostic_statement_events add column client_host varchar(191) not null default ''`,
		`alter table sql_diagnostic_instance_status add column slow_log_table_available integer not null default 0`,
		`alter table sql_diagnostic_instance_status add column slow_log_threshold_ms integer not null default 0`,
		`alter table sql_diagnostic_collection_runs add column slow_log_table_available integer not null default 0`,
		`alter table sql_diagnostic_collection_runs add column slow_log_threshold_ms integer not null default 0`,
	} {
		if _, alterErr := r.db.Exec(statement); alterErr != nil &&
			!strings.Contains(strings.ToLower(alterErr.Error()), "duplicate column") &&
			!strings.Contains(strings.ToLower(alterErr.Error()), "already exists") {
			return alterErr
		}
	}
	return nil
}

func (r *SQLDiagnosticRepository) LoadConfig(ctx context.Context) (sqldomain.Config, error) {
	cfg := sqldomain.DefaultConfig()
	var updated string
	err := r.db.QueryRowContext(ctx, `
		select enabled, collection_interval_seconds, slow_threshold_ms, retention_hours,
			max_sql_text_bytes, capture_sql_text, redact_literals, updated_at
		from sql_diagnostic_config where id = ?
	`, "default").Scan(&cfg.Enabled, &cfg.CollectionIntervalSeconds, &cfg.SlowThresholdMS,
		&cfg.RetentionHours, &cfg.MaxSQLTextBytes, &cfg.CaptureSQLText, &cfg.RedactLiterals, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		cfg.UpdatedAt = time.Now().UTC()
		return cfg, r.SaveConfig(ctx, cfg)
	}
	if err != nil {
		return sqldomain.Config{}, err
	}
	cfg.UpdatedAt = parseTime(updated)
	return cfg, nil
}

func (r *SQLDiagnosticRepository) SaveConfig(ctx context.Context, cfg sqldomain.Config) error {
	if cfg.UpdatedAt.IsZero() {
		cfg.UpdatedAt = time.Now().UTC()
	}
	_, err := r.db.ExecContext(ctx, `
		insert into sql_diagnostic_config (
			id, enabled, collection_interval_seconds, slow_threshold_ms, retention_hours,
			max_sql_text_bytes, capture_sql_text, redact_literals, updated_at
		) values (?, ?, ?, ?, ?, ?, ?, ?, ?)
		on conflict(id) do update set
			enabled=excluded.enabled,
			collection_interval_seconds=excluded.collection_interval_seconds,
			slow_threshold_ms=excluded.slow_threshold_ms,
			retention_hours=excluded.retention_hours,
			max_sql_text_bytes=excluded.max_sql_text_bytes,
			capture_sql_text=excluded.capture_sql_text,
			redact_literals=excluded.redact_literals,
			updated_at=excluded.updated_at
	`, "default", cfg.Enabled, cfg.CollectionIntervalSeconds, cfg.SlowThresholdMS,
		cfg.RetentionHours, cfg.MaxSQLTextBytes, cfg.CaptureSQLText, cfg.RedactLiterals,
		cfg.UpdatedAt.UTC().Format(time.RFC3339Nano))
	return err
}

func (r *SQLDiagnosticRepository) SaveSessionSnapshot(ctx context.Context, instance sqldomain.Instance, observedAt time.Time, sessions []sqldomain.Session) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, item := range sessions {
		if _, err := tx.ExecContext(ctx, `
			insert into sql_diagnostic_sessions (
				id, machine_id, machine_name, machine_ip, cluster_name, port, version,
				process_id, thread_id, db_user, client_host, database_name, command, state,
				sql_text, digest, digest_text, query_started_at, first_seen_at, last_seen_at,
				ended_at, elapsed_ms, max_elapsed_ms, timing_source, timing_precision_ms,
				sample_count, source, sql_text_truncated
			) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			on conflict(id) do update set
				machine_name=excluded.machine_name, machine_ip=excluded.machine_ip,
				cluster_name=excluded.cluster_name, version=excluded.version,
				thread_id=excluded.thread_id, db_user=excluded.db_user,
				client_host=excluded.client_host, database_name=excluded.database_name,
				command=excluded.command, state=excluded.state, sql_text=excluded.sql_text,
				digest=excluded.digest, digest_text=excluded.digest_text,
				last_seen_at=excluded.last_seen_at, ended_at=excluded.ended_at,
				elapsed_ms=excluded.elapsed_ms,
				max_elapsed_ms=case when excluded.max_elapsed_ms > sql_diagnostic_sessions.max_elapsed_ms then excluded.max_elapsed_ms else sql_diagnostic_sessions.max_elapsed_ms end,
				timing_source=excluded.timing_source,
				timing_precision_ms=excluded.timing_precision_ms,
				sample_count=sql_diagnostic_sessions.sample_count + 1,
				source=excluded.source, sql_text_truncated=excluded.sql_text_truncated
		`, item.ID, instance.MachineID, instance.MachineName, instance.MachineIP, instance.Cluster,
			instance.Port, instance.Version, item.ProcessID, item.ThreadID, item.User, item.ClientHost,
			item.Database, item.Command, item.State, item.SQLText, item.Digest, item.DigestText,
			formatTime(item.QueryStartedAt), formatTime(item.FirstSeenAt), formatTime(item.LastSeenAt),
			nullableTime(item.EndedAt), item.ElapsedMS, item.MaxElapsedMS,
			item.TimingSource, item.TimingPrecisionMS, item.SampleCount, item.Source,
			item.SQLTextTruncated); err != nil {
			return err
		}
	}
	// Anything still open from this instance but absent in this observation has
	// finished or switched statements. last_seen_at remains the upper bound.
	if _, err := tx.ExecContext(ctx, `
		update sql_diagnostic_sessions set ended_at = ?
		where machine_id = ? and port = ? and ended_at is null and last_seen_at < ?
	`, formatTime(observedAt), instance.MachineID, instance.Port, formatTime(observedAt)); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *SQLDiagnosticRepository) SaveStatementEvents(ctx context.Context, events []sqldomain.StatementEvent) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, item := range events {
		i := item.Instance
		if _, err := tx.ExecContext(ctx, `
			insert into sql_diagnostic_statement_events (
				id, machine_id, machine_name, machine_ip, cluster_name, port, version,
				server_boot_id, thread_id, event_id, event_name, db_user, client_host, database_name, sql_text,
				digest, digest_text, duration_ms, lock_time_ms, rows_affected, rows_sent,
				rows_examined, created_tmp_disk_tables, no_index_used, error_count,
				warning_count, started_at, ended_at, collected_at
			) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			on conflict(id) do nothing
		`, item.ID, i.MachineID, i.MachineName, i.MachineIP, i.Cluster, i.Port, i.Version,
			item.ServerBootID, item.ThreadID, item.EventID, item.EventName, item.User, item.ClientHost, item.Database,
			item.SQLText, item.Digest, item.DigestText, item.DurationMS, item.LockTimeMS,
			item.RowsAffected, item.RowsSent, item.RowsExamined, item.CreatedTmpDisk,
			item.NoIndexUsed, item.ErrorCount, item.WarningCount, formatTime(item.StartedAt),
			formatTime(item.EndedAt), formatTime(item.CollectedAt)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r *SQLDiagnosticRepository) SaveDigestSnapshots(ctx context.Context, items []sqldomain.DigestSnapshot) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, item := range items {
		i := item.Instance
		if _, err := tx.ExecContext(ctx, `
			insert into sql_diagnostic_digest_snapshots (
				id, machine_id, machine_name, machine_ip, cluster_name, port, version,
				server_boot_id, digest, digest_text, database_name, count_star,
				sum_timer_wait_ms, max_timer_wait_ms, sum_lock_time_ms, sum_rows_affected,
				sum_rows_sent, sum_rows_examined, sum_errors, sum_warnings,
				first_seen_at, last_seen_at, collected_at
			) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			on conflict(id) do nothing
		`, item.ID, i.MachineID, i.MachineName, i.MachineIP, i.Cluster, i.Port, i.Version,
			item.ServerBootID, item.Digest, item.DigestText, item.Database, item.Count,
			item.SumTimerWaitMS, item.MaxTimerWaitMS, item.SumLockTimeMS, item.SumRowsAffected,
			item.SumRowsSent, item.SumRowsExamined, item.SumErrors, item.SumWarnings,
			formatTime(item.FirstSeenAt), formatTime(item.LastSeenAt), formatTime(item.CollectedAt)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r *SQLDiagnosticRepository) SaveInstanceStatus(ctx context.Context, item sqldomain.InstanceStatus) error {
	i := item.Instance
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `
		insert into sql_diagnostic_instance_status (
			id, machine_id, machine_name, machine_ip, cluster_name, port, version,
			status, collection_mode, last_attempt_at, last_success_at, last_error, live_session_count,
			performance_schema_available, history_long_consumer_enabled,
			digest_consumer_enabled, slow_log_table_available, slow_log_threshold_ms,
			server_clock_offset_ms, sql_text_limit
		) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		on conflict(id) do update set
			machine_name=excluded.machine_name, machine_ip=excluded.machine_ip,
			cluster_name=excluded.cluster_name, version=excluded.version,
			status=excluded.status, collection_mode=excluded.collection_mode, last_attempt_at=excluded.last_attempt_at,
			last_success_at=excluded.last_success_at, last_error=excluded.last_error,
			live_session_count=excluded.live_session_count,
			performance_schema_available=excluded.performance_schema_available,
			history_long_consumer_enabled=excluded.history_long_consumer_enabled,
			digest_consumer_enabled=excluded.digest_consumer_enabled,
			slow_log_table_available=excluded.slow_log_table_available,
			slow_log_threshold_ms=excluded.slow_log_threshold_ms,
			server_clock_offset_ms=excluded.server_clock_offset_ms,
			sql_text_limit=excluded.sql_text_limit
	`, i.Key(), i.MachineID, i.MachineName, i.MachineIP, i.Cluster, i.Port, i.Version,
		item.Status, item.CollectionMode, formatTime(item.LastAttemptAt), optionalTime(item.LastSuccessAt),
		item.LastError, item.LiveSessionCount, item.PerformanceSchemaAvailable,
		item.HistoryLongConsumerEnabled, item.DigestConsumerEnabled,
		item.SlowLogTableAvailable, item.SlowLogThresholdMS,
		item.ServerClockOffsetMS, item.SQLTextLimit); err != nil {
		return err
	}
	runID := diagnosticID(i.Key(), item.LastAttemptAt.UnixNano())
	if _, err = tx.ExecContext(ctx, `
		insert into sql_diagnostic_collection_runs (
			id, machine_id, machine_name, machine_ip, cluster_name, port, version,
			status, collection_mode, last_attempt_at, last_success_at, last_error, live_session_count,
			performance_schema_available, history_long_consumer_enabled,
			digest_consumer_enabled, slow_log_table_available, slow_log_threshold_ms,
			server_clock_offset_ms, sql_text_limit
		) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		on conflict(id) do nothing
	`, runID, i.MachineID, i.MachineName, i.MachineIP, i.Cluster, i.Port, i.Version,
		item.Status, item.CollectionMode, formatTime(item.LastAttemptAt), optionalTime(item.LastSuccessAt),
		item.LastError, item.LiveSessionCount, item.PerformanceSchemaAvailable,
		item.HistoryLongConsumerEnabled, item.DigestConsumerEnabled,
		item.SlowLogTableAvailable, item.SlowLogThresholdMS,
		item.ServerClockOffsetMS, item.SQLTextLimit); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *SQLDiagnosticRepository) ListInstanceStatuses(ctx context.Context) ([]sqldomain.InstanceStatus, error) {
	rows, err := r.db.QueryContext(ctx, `
		select machine_id, machine_name, machine_ip, cluster_name, port, version,
			status, collection_mode, last_attempt_at, last_success_at, last_error, live_session_count,
			performance_schema_available, history_long_consumer_enabled,
			digest_consumer_enabled, slow_log_table_available, slow_log_threshold_ms,
			server_clock_offset_ms, sql_text_limit
		from sql_diagnostic_instance_status order by cluster_name, machine_name, port
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []sqldomain.InstanceStatus
	for rows.Next() {
		var item sqldomain.InstanceStatus
		var attempt, success sql.NullString
		if err := rows.Scan(&item.Instance.MachineID, &item.Instance.MachineName,
			&item.Instance.MachineIP, &item.Instance.Cluster, &item.Instance.Port,
			&item.Instance.Version, &item.Status, &item.CollectionMode, &attempt, &success, &item.LastError,
			&item.LiveSessionCount, &item.PerformanceSchemaAvailable,
			&item.HistoryLongConsumerEnabled, &item.DigestConsumerEnabled,
			&item.SlowLogTableAvailable, &item.SlowLogThresholdMS,
			&item.ServerClockOffsetMS, &item.SQLTextLimit); err != nil {
			return nil, err
		}
		item.LastAttemptAt = parseDiagnosticNullableTime(attempt)
		item.LastSuccessAt = parseDiagnosticNullableTime(success)
		out = append(out, item)
	}
	return out, rows.Err()
}

func (r *SQLDiagnosticRepository) ListCollectionStatuses(ctx context.Context, start, end time.Time) ([]sqldomain.InstanceStatus, error) {
	rows, err := r.db.QueryContext(ctx, `
		select machine_id, machine_name, machine_ip, cluster_name, port, version,
			status, collection_mode, last_attempt_at, last_success_at, last_error, live_session_count,
			performance_schema_available, history_long_consumer_enabled,
			digest_consumer_enabled, slow_log_table_available, slow_log_threshold_ms,
			server_clock_offset_ms, sql_text_limit
		from sql_diagnostic_collection_runs
		where last_attempt_at >= ? and last_attempt_at <= ? and collection_mode = 'full'
		order by machine_id, port, last_attempt_at
	`, formatTime(start), formatTime(end))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []sqldomain.InstanceStatus
	for rows.Next() {
		var item sqldomain.InstanceStatus
		var attempt, success sql.NullString
		if err := rows.Scan(&item.Instance.MachineID, &item.Instance.MachineName,
			&item.Instance.MachineIP, &item.Instance.Cluster, &item.Instance.Port,
			&item.Instance.Version, &item.Status, &item.CollectionMode, &attempt, &success, &item.LastError,
			&item.LiveSessionCount, &item.PerformanceSchemaAvailable,
			&item.HistoryLongConsumerEnabled, &item.DigestConsumerEnabled,
			&item.SlowLogTableAvailable, &item.SlowLogThresholdMS,
			&item.ServerClockOffsetMS, &item.SQLTextLimit); err != nil {
			return nil, err
		}
		item.LastAttemptAt = parseDiagnosticNullableTime(attempt)
		item.LastSuccessAt = parseDiagnosticNullableTime(success)
		out = append(out, item)
	}
	return out, rows.Err()
}

func (r *SQLDiagnosticRepository) ListSessions(ctx context.Context, start, end time.Time) ([]sqldomain.Session, error) {
	rows, err := r.db.QueryContext(ctx, `
		select id, machine_id, machine_name, machine_ip, cluster_name, port, version,
			process_id, thread_id, db_user, client_host, database_name, command, state,
			sql_text, digest, digest_text, query_started_at, first_seen_at, last_seen_at,
			ended_at, elapsed_ms, max_elapsed_ms, timing_source, timing_precision_ms,
			sample_count, source, sql_text_truncated
		from sql_diagnostic_sessions
		where query_started_at <= ? and last_seen_at >= ?
		order by last_seen_at desc
	`, formatTime(end), formatTime(start))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []sqldomain.Session
	for rows.Next() {
		item, err := scanDiagnosticSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (r *SQLDiagnosticRepository) ListStatementEvents(ctx context.Context, query sqldomain.StatementEventQuery) ([]sqldomain.StatementEvent, error) {
	if query.Limit <= 0 || query.Limit > 100000 {
		query.Limit = 100000
	}
	var statement strings.Builder
	statement.WriteString(`
		select id, machine_id, machine_name, machine_ip, cluster_name, port, version,
			server_boot_id, thread_id, event_id, event_name, database_name, sql_text,
			db_user, client_host,
			digest, digest_text, duration_ms, lock_time_ms, rows_affected, rows_sent,
			rows_examined, created_tmp_disk_tables, no_index_used, error_count,
			warning_count, started_at, ended_at, collected_at
		from sql_diagnostic_statement_events
		where ended_at >= ? and ended_at <= ? and duration_ms >= ?
	`)
	args := []any{formatTime(query.Start), formatTime(query.End), query.MinimumDurationMS}
	if query.Cluster != "" {
		statement.WriteString(" and cluster_name = ?")
		args = append(args, query.Cluster)
	}
	if query.Machine != "" {
		statement.WriteString(" and (machine_id = ? or machine_name = ? or machine_ip = ?)")
		args = append(args, query.Machine, query.Machine, query.Machine)
	}
	if query.Port > 0 {
		statement.WriteString(" and port = ?")
		args = append(args, query.Port)
	}
	if query.Database != "" {
		statement.WriteString(" and database_name = ?")
		args = append(args, query.Database)
	}
	if query.Keyword != "" {
		statement.WriteString(" and (lower(sql_text) like lower(?) or lower(digest_text) like lower(?) or lower(digest) like lower(?))")
		like := "%" + query.Keyword + "%"
		args = append(args, like, like, like)
	}
	statement.WriteString(" order by ended_at desc limit ?")
	args = append(args, query.Limit)
	rows, err := r.db.QueryContext(ctx, statement.String(), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []sqldomain.StatementEvent
	for rows.Next() {
		item, err := scanStatementEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (r *SQLDiagnosticRepository) ListDigestSnapshots(ctx context.Context, baselineStart, end time.Time) ([]sqldomain.DigestSnapshot, error) {
	rows, err := r.db.QueryContext(ctx, `
		select id, machine_id, machine_name, machine_ip, cluster_name, port, version,
			server_boot_id, digest, digest_text, database_name, count_star,
			sum_timer_wait_ms, max_timer_wait_ms, sum_lock_time_ms, sum_rows_affected,
			sum_rows_sent, sum_rows_examined, sum_errors, sum_warnings,
			first_seen_at, last_seen_at, collected_at
		from sql_diagnostic_digest_snapshots
		where collected_at >= ? and collected_at <= ?
		order by machine_id, port, digest, collected_at
	`, formatTime(baselineStart), formatTime(end))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []sqldomain.DigestSnapshot
	for rows.Next() {
		item, err := scanDigestSnapshot(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (r *SQLDiagnosticRepository) SaveKillAudit(ctx context.Context, item sqldomain.KillAudit) error {
	i := item.Instance
	_, err := r.db.ExecContext(ctx, `
		insert into sql_diagnostic_kill_audit (
			id, machine_id, machine_name, machine_ip, cluster_name, port, version,
			process_id, expected_digest, expected_started_at, sql_text, db_user,
			client_host, reason, request_source, status, error, requested_at, completed_at
		) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		on conflict(id) do update set
			sql_text=excluded.sql_text, db_user=excluded.db_user,
			client_host=excluded.client_host, status=excluded.status,
			error=excluded.error, completed_at=excluded.completed_at
	`, item.ID, i.MachineID, i.MachineName, i.MachineIP, i.Cluster, i.Port, i.Version,
		item.ProcessID, item.ExpectedDigest, formatTime(item.ExpectedStartedAt), item.SQLText,
		item.User, item.ClientHost, item.Reason, item.RequestSource, item.Status, item.Error,
		formatTime(item.RequestedAt), nullableTime(item.CompletedAt))
	return err
}

func (r *SQLDiagnosticRepository) ListKillAudits(ctx context.Context, start, end time.Time) ([]sqldomain.KillAudit, error) {
	rows, err := r.db.QueryContext(ctx, `
		select id, machine_id, machine_name, machine_ip, cluster_name, port, version,
			process_id, expected_digest, expected_started_at, sql_text, db_user,
			client_host, reason, request_source, status, error, requested_at, completed_at
		from sql_diagnostic_kill_audit
		where requested_at >= ? and requested_at <= ? order by requested_at desc
	`, formatTime(start), formatTime(end))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []sqldomain.KillAudit
	for rows.Next() {
		var item sqldomain.KillAudit
		var expected, requested string
		var completed sql.NullString
		if err := rows.Scan(&item.ID, &item.Instance.MachineID, &item.Instance.MachineName,
			&item.Instance.MachineIP, &item.Instance.Cluster, &item.Instance.Port,
			&item.Instance.Version, &item.ProcessID, &item.ExpectedDigest, &expected,
			&item.SQLText, &item.User, &item.ClientHost, &item.Reason, &item.RequestSource,
			&item.Status, &item.Error, &requested, &completed); err != nil {
			return nil, err
		}
		item.ExpectedStartedAt, item.RequestedAt = parseTime(expected), parseTime(requested)
		if completed.Valid {
			value := parseTime(completed.String)
			item.CompletedAt = &value
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (r *SQLDiagnosticRepository) PurgeBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	var total int64
	for _, query := range []string{
		`delete from sql_diagnostic_sessions where last_seen_at < ?`,
		`delete from sql_diagnostic_statement_events where ended_at < ?`,
		// Keep one extra hour of digest baselines so the beginning of a retained
		// query window can still calculate an interval delta.
		`delete from sql_diagnostic_digest_snapshots where collected_at < ?`,
		`delete from sql_diagnostic_kill_audit where requested_at < ?`,
		`delete from sql_diagnostic_collection_runs where last_attempt_at < ?`,
	} {
		value := cutoff
		if strings.Contains(query, "digest_snapshots") {
			value = cutoff.Add(-time.Hour)
		}
		result, err := r.db.ExecContext(ctx, query, formatTime(value))
		if err != nil {
			return total, err
		}
		affected, _ := result.RowsAffected()
		total += affected
	}
	return total, nil
}

func scanDiagnosticSession(scanner interface{ Scan(...any) error }) (sqldomain.Session, error) {
	var item sqldomain.Session
	var started, first, last string
	var ended sql.NullString
	err := scanner.Scan(&item.ID, &item.Instance.MachineID, &item.Instance.MachineName,
		&item.Instance.MachineIP, &item.Instance.Cluster, &item.Instance.Port,
		&item.Instance.Version, &item.ProcessID, &item.ThreadID, &item.User,
		&item.ClientHost, &item.Database, &item.Command, &item.State, &item.SQLText,
		&item.Digest, &item.DigestText, &started, &first, &last, &ended,
		&item.ElapsedMS, &item.MaxElapsedMS, &item.TimingSource, &item.TimingPrecisionMS,
		&item.SampleCount, &item.Source, &item.SQLTextTruncated)
	if err != nil {
		return item, err
	}
	item.QueryStartedAt, item.FirstSeenAt, item.LastSeenAt = parseTime(started), parseTime(first), parseTime(last)
	if ended.Valid {
		value := parseTime(ended.String)
		item.EndedAt = &value
	}
	return item, nil
}

func scanStatementEvent(scanner interface{ Scan(...any) error }) (sqldomain.StatementEvent, error) {
	var item sqldomain.StatementEvent
	var started, ended, collected string
	err := scanner.Scan(&item.ID, &item.Instance.MachineID, &item.Instance.MachineName,
		&item.Instance.MachineIP, &item.Instance.Cluster, &item.Instance.Port,
		&item.Instance.Version, &item.ServerBootID, &item.ThreadID, &item.EventID,
		&item.EventName, &item.Database, &item.SQLText, &item.User, &item.ClientHost, &item.Digest, &item.DigestText,
		&item.DurationMS, &item.LockTimeMS, &item.RowsAffected, &item.RowsSent,
		&item.RowsExamined, &item.CreatedTmpDisk, &item.NoIndexUsed, &item.ErrorCount,
		&item.WarningCount, &started, &ended, &collected)
	if err != nil {
		return item, err
	}
	item.StartedAt, item.EndedAt, item.CollectedAt = parseTime(started), parseTime(ended), parseTime(collected)
	return item, nil
}

func scanDigestSnapshot(scanner interface{ Scan(...any) error }) (sqldomain.DigestSnapshot, error) {
	var item sqldomain.DigestSnapshot
	var first, last, collected string
	err := scanner.Scan(&item.ID, &item.Instance.MachineID, &item.Instance.MachineName,
		&item.Instance.MachineIP, &item.Instance.Cluster, &item.Instance.Port,
		&item.Instance.Version, &item.ServerBootID, &item.Digest, &item.DigestText,
		&item.Database, &item.Count, &item.SumTimerWaitMS, &item.MaxTimerWaitMS,
		&item.SumLockTimeMS, &item.SumRowsAffected, &item.SumRowsSent,
		&item.SumRowsExamined, &item.SumErrors, &item.SumWarnings, &first, &last, &collected)
	if err != nil {
		return item, err
	}
	item.FirstSeenAt, item.LastSeenAt, item.CollectedAt = parseTime(first), parseTime(last), parseTime(collected)
	return item, nil
}

func formatTime(value time.Time) string {
	// Fixed-width fractional seconds keep lexical ordering identical to
	// chronological ordering in every supported metadata database.
	return value.UTC().Format("2006-01-02T15:04:05.000000000Z07:00")
}

func optionalTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return formatTime(value)
}

func nullableTime(value *time.Time) any {
	if value == nil || value.IsZero() {
		return nil
	}
	return formatTime(*value)
}

func parseDiagnosticNullableTime(value sql.NullString) time.Time {
	if !value.Valid {
		return time.Time{}
	}
	return parseTime(value.String)
}

func parseTime(value string) time.Time {
	result, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		result, _ = time.Parse(time.RFC3339, value)
	}
	return result
}

func diagnosticID(parts ...any) string {
	var b strings.Builder
	for index, part := range parts {
		if index > 0 {
			b.WriteByte(':')
		}
		_, _ = fmt.Fprint(&b, part)
	}
	return b.String()
}
