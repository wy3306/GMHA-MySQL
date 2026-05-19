package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	recoverydomain "gmha/internal/domain/recovery"
)

// RecoveryRepository 是自动恢复实体的 SQLite 仓储实现。
type RecoveryRepository struct {
	db *sql.DB
}

func NewRecoveryRepository(db *sql.DB) *RecoveryRepository {
	return &RecoveryRepository{db: db}
}

func (r *RecoveryRepository) Migrate() error {
	_, err := r.db.Exec(`
		create table if not exists recovery_tasks (
			id text primary key,
			agent_id text not null default '',
			machine_id text not null,
			machine_ip text not null,
			status text not null,
			trigger_type text not null,
			action text not null default '',
			reason text not null default '',
			attempt integer not null default 0,
			max_attempts integer not null default 3,
			heartbeat_deadline text,
			last_error text not null default '',
			last_ssh_output text not null default '',
			suppressed_until text,
			created_at text not null,
			updated_at text not null
		);
		create index if not exists idx_recovery_tasks_machine_time on recovery_tasks(machine_id, created_at desc);
		create table if not exists recovery_latest_state (
			machine_id text primary key,
			in_progress integer not null default 0,
			last_attempt_at text,
			last_success_at text,
			consecutive_failures integer not null default 0,
			suppressed_until text,
			last_task_id text not null default '',
			last_result text not null default '',
			lock_until text,
			updated_at text not null
		);
	`)
	return err
}

