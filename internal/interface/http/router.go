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
	machineHandler := handler.NewMachineHandler(core.MachineService)
	agentHandler := handler.NewAgentHandler(core.AgentService, core.RecoveryService)
	taskHandler := handler.NewTaskHandler(core.TaskService)
	dynamicHandler := handler.NewDynamicCollectHandler(core.HeartbeatService)
	haHandler := handler.NewHAHandler(core.HAService)
	mux.HandleFunc("/api/v1/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("/api/v1/machines", machineHandler.HandleMachines)
	mux.HandleFunc("/api/v1/machines/", machineHandler.HandleMachineByID)
	mux.HandleFunc("/api/v1/ssh-credentials", machineHandler.HandleCredentials)
	mux.HandleFunc("/api/v1/ssh-credentials/", machineHandler.HandleCredentialByID)
	mux.HandleFunc("/api/v1/clusters", machineHandler.HandleClusters)
	mux.HandleFunc("/api/v1/clusters/", func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/vip/") || strings.Contains(r.URL.Path, "/failover/") {
			haHandler.HandleClusterActions(w, r)
			return
		}
		machineHandler.HandleClusterByName(w, r)
	})
	mux.HandleFunc("/api/v1/agents", agentHandler.HandleAgents)
	mux.HandleFunc("/api/v1/agents/retry-install", agentHandler.HandleRetryInstall)
	mux.HandleFunc("/api/v1/agents/upgrade", agentHandler.HandleUpgrade)
	mux.HandleFunc("/api/v1/agents/uninstall", agentHandler.HandleUninstall)
	mux.HandleFunc("/api/v1/agents/recovery-tasks", agentHandler.HandleRecoveryTasks)
	mux.HandleFunc("/api/v1/agents/recover", agentHandler.HandleRecover)
	mux.HandleFunc("/api/v1/agents/register", agentHandler.HandleRegister)
	mux.HandleFunc("/api/v1/agents/heartbeat", agentHandler.HandleHeartbeat)
	mux.HandleFunc("/api/v1/tasks", taskHandler.HandleTasks)
	mux.HandleFunc("/api/v1/tasks/exec", taskHandler.HandleCreateExecTask)
	mux.HandleFunc("/api/v1/tasks/collect-machine-info", taskHandler.HandleCreateCollectMachineInfoTask)
	mux.HandleFunc("/api/v1/tasks/mysql-install", taskHandler.HandleCreateMySQLInstallTask)
	mux.HandleFunc("/api/v1/dynamic-collect/config", dynamicHandler.HandleConfig)
	mux.HandleFunc("/api/v1/mysql-dynamic-collect/config", dynamicHandler.HandleMySQLConfig)
	mux.HandleFunc("/api/v1/software/mysql/", taskHandler.HandleMySQLPackageDownload)
	mux.Handle("/ws/agent/tasks", taskHandler.HandleAgentWS())
	return mux
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
