// Package app 是应用服务层，负责编排领域对象、仓储和用例，提供统一的服务接口。
// App 结构体是整个应用的核心，持有所有服务实例，在 New() 中完成初始化和依赖注入。
package app

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
	agentdomain "gmha/internal/domain/agent"
	dynamicdomain "gmha/internal/domain/dynamic"
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
	DBPath           string // 兼容旧版 SQLite 文件路径
	DatabaseDriver   string // sqlite（默认）、mysql、postgres
	DatabaseDSN      string // 外部数据库连接串；SQLite 为空时使用 DBPath
	ManagerPublicKey string
	AgentBinaryPath  string
	ManagerHTTPAddr  string
	ManagerGRPCAddr  string
}

// App 是应用核心结构体，持有所有服务实例。
type App struct {
	db                    *sql.DB
	MachineService        *MachineService
	ClusterService        *ClusterService
	AgentService          *AgentService
	HeartbeatService      *HeartbeatService
	RecoveryService       *RecoveryService
	TaskService           *TaskService
	ClusterUpgradeService *ClusterUpgradeService
	MySQLService          *MySQLService
	HistogramService      *HistogramService
	BinlogAnalysisService *BinlogAnalysisService
	HAService             *HAService
	PackageService        *PackageService
	BackupService         *BackupService
	AlertService          *AlertService
	ManagerRuntime        *ManagerRuntimeService
	ManagerHA             *ManagerHAService
	UpgradeService        *UpgradeService
	SQLDiagnosticService  *SQLDiagnosticService
	FlameGraphService     *FlameGraphService
	AIService             *AIService
}

