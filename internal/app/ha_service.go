package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"regexp"
	"sort"
	"strings"
	"time"

	hadomain "gmha/internal/domain/ha"
	machinedomain "gmha/internal/domain/machine"
	taskdomain "gmha/internal/domain/task"
	mysqlapp "gmha/internal/mysql"
)

// HARepository 定义了高可用领域的仓储接口。
type HARepository interface {
	EnsureDefaultPolicies(ctx context.Context, clusterID string) error
	GetFailoverPolicy(ctx context.Context, clusterID string) (hadomain.FailoverPolicy, error)
	GetNetworkPolicy(ctx context.Context, clusterID string) (hadomain.NetworkPolicy, error)
	ListVIPConfigs(ctx context.Context, clusterID string) ([]hadomain.ClusterVIPConfig, error)
	UpsertVIPBindingState(ctx context.Context, state hadomain.VIPBindingState) error
	GetVIPBindingStates(ctx context.Context, clusterID string) ([]hadomain.VIPBindingState, error)
	ListMachineInterfaces(ctx context.Context, machineID string) ([]hadomain.MachineNetworkInterface, error)
	AcquireFailoverLock(ctx context.Context, clusterID, failoverID, owner string, ttl time.Duration) error
	RenewFailoverLock(ctx context.Context, clusterID, failoverID string, ttl time.Duration) error
	ReleaseFailoverLock(ctx context.Context, clusterID, failoverID string) error
	SaveFailoverEvent(ctx context.Context, event hadomain.FailoverEvent) error
	GetFailoverEvent(ctx context.Context, clusterID, failoverID string) (hadomain.FailoverEvent, bool, error)
	InsertVIPOperationLog(ctx context.Context, clusterID, failoverID, vip, operation, machineID, hostIP, iface, command string, resultCode int, stdout, stderr, operator, status string) error
}

// HAService 是高可用服务，负责故障转移规划和执行、VIP 管理、候选评分。
type HAService struct {
	repo      HARepository
	machines  machinedomain.Repository
	instances MySQLInstanceRepository
	presets   MySQLAccountPresetRepository
	vip       *VIPService
	tasks     *TaskService
}

func NewHAService(repo HARepository, machines machinedomain.Repository, instances MySQLInstanceRepository, presets ...MySQLAccountPresetRepository) *HAService {
	s := &HAService{repo: repo, machines: machines, instances: instances}
	if len(presets) > 0 {
		s.presets = presets[0]
	}
	s.vip = NewVIPService(repo, machines, instances)
	return s
}

func (s *HAService) architectureManagementAccount(ctx context.Context) (string, string) {
	items := defaultMySQLAccountPresets()
	if s.presets != nil {
		if saved, err := s.presets.List(ctx); err == nil {
			items = normalizeMySQLAccountPresets(saved)
		}
	}
	for _, item := range items {
		if item.Role == "mha" && item.Enabled && strings.TrimSpace(item.Username) != "" && item.Password != "" {
			return strings.TrimSpace(item.Username), item.Password
		}
	}
	return "mha", "3306niubi"
}

func (s *HAService) VIP() *VIPService {
	return s.vip
}

type vipConfigWriter interface {
	UpsertVIPConfig(context.Context, hadomain.ClusterVIPConfig) (hadomain.ClusterVIPConfig, error)
	DeleteVIPConfig(context.Context, string, string) error
}

func (s *HAService) ListVIPConfigs(ctx context.Context, clusterID string) ([]hadomain.ClusterVIPConfig, error) {
	return s.repo.ListVIPConfigs(ctx, strings.TrimSpace(clusterID))
}

