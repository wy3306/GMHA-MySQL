package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	mysqlapp "gmha/internal/mysql"
)

// MySQLInstanceRepository 是 MySQL 实例实体的 SQLite 仓储实现。
type MySQLInstanceRepository struct {
	db *DB
}

func NewMySQLInstanceRepository(db *DB) *MySQLInstanceRepository {
	return &MySQLInstanceRepository{db: db}
}

func (r *MySQLInstanceRepository) Migrate() error {
	_, err := r.db.Exec(`
		create table if not exists mysql_instances (
			machine_id text not null,
			port integer not null,
			server_id integer not null default 1,
			mysql_user text not null default 'mysql',
			instance_dir text not null default '',
			data_dir text not null,
			binlog_dir text not null default '',
			redo_dir text not null default '',
			undo_dir text not null default '',
			tmp_dir text not null default '',
			base_dir text not null,
			profile text not null default '',
			package_name text not null default '',
			version text not null default '',
			architecture text not null default '',
			systemd_unit text not null default '',
			my_cnf_path text not null default '',
			socket_path text not null default '',
			status text not null,
			last_task_id text not null default '',
			updated_at text not null,
			primary key(machine_id, port)
		);
		create index if not exists idx_mysql_instances_status on mysql_instances(status);
	`)
	if err != nil {
		return err
	}
	for _, stmt := range []string{
		`alter table mysql_instances add column server_id integer not null default 1`,
		`alter table mysql_instances add column mysql_user text not null default 'mysql'`,
		`alter table mysql_instances add column instance_dir text not null default ''`,
		`alter table mysql_instances add column binlog_dir text not null default ''`,
		`alter table mysql_instances add column redo_dir text not null default ''`,
		`alter table mysql_instances add column undo_dir text not null default ''`,
		`alter table mysql_instances add column tmp_dir text not null default ''`,
		`alter table mysql_instances add column version text not null default ''`,
		`alter table mysql_instances add column architecture text not null default ''`,
	} {
		_, _ = r.db.Exec(stmt)
	}
	return nil
}

func (r *MySQLInstanceRepository) Save(ctx context.Context, item mysqlapp.Instance) error {
	if item.UpdatedAt.IsZero() {
		item.UpdatedAt = time.Now().UTC()
	}
	_, err := r.db.ExecContext(ctx, `
		insert into mysql_instances (
			machine_id, port, server_id, mysql_user, instance_dir, data_dir, binlog_dir, redo_dir, undo_dir, tmp_dir,
			base_dir, profile, package_name, version, architecture, systemd_unit, my_cnf_path, socket_path, status, last_task_id, updated_at
		) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		on conflict(machine_id, port) do update set
			server_id = excluded.server_id,
			mysql_user = excluded.mysql_user,
			instance_dir = excluded.instance_dir,
			data_dir = excluded.data_dir,
			binlog_dir = excluded.binlog_dir,
			redo_dir = excluded.redo_dir,
			undo_dir = excluded.undo_dir,
			tmp_dir = excluded.tmp_dir,
			base_dir = excluded.base_dir,
			profile = excluded.profile,
			package_name = excluded.package_name,
			version = excluded.version,
			architecture = excluded.architecture,
			systemd_unit = excluded.systemd_unit,
			my_cnf_path = excluded.my_cnf_path,
			socket_path = excluded.socket_path,
			status = excluded.status,
			last_task_id = excluded.last_task_id,
			updated_at = excluded.updated_at
	`, item.MachineID, item.Port, item.ServerID, item.MySQLUser, item.InstanceDir, item.DataDir, item.BinlogDir, item.RedoDir, item.UndoDir, item.TmpDir, item.BaseDir, item.Profile, item.PackageName, item.Version, item.Architecture, item.SystemdUnit, item.MyCnfPath, item.SocketPath, item.Status, item.LastTaskID, item.UpdatedAt.UTC().Format(time.RFC3339))
	return err
}

func (r *MySQLInstanceRepository) List(ctx context.Context) ([]mysqlapp.Instance, error) {
	rows, err := r.db.QueryContext(ctx, `
		select machine_id, port, server_id, mysql_user, instance_dir, data_dir, binlog_dir, redo_dir, undo_dir, tmp_dir,
			base_dir, profile, package_name, version, architecture, systemd_unit, my_cnf_path, socket_path, status, last_task_id, updated_at
		from mysql_instances order by updated_at desc
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]mysqlapp.Instance, 0)
	for rows.Next() {
		item, err := scanMySQLInstance(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (r *MySQLInstanceRepository) Get(ctx context.Context, machineID string, port int) (mysqlapp.Instance, bool, error) {
	row := r.db.QueryRowContext(ctx, `
		select machine_id, port, server_id, mysql_user, instance_dir, data_dir, binlog_dir, redo_dir, undo_dir, tmp_dir,
			base_dir, profile, package_name, version, architecture, systemd_unit, my_cnf_path, socket_path, status, last_task_id, updated_at
		from mysql_instances where machine_id = ? and port = ?
	`, machineID, port)
	item, err := scanMySQLInstance(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return mysqlapp.Instance{}, false, nil
		}
		return mysqlapp.Instance{}, false, err
	}
	return item, true, nil
}

func (r *MySQLInstanceRepository) Delete(ctx context.Context, machineID string, port int) error {
	_, err := r.db.ExecContext(ctx, `
		delete from mysql_instances where machine_id = ? and port = ?
	`, machineID, port)
	return err
}

func (r *MySQLInstanceRepository) DeleteByMachineID(ctx context.Context, machineID string) error {
	_, err := r.db.ExecContext(ctx, `delete from mysql_instances where machine_id = ?`, machineID)
	return err
}

func (r *MySQLInstanceRepository) UpdateStatus(ctx context.Context, machineID string, port int, status string) error {
	_, err := r.db.ExecContext(ctx, `
		update mysql_instances
		set status = ?, updated_at = ?
		where machine_id = ? and port = ?
	`, status, time.Now().UTC().Format(time.RFC3339), machineID, port)
	return err
}

func (r *MySQLInstanceRepository) PruneUninstalled(ctx context.Context) (int64, error) {
	result, err := r.db.ExecContext(ctx, `
		delete from mysql_instances
		where exists (
			select 1
			from tasks
			where tasks.type = 'mysql_uninstall'
			  and tasks.status = 'success'
			  and tasks.machine_id = mysql_instances.machine_id
			  and cast(json_extract(tasks.spec_json, '$.port') as integer) = mysql_instances.port
			  and coalesce(tasks.finished_at, tasks.created_at) >= mysql_instances.updated_at
		)
	`)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func scanMySQLInstance(scanner interface {
	Scan(dest ...any) error
}) (mysqlapp.Instance, error) {
	var item mysqlapp.Instance
	var updatedAt string
	if err := scanner.Scan(&item.MachineID, &item.Port, &item.ServerID, &item.MySQLUser, &item.InstanceDir, &item.DataDir, &item.BinlogDir, &item.RedoDir, &item.UndoDir, &item.TmpDir, &item.BaseDir, &item.Profile, &item.PackageName, &item.Version, &item.Architecture, &item.SystemdUnit, &item.MyCnfPath, &item.SocketPath, &item.Status, &item.LastTaskID, &updatedAt); err != nil {
		return mysqlapp.Instance{}, err
	}
	item.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	if item.Version == "" {
		item.Version, _ = mysqlapp.PackageVersion(item.PackageName)
	}
	if item.Architecture == "" {
		item.Architecture, _ = mysqlapp.PackageArchitecture(item.PackageName)
	}
	return item, nil
}
