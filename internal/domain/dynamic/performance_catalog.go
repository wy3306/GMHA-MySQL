package dynamic

import (
	"sort"
	"strings"
)

// PerformanceMetricDefinition describes one externally queryable metric.
// Raw collector counters are marked as counter so the API can return a
// time-normalized rate instead of exposing a misleading cumulative value.
type PerformanceMetricDefinition struct {
	Name              string `json:"name"`
	DisplayName       string `json:"display_name"`
	Scope             string `json:"scope"`
	Category          string `json:"category"`
	Unit              string `json:"unit"`
	ValueKind         string `json:"value_kind"`
	Aggregation       string `json:"aggregation"`
	IntervalSeconds   int    `json:"interval_seconds"`
	Available         bool   `json:"available"`
	UnavailableReason string `json:"unavailable_reason,omitempty"`
	Description       string `json:"description"`
}

var machinePerformanceMetrics = []PerformanceMetricDefinition{
	{Name: "cpu_usage_percent", DisplayName: "CPU 使用率", Scope: "machine", Category: "cpu", Unit: "%", ValueKind: "gauge", Aggregation: "avg", IntervalSeconds: 5, Available: true, Description: "机器所有 CPU 核心的非空闲时间占比"},
	{Name: "mem_usage_percent", DisplayName: "内存使用率", Scope: "machine", Category: "memory", Unit: "%", ValueKind: "gauge", Aggregation: "avg", IntervalSeconds: 5, Available: true, Description: "基于 MemAvailable 计算的物理内存使用率"},
	{Name: "host_load_1m", DisplayName: "1 分钟负载", Scope: "machine", Category: "load", Unit: "", ValueKind: "gauge", Aggregation: "avg", IntervalSeconds: 5, Available: true, Description: "操作系统 1 分钟 load average"},
	{Name: "host_load_5m", DisplayName: "5 分钟负载", Scope: "machine", Category: "load", Unit: "", ValueKind: "gauge", Aggregation: "avg", IntervalSeconds: 5, Available: true, Description: "操作系统 5 分钟 load average"},
	{Name: "host_load_15m", DisplayName: "15 分钟负载", Scope: "machine", Category: "load", Unit: "", ValueKind: "gauge", Aggregation: "avg", IntervalSeconds: 5, Available: true, Description: "操作系统 15 分钟 load average"},
	{Name: "host_disk_busy_percent", DisplayName: "磁盘 IO 忙碌率", Scope: "machine", Category: "disk_io", Unit: "%", ValueKind: "gauge", Aggregation: "max", IntervalSeconds: 5, Available: true, Description: "按块设备采集的 IO busy 时间占比"},
	{Name: "host_disk_read_iops", DisplayName: "磁盘读 IOPS", Scope: "machine", Category: "disk_io", Unit: "次/s", ValueKind: "gauge", Aggregation: "sum", IntervalSeconds: 5, Available: true, Description: "所有块设备每秒读操作数"},
	{Name: "host_disk_write_iops", DisplayName: "磁盘写 IOPS", Scope: "machine", Category: "disk_io", Unit: "次/s", ValueKind: "gauge", Aggregation: "sum", IntervalSeconds: 5, Available: true, Description: "所有块设备每秒写操作数"},
	{Name: "host_disk_read_bytes_sec", DisplayName: "磁盘读取吞吐", Scope: "machine", Category: "disk_io", Unit: "B/s", ValueKind: "gauge", Aggregation: "sum", IntervalSeconds: 5, Available: true, Description: "所有块设备每秒读取字节数"},
	{Name: "host_disk_write_bytes_sec", DisplayName: "磁盘写入吞吐", Scope: "machine", Category: "disk_io", Unit: "B/s", ValueKind: "gauge", Aggregation: "sum", IntervalSeconds: 5, Available: true, Description: "所有块设备每秒写入字节数"},
	{Name: "host_filesystem_used_percent", DisplayName: "文件系统使用率", Scope: "machine", Category: "filesystem", Unit: "%", ValueKind: "gauge", Aggregation: "max", IntervalSeconds: 30, Available: true, Description: "按挂载点采集的文件系统空间使用率"},
	{Name: "host_filesystem_used_bytes", DisplayName: "文件系统已用空间", Scope: "machine", Category: "filesystem", Unit: "B", ValueKind: "gauge", Aggregation: "sum", IntervalSeconds: 30, Available: true, Description: "按挂载点采集的已使用字节数"},
	{Name: "host_filesystem_available_bytes", DisplayName: "文件系统可用空间", Scope: "machine", Category: "filesystem", Unit: "B", ValueKind: "gauge", Aggregation: "sum", IntervalSeconds: 30, Available: true, Description: "按挂载点采集的可用字节数"},
	{Name: "host_inode_used_percent", DisplayName: "Inode 使用率", Scope: "machine", Category: "filesystem", Unit: "%", ValueKind: "gauge", Aggregation: "max", IntervalSeconds: 30, Available: true, Description: "按挂载点采集的 inode 使用率"},
	{Name: "host_swap_used_percent", DisplayName: "Swap 使用率", Scope: "machine", Category: "memory", Unit: "%", ValueKind: "gauge", Aggregation: "max", IntervalSeconds: 30, Available: true, Description: "按 Swap 设备采集的空间使用率；未配置 Swap 时为 0"},
	{Name: "host_swap_used_bytes", DisplayName: "Swap 已用空间", Scope: "machine", Category: "memory", Unit: "B", ValueKind: "gauge", Aggregation: "sum", IntervalSeconds: 30, Available: true, Description: "所有 Swap 设备已使用字节数"},
	{Name: "host_swap_available_bytes", DisplayName: "Swap 可用空间", Scope: "machine", Category: "memory", Unit: "B", ValueKind: "gauge", Aggregation: "sum", IntervalSeconds: 30, Available: true, Description: "所有 Swap 设备可用字节数"},
	{Name: "host_network_receive_bytes_sec", DisplayName: "网络接收吞吐", Scope: "machine", Category: "network", Unit: "B/s", ValueKind: "gauge", Aggregation: "sum", IntervalSeconds: 5, Available: true, Description: "非回环网卡每秒接收字节数"},
	{Name: "host_network_transmit_bytes_sec", DisplayName: "网络发送吞吐", Scope: "machine", Category: "network", Unit: "B/s", ValueKind: "gauge", Aggregation: "sum", IntervalSeconds: 5, Available: true, Description: "非回环网卡每秒发送字节数"},
	{Name: "ntp_offset_ms", DisplayName: "时钟偏移", Scope: "machine", Category: "system", Unit: "ms", ValueKind: "gauge", Aggregation: "max", IntervalSeconds: 60, Available: true, Description: "chrony 报告的系统时钟偏移"},
	{Name: "host_ssh_probe_ok", DisplayName: "SSH 综合探测", Scope: "machine", Category: "system", Unit: "0/1", ValueKind: "state", Aggregation: "min", IntervalSeconds: 30, Available: true, Description: "SSH 端口或服务任一正常时为 1"},
	{Name: "host_ssh_tcp_ok", DisplayName: "SSH 端口探测", Scope: "machine", Category: "system", Unit: "0/1", ValueKind: "state", Aggregation: "min", IntervalSeconds: 30, Available: true, Description: "本机 SSH TCP 端口可连接时为 1"},
	{Name: "host_ssh_service_ok", DisplayName: "SSH 服务状态", Scope: "machine", Category: "system", Unit: "0/1", ValueKind: "state", Aggregation: "min", IntervalSeconds: 30, Available: true, Description: "systemd 或 sshd 进程正常时为 1"},
	{Name: "agent_cpu_usage_percent", DisplayName: "Agent CPU 使用率", Scope: "machine", Category: "agent", Unit: "%", ValueKind: "gauge", Aggregation: "avg", IntervalSeconds: 15, Available: true, Description: "GMHA Agent 进程自身 CPU 使用率"},
	{Name: "agent_memory_rss_mb", DisplayName: "Agent 内存占用", Scope: "machine", Category: "agent", Unit: "MB", ValueKind: "gauge", Aggregation: "sum", IntervalSeconds: 15, Available: true, Description: "GMHA Agent 进程常驻内存"},
}