func (s *HAService) SaveVIPConfig(ctx context.Context, clusterID string, cfg hadomain.ClusterVIPConfig) (hadomain.ClusterVIPConfig, error) {
	writer, ok := s.repo.(vipConfigWriter)
	if !ok {
		return hadomain.ClusterVIPConfig{}, errors.New("VIP config repository is not writable")
	}
	cfg.ClusterID = strings.TrimSpace(clusterID)
	cfg.VIPAddress = strings.TrimSpace(cfg.VIPAddress)
	parsedVIP := net.ParseIP(cfg.VIPAddress)
	if cfg.ClusterID == "" || parsedVIP == nil {
		return hadomain.ClusterVIPConfig{}, errors.New("valid cluster and VIP address are required")
	}
	if parsedVIP.To4() == nil {
		return hadomain.ClusterVIPConfig{}, errors.New("current automatic ARP and BGP VIP drivers require an IPv4 address")
	}
	if cfg.VIPPrefix <= 0 || cfg.VIPPrefix > 32 {
		return hadomain.ClusterVIPConfig{}, errors.New("IPv4 VIP prefix must be between 1 and 32")
	}
	cfg, err := s.resolveAutomaticVIPMode(ctx, cfg.ClusterID, cfg)
	if err != nil {
		return hadomain.ClusterVIPConfig{}, err
	}
	switch cfg.VIPRouteMode {
	case hadomain.VipRouteModeL2ARP:
		cfg.ArpingEnabled = true
	case hadomain.VipRouteModeBGP:
		cfg.BGPEnabled = true
		peerIP := net.ParseIP(cfg.BGPPeerAddress)
		if cfg.BGPLocalAS <= 0 || cfg.BGPPeerAS <= 0 || peerIP == nil || peerIP.To4() == nil {
			return hadomain.ClusterVIPConfig{}, errors.New("BGP mode requires local AS, peer AS and peer address")
		}
		if cfg.BGPRouterID != "" {
			routerID := net.ParseIP(cfg.BGPRouterID)
			if routerID == nil || routerID.To4() == nil {
				return hadomain.ClusterVIPConfig{}, errors.New("BGP router ID must be an IPv4 address")
			}
		}
		if cfg.BGPCommunity != "" && !regexp.MustCompile(`^[0-9]{1,10}:[0-9]{1,10}$`).MatchString(cfg.BGPCommunity) {
			return hadomain.ClusterVIPConfig{}, errors.New("BGP community must use ASN:value format")
		}
	default:
		return hadomain.ClusterVIPConfig{}, fmt.Errorf("unsupported VIP route mode %s", cfg.VIPRouteMode)
	}
	if cfg.DefaultInterface != "" && !regexp.MustCompile(`^[A-Za-z0-9_.:-]+$`).MatchString(cfg.DefaultInterface) {
		return hadomain.ClusterVIPConfig{}, errors.New("invalid VIP network interface")
	}
	if cfg.ArpingCount <= 0 {
		cfg.ArpingCount = 3
	}
	cfg.Enabled, cfg.CheckAfterBind, cfg.ExternalCheckEnabled = true, true, true
	return writer.UpsertVIPConfig(ctx, cfg)
}

// resolveAutomaticVIPMode hides route advertisement internals from callers.
// L2 uses gratuitous ARP; an explicitly configured L3/BGP cluster reuses its
// saved BGP policy. Other legacy modes are normalized away.
func (s *HAService) resolveAutomaticVIPMode(ctx context.Context, clusterID string, cfg hadomain.ClusterVIPConfig) (hadomain.ClusterVIPConfig, error) {
	policy, _ := s.repo.GetNetworkPolicy(ctx, clusterID)
	wantsBGP := strings.EqualFold(policy.NetworkTopology, "L3") || policy.VIPRouteMode == hadomain.VipRouteModeBGP || cfg.VIPRouteMode == hadomain.VipRouteModeBGP
	if wantsBGP {
		if cfg.BGPLocalAS <= 0 || cfg.BGPPeerAS <= 0 || strings.TrimSpace(cfg.BGPPeerAddress) == "" {
			if templates, err := s.repo.ListVIPConfigs(ctx, clusterID); err == nil {
				for _, template := range templates {
					if template.VIPRouteMode == hadomain.VipRouteModeBGP && template.BGPLocalAS > 0 && template.BGPPeerAS > 0 && strings.TrimSpace(template.BGPPeerAddress) != "" {
						cfg.BGPLocalAS, cfg.BGPPeerAS, cfg.BGPPeerAddress = template.BGPLocalAS, template.BGPPeerAS, template.BGPPeerAddress
						cfg.BGPRouterID, cfg.BGPCommunity = template.BGPRouterID, template.BGPCommunity
						break
					}
				}
			}
		}
		if cfg.BGPLocalAS <= 0 || cfg.BGPPeerAS <= 0 || strings.TrimSpace(cfg.BGPPeerAddress) == "" {
			return cfg, errors.New("集群为三层网络，但没有可复用的 BGP 邻居策略；请先完善集群网络策略")
		}
		cfg.VIPRouteMode, cfg.BGPEnabled, cfg.ArpingEnabled = hadomain.VipRouteModeBGP, true, false
		return cfg, nil
	}
	cfg.VIPRouteMode, cfg.ArpingEnabled, cfg.BGPEnabled = hadomain.VipRouteModeL2ARP, true, false
	return cfg, nil
}

// ApplyVIPConfig saves the desired VIP and executes the complete remote bind
// and verification flow over the existing Agent task channel. No SSH or MySQL
// execution credential is accepted by this API.
func (s *HAService) ApplyVIPConfig(ctx context.Context, clusterID, targetMachineID string, cfg hadomain.ClusterVIPConfig) (hadomain.VIPBindingState, error) {
	saved, err := s.SaveVIPConfig(ctx, clusterID, cfg)
	if err != nil {
		return hadomain.VIPBindingState{}, err
	}
	return s.vip.Bind(ctx, strings.TrimSpace(clusterID), strings.TrimSpace(targetMachineID), saved)
}

// RemoveVIPConfig withdraws the live VIP from every cluster node, verifies it
// is absent, and only then deletes Manager's configuration and binding state.
func (s *HAService) RemoveVIPConfig(ctx context.Context, clusterID, vip string) error {
	if err := s.vip.Remove(ctx, strings.TrimSpace(clusterID), strings.TrimSpace(vip)); err != nil {
		return err
	}
	return s.DeleteVIPConfig(ctx, clusterID, vip)
}

