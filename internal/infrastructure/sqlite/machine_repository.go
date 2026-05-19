package sqlite

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"time"

	"gmha/internal/domain/machine"
	_ "modernc.org/sqlite"
)

type MachineRepository struct {
	db *sql.DB
}

func NewMachineRepository(dbPath string) (*MachineRepository, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}
	repo := &MachineRepository{db: db}
	if err := repo.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return repo, nil
}

func (r *MachineRepository) Close() error {
	return r.db.Close()
}

func (r *MachineRepository) migrate() error {
	_, err := r.db.Exec(`
		create table if not exists machines (
			id text primary key,
			name text not null,
			ip text not null,
			ssh_port integer not null,
			ssh_user text not null,
			cluster_name text not null default '',
			status text not null,
			last_error text not null default '',
			created_at text not null,
			updated_at text not null
		);
		create unique index if not exists idx_machines_ip_port on machines(ip, ssh_port);
		create table if not exists clusters (
			name text primary key,
			created_at text not null
		);
	`)
	return err
}

func (r *MachineRepository) Save(ctx context.Context, m machine.Machine) (machine.Machine, error) {
	now := time.Now().UTC()
	m.CreatedAt = now
	m.UpdatedAt = now
	_, err := r.db.ExecContext(ctx, `
		insert into machines (id, name, ip, ssh_port, ssh_user, cluster_name, status, last_error, created_at, updated_at)
		values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		on conflict(id) do update set
			name = excluded.name,
			ip = excluded.ip,
			ssh_port = excluded.ssh_port,
			ssh_user = excluded.ssh_user,
			cluster_name = excluded.cluster_name,
			status = excluded.status,
			last_error = excluded.last_error,
			updated_at = excluded.updated_at
	`, m.ID, m.Name, m.IP, m.SSHPort, m.SSHUser, m.Cluster, string(m.Status), m.LastError, now.Format(time.RFC3339), now.Format(time.RFC3339))
	if err != nil {
		return machine.Machine{}, err
	}
	return r.getByID(ctx, m.ID)
}

func (r *MachineRepository) UpdateStatus(ctx context.Context, machineID string, status machine.Status, lastError string) error {
	_, err := r.db.ExecContext(ctx, `update machines set status = ?, last_error = ?, updated_at = ? where id = ?`,
		string(status), lastError, time.Now().UTC().Format(time.RFC3339), machineID)
	return err
}

func (r *MachineRepository) List(ctx context.Context) ([]machine.Machine, error) {
	rows, err := r.db.QueryContext(ctx, `
		select id, name, ip, ssh_port, ssh_user, cluster_name, status, last_error, created_at, updated_at
		from machines
		order by created_at desc
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []machine.Machine
	for rows.Next() {
		var m machine.Machine
		var createdAt, updatedAt string
		var status string
		if err := rows.Scan(&m.ID, &m.Name, &m.IP, &m.SSHPort, &m.SSHUser, &m.Cluster, &status, &m.LastError, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		m.Status = machine.Status(status)
		m.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		m.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
		result = append(result, m)
	}
	return result, rows.Err()
}

func (r *MachineRepository) getByID(ctx context.Context, id string) (machine.Machine, error) {
	row := r.db.QueryRowContext(ctx, `
		select id, name, ip, ssh_port, ssh_user, cluster_name, status, last_error, created_at, updated_at
		from machines
		where id = ?
	`, id)

	var m machine.Machine
	var createdAt, updatedAt string
	var status string
	if err := row.Scan(&m.ID, &m.Name, &m.IP, &m.SSHPort, &m.SSHUser, &m.Cluster, &status, &m.LastError, &createdAt, &updatedAt); err != nil {
		return machine.Machine{}, err
	}
	m.Status = machine.Status(status)
	m.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	m.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	return m, nil
}

func (r *MachineRepository) Exists(ctx context.Context, name string) (bool, error) {
	row := r.db.QueryRowContext(ctx, `select 1 from clusters where name = ?`, name)
	var v int
	if err := row.Scan(&v); err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (r *MachineRepository) Create(ctx context.Context, name string) error {
	_, err := r.db.ExecContext(ctx, `insert into clusters (name, created_at) values (?, ?) on conflict(name) do nothing`,
		name, time.Now().UTC().Format(time.RFC3339))
	return err
}

func (r *MachineRepository) ListNames(ctx context.Context) ([]string, error) {
	rows, err := r.db.QueryContext(ctx, `select name from clusters order by name asc`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
}
