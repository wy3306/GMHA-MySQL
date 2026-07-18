package dynamic

import (
	"context"
	"testing"
	"time"

	dyndomain "gmha/internal/domain/dynamic"
)

type fakeCollector struct{ name string }

func (f fakeCollector) Name() string { return f.name }
func (f fakeCollector) Collect(ctx context.Context, spec dyndomain.CollectTaskSpec) dyndomain.MetricResult {
	return metricOK(spec, "test", dyndomain.ValueTypeString, spec.Name, time.Now())
}

func TestRegistryAndHotUpdate(t *testing.T) {
	reg := NewCollectorRegistry()
	reg.Register("a", func() DynamicCollector { return fakeCollector{name: "a"} })
	mgr := NewDynamicCollectManager("agent-1", reg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mgr.Start(ctx, dyndomain.DynamicCollectConfig{Enabled: true, Version: "v1", Tasks: []dyndomain.CollectTaskSpec{{Name: "a", Enabled: true, Type: dyndomain.TaskTypeBuiltin, IntervalSeconds: 1}}})
	if _, ok := mgr.runners["a"]; !ok {
		t.Fatal("collector a not started")
	}
	time.Sleep(20 * time.Millisecond)
	if _, ok := mgr.GetLastMetricResult("a"); !ok {
		t.Fatal("collector a did not publish a result")
	}
	mgr.UpdateCollectConfig(ctx, dyndomain.DynamicCollectConfig{Enabled: true, Version: "v2", Tasks: []dyndomain.CollectTaskSpec{{Name: "a", Enabled: false, Type: dyndomain.TaskTypeBuiltin, IntervalSeconds: 1}}})
	if _, ok := mgr.runners["a"]; ok {
		t.Fatal("collector a should be stopped after disabling")
	}
	if _, ok := mgr.GetLastMetricResult("a"); ok {
		t.Fatal("disabled collector result must not remain in heartbeat batch")
	}
}
