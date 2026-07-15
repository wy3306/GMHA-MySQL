package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/smtp"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	alertdomain "gmha/internal/domain/alert"
	dynamicdomain "gmha/internal/domain/dynamic"
	hbdomain "gmha/internal/domain/heartbeat"
)

// AlertService evaluates metrics on the Manager. Notifications are queued so
// a slow third-party endpoint can never delay Agent heartbeat processing.
type AlertService struct {
	repo        alertdomain.Repository
	queue       chan alertdomain.Event
	evaluations chan hbdomain.HeartbeatPayload
	http        *http.Client
}

func NewAlertService(repo alertdomain.Repository) *AlertService {
	s := &AlertService{repo: repo, queue: make(chan alertdomain.Event, 256), evaluations: make(chan hbdomain.HeartbeatPayload, 128), http: &http.Client{Timeout: 8 * time.Second}}
	go s.deliveryLoop()
	go s.evaluationLoop()
	return s
}

func (s *AlertService) EnsureDefaults(ctx context.Context) error {
	rules, err := s.repo.ListRules(ctx)
	if err != nil {
		return err
	}
	existing := map[string]bool{}
	for _, item := range rules {
		existing[item.ID] = true
	}
	defaults := []alertdomain.Rule{
		{Name: "主机 CPU 使用率过高", Metric: "cpu_usage_percent", Operator: ">=", Threshold: 85, Severity: alertdomain.SeverityWarning, ConsecutiveCount: 3},
		{Name: "主机内存使用率严重", Metric: "mem_usage_percent", Operator: ">=", Threshold: 90, Severity: alertdomain.SeverityCritical, ConsecutiveCount: 3},
		{Name: "数据盘空间不足", Metric: "mysql_data_disk_usage", Operator: ">=", Threshold: 85, Severity: alertdomain.SeverityCritical, ConsecutiveCount: 2},
		{Name: "Binlog 盘空间不足", Metric: "mysql_binlog_disk_usage", Operator: ">=", Threshold: 85, Severity: alertdomain.SeverityCritical, ConsecutiveCount: 2},
		{Name: "MySQL 无法连接", Metric: "mysql_connectivity", Operator: "==", Threshold: 0, Severity: alertdomain.SeverityFatal, ConsecutiveCount: 2},
		{Name: "MySQL 进程停止", Metric: "mysql_process_alive", Operator: "==", Threshold: 0, Severity: alertdomain.SeverityFatal, ConsecutiveCount: 2},
		{Name: "复制延迟过高", Metric: "mysql_replication_lag", Operator: ">=", Threshold: 30, Severity: alertdomain.SeverityCritical, ConsecutiveCount: 3},
		{Name: "复制 IO 线程异常", Metric: "mysql_replica_io_thread", Operator: "==", Threshold: 0, Severity: alertdomain.SeverityCritical, ConsecutiveCount: 2},
		{Name: "复制 SQL 线程异常", Metric: "mysql_replica_sql_thread", Operator: "==", Threshold: 0, Severity: alertdomain.SeverityCritical, ConsecutiveCount: 2},
		{Name: "连接使用率偏高", Metric: "mysql_connection_usage_percent", Operator: ">=", Threshold: 80, Severity: alertdomain.SeverityWarning, ConsecutiveCount: 3},
		{Name: "连接数需要关注", Metric: "mysql_threads_connected", Operator: ">=", Threshold: 100, Severity: alertdomain.SeverityNotice, ConsecutiveCount: 3},
		{Name: "长事务持续", Metric: "mysql_longest_transaction_seconds", Operator: ">=", Threshold: 300, Severity: alertdomain.SeverityWarning, ConsecutiveCount: 3},
		{Name: "Agent 内存占用过高", Metric: "agent_memory_rss_mb", Operator: ">=", Threshold: 256, Severity: alertdomain.SeverityWarning, ConsecutiveCount: 5},
		{Name: "Agent CPU 占用过高", Metric: "agent_cpu_usage_percent", Operator: ">=", Threshold: 10, Severity: alertdomain.SeverityWarning, ConsecutiveCount: 5},
		{Name: "Agent 心跳中断", Metric: "agent_heartbeat_alive", Operator: "==", Threshold: 0, Severity: alertdomain.SeverityFatal, ConsecutiveCount: 1},
		{Name: "Agent 健康状态异常", Metric: "agent_overall_health", Operator: ">=", Threshold: 1, Severity: alertdomain.SeverityWarning, ConsecutiveCount: 2},
	}
	now := time.Now().UTC()
	for i := range defaults {
		defaults[i].ID = stableID("default", defaults[i].Metric)
		if existing[defaults[i].ID] {
			continue
		}
		defaults[i].Description = "系统默认规则，可按业务负载调整阈值"
		defaults[i].Enabled = true
		defaults[i].Scope = "all"
		defaults[i].RepeatIntervalSeconds = 300
		defaults[i].MaxNotifications = 10
		defaults[i].CreatedAt = now
		defaults[i].UpdatedAt = now
		if err := s.repo.SaveRule(ctx, defaults[i]); err != nil {
			return err
		}
	}
	return nil
}

