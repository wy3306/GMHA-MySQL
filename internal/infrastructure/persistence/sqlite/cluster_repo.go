package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	clusterdomain "gmha/internal/domain/cluster"
)

// ClusterRepository 是集群实体的 SQLite 仓储实现。
type ClusterRepository struct {
	db *sql.DB
}

func NewClusterRepository(db *sql.DB) *ClusterRepository {
	return &ClusterRepository{db: db}
}

func (r *ClusterRepository) Migrate() error {
	_, err := r.db.Exec(`
		create table if not exists clusters (
			name text primary key,
			description text not null default '',
			created_at text not null
		);
	`)
	if err != nil {
		return err
	}
	_, _ = r.db.Exec(`alter table clusters add column description text not null default ''`)
	return nil
}

func (r *ClusterRepository) Exists(ctx context.Context, name string) (bool, error) {
	row := r.db.QueryRowContext(ctx, `select 1 from clusters where name = ?`, strings.TrimSpace(name))
	var exists int
	if err := row.Scan(&exists); err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (r *ClusterRepository) Create(ctx context.Context, cluster clusterdomain.Cluster) error {
	_, err := r.db.ExecContext(ctx, `
		insert into clusters (name, description, created_at) values (?, ?, ?)
		on conflict(name) do nothing
	`, strings.TrimSpace(cluster.Name), strings.TrimSpace(cluster.Description), time.Now().UTC().Format(time.RFC3339))
	return err
}

func (r *ClusterRepository) Get(ctx context.Context, name string) (clusterdomain.Cluster, bool, error) {
	row := r.db.QueryRowContext(ctx, `select name, description, created_at from clusters where name = ?`, strings.TrimSpace(name))
	var item clusterdomain.Cluster
	var createdAt string
	if err := row.Scan(&item.Name, &item.Description, &createdAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return clusterdomain.Cluster{}, false, nil
		}
		return clusterdomain.Cluster{}, false, err
	}
	item.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	return item, true, nil
}

func (r *ClusterRepository) List(ctx context.Context) ([]clusterdomain.Cluster, error) {
	rows, err := r.db.QueryContext(ctx, `select name, description, created_at from clusters order by name asc`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []clusterdomain.Cluster
	for rows.Next() {
		var item clusterdomain.Cluster
		var createdAt string
		if err := rows.Scan(&item.Name, &item.Description, &createdAt); err != nil {
			return nil, err
		}
		item.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		out = append(out, item)
	}
	return out, rows.Err()
}

func (r *ClusterRepository) Update(ctx context.Context, oldName string, cluster clusterdomain.Cluster) error {
	_, err := r.db.ExecContext(ctx, `update clusters set name = ?, description = ? where name = ?`, strings.TrimSpace(cluster.Name), strings.TrimSpace(cluster.Description), strings.TrimSpace(oldName))
	return err
}

func (r *ClusterRepository) Delete(ctx context.Context, name string) error {
	_, err := r.db.ExecContext(ctx, `delete from clusters where name = ?`, strings.TrimSpace(name))
	return err
}
