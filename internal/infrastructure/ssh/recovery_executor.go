package ssh

import (
	"context"
	"strings"

	machinedomain "gmha/internal/domain/machine"
)

// RecoveryExecutor 通过 SSH 执行 Agent 恢复操作（检查状态、启动、重启）。
type RecoveryExecutor struct {
	client *Client
}

func NewRecoveryExecutor(client *Client) *RecoveryExecutor {
	return &RecoveryExecutor{client: client}
}

func (e *RecoveryExecutor) Inspect(ctx context.Context, endpoint machinedomain.Endpoint, auth machinedomain.SSHAuth) (string, error) {
	out, err := e.client.RunOutput(ctx, endpoint, auth, "systemctl is-active gmha-agent; echo ====; systemctl is-enabled gmha-agent || true; echo ====; systemctl status gmha-agent --no-pager -n 20 || true; echo ====; journalctl -u gmha-agent -n 30 --no-pager || true")
	return strings.TrimSpace(string(out)), err
}

func (e *RecoveryExecutor) Start(ctx context.Context, endpoint machinedomain.Endpoint, auth machinedomain.SSHAuth) (string, error) {
	out, err := e.client.RunOutput(ctx, endpoint, auth, "systemctl start gmha-agent && systemctl status gmha-agent --no-pager -n 20 || true")
	return strings.TrimSpace(string(out)), err
}

func (e *RecoveryExecutor) Restart(ctx context.Context, endpoint machinedomain.Endpoint, auth machinedomain.SSHAuth) (string, error) {
	out, err := e.client.RunOutput(ctx, endpoint, auth, "systemctl restart gmha-agent && systemctl status gmha-agent --no-pager -n 20 || true")
	return strings.TrimSpace(string(out)), err
}
