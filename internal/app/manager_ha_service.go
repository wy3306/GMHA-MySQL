package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"gmha/internal/buildinfo"
	collectdomain "gmha/internal/collect"
	machinedomain "gmha/internal/domain/machine"
	managerdomain "gmha/internal/domain/manager"
	taskdomain "gmha/internal/domain/task"
)

type ManagerHAOverview struct {
	Config         managerdomain.HAConfig `json:"config"`
	Nodes          []managerdomain.Node   `json:"nodes"`
	ActiveNodeID   string                 `json:"active_node_id"`
	CurrentNodeID  string                 `json:"current_node_id"`
	SharedDatabase bool                   `json:"shared_database"`
	Ready          bool                   `json:"ready"`
	Warnings       []string               `json:"warnings"`
}

type AddManagerNodeRequest struct {
	MachineID  string `json:"machine_id"`
	HTTPPort   int    `json:"http_port"`
	GRPCPort   int    `json:"grpc_port"`
	Interface  string `json:"interface"`
	InstallDir string `json:"install_dir"`
}

type ManagerVIPSwitchResult struct {
	FromNodeID string `json:"from_node_id"`
	ToNodeID   string `json:"to_node_id"`
	TaskID     string `json:"task_id"`
	Status     string `json:"status"`
}

type ManagerNetworkInterface struct {
	Name        string   `json:"name"`
	IPs         []string `json:"ips"`
	Recommended bool     `json:"recommended"`
	Reason      string   `json:"reason"`
	score       int
}

type ManagerNetworkInterfaceResult struct {
	NodeID      string                    `json:"node_id"`
	NodeName    string                    `json:"node_name"`
	Recommended string                    `json:"recommended"`
	Interfaces  []ManagerNetworkInterface `json:"interfaces"`
	CollectedAt time.Time                 `json:"collected_at"`
}

type managerBootstrapGrant struct {
	Config    ManagerRuntimeConfig
	NodeIP    string
	ExpiresAt time.Time
}

type ManagerHAService struct {
	repo        managerdomain.Repository
	machines    machinedomain.Repository
	tasks       *TaskService
	runtime     *ManagerRuntimeService
	machineInfo *MachineService
	client      *http.Client

	mu        sync.Mutex
	currentID string
	grants    map[string]managerBootstrapGrant
}

func NewManagerHAService(repo managerdomain.Repository, machines machinedomain.Repository, tasks *TaskService, runtime *ManagerRuntimeService, machineInfo *MachineService) *ManagerHAService {
	return &ManagerHAService{
		repo: repo, machines: machines, tasks: tasks, runtime: runtime, machineInfo: machineInfo,
		client: &http.Client{Timeout: 1500 * time.Millisecond},
		grants: make(map[string]managerBootstrapGrant),
	}
}

func (s *ManagerHAService) Start(ctx context.Context, cfg ManagerRuntimeConfig) error {
	node, err := s.registerCurrent(ctx, cfg)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.currentID = node.ID
	s.mu.Unlock()
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				node.State, node.Version, node.LastSeenAt, node.LastError = "online", buildVersion(), time.Now().UTC(), ""
				_ = s.repo.SaveNode(context.Background(), node)
			}
		}
	}()
	return nil
}

func (s *ManagerHAService) registerCurrent(ctx context.Context, cfg ManagerRuntimeConfig) (managerdomain.Node, error) {
	cfg = normalizeManagerRuntimeConfig(cfg)
	host := firstNonEmpty(strings.TrimSpace(os.Getenv("GMHA_NODE_IP")), advertisedLocalIP())
	httpAddress := "http://" + net.JoinHostPort(host, managerListenPort(cfg.ListenHTTP, "8080"))
	grpcAddress := net.JoinHostPort(host, managerListenPort(cfg.ListenGRPC, "9100"))
	hostname, _ := os.Hostname()
	id := managerNodeID(host, httpAddress)
	machineID := ""
	if machine, ok, _ := s.machines.GetByIP(ctx, host); ok {
		machineID = machine.ID
		if strings.TrimSpace(machine.Name) != "" {
			hostname = machine.Name
		}
	}
	items, err := s.repo.ListNodes(ctx)
	if err != nil {
		return managerdomain.Node{}, err
	}
	role, vipInterface := "standby", ""
	active := false
	for _, item := range items {
		if item.ID == id {
			role = item.Role
			vipInterface = item.VIPInterface
		}
		if item.Role == "active" {
			active = true
		}
	}
	if !active {
		role = "active"
	}
	node := managerdomain.Node{
		ID: id, MachineID: machineID, Name: hostname, IP: host,
		HTTPAddress: httpAddress, GRPCAddress: grpcAddress,
		VIPInterface: vipInterface,
		Role:         role, State: "online", Version: buildVersion(), LastSeenAt: time.Now().UTC(),
	}
	if err := s.repo.SaveNode(ctx, node); err != nil {
		return managerdomain.Node{}, err
	}
	if role == "active" {
		_ = s.repo.SetActive(ctx, id, time.Now().UTC())
	}
	return node, nil
}

