// Package sqlite 提供基于 SQLite 的数据持久化实现，用于存储主机、Agent 和引导令牌等数据。
package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gmha/internal/domain"
	_ "modernc.org/sqlite"
)

// Store 是 SQLite 数据存储实现，封装了数据库连接和所有 CRUD 操作。
type Store struct {
	db *sql.DB
}

// NewStore 创建一个新的 SQLite 存储实例，自动创建数据库文件并执行表结构迁移。
func NewStore(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	store := &Store{db: db}
	if err := store.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

// Close 关闭数据库连接。
func (s *Store) Close() error {
	return s.db.Close()
}

// migrate 执行数据库表结构的自动迁移，创建必要的表和索引。
func (s *Store) migrate() error {
	stmts := []string{
		`create table if not exists hosts (
			id text primary key,
			name text not null,
			address text not null,
			cluster_name text not null default '',
			ssh_port integer not null,
			ssh_user text not null,
			bootstrap_state text not null,
			last_error text not null default '',
			agent_id text not null default '',
			created_at text not null,
			updated_at text not null
		);`,
		`create unique index if not exists idx_hosts_name_address on hosts(name, address);`,
		`create table if not exists bootstrap_tokens (
			host_id text primary key,
			token_hash text not null,
			issued_at text not null,
			expires_at text not null,
			last_used_at text
		);`,
		`create table if not exists agents (
			id text primary key,
			host_id text not null unique,
			hostname text not null,
			advertise_addr text not null default '',
			version text not null default '',
			state text not null,
			registered_at text not null,
			last_seen_at text not null
		);`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

// UpsertHost 插入或更新主机记录，若主机已存在则更新其名称、地址和 SSH 配置。
func (s *Store) UpsertHost(ctx context.Context, host domain.Host) (domain.Host, error) {
	now := time.Now().UTC()
	host.CreatedAt = now
	host.UpdatedAt = now
	if host.SSHPort == 0 {
		host.SSHPort = 22
	}
	_, err := s.db.ExecContext(ctx, `
		insert into hosts (id, name, address, cluster_name, ssh_port, ssh_user, bootstrap_state, last_error, agent_id, created_at, updated_at)
		values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		on conflict(id) do update set
			name=excluded.name,
			address=excluded.address,
			cluster_name=excluded.cluster_name,
			ssh_port=excluded.ssh_port,
			ssh_user=excluded.ssh_user,
			updated_at=excluded.updated_at
	`, host.ID, host.Name, host.Address, host.Cluster, host.SSHPort, host.SSHUser, host.BootstrapState, host.LastError, host.AgentID, host.CreatedAt.Format(time.RFC3339), host.UpdatedAt.Format(time.RFC3339))
	if err != nil {
		return domain.Host{}, err
	}
	return s.GetHostValue(ctx, host.ID)
}

// ListHosts 查询并返回所有主机列表，按创建时间倒序排列。
func (s *Store) ListHosts(ctx context.Context) ([]domain.Host, error) {
	rows, err := s.db.QueryContext(ctx, `
		select id, name, address, cluster_name, ssh_port, ssh_user, bootstrap_state, last_error, agent_id, created_at, updated_at
		from hosts order by created_at desc
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var hosts []domain.Host
	for rows.Next() {
		host, err := scanHost(rows)
		if err != nil {
			return nil, err
		}
		hosts = append(hosts, host)
	}
	return hosts, rows.Err()
}

// GetHost 根据主机 ID 查询主机信息，返回主机对象和是否存在。
func (s *Store) GetHost(ctx context.Context, hostID string) (domain.Host, bool, error) {
	host, err := s.GetHostValue(ctx, hostID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Host{}, false, nil
		}
		return domain.Host{}, false, err
	}
	return host, true, nil
}

// GetHostValue 根据主机 ID 查询并返回完整的主机对象，若不存在则返回错误。
func (s *Store) GetHostValue(ctx context.Context, hostID string) (domain.Host, error) {
	row := s.db.QueryRowContext(ctx, `
		select id, name, address, cluster_name, ssh_port, ssh_user, bootstrap_state, last_error, agent_id, created_at, updated_at
		from hosts where id = ?
	`, hostID)
	return scanHost(row)
}

// UpdateHostBootstrapState 更新主机的引导状态和最后错误信息。
func (s *Store) UpdateHostBootstrapState(ctx context.Context, hostID, state, lastError string) error {
	_, err := s.db.ExecContext(ctx, `update hosts set bootstrap_state = ?, last_error = ?, updated_at = ? where id = ?`,
		state, lastError, time.Now().UTC().Format(time.RFC3339), hostID)
	return err
}

// SaveBootstrapToken 保存或更新主机的引导令牌记录。
func (s *Store) SaveBootstrapToken(ctx context.Context, token domain.BootstrapToken) error {
	_, err := s.db.ExecContext(ctx, `
		insert into bootstrap_tokens (host_id, token_hash, issued_at, expires_at, last_used_at)
		values (?, ?, ?, ?, ?)
		on conflict(host_id) do update set
			token_hash=excluded.token_hash,
			issued_at=excluded.issued_at,
			expires_at=excluded.expires_at,
			last_used_at=excluded.last_used_at
	`, token.HostID, token.TokenHash, token.IssuedAt.UTC().Format(time.RFC3339), token.ExpiresAt.UTC().Format(time.RFC3339), nil)
	return err
}

// ValidateBootstrapToken 验证引导令牌的哈希值和有效期，验证通过后更新最后使用时间。
func (s *Store) ValidateBootstrapToken(ctx context.Context, hostID, token string, now time.Time) error {
	var tokenHash string
	var expiresAt string
	row := s.db.QueryRowContext(ctx, `select token_hash, expires_at from bootstrap_tokens where host_id = ?`, hostID)
	if err := row.Scan(&tokenHash, &expiresAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("bootstrap token not found for host %s", hostID)
		}
		return err
	}
	sum := sha256.Sum256([]byte(token))
	if hex.EncodeToString(sum[:]) != tokenHash {
		return errors.New("invalid bootstrap token")
	}
	expireTime, err := time.Parse(time.RFC3339, expiresAt)
	if err != nil {
		return err
	}
	if now.After(expireTime) {
		return errors.New("bootstrap token expired")
	}
	_, err = s.db.ExecContext(ctx, `update bootstrap_tokens set last_used_at = ? where host_id = ?`, now.UTC().Format(time.RFC3339), hostID)
	return err
}

// UpsertAgent 插入或更新 Agent 记录，同时更新对应主机的 agent_id 字段。
func (s *Store) UpsertAgent(ctx context.Context, agent domain.Agent) (domain.Agent, error) {
	if agent.ID == "" {
		agent.ID = "agent-" + agent.HostID
	}
	now := time.Now().UTC()
	if agent.RegisteredAt.IsZero() {
		agent.RegisteredAt = now
	}
	if agent.LastSeenAt.IsZero() {
		agent.LastSeenAt = now
	}
	_, err := s.db.ExecContext(ctx, `
		insert into agents (id, host_id, hostname, advertise_addr, version, state, registered_at, last_seen_at)
		values (?, ?, ?, ?, ?, ?, ?, ?)
		on conflict(host_id) do update set
			hostname=excluded.hostname,
			advertise_addr=excluded.advertise_addr,
			version=excluded.version,
			state=excluded.state,
			last_seen_at=excluded.last_seen_at
	`, agent.ID, agent.HostID, agent.Hostname, agent.AdvertiseAddr, agent.Version, agent.State, agent.RegisteredAt.UTC().Format(time.RFC3339), agent.LastSeenAt.UTC().Format(time.RFC3339))
	if err != nil {
		return domain.Agent{}, err
	}
	_, err = s.db.ExecContext(ctx, `update hosts set agent_id = ?, updated_at = ? where id = ?`, agent.ID, now.Format(time.RFC3339), agent.HostID)
	if err != nil {
		return domain.Agent{}, err
	}
	return s.GetAgentValue(ctx, agent.HostID)
}

// GetAgentByHostID 根据主机 ID 查询对应的 Agent 信息。
func (s *Store) GetAgentByHostID(ctx context.Context, hostID string) (domain.Agent, bool, error) {
	agent, err := s.GetAgentValue(ctx, hostID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Agent{}, false, nil
		}
		return domain.Agent{}, false, err
	}
	return agent, true, nil
}

// GetAgentValue 根据主机 ID 查询并返回完整的 Agent 对象。
func (s *Store) GetAgentValue(ctx context.Context, hostID string) (domain.Agent, error) {
	row := s.db.QueryRowContext(ctx, `
		select id, host_id, hostname, advertise_addr, version, state, registered_at, last_seen_at
		from agents where host_id = ?
	`, hostID)
	var agent domain.Agent
	var registeredAt, lastSeenAt string
	if err := row.Scan(&agent.ID, &agent.HostID, &agent.Hostname, &agent.AdvertiseAddr, &agent.Version, &agent.State, &registeredAt, &lastSeenAt); err != nil {
		return domain.Agent{}, err
	}
	agent.RegisteredAt, _ = time.Parse(time.RFC3339, registeredAt)
	agent.LastSeenAt, _ = time.Parse(time.RFC3339, lastSeenAt)
	return agent, nil
}

// UpdateAgentHeartbeat 更新 Agent 的心跳时间和状态为在线。
func (s *Store) UpdateAgentHeartbeat(ctx context.Context, hostID string, seenAt time.Time) error {
	_, err := s.db.ExecContext(ctx, `update agents set state = ?, last_seen_at = ? where host_id = ?`, domain.AgentStateOnline, seenAt.UTC().Format(time.RFC3339), hostID)
	return err
}

// hostScanner 是一个通用的行扫描接口，兼容 sql.Row 和 sql.Rows。
type hostScanner interface {
	Scan(dest ...any) error
}

// scanHost 从数据库行中扫描并构建 Host 结构体。
func scanHost(row hostScanner) (domain.Host, error) {
	var host domain.Host
	var createdAt, updatedAt string
	if err := row.Scan(&host.ID, &host.Name, &host.Address, &host.Cluster, &host.SSHPort, &host.SSHUser, &host.BootstrapState, &host.LastError, &host.AgentID, &createdAt, &updatedAt); err != nil {
		return domain.Host{}, err
	}
	host.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	host.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	return host, nil
}
