package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"gmha/internal/app"
	alertdomain "gmha/internal/domain/alert"
)

type AlertHandler struct {
	alerts    *app.AlertService
	heartbeat *app.HeartbeatService
}

func NewAlertHandler(alerts *app.AlertService, heartbeat *app.HeartbeatService) *AlertHandler {
	return &AlertHandler{alerts: alerts, heartbeat: heartbeat}
}

func (h *AlertHandler) Handle(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/alerts")
	switch {
	case path == "/rules":
		h.rules(w, r)
	case path == "/filters":
		h.filters(w, r)
	case path == "/events":
		h.events(w, r)
	case path == "/events/action":
		h.eventAction(w, r)
	case path == "/events/automation":
		h.automationState(w, r)
	case path == "/channels":
		h.channels(w, r)
	case path == "/channels/test":
		h.testChannel(w, r)
	case path == "/deliveries":
		h.deliveries(w, r)
	case path == "/metrics":
		h.metrics(w, r)
	case path == "/summary":
		h.summary(w, r)
	case path == "/export/prometheus":
		h.prometheus(w, r)
	case path == "/export/zabbix":
		h.zabbix(w, r)
	default:
		http.NotFound(w, r)
	}
}
func (h *AlertHandler) deliveries(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, err := h.alerts.ListDeliveries(r.Context(), limit)
	writeAlert(w, items, err)
}
func (h *AlertHandler) filters(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		items, err := h.alerts.ListFilters(r.Context())
		writeAlert(w, items, err)
	case http.MethodPost, http.MethodPut:
		var x alertdomain.Filter
		if !decodeAlert(w, r, &x) {
			return
		}
		item, err := h.alerts.SaveFilter(r.Context(), x)
		writeAlert(w, item, err)
	case http.MethodDelete:
		writeAlert(w, map[string]bool{"deleted": true}, h.alerts.DeleteFilter(r.Context(), r.URL.Query().Get("id")))
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}
func (h *AlertHandler) rules(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		items, err := h.alerts.ListRules(r.Context())
		writeAlert(w, items, err)
	case http.MethodPost, http.MethodPut:
		var x alertdomain.Rule
		if !decodeAlert(w, r, &x) {
			return
		}
		item, err := h.alerts.SaveRule(r.Context(), x)
		writeAlert(w, item, err)
	case http.MethodDelete:
		writeAlert(w, map[string]bool{"deleted": true}, h.alerts.DeleteRule(r.Context(), r.URL.Query().Get("id")))
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}
func (h *AlertHandler) events(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, err := h.alerts.ListEvents(r.Context(), alertdomain.EventFilter{Status: r.URL.Query().Get("status"), Severity: r.URL.Query().Get("severity"), ClusterID: r.URL.Query().Get("cluster_id"), Keyword: r.URL.Query().Get("keyword"), Limit: limit})
	writeAlert(w, items, err)
}
func (h *AlertHandler) eventAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var x struct {
		ID, Action, Actor string
		SilenceSeconds    int `json:"silence_seconds"`
	}
	if !decodeAlert(w, r, &x) {
		return
	}
	if x.Action == "silence" {
		allowed := map[int]bool{3600: true, 7200: true, 10800: true, 18000: true, 43200: true, 86400: true}
		if !allowed[x.SilenceSeconds] {
			writeAlert(w, nil, fmt.Errorf("静默时长仅支持 1、2、3、5、12 或 24 小时"))
			return
		}
	}
	var until *time.Time
	if x.SilenceSeconds > 0 {
		v := time.Now().UTC().Add(time.Duration(x.SilenceSeconds) * time.Second)
		until = &v
	}
	writeAlert(w, map[string]bool{"updated": true}, h.alerts.EventAction(r.Context(), x.ID, x.Action, x.Actor, until))
}
func (h *AlertHandler) automationState(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodPut {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var x struct {
		ID    string `json:"id"`
		State string `json:"state"`
	}
	if !decodeAlert(w, r, &x) {
		return
	}
	writeAlert(w, map[string]bool{"updated": true}, h.alerts.UpdateAutomationState(r.Context(), x.ID, x.State))
}
func (h *AlertHandler) channels(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		items, err := h.alerts.ListChannels(r.Context())
		if err == nil {
			for i := range items {
				items[i].Config = maskAlertSecrets(items[i].Config)
			}
		}
		writeAlert(w, items, err)
	case http.MethodPost, http.MethodPut:
		var x alertdomain.Channel
		if !decodeAlert(w, r, &x) {
			return
		}
		if x.ID != "" {
			items, _ := h.alerts.ListChannels(r.Context())
			for _, old := range items {
				if old.ID == x.ID {
					for _, key := range []string{"password", "token", "secret"} {
						if x.Config[key] == "******" {
							x.Config[key] = old.Config[key]
						}
					}
				}
			}
		}
		item, err := h.alerts.SaveChannel(r.Context(), x)
		item.Config = maskAlertSecrets(item.Config)
		writeAlert(w, item, err)
	case http.MethodDelete:
		writeAlert(w, map[string]bool{"deleted": true}, h.alerts.DeleteChannel(r.Context(), r.URL.Query().Get("id")))
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}
func (h *AlertHandler) testChannel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var x alertdomain.Channel
	if !decodeAlert(w, r, &x) {
		return
	}
	if x.ID != "" {
		items, _ := h.alerts.ListChannels(r.Context())
		for _, old := range items {
			if old.ID == x.ID {
				for k, v := range old.Config {
					if x.Config[k] == "******" || x.Config[k] == "" {
						x.Config[k] = v
					}
				}
			}
		}
	}
	writeAlert(w, map[string]string{"status": "delivered"}, h.alerts.TestChannel(r.Context(), x))
}
func (h *AlertHandler) metrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	writeAlert(w, map[string]any{"host": h.heartbeat.GetDynamicCollectConfig(), "mysql": h.heartbeat.GetMySQLDynamicCollectConfig(), "virtual": []map[string]string{{"name": "agent_heartbeat_alive", "display_name": "Agent 心跳存活"}, {"name": "agent_overall_health", "display_name": "Agent 整体健康"}, {"name": "agent_health_check_failed", "display_name": "Agent 健康检查失败"}}}, nil)
}
func (h *AlertHandler) summary(w http.ResponseWriter, r *http.Request) {
	items, err := h.alerts.ListEvents(r.Context(), alertdomain.EventFilter{Limit: 1000})
	if err != nil {
		writeAlert(w, nil, err)
		return
	}
	counts := map[string]int{"firing": 0, "resolved": 0, "notice": 0, "warning": 0, "critical": 0, "fatal": 0}
	for _, x := range items {
		counts[x.Status]++
		if x.Status == "firing" {
			counts[string(x.Severity)]++
		}
	}
	writeAlert(w, map[string]any{"counts": counts, "total": len(items)}, nil)
}
func (h *AlertHandler) prometheus(w http.ResponseWriter, r *http.Request) {
	items, err := h.alerts.ListEvents(r.Context(), alertdomain.EventFilter{Status: "firing", Limit: 1000})
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	fmt.Fprintln(w, "# HELP gmha_alert_info Active GMHA alerts\n# TYPE gmha_alert_info gauge")
	for _, x := range items {
		fmt.Fprintf(w, "gmha_alert_info{event_id=%q,rule=%q,severity=%q,machine_id=%q,cluster_id=%q,metric=%q} 1\n", x.ID, x.RuleName, x.Severity, x.MachineID, x.ClusterID, x.Metric)
	}
	fmt.Fprintln(w, "# HELP gmha_alert_value Current metric value for active GMHA alerts\n# TYPE gmha_alert_value gauge")
	for _, x := range items {
		fmt.Fprintf(w, "gmha_alert_value{event_id=%q,metric=%q,machine_id=%q} %v\n", x.ID, x.Metric, x.MachineID, x.Value)
	}
}
func (h *AlertHandler) zabbix(w http.ResponseWriter, r *http.Request) {
	items, err := h.alerts.ListEvents(r.Context(), alertdomain.EventFilter{Status: "firing", Limit: 1000})
	if err != nil {
		writeAlert(w, nil, err)
		return
	}
	out := make([]map[string]any, 0, len(items))
	for _, x := range items {
		out = append(out, map[string]any{"host": x.MachineID, "key": "gmha.alert." + x.Metric, "value": x.Value, "severity": x.Severity, "clock": x.LastSeenAt.Unix()})
	}
	writeAlert(w, out, nil)
}
func decodeAlert(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(v); err != nil {
		http.Error(w, `{"error":"invalid json"}`, 400)
		return false
	}
	return true
}
func writeAlert(w http.ResponseWriter, v any, err error) {
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		w.WriteHeader(400)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	_ = json.NewEncoder(w).Encode(v)
}
func maskAlertSecrets(cfg map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range cfg {
		if (k == "password" || k == "token" || k == "secret") && v != "" {
			out[k] = "******"
		} else {
			out[k] = v
		}
	}
	return out
}
