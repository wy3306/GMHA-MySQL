package task

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

// CreateMySQLInstallTaskRequest 是创建 MySQL 安装任务的请求参数。
type CreateMySQLInstallTaskRequest struct {
	Machine          string
	Port             int
	ServerID         int
	MySQLUser        string
	InstanceDir      string
	DataDir          string
	BinlogDir        string
	RedoDir          string
	UndoDir          string
	TmpDir           string
	BaseDir          string
	MyCnfPath        string
	SocketPath       string
	ErrorLog         string
	PIDFile          string
	CharacterSetsDir string
	PluginDir        string
	RootPassword     string
	Profile          string
	Accounts         []taskdomain.MySQLAccountSpec
}

// CreateMySQLInstallTaskResult 是创建 MySQL 安装任务的结果。
type CreateMySQLInstallTaskResult struct {
	Task   taskdomain.Task
	Steps  []taskdomain.Step
	Events []taskdomain.Event
}

// CreateMySQLInstallTaskUsecase 是创建 MySQL 安装任务的用例，负责计算配置参数并构建安装任务。
type CreateMySQLInstallTaskUsecase struct {
	machines        MachineRepository
	agents          AgentRepository
	machineInfo     MachineInfoRepository
	loader          *render.Loader
	engine          *render.Engine
	calculator      *mysqlapp.Calculator
	packageSelector *mysqlapp.PackageSelector
	managerHTTPAddr string
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
	managerHTTPAddr string,
) *CreateMySQLInstallTaskUsecase {
	return &CreateMySQLInstallTaskUsecase{
		machines:        machines,
		agents:          agents,
		machineInfo:     machineInfo,
		loader:          loader,
		engine:          engine,
		calculator:      calculator,
		packageSelector: selector,
		managerHTTPAddr: strings.TrimRight(managerHTTPAddr, "/"),
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
	pkg, err := u.packageSelector.Select(info)
	if err != nil {
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
	accountCheck := make([]mysqlapp.AccountSpec, 0, len(accounts))
	for _, item := range accounts {
		accountCheck = append(accountCheck, mysqlapp.AccountSpec{
			Role:           item.Role,
			Username:       item.Username,
			Password:       item.Password,
			Host:           item.Host,
			Enabled:        item.Enabled,
			ExtendedBackup: item.ExtendedBackup,
		})
	}
	if err := mysqlapp.ValidateAccountSpecs(accountCheck); err != nil {
		return CreateMySQLInstallTaskResult{}, err
	}

	spec := taskdomain.MySQLInstallSpec{
		Port:               input.Port,
		ServerID:           vars.ServerID,
		MySQLUser:          input.MySQLUser,
		InstanceDir:        input.InstanceDir,
		DataDir:            input.DataDir,
		BinlogDir:          input.BinlogDir,
		RedoDir:            input.RedoDir,
		UndoDir:            input.UndoDir,
		TmpDir:             input.TmpDir,
		BaseDir:            input.BaseDir,
		RootPassword:       req.RootPassword,
		Profile:            req.Profile,
		PackageName:        pkg.FileName,
		PackageDownloadURL: fmt.Sprintf("%s/api/v1/software/mysql/%s", strings.TrimRight(u.managerHTTPAddr, "/"), pkg.FileName),
		MyCnfPath:          input.MyCnfPath,
		MyCnfContent:       string(renderedMyCnf),
		SocketPath:         input.SocketPath,
		ErrorLog:           input.ErrorLog,
		PIDFile:            input.PIDFile,
		CharacterSetsDir:   input.CharacterSetsDir,
		PluginDir:          input.PluginDir,
		SystemdUnitName:    "mysqld",
		SystemdContent:     string(renderedSystemd),
		LimitsPath:         "/etc/security/limits.d/mysql.conf",
		LimitsContent:      string(renderedLimits),
		SysctlPath:         "/etc/sysctl.d/99-gmha-mysql.conf",
		SysctlContent:      string(renderedSysctl),
		EnvFilePath:        "/etc/profile.d/mysql.sh",
		EnvContent:         string(renderedEnv),
		Accounts:           accounts,
	}
	specJSON, _ := json.Marshal(spec)

	now := time.Now().UTC()
	taskID := fmt.Sprintf("task-%d", now.UnixNano())
	task := taskdomain.Task{
		ID:              taskID,
		Type:            taskdomain.TypeMySQLInstall,
		MachineID:       machine.ID,
		AgentID:         agent.ID,
		Status:          taskdomain.StatusPending,
		ProgressPercent: 0,
		CurrentStep:     "等待派发",
		SpecJSON:        specJSON,
		CreatedAt:       now,
	}
	steps := buildMySQLInstallSteps(taskID)
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

// renderTemplate 使用配置变量渲染指定名称的模板文件。
func (u *CreateMySQLInstallTaskUsecase) renderTemplate(name string, vars mysqlapp.ConfigVars) ([]byte, error) {
	source, err := u.loader.LoadTemplate("mysql", name)
	if err != nil {
		return nil, err
	}
	return u.engine.Render(name, string(source), vars)
}

// buildMySQLInstallSteps 构建 MySQL 安装任务的所有步骤。
func buildMySQLInstallSteps(taskID string) []taskdomain.Step {
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
		"initialize_mysql",
		"start_mysql",
		"wait_mysql_ready",
		"set_root_password",
		"verify_mysql",
		"init_accounts",
		"ensure_heartbeat_table",
		"setup_agent_collect_config",
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
		})
	}
	return out
}
