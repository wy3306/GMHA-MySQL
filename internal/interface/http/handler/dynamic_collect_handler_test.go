package handler

import (
	"testing"

	dynamicdomain "gmha/internal/domain/dynamic"
)

func TestNormalizeCollectConfigProtectsAgentAndMySQLResources(t *testing.T) {
	cfg := dynamicdomain.DynamicCollectConfig{Enabled: true, Tasks: []dynamicdomain.CollectTaskSpec{
		{Name: "mysql_query", Enabled: true, IntervalSeconds: 1, TimeoutSeconds: 30, Params: map[string]string{"query": "select 1"}},
		{Name: "agent_cpu_usage_percent", Enabled: true, IntervalSeconds: 1, TimeoutSeconds: 0},
	}}
	normalized, err := normalizeCollectConfig(cfg, 5)
	if err != nil {
		t.Fatal(err)
	}
	if normalized.Tasks[0].IntervalSeconds != 5 || normalized.Tasks[0].TimeoutSeconds != 5 {
		t.Fatalf("mysql limits not applied: %+v", normalized.Tasks[0])
	}
	if normalized.Tasks[1].IntervalSeconds != 15 || normalized.Tasks[1].TimeoutSeconds != 1 {
		t.Fatalf("agent limits not applied: %+v", normalized.Tasks[1])
	}
}

func TestNormalizeCollectConfigRejectsUnboundedCollectorCount(t *testing.T) {
	cfg := dynamicdomain.DynamicCollectConfig{Tasks: make([]dynamicdomain.CollectTaskSpec, 257)}
	if _, err := normalizeCollectConfig(cfg, 1); err == nil {
		t.Fatal("expected collector limit error")
	}
}

func TestNormalizeCollectConfigRejectsDuplicateAndUnknownCollectors(t *testing.T) {
	duplicate := dynamicdomain.DynamicCollectConfig{Tasks: []dynamicdomain.CollectTaskSpec{
		{Name: "cpu", Type: dynamicdomain.TaskTypeBuiltin},
		{Name: " cpu ", Type: dynamicdomain.TaskTypeBuiltin},
	}}
	if _, err := normalizeCollectConfig(duplicate, 1); err == nil {
		t.Fatal("duplicate collector names must be rejected")
	}
	unknownType := dynamicdomain.DynamicCollectConfig{Tasks: []dynamicdomain.CollectTaskSpec{{
		Name: "cpu", Type: "plugin",
	}}}
	if _, err := normalizeCollectConfig(unknownType, 1); err == nil {
		t.Fatal("unknown collector types must be rejected")
	}
}