func (s *AlertService) ListRules(ctx context.Context) ([]alertdomain.Rule, error) {
	return s.repo.ListRules(ctx)
}
func (s *AlertService) SaveRule(ctx context.Context, x alertdomain.Rule) (alertdomain.Rule, error) {
	if strings.TrimSpace(x.Name) == "" || strings.TrimSpace(x.Metric) == "" {
		return x, errors.New("name and metric are required")
	}
	if !validOperator(x.Operator) {
		return x, errors.New("operator must be one of >, >=, <, <=, ==, !=")
	}
	if len(x.Thresholds) == 0 {
		x.Thresholds = []alertdomain.ThresholdLevel{{Severity: x.Severity, Threshold: x.Threshold, Enabled: true}}
	}
	seenSeverities := map[alertdomain.Severity]bool{}
	normalized := make([]alertdomain.ThresholdLevel, 0, len(x.Thresholds))
	for _, level := range x.Thresholds {
		if !level.Enabled {
			continue
		}
		if alertdomain.SeverityRank(level.Severity) == 0 || seenSeverities[level.Severity] {
			return x, errors.New("threshold severity must be valid and unique")
		}
		seenSeverities[level.Severity] = true
		normalized = append(normalized, level)
	}
	if len(normalized) == 0 {
		return x, errors.New("at least one severity threshold must be enabled")
	}
	sort.Slice(normalized, func(i, j int) bool {
		return alertdomain.SeverityRank(normalized[i].Severity) < alertdomain.SeverityRank(normalized[j].Severity)
	})
	x.Thresholds = normalized
	x.Severity, x.Threshold = normalized[0].Severity, normalized[0].Threshold
	if x.ConsecutiveCount < 1 {
		x.ConsecutiveCount = 1
	}
	if x.RepeatIntervalSeconds < 30 {
		x.RepeatIntervalSeconds = 30
	}
	if x.MaxNotifications < 0 {
		x.MaxNotifications = 0
	}
	now := time.Now().UTC()
	if x.ID == "" {
		x.ID = stableID(x.Name, fmt.Sprint(now.UnixNano()))
		x.CreatedAt = now
	}
	if x.CreatedAt.IsZero() {
		x.CreatedAt = now
	}
	x.UpdatedAt = now
	return x, s.repo.SaveRule(ctx, x)
}
func (s *AlertService) DeleteRule(ctx context.Context, id string) error {
	return s.repo.DeleteRule(ctx, id)
}
func (s *AlertService) ListFilters(ctx context.Context) ([]alertdomain.Filter, error) {
	return s.repo.ListFilters(ctx)
}
func (s *AlertService) SaveFilter(ctx context.Context, x alertdomain.Filter) (alertdomain.Filter, error) {
	if strings.TrimSpace(x.Name) == "" {
		return x, errors.New("filter name is required")
	}
	if strings.TrimSpace(x.ClusterPattern+x.MachinePattern+x.IPCIDR+x.CategoryPattern+x.MessagePattern) == "" {
		return x, errors.New("at least one filter condition is required")
	}
	if x.IPCIDR != "" {
		if _, _, err := net.ParseCIDR(strings.TrimSpace(x.IPCIDR)); err != nil {
			return x, errors.New("invalid IP CIDR")
		}
	}
	if x.UseRegex {
		for _, pattern := range []string{x.ClusterPattern, x.MachinePattern, x.CategoryPattern, x.MessagePattern} {
			if pattern != "" {
				if _, err := regexp.Compile(pattern); err != nil {
					return x, fmt.Errorf("invalid regular expression: %w", err)
				}
			}
		}
	}
	now := time.Now().UTC()
	if x.ID == "" {
		x.ID = stableID(x.Name, fmt.Sprint(now.UnixNano()))
		x.CreatedAt = now
	}
	if x.CreatedAt.IsZero() {
		x.CreatedAt = now
	}
	x.UpdatedAt = now
	return x, s.repo.SaveFilter(ctx, x)
}
func (s *AlertService) DeleteFilter(ctx context.Context, id string) error {
	return s.repo.DeleteFilter(ctx, id)
}
func (s *AlertService) ListEvents(ctx context.Context, f alertdomain.EventFilter) ([]alertdomain.Event, error) {
	items, err := s.repo.ListEvents(ctx, f)
	if err != nil {
		return nil, err
	}
	filters, err := s.repo.ListFilters(ctx)
	if err != nil || len(filters) == 0 {
		return items, err
	}
	out := make([]alertdomain.Event, 0, len(items))
	for _, event := range items {
		if !storedEventFiltered(filters, event) {
			out = append(out, event)
		}
	}
	return out, nil
}
func (s *AlertService) EventAction(ctx context.Context, id, action, actor string, until *time.Time) error {
	return s.repo.UpdateEventAction(ctx, id, action, actor, until)
}
func (s *AlertService) UpdateAutomationState(ctx context.Context, id, state string) error {
	switch state {
	case "pending", "claimed", "running", "succeeded", "failed", "skipped":
	default:
		return errors.New("invalid automation state")
	}
	return s.repo.UpdateAutomationState(ctx, id, state)
}
func (s *AlertService) ListChannels(ctx context.Context) ([]alertdomain.Channel, error) {
	return s.repo.ListChannels(ctx)
}
func (s *AlertService) SaveChannel(ctx context.Context, x alertdomain.Channel) (alertdomain.Channel, error) {
	if x.Name == "" || x.Type == "" {
		return x, errors.New("name and type are required")
	}
	switch x.Type {
	case "email", "dingtalk", "feishu", "webhook", "zabbix":
	default:
		return x, errors.New("unsupported channel type")
	}
	if err := validateChannelConfig(x); err != nil {
		return x, err
	}
	now := time.Now().UTC()
	if x.ID == "" {
		x.ID = stableID(x.Name, fmt.Sprint(now.UnixNano()))
		x.CreatedAt = now
	}
	if x.MinimumSeverity == "" {
		x.MinimumSeverity = alertdomain.SeverityWarning
	}
	x.UpdatedAt = now
	return x, s.repo.SaveChannel(ctx, x)
}
func (s *AlertService) DeleteChannel(ctx context.Context, id string) error {
	return s.repo.DeleteChannel(ctx, id)
}
func (s *AlertService) ListDeliveries(ctx context.Context, limit int) ([]alertdomain.Delivery, error) {
	return s.repo.ListDeliveries(ctx, limit)
}
func (s *AlertService) SaveMetricConfig(ctx context.Context, kind string, cfg dynamicdomain.DynamicCollectConfig) error {
	return s.repo.SaveMetricConfig(ctx, kind, cfg)
}

