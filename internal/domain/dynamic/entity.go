// Package dynamic 定义了动态指标采集领域的实体和配置。
// 动态指标采集允许 Manager 通过 gRPC 心跳流实时推送采集配置给 Agent，
// Agent 按照配置的间隔和方式采集主机及 MySQL 指标并上报。
package dynamic

import "time"

const (
	TaskTypeBuiltin = "builtin"
	TaskTypeCommand = "command"

	ValueTypeBool   = "bool"
	ValueTypeInt    = "int"
	ValueTypeFloat  = "float"
	ValueTypeString = "string"
	ValueTypeMap    = "map"
	ValueTypeArray  = "array"
	ValueTypeRaw    = "raw"
)

// CollectTaskSpec 定义了单个指标的采集规格，包括名称、类型、间隔、超时、命令等。
type CollectTaskSpec struct {
	Name            string            `json:"name"`
	Enabled         bool              `json:"enabled"`
	Type            string            `json:"type"`
	Category        string            `json:"category,omitempty"`
	IntervalSeconds int               `json:"interval_seconds"`
	TimeoutSeconds  int               `json:"timeout_seconds"`
	Command         string            `json:"command,omitempty"`
	Parser          string            `json:"parser,omitempty"`
	Params          map[string]string `json:"params,omitempty"`
	Labels          map[string]string `json:"labels,omitempty"`
}

// DynamicCollectConfig 是动态采集配置的顶层结构，包含版本信息和所有采集任务列表。
type DynamicCollectConfig struct {
	Enabled   bool              `json:"enabled"`
	Version   string            `json:"version"`
	UpdatedAt time.Time         `json:"updated_at"`
	Tasks     []CollectTaskSpec `json:"tasks"`
}

// MetricResult 表示单个指标的采集结果。
type MetricResult struct {
	Name        string            `json:"name"`
	Category    string            `json:"category"`
	Success     bool              `json:"success"`
	ValueType   string            `json:"value_type"`
	Value       any               `json:"value"`
	Labels      map[string]string `json:"labels,omitempty"`
	CollectedAt time.Time         `json:"collected_at"`
	DurationMS  int64             `json:"duration_ms"`
	Error       string            `json:"error,omitempty"`
}

// MetricBatchResult 是批量指标采集结果的包装。
type MetricBatchResult struct {
	AgentID     string         `json:"agent_id"`
	Version     string         `json:"version"`
	GeneratedAt time.Time      `json:"generated_at"`
	Items       []MetricResult `json:"items"`
}

// BuildDefaultDynamicCollectConfig 构建默认的主机级动态采集配置，包含16项基础指标：
// CPU使用率、内存使用率、IO状态、负载均值、NTP偏移、SSH探活、inode使用率、
// MySQL进程存活、端口监听、Socket状态、连接状态、数据盘/Binlog盘/Redo盘/Tmp盘/Undo盘使用率。
func BuildDefaultDynamicCollectConfig() DynamicCollectConfig {
	now := time.Now().UTC()
	names := []string{
		"cpu_usage_percent",
		"mem_usage_percent",
		"io_status",
		"load_average",
		"ntp_offset_ms",
		"ssh_probe",
		"inode_usage",
		"mysql_process_alive",
		"mysql_port_listening",
		"mysql_socket_ok",
		"mysql_connectivity",
		"mysql_data_disk_usage",
		"mysql_binlog_disk_usage",
		"mysql_redo_disk_usage",
		"mysql_tmp_disk_usage",
		"mysql_undo_disk_usage",
	}
	tasks := make([]CollectTaskSpec, 0, len(names))
	for _, name := range names {
		tasks = append(tasks, CollectTaskSpec{
			Name:            name,
			Enabled:         true,
			Type:            TaskTypeBuiltin,
			IntervalSeconds: 1,
			TimeoutSeconds:  1,
		})
	}
	return DynamicCollectConfig{
		Enabled:   true,
		Version:   now.Format("20060102T150405.000000000Z"),
		UpdatedAt: now,
		Tasks:     tasks,
	}
}

