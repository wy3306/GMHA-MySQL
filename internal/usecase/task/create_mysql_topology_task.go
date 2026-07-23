package task

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	agentdomain "gmha/internal/domain/agent"
	machinedomain "gmha/internal/domain/machine"
	taskdomain "gmha/internal/domain/task"
	mysqlapp "gmha/internal/mysql"
)

// MySQLTopologyMasterSlave 表示主从复制拓扑类型。
// MySQLTopologyMultiMaster 表示双主复制拓扑类型。
const (
	MySQLTopologyMasterSlave = "master_slave"
	MySQLTopologyMultiMaster = "multi_master"
)

// MySQLTopologyInstanceRepository 定义了拓扑任务所需的 MySQL 实例仓储接口。
type MySQLTopologyInstanceRepository interface {
	Get(ctx context.Context, machineID string, port int) (mysqlapp.Instance, bool, error)
}

// CreateMySQLTopologyTaskRequest 是创建 MySQL 拓扑任务的请求参数。
type CreateMySQLTopologyTaskRequest struct {
	Topology            string
	Port                int
	RootPassword        string
	ReplicationUser     string
	ReplicationPassword string
	CloneUser           string
	ClonePassword       string
	UseClone            bool
	PrimaryMachine      string
	CloneSeedMachine    string
	CloneTargetMachines []string
	ParallelType        string
	ParallelWorkers     int
	Nodes               []CreateMySQLTopologyNodeRequest
}

// CreateMySQLTopologyNodeRequest 是拓扑节点的请求参数。
type CreateMySQLTopologyNodeRequest struct {
	Machine       string
	Port          int
	Role          string
	SourceMachine string
	DelaySeconds  int
}

// CreateMySQLTopologyTaskResult 是创建 MySQL 拓扑任务的结果，包含多个任务及其步骤和事件。
type CreateMySQLTopologyTaskResult struct {
	Tasks  []taskdomain.Task
	Steps  map[string][]taskdomain.Step
	Events map[string][]taskdomain.Event
}

// CreateMySQLTopologyTaskUsecase 是创建 MySQL 拓扑任务的用例，负责验证节点、分配角色和构建复制任务。
type CreateMySQLTopologyTaskUsecase struct {
	machines  MachineRepository
	agents    AgentRepository
	instances MySQLTopologyInstanceRepository
}

// NewCreateMySQLTopologyTaskUsecase 创建一个新的 MySQL 拓扑任务用例实例。
func NewCreateMySQLTopologyTaskUsecase(machines MachineRepository, agents AgentRepository, instances MySQLTopologyInstanceRepository) *CreateMySQLTopologyTaskUsecase {
	return &CreateMySQLTopologyTaskUsecase{machines: machines, agents: agents, instances: instances}
}