func (s *AlertService) ObserveHeartbeat(ctx context.Context, payload hbdomain.HeartbeatPayload) {
	select {
	case s.evaluations <- payload:
	default:
	}
}
func (s *AlertService) evaluationLoop() {
	for payload := range s.evaluations {
		s.evaluatePayload(context.Background(), payload)
	}
}
func (s *AlertService) evaluatePayload(ctx context.Context, payload hbdomain.HeartbeatPayload) {
	rules, err := s.repo.ListRules(ctx)
	if err != nil {
		return
	}
	filters, _ := s.repo.ListFilters(ctx)
	byMetric := map[string][]alertdomain.Rule{}
	for _, r := range rules {
		if r.Enabled {
			byMetric[r.Metric] = append(byMetric[r.Metric], r)
		}
	}
	for _, metric := range payload.Metrics {
		if !metric.Success {
			continue
		}
		numeric, ok := metricNumber(metric.Value)
		if !ok {
			continue
		}
		for _, rule := range byMetric[metric.Name] {
			if rule.ClusterID != "" && rule.ClusterID != payload.ClusterID {
				continue
			}
			if rule.Scope != "" && rule.Scope != "all" && rule.Scope != metric.Labels["metric_scope"] {
				continue
			}
			if !labelsMatch(rule.Labels, metric.Labels) {
				continue
			}
			if alertFiltered(filters, rule, payload, metric) {
				continue
			}
			s.evaluate(ctx, rule, payload, metric, numeric)
		}
	}
}

