package handler

import (
	"encoding/json"
	"net/http"
	"strings"

	"gmha/internal/app"
)

type ClusterUpgradeHandler struct {
	service *app.ClusterUpgradeService
}

func NewClusterUpgradeHandler(service *app.ClusterUpgradeService) *ClusterUpgradeHandler {
	return &ClusterUpgradeHandler{service: service}
}

func (h *ClusterUpgradeHandler) HandlePlan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req app.ClusterUpgradeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	plan, err := h.service.Plan(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, plan)
}

func (h *ClusterUpgradeHandler) HandleStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req app.ClusterUpgradeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	run, err := h.service.Start(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusAccepted, run)
}

func (h *ClusterUpgradeHandler) HandleRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	runID := strings.TrimSpace(r.URL.Query().Get("run_id"))
	if runID == "" {
		writeError(w, http.StatusBadRequest, http.ErrMissingFile)
		return
	}
	run, found, err := h.service.Get(r.Context(), runID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, http.ErrMissingFile)
		return
	}
	writeJSON(w, http.StatusOK, run)
}
