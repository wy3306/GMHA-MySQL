package handler

import (
	"encoding/json"
	"errors"
	"net/http"

	"gmha/internal/app"
	taskdomain "gmha/internal/domain/task"
)

type MySQLHandler struct {
	service    *app.MySQLService
	histograms *app.HistogramService
}

func NewMySQLHandler(service *app.MySQLService, histograms ...*app.HistogramService) *MySQLHandler {
	handler := &MySQLHandler{service: service}
	if len(histograms) > 0 {
		handler.histograms = histograms[0]
	}
	return handler
}

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

func (h *MySQLHandler) HandleHistograms(w http.ResponseWriter, r *http.Request) {
	if h.histograms == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("histogram service is unavailable"))
		return
	}
	switch r.Method {
	case http.MethodGet:
		port, err := optionalPositiveInt(r.URL.Query().Get("port"))
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		result, err := h.histograms.Inspect(r.Context(), app.HistogramInspectRequest{
			MachineID: r.URL.Query().Get("machine_id"),
			Port:      port,
			Schema:    r.URL.Query().Get("schema"),
			Table:     r.URL.Query().Get("table"),
		})
		if err != nil {
			if errors.Is(err, app.ErrHistogramUnsupported) {
				writeError(w, http.StatusUnprocessableEntity, err)
			} else {
				writeError(w, http.StatusBadGateway, err)
			}
			return
		}
		writeJSON(w, http.StatusOK, result)
	case http.MethodPost, http.MethodDelete:
		var req struct {
			MachineID string   `json:"machine_id"`
			Port      int      `json:"port"`
			Schema    string   `json:"schema"`
			Table     string   `json:"table"`
			Columns   []string `json:"columns"`
			Buckets   int      `json:"buckets,omitempty"`
		}
		if err := decodeStrictJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		input := app.HistogramManageRequest{
			MachineID: req.MachineID, Port: req.Port, Schema: req.Schema,
			Table: req.Table, Columns: req.Columns, Buckets: req.Buckets,
		}
		var (
			result any
			err    error
		)
		if r.Method == http.MethodPost {
			result, err = h.histograms.Update(r.Context(), input)
		} else {
			result, err = h.histograms.Drop(r.Context(), input)
		}
		if err != nil {
			if errors.Is(err, app.ErrHistogramUnsupported) {
				writeError(w, http.StatusUnprocessableEntity, err)
			} else {
				writeError(w, http.StatusBadRequest, err)
			}
			return
		}
		writeJSON(w, http.StatusOK, result)
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
