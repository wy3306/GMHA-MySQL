package menu

import (
	"bufio"
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"gmha/internal/app"
	taskusecase "gmha/internal/usecase/task"
)

// TopologyMenu 是集群架构搭建的交互式菜单，负责 MySQL 主从/双主拓扑的创建、调整和查看。
type TopologyMenu struct {
	core *app.App
}

// NewTopologyMenu 创建一个新的 TopologyMenu 实例。
func NewTopologyMenu(core *app.App) *TopologyMenu {
	return &TopologyMenu{core: core}
}

// Run 运行架构搭建菜单的主循环，显示菜单选项并处理用户选择。
func (m *TopologyMenu) Run(reader *bufio.Reader) error {
	for {
		fmt.Println()
		fmt.Println("==== 架构搭建 ====")
		fmt.Println("1. 新建集群架构")
		fmt.Println("2. 调整集群架构")
		fmt.Println("3. 主从切换主备角色")
		fmt.Println("4. 新增从库并通过 Clone 加入")
		fmt.Println("5. 查看当前集群架构")
		fmt.Println("0. 返回上级")
		fmt.Print("请选择: ")
		line, err := reader.ReadString('\n')
		if err != nil {
			return err
		}
		switch trim(line) {
		case "1":
			if err := m.BuildNewClusterTopology(reader); err != nil {
				printError(err)
			}
		case "2":
			if err := m.AdjustClusterTopology(reader); err != nil {
				printError(err)
			}
		case "3":
			if err := m.SwitchMasterSlave(reader); err != nil {
				printError(err)
			}
		case "4":
			if err := m.AddReplicaWithClone(reader); err != nil {
				printError(err)
			}
		case "5":
			if err := m.ShowClusterTopology(reader); err != nil {
				printError(err)
			}
		case "0", "esc", "ESC":
			return nil
		default:
			fmt.Println("无效选项")
		}
	}
}

