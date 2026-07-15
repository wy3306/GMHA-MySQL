package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	backupdomain "gmha/internal/domain/backup"
)

type BackupRepository struct{ db *DB }

func NewBackupRepository(db *DB) *BackupRepository { return &BackupRepository{db: db} }

func (r *BackupRepository) Migrate() error {
	_, err := r.db.Exec(`
		create table if not exists backup_policies (
			id varchar(160) primary key, name varchar(255) not null, cluster_name varchar(255) not null, machine_id varchar(160) not null,
			port integer not null, backup_type varchar(32) not null default 'full', disk_usage_threshold integer not null default 95,
			schedule_type varchar(32) not null, weekdays text not null, weekday_backup_types text not null,
			interval_minutes integer not null default 0, start_at varchar(64) not null,
			retry_count integer not null default 0, retry_interval_seconds integer not null default 60,
			include_binlog integer not null default 0, backup_location text not null,
			mysql_user varchar(255) not null, mysql_password text not null, enabled integer not null default 1,
			last_run_at varchar(64) not null default '', next_run_at varchar(64) not null default '',
			created_at varchar(64) not null, updated_at varchar(64) not null
		);
		create index if not exists idx_backup_policy_due on backup_policies(enabled, next_run_at);
		create index if not exists idx_backup_policy_cluster on backup_policies(cluster_name);
		create table if not exists backup_runs (
			id varchar(160) primary key, policy_id varchar(160) not null, cluster_name varchar(255) not null, machine_id varchar(160) not null,
			port integer not null, backup_type varchar(32) not null default 'full', base_run_id varchar(160) not null default '',
			backup_path text not null, task_id varchar(160) not null, status varchar(32) not null,
			include_binlog integer not null default 0, restore_task_id varchar(160) not null default '', created_at varchar(64) not null
		);
		create index if not exists idx_backup_run_cluster on backup_runs(cluster_name, created_at);
	`)
	if err != nil {
		return err
	}
	for _, stmt := range []string{
		`alter table backup_policies add column backup_type varchar(32) not null default 'full'`,
		`alter table backup_policies add column disk_usage_threshold integer not null default 95`,
		`alter table backup_policies add column weekday_backup_types text not null default '{}'`,
		`alter table backup_runs add column backup_type varchar(32) not null default 'full'`,
		`alter table backup_runs add column base_run_id varchar(160) not null default ''`,
	} {
		_, _ = r.db.Exec(stmt)
	}
	return nil
}

func (r *BackupRepository) SavePolicy(ctx context.Context, p backupdomain.Policy) error {
	weekdays, _ := json.Marshal(p.Weekdays)
	weekdayTypes, _ := json.Marshal(p.WeekdayBackupTypes)
	_, err := r.db.ExecContext(ctx, `insert into backup_policies
		(id,name,cluster_name,machine_id,port,backup_type,disk_usage_threshold,schedule_type,weekdays,weekday_backup_types,interval_minutes,start_at,retry_count,retry_interval_seconds,include_binlog,backup_location,mysql_user,mysql_password,enabled,last_run_at,next_run_at,created_at,updated_at)
		values (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		on conflict(id) do update set name=excluded.name, cluster_name=excluded.cluster_name, machine_id=excluded.machine_id,
		port=excluded.port, backup_type=excluded.backup_type, disk_usage_threshold=excluded.disk_usage_threshold,
		schedule_type=excluded.schedule_type, weekdays=excluded.weekdays, weekday_backup_types=excluded.weekday_backup_types, interval_minutes=excluded.interval_minutes,
		start_at=excluded.start_at, retry_count=excluded.retry_count, retry_interval_seconds=excluded.retry_interval_seconds,
		include_binlog=excluded.include_binlog, backup_location=excluded.backup_location, mysql_user=excluded.mysql_user,
		mysql_password=excluded.mysql_password, enabled=excluded.enabled, next_run_at=excluded.next_run_at, updated_at=excluded.updated_at`,
		p.ID, p.Name, p.Cluster, p.MachineID, p.Port, p.BackupType, p.DiskUsageThreshold, p.ScheduleType, string(weekdays), string(weekdayTypes), p.IntervalMinutes, formatBackupTime(p.StartAt), p.RetryCount,
		p.RetryIntervalSeconds, backupBoolInt(p.IncludeBinlog), p.BackupLocation, p.MySQLUser, p.MySQLPassword, backupBoolInt(p.Enabled), formatBackupTime(p.LastRunAt), formatBackupTime(p.NextRunAt), formatBackupTime(p.CreatedAt), formatBackupTime(p.UpdatedAt))
	return err
}

