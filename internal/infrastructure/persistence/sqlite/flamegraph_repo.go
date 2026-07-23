package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	flamegraphdomain "gmha/internal/domain/flamegraph"
)

type FlameGraphRepository struct{ db *DB }

func NewFlameGraphRepository(db *DB) *FlameGraphRepository {
	return &FlameGraphRepository{db: db}
}

func (r *FlameGraphRepository) Migrate() error {
	_, err := r.db.Exec(`
		create table if not exists flamegraph_profiles (
			id varchar(160) primary key, schedule_id varchar(160) not null default '', task_id varchar(160) not null default '',
			cluster_name varchar(255) not null default '', machine_id varchar(160) not null,
			target_type varchar(32) not null, target_value varchar(255) not null default '',
			duration_seconds integer not null, frequency_hz integer not null,
			requested_backend varchar(32) not null default 'auto', backend varchar(32) not null default '',
			status varchar(32) not null, sample_count integer not null default 0, stack_count integer not null default 0,
			folded_stacks text not null, error text not null,
			created_at varchar(64) not null, started_at varchar(64) not null default '', finished_at varchar(64) not null default ''
		);
		create index if not exists idx_flamegraph_profiles_cluster on flamegraph_profiles(cluster_name, created_at);
		create index if not exists idx_flamegraph_profiles_task on flamegraph_profiles(task_id);
		create table if not exists flamegraph_schedules (
			id varchar(160) primary key, name varchar(255) not null, cluster_name varchar(255) not null default '',
			machine_id varchar(160) not null, target_type varchar(32) not null, target_value varchar(255) not null default '',
			duration_seconds integer not null, frequency_hz integer not null, backend varchar(32) not null default 'auto',
			schedule_type varchar(32) not null, interval_minutes integer not null default 0,
			start_at varchar(64) not null, enabled integer not null default 1,
			last_run_at varchar(64) not null default '', next_run_at varchar(64) not null default '',
			created_at varchar(64) not null, updated_at varchar(64) not null
		);
		create index if not exists idx_flamegraph_schedule_due on flamegraph_schedules(enabled, next_run_at);
		create index if not exists idx_flamegraph_schedule_cluster on flamegraph_schedules(cluster_name);
	`)
	return err
}

func (r *FlameGraphRepository) CreateProfile(ctx context.Context, p flamegraphdomain.Profile) error {
	_, err := r.db.ExecContext(ctx, `insert into flamegraph_profiles
		(id,schedule_id,task_id,cluster_name,machine_id,target_type,target_value,duration_seconds,frequency_hz,requested_backend,backend,status,sample_count,stack_count,folded_stacks,error,created_at,started_at,finished_at)
		values(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		p.ID, p.ScheduleID, p.TaskID, p.Cluster, p.MachineID, p.TargetType, p.Target, p.DurationSec, p.FrequencyHz,
		p.RequestedTool, p.Backend, p.Status, p.SampleCount, p.StackCount, p.FoldedStacks, p.Error,
		formatFlameGraphTime(p.CreatedAt), formatFlameGraphTimePtr(p.StartedAt), formatFlameGraphTimePtr(p.FinishedAt))
	return err
}

func (r *FlameGraphRepository) AttachProfileTask(ctx context.Context, id, taskID string) error {
	_, err := r.db.ExecContext(ctx, `update flamegraph_profiles set task_id=? where id=?`, taskID, id)
	return err
}

func (r *FlameGraphRepository) CompleteProfile(ctx context.Context, id, status, backend string, samples int64, stacks int, folded, failure string, started, finished time.Time) error {
	_, err := r.db.ExecContext(ctx, `update flamegraph_profiles set status=?,backend=?,sample_count=?,stack_count=?,folded_stacks=?,error=?,
		started_at=case when started_at='' then ? else started_at end,finished_at=? where id=?`,
		status, backend, samples, stacks, folded, failure, formatFlameGraphTime(started), formatFlameGraphTime(finished), id)
	return err
}

func (r *FlameGraphRepository) GetProfile(ctx context.Context, id string) (flamegraphdomain.Profile, bool, error) {
	p, err := scanFlameGraphProfile(r.db.QueryRowContext(ctx, flameGraphProfileSelect+` where id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return flamegraphdomain.Profile{}, false, nil
	}
	return p, err == nil, err
}

func (r *FlameGraphRepository) ListProfiles(ctx context.Context, cluster string, limit int) ([]flamegraphdomain.Profile, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := r.db.QueryContext(ctx, flameGraphProfileListSelect+` where (?='' or cluster_name=?) order by created_at desc limit ?`, cluster, cluster, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]flamegraphdomain.Profile, 0)
	for rows.Next() {
		p, err := scanFlameGraphProfile(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *FlameGraphRepository) DeleteProfile(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, `delete from flamegraph_profiles where id=?`, id)
	return err
}

func (r *FlameGraphRepository) SaveSchedule(ctx context.Context, s flamegraphdomain.Schedule) error {
	_, err := r.db.ExecContext(ctx, `insert into flamegraph_schedules
		(id,name,cluster_name,machine_id,target_type,target_value,duration_seconds,frequency_hz,backend,schedule_type,interval_minutes,start_at,enabled,last_run_at,next_run_at,created_at,updated_at)
		values(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		on conflict(id) do update set name=excluded.name,cluster_name=excluded.cluster_name,machine_id=excluded.machine_id,
		target_type=excluded.target_type,target_value=excluded.target_value,duration_seconds=excluded.duration_seconds,
		frequency_hz=excluded.frequency_hz,backend=excluded.backend,schedule_type=excluded.schedule_type,
		interval_minutes=excluded.interval_minutes,start_at=excluded.start_at,enabled=excluded.enabled,
		next_run_at=excluded.next_run_at,updated_at=excluded.updated_at`,
		s.ID, s.Name, s.Cluster, s.MachineID, s.TargetType, s.Target, s.DurationSec, s.FrequencyHz, s.Backend,
		s.ScheduleType, s.IntervalMinutes, formatFlameGraphTime(s.StartAt), flameGraphBool(s.Enabled),
		formatFlameGraphTime(s.LastRunAt), formatFlameGraphTime(s.NextRunAt), formatFlameGraphTime(s.CreatedAt), formatFlameGraphTime(s.UpdatedAt))
	return err
}

