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
	"html"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/http"
	"net/mail"
	"net/smtp"
	"net/textproto"
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
	accounts    *AccountService
	queue       chan alertdomain.NotificationJob
	evaluations chan hbdomain.HeartbeatPayload
	http        *http.Client

	overflowMu sync.Mutex
	overflow   map[string]hbdomain.HeartbeatPayload
	channelMu  sync.RWMutex
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

func (s *AlertService) ConfigureAccountRecipients(accounts *AccountService) {
	s.accounts = accounts
}

func (s *AlertService) AlertRecipientDirectory(ctx context.Context) (AlertRecipientDirectory, error) {
	if s.accounts == nil {
		return AlertRecipientDirectory{}, errors.New("平台账号服务尚未就绪")
	}
	return s.accounts.AlertRecipientDirectory(ctx)
}

func alertLevel(severity alertdomain.Severity, threshold float64) alertdomain.ThresholdLevel {
	return alertdomain.ThresholdLevel{Severity: severity, Threshold: threshold, Enabled: true}
}

func defaultAlertRule(name, metric, operator string, consecutive int, levels ...alertdomain.ThresholdLevel) alertdomain.Rule {
	rule := alertdomain.Rule{
		Name: name, Metric: metric, Operator: operator, Thresholds: levels,
		ConsecutiveCount: consecutive, Enabled: true, Scope: "all",
		RepeatIntervalSeconds: 300, MaxNotifications: 10,
		Description: "系统内置基线规则，可按业务负载、实例规格和维护窗口调整",
	}
	if len(levels) > 0 {
		rule.Severity, rule.Threshold = levels[0].Severity, levels[0].Threshold
	}
	return rule
}

