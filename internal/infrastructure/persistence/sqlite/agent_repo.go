// Package sqlite 实现了所有领域实体的 SQLite 仓储，使用纯 Go SQLite 驱动（modernc.org/sqlite）。
// 采用 WAL 模式和单连接串行化，确保数据一致性。
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	agentdomain "gmha/internal/domain/agent"
)

// AgentRepository 是 Agent 实体的 SQLite 仓储实现。
type AgentRepository struct {
	db *DB
}

// NewAgentRepository 创建一个新的 AgentRepository 实例。
func NewAgentRepository(db *DB) *AgentRepository {
	return &AgentRepository{db: db}
}

// Migrate 执行 Agent 表的数据库迁移，创建 agents 表（如不存在）。
func (r *AgentRepository) Migrate() error {
	_, err := r.db.Exec(`
		create table if not exists agents (
			id text primary key,
			machine_id text not null unique,
			install_dir text not null,
			version text not null default '',
			state text not null,
			last_error text not null default '',
			last_heartbeat_at text,
			registered_at text,
			created_at text not null,
			updated_at text not null
		);
	`)
	return err
}

func (r *AgentRepository) Save(ctx context.Context, agent agentdomain.Agent) (agentdomain.Agent, error) {
	now := time.Now().UTC()
	if agent.CreatedAt.IsZero() {
		agent.CreatedAt = now
	}
	agent.UpdatedAt = now
	_, err := r.db.ExecContext(ctx, `
		insert into agents (id, machine_id, install_dir, version, state, last_error, last_heartbeat_at, registered_at, created_at, updated_at)
		values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		on conflict(machine_id) do update set
			install_dir = excluded.install_dir,
			version = excluded.version,
			state = excluded.state,
			last_error = excluded.last_error,
			updated_at = excluded.updated_at
	`, agent.ID, agent.MachineID, agent.InstallDir, agent.Version, string(agent.State), agent.LastError, formatNullableTime(agent.LastHeartbeatAt), formatNullableTime(agent.RegisteredAt), agent.CreatedAt.Format(time.RFC3339), agent.UpdatedAt.Format(time.RFC3339))
	if err != nil {
		return agentdomain.Agent{}, err
	}
	return r.GetValue(ctx, agent.MachineID)
}

func (r *AgentRepository) GetByMachineID(ctx context.Context, machineID string) (agentdomain.Agent, bool, error) {
	item, err := r.GetValue(ctx, machineID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return agentdomain.Agent{}, false, nil
		}
		return agentdomain.Agent{}, false, err
	}
	return item, true, nil
}

func (r *AgentRepository) GetValue(ctx context.Context, machineID string) (agentdomain.Agent, error) {
	row := r.db.QueryRowContext(ctx, `
		select id, machine_id, install_dir, version, state, last_error, last_heartbeat_at, registered_at, created_at, updated_at
		from agents where machine_id = ?
	`, machineID)
	return scanAgent(row)
}

func (r *AgentRepository) List(ctx context.Context) ([]agentdomain.Agent, error) {
	rows, err := r.db.QueryContext(ctx, `
		select id, machine_id, install_dir, version, state, last_error, last_heartbeat_at, registered_at, created_at, updated_at
		from agents order by created_at desc
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []agentdomain.Agent
	for rows.Next() {
		item, err := scanAgent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (r *AgentRepository) UpdateState(ctx context.Context, machineID string, state agentdomain.State, lastError string) error {
	_, err := r.db.ExecContext(ctx, `update agents set state = ?, last_error = ?, updated_at = ? where machine_id = ?`,
		string(state), lastError, time.Now().UTC().Format(time.RFC3339), machineID)
	return err
}

func (r *AgentRepository) MarkRegistered(ctx context.Context, machineID string, at time.Time) error {
	_, err := r.db.ExecContext(ctx, `update agents set registered_at = ?, state = ?, updated_at = ? where machine_id = ?`,
		at.UTC().Format(time.RFC3339), string(agentdomain.StateOnline), at.UTC().Format(time.RFC3339), machineID)
	return err
}

func (r *AgentRepository) UpdateHeartbeat(ctx context.Context, machineID string, at time.Time) error {
	_, err := r.db.ExecContext(ctx, `update agents set last_heartbeat_at = ?, state = ?, updated_at = ? where machine_id = ?`,
		at.UTC().Format(time.RFC3339), string(agentdomain.StateOnline), at.UTC().Format(time.RFC3339), machineID)
	return err
}

func (r *AgentRepository) DeleteByMachineID(ctx context.Context, machineID string) error {
	_, err := r.db.ExecContext(ctx, `delete from agents where machine_id = ?`, machineID)
	return err
}

type agentScanner interface {
	Scan(dest ...any) error
}

func scanAgent(scanner agentScanner) (agentdomain.Agent, error) {
	var item agentdomain.Agent
	var state string
	var hb, reg, createdAt, updatedAt sql.NullString
	if err := scanner.Scan(&item.ID, &item.MachineID, &item.InstallDir, &item.Version, &state, &item.LastError, &hb, &reg, &createdAt, &updatedAt); err != nil {
		return agentdomain.Agent{}, err
	}
	item.State = agentdomain.State(state)
	item.LastHeartbeatAt = parseNullableTime(hb)
	item.RegisteredAt = parseNullableTime(reg)
	if createdAt.Valid {
		item.CreatedAt, _ = time.Parse(time.RFC3339, createdAt.String)
	}
	if updatedAt.Valid {
		item.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt.String)
	}
	return item, nil
}

func formatNullableTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return formatDatabaseTime(*t)
}

const databaseTimeLayout = "2006-01-02T15:04:05.000000000Z07:00"

func formatDatabaseTime(t time.Time) string { return t.UTC().Format(databaseTimeLayout) }

func parseNullableTime(v sql.NullString) *time.Time {
	if !v.Valid || v.String == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339, v.String)
	if err != nil {
		return nil
	}
	return &t
}
