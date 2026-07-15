package sqlite

import (
	"context"
	"encoding/json"
	"strings"

	taskdomain "gmha/internal/domain/task"
)

// MySQLAccountPresetRepository 持久化 MySQL 安装时使用的三类预设账号。
type MySQLAccountPresetRepository struct{ db *DB }

func NewMySQLAccountPresetRepository(db *DB) *MySQLAccountPresetRepository {
	return &MySQLAccountPresetRepository{db: db}
}

func (r *MySQLAccountPresetRepository) Migrate() error {
	_, err := r.db.Exec(`
		create table if not exists mysql_account_presets (
			role text primary key,
			username text not null default '',
			password text not null default '',
			host text not null default '',
			enabled integer not null default 1,
			extended_backup integer not null default 0,
			privileges text not null default ''
		);
	`)
	if err != nil {
		return err
	}
	// 兼容已有数据库：旧表不存在 privileges 列时追加迁移。
	if _, err := r.db.Exec(`alter table mysql_account_presets add column privileges text not null default ''`); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column") && !strings.Contains(strings.ToLower(err.Error()), "already exists") {
		return err
	}
	return nil
}

func (r *MySQLAccountPresetRepository) List(ctx context.Context) ([]taskdomain.MySQLAccountSpec, error) {
	rows, err := r.db.QueryContext(ctx, `select role, username, password, host, enabled, extended_backup, privileges from mysql_account_presets`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]taskdomain.MySQLAccountSpec, 0, 3)
	for rows.Next() {
		var item taskdomain.MySQLAccountSpec
		var privileges string
		if err := rows.Scan(&item.Role, &item.Username, &item.Password, &item.Host, &item.Enabled, &item.ExtendedBackup, &privileges); err != nil {
			return nil, err
		}
		if strings.TrimSpace(privileges) != "" {
			_ = json.Unmarshal([]byte(privileges), &item.Privileges)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *MySQLAccountPresetRepository) Save(ctx context.Context, items []taskdomain.MySQLAccountSpec) error {
	for _, item := range items {
		role := strings.ToLower(strings.TrimSpace(item.Role))
		if role != "monitor" && role != "mha" && role != "backup" {
			continue
		}
		privileges, err := json.Marshal(item.Privileges)
		if err != nil {
			return err
		}
		if _, err := r.db.ExecContext(ctx, `insert into mysql_account_presets (role, username, password, host, enabled, extended_backup, privileges) values (?, ?, ?, ?, ?, ?, ?) on conflict(role) do update set username=excluded.username, password=excluded.password, host=excluded.host, enabled=excluded.enabled, extended_backup=excluded.extended_backup, privileges=excluded.privileges`, role, strings.TrimSpace(item.Username), item.Password, strings.TrimSpace(item.Host), item.Enabled, item.ExtendedBackup, string(privileges)); err != nil {
			return err
		}
	}
	return nil
}