// Execute 执行创建 MySQL 拓扑任务的完整流程，包括验证参数、解析节点、分配复制源和构建任务。
func (u *CreateMySQLTopologyTaskUsecase) Execute(ctx context.Context, req CreateMySQLTopologyTaskRequest) (CreateMySQLTopologyTaskResult, error) {
	if u.instances == nil {
		return CreateMySQLTopologyTaskResult{}, errors.New("mysql instance repository not configured")
	}
	req = normalizeTopologyRequest(req)
	if req.Port <= 0 {
		return CreateMySQLTopologyTaskResult{}, errors.New("port is required")
	}
	if strings.TrimSpace(req.RootPassword) == "" {
		return CreateMySQLTopologyTaskResult{}, errors.New("root_password is required")
	}
	if len(req.Nodes) < 2 {
		return CreateMySQLTopologyTaskResult{}, errors.New("at least two mysql nodes are required")
	}

	resolved, err := u.resolveTopologyNodes(ctx, req)
	if err != nil {
		return CreateMySQLTopologyTaskResult{}, err
	}
	if req.UseClone {
		for _, item := range resolved {
			if !mysqlapp.SupportsCloneForVersion(item.spec.Version) {
				return CreateMySQLTopologyTaskResult{}, fmt.Errorf("MySQL Clone is unavailable on MySQL %s node %s; Clone requires MySQL 8.0.17 or newer, so disable use_clone and initialize the replica from a physical backup", item.spec.Version, item.machine.Name)
			}
		}
	}
	ensureUniqueTopologyServerIDs(resolved)
	if err := assignTopologySources(req.Topology, req.Port, req.PrimaryMachine, resolved); err != nil {
		return CreateMySQLTopologyTaskResult{}, err
	}
	if err := assignCloneTargets(req, resolved); err != nil {
		return CreateMySQLTopologyTaskResult{}, err
	}

	now := time.Now().UTC()
	result := CreateMySQLTopologyTaskResult{
		Steps:  make(map[string][]taskdomain.Step, len(resolved)),
		Events: make(map[string][]taskdomain.Event, len(resolved)),
	}
	allNodes := make([]taskdomain.MySQLTopologyNodeSpec, 0, len(resolved))
	for _, item := range resolved {
		allNodes = append(allNodes, item.spec)
	}
	for idx, item := range resolved {
		specPort := item.spec.Port
		if specPort <= 0 {
			specPort = req.Port
		}
		spec := taskdomain.MySQLTopologySpec{
			Topology:            req.Topology,
			Port:                specPort,
			RootPassword:        req.RootPassword,
			ReplicationUser:     req.ReplicationUser,
			ReplicationPassword: req.ReplicationPassword,
			CloneUser:           req.CloneUser,
			ClonePassword:       req.ClonePassword,
			UseClone:            req.UseClone,
			PrimaryMachine:      req.PrimaryMachine,
			CloneSeedMachine:    req.CloneSeedMachine,
			ParallelType:        req.ParallelType,
			ParallelWorkers:     req.ParallelWorkers,
			Node:                item.spec,
			Nodes:               allNodes,
		}
		specJSON, _ := json.Marshal(spec)
		taskID := fmt.Sprintf("task-%d-%d", now.UnixNano(), idx+1)
		task := taskdomain.Task{
			ID:              taskID,
			Type:            taskdomain.TypeMySQLTopology,
			MachineID:       item.machine.ID,
			AgentID:         item.agent.ID,
			Status:          taskdomain.StatusPending,
			ProgressPercent: 0,
			CurrentStep:     "等待派发",
			SpecJSON:        specJSON,
			CreatedAt:       now,
		}
		steps := buildMySQLTopologySteps(taskID)
		events := []taskdomain.Event{{
			ID:        fmt.Sprintf("task-event-%d-%d", now.UnixNano(), idx+1),
			TaskID:    taskID,
			StepID:    steps[0].ID,
			EventType: taskdomain.EventInfo,
			Content:   "mysql_topology task created",
			CreatedAt: now,
		}}
		result.Tasks = append(result.Tasks, task)
		result.Steps[taskID] = steps
		result.Events[taskID] = events
	}
	return result, nil
}

// resolvedTopologyNode 是解析后的拓扑节点，包含机器、Agent 和任务规格信息。
type resolvedTopologyNode struct {
	machine         machinedomain.Machine
	agent           agentdomain.Agent
	requestedSource string
	spec            taskdomain.MySQLTopologyNodeSpec
}

// normalizeTopologyRequest 对拓扑请求参数进行标准化，填充默认值。
func normalizeTopologyRequest(req CreateMySQLTopologyTaskRequest) CreateMySQLTopologyTaskRequest {
	req.Topology = strings.TrimSpace(req.Topology)
	if req.Topology == "" {
		req.Topology = MySQLTopologyMasterSlave
	}
	if req.Port <= 0 {
		req.Port = 3306
	}
	if strings.TrimSpace(req.ReplicationUser) == "" {
		req.ReplicationUser = "repl"
	}
	if strings.TrimSpace(req.ReplicationPassword) == "" {
		req.ReplicationPassword = "3306niubi"
	}
	if strings.TrimSpace(req.CloneUser) == "" {
		req.CloneUser = "clone"
	}
	if strings.TrimSpace(req.ClonePassword) == "" {
		req.ClonePassword = "3306niubi"
	}
	if strings.TrimSpace(req.ParallelType) == "" {
		req.ParallelType = "LOGICAL_CLOCK"
	}
	if req.ParallelWorkers <= 0 {
		req.ParallelWorkers = 4
	}
	return req
}

