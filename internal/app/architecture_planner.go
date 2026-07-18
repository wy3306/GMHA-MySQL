package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	hadomain "gmha/internal/domain/ha"
	machinedomain "gmha/internal/domain/machine"
	taskdomain "gmha/internal/domain/task"
	mysqlapp "gmha/internal/mysql"
)

// PlanArchitectureAdjustment 对现有实例执行只读预检，生成候选选举结果和严格有序的切换计划。
func (s *HAService) PlanArchitectureAdjustment(ctx context.Context, clusterID string, req hadomain.ArchitectureAdjustmentRequest) (hadomain.ArchitectureAdjustmentPlan, error) {
	clusterID = strings.TrimSpace(clusterID)
	if clusterID == "" {
		return hadomain.ArchitectureAdjustmentPlan{}, errors.New("cluster_id is required")
	}
	if err := validateArchitectureRequest(req); err != nil {
		return hadomain.ArchitectureAdjustmentPlan{}, err
	}
	machines, err := s.machines.List(ctx)
	if err != nil {
		return hadomain.ArchitectureAdjustmentPlan{}, err
	}
	machineByID := make(map[string]machinedomain.Machine)
	for _, machine := range machines {
		if machine.Cluster == clusterID {
			machineByID[machine.ID] = machine
		}
	}
	instances, err := s.instances.List(ctx)
	if err != nil {
		return hadomain.ArchitectureAdjustmentPlan{}, err
	}
	instancesByMachine := make(map[string][]mysqlapp.Instance)
	for _, instance := range instances {
		if _, ok := machineByID[instance.MachineID]; ok {
			instancesByMachine[instance.MachineID] = append(instancesByMachine[instance.MachineID], instance)
		}
	}
	if len(req.Nodes) < 2 {
		return hadomain.ArchitectureAdjustmentPlan{}, errors.New("at least two target nodes are required")
	}
	scores := architectureCandidateScores(clusterID, req, machineByID, instancesByMachine)
	plan := hadomain.ArchitectureAdjustmentPlan{
		PlanID: "arch-" + strings.TrimPrefix(newFailoverID(), "fo-"), ClusterID: clusterID,
		Architecture:            req.Architecture,
		WaitDelayTimeoutSeconds: 60, RequiresForceConfirmation: true,
		CreatedAt: time.Now().UTC(), Executable: true,
	}
	if s.tasks != nil && !req.VIPOnly {
		for _, node := range req.Nodes {
			if compatible, reason := s.tasks.MachineCapability(node.MachineID, taskdomain.CapabilityMySQLDefaultsFile); !compatible {
				plan.Executable = false
				name := node.MachineID
				if machine, ok := machineByID[node.MachineID]; ok && strings.TrimSpace(machine.Name) != "" {
					name = machine.Name
				}
				plan.BlockingReasons = append(plan.BlockingReasons, fmt.Sprintf("节点 %s 无法执行架构切换：%s，请先升级该节点 Agent", name, reason))
			}
		}
	}
	if req.VIPOnly {
		plan.RequiresForceConfirmation = false
	}
	if req.Architecture == hadomain.ArchitectureStandalone {
		plan.RankedCandidates = scores
		plan.RequiresForceConfirmation = false
		for _, score := range scores {
			if !score.Eligible {
				plan.Executable = false
				plan.BlockingReasons = append(plan.BlockingReasons, fmt.Sprintf("independent target %s is not eligible: %s", score.MachineID, strings.Join(score.RejectReasons, ", ")))
			}
		}
	} else {
		selected, ranked, selectErr := NewCandidateSelector().Select(scores)
		plan.RankedCandidates = ranked
		if selectErr != nil {
			plan.Executable = false
			plan.BlockingReasons = append(plan.BlockingReasons, selectErr.Error())
		} else {
			plan.SelectedCandidate = selected
		}
	}
	network, networkErr := s.repo.GetNetworkPolicy(ctx, clusterID)
	if networkErr == nil {
		plan.VIPRouteMode = network.VIPRouteMode
	}
	vips, vipErr := s.repo.ListVIPConfigs(ctx, clusterID)
	if vipErr == nil {
		for _, vip := range vips {
			plan.VIPAddresses = append(plan.VIPAddresses, vip.VIPAddress)
			if plan.VIPRouteMode == "" || len(plan.VIPAddresses) == 1 {
				plan.VIPRouteMode = vip.VIPRouteMode
			}
			if vip.VIPRouteMode != "" && plan.VIPRouteMode != vip.VIPRouteMode {
				plan.Executable = false
				plan.BlockingReasons = append(plan.BlockingReasons, "all enabled VIPs must use the same route mode in one architecture adjustment")
			}
		}
	}
	if req.MoveVIP {
		if len(plan.VIPAddresses) == 0 {
			plan.Executable = false
			plan.BlockingReasons = append(plan.BlockingReasons, "VIP migration requested but no enabled VIP is configured")
		}
		switch plan.VIPRouteMode {
		case hadomain.VipRouteModeL2ARP:
			if s.tasks == nil {
				plan.Executable = false
				plan.BlockingReasons = append(plan.BlockingReasons, "L2 ARP driver is not configured with a live Agent network executor")
			} else {
				plan.Warnings = append(plan.Warnings, "L2 VIP will be removed from every cluster node, verified absent, then bound to the promoted primary and announced by gratuitous ARP")
			}
		case hadomain.VipRouteModeBGP:
			if s.tasks == nil {
				plan.Executable = false
				plan.BlockingReasons = append(plan.BlockingReasons, "BGP driver is not configured with a live routing executor")
			}
		default:
			plan.Executable = false
			plan.BlockingReasons = append(plan.BlockingReasons, fmt.Sprintf("VIP route mode %s cannot be executed automatically", plan.VIPRouteMode))
		}
	}
	plan.Steps = architecturePlanSteps(req)
	return plan, nil
}

