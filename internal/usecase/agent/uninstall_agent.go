package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"

	agentdomain "gmha/internal/domain/agent"
	machinedomain "gmha/internal/domain/machine"
)

// UninstallMachineRepository 定义了卸载场景下的机器仓储接口。
type UninstallMachineRepository interface {
	GetByID(ctx context.Context, machineID string) (machinedomain.Machine, bool, error)
	UpdateStatus(ctx context.Context, machineID string, status machinedomain.Status, lastError string) error
}

// UninstallAgentRepository 定义了卸载场景下的 Agent 仓储接口，支持删除操作。
type UninstallAgentRepository interface {
	GetByMachineID(ctx context.Context, machineID string) (agentdomain.Agent, bool, error)
	UpdateState(ctx context.Context, machineID string, state agentdomain.State, lastError string) error
	DeleteByMachineID(ctx context.Context, machineID string) error
}

// UninstallDependencies 是卸载 Agent 用例所需的外部依赖集合。
type UninstallDependencies struct {
	MachineRepo UninstallMachineRepository
	AgentRepo   UninstallAgentRepository
	SSHClient   SSHClient
}

// UninstallAgentUsecase 是卸载 Agent 的用例，负责通过 SSH 从目标机器移除 Agent。
type UninstallAgentUsecase struct {
	machineRepo UninstallMachineRepository
	agentRepo   UninstallAgentRepository
	sshClient   SSHClient
}

// UninstallAgentRequest 是卸载 Agent 的请求参数。
type UninstallAgentRequest struct {
	MachineID string
}

// UninstallAgentResponse 是卸载 Agent 的响应结果。
type UninstallAgentResponse struct {
	MachineID  string `json:"machine_id"`
	InstallDir string `json:"install_dir"`
	FinalState string `json:"final_state"`
}

// NewUninstallAgentUsecase 创建一个新的卸载 Agent 用例实例。
func NewUninstallAgentUsecase(dep UninstallDependencies) *UninstallAgentUsecase {
	return &UninstallAgentUsecase{
		machineRepo: dep.MachineRepo,
		agentRepo:   dep.AgentRepo,
		sshClient:   dep.SSHClient,
	}
}

// Execute 执行卸载 Agent 的完整流程，包括停止服务、删除文件和清理配置。
func (u *UninstallAgentUsecase) Execute(ctx context.Context, req UninstallAgentRequest) (UninstallAgentResponse, error) {
	if strings.TrimSpace(req.MachineID) == "" {
		return UninstallAgentResponse{}, errors.New("machine_id is required")
	}
	machine, ok, err := u.machineRepo.GetByID(ctx, req.MachineID)
	if err != nil {
		return UninstallAgentResponse{}, err
	}
	if !ok {
		return UninstallAgentResponse{}, errors.New("machine not found")
	}
	agent, ok, err := u.agentRepo.GetByMachineID(ctx, machine.ID)
	if err != nil {
		return UninstallAgentResponse{}, err
	}
	installDir := agentdomain.ResolveInstallDir(machine.SSHUser, machine.AgentInstallDir)
	if ok && strings.TrimSpace(agent.InstallDir) != "" {
		installDir = agent.InstallDir
	}
	endpoint := machinedomain.Endpoint{IP: machine.IP, SSHPort: machine.SSHPort}
	auth := machinedomain.SSHAuth{User: machine.SSHUser}

	if ok {
		_ = u.agentRepo.UpdateState(ctx, machine.ID, agentdomain.StateOffline, "")
	}
	_ = u.machineRepo.UpdateStatus(ctx, machine.ID, machinedomain.StatusAgentInstalling, "")

	commands := []struct {
		command string
		errMsg  string
	}{
		{
			command: "systemctl disable --now gmha-agent || systemctl stop gmha-agent || true",
			errMsg:  "failed to stop gmha-agent service",
		},
		{
			command: "rm -f /etc/systemd/system/gmha-agent.service && systemctl daemon-reload",
			errMsg:  "failed to remove gmha-agent systemd unit",
		},
		{
			command: fmt.Sprintf("rm -rf %s", shellQuote(installDir)),
			errMsg:  fmt.Sprintf("failed to remove install dir %s", installDir),
		},
	}
	for _, item := range commands {
		if err := u.sshClient.Run(ctx, endpoint, auth, item.command); err != nil {
			msg := fmt.Sprintf("%s: %v", item.errMsg, err)
			if ok {
				_ = u.agentRepo.UpdateState(ctx, machine.ID, agentdomain.StateError, msg)
			}
			_ = u.machineRepo.UpdateStatus(ctx, machine.ID, machinedomain.StatusAgentError, msg)
			return UninstallAgentResponse{}, errors.New(msg)
		}
	}

	if err := u.agentRepo.DeleteByMachineID(ctx, machine.ID); err != nil {
		return UninstallAgentResponse{}, err
	}
	if err := u.machineRepo.UpdateStatus(ctx, machine.ID, machinedomain.StatusSSHTrustReady, ""); err != nil {
		return UninstallAgentResponse{}, err
	}
	return UninstallAgentResponse{
		MachineID:  machine.ID,
		InstallDir: installDir,
		FinalState: string(machinedomain.StatusSSHTrustReady),
	}, nil
}
