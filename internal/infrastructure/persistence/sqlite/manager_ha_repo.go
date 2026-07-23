package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	managerdomain "gmha/internal/domain/manager"
)

type ManagerHARepository struct{ db *DB }

func NewManagerHARepository(db *DB) *ManagerHARepository { return &ManagerHARepository{db: db} }

func (r *ManagerHARepository) Migrate() error {
	if _, err := r.db.Exec(`
		create table if not exists manager_ha_config (
			id text primary key,
			enabled integer not null default 0,
			vip text not null default '',
			prefix integer not null default 24,
			interface_name text not null default '',
			install_dir text not null default '/opt/gmha',
			service_name text not null default 'gmha-manager',
			updated_at text not null
		);
	`); err != nil {
		return err
	}
	if _, err := r.db.Exec(`
		create table if not exists manager_nodes (
			id text primary key,
			machine_id text not null default '',
			name text not null,
			ip text not null,
			http_address text not null,
			grpc_address text not null,
			vip_interface text not null default '',
			role text not null default 'standby',
			status text not null default 'unknown',
			version text not null default '',
			last_seen_at text not null default '',
			last_error text not null default '',
			task_id text not null default '',
			created_at text not null,
			updated_at text not null
		);
		create index if not exists idx_manager_nodes_machine on manager_nodes(machine_id);
	`); err != nil {
		return err
	}
	if _, err := r.db.Exec(`alter table manager_nodes add column vip_interface text not null default ''`); err != nil {
		message := strings.ToLower(err.Error())
		if !strings.Contains(message, "duplicate column") && !strings.Contains(message, "already exists") {
			return err
		}
	}
	return nil
}

func (r *ManagerHARepository) GetConfig(ctx context.Context) (managerdomain.HAConfig, error) {
	var item managerdomain.HAConfig
	var enabled int
	var updated string
	err := r.db.QueryRowContext(ctx, `
		select enabled, vip, prefix, interface_name, install_dir, service_name, updated_at
		from manager_ha_config where id = ?
	`, "default").Scan(&enabled, &item.VIP, &item.Prefix, &item.Interface, &item.InstallDir, &item.ServiceName, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return managerdomain.HAConfig{Prefix: 24, InstallDir: "/opt/gmha", ServiceName: "gmha-manager"}, nil
	}
	item.Enabled = enabled != 0
	item.UpdatedAt, _ = time.Parse(time.RFC3339, updated)
	return item, err
}

func (r *ManagerHARepository) SaveConfig(ctx context.Context, item managerdomain.HAConfig) error {
	item.UpdatedAt = time.Now().UTC()
	_, err := r.db.ExecContext(ctx, `
		insert into manager_ha_config (id, enabled, vip, prefix, interface_name, install_dir, service_name, updated_at)
		values (?, ?, ?, ?, ?, ?, ?, ?)
		on conflict(id) do update set enabled=excluded.enabled, vip=excluded.vip, prefix=excluded.prefix,
			interface_name=excluded.interface_name, install_dir=excluded.install_dir,
			service_name=excluded.service_name, updated_at=excluded.updated_at
	`, "default", boolInt(item.Enabled), item.VIP, item.Prefix, item.Interface, item.InstallDir, item.ServiceName, item.UpdatedAt.Format(time.RFC3339))
	return err
}

func (r *ManagerHARepository) ListNodes(ctx context.Context) ([]managerdomain.Node, error) {
	rows, err := r.db.QueryContext(ctx, `
		select id, machine_id, name, ip, http_address, grpc_address, vip_interface, role, status, version,
			last_seen_at, last_error, task_id, created_at, updated_at
		from manager_nodes order by role asc, created_at asc
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []managerdomain.Node
	for rows.Next() {
		item, err := scanManagerNode(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *ManagerHARepository) GetNode(ctx context.Context, id string) (managerdomain.Node, bool, error) {
	row := r.db.QueryRowContext(ctx, `
		select id, machine_id, name, ip, http_address, grpc_address, vip_interface, role, status, version,
			last_seen_at, last_error, task_id, created_at, updated_at
		from manager_nodes where id = ?
	`, id)
	item, err := scanManagerNode(row)
	if errors.Is(err, sql.ErrNoRows) {
		return managerdomain.Node{}, false, nil
	}
	return item, err == nil, err
}

func (r *ManagerHARepository) SaveNode(ctx context.Context, item managerdomain.Node) error {
	now := time.Now().UTC()
	if item.CreatedAt.IsZero() {
		item.CreatedAt = now
	}
	item.UpdatedAt = now
	_, err := r.db.ExecContext(ctx, `
		insert into manager_nodes (id, machine_id, name, ip, http_address, grpc_address, vip_interface, role, status,
			version, last_seen_at, last_error, task_id, created_at, updated_at)
		values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		on conflict(id) do update set machine_id=excluded.machine_id, name=excluded.name, ip=excluded.ip,
			http_address=excluded.http_address, grpc_address=excluded.grpc_address, vip_interface=excluded.vip_interface, role=excluded.role,
			status=excluded.status, version=excluded.version, last_seen_at=excluded.last_seen_at,
			last_error=excluded.last_error, task_id=excluded.task_id, updated_at=excluded.updated_at
	`, item.ID, item.MachineID, item.Name, item.IP, item.HTTPAddress, item.GRPCAddress, item.VIPInterface, item.Role, item.State,
		item.Version, formatManagerTime(item.LastSeenAt), item.LastError, item.TaskID,
		item.CreatedAt.Format(time.RFC3339), item.UpdatedAt.Format(time.RFC3339))
	return err
}

func (r *ManagerHARepository) DeleteNode(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, `delete from manager_nodes where id = ?`, id)
	return err
}

func (r *ManagerHARepository) SetActive(ctx context.Context, id string, now time.Time) error {
	if _, err := r.db.ExecContext(ctx, `update manager_nodes set role = 'standby', updated_at = ? where id <> ?`, now.Format(time.RFC3339), id); err != nil {
		return err
	}
	_, err := r.db.ExecContext(ctx, `update manager_nodes set role = 'active', status = 'online', last_seen_at = ?, updated_at = ? where id = ?`, now.Format(time.RFC3339), now.Format(time.RFC3339), id)
	return err
}

type managerNodeScanner interface{ Scan(...any) error }

func scanManagerNode(scanner managerNodeScanner) (managerdomain.Node, error) {
	var item managerdomain.Node
	var lastSeen, created, updated string
	err := scanner.Scan(&item.ID, &item.MachineID, &item.Name, &item.IP, &item.HTTPAddress, &item.GRPCAddress,
		&item.VIPInterface, &item.Role, &item.State, &item.Version, &lastSeen, &item.LastError, &item.TaskID, &created, &updated)
	item.LastSeenAt, _ = time.Parse(time.RFC3339, lastSeen)
	item.CreatedAt, _ = time.Parse(time.RFC3339, created)
	item.UpdatedAt, _ = time.Parse(time.RFC3339, updated)
	return item, err
}

func formatManagerTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}