func (s *HAService) DeleteVIPConfig(ctx context.Context, clusterID, vip string) error {
	writer, ok := s.repo.(vipConfigWriter)
	if !ok {
		return errors.New("VIP config repository is not writable")
	}
	return writer.DeleteVIPConfig(ctx, strings.TrimSpace(clusterID), strings.TrimSpace(vip))
}

// ConfigureArchitectureExecutor 注入 Agent 任务服务，启用在线架构调整执行器。
func (s *HAService) ConfigureArchitectureExecutor(tasks *TaskService) {
	s.tasks = tasks
	s.vip.tasks = tasks
	if recovery, ok := s.repo.(architectureRunRecoveryRepository); ok {
		_ = recovery.MarkInterruptedArchitectureRuns(context.Background())
	}
}

func (s *HAService) PlanFailover(ctx context.Context, clusterID string) (hadomain.FailoverEvent, error) {
	policy, err := s.repo.GetFailoverPolicy(ctx, clusterID)
	if err != nil {
		return hadomain.FailoverEvent{}, err
	}
	event := hadomain.FailoverEvent{
		FailoverID:     newFailoverID(),
		ClusterID:      clusterID,
		Mode:           policy.FailoverMode,
		SwitchStrategy: policy.SwitchStrategy,
		Status:         hadomain.FailoverStatusInit,
		Reason:         "plan only; no operations executed",
		StartedAt:      time.Now().UTC(),
	}
	return event, nil
}

func (s *HAService) StartFailover(ctx context.Context, clusterID string) (hadomain.FailoverEvent, error) {
	event, err := s.PlanFailover(ctx, clusterID)
	if err != nil {
		return hadomain.FailoverEvent{}, err
	}
	policy, err := s.repo.GetFailoverPolicy(ctx, clusterID)
	if err != nil {
		return event, err
	}
	if !policy.AutoFailoverEnabled {
		return s.fail(ctx, event, "auto failover is disabled by cluster policy", "HIGH", "")
	}
	if policy.SwitchStrategy != hadomain.DefaultSwitchStrategy {
		return s.fail(ctx, event, fmt.Sprintf("switch strategy %s is not implemented; default automatic strategy is safe-wait-replay-auto", policy.SwitchStrategy), "HIGH", "")
	}
	event.Status = hadomain.FailoverStatusAcquireLock
	if err := s.repo.SaveFailoverEvent(ctx, event); err != nil {
		return event, err
	}
	if err := s.repo.AcquireFailoverLock(ctx, clusterID, event.FailoverID, "gmha-manager", 10*time.Minute); err != nil {
		return s.fail(ctx, event, err.Error(), "HIGH", "")
	}
	defer func() { _ = s.repo.ReleaseFailoverLock(context.Background(), clusterID, event.FailoverID) }()

	event.Status = hadomain.FailoverStatusFenceOldMaster
	if policy.RequireOldMasterFence {
		return s.fail(ctx, event, "old master fencing executor is not configured; automatic failover stopped by safe-wait-replay-auto", "HIGH", "old master not fenced")
	}
	return s.fail(ctx, event, "failover execution requires live MySQL/Agent integrations; generated guard rails and stopped before unsafe promotion", "MEDIUM", "")
}

func (s *HAService) GetFailover(ctx context.Context, clusterID, failoverID string) (hadomain.FailoverEvent, bool, error) {
	return s.repo.GetFailoverEvent(ctx, clusterID, failoverID)
}

func (s *HAService) fail(ctx context.Context, event hadomain.FailoverEvent, reason, riskLevel, riskSummary string) (hadomain.FailoverEvent, error) {
	event.Status = hadomain.FailoverStatusFailed
	event.Reason = reason
	event.RiskLevel = riskLevel
	event.RiskSummary = riskSummary
	event.FinishedAt = time.Now().UTC()
	err := s.repo.SaveFailoverEvent(ctx, event)
	return event, err
}

// VIPService 是 VIP 管理服务，负责 VIP 绑定状态管理、扫描、验证和驱动选择。
type VIPService struct {
	repo      HARepository
	machines  machinedomain.Repository
	instances MySQLInstanceRepository
	selector  *VIPInterfaceSelector
	drivers   map[string]VipDriver
	tasks     *TaskService
}

func NewVIPService(repo HARepository, machines machinedomain.Repository, instances MySQLInstanceRepository) *VIPService {
	selector := &VIPInterfaceSelector{repo: repo}
	return &VIPService{
		repo:      repo,
		machines:  machines,
		instances: instances,
		selector:  selector,
		drivers: map[string]VipDriver{
			hadomain.VipRouteModeL2ARP:    NewL2ARPVipDriver(nil),
			hadomain.VipRouteModeManual:   NotImplementedVipDriver{Mode: hadomain.VipRouteModeManual, Message: "MANUAL VIP driver does not execute add/del; automatic failover is blocked"},
			hadomain.VipRouteModeBGP:      ArchitectureManagedVIPDriver{Mode: hadomain.VipRouteModeBGP},
			hadomain.VipRouteModeCloudAPI: NotImplementedVipDriver{Mode: hadomain.VipRouteModeCloudAPI, Message: "CLOUD_API VIP driver is not implemented; automatic failover is blocked"},
		},
	}
}

