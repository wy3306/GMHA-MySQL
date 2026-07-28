package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	hadomain "gmha/internal/domain/ha"
	machinedomain "gmha/internal/domain/machine"
	taskdomain "gmha/internal/domain/task"
	mysqlapp "gmha/internal/mysql"
)

const (
	clusterUpgradePending   = "pending"
	clusterUpgradeRunning   = "running"
	clusterUpgradeSucceeded = "success"
	clusterUpgradeFailed    = "failed"
)

type ClusterUpgradeRequest struct {
	Cluster          string `json:"cluster"`
	TargetVersion    string `json:"target_version"`
	Port             int    `json:"port"`
	RiskAcknowledged bool   `json:"risk_acknowledged"`
}

type ClusterUpgradeNode struct {
	MachineID      string `json:"machine_id"`
	Machine        string `json:"machine"`
	Hostname       string `json:"hostname,omitempty"`
	IP             string `json:"ip"`
	Port           int    `json:"port"`
	Role           string `json:"role"`
	SourceHost     string `json:"source_host,omitempty"`
	SourcePort     int    `json:"source_port,omitempty"`
	CurrentVersion string `json:"current_version"`
	TargetVersion  string `json:"target_version"`
	PackageName    string `json:"package_name"`
	ReadOnly       bool   `json:"read_only"`
	SuperReadOnly  bool   `json:"super_read_only"`
	ReceiverState  string `json:"receiver_state,omitempty"`
	ApplierState   string `json:"applier_state,omitempty"`
	DelaySeconds   int    `json:"delay_seconds,omitempty"`
	ServerUUID     string `json:"server_uuid,omitempty"`
	GroupState     string `json:"group_state,omitempty"`
	GroupRole      string `json:"group_role,omitempty"`
	GroupName      string `json:"group_name,omitempty"`
	PrecheckTaskID string `json:"precheck_task_id,omitempty"`
	UpgradeTaskID  string `json:"upgrade_task_id,omitempty"`
	Status         string `json:"status"`
	Error          string `json:"error,omitempty"`
}

