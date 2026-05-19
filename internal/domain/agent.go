// Package domain 包含了 GMHA 系统的核心领域模型（旧版，基于 HostID 的 Agent 结构）。
package domain

import "time"

// Agent 是旧版的 Agent 领域模型，使用 HostID 关联主机（已被 internal/domain/agent 包替代）。
type Agent struct {
	ID            string
	HostID        string
	Hostname      string
	AdvertiseAddr string
	Version       string
	State         string
	RegisteredAt  time.Time
	LastSeenAt    time.Time
}

const AgentStateOnline = "online"