func (s *ManagerHAService) Overview(ctx context.Context) (ManagerHAOverview, error) {
	cfg, err := s.repo.GetConfig(ctx)
	if err != nil {
		return ManagerHAOverview{}, err
	}
	nodes, err := s.repo.ListNodes(ctx)
	if err != nil {
		return ManagerHAOverview{}, err
	}
	s.mu.Lock()
	currentID := s.currentID
	s.mu.Unlock()
	overview := ManagerHAOverview{Config: cfg, Nodes: nodes, CurrentNodeID: currentID}
	status, _ := s.runtime.GetStatus(ctx)
	overview.SharedDatabase = status.Config.DatabaseDriver == "mysql" || status.Config.DatabaseDriver == "postgres"
	for i := range overview.Nodes {
		node := &overview.Nodes[i]
		if node.Role == "active" {
			overview.ActiveNodeID = node.ID
		}
		if node.ID == currentID {
			node.State, node.Version, node.LastSeenAt, node.LastError = "online", buildVersion(), time.Now().UTC(), ""
			continue
		}
		s.refreshRemoteNode(ctx, node)
	}
	if !overview.SharedDatabase {
		overview.Warnings = append(overview.Warnings, "SQLite 仅支持单节点；扩展 Manager 前请切换到 MySQL 或 PostgreSQL。")
	}
	if len(nodes) < 2 {
		overview.Warnings = append(overview.Warnings, "当前只有一个 Manager 节点，尚未形成高可用。")
	}
	if cfg.Enabled && (cfg.VIP == "" || cfg.Interface == "") {
		overview.Warnings = append(overview.Warnings, "已启用高可用，但 VIP 或网卡尚未配置完整。")
	}
	overview.Ready = overview.SharedDatabase && len(nodes) >= 2 && cfg.Enabled && cfg.VIP != "" && cfg.Interface != "" && overview.ActiveNodeID != ""
	return overview, nil
}

func (s *ManagerHAService) refreshRemoteNode(ctx context.Context, node *managerdomain.Node) {
	endpoint := strings.TrimRight(node.HTTPAddress, "/") + "/api/v1/manager/status"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return
	}
	resp, err := s.client.Do(req)
	if err != nil {
		node.State, node.LastError = "offline", err.Error()
		return
	}
	defer resp.Body.Close()
	var status ManagerRuntimeStatus
	if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&status) != nil || !status.Running {
		node.State, node.LastError = "offline", "Manager 状态接口不可用"
		return
	}
	node.State, node.Version, node.LastSeenAt, node.LastError = "online", status.Version, time.Now().UTC(), ""
	_ = s.repo.SaveNode(context.Background(), *node)
}