func validateArchitectureRequest(req hadomain.ArchitectureAdjustmentRequest) error {
	switch req.Architecture {
	case hadomain.ArchitectureStandalone, hadomain.ArchitectureMasterSlave, hadomain.ArchitectureDualMaster, hadomain.ArchitectureMultiMaster:
	default:
		return fmt.Errorf("unsupported architecture %s", req.Architecture)
	}
	masters, independents := 0, 0
	seen := make(map[string]bool)
	for _, node := range req.Nodes {
		if strings.TrimSpace(node.MachineID) == "" {
			return errors.New("node machine_id is required")
		}
		if seen[node.MachineID] {
			return fmt.Errorf("machine %s selected more than once", node.MachineID)
		}
		seen[node.MachineID] = true
		if node.DelaySeconds < 0 {
			return fmt.Errorf("delay_seconds for machine %s cannot be negative", node.MachineID)
		}
		role := strings.ToUpper(strings.TrimSpace(node.Role))
		if role != "M" && role != "S" && role != "I" {
			return fmt.Errorf("node %s role must be M, S or I", node.MachineID)
		}
		if role == "M" {
			masters++
			if node.DelaySeconds > 0 {
				return fmt.Errorf("master candidate %s cannot be a delayed replica", node.MachineID)
			}
		} else if role == "I" {
			independents++
			if node.SourceMachineID != "" || node.DelaySeconds != 0 {
				return fmt.Errorf("independent node %s cannot have a replication source or delay", node.MachineID)
			}
		}
	}
	if req.CurrentMasterMachineID != "" && !seen[req.CurrentMasterMachineID] {
		return errors.New("current master must be included in target nodes")
	}
	if req.InitializeVIP && !req.MoveVIP {
		return errors.New("VIP initialization requires move_vip")
	}
	if req.InitializeVIP && req.CurrentMasterMachineID != "" {
		return errors.New("VIP initialization cannot declare a current master")
	}
	if req.VIPOnly && !req.MoveVIP {
		return errors.New("VIP-only workflow requires move_vip")
	}
	if req.VIPOnly && req.Architecture == hadomain.ArchitectureStandalone {
		return errors.New("VIP-only workflow requires a replicated architecture")
	}
	if req.VIPOnly {
		if strings.TrimSpace(req.PreferredNewMasterMachineID) == "" {
			return errors.New("VIP-only workflow requires a target master")
		}
		targetIsMaster := false
		for _, node := range req.Nodes {
			if node.MachineID == req.PreferredNewMasterMachineID && strings.EqualFold(strings.TrimSpace(node.Role), "M") {
				targetIsMaster = true
				break
			}
		}
		if !targetIsMaster {
			return errors.New("VIP-only target must be a master in the target topology")
		}
	}
	// When all current nodes are independent writers there is no current master
	// to declare. The executor freezes every writer before scanning/removing the
	// VIP, which provides the same fencing guarantee as the normal old-master path.
	if req.MoveVIP && req.CurrentMasterMachineID != "" && !req.InitializeVIP && req.PreferredNewMasterMachineID == req.CurrentMasterMachineID {
		return errors.New("VIP migration requires a different target master; the current master already owns the traffic endpoint")
	}
	if req.Architecture == hadomain.ArchitectureStandalone {
		if independents != len(req.Nodes) || masters != 0 {
			return errors.New("standalone architecture requires every node to use independent role I")
		}
		if req.MoveVIP {
			return errors.New("standalone architecture cannot own or migrate a shared VIP")
		}
		return nil
	}
	if independents != 0 {
		return errors.New("replicated architectures cannot contain independent role I")
	}
	if req.Architecture == hadomain.ArchitectureMasterSlave && masters != 1 {
		return errors.New("master_slave requires exactly one master")
	}
	if req.Architecture == hadomain.ArchitectureDualMaster && masters != 2 {
		return errors.New("dual-master architecture requires exactly two masters")
	}
	if req.Architecture == hadomain.ArchitectureMultiMaster && masters < 3 {
		return errors.New("multi-master architecture requires at least three masters")
	}
	return nil
}

