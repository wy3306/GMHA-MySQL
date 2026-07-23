package handler

import (
	"encoding/json"
	"net/http"

	"gmha/internal/app"
)

// ManagerHandler 暴露 Manager 运行时控制台 API，复用 CLI 使用的 ManagerRuntimeService。
type ManagerHandler struct{ runtime *app.ManagerRuntimeService }

func NewManagerHandler(runtime *app.ManagerRuntimeService) *ManagerHandler {
	return &ManagerHandler{runtime: runtime}
}

func (h *ManagerHandler) HandleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	status, err := h.runtime.AdoptCurrentProcess()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	status.Config = status.Config.Redacted()
	writeJSON(w, http.StatusOK, status)
}

func (h *ManagerHandler) HandleDatabaseTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var cfg app.ManagerRuntimeConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	result, err := h.runtime.TestDatabase(r.Context(), cfg)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *ManagerHandler) HandleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		status, err := h.runtime.GetStatus(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, status.Config.Redacted())
	case http.MethodPut:
		var req struct {
			app.ManagerRuntimeConfig
			TestToken string `json:"test_token"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if err := h.runtime.SaveConfigVerified(r.Context(), req.ManagerRuntimeConfig, req.TestToken); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		status, err := h.runtime.GetStatus(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, status.Config.Redacted())
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (h *ManagerHandler) HandleAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Config app.ManagerRuntimeConfig `json:"config"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	// The action endpoint itself proves that this process is the active Web
	// Manager. Repair stale/missing runtime state before deciding self-control.
	if _, err := h.runtime.AdoptCurrentProcess(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	var (
		status app.ManagerRuntimeStatus
		err    error
	)
	switch r.URL.Path {
	case "/api/v1/manager/start":
		status, err = h.runtime.Start(r.Context(), req.Config)
	case "/api/v1/manager/restart":
		if h.runtime.IsCurrentProcess() {
			status, err = h.runtime.RestartCurrentProcess(req.Config)
		} else {
			status, err = h.runtime.Restart(r.Context(), req.Config)
		}
	case "/api/v1/manager/stop":
		if h.runtime.IsCurrentProcess() {
			status, err = h.runtime.StopCurrentProcess()
		} else {
			err = h.runtime.Stop(r.Context())
			if err == nil {
				status, err = h.runtime.GetStatus(r.Context())
			}
		}
	default:
		http.NotFound(w, r)
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}
