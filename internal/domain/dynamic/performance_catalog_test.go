package dynamic

import "testing"

func TestPerformanceCatalogIncludesEveryConfiguredMetricAndMachineSeries(t *testing.T) {
	catalog := BuildPerformanceMetricCatalog()
	seen := make(map[string]PerformanceMetricDefinition, len(catalog))
	for _, item := range catalog {
		if item.Name == "" || item.DisplayName == "" || item.Scope == "" || item.ValueKind == "" || item.Aggregation == "" {
			t.Fatalf("incomplete metric definition: %+v", item)
		}
		if !item.Available {
			t.Fatalf("metric must have an implemented collector mapping: %+v", item)
		}
		if _, exists := seen[item.Name]; exists {
			t.Fatalf("duplicate metric definition: %s", item.Name)
		}
		seen[item.Name] = item
	}
	for _, task := range BuildDefaultMySQLDynamicCollectConfig().Tasks {
		if _, ok := seen[task.Name]; !ok {
			t.Fatalf("configured MySQL metric missing from catalog: %s", task.Name)
		}
	}
	for _, name := range []string{
		"cpu_usage_percent", "mem_usage_percent", "host_load_1m",
		"host_disk_busy_percent", "host_disk_read_iops", "host_disk_write_iops",
		"host_disk_read_bytes_sec", "host_disk_write_bytes_sec",
		"host_filesystem_used_percent", "host_inode_used_percent",
		"host_network_receive_bytes_sec", "host_network_transmit_bytes_sec",
		"host_memory_available_bytes", "host_mysql_process_rss_bytes",
		"mysql_memory_tracked_bytes", "mysql_memory_module_bytes",
	} {
		if item, ok := seen[name]; !ok || !item.Available {
			t.Fatalf("machine performance metric missing or unavailable: %s", name)
		}
	}
}
