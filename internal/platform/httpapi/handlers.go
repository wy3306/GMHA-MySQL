package httpapi

import (
	"encoding/json"
	"net/http"

	"gmha/internal/domain"
	api "gmha/pkg/api/v1"
)

// handleIndex 处理首页请求，渲染并返回前端 HTML 页面。
func (s *server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = webPage.Execute(w, map[string]any{"PublicURL": s.dep.Config.PublicURL})
}

// handleHealth 处理健康检查请求，返回服务状态。
func (s *server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

// handleHosts 处理主机列表查询请求，返回所有已注册的主机信息。
func (s *server) handleHosts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	hosts, err := s.dep.HostService.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": hosts})
}

// handleBootstrap 处理主机引导请求，执行 Agent 部署和初始化流程。
func (s *server) handleBootstrap(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req api.BootstrapHostRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	resp, err := s.dep.BootstrapService.BootstrapHost(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleAgentRegister 处理 Agent 注册请求，验证引导令牌并完成注册。
func (s *server) handleAgentRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req api.AgentRegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := req.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	agent, err := s.dep.AgentService.Register(r.Context(), domain.Agent{
		HostID:        req.HostID,
		Hostname:      req.Hostname,
		AdvertiseAddr: req.AdvertiseAddr,
		Version:       req.Version,
	}, req.BootstrapToken)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"agent_id": agent.ID, "state": agent.State})
}

// handleAgentHeartbeat 处理 Agent 心跳请求，更新 Agent 的最后活跃时间。
func (s *server) handleAgentHeartbeat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req api.AgentHeartbeatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.HostID == "" {
		writeError(w, http.StatusBadRequest, "host_id is required")
		return
	}
	if err := s.dep.AgentService.Heartbeat(r.Context(), req.HostID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"state": domain.AgentStateOnline})
}
