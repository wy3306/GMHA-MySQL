package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"gmha/internal/app"
	alertdomain "gmha/internal/domain/alert"
	dynamicdomain "gmha/internal/domain/dynamic"
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
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	items, err := h.alerts.ListEvents(r.Context(), alertdomain.EventFilter{Status: r.URL.Query().Get("status"), Severity: r.URL.Query().Get("severity"), ClusterID: r.URL.Query().Get("cluster_id"), Keyword: r.URL.Query().Get("keyword"), Limit: limit, Offset: offset})
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
			writeAlert(w, nil, alertdomain.Invalid("静默时长仅支持 1、2、3、5、12 或 24 小时"))
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
		ID            string `json:"id"`
		State         string `json:"state"`
		ExpectedState string `json:"expected_state"`
	}
	if !decodeAlert(w, r, &x) {
		return
	}
	writeAlert(w, map[string]bool{"updated": true}, h.alerts.UpdateAutomationState(r.Context(), x.ID, x.State, x.ExpectedState))
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
					for key, value := range old.Config {
						if x.Config[key] == "******" {
							x.Config[key] = value
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
	catalog := dynamicdomain.BuildPerformanceMetricCatalog()
	catalog = append(catalog,
		dynamicdomain.PerformanceMetricDefinition{Name: "agent_heartbeat_alive", DisplayName: "Agent 心跳存活", Scope: "agent", Category: "health", Unit: "0/1", ValueKind: "state", Aggregation: "min", IntervalSeconds: 5, Available: true, Description: "1 表示心跳正常，0 表示心跳中断"},
		dynamicdomain.PerformanceMetricDefinition{Name: "agent_overall_health", DisplayName: "Agent 整体健康", Scope: "agent", Category: "health", Unit: "0/1", ValueKind: "state", Aggregation: "max", IntervalSeconds: 5, Available: true, Description: "0 表示健康，1 表示存在异常健康检查"},
		dynamicdomain.PerformanceMetricDefinition{Name: "agent_health_check_failed", DisplayName: "Agent 健康检查失败", Scope: "agent", Category: "health", Unit: "0/1", ValueKind: "state", Aggregation: "max", IntervalSeconds: 5, Available: true, Description: "按 check_name 标签区分具体健康检查"},
	)
	writeAlert(w, map[string]any{
		"host": h.heartbeat.GetDynamicCollectConfig(), "mysql": h.heartbeat.GetMySQLDynamicCollectConfig(),
		"catalog": catalog, "runtime": h.alerts.RuntimeStatus(),
	}, nil)
}
func (h *AlertHandler) summary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	summary, err := h.alerts.Summary(r.Context())
	if err != nil {
		writeAlert(w, nil, err)
		return
	}
	writeAlert(w, map[string]any{
		"counts": summary.Counts, "total": summary.Total,
		"active_acknowledged": summary.ActiveAcknowledged,
		"active_silenced":     summary.ActiveSilenced,
		"last_24_hours":       summary.Last24Hours,
		"runtime":             h.alerts.RuntimeStatus(),
	}, nil)
}
func (h *AlertHandler) prometheus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
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
	runtime := h.alerts.RuntimeStatus()
	fmt.Fprintln(w, "# HELP gmha_alert_evaluation_queue_depth Pending heartbeat evaluations\n# TYPE gmha_alert_evaluation_queue_depth gauge")
	fmt.Fprintf(w, "gmha_alert_evaluation_queue_depth %d\n", runtime.EvaluationQueue.Depth)
	fmt.Fprintln(w, "# HELP gmha_alert_evaluation_overflow Coalesced latest heartbeat evaluations waiting outside the queue\n# TYPE gmha_alert_evaluation_overflow gauge")
	fmt.Fprintf(w, "gmha_alert_evaluation_overflow %d\n", runtime.EvaluationQueue.Overflow)
	fmt.Fprintln(w, "# HELP gmha_alert_notification_queue_depth Pending alert notification events\n# TYPE gmha_alert_notification_queue_depth gauge")
	fmt.Fprintf(w, "gmha_alert_notification_queue_depth %d\n", runtime.NotificationQueue.Depth)
	fmt.Fprintln(w, "# HELP gmha_alert_notification_outbox_pending Durable notification jobs awaiting completion\n# TYPE gmha_alert_notification_outbox_pending gauge")
	fmt.Fprintf(w, "gmha_alert_notification_outbox_pending %d\n", runtime.NotificationQueue.DurablePending)
	fmt.Fprintln(w, "# HELP gmha_alert_notifications_dropped_total Notification events rejected because the queue was full\n# TYPE gmha_alert_notifications_dropped_total counter")
	fmt.Fprintf(w, "gmha_alert_notifications_dropped_total %d\n", runtime.NotificationsDropped)
	fmt.Fprintln(w, "# HELP gmha_alert_notifications_deferred_total Notification events durably deferred for later delivery\n# TYPE gmha_alert_notifications_deferred_total counter")
	fmt.Fprintf(w, "gmha_alert_notifications_deferred_total %d\n", runtime.NotificationsDeferred)
	fmt.Fprintln(w, "# HELP gmha_alert_deliveries_total Third-party delivery attempts by result\n# TYPE gmha_alert_deliveries_total counter")
	fmt.Fprintf(w, "gmha_alert_deliveries_total{result=\"success\"} %d\n", runtime.DeliveriesSucceeded)
	fmt.Fprintf(w, "gmha_alert_deliveries_total{result=\"failed\"} %d\n", runtime.DeliveriesFailed)
}
func (h *AlertHandler) zabbix(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
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
		status := http.StatusInternalServerError
		var validation alertdomain.ValidationError
		if errors.As(err, &validation) {
			status = http.StatusBadRequest
		}
		if errors.Is(err, alertdomain.ErrNotFound) {
			status = http.StatusNotFound
		}
		if errors.Is(err, alertdomain.ErrConflict) {
			status = http.StatusConflict
		}
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	_ = json.NewEncoder(w).Encode(v)
}
func maskAlertSecrets(cfg map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range cfg {
		if isAlertSecretKey(k) && v != "" {
			out[k] = "******"
		} else {
			out[k] = v
		}
	}
	return out
}
func isAlertSecretKey(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "password", "token", "secret", "webhook", "url":
		return true
	default:
		return false
	}
}
