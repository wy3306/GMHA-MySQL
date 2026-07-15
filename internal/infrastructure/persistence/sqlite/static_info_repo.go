package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	collectdomain "gmha/internal/collect"
)

// StaticInfoRepository 是静态信息的 SQLite 仓储实现。
type StaticInfoRepository struct {
	db *DB
}

func NewStaticInfoRepository(db *DB) *StaticInfoRepository {
	return &StaticInfoRepository{db: db}
}

func (r *StaticInfoRepository) Migrate() error {
	_, err := r.db.Exec(`
		create table if not exists machine_static_info (
			machine_id text primary key,
			host_json text not null default '{}',
			mysql_json text not null default '{}',
			collected_at text not null,
			updated_at text not null
		);
	`)
	return err
}

func (r *StaticInfoRepository) Save(ctx context.Context, item collectdomain.StaticInfo) error {
	hostJSON, _ := json.Marshal(item.Host)
	mysqlJSON, _ := json.Marshal(item.MySQL)
	now := time.Now().UTC()
	if item.CollectedAt.IsZero() {
		item.CollectedAt = now
	}
	if item.UpdatedAt.IsZero() {
		item.UpdatedAt = now
	}
	_, err := r.db.ExecContext(ctx, `
		insert into machine_static_info (machine_id, host_json, mysql_json, collected_at, updated_at)
		values (?, ?, ?, ?, ?)
		on conflict(machine_id) do update set
			host_json = excluded.host_json,
			mysql_json = excluded.mysql_json,
			collected_at = excluded.collected_at,
			updated_at = excluded.updated_at
	`, item.MachineID, string(hostJSON), string(mysqlJSON), item.CollectedAt.UTC().Format(time.RFC3339), item.UpdatedAt.UTC().Format(time.RFC3339))
	return err
}

func (r *StaticInfoRepository) Get(ctx context.Context, machineID string) (collectdomain.StaticInfo, bool, error) {
	row := r.db.QueryRowContext(ctx, `
		select machine_id, host_json, mysql_json, collected_at, updated_at
		from machine_static_info where machine_id = ?
	`, machineID)
	var item collectdomain.StaticInfo
	var hostJSON, mysqlJSON, collectedAt, updatedAt string
	if err := row.Scan(&item.MachineID, &hostJSON, &mysqlJSON, &collectedAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return collectdomain.StaticInfo{}, false, nil
		}
		return collectdomain.StaticInfo{}, false, err
	}
	_ = json.Unmarshal([]byte(hostJSON), &item.Host)
	_ = json.Unmarshal([]byte(mysqlJSON), &item.MySQL)
	item.CollectedAt, _ = time.Parse(time.RFC3339, collectedAt)
	item.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	return item, true, nil
}

func (r *StaticInfoRepository) DeleteByMachineID(ctx context.Context, machineID string) error {
	_, err := r.db.ExecContext(ctx, `delete from machine_static_info where machine_id = ?`, machineID)
	return err
}
