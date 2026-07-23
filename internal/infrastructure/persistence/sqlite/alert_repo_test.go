package sqlite

import (
	"context"
	"database/sql"
	"testing"
	"time"

	alertdomain "gmha/internal/domain/alert"
	dynamicdomain "gmha/internal/domain/dynamic"
	_ "modernc.org/sqlite"
)

func TestAlertRepositoryPersistsRuleEventChannelAndMetricConfig(t *testing.T) {
	db, err := sql.Open("sqlite", t.TempDir()+"/alerts.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := NewAlertRepository(NewDB(db, DialectSQLite))
	if err := repo.Migrate(); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	rule := alertdomain.Rule{ID: "rule-1", Name: "CPU", Metric: "cpu_usage_percent", Enabled: true, Operator: ">=", Threshold: 80, Severity: alertdomain.SeverityWarning, Thresholds: []alertdomain.ThresholdLevel{{Severity: alertdomain.SeverityWarning, Threshold: 80, Enabled: true}, {Severity: alertdomain.SeverityFatal, Threshold: 95, Enabled: true}}, ConsecutiveCount: 3, RepeatIntervalSeconds: 300, MaxNotifications: 5, CreatedAt: now, UpdatedAt: now}
	if err := repo.SaveRule(ctx, rule); err != nil {
		t.Fatal(err)
	}
	rules, err := repo.ListRules(ctx)
	if err != nil || len(rules) != 1 || rules[0].MaxNotifications != 5 || len(rules[0].Thresholds) != 2 {
		t.Fatalf("rules: %+v %v", rules, err)
	}
	filter := alertdomain.Filter{ID: "filter-1", Name: "maintenance", Enabled: true, ClusterPattern: "prod", IPCIDR: "10.0.0.0/8", MessagePattern: "timeout.*", UseRegex: true, CreatedAt: now, UpdatedAt: now}
	if err := repo.SaveFilter(ctx, filter); err != nil {
		t.Fatal(err)
	}
	filters, err := repo.ListFilters(ctx)
	if err != nil || len(filters) != 1 || filters[0].IPCIDR != filter.IPCIDR {
		t.Fatalf("filters: %+v %v", filters, err)
	}
	event := alertdomain.Event{ID: "event-1", Fingerprint: "fp", RuleID: rule.ID, RuleName: rule.Name, Metric: rule.Metric, MachineID: "m1", AgentID: "a1", Severity: rule.Severity, Status: "firing", Value: 90, Threshold: 80, Operator: ">=", OccurrenceCount: 1, FirstSeenAt: now, LastSeenAt: now, AutomationState: "pending"}
	if err := repo.SaveEvent(ctx, event); err != nil {
		t.Fatal(err)
	}
	activeEvents, err := repo.ListActiveEventsForRuleTarget(ctx, rule.ID, event.MachineID)
	if err != nil || len(activeEvents) != 1 || activeEvents[0].ID != event.ID {
		t.Fatalf("active events for target: %+v %v", activeEvents, err)
	}
	event.Fingerprint = "stable-fp"
	if err := repo.SaveEvent(ctx, event); err != nil {
		t.Fatal(err)
	}
	if _, found, err := repo.GetActiveEvent(ctx, "fp"); err != nil || found {
		t.Fatalf("old fingerprint should no longer identify the active event: found=%v err=%v", found, err)
	}
	if updated, found, err := repo.GetActiveEvent(ctx, event.Fingerprint); err != nil || !found || updated.ID != event.ID {
		t.Fatalf("updated fingerprint was not persisted: %+v found=%v err=%v", updated, found, err)
	}
	if err := repo.UpdateEventAction(ctx, event.ID, "acknowledge", "operator", nil); err != nil {
		t.Fatal(err)
	}
	silencedUntil := now.Add(time.Hour)
	if err := repo.UpdateEventAction(ctx, event.ID, "silence", "operator", &silencedUntil); err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateAutomationState(ctx, event.ID, "claimed", "pending"); err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateAutomationState(ctx, event.ID, "running", "pending"); err != alertdomain.ErrConflict {
		t.Fatalf("stale automation claim must conflict, got %v", err)
	}
	events, err := repo.ListEvents(ctx, alertdomain.EventFilter{Status: "firing"})
	if err != nil || len(events) != 1 || events[0].AutomationState != "claimed" || events[0].AcknowledgedBy != "operator" || events[0].SilencedUntil == nil {
		t.Fatalf("events: %+v %v", events, err)
	}
	summary, err := repo.SummarizeEvents(ctx, now.Add(time.Minute))
	if err != nil || summary.Total != 1 || summary.Counts["firing"] != 1 || summary.Counts["warning"] != 1 || summary.ActiveAcknowledged != 1 || summary.ActiveSilenced != 1 {
		t.Fatalf("summary: %+v %v", summary, err)
	}
	channel := alertdomain.Channel{ID: "channel-1", Name: "webhook", Type: "webhook", Enabled: true, MinimumSeverity: alertdomain.SeverityWarning, Config: map[string]string{"url": "https://example.test"}, CreatedAt: now, UpdatedAt: now}
	if err := repo.SaveChannel(ctx, channel); err != nil {
		t.Fatal(err)
	}
	channels, err := repo.ListChannels(ctx)
	if err != nil || len(channels) != 1 || channels[0].Config["url"] == "" {
		t.Fatalf("channels: %+v %v", channels, err)
	}
	delivery := alertdomain.Delivery{ID: "delivery-1", EventID: event.ID, RuleName: rule.Name, Severity: rule.Severity, MachineID: "m1", ChannelID: channel.ID, ChannelName: channel.Name, ChannelType: channel.Type, Status: "success", DeliveredAt: now}
	if err := repo.SaveDelivery(ctx, delivery); err != nil {
		t.Fatal(err)
	}
	deliveries, err := repo.ListDeliveries(ctx, 10)
	if err != nil || len(deliveries) != 1 || deliveries[0].EventID != event.ID {
		t.Fatalf("deliveries: %+v %v", deliveries, err)
	}
	job := alertdomain.NotificationJob{ID: "job-1", Event: event, CreatedAt: now, UpdatedAt: now}
	if err := repo.SaveNotificationJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	if count, err := repo.CountPendingNotificationJobs(ctx); err != nil || count != 1 {
		t.Fatalf("pending outbox count: %d %v", count, err)
	}
	jobs, err := repo.ListPendingNotificationJobs(ctx, now.Add(time.Second), 10)
	if err != nil || len(jobs) != 1 || jobs[0].Event.ID != event.ID {
		t.Fatalf("outbox jobs: %+v %v", jobs, err)
	}
	if err := repo.FinishNotificationJob(ctx, job.ID, false, "temporary failure", now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	jobs, err = repo.ListPendingNotificationJobs(ctx, now.Add(2*time.Second), 10)
	if err != nil || len(jobs) != 1 || jobs[0].Attempts != 1 || jobs[0].LastError == "" {
		t.Fatalf("retryable outbox job: %+v %v", jobs, err)
	}
	if err := repo.FinishNotificationJob(ctx, job.ID, true, "", now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	jobs, err = repo.ListPendingNotificationJobs(ctx, now.Add(3*time.Second), 10)
	if err != nil || len(jobs) != 0 {
		t.Fatalf("completed outbox job should be removed: %+v %v", jobs, err)
	}
	if count, err := repo.CountPendingNotificationJobs(ctx); err != nil || count != 0 {
		t.Fatalf("completed outbox count: %d %v", count, err)
	}
	cfg := dynamicdomain.BuildDefaultDynamicCollectConfig()
	if err := repo.SaveMetricConfig(ctx, "host", cfg); err != nil {
		t.Fatal(err)
	}
	loaded, ok, err := repo.LoadMetricConfig(ctx, "host")
	if err != nil || !ok || len(loaded.Tasks) != len(cfg.Tasks) {
		t.Fatalf("config: %+v %v %v", loaded, ok, err)
	}
	if err := repo.UpdateEventAction(ctx, "missing-event", "resolve", "", nil); err != alertdomain.ErrNotFound {
		t.Fatalf("missing event must return ErrNotFound, got %v", err)
	}
}
