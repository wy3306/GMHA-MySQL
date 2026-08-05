package app

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	alertdomain "gmha/internal/domain/alert"
	dynamicdomain "gmha/internal/domain/dynamic"
	hbdomain "gmha/internal/domain/heartbeat"
)

type alertMemoryRepo struct {
	mu                    sync.Mutex
	rules                 []alertdomain.Rule
	events                map[string]alertdomain.Event
	states                map[string]alertdomain.EvaluationState
	channels              []alertdomain.Channel
	roles                 []alertdomain.NotificationRole
	recipients            []alertdomain.NotificationRecipient
	filters               []alertdomain.Filter
	restartClassification alertdomain.MySQLRestartClassification
}

func newAlertMemoryRepo() *alertMemoryRepo {
	return &alertMemoryRepo{events: map[string]alertdomain.Event{}, states: map[string]alertdomain.EvaluationState{}}
}
func (r *alertMemoryRepo) ListRules(context.Context) ([]alertdomain.Rule, error) { return r.rules, nil }
func (r *alertMemoryRepo) SaveRule(_ context.Context, x alertdomain.Rule) error {
	r.rules = append(r.rules, x)
	return nil
}
func (r *alertMemoryRepo) DeleteRule(context.Context, string) error { return nil }
func (r *alertMemoryRepo) ListFilters(context.Context) ([]alertdomain.Filter, error) {
	return r.filters, nil
}
func (r *alertMemoryRepo) SaveFilter(_ context.Context, x alertdomain.Filter) error {
	r.filters = append(r.filters, x)
	return nil
}
func (r *alertMemoryRepo) DeleteFilter(context.Context, string) error { return nil }
func (r *alertMemoryRepo) ListEvents(context.Context, alertdomain.EventFilter) ([]alertdomain.Event, error) {
	out := []alertdomain.Event{}
	for _, x := range r.events {
		out = append(out, x)
	}
	return out, nil
}
func (r *alertMemoryRepo) GetActiveEvent(_ context.Context, fp string) (alertdomain.Event, bool, error) {
	for _, x := range r.events {
		if x.Fingerprint == fp && x.Status == "firing" {
			return x, true, nil
		}
	}
	return alertdomain.Event{}, false, nil
}
func (r *alertMemoryRepo) ListActiveEventsForRuleTarget(_ context.Context, ruleID, machineID string) ([]alertdomain.Event, error) {
	out := make([]alertdomain.Event, 0)
	for _, x := range r.events {
		if x.RuleID == ruleID && x.MachineID == machineID && x.Status == "firing" {
			out = append(out, x)
		}
	}
	return out, nil
}
func (r *alertMemoryRepo) SaveEvent(_ context.Context, x alertdomain.Event) error {
	r.events[x.ID] = x
	return nil
}
func (r *alertMemoryRepo) UpdateEventAction(context.Context, string, string, string, *time.Time) error {
	return nil
}
func (r *alertMemoryRepo) UpdateAutomationState(context.Context, string, string, string) error {
	return nil
}
func (r *alertMemoryRepo) GetEvaluationState(_ context.Context, fp string) (alertdomain.EvaluationState, bool, error) {
	x, ok := r.states[fp]
	return x, ok, nil
}
func (r *alertMemoryRepo) SaveEvaluationState(_ context.Context, x alertdomain.EvaluationState) error {
	r.states[x.Fingerprint] = x
	return nil
}
func (r *alertMemoryRepo) ClassifyMySQLRestart(context.Context, string, int, time.Time, time.Time) (alertdomain.MySQLRestartClassification, error) {
	return r.restartClassification, nil
}
func (r *alertMemoryRepo) ListChannels(context.Context) ([]alertdomain.Channel, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]alertdomain.Channel(nil), r.channels...), nil
}
func (r *alertMemoryRepo) SaveChannel(_ context.Context, channel alertdomain.Channel) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.channels {
		if r.channels[i].ID == channel.ID {
			r.channels[i] = channel
			return nil
		}
	}
	r.channels = append(r.channels, channel)
	return nil
}
func (r *alertMemoryRepo) UpdateChannelDeliveryStatus(_ context.Context, id, status, lastError string, lastDeliveredAt *time.Time, updatedAt time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.channels {
		if r.channels[i].ID == id {
			r.channels[i].LastStatus = status
			r.channels[i].LastError = lastError
			r.channels[i].LastDeliveredAt = lastDeliveredAt
			r.channels[i].UpdatedAt = updatedAt
			return nil
		}
	}
	return alertdomain.ErrNotFound
}
func (r *alertMemoryRepo) DeleteChannel(context.Context, string) error { return nil }
func (r *alertMemoryRepo) ListNotificationRoles(context.Context) ([]alertdomain.NotificationRole, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]alertdomain.NotificationRole(nil), r.roles...), nil
}
func (r *alertMemoryRepo) SaveNotificationRole(_ context.Context, x alertdomain.NotificationRole) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.roles {
		if r.roles[i].ID == x.ID {
			r.roles[i] = x
			return nil
		}
	}
	r.roles = append(r.roles, x)
	return nil
}
func (r *alertMemoryRepo) DeleteNotificationRole(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.roles {
		if r.roles[i].ID == id {
			r.roles = append(r.roles[:i], r.roles[i+1:]...)
			return nil
		}
	}
	return alertdomain.ErrNotFound
}
func (r *alertMemoryRepo) ListNotificationRecipients(context.Context) ([]alertdomain.NotificationRecipient, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]alertdomain.NotificationRecipient(nil), r.recipients...), nil
}
func (r *alertMemoryRepo) SaveNotificationRecipient(_ context.Context, x alertdomain.NotificationRecipient) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.recipients {
		if r.recipients[i].ID == x.ID {
			r.recipients[i] = x
			return nil
		}
	}
	r.recipients = append(r.recipients, x)
	return nil
}
func (r *alertMemoryRepo) DeleteNotificationRecipient(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.recipients {
		if r.recipients[i].ID == id {
			r.recipients = append(r.recipients[:i], r.recipients[i+1:]...)
			return nil
		}
	}
	return alertdomain.ErrNotFound
}
func (r *alertMemoryRepo) ListDeliveries(context.Context, int) ([]alertdomain.Delivery, error) {
	return nil, nil
}
func (r *alertMemoryRepo) SaveDelivery(context.Context, alertdomain.Delivery) error { return nil }
func (r *alertMemoryRepo) LoadMetricConfig(context.Context, string) (dynamicdomain.DynamicCollectConfig, bool, error) {
	return dynamicdomain.DynamicCollectConfig{}, false, nil
}
func (r *alertMemoryRepo) SaveMetricConfig(context.Context, string, dynamicdomain.DynamicCollectConfig) error {
	return nil
}