func defaultAlertRules() []alertdomain.Rule {
	notice, warning := alertdomain.SeverityNotice, alertdomain.SeverityWarning
	critical, fatal := alertdomain.SeverityCritical, alertdomain.SeverityFatal
	return []alertdomain.Rule{
		// Host capacity and operating-system health.
		defaultAlertRule("主机 CPU 使用率过高", "cpu_usage_percent", ">=", 3, alertLevel(warning, 80), alertLevel(critical, 90), alertLevel(fatal, 97)),
		defaultAlertRule("主机内存使用率过高", "mem_usage_percent", ">=", 3, alertLevel(warning, 85), alertLevel(critical, 92), alertLevel(fatal, 97)),
		defaultAlertRule("主机文件系统空间不足", "host_filesystem_used_percent", ">=", 2, alertLevel(notice, 80), alertLevel(warning, 85), alertLevel(critical, 92), alertLevel(fatal, 97)),
		defaultAlertRule("主机 Inode 使用率过高", "host_inode_used_percent", ">=", 2, alertLevel(warning, 85), alertLevel(critical, 92), alertLevel(fatal, 97)),
		defaultAlertRule("主机 Swap 使用率过高", "host_swap_used_percent", ">=", 3, alertLevel(notice, 50), alertLevel(warning, 80), alertLevel(critical, 95)),
		defaultAlertRule("主机磁盘 IO 持续繁忙", "host_disk_busy_percent", ">=", 6, alertLevel(warning, 80), alertLevel(critical, 95)),
		defaultAlertRule("主机时钟偏移过大", "ntp_offset_abs_ms", ">=", 2, alertLevel(warning, 100), alertLevel(critical, 500), alertLevel(fatal, 2000)),
		defaultAlertRule("SSH 服务探测失败", "host_ssh_probe_ok", "==", 2, alertLevel(critical, 0)),

		// Agent availability and self-observation.
		defaultAlertRule("Agent 心跳中断", "agent_heartbeat_alive", "==", 1, alertLevel(fatal, 0)),
		defaultAlertRule("Agent 健康状态异常", "agent_overall_health", ">=", 2, alertLevel(warning, 1)),
		defaultAlertRule("Agent 健康检查失败", "agent_health_check_failed", ">=", 2, alertLevel(warning, 1)),
		defaultAlertRule("Agent 内存占用过高", "agent_memory_rss_mb", ">=", 5, alertLevel(warning, 256), alertLevel(critical, 512)),
		defaultAlertRule("Agent CPU 占用过高", "agent_cpu_usage_percent", ">=", 5, alertLevel(warning, 10), alertLevel(critical, 30)),

		// MySQL availability and connection capacity. Restart events are emitted
		// by the uptime observer and classified as manual or unexpected.
		defaultAlertRule("MySQL 无法连接", "mysql_connectivity", "==", 2, alertLevel(fatal, 0)),
		defaultAlertRule("MySQL 进程停止", "mysql_process_alive", "==", 2, alertLevel(fatal, 0)),
		defaultAlertRule("MySQL 端口未监听", "mysql_port_listening", "==", 2, alertLevel(critical, 0)),
		defaultAlertRule("连接使用率偏高", "mysql_connection_usage_percent", ">=", 3, alertLevel(notice, 70), alertLevel(warning, 80), alertLevel(critical, 90), alertLevel(fatal, 95)),
		defaultAlertRule("长时间空闲连接过多", "mysql_long_sleep_connections", ">=", 3, alertLevel(warning, 50), alertLevel(critical, 200)),

		// Replication and consistency.
		defaultAlertRule("复制延迟过高", "mysql_replication_lag", ">=", 3, alertLevel(notice, 10), alertLevel(warning, 30), alertLevel(critical, 120), alertLevel(fatal, 600)),
		defaultAlertRule("复制 IO 线程异常", "mysql_replica_io_thread", "==", 2, alertLevel(critical, 0)),
		defaultAlertRule("复制 SQL 线程异常", "mysql_replica_sql_thread", "==", 2, alertLevel(critical, 0)),
		defaultAlertRule("复制通道报错", "mysql_replication_error_count", ">=", 1, alertLevel(critical, 1)),
		defaultAlertRule("从库未追平", "mysql_replica_catchup_status", "==", 3, alertLevel(warning, 0)),
		defaultAlertRule("GTID 同步状态异常", "mysql_gtid_sync_status", "==", 3, alertLevel(warning, 0)),

		// Transactions, locks, query behavior and cache pressure.
		defaultAlertRule("长事务持续", "mysql_longest_transaction_seconds", ">=", 3, alertLevel(warning, 300), alertLevel(critical, 1800), alertLevel(fatal, 3600)),
		defaultAlertRule("存在行锁等待", "mysql_lock_wait_sessions", ">=", 2, alertLevel(warning, 1), alertLevel(critical, 10)),
		defaultAlertRule("存在元数据锁等待", "mysql_metadata_lock_waits", ">=", 2, alertLevel(warning, 1), alertLevel(critical, 5)),
		defaultAlertRule("阻塞持续时间过长", "mysql_max_blocking_seconds", ">=", 2, alertLevel(warning, 30), alertLevel(critical, 300), alertLevel(fatal, 1800)),
		defaultAlertRule("阻塞链过长", "mysql_blocking_chain_length", ">=", 2, alertLevel(warning, 3), alertLevel(critical, 10)),
		defaultAlertRule("磁盘临时表比例过高", "mysql_tmp_disk_table_ratio", ">=", 3, alertLevel(warning, 25), alertLevel(critical, 50)),
		defaultAlertRule("全表扫描比例过高", "mysql_table_scan_ratio", ">=", 6, alertLevel(warning, 25), alertLevel(critical, 50)),
		defaultAlertRule("无索引 Join 比例过高", "mysql_join_full_scan_ratio", ">=", 6, alertLevel(warning, 5), alertLevel(critical, 20)),
		defaultAlertRule("最慢 SQL 耗时过长", "mysql_slowest_sql_seconds", ">=", 2, alertLevel(warning, 5), alertLevel(critical, 30), alertLevel(fatal, 120)),
		defaultAlertRule("Buffer Pool 命中率偏低", "mysql_buffer_pool_hit_ratio", "<=", 6, alertLevel(warning, 99), alertLevel(critical, 95)),
		defaultAlertRule("Buffer Pool 脏页比例过高", "mysql_buffer_pool_dirty_ratio", ">=", 6, alertLevel(warning, 60), alertLevel(critical, 80)),
		defaultAlertRule("文件句柄使用率过高", "mysql_open_files_usage_percent", ">=", 3, alertLevel(warning, 80), alertLevel(critical, 90)),
		defaultAlertRule("表缓存使用率过高", "mysql_table_cache_usage_percent", ">=", 6, alertLevel(warning, 90), alertLevel(critical, 98)),
		defaultAlertRule("线程缓存命中率偏低", "mysql_thread_cache_hit_ratio", "<=", 6, alertLevel(warning, 90), alertLevel(critical, 70)),
		defaultAlertRule("Undo History List 积压", "mysql_history_list_length", ">=", 3, alertLevel(warning, 100000), alertLevel(critical, 1000000)),

		// Database filesystems and logical capacity.
		defaultAlertRule("数据盘空间不足", "mysql_data_disk_usage", ">=", 2, alertLevel(warning, 80), alertLevel(critical, 90), alertLevel(fatal, 97)),
		defaultAlertRule("Binlog 盘空间不足", "mysql_binlog_disk_usage", ">=", 2, alertLevel(warning, 80), alertLevel(critical, 90), alertLevel(fatal, 97)),
		defaultAlertRule("Redo 盘空间不足", "mysql_redo_disk_usage", ">=", 2, alertLevel(warning, 80), alertLevel(critical, 90), alertLevel(fatal, 97)),
		defaultAlertRule("临时盘空间不足", "mysql_tmp_disk_usage", ">=", 2, alertLevel(warning, 80), alertLevel(critical, 90), alertLevel(fatal, 97)),
		defaultAlertRule("Undo 盘空间不足", "mysql_undo_disk_usage", ">=", 2, alertLevel(warning, 80), alertLevel(critical, 90), alertLevel(fatal, 97)),
		defaultAlertRule("数据库高碎片表过多", "mysql_fragmented_table_count", ">=", 1, alertLevel(warning, 1), alertLevel(critical, 5), alertLevel(fatal, 20)),
		defaultAlertRule("数据库单表碎片率过高", "mysql_max_table_fragment_percent", ">=", 1, alertLevel(warning, 30), alertLevel(critical, 50), alertLevel(fatal, 70)),
		defaultAlertRule("数据库可回收碎片空间过大", "mysql_tablespace_fragment_total_bytes", ">=", 1, alertLevel(warning, 10737418240), alertLevel(critical, 53687091200)),

		// Error log, durability and safety signals.
		defaultAlertRule("慢查询日志未开启", "mysql_slow_query_log_enabled", "==", 1, alertLevel(notice, 0)),
		defaultAlertRule("错误日志 ERROR 激增", "mysql_recent_error_count", ">=", 2, alertLevel(warning, 10), alertLevel(critical, 100)),
		defaultAlertRule("检测到 MySQL OOM", "mysql_oom_keyword_count", ">=", 1, alertLevel(fatal, 1)),
		defaultAlertRule("检测到磁盘写满错误", "mysql_disk_full_keyword_count", ">=", 1, alertLevel(fatal, 1)),
		defaultAlertRule("检测到表损坏", "mysql_table_corruption_keyword_count", ">=", 1, alertLevel(fatal, 1)),
		defaultAlertRule("检测到崩溃恢复", "mysql_crash_recovery_keyword_count", ">=", 1, alertLevel(critical, 1)),
		defaultAlertRule("数据库权限失败激增", "mysql_permission_failed_keyword_count", ">=", 2, alertLevel(warning, 10), alertLevel(critical, 100)),
	}
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
	defaults := defaultAlertRules()
	now := time.Now().UTC()
	for i := range defaults {
		defaults[i].ID = stableID("default", defaults[i].Metric)
		if existing[defaults[i].ID] {
			continue
		}
		defaults[i].CreatedAt = now
		defaults[i].UpdatedAt = now
		if _, err := s.SaveRule(ctx, defaults[i]); err != nil {
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
	x.RecipientIDs = normalizeStringIDs(x.RecipientIDs)
	var err error
	if s.accounts != nil {
		if x.Type == "email" {
			x.RecipientRoles, err = normalizeChannelChoices(x.RecipientRoles, platformRecipientRoles, nil)
			if err != nil {
				return x, alertdomain.Invalid("选择的平台角色无效")
			}
		} else {
			x.RecipientIDs, x.RecipientRoles = nil, nil
		}
	} else {
		x.RecipientRoles, err = normalizeChannelChoices(x.RecipientRoles, channelRecipientRoles, []string{"oncall"})
		if err != nil {
			return x, alertdomain.Invalid("recipient role is invalid")
		}
	}
	if x, err = s.bindEmailRecipients(ctx, x, false); err != nil {
		return x, err
	}
	if err := validateChannelConfig(x); err != nil {
		return x, err
	}
	x.ContentFilter.Categories, err = normalizeChannelChoices(x.ContentFilter.Categories, channelContentCategories, nil)
	if err != nil {
		return x, alertdomain.Invalid("content category is invalid")
	}
	x.ContentFilter.EventStates, err = normalizeChannelChoices(x.ContentFilter.EventStates, channelEventStates, nil)
	if err != nil {
		return x, alertdomain.Invalid("event state is invalid")
	}
	x.UpdatedAt = now
	s.channelMu.Lock()
	defer s.channelMu.Unlock()
	return x, s.repo.SaveChannel(ctx, x)
}

func (s *AlertService) ListNotificationRoles(ctx context.Context) ([]alertdomain.NotificationRole, error) {
	return s.repo.ListNotificationRoles(ctx)
}

func (s *AlertService) SaveNotificationRole(ctx context.Context, x alertdomain.NotificationRole) (alertdomain.NotificationRole, error) {
	x.Name, x.Description = strings.TrimSpace(x.Name), strings.TrimSpace(x.Description)
	if x.Name == "" {
		return x, alertdomain.Invalid("角色名称不能为空")
	}
	now := time.Now().UTC()
	if x.ID == "" {
		x.ID, x.CreatedAt = stableID("notification-role", x.Name, fmt.Sprint(now.UnixNano())), now
	}
	x.UpdatedAt = now
	return x, s.repo.SaveNotificationRole(ctx, x)
}

func (s *AlertService) DeleteNotificationRole(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return alertdomain.Invalid("角色编号不能为空")
	}
	recipients, err := s.repo.ListNotificationRecipients(ctx)
	if err != nil {
		return err
	}
	for _, recipient := range recipients {
		for _, roleID := range recipient.RoleIDs {
			if roleID == id {
				return alertdomain.ErrConflict
			}
		}
	}
	return s.repo.DeleteNotificationRole(ctx, id)
}

func (s *AlertService) ListNotificationRecipients(ctx context.Context) ([]alertdomain.NotificationRecipient, error) {
	return s.repo.ListNotificationRecipients(ctx)
}

func (s *AlertService) SaveNotificationRecipient(ctx context.Context, x alertdomain.NotificationRecipient) (alertdomain.NotificationRecipient, error) {
	x.Name, x.Email = strings.TrimSpace(x.Name), strings.ToLower(strings.TrimSpace(x.Email))
	if x.Name == "" || x.Email == "" {
		return x, alertdomain.Invalid("人员姓名和邮箱不能为空")
	}
	address, err := mail.ParseAddress(x.Email)
	if err != nil || !strings.EqualFold(address.Address, x.Email) {
		return x, alertdomain.Invalid("邮箱格式不正确")
	}
	x.RoleIDs = normalizeStringIDs(x.RoleIDs)
	roles, err := s.repo.ListNotificationRoles(ctx)
	if err != nil {
		return x, err
	}
	roleSet := make(map[string]bool, len(roles))
	for _, role := range roles {
		roleSet[role.ID] = true
	}
	for _, roleID := range x.RoleIDs {
		if !roleSet[roleID] {
			return x, alertdomain.Invalid("选择的角色不存在")
		}
	}
	now := time.Now().UTC()
	if x.ID == "" {
		x.ID, x.CreatedAt = stableID("notification-recipient", x.Email, fmt.Sprint(now.UnixNano())), now
	}
	x.UpdatedAt = now
	return x, s.repo.SaveNotificationRecipient(ctx, x)
}

func (s *AlertService) DeleteNotificationRecipient(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return alertdomain.Invalid("人员编号不能为空")
	}
	channels, err := s.repo.ListChannels(ctx)
	if err != nil {
		return err
	}
	for _, channel := range channels {
		for _, recipientID := range channel.RecipientIDs {
			if recipientID == id {
				return alertdomain.ErrConflict
			}
		}
	}
	return s.repo.DeleteNotificationRecipient(ctx, id)
}

