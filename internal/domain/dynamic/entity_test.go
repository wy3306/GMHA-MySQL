package dynamic

import "testing"

func TestBuildDefaultDynamicCollectConfig(t *testing.T) {
	cfg := BuildDefaultDynamicCollectConfig()
	if !cfg.Enabled {
		t.Fatal("default dynamic collect config should be enabled")
	}
	if len(cfg.Tasks) != 16 {
		t.Fatalf("expected 16 default tasks, got %d", len(cfg.Tasks))
	}
	for _, task := range cfg.Tasks {
		if !task.Enabled || task.IntervalSeconds != 1 || task.Type != TaskTypeBuiltin {
			t.Fatalf("unexpected default task: %+v", task)
		}
	}
}
