package task

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	collectdomain "gmha/internal/collect"
	agentdomain "gmha/internal/domain/agent"
	taskdomain "gmha/internal/domain/task"
	"gmha/internal/infrastructure/render"
	mysqlapp "gmha/internal/mysql"
)

// ErrMachineInfoNotFound 表示机器信息未找到，需要先采集机器信息。
var ErrMachineInfoNotFound = errors.New("machine info not found, please collect machine info first")

// MachineInfoRepository 定义了机器信息仓储接口，用于查询采集到的机器信息。
type MachineInfoRepository interface {
	Get(ctx context.Context, machineID string) (collectdomain.MachineInfo, bool, error)
}

type PerconaToolkitPackageResolver interface {
	ResolvePerconaToolkitPackage(arch string) (string, error)
	ResolveXtraBackupPackage(mysqlVersion, arch, glibcVersion string) (string, error)
}

// CreateMySQLInstallTaskRequest 是创建 MySQL 安装任务的请求参数。
type CreateMySQLInstallTaskRequest struct {
	ParentTaskID      string
	Machine           string
	Port              int
	ServerID          int
	MySQLUser         string
	InstanceDir       string
	DataDir           string
	BinlogDir         string
	RedoDir           string
	UndoDir           string
	TmpDir            string
	BaseDir           string
	MyCnfPath         string
	SocketPath        string
	ErrorLog          string
	PIDFile           string
	CharacterSetsDir  string
	PluginDir         string
	RootPassword      string
	Profile           string
	PackageName       string
	Version           string
	Architecture      string
	InstallPTTools    bool
	InstallXtraBackup bool
	MemoryAllocator   string
	RuntimeParameters map[string]string
	Accounts          []taskdomain.MySQLAccountSpec
}

// ListPackages 返回安装任务可选的 MySQL 安装包版本。
func (u *CreateMySQLInstallTaskUsecase) ListPackages() ([]mysqlapp.PackageOption, error) {
	return u.packageSelector.ListOptions()
}

// ResolvePackage validates a named package against the target machine's
// architecture and glibc before an upgrade workflow is created.
func (u *CreateMySQLInstallTaskUsecase) ResolvePackage(info collectdomain.MachineInfo, name string) (mysqlapp.Package, error) {
	return u.packageSelector.SelectNamed(info, name)
}

// CreateMySQLInstallTaskResult 是创建 MySQL 安装任务的结果。
type CreateMySQLInstallTaskResult struct {
	Task   taskdomain.Task
	Steps  []taskdomain.Step
	Events []taskdomain.Event
}

// CreateMySQLInstallTaskUsecase 是创建 MySQL 安装任务的用例，负责计算配置参数并构建安装任务。
type CreateMySQLInstallTaskUsecase struct {
	machines          MachineRepository
	agents            AgentRepository
	machineInfo       MachineInfoRepository
	loader            *render.Loader
	engine            *render.Engine
	calculator        *mysqlapp.Calculator
	packageSelector   *mysqlapp.PackageSelector
	ptPackageResolver PerconaToolkitPackageResolver
	managerHTTPAddr   string
	managerAddrForIP  func(string) string
}

// NewCreateMySQLInstallTaskUsecase 创建一个新的 MySQL 安装任务用例实例。
func NewCreateMySQLInstallTaskUsecase(
	machines MachineRepository,
	agents AgentRepository,
	machineInfo MachineInfoRepository,
	loader *render.Loader,
	engine *render.Engine,
	calculator *mysqlapp.Calculator,
	selector *mysqlapp.PackageSelector,
	ptPackageResolver PerconaToolkitPackageResolver,
	managerHTTPAddr string,
	managerAddrForIP ...func(string) string,
) *CreateMySQLInstallTaskUsecase {
	var resolver func(string) string
	if len(managerAddrForIP) > 0 {
		resolver = managerAddrForIP[0]
	}
	return &CreateMySQLInstallTaskUsecase{
		machines:          machines,
		agents:            agents,
		machineInfo:       machineInfo,
		loader:            loader,
		engine:            engine,
		calculator:        calculator,
		packageSelector:   selector,
		ptPackageResolver: ptPackageResolver,
		managerHTTPAddr:   strings.TrimRight(managerHTTPAddr, "/"),
		managerAddrForIP:  resolver,
	}
}

