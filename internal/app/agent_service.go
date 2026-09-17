package app

import (
	"bytes"
	"context"
	"debug/elf"
	"errors"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"encoding/json"
	"fmt"
	"gmha/internal/agent/mysqlcheck"
	"gmha/internal/buildinfo"
	agentdomain "gmha/internal/domain/agent"
	credentialdomain "gmha/internal/domain/credential"
	dynamicdomain "gmha/internal/domain/dynamic"
	hbdomain "gmha/internal/domain/heartbeat"
	machinedomain "gmha/internal/domain/machine"
	taskdomain "gmha/internal/domain/task"
	agentusecase "gmha/internal/usecase/agent"
)

// AgentService 是 Agent 管理服务，负责 Agent 的安装、升级、卸载、重试安装、
// 平台检测、二进制构建、心跳管理和 MySQL 配置修复等。
type AgentService struct {
	packages        *PackageService
	repo            agentdomain.Repository
	machineRepo     machinedomain.Repository
	credentialRepo  credentialdomain.Repository
	sshClient       agentusecase.SSHClient
	heartbeat       *HeartbeatService
	recovery        *RecoveryService
	installer       *agentusecase.InstallAgentUsecase
	upgrader        *agentusecase.UpgradeAgentUsecase
	uninstaller     *agentusecase.UninstallAgentUsecase
	taskService     *TaskService
	mysqlService    *MySQLService
	binaryPath      string
	managerHTTPAddr string
	managerGRPCAddr string
	upgradeMu       sync.Mutex
	upgrading       map[string]struct{}
}

// SetCredentialRepository configures the SSH credential source used by Agent
// lifecycle operations. It is kept as a setter to preserve the constructor's
// compatibility with existing callers and tests.
func (s *AgentService) SetCredentialRepository(repo credentialdomain.Repository) {
	s.credentialRepo = repo
}

