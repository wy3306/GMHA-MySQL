package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	hbdomain "gmha/internal/domain/heartbeat"
)

// HeartbeatRepository 是心跳状态的 SQLite 仓储实现。
type HeartbeatRepository struct {
	db *DB
}

func NewHeartbeatRepository(db *DB) *HeartbeatRepository {
	return &HeartbeatRepository{db: db}
}

func (r *HeartbeatRepository) Migrate() error {
	_, err := r.db.Exec(`
		create table if not exists agent_latest_status (
			agent_id text primary key,
			machine_id text not null,
			cluster_id text not null default '',
			hostname text not null,
			version text not null,
			current_state text not null,
			overall_health text not null,
			last_heartbeat_at text not null,
			last_healthy_at text,
			last_state_change_at text not null,
			last_seq integer not null default 0,
			last_boot_id text not null default '',
			consecutive_misses integer not null default 0,
			consecutive_bad_checks integer not null default 0,
			last_error_summary text not null default '',
			checks_json text not null default '[]',
			metrics_json text not null default '[]',
			updated_at text not null
		);
		create index if not exists idx_agent_latest_status_state on agent_latest_status(current_state);
		create index if not exists idx_agent_latest_status_cluster on agent_latest_status(cluster_id);
		create table if not exists agent_event_log (
			id text primary key,
			agent_id text not null,
			machine_id text not null,
			event_type text not null,
			prev_state text not null default '',
			new_state text not null default '',
			reason text not null,
			heartbeat_seq integer not null default 0,
			payload_json text not null,
			created_at text not null
		);
		create index if not exists idx_agent_event_log_agent_time on agent_event_log(agent_id, created_at desc);
		create table if not exists agent_metric_snapshot (
			id integer primary key autoincrement,
			agent_id text not null,
			machine_id text not null,
			cluster_id text not null default '',
			metrics_json text not null default '[]',
			collected_at text not null
		);
		create index if not exists idx_agent_metric_snapshot_cluster_time on agent_metric_snapshot(cluster_id, collected_at desc);
		create index if not exists idx_agent_metric_snapshot_machine_time on agent_metric_snapshot(machine_id, collected_at desc);
		create table if not exists performance_metric_sample (
			id integer primary key autoincrement,
			sample_key varchar(128) not null,
			agent_id varchar(191) not null,
			machine_id varchar(191) not null,
			cluster_id varchar(191) not null default '',
			scope varchar(32) not null,
			category varchar(64) not null default '',
			metric_name varchar(191) not null,
			instance varchar(191) not null default '',
			labels_json text not null default '{}',
			value_type varchar(32) not null default '',
			numeric_value real,
			value_json text not null default 'null',
			success integer not null default 1,
			error text not null default '',
			collected_at varchar(64) not null
		);
		create unique index if not exists idx_performance_metric_sample_key on performance_metric_sample(sample_key);
		create index if not exists idx_performance_metric_cluster_name_time on performance_metric_sample(cluster_id, metric_name, collected_at desc);
		create index if not exists idx_performance_metric_machine_name_time on performance_metric_sample(machine_id, metric_name, collected_at desc);
		create index if not exists idx_performance_metric_instance_name_time on performance_metric_sample(instance, metric_name, collected_at desc);
	`)
	_, _ = r.db.Exec(`alter table agent_latest_status add column metrics_json text not null default '[]'`)
	return err
}

// AppendMetricSnapshot stores only snapshots that contain metrics and keeps a
// rolling seven-day window. Cleanup is intentionally amortized to every 128th
// insert so the heartbeat hot path does not perform a delete each time.
func (r *HeartbeatRepository) AppendMetricSnapshot(ctx context.Context, item hbdomain.MetricSnapshot) error {
	if len(item.Metrics) == 0 {
		return nil
	}
	metricsJSON, err := json.Marshal(item.Metrics)
	if err != nil {
		return err
	}
	result, err := r.db.ExecContext(ctx, `
		insert into agent_metric_snapshot (agent_id, machine_id, cluster_id, metrics_json, collected_at)
		values (?, ?, ?, ?, ?)
	`, item.AgentID, item.MachineID, item.ClusterID, string(metricsJSON), item.CollectedAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return err
	}
	if id, idErr := result.LastInsertId(); idErr == nil && id%128 == 0 {
		cutoff := time.Now().UTC().Add(-7 * 24 * time.Hour).Format(time.RFC3339Nano)
		_, _ = r.db.ExecContext(ctx, `delete from agent_metric_snapshot where collected_at < ?`, cutoff)
		_, _ = r.db.ExecContext(ctx, `delete from performance_metric_sample where collected_at < ?`, cutoff)
	}
	return nil
}

