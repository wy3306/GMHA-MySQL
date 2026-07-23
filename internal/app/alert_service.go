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
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	alertdomain "gmha/internal/domain/alert"
	dynamicdomain "gmha/internal/domain/dynamic"
	hbdomain "gmha/internal/domain/heartbeat"
)

// AlertService evaluates metrics on the Manager. Notifications are queued so
// a slow third-party endpoint can never delay Agent heartbeat processing.
type AlertService struct {
	repo        alertdomain.Repository
	queue       chan alertdomain.NotificationJob
	evaluations chan hbdomain.HeartbeatPayload
	http        *http.Client

	overflowMu sync.Mutex
	overflow   map[string]hbdomain.HeartbeatPayload
	inFlight   sync.Map
	runtime    alertRuntimeCounters
}

type alertRuntimeCounters struct {
	evaluationsReceived   atomic.Uint64
	evaluationsProcessed  atomic.Uint64
	evaluationsCoalesced  atomic.Uint64
	notificationsQueued   atomic.Uint64
	notificationsDeferred atomic.Uint64
	notificationsDropped  atomic.Uint64
	deliveriesSucceeded   atomic.Uint64
	deliveriesFailed      atomic.Uint64
	lastEvaluationUnixMS  atomic.Int64
	lastDeliveryUnixMS    atomic.Int64
}

type AlertQueueStatus struct {
	Depth          int `json:"depth"`
	Capacity       int `json:"capacity"`
	Overflow       int `json:"overflow,omitempty"`
	DurablePending int `json:"durable_pending"`
}

type AlertRuntimeStatus struct {
	Healthy               bool             `json:"healthy"`
	EvaluationQueue       AlertQueueStatus `json:"evaluation_queue"`
	NotificationQueue     AlertQueueStatus `json:"notification_queue"`
	EvaluationsReceived   uint64           `json:"evaluations_received"`
	EvaluationsProcessed  uint64           `json:"evaluations_processed"`
	EvaluationsCoalesced  uint64           `json:"evaluations_coalesced"`
	NotificationsQueued   uint64           `json:"notifications_queued"`
	NotificationsDeferred uint64           `json:"notifications_deferred"`
	NotificationsDropped  uint64           `json:"notifications_dropped"`
	DeliveriesSucceeded   uint64           `json:"deliveries_succeeded"`
	DeliveriesFailed      uint64           `json:"deliveries_failed"`
	LastEvaluationAt      *time.Time       `json:"last_evaluation_at,omitempty"`
	LastDeliveryAt        *time.Time       `json:"last_delivery_at,omitempty"`
}

