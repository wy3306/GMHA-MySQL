package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// ManagerRuntimeConfig 是 Manager 运行时配置参数，包含 HTTP/gRPC 监听地址、
// 数据库路径、SSH 公钥路径、Agent 二进制路径和 Manager 地址等。
type ManagerRuntimeConfig struct {
	ListenHTTP       string `json:"listen_http"`
	ListenGRPC       string `json:"listen_grpc"`
	DBPath           string `json:"db_path"`
	ManagerPublicKey string `json:"manager_public_key"`
	AgentBinaryPath  string `json:"agent_binary_path"`
	ManagerHTTPAddr  string `json:"manager_http_addr"`
	ManagerGRPCAddr  string `json:"manager_grpc_addr"`
}

// ManagerRuntimeStatus 是 Manager 运行时状态视图，包含是否运行中、PID、启动时间等。
type ManagerRuntimeStatus struct {
	Running   bool                 `json:"running"`
	PID       int                  `json:"pid"`
	StartedAt time.Time            `json:"started_at"`
	LogPath   string               `json:"log_path"`
	Config    ManagerRuntimeConfig `json:"config"`
}

// managerRuntimeState 是持久化到磁盘的 Manager 运行状态。
type managerRuntimeState struct {
	PID       int                  `json:"pid"`
	StartedAt time.Time            `json:"started_at"`
	LogPath   string               `json:"log_path"`
	Config    ManagerRuntimeConfig `json:"config"`
}

// ManagerRuntimeService 是 Manager 运行时管理服务，支持前台启动、后台守护、
// 停止、重启 Manager 进程，以及配置的持久化和状态查询。
type ManagerRuntimeService struct {
	statePath     string
	defaultConfig ManagerRuntimeConfig
}

// NewManagerRuntimeService 创建 Manager 运行时服务实例，状态文件保存在 ~/.gmha/ 目录下。
func NewManagerRuntimeService(cfg Config) *ManagerRuntimeService {
	home, _ := os.UserHomeDir()
	base := filepath.Join(home, ".gmha")
	return &ManagerRuntimeService{
		statePath: filepath.Join(base, "manager-runtime.json"),
		defaultConfig: normalizeManagerRuntimeConfig(ManagerRuntimeConfig{
			ListenHTTP:       ":8080",
			ListenGRPC:       ":9100",
			DBPath:           cfg.DBPath,
			ManagerPublicKey: cfg.ManagerPublicKey,
			AgentBinaryPath:  cfg.AgentBinaryPath,
			ManagerHTTPAddr:  cfg.ManagerHTTPAddr,
			ManagerGRPCAddr:  cfg.ManagerGRPCAddr,
		}),
	}
}

// GetStatus 获取 Manager 的当前运行状态。
func (s *ManagerRuntimeService) GetStatus(ctx context.Context) (ManagerRuntimeStatus, error) {
	state, ok, err := s.loadState()
	if err != nil {
		return ManagerRuntimeStatus{}, err
	}
	if !ok {
		return ManagerRuntimeStatus{Config: s.defaultConfig}, nil
	}
	running := processRunning(state.PID)
	if !running {
		return ManagerRuntimeStatus{Config: normalizeManagerRuntimeConfig(state.Config), LogPath: state.LogPath}, nil
	}
	return ManagerRuntimeStatus{
		Running:   true,
		PID:       state.PID,
		StartedAt: state.StartedAt,
		LogPath:   state.LogPath,
		Config:    normalizeManagerRuntimeConfig(state.Config),
	}, nil
}

// SaveConfig 保存 Manager 启动配置到磁盘（不重启进程）。
func (s *ManagerRuntimeService) SaveConfig(ctx context.Context, cfg ManagerRuntimeConfig) error {
	state, _, err := s.loadOrDefault(cfg)
	if err != nil {
		return err
	}
	state.Config = normalizeManagerRuntimeConfig(cfg)
	return s.persistState(state)
}