func (r *HeartbeatRepository) AppendMetricSamples(ctx context.Context, items []hbdomain.MetricSample) error {
	if len(items) == 0 {
		return nil
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, item := range items {
		labelsJSON, marshalErr := json.Marshal(item.Labels)
		if marshalErr != nil {
			return marshalErr
		}
		valueJSON, marshalErr := json.Marshal(item.Value)
		if marshalErr != nil {
			return marshalErr
		}
		var numeric any
		if item.NumericValue != nil {
			numeric = *item.NumericValue
		}
		if _, err := tx.ExecContext(ctx, `
			insert into performance_metric_sample (
				sample_key, agent_id, machine_id, cluster_id, scope, category,
				metric_name, instance, labels_json, value_type, numeric_value,
				value_json, success, error, collected_at
			) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			on conflict(sample_key) do nothing
		`, metricSampleKey(item, string(labelsJSON)), item.AgentID, item.MachineID, item.ClusterID,
			item.Scope, item.Category, item.MetricName, item.Instance, string(labelsJSON),
			item.ValueType, numeric, string(valueJSON), boolInteger(item.Success), item.Error,
			item.CollectedAt.UTC().Format(time.RFC3339Nano)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r *HeartbeatRepository) ListMetricSamples(ctx context.Context, query hbdomain.MetricSampleQuery) ([]hbdomain.MetricSample, error) {
	limit := query.Limit
	if limit <= 0 || limit > 200000 {
		limit = 50000
	}
	var sqlText strings.Builder
	sqlText.WriteString(`
		select id, agent_id, machine_id, cluster_id, scope, category, metric_name,
			instance, labels_json, value_type, numeric_value, value_json, success,
			error, collected_at
		from performance_metric_sample
		where cluster_id = ? and metric_name = ? and collected_at >= ? and collected_at <= ?
	`)
	args := []any{query.ClusterID, query.Metric, query.StartAt.UTC().Format(time.RFC3339Nano), query.EndAt.UTC().Format(time.RFC3339Nano)}
	if query.MachineID != "" {
		sqlText.WriteString(" and machine_id = ?")
		args = append(args, query.MachineID)
	}
	if query.Instance != "" {
		sqlText.WriteString(" and instance = ?")
		args = append(args, query.Instance)
	}
	sqlText.WriteString(" order by collected_at asc limit ?")
	args = append(args, limit)
	rows, err := r.db.QueryContext(ctx, sqlText.String(), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]hbdomain.MetricSample, 0)
	for rows.Next() {
		var item hbdomain.MetricSample
		var labelsJSON, valueJSON, collectedAt string
		var numeric sql.NullFloat64
		var success int
		if err := rows.Scan(&item.ID, &item.AgentID, &item.MachineID, &item.ClusterID,
			&item.Scope, &item.Category, &item.MetricName, &item.Instance, &labelsJSON,
			&item.ValueType, &numeric, &valueJSON, &success, &item.Error, &collectedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(labelsJSON), &item.Labels)
		_ = json.Unmarshal([]byte(valueJSON), &item.Value)
		if numeric.Valid {
			value := numeric.Float64
			item.NumericValue = &value
		}
		item.Success = success != 0
		item.CollectedAt, _ = time.Parse(time.RFC3339Nano, collectedAt)
		out = append(out, item)
	}
	return out, rows.Err()
}

func metricSampleKey(item hbdomain.MetricSample, labelsJSON string) string {
	// Heartbeats can repeat a collector's most recent value until its own
	// interval elapses. A stable content identity makes persistence idempotent
	// without treating two genuinely distinct collection times as duplicates.
	source := strings.Join([]string{
		item.AgentID,
		item.MachineID,
		item.ClusterID,
		item.MetricName,
		item.Instance,
		item.ValueType,
		labelsJSON,
		item.CollectedAt.UTC().Format(time.RFC3339Nano),
	}, "\x00")
	sum := sha256.Sum256([]byte(source))
	return hex.EncodeToString(sum[:])
}

func boolInteger(value bool) int {
	if value {
		return 1
	}
	return 0
}

func (r *HeartbeatRepository) ListMetricSnapshots(ctx context.Context, clusterID string, since time.Time, limit int) ([]hbdomain.MetricSnapshot, error) {
	if limit <= 0 || limit > 20000 {
		limit = 10000
	}
	rows, err := r.db.QueryContext(ctx, `
		select id, agent_id, machine_id, cluster_id, metrics_json, collected_at
		from agent_metric_snapshot
		where cluster_id = ? and collected_at >= ?
		order by collected_at asc limit ?
	`, clusterID, since.UTC().Format(time.RFC3339Nano), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]hbdomain.MetricSnapshot, 0)
	for rows.Next() {
		var item hbdomain.MetricSnapshot
		var metricsJSON, collectedAt string
		if err := rows.Scan(&item.ID, &item.AgentID, &item.MachineID, &item.ClusterID, &metricsJSON, &collectedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(metricsJSON), &item.Metrics)
		item.CollectedAt, _ = time.Parse(time.RFC3339Nano, collectedAt)
		out = append(out, item)
	}
	return out, rows.Err()
}

func (r *HeartbeatRepository) ListMetricSnapshotsRange(ctx context.Context, clusterID string, start, end time.Time, limit int) ([]hbdomain.MetricSnapshot, error) {
	if limit <= 0 || limit > 20000 {
		limit = 10000
	}
	rows, err := r.db.QueryContext(ctx, `
		select id, agent_id, machine_id, cluster_id, metrics_json, collected_at
		from (
			select id, agent_id, machine_id, cluster_id, metrics_json, collected_at
			from agent_metric_snapshot
			where cluster_id = ? and collected_at >= ? and collected_at <= ?
			order by collected_at desc limit ?
		)
		order by collected_at asc
	`, clusterID, start.UTC().Format(time.RFC3339Nano), end.UTC().Format(time.RFC3339Nano), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]hbdomain.MetricSnapshot, 0)
	for rows.Next() {
		var item hbdomain.MetricSnapshot
		var metricsJSON, collectedAt string
		if err := rows.Scan(&item.ID, &item.AgentID, &item.MachineID, &item.ClusterID, &metricsJSON, &collectedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(metricsJSON), &item.Metrics)
		item.CollectedAt, _ = time.Parse(time.RFC3339Nano, collectedAt)
		out = append(out, item)
	}
	return out, rows.Err()
}

