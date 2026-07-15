package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"gmha/internal/app"
	dynamicdomain "gmha/internal/domain/dynamic"
)

// DynamicCollectHandler 是动态采集配置的 HTTP 请求处理器。
type DynamicCollectHandler struct {
	heartbeat *app.HeartbeatService
	alerts    *app.AlertService
}

// NewDynamicCollectHandler 创建一个新的 DynamicCollectHandler 实例。
func NewDynamicCollectHandler(heartbeat *app.HeartbeatService, alerts ...*app.AlertService) *DynamicCollectHandler {
	h := &DynamicCollectHandler{heartbeat: heartbeat}
	if len(alerts) > 0 {
		h.alerts = alerts[0]
	}
	return h
}

// HandleConfig 处理动态采集配置的查询和更新请求（GET/PUT/POST）。
func (h *DynamicCollectHandler) HandleConfig(w http.ResponseWriter, r *http.Request) {
	if h.heartbeat == nil {
		writeError(w, http.StatusServiceUnavailable, errServiceUnavailable("heartbeat service not configured"))
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, h.heartbeat.GetDynamicCollectConfig())
	case http.MethodPut, http.MethodPost:
		var cfg dynamicdomain.DynamicCollectConfig
		if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		var err error
		cfg, err = normalizeCollectConfig(cfg, 1)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if h.alerts != nil {
			if err := h.alerts.SaveMetricConfig(r.Context(), "host", cfg); err != nil {
				writeError(w, http.StatusInternalServerError, err)
				return
			}
		}
		writeJSON(w, http.StatusOK, h.heartbeat.UpdateDynamicCollectConfig(cfg))
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// HandleMySQLConfig 处理 MySQL 动态采集配置的查询和更新请求（GET/PUT/POST）。
func (h *DynamicCollectHandler) HandleMySQLConfig(w http.ResponseWriter, r *http.Request) {
	if h.heartbeat == nil {
		writeError(w, http.StatusServiceUnavailable, errServiceUnavailable("heartbeat service not configured"))
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, h.heartbeat.GetMySQLDynamicCollectConfig())
	case http.MethodPut, http.MethodPost:
		var cfg dynamicdomain.DynamicCollectConfig
		if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		var err error
		cfg, err = normalizeCollectConfig(cfg, 5)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if h.alerts != nil {
			if err := h.alerts.SaveMetricConfig(r.Context(), "mysql", cfg); err != nil {
				writeError(w, http.StatusInternalServerError, err)
				return
			}
		}
		writeJSON(w, http.StatusOK, h.heartbeat.UpdateMySQLDynamicCollectConfig(cfg))
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func normalizeCollectConfig(cfg dynamicdomain.DynamicCollectConfig, baseMinimum int) (dynamicdomain.DynamicCollectConfig, error) {
	if len(cfg.Tasks) > 256 {
		return cfg, errors.New("a maximum of 256 collectors is allowed")
	}
	for i := range cfg.Tasks {
		task := &cfg.Tasks[i]
		if strings.TrimSpace(task.Name) == "" {
			return cfg, errors.New("collector name is required")
		}
		minimum := baseMinimum
		if strings.HasPrefix(task.Name, "agent_") {
			minimum = 15
		}
		if task.Params["query"] != "" {
			minimum = 5
		}
		if task.IntervalSeconds < minimum {
			task.IntervalSeconds = minimum
		}
		if task.TimeoutSeconds < 1 {
			task.TimeoutSeconds = 1
		}
		if task.TimeoutSeconds > 10 {
			task.TimeoutSeconds = 10
		}
		if task.TimeoutSeconds > task.IntervalSeconds {
			task.TimeoutSeconds = task.IntervalSeconds
		}
	}
	cfg.UpdatedAt = time.Now().UTC()
	cfg.Version = cfg.UpdatedAt.Format("20060102T150405.000000000Z")
	return cfg, nil
}

// errServiceUnavailable 表示服务不可用的错误类型。
type errServiceUnavailable string

// Error 返回错误信息字符串。
func (e errServiceUnavailable) Error() string { return string(e) }
