package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	accountdomain "gmha/internal/domain/account"
)

type AccountRepository struct{ db *DB }

func NewAccountRepository(db *DB) *AccountRepository { return &AccountRepository{db: db} }

func (r *AccountRepository) Migrate() error {
	_, err := r.db.Exec(`
		create table if not exists platform_user (
			id varchar(191) primary key, username varchar(191) not null unique, name text not null default '',
			email text not null default '', phone text not null default '', password_hash text not null,
			role varchar(191) not null default 'viewer', permissions_json text not null default '[]',
			enabled integer not null default 1, created_at text not null, updated_at text not null,
			last_login_at text
		);
		create index if not exists idx_platform_user_role on platform_user(role, enabled);
		create table if not exists platform_session (
			token_hash varchar(64) primary key, user_id varchar(191) not null, expires_at varchar(64) not null, created_at text not null
		);
		create index if not exists idx_platform_session_user on platform_session(user_id, expires_at);
	`)
	return err
}

func (r *AccountRepository) EnsureAdmin(ctx context.Context, passwordHash string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	permissions, _ := json.Marshal([]string{"*"})
	_, err := r.db.ExecContext(ctx, `insert into platform_user(id,username,name,email,phone,password_hash,role,permissions_json,enabled,created_at,updated_at) values(?,?,?,?,?,?,?,?,1,?,?) on conflict(id) do nothing`, "user-admin", "admin", "平台管理员", "", "", passwordHash, "admin", string(permissions), now, now)
	return err
}

func (r *AccountRepository) List(ctx context.Context) ([]accountdomain.User, error) {
	rows, err := r.db.QueryContext(ctx, `select id,username,name,email,phone,password_hash,role,permissions_json,enabled,created_at,updated_at,last_login_at from platform_user order by case when username='admin' then 0 else 1 end, created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []accountdomain.User
	for rows.Next() {
		x, err := scanAccountUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

func (r *AccountRepository) FindByUsername(ctx context.Context, username string) (accountdomain.User, error) {
	return scanAccountUser(r.db.QueryRowContext(ctx, `select id,username,name,email,phone,password_hash,role,permissions_json,enabled,created_at,updated_at,last_login_at from platform_user where username=?`, username))
}

func (r *AccountRepository) FindByID(ctx context.Context, id string) (accountdomain.User, error) {
	return scanAccountUser(r.db.QueryRowContext(ctx, `select id,username,name,email,phone,password_hash,role,permissions_json,enabled,created_at,updated_at,last_login_at from platform_user where id=?`, id))
}

type accountScanner interface{ Scan(...any) error }

func scanAccountUser(row accountScanner) (accountdomain.User, error) {
	var x accountdomain.User
	var permissions, created, updated string
	var last sql.NullString
	if err := row.Scan(&x.ID, &x.Username, &x.Name, &x.Email, &x.Phone, &x.PasswordHash, &x.Role, &permissions, &x.Enabled, &created, &updated, &last); err != nil {
		return x, err
	}
	_ = json.Unmarshal([]byte(permissions), &x.Permissions)
	x.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	x.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	if last.Valid {
		if value, err := time.Parse(time.RFC3339Nano, last.String); err == nil {
			x.LastLoginAt = &value
		}
	}
	return x, nil
}

func (r *AccountRepository) Create(ctx context.Context, x accountdomain.User) error {
	permissions, _ := json.Marshal(x.Permissions)
	_, err := r.db.ExecContext(ctx, `insert into platform_user(id,username,name,email,phone,password_hash,role,permissions_json,enabled,created_at,updated_at) values(?,?,?,?,?,?,?,?,?,?,?)`, x.ID, x.Username, x.Name, x.Email, x.Phone, x.PasswordHash, x.Role, string(permissions), x.Enabled, x.CreatedAt.Format(time.RFC3339Nano), x.UpdatedAt.Format(time.RFC3339Nano))
	return err
}

func (r *AccountRepository) Update(ctx context.Context, x accountdomain.User) error {
	permissions, _ := json.Marshal(x.Permissions)
	result, err := r.db.ExecContext(ctx, `update platform_user set name=?,email=?,phone=?,password_hash=?,role=?,permissions_json=?,enabled=?,updated_at=? where id=?`, x.Name, x.Email, x.Phone, x.PasswordHash, x.Role, string(permissions), x.Enabled, x.UpdatedAt.Format(time.RFC3339Nano), x.ID)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (r *AccountRepository) Delete(ctx context.Context, id string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `delete from platform_session where user_id=?`, id); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `delete from platform_user where id=?`, id)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return sql.ErrNoRows
	}
	return tx.Commit()
}

func (r *AccountRepository) SaveSession(ctx context.Context, session accountdomain.Session) error {
	_, err := r.db.ExecContext(ctx, `insert into platform_session(token_hash,user_id,expires_at,created_at) values(?,?,?,?)`, session.TokenHash, session.UserID, session.ExpiresAt.Format(time.RFC3339Nano), session.CreatedAt.Format(time.RFC3339Nano))
	return err
}

func (r *AccountRepository) UserForSession(ctx context.Context, tokenHash string, now time.Time) (accountdomain.User, error) {
	var userID, expires string
	err := r.db.QueryRowContext(ctx, `select user_id,expires_at from platform_session where token_hash=?`, tokenHash).Scan(&userID, &expires)
	if err != nil {
		return accountdomain.User{}, err
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, expires)
	if err != nil || !expiresAt.After(now) {
		_, _ = r.db.ExecContext(ctx, `delete from platform_session where token_hash=?`, tokenHash)
		return accountdomain.User{}, sql.ErrNoRows
	}
	user, err := r.FindByID(ctx, userID)
	if err != nil || !user.Enabled {
		return accountdomain.User{}, sql.ErrNoRows
	}
	return user, nil
}

func (r *AccountRepository) DeleteSession(ctx context.Context, tokenHash string) error {
	_, err := r.db.ExecContext(ctx, `delete from platform_session where token_hash=?`, tokenHash)
	return err
}

func (r *AccountRepository) DeleteSessionsForUser(ctx context.Context, userID string) error {
	_, err := r.db.ExecContext(ctx, `delete from platform_session where user_id=?`, userID)
	return err
}

func (r *AccountRepository) TouchLogin(ctx context.Context, userID string, at time.Time) error {
	_, err := r.db.ExecContext(ctx, `update platform_user set last_login_at=?,updated_at=? where id=?`, at.Format(time.RFC3339Nano), at.Format(time.RFC3339Nano), userID)
	return err
}

func (r *AccountRepository) CleanupSessions(ctx context.Context, now time.Time) error {
	_, err := r.db.ExecContext(ctx, `delete from platform_session where expires_at<=?`, now.Format(time.RFC3339Nano))
	return err
}
