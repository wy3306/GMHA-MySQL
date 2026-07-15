package app

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
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
func (r *alertMemoryRepo) SaveEvent(_ context.Context, x alertdomain.Event) error {
	r.events[x.ID] = x
	return nil
}
func (r *alertMemoryRepo) UpdateEventAction(context.Context, string, string, string, *time.Time) error {
	return nil
}
func (r *alertMemoryRepo) UpdateAutomationState(context.Context, string, string) error { return nil }
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
