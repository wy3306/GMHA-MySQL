package dynamic

import "testing"

func TestBuildDefaultDynamicCollectConfig(t *testing.T) {
	cfg := BuildDefaultDynamicCollectConfig()
	if !cfg.Enabled {
		t.Fatal("default dynamic collect config should be enabled")
	}
	if len(cfg.Tasks) != 11 {
		t.Fatalf("expected 11 host tasks, got %d", len(cfg.Tasks))
	}
	for _, task := range cfg.Tasks {
		if len(task.Name) >= 6 && task.Name[:6] == "mysql_" {
			t.Fatalf("mysql task leaked into host collectors: %+v", task)
		}
		if !task.Enabled || task.IntervalSeconds < 5 || task.Type != TaskTypeBuiltin {
			t.Fatalf("unexpected default task: %+v", task)
		}
	}
}
