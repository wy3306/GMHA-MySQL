package app

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"encoding/json"
	"fmt"
	"gmha/internal/agent/mysqlcheck"
	agentdomain "gmha/internal/domain/agent"
	dynamicdomain "gmha/internal/domain/dynamic"
	hbdomain "gmha/internal/domain/heartbeat"
	machinedomain "gmha/internal/domain/machine"
	agentusecase "gmha/internal/usecase/agent"
)

// AgentService 是 Agent 管理服务，负责 Agent 的安装、升级、卸载、重试安装、
// 平台检测、二进制构建、心跳管理和 MySQL 配置修复等。
type AgentService struct {
	repo            agentdomain.Repository
	machineRepo     machinedomain.Repository
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
			if strings.TrimSpace(agent.InstallDir) != "" {
				view.InstallDir = agent.InstallDir
			}
			if strings.TrimSpace(agent.LastError) != "" {
				view.LastError = agent.LastError
			}
		}
		if hb, ok := heartbeatByMachineID[machine.ID]; ok {
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
	targetOS, targetArch, err := s.detectRemotePlatform(ctx, machine)
	if err != nil {
		return agentusecase.InstallAgentResponse{}, err
	}
	binary, err := s.loadAgentBinary(targetOS, targetArch)
	if err != nil {
		return agentusecase.InstallAgentResponse{}, err
	}
	return s.installer.Execute(ctx, req, binary)
}

// UninstallByIP 卸载指定 IP 机器上的 Agent，并清除心跳数据。
func (s *AgentService) UninstallByIP(ctx context.Context, ip string) (agentusecase.UninstallAgentResponse, error) {
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

	resp, err := s.uninstaller.Execute(ctx, agentusecase.UninstallAgentRequest{MachineID: machine.ID})
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
	agent, agentFound, err := s.repo.GetByMachineID(ctx, machine.ID)
	if err != nil {
		return agentusecase.UpgradeAgentResponse{}, err
	}
	if !agentFound {
		return agentusecase.UpgradeAgentResponse{}, errors.New("agent not found")
	}
	targetOS, targetArch, err := s.detectRemotePlatform(ctx, machine)
	if err != nil {
		return agentusecase.UpgradeAgentResponse{}, err
	}
	binary, err := s.loadAgentBinary(targetOS, targetArch)
	if err != nil {
		return agentusecase.UpgradeAgentResponse{}, err
	}
	return s.upgrader.Execute(ctx, agentusecase.UpgradeAgentRequest{
		MachineID:       machine.ID,
		IP:              machine.IP,
		Version:         agent.Version,
		ManagerHTTPAddr: ResolveManagerHTTPAddrForTarget(s.managerHTTPAddr, machine.IP),
		ManagerGRPCAddr: ResolveManagerGRPCAddrForTarget("", s.managerGRPCAddr, machine.IP),
	}, binary)
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
		if data, err := os.ReadFile(s.binaryPath); err == nil {
			if looksLikeGMHAAgentBinary(data) && binaryMatchesTarget(data, targetOS) {
				return data, nil
			}
			return nil, errors.New("configured agent binary is not a valid GMHA agent for target platform")
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
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

func binaryMatchesTarget(data []byte, targetOS string) bool {
	switch targetOS {
	case "linux":
		return len(data) >= 4 && bytes.Equal(data[:4], []byte{0x7f, 'E', 'L', 'F'})
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
	auth := machinedomain.SSHAuth{User: machine.SSHUser}
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
