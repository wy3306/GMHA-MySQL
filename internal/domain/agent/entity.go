// Package agent 定义了 Agent 实体的领域模型和仓储接口。
// Agent 是部署在被纳管机器上的守护进程，负责执行任务、采集指标和上报心跳。
package agent

import (
	"context"
	"path/filepath"
	"strings"
	"time"
)

// State 表示 Agent 的运行状态。
type State string

const (
	StateInstalling State = "installing" // Agent 正在安装中
	StateOnline     State = "online"     // Agent 在线，心跳正常
	StateOffline    State = "offline"    // Agent 离线，心跳超时
	StateError      State = "error"      // Agent 出现错误
)

// DefaultInstallDir 是 Agent 的默认安装目录。
const DefaultInstallDir = "/home/gmha/agent"

// ResolveInstallDir 根据 SSH 用户名和请求的安装路径，解析出最终的 Agent 安装目录。
// 如果请求路径为空，则根据用户名推导：root 用户使用默认目录，其他用户使用 /home/{user}/gmha/agent。
func ResolveInstallDir(sshUser, requested string) string {
	requested = strings.TrimSpace(requested)
	if requested != "" {
		return requested
	}
	if strings.TrimSpace(sshUser) == "" || sshUser == "root" {
		return DefaultInstallDir
	}
	return filepath.ToSlash(filepath.Join("/home", sshUser, "gmha", "agent"))
}

// Agent 是 Agent 实体的领域模型，表示部署在被纳管机器上的守护进程。
type Agent struct {
	ID              string
	MachineID       string
	InstallDir      string
	Version         string
	State           State
	LastError       string
	LastHeartbeatAt *time.Time
	RegisteredAt    *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// Repository 定义了 Agent 实体的仓储接口，用于持久化 Agent 数据。
type Repository interface {
	Save(ctx context.Context, agent Agent) (Agent, error)
	GetByMachineID(ctx context.Context, machineID string) (Agent, bool, error)
	List(ctx context.Context) ([]Agent, error)
	UpdateState(ctx context.Context, machineID string, state State, lastError string) error
	MarkRegistered(ctx context.Context, machineID string, at time.Time) error
	UpdateHeartbeat(ctx context.Context, machineID string, at time.Time) error
	DeleteByMachineID(ctx context.Context, machineID string) error
}
