package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	credentialdomain "gmha/internal/domain/credential"
)

// CredentialRepository 是 SSH 凭据实体的 SQLite 仓储实现。
type CredentialRepository struct {
	db *DB
}

func NewCredentialRepository(db *DB) *CredentialRepository {
	return &CredentialRepository{db: db}
}

func (r *CredentialRepository) Migrate() error {
	_, err := r.db.Exec(`
		create table if not exists ssh_credentials (
			id text primary key,
			name text not null unique,
			ssh_user text not null,
			credential_type text not null default 'password',
			ssh_password text not null,
			private_key text not null default '',
			passphrase text not null default '',
			created_at text not null,
			updated_at text not null
		);
	`)
	_, _ = r.db.Exec(`alter table ssh_credentials add column credential_type text not null default 'password'`)
	_, _ = r.db.Exec(`alter table ssh_credentials add column private_key text not null default ''`)
	_, _ = r.db.Exec(`alter table ssh_credentials add column passphrase text not null default ''`)
	return err
}

func (r *CredentialRepository) Save(ctx context.Context, item credentialdomain.SSHCredential) (credentialdomain.SSHCredential, error) {
	item.Name = strings.TrimSpace(item.Name)
	item.SSHUser = strings.TrimSpace(item.SSHUser)
	if item.Type == "" {
		item.Type = credentialdomain.TypePassword
	}
	if item.ID == "" {
		item.ID = credentialdomain.NewID(item.Name)
	}
	now := time.Now().UTC()
	if item.CreatedAt.IsZero() {
		item.CreatedAt = now
	}
	item.UpdatedAt = now
	_, err := r.db.ExecContext(ctx, `
		insert into ssh_credentials (id, name, ssh_user, credential_type, ssh_password, private_key, passphrase, created_at, updated_at)
		values (?, ?, ?, ?, ?, ?, ?, ?, ?)
		on conflict(name) do update set
			ssh_user = excluded.ssh_user,
			credential_type = excluded.credential_type,
			ssh_password = excluded.ssh_password,
			private_key = excluded.private_key,
			passphrase = excluded.passphrase,
			updated_at = excluded.updated_at
	`, item.ID, item.Name, item.SSHUser, item.Type, item.SSHPassword, item.PrivateKey, item.Passphrase, item.CreatedAt.Format(time.RFC3339), item.UpdatedAt.Format(time.RFC3339))
	if err != nil {
		return credentialdomain.SSHCredential{}, err
	}
	return r.GetValueByName(ctx, item.Name)
}

func (r *CredentialRepository) GetByID(ctx context.Context, id string) (credentialdomain.SSHCredential, bool, error) {
	row := r.db.QueryRowContext(ctx, `
		select id, name, ssh_user, credential_type, ssh_password, private_key, passphrase, created_at, updated_at
		from ssh_credentials
		where id = ?
	`, strings.TrimSpace(id))
	item, err := scanCredential(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return credentialdomain.SSHCredential{}, false, nil
		}
		return credentialdomain.SSHCredential{}, false, err
	}
	return item, true, nil
}

func (r *CredentialRepository) GetByName(ctx context.Context, name string) (credentialdomain.SSHCredential, bool, error) {
	item, err := r.GetValueByName(ctx, name)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return credentialdomain.SSHCredential{}, false, nil
		}
		return credentialdomain.SSHCredential{}, false, err
	}
	return item, true, nil
}

func (r *CredentialRepository) GetValueByName(ctx context.Context, name string) (credentialdomain.SSHCredential, error) {
	row := r.db.QueryRowContext(ctx, `
		select id, name, ssh_user, credential_type, ssh_password, private_key, passphrase, created_at, updated_at
		from ssh_credentials
		where name = ?
	`, strings.TrimSpace(name))
	return scanCredential(row)
}

func (r *CredentialRepository) List(ctx context.Context) ([]credentialdomain.SSHCredential, error) {
	rows, err := r.db.QueryContext(ctx, `
		select id, name, ssh_user, credential_type, ssh_password, private_key, passphrase, created_at, updated_at
		from ssh_credentials
		order by name asc
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []credentialdomain.SSHCredential
	for rows.Next() {
		item, err := scanCredential(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (r *CredentialRepository) Delete(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, `delete from ssh_credentials where id = ?`, strings.TrimSpace(id))
	return err
}

type credentialScanner interface {
	Scan(dest ...any) error
}

func scanCredential(scanner credentialScanner) (credentialdomain.SSHCredential, error) {
	var item credentialdomain.SSHCredential
	var createdAt string
	var updatedAt string
	if err := scanner.Scan(&item.ID, &item.Name, &item.SSHUser, &item.Type, &item.SSHPassword, &item.PrivateKey, &item.Passphrase, &createdAt, &updatedAt); err != nil {
		return credentialdomain.SSHCredential{}, err
	}
	item.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	item.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	return item, nil
}
