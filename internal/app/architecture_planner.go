package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	hadomain "gmha/internal/domain/ha"
	machinedomain "gmha/internal/domain/machine"
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
	selected, ranked, selectErr := NewCandidateSelector().Select(scores)
	plan := hadomain.ArchitectureAdjustmentPlan{
		PlanID: "arch-" + strings.TrimPrefix(newFailoverID(), "fo-"), ClusterID: clusterID,
		Architecture: req.Architecture, RankedCandidates: ranked,
		WaitDelayTimeoutSeconds: 60, RequiresForceConfirmation: true,
		CreatedAt: time.Now().UTC(), Executable: true,
	}
	if selectErr != nil {
		plan.Executable = false
		plan.BlockingReasons = append(plan.BlockingReasons, selectErr.Error())
	} else {
		plan.SelectedCandidate = selected
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
		case hadomain.VipRouteModeKeepalived:
			if s.tasks == nil {
				plan.Executable = false
				plan.BlockingReasons = append(plan.BlockingReasons, "Keepalived driver and dual-node health script are not configured")
			}
			if req.Architecture != hadomain.ArchitectureKeepalivedDualMaster {
				plan.Executable = false
				plan.BlockingReasons = append(plan.BlockingReasons, "Keepalived VIP requires keepalived_dual_master architecture")
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
	case hadomain.ArchitectureMasterSlave, hadomain.ArchitectureDualMaster, hadomain.ArchitectureMultiMaster, hadomain.ArchitectureKeepalivedDualMaster:
	default:
		return fmt.Errorf("unsupported architecture %s", req.Architecture)
	}
	masters := 0
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
		if role != "M" && role != "S" {
			return fmt.Errorf("node %s role must be M or S", node.MachineID)
		}
		if role == "M" {
			masters++
			if node.DelaySeconds > 0 {
				return fmt.Errorf("master candidate %s cannot be a delayed replica", node.MachineID)
			}
		}
	}
	if req.CurrentMasterMachineID != "" && !seen[req.CurrentMasterMachineID] {
		return errors.New("current master must be included in target nodes")
	}
	if req.MoveVIP && req.CurrentMasterMachineID == "" {
		return errors.New("VIP migration requires the current master so business sessions can be drained and the old holder fenced")
	}
	if req.MoveVIP && req.PreferredNewMasterMachineID == req.CurrentMasterMachineID {
		return errors.New("VIP migration requires a different target master; the current master already owns the traffic endpoint")
	}
	if req.Architecture == hadomain.ArchitectureMasterSlave && masters != 1 {
		return errors.New("master_slave requires exactly one master")
	}
	if (req.Architecture == hadomain.ArchitectureDualMaster || req.Architecture == hadomain.ArchitectureKeepalivedDualMaster) && masters != 2 {
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
		if !strings.EqualFold(strings.TrimSpace(node.Role), "M") {
			score.Eligible = false
			score.RejectReasons = append(score.RejectReasons, "target role is replica and cannot be promoted")
		}
		if req.CurrentMasterMachineID != "" && node.MachineID == req.CurrentMasterMachineID && req.PreferredNewMasterMachineID != req.CurrentMasterMachineID {
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
		{"fence_old_master", "隔离旧主", "先撤销写能力和旧 VIP；无法确认隔离成功时禁止继续", true, false},
		{"promote_new_master", "提升新主", "停止并重置复制，关闭只读，再次验证只有一个可写主节点", true, false},
		{"repoint_replicas", "重定向复制关系", "按目标架构配置一主多从、双主及延时从库", true, false},
		{"verify_topology", "验证新拓扑", "验证复制线程、GTID、只读状态和延时从库参数", false, false},
	}
	if req.MoveVIP {
		definitions = append(definitions,
			struct {
				code, name, description string
				destructive, confirm    bool
			}{"move_vip", "迁移 VIP", "确认旧节点无 VIP 后绑定新主，并通过 ARP、BGP 或 Keepalived 宣告", true, false},
			struct {
				code, name, description string
				destructive, confirm    bool
			}{"verify_single_vip", "防脑裂复核", "从所有节点和外部探测点确认 VIP 仅由新主持有", false, false},
		)
	}
	definitions = append(definitions,
		struct {
			code, name, description string
			destructive, confirm    bool
		}{"pt_repair_on_failure", "按需 PT 修复", "仅在强制切主后的复制重建失败时，校验兼容版本并使用 pt-table-checksum/pt-table-sync 修复", true, true},
		struct {
			code, name, description string
			destructive, confirm    bool
		}{"release_lock", "释放切换锁", "记录审计结果并释放集群锁", false, false},
	)
	steps := make([]hadomain.ArchitecturePlanStep, 0, len(definitions))
	for index, item := range definitions {
		steps = append(steps, hadomain.ArchitecturePlanStep{Order: index + 1, Code: item.code, Name: item.name, Description: item.description, Destructive: item.destructive, RequiresConfirmation: item.confirm})
	}
	return steps
}