func (s *ManagerHAService) NetworkInterfaces(ctx context.Context, nodeID, vip string, prefix int, refresh bool) (ManagerNetworkInterfaceResult, error) {
	nodeID = strings.TrimSpace(nodeID)
	var node managerdomain.Node
	if nodeID != "" {
		item, ok, err := s.repo.GetNode(ctx, nodeID)
		if err != nil {
			return ManagerNetworkInterfaceResult{}, err
		}
		if !ok {
			return ManagerNetworkInterfaceResult{}, errors.New("Manager 节点不存在")
		}
		node = item
	} else {
		nodes, err := s.repo.ListNodes(ctx)
		if err != nil {
			return ManagerNetworkInterfaceResult{}, err
		}
		for _, item := range nodes {
			if item.Role == "active" {
				node = item
				break
			}
		}
		if node.ID == "" && len(nodes) > 0 {
			node = nodes[0]
		}
	}
	if node.ID == "" {
		return ManagerNetworkInterfaceResult{}, errors.New("尚无可扫描的 Manager 节点")
	}

	s.mu.Lock()
	currentID := s.currentID
	s.mu.Unlock()
	var raw []collectdomain.NetworkInterface
	if node.ID == currentID {
		raw = localManagerNetworkInterfaces()
	} else {
		if node.MachineID == "" || s.machineInfo == nil {
			return ManagerNetworkInterfaceResult{}, errors.New("该 Manager 节点未关联纳管机器，无法读取远程网卡")
		}
		var info collectdomain.StaticInfo
		var err error
		if refresh {
			info, err = s.machineInfo.RefreshStaticInfo(ctx, node.MachineID)
		} else {
			info, err = s.machineInfo.GetStaticInfo(ctx, node.MachineID)
		}
		if err != nil {
			return ManagerNetworkInterfaceResult{}, err
		}
		raw = info.Host.Interfaces
	}
	cfg, _ := s.repo.GetConfig(ctx)
	if strings.TrimSpace(vip) == "" {
		vip = cfg.VIP
	}
	if prefix == 0 {
		prefix = cfg.Prefix
	}
	items, recommended := rankManagerNetworkInterfaces(raw, node.IP, vip, prefix)
	if len(items) == 0 {
		return ManagerNetworkInterfaceResult{}, errors.New("目标节点未上报可用于 VIP 的 IPv4/IPv6 网卡")
	}
	return ManagerNetworkInterfaceResult{
		NodeID: node.ID, NodeName: node.Name, Recommended: recommended,
		Interfaces: items, CollectedAt: time.Now().UTC(),
	}, nil
}

func localManagerNetworkInterfaces() []collectdomain.NetworkInterface {
	interfaces, _ := net.Interfaces()
	items := make([]collectdomain.NetworkInterface, 0, len(interfaces))
	for _, item := range interfaces {
		if item.Flags&net.FlagUp == 0 || item.Flags&net.FlagLoopback != 0 || item.Name == "lo" {
			continue
		}
		addresses, _ := item.Addrs()
		ips := make([]string, 0, len(addresses))
		for _, address := range addresses {
			value := strings.SplitN(address.String(), "/", 2)[0]
			if ip := net.ParseIP(value); ip != nil && !ip.IsLoopback() {
				ips = append(ips, value)
			}
		}
		if len(ips) > 0 {
			items = append(items, collectdomain.NetworkInterface{Name: item.Name, IPs: ips})
		}
	}
	return items
}

func rankManagerNetworkInterfaces(raw []collectdomain.NetworkInterface, nodeIP, vip string, prefix int) ([]ManagerNetworkInterface, string) {
	if prefix == 0 {
		prefix = 24
	}
	seen := make(map[string]struct{})
	items := make([]ManagerNetworkInterface, 0, len(raw))
	for _, item := range raw {
		name := strings.TrimSpace(strings.SplitN(item.Name, "@", 2)[0])
		if name == "" || name == "lo" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		option := ManagerNetworkInterface{Name: name, IPs: append([]string(nil), item.IPs...)}
		for _, address := range item.IPs {
			ip := strings.SplitN(address, "/", 2)[0]
			if ip == nodeIP {
				option.score += 50
				option.Reason = "匹配节点管理地址"
			}
			if vip != "" && managerSameSubnet(ip, vip, prefix) {
				option.score += 100
				option.Reason = "与 Manager VIP 位于同一网段"
			}
		}
		if option.Reason == "" {
			option.Reason = "节点可用网络接口"
		}
		items = append(items, option)
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].score == items[j].score {
			return items[i].Name < items[j].Name
		}
		return items[i].score > items[j].score
	})
	recommended := ""
	if len(items) > 0 {
		items[0].Recommended = true
		recommended = items[0].Name
	}
	return items, recommended
}

func managerSameSubnet(left, right string, prefix int) bool {
	leftIP, rightIP := net.ParseIP(left), net.ParseIP(right)
	if leftIP == nil || rightIP == nil {
		return false
	}
	bits := 128
	if leftIP.To4() != nil && rightIP.To4() != nil {
		leftIP, rightIP, bits = leftIP.To4(), rightIP.To4(), 32
	} else if (leftIP.To4() == nil) != (rightIP.To4() == nil) {
		return false
	}
	if prefix < 0 || prefix > bits {
		return false
	}
	mask := net.CIDRMask(prefix, bits)
	return leftIP.Mask(mask).Equal(rightIP.Mask(mask))
}