func architectureCandidateScores(clusterID string, req hadomain.ArchitectureAdjustmentRequest, machines map[string]machinedomain.Machine, instances map[string][]mysqlapp.Instance) []hadomain.CandidateScore {
	serverIDs := make(map[int]int)
	for _, node := range req.Nodes {
		if instance, ok := architectureInstanceForNode(node, instances[node.MachineID]); ok {
			serverIDs[instance.ServerID]++
		}
	}
	out := make([]hadomain.CandidateScore, 0, len(req.Nodes))
	for _, node := range req.Nodes {
		machine, machineOK := machines[node.MachineID]
		instance, instanceOK := architectureInstanceForNode(node, instances[node.MachineID])
		score := hadomain.CandidateScore{ClusterID: clusterID, MachineID: node.MachineID, Hostname: machine.Name, IP: machine.IP, Port: node.Port, Eligible: true, DataFreshnessScore: 100, RelayReceivedScore: 100, RelayExecutedScore: 100, HealthScore: 100, DelaySeconds: node.DelaySeconds, ElectionPriority: node.ElectionPriority, CanBindVIP: true}
		if instanceOK {
			score.InstanceID = instanceID(instance)
			if score.Port <= 0 {
				score.Port = instance.Port
			}
		}
		if !machineOK || !instanceOK {
			score.Eligible = false
			score.RejectReasons = append(score.RejectReasons, "machine or MySQL instance is not registered in this cluster")
		}
		if instanceOK && instance.Status != "" && !strings.EqualFold(instance.Status, mysqlapp.StatusRunning) {
			score.Eligible = false
			score.HealthScore = 0
			score.RejectReasons = append(score.RejectReasons, "instance is not running")
		}
		if instanceOK && serverIDs[instance.ServerID] > 1 {
			score.Eligible = false
			score.RejectReasons = append(score.RejectReasons, "server_id is not unique")
		}
		if node.DelaySeconds > 0 {
			score.Eligible = false
			score.RejectReasons = append(score.RejectReasons, "delayed replica cannot be elected automatically")
		}
		if req.Architecture != hadomain.ArchitectureStandalone && !strings.EqualFold(strings.TrimSpace(node.Role), "M") {
			score.Eligible = false
			score.RejectReasons = append(score.RejectReasons, "target role is replica and cannot be promoted")
		}
		if req.Architecture != hadomain.ArchitectureStandalone && req.CurrentMasterMachineID != "" && node.MachineID == req.CurrentMasterMachineID && req.PreferredNewMasterMachineID != req.CurrentMasterMachineID {
			score.Eligible = false
			score.RejectReasons = append(score.RejectReasons, "candidate is current master")
		}
		if req.PreferredNewMasterMachineID != "" {
			if node.MachineID == req.PreferredNewMasterMachineID {
				score.ElectionPriority += 1_000_000
			} else {
				score.ElectionPriority -= 1_000_000
			}
		}
		out = append(out, score)
	}
	return out
}