// resolveTopologyNodes 解析拓扑请求中的所有节点，验证机器、Agent 和 MySQL 实例状态。
func (u *CreateMySQLTopologyTaskUsecase) resolveTopologyNodes(ctx context.Context, req CreateMySQLTopologyTaskRequest) ([]resolvedTopologyNode, error) {
	resolver := &CreateExecTaskUsecase{machines: u.machines, agents: u.agents}
	out := make([]resolvedTopologyNode, 0, len(req.Nodes))
	seen := make(map[string]bool, len(req.Nodes))
	masterCount := 0
	for i, node := range req.Nodes {
		target := strings.TrimSpace(node.Machine)
		if target == "" {
			return nil, fmt.Errorf("node %d machine is required", i+1)
		}
		role := strings.ToUpper(strings.TrimSpace(node.Role))
		if role != "M" && role != "S" {
			return nil, fmt.Errorf("node %s role must be M or S", target)
		}
		if role == "M" {
			masterCount++
		}
		machine, ok, err := resolver.resolveMachine(ctx, target)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("machine %s not found", target)
		}
		if seen[machine.ID] {
			return nil, fmt.Errorf("machine %s selected more than once", target)
		}
		seen[machine.ID] = true
		agent, ok, err := u.agents.GetByMachineID(ctx, machine.ID)
		if err != nil {
			return nil, err
		}
		if !ok || agent.State != agentdomain.StateOnline {
			return nil, fmt.Errorf("machine %s requires online agent", target)
		}
		port := node.Port
		if port <= 0 {
			port = req.Port
		}
		instance, ok, err := u.instances.Get(ctx, machine.ID, port)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("machine %s has no mysql instance on port %d", target, port)
		}
		serverID := instance.ServerID
		if serverID <= 0 {
			serverID = i + 1
		}
		version := strings.TrimSpace(instance.Version)
		if version == "" {
			version, _ = mysqlapp.PackageVersion(instance.PackageName)
		}
		out = append(out, resolvedTopologyNode{
			machine:         machine,
			agent:           agent,
			requestedSource: strings.TrimSpace(node.SourceMachine),
			spec: taskdomain.MySQLTopologyNodeSpec{
				MachineID:                machine.ID,
				MachineName:              machine.Name,
				IP:                       machine.IP,
				Port:                     instance.Port,
				Version:                  version,
				Role:                     role,
				ServerID:                 serverID,
				ReplicationDelaySeconds:  max(node.DelaySeconds, 0),
				AutoIncrementOffset:      1,
				AutoIncrementIncrement:   1,
				InstanceDir:              instance.InstanceDir,
				DataDir:                  instance.DataDir,
				BaseDir:                  instance.BaseDir,
				MySQLUser:                instance.MySQLUser,
				MyCnfPath:                instance.MyCnfPath,
				SocketPath:               instance.SocketPath,
				SystemdUnitName:          instance.SystemdUnit,
				ResetServerUUID:          true,
				ReadOnly:                 role == "S",
				SuperReadOnly:            role == "S",
				RequiresReplicationSetup: role == "S",
			},
		})
	}
	switch req.Topology {
	case MySQLTopologyMasterSlave:
		if masterCount != 1 {
			return nil, errors.New("ordinary master-slave requires exactly one M")
		}
		if len(out)-masterCount == 0 {
			return nil, errors.New("ordinary master-slave requires at least one S")
		}
	case MySQLTopologyMultiMaster:
		if masterCount != 2 {
			return nil, errors.New("M-M topology requires exactly two M nodes")
		}
	default:
		return nil, fmt.Errorf("unsupported topology %s", req.Topology)
	}
	return out, nil
}

