package command

import (
	"context"
	"flag"
	"fmt"
	"time"

	"gmha/internal/app"
	taskdomain "gmha/internal/domain/task"
	taskusecase "gmha/internal/usecase/task"
)

// MySQLCommand 是 MySQL 管理的 CLI 命令处理器，负责处理 mysql 相关的子命令。
type MySQLCommand struct {
	core *app.App
}

// NewMySQLCommand 创建一个新的 MySQLCommand 实例。
func NewMySQLCommand(core *app.App) *MySQLCommand {
	return &MySQLCommand{core: core}
}

// fillMySQLInstallPathDefaults 为 MySQL 安装路径参数填充默认值，包括数据目录、binlog 目录、redo 目录等。
func fillMySQLInstallPathDefaults(port int, instanceDir, baseDir string, dataDir, binlogDir, redoDir, undoDir, tmpDir, myCnfPath, socketPath, errorLog, pidFile, characterSetsDir, pluginDir *string) {
	if instanceDir == "" {
		instanceDir = fmt.Sprintf("/data/%d", port)
	}
	if baseDir == "" {
		baseDir = "/usr/local/mysql"
	}
	if *dataDir == "" {
		*dataDir = instanceDir + "/data"
	}
	if *binlogDir == "" {
		*binlogDir = instanceDir + "/binlog"
	}
	if *redoDir == "" {
		*redoDir = instanceDir + "/redo"
	}
	if *undoDir == "" {
		*undoDir = instanceDir + "/undo"
	}
	if *tmpDir == "" {
		*tmpDir = instanceDir + "/tmp"
	}
	if *myCnfPath == "" {
		*myCnfPath = instanceDir + "/my.cnf"
	}
	if *socketPath == "" {
		*socketPath = *dataDir + "/mysql.sock"
	}
	if *errorLog == "" {
		*errorLog = *dataDir + "/mysqld.log"
	}
	if *pidFile == "" {
		*pidFile = *dataDir + "/mysqld.pid"
	}
	if *characterSetsDir == "" {
		*characterSetsDir = baseDir + "/share/charsets"
	}
	if *pluginDir == "" {
		*pluginDir = baseDir + "/lib/plugin"
	}
}

