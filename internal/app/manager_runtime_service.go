package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"gmha/internal/buildinfo"
)

// ManagerRuntimeConfig 是 Manager 运行时配置参数，包含 HTTP/gRPC 监听地址、
// 数据库路径、SSH 公钥路径、Agent 二进制路径和 Manager 地址等。
type ManagerRuntimeConfig struct {
	ListenHTTP       string `json:"listen_http"`
	ListenGRPC       string `json:"listen_grpc"`
	DBPath           string `json:"db_path"`
	DatabaseDriver   string `json:"database_driver"`
	DatabaseDSN      string `json:"database_dsn"`
	ManagerPublicKey string `json:"manager_public_key"`
	AgentBinaryPath  string `json:"agent_binary_path"`
	ManagerHTTPAddr  string `json:"manager_http_addr"`
	ManagerGRPCAddr  string `json:"manager_grpc_addr"`
}

// ManagerRuntimeStatus 是 Manager 运行时状态视图，包含是否运行中、PID、启动时间等。
type ManagerRuntimeStatus struct {
	Version   string               `json:"version"`
	Running   bool                 `json:"running"`
	PID       int                  `json:"pid"`
	StartedAt time.Time            `json:"started_at"`
	LogPath   string               `json:"log_path"`
	Discovery string               `json:"discovery"`
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
	healthClient  *http.Client
}

// NewManagerRuntimeService 创建 Manager 运行时服务实例，状态文件保存在 ~/.gmha/ 目录下。
func NewManagerRuntimeService(cfg Config) *ManagerRuntimeService {
	home, _ := os.UserHomeDir()
	base := filepath.Join(home, ".gmha")
	return &ManagerRuntimeService{
		statePath:    filepath.Join(base, "manager-runtime.json"),
		healthClient: &http.Client{Timeout: 1200 * time.Millisecond},
		defaultConfig: normalizeManagerRuntimeConfig(ManagerRuntimeConfig{
			ListenHTTP:       ":8080",
			ListenGRPC:       ":9100",
			DBPath:           cfg.DBPath,
			DatabaseDriver:   cfg.DatabaseDriver,
			DatabaseDSN:      cfg.DatabaseDSN,
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
		if s.managerHealthy(ctx, s.defaultConfig) {
			return ManagerRuntimeStatus{Version: buildinfo.CurrentVersion(), Running: true, Discovery: "health", Config: s.defaultConfig}, nil
		}
		return ManagerRuntimeStatus{Version: buildinfo.CurrentVersion(), Config: s.defaultConfig}, nil
	}
	running := processRunning(state.PID)
	if running && state.PID != os.Getpid() && time.Since(state.StartedAt) > 3*time.Second {
		// Kill(pid, 0) 对僵尸进程也会成功。启动宽限期之后以 HTTP 健康检查
		// 作为最终依据，避免控制台把监听失败后退出的 Manager 显示为运行中。
		running = s.managerHealthy(ctx, state.Config)
	}
	if !running && s.managerHealthy(ctx, state.Config) {
		return ManagerRuntimeStatus{Version: buildinfo.CurrentVersion(), Running: true, Discovery: "health", LogPath: state.LogPath, Config: normalizeManagerRuntimeConfig(state.Config)}, nil
	}
	if !running {
		return ManagerRuntimeStatus{Version: buildinfo.CurrentVersion(), Config: normalizeManagerRuntimeConfig(state.Config), LogPath: state.LogPath}, nil
	}
	return ManagerRuntimeStatus{
		Version:   buildinfo.CurrentVersion(),
		Running:   true,
		PID:       state.PID,
		StartedAt: state.StartedAt,
		LogPath:   state.LogPath,
		Discovery: "state",
		Config:    normalizeManagerRuntimeConfig(state.Config),
	}, nil
}

// RegisterCurrentProcess 登记当前 serve 进程。无论 Manager 是由控制台、systemd
// 还是直接执行 gmha serve 启动，CLI 和 Web 控制台都能发现同一运行实例。
func (s *ManagerRuntimeService) RegisterCurrentProcess(cfg ManagerRuntimeConfig) error {
	cfg = normalizeManagerRuntimeConfig(cfg)
	if err := validateManagerRuntimeConfig(cfg); err != nil {
		return err
	}
	state, ok, err := s.loadState()
	if err != nil {
		return err
	}
	logPath := ""
	if ok && state.PID == os.Getpid() {
		logPath = state.LogPath
	}
	return s.persistState(managerRuntimeState{
		PID: os.Getpid(), StartedAt: time.Now().UTC(), LogPath: logPath, Config: cfg,
	})
}

// AdoptCurrentProcess makes the process serving the Web console authoritative.
// A request cannot reach Manager's own API unless that Manager is running, so
// stale PID files or an advertised address which is not locally routable must
// not make the Web console report "stopped" and try to bind the same ports again.
func (s *ManagerRuntimeService) AdoptCurrentProcess() (ManagerRuntimeStatus, error) {
	state, ok, err := s.loadState()
	if err != nil {
		return ManagerRuntimeStatus{}, err
	}
	if !ok {
		state.Config = s.defaultConfig
	}
	if state.PID != os.Getpid() {
		state.PID = os.Getpid()
		state.StartedAt = time.Now().UTC()
		// A log path from another stale process is misleading for a directly
		// started Manager. Preserve it only when it actually exists.
		if state.LogPath != "" {
			if _, statErr := os.Stat(state.LogPath); statErr != nil {
				state.LogPath = ""
			}
		}
		if err := s.persistState(state); err != nil {
			return ManagerRuntimeStatus{}, err
		}
	}
	return ManagerRuntimeStatus{
		Version: buildinfo.CurrentVersion(), Running: true, PID: os.Getpid(), StartedAt: state.StartedAt,
		LogPath: state.LogPath, Discovery: "current", Config: normalizeManagerRuntimeConfig(state.Config),
	}, nil
}

// TestDatabase 验证所选数据库驱动和连接串，不执行迁移或切换。
func (s *ManagerRuntimeService) TestDatabase(ctx context.Context, cfg ManagerRuntimeConfig) error {
	cfg = normalizeManagerRuntimeConfig(cfg)
	if err := validateManagerDatabaseConfig(cfg); err != nil {
		return err
	}
	db, _, err := openDatabase(Config{DBPath: cfg.DBPath, DatabaseDriver: cfg.DatabaseDriver, DatabaseDSN: cfg.DatabaseDSN})
	if err != nil {
		return fmt.Errorf("数据库连接失败: %w", err)
	}
	defer db.Close()
	return db.PingContext(ctx)
}

func (s *ManagerRuntimeService) managerHealthy(ctx context.Context, cfg ManagerRuntimeConfig) bool {
	endpoint, err := managerHealthURL(cfg.ManagerHTTPAddr)
	if err != nil {
		return false
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return false
	}
	client := s.healthClient
	if client == nil {
		client = &http.Client{Timeout: 1200 * time.Millisecond}
	}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	var result struct {
		Status string `json:"status"`
	}
	return json.Unmarshal(body, &result) == nil && result.Status == "ok"
}

func managerHealthURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("empty manager address")
	}
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	if u.Hostname() == "0.0.0.0" || u.Hostname() == "::" {
		port := u.Port()
		u.Host = "127.0.0.1"
		if port != "" {
			u.Host += ":" + port
		}
	}
	u.Path, u.RawQuery, u.Fragment = "/api/v1/healthz", "", ""
	return u.String(), nil
}

