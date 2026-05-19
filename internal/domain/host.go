package domain

import "time"

// Host 是旧版的主机领域模型，包含引导状态（已被 internal/domain/machine 包替代）。
type Host struct {
	ID             string
	Name           string
	Address        string
	Cluster        string
	SSHPort        int
	SSHUser        string
	BootstrapState string
	LastError      string
	AgentID        string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

const (
	HostBootstrapPending   = "pending"
	HostBootstrapRunning   = "running"
	HostBootstrapSucceeded = "succeeded"
	HostBootstrapFailed    = "failed"
)