func TestDefaultAlertRulesCoverEveryOperationalCategory(t *testing.T) {
	rules := defaultAlertRules()
	if len(rules) < 50 {
		t.Fatalf("expected a broad built-in rule set, got %d rules", len(rules))
	}

	catalog := map[string]bool{}
	for _, metric := range dynamicdomain.BuildPerformanceMetricCatalog() {
		if metric.Available {
			catalog[metric.Name] = true
		}
	}
	for _, synthetic := range []string{"agent_heartbeat_alive", "agent_overall_health", "agent_health_check_failed"} {
		catalog[synthetic] = true
	}

	required := map[string][]string{
		"host":          {"cpu_usage_percent", "mem_usage_percent", "host_filesystem_used_percent", "ntp_offset_abs_ms"},
		"agent":         {"agent_heartbeat_alive", "agent_overall_health"},
		"availability":  {"mysql_connectivity", "mysql_process_alive", "mysql_port_listening"},
		"connection":    {"mysql_connection_usage_percent", "mysql_long_sleep_connections"},
		"replication":   {"mysql_replication_lag", "mysql_replica_io_thread", "mysql_replica_sql_thread", "mysql_replication_error_count"},
		"transaction":   {"mysql_longest_transaction_seconds", "mysql_lock_wait_sessions", "mysql_metadata_lock_waits"},
		"performance":   {"mysql_tmp_disk_table_ratio", "mysql_table_scan_ratio", "mysql_slowest_sql_seconds"},
		"storage":       {"mysql_data_disk_usage", "mysql_binlog_disk_usage", "mysql_redo_disk_usage"},
		"fragmentation": {"mysql_fragmented_table_count", "mysql_max_table_fragment_percent", "mysql_tablespace_fragment_total_bytes"},
		"log":           {"mysql_slow_query_log_enabled", "mysql_recent_error_count", "mysql_oom_keyword_count", "mysql_table_corruption_keyword_count"},
	}
	wanted := map[string]string{}
	for category, metrics := range required {
		for _, metric := range metrics {
			wanted[metric] = category
		}
	}

	seen := map[string]alertdomain.Rule{}
	validationRepo := newAlertMemoryRepo()
	service := &AlertService{repo: validationRepo}
	for _, rule := range rules {
		if _, duplicate := seen[rule.Metric]; duplicate {
			t.Fatalf("duplicate built-in rule metric: %s", rule.Metric)
		}
		if !catalog[rule.Metric] {
			t.Fatalf("built-in rule references unavailable metric: %s", rule.Metric)
		}
		if _, err := service.SaveRule(context.Background(), rule); err != nil {
			t.Fatalf("built-in rule %s is invalid: %v", rule.Metric, err)
		}
		seen[rule.Metric] = rule
	}
	for metric, category := range wanted {
		if _, ok := seen[metric]; !ok {
			t.Errorf("%s category missing required metric %s", category, metric)
		}
	}
}

func TestEnsureDefaultsAddsMissingRulesWithoutDuplicatingExistingOnes(t *testing.T) {
	repo := newAlertMemoryRepo()
	service := &AlertService{repo: repo}
	if err := service.EnsureDefaults(context.Background()); err != nil {
		t.Fatalf("EnsureDefaults() failed: %v", err)
	}
	want := len(defaultAlertRules())
	if len(repo.rules) != want {
		t.Fatalf("EnsureDefaults() stored %d rules, want %d", len(repo.rules), want)
	}
	for _, rule := range repo.rules {
		if rule.ID != stableID("default", rule.Metric) {
			t.Fatalf("rule %s has unstable default id %s", rule.Metric, rule.ID)
		}
	}
	if err := service.EnsureDefaults(context.Background()); err != nil {
		t.Fatalf("second EnsureDefaults() failed: %v", err)
	}
	if len(repo.rules) != want {
		t.Fatalf("second EnsureDefaults() duplicated rules: got %d, want %d", len(repo.rules), want)
	}
}