// SaveConfig 保存 Manager 启动配置到磁盘（不重启进程）。
func (s *ManagerRuntimeService) SaveConfig(ctx context.Context, cfg ManagerRuntimeConfig) error {
	cfg = normalizeManagerRuntimeConfig(cfg)
	if err := validateManagerRuntimeConfig(cfg); err != nil {
		return err
	}
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
	return s.launch(cfg, 0)
}

// IsCurrentProcess reports whether the runtime state points at this HTTP server.
func (s *ManagerRuntimeService) IsCurrentProcess() bool {
	state, ok, err := s.loadState()
	return err == nil && ok && state.PID == os.Getpid()
}

// RestartCurrentProcess starts a replacement which waits for this process to
// release its listeners. The delayed SIGTERM gives the HTTP handler time to
// return a successful response to the console.
func (s *ManagerRuntimeService) RestartCurrentProcess(cfg ManagerRuntimeConfig) (ManagerRuntimeStatus, error) {
	cfg = normalizeManagerRuntimeConfig(cfg)
	if err := validateManagerRuntimeConfig(cfg); err != nil {
		return ManagerRuntimeStatus{}, err
	}
	status, err := s.launch(cfg, os.Getpid())
	if err != nil {
		return ManagerRuntimeStatus{}, err
	}
	signalCurrentProcessAfterDelay()
	return status, nil
}