func (r *BackupRepository) GetPolicy(ctx context.Context, id string) (backupdomain.Policy, bool, error) {
	p, err := scanBackupPolicy(r.db.QueryRowContext(ctx, policySelect+` where id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return backupdomain.Policy{}, false, nil
	}
	return p, err == nil, err
}

func (r *BackupRepository) ListPolicies(ctx context.Context, cluster string) ([]backupdomain.Policy, error) {
	rows, err := r.db.QueryContext(ctx, policySelect+` where (?='' or cluster_name=?) order by created_at desc`, cluster, cluster)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []backupdomain.Policy
	for rows.Next() {
		p, err := scanBackupPolicy(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *BackupRepository) ListDuePolicies(ctx context.Context, now time.Time) ([]backupdomain.Policy, error) {
	rows, err := r.db.QueryContext(ctx, policySelect+` where enabled=1 and next_run_at<>'' and next_run_at<=? order by next_run_at`, formatBackupTime(now))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []backupdomain.Policy
	for rows.Next() {
		p, err := scanBackupPolicy(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *BackupRepository) UpdatePolicySchedule(ctx context.Context, id string, last, next time.Time, enabled bool) error {
	_, err := r.db.ExecContext(ctx, `update backup_policies set last_run_at=?,next_run_at=?,enabled=?,updated_at=? where id=?`, formatBackupTime(last), formatBackupTime(next), backupBoolInt(enabled), formatBackupTime(time.Now().UTC()), id)
	return err
}

func (r *BackupRepository) DeletePolicy(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, `delete from backup_policies where id=?`, id)
	return err
}

func (r *BackupRepository) SaveRun(ctx context.Context, run backupdomain.Run) error {
	_, err := r.db.ExecContext(ctx, `insert into backup_runs(id,policy_id,cluster_name,machine_id,port,backup_type,base_run_id,backup_path,task_id,status,include_binlog,restore_task_id,created_at) values(?,?,?,?,?,?,?,?,?,?,?,?,?)`, run.ID, run.PolicyID, run.Cluster, run.MachineID, run.Port, run.BackupType, run.BaseRunID, run.BackupPath, run.TaskID, run.Status, backupBoolInt(run.IncludeBinlog), run.RestoreTaskID, formatBackupTime(run.CreatedAt))
	return err
}

func (r *BackupRepository) GetRun(ctx context.Context, id string) (backupdomain.Run, bool, error) {
	run, err := scanBackupRun(r.db.QueryRowContext(ctx, runSelect+` where id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return backupdomain.Run{}, false, nil
	}
	return run, err == nil, err
}

func (r *BackupRepository) ListRuns(ctx context.Context, cluster string, limit int) ([]backupdomain.Run, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := r.db.QueryContext(ctx, runSelect+` where (?='' or cluster_name=?) order by created_at desc limit ?`, cluster, cluster, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []backupdomain.Run
	for rows.Next() {
		run, err := scanBackupRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, run)
	}
	return out, rows.Err()
}

func (r *BackupRepository) SetRestoreTask(ctx context.Context, id, taskID string) error {
	_, err := r.db.ExecContext(ctx, `update backup_runs set restore_task_id=? where id=?`, taskID, id)
	return err
}

const policySelect = `select id,name,cluster_name,machine_id,port,backup_type,disk_usage_threshold,schedule_type,weekdays,weekday_backup_types,interval_minutes,start_at,retry_count,retry_interval_seconds,include_binlog,backup_location,mysql_user,mysql_password,enabled,last_run_at,next_run_at,created_at,updated_at from backup_policies`
const runSelect = `select id,policy_id,cluster_name,machine_id,port,backup_type,base_run_id,backup_path,task_id,status,include_binlog,restore_task_id,created_at from backup_runs`

func scanBackupPolicy(s interface{ Scan(...any) error }) (backupdomain.Policy, error) {
	var p backupdomain.Policy
	var weekdays, weekdayTypes, start, last, next, created, updated string
	var binlog, enabled int
	err := s.Scan(&p.ID, &p.Name, &p.Cluster, &p.MachineID, &p.Port, &p.BackupType, &p.DiskUsageThreshold, &p.ScheduleType, &weekdays, &weekdayTypes, &p.IntervalMinutes, &start, &p.RetryCount, &p.RetryIntervalSeconds, &binlog, &p.BackupLocation, &p.MySQLUser, &p.MySQLPassword, &enabled, &last, &next, &created, &updated)
	if err != nil {
		return p, err
	}
	_ = json.Unmarshal([]byte(weekdays), &p.Weekdays)
	_ = json.Unmarshal([]byte(weekdayTypes), &p.WeekdayBackupTypes)
	p.IncludeBinlog = binlog == 1
	p.Enabled = enabled == 1
	p.StartAt = parseBackupTime(start)
	p.LastRunAt = parseBackupTime(last)
	p.NextRunAt = parseBackupTime(next)
	p.CreatedAt = parseBackupTime(created)
	p.UpdatedAt = parseBackupTime(updated)
	return p, nil
}
func scanBackupRun(s interface{ Scan(...any) error }) (backupdomain.Run, error) {
	var r backupdomain.Run
	var binlog int
	var created string
	err := s.Scan(&r.ID, &r.PolicyID, &r.Cluster, &r.MachineID, &r.Port, &r.BackupType, &r.BaseRunID, &r.BackupPath, &r.TaskID, &r.Status, &binlog, &r.RestoreTaskID, &created)
	r.IncludeBinlog = binlog == 1
	r.CreatedAt = parseBackupTime(created)
	return r, err
}
func backupBoolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
func formatBackupTime(v time.Time) string {
	if v.IsZero() {
		return ""
	}
	return v.UTC().Format(time.RFC3339)
}
func parseBackupTime(v string) time.Time { t, _ := time.Parse(time.RFC3339, v); return t }