// Execute 执行创建 MySQL 安装任务的完整流程，包括验证参数、计算配置、渲染模板和构建任务。
func (u *CreateMySQLInstallTaskUsecase) Execute(ctx context.Context, req CreateMySQLInstallTaskRequest) (CreateMySQLInstallTaskResult, error) {
	target := strings.TrimSpace(req.Machine)
	if target == "" {
		return CreateMySQLInstallTaskResult{}, errors.New("machine is required")
	}
	if req.Port <= 0 {
		return CreateMySQLInstallTaskResult{}, errors.New("port is required")
	}
	if strings.TrimSpace(req.RootPassword) == "" {
		return CreateMySQLInstallTaskResult{}, errors.New("root_password is required")
	}
	if strings.TrimSpace(req.Profile) == "" {
		req.Profile = "default"
	}

	machine, ok, err := (&CreateExecTaskUsecase{machines: u.machines, agents: u.agents}).resolveMachine(ctx, target)
	if err != nil {
		return CreateMySQLInstallTaskResult{}, err
	}
	if !ok {
		return CreateMySQLInstallTaskResult{}, fmt.Errorf("machine %s not found", target)
	}
	agent, ok, err := u.agents.GetByMachineID(ctx, machine.ID)
	if err != nil {
		return CreateMySQLInstallTaskResult{}, err
	}
	if !ok || agent.State != agentdomain.StateOnline {
		return CreateMySQLInstallTaskResult{}, errors.New("online agent is required for mysql installation")
	}
	info, ok, err := u.machineInfo.Get(ctx, machine.ID)
	if err != nil {
		return CreateMySQLInstallTaskResult{}, err
	}
	if !ok {
		return CreateMySQLInstallTaskResult{}, ErrMachineInfoNotFound
	}

	profile, err := mysqlapp.LoadProfile("configs", req.Profile)
	if err != nil {
		return CreateMySQLInstallTaskResult{}, err
	}
	input := mysqlapp.NormalizeConfigInput(mysqlapp.ConfigInput{
		Port:             req.Port,
		ServerID:         req.ServerID,
		MySQLUser:        req.MySQLUser,
		InstanceDir:      req.InstanceDir,
		DataDir:          req.DataDir,
		BinlogDir:        req.BinlogDir,
		RedoDir:          req.RedoDir,
		UndoDir:          req.UndoDir,
		TmpDir:           req.TmpDir,
		BaseDir:          req.BaseDir,
		MyCnfPath:        req.MyCnfPath,
		SocketPath:       req.SocketPath,
		ErrorLog:         req.ErrorLog,
		PIDFile:          req.PIDFile,
		CharacterSetsDir: req.CharacterSetsDir,
		PluginDir:        req.PluginDir,
	})
	vars, err := u.calculator.Calculate(info, profile, input)
	if err != nil {
		return CreateMySQLInstallTaskResult{}, err
	}
	var pkg mysqlapp.Package
	if strings.TrimSpace(req.PackageName) != "" {
		pkg, err = u.packageSelector.SelectNamed(info, req.PackageName)
	} else {
		pkg, err = u.packageSelector.SelectVersionArchitecture(info, req.Version, req.Architecture)
	}
	if err != nil {
		return CreateMySQLInstallTaskResult{}, err
	}
	if req.InstallPTTools && !mysqlapp.SupportsPerconaToolkit(pkg.Version) {
		return CreateMySQLInstallTaskResult{}, fmt.Errorf("automatic Percona Toolkit installation is not supported for MySQL %s; disable install_pt_tools", pkg.Version)
	}
	ptPackageName := ""
	ptPackageDownloadURL := ""
	xtraBackupPackageName := ""
	xtraBackupPackageDownloadURL := ""
	if req.InstallPTTools {
		if u.ptPackageResolver == nil {
			return CreateMySQLInstallTaskResult{}, errors.New("Percona Toolkit package resolver is not configured")
		}
		ptPackageName, err = u.ptPackageResolver.ResolvePerconaToolkitPackage(info.Arch)
		if err != nil {
			return CreateMySQLInstallTaskResult{}, err
		}
		ptPackageDownloadURL = u.managerResourceURL(machine.IP, "/api/v1/packages/percona-toolkit/"+url.PathEscape(ptPackageName))
	}
	if req.InstallXtraBackup {
		if u.ptPackageResolver == nil {
			return CreateMySQLInstallTaskResult{}, errors.New("XtraBackup package resolver is not configured")
		}
		xtraBackupPackageName, err = u.ptPackageResolver.ResolveXtraBackupPackage(pkg.Version, info.Arch, info.GlibcVersion)
		if err != nil {
			return CreateMySQLInstallTaskResult{}, err
		}
		xtraBackupPackageDownloadURL = u.managerResourceURL(machine.IP, "/api/v1/packages/xtrabackup/"+url.PathEscape(xtraBackupPackageName))
	}
	memoryAllocator := strings.ToLower(strings.TrimSpace(req.MemoryAllocator))
	if memoryAllocator == "" {
		memoryAllocator = "system"
	}
	if memoryAllocator != "system" && memoryAllocator != "tcmalloc" {
		return CreateMySQLInstallTaskResult{}, fmt.Errorf("unsupported memory allocator %q; use system or tcmalloc", req.MemoryAllocator)
	}
	if err := mysqlapp.ApplyRuntimeParametersForVersion(&vars, pkg.Version, req.RuntimeParameters); err != nil {
		return CreateMySQLInstallTaskResult{}, err
	}
	tpl, err := u.loader.LoadTemplate("mysql", "my.cnf.tmpl")
	if err != nil {
		return CreateMySQLInstallTaskResult{}, err
	}
	renderedMyCnf, err := u.engine.Render("mysql-mycnf", string(tpl), vars)
	if err != nil {
		return CreateMySQLInstallTaskResult{}, err
	}
	renderedSystemd, err := u.renderTemplate("mysqld.service.tmpl", vars)
	if err != nil {
		return CreateMySQLInstallTaskResult{}, err
	}
	renderedEnv, err := u.renderTemplate("mysql.env.tmpl", vars)
	if err != nil {
		return CreateMySQLInstallTaskResult{}, err
	}
	renderedLimits, err := u.renderTemplate("limits.conf.tmpl", vars)
	if err != nil {
		return CreateMySQLInstallTaskResult{}, err
	}
	renderedSysctl, err := u.renderTemplate("sysctl.conf.tmpl", vars)
	if err != nil {
		return CreateMySQLInstallTaskResult{}, err
	}
	accounts := normalizeInstallAccounts(req.Accounts)
	if req.InstallXtraBackup {
		accounts = ensureXtraBackupAccountPrivileges(accounts)
	}
	accountCheck := make([]mysqlapp.AccountSpec, 0, len(accounts))
	for _, item := range accounts {
		accountCheck = append(accountCheck, mysqlapp.AccountSpec{
			Role:           item.Role,
			Username:       item.Username,
			Password:       item.Password,
			Host:           item.Host,
			Enabled:        item.Enabled,
			ExtendedBackup: item.ExtendedBackup,
			Privileges:     item.Privileges,
		})
	}
	if err := mysqlapp.ValidateAccountSpecs(accountCheck); err != nil {
		return CreateMySQLInstallTaskResult{}, err
	}

	spec := taskdomain.MySQLInstallSpec{
		Port:                         input.Port,
		ServerID:                     vars.ServerID,
		MySQLUser:                    input.MySQLUser,
		InstanceDir:                  input.InstanceDir,
		DataDir:                      input.DataDir,
		BinlogDir:                    input.BinlogDir,
		RedoDir:                      input.RedoDir,
		UndoDir:                      input.UndoDir,
		TmpDir:                       input.TmpDir,
		BaseDir:                      input.BaseDir,
		RootPassword:                 req.RootPassword,
		Profile:                      req.Profile,
		PackageName:                  pkg.FileName,
		Version:                      pkg.Version,
		Architecture:                 mysqlapp.NormalizeArchitecture(pkg.Arch),
		PackageDownloadURL:           u.managerResourceURL(machine.IP, "/api/v1/software/mysql/"+url.PathEscape(pkg.FileName)),
		MyCnfPath:                    input.MyCnfPath,
		MyCnfContent:                 string(renderedMyCnf),
		SocketPath:                   input.SocketPath,
		ErrorLog:                     input.ErrorLog,
		PIDFile:                      input.PIDFile,
		CharacterSetsDir:             input.CharacterSetsDir,
		PluginDir:                    input.PluginDir,
		SystemdUnitName:              "mysqld",
		SystemdContent:               string(renderedSystemd),
		LimitsPath:                   "/etc/security/limits.d/mysql.conf",
		LimitsContent:                string(renderedLimits),
		SysctlPath:                   "/etc/sysctl.d/99-gmha-mysql.conf",
		SysctlContent:                string(renderedSysctl),
		EnvFilePath:                  "/etc/profile.d/mysql.sh",
		EnvContent:                   string(renderedEnv),
		InstallPTTools:               req.InstallPTTools,
		PTToolsPackageName:           ptPackageName,
		PTToolsPackageDownloadURL:    ptPackageDownloadURL,
		InstallXtraBackup:            req.InstallXtraBackup,
		XtraBackupPackageName:        xtraBackupPackageName,
		XtraBackupPackageDownloadURL: xtraBackupPackageDownloadURL,
		MemoryAllocator:              memoryAllocator,
		RuntimeParameters:            normalizedRuntimeParameters(req.RuntimeParameters),
		Accounts:                     accounts,
	}
	specJSON, _ := json.Marshal(spec)

	now := time.Now().UTC()
	taskID := fmt.Sprintf("task-%d", now.UnixNano())
	task := taskdomain.Task{
		ID:              taskID,
		ParentTaskID:    strings.TrimSpace(req.ParentTaskID),
		Type:            taskdomain.TypeMySQLInstall,
		MachineID:       machine.ID,
		AgentID:         agent.ID,
		Status:          taskdomain.StatusPending,
		ProgressPercent: 0,
		CurrentStep:     "等待派发",
		SpecJSON:        specJSON,
		CreatedAt:       now,
	}
	steps := buildMySQLInstallSteps(taskID, req.InstallPTTools, req.InstallXtraBackup, memoryAllocator)
	events := []taskdomain.Event{{
		ID:        fmt.Sprintf("task-event-%d", now.UnixNano()),
		TaskID:    taskID,
		StepID:    steps[0].ID,
		EventType: taskdomain.EventInfo,
		Content:   "mysql_install task created",
		CreatedAt: now,
	}}
	return CreateMySQLInstallTaskResult{Task: task, Steps: steps, Events: events}, nil
}

