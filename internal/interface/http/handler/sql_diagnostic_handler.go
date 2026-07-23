package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"gmha/internal/app"
	sqldomain "gmha/internal/domain/sqldiagnostic"
)

type SQLDiagnosticHandler struct{ service *app.SQLDiagnosticService }

func NewSQLDiagnosticHandler(service *app.SQLDiagnosticService) *SQLDiagnosticHandler {
	return &SQLDiagnosticHandler{service: service}
}

func (h *SQLDiagnosticHandler) HandleExplain(w http.ResponseWriter, r *http.Request) {
	if h.service == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("sql diagnostic service is unavailable"))
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		MachineID string `json:"machine_id"`
		Port      int    `json:"port"`
		Database  string `json:"database"`
		SQL       string `json:"sql"`
	}
	if err := decodeStrictJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	result, err := h.service.Explain(r.Context(), app.SQLExplainRequest{
		MachineID: req.MachineID, Port: req.Port, Database: req.Database, SQL: req.SQL,
	})
	if err != nil {
		if errors.Is(err, app.ErrSQLExplainInvalid) {
			writeError(w, http.StatusBadRequest, err)
		} else {
			writeError(w, http.StatusBadGateway, err)
		}
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *SQLDiagnosticHandler) HandleConfig(w http.ResponseWriter, r *http.Request) {
	if h.service == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("sql diagnostic service is unavailable"))
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, h.service.Config())
	case http.MethodPut:
		var cfg sqldomain.Config
		if err := decodeStrictJSON(r, &cfg); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		saved, err := h.service.SaveConfig(r.Context(), cfg)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, saved)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (h *SQLDiagnosticHandler) HandleCurrent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	port, err := optionalPositiveInt(r.URL.Query().Get("port"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	result, err := h.service.Current(r.Context(), r.URL.Query().Get("cluster"), r.URL.Query().Get("machine"), port)
	hasReachableInstance := false
	for _, status := range result.Statuses {
		if status.Status != "error" {
			hasReachableInstance = true
			break
		}
	}
	if err != nil && !hasReachableInstance {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *SQLDiagnosticHandler) HandleHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	query, err := diagnosticHistoryQuery(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	result, err := h.service.History(r.Context(), query)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *SQLDiagnosticHandler) HandleTop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	query, err := diagnosticHistoryQuery(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	result, err := h.service.TopSQL(r.Context(), query, r.URL.Query().Get("order_by"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *SQLDiagnosticHandler) HandleSlow(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	query, err := diagnosticHistoryQuery(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	var threshold int64
	if raw := strings.TrimSpace(r.URL.Query().Get("threshold_ms")); raw != "" {
		threshold, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || threshold <= 0 {
			writeError(w, http.StatusBadRequest, errors.New("threshold_ms must be a positive integer"))
			return
		}
	}
	result, err := h.service.SlowSQL(r.Context(), query, threshold)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *SQLDiagnosticHandler) HandleKill(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		MachineID         string `json:"machine_id"`
		Port              int    `json:"port"`
		ProcessID         uint64 `json:"process_id"`
		ExpectedDigest    string `json:"expected_digest"`
		ExpectedStartedAt string `json:"expected_started_at"`
		Confirmation      string `json:"confirmation"`
		Reason            string `json:"reason"`
	}
	if err := decodeStrictJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	startedAt, err := time.Parse(time.RFC3339Nano, req.ExpectedStartedAt)
	if err != nil {
		writeError(w, http.StatusBadRequest, errors.New("expected_started_at must be RFC3339"))
		return
	}
	result, err := h.service.KillQuery(r.Context(), app.KillSQLRequest{
		MachineID: req.MachineID, Port: req.Port, ProcessID: req.ProcessID,
		ExpectedDigest: req.ExpectedDigest, ExpectedStartedAt: startedAt,
		Confirmation: req.Confirmation, Reason: req.Reason,
		RequestSource: diagnosticRequestSource(r),
	})
	if err != nil {
		switch {
		case errors.Is(err, app.ErrSQLDiagnosticConflict):
			writeError(w, http.StatusConflict, err)
		case errors.Is(err, app.ErrSQLDiagnosticForbidden):
			writeError(w, http.StatusForbidden, err)
		default:
			writeError(w, http.StatusBadRequest, err)
		}
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *SQLDiagnosticHandler) HandleKillAudits(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	start, end, err := diagnosticTimeRange(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	items, err := h.service.KillAudits(r.Context(), start, end)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	cluster := strings.TrimSpace(r.URL.Query().Get("cluster"))
	machine := strings.TrimSpace(r.URL.Query().Get("machine"))
	port, err := optionalPositiveInt(r.URL.Query().Get("port"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if cluster != "" || machine != "" || port > 0 {
		filtered := items[:0]
		for _, item := range items {
			if cluster != "" && !strings.EqualFold(item.Instance.Cluster, cluster) {
				continue
			}
			if machine != "" &&
				!strings.EqualFold(item.Instance.MachineID, machine) &&
				!strings.EqualFold(item.Instance.MachineName, machine) &&
				!strings.EqualFold(item.Instance.MachineIP, machine) {
				continue
			}
			if port > 0 && item.Instance.Port != port {
				continue
			}
			filtered = append(filtered, item)
		}
		items = filtered
	}
	writeJSON(w, http.StatusOK, map[string]any{"start": start, "end": end, "items": items})
}

func diagnosticHistoryQuery(r *http.Request) (app.SQLDiagnosticHistoryQuery, error) {
	start, end, err := diagnosticTimeRange(r)
	if err != nil {
		return app.SQLDiagnosticHistoryQuery{}, err
	}
	port, err := optionalPositiveInt(r.URL.Query().Get("port"))
	if err != nil {
		return app.SQLDiagnosticHistoryQuery{}, err
	}
	limit, err := optionalPositiveInt(r.URL.Query().Get("limit"))
	if err != nil {
		return app.SQLDiagnosticHistoryQuery{}, err
	}
	offset := 0
	if raw := strings.TrimSpace(r.URL.Query().Get("offset")); raw != "" {
		offset, err = strconv.Atoi(raw)
		if err != nil || offset < 0 {
			return app.SQLDiagnosticHistoryQuery{}, errors.New("offset must be a non-negative integer")
		}
	}
	sortDirection := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("direction")))
	if sortDirection != "" && sortDirection != "asc" && sortDirection != "desc" {
		return app.SQLDiagnosticHistoryQuery{}, errors.New("direction must be asc or desc")
	}
	return app.SQLDiagnosticHistoryQuery{
		Start: start, End: end, Cluster: r.URL.Query().Get("cluster"),
		Machine: r.URL.Query().Get("machine"), Port: port, User: r.URL.Query().Get("user"),
		Database: r.URL.Query().Get("database"), Keyword: r.URL.Query().Get("keyword"),
		SortBy: r.URL.Query().Get("sort_by"), SortDirection: sortDirection,
		Limit: limit, Offset: offset,
	}, nil
}

func diagnosticTimeRange(r *http.Request) (time.Time, time.Time, error) {
	var start, end time.Time
	var err error
	if raw := strings.TrimSpace(r.URL.Query().Get("start")); raw != "" {
		start, err = time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			return time.Time{}, time.Time{}, errors.New("start must be RFC3339")
		}
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("end")); raw != "" {
		end, err = time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			return time.Time{}, time.Time{}, errors.New("end must be RFC3339")
		}
	}
	return start, end, nil
}

func optionalPositiveInt(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("%q must be a positive integer", raw)
	}
	return value, nil
}

func decodeStrictJSON(r *http.Request, target any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(nilResponseWriter{}, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("request body must contain one JSON object")
		}
		return err
	}
	return nil
}

// MaxBytesReader only calls Header/WriteHeader on overflow. A no-op writer keeps
// the decoder helper reusable while handlers retain control of the final error.
type nilResponseWriter struct{}

func (nilResponseWriter) Header() http.Header       { return make(http.Header) }
func (nilResponseWriter) Write([]byte) (int, error) { return 0, nil }
func (nilResponseWriter) WriteHeader(int)           {}

func diagnosticRequestSource(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	userAgent := strings.TrimSpace(r.UserAgent())
	if len(userAgent) > 160 {
		userAgent = userAgent[:160]
	}
	return fmt.Sprintf("remote=%s; user_agent=%s", host, userAgent)
}
