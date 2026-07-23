package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"gmha/internal/app"
	flamegraphdomain "gmha/internal/domain/flamegraph"
)

type FlameGraphHandler struct{ service *app.FlameGraphService }

func NewFlameGraphHandler(service *app.FlameGraphService) *FlameGraphHandler {
	return &FlameGraphHandler{service: service}
}

func (h *FlameGraphHandler) HandleProfiles(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		items, err := h.service.ListProfiles(r.Context(), strings.TrimSpace(r.URL.Query().Get("cluster")), limit)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": len(items)})
	case http.MethodPost:
		var req app.FlameGraphCaptureRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if req.Backend == "" {
			req.Backend = flamegraphdomain.BackendAuto
		}
		item, err := h.service.Capture(r.Context(), req)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusCreated, item)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (h *FlameGraphHandler) HandleProfileByID(w http.ResponseWriter, r *http.Request) {
	id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/performance/flamegraphs/"), "/")
	if id == "" || strings.Contains(id, "/") {
		writeError(w, http.StatusBadRequest, errors.New("invalid flame graph profile path"))
		return
	}
	switch r.Method {
	case http.MethodGet:
		item, ok, err := h.service.GetProfile(r.Context(), id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		if !ok {
			writeError(w, http.StatusNotFound, errors.New("火焰图记录不存在"))
			return
		}
		writeJSON(w, http.StatusOK, item)
	case http.MethodDelete:
		if err := h.service.DeleteProfile(r.Context(), id); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"id": id})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

type flameGraphScheduleRequest struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	MachineID       string    `json:"machine_id"`
	TargetType      string    `json:"target_type"`
	Target          string    `json:"target"`
	DurationSec     int       `json:"duration_seconds"`
	FrequencyHz     int       `json:"frequency_hz"`
	Backend         string    `json:"backend"`
	ScheduleType    string    `json:"schedule_type"`
	IntervalMinutes int       `json:"interval_minutes"`
	StartAt         time.Time `json:"start_at"`
	Enabled         *bool     `json:"enabled"`
}

func (h *FlameGraphHandler) HandleSchedules(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		items, err := h.service.ListSchedules(r.Context(), strings.TrimSpace(r.URL.Query().Get("cluster")))
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": len(items)})
	case http.MethodPost:
		var req flameGraphScheduleRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		enabled := true
		if req.Enabled != nil {
			enabled = *req.Enabled
		}
		if req.Backend == "" {
			req.Backend = flamegraphdomain.BackendAuto
		}
		item, err := h.service.SaveSchedule(r.Context(), flamegraphdomain.Schedule{
			ID: req.ID, Name: req.Name, MachineID: req.MachineID, TargetType: req.TargetType, Target: req.Target,
			DurationSec: req.DurationSec, FrequencyHz: req.FrequencyHz, Backend: req.Backend,
			ScheduleType: req.ScheduleType, IntervalMinutes: req.IntervalMinutes, StartAt: req.StartAt, Enabled: enabled,
		})
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusCreated, item)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (h *FlameGraphHandler) HandleScheduleByID(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/performance/flamegraphs/schedules/"), "/")
	parts := strings.Split(path, "/")
	if len(parts) == 2 && parts[1] == "run" && r.Method == http.MethodPost {
		item, err := h.service.RunSchedule(r.Context(), parts[0])
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusCreated, item)
		return
	}
	if len(parts) == 1 && parts[0] != "" && r.Method == http.MethodDelete {
		if err := h.service.DeleteSchedule(r.Context(), parts[0]); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"id": parts[0]})
		return
	}
	w.WriteHeader(http.StatusMethodNotAllowed)
}
