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
	out, err := e.client.RunOutput(ctx, endpoint, auth, agentRecoveryDiagnostics)
	return strings.TrimSpace(string(out)), err
}

func (e *RecoveryExecutor) Start(ctx context.Context, endpoint machinedomain.Endpoint, auth machinedomain.SSHAuth) (string, error) {
	out, err := e.client.RunOutput(ctx, endpoint, auth, recoveryActivationCommand("start"))
	return strings.TrimSpace(string(out)), err
}

func (e *RecoveryExecutor) Restart(ctx context.Context, endpoint machinedomain.Endpoint, auth machinedomain.SSHAuth) (string, error) {
	out, err := e.client.RunOutput(ctx, endpoint, auth, recoveryActivationCommand("restart"))
	return strings.TrimSpace(string(out)), err
}

// Keep the activation exit status: diagnostics must never turn failure into success.
func recoveryActivationCommand(action string) string {
	return `rc=0
systemctl daemon-reload && systemctl reset-failed gmha-agent.service && systemctl enable gmha-agent.service && systemctl ` + action + ` gmha-agent.service && systemctl is-active --quiet gmha-agent.service || rc=$?
` + agentRecoveryDiagnostics + `
exit "$rc"`
}

const agentRecoveryDiagnostics = `systemctl is-active gmha-agent.service || true
systemctl is-enabled gmha-agent.service || true
systemctl status gmha-agent.service --no-pager --full -n 20 || true
journalctl -u gmha-agent.service -n 30 --no-pager || true
dir=$(systemctl show gmha-agent.service -p WorkingDirectory --value 2>/dev/null || true)
if [ -n "$dir" ] && [ -f "$dir/logs/agent.log" ]; then tail -c 8192 "$dir/logs/agent.log"; fi
true`
