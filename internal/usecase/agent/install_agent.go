// Package agent 是 Agent 管理的应用服务层，负责 Agent 的安装、卸载和升级等用例的编排。
package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	agentdomain "gmha/internal/domain/agent"
	machinedomain "gmha/internal/domain/machine"
)

// SSHClient 定义了 SSH 远程操作接口，用于在目标机器上执行命令、获取输出和上传文件。
type SSHClient interface {
	Run(ctx context.Context, endpoint machinedomain.Endpoint, auth machinedomain.SSHAuth, command string) error
	RunOutput(ctx context.Context, endpoint machinedomain.Endpoint, auth machinedomain.SSHAuth, command string) ([]byte, error)
	Upload(ctx context.Context, endpoint machinedomain.Endpoint, auth machinedomain.SSHAuth, remotePath string, content []byte, perm string) error
}

// Renderer 定义了配置渲染接口，用于生成 Agent 配置文件和 Systemd 服务单元文件。
type Renderer interface {
	RenderAgentConfig(input AgentConfigRenderInput) ([]byte, error)
	RenderSystemd(input SystemdRenderInput) ([]byte, error)
}

// MachineRepository 定义了机器仓储接口，用于查询和更新机器信息。
type MachineRepository interface {
	GetByID(ctx context.Context, machineID string) (machinedomain.Machine, bool, error)
	UpdateStatus(ctx context.Context, machineID string, status machinedomain.Status, lastError string) error
}

// AgentRepository 定义了 Agent 仓储接口，用于保存、查询和更新 Agent 信息。
type AgentRepository interface {
	Save(ctx context.Context, agent agentdomain.Agent) (agentdomain.Agent, error)
	GetByMachineID(ctx context.Context, machineID string) (agentdomain.Agent, bool, error)
	UpdateState(ctx context.Context, machineID string, state agentdomain.State, lastError string) error
}

// RegistrationWaiter 定义了 Agent 注册等待接口，用于等待 Agent 上线。
type RegistrationWaiter interface {
	WaitForOnline(ctx context.Context, machineID string, timeout time.Duration) error
}

// InstallAgentRequest 是安装 Agent 的请求参数，包含机器 ID、SSH 凭证和安装配置。
type InstallAgentRequest struct {
	MachineID       string
	IP              string
	SSHUser         string
	SSHPassword     string
	InstallDir      string
	Version         string
	ManagerHTTPAddr string
	ManagerGRPCAddr string
}

// InstallAgentResponse 是安装 Agent 的响应结果，包含安装后的状态信息。
type InstallAgentResponse struct {
	MachineID  string
	AgentID    string
	InstallDir string
	FinalState string
	LastError  string
}

// AgentConfigRenderInput 是 Agent 配置文件渲染的输入参数。
type AgentConfigRenderInput struct {
	AgentID           string
	MachineID         string
	MachineIP         string
	InstallDir        string
	ManagerMode       string
	ManagerHTTPAddr   string
	ManagerGRPCAddr   string
	HeartbeatInterval string
	Token             string
}

// SystemdRenderInput 是 Systemd 服务单元文件渲染的输入参数。
type SystemdRenderInput struct {
	InstallDir string
}

// Dependencies 是安装 Agent 用例所需的外部依赖集合。
type Dependencies struct {
	MachineRepo MachineRepository
	AgentRepo   AgentRepository
	SSHClient   SSHClient
	Renderer    Renderer
	Waiter      RegistrationWaiter
}

// InstallAgentUsecase 是安装 Agent 的用例，负责通过 SSH 将 Agent 部署到目标机器。
type InstallAgentUsecase struct {
	machineRepo MachineRepository
	agentRepo   AgentRepository
	sshClient   SSHClient
	renderer    Renderer
	waiter      RegistrationWaiter
}

// NewInstallAgentUsecase 创建一个新的安装 Agent 用例实例。
func NewInstallAgentUsecase(dep Dependencies) *InstallAgentUsecase {
	return &InstallAgentUsecase{
		machineRepo: dep.MachineRepo,
		agentRepo:   dep.AgentRepo,
		sshClient:   dep.SSHClient,
		renderer:    dep.Renderer,
		waiter:      dep.Waiter,
	}
}

