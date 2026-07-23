package handler

import (
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"strings"

	"gmha/internal/app"
	managerdomain "gmha/internal/domain/manager"
)

type ManagerHAHandler struct{ service *app.ManagerHAService }

func NewManagerHAHandler(service *app.ManagerHAService) *ManagerHAHandler {
	return &ManagerHAHandler{service: service}
}

func (h *ManagerHAHandler) HandleOverview(w http.ResponseWriter, r *http.Request) {
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

func (h *ManagerHAHandler) HandleConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req managerdomain.HAConfig
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	item, err := h.service.SaveConfig(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (h *ManagerHAHandler) HandleNodes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req app.AddManagerNodeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	item, err := h.service.AddNode(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusAccepted, item)
}

func (h *ManagerHAHandler) HandleInterfaces(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	prefix, _ := strconv.Atoi(r.URL.Query().Get("prefix"))
	item, err := h.service.NetworkInterfaces(
		r.Context(),
		r.URL.Query().Get("node_id"),
		r.URL.Query().Get("vip"),
		prefix,
		r.Method == http.MethodPost,
	)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (h *ManagerHAHandler) HandleNodeAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		NodeID string `json:"node_id"`
		Action string `json:"action"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	taskID, err := h.service.NodeAction(r.Context(), req.NodeID, req.Action)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"task_id": taskID, "status": "accepted"})
}

func (h *ManagerHAHandler) HandleVIPSwitch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		TargetNodeID string `json:"target_node_id"`
		Interface    string `json:"interface"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	result, err := h.service.SwitchVIP(r.Context(), req.TargetNodeID, req.Interface)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusAccepted, result)
}

func (h *ManagerHAHandler) HandleBootstrapConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	content, err := h.service.BootstrapConfig(r.URL.Query().Get("token"))
	if err != nil {
		writeError(w, http.StatusForbidden, err)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(content)
}

func (h *ManagerHAHandler) HandleBootstrapBinary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if err := h.service.ValidateBootstrapToken(r.URL.Query().Get("token")); err != nil {
		writeError(w, http.StatusForbidden, err)
		return
	}
	path, err := os.Executable()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	w.Header().Set("Content-Disposition", `attachment; filename="gmha"`)
	w.Header().Set("Cache-Control", "no-store")
	http.ServeFile(w, r, strings.TrimSpace(path))
}
