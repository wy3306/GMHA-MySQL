package menu

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"gmha/internal/app"
	collectdomain "gmha/internal/collect"
	taskdomain "gmha/internal/domain/task"
	taskusecase "gmha/internal/usecase/task"
)

// MySQLMenu 是 MySQL 管理的交互式菜单，负责展示 MySQL 实例信息并提供安装、卸载、查看等操作入口。
type MySQLMenu struct {
	core *app.App
}

// NewMySQLMenu 创建一个新的 MySQLMenu 实例。
func NewMySQLMenu(core *app.App) *MySQLMenu {
	return &MySQLMenu{core: core}
}

// Run 运行 MySQL 管理菜单的主循环，显示菜单选项并处理用户选择。
func (m *MySQLMenu) Run(reader *bufio.Reader) error {
	for {
		fmt.Println()
		fmt.Println("==== MySQL 管理 ====")
		fmt.Println("1. 安装 MySQL")
		fmt.Println("2. 查看 MySQL 实例")
		fmt.Println("3. 卸载 MySQL")
		fmt.Println("4. 集群一键安装 MySQL")
		fmt.Println("5. 集群一键卸载 MySQL")
		fmt.Println("6. 清理 MySQL 实例记录")
		fmt.Println("7. 查看 MySQL 静态数据")
		fmt.Println("8. 查看 MySQL 动态数据")
		fmt.Println("0. 返回上级")
		fmt.Print("请选择: ")

		line, err := reader.ReadString('\n')
		if err != nil {
			return err
		}
		switch trim(line) {
		case "1":
			if err := m.Install(reader); err != nil {
				printError(err)
			}
		case "2":
			if err := m.ListInstances(); err != nil {
				printError(err)
			}
		case "3":
			if err := m.Uninstall(reader); err != nil {
				printError(err)
			}
		case "4":
			if err := m.InstallCluster(reader); err != nil {
				printError(err)
			}
		case "5":
			if err := m.UninstallCluster(reader); err != nil {
				printError(err)
			}
		case "6":
			if err := m.ForgetInstance(reader); err != nil {
				printError(err)
			}
		case "7":
			if err := m.ShowStatic(reader); err != nil {
				printError(err)
			}
		case "8":
			if err := m.ShowDynamic(reader); err != nil {
				printError(err)
			}
		case "0", "esc", "ESC":
			return nil
		default:
			fmt.Println("无效选项")
		}
	}
}

// ShowStatic 让用户选择机器并显示其 MySQL 静态信息。
func (m *MySQLMenu) ShowStatic(reader *bufio.Reader) error {
	machines, err := NewMachineMenu(m.core).selectManagedMachines(reader, "选择机器序号/名称/IP，多个用逗号分隔")
	if err != nil {
		return backAsNil(err)
	}
	for _, machine := range machines {
		ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
		item, err := m.core.MachineService.RefreshStaticInfo(ctx, machine.IP)
		cancel()
		if err != nil {
			return err
		}
		if len(machines) > 1 {
			fmt.Printf("\n===== %s (%s) =====\n", machine.Name, machine.IP)
		}
		printMySQLStaticInfo(machine.Name, item.MySQL)
	}
	return nil
}

// ShowDynamic 让用户选择 MySQL 实例并显示其动态指标数据。
func (m *MySQLMenu) ShowDynamic(reader *bufio.Reader) error {
	instances, err := selectMySQLInstances(m.core, reader, "选择 MySQL 实例序号/名称/IP:Port，多个用逗号分隔")
	if err != nil {
		return backAsNil(err)
	}
	for _, instance := range instances {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		item, err := m.core.MachineService.GetMySQLDynamicMetrics(ctx, instance.Endpoint())
		cancel()
		if err != nil {
			return err
		}
		if len(instances) > 1 {
			fmt.Printf("\n===== %s (%s) =====\n", instance.Name, instance.Endpoint())
		}
		printDynamicMetrics(item, "MySQL 动态数据")
	}
	return nil
}