// Execute 执行安装 Agent 的完整流程，包括验证参数、上传二进制文件、配置和启动服务。
func (u *InstallAgentUsecase) Execute(ctx context.Context, req InstallAgentRequest, binary []byte) (InstallAgentResponse, error) {
	if strings.TrimSpace(req.MachineID) == "" {
		return InstallAgentResponse{}, errors.New("machine_id is required")
	}
	if len(binary) == 0 {
		return InstallAgentResponse{}, errors.New("agent binary is required")
	}
	machine, ok, err := u.machineRepo.GetByID(ctx, req.MachineID)
	if err != nil {
		return InstallAgentResponse{}, err
	}
	if !ok {
		return InstallAgentResponse{}, errors.New("machine not found")
	}
	if machine.Status != machinedomain.StatusSSHTrustReady && machine.Status != machinedomain.StatusAgentError {
		return InstallAgentResponse{}, fmt.Errorf("machine status %s does not allow agent installation", machine.Status)
	}
	installDir := agentdomain.ResolveInstallDir(machine.SSHUser, req.InstallDir)

	agentID := "agent-" + machine.ID
	endpoint := machinedomain.Endpoint{IP: machine.IP, SSHPort: machine.SSHPort}
	sshUser := strings.TrimSpace(req.SSHUser)
	if sshUser == "" {
		sshUser = machine.SSHUser
	}
	auth := machinedomain.SSHAuth{User: sshUser, Password: req.SSHPassword}
	managerGRPCAddr := strings.TrimSpace(req.ManagerGRPCAddr)
	if managerGRPCAddr == "" {
		managerGRPCAddr, err = u.detectManagerGRPCAddr(ctx, endpoint, auth)
		if err != nil {
			return u.fail(ctx, machine.ID, installDir, agentID, err)
		}
	}

	configBytes, err := u.renderer.RenderAgentConfig(AgentConfigRenderInput{
		AgentID:           agentID,
		MachineID:         machine.ID,
		MachineIP:         machine.IP,
		InstallDir:        installDir,
		ManagerMode:       "grpc",
		ManagerHTTPAddr:   req.ManagerHTTPAddr,
		ManagerGRPCAddr:   managerGRPCAddr,
		HeartbeatInterval: "5s",
		Token:             "",
	})
	if err != nil {
		return InstallAgentResponse{}, err
	}
	systemdBytes, err := u.renderer.RenderSystemd(SystemdRenderInput{InstallDir: installDir})
	if err != nil {
		return InstallAgentResponse{}, err
	}

	_ = u.machineRepo.UpdateStatus(ctx, machine.ID, machinedomain.StatusAgentInstalling, "")
	_, _ = u.agentRepo.Save(ctx, agentdomain.Agent{
		ID:         agentID,
		MachineID:  machine.ID,
		InstallDir: installDir,
		Version:    req.Version,
		State:      agentdomain.StateInstalling,
	})

	steps := []struct {
		command string
		errMsg  string
	}{
		{
			command: fmt.Sprintf("mkdir -p %s %s/logs && chmod 755 %s", shellQuote(installDir), shellQuote(installDir), shellQuote(installDir)),
			errMsg:  fmt.Sprintf("failed to prepare install dir %s", installDir),
		},
		{
			command: "test -d /etc/systemd/system && test -w /etc/systemd/system",
			errMsg:  "no permission to write /etc/systemd/system; check ssh user privileges",
		},
	}
	for _, step := range steps {
		if err := u.sshClient.Run(ctx, endpoint, auth, step.command); err != nil {
			return u.fail(ctx, machine.ID, installDir, agentID, fmt.Errorf("%s: %w", step.errMsg, err))
		}
	}
	if err := u.sshClient.Upload(ctx, endpoint, auth, installDir+"/agentd", binary, "0755"); err != nil {
		return u.fail(ctx, machine.ID, installDir, agentID, fmt.Errorf("failed to upload agent binary to %s: %w", installDir, err))
	}
	if err := u.sshClient.Upload(ctx, endpoint, auth, installDir+"/agent.yaml", configBytes, "0644"); err != nil {
		return u.fail(ctx, machine.ID, installDir, agentID, fmt.Errorf("failed to upload agent config to %s: %w", installDir, err))
	}
	if err := u.sshClient.Upload(ctx, endpoint, auth, "/etc/systemd/system/gmha-agent.service", systemdBytes, "0644"); err != nil {
		return u.fail(ctx, machine.ID, installDir, agentID, fmt.Errorf("failed to upload systemd unit: %w", err))
	}
	for _, cmd := range []string{
		"systemctl daemon-reload",
		"systemctl enable gmha-agent",
		"systemctl restart gmha-agent || systemctl start gmha-agent",
	} {
		if err := u.sshClient.Run(ctx, endpoint, auth, cmd); err != nil {
			return u.fail(ctx, machine.ID, installDir, agentID, err)
		}
	}

	if u.waiter != nil {
		waitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		if err := u.waiter.WaitForOnline(waitCtx, machine.ID, 30*time.Second); err != nil {
			return u.fail(ctx, machine.ID, installDir, agentID, u.wrapStartupFailure(ctx, endpoint, auth, err))
		}
	}

	_ = u.machineRepo.UpdateStatus(ctx, machine.ID, machinedomain.StatusAgentOnline, "")
	_ = u.agentRepo.UpdateState(ctx, machine.ID, agentdomain.StateOnline, "")
	return InstallAgentResponse{
		MachineID:  machine.ID,
		AgentID:    agentID,
		InstallDir: installDir,
		FinalState: string(agentdomain.StateOnline),
	}, nil
}