func (r *HeartbeatRepository) UpsertLatestStatus(ctx context.Context, item hbdomain.LatestStatus) error {
	checksJSON, err := json.Marshal(item.Checks)
	if err != nil {
		return err
	}
	metricsJSON, err := json.Marshal(item.Metrics)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `
		insert into agent_latest_status (
			agent_id, machine_id, cluster_id, hostname, version, current_state, overall_health,
			last_heartbeat_at, last_healthy_at, last_state_change_at, last_seq, last_boot_id,
			consecutive_misses, consecutive_bad_checks, last_error_summary, checks_json, metrics_json, updated_at
		) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		on conflict(agent_id) do update set
			machine_id = excluded.machine_id,
			cluster_id = excluded.cluster_id,
			hostname = excluded.hostname,
			version = excluded.version,
			current_state = excluded.current_state,
			overall_health = excluded.overall_health,
			last_heartbeat_at = excluded.last_heartbeat_at,
			last_healthy_at = excluded.last_healthy_at,
			last_state_change_at = excluded.last_state_change_at,
			last_seq = excluded.last_seq,
			last_boot_id = excluded.last_boot_id,
			consecutive_misses = excluded.consecutive_misses,
			consecutive_bad_checks = excluded.consecutive_bad_checks,
			last_error_summary = excluded.last_error_summary,
			checks_json = excluded.checks_json,
			metrics_json = excluded.metrics_json,
			updated_at = excluded.updated_at
	`,
		item.AgentID, item.MachineID, item.ClusterID, item.Hostname, item.Version, string(item.CurrentState), string(item.OverallHealth),
		item.LastHeartbeatAt.Format(time.RFC3339), formatNullableTime(item.LastHealthyAt), item.LastStateChangeAt.Format(time.RFC3339),
		item.LastSeq, item.LastBootID, item.ConsecutiveMisses, item.ConsecutiveBadChecks, item.LastErrorSummary, string(checksJSON), string(metricsJSON), item.UpdatedAt.Format(time.RFC3339),
	)
	return err
}

