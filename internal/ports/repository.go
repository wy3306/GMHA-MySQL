// Package ports 定义了应用层的端口接口（驱动接口），用于解耦业务逻辑与基础设施实现。
package ports

import (
	"context"
	"time"

	"gmha/internal/domain"
)

// Repository 是数据仓储接口，定义了主机、Agent 和引导令牌的持久化操作。
type Repository interface {
	// UpsertHost 插入或更新主机记录。
	UpsertHost(ctx context.Context, host domain.Host) (domain.Host, error)
	// ListHosts 返回所有主机列表。
	ListHosts(ctx context.Context) ([]domain.Host, error)
	// GetHost 根据 ID 获取主机信息。
	GetHost(ctx context.Context, hostID string) (domain.Host, bool, error)
	// UpdateHostBootstrapState 更新主机的引导状态。
	UpdateHostBootstrapState(ctx context.Context, hostID, state, lastError string) error
	// SaveBootstrapToken 保存引导令牌。
	SaveBootstrapToken(ctx context.Context, token domain.BootstrapToken) error
	// ValidateBootstrapToken 验证引导令牌的有效性。
	ValidateBootstrapToken(ctx context.Context, hostID, token string, now time.Time) error
	// UpsertAgent 插入或更新 Agent 记录。
	UpsertAgent(ctx context.Context, agent domain.Agent) (domain.Agent, error)
	// GetAgentByHostID 根据主机 ID 获取 Agent 信息。
	GetAgentByHostID(ctx context.Context, hostID string) (domain.Agent, bool, error)
	// UpdateAgentHeartbeat 更新 Agent 的心跳时间。
	UpdateAgentHeartbeat(ctx context.Context, hostID string, seenAt time.Time) error
	// Close 关闭仓储连接，释放资源。
	Close() error
}