var memoryAnalysisMetrics = []PerformanceMetricDefinition{
	{Name: "host_memory_total_bytes", DisplayName: "物理内存总量", Scope: "machine", Category: "memory", Unit: "B", ValueKind: "gauge", Aggregation: "sum", IntervalSeconds: 15, Available: true, Description: "Linux MemTotal"},
	{Name: "host_memory_used_bytes", DisplayName: "物理内存已用", Scope: "machine", Category: "memory", Unit: "B", ValueKind: "gauge", Aggregation: "sum", IntervalSeconds: 15, Available: true, Description: "MemTotal 减去 MemAvailable"},
	{Name: "host_memory_available_bytes", DisplayName: "物理内存可用", Scope: "machine", Category: "memory", Unit: "B", ValueKind: "gauge", Aggregation: "sum", IntervalSeconds: 15, Available: true, Description: "内核估算的可分配内存"},
	{Name: "host_memory_free_bytes", DisplayName: "完全空闲内存", Scope: "machine", Category: "memory", Unit: "B", ValueKind: "gauge", Aggregation: "sum", IntervalSeconds: 15, Available: true, Description: "Linux MemFree"},
	{Name: "host_memory_buffers_bytes", DisplayName: "块设备缓冲", Scope: "machine", Category: "memory", Unit: "B", ValueKind: "gauge", Aggregation: "sum", IntervalSeconds: 15, Available: true, Description: "Linux Buffers"},
	{Name: "host_memory_cached_bytes", DisplayName: "可回收文件缓存", Scope: "machine", Category: "memory", Unit: "B", ValueKind: "gauge", Aggregation: "sum", IntervalSeconds: 15, Available: true, Description: "Cached、SReclaimable 与 Shmem 的组合"},
	{Name: "host_memory_anon_bytes", DisplayName: "匿名页", Scope: "machine", Category: "memory", Unit: "B", ValueKind: "gauge", Aggregation: "sum", IntervalSeconds: 15, Available: true, Description: "进程堆栈等匿名内存"},
	{Name: "host_memory_slab_bytes", DisplayName: "内核 Slab", Scope: "machine", Category: "memory", Unit: "B", ValueKind: "gauge", Aggregation: "sum", IntervalSeconds: 15, Available: true, Description: "内核对象缓存"},
	{Name: "host_memory_page_tables_bytes", DisplayName: "页表内存", Scope: "machine", Category: "memory", Unit: "B", ValueKind: "gauge", Aggregation: "sum", IntervalSeconds: 15, Available: true, Description: "Linux PageTables"},
	{Name: "host_memory_kernel_stack_bytes", DisplayName: "内核栈", Scope: "machine", Category: "memory", Unit: "B", ValueKind: "gauge", Aggregation: "sum", IntervalSeconds: 15, Available: true, Description: "Linux KernelStack"},
	{Name: "host_memory_swap_used_bytes", DisplayName: "Swap 已用", Scope: "machine", Category: "memory", Unit: "B", ValueKind: "gauge", Aggregation: "sum", IntervalSeconds: 15, Available: true, Description: "主机 Swap 已用字节数"},
	{Name: "host_mysql_process_rss_bytes", DisplayName: "MySQL 进程 RSS", Scope: "machine", Category: "memory", Unit: "B", ValueKind: "gauge", Aggregation: "sum", IntervalSeconds: 15, Available: true, Description: "主机 mysqld 与 mariadbd 进程常驻内存合计"},
	{Name: "mysql_memory_tracked_bytes", DisplayName: "数据库已跟踪内存", Scope: "mysql", Category: "memory", Unit: "B", ValueKind: "gauge", Aggregation: "sum", IntervalSeconds: 60, Available: true, Description: "performance_schema 内存模块当前值合计"},
	{Name: "mysql_memory_high_water_bytes", DisplayName: "数据库内存历史峰值", Scope: "mysql", Category: "memory", Unit: "B", ValueKind: "gauge", Aggregation: "sum", IntervalSeconds: 60, Available: true, Description: "performance_schema 内存模块峰值合计"},
	{Name: "mysql_memory_module_count", DisplayName: "数据库内存模块数", Scope: "mysql", Category: "memory", Unit: "个", ValueKind: "gauge", Aggregation: "sum", IntervalSeconds: 60, Available: true, Description: "当前有内存占用的数据库模块数量"},
	{Name: "mysql_memory_module_bytes", DisplayName: "数据库模块当前内存", Scope: "mysql", Category: "memory", Unit: "B", ValueKind: "gauge", Aggregation: "sum", IntervalSeconds: 60, Available: true, Description: "细分到每个 performance_schema 内存事件"},
	{Name: "mysql_memory_module_high_bytes", DisplayName: "数据库模块峰值内存", Scope: "mysql", Category: "memory", Unit: "B", ValueKind: "gauge", Aggregation: "sum", IntervalSeconds: 60, Available: true, Description: "每个数据库内存模块的历史高水位"},
	{Name: "mysql_memory_module_allocations", DisplayName: "数据库模块当前分配数", Scope: "mysql", Category: "memory", Unit: "个", ValueKind: "gauge", Aggregation: "sum", IntervalSeconds: 60, Available: true, Description: "每个数据库内存模块当前未释放的分配数量"},
}

