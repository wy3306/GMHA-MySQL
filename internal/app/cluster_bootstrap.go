package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	hadomain "gmha/internal/domain/ha"
	taskdomain "gmha/internal/domain/task"
	taskusecase "gmha/internal/usecase/task"
)

// ClusterBootstrapInstall 描述组合流程中的单个 MySQL 安装目标。
type ClusterBootstrapInstall struct {
	Machine           string                        `json:"machine"`
	MachineID         string                        `json:"machine_id"`
	Version           string                        `json:"version"`
	Architecture      string                        `json:"architecture"`
	Port              int                           `json:"port"`
	ServerID          int                           `json:"server_id"`
	MySQLUser         string                        `json:"mysql_user"`
	RootPassword      string                        `json:"root_password"`
	Profile           string                        `json:"profile"`
	InstanceDir       string                        `json:"instance_dir"`
	DataDir           string                        `json:"data_dir"`
	BinlogDir         string                        `json:"binlog_dir"`
	RedoDir           string                        `json:"redo_dir"`
	UndoDir           string                        `json:"undo_dir"`
	TmpDir            string                        `json:"tmp_dir"`
	BaseDir           string                        `json:"base_dir"`
	MyCnfPath         string                        `json:"my_cnf_path"`
	SocketPath        string                        `json:"socket_path"`
	ErrorLog          string                        `json:"error_log"`
	PIDFile           string                        `json:"pid_file"`
	CharacterSetsDir  string                        `json:"character_sets_dir"`
	PluginDir         string                        `json:"plugin_dir"`
	InstallPTTools    bool                          `json:"install_pt_tools"`
	InstallXtraBackup bool                          `json:"install_xtrabackup"`
	MemoryAllocator   string                        `json:"memory_allocator"`
	RuntimeParameters map[string]string             `json:"runtime_parameters"`
	Accounts          []taskdomain.MySQLAccountSpec `json:"accounts"`
}

// ClusterBootstrapRequest 把批量安装、复制拓扑和 VIP 编排成一个 Manager 父任务。
type ClusterBootstrapRequest struct {
	Architecture             string                    `json:"architecture"`
	PrimaryMachineID         string                    `json:"primary_machine_id"`
	SecondaryMasterMachineID string                    `json:"secondary_master_machine_id,omitempty"`
	EnableVIP                bool                      `json:"enable_vip"`
	VIP                      hadomain.ClusterVIPConfig `json:"vip"`
	Installs                 []ClusterBootstrapInstall `json:"installs"`
	replicationUser          string
	replicationPassword      string
}

func validateClusterBootstrapRequest(req ClusterBootstrapRequest) error {
	if req.Architecture != hadomain.ArchitectureMasterSlave && req.Architecture != hadomain.ArchitectureDualMaster {
		return errors.New("architecture must be master_slave or dual_master")
	}
	if len(req.Installs) < 2 {
		return errors.New("at least two install targets are required")
	}
	ids := make(map[string]bool, len(req.Installs))
	for _, item := range req.Installs {
		if strings.TrimSpace(item.Machine) == "" || strings.TrimSpace(item.MachineID) == "" {
			return errors.New("every install target requires machine and machine_id")
		}
		if ids[item.MachineID] {
			return fmt.Errorf("machine %s selected more than once", item.MachineID)
		}
		ids[item.MachineID] = true
		if item.Port <= 0 || item.ServerID <= 0 {
			return fmt.Errorf("machine %s requires valid port and server_id", item.MachineID)
		}
		if strings.TrimSpace(item.RootPassword) == "" {
			return errors.New("root_password is required")
		}
	}
	if !ids[req.PrimaryMachineID] {
		return errors.New("primary_machine_id must reference an install target")
	}
	if req.Architecture == hadomain.ArchitectureDualMaster {
		if req.SecondaryMasterMachineID == req.PrimaryMachineID || !ids[req.SecondaryMasterMachineID] {
			return errors.New("dual master requires a different secondary_master_machine_id")
		}
	}
	if _, _, ok := clusterBootstrapMHAAccount(req); !ok {
		return errors.New("an enabled MHA management account with username and password is required")
	}
	if req.EnableVIP && strings.TrimSpace(req.VIP.VIPAddress) == "" {
		return errors.New("vip_address is required when VIP is enabled")
	}
	return nil
}