func TestAlertEvaluationConsecutiveSuppressAndResolve(t *testing.T) {
	repo := newAlertMemoryRepo()
	repo.rules = []alertdomain.Rule{{ID: "r1", Name: "CPU high", Metric: "cpu", Enabled: true, Operator: ">=", Threshold: 80, Severity: alertdomain.SeverityCritical, ConsecutiveCount: 2, RepeatIntervalSeconds: 3600, MaxNotifications: 1}}
	service := NewAlertService(repo)
	payload := hbdomain.HeartbeatPayload{AgentID: "a1", MachineID: "m1", Metrics: []dynamicdomain.MetricResult{{Name: "cpu", Success: true, Value: 90, Labels: map[string]string{"metric_scope": "machine_dynamic"}}}}
	service.evaluatePayload(context.Background(), payload)
	if len(repo.events) != 0 {
		t.Fatal("must not fire before consecutive threshold")
	}
	service.evaluatePayload(context.Background(), payload)
	if len(repo.events) != 1 {
		t.Fatalf("expected one event, got %d", len(repo.events))
	}
	var event alertdomain.Event
	for _, event = range repo.events {
	}
	if event.NotificationCount != 1 || event.OccurrenceCount != 1 {
		t.Fatalf("unexpected first event: %+v", event)
	}
	service.evaluatePayload(context.Background(), payload)
	for _, event = range repo.events {
	}
	if event.NotificationCount != 1 || event.OccurrenceCount != 2 {
		t.Fatalf("repeat suppression failed: %+v", event)
	}
	payload.Metrics[0].Value = 10
	service.evaluatePayload(context.Background(), payload)
	for _, event = range repo.events {
	}
	if event.Status != "resolved" || event.ResolvedAt == nil {
		t.Fatalf("event should resolve: %+v", event)
	}
}

func TestAlertRecoveryReconcilesChangedCollectorMetadata(t *testing.T) {
	repo := newAlertMemoryRepo()
	repo.rules = []alertdomain.Rule{{
		ID: "mysql-process", Name: "MySQL process stopped", Metric: "mysql_process_alive",
		Enabled: true, Operator: "==", Threshold: 0, Severity: alertdomain.SeverityFatal,
		ConsecutiveCount: 1, RepeatIntervalSeconds: 300,
	}}
	service := NewAlertService(repo)
	sampledAt := time.Now().UTC()
	payload := hbdomain.HeartbeatPayload{
		MachineID: "db-02",
		Metrics: []dynamicdomain.MetricResult{{
			Name: "mysql_process_alive", Success: true, Value: false, CollectedAt: sampledAt,
			Labels: map[string]string{
				"display_name": "MySQL进程状态", "metric_scope": "mysql_dynamic",
				"mysql_host": "127.0.0.1", "mysql_port": "3306",
				"mysql_instance": "port:3306", "mysql_endpoint": "127.0.0.1:3306",
			},
		}},
	}
	service.evaluatePayload(context.Background(), payload)
	if len(repo.events) != 1 {
		t.Fatalf("expected one firing event, got %+v", repo.events)
	}

	payload.Metrics[0].Value = true
	payload.Metrics[0].CollectedAt = sampledAt.Add(time.Second)
	payload.Metrics[0].Labels["display_name"] = "MySQL 进程"
	payload.Metrics[0].Labels["mysql_instance"] = "socket:/data/3306/data/mysql.sock"
	service.evaluatePayload(context.Background(), payload)

	for _, event := range repo.events {
		if event.Status != "resolved" || event.ResolvedAt == nil {
			t.Fatalf("healthy sample with changed metadata must resolve the old event: %+v", event)
		}
		if event.Labels["resolution_reason"] != "condition_cleared" {
			t.Fatalf("resolution reason was not recorded: %+v", event.Labels)
		}
	}
}

func TestAlertRecoveryOnlyResolvesMatchingMySQLPort(t *testing.T) {
	repo := newAlertMemoryRepo()
	repo.rules = []alertdomain.Rule{{
		ID: "mysql-process", Name: "MySQL process stopped", Metric: "mysql_process_alive",
		Enabled: true, Operator: "==", Threshold: 0, Severity: alertdomain.SeverityFatal,
		ConsecutiveCount: 1, RepeatIntervalSeconds: 300,
	}}
	service := NewAlertService(repo)
	base := time.Now().UTC()
	for index, port := range []string{"3306", "3307"} {
		service.evaluatePayload(context.Background(), hbdomain.HeartbeatPayload{
			MachineID: "db-02", Metrics: []dynamicdomain.MetricResult{{
				Name: "mysql_process_alive", Success: true, Value: false,
				CollectedAt: base.Add(time.Duration(index) * time.Second),
				Labels:      map[string]string{"mysql_port": port, "mysql_instance": "port:" + port},
			}},
		})
	}
	service.evaluatePayload(context.Background(), hbdomain.HeartbeatPayload{
		MachineID: "db-02", Metrics: []dynamicdomain.MetricResult{{
			Name: "mysql_process_alive", Success: true, Value: true,
			CollectedAt: base.Add(2 * time.Second),
			Labels:      map[string]string{"mysql_port": "3306", "mysql_instance": "socket:/data/3306/mysql.sock"},
		}},
	})
	firing, resolved := 0, 0
	for _, event := range repo.events {
		if event.Status == "firing" {
			firing++
			if event.Labels["mysql_port"] != "3307" {
				t.Fatalf("wrong MySQL instance remained active: %+v", event)
			}
		} else if event.Status == "resolved" {
			resolved++
		}
	}
	if firing != 1 || resolved != 1 {
		t.Fatalf("expected one active and one historical event, got firing=%d resolved=%d events=%+v", firing, resolved, repo.events)
	}
}