func (s *AlertService) evaluate(ctx context.Context, rule alertdomain.Rule, payload hbdomain.HeartbeatPayload, metric dynamicdomain.MetricResult, v float64) {
	fp := fingerprint(rule.ID, payload.MachineID, metric.Labels)
	state, _, err := s.repo.GetEvaluationState(ctx, fp)
	if err != nil {
		return
	}
	now := time.Now().UTC()
	level, firing := matchingThreshold(rule, v)
	if firing {
		state.Consecutive++
	} else {
		state.Consecutive = 0
	}
	state.Fingerprint = fp
	state.RuleID = rule.ID
	state.LastValue = v
	state.UpdatedAt = now
	_ = s.repo.SaveEvaluationState(ctx, state)
	active, ok, err := s.repo.GetActiveEvent(ctx, fp)
	if err != nil {
		return
	}
	if !firing {
		if ok {
			active.Status = "resolved"
			active.LastSeenAt = now
			active.Value = v
			active.ResolvedAt = &now
			_ = s.repo.SaveEvent(ctx, active)
			s.enqueue(active)
		}
		return
	}
	if state.Consecutive < rule.ConsecutiveCount {
		return
	}
	if !ok {
		labels := cloneLabels(metric.Labels)
		labels["machine_name"], labels["machine_ip"], labels["alert_category"] = alertMachineName(payload), payload.MachineIP, alertCategory(metric)
		active = alertdomain.Event{ID: stableID(fp, fmt.Sprint(now.UnixNano())), Fingerprint: fp, RuleID: rule.ID, RuleName: rule.Name, Metric: rule.Metric, MachineID: payload.MachineID, AgentID: payload.AgentID, ClusterID: payload.ClusterID, Labels: labels, Severity: level.Severity, Status: "firing", Value: v, Threshold: level.Threshold, Operator: rule.Operator, OccurrenceCount: 1, FirstSeenAt: now, LastSeenAt: now, AutomationState: "pending"}
	} else {
		active.Value = v
		active.ClusterID = payload.ClusterID
		active.Labels = cloneLabels(metric.Labels)
		active.Labels["machine_name"], active.Labels["machine_ip"], active.Labels["alert_category"] = alertMachineName(payload), payload.MachineIP, alertCategory(metric)
		active.Severity = level.Severity
		active.Threshold = level.Threshold
		active.Operator = rule.Operator
		active.LastSeenAt = now
		active.OccurrenceCount++
	}
	notify := active.SilencedUntil == nil || active.SilencedUntil.Before(now)
	notify = notify && (rule.MaxNotifications == 0 || active.NotificationCount < rule.MaxNotifications)
	notify = notify && (active.LastNotifiedAt == nil || now.Sub(*active.LastNotifiedAt) >= time.Duration(rule.RepeatIntervalSeconds)*time.Second)
	if notify {
		notify = s.enqueue(active)
		if notify {
			active.NotificationCount++
			active.LastNotifiedAt = &now
		}
	}
	_ = s.repo.SaveEvent(ctx, active)
}

func matchingThreshold(rule alertdomain.Rule, value float64) (alertdomain.ThresholdLevel, bool) {
	levels := rule.Thresholds
	if len(levels) == 0 {
		levels = []alertdomain.ThresholdLevel{{Severity: rule.Severity, Threshold: rule.Threshold, Enabled: true}}
	}
	var selected alertdomain.ThresholdLevel
	matched := false
	for _, level := range levels {
		if level.Enabled && compare(value, rule.Operator, level.Threshold) && (!matched || alertdomain.SeverityRank(level.Severity) > alertdomain.SeverityRank(selected.Severity)) {
			selected, matched = level, true
		}
	}
	return selected, matched
}