func (s *AgentService) machineSSHAuth(ctx context.Context, machine machinedomain.Machine) (machinedomain.SSHAuth, error) {
	auth := machinedomain.SSHAuth{User: strings.TrimSpace(machine.SSHUser)}
	if strings.TrimSpace(machine.CredentialID) != "" && s.credentialRepo != nil {
		credential, found, err := s.credentialRepo.GetByID(ctx, machine.CredentialID)
		if err != nil {
			return auth, fmt.Errorf("load SSH credential: %w", err)
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
	return auth, nil
}

// NewAgentService 创建 Agent 管理服务实例。
func NewAgentService(repo agentdomain.Repository, machineRepo machinedomain.Repository, sshClient agentusecase.SSHClient, heartbeat *HeartbeatService, recovery *RecoveryService, installer *agentusecase.InstallAgentUsecase, upgrader *agentusecase.UpgradeAgentUsecase, uninstaller *agentusecase.UninstallAgentUsecase, taskService *TaskService, mysqlService *MySQLService, binaryPath, managerHTTPAddr, managerGRPCAddr string) *AgentService {
	return &AgentService{
		repo:            repo,
		machineRepo:     machineRepo,
		sshClient:       sshClient,
		heartbeat:       heartbeat,
		recovery:        recovery,
		installer:       installer,
		upgrader:        upgrader,
		uninstaller:     uninstaller,
		taskService:     taskService,
		mysqlService:    mysqlService,
		binaryPath:      binaryPath,
		managerHTTPAddr: managerHTTPAddr,
		managerGRPCAddr: managerGRPCAddr,
		upgrading:       make(map[string]struct{}),
	}
}

// List 返回所有 Agent 实体列表。
func (s *AgentService) List(ctx context.Context) ([]agentdomain.Agent, error) {
	return s.repo.List(ctx)
}

// AgentView 是 Agent 的聚合展示视图，关联了机器状态、心跳状态、恢复状态等信息。
type AgentView struct {
	Name              string                       `json:"name"`
	IP                string                       `json:"ip"`
	Cluster           string                       `json:"cluster"`
	MachineStatus     string                       `json:"machine_status"`
	InstallState      string                       `json:"install_state"`
	HeartbeatState    string                       `json:"heartbeat_state"`
	OverallHealth     string                       `json:"overall_health"`
	LastHeartbeatAt   string                       `json:"last_heartbeat_at"`
	LastStateChangeAt string                       `json:"last_state_change_at"`
	RecoveryState     string                       `json:"recovery_state"`
	SuppressedUntil   string                       `json:"suppressed_until"`
	InstallDir        string                       `json:"install_dir"`
	Version           string                       `json:"version"`
	LastError         string                       `json:"last_error"`
	CheckSummary      string                       `json:"check_summary"`
	Checks            []hbdomain.HealthCheck       `json:"checks"`
	Metrics           []dynamicdomain.MetricResult `json:"metrics"`
}

// ListViews 返回所有 Agent 的聚合视图，关联机器、心跳和恢复状态。
func (s *AgentService) ListViews(ctx context.Context) ([]AgentView, error) {
	machines, err := s.machineRepo.List(ctx)
	if err != nil {
		return nil, err
	}
	agents, err := s.repo.List(ctx)
	if err != nil {
		return nil, err
	}
	agentByMachineID := make(map[string]agentdomain.Agent, len(agents))
	for _, item := range agents {
		agentByMachineID[item.MachineID] = item
	}
	heartbeatByMachineID := make(map[string]HeartbeatView)
	if s.heartbeat != nil {
		for _, item := range s.heartbeat.Snapshot() {
			heartbeatByMachineID[item.MachineID] = item
		}
	}
	recoveryByMachineID := make(map[string]string)
	suppressedByMachineID := make(map[string]string)
	if s.recovery != nil {
		snapshot, err := s.recovery.LatestSnapshot(ctx)
		if err != nil {
			return nil, err
		}
		for machineID, item := range snapshot {
			switch {
			case item.InProgress:
				recoveryByMachineID[machineID] = "recovering"
			case item.SuppressedUntil != nil && item.SuppressedUntil.After(time.Now()):
				recoveryByMachineID[machineID] = "suppressed"
				suppressedByMachineID[machineID] = item.SuppressedUntil.Local().Format("2006-01-02 15:04:05")
			case strings.TrimSpace(item.LastResult) != "":
				recoveryByMachineID[machineID] = "idle"
			}
		}
	}
	out := make([]AgentView, 0, len(machines))
	for _, machine := range machines {
		view := AgentView{
			Name:            machine.Name,
			IP:              machine.IP,
			Cluster:         machine.Cluster,
			MachineStatus:   string(machine.Status),
			InstallState:    "-",
			HeartbeatState:  "INIT",
			OverallHealth:   "-",
			RecoveryState:   "-",
			SuppressedUntil: "-",
			InstallDir:      agentdomain.ResolveInstallDir(machine.SSHUser, machine.AgentInstallDir),
			LastError:       machine.LastError,
		}
		if agent, ok := agentByMachineID[machine.ID]; ok {
			view.InstallState = string(agent.State)
			view.Version = agent.Version
			if strings.TrimSpace(agent.InstallDir) != "" {
				view.InstallDir = agent.InstallDir
			}
			if strings.TrimSpace(agent.LastError) != "" {
				view.LastError = agent.LastError
			}
		}
		if hb, ok := heartbeatByMachineID[machine.ID]; ok {
			view.Version = hb.Version
			view.HeartbeatState = string(hb.CurrentState)
			view.OverallHealth = string(hb.OverallHealth)
			if !hb.LastHeartbeatAt.IsZero() {
				view.LastHeartbeatAt = hb.LastHeartbeatAt.Format("2006-01-02 15:04:05")
			}
			if !hb.LastStateChangeAt.IsZero() {
				view.LastStateChangeAt = hb.LastStateChangeAt.Format("2006-01-02 15:04:05")
			}
			if strings.TrimSpace(hb.LastErrorSummary) != "" {
				view.LastError = hb.LastErrorSummary
			}
			view.Checks = append([]hbdomain.HealthCheck(nil), hb.Checks...)
			view.Metrics = append([]dynamicdomain.MetricResult(nil), hb.Metrics...)
			view.CheckSummary = healthCheckSummary(hb.Checks)
			if status, _, authoritative := machineStatusFromHeartbeat(hb.CurrentState, hb.LastErrorSummary); authoritative {
				view.MachineStatus = string(status)
			}
		}
		if state, ok := recoveryByMachineID[machine.ID]; ok {
			view.RecoveryState = state
		}
		if until, ok := suppressedByMachineID[machine.ID]; ok {
			view.SuppressedUntil = until
		}
		out = append(out, view)
	}
	return out, nil
}

func healthCheckSummary(checks []hbdomain.HealthCheck) string {
	if len(checks) == 0 {
		return "-"
	}
	parts := make([]string, 0, len(checks))
	for _, check := range checks {
		if check.Status == hbdomain.CheckOK {
			continue
		}
		detail := strings.TrimSpace(check.Detail)
		if detail != "" {
			parts = append(parts, string(check.Status)+":"+check.Name+"("+detail+")")
		} else {
			parts = append(parts, string(check.Status)+":"+check.Name)
		}
	}
	if len(parts) == 0 {
		return "OK"
	}
	return strings.Join(parts, "; ")
}

// ListInstallCandidates 列出可安装 Agent 的候选机器（已分配集群、SSH 就绪、Agent 未在线）。
func (s *AgentService) ListInstallCandidates(ctx context.Context) ([]AgentView, error) {
	items, err := s.ListViews(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]AgentView, 0)
	for _, item := range items {
		if strings.TrimSpace(item.Cluster) == "" {
			continue
		}
		if item.MachineStatus != string(machinedomain.StatusSSHTrustReady) && item.MachineStatus != string(machinedomain.StatusAgentError) {
			continue
		}
		if item.HeartbeatState == "ONLINE" {
			continue
		}
		out = append(out, item)
	}
	return out, nil
}

// GetByIP 按 IP 获取 Agent 实体。
func (s *AgentService) GetByIP(ctx context.Context, ip string) (agentdomain.Agent, bool, error) {
	machine, ok, err := s.resolveMachineByIP(ctx, ip)
	if err != nil || !ok {
		return agentdomain.Agent{}, ok, err
	}
	return s.repo.GetByMachineID(ctx, machine.ID)
}

// GetViewByIP 按 IP 获取 Agent 聚合视图。
func (s *AgentService) GetViewByIP(ctx context.Context, ip string) (AgentView, bool, error) {
	ip = strings.TrimSpace(ip)
	if ip == "" {
		return AgentView{}, false, errors.New("ip is required")
	}
	items, err := s.ListViews(ctx)
	if err != nil {
		return AgentView{}, false, err
	}
	for _, item := range items {
		if item.IP == ip {
			return item, true, nil
		}
	}
	return AgentView{}, false, nil
}

// RetryInstallByIP 重试安装指定 IP 机器上的 Agent，会自动检测目标平台并构建对应的二进制。
func (s *AgentService) RetryInstallByIP(ctx context.Context, req agentusecase.InstallAgentRequest) (agentusecase.InstallAgentResponse, error) {
	if s.installer == nil {
		return agentusecase.InstallAgentResponse{}, errors.New("installer not configured")
	}
	machine, ok, err := s.resolveMachineByIP(ctx, req.IP)
	if err != nil {
		return agentusecase.InstallAgentResponse{}, err
	}
	if !ok {
		return agentusecase.InstallAgentResponse{}, errors.New("machine not found")
	}
	// Agent 是后续资源采集与集群编排的基础能力，允许在分配集群前完成安装。
	agent, agentFound, err := s.repo.GetByMachineID(ctx, machine.ID)
	if err != nil {
		return agentusecase.InstallAgentResponse{}, err
	}
	if agentFound && agent.State == agentdomain.StateOnline {
		return agentusecase.InstallAgentResponse{}, errors.New("agent is already online")
	}
	req.MachineID = machine.ID
	if strings.TrimSpace(req.SSHUser) == "" {
		req.SSHUser = machine.SSHUser
	}
	req.ManagerHTTPAddr = ResolveManagerHTTPAddrForTarget(s.managerHTTPAddr, machine.IP)
	req.ManagerGRPCAddr = ResolveManagerGRPCAddrForTarget("", s.managerGRPCAddr, machine.IP)
	if strings.TrimSpace(req.InstallDir) == "" && agentFound && strings.TrimSpace(agent.InstallDir) != "" {
		req.InstallDir = agent.InstallDir
	}
	req.InstallDir = agentdomain.ResolveInstallDir(machine.SSHUser, req.InstallDir)
	if strings.TrimSpace(req.Version) == "" {
		req.Version = buildinfo.CurrentVersion()
	}
	targetOS, targetArch, err := s.detectRemotePlatform(ctx, machine)
	if err != nil {
		return agentusecase.InstallAgentResponse{}, err
	}
	binary, version, err := s.latestAgentBinary(targetOS, targetArch)
	if err != nil {
		return agentusecase.InstallAgentResponse{}, err
	}
	req.Version = version
	return s.installer.Execute(ctx, req, binary)
}

// DetectVersionByIP reads the installed Agent binary version over SSH and
// persists it. This also repairs legacy/adopted Agent records whose version was
// never reported because the process could not establish a heartbeat.
func (s *AgentService) DetectVersionByIP(ctx context.Context, ip string) (AgentView, error) {
	machine, ok, err := s.resolveMachineByIP(ctx, ip)
	if err != nil {
		return AgentView{}, err
	}
	if !ok {
		return AgentView{}, errors.New("machine not found")
	}
	if s.sshClient == nil {
		return AgentView{}, errors.New("ssh client not configured")
	}

	agent, found, err := s.repo.GetByMachineID(ctx, machine.ID)
	if err != nil {
		return AgentView{}, err
	}
	if !found {
		agent = agentdomain.Agent{ID: "agent-" + machine.ID, MachineID: machine.ID, State: agentdomain.StateOffline}
	}
	agent.InstallDir = agentdomain.ResolveInstallDir(machine.SSHUser, firstNonEmpty(agent.InstallDir, machine.AgentInstallDir))
	endpoint := machinedomain.Endpoint{IP: machine.IP, SSHPort: machine.SSHPort}
	auth, err := s.machineSSHAuth(ctx, machine)
	if err != nil {
		return AgentView{}, err
	}
	output, err := s.sshClient.RunOutput(ctx, endpoint, auth, shellQuote(strings.TrimSuffix(agent.InstallDir, "/")+"/agentd")+" --version")
	if err != nil {
		return AgentView{}, fmt.Errorf("检测 Agent 版本失败: %w", err)
	}
	version, err := detectAgentVersionOutput(output)
	if err != nil {
		return AgentView{}, err
	}
	agent.Version = version
	if _, err := s.repo.Save(ctx, agent); err != nil {
		return AgentView{}, err
	}
	view, ok, err := s.GetViewByIP(ctx, machine.IP)
	if err != nil {
		return AgentView{}, err
	}
	if !ok {
		return AgentView{}, errors.New("agent view not found after version detection")
	}
	return view, nil
}

func detectAgentVersionOutput(output []byte) (string, error) {
	for _, field := range strings.Fields(string(output)) {
		candidate := strings.Trim(field, " ,;()[]{}")
		if _, ok := parseComponentVersion(candidate); ok {
			return componentVersion(candidate), nil
		}
	}
	return "", fmt.Errorf("Agent 版本输出无法识别: %s", strings.TrimSpace(string(output)))
}

// UninstallByIP 卸载指定 IP 机器上的 Agent，并清除心跳数据。
func (s *AgentService) UninstallByIP(ctx context.Context, ip string) (agentusecase.UninstallAgentResponse, error) {
	return s.uninstallByIP(ctx, ip, nil)
}

// UninstallByIPWithAuth uses the credential already resolved by MachineService.
// Agent removal itself must use SSH because stopping the Agent also terminates
// its task channel.
func (s *AgentService) UninstallByIPWithAuth(ctx context.Context, ip string, auth machinedomain.SSHAuth) (agentusecase.UninstallAgentResponse, error) {
	return s.uninstallByIP(ctx, ip, &auth)
}

func (s *AgentService) uninstallByIP(ctx context.Context, ip string, auth *machinedomain.SSHAuth) (agentusecase.UninstallAgentResponse, error) {
	if s.uninstaller == nil {
		return agentusecase.UninstallAgentResponse{}, errors.New("uninstaller not configured")
	}
	machine, ok, err := s.resolveMachineByIP(ctx, ip)
	if err != nil {
		return agentusecase.UninstallAgentResponse{}, err
	}
	if !ok {
		return agentusecase.UninstallAgentResponse{}, errors.New("machine not found")
	}

	resp, err := s.uninstaller.Execute(ctx, agentusecase.UninstallAgentRequest{MachineID: machine.ID, SSHAuth: auth})
	if err != nil {
		return agentusecase.UninstallAgentResponse{}, err
	}
	if s.heartbeat != nil {
		if hbErr := s.heartbeat.RemoveMachine(ctx, machine.ID); hbErr != nil {
			return agentusecase.UninstallAgentResponse{}, hbErr
		}
	}
	return resp, nil
}

// UpgradeByIP 升级指定 IP 机器上的 Agent，会自动检测目标平台并构建新版本二进制。
func (s *AgentService) UpgradeByIP(ctx context.Context, ip string) (agentusecase.UpgradeAgentResponse, error) {
	if s.upgrader == nil {
		return agentusecase.UpgradeAgentResponse{}, errors.New("upgrader not configured")
	}
	machine, ok, err := s.resolveMachineByIP(ctx, ip)
	if err != nil {
		return agentusecase.UpgradeAgentResponse{}, err
	}
	if !ok {
		return agentusecase.UpgradeAgentResponse{}, errors.New("machine not found")
	}
	if !s.beginAgentUpgrade(machine.ID) {
		return agentusecase.UpgradeAgentResponse{}, errors.New("该机器的 Agent 正在升级，请勿重复提交")
	}
	defer s.finishAgentUpgrade(machine.ID)
	agent, agentFound, err := s.repo.GetByMachineID(ctx, machine.ID)
	if err != nil {
		return agentusecase.UpgradeAgentResponse{}, err
	}
	if !agentFound {
		return agentusecase.UpgradeAgentResponse{}, errors.New("agent not found")
	}
	if s.taskService != nil {
		if ready, _ := s.taskService.MachineAgentReady(machine.ID); ready {
			return s.upgradeOnlineAgent(ctx, machine, agent)
		}
	}
	targetOS, targetArch, err := s.detectRemotePlatform(ctx, machine)
	if err != nil {
		return agentusecase.UpgradeAgentResponse{}, fmt.Errorf("Agent 任务通道离线，SSH 连接 %s:%d 也失败，无法推送升级：%w；请检查目标机 SSH 服务、防火墙、安全组及 Manager 到目标网段的连通性", machine.IP, machine.SSHPort, err)
	}
	binary, version, err := s.latestAgentBinary(targetOS, targetArch)
	if err != nil {
		return agentusecase.UpgradeAgentResponse{}, err
	}
	if comparison, ok := compareComponentVersions(agent.Version, version); !ok || comparison >= 0 {
		return agentusecase.UpgradeAgentResponse{}, fmt.Errorf("当前版本 %s，目标版本 %s；请先构建发布更高版本 Agent", agent.Version, version)
	}
	return s.upgradeByIPBinary(ctx, machine, version, binary)
}

// upgradeOnlineAgent sends a delayed self-update through the already connected
// Agent task channel. This is the primary path: an online Agent does not need
// inbound SSH merely to replace its own executable.
func (s *AgentService) upgradeOnlineAgent(ctx context.Context, machine machinedomain.Machine, current agentdomain.Agent) (agentusecase.UpgradeAgentResponse, error) {
	if s.packages == nil || s.heartbeat == nil {
		return agentusecase.UpgradeAgentResponse{}, errors.New("Agent 在线升级服务未配置")
	}
	items, err := s.packages.List("gmha-agent", "")
	if err != nil {
		return agentusecase.UpgradeAgentResponse{}, err
	}
	versions := make([]string, 0, len(items))
	for _, item := range items {
		if _, ok := parseComponentVersion(item.Version); ok {
			versions = append(versions, item.Version)
		}
	}
	targetVersion := highestComponentVersion(versions...)
	if targetVersion == "" {
		return agentusecase.UpgradeAgentResponse{}, errors.New("没有可用的 Agent 升级制品")
	}
	if comparison, ok := compareComponentVersions(current.Version, targetVersion); !ok || comparison >= 0 {
		return agentusecase.UpgradeAgentResponse{}, fmt.Errorf("当前版本 %s，目标版本 %s，没有可推送的新版本", current.Version, targetVersion)
	}

	byArch := make(map[string]PackageItem)
	for _, item := range items {
		if strings.EqualFold(componentVersion(item.Version), componentVersion(targetVersion)) {
			byArch[normalizeComponentArch(item.Arch)] = item
		}
	}
	amd64, amdOK := byArch["amd64"]
	arm64, armOK := byArch["arm64"]
	if !amdOK || !armOK {
		return agentusecase.UpgradeAgentResponse{}, fmt.Errorf("Agent %s 发布不完整：AMD64 与 ARM64 制品必须同时存在", targetVersion)
	}
	if _, err := s.packages.Verify("gmha-agent", amd64.Name); err != nil {
		return agentusecase.UpgradeAgentResponse{}, err
	}
	if _, err := s.packages.Verify("gmha-agent", arm64.Name); err != nil {
		return agentusecase.UpgradeAgentResponse{}, err
	}

	installDir := agentdomain.ResolveInstallDir(machine.SSHUser, current.InstallDir)
	managerURL := strings.TrimRight(ResolveManagerHTTPAddrForTarget(s.managerHTTPAddr, machine.IP), "/")
	command := onlineAgentUpgradeCommand(installDir, managerURL, targetVersion, amd64, arm64)
	startedAt := time.Now().UTC()
	task, err := s.taskService.CreateExecTaskWithOptions(ctx, machine.IP, command, ExecTaskOptions{
		Operation: "agent_self_upgrade", DisplayName: "在线推送升级 Agent", StepName: "下载、校验并安排重启", Internal: true,
	})
	if err != nil {
		return agentusecase.UpgradeAgentResponse{}, fmt.Errorf("创建 Agent 在线升级任务失败: %w", err)
	}
	detail, err := s.taskService.WaitForTask(ctx, task.Task.ID, 45*time.Second)
	if err != nil {
		return agentusecase.UpgradeAgentResponse{}, fmt.Errorf("等待 Agent 在线升级任务失败: %w", err)
	}
	if detail.Task.Status != taskdomain.StatusSuccess {
		return agentusecase.UpgradeAgentResponse{}, fmt.Errorf("Agent 在线升级准备失败，任务 %s 状态为 %s；请在任务中心查看输出", task.Task.ID, detail.Task.Status)
	}
	if err := s.heartbeat.WaitForVersionHeartbeat(ctx, machine.ID, targetVersion, startedAt, 45*time.Second); err != nil {
		return agentusecase.UpgradeAgentResponse{}, fmt.Errorf("Agent 已接收升级，但新版本心跳未确认: %w；请查看 %s/logs/agent.log", err, installDir)
	}
	current.Version = targetVersion
	current.State = agentdomain.StateOnline
	current.LastError = ""
	if _, err := s.repo.Save(ctx, current); err != nil {
		return agentusecase.UpgradeAgentResponse{}, err
	}
	return agentusecase.UpgradeAgentResponse{MachineID: machine.ID, AgentID: current.ID, InstallDir: installDir, FinalState: string(agentdomain.StateOnline)}, nil
}

func onlineAgentUpgradeCommand(installDir, managerURL, version string, amd64, arm64 PackageItem) string {
	packageURL := func(name string) string {
		return managerURL + "/api/v1/software/packages/gmha-agent/" + url.PathEscape(name)
	}
	return fmt.Sprintf(`set -eu
dir=%s
arch=$(uname -m)
case "$arch" in
  x86_64|amd64) url=%s; sha=%s ;;
  aarch64|arm64) url=%s; sha=%s ;;
  *) echo "unsupported Agent architecture: $arch" >&2; exit 1 ;;
esac
candidate="$dir/.agentd.%s.candidate"
backup="$dir/agentd.backup-%s"
mkdir -p "$dir/logs"
if command -v curl >/dev/null 2>&1; then curl -fsSL --connect-timeout 10 --max-time 180 "$url" -o "$candidate"; elif command -v wget >/dev/null 2>&1; then wget -q -T 180 -O "$candidate" "$url"; else echo 'curl or wget is required' >&2; exit 1; fi
printf '%%s  %%s\n' "$sha" "$candidate" | sha256sum -c -
chmod 0755 "$candidate"
test "$("$candidate" --version)" = %s
cp -p "$dir/agentd" "$backup"
cat > "$dir/.apply-agent-upgrade.sh" <<'GMHA_UPGRADE'
#!/bin/sh
set -eu
dir="$1"; candidate="$2"; backup="$3"
exec >>"$dir/logs/upgrade.log" 2>&1
mv -f "$candidate" "$dir/agentd"
if ! systemctl enable gmha-agent.service >/dev/null 2>&1 || ! systemctl restart gmha-agent.service; then
  cp -p "$backup" "$dir/agentd"
  systemctl restart gmha-agent.service || true
  exit 1
fi
rm -f "$0"
GMHA_UPGRADE
chmod 0700 "$dir/.apply-agent-upgrade.sh"
command -v systemd-run >/dev/null 2>&1
: >"$dir/logs/upgrade.log"
systemd-run --quiet --collect --unit=gmha-agent-upgrade-%s --on-active=2s "$dir/.apply-agent-upgrade.sh" "$dir" "$candidate" "$backup"
echo 'Agent upgrade staged; restart scheduled'`,
		shellQuote(installDir), shellQuote(packageURL(amd64.Name)), shellQuote(amd64.SHA256),
		shellQuote(packageURL(arm64.Name)), shellQuote(arm64.SHA256), strings.TrimPrefix(componentVersion(version), "V"),
		strings.ReplaceAll(componentVersion(version), "/", "_"), shellQuote(componentVersion(version)), strings.TrimPrefix(componentVersion(version), "V"))
}

// UpgradeByIPBinary upgrades an Agent with a versioned binary selected from the
// Manager package repository. The normal platform binary remains the fallback
// used by the legacy one-click action.
func (s *AgentService) UpgradeByIPBinary(ctx context.Context, ip, version string, binary []byte) (agentusecase.UpgradeAgentResponse, error) {
	if s.upgrader == nil {
		return agentusecase.UpgradeAgentResponse{}, errors.New("upgrader not configured")
	}
	machine, ok, err := s.resolveMachineByIP(ctx, ip)
	if err != nil {
		return agentusecase.UpgradeAgentResponse{}, err
	}
	if !ok {
		return agentusecase.UpgradeAgentResponse{}, errors.New("machine not found")
	}
	if !s.beginAgentUpgrade(machine.ID) {
		return agentusecase.UpgradeAgentResponse{}, errors.New("该机器的 Agent 正在升级，请勿重复提交")
	}
	defer s.finishAgentUpgrade(machine.ID)
	return s.upgradeByIPBinary(ctx, machine, version, binary)
}

func (s *AgentService) upgradeByIPBinary(ctx context.Context, machine machinedomain.Machine, version string, binary []byte) (agentusecase.UpgradeAgentResponse, error) {
	return s.upgrader.Execute(ctx, agentusecase.UpgradeAgentRequest{
		MachineID:       machine.ID,
		IP:              machine.IP,
		Version:         version,
		ManagerHTTPAddr: ResolveManagerHTTPAddrForTarget(s.managerHTTPAddr, machine.IP),
		ManagerGRPCAddr: ResolveManagerGRPCAddrForTarget("", s.managerGRPCAddr, machine.IP),
	}, binary)
}

func (s *AgentService) beginAgentUpgrade(machineID string) bool {
	s.upgradeMu.Lock()
	defer s.upgradeMu.Unlock()
	if s.upgrading == nil {
		s.upgrading = make(map[string]struct{})
	}
	if _, exists := s.upgrading[machineID]; exists {
		return false
	}
	s.upgrading[machineID] = struct{}{}
	return true
}

func (s *AgentService) finishAgentUpgrade(machineID string) {
	s.upgradeMu.Lock()
	delete(s.upgrading, machineID)
	s.upgradeMu.Unlock()
}

// ListUninstallCandidates 列出可卸载 Agent 的候选机器。
func (s *AgentService) ListUninstallCandidates(ctx context.Context) ([]AgentView, error) {
	items, err := s.ListViews(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]AgentView, 0)
	for _, item := range items {
		if item.InstallState != "-" ||
			item.MachineStatus == string(machinedomain.StatusAgentInstalling) ||
			item.MachineStatus == string(machinedomain.StatusAgentOnline) ||
			item.MachineStatus == string(machinedomain.StatusAgentError) ||
			strings.TrimSpace(item.Cluster) != "" {
			out = append(out, item)
		}
	}
	return out, nil
}