func architectureInstanceForNode(node hadomain.ArchitectureNodeRequest, instances []mysqlapp.Instance) (mysqlapp.Instance, bool) {
	if node.Port > 0 {
		for _, instance := range instances {
			if instance.Port == node.Port {
				return instance, true
			}
		}
		return mysqlapp.Instance{}, false
	}
	if len(instances) == 1 {
		return instances[0], true
	}
	return mysqlapp.Instance{}, false
}

func architecturePlanSteps(req hadomain.ArchitectureAdjustmentRequest) []hadomain.ArchitecturePlanStep {
	if req.VIPOnly {
		items := []hadomain.ArchitecturePlanStep{
			{Code: "acquire_lock", Name: "获取集群切换锁", Description: "阻止并发 VIP 绑定、漂移和架构变更"},
			{Code: "preflight", Name: "实时预检", Description: "确认 Agent、MHA 管理通道、目标主节点和业务网卡可用"},
		}
		if !req.InitializeVIP {
			items = append(items,
				hadomain.ArchitecturePlanStep{Code: "freeze_business_access", Name: "锁定业务入口", Description: "在全部相关 MySQL 节点启用 offline_mode，阻止新业务连接进入", Destructive: true},
				hadomain.ArchitecturePlanStep{Code: "drain_business_sessions", Name: "排空业务会话", Description: "保留 MHA、复制、监控和备份连接，清理其余存量业务会话", Destructive: true},
			)
		}
		items = append(items,
			hadomain.ArchitecturePlanStep{Code: "check_vip_conflict", Name: "扫描 VIP 持有者", Description: "在全部集群机器检查 VIP，发现多持有者立即中止"},
			hadomain.ArchitecturePlanStep{Code: "withdraw_vip", Name: "撤销旧 VIP", Description: "从全部集群机器撤销 VIP，关闭旧业务网络入口", Destructive: true},
			hadomain.ArchitecturePlanStep{Code: "verify_zero_vip", Name: "确认零持有者屏障", Description: "再次扫描全部机器，只有确认 VIP 持有者为零才允许继续"},
			hadomain.ArchitecturePlanStep{Code: "bind_vip", Name: reqVIPStepName(req), Description: "在目标主节点绑定 VIP，并自动执行 ARP/BGP 网络宣告", Destructive: true},
			hadomain.ArchitecturePlanStep{Code: "verify_single_vip", Name: "防脑裂复核", Description: "连续从全部集群机器确认 VIP 仅由目标主节点持有"},
		)
		if !req.InitializeVIP {
			items = append(items, hadomain.ArchitecturePlanStep{Code: "resume_business_connections", Name: "恢复业务访问", Description: "唯一持有者验证通过后关闭 offline_mode，恢复业务连接"})
		}
		items = append(items,
			hadomain.ArchitecturePlanStep{Code: "release_lock", Name: "释放切换锁", Description: "记录审计结果并释放集群锁"},
		)
		for index := range items {
			items[index].Order = index + 1
		}
		return items
	}
	if items := architectureConversionPlanSteps(req); len(items) > 0 {
		return items
	}
	if req.Architecture == hadomain.ArchitectureStandalone {
		items := []hadomain.ArchitecturePlanStep{
			{Code: "acquire_lock", Name: "获取集群切换锁", Description: "阻止并发架构变更和脑裂"},
			{Code: "preflight", Name: "实时预检", Description: "确认 Agent、MySQL、GTID 与 server_id 状态"},
			{Code: "validate_independent_targets", Name: "确认拆分目标", Description: "逐节点读取实时 GTID 与只读状态，确认所有实例可安全拆分"},
			{Code: "freeze_old_master", Name: "冻结当前主库写入", Description: "拆分前短暂冻结写入，建立一致的数据分界点", Destructive: true},
			{Code: "kill_business_sessions", Name: "清理业务会话", Description: "清理非管理连接，防止校验期间产生新事务", Destructive: true},
			{Code: "wait_replication_zero", Name: "等待所有从库追平", Description: "确认复制延迟、线程和 GTID 均已追平"},
			{Code: "pt_verify_before_split", Name: "PT 数据一致性验证", Description: "使用 pt-table-checksum 验证拆分前各实例数据完全一致"},
			{Code: "detach_replication", Name: "解除复制并转为独立实例", Description: "停止并清理复制关系，恢复每个实例独立可写", Destructive: true},
			{Code: "verify_topology", Name: "验证独立实例", Description: "确认无复制通道、实例可写且自增参数已恢复"},
			{Code: "resume_business_connections", Name: "恢复业务连接", Description: "所有校验通过后关闭 offline_mode，重新允许业务建立连接"},
			{Code: "release_lock", Name: "释放切换锁", Description: "记录审计结果并释放集群锁"},
		}
		items = addArchitectureManagementRepairStep(items, req)
		for index := range items {
			items[index].Order = index + 1
		}
		return items
	}
	if req.CurrentMasterMachineID == "" && !req.InitializeVIP {
		items := []hadomain.ArchitecturePlanStep{
			{Code: "acquire_lock", Name: "获取集群切换锁", Description: "阻止并发架构变更和脑裂"},
			{Code: "preflight", Name: "实时预检", Description: "确认 Agent、MySQL、GTID 与 server_id 状态"},
			{Code: "freeze_old_master", Name: "冻结全部独立写入口", Description: "建立复制前冻结所有独立实例写入，阻止新的分叉事务", Destructive: true},
			{Code: "kill_business_sessions", Name: "清理业务会话", Description: "保留管理连接，清理可能继续写入的业务会话", Destructive: true},
			{Code: "elect_candidate", Name: "选举数据基准节点", Description: "比较实时 GTID 集合，仅允许包含全部事务历史的节点作为复制基准"},
			{Code: "align_replica_gtid", Name: "对齐空实例 GTID 基线", Description: "仅当目标从节点不存在业务表时，清理其独立心跳 GTID 并标记主库基线", Destructive: true},
			{Code: "promote_new_master", Name: "启用基准主节点", Description: "清理旧复制元数据并恢复目标主节点可写", Destructive: true},
			{Code: "repoint_replicas", Name: "建立目标复制关系", Description: "通过 Agent 配置一主多从或双向复制", Destructive: true},
			{Code: "verify_topology", Name: "验证复制拓扑", Description: "验证复制线程、GTID、只读状态和自增参数"},
			{Code: "pt_repair_on_failure", Name: "按需 PT 修复", Description: "仅在显式强制路径中使用 pt-table-sync 修复", Destructive: true, RequiresConfirmation: true},
			{Code: "pt_verify_replication", Name: "PT 复制一致性验证", Description: "使用 pt-table-checksum 验证新复制关系的数据一致性"},
		}
		if req.MoveVIP {
			items = append(items,
				hadomain.ArchitecturePlanStep{Code: "move_vip", Name: "迁移 VIP", Description: "在全部集群机器撤销 VIP，确认零持有者后绑定新主并自动宣告", Destructive: true},
				hadomain.ArchitecturePlanStep{Code: "verify_single_vip", Name: "防脑裂复核", Description: "连续从全部集群机器确认 VIP 仅由新主持有"},
			)
		}
		items = append(items, hadomain.ArchitecturePlanStep{Code: "resume_business_connections", Name: "恢复业务连接", Description: "拓扑、PT 与 VIP 校验全部通过后关闭 offline_mode"})
		items = append(items, hadomain.ArchitecturePlanStep{Code: "release_lock", Name: "释放切换锁", Description: "记录审计结果并释放集群锁"})
		items = addArchitectureManagementRepairStep(items, req)
		for index := range items {
			items[index].Order = index + 1
		}
		return items
	}
	definitions := []struct {
		code, name, description string
		destructive, confirm    bool
	}{
		{"acquire_lock", "获取集群切换锁", "使用带 TTL 的集群级锁阻止并发切换和脑裂", false, false},
		{"preflight", "实时预检", "确认 Agent、MySQL、GTID、server_id、候选节点与网络接口状态", false, false},
		{"elect_candidate", "实时选举候选主库", "仅从目标主角色、非延时且健康节点中，按 GTID 新鲜度和人工优先级选举", false, false},
		{"check_vip_conflict", "扫描 VIP 持有者", "在所有集群机器检查 VIP，发现多持有者立即中止", false, false},
		{"freeze_old_master", "冻结旧主写入", "设置 read_only/super_read_only，并阻止新业务连接", true, false},
		{"kill_business_sessions", "清理业务会话", "保留 root、复制、监控、MHA、备份及请求中声明的管理用户", true, false},
		{"wait_replication_zero", "等待复制追平", "最多等待 60 秒，必须同时满足延迟为 0 且 GTID/relay log 已执行完成", false, false},
		{"force_gate", "强制切主确认", "超过 60 秒暂停流程并要求用户显式确认数据丢失风险", true, true},
		{"fence_old_master", "隔离旧主", "再次验证 read_only 与 super_read_only，无法确认旧主不可写时禁止继续", true, false},
		{"promote_new_master", "提升新主", "停止并重置复制，关闭只读，再次验证只有一个可写主节点", true, false},
		{"repoint_replicas", "重定向复制关系", "按目标架构配置一主多从、双主及延时从库", true, false},
		{"verify_topology", "验证新拓扑", "验证复制线程、GTID、只读状态和延时从库参数", false, false},
		{"pt_repair_on_failure", "按需 PT 修复", "仅在强制切主后的复制重建失败时，使用 pt-table-sync 修复并重新校验", true, true},
		{"pt_verify_replication", "PT 复制一致性验证", "新复制关系建立后必须运行 pt-table-checksum，任一实例存在差异都会阻断成功", false, false},
	}
	if req.MoveVIP {
		definitions = append(definitions,
			struct {
				code, name, description string
				destructive, confirm    bool
			}{"move_vip", "迁移 VIP", "在全部集群机器撤销 VIP，确认零持有者后绑定新主并自动宣告", true, false},
			struct {
				code, name, description string
				destructive, confirm    bool
			}{"verify_single_vip", "防脑裂复核", "连续从全部集群机器确认 VIP 仅由新主持有；无法证明时撤销新节点绑定", false, false},
		)
	}
	definitions = append(definitions,
		struct {
			code, name, description string
			destructive, confirm    bool
		}{"resume_business_connections", "恢复业务连接", "拓扑、PT 与 VIP 校验全部通过后关闭 offline_mode，重新允许业务建立连接", false, false},
	)
	definitions = append(definitions,
		struct {
			code, name, description string
			destructive, confirm    bool
		}{"release_lock", "释放切换锁", "记录审计结果并释放集群锁", false, false},
	)
	steps := make([]hadomain.ArchitecturePlanStep, 0, len(definitions))
	for index, item := range definitions {
		steps = append(steps, hadomain.ArchitecturePlanStep{Order: index + 1, Code: item.code, Name: item.name, Description: item.description, Destructive: item.destructive, RequiresConfirmation: item.confirm})
	}
	steps = addArchitectureManagementRepairStep(steps, req)
	for index := range steps {
		steps[index].Order = index + 1
	}
	return steps
}