// ShowClusterTopology 显示指定集群的当前拓扑结构。
func (m *TopologyMenu) ShowClusterTopology(reader *bufio.Reader) error {
	cluster, err := selectClusterName(m.core, reader, "选择集群序号/名称")
	if err != nil {
		return backAsNil(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	view, err := m.buildClusterTopologyView(ctx, cluster)
	if err != nil {
		return err
	}
	printClusterTopologyView(view)
	return nil
}

// BuildNewClusterTopology 引导用户创建新的集群架构，不执行 Clone，只配置复制关系。
func (m *TopologyMenu) BuildNewClusterTopology(reader *bufio.Reader) error {
	topology, nodes, primary, err := m.collectRoleAssignedTopology(reader)
	if err != nil {
		return backAsNil(err)
	}
	return m.createTopologyTasksWithOptions(reader, topology, nodes, primary, topologyTaskOptions{
		UseCloneMode:  cloneModeDisabled,
		ConfirmAction: "将新建集群架构；新建流程不会执行 Clone，只配置复制关系",
	})
}

// AdjustClusterTopology 引导用户调整现有集群架构，支持 Clone 同步。
func (m *TopologyMenu) AdjustClusterTopology(reader *bufio.Reader) error {
	topology, nodes, primary, err := m.collectRoleAssignedTopology(reader)
	if err != nil {
		return backAsNil(err)
	}
	return m.createTopologyTasksWithOptions(reader, topology, nodes, primary, topologyTaskOptions{
		ConfirmAction: "将调整集群架构",
	})
}

// collectRoleAssignedTopology 收集用户对集群中各 MySQL 实例的角色分配（M/S）。
func (m *TopologyMenu) collectRoleAssignedTopology(reader *bufio.Reader) (string, []taskusecase.CreateMySQLTopologyNodeRequest, string, error) {
	cluster, err := selectClusterName(m.core, reader, "选择集群序号/名称")
	if err != nil {
		return "", nil, "", err
	}
	instances, err := m.listClusterTopologyInstances(cluster)
	if err != nil {
		return "", nil, "", err
	}
	printClusterTopologyInstances(cluster, instances)
	fmt.Println("角色分配示例：1=M,2=S,3=S,4=S 或 DB-01:3306=M,192.168.139.194:3306=S")
	text, err := promptMenu(reader, "请输入每个实例角色，M=主库，S=从库，多个用逗号分隔")
	if err != nil {
		return "", nil, "", err
	}
	nodes, masters, err := parseTopologyInstanceRoleAssignments(text, instances)
	if err != nil {
		return "", nil, "", err
	}
	topology := taskusecase.MySQLTopologyMasterSlave
	if len(masters) == 2 {
		topology = taskusecase.MySQLTopologyMultiMaster
	}
	primary := masters[0].Endpoint()
	if len(masters) == 2 {
		fmt.Println("双主架构主源/种子主库候选：")
		for i, item := range masters {
			fmt.Printf("%d. %s | %s\n", i+1, item.Name, item.Endpoint())
		}
		primaryText, err := promptMenuWithDefault(reader, "选择从库挂载和 Clone 使用的主源/种子主库序号/名称/IP:Port", masters[0].Endpoint())
		if err != nil {
			return "", nil, "", err
		}
		seed, err := resolveTopologyInstanceToken(masters, primaryText)
		if err != nil {
			return "", nil, "", err
		}
		primary = seed.Endpoint()
		nodes, err = assignReplicaSources(reader, nodes, instances, masters, seed)
		if err != nil {
			return "", nil, "", err
		}
	}
	return topology, nodes, primary, nil
}

// SwitchMasterSlave 引导用户执行主从切换，将从库提升为主库。
func (m *TopologyMenu) SwitchMasterSlave(reader *bufio.Reader) error {
	cluster, err := selectClusterName(m.core, reader, "选择集群序号/名称")
	if err != nil {
		return backAsNil(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	view, err := m.buildClusterTopologyView(ctx, cluster)
	if err != nil {
		return err
	}
	printClusterTopologyView(view)
	if countTopologyMasters(view) != 1 {
		return fmt.Errorf("主备切换仅支持当前为单主主从的架构")
	}
	replicas := topologyReplicaInstanceViews(view)
	if len(replicas) == 0 {
		return fmt.Errorf("当前架构未发现可提升的从库")
	}
	fmt.Println("可提升为新主库的从库：")
	for i, item := range replicas {
		fmt.Printf("%d. %s | %s\n", i+1, item.Name, item.Endpoint())
	}
	text, err := promptMenu(reader, "选择新主库序号/名称/IP:Port")
	if err != nil {
		return backAsNil(err)
	}
	promoted, err := resolveTopologyInstanceToken(replicas, text)
	if err != nil {
		return err
	}
	nodes := make([]taskusecase.CreateMySQLTopologyNodeRequest, 0, len(view.Nodes))
	for _, node := range view.Nodes {
		role := "S"
		if node.MachineID == promoted.ID {
			role = "M"
		}
		nodes = append(nodes, taskusecase.CreateMySQLTopologyNodeRequest{Machine: node.IP, Port: node.Port, Role: role})
	}
	return m.createTopologyTasksWithOptions(reader, taskusecase.MySQLTopologyMasterSlave, nodes, promoted.Endpoint(), topologyTaskOptions{
		Port:          promoted.Port,
		UseCloneMode:  cloneModeDisabled,
		ConfirmAction: fmt.Sprintf("将把 %s(%s) 提升为主库，其它节点切为从库", promoted.Name, promoted.Endpoint()),
	})
}

// AddReplicaWithClone 引导用户新增从库并通过 Clone 从主库同步数据。
func (m *TopologyMenu) AddReplicaWithClone(reader *bufio.Reader) error {
	cluster, err := selectClusterName(m.core, reader, "选择集群序号/名称")
	if err != nil {
		return backAsNil(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	view, err := m.buildClusterTopologyView(ctx, cluster)
	if err != nil {
		return err
	}
	printClusterTopologyView(view)
	masters := topologyMasterInstanceViews(view)
	if len(masters) == 0 || len(masters) > 2 {
		return fmt.Errorf("新增从库需要当前架构存在 1 到 2 个主库")
	}
	fmt.Println("从库 Clone 源主库候选：")
	for i, item := range masters {
		fmt.Printf("%d. %s | %s\n", i+1, item.Name, item.Endpoint())
	}
	sourceText, err := promptMenuWithDefault(reader, "选择新从库的源主库序号/名称/IP:Port", masters[0].Endpoint())
	if err != nil {
		return backAsNil(err)
	}
	source, err := resolveTopologyInstanceToken(masters, sourceText)
	if err != nil {
		return err
	}
	clusterInstances, err := m.listClusterTopologyInstances(cluster)
	if err != nil {
		return err
	}
	candidates := excludeTopologyInstances(clusterInstances, topologyParticipantIDs(view))
	if len(candidates) == 0 {
		return fmt.Errorf("集群中没有可新增的机器；请先纳管机器、分配到集群并安装 MySQL")
	}
	printClusterTopologyInstances("可新增为从库", candidates)
	text, err := promptMenu(reader, "选择新增从库序号/名称/IP:Port，多个用逗号分隔")
	if err != nil {
		return backAsNil(err)
	}
	newReplicas, err := resolveTopologyInstanceTokens(candidates, text)
	if err != nil {
		return err
	}
	nodes := make([]taskusecase.CreateMySQLTopologyNodeRequest, 0, len(masters)+len(newReplicas))
	topology := taskusecase.MySQLTopologyMasterSlave
	if len(masters) == 2 {
		topology = taskusecase.MySQLTopologyMultiMaster
		for _, master := range masters {
			nodes = append(nodes, taskusecase.CreateMySQLTopologyNodeRequest{Machine: master.IP, Port: master.Port, Role: "M"})
		}
	} else {
		nodes = append(nodes, taskusecase.CreateMySQLTopologyNodeRequest{Machine: source.IP, Port: source.Port, Role: "M"})
	}
	for _, replica := range newReplicas {
		nodes = append(nodes, taskusecase.CreateMySQLTopologyNodeRequest{Machine: replica.IP, Port: replica.Port, Role: "S", SourceMachine: source.Endpoint()})
	}
	return m.createTopologyTasksWithOptions(reader, topology, nodes, source.Endpoint(), topologyTaskOptions{
		Port:          source.Port,
		UseCloneMode:  cloneModeForced,
		CloneTargets:  topologyInstanceEndpoints(newReplicas),
		ConfirmAction: fmt.Sprintf("将新增 %d 台从库，并从 %s(%s) Clone 数据", len(newReplicas), source.Name, source.Endpoint()),
	})
}

// createTopologyTasks 创建拓扑搭建任务（使用默认选项）。
func (m *TopologyMenu) createTopologyTasks(reader *bufio.Reader, topology string, nodes []taskusecase.CreateMySQLTopologyNodeRequest, primaryMachine string) error {
	return m.createTopologyTasksWithOptions(reader, topology, nodes, primaryMachine, topologyTaskOptions{})
}

// cloneMode 定义 Clone 同步模式的枚举类型。
type cloneMode int

const (
	cloneModePrompt   cloneMode = iota // 交互式询问是否使用 Clone
	cloneModeDisabled                  // 禁用 Clone
	cloneModeForced                    // 强制使用 Clone
)

// topologyTaskOptions 定义拓扑任务创建的可选配置项。
type topologyTaskOptions struct {
	Port          int
	UseCloneMode  cloneMode
	CloneTargets  []string
	ConfirmAction string
}

// createTopologyTasksWithOptions 使用指定选项创建拓扑搭建任务，包括密码、复制账号、Clone 配置等。
func (m *TopologyMenu) createTopologyTasksWithOptions(reader *bufio.Reader, topology string, nodes []taskusecase.CreateMySQLTopologyNodeRequest, primaryMachine string, opts topologyTaskOptions) error {
	port := opts.Port
	if port <= 0 {
		for _, node := range nodes {
			if node.Port > 0 {
				port = node.Port
				break
			}
		}
		if port <= 0 {
			port = 3306
		}
	}
	rootPassword, err := promptMenu(reader, "root 密码")
	if err != nil {
		return backAsNil(err)
	}
	replUser, err := promptMenuWithDefault(reader, "复制账号", "repl")
	if err != nil {
		return backAsNil(err)
	}
	replPassword, err := promptMenuWithDefault(reader, "复制账号密码", "3306niubi")
	if err != nil {
		return backAsNil(err)
	}
	useClone := false
	switch opts.UseCloneMode {
	case cloneModeDisabled:
		fmt.Println("Clone 同步：no")
	case cloneModeForced:
		fmt.Println("Clone 同步：yes")
		useClone = true
	default:
		useCloneText, err := promptMenuWithDefault(reader, "主库已有数据时使用 Clone 同步 yes/no", "yes")
		if err != nil {
			return backAsNil(err)
		}
		useClone = isMenuYes(useCloneText)
	}
	cloneUser := "clone"
	clonePassword := "3306niubi"
	if useClone {
		cloneUser, err = promptMenuWithDefault(reader, "Clone 账号", cloneUser)
		if err != nil {
			return backAsNil(err)
		}
		clonePassword, err = promptMenuWithDefault(reader, "Clone 账号密码", clonePassword)
		if err != nil {
			return backAsNil(err)
		}
	}
	parallelType, err := promptMenuWithDefault(reader, "slave/replica parallel type", "LOGICAL_CLOCK")
	if err != nil {
		return backAsNil(err)
	}
	parallelWorkers, err := promptMenuIntWithDefault(reader, "parallel workers", 4)
	if err != nil {
		return backAsNil(err)
	}
	action := opts.ConfirmAction
	if strings.TrimSpace(action) == "" {
		action = fmt.Sprintf("将创建 %d 个 MySQL 架构搭建任务，会修改 my.cnf、重启 MySQL，并按选择执行复制/Clone", len(nodes))
	} else {
		action = fmt.Sprintf("%s；将创建 %d 个 MySQL 架构搭建任务，会修改 my.cnf、重启 MySQL", action, len(nodes))
	}
	confirm, err := confirmYES(reader, action)
	if err != nil {
		return backAsNil(err)
	}
	if !confirm {
		fmt.Println("已取消架构搭建。")
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	result, err := m.core.TaskService.CreateMySQLTopologyTasks(ctx, taskusecase.CreateMySQLTopologyTaskRequest{
		Topology:            topology,
		Port:                port,
		RootPassword:        rootPassword,
		ReplicationUser:     replUser,
		ReplicationPassword: replPassword,
		CloneUser:           cloneUser,
		ClonePassword:       clonePassword,
		UseClone:            useClone,
		PrimaryMachine:      primaryMachine,
		CloneSeedMachine:    primaryMachine,
		CloneTargetMachines: opts.CloneTargets,
		ParallelType:        parallelType,
		ParallelWorkers:     parallelWorkers,
		Nodes:               nodes,
	})
	if err != nil {
		return err
	}
	taskIDs := make([]string, 0, len(result.Tasks))
	for _, detail := range result.Tasks {
		fmt.Printf("架构搭建任务已创建：%s (%s) task=%s\n", emptyAsDash(detail.MachineName), emptyAsDash(detail.MachineIP), detail.Task.ID)
		taskIDs = append(taskIDs, detail.Task.ID)
	}
	if len(taskIDs) == 1 {
		return watchTask(m.core, reader, taskIDs[0])
	}
	return watchTaskGroup(m.core, reader, "MySQL 架构搭建进度", taskIDs)
}

// listClusterMachineViews 获取指定集群的机器视图列表。
func (m *TopologyMenu) listClusterMachineViews(cluster string) ([]machineView, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	items, err := m.core.MachineService.ListMachines(ctx)
	if err != nil {
		return nil, err
	}
	views := make([]machineView, 0, len(items))
	for _, item := range items {
		if item.Cluster != cluster {
			continue
		}
		views = append(views, machineView{
			ID:      item.ID,
			Name:    item.Name,
			IP:      item.IP,
			SSHPort: item.SSHPort,
			SSHUser: item.SSHUser,
			Cluster: item.Cluster,
			Status:  string(item.Status),
		})
	}
	if len(views) == 0 {
		return nil, fmt.Errorf("集群 %s 暂无机器", cluster)
	}
	return views, nil
}

// printClusterMachines 打印集群中的机器列表。
func printClusterMachines(cluster string, machines []machineView) {
	fmt.Printf("集群 %s 机器：\n", cluster)
	for i, item := range machines {
		fmt.Printf("%d. %s | %s:%d | 用户=%s | 状态=%s\n", i+1, item.Name, item.IP, item.SSHPort, item.SSHUser, item.Status)
	}
}

// topologyInstanceView 是 MySQL 实例在拓扑菜单中的视图，包含展示所需的基本信息。
type topologyInstanceView struct {
	ID        string
	Name      string
	IP        string
	Port      int
	Cluster   string
	Status    string
	Heartbeat string
	UpdatedAt string
}

// Endpoint 返回实例的 IP:Port 端点地址。
func (v topologyInstanceView) Endpoint() string {
	return fmt.Sprintf("%s:%d", v.IP, v.Port)
}

// NameEndpoint 返回实例的 名称:Port 端点地址。
func (v topologyInstanceView) NameEndpoint() string {
	return fmt.Sprintf("%s:%d", v.Name, v.Port)
}

// listClusterTopologyInstances 获取指定集群中所有 MySQL 实例的拓扑视图。
func (m *TopologyMenu) listClusterTopologyInstances(cluster string) ([]topologyInstanceView, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	items, err := m.core.MySQLService.ListInstanceViews(ctx)
	if err != nil {
		return nil, err
	}
	views := make([]topologyInstanceView, 0, len(items))
	for _, item := range items {
		if item.Cluster != cluster {
			continue
		}
		views = append(views, topologyInstanceView{
			ID:        item.MachineID,
			Name:      item.MachineName,
			IP:        item.MachineIP,
			Port:      item.Port,
			Cluster:   item.Cluster,
			Status:    item.Status,
			Heartbeat: item.HeartbeatStatus,
			UpdatedAt: item.UpdatedAt.Local().Format("2006-01-02 15:04:05"),
		})
	}
	if len(views) == 0 {
		return nil, fmt.Errorf("集群 %s 暂无 MySQL 实例", cluster)
	}
	return views, nil
}

// printClusterTopologyInstances 打印集群中的 MySQL 实例列表。
func printClusterTopologyInstances(cluster string, instances []topologyInstanceView) {
	fmt.Printf("集群 %s MySQL 实例：\n", cluster)
	for i, item := range instances {
		fmt.Printf("%d. %s | %s | 状态=%s | 心跳=%s | 更新时间=%s\n", i+1, item.Name, item.Endpoint(), emptyAsDash(item.Status), emptyAsDash(item.Heartbeat), emptyAsDash(item.UpdatedAt))
	}
}

// selectMySQLInstances 展示所有 MySQL 实例并引导用户选择多个实例。
func selectMySQLInstances(core *app.App, reader *bufio.Reader, label string) ([]topologyInstanceView, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	items, err := core.MySQLService.ListInstanceViews(ctx)
	if err != nil {
		return nil, err
	}
	instances := make([]topologyInstanceView, 0, len(items))
	for _, item := range items {
		instances = append(instances, topologyInstanceView{
			ID:        item.MachineID,
			Name:      item.MachineName,
			IP:        item.MachineIP,
			Port:      item.Port,
			Cluster:   item.Cluster,
			Status:    item.Status,
			Heartbeat: item.HeartbeatStatus,
			UpdatedAt: item.UpdatedAt.Local().Format("2006-01-02 15:04:05"),
		})
	}
	if len(instances) == 0 {
		return nil, fmt.Errorf("暂无 MySQL 实例")
	}
	printClusterTopologyInstances("全部", instances)
	text, err := promptMenu(reader, label)
	if err != nil {
		return nil, err
	}
	return resolveTopologyInstanceTokens(instances, text)
}

// parseTopologyInstanceRoleAssignments 解析用户输入的实例角色分配（如 1=M,2=S,3=S）。
func parseTopologyInstanceRoleAssignments(input string, instances []topologyInstanceView) ([]taskusecase.CreateMySQLTopologyNodeRequest, []topologyInstanceView, error) {
	tokens := splitCommaInput(input)
	if len(tokens) == 0 {
		return nil, nil, fmt.Errorf("未输入角色分配")
	}
	roleByInstance := make(map[string]string, len(instances))
	for _, token := range tokens {
		selector, role, err := splitRoleAssignment(token)
		if err != nil {
			return nil, nil, err
		}
		item, err := resolveTopologyInstanceToken(instances, selector)
		if err != nil {
			return nil, nil, err
		}
		key := topologyInstanceKey(item)
		if _, exists := roleByInstance[key]; exists {
			return nil, nil, fmt.Errorf("实例 %s 重复分配角色", item.Endpoint())
		}
		roleByInstance[key] = role
	}
	if len(roleByInstance) != len(instances) {
		missing := make([]string, 0, len(instances)-len(roleByInstance))
		for _, item := range instances {
			if _, ok := roleByInstance[topologyInstanceKey(item)]; !ok {
				missing = append(missing, item.Name+"("+item.Endpoint()+")")
			}
		}
		return nil, nil, fmt.Errorf("必须为集群中所有 MySQL 实例分配角色，缺少: %s", strings.Join(missing, ", "))
	}
	nodes := make([]taskusecase.CreateMySQLTopologyNodeRequest, 0, len(instances))
	masters := make([]topologyInstanceView, 0, 2)
	slaveCount := 0
	for _, item := range instances {
		role := roleByInstance[topologyInstanceKey(item)]
		nodes = append(nodes, taskusecase.CreateMySQLTopologyNodeRequest{Machine: item.IP, Port: item.Port, Role: role})
		if role == "M" {
			masters = append(masters, item)
		} else {
			slaveCount++
		}
	}
	if len(masters) == 0 {
		return nil, nil, fmt.Errorf("至少需要 1 个主库实例 M")
	}
	if len(masters) > 2 {
		return nil, nil, fmt.Errorf("一个集群最多只能指定 2 个主库实例 M")
	}
	if len(masters) == 1 && slaveCount == 0 {
		return nil, nil, fmt.Errorf("普通主从至少需要 1 个从库实例 S")
	}
	return nodes, masters, nil
}

// parseTopologyRoleAssignments 解析用户输入的机器角色分配（如 db-01=M,db-02=S）。
func parseTopologyRoleAssignments(input string, machines []machineView) ([]taskusecase.CreateMySQLTopologyNodeRequest, []machineView, error) {
	tokens := splitCommaInput(input)
	if len(tokens) == 0 {
		return nil, nil, fmt.Errorf("未输入角色分配")
	}
	roleByMachine := make(map[string]string, len(machines))
	for _, token := range tokens {
		selector, role, err := splitRoleAssignment(token)
		if err != nil {
			return nil, nil, err
		}
		item, err := resolveMachineViewToken(machines, selector)
		if err != nil {
			return nil, nil, err
		}
		if _, exists := roleByMachine[item.ID]; exists {
			return nil, nil, fmt.Errorf("机器 %s 重复分配角色", item.Name)
		}
		roleByMachine[item.ID] = role
	}
	if len(roleByMachine) != len(machines) {
		missing := make([]string, 0, len(machines)-len(roleByMachine))
		for _, item := range machines {
			if _, ok := roleByMachine[item.ID]; !ok {
				missing = append(missing, item.Name+"("+item.IP+")")
			}
		}
		return nil, nil, fmt.Errorf("必须为集群中所有机器分配角色，缺少: %s", strings.Join(missing, ", "))
	}
	nodes := make([]taskusecase.CreateMySQLTopologyNodeRequest, 0, len(machines))
	masters := make([]machineView, 0, 2)
	slaveCount := 0
	for _, item := range machines {
		role := roleByMachine[item.ID]
		nodes = append(nodes, taskusecase.CreateMySQLTopologyNodeRequest{Machine: item.IP, Role: role})
		if role == "M" {
			masters = append(masters, item)
		} else {
			slaveCount++
		}
	}
	if len(masters) == 0 {
		return nil, nil, fmt.Errorf("至少需要 1 台主库 M")
	}
	if len(masters) > 2 {
		return nil, nil, fmt.Errorf("一个集群最多只能指定 2 台主库 M")
	}
	if len(masters) == 1 && slaveCount == 0 {
		return nil, nil, fmt.Errorf("普通主从至少需要 1 台从库 S")
	}
	return nodes, masters, nil
}

// assignReplicaSources 为双主架构中的从库分配上游主库。
func assignReplicaSources(reader *bufio.Reader, nodes []taskusecase.CreateMySQLTopologyNodeRequest, instances []topologyInstanceView, masters []topologyInstanceView, primary topologyInstanceView) ([]taskusecase.CreateMySQLTopologyNodeRequest, error) {
	replicas := make([]topologyInstanceView, 0)
	for i, node := range nodes {
		if node.Role != "S" {
			continue
		}
		instance, err := resolveTopologyInstanceToken(instances, fmt.Sprintf("%s:%d", node.Machine, node.Port))
		if err != nil {
			return nil, err
		}
		replicas = append(replicas, instance)
		nodes[i].SourceMachine = primary.Endpoint()
	}
	if len(replicas) == 0 || len(masters) < 2 {
		return nodes, nil
	}
	mode, err := promptMenuWithDefault(reader, "双主从库上游选择：1=全部挂主源，2=逐台选择", "1")
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(mode) != "2" {
		return nodes, nil
	}
	for i, node := range nodes {
		if node.Role != "S" {
			continue
		}
		replica, err := resolveTopologyInstanceToken(instances, fmt.Sprintf("%s:%d", node.Machine, node.Port))
		if err != nil {
			return nil, err
		}
		fmt.Printf("从库 %s(%s) 上游主库候选：\n", replica.Name, replica.Endpoint())
		for idx, master := range masters {
			fmt.Printf("%d. %s | %s\n", idx+1, master.Name, master.Endpoint())
		}
		text, err := promptMenuWithDefault(reader, "选择上游主库序号/名称/IP:Port", primary.Endpoint())
		if err != nil {
			return nil, err
		}
		source, err := resolveTopologyInstanceToken(masters, text)
		if err != nil {
			return nil, err
		}
		nodes[i].SourceMachine = source.Endpoint()
	}
	return nodes, nil
}

// splitRoleAssignment 解析角色分配表达式（如 "db-01=M"），返回选择器和角色。
func splitRoleAssignment(token string) (string, string, error) {
	token = strings.TrimSpace(token)
	parts := strings.SplitN(token, "=", 2)
	if len(parts) != 2 {
		if idx := strings.LastIndex(token, ":"); idx > 0 {
			parts = []string{token[:idx], token[idx+1:]}
		}
	}
	if len(parts) != 2 {
		return "", "", fmt.Errorf("角色分配 %s 无效，应使用 机器=M 或 机器=S", token)
	}
	selector := strings.TrimSpace(parts[0])
	role := strings.ToUpper(strings.TrimSpace(parts[1]))
	if selector == "" {
		return "", "", fmt.Errorf("角色分配 %s 缺少机器", token)
	}
	if role != "M" && role != "S" {
		return "", "", fmt.Errorf("机器 %s 的角色必须是 M 或 S", selector)
	}
	return selector, role, nil
}

// selectClusterMachines 展示集群中的机器列表并引导用户选择多台机器。
func (m *TopologyMenu) selectClusterMachines(reader *bufio.Reader, cluster string, label string) ([]machineView, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	items, err := m.core.MachineService.ListMachines(ctx)
	if err != nil {
		return nil, err
	}
	views := make([]machineView, 0, len(items))
	fmt.Printf("集群 %s 机器：\n", cluster)
	for _, item := range items {
		if item.Cluster != cluster {
			continue
		}
		view := machineView{
			ID:      item.ID,
			Name:    item.Name,
			IP:      item.IP,
			SSHPort: item.SSHPort,
			SSHUser: item.SSHUser,
			Cluster: item.Cluster,
			Status:  string(item.Status),
		}
		views = append(views, view)
		fmt.Printf("%d. %s | %s:%d | 用户=%s | 状态=%s\n", len(views), view.Name, view.IP, view.SSHPort, view.SSHUser, view.Status)
	}
	if len(views) == 0 {
		return nil, fmt.Errorf("集群 %s 暂无机器", cluster)
	}
	text, err := prompt(reader, label)
	if err != nil {
		return nil, err
	}
	tokens := splitCommaInput(text)
	if len(tokens) == 0 {
		return nil, fmt.Errorf("未选择机器")
	}
	selected := make([]machineView, 0, len(tokens))
	seen := make(map[string]bool, len(tokens))
	for _, token := range tokens {
		item, err := resolveMachineViewToken(views, token)
		if err != nil {
			return nil, err
		}
		if seen[item.ID] {
			continue
		}
		seen[item.ID] = true
		selected = append(selected, item)
	}
	return selected, nil
}

// resolveMachineViewToken 根据序号、名称、IP 或 ID 从机器列表中解析单台机器。
func resolveMachineViewToken(items []machineView, token string) (machineView, error) {
	token = strings.TrimSpace(token)
	if idx, err := strconv.Atoi(token); err == nil {
		if idx < 1 || idx > len(items) {
			return machineView{}, fmt.Errorf("无效机器序号 %s", token)
		}
		return items[idx-1], nil
	}
	for _, item := range items {
		if item.IP == token || item.Name == token || item.ID == token {
			return item, nil
		}
	}
	return machineView{}, fmt.Errorf("未找到机器 %s", token)
}

// resolveMachineViewTokens 根据逗号分隔的输入从机器列表中解析多台机器。
func resolveMachineViewTokens(items []machineView, input string) ([]machineView, error) {
	tokens := splitCommaInput(input)
	if len(tokens) == 0 {
		return nil, fmt.Errorf("未选择机器")
	}
	selected := make([]machineView, 0, len(tokens))
	seen := make(map[string]bool, len(tokens))
	for _, token := range tokens {
		item, err := resolveMachineViewToken(items, token)
		if err != nil {
			return nil, err
		}
		if seen[item.ID] {
			continue
		}
		seen[item.ID] = true
		selected = append(selected, item)
	}
	return selected, nil
}

// resolveTopologyInstanceToken 根据序号、IP:Port、名称:Port 等从实例列表中解析单个 MySQL 实例。
func resolveTopologyInstanceToken(items []topologyInstanceView, token string) (topologyInstanceView, error) {
	token = strings.TrimSpace(token)
	if idx, err := strconv.Atoi(token); err == nil {
		if idx < 1 || idx > len(items) {
			return topologyInstanceView{}, fmt.Errorf("无效实例序号 %s", token)
		}
		return items[idx-1], nil
	}
	matches := make([]topologyInstanceView, 0, 1)
	for _, item := range items {
		if item.Endpoint() == token || item.NameEndpoint() == token || item.ID == token {
			return item, nil
		}
		if item.IP == token || item.Name == token {
			matches = append(matches, item)
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) > 1 {
		return topologyInstanceView{}, fmt.Errorf("实例 %s 匹配到多个端口，请使用 IP:Port 或 名称:Port", token)
	}
	return topologyInstanceView{}, fmt.Errorf("未找到 MySQL 实例 %s", token)
}

// resolveTopologyInstanceTokens 根据逗号分隔的输入从实例列表中解析多个 MySQL 实例。
func resolveTopologyInstanceTokens(items []topologyInstanceView, input string) ([]topologyInstanceView, error) {
	tokens := splitCommaInput(input)
	if len(tokens) == 0 {
		return nil, fmt.Errorf("未选择 MySQL 实例")
	}
	selected := make([]topologyInstanceView, 0, len(tokens))
	seen := make(map[string]bool, len(tokens))
	for _, token := range tokens {
		item, err := resolveTopologyInstanceToken(items, token)
		if err != nil {
			return nil, err
		}
		key := topologyInstanceKey(item)
		if seen[key] {
			continue
		}
		seen[key] = true
		selected = append(selected, item)
	}
	return selected, nil
}

// topologyInstanceKey 返回实例的唯一键，格式为 "ID:Port"。
func topologyInstanceKey(item topologyInstanceView) string {
	return fmt.Sprintf("%s:%d", item.ID, item.Port)
}

// topologyParticipantIDs 获取拓扑视图中所有参与复制关系的节点 ID 集合。
func topologyParticipantIDs(view clusterTopologyView) map[string]bool {
	out := make(map[string]bool, len(view.Nodes))
	for _, node := range view.Nodes {
		if node.Incoming || node.Outgoing {
			out[topologyNodeKey(node)] = true
		}
	}
	return out
}

// excludeTopologyInstances 从实例列表中排除已指定的实例。
func excludeTopologyInstances(items []topologyInstanceView, excluded map[string]bool) []topologyInstanceView {
	out := make([]topologyInstanceView, 0, len(items))
	for _, item := range items {
		if !excluded[topologyInstanceKey(item)] {
			out = append(out, item)
		}
	}
	return out
}

// topologyInstanceEndpoints 返回实例列表中所有实例的端点地址切片。
func topologyInstanceEndpoints(items []topologyInstanceView) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, item.Endpoint())
	}
	return out
}

// topologyNodeKey 返回拓扑节点的唯一键，格式为 "MachineID:Port"。
func topologyNodeKey(node clusterTopologyNode) string {
	return fmt.Sprintf("%s:%d", node.MachineID, node.Port)
}

// countTopologyMasters 统计拓扑视图中主库节点的数量。
func countTopologyMasters(view clusterTopologyView) int {
	count := 0
	for _, node := range view.Nodes {
		if node.Outgoing {
			count++
		}
	}
	return count
}

// topologyMasterInstanceViews 从拓扑视图中提取所有主库实例。
func topologyMasterInstanceViews(view clusterTopologyView) []topologyInstanceView {
	out := make([]topologyInstanceView, 0, 2)
	for _, node := range view.Nodes {
		if !node.Outgoing {
			continue
		}
		out = append(out, topologyInstanceView{ID: node.MachineID, Name: node.Name, IP: node.IP, Port: node.Port})
	}
	return out
}

// topologyReplicaInstanceViews 从拓扑视图中提取所有从库实例。
func topologyReplicaInstanceViews(view clusterTopologyView) []topologyInstanceView {
	out := make([]topologyInstanceView, 0)
	for _, node := range view.Nodes {
		if node.Incoming && !node.Outgoing {
			out = append(out, topologyInstanceView{ID: node.MachineID, Name: node.Name, IP: node.IP, Port: node.Port})
		}
	}
	return out
}

// clusterTopologyView 表示集群拓扑的完整视图，包含节点和复制边。
type clusterTopologyView struct {
	Cluster string
	Nodes   []clusterTopologyNode
	Edges   []clusterTopologyEdge
}

// clusterTopologyNode 表示拓扑视图中的一个 MySQL 实例节点。
type clusterTopologyNode struct {
	MachineID   string
	Name        string
	IP          string
	Port        int
	Role        string
	ServerID    int
	Incoming    bool
	Outgoing    bool
	ReadOnly    string
	SuperRO     string
	Heartbeat   string
	LastUpdated string
	Error       string
}

// clusterTopologyEdge 表示拓扑视图中的一条复制关系边（源 -> 目标）。
type clusterTopologyEdge struct {
	SourceIP   string
	SourcePort int
	TargetIP   string
	TargetPort int
	SourceName string
	TargetName string
	IORunning  string
	SQLRunning string
	Lag        string
	LastError  string
}

// buildClusterTopologyView 构建集群的完整拓扑视图，包括节点信息和复制关系。
func (m *TopologyMenu) buildClusterTopologyView(ctx context.Context, cluster string) (clusterTopologyView, error) {
	instances, err := m.core.MySQLService.ListInstanceViews(ctx)
	if err != nil {
		return clusterTopologyView{}, err
	}
	view := clusterTopologyView{Cluster: cluster}
	for _, instance := range instances {
		if instance.Cluster != cluster {
			continue
		}
		node := clusterTopologyNode{
			MachineID:   instance.MachineID,
			Name:        instance.MachineName,
			IP:          instance.MachineIP,
			Port:        instance.Port,
			Role:        "standalone",
			ServerID:    instance.ServerID,
			Heartbeat:   instance.HeartbeatStatus,
			LastUpdated: instance.UpdatedAt.Local().Format("2006-01-02 15:04:05"),
		}
		view.Nodes = append(view.Nodes, node)
	}
	if len(view.Nodes) == 0 {
		return clusterTopologyView{}, fmt.Errorf("集群 %s 中未找到 MySQL 实例", cluster)
	}
	nodeByEndpoint := map[string]*clusterTopologyNode{}
	for i := range view.Nodes {
		nodeByEndpoint[clusterNodeEndpoint(view.Nodes[i].IP, view.Nodes[i].Port)] = &view.Nodes[i]
	}
	for i := range view.Nodes {
		node := &view.Nodes[i]
		metrics, err := m.core.MachineService.GetMySQLDynamicMetrics(ctx, clusterNodeEndpoint(node.IP, node.Port))
		if err != nil {
			node.Error = err.Error()
			continue
		}
		for _, metric := range metrics.Metrics {
			switch metric.Name {
			case "mysql_server_id":
				if serverID, ok := intFromMetricValue(metric.Value); ok {
					node.ServerID = serverID
				}
			case "mysql_read_only":
				node.ReadOnly = metricValueDisplay(metric.Value)
			case "mysql_super_read_only":
				node.SuperRO = metricValueDisplay(metric.Value)
			case "mysql_replication_thread_status":
				edge, ok := replicationEdgeFromMetric(node, metric.Value)
				if ok {
					view.Edges = append(view.Edges, edge)
				}
			}
		}
	}
	incoming := map[string]bool{}
	outgoing := map[string]bool{}
	for i := range view.Edges {
		if source, ok := nodeByEndpoint[clusterNodeEndpoint(view.Edges[i].SourceIP, view.Edges[i].SourcePort)]; ok {
			view.Edges[i].SourceName = source.Name
			outgoing[topologyNodeKey(*source)] = true
			source.Outgoing = true
		}
		if target, ok := nodeByEndpoint[clusterNodeEndpoint(view.Edges[i].TargetIP, view.Edges[i].TargetPort)]; ok {
			view.Edges[i].TargetName = target.Name
			incoming[topologyNodeKey(*target)] = true
			target.Incoming = true
		}
	}
	for i := range view.Nodes {
		node := &view.Nodes[i]
		key := topologyNodeKey(*node)
		switch {
		case incoming[key] && outgoing[key]:
			node.Role = "M/S"
		case outgoing[key]:
			node.Role = "M"
		case incoming[key]:
			node.Role = "S"
		default:
			if strings.EqualFold(node.ReadOnly, "true") || strings.EqualFold(node.ReadOnly, "ON") {
				node.Role = "readonly"
			} else {
				node.Role = "standalone"
			}
		}
	}
	return view, nil
}

// replicationEdgeFromMetric 从动态指标中解析复制关系边。
func replicationEdgeFromMetric(node *clusterTopologyNode, value any) (clusterTopologyEdge, bool) {
	status := mapFromAny(value)
	if len(status) == 0 {
		return clusterTopologyEdge{}, false
	}
	replicaStatus := mapFromAny(status["replica_status"])
	if len(replicaStatus) == 0 {
		return clusterTopologyEdge{}, false
	}
	sourceIP := firstAnyString(replicaStatus, "Source_Host", "Master_Host")
	if sourceIP == "" {
		return clusterTopologyEdge{}, false
	}
	sourcePort := node.Port
	if port, ok := intFromMetricValue(firstAnyValue(replicaStatus, "Source_Port", "Master_Port")); ok && port > 0 {
		sourcePort = port
	}
	return clusterTopologyEdge{
		SourceIP:   sourceIP,
		SourcePort: sourcePort,
		TargetIP:   node.IP,
		TargetPort: node.Port,
		TargetName: node.Name,
		IORunning:  firstAnyString(status, "io_running"),
		SQLRunning: firstAnyString(status, "sql_running"),
		Lag:        firstAnyString(status, "lag_seconds"),
		LastError:  firstAnyString(status, "last_error"),
	}, true
}

// printClusterTopologyView 打印集群拓扑视图，包括节点信息和复制关系。
func printClusterTopologyView(view clusterTopologyView) {
	fmt.Printf("集群架构：%s\n", view.Cluster)
	fmt.Println()
	headers := []string{"角色", "机器名", "实例", "server_id", "read_only", "super_read_only", "心跳", "更新时间", "错误"}
	rows := make([][]string, 0, len(view.Nodes))
	for _, node := range view.Nodes {
		rows = append(rows, []string{
			emptyAsDash(node.Role),
			emptyAsDash(node.Name),
			clusterNodeEndpoint(node.IP, node.Port),
			strconv.Itoa(node.ServerID),
			emptyAsDash(node.ReadOnly),
			emptyAsDash(node.SuperRO),
			emptyAsDash(node.Heartbeat),
			emptyAsDash(node.LastUpdated),
			summarizeError(node.Error),
		})
	}
	printAlignedTable(headers, rows)
	fmt.Println()
	if len(view.Edges) == 0 {
		fmt.Println("拓扑关系：未发现复制链路，当前按独立节点展示。")
		return
	}
	fmt.Println("拓扑关系：")
	for _, edge := range view.Edges {
		sourceEndpoint := clusterNodeEndpoint(edge.SourceIP, edge.SourcePort)
		source := sourceEndpoint
		if strings.TrimSpace(edge.SourceName) != "" {
			source = fmt.Sprintf("%s(%s)", edge.SourceName, sourceEndpoint)
		}
		targetEndpoint := clusterNodeEndpoint(edge.TargetIP, edge.TargetPort)
		target := targetEndpoint
		if strings.TrimSpace(edge.TargetName) != "" {
			target = fmt.Sprintf("%s(%s)", edge.TargetName, targetEndpoint)
		}
		fmt.Printf("%s -> %s  IO=%s SQL=%s Lag=%s Error=%s\n", source, target, emptyAsDash(edge.IORunning), emptyAsDash(edge.SQLRunning), emptyAsDash(edge.Lag), emptyAsDash(summarizeError(edge.LastError)))
	}
}

// clusterNodeEndpoint 生成节点的 IP:Port 端点地址。
func clusterNodeEndpoint(ip string, port int) string {
	if port <= 0 {
		return ip
	}
	return fmt.Sprintf("%s:%d", ip, port)
}

// mapFromAny 将任意值转换为 map[string]any 类型。
func mapFromAny(value any) map[string]any {
	out := map[string]any{}
	switch typed := value.(type) {
	case map[string]any:
		return typed
	case map[string]string:
		for k, v := range typed {
			out[k] = v
		}
	}
	return out
}

// firstAnyString 从 map 中按优先级查找第一个非空字符串值。
func firstAnyString(items map[string]any, keys ...string) string {
	for _, key := range keys {
		if v := metricValueDisplay(items[key]); strings.TrimSpace(v) != "" && v != "-" {
			return v
		}
	}
	return ""
}

// firstAnyValue 从 map 中按优先级查找第一个存在的值。
func firstAnyValue(items map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, ok := items[key]; ok {
			return value
		}
	}
	return nil
}

// metricValueDisplay 将指标值转换为显示用的字符串。
func metricValueDisplay(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case bool:
		return strconv.FormatBool(typed)
	case int:
		return strconv.Itoa(typed)
	case int64:
		return strconv.FormatInt(typed, 10)
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	default:
		return fmt.Sprintf("%v", typed)
	}
}

// intFromMetricValue 从指标值中提取整数，支持多种数值类型和字符串。
func intFromMetricValue(value any) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, true
	case int64:
		return int(typed), true
	case float64:
		return int(typed), true
	case string:
		v, err := strconv.Atoi(strings.TrimSpace(typed))
		return v, err == nil
	default:
		return 0, false
	}
}