func (u *CreateMySQLInstallTaskUsecase) managerResourceURL(targetIP, path string) string {
	base := u.managerHTTPAddr
	if u.managerAddrForIP != nil {
		// Resolver may return an ordered fallback list for Agent configuration.
		// A task URL uses the first (target-specific) address; newer Agents also
		// rebase it to the endpoint of their active task connection.
		if resolved := strings.TrimSpace(strings.Split(u.managerAddrForIP(targetIP), ",")[0]); resolved != "" {
			base = strings.TrimRight(resolved, "/")
		}
	}
	if base == "" {
		return "/" + strings.TrimLeft(path, "/")
	}
	return base + "/" + strings.TrimLeft(path, "/")
}

func normalizedRuntimeParameters(parameters map[string]string) map[string]string {
	out := make(map[string]string)
	for name, value := range parameters {
		name = strings.ToLower(strings.TrimSpace(name))
		value = strings.TrimSpace(value)
		if name != "" && value != "" {
			out[name] = value
		}
	}
	return out
}

// renderTemplate 使用配置变量渲染指定名称的模板文件。
func (u *CreateMySQLInstallTaskUsecase) renderTemplate(name string, vars mysqlapp.ConfigVars) ([]byte, error) {
	source, err := u.loader.LoadTemplate("mysql", name)
	if err != nil {
		return nil, err
	}
	return u.engine.Render(name, string(source), vars)
}