func (r *FlameGraphRepository) GetSchedule(ctx context.Context, id string) (flamegraphdomain.Schedule, bool, error) {
	s, err := scanFlameGraphSchedule(r.db.QueryRowContext(ctx, flameGraphScheduleSelect+` where id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return flamegraphdomain.Schedule{}, false, nil
	}
	return s, err == nil, err
}

func (r *FlameGraphRepository) ListSchedules(ctx context.Context, cluster string) ([]flamegraphdomain.Schedule, error) {
	rows, err := r.db.QueryContext(ctx, flameGraphScheduleSelect+` where (?='' or cluster_name=?) order by created_at desc`, cluster, cluster)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]flamegraphdomain.Schedule, 0)
	for rows.Next() {
		s, err := scanFlameGraphSchedule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *FlameGraphRepository) ListDueSchedules(ctx context.Context, now time.Time) ([]flamegraphdomain.Schedule, error) {
	rows, err := r.db.QueryContext(ctx, flameGraphScheduleSelect+` where enabled=1 and next_run_at<>'' and next_run_at<=? order by next_run_at`, formatFlameGraphTime(now))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]flamegraphdomain.Schedule, 0)
	for rows.Next() {
		s, err := scanFlameGraphSchedule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *FlameGraphRepository) UpdateScheduleRun(ctx context.Context, id string, last, next time.Time, enabled bool) error {
	_, err := r.db.ExecContext(ctx, `update flamegraph_schedules set last_run_at=?,next_run_at=?,enabled=?,updated_at=? where id=?`,
		formatFlameGraphTime(last), formatFlameGraphTime(next), flameGraphBool(enabled), formatFlameGraphTime(time.Now().UTC()), id)
	return err
}

func (r *FlameGraphRepository) DeleteSchedule(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, `delete from flamegraph_schedules where id=?`, id)
	return err
}

const flameGraphProfileSelect = `select id,schedule_id,task_id,cluster_name,machine_id,target_type,target_value,duration_seconds,frequency_hz,requested_backend,backend,status,sample_count,stack_count,folded_stacks,error,created_at,started_at,finished_at from flamegraph_profiles`
const flameGraphProfileListSelect = `select id,schedule_id,task_id,cluster_name,machine_id,target_type,target_value,duration_seconds,frequency_hz,requested_backend,backend,status,sample_count,stack_count,'' as folded_stacks,error,created_at,started_at,finished_at from flamegraph_profiles`
const flameGraphScheduleSelect = `select id,name,cluster_name,machine_id,target_type,target_value,duration_seconds,frequency_hz,backend,schedule_type,interval_minutes,start_at,enabled,last_run_at,next_run_at,created_at,updated_at from flamegraph_schedules`

func scanFlameGraphProfile(row interface{ Scan(...any) error }) (flamegraphdomain.Profile, error) {
	var p flamegraphdomain.Profile
	var created, started, finished string
	err := row.Scan(&p.ID, &p.ScheduleID, &p.TaskID, &p.Cluster, &p.MachineID, &p.TargetType, &p.Target,
		&p.DurationSec, &p.FrequencyHz, &p.RequestedTool, &p.Backend, &p.Status, &p.SampleCount, &p.StackCount,
		&p.FoldedStacks, &p.Error, &created, &started, &finished)
	p.CreatedAt = parseFlameGraphTime(created)
	p.StartedAt = parseFlameGraphTimePtr(started)
	p.FinishedAt = parseFlameGraphTimePtr(finished)
	return p, err
}

func scanFlameGraphSchedule(row interface{ Scan(...any) error }) (flamegraphdomain.Schedule, error) {
	var s flamegraphdomain.Schedule
	var enabled int
	var start, last, next, created, updated string
	err := row.Scan(&s.ID, &s.Name, &s.Cluster, &s.MachineID, &s.TargetType, &s.Target, &s.DurationSec,
		&s.FrequencyHz, &s.Backend, &s.ScheduleType, &s.IntervalMinutes, &start, &enabled, &last, &next, &created, &updated)
	s.Enabled = enabled == 1
	s.StartAt = parseFlameGraphTime(start)
	s.LastRunAt = parseFlameGraphTime(last)
	s.NextRunAt = parseFlameGraphTime(next)
	s.CreatedAt = parseFlameGraphTime(created)
	s.UpdatedAt = parseFlameGraphTime(updated)
	return s, err
}

func flameGraphBool(value bool) int {
	if value {
		return 1
	}
	return 0
}

func formatFlameGraphTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func formatFlameGraphTimePtr(value *time.Time) string {
	if value == nil {
		return ""
	}
	return formatFlameGraphTime(*value)
}

func parseFlameGraphTime(value string) time.Time {
	result, _ := time.Parse(time.RFC3339Nano, value)
	return result
}

func parseFlameGraphTimePtr(value string) *time.Time {
	result := parseFlameGraphTime(value)
	if result.IsZero() {
		return nil
	}
	return &result
}