func (s *ManagerHAService) SaveConfig(ctx context.Context, cfg managerdomain.HAConfig) (managerdomain.HAConfig, error) {
	cfg.VIP, cfg.Interface = strings.TrimSpace(cfg.VIP), strings.TrimSpace(cfg.Interface)
	if cfg.Prefix == 0 {
		cfg.Prefix = 24
	}
	if cfg.Prefix < 1 || cfg.Prefix > 128 {
		return cfg, errors.New("VIP 前缀长度不合法")
	}
	if cfg.InstallDir == "" {
		cfg.InstallDir = "/opt/gmha"
	}
	if cfg.ServiceName == "" {
		cfg.ServiceName = "gmha-manager"
	}
	if cfg.Enabled {
		if net.ParseIP(cfg.VIP) == nil {
			return cfg, errors.New("请输入有效的 Manager VIP")
		}
		if cfg.Interface == "" {
			return cfg, errors.New("VIP 网卡不能为空")
		}
		status, _ := s.runtime.GetStatus(ctx)
		if status.Config.DatabaseDriver == "sqlite" {
			return cfg, errors.New("Manager 高可用必须使用 MySQL 或 PostgreSQL 共享数据库")
		}
	}
	if err := s.repo.SaveConfig(ctx, cfg); err != nil {
		return cfg, err
	}
	if cfg.Interface != "" {
		if nodes, listErr := s.repo.ListNodes(ctx); listErr == nil {
			for _, node := range nodes {
				if node.Role == "active" {
					node.VIPInterface = cfg.Interface
					_ = s.repo.SaveNode(ctx, node)
					break
				}
			}
		}
	}
	if cfg.Enabled {
		runtimeStatus, statusErr := s.runtime.GetStatus(ctx)
		if statusErr != nil {
			return cfg, statusErr
		}
		runtimeCfg := runtimeStatus.Config
		runtimeCfg.ManagerHTTPAddr = "http://" + net.JoinHostPort(cfg.VIP, managerListenPort(runtimeCfg.ListenHTTP, "8080"))
		runtimeCfg.ManagerGRPCAddr = net.JoinHostPort(cfg.VIP, managerListenPort(runtimeCfg.ListenGRPC, "9100"))
		if err := s.runtime.SaveConfig(ctx, runtimeCfg); err != nil {
			return cfg, fmt.Errorf("保存 VIP 广播地址失败: %w", err)
		}
	}
	return s.repo.GetConfig(ctx)
}

