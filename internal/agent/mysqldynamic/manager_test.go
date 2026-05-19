package mysqldynamic

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	dyndomain "gmha/internal/domain/dynamic"
)

type fakeMySQLCollector struct {
	name  string
	count *atomic.Int64
}

func (f fakeMySQLCollector) Name() string { return f.name }

func (f fakeMySQLCollector) Category() string { return "custom" }

func (f fakeMySQLCollector) Collect(ctx context.Context, env *CollectEnv, spec dyndomain.CollectTaskSpec) dyndomain.MetricResult {
	f.count.Add(1)
	return metricOK(spec, "custom", dyndomain.ValueTypeBool, true, time.Now())
}

func TestRegistryAndHotUpdate(t *testing.T) {
	var count atomic.Int64
	reg := NewCollectorRegistry()
	reg.Register("a", func() MySQLDynamicCollector { return fakeMySQLCollector{name: "a", count: &count} })
	if _, err := reg.Create("a"); err != nil {
		t.Fatalf("registered collector not found: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mgr := NewMySQLDynamicCollectManager("agent-1", reg, func() (*CollectEnv, error) { return &CollectEnv{}, nil })
	mgr.Start(ctx, dyndomain.DynamicCollectConfig{
		Enabled: true,
		Version: "v1",
		Tasks: []dyndomain.CollectTaskSpec{{
			Name:            "a",
			Enabled:         true,
			Type:            dyndomain.TaskTypeBuiltin,
			Category:        "custom",
			IntervalSeconds: 1,
			TimeoutSeconds:  1,
		}},
	})
	time.Sleep(50 * time.Millisecond)
	if count.Load() == 0 {
		t.Fatalf("expected collector to run at least once")
	}
	if _, ok := mgr.GetLastMetricResult("a"); !ok {
		t.Fatalf("expected last metric result")
	}
	mgr.UpdateMySQLDynamicCollectConfig(ctx, dyndomain.DynamicCollectConfig{
		Enabled: true,
		Version: "v2",
		Tasks: []dyndomain.CollectTaskSpec{{
			Name:            "a",
			Enabled:         false,
			Type:            dyndomain.TaskTypeBuiltin,
			Category:        "custom",
			IntervalSeconds: 1,
			TimeoutSeconds:  1,
		}},
	})
	if len(mgr.runners) != 0 {
		t.Fatalf("expected disabled collector to stop")
	}
}
