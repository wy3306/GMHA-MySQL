package handler

import (
	"encoding/json"
	"net/http"

	"gmha/internal/app"
	taskdomain "gmha/internal/domain/task"
)

type MySQLHandler struct{ service *app.MySQLService }

func NewMySQLHandler(service *app.MySQLService) *MySQLHandler { return &MySQLHandler{service: service} }

func (h *MySQLHandler) HandleInstances(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		items, err := h.service.ListInstanceViews(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, items)
	case http.MethodDelete:
		var req struct {
			Machine string `json:"machine"`
			Port    int    `json:"port"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if err := h.service.ForgetInstance(r.Context(), req.Machine, req.Port); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"machine": req.Machine, "port": req.Port})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (h *MySQLHandler) HandleAccountPresets(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		items, err := h.service.AccountPresets(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, items)
	case http.MethodPut:
		var items []taskdomain.MySQLAccountSpec
		if err := json.NewDecoder(r.Body).Decode(&items); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		items, err := h.service.SaveAccountPresets(r.Context(), items)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, items)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}