// BuildPerformanceMetricCatalog is the single catalog used by persistence,
// external APIs and the UI. It intentionally includes unavailable/reserved
// MySQL definitions so omissions are observable rather than silently hidden.
func BuildPerformanceMetricCatalog() []PerformanceMetricDefinition {
	out := append([]PerformanceMetricDefinition{}, machinePerformanceMetrics...)
	for _, task := range BuildDefaultMySQLDynamicCollectConfig().Tasks {
		displayName := task.Labels["display_name"]
		if displayName == "" {
			displayName = task.Name
		}
		kind := inferMetricKind(task.Name)
		definition := PerformanceMetricDefinition{
			Name: task.Name, DisplayName: displayName, Scope: "mysql",
			Category: task.Category, Unit: inferMetricUnit(task.Name),
			ValueKind: kind, Aggregation: inferMetricAggregation(task.Name),
			IntervalSeconds: task.IntervalSeconds, Available: task.Enabled,
			Description: displayName,
		}
		if !task.Enabled {
			definition.UnavailableReason = task.Labels["disabled_reason"]
		}
		out = append(out, definition)
	}
	out = append(out, memoryAnalysisMetrics...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Scope != out[j].Scope {
			return out[i].Scope < out[j].Scope
		}
		if out[i].Category != out[j].Category {
			return out[i].Category < out[j].Category
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func FindPerformanceMetric(name string) (PerformanceMetricDefinition, bool) {
	for _, item := range BuildPerformanceMetricCatalog() {
		if item.Name == name {
			return item, true
		}
	}
	return PerformanceMetricDefinition{}, false
}

func inferMetricKind(name string) string {
	if metricIsCounter(name) {
		return "counter"
	}
	if strings.Contains(name, "status") || strings.HasSuffix(name, "_ok") || strings.Contains(name, "role") || strings.Contains(name, "enabled") {
		return "state"
	}
	return "gauge"
}

func inferMetricUnit(name string) string {
	switch {
	case strings.HasSuffix(name, "_disk_usage"):
		return "%"
	case name == "mysql_error_log_growth_per_min":
		return "B/min"
	case name == "mysql_error_log_growth" ||
		name == "mysql_slow_query_log_growth" || name == "mysql_tmp_dir_space_change" ||
		name == "mysql_undo_growth_rate" || name == "mysql_master_binlog_rate" ||
		name == "mysql_relay_log_growth_rate" || name == "mysql_relay_log_replay_rate":
		return "B/s"
	case name == "mysql_last_replication_error_time":
		return ""
	case strings.Contains(name, "percent"), strings.Contains(name, "ratio"):
		return "%"
	case strings.Contains(name, "bytes_sec"), strings.Contains(name, "write_rate"), strings.Contains(name, "growth_rate"):
		return "B/s"
	case strings.Contains(name, "bytes"), strings.Contains(name, "_size"), strings.Contains(name, "backlog"):
		return "B"
	case strings.Contains(name, "_per_min"):
		return "次/min"
	case strings.Contains(name, "_per_sec"), name == "mysql_qps", name == "mysql_tps":
		return "次/s"
	case strings.Contains(name, "seconds"), strings.Contains(name, "_lag"), strings.Contains(name, "_time"):
		return "s"
	case strings.Contains(name, "connections"), strings.Contains(name, "sessions"), strings.Contains(name, "threads"), strings.Contains(name, "transactions"):
		return "个"
	case strings.Contains(name, "count"), strings.Contains(name, "waits"), strings.Contains(name, "deadlocks"):
		return "次"
	default:
		return ""
	}
}

func inferMetricAggregation(name string) string {
	switch {
	case strings.Contains(name, "lag"), strings.Contains(name, "longest"), strings.Contains(name, "slowest"),
		strings.Contains(name, "used_percent"), strings.Contains(name, "busy_percent"), strings.Contains(name, "pressure"):
		return "max"
	case strings.Contains(name, "ratio"), strings.Contains(name, "hit_ratio"), strings.Contains(name, "usage_percent"),
		name == "cpu_usage_percent", name == "mem_usage_percent":
		return "avg"
	default:
		return "sum"
	}
}

func metricIsCounter(name string) bool {
	if name == "mysql_qps" || name == "mysql_tps" {
		return true
	}
	for _, token := range []string{
		"_per_sec", "_per_min", "_delta", "_growth", "_write_rate", "_space_change",
		"mysql_aborted_clients", "mysql_aborted_connects", "mysql_connections_per_sec",
		"mysql_threads_created_per_sec", "mysql_table_scan_count", "mysql_range_scan_count",
		"mysql_select_full_join", "mysql_select_range_check", "mysql_sort_scan",
		"mysql_sort_merge_passes", "mysql_created_tmp_tables", "mysql_created_tmp_disk_tables",
		"mysql_created_tmp_files", "mysql_binlog_cache_use", "mysql_binlog_cache_disk_use",
		"mysql_buffer_pool_read_requests", "mysql_buffer_pool_reads", "mysql_table_cache_overflows",
		"mysql_semisync_wait_count", "mysql_slow_queries_total", "mysql_row_lock_wait_count",
		"mysql_row_lock_wait_time", "mysql_deadlocks", "mysql_slow_query_count",
	} {
		if strings.Contains(name, token) {
			return true
		}
	}
	if strings.HasSuffix(name, "_rate") {
		return true
	}
	return false
}
