package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	machinedomain "gmha/internal/domain/machine"
)

// MachineRepository 是机器实体的 SQLite 仓储实现。
type MachineRepository struct {
	db *DB
}

// NewMachineRepository 创建一个新的 MachineRepository 实例。
func NewMachineRepository(db *DB) *MachineRepository {
	return &MachineRepository{db: db}
}

func (r *MachineRepository) Migrate() error {
	_, err := r.db.Exec(`
		create table if not exists machines (
			id text primary key,
			name text not null,
			ip text not null,
			ssh_port integer not null,
			ssh_user text not null,
			credential_id text not null default '',
			cluster_name text not null default '',
			agent_install_dir text not null default '',
			status text not null,
			last_error text not null default '',
			created_at text not null,
			updated_at text not null
		);
		create unique index if not exists idx_machines_ip_port on machines(ip, ssh_port);
	`)
	if err != nil {
		return err
	}
	_, _ = r.db.Exec(`alter table machines add column agent_install_dir text not null default ''`)
	_, _ = r.db.Exec(`alter table machines add column credential_id text not null default ''`)
	return nil
}

func (r *MachineRepository) Save(ctx context.Context, machine machinedomain.Machine) (machinedomain.Machine, error) {
	now := time.Now().UTC()
	machine.CreatedAt = now
	machine.UpdatedAt = now
	_, err := r.db.ExecContext(ctx, `
		insert into machines (id, name, ip, ssh_port, ssh_user, credential_id, cluster_name, agent_install_dir, status, last_error, created_at, updated_at)
		values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		on conflict(id) do update set
			name = excluded.name,
			ip = excluded.ip,
			ssh_port = excluded.ssh_port,
			ssh_user = excluded.ssh_user,
			credential_id = excluded.credential_id,
			cluster_name = excluded.cluster_name,
			agent_install_dir = excluded.agent_install_dir,
			status = excluded.status,
			last_error = excluded.last_error,
			updated_at = excluded.updated_at
	`, machine.ID, machine.Name, machine.IP, machine.SSHPort, machine.SSHUser, machine.CredentialID, machine.Cluster, machine.AgentInstallDir, string(machine.Status), machine.LastError, now.Format(time.RFC3339), now.Format(time.RFC3339))
	if err != nil {
		return machinedomain.Machine{}, err
	}
	return r.findByID(ctx, machine.ID)
}

func (r *MachineRepository) UpdateStatus(ctx context.Context, machineID string, status machinedomain.Status, lastError string) error {
	_, err := r.db.ExecContext(ctx, `
		update machines
		set status = ?, last_error = ?, updated_at = ?
		where id = ?
	`, string(status), lastError, time.Now().UTC().Format(time.RFC3339), machineID)
	return err
}

func (r *MachineRepository) GetByID(ctx context.Context, machineID string) (machinedomain.Machine, bool, error) {
	row := r.db.QueryRowContext(ctx, `
		select id, name, ip, ssh_port, ssh_user, credential_id, cluster_name, agent_install_dir, status, last_error, created_at, updated_at
		from machines
		where id = ?
	`, machineID)
	item, err := scanMachine(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return machinedomain.Machine{}, false, nil
		}
		return machinedomain.Machine{}, false, err
	}
	return item, true, nil
}

func (r *MachineRepository) GetByIP(ctx context.Context, ip string) (machinedomain.Machine, bool, error) {
	row := r.db.QueryRowContext(ctx, `
		select id, name, ip, ssh_port, ssh_user, credential_id, cluster_name, agent_install_dir, status, last_error, created_at, updated_at
		from machines
		where ip = ?
		order by created_at desc
		limit 1
	`, ip)
	item, err := scanMachine(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return machinedomain.Machine{}, false, nil
		}
		return machinedomain.Machine{}, false, err
	}
	return item, true, nil
}

func (r *MachineRepository) List(ctx context.Context) ([]machinedomain.Machine, error) {
	rows, err := r.db.QueryContext(ctx, `
		select id, name, ip, ssh_port, ssh_user, credential_id, cluster_name, agent_install_dir, status, last_error, created_at, updated_at
		from machines
		order by created_at desc
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []machinedomain.Machine
	for rows.Next() {
		item, err := scanMachine(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (r *MachineRepository) UpdateBasics(ctx context.Context, machine machinedomain.Machine) error {
	_, err := r.db.ExecContext(ctx, `
		update machines
		set name = ?, ip = ?, ssh_port = ?, ssh_user = ?, updated_at = ?
		where id = ?
	`, machine.Name, machine.IP, machine.SSHPort, machine.SSHUser, time.Now().UTC().Format(time.RFC3339), machine.ID)
	return err
}

func (r *MachineRepository) AssignCluster(ctx context.Context, machineID, clusterName string) error {
	_, err := r.db.ExecContext(ctx, `
		update machines
		set cluster_name = ?, updated_at = ?
		where id = ?
	`, clusterName, time.Now().UTC().Format(time.RFC3339), machineID)
	return err
}

func (r *MachineRepository) RebindCluster(ctx context.Context, oldName, newName string) error {
	_, err := r.db.ExecContext(ctx, `
		update machines
		set cluster_name = ?, updated_at = ?
		where cluster_name = ?
	`, newName, time.Now().UTC().Format(time.RFC3339), oldName)
	return err
}

func (r *MachineRepository) ClearCluster(ctx context.Context, clusterName string) error {
	_, err := r.db.ExecContext(ctx, `
		update machines
		set cluster_name = '', updated_at = ?
		where cluster_name = ?
	`, time.Now().UTC().Format(time.RFC3339), clusterName)
	return err
}

func (r *MachineRepository) Delete(ctx context.Context, machineID string) error {
	_, err := r.db.ExecContext(ctx, `delete from machines where id = ?`, machineID)
	return err
}

type rowScanner interface {
	Scan(dest ...any) error
}

func (r *MachineRepository) findByID(ctx context.Context, id string) (machinedomain.Machine, error) {
	row := r.db.QueryRowContext(ctx, `
		select id, name, ip, ssh_port, ssh_user, credential_id, cluster_name, agent_install_dir, status, last_error, created_at, updated_at
		from machines
		where id = ?
	`, id)
	return scanMachine(row)
}

func scanMachine(scanner rowScanner) (machinedomain.Machine, error) {
	var item machinedomain.Machine
	var createdAt string
	var updatedAt string
	var status string
	if err := scanner.Scan(&item.ID, &item.Name, &item.IP, &item.SSHPort, &item.SSHUser, &item.CredentialID, &item.Cluster, &item.AgentInstallDir, &status, &item.LastError, &createdAt, &updatedAt); err != nil {
		return machinedomain.Machine{}, err
	}
	item.Status = machinedomain.Status(status)
	item.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	item.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	return item, nil
}