// StartClusterBootstrap 创建父任务并在后台严格按安装、架构、VIP 顺序执行。
func (s *HAService) StartClusterBootstrap(ctx context.Context, clusterID string, req ClusterBootstrapRequest) (TaskDetail, error) {
	clusterID = strings.TrimSpace(clusterID)
	if clusterID == "" {
		return TaskDetail{}, errors.New("cluster is required")
	}
	if s.tasks == nil {
		return TaskDetail{}, errors.New("task service is not configured")
	}
	if err := validateClusterBootstrapRequest(req); err != nil {
		return TaskDetail{}, err
	}
	req.replicationUser, req.replicationPassword, _ = clusterBootstrapMHAAccount(req)
	taskID := fmt.Sprintf("cluster-bootstrap-%d", time.Now().UTC().UnixNano())
	detail, err := s.tasks.CreateClusterBootstrapTrackingTask(ctx, taskID, clusterID, req.Architecture, len(req.Installs))
	if err != nil {
		return TaskDetail{}, err
	}
	go s.executeClusterBootstrap(context.Background(), taskID, clusterID, req)
	return detail, nil
}

func (s *HAService) executeClusterBootstrap(ctx context.Context, parentID, clusterID string, req ClusterBootstrapRequest) {
	fail := func(step string, err error) {
		_ = s.tasks.UpdateClusterBootstrapStep(ctx, parentID, step, taskdomain.StepFailed, err.Error(), nil)
	}
	_ = s.tasks.UpdateClusterBootstrapStep(ctx, parentID, "create_install_tasks", taskdomain.StepRunning, "正在为每台目标机器创建独立安装任务。", nil)
	installTaskIDs := make([]string, 0, len(req.Installs))
	for _, item := range req.Installs {
		detail, err := s.tasks.CreateMySQLInstallTask(ctx, taskusecase.CreateMySQLInstallTaskRequest{
			ParentTaskID: parentID,
			Machine:      item.Machine, Version: item.Version, Architecture: item.Architecture, Port: item.Port, ServerID: item.ServerID,
			MySQLUser: item.MySQLUser, RootPassword: item.RootPassword, Profile: item.Profile, InstanceDir: item.InstanceDir,
			DataDir: item.DataDir, BinlogDir: item.BinlogDir, RedoDir: item.RedoDir, UndoDir: item.UndoDir, TmpDir: item.TmpDir,
			BaseDir: item.BaseDir, MyCnfPath: item.MyCnfPath, SocketPath: item.SocketPath, ErrorLog: item.ErrorLog, PIDFile: item.PIDFile,
			CharacterSetsDir: item.CharacterSetsDir, PluginDir: item.PluginDir, InstallPTTools: item.InstallPTTools,
			InstallXtraBackup: item.InstallXtraBackup, MemoryAllocator: item.MemoryAllocator,
			RuntimeParameters: item.RuntimeParameters, Accounts: item.Accounts,
		})
		if err != nil {
			fail("create_install_tasks", fmt.Errorf("create install task for %s: %w", item.Machine, err))
			return
		}
		installTaskIDs = append(installTaskIDs, detail.Task.ID)
	}
	_ = s.tasks.UpdateClusterBootstrapStep(ctx, parentID, "create_install_tasks", taskdomain.StepSuccess, fmt.Sprintf("已创建 %d 个安装子任务。", len(installTaskIDs)), installTaskIDs)
	_ = s.tasks.UpdateClusterBootstrapStep(ctx, parentID, "wait_install_tasks", taskdomain.StepRunning, "等待全部 MySQL 安装任务成功。", installTaskIDs)
	if err := s.waitTasks(ctx, installTaskIDs, 2*time.Hour); err != nil {
		_ = s.tasks.UpdateClusterBootstrapStep(ctx, parentID, "wait_install_tasks", taskdomain.StepFailed, err.Error(), installTaskIDs)
		return
	}
	_ = s.tasks.UpdateClusterBootstrapStep(ctx, parentID, "wait_install_tasks", taskdomain.StepSuccess, "全部 MySQL 实例安装成功并已登记。", installTaskIDs)

	if req.EnableVIP {
		_ = s.tasks.UpdateClusterBootstrapStep(ctx, parentID, "save_vip", taskdomain.StepRunning, "正在校验并保存 VIP 配置。", nil)
		req.VIP = s.resolveBootstrapVIPConfig(ctx, clusterID, req.Architecture, req.VIP)
		if _, err := s.SaveVIPConfig(ctx, clusterID, req.VIP); err != nil {
			fail("save_vip", err)
			return
		}
		_ = s.tasks.UpdateClusterBootstrapStep(ctx, parentID, "save_vip", taskdomain.StepSuccess, fmt.Sprintf("VIP %s 配置已保存，系统选择 %s 宣告策略，目标网卡 %s。", req.VIP.VIPAddress, req.VIP.VIPRouteMode, req.VIP.DefaultInterface), nil)
	} else {
		_ = s.tasks.UpdateClusterBootstrapStep(ctx, parentID, "save_vip", taskdomain.StepSuccess, "本次未启用 VIP，已跳过。", nil)
	}

	nodes := make([]hadomain.ArchitectureNodeRequest, 0, len(req.Installs))
	rootPasswords := make(map[string]string, len(req.Installs))
	for _, item := range req.Installs {
		role, source := "S", req.PrimaryMachineID
		if item.MachineID == req.PrimaryMachineID || (req.Architecture == hadomain.ArchitectureDualMaster && item.MachineID == req.SecondaryMasterMachineID) {
			role, source = "M", ""
		}
		nodes = append(nodes, hadomain.ArchitectureNodeRequest{MachineID: item.MachineID, Port: item.Port, Role: role, SourceMachineID: source, ElectionPriority: 50})
		rootPasswords[item.MachineID] = item.RootPassword
	}
	_ = s.tasks.UpdateClusterBootstrapStep(ctx, parentID, "apply_architecture", taskdomain.StepRunning, "正在生成安全计划并应用复制拓扑。", nil)
	run, err := s.StartArchitectureAdjustment(ctx, clusterID, hadomain.ArchitectureAdjustmentRequest{
		Architecture: req.Architecture, PreferredNewMasterMachineID: req.PrimaryMachineID,
		MoveVIP: req.EnableVIP, InitializeVIP: req.EnableVIP,
		ManagementUsers: []string{"root", "monitor", "mha", "backup", "repl"}, RootPassword: req.Installs[0].RootPassword, RootPasswords: rootPasswords,
		ReplicationUser: req.replicationUser, ReplicationPassword: req.replicationPassword, Nodes: nodes,
	})
	if err != nil {
		fail("apply_architecture", err)
		return
	}
	if err := s.tasks.AttachChildTasks(ctx, parentID, []string{run.RunID}); err != nil {
		fail("apply_architecture", fmt.Errorf("attach architecture task: %w", err))
		return
	}
	_ = s.tasks.UpdateClusterBootstrapStep(ctx, parentID, "apply_architecture", taskdomain.StepRunning, "架构调整子流程已启动。", []string{run.RunID})
	if err := s.waitArchitectureRun(ctx, clusterID, run.RunID, 2*time.Hour); err != nil {
		fail("apply_architecture", err)
		return
	}
	_ = s.tasks.UpdateClusterBootstrapStep(ctx, parentID, "apply_architecture", taskdomain.StepSuccess, "主从复制架构已应用。", []string{run.RunID})
	_ = s.tasks.UpdateClusterBootstrapStep(ctx, parentID, "verify", taskdomain.StepRunning, "正在复核拓扑和 VIP 状态。", []string{run.RunID})
	_ = s.tasks.UpdateClusterBootstrapStep(ctx, parentID, "verify", taskdomain.StepSuccess, "批量安装、复制拓扑与 VIP 初始化全部完成。", []string{run.RunID})
}