// EnsureInstalledForMachine 确保指定机器上已安装 Agent，未安装则自动触发安装。
func (s *AgentService) EnsureInstalledForMachine(ctx context.Context, machineID string) error {
	if s.installer == nil {
		return errors.New("installer not configured")
	}
	machine, ok, err := s.machineRepo.GetByID(ctx, machineID)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("machine not found")
	}
	if strings.TrimSpace(machine.Cluster) == "" {
		return nil
	}
	if agent, ok, err := s.repo.GetByMachineID(ctx, machine.ID); err != nil {
		return err
	} else if ok && agent.State == agentdomain.StateOnline {
		return nil
	}
	_, err = s.RetryInstallByIP(ctx, agentusecase.InstallAgentRequest{
		IP:         machine.IP,
		InstallDir: machine.AgentInstallDir,
	})
	return err
}

// Register 标记指定 IP 机器上的 Agent 已注册。
func (s *AgentService) Register(ctx context.Context, ip string) error {
	machine, ok, err := s.resolveMachineByIP(ctx, ip)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("machine not found")
	}
	if _, ok, err := s.repo.GetByMachineID(ctx, machine.ID); err != nil {
		return err
	} else if !ok {
		return errors.New("agent not found")
	}
	return s.repo.MarkRegistered(ctx, machine.ID, time.Now())
}