// ForgetInstance 清理 Manager 中指定机器和端口的 MySQL 实例记录，不连接目标机器。
func (m *MySQLMenu) ForgetInstance(reader *bufio.Reader) error {
	if err := m.ListInstances(); err != nil {
		return err
	}
	machines, err := NewMachineMenu(m.core).selectManagedMachines(reader, "选择机器序号/名称/IP，多个用逗号分隔")
	if err != nil {
		return backAsNil(err)
	}
	port, err := promptMenuIntWithDefault(reader, "MySQL 端口", 3306)
	if err != nil {
		return backAsNil(err)
	}
	confirm, err := confirmYES(reader, fmt.Sprintf("仅清理 Manager 中 %d 台机器端口 %d 的 MySQL 实例记录，不会连接目标机器、不删除远端文件", len(machines), port))
	if err != nil {
		return backAsNil(err)
	}
	if !confirm {
		fmt.Println("已取消清理。")
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	for _, machine := range machines {
		if err := m.core.MySQLService.ForgetInstance(ctx, machine.IP, port); err != nil {
			return err
		}
	}
	fmt.Println("MySQL 实例记录已清理。")
	return nil
}

// InstallCluster 引导用户配置参数并对集群所有已纳管机器批量创建 MySQL 安装任务。
func (m *MySQLMenu) InstallCluster(reader *bufio.Reader) error {
	cluster, err := selectClusterName(m.core, reader, "选择集群序号/名称")
	if err != nil {
		return backAsNil(err)
	}
	port, err := promptMenuIntWithDefault(reader, "MySQL 端口", 3306)
	if err != nil {
		return backAsNil(err)
	}
	rootPassword, err := promptMenu(reader, "root 密码")
	if err != nil {
		return backAsNil(err)
	}
	profile, err := promptMenuWithDefault(reader, "参数 Profile", "prod")
	if err != nil {
		return backAsNil(err)
	}
	serverIDStart, err := promptMenuIntWithDefault(reader, "起始 server_id", 1)
	if err != nil {
		return backAsNil(err)
	}
	mysqlUser, err := promptMenuWithDefault(reader, "MySQL 管理用户", "mysql")
	if err != nil {
		return backAsNil(err)
	}
	accounts, err := promptMySQLAccounts(reader)
	if err != nil {
		return backAsNil(err)
	}

	instanceDir := fmt.Sprintf("/data/%d", port)
	dataDir, err := promptMenuWithDefault(reader, "data 目录", instanceDir+"/data")
	if err != nil {
		return backAsNil(err)
	}
	binlogDir, err := promptMenuWithDefault(reader, "binlog 目录", instanceDir+"/binlog")
	if err != nil {
		return backAsNil(err)
	}
	redoDir, err := promptMenuWithDefault(reader, "redo 目录", instanceDir+"/redo")
	if err != nil {
		return backAsNil(err)
	}
	undoDir, err := promptMenuWithDefault(reader, "undo 目录", instanceDir+"/undo")
	if err != nil {
		return backAsNil(err)
	}
	tmpDir, err := promptMenuWithDefault(reader, "tmp 目录", instanceDir+"/tmp")
	if err != nil {
		return backAsNil(err)
	}
	baseDir, err := promptMenuWithDefault(reader, "安装目录 base_dir", "/usr/local/mysql")
	if err != nil {
		return backAsNil(err)
	}
	myCnfPath, err := promptMenuWithDefault(reader, "my.cnf 文件", instanceDir+"/my.cnf")
	if err != nil {
		return backAsNil(err)
	}
	socketPath, err := promptMenuWithDefault(reader, "socket 文件", dataDir+"/mysql.sock")
	if err != nil {
		return backAsNil(err)
	}
	logFile, err := promptMenuWithDefault(reader, "mysqld.log 文件", dataDir+"/mysqld.log")
	if err != nil {
		return backAsNil(err)
	}
	pidFile, err := promptMenuWithDefault(reader, "pid_file 文件", dataDir+"/mysqld.pid")
	if err != nil {
		return backAsNil(err)
	}
	charsetsDir, err := promptMenuWithDefault(reader, "character_sets_dir", baseDir+"/share/charsets")
	if err != nil {
		return backAsNil(err)
	}
	pluginDir, err := promptMenuWithDefault(reader, "plugin_dir", baseDir+"/lib/plugin")
	if err != nil {
		return backAsNil(err)
	}
	confirm, err := confirmYesDefault(reader, fmt.Sprintf("确认对集群 %s 的所有已纳管机器创建 MySQL 安装任务", cluster), true)
	if err != nil {
		return backAsNil(err)
	}
	if !confirm {
		fmt.Println("已取消集群安装。")
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	result, err := m.core.TaskService.CreateClusterMySQLInstallTasks(ctx, app.ClusterMySQLInstallRequest{
		Cluster:          cluster,
		Port:             port,
		ServerIDStart:    serverIDStart,
		MySQLUser:        mysqlUser,
		InstanceDir:      instanceDir,
		DataDir:          dataDir,
		BinlogDir:        binlogDir,
		RedoDir:          redoDir,
		UndoDir:          undoDir,
		TmpDir:           tmpDir,
		BaseDir:          baseDir,
		MyCnfPath:        myCnfPath,
		SocketPath:       socketPath,
		ErrorLog:         logFile,
		PIDFile:          pidFile,
		CharacterSetsDir: charsetsDir,
		PluginDir:        pluginDir,
		RootPassword:     rootPassword,
		Profile:          profile,
		Accounts:         accounts,
	})
	if err != nil {
		return err
	}
	printClusterMySQLInstallResult(result)
	if result.Created == 1 {
		for _, item := range result.Items {
			if item.Error == "" {
				return watchTask(m.core, reader, item.Task.Task.ID)
			}
		}
	}
	taskIDs := make([]string, 0, len(result.Items))
	for _, item := range result.Items {
		if item.Error == "" {
			taskIDs = append(taskIDs, item.Task.Task.ID)
		}
	}
	if len(taskIDs) == 0 {
		return nil
	}
	return watchTaskGroup(m.core, reader, fmt.Sprintf("集群 %s MySQL 安装进度", result.Cluster), taskIDs)
}

// UninstallCluster 引导用户选择集群并对所有已纳管机器批量创建 MySQL 卸载任务。
func (m *MySQLMenu) UninstallCluster(reader *bufio.Reader) error {
	cluster, err := selectClusterName(m.core, reader, "选择集群序号/名称")
	if err != nil {
		return backAsNil(err)
	}
	port, err := promptMenuIntWithDefault(reader, "MySQL 端口", 3306)
	if err != nil {
		return backAsNil(err)
	}

	confirm, err := confirmYES(reader, fmt.Sprintf("确认对集群 %s 的所有已纳管机器卸载 MySQL（端口 %d）吗？请注意此操作会删除数据文件", cluster, port))
	if err != nil {
		return backAsNil(err)
	}
	if !confirm {
		fmt.Println("已取消集群卸载。")
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	result, err := m.core.TaskService.CreateClusterMySQLUninstallTasks(ctx, app.ClusterMySQLUninstallRequest{
		Cluster: cluster,
		Port:    port,
	})
	if err != nil {
		return err
	}
	printClusterMySQLUninstallResult(result)

	taskIDs := make([]string, 0, len(result.Items))
	for _, item := range result.Items {
		if item.Error == "" {
			taskIDs = append(taskIDs, item.Task.Task.ID)
		}
	}
	if len(taskIDs) == 0 {
		return nil
	}
	return watchTaskGroup(m.core, reader, fmt.Sprintf("集群 %s MySQL 卸载进度", result.Cluster), taskIDs)
}

// Uninstall 引导用户选择机器并创建 MySQL 卸载任务。
func (m *MySQLMenu) Uninstall(reader *bufio.Reader) error {
	if err := m.ListInstances(); err != nil {
		return err
	}
	machines, err := NewMachineMenu(m.core).selectManagedMachines(reader, "选择机器序号/名称/IP，多个用逗号分隔")
	if err != nil {
		return backAsNil(err)
	}
	port, err := promptMenuIntWithDefault(reader, "MySQL 端口", 3306)
	if err != nil {
		return backAsNil(err)
	}
	confirm, err := confirmYES(reader, fmt.Sprintf("确认卸载 %d 台机器上的 MySQL（端口 %d）吗？请注意此操作会删除数据文件", len(machines), port))
	if err != nil {
		return backAsNil(err)
	}
	if !confirm {
		fmt.Println("已取消卸载。")
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	taskIDs := make([]string, 0, len(machines))
	for _, machine := range machines {
		item, err := m.core.TaskService.CreateMySQLUninstallTask(ctx, taskusecase.CreateMySQLUninstallTaskRequest{
			Machine: machine.IP,
			Port:    port,
		})
		if err != nil {
			return err
		}
		fmt.Printf("MySQL 卸载任务已创建：%s (%s)\n", machine.Name, machine.IP)
		if len(machines) == 1 {
			if err := printTaskDetail(item); err != nil {
				return err
			}
		}
		taskIDs = append(taskIDs, item.Task.ID)
	}
	if len(taskIDs) == 1 {
		return watchTask(m.core, reader, taskIDs[0])
	}
	return watchTaskGroup(m.core, reader, "MySQL 批量卸载进度", taskIDs)
}

// Install 引导用户配置参数并对选定机器创建 MySQL 安装任务。
func (m *MySQLMenu) Install(reader *bufio.Reader) error {
	machines, err := NewMachineMenu(m.core).selectManagedMachines(reader, "选择机器序号/名称/IP，多个用逗号分隔")
	if err != nil {
		return backAsNil(err)
	}
	port, err := promptMenuIntWithDefault(reader, "MySQL 端口", 3306)
	if err != nil {
		return backAsNil(err)
	}
	rootPassword, err := promptMenu(reader, "root 密码")
	if err != nil {
		return backAsNil(err)
	}
	profile, err := promptMenuWithDefault(reader, "参数 Profile", "prod")
	if err != nil {
		return backAsNil(err)
	}
	accounts, err := promptMySQLAccounts(reader)
	if err != nil {
		return backAsNil(err)
	}

	serverID, err := promptMenuIntWithDefault(reader, "server_id", 1)
	if err != nil {
		return backAsNil(err)
	}
	mysqlUser, err := promptMenuWithDefault(reader, "MySQL 管理用户", "mysql")
	if err != nil {
		return backAsNil(err)
	}

	instanceDir := fmt.Sprintf("/data/%d", port)
	dataDir, err := promptMenuWithDefault(reader, "data 目录", instanceDir+"/data")
	if err != nil {
		return backAsNil(err)
	}
	binlogDir, err := promptMenuWithDefault(reader, "binlog 目录", instanceDir+"/binlog")
	if err != nil {
		return backAsNil(err)
	}
	redoDir, err := promptMenuWithDefault(reader, "redo 目录", instanceDir+"/redo")
	if err != nil {
		return backAsNil(err)
	}
	undoDir, err := promptMenuWithDefault(reader, "undo 目录", instanceDir+"/undo")
	if err != nil {
		return backAsNil(err)
	}
	tmpDir, err := promptMenuWithDefault(reader, "tmp 目录", instanceDir+"/tmp")
	if err != nil {
		return backAsNil(err)
	}
	baseDir, err := promptMenuWithDefault(reader, "安装目录 base_dir", "/usr/local/mysql")
	if err != nil {
		return backAsNil(err)
	}
	myCnfPath, err := promptMenuWithDefault(reader, "my.cnf 文件", instanceDir+"/my.cnf")
	if err != nil {
		return backAsNil(err)
	}
	socketPath, err := promptMenuWithDefault(reader, "socket 文件", dataDir+"/mysql.sock")
	if err != nil {
		return backAsNil(err)
	}
	logFile, err := promptMenuWithDefault(reader, "mysqld.log 文件", dataDir+"/mysqld.log")
	if err != nil {
		return backAsNil(err)
	}
	pidFile, err := promptMenuWithDefault(reader, "pid_file 文件", dataDir+"/mysqld.pid")
	if err != nil {
		return backAsNil(err)
	}
	charsetsDir, err := promptMenuWithDefault(reader, "character_sets_dir", baseDir+"/share/charsets")
	if err != nil {
		return backAsNil(err)
	}
	pluginDir, err := promptMenuWithDefault(reader, "plugin_dir", baseDir+"/lib/plugin")
	if err != nil {
		return backAsNil(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	taskIDs := make([]string, 0, len(machines))
	for i, machine := range machines {
		item, err := m.core.TaskService.CreateMySQLInstallTask(ctx, taskusecase.CreateMySQLInstallTaskRequest{
			Machine:          machine.IP,
			Port:             port,
			ServerID:         serverID + i,
			MySQLUser:        mysqlUser,
			InstanceDir:      instanceDir,
			DataDir:          dataDir,
			BinlogDir:        binlogDir,
			RedoDir:          redoDir,
			UndoDir:          undoDir,
			TmpDir:           tmpDir,
			BaseDir:          baseDir,
			MyCnfPath:        myCnfPath,
			SocketPath:       socketPath,
			ErrorLog:         logFile,
			PIDFile:          pidFile,
			CharacterSetsDir: charsetsDir,
			PluginDir:        pluginDir,
			RootPassword:     rootPassword,
			Profile:          profile,
			Accounts:         accounts,
		})
		if err != nil {
			return err
		}
		fmt.Printf("MySQL 安装任务已创建：%s (%s)\n", machine.Name, machine.IP)
		if len(machines) == 1 {
			if err := printTaskDetail(item); err != nil {
				return err
			}
		}
		taskIDs = append(taskIDs, item.Task.ID)
	}
	if len(taskIDs) == 1 {
		return watchTask(m.core, reader, taskIDs[0])
	}
	return watchTaskGroup(m.core, reader, "MySQL 批量安装进度", taskIDs)
}

// promptMySQLAccounts 引导用户配置 MySQL 账号初始化参数（monitor、mha、backup）。
func promptMySQLAccounts(reader *bufio.Reader) ([]taskdomain.MySQLAccountSpec, error) {
	fmt.Println()
	fmt.Println("MySQL 账号初始化配置，直接回车使用默认值。")
	monitor, err := promptMySQLAccount(reader, "monitor", true, false)
	if err != nil {
		return nil, err
	}
	mha, err := promptMySQLAccount(reader, "mha", true, false)
	if err != nil {
		return nil, err
	}
	backup, err := promptMySQLAccount(reader, "backup", true, true)
	if err != nil {
		return nil, err
	}
	return []taskdomain.MySQLAccountSpec{monitor, mha, backup}, nil
}

// promptMySQLAccount 引导用户配置单个 MySQL 账号的用户名、密码、主机和扩展权限。
func promptMySQLAccount(reader *bufio.Reader, role string, defaultEnabled bool, askExtended bool) (taskdomain.MySQLAccountSpec, error) {
	enabledDefault := "yes"
	if !defaultEnabled {
		enabledDefault = "no"
	}
	enabledText, err := promptMenuWithDefault(reader, fmt.Sprintf("初始化 %s 账号 yes/no", role), enabledDefault)
	if err != nil {
		return taskdomain.MySQLAccountSpec{}, err
	}
	enabled := isMenuYes(enabledText)
	spec := taskdomain.MySQLAccountSpec{Role: role, Enabled: enabled}
	if !enabled {
		return spec, nil
	}
	username, err := promptMenuWithDefault(reader, role+" 用户名", role)
	if err != nil {
		return taskdomain.MySQLAccountSpec{}, err
	}
	password, err := promptMenuWithDefault(reader, role+" 密码", "3306niubi")
	if err != nil {
		return taskdomain.MySQLAccountSpec{}, err
	}
	host, err := promptMenuWithDefault(reader, role+" host", "%")
	if err != nil {
		return taskdomain.MySQLAccountSpec{}, err
	}
	spec.Username = username
	spec.Password = password
	spec.Host = host
	if askExtended {
		extended, err := promptMenuWithDefault(reader, "backup 扩展权限 BACKUP_ADMIN/CLONE_ADMIN yes/no", "no")
		if err != nil {
			return taskdomain.MySQLAccountSpec{}, err
		}
		spec.ExtendedBackup = isMenuYes(extended)
	}
	return spec, nil
}

// isMenuYes 判断用户输入是否为肯定回答（yes 或 y）。
func isMenuYes(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "yes", "y":
		return true
	default:
		return false
	}
}

// ListInstances 显示所有 MySQL 实例列表。
func (m *MySQLMenu) ListInstances() error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	items, err := m.core.MySQLService.ListInstanceViews(ctx)
	if err != nil {
		return err
	}
	if len(items) == 0 {
		fmt.Println("暂无 MySQL 实例。")
		return nil
	}
	headers := []string{"机器名", "IP", "集群", "端口", "server_id", "用户", "实例目录", "data目录", "状态", "心跳", "检查详情", "检查时间", "Profile", "安装包", "更新时间"}
	rows := make([][]string, 0, len(items))
	for _, item := range items {
		rows = append(rows, []string{
			emptyAsDash(item.MachineName),
			emptyAsDash(item.MachineIP),
			emptyAsDash(item.Cluster),
			strconv.Itoa(item.Port),
			strconv.Itoa(item.ServerID),
			emptyAsDash(item.MySQLUser),
			emptyAsDash(item.InstanceDir),
			emptyAsDash(item.DataDir),
			emptyAsDash(item.Status),
			emptyAsDash(item.HeartbeatStatus),
			summarizeError(item.HeartbeatDetail),
			emptyAsDash(item.HeartbeatCheckedAt),
			emptyAsDash(item.Profile),
			emptyAsDash(item.PackageName),
			item.UpdatedAt.Local().Format("2006-01-02 15:04:05"),
		})
	}
	printAlignedTable(headers, rows)
	return nil
}

// printClusterMySQLInstallResult 以表格形式打印集群 MySQL 安装任务创建结果。
func printClusterMySQLInstallResult(result app.ClusterMySQLInstallResult) {
	fmt.Printf("集群 %s MySQL 安装任务创建完成：成功 %d，失败 %d\n", result.Cluster, result.Created, result.Failed)
	headers := []string{"机器名", "IP", "任务ID", "状态", "错误"}
	rows := make([][]string, 0, len(result.Items))
	for _, item := range result.Items {
		taskID := "-"
		status := "-"
		if item.Error == "" {
			taskID = item.Task.Task.ID
			status = string(item.Task.Task.Status)
		}
		rows = append(rows, []string{
			emptyAsDash(item.Name),
			emptyAsDash(item.IP),
			taskID,
			status,
			emptyAsDash(item.Error),
		})
	}
	printAlignedTable(headers, rows)
}

// printClusterMySQLUninstallResult 以表格形式打印集群 MySQL 卸载任务创建结果。
func printClusterMySQLUninstallResult(result app.ClusterMySQLUninstallResult) {
	fmt.Printf("集群 %s MySQL 卸载任务创建完成：成功 %d，失败 %d\n", result.Cluster, result.Created, result.Failed)
	headers := []string{"机器名", "IP", "任务ID", "状态", "错误"}
	rows := make([][]string, 0, len(result.Items))
	for _, item := range result.Items {
		taskID := "-"
		status := "-"
		if item.Error == "" {
			taskID = item.Task.Task.ID
			status = string(item.Task.Task.Status)
		}
		rows = append(rows, []string{
			emptyAsDash(item.Name),
			emptyAsDash(item.IP),
			taskID,
			status,
			emptyAsDash(item.Error),
		})
	}
	printAlignedTable(headers, rows)
}

// printMySQLStaticInfo 以表格形式打印 MySQL 静态信息。
func printMySQLStaticInfo(machine string, item collectdomain.MySQLStaticInfo) {
	fmt.Printf("MySQL 静态数据：%s\n", machine)
	headers := []string{"字段", "值"}
	rows := [][]string{
		{"installed", strconv.FormatBool(item.Installed)},
		{"collect_ok", strconv.FormatBool(item.CollectOK)},
		{"error", emptyAsDash(item.Error)},
		{"server_id", strconv.Itoa(item.ServerID)},
		{"base_dir", emptyAsDash(item.BaseDir)},
		{"version", emptyAsDash(item.Version)},
		{"port", strconv.Itoa(item.Port)},
		{"config_file", emptyAsDash(item.ConfigFile)},
		{"slow_log", emptyAsDash(item.SlowLog)},
		{"error_log", emptyAsDash(item.ErrorLog)},
		{"socket", emptyAsDash(item.Socket)},
		{"data_dir", emptyAsDash(item.DataDir)},
		{"undo_dir", emptyAsDash(item.UndoDir)},
		{"redo_dir", emptyAsDash(item.RedoDir)},
		{"binlog_dir", emptyAsDash(item.BinlogDir)},
		{"tmp_dir", emptyAsDash(item.TmpDir)},
	}
	printAlignedTable(headers, rows)
}

// backAsNil 将返回菜单的错误转换为 nil，以便上层正常退出。
func backAsNil(err error) error {
	if errors.Is(err, ErrBackToMenu) {
		return nil
	}
	return err
}