func (s *VIPService) Status(ctx context.Context, clusterID string) ([]hadomain.VIPBindingState, error) {
	return s.repo.GetVIPBindingStates(ctx, clusterID)
}

func (s *VIPService) Scan(ctx context.Context, clusterID string) ([]hadomain.VIPBindingState, error) {
	if s.tasks == nil {
		return nil, errors.New("live VIP scan requires the Agent task executor")
	}
	configs, err := s.repo.ListVIPConfigs(ctx, clusterID)
	if err != nil {
		return nil, err
	}
	if len(configs) == 0 {
		return nil, nil
	}
	machines, err := s.machines.List(ctx)
	if err != nil {
		return nil, err
	}
	parent, err := s.tasks.CreateBatchTrackingTask(ctx, "vip_scan", "扫描集群 VIP 绑定状态", clusterID)
	if err != nil {
		return nil, err
	}
	created, createFailed := 0, 0
	defer func() {
		_ = s.tasks.FinalizeBatchTrackingTask(context.WithoutCancel(ctx), parent.Task.ID, created, createFailed)
	}()
	previousStates, _ := s.repo.GetVIPBindingStates(ctx, clusterID)
	previousByVIP := make(map[string]hadomain.VIPBindingState, len(previousStates))
	for _, state := range previousStates {
		previousByVIP[state.VIPAddress] = state
	}
	var out []hadomain.VIPBindingState
	for _, cfg := range configs {
		var holders []string
		holderInterfaces := make(map[string]string)
		for _, machine := range machines {
			if machine.Cluster != clusterID {
				continue
			}
			command := vipScanCommand(cfg.VIPAddress)
			detail, createErr := s.tasks.CreateExecTaskWithOptions(ctx, machine.IP, command, ExecTaskOptions{
				ParentTaskID: parent.Task.ID, Operation: "vip_scan", DisplayName: "检测 VIP " + cfg.VIPAddress, StepName: "检查目标机器网卡地址",
			})
			if createErr != nil {
				createFailed++
				return nil, createErr
			}
			created++
			completed, waitErr := s.tasks.WaitForTask(ctx, detail.Task.ID, 30*time.Second)
			if waitErr != nil || completed.Task.Status != taskdomain.StatusSuccess {
				return nil, fmt.Errorf("VIP scan failed on %s", machine.Name)
			}
			iface := vipScanInterface(completed)
			if iface != "" {
				holders = append(holders, machine.ID)
				holderInterfaces[machine.ID] = iface
			}
		}
		previous := previousByVIP[cfg.VIPAddress]
		expectedHolder := previous.ExpectedHolderMachineID
		checkedAt := time.Now().UTC()
		status := hadomain.VipStatusUnbound
		if len(holders) == 1 {
			status = hadomain.VipStatusBound
			if expectedHolder != "" && holders[0] != expectedHolder {
				status = hadomain.VipStatusMismatch
			}
		}
		if len(holders) > 1 {
			status = hadomain.VipStatusConflict
		}
		state := hadomain.VIPBindingState{
			TaskID:                  parent.Task.ID,
			ClusterID:               clusterID,
			VIPConfigID:             cfg.ID,
			VIPAddress:              cfg.VIPAddress,
			VIPStatus:               status,
			DetectedHolders:         strings.Join(holders, ","),
			ExpectedHolderMachineID: expectedHolder,
			LastCheckResult:         fmt.Sprintf("live Agent scan found %d holder(s)", len(holders)),
			CreatedAt:               checkedAt,
			UpdatedAt:               checkedAt,
		}
		if len(holders) == 1 {
			state.CurrentHolderMachineID = holders[0]
			if state.ExpectedHolderMachineID == "" {
				state.ExpectedHolderMachineID = holders[0]
			}
			state.CurrentInterface = holderInterfaces[holders[0]]
		}
		if err := s.repo.UpsertVIPBindingState(ctx, state); err != nil {
			return nil, err
		}
		_ = s.repo.InsertVIPOperationLog(ctx, clusterID, "", cfg.VIPAddress, "CHECK", "", "", "", "", 0, "", "", "gmha-manager", "SUCCESS")
		out = append(out, state)
	}
	return out, nil
}

const vipScanMarker = "__GMHA_VIP_SCAN__"

func vipScanCommand(vip string) string {
	return "iface=$(ip -o -4 addr show | awk -v vip=" + shellQuote(vip) + " '$4 ~ (\"^\" vip \"/\") {print $2; exit}'); printf '" + vipScanMarker + "%s\\n' \"${iface:-UNBOUND}\""
}

func vipScanInterface(detail TaskDetail) string {
	for index := len(detail.Steps) - 1; index >= 0; index-- {
		message := detail.Steps[index].Message
		if marker := strings.LastIndex(message, vipScanMarker); marker >= 0 {
			value := strings.TrimSpace(strings.SplitN(message[marker+len(vipScanMarker):], "\n", 2)[0])
			if value != "" && value != "UNBOUND" {
				return value
			}
		}
	}
	return ""
}