// buildMySQLInstallSteps 构建 MySQL 安装任务的所有步骤。
func buildMySQLInstallSteps(taskID string, installPTTools, installXtraBackup bool, memoryAllocator string) []taskdomain.Step {
	names := []string{
		"check_env",
		"disable_firewall_selinux",
		"uninstall_mariadb",
		"install_dependencies",
		"optimize_limits",
		"optimize_sysctl",
		"create_mysql_user",
		"create_directories",
		"upload_mysql_package",
		"extract_mysql",
		"create_symlink",
		"check_mysql_binary",
		"setup_env",
		"generate_mycnf",
		"generate_systemd",
	}
	if memoryAllocator == "tcmalloc" {
		names = append(names, "configure_memory_allocator")
	}
	names = append(names,
		"initialize_mysql",
		"start_mysql",
		"wait_mysql_ready",
		"set_root_password",
		"verify_mysql",
	)
	if installPTTools {
		names = append(names, "install_pt_tools")
	}
	if installXtraBackup {
		names = append(names, "install_xtrabackup")
	}
	names = append(names,
		"init_accounts",
		"ensure_heartbeat_table",
		"setup_agent_collect_config",
	)
	steps := make([]taskdomain.Step, 0, len(names))
	now := time.Now().UTC().UnixNano()
	for i, name := range names {
		steps = append(steps, taskdomain.Step{
			ID:       fmt.Sprintf("task-step-%d-%d", now, i+1),
			TaskID:   taskID,
			StepNo:   i + 1,
			StepName: name,
			Status:   taskdomain.StepPending,
			Message:  mysqlInstallStepMessage(name),
		})
	}
	return steps
}

