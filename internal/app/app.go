// Package app 是应用服务层，负责编排领域对象、仓储和用例，提供统一的服务接口。
// App 结构体是整个应用的核心，持有所有服务实例，在 New() 中完成初始化和依赖注入。
package app

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"

	agentdomain "gmha/internal/domain/agent"
	hbdomain "gmha/internal/domain/heartbeat"
	machinedomain "gmha/internal/domain/machine"
	taskdomain "gmha/internal/domain/task"
	sqliteinfra "gmha/internal/infrastructure/persistence/sqlite"
	renderinfra "gmha/internal/infrastructure/render"
	sshinfra "gmha/internal/infrastructure/ssh"
	mysqlapp "gmha/internal/mysql"
	agentusecase "gmha/internal/usecase/agent"
	machineusecase "gmha/internal/usecase/machine"
	taskusecase "gmha/internal/usecase/task"
	_ "modernc.org/sqlite"
)

// Config 是应用配置，包含数据库路径、SSH 公钥、Agent 二进制路径和 Manager 地址等。
type Config struct {
	DBPath           string
	ManagerPublicKey string
	AgentBinaryPath  string
	ManagerHTTPAddr  string
	ManagerGRPCAddr  string
}

// App 是应用核心结构体，持有所有服务实例。
type App struct {
	db               *sql.DB
	MachineService   *MachineService
	ClusterService   *ClusterService
	AgentService     *AgentService
	HeartbeatService *HeartbeatService
	RecoveryService  *RecoveryService
	TaskService      *TaskService
	MySQLService     *MySQLService
	HAService        *HAService
	ManagerRuntime   *ManagerRuntimeService
}