func (s *VIPService) clusterMachines(ctx context.Context, clusterID string) ([]machinedomain.Machine, error) {
	items, err := s.machines.List(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]machinedomain.Machine, 0, len(items))
	for _, item := range items {
		if item.Cluster == clusterID {
			result = append(result, item)
		}
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("集群 %s 中没有可执行 VIP 操作的机器", clusterID)
	}
	return result, nil
}

func (s *VIPService) runCommand(ctx context.Context, machine machinedomain.Machine, operation, display, step, command string) (string, error) {
	if s.tasks == nil {
		return "", errors.New("VIP 操作需要在线 Agent 任务通道")
	}
	detail, err := s.tasks.CreateExecTaskWithOptions(ctx, machine.IP, command, ExecTaskOptions{Operation: operation, DisplayName: display, StepName: step})
	if err != nil {
		return "", err
	}
	completed, err := s.tasks.WaitForTask(ctx, detail.Task.ID, 45*time.Second)
	if err != nil {
		return detail.Task.ID, err
	}
	if completed.Task.Status != taskdomain.StatusSuccess {
		return detail.Task.ID, fmt.Errorf("%s 在 %s 执行失败: %s", display, machine.Name, emptyTaskError(completed))
	}
	return detail.Task.ID, nil
}

func (s *VIPService) Bind(ctx context.Context, clusterID, targetMachineID string, cfg hadomain.ClusterVIPConfig) (hadomain.VIPBindingState, error) {
	var result hadomain.VIPBindingState
	err := s.withVIPOperationLock(ctx, clusterID, "bind", func(operationCtx context.Context) error {
		var bindErr error
		result, bindErr = s.bindLocked(operationCtx, clusterID, targetMachineID, cfg)
		return bindErr
	})
	return result, err
}

func (s *VIPService) withVIPOperationLock(ctx context.Context, clusterID, operation string, execute func(context.Context) error) error {
	const lockTTL = 5 * time.Minute
	lockID := "vip-" + operation + "-" + strings.TrimPrefix(newFailoverID(), "fo-")
	if err := s.repo.AcquireFailoverLock(ctx, clusterID, lockID, "gmha-vip", lockTTL); err != nil {
		return fmt.Errorf("集群正在执行其他高可用操作，VIP %s 已阻止: %w", operation, err)
	}
	executionCtx, cancel := context.WithCancel(ctx)
	lockErrors := make(chan error, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-executionCtx.Done():
				return
			case <-ticker.C:
				if err := s.repo.RenewFailoverLock(context.Background(), clusterID, lockID, lockTTL); err != nil {
					lockErrors <- err
					cancel()
					return
				}
			}
		}
	}()
	operationErr := execute(executionCtx)
	cancel()
	<-done
	_ = s.repo.ReleaseFailoverLock(context.Background(), clusterID, lockID)
	select {
	case lockErr := <-lockErrors:
		return fmt.Errorf("VIP 操作失去集群互斥锁，已终止: %w", lockErr)
	default:
		return operationErr
	}
}