func NewAlertService(repo alertdomain.Repository) *AlertService {
	s := &AlertService{
		repo: repo, queue: make(chan alertdomain.NotificationJob, 256),
		evaluations: make(chan hbdomain.HeartbeatPayload, 256),
		overflow:    make(map[string]hbdomain.HeartbeatPayload),
		http:        &http.Client{Timeout: 8 * time.Second},
	}
	go s.deliveryLoop()
	go s.evaluationLoop()
	if _, ok := repo.(alertdomain.NotificationOutbox); ok {
		go s.notificationRecoveryLoop()
	}
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
		{Name: "主机文件系统空间不足", Metric: "host_filesystem_used_percent", Operator: ">=", Threshold: 85, Severity: alertdomain.SeverityCritical, ConsecutiveCount: 2},
		{Name: "主机 Inode 使用率过高", Metric: "host_inode_used_percent", Operator: ">=", Threshold: 85, Severity: alertdomain.SeverityWarning, ConsecutiveCount: 2},
		{Name: "主机 Swap 使用率过高", Metric: "host_swap_used_percent", Operator: ">=", Threshold: 80, Severity: alertdomain.SeverityWarning, ConsecutiveCount: 3},
		{Name: "SSH 服务探测失败", Metric: "host_ssh_probe_ok", Operator: "==", Threshold: 0, Severity: alertdomain.SeverityCritical, ConsecutiveCount: 2},
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
		return x, alertdomain.Invalid("name and metric are required")
	}
	if !validOperator(x.Operator) {
		return x, alertdomain.Invalid("operator must be one of >, >=, <, <=, ==, !=")
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
			return x, alertdomain.Invalid("threshold severity must be valid and unique")
		}
		seenSeverities[level.Severity] = true
		normalized = append(normalized, level)
	}
	if len(normalized) == 0 {
		return x, alertdomain.Invalid("at least one severity threshold must be enabled")
	}
	sort.Slice(normalized, func(i, j int) bool {
		return alertdomain.SeverityRank(normalized[i].Severity) < alertdomain.SeverityRank(normalized[j].Severity)
	})
	if err := validateThresholdOrder(x.Operator, normalized); err != nil {
		return x, err
	}
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
		return x, alertdomain.Invalid("filter name is required")
	}
	if strings.TrimSpace(x.ClusterPattern+x.MachinePattern+x.IPCIDR+x.CategoryPattern+x.MessagePattern) == "" {
		return x, alertdomain.Invalid("at least one filter condition is required")
	}
	if x.IPCIDR != "" {
		if _, _, err := net.ParseCIDR(strings.TrimSpace(x.IPCIDR)); err != nil {
			return x, alertdomain.Invalid("invalid IP CIDR")
		}
	}
	if x.UseRegex {
		for _, pattern := range []string{x.ClusterPattern, x.MachinePattern, x.CategoryPattern, x.MessagePattern} {
			if pattern != "" {
				if _, err := regexp.Compile(pattern); err != nil {
					return x, alertdomain.Invalid(fmt.Sprintf("invalid regular expression: %v", err))
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
	return s.repo.ListEvents(ctx, f)
}
func (s *AlertService) Summary(ctx context.Context) (alertdomain.EventSummary, error) {
	if reader, ok := s.repo.(alertdomain.EventSummaryReader); ok {
		return reader.SummarizeEvents(ctx, time.Now().UTC())
	}
	items, err := s.ListEvents(ctx, alertdomain.EventFilter{Limit: 1000})
	if err != nil {
		return alertdomain.EventSummary{}, err
	}
	out := alertdomain.EventSummary{Counts: defaultAlertCounts(), Total: len(items)}
	now := time.Now().UTC()
	for _, event := range items {
		out.Counts[event.Status]++
		if event.LastSeenAt.After(now.Add(-24 * time.Hour)) {
			out.Last24Hours++
		}
		if event.Status != "firing" {
			continue
		}
		out.Counts[string(event.Severity)]++
		if event.AcknowledgedAt != nil {
			out.ActiveAcknowledged++
		}
		if event.SilencedUntil != nil && event.SilencedUntil.After(now) {
			out.ActiveSilenced++
		}
	}
	return out, nil
}
func defaultAlertCounts() map[string]int {
	return map[string]int{"firing": 0, "resolved": 0, "notice": 0, "warning": 0, "critical": 0, "fatal": 0}
}
func (s *AlertService) EventAction(ctx context.Context, id, action, actor string, until *time.Time) error {
	if strings.TrimSpace(id) == "" {
		return alertdomain.Invalid("event id is required")
	}
	switch action {
	case "acknowledge", "resolve":
	case "silence":
		if until == nil || !until.After(time.Now().UTC()) {
			return alertdomain.Invalid("silence deadline must be in the future")
		}
	default:
		return alertdomain.Invalid("action must be acknowledge, silence or resolve")
	}
	return s.repo.UpdateEventAction(ctx, id, action, actor, until)
}
func (s *AlertService) UpdateAutomationState(ctx context.Context, id, state, expectedState string) error {
	if strings.TrimSpace(id) == "" {
		return alertdomain.Invalid("event id is required")
	}
	switch state {
	case "pending", "claimed", "running", "succeeded", "failed", "skipped":
	default:
		return alertdomain.Invalid("invalid automation state")
	}
	if expectedState != "" {
		switch expectedState {
		case "pending", "claimed", "running", "succeeded", "failed", "skipped":
		default:
			return alertdomain.Invalid("invalid expected automation state")
		}
	}
	return s.repo.UpdateAutomationState(ctx, id, state, expectedState)
}
func (s *AlertService) ListChannels(ctx context.Context) ([]alertdomain.Channel, error) {
	return s.repo.ListChannels(ctx)
}
func (s *AlertService) SaveChannel(ctx context.Context, x alertdomain.Channel) (alertdomain.Channel, error) {
	x.Name, x.Type = strings.TrimSpace(x.Name), strings.TrimSpace(x.Type)
	if x.Name == "" || x.Type == "" {
		return x, alertdomain.Invalid("name and type are required")
	}
	switch x.Type {
	case "email", "dingtalk", "feishu", "webhook", "zabbix":
	default:
		return x, alertdomain.Invalid("unsupported channel type")
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
	if alertdomain.SeverityRank(x.MinimumSeverity) == 0 {
		return x, alertdomain.Invalid("minimum severity is invalid")
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
	s.runtime.evaluationsReceived.Add(1)
	select {
	case s.evaluations <- payload:
	default:
		key := payload.AgentID
		if key == "" {
			key = payload.MachineID
		}
		if key == "" {
			key = fmt.Sprintf("anonymous-%d", time.Now().UnixNano())
		}
		s.overflowMu.Lock()
		s.overflow[key] = payload
		s.overflowMu.Unlock()
		s.runtime.evaluationsCoalesced.Add(1)
	}
}
func (s *AlertService) evaluationLoop() {
	for payload := range s.evaluations {
		s.processEvaluation(payload)
		for {
			next, ok := s.popOverflow()
			if !ok {
				break
			}
			s.processEvaluation(next)
		}
	}
}
func (s *AlertService) processEvaluation(payload hbdomain.HeartbeatPayload) {
	s.evaluatePayload(context.Background(), payload)
	s.runtime.evaluationsProcessed.Add(1)
	s.runtime.lastEvaluationUnixMS.Store(time.Now().UTC().UnixMilli())
}
func (s *AlertService) popOverflow() (hbdomain.HeartbeatPayload, bool) {
	s.overflowMu.Lock()
	defer s.overflowMu.Unlock()
	for key, payload := range s.overflow {
		delete(s.overflow, key)
		return payload, true
	}
	return hbdomain.HeartbeatPayload{}, false
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
	for _, metric := range alertEvaluationMetrics(payload.Metrics) {
		numeric, _ := metricNumber(metric.Value)
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
				s.suppressEvaluation(ctx, rule, payload, metric, numeric)
				continue
			}
			s.evaluate(ctx, rule, payload, metric, numeric)
		}
	}
}

func (s *AlertService) suppressEvaluation(ctx context.Context, rule alertdomain.Rule, payload hbdomain.HeartbeatPayload, metric dynamicdomain.MetricResult, value float64) {
	fp := fingerprint(rule.ID, payload.MachineID, metric.Labels)
	now := time.Now().UTC()
	state := alertdomain.EvaluationState{
		Fingerprint: fp, RuleID: rule.ID, Consecutive: 0, LastValue: value,
		LastSampleAt: metric.CollectedAt.UTC(), UpdatedAt: now,
	}
	_ = s.repo.SaveEvaluationState(ctx, state)
	activeEvents, err := s.activeEventsForTarget(ctx, rule, payload.MachineID, metric.Labels, fp)
	if err != nil {
		return
	}
	for _, active := range activeEvents {
		resolveAlertEvent(&active, value, now, "suppressed_by_filter")
		_ = s.repo.SaveEvent(ctx, active)
	}
}

// alertEvaluationMetrics turns structured collectors into the same normalized
// numeric leaf metrics used by the performance API. This makes disk devices,
// filesystems, network interfaces and load averages first-class alert targets.
func alertEvaluationMetrics(metrics []dynamicdomain.MetricResult) []dynamicdomain.MetricResult {
	out := make([]dynamicdomain.MetricResult, 0, len(metrics)*2)
	for _, metric := range metrics {
		if !metric.Success {
			continue
		}
		if number, ok := performanceNumber(metric.Value); ok {
			metric.Value = number
			metric.ValueType = dynamicdomain.ValueTypeFloat
			out = append(out, metric)
			continue
		}
		leaves := flattenPerformanceMetric(metric)
		if len(leaves) == 0 && strings.HasPrefix(metric.Name, "mysql_") {
			if number, ok := structuredMetricNumber(metric.Value); ok {
				leaves = append(leaves, performanceLeaf{name: metric.Name, category: metric.Category, value: number, valueType: dynamicdomain.ValueTypeFloat})
			}
		}
		for _, leaf := range leaves {
			out = append(out, dynamicdomain.MetricResult{
				Name: leaf.name, Category: leaf.category, Success: true,
				ValueType: leaf.valueType, Value: leaf.value,
				Labels:      mergeLabels(metric.Labels, leaf.labels),
				CollectedAt: metric.CollectedAt, DurationMS: metric.DurationMS,
			})
		}
	}
	return out
}

func (s *AlertService) evaluate(ctx context.Context, rule alertdomain.Rule, payload hbdomain.HeartbeatPayload, metric dynamicdomain.MetricResult, v float64) {
	fp := fingerprint(rule.ID, payload.MachineID, metric.Labels)
	state, _, err := s.repo.GetEvaluationState(ctx, fp)
	if err != nil {
		return
	}
	if !metric.CollectedAt.IsZero() && !state.LastSampleAt.IsZero() && !metric.CollectedAt.After(state.LastSampleAt) {
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
	state.LastSampleAt = metric.CollectedAt.UTC()
	state.UpdatedAt = now
	_ = s.repo.SaveEvaluationState(ctx, state)
	activeEvents, err := s.activeEventsForTarget(ctx, rule, payload.MachineID, metric.Labels, fp)
	if err != nil {
		return
	}
	if !firing {
		for _, active := range activeEvents {
			resolveAlertEvent(&active, v, now, "condition_cleared")
			_ = s.repo.SaveEvent(ctx, active)
			s.enqueue(active)
		}
		return
	}
	if state.Consecutive < rule.ConsecutiveCount {
		return
	}
	var active alertdomain.Event
	ok := len(activeEvents) > 0
	if ok {
		active = activeEvents[0]
		active.Fingerprint = fp
		for _, duplicate := range activeEvents[1:] {
			resolveAlertEvent(&duplicate, v, now, "duplicate_merged")
			_ = s.repo.SaveEvent(ctx, duplicate)
		}
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
	// Persist the event before creating its durable notification job. External
	// consumers must never receive an event ID that is absent from the API.
	if err := s.repo.SaveEvent(ctx, active); err != nil {
		return
	}
	if notify {
		notify = s.enqueue(active)
		if notify {
			active.NotificationCount++
			active.LastNotifiedAt = &now
			_ = s.repo.SaveEvent(ctx, active)
		}
	}
}

func (s *AlertService) activeEventsForTarget(ctx context.Context, rule alertdomain.Rule, machineID string, labels map[string]string, fp string) ([]alertdomain.Event, error) {
	if reader, ok := s.repo.(alertdomain.ActiveEventReader); ok {
		candidates, err := reader.ListActiveEventsForRuleTarget(ctx, rule.ID, machineID)
		if err != nil {
			return nil, err
		}
		out := make([]alertdomain.Event, 0, len(candidates))
		for _, event := range candidates {
			if event.Fingerprint == fp || sameAlertTarget(event.Labels, labels) {
				out = append(out, event)
			}
		}
		return out, nil
	}
	active, found, err := s.repo.GetActiveEvent(ctx, fp)
	if err != nil || !found {
		return nil, err
	}
	return []alertdomain.Event{active}, nil
}

func resolveAlertEvent(event *alertdomain.Event, value float64, now time.Time, reason string) {
	event.Status = "resolved"
	event.Value = value
	event.LastSeenAt = now
	event.ResolvedAt = &now
	if event.Labels == nil {
		event.Labels = map[string]string{}
	}
	event.Labels["resolution_reason"] = reason
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
	now := time.Now().UTC()
	job := alertdomain.NotificationJob{
		ID:    stableID("notification", event.ID, fmt.Sprint(now.UnixNano())),
		Event: event, CreatedAt: now, UpdatedAt: now,
	}
	durable := false
	if outbox, ok := s.repo.(alertdomain.NotificationOutbox); ok {
		if err := outbox.SaveNotificationJob(context.Background(), job); err != nil {
			s.runtime.notificationsDropped.Add(1)
			return false
		}
		durable = true
	}
	s.inFlight.Store(job.ID, struct{}{})
	select {
	case s.queue <- job:
		s.runtime.notificationsQueued.Add(1)
		return true
	default:
		s.inFlight.Delete(job.ID)
		if durable {
			s.runtime.notificationsDeferred.Add(1)
			return true
		}
		s.runtime.notificationsDropped.Add(1)
		return false
	}
}
func (s *AlertService) deliveryLoop() {
	for job := range s.queue {
		func() {
			defer s.inFlight.Delete(job.ID)
			event := job.Event
			allSucceeded := true
			lastError := ""
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			channels, err := s.repo.ListChannels(ctx)
			cancel()
			if err != nil {
				allSucceeded, lastError = false, err.Error()
			} else {
				for _, channel := range channels {
					if channel.Enabled && alertdomain.SeverityRank(event.Severity) >= alertdomain.SeverityRank(channel.MinimumSeverity) {
						ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
						var deliveryErr error
					retryLoop:
						for attempt := 0; attempt < 3; attempt++ {
							deliveryErr = s.deliver(ctx, channel, event)
							if deliveryErr == nil {
								break
							}
							select {
							case <-ctx.Done():
								break retryLoop
							case <-time.After(time.Duration(attempt+1) * 200 * time.Millisecond):
							}
						}
						cancel()
						now := time.Now().UTC()
						channel.UpdatedAt = now
						if deliveryErr != nil {
							channel.LastStatus = "failed"
							channel.LastError = deliveryErr.Error()
							allSucceeded, lastError = false, deliveryErr.Error()
							s.runtime.deliveriesFailed.Add(1)
						} else {
							channel.LastStatus = "success"
							channel.LastError = ""
							channel.LastDeliveredAt = &now
							s.runtime.deliveriesSucceeded.Add(1)
						}
						persistCtx, persistCancel := context.WithTimeout(context.Background(), 3*time.Second)
						if err := s.repo.SaveChannel(persistCtx, channel); err != nil {
							allSucceeded, lastError = false, err.Error()
						}
						delivery := alertdomain.Delivery{ID: stableID(event.ID, channel.ID, fmt.Sprint(now.UnixNano())), EventID: event.ID, RuleName: event.RuleName, Severity: event.Severity, MachineID: event.MachineID, ChannelID: channel.ID, ChannelName: channel.Name, ChannelType: channel.Type, Status: channel.LastStatus, DeliveredAt: now}
						if deliveryErr != nil {
							delivery.Error = deliveryErr.Error()
						}
						if err := s.repo.SaveDelivery(persistCtx, delivery); err != nil {
							allSucceeded, lastError = false, err.Error()
						}
						persistCancel()
						s.runtime.lastDeliveryUnixMS.Store(now.UnixMilli())
					}
				}
			}
			if outbox, ok := s.repo.(alertdomain.NotificationOutbox); ok {
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				_ = outbox.FinishNotificationJob(ctx, job.ID, allSucceeded, lastError, time.Now().UTC())
				cancel()
			}
		}()
	}
}

func (s *AlertService) notificationRecoveryLoop() {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		outbox, ok := s.repo.(alertdomain.NotificationOutbox)
		if !ok {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		jobs, err := outbox.ListPendingNotificationJobs(ctx, time.Now().UTC().Add(-30*time.Second), 100)
		cancel()
		if err != nil {
			continue
		}
		for _, job := range jobs {
			if _, loaded := s.inFlight.LoadOrStore(job.ID, struct{}{}); loaded {
				continue
			}
			select {
			case s.queue <- job:
				s.runtime.notificationsQueued.Add(1)
			default:
				s.inFlight.Delete(job.ID)
				s.runtime.notificationsDeferred.Add(1)
				return
			}
		}
	}
}

func (s *AlertService) RuntimeStatus() AlertRuntimeStatus {
	s.overflowMu.Lock()
	overflow := len(s.overflow)
	s.overflowMu.Unlock()
	durablePending := 0
	if outbox, ok := s.repo.(alertdomain.NotificationOutbox); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		durablePending, _ = outbox.CountPendingNotificationJobs(ctx)
		cancel()
	}
	out := AlertRuntimeStatus{
		EvaluationQueue:       AlertQueueStatus{Depth: len(s.evaluations), Capacity: cap(s.evaluations), Overflow: overflow},
		NotificationQueue:     AlertQueueStatus{Depth: len(s.queue), Capacity: cap(s.queue), DurablePending: durablePending},
		EvaluationsReceived:   s.runtime.evaluationsReceived.Load(),
		EvaluationsProcessed:  s.runtime.evaluationsProcessed.Load(),
		EvaluationsCoalesced:  s.runtime.evaluationsCoalesced.Load(),
		NotificationsQueued:   s.runtime.notificationsQueued.Load(),
		NotificationsDeferred: s.runtime.notificationsDeferred.Load(),
		NotificationsDropped:  s.runtime.notificationsDropped.Load(),
		DeliveriesSucceeded:   s.runtime.deliveriesSucceeded.Load(),
		DeliveriesFailed:      s.runtime.deliveriesFailed.Load(),
	}
	out.Healthy = overflow == 0 &&
		out.EvaluationQueue.Depth < out.EvaluationQueue.Capacity*4/5 &&
		out.NotificationQueue.Depth < out.NotificationQueue.Capacity*4/5 &&
		out.NotificationQueue.DurablePending < out.NotificationQueue.Capacity*4/5
	out.LastEvaluationAt = unixMilliTime(s.runtime.lastEvaluationUnixMS.Load())
	out.LastDeliveryAt = unixMilliTime(s.runtime.lastDeliveryUnixMS.Load())
	return out
}

func unixMilliTime(value int64) *time.Time {
	if value <= 0 {
		return nil
	}
	result := time.UnixMilli(value).UTC()
	return &result
}
func validateChannelConfig(c alertdomain.Channel) error {
	require := func(keys ...string) error {
		for _, key := range keys {
			if strings.TrimSpace(c.Config[key]) == "" {
				return alertdomain.Invalid(fmt.Sprintf("%s is required for %s", key, c.Type))
			}
		}
		return nil
	}
	switch c.Type {
	case "email":
		if err := require("host", "username", "password", "from", "to"); err != nil {
			return err
		}
		if err := validatePort(c.Config["port"], 25); err != nil {
			return err
		}
		return nil
	case "dingtalk", "feishu":
		if err := require("webhook"); err != nil {
			return err
		}
		return validateAlertHTTPURL(c.Config["webhook"])
	case "webhook":
		if err := require("url"); err != nil {
			return err
		}
		return validateAlertHTTPURL(c.Config["url"])
	case "zabbix":
		if err := require("host"); err != nil {
			return err
		}
		return validatePort(c.Config["port"], 10051)
	}
	return nil
}
func validateAlertHTTPURL(raw string) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return alertdomain.Invalid("webhook address must be a valid http or https URL")
	}
	return nil
}
func validatePort(raw string, defaultPort int) error {
	if strings.TrimSpace(raw) == "" {
		raw = strconv.Itoa(defaultPort)
	}
	port, err := strconv.Atoi(raw)
	if err != nil || port < 1 || port > 65535 {
		return alertdomain.Invalid("port must be between 1 and 65535")
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
func validateThresholdOrder(operator string, levels []alertdomain.ThresholdLevel) error {
	if len(levels) <= 1 {
		return nil
	}
	if operator == "==" || operator == "!=" {
		return alertdomain.Invalid("== and != rules support exactly one enabled severity threshold")
	}
	for i := 1; i < len(levels); i++ {
		previous, current := levels[i-1].Threshold, levels[i].Threshold
		if (operator == ">" || operator == ">=") && current < previous {
			return alertdomain.Invalid("higher severities must use greater or equal thresholds")
		}
		if (operator == "<" || operator == "<=") && current > previous {
			return alertdomain.Invalid("higher severities must use lower or equal thresholds")
		}
	}
	return nil
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
	identity := alertIdentityLabels(labels)
	keys := make([]string, 0, len(identity))
	for k := range identity {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	b := strings.Builder{}
	b.WriteString(ruleID + "|" + machineID)
	for _, k := range keys {
		b.WriteString("|" + k + "=" + identity[k])
	}
	return stableID(b.String(), "")
}

func alertIdentityLabels(labels map[string]string) map[string]string {
	out := make(map[string]string, len(labels))
	mysqlPort := strings.TrimSpace(labels["mysql_port"])
	for key, raw := range labels {
		value := strings.TrimSpace(raw)
		if value == "" || !alertIdentityLabel(key, mysqlPort != "") {
			continue
		}
		out[key] = value
	}
	if mysqlPort != "" {
		out["mysql_port"] = mysqlPort
	}
	return out
}

func alertIdentityLabel(key string, hasMySQLPort bool) bool {
	switch key {
	case "display_name", "metric_scope", "machine_name", "machine_ip", "alert_category", "resolution_reason":
		return false
	case "mysql_host", "mysql_endpoint", "mysql_instance":
		return !hasMySQLPort
	default:
		return true
	}
}

func sameAlertTarget(left, right map[string]string) bool {
	a, b := alertIdentityLabels(left), alertIdentityLabels(right)
	if len(a) != len(b) {
		return false
	}
	for key, value := range a {
		if b[key] != value {
			return false
		}
	}
	return true
}
func stableID(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return hex.EncodeToString(sum[:12])
}
