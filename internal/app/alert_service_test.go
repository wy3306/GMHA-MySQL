package app

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	alertdomain "gmha/internal/domain/alert"
	dynamicdomain "gmha/internal/domain/dynamic"
	hbdomain "gmha/internal/domain/heartbeat"
)

type alertMemoryRepo struct {
	rules    []alertdomain.Rule
	events   map[string]alertdomain.Event
	states   map[string]alertdomain.EvaluationState
	channels []alertdomain.Channel
	filters  []alertdomain.Filter
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
func (r *alertMemoryRepo) ListChannels(context.Context) ([]alertdomain.Channel, error) {
	return r.channels, nil
}
func (r *alertMemoryRepo) SaveChannel(context.Context, alertdomain.Channel) error { return nil }
func (r *alertMemoryRepo) DeleteChannel(context.Context, string) error            { return nil }
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