func (s *VIPService) bindLocked(ctx context.Context, clusterID, targetMachineID string, cfg hadomain.ClusterVIPConfig) (hadomain.VIPBindingState, error) {
	machines, err := s.clusterMachines(ctx, clusterID)
	if err != nil {
		return hadomain.VIPBindingState{}, err
	}
	var target machinedomain.Machine
	for _, machine := range machines {
		if machine.ID == targetMachineID {
			target = machine
			break
		}
	}
	if target.ID == "" {
		return hadomain.VIPBindingState{}, errors.New("请选择当前集群中的 VIP 持有机器")
	}
	if cfg.VIPRouteMode == hadomain.VipRouteModeL2ARP {
		iface, selectErr := s.selector.Select(ctx, SelectVIPInterfaceRequest{ClusterID: clusterID, MachineID: target.ID, DefaultInterface: cfg.DefaultInterface, VIPAddress: cfg.VIPAddress})
		if selectErr != nil {
			return hadomain.VIPBindingState{}, selectErr
		}
		cfg.DefaultInterface = iface
	}
	for _, machine := range machines {
		command := l2VIPRemoveCommand(cfg)
		if cfg.VIPRouteMode == hadomain.VipRouteModeBGP {
			command = bgpVIPWithdrawCommand(cfg)
		}
		if _, err := s.runCommand(ctx, machine, "vip_withdraw", "撤销旧 VIP "+cfg.VIPAddress, "确保旧持有者已解除绑定", command); err != nil {
			return hadomain.VIPBindingState{}, err
		}
	}
	withdrawnStates, err := s.Scan(ctx, clusterID)
	if err != nil {
		return hadomain.VIPBindingState{}, fmt.Errorf("VIP %s 零持有者屏障检查失败: %w", cfg.VIPAddress, err)
	}
	for _, state := range withdrawnStates {
		if state.VIPAddress == cfg.VIPAddress && state.VIPStatus != hadomain.VipStatusUnbound {
			return state, fmt.Errorf("VIP %s 撤销后仍存在持有者 %s，已阻止重新绑定", cfg.VIPAddress, state.DetectedHolders)
		}
	}
	bindCommand := l2VIPBindCommand(cfg)
	if cfg.VIPRouteMode == hadomain.VipRouteModeBGP {
		bindCommand = bgpVIPAnnounceCommand(cfg, target)
	}
	bindTaskID, err := s.runCommand(ctx, target, "vip_bind", "绑定 VIP "+cfg.VIPAddress, "绑定并自动宣告 VIP", bindCommand)
	if err != nil {
		_ = s.repo.UpsertVIPBindingState(context.WithoutCancel(ctx), hadomain.VIPBindingState{ClusterID: clusterID, VIPConfigID: cfg.ID, VIPAddress: cfg.VIPAddress, ExpectedHolderMachineID: target.ID, CurrentInterface: cfg.DefaultInterface, VIPStatus: hadomain.VipStatusFailed, LastError: err.Error()})
		return hadomain.VIPBindingState{}, err
	}
	var verified hadomain.VIPBindingState
	for round := 1; round <= 2; round++ {
		states, scanErr := s.Scan(ctx, clusterID)
		if scanErr != nil {
			rollback := l2VIPRemoveCommand(cfg)
			if cfg.VIPRouteMode == hadomain.VipRouteModeBGP {
				rollback = bgpVIPWithdrawCommand(cfg)
			}
			_, _ = s.runCommand(context.WithoutCancel(ctx), target, "vip_rollback", "回滚 VIP "+cfg.VIPAddress, "全节点验证不可用，撤销新节点 VIP", rollback)
			failed := hadomain.VIPBindingState{ClusterID: clusterID, VIPConfigID: cfg.ID, VIPAddress: cfg.VIPAddress, ExpectedHolderMachineID: target.ID, VIPStatus: hadomain.VipStatusFailed, LastError: "cluster-wide verification unavailable; new target binding was withdrawn"}
			_ = s.repo.UpsertVIPBindingState(context.WithoutCancel(ctx), failed)
			return failed, scanErr
		}
		verified = hadomain.VIPBindingState{ClusterID: clusterID, VIPConfigID: cfg.ID, VIPAddress: cfg.VIPAddress, ExpectedHolderMachineID: target.ID}
		for _, state := range states {
			if state.VIPAddress == cfg.VIPAddress {
				verified = state
				break
			}
		}
		if verified.VIPAddress == "" || verified.VIPStatus != hadomain.VipStatusBound || verified.CurrentHolderMachineID != target.ID {
			rollback := l2VIPRemoveCommand(cfg)
			if cfg.VIPRouteMode == hadomain.VipRouteModeBGP {
				rollback = bgpVIPWithdrawCommand(cfg)
			}
			_, _ = s.runCommand(context.WithoutCancel(ctx), target, "vip_rollback", "回滚 VIP "+cfg.VIPAddress, "唯一持有者验证失败，撤销新节点 VIP", rollback)
			verified.VIPStatus = hadomain.VipStatusFailed
			verified.LastError = fmt.Sprintf("第 %d 轮唯一持有者验证失败；新目标绑定已回滚", round)
			_ = s.repo.UpsertVIPBindingState(context.WithoutCancel(ctx), verified)
			return verified, fmt.Errorf("VIP %s 第 %d 轮单持有者复检失败，当前持有者 %s", cfg.VIPAddress, round, verified.DetectedHolders)
		}
	}
	verified.TaskID = bindTaskID
	verified.ExpectedHolderMachineID = target.ID
	verified.LastCheckResult = "two consecutive cluster-wide scans verified one holder"
	_ = s.repo.UpsertVIPBindingState(ctx, verified)
	_ = s.repo.InsertVIPOperationLog(ctx, clusterID, "", cfg.VIPAddress, "ADD", target.ID, target.IP, verified.CurrentInterface, "Agent managed automatic advertisement with two-round verification", 0, "", "", "gmha-manager", "SUCCESS")
	return verified, nil
}

func (s *VIPService) Remove(ctx context.Context, clusterID, vip string) error {
	return s.withVIPOperationLock(ctx, clusterID, "delete", func(operationCtx context.Context) error {
		return s.removeLocked(operationCtx, clusterID, vip)
	})
}