// Run 解析并执行 MySQL 管理子命令，支持 install-cluster、install、uninstall、forget、list、static-info、dynamic-info 等操作。
func (c *MySQLCommand) Run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%s", usage())
	}
	switch args[0] {
	case "install-cluster":
		fs := flag.NewFlagSet("mysql install-cluster", flag.ContinueOnError)
		cluster := fs.String("cluster", "", "集群名称")
		port := fs.Int("port", 3306, "MySQL 端口")
		serverIDStart := fs.Int("server-id-start", 1, "起始 server_id，每台机器递增")
		mysqlUser := fs.String("mysql-user", "mysql", "MySQL 管理用户")
		instanceDir := fs.String("instance-dir", "", "实例根目录，默认 /data/<port>")
		dataDir := fs.String("data-dir", "", "数据目录，默认 /data/<port>/data")
		binlogDir := fs.String("binlog-dir", "", "binlog 目录，默认 /data/<port>/binlog")
		redoDir := fs.String("redo-dir", "", "redo 目录，默认 /data/<port>/redo")
		undoDir := fs.String("undo-dir", "", "undo 目录，默认 /data/<port>/undo")
		tmpDir := fs.String("tmp-dir", "", "tmp 目录，默认 /data/<port>/tmp")
		baseDir := fs.String("base-dir", "/usr/local/mysql", "安装目录")
		myCnfPath := fs.String("my-cnf", "", "my.cnf 文件，默认 /data/<port>/my.cnf")
		socketPath := fs.String("socket", "", "socket 文件，默认 /data/<port>/data/mysql.sock")
		errorLog := fs.String("log-file", "", "mysqld.log 文件，默认 /data/<port>/data/mysqld.log")
		pidFile := fs.String("pid-file", "", "pid 文件，默认 /data/<port>/data/mysqld.pid")
		characterSetsDir := fs.String("character-sets-dir", "", "字符集目录，默认 /usr/local/mysql/share/charsets")
		pluginDir := fs.String("plugin-dir", "", "插件目录，默认 /usr/local/mysql/lib/plugin")
		rootPassword := fs.String("root-password", "", "root 密码")
		profile := fs.String("profile", "prod", "参数 profile")
		installPTTools := fs.Bool("install-pt-tools", false, "安装 Percona Toolkit 及依赖")
		installXtraBackup := fs.Bool("install-xtrabackup", false, "安装与 MySQL 版本匹配的 Percona XtraBackup")
		memoryAllocator := fs.String("memory-allocator", "system", "内存分配器：system 或 tcmalloc")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *instanceDir == "" {
			*instanceDir = fmt.Sprintf("/data/%d", *port)
		}
		fillMySQLInstallPathDefaults(*port, *instanceDir, *baseDir, dataDir, binlogDir, redoDir, undoDir, tmpDir, myCnfPath, socketPath, errorLog, pidFile, characterSetsDir, pluginDir)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		result, err := c.core.TaskService.CreateClusterMySQLInstallTasks(ctx, app.ClusterMySQLInstallRequest{
			Cluster:           *cluster,
			Port:              *port,
			ServerIDStart:     *serverIDStart,
			MySQLUser:         *mysqlUser,
			InstanceDir:       *instanceDir,
			DataDir:           *dataDir,
			BinlogDir:         *binlogDir,
			RedoDir:           *redoDir,
			UndoDir:           *undoDir,
			TmpDir:            *tmpDir,
			BaseDir:           *baseDir,
			MyCnfPath:         *myCnfPath,
			SocketPath:        *socketPath,
			ErrorLog:          *errorLog,
			PIDFile:           *pidFile,
			CharacterSetsDir:  *characterSetsDir,
			PluginDir:         *pluginDir,
			RootPassword:      *rootPassword,
			Profile:           *profile,
			InstallPTTools:    *installPTTools,
			InstallXtraBackup: *installXtraBackup,
			MemoryAllocator:   *memoryAllocator,
		})
		if err != nil {
			return err
		}
		return printJSON(result)
	case "install":
		fs := flag.NewFlagSet("mysql install", flag.ContinueOnError)
		machine := fs.String("machine", "", "机器名或 IP")
		port := fs.Int("port", 3306, "MySQL 端口")
		serverID := fs.Int("server-id", 1, "server_id")
		mysqlUser := fs.String("mysql-user", "mysql", "MySQL 管理用户")
		instanceDir := fs.String("instance-dir", "", "实例根目录，默认 /data/<port>")
		dataDir := fs.String("data-dir", "", "数据目录，默认 /data/<port>/data")
		binlogDir := fs.String("binlog-dir", "", "binlog 目录，默认 /data/<port>/binlog")
		redoDir := fs.String("redo-dir", "", "redo 目录，默认 /data/<port>/redo")
		undoDir := fs.String("undo-dir", "", "undo 目录，默认 /data/<port>/undo")
		tmpDir := fs.String("tmp-dir", "", "tmp 目录，默认 /data/<port>/tmp")
		baseDir := fs.String("base-dir", "/usr/local/mysql", "安装目录")
		myCnfPath := fs.String("my-cnf", "", "my.cnf 文件，默认 /data/<port>/my.cnf")
		socketPath := fs.String("socket", "", "socket 文件，默认 /data/<port>/data/mysql.sock")
		errorLog := fs.String("log-file", "", "mysqld.log 文件，默认 /data/<port>/data/mysqld.log")
		pidFile := fs.String("pid-file", "", "pid 文件，默认 /data/<port>/data/mysqld.pid")
		characterSetsDir := fs.String("character-sets-dir", "", "字符集目录，默认 /usr/local/mysql/share/charsets")
		pluginDir := fs.String("plugin-dir", "", "插件目录，默认 /usr/local/mysql/lib/plugin")
		rootPassword := fs.String("root-password", "", "root 密码")
		profile := fs.String("profile", "default", "参数 profile")
		installPTTools := fs.Bool("install-pt-tools", false, "安装 Percona Toolkit 及依赖")
		installXtraBackup := fs.Bool("install-xtrabackup", false, "安装与 MySQL 版本匹配的 Percona XtraBackup")
		memoryAllocator := fs.String("memory-allocator", "system", "内存分配器：system 或 tcmalloc")
		monitorEnabled := fs.Bool("monitor-enabled", true, "是否初始化 monitor 账号")
		monitorUser := fs.String("monitor-user", "", "monitor 用户名，默认 monitor")
		monitorPassword := fs.String("monitor-password", "", "monitor 密码，默认 3306niubi")
		monitorHost := fs.String("monitor-host", "", "monitor host，默认 %")
		mhaEnabled := fs.Bool("mha-enabled", true, "是否初始化 mha 账号")
		mhaUser := fs.String("mha-user", "", "mha 用户名，默认 mha")
		mhaPassword := fs.String("mha-password", "", "mha 密码，默认 3306niubi")
		mhaHost := fs.String("mha-host", "", "mha host，默认 %")
		backupEnabled := fs.Bool("backup-enabled", true, "是否初始化 backup 账号")
		backupUser := fs.String("backup-user", "", "backup 用户名，默认 backup")
		backupPassword := fs.String("backup-password", "", "backup 密码，默认 3306niubi")
		backupHost := fs.String("backup-host", "", "backup host，默认 %")
		backupExtended := fs.Bool("backup-extended", false, "backup 是否授予 BACKUP_ADMIN/CLONE_ADMIN，默认关闭")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		item, err := c.core.TaskService.CreateMySQLInstallTask(ctx, taskusecase.CreateMySQLInstallTaskRequest{
			Machine:           *machine,
			Port:              *port,
			ServerID:          *serverID,
			MySQLUser:         *mysqlUser,
			InstanceDir:       *instanceDir,
			DataDir:           *dataDir,
			BinlogDir:         *binlogDir,
			RedoDir:           *redoDir,
			UndoDir:           *undoDir,
			TmpDir:            *tmpDir,
			BaseDir:           *baseDir,
			MyCnfPath:         *myCnfPath,
			SocketPath:        *socketPath,
			ErrorLog:          *errorLog,
			PIDFile:           *pidFile,
			CharacterSetsDir:  *characterSetsDir,
			PluginDir:         *pluginDir,
			RootPassword:      *rootPassword,
			Profile:           *profile,
			InstallPTTools:    *installPTTools,
			InstallXtraBackup: *installXtraBackup,
			MemoryAllocator:   *memoryAllocator,
			Accounts: []taskdomain.MySQLAccountSpec{
				{Role: "monitor", Username: *monitorUser, Password: *monitorPassword, Host: *monitorHost, Enabled: *monitorEnabled},
				{Role: "mha", Username: *mhaUser, Password: *mhaPassword, Host: *mhaHost, Enabled: *mhaEnabled},
				{Role: "backup", Username: *backupUser, Password: *backupPassword, Host: *backupHost, Enabled: *backupEnabled, ExtendedBackup: *backupExtended},
			},
		})
		if err != nil {
			return err
		}
		return printJSON(item)
	case "uninstall":
		fs := flag.NewFlagSet("mysql uninstall", flag.ContinueOnError)
		machine := fs.String("machine", "", "机器名或 IP")
		port := fs.Int("port", 3306, "MySQL 端口")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		item, err := c.core.TaskService.CreateMySQLUninstallTask(ctx, taskusecase.CreateMySQLUninstallTaskRequest{
			Machine: *machine,
			Port:    *port,
		})
		if err != nil {
			return err
		}
		return printJSON(item)
	case "forget":
		fs := flag.NewFlagSet("mysql forget", flag.ContinueOnError)
		machine := fs.String("machine", "", "机器名或 IP")
		port := fs.Int("port", 3306, "MySQL 端口")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := c.core.MySQLService.ForgetInstance(ctx, *machine, *port); err != nil {
			return err
		}
		return printJSON(map[string]any{
			"machine": *machine,
			"port":    *port,
			"status":  "forgotten",
		})
	case "list":
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		items, err := c.core.MySQLService.ListInstanceViews(ctx)
		if err != nil {
			return err
		}
		return printJSON(items)
	case "static-info":
		fs := flag.NewFlagSet("mysql static-info", flag.ContinueOnError)
		machine := fs.String("machine", "", "机器名或 IP")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
		defer cancel()
		item, err := c.core.MachineService.RefreshStaticInfo(ctx, *machine)
		if err != nil {
			return err
		}
		return printJSON(item.MySQL)
	case "dynamic-info":
		fs := flag.NewFlagSet("mysql dynamic-info", flag.ContinueOnError)
		machine := fs.String("machine", "", "机器名或 IP")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		item, err := c.core.MachineService.GetMySQLDynamicMetrics(ctx, *machine)
		if err != nil {
			return err
		}
		return printJSON(item)
	default:
		return fmt.Errorf("%s", usage())
	}
}