// BuildDefaultMySQLDynamicCollectConfig 构建默认的 MySQL 动态采集配置，包含100+项指标，
// 涵盖连接、复制、性能、存储、拓扑、变量等六大类，采集间隔从1秒到300秒不等。
func BuildDefaultMySQLDynamicCollectConfig() DynamicCollectConfig {
	now := time.Now().UTC()
	tasks := make([]CollectTaskSpec, 0, 128)
	add := func(name, category string, interval int, displayName string, params map[string]string) {
		if params == nil {
			params = map[string]string{}
		}
		tasks = append(tasks, CollectTaskSpec{
			Name:            name,
			Enabled:         true,
			Type:            TaskTypeBuiltin,
			Category:        category,
			IntervalSeconds: interval,
			TimeoutSeconds:  1,
			Params:          params,
			Labels:          map[string]string{"display_name": displayName},
		})
	}
	status := func(name, category string, interval int, displayName, variable string) {
		add(name, category, interval, displayName, map[string]string{"status": variable})
	}
	variable := func(name, category string, interval int, displayName, variableName string) {
		add(name, category, interval, displayName, map[string]string{"variable": variableName})
	}

	status("mysql_threads_connected", "connection", 1, "当前连接数", "Threads_connected")
	status("mysql_threads_running", "connection", 1, "当前运行线程数", "Threads_running")
	add("mysql_active_connections", "connection", 1, "活跃连接数", map[string]string{"query": "select count(*) from information_schema.processlist where command <> 'Sleep'"})
	add("mysql_sleep_connections", "connection", 1, "空闲连接数", map[string]string{"query": "select count(*) from information_schema.processlist where command = 'Sleep'"})
	add("mysql_process_alive", "connection", 1, "MySQL进程状态", nil)
	add("mysql_port_listening", "connection", 1, "MySQL端口监听状态", nil)
	add("mysql_socket_ok", "connection", 1, "MySQL Socket状态", nil)
	add("mysql_probe", "connection", 1, "MySQL探活状态", nil)
	add("mysql_connectivity", "connection", 1, "MySQL是否可连接", nil)
	add("mysql_replication_thread_status", "replication", 1, "主从复制线程状态", nil)
	add("mysql_replica_io_thread", "replication", 1, "IO线程状态", map[string]string{"replica_field": "Replica_IO_Running", "slave_field": "Slave_IO_Running"})
	add("mysql_replica_sql_thread", "replication", 1, "SQL线程状态", map[string]string{"replica_field": "Replica_SQL_Running", "slave_field": "Slave_SQL_Running"})
	add("mysql_replication_lag", "replication", 1, "主从延迟", map[string]string{"replica_field": "Seconds_Behind_Source", "slave_field": "Seconds_Behind_Master"})
	add("mysql_seconds_behind_master", "replication", 1, "Seconds_Behind_Master", map[string]string{"replica_field": "Seconds_Behind_Source", "slave_field": "Seconds_Behind_Master"})
	variable("mysql_server_id", "topology", 1, "server_id", "server_id")
	variable("mysql_read_only", "variables", 1, "只读状态", "read_only")
	variable("mysql_super_read_only", "variables", 1, "Super Read Only状态", "super_read_only")
	add("mysql_role", "topology", 1, "角色状态", nil)
	add("mysql_semisync_status", "replication", 1, "半同步状态", nil)
	add("mysql_lock_wait_sessions", "performance", 1, "锁等待会话数", map[string]string{"query": "select count(*) from information_schema.innodb_trx where trx_state = 'LOCK WAIT'"})
	add("mysql_blocked_sessions", "performance", 1, "被阻塞会话数", map[string]string{"query": "select count(*) from information_schema.innodb_trx where trx_state = 'LOCK WAIT'"})
	status("mysql_row_lock_waits_current", "performance", 1, "当前行锁等待数", "Innodb_row_lock_current_waits")
	add("mysql_metadata_lock_waits", "performance", 1, "元数据锁等待数", map[string]string{"query": "select count(*) from performance_schema.metadata_locks where lock_status = 'PENDING'"})
	add("mysql_running_ddl_sessions", "performance", 1, "正在执行DDL的会话数", map[string]string{"query": "select count(*) from information_schema.processlist where info regexp '^[[:space:]]*(alter|create|drop|truncate|rename)[[:space:]]'"})
	add("mysql_active_transactions", "performance", 1, "活跃事务数", map[string]string{"query": "select count(*) from information_schema.innodb_trx"})
	add("mysql_longest_transaction_seconds", "performance", 1, "事务最长运行时间", map[string]string{"query": "select coalesce(max(timestampdiff(second, trx_started, now())),0) from information_schema.innodb_trx"})

	status("mysql_max_used_connections", "connection", 5, "历史最大连接数", "Max_used_connections")
	status("mysql_aborted_clients", "connection", 5, "异常断开连接次数", "Aborted_clients")
	status("mysql_aborted_clients_delta", "connection", 5, "异常断开连接增量", "Aborted_clients")
	status("mysql_aborted_connects", "connection", 5, "失败连接次数", "Aborted_connects")
	status("mysql_aborted_connects_delta", "connection", 5, "失败连接增量", "Aborted_connects")
	status("mysql_connections_per_sec", "connection", 5, "每秒新建连接数", "Connections")
	status("mysql_threads_created_per_sec", "connection", 5, "每秒断开连接数", "Threads_created")
	add("mysql_connection_usage_percent", "connection", 5, "连接使用率", nil)
	status("mysql_connection_spike_count", "connection", 5, "连接突增次数", "Connections")
	add("mysql_long_transaction_connections", "connection", 5, "长事务连接数", map[string]string{"query": "select count(*) from information_schema.innodb_trx where timestampdiff(second, trx_started, now()) > 300"})
	add("mysql_long_sleep_connections", "connection", 5, "长时间Sleep连接数", map[string]string{"query": "select count(*) from information_schema.processlist where command = 'Sleep' and time > 300"})
	status("mysql_qps", "performance", 5, "QPS", "Questions")
	status("mysql_tps", "performance", 5, "TPS", "Com_commit")
	status("mysql_com_commit_per_sec", "performance", 5, "每秒提交事务数", "Com_commit")
	status("mysql_com_rollback_per_sec", "performance", 5, "每秒回滚事务数", "Com_rollback")
	status("mysql_select_per_sec", "performance", 5, "每秒Select次数", "Com_select")
	status("mysql_insert_per_sec", "performance", 5, "每秒Insert次数", "Com_insert")
	status("mysql_update_per_sec", "performance", 5, "每秒Update次数", "Com_update")
	status("mysql_delete_per_sec", "performance", 5, "每秒Delete次数", "Com_delete")
	status("mysql_ddl_per_sec", "performance", 5, "每秒DDL次数", "Com_alter_table")
	status("mysql_rows_sent_per_sec", "performance", 5, "每秒返回行数", "Innodb_rows_read")
	status("mysql_rows_examined_per_sec", "performance", 5, "每秒扫描行数", "Handler_read_rnd_next")
	status("mysql_table_scan_count", "performance", 5, "全表扫描次数", "Handler_read_rnd_next")
	status("mysql_range_scan_count", "performance", 5, "范围扫描次数", "Handler_read_next")
	add("mysql_table_scan_ratio", "performance", 5, "全表扫描占比", nil)
	status("mysql_select_full_join", "performance", 5, "Join全表扫描次数", "Select_full_join")
	status("mysql_select_range_check", "performance", 5, "Join范围检查次数", "Select_range_check")
	add("mysql_join_full_scan_ratio", "performance", 5, "Join全表扫描占比", nil)
	status("mysql_sort_scan", "performance", 5, "排序执行次数", "Sort_scan")
	status("mysql_sort_merge_passes", "performance", 5, "排序归并次数", "Sort_merge_passes")
	status("mysql_created_tmp_tables", "performance", 5, "临时表创建次数", "Created_tmp_tables")
	status("mysql_created_tmp_disk_tables", "performance", 5, "临时磁盘表创建次数", "Created_tmp_disk_tables")
	add("mysql_tmp_disk_table_ratio", "performance", 5, "临时磁盘表占比", nil)
	status("mysql_created_tmp_files", "performance", 5, "临时文件创建次数", "Created_tmp_files")
	status("mysql_tmp_file_growth_rate", "performance", 5, "临时文件增长速率", "Created_tmp_files")
	status("mysql_binlog_cache_use", "storage", 5, "Binlog cache使用次数", "Binlog_cache_use")
	status("mysql_binlog_cache_disk_use", "storage", 5, "Binlog cache落盘次数", "Binlog_cache_disk_use")
	add("mysql_binlog_cache_disk_ratio", "storage", 5, "Binlog cache落盘占比", nil)
	status("mysql_binlog_write_rate", "storage", 5, "Binlog写入速率", "Binlog_cache_use")
	add("mysql_relay_log_growth_rate", "replication", 5, "Relay Log增长速率", nil)
	add("mysql_master_binlog_rate", "replication", 5, "主库Binlog产生速率", nil)
	add("mysql_relay_log_replay_rate", "replication", 5, "从库Relay Log回放速率", nil)
	status("mysql_buffer_pool_pages_total", "storage", 5, "Buffer Pool总页数", "Innodb_buffer_pool_pages_total")
	status("mysql_buffer_pool_pages_free", "storage", 5, "Buffer Pool空闲页数", "Innodb_buffer_pool_pages_free")
	add("mysql_buffer_pool_usage_percent", "storage", 5, "Buffer Pool使用率", nil)
	status("mysql_buffer_pool_pages_dirty", "storage", 5, "Buffer Pool脏页数", "Innodb_buffer_pool_pages_dirty")
	add("mysql_buffer_pool_dirty_ratio", "storage", 5, "Buffer Pool脏页比例", nil)
	status("mysql_buffer_pool_read_requests", "storage", 5, "Buffer Pool逻辑读次数", "Innodb_buffer_pool_read_requests")
	status("mysql_buffer_pool_reads", "storage", 5, "Buffer Pool物理读次数", "Innodb_buffer_pool_reads")
	add("mysql_buffer_pool_hit_ratio", "storage", 5, "Buffer Pool命中率", nil)
	status("mysql_logical_reads_per_sec", "storage", 5, "每秒逻辑读", "Innodb_buffer_pool_read_requests")
	status("mysql_physical_reads_per_sec", "storage", 5, "每秒物理读", "Innodb_buffer_pool_reads")
	status("mysql_dirty_pages_write_per_sec", "storage", 5, "每秒脏页写出", "Innodb_buffer_pool_pages_flushed")
	status("mysql_fsync_per_sec", "storage", 5, "每秒fsync次数", "Innodb_data_fsyncs")
	status("mysql_redo_write_rate", "storage", 5, "Redo写入速率", "Innodb_os_log_written")
	add("mysql_undo_growth_rate", "storage", 5, "Undo增长速率", nil)
	add("mysql_checkpoint_pressure", "storage", 5, "Checkpoint压力", nil)
	add("mysql_background_flush_pressure", "storage", 5, "后台刷盘压力", nil)
	status("mysql_open_files", "storage", 5, "Open_files", "Open_files")
	add("mysql_open_files_usage_percent", "storage", 5, "文件句柄使用率", nil)
	status("mysql_open_files_growth_trend", "storage", 5, "文件句柄增长趋势", "Open_files")
	status("mysql_open_tables", "storage", 5, "Open_tables", "Open_tables")
	add("mysql_table_cache_usage_percent", "storage", 5, "表缓存使用率", nil)
	status("mysql_table_cache_overflows", "storage", 5, "表缓存溢出次数", "Opened_tables")
	add("mysql_table_cache_overflow_ratio", "storage", 5, "表缓存溢出率", nil)
	add("mysql_table_definition_cache_usage", "storage", 5, "表定义缓存使用情况", nil)
	add("mysql_internal_tmp_disk_table_ratio", "performance", 5, "内部临时表落盘率", nil)
	add("mysql_memory_tmp_to_disk_ratio", "performance", 5, "内存临时表转磁盘表比例", nil)
	add("mysql_replication_lag_change_rate", "replication", 5, "主从延迟变化速率", nil)
	add("mysql_relay_log_backlog", "replication", 5, "Relay Log堆积量", nil)
	add("mysql_replica_catchup_status", "replication", 5, "从库追平状态", nil)
	status("mysql_semisync_wait_count", "replication", 5, "半同步等待次数", "Rpl_semi_sync_master_yes_tx")
	add("mysql_thread_cache_hit_ratio", "performance", 5, "线程缓存命中率", nil)

	add("mysql_top_source_ip_connections", "connection", 10, "高频来源IP连接数", map[string]string{"query": "select coalesce(host,'') as host, count(*) from information_schema.processlist group by host order by count(*) desc limit 10"})
	add("mysql_top_source_user_connections", "connection", 10, "高频来源用户连接数", map[string]string{"query": "select coalesce(user,'') as user, count(*) from information_schema.processlist group by user order by count(*) desc limit 10"})
	status("mysql_slow_queries_per_min", "performance", 10, "慢SQL每分钟新增数", "Slow_queries")
	status("mysql_slow_queries_total", "performance", 10, "慢SQL总数", "Slow_queries")
	add("mysql_slowest_sql_seconds", "performance", 10, "最慢SQL耗时", nil)
	add("mysql_slow_sql_total_seconds", "performance", 10, "慢SQL总耗时", nil)
	add("mysql_slow_sql_topn_trend", "performance", 10, "慢SQL TopN数量趋势", nil)
	add("mysql_history_list_length", "storage", 10, "History List Length", nil)
	add("mysql_purge_backlog_length", "storage", 10, "Purge积压长度", nil)
	add("mysql_uncommitted_transactions", "performance", 10, "未提交事务数", map[string]string{"query": "select count(*) from information_schema.innodb_trx"})
	add("mysql_large_transaction_count", "performance", 10, "大事务数量", map[string]string{"query": "select count(*) from information_schema.innodb_trx where timestampdiff(second, trx_started, now()) > 600"})
	status("mysql_row_lock_wait_count", "performance", 10, "行锁等待次数", "Innodb_row_lock_waits")
	status("mysql_row_lock_wait_time", "performance", 10, "行锁等待时间", "Innodb_row_lock_time")
	status("mysql_deadlocks", "performance", 10, "死锁次数", "Innodb_deadlocks")
	status("mysql_deadlocks_delta", "performance", 10, "死锁次数增量", "Innodb_deadlocks")
	add("mysql_blocking_chain_length", "performance", 10, "阻塞链长度", nil)
	add("mysql_max_blocking_seconds", "performance", 10, "最大阻塞时长", nil)
	add("mysql_join_buffer_pressure", "performance", 10, "Join Buffer使用压力", nil)
	add("mysql_sort_buffer_pressure", "performance", 10, "Sort Buffer使用压力", nil)
	add("mysql_read_buffer_pressure", "performance", 10, "Read Buffer使用压力", nil)
	add("mysql_read_rnd_buffer_pressure", "performance", 10, "Read Rnd Buffer使用压力", nil)
	add("mysql_replication_error_count", "replication", 10, "复制报错次数", nil)
	add("mysql_gtid_sync_status", "replication", 10, "GTID同步状态", nil)
	add("mysql_master_role_change", "topology", 10, "主库角色变化", nil)

	add("mysql_error_log_size", "storage", 30, "错误日志大小", nil)
	add("mysql_error_log_growth", "storage", 30, "错误日志增长量", nil)
	add("mysql_error_log_growth_per_min", "storage", 30, "每分钟错误日志新增量", nil)
	add("mysql_recent_error_count", "storage", 30, "最近N分钟ERROR数量", nil)
	add("mysql_recent_warning_count", "storage", 30, "最近N分钟WARNING数量", nil)
	add("mysql_error_code_stats", "storage", 30, "错误码分类统计", nil)
	add("mysql_oom_keyword_count", "storage", 30, "OOM关键字次数", nil)
	add("mysql_crash_recovery_keyword_count", "storage", 30, "崩溃恢复关键字次数", nil)
	add("mysql_replication_error_keyword_count", "storage", 30, "复制错误关键字次数", nil)
	add("mysql_disk_full_keyword_count", "storage", 30, "磁盘满关键字次数", nil)
	add("mysql_table_corruption_keyword_count", "storage", 30, "表损坏关键字次数", nil)
	add("mysql_permission_failed_keyword_count", "storage", 30, "权限失败关键字次数", nil)
	add("mysql_slow_sql_hot_db_stats", "performance", 30, "慢SQL热点库统计", nil)
	add("mysql_slow_sql_hot_table_stats", "performance", 30, "慢SQL热点表统计", nil)
	add("mysql_last_replication_error_time", "replication", 30, "最近一次复制错误时间", nil)
	add("mysql_tmp_dir_space_change", "storage", 30, "临时目录空间占用变化", nil)

	add("mysql_tablespace_fragment_total_bytes", "storage", 300, "所有表空间总碎片大小", nil)
	add("mysql_index_data_total_bytes", "storage", 300, "所有索引数据量大小", nil)
	add("mysql_table_data_total_bytes", "storage", 300, "所有数据量大小", nil)
	variable("mysql_slow_query_threshold", "variables", 300, "慢查询阈值", "long_query_time")
	variable("mysql_slow_query_log_enabled", "variables", 300, "慢查询日志是否开启", "slow_query_log")
	add("mysql_slow_query_log_growth", "performance", 300, "慢查询日志增长量", nil)
	status("mysql_slow_query_count", "performance", 300, "慢查询数量", "Slow_queries")
	status("mysql_recent_slow_query_count", "performance", 300, "最近N分钟慢查询数量", "Slow_queries")

	implementedNoParam := mysqlImplementedNoParamCollectors()
	for i := range tasks {
		if len(tasks[i].Params) > 0 {
			continue
		}
		if implementedNoParam[tasks[i].Name] {
			continue
		}
		tasks[i].Enabled = false
		tasks[i].Labels["default_state"] = "disabled"
		tasks[i].Labels["disabled_reason"] = "reserved metric, collector mapping not implemented yet"
	}

	return DynamicCollectConfig{
		Enabled:   true,
		Version:   "mysql-" + now.Format("20060102T150405.000000000Z"),
		UpdatedAt: now,
		Tasks:     tasks,
	}
}

