// Package handler 提供 GMHA HTTP API 的请求处理器，负责将 HTTP 请求路由到对应的应用服务。
package handler

import (
	"encoding/json"
	"net/http"
	"strings"

	"gmha/internal/app"
	agentusecase "gmha/internal/usecase/agent"
)

// AgentHandler 是 Agent 相关 HTTP API 的请求处理器。
type AgentHandler struct {
	service  *app.AgentService
	recovery *app.RecoveryService
}

// NewAgentHandler 创建一个新的 AgentHandler 实例。
func NewAgentHandler(service *app.AgentService, recovery *app.RecoveryService) *AgentHandler {
	return &AgentHandler{service: service, recovery: recovery}
}

// HandleAgents 处理 Agent 列表查询请求，支持按 IP 查询、按 pending 状态过滤。
func (h *AgentHandler) HandleAgents(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		if r.URL.Query().Get("pending") == "true" {
			items, err := h.service.ListInstallCandidates(r.Context())
			if err != nil {
				writeError(w, http.StatusInternalServerError, err)
				return
			}
			writeJSON(w, http.StatusOK, items)
			return
		}
		if ip := strings.TrimSpace(r.URL.Query().Get("ip")); ip != "" {
			item, ok, err := h.service.GetViewByIP(r.Context(), ip)
			if err != nil {
				writeError(w, http.StatusBadRequest, err)
				return
			}
			if !ok {
				writeError(w, http.StatusNotFound, http.ErrMissingFile)
				return
			}
			writeJSON(w, http.StatusOK, item)
			return
		}
		items, err := h.service.ListViews(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, items)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

type retryInstallRequest struct {
	IP         string `json:"ip"`
	InstallDir string `json:"install_dir"`
}

type uninstallAgentRequest struct {
	IP string `json:"ip"`
}

type recoveryRequest struct {
	IP string `json:"ip"`
}

type upgradeAgentRequest struct {
	IP string `json:"ip"`
}

// HandleRetryInstall 处理 Agent 重试安装请求。
func (h *AgentHandler) HandleRetryInstall(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req retryInstallRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	resp, err := h.service.RetryInstallByIP(r.Context(), agentusecase.InstallAgentRequest{
		IP:         req.IP,
		InstallDir: req.InstallDir,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// HandleUninstall 处理 Agent 卸载请求。
func (h *AgentHandler) HandleUninstall(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req uninstallAgentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	resp, err := h.service.UninstallByIP(r.Context(), req.IP)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// HandleUpgrade 处理 Agent 升级请求。
func (h *AgentHandler) HandleUpgrade(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req upgradeAgentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	resp, err := h.service.UpgradeByIP(r.Context(), req.IP)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

type registerRequest struct {
	IP string `json:"ip"`
}

// HandleRegister 处理 Agent 注册请求。
func (h *AgentHandler) HandleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req registerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := h.service.Register(r.Context(), req.IP); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ip": req.IP, "state": "online"})
}

// HandleHeartbeat 处理 Agent 心跳请求。
func (h *AgentHandler) HandleHeartbeat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req registerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := h.service.Heartbeat(r.Context(), req.IP); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ip": req.IP, "state": "online"})
}

// HandleRecoveryTasks 处理恢复任务列表查询请求。
func (h *AgentHandler) HandleRecoveryTasks(w http.ResponseWriter, r *http.Request) {
	if h.recovery == nil {
		writeError(w, http.StatusNotImplemented, http.ErrNotSupported)
		return
	}
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	items, err := h.recovery.ListRecent(r.Context(), 20)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

// HandleRecover 处理手动触发 Agent 恢复请求。
func (h *AgentHandler) HandleRecover(w http.ResponseWriter, r *http.Request) {
	if h.recovery == nil {
		writeError(w, http.StatusNotImplemented, http.ErrNotSupported)
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req recoveryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	resp, err := h.recovery.TriggerManualRecoverByIP(r.Context(), req.IP)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}