func (s *ManagerHAService) AddNode(ctx context.Context, req AddManagerNodeRequest) (managerdomain.Node, error) {
	status, err := s.runtime.GetStatus(ctx)
	if err != nil {
		return managerdomain.Node{}, err
	}
	if status.Config.DatabaseDriver == "sqlite" {
		return managerdomain.Node{}, errors.New("扩展 Manager 节点前必须切换到 MySQL 或 PostgreSQL 共享数据库")
	}
	machine, ok, err := s.machines.GetByID(ctx, strings.TrimSpace(req.MachineID))
	if err != nil || !ok {
		return managerdomain.Node{}, errors.New("目标机器不存在或尚未纳管")
	}
	if req.HTTPPort == 0 {
		req.HTTPPort = 8080
	}
	if req.GRPCPort == 0 {
		req.GRPCPort = 9100
	}
	if req.InstallDir == "" {
		req.InstallDir = "/opt/gmha"
	}
	token, err := s.createBootstrapGrant(status.Config, machine.IP)
	if err != nil {
		return managerdomain.Node{}, err
	}
	source := "http://" + net.JoinHostPort(advertisedLocalIP(), managerListenPort(status.Config.ListenHTTP, "8080"))
	haCfg, _ := s.repo.GetConfig(ctx)
	advertisedHTTPHost, advertisedGRPCHost := machine.IP, machine.IP
	if haCfg.Enabled && net.ParseIP(haCfg.VIP) != nil {
		advertisedHTTPHost, advertisedGRPCHost = haCfg.VIP, haCfg.VIP
	}
	serviceName := "gmha-manager"
	envPath := filepath.Join(req.InstallDir, "manager.env")
	binaryPath := filepath.Join(req.InstallDir, "gmha")
	unit := fmt.Sprintf(`[Unit]
Description=GMHA Manager
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
EnvironmentFile=%s
ExecStart=%s serve --listen :%d --grpc-listen :%d --agent-binary %s/bin/agentd --manager-http-addr http://%s:%d --manager-grpc-addr %s:%d
Restart=always
RestartSec=3
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
`, envPath, binaryPath, req.HTTPPort, req.GRPCPort, req.InstallDir, advertisedHTTPHost, req.HTTPPort, advertisedGRPCHost, req.GRPCPort)
	commands := []taskdomain.ExecCommandStep{
		{Name: "准备安装目录", Command: fmt.Sprintf("mkdir -p %s && chmod 700 %s", shellQuote(req.InstallDir), shellQuote(req.InstallDir))},
		{Name: "下载 Manager 内核", Command: fmt.Sprintf("curl -fsSL %s/api/v1/manager/ha/bootstrap/binary?token=%s -o %s && chmod 0755 %s", shellQuote(source), shellQuote(token), shellQuote(binaryPath), shellQuote(binaryPath))},
		{Name: "写入共享数据库配置", Command: fmt.Sprintf("curl -fsSL %s/api/v1/manager/ha/bootstrap/config?token=%s -o %s && chmod 0600 %s", shellQuote(source), shellQuote(token), shellQuote(envPath), shellQuote(envPath))},
		{Name: "安装 systemd 服务", Command: fmt.Sprintf("printf %%s %s > /etc/systemd/system/%s.service && systemctl daemon-reload && systemctl enable --now %s", shellQuote(unit), shellQuote(serviceName), shellQuote(serviceName))},
		{Name: "检查 Manager 健康", Command: fmt.Sprintf("for i in $(seq 1 30); do curl -fsS http://127.0.0.1:%d/api/v1/healthz && exit 0; sleep 1; done; exit 1", req.HTTPPort)},
	}
	detail, err := s.tasks.CreateExecTaskWithOptions(ctx, machine.IP, "", ExecTaskOptions{
		Operation: "manager_node_install", DisplayName: "安装 Manager 高可用节点",
		Commands: commands, TaskType: taskdomain.TypeExec,
	})
	if err != nil {
		return managerdomain.Node{}, err
	}
	directHTTPAddress := "http://" + net.JoinHostPort(machine.IP, strconv.Itoa(req.HTTPPort))
	node := managerdomain.Node{
		ID: managerNodeID(machine.IP, directHTTPAddress), MachineID: machine.ID, Name: machine.Name, IP: machine.IP,
		HTTPAddress:  directHTTPAddress,
		GRPCAddress:  net.JoinHostPort(machine.IP, strconv.Itoa(req.GRPCPort)),
		VIPInterface: strings.TrimSpace(req.Interface),
		Role:         "standby", State: "installing", Version: status.Version, TaskID: detail.Task.ID,
	}
	if err := s.repo.SaveNode(ctx, node); err != nil {
		return managerdomain.Node{}, err
	}
	return node, nil
}

func (s *ManagerHAService) NodeAction(ctx context.Context, id, action string) (string, error) {
	node, ok, err := s.repo.GetNode(ctx, id)
	if err != nil || !ok {
		return "", errors.New("Manager 节点不存在")
	}
	action = strings.ToLower(strings.TrimSpace(action))
	command := ""
	switch action {
	case "start", "restart", "stop":
		command = "systemctl " + action + " gmha-manager"
	default:
		return "", errors.New("不支持的 Manager 节点操作")
	}
	detail, err := s.tasks.CreateExecTaskWithOptions(ctx, node.IP, command, ExecTaskOptions{
		Operation: "manager_node_" + action, DisplayName: "Manager 节点" + action, TaskType: taskdomain.TypeExec,
	})
	if err != nil {
		return "", err
	}
	node.TaskID, node.State = detail.Task.ID, map[string]string{"start": "starting", "restart": "restarting", "stop": "stopping"}[action]
	_ = s.repo.SaveNode(ctx, node)
	return detail.Task.ID, nil
}