func alertFiltered(filters []alertdomain.Filter, rule alertdomain.Rule, payload hbdomain.HeartbeatPayload, metric dynamicdomain.MetricResult) bool {
	for _, filter := range filters {
		if !filter.Enabled {
			continue
		}
		if !matchAlertText(filter.ClusterPattern, payload.ClusterID, filter.UseRegex) ||
			!matchAlertText(filter.MachinePattern, alertMachineName(payload)+" "+payload.MachineID, filter.UseRegex) ||
			!matchAlertText(filter.CategoryPattern, alertCategory(metric), filter.UseRegex) ||
			!matchAlertText(filter.MessagePattern, alertMessage(rule, payload, metric), filter.UseRegex) ||
			!matchAlertCIDR(filter.IPCIDR, payload.MachineIP) {
			continue
		}
		return true
	}
	return false
}

func storedEventFiltered(filters []alertdomain.Filter, event alertdomain.Event) bool {
	for _, filter := range filters {
		if !filter.Enabled {
			continue
		}
		machine := strings.TrimSpace(event.Labels["machine_name"] + " " + event.MachineID)
		category := event.Labels["alert_category"]
		if category == "" {
			category = event.Labels["metric_scope"]
		}
		message := strings.Join([]string{event.RuleName, event.Metric, event.Labels["display_name"]}, " ")
		if matchAlertText(filter.ClusterPattern, event.ClusterID, filter.UseRegex) &&
			matchAlertText(filter.MachinePattern, machine, filter.UseRegex) &&
			matchAlertText(filter.CategoryPattern, category, filter.UseRegex) &&
			matchAlertText(filter.MessagePattern, message, filter.UseRegex) &&
			matchAlertCIDR(filter.IPCIDR, event.Labels["machine_ip"]) {
			return true
		}
	}
	return false
}

func matchAlertText(pattern, value string, useRegex bool) bool {
	if strings.TrimSpace(pattern) == "" {
		return true
	}
	if useRegex {
		re, err := regexp.Compile(pattern)
		return err == nil && re.MatchString(value)
	}
	return strings.Contains(strings.ToLower(value), strings.ToLower(strings.TrimSpace(pattern)))
}
func matchAlertCIDR(cidr, ip string) bool {
	if strings.TrimSpace(cidr) == "" {
		return true
	}
	parsed := net.ParseIP(strings.TrimSpace(ip))
	_, network, err := net.ParseCIDR(strings.TrimSpace(cidr))
	return err == nil && parsed != nil && network.Contains(parsed)
}
func alertMachineName(payload hbdomain.HeartbeatPayload) string {
	if payload.MachineName != "" {
		return payload.MachineName
	}
	return payload.Hostname
}
func alertCategory(metric dynamicdomain.MetricResult) string {
	if metric.Category != "" {
		return metric.Category
	}
	return metric.Labels["metric_scope"]
}
func alertMessage(rule alertdomain.Rule, payload hbdomain.HeartbeatPayload, metric dynamicdomain.MetricResult) string {
	parts := []string{rule.Name, rule.Description, rule.Metric, metric.Name, alertCategory(metric), payload.Summary}
	for key, value := range metric.Labels {
		parts = append(parts, key, value)
	}
	return strings.Join(parts, " ")
}
func cloneLabels(source map[string]string) map[string]string {
	out := make(map[string]string, len(source)+3)
	for key, value := range source {
		out[key] = value
	}
	return out
}