// Heartbeat 更新指定 IP 机器上 Agent 的心跳时间。
func (s *AgentService) Heartbeat(ctx context.Context, ip string) error {
	machine, ok, err := s.resolveMachineByIP(ctx, ip)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("machine not found")
	}
	return s.repo.UpdateHeartbeat(ctx, machine.ID, time.Now())
}

func (s *AgentService) resolveMachineByIP(ctx context.Context, ip string) (machinedomain.Machine, bool, error) {
	ip = strings.TrimSpace(ip)
	if ip == "" {
		return machinedomain.Machine{}, false, errors.New("ip is required")
	}
	return s.machineRepo.GetByIP(ctx, ip)
}

// RepairMySQLConfigByIP 修复指定 IP 机器上 Agent 的 MySQL 检查配置文件。
// 从管理端获取该机器上的 MySQL 实例信息，生成 mysqlcheck 配置并下发到 Agent。
func (s *AgentService) RepairMySQLConfigByIP(ctx context.Context, ip string) (string, error) {
	if s.taskService == nil || s.mysqlService == nil {
		return "", errors.New("task service or mysql service not configured")
	}

	// 1. 获取机器信息
	agentView, ok, err := s.GetViewByIP(ctx, ip)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("未找到 IP 为 %s 的 Agent 信息", ip)
	}

	// 2. 获取该机器上的所有 MySQL 实例
	var machineInstances []MySQLInstanceView
	allViews, err := s.mysqlService.ListInstanceViews(ctx)
	if err != nil {
		return "", err
	}
	for _, v := range allViews {
		if v.MachineIP == ip {
			machineInstances = append(machineInstances, v)
		}
	}

	if len(machineInstances) == 0 {
		return "", fmt.Errorf("在管理端数据库中未找到该机器的 MySQL 实例记录，请先安装或纳管 MySQL 实例")
	}

	config := mysqlcheck.Config{Instances: make([]mysqlcheck.InstanceConfig, 0, len(machineInstances))}
	for _, inst := range machineInstances {
		config.Instances = append(config.Instances, mysqlcheck.InstanceConfig{
			Port:        inst.Port,
			Socket:      inst.SocketPath,
			Username:    "mha",
			Password:    "3306niubi",
			Database:    "gmha",
			SystemdUnit: inst.SystemdUnit,
			DataDir:     inst.DataDir,
			BinlogDir:   inst.BinlogDir,
			RedoDir:     inst.RedoDir,
			TmpDir:      inst.TmpDir,
			UndoDir:     inst.UndoDir,
		})
	}

	configJSON, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return "", err
	}

	installDir := agentView.InstallDir
	if installDir == "" {
		installDir = "/home/gmha/agent"
	}
	configPath := filepath.Join(installDir, mysqlcheck.DefaultConfigFile)

	// 使用 TaskService 执行 shell 命令写入文件
	cmd := fmt.Sprintf("cat > %s <<EOF\n%s\nEOF\n", configPath, string(configJSON))
	task, err := s.taskService.CreateExecTaskWithOptions(ctx, ip, cmd, ExecTaskOptions{
		Operation: "mysql_monitor_config_repair", DisplayName: "修复 MySQL 监控配置", StepName: "写入 MySQL 监控配置",
	})
	if err != nil {
		return "", fmt.Errorf("创建修复任务失败: %w", err)
	}

	return task.Task.ID, nil
}