func (s *ManagerHAService) SwitchVIP(ctx context.Context, targetID, interfaceName string) (ManagerVIPSwitchResult, error) {
	cfg, err := s.repo.GetConfig(ctx)
	if err != nil {
		return ManagerVIPSwitchResult{}, err
	}
	if !cfg.Enabled || cfg.VIP == "" {
		return ManagerVIPSwitchResult{}, errors.New("请先启用并完整配置 Manager VIP")
	}
	nodes, err := s.repo.ListNodes(ctx)
	if err != nil {
		return ManagerVIPSwitchResult{}, err
	}
	var source, target managerdomain.Node
	for _, node := range nodes {
		if node.Role == "active" {
			source = node
		}
		if node.ID == targetID {
			target = node
		}
	}
	if target.ID == "" {
		return ManagerVIPSwitchResult{}, errors.New("目标 Manager 节点不存在")
	}
	if target.State != "online" {
		return ManagerVIPSwitchResult{}, errors.New("目标 Manager 节点不在线，不能承接 VIP")
	}
	targetInterface := firstNonEmpty(strings.TrimSpace(interfaceName), target.VIPInterface, cfg.Interface)
	if targetInterface == "" {
		return ManagerVIPSwitchResult{}, errors.New("请选择目标 Manager 节点的 VIP 网卡")
	}
	sourceInterface := firstNonEmpty(source.VIPInterface, cfg.Interface)
	if source.ID == target.ID && sourceInterface == "" {
		sourceInterface = targetInterface
	}
	if source.ID != "" && sourceInterface == "" {
		return ManagerVIPSwitchResult{}, errors.New("原 Manager 节点缺少 VIP 网卡配置，无法安全释放 VIP")
	}
	target.VIPInterface = targetInterface
	_ = s.repo.SaveNode(ctx, target)
	s.mu.Lock()
	currentID := s.currentID
	s.mu.Unlock()
	acquireLocal := target.ID == currentID
	if target.MachineID == "" && !acquireLocal {
		return ManagerVIPSwitchResult{}, errors.New("目标 Manager 节点未关联纳管机器，无法通过 Agent 执行 VIP 漂移")
	}
	if source.ID == target.ID {
		go s.finishVIPSwitch(source, target, cfg, "", false, acquireLocal, sourceInterface, targetInterface)
		return ManagerVIPSwitchResult{FromNodeID: source.ID, ToNodeID: target.ID, Status: "ensuring"}, nil
	}
	var firstTask string
	releaseLocal := false
	if source.ID != "" && source.MachineID != "" {
		command := fmt.Sprintf("ip addr del %s/%d dev %s 2>/dev/null || true", shellQuote(cfg.VIP), cfg.Prefix, shellQuote(sourceInterface))
		detail, createErr := s.tasks.CreateExecTaskWithOptions(ctx, source.IP, command, ExecTaskOptions{Operation: "manager_vip_release", DisplayName: "释放 Manager VIP", TaskType: taskdomain.TypeExec})
		if createErr != nil {
			return ManagerVIPSwitchResult{}, createErr
		}
		firstTask = detail.Task.ID
	} else if source.ID != "" {
		s.mu.Lock()
		releaseLocal = source.ID == s.currentID
		s.mu.Unlock()
		if !releaseLocal {
			return ManagerVIPSwitchResult{}, errors.New("原 Manager 节点未关联纳管机器，无法安全释放 VIP")
		}
	}
	go s.finishVIPSwitch(source, target, cfg, firstTask, releaseLocal, acquireLocal, sourceInterface, targetInterface)
	return ManagerVIPSwitchResult{FromNodeID: source.ID, ToNodeID: target.ID, TaskID: firstTask, Status: "switching"}, nil
}