// assignTopologySources 根据拓扑类型为各节点分配复制源。
func assignTopologySources(topology string, port int, primaryMachine string, nodes []resolvedTopologyNode) error {
	switch topology {
	case MySQLTopologyMasterSlave:
		var master taskdomain.MySQLTopologyNodeSpec
		for _, node := range nodes {
			if node.spec.Role == "M" {
				master = node.spec
				break
			}
		}
		for i := range nodes {
			if nodes[i].spec.Role != "S" {
				continue
			}
			nodes[i].spec.SourceMachineID = master.MachineID
			nodes[i].spec.SourceMachineName = master.MachineName
			nodes[i].spec.SourceIP = master.IP
			nodes[i].spec.SourcePort = master.Port
			nodes[i].spec.RequiresReplicationSetup = true
		}
	case MySQLTopologyMultiMaster:
		masters := make([]int, 0, 2)
		for i := range nodes {
			if nodes[i].spec.Role == "M" {
				masters = append(masters, i)
			}
		}
		if len(masters) != 2 {
			return errors.New("M-M topology requires exactly two M nodes")
		}
		primaryIdx := masters[0]
		if strings.TrimSpace(primaryMachine) != "" {
			primary, ok := findResolvedTopologyNodeIndex(nodes, primaryMachine)
			if !ok {
				return fmt.Errorf("primary machine %s not found", primaryMachine)
			}
			if nodes[primary].spec.Role != "M" {
				return fmt.Errorf("primary machine %s must be M", primaryMachine)
			}
			primaryIdx = primary
		}
		primary := nodes[primaryIdx].spec
		for order, nodeIdx := range masters {
			peerIdx := masters[0]
			if nodeIdx == masters[0] {
				peerIdx = masters[1]
			}
			peer := nodes[peerIdx].spec
			nodes[nodeIdx].spec.SourceMachineID = peer.MachineID
			nodes[nodeIdx].spec.SourceMachineName = peer.MachineName
			nodes[nodeIdx].spec.SourceIP = peer.IP
			nodes[nodeIdx].spec.SourcePort = peer.Port
			nodes[nodeIdx].spec.AutoIncrementIncrement = 2
			nodes[nodeIdx].spec.AutoIncrementOffset = order + 1
			nodes[nodeIdx].spec.ReadOnly = false
			nodes[nodeIdx].spec.SuperReadOnly = false
			nodes[nodeIdx].spec.RequiresReplicationSetup = true
		}
		for i := range nodes {
			if nodes[i].spec.Role != "S" {
				continue
			}
			source := primary
			if nodes[i].requestedSource != "" {
				sourceIdx, ok := findResolvedTopologyNodeIndex(nodes, nodes[i].requestedSource)
				if !ok {
					return fmt.Errorf("replica source machine %s not found", nodes[i].requestedSource)
				}
				if nodes[sourceIdx].spec.Role != "M" {
					return fmt.Errorf("replica source machine %s must be M", nodes[i].requestedSource)
				}
				source = nodes[sourceIdx].spec
			}
			nodes[i].spec.SourceMachineID = source.MachineID
			nodes[i].spec.SourceMachineName = source.MachineName
			nodes[i].spec.SourceIP = source.IP
			nodes[i].spec.SourcePort = source.Port
			nodes[i].spec.ReadOnly = true
			nodes[i].spec.SuperReadOnly = true
			nodes[i].spec.RequiresReplicationSetup = true
		}
	}
	return nil
}