func (s *AlertService) enqueue(event alertdomain.Event) bool {
	select {
	case s.queue <- event:
		return true
	default:
		return false
	}
}
func (s *AlertService) deliveryLoop() {
	for event := range s.queue {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		channels, err := s.repo.ListChannels(ctx)
		if err == nil {
			for _, channel := range channels {
				if channel.Enabled && alertdomain.SeverityRank(event.Severity) >= alertdomain.SeverityRank(channel.MinimumSeverity) {
					var deliveryErr error
					for attempt := 0; attempt < 3; attempt++ {
						deliveryErr = s.deliver(ctx, channel, event)
						if deliveryErr == nil {
							break
						}
						select {
						case <-ctx.Done():
							break
						case <-time.After(time.Duration(attempt+1) * 200 * time.Millisecond):
						}
					}
					now := time.Now().UTC()
					channel.UpdatedAt = now
					if deliveryErr != nil {
						channel.LastStatus = "failed"
						channel.LastError = deliveryErr.Error()
					} else {
						channel.LastStatus = "success"
						channel.LastError = ""
						channel.LastDeliveredAt = &now
					}
					_ = s.repo.SaveChannel(ctx, channel)
					delivery := alertdomain.Delivery{ID: stableID(event.ID, channel.ID, fmt.Sprint(now.UnixNano())), EventID: event.ID, RuleName: event.RuleName, Severity: event.Severity, MachineID: event.MachineID, ChannelID: channel.ID, ChannelName: channel.Name, ChannelType: channel.Type, Status: channel.LastStatus, DeliveredAt: now}
					if deliveryErr != nil {
						delivery.Error = deliveryErr.Error()
					}
					_ = s.repo.SaveDelivery(ctx, delivery)
				}
			}
		}
		cancel()
	}
}
func validateChannelConfig(c alertdomain.Channel) error {
	require := func(keys ...string) error {
		for _, key := range keys {
			if strings.TrimSpace(c.Config[key]) == "" {
				return fmt.Errorf("%s is required for %s", key, c.Type)
			}
		}
		return nil
	}
	switch c.Type {
	case "email":
		return require("host", "username", "password", "from", "to")
	case "dingtalk", "feishu":
		return require("webhook")
	case "webhook":
		return require("url")
	case "zabbix":
		return require("host")
	}
	return nil
}
func (s *AlertService) TestChannel(ctx context.Context, channel alertdomain.Channel) error {
	if err := validateChannelConfig(channel); err != nil {
		return err
	}
	return s.deliver(ctx, channel, alertdomain.Event{ID: "test", RuleName: "GMHA 告警通道测试", Metric: "gmha_test", MachineID: "manager", Severity: alertdomain.SeverityNotice, Status: "firing", Value: 1, Threshold: 1, Operator: "==", FirstSeenAt: time.Now().UTC(), LastSeenAt: time.Now().UTC()})
}
func (s *AlertService) deliver(ctx context.Context, c alertdomain.Channel, e alertdomain.Event) error {
	title := fmt.Sprintf("[GMHA][%s] %s", strings.ToUpper(string(e.Severity)), e.RuleName)
	text := fmt.Sprintf("%s\n状态: %s\n机器: %s\n指标: %s\n当前值: %v %s 阈值: %v\n时间: %s", title, e.Status, e.MachineID, e.Metric, e.Value, e.Operator, e.Threshold, e.LastSeenAt.Format(time.RFC3339))
	switch c.Type {
	case "email":
		return sendAlertEmail(c.Config, title, text)
	case "dingtalk":
		return s.postJSON(ctx, c.Config["webhook"], map[string]any{"msgtype": "markdown", "markdown": map[string]string{"title": title, "text": text}})
	case "feishu":
		return s.postJSON(ctx, c.Config["webhook"], map[string]any{"msg_type": "text", "content": map[string]string{"text": text}})
	case "webhook", "zabbix":
		if c.Type == "zabbix" && c.Config["host"] != "" {
			return sendZabbix(ctx, c.Config, e)
		}
		return s.postJSON(ctx, c.Config["url"], map[string]any{"source": "gmha", "event": e})
	}
	return nil
}
func sendZabbix(ctx context.Context, cfg map[string]string, event alertdomain.Event) error {
	port := cfg["port"]
	if port == "" {
		port = "10051"
	}
	dialer := net.Dialer{Timeout: 5 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(cfg["host"], port))
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(8 * time.Second))
	payload, _ := json.Marshal(map[string]any{"request": "sender data", "data": []map[string]any{{"host": event.MachineID, "key": "gmha.alert." + event.Metric, "value": event.Value, "clock": event.LastSeenAt.Unix()}}})
	header := append([]byte{'Z', 'B', 'X', 'D', 1}, make([]byte, 8)...)
	binary.LittleEndian.PutUint64(header[5:], uint64(len(payload)))
	if _, err = conn.Write(append(header, payload...)); err != nil {
		return err
	}
	response, err := io.ReadAll(io.LimitReader(conn, 64*1024))
	if err != nil {
		return err
	}
	if len(response) < 13 || string(response[:4]) != "ZBXD" {
		return errors.New("invalid zabbix response")
	}
	var body map[string]any
	if err := json.Unmarshal(response[13:], &body); err != nil {
		return err
	}
	if body["response"] != "success" {
		return fmt.Errorf("zabbix rejected alert: %v", body["info"])
	}
	return nil
}
func (s *AlertService) postJSON(ctx context.Context, url string, payload any) error {
	if url == "" {
		return errors.New("webhook url is required")
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("remote returned %s", resp.Status)
	}
	return nil
}
func sendAlertEmail(cfg map[string]string, subject, body string) error {
	host := cfg["host"]
	port := cfg["port"]
	if port == "" {
		port = "25"
	}
	from := cfg["from"]
	to := strings.Split(cfg["to"], ",")
	if host == "" || from == "" || len(to) == 0 {
		return errors.New("email host, from and to are required")
	}
	addr := host + ":" + port
	auth := smtp.PlainAuth("", cfg["username"], cfg["password"], host)
	msg := []byte("To: " + strings.Join(to, ",") + "\r\nSubject: " + subject + "\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n" + body)
	if cfg["tls"] != "true" && cfg["starttls"] != "true" {
		return smtp.SendMail(addr, auth, from, to, msg)
	}
	if cfg["starttls"] == "true" {
		client, err := smtp.Dial(addr)
		if err != nil {
			return err
		}
		defer client.Close()
		if err = client.StartTLS(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}); err != nil {
			return err
		}
		if cfg["username"] != "" {
			if err = client.Auth(auth); err != nil {
				return err
			}
		}
		if err = client.Mail(from); err != nil {
			return err
		}
		for _, x := range to {
			if err = client.Rcpt(strings.TrimSpace(x)); err != nil {
				return err
			}
		}
		w, err := client.Data()
		if err != nil {
			return err
		}
		if _, err = w.Write(msg); err != nil {
			return err
		}
		return w.Close()
	}
	conn, err := tls.Dial("tcp", addr, &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12})
	if err != nil {
		return err
	}
	client, err := smtp.NewClient(conn, host)
	if err != nil {
		return err
	}
	defer client.Close()
	if cfg["username"] != "" {
		if err = client.Auth(auth); err != nil {
			return err
		}
	}
	if err = client.Mail(from); err != nil {
		return err
	}
	for _, x := range to {
		if err = client.Rcpt(strings.TrimSpace(x)); err != nil {
			return err
		}
	}
	w, err := client.Data()
	if err != nil {
		return err
	}
	if _, err = w.Write(msg); err != nil {
		return err
	}
	return w.Close()
}

func metricNumber(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case bool:
		if x {
			return 1, true
		}
		return 0, true
	case string:
		f, e := strconv.ParseFloat(strings.TrimSpace(x), 64)
		return f, e == nil
	}
	return 0, false
}
func validOperator(v string) bool {
	for _, x := range []string{">", ">=", "<", "<=", "==", "!="} {
		if v == x {
			return true
		}
	}
	return false
}
func compare(v float64, op string, t float64) bool {
	switch op {
	case ">":
		return v > t
	case ">=":
		return v >= t
	case "<":
		return v < t
	case "<=":
		return v <= t
	case "==":
		return v == t
	case "!=":
		return v != t
	}
	return false
}
func labelsMatch(expected, actual map[string]string) bool {
	for k, v := range expected {
		if actual[k] != v {
			return false
		}
	}
	return true
}
func fingerprint(ruleID, machineID string, labels map[string]string) string {
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	b := strings.Builder{}
	b.WriteString(ruleID + "|" + machineID)
	for _, k := range keys {
		b.WriteString("|" + k + "=" + labels[k])
	}
	return stableID(b.String(), "")
}
func stableID(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return hex.EncodeToString(sum[:12])
}