func (s *ManagerHAService) finishVIPSwitch(source, target managerdomain.Node, cfg managerdomain.HAConfig, releaseTask string, releaseLocal, acquireLocal bool, sourceInterface, targetInterface string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if releaseLocal {
		output, err := exec.CommandContext(ctx, "ip", "addr", "del", fmt.Sprintf("%s/%d", cfg.VIP, cfg.Prefix), "dev", sourceInterface).CombinedOutput()
		message := strings.ToLower(string(output))
		if err != nil && !strings.Contains(message, "cannot assign requested address") && !strings.Contains(message, "not found") {
			target.LastError, target.State = "本机释放 VIP 失败: "+strings.TrimSpace(string(output)), "error"
			_ = s.repo.SaveNode(context.Background(), target)
			return
		}
	}
	if releaseTask != "" {
		if detail, err := s.tasks.WaitForTask(ctx, releaseTask, 60*time.Second); err != nil || detail.Task.Status != taskdomain.StatusSuccess {
			target.LastError, target.State = "释放原节点 VIP 失败", "error"
			_ = s.repo.SaveNode(context.Background(), target)
			return
		}
	}
	if acquireLocal {
		output, err := exec.CommandContext(ctx, "ip", "addr", "replace", fmt.Sprintf("%s/%d", cfg.VIP, cfg.Prefix), "dev", targetInterface).CombinedOutput()
		if err != nil {
			target.LastError, target.State = "本机绑定 VIP 失败: "+strings.TrimSpace(string(output)), "error"
			_ = s.repo.SaveNode(context.Background(), target)
			return
		}
		if arping, lookupErr := exec.LookPath("arping"); lookupErr == nil {
			_, _ = exec.CommandContext(ctx, arping, "-A", "-c", "3", "-I", targetInterface, cfg.VIP).CombinedOutput()
		}
		_ = s.repo.SetActive(context.Background(), target.ID, time.Now().UTC())
		return
	}
	command := fmt.Sprintf("ip addr replace %s/%d dev %s && (command -v arping >/dev/null && arping -A -c 3 -I %s %s || true)",
		shellQuote(cfg.VIP), cfg.Prefix, shellQuote(targetInterface), shellQuote(targetInterface), shellQuote(cfg.VIP))
	detail, err := s.tasks.CreateExecTaskWithOptions(ctx, target.IP, command, ExecTaskOptions{Operation: "manager_vip_acquire", DisplayName: "接管 Manager VIP", TaskType: taskdomain.TypeExec})
	if err != nil {
		target.LastError, target.State = err.Error(), "error"
		_ = s.repo.SaveNode(context.Background(), target)
		return
	}
	target.TaskID, target.State = detail.Task.ID, "switching"
	_ = s.repo.SaveNode(context.Background(), target)
	if result, err := s.tasks.WaitForTask(ctx, detail.Task.ID, 60*time.Second); err != nil || result.Task.Status != taskdomain.StatusSuccess {
		target.LastError, target.State = "目标节点绑定 VIP 失败", "error"
		_ = s.repo.SaveNode(context.Background(), target)
		return
	}
	_ = s.repo.SetActive(context.Background(), target.ID, time.Now().UTC())
}

func (s *ManagerHAService) createBootstrapGrant(cfg ManagerRuntimeConfig, nodeIP string) (string, error) {
	bytes := make([]byte, 24)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	token := hex.EncodeToString(bytes)
	s.mu.Lock()
	s.grants[token] = managerBootstrapGrant{Config: normalizeManagerRuntimeConfig(cfg), NodeIP: strings.TrimSpace(nodeIP), ExpiresAt: time.Now().Add(15 * time.Minute)}
	s.mu.Unlock()
	return token, nil
}

func (s *ManagerHAService) BootstrapConfig(token string) ([]byte, error) {
	s.mu.Lock()
	grant, ok := s.grants[strings.TrimSpace(token)]
	s.mu.Unlock()
	if !ok || time.Now().After(grant.ExpiresAt) {
		return nil, errors.New("Manager 节点安装令牌无效或已过期")
	}
	cfg := grant.Config
	lines := []string{
		"GMHA_DB_DRIVER=" + strconv.Quote(cfg.DatabaseDriver),
		"GMHA_DB_DSN=" + strconv.Quote(cfg.DatabaseDSN),
		"GMHA_DB_PATH=" + strconv.Quote(cfg.DBPath),
		"GMHA_MANAGER_PUBKEY=" + strconv.Quote(cfg.ManagerPublicKey),
		"GMHA_NODE_IP=" + strconv.Quote(grant.NodeIP),
	}
	return []byte(strings.Join(lines, "\n") + "\n"), nil
}

func (s *ManagerHAService) ValidateBootstrapToken(token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	grant, ok := s.grants[strings.TrimSpace(token)]
	if !ok || time.Now().After(grant.ExpiresAt) {
		return errors.New("Manager 节点安装令牌无效或已过期")
	}
	return nil
}

func managerNodeID(ip, address string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(ip) + "\x00" + strings.TrimSpace(address)))
	return "manager-" + hex.EncodeToString(sum[:8])
}

func advertisedLocalIP() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err == nil {
		defer conn.Close()
		if address, ok := conn.LocalAddr().(*net.UDPAddr); ok && address.IP != nil {
			return address.IP.String()
		}
	}
	return "127.0.0.1"
}

func buildVersion() string {
	return buildinfo.CurrentVersion()
}

func managerListenPort(listen, fallback string) string {
	if strings.HasPrefix(listen, ":") {
		return strings.TrimPrefix(listen, ":")
	}
	_, port, err := net.SplitHostPort(listen)
	if err != nil || port == "" {
		return fallback
	}
	return port
}