// mysqlImplementedNoParamCollectors 返回已实现的无参数 MySQL 指标采集器列表。
// 未实现的指标会被标记为 disabled，避免 Agent 执行时找不到对应的采集器。
func mysqlImplementedNoParamCollectors() map[string]bool {
	out := map[string]bool{}
	for _, name := range []string{
		"mysql_process_alive",
		"mysql_port_listening",
		"mysql_socket_ok",
		"mysql_probe",
		"mysql_connectivity",
		"mysql_replication_thread_status",
		"mysql_role",
		"mysql_semisync_status",
		"mysql_connection_usage_percent",
		"mysql_table_scan_ratio",
		"mysql_join_full_scan_ratio",
		"mysql_tmp_disk_table_ratio",
		"mysql_binlog_cache_disk_ratio",
		"mysql_buffer_pool_usage_percent",
		"mysql_buffer_pool_dirty_ratio",
		"mysql_buffer_pool_hit_ratio",
		"mysql_open_files_usage_percent",
		"mysql_table_cache_usage_percent",
		"mysql_table_cache_overflow_ratio",
		"mysql_internal_tmp_disk_table_ratio",
		"mysql_memory_tmp_to_disk_ratio",
		"mysql_thread_cache_hit_ratio",
	} {
		out[name] = true
	}
	return out
}