// Start 以后台守护模式启动 Manager 进程，日志输出到 ~/.gmha/manager.log。
func (s *ManagerRuntimeService) Start(ctx context.Context, cfg ManagerRuntimeConfig) (ManagerRuntimeStatus, error) {
	status, err := s.GetStatus(ctx)
	if err != nil {
		return ManagerRuntimeStatus{}, err
	}
	if status.Running {
		return status, errors.New("manager 已在运行，当前启动参数不会热更新；如需修改请先用“修改启动参数”保存，再停止并重新启动")
	}

	cfg = normalizeManagerRuntimeConfig(cfg)
	if err := validateManagerRuntimeConfig(cfg); err != nil {
		return ManagerRuntimeStatus{}, err
	}

	exePath, err := os.Executable()
	if err != nil {
		return ManagerRuntimeStatus{}, err
	}
	baseDir := filepath.Dir(s.statePath)
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		return ManagerRuntimeStatus{}, err
	}
	logPath := filepath.Join(baseDir, "manager.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return ManagerRuntimeStatus{}, err
	}
	defer logFile.Close()

	args := []string{
		"serve",
		"--listen", cfg.ListenHTTP,
		"--grpc-listen", cfg.ListenGRPC,
		"--db", cfg.DBPath,
		"--agent-binary", cfg.AgentBinaryPath,
		"--manager-http-addr", cfg.ManagerHTTPAddr,
		"--manager-grpc-addr", cfg.ManagerGRPCAddr,
	}
	if strings.TrimSpace(cfg.ManagerPublicKey) != "" {
		args = append(args, "--manager-pubkey", cfg.ManagerPublicKey)
	}

	cmd := exec.Command(exePath, args...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Stdin = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return ManagerRuntimeStatus{}, err
	}

	state := managerRuntimeState{
		PID:       cmd.Process.Pid,
		StartedAt: time.Now().UTC(),
		LogPath:   logPath,
		Config:    cfg,
	}
	if err := s.persistState(state); err != nil {
		return ManagerRuntimeStatus{}, err
	}

	time.Sleep(300 * time.Millisecond)
	return s.GetStatus(ctx)
}

