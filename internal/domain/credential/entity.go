// Package credential 定义了 SSH 凭据实体的领域模型和仓储接口。
// SSH 凭据用于存储连接被纳管机器所需的用户名和密码。
package credential

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

type Type string

const (
	TypePassword   Type = "password"
	TypePrivateKey Type = "private_key"
)

// SSHCredential 是 SSH 凭据实体的领域模型。敏感字段只在服务端使用，绝不返回 Web 页面。
type SSHCredential struct {
	ID          string
	Name        string
	SSHUser     string
	Type        Type
	SSHPassword string
	PrivateKey  string
	Passphrase  string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Repository 定义了 SSH 凭据实体的仓储接口。
type Repository interface {
	Save(ctx context.Context, item SSHCredential) (SSHCredential, error)
	GetByID(ctx context.Context, id string) (SSHCredential, bool, error)
	GetByName(ctx context.Context, name string) (SSHCredential, bool, error)
	List(ctx context.Context) ([]SSHCredential, error)
	Delete(ctx context.Context, id string) error
}

// NewID 根据凭据名称生成唯一的凭据 ID。
func NewID(name string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(name)))
	return fmt.Sprintf("cred-%s", hex.EncodeToString(sum[:8]))
}