func TestAlertRuleSelectsHighestMatchingSeverity(t *testing.T) {
	repo := newAlertMemoryRepo()
	repo.rules = []alertdomain.Rule{{ID: "r-levels", Name: "CPU", Metric: "cpu", Enabled: true, Operator: ">=", Thresholds: []alertdomain.ThresholdLevel{{Severity: alertdomain.SeverityWarning, Threshold: 70, Enabled: true}, {Severity: alertdomain.SeverityCritical, Threshold: 85, Enabled: true}, {Severity: alertdomain.SeverityFatal, Threshold: 95, Enabled: true}}, ConsecutiveCount: 1, RepeatIntervalSeconds: 300}}
	service := NewAlertService(repo)
	payload := hbdomain.HeartbeatPayload{AgentID: "a1", MachineID: "m1", Metrics: []dynamicdomain.MetricResult{{Name: "cpu", Category: "host", Success: true, Value: 96}}}
	service.evaluatePayload(context.Background(), payload)
	for _, event := range repo.events {
		if event.Severity != alertdomain.SeverityFatal || event.Threshold != 95 {
			t.Fatalf("unexpected selected level: %+v", event)
		}
	}
}

func TestAlertRuleRejectsAmbiguousThresholdOrdering(t *testing.T) {
	service := NewAlertService(newAlertMemoryRepo())
	_, err := service.SaveRule(context.Background(), alertdomain.Rule{
		Name: "CPU", Metric: "cpu", Operator: ">=",
		Thresholds: []alertdomain.ThresholdLevel{
			{Severity: alertdomain.SeverityWarning, Threshold: 90, Enabled: true},
			{Severity: alertdomain.SeverityCritical, Threshold: 80, Enabled: true},
		},
	})
	if err == nil {
		t.Fatal("higher severity must not accept a lower threshold for a >= rule")
	}
	_, err = service.SaveRule(context.Background(), alertdomain.Rule{
		Name: "State", Metric: "state", Operator: "!=",
		Thresholds: []alertdomain.ThresholdLevel{
			{Severity: alertdomain.SeverityWarning, Threshold: 0, Enabled: true},
			{Severity: alertdomain.SeverityCritical, Threshold: 1, Enabled: true},
		},
	})
	if err == nil {
		t.Fatal("!= rules with multiple enabled thresholds are ambiguous")
	}
}

func TestAlertChannelValidationRejectsInvalidEndpointAndPort(t *testing.T) {
	service := NewAlertService(newAlertMemoryRepo())
	_, err := service.SaveChannel(context.Background(), alertdomain.Channel{
		Name: "hook", Type: "webhook", Config: map[string]string{"url": "file:///tmp/hook"},
	})
	if err == nil {
		t.Fatal("webhooks must use an HTTP endpoint")
	}
	_, err = service.SaveChannel(context.Background(), alertdomain.Channel{
		Name: "zabbix", Type: "zabbix", Config: map[string]string{"host": "127.0.0.1", "port": "70000"},
	})
	if err == nil {
		t.Fatal("invalid target ports must be rejected")
	}
	_, err = service.SaveChannel(context.Background(), alertdomain.Channel{
		Name: "qq-email", Type: "email",
		Config: map[string]string{
			"host": "smtp.qq.com", "port": "265", "username": "sender@example.com",
			"password": "authorization-code", "from": "sender@example.com", "to": "recipient@example.com",
		},
	})
	if err == nil || !strings.Contains(err.Error(), "465") || !strings.Contains(err.Error(), "587") {
		t.Fatalf("mistyped SMTP port must explain the supported SSL ports: %v", err)
	}
}