func (s *VIPService) removeLocked(ctx context.Context, clusterID, vip string) error {
	configs, err := s.repo.ListVIPConfigs(ctx, clusterID)
	if err != nil {
		return err
	}
	var cfg hadomain.ClusterVIPConfig
	for _, item := range configs {
		if item.VIPAddress == vip {
			cfg = item
			break
		}
	}
	if cfg.ID == 0 {
		return fmt.Errorf("VIP %s 未配置", vip)
	}
	machines, err := s.clusterMachines(ctx, clusterID)
	if err != nil {
		return err
	}
	for _, machine := range machines {
		command := l2VIPRemoveCommand(cfg)
		if cfg.VIPRouteMode == hadomain.VipRouteModeBGP {
			command = bgpVIPWithdrawCommand(cfg)
		}
		if _, err := s.runCommand(ctx, machine, "vip_delete", "删除 VIP "+vip, "撤销 VIP 并验证不存在", command); err != nil {
			return err
		}
	}
	states, err := s.Scan(ctx, clusterID)
	if err != nil {
		return err
	}
	for _, state := range states {
		if state.VIPAddress == vip && state.VIPStatus != hadomain.VipStatusUnbound {
			return fmt.Errorf("VIP %s 删除复检失败，仍有持有者 %s", vip, state.DetectedHolders)
		}
	}
	_ = s.repo.InsertVIPOperationLog(ctx, clusterID, "", vip, "DELETE", "", "", "", "Agent managed withdrawal", 0, "", "", "gmha-manager", "SUCCESS")
	return nil
}

func (s *VIPService) Adopt(ctx context.Context, clusterID, vip string) (hadomain.VIPBindingState, error) {
	configs, err := s.repo.ListVIPConfigs(ctx, clusterID)
	if err != nil {
		return hadomain.VIPBindingState{}, err
	}
	for _, cfg := range configs {
		if cfg.VIPAddress != vip {
			continue
		}
		states, scanErr := s.Scan(ctx, clusterID)
		if scanErr != nil {
			return hadomain.VIPBindingState{}, scanErr
		}
		for _, state := range states {
			if state.VIPAddress != vip {
				continue
			}
			if state.VIPStatus == hadomain.VipStatusConflict {
				return hadomain.VIPBindingState{}, fmt.Errorf("cannot adopt VIP %s while multiple holders are detected", vip)
			}
			_ = s.repo.InsertVIPOperationLog(ctx, clusterID, "", cfg.VIPAddress, "ADOPT", state.CurrentHolderMachineID, "", state.CurrentInterface, "live Agent scan", 0, "", "", "gmha-manager", "SUCCESS")
			return state, nil
		}
		return hadomain.VIPBindingState{}, fmt.Errorf("VIP %s was not returned by live scan", vip)
	}
	return hadomain.VIPBindingState{}, fmt.Errorf("vip %s is not configured for cluster %s", vip, clusterID)
}

func (s *VIPService) Validate(ctx context.Context, clusterID string) ([]hadomain.VIPBindingState, error) {
	return s.Scan(ctx, clusterID)
}

func (s *VIPService) DriverFor(ctx context.Context, clusterID string, cfg hadomain.ClusterVIPConfig) (VipDriver, error) {
	network, err := s.repo.GetNetworkPolicy(ctx, clusterID)
	if err != nil {
		return nil, err
	}
	mode := cfg.VIPRouteMode
	if mode == "" {
		mode = network.VIPRouteMode
	}
	if strings.EqualFold(network.NetworkTopology, "L3") && mode == hadomain.VipRouteModeL2ARP {
		return nil, errors.New("network_topology=L3 cannot use L2_ARP VIP drift; configure the cluster BGP policy")
	}
	driver, ok := s.drivers[mode]
	if !ok {
		return nil, fmt.Errorf("vip route mode %s is not supported", mode)
	}
	return driver, nil
}

type VIPInterfaceSelector struct {
	repo HARepository
}

type SelectVIPInterfaceRequest struct {
	ClusterID                 string
	MachineID                 string
	InstanceVIPInterface      string
	DefaultInterface          string
	VIPAddress                string
	RequireSafeExplicitResult bool
}

func (s *VIPInterfaceSelector) Select(ctx context.Context, req SelectVIPInterfaceRequest) (string, error) {
	if strings.TrimSpace(req.InstanceVIPInterface) != "" {
		return strings.TrimSpace(req.InstanceVIPInterface), nil
	}
	if strings.TrimSpace(req.DefaultInterface) != "" {
		return strings.TrimSpace(req.DefaultInterface), nil
	}
	if s == nil || s.repo == nil {
		return "", errors.New("VIP interface selector is not configured")
	}
	interfaces, err := s.repo.ListMachineInterfaces(ctx, req.MachineID)
	if err != nil {
		return "", err
	}
	for _, item := range interfaces {
		if item.CanBindVIP && item.IsUp {
			return item.InterfaceName, nil
		}
	}
	network, err := s.repo.GetNetworkPolicy(ctx, req.ClusterID)
	if err != nil {
		return "", err
	}
	if network.AutoDetectVIPInterface {
		for _, item := range interfaces {
			if !item.IsUp || item.SubnetCIDR == "" {
				continue
			}
			if ipInCIDR(req.VIPAddress, item.SubnetCIDR) {
				return item.InterfaceName, nil
			}
		}
	}
	return "", errors.New("unable to determine VIP interface; configure instance vip_interface, cluster_vip_config.default_interface, or machine_network_interface.can_bind_vip")
}

// CandidateSelector 是候选节点选择器，根据评分选择最优的新主节点。
type CandidateSelector struct{}

