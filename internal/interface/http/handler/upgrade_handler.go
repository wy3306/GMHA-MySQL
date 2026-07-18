package handler

import (
	"encoding/json"
	"net/http"
	"strings"

	"gmha/internal/app"
)

type UpgradeHandler struct{ service *app.UpgradeService }

func NewUpgradeHandler(service *app.UpgradeService) *UpgradeHandler {
	return &UpgradeHandler{service: service}
}

func (h *UpgradeHandler) HandleOverview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	item, err := h.service.Overview(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (h *UpgradeHandler) HandleJobs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": h.service.List()})
}

func (h *UpgradeHandler) HandleJob(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/upgrades/")
	item, ok := h.service.Get(id)
	if !ok {
		writeError(w, http.StatusNotFound, http.ErrMissingFile)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (h *UpgradeHandler) HandleAgent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		PackageName string   `json:"package_name"`
		Targets     []string `json:"targets"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	item, err := h.service.StartAgentUpgrade(req.PackageName, req.Targets)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusAccepted, item)
}

func (h *UpgradeHandler) HandleManager(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		PackageName string `json:"package_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	item, err := h.service.StartManagerUpgrade(req.PackageName)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusAccepted, item)
}