func architectureTransitionKind(req hadomain.ArchitectureAdjustmentRequest) string {
	current, target := strings.TrimSpace(req.CurrentArchitecture), strings.TrimSpace(req.Architecture)
	switch {
	case current == hadomain.ArchitectureDualMaster && target == hadomain.ArchitectureMasterSlave:
		return "dual_to_master_slave"
	case current == hadomain.ArchitectureMasterSlave && target == hadomain.ArchitectureDualMaster:
		return "master_slave_to_dual"
	default:
		return ""
	}
}

func architectureConversionPlanSteps(req hadomain.ArchitectureAdjustmentRequest) []hadomain.ArchitecturePlanStep {
	kind := architectureTransitionKind(req)
	if kind == "" {
		return nil
	}
	freezeName, freezeDescription := "锁定当前写入口", "短暂阻止新业务连接，建立一致的拓扑变更边界"
	reconfigureName, reconfigureDescription := "建立双向复制", "保留当前主库，补充反向复制和双主自增参数；不重复选举或提升现有主库"
	if kind == "dual_to_master_slave" {
		freezeName, freezeDescription = "隔离待降级主节点", "仅冻结将降为从库的主节点，不影响保留主节点的角色"
		reconfigureName, reconfigureDescription = "降级为从库", "清理待降级节点的旧复制通道并指向保留主库；不重复提升保留主库"
	}
	items := []hadomain.ArchitecturePlanStep{
		{Code: "acquire_lock", Name: "获取集群切换锁", Description: "阻止并发架构调整和 VIP 漂移"},
		{Code: "preflight", Name: "实时预检", Description: "确认 Agent、GTID、复制线程和 MHA 管理通道可用"},
		{Code: "freeze_business_access", Name: freezeName, Description: freezeDescription, Destructive: true},
		{Code: "drain_business_sessions", Name: "排空受影响业务会话", Description: "保留管理与复制连接，清理受影响节点上的业务会话", Destructive: true},
		{Code: "wait_replication_zero", Name: "等待复制追平", Description: "确认目标节点已执行完 relay log 且 GTID 无缺口"},
		{Code: "reconfigure_topology", Name: reconfigureName, Description: reconfigureDescription, Destructive: true},
		{Code: "verify_topology", Name: "验证目标拓扑", Description: "验证复制方向、线程、只读状态和双主自增参数"},
		{Code: "pt_verify_replication", Name: "PT 数据一致性验证", Description: "验证拓扑转换后各实例业务数据一致"},
	}
	if req.MoveVIP {
		items = append(items,
			hadomain.ArchitecturePlanStep{Code: "check_vip_conflict", Name: "扫描 VIP 持有者", Description: "确认 VIP 当前不存在多节点持有"},
			hadomain.ArchitecturePlanStep{Code: "move_vip", Name: "迁移 VIP", Description: "执行零持有者屏障后将 VIP 迁移到保留主节点", Destructive: true},
			hadomain.ArchitecturePlanStep{Code: "verify_single_vip", Name: "防脑裂复核", Description: "确认 VIP 最终仅由目标主节点持有"},
		)
	}
	items = append(items,
		hadomain.ArchitecturePlanStep{Code: "resume_business_connections", Name: "恢复业务访问", Description: "全部拓扑与 VIP 校验通过后恢复业务连接"},
		hadomain.ArchitecturePlanStep{Code: "release_lock", Name: "释放切换锁", Description: "记录审计结果并释放集群锁"},
	)
	for index := range items {
		items[index].Order = index + 1
	}
	return items
}

func reqVIPStepName(req hadomain.ArchitectureAdjustmentRequest) string {
	if req.InitializeVIP {
		return "绑定 VIP"
	}
	return "漂移 VIP"
}

func addArchitectureManagementRepairStep(items []hadomain.ArchitecturePlanStep, req hadomain.ArchitectureAdjustmentRequest) []hadomain.ArchitecturePlanStep {
	if strings.TrimSpace(req.RootPassword) == "" || len(req.RootPasswords) > 0 {
		return items
	}
	repair := hadomain.ArchitecturePlanStep{
		Code: "repair_management_privileges", Name: "修复 MHA 管理权限",
		Description: "仅使用一次 root 凭据补齐旧实例的 MHA 管理权限，后续步骤切回 Agent 保存的 MHA 账号",
	}
	insertAt := 1
	if len(items) < insertAt {
		insertAt = len(items)
	}
	items = append(items, hadomain.ArchitecturePlanStep{})
	copy(items[insertAt+1:], items[insertAt:])
	items[insertAt] = repair
	return items
}