func (s *AlertService) bindEmailRecipients(ctx context.Context, channel alertdomain.Channel, activeOnly bool) (alertdomain.Channel, error) {
	if channel.Type != "email" {
		return channel, nil
	}
	if s.accounts != nil {
		roles := make([]string, 0, len(channel.RecipientRoles))
		for _, role := range channel.RecipientRoles {
			if platformRecipientRoles[strings.ToLower(strings.TrimSpace(role))] {
				roles = append(roles, strings.ToLower(strings.TrimSpace(role)))
			}
		}
		if len(channel.RecipientIDs) == 0 && len(roles) == 0 {
			if strings.TrimSpace(channel.Config["to"]) != "" {
				return channel, nil
			}
			return channel, alertdomain.Invalid("请至少选择一名推送人员或一个平台角色")
		}
		emails, err := s.accounts.ResolveAlertRecipientEmails(ctx, channel.RecipientIDs, roles)
		if err != nil {
			return channel, alertdomain.Invalid(err.Error())
		}
		config := make(map[string]string, len(channel.Config)+1)
		for key, value := range channel.Config {
			config[key] = value
		}
		config["to"] = strings.Join(emails, ",")
		channel.Config = config
		return channel, nil
	}
	if len(channel.RecipientIDs) == 0 {
		return channel, nil
	}
	recipients, err := s.repo.ListNotificationRecipients(ctx)
	if err != nil {
		return channel, err
	}
	byID := make(map[string]alertdomain.NotificationRecipient, len(recipients))
	for _, recipient := range recipients {
		byID[recipient.ID] = recipient
	}
	emails := make([]string, 0, len(channel.RecipientIDs))
	for _, id := range channel.RecipientIDs {
		recipient, ok := byID[id]
		if !ok {
			return channel, alertdomain.Invalid("选择的通知人员不存在")
		}
		if activeOnly && !recipient.Enabled {
			continue
		}
		emails = append(emails, recipient.Email)
	}
	if len(emails) == 0 {
		return channel, alertdomain.Invalid("没有可接收邮件的已启用人员")
	}
	config := make(map[string]string, len(channel.Config)+1)
	for key, value := range channel.Config {
		config[key] = value
	}
	config["to"] = strings.Join(emails, ",")
	channel.Config = config
	return channel, nil
}
func (s *AlertService) SetChannelEnabled(ctx context.Context, id string, enabled bool) (alertdomain.Channel, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return alertdomain.Channel{}, alertdomain.Invalid("channel id is required")
	}
	s.channelMu.Lock()
	defer s.channelMu.Unlock()
	channels, err := s.repo.ListChannels(ctx)
	if err != nil {
		return alertdomain.Channel{}, err
	}
	for _, channel := range channels {
		if channel.ID != id {
			continue
		}
		channel.Enabled = enabled
		channel.UpdatedAt = time.Now().UTC()
		return channel, s.repo.SaveChannel(ctx, channel)
	}
	return alertdomain.Channel{}, alertdomain.ErrNotFound
}
func (s *AlertService) DeleteChannel(ctx context.Context, id string) error {
	s.channelMu.Lock()
	defer s.channelMu.Unlock()
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
	metrics := alertEvaluationMetrics(payload.Metrics)
	s.observeMySQLRestarts(ctx, payload, metrics, filters)
	for _, metric := range metrics {
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

const mysqlRestartMetric = "mysql_restart_detected"

func (s *AlertService) observeMySQLRestarts(ctx context.Context, payload hbdomain.HeartbeatPayload, metrics []dynamicdomain.MetricResult, filters []alertdomain.Filter) {
	for _, metric := range metrics {
		if metric.Name != "mysql_uptime" {
			continue
		}
		uptime, ok := metricNumber(metric.Value)
		if !ok || uptime < 0 {
			continue
		}
		s.observeMySQLRestart(ctx, payload, metric, uptime, filters)
	}
}

func (s *AlertService) observeMySQLRestart(ctx context.Context, payload hbdomain.HeartbeatPayload, metric dynamicdomain.MetricResult, uptime float64, filters []alertdomain.Filter) {
	sampledAt := metric.CollectedAt.UTC()
	if sampledAt.IsZero() {
		sampledAt = payload.SentAt.UTC()
	}
	if sampledAt.IsZero() {
		sampledAt = time.Now().UTC()
	}
	stateFingerprint := fingerprint(stableID("system", "mysql-restart-observer"), payload.MachineID, metric.Labels)
	state, found, err := s.repo.GetEvaluationState(ctx, stateFingerprint)
	if err != nil {
		return
	}
	if found && !state.LastSampleAt.IsZero() && !sampledAt.After(state.LastSampleAt) {
		return
	}
	next := alertdomain.EvaluationState{
		Fingerprint: stateFingerprint, RuleID: stableID("system", "mysql-restart-observer"),
		Consecutive: 0, LastValue: uptime, LastSampleAt: sampledAt, UpdatedAt: time.Now().UTC(),
	}
	if !found || state.LastSampleAt.IsZero() {
		_ = s.repo.SaveEvaluationState(ctx, next)
		return
	}

	previousBootAt := state.LastSampleAt.Add(-time.Duration(state.LastValue * float64(time.Second)))
	currentBootAt := sampledAt.Add(-time.Duration(uptime * float64(time.Second)))
	// Uptime is sampled with second precision. A 30-second boot-time movement
	// absorbs collection jitter while still detecting a normal MySQL restart.
	if !currentBootAt.After(previousBootAt.Add(30 * time.Second)) {
		_ = s.repo.SaveEvaluationState(ctx, next)
		return
	}

	port, _ := strconv.Atoi(strings.TrimSpace(metric.Labels["mysql_port"]))
	classification := alertdomain.MySQLRestartClassification{}
	if classifier, ok := s.repo.(alertdomain.MySQLRestartClassifier); ok {
		classification, _ = classifier.ClassifyMySQLRestart(ctx, payload.MachineID, port, currentBootAt, sampledAt)
	}
	restartType, ruleName, severity := "unexpected", "MySQL 意外重启", alertdomain.SeverityCritical
	if classification.Manual {
		restartType, ruleName, severity = "manual", "MySQL 手动重启", alertdomain.SeverityNotice
	}
	labels := cloneLabels(metric.Labels)
	labels["restart_type"] = restartType
	labels["restart_boot_time"] = currentBootAt.Truncate(time.Second).Format(time.RFC3339)
	labels["previous_uptime_seconds"] = strconv.FormatInt(int64(state.LastValue), 10)
	labels["current_uptime_seconds"] = strconv.FormatInt(int64(uptime), 10)
	labels["machine_name"], labels["machine_ip"], labels["alert_category"] = alertMachineName(payload), payload.MachineIP, "mysql"
	if classification.TaskID != "" {
		labels["manual_task_id"] = classification.TaskID
	}
	if classification.Operation != "" {
		labels["manual_operation"] = classification.Operation
	}
	restartRule := alertdomain.Rule{
		ID: stableID("system", mysqlRestartMetric), Name: ruleName,
		Description: "检测到 MySQL 启动时间发生变化", Metric: mysqlRestartMetric,
		Operator: "==", Threshold: 1, Severity: severity,
	}
	restartMetric := dynamicdomain.MetricResult{
		Name: mysqlRestartMetric, Category: "mysql", Success: true,
		ValueType: dynamicdomain.ValueTypeFloat, Value: float64(1),
		Labels: labels, CollectedAt: sampledAt,
	}
	if !alertFiltered(filters, restartRule, payload, restartMetric) {
		s.recordMySQLRestartEvent(ctx, payload, restartRule, restartMetric, classification, currentBootAt)
	}
	_ = s.repo.SaveEvaluationState(ctx, next)
}

func (s *AlertService) recordMySQLRestartEvent(ctx context.Context, payload hbdomain.HeartbeatPayload, rule alertdomain.Rule, metric dynamicdomain.MetricResult, classification alertdomain.MySQLRestartClassification, bootAt time.Time) {
	now := time.Now().UTC()
	eventFingerprint := stableID(rule.ID, payload.MachineID, metric.Labels["mysql_port"], bootAt.Truncate(time.Second).Format(time.RFC3339))
	eventID := stableID("event", eventFingerprint)
	if _, found, err := s.repo.GetActiveEvent(ctx, eventFingerprint); err != nil {
		return
	} else if found {
		return
	}
	event := alertdomain.Event{
		ID: eventID, Fingerprint: eventFingerprint, RuleID: rule.ID, RuleName: rule.Name,
		Metric: mysqlRestartMetric, MachineID: payload.MachineID, AgentID: payload.AgentID,
		ClusterID: payload.ClusterID, Labels: cloneLabels(metric.Labels),
		Severity: rule.Severity, Status: "firing", Value: 1, Threshold: 1, Operator: "==",
		OccurrenceCount: 1, FirstSeenAt: now, LastSeenAt: now, AutomationState: "pending",
	}
	if classification.Manual {
		event.AutomationState = "skipped"
	}
	if err := s.repo.SaveEvent(ctx, event); err != nil {
		return
	}
	if s.enqueue(event) {
		event.NotificationCount = 1
		event.LastNotifiedAt = &now
		_ = s.repo.SaveEvent(ctx, event)
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
					if channelMatchesEvent(channel, event) {
						s.channelMu.RLock()
						refreshCtx, refreshCancel := context.WithTimeout(context.Background(), 3*time.Second)
						current, found, refreshErr := s.channelByID(refreshCtx, channel.ID)
						refreshCancel()
						if refreshErr != nil {
							s.channelMu.RUnlock()
							allSucceeded, lastError = false, refreshErr.Error()
							continue
						}
						if !found || !channelMatchesEvent(current, event) {
							s.channelMu.RUnlock()
							continue
						}
						channel = current
						channel, err = s.bindEmailRecipients(context.Background(), channel, true)
						if err != nil {
							now := time.Now().UTC()
							_ = s.repo.UpdateChannelDeliveryStatus(context.Background(), channel.ID, "failed", err.Error(), channel.LastDeliveredAt, now)
							_ = s.repo.SaveDelivery(context.Background(), alertdomain.Delivery{ID: stableID(event.ID, channel.ID, fmt.Sprint(now.UnixNano())), EventID: event.ID, RuleName: event.RuleName, Severity: event.Severity, MachineID: event.MachineID, ChannelID: channel.ID, ChannelName: channel.Name, ChannelType: channel.Type, Status: "failed", Error: err.Error(), DeliveredAt: now})
							s.runtime.deliveriesFailed.Add(1)
							allSucceeded, lastError = false, err.Error()
							s.channelMu.RUnlock()
							continue
						}
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
						if err := s.repo.UpdateChannelDeliveryStatus(persistCtx, channel.ID, channel.LastStatus, channel.LastError, channel.LastDeliveredAt, channel.UpdatedAt); err != nil {
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
						s.channelMu.RUnlock()
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

func (s *AlertService) channelByID(ctx context.Context, id string) (alertdomain.Channel, bool, error) {
	channels, err := s.repo.ListChannels(ctx)
	if err != nil {
		return alertdomain.Channel{}, false, err
	}
	for _, channel := range channels {
		if channel.ID == id {
			return channel, true, nil
		}
	}
	return alertdomain.Channel{}, false, nil
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
		switch strings.TrimSpace(c.Config["port"]) {
		case "465", "587":
		default:
			return alertdomain.Invalid("SMTP 端口只能使用 465（SSL）或 587（STARTTLS）")
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

var channelRecipientRoles = map[string]bool{"oncall": true, "dba": true, "ops": true, "developer": true, "manager": true}
var platformRecipientRoles = map[string]bool{"admin": true, "operator": true, "dba": true, "auditor": true}
var channelContentCategories = map[string]bool{"host": true, "agent": true, "availability": true, "connection": true, "replication": true, "transaction": true, "performance": true, "storage": true, "fragmentation": true, "log": true}
var channelEventStates = map[string]bool{"firing": true, "resolved": true}

func normalizeChannelChoices(values []string, allowed map[string]bool, fallback []string) ([]string, error) {
	if len(values) == 0 {
		return append([]string(nil), fallback...), nil
	}
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if !allowed[value] {
			return nil, alertdomain.Invalid("unsupported channel routing value")
		}
		if !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	return out, nil
}

func normalizeStringIDs(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func channelMatchesEvent(channel alertdomain.Channel, event alertdomain.Event) bool {
	if !channel.Enabled || alertdomain.SeverityRank(event.Severity) < alertdomain.SeverityRank(channel.MinimumSeverity) {
		return false
	}
	if !channelChoiceMatches(channel.ContentFilter.EventStates, event.Status) {
		return false
	}
	return channelChoiceMatches(channel.ContentFilter.Categories, channelContentCategory(event))
}

func channelChoiceMatches(choices []string, value string) bool {
	if len(choices) == 0 {
		return true
	}
	for _, choice := range choices {
		if choice == value {
			return true
		}
	}
	return false
}

func channelContentCategory(event alertdomain.Event) string {
	metric := strings.ToLower(event.Metric)
	if strings.HasPrefix(metric, "agent_") {
		return "agent"
	}
	if !strings.HasPrefix(metric, "mysql_") {
		return "host"
	}
	if containsMetricFragment(metric, "connectivity", "process_alive", "port_listening", "probe", "socket_ok") {
		return "availability"
	}
	if containsMetricFragment(metric, "replica", "replication", "gtid", "semisync", "master_role") {
		return "replication"
	}
	if containsMetricFragment(metric, "connection", "threads_connected", "long_sleep", "active_connections") {
		return "connection"
	}
	if containsMetricFragment(metric, "longest_transaction", "lock_wait", "metadata_lock", "blocking", "history_list", "purge_backlog", "max_blocking") {
		return "transaction"
	}
	if containsMetricFragment(metric, "fragment", "tablespace_fragment", "max_table_fragment") {
		return "fragmentation"
	}
	if containsMetricFragment(metric, "disk_full", "oom", "table_corruption", "crash_recovery", "permission_failed", "recent_error", "error_log", "slow_query_log_enabled") {
		return "log"
	}
	if containsMetricFragment(metric, "data_disk", "binlog_disk", "redo_disk", "tmp_disk_usage", "undo_disk", "buffer_pool", "open_files", "table_cache") {
		return "storage"
	}
	return "performance"
}

func containsMetricFragment(value string, fragments ...string) bool {
	for _, fragment := range fragments {
		if strings.Contains(value, fragment) {
			return true
		}
	}
	return false
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
	var err error
	channel.RecipientIDs = normalizeStringIDs(channel.RecipientIDs)
	if channel, err = s.bindEmailRecipients(ctx, channel, true); err != nil {
		return err
	}
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
		subject, plainBody, htmlBody := renderAlertEmail(e)
		return sendAlertEmail(ctx, c.Config, subject, plainBody, htmlBody)
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
func sendAlertEmail(ctx context.Context, cfg map[string]string, subject, plainBody, htmlBody string) error {
	host := strings.TrimSpace(cfg["host"])
	port := strings.TrimSpace(cfg["port"])
	from := strings.TrimSpace(cfg["from"])
	to := make([]string, 0)
	for _, recipient := range strings.Split(cfg["to"], ",") {
		if recipient = strings.TrimSpace(recipient); recipient != "" {
			to = append(to, recipient)
		}
	}
	if host == "" || from == "" || len(to) == 0 {
		return errors.New("email host, from and to are required")
	}
	fromAddress, recipients, msg, err := buildAlertEmail(from, to, subject, plainBody, htmlBody)
	if err != nil {
		return err
	}
	addr := net.JoinHostPort(host, port)
	auth := smtp.PlainAuth("", cfg["username"], cfg["password"], host)
	dialer := &net.Dialer{Timeout: 8 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("无法连接 SMTP 服务器 %s，请检查地址、端口和网络: %w", addr, err)
	}
	_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
	if port == "465" {
		tlsConn := tls.Client(conn, &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12})
		if err = tlsConn.HandshakeContext(ctx); err != nil {
			_ = conn.Close()
			return fmt.Errorf("SMTP SSL 握手失败；465 端口必须使用 SSL，请确认端口填写正确: %w", err)
		}
		conn = tlsConn
	}
	client, err := smtp.NewClient(conn, host)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("SMTP 服务器未返回有效欢迎信息；请确认端口与加密方式匹配（465 使用 SSL，587 使用 STARTTLS）: %w", err)
	}
	defer client.Close()
	if port == "587" {
		ok, _ := client.Extension("STARTTLS")
		if !ok {
			return errors.New("SMTP 服务器不支持 STARTTLS；587 端口必须使用 STARTTLS")
		}
		if err = client.StartTLS(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}); err != nil {
			return fmt.Errorf("SMTP STARTTLS 握手失败；请确认 587 端口可用: %w", err)
		}
	}
	if cfg["username"] != "" {
		if err = client.Auth(auth); err != nil {
			return fmt.Errorf("SMTP 登录失败，请检查发件邮箱和授权码: %w", err)
		}
	}
	if err = client.Mail(fromAddress); err != nil {
		return fmt.Errorf("SMTP 服务器拒绝发件地址 %s: %w", fromAddress, err)
	}
	for _, x := range recipients {
		if err = client.Rcpt(x); err != nil {
			return fmt.Errorf("SMTP 服务器拒绝收件地址 %s: %w", x, err)
		}
	}
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("SMTP 服务器拒绝邮件内容: %w", err)
	}
	if _, err = w.Write(msg); err != nil {
		return fmt.Errorf("写入 SMTP 邮件内容失败: %w", err)
	}
	if err = w.Close(); err != nil {
		return fmt.Errorf("SMTP 邮件发送失败: %w", err)
	}
	return nil
}

func buildAlertEmail(from string, to []string, subject, plainBody, htmlBody string) (string, []string, []byte, error) {
	fromAddress, err := mail.ParseAddress(strings.TrimSpace(from))
	if err != nil {
		return "", nil, nil, fmt.Errorf("发件邮箱地址格式无效: %w", err)
	}
	recipients := make([]string, 0, len(to))
	toHeaders := make([]string, 0, len(to))
	for _, raw := range to {
		address, parseErr := mail.ParseAddress(strings.TrimSpace(raw))
		if parseErr != nil {
			return "", nil, nil, fmt.Errorf("收件邮箱地址 %s 格式无效: %w", raw, parseErr)
		}
		recipients = append(recipients, address.Address)
		toHeaders = append(toHeaders, address.String())
	}
	var alternative bytes.Buffer
	writer := multipart.NewWriter(&alternative)
	plainHeaders := textproto.MIMEHeader{}
	plainHeaders.Set("Content-Type", "text/plain; charset=UTF-8")
	plainHeaders.Set("Content-Transfer-Encoding", "quoted-printable")
	plainPart, err := writer.CreatePart(plainHeaders)
	if err != nil {
		return "", nil, nil, err
	}
	if err = writeAlertEmailPart(plainPart, plainBody); err != nil {
		return "", nil, nil, err
	}
	htmlHeaders := textproto.MIMEHeader{}
	htmlHeaders.Set("Content-Type", "text/html; charset=UTF-8")
	htmlHeaders.Set("Content-Transfer-Encoding", "quoted-printable")
	htmlPart, err := writer.CreatePart(htmlHeaders)
	if err != nil {
		return "", nil, nil, err
	}
	if err = writeAlertEmailPart(htmlPart, htmlBody); err != nil {
		return "", nil, nil, err
	}
	if err = writer.Close(); err != nil {
		return "", nil, nil, err
	}
	headers := []string{
		"From: " + fromAddress.String(),
		"To: " + strings.Join(toHeaders, ", "),
		"Date: " + time.Now().Format(time.RFC1123Z),
		"Subject: " + mime.QEncoding.Encode("UTF-8", safeMailHeader(subject)),
		"MIME-Version: 1.0",
		fmt.Sprintf("Content-Type: multipart/alternative; boundary=%q", writer.Boundary()),
	}
	message := strings.Join(headers, "\r\n") + "\r\n\r\n" + alternative.String()
	return fromAddress.Address, recipients, []byte(message), nil
}

func writeAlertEmailPart(part io.Writer, body string) error {
	body = strings.ReplaceAll(body, "\r\n", "\n")
	body = strings.ReplaceAll(body, "\n", "\r\n")
	encoded := quotedprintable.NewWriter(part)
	if _, err := io.WriteString(encoded, body); err != nil {
		_ = encoded.Close()
		return err
	}
	return encoded.Close()
}

func renderAlertEmail(event alertdomain.Event) (string, string, string) {
	severityLabel, severityColor, severityBackground := alertEmailSeverity(event.Severity)
	statusLabel, statusColor, statusBackground := alertEmailStatus(event.Status)
	ruleName := strings.TrimSpace(event.RuleName)
	if ruleName == "" {
		ruleName = "未命名告警"
	}
	context := alertEmailEventContext(event)
	subject := fmt.Sprintf("[GMHA][%s][%s] %s", severityLabel, statusLabel, ruleName)
	if context.InstanceName != "" {
		subject += " - " + context.InstanceName
	}
	currentValue := strconv.FormatFloat(event.Value, 'f', -1, 64)
	threshold := strconv.FormatFloat(event.Threshold, 'f', -1, 64)
	plainRows := []string{
		"GMHA 告警通知",
		"",
		fmt.Sprintf("告警：%s", ruleName),
		fmt.Sprintf("等级：%s", severityLabel),
		fmt.Sprintf("状态：%s", statusLabel),
		fmt.Sprintf("当前值：%s", currentValue),
		fmt.Sprintf("触发条件：%s %s %s", currentValue, valueOrDash(event.Operator), threshold),
		"",
		"定位信息：",
		"实例名称：" + valueOrDash(context.InstanceName),
		"所属集群：" + valueOrDash(context.ClusterName),
		"实例地址：" + valueOrDash(context.InstanceEndpoint),
		"实例角色：" + valueOrDash(context.InstanceRole),
		"架构类型：" + valueOrDash(context.Architecture),
		"实例关系：" + valueOrDash(context.Relation),
	}
	if context.SourceInstance != "" {
		plainRows = append(plainRows, "上游实例："+context.SourceInstance)
	}
	plainRows = append(plainRows,
		"机器名称："+valueOrDash(context.MachineName),
		"机器 IP："+valueOrDash(context.MachineIP),
		"监控指标："+valueOrDash(event.Metric),
		"",
		"事件信息：",
		"首次出现："+formatAlertEmailTime(event.FirstSeenAt),
		"最近出现："+formatAlertEmailTime(event.LastSeenAt),
		fmt.Sprintf("出现次数：%d", event.OccurrenceCount),
		"事件编号："+valueOrDash(event.ID),
	)
	additionalLabelKeys := sortedAlertAdditionalLabelKeys(event.Labels)
	if len(additionalLabelKeys) > 0 {
		plainRows = append(plainRows, "", "附加标签：")
		for _, key := range additionalLabelKeys {
			plainRows = append(plainRows, fmt.Sprintf("- %s: %s", key, event.Labels[key]))
		}
	}

	labelsHTML := ""
	if len(additionalLabelKeys) > 0 {
		var labels strings.Builder
		labels.WriteString(`<tr><td style="padding:0 32px 28px"><div style="font-size:12px;font-weight:700;color:#52667d;margin-bottom:10px">附加标签</div><div>`)
		for _, key := range additionalLabelKeys {
			labels.WriteString(`<span style="display:inline-block;margin:0 6px 6px 0;padding:5px 8px;border:1px solid #dbe4ee;border-radius:5px;background:#f7f9fc;color:#53677d;font-size:11px">`)
			labels.WriteString(html.EscapeString(key + ": " + event.Labels[key]))
			labels.WriteString(`</span>`)
		}
		labels.WriteString(`</div></td></tr>`)
		labelsHTML = labels.String()
	}
	locationRows := alertEmailTableRow("实例名称", context.InstanceName) +
		alertEmailTableRow("所属集群", context.ClusterName) +
		alertEmailTableRow("实例地址", context.InstanceEndpoint) +
		alertEmailTableRow("实例角色", context.InstanceRole) +
		alertEmailTableRow("架构类型", context.Architecture) +
		alertEmailTableRow("实例关系", context.Relation)
	if context.SourceInstance != "" {
		locationRows += alertEmailTableRow("上游实例", context.SourceInstance)
	}
	locationRows += alertEmailTableRow("机器名称", context.MachineName) + alertEmailTableRow("机器 IP", context.MachineIP)
	eventRows := alertEmailTableRow("监控指标", event.Metric) +
		alertEmailTableRow("首次出现", formatAlertEmailTime(event.FirstSeenAt)) +
		alertEmailTableRow("最近出现", formatAlertEmailTime(event.LastSeenAt)) +
		alertEmailTableRow("出现次数", strconv.Itoa(event.OccurrenceCount)) +
		alertEmailTableRow("通知次数", strconv.Itoa(event.NotificationCount))
	htmlBody := fmt.Sprintf(`<!doctype html>
<html><head><meta charset="UTF-8"><meta name="viewport" content="width=device-width,initial-scale=1"></head>
<body style="margin:0;padding:0;background:#eef3f8;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI','PingFang SC','Microsoft YaHei',Arial,sans-serif;color:#21344a">
<table role="presentation" width="100%%" cellspacing="0" cellpadding="0" style="background:#eef3f8"><tr><td align="center" style="padding:28px 14px">
<table role="presentation" width="640" cellspacing="0" cellpadding="0" style="width:100%%;max-width:640px;border:1px solid #dce5ef;border-radius:12px;background:#ffffff;overflow:hidden;box-shadow:0 10px 30px rgba(37,58,82,.08)">
<tr><td style="padding:18px 28px;border-bottom:1px solid #e8edf3;background:#f8fafc"><table role="presentation" width="100%%"><tr><td style="font-size:15px;font-weight:750;color:#1e3854">GMHA 告警通知</td><td align="right" style="font-size:11px;color:#7d8da0">%s</td></tr></table></td></tr>
<tr><td style="padding:30px 32px 22px"><span style="display:inline-block;margin-right:7px;padding:5px 9px;border-radius:5px;background:%s;color:%s;font-size:11px;font-weight:750">%s</span><span style="display:inline-block;padding:5px 9px;border-radius:5px;background:%s;color:%s;font-size:11px;font-weight:750">%s</span><h1 style="margin:16px 0 8px;font-size:23px;line-height:1.4;color:#1d334b">%s</h1><p style="margin:0;color:#748599;font-size:12px">事件编号：%s</p></td></tr>
<tr><td style="padding:0 32px 24px"><table role="presentation" width="100%%" cellspacing="0" cellpadding="0" style="border-radius:9px;background:%s"><tr><td style="padding:18px 20px"><div style="margin-bottom:6px;color:%s;font-size:11px;font-weight:700">当前值 / 触发条件</div><div style="color:#1f3349;font-size:22px;font-weight:750">%s <span style="color:#8190a1;font-size:14px;font-weight:600">%s %s</span></div></td></tr></table></td></tr>
<tr><td style="padding:0 32px 8px"><div style="font-size:12px;font-weight:750;color:#324b66;margin-bottom:8px">实例与拓扑</div><table role="presentation" width="100%%" cellspacing="0" cellpadding="0" style="border-top:1px solid #e7edf3">%s</table></td></tr>
<tr><td style="padding:20px 32px 28px"><div style="font-size:12px;font-weight:750;color:#324b66;margin-bottom:8px">事件信息</div><table role="presentation" width="100%%" cellspacing="0" cellpadding="0" style="border-top:1px solid #e7edf3">%s</table></td></tr>
%s
<tr><td style="padding:17px 28px;border-top:1px solid #e8edf3;background:#f8fafc;color:#7b8a9b;font-size:10px;line-height:1.6">此邮件由 GMHA 告警系统自动发送。请登录管理平台查看告警详情与处置记录。</td></tr>
</table></td></tr></table></body></html>`,
		formatAlertEmailTime(event.LastSeenAt),
		severityBackground, severityColor, html.EscapeString(severityLabel),
		statusBackground, statusColor, html.EscapeString(statusLabel),
		html.EscapeString(ruleName), html.EscapeString(valueOrDash(event.ID)),
		severityBackground, severityColor, html.EscapeString(currentValue), html.EscapeString(valueOrDash(event.Operator)), html.EscapeString(threshold),
		locationRows, eventRows,
		labelsHTML,
	)
	return subject, strings.Join(plainRows, "\n"), htmlBody
}

type alertEmailContext struct {
	InstanceName     string
	ClusterName      string
	InstanceEndpoint string
	InstanceRole     string
	Architecture     string
	Relation         string
	SourceInstance   string
	MachineName      string
	MachineIP        string
}

func alertEmailEventContext(event alertdomain.Event) alertEmailContext {
	labels := event.Labels
	context := alertEmailContext{
		InstanceName:     strings.TrimSpace(labels["instance_name"]),
		ClusterName:      strings.TrimSpace(labels["cluster_name"]),
		InstanceEndpoint: strings.TrimSpace(labels["instance_endpoint"]),
		InstanceRole:     strings.TrimSpace(labels["instance_role"]),
		Architecture:     strings.TrimSpace(labels["topology_architecture_label"]),
		Relation:         strings.TrimSpace(labels["instance_relation"]),
		MachineName:      strings.TrimSpace(labels["machine_name"]),
		MachineIP:        strings.TrimSpace(labels["machine_ip"]),
	}
	if context.ClusterName == "" {
		context.ClusterName = strings.TrimSpace(event.ClusterID)
	}
	if context.MachineName == "" {
		context.MachineName = strings.TrimSpace(event.MachineID)
	}
	if context.InstanceEndpoint == "" {
		context.InstanceEndpoint = strings.TrimSpace(labels["mysql_endpoint"])
	}
	if context.InstanceName == "" && strings.TrimSpace(labels["mysql_port"]) != "" {
		context.InstanceName = context.MachineName + ":" + strings.TrimSpace(labels["mysql_port"])
	}
	if context.InstanceRole == "" {
		context.InstanceRole = strings.TrimSpace(labels["observed_instance_role"])
	}
	if context.InstanceEndpoint == "" && context.MachineIP != "" && strings.TrimSpace(labels["mysql_port"]) != "" {
		context.InstanceEndpoint = context.MachineIP + ":" + strings.TrimSpace(labels["mysql_port"])
	}
	sourceName := strings.TrimSpace(labels["source_instance_name"])
	sourceEndpoint := strings.TrimSpace(labels["source_instance_endpoint"])
	context.SourceInstance = sourceName
	if sourceEndpoint != "" && sourceEndpoint != sourceName {
		if context.SourceInstance != "" {
			context.SourceInstance += "（" + sourceEndpoint + "）"
		} else {
			context.SourceInstance = sourceEndpoint
		}
	}
	return context
}

func alertEmailSeverity(severity alertdomain.Severity) (string, string, string) {
	switch severity {
	case alertdomain.SeverityFatal:
		return "致命", "#8f1d2c", "#fdecef"
	case alertdomain.SeverityCritical:
		return "严重", "#bf293f", "#fff0f2"
	case alertdomain.SeverityWarning:
		return "警告", "#a96409", "#fff7e6"
	default:
		return "通知", "#176dcc", "#edf5ff"
	}
}

func alertEmailStatus(status string) (string, string, string) {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "resolved":
		return "已恢复", "#177a52", "#ebf8f2"
	case "firing":
		return "告警中", "#bf293f", "#fff0f2"
	default:
		return valueOrDash(status), "#53677d", "#f1f4f7"
	}
}

func alertEmailTableRow(label, value string) string {
	return fmt.Sprintf(`<tr><td style="width:104px;padding:10px 0;border-bottom:1px solid #edf1f5;color:#7b8b9d;font-size:11px">%s</td><td style="padding:10px 0;border-bottom:1px solid #edf1f5;color:#2d4259;font-size:12px;font-weight:600;word-break:break-all">%s</td></tr>`, html.EscapeString(label), html.EscapeString(valueOrDash(value)))
}

func sortedAlertLabelKeys(labels map[string]string) []string {
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedAlertAdditionalLabelKeys(labels map[string]string) []string {
	keys := make([]string, 0, len(labels))
	for key, value := range labels {
		if strings.TrimSpace(value) == "" || alertCoreContextLabel(key) {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func alertCoreContextLabel(key string) bool {
	switch key {
	case "display_name", "metric_scope", "machine_name", "machine_ip", "cluster_name", "alert_category", "resolution_reason",
		"mysql_host", "mysql_endpoint", "mysql_instance", "mysql_port",
		"instance_name", "instance_ip", "instance_port", "instance_endpoint", "instance_role", "instance_role_code", "observed_instance_role",
		"topology_architecture", "topology_architecture_label", "instance_relation",
		"source_machine_id", "source_instance_name", "source_instance_ip", "source_instance_port", "source_instance_endpoint":
		return true
	default:
		return false
	}
}

func formatAlertEmailTime(value time.Time) string {
	if value.IsZero() {
		return "—"
	}
	return value.Local().Format("2006-01-02 15:04:05 MST")
}

func valueOrDash(value string) string {
	if value = strings.TrimSpace(value); value != "" {
		return value
	}
	return "—"
}

func safeMailHeader(value string) string {
	return strings.Map(func(r rune) rune {
		if r == '\r' || r == '\n' {
			return -1
		}
		return r
	}, value)
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
	if alertCoreContextLabel(key) && key != "mysql_port" {
		return false
	}
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
