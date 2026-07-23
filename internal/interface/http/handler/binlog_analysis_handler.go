package handler

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"gmha/internal/app"
)

type BinlogAnalysisHandler struct {
	service *app.BinlogAnalysisService
}

func NewBinlogAnalysisHandler(service *app.BinlogAnalysisService) *BinlogAnalysisHandler {
	return &BinlogAnalysisHandler{service: service}
}

func (h *BinlogAnalysisHandler) HandleCollection(w http.ResponseWriter, r *http.Request) {
	if h.service == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("binlog analysis service is unavailable"))
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{"items": h.service.List()})
	case http.MethodPost:
		var body struct {
			MachineID            string `json:"machine_id"`
			Port                 int    `json:"port"`
			StartTime            string `json:"start_time"`
			EndTime              string `json:"end_time"`
			StartFile            string `json:"start_file"`
			BigTxnMode           string `json:"big_txn_mode"`
			BigTxnRowsThreshold  int    `json:"big_txn_rows_threshold"`
			BigTxnBytesThreshold uint64 `json:"big_txn_bytes_threshold"`
		}
		if err := decodeStrictJSON(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		start, err := parseBinlogAnalysisTime(body.StartTime)
		if err != nil {
			writeError(w, http.StatusBadRequest, errors.New("开始时间格式不正确"))
			return
		}
		end, err := parseBinlogAnalysisTime(body.EndTime)
		if err != nil {
			writeError(w, http.StatusBadRequest, errors.New("结束时间格式不正确"))
			return
		}
		task, err := h.service.Create(r.Context(), app.BinlogAnalysisRequest{
			MachineID: strings.TrimSpace(body.MachineID), Port: body.Port,
			StartTime: start, EndTime: end, StartFile: strings.TrimSpace(body.StartFile),
			BigTxnMode: body.BigTxnMode, BigTxnRowsThreshold: body.BigTxnRowsThreshold,
			BigTxnBytesThreshold: body.BigTxnBytesThreshold,
		})
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusAccepted, task)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (h *BinlogAnalysisHandler) HandleTask(w http.ResponseWriter, r *http.Request) {
	if h.service == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("binlog analysis service is unavailable"))
		return
	}
	rawID := strings.TrimPrefix(r.URL.Path, "/api/v1/mysql/binlog-analysis/")
	id, err := url.PathUnescape(strings.Trim(rawID, "/"))
	if err != nil || strings.TrimSpace(id) == "" || strings.Contains(id, "/") {
		writeError(w, http.StatusBadRequest, errors.New("Binlog 分析任务 ID 不正确"))
		return
	}
	switch r.Method {
	case http.MethodGet:
		task, ok := h.service.Get(id)
		if !ok {
			writeError(w, http.StatusNotFound, errors.New("Binlog 分析任务不存在"))
			return
		}
		writeJSON(w, http.StatusOK, task)
	case http.MethodDelete:
		task, err := h.service.Cancel(id)
		if err != nil {
			writeError(w, http.StatusConflict, err)
			return
		}
		writeJSON(w, http.StatusOK, task)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func parseBinlogAnalysisTime(raw string) (time.Time, error) {
	text := strings.TrimSpace(raw)
	for _, layout := range []string{
		time.RFC3339Nano,
		"2006-01-02T15:04:05",
		"2006-01-02T15:04",
		"2006-01-02 15:04:05",
		"2006-01-02 15:04",
	} {
		var (
			value time.Time
			err   error
		)
		if layout == time.RFC3339Nano {
			value, err = time.Parse(layout, text)
		} else {
			value, err = time.ParseInLocation(layout, text, time.Local)
		}
		if err == nil {
			return value, nil
		}
	}
	return time.Time{}, errors.New("invalid time")
}
