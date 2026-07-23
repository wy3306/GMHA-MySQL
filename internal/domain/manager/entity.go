package manager

import (
	"context"
	"time"
)

type HAConfig struct {
	Enabled     bool      `json:"enabled"`
	VIP         string    `json:"vip"`
	Prefix      int       `json:"prefix"`
	Interface   string    `json:"interface"`
	InstallDir  string    `json:"install_dir"`
	ServiceName string    `json:"service_name"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type Node struct {
	ID           string    `json:"id"`
	MachineID    string    `json:"machine_id"`
	Name         string    `json:"name"`
	IP           string    `json:"ip"`
	HTTPAddress  string    `json:"http_address"`
	GRPCAddress  string    `json:"grpc_address"`
	VIPInterface string    `json:"vip_interface"`
	Role         string    `json:"role"`
	State        string    `json:"state"`
	Version      string    `json:"version"`
	LastSeenAt   time.Time `json:"last_seen_at"`
	LastError    string    `json:"last_error"`
	TaskID       string    `json:"task_id"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type Repository interface {
	Migrate() error
	GetConfig(context.Context) (HAConfig, error)
	SaveConfig(context.Context, HAConfig) error
	ListNodes(context.Context) ([]Node, error)
	GetNode(context.Context, string) (Node, bool, error)
	SaveNode(context.Context, Node) error
	DeleteNode(context.Context, string) error
	SetActive(context.Context, string, time.Time) error
}
