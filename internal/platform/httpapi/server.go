// Package httpapi 提供 GMHA 管理端的 HTTP API 服务，包括路由注册和请求处理。
package httpapi

import (
	"net/http"

	"gmha/internal/app/agent"
	"gmha/internal/app/bootstrap"
	"gmha/internal/app/host"
	"gmha/internal/platform/config"
	"gmha/internal/platform/web"
)

// Dependencies 是 HTTP 服务器的依赖注入结构体，包含配置和各业务服务实例。
type Dependencies struct {
	Config           config.ManagerConfig
	HostService      *host.Service
	AgentService     *agent.Service
	BootstrapService *bootstrap.Service
}

// NewServer 创建并返回一个配置好所有路由的 HTTP 服务器处理器。
func NewServer(dep Dependencies) http.Handler {
	s := &server{dep: dep}
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/api/v1/healthz", s.handleHealth)
	mux.HandleFunc("/api/v1/hosts", s.handleHosts)
	mux.HandleFunc("/api/v1/bootstrap", s.handleBootstrap)
	mux.HandleFunc("/api/v1/agents/register", s.handleAgentRegister)
	mux.HandleFunc("/api/v1/agents/heartbeat", s.handleAgentHeartbeat)
	mux.Handle("/assets/", http.StripPrefix("/assets/", web.Static()))
	return mux
}

// server 是 HTTP 服务器的内部实现，持有所有依赖项。
type server struct {
	dep Dependencies
}