func clusterBootstrapMHAAccount(req ClusterBootstrapRequest) (string, string, bool) {
	if len(req.Installs) == 0 {
		return "", "", false
	}
	for _, account := range req.Installs[0].Accounts {
		if strings.EqualFold(strings.TrimSpace(account.Role), "mha") && account.Enabled {
			user, password := strings.TrimSpace(account.Username), strings.TrimSpace(account.Password)
			return user, password, user != "" && password != ""
		}
	}
	return "", "", false
}

func (s *HAService) resolveBootstrapVIPConfig(ctx context.Context, clusterID, architecture string, cfg hadomain.ClusterVIPConfig) hadomain.ClusterVIPConfig {
	policy, _ := s.repo.GetNetworkPolicy(ctx, clusterID)
	if policy.VIPRouteMode == hadomain.VipRouteModeBGP {
		if cfg.BGPLocalAS > 0 && cfg.BGPPeerAS > 0 && strings.TrimSpace(cfg.BGPPeerAddress) != "" {
			cfg.VIPRouteMode = hadomain.VipRouteModeBGP
			return cfg
		}
		if existing, err := s.repo.ListVIPConfigs(ctx, clusterID); err == nil {
			for _, template := range existing {
				if template.VIPRouteMode == hadomain.VipRouteModeBGP && template.BGPLocalAS > 0 && template.BGPPeerAS > 0 && strings.TrimSpace(template.BGPPeerAddress) != "" {
					cfg.VIPRouteMode, cfg.BGPLocalAS, cfg.BGPPeerAS = hadomain.VipRouteModeBGP, template.BGPLocalAS, template.BGPPeerAS
					cfg.BGPPeerAddress, cfg.BGPRouterID, cfg.BGPCommunity = template.BGPPeerAddress, template.BGPRouterID, template.BGPCommunity
					return cfg
				}
			}
		}
	}
	cfg.VIPRouteMode = hadomain.VipRouteModeL2ARP
	return cfg
}

