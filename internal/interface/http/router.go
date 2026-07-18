// Package http 提供 GMHA HTTP 服务器和路由配置，负责将 API 请求路由到对应的处理器。
package http

import (
	"context"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"gmha/internal/app"
	"gmha/internal/interface/http/handler"
)

// NewRouter 创建并配置 HTTP 路由，注册所有 API 端点。
func NewRouter(core *app.App) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/", frontendHandler())
	machineHandler := handler.NewMachineHandler(core.MachineService)
	managerHandler := handler.NewManagerHandler(core.ManagerRuntime)
	upgradeHandler := handler.NewUpgradeHandler(core.UpgradeService)
	agentHandler := handler.NewAgentHandler(core.AgentService, core.RecoveryService)
	mysqlHandler := handler.NewMySQLHandler(core.MySQLService)
	taskHandler := handler.NewTaskHandler(core.TaskService)
	packageHandler := handler.NewPackageHandler(core.PackageService)
	dynamicHandler := handler.NewDynamicCollectHandler(core.HeartbeatService, core.AlertService)
	haHandler := handler.NewHAHandler(core.HAService)
	topologyHandler := handler.NewClusterTopologyHandler(core.MachineService, core.MySQLService, core.HeartbeatService)
	backupHandler := handler.NewBackupHandler(core.BackupService)
	alertHandler := handler.NewAlertHandler(core.AlertService, core.HeartbeatService)
	mux.HandleFunc("/api/v1/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("/api/v1/machines", machineHandler.HandleMachines)
	mux.HandleFunc("/api/v1/machines/batch-delete", machineHandler.HandleBatchDeleteMachines)
	mux.HandleFunc("/api/v1/machines/precheck", machineHandler.HandlePrecheck)
	mux.HandleFunc("/api/v1/machines/cleanup", machineHandler.HandleCleanup)
	mux.HandleFunc("/api/v1/manager/status", managerHandler.HandleStatus)
	mux.HandleFunc("/api/v1/manager/config", managerHandler.HandleConfig)
	mux.HandleFunc("/api/v1/manager/database/test", managerHandler.HandleDatabaseTest)
	mux.HandleFunc("/api/v1/manager/start", managerHandler.HandleAction)
	mux.HandleFunc("/api/v1/manager/restart", managerHandler.HandleAction)
	mux.HandleFunc("/api/v1/manager/stop", managerHandler.HandleAction)
	mux.HandleFunc("/api/v1/upgrades/overview", upgradeHandler.HandleOverview)
	mux.HandleFunc("/api/v1/upgrades/jobs", upgradeHandler.HandleJobs)
	mux.HandleFunc("/api/v1/upgrades/agent", upgradeHandler.HandleAgent)
	mux.HandleFunc("/api/v1/upgrades/manager", upgradeHandler.HandleManager)
	mux.HandleFunc("/api/v1/upgrades/", upgradeHandler.HandleJob)
	mux.HandleFunc("/api/v1/machines/", machineHandler.HandleMachineByID)
	mux.HandleFunc("/api/v1/ssh-credentials", machineHandler.HandleCredentials)
	mux.HandleFunc("/api/v1/ssh-credentials/", machineHandler.HandleCredentialByID)
	mux.HandleFunc("/api/v1/clusters", machineHandler.HandleClusters)
	mux.HandleFunc("/api/v1/clusters/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(strings.Trim(r.URL.Path, "/"), "/machines") {
			machineHandler.HandleClusterMachines(w, r)
			return
		}
		if strings.HasSuffix(strings.Trim(r.URL.Path, "/"), "/topology") {
			topologyHandler.HandleTopology(w, r)
			return
		}
		if strings.HasSuffix(strings.Trim(r.URL.Path, "/"), "/cleanup") {
			machineHandler.HandleClusterCleanup(w, r)
			return
		}
		if strings.HasSuffix(strings.Trim(r.URL.Path, "/"), "/members") {
			machineHandler.HandleClusterMembers(w, r)
			return
		}
		if isHAClusterActionPath(r.URL.Path) {
			haHandler.HandleClusterActions(w, r)
			return
		}
		machineHandler.HandleClusterByName(w, r)
	})
	mux.HandleFunc("/api/v1/agents", agentHandler.HandleAgents)
	mux.HandleFunc("/api/v1/mysql/instances", mysqlHandler.HandleInstances)
	mux.HandleFunc("/api/v1/mysql/account-presets", mysqlHandler.HandleAccountPresets)
	mux.HandleFunc("/api/v1/agents/retry-install", agentHandler.HandleRetryInstall)
	mux.HandleFunc("/api/v1/agents/upgrade", agentHandler.HandleUpgrade)
	mux.HandleFunc("/api/v1/agents/detect-version", agentHandler.HandleDetectVersion)
	mux.HandleFunc("/api/v1/agents/repair-mysql-config", agentHandler.HandleRepairMySQLConfig)
	mux.HandleFunc("/api/v1/agents/uninstall", agentHandler.HandleUninstall)
	mux.HandleFunc("/api/v1/agents/recovery-tasks", agentHandler.HandleRecoveryTasks)
	mux.HandleFunc("/api/v1/agents/recover", agentHandler.HandleRecover)
	mux.HandleFunc("/api/v1/agents/register", agentHandler.HandleRegister)
	mux.HandleFunc("/api/v1/agents/heartbeat", agentHandler.HandleHeartbeat)
	mux.HandleFunc("/api/v1/tasks", taskHandler.HandleTasks)
	mux.HandleFunc("/api/v1/tasks/exec", taskHandler.HandleCreateExecTask)
	mux.HandleFunc("/api/v1/tasks/collect-machine-info", taskHandler.HandleCreateCollectMachineInfoTask)
	mux.HandleFunc("/api/v1/tasks/cluster-automation", taskHandler.HandleClusterAutomation)
	mux.HandleFunc("/api/v1/tasks/cluster-automation/report", taskHandler.HandleClusterAutomationReport)
	mux.HandleFunc("/api/v1/tasks/cluster-automation/artifacts/", taskHandler.HandleClusterAutomationArtifact)
	mux.HandleFunc("/api/v1/tasks/mysql-install", taskHandler.HandleCreateMySQLInstallTask)
	mux.HandleFunc("/api/v1/tasks/mysql-parameters", taskHandler.HandleMySQLParameters)
	mux.HandleFunc("/api/v1/tasks/mysql-users", taskHandler.HandleMySQLUsers)
	mux.HandleFunc("/api/v1/tasks/mysql-upgrade", taskHandler.HandleMySQLUpgrade)
	mux.HandleFunc("/api/v1/tasks/mysql-upgrade/precheck", taskHandler.HandleMySQLUpgradePrecheck)
	mux.HandleFunc("/api/v1/tasks/mysql-uninstall", taskHandler.HandleCreateMySQLUninstallTask)
	mux.HandleFunc("/api/v1/tasks/mysql-topology", taskHandler.HandleCreateMySQLTopologyTasks)
	mux.HandleFunc("/api/v1/tasks/cluster-mysql-install", taskHandler.HandleCreateClusterMySQLInstallTasks)
	mux.HandleFunc("/api/v1/tasks/cluster-mysql-uninstall", taskHandler.HandleCreateClusterMySQLUninstallTasks)
	mux.HandleFunc("/api/v1/mysql/packages", taskHandler.HandleMySQLPackages)
	mux.HandleFunc("/api/v1/backup/policies", backupHandler.HandlePolicies)
	mux.HandleFunc("/api/v1/backup/policies/", backupHandler.HandlePolicyByID)
	mux.HandleFunc("/api/v1/backup/runs", backupHandler.HandleRuns)
	mux.HandleFunc("/api/v1/backup/cluster-runs", backupHandler.HandleClusterRuns)
	mux.HandleFunc("/api/v1/backup/runs/", backupHandler.HandleRunByID)
	mux.HandleFunc("/api/v1/packages", packageHandler.HandlePackages)
	mux.HandleFunc("/api/v1/packages/fetch", packageHandler.HandleFetchPackage)
	mux.HandleFunc("/api/v1/packages/fetch-bundle", packageHandler.HandleFetchPackageBundle)
	mux.HandleFunc("/api/v1/packages/verify", packageHandler.HandleVerifyPackage)
	mux.HandleFunc("/api/v1/packages/delete", packageHandler.HandleDeleteForm)
	mux.HandleFunc("/api/v1/packages/", packageHandler.HandlePackageByPath)
	mux.HandleFunc("/api/v1/package-settings", packageHandler.HandleSettings)
	mux.HandleFunc("/api/v1/dynamic-collect/config", dynamicHandler.HandleConfig)
	mux.HandleFunc("/api/v1/mysql-dynamic-collect/config", dynamicHandler.HandleMySQLConfig)
	mux.HandleFunc("/api/v1/alerts/", alertHandler.Handle)
	mux.HandleFunc("/api/v1/software/mysql/", taskHandler.HandleMySQLPackageDownload)
	mux.Handle("/ws/agent/tasks", taskHandler.HandleAgentWS())
	return trackPlatformOperations(mux, core.TaskService)
}

func isHAClusterActionPath(path string) bool {
	trimmed := strings.Trim(path, "/")
	return strings.HasSuffix(trimmed, "/bootstrap") || strings.Contains(trimmed, "/vip/") || strings.Contains(trimmed, "/failover/") || strings.Contains(trimmed, "/architecture/")
}

// Serve 在指定地址启动 HTTP 服务器。
func Serve(core *app.App, listen string) error {
	lis, err := net.Listen("tcp", listen)
	if err != nil {
		return err
	}
	return ServeListener(context.Background(), core, lis)
}

// ServeListener 在指定监听器上启动 HTTP 服务器，支持优雅关闭。
func ServeListener(ctx context.Context, core *app.App, lis net.Listener) error {
	srv := &http.Server{
		Handler:           NewRouter(core),
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	log.Printf("gmha http listening on %s", lis.Addr().String())
	err := srv.Serve(lis)
	if err != nil && err != http.ErrServerClosed {
		return err
	}
	if ctx.Err() == nil {
		os.Exit(0)
	}
	return nil
}