// loadAgentBinary 加载 Agent 二进制文件。
// 优先从源码构建，其次从配置路径加载预编译的二进制。
func (s *AgentService) loadAgentBinary(targetOS, targetArch string) ([]byte, error) {
	repoRoot, err := findRepoRoot()
	if err == nil {
		if data, buildErr := s.buildAgentBinary(repoRoot, targetOS, targetArch); buildErr == nil {
			return data, nil
		}
	}

	if strings.TrimSpace(s.binaryPath) != "" {
		candidates := []string{s.binaryPath}
		if targetOS == "linux" {
			candidates = append([]string{filepath.Join(filepath.Dir(s.binaryPath), "agentd-linux-"+targetArch)}, candidates...)
		}
		for _, candidate := range candidates {
			if data, readErr := os.ReadFile(candidate); readErr == nil {
				if looksLikeGMHAAgentBinary(data) && binaryMatchesTarget(data, targetOS, targetArch) {
					return data, nil
				}
			} else if !errors.Is(readErr, os.ErrNotExist) {
				return nil, readErr
			}
		}
		return nil, fmt.Errorf("configured agent binary is not valid for target %s/%s", targetOS, targetArch)
	}

	if err != nil {
		return nil, err
	}
	return nil, errors.New("agent binary not found and cmd/agent source is unavailable")
}