func (s *HAService) waitTasks(ctx context.Context, ids []string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		allSuccess := true
		for _, id := range ids {
			detail, err := s.tasks.GetTaskDetail(ctx, id)
			if err != nil {
				return err
			}
			if detail.Task.Status == taskdomain.StatusFailed {
				return errors.New(clusterInstallFailureSummary(detail))
			}
			if detail.Task.Status != taskdomain.StatusSuccess {
				allSuccess = false
			}
		}
		if allSuccess {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return errors.New("timed out waiting for MySQL install tasks")
}

func clusterInstallFailureSummary(detail TaskDetail) string {
	taskID := detail.Task.ID
	target := strings.TrimSpace(detail.MachineName)
	if detail.MachineIP != "" {
		if target != "" {
			target += "（" + detail.MachineIP + "）"
		} else {
			target = detail.MachineIP
		}
	}
	if target == "" {
		target = strings.TrimSpace(detail.Task.MachineID)
	}
	if target == "" {
		target = "未知目标"
	}

	stepName, reason := strings.TrimSpace(detail.Task.CurrentStep), ""
	for i := len(detail.Steps) - 1; i >= 0; i-- {
		step := detail.Steps[i]
		if step.Status != taskdomain.StepFailed {
			continue
		}
		if strings.TrimSpace(step.StepName) != "" {
			stepName = strings.TrimSpace(step.StepName)
		}
		reason = strings.TrimSpace(step.Message)
		break
	}
	for i := len(detail.Events) - 1; i >= 0; i-- {
		event := detail.Events[i]
		if event.EventType != taskdomain.EventError || strings.TrimSpace(event.Content) == "" {
			continue
		}
		eventReason := strings.TrimSpace(event.Content)
		if reason == "" || strings.EqualFold(reason, stepName) {
			reason = eventReason
		} else if !strings.Contains(reason, eventReason) {
			reason += "；ERROR 日志：" + eventReason
		}
		break
	}
	if stepName == "" {
		stepName = "未标明步骤"
	}
	if reason == "" {
		reason = "Agent 未返回具体错误，请检查该任务的 ERROR 日志和 Agent 日志"
	}
	return fmt.Sprintf("MySQL 安装失败：目标 %s；任务 %s；失败步骤 %s；原因：%s", target, taskID, stepName, reason)
}

func (s *HAService) waitArchitectureRun(ctx context.Context, clusterID, runID string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		run, found, err := s.GetArchitectureRun(ctx, clusterID, runID)
		if err != nil {
			return err
		}
		if !found {
			return errors.New("architecture run not found")
		}
		if run.Status == hadomain.ArchitectureRunSucceeded {
			return nil
		}
		if run.Status == hadomain.ArchitectureRunFailed {
			return fmt.Errorf("architecture run failed: %s", run.Error)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return errors.New("timed out waiting for architecture initialization")
}