type ClusterUpgradeStage struct {
	Code       string     `json:"code"`
	Name       string     `json:"name"`
	Status     string     `json:"status"`
	Message    string     `json:"message,omitempty"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

type ClusterUpgradePlan struct {
	Cluster                   string                `json:"cluster"`
	Architecture              string                `json:"architecture"`
	TargetVersion             string                `json:"target_version"`
	Port                      int                   `json:"port"`
	Executable                bool                  `json:"executable"`
	BlockingReasons           []string              `json:"blocking_reasons,omitempty"`
	Warnings                  []string              `json:"warnings,omitempty"`
	OriginalPrimaryMachineID  string                `json:"original_primary_machine_id,omitempty"`
	TemporaryPrimaryMachineID string                `json:"temporary_primary_machine_id,omitempty"`
	Nodes                     []ClusterUpgradeNode  `json:"nodes"`
	Stages                    []ClusterUpgradeStage `json:"stages"`
	CreatedAt                 time.Time             `json:"created_at"`
}

type ClusterUpgradeRun struct {
	RunID                     string                `json:"run_id"`
	Cluster                   string                `json:"cluster"`
	Architecture              string                `json:"architecture"`
	TargetVersion             string                `json:"target_version"`
	Port                      int                   `json:"port"`
	Status                    string                `json:"status"`
	CurrentStage              string                `json:"current_stage,omitempty"`
	Error                     string                `json:"error,omitempty"`
	OriginalPrimaryMachineID  string                `json:"original_primary_machine_id"`
	TemporaryPrimaryMachineID string                `json:"temporary_primary_machine_id"`
	Nodes                     []ClusterUpgradeNode  `json:"nodes"`
	Stages                    []ClusterUpgradeStage `json:"stages"`
	ArchitectureRunIDs        []string              `json:"architecture_run_ids,omitempty"`
	SwitchAwayRunID           string                `json:"switch_away_run_id,omitempty"`
	SwitchBackRunID           string                `json:"switch_back_run_id,omitempty"`
	CreatedAt                 time.Time             `json:"created_at"`
	UpdatedAt                 time.Time             `json:"updated_at"`
	FinishedAt                *time.Time            `json:"finished_at,omitempty"`
}

type clusterUpgradeTaskSpec struct {
	Operation   string            `json:"operation"`
	DisplayName string            `json:"display_name"`
	Cluster     string            `json:"cluster"`
	Run         ClusterUpgradeRun `json:"run"`
}

type clusterUpgradeProbe struct {
	MachineID     string
	Hostname      string
	Version       string
	ServerID      int
	ReadOnly      bool
	SuperReadOnly bool
	GTIDMode      string
	SourceHost    string
	SourcePort    int
	ReceiverState string
	ApplierState  string
	ChannelCount  int
	DelaySeconds  int
	ServerUUID    string
	GroupState    string
	GroupRole     string
	GroupName     string
	GroupView     string
	GroupMembers  int
	GroupOnline   int
	GroupPrimary  int
	GroupQueue    int
	OfflineMode   bool
	TaskID        string
}

type ClusterUpgradeService struct {
	tasks  *TaskService
	ha     *HAService
	mu     sync.Mutex
	active map[string]string
}

func NewClusterUpgradeService(tasks *TaskService, ha *HAService) *ClusterUpgradeService {
	return &ClusterUpgradeService{tasks: tasks, ha: ha, active: make(map[string]string)}
}

// RecoverInterrupted safely terminates durable rolling-upgrade tasks after a
// Manager restart. The completed-stage audit trail is retained, but execution
// never resumes from a stale topology snapshot: the operator must generate a
// fresh live plan before making another topology or binary change.
func (s *ClusterUpgradeService) RecoverInterrupted(ctx context.Context) error {
	for _, status := range []taskdomain.Status{taskdomain.StatusPending, taskdomain.StatusRunning} {
		items, err := s.tasks.repo.ListTasksByStatus(ctx, status, 1000)
		if err != nil {
			return err
		}
		for _, task := range items {
			if task.Type != taskdomain.TypeMySQLClusterUpgrade {
				continue
			}
			var spec clusterUpgradeTaskSpec
			if json.Unmarshal(task.SpecJSON, &spec) != nil || spec.Run.RunID == "" {
				continue
			}
			run := spec.Run
			_ = s.ha.repo.ReleaseFailoverLock(ctx, run.Cluster, run.RunID)
			stage := strings.TrimSpace(run.CurrentStage)
			if stage == "" {
				stage = "cluster_preflight"
			}
			s.failRun(&run, stage, errors.New("Manager 在滚动升级期间发生重启；为避免基于过期拓扑继续执行，流程已安全停止。请确认当前 PRIMARY 与业务入口后重新生成实时升级计划"))
		}
	}
	return nil
}

func clusterUpgradeStages() []ClusterUpgradeStage {
	return []ClusterUpgradeStage{
		{Code: "cluster_preflight", Name: "集群拓扑与不停机条件预检", Status: "pending"},
		{Code: "precheck_all_nodes", Name: "全节点版本升级预检", Status: "pending"},
		{Code: "upgrade_replicas", Name: "逐台升级全部从库", Status: "pending"},
		{Code: "verify_replicas", Name: "验证升级后从库与复制", Status: "pending"},
		{Code: "switch_to_upgraded_replica", Name: "切换到已升级从库并迁移 VIP", Status: "pending"},
		{Code: "upgrade_original_primary", Name: "升级已降级的原主库", Status: "pending"},
		{Code: "verify_original_primary", Name: "验证原主库升级与追平", Status: "pending"},
		{Code: "switch_back_original_primary", Name: "切回原主库并迁回 VIP", Status: "pending"},
		{Code: "final_cluster_verify", Name: "最终版本、复制与写入口复核", Status: "pending"},
		{Code: "release_maintenance_lock", Name: "释放集群维护锁", Status: "pending"},
	}
}

func mgrClusterUpgradeStages() []ClusterUpgradeStage {
	stages := clusterUpgradeStages()
	names := map[string]string{
		"cluster_preflight":            "MGR 仲裁、成员与不停机条件预检",
		"upgrade_replicas":             "逐台升级全部 SECONDARY 成员",
		"verify_replicas":              "验证升级后 SECONDARY 重新 ONLINE",
		"switch_to_upgraded_replica":   "将 PRIMARY 切换到已升级成员",
		"upgrade_original_primary":     "升级原 PRIMARY 成员",
		"verify_original_primary":      "验证原 PRIMARY 重新加入复制组",
		"switch_back_original_primary": "将 PRIMARY 切回原成员",
		"final_cluster_verify":         "最终版本、MGR 仲裁与单主状态复核",
		"release_maintenance_lock":     "释放集群维护锁",
	}
	for index := range stages {
		if name := names[stages[index].Code]; name != "" {
			stages[index].Name = name
		}
	}
	return stages
}

func (s *ClusterUpgradeService) Plan(ctx context.Context, req ClusterUpgradeRequest) (ClusterUpgradePlan, error) {
	return s.buildPlan(ctx, req, "")
}

func (s *ClusterUpgradeService) Start(ctx context.Context, req ClusterUpgradeRequest) (ClusterUpgradeRun, error) {
	if !req.RiskAcknowledged {
		return ClusterUpgradeRun{}, errors.New("必须确认已完成可恢复备份，并接受滚动升级期间的短暂连接重建")
	}
	plan, err := s.buildPlan(ctx, req, "")
	if err != nil {
		return ClusterUpgradeRun{}, err
	}
	if !plan.Executable {
		return ClusterUpgradeRun{}, fmt.Errorf("集群滚动升级预检未通过: %s", strings.Join(plan.BlockingReasons, "；"))
	}
	s.mu.Lock()
	if active := s.active[plan.Cluster]; active != "" {
		s.mu.Unlock()
		return ClusterUpgradeRun{}, fmt.Errorf("集群 %s 已有滚动升级任务 %s", plan.Cluster, active)
	}
	now := time.Now().UTC()
	run := ClusterUpgradeRun{
		RunID:   "cluster-upgrade-" + strings.TrimPrefix(newFailoverID(), "fo-"),
		Cluster: plan.Cluster, Architecture: plan.Architecture, TargetVersion: plan.TargetVersion, Port: plan.Port,
		Status: clusterUpgradePending, OriginalPrimaryMachineID: plan.OriginalPrimaryMachineID,
		TemporaryPrimaryMachineID: plan.TemporaryPrimaryMachineID, Nodes: plan.Nodes,
		Stages: plan.Stages, CreatedAt: now, UpdatedAt: now,
	}
	s.active[plan.Cluster] = run.RunID
	s.mu.Unlock()
	if err := s.createTrackingTask(ctx, run); err != nil {
		s.clearActive(run)
		return ClusterUpgradeRun{}, err
	}
	go s.execute(context.Background(), run)
	return run, nil
}

func (s *ClusterUpgradeService) Get(ctx context.Context, runID string) (ClusterUpgradeRun, bool, error) {
	task, ok, err := s.tasks.repo.GetTask(ctx, strings.TrimSpace(runID))
	if err != nil || !ok {
		return ClusterUpgradeRun{}, ok, err
	}
	if task.Type != taskdomain.TypeMySQLClusterUpgrade {
		return ClusterUpgradeRun{}, false, nil
	}
	var spec clusterUpgradeTaskSpec
	if err := json.Unmarshal(task.SpecJSON, &spec); err != nil {
		return ClusterUpgradeRun{}, false, err
	}
	return spec.Run, true, nil
}

func (s *ClusterUpgradeService) buildPlan(ctx context.Context, req ClusterUpgradeRequest, parentTaskID string) (ClusterUpgradePlan, error) {
	req.Cluster = strings.TrimSpace(req.Cluster)
	req.TargetVersion = strings.TrimSpace(req.TargetVersion)
	if req.Cluster == "" || req.TargetVersion == "" {
		return ClusterUpgradePlan{}, errors.New("cluster and target_version are required")
	}
	if req.Port <= 0 || req.Port > 65535 {
		return ClusterUpgradePlan{}, errors.New("valid MySQL port is required")
	}
	plan := ClusterUpgradePlan{Cluster: req.Cluster, Architecture: string(hadomain.ArchitectureMasterSlave), TargetVersion: req.TargetVersion, Port: req.Port, Executable: true, Nodes: []ClusterUpgradeNode{}, Stages: clusterUpgradeStages(), CreatedAt: time.Now().UTC()}
	machines, err := s.tasks.ListClusterMachines(ctx, []string{req.Cluster})
	if err != nil {
		return ClusterUpgradePlan{}, err
	}
	sort.Slice(machines, func(i, j int) bool {
		if machines[i].Name != machines[j].Name {
			return machines[i].Name < machines[j].Name
		}
		return machines[i].IP < machines[j].IP
	})
	instances, err := s.tasks.mysqlInstance.List(ctx)
	if err != nil {
		return ClusterUpgradePlan{}, err
	}
	instanceByMachine := make(map[string]mysqlapp.Instance)
	for _, instance := range instances {
		if instance.Port == req.Port {
			instanceByMachine[instance.MachineID] = instance
		}
	}
	probes := make(map[string]clusterUpgradeProbe, len(machines))
	serverIDOwners := make(map[int]string, len(machines))
	for _, machine := range machines {
		instance, ok := instanceByMachine[machine.ID]
		if !ok {
			plan.Executable = false
			plan.BlockingReasons = append(plan.BlockingReasons, fmt.Sprintf("机器 %s 没有登记 %d 端口的 MySQL 实例", machine.Name, req.Port))
			continue
		}
		if compatible, reason := s.tasks.MachineCapability(machine.ID, taskdomain.CapabilityMySQLDefaultsFile); !compatible {
			plan.Executable = false
			plan.BlockingReasons = append(plan.BlockingReasons, fmt.Sprintf("机器 %s 无法安全升级: %s", machine.Name, reason))
			continue
		}
		probe, probeErr := s.probeNode(ctx, machine, instance, parentTaskID)
		if probeErr != nil {
			plan.Executable = false
			plan.BlockingReasons = append(plan.BlockingReasons, fmt.Sprintf("机器 %s 实时探测失败: %v", machine.Name, probeErr))
			continue
		}
		probes[machine.ID] = probe
		if !strings.EqualFold(strings.TrimSpace(probe.GTIDMode), "ON") {
			plan.Executable = false
			plan.BlockingReasons = append(plan.BlockingReasons, fmt.Sprintf("机器 %s 未启用 GTID，无法执行安全自动定位和主从切换", machine.Name))
		}
		if probe.ServerID <= 0 {
			plan.Executable = false
			plan.BlockingReasons = append(plan.BlockingReasons, fmt.Sprintf("机器 %s 的 server_id 无效", machine.Name))
		} else if owner := serverIDOwners[probe.ServerID]; owner != "" {
			plan.Executable = false
			plan.BlockingReasons = append(plan.BlockingReasons, fmt.Sprintf("机器 %s 与 %s 使用了重复的 server_id=%d", machine.Name, owner, probe.ServerID))
		} else {
			serverIDOwners[probe.ServerID] = machine.Name
		}
		info, infoOK, infoErr := s.tasks.machineInfo.Get(ctx, machine.ID)
		if infoErr != nil || !infoOK {
			plan.Executable = false
			plan.BlockingReasons = append(plan.BlockingReasons, fmt.Sprintf("机器 %s 缺少架构/glibc 静态信息，请先采集机器信息", machine.Name))
			continue
		}
		targetPackage, packageErr := s.tasks.createMySQL.ResolveVersionPackage(info, req.TargetVersion)
		if packageErr != nil {
			plan.Executable = false
			plan.BlockingReasons = append(plan.BlockingReasons, fmt.Sprintf("机器 %s 没有兼容的 MySQL %s 制品: %v", machine.Name, req.TargetVersion, packageErr))
			continue
		}
		if compatibilityErr := mysqlapp.ValidateUpgradeCompatibility(probe.Version, targetPackage.Version); compatibilityErr != nil {
			plan.Executable = false
			plan.BlockingReasons = append(plan.BlockingReasons, fmt.Sprintf("机器 %s 版本路径不可执行: %v", machine.Name, compatibilityErr))
		}
		role := "invalid"
		if probe.GroupState != "" {
			switch {
			case strings.EqualFold(probe.GroupState, "ONLINE") && strings.EqualFold(probe.GroupRole, "PRIMARY"):
				role = "primary"
			case strings.EqualFold(probe.GroupState, "ONLINE") && strings.EqualFold(probe.GroupRole, "SECONDARY"):
				role = "replica"
			}
		} else {
			switch {
			case probe.SourceHost == "" && !probe.ReadOnly && !probe.SuperReadOnly:
				role = "primary"
			case probe.SourceHost != "" && probe.ReadOnly && probe.SuperReadOnly:
				role = "replica"
			}
		}
		plan.Nodes = append(plan.Nodes, ClusterUpgradeNode{
			MachineID: machine.ID, Machine: machine.Name, Hostname: probe.Hostname, IP: machine.IP, Port: instance.Port, Role: role,
			SourceHost: probe.SourceHost, SourcePort: probe.SourcePort, CurrentVersion: probe.Version,
			TargetVersion: targetPackage.Version, PackageName: targetPackage.FileName,
			ReadOnly: probe.ReadOnly, SuperReadOnly: probe.SuperReadOnly,
			ReceiverState: probe.ReceiverState, ApplierState: probe.ApplierState,
			DelaySeconds: probe.DelaySeconds, ServerUUID: probe.ServerUUID,
			GroupState: probe.GroupState, GroupRole: probe.GroupRole, GroupName: probe.GroupName, Status: "ready",
		})
	}
	if len(plan.Nodes) != len(machines) {
		plan.Executable = false
	}
	mgrMembers := 0
	for _, probe := range probes {
		if probe.GroupState != "" {
			mgrMembers++
		}
	}
	isMGR := mgrMembers > 0
	if isMGR {
		plan.Architecture = string(hadomain.ArchitectureMGRRouter)
		plan.Stages = mgrClusterUpgradeStages()
		if mgrMembers != len(machines) {
			plan.Executable = false
			plan.BlockingReasons = append(plan.BlockingReasons, "仅部分节点属于 MGR，拒绝在混合拓扑上执行滚动升级")
		}
		if len(machines) < 3 || len(machines) > 9 || len(machines)%2 == 0 {
			plan.Executable = false
			plan.BlockingReasons = append(plan.BlockingReasons, fmt.Sprintf("MGR 滚动升级要求 3–9 个奇数成员，当前为 %d 个", len(machines)))
		}
		groupName, groupView := "", ""
		localUUIDs := make(map[string]bool, len(plan.Nodes))
		for _, node := range plan.Nodes {
			probe := probes[node.MachineID]
			if strings.TrimSpace(probe.ServerUUID) == "" || strings.TrimSpace(probe.GroupName) == "" {
				plan.Executable = false
				plan.BlockingReasons = append(plan.BlockingReasons, fmt.Sprintf("MGR 成员 %s 缺少 server_uuid 或复制组 UUID", node.Machine))
			}
			localUUIDs[probe.ServerUUID] = true
			if groupName == "" {
				groupName = probe.GroupName
			} else if probe.GroupName != groupName {
				plan.Executable = false
				plan.BlockingReasons = append(plan.BlockingReasons, fmt.Sprintf("MGR 成员 %s 属于不同复制组 %s", node.Machine, probe.GroupName))
			}
			if groupView == "" {
				groupView = probe.GroupView
			} else if probe.GroupView != groupView {
				plan.Executable = false
				plan.BlockingReasons = append(plan.BlockingReasons, fmt.Sprintf("MGR 成员 %s 观察到不同的成员视图", node.Machine))
			}
			if !mgrVersionSupported(probe.Version) {
				plan.Executable = false
				plan.BlockingReasons = append(plan.BlockingReasons, fmt.Sprintf("MGR 成员 %s 运行 %s；安全在线升级要求 MySQL 8.0.17 或更高版本", node.Machine, probe.Version))
			}
			if !strings.EqualFold(probe.GroupState, "ONLINE") || probe.GroupOnline != len(machines) || probe.GroupMembers != len(machines) {
				plan.Executable = false
				plan.BlockingReasons = append(plan.BlockingReasons, fmt.Sprintf("MGR 成员 %s 未处于完整 ONLINE 组（状态=%s，在线=%d/%d）", node.Machine, probe.GroupState, probe.GroupOnline, probe.GroupMembers))
			}
			if probe.GroupPrimary != 1 {
				plan.Executable = false
				plan.BlockingReasons = append(plan.BlockingReasons, fmt.Sprintf("MGR 成员 %s 观察到 %d 个 PRIMARY，必须恰好为 1", node.Machine, probe.GroupPrimary))
			}
			if probe.GroupQueue != 0 {
				plan.Executable = false
				plan.BlockingReasons = append(plan.BlockingReasons, fmt.Sprintf("MGR 成员 %s 观察到 %d 个待应用事务", node.Machine, probe.GroupQueue))
			}
		}
		viewUUIDs := make(map[string]bool)
		for _, value := range strings.Split(groupView, ",") {
			if value = strings.TrimSpace(value); value != "" {
				viewUUIDs[value] = true
			}
		}
		if len(viewUUIDs) != len(localUUIDs) {
			plan.Executable = false
			plan.BlockingReasons = append(plan.BlockingReasons, "MGR 实时成员视图与当前集群机器不一致")
		} else {
			for uuid := range localUUIDs {
				if !viewUUIDs[uuid] {
					plan.Executable = false
					plan.BlockingReasons = append(plan.BlockingReasons, "MGR 实时成员视图包含当前集群之外的成员")
					break
				}
			}
		}
	}
	primaries := make([]ClusterUpgradeNode, 0, 1)
	replicas := make([]ClusterUpgradeNode, 0, len(plan.Nodes))
	for _, node := range plan.Nodes {
		switch node.Role {
		case "primary":
			primaries = append(primaries, node)
		case "replica":
			replicas = append(replicas, node)
		default:
			plan.Executable = false
			plan.BlockingReasons = append(plan.BlockingReasons, fmt.Sprintf("节点 %s 既不是唯一可写主库，也不是只读从库", node.Machine))
		}
	}
	if len(primaries) != 1 {
		plan.Executable = false
		plan.BlockingReasons = append(plan.BlockingReasons, fmt.Sprintf("不停机滚动升级要求恰好一个可写主库，当前识别到 %d 个", len(primaries)))
	}
	if len(replicas) == 0 {
		plan.Executable = false
		plan.BlockingReasons = append(plan.BlockingReasons, "不停机滚动升级至少需要一个健康从库")
	}
	if len(primaries) == 1 {
		plan.OriginalPrimaryMachineID = primaries[0].MachineID
	}
	if len(primaries) == 1 && !isMGR {
		primaryProbe := probes[primaries[0].MachineID]
		for _, replica := range replicas {
			probe := probes[replica.MachineID]
			sourceMatches := strings.EqualFold(strings.TrimSpace(probe.SourceHost), strings.TrimSpace(primaries[0].IP)) ||
				strings.EqualFold(strings.TrimSpace(probe.SourceHost), strings.TrimSpace(primaryProbe.Hostname)) ||
				strings.EqualFold(strings.TrimSpace(probe.SourceHost), strings.TrimSpace(primaries[0].Machine))
			if !sourceMatches || (probe.SourcePort > 0 && probe.SourcePort != primaries[0].Port) {
				plan.Executable = false
				plan.BlockingReasons = append(plan.BlockingReasons, fmt.Sprintf("从库 %s 的复制源 %s:%d 不是当前主库 %s:%d", replica.Machine, probe.SourceHost, probe.SourcePort, primaries[0].IP, primaries[0].Port))
			}
			if probe.ChannelCount != 1 || !strings.EqualFold(probe.ReceiverState, "ON") || !strings.EqualFold(probe.ApplierState, "ON") {
				plan.Executable = false
				plan.BlockingReasons = append(plan.BlockingReasons, fmt.Sprintf("从库 %s 复制线程不健康（通道=%d，接收=%s，应用=%s）", replica.Machine, probe.ChannelCount, probe.ReceiverState, probe.ApplierState))
			}
			if probe.DelaySeconds > 0 {
				plan.Warnings = append(plan.Warnings, fmt.Sprintf("从库 %s 是延时从库（%d 秒），不会被选为临时主库", replica.Machine, probe.DelaySeconds))
			}
		}
	}
	sort.Slice(replicas, func(i, j int) bool {
		if replicas[i].DelaySeconds != replicas[j].DelaySeconds {
			return replicas[i].DelaySeconds < replicas[j].DelaySeconds
		}
		return replicas[i].Machine < replicas[j].Machine
	})
	for _, replica := range replicas {
		if isMGR || replica.DelaySeconds == 0 {
			plan.TemporaryPrimaryMachineID = replica.MachineID
			break
		}
	}
	if plan.TemporaryPrimaryMachineID == "" {
		plan.Executable = false
		plan.BlockingReasons = append(plan.BlockingReasons, "没有可提升的非延时从库")
	}
	if isMGR {
		plan.Warnings = append(plan.Warnings,
			"升级期间 SECONDARY 会逐台重启并自动重新加入复制组，任何时刻必须保有多数派",
			"PRIMARY 切换由 Group Replication 完成，MySQL Router 会根据元数据将新连接路由到新主节点",
			"任一步骤失败都会停止后续变更；成员未恢复 ONLINE 时不会继续升级下一节点",
		)
	} else {
		vips, vipErr := s.ha.ListVIPConfigs(ctx, req.Cluster)
		if vipErr != nil {
			plan.Executable = false
			plan.BlockingReasons = append(plan.BlockingReasons, "无法读取集群 VIP 配置: "+vipErr.Error())
		} else {
			enabledVIPs := 0
			enabledAddresses := make(map[string]struct{})
			for _, vip := range vips {
				if vip.Enabled {
					enabledVIPs++
					enabledAddresses[vip.VIPAddress] = struct{}{}
				}
			}
			if enabledVIPs == 0 {
				plan.Executable = false
				plan.BlockingReasons = append(plan.BlockingReasons, "不停机滚动升级要求已启用的集群 VIP，以便两次切主时同步迁移业务入口")
			} else if plan.OriginalPrimaryMachineID != "" {
				states, scanErr := s.ha.VIP().Scan(ctx, req.Cluster)
				if scanErr != nil {
					plan.Executable = false
					plan.BlockingReasons = append(plan.BlockingReasons, "无法确认当前 VIP 持有者: "+scanErr.Error())
				} else {
					verified := make(map[string]bool, len(states))
					for _, state := range states {
						if _, enabled := enabledAddresses[state.VIPAddress]; !enabled {
							continue
						}
						verified[state.VIPAddress] = state.VIPStatus == hadomain.VipStatusBound && state.CurrentHolderMachineID == plan.OriginalPrimaryMachineID
					}
					for address := range enabledAddresses {
						if !verified[address] {
							plan.Executable = false
							plan.BlockingReasons = append(plan.BlockingReasons, fmt.Sprintf("VIP %s 当前未唯一绑定在原主库；请先修复业务入口再执行不停机升级", address))
						}
					}
				}
			}
		}
		plan.Warnings = append(plan.Warnings,
			"升级期间各从库会依次重启，但始终保留一个可写主库",
			"两次主从切换会冻结旧主写入并迁移 VIP，客户端需要具备断线重连能力",
			"任一步骤失败都会停止后续变更；不会在复制未追平时强制切主",
		)
	}
	return plan, nil
}

func (s *ClusterUpgradeService) probeNode(ctx context.Context, machine machinedomain.Machine, instance mysqlapp.Instance, parentTaskID string) (clusterUpgradeProbe, error) {
	client := upgradeShellQuote(instance.BaseDir+"/bin/mysql") + " --defaults-extra-file=" + mysqlDefaultsFilePlaceholder + fmt.Sprintf(" --protocol=tcp --host=127.0.0.1 --port=%d --batch --raw --skip-column-names", instance.Port)
	sql := "SELECT CONCAT('GMHA_CLUSTER_UPGRADE_NODE\\t',@@hostname,'\\t',@@version,'\\t',@@server_id,'\\t',@@read_only,'\\t',@@super_read_only,'\\t',@@global.gtid_mode,'\\t'," +
		"COALESCE((SELECT HOST FROM performance_schema.replication_connection_configuration ORDER BY CHANNEL_NAME LIMIT 1),''),'\\t'," +
		"COALESCE((SELECT PORT FROM performance_schema.replication_connection_configuration ORDER BY CHANNEL_NAME LIMIT 1),0),'\\t'," +
		"COALESCE((SELECT SERVICE_STATE FROM performance_schema.replication_connection_status ORDER BY CHANNEL_NAME LIMIT 1),''),'\\t'," +
		"COALESCE((SELECT SERVICE_STATE FROM performance_schema.replication_applier_status ORDER BY CHANNEL_NAME LIMIT 1),''),'\\t'," +
		"(SELECT COUNT(*) FROM performance_schema.replication_connection_configuration),'\\t'," +
		"COALESCE((SELECT DESIRED_DELAY FROM performance_schema.replication_applier_configuration ORDER BY CHANNEL_NAME LIMIT 1),0),'\\t',@@offline_mode);"
	mgrSQL := "SELECT CONCAT('GMHA_CLUSTER_UPGRADE_MGR\\t',@@server_uuid,'\\t'," +
		"@@group_replication_group_name,'\\t'," +
		"COALESCE((SELECT MEMBER_STATE FROM performance_schema.replication_group_members WHERE MEMBER_ID=@@server_uuid LIMIT 1),''),'\\t'," +
		"COALESCE((SELECT MEMBER_ROLE FROM performance_schema.replication_group_members WHERE MEMBER_ID=@@server_uuid LIMIT 1),''),'\\t'," +
		"(SELECT COUNT(*) FROM performance_schema.replication_group_members),'\\t'," +
		"(SELECT COUNT(*) FROM performance_schema.replication_group_members WHERE MEMBER_STATE='ONLINE'),'\\t'," +
		"(SELECT COUNT(*) FROM performance_schema.replication_group_members WHERE MEMBER_ROLE='PRIMARY'),'\\t'," +
		"COALESCE((SELECT GROUP_CONCAT(MEMBER_ID ORDER BY MEMBER_ID SEPARATOR ',') FROM performance_schema.replication_group_members),''),'\\t'," +
		"COALESCE((SELECT SUM(COUNT_TRANSACTIONS_IN_QUEUE+COUNT_TRANSACTIONS_REMOTE_IN_APPLIER_QUEUE) FROM performance_schema.replication_group_member_stats),0));"
	command := "{ " + client + " --execute=" + upgradeShellQuote(sql) + "; } && { " + client + " --execute=" + upgradeShellQuote(mgrSQL) + " 2>/dev/null || true; }"
	detail, err := s.tasks.CreateExecTaskWithOptions(ctx, machine.IP, command, ExecTaskOptions{
		ParentTaskID: parentTaskID, Operation: "mysql_cluster_upgrade_probe", DisplayName: "探测集群升级拓扑 " + machine.Name, StepName: "读取实时版本与复制状态", Port: instance.Port,
	})
	if err != nil {
		return clusterUpgradeProbe{}, err
	}
	completed, err := s.tasks.WaitForTask(ctx, detail.Task.ID, 45*time.Second)
	if err != nil {
		return clusterUpgradeProbe{TaskID: detail.Task.ID}, err
	}
	if completed.Task.Status != taskdomain.StatusSuccess {
		return clusterUpgradeProbe{TaskID: detail.Task.ID}, fmt.Errorf("probe task failed: %s", emptyTaskError(completed))
	}
	marker, mgrMarker := "", ""
	for _, event := range completed.Events {
		if index := strings.Index(event.Content, "GMHA_CLUSTER_UPGRADE_NODE\t"); index >= 0 {
			marker = strings.SplitN(event.Content[index:], "\n", 2)[0]
		}
		if index := strings.Index(event.Content, "GMHA_CLUSTER_UPGRADE_MGR\t"); index >= 0 {
			mgrMarker = strings.SplitN(event.Content[index:], "\n", 2)[0]
		}
	}
	for _, step := range completed.Steps {
		if index := strings.Index(step.Message, "GMHA_CLUSTER_UPGRADE_NODE\t"); index >= 0 {
			marker = strings.SplitN(step.Message[index:], "\n", 2)[0]
		}
		if index := strings.Index(step.Message, "GMHA_CLUSTER_UPGRADE_MGR\t"); index >= 0 {
			mgrMarker = strings.SplitN(step.Message[index:], "\n", 2)[0]
		}
	}
	parts := strings.Split(strings.TrimSpace(marker), "\t")
	if len(parts) < 13 {
		return clusterUpgradeProbe{TaskID: detail.Task.ID}, errors.New("live topology probe returned malformed output")
	}
	serverID, _ := strconv.Atoi(parts[3])
	sourcePort, _ := strconv.Atoi(parts[8])
	channels, _ := strconv.Atoi(parts[11])
	delay, _ := strconv.Atoi(parts[12])
	probe := clusterUpgradeProbe{
		MachineID: machine.ID, Hostname: parts[1], Version: parts[2], ServerID: serverID,
		ReadOnly: mysqlBool(parts[4]), SuperReadOnly: mysqlBool(parts[5]), GTIDMode: parts[6],
		SourceHost: parts[7], SourcePort: sourcePort, ReceiverState: parts[9], ApplierState: parts[10],
		ChannelCount: channels, DelaySeconds: delay, TaskID: detail.Task.ID,
	}
	if len(parts) >= 14 {
		probe.OfflineMode = mysqlBool(parts[13])
	}
	mgrParts := strings.Split(strings.TrimSpace(mgrMarker), "\t")
	if len(mgrParts) >= 10 {
		probe.ServerUUID, probe.GroupName, probe.GroupState, probe.GroupRole = mgrParts[1], mgrParts[2], mgrParts[3], mgrParts[4]
		probe.GroupMembers, _ = strconv.Atoi(mgrParts[5])
		probe.GroupOnline, _ = strconv.Atoi(mgrParts[6])
		probe.GroupPrimary, _ = strconv.Atoi(mgrParts[7])
		probe.GroupView = mgrParts[8]
		probe.GroupQueue, _ = strconv.Atoi(mgrParts[9])
	}
	return probe, nil
}

func mysqlBool(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	return value == "1" || value == "on" || value == "true" || value == "yes"
}

func (s *ClusterUpgradeService) createTrackingTask(ctx context.Context, run ClusterUpgradeRun) error {
	spec, err := json.Marshal(clusterUpgradeTaskSpec{Operation: "mysql_cluster_rolling_upgrade", DisplayName: "MySQL 集群不停机滚动升级", Cluster: run.Cluster, Run: run})
	if err != nil {
		return err
	}
	task := taskdomain.Task{
		ID: run.RunID, Type: taskdomain.TypeMySQLClusterUpgrade, MachineID: run.Cluster, AgentID: "manager",
		Status: taskdomain.StatusPending, CurrentStep: "waiting_start", SpecJSON: spec, CreatedAt: run.CreatedAt,
	}
	steps := make([]taskdomain.Step, 0, len(run.Stages))
	for index, stage := range run.Stages {
		steps = append(steps, taskdomain.Step{
			ID: run.RunID + "-" + stage.Code, TaskID: run.RunID, StepNo: index + 1,
			StepName: stage.Code, Status: taskdomain.StepPending, Message: stage.Name,
		})
	}
	architectureLabel := "传统主从"
	if isMGRClusterUpgrade(run) {
		architectureLabel = "MGR + MySQL Router"
	}
	events := []taskdomain.Event{{
		ID: run.RunID + "-created", TaskID: run.RunID, EventType: taskdomain.EventInfo,
		Content:   fmt.Sprintf("已创建集群 %s 的 MySQL %s 不停机滚动升级任务（%s）；固定执行从节点升级、切主、原主升级、切回。", run.Cluster, run.TargetVersion, architectureLabel),
		CreatedAt: run.CreatedAt,
	}}
	return s.tasks.repo.CreateTask(ctx, task, steps, events)
}

func (s *ClusterUpgradeService) syncRun(ctx context.Context, run ClusterUpgradeRun) error {
	task, ok, err := s.tasks.repo.GetTask(ctx, run.RunID)
	if err != nil || !ok {
		if err != nil {
			return err
		}
		return errors.New("cluster upgrade tracking task not found")
	}
	spec, err := json.Marshal(clusterUpgradeTaskSpec{Operation: "mysql_cluster_rolling_upgrade", DisplayName: "MySQL 集群不停机滚动升级", Cluster: run.Cluster, Run: run})
	if err != nil {
		return err
	}
	task.SpecJSON, task.CurrentStep = spec, run.CurrentStage
	switch run.Status {
	case clusterUpgradeSucceeded:
		task.Status, task.ProgressPercent, task.FinishedAt = taskdomain.StatusSuccess, 100, run.FinishedAt
	case clusterUpgradeFailed:
		task.Status, task.FinishedAt = taskdomain.StatusFailed, run.FinishedAt
	case clusterUpgradeRunning:
		task.Status = taskdomain.StatusRunning
		if task.StartedAt == nil {
			started := run.UpdatedAt
			task.StartedAt = &started
		}
	default:
		task.Status = taskdomain.StatusPending
	}
	completed := 0
	for _, stage := range run.Stages {
		if stage.Status == "success" || stage.Status == "failed" {
			completed++
		}
	}
	if run.Status != clusterUpgradeSucceeded {
		task.ProgressPercent = completed * 100 / len(run.Stages)
	}
	if err := s.tasks.repo.UpdateTask(ctx, task); err != nil {
		return err
	}
	steps, err := s.tasks.repo.ListSteps(ctx, run.RunID)
	if err != nil {
		return err
	}
	stageByCode := make(map[string]ClusterUpgradeStage, len(run.Stages))
	for _, stage := range run.Stages {
		stageByCode[stage.Code] = stage
	}
	for _, step := range steps {
		stage := stageByCode[step.StepName]
		step.StartedAt, step.FinishedAt = stage.StartedAt, stage.FinishedAt
		step.Message = stage.Name
		if stage.Message != "" {
			step.Message += "：" + stage.Message
		}
		switch stage.Status {
		case "running":
			step.Status = taskdomain.StepRunning
		case "success":
			step.Status = taskdomain.StepSuccess
		case "failed":
			step.Status = taskdomain.StepFailed
		default:
			step.Status = taskdomain.StepPending
		}
		if err := s.tasks.repo.UpdateStep(ctx, step); err != nil {
			return err
		}
	}
	return nil
}

func (s *ClusterUpgradeService) appendRunEvent(ctx context.Context, run ClusterUpgradeRun, eventType taskdomain.EventType, content string) {
	_ = s.tasks.repo.AppendEvent(ctx, taskdomain.Event{
		ID: fmt.Sprintf("%s-event-%d", run.RunID, time.Now().UTC().UnixNano()), TaskID: run.RunID,
		StepID: run.RunID + "-" + run.CurrentStage, EventType: eventType, Content: content, CreatedAt: time.Now().UTC(),
	})
}

func (s *ClusterUpgradeService) execute(ctx context.Context, run ClusterUpgradeRun) {
	defer s.clearActive(run)
	const lockTTL = 10 * time.Minute
	if err := s.ha.repo.AcquireFailoverLock(ctx, run.Cluster, run.RunID, "gmha-cluster-upgrade", lockTTL); err != nil {
		s.failRun(&run, "cluster_preflight", fmt.Errorf("无法获取集群维护锁: %w", err))
		return
	}
	lockReleased := false
	releaseLock := func() error {
		if lockReleased {
			return nil
		}
		if err := s.ha.repo.ReleaseFailoverLock(context.Background(), run.Cluster, run.RunID); err != nil {
			return err
		}
		lockReleased = true
		return nil
	}
	defer func() { _ = releaseLock() }()
	executionCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	lockErrors := make(chan error, 1)
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-executionCtx.Done():
				return
			case <-ticker.C:
				if err := s.ha.repo.RenewFailoverLock(context.Background(), run.Cluster, run.RunID, lockTTL); err != nil {
					select {
					case lockErrors <- err:
					default:
					}
					cancel()
					return
				}
			}
		}
	}()
	run.Status, run.UpdatedAt = clusterUpgradeRunning, time.Now().UTC()
	_ = s.syncRun(context.Background(), run)

	stages := []struct {
		code string
		run  func(context.Context, *ClusterUpgradeRun) (string, error)
	}{
		{"cluster_preflight", s.executeClusterPreflight},
		{"precheck_all_nodes", s.executeAllPrechecks},
		{"upgrade_replicas", s.executeReplicaUpgrades},
		{"verify_replicas", s.verifyUpgradedReplicas},
		{"switch_to_upgraded_replica", s.switchToTemporaryPrimary},
		{"upgrade_original_primary", s.upgradeOriginalPrimary},
		{"verify_original_primary", s.verifyOriginalPrimary},
		{"switch_back_original_primary", s.switchBackOriginalPrimary},
		{"final_cluster_verify", s.finalClusterVerify},
		{"release_maintenance_lock", func(_ context.Context, _ *ClusterUpgradeRun) (string, error) {
			return "集群维护锁已释放", releaseLock()
		}},
	}
	for _, stage := range stages {
		if index := clusterUpgradeStageIndex(run.Stages, stage.code); index >= 0 && run.Stages[index].Status == "success" {
			continue
		}
		select {
		case lockErr := <-lockErrors:
			s.failRun(&run, stage.code, fmt.Errorf("集群维护锁续租失败: %w", lockErr))
			return
		default:
		}
		if err := s.runStage(executionCtx, &run, stage.code, stage.run); err != nil {
			return
		}
	}
	now := time.Now().UTC()
	run.Status, run.CurrentStage, run.Error = clusterUpgradeSucceeded, "release_maintenance_lock", ""
	run.UpdatedAt, run.FinishedAt = now, &now
	_ = s.syncRun(context.Background(), run)
	if isMGRClusterUpgrade(run) {
		s.appendRunEvent(context.Background(), run, taskdomain.EventInfo, fmt.Sprintf("集群滚动升级完成：全部成员已运行 MySQL %s，原成员已恢复为 MGR PRIMARY，Router 业务入口保持可用。", run.TargetVersion))
	} else {
		s.appendRunEvent(context.Background(), run, taskdomain.EventInfo, fmt.Sprintf("集群滚动升级完成：全部节点已运行 MySQL %s，原主库已恢复为唯一写主，VIP 已迁回。", run.TargetVersion))
	}
}

func (s *ClusterUpgradeService) runStage(ctx context.Context, run *ClusterUpgradeRun, code string, execute func(context.Context, *ClusterUpgradeRun) (string, error)) error {
	index := clusterUpgradeStageIndex(run.Stages, code)
	if index < 0 {
		return fmt.Errorf("unknown cluster upgrade stage %s", code)
	}
	now := time.Now().UTC()
	run.CurrentStage, run.Status, run.UpdatedAt = code, clusterUpgradeRunning, now
	run.Stages[index].Status, run.Stages[index].StartedAt, run.Stages[index].FinishedAt = "running", &now, nil
	_ = s.syncRun(context.Background(), *run)
	s.appendRunEvent(context.Background(), *run, taskdomain.EventInfo, "开始："+run.Stages[index].Name)
	message, err := execute(ctx, run)
	finished := time.Now().UTC()
	run.UpdatedAt = finished
	run.Stages[index].FinishedAt, run.Stages[index].Message = &finished, message
	if err != nil {
		run.Stages[index].Status, run.Status, run.Error, run.FinishedAt = "failed", clusterUpgradeFailed, err.Error(), &finished
		if message == "" {
			run.Stages[index].Message = err.Error()
		}
		_ = s.syncRun(context.Background(), *run)
		s.appendRunEvent(context.Background(), *run, taskdomain.EventError, fmt.Sprintf("%s失败：%v", run.Stages[index].Name, err))
		return err
	}
	run.Stages[index].Status = "success"
	_ = s.syncRun(context.Background(), *run)
	s.appendRunEvent(context.Background(), *run, taskdomain.EventInfo, "完成："+run.Stages[index].Name+"。"+message)
	return nil
}

func (s *ClusterUpgradeService) failRun(run *ClusterUpgradeRun, stage string, err error) {
	now := time.Now().UTC()
	run.Status, run.CurrentStage, run.Error = clusterUpgradeFailed, stage, err.Error()
	run.UpdatedAt, run.FinishedAt = now, &now
	if index := clusterUpgradeStageIndex(run.Stages, stage); index >= 0 {
		run.Stages[index].Status, run.Stages[index].Message, run.Stages[index].FinishedAt = "failed", err.Error(), &now
		if run.Stages[index].StartedAt == nil {
			run.Stages[index].StartedAt = &now
		}
	}
	_ = s.syncRun(context.Background(), *run)
	s.appendRunEvent(context.Background(), *run, taskdomain.EventError, err.Error())
}

func (s *ClusterUpgradeService) clearActive(run ClusterUpgradeRun) {
	s.mu.Lock()
	if s.active[run.Cluster] == run.RunID {
		delete(s.active, run.Cluster)
	}
	s.mu.Unlock()
}

func clusterUpgradeStageIndex(stages []ClusterUpgradeStage, code string) int {
	for index := range stages {
		if stages[index].Code == code {
			return index
		}
	}
	return -1
}

func (s *ClusterUpgradeService) executeClusterPreflight(ctx context.Context, run *ClusterUpgradeRun) (string, error) {
	plan, err := s.buildPlan(ctx, ClusterUpgradeRequest{Cluster: run.Cluster, TargetVersion: run.TargetVersion, Port: run.Port, RiskAcknowledged: true}, run.RunID)
	if err != nil {
		return "", err
	}
	if !plan.Executable {
		return "", errors.New(strings.Join(plan.BlockingReasons, "；"))
	}
	if plan.Architecture != run.Architecture || plan.OriginalPrimaryMachineID != run.OriginalPrimaryMachineID || plan.TemporaryPrimaryMachineID != run.TemporaryPrimaryMachineID {
		return "", errors.New("启动后集群主从角色发生变化，已停止滚动升级，请重新生成计划")
	}
	run.Nodes = mergeClusterUpgradeTasks(plan.Nodes, run.Nodes)
	if isMGRClusterUpgrade(*run) {
		return fmt.Sprintf("确认 %d 个 MGR 成员全部 ONLINE、单 PRIMARY、具有多数派并匹配兼容制品", len(run.Nodes)), nil
	}
	return fmt.Sprintf("确认 1 个主库、%d 个健康从库、可迁移 VIP 和全节点兼容制品", len(run.Nodes)-1), nil
}

func mergeClusterUpgradeTasks(fresh, previous []ClusterUpgradeNode) []ClusterUpgradeNode {
	old := make(map[string]ClusterUpgradeNode, len(previous))
	for _, node := range previous {
		old[node.MachineID] = node
	}
	for index := range fresh {
		if item, ok := old[fresh[index].MachineID]; ok {
			fresh[index].PrecheckTaskID = item.PrecheckTaskID
			fresh[index].UpgradeTaskID = item.UpgradeTaskID
		}
	}
	return fresh
}

func (s *ClusterUpgradeService) executeAllPrechecks(ctx context.Context, run *ClusterUpgradeRun) (string, error) {
	for index := range run.Nodes {
		node := &run.Nodes[index]
		if node.PrecheckTaskID != "" {
			continue
		}
		plan, err := s.tasks.CreateMySQLUpgradePrecheck(ctx, MySQLUpgradeRequest{
			Machine: node.MachineID, Port: node.Port, PackageName: node.PackageName, ParentTaskID: run.RunID,
		})
		if err != nil {
			node.Status, node.Error = "failed", err.Error()
			return "", fmt.Errorf("节点 %s 创建升级预检失败: %w", node.Machine, err)
		}
		node.PrecheckTaskID, node.Status = plan.Task.Task.ID, "prechecking"
		run.UpdatedAt = time.Now().UTC()
		_ = s.syncRun(context.Background(), *run)
	}
	for index := range run.Nodes {
		node := &run.Nodes[index]
		completed, err := s.tasks.WaitForTask(ctx, node.PrecheckTaskID, 30*time.Minute)
		if err != nil || completed.Task.Status != taskdomain.StatusSuccess {
			if err == nil {
				err = errors.New(emptyTaskError(completed))
			}
			node.Status, node.Error = "failed", err.Error()
			return "", fmt.Errorf("节点 %s 升级预检未通过: %w", node.Machine, err)
		}
		node.Status, node.Error = "precheck_passed", ""
		run.UpdatedAt = time.Now().UTC()
		_ = s.syncRun(context.Background(), *run)
	}
	return fmt.Sprintf("%d 个节点的官方升级检查、配置与磁盘预检全部通过", len(run.Nodes)), nil
}

func (s *ClusterUpgradeService) executeReplicaUpgrades(ctx context.Context, run *ClusterUpgradeRun) (string, error) {
	replicas := clusterUpgradeReplicaOrder(*run)
	for _, machineID := range replicas {
		if isMGRClusterUpgrade(*run) {
			if err := s.leaveMGRForUpgrade(ctx, run, machineID); err != nil {
				return "", err
			}
		}
		if err := s.upgradeOneNode(ctx, run, machineID); err != nil {
			return "", err
		}
		if isMGRClusterUpgrade(*run) {
			if err := s.rejoinMGRMember(ctx, run, machineID); err != nil {
				return "", err
			}
			if err := s.waitMGRMemberOnline(ctx, run, machineID, len(run.Nodes)); err != nil {
				return "", err
			}
			if err := s.persistMGRStartOnBoot(ctx, run, machineID); err != nil {
				return "", err
			}
			if err := s.verifyMGRNode(ctx, run, machineID, "SECONDARY", true); err != nil {
				return "", err
			}
		} else {
			if err := s.verifyReplicaNode(ctx, run, machineID, run.OriginalPrimaryMachineID, true); err != nil {
				return "", err
			}
		}
	}
	if isMGRClusterUpgrade(*run) {
		return fmt.Sprintf("%d 个 SECONDARY 已逐台升级并重新加入 MGR，每次仅重启一个成员", len(replicas)), nil
	}
	return fmt.Sprintf("%d 个从库已逐台升级，每次仅重启一个从库", len(replicas)), nil
}

func clusterUpgradeReplicaOrder(run ClusterUpgradeRun) []string {
	replicas := make([]string, 0, len(run.Nodes)-1)
	for _, node := range run.Nodes {
		if node.MachineID != run.OriginalPrimaryMachineID {
			replicas = append(replicas, node.MachineID)
		}
	}
	sort.SliceStable(replicas, func(i, j int) bool {
		if replicas[i] == run.TemporaryPrimaryMachineID {
			return false
		}
		if replicas[j] == run.TemporaryPrimaryMachineID {
			return true
		}
		return replicas[i] < replicas[j]
	})
	return replicas
}

func (s *ClusterUpgradeService) upgradeOneNode(ctx context.Context, run *ClusterUpgradeRun, machineID string) error {
	node := clusterUpgradeNode(run, machineID)
	if node == nil {
		return fmt.Errorf("upgrade node %s not found", machineID)
	}
	if node.UpgradeTaskID == "" {
		plan, err := s.tasks.CreateMySQLUpgradeTask(ctx, MySQLUpgradeRequest{
			Machine: node.MachineID, Port: node.Port, PackageName: node.PackageName,
			PrecheckTaskID: node.PrecheckTaskID, RiskAcknowledged: true, ParentTaskID: run.RunID,
		})
		if err != nil {
			node.Status, node.Error = "failed", err.Error()
			return fmt.Errorf("节点 %s 创建升级任务失败: %w", node.Machine, err)
		}
		node.UpgradeTaskID, node.Status = plan.Task.Task.ID, "upgrading"
		run.UpdatedAt = time.Now().UTC()
		_ = s.syncRun(context.Background(), *run)
	}
	completed, err := s.tasks.WaitForTask(ctx, node.UpgradeTaskID, 2*time.Hour)
	if err != nil || completed.Task.Status != taskdomain.StatusSuccess {
		if err == nil {
			err = errors.New(emptyTaskError(completed))
		}
		node.Status, node.Error = "failed", err.Error()
		return fmt.Errorf("节点 %s 升级失败: %w", node.Machine, err)
	}
	node.Status, node.Error, node.CurrentVersion = "upgraded", "", run.TargetVersion
	run.UpdatedAt = time.Now().UTC()
	_ = s.syncRun(context.Background(), *run)
	return nil
}

func (s *ClusterUpgradeService) verifyUpgradedReplicas(ctx context.Context, run *ClusterUpgradeRun) (string, error) {
	for _, node := range run.Nodes {
		if node.MachineID == run.OriginalPrimaryMachineID {
			continue
		}
		if isMGRClusterUpgrade(*run) {
			if err := s.verifyMGRNode(ctx, run, node.MachineID, "SECONDARY", true); err != nil {
				return "", err
			}
		} else {
			if err := s.verifyReplicaNode(ctx, run, node.MachineID, run.OriginalPrimaryMachineID, true); err != nil {
				return "", err
			}
		}
	}
	if isMGRClusterUpgrade(*run) {
		return "全部升级后 SECONDARY 均已恢复 ONLINE，复制组保持多数派", nil
	}
	return "全部升级后从库均为只读，复制线程正常且仍跟随原主库", nil
}

func (s *ClusterUpgradeService) switchToTemporaryPrimary(ctx context.Context, run *ClusterUpgradeRun) (string, error) {
	if isMGRClusterUpgrade(*run) {
		if err := s.leaveMGRForUpgrade(ctx, run, run.OriginalPrimaryMachineID); err != nil {
			return "", err
		}
		if err := s.setMGRPrimary(ctx, run, run.TemporaryPrimaryMachineID, run.OriginalPrimaryMachineID); err != nil {
			_ = s.rejoinMGRMember(context.Background(), run, run.OriginalPrimaryMachineID)
			_ = s.waitMGRMemberOnline(context.Background(), run, run.OriginalPrimaryMachineID, len(run.Nodes))
			_ = s.persistMGRStartOnBoot(context.Background(), run, run.OriginalPrimaryMachineID)
			return "", err
		}
		return fmt.Sprintf("MGR PRIMARY 已切换到升级后的成员 %s；Router 将新连接路由到新主节点", clusterUpgradeNodeName(run, run.TemporaryPrimaryMachineID)), nil
	}
	if err := s.executeSwitchover(ctx, run, run.OriginalPrimaryMachineID, run.TemporaryPrimaryMachineID, true); err != nil {
		return "", err
	}
	return fmt.Sprintf("VIP 与写入口已迁移到临时主库 %s；低版本原主库保持只读隔离", clusterUpgradeNodeName(run, run.TemporaryPrimaryMachineID)), nil
}

func (s *ClusterUpgradeService) upgradeOriginalPrimary(ctx context.Context, run *ClusterUpgradeRun) (string, error) {
	if isMGRClusterUpgrade(*run) {
		if err := s.upgradeOneNode(ctx, run, run.OriginalPrimaryMachineID); err != nil {
			return "", err
		}
		if err := s.rejoinMGRMember(ctx, run, run.OriginalPrimaryMachineID); err != nil {
			return "", err
		}
		if err := s.waitMGRMemberOnline(ctx, run, run.OriginalPrimaryMachineID, len(run.Nodes)); err != nil {
			return "", err
		}
		if err := s.persistMGRStartOnBoot(ctx, run, run.OriginalPrimaryMachineID); err != nil {
			return "", err
		}
		if err := s.verifyMGRNode(ctx, run, run.OriginalPrimaryMachineID, "SECONDARY", true); err != nil {
			return "", err
		}
		return "原 PRIMARY 已在 SECONDARY 状态完成升级并自动重新加入复制组", nil
	}
	if err := s.verifyDetachedOriginalPrimary(ctx, run); err != nil {
		return "", fmt.Errorf("原主库隔离状态不满足升级条件: %w", err)
	}
	if err := s.upgradeOneNode(ctx, run, run.OriginalPrimaryMachineID); err != nil {
		return "", err
	}
	if err := s.attachOriginalPrimaryAsReplica(ctx, run); err != nil {
		return "", fmt.Errorf("原主库升级完成，但重新接入复制失败: %w", err)
	}
	return "原主库已在不承载业务写入时完成升级，并以同版本从库重新接入", nil
}

func (s *ClusterUpgradeService) verifyOriginalPrimary(ctx context.Context, run *ClusterUpgradeRun) (string, error) {
	if isMGRClusterUpgrade(*run) {
		if err := s.verifyMGRNode(ctx, run, run.OriginalPrimaryMachineID, "SECONDARY", true); err != nil {
			return "", err
		}
		return "原 PRIMARY 已升级到目标版本并作为 ONLINE SECONDARY 追平复制组", nil
	}
	if err := s.verifyReplicaNode(ctx, run, run.OriginalPrimaryMachineID, run.TemporaryPrimaryMachineID, true); err != nil {
		return "", err
	}
	return "原主库已升级到目标版本并追平临时主库", nil
}

func (s *ClusterUpgradeService) verifyDetachedOriginalPrimary(ctx context.Context, run *ClusterUpgradeRun) error {
	node := clusterUpgradeNode(run, run.OriginalPrimaryMachineID)
	if node == nil {
		return errors.New("original primary node not found")
	}
	machine, instance, err := s.tasks.ResolveMySQLInstance(ctx, node.MachineID, node.Port)
	if err != nil {
		return err
	}
	probe, err := s.probeNode(ctx, machine, instance, run.RunID)
	if err != nil {
		return err
	}
	if !probe.ReadOnly || !probe.SuperReadOnly || probe.SourceHost != "" || probe.ChannelCount != 0 {
		return fmt.Errorf("节点 %s 必须保持无复制源的只读隔离状态（源=%s，通道=%d，只读=%t/%t）", node.Machine, probe.SourceHost, probe.ChannelCount, probe.ReadOnly, probe.SuperReadOnly)
	}
	node.Role, node.SourceHost, node.SourcePort = "detached", "", 0
	node.ReadOnly, node.SuperReadOnly = probe.ReadOnly, probe.SuperReadOnly
	return nil
}

func (s *ClusterUpgradeService) attachOriginalPrimaryAsReplica(ctx context.Context, run *ClusterUpgradeRun) error {
	temporary := clusterUpgradeNode(run, run.TemporaryPrimaryMachineID)
	original := clusterUpgradeNode(run, run.OriginalPrimaryMachineID)
	if temporary == nil || original == nil {
		return errors.New("temporary or original primary node not found")
	}
	replicationUser, replicationPassword := s.ha.architectureManagementAccount(ctx)
	req := hadomain.ArchitectureAdjustmentRequest{
		Architecture:                hadomain.ArchitectureMasterSlave,
		CurrentArchitecture:         hadomain.ArchitectureMasterSlave,
		CurrentMasterMachineID:      temporary.MachineID,
		PreferredNewMasterMachineID: temporary.MachineID,
		ReplicationUser:             replicationUser,
		ReplicationPassword:         replicationPassword,
		Nodes: []hadomain.ArchitectureNodeRequest{
			{MachineID: temporary.MachineID, Port: temporary.Port, Role: "M", ElectionPriority: 1000},
			{MachineID: original.MachineID, Port: original.Port, Role: "S", SourceMachineID: temporary.MachineID},
		},
	}
	machines, err := s.ha.architectureMachines(ctx, req)
	if err != nil {
		return err
	}
	taskIDs, err := s.ha.configureArchitectureTopology(ctx, req, temporary.MachineID, machines)
	_ = s.tasks.AttachChildTasks(context.Background(), run.RunID, taskIDs)
	if err != nil {
		return err
	}
	verifiedIDs, err := s.ha.verifyArchitectureDataWithPT(ctx, req, temporary.MachineID, machines)
	_ = s.tasks.AttachChildTasks(context.Background(), run.RunID, verifiedIDs)
	if err != nil {
		return err
	}
	resumeReq := req
	resumeReq.Nodes = []hadomain.ArchitectureNodeRequest{req.Nodes[1]}
	resumeIDs, err := s.ha.resumeArchitectureBusinessConnections(ctx, resumeReq, machines)
	_ = s.tasks.AttachChildTasks(context.Background(), run.RunID, resumeIDs)
	return err
}

func (s *ClusterUpgradeService) switchBackOriginalPrimary(ctx context.Context, run *ClusterUpgradeRun) (string, error) {
	if isMGRClusterUpgrade(*run) {
		if err := s.setMGRPrimary(ctx, run, run.OriginalPrimaryMachineID, ""); err != nil {
			return "", err
		}
		return fmt.Sprintf("MGR PRIMARY 已切回原成员 %s，Router 元数据保持有效", clusterUpgradeNodeName(run, run.OriginalPrimaryMachineID)), nil
	}
	if err := s.executeSwitchover(ctx, run, run.TemporaryPrimaryMachineID, run.OriginalPrimaryMachineID, false); err != nil {
		return "", err
	}
	return fmt.Sprintf("VIP 已迁回原主库 %s", clusterUpgradeNodeName(run, run.OriginalPrimaryMachineID)), nil
}

func (s *ClusterUpgradeService) finalClusterVerify(ctx context.Context, run *ClusterUpgradeRun) (string, error) {
	if isMGRClusterUpgrade(*run) {
		if err := s.executeMGRControlSQL(
			ctx, run, run.OriginalPrimaryMachineID, "mysql_cluster_upgrade_mgr_protocol",
			"更新 MGR 通信协议版本",
			"SELECT group_replication_set_communication_protocol("+upgradeShellQuote(run.TargetVersion)+");",
			time.Minute,
		); err != nil {
			return "", err
		}
		for _, node := range run.Nodes {
			role := "SECONDARY"
			if node.MachineID == run.OriginalPrimaryMachineID {
				role = "PRIMARY"
			}
			if err := s.verifyMGRNode(ctx, run, node.MachineID, role, true); err != nil {
				return "", err
			}
		}
		return fmt.Sprintf("全部 %d 个成员均为 MySQL %s 且 ONLINE，复制组保持单 PRIMARY 和多数派", len(run.Nodes), run.TargetVersion), nil
	}
	if err := s.verifyPrimaryNode(ctx, run, run.OriginalPrimaryMachineID, true); err != nil {
		return "", err
	}
	for _, node := range run.Nodes {
		if node.MachineID == run.OriginalPrimaryMachineID {
			continue
		}
		if err := s.verifyReplicaNode(ctx, run, node.MachineID, run.OriginalPrimaryMachineID, true); err != nil {
			return "", err
		}
	}
	states, err := s.ha.VIP().Scan(ctx, run.Cluster)
	if err != nil {
		return "", fmt.Errorf("最终 VIP 扫描失败: %w", err)
	}
	configs, err := s.ha.ListVIPConfigs(ctx, run.Cluster)
	if err != nil {
		return "", fmt.Errorf("最终 VIP 配置读取失败: %w", err)
	}
	enabledVIPs := make(map[string]struct{})
	for _, config := range configs {
		if config.Enabled {
			enabledVIPs[config.VIPAddress] = struct{}{}
		}
	}
	verifiedVIPs := 0
	for _, state := range states {
		if _, enabled := enabledVIPs[state.VIPAddress]; !enabled {
			continue
		}
		verifiedVIPs++
		if state.VIPStatus != hadomain.VipStatusBound || state.CurrentHolderMachineID != run.OriginalPrimaryMachineID {
			return "", fmt.Errorf("VIP %s 未唯一绑定在原主库，状态=%s，持有者=%s", state.VIPAddress, state.VIPStatus, state.DetectedHolders)
		}
	}
	if len(enabledVIPs) == 0 || verifiedVIPs != len(enabledVIPs) {
		return "", errors.New("最终 VIP 扫描未完整返回全部已启用业务入口")
	}
	return fmt.Sprintf("全部 %d 个节点均为 MySQL %s，复制健康，原主库为唯一写入口", len(run.Nodes), run.TargetVersion), nil
}

func (s *ClusterUpgradeService) executeSwitchover(ctx context.Context, run *ClusterUpgradeRun, currentPrimary, newPrimary string, detachCurrentPrimary bool) error {
	request := clusterUpgradeSwitchoverRequest(*run, currentPrimary, newPrimary, detachCurrentPrimary)
	runID := run.SwitchAwayRunID
	if newPrimary == run.OriginalPrimaryMachineID {
		runID = run.SwitchBackRunID
	}
	if runID == "" {
		architectureRun, err := s.ha.startArchitectureAdjustmentWithLock(ctx, ctx, run.Cluster, request, run.RunID)
		if err != nil {
			return err
		}
		runID = architectureRun.RunID
		run.ArchitectureRunIDs = append(run.ArchitectureRunIDs, runID)
		if newPrimary == run.OriginalPrimaryMachineID {
			run.SwitchBackRunID = runID
		} else {
			run.SwitchAwayRunID = runID
		}
		_ = s.tasks.AttachChildTasks(context.Background(), run.RunID, []string{runID})
		run.UpdatedAt = time.Now().UTC()
		_ = s.syncRun(context.Background(), *run)
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	timeout := time.NewTimer(20 * time.Minute)
	defer timeout.Stop()
	for {
		item, found, getErr := s.ha.GetArchitectureRun(ctx, run.Cluster, runID)
		if getErr != nil {
			return getErr
		}
		if !found {
			return errors.New("architecture switchover run disappeared")
		}
		switch item.Status {
		case hadomain.ArchitectureRunSucceeded:
			return nil
		case hadomain.ArchitectureRunFailed:
			return errors.New(item.Error)
		case hadomain.ArchitectureRunWaitingForce:
			return errors.New("复制未在安全窗口内追平；滚动升级禁止强制切主，流程已停止")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timeout.C:
			return errors.New("等待主从切换完成超时")
		case <-ticker.C:
		}
	}
}

func clusterUpgradeSwitchoverRequest(run ClusterUpgradeRun, currentPrimary, newPrimary string, detachCurrentPrimary bool) hadomain.ArchitectureAdjustmentRequest {
	request := hadomain.ArchitectureAdjustmentRequest{
		Architecture: hadomain.ArchitectureMasterSlave, CurrentArchitecture: hadomain.ArchitectureMasterSlave,
		CurrentMasterMachineID: currentPrimary, PreferredNewMasterMachineID: newPrimary,
		MoveVIP: true, ForceAfterTimeout: false,
	}
	if detachCurrentPrimary {
		request.MaintenanceDetachedMachineIDs = []string{currentPrimary}
	}
	for _, node := range run.Nodes {
		role, source := "S", newPrimary
		if node.MachineID == newPrimary {
			role, source = "M", ""
		}
		request.Nodes = append(request.Nodes, hadomain.ArchitectureNodeRequest{
			MachineID: node.MachineID, Port: node.Port, Role: role, SourceMachineID: source,
			DelaySeconds: node.DelaySeconds, ElectionPriority: boolPriority(node.MachineID == newPrimary),
		})
	}
	return request
}

func boolPriority(value bool) int {
	if value {
		return 1000
	}
	return 0
}

func (s *ClusterUpgradeService) verifyPrimaryNode(ctx context.Context, run *ClusterUpgradeRun, machineID string, requireTarget bool) error {
	node := clusterUpgradeNode(run, machineID)
	if node == nil {
		return errors.New("primary node not found")
	}
	machine, instance, err := s.tasks.ResolveMySQLInstance(ctx, machineID, node.Port)
	if err != nil {
		return err
	}
	probe, err := s.probeNode(ctx, machine, instance, run.RunID)
	if err != nil {
		return err
	}
	if probe.SourceHost != "" || probe.ReadOnly || probe.SuperReadOnly {
		return fmt.Errorf("节点 %s 不是唯一可写主库", node.Machine)
	}
	if requireTarget && probe.Version != run.TargetVersion {
		return fmt.Errorf("节点 %s 当前版本 %s，不是目标版本 %s", node.Machine, probe.Version, run.TargetVersion)
	}
	node.Role, node.SourceHost, node.CurrentVersion = "primary", "", probe.Version
	node.ReadOnly, node.SuperReadOnly = probe.ReadOnly, probe.SuperReadOnly
	return nil
}

func (s *ClusterUpgradeService) verifyReplicaNode(ctx context.Context, run *ClusterUpgradeRun, machineID, expectedPrimary string, requireTarget bool) error {
	node := clusterUpgradeNode(run, machineID)
	primary := clusterUpgradeNode(run, expectedPrimary)
	if node == nil || primary == nil {
		return errors.New("replica or expected primary node not found")
	}
	machine, instance, err := s.tasks.ResolveMySQLInstance(ctx, machineID, node.Port)
	if err != nil {
		return err
	}
	probe, err := s.probeNode(ctx, machine, instance, run.RunID)
	if err != nil {
		return err
	}
	sourceMatches := strings.EqualFold(probe.SourceHost, primary.IP) || strings.EqualFold(probe.SourceHost, primary.Machine) || strings.EqualFold(probe.SourceHost, primary.Hostname)
	if !sourceMatches || probe.SourcePort != primary.Port || !probe.ReadOnly || !probe.SuperReadOnly || probe.ChannelCount != 1 || !strings.EqualFold(probe.ReceiverState, "ON") || !strings.EqualFold(probe.ApplierState, "ON") {
		return fmt.Errorf("节点 %s 复制状态不安全（源=%s:%d，只读=%t/%t，接收=%s，应用=%s）", node.Machine, probe.SourceHost, probe.SourcePort, probe.ReadOnly, probe.SuperReadOnly, probe.ReceiverState, probe.ApplierState)
	}
	if requireTarget && probe.Version != run.TargetVersion {
		return fmt.Errorf("节点 %s 当前版本 %s，不是目标版本 %s", node.Machine, probe.Version, run.TargetVersion)
	}
	node.Role, node.SourceHost, node.SourcePort, node.CurrentVersion = "replica", probe.SourceHost, probe.SourcePort, probe.Version
	node.ReadOnly, node.SuperReadOnly = probe.ReadOnly, probe.SuperReadOnly
	node.ReceiverState, node.ApplierState = probe.ReceiverState, probe.ApplierState
	return nil
}

func isMGRClusterUpgrade(run ClusterUpgradeRun) bool {
	return run.Architecture == string(hadomain.ArchitectureMGRRouter)
}

func (s *ClusterUpgradeService) verifyMGRNode(ctx context.Context, run *ClusterUpgradeRun, machineID, expectedRole string, requireTarget bool) error {
	return s.verifyMGRNodeWithOnlineCount(ctx, run, machineID, expectedRole, requireTarget, len(run.Nodes))
}

func (s *ClusterUpgradeService) verifyMGRNodeWithOnlineCount(ctx context.Context, run *ClusterUpgradeRun, machineID, expectedRole string, requireTarget bool, expectedOnline int) error {
	node := clusterUpgradeNode(run, machineID)
	if node == nil {
		return errors.New("MGR member not found")
	}
	machine, instance, err := s.tasks.ResolveMySQLInstance(ctx, machineID, node.Port)
	if err != nil {
		return err
	}
	probe, err := s.probeNode(ctx, machine, instance, run.RunID)
	if err != nil {
		return err
	}
	if !strings.EqualFold(probe.GroupState, "ONLINE") ||
		!strings.EqualFold(probe.GroupRole, expectedRole) ||
		probe.GroupMembers < expectedOnline ||
		probe.GroupMembers > len(run.Nodes) ||
		probe.GroupOnline != expectedOnline ||
		probe.GroupPrimary != 1 ||
		probe.GroupQueue != 0 ||
		probe.OfflineMode {
		return fmt.Errorf(
			"成员 %s 的 MGR 状态不安全（状态=%s，角色=%s，在线=%d/%d，PRIMARY=%d，队列=%d，offline_mode=%t）",
			node.Machine, probe.GroupState, probe.GroupRole, probe.GroupOnline, probe.GroupMembers, probe.GroupPrimary, probe.GroupQueue, probe.OfflineMode,
		)
	}
	if requireTarget && probe.Version != run.TargetVersion {
		return fmt.Errorf("节点 %s 当前版本 %s，不是目标版本 %s", node.Machine, probe.Version, run.TargetVersion)
	}
	node.Role, node.GroupState, node.GroupRole = strings.ToLower(expectedRole), probe.GroupState, probe.GroupRole
	node.ServerUUID, node.CurrentVersion = probe.ServerUUID, probe.Version
	node.ReadOnly, node.SuperReadOnly = probe.ReadOnly, probe.SuperReadOnly
	return nil
}

func (s *ClusterUpgradeService) executeMGRControlSQL(ctx context.Context, run *ClusterUpgradeRun, machineID, operation, displayName, sql string, timeout time.Duration) error {
	node := clusterUpgradeNode(run, machineID)
	if node == nil {
		return errors.New("MGR control member not found")
	}
	machine, instance, err := s.tasks.ResolveMySQLInstance(ctx, machineID, node.Port)
	if err != nil {
		return err
	}
	client := upgradeShellQuote(instance.BaseDir+"/bin/mysql") + " --defaults-extra-file=" + mysqlDefaultsFilePlaceholder +
		fmt.Sprintf(" --protocol=tcp --host=127.0.0.1 --port=%d --batch --raw --skip-column-names", node.Port)
	detail, err := s.tasks.CreateExecTaskWithOptions(ctx, machine.IP, client+" --execute="+upgradeShellQuote(sql), ExecTaskOptions{
		ParentTaskID: run.RunID, Operation: operation, DisplayName: displayName, StepName: displayName, Port: node.Port,
	})
	if err != nil {
		return err
	}
	completed, err := s.tasks.WaitForTask(ctx, detail.Task.ID, timeout)
	if err != nil {
		return err
	}
	if completed.Task.Status != taskdomain.StatusSuccess {
		return fmt.Errorf("%s: %s", displayName, emptyTaskError(completed))
	}
	return nil
}

func (s *ClusterUpgradeService) leaveMGRForUpgrade(ctx context.Context, run *ClusterUpgradeRun, machineID string) error {
	if err := s.executeMGRControlSQL(
		ctx, run, machineID, "mysql_cluster_upgrade_mgr_leave", "隔离并移出待升级 MGR 成员",
		"SET PERSIST group_replication_start_on_boot=OFF; SET GLOBAL offline_mode=ON; SET GLOBAL super_read_only=ON; SET GLOBAL read_only=ON; STOP GROUP_REPLICATION;",
		2*time.Minute,
	); err != nil {
		return err
	}
	target := run.OriginalPrimaryMachineID
	if target == machineID {
		target = run.TemporaryPrimaryMachineID
	}
	if target == machineID || target == "" {
		for _, member := range run.Nodes {
			if member.MachineID != machineID {
				target = member.MachineID
				break
			}
		}
	}
	node := clusterUpgradeNode(run, target)
	if node == nil {
		return errors.New("temporary MGR primary not found")
	}
	return s.waitMGRGroupHealthy(ctx, run, target, len(run.Nodes)-1)
}

func (s *ClusterUpgradeService) rejoinMGRMember(ctx context.Context, run *ClusterUpgradeRun, machineID string) error {
	return s.executeMGRControlSQL(
		ctx, run, machineID, "mysql_cluster_upgrade_mgr_rejoin", "恢复 MGR 成员",
		"START GROUP_REPLICATION;",
		4*time.Minute,
	)
}

func (s *ClusterUpgradeService) persistMGRStartOnBoot(ctx context.Context, run *ClusterUpgradeRun, machineID string) error {
	return s.executeMGRControlSQL(
		ctx, run, machineID, "mysql_cluster_upgrade_mgr_autostart", "恢复 MGR 成员自动入组",
		"SET PERSIST group_replication_start_on_boot=ON; SET GLOBAL offline_mode=OFF;",
		time.Minute,
	)
}

func (s *ClusterUpgradeService) waitMGRGroupHealthy(ctx context.Context, run *ClusterUpgradeRun, observerMachineID string, expectedOnline int) error {
	node := clusterUpgradeNode(run, observerMachineID)
	if node == nil {
		return errors.New("MGR observer member not found")
	}
	machine, instance, err := s.tasks.ResolveMySQLInstance(ctx, observerMachineID, node.Port)
	if err != nil {
		return err
	}
	client := upgradeShellQuote(instance.BaseDir+"/bin/mysql") + " --defaults-extra-file=" + mysqlDefaultsFilePlaceholder +
		fmt.Sprintf(" --protocol=tcp --host=127.0.0.1 --port=%d --batch --raw --skip-column-names", node.Port)
	sql := fmt.Sprintf(
		"SELECT IF((SELECT COUNT(*) FROM performance_schema.replication_group_members)=%d AND (SELECT COUNT(*) FROM performance_schema.replication_group_members WHERE MEMBER_STATE='ONLINE')=%d AND (SELECT COUNT(*) FROM performance_schema.replication_group_members WHERE MEMBER_ROLE='PRIMARY')=1 AND COALESCE((SELECT SUM(COUNT_TRANSACTIONS_IN_QUEUE+COUNT_TRANSACTIONS_REMOTE_IN_APPLIER_QUEUE) FROM performance_schema.replication_group_member_stats),0)=0,'MGR_READY','MGR_WAIT');",
		expectedOnline, expectedOnline,
	)
	command := "for i in $(seq 1 120); do " + client + " --execute=" + upgradeShellQuote(sql) +
		" 2>/dev/null | grep -Fxq MGR_READY && exit 0; sleep 1; done; exit 1"
	detail, err := s.tasks.CreateExecTaskWithOptions(ctx, machine.IP, command, ExecTaskOptions{
		ParentTaskID: run.RunID, Operation: "mysql_cluster_upgrade_mgr_quorum",
		DisplayName: "等待 MGR 剩余成员稳定", StepName: "验证多数派、PRIMARY 与应用队列", Port: node.Port,
	})
	if err != nil {
		return err
	}
	completed, err := s.tasks.WaitForTask(ctx, detail.Task.ID, 3*time.Minute)
	if err != nil {
		return err
	}
	if completed.Task.Status != taskdomain.StatusSuccess {
		return fmt.Errorf("MGR 成员离组后未恢复稳定多数派: %s", emptyTaskError(completed))
	}
	return nil
}

func (s *ClusterUpgradeService) waitMGRMemberOnline(ctx context.Context, run *ClusterUpgradeRun, machineID string, expectedOnline int) error {
	sql := fmt.Sprintf(
		"SELECT IF(COALESCE((SELECT MEMBER_STATE FROM performance_schema.replication_group_members WHERE MEMBER_ID=@@server_uuid),'')='ONLINE' AND (SELECT COUNT(*) FROM performance_schema.replication_group_members)=%d AND (SELECT COUNT(*) FROM performance_schema.replication_group_members WHERE MEMBER_STATE='ONLINE')=%d AND (SELECT COUNT(*) FROM performance_schema.replication_group_members WHERE MEMBER_ROLE='PRIMARY')=1 AND COALESCE((SELECT SUM(COUNT_TRANSACTIONS_IN_QUEUE+COUNT_TRANSACTIONS_REMOTE_IN_APPLIER_QUEUE) FROM performance_schema.replication_group_member_stats),0)=0,'MGR_READY','MGR_WAIT');",
		expectedOnline, expectedOnline,
	)
	node := clusterUpgradeNode(run, machineID)
	if node == nil {
		return errors.New("MGR wait member not found")
	}
	machine, instance, err := s.tasks.ResolveMySQLInstance(ctx, machineID, node.Port)
	if err != nil {
		return err
	}
	client := upgradeShellQuote(instance.BaseDir+"/bin/mysql") + " --defaults-extra-file=" + mysqlDefaultsFilePlaceholder +
		fmt.Sprintf(" --protocol=tcp --host=127.0.0.1 --port=%d --batch --raw --skip-column-names", node.Port)
	command := "for i in $(seq 1 180); do " + client + " --execute=" + upgradeShellQuote(sql) +
		" 2>/dev/null | grep -Fxq MGR_READY && exit 0; sleep 1; done; exit 1"
	detail, err := s.tasks.CreateExecTaskWithOptions(ctx, machine.IP, command, ExecTaskOptions{
		ParentTaskID: run.RunID, Operation: "mysql_cluster_upgrade_mgr_wait",
		DisplayName: "等待 MGR 成员重新 ONLINE", StepName: "验证 MGR 成员与多数派", Port: node.Port,
	})
	if err != nil {
		return err
	}
	completed, err := s.tasks.WaitForTask(ctx, detail.Task.ID, 4*time.Minute)
	if err != nil {
		return err
	}
	if completed.Task.Status != taskdomain.StatusSuccess {
		return fmt.Errorf("成员 %s 未在超时前重新 ONLINE: %s", node.Machine, emptyTaskError(completed))
	}
	return nil
}

func (s *ClusterUpgradeService) setMGRPrimary(ctx context.Context, run *ClusterUpgradeRun, machineID, excludedMachineID string) error {
	node := clusterUpgradeNode(run, machineID)
	if node == nil {
		return errors.New("target MGR primary member not found")
	}
	if strings.TrimSpace(node.ServerUUID) == "" {
		return fmt.Errorf("成员 %s 缺少 server_uuid，无法执行 PRIMARY 切换", node.Machine)
	}
	machine, instance, err := s.tasks.ResolveMySQLInstance(ctx, machineID, node.Port)
	if err != nil {
		return err
	}
	client := upgradeShellQuote(instance.BaseDir+"/bin/mysql") + " --defaults-extra-file=" + mysqlDefaultsFilePlaceholder +
		fmt.Sprintf(" --protocol=tcp --host=127.0.0.1 --port=%d --batch --raw --skip-column-names", node.Port)
	switchSQL := "SELECT IF((SELECT MEMBER_ROLE FROM performance_schema.replication_group_members WHERE MEMBER_ID=@@server_uuid)='PRIMARY','ALREADY_PRIMARY',group_replication_set_as_primary(" + upgradeShellQuote(node.ServerUUID) + "));"
	roleSQL := "SELECT MEMBER_ROLE FROM performance_schema.replication_group_members WHERE MEMBER_ID=@@server_uuid;"
	command := client + " --execute=" + upgradeShellQuote(switchSQL) +
		" && for i in $(seq 1 120); do " + client + " --execute=" + upgradeShellQuote(roleSQL) +
		" | grep -Fxq PRIMARY && exit 0; sleep 1; done; exit 1"
	detail, err := s.tasks.CreateExecTaskWithOptions(ctx, machine.IP, command, ExecTaskOptions{
		ParentTaskID: run.RunID, Operation: "mysql_cluster_upgrade_mgr_primary",
		DisplayName: "切换 MGR PRIMARY 到 " + node.Machine, StepName: "执行 Group Replication 单主切换", Port: node.Port,
	})
	if err != nil {
		return err
	}
	completed, err := s.tasks.WaitForTask(ctx, detail.Task.ID, 3*time.Minute)
	if err != nil {
		return err
	}
	if completed.Task.Status != taskdomain.StatusSuccess {
		return fmt.Errorf("切换 MGR PRIMARY 失败: %s", emptyTaskError(completed))
	}
	expectedOnline := len(run.Nodes)
	if excludedMachineID != "" {
		expectedOnline--
	}
	for _, member := range run.Nodes {
		if member.MachineID == excludedMachineID {
			continue
		}
		role := "SECONDARY"
		if member.MachineID == machineID {
			role = "PRIMARY"
		}
		if err := s.verifyMGRNodeWithOnlineCount(ctx, run, member.MachineID, role, false, expectedOnline); err != nil {
			return err
		}
	}
	return nil
}

func clusterUpgradeNode(run *ClusterUpgradeRun, machineID string) *ClusterUpgradeNode {
	for index := range run.Nodes {
		if run.Nodes[index].MachineID == machineID {
			return &run.Nodes[index]
		}
	}
	return nil
}

func clusterUpgradeNodeName(run *ClusterUpgradeRun, machineID string) string {
	if node := clusterUpgradeNode(run, machineID); node != nil {
		return node.Machine
	}
	return machineID
}
