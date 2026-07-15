package handler

import (
	"encoding/json"
	"net/http"
	"strings"

	"gmha/internal/app"
	hadomain "gmha/internal/domain/ha"
)

// HAHandler 是高可用相关 HTTP API 的请求处理器，负责 VIP 管理和故障切换操作。
type HAHandler struct {
	ha *app.HAService
}

// NewHAHandler 创建一个新的 HAHandler 实例。
func NewHAHandler(ha *app.HAService) *HAHandler {
	return &HAHandler{ha: ha}
}

// HandleClusterActions 处理集群级别的 HA 操作请求，包括 VIP 状态/扫描/采纳/验证和故障切换计划/启动/状态查询。
func (h *HAHandler) HandleClusterActions(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/clusters/"), "/"), "/")
	if len(parts) < 2 {
		http.NotFound(w, r)
		return
	}
	clusterID := parts[0]
	switch {
	case len(parts) == 3 && parts[1] == "vip" && parts[2] == "config" && r.Method == http.MethodGet:
		items, err := h.ha.ListVIPConfigs(r.Context(), clusterID)
		writeHAJSON(w, items, err)
	case len(parts) == 3 && parts[1] == "vip" && parts[2] == "config" && r.Method == http.MethodPost:
		var req hadomain.ClusterVIPConfig
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeHAError(w, http.StatusBadRequest, err.Error())
			return
		}
		item, err := h.ha.SaveVIPConfig(r.Context(), clusterID, req)
		writeHAJSON(w, item, err)
	case len(parts) == 3 && parts[1] == "vip" && parts[2] == "config" && r.Method == http.MethodDelete:
		err := h.ha.DeleteVIPConfig(r.Context(), clusterID, r.URL.Query().Get("vip"))
		writeHAJSON(w, map[string]bool{"deleted": err == nil}, err)
	case len(parts) == 3 && parts[1] == "vip" && parts[2] == "status" && r.Method == http.MethodGet:
		items, err := h.ha.VIP().Status(r.Context(), clusterID)
		writeHAJSON(w, items, err)
	case len(parts) == 3 && parts[1] == "vip" && parts[2] == "scan" && r.Method == http.MethodPost:
		items, err := h.ha.VIP().Scan(r.Context(), clusterID)
		writeHAJSON(w, items, err)
	case len(parts) == 3 && parts[1] == "vip" && parts[2] == "adopt" && r.Method == http.MethodPost:
		var req struct {
			VIP string `json:"vip"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		item, err := h.ha.VIP().Adopt(r.Context(), clusterID, strings.TrimSpace(req.VIP))
		writeHAJSON(w, item, err)
	case len(parts) == 3 && parts[1] == "vip" && parts[2] == "validate" && r.Method == http.MethodPost:
		items, err := h.ha.VIP().Validate(r.Context(), clusterID)
		writeHAJSON(w, items, err)
	case len(parts) == 3 && parts[1] == "failover" && parts[2] == "plan" && r.Method == http.MethodPost:
		item, err := h.ha.PlanFailover(r.Context(), clusterID)
		writeHAJSON(w, item, err)
	case len(parts) == 3 && parts[1] == "architecture" && parts[2] == "plan" && r.Method == http.MethodPost:
		var req hadomain.ArchitectureAdjustmentRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeHAError(w, http.StatusBadRequest, err.Error())
			return
		}
		item, err := h.ha.PlanArchitectureAdjustment(r.Context(), clusterID, req)
		writeHAJSON(w, item, err)
	case len(parts) == 3 && parts[1] == "architecture" && parts[2] == "start" && r.Method == http.MethodPost:
		var req hadomain.ArchitectureAdjustmentRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeHAError(w, http.StatusBadRequest, err.Error())
			return
		}
		item, err := h.ha.StartArchitectureAdjustment(r.Context(), clusterID, req)
		writeHAJSON(w, item, err)
	case len(parts) == 3 && parts[1] == "architecture" && r.Method == http.MethodGet:
		item, ok, err := h.ha.GetArchitectureRun(r.Context(), clusterID, parts[2])
		if err != nil {
			writeHAError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if !ok {
			writeHAError(w, http.StatusNotFound, "architecture run not found")
			return
		}
		writeHAJSON(w, item, nil)
	case len(parts) == 4 && parts[1] == "architecture" && parts[3] == "force" && r.Method == http.MethodPost:
		item, err := h.ha.ConfirmArchitectureForce(r.Context(), clusterID, parts[2])
		writeHAJSON(w, item, err)
	case len(parts) == 3 && parts[1] == "failover" && parts[2] == "start" && r.Method == http.MethodPost:
		item, err := h.ha.StartFailover(r.Context(), clusterID)
		writeHAJSON(w, item, err)
	case len(parts) == 3 && parts[1] == "failover" && r.Method == http.MethodGet:
		item, ok, err := h.ha.GetFailover(r.Context(), clusterID, parts[2])
		if err != nil {
			writeHAError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if !ok {
			writeHAError(w, http.StatusNotFound, "failover not found")
			return
		}
		writeHAJSON(w, item, nil)
	default:
		http.NotFound(w, r)
	}
}

// writeHAJSON 将 HA 操作结果以 JSON 格式写入响应，出错时返回错误信息。
func writeHAJSON(w http.ResponseWriter, v any, err error) {
	if err != nil {
		writeHAError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// writeHAError 以 JSON 格式写入 HA 操作的错误响应。
func writeHAError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
