package domain

import "time"

// BootstrapToken 是 Agent 引导注册时使用的令牌，用于初始身份验证。
type BootstrapToken struct {
	HostID     string
	TokenHash  string
	ExpiresAt  time.Time
	IssuedAt   time.Time
	LastUsedAt *time.Time
}