func (r *RecoveryRepository) CreateTask(ctx context.Context, task recoverydomain.Task) (recoverydomain.Task, error) {
	now := time.Now().UTC()
	if task.CreatedAt.IsZero() {
		task.CreatedAt = now
	}
	task.UpdatedAt = now
	_, err := r.db.ExecContext(ctx, `
		insert into recovery_tasks (
			id, agent_id, machine_id, machine_ip, status, trigger_type, action, reason, attempt, max_attempts,
			heartbeat_deadline, last_error, last_ssh_output, suppressed_until, created_at, updated_at
		) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, task.ID, task.AgentID, task.MachineID, task.MachineIP, string(task.Status), string(task.Trigger), string(task.Action), task.Reason, task.Attempt, task.MaxAttempts, formatNullableTime(task.HeartbeatDeadline), task.LastError, task.LastSSHOutput, formatNullableTime(task.SuppressedUntil), task.CreatedAt.Format(time.RFC3339), task.UpdatedAt.Format(time.RFC3339))
	if err != nil {
		return recoverydomain.Task{}, err
	}
	return task, nil
}

func (r *RecoveryRepository) UpdateTask(ctx context.Context, task recoverydomain.Task) error {
	task.UpdatedAt = time.Now().UTC()
	_, err := r.db.ExecContext(ctx, `
		update recovery_tasks
		set status = ?, trigger_type = ?, action = ?, reason = ?, attempt = ?, max_attempts = ?,
			heartbeat_deadline = ?, last_error = ?, last_ssh_output = ?, suppressed_until = ?, updated_at = ?
		where id = ?
	`, string(task.Status), string(task.Trigger), string(task.Action), task.Reason, task.Attempt, task.MaxAttempts, formatNullableTime(task.HeartbeatDeadline), task.LastError, task.LastSSHOutput, formatNullableTime(task.SuppressedUntil), task.UpdatedAt.Format(time.RFC3339), task.ID)
	return err
}

func (r *RecoveryRepository) ListRecent(ctx context.Context, limit int) ([]recoverydomain.Task, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := r.db.QueryContext(ctx, `
		select id, agent_id, machine_id, machine_ip, status, trigger_type, action, reason, attempt, max_attempts,
			heartbeat_deadline, last_error, last_ssh_output, suppressed_until, created_at, updated_at
		from recovery_tasks
		order by created_at desc
		limit ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []recoverydomain.Task
	for rows.Next() {
		item, err := scanRecoveryTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (r *RecoveryRepository) GetLatestState(ctx context.Context, machineID string) (recoverydomain.LatestState, bool, error) {
	row := r.db.QueryRowContext(ctx, `
		select machine_id, in_progress, last_attempt_at, last_success_at, consecutive_failures,
			suppressed_until, last_task_id, last_result, updated_at
		from recovery_latest_state where machine_id = ?
	`, machineID)
	var item recoverydomain.LatestState
	var inProgress int
	var lastAttemptAt, lastSuccessAt, suppressedUntil, updatedAt sql.NullString
	if err := row.Scan(&item.MachineID, &inProgress, &lastAttemptAt, &lastSuccessAt, &item.ConsecutiveFailures, &suppressedUntil, &item.LastTaskID, &item.LastResult, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return recoverydomain.LatestState{}, false, nil
		}
		return recoverydomain.LatestState{}, false, err
	}
	item.InProgress = inProgress == 1
	item.LastAttemptAt = parseNullableTime(lastAttemptAt)
	item.LastSuccessAt = parseNullableTime(lastSuccessAt)
	item.SuppressedUntil = parseNullableTime(suppressedUntil)
	if updatedAt.Valid {
		item.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt.String)
	}
	return item, true, nil
}

func (r *RecoveryRepository) ListLatestStates(ctx context.Context) ([]recoverydomain.LatestState, error) {
	rows, err := r.db.QueryContext(ctx, `
		select machine_id, in_progress, last_attempt_at, last_success_at, consecutive_failures,
			suppressed_until, last_task_id, last_result, updated_at
		from recovery_latest_state
		order by updated_at desc
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]recoverydomain.LatestState, 0)
	for rows.Next() {
		var item recoverydomain.LatestState
		var inProgress int
		var lastAttemptAt, lastSuccessAt, suppressedUntil, updatedAt sql.NullString
		if err := rows.Scan(&item.MachineID, &inProgress, &lastAttemptAt, &lastSuccessAt, &item.ConsecutiveFailures, &suppressedUntil, &item.LastTaskID, &item.LastResult, &updatedAt); err != nil {
			return nil, err
		}
		item.InProgress = inProgress == 1
		item.LastAttemptAt = parseNullableTime(lastAttemptAt)
		item.LastSuccessAt = parseNullableTime(lastSuccessAt)
		item.SuppressedUntil = parseNullableTime(suppressedUntil)
		if updatedAt.Valid {
			item.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt.String)
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (r *RecoveryRepository) SaveLatestState(ctx context.Context, state recoverydomain.LatestState) error {
	now := time.Now().UTC()
	state.UpdatedAt = now
	inProgress := 0
	if state.InProgress {
		inProgress = 1
	}
	_, err := r.db.ExecContext(ctx, `
		insert into recovery_latest_state (
			machine_id, in_progress, last_attempt_at, last_success_at, consecutive_failures,
			suppressed_until, last_task_id, last_result, updated_at
		) values (?, ?, ?, ?, ?, ?, ?, ?, ?)
		on conflict(machine_id) do update set
			in_progress = excluded.in_progress,
			last_attempt_at = excluded.last_attempt_at,
			last_success_at = excluded.last_success_at,
			consecutive_failures = excluded.consecutive_failures,
			suppressed_until = excluded.suppressed_until,
			last_task_id = excluded.last_task_id,
			last_result = excluded.last_result,
			updated_at = excluded.updated_at
	`, state.MachineID, inProgress, formatNullableTime(state.LastAttemptAt), formatNullableTime(state.LastSuccessAt), state.ConsecutiveFailures, formatNullableTime(state.SuppressedUntil), state.LastTaskID, state.LastResult, state.UpdatedAt.Format(time.RFC3339))
	return err
}

func (r *RecoveryRepository) TryAcquireLock(ctx context.Context, machineID string, lockUntil time.Time) (bool, error) {
	row := r.db.QueryRowContext(ctx, `select lock_until from recovery_latest_state where machine_id = ?`, machineID)
	var lockUntilText sql.NullString
	if err := row.Scan(&lockUntilText); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	if lockUntilText.Valid && lockUntilText.String != "" {
		current, err := time.Parse(time.RFC3339, lockUntilText.String)
		if err == nil && current.After(time.Now()) {
			return false, nil
		}
	}
	_, err := r.db.ExecContext(ctx, `
		insert into recovery_latest_state (machine_id, in_progress, lock_until, updated_at)
		values (?, 1, ?, ?)
		on conflict(machine_id) do update set
			in_progress = 1,
			lock_until = ?,
			updated_at = ?
	`, machineID, lockUntil.UTC().Format(time.RFC3339), time.Now().UTC().Format(time.RFC3339), lockUntil.UTC().Format(time.RFC3339), time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return false, err
	}
	return true, nil
}

func (r *RecoveryRepository) ReleaseLock(ctx context.Context, machineID string) error {
	_, err := r.db.ExecContext(ctx, `update recovery_latest_state set in_progress = 0, lock_until = null, updated_at = ? where machine_id = ?`, time.Now().UTC().Format(time.RFC3339), machineID)
	return err
}

func (r *RecoveryRepository) DeleteByMachineID(ctx context.Context, machineID string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `delete from recovery_tasks where machine_id = ?`, machineID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `delete from recovery_latest_state where machine_id = ?`, machineID); err != nil {
		return err
	}
	return tx.Commit()
}

type recoveryScanner interface {
	Scan(dest ...any) error
}

func scanRecoveryTask(scanner recoveryScanner) (recoverydomain.Task, error) {
	var item recoverydomain.Task
	var status, trigger, action, createdAt, updatedAt string
	var hbDeadline, suppressedUntil sql.NullString
	if err := scanner.Scan(&item.ID, &item.AgentID, &item.MachineID, &item.MachineIP, &status, &trigger, &action, &item.Reason, &item.Attempt, &item.MaxAttempts, &hbDeadline, &item.LastError, &item.LastSSHOutput, &suppressedUntil, &createdAt, &updatedAt); err != nil {
		return recoverydomain.Task{}, err
	}
	item.Status = recoverydomain.Status(status)
	item.Trigger = recoverydomain.Trigger(trigger)
	item.Action = recoverydomain.Action(action)
	item.HeartbeatDeadline = parseNullableTime(hbDeadline)
	item.SuppressedUntil = parseNullableTime(suppressedUntil)
	item.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	item.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	return item, nil
}