// buildAgentBinary 从源码交叉编译 Agent 二进制（GOOS/GOARCH/CGO_ENABLED=0）。
func (s *AgentService) buildAgentBinary(repoRoot, targetOS, targetArch string) ([]byte, error) {
	agentMain := filepath.Join(repoRoot, "cmd", "agent", "main.go")
	if _, err := os.Stat(agentMain); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, errors.New("cmd/agent source is unavailable")
		}
		return nil, err
	}
	tmpDir, err := os.MkdirTemp("", "gmha-agent-build-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpDir)

	outputPath := filepath.Join(tmpDir, "agentd")
	cmd := exec.Command("go", "build", "-o", outputPath, "./cmd/agent")
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(),
		"GOOS="+targetOS,
		"GOARCH="+targetArch,
		"CGO_ENABLED=0",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, errors.New("failed to build agent binary automatically: " + strings.TrimSpace(string(out)))
	}
	data, err := os.ReadFile(outputPath)
	if err != nil {
		return nil, err
	}
	if !looksLikeGMHAAgentBinary(data) {
		return nil, errors.New("built agent binary validation failed: output is not a GMHA agent binary")
	}
	return data, nil
}

func findRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", errors.New("cannot locate repository root for building agent binary")
}

func looksLikeGMHAAgentBinary(data []byte) bool {
	markers := [][]byte{
		[]byte("gmha/internal/agent"),
		[]byte("gmha/cmd/agent"),
		[]byte("manager_grpc_addr"),
		[]byte("heartbeat_interval"),
	}
	for _, marker := range markers {
		if bytes.Contains(data, marker) {
			return true
		}
	}
	return false
}