// fail 处理安装失败的情况，更新机器和 Agent 的状态为错误状态。
func (u *InstallAgentUsecase) fail(ctx context.Context, machineID, installDir, agentID string, err error) (InstallAgentResponse, error) {
	msg := err.Error()
	_ = u.machineRepo.UpdateStatus(ctx, machineID, machinedomain.StatusAgentError, msg)
	_ = u.agentRepo.UpdateState(ctx, machineID, agentdomain.StateError, msg)
	return InstallAgentResponse{
		MachineID:  machineID,
		AgentID:    agentID,
		InstallDir: installDir,
		FinalState: string(agentdomain.StateError),
		LastError:  msg,
	}, err
}

// shellQuote 对字符串进行 Shell 引号转义，防止命令注入。
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

// detectManagerGRPCAddr 通过 SSH 会话检测 Manager 的 gRPC 地址，用于自动发现。
func (u *InstallAgentUsecase) detectManagerGRPCAddr(ctx context.Context, endpoint machinedomain.Endpoint, auth machinedomain.SSHAuth) (string, error) {
	output, err := u.sshClient.RunOutput(ctx, endpoint, auth, `sh -lc 'v="${SSH_CONNECTION:-$SSH_CLIENT}"; set -- $v; printf "%s" "$1"'`)
	if err != nil {
		return "", fmt.Errorf("failed to detect manager source ip from ssh session: %w", err)
	}
	host := strings.TrimSpace(string(output))
	if host == "" {
		return "", errors.New("failed to detect manager source ip from ssh session")
	}
	return host + ":9100", nil
}

// wrapStartupFailure 在 Agent 启动失败时收集 systemctl 和 journalctl 的诊断信息，包装成详细的错误信息。
func (u *InstallAgentUsecase) wrapStartupFailure(ctx context.Context, endpoint machinedomain.Endpoint, auth machinedomain.SSHAuth, baseErr error) error {
	parts := []string{baseErr.Error()}
	if output, err := u.sshClient.RunOutput(ctx, endpoint, auth, "systemctl status gmha-agent --no-pager --full -n 20 || true"); err == nil {
		if text := strings.TrimSpace(string(output)); text != "" {
			parts = append(parts, "systemctl: "+singleLine(text))
		}
	}
	if output, err := u.sshClient.RunOutput(ctx, endpoint, auth, "journalctl -u gmha-agent -n 30 --no-pager || true"); err == nil {
		if text := strings.TrimSpace(string(output)); text != "" {
			parts = append(parts, "journalctl: "+singleLine(text))
		}
	}
	return errors.New(strings.Join(parts, " | "))
}

// singleLine 将多行文本转换为单行，便于在错误信息中展示。
func singleLine(s string) string {
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ; ")
	return strings.TrimSpace(s)
}