func (r *HeartbeatRepository) AppendEvent(ctx context.Context, item hbdomain.StateEvent) error {
	_, err := r.db.ExecContext(ctx, `
		insert into agent_event_log (
			id, agent_id, machine_id, event_type, prev_state, new_state, reason, heartbeat_seq, payload_json, created_at
		) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, item.ID, item.AgentID, item.MachineID, item.EventType, string(item.PrevState), string(item.NewState), item.Reason, item.HeartbeatSeq, item.PayloadJSON, item.CreatedAt.Format(time.RFC3339))
	return err
}

func (r *HeartbeatRepository) ListLatest(ctx context.Context) ([]hbdomain.LatestStatus, error) {
	rows, err := r.db.QueryContext(ctx, `
		select agent_id, machine_id, cluster_id, hostname, version, current_state, overall_health,
			last_heartbeat_at, last_healthy_at, last_state_change_at, last_seq, last_boot_id,
			consecutive_misses, consecutive_bad_checks, last_error_summary, checks_json, metrics_json, updated_at
		from agent_latest_status
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []hbdomain.LatestStatus
	for rows.Next() {
		var item hbdomain.LatestStatus
		var state, health, lastHeartbeatAt, lastHealthyAt, lastStateChangeAt, checksJSON, metricsJSON, updatedAt string
		if err := rows.Scan(
			&item.AgentID, &item.MachineID, &item.ClusterID, &item.Hostname, &item.Version,
			&state, &health, &lastHeartbeatAt, &lastHealthyAt, &lastStateChangeAt,
			&item.LastSeq, &item.LastBootID, &item.ConsecutiveMisses, &item.ConsecutiveBadChecks,
			&item.LastErrorSummary, &checksJSON, &metricsJSON, &updatedAt,
		); err != nil {
			return nil, err
		}
		item.CurrentState = hbdomain.AgentState(state)
		item.OverallHealth = hbdomain.HealthLevel(health)
		item.LastHeartbeatAt, _ = time.Parse(time.RFC3339, lastHeartbeatAt)
		if lastHealthyAt != "" {
			t, _ := time.Parse(time.RFC3339, lastHealthyAt)
			item.LastHealthyAt = &t
		}
		item.LastStateChangeAt, _ = time.Parse(time.RFC3339, lastStateChangeAt)
		item.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
		_ = json.Unmarshal([]byte(checksJSON), &item.Checks)
		_ = json.Unmarshal([]byte(metricsJSON), &item.Metrics)
		out = append(out, item)
	}
	return out, rows.Err()
}

func (r *HeartbeatRepository) DeleteLatestByMachineID(ctx context.Context, machineID string) error {
	_, err := r.db.ExecContext(ctx, `delete from agent_latest_status where machine_id = ?`, machineID)
	return err
}

func (r *HeartbeatRepository) DeleteByMachineID(ctx context.Context, machineID string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `delete from agent_latest_status where machine_id = ?`, machineID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `delete from agent_event_log where machine_id = ?`, machineID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `delete from agent_metric_snapshot where machine_id = ?`, machineID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `delete from performance_metric_sample where machine_id = ?`, machineID); err != nil {
		return err
	}
	return tx.Commit()
}
