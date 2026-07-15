// Package machine 定义了机器实体的领域模型和仓储接口。
// 机器是被纳管的服务器，通过 SSH 连接进行管理，Agent 部署在其上运行。
package machine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"
)

// Status 表示机器的生命周期状态。
type Status string

const (
	StatusPending         Status = "pending"          // 初始状态，等待 SSH 连接测试
	StatusSSHConnected    Status = "ssh_connected"    // SSH 连接测试通过
	StatusSSHTrustReady   Status = "ssh_trust_ready"  // SSH 免密信任已建立
	StatusAgentInstalling Status = "agent_installing" // Agent 正在安装
	StatusAgentOnline     Status = "agent_online"     // Agent 在线运行
	StatusAgentError      Status = "agent_error"      // Agent 安装或运行出错
	StatusSSHFailed       Status = "ssh_failed"       // SSH 连接失败
)

// Machine 是机器实体的领域模型，表示一台被纳管的服务器。
type Machine struct {
	ID              string
	Name            string
	IP              string
	SSHPort         int
	SSHUser         string
	CredentialID    string
	Cluster         string
	AgentInstallDir string
	Status          Status
	LastError       string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// Endpoint 表示机器的网络连接端点（IP + SSH端口）。
type Endpoint struct {
	IP      string
	SSHPort int
}

// SSHAuth 表示 SSH 认证信息，支持密码或私钥文件内容。
type SSHAuth struct {
	User       string
	Password   string
	PrivateKey string
	Passphrase string
}

// Repository 定义了机器实体的仓储接口，用于持久化机器数据。
type Repository interface {
	Save(ctx context.Context, machine Machine) (Machine, error)
	UpdateStatus(ctx context.Context, machineID string, status Status, lastError string) error
	GetByID(ctx context.Context, machineID string) (Machine, bool, error)
	GetByIP(ctx context.Context, ip string) (Machine, bool, error)
	List(ctx context.Context) ([]Machine, error)
	UpdateBasics(ctx context.Context, machine Machine) error
	AssignCluster(ctx context.Context, machineID, clusterName string) error
	RebindCluster(ctx context.Context, oldName, newName string) error
	ClearCluster(ctx context.Context, clusterName string) error
	Delete(ctx context.Context, machineID string) error
}

// NewID 根据机器名称、IP 和 SSH 端口生成唯一的机器 ID。
// 使用 SHA256 哈希算法，取前8字节转为十六进制字符串。
func NewID(name, ip string, sshPort int) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%d", name, ip, sshPort)))
	return "machine-" + hex.EncodeToString(sum[:8])
}
