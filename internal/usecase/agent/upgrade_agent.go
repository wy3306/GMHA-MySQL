package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	agentdomain "gmha/internal/domain/agent"
	credentialdomain "gmha/internal/domain/credential"
	machinedomain "gmha/internal/domain/machine"
)

// UpgradeMachineRepository 定义了升级场景下的机器仓储接口。
type UpgradeMachineRepository interface {
	GetByID(ctx context.Context, machineID string) (machinedomain.Machine, bool, error)
	UpdateStatus(ctx context.Context, machineID string, status machinedomain.Status, lastError string) error
}

// UpgradeAgentRepository 定义了升级场景下的 Agent 仓储接口。
type UpgradeAgentRepository interface {
	GetByMachineID(ctx context.Context, machineID string) (agentdomain.Agent, bool, error)
	Save(ctx context.Context, agent agentdomain.Agent) (agentdomain.Agent, error)
	UpdateState(ctx context.Context, machineID string, state agentdomain.State, lastError string) error
}

// HeartbeatReader 定义了心跳读取接口，用于等待 Agent 升级后的新鲜心跳。
type HeartbeatReader interface {
	WaitForFreshHeartbeat(ctx context.Context, machineID string, startedAt time.Time, timeout time.Duration) error
}

// UpgradeDependencies 是升级 Agent 用例所需的外部依赖集合。
type UpgradeDependencies struct {
	MachineRepo    UpgradeMachineRepository
	AgentRepo      UpgradeAgentRepository
	CredentialRepo credentialdomain.Repository
	SSHClient      SSHClient
	Renderer       Renderer
	Heartbeat      HeartbeatReader
}

// UpgradeAgentRequest 是升级 Agent 的请求参数。
type UpgradeAgentRequest struct {
	MachineID       string
	IP              string
	Version         string
	ManagerHTTPAddr string
	ManagerGRPCAddr string
}

// UpgradeAgentResponse 是升级 Agent 的响应结果。
type UpgradeAgentResponse struct {
	MachineID  string `json:"machine_id"`
	AgentID    string `json:"agent_id"`
	InstallDir string `json:"install_dir"`
	FinalState string `json:"final_state"`
	LastError  string `json:"last_error,omitempty"`
}

// UpgradeAgentUsecase 是升级 Agent 的用例，负责通过 SSH 将新版本 Agent 部署到目标机器。
type UpgradeAgentUsecase struct {
	machineRepo    UpgradeMachineRepository
	agentRepo      UpgradeAgentRepository
	credentialRepo credentialdomain.Repository
	sshClient      SSHClient
	renderer       Renderer
	heartbeat      HeartbeatReader
}

// NewUpgradeAgentUsecase 创建一个新的升级 Agent 用例实例。
func NewUpgradeAgentUsecase(dep UpgradeDependencies) *UpgradeAgentUsecase {
	return &UpgradeAgentUsecase{
		machineRepo:    dep.MachineRepo,
		agentRepo:      dep.AgentRepo,
		credentialRepo: dep.CredentialRepo,
		sshClient:      dep.SSHClient,
		renderer:       dep.Renderer,
		heartbeat:      dep.Heartbeat,
	}
}