func binaryMatchesTarget(data []byte, targetOS, targetArch string) bool {
	switch targetOS {
	case "linux":
		file, err := elf.NewFile(bytes.NewReader(data))
		if err != nil {
			return false
		}
		defer file.Close()
		switch targetArch {
		case "amd64":
			return file.Machine == elf.EM_X86_64
		case "arm64":
			return file.Machine == elf.EM_AARCH64
		default:
			return false
		}
	case "darwin":
		return len(data) >= 4 && (bytes.Equal(data[:4], []byte{0xcf, 0xfa, 0xed, 0xfe}) || bytes.Equal(data[:4], []byte{0xca, 0xfe, 0xba, 0xbe}))
	default:
		return true
	}
}

// detectRemotePlatform 通过 SSH 执行 uname 命令检测远程机器的操作系统和架构。
func (s *AgentService) detectRemotePlatform(ctx context.Context, machine machinedomain.Machine) (string, string, error) {
	if s.sshClient == nil {
		return "", "", errors.New("ssh client not configured")
	}
	endpoint := machinedomain.Endpoint{IP: machine.IP, SSHPort: machine.SSHPort}
	auth, err := s.machineSSHAuth(ctx, machine)
	if err != nil {
		return "", "", err
	}
	output, err := s.sshClient.RunOutput(ctx, endpoint, auth, `sh -lc 'printf "%s %s" "$(uname -s)" "$(uname -m)"'`)
	if err != nil {
		return "", "", err
	}
	parts := strings.Fields(strings.TrimSpace(string(output)))
	if len(parts) != 2 {
		return "", "", errors.New("failed to detect remote platform")
	}
	goos := normalizeGOOS(parts[0])
	goarch := normalizeGOARCH(parts[1])
	if goos == "" || goarch == "" {
		return "", "", errors.New("unsupported remote platform: " + strings.TrimSpace(string(output)))
	}
	return goos, goarch, nil
}