func mysqlInstallStepMessage(name string) string {
	labels := map[string]string{
		"check_env":                  "检查安装环境",
		"disable_firewall_selinux":   "关闭防火墙和 SELinux",
		"uninstall_mariadb":          "清理 MariaDB 冲突包",
		"install_dependencies":       "安装 MySQL 依赖",
		"optimize_limits":            "优化系统资源限制",
		"optimize_sysctl":            "优化内核参数",
		"create_mysql_user":          "创建 MySQL 系统用户",
		"create_directories":         "创建实例目录",
		"upload_mysql_package":       "下载 MySQL 安装包",
		"extract_mysql":              "解压 MySQL",
		"create_symlink":             "创建安装目录链接",
		"check_mysql_binary":         "验证 MySQL 二进制",
		"setup_env":                  "配置 MySQL 环境变量",
		"generate_mycnf":             "生成 my.cnf",
		"generate_systemd":           "生成 systemd 服务",
		"initialize_mysql":           "初始化 MySQL 数据目录",
		"start_mysql":                "启动 MySQL",
		"wait_mysql_ready":           "等待 MySQL 就绪",
		"set_root_password":          "设置 root 密码",
		"verify_mysql":               "验证 MySQL 服务",
		"install_pt_tools":           "安装 PT 工具（Percona Toolkit）",
		"install_xtrabackup":         "安装并验证 Percona XtraBackup",
		"configure_memory_allocator": "安装并启用 tcmalloc（可选）",
		"init_accounts":              "创建数据库用户并授权",
		"ensure_heartbeat_table":     "初始化心跳表",
		"setup_agent_collect_config": "配置 Agent 采集",
	}
	if label := labels[name]; label != "" {
		return label
	}
	return "等待 Agent 执行"
}

// normalizeInstallAccounts 对安装任务中的账户规格进行标准化处理。
func normalizeInstallAccounts(input []taskdomain.MySQLAccountSpec) []taskdomain.MySQLAccountSpec {
	items := make([]mysqlapp.AccountSpec, 0, len(input))
	for _, item := range input {
		items = append(items, mysqlapp.AccountSpec{
			Role:           item.Role,
			Username:       item.Username,
			Password:       item.Password,
			Host:           item.Host,
			Enabled:        item.Enabled,
			ExtendedBackup: item.ExtendedBackup,
			Privileges:     item.Privileges,
		})
	}
	normalized := mysqlapp.NormalizeAccountSpecs(items)
	out := make([]taskdomain.MySQLAccountSpec, 0, len(normalized))
	for _, item := range normalized {
		out = append(out, taskdomain.MySQLAccountSpec{
			Role:           item.Role,
			Username:       item.Username,
			Password:       item.Password,
			Host:           item.Host,
			Enabled:        item.Enabled,
			ExtendedBackup: item.ExtendedBackup,
			Privileges:     item.Privileges,
		})
	}
	return out
}

// ensureXtraBackupAccountPrivileges makes the enabled backup account usable by
// XtraBackup immediately after installation without changing disabled accounts.
func ensureXtraBackupAccountPrivileges(accounts []taskdomain.MySQLAccountSpec) []taskdomain.MySQLAccountSpec {
	for i := range accounts {
		if accounts[i].Role != mysqlapp.AccountRoleBackup || !accounts[i].Enabled {
			continue
		}
		accounts[i].ExtendedBackup = true
		found := false
		for _, privilege := range accounts[i].Privileges {
			if strings.EqualFold(strings.TrimSpace(privilege), "BACKUP_ADMIN") {
				found = true
				break
			}
		}
		if !found {
			accounts[i].Privileges = append(accounts[i].Privileges, "BACKUP_ADMIN")
		}
	}
	return accounts
}