// New 创建并初始化应用核心实例。
// 初始化流程：创建 SQLite 数据库 → 运行所有表迁移 → 实例化仓储 → 创建用例 → 组装服务。
func New(cfg Config) (*App, error) {
	db, dialect, err := openDatabase(cfg)
	if err != nil {
		return nil, err
	}
	store := sqliteinfra.NewDB(db, dialect)

	machineRepo := sqliteinfra.NewMachineRepository(store)
	clusterRepo := sqliteinfra.NewClusterRepository(store)
	agentRepo := sqliteinfra.NewAgentRepository(store)
	heartbeatRepo := sqliteinfra.NewHeartbeatRepository(store)
	recoveryRepo := sqliteinfra.NewRecoveryRepository(store)
	machineInfoRepo := sqliteinfra.NewMachineInfoRepository(store)
	staticInfoRepo := sqliteinfra.NewStaticInfoRepository(store)
	mysqlInstanceRepo := sqliteinfra.NewMySQLInstanceRepository(store)
	mysqlAccountPresetRepo := sqliteinfra.NewMySQLAccountPresetRepository(store)
	haRepo := sqliteinfra.NewHARepository(store)
	taskRepo := sqliteinfra.NewTaskRepository(store)
	backupRepo := sqliteinfra.NewBackupRepository(store)
	alertRepo := sqliteinfra.NewAlertRepository(store)
	credentialRepo := sqliteinfra.NewCredentialRepository(store)
	sqlDiagnosticRepo := sqliteinfra.NewSQLDiagnosticRepository(store)
	flameGraphRepo := sqliteinfra.NewFlameGraphRepository(store)
	managerHARepo := sqliteinfra.NewManagerHARepository(store)
	aiRepo := sqliteinfra.NewAIRepository(store)
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
	if err := mysqlAccountPresetRepo.Migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := taskRepo.Migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := backupRepo.Migrate(); err != nil {
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
	if err := alertRepo.Migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := sqlDiagnosticRepo.Migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := flameGraphRepo.Migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := managerHARepo.Migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := aiRepo.Migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}

	sshClient := sshinfra.NewClient(cfg.ManagerPublicKey)
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
	alertService := NewAlertService(alertRepo)
	if err := alertService.EnsureDefaults(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	heartbeatService.SetAlertObserver(alertService)
	if saved, ok, err := alertRepo.LoadMetricConfig(context.Background(), "host"); err != nil {
		_ = db.Close()
		return nil, err
	} else if ok {
		var changed bool
		saved, changed = mergeDynamicCollectConfig(saved, dynamicdomain.BuildDefaultDynamicCollectConfig())
		if changed {
			if err := alertRepo.SaveMetricConfig(context.Background(), "host", saved); err != nil {
				_ = db.Close()
				return nil, err
			}
		}
		heartbeatService.UpdateDynamicCollectConfig(saved)
	}
	if saved, ok, err := alertRepo.LoadMetricConfig(context.Background(), "mysql"); err != nil {
		_ = db.Close()
		return nil, err
	} else if ok {
		var changed bool
		saved, changed = mergeDynamicCollectConfig(saved, dynamicdomain.BuildDefaultMySQLDynamicCollectConfig())
		if changed {
			if err := alertRepo.SaveMetricConfig(context.Background(), "mysql", saved); err != nil {
				_ = db.Close()
				return nil, err
			}
		}
		heartbeatService.UpdateMySQLDynamicCollectConfig(saved)
	}
	installAgent := agentusecase.NewInstallAgentUsecase(agentusecase.Dependencies{
		MachineRepo: machinedomain.Repository(machineRepo),
		AgentRepo:   agentdomain.Repository(agentRepo),
		SSHClient:   sshClient,
		Renderer:    renderer,
		Waiter:      heartbeatService,
	})
	upgradeAgent := agentusecase.NewUpgradeAgentUsecase(agentusecase.UpgradeDependencies{
		MachineRepo:    machinedomain.Repository(machineRepo),
		AgentRepo:      agentdomain.Repository(agentRepo),
		CredentialRepo: credentialRepo,
		SSHClient:      sshClient,
		Renderer:       renderer,
		Heartbeat:      heartbeatService,
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
	packageSelector := mysqlapp.NewPackageSelector(filepath.Join("software", "mysql"))
	home, _ := os.UserHomeDir()
	packageService, err := NewPackageService(filepath.Join(home, ".gmha", "package-store.json"), packageSelector)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	createMySQLInstallTask := taskusecase.NewCreateMySQLInstallTaskUsecase(
		machineRepo,
		agentRepo,
		machineInfoRepo,
		renderLoader,
		renderEngine,
		mysqlapp.NewCalculator(),
		packageSelector,
		packageService,
		cfg.ManagerHTTPAddr,
		func(targetIP string) string {
			return ResolveManagerHTTPAddrForTarget(cfg.ManagerHTTPAddr, targetIP)
		},
	)
	createMySQLUninstallTask := taskusecase.NewCreateMySQLUninstallTaskUsecase(machineRepo, agentRepo, mysqlInstanceRepo)
	createMySQLTopologyTask := taskusecase.NewCreateMySQLTopologyTaskUsecase(machineRepo, agentRepo, mysqlInstanceRepo)
	taskService := NewTaskService(taskdomain.Repository(taskRepo), createExecTask, createCollectTask, createStaticTask, createMySQLInstallTask, createMySQLUninstallTask, createMySQLTopologyTask, machineInfoRepo, staticInfoRepo, machineRepo, mysqlInstanceRepo)
	mysqlService := NewMySQLService(mysqlInstanceRepo, machinedomain.Repository(machineRepo), heartbeatService, mysqlAccountPresetRepo)
	histogramService := NewHistogramService(mysqlInstanceRepo, machinedomain.Repository(machineRepo), mysqlAccountPresetRepo)
	binlogAnalysisService := NewBinlogAnalysisService(mysqlInstanceRepo, machinedomain.Repository(machineRepo), mysqlAccountPresetRepo)
	sqlDiagnosticService, err := NewSQLDiagnosticService(sqlDiagnosticRepo, mysqlInstanceRepo, machinedomain.Repository(machineRepo), mysqlAccountPresetRepo)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	sqlDiagnosticService.Start()
	haService := NewHAService(haRepo, machinedomain.Repository(machineRepo), mysqlInstanceRepo, mysqlAccountPresetRepo)
	haService.ConfigureArchitectureExecutor(taskService)
	clusterUpgradeService := NewClusterUpgradeService(taskService, haService)
	if err := clusterUpgradeService.RecoverInterrupted(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	clusterService := NewClusterService(clusterRepo)
	agentService := NewAgentService(agentRepo, machineRepo, sshClient, heartbeatService, recoveryService, installAgent, upgradeAgent, uninstallAgent, taskService, mysqlService, cfg.AgentBinaryPath, cfg.ManagerHTTPAddr, cfg.ManagerGRPCAddr)
	agentService.SetCredentialRepository(credentialRepo)
	machineService := NewMachineService(onboard, machineRepo, clusterRepo, credentialRepo, machineInfoRepo, staticInfoRepo, recoveryRepo, sshClient, agentService, taskService)
	backupService := NewBackupService(backupRepo, taskService, machinedomain.Repository(machineRepo), mysqlInstanceRepo)
	machineService.ConfigureClusterDependencies(haService, backupService)
	taskService.ConfigureClusterSafetyDependencies(haService, backupService)
	backupService.Start()
	flameGraphService := NewFlameGraphService(flameGraphRepo, taskService, machinedomain.Repository(machineRepo))
	taskService.SetFlameGraphTaskResultSaver(flameGraphService)
	flameGraphService.Start()

	managerRuntime := NewManagerRuntimeService(cfg)
	managerRuntime.SetPlatformUsageChecker(func(ctx context.Context) (bool, error) {
		var total int
		err := db.QueryRowContext(ctx, `
			select
				(select count(*) from machines) +
				(select count(*) from clusters) +
				(select count(*) from tasks)
		`).Scan(&total)
		return total > 0, err
	})
	upgradeService := NewUpgradeService(filepath.Join(home, ".gmha", "upgrade-jobs.json"), packageService, agentService, managerRuntime)
	managerHAService := NewManagerHAService(managerHARepo, machineRepo, taskService, managerRuntime, machineService)
	aiService, err := NewAIService(aiRepo, alertService, machineService, taskService, filepath.Join(home, ".gmha", "ai-secret.key"))
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	aiService.ConfigurePlatformContext(haService, backupService)
	aiService.ConfigureClusterOperations(clusterUpgradeService)
	return &App{
		db:                    db,
		MachineService:        machineService,
		ClusterService:        clusterService,
		AgentService:          agentService,
		HeartbeatService:      heartbeatService,
		RecoveryService:       recoveryService,
		TaskService:           taskService,
		ClusterUpgradeService: clusterUpgradeService,
		MySQLService:          mysqlService,
		HistogramService:      histogramService,
		BinlogAnalysisService: binlogAnalysisService,
		HAService:             haService,
		PackageService:        packageService,
		BackupService:         backupService,
		AlertService:          alertService,
		ManagerRuntime:        managerRuntime,
		ManagerHA:             managerHAService,
		UpgradeService:        upgradeService,
		SQLDiagnosticService:  sqlDiagnosticService,
		FlameGraphService:     flameGraphService,
		AIService:             aiService,
	}, nil
}

func openDatabase(cfg Config) (*sql.DB, sqliteinfra.Dialect, error) {
	driver := strings.ToLower(strings.TrimSpace(cfg.DatabaseDriver))
	if driver == "" {
		driver = string(sqliteinfra.DialectSQLite)
	}
	dsn := strings.TrimSpace(cfg.DatabaseDSN)
	if dsn == "" {
		dsn = cfg.DBPath
	}
	if dsn == "" {
		dsn = "./data/manager.db"
	}

	var dialect sqliteinfra.Dialect
	switch driver {
	case "sqlite", "sqlite3":
		dialect, driver = sqliteinfra.DialectSQLite, "sqlite"
		if err := os.MkdirAll(filepath.Dir(dsn), 0o755); err != nil {
			return nil, "", err
		}
	case "mysql":
		dialect = sqliteinfra.DialectMySQL
	case "postgres", "postgresql":
		dialect, driver = sqliteinfra.DialectPostgres, "pgx"
	default:
		return nil, "", fmt.Errorf("unsupported database driver %q (supported: sqlite, mysql, postgres)", cfg.DatabaseDriver)
	}

	db, err := sql.Open(driver, dsn)
	if err != nil {
		return nil, "", err
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, "", err
	}
	if dialect == sqliteinfra.DialectSQLite {
		configureSQLite(db)
	} else {
		db.SetMaxOpenConns(20)
		db.SetMaxIdleConns(5)
	}
	return db, dialect, nil
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
	if a.BackupService != nil {
		a.BackupService.Close()
	}
	if a.SQLDiagnosticService != nil {
		a.SQLDiagnosticService.Close()
	}
	if a.FlameGraphService != nil {
		a.FlameGraphService.Close()
	}
	if a.BinlogAnalysisService != nil {
		a.BinlogAnalysisService.Close()
	}
	if a.AIService != nil {
		a.AIService.Close()
	}
	if a.db == nil {
		return nil
	}
	return a.db.Close()
}