// Execute 执行升级 Agent 的完整流程，包括验证参数、替换二进制文件和重启服务。
func (u *UpgradeAgentUsecase) Execute(ctx context.Context, req UpgradeAgentRequest, binary []byte) (UpgradeAgentResponse, error) {
	if strings.TrimSpace(req.MachineID) == "" {
		return UpgradeAgentResponse{}, errors.New("machine_id is required")
	}
	if len(binary) == 0 {
		return UpgradeAgentResponse{}, errors.New("agent binary is required")
	}
	machine, ok, err := u.machineRepo.GetByID(ctx, req.MachineID)
	if err != nil {
		return UpgradeAgentResponse{}, err
	}
	if !ok {
		return UpgradeAgentResponse{}, errors.New("machine not found")
	}
	if machine.Status != machinedomain.StatusAgentOnline &&
		machine.Status != machinedomain.StatusAgentError &&
		machine.Status != machinedomain.StatusSSHTrustReady {
		return UpgradeAgentResponse{}, fmt.Errorf("machine status %s does not allow agent upgrade", machine.Status)
	}

	agent, ok, err := u.agentRepo.GetByMachineID(ctx, machine.ID)
	if err != nil {
		return UpgradeAgentResponse{}, err
	}
	if !ok {
		return UpgradeAgentResponse{}, errors.New("agent not found")
	}

	installDir := agentdomain.ResolveInstallDir(machine.SSHUser, agent.InstallDir)
	if strings.TrimSpace(installDir) == "" {
		installDir = agentdomain.ResolveInstallDir(machine.SSHUser, machine.AgentInstallDir)
	}
	managerGRPCAddr := strings.TrimSpace(req.ManagerGRPCAddr)
	if managerGRPCAddr == "" {
		return UpgradeAgentResponse{}, errors.New("manager_grpc_addr is required")
	}

	configBytes, err := u.renderer.RenderAgentConfig(AgentConfigRenderInput{
		AgentID:           agent.ID,
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
		return UpgradeAgentResponse{}, err
	}
	systemdBytes, err := u.renderer.RenderSystemd(SystemdRenderInput{InstallDir: installDir})
	if err != nil {
		return UpgradeAgentResponse{}, err
	}

	endpoint := machinedomain.Endpoint{IP: machine.IP, SSHPort: machine.SSHPort}
	auth := machinedomain.SSHAuth{User: machine.SSHUser}
	if strings.TrimSpace(machine.CredentialID) != "" && u.credentialRepo != nil {
		credential, found, credentialErr := u.credentialRepo.GetByID(ctx, machine.CredentialID)
		if credentialErr != nil {
			return u.fail(ctx, machine.ID, installDir, agent.ID, fmt.Errorf("load SSH credential: %w", credentialErr))
		}
		if found {
			auth = machinedomain.SSHAuth{
				User:       credential.SSHUser,
				Password:   credential.SSHPassword,
				PrivateKey: credential.PrivateKey,
				Passphrase: credential.Passphrase,
			}
		}
	}
	if strings.TrimSpace(auth.User) == "" {
		auth.User = "root"
	}

	_ = u.machineRepo.UpdateStatus(ctx, machine.ID, machinedomain.StatusAgentInstalling, "")
	_, _ = u.agentRepo.Save(ctx, agentdomain.Agent{
		ID:         agent.ID,
		MachineID:  machine.ID,
		InstallDir: installDir,
		Version:    req.Version,
		State:      agentdomain.StateInstalling,
	})

	for _, step := range []struct {
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
	} {
		if err := u.sshClient.Run(ctx, endpoint, auth, step.command); err != nil {
			return u.fail(ctx, machine.ID, installDir, agent.ID, fmt.Errorf("%s: %w", step.errMsg, err))
		}
	}

	tmpAgentPath := fmt.Sprintf("%s/.agentd.%d.tmp", installDir, time.Now().UnixNano())
	backupAgentPath := fmt.Sprintf("%s/agentd.backup-%s", installDir, strings.ReplaceAll(strings.TrimSpace(agent.Version), "/", "_"))
	if err := u.sshClient.Upload(ctx, endpoint, auth, tmpAgentPath, binary, "0755"); err != nil {
		return u.fail(ctx, machine.ID, installDir, agent.ID, fmt.Errorf("failed to upload agent binary to %s: %w", installDir, err))
	}
	if err := u.sshClient.Run(ctx, endpoint, auth, fmt.Sprintf("chmod 0755 %s && if test -f %s; then cp -p %s %s; fi && mv -f %s %s", shellQuote(tmpAgentPath), shellQuote(installDir+"/agentd"), shellQuote(installDir+"/agentd"), shellQuote(backupAgentPath), shellQuote(tmpAgentPath), shellQuote(installDir+"/agentd"))); err != nil {
		return u.fail(ctx, machine.ID, installDir, agent.ID, fmt.Errorf("failed to replace agent binary: %w", err))
	}
	rollback := func(baseErr error) (UpgradeAgentResponse, error) {
		rollbackCmd := fmt.Sprintf("if test -f %s; then cp -p %s %s && systemctl restart gmha-agent; fi", shellQuote(backupAgentPath), shellQuote(backupAgentPath), shellQuote(installDir+"/agentd"))
		rollbackCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if rollbackErr := u.sshClient.Run(rollbackCtx, endpoint, auth, rollbackCmd); rollbackErr != nil {
			baseErr = fmt.Errorf("%w; automatic rollback failed: %v", baseErr, rollbackErr)
		} else {
			baseErr = fmt.Errorf("%w; previous Agent binary restored", baseErr)
		}
		return u.fail(ctx, machine.ID, installDir, agent.ID, baseErr)
	}
	if err := u.sshClient.Upload(ctx, endpoint, auth, installDir+"/agent.yaml", configBytes, "0644"); err != nil {
		return rollback(fmt.Errorf("failed to upload agent config to %s: %w", installDir, err))
	}
	if err := u.sshClient.Upload(ctx, endpoint, auth, "/etc/systemd/system/gmha-agent.service", systemdBytes, "0644"); err != nil {
		return rollback(fmt.Errorf("failed to upload systemd unit: %w", err))
	}
	startedAt := time.Now().UTC()
	for _, cmd := range []string{
		"systemctl daemon-reload",
		"systemctl enable gmha-agent",
		"systemctl restart gmha-agent",
	} {
		if err := u.sshClient.Run(ctx, endpoint, auth, cmd); err != nil {
			return rollback(err)
		}
	}

	if u.heartbeat != nil {
		waitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		if err := u.heartbeat.WaitForFreshHeartbeat(waitCtx, machine.ID, startedAt, 30*time.Second); err != nil {
			return rollback(u.wrapStartupFailure(ctx, endpoint, auth, err))
		}
	}

	_ = u.machineRepo.UpdateStatus(ctx, machine.ID, machinedomain.StatusAgentOnline, "")
	_ = u.agentRepo.UpdateState(ctx, machine.ID, agentdomain.StateOnline, "")
	return UpgradeAgentResponse{
		MachineID:  machine.ID,
		AgentID:    agent.ID,
		InstallDir: installDir,
		FinalState: string(agentdomain.StateOnline),
	}, nil
}

// fail 处理升级失败的情况，更新机器和 Agent 的状态为错误状态。
func (u *UpgradeAgentUsecase) fail(ctx context.Context, machineID, installDir, agentID string, err error) (UpgradeAgentResponse, error) {
	msg := err.Error()
	_ = u.machineRepo.UpdateStatus(ctx, machineID, machinedomain.StatusAgentError, msg)
	_ = u.agentRepo.UpdateState(ctx, machineID, agentdomain.StateError, msg)
	return UpgradeAgentResponse{
		MachineID:  machineID,
		AgentID:    agentID,
		InstallDir: installDir,
		FinalState: string(agentdomain.StateError),
		LastError:  msg,
	}, err
}

// wrapStartupFailure 在 Agent 升级后启动失败时收集诊断信息，包装成详细的错误信息。
func (u *UpgradeAgentUsecase) wrapStartupFailure(ctx context.Context, endpoint machinedomain.Endpoint, auth machinedomain.SSHAuth, baseErr error) error {
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
