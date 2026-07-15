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
	vip       *VIPService
	tasks     *TaskService
}

func NewHAService(repo HARepository, machines machinedomain.Repository, instances MySQLInstanceRepository) *HAService {
	s := &HAService{repo: repo, machines: machines, instances: instances}
	s.vip = NewVIPService(repo, machines, instances)
	return s
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
		return hadomain.ClusterVIPConfig{}, errors.New("current ARP, BGP and Keepalived VIP drivers require an IPv4 address")
	}
	if cfg.VIPPrefix <= 0 || cfg.VIPPrefix > 32 {
		return hadomain.ClusterVIPConfig{}, errors.New("IPv4 VIP prefix must be between 1 and 32")
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
	case hadomain.VipRouteModeKeepalived:
		if strings.TrimSpace(cfg.DefaultInterface) == "" {
			return hadomain.ClusterVIPConfig{}, errors.New("Keepalived mode requires an explicit network interface")
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
			hadomain.VipRouteModeL2ARP:      NewL2ARPVipDriver(nil),
			hadomain.VipRouteModeManual:     NotImplementedVipDriver{Mode: hadomain.VipRouteModeManual, Message: "MANUAL VIP driver does not execute add/del; automatic failover is blocked"},
			hadomain.VipRouteModeBGP:        ArchitectureManagedVIPDriver{Mode: hadomain.VipRouteModeBGP},
			hadomain.VipRouteModeCloudAPI:   NotImplementedVipDriver{Mode: hadomain.VipRouteModeCloudAPI, Message: "CLOUD_API VIP driver is not implemented; automatic failover is blocked"},
			hadomain.VipRouteModeKeepalived: ArchitectureManagedVIPDriver{Mode: hadomain.VipRouteModeKeepalived},
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
	var out []hadomain.VIPBindingState
	for _, cfg := range configs {
		var holders []string
		for _, machine := range machines {
			if machine.Cluster != clusterID {
				continue
			}
			command := "if ip -o addr show | awk '{print $4}' | cut -d/ -f1 | grep -Fxq " + shellQuote(cfg.VIPAddress) + "; then echo BOUND; else echo UNBOUND; fi"
			detail, createErr := s.tasks.CreateExecTask(ctx, machine.IP, command)
			if createErr != nil {
				return nil, createErr
			}
			completed, waitErr := s.tasks.WaitForTask(ctx, detail.Task.ID, 30*time.Second)
			if waitErr != nil || completed.Task.Status != taskdomain.StatusSuccess {
				return nil, fmt.Errorf("VIP scan failed on %s", machine.Name)
			}
			if len(completed.Steps) > 0 && strings.TrimSpace(completed.Steps[len(completed.Steps)-1].Message) == "BOUND" {
				holders = append(holders, machine.ID)
			}
		}
		status := hadomain.VipStatusUnbound
		if len(holders) == 1 {
			status = hadomain.VipStatusBound
		}
		if len(holders) > 1 {
			status = hadomain.VipStatusConflict
		}
		state := hadomain.VIPBindingState{
			ClusterID:       clusterID,
			VIPConfigID:     cfg.ID,
			VIPAddress:      cfg.VIPAddress,
			VIPStatus:       status,
			DetectedHolders: strings.Join(holders, ","),
			LastCheckResult: fmt.Sprintf("live Agent scan found %d holder(s)", len(holders)),
		}
		if len(holders) == 1 {
			state.CurrentHolderMachineID = holders[0]
		}
		if err := s.repo.UpsertVIPBindingState(ctx, state); err != nil {
			return nil, err
		}
		_ = s.repo.InsertVIPOperationLog(ctx, clusterID, "", cfg.VIPAddress, "CHECK", "", "", "", "", 0, "", "", "gmha-manager", "SUCCESS")
		out = append(out, state)
	}
	return out, nil
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
		return nil, errors.New("network_topology=L3 cannot use L2_ARP VIP drift; configure BGP/CLOUD_API/KEEPALIVED driver")
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