func NewCandidateSelector() *CandidateSelector {
	return &CandidateSelector{}
}

func (s *CandidateSelector) Select(scores []hadomain.CandidateScore) (hadomain.CandidateScore, []hadomain.CandidateScore, error) {
	ranked := append([]hadomain.CandidateScore(nil), scores...)
	for i := range ranked {
		if !ranked[i].Eligible {
			ranked[i].FinalScore = -1_000_000
			continue
		}
		dataScore := ranked[i].DataFreshnessScore*100000 + ranked[i].RelayReceivedScore*1000 + ranked[i].RelayExecutedScore
		delayPenalty := ranked[i].DelaySeconds * 100
		if ranked[i].NeedRelayReplay {
			delayPenalty += 50
		}
		ranked[i].FinalScore = dataScore + ranked[i].ElectionPriority + ranked[i].HealthScore - ranked[i].RiskPenalty - delayPenalty
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].Eligible != ranked[j].Eligible {
			return ranked[i].Eligible
		}
		if ranked[i].DataFreshnessScore != ranked[j].DataFreshnessScore {
			return ranked[i].DataFreshnessScore > ranked[j].DataFreshnessScore
		}
		if ranked[i].RelayReceivedScore != ranked[j].RelayReceivedScore {
			return ranked[i].RelayReceivedScore > ranked[j].RelayReceivedScore
		}
		if ranked[i].RelayExecutedScore != ranked[j].RelayExecutedScore {
			return ranked[i].RelayExecutedScore > ranked[j].RelayExecutedScore
		}
		if ranked[i].DelaySeconds != ranked[j].DelaySeconds {
			return ranked[i].DelaySeconds < ranked[j].DelaySeconds
		}
		return ranked[i].FinalScore > ranked[j].FinalScore
	})
	if len(ranked) == 0 || !ranked[0].Eligible {
		return hadomain.CandidateScore{}, ranked, errors.New("no eligible candidate")
	}
	return ranked[0], ranked, nil
}

type RelayStatus struct {
	SQLRunning       bool
	LastSQLError     string
	DelaySeconds     int
	GTIDMode         bool
	ExecutedGTIDSet  string
	RetrievedGTIDSet string
	ExecMasterLogPos int64
	ReadMasterLogPos int64
}

func RelayReplayComplete(st RelayStatus) error {
	if !st.SQLRunning {
		return errors.New("replication SQL thread is not running")
	}
	if strings.TrimSpace(st.LastSQLError) != "" {
		return fmt.Errorf("replication SQL error: %s", st.LastSQLError)
	}
	if st.DelaySeconds != 0 {
		return fmt.Errorf("replication delay is %d seconds", st.DelaySeconds)
	}
	if st.GTIDMode && st.RetrievedGTIDSet != "" && st.ExecutedGTIDSet != st.RetrievedGTIDSet {
		return errors.New("retrieved GTID set has not been fully executed")
	}
	if !st.GTIDMode && st.ReadMasterLogPos > 0 && st.ExecMasterLogPos < st.ReadMasterLogPos {
		return fmt.Errorf("relay log not replayed: exec pos %d read pos %d", st.ExecMasterLogPos, st.ReadMasterLogPos)
	}
	return nil
}

func BuildScoresFromInstances(clusterID string, instances []mysqlapp.Instance, machines map[string]machinedomain.Machine, oldMasterMachineID string) []hadomain.CandidateScore {
	out := make([]hadomain.CandidateScore, 0, len(instances))
	seenServerID := make(map[int]int)
	for _, inst := range instances {
		seenServerID[inst.ServerID]++
	}
	for _, inst := range instances {
		m := machines[inst.MachineID]
		score := hadomain.CandidateScore{
			ClusterID:          clusterID,
			InstanceID:         instanceID(inst),
			MachineID:          inst.MachineID,
			Hostname:           m.Name,
			IP:                 m.IP,
			Port:               inst.Port,
			Eligible:           true,
			DataFreshnessScore: 100,
			RelayReceivedScore: 100,
			RelayExecutedScore: 100,
			HealthScore:        100,
			CanBindVIP:         true,
		}
		if inst.MachineID == oldMasterMachineID {
			score.Eligible = false
			score.RejectReasons = append(score.RejectReasons, "candidate is old master")
		}
		if seenServerID[inst.ServerID] > 1 {
			score.Eligible = false
			score.RejectReasons = append(score.RejectReasons, "server_id is not unique")
		}
		out = append(out, score)
	}
	return out
}

func instanceID(inst mysqlapp.Instance) string {
	return fmt.Sprintf("%s:%d", inst.MachineID, inst.Port)
}

func ipInCIDR(ipValue, cidr string) bool {
	ip := net.ParseIP(strings.TrimSpace(ipValue))
	if ip == nil {
		return false
	}
	_, network, err := net.ParseCIDR(strings.TrimSpace(cidr))
	return err == nil && network.Contains(ip)
}

func newFailoverID() string {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Sprintf("fo-%d", time.Now().UnixNano())
	}
	return "fo-" + hex.EncodeToString(buf[:])
}

func marshalJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