// New 创建并初始化应用核心实例。
// 初始化流程：创建 SQLite 数据库 → 运行所有表迁移 → 实例化仓储 → 创建用例 → 组装服务。
func New(cfg Config) (*App, error) {
	if err := os.MkdirAll(filepath.Dir(cfg.DBPath), 0o755); err != nil {
		return nil, err
	}

	db, err := sql.Open("sqlite", cfg.DBPath)
	if err != nil {
		return nil, err
	}
	configureSQLite(db)

	machineRepo := sqliteinfra.NewMachineRepository(db)
	clusterRepo := sqliteinfra.NewClusterRepository(db)
	agentRepo := sqliteinfra.NewAgentRepository(db)
	heartbeatRepo := sqliteinfra.NewHeartbeatRepository(db)
	recoveryRepo := sqliteinfra.NewRecoveryRepository(db)
	machineInfoRepo := sqliteinfra.NewMachineInfoRepository(db)
	staticInfoRepo := sqliteinfra.NewStaticInfoRepository(db)
	mysqlInstanceRepo := sqliteinfra.NewMySQLInstanceRepository(db)
	haRepo := sqliteinfra.NewHARepository(db)
	taskRepo := sqliteinfra.NewTaskRepository(db)
	credentialRepo := sqliteinfra.NewCredentialRepository(db)
	if err := machineRepo.Migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := clusterRepo.Migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := agentRepo.Migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := heartbeatRepo.Migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := recoveryRepo.Migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := machineInfoRepo.Migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := staticInfoRepo.Migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := mysqlInstanceRepo.Migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := taskRepo.Migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := credentialRepo.Migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := haRepo.Migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}

	sshClient := sshinfra.NewClient()
	trustService, err := sshinfra.NewTrustService(cfg.ManagerPublicKey, sshClient)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	renderer := renderinfra.NewRenderer()
	renderLoader := renderinfra.NewLoader("configs")
	renderEngine := renderinfra.NewEngine()

	onboard := machineusecase.NewOnboardUsecase(machineusecase.Dependencies{
		MachineRepo: machinedomain.Repository(machineRepo),
		SSHClient:   sshClient,
		Trust:       trustService,
	})
	heartbeatService := NewHeartbeatService(hbdomain.Repository(heartbeatRepo), HeartbeatConfig{}, agentRepo, machineRepo, mysqlInstanceRepo)
	if err := heartbeatService.LoadLatest(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	installAgent := agentusecase.NewInstallAgentUsecase(agentusecase.Dependencies{
		MachineRepo: machinedomain.Repository(machineRepo),
		AgentRepo:   agentdomain.Repository(agentRepo),
		SSHClient:   sshClient,
		Renderer:    renderer,
		Waiter:      heartbeatService,
	})
	upgradeAgent := agentusecase.NewUpgradeAgentUsecase(agentusecase.UpgradeDependencies{
		MachineRepo: machinedomain.Repository(machineRepo),
		AgentRepo:   agentdomain.Repository(agentRepo),
		SSHClient:   sshClient,
		Renderer:    renderer,
		Heartbeat:   heartbeatService,
	})
	uninstallAgent := agentusecase.NewUninstallAgentUsecase(agentusecase.UninstallDependencies{
		MachineRepo: machinedomain.Repository(machineRepo),
		AgentRepo:   agentdomain.Repository(agentRepo),
		SSHClient:   sshClient,
	})
	recoveryExecutor := sshinfra.NewRecoveryExecutor(sshClient)
	recoveryService := NewRecoveryService(recoveryRepo, machineRepo, agentRepo, heartbeatService, recoveryExecutor)
	createExecTask := taskusecase.NewCreateExecTaskUsecase(machineRepo, agentRepo)
	createCollectTask := taskusecase.NewCreateCollectMachineInfoUsecase(machineRepo, agentRepo)
	createStaticTask := taskusecase.NewCreateCollectStaticInfoUsecase(machineRepo, agentRepo)
	createMySQLInstallTask := taskusecase.NewCreateMySQLInstallTaskUsecase(
		machineRepo,
		agentRepo,
		machineInfoRepo,
		renderLoader,
		renderEngine,
		mysqlapp.NewCalculator(),
		mysqlapp.NewPackageSelector(filepath.Join("software", "mysql")),
		cfg.ManagerHTTPAddr,
	)
	createMySQLUninstallTask := taskusecase.NewCreateMySQLUninstallTaskUsecase(machineRepo, agentRepo, mysqlInstanceRepo)
	createMySQLTopologyTask := taskusecase.NewCreateMySQLTopologyTaskUsecase(machineRepo, agentRepo, mysqlInstanceRepo)
	taskService := NewTaskService(taskdomain.Repository(taskRepo), createExecTask, createCollectTask, createStaticTask, createMySQLInstallTask, createMySQLUninstallTask, createMySQLTopologyTask, machineInfoRepo, staticInfoRepo, machineRepo, mysqlInstanceRepo)
	mysqlService := NewMySQLService(mysqlInstanceRepo, machinedomain.Repository(machineRepo), heartbeatService)
	haService := NewHAService(haRepo, machinedomain.Repository(machineRepo), mysqlInstanceRepo)
	clusterService := NewClusterService(clusterRepo)
	agentService := NewAgentService(agentRepo, machineRepo, sshClient, heartbeatService, recoveryService, installAgent, upgradeAgent, uninstallAgent, taskService, mysqlService, cfg.AgentBinaryPath, cfg.ManagerHTTPAddr, cfg.ManagerGRPCAddr)
	machineService := NewMachineService(onboard, machineRepo, clusterRepo, credentialRepo, machineInfoRepo, staticInfoRepo, recoveryRepo, sshClient, agentService, taskService)

	return &App{
		db:               db,
		MachineService:   machineService,
		ClusterService:   clusterService,
		AgentService:     agentService,
		HeartbeatService: heartbeatService,
		RecoveryService:  recoveryService,
		TaskService:      taskService,
		MySQLService:     mysqlService,
		HAService:        haService,
		ManagerRuntime:   NewManagerRuntimeService(cfg),
	}, nil
}

func configureSQLite(db *sql.DB) {
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	_, _ = db.Exec(`
		pragma journal_mode = wal;
		pragma synchronous = normal;
		pragma busy_timeout = 10000;
		pragma foreign_keys = on;
	`)
}

func (a *App) Close() error {
	if a.db == nil {
		return nil
	}
	return a.db.Close()
}