// StopCurrentProcess records the stopped state, then terminates this Manager
// after the API response has had time to flush.
func (s *ManagerRuntimeService) StopCurrentProcess() (ManagerRuntimeStatus, error) {
	state, ok, err := s.loadState()
	if err != nil {
		return ManagerRuntimeStatus{}, err
	}
	if !ok || state.PID != os.Getpid() {
		return ManagerRuntimeStatus{}, errors.New("当前 Manager 未登记为受控进程")
	}
	state.PID = 0
	state.StartedAt = time.Time{}
	if err := s.persistState(state); err != nil {
		return ManagerRuntimeStatus{}, err
	}
	signalCurrentProcessAfterDelay()
	return ManagerRuntimeStatus{Version: buildinfo.CurrentVersion(), Config: normalizeManagerRuntimeConfig(state.Config), LogPath: state.LogPath}, nil
}

func signalCurrentProcessAfterDelay() {
	go func() {
		time.Sleep(500 * time.Millisecond)
		if process, err := os.FindProcess(os.Getpid()); err == nil {
			_ = process.Signal(syscall.SIGTERM)
		}
	}()
}

func (s *ManagerRuntimeService) launch(cfg ManagerRuntimeConfig, waitForPID int) (ManagerRuntimeStatus, error) {

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
		"--db-driver", cfg.DatabaseDriver,
		"--agent-binary", cfg.AgentBinaryPath,
		"--manager-http-addr", cfg.ManagerHTTPAddr,
		"--manager-grpc-addr", cfg.ManagerGRPCAddr,
	}
	if strings.TrimSpace(cfg.DatabaseDSN) != "" {
		args = append(args, "--db-dsn", cfg.DatabaseDSN)
	}
	if strings.TrimSpace(cfg.ManagerPublicKey) != "" {
		args = append(args, "--manager-pubkey", cfg.ManagerPublicKey)
	}

	cmd := exec.Command(exePath, args...)
	if waitForPID > 0 {
		cmd.Env = append(os.Environ(), "GMHA_WAIT_FOR_PID="+strconv.Itoa(waitForPID))
	}
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Stdin = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return ManagerRuntimeStatus{}, err
	}
	pid := cmd.Process.Pid
	// The daemon is intentionally independent from the console process. Release
	// prevents an exited child from remaining as a zombie and being misdetected.
	_ = cmd.Process.Release()

	state := managerRuntimeState{
		PID:       pid,
		StartedAt: time.Now().UTC(),
		LogPath:   logPath,
		Config:    cfg,
	}
	if err := s.persistState(state); err != nil {
		return ManagerRuntimeStatus{}, err
	}

	time.Sleep(300 * time.Millisecond)
	status, err := s.GetStatus(context.Background())
	if err != nil {
		return ManagerRuntimeStatus{}, err
	}
	if !status.Running {
		return status, fmt.Errorf("manager 启动后立即退出，请检查日志 %s", logPath)
	}
	return status, nil
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
	if strings.TrimSpace(cfg.DatabaseDriver) == "" {
		cfg.DatabaseDriver = "sqlite"
	}
	cfg.DatabaseDriver = strings.ToLower(strings.TrimSpace(cfg.DatabaseDriver))
	if cfg.DatabaseDriver == "sqlite3" {
		cfg.DatabaseDriver = "sqlite"
	}
	if cfg.DatabaseDriver == "postgresql" {
		cfg.DatabaseDriver = "postgres"
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
	if looksLikePromptGarbage(cfg.DatabaseDriver) {
		cfg.DatabaseDriver = ""
	}
	if looksLikePromptGarbage(cfg.DatabaseDSN) {
		cfg.DatabaseDSN = ""
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
	if err := validateManagerDatabaseConfig(cfg); err != nil {
		return err
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

func validateManagerDatabaseConfig(cfg ManagerRuntimeConfig) error {
	switch strings.ToLower(strings.TrimSpace(cfg.DatabaseDriver)) {
	case "sqlite", "sqlite3":
		if strings.TrimSpace(cfg.DBPath) == "" && strings.TrimSpace(cfg.DatabaseDSN) == "" {
			return errors.New("SQLite 数据库路径不能为空")
		}
	case "mysql", "postgres", "postgresql":
		if strings.TrimSpace(cfg.DatabaseDSN) == "" {
			return fmt.Errorf("%s 数据库连接串不能为空", cfg.DatabaseDriver)
		}
	default:
		return fmt.Errorf("不支持的数据库驱动 %q，可选 sqlite、mysql、postgres", cfg.DatabaseDriver)
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
		"数据库":      status.Config.DatabaseDriver + ": " + firstNonEmpty(status.Config.DatabaseDSN, status.Config.DBPath),
		"Agent二进制": status.Config.AgentBinaryPath,
		"日志文件":     emptyStringAsDash(status.LogPath),
	}, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func emptyStringAsDash(v string) string {
	if strings.TrimSpace(v) == "" {
		return "-"
	}
	return v
}
