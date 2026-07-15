package sqlite

import (
	"context"
	"encoding/json"
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
	`)
	_, _ = r.db.Exec(`alter table agent_latest_status add column metrics_json text not null default '[]'`)
	return err
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
	return tx.Commit()
}
