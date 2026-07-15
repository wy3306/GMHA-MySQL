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
	if err := repo.UpdateAutomationState(ctx, event.ID, "claimed"); err != nil {
		t.Fatal(err)
	}
	events, err := repo.ListEvents(ctx, alertdomain.EventFilter{Status: "firing"})
	if err != nil || len(events) != 1 || events[0].AutomationState != "claimed" {
		t.Fatalf("events: %+v %v", events, err)
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
	cfg := dynamicdomain.BuildDefaultDynamicCollectConfig()
	if err := repo.SaveMetricConfig(ctx, "host", cfg); err != nil {
		t.Fatal(err)
	}
	loaded, ok, err := repo.LoadMetricConfig(ctx, "host")
	if err != nil || !ok || len(loaded.Tasks) != len(cfg.Tasks) {
		t.Fatalf("config: %+v %v %v", loaded, ok, err)
	}
}