func TestAlertChannelNormalizesRecipientRolesAndContentFilters(t *testing.T) {
	service := NewAlertService(newAlertMemoryRepo())
	channel, err := service.SaveChannel(context.Background(), alertdomain.Channel{
		Name: "DBA webhook", Type: "webhook", Enabled: true,
		MinimumSeverity: alertdomain.SeverityWarning,
		RecipientRoles:  []string{" DBA ", "dba", "oncall"},
		ContentFilter: alertdomain.ChannelContentFilter{
			Categories:  []string{"replication", "storage"},
			EventStates: []string{"firing", "resolved"},
		},
		Config: map[string]string{"url": "https://example.test/alerts"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(channel.RecipientRoles) != 2 || channel.RecipientRoles[0] != "dba" || channel.RecipientRoles[1] != "oncall" {
		t.Fatalf("unexpected normalized roles: %v", channel.RecipientRoles)
	}

	_, err = service.SaveChannel(context.Background(), alertdomain.Channel{
		Name: "invalid", Type: "webhook", MinimumSeverity: alertdomain.SeverityWarning,
		RecipientRoles: []string{"root"}, Config: map[string]string{"url": "https://example.test/alerts"},
	})
	if err == nil {
		t.Fatal("unsupported recipient roles must be rejected")
	}
}

func TestEmailChannelBindsRecipientsAndSkipsDisabledPeople(t *testing.T) {
	repo := newAlertMemoryRepo()
	service := NewAlertService(repo)
	role, err := service.SaveNotificationRole(context.Background(), alertdomain.NotificationRole{Name: "DBA", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	active, err := service.SaveNotificationRecipient(context.Background(), alertdomain.NotificationRecipient{Name: "张三", Email: "ZHANGSAN@example.com", RoleIDs: []string{role.ID}, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	disabled, err := service.SaveNotificationRecipient(context.Background(), alertdomain.NotificationRecipient{Name: "李四", Email: "lisi@example.com", RoleIDs: []string{role.ID}, Enabled: false})
	if err != nil {
		t.Fatal(err)
	}
	channel, err := service.SaveChannel(context.Background(), alertdomain.Channel{
		Name: "值班邮箱", Type: "email", Enabled: true, MinimumSeverity: alertdomain.SeverityWarning,
		RecipientIDs: []string{active.ID, disabled.ID},
		Config:       map[string]string{"host": "smtp.example.com", "port": "465", "username": "sender@example.com", "password": "secret", "from": "sender@example.com"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if channel.Config["to"] != "zhangsan@example.com,lisi@example.com" {
		t.Fatalf("saved channel should derive all selected addresses: %q", channel.Config["to"])
	}
	deliveryChannel, err := service.bindEmailRecipients(context.Background(), channel, true)
	if err != nil {
		t.Fatal(err)
	}
	if deliveryChannel.Config["to"] != "zhangsan@example.com" {
		t.Fatalf("disabled recipients must be skipped at delivery: %q", deliveryChannel.Config["to"])
	}
	if err := service.DeleteNotificationRecipient(context.Background(), active.ID); !errors.Is(err, alertdomain.ErrConflict) {
		t.Fatalf("recipient bound to a channel must be protected, got %v", err)
	}
}

func TestAlertChannelContentFilterMatchesCategoryAndLifecycle(t *testing.T) {
	channel := alertdomain.Channel{
		Enabled: true, MinimumSeverity: alertdomain.SeverityWarning,
		ContentFilter: alertdomain.ChannelContentFilter{
			Categories:  []string{"replication", "storage"},
			EventStates: []string{"firing"},
		},
	}
	tests := []struct {
		name  string
		event alertdomain.Event
		want  bool
	}{
		{name: "replication firing", event: alertdomain.Event{Metric: "mysql_replication_lag", Severity: alertdomain.SeverityCritical, Status: "firing"}, want: true},
		{name: "storage firing", event: alertdomain.Event{Metric: "mysql_data_disk_usage", Severity: alertdomain.SeverityWarning, Status: "firing"}, want: true},
		{name: "performance excluded", event: alertdomain.Event{Metric: "mysql_slowest_sql_seconds", Severity: alertdomain.SeverityCritical, Status: "firing"}, want: false},
		{name: "recovery excluded", event: alertdomain.Event{Metric: "mysql_replication_lag", Severity: alertdomain.SeverityCritical, Status: "resolved"}, want: false},
		{name: "below severity", event: alertdomain.Event{Metric: "mysql_replication_lag", Severity: alertdomain.SeverityNotice, Status: "firing"}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := channelMatchesEvent(channel, tt.event); got != tt.want {
				t.Fatalf("channelMatchesEvent() = %v, want %v", got, tt.want)
			}
		})
	}

	channel.ContentFilter = alertdomain.ChannelContentFilter{}
	if !channelMatchesEvent(channel, alertdomain.Event{Metric: "cpu_usage_percent", Severity: alertdomain.SeverityWarning, Status: "resolved"}) {
		t.Fatal("an empty content filter must preserve legacy match-all behavior")
	}
}

func TestDisablingChannelWaitsForInflightDeliveryAndStopsQueuedAlerts(t *testing.T) {
	requests := make(chan struct{}, 2)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests <- struct{}{}
		<-release
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	repo := newAlertMemoryRepo()
	repo.channels = []alertdomain.Channel{{
		ID: "channel-1", Name: "oncall", Type: "webhook", Enabled: true,
		MinimumSeverity: alertdomain.SeverityWarning, Config: map[string]string{"url": server.URL},
	}}
	service := NewAlertService(repo)
	event := alertdomain.Event{ID: "event-1", RuleName: "CPU high", Metric: "cpu_usage_percent", MachineID: "machine-1", Severity: alertdomain.SeverityWarning, Status: "firing", LastSeenAt: time.Now().UTC()}
	if !service.enqueue(event) {
		t.Fatal("first alert was not queued")
	}
	select {
	case <-requests:
	case <-time.After(2 * time.Second):
		t.Fatal("first delivery did not start")
	}

	disabled := make(chan error, 1)
	go func() {
		_, err := service.SetChannelEnabled(context.Background(), "channel-1", false)
		disabled <- err
	}()
	select {
	case <-disabled:
		t.Fatal("disable returned before the in-flight delivery reached a terminal state")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if err := <-disabled; err != nil {
		t.Fatal(err)
	}

	channels, err := repo.ListChannels(context.Background())
	if err != nil || len(channels) != 1 || channels[0].Enabled {
		t.Fatalf("channel must remain disabled after delivery status is persisted: %+v %v", channels, err)
	}
	event.ID = "event-2"
	if !service.enqueue(event) {
		t.Fatal("second alert was not queued")
	}
	select {
	case <-requests:
		t.Fatal("a disabled channel received a later alert")
	case <-time.After(350 * time.Millisecond):
	}
}

func TestBuildAlertEmailIncludesRFCHeaders(t *testing.T) {
	from, recipients, message, err := buildAlertEmail(
		"sender@example.com",
		[]string{"recipient@example.com"},
		"[GMHA][NOTICE] 告警通道测试",
		"测试邮件",
		"<html><body><strong>测试邮件</strong></body></html>",
	)
	if err != nil {
		t.Fatal(err)
	}
	if from != "sender@example.com" || len(recipients) != 1 || recipients[0] != "recipient@example.com" {
		t.Fatalf("unexpected SMTP envelope: from=%q recipients=%v", from, recipients)
	}
	text := string(message)
	for _, header := range []string{"From: ", "To: ", "Date: ", "Subject: =?UTF-8?", "MIME-Version: 1.0", "Content-Type: multipart/alternative", "Content-Type: text/plain; charset=UTF-8", "Content-Type: text/html; charset=UTF-8", "Content-Transfer-Encoding: quoted-printable"} {
		if !strings.Contains(text, header) {
			t.Fatalf("message is missing RFC header %q:\n%s", header, text)
		}
	}
}

func TestBuildAlertEmailWrapsEverySMTPLineBelowRFC5321Limit(t *testing.T) {
	longHTML := "<html><body><table><tr><td>" + strings.Repeat("告警实例与拓扑信息-DB-01-192.168.31.210:3306 ", 300) + "</td></tr></table></body></html>"
	_, _, message, err := buildAlertEmail(
		"sender@example.com",
		[]string{"recipient@example.com"},
		"[GMHA][严重][告警中] 超长邮件兼容性测试",
		strings.Repeat("超长纯文本告警内容 ", 300),
		longHTML,
	)
	if err != nil {
		t.Fatal(err)
	}
	for index, line := range strings.Split(string(message), "\r\n") {
		if len([]byte(line)) > 998 {
			t.Fatalf("SMTP line %d contains %d octets, exceeds RFC 5321 limit", index+1, len([]byte(line)))
		}
	}
}

func TestRenderAlertEmailIsReadableAndEscapesEventData(t *testing.T) {
	event := alertdomain.Event{
		ID: "event-01", RuleName: "复制延迟 <严重>", Metric: "mysql_replication_lag", MachineID: "db-01",
		ClusterID: "prod", Severity: alertdomain.SeverityCritical, Status: "firing", Value: 45.5, Threshold: 30, Operator: ">=",
		OccurrenceCount: 3, NotificationCount: 1, FirstSeenAt: time.Date(2026, 8, 5, 10, 0, 0, 0, time.UTC), LastSeenAt: time.Date(2026, 8, 5, 10, 2, 0, 0, time.UTC),
		Labels: map[string]string{
			"machine_name": "db-replica", "machine_ip": "10.8.0.11", "mysql_port": "3307",
			"instance_name": "db-replica:3307", "instance_endpoint": "10.8.0.11:3307", "instance_role": "从库",
			"topology_architecture_label": "主从复制", "instance_relation": "从库，复制自 db-primary:3306",
			"source_instance_name": "db-primary:3306", "source_instance_endpoint": "10.8.0.10:3306",
			"replica": "db-02", "unsafe": "<script>alert(1)</script>",
		},
	}
	subject, plainBody, htmlBody := renderAlertEmail(event)
	for _, value := range []string{"严重", "告警中", "复制延迟 <严重>"} {
		if !strings.Contains(subject, value) {
			t.Fatalf("subject is missing %q: %s", value, subject)
		}
	}
	for _, value := range []string{"当前值：45.5", "触发条件：45.5 >= 30", "实例名称：db-replica:3307", "所属集群：prod", "实例地址：10.8.0.11:3307", "实例角色：从库", "架构类型：主从复制", "实例关系：从库，复制自 db-primary:3306", "上游实例：db-primary:3306（10.8.0.10:3306）", "事件编号：event-01"} {
		if !strings.Contains(plainBody, value) {
			t.Fatalf("plain body is missing %q:\n%s", value, plainBody)
		}
	}
	for _, value := range []string{"GMHA 告警通知", "当前值 / 触发条件", "实例与拓扑", "实例名称", "所属集群", "实例地址", "实例角色", "实例关系", "上游实例", "事件信息", "附加标签", "&lt;script&gt;alert(1)&lt;/script&gt;"} {
		if !strings.Contains(htmlBody, value) {
			t.Fatalf("html body is missing %q", value)
		}
	}
	if strings.Contains(htmlBody, "<script>") {
		t.Fatal("html body contains unescaped event data")
	}
}

func TestAlertTopologyContextDoesNotChangeInstanceFingerprint(t *testing.T) {
	base := map[string]string{"mysql_port": "3306", "instance_role": "从库", "source_instance_name": "db-primary:3306"}
	changed := map[string]string{"mysql_port": "3306", "instance_role": "主库", "source_instance_name": "db-new-primary:3306", "instance_relation": "主库 / 下游实例的复制源"}
	if left, right := fingerprint("rule-1", "machine-1", base), fingerprint("rule-1", "machine-1", changed); left != right {
		t.Fatalf("topology metadata changed the instance fingerprint: %s != %s", left, right)
	}
}

func TestAlertFilterSuppressesByCIDRAndMessageRegex(t *testing.T) {
	repo := newAlertMemoryRepo()
	repo.rules = []alertdomain.Rule{{ID: "r1", Name: "复制延迟过高", Metric: "lag", Description: "replication delay", Enabled: true, Operator: ">=", Threshold: 10, Severity: alertdomain.SeverityWarning, ConsecutiveCount: 1}}
	repo.filters = []alertdomain.Filter{{Name: "maintenance", Enabled: true, IPCIDR: "10.8.0.0/16", MessagePattern: "replication.*delay", UseRegex: true}}
	service := NewAlertService(repo)
	payload := hbdomain.HeartbeatPayload{MachineID: "m1", MachineIP: "10.8.1.20", Metrics: []dynamicdomain.MetricResult{{Name: "lag", Category: "mysql", Success: true, Value: 30}}}
	service.evaluatePayload(context.Background(), payload)
	if len(repo.events) != 0 {
		t.Fatalf("filtered alert must not create events: %+v", repo.events)
	}
}

func TestAlertFilterResolvesExistingEventWithoutRewritingHistory(t *testing.T) {
	repo := newAlertMemoryRepo()
	repo.rules = []alertdomain.Rule{{
		ID: "r1", Name: "CPU high", Metric: "cpu", Enabled: true,
		Operator: ">=", Threshold: 80, Severity: alertdomain.SeverityWarning,
		ConsecutiveCount: 1, RepeatIntervalSeconds: 300,
	}}
	service := NewAlertService(repo)
	payload := hbdomain.HeartbeatPayload{
		MachineID: "m1", MachineIP: "10.8.1.20",
		Metrics: []dynamicdomain.MetricResult{{Name: "cpu", Category: "host", Success: true, Value: 90}},
	}
	service.evaluatePayload(context.Background(), payload)
	repo.filters = []alertdomain.Filter{{Name: "maintenance", Enabled: true, IPCIDR: "10.8.0.0/16"}}
	service.evaluatePayload(context.Background(), payload)
	events, err := service.ListEvents(context.Background(), alertdomain.EventFilter{})
	if err != nil || len(events) != 1 {
		t.Fatalf("historical event must remain queryable: %+v %v", events, err)
	}
	if events[0].Status != "resolved" || events[0].Labels["resolution_reason"] != "suppressed_by_filter" {
		t.Fatalf("active event should be resolved by the filter: %+v", events[0])
	}
}

func TestAlertEvaluationUsesStructuredMetricLeaves(t *testing.T) {
	repo := newAlertMemoryRepo()
	repo.rules = []alertdomain.Rule{{
		ID: "disk-busy", Name: "Disk busy", Metric: "host_disk_busy_percent",
		Enabled: true, Operator: ">=", Threshold: 80, Severity: alertdomain.SeverityCritical,
		ConsecutiveCount: 1, RepeatIntervalSeconds: 300,
	}}
	service := NewAlertService(repo)
	service.evaluatePayload(context.Background(), hbdomain.HeartbeatPayload{
		AgentID: "a1", MachineID: "m1",
		Metrics: []dynamicdomain.MetricResult{{
			Name: "io_status", Category: "disk_io", Success: true,
			Value:  map[string]any{"sda": map[string]any{"busy_ratio": 0.91}},
			Labels: map[string]string{"metric_scope": "machine_dynamic"},
		}},
	})
	if len(repo.events) != 1 {
		t.Fatalf("structured disk metric should create one event, got %+v", repo.events)
	}
	for _, event := range repo.events {
		if event.Metric != "host_disk_busy_percent" || event.Value != 91 || event.Labels["device"] != "sda" {
			t.Fatalf("unexpected normalized alert event: %+v", event)
		}
	}
}

func TestAlertEvaluationDoesNotCountRepeatedCollectorSample(t *testing.T) {
	repo := newAlertMemoryRepo()
	repo.rules = []alertdomain.Rule{{
		ID: "cpu-high", Name: "CPU high", Metric: "cpu",
		Enabled: true, Operator: ">=", Threshold: 80, Severity: alertdomain.SeverityWarning,
		ConsecutiveCount: 2, RepeatIntervalSeconds: 300,
	}}
	service := NewAlertService(repo)
	sampledAt := time.Now().UTC()
	payload := hbdomain.HeartbeatPayload{
		AgentID: "a1", MachineID: "m1",
		Metrics: []dynamicdomain.MetricResult{{Name: "cpu", Success: true, Value: 90, CollectedAt: sampledAt}},
	}
	service.evaluatePayload(context.Background(), payload)
	service.evaluatePayload(context.Background(), payload)
	if len(repo.events) != 0 {
		t.Fatalf("the same collector sample must not satisfy a consecutive threshold: %+v", repo.events)
	}
	payload.Metrics[0].CollectedAt = sampledAt.Add(5 * time.Second)
	service.evaluatePayload(context.Background(), payload)
	if len(repo.events) != 1 {
		t.Fatalf("a newer collector sample should complete the threshold: %+v", repo.events)
	}
}

func TestMySQLRestartClassificationUsesCriticalForUnexpectedRestart(t *testing.T) {
	repo := newAlertMemoryRepo()
	service := NewAlertService(repo)
	firstSample := time.Date(2026, 7, 28, 8, 0, 0, 0, time.UTC)
	payload := hbdomain.HeartbeatPayload{
		AgentID: "a1", MachineID: "m1", MachineName: "db-1", MachineIP: "10.0.0.8",
		Metrics: []dynamicdomain.MetricResult{{
			Name: "mysql_uptime", Category: "mysql", Success: true,
			Value: float64(3600), CollectedAt: firstSample,
			Labels: map[string]string{"mysql_port": "3306", "metric_scope": "mysql"},
		}},
	}
	service.evaluatePayload(context.Background(), payload)
	payload.Metrics[0].Value = float64(15)
	payload.Metrics[0].CollectedAt = firstSample.Add(10 * time.Second)
	service.evaluatePayload(context.Background(), payload)

	if len(repo.events) != 1 {
		t.Fatalf("unexpected restart should create one event, got %+v", repo.events)
	}
	for _, event := range repo.events {
		if event.Severity != alertdomain.SeverityCritical || event.RuleName != "MySQL 意外重启" {
			t.Fatalf("unexpected restart should be critical: %+v", event)
		}
		if event.Labels["restart_type"] != "unexpected" || event.Labels["mysql_port"] != "3306" {
			t.Fatalf("restart classification labels are incomplete: %+v", event.Labels)
		}
	}
}

func TestMySQLRestartClassificationUsesNoticeForManualRestart(t *testing.T) {
	repo := newAlertMemoryRepo()
	repo.restartClassification = alertdomain.MySQLRestartClassification{
		Manual: true, TaskID: "task-restart-1", Operation: "mysql_restart",
	}
	service := NewAlertService(repo)
	firstSample := time.Date(2026, 7, 28, 8, 0, 0, 0, time.UTC)
	payload := hbdomain.HeartbeatPayload{
		AgentID: "a1", MachineID: "m1",
		Metrics: []dynamicdomain.MetricResult{{
			Name: "mysql_uptime", Category: "mysql", Success: true,
			Value: float64(600), CollectedAt: firstSample,
			Labels: map[string]string{"mysql_port": "3307"},
		}},
	}
	service.evaluatePayload(context.Background(), payload)
	payload.Metrics[0].Value = float64(5)
	payload.Metrics[0].CollectedAt = firstSample.Add(5 * time.Second)
	service.evaluatePayload(context.Background(), payload)

	if len(repo.events) != 1 {
		t.Fatalf("manual restart should create one event, got %+v", repo.events)
	}
	for _, event := range repo.events {
		if event.Severity != alertdomain.SeverityNotice || event.RuleName != "MySQL 手动重启" {
			t.Fatalf("manual restart should be a notice: %+v", event)
		}
		if event.Labels["restart_type"] != "manual" || event.Labels["manual_task_id"] != "task-restart-1" || event.AutomationState != "skipped" {
			t.Fatalf("manual restart audit labels are incomplete: %+v", event)
		}
	}
}

func TestMySQLUptimeIncreaseDoesNotCreateRestartEvent(t *testing.T) {
	repo := newAlertMemoryRepo()
	service := NewAlertService(repo)
	firstSample := time.Date(2026, 7, 28, 8, 0, 0, 0, time.UTC)
	payload := hbdomain.HeartbeatPayload{
		MachineID: "m1",
		Metrics: []dynamicdomain.MetricResult{{
			Name: "mysql_uptime", Success: true, Value: float64(600), CollectedAt: firstSample,
			Labels: map[string]string{"mysql_port": "3306"},
		}},
	}
	service.evaluatePayload(context.Background(), payload)
	payload.Metrics[0].Value = float64(610)
	payload.Metrics[0].CollectedAt = firstSample.Add(10 * time.Second)
	service.evaluatePayload(context.Background(), payload)
	if len(repo.events) != 0 {
		t.Fatalf("normal uptime growth must not create a restart event: %+v", repo.events)
	}
}

func TestMergeDynamicCollectConfigAddsNewCollectorsAndPreservesChoices(t *testing.T) {
	saved := dynamicdomain.DynamicCollectConfig{
		Enabled: true, Version: "old",
		Tasks: []dynamicdomain.CollectTaskSpec{{
			Name: "cpu_usage_percent", Enabled: false, IntervalSeconds: 20, TimeoutSeconds: 2,
		}},
	}
	merged, changed := mergeDynamicCollectConfig(saved, dynamicdomain.BuildDefaultDynamicCollectConfig())
	if !changed || len(merged.Tasks) != len(dynamicdomain.BuildDefaultDynamicCollectConfig().Tasks) {
		t.Fatalf("new collectors were not merged: %+v", merged)
	}
	if merged.Tasks[0].Enabled || merged.Tasks[0].IntervalSeconds != 20 || merged.Tasks[0].Labels["display_name"] == "" {
		t.Fatalf("operator choices or refreshed metadata were lost: %+v", merged.Tasks[0])
	}
}

func TestSendZabbixNativeProtocol(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	received := make(chan map[string]any, 1)
	go func() {
		conn, _ := listener.Accept()
		defer conn.Close()
		header := make([]byte, 13)
		_, _ = io.ReadFull(conn, header)
		body := make([]byte, binary.LittleEndian.Uint64(header[5:]))
		_, _ = io.ReadFull(conn, body)
		var payload map[string]any
		_ = json.Unmarshal(body, &payload)
		received <- payload
		reply, _ := json.Marshal(map[string]any{"response": "success", "info": "processed: 1; failed: 0"})
		frame := append([]byte{'Z', 'B', 'X', 'D', 1}, make([]byte, 8)...)
		binary.LittleEndian.PutUint64(frame[5:], uint64(len(reply)))
		_, _ = conn.Write(append(frame, reply...))
	}()
	addr := listener.Addr().(*net.TCPAddr)
	event := alertdomain.Event{MachineID: "db-1", Metric: "mysql_process_alive", Value: 0, LastSeenAt: time.Now().UTC()}
	if err := sendZabbix(context.Background(), map[string]string{"host": "127.0.0.1", "port": strconv.Itoa(addr.Port)}, event); err != nil {
		t.Fatal(err)
	}
	payload := <-received
	if payload["request"] != "sender data" {
		t.Fatalf("unexpected payload: %+v", payload)
	}
}

func TestWebhookChannelPayloads(t *testing.T) {
	received := make(chan map[string]any, 3)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		received <- payload
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	service := NewAlertService(newAlertMemoryRepo())
	for _, channelType := range []string{"webhook", "dingtalk", "feishu"} {
		configKey := "webhook"
		if channelType == "webhook" {
			configKey = "url"
		}
		channel := alertdomain.Channel{
			Name: channelType, Type: channelType,
			Config: map[string]string{configKey: server.URL},
		}
		if err := service.TestChannel(context.Background(), channel); err != nil {
			t.Fatalf("%s test delivery failed: %v", channelType, err)
		}
		select {
		case payload := <-received:
			if len(payload) == 0 {
				t.Fatalf("%s sent an empty payload", channelType)
			}
		case <-time.After(time.Second):
			t.Fatalf("%s did not send a payload", channelType)
		}
	}
}
