package handler

import (
	"encoding/json"
	"net/http"

	"gmha/internal/app"
	dynamicdomain "gmha/internal/domain/dynamic"
)

// DynamicCollectHandler 是动态采集配置的 HTTP 请求处理器。
type DynamicCollectHandler struct {
	heartbeat *app.HeartbeatService
}

// NewDynamicCollectHandler 创建一个新的 DynamicCollectHandler 实例。
func NewDynamicCollectHandler(heartbeat *app.HeartbeatService) *DynamicCollectHandler {
	return &DynamicCollectHandler{heartbeat: heartbeat}
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
		writeJSON(w, http.StatusOK, h.heartbeat.UpdateMySQLDynamicCollectConfig(cfg))
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// errServiceUnavailable 表示服务不可用的错误类型。
type errServiceUnavailable string

// Error 返回错误信息字符串。
func (e errServiceUnavailable) Error() string { return string(e) }