// assignCloneTargets 根据请求配置为节点分配克隆目标。
func assignCloneTargets(req CreateMySQLTopologyTaskRequest, nodes []resolvedTopologyNode) error {
	if !req.UseClone {
		return nil
	}
	if len(req.CloneTargetMachines) > 0 {
		targets := make(map[string]bool, len(req.CloneTargetMachines))
		for _, target := range req.CloneTargetMachines {
			node, ok := findResolvedTopologyNode(nodes, strings.TrimSpace(target))
			if !ok {
				return fmt.Errorf("clone target machine %s not found", target)
			}
			targets[node.machine.ID] = true
		}
		for i := range nodes {
			nodes[i].spec.RequiresClone = targets[nodes[i].machine.ID]
		}
		return nil
	}
	switch req.Topology {
	case MySQLTopologyMasterSlave:
		for i := range nodes {
			nodes[i].spec.RequiresClone = nodes[i].spec.Role == "S"
		}
	case MySQLTopologyMultiMaster:
		seed := strings.TrimSpace(req.PrimaryMachine)
		if seed == "" {
			seed = strings.TrimSpace(req.CloneSeedMachine)
		}
		if seed == "" {
			return errors.New("clone_seed_machine is required for M-M clone")
		}
		seedMachine, ok := findResolvedTopologyNode(nodes, seed)
		if !ok {
			return fmt.Errorf("clone seed machine %s not found", seed)
		}
		if seedMachine.spec.Role != "M" {
			return fmt.Errorf("clone seed machine %s must be M", seed)
		}
		for i := range nodes {
			nodes[i].spec.RequiresClone = nodes[i].machine.ID != seedMachine.machine.ID
			if nodes[i].spec.RequiresClone {
				nodes[i].spec.SourceMachineID = seedMachine.spec.MachineID
				nodes[i].spec.SourceMachineName = seedMachine.spec.MachineName
				nodes[i].spec.SourceIP = seedMachine.spec.IP
				nodes[i].spec.SourcePort = req.Port
			}
		}
	}
	return nil
}

// findResolvedTopologyNode 根据选择器查找匹配的拓扑节点。
func findResolvedTopologyNode(nodes []resolvedTopologyNode, selector string) (resolvedTopologyNode, bool) {
	for _, node := range nodes {
		if matchesResolvedTopologyNode(node, selector) {
			return node, true
		}
	}
	return resolvedTopologyNode{}, false
}

// findResolvedTopologyNodeIndex 根据选择器查找匹配的拓扑节点索引。
func findResolvedTopologyNodeIndex(nodes []resolvedTopologyNode, selector string) (int, bool) {
	for i, node := range nodes {
		if matchesResolvedTopologyNode(node, selector) {
			return i, true
		}
	}
	return -1, false
}

// matchesResolvedTopologyNode 判断拓扑节点是否匹配给定的选择器。
func matchesResolvedTopologyNode(node resolvedTopologyNode, selector string) bool {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return false
	}
	endpoint := fmt.Sprintf("%s:%d", node.machine.IP, node.spec.Port)
	nameEndpoint := fmt.Sprintf("%s:%d", node.machine.Name, node.spec.Port)
	return node.machine.ID == selector ||
		node.machine.IP == selector ||
		node.machine.Name == selector ||
		endpoint == selector ||
		nameEndpoint == selector
}

// ensureUniqueTopologyServerIDs 确保所有拓扑节点的 Server ID 唯一。
func ensureUniqueTopologyServerIDs(nodes []resolvedTopologyNode) {
	seen := make(map[int]bool, len(nodes))
	duplicate := false
	for _, node := range nodes {
		if node.spec.ServerID <= 0 || seen[node.spec.ServerID] {
			duplicate = true
			break
		}
		seen[node.spec.ServerID] = true
	}
	if !duplicate {
		return
	}
	for i := range nodes {
		nodes[i].spec.ServerID = i + 1
	}
}

// buildMySQLTopologySteps 构建 MySQL 拓扑任务的所有步骤。
func buildMySQLTopologySteps(taskID string) []taskdomain.Step {
	names := []string{
		"check_mysql",
		"configure_mycnf",
		"restart_mysql",
		"prepare_replication_accounts",
		"clone_from_source",
		"configure_replication",
		"verify_replication",
	}
	steps := make([]taskdomain.Step, 0, len(names))
	now := time.Now().UTC().UnixNano()
	for i, name := range names {
		steps = append(steps, taskdomain.Step{
			ID:       fmt.Sprintf("task-step-%d-%d", now, i+1),
			TaskID:   taskID,
			StepNo:   i + 1,
			StepName: name,
			Status:   taskdomain.StepPending,
			Message:  "等待 Agent 执行",
		})
	}
	return steps
}
