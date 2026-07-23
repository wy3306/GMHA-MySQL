package dynamic

import "testing"

func TestBuildDefaultDynamicCollectConfig(t *testing.T) {
	cfg := BuildDefaultDynamicCollectConfig()
	if !cfg.Enabled {
		t.Fatal("default dynamic collect config should be enabled")
	}
	if len(cfg.Tasks) != 13 {
		t.Fatalf("expected 13 host tasks, got %d", len(cfg.Tasks))
	}
	foundSwap, foundMemoryDetail := false, false
	for _, task := range cfg.Tasks {
		if len(task.Name) >= 6 && task.Name[:6] == "mysql_" {
			t.Fatalf("mysql task leaked into host collectors: %+v", task)
		}
		if !task.Enabled || task.IntervalSeconds < 5 || task.Type != TaskTypeBuiltin {
			t.Fatalf("unexpected default task: %+v", task)
		}
		if task.Name == "swap_usage" {
			foundSwap = true
		}
		if task.Name == "host_memory_detail" {
			foundMemoryDetail = true
		}
	}
	if !foundSwap {
		t.Fatal("swap capacity collector missing from default host tasks")
	}
	if !foundMemoryDetail {
		t.Fatal("memory detail collector missing from default host tasks")
	}
}