func normalizeGOOS(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "linux":
		return "linux"
	case "darwin":
		return "darwin"
	default:
		return ""
	}
}

func normalizeGOARCH(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "x86_64", "amd64":
		return "amd64"
	case "aarch64", "arm64":
		return "arm64"
	default:
		return ""
	}
}

func (s *AgentService) latestAgentBinary(targetOS, targetArch string) ([]byte, string, error) {
	if s.packages != nil {
		items, err := s.packages.List("gmha-agent", "")
		if err != nil {
			return nil, "", err
		}
		var selected *PackageItem
		for i := range items {
			item := &items[i]
			if targetOS != "linux" || normalizeComponentArch(item.Arch) != normalizeComponentArch(targetArch) {
				continue
			}
			if _, ok := parseComponentVersion(item.Version); !ok {
				continue
			}
			if selected == nil {
				selected = item
				continue
			}
			if cmp, ok := compareComponentVersions(item.Version, selected.Version); ok && cmp > 0 {
				selected = item
			}
		}
		if selected != nil {
			if _, err := s.packages.Verify("gmha-agent", selected.Name); err != nil {
				return nil, "", err
			}
			path, err := s.packages.Open("gmha-agent", selected.Name)
			if err != nil {
				return nil, "", err
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return nil, "", err
			}
			if err := validateAgentBinary(data, selected.Arch); err != nil {
				return nil, "", err
			}
			return data, componentVersion(selected.Version), nil
		}
		if len(items) > 0 {
			return nil, "", fmt.Errorf("没有适配 %s/%s 的已发布 Agent，请先构建发布或上传制品", targetOS, targetArch)
		}
	}
	data, err := s.loadAgentBinary(targetOS, targetArch)
	return data, buildinfo.CurrentVersion(), err
}