// Stop 停止 Manager 后台进程，先发送 SIGTERM，超时后发送 SIGKILL。
func (s *ManagerRuntimeService) Stop(ctx context.Context) error {
	state, ok, err := s.loadState()
	if err != nil {
		return err
	}
	if !ok || state.PID <= 0 {
		return errors.New("manager 未运行")
	}
	proc, err := os.FindProcess(state.PID)
	if err != nil {
		return err
	}
	if err := proc.Signal(syscall.SIGTERM); err != nil && !strings.Contains(err.Error(), "process already finished") {
		return err
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !processRunning(state.PID) {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if processRunning(state.PID) {
		_ = proc.Signal(syscall.SIGKILL)
	}
	state.PID = 0
	state.StartedAt = time.Time{}
	return s.persistState(state)
}

// Restart 重启 Manager 后台进程（先停止再启动）。
func (s *ManagerRuntimeService) Restart(ctx context.Context, cfg ManagerRuntimeConfig) (ManagerRuntimeStatus, error) {
	status, err := s.GetStatus(ctx)
	if err != nil {
		return ManagerRuntimeStatus{}, err
	}
	if status.Running {
		if err := s.Stop(ctx); err != nil {
			return ManagerRuntimeStatus{}, err
		}
	}
	return s.Start(ctx, cfg)
}

func (s *ManagerRuntimeService) loadOrDefault(cfg ManagerRuntimeConfig) (managerRuntimeState, bool, error) {
	state, ok, err := s.loadState()
	if err != nil {
		return managerRuntimeState{}, false, err
	}
	if ok {
		return state, true, nil
	}
	if strings.TrimSpace(cfg.ListenHTTP) == "" &&
		strings.TrimSpace(cfg.ListenGRPC) == "" &&
		strings.TrimSpace(cfg.DBPath) == "" &&
		strings.TrimSpace(cfg.ManagerPublicKey) == "" &&
		strings.TrimSpace(cfg.AgentBinaryPath) == "" &&
		strings.TrimSpace(cfg.ManagerHTTPAddr) == "" &&
		strings.TrimSpace(cfg.ManagerGRPCAddr) == "" {
		return managerRuntimeState{Config: s.defaultConfig}, false, nil
	}
	return managerRuntimeState{Config: normalizeManagerRuntimeConfig(cfg)}, false, nil
}

func (s *ManagerRuntimeService) loadState() (managerRuntimeState, bool, error) {
	data, err := os.ReadFile(s.statePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return managerRuntimeState{}, false, nil
		}
		return managerRuntimeState{}, false, err
	}
	var state managerRuntimeState
	if err := json.Unmarshal(data, &state); err != nil {
		return managerRuntimeState{}, false, err
	}
	state.Config = normalizeManagerRuntimeConfig(state.Config)
	return state, true, nil
}

func (s *ManagerRuntimeService) persistState(state managerRuntimeState) error {
	if err := os.MkdirAll(filepath.Dir(s.statePath), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.statePath, data, 0o644)
}

func normalizeManagerRuntimeConfig(cfg ManagerRuntimeConfig) ManagerRuntimeConfig {
	cfg = sanitizeManagerRuntimeConfig(cfg)
	if strings.TrimSpace(cfg.ListenHTTP) == "" {
		cfg.ListenHTTP = ":8080"
	}
	if strings.TrimSpace(cfg.ListenGRPC) == "" {
		cfg.ListenGRPC = ":9100"
	}
	if strings.TrimSpace(cfg.DBPath) == "" {
		cfg.DBPath = "./data/manager.db"
	}
	if strings.TrimSpace(cfg.AgentBinaryPath) == "" {
		cfg.AgentBinaryPath = "./bin/agentd"
	}
	if strings.TrimSpace(cfg.ManagerHTTPAddr) == "" {
		cfg.ManagerHTTPAddr = DefaultManagerHTTPAddr()
	}
	cfg.ManagerGRPCAddr = NormalizeManagerGRPCAddr(cfg.ManagerHTTPAddr, cfg.ManagerGRPCAddr)
	return cfg
}

func sanitizeManagerRuntimeConfig(cfg ManagerRuntimeConfig) ManagerRuntimeConfig {
	if looksLikePromptGarbage(cfg.ListenHTTP) {
		cfg.ListenHTTP = ""
	}
	if looksLikePromptGarbage(cfg.ListenGRPC) {
		cfg.ListenGRPC = ""
	}
	if looksLikePromptGarbage(cfg.DBPath) {
		cfg.DBPath = ""
	}
	if looksLikePromptGarbage(cfg.ManagerPublicKey) {
		cfg.ManagerPublicKey = ""
	}
	if looksLikePromptGarbage(cfg.AgentBinaryPath) {
		cfg.AgentBinaryPath = ""
	}
	if looksLikePromptGarbage(cfg.ManagerHTTPAddr) {
		cfg.ManagerHTTPAddr = ""
	}
	if looksLikePromptGarbage(cfg.ManagerGRPCAddr) {
		cfg.ManagerGRPCAddr = ""
	}
	return cfg
}

func looksLikePromptGarbage(v string) bool {
	v = strings.TrimSpace(v)
	if v == "" {
		return false
	}
	badMarkers := []string{
		"请选择",
		"输入 0 或 esc 返回",
		"HTTP 监听地址",
		"gRPC 监听地址",
		"Manager HTTP 地址",
		"Manager gRPC 地址",
		"数据库路径",
		"Agent 二进制路径",
		"Manager SSH 公钥路径",
		". 返回",
	}
	for _, marker := range badMarkers {
		if strings.Contains(v, marker) {
			return true
		}
	}
	return false
}

func validateManagerRuntimeConfig(cfg ManagerRuntimeConfig) error {
	if strings.TrimSpace(cfg.ListenHTTP) == "" {
		return errors.New("HTTP 监听地址不能为空")
	}
	if strings.TrimSpace(cfg.ListenGRPC) == "" {
		return errors.New("gRPC 监听地址不能为空")
	}
	if strings.TrimSpace(cfg.DBPath) == "" {
		return errors.New("数据库路径不能为空")
	}
	if strings.TrimSpace(cfg.AgentBinaryPath) == "" {
		return errors.New("Agent 二进制路径不能为空")
	}
	if strings.TrimSpace(cfg.ManagerHTTPAddr) == "" {
		return errors.New("Manager HTTP 地址不能为空")
	}
	if strings.TrimSpace(cfg.ManagerGRPCAddr) == "" {
		return errors.New("Manager gRPC 地址不能为空")
	}
	if strings.HasPrefix(cfg.ManagerGRPCAddr, "127.0.0.1:") || strings.HasPrefix(cfg.ManagerGRPCAddr, "localhost:") {
		return errors.New("Manager gRPC 地址不能使用 127.0.0.1 或 localhost，目标主机上的 Agent 无法访问")
	}
	return nil
}

func processRunning(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil
}

func formatPID(pid int) string {
	if pid <= 0 {
		return "-"
	}
	return strconv.Itoa(pid)
}

// DescribeStatus 返回人类可读的 Manager 状态信息（中文键名）。
func (s *ManagerRuntimeService) DescribeStatus(ctx context.Context) (map[string]string, error) {
	status, err := s.GetStatus(ctx)
	if err != nil {
		return nil, err
	}
	return map[string]string{
		"运行状态":     map[bool]string{true: "运行中", false: "未运行"}[status.Running],
		"PID":      formatPID(status.PID),
		"HTTP监听":   status.Config.ListenHTTP,
		"gRPC监听":   status.Config.ListenGRPC,
		"HTTP地址":   status.Config.ManagerHTTPAddr,
		"gRPC地址":   status.Config.ManagerGRPCAddr,
		"数据库":      status.Config.DBPath,
		"Agent二进制": status.Config.AgentBinaryPath,
		"日志文件":     emptyStringAsDash(status.LogPath),
	}, nil
}

func emptyStringAsDash(v string) string {
	if strings.TrimSpace(v) == "" {
		return "-"
	}
	return v
}
